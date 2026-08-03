package recommendation

import (
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

func TestTicketOutcomeForStatus(t *testing.T) {
	success := models.RecommendationResolutionStatusSuccess
	failed := models.RecommendationResolutionStatusFailed

	tests := []struct {
		status  string
		outcome models.RecommendationResolutionStatus
		ok      bool
	}{
		{"resolved", success, true},
		{"closed", success, true},
		{"done", success, true},
		{"complete", success, true},
		{"completed", success, true},
		{"Resolved", success, true},
		{" CLOSED ", success, true},
		{"rejected", failed, true},
		{"cancelled", failed, true},
		{"canceled", failed, true},
		{"declined", failed, true},
		{"open", "", false},
		{"in progress", "", false},
		{"pending", "", false},
		{"acknowledged", "", false},
		{"", "", false},
		{"To Do", "", false},
	}
	for _, tt := range tests {
		t.Run("status_"+tt.status, func(t *testing.T) {
			outcome, ok := ticketOutcomeForStatus(tt.status)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.outcome, outcome)
			}
		})
	}
}
