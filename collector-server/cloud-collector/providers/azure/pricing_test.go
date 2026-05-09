package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"
)

func TestPricingCache(t *testing.T) {
	cache := GetPricingCache()

	t.Run("Cache initialization", func(t *testing.T) {
		if cache == nil {
			t.Fatal("PricingCache should not be nil")
		}
		if !cache.IsEnabled() {
			t.Error("PricingCache should be enabled by default")
		}
	})

	t.Run("Enable/Disable pricing", func(t *testing.T) {
		cache.SetEnabled(false)
		if cache.IsEnabled() {
			t.Error("PricingCache should be disabled")
		}
		cache.SetEnabled(true)
		if !cache.IsEnabled() {
			t.Error("PricingCache should be enabled")
		}
	})

	t.Run("Set TTL", func(t *testing.T) {
		cache.SetTTL(12 * time.Hour)
		stats := cache.GetCacheStats()
		if stats["ttl_hours"].(float64) != 12.0 {
			t.Errorf("Expected TTL to be 12 hours, got %v", stats["ttl_hours"])
		}
		// Reset to default
		cache.SetTTL(24 * time.Hour)
	})

	t.Run("Clear cache", func(t *testing.T) {
		cache.ClearCache()
		stats := cache.GetCacheStats()
		if stats["total_entries"].(int) != 0 {
			t.Errorf("Expected 0 cache entries after clear, got %d", stats["total_entries"])
		}
	})
}

func TestGetVMPrice(t *testing.T) {
	cache := GetPricingCache()
	cache.SetEnabled(true)

	ctx := providers.NewCloudProviderContext(context.Background())

	t.Run("Fetch VM price - may succeed or fail depending on network", func(t *testing.T) {
		// This test may fail if there's no internet connection or Azure API is down
		// We'll just check it doesn't panic
		price, err := cache.GetVMPrice(ctx, "Standard_D2s_v5", "eastus")
		if err != nil {
			t.Logf("VM price fetch failed (expected if no network): %v", err)
		} else {
			t.Logf("VM price fetched successfully: $%.2f/month", price)
			if price <= 0 {
				t.Error("Price should be greater than 0")
			}
		}
	})

	t.Run("Cache hit on second call", func(t *testing.T) {
		// Clear cache first
		cache.ClearCache()

		// First call
		price1, err1 := cache.GetVMPrice(ctx, "Standard_D2s_v5", "eastus")

		// Second call should hit cache (if first succeeded)
		if err1 == nil {
			price2, err2 := cache.GetVMPrice(ctx, "Standard_D2s_v5", "eastus")
			if err2 != nil {
				t.Errorf("Second call failed: %v", err2)
			}
			if price1 != price2 {
				t.Errorf("Prices don't match: %v vs %v", price1, price2)
			}

			stats := cache.GetCacheStats()
			if stats["total_entries"].(int) != 1 {
				t.Errorf("Expected 1 cache entry, got %d", stats["total_entries"])
			}
		}
	})
}

func TestGetDiskPrice(t *testing.T) {
	cache := GetPricingCache()
	cache.SetEnabled(true)

	ctx := providers.NewCloudProviderContext(context.Background())

	t.Run("Fetch disk price - may succeed or fail depending on network", func(t *testing.T) {
		price, err := cache.GetDiskPrice(ctx, "Premium_LRS", "eastus", 128.0)
		if err != nil {
			t.Logf("Disk price fetch failed (expected if no network): %v", err)
		} else {
			t.Logf("Disk price fetched successfully: $%.2f/month for 128GB", price)
			if price <= 0 {
				t.Error("Price should be greater than 0")
			}
		}
	})
}

func TestGetVMCostFallback(t *testing.T) {
	tests := []struct {
		vmSize       string
		expectedCost float64
	}{
		{"Standard_B_2ms", 30.0},   // Burstable
		{"Standard_D2s_v5", 150.0}, // General Purpose v5
		{"Standard_D4s_v4", 170.0}, // General Purpose v4
		{"Standard_E4s_v3", 250.0}, // Memory Optimized
		{"Standard_F4s", 140.0},    // Compute Optimized
		{"UnknownVM", 100.0},       // Default
	}

	for _, tt := range tests {
		t.Run(tt.vmSize, func(t *testing.T) {
			cost := getVMCostFallback(tt.vmSize)
			if cost != tt.expectedCost {
				t.Errorf("Expected cost %v for %s, got %v", tt.expectedCost, tt.vmSize, cost)
			}
		})
	}
}

func TestGetDiskMonthlyCostFallback(t *testing.T) {
	tests := []struct {
		sku      string
		sizeGB   float64
		expected float64
	}{
		{"Premium_LRS", 128.0, 128.0 * 0.154},
		{"StandardSSD_LRS", 128.0, 128.0 * 0.075},
		{"Standard_LRS", 128.0, 128.0 * 0.04},
	}

	for _, tt := range tests {
		t.Run(tt.sku, func(t *testing.T) {
			cost := getDiskMonthlyCostFallback(tt.sku, tt.sizeGB)
			if cost != tt.expected {
				t.Errorf("Expected cost %v for %s %vGB, got %v", tt.expected, tt.sku, tt.sizeGB, cost)
			}
		})
	}
}

func TestMapDiskSKUToProductName(t *testing.T) {
	tests := []struct {
		sku      string
		expected string
	}{
		{"Premium_LRS", "Premium SSD Managed Disks"},
		{"StandardSSD_LRS", "Standard SSD Managed Disks"},
		{"Standard_LRS", "Standard HDD Managed Disks"},
		{"PremiumV2_LRS", "Premium SSD v2 Managed Disks"},
		{"UltraSSD_LRS", "Ultra Disks"},
		{"Unknown_SKU", "Managed Disks"},
	}

	for _, tt := range tests {
		t.Run(tt.sku, func(t *testing.T) {
			result := mapDiskSKUToProductName(tt.sku)
			if result != tt.expected {
				t.Errorf("Expected %s for %s, got %s", tt.expected, tt.sku, result)
			}
		})
	}
}
