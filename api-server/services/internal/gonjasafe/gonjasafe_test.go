package gonjasafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noirbizarre/gonja"
	"github.com/noirbizarre/gonja/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const marker = "loader-test-marker-5c1e"

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
	return tpl.Execute(gonja.Context{"name": "world"})
}

func loadingTemplates(path string) map[string]string {
	return map[string]string{
		"include":         `{{ name }}{% include "` + path + `" %}`,
		"include_ignored": `{% include "` + path + `" ignore missing %}`,
		"import":          `{% import "` + path + `" as x %}{{ x.m() }}`,
		"from":            `{% from "` + path + `" import m %}{{ m() }}`,
		"extends":         `{% extends "` + path + `" %}`,
	}
}

// The upstream loader does read the file, so the refusal cases below exercise
// real file loads rather than paths that would have failed anyway.
func TestUpstreamFromStringReadsFiles(t *testing.T) {
	path := writeTemplateFile(t)
	tpl, err := gonja.FromString(`{% include "` + path + `" %}`)
	require.NoError(t, err)
	out, err := tpl.Execute(gonja.Context{})
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
				assert.Empty(t, strings.TrimSpace(out))
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
		`{% raw %}{{ name }}{% endraw %}`:                               "{{ name }}",
	}
	for source, want := range cases {
		out, err := render(source)
		require.NoError(t, err, source)
		assert.Equal(t, want, strings.TrimSpace(out), source)
	}
}

// Filters registered on gonja.DefaultEnv (eventrule registers split, pluck, …
// at init) must stay available, including ones registered after this package
// was loaded.
func TestFromStringSeesFiltersRegisteredOnDefaultEnv(t *testing.T) {
	const name = "gonjasafe_test_shout"
	if !gonja.DefaultEnv.Filters.Exists(name) {
		require.NoError(t, gonja.DefaultEnv.Filters.Register(name, func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
			return exec.AsValue(strings.ToUpper(in.String()) + "!")
		}))
	}
	out, err := render(`{{ name | ` + name + ` }}`)
	require.NoError(t, err)
	assert.Equal(t, "WORLD!", out)
}

// A compiled template must stay executable more than once (label mappings are cached).
func TestFromStringTemplateIsReusable(t *testing.T) {
	tpl, err := FromString(`{{ name }}`)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		out, err := tpl.Execute(gonja.Context{"name": "world"})
		require.NoError(t, err)
		assert.Equal(t, "world", out)
	}
}
