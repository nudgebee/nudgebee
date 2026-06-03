package anomoly

import (
	"nudgebee/services/config"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"
	"time"
)

func TestSpendAnomaly(t *testing.T) {
	testenv.RequireMetastore(t)
	tenant, account, user := testenv.RequireTenant(t)
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

	ctx := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)

	err := ExecuteSpendAnomalyForAccount(ctx, account)
	if err != nil {
		t.Fatalf("ExecuteSpendAnomalyForAccount failed: %v", err)
	}
}
