package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A custom tool's audit keeps its environment variable names, never their
// values: a container tool's environment can hold a credential under any name.
func TestToolForAuditRedactsEveryEnvValue(t *testing.T) {
	envVars := map[string]any{
		"OPENAI_KEY":   "sk-proj-Qv7rTx2mWp9zQv7rTx2mWp9zQv7rTx2mWp9z",
		"DATABASE_URL": "postgres://admin:Qv7rTx2mWp9z@db/app",
		"REGION":       "us-east-1",
	}
	tool := ToolDto{
		Id:     "tool-1",
		Name:   "report-builder",
		Config: map[string]any{"image": "registry.example.com/report:1.2", "env_vars": envVars},
	}

	audited := toolForAudit(tool)

	stored, err := json.Marshal(audited)
	require.NoError(t, err)
	for _, value := range []string{"Qv7rTx2mWp9z", "us-east-1"} {
		assert.NotContains(t, string(stored), value)
	}
	assert.Equal(t, map[string]any{
		"OPENAI_KEY":   auditRedactedValue,
		"DATABASE_URL": auditRedactedValue,
		"REGION":       auditRedactedValue,
	}, audited.Config["env_vars"])
	assert.Equal(t, "registry.example.com/report:1.2", audited.Config["image"])
	assert.Equal(t, "report-builder", audited.Name)

	// The tool that runs keeps its values.
	assert.Equal(t, "us-east-1", tool.Config["env_vars"].(map[string]any)["REGION"])
	assert.Equal(t, envVars, tool.Config["env_vars"])
}

func TestToolForAuditLeavesToolsWithoutEnv(t *testing.T) {
	tool := ToolDto{Id: "tool-2", Config: map[string]any{"url": "https://example.com"}}
	assert.Equal(t, tool, toolForAudit(tool))
	assert.Equal(t, ToolDto{}, toolForAudit(ToolDto{}))

	stringEnv := ToolDto{Config: map[string]any{"env_vars": map[string]string{"TOKEN": "abc"}}}
	assert.Equal(t, map[string]any{"TOKEN": auditRedactedValue}, toolForAudit(stringEnv).Config["env_vars"])
}
