package eventrule

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"strings"

	"github.com/noirbizarre/gonja"
	"github.com/noirbizarre/gonja/exec"
)

// escapeForJSON escapes a string so it can be safely embedded in a JSON string value
func escapeForJSON(s string) string {
	// Use json.Marshal to properly escape the string, then strip the surrounding quotes
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// Remove the surrounding quotes added by Marshal
	return string(b[1 : len(b)-1])
}

func init() {
	// Register custom split filter for gonja templates
	// This filter splits a string by a separator and returns an array of parts
	//
	// Usage examples:
	//   Get everything after first dash:
	//     {{ string | split(sep='-') | slice('1:') | join('-') }}
	//     Example: "production-air-worker" → "air-worker"
	//
	//   Get the prefix (first part):
	//     {{ string | split(sep='-') | first }}
	//     Example: "staging-air-worker" → "staging"
	//
	//   Get the suffix (last part):
	//     {{ string | split(sep='-') | last }}
	//     Example: "production-air-worker" → "worker"
	//
	//   Split by custom separator:
	//     {{ string | split(sep=':') }}
	//     Example: "key:value:data" → ["key", "value", "data"]
	err := gonja.DefaultEnv.Filters.Register("split", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(0, []*exec.KwArg{
			{Name: "sep", Default: exec.AsValue("-")},
		})
		if p.IsError() {
			return exec.AsValue(errors.New("wrong signature for 'split'"))
		}

		str := in.String()
		separator := p.KwArgs["sep"].String()

		parts := strings.Split(str, separator)

		// Convert []string to []interface{} for gonja
		result := make([]interface{}, len(parts))
		for i, part := range parts {
			result[i] = part
		}

		return exec.AsValue(result)
	})
	if err != nil {
		panic("failed to register split filter: " + err.Error())
	}

	// Register markdown filter to convert PlaybookActionResponse to Markdown format
	// Usage: {{ outputs['metrics_1'] | markdown }}
	err = gonja.DefaultEnv.Filters.Register("markdown", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		if in.IsNil() {
			return exec.AsValue("")
		}

		val := in.Interface()

		// Check if it's a PlaybookActionResponse
		switch resp := val.(type) {
		case playbooks.PlaybookActionResponseMarkdown:
			return exec.AsValue(escapeForJSON(resp.Text))

		case playbooks.PlaybookActionResponseJson:
			// Return JSON data escaped for embedding in JSON string
			return exec.AsValue(escapeForJSON(resp.Data))

		case playbooks.PlaybookActionResponseTable:
			// Convert table to Markdown format
			var sb strings.Builder
			if len(resp.Headers) > 0 {
				sb.WriteString("| ")
				sb.WriteString(strings.Join(resp.Headers, " | "))
				sb.WriteString(" |\n")
				sb.WriteString("|")
				for range resp.Headers {
					sb.WriteString(" --- |")
				}
				sb.WriteString("\n")
			}
			for _, row := range resp.Rows {
				sb.WriteString("| ")
				rowStrs := make([]string, len(row))
				for i, cell := range row {
					rowStrs[i] = fmt.Sprintf("%v", cell)
				}
				sb.WriteString(strings.Join(rowStrs, " | "))
				sb.WriteString(" |\n")
			}
			return exec.AsValue(escapeForJSON(sb.String()))

		case playbooks.PlaybookActionResponseFile:
			return exec.AsValue(escapeForJSON(resp.Data))

		default:
			return exec.AsValue(escapeForJSON(fmt.Sprintf("%v", val)))
		}
	})
	if err != nil {
		panic("failed to register markdown filter: " + err.Error())
	}

	// Register 'pluck' filter to extract a field from array of objects
	// Usage: {{ extracted_labels['action_0']['_series'] | pluck('status') }}
	// Result: ["400", "404", "429"]
	err = gonja.DefaultEnv.Filters.Register("pluck", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(1, []*exec.KwArg{})
		if p.IsError() {
			return exec.AsValue(errors.New("wrong signature for 'pluck', expected: array | pluck('field_name')"))
		}

		if in.IsNil() {
			return exec.AsValue([]interface{}{})
		}

		fieldName := p.Args[0].String()

		// Handle different input types
		var inputArray []interface{}
		switch v := in.Interface().(type) {
		case []interface{}:
			inputArray = v
		case []map[string]any:
			for _, item := range v {
				inputArray = append(inputArray, item)
			}
		default:
			return exec.AsValue(fmt.Errorf("pluck filter expects array, got %T", v))
		}

		result := make([]interface{}, 0, len(inputArray))
		for _, item := range inputArray {
			if itemMap, ok := item.(map[string]any); ok {
				if val, exists := itemMap[fieldName]; exists {
					result = append(result, val)
				}
			}
		}

		return exec.AsValue(result)
	})
	if err != nil {
		panic("failed to register pluck filter: " + err.Error())
	}

	// Register 'top' filter to take first N items from array
	// Usage: {{ some_array | top(5) }}
	// Result: First 5 items from the array
	err = gonja.DefaultEnv.Filters.Register("top", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(1, []*exec.KwArg{})
		if p.IsError() {
			return exec.AsValue(errors.New("wrong signature for 'top', expected: array | top(n)"))
		}

		if in.IsNil() {
			return exec.AsValue([]interface{}{})
		}

		n := p.Args[0].Integer()

		// Handle different input types
		var inputArray []interface{}
		switch v := in.Interface().(type) {
		case []interface{}:
			inputArray = v
		default:
			return exec.AsValue(fmt.Errorf("top filter expects array, got %T", v))
		}

		if n >= len(inputArray) {
			return exec.AsValue(inputArray)
		}

		return exec.AsValue(inputArray[:n])
	})
	if err != nil {
		panic("failed to register top filter: " + err.Error())
	}

	// Register comparison filters for use in boolean conditions
	// These return "true" or "false" strings for use in "if" fields

	// Register 'gt' (greater than) filter
	// Usage: {{ value | gt(5) }}  → "true" if value > 5, else "false"
	err = gonja.DefaultEnv.Filters.Register("gt", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(1, []*exec.KwArg{})
		if p.IsError() {
			return exec.AsValue("false")
		}

		threshold := p.Args[0].Float()
		value := in.Float()

		if value > threshold {
			return exec.AsValue("true")
		}
		return exec.AsValue("false")
	})
	if err != nil {
		panic("failed to register gt filter: " + err.Error())
	}

	// Register 'gte' (greater than or equal) filter
	// Usage: {{ value | gte(5) }}  → "true" if value >= 5, else "false"
	err = gonja.DefaultEnv.Filters.Register("gte", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(1, []*exec.KwArg{})
		if p.IsError() {
			return exec.AsValue("false")
		}

		threshold := p.Args[0].Float()
		value := in.Float()

		if value >= threshold {
			return exec.AsValue("true")
		}
		return exec.AsValue("false")
	})
	if err != nil {
		panic("failed to register gte filter: " + err.Error())
	}

	// Register 'lt' (less than) filter
	// Usage: {{ value | lt(5) }}  → "true" if value < 5, else "false"
	err = gonja.DefaultEnv.Filters.Register("lt", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(1, []*exec.KwArg{})
		if p.IsError() {
			return exec.AsValue("false")
		}

		threshold := p.Args[0].Float()
		value := in.Float()

		if value < threshold {
			return exec.AsValue("true")
		}
		return exec.AsValue("false")
	})
	if err != nil {
		panic("failed to register lt filter: " + err.Error())
	}

	// Register 'lte' (less than or equal) filter
	// Usage: {{ value | lte(5) }}  → "true" if value <= 5, else "false"
	err = gonja.DefaultEnv.Filters.Register("lte", func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		p := params.Expect(1, []*exec.KwArg{})
		if p.IsError() {
			return exec.AsValue("false")
		}

		threshold := p.Args[0].Float()
		value := in.Float()

		if value <= threshold {
			return exec.AsValue("true")
		}
		return exec.AsValue("false")
	})
	if err != nil {
		panic("failed to register lte filter: " + err.Error())
	}
}
