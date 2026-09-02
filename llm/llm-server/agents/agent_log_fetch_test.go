package agents

import (
	"encoding/json"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestBuildLogIntentMessages_SystemPromptIsStable is the regression guard for
// the prompt-cacheability refactor: the System-role message in a log-intent
// LLM call must be byte-identical regardless of per-call inputs (OriginalQuery,
// ConversationContext, Query). If a future change splices dynamic content
// back into the system message, the upstream provider's prompt cache hit rate
// silently collapses — this test catches that at unit-test time.
func TestBuildLogIntentMessages_SystemPromptIsStable(t *testing.T) {
	const sysPrompt = "Static system prompt. Extract log retrieval parameters."

	cases := []struct {
		name    string
		request core.NBAgentRequest
	}{
		{"minimal", core.NBAgentRequest{Query: "show me logs"}},
		{"with original query", core.NBAgentRequest{
			Query:         "get logs for pod X",
			OriginalQuery: "Why is pod X crashing?",
		}},
		{"with context", core.NBAgentRequest{
			Query:               "errors in last hour",
			ConversationContext: "User is debugging an outage at 14:30 UTC.",
		}},
		{"with both", core.NBAgentRequest{
			Query:               "get logs for pod Y",
			OriginalQuery:       "Was pod Y affected by the incident?",
			ConversationContext: "Incident ticket NB-12345 opened 14:30.",
		}},
		{"redundant original query (matches Query)", core.NBAgentRequest{
			Query:         "same string",
			OriginalQuery: "same string",
		}},
	}

	var baseline llms.MessageContent
	for i, tc := range cases {
		msgs := buildLogIntentMessages(sysPrompt, tc.request)

		// Exactly two messages: one system, one human. Dynamic content must
		// not leak into additional system messages.
		assert.Len(t, msgs, 2, "case %q: expected 2 messages (1 system + 1 human)", tc.name)
		assert.Equal(t, llms.ChatMessageTypeSystem, msgs[0].Role, "case %q: first message must be system", tc.name)
		assert.Equal(t, llms.ChatMessageTypeHuman, msgs[1].Role, "case %q: second message must be human", tc.name)

		// The system message must be byte-identical across every case —
		// that's the entire point of the refactor. Compare against the first
		// case as baseline.
		if i == 0 {
			baseline = msgs[0]
			continue
		}
		assert.Equal(t, baseline, msgs[0], "case %q: system message diverged from baseline (cache-breaking)", tc.name)
	}
}

func TestMergeRefs(t *testing.T) {
	uiRef := toolcore.NBToolResponseReference{Text: "Query Logs", Url: "https://app/kubernetes/details/acc#monitoring/logs", Type: "url"}
	fileRef := toolcore.NBToolResponseReference{Text: "logs_loki_1.txt", Url: "logs_loki_1.txt", Type: "file"}
	dupURL := toolcore.NBToolResponseReference{Text: "dup", Url: uiRef.Url, Type: "url"}
	noURL := toolcore.NBToolResponseReference{Text: "blank", Url: "", Type: "url"}

	cases := []struct {
		name     string
		toolRefs []toolcore.NBToolResponseReference
		fileRefs []toolcore.NBToolResponseReference
		wantURLs []string
	}{
		{"both nil", nil, nil, nil},
		{"only tool ref", []toolcore.NBToolResponseReference{uiRef}, nil, []string{uiRef.Url}},
		{"only file ref", nil, []toolcore.NBToolResponseReference{fileRef}, []string{fileRef.Url}},
		{"tool first then file", []toolcore.NBToolResponseReference{uiRef}, []toolcore.NBToolResponseReference{fileRef}, []string{uiRef.Url, fileRef.Url}},
		{"dedupe by url", []toolcore.NBToolResponseReference{uiRef}, []toolcore.NBToolResponseReference{dupURL}, []string{uiRef.Url}},
		{"skip empty url", []toolcore.NBToolResponseReference{noURL}, []toolcore.NBToolResponseReference{fileRef}, []string{fileRef.Url}},
		{"all empty url -> nil", []toolcore.NBToolResponseReference{noURL}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRefs(tc.toolRefs, tc.fileRefs)
			if tc.wantURLs == nil {
				assert.Nil(t, got)
				return
			}
			urls := make([]string, len(got))
			for i, r := range got {
				urls[i] = r.Url
			}
			assert.Equal(t, tc.wantURLs, urls)
		})
	}
}

// TestProviderQueryExamples_Coverage guards every backend's few-shot examples
// against drift: each provider has examples, uses its canonical labels, and
// doesn't leak labels from another provider (the #28517 failure mode).
func TestProviderQueryExamples_Coverage(t *testing.T) {
	cases := []struct {
		provider     string
		wantNonEmpty bool
		// forbiddenInAnswer: labels that must NOT appear in any Answer JSON.
		// They may still appear in Explanations (which teach what NOT to use).
		forbiddenInAnswer []string
		// requireSomeAnswer: canonical labels that must appear in at least
		// one Answer JSON.
		requireSomeAnswer []string
	}{
		{
			provider:          "loki",
			wantNonEmpty:      true,
			forbiddenInAnswer: []string{"service_name"}, // #28517: not a real Loki label
			requireSomeAnswer: []string{"app", "namespace", "container"},
		},
		{
			provider:     "signoz",
			wantNonEmpty: true,
			// Signoz uses dotted OTel-style names; pod_name is canonical, NOT host.name.
			forbiddenInAnswer: []string{},
			requireSomeAnswer: []string{"service.name", "pod_name", "service.namespace"},
		},
		{
			provider:     "es",
			wantNonEmpty: true,
			// ES uses kubernetes.*.keyword — bare k8s_pod_name (Loki-style)
			// or pod_name (Signoz/Datadog) would be wrong.
			forbiddenInAnswer: []string{"\"pod_name\":", "\"app\":"},
			requireSomeAnswer: []string{"kubernetes.pod_name.keyword", "kubernetes.namespace_name.keyword"},
		},
		{
			provider:          "elasticsearch", // alias of "es"
			wantNonEmpty:      true,
			forbiddenInAnswer: []string{"\"pod_name\":", "\"app\":"},
			requireSomeAnswer: []string{"kubernetes.pod_name.keyword", "kubernetes.namespace_name.keyword"},
		},
		{
			provider:     "unknown_backend",
			wantNonEmpty: false, // unknown providers must return empty, not fall back to a wrong-backend default
		},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			examples := providerSpecificQueryExamples(tc.provider)
			if !tc.wantNonEmpty {
				assert.Empty(t, examples,
					"provider %q must return no examples — unknown backends must NOT silently fall back to another provider's examples",
					tc.provider)
				return
			}
			assert.NotEmpty(t, examples, "provider %q must have at least one example", tc.provider)

			combinedAnswers := ""
			for _, ex := range examples {
				combinedAnswers += ex.Answer + "\n"
				for _, forbidden := range tc.forbiddenInAnswer {
					assert.NotContains(t, ex.Answer, forbidden,
						"provider %q example uses forbidden label %q in Answer (cross-provider leakage). Question: %q",
						tc.provider, forbidden, ex.Question)
				}
			}
			for _, must := range tc.requireSomeAnswer {
				assert.Contains(t, combinedAnswers, must,
					"provider %q has no example demonstrating canonical label %q — LLM has no schema reference for it",
					tc.provider, must)
			}
		})
	}
}

// TestLogFetch_DefaultExamplesPresent — the unknown-provider fallback set
// must stay non-empty; an empty list leaves the LLM with no schema reference.
func TestLogFetch_DefaultExamplesPresent(t *testing.T) {
	defaults := defaultQueryExamples()
	assert.NotEmpty(t, defaults, "default examples must exist for the no-provider-fields fallback path")
	for _, ex := range defaults {
		assert.NotEmpty(t, ex.Answer, "every default example must have an Answer")
		assert.True(t, strings.Contains(ex.Answer, "{") && strings.Contains(ex.Answer, "}"),
			"default example Answer must be a JSON object: %q", ex.Answer)
	}
}

// TestBuildKubectlLogCommand covers the pod/ prefix heuristic, container /
// previous flags, default tail, and the filter_pattern → grep+head pipeline.
func TestBuildKubectlLogCommand(t *testing.T) {
	cases := []struct {
		name     string
		intent   kubectlLogQuery
		contains []string
	}{
		{
			name: "deployment-managed pod gets pod/ prefix",
			intent: kubectlLogQuery{
				ResourceName: "cron-scheduler-7d8b598678-v6nhb",
				Namespace:    "prod",
				Tail:         100,
			},
			contains: []string{"pod/cron-scheduler-7d8b598678-v6nhb", "-n prod", "--tail=100"},
		},
		{
			name: "statefulset pod stays unprefixed",
			intent: kubectlLogQuery{
				ResourceName: "kafka-0",
				Namespace:    "data",
				Tail:         50,
			},
			contains: []string{"kafka-0", "-n data", "--tail=50"},
		},
		{
			name: "investigation tail with filter pattern triggers grep + head",
			intent: kubectlLogQuery{
				ResourceName:  "task-scheduler-6fb6dd6b8c-5h5pq",
				Namespace:     "namespace-73a",
				Tail:          10000,
				FilterPattern: "(error|exception|fail|fatal)",
			},
			contains: []string{"--tail=10000", "| grep -i -E '(error|exception|fail|fatal)'", "| head -200"},
		},
		{
			name: "previous flag passes through",
			intent: kubectlLogQuery{
				ResourceName: "web-0",
				Namespace:    "default",
				IsPrevious:   true,
				Tail:         200,
			},
			contains: []string{"--previous", "--tail=200"},
		},
		{
			name: "container flag passes through",
			intent: kubectlLogQuery{
				ResourceName: "api-server-7f8b9c-x2k",
				Namespace:    "prod",
				Container:    "sidecar",
				Tail:         100,
			},
			contains: []string{"-c sidecar"},
		},
		{
			name: "deployment resource type qualifies as deployment/",
			intent: kubectlLogQuery{
				ResourceName: "api-service",
				ResourceType: "deployment",
				Namespace:    "prod",
				Tail:         100,
			},
			contains: []string{"deployment/api-service"},
		},
		{
			name: "missing tail defaults to 100",
			intent: kubectlLogQuery{
				ResourceName: "web-0",
				Namespace:    "default",
				Tail:         0,
			},
			contains: []string{"--tail=100"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildKubectlLogCommand(tc.intent)
			for _, sub := range tc.contains {
				assert.Contains(t, got, sub, "command: %s", got)
			}
		})
	}
}

// TestLooksLikePodName: false positives on StatefulSet/DaemonSet/Job pods
// would break their log retrieval (wrong pod/ prefix). False negatives on
// Deployment pods are merely sub-optimal — kubectl resolves them either way.
func TestLooksLikePodName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		// Deployment-managed pods: must match.
		{"cron-scheduler-7d8b598678-v6nhb", true},
		{"api-service-6dfcbb87-d5c9z", true},
		{"my-app-51-549c7cfdbf-mlfm6", true},

		// StatefulSet pods: must NOT match (false positive would break logs
		// for kafka, postgres, redis HA setups).
		{"web-0", false},
		{"kafka-2", false},
		{"mysql-0", false},

		// DaemonSet pods (single hash segment): must NOT match.
		{"fluentd-k8sms", false},
		{"node-exporter-x9k", false},

		// Job pods (single hash segment): must NOT match.
		{"migration-x7q9k", false},

		// Bare/raw pods (no suffix): must NOT match.
		{"nginx", false},
		{"my-app", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, looksLikePodName(tt.name), "looksLikePodName(%q)", tt.name)
		})
	}
}

// TestUnwrapLokiInnerTimestamps — Loki responses carry two timestamps per
// record (outer = ingest time, often clustered; inner = real log time inside
// `message`). The unwrap must surface the inner timestamp so downstream
// synthesis can cite real time-window anomalies without parsing escaped JSON.
// Best-effort: any non-Loki / non-JSON / missing-inner-timestamp shape must
// return unchanged so we don't corrupt non-Loki responses.
func TestUnwrapLokiInnerTimestamps(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	cases := []struct {
		name          string
		in            string
		wantSubstr    []string
		wantUnchanged bool
	}{
		{
			name: "loki shape — inner timestamp surfaces",
			in:   `{"logs":[{"timestamp":"2026-05-05T15:49:52Z","message":"{\"timestamp\":\"2026-05-05T03:05:53Z\",\"level\":\"ERROR\"}","labels":{"app":"x"}}]}`,
			wantSubstr: []string{
				`"timestamp":"2026-05-05T03:05:53Z"`, // inner replaced outer
			},
		},
		{
			name: "loki shape — multiple entries, all unwrapped",
			in:   `{"logs":[{"timestamp":"2026-05-05T15:49:52Z","message":"{\"timestamp\":\"2026-05-05T03:00:01Z\"}"},{"timestamp":"2026-05-05T15:49:52Z","message":"{\"timestamp\":\"2026-05-05T03:05:30Z\"}"}]}`,
			wantSubstr: []string{
				`"timestamp":"2026-05-05T03:00:01Z"`,
				`"timestamp":"2026-05-05T03:05:30Z"`,
			},
		},
		{
			name:          "non-JSON message — entry left unchanged",
			in:            `{"logs":[{"timestamp":"2026-05-05T15:49:52Z","message":"plain text log line, not JSON"}]}`,
			wantUnchanged: true,
		},
		{
			name:          "JSON message but no inner timestamp — unchanged",
			in:            `{"logs":[{"timestamp":"2026-05-05T15:49:52Z","message":"{\"level\":\"INFO\",\"msg\":\"started\"}"}]}`,
			wantUnchanged: true,
		},
		{
			name:          "non-Loki shape — unchanged",
			in:            `{"stdout":"some kubectl output\n"}`,
			wantUnchanged: true,
		},
		{
			name:          "empty input — unchanged",
			in:            "",
			wantUnchanged: true,
		},
		{
			name:          "malformed JSON — unchanged",
			in:            `{"logs": [{`,
			wantUnchanged: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unwrapLokiInnerTimestamps(ctx, tc.in)
			if tc.wantUnchanged {
				assert.Equal(t, tc.in, got, "expected input returned unchanged")
				return
			}
			for _, sub := range tc.wantSubstr {
				assert.Contains(t, got, sub)
			}
		})
	}
}

// TestEscapeShellSingleQuoted — user-supplied filter_patterns must not break
// out of the shell-quoted grep argument.
func TestEscapeShellSingleQuoted(t *testing.T) {
	cases := map[string]string{
		"plain":         "plain",
		"with'quote":    `with'\''quote`,
		"two''quotes":   `two'\'''\''quotes`,
		"trailing'":     `trailing'\''`,
		"":              "",
		"no-quotes-yet": "no-quotes-yet",
	}
	for in, want := range cases {
		assert.Equal(t, want, escapeShellSingleQuoted(in), "input: %q", in)
	}
}

// TestFlattenLogsToJSONL pins the JSONL conversion: a Loki-shaped envelope
// becomes one entry per line in `<timestamp>\t<message>` form so grep can
// localise matches. Non-envelope inputs (kubectl text, placeholders, malformed
// JSON) pass through verbatim. This is the contract `agent_log.go`'s prompt
// relies on when it tells the LLM to run `grep ... | head -20`.
func TestFlattenLogsToJSONL(t *testing.T) {
	t.Run("loki envelope becomes JSONL", func(t *testing.T) {
		input := `{"logs":[` +
			`{"labels":{"app":"x"},"timestamp":"2026-05-06T03:00:10Z","message":"{\"level\":\"ERROR\",\"error\":\"ConnectionError\"}"},` +
			`{"labels":{"app":"x"},"timestamp":"2026-05-06T03:00:20Z","message":"{\"level\":\"INFO\",\"message\":\"Job ok\"}"}` +
			`]}`
		got := flattenLogsToJSONL(input)
		lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		assert.Len(t, lines, 2, "expected two JSONL lines, got %d: %q", len(lines), got)
		assert.Contains(t, lines[0], "2026-05-06T03:00:10Z")
		assert.Contains(t, lines[0], "ERROR")
		assert.Contains(t, lines[0], "ConnectionError")
		assert.Contains(t, lines[1], "2026-05-06T03:00:20Z")
		// each line must end with newline (so head -20 means 20 entries)
		assert.True(t, strings.HasSuffix(got, "\n"))
	})

	t.Run("kubectl text passthrough", func(t *testing.T) {
		input := "2026-05-06T03:00:10Z stderr F ERROR Cannot connect to upstream\n2026-05-06T03:00:20Z stdout F INFO heartbeat ok\n"
		got := flattenLogsToJSONL(input)
		assert.Equal(t, input, got, "kubectl text logs must pass through unchanged")
	})

	t.Run("no-logs-found placeholder passthrough", func(t *testing.T) {
		input := `No logs found for Loki query: {namespace="ns",pod="p"} (time range: ...). The query executed successfully but returned no results.`
		got := flattenLogsToJSONL(input)
		assert.Equal(t, input, got)
	})

	t.Run("envelope with zero entries passthrough", func(t *testing.T) {
		input := `{"logs":[]}`
		got := flattenLogsToJSONL(input)
		assert.Equal(t, input, got, "empty envelope should not be flattened")
	})

	t.Run("malformed JSON passthrough", func(t *testing.T) {
		input := `{"logs": broken`
		got := flattenLogsToJSONL(input)
		assert.Equal(t, input, got)
	})

	t.Run("empty input passthrough", func(t *testing.T) {
		assert.Equal(t, "", flattenLogsToJSONL(""))
		assert.Equal(t, "   ", flattenLogsToJSONL("   "))
	})

	t.Run("entry without timestamp still emits message", func(t *testing.T) {
		input := `{"logs":[{"message":"just a message"}]}`
		got := flattenLogsToJSONL(input)
		assert.Equal(t, "just a message\n", got)
	})

	t.Run("grep shape — head -N means N entries", func(t *testing.T) {
		// Build an envelope with 50 entries; verify the result has exactly
		// 50 newline-terminated lines so `head -N` semantics work.
		var sb strings.Builder
		sb.WriteString(`{"logs":[`)
		for i := 0; i < 50; i++ {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`{"timestamp":"2026-05-06T00:00:00Z","message":"line"}`)
		}
		sb.WriteString(`]}`)
		got := flattenLogsToJSONL(sb.String())
		assert.Equal(t, 50, strings.Count(got, "\n"))
	})
}

// TestLooksLikeFetchError pins the four detection layers — kubectl
// substrings, JSON-envelope error/status fields, kubectl_execute wrapper
// (stdout/stderr recursion), and the relay's "Server returned <code>:"
// wrapper. Catches the regression where every kubectl call returned a
// relay-emitted 500 envelope and the previous detector missed it.
func TestLooksLikeFetchError(t *testing.T) {
	cases := []struct {
		name               string
		provider           string
		body               string
		wantMatched        bool
		wantReasonContains string
	}{
		// Branch 1 — substring scan on raw text
		{
			name:               "kubectl Forbidden raw",
			provider:           "kubectl",
			body:               "Error from server (Forbidden): pods is forbidden",
			wantMatched:        true,
			wantReasonContains: "Forbidden",
		},
		{
			name:               "kubectl NotFound raw",
			provider:           "kubectl",
			body:               "Error from server (NotFound): pods \"x\" not found",
			wantMatched:        true,
			wantReasonContains: "NotFound",
		},
		{
			name:               "kubectl x509 cert error raw",
			provider:           "kubectl",
			body:               "Unable to connect to the server: x509: certificate signed by unknown authority",
			wantMatched:        true,
			wantReasonContains: "x509",
		},

		// Branch 1 — relay wrapper regex
		{
			name:               "relay 500 wrapper raw",
			provider:           "kubectl",
			body:               `Error: Server returned 500: {"error":"findings field not found"}`,
			wantMatched:        true,
			wantReasonContains: "Server returned 500",
		},
		{
			name:               "relay 502 wrapper raw",
			provider:           "kubectl",
			body:               "Error: Server returned 502: bad gateway",
			wantMatched:        true,
			wantReasonContains: "Server returned 502",
		},
		{
			name:               "relay 400 wrapper raw",
			provider:           "kubectl",
			body:               "Server returned 400: status: 400 Bad Request",
			wantMatched:        true,
			wantReasonContains: "Server returned 400",
		},

		// Branch 2 — Loki/Signoz/ES JSON envelope
		{
			name:               "loki error envelope",
			provider:           "loki",
			body:               `{"error":"too many outstanding requests","data":null}`,
			wantMatched:        true,
			wantReasonContains: "too many outstanding",
		},
		{
			name:               "loki status:error",
			provider:           "loki",
			body:               `{"status":"error","errorType":"bad_data","error":"parse error at line 1"}`,
			wantMatched:        true,
			wantReasonContains: "parse error",
		},

		// Branch 3 — kubectl_execute wrapper recursion (under length cap)
		{
			name:               "kubectl_execute wraps Forbidden in stdout",
			provider:           "kubectl",
			body:               `{"stdout":"Error from server (Forbidden): pods is forbidden","stderr":""}`,
			wantMatched:        true,
			wantReasonContains: "Forbidden",
		},
		{
			name:               "kubectl_execute wraps connection refused in stderr",
			provider:           "kubectl",
			body:               `{"stdout":"","stderr":"The connection to the server localhost:6443 was refused"}`,
			wantMatched:        true,
			wantReasonContains: "refused",
		},
		{
			name:               "todays regression — relay 500 wrapper inside kubectl_execute stdout",
			provider:           "kubectl",
			body:               `{"stdout":"Error: Server returned 500: {\"error\":\"findings field not found or is nil from data\",\"result\":null}\n","stderr":""}`,
			wantMatched:        true,
			wantReasonContains: "Server returned 500",
		},

		// Branch 3 — length cap protects real log content
		{
			name:        "long log dump containing forbidden as content (over cap)",
			provider:    "kubectl",
			body:        `{"stdout":"` + strings.Repeat("INFO: forbidden access attempt by user X\n", 20) + `","stderr":""}`,
			wantMatched: false,
		},
		{
			name:               "short stdout that contains forbidden (under cap)",
			provider:           "kubectl",
			body:               `{"stdout":"INFO: forbidden access attempt","stderr":""}`,
			wantMatched:        true,
			wantReasonContains: "forbidden",
		},

		// Negatives
		{
			name:        "empty input",
			provider:    "kubectl",
			body:        "",
			wantMatched: false,
		},
		{
			name:        "valid kubectl get pods output",
			provider:    "kubectl",
			body:        `{"stdout":"NAME   READY   STATUS    RESTARTS   AGE\nweb-1  1/1     Running   0          3m","stderr":""}`,
			wantMatched: false,
		},
		{
			name:        "valid loki response",
			provider:    "loki",
			body:        `{"logs":[{"timestamp":"2026-01-01T00:00:00Z","message":"hello"}],"status":"success"}`,
			wantMatched: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, reason := looksLikeFetchError(tc.provider, tc.body)
			assert.Equal(t, tc.wantMatched, matched,
				"looksLikeFetchError(%q, ...) matched=%v; want %v; reason=%q",
				tc.provider, matched, tc.wantMatched, reason)
			if tc.wantMatched && tc.wantReasonContains != "" {
				assert.Contains(t, reason, tc.wantReasonContains,
					"reason should contain %q for case %s", tc.wantReasonContains, tc.name)
			}
		})
	}
}

// TestMakeFetchResponse_PreviewsLogsWhenFileRefPresent guards the fix that
// stopped inlining the full logs alongside the file_ref, plus the two follow-ups
// added after three sessions showed the agent spending a turn on `head -n 20
// <file_ref>` before it could filter:
//
//   - the preview is now of the SAME representation written to the file
//     (flattenLogsToJSONL), so a filter can be written straight from it;
//   - `complete` states outright whether file_ref holds anything extra, rather
//     than leaving the model to infer it from an absent truncation marker.
func TestMakeFetchResponse_PreviewsLogsWhenFileRefPresent(t *testing.T) {
	bigLogs := strings.Repeat("x", logInlinePreviewBytes*3)

	decode := func(resp core.NBAgentResponse) map[string]any {
		assert.Len(t, resp.Response, 1)
		var env map[string]any
		assert.NoError(t, json.Unmarshal([]byte(resp.Response[0]), &env))
		return env
	}
	str := func(env map[string]any, k string) string {
		v, _ := env[k].(string)
		return v
	}

	t.Run("file_ref present: logs previewed, not inlined in full", func(t *testing.T) {
		env := decode(makeFetchResponse("fetch_logs", "q", bigLogs, flattenLogsToJSONL(bigLogs), "logs_loki_1.txt", "", nil))
		assert.Equal(t, "logs_loki_1.txt", str(env, "file_ref"))
		assert.Less(t, len(str(env, "logs")), len(bigLogs),
			"full logs must not be inlined when a file_ref exists")
		assert.Contains(t, str(env, "logs"), "file_ref")
		assert.Contains(t, str(env, "logs"), "shell_execute")
		assert.Equal(t, false, env["logs_complete"],
			"a truncated payload must report logs_complete=false so the agent knows file_ref has more")
	})

	t.Run("no file_ref: full logs inlined", func(t *testing.T) {
		env := decode(makeFetchResponse("fetch_logs", "q", bigLogs, "", "", "", nil))
		assert.Equal(t, "", str(env, "file_ref"))
		assert.Equal(t, bigLogs, str(env, "logs"),
			"with no file_ref the full logs must remain available inline")
		assert.Equal(t, true, env["logs_complete"])
	})

	t.Run("file_ref present but logs already small: inlined unchanged and complete", func(t *testing.T) {
		const small = "tiny log line"
		env := decode(makeFetchResponse("fetch_logs", "q", small, flattenLogsToJSONL(small), "logs_loki_2.txt", "", nil))
		assert.True(t, strings.HasPrefix(str(env, "logs"), small),
			"the log content itself must be inlined unchanged")
		assert.Contains(t, str(env, "logs"), "complete —",
			"a complete payload is marked in-band, mirroring the truncation marker")
		assert.Equal(t, true, env["logs_complete"],
			"everything is inline, so shelling out to read file_ref would be pure cost")
	})

	t.Run("preview format matches the file format, not the raw backend payload", func(t *testing.T) {
		// The saved file is flattenLogsToJSONL(logs): "<timestamp>\t<message>"
		// per entry. Previewing the raw Loki envelope instead made it impossible
		// to write a working grep from the preview, which is what forced the
		// extra `head` turn. Both must now be byte-identical for a payload that
		// fits inline.
		raw := `{"logs":[{"timestamp":"2026-08-12T10:00:00Z","message":"{\"level\":\"ERROR\",\"msg\":\"boom\"}"}]}`
		env := decode(makeFetchResponse("fetch_logs", "q", raw, flattenLogsToJSONL(raw), "logs_loki_4.txt", "", nil))
		assert.True(t, strings.HasPrefix(str(env, "logs"), flattenLogsToJSONL(raw)),
			"the inline preview must be the same representation that was written to file_ref")
		assert.NotContains(t, str(env, "logs"), `{"logs":[`,
			"the raw backend envelope must not leak into the preview")
	})

	t.Run("bundle_signal present: threaded through envelope verbatim", func(t *testing.T) {
		const sig = "=== CRASH BUNDLE ===\n[error] 3 hits\n[oom] 1 hit"
		env := decode(makeFetchResponse("fetch_logs", "q", "tiny", flattenLogsToJSONL("tiny"), "logs_loki_3.txt", sig, nil))
		assert.Equal(t, sig, str(env, "bundle_signal"),
			"bundle_signal must appear verbatim in the envelope so the LLM can read it without a follow-up tool call")
	})
}

// TestBuildLogIntentMessages_AccountPromptPropagation pins the contract for
// account GlobalContext propagation into the query-generator call:
//   - present  → appended to the SYSTEM message (account-stable, cache-safe)
//   - absent   → system message is exactly the raw prompt (no empty block)
//   - stable   → same AccountPrompt across calls yields byte-identical system
//     messages, preserving the provider prompt-cache prefix
//   - oversized → truncated via TruncateMiddle so a huge curated context
//     cannot bloat every intent call
func TestBuildLogIntentMessages_AccountPromptPropagation_DuplicateGuard(t *testing.T) {
	const sysPrompt = "Static system prompt. Extract log retrieval parameters."
	const gc = "Log fields for this deployment: service.name, body, k8s.pod.name."

	textOf := func(m llms.MessageContent) string {
		var b strings.Builder
		for _, p := range m.Parts {
			if tp, ok := p.(llms.TextContent); ok {
				b.WriteString(tp.Text)
			}
		}
		return b.String()
	}

	t.Run("account prompt lands in system message only", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for svc", AccountPrompt: gc})
		assert.Len(t, msgs, 2)
		sys, human := textOf(msgs[0]), textOf(msgs[1])
		assert.Contains(t, sys, gc)
		assert.Contains(t, sys, "Account preferences")
		assert.NotContains(t, human, gc)
	})

	t.Run("no account prompt leaves system prompt untouched", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for svc"})
		assert.Equal(t, sysPrompt, textOf(msgs[0]))
	})

	t.Run("same account prompt is byte-stable across calls", func(t *testing.T) {
		a := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for svc A", AccountPrompt: gc})
		b := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "errors in svc B last 1h", OriginalQuery: "why is B failing?", AccountPrompt: gc})
		assert.Equal(t, textOf(a[0]), textOf(b[0]), "system message must not vary with per-call inputs")
	})

	t.Run("oversized account prompt is truncated", func(t *testing.T) {
		huge := strings.Repeat("x", defaultCustomAgentAccountPromptBytes+50_000)
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs", AccountPrompt: huge})
		sys := textOf(msgs[0])
		assert.Less(t, len(sys), len(sysPrompt)+defaultCustomAgentAccountPromptBytes+1024)
	})
}

// TestDefaultProviderLogFields pins the fallback field vocabulary advertised
// to the query-generator when backend label discovery fails: Signoz stores
// logs under OTel attribute names — the generic `_body`/`namespace`/`pod` set
// matches no Signoz attribute and every query silently returns zero rows.
func TestDefaultProviderLogFields_DuplicateGuard(t *testing.T) {
	signozFields := defaultProviderLogFields("signoz")
	assert.ElementsMatch(t,
		[]string{"body", "service.name", "k8s.cluster.name", "k8s.namespace.name", "k8s.pod.name", "trace_id"},
		signozFields)
	assert.Equal(t, signozFields, defaultProviderLogFields("SigNoz"), "provider match must be case-insensitive")
	assert.Equal(t, []string{"_body", "namespace", "pod"}, defaultProviderLogFields("loki"))
	assert.Equal(t, []string{"_body", "namespace", "pod"}, defaultProviderLogFields(""))
}

// TestEffectiveProvider guards the per-request log_provider_override: it must
// outrank whatever provider the agent resolved at construction, "k8s" must mean
// "no services-server backend" (empty provider → kubectl path), and — critically
// — it must NOT be written back onto the agent. fetch_logs instances are shared
// across requests via the 30-minute tool-list caches, so a mutation would pin the
// backend for every other request on the account.
func TestEffectiveProvider(t *testing.T) {
	cases := []struct {
		name     string
		resolved string
		override string
		want     string
	}{
		{"no override keeps resolved provider", "loki", "", "loki"},
		{"k8s override routes to kubectl", "loki", "k8s", ""},
		{"k8s override is case/space tolerant", "loki", " K8S ", ""},
		{"override pins a backend", "", "loki", "loki"},
		{"override case is preserved for api-server", "loki", "ES", "ES"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &FetchLogsAgent{
				accountId: "acc-1",
				provider:  services_server.ObservabilityProvider{Provider: tc.resolved},
			}
			got := a.effectiveProvider(core.NBAgentRequest{
				QueryConfig: toolcore.NBQueryConfig{LogProviderOverride: tc.override},
			})
			assert.Equal(t, tc.want, got.Provider)
			// The agent itself must be untouched — this is the cross-request leak guard.
			assert.Equal(t, tc.resolved, a.provider.Provider, "effectiveProvider must not mutate the shared agent")
		})
	}
}

// TestBuildLogIntentMessages_AccountPromptPropagation pins the contract for
// account GlobalContext propagation into the query-generator call:
//   - present  → appended to the SYSTEM message (account-stable, cache-safe)
//   - absent   → system message is exactly the raw prompt (no empty block)
//   - stable   → same AccountPrompt across calls yields byte-identical system
//     messages, preserving the provider prompt-cache prefix
//   - oversized → truncated via TruncateMiddle so a huge curated context
//     cannot bloat every intent call
func TestBuildLogIntentMessages_AccountPromptPropagation(t *testing.T) {
	const sysPrompt = "Static system prompt. Extract log retrieval parameters."
	const gc = "Log fields for this deployment: service.name, body, k8s.pod.name."

	textOf := func(m llms.MessageContent) string {
		var b strings.Builder
		for _, p := range m.Parts {
			if tp, ok := p.(llms.TextContent); ok {
				b.WriteString(tp.Text)
			}
		}
		return b.String()
	}

	t.Run("account prompt lands in system message only", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for svc", AccountPrompt: gc})
		assert.Len(t, msgs, 2)
		sys, human := textOf(msgs[0]), textOf(msgs[1])
		assert.Contains(t, sys, gc)
		assert.Contains(t, sys, "Account preferences")
		assert.NotContains(t, human, gc)
	})

	t.Run("no account prompt leaves system prompt untouched", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for svc"})
		assert.Equal(t, sysPrompt, textOf(msgs[0]))
	})

	t.Run("same account prompt is byte-stable across calls", func(t *testing.T) {
		a := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for svc A", AccountPrompt: gc})
		b := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "errors in svc B last 1h", OriginalQuery: "why is B failing?", AccountPrompt: gc})
		assert.Equal(t, textOf(a[0]), textOf(b[0]), "system message must not vary with per-call inputs")
	})

	t.Run("oversized account prompt is truncated", func(t *testing.T) {
		huge := strings.Repeat("x", defaultCustomAgentAccountPromptBytes+50_000)
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs", AccountPrompt: huge})
		sys := textOf(msgs[0])
		assert.Less(t, len(sys), len(sysPrompt)+defaultCustomAgentAccountPromptBytes+1024)
	})
}

func TestBuildLogIntentMessages_DomainKnowledgePropagation(t *testing.T) {
	const sysPrompt = "Static system prompt. Extract log retrieval parameters."
	const kbBlock = "<retrieved_knowledge>\nFor service payments, search in index custom-app-logs-* with field k8s_container_name.\n</retrieved_knowledge>"
	const skillsCtx = "<skill>\n<name>log_custom_conventions</name>\n<content>Use index app-logs-*</content>\n</skill>"

	textOf := func(m llms.MessageContent) string {
		var b strings.Builder
		for _, p := range m.Parts {
			if tp, ok := p.(llms.TextContent); ok {
				b.WriteString(tp.Text)
			}
		}
		return b.String()
	}

	t.Run("KBPrestepContent lands in human message and leaves system prompt stable", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for payments", KBPrestepContent: kbBlock})
		assert.Len(t, msgs, 2)
		sys, human := textOf(msgs[0]), textOf(msgs[1])
		assert.Equal(t, sysPrompt, sys, "system message must stay cache-stable")
		assert.Contains(t, human, "Domain Knowledge / Runbooks:")
		assert.Contains(t, human, kbBlock)
		assert.Contains(t, human, "Current query: logs for payments")
	})

	t.Run("SkillsContext fallback lands in human message when KBPrestepContent is empty", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for auth", SkillsContext: skillsCtx})
		assert.Len(t, msgs, 2)
		sys, human := textOf(msgs[0]), textOf(msgs[1])
		assert.Equal(t, sysPrompt, sys, "system message must stay cache-stable")
		assert.Contains(t, human, "Skills Context:")
		assert.Contains(t, human, skillsCtx)
	})

	t.Run("both KBPrestepContent and SkillsContext are included when present", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "logs for payments", KBPrestepContent: kbBlock, SkillsContext: skillsCtx})
		assert.Len(t, msgs, 2)
		sys, human := textOf(msgs[0]), textOf(msgs[1])
		assert.Equal(t, sysPrompt, sys, "system message must stay cache-stable")
		assert.Contains(t, human, "Domain Knowledge / Runbooks:")
		assert.Contains(t, human, kbBlock)
		assert.Contains(t, human, "Skills Context:")
		assert.Contains(t, human, skillsCtx)
		assert.Contains(t, human, "Current query: logs for payments")
	})

	t.Run("neither present produces clean human query without domain knowledge header", func(t *testing.T) {
		msgs := buildLogIntentMessages(sysPrompt, core.NBAgentRequest{Query: "show me logs"})
		assert.Len(t, msgs, 2)
		sys, human := textOf(msgs[0]), textOf(msgs[1])
		assert.Equal(t, sysPrompt, sys)
		assert.NotContains(t, human, "Domain Knowledge")
		assert.Equal(t, "show me logs", human)
	})
}

// TestDefaultProviderLogFields pins the fallback field vocabulary advertised
// to the query-generator when backend label discovery fails: Signoz stores
// logs under OTel attribute names — the generic `_body`/`namespace`/`pod` set
// matches no Signoz attribute and every query silently returns zero rows.
func TestDefaultProviderLogFields(t *testing.T) {
	signozFields := defaultProviderLogFields("signoz")
	assert.ElementsMatch(t,
		[]string{"body", "service.name", "k8s.cluster.name", "k8s.namespace.name", "k8s.pod.name", "trace_id"},
		signozFields)
	assert.Equal(t, signozFields, defaultProviderLogFields("SigNoz"), "provider match must be case-insensitive")
	assert.Equal(t, []string{"_body", "namespace", "pod"}, defaultProviderLogFields("loki"))
	assert.Equal(t, []string{"_body", "namespace", "pod"}, defaultProviderLogFields(""))
}

func TestIsNotFoundReason(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"kubectl NotFound", `error: error from server (NotFound): pods "llm-server" not found in namespace "nudgebee-agent"`, true},
		{"lowercase notfound", "resource notfound in cluster", true},
		{"forbidden must not trigger discovery", `error: error from server (Forbidden): pods "x" is forbidden`, false},
		{"unauthorized must not trigger discovery", "Unauthorized", false},
		{"connection refused must not trigger discovery", "dial tcp: connection refused", false},
		{"empty reason", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isNotFoundReason(tc.reason))
		})
	}
}

func TestExtractKubectlStdout(t *testing.T) {
	t.Run("unwraps stdout/stderr envelope", func(t *testing.T) {
		got := extractKubectlStdout(`{"stdout":"nudgebee  pod/relay-server-abc123-xyz\n","stderr":""}`)
		assert.Equal(t, "nudgebee  pod/relay-server-abc123-xyz", got)
	})

	t.Run("falls back to raw string for non-JSON input", func(t *testing.T) {
		got := extractKubectlStdout("nudgebee  pod/relay-server-abc123-xyz")
		assert.Equal(t, "nudgebee  pod/relay-server-abc123-xyz", got)
	})

	t.Run("empty stdout field falls back to raw trimmed input", func(t *testing.T) {
		got := extractKubectlStdout(`{"stdout":"","stderr":"some stderr"}`)
		assert.Equal(t, `{"stdout":"","stderr":"some stderr"}`, got)
	})
}

func TestParseKubectlDiscoveryCandidates(t *testing.T) {
	t.Run("parses multi-type -A output into namespace/name (kind)", func(t *testing.T) {
		stdout := "nudgebee   pod/relay-server-68868457d8-62cwv   1/1   Running   0   3d\n" +
			"nudgebee   deployment.apps/relay-server   1/1   1   1   3d\n"
		got := parseKubectlDiscoveryCandidates(stdout)
		assert.Equal(t, []string{
			"nudgebee/relay-server-68868457d8-62cwv (pod)",
			"nudgebee/relay-server (deployment.apps)",
		}, got)
	})

	t.Run("skips malformed lines without a kind/name split", func(t *testing.T) {
		stdout := "nudgebee   not-a-kind-slash-name\nnudgebee   pod/ok-pod   1/1   Running   0   1d"
		got := parseKubectlDiscoveryCandidates(stdout)
		assert.Equal(t, []string{"nudgebee/ok-pod (pod)"}, got)
	})

	t.Run("caps at discoveryCandidateCap", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < discoveryCandidateCap+5; i++ {
			b.WriteString("nudgebee   pod/relay-server-" + string(rune('a'+i)) + "   1/1   Running   0   1d\n")
		}
		got := parseKubectlDiscoveryCandidates(b.String())
		assert.Len(t, got, discoveryCandidateCap)
	})

	t.Run("empty input yields no candidates", func(t *testing.T) {
		assert.Nil(t, parseKubectlDiscoveryCandidates(""))
	})
}

func TestFormatDiscoveryHint(t *testing.T) {
	t.Run("no candidates yields empty hint", func(t *testing.T) {
		assert.Equal(t, "", formatDiscoveryHint(nil))
	})

	t.Run("renders candidates into a retry-oriented sentence", func(t *testing.T) {
		hint := formatDiscoveryHint([]string{"nudgebee/relay-server-68868457d8-62cwv (pod)"})
		assert.Contains(t, hint, "Similar resources found via discovery:")
		assert.Contains(t, hint, "nudgebee/relay-server-68868457d8-62cwv (pod)")
		assert.Contains(t, hint, "retry fetch_logs with the corrected namespace/resource name")
	})
}

// TestLogLabelsCacheKey pins the scoping of the label-discovery cache. Getting
// this wrong is silent and expensive: too coarse a key serves one ES index's
// field list for another index's query (the generator then emits fields that
// don't exist and the query returns zero rows), while a key that varies on
// incidental config whitespace/casing never hits and reinstates the
// per-fetch discovery round-trip the cache exists to remove.
func TestLogLabelsCacheKey(t *testing.T) {
	acct := "11111111-2222-3333-4444-555555555555"

	t.Run("provider name is normalised", func(t *testing.T) {
		assert.Equal(t,
			logLabelsCacheKey(acct, services_server.ObservabilityProvider{Provider: "loki"}),
			logLabelsCacheKey(acct, services_server.ObservabilityProvider{Provider: "  Loki "}),
		)
	})

	t.Run("default index is part of the key", func(t *testing.T) {
		a := logLabelsCacheKey(acct, services_server.ObservabilityProvider{Provider: "es", DefaultIndex: "app-logs-*"})
		b := logLabelsCacheKey(acct, services_server.ObservabilityProvider{Provider: "es", DefaultIndex: "nginx-access-*"})
		assert.NotEqual(t, a, b, "ES label discovery is index-scoped — entries must not collide")
	})

	t.Run("accounts never share an entry", func(t *testing.T) {
		other := "99999999-8888-7777-6666-555555555555"
		p := services_server.ObservabilityProvider{Provider: "loki"}
		assert.NotEqual(t, logLabelsCacheKey(acct, p), logLabelsCacheKey(other, p))
	})
}

// TestFetchLabelsAndIndices_ServesCachedEntry asserts the cache-hit path
// short-circuits before QueryLabels — i.e. a warm entry costs no
// services-server round-trip. The test never provisions a backend, so a cache
// miss would fall through to discovery and return empty; getting the seeded
// values back is proof the short-circuit fired.
func TestFetchLabelsAndIndices_ServesCachedEntry(t *testing.T) {
	acct := "cache-hit-" + t.Name()
	provider := services_server.ObservabilityProvider{Provider: "loki"}

	seeded := logLabels{
		Fields:    []string{"app", "namespace", "_body"},
		Indices:   map[string]string{"logs": "app-logs-*"},
		ExpiresAt: time.Now().Add(logLabelsCacheTTL),
	}
	data, err := common.MarshalJson(seeded)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(logLabelsCacheNS, logLabelsCacheKey(acct, provider), data,
		common.CacheSetWithExpiration(logLabelsCacheTTL),
		common.CacheSetWithTags(logLabelsAccountTag(acct))))

	fields, indices := fetchLabelsAndIndices(acct, provider)
	assert.Equal(t, seeded.Fields, fields)
	assert.Equal(t, seeded.Indices, indices)

	t.Run("account tag evicts every entry for the account", func(t *testing.T) {
		require.NoError(t, common.CacheDeleteWithTag(logLabelsCacheNS, logLabelsAccountTag(acct)))
		_, ok := common.CacheGet(logLabelsCacheNS, logLabelsCacheKey(acct, provider))
		assert.False(t, ok, "integration-change invalidation must drop the cached labels")
	})
}

// TestFetchLabelsAndIndices_IgnoresExpiredEntry pins the stamped-expiry check.
// bigcache (the default in_memory backend) ignores per-entry TTLs, so without
// the stamp a stale label set would be served well past logLabelsCacheTTL —
// keeping a newly deployed workload's labels out of the query-generator prompt
// for as long as the global LifeWindow. An expired entry must read as a miss.
func TestFetchLabelsAndIndices_IgnoresExpiredEntry(t *testing.T) {
	acct := "expired-labels-" + t.Name()
	provider := services_server.ObservabilityProvider{Provider: "loki"}

	data, err := common.MarshalJson(logLabels{
		Fields:    []string{"stale-label"},
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(logLabelsCacheNS, logLabelsCacheKey(acct, provider), data))

	fields, _ := fetchLabelsAndIndices(acct, provider)
	assert.NotContains(t, fields, "stale-label", "an expired entry must not be served")
}

// TestFetchLabelsAndIndices_EmptyAccountBypassesCache pins the empty-accountId
// guard. Without it the cache key degrades to ":provider:index", which every
// empty-account caller would share — a key not scoped to a tenant. Discovery
// for an empty account fails upstream today, so nothing reaches the write path
// anyway; this asserts the guard holds regardless of that downstream behaviour.
func TestFetchLabelsAndIndices_EmptyAccountBypassesCache(t *testing.T) {
	provider := services_server.ObservabilityProvider{Provider: "loki"}
	sharedKey := logLabelsCacheKey("", provider)
	// Best-effort clear; CacheDelete errors when the key is absent, which is the
	// normal state here.
	_ = common.CacheDelete(logLabelsCacheNS, sharedKey)

	fields, indices := fetchLabelsAndIndices("", provider)
	assert.Empty(t, fields)
	assert.Empty(t, indices)

	_, ok := common.CacheGet(logLabelsCacheNS, sharedKey)
	assert.False(t, ok, "an empty accountId must never write to the tenant-less shared key")
}
