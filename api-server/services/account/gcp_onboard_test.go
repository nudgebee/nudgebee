package account

import (
	"log/slog"
	"net/url"
	"nudgebee/services/config"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGCPOnBoardUrl_Vulnerability(t *testing.T) {
	testenv.RequireMetastore(t)
	// Save original config
	origUrl := config.Config.NUDGEBEE_URL
	origRole := config.Config.NUDGEBEE_INSTANCE_ROLE
	defer func() {
		config.Config.NUDGEBEE_URL = origUrl
		config.Config.NUDGEBEE_INSTANCE_ROLE = origRole
	}()

	// Setup mock config
	config.Config.NUDGEBEE_URL = "https://nudgebee.com"
	config.Config.NUDGEBEE_INSTANCE_ROLE = "role"

	// Create a request context with tenant and user
	logger := slog.Default()
	ctx := security.NewRequestContextForUserTenant("user1", "tenant1", logger, nil, nil)

	// malicious account name with parameter injection
	maliciousAccountName := "test&injected=true"

	req := AccountCreateRequest{
		CloudProvider: "GCP",
		AccountName:   maliciousAccountName,
	}

	resp, err := GCPOnBoardUrl(ctx, req)
	assert.NoError(t, err)

	// In the vulnerable version, the URL string is constructed via Sprintf.
	// "https://console.cloud.google.com/dm/deploy/new?project=%s...&param_NudgebeeAccountName=%s"

	// If the fix is working, "injected" should NOT be a query parameter.
	// If it is vulnerable, "injected" WILL be a query parameter.

	parsedUrl, err := url.Parse(resp.Url)
	assert.NoError(t, err)

	queryParams := parsedUrl.Query()

	// If vulnerable, this assertion will fail (injected will be "true")
	// If fixed, this assertion will pass (injected will be empty)
	injectedParam := queryParams.Get("injected")
	assert.Empty(t, injectedParam, "Vulnerability found: 'injected' parameter exists in URL")

	// Also ensure the account name is correctly encoded in the actual parameter
	accountNameParam := queryParams.Get("param_NudgebeeAccountName")
	assert.Equal(t, maliciousAccountName, accountNameParam)
}
