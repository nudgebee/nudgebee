// Package auth authenticates inbound gateway requests and resolves them to an NB
// identity. The gateway sits directly behind ingress (no Next.js in front), so it
// must authenticate itself. Token validation is behind a Validator interface with
// pluggable modes: "user_auths" (the real NB Personal Access Token lookup via the
// sha256 column — the secure default), "static" (a configured token → identity, an
// ASSUMPTION for controlled setups), and "none" (no auth — an open proxy, gated
// behind an explicit insecure acknowledgment; see CheckConfig).
package auth

import (
	"crypto/subtle"
	"errors"
	"log/slog"

	"nudgebee/llm-gateway/config"
)

// CheckConfig validates the auth configuration at startup and returns an error if
// the gateway would run insecurely by accident. Mode "none" disables authentication
// entirely — an open proxy to the server-side provider keys — so it is permitted
// only when gateway_auth_allow_insecure is explicitly set. Callers should treat a
// non-nil result as fatal (fail-closed at boot).
func CheckConfig() error {
	if config.Config.AuthMode == "none" && !config.Config.AuthAllowInsecure {
		return errors.New(`auth: mode "none" disables authentication (open proxy to provider keys); ` +
			`set gateway_auth_mode=user_auths, or for local dev only set ` +
			`gateway_auth_allow_insecure=true to acknowledge running with no auth`)
	}
	return nil
}

// Identity is the resolved caller. Fields are empty when auth mode is "none".
type Identity struct {
	TenantID  string
	AccountID string
	UserID    string
	TokenID   string // opaque id/name of the token used (not the secret)
}

// Validator resolves a raw bearer token to an identity. ok=false means the token
// is invalid/unknown and the request must be rejected.
type Validator interface {
	// Validate returns the identity for a raw token. Mode "none" returns an empty
	// identity with ok=true for any input (including empty).
	Validate(rawToken string) (Identity, bool)
	// RequiresToken reports whether a missing token should be rejected (401).
	RequiresToken() bool
}

// NewValidator builds the Validator for the configured auth mode.
func NewValidator() Validator {
	switch config.Config.AuthMode {
	case "static":
		if config.Config.StaticToken == "" {
			slog.Warn("auth: mode=static but gateway_static_token is empty — all requests will be rejected")
		}
		slog.Info("auth: static mode (ASSUMPTION — single configured token → identity; replace with user_auths lookup)")
		return &staticValidator{
			token: config.Config.StaticToken,
			identity: Identity{
				// account_id intentionally unset — tenant+user is the grain for now.
				TenantID: config.Config.StaticTenantID,
				UserID:   config.Config.StaticUserID,
				TokenID:  "static",
			},
		}
	case "user_auths":
		slog.Info("auth: user_auths mode — validating NB PAT via sha256 lookup on user_auths")
		return newUserAuthsValidator()
	default: // "none"
		slog.Warn("auth: mode=none — requests are NOT authenticated; identity will be empty")
		return &noneValidator{}
	}
}

// noneValidator authenticates nothing (dev/testing). Identity stays empty.
type noneValidator struct{}

func (noneValidator) Validate(string) (Identity, bool) { return Identity{}, true }
func (noneValidator) RequiresToken() bool              { return false }

// denyValidator rejects everything (fail-closed placeholder for unimplemented modes).
type denyValidator struct{}

func (denyValidator) Validate(string) (Identity, bool) { return Identity{}, false }
func (denyValidator) RequiresToken() bool              { return true }

// staticValidator matches one configured token (constant-time) → one identity.
// ASSUMPTION mode: stands in for the real per-tenant user_auths lookup.
type staticValidator struct {
	token    string
	identity Identity
}

func (v *staticValidator) Validate(raw string) (Identity, bool) {
	if v.token == "" || raw == "" {
		return Identity{}, false
	}
	if subtle.ConstantTimeCompare([]byte(raw), []byte(v.token)) == 1 {
		return v.identity, true
	}
	return Identity{}, false
}

func (v *staticValidator) RequiresToken() bool { return true }
