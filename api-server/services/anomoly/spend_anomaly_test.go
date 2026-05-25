package anomoly

import (
	"nudgebee/services/config"
	"nudgebee/services/security"
	"testing"
	"time"
)

func TestSpendAnomaly(t *testing.T) {
	// Target Feb 19 2026 — AWS prod RDS spike ($76.78 vs $20.42 avg, z=16.48)
	targetDate := time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC)
	config.Config.NBSpendAnomalyBaselineDays = 30
	config.Config.NBSpendAnomalyZScoreThreshold = 3.0
	config.Config.NBSpendAnomalyMinAbsChange = 50.0
	config.Config.NBSpendAnomalyMinPctChange = 20.0
	config.Config.NBSpendAnomalyMinBaselineSpend = 10.0
	config.Config.NBSpendAnomalyCooldownDays = 0

	// Override target date for testing
	SpendAnomalyTargetDate = &targetDate

	ctx := security.NewRequestContextForUserTenant(
		"6c008cf8-4d79-4999-8447-573a697d0652",
		"890cad87-c452-4aa7-b84a-742cee0454a1",
		nil, nil, nil,
	)

	err := ExecuteSpendAnomalyForAccount(ctx, "6c008cf8-4d79-4999-8447-573a697d0652")
	if err != nil {
		t.Fatalf("ExecuteSpendAnomalyForAccount failed: %v", err)
	}
}
