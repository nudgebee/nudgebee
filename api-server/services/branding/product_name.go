// Package branding names the product in text this server writes itself —
// email subjects, the invite email, webhook test messages.
//
// Per-host branding overrides (the tenant_branding_override table, its admin
// actions and GET /v1/branding/overrides) are EE-only and live in
// nudgebee/services/ee/branding, which is stripped from OSS snapshots. That
// package registers a resolver here from init(); OSS registers none, so
// ProductName is the configured branding_name. The strip is the boundary, as
// it is for the frontend provider (app/src/ee/branding/serverInit.ts).
package branding

import "nudgebee/services/config"

var productNameResolver func() string

// RegisterProductNameResolver installs the source ProductName reads. Call from
// package init().
func RegisterProductNameResolver(r func() string) {
	productNameResolver = r
}

// ProductName is the product name for text this server writes itself.
func ProductName() string {
	if productNameResolver != nil {
		return productNameResolver()
	}
	return config.Config.BrandingName
}
