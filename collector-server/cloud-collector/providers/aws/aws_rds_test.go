package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAvailableRdsInstances(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")
	cfg, err := getAwsConfigFromAccount(context.Background(), providers.Account{
		AccountNumber: testAWSAccountNumber,
	})
	assert.Nil(t, err)

	instances, err := getAvailableRdsInstances(nil, cfg, "us-east-1", "PostgreSQL", "2 GiB", "1", "", "Single-AZ", rdsPricingPins{})
	assert.Nil(t, err)
	assert.NotNil(t, instances)

	instances, err = getAvailableRdsInstances(nil, cfg, "us-east-1", "PostgreSQL", "8 GiB", "2", "", "Single-AZ", rdsPricingPins{})
	assert.Nil(t, err)
	assert.NotNil(t, instances)

	alternateInsatnces, err := alternateInstancesBasedOnPricing(instances, instances[0])
	assert.Nil(t, err)
	assert.NotNil(t, alternateInsatnces)
}

func TestNormalizeRdsEngineForPricing(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"mysql", "MySQL"},
		{"MYSQL", "MySQL"},
		{"mariadb", "MariaDB"},
		{"postgres", "PostgreSQL"},
		{"postgresql", "PostgreSQL"},
		{"aurora-mysql", "Aurora MySQL"},
		{"aurora-postgresql", "Aurora PostgreSQL"},
		{"oracle-ee", "Oracle"},
		{"oracle-se2", "Oracle"},
		{"oracle-ee-cdb", "Oracle"},
		{"sqlserver-ee", "SQL Server"},
		{"sqlserver-se", "SQL Server"},
		{"sqlserver-ex", "SQL Server"},
		{"sqlserver-web", "SQL Server"},
		{"db2-ae", "Db2"},
		{"db2-se", "Db2"},
		{"", ""},
		// Pre-canonicalized values are also accepted.
		{"PostgreSQL", "PostgreSQL"},
		{"MySQL", "MySQL"},
		// Unknown engines fall through unchanged.
		{"some-future-engine", "some-future-engine"},
	}
	for _, c := range cases {
		got := normalizeRdsEngineForPricing(c.in)
		assert.Equal(t, c.want, got, "input=%q", c.in)
	}
}

func TestSynthesizeRdsInstanceTypeDetailsRoundtripsThroughGetPricingValue(t *testing.T) {
	attrs := map[string]any{
		"databaseEngine":   "MySQL",
		"deploymentOption": "Single-AZ",
		"instanceType":     "db.t4g.micro",
		"memory":           "1 GiB",
		"vcpu":             "2",
	}
	details := synthesizeRdsInstanceTypeDetails(0.016, attrs)

	// product.attributes round-trip
	product, ok := details["product"].(map[string]any)
	assert.True(t, ok)
	a, ok := product["attributes"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "1 GiB", a["memory"])

	// terms shape — must be readable by getPricingValue
	price, err := getPricingValue(details)
	assert.Nil(t, err)
	// Float formatting may not be exact; compare as string for determinism.
	assert.Equal(t, "0.016", strconv.FormatFloat(price, 'f', -1, 64))
}

func TestSynthesizeRdsInstanceTypeDetailsHandlesEmptyAttributes(t *testing.T) {
	details := synthesizeRdsInstanceTypeDetails(0, map[string]any{})
	price, err := getPricingValue(details)
	assert.Nil(t, err)
	assert.Equal(t, 0.0, price)
}

func TestRdsLicenseModelForPricing(t *testing.T) {
	cases := map[string]string{
		"license-included":       "License included",
		"bring-your-own-license": "Bring your own license",
		"general-public-license": "No license required",
		"postgresql-license":     "No license required",
		"":                       "",
		"something-new":          "",
	}
	for in, want := range cases {
		assert.Equal(t, want, rdsLicenseModelForPricing(in), in)
	}
}

func TestRdsEditionForPricing(t *testing.T) {
	cases := map[string]string{
		"oracle-ee":           "Enterprise",
		"oracle-se2":          "Standard Two",
		"oracle-se1":          "Standard One",
		"oracle-se":           "Standard",
		"sqlserver-ee":        "Enterprise",
		"sqlserver-se":        "Standard",
		"sqlserver-ex":        "Express",
		"sqlserver-web":       "Web",
		"custom-oracle-ee":    "Enterprise",
		"custom-sqlserver-se": "Standard",
		"postgres":            "",
		"mysql":               "",
		"aurora-postgresql":   "",
		"Oracle":              "",
	}
	for in, want := range cases {
		assert.Equal(t, want, rdsEditionForPricing(in), in)
	}
}

func rdsSku(price float64, attrs map[string]any) map[string]interface{} {
	return synthesizeRdsInstanceTypeDetails(price, attrs)
}

// The Oracle BYOL/Enterprise case from the live Pricing API: a standard SKU and
// an RDS Custom SKU share every pinned attribute, and Custom costs 20% more.
func TestSelectRdsPricingCandidatesDropsCustomAndOrdersCheapestFirst(t *testing.T) {
	products := []map[string]interface{}{
		rdsSku(0.438, map[string]any{"licenseModel": "License included", "databaseEdition": "Standard Two"}),
		rdsSku(0.205, map[string]any{"licenseModel": "Bring your own license", "databaseEdition": "Enterprise", "deploymentModel": "Custom"}),
		rdsSku(0.171, map[string]any{"licenseModel": "Bring your own license", "databaseEdition": "Enterprise"}),
	}
	got := selectRdsPricingCandidates(products, "oracle-ee")
	if assert.Len(t, got, 2) {
		p0, _ := getPricingValue(got[0])
		p1, _ := getPricingValue(got[1])
		assert.InDelta(t, 0.171, p0, 1e-9, "cheapest standard SKU first")
		assert.InDelta(t, 0.438, p1, 1e-9)
		for _, p := range got {
			assert.False(t, rdsProductIsCustom(p), "no Custom SKU may survive for a standard engine")
		}
	}
}

func TestSelectRdsPricingCandidatesKeepsOnlyCustomForCustomEngines(t *testing.T) {
	products := []map[string]interface{}{
		rdsSku(0.171, map[string]any{"databaseEdition": "Enterprise"}),
		rdsSku(0.205, map[string]any{"databaseEdition": "Enterprise", "deploymentModel": "Custom"}),
	}
	got := selectRdsPricingCandidates(products, "custom-oracle-ee")
	if assert.Len(t, got, 1) {
		assert.True(t, rdsProductIsCustom(got[0]))
	}
}

func TestSelectRdsPricingCandidatesPutsUnpriceableLast(t *testing.T) {
	noTerms := map[string]interface{}{"product": map[string]any{"attributes": map[string]any{}}}
	products := []map[string]interface{}{noTerms, rdsSku(0.3, map[string]any{})}
	got := selectRdsPricingCandidates(products, "postgres")
	if assert.Len(t, got, 2) {
		p0, err := getPricingValue(got[0])
		assert.NoError(t, err)
		assert.InDelta(t, 0.3, p0, 1e-9)
	}
}

func TestSelectRdsPricingCandidatesIsEmptyForEmptyInput(t *testing.T) {
	assert.Empty(t, selectRdsPricingCandidates(nil, "oracle-ee"))
}

func rdsStorageSku(price float64, deployment string) map[string]interface{} {
	return synthesizeRdsInstanceTypeDetails(price, map[string]any{"deploymentOption": deployment})
}

// Live us-east-1 gp3 prices per GB-month: Single-AZ 0.115, Multi-AZ and SQL
// Server Mirror 0.230, readable-standby cluster 0.345 — for every engine.
func rdsStorageCatalogue() []map[string]interface{} {
	return []map[string]interface{}{
		rdsStorageSku(0.345, "Multi-AZ (readable standbys)"),
		rdsStorageSku(0.230, "Multi-AZ"),
		rdsStorageSku(0.115, "Single-AZ"),
		rdsStorageSku(0.230, "Multi-AZ (SQL Server Mirror)"),
	}
}

func TestSelectRdsStoragePriceSingleAZ(t *testing.T) {
	price, distinct, err := selectRdsStoragePrice(rdsStorageCatalogue(), false)
	assert.NoError(t, err)
	assert.InDelta(t, 0.115, price, 1e-9)
	assert.Equal(t, 1, distinct)
}

func TestSelectRdsStoragePriceMultiAZTakesTheCheapestVariant(t *testing.T) {
	price, distinct, err := selectRdsStoragePrice(rdsStorageCatalogue(), true)
	assert.NoError(t, err)
	assert.InDelta(t, 0.230, price, 1e-9, "Multi-AZ / Mirror, not the readable-standby cluster tier")
	assert.Equal(t, 2, distinct, "0.230 and 0.345 both matched the prefix; the caller logs that")
}

func TestSelectRdsStoragePriceIgnoresOtherTopologies(t *testing.T) {
	_, _, err := selectRdsStoragePrice([]map[string]interface{}{rdsStorageSku(0.230, "Multi-AZ")}, false)
	assert.Error(t, err, "a Single-AZ instance must not be priced off Multi-AZ storage")
}

func TestSelectRdsStoragePriceSkipsUnpriceable(t *testing.T) {
	noTerms := map[string]interface{}{"product": map[string]any{"attributes": map[string]any{"deploymentOption": "Single-AZ"}}}
	price, _, err := selectRdsStoragePrice([]map[string]interface{}{noTerms, rdsStorageSku(0.115, "Single-AZ")}, false)
	assert.NoError(t, err)
	assert.InDelta(t, 0.115, price, 1e-9)
}

func TestRdsStorageDeploymentMatches(t *testing.T) {
	assert.True(t, rdsStorageDeploymentMatches("Single-AZ", false))
	assert.False(t, rdsStorageDeploymentMatches("Multi-AZ", false))
	assert.True(t, rdsStorageDeploymentMatches("Multi-AZ", true))
	assert.True(t, rdsStorageDeploymentMatches("Multi-AZ (SQL Server Mirror)", true))
	assert.True(t, rdsStorageDeploymentMatches("Multi-AZ (readable standbys)", true))
	assert.False(t, rdsStorageDeploymentMatches("Single-AZ", true))
}
