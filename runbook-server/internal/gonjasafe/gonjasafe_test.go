package gonjasafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const marker = "loader-test-marker-7f3a"

// writeTemplateFile creates a template file the test owns and returns its path.
func writeTemplateFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "other.j2")
	content := marker + `{% macro m() %}` + marker + `{% endmacro %}{% block b %}` + marker + `{% endblock %}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func render(source string) (string, error) {
	tpl, err := FromString(source)
	if err != nil {
		return "", err
	}
	return tpl.ExecuteToString(exec.NewContext(map[string]any{"name": "world"}))
}

func loadingTemplates(path string) map[string]string {
	return map[string]string{
		"include":         `{% include "` + path + `" %}`,
		"include_ignored": `{% include "` + path + `" ignore missing %}`,
		"import":          `{% import "` + path + `" as x %}{{ x.m() }}`,
		"from":            `{% from "` + path + `" import m %}{{ m() }}`,
		"extends":         `{% extends "` + path + `" %}`,
		"include_self":    `{% include "` + rootID + `" %}`,
		"extends_self":    `{% extends "` + rootID + `" %}`,
	}
}

// The upstream loader does read the file, so the cases below exercise real file loads.
func TestUpstreamFromStringReadsFiles(t *testing.T) {
	path := writeTemplateFile(t)
	tpl, err := gonja.FromString(`{% include "` + path + `" %}`)
	require.NoError(t, err)
	out, err := tpl.ExecuteToString(exec.NewContext(nil))
	require.NoError(t, err)
	assert.Contains(t, out, marker)
}

func TestFromStringRefusesOtherTemplates(t *testing.T) {
	path := writeTemplateFile(t)
	for name, source := range loadingTemplates(path) {
		t.Run(name, func(t *testing.T) {
			out, err := render(source)
			assert.NotContains(t, out, marker)
			if name == "include_ignored" {
				// "ignore missing" turns the refusal into empty output.
				assert.NoError(t, err)
				assert.Empty(t, out)
				return
			}
			require.Error(t, err)
			assert.NotContains(t, err.Error(), marker)
		})
	}
}

func TestFromStringRendersOrdinaryTemplates(t *testing.T) {
	cases := map[string]string{
		`Hello {{ name | upper }}!`:                                     "Hello WORLD!",
		`{% for c in ["a", "b"] %}{{ c }}{% endfor %}`:                  "ab",
		`{% set x = 2 %}{{ x * 3 }}`:                                    "6",
		`{% macro greet(n) %}hi {{ n }}{% endmacro %}{{ greet(name) }}`: "hi world",
		`{% if name == "world" %}yes{% else %}no{% endif %}`:            "yes",
		`{% block b %}inner{% endblock %}`:                              "inner",
		`{% raw %}{{ name }}{% endraw %}`:                               "{{ name }}",
	}
	for source, want := range cases {
		out, err := render(source)
		require.NoError(t, err, source)
		assert.Equal(t, want, strings.TrimSpace(out), source)
	}
}

// A compiled template must stay executable more than once (templates are cached).
func TestFromStringTemplateIsReusable(t *testing.T) {
	tpl, err := FromString(`{{ name }}`)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		out, err := tpl.ExecuteToString(exec.NewContext(map[string]any{"name": "world"}))
		require.NoError(t, err)
		assert.Equal(t, "world", out)
	}
}
