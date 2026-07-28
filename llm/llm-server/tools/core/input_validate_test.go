package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// validateTestTool is a minimal NBTool stub — only Name()/InputSchema() are
// exercised; other methods panic if a test path ever reaches them.
type validateTestTool struct {
	name   string
	schema ToolSchema
}

func (t *validateTestTool) Name() string            { return t.name }
func (t *validateTestTool) Description() string     { return "" }
func (t *validateTestTool) GetType() NBToolType     { return NBToolTypeTool }
func (t *validateTestTool) InputSchema() ToolSchema { return t.schema }
func (t *validateTestTool) Call(_ NbToolContext, _ NBToolCallRequest) (NBToolResponse, error) {
	panic("Call should not be invoked from validator tests")
}

func newValidateTestTool(name string, props map[string]ToolSchemaProperty) *validateTestTool {
	return &validateTestTool{name: name, schema: ToolSchema{Properties: props}}
}

func TestValidateToolInput_Skips(t *testing.T) {
	t.Run("nil tool → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(nil, `{"x":1}`))
	})

	t.Run("tool with no schema properties and no required → nil", func(t *testing.T) {
		tl := newValidateTestTool("t", nil)
		assert.Nil(t, ValidateToolInput(tl, `{"anything":true}`))
	})

	t.Run("non-JSON input is not validated", func(t *testing.T) {
		tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
			"command": {Type: ToolSchemaTypeString},
		})
		tl.schema.Required = []string{"command"}
		assert.Nil(t, ValidateToolInput(tl, "kubectl get pods"))
	})
}

func TestValidateToolInput_RequiredFields(t *testing.T) {
	tl := newValidateTestTool("shell", map[string]ToolSchemaProperty{
		"command": {Type: ToolSchemaTypeString, Description: "Shell command to run"},
		"timeout": {Type: ToolSchemaTypeInteger},
	})
	tl.schema.Required = []string{"command"}

	t.Run("missing required field produces actionable error", func(t *testing.T) {
		got := ValidateToolInput(tl, `{"timeout": 30}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `missing required field "command"`)
			assert.Contains(t, *got, `Invalid tool input for "shell"`)
			assert.Contains(t, *got, "Expected schema:")
			assert.Contains(t, *got, "command: string (required)")
		}
	})

	t.Run("all required present → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(tl, `{"command": "ls", "timeout": 30}`))
	})
}

func TestValidateToolInput_TypeChecking(t *testing.T) {
	tl := newValidateTestTool("loki_query", map[string]ToolSchemaProperty{
		"query":   {Type: ToolSchemaTypeString},
		"limit":   {Type: ToolSchemaTypeInteger},
		"labels":  {Type: ToolSchemaTypeArray},
		"filters": {Type: ToolSchemaTypeObject},
		"live":    {Type: ToolSchemaTypeBoolean},
	})

	cases := []struct {
		name      string
		input     string
		wantSub   string
		shouldErr bool
	}{
		{"string ok", `{"query":"foo"}`, "", false},
		{"string wrong type", `{"query":123}`, `field "query" expected string, got float64`, true},
		{"integer ok (json number)", `{"limit":100}`, "", false},
		{"integer ok (whole-valued float)", `{"limit":100.0}`, "", false},
		{"integer rejects fractional float", `{"limit":1.5}`, `field "limit" expected integer, got non-integer number 1.5`, true},
		{"integer wrong type", `{"limit":"100"}`, `field "limit" expected integer, got string`, true},
		{"array ok", `{"labels":["a","b"]}`, "", false},
		{"array wrong type", `{"labels":"a"}`, `field "labels" expected array, got string`, true},
		{"object ok", `{"filters":{"k":"v"}}`, "", false},
		{"object wrong type", `{"filters":[1,2]}`, `field "filters" expected object, got`, true},
		{"boolean ok", `{"live":true}`, "", false},
		{"boolean wrong type", `{"live":"yes"}`, `field "live" expected boolean, got string`, true},
		{"null value skips type check", `{"query":null}`, "", false},
		{"unknown field ignored", `{"query":"foo","unknown":"x"}`, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateToolInput(tl, tc.input)
			if tc.shouldErr {
				if assert.NotNil(t, got) {
					assert.Contains(t, *got, tc.wantSub)
				}
			} else {
				assert.Nil(t, got)
			}
		})
	}
}

func TestValidateToolInput_Enum(t *testing.T) {
	tl := newValidateTestTool("tickets", map[string]ToolSchemaProperty{
		"status": {
			Type: ToolSchemaTypeString,
			Enum: []any{"OPEN", "CLOSED", "PENDING"},
		},
	})

	t.Run("value in enum → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(tl, `{"status":"OPEN"}`))
	})

	t.Run("value not in enum → error lists enum", func(t *testing.T) {
		got := ValidateToolInput(tl, `{"status":"INVALID"}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `field "status" value INVALID not in allowed enum`)
			assert.Contains(t, *got, "OPEN")
		}
	})

	t.Run("numeric enum matches across types (int literal vs JSON float)", func(t *testing.T) {
		// Enum []any{1,2,3} vs JSON float64: without numeric unification
		// 1 == 1.0 is false in Go and the validator would falsely reject.
		intEnumTool := newValidateTestTool("priority", map[string]ToolSchemaProperty{
			"level": {
				Type: ToolSchemaTypeInteger,
				Enum: []any{1, 2, 3},
			},
		})
		assert.Nil(t, ValidateToolInput(intEnumTool, `{"level":2}`))
	})

	t.Run("numeric enum still rejects values outside the set", func(t *testing.T) {
		intEnumTool := newValidateTestTool("priority", map[string]ToolSchemaProperty{
			"level": {
				Type: ToolSchemaTypeInteger,
				Enum: []any{1, 2, 3},
			},
		})
		got := ValidateToolInput(intEnumTool, `{"level":5}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `field "level" value 5 not in allowed enum`)
		}
	})

	t.Run("non-comparable input value does not panic on enum check", func(t *testing.T) {
		// String-enum schema but input is a JSON array: the enum check runs on a
		// non-comparable []any, where == would panic but reflect.DeepEqual is safe.
		tl := newValidateTestTool("tickets", map[string]ToolSchemaProperty{
			"status": {
				Type: ToolSchemaTypeString,
				Enum: []any{"OPEN", "CLOSED"},
			},
		})
		assert.NotPanics(t, func() {
			got := ValidateToolInput(tl, `{"status":["OPEN"]}`)
			if assert.NotNil(t, got) {
				assert.Contains(t, *got, `field "status" expected string`)
			}
		})
	})
}

func TestValidateToolInput_RequiredOneOf(t *testing.T) {
	newSQLTool := func() *validateTestTool {
		tl := newValidateTestTool("postgres", map[string]ToolSchemaProperty{
			"command":  {Type: ToolSchemaTypeString},
			"query":    {Type: ToolSchemaTypeString},
			"database": {Type: ToolSchemaTypeString},
		})
		tl.schema.RequiredOneOf = [][]string{{"command", "query"}}
		return tl
	}

	t.Run("command satisfies the group → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(newSQLTool(), `{"command":"SELECT 1"}`))
	})

	t.Run("query alias satisfies the group → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(newSQLTool(), `{"query":"SELECT 1","database":"app"}`))
	})

	t.Run("both present is allowed → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(newSQLTool(), `{"command":"SELECT 1","query":"SELECT 2"}`))
	})

	t.Run("neither present → actionable error naming the group", func(t *testing.T) {
		got := ValidateToolInput(newSQLTool(), `{"database":"app"}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `at least one of [command query] is required`)
			assert.Contains(t, *got, "Provide at least one of:")
			assert.Contains(t, *got, "command | query")
		}
	})

	t.Run("present-but-null does not satisfy the group", func(t *testing.T) {
		got := ValidateToolInput(newSQLTool(), `{"command":null}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `at least one of [command query] is required`)
		}
	})

	t.Run("non-null alias alongside a null primary → nil", func(t *testing.T) {
		assert.Nil(t, ValidateToolInput(newSQLTool(), `{"command":null,"query":"SELECT 1"}`))
	})

	t.Run("group unsatisfied is reported alongside a type error", func(t *testing.T) {
		tl := newSQLTool()
		got := ValidateToolInput(tl, `{"database":123}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `at least one of [command query] is required`)
			assert.Contains(t, *got, `field "database" expected string, got float64`)
		}
	})

	t.Run("schema with only RequiredOneOf is not skipped", func(t *testing.T) {
		tl := &validateTestTool{name: "t", schema: ToolSchema{
			RequiredOneOf: [][]string{{"a", "b"}},
		}}
		got := ValidateToolInput(tl, `{"c":1}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `at least one of [a b] is required`)
		}
	})
}

func TestValidateToolInput_MalformedJSON(t *testing.T) {
	tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
		"x": {Type: ToolSchemaTypeString},
	})
	got := ValidateToolInput(tl, `{"x": "abc"`)
	if assert.NotNil(t, got) {
		assert.True(t, strings.HasPrefix(*got, "Invalid JSON tool input:"),
			"expected 'Invalid JSON tool input:' prefix, got: %s", *got)
	}
}

func TestValidateToolInput_MultipleErrorsReported(t *testing.T) {
	tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
		"name":  {Type: ToolSchemaTypeString},
		"count": {Type: ToolSchemaTypeInteger},
		"extra": {Type: ToolSchemaTypeBoolean},
	})
	tl.schema.Required = []string{"name", "count"}

	got := ValidateToolInput(tl, `{"count":"ten","extra":"yes"}`)
	if assert.NotNil(t, got) {
		assert.Contains(t, *got, `missing required field "name"`)
		assert.Contains(t, *got, `field "count" expected integer, got string`)
		assert.Contains(t, *got, `field "extra" expected boolean, got string`)
	}
}

// TestMissingRequiredFields covers the helper used by the executor to route
// missing-Required rejections to a tool's MissingFieldsResponder before the
// generic ValidateToolInput fires. Only Required[] is considered — the caller
// is responsible for the RequiredOneOf case, which shouldn't trigger a
// per-tool custom message (validator already emits the group-level line).
func TestMissingRequiredFields(t *testing.T) {
	t.Run("nil tool returns nil", func(t *testing.T) {
		assert.Nil(t, MissingRequiredFields(nil, `{"x":1}`))
	})

	t.Run("no required fields returns nil", func(t *testing.T) {
		tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
			"opt": {Type: ToolSchemaTypeString},
		})
		assert.Nil(t, MissingRequiredFields(tl, `{"opt":"x"}`))
	})

	t.Run("non-JSON input returns nil (validator delegates to plain-text wrap upstream)", func(t *testing.T) {
		tl := newValidateTestTool("shell", map[string]ToolSchemaProperty{
			"command": {Type: ToolSchemaTypeString},
		})
		tl.schema.Required = []string{"command"}
		assert.Nil(t, MissingRequiredFields(tl, "kubectl get pods"))
	})

	t.Run("missing field returns it in declared order", func(t *testing.T) {
		tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
			"first":  {Type: ToolSchemaTypeString},
			"second": {Type: ToolSchemaTypeString},
			"third":  {Type: ToolSchemaTypeString},
		})
		tl.schema.Required = []string{"first", "second", "third"}
		got := MissingRequiredFields(tl, `{"second":"present"}`)
		assert.Equal(t, []string{"first", "third"}, got)
	})

	t.Run("nil-valued required field counts as missing", func(t *testing.T) {
		tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
			"required_str": {Type: ToolSchemaTypeString},
		})
		tl.schema.Required = []string{"required_str"}
		got := MissingRequiredFields(tl, `{"required_str":null}`)
		assert.Equal(t, []string{"required_str"}, got)
	})

	t.Run("empty-string required field counts as missing (whitespace trimmed)", func(t *testing.T) {
		tl := newValidateTestTool("think", map[string]ToolSchemaProperty{
			"reasoning": {Type: ToolSchemaTypeString},
		})
		tl.schema.Required = []string{"reasoning"}
		got := MissingRequiredFields(tl, `{"reasoning":"   "}`)
		assert.Equal(t, []string{"reasoning"}, got)
	})

	t.Run("all present returns nil", func(t *testing.T) {
		tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
			"a": {Type: ToolSchemaTypeString},
			"b": {Type: ToolSchemaTypeInteger},
		})
		tl.schema.Required = []string{"a", "b"}
		assert.Nil(t, MissingRequiredFields(tl, `{"a":"x","b":1}`))
	})

	t.Run("invalid JSON returns nil (validator handles reporting)", func(t *testing.T) {
		tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
			"a": {Type: ToolSchemaTypeString},
		})
		tl.schema.Required = []string{"a"}
		assert.Nil(t, MissingRequiredFields(tl, `{invalid json}`))
	})
}

// TestParseNBToolCallRequestFromInput mirrors the parsing rules callNbTool
// uses so a MissingFieldsResponder gets the same request shape regardless of
// which entry point the framework used. Non-JSON input → Command carries the
// raw string. JSON input → top-level "command" string becomes Command, every
// other top-level key becomes an Arguments entry.
func TestParseNBToolCallRequestFromInput(t *testing.T) {
	t.Run("non-JSON becomes Command", func(t *testing.T) {
		got := ParseNBToolCallRequestFromInput("kubectl get pods -n default")
		assert.Equal(t, "kubectl get pods -n default", got.Command)
		assert.Empty(t, got.Arguments)
	})

	t.Run("top-level command string becomes Command", func(t *testing.T) {
		got := ParseNBToolCallRequestFromInput(`{"command":"kubectl get pods"}`)
		assert.Equal(t, "kubectl get pods", got.Command)
		assert.Empty(t, got.Arguments)
	})

	t.Run("non-command keys land in Arguments", func(t *testing.T) {
		got := ParseNBToolCallRequestFromInput(`{"command":"select 1","database":"prod","limit":100}`)
		assert.Equal(t, "select 1", got.Command)
		assert.Equal(t, "prod", got.Arguments["database"])
		assert.EqualValues(t, 100, got.Arguments["limit"])
	})

	t.Run("no command key leaves Command empty", func(t *testing.T) {
		got := ParseNBToolCallRequestFromInput(`{"reasoning":"stuck on migration"}`)
		assert.Equal(t, "", got.Command)
		assert.Equal(t, "stuck on migration", got.Arguments["reasoning"])
	})

	t.Run("invalid JSON falls back to Command", func(t *testing.T) {
		got := ParseNBToolCallRequestFromInput(`{malformed`)
		assert.Equal(t, "{malformed", got.Command)
	})
}

// TestValidateToolInput_RequiredNullBypass pins the Gemini-review fix: a
// required field passed as explicit null must NOT bypass validation. The
// previous shape checked only `exists`, then checkSchemaType short-circuited
// on nil, so `{"command": null}` slipped through for a Required=["command"]
// tool. Now null-valued required fields produce a distinct error message
// (not the plain "missing" line) so the LLM can distinguish "you forgot to
// send it" from "you sent null".
func TestValidateToolInput_RequiredNullBypass(t *testing.T) {
	tl := newValidateTestTool("shell", map[string]ToolSchemaProperty{
		"command": {Type: ToolSchemaTypeString},
	})
	tl.schema.Required = []string{"command"}

	t.Run("explicit null on required field is rejected", func(t *testing.T) {
		got := ValidateToolInput(tl, `{"command": null}`)
		if assert.NotNil(t, got, "null on required field must not slip through") {
			assert.Contains(t, *got, `required field "command" must not be null`)
			assert.NotContains(t, *got, `missing required field "command"`,
				"null and missing produce different error lines so the LLM can tell them apart")
		}
	})

	t.Run("actually missing produces the missing-line, not null-line", func(t *testing.T) {
		got := ValidateToolInput(tl, `{"work_dir": "/tmp"}`)
		if assert.NotNil(t, got) {
			assert.Contains(t, *got, `missing required field "command"`)
			assert.NotContains(t, *got, `must not be null`)
		}
	})
}

// TestValidateToolInput_TypeErrorSuppressesEnumError pins the Gemini-review
// noise-reduction: when a field's type is wrong, the enum-membership check
// is skipped so the LLM sees one clear error per field ("expected string,
// got int") instead of a redundant tail ("value 42 not in allowed enum
// [pending running done]"). Two errors on the same field made the LLM's
// self-correction prompt noisier without adding information.
func TestValidateToolInput_TypeErrorSuppressesEnumError(t *testing.T) {
	tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
		"status": {
			Type: ToolSchemaTypeString,
			Enum: []any{"pending", "running", "done"},
		},
	})
	// Integer where string was expected — should ONLY produce the type error.
	got := ValidateToolInput(tl, `{"status": 42}`)
	if assert.NotNil(t, got) {
		assert.Contains(t, *got, `field "status" expected string, got`)
		assert.NotContains(t, *got, "not in allowed enum",
			"enum check must be suppressed when the type is wrong — one error per field")
	}
}

// TestValidateToolInput_EnumStillCheckedWhenTypeMatches verifies the
// suppression is scoped to type-mismatch — a correctly-typed but
// out-of-enum value still triggers the enum error.
func TestValidateToolInput_EnumStillCheckedWhenTypeMatches(t *testing.T) {
	tl := newValidateTestTool("t", map[string]ToolSchemaProperty{
		"status": {
			Type: ToolSchemaTypeString,
			Enum: []any{"pending", "running", "done"},
		},
	})
	got := ValidateToolInput(tl, `{"status": "banana"}`)
	if assert.NotNil(t, got) {
		assert.Contains(t, *got, "not in allowed enum")
		assert.NotContains(t, *got, "expected string, got",
			"type check must pass when value is a string")
	}
}
