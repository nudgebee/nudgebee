package observability

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultESTraceQuerySize bounds a raw trace search that states no size of its own. The
// three trace builders (buildTraceQuery, buildOtelTraceQuery, buildAPMTraceQuery) all
// default to 100, so a raw query is bounded the same way rather than inheriting the logs
// path's defaultESLogQuerySize of 1000.
const defaultESTraceQuerySize = 100

// esTraceRawQueryType returns the language a raw trace query is written in, read from the
// request's free-form overrides exactly as the logs path reads it (see
// ElasticSaasSource.QueryLogsDetailed). "dsl" — a raw Elasticsearch search body — is the
// default. PPL is not offered for traces: it answers with a flat column/row table, not
// _search hits, so none of the span parsers can read it.
func esTraceRawQueryType(request map[string]any) string {
	if request == nil {
		return "dsl"
	}
	qt, _ := request["query_type"].(string)
	if qt = strings.ToLower(strings.TrimSpace(qt)); qt == "" {
		return "dsl"
	}
	return qt
}

// esRawTraceQueryClause returns a raw trace query as a single Elasticsearch filter clause,
// for the operations that own the rest of the body and so cannot simply be handed the
// caller's whole one — the grouped views, which supply their own composite aggregation.
//
// The caller's query takes the place of the where clause in that operation's filter list,
// while the operation keeps its own time bound and aggregations. So a raw query narrows a
// grouped view exactly as it narrows the span listing, and still cannot widen its window.
//
// A DSL body with no "query" key degrades to match_all rather than erroring: that body is a
// legitimate "everything in the window", and the operation's own filters still apply.
func esRawTraceQueryClause(raw, queryType string) (map[string]any, error) {
	switch queryType {
	case "dsl":
		body := map[string]any{}
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				return nil, fmt.Errorf("failed to parse raw trace query as an elasticsearch dsl body: %w", err)
			}
		}
		// An empty query object is NOT a usable clause: Elasticsearch rejects `{}` inside
		// bool.filter with "[bool] failed to parse field [filter]" (verified against a
		// live cluster), so `{"query":{}}` would 400 rather than match everything. Treat
		// it the same as a body with no query at all.
		if q, ok := body["query"].(map[string]any); ok && len(q) > 0 {
			return q, nil
		}
		return map[string]any{"match_all": map[string]any{}}, nil
	case "kql":
		return kqlToDSL(raw)
	default:
		return nil, fmt.Errorf("unsupported trace query_type %q: a raw elasticsearch trace query must be %q or %q", queryType, "dsl", "kql")
	}
}

// esTraceFilters returns the filter list an aggregation-owning operation applies: that
// operation's own time bound, plus either the caller's raw query or the canonical where
// clause. One place, so the grouped views and the span listing agree on precedence.
func esTraceFilters(req TracesV3Request, timeFilter map[string]any, labelMapping map[string]string) ([]map[string]any, error) {
	filters := []map[string]any{}
	if timeFilter != nil {
		filters = append(filters, timeFilter)
	}
	if hasRawTraceQuery(req) {
		clause, err := esRawTraceQueryClause(req.Query, esTraceRawQueryType(req.Request))
		if err != nil {
			return nil, err
		}
		return append(filters, clause), nil
	}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, labelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		filters = append(filters, whereFilters...)
	}
	return filters, nil
}

// finalizeESTraceQueryBody turns a caller-supplied raw trace query into a complete _search
// body for one of the three Elasticsearch trace layouts.
//
// This deliberately does NOT reuse the logs path's finalizeESLogQueryBody. That helper
// hard-codes @timestamp in both its range clause (esLogTimeRangeClause) and its default
// sort. Data Prepper indices have no @timestamp — their time field is startTime — so a raw
// trace query routed through it would range on a missing field (zero hits, always) and then
// fail the search outright on an unmapped sort field. Its 1000-row default and []SortField
// signature are log-shaped too. Only the parse-and-merge skeleton is genuinely common, and
// it lives here, parameterised by each reader's own time clause and default sort.
//
// The time filter is AND-ed around the caller's query rather than merged into it, so a raw
// query can never widen the window it was asked for. size / from / sort are defaults only:
// a raw query that states its own keeps them.
func finalizeESTraceQueryBody(raw, queryType string, timeFilter map[string]any, defaultSort []map[string]any, limit, offset int) (map[string]any, error) {
	body := map[string]any{}

	switch queryType {
	case "dsl":
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				return nil, fmt.Errorf("failed to parse raw trace query as an elasticsearch dsl body: %w", err)
			}
			if body == nil {
				body = map[string]any{}
			}
		}
	case "kql":
		inner, err := kqlToDSL(raw)
		if err != nil {
			return nil, err
		}
		body["query"] = inner
	default:
		return nil, fmt.Errorf("unsupported trace query_type %q: a raw elasticsearch trace query must be %q or %q", queryType, "dsl", "kql")
	}

	if timeFilter != nil {
		// A missing OR EMPTY query becomes match_all: Elasticsearch rejects `{}` inside
		// bool.filter outright (see esRawTraceQueryClause), so a body like {"query":{}}
		// would 400 instead of matching the window.
		userQuery, ok := body["query"].(map[string]any)
		if !ok || len(userQuery) == 0 {
			userQuery = map[string]any{"match_all": map[string]any{}}
		}
		body["query"] = map[string]any{
			"bool": map[string]any{"filter": []any{userQuery, timeFilter}},
		}
	}

	if _, ok := body["size"]; !ok {
		if limit > 0 {
			body["size"] = limit
		} else {
			body["size"] = defaultESTraceQuerySize
		}
	}
	if _, ok := body["from"]; !ok && offset > 0 {
		body["from"] = offset
	}
	if _, ok := body["sort"]; !ok && len(defaultSort) > 0 {
		body["sort"] = defaultSort
	}
	return body, nil
}
