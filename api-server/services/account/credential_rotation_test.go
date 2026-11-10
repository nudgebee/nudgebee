package account

import (
	"nudgebee/services/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFirstSubscription covers how the rotation handler picks a representative
// subscription to validate the new secret against: assume_role stores an
// account's subscription IDs comma-joined, and only the first is needed.
func TestFirstSubscription(t *testing.T) {
	assert.Equal(t, "sub-a", firstSubscription("sub-a"))
	assert.Equal(t, "sub-a", firstSubscription("sub-a,sub-b,sub-c"))
	assert.Equal(t, "sub-a", firstSubscription(" sub-a , sub-b "))
	assert.Equal(t, "", firstSubscription(""))
}

// TestAccountUpdateCredentialsRequest_Decodes guards the RPC payload contract:
// UnmarshalMapToStruct silently drops keys whose mapstructure tag doesn't match,
// so a renamed field would make the rotation a no-op with no error — the same
// failure class as the account_env picker regression.
func TestAccountUpdateCredentialsRequest_Decodes(t *testing.T) {
	var request AccountUpdateCredentialsRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"id":             "acc-123",
		"cloud_provider": "Azure",
		"client_secret":  "new-secret-value",
	}, &request)

	assert.NoError(t, err)
	assert.Equal(t, "acc-123", request.Id)
	assert.Equal(t, "Azure", request.CloudProvider)
	assert.Equal(t, "new-secret-value", request.ClientSecret)
}

// TestCredentialRotationTargets covers the fan-out: every account on the app
// registration the caller may update gets the new secret, the starting account
// always does, and accounts the caller can't update are left alone.
func TestCredentialRotationTargets(t *testing.T) {
	group := []azureCredentialGroupMember{
		{Id: "a", Status: "active"},
		{Id: "b", Status: "active"},
		{Id: "c", Status: "disabled"},
		{Id: "d", Status: "active"},
	}
	canUpdate := func(id string) bool { return id != "d" }

	targets := credentialRotationTargets("a", group, canUpdate)
	assert.Equal(t, []azureCredentialGroupMember{
		{Id: "a", Status: "active"},
		{Id: "b", Status: "active"},
		{Id: "c", Status: "disabled"},
	}, targets)

	// The starting account was access-checked before validation, so it is
	// written even when the per-account check below would say no.
	targets = credentialRotationTargets("d", group, func(string) bool { return false })
	assert.Equal(t, []azureCredentialGroupMember{{Id: "d", Status: "active"}}, targets)

	// A group query that missed the starting account still rotates it.
	targets = credentialRotationTargets("z", nil, canUpdate)
	assert.Equal(t, []azureCredentialGroupMember{{Id: "z", Status: "active"}}, targets)
}

// TestActiveTargets: only accounts that were actually updated and are active get
// their collection restarted.
func TestActiveTargets(t *testing.T) {
	targets := []azureCredentialGroupMember{
		{Id: "a", Status: "active"},
		{Id: "b", Status: "disabled"},
		{Id: "c", Status: "active"},
	}
	assert.Equal(t, []string{"a"}, activeTargets(targets, []string{"a", "b"}))
	assert.Empty(t, activeTargets(targets, nil))
}

// TestAccountUpdateCredentialsRequest_RejectsMalformedId: a non-UUID id fails
// validation (400) instead of reaching the uuid[] cast in SQL.
func TestAccountUpdateCredentialsRequest_RejectsMalformedId(t *testing.T) {
	err := common.ValidateStruct(AccountUpdateCredentialsRequest{Id: "not-a-uuid", CloudProvider: "Azure", ClientSecret: "s"})
	assert.Error(t, err)
	err = common.ValidateStruct(AccountUpdateCredentialsRequest{Id: "00000000-0000-0000-0000-00000000000a", CloudProvider: "Azure", ClientSecret: "s"})
	assert.NoError(t, err)
}
