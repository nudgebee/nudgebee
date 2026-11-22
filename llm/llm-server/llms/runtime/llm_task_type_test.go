package runtime

import (
	"context"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTierAttributionForRecord confirms the writer helper returns nil pointers
// when nothing is stamped (legacy rows stay NULL) and populated pointers when
// tier / task_type are present on the context.
func TestTierAttributionForRecord(t *testing.T) {
	// nil context → both nil (no panic).
	nilTier, nilTask := tierAttributionForRecord(nil)
	assert.Nil(t, nilTier)
	assert.Nil(t, nilTask)

	// Unstamped context → both nil.
	bare := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	tier, task := tierAttributionForRecord(bare)
	assert.Nil(t, tier)
	assert.Nil(t, task)

	// Stamped tier + task_type → both non-nil with the stamped values.
	stamped := security.NewRequestContext(
		context.WithValue(
			context.WithValue(context.Background(), ContextKeyModelTier, ModelTierSummary),
			ContextKeyTaskType, TaskTypeQuery,
		),
		nil, nil, nil, nil,
	)
	tier, task = tierAttributionForRecord(stamped)
	if assert.NotNil(t, tier) {
		assert.Equal(t, string(ModelTierSummary), *tier)
	}
	if assert.NotNil(t, task) {
		assert.Equal(t, TaskTypeQuery, *task)
	}
}
