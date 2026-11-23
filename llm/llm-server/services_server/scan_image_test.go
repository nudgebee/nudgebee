package services_server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteScanImageQuerySendsTheGrantKey: the server honours a custom-role
// grant only when the call names the action's grant key, so the scan request
// must carry it. The caller stays the session user.
func TestExecuteScanImageQuerySendsTheGrantKey(t *testing.T) {
	calls := forecastServer(t, http.StatusOK, `{"data":[{"image":"registry.example.com/web:1.0"}]}`)

	_, err := ExecuteScanImageQuery(ScanImageServiceRequest{
		Action:           Action{Name: "security_scan_image"},
		Input:            ScanImageRequest{AccountId: "acc-1", Namespace: "default", Workload: "web"},
		SessionVariables: SessionVariables{UserID: "automation-user", UserTenantID: "tenant-1"},
	})
	require.NoError(t, err)

	got := <-calls
	assert.Equal(t, "/rpc/recommendation", got.path)
	assert.Equal(t, map[string]any{"name": "security_scan_image", "permission": "security:Execute"}, got.payload["action"])
	sv, _ := got.payload["session_variables"].(map[string]any)
	assert.Equal(t, "automation-user", sv["user_id"], "the grant is checked on the caller: %v", got.payload["session_variables"])
}
