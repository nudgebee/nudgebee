package observability

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/query"
)

// swFoldTimeRange moves time conditions out of the filter and into the request window.
//
// SolarWinds log search has no range operators — "time:>…" is answered with HTTP 500 —
// but callers, nubi's logs agent among them, express a window as timestamp/time _gte/_lte
// conditions. A time range at the top level of the filter means exactly the window, so it
// narrows StartTime/EndTime (never widens them) and is dropped from the filter. One nested
// under _or/_not cannot be a window and is left for the builder to refuse.
func swFoldTimeRange(req FetchLogRequest) (FetchLogRequest, error) {
	w, err := swFoldTimeClause(req.QueryRequest.Where, &req)
	if err != nil {
		return req, err
	}
	req.QueryRequest.Where = w
	if req.StartTime > 0 && req.EndTime > 0 && req.StartTime >= req.EndTime {
		return req, fmt.Errorf("the time conditions leave an empty window (%s to %s)",
			swMsToISO8601(req.StartTime), swMsToISO8601(req.EndTime))
	}
	return req, nil
}

func swFoldTimeClause(w query.QueryWhereClause, req *FetchLogRequest) (query.QueryWhereClause, error) {
	if len(w.Binary) > 0 {
		kept := query.BinaryWhereClause{}
		for field, ops := range w.Binary {
			if !swIsTimeField(field) {
				kept[field] = ops
				continue
			}
			for op, val := range ops {
				if err := swNarrowWindow(req, op, val); err != nil {
					return w, err
				}
			}
		}
		w.Binary = kept
	}
	if len(w.And) > 0 {
		and := make([]query.QueryWhereClause, 0, len(w.And))
		for _, c := range w.And {
			folded, err := swFoldTimeClause(c, req)
			if err != nil {
				return w, err
			}
			if len(folded.Binary) > 0 || len(folded.And) > 0 || len(folded.Or) > 0 || folded.Not != nil {
				and = append(and, folded)
			}
		}
		w.And = and
	}
	return w, nil
}

func swIsTimeField(field string) bool {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "time", "timestamp", "@timestamp":
		return true
	}
	return false
}

func swNarrowWindow(req *FetchLogRequest, op query.BinaryWhereClauseType, val any) error {
	switch op {
	case query.Gt, query.Gte:
		ms, err := swTimeValueMs(val)
		if err != nil {
			return err
		}
		if req.StartTime == 0 || ms > req.StartTime {
			req.StartTime = ms
		}
	case query.Lt, query.Lte:
		ms, err := swTimeValueMs(val)
		if err != nil {
			return err
		}
		if req.EndTime == 0 || ms < req.EndTime {
			req.EndTime = ms
		}
	case query.Between:
		bounds, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("time %s needs {_gte, _lte} bounds", op)
		}
		for k, v := range bounds {
			if err := swNarrowWindow(req, query.BinaryWhereClauseType(k), v); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("SolarWinds log search cannot filter time with %q; set the time range instead", op)
	}
	return nil
}

// swTimeValueMs reads a time bound given as RFC3339 text or as epoch seconds, ms, µs or ns.
func swTimeValueMs(v any) (int64, error) {
	var n float64
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return ts.UnixMilli(), nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("time bound %q is neither RFC3339 nor an epoch number", t)
		}
		n = f
	case float64:
		n = t
	case int64:
		n = float64(t)
	case int:
		n = float64(t)
	default:
		return 0, fmt.Errorf("time bound %v is neither RFC3339 nor an epoch number", v)
	}
	switch a := math.Abs(n); {
	case a >= 1e17:
		return int64(n / 1e6), nil // ns
	case a >= 1e14:
		return int64(n / 1e3), nil // µs
	case a >= 1e11:
		return int64(n), nil // ms
	default:
		return int64(n * 1e3), nil // s
	}
}
