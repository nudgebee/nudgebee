package core

import (
	"context"
	"nudgebee/services/common"
)

// ExternalUser is a user/account discovered on an external integration (Slack,
// PagerDuty, ZenDuty, GitHub, ...). Email is empty for sources that don't expose
// it (e.g. GitHub returns logins only).
type ExternalUser struct {
	ID          string // provider-side id: Slack Uxxx, PD/ZD user id, GitHub login
	Username    string
	Email       string
	DisplayName string
}

// UserLister is an optional capability an Integration may implement to enumerate
// the external users/accounts it knows about. Identity sync uses this to map
// internal Nudgebee users to their external accounts by email. Only integrations
// with a user concept (ticketing/messaging) implement it — same optional-interface
// pattern as TestableIntegration.
type UserLister interface {
	ListUsers(ctx context.Context, values []IntegrationConfigValue) ([]ExternalUser, error)
}

// ConfigValue returns the (decrypted) value of a named config key from a config
// slice, or "" if absent. Encrypted values are transparently decrypted, so callers
// (e.g. ListUsers implementations during sync) get a usable token/key.
func ConfigValue(values []IntegrationConfigValue, name string) string {
	for _, v := range values {
		if v.Name != name {
			continue
		}
		if v.IsEncrypted {
			decrypted, err := common.Decrypt(v.Value)
			if err != nil {
				return ""
			}
			return decrypted
		}
		return v.Value
	}
	return ""
}
