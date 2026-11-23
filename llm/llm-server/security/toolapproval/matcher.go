package toolapproval

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/google/shlex"
)

const (
	MaxPatternBytes = 1024
	MaxConditions   = 16
	MaxInputBytes   = 64 * 1024
	MaxRulesPerTool = 50
	MaxRulesTenant  = 200
)

var executableArgumentRoots = map[string]struct{}{
	"command": {}, "commands": {}, "cmd": {}, "query": {}, "sql": {}, "script": {}, "code": {},
	"args": {}, "argv": {},
}

// IsExecutableField reports a field that carries a CLI command or code. An
// argument condition on it splits every value into the commands it runs, as
// the command rules do, and each of them must match.
func IsExecutableField(name string) bool {
	_, executable := executableArgumentRoots[strings.ToLower(strings.TrimSpace(name))]
	return executable
}

func ValidateRule(rule Rule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.ToolName = strings.TrimSpace(rule.ToolName)
	if rule.ToolConfigID != nil {
		trimmed := strings.TrimSpace(*rule.ToolConfigID)
		if trimmed == "" || trimmed == UnresolvedTargetID {
			return errors.New("tool approval: tool_config_id must identify a resolved execution target")
		}
	}
	if rule.Name == "" {
		return errors.New("tool approval: name is required")
	}
	if rule.ToolName == "" {
		return errors.New("tool approval: tool_name is required")
	}
	switch rule.MatchKind {
	case MatchTool:
		return nil
	case MatchExactCommand, MatchCommandRegex:
		if rule.CommandPattern == nil || strings.TrimSpace(*rule.CommandPattern) == "" {
			return errors.New("tool approval: command_pattern is required")
		}
		if len(*rule.CommandPattern) > MaxPatternBytes {
			return fmt.Errorf("tool approval: command_pattern exceeds %d bytes", MaxPatternBytes)
		}
		if strings.EqualFold(rule.ToolName, "shell_execute") && (rule.WorkingDir == nil || strings.TrimSpace(*rule.WorkingDir) == "") {
			return errors.New("tool approval: shell command rules require an explicit working_dir")
		}
		if rule.MatchKind == MatchExactCommand {
			if _, err := commandTokens(*rule.CommandPattern); err != nil {
				return fmt.Errorf("tool approval: invalid exact command: %w", err)
			}
		} else if _, err := compileFullMatch(*rule.CommandPattern); err != nil {
			return fmt.Errorf("tool approval: invalid command regex: %w", err)
		}
		return nil
	case MatchArguments:
		conditions, err := decodeConditions(rule.Conditions)
		if err != nil {
			return err
		}
		if len(conditions) == 0 || len(conditions) > MaxConditions {
			return fmt.Errorf("tool approval: conditions must contain 1-%d entries", MaxConditions)
		}
		for _, condition := range conditions {
			if strings.TrimSpace(condition.Path) == "" {
				return errors.New("tool approval: condition path is required")
			}
			switch condition.Operator {
			case OperatorEquals:
				if len(condition.Value) == 0 || !json.Valid(condition.Value) {
					return errors.New("tool approval: equals condition value must be valid JSON")
				}
			case OperatorRegex:
				var pattern string
				if err := json.Unmarshal(condition.Value, &pattern); err != nil {
					return errors.New("tool approval: regex condition value must be a string")
				}
				if len(pattern) > MaxPatternBytes {
					return fmt.Errorf("tool approval: regex exceeds %d bytes", MaxPatternBytes)
				}
				if _, err := compileFullMatch(pattern); err != nil {
					return fmt.Errorf("tool approval: invalid condition regex: %w", err)
				}
			default:
				return fmt.Errorf("tool approval: unsupported operator %q", condition.Operator)
			}
		}
		return nil
	default:
		return fmt.Errorf("tool approval: unsupported match_kind %q", rule.MatchKind)
	}
}

const UnresolvedTargetID = "__unresolved_tool_config__"

func MatchRule(rule Rule, toolName, input, toolConfigID string) (bool, string) {
	if !rule.Enabled || !strings.EqualFold(strings.TrimSpace(rule.ToolName), strings.TrimSpace(toolName)) || len(input) > MaxInputBytes {
		return false, "tool or input does not match"
	}
	if err := ValidateRule(rule); err != nil {
		return false, err.Error()
	}
	if toolConfigID == UnresolvedTargetID {
		return false, "execution target is unresolved"
	}
	if toolConfigID != "" {
		// An unpinned organisation rule covers whichever target the caller
		// was already authorized to use; a pinned one only its integration.
		if rule.ToolConfigID == nil && !rule.AllAccounts() {
			return false, "execution target does not match"
		}
		if rule.ToolConfigID != nil && strings.TrimSpace(*rule.ToolConfigID) != toolConfigID {
			return false, "execution target does not match"
		}
	} else if rule.ToolConfigID != nil {
		return false, "rule requires a different execution target"
	}
	if rule.MatchKind == MatchTool {
		return true, "entire tool allowed"
	}
	payload, command, workingDir, ok := parseInput(input)
	if rule.MatchKind == MatchExactCommand || rule.MatchKind == MatchCommandRegex {
		if !ok || command == "" {
			return false, "command field is missing"
		}
		if rule.WorkingDir != nil && strings.TrimSpace(*rule.WorkingDir) != strings.TrimSpace(workingDir) {
			return false, "working directory does not match"
		}
		// A chain matches only when every command in it does.
		if rule.MatchKind == MatchExactCommand {
			invocation, err := commandTokens(command)
			if err != nil {
				return false, "command syntax requires manual approval"
			}
			allowed, _ := commandTokens(*rule.CommandPattern)
			for _, part := range invocation {
				if !slices.ContainsFunc(allowed, func(tokens []string) bool { return slices.Equal(tokens, part) }) {
					return false, "command does not match"
				}
			}
			return true, "exact command matched"
		}
		parts, err := splitCommands(command)
		if err != nil {
			return false, "command syntax requires manual approval"
		}
		re, err := compileFullMatch(*rule.CommandPattern)
		if err != nil {
			return false, "command regex did not match"
		}
		for _, part := range parts {
			if !re.MatchString(part) {
				return false, "command regex did not match"
			}
		}
		return true, "command regex matched"
	}

	// Only the named fields are checked; any other field is approved with the
	// value sent, by design (the editor says so).
	conditions, _ := decodeConditions(rule.Conditions)
	for _, condition := range conditions {
		value, found := lookupPath(payload, condition.Path)
		if !found {
			return false, "argument path is missing"
		}
		root := strings.Split(strings.TrimSpace(condition.Path), ".")[0]
		var commands []string
		if IsExecutableField(root) {
			var ok bool
			if commands, ok = commandValues(value); !ok {
				return false, "command syntax requires manual approval"
			}
		}
		switch condition.Operator {
		case OperatorEquals:
			// Compare values, not bytes: Postgres stores conditions as jsonb,
			// which reorders object keys and keeps numbers such as 1.50.
			var expected any
			if err := json.Unmarshal(condition.Value, &expected); err != nil || !reflect.DeepEqual(value, expected) {
				return false, "argument value does not match"
			}
		case OperatorRegex:
			var pattern string
			_ = json.Unmarshal(condition.Value, &pattern)
			re, err := compileFullMatch(pattern)
			if err != nil {
				return false, "argument regex did not match"
			}
			// A command list matches only when every command does.
			if commands == nil {
				actual, ok := value.(string)
				if !ok {
					return false, "regex argument is not a string"
				}
				commands = []string{actual}
			}
			for _, command := range commands {
				if !re.MatchString(strings.TrimSpace(command)) {
					return false, "argument regex did not match"
				}
			}
		}
	}
	return true, "argument conditions matched"
}

// compileFullMatch compiles pattern so it must match the whole input. The
// pattern is compiled on its own first: an unbalanced group such as "a)|(b"
// would otherwise close the wrapper early and turn "^(?:a)|(b)$" into an
// unanchored alternation.
func compileFullMatch(pattern string) (*regexp.Regexp, error) {
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, err
	}
	return regexp.Compile("^(?:" + pattern + ")$")
}

// commandValues returns the commands a command field's value runs: a string,
// or a list of strings, each split at its chain operators. ok is false for a
// command substitution or a value that is neither.
func commandValues(value any) ([]string, bool) {
	var values []string
	switch typed := value.(type) {
	case string:
		values = []string{typed}
	case []any:
		for _, item := range typed {
			command, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, command)
		}
	default:
		return nil, false
	}
	var commands []string
	for _, value := range values {
		parts, err := splitCommands(value)
		if err != nil {
			return nil, false
		}
		commands = append(commands, parts...)
	}
	return commands, len(commands) > 0
}

func parseInput(input string) (map[string]any, string, string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "{") {
		return map[string]any{"command": input}, input, "", true
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return nil, "", "", false
	}
	command, _ := payload["command"].(string)
	workingDir, _ := payload["work_dir"].(string)
	return payload, command, workingDir, true
}

// splitCommands splits a command line into the commands it runs: a chain
// (`;`, `&&`, `||`, `|`, `&`, a newline) gives one entry per command, while a
// redirect such as `2>&1`, `&>` or `>|` stays part of its command. A command
// substitution (`$(…)`, backticks, `<(…)`, `>(…)`) or a subshell runs a command
// that cannot be matched on its own, so it is an error and the call asks.
func splitCommands(command string) ([]string, error) {
	runes := []rune(command)
	var parts []string
	var current strings.Builder
	flush := func() {
		if part := strings.TrimSpace(current.String()); part != "" {
			parts = append(parts, part)
		}
		current.Reset()
	}
	next := func(i int) rune {
		if i+1 < len(runes) {
			return runes[i+1]
		}
		return 0
	}
	quote := rune(0)
	// afterRedirect is set only by an unquoted, unescaped `>` or `<`, so an
	// escaped `\>` followed by `&` still splits.
	afterRedirect := false
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		wasRedirect := afterRedirect
		afterRedirect = false
		if quote == '\'' {
			current.WriteRune(r)
			if r == '\'' {
				quote = 0
			}
			continue
		}
		if r == '\\' && i+1 < len(runes) {
			current.WriteRune(r)
			current.WriteRune(runes[i+1])
			i++
			continue
		}
		if r == '`' || (r == '$' && next(i) == '(') {
			return nil, errors.New("command substitution is not supported")
		}
		if quote == '"' {
			current.WriteRune(r)
			if r == '"' {
				quote = 0
			}
			continue
		}
		switch {
		case r == '$' && (next(i) == '\'' || next(i) == '"'):
			// $'…' and $"…" quote differently from '…' and "…".
			return nil, errors.New("$'…' and $\"…\" quoting is not supported")
		case r == '\'' || r == '"':
			quote = r
		case r == '>' || r == '<':
			afterRedirect = true
		case r == '(' || r == ')':
			return nil, errors.New("subshells and process substitution are not supported")
		case r == ';' || r == '\n' || r == '\r':
			flush()
			continue
		case r == '&' && (wasRedirect || next(i) == '>'):
			// part of a redirect: 2>&1, <&3, &>file
		case r == '&':
			if next(i) == '&' {
				i++
			}
			flush()
			continue
		case r == '|' && wasRedirect:
			// >| file
		case r == '|':
			if next(i) == '|' || next(i) == '&' {
				i++
			}
			flush()
			continue
		}
		current.WriteRune(r)
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	flush()
	if len(parts) == 0 {
		return nil, errors.New("empty command")
	}
	return parts, nil
}

// commandTokens splits a command line into its commands and each command into
// words, for comparing exact commands.
func commandTokens(command string) ([][]string, error) {
	parts, err := splitCommands(command)
	if err != nil {
		return nil, err
	}
	commands := make([][]string, 0, len(parts))
	for _, part := range parts {
		if strings.Contains(part, "\"") && strings.Contains(part, "\\") && backslashInDoubleQuotes(part) {
			// shlex drops a backslash before any character inside double
			// quotes, while the shell keeps it unless the next character is
			// special, so the two would disagree on the argument.
			return nil, errors.New("backslashes inside double quotes are not supported")
		}
		tokens, err := shlex.Split(part)
		if err != nil || len(tokens) == 0 {
			return nil, errors.New("command cannot be parsed")
		}
		commands = append(commands, tokens)
	}
	return commands, nil
}

func backslashInDoubleQuotes(command string) bool {
	quote := rune(0)
	escaped := false
	for _, r := range command {
		switch {
		case escaped:
			escaped = false
		case quote == '"' && r == '\\':
			return true
		case quote == 0 && r == '\\':
			escaped = true
		case (r == '\'' || r == '"') && quote == 0:
			quote = r
		case r == quote:
			quote = 0
		}
	}
	return false
}

func decodeConditions(raw json.RawMessage) ([]Condition, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var conditions []Condition
	if err := json.Unmarshal(raw, &conditions); err != nil {
		return nil, fmt.Errorf("tool approval: invalid conditions: %w", err)
	}
	return conditions, nil
}

func lookupPath(payload map[string]any, path string) (any, bool) {
	var current any = payload
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
