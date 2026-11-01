package tools

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/shlex"
)

const (
	preContextLineCount  = 2
	postContextLineCount = 5
)

// cliErrorPrefixRe matches CLI-style error prefixes (`error:`, `fatal:`,
// `usage:`) only when they appear at start-of-string or after whitespace —
// NOT after a `"` character. This is what separates real stderr prefixes
// like `error: unknown flag` from JSON-payload keys like `{"error":"..."}`.
// Without this anchor, every relay-wrapped opaque error (`{"error":"connection
// timeout"}`) would falsely register as CLI signal and get wrapped in the
// generic recovery hint, defeating the pass-through goal.
var cliErrorPrefixRe = regexp.MustCompile(`(?:^|[ \t\n\r])(?:error|fatal|usage):`)

// wrapCliError packages a raw CLI error together with an actionable recovery
// hint as a JSON {error_hint, original_error} envelope for the planner
// observation. When hint is empty it returns rawError unchanged, so callers can
// pass the result of a tool-specific hint function and only pay the envelope
// when there is something useful to say. This is the shared skeleton behind the
// per-tool hint functions (shellErrorHint, githubErrorHint, ...): each tool
// supplies its own hint text — including its own help invocation, since help
// syntax differs per CLI (`gh <sub> --help`, `kubectl <cmd> --help`, etc.).
func wrapCliError(rawError, hint string) string {
	if strings.TrimSpace(hint) == "" {
		return rawError
	}
	wrapped := map[string]string{
		"error_hint":     hint,
		"original_error": rawError,
	}
	body, err := common.MarshalJson(wrapped)
	if err != nil {
		// Marshal can't realistically fail on a string-only map, but degrade
		// gracefully rather than masking the underlying error.
		return rawError
	}
	return string(body)
}

// genericCliRecoveryHint returns a universal "read the error before switching
// commands" nudge for CLI-shape tool failures that don't match any
// tool-specific hint. Steers the model to correct THIS command rather than
// pivoting to a different tool — the dominant recoverable-failure misuse
// pattern surfaced across the 2026-06-22 and 2026-07-02 DB sweeps.
//
// Fires ONLY when the raw error carries CLI signal (usage/available/unknown/
// invalid/did-you-mean/expected/not a valid/error:/fatal:), so genuinely
// opaque errors (network, backend timeouts) don't get wrapped in noise.
//
// The optional helpInvocation is the tool-specific way to see valid syntax.
// CLI help conventions are NOT uniform — DO NOT hardcode `--help` (Cobra),
// sqlcmd uses `-?`, sqlplus uses `-H`, mysql/psql have `\h`/`\?` in
// interactive mode, and some tools (raw HTTP, SQL query wrappers) have no
// useful CLI help mechanism at all. Pass "" to omit the help suggestion.
func genericCliRecoveryHint(rawError, toolDisplayName, helpInvocation string) string {
	trimmed := strings.TrimSpace(rawError)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	// Boundary-anchor the colon-suffixed prefixes so JSON keys like
	// `{"error":"connection timeout"}` don't false-positive into the signal
	// bucket — the shape shows up in every relay-wrapped opaque error, and
	// treating `"error":` as a CLI-error prefix would wrap those too, defeating
	// the pass-through goal. Match `error:` / `fatal:` / `usage:` only at
	// start-of-string or after horizontal whitespace / newline (never after
	// a `"`, which is what distinguishes CLI prefixes from JSON keys).
	hasSignal := cliErrorPrefixRe.MatchString(lower) ||
		strings.Contains(lower, "available") ||
		strings.Contains(lower, "unknown") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "did you mean") ||
		strings.Contains(lower, "expected") ||
		strings.Contains(lower, "not a valid") ||
		strings.Contains(lower, "syntax error")
	if !hasSignal {
		return ""
	}
	base := "Before switching to a different command, re-read the raw output above — CLI tools typically print the correct usage / valid values on error. Correct THIS command rather than pivoting to something else."
	if toolDisplayName != "" && helpInvocation != "" {
		base += fmt.Sprintf(" If you're unsure of the syntax for %s, run `%s` to see valid options.", toolDisplayName, helpInvocation)
	}
	return base
}

// cliRecoveryEnvelope is a convenience wrapper that combines a tool-specific
// hint (may be empty) with the generic CLI-recovery fallback and packages the
// result through wrapCliError. New callers should prefer this over the
// two-step compose-then-wrap idiom used by PR #33404's initial rollout —
// same behavior, fewer moving parts.
//
// The specific hint always wins when present; the generic hint fires only
// when the specific hint is empty AND the raw error carries CLI signal.
// When both are empty, rawError is returned unchanged (no envelope).
func cliRecoveryEnvelope(rawError, specificHint, toolDisplayName, helpInvocation string) string {
	hint := specificHint
	if strings.TrimSpace(hint) == "" {
		hint = genericCliRecoveryHint(rawError, toolDisplayName, helpInvocation)
	}
	return wrapCliError(rawError, hint)
}

func convertCsvToJsonString(toolContext core.NbToolContext, csvData string, seprator rune) string {
	reader := csv.NewReader(strings.NewReader(csvData))
	reader.Comma = rune(seprator)
	records, err := reader.ReadAll()

	if err != nil {
		slog.Warn("Unable to parse CSV:", "error", err)
		return csvData
	}

	// Remove BOM character if present
	if len(records) > 0 && len(records[0]) > 0 {
		records[0][0] = strings.TrimPrefix(records[0][0], "\ufeff")
	}

	if len(records) < 2 {
		return csvData
	}

	headers := records[0]
	var jsonArray []map[string]string

	for _, row := range records[1:] {
		if len(row) != len(headers) {
			toolContext.Ctx.GetLogger().Error("Row length mismatch:", "row", slog.AnyValue(row))
			continue
		}
		rowMap := make(map[string]string)
		for i, value := range row {
			rowMap[headers[i]] = value
		}
		jsonArray = append(jsonArray, rowMap)
	}

	jsonData, err := common.MarshalJson(jsonArray)
	if err != nil {
		toolContext.Ctx.GetLogger().Error("Error marshaling JSON:", "error", err)
		return csvData
	}

	return string(jsonData)
}

func configValueExists(configs []core.ToolConfigValue, name string) bool {
	for _, cfg := range configs {
		if cfg.Name == name && cfg.Value != "" {
			return true
		}
	}
	return false
}

func ExecuteCliCommand(toolContext core.NbToolContext, command string, env []string, allowedCommands []string) (string, error) {

	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty command")
	}

	var pipelineStages []string

	// gh/glab + jq special case
	if isGitCliCommandWithJq(command) {
		pipelineStages = []string{command}
	} else {
		pipelineStages = strings.Split(command, "|")
	}

	cmdCtx := toolContext.Ctx.GetContext()

	cmds := make([]*exec.Cmd, len(pipelineStages))

	for i, stageStr := range pipelineStages {
		args, err := shlex.Split(strings.TrimSpace(stageStr))
		if err != nil || len(args) == 0 {
			return "", fmt.Errorf("failed to parse pipeline stage %d: %q", i, stageStr)
		}

		if len(allowedCommands) > 0 {
			isAllowed := false
			for _, allowed := range allowedCommands {
				if args[0] == allowed {
					isAllowed = true
					break
				}
			}
			if !isAllowed {
				return "", fmt.Errorf("command '%s' is not allowed", args[0])
			}
		}

		cmds[i] = exec.CommandContext(cmdCtx, args[0], args[1:]...)
		cmds[i].Env = append(env, "PATH="+os.Getenv("PATH"))
	}

	for i := 0; i < len(cmds)-1; i++ {
		stdoutPipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("failed to create stdout pipe for stage %d: %w", i, err)
		}
		cmds[i+1].Stdin = stdoutPipe
	}

	var finalStdout bytes.Buffer
	cmds[len(cmds)-1].Stdout = &finalStdout
	// Each command gets its own stderr buffer. Sharing a single buffer across
	// stages is a data race: os/exec spawns an independent copy goroutine per
	// command, and they would write the same buffer concurrently. We aggregate
	// the per-stage buffers after Wait() (which guarantees each copy goroutine
	// has finished) into the combined stderr string.
	stderrBufs := make([]bytes.Buffer, len(cmds))
	for i := range cmds {
		cmds[i].Stderr = &stderrBufs[i]
	}

	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start command %q: %w", cmd.String(), err)
		}
	}

	var finalErr error
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			if finalErr == nil {
				finalErr = fmt.Errorf("pipeline stage %q failed: %w", cmd.String(), err)
			}
		}
	}

	var combinedStderrBuilder strings.Builder
	for i := range stderrBufs {
		combinedStderrBuilder.WriteString(stderrBufs[i].String())
	}
	combinedStderr := combinedStderrBuilder.String()

	if finalErr != nil {
		toolContext.Ctx.GetLogger().Error("tools: cli command execution failed", "error", finalErr, "stderr", combinedStderr, "command", command)
		return finalStdout.String(), fmt.Errorf("tools: cli command failed: %v\nStderr: %s", finalErr, combinedStderr)
	}

	return finalStdout.String(), nil
}

func IsErrorLogLine(log string) bool {
	return ClassifyLogSeverity(log) != LogSeverityNone
}

// LogSeverity represents the severity level of a log line.
type LogSeverity int

const (
	LogSeverityNone    LogSeverity = 0
	LogSeverityWarning LogSeverity = 1
	LogSeverityError   LogSeverity = 2
)

// ClassifyLogSeverity returns the severity of a log line, distinguishing
// ERROR/PANIC/FATAL (high severity) from WARNING (lower severity).
func ClassifyLogSeverity(log string) LogSeverity {
	parts := strings.Fields(strings.ToUpper(log))
	hasWarning := false
	for _, part := range parts {
		// Exact matches first
		if part == "E" || part == "ERROR" || part == "[E]" || part == "[ERROR]" ||
			part == "PANIC" || part == "FATAL" {
			return LogSeverityError
		}
		// Then check for ERROR/PANIC/FATAL with common delimiters
		if strings.HasPrefix(part, "ERROR:") || strings.HasPrefix(part, "FATAL:") ||
			strings.HasPrefix(part, "PANIC:") {
			return LogSeverityError
		}
		if part == "WARNING" || part == "W" || part == "[W]" || strings.HasPrefix(part, "WARNING:") {
			hasWarning = true
		}
	}
	if hasWarning {
		return LogSeverityWarning
	}
	return LogSeverityNone
}

// CountLogsInObservabilityResponse returns the number of logs in a serialized
// ObservabilityLogResponse JSON string. Returns 0 if the response cannot be parsed.
func CountLogsInObservabilityResponse(toolResponse string) int {
	resultsMap := core.ObservabilityLogResponse{}
	err := common.UnmarshalJson([]byte(toolResponse), &resultsMap)
	if err != nil {
		return 0
	}
	return len(resultsMap.Logs)
}

func normalizeLogLine(log string) string {
	return strings.TrimSpace(log)
}

func GetErrorLinesFromLogStringOrDefault(log string, returnDefault bool) []string {
	lines := strings.Split(log, "\n")
	// Handle edge case of empty log string resulting in [""]
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	numLines := len(lines)

	// 1. Find indices of all potential primary error lines
	var errorIndices []int
	for i, line := range lines {
		if IsErrorLogLine(line) {
			errorIndices = append(errorIndices, i)
		}
	}

	var resultBlocks [][]string
	seenErrors := make(map[string]bool)
	totalLines := 0

	// 2. Iterate backwards through the identified error lines to prioritize the latest ones
	for i := len(errorIndices) - 1; i >= 0; i-- {
		// 5. Limit Results: Stop if we have collected enough lines
		if totalLines >= config.Config.LLMServerAgentMaxLogLines {
			break
		}

		errIdx := errorIndices[i]
		errorLine := lines[errIdx]

		normalizedErrorLine := normalizeLogLine(errorLine)

		if seenErrors[normalizedErrorLine] {
			continue
		}

		seenErrors[normalizedErrorLine] = true

		currentBlock := []string{errorLine}

		// Take next 5 lines as part of stack trace
		e := 0
		for j := errIdx + 1; j < numLines && e < 5; j++ {
			nextLine := lines[j]
			if IsErrorLogLine(nextLine) {
				break
			}
			currentBlock = append(currentBlock, nextLine)
			e++
		}

		// Prepend the current block. Since we iterate errors backwards, prepending
		// ensures the final flattened list maintains the correct relative order of errors.
		resultBlocks = append([][]string{currentBlock}, resultBlocks...)
		totalLines += len(currentBlock)
	}

	var finalLines []string
	for _, block := range resultBlocks {
		finalLines = append(finalLines, block...)
	}

	// if no errors were found and default is requested
	if len(finalLines) == 0 && returnDefault {
		finalLines = lines
	}

	numLines = len(finalLines)

	start := 0
	maxDefaultLines := config.Config.LLMServerAgentMaxLogLines
	if numLines > maxDefaultLines {
		start = numLines - maxDefaultLines
	}
	if start < 0 {
		start = 0
	}

	return finalLines[start:]
}

// minErrorLinesForWarningSkip is the minimum number of ERROR/FATAL/PANIC lines
// needed before warnings are excluded from the filtered output.
const minErrorLinesForWarningSkip = 5

func GetErrorLinesFromObservabilityLogString(toolResponse string) (string, error) {
	// check if the response is a valid json and reduce size by only picking errors
	resultsMap := core.ObservabilityLogResponse{}
	err := common.UnmarshalJson([]byte(toolResponse), &resultsMap)
	if err != nil {
		return "", err
	}

	logs := resultsMap.Logs
	numLogs := len(logs)

	// 1. Classify log lines by severity, separating errors from warnings
	var errorIndices []int
	var warningIndices []int
	errorCounts := make(map[string]int)
	for i, log := range logs {
		severity := ClassifyLogSeverity(log.Message)
		switch severity {
		case LogSeverityError:
			errorIndices = append(errorIndices, i)
			errorCounts[normalizeLogLine(log.Message)]++
		case LogSeverityWarning:
			warningIndices = append(warningIndices, i)
			errorCounts[normalizeLogLine(log.Message)]++
		}
	}

	// 2. Prioritize errors; include warnings only when there are few errors
	indicesToProcess := errorIndices
	if len(errorIndices) < minErrorLinesForWarningSkip {
		indicesToProcess = append(append([]int{}, errorIndices...), warningIndices...)
		slices.Sort(indicesToProcess)
	}

	var resultBlocks [][]core.ObservabilityLog
	seenErrors := make(map[string]bool)
	claimedIndices := make(map[int]bool)
	totalLines := 0

	// 3. Iterate backwards through the identified lines to prioritize the latest ones
	for i := len(indicesToProcess) - 1; i >= 0; i-- {
		// Limit Results heuristic
		if totalLines >= config.Config.LLMServerAgentMaxLogLines {
			break
		}

		errIdx := indicesToProcess[i]
		errorLog := logs[errIdx]
		normalizedErrorMsg := normalizeLogLine(errorLog.Message)

		if seenErrors[normalizedErrorMsg] {
			continue
		}
		seenErrors[normalizedErrorMsg] = true
		claimedIndices[errIdx] = true

		// Append repetition count if applicable
		if count := errorCounts[normalizedErrorMsg]; count > 1 {
			errorLog.Message = fmt.Sprintf("%s (Repeated %d times)", errorLog.Message, count)
		}

		// Collect Pre-Context
		var preContext []core.ObservabilityLog
		for k := 1; k <= preContextLineCount; k++ {
			idx := errIdx - k
			if idx < 0 {
				break
			}
			if claimedIndices[idx] || IsErrorLogLine(logs[idx].Message) {
				break
			}
			preContext = append(preContext, logs[idx])
			claimedIndices[idx] = true
		}
		slices.Reverse(preContext)

		// Collect Post-Context
		var postContext []core.ObservabilityLog
		for k := 1; k <= postContextLineCount; k++ {
			idx := errIdx + k
			if idx >= numLogs {
				break
			}
			if claimedIndices[idx] || IsErrorLogLine(logs[idx].Message) {
				break
			}
			postContext = append(postContext, logs[idx])
			claimedIndices[idx] = true
		}

		// Assemble Block
		currentBlock := append(preContext, errorLog)
		currentBlock = append(currentBlock, postContext...)

		// Append block (will reverse later)
		resultBlocks = append(resultBlocks, currentBlock)
		totalLines += len(currentBlock)
	}
	slices.Reverse(resultBlocks)

	var updatedLogs []core.ObservabilityLog
	for _, block := range resultBlocks {
		updatedLogs = append(updatedLogs, block...)
	}

	// If no errors were found, fall back to keeping all logs (truncated)
	if len(updatedLogs) == 0 {
		updatedLogs = logs
	}

	// Final truncation to strict limit
	if len(updatedLogs) > config.Config.LLMServerAgentMaxLogLines {
		updatedLogs = updatedLogs[len(updatedLogs)-config.Config.LLMServerAgentMaxLogLines:]
	}

	resultsMap.Logs = updatedLogs

	toolResponseBytes, err := common.MarshalJson(resultsMap)
	if err != nil {
		return "", err
	}
	toolResponse = string(toolResponseBytes)
	return toolResponse, nil
}

// ParseDuration parses a duration string, including support for days (d), weeks (w),
// months (mo), and years (y), which are not supported by time.ParseDuration.
// It also supports aliases like "days", "weeks", "months", "years".
// Note: 'm' is treated as minutes (standard Go behavior). Use 'mo' or 'month' for months.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	// check for custom units first if they are not standard
	// standard units: "ns", "us" (or "µs"), "ms", "s", "m", "h".
	// custom units we want to support: "d", "w", "mo", "y".

	// Find where the number ends and unit begins
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && c != '.' && c != '-' && c != '+' {
			break
		}
	}

	if i == 0 {
		return 0, fmt.Errorf("invalid duration format: %s", s)
	}

	valStr := s[:i]
	unit := strings.TrimSpace(strings.ToLower(s[i:]))

	// Use ParseFloat to support fractional durations like "1.5d"
	valFloat, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value: %s", s)
	}

	// Helper to convert float hours to Duration
	hoursToDuration := func(hours float64) time.Duration {
		return time.Duration(hours * float64(time.Hour))
	}

	switch unit {
	case "d", "day", "days":
		return hoursToDuration(valFloat * 24), nil
	case "w", "week", "weeks":
		return hoursToDuration(valFloat * 7 * 24), nil
	case "mo", "month", "months": // Month - approximating as 30 days
		return hoursToDuration(valFloat * 30 * 24), nil
	case "y", "year", "years": // Year - approximating as 365 days
		return hoursToDuration(valFloat * 365 * 24), nil
	}

	// Fallback to standard library for everything else (including 'm' for minutes)
	// Note: time.ParseDuration does not support whitespace in unit or full names like "minutes",
	// so this fallback works best for standard short codes.
	return time.ParseDuration(s)
}

// parseUnixTimestamp heuristically determines the unit of a numeric timestamp
// (seconds, milliseconds, or nanoseconds) and converts it to a time.Time.
func parseUnixTimestamp(ts int64) time.Time {
	// A simple heuristic to guess the unit of the timestamp.
	// Current time in seconds is ~1.7e9.
	// Current time in milliseconds is ~1.7e12.
	// Current time in nanoseconds is ~1.7e18.
	// We can use orders of magnitude to guess.
	if ts > 1e15 { // If it's a large number, it's likely in nanoseconds.
		return time.Unix(0, ts)
	}
	if ts > 1e12 { // If it's moderately large, it's likely in milliseconds.
		return time.UnixMilli(ts)
	}
	// Otherwise, assume it's in seconds.
	return time.Unix(ts, 0)
}

// parseTimeValue attempts to parse a time from various formats (string, numeric).
func parseTimeValue(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		// Try RFC3339Nano first, then RFC3339, as they are common standards.
		t, err := time.Parse(time.RFC3339Nano, v)
		if err == nil {
			return t, nil
		}
		t, err = time.Parse(time.RFC3339, v)
		if err == nil {
			return t, nil
		}

		// Try layouts without timezone, assuming UTC
		layoutsNoTZ := []string{
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		}
		for _, layout := range layoutsNoTZ {
			t, err := time.Parse(layout, v)
			if err == nil {
				return t, nil
			}
		}

		// Try to parse as a Unix timestamp string.
		unixTime, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			// If it's not a standard format or a number, we can't parse it.
			return time.Time{}, fmt.Errorf("unsupported string time format: '%s'", v)
		}
		// If it parses as a number, use the numeric timestamp parser.
		return parseUnixTimestamp(unixTime), nil

	case float64:
		return parseUnixTimestamp(int64(v)), nil
	case int:
		return parseUnixTimestamp(int64(v)), nil
	case int64:
		return parseUnixTimestamp(v), nil
	case json.Number:
		unixTime, err := v.Int64()
		if err != nil {
			// It might be a float, try that.
			unixFloat, errFloat := v.Float64()
			if errFloat != nil {
				return time.Time{}, fmt.Errorf("could not parse json.Number '%s' as a number: %v", v, err)
			}
			return parseUnixTimestamp(int64(unixFloat)), nil
		}
		return parseUnixTimestamp(unixTime), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported type for time value: %T", v)
	}
}

// hasWindowArgs reports whether a tool call carries any explicit time-window control.
func hasWindowArgs(args map[string]any) bool {
	for _, k := range []string{"range", "duration", "start_time", "end_time", "start", "end", "starttime", "endtime"} {
		if _, ok := args[k]; ok {
			return true
		}
	}
	return false
}

// windowFromTimestampBounds derives a search window from timestamp bounds the agent wrote
// into the filter itself (`{"timestamp": {"_gte": "…"}}`) when it gave no window argument.
//
// Without this the call fell back to the default last hour: the filter asked for 18:18–18:48
// yesterday, the backend was searched over the last hour, and the answer was zero rows for a
// window that had plenty. Only bounds that must hold for every row count — top-level and
// under _and; a bound inside _or or _not does not constrain the window. A lone lower bound
// runs to now; a lone upper bound covers the hour before it.
func windowFromTimestampBounds(where core.QueryWhereClause, now time.Time) (time.Time, time.Time, bool) {
	var lo, hi time.Time
	var walk func(w core.QueryWhereClause)
	walk = func(w core.QueryWhereClause) {
		for field, ops := range w.Binary {
			switch strings.ToLower(field) {
			case "timestamp", "_timestamp", "@timestamp", "time":
			default:
				continue
			}
			for op, v := range ops {
				t, err := parseTimeValue(v)
				if err != nil {
					continue
				}
				switch op {
				case core.Gt, core.Gte:
					if lo.IsZero() || t.After(lo) {
						lo = t
					}
				case core.Lt, core.Lte:
					if hi.IsZero() || t.Before(hi) {
						hi = t
					}
				}
			}
		}
		for _, c := range w.And {
			walk(c)
		}
	}
	walk(where)

	switch {
	case !lo.IsZero() && !hi.IsZero():
		if hi.Before(lo) {
			return time.Time{}, time.Time{}, false
		}
		return lo.UTC(), hi.UTC(), true
	case !lo.IsZero():
		if lo.After(now) {
			return time.Time{}, time.Time{}, false
		}
		return lo.UTC(), now.UTC(), true
	case !hi.IsZero():
		return hi.Add(-time.Hour).UTC(), hi.UTC(), true
	}
	return time.Time{}, time.Time{}, false
}

// LogQueryWindow is the time window a canonical log query asks for.
type LogQueryWindow struct {
	Start time.Time
	End   time.Time
	// EndsNow means the query named no end: End is the `now` it was resolved
	// with, and the query's real end is whenever it runs.
	EndsNow bool
	// Range is set, with EndsNow, when the window is a bare relative range
	// ("last 30m"): Start is Range before End.
	Range time.Duration
}

// ResolveLogQueryWindow resolves the window a canonical log query states, in
// every form a query can carry it:
//
//   - a start_time/end_time pair, or a start_time alone (it runs to now);
//   - a relative range, ending now or at an explicit end_time, or running
//     forward from a start_time;
//   - an ISO-8601 interval in the range slot ("A/B");
//   - timestamp bounds in the filter, when no window control is given.
//
// args are window controls passed beside the query (a tool call's arguments);
// they win over the query's own. ok is false when nothing states a window, and
// the caller applies its own fallback.
//
// logs_execute_v2 runs a query over this window and the log query plan's
// validation bounds a plan by it. Both must read a plan's window the same way:
// a form only one of them understood is how a fetch came to run over a window
// nobody asked for.
func ResolveLogQueryWindow(query core.QueryBuilder, args map[string]any, now time.Time) (LogQueryWindow, bool) {
	controls := make(map[string]any, len(args)+3)
	for k, v := range args {
		controls[k] = v
	}
	if _, ok := controls["range"]; !ok && query.TimeRange != "" {
		controls["range"] = query.TimeRange
	}
	if _, ok := controls["start_time"]; !ok && query.StartTime != "" {
		controls["start_time"] = query.StartTime
	}
	if _, ok := controls["end_time"]; !ok && query.EndTime != "" {
		controls["end_time"] = query.EndTime
	}

	if !hasWindowArgs(controls) {
		// A window written into the filter (timestamp bounds) is still the window asked for;
		// without this the search ran over the default last hour and missed it entirely.
		if from, to, ok := windowFromTimestampBounds(query.Where, now); ok {
			return LogQueryWindow{Start: from, End: to}, true
		}
		return LogQueryWindow{}, false
	}

	_, hasStart := firstTimeValue(controls, "start_time", "start", "starttime")
	_, hasEnd := firstTimeValue(controls, "end_time", "end", "endtime")
	if start, end, ok := resolveWindowFromLabelsAt(controls, now); ok {
		window := LogQueryWindow{Start: start.UTC(), End: end.UTC()}
		if !hasStart && !hasEnd {
			if d, ok := relativeRange(controls); ok {
				window.EndsNow, window.Range = true, d
			}
		}
		return window, true
	}
	// A start with no end and no range runs to now.
	if start, ok := firstTimeValue(controls, "start_time", "start", "starttime"); ok && !hasEnd && start.Before(now) {
		return LogQueryWindow{Start: start.UTC(), End: now.UTC(), EndsNow: true}, true
	}
	return LogQueryWindow{}, false
}

// relativeRange returns the duration in the range slot. An ISO interval there
// is not a duration and reports false.
func relativeRange(labels map[string]any) (time.Duration, bool) {
	for _, k := range []string{"range", "duration"} {
		valStr, ok := labels[k].(string)
		if !ok {
			continue
		}
		if d, err := ParseDuration(valStr); err == nil {
			if d < 0 {
				d = -d
			}
			return d, true
		}
	}
	return 0, false
}

// ExtractStartEndtimeFromLabels extracts start and end times from a map of labels.
// It checks for common key pairs like (start_time, end_time) and (start, end).
// It also checks for a 'range' or 'duration' key to calculate start time relative to end time (defaults to Now).
// It supports various time formats including RFC3339 and Unix timestamps (s, ms, ns).
func ExtractStartEndtimeFromLabels(nbRequestContext core.NbToolContext, labels map[string]any) (time.Time, time.Time, error) {
	// Caller/agent-provided time controls ALWAYS take priority over the ambient
	// event labels carried in QueryConfig.Labels. An event-triggered conversation
	// seeds QueryConfig.Labels with the alert's start/end epoch (often a 29s–2m
	// window); when the LLM explicitly asks for e.g. {"range":"1h"} that request
	// must win, not the narrow event window. We therefore resolve the caller
	// labels FIRST — including the range branch — and only fall back to event
	// labels when the caller supplied no usable time control.
	//
	// Resolving each set in isolation (rather than merging them) is what fixes the
	// long-standing bug where an explicit range lost to the event's start/end
	// pair: within a single set the explicit-pair check runs before the range
	// check, so a merged event pair would always shadow a caller range.
	// Returned in UTC so a caller that formats the window never mixes offsets.
	if start, end, ok := resolveWindowFromLabels(labels); ok {
		return start.UTC(), end.UTC(), nil
	}
	if start, end, ok := resolveWindowFromLabels(nbRequestContext.QueryConfig.Labels); ok {
		return start.UTC(), end.UTC(), nil
	}
	return time.Time{}, time.Time{}, errors.New("no valid start/end time key pair or range found in labels")
}

// resolveWindowFromLabels derives a (start, end) window from a single label set.
// Priority within the set: an explicit start/end pair wins; otherwise a
// range/duration is applied, anchored to an explicit end key if present, else to
// now. Returns ok=false when the set holds no usable time information so the
// caller can fall back to another source (e.g. event labels).
func resolveWindowFromLabels(labels map[string]any) (time.Time, time.Time, bool) {
	return resolveWindowFromLabelsAt(labels, time.Now().UTC())
}

// resolveWindowFromLabelsAt is resolveWindowFromLabels with the instant "now"
// stands for supplied, so a caller resolving two windows compares them against
// the same instant.
func resolveWindowFromLabelsAt(labels map[string]any, now time.Time) (time.Time, time.Time, bool) {
	if labels == nil {
		return time.Time{}, time.Time{}, false
	}

	// 1. Explicit start/end pairs.
	keyPairs := [][2]string{
		{"start_time", "end_time"},
		{"start", "end"},
		{"starttime", "endtime"},
	}
	for _, pair := range keyPairs {
		startVal, startOk := labels[pair[0]]
		endVal, endOk := labels[pair[1]]
		if !startOk || !endOk {
			continue
		}

		startTime, err := parseTimeValue(startVal)
		if err != nil {
			continue
		}
		endTime, err := parseTimeValue(endVal)
		if err != nil {
			continue
		}

		// Swap if start > end (malformed event labels).
		if startTime.After(endTime) {
			startTime, endTime = endTime, startTime
		}
		return startTime, endTime, true
	}

	// 1b. An ISO-8601 interval in the range slot ("2026-09-25T18:18:00Z/2026-09-25T18:48:00Z").
	// The logs agent writes absolute windows this way; it is not a duration, so it used to
	// fail ParseDuration below and the caller silently fell back to the last hour — the
	// agent then analysed logs from the wrong window and reported zero in the one it asked for.
	for _, k := range []string{"range", "duration"} {
		valStr, ok := labels[k].(string)
		if !ok {
			continue
		}
		startStr, endStr, found := strings.Cut(strings.TrimSpace(valStr), "/")
		if !found {
			continue
		}
		startTime, errStart := parseTimeValue(strings.TrimSpace(startStr))
		endTime, errEnd := parseTimeValue(strings.TrimSpace(endStr))
		if errStart != nil || errEnd != nil {
			continue
		}
		if startTime.After(endTime) {
			startTime, endTime = endTime, startTime
		}
		return startTime, endTime, true
	}

	// 2. Range/duration, anchored to an explicit end key if present; else it runs
	// forward from an explicit start key; else back from now. A start plus a range
	// ("75m from 10:30") used to be anchored to now, which silently replaced an
	// incident window from hours ago with the last 75 minutes — the query came back
	// empty and the investigation read that as "logs expired".
	var duration time.Duration
	hasRange := false
	for _, k := range []string{"range", "duration"} {
		if val, ok := labels[k]; ok {
			if valStr, ok := val.(string); ok {
				if d, err := ParseDuration(valStr); err == nil {
					// "-1h" means the last hour, not an hour into the future.
					if d < 0 {
						d = -d
					}
					duration, hasRange = d, true
					break
				}
			}
		}
	}
	if !hasRange {
		return time.Time{}, time.Time{}, false
	}
	if endTime, ok := firstTimeValue(labels, "end_time", "end", "endtime"); ok {
		return endTime.Add(-duration), endTime, true
	}
	if startTime, ok := firstTimeValue(labels, "start_time", "start", "starttime"); ok && startTime.Before(now) {
		endTime := startTime.Add(duration)
		if endTime.After(now) {
			endTime = now
		}
		return startTime, endTime, true
	}
	return now.Add(-duration), now, true
}

// firstTimeValue returns the first of keys that holds a parseable time.
func firstTimeValue(labels map[string]any, keys ...string) (time.Time, bool) {
	for _, k := range keys {
		if val, ok := labels[k]; ok {
			if t, err := parseTimeValue(val); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// ExpandNarrowTimeWindow widens a time range that is too narrow for log queries.
// Event labels (PagerDuty, Alertmanager) often carry 29–120s windows that are too
// tight to capture surrounding log context. Based on analysis of 75 production
// failures, the threshold of 5 minutes and expansion of ±15 minutes were chosen
// because PagerDuty windows are typically 29s and Alertmanager windows ~74–160s.
//
// This is intentionally NOT built into ExtractStartEndtimeFromLabels because that
// function is shared by event, metrics, and trace tools where narrow windows may
// be intentional. Only log-specific callers should use this.
func ExpandNarrowTimeWindow(logger interface{ Debug(string, ...any) }, start, end time.Time) (time.Time, time.Time) {
	const minWindow = 5 * time.Minute
	const expandHalf = 15 * time.Minute
	if end.Sub(start) < minWindow {
		midpoint := start.Add(end.Sub(start) / 2)
		logger.Debug("ExpandNarrowTimeWindow: widening narrow time window for log query",
			"original_start", start.Format(time.RFC3339),
			"original_end", end.Format(time.RFC3339),
			"expanded_window", (2 * expandHalf).String(),
		)
		return midpoint.Add(-expandHalf), midpoint.Add(expandHalf)
	}
	return start, end
}

// isGitCliCommandWithJq reports whether `command` is a `gh` or `glab` invocation
// that filters output with jq. Both CLIs expose their own `--jq` flag whose
// expression legitimately contains `|` (e.g. `--jq '.[] | .title'`), so
// ExecuteCliCommand must not split such a command on `|` — that would shred the
// expression across bogus pipeline stages.
func isGitCliCommandWithJq(command string) bool {

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	switch filepath.Base(parts[0]) {
	case "gh", "glab":
	default:
		return false
	}

	segments := strings.Split(command, "|")
	jqPipe := false

	for i := 1; i < len(segments); i++ {
		seg := strings.TrimSpace(segments[i])
		if seg == "" {
			continue
		}

		partSeg := strings.Fields(seg)
		if len(partSeg) == 0 {
			continue
		}

		exeSeg := filepath.Base(partSeg[0])
		if exeSeg == "jq" {
			jqPipe = true
			break
		}
	}

	jqFlag := false
	for _, token := range parts {
		if token == "--jq" || strings.HasPrefix(token, "--jq=") {
			jqFlag = true
			break
		}
	}

	return jqPipe || jqFlag
}
