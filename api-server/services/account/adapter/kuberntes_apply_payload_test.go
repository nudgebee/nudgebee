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
