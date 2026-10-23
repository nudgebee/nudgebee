package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"nudgebee/runbook/internal/model"

	"github.com/nikolalohinski/gonja/v2"
)

var (
	templateBlockRe = regexp.MustCompile(`(?s)\{\{.*?\}\}|\{%.*?%\}`)
	rawSpanRe       = regexp.MustCompile(`(?s)\{%-?\s*raw\s*-?%\}.*?\{%-?\s*endraw\s*-?%\}`)
	stringLiteralRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	// engineNameRe matches a name renderGonja puts in the template context, standing on its own
	// rather than as a field (.Values) or another system's variable ($labels).
	engineNameRe     = regexp.MustCompile(`[^\w.$](Inputs|Tasks|Matrix|Secrets|Configs|Vars|State|Self|now|date|date_iso|date_us|time|time_hms|datetime|timestamp_iso)\b`)
	loneExpressionRe = regexp.MustCompile(`(?s)^\s*\{\{-?\s*(.*?)\s*-?\}\}\s*$`)
	identifierRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// scheduleSettings configure a schedule trigger; every other param is an input of the scheduled run.
var scheduleSettings = map[string]bool{"cron": true, "overlap_policy": true, "catchup_window": true}

// ValidateUnrenderedInputs refuses a template in a value the engine uses as written: an input's
// default, or an input a schedule trigger passes. Only step params are rendered; rendering a
// default as well would also render a caller's value, which is what a default holds by run time.
// Templates for other systems ({{ $labels.pod }}) pass, and so does a bare input name, which is
// also reachable from the root.
func ValidateUnrenderedInputs(def model.WorkflowDefinition) error {
	var defaults []string
	var fix string
	for _, input := range def.Inputs {
		if tpl, ok := engineTemplateIn(input.Default); ok {
			defaults = append(defaults, fmt.Sprintf("%s: %q", input.ID, tpl))
			if fix == "" {
				fix = fallbackFix(input.ID, input.Default)
			}
		}
	}
	if len(defaults) > 0 {
		return fmt.Errorf("an input default is used as written and never rendered (%s): drop the default, leave the input not required, and %s", strings.Join(defaults, ", "), fix)
	}

	var passed []string
	for i, trigger := range def.Triggers {
		if trigger.Type != model.WorkflowTriggerSchedule {
			continue
		}
		keys := make([]string, 0, len(trigger.Params))
		for key := range trigger.Params {
			if !scheduleSettings[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if tpl, ok := engineTemplateIn(trigger.Params[key]); ok {
				passed = append(passed, fmt.Sprintf("schedule trigger index %d, %s: %q", i, key, tpl))
				if fix == "" {
					fix = fallbackFix(key, trigger.Params[key])
				}
			}
		}
	}
	if len(passed) > 0 {
		return fmt.Errorf("an input a schedule trigger passes is used as written and never rendered (%s): stop passing it from the schedule, and %s", strings.Join(passed, ", "), fix)
	}
	return nil
}

// fallbackFix says how to keep the input as an override while the template supplies its value:
// an input with no default is nil in the template context, so `or` falls through to the template.
func fallbackFix(id string, value any) string {
	tpl, _ := value.(string)
	m := loneExpressionRe.FindStringSubmatch(tpl)
	if m == nil || strings.Contains(m[1], "}}") || strings.Contains(m[1], "{{") {
		return "put the template in the params of the step that reads the input instead"
	}
	ref := "Inputs." + id
	if !identifierRe.MatchString(id) {
		ref = fmt.Sprintf("Inputs[%q]", id)
	}
	return fmt.Sprintf("have the step that reads the input fall back to the template instead, as in {{ %s or %s }}", ref, m[1])
}

// engineTemplateIn returns the first string in v, or nested in its maps and lists, holding a block
// that names the engine's template context.
func engineTemplateIn(v any) (string, bool) {
	switch val := v.(type) {
	case string:
		if holdsEngineTemplate(val) {
			return val, true
		}
	case map[string]any:
		keys := make([]string, 0, len(val))
		for key := range val {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if tpl, ok := engineTemplateIn(val[key]); ok {
				return tpl, true
			}
		}
	case []any:
		for _, item := range val {
			if tpl, ok := engineTemplateIn(item); ok {
				return tpl, true
			}
		}
	}
	return "", false
}

// holdsEngineTemplate reports whether s has a block the engine would render that names its
// template context. An expression block it cannot parse (Sprig, Go templates, Handlebars) is
// another system's text, and so are raw spans and names inside quoted strings.
func holdsEngineTemplate(s string) bool {
	for _, block := range templateBlockRe.FindAllString(rawSpanRe.ReplaceAllString(s, ""), -1) {
		if strings.HasPrefix(block, "{{") {
			if _, err := gonja.FromString(block); err != nil {
				continue
			}
		}
		if engineNameRe.MatchString(stringLiteralRe.ReplaceAllString(block, `""`)) {
			return true
		}
	}
	return false
}
