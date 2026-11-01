package executors

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules fixture is the contract shared with the app's Run Script hint; the
// app runs the same cases through its mirror of these rules.
type scriptStepRules struct {
	AccountExecutor []struct {
		Executor string `json:"executor"`
		Provider string `json:"provider"`
		OK       bool   `json:"ok"`
	} `json:"account_executor"`
	ScriptLogin []struct {
		Script string            `json:"script"`
		Env    map[string]string `json:"env"`
		Login  string            `json:"login"`
	} `json:"script_login"`
}

func loadScriptStepRules(t *testing.T) scriptStepRules {
	t.Helper()
	raw, err := os.ReadFile("testdata/script_step_rules.json")
	require.NoError(t, err)
	var rules scriptStepRules
	require.NoError(t, json.Unmarshal(raw, &rules))
	require.NotEmpty(t, rules.AccountExecutor)
	require.NotEmpty(t, rules.ScriptLogin)
	return rules
}

func TestScriptStepRulesFixture_AccountExecutor(t *testing.T) {
	for _, c := range loadScriptStepRules(t).AccountExecutor {
		err := CheckAccountSupports(c.Executor, c.Provider)
		assert.Equal(t, c.OK, err == nil, "%q on a %q account", c.Executor, c.Provider)
	}
}

func TestScriptStepRulesFixture_ScriptLogin(t *testing.T) {
	for _, c := range loadScriptStepRules(t).ScriptLogin {
		assert.Equal(t, c.Login, ScriptLogin(c.Script, c.Env), "script %q env %v", c.Script, c.Env)
	}
}
