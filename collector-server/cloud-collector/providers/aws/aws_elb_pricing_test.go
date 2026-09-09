package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
)

// TestElbPriceFiltersUseRegionCode pins the filter that replaced the hand-kept
// region -> location table. location required a lookup; regionCode does not, so
// a region nobody has added to a map still prices correctly.
func TestElbPriceFiltersUseRegionCode(t *testing.T) {
	f := elbPriceFilters("us-east-1")

	assert.Equal(t, "us-east-1", f["regionCode"])
	assert.Equal(t, "Load Balancer", f["productFamily"])

	_, hasLocation := f["location"]
	assert.False(t, hasLocation,
		"location is what needed the lookup table that silently priced unmapped regions at $0")

	// Present-but-empty is meaningful: getAvailableInstancesFromPricing skips
	// empty values AND uses the key's presence to suppress its default Linux
	// filter. Load balancers have no OS, so both behaviours are required.
	osFilter, ok := f["operatingSystem"]
	assert.True(t, ok, "the key must exist to suppress the default Linux filter")
	assert.Empty(t, osFilter)
}

// TestElbPriceFiltersCoverPreviouslyUnmappedRegions walks the regions the
// deleted table omitted. Prices for these were verified against the live
// Pricing API (see the PR body) — e.g. il-central-1 returns
// ILC1-LoadBalancerUsage at $0.0294/hr, which the old code reported as $0.
func TestElbPriceFiltersCoverPreviouslyUnmappedRegions(t *testing.T) {
	for _, region := range []string{
		"ap-south-2", "il-central-1", "me-central-1", "me-south-1",
		"af-south-1", "eu-south-1", "eu-south-2", "eu-central-2",
		"ap-east-1", "ap-southeast-3", "ap-southeast-4", "ca-west-1",
	} {
		t.Run(region, func(t *testing.T) {
			assert.Equal(t, region, elbPriceFilters(region)["regionCode"],
				"no lookup table means no region can be silently missing")
		})
	}
}

// TestGetElbPriceRejectsEmptyRegion: an empty region would otherwise become an
// unfiltered price query. It must be an error, never a 0 that reads as free.
func TestGetElbPriceRejectsEmptyRegion(t *testing.T) {
	price, err := getElbPrice(aws.Config{}, "")
	assert.Error(t, err)
	assert.Zero(t, price)
}
