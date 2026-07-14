package recommendation

import (
	"testing"
	"time"
)

func TestWastedSinceDetected(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		savings   float64
		createdAt time.Time
		want      float64
	}{
		{"thirty days open accrues one month of savings", 600, now.AddDate(0, 0, -30), 600},
		{"fifteen days open accrues half", 600, now.AddDate(0, 0, -15), 300},
		{"uncapped past thirty days", 300, now.AddDate(0, 0, -60), 600},
		{"zero savings accrues nothing", 0, now.AddDate(0, 0, -30), 0},
		{"negative savings accrues nothing", -50, now.AddDate(0, 0, -30), 0},
		{"zero created_at accrues nothing", 600, time.Time{}, 0},
		{"future created_at accrues nothing", 600, now.AddDate(0, 0, 1), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WastedSinceDetected(tt.savings, tt.createdAt, now)
			if diff := got - tt.want; diff > 0.01 || diff < -0.01 {
				t.Errorf("WastedSinceDetected(%v, %v) = %v, want %v", tt.savings, tt.createdAt, got, tt.want)
			}
		})
	}
}
