package tools

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"os"
	"os/exec"
	"path/filepath"
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

	// GH/JQ special case
	if isGithubCommandWithJq(command) {
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

	var finalStdout, combinedStderr bytes.Buffer
	cmds[len(cmds)-1].Stdout = &finalStdout
	for i := range cmds {
		cmds[i].Stderr = &combinedStderr
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

	if finalErr != nil {
		toolContext.Ctx.GetLogger().Error("tools: cli command execution failed", "error", finalErr, "stderr", combinedStderr.String(), "command", command)
		return finalStdout.String(), fmt.Errorf("tools: cli command failed: %v\nStderr: %s", finalErr, combinedStderr.String())
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

// ExtractStartEndtimeFromLabels extracts start and end times from a map of labels.
// It checks for common key pairs like (start_time, end_time) and (start, end).
// It also checks for a 'range' or 'duration' key to calculate start time relative to end time (defaults to Now).
// It supports various time formats including RFC3339 and Unix timestamps (s, ms, ns).
func ExtractStartEndtimeFromLabels(nbRequestContext core.NbToolContext, labels map[string]any) (time.Time, time.Time, error) {
	if labels == nil {
		labels = map[string]any{}
	}

	// Merge event labels as base, then overlay agent/LLM-provided labels on top.
	// This ensures LLM-specified times (start_time/end_time) take priority over
	// event label times (start/end epoch millis) which are often narrow windows.
	labelsFinal := map[string]any{}
	if nbRequestContext.QueryConfig.Labels != nil {
		maps.Copy(labelsFinal, nbRequestContext.QueryConfig.Labels)
	}
	maps.Copy(labelsFinal, labels)

	// 1. Check for explicit start/end pairs FIRST
	keyPairs := [][2]string{
		{"start_time", "end_time"},
		{"start", "end"},
		{"starttime", "endtime"},
	}

	for _, pair := range keyPairs {
		startKey, endKey := pair[0], pair[1]

		startVal, startOk := labelsFinal[startKey]
		endVal, endOk := labelsFinal[endKey]

		if startOk && endOk {
			startTime, err := parseTimeValue(startVal)
			if err != nil {
				continue
			}

			parsedEndTime, err := parseTimeValue(endVal)
			if err != nil {
				continue
			}

			// Swap if start > end (malformed event labels)
			if startTime.After(parsedEndTime) {
				startTime, parsedEndTime = parsedEndTime, startTime
			}

			return startTime, parsedEndTime, nil
		}
	}

	// Default End Time is Now
	endTime := time.Now()

	// Check if end time is explicitly provided
	endKeys := []string{"end_time", "end", "endtime"}
	for _, k := range endKeys {
		if val, ok := labelsFinal[k]; ok {
			parsedEnd, err := parseTimeValue(val)
			if err == nil {
				endTime = parsedEnd
				break
			}
		}
	}

	// Check for Duration/Range
	rangeKeys := []string{"range", "duration"}
	for _, k := range rangeKeys {
		if val, ok := labelsFinal[k]; ok {
			if valStr, ok := val.(string); ok {
				duration, err2 := ParseDuration(valStr)
				if err2 == nil {
					return endTime.Add(-duration), endTime, nil
				}
			}
		}
	}

	return time.Time{}, time.Time{}, errors.New("no valid start/end time key pair or range found in labels")
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

func isGithubCommandWithJq(command string) bool {

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	exe := filepath.Base(parts[0])
	isGh := exe == "gh"

	if !isGh {
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
