package azure

import "testing"

func TestSelectHourlyConsumptionPrice(t *testing.T) {
	items := []RetailPriceItem{
		{RetailPrice: 0.0, UnitOfMeasure: "1 Hour", Type: "Consumption", MeterName: "Standard IPv4 Static Public IP"},
		{RetailPrice: 0.005, UnitOfMeasure: "1 Hour", Type: "DevTestConsumption", MeterName: "Standard IPv4 Static Public IP"},
		{RetailPrice: 0.030, UnitOfMeasure: "1 Hour", Type: "Consumption", MeterName: "Standard IPv4 Static Public IP"},
		{RetailPrice: 0.006, UnitOfMeasure: "1 Hour", Type: "Consumption", MeterName: "Standard IPv4 Static Public IP"},
		{RetailPrice: 1.0, UnitOfMeasure: "1 GB", Type: "Consumption", MeterName: "Standard IPv4 Static Public IP"},
		{RetailPrice: 0.001, UnitOfMeasure: "1 Hour", Type: "Consumption", MeterName: "Dynamic Public IP"},
	}

	// Lowest hourly Consumption price whose meter contains "Static" (skips zero,
	// DevTest, non-hourly, and the non-matching "Dynamic" meter).
	got, ok := selectHourlyConsumptionPrice(items, "Static")
	if !ok || got != 0.006 {
		t.Errorf("with meter filter = (%v, %v), want (0.006, true)", got, ok)
	}

	// Without a meter filter, the cheaper "Dynamic" hourly price wins.
	got, ok = selectHourlyConsumptionPrice(items, "")
	if !ok || got != 0.001 {
		t.Errorf("no meter filter = (%v, %v), want (0.001, true)", got, ok)
	}

	// No hourly items.
	if _, ok := selectHourlyConsumptionPrice([]RetailPriceItem{{RetailPrice: 1, UnitOfMeasure: "1 GB", Type: "Consumption"}}, ""); ok {
		t.Error("expected ok=false when no hourly consumption price present")
	}
}

func TestAzureSkuName(t *testing.T) {
	meta := map[string]any{"sku": map[string]any{"name": "Standard"}}
	if got := azureSkuName(meta); got != "Standard" {
		t.Errorf("azureSkuName = %q, want Standard", got)
	}
	if got := azureSkuName(map[string]any{}); got != "" {
		t.Errorf("azureSkuName(empty) = %q, want empty", got)
	}
}
