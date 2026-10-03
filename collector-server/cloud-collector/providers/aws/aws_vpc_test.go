package aws

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

const eipFlatMonthly = awsPublicIPv4HourlyUSD * 24 * 30

func TestIdleElasticIPMonthlyCostUsesTheQuotedPrice(t *testing.T) {
	cost, source := idleElasticIPMonthlyCost([]map[string]interface{}{pricedInstance("0.0050000000")}, nil)
	assert.InDelta(t, 0.005*24*30, cost, 1e-9)
	assert.Equal(t, eipPricingSourceAPI, source)
}

// An empty price list is the failure that shipped 145 idle addresses as $0: the
// lookup succeeded, matched nothing, and the caller never set a cost.
func TestIdleElasticIPMonthlyCostFallsBackWhenNothingMatched(t *testing.T) {
	cost, source := idleElasticIPMonthlyCost(nil, nil)
	assert.InDelta(t, eipFlatMonthly, cost, 1e-9)
	assert.Equal(t, eipPricingSourceFlatRate, source)
}

func TestIdleElasticIPMonthlyCostFallsBackOnLookupError(t *testing.T) {
	cost, source := idleElasticIPMonthlyCost(nil, errors.New("pricing:GetProducts denied"))
	assert.InDelta(t, eipFlatMonthly, cost, 1e-9)
	assert.Equal(t, eipPricingSourceFlatRate, source)
}

// getPricingValue reports a "0.0000000000" USD amount as a successful lookup, so
// a zero-priced product must not be trusted over the flat rate.
func TestIdleElasticIPMonthlyCostFallsBackOnZeroPricedProduct(t *testing.T) {
	cost, source := idleElasticIPMonthlyCost([]map[string]interface{}{pricedInstance("0.0000000000")}, nil)
	assert.InDelta(t, eipFlatMonthly, cost, 1e-9)
	assert.Equal(t, eipPricingSourceFlatRate, source)
}

func TestIdleElasticIPFlatRateIsNeverZero(t *testing.T) {
	assert.Greater(t, eipFlatMonthly, 0.0)
}

// The usage type must be the one AWS bills public IPv4 under today; the retired
// ElasticIP:IdleAddress line matches no product and was the root of the $0 rows.
func TestGetUsageTypeBuildsThePublicIPv4IdleLine(t *testing.T) {
	got, err := getUsageType("us-east-1")
	assert.NoError(t, err)
	assert.Equal(t, "USE1-PublicIPv4:IdleAddress", got)
}

func TestGetUsageTypeDistinguishesMumbaiFromSingapore(t *testing.T) {
	mumbai, err := getUsageType("ap-south-1")
	assert.NoError(t, err)
	singapore, err2 := getUsageType("ap-southeast-1")
	assert.NoError(t, err2)
	assert.Equal(t, "APS3-PublicIPv4:IdleAddress", mumbai)
	assert.Equal(t, "APS1-PublicIPv4:IdleAddress", singapore)
	assert.NotEqual(t, mumbai, singapore)
}

// Pins the three filter conditions that each, on their own, made the lookup
// return nothing: a productFamily the product does not have, the helper's
// default operatingSystem=Linux, and the retired usage type.
func TestIdleElasticIPPricingFiltersMatchTheRealProduct(t *testing.T) {
	f := idleElasticIPPricingFilters("USE1-PublicIPv4:IdleAddress")
	assert.Equal(t, "USE1-PublicIPv4:IdleAddress", f["usagetype"])
	pf, ok := f["productFamily"]
	assert.True(t, ok, "productFamily must be present so the helper sees it")
	assert.Empty(t, pf, "and empty, so it is dropped: the product has no productFamily")
	osv, ok := f["operatingSystem"]
	assert.True(t, ok, "operatingSystem must be present to override the Linux default")
	assert.Empty(t, osv, "and empty, so no OS filter is sent")
	assert.NotContains(t, f["usagetype"], "ElasticIP", "the pre-2024 usage type matches no product")
}

func TestGetUsageTypeRejectsUnknownRegion(t *testing.T) {
	_, err := getUsageType("mars-north-1")
	assert.Error(t, err)
}
