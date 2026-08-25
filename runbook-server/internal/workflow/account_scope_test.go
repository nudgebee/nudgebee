package workflow

import (
	"context"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	scopeAcctA = "11111111-1111-1111-1111-111111111111"
	scopeAcctB = "22222222-2222-2222-2222-222222222222"
)

// accountAdminContext builds a RequestContext for a user holding account_admin
// on exactly `scoped`, using the same wire shape api-server emits (see
// security_context_wire_test.go) — the scoped roles can't be constructed through
// the NewSecurityContextFor* helpers, which only cover tenant/super admins.
func accountAdminContext(t *testing.T, accountIds []string, scoped []string) *security.RequestContext {
	t.Helper()

	payload := map[string]any{
		"TenantId":        "t1",
		"UserId":          "u1",
		"AccountIds":      accountIds,
		"Roles":           []string{"account_admin"},
		"ScopedEntityIds": map[string][]string{"account_admin": scoped},
	}
	wire, err := common.MarshalJson(payload)
	require.NoError(t, err)

	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson(wire, &sc))

	return security.NewRequestContext(context.Background(), &sc, nil, nil, nil)
}

// A tenant admin with no filter gets every account in the tenant — this is what
// makes the Automations listing tenant-level.
func TestResolveReadableAccounts_TenantAdminNoFilter(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{scopeAcctA, scopeAcctB})

	accounts, err := ResolveReadableAccounts(ctx, nil)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{scopeAcctA, scopeAcctB}, accounts)
}

func TestResolveReadableAccounts_TenantAdminWithFilter(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{scopeAcctA, scopeAcctB})

	accounts, err := ResolveReadableAccounts(ctx, []string{scopeAcctB})

	require.NoError(t, err)
	assert.Equal(t, []string{scopeAcctB}, accounts)
}

// An account admin must still get a usable listing rather than the 401 a
// HasTenantAccess-based resolver (configAccountScope) would produce.
func TestResolveReadableAccounts_AccountAdminNoFilter(t *testing.T) {
	ctx := accountAdminContext(t, []string{scopeAcctA, scopeAcctB}, []string{scopeAcctA})

	accounts, err := ResolveReadableAccounts(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, []string{scopeAcctA}, accounts)
}

// Requesting an account outside the ACL narrows the scope instead of widening
// it — the readable half of the request still resolves.
func TestResolveReadableAccounts_AccountAdminFilterDropsInaccessible(t *testing.T) {
	ctx := accountAdminContext(t, []string{scopeAcctA, scopeAcctB}, []string{scopeAcctA})

	accounts, err := ResolveReadableAccounts(ctx, []string{scopeAcctA, scopeAcctB})

	require.NoError(t, err)
	assert.Equal(t, []string{scopeAcctA}, accounts)
}

// Asking only for an inaccessible account is an authorization failure, not an
// empty page — otherwise a wrong deep link looks like "no automations here".
func TestResolveReadableAccounts_AccountAdminFilterAllInaccessible(t *testing.T) {
	ctx := accountAdminContext(t, []string{scopeAcctA, scopeAcctB}, []string{scopeAcctA})

	_, err := ResolveReadableAccounts(ctx, []string{scopeAcctB})

	assert.Error(t, err)
}

// A caller with no readable accounts must be rejected: the DAO would otherwise
// receive an empty scope, which it refuses (an unbounded ANY('{}') is a bug).
func TestResolveReadableAccounts_NoReadableAccounts(t *testing.T) {
	ctx := accountAdminContext(t, []string{}, []string{})

	_, err := ResolveReadableAccounts(ctx, nil)

	assert.Error(t, err)
}

// Explicitly selecting every readable account is the same request as selecting
// none. The filter's per-provider "Select All" makes that one click, and on a
// large tenant enumerating the result would trip the downstream filter-size cap.
func TestResolveAccountScope_SelectAllIsTenantWide(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{scopeAcctA, scopeAcctB})

	scope, err := ResolveAccountScope(ctx, []string{scopeAcctA, scopeAcctB})

	require.NoError(t, err)
	assert.True(t, scope.TenantWide, "selecting every readable account should read as tenant-wide")
}

// A genuine subset must stay pinned to that subset.
func TestResolveAccountScope_SubsetIsNotTenantWide(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{scopeAcctA, scopeAcctB})

	scope, err := ResolveAccountScope(ctx, []string{scopeAcctA})

	require.NoError(t, err)
	assert.False(t, scope.TenantWide)
	assert.Equal(t, []string{scopeAcctA}, scope.AccountIDs)
}

// An account-scoped caller never gets the tenant-wide shortcut, even when they
// select everything they can see — the tenant clause alone would span accounts
// they cannot read.
func TestResolveAccountScope_AccountAdminNeverTenantWide(t *testing.T) {
	ctx := accountAdminContext(t, []string{scopeAcctA, scopeAcctB}, []string{scopeAcctA})

	scope, err := ResolveAccountScope(ctx, []string{scopeAcctA})

	require.NoError(t, err)
	assert.False(t, scope.TenantWide, "an account-scoped caller must stay enumerated")
}

// Blank entries (an empty ?account= in the URL) are ignored rather than passed
// through as an account id that matches nothing.
func TestResolveReadableAccounts_DropsBlankEntries(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{scopeAcctA, scopeAcctB})

	accounts, err := ResolveReadableAccounts(ctx, []string{"", scopeAcctA, ""})

	require.NoError(t, err)
	assert.Equal(t, []string{scopeAcctA}, accounts)
}
