package observability

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

func TestJaegerActionRequestMapsEachActionToTheQueryAPI(t *testing.T) {
	cases := []struct {
		action string
		params map[string]any
		path   string
		check  func(t *testing.T, q map[string][]string)
	}{
		{"jaeger_query_services", nil, "/api/services", nil},
		{"jaeger_query_operations", map[string]any{"service": "orders web"}, "/api/services/orders%20web/operations", nil},
		{"jaeger_query_trace_by_id", map[string]any{"trace_id": "abc123"}, "/api/traces/abc123", nil},
		{"jaeger_query_metrics", map[string]any{"metric_type": "calls", "service": "x"}, "/api/metrics/calls", func(t *testing.T, q map[string][]string) {
			if q["service"][0] != "x" || len(q["metric_type"]) != 0 {
				t.Errorf("metrics query = %v", q)
			}
		}},
		{"jaeger_query_traces", map[string]any{
			"service": "orders-web", "start": int64(1_700_000_000_000_000), "limit": 100, "operation": "",
			"tags": map[string]string{"http.status_code": "500"},
		}, "/api/traces", func(t *testing.T, q map[string][]string) {
			if q["service"][0] != "orders-web" || q["start"][0] != "1700000000000000" || q["limit"][0] != "100" {
				t.Errorf("traces query = %v", q)
			}
			if _, ok := q["operation"]; ok {
				t.Error("an empty operation must not be sent")
			}
			if q["tags"][0] != `{"http.status_code":"500"}` {
				t.Errorf("tags = %q, want Jaeger's JSON object", q["tags"][0])
			}
		}},
	}
	for _, c := range cases {
		path, params, err := jaegerActionRequest(c.action, c.params)
		if err != nil {
			t.Fatalf("%s: %v", c.action, err)
		}
		if path != c.path {
			t.Errorf("%s path = %q, want %q", c.action, path, c.path)
		}
		if c.check != nil {
			c.check(t, params)
		}
	}
	for _, bad := range []struct {
		action string
		params map[string]any
	}{
		{"jaeger_query_trace_by_id", nil}, {"jaeger_query_operations", nil}, {"jaeger_query_metrics", nil}, {"k8s_pods", nil},
	} {
		if _, _, err := jaegerActionRequest(bad.action, bad.params); err == nil {
			t.Errorf("%s with %v should be refused", bad.action, bad.params)
		}
	}
}

// fakeJaeger answers the way the Jaeger query API does, from responses captured off a
// real Jaeger holding OTLP traces.
func fakeJaeger(t *testing.T) (*httptest.Server, *[]string, *string) {
	t.Helper()
	traces, err := os.ReadFile("testdata/jaeger_traces_orders_web.json")
	if err != nil {
		t.Fatal(err)
	}
	services, err := os.ReadFile("testdata/jaeger_services.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		switch {
		case r.URL.Path == "/api/services":
			_, _ = w.Write(services)
		case r.URL.Path == "/api/traces":
			_, _ = w.Write(traces)
		case strings.HasSuffix(r.URL.Path, "/operations"):
			_, _ = w.Write([]byte(`{"data":["GET /orders","POST /checkout"],"total":2}`))
		default:
			http.Error(w, "no such thing", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen, &auth
}

func TestJaegerDirectGetReturnsTheRelayShapeAndSendsTheToken(t *testing.T) {
	srv, seen, auth := fakeJaeger(t)
	cfg := integrations.JaegerConfig{URL: srv.URL, Token: "s3cret"}

	out, err := jaegerDirectGet(cfg, relay.ActionExecuteBody{ActionName: "jaeger_query_services"})
	if err != nil {
		t.Fatal(err)
	}
	if out["status_code"] != float64(200) {
		t.Errorf("status_code = %v", out["status_code"])
	}
	if *auth != "Bearer s3cret" {
		t.Errorf("Authorization = %q", *auth)
	}
	if len(*seen) != 1 || (*seen)[0] != "/api/services" {
		t.Errorf("requests = %v", *seen)
	}

	_, err = jaegerDirectGet(cfg, relay.ActionExecuteBody{ActionName: "jaeger_query_trace_by_id", ActionParams: map[string]any{"trace_id": "missing"}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err = %v, want Jaeger's HTTP error named", err)
	}
}

// The user-connected source gets the same behaviour as agent mode because it runs the
// same logic over a different transport: services fill the dropdown, spans convert.
func TestJaegerSharedLogicOverTheDirectTransport(t *testing.T) {
	srv, seen, _ := fakeJaeger(t)
	src := &JaegerTraceSource{exec: func(_ *security.RequestContext, body relay.ActionExecuteBody) (map[string]any, error) {
		return jaegerDirectGet(integrations.JaegerConfig{URL: srv.URL}, body)
	}}
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	account := "11111111-1111-1111-1111-111111111111"

	got, err := src.GetLabelValues(ctx, TracesV3LabelValuesRequest{AccountId: account, Label: "workload_name"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"orders-web": true, "orders-api": true, "orders-db": true}
	for _, v := range got.Values {
		delete(want, v)
	}
	if len(want) != 0 {
		t.Errorf("service values = %v, missing %v", got.Values, want)
	}

	end := time.Now()
	spans, err := src.QueryTraces(ctx, TracesV3Request{
		AccountId:    account,
		StartTime:    end.Add(-time.Hour).UnixMilli(),
		EndTime:      end.UnixMilli(),
		Request:      map[string]any{"service": "orders-web"},
		QueryRequest: TracesQueryBuilderRequest{Limit: 50},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) == 0 {
		t.Fatalf("no spans converted; requests were %v", *seen)
	}
	var sawServer bool
	for _, sp := range spans {
		if sp.TraceID == "" || sp.SpanID == "" || sp.SpanName == "" || sp.DurationNs <= 0 {
			t.Errorf("incomplete span %+v", sp)
		}
		if sp.ServiceName == "" {
			t.Errorf("span %s has no service name", sp.SpanID)
		}
		if strings.HasPrefix(sp.SpanName, "GET /") || strings.HasPrefix(sp.SpanName, "POST /") {
			sawServer = true
		}
	}
	if !sawServer {
		t.Error("no HTTP span converted")
	}
}
