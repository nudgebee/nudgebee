package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// pricedInstance builds the minimal AWS Pricing API shape getPricingValue reads:
// terms.OnDemand[*].priceDimensions[*].pricePerUnit.USD.
func pricedInstance(usd string) map[string]interface{} {
	return map[string]interface{}{
		"product": map[string]any{
			"attributes": map[string]any{"currentGeneration": "Yes"},
		},
		"terms": map[string]any{
			"OnDemand": map[string]any{
				"term": map[string]any{
					"priceDimensions": map[string]any{
						"dim": map[string]any{
							"pricePerUnit": map[string]any{"USD": usd},
						},
					},
				},
			},
		},
	}
}

// TestGetPricingValueReturnsZeroWithoutError documents the hazard the guards
// exist for: the pricing API can return a parseable "0.0000000000" USD amount,
// and getPricingValue reports that as a successful lookup. Callers therefore
// cannot use "err == nil" alone to decide a price is usable.
func TestGetPricingValueReturnsZeroWithoutError(t *testing.T) {
	price, err := getPricingValue(pricedInstance("0.0000000000"))
	assert.NoError(t, err, "a zero USD amount parses fine — it is not an error path")
	assert.Zero(t, price, "which is exactly why a > 0 check is needed on top of the error check")
}

// TestAlternateInstancesBasedOnPricingSkipsZeroPricedSku pins the root guard.
// A 0 price means "no usable USD amount", not a free instance. Without the
// guard it sorts to the front as the cheapest alternative, and every caller
// that reads alternates[0] reports the entire current bill as savings.
func TestAlternateInstancesBasedOnPricingSkipsZeroPricedSku(t *testing.T) {
	current := pricedInstance("1.0000000000")
	zero := pricedInstance("0.0000000000")
	real := pricedInstance("0.5000000000")

	got, err := alternateInstancesBasedOnPricing([]map[string]interface{}{zero, real}, current)
	assert.NoError(t, err)

	assert.Len(t, got, 1, "the zero-priced SKU must not be offered as an alternative")
	price, err := getPricingValue(got[0])
	assert.NoError(t, err)
	assert.Equal(t, 0.5, price, "the cheapest USABLE alternative wins, not the unusable 0")
}

// TestAlternateInstancesBasedOnPricingKeepsCheaperRealPrices guards against the
// fix over-correcting: genuinely cheaper instances must still be returned,
// cheapest first.
func TestAlternateInstancesBasedOnPricingKeepsCheaperRealPrices(t *testing.T) {
	current := pricedInstance("1.0000000000")
	got, err := alternateInstancesBasedOnPricing(
		[]map[string]interface{}{pricedInstance("0.8000000000"), pricedInstance("0.2000000000")},
		current,
	)
	assert.NoError(t, err)
	assert.Len(t, got, 2)

	first, _ := getPricingValue(got[0])
	assert.Equal(t, 0.2, first, "cheapest usable alternative sorts first")
}

// TestUsablyPricedInstancesKeepsRealAlternatives pins the failure mode a bare
// `minPrice > 0` guard introduces. Callers pick an alternative by sorting on
// price and reading index 0, so an unpriced SKU sorts to the front; guarding
// only at the end then throws away the whole recommendation even though a
// genuinely cheaper node type was available further down the list.
func TestUsablyPricedInstancesKeepsRealAlternatives(t *testing.T) {
	got := usablyPricedInstances([]map[string]interface{}{
		pricedInstance("0.0000000000"), // unpriced — must not survive
		pricedInstance("0.5000000000"), // real cheaper alternative — must survive
		{"product": map[string]any{}},  // malformed, getPricingValue errors
	})

	assert.Len(t, got, 1, "only the entry with a real USD price survives")
	price, err := getPricingValue(got[0])
	assert.NoError(t, err)
	assert.Equal(t, 0.5, price,
		"the real alternative must still be recommendable when an unpriced SKU is present")
}

func TestUsablyPricedInstancesEmptyWhenNothingPriced(t *testing.T) {
	assert.Empty(t, usablyPricedInstances([]map[string]interface{}{pricedInstance("0.0000000000")}))
	assert.Empty(t, usablyPricedInstances(nil))
}
