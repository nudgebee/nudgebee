package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationConnectionForAccountRequiresAccountReadAccess(t *testing.T) {
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{
		"TenantId":"tenant-1",
		"UserId":"user-1",
		"AccountIds":["account-A"],
		"Roles":["account_admin_readonly"],
		"ScopedEntityIds":{"account_admin_readonly":["account-A"]}
	}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

	err := TestIntegrationConnectionForAccount(ctx, "integration-1", "account-B")
	assert.EqualError(t, err, "integrations: connection test is not permitted")
}
