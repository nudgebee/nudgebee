package observability

import (
	"encoding/json"
	"errors"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"
)

func convertOrderByWithMapping(orderBy []SortField, mapping map[string]string) []SortField {
	converted := make([]SortField, 0, len(orderBy))
	for _, ob := range orderBy {
		newCol, exists := mapping[ob.ColumnName]
		if !exists {
			newCol = ob.ColumnName
		}
		converted = append(converted, SortField{
			ColumnName: newCol,
			Order:      ob.Order,
		})
	}
	return converted
}

// normaliseInOperand wraps a scalar _in / _not_in operand in a one-element list.
//
// Both operators take a list, but the Traces tab's Service filter is a single-select
// and sends a bare scalar: `workload_name: {_in: "qa-payment-s3"}`. Providers then
// disagreed about what that means - Splunk Enterprise answered 400 (#41493), SolarWinds
// dropped the predicate and returned every span unscoped, Cloud Trace did the same
// until it grew its own `case string:` (#41419), and the ES readers reject or mis-render
// it - so the same bug arrived once per provider. A one-element list means exactly what
// the scalar meant, and this is the one rewriter every logs and traces request passes
// through, so normalising here retires the class instead of patching the next provider.
//
// A []string operand is retyped to []any rather than passed through, so the operand of
// an _in is ALWAYS a []any once past here. Most builders read it through a switch with
// both arms, but swInValues (solarwinds_traces.go) asserts []any alone and silently drops
// the predicate on anything else - the same unscoped-result failure this function exists
// to stop. No caller passes []string today; making the output type uniform is what keeps
// it that way without relying on each provider to be broad enough.
//
// Only _in / _not_in are touched. A nil operand is left alone: "no values" is the
// caller's to reject (each builder already does), not something to turn into [nil].
func normaliseInOperand(op query.BinaryWhereClauseType, val any) any {
	if op != query.In && op != query.NotIn {
		return val
	}
	switch v := val.(type) {
	case []any, nil:
		return val
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	default:
		return []any{val}
	}
}

func convertWhereClauseWithMApping(whereClause query.QueryWhereClause, mapping map[string]string) query.QueryWhereClause {
	converted := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{},
		And:    []query.QueryWhereClause{},
		Or:     []query.QueryWhereClause{},
	}

	// Convert binary conditions
	for col, ops := range whereClause.Binary {
		newCol, exists := mapping[col]
		if !exists {
			newCol = col
		}
		if _, ok := converted.Binary[newCol]; !ok {
			converted.Binary[newCol] = make(map[query.BinaryWhereClauseType]any)
		}
		for op, val := range ops {
			converted.Binary[newCol][op] = normaliseInOperand(op, val)
		}
	}

	// Recursively convert AND conditions
	for _, andClause := range whereClause.And {
		converted.And = append(converted.And, convertWhereClauseWithMApping(andClause, mapping))
	}

	// Recursively convert OR conditions
	for _, orClause := range whereClause.Or {
		converted.Or = append(converted.Or, convertWhereClauseWithMApping(orClause, mapping))
	}

	// Recursively convert the NOT condition. Without this the Not branch is silently
	// dropped here, so a `_not` clause loses its negation before any provider's query
	// builder runs (wrong results, no error) — provider-independent.
	if whereClause.Not != nil {
		notConverted := convertWhereClauseWithMApping(*whereClause.Not, mapping)
		converted.Not = &notConverted
	}

	return converted
}

func GetSeverityLevels(msg string) string {
	msgLower := strings.ToLower(msg)

	errorPatterns := []string{"error", "fail", "fatal", "exception", "traceback", "panic", "segfault"}
	warningPatterns := []string{"warn", "deprecated", "timeout", "retry", "unavailable"}
	debugPatterns := []string{"debug", "trace", "test", "dev", "profiling"}
	infoPatterns := []string{"info", "started", "connected", "listening", "running", "success"}

	for _, word := range errorPatterns {
		if strings.Contains(msgLower, word) {
			return "error"
		}
	}
	for _, word := range warningPatterns {
		if strings.Contains(msgLower, word) {
			return "warning"
		}
	}
	for _, word := range debugPatterns {
		if strings.Contains(msgLower, word) {
			return "debug"
		}
	}
	for _, word := range infoPatterns {
		if strings.Contains(msgLower, word) {
			return "info"
		}
	}

	// fallback
	return "info"

}

func getQueryRequest(ctx *security.RequestContext, traceRequest TracesQueryBuilderRequest, tableDef query.TableDefinition, tableName string) query.QueryRequest {
	queryRequest := query.QueryRequest{}
	queryRequest.Where = traceRequest.Where
	queryRequest.Limit = traceRequest.Limit
	queryRequest.OrderBy = traceRequest.OrderBy
	queryRequest.Offset = traceRequest.Offset
	queryRequest.Table = tableName
	traceQuery := queryRequest
	binary := traceQuery.Where.Binary
	tableDefination := ClickhouseTraceTableDefinition
	if tableName == "traces_grouping_v2" {
		tableDefination = ClickhouseTraceGroupingTableDefinition
	}
	cols := make([]query.QueryColumn, 0, len(tableDefination))
	for k, tableColDef := range tableDefination {
		var queryColDef query.QueryColumn
		queryColDef.Name = k
		if !tableColDef.IsAggregated {
			cols = append(cols, queryColDef)
		}
	}
	queryRequest.Columns = cols

	if binary != nil && binary[tableDef.AccountIdColumnName] != nil && binary[tableDef.AccountIdColumnName][query.Eq] != nil {
		delete(binary, tableDef.AccountIdColumnName)
		traceQuery.Columns = lo.Filter(traceQuery.Columns, func(column query.QueryColumn, i int) bool {
			return column.Name != tableDef.AccountIdColumnName
		})
	}
	return queryRequest
}

var possibleFormats = []string{
	"2006-01-02 15:04:05,000",      // 2025-11-18 09:18:59,999
	time.RFC3339,                   // 2025-11-19T15:04:05Z
	"2006-01-02 15:04:05",          // 2025-11-19 15:04:05
	"2006-01-02",                   // 2025-11-19
	"2006/01/02 15:04:05",          // 2025/11/19 15:04:05
	"02-01-2006",                   // 19-11-2025
	"02/01/2006",                   // 19/11/2025
	"2006-01-02T15:04:05.000Z0700", // ISO + timezone
	"2006-01-02T15:04:05Z0700",
}

func ParseDateToMillis(dateStr string) (int64, error) {
	for _, format := range possibleFormats {
		t, err := time.Parse(format, dateStr)
		if err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, errors.New("no matching date format found for: " + dateStr)
}

// logGroupWindowEndUnix normalizes a request's end time to unix seconds. Callers
// use it only as the fallback for a group whose own lines yield no timestamp —
// reporting it as a group's last-seen makes every row read as the same time.
func logGroupWindowEndUnix(endTime int64) int64 {
	switch {
	case endTime <= 0:
		return time.Now().Unix()
	case endTime >= 1e12: // milliseconds
		return endTime / 1000
	default:
		return endTime
	}
}

// logLastSeenUnix converts an OutputLog timestamp to unix seconds for use as a log
// group's last-seen. Returns 0 when the timestamp is absent or unparseable, which
// callers treat as "no reading" and fall back to the query window.
func logLastSeenUnix(timestamp string) int64 {
	if timestamp == "" {
		return 0
	}
	ms, err := ParseDateToMillis(timestamp)
	if err != nil {
		return 0
	}
	return ms / 1000
}

// isEmptyWhereClause returns true when a QueryWhereClause has no conditions at all.
func isEmptyWhereClause(where query.QueryWhereClause) bool {
	return len(where.Binary) == 0 && len(where.And) == 0 && len(where.Or) == 0 && where.Not == nil
}

// removeKeysFromWhereClause returns a copy of where with all occurrences of the
// given keys removed from every Binary map, at every nesting level.
// Empty sub-clauses produced by the removal are pruned so that downstream query
// builders never receive dangling {Binary:{}} noise inside And/Or/Not.
// If keys is empty the original clause is returned unchanged.
func removeKeysFromWhereClause(where query.QueryWhereClause, keys []string) query.QueryWhereClause {
	if len(keys) == 0 {
		return where
	}
	ignoreSet := make(map[string]bool, len(keys))
	for _, k := range keys {
		ignoreSet[k] = true
	}
	return removeKeysFromWhereClauseRecursive(where, ignoreSet)
}

func removeKeysFromWhereClauseRecursive(where query.QueryWhereClause, ignoreSet map[string]bool) query.QueryWhereClause {
	return query.QueryWhereClause{
		Binary: filterBinaryClause(where.Binary, ignoreSet),
		And:    filterClauseList(where.And, ignoreSet),
		Or:     filterClauseList(where.Or, ignoreSet),
		Not:    filterNotClause(where.Not, ignoreSet),
	}
}

// filterBinaryClause removes ignored keys from a BinaryWhereClause map.
func filterBinaryClause(binary query.BinaryWhereClause, ignoreSet map[string]bool) query.BinaryWhereClause {
	if len(binary) == 0 {
		return nil
	}
	result := make(query.BinaryWhereClause, len(binary))
	for key, ops := range binary {
		if !ignoreSet[key] {
			result[key] = ops
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// filterClauseList recursively filters a slice of sub-clauses, pruning empty results.
func filterClauseList(clauses []query.QueryWhereClause, ignoreSet map[string]bool) []query.QueryWhereClause {
	var result []query.QueryWhereClause
	for _, clause := range clauses {
		filtered := removeKeysFromWhereClauseRecursive(clause, ignoreSet)
		if !isEmptyWhereClause(filtered) {
			result = append(result, filtered)
		}
	}
	return result
}

// parseInt64Value safely converts an interface value to int64.
// It handles int64, float64 (JSON default), and int types.
// Returns (value, true) on success, (0, false) if the type is unsupported.
func parseInt64Value(val any) (int64, bool) {
	switch v := val.(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

// filterNotClause recursively filters a Not clause, returning nil if it becomes empty.
func filterNotClause(not *query.QueryWhereClause, ignoreSet map[string]bool) *query.QueryWhereClause {
	if not == nil {
		return nil
	}
	filtered := removeKeysFromWhereClauseRecursive(*not, ignoreSet)
	if isEmptyWhereClause(filtered) {
		return nil
	}
	return &filtered
}

// rootCandidate pairs a span with the fields pickRootSpans needs, so each span's timestamp is
// parsed exactly once (not O(N log N) times inside the sort comparator).
type rootCandidate struct {
	span    common.OpenTelemetryTrace
	isRoot  bool
	startMs int64
}

// pickRootSpans reduces a flat span list to one representative row per trace for the "By Traces"
// view: the span with an empty ParentSpanID, or — when no root is present in the set — the
// earliest span by Timestamp. The result is re-sorted to honor the request's primary OrderBy
// (timestamp or duration_ns, asc/desc) so page ordering matches span mode. Used by every
// non-ClickHouse provider (ClickHouse dedups in SQL instead).
func pickRootSpans(spans []common.OpenTelemetryTrace, orderBy []query.QueryOrderBy) []common.OpenTelemetryTrace {
	chosen := map[string]rootCandidate{}
	seenOrder := make([]string, 0, len(spans))
	for _, sp := range spans {
		key := sp.TraceID
		if key == "" {
			// No trace id to group on — keep the span as its own row (keyed by span id).
			key = "span:" + sp.SpanID
		}
		startMs, _ := ParseDateToMillis(sp.Timestamp)
		cand := rootCandidate{span: sp, isRoot: sp.ParentSpanID == "", startMs: startMs}
		existing, ok := chosen[key]
		if !ok {
			chosen[key] = cand
			seenOrder = append(seenOrder, key)
			continue
		}
		// Prefer a real root; on a tie prefer the earliest span as a pseudo-root.
		if cand.isRoot != existing.isRoot {
			if cand.isRoot {
				chosen[key] = cand
			}
		} else if cand.startMs < existing.startMs {
			chosen[key] = cand
		}
	}
	candidates := make([]rootCandidate, 0, len(seenOrder))
	for _, key := range seenOrder {
		candidates = append(candidates, chosen[key])
	}
	sortRootCandidates(candidates, orderBy)
	result := make([]common.OpenTelemetryTrace, 0, len(candidates))
	for _, cand := range candidates {
		result = append(result, cand.span)
	}
	return result
}

// sortRootCandidates orders root candidates by the request's primary sort column (timestamp or
// duration_ns), reusing the timestamp parsed once in pickRootSpans. Defaults to timestamp desc
// when no order is given, matching the span listing default.
func sortRootCandidates(candidates []rootCandidate, orderBy []query.QueryOrderBy) {
	col := "timestamp"
	order := query.Desc
	if len(orderBy) > 0 {
		col = orderBy[0].Column
		order = orderBy[0].Order
	}
	desc := order == query.Desc || order == query.DescNullsLast || order == query.DescNullsFirst
	sort.SliceStable(candidates, func(i, j int) bool {
		var vi, vj int64
		switch col {
		case "duration_ns":
			vi, vj = candidates[i].span.DurationNs, candidates[j].span.DurationNs
		default:
			vi, vj = candidates[i].startMs, candidates[j].startMs
		}
		if desc {
			return vi > vj
		}
		return vi < vj
	})
}

// queryRootSpansViaSpans is the generic "By Traces" implementation for providers without native
// trace-level grouping: it runs the provider's normal span query and reduces the result to one
// root span per trace. The provider applies limit/offset at the span level, so a page may yield
// fewer than `limit` distinct traces — acceptable for v1 (ClickHouse does exact trace paging).
func queryRootSpansViaSpans(ctx *security.RequestContext, src TraceSource, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	spans, err := src.QueryTraces(ctx, req)
	if err != nil {
		return nil, err
	}
	return pickRootSpans(spans, req.QueryRequest.OrderBy), nil
}

// countTracesByTraceEstimate is the generic "By Traces" count for providers that cannot cheaply
// count distinct traces. Count = -1 signals the frontend to estimate pagination — the same
// contract Jaeger's CountTraces already uses.
func countTracesByTraceEstimate() (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

// labelKeyExpander is implemented by a log source on which one canonical label is
// stored under several provider keys (SigNoz keeps a workload's name under
// k8s.deployment.name, k8s.statefulset.name, … depending on its kind). The first
// key of each list is the one GetLabelMapping maps the label to.
type labelKeyExpander interface {
	ExpandedLabelKeys() map[string][]string
}

// negativeWhereOperators match rows where a condition does NOT hold. Expanded over
// several keys they must hold for every key (AND); the rest need any key (OR).
var negativeWhereOperators = map[query.BinaryWhereClauseType]bool{
	query.Nq: true, query.NqF: true, query.NotIn: true, query.NLike: true,
	query.NIContains: true, query.NRegex: true,
	NotContains: true, NotRegEx: true, NotExist: true,
}

// expandCanonicalLabelKeys rewrites every condition on an expandable canonical
// label into the same condition over each of the source's keys for it — OR for a
// positive condition, AND for a negative one — before the label mapping runs, so
// `app = redis` also finds a StatefulSet named redis. A label the merged mapping
// sends elsewhere (an account override) is left for the mapping. It also returns
// every provider key an applied expansion wrote, for the validators.
func expandCanonicalLabelKeys(where query.QueryWhereClause, source LogSource, mapping map[string]string) (query.QueryWhereClause, map[string]bool) {
	expander, ok := source.(labelKeyExpander)
	if !ok {
		return where, nil
	}
	expansions := map[string][]string{}
	for label, keys := range expander.ExpandedLabelKeys() {
		if len(keys) > 1 && mapping[label] == keys[0] {
			expansions[label] = keys
		}
	}
	if len(expansions) == 0 {
		return where, nil
	}
	written := map[string]bool{}
	return expandLabelKeys(where, expansions, written), written
}

func expandLabelKeys(where query.QueryWhereClause, expansions map[string][]string, written map[string]bool) query.QueryWhereClause {
	out := query.QueryWhereClause{Binary: query.BinaryWhereClause{}}
	// app and workload expand to the same keys, so `app = x AND workload = x` would
	// AND two identical 5-way ORs — 25 SigNoz queries for a 5-query filter. An
	// expanded group identical to a sibling is added once.
	seen := map[string]bool{}
	addAnd := func(c query.QueryWhereClause) {
		if b, err := json.Marshal(c); err == nil {
			if seen[string(b)] {
				return
			}
			seen[string(b)] = true
		}
		out.And = append(out.And, c)
	}
	for field, conds := range where.Binary {
		keys, expand := expansions[field]
		if !expand {
			out.Binary[field] = conds
			continue
		}
		for op, value := range conds {
			leaves := make([]query.QueryWhereClause, len(keys))
			for i, k := range keys {
				leaves[i] = query.QueryWhereClause{Binary: query.BinaryWhereClause{k: {op: value}}}
				written[k] = true
			}
			isNull, _ := value.(bool)
			if negativeWhereOperators[op] || (op == query.IsNull && isNull) {
				addAnd(query.QueryWhereClause{And: leaves})
			} else {
				addAnd(query.QueryWhereClause{Or: leaves})
			}
		}
	}
	for _, sub := range where.And {
		addAnd(expandLabelKeys(sub, expansions, written))
	}
	for _, sub := range where.Or {
		out.Or = append(out.Or, expandLabelKeys(sub, expansions, written))
	}
	if where.Not != nil {
		n := expandLabelKeys(*where.Not, expansions, written)
		out.Not = &n
	}
	return out
}

// expandedSecondaryKeys lists the extra keys expandCanonicalLabelKeys may add for
// source. Validation ignores them: a cluster with no StatefulSet logs has no
// k8s.statefulset.name, which must not be reported as an unknown label the caller
// typed.
func expandedSecondaryKeys(source LogSource) map[string]bool {
	expander, ok := source.(labelKeyExpander)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, keys := range expander.ExpandedLabelKeys() {
		for _, k := range keys[1:] {
			out[k] = true
		}
	}
	return out
}
