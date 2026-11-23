package observability

import (
	"errors"
	"nudgebee/services/security"
	"strings"
	"testing"
	"time"
)

var alertNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func esStatus(providerType string, connected bool) ProviderConnectionStatus {
	s := ProviderConnectionStatus{
		ProviderType:    providerType,
		Provider:        "elasticsearch",
		Source:          "user",
		IntegrationId:   "es-1",
		IntegrationName: "prod-es",
		Connected:       connected,
	}
	if !connected {
		s.Error = "dial tcp 10.0.0.1:9200: connect: connection refused"
	}
	return s
}

func failingFor(providerType string, d time.Duration, alerted bool) ProviderConnectionStatus {
	s := esStatus(providerType, false)
	s.FailingSince = alertNow.Add(-d).Format(time.RFC3339)
	s.Alerted = alerted
	return s
}

func TestApplyAlertState_FirstFailureStartsTheHoldWithoutAlerting(t *testing.T) {
	statuses := []ProviderConnectionStatus{esStatus("logs", false)}
	failing, recovered := applyAlertState(nil, statuses, alertNow, true)
	if len(failing) != 0 || len(recovered) != 0 {
		t.Fatalf("one failed probe must not alert, got failing=%v recovered=%v", failing, recovered)
	}
	if statuses[0].FailingSince != alertNow.Format(time.RFC3339) || statuses[0].Alerted {
		t.Fatalf("want failure run started now and not alerted, got %+v", statuses[0])
	}
}

func TestApplyAlertState_SecondCheckPastTheHoldAlertsOncePerIntegration(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{
		"logs":    failingFor("logs", 10*time.Minute, false),
		"metrics": failingFor("metrics", 10*time.Minute, false),
	}
	statuses := []ProviderConnectionStatus{esStatus("logs", false), esStatus("metrics", false)}
	failing, _ := applyAlertState(prev, statuses, alertNow, true)
	if len(failing) != 1 {
		t.Fatalf("want one alert for the integration serving both signals, got %d", len(failing))
	}
	if got := strings.Join(failing[0].Signals, ","); got != "logs,metrics" {
		t.Fatalf("signals = %q, want logs,metrics", got)
	}
	if failing[0].FailingSince != prev["logs"].FailingSince {
		t.Fatalf("failure run must carry over: got %q want %q", failing[0].FailingSince, prev["logs"].FailingSince)
	}
	for _, s := range statuses {
		if !s.Alerted {
			t.Fatalf("%s not marked alerted", s.ProviderType)
		}
	}
}

func TestApplyAlertState_StillWithinTheHoldDoesNotAlert(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{"logs": failingFor("logs", 2*time.Minute, false)}
	statuses := []ProviderConnectionStatus{esStatus("logs", false)}
	if failing, _ := applyAlertState(prev, statuses, alertNow, true); len(failing) != 0 {
		t.Fatalf("a post-save re-check minutes after the first failure must not alert, got %v", failing)
	}
}

func TestApplyAlertState_AlreadyAlertedIsNotRepeated(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{"logs": failingFor("logs", 2*time.Hour, true)}
	statuses := []ProviderConnectionStatus{esStatus("logs", false)}
	if failing, _ := applyAlertState(prev, statuses, alertNow, true); len(failing) != 0 {
		t.Fatalf("a failure already announced must not be announced every 10 minutes, got %v", failing)
	}
	if !statuses[0].Alerted {
		t.Fatal("alerted flag must carry over, or the next run alerts again")
	}
}

func TestApplyAlertState_RecoveryIsAnnouncedOnlyAfterAnAlert(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{
		"logs":    failingFor("logs", time.Hour, true),
		"metrics": failingFor("metrics", 2*time.Minute, false),
	}
	statuses := []ProviderConnectionStatus{esStatus("logs", true), esStatus("metrics", true)}
	_, recovered := applyAlertState(prev, statuses, alertNow, true)
	if len(recovered) != 1 || strings.Join(recovered[0].Signals, ",") != "logs" {
		t.Fatalf("want a recovery for the announced signal only, got %v", recovered)
	}
	for _, s := range statuses {
		if s.FailingSince != "" || s.Alerted {
			t.Fatalf("a passing probe must clear the failure run, got %+v", s)
		}
	}
}

func TestApplyAlertState_ADifferentIntegrationStartsAfresh(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{"logs": failingFor("logs", time.Hour, true)}
	other := esStatus("logs", false)
	other.IntegrationId, other.IntegrationName = "es-2", "new-es"
	statuses := []ProviderConnectionStatus{other}
	failing, recovered := applyAlertState(prev, statuses, alertNow, true)
	if len(failing) != 0 || len(recovered) != 0 {
		t.Fatalf("switching backend must neither alert nor recover, got failing=%v recovered=%v", failing, recovered)
	}
	if statuses[0].FailingSince != alertNow.Format(time.RFC3339) || statuses[0].Alerted {
		t.Fatalf("new backend's failure run must start now, got %+v", statuses[0])
	}
}

func TestApplyAlertState_AgentServedSignalsAreIgnored(t *testing.T) {
	statuses := []ProviderConnectionStatus{{ProviderType: "logs", Provider: "loki", Source: "agent"}}
	applyAlertState(nil, statuses, alertNow, true)
	if statuses[0].FailingSince != "" {
		t.Fatalf("agent-served signal must carry no alert state, got %+v", statuses[0])
	}
}

func TestApplyAlertState_DisabledTracksButNeverAlerts(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{"logs": failingFor("logs", time.Hour, false)}
	statuses := []ProviderConnectionStatus{esStatus("logs", false)}
	if failing, _ := applyAlertState(prev, statuses, alertNow, false); len(failing) != 0 {
		t.Fatalf("flag off must not alert, got %v", failing)
	}
	if statuses[0].Alerted || statuses[0].FailingSince != prev["logs"].FailingSince {
		t.Fatalf("flag off must keep the failure run unannounced, got %+v", statuses[0])
	}
}

func stubAlertSeams(t *testing.T, prev map[string]ProviderConnectionStatus, publishErr error) *[]map[string]any {
	t.Helper()
	origPrev, origPublish, origNow, origEnabled := previousProviderStatus, publishProviderAlert, providerAlertNow, providerAlertEnabled
	t.Cleanup(func() {
		previousProviderStatus, publishProviderAlert, providerAlertNow, providerAlertEnabled = origPrev, origPublish, origNow, origEnabled
	})
	previousProviderStatus = func(_ *security.RequestContext, _ string) (map[string]ProviderConnectionStatus, error) {
		return prev, nil
	}
	providerAlertNow = func() time.Time { return alertNow }
	providerAlertEnabled = func() bool { return true }
	published := []map[string]any{}
	publishProviderAlert = func(_ *security.RequestContext, message map[string]any) error {
		published = append(published, message)
		return publishErr
	}
	return &published
}

func TestNotifyProviderTransitions_PublishesRoutableMessage(t *testing.T) {
	published := stubAlertSeams(t, map[string]ProviderConnectionStatus{"logs": failingFor("logs", 10*time.Minute, false)}, nil)
	statuses := []ProviderConnectionStatus{esStatus("logs", false)}
	acc := providerStatusAccount{AccountId: "acc-1", TenantId: "tenant-1", AccountName: "prod-eks"}

	notifyProviderTransitions(security.NewRequestContextForSuperAdmin(nil, nil, nil), acc, statuses)

	if len(*published) != 1 {
		t.Fatalf("want one message, got %d", len(*published))
	}
	msg := (*published)[0]
	if msg["type"] != integrationHealthMessageType || msg["source"] != "troubleshoot" || msg["tenant_id"] != "tenant-1" {
		t.Fatalf("unexpected envelope: %v", msg)
	}
	params := msg["parameters"].(map[string]any)
	if params["account_id"] != "acc-1" || params["state"] != "failing" {
		t.Fatalf("account_id and state must be set for rule matching, got %v", params)
	}
	body := params["message"].(string)
	for _, want := range []string{"prod-es (elasticsearch)", "prod-eks", "logs", "connection refused"} {
		if !strings.Contains(body, want) {
			t.Errorf("message missing %q:\n%s", want, body)
		}
	}
	if !statuses[0].Alerted {
		t.Fatal("a published failure must be recorded as alerted")
	}
}

func TestNotifyProviderTransitions_FailedPublishIsRetriedNextRun(t *testing.T) {
	stubAlertSeams(t, map[string]ProviderConnectionStatus{"logs": failingFor("logs", 10*time.Minute, false)}, errors.New("broker down"))
	statuses := []ProviderConnectionStatus{esStatus("logs", false)}
	notifyProviderTransitions(security.NewRequestContextForSuperAdmin(nil, nil, nil), providerStatusAccount{AccountId: "acc-1"}, statuses)
	if statuses[0].Alerted {
		t.Fatal("an alert that never went out must not be recorded as sent")
	}
}

func TestDecodeProviderStatusStamp_IgnoresCheckedAt(t *testing.T) {
	got, err := decodeProviderStatusStamp([]byte(`{"checkedAt":"2026-10-18T12:00:00Z","logs":{"provider_type":"logs","integration_id":"es-1","failing_since":"2026-10-18T11:50:00Z","alerted":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got["logs"].Alerted || got["logs"].FailingSince != "2026-10-18T11:50:00Z" {
		t.Fatalf("unexpected decode: %+v", got)
	}
}

func TestTruncateAlertError(t *testing.T) {
	long := strings.Repeat("é", providerAlertErrorLimit+10)
	got := truncateAlertError(long)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != providerAlertErrorLimit+1 {
		t.Fatalf("want %d runes plus ellipsis, got %d", providerAlertErrorLimit, len([]rune(got)))
	}
	if strings.Contains(truncateAlertError("a `b`\n c"), "`") {
		t.Fatal("backticks must not break out of the inline code span")
	}
}

func TestNotifyProviderTransitions_FailedPublishKeepsEarlierAnnouncements(t *testing.T) {
	stubAlertSeams(t, map[string]ProviderConnectionStatus{
		"logs":    failingFor("logs", time.Hour, true),
		"metrics": failingFor("metrics", 10*time.Minute, false),
	}, errors.New("broker down"))
	statuses := []ProviderConnectionStatus{esStatus("logs", false), esStatus("metrics", false)}
	notifyProviderTransitions(security.NewRequestContextForSuperAdmin(nil, nil, nil), providerStatusAccount{AccountId: "acc-1"}, statuses)
	if !statuses[0].Alerted {
		t.Fatal("logs was announced on an earlier run; a failed publish for metrics must not re-arm it")
	}
	if statuses[1].Alerted {
		t.Fatal("metrics' alert never went out and must be retried")
	}
}

func TestNotifyProviderTransitions_FailedRecoveryPublishIsRetriedNextRun(t *testing.T) {
	prev := map[string]ProviderConnectionStatus{"logs": failingFor("logs", time.Hour, true)}
	stubAlertSeams(t, prev, errors.New("broker down"))
	statuses := []ProviderConnectionStatus{esStatus("logs", true)}
	notifyProviderTransitions(security.NewRequestContextForSuperAdmin(nil, nil, nil), providerStatusAccount{AccountId: "acc-1"}, statuses)
	if !statuses[0].Alerted || statuses[0].FailingSince != prev["logs"].FailingSince {
		t.Fatalf("a recovery that never went out must keep the announced failure, got %+v", statuses[0])
	}

	// Next run, still healthy: the stamp just written still asks for the recovery.
	retry := []ProviderConnectionStatus{esStatus("logs", true)}
	_, recovered := applyAlertState(map[string]ProviderConnectionStatus{"logs": statuses[0]}, retry, alertNow, true)
	if len(recovered) != 1 {
		t.Fatalf("want the recovery retried on the next run, got %v", recovered)
	}
}
