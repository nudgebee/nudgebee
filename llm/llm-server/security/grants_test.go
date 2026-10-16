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

func TestToolPathChecksUseGrantsOnlyInAGrantScopedRun(t *testing.T) {
	sc := caseContext(t, map[string]any{"ScopedCustomPermissions": map[string]any{"acct-a": map[string]any{"k8s:Read": true}}})
	person := NewRequestContext(t.Context(), sc, nil, nil, nil)
	bee := WithGrantScopedRun(person)

	assert.False(t, IsGrantScopedRun(person))
	assert.True(t, IsGrantScopedRun(bee))

	// A person keeps the built-in role check; the bee's run reads through its grant.
	assert.False(t, ToolPathAccountAccess(person, "acct-a", "k8s", SecurityAccessTypeRead))
	assert.True(t, ToolPathAccountAccess(bee, "acct-a", "k8s", SecurityAccessTypeRead))
	assert.False(t, ToolPathAccountAccess(bee, "acct-a", "k8s", SecurityAccessTypeCreate))
	assert.False(t, ToolPathAccountAccess(bee, "acct-a", "cloud", SecurityAccessTypeRead))

	// Integration use is checked only in a grant-scoped run.
	assert.NoError(t, CheckIntegrationUse(person, "acct-a", "jira", true))
	err := CheckIntegrationUse(bee, "acct-a", "jira", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrations.jira:Read on account acct-a")
	err = CheckIntegrationUse(bee, "acct-a", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integration type is unknown")
}

func TestGrantChecksRefuseANilContext(t *testing.T) {
	var sc *SecurityContext
	assert.False(t, sc.Can("acct-a", "k8s", "Read"))
	assert.False(t, sc.MayUseIntegration("acct-a", "jira", false))
}
