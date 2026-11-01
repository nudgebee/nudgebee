package aws

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetCloudFrontWafShare(t *testing.T) {
	t.Helper()
	reset := func() {
		cloudFrontWafMu.Lock()
		cloudFrontWafEntries = map[string]cloudFrontWafEntry{}
		cloudFrontWafMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func countingCloudFrontList(calls *atomic.Int32, err error) func() ([]providers.Resource, bool, error) {
	return listing(calls, true, err)
}

func listing(calls *atomic.Int32, complete bool, err error) func() ([]providers.Resource, bool, error) {
	return func() ([]providers.Resource, bool, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond) // keep the first call in flight while the others arrive
		if err != nil {
			return nil, false, err
		}
		return []providers.Resource{{Id: "acl-1", Region: "global", Meta: map[string]any{"Name": "acl-1"}, Tags: map[string][]string{"team": {"sre"}}}}, complete, nil
	}
}

// A sync lists WAF once per region, in parallel; the CloudFront scope is the same
// for every region, so it must be listed once, not once per region.
func TestCloudFrontWafScopeListedOncePerSync(t *testing.T) {
	resetCloudFrontWafShare(t)
	account := providers.Account{ID: "cloud-account-1", AccountNumber: "111111111111"}
	var calls atomic.Int32

	var wg sync.WaitGroup
	for range 17 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
			assert.NoError(t, err)
			assert.Len(t, got, 1)
		}()
	}
	wg.Wait()

	got, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, int32(1), calls.Load(), "regions of one sync must share one CloudFront listing")
}

func TestCloudFrontWafScopeFailureIsNotShared(t *testing.T) {
	resetCloudFrontWafShare(t)
	account := providers.Account{ID: "cloud-account-2", AccountNumber: "222222222222"}
	var calls atomic.Int32

	_, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, errors.New("throttled")))
	require.Error(t, err)

	got, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, int32(2), calls.Load(), "a failed listing must not be reused")
}

func TestCloudFrontWafScopeListedAgainAfterTTL(t *testing.T) {
	resetCloudFrontWafShare(t)
	account := providers.Account{ID: "cloud-account-3", AccountNumber: "333333333333"}
	var calls atomic.Int32

	_, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	key := account.ID + "/" + account.AccountNumber
	cloudFrontWafMu.Lock()
	entry := cloudFrontWafEntries[key]
	entry.fetchedAt = time.Now().Add(-cloudFrontWafTTL)
	cloudFrontWafEntries[key] = entry
	cloudFrontWafMu.Unlock()

	_, err = listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "the next sync must list again")

	_, err = listCloudFrontWafScope(providers.Account{ID: "cloud-account-4", AccountNumber: "444444444444"}, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load(), "accounts must not share a listing")

	// Two cloud accounts on one AWS account can hold different permissions.
	_, err = listCloudFrontWafScope(providers.Account{ID: "cloud-account-5", AccountNumber: account.AccountNumber}, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	assert.Equal(t, int32(4), calls.Load(), "cloud accounts on one AWS account must not share a listing")
}

// A list call that failed inside the listing (logged, not returned) leaves it
// partial; sharing it would hand every region of the sync the gaps.
func TestCloudFrontWafScopePartialListingIsNotShared(t *testing.T) {
	resetCloudFrontWafShare(t)
	account := providers.Account{ID: "cloud-account-6", AccountNumber: "666666666666"}
	var calls atomic.Int32

	_, err := listCloudFrontWafScope(account, listing(&calls, false, nil))
	require.NoError(t, err)
	_, err = listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)

	assert.Equal(t, int32(2), calls.Load(), "a partial listing must not be reused")
}

// The store writes into each resource's Meta; callers sharing one listing must not
// write into the same map.
func TestCloudFrontWafScopeCallersGetTheirOwnMaps(t *testing.T) {
	resetCloudFrontWafShare(t)
	account := providers.Account{ID: "cloud-account-7", AccountNumber: "777777777777"}
	var calls atomic.Int32

	first, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	first[0].Meta["nb_source"] = "api"
	first[0].Tags["owner"] = []string{"x"}

	second, err := listCloudFrontWafScope(account, countingCloudFrontList(&calls, nil))
	require.NoError(t, err)
	assert.NotContains(t, second[0].Meta, "nb_source")
	assert.NotContains(t, second[0].Tags, "owner")
}
