package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nudgebee/logparser"
	"github.com/samber/lo"
)

func init() {
	playbooks.RegisterAction("signoz_logs_enricher", &signozLogsAction{})
	playbooks.RegisterAction("logs", &observabilityLogAction{})
	playbooks.RegisterAction("datadog_logs", &datadogLogsAction{})
}

// Get top 2 error logs based on severity
const actionLogMaxInsightErrors = 10

// actionLogMaxGroupInsights is how many grouped highlights a log card gets when the
// source provided none of its own.
const actionLogMaxGroupInsights = 3

// firstStringField returns the first key in obj that holds a non-empty value, coerced to a
// string. Structured logs sometimes encode time/level/severity numerically (epoch timestamp
// as float64, level as int); those are converted rather than silently dropped.
func firstStringField(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := obj[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case json.Number:
			return v.String()
		}
	}
	return ""
}

// splitLeadingRFC3339 splits a "<rfc3339> <rest>" line into its timestamp and remainder.
// Returns ("", line) unchanged when the first token is not a timestamp, so lines from
// sources that do not stamp them are left alone. A line that is nothing but a timestamp
// (what `--timestamps` makes of a blank log line) yields an empty remainder, which the
// caller drops.
func splitLeadingRFC3339(line string) (string, string) {
	head, rest, _ := strings.Cut(line, " ")
	ts, err := time.Parse(time.RFC3339Nano, head)
	if err != nil {
		return "", line
	}
	return ts.UTC().Format(time.RFC3339Nano), strings.TrimSpace(rest)
}

// parseLogTextToOutputLogs converts raw newline-separated log text (e.g. kubectl / agent
// logs_enricher stdout) into structured OutputLog entries.
//
// The relay fallback paths used to return this text as a type:"file" evidence, but a plain
// text "file" renders to nothing in the UI log card (which expects the structured
// {data:[{timestamp,message,severity,labels}]} shape) and is mis-handled by the log-analysis
// pipeline (which treats type:"file" as base64+gzip). Returning structured OutputLog entries
// instead makes the same captured logs render in the card AND feed the analyzer.
//
// JSON log lines (the common structured-logging case) have their timestamp/level/message
// fields lifted out via the usual key aliases; non-JSON lines become a plain message with a
// best-effort level guess. The full parsed object is retained under Labels for drill-down.
func parseLogTextToOutputLogs(text string) []OutputLog {
	lines := strings.Split(text, "\n")
	entries := make([]OutputLog, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// `kubectl logs --timestamps` prefixes every line with an RFC3339Nano stamp; lift
		// it off before parsing so it neither pollutes the message body (and with it the
		// Drain3 pattern templates) nor leaves the entry timestamp-less.
		streamTimestamp, line := splitLeadingRFC3339(line)
		if line == "" {
			continue
		}

		entry := OutputLog{Message: line, Timestamp: streamTimestamp}
		var obj map[string]any
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &obj) == nil {
			entry.Labels = obj
			if ts := firstStringField(obj, "time", "timestamp", "ts", "@timestamp"); ts != "" {
				entry.Timestamp = ts
			}
			if msg := firstStringField(obj, "msg", "message", "body", "log"); msg != "" {
				entry.Message = msg
			}
			entry.Severity = strings.ToUpper(firstStringField(obj, "level", "severity", "severity_text", "lvl"))
		}
		if entry.Severity == "" {
			if lvl := logparser.GuessLevel(entry.Message); lvl != logparser.LevelUnknown {
				entry.Severity = strings.ToUpper(lvl.String())
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

type RegexLabelExtractor struct {
	Pattern   string `json:"pattern"`
	LabelName string `json:"label_name"`
}

type LabelExtractor struct {
	LabelName       string `json:"label_name"`
	PlaceHolderName string `json:"placeholder_name,omitempty"` // optional, if not provided, use label_name
}

type PlaybookQueryGenerator interface {
	CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool
	GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error)
}

// actionLogCompileRegexPatterns compiles regex patterns and returns valid ones
func actionLogCompileRegexPatterns(extractors []RegexLabelExtractor) map[string]*regexp.Regexp {
	compiledPatterns := make(map[string]*regexp.Regexp)
	for _, extractor := range extractors {
		if compiled, err := regexp.Compile(extractor.Pattern); err == nil {
			compiledPatterns[extractor.LabelName] = compiled
		} else {
			slog.Error("logsEnricherAction: invalid regex pattern",
				"pattern", extractor.Pattern,
				"label_name", extractor.LabelName,
				"error", err)
		}
	}
	return compiledPatterns
}

// actionLogApplyRegexExtractors applies regex patterns to extract labels from log body.
// Collects all unique values for each label.
func actionLogApplyRegexExtractors(body string, compiledPatterns map[string]*regexp.Regexp, extractors []RegexLabelExtractor, extractedLabels map[string]any) {
	if len(compiledPatterns) == 0 {
		return
	}

	for _, extractor := range extractors {
		pattern, exists := compiledPatterns[extractor.LabelName]
		if !exists {
			continue
		}

		matches := pattern.FindStringSubmatch(body)
		var val string
		if len(matches) > 1 {
			val = matches[1]
		} else if len(matches) == 1 {
			val = matches[0]
		} else {
			continue
		}

		existing, hasLabel := extractedLabels[extractor.LabelName]
		if !hasLabel {
			extractedLabels[extractor.LabelName] = []any{val}
			continue
		}

		// Regex matches are always strings — use direct comparison instead of reflect.DeepEqual.
		valArray := existing.([]any)
		alreadyExists := false
		for _, existingVal := range valArray {
			if s, ok := existingVal.(string); ok && s == val {
				alreadyExists = true
				break
			}
		}
		if !alreadyExists {
			extractedLabels[extractor.LabelName] = append(valArray, val)
		}
	}
}

// actionLogApplyLabelExtractors extracts labels from log data fields
// Now collects ALL unique values for each label, not just the first one
func actionLogApplyLabelExtractors(logData map[string]any, labelExtractor []LabelExtractor, extractedLabels map[string]any) {
	for _, le := range labelExtractor {
		placeholder := le.PlaceHolderName
		if placeholder == "" {
			placeholder = le.LabelName
		}

		var val any
		var found bool

		// First try direct access
		if v, ok := logData[placeholder]; ok && v != "" {
			val = v
			found = true
		} else if resourcesString, ok := logData["resources_string"].(map[string]any); ok {
			// Then try nested in resources_string
			if v, ok := resourcesString[le.LabelName]; ok && v != "" {
				val = v
				found = true
			}
		}

		if found {
			// Initialize array if this is the first value for this label
			if _, exists := extractedLabels[le.LabelName]; !exists {
				extractedLabels[le.LabelName] = []any{}
			}

			// Add value to array if it's not already present (collect unique values)
			// Use DeepEqual to handle complex types like maps and slices
			valArray := extractedLabels[le.LabelName].([]any)
			alreadyExists := false
			for _, existingVal := range valArray {
				if reflect.DeepEqual(existingVal, val) {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				extractedLabels[le.LabelName] = append(valArray, val)
			}
		}
	}
}

// actionLogExtractionResults logs the results for debugging
func actionLogExtractionResults(extractedLabels map[string]any, compiledPatternsCount int) {
	if len(extractedLabels) > 0 {
		// Count total values across all labels
		valueCounts := make(map[string]int)
		for labelName, values := range extractedLabels {
			if valArray, ok := values.([]any); ok {
				valueCounts[labelName] = len(valArray)
			} else {
				// Fallback for non-array values (shouldn't happen with new implementation)
				valueCounts[labelName] = 1
			}
		}
		slog.Debug("signozLogsEnricherAction: labels extracted successfully",
			"extracted_label_count", len(extractedLabels),
			"value_counts", valueCounts)
	} else {
		slog.Debug("signozLogsEnricherAction: no labels extracted",
			"compiled_patterns", compiledPatternsCount)
	}
}

func actionLogExtractErrorPatterns(logs []map[string]any, maxErrors int) []playbooks.PlaybookActionResponseInsight {
	// Filter logs that contain error indicators
	errorPatterns := []string{
		"exception",
		"error",
		"fatal",
	}

	// Create streaming pattern extractor (memory-efficient)
	extractor, err := logparser.NewPatternExtractor()
	if err != nil {
		return []playbooks.PlaybookActionResponseInsight{}
	}

	// Map to store severity for example logs
	errorMetadata := make(map[string]string) // Map from body to severity

	// Stream logs one at a time (memory-efficient)
	for _, logData := range logs {
		body, _ := logData["body"].(string)
		bodyLower := strings.ToLower(body)

		// Check if log contains any error pattern in the body
		hasError := false
		if body != "" {
			for _, pattern := range errorPatterns {
				if strings.Contains(bodyLower, pattern) {
					hasError = true
					break
				}
			}
		}

		// Check for API failures (statusCode >= 500) even if body is empty
		isAPIFailure := false
		var apiFailureMessage string
		if labels, ok := logData["labels"].(map[string]any); ok {
			// Check for statusCode in labels
			var statusCode int
			if sc, ok := labels["statusCode"].(float64); ok {
				statusCode = int(sc)
			} else if sc, ok := labels["statusCode"].(int); ok {
				statusCode = sc
			}

			// Check for statusCode in FIELDS
			if statusCode == 0 {
				if fields, ok := labels["FIELDS"].(map[string]any); ok {
					if sc, ok := fields["statusCode"].(float64); ok {
						statusCode = int(sc)
					} else if sc, ok := fields["statusCode"].(int); ok {
						statusCode = sc
					}
				}
			}

			// Treat 5xx status codes as errors
			if statusCode >= 500 {
				isAPIFailure = true
				// Build meaningful error message from API log metadata
				endpoint := ""
				httpMethod := ""
				requestURI := ""

				if fields, ok := labels["FIELDS"].(map[string]any); ok {
					endpoint, _ = fields["endpoint"].(string)
					httpMethod, _ = fields["httpMethod"].(string)
					requestURI, _ = fields["requestURI"].(string)
				}
				if endpoint == "" {
					endpoint, _ = labels["endpoint"].(string)
				}
				if httpMethod == "" {
					httpMethod, _ = labels["httpMethod"].(string)
				}
				if requestURI == "" {
					requestURI, _ = labels["requestURI"].(string)
				}

				apiFailureMessage = fmt.Sprintf("API Request Failed | %s %s | Status: %d | Endpoint: %s", httpMethod, requestURI, statusCode, endpoint)
			}
		}

		if hasError || isAPIFailure {
			// Build full error message with stack trace for better pattern matching
			fullErrorMessage := body
			if isAPIFailure && body == "" {
				fullErrorMessage = apiFailureMessage
			}

			// Try to get stack trace from labels.exception_stack_trace or labels.FIELDS.thrown.extendedStackTrace
			if labels, ok := logData["labels"].(map[string]any); ok {
				// First try exception_stack_trace (top-level extracted field)
				if stackTrace, ok := labels["exception_stack_trace"].(string); ok && stackTrace != "" {
					fullErrorMessage = fullErrorMessage + "\n" + stackTrace
				} else if fields, ok := labels["FIELDS"].(map[string]any); ok {
					// Try FIELDS.thrown.extendedStackTrace
					if thrown, ok := fields["thrown"].(map[string]any); ok {
						if extStackTrace, ok := thrown["extendedStackTrace"].(string); ok && extStackTrace != "" {
							fullErrorMessage = fullErrorMessage + "\n" + extStackTrace
						}
					}
				}
			}

			// Process log without buffering
			if fullErrorMessage != "" {
				if err := extractor.AddLog(fullErrorMessage); err == nil {
					// Store severity for this log
					severityText, _ := logData["severity_text"].(string)
					if severityText == "" {
						if isAPIFailure {
							severityText = "ERROR"
						} else {
							severityText = "ERROR"
						}
					}
					errorMetadata[fullErrorMessage] = severityText
				}
			}
		}
	}

	// Extract patterns from processed logs
	patterns := extractor.GetPatterns(maxErrors)

	// Convert patterns to insights
	insights := make([]playbooks.PlaybookActionResponseInsight, 0, len(patterns))
	for _, pattern := range patterns {
		// Get severity from the example log
		severity := errorMetadata[pattern.Example]
		if severity == "" {
			severity = "ERROR"
		}

		// Include full example error with complete stack trace
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%s: %s (occurred %d times, %.1f%%)", severity, pattern.Example, pattern.Count, pattern.Percentage),
			Severity: "High",
		})
	}

	return insights
}

// --- Shared canonical log helpers ---------------------------------------
// The three log actions (logs / signoz_logs_enricher / datadog_logs) all fetch
// through observability.FetchLogs and get back []OutputLog. These helpers build
// one identical response shape (insights + log_summary + extracted_labels) so
// every provider renders through the same frontend cards.

// outputLogsToInsightMaps converts canonical logs to the {"body","severity_text",
// "labels"} map shape that playbooks.ComputeLogSummary / actionLogExtractErrorPatterns consume.
func outputLogsToInsightMaps(logs []OutputLog) []map[string]any {
	return lo.Map(logs, func(item OutputLog, _ int) map[string]any {
		return map[string]any{
			"body":          item.Message,
			"severity_text": item.Severity,
			"labels":        item.Labels,
		}
	})
}

// summarizeOutputLogs groups provider log rows the way pod log files are grouped, so
// every log card can show what the lines say instead of every line.
func summarizeOutputLogs(logs []OutputLog, maxInsights int) playbooks.LogLinesDigest {
	entries := make([]playbooks.LogEntry, len(logs))
	for i, l := range logs {
		entries[i] = playbooks.LogEntry{Line: l.Message, Level: l.Severity, Timestamp: l.Timestamp}
	}
	return playbooks.SummarizeLogEntries(entries, maxInsights)
}

// putLogGroups stores the grouped view the log cards render, under the keys the
// pod-log action uses.
func putLogGroups(additionalInfo map[string]any, digest playbooks.LogLinesDigest) {
	if len(digest.Groups) == 0 {
		return
	}
	additionalInfo["log_groups"] = digest.Groups
	additionalInfo["log_total_lines"] = digest.TotalLines
}

// extractLogLabelsFromOutputLogs applies regex + label extractors across a slice
// of canonical logs and returns the collected labels (values de-duplicated into
// arrays). Shared label extractor for all log actions.
func extractLogLabelsFromOutputLogs(logs []OutputLog, extractors []RegexLabelExtractor, labelExtractor []LabelExtractor) map[string]any {
	extractedLabels := make(map[string]any)

	if len(extractors) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	compiledPatterns := actionLogCompileRegexPatterns(extractors)
	if len(compiledPatterns) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	for _, logData := range logs {
		if logData.Message != "" {
			actionLogApplyRegexExtractors(logData.Message, compiledPatterns, extractors, extractedLabels)
		}
		actionLogApplyLabelExtractors(logData.Labels, labelExtractor, extractedLabels)
	}

	actionLogExtractionResults(extractedLabels, len(compiledPatterns))
	return extractedLabels
}

// extractLogInsights surfaces the most relevant error insights from canonical
// logs: first any explicit ERROR/FATAL-severity lines (per-line "Critical"
// insights, preserving SigNoz's original behavior), then falls back to the
// keyword/5xx pattern extractor when no severity-tagged errors are present.
func extractLogInsights(logs []OutputLog, maxErrors int) []playbooks.PlaybookActionResponseInsight {
	var insights []playbooks.PlaybookActionResponseInsight
	seen := map[string]bool{}
	for _, l := range logs {
		if len(insights) >= maxErrors {
			break
		}
		sev := strings.ToUpper(l.Severity)
		// The same error logged three times read as three identical highlights.
		if (sev == "ERROR" || sev == "FATAL") && l.Message != "" && !seen[l.Message] {
			seen[l.Message] = true
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  prefixSeverity(sev, l.Message),
				Severity: "Critical",
			})
		}
	}
	if len(insights) == 0 {
		insights = actionLogExtractErrorPatterns(outputLogsToInsightMaps(logs), maxErrors)
	}
	return insights
}

// prefixSeverity labels a message with its severity unless the message already opens
// with it — "ERROR: report generation failed" otherwise became "ERROR: ERROR: …".
func prefixSeverity(sev, message string) string {
	upper := strings.ToUpper(strings.TrimSpace(message))
	// Only a whole word counts: "Errors occurred" does not already carry "ERROR".
	if rest, ok := strings.CutPrefix(upper, sev); ok {
		next, _ := utf8.DecodeRuneInString(rest)
		if rest == "" || !unicode.IsLetter(next) && !unicode.IsDigit(next) {
			return message
		}
	}
	return fmt.Sprintf("%s: %s", sev, message)
}

// buildLogsActionResponse assembles the standard LogsActionResponse from canonical
// logs: {"data": logs} payload, error insights, log_summary, metadata, and
// extracted labels. additionalInfo may carry action_name / reference_url that a
// caller wants preserved; title, when non-empty, is merged in.
func buildLogsActionResponse(logs []OutputLog, title string, additionalInfo map[string]any, maxInsights int, metaQuery any, extractors []RegexLabelExtractor, labelExtractors []LabelExtractor) *playbooks.LogsActionResponse {
	// No logs means no card. An evidence of {"data":[]} renders an empty log
	// viewer, which reads as "we looked and the service was quiet" when the truth
	// is usually that nothing was collected — a resource that ships no logs, a
	// scope that matched nothing, or missing permissions. The cloud_logs action
	// already returns nil in this case; this path is the one that did not, so the
	// same query produced an empty card or no card depending on which provider
	// answered. Callers treat nil as "action produced nothing", which is the
	// honest signal.
	if len(logs) == 0 {
		return nil
	}
	if additionalInfo == nil {
		additionalInfo = map[string]any{}
	}
	if title != "" {
		additionalInfo["title"] = title
	}
	if summary := playbooks.ComputeLogSummary(outputLogsToInsightMaps(logs)); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	digest := summarizeOutputLogs(logs, 0)
	putLogGroups(additionalInfo, digest)
	// What window these lines actually cover, which is not the window that was
	// asked for: the provider returns its most recent N inside the range, so a
	// 60-minute request routinely comes back as the last ~8 minutes, and a
	// long-lived event re-enriched later gets lines from hours after it started.
	// The pod-log path stamps the same keys; without them here, the majority of
	// log evidence still cannot say what it is evidence of.
	if digest.FirstSeen != "" {
		additionalInfo["log_window_start"] = digest.FirstSeen
		additionalInfo["log_window_end"] = digest.LastSeen
	}
	insight := extractLogInsights(logs, maxInsights)
	// A card that does not hold its whole window says so in its summary line, which is
	// built from the insights. Without it, a card with no error line reads as "no errors".
	if note, _ := additionalInfo["log_truncation_note"].(string); note != "" {
		insight = append(insight, playbooks.PlaybookActionResponseInsight{Message: note, Severity: "Info"})
	}
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                metaQuery,
	}
	base := playbooks.NewPlaybookActionResponseJson(map[string]any{"data": logs}, additionalInfo, insight, metadata)
	return &playbooks.LogsActionResponse{
		Data:            base.Data,
		AdditionalInfo:  base.AdditionalInfo,
		Insight:         base.Insight,
		Metadata:        base.Metadata,
		ExtractedLabels: extractLogLabelsFromOutputLogs(logs, extractors, labelExtractors),
	}
}

// logsResponseOrNil returns a *true* nil PlaybookActionResponse when there is no
// log evidence to store.
//
// Returning the typed pointer straight from an action would produce a non-nil
// interface wrapping a nil *LogsActionResponse: every downstream `!= nil` check
// passes and the first field access panics. Every caller of
// buildLogsActionResponse must go through here.
func logsResponseOrNil(resp *playbooks.LogsActionResponse) (playbooks.PlaybookActionResponse, error) {
	if resp == nil {
		return nil, nil
	}
	return resp, nil
}

// addExecutedQueryInfo records the provider query FetchLogs actually ran (and the
// resolved provider) into an evidence's additional_info, so the UI can show them
// for reference. Empty values are skipped.
func addExecutedQueryInfo(info map[string]any, result FetchLogsResult) map[string]any {
	if info == nil {
		info = map[string]any{}
	}
	if result.Query != "" {
		info["executed_query"] = result.Query
	}
	if result.Provider != "" {
		info["provider"] = result.Provider
	}
	return info
}

// lineFilterSource is implemented by log sources that can narrow a query they ran to
// the lines matching a pattern. A log card uses it to keep the error and warning lines
// of a window that holds more lines than the card.
//
// An implementation must report each line's level as read off the line, or unknown —
// never a default. The narrowed lines are ranked by that level, and a source that stamps
// every line INFO (the Elasticsearch agent source does, for stdout) would have all of
// them discarded as routine.
type lineFilterSource interface {
	withLineFilter(query, pattern string) (string, bool)
}

// The lines a log card asks a lineFilterSource for: a level word at the start of a word
// anywhere in the line, or a glog header (E0917 …). That is looser than
// logparser.GuessLevel, which reads only a line's first fields, so the level the source
// reports decides afterwards. "alert", which GuessLevel also reads as critical, is left
// out: alerting components write the word on routine lines.
var (
	errorLines          = regexp.MustCompile(`(?i)(^|[^a-z])(err|crit|fatal|ftl|emerg|panic)|^[EF][0-9]{4} `)
	errorOrWarningLines = regexp.MustCompile(`(?i)(^|[^a-z])(err|crit|fatal|ftl|emerg|panic|warn|wrn)|^[EFW][0-9]{4} `)
)

// flaggedLinesBudget is how long a card's narrowed queries may take, all of them
// together. The card's own query can take up to 60s — the Loki client timeout on the
// agent and on a user-configured Loki — so a full card takes at most half as long again,
// and a narrowed query that runs out of time leaves the card as it was.
var flaggedLinesBudget = 30 * time.Second

// fetchCardLogs is FetchLogs for a log card. When the window holds more lines than
// req.Limit, the card keeps the ones in it that look like errors or warnings, and the
// returned note says what the card holds; the note is empty when the whole window fit.
//
// Providers return the most recent req.Limit lines of the window. On a busy workload a
// 60-minute request comes back as its last few minutes, and the error lines that explain
// the alert are older than every line kept: a card for an error-spike alert could hold
// 1000 lines without one of them at ERROR, and read as "no errors".
func fetchCardLogs(ctx *security.RequestContext, req FetchLogRequest) (FetchLogsResult, string, error) {
	result, err := FetchLogs(ctx, req)
	if err != nil {
		return result, "", err
	}
	executed := result.Query
	var note string
	result.Logs, note = keepFlaggedLines(result.Logs, req.Limit, flaggedLinesBudget, func(pattern string, limit int, timeout time.Duration) ([]OutputLog, error) {
		return queryMatchingLines(ctx, req, executed, pattern, limit, timeout)
	})
	if note != "" {
		ctx.GetLogger().Info("observability: log card reached its line limit",
			"limit", req.Limit, "provider", result.Provider, "note", note)
	}
	return result, note, nil
}

// queryMatchingLines runs executed — the provider query FetchLogs ran for req — again,
// narrowed to the lines matching pattern, giving up after timeout without a retry.
//
// It goes to the source directly because executed already carries the label mapping and
// default filters FetchLogs applied. Sent through FetchLogs again, the account's default
// filters would land in an empty where-clause, and LokiSource builds its query from a
// non-empty where-clause in preference to the raw one, dropping the workload selector.
func queryMatchingLines(ctx *security.RequestContext, req FetchLogRequest, executed, pattern string, limit int, timeout time.Duration) ([]OutputLog, error) {
	source, err := getLogSourceForAccount(ctx, req.AccountId, req.LogProvider, req.LogProviderSource)
	if err != nil {
		return nil, err
	}
	filterable, ok := source.(lineFilterSource)
	if !ok {
		return nil, errors.New("log source cannot filter lines")
	}
	query, ok := filterable.withLineFilter(executed, pattern)
	if !ok {
		return nil, errors.New("log query cannot take a line filter")
	}
	logs, err := source.QueryLogs(ctx, FetchLogRequest{
		AccountId: req.AccountId,
		Query:     query,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Limit:     limit,
		Timeout:   timeout,
	})
	if err != nil {
		ctx.GetLogger().Info("observability: error-line log query failed", "timeout", timeout, "error", err)
		return nil, err
	}
	normalizeOutputLogLabels(logs, getMergedLabelMapping(ctx, req.AccountId, source))
	return logs, nil
}

// keepFlaggedLines makes room, in a card that filled its limit, for the window's lines
// that look like errors or warnings: up to half the card, the rest being the most recent
// lines as before. query runs the card's query narrowed to a line pattern, within a
// timeout; when it fails (a source that cannot filter lines, or one too slow for budget,
// included) the card keeps the lines it had.
//
// Returns the lines, newest first, and a note saying what they are — empty, with the
// lines untouched, when the window fit.
func keepFlaggedLines(recent []OutputLog, limit int, budget time.Duration, query func(pattern string, limit int, timeout time.Duration) ([]OutputLog, error)) ([]OutputLog, string) {
	if limit <= 0 || len(recent) < limit {
		return recent, ""
	}
	head := fmt.Sprintf("The queried window has more lines than the %d a log card holds.", limit)
	partial := head + " This card shows only part of it, so errors logged elsewhere in the window may not be here."
	reserve := limit / 2
	if reserve == 0 {
		return recent, partial
	}
	deadline := time.Now().Add(budget)
	flagged, more, err := queryFlaggedLines(query, errorOrWarningLines, reserve, budget)
	if err != nil {
		return recent, partial
	}
	which := "every line in it that looks like an error or warning"
	if more {
		// Warnings crowd errors out of a limited result just as info lines did. The
		// errors-only query gets what is left of the budget, and only when the first
		// query left at least half of it: a slow first answer predicts a slow second.
		which = "the most recent of its lines that look like errors or warnings"
		if left := time.Until(deadline); left >= budget/2 {
			if errs, moreErrs, err := queryFlaggedLines(query, errorLines, reserve, left); err == nil {
				flagged = firstDistinct(reserve, errs, flagged)
				which = "every line in it that looks like an error, then the most recent that look like warnings"
				if moreErrs {
					which = "the most recent of its lines that look like errors"
				}
			}
		}
	}
	kept := newestFirst(firstDistinct(limit, flagged, newestFirst(recent)))
	switch {
	case len(flagged) > 0:
		return kept, fmt.Sprintf("%s This card keeps %s (%d), and the %d most recent other lines.", head, which, len(flagged), len(kept)-len(flagged))
	case !more:
		return kept, fmt.Sprintf("%s None of its lines look like errors or warnings; these are the %d most recent.", head, len(kept))
	}
	return kept, partial
}

// queryFlaggedLines asks query for the lines matching re and keeps the ones that are not
// routine, newest first. more reports that the source had at least limit matching lines,
// so older ones may be missing.
func queryFlaggedLines(query func(pattern string, limit int, timeout time.Duration) ([]OutputLog, error), re *regexp.Regexp, limit int, timeout time.Duration) ([]OutputLog, bool, error) {
	logs, err := query(re.String(), limit, timeout)
	if err != nil {
		return nil, false, err
	}
	var flagged []OutputLog
	for _, l := range logs {
		// The source narrowed the lines already. Checking again means a source that
		// ignored the filter cannot pass routine lines off as errors.
		if !re.MatchString(l.Message) {
			continue
		}
		// The pattern finds a level word anywhere in a line; the level read off the
		// line decides. `level=info msg="cache refreshed" errors=0` is routine.
		if level := logparser.GuessLevel(l.Severity); level == logparser.LevelInfo || level == logparser.LevelDebug {
			continue
		}
		flagged = append(flagged, l)
	}
	return newestFirst(flagged), len(logs) >= limit, nil
}

// firstDistinct takes up to n lines from lists in order, skipping a line an earlier one
// already took. The same line comes back from both the card's query and a narrowed one.
func firstDistinct(n int, lists ...[]OutputLog) []OutputLog {
	out := make([]OutputLog, 0, n)
	seen := map[string]bool{}
	for _, list := range lists {
		for _, l := range list {
			if len(out) >= n {
				return out
			}
			key := l.Timestamp + "\x00" + l.Message
			if !seen[key] {
				seen[key] = true
				out = append(out, l)
			}
		}
	}
	return out
}

// newestFirst returns logs ordered newest first. Loki returns each stream newest first
// but the streams one after another. Lines without an RFC 3339 timestamp go last, in the
// order they came.
func newestFirst(logs []OutputLog) []OutputLog {
	type dated struct {
		log OutputLog
		at  time.Time
		ok  bool
	}
	rows := make([]dated, len(logs))
	for i, l := range logs {
		at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		rows[i] = dated{log: l, at: at, ok: err == nil}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ok != rows[j].ok {
			return rows[i].ok
		}
		return rows[i].ok && rows[i].at.After(rows[j].at)
	})
	out := make([]OutputLog, len(rows))
	for i, r := range rows {
		out[i] = r.log
	}
	return out
}

// putLogTruncation records on a card that it does not hold its whole window, and what it
// holds instead (see fetchCardLogs).
func putLogTruncation(additionalInfo map[string]any, note string) map[string]any {
	if note != "" {
		additionalInfo["log_truncated"] = true
		additionalInfo["log_truncation_note"] = note
	}
	return additionalInfo
}

// signozLogsAction is the action for signoz_logs_enricher.
type signozLogsAction struct{}
type signozLogsParams struct {
	Query           any                   `json:"query,omitempty"`
	Start           string                `json:"start,omitempty"`
	End             string                `json:"end,omitempty"`
	RawQuery        map[string]any        `json:"raw_query,omitempty"`
	Duration        int                   `json:"duration,omitempty"`
	RegexExtractors []RegexLabelExtractor `json:"regex_extractors,omitempty"`
	LabelExtractors []LabelExtractor      `json:"label_extractors,omitempty"`
}

func (a *signozLogsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	securityCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	provider, err := GetDefaultProvider(securityCtx, ctx.GetAccountId(), "logs", "", "")
	if err != nil {
		securityCtx.GetLogger().Error("signozLogsEnricherAction: unable to get default log provider", "error", err)
		return false
	}
	if provider.Provider != "signoz" {
		return false
	}

	if ctx.GetEvent().Labels == nil {
		return false
	}

	if ctx.GetEvent().Labels["pod"] != "" && getEventNamespace(ctx.GetEvent()) != "" {
		return true
	}

	if ctx.GetEvent().Labels["trace_id"] != "" {
		return true
	}

	if ctx.GetEvent().Labels["relatedLogsParams"] != "" {
		return true
	}

	if ctx.GetEvent().Labels["service_name"] != "" || ctx.GetEvent().Labels["service"] != "" {
		return true
	}

	// Fallback: query by workload name + namespace
	if ctx.GetEvent().SubjectName != "" && getEventNamespace(ctx.GetEvent()) != "" {
		return true
	}

	return false
}

func (a *signozLogsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	params := map[string]any{}
	namespace := getEventNamespace(ctx.GetEvent())
	if ctx.GetEvent().Labels["pod"] != "" && namespace != "" {
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": ctx.GetEvent().Labels["pod"],
						"op":    "=",
						"key": map[string]any{
							"key": "k8s.pod.name",
						},
					},
					{
						"value": namespace,
						"op":    "=",
						"key": map[string]any{
							"key": "k8s.namespace.name",
						},
					},
				},
			},
			"title": "Pod Logs For - " + ctx.GetEvent().Labels["pod"],
		}
	} else if ctx.GetEvent().Labels["trace_id"] != "" {
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": ctx.GetEvent().Labels["trace_id"],
						"op":    "=",
						"key": map[string]any{
							"key": "trace_id",
						},
					},
				},
			},
			"title": "Trace Logs For - " + ctx.GetEvent().Labels["trace_id"],
		}
	} else if ctx.GetEvent().Labels["relatedLogsParams"] != "" {
		parsedQuery, err := a.parseRelatedLogsParams(ctx.GetEvent().Labels["relatedLogsParams"])
		if err != nil {
			return nil, err
		}
		params["title"] = "Related Logs"
		params["raw_query"] = parsedQuery
	} else if ctx.GetEvent().Labels["service_name"] != "" || ctx.GetEvent().Labels["service"] != "" {
		service_name := ctx.GetEvent().Labels["service_name"]
		if service_name == "" {
			service_name = ctx.GetEvent().Labels["service"]
		}
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": service_name,
						"op":    "=",
						"key": map[string]any{
							"key": "service.name",
						},
					},
				},
			},
			"title": "Service Logs For - " + ctx.GetEvent().Labels["service_name"],
		}
	} else if ctx.GetEvent().SubjectName != "" && namespace != "" {
		workloadName := getEventWorkload(ctx.GetEvent())
		nameItem := map[string]any{
			"value": workloadName,
			"op":    "contains",
			"key": map[string]any{
				"key": "k8s.deployment.name",
			},
		}
		title := "Workload Logs For - " + workloadName
		// A pod event reads that pod's logs, not its owner's: see eventPodSubject.
		if pod := eventPodSubject(ctx.GetEvent()); pod != "" {
			nameItem = map[string]any{
				"value": pod,
				"op":    "=",
				"key": map[string]any{
					"key": "k8s.pod.name",
				},
			}
			title = "Pod Logs For - " + pod
		}
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					nameItem,
					{
						"value": namespace,
						"op":    "=",
						"key": map[string]any{
							"key": "k8s.namespace.name",
						},
					},
				},
			},
			"title": title,
		}
	}
	return a.Execute(ctx, params)
}

// parseRelatedLogsParams extracts and transforms related logs parameters
func (a *signozLogsAction) parseRelatedLogsParams(relatedLogsParams string) (map[string]any, error) {
	var rawQuery map[string]any
	if err := common.UnmarshalJsonString(relatedLogsParams, &rawQuery); err != nil {
		slog.Error("signozLogsEnricherAction: invalid relatedLogsParams json", "error", err, "relatedLogsParams", relatedLogsParams)
		return nil, errors.New("signozLogsEnricherAction: invalid relatedLogsParams json")
	}

	parsedQuery := map[string]any{}

	// Handle composite query
	if rawQuery["compositeQuery"] != nil {
		compositeQuery, err := a.parseCompositeQuery(rawQuery["compositeQuery"])
		if err != nil {
			return nil, err
		}
		parsedQuery["compositeQuery"] = compositeQuery
	}

	// Handle time range
	if timerange := rawQuery["timeRange"]; timerange != nil {
		timerangeMap, err := a.parseTimeRange(timerange)
		if err != nil {
			return nil, err
		}
		parsedQuery["start"] = timerangeMap["start"]
		parsedQuery["end"] = timerangeMap["end"]
		parsedQuery["step"] = 60
		parsedQuery["variables"] = map[string]any{}
	}

	return parsedQuery, nil
}

// parseCompositeQuery handles composite query parsing with proper error handling
func (a *signozLogsAction) parseCompositeQuery(compositeQueryRaw any) (map[string]any, error) {
	var compositeQuery map[string]any

	switch v := compositeQueryRaw.(type) {
	case string:
		if err := common.UnmarshalJsonString(v, &compositeQuery); err != nil {
			slog.Error("signozLogsEnricherAction: invalid compositeQuery json", "error", err, "compositeQuery", v)
			return nil, errors.New("signozLogsEnricherAction: invalid compositeQuery json")
		}
	case map[string]any:
		compositeQuery = v
	default:
		return nil, errors.New("signozLogsEnricherAction: compositeQuery must be string or map")
	}

	// Build final query structure
	finalQuery := map[string]any{
		"panelType":      "list",
		"queryType":      "builder",
		"fillGaps":       false,
		"builderQueries": map[string]any{},
	}

	// Extract and process query data
	if builder, ok := compositeQuery["builder"].(map[string]any); ok {
		if queryDataRaw, ok := builder["queryData"].([]any); ok {
			builderQueries := finalQuery["builderQueries"].(map[string]any)
			for _, qdRaw := range queryDataRaw {
				if qd, ok := qdRaw.(map[string]any); ok {
					if queryName, ok := qd["queryName"].(string); ok {
						builderQueries[strings.TrimSpace(queryName)] = qd
					}
				}
			}
		}
	}

	return finalQuery, nil
}

// parseTimeRange handles time range parsing with proper error handling
func (a *signozLogsAction) parseTimeRange(timerangeRaw any) (map[string]any, error) {
	var timerangeMap map[string]any

	switch v := timerangeRaw.(type) {
	case string:
		if err := common.UnmarshalJsonString(v, &timerangeMap); err != nil {
			slog.Error("signozLogsEnricherAction: invalid timeRange json", "error", err, "timeRange", v)
			return nil, errors.New("signozLogsEnricherAction: invalid timeRange json")
		}
	case map[string]any:
		timerangeMap = v
	default:
		return nil, errors.New("signozLogsEnricherAction: timeRange must be string or map")
	}

	return timerangeMap, nil
}

// getSignozBaseURL retrieves the SigNoz base URL from the agent table for the given account
func getSignozBaseURL(accountId string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("signozLogsAction: failed to get database manager for SigNoz URL", "error", err)
		return ""
	}

	query := `SELECT connection_status->>'logProviderUrl'
	          FROM agent
	          WHERE cloud_account_id = $1
	          AND connection_status->>'logsConnectionProvider' = 'signoz'
	          LIMIT 1`

	var logProviderUrl string
	err = dbms.Db.QueryRowx(query, accountId).Scan(&logProviderUrl)
	if err != nil {
		slog.Debug("signozLogsAction: no SigNoz URL found for account", "accountId", accountId, "error", err)
		return ""
	}

	return logProviderUrl
}

// transformToSignozURLFormat transforms the internal compositeQuery format to SigNoz URL format
// Internal format uses builderQueries (object), URL format uses builder.queryData (array)
func transformToSignozURLFormat(compositeQuery map[string]any) map[string]any {
	urlFormat := map[string]any{
		"queryType": "builder",
		"builder": map[string]any{
			"queryData":     []any{},
			"queryFormulas": []any{},
		},
		"promql": []map[string]any{
			{"name": "A", "query": "", "legend": "", "disabled": false},
		},
		"clickhouse_sql": []map[string]any{
			{"name": "A", "legend": "", "disabled": false, "query": ""},
		},
		"id": common.GenerateUUID(),
	}

	// Convert builderQueries (object) to builder.queryData (array)
	if builderQueries, ok := compositeQuery["builderQueries"].(map[string]any); ok {
		queryData := []any{}
		for _, query := range builderQueries {
			if queryMap, ok := query.(map[string]any); ok {
				queryData = append(queryData, transformQueryForSignozURL(queryMap))
			}
		}
		urlFormat["builder"].(map[string]any)["queryData"] = queryData
	}

	return urlFormat
}

// transformQueryForSignozURL transforms a single query to SigNoz URL format
func transformQueryForSignozURL(query map[string]any) map[string]any {
	result := map[string]any{
		"dataSource":        getSignozMapValue(query, "dataSource", "logs"),
		"queryName":         getSignozMapValue(query, "queryName", "A"),
		"aggregateOperator": getSignozMapValue(query, "aggregateOperator", "noop"),
		"aggregateAttribute": map[string]any{
			"id": "------false", "dataType": "", "key": "",
			"isColumn": false, "type": "", "isJSON": false,
		},
		"timeAggregation":  getSignozMapValue(query, "timeAggregation", "rate"),
		"spaceAggregation": getSignozMapValue(query, "spaceAggregation", "sum"),
		"functions":        getSignozMapValue(query, "functions", []any{}),
		"expression":       getSignozMapValue(query, "expression", "A"),
		"disabled":         getSignozMapValue(query, "disabled", false),
		"stepInterval":     getSignozMapValue(query, "stepInterval", 60),
		"having":           getSignozMapValue(query, "having", []any{}),
		"limit":            nil,
		"orderBy":          getSignozMapValue(query, "orderBy", []map[string]any{{"columnName": "timestamp", "order": "desc"}}),
		"groupBy":          getSignozMapValue(query, "groupBy", []any{}),
		"legend":           getSignozMapValue(query, "legend", ""),
		"reduceTo":         getSignozMapValue(query, "reduceTo", "avg"),
	}

	// Transform filters with enhanced key structure
	if filters, ok := query["filters"].(map[string]any); ok {
		result["filters"] = transformFiltersForSignozURL(filters)
	} else {
		result["filters"] = map[string]any{"items": []any{}, "op": "AND"}
	}

	return result
}

// getSignozMapValue returns the value from map or default if not present
func getSignozMapValue(m map[string]any, key string, defaultValue any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return defaultValue
}

// transformFiltersForSignozURL transforms filters to SigNoz URL format
func transformFiltersForSignozURL(filters map[string]any) map[string]any {
	result := map[string]any{
		"items": []any{},
		"op":    getSignozMapValue(filters, "op", "AND"),
	}

	if items, ok := filters["items"].([]any); ok {
		transformedItems := []any{}
		for _, item := range items {
			if itemMap, ok := item.(map[string]any); ok {
				transformedItems = append(transformedItems, transformFilterItemForSignozURL(itemMap))
			}
		}
		result["items"] = transformedItems
	} else if items, ok := filters["items"].([]map[string]any); ok {
		transformedItems := []any{}
		for _, itemMap := range items {
			transformedItems = append(transformedItems, transformFilterItemForSignozURL(itemMap))
		}
		result["items"] = transformedItems
	}

	return result
}

// transformFilterItemForSignozURL transforms a single filter item to SigNoz URL format
func transformFilterItemForSignozURL(item map[string]any) map[string]any {
	// Get the key name from nested structure
	var keyName string
	if keyObj, ok := item["key"].(map[string]any); ok {
		if k, ok := keyObj["key"].(string); ok {
			keyName = k
		}
	}

	// Generate short ID (8 hex chars)
	shortID := common.GenerateUUID()[:8]

	return map[string]any{
		"id": shortID,
		"key": map[string]any{
			"key":      keyName,
			"dataType": "string",
			"type":     "resource",
			"isColumn": true,
			"isJSON":   false,
			"id":       fmt.Sprintf("%s--string--resource--true", keyName),
		},
		"op":    getSignozMapValue(item, "op", "="),
		"value": item["value"],
	}
}

// buildSignozLogsExplorerURL constructs the SigNoz logs explorer URL with encoded query parameters
func buildSignozLogsExplorerURL(baseURL string, rawQuery map[string]any, startTime, endTime time.Time) (string, error) {
	// Extract compositeQuery from rawQuery
	compositeQueryRaw, ok := rawQuery["compositeQuery"]
	if !ok {
		return "", fmt.Errorf("compositeQuery not found in rawQuery")
	}

	compositeQuery, ok := compositeQueryRaw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("compositeQuery is not a map")
	}

	// Transform internal format to SigNoz URL format
	urlCompositeQuery := transformToSignozURLFormat(compositeQuery)

	// Marshal compositeQuery to JSON
	compositeQueryJSON := common.MarshalJsonSafeString(urlCompositeQuery)
	if compositeQueryJSON == "" {
		return "", fmt.Errorf("failed to marshal compositeQuery")
	}

	// Create options JSON
	options := map[string]any{
		"selectColumns": []string{},
		"maxLines":      2,
		"format":        "list",
	}
	optionsJSON := common.MarshalJsonSafeString(options)
	if optionsJSON == "" {
		return "", fmt.Errorf("failed to marshal options")
	}

	// Create timeRange JSON
	timeRange := map[string]any{
		"start":    startTime.UnixMilli(),
		"end":      endTime.UnixMilli(),
		"pageSize": 100,
	}
	timeRangeJSON := common.MarshalJsonSafeString(timeRange)
	if timeRangeJSON == "" {
		return "", fmt.Errorf("failed to marshal timeRange")
	}

	// Build URL with query parameters
	// compositeQuery needs to be double-encoded
	encodedCompositeQuery := url.QueryEscape(compositeQueryJSON)

	// Build the URL
	baseURL = strings.TrimRight(baseURL, "/")
	logsExplorerURL := fmt.Sprintf("%s/logs/logs-explorer?options=%s&compositeQuery=%s&timeRange=%s",
		baseURL,
		url.QueryEscape(optionsJSON),
		url.QueryEscape(encodedCompositeQuery),
		url.QueryEscape(timeRangeJSON),
	)

	return logsExplorerURL, nil
}

// signozOpToCanonical is the inverse of FlattenSignozQuery's operator switch,
// mapping a SigNoz filter op string back to the canonical where-clause operator.
var signozOpToCanonical = map[string]query.BinaryWhereClauseType{
	"=":         Eq,
	"!=":        Nq,
	"in":        In,
	"nin":       NotIn,
	"like":      Like,
	"nlike":     NLike,
	"contains":  Contains,
	"ncontains": NotContains,
	"exists":    Exist,
	"nexists":   NotExist,
	"regex":     RegEx,
	"nregex":    NotRegEx,
	"ilike":     ILike,
	">":         Gt,
	">=":        Gte,
	"<":         Lt,
	"<=":        Lte,
}

// signozRawQueryToWhereClause converts a raw_query (a SigNoz composite query, e.g.
// from an alert's related-logs link) into a canonical where-clause, so it runs
// through FetchLogs on whichever SigNoz source the account uses. Only plain log
// lists convert: each enabled builder query must be a noop logs query whose
// filter items are ANDed; several queries are ORed. Anything else reports false
// and stays on the agent-relay path.
func signozRawQueryToWhereClause(raw map[string]any) (query.QueryWhereClause, bool) {
	composite, _ := raw["compositeQuery"].(map[string]any)
	if composite == nil {
		return query.QueryWhereClause{}, false
	}
	if panel, _ := composite["panelType"].(string); panel != "" && panel != "list" {
		return query.QueryWhereClause{}, false
	}
	builderQueries, _ := composite["builderQueries"].(map[string]any)
	names := make([]string, 0, len(builderQueries))
	for name := range builderQueries {
		names = append(names, name)
	}
	sort.Strings(names)
	var clauses []query.QueryWhereClause
	for _, name := range names {
		q, ok := builderQueries[name].(map[string]any)
		if !ok {
			return query.QueryWhereClause{}, false
		}
		if disabled, _ := q["disabled"].(bool); disabled {
			continue
		}
		if ds, _ := q["dataSource"].(string); ds != "" && ds != "logs" {
			return query.QueryWhereClause{}, false
		}
		if agg, _ := q["aggregateOperator"].(string); agg != "" && agg != "noop" {
			return query.QueryWhereClause{}, false
		}
		filters, _ := q["filters"].(map[string]any)
		if op, _ := filters["op"].(string); op != "" && !strings.EqualFold(op, "AND") {
			return query.QueryWhereClause{}, false
		}
		if !signozRawItemsConvertible(filters["items"]) {
			return query.QueryWhereClause{}, false
		}
		where, err := signozFilterItemsToWhereClause(filters)
		if err != nil {
			return query.QueryWhereClause{}, false
		}
		clauses = append(clauses, where)
	}
	switch len(clauses) {
	case 0:
		return query.QueryWhereClause{}, false
	case 1:
		return clauses[0], true
	}
	return query.QueryWhereClause{Or: clauses}, true
}

// signozRawItemsConvertible reports whether every filter item converts without
// losing meaning. The where-clause carries only the key name, so an item typed
// as non-string (e.g. a float64 status code, compared numerically by SigNoz) or
// one that cannot be read keeps the agent-relay path, which sends items verbatim.
func signozRawItemsConvertible(raw any) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			return false
		}
		key, ok := item["key"].(map[string]any)
		if !ok {
			return false
		}
		if name, _ := key["key"].(string); name == "" {
			return false
		}
		if dt, _ := key["dataType"].(string); dt != "" && dt != "string" {
			return false
		}
	}
	return true
}

// signozRawQueryWindow reads a raw_query's own start/end (Unix ms), falling back
// to the given window for whichever is absent.
func signozRawQueryWindow(raw map[string]any, start, end time.Time) (time.Time, time.Time) {
	ms := func(v any) (time.Time, bool) {
		var n float64
		switch x := v.(type) {
		case float64:
			n = x
		case int64:
			n = float64(x)
		case int:
			n = float64(x)
		case string:
			f, err := strconv.ParseFloat(x, 64)
			if err != nil {
				return time.Time{}, false
			}
			n = f
		default:
			return time.Time{}, false
		}
		if n <= 0 {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(n)), true
	}
	if t, ok := ms(raw["start"]); ok {
		start = t
	}
	if t, ok := ms(raw["end"]); ok {
		end = t
	}
	return start, end
}

// signozFilterItemsToWhereClause converts SigNoz filter items (the "query" playbook
// param) into a canonical where-clause — the inverse of FlattenSignozQuery. It
// accepts the {op, items} map form and the []any / []map[string]any array forms
// (the same shapes the legacy path handled), AND-ing all items together.
func signozFilterItemsToWhereClause(q any) (query.QueryWhereClause, error) {
	var items []any
	switch v := q.(type) {
	case map[string]any:
		if raw, ok := v["items"].([]any); ok {
			items = raw
		}
	case []any:
		items = v
	case []map[string]any:
		for _, it := range v {
			items = append(items, it)
		}
	}

	clauses := make([]query.QueryWhereClause, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := ""
		if k, ok := item["key"].(map[string]any); ok {
			key, _ = k["key"].(string)
		}
		if key == "" {
			continue
		}
		opStr, _ := item["op"].(string)
		canonicalOp, ok := signozOpToCanonical[opStr]
		if !ok {
			return query.QueryWhereClause{}, fmt.Errorf("signoz_logs_enricher: unsupported filter op %q", opStr)
		}
		clauses = append(clauses, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{key: {canonicalOp: item["value"]}},
		})
	}

	if len(clauses) == 0 {
		return query.QueryWhereClause{}, errors.New("signoz_logs_enricher: no valid filter items in query")
	}
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	return query.QueryWhereClause{And: clauses}, nil
}

// buildSignozRawQueryFromWhere reconstructs the SigNoz composite query (used only
// to build the Logs Explorer reference URL) from a canonical where-clause. FetchLogs
// rebuilds the same query internally from the same clause, so the deep-link matches
// the fetched logs.
func buildSignozRawQueryFromWhere(where query.QueryWhereClause, startTime, endTime time.Time) (map[string]any, error) {
	flattened, err := FlattenSignozQuery(where)
	if err != nil {
		return nil, err
	}
	filterItems := make([]map[string]any, 0, len(flattened))
	for _, fc := range flattened {
		filterItems = append(filterItems, map[string]any{
			"key":   map[string]any{"key": fc.Key["key"]},
			"op":    fc.Op,
			"value": fc.Value,
		})
	}
	return map[string]any{
		"compositeQuery": map[string]any{
			"builderQueries": map[string]any{
				"A": map[string]any{
					"aggregateAttribute": map[string]any{},
					"aggregateOperator":  "noop",
					"dataSource":         "logs",
					"filters": map[string]any{
						"items": filterItems,
						"op":    "AND",
					},
					"orderBy": []map[string]any{
						{"columnName": "timestamp", "order": "desc"},
					},
				},
			},
			"fillGaps":  false,
			"panelType": "list",
			"queryType": "builder",
		},
		"end":       endTime.UnixMilli(),
		"start":     startTime.UnixMilli(),
		"step":      60,
		"variables": map[string]any{},
	}, nil
}

// runSignozFetch executes a canonical where-clause through FetchLogs and builds the
// standard log response, attaching the SigNoz Logs Explorer reference URL.
func (a *signozLogsAction) runSignozFetch(ctx playbooks.PlaybookActionContext, where query.QueryWhereClause, title string, startTime, endTime time.Time, metaQuery any, extractors []RegexLabelExtractor, labelExtractors []LabelExtractor) (playbooks.PlaybookActionResponse, error) {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	logResult, err := FetchLogs(requestCtx, FetchLogRequest{
		AccountId: ctx.GetAccountId(),
		StartTime: startTime.UnixMilli(),
		EndTime:   endTime.UnixMilli(),
		Limit:     1000,
		QueryRequest: LogsQueryBuilderRequest{
			Where: where,
		},
	})
	if err != nil {
		return nil, err
	}

	additionalInfo := addExecutedQueryInfo(map[string]any{}, logResult)
	if baseURL := getSignozBaseURL(ctx.GetAccountId()); baseURL != "" {
		if rawQuery, buildErr := buildSignozRawQueryFromWhere(where, startTime, endTime); buildErr == nil {
			if explorerURL, urlErr := buildSignozLogsExplorerURL(baseURL, rawQuery, startTime, endTime); urlErr == nil {
				additionalInfo["reference_url"] = explorerURL
			} else {
				ctx.GetLogger().Warn("signozLogsAction: failed to build logs explorer URL", "error", urlErr, "accountId", ctx.GetAccountId())
			}
		}
	}

	return logsResponseOrNil(buildLogsActionResponse(logResult.Logs, title, additionalInfo, actionLogMaxInsightErrors, metaQuery, extractors, labelExtractors))
}

func (a *signozLogsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params signozLogsParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, err
	}
	startTime, endTime := a.resolveTimeWindow(ctx, params)

	title, _ := rawParams["title"].(string)

	// raw_query / relatedLogsParams carry a SigNoz composite query. A plain log
	// list converts to a where-clause and runs through FetchLogs, which reaches a
	// URL-connected SigNoz as well as one behind the agent; only shapes that cannot
	// convert (aggregates, OR-ed filter items) keep the agent-relay path.
	if params.RawQuery != nil {
		if where, ok := signozRawQueryToWhereClause(params.RawQuery); ok {
			start, end := signozRawQueryWindow(params.RawQuery, startTime, endTime)
			return a.runSignozFetch(ctx, where, title, start, end, rawParams, params.RegexExtractors, params.LabelExtractors)
		}
		return a.executeRawQueryLegacy(ctx, rawParams, params, startTime, endTime)
	}

	where, err := signozFilterItemsToWhereClause(params.Query)
	if err != nil {
		return nil, err
	}
	return a.runSignozFetch(ctx, where, title, startTime, endTime, rawParams, params.RegexExtractors, params.LabelExtractors)
}

// resolveTimeWindow computes the [start,end] window from the event (preferred) or
// the params.Duration default (60m).
func (a *signozLogsAction) resolveTimeWindow(ctx playbooks.PlaybookActionContext, params signozLogsParams) (time.Time, time.Time) {
	endTime := time.Now()
	durationMinues := 60
	if params.Duration != 0 {
		durationMinues = params.Duration
	}
	startTime := endTime.Add(-time.Duration(durationMinues) * time.Minute)
	if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	}
	return startTime, endTime
}

// executeRawQueryLegacy runs the opaque SigNoz composite query (raw_query /
// relatedLogsParams) directly via the relay signoz_query_range action, parsing the
// SigNoz-native nested response. Retained for inputs that can't be canonicalized.
func (a *signozLogsAction) executeRawQueryLegacy(ctx playbooks.PlaybookActionContext, rawParams map[string]any, params signozLogsParams, startTime, endTime time.Time) (playbooks.PlaybookActionResponse, error) {
	filters := map[string]any{}
	if queryMap, ok := params.Query.(map[string]any); ok && queryMap["items"] != nil {
		filters = params.Query.(map[string]any)
	} else if queryArr, ok := params.Query.([]any); ok {
		filters = map[string]any{
			"op":    "AND",
			"items": queryArr,
		}
	} else if queryArr, ok := params.Query.([]map[string]any); ok {
		filters = map[string]any{
			"op":    "AND",
			"items": queryArr,
		}
	}

	var rawQuery map[string]any
	if params.RawQuery != nil {
		rawQuery = params.RawQuery
	} else {
		rawQuery = map[string]any{
			"compositeQuery": map[string]any{
				"builderQueries": map[string]any{
					"A": map[string]any{
						"aggregateAttribute": map[string]any{},
						"aggregateOperator":  "noop",
						"dataSource":         "logs",
						"disabled":           false,
						"expression":         "A",
						"functions":          []any{},
						"groupBy":            []any{},
						"having":             []any{},
						"legend":             "",
						"limit":              100,
						"offset":             0,
						"pageSize":           100,
						"queryName":          "A",
						"reduceTo":           "avg",
						"spaceAggregation":   "sum",
						"stepInterval":       60,
						"timeAggregation":    "rate",
						"filters":            filters,
						"orderBy": []map[string]any{
							{"columnName": "timestamp", "order": "desc"},
						},
					},
				},
				"fillGaps":  false,
				"panelType": "list",
				"queryType": "builder",
			},
			"end":       endTime.UnixMilli(),
			"start":     startTime.UnixMilli(),
			"step":      60,
			"variables": map[string]any{},
		}
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    ctx.GetAccountId(),
			ActionName:   "signoz_query_range",
			ActionParams: rawQuery,
			Origin:       "services-server",
			NoSinks:      true,
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		return nil, err
	}

	relayDataAny := relayResponse["data"]
	if relayDataAny == nil {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}

	relayData, ok := relayDataAny.(map[string]any)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}

	if relayData["success"] != nil {
		if success, ok := relayData["success"].(bool); !ok || !success {
			ctx.GetLogger().Error("relay: relay query success is false",
				"relay_error_code", relayData["error_code"],
				"relay_msg", relayData["msg"],
				"response", slog.AnyValue(relayData))
			return nil, errors.New("relay: unable to execute relay query")
		}
	}

	dataAny, ok := relayData["data"]
	if !ok || dataAny == nil {
		ctx.GetLogger().Error("relay: relay query data is nil", "response", slog.AnyValue(relayData))
		return nil, errors.New("relay: unable to execute relay query")
	}
	data, ok := dataAny.(map[string]any)
	if !ok {
		ctx.GetLogger().Error("relay: relay query data is not a map", "response", slog.AnyValue(relayData))
		return nil, errors.New("relay: unable to execute relay query")
	}

	insight := []playbooks.PlaybookActionResponseInsight{}
	errorInsights := a.extractTopErrorLogs(data)
	insight = append(insight, errorInsights...)
	additionalInfo := map[string]any{}
	if rawParams["title"] != "" {
		additionalInfo["title"] = rawParams["title"]
	}

	// Add log summary (level breakdown + pattern grouping)
	logEntries := a.extractLogEntries(data)
	if summary := playbooks.ComputeLogSummary(logEntries); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	putLogGroups(additionalInfo, playbooks.SummarizeLogEntries(signozLogEntries(logEntries), 0))

	// Generate SigNoz logs explorer URL
	baseURL := getSignozBaseURL(ctx.GetAccountId())
	if baseURL != "" {
		logsExplorerURL, err := buildSignozLogsExplorerURL(baseURL, rawQuery, startTime, endTime)
		if err != nil {
			ctx.GetLogger().Warn("signozLogsAction: failed to build logs explorer URL", "error", err, "accountId", ctx.GetAccountId())
		} else {
			additionalInfo["reference_url"] = logsExplorerURL
			ctx.GetLogger().Debug("signozLogsAction: generated logs explorer URL", "url", logsExplorerURL)
		}
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"end":                  endTime.UnixMilli(),
		"start":                startTime.UnixMilli(),
	}
	// Create base response using the standard function
	baseResponse := playbooks.NewPlaybookActionResponseJson(data, additionalInfo, insight, metadata)

	// Create enhanced response with label extraction capability
	response := &playbooks.LogsActionResponse{
		Data:            baseResponse.Data,
		AdditionalInfo:  baseResponse.AdditionalInfo,
		Insight:         baseResponse.Insight,
		Metadata:        baseResponse.Metadata,
		ExtractedLabels: a.extractLabelsFromLogs(data, params.RegexExtractors, params.LabelExtractors),
	}
	return response, err
}

// extractLabelsFromLogs processes log data and applies regex patterns to extract labels
func (a *signozLogsAction) extractLabelsFromLogs(data map[string]any, extractors []RegexLabelExtractor, labelExtractor []LabelExtractor) map[string]any {
	extractedLabels := make(map[string]any)

	if len(extractors) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	// Compile regex patterns
	compiledPatterns := actionLogCompileRegexPatterns(extractors)
	if len(compiledPatterns) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	// Get log entries from data structure
	logEntries := a.extractLogEntries(data)
	if len(logEntries) == 0 {
		return extractedLabels
	}

	// Process ALL log entries to collect all unique values for each label
	for _, logData := range logEntries {
		body, _ := logData["body"].(string)

		// Apply regex extractors (even if body is empty, we might have label extractors)
		if body != "" {
			actionLogApplyRegexExtractors(body, compiledPatterns, extractors, extractedLabels)
		}

		// Apply label extractors
		actionLogApplyLabelExtractors(logData, labelExtractor, extractedLabels)
	}

	actionLogExtractionResults(extractedLabels, len(compiledPatterns))
	return extractedLabels
}

// extractLogEntries extracts log data from the nested data structure
func (a *signozLogsAction) extractLogEntries(data map[string]any) []map[string]any {
	var logEntries []map[string]any

	result, ok := data["data"].(map[string]any)
	if !ok {
		return logEntries
	}

	resultData, ok := result["result"].([]any)
	if !ok {
		return logEntries
	}

	for _, r := range resultData {
		resultMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		list, ok := resultMap["list"].([]any)
		if !ok {
			continue
		}

		for _, entry := range list {
			logEntry, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			logData, ok := logEntry["data"].(map[string]any)
			if ok {
				logEntries = append(logEntries, logData)
			}
		}
	}

	return logEntries
}

// signozLogEntries maps SigNoz rows onto the fields the grouping reads.
func signozLogEntries(rows []map[string]any) []playbooks.LogEntry {
	entries := make([]playbooks.LogEntry, 0, len(rows))
	for _, row := range rows {
		body, _ := row["body"].(string)
		severity, _ := row["severity_text"].(string)
		entries = append(entries, playbooks.LogEntry{Line: body, Level: severity, Timestamp: firstStringField(row, "timestamp")})
	}
	return entries
}

func (a *signozLogsAction) extractTopErrorLogs(data map[string]any) []playbooks.PlaybookActionResponseInsight {
	var insights []playbooks.PlaybookActionResponseInsight

	resultData, ok := data["data"].(map[string]any)
	if !ok || len(resultData) == 0 {
		return insights
	}
	result, ok := resultData["result"].([]any)
	if !ok || len(result) == 0 {
		return insights
	}

	var errorLogs []map[string]any
	var allLogs []map[string]any

	for _, r := range result {
		resultMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		list, ok := resultMap["list"].([]any)
		if !ok {
			continue
		}

		for _, entry := range list {
			logEntry, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			logData, ok := logEntry["data"].(map[string]any)
			if !ok {
				continue
			}

			severityText, _ := logData["severity_text"].(string)
			// Check if this is an error level log
			if severityText == "ERROR" || severityText == "FATAL" {
				errorLogs = append(errorLogs, logData)
			}
			// Store all logs for fallback pattern matching
			allLogs = append(allLogs, logData)
		}
	}

	for i, logData := range errorLogs {
		if i >= actionLogMaxInsightErrors {
			break
		}

		body, _ := logData["body"].(string)
		severityText, _ := logData["severity_text"].(string)

		if body != "" {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("%s: %s", severityText, body),
				Severity: "Critical",
			})
		}
	}

	// Fallback: if no severity-based error logs found, search for error patterns
	if len(insights) == 0 {
		insights = actionLogExtractErrorPatterns(allLogs, actionLogMaxInsightErrors)
	}

	return insights
}

type observabilityLogAction struct{}
type observabilityLogActionParams struct {
	Query           string                `json:"query,omitempty"`
	QueryOptions    map[string]any        `json:"query_options,omitempty"`
	AccountId       string                `json:"account_id,omitempty"`
	Duration        int                   `json:"duration,omitempty"`
	RegexExtractors []RegexLabelExtractor `json:"regex_extractors,omitempty"`
	LabelExtractors []LabelExtractor      `json:"label_extractors,omitempty"`
}

// resolveLogQueryWindow returns the event's log query window in epoch milliseconds.
// The floor/cap rules live on PlaybookEvent so the log, metric, and cloud-log
// enrichers all derive their window the same way — see PlaybookEvent.ResolveQueryWindow.
func resolveLogQueryWindow(event playbooks.PlaybookEvent, durationMinutes int) (int64, int64) {
	start, end := event.ResolveQueryWindow(durationMinutes)
	return start.UnixMilli(), end.UnixMilli()
}

// getEventNamespace returns the namespace for log collection from SubjectNamespace.
// For webhook events this is populated from Labels["namespace"] during enrichment.
// For agent events this is set directly from the k8s resource metadata.
func getEventNamespace(event playbooks.PlaybookEvent) string {
	return event.SubjectNamespace
}

// getEventWorkload returns the deployment-shaped name for observability queries
// keyed on workload (k8s.deployment.name / Datadog `kube_deployment` /
// clickhouse otel_traces `workload_name`).
//
// Prefer SubjectOwner (e.g. "checkout") over SubjectName (e.g.
// "checkout-66cbdd8d5-w6kxc", a pod with ReplicaSet hash) — the storage
// columns hold deployment names without the hash, so passing SubjectName
// silently returns zero results for any prometheus-source event the agent
// emitted with a pod-level SubjectName. SubjectOwner is populated upstream
// in `event.InvestigateEvent` from alert labels or a k8s_pods lookup;
// when present it carries the correct deployment shape.
//
// Fall through to SubjectName when SubjectOwner is empty so events that
// pre-date the upstream resolver (and any path that bypasses it) still
// produce a best-effort query rather than nothing.
func getEventWorkload(event playbooks.PlaybookEvent) string {
	// A CronJob is the exception: its owner is one level too high. The subject
	// `nightly-report-29836288` owned by CronJob `nightly-report` resolved to the
	// CronJob, and LokiSource's generator turns that into `pod=~"nightly-report-.*"`
	// — which matches every run the CronJob has ever made. 69 of 976 job_failure
	// findings on dev in the week to 2026-09-23 were captioned with a *different*
	// run's output that way. The run's own name is already workload-shaped (a Job
	// name carries no ReplicaSet hash), so it needs no owner lookup.
	if strings.EqualFold(event.SubjectOwnerKind, "cronjob") && event.SubjectName != "" {
		return event.SubjectName
	}
	if event.SubjectOwner != "" {
		return event.SubjectOwner
	}
	return event.SubjectName
}

// eventPodSubject returns the pod an event is about, or "" when its subject is
// not a pod.
//
// Evidence for a pod event has to come from that pod. getEventWorkload resolves
// to the owner, which is right for trace and service queries but wrong for logs
// and metrics: a workload-scoped log query returns every replica's lines (and
// `kubectl logs <kind>/<owner>` reads one arbitrary replica), and every
// provider's utilisation query sums all the owner's pods into one series. On an
// OOMKilled DaemonSet pod that put another node's logs and the whole fleet's
// memory under the killed pod's finding.
func eventPodSubject(event playbooks.PlaybookEvent) string {
	if strings.EqualFold(event.SubjectType, "pod") {
		return event.SubjectName
	}
	return ""
}

// Kubernetes object naming rules (RFC 1123). Namespaces are labels; workload and pod
// names are subdomains, so they additionally allow dots.
var (
	k8sNamespacePattern  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	k8sObjectNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
)

// isK8sLogTarget reports whether (workloadName, namespace) can name a real Kubernetes
// object, and therefore whether asking the agent for its logs can ever succeed.
//
// Not every event with a "subject" has a K8s one. CI events carry a repository as the
// namespace and a job as the workload ("nudgebee/nudgebee-enterprise" /
// "label-prs #32334764400"), which the relay fallback happily turned into
// `kubectl logs deployment/label-prs #32334764400 -n nudgebee/nudgebee-enterprise` —
// a guaranteed failure, one relay round-trip per event, and a shell metacharacter in a
// command string. Screening on the naming rules keeps those events out of the agent path
// entirely; they still get whatever the configured log source can answer.
func isK8sLogTarget(workloadName, namespace string) bool {
	if namespace == "" || len(namespace) > 63 || !k8sNamespacePattern.MatchString(namespace) {
		return false
	}
	return workloadName != "" && len(workloadName) <= 253 && k8sObjectNamePattern.MatchString(workloadName)
}

// noWorkloadLogAggKeys are event classes where a workload-scoped log query
// cannot return the subject's logs, so whatever it does return is another pod's.
//
// An ImagePullBackOff pod never started a container and has no logs by
// definition, but `logs` queries by workload, so it answered with the healthy
// replicas still serving the old image. On dev, 130 of 133 such payloads
// contained no reference to the failing pod at all, and 48 carried error lines
// from unrelated work — rendered under the failing pod's finding. The image
// name, the pull error and the pod events are the evidence here, and
// pod_enricher plus resource_events_enricher already collect them.
var noWorkloadLogAggKeys = map[string]bool{
	"image_pull_backoff_reporter": true,
}

func (a *observabilityLogAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if noWorkloadLogAggKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getLogSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")
	namespace := getEventNamespace(ctx.GetEvent())
	if err != nil || source == nil {
		// No configured log source — allow relay fallback only for non-cloud events
		// whose subject the agent could actually resolve.
		if isK8sLogTarget(getEventWorkload(ctx.GetEvent()), namespace) && !isCloudEventSource(ctx.GetEvent().Source) {
			return true
		}
		return false
	}

	// Events carrying a trace_id can be correlated directly, even without a
	// workload name (e.g. APM / webhook alerts) — as long as the backend can
	// actually filter on trace_id.
	if ctx.GetEvent().Labels["trace_id"] != "" && logSourceSupportsTraceIDFilter(requestCtx, ctx.GetAccountId(), source) {
		return true
	}

	if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
		return autoExecutor.CanGenerateQuery(ctx)
	}

	// Source exists but doesn't implement PlaybookQueryGenerator — use workload-based query
	if ctx.GetEvent().SubjectName != "" && namespace != "" {
		return true
	}

	return false
}

func (a *observabilityLogAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getLogSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")

	// Trace-correlated logs take priority over workload logs when the event
	// carries a trace_id and the backend can actually filter on it. Falls
	// through to the generator/workload paths when the query matches nothing.
	if err == nil && source != nil && ctx.GetEvent().Labels["trace_id"] != "" &&
		logSourceSupportsTraceIDFilter(requestCtx, ctx.GetAccountId(), source) {
		result, traceErr := a.autoExecuteByTraceID(ctx)
		if traceErr == nil && result != nil {
			return result, nil
		}
		ctx.GetLogger().Info("observability: trace_id log query found nothing, falling back",
			"trace_id", ctx.GetEvent().Labels["trace_id"], "error", traceErr)
	}

	// Try PlaybookQueryGenerator if source supports it
	if err == nil && source != nil {
		if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
			query, request, genErr := autoExecutor.GenerateQuery(ctx)
			if genErr == nil {
				params := map[string]any{
					"query":         query,
					"query_options": request,
				}
				result, execErr := a.Execute(ctx, params)
				if execErr == nil && result != nil {
					return result, nil
				}
				ctx.GetLogger().Info("observability: PlaybookQueryGenerator Execute failed, falling back to workload query", "error", execErr)
			} else {
				ctx.GetLogger().Info("observability: PlaybookQueryGenerator failed, trying workload query", "error", genErr)
			}
		}
	}

	// Fallback: workload-based query via configured log source, then relay
	return a.autoExecuteByWorkload(ctx)
}

// autoExecuteByTraceID queries the configured log source for logs correlated to the
// event's trace_id label. The where clause uses the canonical "trace_id" key so
// getMergedLabelMapping can translate it to the provider-specific field name.
// Returns (nil, nil) when the query matches nothing so the caller falls back to the
// workload-based query.
func (a *observabilityLogAction) autoExecuteByTraceID(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	traceID := ctx.GetEvent().Labels["trace_id"]

	endTime := time.Now().UnixMilli()
	startTime := endTime - int64(60*60*1000) // 1 hour default
	if ctx.GetEvent().StartedAt != nil {
		startTime = ctx.GetEvent().StartedAt.UnixMilli()
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = ctx.GetEvent().EndedAt.UnixMilli()
	}
	// The trace query is deliberately unscoped (traces cross namespaces), so cap
	// the window: a long-lived alert (StartedAt days ago, no EndedAt) must not
	// turn it into a full-backend scan over days of logs.
	const maxTraceWindowMs = int64(6 * 60 * 60 * 1000)
	if endTime-startTime > maxTraceWindowMs {
		startTime = endTime - maxTraceWindowMs
	}

	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	logResult, truncation, err := fetchCardLogs(requestCtx, FetchLogRequest{
		AccountId: ctx.GetAccountId(),
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     1000,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"trace_id": {query.Eq: traceID}},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(logResult.Logs) == 0 {
		return nil, nil
	}

	ctx.GetLogger().Info("observability: logs auto action found trace-correlated results",
		"trace_id", traceID, "count", len(logResult.Logs))
	additionalInfo := putLogTruncation(addExecutedQueryInfo(map[string]any{"title": "Trace Logs For - " + traceID}, logResult), truncation)
	return a.buildLogResponse(logResult.Logs, map[string]any{
		"trace_id": traceID,
		"source":   "configured_integration",
	}, additionalInfo)
}

// logSourceSupportsTraceIDFilter reports whether the source can actually filter on
// trace_id. Sources that declare trace_id (canonical or mapped name) in
// GetIgnoredQueryRequestKeys strip the condition before querying, which would turn a
// "logs for this trace" query into an unfiltered log dump presented as trace
// evidence — worse than falling back to the workload query.
func logSourceSupportsTraceIDFilter(ctx *security.RequestContext, accountId string, source LogSource) bool {
	filter, ok := source.(QueryRequestKeyFilter)
	if !ok {
		return true
	}
	return traceIDFilterSurvivesStrip(getMergedLabelMapping(ctx, accountId, source), filter)
}

// traceIDFilterSurvivesStrip reports whether a trace_id condition survives the
// source's ignored-key strip. Ignored keys are declared in provider space and
// stripped AFTER label-mapping runs (see FetchLogs), so compare against the
// post-mapping name: a tenant-level mapping of trace_id to a filterable provider
// field means the mapped condition survives, overriding the source's default ignore.
func traceIDFilterSurvivesStrip(mapping map[string]string, filter QueryRequestKeyFilter) bool {
	mappedKey := "trace_id"
	if mapping["trace_id"] != "" {
		mappedKey = mapping["trace_id"]
	}
	for _, key := range filter.GetIgnoredQueryRequestKeys() {
		if key == mappedKey {
			return false
		}
	}
	return true
}

// autoExecuteByWorkload queries logs using the event's workload name and
// namespace. If the configured log source returns no results, falls back to
// relay kubectl logs.
func (a *observabilityLogAction) autoExecuteByWorkload(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	workloadName := getEventWorkload(ctx.GetEvent())
	namespace := getEventNamespace(ctx.GetEvent())
	where := buildWorkloadLogWhereClause(workloadName, namespace)
	// A pod event reads that pod's logs, not its owner's: see eventPodSubject.
	if pod := eventPodSubject(ctx.GetEvent()); pod != "" {
		workloadName = pod
		where = buildPodLogWhereClause(pod, namespace)
	}

	// Anchor on the event window (floored/capped by resolveLogQueryWindow) rather than a
	// fixed lookback from now, so logs for an event that ended hours ago still line up
	// with it. The floor is what makes this safe: an event-anchored window used to
	// collapse to a couple of seconds for a just-fired alert.
	startTime, endTime := resolveLogQueryWindow(ctx.GetEvent(), 0)

	// Try configured log source with workload-based query
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	logResult, truncation, err := fetchCardLogs(requestCtx, FetchLogRequest{
		AccountId: ctx.GetAccountId(),
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     1000,
		QueryRequest: LogsQueryBuilderRequest{
			Where: where,
		},
	})

	if err != nil {
		ctx.GetLogger().Info("observability: log source query failed, falling back to relay", "error", err)
	}
	logoutput := logResult.Logs

	if len(logoutput) > 0 {
		ctx.GetLogger().Info("observability: logs auto action found results via configured source",
			"workload", workloadName, "namespace", namespace, "count", len(logoutput))
		return a.buildLogResponse(logoutput, map[string]any{
			"workload_name": workloadName,
			"namespace":     namespace,
			"source":        "configured_integration",
		}, putLogTruncation(addExecutedQueryInfo(map[string]any{}, logResult), truncation))
	}

	// Skip relay fallback for cloud event sources — these accounts have no K8s agent
	if isCloudEventSource(ctx.GetEvent().Source) {
		ctx.GetLogger().Info("observability: skipping relay fallback for cloud event source",
			"source", ctx.GetEvent().Source, "workload", workloadName)
		return nil, nil
	}

	// Skip relay fallback when the subject is not a Kubernetes object (CI jobs, pull
	// requests, and anything else whose "namespace"/"workload" are borrowed fields).
	if !isK8sLogTarget(workloadName, namespace) {
		ctx.GetLogger().Info("observability: skipping relay fallback for non-kubernetes subject",
			"source", ctx.GetEvent().Source, "workload", workloadName, "namespace", namespace)
		return nil, nil
	}

	// Fallback: relay kubectl logs
	ctx.GetLogger().Info("observability: falling back to relay kubectl logs",
		"workload", workloadName, "namespace", namespace)
	return a.fetchLogsViaRelay(ctx, workloadName, namespace)
}

// fetchLogsViaRelay fetches logs via relay. For workload kinds (Deployment, DaemonSet,
// StatefulSet, ReplicaSet) it uses kubectl logs since logs_enricher expects a pod name.
func (a *observabilityLogAction) fetchLogsViaRelay(ctx playbooks.PlaybookActionContext, workloadName, namespace string) (playbooks.PlaybookActionResponse, error) {
	// The kind has to describe workloadName, not the event's subject. For a pod
	// event with SubjectOwner set, getEventWorkload already resolved the name up
	// to the owning workload ("llm-server", not "llm-server-8b5f-tc2jd"), so the
	// matching kind is SubjectOwnerKind. Reading SubjectType here instead pairs a
	// deployment name with kind "pod".
	kind := relayLogKind(ctx.GetEvent(), workloadName)

	if kubectlLogKinds[strings.ToLower(kind)] {
		resp, err := a.fetchLogsViaKubectl(ctx, kind, workloadName, namespace)
		if err == nil {
			return resp, nil
		}
		// kubectl could not resolve the subject (RBAC, a kind this cluster
		// shapes differently). logs_enricher may still answer for a pod name.
		ctx.GetLogger().Info("observability: kubectl logs failed, falling back to logs_enricher",
			"kind", kind, "workload", workloadName, "namespace", namespace, "error", err)
	}

	return a.fetchLogsViaLogsEnricher(ctx, workloadName, namespace)
}

// relayLogKind is the kind that describes name for `kubectl logs <kind>/<name>`.
func relayLogKind(event playbooks.PlaybookEvent, name string) string {
	// The pod subject itself: `kubectl logs pod/<name>`. Any workload kind here
	// would read one arbitrary replica instead.
	if pod := eventPodSubject(event); pod != "" && name == pod {
		return "pod"
	}
	if event.SubjectOwner != "" && name == event.SubjectOwner && event.SubjectOwnerKind != "" {
		return event.SubjectOwnerKind
	}
	if event.Labels != nil && event.Labels["kind"] != "" {
		return event.Labels["kind"]
	}
	// Fall back to SubjectType for agent-generated events (lowercase, e.g. "deployment")
	return event.SubjectType
}

// fetchLogsViaLogsEnricher uses the relay agent's logs_enricher to fetch logs for a pod.
func (a *observabilityLogAction) fetchLogsViaLogsEnricher(ctx playbooks.PlaybookActionContext, workloadName, namespace string) (playbooks.PlaybookActionResponse, error) {
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "logs_enricher",
			ActionParams: map[string]any{
				"name":      workloadName,
				"namespace": namespace,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	relayResponse, additionalInfo, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}
	raw, ok := relayResponse["data"].(string)
	if !ok {
		return nil, errors.New("relay: unable to extract log data from response")
	}
	data, err := playbooks.DecodeAgentPayload(raw)
	if err != nil {
		return nil, fmt.Errorf("relay: unable to decode logs_enricher payload: %w", err)
	}
	logs := parseLogTextToOutputLogs(data)
	if len(logs) == 0 {
		// The agent answered with an empty (gzipped) log — the pod wrote nothing in
		// range. Same rule as buildLogsActionResponse: no logs, no card.
		ctx.GetLogger().Info("observability: logs_enricher returned no log content",
			"workload", workloadName, "namespace", namespace)
		return nil, nil
	}
	filename, _ := relayResponse["filename"].(string)
	insight := playbooks.InsightFromRelayResponse(relayResponse)
	if summary := playbooks.ComputeLogSummary(outputLogsToInsightMaps(logs)); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	// The agent sends no insight for a log file, so without these the card has no
	// summary line at all.
	digest := summarizeOutputLogs(logs, actionLogMaxGroupInsights)
	putLogGroups(additionalInfo, digest)
	if len(insight) == 0 {
		insight = digest.Insights
	}
	if filename != "" {
		additionalInfo["filename"] = filename
	}
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query": map[string]any{
			"workload_name": workloadName,
			"namespace":     namespace,
			"source":        "logs_enricher",
		},
	}
	// Return structured log entries (not a raw-text file) so the evidence renders in the UI
	// log card and is consumed by the log-analysis pipeline. See parseLogTextToOutputLogs.
	return playbooks.NewPlaybookActionResponseJson(
		map[string]any{"data": logs}, additionalInfo, insight, metadata), nil
}

// kubectlLogKinds are the kinds `kubectl logs <kind>/<name>` resolves. Doubles as the
// allowlist for the only value interpolated into the kubectl command that isK8sLogTarget
// does not already screen — see fetchLogsViaKubectl.
//
// Pods and Jobs are here even though logs_enricher can also answer for them, because
// this path bounds the read and that one does not. logs_enricher takes no time argument
// — podLogParams has declared since_time/tail_lines since it was written and never sent
// them, and nothing in the agent contract accepts them — so it returns the container's
// whole lifetime up to the agent's own tail. Measured over a week on dev, that is a
// median 4.8 days of log per evidence, 42 of 53 payloads truncated at the line cap, and
// 19 of 53 with no parseable timestamp at all. The kubectl path asks for
// --since-time=<event window> --timestamps and lands at a 570s median.
var kubectlLogKinds = map[string]bool{
	"deployment":  true,
	"daemonset":   true,
	"statefulset": true,
	"replicaset":  true,
	"pod":         true,
	"job":         true,
}

// fetchLogsViaKubectl uses kubectl logs <kind>/<name> via kubectl_command_executor
// for workload kinds where logs_enricher cannot resolve the resource.
func (a *observabilityLogAction) fetchLogsViaKubectl(ctx playbooks.PlaybookActionContext, kind, workloadName, namespace string) (playbooks.PlaybookActionResponse, error) {
	// kind reaches here from event labels / SubjectType, i.e. from a webhook payload.
	// The sole caller already screens it, but the command is assembled here, so the
	// check belongs here too: with this, every value interpolated below is either from
	// this fixed set, screened by isK8sLogTarget, or a timestamp we formatted ourselves.
	kind = strings.ToLower(kind)
	if !kubectlLogKinds[kind] {
		return nil, fmt.Errorf("relay: kubectl logs does not support kind %q", kind)
	}

	// --since-time bounds the output to the event's window. Without it `--tail=1000`
	// returns whatever the container last wrote, however long ago: an event raised today
	// was being enriched with the four lines a quiet pod emitted twelve days earlier, and
	// nothing in the evidence said so. --timestamps is what makes that visible — it is
	// the only way lines from this path carry a time at all (see parseLogTextToOutputLogs).
	startTime, _ := resolveLogQueryWindow(ctx.GetEvent(), 0)
	sinceTime := time.UnixMilli(startTime).UTC().Format(time.RFC3339)

	command := fmt.Sprintf("kubectl logs %s/%s -n %s --tail=1000 --all-containers=true --timestamps=true --since-time=%s",
		kind, workloadName, namespace, sinceTime)

	ctx.GetLogger().Info("observability: fetching logs via kubectl", "command", command)

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": command,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	relayResponse, additionalInfo, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}
	// kubectl_command_executor on the agent (robusta/playbooks/nudgebee_playbooks/kubectl_actions.py)
	// returns its output wrapped in a JsonBlock, so stdout/stderr live one level deep inside
	// relayResponse["data"] (a JSON-encoded string), not at the top level.
	stdout, stderr, exitCode, haveExitCode := extractKubectlOutput(relayResponse)
	if kubectlLogsFailed(stdout, stderr, exitCode, haveExitCode) {
		ctx.GetLogger().Error("relay: kubectl logs failed",
			"exit_code", exitCode, "have_exit_code", haveExitCode, "stderr", stderr, "response", slog.AnyValue(relayResponse))
		return nil, fmt.Errorf("relay: kubectl logs failed (exit %d): %s", exitCode, stderr)
	}
	if stdout == "" {
		// With --since-time, "nothing" is an ordinary answer: the workload was simply
		// quiet during the event window. Reporting the quiet case as an error is what
		// used to push callers into treating the twelve-day-old tail as the better
		// answer.
		//
		// Non-empty stderr is NOT that signal. `kubectl logs deployment/x` writes an
		// informational "Found 21 pods, using pod/x-z5fq5" to stderr whenever the
		// selector matches more than one pod, and exits 0. Every multi-replica
		// workload therefore looked like a failure whenever the pod kubectl happened
		// to pick was quiet in the window — observed on dev for
		// victoria-prometheus-node-exporter, arc-nudgebee-vx286-runner and
		// services-server, four times in twenty minutes. Trust the exit code, which
		// the agent reports, and keep the stderr heuristic only for responses that
		// carry no exit code at all.
		ctx.GetLogger().Info("observability: kubectl logs returned nothing in the event window",
			"workload", workloadName, "namespace", namespace, "since", sinceTime)
		return nil, nil
	}
	logs := parseLogTextToOutputLogs(stdout)
	if len(logs) == 0 {
		ctx.GetLogger().Info("observability: kubectl logs produced no parseable entries",
			"workload", workloadName, "namespace", namespace)
		return nil, nil
	}
	insight := playbooks.InsightFromRelayResponse(relayResponse)
	if summary := playbooks.ComputeLogSummary(outputLogsToInsightMaps(logs)); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	// The agent sends no insight for a log file, so without these the card has no
	// summary line at all.
	digest := summarizeOutputLogs(logs, actionLogMaxGroupInsights)
	putLogGroups(additionalInfo, digest)
	if len(insight) == 0 {
		insight = digest.Insights
	}
	additionalInfo["filename"] = fmt.Sprintf("%s-%s.log", workloadName, namespace)
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query": map[string]any{
			"workload_name": workloadName,
			"namespace":     namespace,
			"source":        "kubectl",
		},
	}
	// Return structured log entries (not a raw-text file) so the evidence renders in the UI
	// log card and is consumed by the log-analysis pipeline. See parseLogTextToOutputLogs.
	return playbooks.NewPlaybookActionResponseJson(
		map[string]any{"data": logs}, additionalInfo, insight, metadata), nil
}

// kubectlLogsFailed reports whether a kubectl_command_executor result is an
// actual failure rather than a quiet window.
//
// The exit code is authoritative when the agent sends one. Non-empty stderr is
// not: `kubectl logs deployment/x` writes "Found 21 pods, using pod/x-z5fq5" to
// stderr whenever the selector matches more than one pod, and still exits 0. So
// every multi-replica workload read as a failure whenever the pod kubectl picked
// happened to be quiet in the window, which pushed the caller onto the unbounded
// logs_enricher fallback — observed on dev for victoria-prometheus-node-exporter,
// arc-nudgebee-vx286-runner and services-server, four times in twenty minutes.
//
// Responses that carry no exit code keep the old stderr heuristic, which is the
// best signal available for them.
func kubectlLogsFailed(stdout, stderr string, exitCode int, haveExitCode bool) bool {
	if haveExitCode {
		return exitCode != 0
	}
	return stdout == "" && strings.TrimSpace(stderr) != ""
}

// extractKubectlOutput unwraps the kubectl_command_executor response to recover stdout/stderr.
//
// kubectl_command_executor in robusta publishes its output via add_enrichment([JsonBlock(json.dumps({...}))]),
// which ModelConversion.to_evidence_json serializes as {"type":"json","data":"<json_string>"}.
// After relay.ExecuteAndExtractResponse unwraps the outer envelope, the agent action response
// retains this JsonBlock shape, so stdout/stderr live inside the stringified "data" field rather
// than at the top level. This helper handles that indirection.
func extractKubectlOutput(relayResponse map[string]any) (stdout, stderr string, exitCode int, haveExitCode bool) {
	// First try the fast-path: some agent actions (future or alternate shapes) may put
	// stdout/stderr at the top level already.
	if s, ok := relayResponse["stdout"].(string); ok && s != "" {
		stdout = s
	}
	if s, ok := relayResponse["stderr"].(string); ok {
		stderr = s
	}
	if stdout != "" {
		return stdout, stderr, 0, false
	}

	// Fallback: unwrap JsonBlock payload — relayResponse["data"] is a JSON-encoded string
	// containing {"command":..., "stdout":..., "stderr":...}.
	dataStr, ok := relayResponse["data"].(string)
	if !ok || dataStr == "" {
		return stdout, stderr, 0, false
	}
	var inner kubectlJsonBlockPayload
	if err := json.Unmarshal([]byte(dataStr), &inner); err != nil {
		return stdout, stderr, 0, false
	}
	if inner.ExitCode != nil {
		return inner.Stdout, inner.Stderr, *inner.ExitCode, true
	}
	return inner.Stdout, inner.Stderr, 0, false
}

// kubectlJsonBlockPayload is the payload robusta's kubectl_command_executor writes
// inside the JsonBlock data field — see robusta-ai/playbooks sdk: add_enrichment(
// [JsonBlock(json.dumps({"command": ..., "stdout": ..., "stderr": ...}))]).
type kubectlJsonBlockPayload struct {
	Command  string `json:"command"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode *int   `json:"exit_code"`
}

// buildLogResponse creates a standard log action response from OutputLog results
// (configured-source workload auto path — no caller-supplied label extractors).
func (a *observabilityLogAction) buildLogResponse(logoutput []OutputLog, queryInfo, additionalInfo map[string]any) (playbooks.PlaybookActionResponse, error) {
	return logsResponseOrNil(buildLogsActionResponse(logoutput, "", additionalInfo, actionLogMaxInsightErrors, queryInfo, nil, nil))
}

// buildWorkloadLogWhereClause creates a where clause to filter logs by workload name and namespace.
// Uses canonical keys "app" and "namespace" so that getMergedLabelMapping can translate them
// to provider-specific field names (e.g. via the UI's Log Label Mapper: app → deployment.keyword).
//
// Note that a source's static GetLabelMapping() does not necessarily cover "app" — Loki's
// is only {content: log} — so on those backends this clause matches solely when the
// promtail/alloy scrape happens to emit an `app` label equal to the workload name. It is
// the last resort before the agent: sources that can do better implement
// PlaybookQueryGenerator (LokiSource matches on `pod=~"<workload>-.*"`), which AutoExecute
// tries first.
func buildWorkloadLogWhereClause(workloadName, namespace string) query.QueryWhereClause {
	return query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"app": {query.Eq: workloadName}}},
			{Binary: query.BinaryWhereClause{"namespace": {query.Eq: namespace}}},
		},
	}
}

// buildPodLogWhereClause is buildWorkloadLogWhereClause for a single pod. "pod" is
// a canonical key every source that maps "app" also maps (Elasticsearch expands it
// across shipper field names), so it reaches at least as many backends.
func buildPodLogWhereClause(podName, namespace string) query.QueryWhereClause {
	return query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"pod": {query.Eq: podName}}},
			{Binary: query.BinaryWhereClause{"namespace": {query.Eq: namespace}}},
		},
	}
}

// isCloudEventSource returns true for event sources that originate from cloud providers
// (not K8s). These events should not fall back to relay kubectl logs since their accounts
// have no K8s agent connected.
func isCloudEventSource(source string) bool {
	switch source {
	case "Azure_Monitor_Alert", "azure_monitor_webhook",
		"AWS_CloudWatch_Alarm", "AWS_EventBridge",
		"GCP_Metric_Alert", "gcp_monitoring_webhook":
		return true
	}
	return false
}

func (a *observabilityLogAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params observabilityLogActionParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	startTime, endTime := resolveLogQueryWindow(ctx.GetEvent(), params.Duration)

	logResult, truncation, err := fetchCardLogs(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), FetchLogRequest{
		AccountId: params.AccountId,
		Query:     params.Query,
		Request:   params.QueryOptions,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     1000,
		Offset:    0,
	})
	if err != nil {
		return nil, err
	}

	return logsResponseOrNil(buildLogsActionResponse(logResult.Logs, "", putLogTruncation(addExecutedQueryInfo(map[string]any{}, logResult), truncation), actionLogMaxInsightErrors, rawParams, params.RegexExtractors, params.LabelExtractors))
}

// Datadog Logs Action
type datadogLogsAction struct{}

type datadogLogsActionParams struct {
	LogQuery  string `json:"log_query,omitempty"`
	LogFromTs int64  `json:"log_from_ts,omitempty"`
	LogToTs   int64  `json:"log_to_ts,omitempty"`
	LogURL    string `json:"log_url,omitempty"`
}

type DatadogLog struct {
	Timestamp  string                 `json:"timestamp"`
	Attributes map[string]interface{} `json:"attributes"`
}

func (a *datadogLogsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	if labels == nil {
		return false
	}

	// Check if we have log URL or log query parameters
	if labels["log_url"] != "" || labels["log_query"] != "" {
		return true
	}

	return false
}

func (a *datadogLogsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	params := map[string]any{}

	if logURL := labels["log_url"]; logURL != "" {
		params["log_url"] = logURL
	}

	if logQuery := labels["log_query"]; logQuery != "" {
		params["log_query"] = logQuery
	}

	// Prefer log time range from labels (extracted from Datadog payload during webhook ingestion)
	if fromTs := labels["log_from_ts"]; fromTs != "" {
		if v, err := strconv.ParseInt(fromTs, 10, 64); err == nil && v > 0 {
			params["log_from_ts"] = v
		}
	}
	if toTs := labels["log_to_ts"]; toTs != "" {
		if v, err := strconv.ParseInt(toTs, 10, 64); err == nil && v > 0 {
			params["log_to_ts"] = v
		}
	}

	// Fallback to event timestamps if no label timestamps
	if params["log_from_ts"] == nil || params["log_to_ts"] == nil {
		if ctx.GetEvent().StartedAt != nil && ctx.GetEvent().EndedAt != nil {
			params["log_from_ts"] = ctx.GetEvent().StartedAt.Unix()
			params["log_to_ts"] = ctx.GetEvent().EndedAt.Unix()
		}
	}

	return a.Execute(ctx, params)
}

func (a *datadogLogsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params datadogLogsActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	sc := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	// Parse log URL if provided
	if params.LogURL != "" && params.LogQuery == "" {
		log, err := url.Parse(params.LogURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse datadog log url: %w", err)
		}

		logQueryParams := log.Query()
		if query := logQueryParams.Get("query"); query != "" {
			params.LogQuery = query
		}

		if fromTs := logQueryParams.Get("from_ts"); fromTs != "" {
			if fromInt, err := strconv.Atoi(fromTs); err == nil {
				params.LogFromTs = int64(fromInt)
			}
		}

		if toTs := logQueryParams.Get("to_ts"); toTs != "" {
			if toInt, err := strconv.Atoi(toTs); err == nil {
				params.LogToTs = int64(toInt)
			}
		}
	}

	// Build a default log query from event context when log_url had no query param
	if params.LogQuery == "" {
		labels := ctx.GetEvent().Labels
		if labels != nil {
			var queryParts []string
			for _, key := range []string{"service", "host", "env"} {
				if v, ok := labels[key]; ok && v != "" {
					queryParts = append(queryParts, fmt.Sprintf("%s:%s", key, escapeDatadogTagValue(v)))
				}
			}
			if len(queryParts) > 0 {
				params.LogQuery = strings.Join(queryParts, " ")
			}
		}
	}

	// Set default time range if not provided
	if params.LogFromTs == 0 || params.LogToTs == 0 {
		now := time.Now()
		params.LogToTs = now.Unix()
		params.LogFromTs = now.Add(-10 * time.Minute).Unix()
	} else if params.LogFromTs == params.LogToTs {
		// Equal timestamps (e.g., newly triggered alert where starts_at == ends_at):
		// use a 10-minute window centered on the timestamp
		params.LogFromTs = params.LogFromTs - 300
		params.LogToTs = params.LogToTs + 300
	}

	if params.LogQuery == "" {
		return nil, errors.New("log_query or log_url is required")
	}

	// Route through the canonical FetchLogs. Datadog webhook events carry a native
	// query string (from log_url / service:host:env labels), so pass it as Query.
	// Pin the provider to datadog/user: the account's default logs provider may not
	// be Datadog, but this evidence must still resolve the DatadogSource.
	logResult, err := FetchLogs(sc, FetchLogRequest{
		AccountId:         ctx.GetAccountId(),
		Query:             params.LogQuery,
		StartTime:         params.LogFromTs,
		EndTime:           params.LogToTs,
		Limit:             1000,
		LogProvider:       "datadog",
		LogProviderSource: "user",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog logs: %w", err)
	}

	// Skip empty evidence — no value in adding a "Datadog Logs" card with no data
	if len(logResult.Logs) == 0 {
		return nil, nil
	}

	title := "Datadog Logs"
	if t, ok := rawParams["title"].(string); ok && t != "" {
		title = t
	}

	// action_name "cloud_logs" is preserved — logActions mutual-exclusion,
	// agentToServerActionMap dedup, and retained-evidence all key on it.
	return logsResponseOrNil(buildLogsActionResponse(logResult.Logs, title, addExecutedQueryInfo(map[string]any{"action_name": "cloud_logs"}, logResult), 2, rawParams, nil, nil))
}
