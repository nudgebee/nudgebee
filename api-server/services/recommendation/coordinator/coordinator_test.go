package coordinator

import (
	"strings"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

func TestLegalSettle(t *testing.T) {
	inProgress := models.RecommendationResolutionStatusInProgress
	success := models.RecommendationResolutionStatusSuccess
	failed := models.RecommendationResolutionStatusFailed

	tests := []struct {
		name    string
		current models.RecommendationResolutionStatus
		target  models.RecommendationResolutionStatus
		ok      bool
	}{
		{"in progress settles to success", inProgress, success, true},
		{"in progress settles to failed", inProgress, failed, true},
		{"duplicate success delivery is a no-op", success, success, false},
		{"duplicate failed delivery is a no-op", failed, failed, false},
		{"success may not flip to failed", success, failed, false},
		{"failed may not flip to success", failed, success, false},
		{"settling to in progress is not a settle", inProgress, inProgress, false},
		{"settling to an unknown status is refused", inProgress, models.RecommendationResolutionStatus("Configuring"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := legalSettle(tt.current, tt.target)
			assert.Equal(t, tt.ok, ok)
			if !tt.ok {
				assert.NotEmpty(t, reason)
			}
		})
	}
}

func TestProjectRecommendation(t *testing.T) {
	success := models.RecommendationResolutionStatusSuccess
	failed := models.RecommendationResolutionStatusFailed

	tests := []struct {
		name    string
		outcome models.RecommendationResolutionStatus
		current models.RecommendationStatus
		want    models.RecommendationStatus
		applies bool
	}{
		{"success closes an in-progress claim", success, models.RecommendationStatusInProgress, models.RecommendationStatusClosed, true},
		{"success closes even after a reset to open", success, models.RecommendationStatusOpen, models.RecommendationStatusClosed, true},
		{"success never resurrects dismissed", success, models.RecommendationStatusDismissed, models.RecommendationStatusDismissed, false},
		{"success leaves archived alone", success, models.RecommendationStatusArchive, models.RecommendationStatusArchive, false},
		{"success on already closed is a no-op", success, models.RecommendationStatusClosed, models.RecommendationStatusClosed, false},
		{"failure hands an in-progress claim back", failed, models.RecommendationStatusInProgress, models.RecommendationStatusOpen, true},
		{"failure does not touch open", failed, models.RecommendationStatusOpen, models.RecommendationStatusOpen, false},
		{"failure does not reopen closed", failed, models.RecommendationStatusClosed, models.RecommendationStatusClosed, false},
		{"failure does not reopen dismissed", failed, models.RecommendationStatusDismissed, models.RecommendationStatusDismissed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, applies := projectRecommendation(tt.outcome, tt.current)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.applies, applies)
		})
	}
}

func TestLegalDismissal(t *testing.T) {
	tests := []struct {
		name      string
		current   models.RecommendationStatus
		dismissed bool
		ok        bool
	}{
		{"open can be dismissed", models.RecommendationStatusOpen, true, true},
		{"in-progress work is never dismissed", models.RecommendationStatusInProgress, true, false},
		{"closed is never dismissed", models.RecommendationStatusClosed, true, false},
		{"archived is never dismissed", models.RecommendationStatusArchive, true, false},
		{"dismissed twice is a no-op", models.RecommendationStatusDismissed, true, false},
		{"dismissed can be reactivated", models.RecommendationStatusDismissed, false, true},
		{"open cannot be reactivated", models.RecommendationStatusOpen, false, false},
		{"closed cannot be reactivated", models.RecommendationStatusClosed, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := legalDismissal(tt.current, tt.dismissed)
			assert.Equal(t, tt.ok, ok)
			if !tt.ok {
				assert.NotEmpty(t, reason)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	assert.Equal(t, "short", truncateMessage("short"))
	long := strings.Repeat("x", 900)
	truncated := truncateMessage(long)
	assert.Len(t, truncated, 800+len(" …(truncated)"))
	assert.True(t, strings.HasSuffix(truncated, "…(truncated)"))
}
