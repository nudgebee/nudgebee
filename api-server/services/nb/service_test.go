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
	CleanupData(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), "events_normal")
}
