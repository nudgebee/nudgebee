package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/relay"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func timePtr(t time.Time) *time.Time { return &t }

// The floor/cap rules themselves are covered in eventrule/playbooks; this asserts the
// log-side wrapper delegates and converts to epoch milliseconds.
func TestResolveLogQueryWindow(t *testing.T) {
	base := time.Date(2026, 8, 20, 8, 24, 45, 0, time.UTC)
	event := playbooks.PlaybookEvent{
		StartedAt: timePtr(base),
		EndedAt:   timePtr(base.Add(5 * time.Second)),
	}

	start, end := resolveLogQueryWindow(event, 0)
	assert.Equal(t, base.Add(5*time.Second).UnixMilli(), end)
	assert.Equal(t, int64(playbooks.DefaultQueryWindowMinutes*60), (end-start)/1000)
}

func TestResolveLogQueryWindowWithoutEnd(t *testing.T) {
	// EndedAt nil means "still firing": the window ends now and still spans the floor.
	start, end := resolveLogQueryWindow(playbooks.PlaybookEvent{
		StartedAt: timePtr(time.Now()),
	}, 0)
	assert.Equal(t, int64(playbooks.DefaultQueryWindowMinutes*60), (end-start)/1000)
	assert.InDelta(t, time.Now().UnixMilli(), end, 5000)
}

func TestIsK8sLogTarget(t *testing.T) {
	tests := []struct {
		name      string
		workload  string
		namespace string
		want      bool
	}{
		{"deployment", "product-catalog", "demo", true},
		{"pod with dots", "kube-dns.v1", "kube-system", true},
		{"single character", "a", "b", true},
		// CI events borrow subject_namespace for the repository and subject_name for the
		// job, which the relay fallback turned into an impossible kubectl invocation.
		{"repository namespace", "label-prs #32352281275", "nudgebee/nudgebee-enterprise", false},
		{"job name with space", "app-build #123", "nudgebee", false},
		{"uppercase namespace", "web", "Production", false},
		{"empty workload", "", "demo", false},
		{"empty namespace", "web", "", false},
		{"namespace over 63 chars", "web", string(make([]byte, 64)), false},
		{"shell metacharacter", "web; rm -rf /", "demo", false},
		{"leading dash", "-web", "demo", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isK8sLogTarget(tc.workload, tc.namespace))
		})
	}
}

func TestDecodeAgentLogPayload(t *testing.T) {
	// Verbatim logs_enricher payloads from stored evidences on dev: the agent gzips the
	// pod log and sends the base64 of it through Python's bytes repr.
	const crashLoopPayload = `b'H4sIAAAAAAAA/4TQzUrEMBTF8f08xXDWmTa3n+l9AMGNbly5GRITx2DaSJIWRObdpbgSHNxeDvx/3C8UPzswGtkMJ6lOJJ9o5I6YuqpXLVE/Ej1DILjNBTDuH+4eITDnCxghauuXy3Fbw+KSNj748nm0umijs4NAirGAUW861cGbeh+eZl1e3lyqrYHA+mF1ceCSVnc93PI0TE01UKvGbhrGm56/Hced6SwErDlvLmUfl308VFRNP1ez+lDOuvwuy4HbieW092I+v+rZB+8ymJRAifEdjF5VsqWMf+2dlE1P7e1f+lzc4pcLBLS1CQxWUklcD98BAAD//8CKKvOmAQAA'`
	// gzip of the empty string — the agent had nothing to send.
	const emptyPayload = `b'H4sIAAAAAAAA/wEAAP//AAAAAAAAAAA='`

	t.Run("decodes a real payload", func(t *testing.T) {
		got, err := playbooks.DecodeAgentPayload(crashLoopPayload)
		require.NoError(t, err)
		assert.Contains(t, got, "loading vulnerability database")

		entries := parseLogTextToOutputLogs(got)
		assert.Greater(t, len(entries), 1, "decoded payload should yield multiple entries")
		// Before the fix, the base64 blob itself was stored as the single log message.
		assert.NotContains(t, entries[0].Message, "H4sIAAAA")
	})

	t.Run("empty gzip yields no entries", func(t *testing.T) {
		got, err := playbooks.DecodeAgentPayload(emptyPayload)
		require.NoError(t, err)
		assert.Empty(t, parseLogTextToOutputLogs(got))
	})

	t.Run("plain text passes through unchanged", func(t *testing.T) {
		got, err := playbooks.DecodeAgentPayload("starting\nfatal: cannot connect to db")
		require.NoError(t, err)
		assert.Equal(t, "starting\nfatal: cannot connect to db", got)
	})

	t.Run("malformed wrapper is an error, not a fake log line", func(t *testing.T) {
		_, err := playbooks.DecodeAgentPayload("b'not-base64!!'")
		assert.Error(t, err)
	})
}

func TestParseLogTextToOutputLogsTimestamps(t *testing.T) {
	t.Run("kubectl --timestamps prefix is lifted off the message", func(t *testing.T) {
		entries := parseLogTextToOutputLogs(
			"2026-08-20T08:14:47.884081861Z 2026-08-20 08:14:47 - oteldemo.AdService - Targeted ad request received.")
		require.Len(t, entries, 1)
		assert.Equal(t, "2026-08-20T08:14:47.884081861Z", entries[0].Timestamp)
		assert.Equal(t, "2026-08-20 08:14:47 - oteldemo.AdService - Targeted ad request received.", entries[0].Message)
	})

	t.Run("structured log keeps its own timestamp", func(t *testing.T) {
		entries := parseLogTextToOutputLogs(
			`2026-08-20T09:11:01.045598546Z {"time":"2026-08-20T09:11:01.045427571Z","level":"INFO","msg":"api: async request completed"}`)
		require.Len(t, entries, 1)
		assert.Equal(t, "2026-08-20T09:11:01.045427571Z", entries[0].Timestamp)
		assert.Equal(t, "api: async request completed", entries[0].Message)
		assert.Equal(t, "INFO", entries[0].Severity)
	})

	t.Run("untimestamped line is left alone", func(t *testing.T) {
		entries := parseLogTextToOutputLogs("astronomy-db (34.118.225.146:5432) open")
		require.Len(t, entries, 1)
		assert.Empty(t, entries[0].Timestamp)
		assert.Equal(t, "astronomy-db (34.118.225.146:5432) open", entries[0].Message)
	})

	t.Run("timestamp-only line is dropped", func(t *testing.T) {
		assert.Empty(t, parseLogTextToOutputLogs("2026-08-20T08:14:47.884081861Z "))
	})
}

func TestFetchLogsViaKubectlRejectsUnknownKind(t *testing.T) {
	// kind originates in a webhook payload and is interpolated into the kubectl command.
	// The caller screens it; this asserts the command builder refuses on its own too, so
	// a future caller cannot reintroduce the injection path.
	action := &observabilityLogAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{
		SubjectName:      "web",
		SubjectNamespace: "demo",
	})

	// "pod" and "job" are allowlisted now (they route here for the time bound
	// logs_enricher cannot give), so the injection cases are what remain: a
	// shell metacharacter smuggled through an allowlisted prefix, an empty
	// kind, and a kind that is simply not in the set.
	for _, kind := range []string{"job; rm -rf /", "", "Secret", "configmap"} {
		t.Run(kind, func(t *testing.T) {
			resp, err := action.fetchLogsViaKubectl(ctx, kind, "web", "demo")
			require.Error(t, err)
			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), "does not support kind")
		})
	}
}

func TestKubectlLogKindsMatchesRelayRouting(t *testing.T) {
	// fetchLogsViaRelay routes to kubectl on exactly this set; anything else goes to
	// logs_enricher. Keeping one map means the two cannot drift apart.
	assert.Equal(t, map[string]bool{
		"deployment":  true,
		"daemonset":   true,
		"statefulset": true,
		"replicaset":  true,
		"pod":         true,
		"job":         true,
	}, kubectlLogKinds)
}

// scriptAgentLogActions stands a scripted agent in for the relay and returns the
// names of the actions it was asked to run, in order.
func scriptAgentLogActions(t *testing.T, answer func(action string) (map[string]any, error)) *[]string {
	t.Helper()
	actions := []string{}
	original := executeAgentLogActionFn
	t.Cleanup(func() { executeAgentLogActionFn = original })
	executeAgentLogActionFn = func(request relay.RelayExecuteRequest) (map[string]any, map[string]any, error) {
		actions = append(actions, request.Body.ActionName)
		response, err := answer(request.Body.ActionName)
		return response, map[string]any{}, err
	}
	return &actions
}

// kubectlAnswer is the shape kubectl_command_executor answers in: the command's
// output as a JSON string inside a JsonBlock.
func kubectlAnswer(stdout, stderr string, exitCode int) map[string]any {
	inner, _ := json.Marshal(map[string]any{"command": "kubectl logs", "stdout": stdout, "stderr": stderr, "exit_code": exitCode})
	return map[string]any{"type": "json", "data": string(inner)}
}

func deploymentLogContext() playbooks.PlaybookActionContext {
	return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{
		SubjectName:      "web",
		SubjectNamespace: "shop",
		SubjectType:      "deployment",
	})
}

// A container that never started makes `kubectl logs` exit non-zero, but that is
// the cluster answering. logs_enricher would ask the same kubelet the same thing.
func TestFetchLogsViaRelay_NoLogsFromTheClusterStopsAtKubectl(t *testing.T) {
	actions := scriptAgentLogActions(t, func(string) (map[string]any, error) {
		return kubectlAnswer("",
			`Error from server (BadRequest): container "web" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: CreateContainerConfigError`+"\n", 1), nil
	})

	response, err := (&observabilityLogAction{}).fetchLogsViaRelay(deploymentLogContext(), "web", "shop")

	assert.NoError(t, err)
	assert.Nil(t, response)
	assert.Equal(t, []string{"kubectl_command_executor"}, *actions)
}

// Any other kubectl failure keeps its second route, and so does a refusal that
// came after output: with --all-containers a waiting sidecar fails the command
// once the other containers have written theirs.
func TestFetchLogsViaRelay_KubectlFailureStillFallsBack(t *testing.T) {
	cases := map[string]map[string]any{
		"kubectl timed out": kubectlAnswer("", "error: timed out waiting for the condition\n", 1),
		"a waiting sidecar after the others wrote theirs": kubectlAnswer(
			"2026-08-20T08:14:47.884081861Z started\n",
			`Error from server (BadRequest): container "proxy" in pod "web-7d4b9c5f6-x2x7q" is waiting to start: PodInitializing`+"\n", 1),
	}
	for name, kubectl := range cases {
		t.Run(name, func(t *testing.T) {
			actions := scriptAgentLogActions(t, func(action string) (map[string]any, error) {
				if action == "kubectl_command_executor" {
					return kubectl, nil
				}
				return nil, fmt.Errorf("relay: unable to execute relay query -  %v", "logs_enricher: no pods found for workload")
			})

			response, err := (&observabilityLogAction{}).fetchLogsViaRelay(deploymentLogContext(), "web", "shop")

			// The agent finding no pods is an answer too, so the chain ends without
			// an error rather than with the second route's.
			assert.NoError(t, err)
			assert.Nil(t, response)
			assert.Equal(t, []string{"kubectl_command_executor", "logs_enricher"}, *actions)
		})
	}
}

func TestFetchLogsViaLogsEnricher_FailedReadIsStillAnError(t *testing.T) {
	scriptAgentLogActions(t, func(string) (map[string]any, error) {
		return nil, errors.New("unable to consume relay API")
	})

	response, err := (&observabilityLogAction{}).fetchLogsViaLogsEnricher(deploymentLogContext(), "web", "shop")

	assert.Error(t, err)
	assert.Nil(t, response)
}
