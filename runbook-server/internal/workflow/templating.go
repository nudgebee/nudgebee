package workflow

import (
	"bytes"
	"fmt"
	"nudgebee/runbook/internal/model"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
)

// TemplateContext holds the data for templating.
type TemplateContext struct {
	Inputs         map[string]any
	Tasks          map[string]map[string]any
	Matrix         map[string]any // Added for matrix task templating
	Secrets        map[string]any // Added for secrets templating
	Configs        map[string]any // Added for configs templating
	Vars           map[string]any // Unified context for all workflow variables
	State          map[string]any // Unified context for all persistent workflow state
	Self           map[string]any // Context for the current task's own data
	templateEngine string
}

func NewTemplateContextWithEngine(inputs []model.Input, runtimeInputs map[string]any, engine string) *TemplateContext {

	if engine == "" {
		engine = "jinja"
	}

	if engine != "go" && engine != "jinja" {
		return nil
	}

	inputMap := make(map[string]any)
	for _, i := range inputs {
		inputMap[i.ID] = i.Default
	}

	for k, v := range runtimeInputs {
		inputMap[k] = v
	}

	secretsMap := make(map[string]any)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			key := parts[0]
			value := parts[1]
			if strings.HasPrefix(key, "SECRET_") {
				secretName := strings.TrimPrefix(key, "SECRET_")
				secretsMap[secretName] = value
			}
		}
	}

	return &TemplateContext{
		Inputs:         inputMap,
		Tasks:          make(map[string]map[string]any),
		Matrix:         make(map[string]any), // Initialize Matrix
		Secrets:        secretsMap,
		Configs:        make(map[string]any),
		Vars:           inputMap, // Initialize Vars with inputs
		State:          make(map[string]any),
		templateEngine: engine,
	}
}

func NewTemplateContext(inputs []model.Input, runtimeInputs map[string]any) *TemplateContext {
	return NewTemplateContextWithEngine(inputs, runtimeInputs, "jinja")
}

// Render renders a template string using the default (Go) engine.
func (c *TemplateContext) Render(tpl string) (string, error) {
	if c.templateEngine == "go" {
		return c.renderGo(tpl)
	}
	return c.renderGonja(tpl)
}

// renderGo renders a template string using Go's text/template engine.
func (c *TemplateContext) renderGo(tpl string) (string, error) {
	t, err := template.New("").Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, c); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// renderGonja renders a template string using the Gonja (Jinja2) engine.
func (c *TemplateContext) renderGonja(tpl string) (str string, err error) {
	// Parse the template
	t, err := gonja.FromString(tpl)
	if err != nil {
		return "", err
	}

	// Built-in dynamic values evaluated once per render, so a subject like
	// `Error Log Summary_{{ datetime }}` resolves to a stable string at task time.
	nowUTC := time.Now().UTC()

	// Create the context map
	data := map[string]interface{}{
		"Inputs":        c.Inputs,
		"Tasks":         c.Tasks,
		"Matrix":        c.Matrix,
		"Secrets":       c.Secrets,
		"Configs":       c.Configs,
		"Vars":          c.Vars,
		"State":         c.State,
		"Self":          c.Self,
		"now":           func() time.Time { return time.Now().UTC() },
		"date":          nowUTC.Format("02012006"),
		"date_iso":      nowUTC.Format("2006-01-02"),
		"date_us":       nowUTC.Format("01/02/2006"),
		"time":          nowUTC.Format("1504"),
		"time_hms":      nowUTC.Format("15:04:05"),
		"datetime":      nowUTC.Format("02012006_1504"),
		"timestamp_iso": nowUTC.Format(time.RFC3339),
	}

	// Flatten Vars into root to allow direct access (e.g. {{ LoopItem }})
	for k, v := range c.Vars {
		data[k] = v
	}

	ctx := exec.NewContext(data)

	var buf bytes.Buffer

	// Recover from panics in filters
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("template execution panic: %v", r)
			}
		}
	}()

	// Gonja's Execute method writes to the provided writer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// RenderMap renders a map of templates.
func (c *TemplateContext) RenderMap(m map[string]any) (map[string]any, error) {
	rendered := make(map[string]any)
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			rendered[k] = v
			continue
		}
		rendered[k], _ = c.Render(s)
	}
	return rendered, nil
}

func (c *TemplateContext) Clone() *TemplateContext {
	clone := &TemplateContext{
		Inputs:  make(map[string]any),
		Tasks:   make(map[string]map[string]any),
		Secrets: make(map[string]any),
		Configs: make(map[string]any),
		Vars:    make(map[string]any), // Initialize Vars in clone
		State:   make(map[string]any), // Initialize State in clone
		Self:    make(map[string]any), // Initialize Self in clone
	}
	for k, v := range c.Inputs {
		clone.Inputs[k] = v
	}
	for k, v := range c.Tasks {
		clone.Tasks[k] = v
	}
	for k, v := range c.Secrets {
		clone.Secrets[k] = v
	}
	for k, v := range c.Configs {
		clone.Configs[k] = v
	}
	for k, v := range c.Vars { // Copy Vars from original
		clone.Vars[k] = v
	}
	for k, v := range c.State { // Copy State from original
		clone.State[k] = v
	}
	for k, v := range c.Self { // Copy Self from original
		clone.Self[k] = v
	}
	return clone
}

func Render(tpl string, ctx *TemplateContext) (string, error) {
	return ctx.Render(tpl)
}

func ProcessValue(value any, ctx *TemplateContext) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch v := value.(type) {
	case string:
		// Optimization: if the string is a simple variable expression, resolve it directly
		// to preserve the original type (e.g. map, slice) instead of rendering to string.
		if val, ok := ctx.EvaluateSimpleVariable(v); ok {
			return val, nil
		}
		renderedString, err := ctx.Render(v)
		if err != nil {
			return nil, err
		}
		return renderedString, nil
	case map[string]any:
		newMap := make(map[string]any)
		for key, val := range v {
			// Keys are not rendered, only values
			processedVal, err := ProcessValue(val, ctx)
			if err != nil {
				return nil, err
			}
			newMap[key] = processedVal
		}
		return newMap, nil
	case []any:
		newSlice := make([]any, len(v))
		for i, val := range v {
			processedVal, err := ProcessValue(val, ctx)
			if err != nil {
				return nil, err
			}
			newSlice[i] = processedVal
		}
		return newSlice, nil
	default:
		return v, nil
	}
}

// generateMatrixCombinations takes a matrix map (e.g., {"fruit": ["apple", "banana"], "color": ["red", "green"]})
// and returns a slice of maps, where each inner map is a single combination
// (e.g., [{"fruit": "apple", "color": "red"}, {"fruit": "apple", "color": "green"}, ...]).
func generateMatrixCombinations(matrix map[string]any) ([]map[string]any, error) {
	if len(matrix) == 0 {
		return []map[string]any{{}}, nil
	}

	var keys []string
	var valueLists [][]any

	for key, val := range matrix {
		keys = append(keys, key)
		list, ok := val.([]any)
		if !ok {
			// If a matrix value is not a slice, treat it as a single-element slice
			valueLists = append(valueLists, []any{val})
		} else {
			valueLists = append(valueLists, list)
		}
	}

	var combinations []map[string]any
	var generate func(index int, currentCombination map[string]any)

	generate = func(index int, currentCombination map[string]any) {
		if index == len(keys) {
			// Make a copy of the current combination to avoid modification issues
			comboCopy := make(map[string]any)
			for k, v := range currentCombination {
				comboCopy[k] = v
			}
			combinations = append(combinations, comboCopy)
			return
		}

		key := keys[index]
		for _, value := range valueLists[index] {
			currentCombination[key] = value
			generate(index+1, currentCombination)
		}
	}

	generate(0, make(map[string]any))
	return combinations, nil
}

// EvaluateSimpleVariable resolves a simple variable expression (e.g. "{{ Inputs.myList }}" or "{{ Tasks['my-task'].output }}")
// to its raw Go value from the context. It supports dot notation and bracket notation.
// It returns the value and a boolean indicating if resolution was successful.
func (c *TemplateContext) EvaluateSimpleVariable(expr string) (any, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "{{") || !strings.HasSuffix(expr, "}}") {
		return nil, false
	}
	expr = strings.TrimSpace(expr[2 : len(expr)-2])

	keys, err := parseVariableExpression(expr)
	if err != nil || len(keys) == 0 {
		return nil, false
	}

	var current any
	root := keys[0]

	switch root {
	case "Inputs":
		current = c.Inputs
	case "Vars":
		current = c.Vars
	case "Secrets":
		current = c.Secrets
	case "Configs":
		current = c.Configs
	case "State":
		current = c.State
	case "Self":
		current = c.Self
	case "Tasks":
		current = c.Tasks
	default:
		return nil, false
	}

	for i := 1; i < len(keys); i++ {
		key := keys[i]

		// Special handling for Tasks map which might be map[string]map[string]any or map[string]any
		if nestedTasksMap, ok := current.(map[string]map[string]any); ok {
			val, ok := nestedTasksMap[key]
			if !ok {
				return nil, false
			}
			current = val
			continue
		}

		if m, ok := current.(map[string]any); ok {
			val, ok := m[key]
			if !ok {
				return nil, false
			}
			current = val
			continue
		}

		return nil, false
	}

	return current, true
}

func parseVariableExpression(expr string) ([]string, error) {
	var keys []string
	var buffer strings.Builder
	inBrackets := false
	inQuotes := false
	var quoteChar rune

	for i, r := range expr {
		if inQuotes {
			if r == quoteChar {
				inQuotes = false
			} else {
				buffer.WriteRune(r)
			}
			continue
		}

		switch r {
		case '[':
			if !inBrackets {
				if buffer.Len() > 0 {
					keys = append(keys, buffer.String())
					buffer.Reset()
				}
				inBrackets = true
			} else {
				buffer.WriteRune(r)
			}
		case ']':
			if inBrackets {
				if buffer.Len() > 0 {
					keys = append(keys, buffer.String())
					buffer.Reset()
				}
				inBrackets = false
			} else {
				buffer.WriteRune(r)
			}
		case '.':
			if !inBrackets {
				if buffer.Len() > 0 {
					keys = append(keys, buffer.String())
					buffer.Reset()
				}
			} else {
				buffer.WriteRune(r)
			}
		case '"', '\'':
			if inBrackets {
				inQuotes = true
				quoteChar = r
			} else {
				buffer.WriteRune(r)
			}
		default:
			buffer.WriteRune(r)
		}

		// Handle last character
		if i == len(expr)-1 && buffer.Len() > 0 && !inBrackets {
			keys = append(keys, buffer.String())
		}
	}

	if inBrackets || inQuotes {
		return nil, fmt.Errorf("malformed expression: unbalanced brackets or quotes")
	}

	return keys, nil
}
