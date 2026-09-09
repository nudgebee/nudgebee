package observability

import (
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"

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

	for _, kind := range []string{"pod", "job; rm -rf /", "", "Secret"} {
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
	}, kubectlLogKinds)
}
