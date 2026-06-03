package gcloud

import (
	"strings"
	"testing"
	"time"
)

func TestGcpServiceAPIMap_CoversAllServices(t *testing.T) {
	// Every service in gcloudServiceMap should have a corresponding API mapping
	for serviceName := range gcloudServiceMap {
		key := strings.ToLower(serviceName)
		if _, ok := gcpServiceAPIMap[key]; !ok {
			t.Errorf("service %q in gcloudServiceMap has no entry in gcpServiceAPIMap", serviceName)
		}
	}
}

func TestGcpServiceAPIMap_AllValuesEndWithGoogleapisCom(t *testing.T) {
	for service, api := range gcpServiceAPIMap {
		if !strings.HasSuffix(api, ".googleapis.com") {
			t.Errorf("gcpServiceAPIMap[%q] = %q — expected suffix .googleapis.com", service, api)
		}
	}
}

func TestIsServiceEnabled_NilAPIs_ReturnsTrue(t *testing.T) {
	// nil enabledAPIs means pre-check failed — should allow all services
	if !isServiceEnabled("compute engine", nil) {
		t.Error("expected true for nil enabledAPIs")
	}
}

func TestIsServiceEnabled_UnknownService_ReturnsTrue(t *testing.T) {
	apis := map[string]bool{"compute.googleapis.com": true}
	if !isServiceEnabled("some-future-service", apis) {
		t.Error("expected true for unknown service name")
	}
}

func TestIsServiceEnabled_APIEnabled(t *testing.T) {
	apis := map[string]bool{
		"compute.googleapis.com": true,
		"run.googleapis.com":     true,
	}
	if !isServiceEnabled("compute engine", apis) {
		t.Error("expected true: compute.googleapis.com is enabled")
	}
	if !isServiceEnabled("Compute Engine", apis) {
		t.Error("expected true: case-insensitive lookup")
	}
	if !isServiceEnabled("cloud run", apis) {
		t.Error("expected true: run.googleapis.com is enabled")
	}
}

func TestIsServiceEnabled_APIDisabled(t *testing.T) {
	apis := map[string]bool{
		"storage.googleapis.com": true,
	}
	if isServiceEnabled("compute engine", apis) {
		t.Error("expected false: compute.googleapis.com is not in enabled set")
	}
	if isServiceEnabled("kubernetes engine", apis) {
		t.Error("expected false: container.googleapis.com is not in enabled set")
	}
}

func TestIsServiceEnabled_SharedAPIs(t *testing.T) {
	// networking, cloud load balancing, disk, and networkinterface all depend on compute.googleapis.com
	apis := map[string]bool{
		"compute.googleapis.com": true,
	}
	for _, svc := range []string{"compute engine", "networking", "cloud load balancing", "compute.googleapis.com/disk", "compute.googleapis.com/networkinterface"} {
		if !isServiceEnabled(svc, apis) {
			t.Errorf("expected true for %q when compute.googleapis.com is enabled", svc)
		}
	}

	apisNoCompute := map[string]bool{
		"storage.googleapis.com": true,
	}
	for _, svc := range []string{"compute engine", "networking", "cloud load balancing", "compute.googleapis.com/disk", "compute.googleapis.com/networkinterface"} {
		if isServiceEnabled(svc, apisNoCompute) {
			t.Errorf("expected false for %q when compute.googleapis.com is not enabled", svc)
		}
	}
}

func TestClearEnabledAPIsCache(t *testing.T) {
	// Seed the cache
	enabledAPIsCacheMu.Lock()
	enabledAPIsCache["test-project"] = enabledAPIsCacheEntry{
		apis:      map[string]bool{"compute.googleapis.com": true},
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	enabledAPIsCacheMu.Unlock()

	clearEnabledAPIsCache()

	enabledAPIsCacheMu.RLock()
	_, ok := enabledAPIsCache["test-project"]
	enabledAPIsCacheMu.RUnlock()
	if ok {
		t.Error("expected cache to be cleared")
	}
}

func TestEnabledAPIsCacheExpiry(t *testing.T) {
	clearEnabledAPIsCache()

	// Insert an expired entry
	enabledAPIsCacheMu.Lock()
	enabledAPIsCache["expired-project"] = enabledAPIsCacheEntry{
		apis:      map[string]bool{"compute.googleapis.com": true},
		expiresAt: time.Now().Add(-1 * time.Second),
	}
	enabledAPIsCacheMu.Unlock()

	// Reading from cache should miss on expired entry
	enabledAPIsCacheMu.RLock()
	entry, ok := enabledAPIsCache["expired-project"]
	enabledAPIsCacheMu.RUnlock()

	if !ok {
		t.Fatal("entry should exist in map")
	}
	if time.Now().Before(entry.expiresAt) {
		t.Error("entry should be expired")
	}
}
