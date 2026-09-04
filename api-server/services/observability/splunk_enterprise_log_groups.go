package observability

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// This file makes SplunkEnterpriseLogSource satisfy LogGroupSource, which is what the
// "Log Groups" tab resolves through. Without it getLogGroupSource falls through to its
// metrics-provider fallback, that lookup fails too (Splunk Enterprise is the metrics
// provider as well), and the tab renders "Log Grouping not supported" — SupportsLogGroups
// is derived from this interface assertion, not from a static table.

// splunkEnterpriseLogGroupLimit caps how many error patterns the aggregation returns,
// matching the other providers' page size.
const splunkEnterpriseLogGroupLimit = 100

// splunkEnterpriseLogGroupTimeout is deliberately longer than a plain log search: this
// query scans the whole window rather than stopping at the first N events, so it is the
// most expensive read this integration issues.
const splunkEnterpriseLogGroupTimeout = 90 * time.Second

// splunkEnterpriseLogGroupFields lists, per logical role, the Splunk field names checked
// in priority order. The first NON-NULL one wins per event, resolved by SPL's coalesce.
//
// Resolving inside the query rather than probing the index schema first is a deliberate
// choice, and the reason is a Splunk behaviour with no SQL analogue: `stats ... BY f`
// silently drops every event where f is null. Verified against Splunk 10.4.2 — adding
// `k8s.container.name` (absent from that index) to an otherwise working BY clause turned
// 310 matching events into zero rows, with no warning in the response envelope. A missing
// field is therefore not a degraded grouping here, it is an empty tab. Every coalesce
// chain below ends in a literal "" so the grouped field can never be null, which makes
// the aggregation correct on any index regardless of which shipper filled it.
var splunkEnterpriseLogGroupFields = struct {
	Message        []string
	Namespace      []string
	Pod            []string
	Container      []string
	Workload       []string
	Severity       []string
	SeverityNumber []string
}{
	// _raw is the last resort rather than the first choice: it is the entire event, so
	// on structured HEC data it would fold the k8s metadata into the pattern hash and
	// split one recurring error into one group per pod.
	Message:   []string{"body", "message", "log", "_raw"},
	Namespace: []string{"k8s.namespace.name", "kubernetes.namespace_name", "namespace"},
	Pod:       []string{"k8s.pod.name", "kubernetes.pod_name", "pod"},
	Container: []string{"k8s.container.name", "kubernetes.container_name", "container"},
	Workload:  []string{"k8s.deployment.name", "kubernetes.deployment_name", "deployment"},
	Severity:  []string{"severity_text", "severity", "level", "log_level"},
	// Only the OTel-specified name, for the same reason OpenObserve accepts only it: a
	// bare numeric "level" is ambiguous, and syslog counts down (3 = err) where OTel
	// counts up (17 = ERROR), so guessing wrong reports INFO records as errors.
	SeverityNumber: []string{"severity_number"},
}

// The eval aliases the aggregation groups by. Prefixed so they cannot collide with a
// real field the shipper happened to emit.
const (
	splunkLogGroupMessageCol   = "nb_lg_message"
	splunkLogGroupNamespaceCol = "nb_lg_namespace"
	splunkLogGroupPodCol       = "nb_lg_pod"
	splunkLogGroupContainerCol = "nb_lg_container"
	splunkLogGroupWorkloadCol  = "nb_lg_workload"
	splunkLogGroupLevelCol     = "nb_lg_level"
	splunkLogGroupLevelNumCol  = "nb_lg_levelnum"
	splunkLogGroupCountCol     = "nb_lg_count"
	splunkLogGroupLastTimeCol  = "nb_lg_last_time"
)

// OTel severity-number floors for ERROR and FATAL (spec: TRACE 1-4, DEBUG 5-8,
// INFO 9-12, WARN 13-16, ERROR 17-20, FATAL 21-24).
const (
	splunkEnterpriseOtelErrorSeverityNumber = 17
	splunkEnterpriseOtelFatalSeverityNumber = 21
)

// splunkEnterpriseErrorSeverities are the lower-cased severity values treated as errors.
// Same set as the other providers, so a tenant switching backends sees the same groups.
var splunkEnterpriseErrorSeverities = []string{"error", "critical", "fatal", "err", "crit"}

// splunkEnterpriseErrorMessagePatterns are matched against the message when an event
// carries no severity at all. Upper-case on purpose: a case-insensitive "error" match
// hits ordinary prose ("no errors found") far too often.
var splunkEnterpriseErrorMessagePatterns = []string{"ERROR", "FATAL", "CRITICAL"}

// splunkEnterpriseExcludedContainers are infrastructure containers whose logs are noise
// in the error-pattern view.
var splunkEnterpriseExcludedContainers = []string{"prometheus", "grafana", "nudgebee-agent"}

// splunkEnterpriseCoalesce renders `coalesce('a', 'b', <fallback>)` over the candidate
// fields. Field names are single-quoted because the OTel spellings carry dots, which eval
// does not read as a field reference when bare.
//
// fallback is always emitted, and for the grouped fields it must be a non-null literal;
// see the comment on splunkEnterpriseLogGroupFields for why that is load-bearing.
func splunkEnterpriseCoalesce(candidates []string, fallback string) (string, error) {
	parts := make([]string, 0, len(candidates)+1)
	for _, name := range candidates {
		if !isSafeSplunkFieldName(name) {
			return "", fmt.Errorf("invalid or unsafe field name: %q", name)
		}
		parts = append(parts, "'"+name+"'")
	}
	parts = append(parts, fallback)
	return "coalesce(" + strings.Join(parts, ", ") + ")", nil
}

// splunkEnterpriseNumericCoalesce is the numeric counterpart, wrapping each candidate in
// tonumber() so the result can be compared and aggregated arithmetically. Splunk returns
// HEC fields as strings, and max() over a mix of "21" and a numeric sentinel compares
// lexicographically rather than numerically.
func splunkEnterpriseNumericCoalesce(candidates []string, fallback string) (string, error) {
	parts := make([]string, 0, len(candidates)+1)
	for _, name := range candidates {
		if !isSafeSplunkFieldName(name) {
			return "", fmt.Errorf("invalid or unsafe field name: %q", name)
		}
		parts = append(parts, "tonumber('"+name+"')")
	}
	parts = append(parts, fallback)
	return "coalesce(" + strings.Join(parts, ", ") + ")", nil
}

// splunkEnterpriseQuoteLiteralList renders an SPL list of escaped double-quoted literals.
func splunkEnterpriseQuoteLiteralList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = `"` + escapeSplunkString(v) + `"`
	}
	return strings.Join(quoted, ", ")
}

// buildSplunkEnterpriseLogGroupSPL emits the aggregation that backs the Log Groups tab.
//
// The whole aggregation is pushed down to Splunk rather than grouped in Go: generatePatternHash
// hashes the message, so client-side grouping would need every raw event fetched first and
// would give an identical answer at many times the cost. max(_time) gives each group its
// own "Last Time"; without it every row renders at the window edge.
func buildSplunkEnterpriseLogGroupSPL(index, selectedNamespace, selectedWorkload string, limit int) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}
	if limit <= 0 || limit > splunkEnterpriseLogGroupLimit {
		limit = splunkEnterpriseLogGroupLimit
	}

	evals := []struct {
		alias      string
		candidates []string
	}{
		{splunkLogGroupMessageCol, splunkEnterpriseLogGroupFields.Message},
		{splunkLogGroupNamespaceCol, splunkEnterpriseLogGroupFields.Namespace},
		{splunkLogGroupPodCol, splunkEnterpriseLogGroupFields.Pod},
		{splunkLogGroupContainerCol, splunkEnterpriseLogGroupFields.Container},
		{splunkLogGroupWorkloadCol, splunkEnterpriseLogGroupFields.Workload},
		{splunkLogGroupLevelCol, splunkEnterpriseLogGroupFields.Severity},
	}
	assignments := make([]string, 0, len(evals)+1)
	for _, e := range evals {
		expr, err := splunkEnterpriseCoalesce(e.candidates, `""`)
		if err != nil {
			return "", err
		}
		assignments = append(assignments, fmt.Sprintf("%s=%s", e.alias, expr))
	}

	// The severity number is not grouped on — it is 1:1 with the severity text, so adding
	// it to the BY clause would only risk splitting a group — but it still has to survive
	// the aggregation, or the FATAL-vs-ERROR branch in the converter is unreachable.
	// stats discards every field that is neither a BY key nor an aggregate; verified
	// against Splunk 10.4.2, where an earlier revision of this query computed the field
	// and then silently dropped it from all 100 returned rows.
	levelNumExpr, err := splunkEnterpriseNumericCoalesce(splunkEnterpriseLogGroupFields.SeverityNumber, "-1")
	if err != nil {
		return "", err
	}
	assignments = append(assignments, fmt.Sprintf("%s=%s", splunkLogGroupLevelNumCol, levelNumExpr))

	// What counts as an error when the string severity is absent: the numeric OTel
	// severity if the event carries one, otherwise the message content.
	messageMatches := make([]string, 0, len(splunkEnterpriseErrorMessagePatterns))
	for _, p := range splunkEnterpriseErrorMessagePatterns {
		// match() is a case-sensitive regex; the patterns are fixed upper-case literals
		// with no metacharacters, so they need no escaping.
		messageMatches = append(messageMatches,
			fmt.Sprintf(`match(%s, "%s")`, splunkLogGroupMessageCol, p))
	}
	fallbackErrorFilter := fmt.Sprintf(`(%s) OR (%s>=%d)`,
		strings.Join(messageMatches, " OR "),
		splunkLogGroupLevelNumCol, splunkEnterpriseOtelErrorSeverityNumber)

	errorFilter := fmt.Sprintf(`lower(%s) IN (%s) OR (%s="" AND (%s))`,
		splunkLogGroupLevelCol, splunkEnterpriseQuoteLiteralList(splunkEnterpriseErrorSeverities),
		splunkLogGroupLevelCol, fallbackErrorFilter)

	conditions := []string{
		fmt.Sprintf(`%s!=""`, splunkLogGroupMessageCol),
		"(" + errorFilter + ")",
		fmt.Sprintf("NOT %s IN (%s)",
			splunkLogGroupContainerCol, splunkEnterpriseQuoteLiteralList(splunkEnterpriseExcludedContainers)),
	}

	if selectedNamespace != "" {
		conditions = append(conditions, fmt.Sprintf(`%s="%s"`,
			splunkLogGroupNamespaceCol, escapeSplunkString(selectedNamespace)))
	}
	if selectedWorkload != "" {
		// Pods are named {workload}-{suffix}. like() takes SQL wildcards, so '%' is the
		// wildcard here and escapeSplunkString's '*' handling does not interfere. The
		// workload field is checked too, for pods a shipper labelled but didn't name.
		escaped := escapeSplunkString(selectedWorkload)
		conditions = append(conditions, fmt.Sprintf(
			`(like(%s, "%s-%%") OR %s="%s")`,
			splunkLogGroupPodCol, escaped, splunkLogGroupWorkloadCol, escaped))
	}

	groupCols := strings.Join([]string{
		splunkLogGroupMessageCol,
		splunkLogGroupNamespaceCol,
		splunkLogGroupPodCol,
		splunkLogGroupWorkloadCol,
		splunkLogGroupContainerCol,
		splunkLogGroupLevelCol,
	}, ", ")

	return fmt.Sprintf(
		`search index="%s" | eval %s | where %s | stats count AS %s, max(_time) AS %s, max(%s) AS %s BY %s | sort - %s | head %d`,
		index,
		strings.Join(assignments, ", "),
		strings.Join(conditions, " AND "),
		splunkLogGroupCountCol,
		splunkLogGroupLastTimeCol,
		splunkLogGroupLevelNumCol, splunkLogGroupLevelNumCol,
		groupCols,
		splunkLogGroupCountCol,
		limit,
	), nil
}

// splunkEnterpriseNumber coerces a decoded Splunk cell to float64. Splunk returns every
// field as a JSON string and the decoder runs with UseNumber, so both shapes appear.
func splunkEnterpriseNumber(v any) (float64, bool) {
	s := formatSplunkValue(v)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// convertSplunkEnterpriseLogGroups maps aggregated rows onto the LogGroup contract shared
// by every provider. Timestamps are epoch SECONDS — the frontend multiplies by 1000 — and
// max(_time) already yields epoch seconds inside a stats context.
func convertSplunkEnterpriseLogGroups(rows []map[string]any, fallbackTimestampSec int64) LogGroupOutput {
	groups := make([]LogGroup, 0, len(rows))

	for _, row := range rows {
		count, ok := splunkEnterpriseNumber(row[splunkLogGroupCountCol])
		if !ok {
			continue
		}
		sample := formatSplunkValue(row[splunkLogGroupMessageCol])
		if sample == "" {
			continue
		}

		level := formatSplunkValue(row[splunkLogGroupLevelCol])
		if level == "" {
			// Rows are already filtered to errors, so an event with no severity text is
			// an error by message content or by severity number. Keep FATAL distinct
			// rather than flattening everything to "error".
			level = "error"
			if n, ok := splunkEnterpriseNumber(row[splunkLogGroupLevelNumCol]); ok &&
				n >= splunkEnterpriseOtelFatalSeverityNumber {
				level = "fatal"
			}
		}

		timestampSec := fallbackTimestampSec
		if last, ok := splunkEnterpriseNumber(row[splunkLogGroupLastTimeCol]); ok && last > 0 {
			timestampSec = int64(last)
		}

		pod := formatSplunkValue(row[splunkLogGroupPodCol])
		workload := formatSplunkValue(row[splunkLogGroupWorkloadCol])
		if workload == "" {
			// Derived only as a fallback: the drill-down turns whatever is reported here
			// into an `app` filter, which maps to k8s.deployment.name. When the shipper
			// records one, that is the value that will actually match.
			workload = extractWorkloadFromPodName(pod)
		}

		group := LogGroup{
			Sample:      sample,
			Namespace:   formatSplunkValue(row[splunkLogGroupNamespaceCol]),
			Workload:    workload,
			Container:   formatSplunkValue(row[splunkLogGroupContainerCol]),
			Level:       level,
			Count:       int64(count),
			Timestamps:  []int64{timestampSec},
			Values:      []float64{count},
			PatternHash: generatePatternHash(sample),
		}

		// container_id mirrors the Prometheus format so the UI can parse namespace and
		// workload back out of a single field.
		if group.Namespace != "" && group.Workload != "" {
			if group.Container != "" {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s/%s", group.Namespace, group.Workload, group.Container)
			} else {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
			}
		}

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}
}

// QueryLogGroup aggregates error-log patterns in the configured log index.
func (s *SplunkEnterpriseLogSource) QueryLogGroup(
	ctx *security.RequestContext, req FetchLogGroupRequest,
) (LogGroupOutput, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SplunkEnterpriseLogSource.QueryLogGroup: failed to get config", "error", err)
		return LogGroupOutput{}, fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}

	spl, err := buildSplunkEnterpriseLogGroupSPL(
		cfg.LogIndex,
		common.GetString(req.Request, "selectedNamespace"),
		common.GetString(req.Request, "selectedWorkload"),
		splunkEnterpriseLogGroupLimit,
	)
	if err != nil {
		return LogGroupOutput{}, err
	}

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())

	ctx.GetLogger().Info("Splunk Enterprise Log Group Query", "query", spl)

	rows, err := runSplunkEnterpriseSearch(
		cfg, spl, startTime, endTime, splunkEnterpriseLogGroupLimit, splunkEnterpriseLogGroupTimeout,
	)
	if err != nil {
		ctx.GetLogger().Error("SplunkEnterpriseLogSource.QueryLogGroup: search failed", "query", spl, "error", err)
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return LogGroupOutput{}, fmt.Errorf(
				"log group query timed out — the selected time range contains too many events. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return LogGroupOutput{}, err
	}

	// Groups whose max(_time) is unreadable fall back to the end of the query window.
	fallbackSec, _ := strconv.ParseFloat(endTime, 64)
	return convertSplunkEnterpriseLogGroups(rows, int64(fallbackSec)), nil
}
