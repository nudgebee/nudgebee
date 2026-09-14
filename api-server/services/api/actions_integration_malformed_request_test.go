package api

import (
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestHandleIntegrationAction_MalformedRequest_Returns400NotPanic covers the
// action names whose handlers used to do an unchecked
// actionPayload.Input["request"].(map[string]interface{}) type assertion,
// which panics instead of returning 400 when "request" is missing or isn't a
// map (e.g. the RPC payload isn't nested under "request" at all).
func TestHandleIntegrationAction_MalformedRequest_Returns400NotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	actionNames := []string{
		"integrations_create_config",
		"integrations_delete_config",
		"integrations_get_schema",
		"integration_list_config",
		"integrations_update_status",
		"integrations_test_connection",
		"integrations_test_connection_config",
		"webhook_subject_mappings_sync",
	}

	malformedInputs := map[string]map[string]any{
		"nil input map":       nil,
		"missing request key": {},
		"request not a map":   {"request": "not-a-map"},
	}

	for _, actionName := range actionNames {
		for caseName, input := range malformedInputs {
			t.Run(actionName+"/"+caseName, func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", "/rpc/integrations", nil)

				payload := &ActionRequest{
					Action:           ActionRequestAction{Name: actionName},
					Input:            input,
					SessionVariables: map[string]any{"role": "admin"},
				}

				assert.NotPanics(t, func() {
					handleIntegrationAction(payload, c, nil, nil, slog.Default())
				})
				assert.Equal(t, 400, w.Code, "expected 400 for malformed request input, got %d: %s", w.Code, w.Body.String())
			})
		}
	}
}
