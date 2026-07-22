package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFetchLogsAgentV2_EmbedsV1Identity guards the embedding contract: v2 must
// keep v1's tool name ("fetch_logs") and Custom planner type so it slots into
// the same factory/registration without a router change.
func TestFetchLogsAgentV2_EmbedsV1Identity(t *testing.T) {
	a := &FetchLogsAgentV2{FetchLogsAgent: &FetchLogsAgent{}}
	assert.Equal(t, FetchLogsAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeCustom, a.GetPlannerType())
}

// TestFetchResponseIsEmpty pins the kubectl-fallback trigger: a services-server
// fetch is "empty" when the makeFetchResponse envelope has no log content (blank
// logs, the No-logs sentinel, or a {"logs":[]} envelope), but NOT when it has
// real rows or a kubectl {"stdout":...} body (which has no "logs" key).
func TestFetchResponseIsEmpty(t *testing.T) {
	// Build envelopes through the real makeFetchResponse so the test tracks the
	// actual wire shape the agent produces.
	env := func(logs string) core.NBAgentResponse {
		return makeFetchResponse(FetchLogsAgentName, `{"where":{}}`, logs, "", nil)
	}
	cases := []struct {
		name string
		resp core.NBAgentResponse
		want bool
	}{
		{"no response body", core.NBAgentResponse{}, true},
		{"blank logs", env(""), true},
		{"no-logs sentinel", env("No logs found for loki (canonical where: {}; ...)"), true},
		{"empty logs envelope", env(`{"logs":[]}`), true},
		{"real rows", env(`{"logs":[{"timestamp":"t","message":"boom"}]}`), false},
		{"kubectl stdout body (not empty)", env(`{"stdout":"line1\nline2","stderr":""}`), false},
		{"unparseable body is not treated as empty", core.NBAgentResponse{Response: []string{"not-json"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, fetchResponseIsEmpty(tc.resp))
		})
	}
}

// TestCanonicalQueryExamples asserts the v2 few-shots use provider-independent
// canonical entity names — never provider-native field names (which would
// defeat services-server's canonical→provider resolution).
//
// The examples are STRUCTURE-only: field names are placeholders the model
// substitutes with a canonical_name from the account's advertised mapping. They
// must NOT hardcode the generic service/pod/message vocabulary (which only
// resolves when those words are literally keys in label_mappings) nor any
// provider-native field name.
func TestCanonicalQueryExamples(t *testing.T) {
	examples := canonicalQueryExamples()
	assert.NotEmpty(t, examples)

	var answers strings.Builder
	for _, e := range examples {
		answers.WriteString(e.Answer)
	}
	joined := answers.String()

	// Placeholder field tokens the model must substitute are present.
	assert.Contains(t, joined, "<WORKLOAD_FIELD>")
	assert.Contains(t, joined, "<LOG_TEXT_FIELD>")
	assert.Contains(t, joined, "<NAMESPACE_FIELD>")

	// Standard canonical names must NOT be hardcoded as field keys — they only
	// resolve when present in the account's label_mappings, so anchoring them is
	// exactly the bug this guards against.
	assert.NotContains(t, joined, `"service"`)
	assert.NotContains(t, joined, `"message"`)
	assert.NotContains(t, joined, `"pod"`)

	// Provider-native field names must never leak into canonical examples.
	assert.NotContains(t, joined, "kubernetes.pod_name.keyword")
	assert.NotContains(t, joined, "service.name")
	assert.NotContains(t, joined, "severity_text")
}

// TestResolveQueryOperators verifies the operator set advertised to the LLM:
// backend-supplied operators win when present, the static default is used when
// they're absent, the `_or`/`_and` combinators are always present, and the
// result is de-duplicated with backend order preserved.
func TestResolveQueryOperators(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "nil falls back to default + combinators",
			input: nil,
			want:  append(append([]string{}, defaultLogQueryOperators...), "_or", "_and"),
		},
		{
			name:  "empty falls back to default + combinators",
			input: []string{},
			want:  append(append([]string{}, defaultLogQueryOperators...), "_or", "_and"),
		},
		{
			name:  "signoz backend set keeps only its ops plus combinators",
			input: []string{"_eq", "_neq", "_contains", "_like"},
			want:  []string{"_eq", "_neq", "_contains", "_like", "_or", "_and"},
		},
		{
			name:  "backend list already containing a combinator is not duplicated",
			input: []string{"_eq", "_or", "_like"},
			want:  []string{"_eq", "_or", "_like", "_and"},
		},
		{
			name:  "blank and duplicate entries are dropped",
			input: []string{"_eq", "", "_eq", "  ", "_neq"},
			want:  []string{"_eq", "_neq", "_or", "_and"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveQueryOperators(tc.input)
			assert.Equal(t, tc.want, got)
			assert.Contains(t, got, "_or")
			assert.Contains(t, got, "_and")
		})
	}
}

// TestBuildCanonicalLogQueryPrompt is the deterministic regression guard for the
// canonical log-query prompt. The query JSON itself is LLM-generated (and can't
// be asserted offline), so this pins the prompt RULES that fix the observed
// failure modes — value hallucination (nudgebee→production), dropped filters,
// window over-widening (last 30m→24h), and `service_name` leakage — so a future
// prompt edit can't silently drop them.
func TestBuildCanonicalLogQueryPrompt(t *testing.T) {
	// Loki-style account: canonical label_mappings present → canonical mode.
	lokiProvider := services_server.ObservabilityProvider{
		Provider: "loki",
		Capabilities: services_server.ProviderCapabilities{
			SupportedOperators: []string{"_eq", "_neq", "_ilike", "_like"},
			LabelMappings: map[string]string{
				"app": "app", "namespace": "namespace", "pod": "pod",
				"container": "container", "content": "log",
			},
		},
	}
	p := buildCanonicalLogQueryPrompt(lokiProvider, []string{"app", "namespace", "pod", "container", "level"}, nil)

	t.Run("advertises the canonical field mapping", func(t *testing.T) {
		assert.Contains(t, p, "Canonical fields for THIS backend")
		assert.Contains(t, p, "app → app")
		assert.Contains(t, p, "content → log")
	})

	t.Run("forbids service_name (the OTel prior the model leaks)", func(t *testing.T) {
		assert.Contains(t, p, "do NOT emit `service_name`")
		assert.Contains(t, p, "`service.name`")
	})

	t.Run("explicit window is a hard constraint, never widened", func(t *testing.T) {
		assert.Contains(t, p, "A window in the question is a HARD constraint")
		assert.Contains(t, p, "NEVER widen or shrink a window")
		assert.Contains(t, p, "ONLY when the question gives NO window")
	})

	t.Run("values come from the question, never substituted or dropped", func(t *testing.T) {
		assert.Contains(t, p, "values come from the QUESTION, never from the examples")
		assert.Contains(t, p, "VERBATIM")
		assert.Contains(t, p, "NEVER substitute a different name")
		assert.Contains(t, p, "NEVER drop a name the user gave")
	})

	t.Run("operators are advertised", func(t *testing.T) {
		assert.Contains(t, p, "_ilike")
	})

	t.Run("namespace vs workload — bare namespace must not become an app", func(t *testing.T) {
		assert.Contains(t, p, "Namespace vs workload")
		assert.Contains(t, p, "NEVER put that value in the workload/app/pod field")
		assert.Contains(t, p, "is NOT an app name")
	})

	t.Run("prefers canonical over a same-concept backend label, no invented fields", func(t *testing.T) {
		assert.Contains(t, p, "ALWAYS prefer a `canonical_name` over a backend label")
		assert.Contains(t, p, "fallback ONLY for a concept that has NO matching canonical_name")
		assert.Contains(t, p, "never invent or guess a field name")
	})

	// No label_mappings → native-field branch, but the value/window hard rules
	// (which live outside the canonical block) must still be present.
	t.Run("native-field fallback when no canonical mapping", func(t *testing.T) {
		native := services_server.ObservabilityProvider{Provider: "loki"}
		pn := buildCanonicalLogQueryPrompt(native, []string{"app", "namespace"}, nil)
		assert.Contains(t, pn, "AVAILABLE FIELDS")
		assert.NotContains(t, pn, "Canonical fields for THIS backend")
		assert.Contains(t, pn, "A window in the question is a HARD constraint")
		assert.Contains(t, pn, "values come from the QUESTION, never from the examples")
	})
}

// TestCanonicalLogQueryGeneration_Live runs the real failing/edge questions
// (from the manual fetch_logs testing) through generateCanonicalLogQuery against
// the configured LLM and asserts the generated query honours the user's entities
// and window. It is GATED — it needs the live stack (LLM provider + services-server
// for the account's provider/label-mappings), so it only runs when
// RUN_LOG_AGENT_LIVE=1 and TEST_ACCOUNT/TEST_USER/TEST_TENANT are set. Run with:
//
//	RUN_LOG_AGENT_LIVE=1 go test ./agents/ -run TestCanonicalLogQueryGeneration_Live -v
func TestCanonicalLogQueryGeneration_Live(t *testing.T) {
	if os.Getenv("RUN_LOG_AGENT_LIVE") == "" {
		t.Skip("gated: set RUN_LOG_AGENT_LIVE=1 (+ TEST_ACCOUNT/TEST_USER/TEST_TENANT and a running stack) to run")
	}
	account, user, tenant := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT")
	require.NotEmpty(t, account, "TEST_ACCOUNT required")

	// Resolve the account's real provider + canonical label-mappings + labels,
	// exactly as the agent does at request time.
	agent := newFetchLogsAgentV2(account)
	require.NotEmpty(t, agent.provider.Provider, "account has no services-server log provider configured")
	agent.ensureLabelsAndIndices()
	ctx := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})

	cases := []struct {
		name  string
		query string
		// expectedCanonical, when set, pins the EXACT canonical JSON via JSONEq
		// (semantic compare — ignores key order/whitespace). Used for cases whose
		// generation is empirically byte-stable. When empty, the looser
		// wantSubstr/forbid checks apply instead (for cases the weak model varies).
		expectedCanonical string
		wantSubstr        []string // must ALL appear in the generated query JSON
		forbid            []string // must NOT appear
	}{
		// === Cases with a pinned `expectedCanonical` are empirically byte-stable
		// across repeated live runs, so we assert the EXACT canonical JSON. Cases
		// without it (the weak model varies their value/operator/field run-to-run)
		// keep the looser wantSubstr/forbid checks. ===
		{
			name:              "app + namespace + explicit 30m window",
			query:             "errors in app llm-server in namespace nudgebee last 30m",
			expectedCanonical: `{"where":{"app":{"_eq":"llm-server"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%error%"}},"time_range":"30m","limit":5000}`,
		},
		{
			name:              "different app, same shape",
			query:             "errors in app relay-server in namespace nudgebee last 30m",
			expectedCanonical: `{"where":{"app":{"_eq":"relay-server"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%error%"}},"time_range":"30m","limit":5000}`,
		},
		{
			// VARIANT: field for the namespace drifts (`namespace` ↔ `k8s_namespace_name`),
			// so substrings only.
			name:       "all logs, explicit window honoured, no error filter",
			query:      "all logs for app services-server in namespace nudgebee last 1h",
			wantSubstr: []string{"services-server", "nudgebee", `"1h"`},
			forbid:     []string{"service_name", `"24h"`},
		},
		// === OR-condition queries → must emit a canonical `_or` combinator over
		// the log content. These previously produced invalid `(|~ a or |~ b)` LogQL
		// in services-server (now fixed by the buildWhere regex-alternation change).
		{
			name:              "OR over two error levels (ERROR or FATAL)",
			query:             "ERROR or FATAL logs for app services-server in namespace nudgebee last 1h",
			expectedCanonical: `{"where":{"app":{"_eq":"services-server"},"namespace":{"_eq":"nudgebee"},"_or":[{"content":{"_ilike":"%ERROR%"}},{"content":{"_ilike":"%FATAL%"}}]},"time_range":"1h","limit":5000}`,
		},
		{
			name:  "OR over two severities (warning or error)",
			query: "warning or error logs for app llm-server in namespace nudgebee last 30m",
			// VARIANT: the model occasionally abbreviates `%warning%`→`%warn%`, so
			// pin the `_or` structure via substrings rather than the exact value.
			wantSubstr: []string{"_or", "warn", "error", "llm-server", "nudgebee", `"30m"`},
			forbid:     []string{"service_name", `"24h"`},
		},
		{
			name:              "OR over two status codes, namespace-only (401 or 403)",
			query:             "401 or 403 errors in namespace nudgebee last 6h",
			expectedCanonical: `{"where":{"namespace":{"_eq":"nudgebee"},"_or":[{"content":{"_ilike":"%401%"}},{"content":{"_ilike":"%403%"}}]},"time_range":"6h","limit":5000}`,
		},
		{
			name:              "OR over three error levels (ERROR, FATAL or PANIC)",
			query:             "ERROR, FATAL or PANIC logs for app services-server in namespace nudgebee last 1h",
			expectedCanonical: `{"where":{"app":{"_eq":"services-server"},"namespace":{"_eq":"nudgebee"},"_or":[{"content":{"_ilike":"%ERROR%"}},{"content":{"_ilike":"%FATAL%"}},{"content":{"_ilike":"%PANIC%"}}]},"time_range":"1h","limit":5000}`,
		},
		// === Other operators / conditions ===
		{
			// VARIANT: combined OR + content term; structure drifts run-to-run.
			name:       "OR severities combined with a 'containing <term>' filter",
			query:      "ERROR or WARN logs mentioning database for app llm-server in namespace nudgebee last 30m",
			wantSubstr: []string{"_or", "error", "warn", "database", "llm-server", "nudgebee", `"30m"`},
			forbid:     []string{"service_name", `"24h"`},
		},
		{
			// A specific multi-word phrase → a single _ilike content filter.
			name:              "contains a specific phrase",
			query:             "logs containing connection refused for app cloud-collector-server in namespace nudgebee last 6h",
			expectedCanonical: `{"where":{"app":{"_eq":"cloud-collector-server"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%connection refused%"}},"time_range":"6h","limit":5000}`,
		},
		{
			// VARIANT: the negation operator drifts (`_nlike` ↔ `_nicontains`), so
			// pin only that the entities/window/excluded-term survive.
			name:       "exclusion / negation of a term",
			query:      "error logs for app services-server in namespace nudgebee last 1h excluding any line containing healthz",
			wantSubstr: []string{"services-server", "nudgebee", "healthz", `"1h"`},
			forbid:     []string{"service_name", `"24h"`},
		},
		// === Edge cases: entity classification, value-verbatim, exact-window. ===
		{
			// Bare namespace, NO workload → namespace field only (no `app`).
			name:              "bare namespace, no workload, is not an app",
			query:             "errors in nudgebee in the last 1h",
			expectedCanonical: `{"where":{"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%error%"}},"time_range":"1h","limit":5000}`,
		},
		{
			// Pod-level: the exact pod name survives verbatim in the pod field.
			name:              "pod-level query keeps the pod name verbatim",
			query:             "error logs for pod cloud-collector-server-7d9f in namespace nudgebee last 1h",
			expectedCanonical: `{"where":{"pod":{"_eq":"cloud-collector-server-7d9f"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%error%"}},"time_range":"1h","limit":5000}`,
		},
		{
			// Container + pod: both entities in their own fields (no content → limit 1000).
			name:              "container within a pod keeps both entities",
			query:             "logs for container istio-proxy in pod services-server-abc in namespace nudgebee last 30m",
			expectedCanonical: `{"where":{"container":{"_eq":"istio-proxy"},"pod":{"_eq":"services-server-abc"},"namespace":{"_eq":"nudgebee"}},"time_range":"30m","limit":1000}`,
		},
		{
			// Short window honoured exactly, never widened.
			name:              "short explicit window (15m) honoured exactly",
			query:             "errors in app services-server in namespace nudgebee in the last 15 minutes",
			expectedCanonical: `{"where":{"app":{"_eq":"services-server"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%error%"}},"time_range":"15m","limit":5000}`,
		},
		{
			// Long window honoured exactly, never shrunk.
			name:              "long explicit window (24h) honoured exactly",
			query:             "errors for app llm-server in namespace nudgebee in the last 24 hours",
			expectedCanonical: `{"where":{"app":{"_eq":"llm-server"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%error%"}},"time_range":"24h","limit":5000}`,
		},
		{
			// Numeric HTTP status code preserved as a content term, verbatim.
			name:              "numeric status code preserved as a content term",
			query:             "500 errors for app services-server in namespace nudgebee last 1h",
			expectedCanonical: `{"where":{"app":{"_eq":"services-server"},"namespace":{"_eq":"nudgebee"},"content":{"_ilike":"%500%"}},"time_range":"1h","limit":5000}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := core.NBAgentRequest{Query: tc.query, OriginalQuery: tc.query, AccountId: account, UserId: user}
			out, err := generateCanonicalLogQuery(ctx, req, agent.provider, agent.fields, agent.indices)
			require.NoError(t, err)
			t.Logf("query=%q -> %s", tc.query, out)

			// Byte-stable cases pin the EXACT canonical query; JSONEq compares
			// semantically (ignores key order/whitespace), so only a wrong
			// operator/field/value/structure fails.
			if tc.expectedCanonical != "" {
				assert.JSONEq(t, tc.expectedCanonical, out, "exact canonical mismatch for %q", tc.query)
				return
			}

			// Looser checks for cases the weak model varies between runs.
			gen := strings.ToLower(out)
			for _, w := range tc.wantSubstr {
				assert.Contains(t, gen, strings.ToLower(w), "generated query missing %q", w)
			}
			for _, f := range tc.forbid {
				assert.NotContains(t, gen, strings.ToLower(f), "generated query must not contain %q", f)
			}
		})
	}
}
