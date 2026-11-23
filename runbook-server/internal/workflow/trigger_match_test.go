package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkTriggerMatch(t *testing.T, req model.CheckTriggerMatchRequest) model.CheckTriggerMatchResponse {
	t.Helper()
	service := &Service{}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	resp, err := service.CheckTriggerMatch(sc, "test-account", req)
	require.NoError(t, err)
	return resp
}

func eventPayload() map[string]any {
	return map[string]any{
		"event_type":       "KubePodCrashLooping",
		"cluster":          "prod",
		"priority":         "P1",
		"cloud_account_id": "test-account",
	}
}

func TestCheckTriggerMatch_EventFilter(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"filter": `{{ event.event_type == "KubePodCrashLooping" }}`},
		Payload:     eventPayload(),
	})
	assert.True(t, resp.Matched)
	assert.Empty(t, resp.Reason)

	payload := eventPayload()
	payload["event_type"] = "Foo"
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"filter": `{{ event.event_type == "KubePodCrashLooping" }}`},
		Payload:     payload,
	})
	assert.False(t, resp.Matched)
	assert.Equal(t, model.CheckTriggerGateFilter, resp.Gate)
}

func TestCheckTriggerMatch_EventTypeGate(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"event_type": "KubePodCrashLooping"},
		Payload:     map[string]any{"event_type": "Foo"},
	})
	assert.False(t, resp.Matched)
	assert.Equal(t, model.CheckTriggerGateEventType, resp.Gate)
	assert.Contains(t, resp.Reason, "'Foo'")
	assert.Contains(t, resp.Reason, "'KubePodCrashLooping'")
}

func TestCheckTriggerMatch_EventTypeFallsBackToAggregationKey(t *testing.T) {
	// The consumer normalises event_type from aggregation_key for internal events;
	// a payload copied from one of those must not read as a type mismatch here.
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"event_type": "Anomaly"},
		Payload:     map[string]any{"aggregation_key": "Anomaly", "cloud_account_id": "test-account"},
	})
	assert.True(t, resp.Matched)
}

func TestCheckTriggerMatch_LifecyclePhaseGate(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"on": "investigation.completed", "filter": `{{ true }}`},
		Payload:     eventPayload(),
	})
	assert.False(t, resp.Matched)
	assert.Equal(t, model.CheckTriggerGatePhase, resp.Gate)
	assert.Contains(t, resp.Reason, "event.created")

	payload := eventPayload()
	payload["lifecycle_phase"] = "investigation.completed"
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"on": "investigation.completed", "filter": `{{ true }}`},
		Payload:     payload,
	})
	assert.True(t, resp.Matched)
}

func TestCheckTriggerMatch_UnregisterableEventTrigger(t *testing.T) {
	// No event_type and no filter: the registry drops this rule outright, so it
	// can never fire. Saying "no match" without saying why would be useless.
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{},
		Payload:     eventPayload(),
	})
	assert.False(t, resp.Matched)
	assert.Contains(t, resp.Reason, "never registered")
}

func TestCheckTriggerMatch_EventTypeWithoutFilterMatchesEveryEventOfThatType(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"event_type": "KubePodCrashLooping"},
		Payload:     eventPayload(),
	})
	assert.True(t, resp.Matched)
	assert.Contains(t, resp.Reason, "no filter")
}

func TestCheckTriggerMatch_Optimization(t *testing.T) {
	params := map[string]any{
		"categories": []any{"RightSizing", "InfraUpgrade"},
	}
	// The second selected category must match too — the panel's mock only ever
	// seeds the first one.
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerOptimization,
		Params:      params,
		Payload:     map[string]any{"category": "InfraUpgrade", "rule_name": "AWS Ec2 Idle Instance", "cloud_account_id": "test-account"},
	})
	assert.True(t, resp.Matched)
	assert.Equal(t, "{{ event.category in ['RightSizing', 'InfraUpgrade'] }}", resp.Filter)

	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerOptimization,
		Params:      params,
		Payload:     map[string]any{"category": "Configuration", "cloud_account_id": "test-account"},
	})
	assert.False(t, resp.Matched)
	assert.Equal(t, model.CheckTriggerGateFilter, resp.Gate)
}

func TestCheckTriggerMatch_OptimizationRejectsForeignEventType(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerOptimization,
		Params:      map[string]any{"categories": []any{"RightSizing"}},
		Payload:     map[string]any{"event_type": "KubePodCrashLooping", "category": "RightSizing"},
	})
	assert.False(t, resp.Matched)
	assert.Equal(t, model.CheckTriggerGateEventType, resp.Gate)
	assert.Contains(t, resp.Reason, "optimization.recommendation")
}

func TestCheckTriggerMatch_OptimizationWithNoParams(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerOptimization,
		Params:      map[string]any{},
		Payload:     map[string]any{"category": "RightSizing"},
	})
	assert.False(t, resp.Matched)
	assert.Contains(t, resp.Reason, "never registered")
}

func TestCheckTriggerMatch_BadFilterReportsAnError(t *testing.T) {
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"filter": `{{ event.event_type == }}`},
		Payload:     eventPayload(),
	})
	assert.False(t, resp.Matched)
	assert.NotEmpty(t, resp.Error)

	// A filter that panics mid-render (bad timezone) is reported, not fatal. The
	// custom date filters are registered by this package's init, so this exercises
	// the same code path production uses.
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"filter": `{{ now() | tz("Not/AZone") | strftime("%H") | int > 0 }}`},
		Payload:     eventPayload(),
	})
	assert.False(t, resp.Matched)
	assert.NotEmpty(t, resp.Error)
}

func TestCheckTriggerMatch_UnsupportedTriggerType(t *testing.T) {
	service := &Service{}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	_, err := service.CheckTriggerMatch(sc, "test-account", model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerSchedule,
		Payload:     map[string]any{},
	})
	assert.Error(t, err)
}

func TestCheckTriggerMatch_DeniesForeignAccount(t *testing.T) {
	service := &Service{}
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	_, err := service.CheckTriggerMatch(sc, "other-account", model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Payload:     map[string]any{},
	})
	assert.Error(t, err)
}

func TestCheckTriggerMatch_AccountGate(t *testing.T) {
	foreign := eventPayload()
	foreign["cloud_account_id"] = "other-account"

	// No accounts param: only the workflow's own account's events fire it.
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerEvent,
		Params:      map[string]any{"event_type": "KubePodCrashLooping"},
		Payload:     foreign,
	})
	assert.False(t, resp.Matched)
	assert.Equal(t, model.CheckTriggerGateAccount, resp.Gate)
	assert.Contains(t, resp.Reason, "'other-account'")

	// Listed accounts fire it, the workflow's own included only when listed.
	params := map[string]any{"event_type": "KubePodCrashLooping", "accounts": []any{"other-account"}}
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{TriggerType: model.WorkflowTriggerEvent, Params: params, Payload: foreign})
	assert.True(t, resp.Matched)
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{TriggerType: model.WorkflowTriggerEvent, Params: params, Payload: eventPayload()})
	assert.Equal(t, model.CheckTriggerGateAccount, resp.Gate)

	// account_id wins over cloud_account_id, as in the consumer.
	payload := eventPayload()
	payload["account_id"] = "other-account"
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{TriggerType: model.WorkflowTriggerEvent, Params: params, Payload: payload})
	assert.True(t, resp.Matched)

	// The consumer drops events without an account, so they never match.
	delete(payload, "account_id")
	delete(payload, "cloud_account_id")
	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{TriggerType: model.WorkflowTriggerEvent, Params: params, Payload: payload})
	assert.Equal(t, model.CheckTriggerGateAccount, resp.Gate)
}

func TestCheckTriggerMatch_OptimizationClustersAreListenAccounts(t *testing.T) {
	params := map[string]any{"clusters": []any{"other-account"}}
	resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerOptimization,
		Params:      params,
		Payload:     map[string]any{"category": "RightSizing", "cloud_account_id": "other-account"},
	})
	assert.True(t, resp.Matched)

	resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{
		TriggerType: model.WorkflowTriggerOptimization,
		Params:      params,
		Payload:     map[string]any{"category": "RightSizing", "cloud_account_id": "test-account"},
	})
	assert.Equal(t, model.CheckTriggerGateAccount, resp.Gate)
}

// #42099: params.event_type stored as a list (what the builder saves) was read as
// "no event type", so the check skipped the type gate and, with no filter, reported
// the trigger as never registered although the registry does register it.
func TestCheckTriggerMatch_EventTypeList(t *testing.T) {
	for name, types := range map[string]any{"[]any": []any{"Foo", "KubePodCrashLooping"}, "[]string": []string{"Foo", "KubePodCrashLooping"}} {
		t.Run(name, func(t *testing.T) {
			resp := checkTriggerMatch(t, model.CheckTriggerMatchRequest{
				TriggerType: model.WorkflowTriggerEvent,
				Params:      map[string]any{"event_type": types},
				Payload:     eventPayload(),
			})
			assert.True(t, resp.Matched, resp.Reason)

			payload := eventPayload()
			payload["event_type"] = "Bar"
			resp = checkTriggerMatch(t, model.CheckTriggerMatchRequest{
				TriggerType: model.WorkflowTriggerEvent,
				Params:      map[string]any{"event_type": types},
				Payload:     payload,
			})
			assert.False(t, resp.Matched)
			assert.Equal(t, model.CheckTriggerGateEventType, resp.Gate)
			assert.Contains(t, resp.Reason, "'Bar'")
			assert.Contains(t, resp.Reason, "'Foo', 'KubePodCrashLooping'")
		})
	}
}
