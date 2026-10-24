package notifications

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func serveSendResult(t *testing.T, result map[string]any) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{result})
	}))
	t.Cleanup(server.Close)
	old := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = server.URL
	t.Cleanup(func() { config.Config.NotificationServerUrl = old })
}

func newImTaskContext() types.TaskContext {
	return testutils.NewTestTaskContext(uuid.NewString(), uuid.NewString(), uuid.NewString(), slog.Default())
}

func imParams() map[string]any {
	return map[string]any{"provider": "slack", "channel": "C123", "message": "step done"}
}

func TestImSendTask_FailsNamingTheReasonWhenNothingWasPosted(t *testing.T) {
	for _, tc := range []struct {
		name         string
		result       map[string]any
		nonRetryable bool
	}{
		{"permanent", map[string]any{"status": "failed", "reason": "not_in_channel", "retryable": false}, true},
		{"temporary", map[string]any{"status": "failed", "reason": "rate_limited", "retryable": true}, false},
		{"nowhere to send", map[string]any{"status": "not_sent", "reason": "no_destination"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveSendResult(t, tc.result)

			output, err := (&ImSendTask{}).Execute(newImTaskContext(), imParams())

			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, types.ErrTypeNotificationNotDelivered, appErr.Type())
			assert.Equal(t, tc.nonRetryable, appErr.NonRetryable())
			assert.Equal(t, tc.result["reason"], output.(map[string]any)["reason"])
		})
	}
}

func TestImSendTask_ErrorNamesTheMissingConnection(t *testing.T) {
	serveSendResult(t, map[string]any{"platform": "system", "status": "not_sent", "reason": "no_destination"})
	params := imParams()
	params["provider"] = "ms_teams"

	_, err := (&ImSendTask{}).Execute(newImTaskContext(), params)

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "Microsoft Teams isn't connected for this tenant. Connect the Microsoft Teams app in Integrations, or pick another provider.", appErr.Message())
}

func TestImSendTask_ASuppressedMessageStillCompletes(t *testing.T) {
	serveSendResult(t, map[string]any{"platform": "system", "status": "suppressed", "reason": "Muted by a rule"})

	output, err := (&ImSendTask{}).Execute(newImTaskContext(), imParams())

	assert.NoError(t, err)
	assert.Equal(t, "Muted by a rule", output.(map[string]any)["reason"])
}
