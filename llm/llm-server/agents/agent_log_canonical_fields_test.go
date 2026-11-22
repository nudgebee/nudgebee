package agents

import (
	"testing"

	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
)

// providerWithCanonicalFields is the shape a current services-server returns: the
// vocabulary it will resolve (`canonical_fields`) plus the subset of it that needs a
// rename (`label_mappings`). The two are NOT the same list — Loki passes `namespace`,
// `pod` and `app` through unchanged and renames only the log-text field — which is
// why the prompt can no longer be built from the mapping keys alone (#39573).
func providerWithCanonicalFields(canonical []string, mappings map[string]string) services_server.ObservabilityProvider {
	return services_server.ObservabilityProvider{
		Provider: "loki",
		Capabilities: services_server.ProviderCapabilities{
			SupportedOperators: []string{"_eq", "_ilike"},
			LabelMappings:      mappings,
			CanonicalFields:    canonical,
		},
	}
}

// The regression this closes: `message` is resolvable on Loki but is not a mapping
// key for most providers, so a prompt built from the keys never advertised it and the
// model fell through to the invented `_body`.
func TestBuildCanonicalLogQueryPrompt_AdvertisesCanonicalFieldsBeyondTheMapping(t *testing.T) {
	p := buildCanonicalLogQueryPrompt(
		providerWithCanonicalFields(
			[]string{"app", "container", "message", "namespace", "pod"},
			map[string]string{"message": "log"},
		),
		[]string{"app", "namespace"}, nil)

	assert.Contains(t, p, "Canonical fields for THIS backend")
	// Names only, renamed or not: services-server translates each one, so the
	// backend field (`log`) is never shown to the model.
	assert.Contains(t, p, "- message\n")
	assert.Contains(t, p, "- namespace\n")
	assert.NotContains(t, p, "→ log")
	assert.NotContains(t, p, "_body")
}

// The gate used to be `len(labelMappings) > 0`, so a backend that advertises a
// vocabulary while renaming nothing (every canonical name is already its own field
// name) fell through to the provider-native branch and lost the vocabulary entirely.
func TestBuildCanonicalLogQueryPrompt_CanonicalFieldsAloneEnableTheCanonicalBlock(t *testing.T) {
	p := buildCanonicalLogQueryPrompt(
		providerWithCanonicalFields([]string{"message", "namespace", "pod"}, nil),
		[]string{"namespace", "pod", "message"}, nil)

	assert.Contains(t, p, "Canonical fields for THIS backend")
	assert.Contains(t, p, "- message\n")
	assert.NotContains(t, p, "AVAILABLE FIELDS for query building")
}

// Pre-capability services-server: no vocabulary advertised, so the mapping keys are
// still the best available answer. Deleting this fallback would break every install
// whose api-server image predates the capability, which is a normal pairing here.
func TestBuildCanonicalLogQueryPrompt_FallsBackToMappingKeys(t *testing.T) {
	p := buildCanonicalLogQueryPrompt(
		providerWithCanonicalFields(nil, map[string]string{"app": "app", "content": "log"}),
		[]string{"app", "namespace"}, nil)

	assert.Contains(t, p, "Canonical fields for THIS backend")
	assert.Contains(t, p, "- content\n")
	assert.NotContains(t, p, "- message")
}

// The same three properties for the V3 renderer, which is the one the `logs` agent
// uses. It had its own copy of the keys-based derivation.
func TestBuildCanonicalLogQueryPromptV3_UsesCanonicalFields(t *testing.T) {
	p := buildCanonicalLogQueryPromptV3(
		providerWithCanonicalFields([]string{"message", "namespace"}, map[string]string{"message": "log"}),
		[]string{"namespace", "pod"}, nil)

	assert.Contains(t, p, "Canonical fields for THIS backend")
	assert.Contains(t, p, "- message\n")
	assert.NotContains(t, p, "→ log")
	assert.NotContains(t, p, "AVAILABLE FIELDS for query building")

	t.Run("no vocabulary advertised keeps the mapping keys", func(t *testing.T) {
		legacy := buildCanonicalLogQueryPromptV3(
			providerWithCanonicalFields(nil, map[string]string{"content": "log"}),
			[]string{"namespace"}, nil)
		assert.Contains(t, legacy, "- content\n")
		assert.NotContains(t, legacy, "- message")
	})
}

func TestResolveCanonicalLogFields(t *testing.T) {
	cases := []struct {
		name            string
		canonicalFields []string
		labelMappings   map[string]string
		fields          []string
		want            []string
	}{
		{"advertised vocabulary wins", []string{"pod", "message"}, map[string]string{"content": "log"}, []string{"pod", "message"}, []string{"message", "pod"}},
		{"sorted and trimmed", []string{" pod ", "app"}, nil, []string{"app", "pod"}, []string{"app", "pod"}},
		{"blank entries dropped", []string{"", "  ", "message"}, nil, []string{"message"}, []string{"message"}},
		// The list comes from another service, so it is normalised rather than trusted:
		// a repeated name reads to the model as emphasis and costs tokens twice.
		{"duplicates removed", []string{"pod", "message", "pod", " pod "}, nil, []string{"pod", "message"}, []string{"message", "pod"}},
		// `canonical_fields` is what a caller may filter on, not what this backend
		// resolves: a name neither renamed nor in the label list is not advertised.
		{"unreported and unmapped is dropped", []string{"message", "node", "pod"}, nil, []string{"message", "pod", "node_name"}, []string{"message", "pod"}},
		{"renamed counts without being reported", []string{"message", "pod"}, map[string]string{"message": "log"}, []string{"pod"}, []string{"message", "pod"}},
		// Both sides are trimmed, so a padded label name still matches its canonical
		// name instead of dropping a name the backend does report.
		{"a padded label name still matches", []string{"message", "pod"}, nil, []string{" message ", "pod"}, []string{"message", "pod"}},
		{"no labels leaves only the renamed names", []string{"message", "namespace"}, map[string]string{"message": "log"}, nil, []string{"message"}},
		{"falls back to mapping keys", nil, map[string]string{"content": "log", "app": "app"}, nil, []string{"app", "content"}},
		{"nothing either way", nil, nil, nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveCanonicalLogFields(tc.canonicalFields, tc.labelMappings, tc.fields))
		})
	}
}

// The review finding on #40357, with dev Loki's real shape: services-server lists all
// twelve canonical names, but `node`, `level`, `cluster`, `service` and `severity` are
// neither renamed nor Loki labels, so they reach Loki as-is and return 0 rows with a
// success status. QueryLabels has already dropped them from fields; the canonical
// block must not bring them back as "resolved automatically".
func TestBuildCanonicalLogQueryPrompt_OmitsCanonicalNamesTheBackendCannotResolve(t *testing.T) {
	provider := providerWithCanonicalFields(
		[]string{"app", "cluster", "container", "level", "message", "namespace", "node", "pod", "service", "severity", "timestamp", "trace_id"},
		map[string]string{"_body": "log", "content": "log", "message": "log"},
	)
	fields := []string{"namespace", "pod", "container", "app", "_body", "content", "node_name", "k8s_cluster_name", "service_name", "message"}

	for name, p := range map[string]string{
		"v2": buildCanonicalLogQueryPrompt(provider, fields, nil),
		"v3": buildCanonicalLogQueryPromptV3(provider, fields, nil),
	} {
		t.Run(name, func(t *testing.T) {
			for _, kept := range []string{"- app\n", "- container\n", "- message\n", "- namespace\n", "- pod\n"} {
				assert.Contains(t, p, kept)
			}
			for _, dead := range []string{"- node\n", "- level\n", "- cluster\n", "- service\n", "- severity\n"} {
				assert.NotContains(t, p, dead)
			}
			assert.Contains(t, p, "node_name", "the real backend label stays advertised")
		})
	}
}
