package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SignozSource is a LogSource implementation for Signoz.
type SignozSource struct{}

// signozLogLabelMapping maps the canonical log vocabulary onto SigNoz's own
// attribute names.
//
// SigNoz stores OTel resource attributes under their dotted semantic-convention
// names (`k8s.namespace.name`) and the log level in the `severity_text` column.
// FlattenSignozQuery passes a field straight through as an attribute key, so an
// unmapped canonical name (`namespace`, `level`) reaches SigNoz as an attribute
// that does not exist and matches nothing — with a success status and no error.
// Measured on SigNoz v0.51 and v0.145: `level = ERROR` and `namespace = x` both
// returned 0 rows where `severity_text` / `k8s.namespace.name` returned the data,
// which is how Nubi answered "no ERROR logs" over logs that existed.
//
// `message` is the spelling every producer is moving to (#39573); SigNoz calls the
// log line `body`. The rest mirrors cubeAPMLogLabelMapping (also dotted OTel
// names), except `service` and the level, which SigNoz keeps as `service.name`
// and `severity_text`. Deployments whose shipper uses other names remap
// per-account via the label-mapping override rather than editing this table.
var signozLogLabelMapping = map[string]string{
	"message":   "body",
	"namespace": signozNamespaceKey,
	"pod":       signozPodKey,
	"container": "k8s.container.name",
	"node":      "k8s.node.name",
	"workload":  "k8s.deployment.name",
	"app":       "k8s.deployment.name",
	"cluster":   "k8s.cluster.name",
	"host":      "host.name",
	"hostname":  "host.name",
	"service":   "service.name",
	"severity":  "severity_text",
	"level":     "severity_text",
}

// signozLabelValueParams resolves the attribute key and tag type for a label-value
// lookup. A canonical name is mapped through signozLogLabelMapping and looked up
// as a resource attribute: the caller's tag type described the canonical label,
// not the SigNoz key it became. Every mapped key is an OTel resource attribute
// except severity_text and body, which are columns SigNoz answers under any tag
// type. Measured on v0.51 and v0.145: "resource" returned values for
// k8s.namespace.name, service.name and severity_text; "tag" and an empty tag type
// returned nothing for the resource attributes.
func signozLabelValueParams(labelName string, tagType any) (string, any) {
	if mapped, ok := signozLogLabelMapping[labelName]; ok {
		return mapped, "resource"
	}
	return labelName, tagType
}

// signozSeriesPoints reads one aggregate series' points. SigNoz returns them as
// `values: [{timestamp, value}]` with the value as a numeric STRING ("2");
// `points` with float values is accepted too. Reading only `points` as floats
// made every log-group count 0 on both v0.51 and v0.145, and the Log Groups
// page — which hides zero-count groups — always empty.
//
// Timestamps come back in Unix SECONDS, the LogGroup contract every other
// provider follows (the Log Groups page multiplies by 1000); SigNoz reports
// milliseconds. Each point is moved from its bucket's start to its end, capped at
// the query's end: the page shows the latest point as "Last Time" and anchors the
// drill-down there, and a bucket start is up to an hour before the logs it counts.
func signozSeriesPoints(series map[string]any, endTimeMs int64) ([]int64, []float64) {
	endSec := logGroupTimestampSeconds(endTimeMs)
	raw, ok := series["values"].([]any)
	if !ok {
		raw, _ = series["points"].([]any)
	}
	timestamps := make([]int64, 0, len(raw))
	values := make([]float64, 0, len(raw))
	for _, p := range raw {
		point, ok := p.(map[string]any)
		if !ok {
			continue
		}
		val, okVal := signozNumber(point["value"])
		ts, okTs := signozNumber(point["timestamp"])
		if !okVal || !okTs {
			continue
		}
		bucketEnd := logGroupTimestampSeconds(int64(ts)) + signozLogGroupStepSeconds
		if endSec > 0 && bucketEnd > endSec {
			bucketEnd = endSec
		}
		timestamps = append(timestamps, bucketEnd)
		values = append(values, val)
	}
	return timestamps, values
}

// signozLogGroupStepSeconds is the bucket width of both SigNoz log-group queries;
// signozSeriesPoints converts bucket starts to bucket ends with it.
const signozLogGroupStepSeconds = 3600

// signozSeverityKey is the SigNoz column canonical `level`/`severity` map to.
const signozSeverityKey = "severity_text"

// signozSeverityGroups maps each level mapSeverity DISPLAYS to the stored
// severity_text spellings it displays them from (WARNING and WARN both show as
// "warn"). TestSignozSeverityGroupsMatchDisplay keeps the two in step.
var signozSeverityGroups = map[string][]string{
	"debug": {"debug", "trace"},
	"info":  {"info", "information"},
	"warn":  {"warn", "warning"},
	"error": {"error"},
	"fatal": {"fatal", "critical"},
}

// signozSeverityFilter makes an equality or membership filter on severity_text
// match every stored spelling of the level it names. SigNoz compares
// severity_text case-sensitively and stores what the shipper sent (usually
// "ERROR"), while Nudgebee displays the level lowercased and folded (mapSeverity),
// so a filter copied from the screen — `level = error`, `level = warn` — matched
// nothing: Nubi answered "0 ERROR logs" over logs that existed. A displayed level
// expands to every stored spelling it is displayed from; any other value (an
// explicit stored name such as "trace") only to its own case variants. Eq/Neq
// become in/not-in over the expanded set. Anything else — other operators, a
// non-string or empty value — is returned exactly as given.
func signozSeverityFilter(op query.BinaryWhereClauseType, value any) (query.BinaryWhereClauseType, any) {
	var values []any
	newOp := op
	unchanged := value // what an early return hands back
	switch op {
	case Eq, EqF:
		newOp, values = In, []any{value}
	case Nq, NqF:
		newOp, values = NotIn, []any{value}
	case In, NotIn:
		switch v := value.(type) {
		case []any:
			values = v
		case []string:
			for _, s := range v {
				values = append(values, s)
			}
			unchanged = values // FlattenSignozQuery only accepts []any for in/nin
		default:
			return op, value
		}
	default:
		return op, value
	}

	seen := map[string]bool{}
	var expanded []any
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			expanded = append(expanded, s)
		}
	}
	for _, v := range values {
		str, ok := v.(string)
		if !ok || str == "" {
			return op, unchanged
		}
		names, displayed := signozSeverityGroups[strings.ToLower(str)]
		if !displayed {
			names = []string{strings.ToLower(str)}
		}
		add(str)
		for _, name := range names {
			add(name)
			add(strings.ToUpper(name))
			first, size := utf8.DecodeRuneInString(name)
			add(string(unicode.ToUpper(first)) + name[size:])
		}
	}
	return newOp, expanded
}

// signozErrorSeverities is every stored severity_text spelling of the levels the
// log-group queries count (error and fatal), from the same table filters use.
func signozErrorSeverities() []any {
	_, v := signozSeverityFilter(In, []any{"error", "fatal"})
	return v.([]any)
}

// signozNumber accepts a finite JSON number or numeric string. NaN and ±Inf
// (ParseFloat accepts "NaN"/"Inf") are rejected: int64(math.Round(NaN)) is an
// implementation-defined garbage count.
func signozNumber(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case string:
		parsed, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func (s *SignozSource) GetLabelMapping() map[string]string {
	return signozLogLabelMapping
}

// signozWorkloadKeys are the attributes the OTel k8sattributes processor stores a
// workload's name under, one per kind. Deployment first: it is what
// signozLogLabelMapping maps app and workload to.
var signozWorkloadKeys = []string{
	"k8s.deployment.name", "k8s.statefulset.name", "k8s.daemonset.name", "k8s.job.name", "k8s.cronjob.name",
}

// signozExpandedLabelKeys makes app and workload match a workload of any kind:
// mapped to k8s.deployment.name alone they missed StatefulSets, DaemonSets, Jobs
// and CronJobs.
var signozExpandedLabelKeys = map[string][]string{"app": signozWorkloadKeys, "workload": signozWorkloadKeys}

func (s *SignozSource) ExpandedLabelKeys() map[string][]string { return signozExpandedLabelKeys }

func (s *SignozSource) GetSupportedOperators() []string {
	return signozSupportedOperators
}

const (
	Nq          query.BinaryWhereClauseType = "_neq"
	Eq          query.BinaryWhereClauseType = "_eq"
	Lt          query.BinaryWhereClauseType = "_lt"
	Gt          query.BinaryWhereClauseType = "_gt"
	Lte         query.BinaryWhereClauseType = "_lte"
	Gte         query.BinaryWhereClauseType = "_gte"
	In          query.BinaryWhereClauseType = "_in"
	NotIn       query.BinaryWhereClauseType = "_not_in"
	Like        query.BinaryWhereClauseType = "_like"
	Between     query.BinaryWhereClauseType = "_between"
	Contains    query.BinaryWhereClauseType = "_contains"
	NotContains query.BinaryWhereClauseType = "_not_contains"
	Exist       query.BinaryWhereClauseType = "_exist"
	NotExist    query.BinaryWhereClauseType = "_not_exist"
	ILike       query.BinaryWhereClauseType = "_ilike"
	HasKey      query.BinaryWhereClauseType = "_has_key"
	IsNull      query.BinaryWhereClauseType = "_is_null"
	RegEx       query.BinaryWhereClauseType = "_regex"
	NotRegEx    query.BinaryWhereClauseType = "_not_regex"
	NqF         query.BinaryWhereClauseType = "_neq_f"
	EqF         query.BinaryWhereClauseType = "_eq_f"
	LikeF       query.BinaryWhereClauseType = "_like_f"
	ILikeF      query.BinaryWhereClauseType = "_ilike_f"
	NLike       query.BinaryWhereClauseType = "_nlike"
)

type FlattenedClause struct {
	Key   map[string]string `json:"key"`
	Value any               `json:"value"`
	Op    string            `json:"op"`
}

// signozOperators maps every where-clause operator SigNoz can run to its filter op.
// Both the catalog tokens the UI and Nubi send (_nregex, _has_key, _icontains …)
// and the SigNoz-local spellings the enricher's item conversion produces
// (_not_regex, _exist …) are accepted. SigNoz's contains/ncontains are already
// case-insensitive, so the i-variants map onto them. _is_null is handled in
// signozLeaf because its op depends on the value.
var signozOperators = map[query.BinaryWhereClauseType]string{
	Eq: "=", EqF: "=", Nq: "!=", NqF: "!=",
	In: "in", NotIn: "nin",
	Like: "like", NLike: "nlike", ILike: "regex", // ILike: see signozILikeRegex
	Contains: "contains", query.IContains: "contains",
	NotContains: "ncontains", query.NIContains: "ncontains",
	RegEx: "regex", query.NRegex: "nregex", NotRegEx: "nregex",
	HasKey: "exists", Exist: "exists", NotExist: "nexists",
	Lt: "<", Lte: "<=", Gt: ">", Gte: ">=",
}

// signozSupportedOperators is what both SigNoz sources advertise to the UI and
// Nubi: the catalog token for every operator signozOperators runs.
var signozSupportedOperators = []string{
	"_eq", "_neq", "_in", "_not_in", "_like", "_nlike", "_ilike",
	"_contains", "_icontains", "_nicontains", "_regex", "_nregex",
	"_has_key", "_is_null", "_gt", "_gte", "_lt", "_lte",
}

// signozNegatedOperators gives each operator its complement, used to push a NOT
// down to the leaves (SigNoz has no NOT). A negated filter on an attribute a row
// lacks matches that row, as NOT(=) should: measured on v0.145, `user.tier != gold`
// returned the 3 non-gold rows plus the 6 rows without the attribute.
var signozNegatedOperators = map[query.BinaryWhereClauseType]query.BinaryWhereClauseType{
	Eq: Nq, Nq: Eq, EqF: NqF, NqF: EqF,
	In: NotIn, NotIn: In,
	Like: NLike, NLike: Like,
	Contains: query.NIContains, query.IContains: query.NIContains, NotContains: query.IContains, query.NIContains: query.IContains,
	RegEx: query.NRegex, query.NRegex: RegEx, NotRegEx: RegEx,
	HasKey: NotExist, Exist: NotExist, NotExist: Exist,
	Lt: Gte, Gte: Lt, Lte: Gt, Gt: Lte,
}

// signozMaxQueries caps how many builder queries one where-clause may expand to.
// Each OR branch is one SigNoz query (its builder filter cannot express OR), and
// the queries run in a single request, so the cap bounds the ClickHouse work one
// log fetch can cause.
const signozMaxQueries = 25

// signozLeaf translates one binary condition into a SigNoz filter item.
func signozLeaf(field string, op query.BinaryWhereClauseType, value any) (FlattenedClause, error) {
	if field == signozSeverityKey {
		op, value = signozSeverityFilter(op, value)
	}
	// SigNoz's `!=` is a SUBSTRING match on resource attributes, not an exact
	// one, while `=` is exact. Measured on v0.145 against a 15-row set:
	// `service.name != "e2e"` returned 0 of 15 (it excluded all three e2e-*
	// services) and `k8s.pod.name != "checkout"` returned 10 of 15 (it excluded
	// the checkout-* pod), so a user filtering `namespace != prod` silently also
	// loses `prod-east` and `production`.
	//
	// `nin` with a single element is exact on every field class measured — resource
	// attribute, top-level column (severity_text) and log attribute — and keeps the
	// rest of `!=`'s semantics, including matching rows that lack the attribute
	// entirely (`user.tier != gold` and `user.tier nin [gold]` both returned the
	// same 9 rows). So never emit `!=`.
	//
	// This covers NOT as well: signozNegatedOperators pushes a NOT down to the
	// leaves as Eq -> Nq, which lands here. signozSeverityFilter already does the
	// same rewrite for the severity key, for its own case-folding reasons.
	//
	// The rewrite is unconditional: a caller may write `_neq` with a list value
	// ({"ns": {"_neq": ["a", "b"]}}), and only the wrapping is conditional. Making
	// the whole rewrite conditional on a non-list value left `!=` reachable for
	// exactly that input.
	if op == Nq {
		op = NotIn
		if _, isList := value.([]any); !isList {
			value = []any{value}
		}
	}
	if op == ILike {
		pattern, isString := value.(string)
		if !isString {
			return FlattenedClause{}, fmt.Errorf("value for '_ilike' must be a string")
		}
		value = signozILikeRegex(pattern)
	}
	signozOp, ok := signozOperators[op]
	switch {
	case op == IsNull:
		isNull, isBool := value.(bool)
		if !isBool {
			return FlattenedClause{}, fmt.Errorf("value for '_is_null' must be true or false")
		}
		signozOp = "exists"
		if isNull {
			signozOp = "nexists"
		}
	case !ok:
		return FlattenedClause{}, fmt.Errorf("unsupported operator: %s", op)
	case op == In || op == NotIn:
		if _, isList := value.([]any); !isList {
			return FlattenedClause{}, fmt.Errorf("value for '%s' operator must be an array", signozOp)
		}
	}
	return FlattenedClause{Key: signozFilterKey(field), Value: value, Op: signozOp}, nil
}

// signozFilterKey is the filter-item key for field: typed as a string resource
// attribute when field is one of signozResourceKeys, untyped otherwise.
func signozFilterKey(field string) map[string]string {
	key := map[string]string{"key": field}
	if signozResourceKeys[field] {
		key["type"] = "resource"
		key["dataType"] = "string"
	}
	return key
}

// signozResourceKeys are the keys signozFilterKey sends typed as string resource
// attributes: every key our own mapping produces (see signozLabelValueParams —
// all are OTel resource attributes except body and severity_text, which are
// columns) plus the per-kind workload keys. Other keys stay untyped.
//
// An untyped key is resolved by SigNoz from its attribute metadata, and a key
// registered as both a resource and a log attribute resolves to the log
// attribute. k8s.namespace.name is one: Kubernetes event logs carry it as a log
// attribute. Measured on v0.145 over one hour of a Deployment's logs:
// `k8s.deployment.name = X AND k8s.namespace.name = Y` untyped returned 0 rows,
// typed "resource" returned 50+; `k8s.namespace.name = Y` alone returned 42
// untyped and 100 typed. Typing them "tag" (log attribute) returned 0.
var signozResourceKeys = func() map[string]bool {
	keys := map[string]bool{signozNamespaceKey: true, signozPodKey: true}
	for _, k := range signozLogLabelMapping {
		if k != "body" && k != signozSeverityKey {
			keys[k] = true
		}
	}
	for _, k := range signozWorkloadKeys {
		keys[k] = true
	}
	return keys
}()

// signozILikeRegex turns a LIKE pattern into the case-insensitive regex SigNoz runs
// for _ilike. SigNoz v0.51 has no ilike operator (HTTP 400, measured), while regex
// with an inline (?i) works on v0.51 and v0.145: % is any run, _ any one character,
// everything else literal, anchored like LIKE.
func signozILikeRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	return b.String()
}

// signozNegate returns NOT q, with the negation pushed onto the leaves by De Morgan.
// A clause is the AND of its binary conditions, its And children, the OR of its Or
// children, and its Not child; NOT of that is the OR of each part negated.
func signozNegate(q query.QueryWhereClause) (query.QueryWhereClause, error) {
	var parts []query.QueryWhereClause
	for field, conds := range q.Binary {
		for op, value := range conds {
			if op == IsNull {
				isNull, ok := value.(bool)
				if !ok {
					return query.QueryWhereClause{}, fmt.Errorf("value for '_is_null' must be true or false")
				}
				parts = append(parts, query.QueryWhereClause{Binary: query.BinaryWhereClause{field: {IsNull: !isNull}}})
				continue
			}
			neg, ok := signozNegatedOperators[op]
			if !ok {
				return query.QueryWhereClause{}, fmt.Errorf("operator %s cannot be negated in SigNoz queries", op)
			}
			parts = append(parts, query.QueryWhereClause{Binary: query.BinaryWhereClause{field: {neg: value}}})
		}
	}
	for _, sub := range q.And {
		n, err := signozNegate(sub)
		if err != nil {
			return query.QueryWhereClause{}, err
		}
		parts = append(parts, n)
	}
	if len(q.Or) > 0 {
		var all []query.QueryWhereClause
		for _, sub := range q.Or {
			n, err := signozNegate(sub)
			if err != nil {
				return query.QueryWhereClause{}, err
			}
			all = append(all, n)
		}
		parts = append(parts, query.QueryWhereClause{And: all})
	}
	if q.Not != nil {
		parts = append(parts, *q.Not)
	}
	return query.QueryWhereClause{Or: parts}, nil
}

// signozDNF rewrites a where-clause as an OR of AND-terms, one SigNoz builder
// query per term. SigNoz's v3/v4 builder filter cannot express OR on any version:
// a nested group is rejected with 400, and `filters.op: "OR"` is silently run as
// AND (measured on v0.51 and v0.145: 0 rows where 6 matched). So OR becomes
// separate queries whose rows are merged (signozMergeRows), and NOT is pushed to
// the leaves. A clause with no conditions is one empty term (match everything).
func signozDNF(q query.QueryWhereClause) ([][]FlattenedClause, error) {
	terms := [][]FlattenedClause{{}}
	and := func(other [][]FlattenedClause) error {
		if len(terms)*len(other) > signozMaxQueries {
			return fmt.Errorf("this filter needs %d SigNoz queries (OR branches multiply); the limit is %d — simplify the OR conditions", len(terms)*len(other), signozMaxQueries)
		}
		next := make([][]FlattenedClause, 0, len(terms)*len(other))
		for _, a := range terms {
			for _, b := range other {
				term := make([]FlattenedClause, 0, len(a)+len(b))
				next = append(next, append(append(term, a...), b...))
			}
		}
		terms = next
		return nil
	}

	for field, conds := range q.Binary {
		for op, value := range conds {
			leaf, err := signozLeaf(field, op, value)
			if err != nil {
				return nil, err
			}
			for i := range terms {
				terms[i] = append(terms[i], leaf)
			}
		}
	}
	for _, sub := range q.And {
		subTerms, err := signozDNF(sub)
		if err != nil {
			return nil, err
		}
		if err := and(subTerms); err != nil {
			return nil, err
		}
	}
	if len(q.Or) > 0 {
		var alternatives [][]FlattenedClause
		for _, sub := range q.Or {
			subTerms, err := signozDNF(sub)
			if err != nil {
				return nil, err
			}
			alternatives = append(alternatives, subTerms...)
			if len(alternatives) > signozMaxQueries {
				return nil, fmt.Errorf("this filter needs more than %d SigNoz queries (OR branches multiply) — simplify the OR conditions", signozMaxQueries)
			}
		}
		if err := and(alternatives); err != nil {
			return nil, err
		}
	}
	if q.Not != nil {
		negated, err := signozNegate(*q.Not)
		if err != nil {
			return nil, err
		}
		negTerms, err := signozDNF(negated)
		if err != nil {
			return nil, err
		}
		if err := and(negTerms); err != nil {
			return nil, err
		}
	}
	return terms, nil
}

// FlattenSignozQuery translates a where-clause that SigNoz can run as a single
// builder query (no OR after NOT is pushed down) into its filter items. Callers
// that can run several queries use signozDNF.
func FlattenSignozQuery(q query.QueryWhereClause) ([]FlattenedClause, error) {
	terms, err := signozDNF(q)
	if err != nil {
		return nil, err
	}
	if len(terms) != 1 {
		return nil, fmt.Errorf("this filter needs %d SigNoz queries (it contains OR); a single query was required", len(terms))
	}
	return terms[0], nil
}

// signozResponseSnippet returns a truncated JSON rendering of a relay response
// for logging / error context. Signoz payloads can be large, so it is capped.
func signozResponseSnippet(resp map[string]any) string {
	b, err := common.MarshalJson(resp)
	if err != nil {
		return "<unserializable response>"
	}
	const max = 1500
	if len(b) > max {
		// Back up to a UTF-8 rune boundary so we never slice a multi-byte
		// character in half and emit malformed UTF-8.
		i := max
		for i > 0 && !utf8.RuneStart(b[i]) {
			i--
		}
		return string(b[:i]) + "…(truncated)"
	}
	return string(b)
}

// extractSignozError pulls an explicit error message out of a Signoz/relay
// response layer, if present. Signoz reports failures via an "error" field, an
// "errorType", or a status marker rather than a well-formed result payload;
// surfacing it beats masking it as a generic "field not found".
func extractSignozError(m map[string]any) string {
	if m == nil {
		return ""
	}
	if v, ok := m["error"]; ok && v != nil {
		if s, ok := v.(string); ok {
			if s != "" {
				return s
			}
		} else {
			return fmt.Sprintf("%v", v)
		}
	}
	if v, ok := m["errorType"]; ok && v != nil {
		if s := fmt.Sprintf("%v", v); s != "" && s != "<nil>" {
			return s
		}
	}
	if st, ok := m["status"].(string); ok && strings.EqualFold(st, "error") {
		if msg, ok := m["message"].(string); ok && msg != "" {
			return msg
		}
		return "status=error"
	}
	return ""
}

func (s *SignozSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	list, err := signozFetchLogRows(fetchLogRequest, func(body map[string]any) ([]any, error) {
		resp, err := relay.Execute(relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				AccountID:    fetchLogRequest.AccountId,
				ActionName:   "signoz_query_range",
				ActionParams: body,
				NoSinks:      true,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to execute signoz query: %w", err)
		}
		data1, ok := resp["data"].(map[string]any)
		if !ok || data1 == nil {
			ctx.GetLogger().Error("signoz: query_range response missing 'data'", "response", signozResponseSnippet(resp))
			return nil, fmt.Errorf("signoz query_range: 'data' missing from relay response: %s", signozResponseSnippet(resp))
		}
		if errMsg := extractSignozError(data1); errMsg != "" {
			return nil, fmt.Errorf("signoz query_range returned error: %s", errMsg)
		}
		data2, ok := data1["data"].(map[string]any)
		if !ok || data2 == nil {
			ctx.GetLogger().Error("signoz: query_range response missing 'data.data'", "response", signozResponseSnippet(resp))
			return nil, fmt.Errorf("signoz query_range: 'data.data' missing from relay response: %s", signozResponseSnippet(resp))
		}
		if errMsg := extractSignozError(data2); errMsg != "" {
			return nil, fmt.Errorf("signoz query_range returned error: %s", errMsg)
		}
		// signoz_query_range nests the rows at data.data.result (envelope→data→result),
		// not data.data.data.result — read straight from data2.
		result, ok := data2["result"].([]any)
		if !ok || result == nil {
			// Well-formed envelope but no result payload and no explicit error.
			// Surface the actual body so a malformed query or an unexpected Signoz
			// shape is diagnosable instead of silently degrading to the fallback.
			ctx.GetLogger().Error("signoz: query_range response missing 'data.data.result'", "response", signozResponseSnippet(resp))
			return nil, fmt.Errorf("signoz query_range: unexpected response shape (no result payload): %s", signozResponseSnippet(resp))
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return []OutputLog{}, nil
	}

	data, err := common.MarshalJson(list)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}
	var signozLogResp []SigNozLog
	if err := json.Unmarshal([]byte(data), &signozLogResp); err != nil {
		ctx.GetLogger().Error("Error unmarshaling Signoz logs response:", "err", err)
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}
	outputLog := s.convertSigNozLogs(signozLogResp)
	return outputLog, nil
}

func (s *SignozSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	signozLogRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "signoz_label_suggest",
		ActionParams: map[string]any{
			"aggregateAttribute": "",
			"searchText":         "",
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozLogRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels: %w", err)
	}

	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels data1 field not found or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels data2 field not found or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
	}
	result, ok := data2["attributeKeys"].([]any)
	if !ok || result == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels attributeKeys field is not an array or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
	}
	if len(result) == 0 {
		return []OutputLogLabel{}, nil
	}
	data, err := common.MarshalJson(result)
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabels failed to marshal json: %w", err)
	}
	logs, err := s.convertSigNozLogLabels(string(data))
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabels failed to convert Signoz log label to nudgebee json: %w", err)
	}
	return logs, nil
}

func (s *SignozSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	attributeKey, tagType := signozLabelValueParams(fetchLogRequest.LabelName, fetchLogRequest.Request["tagType"])
	signozLogRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "signoz_value_suggest",
		ActionParams: map[string]any{
			"attributeKey":               attributeKey,
			"filterAttributeKeyDataType": fetchLogRequest.Request["filterAttributeKeyDataType"],
			"searchText":                 fetchLogRequest.Request["searchText"],
			"tagType":                    tagType,
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozLogRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels value: %w", err)
	}

	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues data1 field not found or is nil from response")
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues data2 field not found or is nil from response")
	}
	// Values are at data.data.<type>AttributeValues (envelope→data→values), not
	// nested one level deeper — read straight from data2.
	var result []any
	var ok1 bool

	switch fetchLogRequest.Request["filterAttributeKeyDataType"] {
	case "string":
		result, ok1 = data2["stringAttributeValues"].([]any)
	case "number":
		result, ok1 = data2["numberAttributeValues"].([]any)
	case "bool":
		result, ok1 = data2["boolAttributeValues"].([]any)
	default:
		return nil, fmt.Errorf("logs.FetchLogLabelValues unknown data type: %v", fetchLogRequest.Request["filterAttributeKeyDataType"])
	}

	if !ok1 || result == nil {
		return []OutputLogLabelValue{}, nil
	}

	if len(result) == 0 {
		return []OutputLogLabelValue{}, nil
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues failed to marshal json: %w", err)
	}
	logs, err := ConvertSigNozLogLabelValues(data)
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues failed to convert Signoz log label value to nudgebee json: %w", err)
	}
	return logs, nil
}

// SigNoz log structure
type SigNozLog struct {
	Timestamp string `json:"timestamp"`
	Data      struct {
		AttributesBool    map[string]bool    `json:"attributes_bool"`
		AttributesFloat64 map[string]float64 `json:"attributes_float64"`
		AttributesInt64   map[string]int64   `json:"attributes_int64"`
		AttributesString  map[string]string  `json:"attributes_string"`
		Body              string             `json:"body"`
		ID                string             `json:"id"`
		ResourcesString   map[string]string  `json:"resources_string"`
		SeverityNumber    int                `json:"severity_number"`
		SeverityText      string             `json:"severity_text"`
		SpanID            string             `json:"span_id"`
		TraceFlags        int                `json:"trace_flags"`
		TraceID           string             `json:"trace_id"`
	} `json:"data"`
}

// Map SigNoz severity to standard severity levels
func (s *SignozSource) mapSeverity(severityText string, severityNumber int) string {
	switch strings.ToLower(severityText) {
	case "trace", "debug":
		return "debug"
	case "information", "info":
		return "info"
	case "warning", "warn":
		return "warn"
	case "error":
		return "error"
	case "fatal", "critical":
		return "fatal"
	default:
		// Fallback to severity number mapping
		switch {
		case severityNumber <= 8: // TRACE and DEBUG
			return "debug"
		case severityNumber <= 12: // INFO
			return "info"
		case severityNumber <= 16: // WARN
			return "warn"
		case severityNumber <= 20: // ERROR
			return "error"
		default: // FATAL and above
			return "fatal"
		}
	}
}

// Convert SigNoz logs to output format
func (s *SignozSource) convertSigNozLogs(sigNozLogs []SigNozLog) []OutputLog {
	var outputLogs []OutputLog

	for _, sigNozLog := range sigNozLogs {
		// Create streams_tags from various attributes
		streamsTags := make(map[string]string)

		// Add string attributes to streams_tags
		for key, value := range sigNozLog.Data.AttributesString {
			streamsTags[key] = value
		}
		for key, value := range sigNozLog.Data.AttributesBool {
			streamsTags[key] = strconv.FormatBool(value)
		}
		for key, value := range sigNozLog.Data.AttributesFloat64 {
			streamsTags[key] = strconv.FormatFloat(value, 'f', -1, 64)
		}
		for key, value := range sigNozLog.Data.AttributesInt64 {
			streamsTags[key] = strconv.FormatInt(value, 10)
		}

		// Add resource strings to streams_tags (optional, you can customize this)
		for key, value := range sigNozLog.Data.ResourcesString {
			streamsTags[key] = value
		}

		// Add trace information to streams_tags
		if sigNozLog.Data.TraceID != "" {
			streamsTags["trace_id"] = sigNozLog.Data.TraceID
		}
		if sigNozLog.Data.SpanID != "" {
			streamsTags["span_id"] = sigNozLog.Data.SpanID
		}

		labels := make(map[string]interface{})
		for k, v := range streamsTags {
			labels[k] = v
		}

		outputLog := OutputLog{
			Timestamp: sigNozLog.Timestamp,
			Message:   sigNozLog.Data.Body,
			Labels:    labels,
			Severity:  s.mapSeverity(sigNozLog.Data.SeverityText, sigNozLog.Data.SeverityNumber),
		}

		outputLogs = append(outputLogs, outputLog)
	}

	return outputLogs
}

// Input structure
type Input struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
	IsJSON   bool   `json:"isJSON"`
}

// Transform function
func (s *SignozSource) convertSigNozLogLabels(inputJSON string) ([]OutputLogLabel, error) {
	var inputs []Input
	if err := json.Unmarshal([]byte(inputJSON), &inputs); err != nil {
		return nil, err
	}
	var outputs []OutputLogLabel
	for _, in := range inputs {
		out := OutputLogLabel{
			Label: in.Key,
			Attributes: map[string]interface{}{
				"dataType": in.DataType,
				"type":     in.Type,
				"isColumn": in.IsColumn,
				"isJSON":   in.IsJSON,
			},
		}
		outputs = append(outputs, out)
	}
	return outputs, nil
}

func ConvertSigNozLogLabelValues(inputJSON []byte) ([]OutputLogLabelValue, error) {
	var values []string
	if err := json.Unmarshal(inputJSON, &values); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	var output []OutputLogLabelValue
	for _, l := range values {
		output = append(output, OutputLogLabelValue{
			Value:      l,
			Attributes: make(map[string]interface{}), // empty map for now
		})
	}

	return output, nil
}

func (s *SignozSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return "", nil
}

// QueryLogGroup implements LogGroupSource for Signoz (agent).
// Uses the Signoz builder query with count aggregation and groupBy to group error logs.
func (s *SignozSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	filters := s.buildLogGroupFilters(req)

	signozRequest := relay.ActionExecuteBody{
		AccountID:  req.AccountId,
		ActionName: "signoz_query_range",
		ActionParams: map[string]any{
			"start":     req.StartTime,
			"end":       req.EndTime,
			"step":      signozLogGroupStepSeconds,
			"variables": map[string]any{},
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "graph",
				"fillGaps":  false,
				"builderQueries": map[string]any{
					"A": map[string]any{
						"dataSource":         "logs",
						"queryName":          "A",
						"aggregateOperator":  "count",
						"aggregateAttribute": map[string]any{},
						"timeAggregation":    "rate",
						"spaceAggregation":   "sum",
						"functions":          []any{},
						"filters": map[string]any{
							"op":    "AND",
							"items": filters,
						},
						"expression":   "A",
						"disabled":     false,
						"stepInterval": signozLogGroupStepSeconds,
						"having":       []any{},
						"limit":        100,
						"orderBy": []map[string]any{
							{"columnName": "#SIGNOZ_VALUE", "order": "desc"},
						},
						"groupBy": []map[string]any{
							{"key": "severity_text", "dataType": "string", "type": "tag", "isColumn": false},
							{"key": signozNamespaceKey, "dataType": "string", "type": "resource", "isColumn": false},
							{"key": signozPodKey, "dataType": "string", "type": "resource", "isColumn": false},
						},
						"legend":   "",
						"reduceTo": "avg",
						"offset":   0,
						"pageSize": 100,
					},
				},
			},
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozRequest,
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("signoz.QueryLogGroup: failed to execute query: %w", err)
	}

	return s.parseSignozLogGroupResponse(resp, req.EndTime)
}

// buildLogGroupFilters builds Signoz filter items for error/critical log grouping.
func (s *SignozSource) buildLogGroupFilters(req FetchLogGroupRequest) []map[string]any {
	filters := []map[string]any{
		{
			"key":   map[string]string{"key": "severity_text"},
			"value": signozErrorSeverities(),
			"op":    "in",
		},
	}

	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	if selectedNamespace != "" {
		filters = append(filters, map[string]any{
			"key":   signozFilterKey(signozNamespaceKey),
			"value": selectedNamespace,
			"op":    "=",
		})
	}
	if selectedWorkload != "" {
		filters = append(filters, map[string]any{
			"key":   signozFilterKey(signozPodKey),
			"value": selectedWorkload,
			"op":    "contains",
		})
	}

	return filters
}

// parseSignozLogGroupResponse parses the Signoz query_range response for aggregation queries.
func (s *SignozSource) parseSignozLogGroupResponse(resp map[string]any, endTimeMs int64) (LogGroupOutput, error) {
	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		return LogGroupOutput{}, nil
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		return LogGroupOutput{}, nil
	}
	if errMsg, exists := data2["error"]; exists {
		return LogGroupOutput{}, fmt.Errorf("signoz.QueryLogGroup: API error: %v", errMsg)
	}
	result, ok := data2["result"].([]any)
	if !ok || len(result) == 0 {
		return LogGroupOutput{}, nil
	}

	// For aggregation queries, result contains series with queryName, series data
	firstMap, ok := result[0].(map[string]any)
	if !ok {
		return LogGroupOutput{}, nil
	}

	series, ok := firstMap["series"].([]any)
	if !ok || series == nil {
		return LogGroupOutput{}, nil
	}

	groups := make([]LogGroup, 0, len(series))
	for _, sr := range series {
		seriesMap, ok := sr.(map[string]any)
		if !ok {
			continue
		}

		// Extract labels from the series
		labels, _ := seriesMap["labels"].(map[string]any)
		labelMap := make(map[string]string)
		for k, v := range labels {
			if str, ok := v.(string); ok {
				labelMap[k] = str
			}
		}

		group := LogGroup{}

		// Map Signoz label names to standard names
		if ns := firstNonEmpty(labelMap[signozNamespaceKey], labelMap[signozLegacyNamespaceKey]); ns != "" {
			group.Namespace = ns
		}
		if pod := firstNonEmpty(labelMap[signozPodKey], labelMap[signozLegacyPodKey]); pod != "" {
			group.Workload = extractWorkloadFromPodName(pod)
			group.Sample = pod // Use pod name as sample since Signoz groups by pod, not message
		}
		if level := labelMap["severity_text"]; level != "" {
			group.Level = level
		}

		if group.Namespace != "" && group.Workload != "" {
			group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
		}

		// Generate pattern_hash from workload name. Signoz groups by pod rather
		// than by message, so this is an identifier — hash it verbatim.
		if group.Workload != "" {
			group.PatternHash = generateRawHash(group.Workload)
		}

		timestamps, values := signozSeriesPoints(seriesMap, endTimeMs)

		group.Timestamps = timestamps
		group.Values = values
		var total int64
		for _, v := range values {
			total += int64(math.Round(v))
		}
		group.Count = total

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}, nil
}
