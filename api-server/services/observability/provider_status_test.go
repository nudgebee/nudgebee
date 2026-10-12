package observability

import (
	"errors"
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"testing"
)

// The sweep takes a cluster in for its ClickHouse logs pick only while the agent reports
// the logs collected, as the resolver only serves the pick then; a stamp left from before
// is cleared through the "already stamped" arm.
func TestProviderStatusAccountsQuery_ClickHouseArmNeedsCollectedLogs(t *testing.T) {
	start := strings.Index(providerStatusAccountsQuery, "i.type = 'otel_clickhouse'")
	if start < 0 {
		t.Fatal("no otel_clickhouse arm in the candidate query")
	}
	arm := providerStatusAccountsQuery[start:]
	arm = arm[:strings.Index(arm, ")")]
	if !strings.Contains(arm, "a.connection_status ->> 'clickhouseLogsCollected' = 'true'") {
		t.Errorf("the otel_clickhouse arm does not require collected logs:\n%s", arm)
	}
}

type resolvedProvider struct {
	provider    string
	source      string
	integration *core.IntegrationDto
	err         error
}

func stubProviderStatusSeams(t *testing.T, resolved map[string]resolvedProvider, probe func(id string) error) *[]string {
	t.Helper()
	origResolve, origProbe, origNotes := resolveProviderForStatus, probeIntegration, integrationNotes
	t.Cleanup(func() {
		resolveProviderForStatus, probeIntegration, integrationNotes = origResolve, origProbe, origNotes
	})
	// Default: no notes. Tests that care override it after calling this helper.
	integrationNotes = func(_ *security.RequestContext, _, _, _ string) []string { return nil }

	probed := []string{}
	resolveProviderForStatus = func(_ *security.RequestContext, _ string, _ string, providerType string, _ string) (string, string, *core.IntegrationDto, error) {
		r := resolved[providerType]
		return r.provider, r.source, r.integration, r.err
	}
	probeIntegration = func(_ *security.RequestContext, integrationID string) error {
		probed = append(probed, integrationID)
		return probe(integrationID)
	}
	return &probed
}

func byType(statuses []ProviderConnectionStatus) map[string]ProviderConnectionStatus {
	out := map[string]ProviderConnectionStatus{}
	for _, s := range statuses {
		out[s.ProviderType] = s
	}
	return out
}

func TestCheckProviderStatus_RequiresAccount(t *testing.T) {
	if _, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), ""); err == nil {
		t.Fatal("expected an error for an empty account id")
	}
}

func TestCheckProviderStatus_ProbesOnlyNonAgentIntegrations(t *testing.T) {
	probed := stubProviderStatusSeams(t, map[string]resolvedProvider{
		// The agent's own Loki row, auto-flagged as default by the heartbeat: agent-served.
		"logs": {provider: "loki", source: "agent", integration: &core.IntegrationDto{Id: "agent-loki", Name: "loki", Source: "agent", Type: "loki"}},
		// A hosted Datadog the operator marked as the default metrics provider.
		"metrics": {provider: "datadog", source: "user", integration: &core.IntegrationDto{Id: "dd-1", Name: "Datadog prod", Source: "user", Type: "datadog"}},
		// Agent-detected provider with no integration row at all.
		"traces": {provider: "clickhouse", source: "agent"},
	}, func(string) error { return nil })

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("expected one entry per signal, got %d", len(statuses))
	}
	if got := *probed; len(got) != 1 || got[0] != "dd-1" {
		t.Fatalf("expected only the user integration to be probed, probed=%v", got)
	}

	s := byType(statuses)
	if s["logs"].IntegrationId != "" || s["logs"].Connected {
		t.Errorf("agent-served logs must not carry an integration or a probe result: %+v", s["logs"])
	}
	if s["logs"].Provider != "loki" || s["logs"].Source != "agent" {
		t.Errorf("agent-served logs should still report the resolved provider/source: %+v", s["logs"])
	}
	m := s["metrics"]
	if m.IntegrationId != "dd-1" || m.IntegrationName != "Datadog prod" || !m.Connected || m.Error != "" {
		t.Errorf("user-served metrics should report the probed integration as connected: %+v", m)
	}
	if s["traces"].IntegrationId != "" || s["traces"].Provider != "clickhouse" {
		t.Errorf("agent-detected traces should pass through unprobed: %+v", s["traces"])
	}
}

func TestCheckProviderStatus_ReportsProbeFailure(t *testing.T) {
	stubProviderStatusSeams(t, map[string]resolvedProvider{
		"logs": {provider: "loki", source: "user", integration: &core.IntegrationDto{Id: "loki-cloud", Name: "Grafana Cloud", Source: "user", Type: "loki"}},
	}, func(string) error { return errors.New("failed to connect to the Loki query endpoint: 401") })

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	l := byType(statuses)["logs"]
	if l.Connected {
		t.Errorf("a failing probe must not report connected: %+v", l)
	}
	if l.IntegrationId != "loki-cloud" || l.Error != "failed to connect to the Loki query endpoint: 401" {
		t.Errorf("the probe error should be surfaced verbatim next to the integration: %+v", l)
	}
}

func TestCheckProviderStatus_ResolutionErrorIsPerSignal(t *testing.T) {
	probed := stubProviderStatusSeams(t, map[string]resolvedProvider{
		"logs":    {err: errors.New("no agent connected for account acc-1")},
		"metrics": {provider: "prometheus", source: "user", integration: &core.IntegrationDto{Id: "amp-1", Name: "AMP", Source: "user", Type: "prometheus"}},
		"traces":  {err: errors.New("no agent connected for account acc-1")},
	}, func(string) error { return nil })

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("a signal that cannot be resolved must not fail the whole check: %v", err)
	}
	s := byType(statuses)
	if s["logs"].Error == "" || s["logs"].IntegrationId != "" {
		t.Errorf("unresolvable logs should carry the reason and no integration: %+v", s["logs"])
	}
	if !s["metrics"].Connected {
		t.Errorf("the other signals should still be checked: %+v", s["metrics"])
	}
	if got := *probed; len(got) != 1 || got[0] != "amp-1" {
		t.Errorf("only the resolvable integration should be probed, probed=%v", got)
	}
}

// The stamp is what the Agent Details page reads, so its shape is a contract:
// one entry per signal plus checkedAt, with the probe result inlined.
func TestProviderStatusStampShape(t *testing.T) {
	statuses := []ProviderConnectionStatus{
		{ProviderType: "logs", Provider: "loki", Source: "user", IntegrationId: "loki-1", IntegrationName: "Grafana Cloud", Connected: true},
		{ProviderType: "metrics", Provider: "prometheus", Source: "agent"},
		{ProviderType: "traces", Provider: "clickhouse", Source: "agent"},
	}

	stamp := map[string]any{"checkedAt": "2026-09-05T01:10:00Z"}
	for _, status := range statuses {
		stamp[status.ProviderType] = status
	}
	encoded, err := common.MarshalJson(stamp)
	if err != nil {
		t.Fatalf("stamp must marshal: %v", err)
	}

	var decoded struct {
		CheckedAt string                   `json:"checkedAt"`
		Logs      ProviderConnectionStatus `json:"logs"`
		Metrics   ProviderConnectionStatus `json:"metrics"`
	}
	if err := common.UnmarshalJson(encoded, &decoded); err != nil {
		t.Fatalf("stamp must round-trip: %v", err)
	}
	if decoded.CheckedAt == "" {
		t.Error("the UI renders a Last checked time; checkedAt must survive the round trip")
	}
	// integration_id set is the UI's signal to trust this over the agent's own flag.
	if decoded.Logs.IntegrationId != "loki-1" || !decoded.Logs.Connected {
		t.Errorf("integration-served logs must carry the integration and its probe result: %+v", decoded.Logs)
	}
	if decoded.Metrics.IntegrationId != "" {
		t.Errorf("agent-served metrics must omit integration_id so the UI falls back to the agent flag: %+v", decoded.Metrics)
	}
}

func stubAgentDetails(t *testing.T, features account.AgentDetailsFeatures, err error) {
	t.Helper()
	orig := agentDetailsForStatus
	t.Cleanup(func() { agentDetailsForStatus = orig })
	agentDetailsForStatus = func(string) (account.AgentDetails, error) {
		return account.AgentDetails{Features: features}, err
	}
}

func strPtr(s string) *string { return &s }

// A disconnected agent makes the resolver withhold its reported provider — right for
// routing a query, wrong for a view that answers "where do these logs come from?".
// The status view must still name the backend, or it contradicts the Agent tab, which
// reads the very same field.
func TestCheckProviderStatus_NamesAgentReportedProviderWhenResolverWithholdsIt(t *testing.T) {
	stubProviderStatusSeams(t, map[string]resolvedProvider{
		"logs":    {},
		"metrics": {},
		"traces":  {},
	}, func(string) error { return nil })
	tracesOn := true
	stubAgentDetails(t, account.AgentDetailsFeatures{
		LogsConnectionProvider: strPtr("loki"),
		TraceProvider:          strPtr("otel_clickhouse"),
		TracesEnabled:          &tracesOn,
		PrometheusUrl:          strPtr("http://vmsingle.victoria.svc.cluster.local:8429"),
	}, nil)

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := byType(statuses)
	for signal, want := range map[string]string{"logs": "loki", "traces": "otel_clickhouse", "metrics": "prometheus"} {
		if s[signal].Provider != want {
			t.Errorf("%s: expected the agent-reported %q, got %+v", signal, want, s[signal])
		}
		if s[signal].Source != "agent" {
			t.Errorf("%s: agent-reported provider must be attributed to the agent: %+v", signal, s[signal])
		}
		// Nothing was probed, so nothing may claim to be connected.
		if s[signal].Connected {
			t.Errorf("%s: an unprobed agent-reported provider must not report connected: %+v", signal, s[signal])
		}
	}
}

// A chronosphere Prometheus URL is the one case where the agent's metrics provider is not
// literally "prometheus"; mirror the resolver rather than letting the two disagree.
func TestCheckProviderStatus_AgentChronosphereMapping(t *testing.T) {
	stubProviderStatusSeams(t, map[string]resolvedProvider{"metrics": {}}, func(string) error { return nil })
	stubAgentDetails(t, account.AgentDetailsFeatures{
		PrometheusUrl: strPtr("https://tenant.chronosphere.io/data/metrics"),
	}, nil)

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := byType(statuses)["metrics"].Provider; got != "chronosphere" {
		t.Errorf("expected chronosphere, got %q", got)
	}
}

// An account with no agent row at all is served entirely by integrations; the missing
// agent must not fail the check or invent a provider.
func TestCheckProviderStatus_NoAgentRowIsNotAnError(t *testing.T) {
	stubProviderStatusSeams(t, map[string]resolvedProvider{"logs": {}}, func(string) error { return nil })
	stubAgentDetails(t, account.AgentDetailsFeatures{}, errors.New("no agent connected for account acc-1"))

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("a missing agent row must not fail the check: %v", err)
	}
	if l := byType(statuses)["logs"]; l.Provider != "" || l.Source != "" {
		t.Errorf("expected no invented provider: %+v", l)
	}
}

// Every agent reports traceProvider="otel_clickhouse" regardless of whether a traces
// backend exists, so naming it whenever it is present claimed a source the agent had not
// actually reported. TracesEnabled is the only field that distinguishes the two.
func TestCheckProviderStatus_TracesNeedMoreThanADefaultProviderName(t *testing.T) {
	tracesOff := false
	for _, tc := range []struct {
		name    string
		enabled *bool
		want    string
	}{
		{"traces disabled", &tracesOff, ""},
		{"tracesEnabled absent", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubProviderStatusSeams(t, map[string]resolvedProvider{"traces": {}}, func(string) error { return nil })
			stubAgentDetails(t, account.AgentDetailsFeatures{
				TraceProvider: strPtr("otel_clickhouse"),
				TracesEnabled: tc.enabled,
			}, nil)

			statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := byType(statuses)["traces"]; got.Provider != tc.want {
				t.Errorf("expected provider %q so the UI can say \"Not configured\", got %+v", tc.want, got)
			}
		})
	}
}

// The candidate query re-selects any account that already carries a stamp, so a stamp left
// behind after the last integration is removed would keep that account in scope forever.
// Storing nothing in that case is what bounds the candidate set.
func TestProviderStatusStampIsDroppedWhenNoIntegrationServesAnySignal(t *testing.T) {
	agentOnly := []ProviderConnectionStatus{
		{ProviderType: "logs", Provider: "loki", Source: "agent"},
		{ProviderType: "metrics", Provider: "prometheus", Source: "agent"},
	}
	if stampCarriesAnswer(agentOnly) {
		t.Error("an all-agent result must not be treated as integration-served")
	}

	withIntegration := append(agentOnly, ProviderConnectionStatus{
		ProviderType: "traces", Provider: "datadog", Source: "user", IntegrationId: "dd-1",
	})
	if !stampCarriesAnswer(withIntegration) {
		t.Error("a single integration-served signal must keep the stamp")
	}
}

// The agent reports its Loki as its logs provider whatever the account's default is,
// so only the stamp can tell the page that ClickHouse serves this cluster's logs.
func TestProviderStatusStampIsKeptForClickHouseLogs(t *testing.T) {
	for _, logs := range []ProviderConnectionStatus{
		{ProviderType: "logs", Provider: "otel_clickhouse", Source: "agent"},
		{ProviderType: "logs", Provider: "otel_clickhouse", Source: "agent", Error: "not reachable right now"},
	} {
		statuses := []ProviderConnectionStatus{logs, {ProviderType: "metrics", Provider: "prometheus", Source: "agent"}}
		if !stampCarriesAnswer(statuses) {
			t.Errorf("a ClickHouse logs entry must keep the stamp: %+v", logs)
		}
	}
	// otel_clickhouse serving traces is what the agent itself reports.
	traces := []ProviderConnectionStatus{{ProviderType: "traces", Provider: "otel_clickhouse", Source: "agent"}}
	if stampCarriesAnswer(traces) {
		t.Error("agent-reported ClickHouse traces must not keep a stamp")
	}
}

// A ClickHouse logs default that cannot serve must stay named as the logs provider,
// with its error, rather than turn into the Loki the agent reports.
func TestCheckProviderStatus_ClickHouseLogsDefaultDownKeepsItsName(t *testing.T) {
	stubProviderStatusSeams(t, map[string]resolvedProvider{
		"logs": {err: &BuiltInProviderDownError{ProviderType: "logs", Provider: "otel_clickhouse"}},
	}, func(string) error { return nil })
	stubAgentDetails(t, account.AgentDetailsFeatures{LogsConnectionProvider: strPtr("loki")}, nil)

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	l := byType(statuses)["logs"]
	if l.Provider != "otel_clickhouse" || l.Source != "agent" {
		t.Errorf("expected otel_clickhouse/agent, got %+v", l)
	}
	if l.Error == "" || l.Connected {
		t.Errorf("expected the resolver's error and not connected: %+v", l)
	}
}

// A note must never change whether a signal reads as connected: one integration
// commonly serves all three, and none of them use alert rules.
func TestProviderStatusNoteDoesNotAffectConnected(t *testing.T) {
	integration := &core.IntegrationDto{Id: "int-1", Name: "cube apm test", Type: "cubeapm", Source: "user"}
	resolved := map[string]resolvedProvider{
		"logs":    {provider: "cubeapm", source: "user", integration: integration},
		"metrics": {provider: "cubeapm", source: "user", integration: integration},
		"traces":  {provider: "cubeapm", source: "user", integration: integration},
	}
	stubProviderStatusSeams(t, resolved, func(string) error { return nil })

	calls := 0
	integrationNotes = func(_ *security.RequestContext, _, _, _ string) []string {
		calls++
		return []string{"Queries work, but alert-rule management is unauthorised."}
	}

	statuses, err := CheckProviderStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acct-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for signal, status := range byType(statuses) {
		if !status.Connected {
			t.Errorf("%s: Connected = false; a note must not disconnect a signal that probed clean", signal)
		}
		if status.Error != "" {
			t.Errorf("%s: Error = %q; a note is not an error", signal, status.Error)
		}
		if status.Note == "" {
			t.Errorf("%s: Note is empty; the advisory should be carried to the view", signal)
		}
	}

	// One integration, one fetch — this runs on a cron for every account.
	if calls != 1 {
		t.Errorf("notes fetched %d times for one integration, want 1", calls)
	}
}
