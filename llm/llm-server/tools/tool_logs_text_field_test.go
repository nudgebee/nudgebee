package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startLogLabelsStub answers logs_list_labels with a fixed label list. The labels a
// backend reports and the vocabulary it advertises arrive on two different channels
// (logs_list_labels vs the provider's capabilities), which is what makes the
// combinations below worth pinning separately.
func startLogLabelsStub(t *testing.T, labels ...string) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]any, 0, len(labels))
		for _, l := range labels {
			out = append(out, map[string]any{"label": l, "attributes": map[string]any{}})
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(out))
	}))

	prev := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = prev
		srv.Close()
	}
}

func logToolForTest(t *testing.T, canonicalFields []string, backendLabels ...string) *NBLogTool {
	t.Helper()
	acct := "acct-" + t.Name()
	security.SetTenantIdCacheForTest(acct, "tenant-"+t.Name())
	t.Cleanup(startLogLabelsStub(t, backendLabels...))
	return NewNBLogToolWithProvider(acct, services_server.ObservabilityProvider{
		Provider:          "loki",
		IntegrationSource: "integration",
		Capabilities:      services_server.ProviderCapabilities{CanonicalFields: canonicalFields},
	})
}

// The advertised list is what the query generator is allowed to emit, so this is the
// single place that decides whether the model writes `message` or `_body`. It had no
// coverage at all while it unconditionally appended `_body` (#39573).
func TestQueryLabels_AdvertisesCanonicalLogTextField(t *testing.T) {
	tool := logToolForTest(t, []string{"app", "message", "namespace", "pod"}, "namespace", "pod")

	assert.Equal(t, []string{"namespace", "pod", "message"}, tool.QueryLabels())
}

// services-server unions the canonical vocabulary into logs_list_labels (#39608), so
// `message` normally arrives on BOTH channels. Advertising it twice would be the
// obvious bug of appending unconditionally.
func TestQueryLabels_DoesNotDuplicateLogTextFieldAlreadyReported(t *testing.T) {
	tool := logToolForTest(t, []string{"message", "namespace"}, "namespace", "message", "pod")

	labels := tool.QueryLabels()
	assert.Equal(t, []string{"namespace", "message", "pod"}, labels)
	count := 0
	for _, l := range labels {
		if l == CanonicalLogTextFieldName {
			count++
		}
	}
	assert.Equal(t, 1, count, "the log-text field must be advertised exactly once")
}

// The skew case, and the reason the legacy token is kept rather than deleted: each
// service's image is resolved independently and on-prem installs pin chart versions,
// so this build runs against a services-server that predates `canonical_fields`.
// `_body` is wrong in principle but it is what that server resolves for Loki and ES.
func TestQueryLabels_FallsBackToLegacyTokenWhenBackendAdvertisesNothing(t *testing.T) {
	tool := logToolForTest(t, nil, "namespace", "pod")

	assert.Equal(t, []string{"namespace", "pod", LegacyLogTextToken}, tool.QueryLabels())
}

// A vocabulary that omits `message` is the backend declaring it cannot resolve a text
// field (services-server subtracts GetIgnoredQueryRequestKeys). Falling back to
// `_body` there would advertise a field matching nothing and returning success — the
// silent-empty failure this change exists to remove, re-created by the fallback.
func TestQueryLabels_AdvertisesNoTextFieldWhenBackendResolvesNone(t *testing.T) {
	tool := logToolForTest(t, []string{"namespace", "pod", "severity"}, "namespace", "pod")

	labels := tool.QueryLabels()
	assert.Equal(t, []string{"namespace", "pod"}, labels)
	assert.NotContains(t, labels, LegacyLogTextToken)
	assert.NotContains(t, labels, CanonicalLogTextFieldName)
}

func TestLogTextFieldFor(t *testing.T) {
	cases := []struct {
		name            string
		canonicalFields []string
		wantField       string
		wantAdvertise   bool
	}{
		{"advertised", []string{"app", "message"}, CanonicalLogTextFieldName, true},
		{"advertised with whitespace", []string{" message "}, CanonicalLogTextFieldName, true},
		{"nil is a pre-capability backend", nil, LegacyLogTextToken, true},
		{"empty is a pre-capability backend", []string{}, LegacyLogTextToken, true},
		{"vocabulary without a text field", []string{"namespace", "pod"}, "", false},
		// Not a substring match: a backend naming `message_id` has not told us it
		// resolves the log line's text.
		{"near miss is not a match", []string{"message_id", "messages"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field, advertise := logTextFieldFor(tc.canonicalFields)
			assert.Equal(t, tc.wantField, field)
			assert.Equal(t, tc.wantAdvertise, advertise)
		})
	}
}

// startAnnotatedLogLabelsStub answers logs_list_labels with services-server's own
// verdict on each name, as annotateLogLabelKinds sets it.
func startAnnotatedLogLabelsStub(t *testing.T, labels []map[string]any) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(labels))
	}))

	prev := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = srv.URL
	return func() {
		config.Config.ServiceEndpoint = prev
		srv.Close()
	}
}

func annotatedLogToolForTest(t *testing.T, caps services_server.ProviderCapabilities, labels []map[string]any) *NBLogTool {
	t.Helper()
	acct := "acct-" + t.Name()
	security.SetTenantIdCacheForTest(acct, "tenant-"+t.Name())
	t.Cleanup(startAnnotatedLogLabelsStub(t, labels))
	return NewNBLogToolWithProvider(acct, services_server.ObservabilityProvider{
		Provider:          "loki",
		IntegrationSource: "integration",
		Capabilities:      caps,
	})
}

// services-server lists every canonical name a caller may filter on, not only those
// this backend resolves. On dev Loki `node` and `level` are neither renamed nor stream
// labels, so they reach Loki as-is: `{node="x"}` returns 0 rows with a success status
// where `node_name` returns the data. services-server says so itself — "alias" with no
// field behind it — and that verdict is what keeps them out of the advertised list.
func TestQueryLabels_DropsCanonicalNamesTheBackendCannotResolve(t *testing.T) {
	tool := annotatedLogToolForTest(t,
		services_server.ProviderCapabilities{
			CanonicalFields: []string{"level", "message", "namespace", "node"},
			LabelMappings:   map[string]string{"message": "log"},
		},
		[]map[string]any{
			{"label": "message", "kind": "alias"}, // renamed to Loki's line filter: kept
			{"label": "namespace", "kind": "field"},
			{"label": "node", "kind": "alias"},
			{"label": "level", "kind": "alias"},
			{"label": "node_name", "kind": "field"},
		})

	assert.Equal(t, []string{"message", "namespace", "node_name"}, tool.QueryLabels())
}

// Elasticsearch translates one canonical name into whichever shipper spelling the index
// carries, and names it in `field`. That is a resolvable name, not a dead one.
func TestQueryLabels_KeepsCanonicalNameWithAReportedTarget(t *testing.T) {
	tool := annotatedLogToolForTest(t,
		services_server.ProviderCapabilities{CanonicalFields: []string{"message", "pod"}},
		[]map[string]any{
			{"label": "message", "kind": "field"},
			{"label": "pod", "kind": "alias", "field": "kubernetes.pod_name"},
			{"label": "kubernetes.pod_name", "kind": "field"},
		})

	assert.Equal(t, []string{"message", "pod", "kubernetes.pod_name"}, tool.QueryLabels())
}

// With no verdict at all (the backend reported nothing, so services-server set no
// kind) there is no real label to steer the model towards instead, and dropping the
// unverified names would cost Loki its `namespace` filter. The list is kept as it
// arrived, which is how it behaved before the verdict was read.
func TestQueryLabels_NoVerdictKeepsTheListAsItArrived(t *testing.T) {
	tool := annotatedLogToolForTest(t,
		services_server.ProviderCapabilities{CanonicalFields: []string{"message", "namespace", "node"}},
		[]map[string]any{
			{"label": "message"},
			{"label": "namespace"},
			{"label": "node"},
		})

	assert.Equal(t, []string{"message", "namespace", "node"}, tool.QueryLabels())
}

// Both sides of the canonical lookup are trimmed. A padded name that is otherwise
// unresolved must still be dropped: matching on the raw string would advertise it.
func TestQueryLabels_PaddedCanonicalNameIsStillChecked(t *testing.T) {
	tool := annotatedLogToolForTest(t,
		services_server.ProviderCapabilities{CanonicalFields: []string{"message", "node"}},
		[]map[string]any{
			{"label": "message", "kind": "field"},
			{"label": " node ", "kind": "alias"},
			{"label": "node_name", "kind": "field"},
		})

	assert.Equal(t, []string{"message", "node_name"}, tool.QueryLabels())
}
