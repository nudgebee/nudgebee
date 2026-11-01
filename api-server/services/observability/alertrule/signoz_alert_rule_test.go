package alertrule

import (
	"encoding/json"
	"os"
	"testing"

	"nudgebee/services/integrations/signozclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTrip marshals a built body and decodes it generically, so assertions see
// exactly what is sent to SigNoz.
func roundTrip(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func condition(body map[string]any) map[string]any {
	return body["condition"].(map[string]any)
}

func compositeQuery(body map[string]any) map[string]any {
	return condition(body)["compositeQuery"].(map[string]any)
}

// The v5 bodies below were accepted by a live SigNoz v0.145.0 (POST /api/v1/rules).
func TestBuildSignozAlertRule_V5PrometheusStyleExpressionIsWrapped(t *testing.T) {
	body, err := buildSignozAlertRule(AlertRuleConfig{
		Name:        "pod down",
		AlertType:   "metric",
		Query:       `up{job="api"} == 0`,
		Severity:    "critical",
		Duration:    "2m",
		Enabled:     true,
		Annotations: map[string]string{"summary": "api down", "description": ""},
	}, signozclient.RuleSchemaV5, []string{"nudgebee"})
	require.NoError(t, err)
	got := roundTrip(t, body)

	assert.Equal(t, "v5", got["version"])
	assert.Equal(t, "METRIC_BASED_ALERT", got["alertType"])
	assert.Equal(t, "promql_rule", got["ruleType"])
	assert.Equal(t, false, got["disabled"])
	assert.Equal(t, []any{"nudgebee"}, got["preferredChannels"])
	assert.Equal(t, "2m0s", got["evalWindow"])
	assert.NotContains(t, got, "state")
	assert.Equal(t, map[string]any{"severity": "critical"}, got["labels"])
	assert.Equal(t, map[string]any{"summary": "api down"}, got["annotations"], "empty annotations are dropped")

	cond := condition(got)
	assert.Equal(t, "1", cond["op"])
	assert.Equal(t, 0.0, cond["target"])
	assert.Equal(t, "2", cond["matchType"], "a for-duration means all the time over that window")
	assert.Equal(t, "A", cond["selectedQueryName"])

	cq := compositeQuery(got)
	assert.Equal(t, "promql", cq["queryType"])
	assert.NotContains(t, cq, "promQueries")
	queries := cq["queries"].([]any)
	require.Len(t, queries, 1)
	q := queries[0].(map[string]any)
	assert.Equal(t, "promql", q["type"])
	assert.Equal(t, `(up{job="api"} == 0) * 0 + 1`, q["spec"].(map[string]any)["query"],
		"every series the expression returns must fire, even with value 0")
}

func TestBuildSignozAlertRule_BoolComparisonIsNotWrapped(t *testing.T) {
	body, err := buildSignozAlertRule(AlertRuleConfig{
		Name: "x", AlertType: "metric", Query: `max(k8s_pod_restarts) > bool 5`, Enabled: true,
	}, signozclient.RuleSchemaV5, []string{"c"})
	require.NoError(t, err)
	spec := compositeQuery(roundTrip(t, body))["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	assert.Equal(t, `max(k8s_pod_restarts) > bool 5`, spec["query"], "a bool comparison is 0/1 already; wrapping would fire on every series")

	assert.True(t, promQLHasBoolModifier(`x > bool 1`))
	assert.True(t, promQLHasBoolModifier(`(x==bool 1)`))
	assert.False(t, promQLHasBoolModifier(`up{job="bool"} == 0`), "a label value is not the modifier")
	assert.False(t, promQLHasBoolModifier(`boolean_metric > 1`))
}

func TestBuildSignozAlertRule_ExplicitThresholdKeepsTheExpression(t *testing.T) {
	body, err := buildSignozAlertRule(AlertRuleConfig{
		Name:           "memory",
		AlertType:      "metric",
		Query:          `max(container_memory_working_set_bytes)`,
		Severity:       "warning",
		Enabled:        false,
		ProviderConfig: map[string]any{"threshold": 6e8, "threshold_operator": ">="},
	}, signozclient.RuleSchemaV5, []string{"nudgebee"})
	require.NoError(t, err)
	got := roundTrip(t, body)

	assert.Equal(t, true, got["disabled"])
	assert.Equal(t, "1m0s", got["evalWindow"])
	cond := condition(got)
	assert.Equal(t, "5", cond["op"])
	assert.Equal(t, 6e8, cond["target"])
	assert.Equal(t, "1", cond["matchType"])
	spec := compositeQuery(got)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	assert.Equal(t, `max(container_memory_working_set_bytes)`, spec["query"])
}

func TestBuildSignozAlertRule_OperatorWithoutThresholdKeepsAboveZero(t *testing.T) {
	body, err := buildSignozAlertRule(AlertRuleConfig{
		Name: "x", AlertType: "metric", Query: "up == 0", ProviderConfig: map[string]any{"threshold_operator": "<"},
	}, signozclient.RuleSchemaV5, []string{"c"})
	require.NoError(t, err)
	assert.Equal(t, "1", condition(roundTrip(t, body))["op"], "a wrapped expression is always 1; only above 0 can fire")
}

func TestBuildSignozAlertRule_V4UsesPromQueries(t *testing.T) {
	body, err := buildSignozAlertRule(AlertRuleConfig{
		Name: "x", AlertType: "metric", Query: `rate(x[5m]) > 1`, Enabled: true,
	}, signozclient.RuleSchemaV4, nil)
	require.NoError(t, err)
	got := roundTrip(t, body)

	assert.Equal(t, "v4", got["version"])
	assert.NotContains(t, got, "preferredChannels", "the builder adds only the channels it is given")
	cq := compositeQuery(got)
	assert.NotContains(t, cq, "queries")
	a := cq["promQueries"].(map[string]any)["A"].(map[string]any)
	assert.Equal(t, `(rate(x[5m]) > 1) * 0 + 1`, a["query"])
	assert.Equal(t, "A", a["name"])
}

func TestBuildSignozAlertRule_V4RejectsWhatItCannotExpress(t *testing.T) {
	_, err := buildSignozAlertRule(AlertRuleConfig{
		Name: "x", AlertType: "metric", Query: "x", ProviderConfig: map[string]any{"threshold": 1, "threshold_operator": "<="},
	}, signozclient.RuleSchemaV4, nil)
	assert.ErrorContains(t, err, "before v0.118")

	_, err = buildSignozAlertRule(AlertRuleConfig{Name: "x", AlertType: "log", Query: "body CONTAINS 'x'"}, signozclient.RuleSchemaV4, nil)
	assert.ErrorContains(t, err, "v0.118")
}

func TestBuildSignozAlertRule_V5LogRuleCountsMatchingLines(t *testing.T) {
	body, err := buildSignozAlertRule(AlertRuleConfig{
		Name: "errors", AlertType: "log", Query: `k8s.namespace.name = 'shop' AND body CONTAINS 'error'`, Enabled: true,
	}, signozclient.RuleSchemaV5, []string{"nudgebee"})
	require.NoError(t, err)
	got := roundTrip(t, body)

	assert.Equal(t, "LOGS_BASED_ALERT", got["alertType"])
	assert.Equal(t, "threshold_rule", got["ruleType"])
	cq := compositeQuery(got)
	assert.Equal(t, "builder", cq["queryType"])
	q := cq["queries"].([]any)[0].(map[string]any)
	assert.Equal(t, "builder_query", q["type"])
	spec := q["spec"].(map[string]any)
	assert.Equal(t, "logs", spec["signal"])
	assert.Equal(t, []any{map[string]any{"expression": "count()"}}, spec["aggregations"])
	assert.Equal(t, `k8s.namespace.name = 'shop' AND body CONTAINS 'error'`, spec["filter"].(map[string]any)["expression"])
}

func TestBuildSignozAlertRule_Errors(t *testing.T) {
	_, err := buildSignozAlertRule(AlertRuleConfig{Name: "x"}, signozclient.RuleSchemaV5, []string{"c"})
	assert.ErrorContains(t, err, "needs a query")

	_, err = buildSignozAlertRule(AlertRuleConfig{
		Name: "x", Query: "x", ProviderConfig: map[string]any{"threshold": 1, "threshold_operator": "~"},
	}, signozclient.RuleSchemaV5, []string{"c"})
	assert.ErrorContains(t, err, "unsupported SigNoz threshold operator")
}

func TestSignozChannelsForTokens_MatchesOnTokenNotPath(t *testing.T) {
	body := []byte(`{"status":"success","data":[
		{"name":"this-account","type":"webhook","data":"{\"name\":\"this-account\",\"webhook_configs\":[{\"url\":\"https://app.example.com/api/webhooks/signoz?token=tok-a\"}]}"},
		{"name":"other-account","type":"webhook","data":"{\"name\":\"other-account\",\"webhook_configs\":[{\"url\":\"https://app.example.com/api/webhooks/signoz?token=tok-b\"}]}"},
		{"name":"no-token","type":"webhook","data":"{\"name\":\"no-token\",\"webhook_configs\":[{\"url\":\"https://app.example.com/api/webhooks/signoz\"}]}"},
		{"name":"slack","type":"slack","data":"{\"name\":\"slack\",\"slack_configs\":[{\"api_url\":\"https://hooks.example.com/x?token=tok-a\"}]}"},
		{"name":"broken","type":"webhook","data":"not json"}
	]}`)
	assert.Equal(t, []string{"this-account"}, signozChannelsForTokens(body, []string{"tok-a"}))
	assert.Empty(t, signozChannelsForTokens(body, []string{"tok-c"}))
	assert.Empty(t, signozChannelsForTokens([]byte(`not json`), []string{"tok-a"}))
}

func TestParseSignozRuleList_V0145(t *testing.T) {
	body, err := os.ReadFile("testdata/signoz_v0145_rules.json")
	require.NoError(t, err)
	rules, err := parseSignozRuleList(body)
	require.NoError(t, err)
	require.Len(t, rules, 5)

	byName := map[string]ExternalAlertRule{}
	for _, r := range rules {
		ext, ok := r.external()
		require.True(t, ok)
		byName[ext.Name] = ext
	}

	mem := byName["nbtest demo pod memory high"]
	assert.Equal(t, "01a13ef7-e690-7691-b987-4dbb45e45058", mem.ExternalRuleId)
	assert.Equal(t, "metric", mem.AlertType)
	assert.Equal(t, "critical", mem.Severity)
	assert.True(t, mem.Enabled)
	assert.Contains(t, mem.Query, `"k8s.pod.memory.working_set"`)
	assert.Equal(t, "", mem.Duration)
	assert.Equal(t, "promql_rule", mem.ProviderConfig["rule_type"])

	wrapped := byName["nbtest wrapped up==0"]
	assert.Equal(t, `max by ("k8s.pod.name") ({"k8s.container.restarts", "k8s.namespace.name"="demo"}) > 100`, wrapped.Query,
		"the Prometheus-semantics wrapper Nudgebee adds is removed on read")
	assert.Equal(t, "2m0s", wrapped.Duration)

	logs := byName["nbtest log errors"]
	assert.Equal(t, "log", logs.AlertType)
	assert.Equal(t, `k8s.namespace.name = 'demo' AND body CONTAINS 'error'`, logs.Query)
}

func TestParseSignozRuleList_LegacyShape(t *testing.T) {
	body := []byte(`{"status":"success","data":{"rules":[
		{"id":7,"alert":"legacy","alertType":"METRIC_BASED_ALERT","ruleType":"promql_rule","disabled":true,
		 "labels":{"severity":"warning"},"evalWindow":"5m0s",
		 "condition":{"compositeQuery":{"queryType":"promql","promQueries":{"A":{"query":"up == 0"}}},"op":"1","target":0,"matchType":"1"}},
		{"id":"", "alert":"no id"}
	]}}`)
	rules, err := parseSignozRuleList(body)
	require.NoError(t, err)
	require.Len(t, rules, 2)

	ext, ok := rules[0].external()
	require.True(t, ok)
	assert.Equal(t, "7", ext.ExternalRuleId)
	assert.Equal(t, "up == 0", ext.Query)
	assert.False(t, ext.Enabled)

	_, ok = rules[1].external()
	assert.False(t, ok)
}

func TestParseSignozRuleList_EmptyAndBareArray(t *testing.T) {
	rules, err := parseSignozRuleList([]byte(`{"status":"success","data":{"rules":[]}}`))
	require.NoError(t, err)
	assert.Empty(t, rules)

	rules, err = parseSignozRuleList([]byte(`{"status":"success","data":[{"id":"a","alert":"b"}]}`))
	require.NoError(t, err)
	assert.Len(t, rules, 1)
}

func TestSignozAlertRuleSourceIsListable(t *testing.T) {
	assert.True(t, SupportsListing("signoz", "user"))
}
