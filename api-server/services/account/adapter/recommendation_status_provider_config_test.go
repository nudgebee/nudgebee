package adapter

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

// TestGetRecommendationResolutionStatus_MissingProviderConfig verifies that a
// resolution whose data lacks provider_config is reported InProgress (not an
// error). The status-sync cron maps any error to Failed and re-opens the
// recommendation; without this, a successfully-raised PR whose data lost
// provider_config (pre-fix rows) would be flipped to Failed on every tick.
// Covers both the github and gitlab adapters. The check short-circuits before
// any DB/network access, so no fixtures are needed.
func TestGetRecommendationResolutionStatus_MissingProviderConfig(t *testing.T) {
	ctxt := &testAccountAdapterContext{ctx: context.Background(), logger: slog.Default()}

	// PR descriptor exactly as ApplyRecommendationUsingCodeAgent's prMeta used
	// to overwrite it: no provider_config key.
	data := models.NewJsonObject(map[string]any{
		"org":      "nudgebee",
		"repo":     "nudgebee-infra",
		"branch":   "main",
		"pr_url":   "https://github.com/nudgebee/nudgebee-infra/pull/689",
		"provider": "github",
		"repo_url": "https://github.com/nudgebee/nudgebee-infra",
	})

	adapters := map[string]interface {
		GetRecommendationResolutionStatus(AccountAdapterContext, models.Recommendation, string, models.Json, string) (GetRecommendationResolutionStatusResponse, error)
	}{
		"github": &githubAdapter{},
		"gitlab": &gitlabAdapter{},
	}

	for name, a := range adapters {
		t.Run(name, func(t *testing.T) {
			resp, err := a.GetRecommendationResolutionStatus(ctxt, models.Recommendation{}, "https://github.com/nudgebee/nudgebee-infra/pull/689", data, "")
			assert.NoError(t, err, "missing provider_config must not be a hard error")
			assert.Equal(t, RecommendationResolutionStatusInProgress, resp.Status,
				"missing provider_config must keep the resolution InProgress, not Failed")
		})
	}
}
