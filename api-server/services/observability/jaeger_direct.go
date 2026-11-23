package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

const (
	jaegerDirectTimeout = 30 * time.Second
	jaegerDirectMaxBody = 64 << 20
)

// jaegerDirectExec carries one Jaeger primitive action out against the Jaeger query API
// the account connected, instead of through the agent. The answer has the relay's shape
// — {"status_code": 200, "data": <Jaeger JSON>} — so the shared parsing needs no second
// path.
func jaegerDirectExec(ctx *security.RequestContext, body relay.ActionExecuteBody) (map[string]any, error) {
	cfg, err := integrations.GetJaegerConfig(ctx, body.AccountID)
	if err != nil {
		return nil, err
	}
	return jaegerDirectGet(cfg, body)
}

// jaegerDirectGet is jaegerDirectExec with the connection already resolved.
func jaegerDirectGet(cfg integrations.JaegerConfig, body relay.ActionExecuteBody) (map[string]any, error) {
	path, params, err := jaegerActionRequest(body.ActionName, body.ActionParams)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{"Accept": "application/json"}
	if cfg.Token != "" {
		headers["Authorization"] = "Bearer " + cfg.Token
	}
	target := cfg.URL + path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	resp, err := common.HttpGet(target, common.HttpWithHeaders(headers), common.HttpWithTimeout(jaegerDirectTimeout))
	if err != nil {
		return nil, fmt.Errorf("jaeger: %s request failed: %w", body.ActionName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, jaegerDirectMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("jaeger: reading %s response: %w", body.ActionName, err)
	}
	if len(raw) > jaegerDirectMaxBody {
		return nil, fmt.Errorf("jaeger: %s response is larger than %dMB; narrow the time range or filters", body.ActionName, jaegerDirectMaxBody>>20)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("jaeger: %s returned HTTP %d: %s", body.ActionName, resp.StatusCode, msg)
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("jaeger: %s returned a body that is not JSON: %w", body.ActionName, err)
	}
	return map[string]any{"status_code": float64(200), "data": parsed}, nil
}

// jaegerActionRequest maps a Jaeger primitive action onto the query API's path and query
// string — the same mapping the agent's Jaeger client applies.
func jaegerActionRequest(action string, p map[string]any) (string, url.Values, error) {
	switch action {
	case "jaeger_query_services":
		return "/api/services", nil, nil
	case "jaeger_query_traces":
		return "/api/traces", jaegerQueryValues(p), nil
	case "jaeger_query_trace_by_id":
		id, _ := p["trace_id"].(string)
		if id == "" {
			return "", nil, fmt.Errorf("jaeger: trace id required")
		}
		return "/api/traces/" + url.PathEscape(id), nil, nil
	case "jaeger_query_operations":
		svc, _ := p["service"].(string)
		if svc == "" {
			return "", nil, fmt.Errorf("jaeger: service required")
		}
		return "/api/services/" + url.PathEscape(svc) + "/operations", nil, nil
	case "jaeger_query_metrics":
		metricType, _ := p["metric_type"].(string)
		if metricType == "" {
			return "", nil, fmt.Errorf("jaeger: metric_type required")
		}
		rest := map[string]any{}
		for k, v := range p {
			if k != "metric_type" {
				rest[k] = v
			}
		}
		return "/api/metrics/" + url.PathEscape(metricType), jaegerQueryValues(rest), nil
	default:
		return "", nil, fmt.Errorf("jaeger: action %q is not a Jaeger query", action)
	}
}

// jaegerQueryValues renders action params as a query string. Jaeger takes `tags` as one
// JSON object, so a tag map is encoded rather than dropped.
func jaegerQueryValues(p map[string]any) url.Values {
	v := url.Values{}
	for k, val := range p {
		switch x := val.(type) {
		case string:
			if x != "" {
				v.Set(k, x)
			}
		case []string:
			for _, s := range x {
				v.Add(k, s)
			}
		case []any:
			for _, s := range x {
				if str, ok := s.(string); ok {
					v.Add(k, str)
				}
			}
		case int:
			v.Set(k, strconv.Itoa(x))
		case int64:
			v.Set(k, strconv.FormatInt(x, 10))
		case float64:
			v.Set(k, strconv.FormatFloat(x, 'f', -1, 64))
		case bool:
			v.Set(k, strconv.FormatBool(x))
		case map[string]string:
			if len(x) > 0 {
				if b, err := json.Marshal(x); err == nil {
					v.Set(k, string(b))
				}
			}
		case map[string]any:
			if len(x) > 0 {
				if b, err := json.Marshal(x); err == nil {
					v.Set(k, string(b))
				}
			}
		}
	}
	return v
}
