package ai

import (
	"errors"
	"slices"
	"testing"

	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	acctOwn     = "00000000-0000-0000-0000-00000000000a"
	acctPeer    = "00000000-0000-0000-0000-00000000000b"
	acctHidden  = "00000000-0000-0000-0000-00000000000c"
	acctForeign = "00000000-0000-0000-0000-00000000000d"
)

// stubAccountLookups replaces the tenant and per-user lookups: acctForeign is
// outside the tenant, and a user can read acctOwn and acctPeer only. It
// returns counters so a test can assert a lookup was skipped.
func stubAccountLookups(t *testing.T) (tenantChecks, userLookups *int) {
	t.Helper()
	tenantChecks, userLookups = new(int), new(int)
	origValidate, origUser := validateAccountInTenant, securityContextForUser
	validateAccountInTenant = func(_ *security.RequestContext, accountID string) error {
		*tenantChecks++
		if accountID == acctForeign {
			return errors.New("account '" + accountID + "' does not belong to tenant")
		}
		return nil
	}
	securityContextForUser = func(tenantID, userID string) (*security.SecurityContext, error) {
		*userLookups++
		if userID == "removed-user" {
			return nil, errors.New("user not found")
		}
		return security.NewSecurityContextForTenantAccountAdmin(tenantID, userID, []string{acctOwn, acctPeer}), nil
	}
	t.Cleanup(func() { validateAccountInTenant, securityContextForUser = origValidate, origUser })
	return tenantChecks, userLookups
}

func TestResolveTaskAccount(t *testing.T) {
	for _, tc := range []struct {
		name    string
		user    string
		params  map[string]any
		want    string
		wantErr string
		// Lookups expected: the workflow's own account must cost none.
		tenantChecks, userLookups int
	}{
		{name: "no account_id uses the workflow account", params: map[string]any{}, want: acctOwn},
		{name: "empty account_id uses the workflow account", params: map[string]any{"account_id": ""}, want: acctOwn},
		{name: "blank account_id uses the workflow account", params: map[string]any{"account_id": "  "}, want: acctOwn},
		{name: "own account is not looked up", user: "user-1", params: map[string]any{"account_id": acctOwn}, want: acctOwn},
		{name: "non-string account_id is rejected", params: map[string]any{"account_id": 42}, wantErr: "must be a string"},

		// A workflow run acts as the workflow's last editor; a dry run or the
		// Task Runner as the caller. Either way that user must read the target.
		{name: "user who can read the target", user: "user-1", params: map[string]any{"account_id": acctPeer}, want: acctPeer, tenantChecks: 1, userLookups: 1},
		{name: "user who cannot read the target", user: "user-1", params: map[string]any{"account_id": acctHidden}, wantErr: "not accessible", tenantChecks: 1, userLookups: 1},
		{name: "user whose access cannot be loaded", user: "removed-user", params: map[string]any{"account_id": acctPeer}, wantErr: "unable to verify access", tenantChecks: 1, userLookups: 1},
		{name: "account in another tenant", user: "user-1", params: map[string]any{"account_id": acctForeign}, wantErr: "does not belong to tenant", tenantChecks: 1},

		// A hook action carries no user and an unedited workflow the system
		// user: another account is refused outright, before any lookup.
		{name: "no user cannot leave the workflow account", user: "", params: map[string]any{"account_id": acctPeer}, wantErr: "runs without a user"},
		{name: "system user cannot leave the workflow account", user: security.GetSystemUserId(), params: map[string]any{"account_id": acctPeer}, wantErr: "runs without a user"},
		{name: "no user still runs on the workflow account", user: "", params: map[string]any{"account_id": acctOwn}, want: acctOwn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenantChecks, userLookups := stubAccountLookups(t)
			taskCtx := &testutils.MockTaskContext{Tenant: "tenant-1", Account: acctOwn, User: tc.user}

			got, err := resolveTaskAccount(taskCtx, tc.params)

			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
			assert.Equal(t, tc.tenantChecks, *tenantChecks, "tenant checks")
			assert.Equal(t, tc.userLookups, *userLookups, "user lookups")
		})
	}
}

func TestRejectCrossAccountApprovals(t *testing.T) {
	approved := map[string]string{"kubectl_execute": "yes"}
	for _, tc := range []struct {
		name     string
		account  string
		approved map[string]string
		wantErr  bool
	}{
		{"other account with pre-approved tools", acctPeer, approved, true},
		{"other account with nothing pre-approved", acctPeer, nil, false},
		{"own account with pre-approved tools", acctOwn, approved, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The run's user makes no difference: every workflow run carries one.
			taskCtx := &testutils.MockTaskContext{Tenant: "tenant-1", Account: acctOwn, User: "user-1"}
			err := rejectCrossAccountApprovals(taskCtx, tc.account, tc.approved)
			if tc.wantErr {
				assert.ErrorContains(t, err, "approved_tools cannot be used")
				return
			}
			assert.NoError(t, err)
		})
	}
}

// The three AI steps expose the same optional Account field the non-AI tasks
// do, and the two chat steps report which account they ran against so the
// builder can link to the right conversation.
func TestAIStepsExposeOptionalAccount(t *testing.T) {
	for name, schema := range map[string]*types.Schema{
		"llm.nubi":        (&LLMNubiTask{}).InputSchema(),
		"llm.investigate": (&LLMInvestigateTask{}).InputSchema(),
		"llm.mcp_call":    (&MCPTask{}).InputSchema(),
	} {
		prop, ok := schema.Properties[accountParamFieldName]
		require.True(t, ok, "%s must declare %s", name, accountParamFieldName)
		assert.Equal(t, types.PropertyTypeAccount, prop.Type, name)
		assert.False(t, prop.Required, "%s: account must stay optional", name)
	}

	for name, schema := range map[string]*types.Schema{
		"llm.nubi":        (&LLMNubiTask{}).OutputSchema(),
		"llm.investigate": (&LLMInvestigateTask{}).OutputSchema(),
	} {
		_, ok := schema.Properties[accountParamFieldName]
		assert.True(t, ok, "%s must return %s", name, accountParamFieldName)
	}
}

// The MCP account only matters when the call goes through an integration, and
// the tool list has to follow it.
func TestMCPAccountFieldWiring(t *testing.T) {
	props := (&MCPTask{}).InputSchema().Properties

	account := props[accountParamFieldName]
	require.NotNil(t, account.VisibleWhen)
	assert.Equal(t, "connection_mode", account.VisibleWhen.Field)
	assert.Equal(t, []string{"integration"}, account.VisibleWhen.Value)

	toolName := props["tool_name"]
	assert.True(t, slices.Contains(toolName.DependsOn, accountParamFieldName), "changing the account must clear a stale tool")
	require.NotNil(t, toolName.OptionsSource)
	assert.Equal(t, accountParamFieldName, toolName.OptionsSource.DependencyMapping[accountParamFieldName], "changing the account must re-fetch the tool list")
}
