//go:build e2e

package core

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetConversationTimeAggregates_RollupShape(t *testing.T) {
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}
	RequireEnv(t, "TEST_ACCOUNT")
	accountID := os.Getenv("TEST_ACCOUNT")

	filter := ConversationTimeAggregatesFilter{
		AccountIDs:     []string{accountID},
		StartDate:      time.Now().Add(-7 * 24 * time.Hour),
		EndDate:        time.Now(),
		ExcludedTitles: []string{EventDetailsRetrievalTitle},
	}
	result, err := GetConversationDao().GetConversationTimeAggregates(filter)

	assert.Nil(t, err)
	assert.GreaterOrEqual(t, result.CompletedCount, 0)
	assert.GreaterOrEqual(t, result.TotalCount, result.CompletedCount, "TotalCount must include CompletedCount")
	assert.GreaterOrEqual(t, result.TotalWallTimeSeconds, 0.0)
	assert.GreaterOrEqual(t, result.TotalAgentActiveTimeSeconds, 0.0)
	assert.GreaterOrEqual(t, result.TotalToolTimeSeconds, 0.0)

	// All three time totals are scoped to COMPLETED rows. If completed_count
	// is zero, every total must be zero — catches a regression where the
	// time CTEs accidentally widen back to all rows.
	if result.CompletedCount == 0 {
		assert.Equal(t, 0.0, result.TotalWallTimeSeconds, "wall time must be 0 when no completed rows")
		assert.Equal(t, 0.0, result.TotalAgentActiveTimeSeconds, "agent time must be 0 when no completed rows")
		assert.Equal(t, 0.0, result.TotalToolTimeSeconds, "tool time must be 0 when no completed rows")
	}
}

func TestGetConversationTimeAggregates_NoAccounts(t *testing.T) {
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}

	filter := ConversationTimeAggregatesFilter{
		AccountIDs: nil,
		StartDate:  time.Now().Add(-24 * time.Hour),
		EndDate:    time.Now(),
	}
	result, err := GetConversationDao().GetConversationTimeAggregates(filter)

	assert.Nil(t, err)
	assert.Equal(t, ConversationTimeAggregates{}, result, "empty accountIDs must return zero without touching the DB")
}
