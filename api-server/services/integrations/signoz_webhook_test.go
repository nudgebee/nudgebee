package integrations

import (
	"errors"
	"os"
	"testing"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registeredSigNozRule struct {
	alert, severity, ruleId string
}

// stubSigNozRuleRegistration records rule upserts instead of writing them.
func stubSigNozRuleRegistration(t *testing.T) *[]registeredSigNozRule {
	t.Helper()
	var got []registeredSigNozRule
	prev := signozRegisterEventRule
	signozRegisterEventRule = func(_ *security.RequestContext, _ string, alertName, severity, ruleId string, _ map[string]string) {
		got = append(got, registeredSigNozRule{alertName, severity, ruleId})
	}
	t.Cleanup(func() { signozRegisterEventRule = prev })
	return &got
}

// testdata/signoz_v0145_firing.json is a notification a SigNoz v0.145.0 webhook
// channel delivered for a PromQL rule grouped by OTel k8s attributes.
func TestSigNozWebhook_ParsesV0145Notification(t *testing.T) {
	registered := stubSigNozRuleRegistration(t)
	payload, err := os.ReadFile("testdata/signoz_v0145_firing.json")
	require.NoError(t, err)

	events, err := SigNozWebhook{}.ProcessEventWebook(nil, nil, "acct-1", string(payload))
	require.NoError(t, err)
	require.Len(t, events, 1)
	e := events[0]

	assert.Equal(t, "acct-1", e.AccountId)
	assert.Equal(t, "nbtest demo pod memory high", e.EventType)
	assert.Equal(t, "Demo pod memory high", e.EventTitle)
	assert.Equal(t, "b591fe39e835adef", e.EventId)
	assert.Equal(t, "deployment", e.EventSubjectKind, "k8s.deployment.name is the workload")
	assert.Equal(t, "kafka", e.EventSubjectName)
	assert.Equal(t, "demo", e.EventSubjectNamespace, "k8s.namespace.name is the namespace")
	assert.Equal(t, string(mapGrafanaSeverity("critical")), e.EventPriority)
	assert.Equal(t, "http://localhost:8080/alerts/overview?ruleId=01a13ef7-e690-7691-b987-4dbb45e45058", e.EventUrl)
	assert.True(t, e.EventEndsAt.IsZero(), "SigNoz sends 0001-01-01 for a firing alert")
	assert.Equal(t, time.Date(2026, 10, 15, 9, 50, 30, 335905971, time.UTC), e.EventCreatedAt.UTC())

	inv := e.Investigation
	assert.Equal(t, "signoz", inv.RuleType)
	assert.Equal(t, "nbtest demo pod memory high", inv.RuleId, "rule id is the alert name, as for every Alertmanager-family source")
	assert.Equal(t, "01a13ef7-e690-7691-b987-4dbb45e45058", inv.Labels["ruleId"])
	assert.Equal(t, "demo", inv.Labels["namespace"])
	assert.Equal(t, "kafka", inv.Labels["service"])
	assert.Equal(t, "kafka-5d8d6bb8f6-j6jsq", inv.Labels["k8s.pod.name"], "original OTel labels are kept")
	require.Len(t, inv.Evidences, 1)

	require.Len(t, *registered, 1)
	assert.Equal(t, registeredSigNozRule{"nbtest demo pod memory high", "critical", "01a13ef7-e690-7691-b987-4dbb45e45058"}, (*registered)[0])
}

func TestSigNozWebhook_PodOnlyAlertKeepsPodSubject(t *testing.T) {
	stubSigNozRuleRegistration(t)
	payload := `{"status":"firing","alerts":[{"status":"firing",
		"labels":{"alertname":"restarts","k8s.namespace.name":"shop","k8s.pod.name":"cart-7d9-abc","ruleId":"r1","severity":"warning"},
		"annotations":{},"startsAt":"2026-10-15T09:50:05Z","endsAt":"0001-01-01T00:00:00Z","fingerprint":"f1"}]}`
	events, err := SigNozWebhook{}.ProcessEventWebook(nil, nil, "acct", payload)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "pod", events[0].EventSubjectKind)
	assert.Equal(t, "cart-7d9-abc", events[0].EventSubjectName)
	assert.Equal(t, "shop", events[0].EventSubjectNamespace)
	assert.Equal(t, "restarts", events[0].EventTitle)
	assert.Empty(t, events[0].Investigation.Labels["service"], "a pod name is not backfilled as the service")
}

func TestSigNozWebhook_CommonLabelsFillGapsWithoutOverriding(t *testing.T) {
	stubSigNozRuleRegistration(t)
	payload := `{"status":"resolved","commonLabels":{"severity":"critical","k8s.namespace.name":"common-ns"},
		"alerts":[{"status":"resolved","labels":{"alertname":"a","k8s.namespace.name":"own-ns","service.name":"checkout"},
		"annotations":{"summary":"s"},"startsAt":"2026-10-15T09:50:05Z","endsAt":"2026-10-15T09:55:05Z","fingerprint":"f"}]}`
	events, err := SigNozWebhook{}.ProcessEventWebook(nil, nil, "acct", payload)
	require.NoError(t, err)
	e := events[0]
	assert.Equal(t, "own-ns", e.EventSubjectNamespace)
	assert.Equal(t, "service", e.EventSubjectKind)
	assert.Equal(t, "checkout", e.EventSubjectName)
	assert.Equal(t, string(mapGrafanaSeverity("critical")), e.EventPriority)
	assert.False(t, e.EventEndsAt.IsZero())
}

func TestSigNozWebhook_RejectsNonAlertmanagerPayloads(t *testing.T) {
	stubSigNozRuleRegistration(t)
	_, err := SigNozWebhook{}.ProcessEventWebook(nil, nil, "acct", `not json`)
	assert.ErrorContains(t, err, "failed to parse")

	_, err = SigNozWebhook{}.ProcessEventWebook(nil, nil, "acct", `{"status":"firing"}`)
	assert.ErrorContains(t, err, "no 'alerts' array")

	_, err = SigNozWebhook{}.ProcessEventWebook(nil, nil, "acct", `{"alerts":[]}`)
	assert.True(t, errors.Is(err, core.ErrEventNotSupported), "an empty group flush is skipped, not failed")
}

func TestSigNozWebhookIdentity(t *testing.T) {
	assert.Equal(t, "signoz_webhook", SigNozWebhook{}.Name())
	assert.Equal(t, core.IntegrationCategoryIncidentWebhook, SigNozWebhook{}.Category())
}
