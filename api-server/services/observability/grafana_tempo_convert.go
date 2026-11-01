package observability

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
)

const tempoTraceSourceName = "grafana_tempo"

type tempoKV struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

// tempoValueString flattens an OTLP AnyValue ({"stringValue":…}, {"intValue":"…"},
// {"doubleValue":…}, {"boolValue":…}). Tempo reports a selected attribute that is absent
// on a span as the string "nil", which is treated as empty.
func tempoValueString(v map[string]any) string {
	for _, raw := range v {
		var s string
		switch x := raw.(type) {
		case string:
			s = x
		case float64:
			s = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			s = strconv.FormatBool(x)
		default:
			s = fmt.Sprint(x)
		}
		if s == "nil" {
			return ""
		}
		return s
	}
	return ""
}

func tempoAttrs(kvs []tempoKV) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if v := tempoValueString(kv.Value); v != "" {
			out[kv.Key] = v
		}
	}
	return out
}

func tempoStatusCode(s string) string {
	switch strings.ToLower(s) {
	case "error":
		return "STATUS_CODE_ERROR"
	case "ok":
		return "STATUS_CODE_OK"
	}
	return "STATUS_CODE_UNSET"
}

func tempoSpanKind(s string) string {
	if s == "" {
		return "SPAN_KIND_UNSPECIFIED"
	}
	return "SPAN_KIND_" + strings.ToUpper(s)
}

func tempoFirst(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type tempoSearchSpan struct {
	SpanID            string    `json:"spanID"`
	Name              string    `json:"name"`
	StartTimeUnixNano string    `json:"startTimeUnixNano"`
	DurationNanos     string    `json:"durationNanos"`
	Attributes        []tempoKV `json:"attributes"`
}

type tempoSpanSet struct {
	Spans []tempoSearchSpan `json:"spans"`
}

type tempoSearchResponse struct {
	Traces []struct {
		TraceID  string         `json:"traceID"`
		SpanSet  *tempoSpanSet  `json:"spanSet"`
		SpanSets []tempoSpanSet `json:"spanSets"`
	} `json:"traces"`
}

// Keys the converter lifts into dedicated fields; everything else stays a span attribute.
var tempoResourceKeys = []string{"service.name", "k8s.namespace.name", "k8s.pod.name"}
var tempoLiftedKeys = map[string]bool{
	"service.name": true, "k8s.namespace.name": true, "k8s.pod.name": true,
	"status": true, "kind": true, "span:parentID": true, "name": true,
}

// tempoPadID restores the leading zeros /api/search drops from hex ids (a 30-char trace
// id is common), so a search id matches the full-width id trace-by-id decodes to and the
// one logs and other providers carry.
func tempoPadID(id string, width int) string {
	if id == "" || len(id) >= width {
		return id
	}
	return strings.Repeat("0", width-len(id)) + id
}

// tempoSearchToSpans flattens /api/search into spans. Tempo returns the legacy `spanSet`
// field alongside `spanSets`, and neither is guaranteed to be a subset of the other, so
// both are combined and de-duplicated by trace+span id (spanSets first, then any extra
// spans that only appear in the legacy spanSet).
func tempoSearchToSpans(body []byte) ([]common.OpenTelemetryTrace, error) {
	var resp tempoSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding search response: %w", err)
	}
	seen := map[string]bool{}
	out := []common.OpenTelemetryTrace{}
	for _, tr := range resp.Traces {
		sets := tr.SpanSets
		if tr.SpanSet != nil {
			sets = append(sets, *tr.SpanSet)
		}
		traceID := tempoPadID(tr.TraceID, 32)
		for _, set := range sets {
			for _, sp := range set.Spans {
				key := traceID + "/" + tempoPadID(sp.SpanID, 16)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, tempoSearchSpanToTrace(traceID, sp))
			}
		}
	}
	return out, nil
}

func tempoSearchSpanToTrace(traceID string, sp tempoSearchSpan) common.OpenTelemetryTrace {
	attrs := tempoAttrs(sp.Attributes)
	start, _ := strconv.ParseInt(sp.StartTimeUnixNano, 10, 64)
	dur, _ := strconv.ParseInt(sp.DurationNanos, 10, 64)

	parent := tempoPadID(attrs["span:parentID"], 16)
	if strings.Trim(parent, "0") == "" {
		parent = ""
	}
	res := map[string]string{}
	for _, k := range tempoResourceKeys {
		if v := attrs[k]; v != "" {
			res[k] = v
		}
	}
	spanAttrs := map[string]string{}
	for k, v := range attrs {
		if !tempoLiftedKeys[k] {
			spanAttrs[k] = v
		}
	}
	dest := tempoFirst(attrs["net.peer.name"], attrs["peer.service"])
	ts := time.Unix(0, start).UTC()
	return common.OpenTelemetryTrace{
		Timestamp:           ts.Format(time.RFC3339Nano),
		TraceID:             traceID,
		SpanID:              tempoPadID(sp.SpanID, 16),
		ParentSpanID:        parent,
		SpanName:            tempoFirst(sp.Name, attrs["name"]),
		SpanKind:            tempoSpanKind(attrs["kind"]),
		ServiceName:         attrs["service.name"],
		ResourceAttributes:  res,
		SpanAttributes:      spanAttrs,
		DurationNs:          dur,
		StatusCode:          tempoStatusCode(attrs["status"]),
		WorkloadName:        attrs["service.name"],
		WorkloadNamespace:   attrs["k8s.namespace.name"],
		Resource:            tempoFirst(attrs["http.target"], attrs["url.path"]),
		DestinationName:     dest,
		DestinationWorkload: dest,
		HTTPStatusCode:      attrs["http.status_code"],
		StartTime:           ts.Format(time.RFC3339Nano),
		EndTime:             ts.Add(time.Duration(dur)).Format(time.RFC3339Nano),
		StartTimeUnixNano:   sp.StartTimeUnixNano,
		EndTimeUnixNano:     strconv.FormatInt(start+dur, 10),
		TraceSource:         tempoTraceSourceName,
	}
}

type tempoOTLPSpan struct {
	TraceID           string    `json:"traceId"`
	SpanID            string    `json:"spanId"`
	Name              string    `json:"name"`
	StartTimeUnixNano string    `json:"startTimeUnixNano"`
	EndTimeUnixNano   string    `json:"endTimeUnixNano"`
	Attributes        []tempoKV `json:"attributes"`
	Status            struct {
		Code string `json:"code"`
	} `json:"status"`
	Events []struct {
		Name       string    `json:"name"`
		Attributes []tempoKV `json:"attributes"`
	} `json:"events"`
}

type tempoOTLPResponse struct {
	Trace struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []tempoKV `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []tempoOTLPSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	} `json:"trace"`
}

// tempoOTLPID decodes the base64 ids /api/v2/traces returns into the hex form search
// and the rest of Nudgebee use.
func tempoOTLPID(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return b64
	}
	return hex.EncodeToString(raw)
}

func tempoOTLPToHeatmap(body []byte) ([]common.OpenTelemetryTraceHeatMap, error) {
	var resp tempoOTLPResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding trace response: %w", err)
	}
	out := []common.OpenTelemetryTraceHeatMap{}
	for _, rs := range resp.Trace.ResourceSpans {
		res := tempoAttrs(rs.Resource.Attributes)
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				start, _ := strconv.ParseInt(sp.StartTimeUnixNano, 10, 64)
				end, _ := strconv.ParseInt(sp.EndTimeUnixNano, 10, 64)
				status := sp.Status.Code
				if status == "" {
					status = "STATUS_CODE_UNSET"
				}
				names := make([]string, 0, len(sp.Events))
				evAttrs := make([]map[string]string, 0, len(sp.Events))
				for _, ev := range sp.Events {
					names = append(names, ev.Name)
					evAttrs = append(evAttrs, tempoAttrs(ev.Attributes))
				}
				out = append(out, common.OpenTelemetryTraceHeatMap{
					Timestamp:          time.Unix(0, start).UTC().Format(time.RFC3339Nano),
					ResourceAttributes: res,
					SpanName:           sp.Name,
					StatusCode:         status,
					DurationNs:         end - start,
					SpanAttributes:     tempoAttrs(sp.Attributes),
					TraceID:            tempoOTLPID(sp.TraceID),
					SpanID:             tempoOTLPID(sp.SpanID),
					ServiceName:        res["service.name"],
					EventsName:         names,
					EventsAttributes:   evAttrs,
				})
			}
		}
	}
	return out, nil
}

type tempoSeries struct {
	Labels map[string]string
	Value  float64
}

func parseTempoMetricSeries(body []byte) ([]tempoSeries, error) {
	var resp struct {
		Series []struct {
			Labels []tempoKV `json:"labels"`
			Value  *float64  `json:"value"`
		} `json:"series"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding metrics response: %w", err)
	}
	out := make([]tempoSeries, 0, len(resp.Series))
	for _, s := range resp.Series {
		if s.Value == nil {
			continue
		}
		out = append(out, tempoSeries{Labels: tempoAttrs(s.Labels), Value: *s.Value})
	}
	return out, nil
}
