package alertrule

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func openObserveTestConfig() AlertRuleConfig {
	return AlertRuleConfig{
		Name:           "High-CPU-production",
		Query:          "select value from logs",
		AlertType:      "log",
		Enabled:        true,
		ProviderConfig: map[string]any{"destinations": []string{"nudgebee-events"}},
	}
}

func TestBuildOpenObserveAlertPayload(t *testing.T) {
	connection := openObserveAlertConfig{LogStream: "logs", TraceStream: "traces"}
	tests := []struct {
		name      string
		mutate    func(*AlertRuleConfig)
		wantError string
		queryType string
		operator  string
	}{
		{"SQL defaults", func(*AlertRuleConfig) {}, "", "sql", ">="},
		{"PromQL defaults", func(c *AlertRuleConfig) { c.AlertType = "metric"; c.Query = "up" }, "", "promql", ">="},
		{"above operator", func(c *AlertRuleConfig) { c.ProviderConfig["operator"] = "above" }, "", "sql", ">"},
		{"below operator", func(c *AlertRuleConfig) { c.ProviderConfig["operator"] = "below" }, "", "sql", "<"},
		{"equal operator", func(c *AlertRuleConfig) { c.ProviderConfig["operator"] = "==" }, "", "sql", "="},
		{"explicit SQL", func(c *AlertRuleConfig) { c.AlertType = "metric"; c.ProviderConfig["query_type"] = "sql" }, "", "sql", ">="},
		{"JSON destination list", func(c *AlertRuleConfig) { c.ProviderConfig["destinations"] = []any{"nudgebee-events"} }, "", "sql", ">="},
		{"missing destinations", func(c *AlertRuleConfig) { delete(c.ProviderConfig, "destinations") }, "destinations are required", "", ""},
		{"empty destinations", func(c *AlertRuleConfig) { c.ProviderConfig["destinations"] = []string{} }, "destinations are required", "", ""},
		{"blank destination", func(c *AlertRuleConfig) { c.ProviderConfig["destinations"] = []string{" "} }, "cannot be empty", "", ""},
		{"invalid destination type", func(c *AlertRuleConfig) { c.ProviderConfig["destinations"] = []any{3} }, "destination names", "", ""},
		{"missing query", func(c *AlertRuleConfig) { c.Query = " " }, "query is required", "", ""},
		{"missing name", func(c *AlertRuleConfig) { c.Name = " " }, "name must be non-empty", "", ""},
		{"control in name", func(c *AlertRuleConfig) { c.Name = "bad\nname" }, "control characters", "", ""},
		{"space in name", func(c *AlertRuleConfig) { c.Name = "High CPU" }, "use hyphens or underscores", "", ""},
		{"reserved character in name", func(c *AlertRuleConfig) { c.Name = "High:CPU" }, "use hyphens or underscores", "", ""},
		{"invalid query type", func(c *AlertRuleConfig) { c.ProviderConfig["query_type"] = "javascript" }, "query type", "", ""},
		{"invalid stream", func(c *AlertRuleConfig) { c.ProviderConfig["stream_name"] = "logs; delete" }, "stream name", "", ""},
		{"invalid stream type", func(c *AlertRuleConfig) { c.ProviderConfig["stream_type"] = "other" }, "stream type", "", ""},
		{"invalid operator", func(c *AlertRuleConfig) { c.ProviderConfig["operator"] = "contains" }, "operator", "", ""},
		{"invalid threshold", func(c *AlertRuleConfig) { c.ProviderConfig["threshold"] = "bad" }, "threshold", "", ""},
		{"infinite threshold", func(c *AlertRuleConfig) { c.ProviderConfig["threshold"] = math.Inf(1) }, "finite", "", ""},
		{"invalid period", func(c *AlertRuleConfig) { c.ProviderConfig["period"] = 0 }, "must be positive", "", ""},
		{"invalid frequency", func(c *AlertRuleConfig) { c.ProviderConfig["frequency"] = -1 }, "must be positive", "", ""},
		{"invalid trigger threshold", func(c *AlertRuleConfig) { c.ProviderConfig["trigger_threshold"] = 0 }, "must be positive", "", ""},
		{"invalid silence", func(c *AlertRuleConfig) { c.ProviderConfig["silence"] = -1 }, "cannot be negative", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := openObserveTestConfig()
			tc.mutate(&config)
			payload, err := buildOpenObserveAlertPayload(config, connection)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if payload["name"] != config.Name {
				t.Errorf("name = %v", payload["name"])
			}
			if !reflect.DeepEqual(payload["destinations"], []string{"nudgebee-events"}) {
				t.Errorf("destinations = %v", payload["destinations"])
			}
			query := payload["query_condition"].(map[string]any)
			if query["type"] != tc.queryType {
				t.Errorf("query type = %v", query["type"])
			}
			if _, ok := query["promql_rule_mode"]; ok {
				t.Error("unsupported promql_rule_mode field rendered")
			}
			trigger := payload["trigger_condition"].(map[string]any)
			if trigger["operator"] != tc.operator || trigger["period"] != 15 || trigger["frequency"] != 1 {
				t.Errorf("trigger = %#v", trigger)
			}
			if tc.queryType == "promql" {
				condition := query["promql_condition"].(map[string]any)
				if condition["value"] != float64(0) {
					t.Errorf("PromQL threshold = %v", condition["value"])
				}
				if query["promql"] != config.Query {
					t.Errorf("PromQL = %v", query["promql"])
				}
			} else if query["sql"] != config.Query {
				t.Errorf("SQL = %v", query["sql"])
			}
		})
	}
}

func TestOpenObserveAlertHTTPPaths(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method {
					t.Errorf("method = %s", r.Method)
				}
				expected := "/api/v2/org/alerts"
				if method != http.MethodPost {
					expected += "/alert%2Fid"
				}
				if r.URL.EscapedPath() != expected {
					t.Errorf("path = %s, want %s", r.URL.EscapedPath(), expected)
				}
				user, password, ok := r.BasicAuth()
				if !ok || user != "test" || password != "test-secret" {
					t.Error("missing basic auth")
				}
				if method != http.MethodDelete {
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload["name"] != "test-alert" {
						t.Errorf("body = %#v", payload)
					}
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, "{}")
			}))
			defer server.Close()
			connection := openObserveAlertConfig{URL: server.URL, OrgID: "org", Username: "test", Password: "test-secret"}
			id := "alert/id"
			var payload any = map[string]any{"name": "test-alert"}
			if method == http.MethodPost {
				id = ""
			}
			if method == http.MethodDelete {
				payload = nil
			}
			response, err := openObserveAlertRequest(method, connection, id, payload)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusCreated {
				t.Errorf("status = %d", response.StatusCode)
			}
		})
	}
}

func TestOpenObserveAlertRequestRejectsMissingIDAndUnsupportedMethod(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if _, err := openObserveAlertRequest(method, openObserveAlertConfig{}, "", nil); err == nil {
			t.Errorf("%s with no ID accepted", method)
		}
	}
	if _, err := openObserveAlertRequest(http.MethodPatch, openObserveAlertConfig{}, "id", nil); err == nil {
		t.Error("unsupported method accepted")
	}
}
func TestOpenObserveAlertThresholdSemantics(t *testing.T) {
	tests := []struct {
		name      string
		queryType string
		operator  string
		threshold any
		override  any
		wantRows  int
		wantError bool
	}{
		{"SQL threshold", "sql", "above", 5, nil, 5, false},
		{"SQL explicit row override", "sql", "above", 5, 3, 3, false},
		{"SQL fractional count", "sql", "above", 1.5, nil, 0, true},
		{"SQL nonpositive count", "sql", "above", 0, nil, 0, true},
		{"PromQL above value threshold", "promql", "above", 5, nil, 1, false},
		{"PromQL below value threshold", "promql", "below", 5, nil, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := openObserveTestConfig()
			cfg.ProviderConfig["query_type"] = tc.queryType
			cfg.ProviderConfig["threshold"] = tc.threshold
			cfg.ProviderConfig["operator"] = tc.operator
			if tc.override != nil {
				cfg.ProviderConfig["trigger_threshold"] = tc.override
			}
			payload, err := buildOpenObserveAlertPayload(cfg, openObserveAlertConfig{LogStream: "logs"})
			if tc.wantError {
				if err == nil {
					t.Fatal("invalid SQL row threshold accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			trigger := payload["trigger_condition"].(map[string]any)
			if trigger["threshold"] != tc.wantRows {
				t.Errorf("row threshold = %v, want %d", trigger["threshold"], tc.wantRows)
			}
			if tc.queryType == "promql" {
				if trigger["operator"] != ">=" {
					t.Errorf("PromQL row operator = %v", trigger["operator"])
				}
				condition := payload["query_condition"].(map[string]any)["promql_condition"].(map[string]any)
				wantOperator := ">"
				if tc.operator == "below" {
					wantOperator = "<"
				}
				if condition["operator"] != wantOperator || condition["value"] != float64(5) {
					t.Errorf("PromQL value condition = %#v", condition)
				}
			} else if trigger["operator"] != ">" {
				t.Errorf("SQL row operator = %v", trigger["operator"])
			}
		})
	}
}
