package aws

import (
	"context"
	"net"
	"sync"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	backoffTestDeadRegion = "me-south-1"
	backoffTestLiveRegion = "us-east-1"
	backoffTestAccount    = "123456789012"
)

// regionListerFake lists one resource per region, except where failIn says the
// connection fails, and records which regions it was asked to list.
type regionListerFake struct {
	fakeAwsService
	failIn func(region string) bool
	mu     sync.Mutex
	called []string
}

func (f *regionListerFake) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	f.mu.Lock()
	f.called = append(f.called, region)
	f.mu.Unlock()
	if f.failIn(region) {
		return nil, &smithy.OperationError{ServiceID: "Fake", OperationName: "List", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &timeoutError{}}}
	}
	return []providers.Resource{{Id: "r-" + region, Region: region}}, nil
}

func (f *regionListerFake) calledRegions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.called...)
}

// freshResourceListBackoff gives the test its own region memory so no state leaks
// between tests through the package-level one.
func freshResourceListBackoff(t *testing.T) {
	t.Helper()
	original := resourceListBackoff
	resourceListBackoff = newRegionBackoff(resourceListUnreachableFailures, resourceListUnreachableCooldown)
	t.Cleanup(func() { resourceListBackoff = original })
}

func listInRegions(t *testing.T, serviceKey string, svc awsService, regions ...string) (providers.ListResourcesResponse, error) {
	t.Helper()
	swapAwsService(t, serviceKey, svc)
	ctx := &targetedFetchTestCtx{ctx: context.Background()}
	account := providers.Account{AccountNumber: backoffTestAccount}
	return (&awsProvider{}).ListResources(ctx, account, providers.ListResourceRequest{ServiceName: serviceKey, Regions: regions})
}

func deadRegionOnly(region string) bool { return region == backoffTestDeadRegion }

// Two services in a row that cannot connect to a region set it aside: the next
// service does not wait on it again, still lists the other regions, and reports the
// set-aside region as skipped so archival leaves its rows alone.
func TestListResourcesSetsAsideRegionAfterTwoServicesFailToConnect(t *testing.T) {
	freshResourceListBackoff(t)

	for _, key := range []string{"zzfirst", "zzsecond"} {
		svc := &regionListerFake{failIn: deadRegionOnly}
		resp, err := listInRegions(t, key, svc, backoffTestDeadRegion, backoffTestLiveRegion)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{backoffTestDeadRegion, backoffTestLiveRegion}, svc.calledRegions(), "%s must still try the region", key)
		assert.Equal(t, []string{backoffTestDeadRegion}, resp.SkippedRegions)
	}

	third := &regionListerFake{failIn: deadRegionOnly}
	resp, err := listInRegions(t, "zzthird", third, backoffTestDeadRegion, backoffTestLiveRegion)
	require.NoError(t, err)
	assert.Equal(t, []string{backoffTestLiveRegion}, third.calledRegions(), "a set-aside region must not be called again")
	assert.Equal(t, []string{backoffTestDeadRegion}, resp.SkippedRegions)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, backoffTestLiveRegion, resp.Items[0].Region)
}

// A service whose only region is set aside must not report "no resources", or
// StoreResources would archive everything it has stored for that service.
func TestListResourcesSetAsideOnlyRegionIsNotAnEmptyResult(t *testing.T) {
	freshResourceListBackoff(t)
	resourceListBackoff.recordUnreachable(backoffTestAccount, backoffTestDeadRegion)
	resourceListBackoff.recordUnreachable(backoffTestAccount, backoffTestDeadRegion)

	svc := &regionListerFake{failIn: func(string) bool { return false }}
	resp, err := listInRegions(t, "zzonly", svc, backoffTestDeadRegion)

	assert.Error(t, err, "an all-skipped listing must surface as an error")
	assert.Empty(t, svc.calledRegions())
	assert.Equal(t, []string{backoffTestDeadRegion}, resp.SkippedRegions)
}

// A region where one service fails to connect but the next one answers is not dead
// for the account (some services are blocked in a region, others are not), so the
// answer clears the earlier failure and a later failure starts the count again.
func TestListResourcesSuccessInRegionClearsEarlierFailure(t *testing.T) {
	freshResourceListBackoff(t)

	_, err := listInRegions(t, "zzblocked", &regionListerFake{failIn: deadRegionOnly}, backoffTestDeadRegion, backoffTestLiveRegion)
	require.NoError(t, err)
	_, err = listInRegions(t, "zzanswers", &regionListerFake{failIn: func(string) bool { return false }}, backoffTestDeadRegion)
	require.NoError(t, err)
	_, err = listInRegions(t, "zzblockedagain", &regionListerFake{failIn: deadRegionOnly}, backoffTestDeadRegion, backoffTestLiveRegion)
	require.NoError(t, err)

	assert.False(t, resourceListBackoff.shouldSkip(backoffTestAccount, backoffTestDeadRegion))
}
