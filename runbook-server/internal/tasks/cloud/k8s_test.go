package cloud

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRelay answers every relay request with the agent's kubectl JsonBlock payload.
func fakeRelay(t *testing.T, agentPayload map[string]any) {
	t.Helper()
	block, _ := json.Marshal(agentPayload)
	evidence, _ := json.Marshal([]map[string]any{{"data": string(block)}})
	body, _ := json.Marshal(map[string]any{"data": map[string]any{
		"success":  true,
		"findings": []any{map[string]any{"evidence": []any{map[string]any{"data": string(evidence)}}}},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	orig := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.RelayServerEndpoint = orig })
}

func TestK8sCliTask_ReportsKubectlOutcome(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		wantErr string
		want    any
	}{
		{name: "success returns stdout", payload: map[string]any{"stdout": "persistentvolumeclaim/data patched\n", "stderr": ""}, want: "persistentvolumeclaim/data patched\n"},
		{name: "kubectl stderr fails the task", payload: map[string]any{"stdout": "", "stderr": "error: invalid JSON document\n"}, wantErr: "invalid JSON document"},
		{name: "agent refusal fails the task", payload: map[string]any{"error": "only kubectl get commands are allowed"}, wantErr: "only kubectl get commands are allowed"},
		{name: "non-string stderr does not panic", payload: map[string]any{"stdout": "ok", "stderr": 42}, want: "ok"},
		{name: "no stdout key still succeeds (quiet commands)", payload: map[string]any{"stderr": ""}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeRelay(t, tc.payload)
			taskCtx := testutils.NewTestTaskContext("tenant", "acct", "user", slog.Default())
			out, err := (&K8sCliTask{}).Execute(taskCtx, map[string]any{"command": "kubectl patch pvc data -n ns -p '{}'"})
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"data": tc.want}, out)
		})
	}
}
