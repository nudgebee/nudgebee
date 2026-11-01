package observability

import (
	"errors"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SplunkEnterpriseMetricSource queries a Splunk Enterprise / Splunk Cloud metrics index
// with SPL's mstats command.
//
// Not to be confused with SplunkMetricSource, which targets Splunk Observability Cloud
// over SignalFlow. The two products store metrics in entirely different systems.
type SplunkEnterpriseMetricSource struct{}

// splunkEnterpriseMetricLabelMapping maps canonical label names to the dimension names
// the Splunk Distribution of the OpenTelemetry Collector writes. It mirrors
// splunkEnterpriseLogLabelMapping: resource attributes survive HEC with their dots
// intact, so it is k8s.pod.name rather than the flattened spellings other backends use.
var splunkEnterpriseMetricLabelMapping = map[string]string{
	"namespace": "k8s.namespace.name",
	"pod":       "k8s.pod.name",
	"container": "k8s.container.name",
	"node":      "k8s.node.name",
	"workload":  "k8s.deployment.name",
	"app":       "k8s.deployment.name",
	"cluster":   "k8s.cluster.name",
	"host":      "host.name",
	"service":   "service.name",
}

// splunkEnterpriseMetricGeneratingCommands are the only commands a metric query may
// begin with. Metric queries MUST start with a leading pipe — mstats and mcatalog are
// generating commands and there is no other way to read a metrics index — so the
// blanket "no leading pipe" rule that guards log queries cannot apply here. Restricting
// the opening command to these two keeps the exception as narrow as the requirement.
var splunkEnterpriseMetricGeneratingCommands = map[string]bool{
	"mstats":   true,
	"mcatalog": true,
}

// splunkEnterpriseMetricTransformCommands are the only commands allowed after the opening
// mstats/mcatalog. Each one reshapes the rows already in the pipeline and reads nothing
// else. An allowlist rather than a longer denylist, because the commands that read data
// need not name an index at all: `| tstats count from datamodel=Authentication` and
// `| from datamodel:...` take their scope from a data model defined on the server, and
// passed every index check. `search` stays allowed; its selectors are still checked.
var splunkEnterpriseMetricTransformCommands = map[string]bool{
	"where": true, "eval": true, "search": true,
	"stats": true, "eventstats": true, "streamstats": true, "timechart": true, "chart": true,
	"sort": true, "rename": true, "table": true, "fields": true,
	"head": true, "tail": true, "reverse": true, "dedup": true, "fillnull": true,
	"bin": true, "bucket": true, "top": true, "rare": true, "convert": true,
	// addinfo only attaches the search's own time bounds (info_min_time/info_max_time);
	// the utilisation queries use it to drop the still-filling last span.
	"addinfo": true,
}

// splunkEnterpriseMetricSearchTimeout bounds one metric search. Shorter than the log
// equivalent: mstats reads a purpose-built metrics index and is far cheaper than a
// full-text event search, so a slow one usually means a bad query rather than a big one.
const splunkEnterpriseMetricSearchTimeout = 30 * time.Second

// splunkEnterpriseMetricMaxRows caps rows returned by a single metric search. A time
// series is one row per (span bucket x dimension combination), so a wide BY clause over
// a long window multiplies quickly.
const splunkEnterpriseMetricMaxRows = 20000

// splunkEnterpriseCatalogLimit bounds catalog listings (metric names, dimension values).
const splunkEnterpriseCatalogLimit = 500

// splunkEnterpriseDefaultSpanSeconds is the bucket width used when the caller supplies no
// step interval. One minute matches the default scrape interval of the OTel collector.
const splunkEnterpriseDefaultSpanSeconds = 60

// splunkEnterpriseMaxBucketsPerSeries is the most buckets one series is given when the
// caller supplies no step. The UI never sends step_interval, so a fixed one-minute span
// asked a 30-day chart for 43,200 buckets per series and hit splunkEnterpriseMetricMaxRows
// weeks before the end of the window.
const splunkEnterpriseMaxBucketsPerSeries = 1000

// splunkEnterpriseSpanLadderSeconds are the widths a derived span snaps up to. Round
// widths keep bucket boundaries on the clock and the rendered SPL readable in the Code
// tab. span=auto is deliberately not used: verified on Splunk 10.4, it gave a 7-day
// window 1-day buckets, i.e. 3 points.
var splunkEnterpriseSpanLadderSeconds = []int{60, 300, 900, 1800, 3600, 10800, 21600, 43200, 86400}

// splunkEnterpriseNewestFirst is appended to every mstats search. See
// splunkEnterpriseExecutableSPL for why.
const splunkEnterpriseNewestFirst = " | sort 0 - _time"

func (s *SplunkEnterpriseMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like"}
}

// splunkEnterpriseMetricIndex resolves the configured metrics index, failing with an
// actionable message when it is unset. Empty is a legitimate configuration — a Splunk
// holding only logs — so this must read as "not configured", never as a broken query.
func splunkEnterpriseMetricIndex(cfg integrations.SplunkEnterpriseConfig) (string, error) {
	if cfg.MetricIndex == "" {
		return "", fmt.Errorf(
			"splunk enterprise metrics are not configured for this account: set %s to a metrics index (datatype=metric)",
			integrations.SplunkEnterpriseConfigMetricIndex)
	}
	if !integrations.IsSafeSplunkIndexName(cfg.MetricIndex) {
		return "", fmt.Errorf("invalid or unsafe metrics index name: %q", cfg.MetricIndex)
	}
	return cfg.MetricIndex, nil
}

// validateSplunkEnterpriseMetricQuery is the metric counterpart of
// validateSplunkEnterpriseQuery. It relaxes exactly one rule and tightens another.
//
// Relaxed: a leading pipe is required rather than forbidden, because mstats and mcatalog
// are generating commands.
//
// Tightened: because that opening command carries its own WHERE clause, the query gets
// to choose its own index — so the configured index must actually appear in it, and no
// other index may. Without that check a caller-supplied metric query could read any index
// on the server, which is precisely the escape the log guard's no-leading-pipe rule
// exists to prevent.
func validateSplunkEnterpriseMetricQuery(spl, index string) error {
	trimmed := strings.TrimSpace(spl)
	if trimmed == "" {
		return fmt.Errorf("empty splunk metric query")
	}
	if !strings.HasPrefix(trimmed, "|") {
		return fmt.Errorf("splunk metric query must start with a generating command (%s)",
			strings.Join(sortedSplunkMetricCommands(), " or "))
	}

	// The opening command is the first NON-EMPTY segment: a query starts with "|", so
	// segment 0 is always "". Checking segment 0 (the old code) never fired, and
	// `| tstats`, `| metadata` or `| eventcount` passed as an opening command.
	opening := ""
	for _, segment := range splunkEnterpriseSplitPipeline(trimmed) {
		fields := strings.Fields(strings.TrimSpace(segment))
		if len(fields) == 0 {
			continue
		}
		command := strings.ToLower(strings.TrimPrefix(fields[0], "|"))
		if splunkEnterpriseDisallowedCommands[command] {
			return fmt.Errorf("splunk command %q is not permitted: this integration may only read", command)
		}
		if opening == "" {
			opening = command
			if !splunkEnterpriseMetricGeneratingCommands[command] {
				return fmt.Errorf("splunk metric query may only begin with %s, got %q",
					strings.Join(sortedSplunkMetricCommands(), " or "), command)
			}
			continue
		}
		if !splunkEnterpriseMetricTransformCommands[command] {
			return fmt.Errorf("command %q is not permitted after %s in a metrics query", command, opening)
		}
	}

	refs, err := splunkEnterpriseIndexReferences(trimmed)
	if err != nil {
		return err
	}
	// Every index the query names, in any segment, must be the configured one. The old
	// check only required the configured index to appear SOMEWHERE, so
	// `... WHERE index="otel_metrics" | search index=_audit` passed.
	scoped := false
	for _, ref := range refs {
		// `OR NOT index="otel_metrics"` names only the configured index yet selects every
		// other one.
		if ref.negated {
			return fmt.Errorf(`a negated index selector (NOT index=...) is not permitted in a metrics query`)
		}
		if (ref.op != "=" && ref.op != "::") || ref.value != index {
			return fmt.Errorf(`splunk metric query may only read the configured metrics index (index="%s"), got index%s%s`,
				index, ref.op, ref.value)
		}
		// Matched as a quoted literal so a query naming `otel_metrics_secret` cannot pass
		// as `otel_metrics`, and as a real reference so the literal cannot hide inside a
		// string (`| eval x="index=\"otel_metrics\""`).
		if ref.op == "=" && ref.quoted {
			scoped = true
		}
	}
	if !scoped {
		return fmt.Errorf(`splunk metric query must be scoped to the configured metrics index (expected index="%s")`, index)
	}
	return nil
}

// splunkEnterpriseIndexRef is one place a query selects an index.
type splunkEnterpriseIndexRef struct {
	op      string // "=" (also for "=="), "::", "!=", "<", ">", " IN"
	value   string // empty for IN
	quoted  bool
	negated bool // preceded by NOT or !, directly or through an enclosing group
}

// splunkEnterpriseIndexReferences finds every index selector in an SPL string, outside
// quoted literals. Each spelling Splunk honours counts, verified on Splunk 10.4 against
// `search <selector> | head 1`: `index=x`, `index = x`, `index::x` and `"index"=x` all
// selected _internal. `!=`, comparisons and IN are reported too, so the caller can refuse
// them: each can name an index without spelling it. The field name is matched
// case-insensitively, which is stricter than Splunk (INDEX=x selected nothing).
//
// A quoted literal is skipped as a unit, so `k8s.pod.name="index=_audit"` is a value, not
// a selector. An unterminated quote is an error: it would hide everything after it.
//
// Three constructs are refused outright rather than parsed, because a metrics query from
// the UI or the llm tool never needs them and each lets a query read outside the scope
// the selectors above describe: a subsearch (`[search sourcetype=*]` reads the role's
// default indexes without naming one), a macro (its body is expanded on the server, out
// of sight), and a negated selector (`NOT index="otel_metrics"` names the configured
// index but selects every other). Negation is tracked through parentheses, so
// `NOT (x=1 OR index="otel_metrics")` counts.
func splunkEnterpriseIndexReferences(spl string) ([]splunkEnterpriseIndexRef, error) {
	var refs []splunkEnterpriseIndexRef
	var groups []bool // per open parenthesis: is the group negated
	inNegatedGroup := func() bool { return len(groups) > 0 && groups[len(groups)-1] }
	n := len(spl)
	for i := 0; i < n; {
		var name string
		next := i
		switch c := spl[i]; {
		case c == '[':
			return nil, fmt.Errorf("subsearches ([ ... ]) are not permitted in a metrics query")
		case c == '`':
			return nil, fmt.Errorf("search macros (`...`) are not permitted in a metrics query")
		case c == '(':
			groups = append(groups, inNegatedGroup() || splunkEnterpriseNegatedBefore(spl, i))
			i++
			continue
		case c == ')':
			if len(groups) > 0 {
				groups = groups[:len(groups)-1]
			}
			i++
			continue
		case c == '"':
			end, ok := splunkEnterpriseQuotedEnd(spl, i)
			if !ok {
				return nil, fmt.Errorf("splunk metric query has an unterminated quote")
			}
			// Backslashes dropped so an escaped spelling of the name is still caught.
			name, next = strings.ReplaceAll(spl[i+1:end-1], `\`, ""), end
		case c == '\'':
			// eval/where name a field in single quotes ('index'). Only that exact token is
			// read: search does not treat apostrophes as quotes, so pairing them up would
			// let `don't ... index=_audit ... '` hide a selector between two of them.
			if len(spl)-i >= 7 && strings.EqualFold(spl[i:i+7], "'index'") {
				name, next = "index", i+7
			} else {
				i++
				continue
			}
		case isSplunkIndexNameByte(c):
			for next < n && isSplunkIndexNameByte(spl[next]) {
				next++
			}
			name = spl[i:next]
		default:
			i++
			continue
		}
		if strings.EqualFold(name, "lookup") && splunkEnterpriseFollowedByParen(spl, next) {
			// The eval/where lookup() function reads a lookup table on the search head,
			// the same data `| inputlookup` is denied for (verified present on 10.4).
			return nil, fmt.Errorf("the lookup() function is not permitted in a metrics query")
		}
		if !strings.EqualFold(name, "index") {
			i = next
			continue
		}
		op, valueAt := splunkEnterpriseIndexOperator(spl, next)
		if op == "" {
			// `BY index`, `| fields index`: a reference to the field, not a selector.
			i = next
			continue
		}
		ref := splunkEnterpriseIndexRef{op: op, negated: inNegatedGroup() || splunkEnterpriseNegatedBefore(spl, i)}
		i = valueAt
		if op != " IN" {
			for i < n && (spl[i] == ' ' || spl[i] == '\t' || spl[i] == '\n' || spl[i] == '\r') {
				i++
			}
			if i < n && spl[i] == '"' {
				end, ok := splunkEnterpriseQuotedEnd(spl, i)
				if !ok {
					return nil, fmt.Errorf("splunk metric query has an unterminated quote")
				}
				ref.value, ref.quoted, i = spl[i+1:end-1], true, end
			} else {
				start := i
				for i < n && !strings.ContainsRune(" \t\r\n()[]|,\"", rune(spl[i])) {
					i++
				}
				ref.value = spl[start:i]
			}
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// splunkEnterpriseFollowedByParen reports whether the next non-whitespace byte at or after
// i is '(' — i.e. the token before i is called as a function.
func splunkEnterpriseFollowedByParen(spl string, i int) bool {
	for i < len(spl) && (spl[i] == ' ' || spl[i] == '\t' || spl[i] == '\n' || spl[i] == '\r') {
		i++
	}
	return i < len(spl) && spl[i] == '('
}

// splunkEnterpriseNegatedBefore reports whether the token starting at pos is negated:
// preceded, across whitespace, by `!` or by the word NOT.
func splunkEnterpriseNegatedBefore(spl string, pos int) bool {
	j := pos
	for j > 0 && (spl[j-1] == ' ' || spl[j-1] == '\t' || spl[j-1] == '\n' || spl[j-1] == '\r') {
		j--
	}
	if j > 0 && spl[j-1] == '!' {
		return true
	}
	start := j
	for start > 0 && isSplunkIndexNameByte(spl[start-1]) {
		start--
	}
	return strings.EqualFold(spl[start:j], "not")
}

// splunkEnterpriseIndexOperator reads the operator after an `index` token, skipping
// whitespace. It returns "" when none follows, and where the value starts.
func splunkEnterpriseIndexOperator(spl string, i int) (string, int) {
	n := len(spl)
	for i < n && (spl[i] == ' ' || spl[i] == '\t' || spl[i] == '\n' || spl[i] == '\r') {
		i++
	}
	rest := spl[i:]
	switch {
	case strings.HasPrefix(rest, "=="):
		return "=", i + 2
	case strings.HasPrefix(rest, "="):
		return "=", i + 1
	case strings.HasPrefix(rest, "::"):
		return "::", i + 2
	case strings.HasPrefix(rest, "!="):
		return "!=", i + 2
	case strings.HasPrefix(rest, "<"), strings.HasPrefix(rest, ">"):
		return rest[:1], i + 1
	case len(rest) > 2 && strings.EqualFold(rest[:2], "in") && strings.ContainsRune(" \t\n\r(", rune(rest[2])):
		return " IN", i + 2
	}
	return "", i
}

// splunkEnterpriseQuotedEnd returns the index just past the quote that closes the one at
// start, honouring backslash escapes as splunkEnterpriseSplitPipeline does.
func splunkEnterpriseQuotedEnd(spl string, start int) (int, bool) {
	quote := spl[start]
	for i := start + 1; i < len(spl); i++ {
		switch spl[i] {
		case '\\':
			i++
		case quote:
			return i + 1, true
		}
	}
	return 0, false
}

// isSplunkIndexNameByte reports whether b can continue a field name, so `k8s.index.name`
// or `my_index` is one token rather than a selector. ':' is excluded because `index::x`
// is itself a selector.
func isSplunkIndexNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
		b == '_' || b == '.' || b == '-'
}

func sortedSplunkMetricCommands() []string {
	out := make([]string, 0, len(splunkEnterpriseMetricGeneratingCommands))
	for c := range splunkEnterpriseMetricGeneratingCommands {
		out = append(out, "`| "+c+"`")
	}
	sort.Strings(out)
	return out
}

// escapeSplunkMetricName guards a metric name interpolated into an mstats aggregation.
// Metric names routinely carry dots (k8s.pod.cpu.usage); anything outside this set could
// close the aggregation and append a command.
func isSafeSplunkMetricName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			r != '_' && r != '.' && r != ':' && r != '-' && r != '*' {
			return false
		}
	}
	return true
}

// splunkEnterpriseAggregation maps NudgeBee's aggregate operator vocabulary onto mstats
// aggregation functions. An operator with no mstats equivalent is an error: the old
// fallback to avg answered a p95 or a rate question with an average, a plausible number
// for a question nobody asked. PromQL's `group` has no value-bearing equivalent, so it is
// one of those.
func splunkEnterpriseAggregation(op string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(op)); normalized {
	case "avg", "":
		return "avg", nil
	case "sum", "min", "max", "count", "rate", "median":
		return normalized, nil
	case "stddev":
		return "stdev", nil
	case "stdvar":
		return "var", nil
	case "p50", "p90", "p95", "p99":
		return "perc" + strings.TrimPrefix(normalized, "p"), nil
	case "latest", "last":
		return "latest", nil
	default:
		return "", fmt.Errorf("aggregate_operator %q has no Splunk mstats equivalent", op)
	}
}

// buildSplunkMetricMatcher renders one label matcher as an mstats WHERE predicate.
func buildSplunkMetricMatcher(m LabelMatcher) (string, error) {
	label := m.Label
	if mapped, ok := splunkEnterpriseMetricLabelMapping[label]; ok {
		label = mapped
	}
	if !isSafeSplunkFieldName(label) {
		return "", fmt.Errorf("invalid or unsafe dimension name: %q", m.Label)
	}
	value := escapeSplunkString(m.Value)

	switch m.Operator {
	case "", "_eq", "=", "==":
		return fmt.Sprintf(`%s="%s"`, label, value), nil
	case "_neq", "!=":
		return fmt.Sprintf(`%s!="%s"`, label, value), nil
	case "_prefix":
		// Appends the trailing wildcard. Used for pod-name prefixes, where the
		// replica-hash suffix is unknown.
		return fmt.Sprintf(`%s="%s*"`, label, value), nil
	case "_in":
		// Value is a comma-separated list. Only used for Kubernetes object names, which
		// cannot contain commas (RFC 1123), so the separator is unambiguous there.
		parts := strings.Split(m.Value, ",")
		quoted := make([]string, 0, len(parts))
		for _, p := range parts {
			quoted = append(quoted, fmt.Sprintf(`"%s"`, escapeSplunkString(p)))
		}
		return fmt.Sprintf(`%s IN (%s)`, label, strings.Join(quoted, ", ")), nil
	case "_like":
		// SQL LIKE: '%' is the wildcard and the match is anchored, so "api%" is a prefix
		// and "%check%" a substring. Keeping '%' literally (the old behaviour) searched for
		// `*%check%*` and matched nothing. A bare word keeps the substring match callers
		// have always had. '_' stays literal: Splunk has no single-character wildcard, and
		// '_' is far more common as a literal in dimension values. The '%' swap runs after
		// escaping, so a literal '*' in the value is still escaped.
		if strings.Contains(m.Value, "%") {
			return fmt.Sprintf(`%s="%s"`, label, strings.ReplaceAll(value, "%", "*")), nil
		}
		return fmt.Sprintf(`%s="*%s*"`, label, value), nil
	case "_contains":
		return fmt.Sprintf(`%s="*%s*"`, label, value), nil
	case "=~", "_regex":
		// Rendering a regex as a wildcard term (the old behaviour) turned `a|b` into
		// `="*a|b*"`, which matched nothing and reported no error.
		return "", fmt.Errorf("splunk mstats WHERE has no regex operator: use _eq, _neq or _like")
	default:
		return "", fmt.Errorf("unsupported metric label operator for Splunk: %s", m.Operator)
	}
}

// buildSplunkMStatsQuery renders one QueryItem as an mstats search.
//
// The value column is always aliased to a fixed name so the result decoder does not have
// to guess which column holds the number; mstats would otherwise name it after the
// aggregation and metric (`avg(k8s.pod.cpu.usage)`).
const splunkEnterpriseValueColumn = "nb_value"

func buildSplunkMStatsQuery(item QueryItem, labels map[string]string, index string, spanSeconds int, instant bool) (string, error) {
	if !isSafeSplunkMetricName(item.Metric) {
		return "", fmt.Errorf("invalid or unsafe metric name: %q", item.Metric)
	}
	// isSafeSplunkMetricName admits '*' because catalog lookups take a name pattern. An
	// aggregation cannot: mstats refuses `AS nb_value` over a wildcarded name with an
	// error about "rename specifiers" that does not say what the caller did wrong.
	if strings.Contains(item.Metric, "*") {
		return "", fmt.Errorf("metric name %q contains a wildcard: an mstats query aggregates one metric, so name it exactly", item.Metric)
	}
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe metrics index name: %q", index)
	}

	agg, err := splunkEnterpriseAggregation(item.AggregateOperator)
	if err != nil {
		return "", err
	}

	var where []string
	where = append(where, fmt.Sprintf(`index="%s"`, index))

	// Sorted so the generated SPL is stable and assertable.
	for _, key := range sortedKeys(labels) {
		matcher, err := buildSplunkMetricMatcher(LabelMatcher{Label: key, Operator: "_eq", Value: labels[key]})
		if err != nil {
			return "", err
		}
		where = append(where, matcher)
	}
	for _, m := range item.LabelMatchers {
		matcher, err := buildSplunkMetricMatcher(m)
		if err != nil {
			return "", err
		}
		where = append(where, matcher)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `| mstats %s(%s) AS %s WHERE %s`, agg, item.Metric, splunkEnterpriseValueColumn, strings.Join(where, " AND "))

	if !instant {
		if spanSeconds <= 0 {
			spanSeconds = splunkEnterpriseDefaultSpanSeconds
		}
		fmt.Fprintf(&b, " span=%ds", spanSeconds)
	}

	// Group by every dimension the caller filtered on, so a query that narrows to one
	// namespace still returns a labelled series rather than an anonymous one. Without
	// this the frontend has no way to tell two series apart in a multi-series chart.
	groupBy := splunkEnterpriseGroupByDimensions(item, labels)
	if len(groupBy) > 0 {
		fmt.Fprintf(&b, " BY %s", strings.Join(groupBy, ", "))
	}

	return b.String(), nil
}

// splunkEnterpriseGroupByDimensions collects the distinct, mapped dimension names the
// query filters on, in stable order.
func splunkEnterpriseGroupByDimensions(item QueryItem, labels map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		label := raw
		if mapped, ok := splunkEnterpriseMetricLabelMapping[label]; ok {
			label = mapped
		}
		if !isSafeSplunkFieldName(label) || seen[label] {
			return
		}
		seen[label] = true
		out = append(out, label)
	}
	for _, key := range sortedKeys(labels) {
		add(key)
	}
	for _, m := range item.LabelMatchers {
		add(m.Label)
	}
	return out
}

// splunkEnterpriseSpanSeconds resolves the mstats span for a request. An explicit step
// wins; otherwise the span is the narrowest ladder width that keeps one series at or under
// splunkEnterpriseMaxBucketsPerSeries buckets, capped at a day.
//
// This bounds rows PER SERIES only. Rows are buckets x series, so a query grouped BY a
// dimension with many values can still exceed the per-search row cap; the newest-first
// sort in splunkEnterpriseExecutableSPL is the only defence there.
func splunkEnterpriseSpanSeconds(stepInterval int, startMs, endMs int64) int {
	if stepInterval > 0 {
		return stepInterval
	}
	windowSeconds := (endMs - startMs) / 1000
	for _, span := range splunkEnterpriseSpanLadderSeconds {
		if windowSeconds <= int64(span)*splunkEnterpriseMaxBucketsPerSeries {
			return span
		}
	}
	return splunkEnterpriseSpanLadderSeconds[len(splunkEnterpriseSpanLadderSeconds)-1]
}

// splunkEnterpriseExecutableSPL returns the SPL actually sent to Splunk for a validated
// metric query, and whether rows come back newest first.
//
// mstats emits rows in ascending _time and the search `count` keeps the first
// splunkEnterpriseMetricMaxRows of them, so a capped search silently kept the OLDEST
// points: a 31-day query ended 27 days before its end_time with no sign anything was
// missing. Sorting newest first makes the cap drop the oldest rows instead, which is the
// end of the window a chart or an investigation can best afford to lose. `sort 0` lifts
// sort's own 10,000-row default; this assumes the search head's limits.conf lets every
// mstats row reach the sort, which holds for Splunk's defaults.
//
// Applied to every mstats query, rendered or raw (the Code tab, the llm
// splunk_metrics_query tool and GenerateQuery all send raw SPL through the same loop).
// Anything else (mcatalog) is left as written: it has no _time to sort on.
func splunkEnterpriseExecutableSPL(spl string) (string, bool) {
	if splunkEnterpriseOpeningCommand(spl) != "mstats" {
		return spl, false
	}
	return strings.TrimRight(spl, " \t\r\n") + splunkEnterpriseNewestFirst, true
}

// splunkEnterpriseOpeningCommand returns the lower-cased command of the first non-empty
// pipeline segment, or "" when there is none.
func splunkEnterpriseOpeningCommand(spl string) string {
	for _, segment := range splunkEnterpriseSplitPipeline(strings.TrimSpace(spl)) {
		if fields := strings.Fields(segment); len(fields) > 0 {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

// splunkEnterpriseCatalogQueryMessage answers a caller's raw `| mcatalog` search.
// FetchMetricsQuery returns time series, and a catalog row has neither _time nor a value
// column, so running one came back empty with a note blaming the value column. The
// catalog questions each have their own action.
const splunkEnterpriseCatalogQueryMessage = "catalog searches are not time series; use metrics_list_names / " +
	"metrics_list_labels / metrics_list_label_values / metrics_list_series"

// splunkEnterpriseRawCatalogResult reports whether a caller-supplied query is a catalog
// search, and the result to return for it instead of running it. Only the caller's raw
// SPL goes through here: this source's own catalog lookups call runMetricCatalog.
func splunkEnterpriseRawCatalogResult(queryKey, spl string) (QueryResult, bool) {
	if splunkEnterpriseOpeningCommand(spl) != "mcatalog" {
		return QueryResult{}, false
	}
	result := splunkMetricErrorResult(queryKey, spl, errors.New(splunkEnterpriseCatalogQueryMessage))
	result.Note = splunkEnterpriseCatalogQueryMessage
	return result, true
}

// sortSplunkRowsOldestFirst restores ascending time order after a newest-first search, so
// each series' points arrive in the order the chart draws them. Stable, so rows sharing a
// bucket keep Splunk's order, and rows with no _time (instant queries, which all share
// the fallback) are left exactly as returned.
func sortSplunkRowsOldestFirst(rows []map[string]any, fallbackSeconds int64) {
	type keyed struct {
		ts  int64
		row map[string]any
	}
	tmp := make([]keyed, len(rows))
	for i, row := range rows {
		tmp[i] = keyed{ts: splunkEnterpriseRowTimestampMs(row, fallbackSeconds*1000), row: row}
	}
	sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].ts < tmp[j].ts })
	for i := range tmp {
		rows[i] = tmp[i].row
	}
}

// splunkEnterpriseTruncatedNote states that a search hit the row cap, so a chart that
// starts late is never read as "no data before this point".
func splunkEnterpriseTruncatedNote(maxRows int) string {
	return fmt.Sprintf("Result truncated to the most recent %d rows: points at the start of the window are missing. "+
		"Use a larger step_interval or group by fewer dimensions for the full window.", maxRows)
}

// splunkEnterpriseQueryItems normalises a FetchMetricsRequest into query items.
//
// The BUILDER path sends QueryItems keyed by query name. The raw/code path sends
// Queries, whose values are already complete SPL and are passed through untouched (still
// validated). Callers that send neither get an explicit error rather than an empty chart.
func splunkEnterpriseQueryItems(req FetchMetricsRequest) (items map[string]QueryItem, raw map[string]string) {
	if len(req.QueryItems) > 0 {
		return req.QueryItems, nil
	}
	return nil, req.Queries
}

// GetQuery renders the SPL a request would run, without executing it.
func (s *SplunkEnterpriseMetricSource) GetQuery(ctx *security.RequestContext, req FetchMetricsRequest) (string, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return "", err
	}

	items, raw := splunkEnterpriseQueryItems(req)
	keys := sortedKeys(rawKeys(items, raw))
	if len(keys) == 0 {
		return "", nil
	}

	// GetQuery renders ONE query for display, but a request may carry several. The
	// lowest-sorted key is the representative one; sorting is what makes that choice
	// stable rather than dependent on map iteration order.
	key := keys[0]
	if items != nil {
		// Same span FetchMetricsQuery would use. The UI runs this rendered SPL as a raw
		// query, so a span fixed here is the span the chart gets.
		startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)
		span := splunkEnterpriseSpanSeconds(req.StepInterval, startMs, endMs)
		return buildSplunkMStatsQuery(items[key], req.Labels, index, span, req.Instant)
	}
	return raw[key], nil
}

// WrapAggregate implements MetricAggregateWrapper as the identity. GetQuery has already
// rendered the aggregate operator as the mstats function, so the default PromQL wrap
// produced `sum(| mstats sum(...) ...)`, which Splunk cannot parse.
func (s *SplunkEnterpriseMetricSource) WrapAggregate(expr string, _ string) (string, error) {
	return expr, nil
}

// rawKeys returns a set-like map of the keys present in whichever of the two shapes is
// populated, so callers can iterate them in sorted order.
func rawKeys(items map[string]QueryItem, raw map[string]string) map[string]string {
	out := map[string]string{}
	for k := range items {
		out[k] = ""
	}
	for k := range raw {
		out[k] = ""
	}
	return out
}

// FetchMetricsQuery runs each query and converts the rows into series.
//
// A failure on one query is recorded on that query's own result rather than failing the
// batch: a dashboard with six panels should render the five that work.
func (s *SplunkEnterpriseMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)
	// Splunk rejects an inverted or empty window itself, but its error reaches the
	// caller as a dump of the response body.
	if startMs >= endMs {
		return OutputMetricQuery{}, fmt.Errorf("start_time (%d) must be before end_time (%d)", startMs, endMs)
	}
	startTime, endTime := splunkEnterpriseTimeRangeSeconds(startMs, endMs, time.Now())
	span := splunkEnterpriseSpanSeconds(req.StepInterval, startMs, endMs)
	// An instant row has no _time; it describes the window that ends here.
	endSeconds := endMs / 1000

	items, raw := splunkEnterpriseQueryItems(req)
	results := OutputMetricQuery{Results: []QueryResult{}}

	for _, queryKey := range sortedKeys(rawKeys(items, raw)) {
		var spl string
		var buildErr error
		if items != nil {
			spl, buildErr = buildSplunkMStatsQuery(items[queryKey], req.Labels, index, span, req.Instant)
		} else {
			spl = raw[queryKey]
		}
		if buildErr != nil {
			results.Results = append(results.Results, splunkMetricErrorResult(queryKey, spl, buildErr))
			continue
		}
		if vErr := validateSplunkEnterpriseMetricQuery(spl, index); vErr != nil {
			results.Results = append(results.Results, splunkMetricErrorResult(queryKey, spl, vErr))
			continue
		}
		if items == nil {
			if catalog, ok := splunkEnterpriseRawCatalogResult(queryKey, spl); ok {
				results.Results = append(results.Results, catalog)
				continue
			}
		}

		// The result reports the caller's SPL, not the executed one: the UI keeps
		// result.query and re-submits it, which would stack another sort on every run.
		executable, newestFirst := splunkEnterpriseExecutableSPL(spl)
		ctx.GetLogger().Info("splunk enterprise mstats query", "key", queryKey, "spl", executable)

		rows, runErr := execSplunkEnterpriseSearch(cfg, executable, startTime, endTime,
			splunkEnterpriseMetricMaxRows, splunkEnterpriseMetricSearchTimeout)
		if runErr != nil {
			results.Results = append(results.Results, splunkMetricErrorResult(queryKey, spl, runErr))
			continue
		}
		results.Results = append(results.Results, splunkEnterpriseMetricResult(queryKey, spl, rows, newestFirst, endSeconds))
	}

	return results, nil
}

// splunkEnterpriseMetricResult turns one search's rows into its result. newestFirst says
// the rows came back from splunkEnterpriseExecutableSPL's sorted form, which is what makes
// a full page mean "the oldest rows were dropped" and lets the Note say so.
func splunkEnterpriseMetricResult(queryKey, spl string, rows []map[string]any, newestFirst bool, fallbackSeconds int64) QueryResult {
	if newestFirst {
		sortSplunkRowsOldestFirst(rows, fallbackSeconds)
	}
	result := convertSplunkMetricRows(queryKey, spl, rows, fallbackSeconds)
	if newestFirst && len(rows) >= splunkEnterpriseMetricMaxRows && result.Note == "" {
		result.Note = splunkEnterpriseTruncatedNote(splunkEnterpriseMetricMaxRows)
	}
	return result
}

func splunkMetricErrorResult(queryKey, spl string, err error) QueryResult {
	msg := err.Error()
	return QueryResult{QueryKey: queryKey, Query: spl, Error: &msg}
}

// convertSplunkMetricRows folds mstats rows into series.
//
// mstats returns one flat row per (time bucket x dimension combination). Rows are
// grouped by their dimension set — every column except _time and the aliased value — so
// each distinct combination becomes one series with aligned timestamp/value slices.
//
// fallbackSeconds timestamps a row that carries no _time (an instant query).
func convertSplunkMetricRows(queryKey, spl string, rows []map[string]any, fallbackSeconds int64) QueryResult {
	type series struct {
		metric     map[string]string
		timestamps []int64
		values     []float64
	}
	order := []string{}
	bySeries := map[string]*series{}

	for _, row := range rows {
		valueRaw, ok := row[splunkEnterpriseValueColumn]
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(formatSplunkValue(valueRaw)), 64)
		if err != nil {
			// A non-numeric value column means the row is not a data point (mstats can
			// emit a null for a bucket with no samples). Skipping keeps the series
			// contiguous instead of injecting a zero that reads as a real measurement.
			continue
		}

		labels := map[string]string{}
		for k, v := range row {
			if k == splunkEnterpriseValueColumn || k == "_time" || strings.HasPrefix(k, "_") {
				continue
			}
			labels[k] = formatSplunkValue(v)
		}

		key := splunkMetricSeriesKey(labels)
		s, exists := bySeries[key]
		if !exists {
			s = &series{metric: labels}
			bySeries[key] = s
			order = append(order, key)
		}

		s.timestamps = append(s.timestamps, splunkEnterpriseRowTimestampMs(row, fallbackSeconds*1000))
		s.values = append(s.values, value)
	}

	payload := make([]Result, 0, len(order))
	for _, key := range order {
		s := bySeries[key]
		payload = append(payload, Result{
			Metric:     s.metric,
			Timestamps: s.timestamps,
			Values:     s.values,
		})
	}

	result := QueryResult{QueryKey: queryKey, Query: spl, Payload: payload}
	if len(payload) == 0 {
		matched := int64(len(rows))
		result.DocsMatched = &matched
		if len(rows) > 0 {
			result.Note = "mstats returned rows but none carried a numeric value column"
		}
	}
	return result
}

// splunkMetricSeriesKey builds a stable identity for a dimension set.
func splunkMetricSeriesKey(labels map[string]string) string {
	var b strings.Builder
	for _, k := range sortedKeys(labels) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte('\x1f')
	}
	return b.String()
}

// splunkEnterpriseRowTimestampMs reads _time from an mstats row.
//
// An instant query (no span) carries no _time at all, so the caller supplies the window's
// end: the value describes the window that was queried. "Now" (the old answer) was wrong
// for any window that ended in the past, stamping a last-week value as current.
//
// Milliseconds, like every other provider's Result.timestamps on OSS (#733).
func splunkEnterpriseRowTimestampMs(row map[string]any, fallbackMs int64) int64 {
	raw, ok := row["_time"]
	if !ok {
		return fallbackMs
	}
	text := strings.TrimSpace(formatSplunkValue(raw))
	if text == "" {
		return fallbackMs
	}
	// Splunk emits ISO-8601 with a numeric offset for search results, and bare epoch
	// seconds for some export paths; accept both.
	if t, err := time.Parse("2006-01-02T15:04:05.000-07:00", text); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t.UnixMilli()
	}
	if secs, err := strconv.ParseFloat(text, 64); err == nil {
		return int64(secs * 1000)
	}
	return fallbackMs
}

// FetchMetricList lists metric names in the configured metrics index.
//
// Uses mcatalog rather than /services/catalog/metricstore/metrics deliberately: that
// REST endpoint returns an empty list unless an index filter is supplied (verified on
// Splunk 10.4.2 — unfiltered returned 0 entries while the same call with
// filter=index=<name> returned every metric), which makes it easy to ship a silently
// empty picker. mcatalog also reuses the search plumbing already built here rather than
// adding a second HTTP path with its own auth and TLS handling.
func (s *SplunkEnterpriseMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, index, err := splunkEnterpriseMetricContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	spl := fmt.Sprintf(`| mcatalog values(metric_name) WHERE index="%s"`, index)
	rows, err := s.runMetricCatalog(ctx, cfg, index, spl, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	names := splunkEnterpriseCatalogValues(rows, "values(metric_name)")
	filter := strings.TrimSpace(req.Metric)
	metrics := make([]OutputMetrics, 0, len(names))
	for _, name := range names {
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		metrics = append(metrics, OutputMetrics{Metric: name, Attributes: map[string]any{}})
	}
	return metrics, nil
}

// FetchMetricsLabels lists the dimension names present in the metrics index.
//
// `values(_dims)` is preferred over the REST dimensions endpoint because that endpoint
// requires a metric_name argument (verified: omitting it returns "The following required
// arguments are missing: metric_name") and includes Splunk's own host/source/sourcetype
// alongside real dimensions. mcatalog returns just the dimensions.
func (s *SplunkEnterpriseMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, index, err := splunkEnterpriseMetricContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	where := fmt.Sprintf(`index="%s"`, index)
	if name := strings.TrimSpace(req.MetricName); name != "" {
		if !isSafeSplunkMetricName(name) {
			return nil, fmt.Errorf("invalid or unsafe metric name: %q", name)
		}
		where += fmt.Sprintf(` AND metric_name="%s"`, name)
	}
	spl := fmt.Sprintf(`| mcatalog values(_dims) WHERE %s`, where)

	rows, err := s.runMetricCatalog(ctx, cfg, index, spl, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	dims := splunkEnterpriseCatalogValues(rows, "values(_dims)")
	labels := make([]OutputMetricLabels, 0, len(dims))
	for _, d := range dims {
		labels = append(labels, OutputMetricLabels{Label: d, Attributes: map[string]any{}})
	}
	return labels, nil
}

// FetchMetricLabelValues lists the distinct values of one dimension.
func (s *SplunkEnterpriseMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, index, err := splunkEnterpriseMetricContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	label := strings.TrimSpace(req.Label)
	if mapped, ok := splunkEnterpriseMetricLabelMapping[label]; ok {
		label = mapped
	}
	if !isSafeSplunkFieldName(label) {
		return nil, fmt.Errorf("invalid or unsafe dimension name: %q", req.Label)
	}

	// Scoped to the metric when the caller names one, so a dimension only some metrics
	// carry does not offer values from the others: the index-wide listing offered
	// deployment names for node metrics, which then matched nothing.
	where := fmt.Sprintf(`index="%s"`, index)
	if name := strings.TrimSpace(common.GetString(req.Request, "metric_name")); name != "" {
		if !isSafeSplunkMetricName(name) {
			return nil, fmt.Errorf("invalid or unsafe metric name: %q", name)
		}
		where += fmt.Sprintf(` AND metric_name="%s"`, name)
	}
	spl := fmt.Sprintf(`| mcatalog values(%s) WHERE %s`, label, where)
	rows, err := s.runMetricCatalog(ctx, cfg, index, spl, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	values := splunkEnterpriseCatalogValues(rows, fmt.Sprintf("values(%s)", label))
	out := make([]OutputMetricsLabelValues, 0, len(values))
	for _, v := range values {
		out = append(out, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return out, nil
}

// splunkEnterpriseMetricContext resolves config + metrics index in one step, since every
// catalog call needs both and fails identically without them.
func splunkEnterpriseMetricContext(ctx *security.RequestContext, accountId string) (integrations.SplunkEnterpriseConfig, string, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, accountId)
	if err != nil {
		return cfg, "", fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return cfg, "", err
	}
	return cfg, index, nil
}

// runMetricCatalog validates and executes a catalog query.
func (s *SplunkEnterpriseMetricSource) runMetricCatalog(
	ctx *security.RequestContext,
	cfg integrations.SplunkEnterpriseConfig,
	index, spl string,
	startMs, endMs int64,
) ([]map[string]any, error) {
	if err := validateSplunkEnterpriseMetricQuery(spl, index); err != nil {
		return nil, err
	}
	startTime, endTime := splunkEnterpriseTimeRangeSeconds(startMs, endMs, time.Now())
	ctx.GetLogger().Debug("splunk enterprise mcatalog query", "spl", spl)
	return execSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseCatalogLimit, splunkEnterpriseMetricSearchTimeout)
}

// splunkEnterpriseCatalogValues pulls the multivalue column out of an mcatalog result.
//
// mcatalog returns a single row whose one column is an array. formatSplunkValue joins
// arrays with commas for display, so the raw value is read here instead to keep each
// entry separate — a dimension value containing a comma would otherwise be split in two.
func splunkEnterpriseCatalogValues(rows []map[string]any, column string) []string {
	var out []string
	seen := map[string]bool{}
	for _, row := range rows {
		raw, ok := row[column]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				add := strings.TrimSpace(formatSplunkValue(item))
				if add != "" && !seen[add] {
					seen[add] = true
					out = append(out, add)
				}
			}
		default:
			add := strings.TrimSpace(formatSplunkValue(raw))
			if add != "" && !seen[add] {
				seen[add] = true
				out = append(out, add)
			}
		}
	}
	sort.Strings(out)
	return out
}
