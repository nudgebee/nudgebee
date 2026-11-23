package observability

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/query"
)

// canonicalQueryHint is quoted back when a canonical query is unusable, so the
// caller is told the shape instead of reading a provider's own parser error.
const canonicalQueryHint = `a canonical query is {"where": {"field": {"_eq": "value"}}, "time_range": "1h", "limit": 1000}`

// shiftCanonicalQueryToWhere moves a generated canonical query out of
// FetchLogRequest.Query and into QueryRequest, so it executes exactly like every
// other canonical query instead of being handed to a provider as a native query
// it cannot parse. The Logs page's AI and Code modes put it in Query, where the
// SigNoz source forwarded it as filter items and SigNoz answered HTTP 400.
//
// Its one caller is FetchLogs, gated on the SigNoz provider, and it runs there
// rather than in the provider because FetchLogs resolves canonical label names
// (expandCanonicalLabelKeys) and applies the account's label mapping
// (convertWhereClauseWithMApping) to QueryRequest.Where before calling a source.
// Shifting inside the source lands after both, so `app`/`namespace` would reach
// the backend unmapped and match nothing.
//
// Even for SigNoz, Query is provider-native space (an array of filter items), so
// anything that is not a JSON object carrying a "where" key is left exactly as
// it was.
func shiftCanonicalQueryToWhere(req *FetchLogRequest) error {
	if strings.TrimSpace(req.Query) == "" {
		return nil
	}

	var parsed any
	if err := common.UnmarshalJson([]byte(req.Query), &parsed); err != nil {
		return nil // Not JSON: a provider-native text query.
	}

	// COMPATIBILITY SHIM — delete once the producer is fixed.
	// app/src/components/k8s/details/KubernetesLogs.tsx:657 runs JSON.stringify
	// over `logQuery`, which is already a string, so every non-empty query the
	// Logs page sends for SigNoz arrives encoded twice. The warn shows whether
	// anything still relies on this.
	if s, ok := parsed.(string); ok {
		var inner any
		if err := common.UnmarshalJson([]byte(s), &inner); err != nil {
			return nil
		}
		slog.Warn("logs: query arrived double-encoded — unwrapping (producer bug, see KubernetesLogs.tsx:657)",
			"account_id", req.AccountId, "provider", req.LogProvider)
		parsed = inner
	}

	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil
	}
	rawWhere, ok := obj["where"]
	if !ok {
		return nil
	}
	whereMap, ok := rawWhere.(map[string]any)
	if !ok {
		return fmt.Errorf("query \"where\" is a %T, not an object: %s", rawWhere, canonicalQueryHint)
	}

	where, err := decodeCanonicalWhere(whereMap)
	if err != nil {
		return err
	}
	req.QueryRequest.Where = where
	req.Query = ""

	// The query's own window and limit win: the generator treats the window in
	// the question as a hard constraint, and the canonical executor already
	// resolves it this way (llm/llm-server/tools/tool_logs_v2.go:130).
	var canonical struct {
		TimeRange string `json:"time_range"`
		Limit     int    `json:"limit"`
	}
	if raw, err := common.MarshalJson(obj); err == nil {
		_ = common.UnmarshalJson(raw, &canonical)
	}
	if d, ok := parseCanonicalWindow(canonical.TimeRange); ok {
		end := time.Now()
		req.StartTime, req.EndTime = end.Add(-d).UnixMilli(), end.UnixMilli()
	}
	if canonical.Limit > 0 {
		req.Limit = canonical.Limit
	}
	return nil
}

// decodeCanonicalWhere turns the generator's where clause into a
// QueryWhereClause. The LLM writes a bare field map ({"app": {"_eq": "x"}}),
// which the struct cannot decode on its own — it has only
// _binary/_and/_or/_not, so a bare map would decode to a ZERO clause, mean "no
// filter", and silently widen the query to every log in the window. The bare
// entries are therefore collected under _binary first, and a clause that was
// written but decoded to nothing is an error: a wrong answer reported as
// success is worse than the 400 this replaces.
//
// Mirrors queryBuilderFixWhereClause in
// llm/llm-server/tools/core/query_builder_model.go, which cannot be imported —
// nudgebee/services and nudgebee/llm are separate Go modules. Keep the two in
// step when either learns a new shape.
func decodeCanonicalWhere(whereMap map[string]any) (query.QueryWhereClause, error) {
	expanded, err := expandWhereShorthand(whereMap)
	if err != nil {
		return query.QueryWhereClause{}, fmt.Errorf("invalid canonical where clause: %w (%s)", err, canonicalQueryHint)
	}
	raw, err := common.MarshalJson(expanded)
	if err != nil {
		return query.QueryWhereClause{}, fmt.Errorf("failed to re-encode canonical where clause: %w", err)
	}
	var where query.QueryWhereClause
	if err := common.UnmarshalJson(raw, &where); err != nil {
		return query.QueryWhereClause{}, fmt.Errorf("invalid canonical where clause: %w (%s)", err, canonicalQueryHint)
	}
	if len(whereMap) > 0 && !hasWhereData(where) {
		return query.QueryWhereClause{}, fmt.Errorf("canonical where clause matched no supported condition: %s", canonicalQueryHint)
	}
	return where, nil
}

func expandWhereShorthand(whereMap map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	binary := make(map[string]any)
	for k, v := range whereMap {
		switch k {
		case "_and", "_or":
			list, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be a list of clauses", k)
			}
			clauses := []any{}
			for _, sub := range list {
				subMap, ok := sub.(map[string]any)
				if !ok {
					continue // Skip non-clause elements, as the LLM-side copy does.
				}
				expanded, err := expandWhereShorthand(subMap)
				if err != nil {
					return nil, err
				}
				clauses = append(clauses, expanded)
			}
			out[k] = clauses
		case "_not":
			// An object, not a list: QueryWhereClause.Not is a *QueryWhereClause.
			subMap, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("_not must be a single clause object")
			}
			expanded, err := expandWhereShorthand(subMap)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		default:
			binary[k] = v
		}
	}
	out["_binary"] = binary
	return out, nil
}

// parseCanonicalWindow parses the relative windows the generator emits.
// time.ParseDuration handles h/m/s but not the d suffix.
func parseCanonicalWindow(v string) (time.Duration, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if strings.HasSuffix(v, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || days <= 0 || days > 10000 {
			return 0, false
		}
		return time.Duration(days) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}
