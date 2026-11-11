package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task parameters must not be able to pull other templates (files) into a render.
func TestRenderRefusesTemplateLoading(t *testing.T) {
	const marker = "workflow-loader-test-marker-91c2"
	path := filepath.Join(t.TempDir(), "other.j2")
	require.NoError(t, os.WriteFile(path, []byte(marker+`{% macro m() %}`+marker+`{% endmacro %}`), 0o600))

	for name, tpl := range map[string]string{
		"include": `{% include "` + path + `" %}`,
		"import":  `{% import "` + path + `" as x %}{{ x.m() }}`,
		"from":    `{% from "` + path + `" import m %}{{ m() }}`,
		"extends": `{% extends "` + path + `" %}`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := NewTemplateContext(nil, nil)
			out, err := ctx.Render(tpl)
			require.Error(t, err)
			assert.NotContains(t, out, marker)
			assert.NotContains(t, err.Error(), marker)
		})
	}
}
