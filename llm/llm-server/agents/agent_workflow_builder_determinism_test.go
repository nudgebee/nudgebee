package agents

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSetWorkflowStatus verifies the helper sets a top-level status, preserves the rest of the
// workflow, and errors on malformed JSON (so the caller falls back to the unmodified workflow).
func TestSetWorkflowStatus(t *testing.T) {
	in := `{"name":"wf","definition":{"version":"v1","tasks":[{"id":"a"}]}}`

	out, err := setWorkflowStatus(in, "INACTIVE")
	assert.NoError(t, err)

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "INACTIVE", got["status"])
	assert.Equal(t, "wf", got["name"])
	assert.NotNil(t, got["definition"], "definition must be preserved")

	// Overwrites an existing status rather than appending.
	out2, err := setWorkflowStatus(`{"name":"wf","status":"ACTIVE"}`, "INACTIVE")
	assert.NoError(t, err)
	var got2 map[string]interface{}
	assert.NoError(t, json.Unmarshal([]byte(out2), &got2))
	assert.Equal(t, "INACTIVE", got2["status"])

	_, err = setWorkflowStatus(`{not json`, "INACTIVE")
	assert.Error(t, err)
}

// TestIsRealTemplateSyntaxError covers the markers that must force a template error to fail
// validation rather than soft-pass to finalize (#31495).
func TestIsRealTemplateSyntaxError(t *testing.T) {
	cases := []struct {
		name string
		err  string
		want bool
	}{
		{"jmespath projection", `unable to execute template: invalid expression near "*" in [*]`, true},
		{"unknown filter", `unable to execute template: no filter named 'foo'`, true},
		{"tojson typo", `unable to execute template: filter tojson not found`, true},
		{"malformed", `unable to execute template: unexpected token`, true},
		{"parse error", `unable to execute template: unable to parse expression`, true},
		{"jsonpath project", `unable to execute template: [?status=='x'] not supported`, true},
		{"pure runtime ref", `unable to execute template: Tasks['x'].output.y is not available`, false},
		{"config ref", `unable to execute template: Configs.slack_channel missing at validation`, false},
		{"input ref", `unable to execute template: Inputs.event.account_id`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isRealTemplateSyntaxError(c.err))
		})
	}
}

// TestReferencesRuntimeValue covers the runtime-value markers that make a template error safe to
// defer to execution time.
func TestReferencesRuntimeValue(t *testing.T) {
	cases := []struct {
		name string
		err  string
		want bool
	}{
		{"task index", `... Tasks['x'].output.y ...`, true},
		{"task dot", `... Tasks.x ...`, true},
		{"config", `... Configs.slack_channel ...`, true},
		{"input", `... Inputs.event.id ...`, true},
		{"output", `... .output.foo ...`, true},
		{"none", `unable to execute template: unexpected token`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, referencesRuntimeValue(c.err))
		})
	}
}

// TestTemplateValidationDecision documents the combined fail-closed decision used by toolValidate:
// a template error is deferred (soft-pass) ONLY when it references a runtime value AND carries no
// syntax/filter marker; everything else fails closed.
func TestTemplateValidationDecision(t *testing.T) {
	defer_ := func(errMsg string) bool {
		// mirrors toolValidate: defer (soft-pass) only when it references a runtime value AND
		// carries no syntax/filter marker; everything else fails closed.
		return !isRealTemplateSyntaxError(errMsg) && referencesRuntimeValue(errMsg)
	}
	// Genuine runtime deferral → soft-pass.
	assert.True(t, defer_(`unable to execute template: Tasks['x'].output.y not available`))
	// Bad filter on a runtime value → still fails closed.
	assert.False(t, defer_(`unable to execute template: Configs.x | no filter named 'foo'`))
	// Pure syntax error, no runtime ref → fails closed.
	assert.False(t, defer_(`unable to execute template: invalid expression near "*"`))
}
