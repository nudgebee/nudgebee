package aws

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestNearestValidFargate(t *testing.T) {
	tests := []struct {
		name      string
		reqVCPU   float64
		reqMemGB  float64
		wantVCPU  float64
		wantMemGB float64
	}{
		{"exact smallest", 0.25, 0.5, 0.25, 0.5},
		{"snap mem up within tier", 0.25, 0.75, 0.25, 1},
		{"mem exceeds tier bumps cpu", 0.25, 3, 0.5, 3},
		{"half of 1vcpu/2gb", 0.5, 1, 0.5, 1},
		{"mid tier 1 vcpu", 1, 5, 1, 5},
		{"cpu between tiers rounds up", 0.75, 2, 1, 2},
		{"8 vcpu 4gb step", 8, 18, 8, 20},
		{"over max clamps to largest", 32, 200, 16, 120},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVCPU, gotMem := nearestValidFargate(tt.reqVCPU, tt.reqMemGB)
			if gotVCPU != tt.wantVCPU || gotMem != tt.wantMemGB {
				t.Errorf("nearestValidFargate(%v, %v) = (%v, %v), want (%v, %v)",
					tt.reqVCPU, tt.reqMemGB, gotVCPU, gotMem, tt.wantVCPU, tt.wantMemGB)
			}
		})
	}
}

func TestFargateTaskMonthlyCost(t *testing.T) {
	// 1 vCPU + 2 GB at $0.04048/vCPU-hr and $0.004445/GB-hr, single task.
	// hourly = 0.04048 + 2*0.004445 = 0.04937; monthly = *720 = 35.5464
	got := fargateTaskMonthlyCost(1, 2, 0.04048, 0.004445, 1)
	want := (0.04048 + 2*0.004445) * 24 * 30
	if !almostEqual(got, want) {
		t.Errorf("fargateTaskMonthlyCost = %v, want %v", got, want)
	}
	// Desired count scales linearly.
	if scaled := fargateTaskMonthlyCost(1, 2, 0.04048, 0.004445, 3); !almostEqual(scaled, want*3) {
		t.Errorf("desiredCount scaling = %v, want %v", scaled, want*3)
	}
}
