package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildLogLabels_UnionsCanonicalMappingAndDiscovered pins the shape of the
// advertised set: canonical first (typed), then mapping keys, then whatever the
// backend reported, with no duplicates.
//
// Canonical-first matters because the query generator treats the head of this list as
// the preferred vocabulary; a canonical name reappearing lower down untyped would make
// it ambiguous.
func TestBuildLogLabels_UnionsCanonicalMappingAndDiscovered(t *testing.T) {
	canonical := []canonicalLogField{
		{name: "message", typ: query.LabelTypeString},
		{name: "pod", typ: query.LabelTypeString},
	}
	mapping := map[string]string{
		"pod":     "kubernetes.pod_name", // already canonical — must not duplicate
		"content": "log",                 // a provider alias — advertised as-is
	}
	discovered := []OutputLogLabel{
		{Label: "pod", Attributes: map[string]any{}},     // dup of canonical
		{Label: "content", Attributes: map[string]any{}}, // dup of mapping key
		{Label: "stream", Attributes: map[string]any{}},  // genuinely new
	}

	got := buildLogLabels(canonical, mapping, discovered)

	assert.Equal(t, []string{"message", "pod", "content", "stream"}, labelNames(got),
		"canonical first, then mapping keys, then discovered — deduped")

	require.Len(t, got, 4)
	assert.Equal(t, query.LabelTypeString, got[0].DataType, "canonical fields carry their type")
	assert.Equal(t, map[string]any{"type": query.LabelTypeString}, got[0].Attributes)
	assert.Empty(t, got[2].DataType, "a mapping key has no type to claim")
}

// TestBuildLogLabels_IsDeterministic guards against the advertised vocabulary changing
// between identical requests. The mapping is a Go map, so its keys must be sorted
// before they reach the list — otherwise the prompt the model sees differs per call
// and behaviour becomes unreproducible.
func TestBuildLogLabels_IsDeterministic(t *testing.T) {
	canonical := []canonicalLogField{{name: "message", typ: query.LabelTypeString}}
	mapping := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}

	first := labelNames(buildLogLabels(canonical, mapping, nil))
	for i := 0; i < 100; i++ {
		assert.Equal(t, first, labelNames(buildLogLabels(canonical, mapping, nil)),
			"advertised order changed between identical calls (iteration %d)", i)
	}
}

// TestResolvableCanonicalLogFields_WithholdsIgnoredKeys is the one thing allowed to
// remove a field from the vocabulary.
//
// Dynatrace and New Relic declare `trace_id` in GetIgnoredQueryRequestKeys, which means
// FetchLogs STRIPS that condition before querying. Advertising it would invite a filter
// that is silently dropped — returning a wider set that the caller reads as filtered,
// which is a confidently wrong answer rather than an empty one.
func TestResolvableCanonicalLogFields_WithholdsIgnoredKeys(t *testing.T) {
	for _, provider := range []string{"dynatrace", "newrelic"} {
		t.Run(provider, func(t *testing.T) {
			source, err := getLogSource(provider, "user")
			require.NoError(t, err)

			got := resolvableCanonicalLogFields(source, source.GetLabelMapping())

			var names []string
			for _, f := range got {
				names = append(names, f.name)
			}
			assert.NotContains(t, names, "trace_id",
				"%s strips trace_id from the query, so it must not be advertised", provider)
			assert.Contains(t, names, "message", "only the declared field is withheld")
		})
	}
}

// TestResolvableCanonicalLogFields_KeepsEverythingForLoki is the regression guard for
// the trap this design deliberately avoids.
//
// Loki's mapping is entirely spellings of the log line (`content`, `_body`, `message`)
// — it maps no k8s field at all, because `namespace`, `pod` and `app` are real Loki
// stream labels consumed verbatim. The trace side infers "what a provider can resolve"
// from what its mapping contains; applying that rule here would withhold namespace,
// pod, app and container from the most-used log backend, which is how the equivalent
// trace bug shipped (#36940, fixed by #37126).
func TestResolvableCanonicalLogFields_KeepsEverythingForLoki(t *testing.T) {
	source, err := getLogSource("loki", "agent")
	require.NoError(t, err)

	got := resolvableCanonicalLogFields(source, source.GetLabelMapping())

	assert.Len(t, got, len(canonicalLogFields),
		"Loki declares no ignored keys, so nothing may be withheld from it")

	var names []string
	for _, f := range got {
		names = append(names, f.name)
	}
	for _, essential := range []string{"namespace", "pod", "app", "container", "message"} {
		assert.Contains(t, names, essential)
	}
}

// TestCanonicalLogFields_AreWellFormed keeps the vocabulary itself honest.
func TestCanonicalLogFields_AreWellFormed(t *testing.T) {
	seen := map[string]struct{}{}
	for _, f := range canonicalLogFields {
		assert.NotEmpty(t, f.name)
		assert.NotEmpty(t, f.typ, "canonical field %q has no type; clients derive operators from it", f.name)
		_, dup := seen[f.name]
		assert.False(t, dup, "duplicate canonical field %q", f.name)
		seen[f.name] = struct{}{}
	}

	// `level`/`severity` and `app`/`service` are separate fields on purpose — see the
	// registry comment. Collapsing either would silently redirect a filter to the wrong
	// column on New Relic, OpenObserve, Dynatrace, CubeAPM and Splunk Enterprise.
	for _, name := range []string{"level", "severity", "app", "service"} {
		assert.Contains(t, seen, name)
	}
}

// TestCanonicalLogFieldNamesFor_MatchesLabelListing pins that the two surfaces which
// advertise the vocabulary — the provider-capabilities payload and the label listing —
// cannot drift apart, since a client may read either.
func TestCanonicalLogFieldNamesFor_MatchesLabelListing(t *testing.T) {
	for _, tc := range logSourceDispatchTable {
		t.Run(tc.provider+"/"+tc.source, func(t *testing.T) {
			source, err := getLogSource(tc.provider, tc.source)
			require.NoError(t, err)
			mapping := source.GetLabelMapping()

			fromCaps := canonicalLogFieldNamesFor(source, mapping)

			var fromLabels []string
			for _, f := range resolvableCanonicalLogFields(source, mapping) {
				fromLabels = append(fromLabels, f.name)
			}

			assert.ElementsMatch(t, fromLabels, fromCaps,
				"capabilities and the label listing advertise different vocabularies")
		})
	}
}
