package branding

import (
	"testing"

	"nudgebee/services/config"
)

func TestProductNameWithoutResolverIsConfiguredName(t *testing.T) {
	prevName, prevResolver := config.Config.BrandingName, productNameResolver
	t.Cleanup(func() { config.Config.BrandingName, productNameResolver = prevName, prevResolver })

	config.Config.BrandingName, productNameResolver = "Nudgebee", nil
	if got := ProductName(); got != "Nudgebee" {
		t.Fatalf("ProductName() = %q, want the configured name", got)
	}
}

func TestProductNameUsesRegisteredResolver(t *testing.T) {
	prevName, prevResolver := config.Config.BrandingName, productNameResolver
	t.Cleanup(func() { config.Config.BrandingName, productNameResolver = prevName, prevResolver })

	config.Config.BrandingName = "Nudgebee"
	RegisterProductNameResolver(func() string { return "Acme" })
	if got := ProductName(); got != "Acme" {
		t.Fatalf("ProductName() = %q, want the resolver's name", got)
	}
}
