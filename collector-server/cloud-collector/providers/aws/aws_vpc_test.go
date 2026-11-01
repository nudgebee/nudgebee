package aws

import (
	"errors"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// The $295-on-$25 bug (#38367): the idle-EIP recommendation was emitted once
// per existing VPC resource, so one idle address became 82 findings. The
// recommendation must target the address's own elastic-ip resource instead.
func TestIdleElasticIPRecommendationTargetsTheAddressItself(t *testing.T) {
	rec, ok := idleElasticIPRecommendation(ec2types.Address{
		AllocationId: aws.String("eipalloc-123"),
		PublicIp:     aws.String("52.200.219.207"),
	}, "us-east-1", 3.6, eipPricingSourceAPI)
	assert.True(t, ok)
	assert.Equal(t, "eipalloc-123", rec.ResourceId)
	assert.Equal(t, getAwsServiceResourceType(ServiceNameVPC, "elastic-ip"), rec.ResourceType)
	assert.Equal(t, ServiceNameVPC, rec.ResourceServiceName)
	assert.Equal(t, "us-east-1", rec.ResourceRegion)
	assert.Equal(t, "52.200.219.207", rec.Data["elastic_ip"])
	assert.InDelta(t, 3.6, rec.Savings, 1e-9)
}

func TestIdleElasticIPRecommendationSkipsAssociatedAddresses(t *testing.T) {
	_, ok := idleElasticIPRecommendation(ec2types.Address{
		AllocationId:  aws.String("eipalloc-123"),
		AssociationId: aws.String("eipassoc-456"),
		PublicIp:      aws.String("52.200.219.207"),
	}, "us-east-1", 3.6, eipPricingSourceAPI)
	assert.False(t, ok)
}

// EC2-Classic addresses have no AllocationId; GetResources stores them under
// their public IP, and the recommendation must use the same identity to link.
func TestIdleElasticIPRecommendationFallsBackToPublicIpForClassic(t *testing.T) {
	rec, ok := idleElasticIPRecommendation(ec2types.Address{
		PublicIp: aws.String("52.200.219.207"),
	}, "us-east-1", 3.6, eipPricingSourceFlatRate)
	assert.True(t, ok)
	assert.Equal(t, "52.200.219.207", rec.ResourceId)
}

func TestIdleElasticIPRecommendationSkipsAddressesWithNoIdentity(t *testing.T) {
	_, ok := idleElasticIPRecommendation(ec2types.Address{}, "us-east-1", 3.6, eipPricingSourceFlatRate)
	assert.False(t, ok)
}

// Lockstep with GetResources, which skips an address with no PublicIp: without a
// stored elastic-ip row, a recommendation keyed on AllocationId alone would be
// orphaned (no resource to link to), so the helper must skip it too.
func TestIdleElasticIPRecommendationSkipsAddressesWithNoPublicIp(t *testing.T) {
	_, ok := idleElasticIPRecommendation(ec2types.Address{
		AllocationId: aws.String("eipalloc-123"),
	}, "us-east-1", 3.6, eipPricingSourceFlatRate)
	assert.False(t, ok)
}

func TestEipScanRegionsDeduplicatesAndSeedsTheDefaultRegion(t *testing.T) {
	regions := eipScanRegions("us-east-1", []providers.Resource{
		{Region: "eu-west-1"},
		{Region: "eu-west-1"},
		{Region: "us-east-1"},
		{Region: ""},
		{Region: "ap-south-1"},
	})
	assert.Equal(t, []string{"ap-south-1", "eu-west-1", "us-east-1"}, regions)
}

func TestEipScanRegionsWithNoResourcesStillCoversTheDefaultRegion(t *testing.T) {
	assert.Equal(t, []string{"us-east-1"}, eipScanRegions("us-east-1", nil))
}
