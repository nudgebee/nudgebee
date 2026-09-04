package alertrule

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePromDurationSeconds(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   int
		wantOK bool
	}{
		{"minutes", "5m", 300, true},
		{"seconds", "90s", 90, true},
		{"hours", "1h", 3600, true},
		{"compound", "1h30m", 5400, true},
		{"days", "2d", 172800, true},
		{"weeks", "1w", 604800, true},
		{"bare number is seconds", "45", 45, true},
		{"empty", "", 0, false},
		{"zero", "0s", 0, false},
		{"unknown unit", "5y", 0, false},
		{"garbage", "abc", 0, false},
		// A trailing digit run means a unit was missing.
		{"missing trailing unit", "5m30", 0, false},
		{"leading unit", "m5", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parsePromDurationSeconds(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %d)", ok, tt.wantOK, got)
			}
			if ok && got != tt.want {
				t.Errorf("parsePromDurationSeconds(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestCubeAPMForSeconds(t *testing.T) {
	t.Run("provider_config wins", func(t *testing.T) {
		got := cubeAPMForSeconds(AlertRuleConfig{
			Duration:       "5m",
			ProviderConfig: map[string]any{"for": 120},
		})
		if got != 120 {
			t.Errorf("got %d, want the explicit provider_config value", got)
		}
	})

	t.Run("falls back to the parsed duration", func(t *testing.T) {
		if got := cubeAPMForSeconds(AlertRuleConfig{Duration: "10m"}); got != 600 {
			t.Errorf("got %d, want 600", got)
		}
	})

	// An unparseable duration must not become 0 — a rule with for=0 fires on the
	// first scrape that breaches, which is not what "5 minutes" asked for.
	t.Run("unparseable duration falls back to the default", func(t *testing.T) {
		if got := cubeAPMForSeconds(AlertRuleConfig{Duration: "banana"}); got != cubeAPMDefaultForSeconds {
			t.Errorf("got %d, want the default %d", got, cubeAPMDefaultForSeconds)
		}
	})

	t.Run("no duration at all", func(t *testing.T) {
		if got := cubeAPMForSeconds(AlertRuleConfig{}); got != cubeAPMDefaultForSeconds {
			t.Errorf("got %d, want the default %d", got, cubeAPMDefaultForSeconds)
		}
	})
}

func TestBuildCubeAPMRule(t *testing.T) {
	config := AlertRuleConfig{
		AccountId:   "acct",
		Name:        "HighErrorRate",
		AlertType:   "metric",
		Query:       `sum(rate(http_errors_total[5m])) > 10`,
		Severity:    "critical",
		Duration:    "5m",
		Enabled:     true,
		Annotations: map[string]string{"description": "errors are high", "summary": ""},
		Labels:      map[string]string{"team": "payments"},
	}

	rule := buildCubeAPMRule(config, 0)

	if rule.Name != "HighErrorRate" {
		t.Errorf("Name = %q", rule.Name)
	}
	// id is omitempty; a create must not carry one.
	if rule.ID != 0 {
		t.Errorf("ID = %d, want 0 on create", rule.ID)
	}
	if rule.Datasource != "prometheus" {
		t.Errorf("Datasource = %q, want prometheus for a metric rule", rule.Datasource)
	}
	if rule.Kind != "static" {
		t.Errorf("Kind = %q", rule.Kind)
	}
	if rule.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE for an enabled rule", rule.Status)
	}
	if rule.For != 300 {
		t.Errorf("For = %d, want 300 seconds (CubeAPM takes integer seconds, not \"5m\")", rule.For)
	}
	if rule.Interval != cubeAPMDefaultEvalInterval {
		t.Errorf("Interval = %d", rule.Interval)
	}
	if rule.RepeatInterval != cubeAPMDefaultRepeatInterval {
		t.Errorf("RepeatInterval = %d", rule.RepeatInterval)
	}
	if rule.Labels["severity"] != "critical" {
		t.Errorf("severity label = %q", rule.Labels["severity"])
	}
	// Marks rules this system owns, so an operator can tell them apart from rules
	// authored in CubeAPM's own UI.
	if rule.Labels["source"] != "nudgebee" {
		t.Errorf("source label = %q, want nudgebee", rule.Labels["source"])
	}
	if rule.Labels["team"] != "payments" {
		t.Errorf("custom label was dropped: %v", rule.Labels)
	}
	// Summary defaults to the rule name; an empty supplied value must not blank it.
	if rule.Annotations["summary"] != "HighErrorRate" {
		t.Errorf("summary = %q, want the rule name", rule.Annotations["summary"])
	}
	if rule.Annotations["description"] != "errors are high" {
		t.Errorf("description = %q", rule.Annotations["description"])
	}
	// Notification routing is configured in CubeAPM; populating it here would
	// silently override whatever the operator set up.
	if len(rule.Receiver) != 0 {
		t.Errorf("Receiver = %v, want empty so CubeAPM's own routing is preserved", rule.Receiver)
	}
}

func TestBuildCubeAPMRuleDisabledBecomesPaused(t *testing.T) {
	// CubeAPM has no "disabled" state; a rule that should not evaluate is PAUSED.
	rule := buildCubeAPMRule(AlertRuleConfig{Name: "x", Enabled: false}, 0)
	if rule.Status != "PAUSED" {
		t.Errorf("Status = %q, want PAUSED", rule.Status)
	}
}

func TestBuildCubeAPMRuleUpdateCarriesID(t *testing.T) {
	rule := buildCubeAPMRule(AlertRuleConfig{Name: "x", Enabled: true}, 42)
	if rule.ID != 42 {
		t.Errorf("ID = %d, want 42", rule.ID)
	}

	body, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(body), `"id":42`) {
		t.Errorf("id missing from the update body: %s", body)
	}
}

func TestBuildCubeAPMRuleProviderConfigOverrides(t *testing.T) {
	rule := buildCubeAPMRule(AlertRuleConfig{
		Name:    "x",
		Enabled: true,
		ProviderConfig: map[string]any{
			"interval":         30,
			"repeat_interval":  600,
			"grouping_disable": true,
			"receiver":         map[string]any{"slack_configs": []any{}},
		},
	}, 0)

	if rule.Interval != 30 {
		t.Errorf("Interval = %d, want 30", rule.Interval)
	}
	if rule.RepeatInterval != 600 {
		t.Errorf("RepeatInterval = %d, want 600", rule.RepeatInterval)
	}
	if !rule.GroupingDisable {
		t.Error("GroupingDisable override was ignored")
	}
	if _, ok := rule.Receiver["slack_configs"]; !ok {
		t.Errorf("Receiver override was ignored: %v", rule.Receiver)
	}
}

func TestCubeAPMDatasource(t *testing.T) {
	tests := []struct {
		name   string
		config AlertRuleConfig
		want   string
	}{
		{"metric", AlertRuleConfig{AlertType: "metric"}, "prometheus"},
		{"unspecified defaults to metric", AlertRuleConfig{}, "prometheus"},
		{"log", AlertRuleConfig{AlertType: "log"}, "logs"},
		// The log datasource name is not spelled out in CubeAPM's API reference, so
		// a deployment using a different one is not blocked on a code change.
		{"explicit override", AlertRuleConfig{AlertType: "log",
			ProviderConfig: map[string]any{"datasource": "cube-logs"}}, "cube-logs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMDatasource(tt.config); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCubeAPMAlertTypeFromDatasource(t *testing.T) {
	if got := cubeAPMAlertTypeFromDatasource("logs"); got != "log" {
		t.Errorf("got %q, want log", got)
	}
	if got := cubeAPMAlertTypeFromDatasource("prometheus"); got != "metric" {
		t.Errorf("got %q, want metric", got)
	}
	if got := cubeAPMAlertTypeFromDatasource(""); got != "metric" {
		t.Errorf("got %q, want metric", got)
	}
}

func TestNormalizeCubeAPMSeverity(t *testing.T) {
	tests := map[string]string{
		"critical": "critical",
		"CRITICAL": "critical",
		"fatal":    "critical",
		"high":     "critical",
		"p1":       "critical",
		"info":     "info",
		"low":      "info",
		"warning":  "warning",
		"":         "warning",
		"nonsense": "warning",
	}
	for in, want := range tests {
		if got := normalizeCubeAPMSeverity(in); got != want {
			t.Errorf("normalizeCubeAPMSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCubeAPMProviderInt(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want int
	}{
		{"int", 30, 30},
		{"int64", int64(30), 30},
		// A JSON round-trip turns every number into a float64.
		{"float64", float64(30), 30},
		{"json.Number", json.Number("30"), 30},
		{"string", "30", 30},
		{"unparseable string", "abc", 99},
		{"wrong type", []any{}, 99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMProviderInt(map[string]any{"k": tt.raw}, "k", 99); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("absent key", func(t *testing.T) {
		if got := cubeAPMProviderInt(map[string]any{}, "k", 99); got != 99 {
			t.Errorf("got %d, want the fallback", got)
		}
	})
}

func TestParseCubeAPMRuleID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"top level", `{"id": 7, "name": "x"}`, "7"},
		{"wrapped in data", `{"data": {"id": 9, "name": "x"}}`, "9"},
		// Zero is CubeAPM's unset id, not a real handle.
		{"zero id is not a handle", `{"id": 0}`, ""},
		{"no id", `{"name": "x"}`, ""},
		{"malformed", `not json`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCubeAPMRuleID([]byte(tt.body)); got != tt.want {
				t.Errorf("parseCubeAPMRuleID(%s) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestCubeAPMDurationString(t *testing.T) {
	if got := cubeAPMDurationString(json.Number("300")); got != "5m0s" {
		t.Errorf("got %q, want 5m0s", got)
	}
	if got := cubeAPMDurationString(json.Number("0")); got != "" {
		t.Errorf("got %q, want empty for a zero duration", got)
	}
	if got := cubeAPMDurationString(json.Number("abc")); got != "" {
		t.Errorf("got %q, want empty for an unparseable value", got)
	}
}

func TestNormalizeCubeAPMBaseURL(t *testing.T) {
	tests := map[string]string{
		"http://cube:3199":            "http://cube:3199",
		"http://cube:3199/":           "http://cube:3199",
		"http://cube:3199/api/alerts": "http://cube:3199",
		"  https://cube:3199  ":       "https://cube:3199",
		"":                            "",
	}
	for in, want := range tests {
		if got := normalizeCubeAPMBaseURL(in); got != want {
			t.Errorf("normalizeCubeAPMBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveCubeAPMAdminBaseURL(t *testing.T) {
	if got := deriveCubeAPMAdminBaseURL("http://cube:3140"); got != "http://cube:3199" {
		t.Errorf("got %q, want http://cube:3199", got)
	}
	// A non-standard port means we cannot know where the admin server is;
	// guessing would silently target whatever else is listening.
	if got := deriveCubeAPMAdminBaseURL("http://cube:9999"); got != "" {
		t.Errorf("got %q, want empty for a non-standard port", got)
	}
	if got := deriveCubeAPMAdminBaseURL(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestCubeAPMAdminHeaders(t *testing.T) {
	t.Run("omits Authorization when unset", func(t *testing.T) {
		headers := cubeAPMAdminHeaders("")
		if _, present := headers["Authorization"]; present {
			t.Error("http-token-admin is optional; an empty Bearer is a malformed credential")
		}
		if headers["Content-Type"] != "application/json" {
			t.Errorf("Content-Type = %q", headers["Content-Type"])
		}
	})

	t.Run("sets the bearer token", func(t *testing.T) {
		if got := cubeAPMAdminHeaders(" tok ")["Authorization"]; got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
	})
}

func TestCubeAPMAdminStatusError(t *testing.T) {
	// CubeAPM ships with the admin server disabled on some deployments, and a
	// bare 404 reads as a wrong path rather than a server that is not listening.
	t.Run("404 explains the disabled admin server", func(t *testing.T) {
		err := cubeAPMAdminStatusError(404, nil, "http://cube:3199")
		if !strings.Contains(err.Error(), "http-host-admin") {
			t.Errorf("error should mention the admin server setting: %v", err)
		}
	})

	t.Run("401 names the token field", func(t *testing.T) {
		err := cubeAPMAdminStatusError(401, nil, "http://cube:3199")
		if !strings.Contains(err.Error(), "cubeapm_admin_token") {
			t.Errorf("error should name the field to fix: %v", err)
		}
	})

	t.Run("truncates a long body", func(t *testing.T) {
		err := cubeAPMAdminStatusError(500, []byte(strings.Repeat("x", 5000)), "http://cube:3199")
		if len(err.Error()) > 700 {
			t.Errorf("error is %d chars; the body should be truncated", len(err.Error()))
		}
	})
}

func TestCubeAPMAlertRuleSourceContract(t *testing.T) {
	var s any = &CubeAPMAlertRuleSource{}

	if _, ok := s.(AlertRuleSource); !ok {
		t.Error("CubeAPMAlertRuleSource must implement AlertRuleSource")
	}
	// Listing is what makes CubeAPM rules syncable back into event_rules, and it
	// is also the fallback that recovers a rule id when create does not return one.
	if _, ok := s.(AlertRuleLister); !ok {
		t.Error("CubeAPMAlertRuleSource must implement AlertRuleLister")
	}
}

func TestCubeAPMAlertRuleRoutedFromDispatcher(t *testing.T) {
	src, err := getAlertRuleSource("cubeapm", "user")
	if err != nil {
		t.Fatalf("getAlertRuleSource(cubeapm, user) failed: %v", err)
	}
	if _, ok := src.(*CubeAPMAlertRuleSource); !ok {
		t.Errorf("got %T, want *CubeAPMAlertRuleSource", src)
	}

	if !SupportsListing("cubeapm", "user") {
		t.Error("SupportsListing(cubeapm) = false; the sync path skips providers it cannot list")
	}
}

func TestParseCubeAPMRulesResponse(t *testing.T) {
	// CubeAPM's API reference documents the GET example as a single rule object
	// and does not specify the collection wrapper, so all three shapes are accepted.
	shapes := map[string]string{
		"bare array":  `[{"id":1,"name":"a"},{"id":2,"name":"b"}]`,
		"data wrap":   `{"data":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}`,
		"rules wrap":  `{"rules":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}`,
		"single rule": `{"id":1,"name":"a"}`,
	}

	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			rules, err := parseCubeAPMRulesResponse([]byte(body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rules) == 0 {
				t.Fatal("no rules parsed")
			}
			if rules[0].Name != "a" {
				t.Errorf("rules[0].Name = %q", rules[0].Name)
			}
			if rules[0].ID.String() != "1" {
				t.Errorf("rules[0].ID = %q; the numeric id is the handle for update/delete", rules[0].ID)
			}
		})
	}

	t.Run("unrecognized body errors", func(t *testing.T) {
		if _, err := parseCubeAPMRulesResponse([]byte(`"not a rule"`)); err == nil {
			t.Error("expected an error for an unrecognized response shape")
		}
	})
}

// An install with no rules yet returns an empty list, not a parse failure. The
// length test this replaced could not distinguish "key absent" from "key present
// and empty", so a fresh CubeAPM broke alert-rule sync entirely.
func TestParseCubeAPMRulesResponseAcceptsEmptyListings(t *testing.T) {
	for name, body := range map[string]string{
		"bare empty array": `[]`,
		"empty data wrap":  `{"data": []}`,
		"empty rules wrap": `{"rules": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			rules, err := parseCubeAPMRulesResponse([]byte(body))
			if err != nil {
				t.Fatalf("empty listing should parse cleanly, got error: %v", err)
			}
			if len(rules) != 0 {
				t.Errorf("got %d rules, want 0", len(rules))
			}
		})
	}
}

// An empty account id must fail rather than resolve to whichever CubeAPM
// integration happens to come first in the tenant — the account filter is
// conditional and the query ends in LIMIT 1, so the wrong account's admin
// credentials would otherwise be used to write alert rules.
func TestGetCubeAPMConfigsRequiresAccountID(t *testing.T) {
	_, err := getCubeAPMConfigs(nil, "")
	if err == nil {
		t.Fatal("expected an error for an empty account id")
	}
	if !strings.Contains(err.Error(), "account_id is required") {
		t.Errorf("error = %v", err)
	}
}
