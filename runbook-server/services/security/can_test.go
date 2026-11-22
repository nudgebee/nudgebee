package security

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCanCases runs the cases llm-server's Can also runs, so the two
// copies give the same answers.
func TestCanCases(t *testing.T) {
	body, err := os.ReadFile("testdata/can_cases.json")
	require.NoError(t, err)
	var table struct {
		Cases []struct {
			Name    string         `json:"name"`
			Context map[string]any `json:"context"`
			Account string         `json:"account"`
			Module  string         `json:"module"`
			Class   string         `json:"class"`
			Want    bool           `json:"want"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(body, &table))
	require.NotEmpty(t, table.Cases)

	for _, c := range table.Cases {
		sc := caseContext(t, c.Context)
		assert.Equal(t, c.Want, sc.Can(c.Account, c.Module, c.Class), c.Name)
	}
}

// TestMayUseIntegrationCases runs the use cases llm-server's MayUseIntegration
// also runs.
func TestMayUseIntegrationCases(t *testing.T) {
	body, err := os.ReadFile("testdata/can_cases.json")
	require.NoError(t, err)
	var table struct {
		UseCases []struct {
			Name    string         `json:"name"`
			Context map[string]any `json:"context"`
			Account string         `json:"account"`
			Type    string         `json:"type"`
			Write   bool           `json:"write"`
			Want    bool           `json:"want"`
		} `json:"use_cases"`
	}
	require.NoError(t, json.Unmarshal(body, &table))
	require.NotEmpty(t, table.UseCases)

	for _, c := range table.UseCases {
		sc := caseContext(t, c.Context)
		assert.Equal(t, c.Want, sc.MayUseIntegration(c.Account, c.Type, c.Write), c.Name)
	}
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

func TestGrantChecksRefuseANilContext(t *testing.T) {
	var sc *SecurityContext
	assert.False(t, sc.Can("acct-a", "k8s", "Read"))
	assert.False(t, sc.MayUseIntegration("acct-a", "jira", false))
}
