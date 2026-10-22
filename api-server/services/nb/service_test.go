package nb

import (
	"log/slog"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetVersions(t *testing.T) {
	testenv.RequireEnv(t, "GITHUB_TOKEN")
	resp, err := GetVersions(&security.RequestContext{})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp["agent_version_latest"])
}

func TestJobCleanup(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
	CleanupData(ctx, "events_normal")
	CleanupData(ctx, "event_resolutions_orphaned")
	CleanupData(ctx, "cloud_resource_metrics")
	CleanupData(ctx, "cloud_resources_inactive")
	CleanupData(ctx, "application_profile")
	CleanupData(ctx, "anomaly")
	CleanupData(ctx, "slo_report")
}

// Installs without the legacy relationships table drop its guard rather than fail
// the whole inactive-resources sweep with "relation does not exist".
func TestRelationshipGuards(t *testing.T) {
	assert.Empty(t, relationshipGuards(false))
	guard := relationshipGuards(true)
	assert.Contains(t, guard, "crr.source_id = cr.id")
	assert.Contains(t, guard, "crr.destination_id = cr.id")
}
