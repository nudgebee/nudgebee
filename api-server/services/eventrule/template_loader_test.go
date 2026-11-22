package eventrule

import (
	"os"
	"path/filepath"
	"testing"

	"nudgebee/services/eventrule/playbooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Action parameters are rendered as one template; they must not be able to
// read files from the server.
func TestEvaluateRawParamsTemplates_CannotLoadFiles(t *testing.T) {
	const marker = "eventrule-loader-marker-3b7e"
	path := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(path, []byte(marker), 0o600))

	for name, param := range map[string]string{
		// Single quotes: the params are JSON-encoded before rendering, so a
		// double quote would reach the template as \" and fail to parse anyway.
		"include": `{% include '` + path + `' %}`,
		"import":  `{% import '` + path + `' as x %}`,
		"extends": `{% extends '` + path + `' %}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := evaluateRawParamsTemplates(map[string]any{"command": param},
				playbooks.PlaybookEvent{Labels: map[string]string{}}, map[string]any{}, map[string]map[string]any{})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), marker)
			assert.Nil(t, out)
		})
	}
}

// The custom filters registered on gonja.DefaultEnv still apply.
func TestEvaluateRawParamsTemplates_CustomFiltersStillWork(t *testing.T) {
	out, err := evaluateRawParamsTemplates(map[string]any{"name": `{{ labels.app | split(sep='/') | last }}`},
		playbooks.PlaybookEvent{Labels: map[string]string{"app": "ns/checkout"}}, map[string]any{}, map[string]map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "checkout"}, out)
}
