package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/internal/database/models"
)

func jsonFixture(t *testing.T, obj any) models.Json {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var j models.Json
	if err := j.Scan(raw); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return j
}

// An apply payload that yields zero containers (empty map, or junk keys only)
// must be rejected — previously it produced a no-op agent task while the
// recommendation moved to InProgress and looked resolved.
func TestApplyRecommendationRejectsPayloadWithoutContainers(t *testing.T) {
	k := &kuberntesAdapter{}
	for name, data := range map[string]map[string]any{
		"empty":     {},
		"junk-only": {"card_id": "abc", "accountId": "acc-1"},
	} {
		t.Run(name, func(t *testing.T) {
			workload := "web"
			request := ApplyRecommendationRequest{
				Data: data,
				Recommendation: models.Recommendation{
					Id:       "rec-1",
					RuleName: "pod_right_sizing",
					Category: "RightSizing",
				},
				Resource: models.Resource{
					Id:   "res-1",
					Name: &workload,
					Meta: jsonFixture(t, map[string]any{"namespace": "default", "controllerKind": "Deployment"}),
				},
				ProviderConfig: map[string]any{},
			}
			_, err := k.ApplyRecommendation(nil, request, nil, "resolution-1")
			if err == nil {
				t.Fatal("expected error for payload with no container resource changes")
			}
			if !strings.Contains(err.Error(), "no container resource changes") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// PrecheckApply has to refuse exactly what the apply would, because callers
// rely on it to decide whether an attempt is worth recording. A refusal it
// misses is stored as a failed attempt and offered for a retry that can never
// succeed.
func TestPrecheckApplyRefusesWhatTheApplyWould(t *testing.T) {
	k := &kuberntesAdapter{}
	workload := "web"
	base := func(data map[string]any) ApplyRecommendationRequest {
		return ApplyRecommendationRequest{
			Data: data,
			Recommendation: models.Recommendation{
				Id:             "rec-1",
				RuleName:       "pod_right_sizing",
				Category:       "RightSizing",
				Recommendation: jsonFixture(t, map[string]any{}),
			},
			Resource: models.Resource{
				Id:   "res-1",
				Name: &workload,
				Meta: jsonFixture(t, map[string]any{"namespace": "default", "controllerKind": "Deployment"}),
			},
			ProviderConfig: map[string]any{},
		}
	}
	for name, tc := range map[string]struct {
		data map[string]any
		want string
	}{
		"no container changes": {
			data: map[string]any{"card_id": "abc"},
			want: "no container resource changes",
		},
		"request above limit": {
			data: map[string]any{"app": map[string]any{
				"memory": map[string]any{"request": "2Gi", "limit": "1Gi"},
			}},
			want: "is above memory_limit",
		},
	} {
		t.Run(name, func(t *testing.T) {
			precheckErr := PrecheckApply(&testAccountAdapterContext{}, k, base(tc.data))
			if precheckErr == nil || !strings.Contains(precheckErr.Error(), tc.want) {
				t.Fatalf("precheck: want an error containing %q, got %v", tc.want, precheckErr)
			}
			_, applyErr := k.ApplyRecommendation(&testAccountAdapterContext{}, base(tc.data), nil, "resolution-1")
			if applyErr == nil || applyErr.Error() != precheckErr.Error() {
				t.Fatalf("apply and precheck must refuse with the same reason; apply: %v, precheck: %v", applyErr, precheckErr)
			}
		})
	}
}

// Rules with nothing to decide up front, and adapters that make no such check,
// must pass straight through — the precheck is not allowed to block an apply it
// knows nothing about.
func TestPrecheckApplyPassesWhatItDoesNotCheck(t *testing.T) {
	other := ApplyRecommendationRequest{Recommendation: models.Recommendation{RuleName: "unused_pvc", Category: "RightSizing"}}
	if err := PrecheckApply(&testAccountAdapterContext{}, &kuberntesAdapter{}, other); err != nil {
		t.Fatalf("a rule other than pod_right_sizing must pass the precheck, got: %v", err)
	}
	podResize := ApplyRecommendationRequest{Recommendation: models.Recommendation{RuleName: "pod_right_sizing"}}
	if err := PrecheckApply(&testAccountAdapterContext{}, &githubAdapter{}, podResize); err != nil {
		t.Fatalf("an adapter without a precheck must pass, got: %v", err)
	}
	if err := PrecheckApply(&testAccountAdapterContext{}, nil, podResize); err != nil {
		t.Fatalf("a missing adapter must pass the precheck and fail where it always has, got: %v", err)
	}
}
