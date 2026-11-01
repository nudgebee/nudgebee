package observability

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"nudgebee/services/query"
)

// tempoTraceLabelMapping maps canonical trace fields to TraceQL attributes. Resource
// attributes carry the span's own Kubernetes identity; the callee is the OTel
// net.peer.name span attribute.
var tempoTraceLabelMapping = map[string]string{
	"trace_id":                  "trace:id",
	"span_id":                   "span:id",
	"parent_id":                 "span:parentID",
	"span_name":                 "name",
	"service_name":              "resource.service.name",
	"workload_name":             "resource.service.name",
	"workload_namespace":        "resource.k8s.namespace.name",
	"destination_workload_name": "span.net.peer.name",
	"destination_name":          "span.net.peer.name",
	"http_status_code":          "span.http.status_code",
	"resource":                  "span.http.target",
	"status_code":               "status",
	"duration_ns":               "duration",
}

// tempoTraceUnsupportedFields are canonical fields a Tempo span cannot express: a span
// names its callee but carries no callee namespace or zone. Dropped rather than matched,
// for the reason Splunk and OpenObserve drop them — an unmatched field would blank the
// destination side of a caller-OR-callee evidence search.
var tempoTraceUnsupportedFields = map[string]bool{
	"destination_workload_namespace": true,
	"destination_workload_zone":      true,
	"headers":                        true, // the Traces tab's "Search By Headers": spans carry no headers
}

// tempoTraceIntrinsics are the TraceQL intrinsics a where clause may name directly.
var tempoTraceIntrinsics = map[string]bool{
	"name": true, "status": true, "kind": true, "duration": true,
	"trace:id": true, "span:id": true, "span:parentID": true,
}

// tempoSpanKinds are the TraceQL kind enum values; the UI sends SPAN_KIND_SERVER etc.
var tempoSpanKinds = map[string]bool{
	"unspecified": true, "internal": true, "server": true, "client": true, "producer": true, "consumer": true,
}

var tempoNumericAttrs = map[string]bool{"span.http.status_code": true, "duration": true}

// Scoped attribute names (as QueryLabels returns them) pass through unchanged.
var tempoAttrPattern = regexp.MustCompile(`^(resource|span)\.[A-Za-z0-9_.\-]+$`)

var tempoSupportedOperators = []string{
	string(query.Eq), string(query.Nq), string(query.In), string(query.NotIn),
	string(query.Contains), string(query.IContains), string(query.NIContains),
	string(query.Like), string(query.ILike), string(query.NLike),
	string(query.Regex), string(query.NRegex),
	string(query.Gt), string(query.Gte), string(query.Lt), string(query.Lte),
}

// tempoSelectClause asks search to return the attributes the listing renders; without
// select() Tempo returns only span id, start and duration — Tempo 3.0.3 omits even the
// span name unless `name` is selected.
const tempoSelectClause = "select(resource.service.name, resource.k8s.namespace.name, resource.k8s.pod.name, " +
	"span.net.peer.name, span.peer.service, span.http.status_code, span.http.target, span.url.path, " +
	"status, kind, span:parentID, name)"

// tempoGroupBy has four keys on purpose: Tempo rejects five group-by values once the
// quantile's own `p` label is added ("metrics group by 5 values not yet supported").
const tempoGroupBy = "by (resource.service.name, resource.k8s.namespace.name, name, span.net.peer.name)"

// tempoAttrFor resolves a where-clause field to its TraceQL attribute. service.go
// rewrites every clause through the label mapping before calling the source, so a field
// usually arrives already mapped (`name`, `status`, `trace:id`); canonical names are
// still accepted for callers that skip that step.
func tempoAttrFor(label string) (string, error) {
	if attr, ok := tempoTraceLabelMapping[label]; ok {
		return attr, nil
	}
	if tempoTraceIntrinsics[label] || tempoAttrPattern.MatchString(label) {
		return label, nil
	}
	return "", fmt.Errorf("grafana tempo: unsupported trace field %q", label)
}

// tempoStatusLiteral turns the UI's STATUS_CODE_ERROR (or a bare ERROR/error) into the
// TraceQL status enum, which is unquoted: `status = error`.
func tempoStatusLiteral(v string) (string, error) {
	s := strings.ToLower(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "STATUS_CODE_"))
	switch s {
	case "error", "ok", "unset":
		return s, nil
	}
	return "", fmt.Errorf("grafana tempo: unknown span status %q (want ok, error or unset)", v)
}

func tempoLiteral(attr, v string) string {
	if attr == "kind" {
		if k := strings.ToLower(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "SPAN_KIND_")); tempoSpanKinds[k] {
			return k
		}
	}
	if tempoNumericAttrs[attr] {
		// An empty value on a numeric attribute means "absent": the UI sends
		// http_status_code _neq '' next to its _eq, and `!= ""` matches no span.
		if strings.TrimSpace(v) == "" {
			return "nil"
		}
		if _, err := strconv.ParseInt(v, 10, 64); err == nil {
			if attr == "duration" {
				return v + "ns"
			}
			return v
		}
	}
	return strconv.Quote(v)
}

func tempoJoin(parts []string, sep string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, sep) + ")"
}

func tempoStatusCondition(op query.BinaryWhereClauseType, values []string) (string, error) {
	cmp, sep := "=", " || "
	switch op {
	case query.Eq, query.In, query.Contains, query.IContains, query.Like, query.ILike:
	case query.Nq, query.NotIn, query.NIContains, query.NLike:
		cmp, sep = "!=", " && "
	default:
		return "", fmt.Errorf("grafana tempo: unsupported operator %q for field %q", op, "status")
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		lit, err := tempoStatusLiteral(v)
		if err != nil {
			return "", err
		}
		parts = append(parts, "status "+cmp+" "+lit)
	}
	return tempoJoin(parts, sep), nil
}

func tempoCondition(attr string, op query.BinaryWhereClauseType, raw any) (string, error) {
	values := cubeAPMFilterValues(raw)
	if len(values) == 0 {
		return "", nil
	}
	if attr == "status" {
		return tempoStatusCondition(op, values)
	}
	plain := func(v string) string { return tempoLiteral(attr, v) }
	contains := func(v string) string { return strconv.Quote("(?i).*" + regexp.QuoteMeta(v) + ".*") }
	like := func(v string) string {
		return strconv.Quote("(?i)" + strings.ReplaceAll(regexp.QuoteMeta(v), "%", ".*"))
	}
	rx := func(v string) string { return strconv.Quote(v) }
	each := func(cmp string, lit func(string) string, sep string) string {
		parts := make([]string, 0, len(values))
		for _, v := range values {
			parts = append(parts, attr+" "+cmp+" "+lit(v))
		}
		return tempoJoin(parts, sep)
	}
	switch op {
	case query.Eq, query.In:
		return each("=", plain, " || "), nil
	case query.Nq, query.NotIn:
		return each("!=", plain, " && "), nil
	case query.Contains, query.IContains:
		return each("=~", contains, " || "), nil
	case query.NIContains:
		return each("!~", contains, " && "), nil
	case query.Like, query.ILike:
		return each("=~", like, " || "), nil
	case query.NLike:
		return each("!~", like, " && "), nil
	case query.Regex:
		return each("=~", rx, " || "), nil
	case query.NRegex:
		return each("!~", rx, " && "), nil
	case query.Gt:
		return each(">", plain, " || "), nil
	case query.Gte:
		return each(">=", plain, " || "), nil
	case query.Lt:
		return each("<", plain, " || "), nil
	case query.Lte:
		return each("<=", plain, " || "), nil
	}
	return "", fmt.Errorf("grafana tempo: unsupported operator %q for field %q", op, attr)
}

// tempoWhereExpr renders a where clause as the body of one TraceQL span selector.
// Binary predicates are rendered in sorted label/operator order so the query is stable.
func tempoWhereExpr(w query.QueryWhereClause) (string, error) {
	var parts []string

	// trace_source never reaches here: Tempo declares TraceSource "otel" in
	// allProviderCaps, so the dispatch layer evaluates it.
	labels := make([]string, 0, len(w.Binary))
	for label := range w.Binary {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		if tempoTraceUnsupportedFields[label] {
			continue
		}
		attr, err := tempoAttrFor(label)
		if err != nil {
			return "", err
		}
		ops := make([]string, 0, len(w.Binary[label]))
		for op := range w.Binary[label] {
			ops = append(ops, string(op))
		}
		sort.Strings(ops)
		for _, op := range ops {
			cond, err := tempoCondition(attr, query.BinaryWhereClauseType(op), w.Binary[label][query.BinaryWhereClauseType(op)])
			if err != nil {
				return "", err
			}
			if cond != "" {
				parts = append(parts, cond)
			}
		}
	}
	for _, sub := range w.And {
		e, err := tempoWhereExpr(sub)
		if err != nil {
			return "", err
		}
		if e != "" {
			parts = append(parts, "("+e+")")
		}
	}
	if len(w.Or) > 0 {
		ors := make([]string, 0, len(w.Or))
		for _, sub := range w.Or {
			e, err := tempoWhereExpr(sub)
			if err != nil {
				return "", err
			}
			if e != "" {
				ors = append(ors, e)
			}
		}
		if len(ors) > 0 {
			parts = append(parts, "("+strings.Join(ors, " || ")+")")
		}
	}
	if w.Not != nil {
		return "", fmt.Errorf("grafana tempo: negated filter groups are not supported")
	}
	return strings.Join(parts, " && "), nil
}

func tempoSelector(w query.QueryWhereClause) (string, error) {
	cond, err := tempoWhereExpr(w)
	if err != nil {
		return "", err
	}
	if cond == "" {
		return "{}", nil
	}
	return "{ " + cond + " }", nil
}

// tempoTrailingWith matches a query hint clause at the end of a TraceQL query.
var tempoTrailingWith = regexp.MustCompile(`\s*(with\s*\([^()]*\))\s*$`)

// tempoSearchQuery builds the /api/search query. A raw Code-mode query is used as the
// selector; the standard select() and newest-first hint are appended only when absent,
// so a user who wrote their own is not overridden.
func tempoSearchQuery(raw string, w query.QueryWhereClause) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		sel, err := tempoSelector(w)
		if err != nil {
			return "", err
		}
		q = sel
	}
	if !strings.Contains(q, "select(") {
		// A pipeline stage must come before a trailing `with (...)` hint.
		if m := tempoTrailingWith.FindStringSubmatchIndex(q); m != nil {
			q = strings.TrimRight(q[:m[0]], " ") + " | " + tempoSelectClause + " " + q[m[2]:m[3]]
		} else {
			q += " | " + tempoSelectClause
		}
	}
	if !strings.Contains(q, "most_recent") {
		q += " with (most_recent=true)"
	}
	return q, nil
}

type tempoGroupQueries struct {
	Count, Errors, Quantiles, Max string
}

// buildTempoGroupQueries returns the four instant TraceQL-metrics queries behind the
// grouped view. Grouping needs builder filters: a raw query may already carry a pipeline
// that cannot be combined with `&& status = error`.
func buildTempoGroupQueries(raw string, w query.QueryWhereClause) (tempoGroupQueries, error) {
	if strings.TrimSpace(raw) != "" {
		return tempoGroupQueries{}, fmt.Errorf("grafana tempo: the grouped view uses the filter builder; raw TraceQL is supported in the span listing only")
	}
	cond, err := tempoWhereExpr(w)
	if err != nil {
		return tempoGroupQueries{}, err
	}
	all, errs := "{}", "{ status = error }"
	if cond != "" {
		all = "{ " + cond + " }"
		errs = "{ (" + cond + ") && status = error }"
	}
	return tempoGroupQueries{
		Count:     all + " | count_over_time() " + tempoGroupBy,
		Errors:    errs + " | count_over_time() " + tempoGroupBy,
		Quantiles: all + " | quantile_over_time(duration, .95, .99) " + tempoGroupBy,
		Max:       all + " | max_over_time(duration) " + tempoGroupBy,
	}, nil
}
