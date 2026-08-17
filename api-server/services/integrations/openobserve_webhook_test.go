package integrations

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/event"
)

// decodeOpenObservePayload mirrors what ProcessEventWebook does before handing
// the body to the mapper, so tests feed the mapper exactly what production does.
func decodeOpenObservePayload(t *testing.T, body string) map[string]any {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &raw))
	return raw
}

// canonicalOpenObserveBody is the template shipped in the setup dialog, rendered
// for a pod-scoped alert.
const canonicalOpenObserveBody = `{
  "alert_name": "HighErrorRate",
  "alert_type": "scheduled",
  "stream_name": "k8s_logs",
  "stream_type": "logs",
  "org_name": "default",
  "alert_period": "5",
  "alert_operator": ">=",
  "alert_threshold": "10",
  "alert_count": "42",
  "alert_agg_value": "97.5",
  "alert_start_time": "2026-08-18T10:00:00Z",
  "alert_end_time": "2026-08-18T10:05:00Z",
  "alert_url": "https://o2.example.com/web/alerts?org_identifier=default",
  "severity": "critical",
  "k8s_namespace_name": "prod",
  "k8s_pod_name": "checkout-7d9f-abcde",
  "k8s_deployment_name": "checkout",
  "k8s_cluster_name": "us-east-1",
  "service_name": "checkout"
}`

func TestMapOpenObserveAlertToEvent_CanonicalTemplate(t *testing.T) {
	got, fields, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, canonicalOpenObserveBody))
	require.NoError(t, err)

	assert.Equal(t, "acct-1", got.AccountId)
	assert.Equal(t, openObserveEventType, got.EventType)
	assert.Equal(t, "HighErrorRate (stream: k8s_logs)", got.EventTitle)
	assert.Equal(t, "https://o2.example.com/web/alerts?org_identifier=default", got.EventUrl)
	assert.Equal(t, string(event.EventStatusFiring), got.EventStatus)
	assert.Equal(t, string(event.EventPriorityHigh), got.EventPriority)
	assert.Equal(t, time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC), got.EventCreatedAt)
	assert.Equal(t, time.Date(2026, 8, 18, 10, 5, 0, 0, time.UTC), got.EventEndsAt)

	// Pod beats deployment and service for the subject; both remain candidates.
	assert.Equal(t, "pod", got.EventSubjectKind)
	assert.Equal(t, "checkout-7d9f-abcde", got.EventSubjectName)
	assert.Equal(t, "prod", got.EventSubjectNamespace)
	assert.Equal(t, "checkout-7d9f-abcde,checkout", got.Investigation.Labels["nb_workload_candidates"])

	assert.Equal(t, "HighErrorRate", got.Investigation.RuleName)
	assert.Equal(t, "HighErrorRate", got.Investigation.RuleId)
	assert.Equal(t, IntegrationOpenObserveWebhook, got.Investigation.RuleType)
	assert.Equal(t, event.EventStatusFiring, got.Investigation.Status)
	assert.Equal(t, event.EventPriorityHigh, got.Investigation.Severity)
	assert.NotEmpty(t, got.Investigation.Fingerprint)
	assert.Equal(t, got.Investigation.Fingerprint, got.EventId)

	assert.Contains(t, got.EventDescription, "Condition: >= 10 over 5")
	assert.Contains(t, got.EventDescription, "Matching records: 42")
	assert.Contains(t, got.EventDescription, "Aggregated value: 97.5")

	assert.Subset(t, got.EventTags, []string{"default", "k8s_logs", "prod", "us-east-1"})

	// Alias resolution is written back under canonical keys for the caller.
	assert.Equal(t, "default", fields["org_name"])
	assert.Equal(t, "k8s_logs", fields["stream_name"])
	assert.Equal(t, "logs", fields["stream_type"])

	// One evidence block (the alert table); no rows in this payload.
	require.Len(t, got.Investigation.Evidences, 1)
	assert.Equal(t, "table", got.Investigation.Evidences[0].Type)
}

func TestMapOpenObserveAlertToEvent_DropsUnsubstitutedPlaceholders(t *testing.T) {
	body := `{
	  "alert_name": "DiskPressure",
	  "stream_name": "default",
	  "org_name": "default",
	  "k8s_pod_name": "{k8s_pod_name}",
	  "k8s_namespace_name": "{k8s_namespace_name}",
	  "service_name": "{service_name}",
	  "severity": "{severity}",
	  "alert_start_time": "{alert_start_time}"
	}`

	got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
	require.NoError(t, err)

	// A template referencing stream fields the alert does not carry must not
	// produce literal "{k8s_pod_name}" subjects or labels.
	assert.Empty(t, got.EventSubjectName)
	assert.Empty(t, got.EventSubjectKind)
	assert.Empty(t, got.EventSubjectNamespace)
	assert.NotContains(t, got.Investigation.Labels, "k8s_pod_name")
	assert.NotContains(t, got.Investigation.Labels, "service_name")
	assert.NotContains(t, got.Investigation.Labels, "severity")
	assert.NotContains(t, got.Investigation.Labels, "nb_workload_candidates")

	// No parseable start time → now(), never the zero time.
	assert.False(t, got.EventCreatedAt.IsZero())
	assert.WithinDuration(t, time.Now(), got.EventCreatedAt, time.Minute)

	// Unknown severity defaults to medium rather than burying the alert.
	assert.Equal(t, string(event.EventPriorityMedium), got.EventPriority)
}

// liveOpenObserveBody is a verbatim delivery captured from OpenObserve v0.80.2
// (read back from event_incoming_webhooks.raw). It is the ground truth for three
// behaviours no documentation states: unsubstituted variables arrive as literal
// braces, alert_start_time carries no timezone, and stream-field variables are
// comma-joined aggregates across every row the alert matched.
const liveOpenObserveBody = `{"alert_name":"nudgebee_ngrok_test","alert_type":"scheduled",` +
	`"stream_name":"k8s_events","stream_type":"logs","org_name":"default","alert_period":"5",` +
	`"alert_operator":">=","alert_threshold":"1","alert_count":"100",` +
	`"alert_agg_value":"{alert_agg_value}","alert_start_time":"2026-08-21T04:10:26",` +
	`"alert_end_time":"2026-08-21T04:14:25",` +
	`"alert_url":"/web/short/3ae9dca26668f217?org_identifier=default","severity":"0",` +
	`"k8s_cluster_name":"{k8s_cluster_name}",` +
	`"k8s_namespace_name":"traefik, nudgebee, elasticsearch, kube-system, datadog",` +
	`"k8s_pod_name":"{k8s_pod_name}","k8s_deployment_name":"{k8s_deployment_name}",` +
	`"k8s_node_name":"{k8s_node_name}","service_name":"{service_name}"}`

func TestMapOpenObserveAlertToEvent_LiveCapturedPayload(t *testing.T) {
	got, fields, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, liveOpenObserveBody))
	require.NoError(t, err)

	assert.Equal(t, "nudgebee_ngrok_test (stream: k8s_events)", got.EventTitle)
	assert.Equal(t, string(event.EventStatusFiring), got.EventStatus)

	// severity "0" is not a severity word → default medium.
	assert.Equal(t, string(event.EventPriorityMedium), got.EventPriority)

	// alert_start_time has no timezone offset; it must still parse.
	assert.Equal(t, time.Date(2026, 8, 21, 4, 10, 26, 0, time.UTC), got.EventCreatedAt)
	assert.Equal(t, time.Date(2026, 8, 21, 4, 14, 25, 0, time.UTC), got.EventEndsAt)

	// Unsubstituted variables must not leak into labels or the description.
	for _, key := range []string{"k8s_pod_name", "k8s_deployment_name", "k8s_node_name", "service_name", "k8s_cluster_name", "alert_agg_value"} {
		assert.NotContains(t, got.Investigation.Labels, key, "placeholder %s leaked into labels", key)
	}
	assert.NotContains(t, got.EventDescription, "Aggregated value")
	assert.Contains(t, got.EventDescription, "Matching records: 100")

	// Bug 1: a site-relative alert_url must never be published as-is; the mapper
	// leaves it empty for ProcessEventWebook to absolutise.
	assert.Empty(t, got.EventUrl, "relative alert_url must not become the event URL")
	assert.Equal(t, "/web/short/3ae9dca26668f217?org_identifier=default", fields["alert_url"],
		"raw alert_url must survive for the caller to absolutise")

	// Bug 2: an aggregated namespace is not one namespace.
	assert.Empty(t, got.EventSubjectNamespace)
	assert.Equal(t, "traefik,nudgebee,elasticsearch,kube-system,datadog", got.Investigation.Labels["namespaces"])
	assert.NotContains(t, got.Investigation.Labels, "namespace")

	// ...but every namespace stays filterable as a tag.
	assert.Subset(t, got.EventTags, []string{"traefik", "nudgebee", "elasticsearch", "kube-system", "datadog"})
}

func TestMapOpenObserveAlertToEvent_AggregatedFieldsKeepFingerprintStable(t *testing.T) {
	// Bug 3: three real fires of one alert carried three different namespace
	// aggregates as the evaluation window slid. The fingerprint is the event's
	// identity, so it must not move with them — otherwise every fire opens a new
	// event and no resolution can ever close one.
	windows := []string{
		"traefik, nudgebee, elasticsearch, kube-system, datadog",
		"datadog, traefik, kube-system, iteration-test, actions-runner-system-1, nudgebee, elasticsearch",
		"default, nudgebee, datadog, kube-system",
	}

	var first string
	for i, ns := range windows {
		payload := decodeOpenObservePayload(t, liveOpenObserveBody)
		payload["k8s_namespace_name"] = ns
		got, _, err := mapOpenObserveAlertToEvent("acct-1", payload)
		require.NoError(t, err)
		if i == 0 {
			first = got.Investigation.Fingerprint
			require.NotEmpty(t, first)
			continue
		}
		assert.Equal(t, first, got.Investigation.Fingerprint,
			"window %d produced a different fingerprint — dedup would break", i)
	}

	// A genuinely single-namespace alert still gets its own identity, so distinct
	// namespaces do not collapse into one event.
	single := decodeOpenObservePayload(t, liveOpenObserveBody)
	single["k8s_namespace_name"] = "payments"
	scoped, _, err := mapOpenObserveAlertToEvent("acct-1", single)
	require.NoError(t, err)
	assert.Equal(t, "payments", scoped.EventSubjectNamespace)
	assert.NotEqual(t, first, scoped.Investigation.Fingerprint)
}

func TestExtractOpenObserveSubject_AggregatedWorkloads(t *testing.T) {
	// Multiple pods cannot name one subject, but all remain match candidates.
	multi := map[string]string{"k8s_pod_name": "api-0, api-1, worker-2"}
	kind, name, candidates := extractOpenObserveSubject(multi)
	assert.Empty(t, kind)
	assert.Empty(t, name)
	assert.Equal(t, []string{"api-0", "api-1", "worker-2"}, candidates)

	// A single pod still names the subject, as before.
	single := map[string]string{"k8s_pod_name": "api-0", "k8s_deployment_name": "api"}
	kind, name, candidates = extractOpenObserveSubject(single)
	assert.Equal(t, "pod", kind)
	assert.Equal(t, "api-0", name)
	assert.Equal(t, []string{"api-0", "api"}, candidates)
}

func TestResolveOpenObserveEventURL(t *testing.T) {
	const base = "https://o2.example.com"
	tests := []struct {
		name    string
		rawURL  string
		baseURL string
		want    string
	}{
		{"absolute passes through", "https://o2.example.com/web/short/abc", base, "https://o2.example.com/web/short/abc"},
		{"relative is joined to base", "/web/short/abc?org_identifier=default", base, "https://o2.example.com/web/short/abc?org_identifier=default"},
		{"relative without leading slash", "web/short/abc", base, "https://o2.example.com/web/short/abc"},
		{"base with trailing slash", "/web/short/abc", "https://o2.example.com/", "https://o2.example.com/web/short/abc"},
		{"relative with no base yields nothing rather than a broken link", "/web/short/abc", "", ""},
		{"absent alert_url synthesizes a deep link", "", base, "https://o2.example.com/web/alerts?org_identifier=acme&stream_name=k8s_events&stream_type=logs"},
		{"absent alert_url and no base", "", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOpenObserveEventURL(tc.rawURL, tc.baseURL, "acme", "k8s_events", "logs", "")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEventRuleSeverity(t *testing.T) {
	// event_rules.severity is FK-constrained to exactly {"critical","warning"}.
	// The live k8s_events stream renders severity as "0", which previously
	// reached the column verbatim and failed the constraint, silently losing the
	// rule upsert.
	assert.Equal(t, "critical", eventRuleSeverity(event.EventPriorityHigh))
	for _, p := range []event.EventPriority{
		event.EventPriorityMedium,
		event.EventPriorityLow,
		event.EventPriorityInfo,
		event.EventPriorityDebug,
		event.EventPriority(""),
	} {
		assert.Equal(t, "warning", eventRuleSeverity(p), "priority %q", p)
	}

	// The live payload's numeric severity maps to medium → "warning", never "0".
	got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, liveOpenObserveBody))
	require.NoError(t, err)
	assert.Equal(t, "0", got.Investigation.Labels["severity"], "raw value is still preserved as a label")
	assert.Equal(t, "warning", eventRuleSeverity(got.Investigation.Severity))
}

func TestSplitOpenObserveMultiValue(t *testing.T) {
	assert.Nil(t, splitOpenObserveMultiValue(""))
	assert.Equal(t, []string{"one"}, splitOpenObserveMultiValue("one"))
	assert.Equal(t, []string{"a", "b", "c"}, splitOpenObserveMultiValue("a, b,c"))
	assert.Equal(t, []string{"a", "b"}, splitOpenObserveMultiValue(" a , , b ,"))

	assert.Equal(t, "solo", singleOpenObserveValue("solo"))
	assert.Empty(t, singleOpenObserveValue("a, b"))
	assert.Empty(t, singleOpenObserveValue(""))
}

func TestMapOpenObserveAlertToEvent_MissingAlertNameIsAnError(t *testing.T) {
	body := `{"stream_name": "default", "org_name": "default"}`

	_, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing alert name")
}

func TestMapOpenObserveAlertToEvent_EpochAndDottedKeys(t *testing.T) {
	// Microsecond epochs (OpenObserve's native _timestamp unit) and OTel-style
	// dotted keys, both of which a hand-written template can produce.
	body := `{
	  "alertname": "PodRestarts",
	  "stream": "k8s_events",
	  "organization": "acme",
	  "alert_start_time": 1786997559724000,
	  "k8s.namespace.name": "payments",
	  "k8s.pod.name": "api-0"
	}`

	got, fields, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
	require.NoError(t, err)

	assert.Equal(t, time.UnixMicro(1786997559724000).UTC(), got.EventCreatedAt)
	assert.Equal(t, "pod", got.EventSubjectKind)
	assert.Equal(t, "api-0", got.EventSubjectName)
	assert.Equal(t, "payments", got.EventSubjectNamespace)
	assert.Equal(t, "acme", fields["org_name"])
	assert.Equal(t, "k8s_events", fields["stream_name"])
	assert.Equal(t, "PodRestarts", got.Investigation.RuleName)
}

func TestMapOpenObserveAlertToEvent_StatusAndSeverityAliases(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantStatus   event.EventStatus
		wantPriority event.EventPriority
	}{
		{
			name:         "explicit resolved status",
			body:         `{"alert_name":"A","status":"resolved","severity":"warning"}`,
			wantStatus:   event.EventStatusResolved,
			wantPriority: event.EventPriorityMedium,
		},
		{
			name:         "alert_type spelling a severity is honoured",
			body:         `{"alert_name":"A","alert_type":"critical"}`,
			wantStatus:   event.EventStatusFiring,
			wantPriority: event.EventPriorityHigh,
		},
		{
			name:         "alert_type spelling a trigger mode is not a severity",
			body:         `{"alert_name":"A","alert_type":"realtime"}`,
			wantStatus:   event.EventStatusFiring,
			wantPriority: event.EventPriorityMedium,
		},
		{
			name:         "level alias",
			body:         `{"alert_name":"A","level":"info"}`,
			wantStatus:   event.EventStatusFiring,
			wantPriority: event.EventPriorityInfo,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, tc.body))
			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, got.Investigation.Status)
			assert.Equal(t, tc.wantPriority, got.Investigation.Severity)
		})
	}
}

func TestMapOpenObserveAlertToEvent_FingerprintStability(t *testing.T) {
	first, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, canonicalOpenObserveBody))
	require.NoError(t, err)

	// Same alert, later evaluation window: different times and counts, same key.
	refired := decodeOpenObservePayload(t, canonicalOpenObserveBody)
	refired["alert_start_time"] = "2026-08-18T11:00:00Z"
	refired["alert_count"] = "77"
	second, _, err := mapOpenObserveAlertToEvent("acct-1", refired)
	require.NoError(t, err)
	assert.Equal(t, first.Investigation.Fingerprint, second.Investigation.Fingerprint,
		"a re-fire of the same alert must dedupe onto the same event")

	// Same rule, different pod: must not collapse into one event.
	otherPod := decodeOpenObservePayload(t, canonicalOpenObserveBody)
	otherPod["k8s_pod_name"] = "checkout-7d9f-zzzzz"
	third, _, err := mapOpenObserveAlertToEvent("acct-1", otherPod)
	require.NoError(t, err)
	assert.NotEqual(t, first.Investigation.Fingerprint, third.Investigation.Fingerprint)

	// Same rule name in a different organization: also distinct.
	otherOrg := decodeOpenObservePayload(t, canonicalOpenObserveBody)
	otherOrg["org_name"] = "staging"
	fourth, _, err := mapOpenObserveAlertToEvent("acct-1", otherOrg)
	require.NoError(t, err)
	assert.NotEqual(t, first.Investigation.Fingerprint, fourth.Investigation.Fingerprint)
}

func TestMapOpenObserveAlertToEvent_RowsBecomeEvidenceNotLabels(t *testing.T) {
	t.Run("array rows", func(t *testing.T) {
		body := `{
		  "alert_name": "SlowQueries",
		  "stream_name": "traces",
		  "rows": [{"duration_ms": 4210, "route": "/checkout"}, {"duration_ms": 3980, "route": "/cart"}]
		}`
		got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
		require.NoError(t, err)

		require.Len(t, got.Investigation.Evidences, 2)
		assert.Equal(t, "json", got.Investigation.Evidences[1].Type)
		data, ok := got.Investigation.Evidences[1].Data.(map[string]any)
		require.True(t, ok, "rows evidence data must be a map")
		assert.Equal(t, "OpenObserve Matching Records", data["name"])

		// Record dumps must not pollute the alert-labels table.
		for k := range got.Investigation.Labels {
			assert.NotContains(t, k, "rows")
		}
	})

	t.Run("string rows", func(t *testing.T) {
		body := `{"alert_name":"SlowQueries","rows":"route=/checkout duration=4210ms"}`
		got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
		require.NoError(t, err)

		require.Len(t, got.Investigation.Evidences, 2)
		assert.Equal(t, "markdown", got.Investigation.Evidences[1].Type)
	})

	t.Run("placeholder rows produce no evidence", func(t *testing.T) {
		body := `{"alert_name":"SlowQueries","rows":"{rows}"}`
		got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
		require.NoError(t, err)
		require.Len(t, got.Investigation.Evidences, 1)
	})
}

func TestMapOpenObserveAlertToEvent_TruncatesOversizedLabels(t *testing.T) {
	raw := map[string]any{
		"alert_name": "Noisy",
		"message":    strings.Repeat("x", openObserveMaxLabelValueLen+50),
		"multibyte":  strings.Repeat("é", openObserveMaxLabelValueLen+50),
		"emoji":      strings.Repeat("🚨", openObserveMaxLabelValueLen+50),
		// Exactly at the cap: must be returned untouched, no ellipsis.
		"at_boundary":    strings.Repeat("é", openObserveMaxLabelValueLen),
		"under_boundary": strings.Repeat("🚨", openObserveMaxLabelValueLen-1),
		"short_field":    "kept",
	}

	got, _, err := mapOpenObserveAlertToEvent("acct-1", raw)
	require.NoError(t, err)
	labels := got.Investigation.Labels

	assert.Len(t, []rune(labels["message"]), openObserveMaxLabelValueLen+1) // +1 for the ellipsis

	// Truncation must cut on rune boundaries, not bytes, so the stored value is
	// still valid UTF-8 for multi-byte characters and emojis alike.
	for _, key := range []string{"multibyte", "emoji"} {
		assert.Len(t, []rune(labels[key]), openObserveMaxLabelValueLen+1, "key %s", key)
		assert.True(t, utf8.ValidString(labels[key]), "key %s must remain valid UTF-8", key)
		assert.True(t, strings.HasSuffix(labels[key], "…"), "key %s must be marked truncated", key)
	}

	// Values at or under the cap are passed through verbatim.
	assert.Equal(t, raw["at_boundary"], labels["at_boundary"])
	assert.Equal(t, raw["under_boundary"], labels["under_boundary"])
	assert.Equal(t, "kept", labels["short_field"])
}

func TestMapOpenObserveAlertToEvent_IgnoresReservedLabelNamespace(t *testing.T) {
	// The payload is user-authored, so a template must not be able to render
	// NudgeBee's control labels and steer core subject enrichment.
	body := `{
	  "alert_name": "Spoofed",
	  "k8s_pod_name": "api-0",
	  "nb_skip_workload_match": "true",
	  "nb_workload_candidates": "someone-elses-workload",
	  "nb_webhook_source": "pagerduty_webhook"
	}`

	got, _, err := mapOpenObserveAlertToEvent("acct-1", decodeOpenObservePayload(t, body))
	require.NoError(t, err)

	assert.NotContains(t, got.Investigation.Labels, "nb_skip_workload_match")
	assert.NotContains(t, got.Investigation.Labels, "nb_webhook_source")
	// The candidate list is the parser's own, not the payload's.
	assert.Equal(t, "api-0", got.Investigation.Labels["nb_workload_candidates"])
}

func TestMapOpenObserveAlertToEvent_CapsLabelCount(t *testing.T) {
	raw := map[string]any{
		"alert_name":         "WideRecord",
		"stream_name":        "k8s_logs",
		"severity":           "critical",
		"k8s_namespace_name": "prod",
		"k8s_pod_name":       "api-0",
	}
	for i := 0; i < openObserveMaxLabels*2; i++ {
		raw[fmt.Sprintf("zz_field_%04d", i)] = "noise"
	}

	got, _, err := mapOpenObserveAlertToEvent("acct-1", raw)
	require.NoError(t, err)

	// Cap applies to the copied payload; the identifying labels are re-applied
	// afterwards, so they survive even though they sort late.
	assert.LessOrEqual(t, len(got.Investigation.Labels), openObserveMaxLabels+10)
	assert.Equal(t, "true", got.Investigation.Labels["nb_labels_truncated"])
	assert.Equal(t, "WideRecord", got.Investigation.Labels["alertname"])
	assert.Equal(t, "critical", got.Investigation.Labels["severity"])
	assert.Equal(t, "k8s_logs", got.Investigation.Labels["stream_name"])
	assert.Equal(t, "prod", got.Investigation.Labels["namespace"])
	assert.Equal(t, "api-0", got.Investigation.Labels["nb_workload_candidates"])
	assert.Equal(t, "api-0", got.EventSubjectName)
}

func TestParseOpenObserveTime(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{"empty", "", time.Time{}},
		{"unparseable", "not-a-time", time.Time{}},
		{"zero epoch", "0", time.Time{}},
		{"rfc3339", "2026-08-18T10:00:00Z", time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)},
		{"rfc3339 nano", "2026-08-18T10:00:00.123456789Z", time.Date(2026, 8, 18, 10, 0, 0, 123456789, time.UTC)},
		{"no timezone", "2026-08-18T10:00:00", time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)},
		{"space separated", "2026-08-18 10:00:00", time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)},
		{"epoch seconds", "1786997559", time.Unix(1786997559, 0).UTC()},
		{"epoch millis", "1786997559724", time.UnixMilli(1786997559724).UTC()},
		{"epoch micros", "1786997559724000", time.UnixMicro(1786997559724000).UTC()},
		{"epoch nanos", "1786997559724000000", time.Unix(0, 1786997559724000000).UTC()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseOpenObserveTime(tc.in))
		})
	}
}

func TestCleanOpenObserveValue(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"  prod  ", "prod"},
		{"{alert_name}", ""},
		{"{}", ""},
		{"{k8s.pod.name}", ""},
		{"null", ""},
		{"NONE", ""},
		{"{not a placeholder}", "{not a placeholder}"},
		{"value{with}braces", "value{with}braces"},
		{"0", "0"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, cleanOpenObserveValue(tc.in))
		})
	}
}

func TestOpenObserveWebhookIntegrationContract(t *testing.T) {
	m := OpenObserveWebhook{}
	assert.Equal(t, IntegrationOpenObserveWebhook, m.Name())
	assert.Equal(t, "openobserve_webhook", m.Name())

	schema := m.ConfigSchema()
	for _, key := range []string{"integration_config_name", "account_id", "token"} {
		assert.Contains(t, schema.Properties, key, "config schema must expose %s", key)
	}
	assert.Empty(t, m.ValidateConfig(nil, nil, ""))
}
