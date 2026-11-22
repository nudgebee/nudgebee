package security

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The case table runbook-server's Can and MayUseIntegration run, so the two
// copies give the same answers.
const grantCasesPath = "../../../runbook-server/services/security/testdata/can_cases.json"

type grantCase struct {
	Name    string         `json:"name"`
	Context map[string]any `json:"context"`
	Account string         `json:"account"`
	Module  string         `json:"module"`
	Class   string         `json:"class"`
	Type    string         `json:"type"`
	Write   bool           `json:"write"`
	Want    bool           `json:"want"`
}

func readGrantCases(t *testing.T) (cases, useCases []grantCase) {
	t.Helper()
	body, err := os.ReadFile(grantCasesPath)
	require.NoError(t, err)
	var table struct {
		Cases    []grantCase `json:"cases"`
		UseCases []grantCase `json:"use_cases"`
	}
	require.NoError(t, json.Unmarshal(body, &table))
	require.NotEmpty(t, table.Cases)
	require.NotEmpty(t, table.UseCases)
	return table.Cases, table.UseCases
}

// caseContext builds a case's context in tenant t with accounts acct-a and acct-b.
func caseContext(t *testing.T, fields map[string]any) *SecurityContext {
	t.Helper()
	fields["TenantId"] = "t"
	fields["UserId"] = "u"
	fields["AccountIds"] = []string{"acct-a", "acct-b"}
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	var sc SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	return &sc
}

func TestCanCases(t *testing.T) {
	cases, _ := readGrantCases(t)
	for _, c := range cases {
		assert.Equal(t, c.Want, caseContext(t, c.Context).Can(c.Account, c.Module, c.Class), c.Name)
	}
}

func TestMayUseIntegrationCases(t *testing.T) {
	_, useCases := readGrantCases(t)
	for _, c := range useCases {
		assert.Equal(t, c.Want, caseContext(t, c.Context).MayUseIntegration(c.Account, c.Type, c.Write), c.Name)
	}
}

func TestToolPathChecksHonourGrantsForPeopleWithoutABuiltInRole(t *testing.T) {
	sc := caseContext(t, map[string]any{"ScopedCustomPermissions": map[string]any{"acct-a": map[string]any{"k8s:Read": true}}})
	person := NewRequestContext(t.Context(), sc, nil, nil, nil)
	bee := WithGrantScopedRun(person)

	assert.False(t, IsGrantScopedRun(person))
	assert.True(t, IsGrantScopedRun(bee))
	// Both are grant-checked on acct-a: the person has no built-in role there.
	assert.True(t, IsGrantCheckedRun(person, "acct-a"))
	assert.True(t, IsGrantCheckedRun(bee, "acct-a"))

	// A person and a bee read through the k8s:Read grant, and only that.
	for name, ctx := range map[string]*RequestContext{"person": person, "bee": bee} {
		assert.True(t, ToolPathAccountAccess(ctx, "acct-a", "k8s", SecurityAccessTypeRead), name)
		assert.False(t, ToolPathAccountAccess(ctx, "acct-a", "k8s", SecurityAccessTypeCreate), name+": Read does not run changes")
		assert.False(t, ToolPathAccountAccess(ctx, "acct-a", "cloud", SecurityAccessTypeRead), name+": a grant covers only its module")
		assert.False(t, ToolPathAccountAccess(ctx, "acct-a", "", SecurityAccessTypeRead), name+": a tool with no module needs a built-in role")

		// Integration use needs its own grant.
		err := CheckIntegrationUse(ctx, "acct-a", "jira", false)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "integrations.jira:Read on account acct-a")
		err = CheckIntegrationUse(ctx, "acct-a", "", false)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "integration type is unknown")
	}
}

// No context means no proven built-in role: the grant checks apply and refuse.
// (true from IsGrantCheckedRun is the strict answer; false would skip the checks.)
func TestGrantChecksFailClosedWithoutAContext(t *testing.T) {
	for name, ctx := range map[string]*RequestContext{
		"nil request context":  nil,
		"nil security context": NewRequestContext(t.Context(), nil, nil, nil, nil),
	} {
		assert.True(t, IsGrantCheckedRun(ctx, "acct-a"), name)
		assert.False(t, ToolPathAccountAccess(ctx, "acct-a", "k8s", SecurityAccessTypeRead), name)
		assert.Error(t, CheckIntegrationUse(ctx, "acct-a", "jira", false), name)
		assert.Error(t, CheckIntegrationUse(ctx, "acct-a", "", false), name)
	}
}

// A person with a built-in role on the account keeps the built-in checks: no
// grant lookup, no integration-use check, exactly as before.
func TestToolPathChecksUnchangedForABuiltInRole(t *testing.T) {
	sc := caseContext(t, map[string]any{
		"AccountIds":      []any{"acct-a"},
		"Roles":           []any{"account_admin"},
		"ScopedEntityIds": map[string]any{"account_admin": []any{"acct-a"}},
	})
	admin := NewRequestContext(t.Context(), sc, nil, nil, nil)
	assert.False(t, IsGrantCheckedRun(admin, "acct-a"))
	assert.True(t, ToolPathAccountAccess(admin, "acct-a", "k8s", SecurityAccessTypeCreate))
	assert.True(t, ToolPathAccountAccess(admin, "acct-a", "", SecurityAccessTypeRead))
	assert.NoError(t, CheckIntegrationUse(admin, "acct-a", "jira", true))
	assert.NoError(t, CheckIntegrationUse(admin, "acct-a", "", true))
}

func TestGrantChecksRefuseANilContext(t *testing.T) {
	var sc *SecurityContext
	assert.False(t, sc.Can("acct-a", "k8s", "Read"))
	assert.False(t, sc.MayUseIntegration("acct-a", "jira", false))
}
