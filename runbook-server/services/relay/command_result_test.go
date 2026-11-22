package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeScriptRelay answers like the agent does for a script job: the evidence
// carries the {response, exit_code} payload.
func fakeScriptRelay(t *testing.T, payload map[string]any) {
	t.Helper()
	block, _ := json.Marshal(payload)
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

// #41249: the exit code was dropped here, so `exit 3` reached its task as success.
func TestExecuteRelayJob_ScriptJobKeepsExitCode(t *testing.T) {
	fakeScriptRelay(t, map[string]any{"response": "", "exit_code": 3})
	resp, err := ExecuteRelayJob(newTestRequestContext(), "test-account", RelayJobShell, "", "exit 3", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"stdout":"","exit_code":3}`, resp.(string))
}

// An agent that sends no exit_code must get exactly today's {"stdout": ...} reply.
func TestExecuteRelayJob_ScriptJobWithoutExitCodeIsUnchanged(t *testing.T) {
	fakeScriptRelay(t, map[string]any{"response": "q1\n"})
	resp, err := ExecuteRelayJob(newTestRequestContext(), "test-account", RelayJobShell, "", "echo q1", nil)
	require.NoError(t, err)
	assert.Equal(t, `{"stdout":"q1\n"}`, resp.(string))
}

func TestInterpretCommandResponse(t *testing.T) {
	cases := []struct {
		name    string
		resp    string
		ok      []int
		wantErr string
		want    map[string]any
	}{
		{name: "exit 3 with no output fails and names the code",
			resp: `{"stdout":"","exit_code":3}`, wantErr: "rabbitmqadmin exited 3: no output"},
		{name: "rabbitmqadmin ERROR on stdout fails with that text",
			resp:    `{"stdout":"*** ERROR: Don't know how to list nosuchthing\n","exit_code":1}`,
			wantErr: "Don't know how to list nosuchthing"},
		{name: "missing binary fails",
			resp: `{"stdout":"/tmp/script.sh: 1: list: not found\n","exit_code":127}`, wantErr: "exited 127: /tmp/script.sh: 1: list: not found"},
		{name: "success keeps stdout and exit code",
			resp: `{"stdout":"name\nq1\n","exit_code":0}`, want: map[string]any{"data": "name\nq1\n", "exit_code": 0}},
		{name: "an allowed non-zero exit is a result",
			resp: `{"stdout":"diff","exit_code":1}`, ok: []int{1}, want: map[string]any{"data": "diff", "exit_code": 1}},
		{name: "older agent without exit code: stderr still fails",
			resp: `{"stdout":"","stderr":"connection refused"}`, wantErr: "connection refused"},
		{name: "older agent without exit code: clean run succeeds",
			resp: `{"stdout":"ok"}`, want: map[string]any{"data": "ok"}},
		{name: "older agent without exit code: output with an ERROR line still succeeds, as today",
			resp: `{"stdout":"*** ERROR: x\n"}`, want: map[string]any{"data": "*** ERROR: x\n"}},
		{name: "exit 0 with a warning on stderr succeeds",
			resp: `{"stdout":"ok","stderr":"Warning: deprecated","exit_code":0}`,
			want: map[string]any{"data": "ok", "stderr": "Warning: deprecated", "exit_code": 0}},
		{name: "agent refusal fails", resp: `{"error":"only get commands are allowed"}`, wantErr: "only get commands are allowed"},
		{name: "non-JSON reply fails with the reply", resp: `boom`, wantErr: "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := InterpretCommandResponse("rabbitmqadmin", tc.resp, tc.ok...)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Nil(t, out)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, out)
		})
	}
}

func TestExitCode(t *testing.T) {
	for _, c := range []struct {
		in   any
		want int
		ok   bool
	}{
		{float64(0), 0, true}, {float64(1), 1, true}, {float32(4), 4, true}, {float64(-1), -1, true},
		{2, 2, true}, {int32(5), 5, true}, {int64(3), 3, true}, {nil, 0, false}, {"1", 0, false},
	} {
		got, ok := ExitCode(c.in)
		assert.Equal(t, c.ok, ok)
		assert.Equal(t, c.want, got)
	}
}
