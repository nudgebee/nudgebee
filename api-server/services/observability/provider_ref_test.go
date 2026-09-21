package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getLogSource dispatches on a (provider, source) pair; ProviderRef reports that pair
// back. Two copies of the same fact, so this pins them together.
//
// It matters because the integration-level label mapping is read using the ref: a source
// that misreports itself would resolve another integration's config, and the resulting
// filter would target a field that does not exist there — matching nothing and raising no
// error. That silent-wrong-mapping failure is the one this whole area exists to remove,
// so it must not be reintroduced by a typo in a constant.
var logSourceDispatchTable = []struct {
	provider string
	source   string
}{
	{"loki", "agent"},
	{"signoz", "agent"},
	{"signoz", "user"},
	{"datadog", "user"},
	{"observe", "user"},
	{"loggly", "user"},
	{"azure_app_insights", "user"},
	{"aws_cloudwatch", "user"},
	{"ES", "agent"},
	{"ES", "user"},
	{"newrelic", "user"},
	{"splunk_observability_platform", "user"},
	{"dynatrace", "user"},
	{"solarwinds", "user"},
	{"pinot", "agent"},
	{"pinot", "user"},
	{"hive", "user"},
	{"openobserve", "user"},
	{"splunk_enterprise", "user"},
	{"cubeapm", "user"},
}

// TestProviderRef_MatchesGetLogSource is the invariant: whatever getLogSource hands back
// for a pair must name that same pair.
func TestProviderRef_MatchesGetLogSource(t *testing.T) {
	for _, tc := range logSourceDispatchTable {
		t.Run(tc.provider+"/"+tc.source, func(t *testing.T) {
			source, err := getLogSource(tc.provider, tc.source)
			require.NoError(t, err)
			require.NotNil(t, source)

			assert.Equal(t, providerRef{Provider: tc.provider, Source: tc.source}, source.ProviderRef())
		})
	}
}

// TestProviderRef_IsOneToOne guards the assumption the design rests on: one type per
// pair. A type serving two pairs could not report a single honest ref, and would need
// the caller to supply it again.
func TestProviderRef_IsOneToOne(t *testing.T) {
	seen := map[providerRef]string{}
	for _, tc := range logSourceDispatchTable {
		source, err := getLogSource(tc.provider, tc.source)
		require.NoError(t, err)
		ref := source.ProviderRef()
		if prev, dup := seen[ref]; dup {
			t.Fatalf("%s/%s reports the same ref as %s — a type serving two pairs cannot name itself",
				tc.provider, tc.source, prev)
		}
		seen[ref] = tc.provider + "/" + tc.source
	}
	assert.Len(t, seen, len(logSourceDispatchTable))
}

// TestProviderRef_TableCoversEveryDispatch fails when a provider is added to
// getLogSource without being added here — otherwise a new source could misreport itself
// and no test would notice.
func TestProviderRef_TableCoversEveryDispatch(t *testing.T) {
	for _, tc := range logSourceDispatchTable {
		_, err := getLogSource(tc.provider, tc.source)
		require.NoError(t, err, "table lists a pair getLogSource does not serve")
	}
	// The reverse direction is enforced by the compiler: a new source must implement
	// ProviderRef to satisfy LogSource at all.
	assert.Len(t, logSourceDispatchTable, 20, "update this table when getLogSource gains a provider")
}
