package integrations

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/services/event"
	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const eventWebhookTestIntegrationId = "11111111-2222-3333-4444-555555555555"

var eventWebhookTestSettings = []core.IntegrationConfigValue{
	{Name: core.SyntheticIntegrationIdKey, Value: eventWebhookTestIntegrationId},
}

// The example from the issue, plus the now-required rule_name.
const eventWebhookFiring = `{
  "id": "evt-4411", "fingerprint": "site24x7:dc01.example.com:cpu", "status": "firing",
  "title": "CPU above 85% on dc01", "rule_name": "cpu_high", "severity": "high",
  "description": "CPU at 93%", "starts_at": "2026-09-29T10:00:00+05:30",
  "subject": {"name": "dc01.example.com", "type": "host", "namespace": "MG_Active Directory"},
  "source_tool": "Site24x7", "url": "https://site24x7.example.com/alert/4411",
  "labels": {"client": "example-school"}}`

func parseEventWebhook(t *testing.T, payload string) []core.EventIncomingWebhook {
	t.Helper()
	events, err := EventWebhook{}.ProcessEventWebook(nil, eventWebhookTestSettings, "acc-1", payload)
	require.NoError(t, err)
	return events
}

func validationItems(t *testing.T, err error) []core.WebhookValidationItemError {
	t.Helper()
	var verr *core.WebhookValidationError
	require.True(t, errors.As(err, &verr), "want *core.WebhookValidationError, got %v", err)
	return verr.Items
}

func TestEventWebhook_FiringMapsFieldsAsSent(t *testing.T) {
	events := parseEventWebhook(t, eventWebhookFiring)
	require.Len(t, events, 1)
	e := events[0]

	assert.Equal(t, "ew:"+eventWebhookTestIntegrationId+":id:evt-4411", e.EventId)
	assert.Equal(t, "ew:"+eventWebhookTestIntegrationId+":fp:site24x7:dc01.example.com:cpu", e.Investigation.Fingerprint)
	assert.Equal(t, string(event.EventStatusFiring), e.EventStatus)
	assert.Equal(t, "CPU above 85% on dc01", e.EventTitle)
	assert.Equal(t, "CPU at 93%", e.EventDescription)
	assert.Equal(t, event.EventPriorityHigh, e.Investigation.Severity)
	assert.Equal(t, "cpu_high", e.Investigation.RuleId, "RuleId is required for analysis and becomes the aggregation key")
	assert.Equal(t, "cpu_high", e.Investigation.RuleName)
	assert.Equal(t, "https://site24x7.example.com/alert/4411", e.EventUrl)
	assert.Equal(t, "acc-1", e.AccountId)
	assert.True(t, e.EventCreatedAt.Equal(time.Date(2026, 9, 29, 4, 30, 0, 0, time.UTC)))
	assert.True(t, e.EventEndsAt.IsZero())

	assert.Equal(t, "example-school", e.Investigation.Labels["client"])
	assert.Equal(t, "Site24x7", e.Investigation.Labels["source_tool"])
	assert.Equal(t, e.Investigation.Fingerprint, e.Investigation.Labels["pattern_hash"],
		"grouping must key on the alert, not the per-firing id")
}

func TestEventWebhook_SubjectKeptVerbatim(t *testing.T) {
	e := parseEventWebhook(t, eventWebhookFiring)[0]
	assert.Equal(t, "dc01.example.com", e.EventSubjectName)
	assert.Equal(t, "host", e.EventSubjectKind)
	assert.Equal(t, "MG_Active Directory", e.EventSubjectNamespace)
	// Consumed by core.MatchWorkloadAndEnrich: without it a label match could
	// rewrite the host into a k8s workload.
	assert.Equal(t, "true", e.Investigation.Labels["nb_skip_workload_match"])
}

func TestEventWebhook_NoSubjectLeftForResolution(t *testing.T) {
	e := parseEventWebhook(t, `{"id":"a","fingerprint":"f","status":"firing","title":"t","rule_name":"r"}`)[0]
	assert.Empty(t, e.EventSubjectName)
	assert.NotContains(t, e.Investigation.Labels, "nb_skip_workload_match")
	assert.Equal(t, event.EventPriorityMedium, e.Investigation.Severity, "severity defaults to medium")
}

// A repeat of the same firing must produce the same identity, so
// InvestigateEvent's existing-finding_id branch absorbs it instead of creating
// a second event.
func TestEventWebhook_RepeatIsSameIdentity(t *testing.T) {
	a := parseEventWebhook(t, eventWebhookFiring)[0]
	b := parseEventWebhook(t, eventWebhookFiring)[0]
	assert.Equal(t, a.EventId, b.EventId)
	assert.Equal(t, a.WebhookId, b.WebhookId)
}

// A resolve must target the firing's event (finding_id), and a re-fire with a
// new id must be a new event sharing the fingerprint.
func TestEventWebhook_ResolveAndRefireIdentities(t *testing.T) {
	firing := parseEventWebhook(t, eventWebhookFiring)[0]
	resolved := parseEventWebhook(t, `{"id":"evt-4411","fingerprint":"site24x7:dc01.example.com:cpu","status":"resolved",
		"title":"CPU above 85% on dc01","rule_name":"cpu_high","ends_at":"2026-09-29T11:00:00Z"}`)[0]
	refire := parseEventWebhook(t, strings.Replace(eventWebhookFiring, "evt-4411", "evt-4412", 1))[0]

	assert.Equal(t, string(event.EventStatusResolved), resolved.EventStatus)
	assert.Equal(t, firing.EventId, resolved.EventId, "resolve closes the firing via finding_id")
	assert.True(t, resolved.EventEndsAt.Equal(time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC)))

	assert.NotEqual(t, firing.EventId, refire.EventId, "a re-fire is a new event")
	assert.Equal(t, firing.Investigation.Fingerprint, refire.Investigation.Fingerprint, "and is linked by fingerprint")
}

// resolveEventQuery matches `fingerprint = $3 OR finding_id = $3` with the
// resolve's EventId. If ids and fingerprints shared a namespace, a sender using
// the same string for both would have a resolve close every firing of the alert.
func TestEventWebhook_IdAndFingerprintNeverCollide(t *testing.T) {
	e := parseEventWebhook(t, `{"id":"same","fingerprint":"same","status":"firing","title":"t","rule_name":"r"}`)[0]
	assert.NotEqual(t, e.EventId, e.Investigation.Fingerprint)
}

// Ids are scoped to the integration so they can't collide with other sources'
// finding_ids or another custom integration's.
func TestEventWebhook_IdsScopedToIntegration(t *testing.T) {
	other := []core.IntegrationConfigValue{{Name: core.SyntheticIntegrationIdKey, Value: "other"}}
	a := parseEventWebhook(t, eventWebhookFiring)[0]
	b, err := EventWebhook{}.ProcessEventWebook(nil, other, "acc-1", eventWebhookFiring)
	require.NoError(t, err)
	assert.NotEqual(t, a.EventId, b[0].EventId)
	assert.NotEqual(t, a.Investigation.Fingerprint, b[0].Investigation.Fingerprint)
}

func TestEventWebhook_MissingIntegrationIdFails(t *testing.T) {
	_, err := EventWebhook{}.ProcessEventWebook(nil, nil, "acc-1", eventWebhookFiring)
	require.Error(t, err, "without the integration id every id would be ew::… and never match")
}

func TestEventWebhook_Batch(t *testing.T) {
	events := parseEventWebhook(t, `{"events":[`+eventWebhookFiring+`,
		{"id":"b","fingerprint":"g","status":"resolved","title":"t2","rule_name":"r2"}]}`)
	require.Len(t, events, 2)
	assert.Equal(t, string(event.EventStatusFiring), events[0].EventStatus)
	assert.Equal(t, string(event.EventStatusResolved), events[1].EventStatus)
}

func TestEventWebhook_BatchWithOneBadItemRejectsAll(t *testing.T) {
	payload := `{"events":[` + eventWebhookFiring + `,{"id":"b","status":"firing","title":"t","rule_name":"r"}]}`

	events, err := EventWebhook{}.ProcessEventWebook(nil, eventWebhookTestSettings, "acc-1", payload)
	assert.Nil(t, events)
	items := validationItems(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, core.WebhookValidationItemError{Index: 1, Field: "fingerprint", Reason: "is required"}, items[0])

	// The edge validator is the same parse.
	assert.Equal(t, items, validationItems(t, EventWebhook{}.ValidateWebhookPayload(payload)))
}

func TestEventWebhook_Validation(t *testing.T) {
	valid := map[string]string{"id": `"a"`, "fingerprint": `"f"`, "status": `"firing"`, "title": `"t"`, "rule_name": `"r"`}
	build := func(overrides map[string]string) string {
		fields := map[string]string{}
		for k, v := range valid {
			fields[k] = v
		}
		for k, v := range overrides {
			fields[k] = v
		}
		parts := []string{}
		for k, v := range fields {
			if v != "" {
				parts = append(parts, fmt.Sprintf("%q:%s", k, v))
			}
		}
		return "{" + strings.Join(parts, ",") + "}"
	}

	cases := []struct {
		name      string
		overrides map[string]string
		field     string
	}{
		{"missing id", map[string]string{"id": ""}, "id"},
		{"blank fingerprint", map[string]string{"fingerprint": `"  "`}, "fingerprint"},
		{"missing rule_name", map[string]string{"rule_name": ""}, "rule_name"},
		{"missing title", map[string]string{"title": ""}, "title"},
		{"id too long", map[string]string{"id": `"` + strings.Repeat("x", 257) + `"`}, "id"},
		{"control char in fingerprint", map[string]string{"fingerprint": `"a\u0007b"`}, "fingerprint"},
		{"bad status", map[string]string{"status": `"ok"`}, "status"},
		{"bad severity", map[string]string{"severity": `"urgent"`}, "severity"},
		{"starts_at without offset", map[string]string{"starts_at": `"2026-09-29T10:00:00"`}, "starts_at"},
		{"bad ends_at", map[string]string{"ends_at": `"yesterday"`}, "ends_at"},
		{"ends_at before starts_at", map[string]string{"starts_at": `"2026-09-29T10:00:00Z"`, "ends_at": `"2026-09-29T09:59:59Z"`}, "ends_at"},
		{"javascript url", map[string]string{"url": `"javascript:alert(1)"`}, "url"},
		{"data url", map[string]string{"url": `"data:text/html,x"`}, "url"},
		{"url without host", map[string]string{"url": `"https://"`}, "url"},
		{"reserved label", map[string]string{"labels": `{"nb_skip_workload_match":"true"}`}, "labels.nb_skip_workload_match"},
		{"non-string label", map[string]string{"labels": `{"n":1}`}, "payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EventWebhook{}.ValidateWebhookPayload(build(tc.overrides))
			items := validationItems(t, err)
			require.Len(t, items, 1, "%v", items)
			assert.Equal(t, tc.field, items[0].Field)
			assert.Equal(t, 0, items[0].Index)
		})
	}

	assert.NoError(t, EventWebhook{}.ValidateWebhookPayload(build(nil)))
	assert.NoError(t, EventWebhook{}.ValidateWebhookPayload(build(map[string]string{"url": `"http://site24x7.example.com/alert/1"`})))
	assert.NoError(t, EventWebhook{}.ValidateWebhookPayload(build(map[string]string{
		"starts_at": `"2026-09-29T10:00:00Z"`, "ends_at": `"2026-09-29T10:00:00Z"`})), "an instant alert may end when it starts")
	assert.NoError(t, EventWebhook{}.ValidateWebhookPayload(build(map[string]string{"id": `"` + strings.Repeat("x", 256) + `"`})))
}

func TestEventWebhook_EnvelopeValidation(t *testing.T) {
	many := make([]string, eventWebhookMaxBatch+1)
	for i := range many {
		many[i] = fmt.Sprintf(`{"id":"%d","fingerprint":"f","status":"firing","title":"t","rule_name":"r"}`, i)
	}
	cases := map[string]string{
		"not json":       `nope`,
		"array":          `[` + eventWebhookFiring + `]`,
		"empty events":   `{"events":[]}`,
		"too many":       `{"events":[` + strings.Join(many, ",") + `]}`,
		"malformed json": `{"id":`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			items := validationItems(t, EventWebhook{}.ValidateWebhookPayload(payload))
			require.Len(t, items, 1)
			assert.Equal(t, -1, items[0].Index)
		})
	}

	ok := make([]string, eventWebhookMaxBatch)
	copy(ok, many)
	assert.NoError(t, EventWebhook{}.ValidateWebhookPayload(`{"events":[`+strings.Join(ok, ",")+`]}`))
}

func TestEventWebhook_SkipsResolvedReplay(t *testing.T) {
	var i any = EventWebhook{}
	skipper, ok := i.(core.ResolvedReplaySkipper)
	require.True(t, ok)
	assert.True(t, skipper.SkipResolvedReplay())
	_, ok = i.(core.WebhookPayloadValidator)
	assert.True(t, ok, "must be validated synchronously at the edge")
}
