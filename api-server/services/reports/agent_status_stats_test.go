package reports

import (
	"testing"

	"nudgebee/services/internal/database/models"
)

// A cluster without Karpenter reports karpenterEnabled=false. That is not a
// broken integration, so it must not make the agent "partial connectivity".
func TestAgentStatusStatsKarpenterIsOptional(t *testing.T) {
	healthy := map[string]interface{}{
		"alertManagerConnection": true,
		"grafanaEnabled":         true,
		"logsConnection":         true,
		"opencostConnection":     true,
	}
	with := func(extra map[string]interface{}) map[string]interface{} {
		out := map[string]interface{}{}
		for k, v := range healthy {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	tests := []struct {
		name        string
		connStatus  map[string]interface{}
		wantPartial int
	}{
		{"no Karpenter, flag absent", with(nil), 0},
		{"no Karpenter, flag false", with(map[string]interface{}{"karpenterEnabled": false}), 0},
		{"no Karpenter, other autoscaler", with(map[string]interface{}{"karpenterEnabled": false, "autoScalerType": "cluster-autoscaler"}), 0},
		{"Karpenter connected", with(map[string]interface{}{"karpenterEnabled": true, "autoScalerType": "karpenter"}), 0},
		{"Karpenter is the autoscaler but not connected", with(map[string]interface{}{"karpenterEnabled": false, "autoScalerType": "Karpenter"}), 1},
		{"a required connection down", with(map[string]interface{}{"logsConnection": false}), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accounts := []models.Account{{Id: "a1"}}
			agents := map[string][]interface{}{
				"a1": {map[string]interface{}{"status": "RUNNING", "connection_status": tc.connStatus}},
			}
			total, disconnected, partial := computeAgentStatusStats(accounts, agents)
			if total != 1 || disconnected != 0 || partial != tc.wantPartial {
				t.Errorf("got total=%d disconnected=%d partial=%d, want 1/0/%d", total, disconnected, partial, tc.wantPartial)
			}
		})
	}
}
