package triage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func withMintCap(t *testing.T, limit int) {
	t.Helper()
	prev := mintAttemptsPerTenantPerDay
	mintAttemptsPerTenantPerDay = limit
	t.Cleanup(func() { mintAttemptsPerTenantPerDay = prev })
}

func TestMintBudget_ExhaustedAfterCapAttempts(t *testing.T) {
	withMintCap(t, 3)
	b := &mintBudget{tenants: map[string]*tenantMintBudget{}}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		assert.False(t, b.exhausted("t1", now), "attempt %d is within the cap", i+1)
		b.record("t1", now)
	}
	assert.True(t, b.exhausted("t1", now), "the 4th attempt in the day is over the cap")
	assert.False(t, b.exhausted("t2", now), "the cap is per tenant")
}

func TestMintBudget_NewDayResetsTheCount(t *testing.T) {
	withMintCap(t, 1)
	b := &mintBudget{tenants: map[string]*tenantMintBudget{}}
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

	b.record("t1", start)
	assert.True(t, b.exhausted("t1", start.Add(23*time.Hour)))
	assert.False(t, b.exhausted("t1", start.Add(24*time.Hour)), "a day after the window opened, attempts start over")
}
