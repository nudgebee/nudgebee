package opencostengine

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeMetricsServer stands in for services-server's /rpc/metrics action. It
// records the last request body and returns the supplied OutputMetricQuery JSON.
func fakeMetricsServer(t *testing.T, token, responseJSON string, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc/metrics" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-ACTION-TOKEN"); got != token {
			t.Errorf("X-ACTION-TOKEN = %q, want %q", got, token)
		}
		body, _ := io.ReadAll(r.Body)
		if captured != nil {
			_ = json.Unmarshal(body, captured)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseJSON))
	}))
}

func TestShimInstantQuery(t *testing.T) {
	// metrics_query returns an instant vector parsed into Result rows: one
	// timestamp (seconds) + one value per series.
	resp := `{"results":[{"query_key":"q","query":"up","payload":[
		{"metric":{"job":"kubelet","instance":"n1"},"timestamps":[1719446400],"values":[1]}
	]}]}`
	var captured map[string]any
	srv := fakeMetricsServer(t, "tok", resp, &captured)
	defer srv.Close()

	shim := newPrometheusShim(srv.URL, "tok", nil)
	rec := httptest.NewRecorder()
	form := url.Values{"query": {"up"}, "time": {"1719446400"}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Scope-OrgID", "acct-1")
	shim.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Forwarded request carries the account id + super-admin role.
	input := captured["input"].(map[string]any)["request"].(map[string]any)
	if input["account_id"] != "acct-1" {
		t.Errorf("account_id = %v", input["account_id"])
	}
	if input["instant"] != true {
		t.Errorf("instant = %v, want true", input["instant"])
	}
	if sv := captured["session_variables"].(map[string]any); sv["role"] != "admin" {
		t.Errorf("role = %v, want admin", sv["role"])
	}

	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "success" || out.Data.ResultType != "vector" {
		t.Fatalf("envelope = %+v", out)
	}
	if len(out.Data.Result) != 1 {
		t.Fatalf("result len = %d", len(out.Data.Result))
	}
	s := out.Data.Result[0]
	if s.Metric["job"] != "kubelet" {
		t.Errorf("metric = %v", s.Metric)
	}
	if len(s.Value) != 2 || s.Value[0].(float64) != 1719446400 || s.Value[1].(string) != "1" {
		t.Errorf("value = %v, want [1719446400, \"1\"]", s.Value)
	}
}

func TestShimRangeQuery(t *testing.T) {
	resp := `{"results":[{"query_key":"q","query":"x","payload":[
		{"metric":{"pod":"p1"},"timestamps":[100,160],"values":[0.5,1.5]}
	]}]}`
	srv := fakeMetricsServer(t, "tok", resp, nil)
	defer srv.Close()

	shim := newPrometheusShim(srv.URL, "tok", nil)
	rec := httptest.NewRecorder()
	form := url.Values{"query": {"x"}, "start": {"100"}, "end": {"160"}, "step": {"60"}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query_range", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Scope-OrgID", "acct-1")
	shim.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]any           `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Data.ResultType != "matrix" {
		t.Fatalf("resultType = %s", out.Data.ResultType)
	}
	vals := out.Data.Result[0].Values
	if len(vals) != 2 {
		t.Fatalf("values len = %d", len(vals))
	}
	if vals[0][0].(float64) != 100 || vals[0][1].(string) != "0.5" {
		t.Errorf("point0 = %v", vals[0])
	}
	if vals[1][0].(float64) != 160 || vals[1][1].(string) != "1.5" {
		t.Errorf("point1 = %v", vals[1])
	}
}

func TestShimMissingScopeHeader(t *testing.T) {
	shim := newPrometheusShim("http://unused", "tok", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up&time=1", nil)
	shim.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestShimQueryErrorFailsClosed(t *testing.T) {
	// A per-query error must surface as an error envelope, not empty success.
	resp := `{"results":[{"query_key":"q","query":"bad","error":"boom","payload":[]}]}`
	srv := fakeMetricsServer(t, "tok", resp, nil)
	defer srv.Close()

	shim := newPrometheusShim(srv.URL, "tok", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=bad&time=1", nil)
	req.Header.Set("X-Scope-OrgID", "acct-1")
	shim.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200 on query error, got 200: %s", rec.Body.String())
	}
}

func TestFormatSample(t *testing.T) {
	cases := map[float64]string{0.5: "0.5", 1: "1", 1000000: "1000000"}
	for in, want := range cases {
		if got := formatSample(in); got != want {
			t.Errorf("formatSample(%v) = %q, want %q", in, got, want)
		}
	}
}
