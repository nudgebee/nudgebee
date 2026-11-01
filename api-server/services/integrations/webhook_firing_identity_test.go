package integrations

import (
	"fmt"
	"sort"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type firingIdentityMode string

const (
	// The parser sets core.EventIncomingWebhook.FiringKey; the framework gives each
	// firing its own events row. Needs a fire→resolve→fire test below.
	firingKeyMode firingIdentityMode = "firing_key"
	// The parser's EventId already changes from one firing to the next.
	perFiringEventId firingIdentityMode = "per_firing_event_id"
	// EventId is stable across firings and the payload offers no per-firing key, so
	// a firing after the alert's row closed is folded into that closed row.
	noFiringIdentity firingIdentityMode = "none"
)

type firingIdentity struct {
	mode firingIdentityMode
	// For per_firing_event_id: the payload field that changes per firing.
	// For none: why there is no key, and what would supply one.
	detail string
}

// webhookFiringIdentity records how every webhook parser tells one firing of an
// alert from the next. It exists because a parser whose EventId is stable per
// alert silently swallows every firing after the first close: GCP monitoring did,
// then Alertmanager and Grafana did. A declaration cannot prove a claim,
// but it makes each parser's answer visible in one place, and a new parser cannot
// ship without one (TestEveryWebhookParserDeclaresFiringIdentity).
var webhookFiringIdentity = map[string]firingIdentity{
	IntegrationPrometheusAlertManagerWebhook: {firingKeyMode, "startsAt"},
	IntegrationGrafanaWebhook:                {firingKeyMode, "startsAt"},
	// Alertmanager-compatible body: same stable fingerprint, same per-firing startsAt.
	IntegrationCubeAPMWebhook: {firingKeyMode, "startsAt"},
	IntegrationSigNozWebhook:  {firingKeyMode, "startsAt"},

	IntegrationZendutyWebhook:       {perFiringEventId, "incident unique_id"},
	IntegrationPagerdutyWebhook:     {perFiringEventId, "incident id"},
	IntegrationGCPMonitoringWebhook: {perFiringEventId, "incident.incident_id"},
	IntegrationDatadogWebhook:       {perFiringEventId, "event id ($ID) per notification, with a FiringKey so a re-notification updates its group's open event; incident_uuid for incidents"},
	IntegrationDynatraceWebhook:     {perFiringEventId, "problem id / davis event id"},
	IntegrationAzureMonitorWebhook:  {perFiringEventId, "essentials.alertId (one per fired alert instance)"},
	IntegrationNewRelicWebhook:      {perFiringEventId, "issue id / incident id"},
	IntegrationSplunkWebhook:        {perFiringEventId, "sid (search job id); falls back to search_name, which is per alert"},
	IntegrationGenericWebhook:       {perFiringEventId, "random id per delivery"},
	IntegrationEventWebhook:         {perFiringEventId, "sender's id, required to name one firing (contract)"},

	IntegrationElasticsearchWebhook: {noFiringIdentity, "rule id + alert id are per alert; the documented template carries no per-firing field ({{alert.uuid}} would be one)"},
	IntegrationOpenObserveWebhook:   {noFiringIdentity, "fingerprint is derived from alert identity and OpenObserve sends no resolve"},
	IntegrationServicenowWebhook:    {noFiringIdentity, "message key / fingerprint are per alert and the start is the delivery time"},
	IntegrationSolarWindsWebhook:    {noFiringIdentity, "triggerId is documented as per alert instance, unverified: no payload fixtures"},
}

func TestEveryWebhookParserDeclaresFiringIdentity(t *testing.T) {
	registered := map[string]bool{}
	for _, integration := range core.RegisteredIntegrations() {
		if _, ok := integration.(core.EventIncomingTroubleshootWebhookIntegration); !ok {
			continue
		}
		registered[integration.Name()] = true
	}
	require.NotEmpty(t, registered, "no webhook parsers registered — is the package init running?")

	var undeclared []string
	for name := range registered {
		declared, ok := webhookFiringIdentity[name]
		if !ok {
			undeclared = append(undeclared, name)
			continue
		}
		assert.NotEmpty(t, declared.detail, "%s: say which field identifies a firing, or why none does", name)
	}
	sort.Strings(undeclared)
	assert.Empty(t, undeclared, "declare how these webhook parsers identify a firing in webhookFiringIdentity")

	for name := range webhookFiringIdentity {
		assert.True(t, registered[name], "%s is declared but no longer registered as a webhook parser", name)
	}
}

// firingKeySequence parses one delivery of a single alert for one parser.
type firingKeySequence func(status, startsAt, fingerprint string) (core.EventIncomingWebhook, error)

func alertmanagerStyleBody(status, startsAt, fingerprint string) string {
	return fmt.Sprintf(`{"status": %[1]q, "alerts": [{
		"status": %[1]q,
		"labels": {"alertname": "HighErrorRate", "severity": "critical", "namespace": "payments", "deployment": "checkout-api"},
		"annotations": {"summary": "Checkout error rate above 5%%"},
		"startsAt": %[2]q,
		"endsAt": "0001-01-01T00:00:00Z",
		"generatorURL": "https://alerts.example.com/graph",
		"fingerprint": %[3]q
	}]}`, status, startsAt, fingerprint)
}

func singleEvent(events []core.EventIncomingWebhook, err error) (core.EventIncomingWebhook, error) {
	if err != nil {
		return core.EventIncomingWebhook{}, err
	}
	if len(events) != 1 {
		return core.EventIncomingWebhook{}, fmt.Errorf("expected 1 event, got %d", len(events))
	}
	return events[0], nil
}

var firingKeyParsers = map[string]firingKeySequence{
	IntegrationPrometheusAlertManagerWebhook: func(status, startsAt, fingerprint string) (core.EventIncomingWebhook, error) {
		sc := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		return singleEvent(PrometheusAlertManagerWebhook{}.ProcessEventWebook(sc, nil, "acct-1", alertmanagerStyleBody(status, startsAt, fingerprint)))
	},
	// CubeAPM sends the Alertmanager body, so the same delivery fixture applies.
	IntegrationCubeAPMWebhook: func(status, startsAt, fingerprint string) (core.EventIncomingWebhook, error) {
		sc := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		return singleEvent(CubeAPMWebhook{}.ProcessEventWebook(sc, nil, "acct-1", alertmanagerStyleBody(status, startsAt, fingerprint)))
	},
	IntegrationSigNozWebhook: func(status, startsAt, fingerprint string) (core.EventIncomingWebhook, error) {
		sc := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		return singleEvent(SigNozWebhook{}.ProcessEventWebook(sc, nil, "acct-1", alertmanagerStyleBody(status, startsAt, fingerprint)))
	},
	IntegrationGrafanaWebhook: func(status, startsAt, fingerprint string) (core.EventIncomingWebhook, error) {
		event, err := mapGrafanaAlertToEvent("acct-1", map[string]any{
			"status":      status,
			"fingerprint": fingerprint,
			"startsAt":    startsAt,
			"endsAt":      "0001-01-01T00:00:00Z",
			"labels":      map[string]any{"alertname": "HighErrorRate", "severity": "critical"},
			"annotations": map[string]any{"summary": "Checkout error rate above 5%"},
		})
		if err != nil {
			return core.EventIncomingWebhook{}, err
		}
		return *event, nil
	},
}

func TestFiringKeyParsersSeparateFirings(t *testing.T) {
	for name, declared := range webhookFiringIdentity {
		if declared.mode == firingKeyMode {
			assert.Contains(t, firingKeyParsers, name, "%s declares a firing key but has no fire→resolve→fire test", name)
		}
	}

	for name, parse := range firingKeyParsers {
		t.Run(name, func(t *testing.T) {
			mustParse := func(status, startsAt, fingerprint string) core.EventIncomingWebhook {
				e, err := parse(status, startsAt, fingerprint)
				require.NoError(t, err)
				return e
			}
			first := mustParse("firing", "2026-06-01T10:00:00.250Z", "3f2c9a1b7d4e6a80")
			repeat := mustParse("firing", "2026-06-01T10:00:00.250Z", "3f2c9a1b7d4e6a80")
			resolved := mustParse("resolved", "2026-06-01T10:00:00.250Z", "3f2c9a1b7d4e6a80")
			second := mustParse("firing", "2026-06-01T10:23:30.250Z", "3f2c9a1b7d4e6a80")

			require.NotEmpty(t, first.FiringKey)
			assert.Equal(t, first.FiringKey, repeat.FiringKey, "a repeat notification is the same firing")
			assert.Equal(t, first.FiringKey, resolved.FiringKey, "the resolve belongs to the firing it ends")
			assert.NotEqual(t, first.FiringKey, second.FiringKey, "firing again after a resolve is a new firing")

			// The alert's identity does not change between firings: resolve, replay
			// and the stored delivery log all key on it.
			for _, e := range []core.EventIncomingWebhook{repeat, resolved, second} {
				assert.Equal(t, first.EventId, e.EventId)
				assert.Equal(t, first.Investigation.Fingerprint, e.Investigation.Fingerprint)
			}

			shifted := mustParse("firing", "2026-06-01T15:30:00.250+05:30", "3f2c9a1b7d4e6a80")
			assert.Equal(t, first.FiringKey, shifted.FiringKey, "the key is the instant, whatever offset the payload uses")

			// Rejecting the delivery (Grafana does) is as good as leaving the key empty.
			if noFingerprint, err := parse("firing", "2026-06-01T10:00:00.250Z", ""); err == nil {
				assert.Empty(t, noFingerprint.FiringKey, "without a fingerprint there is no alert to key a firing of")
			}
		})
	}
}
