package observability

import (
	"encoding/json"
	"reflect"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLogSource / mockDynamicLogSource report {loki, agent}; the integration tier is read
// under whatever the source names, so seed under exactly that rather than an unrelated
// literal — otherwise the seed lands where nothing looks.
var testDoubleRef = providerRef{Provider: "loki", Source: "agent"}

// The doubles satisfy LogSource by naming that same ref, so a test that seeds the
// integration tier under testDoubleRef seeds where the resolver will actually look.
// Kept beside the constant rather than next to each double, mirroring provider_ref.go.
func (m *mockLogSource) ProviderRef() providerRef   { return testDoubleRef }
func (s *stubLogSource) ProviderRef() providerRef   { return testDoubleRef }
func (f *fakeLabelSource) ProviderRef() providerRef { return testDoubleRef }

// seedIntegrationMapping writes an integration-tier mapping straight into the
// nb_log_label_mappings cache, which is how these tests supply that layer without
// standing up an integration row. Mirrors seedCache in log_labels_test.go.
func seedIntegrationMapping(t *testing.T, accountId string, ref providerRef, value map[string]string) {
	t.Helper()
	key := logLabelMappingsCacheKey(accountId, ref)
	b, err := json.Marshal(value)
	require.NoError(t, err)
	// Tagged exactly as getIntegrationLogLabels writes it — InvalidateLogLabelMappingsCache
	// deletes by tag, so an untagged seed would make invalidation look broken here and
	// let a genuine regression hide behind the difference.
	require.NoError(t, common.CacheSet(logLabelMappingsCacheNamespace, key, b,
		common.CacheSetWithTags(logLabelMappingsAccountTag(accountId))))
	t.Cleanup(func() { _ = common.CacheDelete(logLabelMappingsCacheNamespace, key) })
}

// clearIntegrationMapping drops the integration-tier cache entry before and after a
// test, so a test that expects "nothing configured" cannot inherit another's seed.
func clearIntegrationMapping(t *testing.T, accountId string, ref providerRef) {
	t.Helper()
	key := logLabelMappingsCacheKey(accountId, ref)
	_ = common.CacheDelete(logLabelMappingsCacheNamespace, key)
	t.Cleanup(func() { _ = common.CacheDelete(logLabelMappingsCacheNamespace, key) })
}

// ---------------------------------------------------------------------------
// The anti-drift anchor
// ---------------------------------------------------------------------------

// TestGetMergedLabelMapping_IsProjectionOfResolver is the test that must fail if
// anyone ever re-implements the merge for the Advanced Settings panel instead of
// projecting the resolver. The panel's whole value is that it reports what queries
// actually do; two implementations of the precedence rule is how that stops being
// true, silently.
func TestGetMergedLabelMapping_IsProjectionOfResolver(t *testing.T) {
	cases := []struct {
		name        string
		static      map[string]string
		account     map[string]string
		dynamic     map[string]string
		integration map[string]string
	}{
		{name: "all tiers empty"},
		{
			name:   "provider default only",
			static: map[string]string{"content": "log"},
		},
		{
			name:    "account only",
			account: map[string]string{"pod": "account_pod"},
		},
		{
			name:        "integration only",
			integration: map[string]string{"pod": "integration_pod"},
		},
		{
			name:        "one key set in every tier",
			static:      map[string]string{"pod": "static_pod"},
			account:     map[string]string{"pod": "account_pod"},
			dynamic:     map[string]string{"pod": "dynamic_pod"},
			integration: map[string]string{"pod": "integration_pod"},
		},
		{
			name:        "disjoint keys across tiers",
			static:      map[string]string{"content": "log"},
			account:     map[string]string{"app": "account_app"},
			dynamic:     map[string]string{"level": "severity"},
			integration: map[string]string{"pod": "integration_pod"},
		},
		{
			name:        "higher tier carries an empty value",
			static:      map[string]string{"pod": "static_pod"},
			integration: map[string]string{"pod": ""},
		},
		{
			name:        "higher tier carries an empty key",
			static:      map[string]string{"pod": "static_pod"},
			integration: map[string]string{"": "orphan"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accountId := "unit-projection-" + tc.name
			ref := testDoubleRef
			clearCache(t, accountId)
			clearIntegrationMapping(t, accountId, ref)
			if tc.account != nil {
				seedCache(t, accountId, tc.account)
			}
			seedIntegrationMapping(t, accountId, ref, tc.integration)

			source := &mockDynamicLogSource{
				mockLogSource:  mockLogSource{staticMapping: tc.static},
				dynamicMapping: tc.dynamic,
			}

			resolved := resolveLogLabelMapping(newLogLabelCtx(), accountId, source, nil)
			merged := getMergedLabelMapping(newLogLabelCtx(), accountId, source)

			assert.Equal(t, resolved.Effective, merged,
				"getMergedLabelMapping must be a projection of resolveLogLabelMapping")
			assert.Equal(t, flattenLayers(resolved.Layers), merged,
				"Effective must be derivable from Layers alone")
		})
	}
}

// TestResolveLogLabelMapping_LayersCoverEveryTierInOrder fails the moment a tier is
// added to the resolver without being added to labelMappingTierOrder (or the other
// way round) — which would leave a layer silently unmerged or unrendered.
func TestResolveLogLabelMapping_LayersCoverEveryTierInOrder(t *testing.T) {
	const accountId = "unit-layer-order"
	ref := testDoubleRef
	clearCache(t, accountId)
	clearIntegrationMapping(t, accountId, ref)
	seedIntegrationMapping(t, accountId, ref, nil)

	resolved := resolveLogLabelMapping(newLogLabelCtx(), accountId,
		&mockLogSource{staticMapping: map[string]string{}}, nil)

	require.Len(t, resolved.Layers, len(labelMappingTierOrder))
	for i, tier := range labelMappingTierOrder {
		assert.Equal(t, tier, resolved.Layers[i].Tier)
		assert.NotNil(t, resolved.Layers[i].Mappings, "an empty tier must be an empty map, never nil")
	}
}

// ---------------------------------------------------------------------------
// flattenLayers
// ---------------------------------------------------------------------------

func TestFlattenLayers_HighestTierWins(t *testing.T) {
	layers := []LabelMappingLayer{
		{Tier: LabelTierProviderDefault, Mappings: map[string]string{"pod": "a"}},
		{Tier: LabelTierTenant, Mappings: map[string]string{"pod": "b"}},
		{Tier: LabelTierAccount, Mappings: map[string]string{"pod": "c"}},
		{Tier: LabelTierProviderConfig, Mappings: map[string]string{"pod": "d"}},
		{Tier: LabelTierIntegration, Mappings: map[string]string{"pod": "e"}},
	}
	assert.Equal(t, map[string]string{"pod": "e"}, flattenLayers(layers))
}

// TestFlattenLayers_EmptyValueNeverErases pins the trap that makes this function
// worth having: convertWhereClauseWithMApping renames on key PRESENCE, so letting an
// empty value through would rewrite the column to "" and break every query using it,
// while also masking a working lower-tier mapping.
func TestFlattenLayers_EmptyValueNeverErases(t *testing.T) {
	layers := []LabelMappingLayer{
		{Tier: LabelTierProviderDefault, Mappings: map[string]string{"pod": "kubernetes.pod_name"}},
		{Tier: LabelTierIntegration, Mappings: map[string]string{"pod": "", "": "orphan"}},
	}
	merged := flattenLayers(layers)

	assert.Equal(t, "kubernetes.pod_name", merged["pod"], "an empty override must not erase a working mapping")
	_, hasOrphan := merged[""]
	assert.False(t, hasOrphan, "an empty canonical key must never reach the query mapping")
}

// ---------------------------------------------------------------------------
// Precedence boundaries
// ---------------------------------------------------------------------------

// TestResolveLogLabelMapping_IntegrationBeatsProviderConfig is the boundary this
// change introduces, and the one with a real trap behind it: GetPinotConfig seeds
// defaults for its column fields, so the provider_config tier is never empty for
// them. Were it on top, an operator's explicit mapping would be swallowed by a
// default nobody typed.
func TestResolveLogLabelMapping_IntegrationBeatsProviderConfig(t *testing.T) {
	const accountId = "unit-integration-vs-providerconfig"
	ref := testDoubleRef
	clearCache(t, accountId)
	clearIntegrationMapping(t, accountId, ref)
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "integration_pod"})

	source := &mockDynamicLogSource{
		mockLogSource:  mockLogSource{staticMapping: map[string]string{}},
		dynamicMapping: map[string]string{"pod": "provider_config_pod", "level": "severity_col"},
	}

	resolved := resolveLogLabelMapping(newLogLabelCtx(), accountId, source, nil)

	assert.Equal(t, "integration_pod", resolved.Effective["pod"])
	assert.Equal(t, "severity_col", resolved.Effective["level"],
		"a provider_config key the integration does not set must survive")
}

func TestResolveLogLabelMapping_IntegrationBeatsAccount(t *testing.T) {
	const accountId = "unit-integration-vs-account"
	ref := testDoubleRef
	clearCache(t, accountId)
	clearIntegrationMapping(t, accountId, ref)
	seedCache(t, accountId, map[string]string{"pod": "account_pod", "app": "account_app"})
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "integration_pod"})

	resolved := resolveLogLabelMapping(newLogLabelCtx(), accountId,
		&mockLogSource{staticMapping: map[string]string{}}, nil)

	assert.Equal(t, "integration_pod", resolved.Effective["pod"])
	assert.Equal(t, "account_app", resolved.Effective["app"])
}

// TestGetIntegrationLogLabels_ScopedToOneAccount verifies the cache key keeps two
// accounts on the same provider apart. An integration commonly serves several
// accounts, and leaking one account's field names into another's queries would
// return other people's logs or none at all.
func TestGetIntegrationLogLabels_ScopedToOneAccount(t *testing.T) {
	ref := testDoubleRef
	const accountA = "unit-scope-account-a"
	const accountB = "unit-scope-account-b"
	clearIntegrationMapping(t, accountA, ref)
	clearIntegrationMapping(t, accountB, ref)
	seedIntegrationMapping(t, accountA, ref, map[string]string{"pod": "a_pod"})
	seedIntegrationMapping(t, accountB, ref, map[string]string{"pod": "b_pod"})

	assert.Equal(t, "a_pod", getIntegrationLogLabels(newLogLabelCtx(), accountA, ref)["pod"])
	assert.Equal(t, "b_pod", getIntegrationLogLabels(newLogLabelCtx(), accountB, ref)["pod"])
}

// TestGetIntegrationLogLabels_ScopedToOneIntegration verifies the same for two log
// integrations on ONE account. The logs tab can pin a non-default provider, and
// reading the default integration's mapping while querying the pinned one is exactly
// the bug defaultLogFiltersCacheKey documents avoiding.
func TestGetIntegrationLogLabels_ScopedToOneIntegration(t *testing.T) {
	const accountId = "unit-scope-two-integrations"
	pinot := providerRef{Provider: "pinot", Source: "user"}
	loki := providerRef{Provider: "loki", Source: "user"}
	clearIntegrationMapping(t, accountId, pinot)
	clearIntegrationMapping(t, accountId, loki)
	seedIntegrationMapping(t, accountId, pinot, map[string]string{"pod": "pinot_pod"})
	seedIntegrationMapping(t, accountId, loki, map[string]string{"pod": "loki_pod"})

	assert.Equal(t, "pinot_pod", getIntegrationLogLabels(newLogLabelCtx(), accountId, pinot)["pod"])
	assert.Equal(t, "loki_pod", getIntegrationLogLabels(newLogLabelCtx(), accountId, loki)["pod"])
}

func TestGetIntegrationLogLabels_EmptyAccountId(t *testing.T) {
	assert.Empty(t, getIntegrationLogLabels(newLogLabelCtx(), "", providerRef{Provider: "ES", Source: "user"}))
}

// TestGetIntegrationLogLabels_FailsOpen verifies that an unresolvable integration
// degrades to "nothing configured" rather than an error. A bad or absent blob must
// never take log querying down with it.
func TestGetIntegrationLogLabels_FailsOpen(t *testing.T) {
	const accountId = "unit-integration-fails-open"
	ref := providerRef{Provider: "definitely-not-a-provider", Source: "user"}
	clearIntegrationMapping(t, accountId, ref)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	assert.Empty(t, getIntegrationLogLabels(newLogLabelCtx(), accountId, ref))
}

// TestLoadIntegrationLogLabels_MalformedAndPartialRows covers the parse contract:
// invalid JSON degrades to empty, a non-matching account contributes nothing, and
// half-filled rows are dropped rather than reaching the query mapping.
func TestLoadIntegrationLogLabels_Sanitize(t *testing.T) {
	got := sanitizeLabelMapping(map[string]string{
		"pod":       " kubernetes.pod_name ",
		"namespace": "",
		"":          "orphan",
		"  ":        "blank-key",
		"app":       "kubernetes.labels.app",
	})
	assert.Equal(t, map[string]string{
		"pod": "kubernetes.pod_name",
		"app": "kubernetes.labels.app",
	}, got)
}

// ---------------------------------------------------------------------------
// Draft (unsaved form rows)
// ---------------------------------------------------------------------------

func TestResolveLogLabelMapping_DraftReplacesSavedIntegrationTier(t *testing.T) {
	const accountId = "unit-draft-replaces"
	ref := testDoubleRef
	clearCache(t, accountId)
	clearIntegrationMapping(t, accountId, ref)
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "saved_pod"})

	resolved := resolveLogLabelMapping(newLogLabelCtx(), accountId,
		&mockLogSource{staticMapping: map[string]string{}},
		&labelMappingOverride{Set: true, Mappings: map[string]string{"pod": "draft_pod"}})

	assert.Equal(t, "draft_pod", resolved.Effective["pod"])
}

// TestResolveLogLabelMapping_DraftSetEmptyClearsTier pins why Set is a separate
// field from the map: "the operator deleted every row" must be expressible, and is
// a different answer from "no draft supplied, read what is saved".
func TestResolveLogLabelMapping_DraftSetEmptyClearsTier(t *testing.T) {
	const accountId = "unit-draft-clears"
	ref := testDoubleRef
	clearCache(t, accountId)
	clearIntegrationMapping(t, accountId, ref)
	seedCache(t, accountId, map[string]string{"pod": "account_pod"})
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "saved_pod"})

	cleared := resolveLogLabelMapping(newLogLabelCtx(), accountId,
		&mockLogSource{staticMapping: map[string]string{}},
		&labelMappingOverride{Set: true})
	assert.Equal(t, "account_pod", cleared.Effective["pod"],
		"clearing every row must fall through to the account tier")

	notSupplied := resolveLogLabelMapping(newLogLabelCtx(), accountId,
		&mockLogSource{staticMapping: map[string]string{}}, nil)
	assert.Equal(t, "saved_pod", notSupplied.Effective["pod"],
		"no draft must read the saved blob")
}

// ---------------------------------------------------------------------------
// Provenance projection
// ---------------------------------------------------------------------------

func TestFields_WinnerContributionsAndOrdering(t *testing.T) {
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: {"pod": "static_pod"},
		LabelTierTenant:          {"pod": "tenant_pod", "namespace": "tenant_ns"},
		LabelTierAccount:         {"pod": "account_pod"},
		LabelTierIntegration:     {"pod": "integration_pod"},
	})

	fields := resolved.Fields(nil)

	var names []string
	for _, f := range fields {
		names = append(names, f.Canonical)
	}
	assert.Equal(t, []string{"namespace", "pod"}, names, "fields must be sorted by canonical name")

	var pod LabelMappingField
	for _, f := range fields {
		if f.Canonical == "pod" {
			pod = f
		}
	}
	assert.Equal(t, "integration_pod", pod.Effective)
	assert.Equal(t, LabelTierIntegration, pod.WinningTier)
	assert.Equal(t, map[LabelMappingTier]string{
		LabelTierProviderDefault: "static_pod",
		LabelTierTenant:          "tenant_pod",
		LabelTierAccount:         "account_pod",
		LabelTierIntegration:     "integration_pod",
	}, pod.Contributions, "every tier that had an opinion must be listed, silent ones must not")
}

// TestFields_UnmappedKnownFieldsAreListed covers the half of the panel's value that
// is about what is NOT set: an unmapped canonical name is passed to the backend
// verbatim and usually matches nothing, which is invisible unless it is shown.
func TestFields_UnmappedKnownFieldsAreListed(t *testing.T) {
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierIntegration: {"pod": "kubernetes.pod_name"},
	})

	fields := resolved.Fields(knownCanonicalLogFields)

	byName := map[string]LabelMappingField{}
	for _, f := range fields {
		byName[f.Canonical] = f
	}

	require.Contains(t, byName, "level")
	assert.Empty(t, byName["level"].Effective)
	assert.Empty(t, byName["level"].WinningTier, "an unmapped field has no winning tier")
	assert.Empty(t, byName["level"].Contributions)

	assert.Equal(t, "kubernetes.pod_name", byName["pod"].Effective)

	// The listing is display-only: unmapped fields must not leak into the map that
	// rewrites queries, or every filter on them would target an empty column name.
	assert.NotContains(t, resolved.Effective, "level")
}

func TestFields_DeterministicAcrossCalls(t *testing.T) {
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierIntegration: {"pod": "p", "namespace": "n", "app": "a", "level": "l"},
	})
	first := resolved.Fields(knownCanonicalLogFields)
	for i := 0; i < 20; i++ {
		assert.True(t, reflect.DeepEqual(first, resolved.Fields(knownCanonicalLogFields)),
			"Fields must be stable across calls — Go map order is not")
	}
}

// ---------------------------------------------------------------------------
// Cache invalidation
// ---------------------------------------------------------------------------

// TestInvalidateLogLabelsCacheForAccount covers the regression that made the panel
// worth distrusting: neither cache had an invalidation hook, so a saved mapping took
// up to the 10 min TTL to apply.
func TestInvalidateLogLabelsCacheForAccount(t *testing.T) {
	const accountId = "unit-invalidate-account"
	const tenantId = "unit-invalidate-tenant"
	ref := testDoubleRef

	seedCache(t, accountId, map[string]string{"pod": "old_pod"})
	seedCache(t, tenantCacheKeyPrefix+tenantId, map[string]string{"pod": "tenant_pod"})
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "old_integration_pod"})

	InvalidateLogLabelsCacheForAccount(accountId)

	_, accountCached := common.CacheGet(logLabelsCacheNamespace, accountId)
	assert.False(t, accountCached, "the account entry must be dropped")

	_, integrationCached := common.CacheGet(logLabelMappingsCacheNamespace, logLabelMappingsCacheKey(accountId, ref))
	assert.False(t, integrationCached, "the integration entry must be dropped")

	_, tenantCached := common.CacheGet(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantId)
	assert.True(t, tenantCached, "an account invalidation must not drop the tenant entry")
}

func TestInvalidateLogLabelsCacheForTenant(t *testing.T) {
	const accountId = "unit-invalidate-tenant-only-account"
	const tenantId = "unit-invalidate-tenant-only"

	seedCache(t, accountId, map[string]string{"pod": "account_pod"})
	seedCache(t, tenantCacheKeyPrefix+tenantId, map[string]string{"pod": "tenant_pod"})

	InvalidateLogLabelsCacheForTenant(tenantId)

	_, tenantCached := common.CacheGet(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantId)
	assert.False(t, tenantCached, "the tenant entry must be dropped")

	_, accountCached := common.CacheGet(logLabelsCacheNamespace, accountId)
	assert.True(t, accountCached, "a tenant invalidation must not drop account entries")
}

func TestInvalidateLogLabelsCache_EmptyIdsAreNoOps(t *testing.T) {
	assert.NotPanics(t, func() {
		InvalidateLogLabelsCacheForAccount("")
		InvalidateLogLabelsCacheForTenant("")
		InvalidateLogLabelMappingsCache("")
	})
}

// ---------------------------------------------------------------------------
// Vocabulary + wire shape
// ---------------------------------------------------------------------------

// TestKnownCanonicalLogFields_MatchesPipelineUsage pins the advertised vocabulary to
// the names the log pipeline really builds filters from, so the panel cannot start
// offering concepts nothing resolves.
func TestKnownCanonicalLogFields_MatchesPipelineUsage(t *testing.T) {
	for _, name := range []string{"app", "namespace", "pod", "trace_id", "content"} {
		assert.Contains(t, knownCanonicalLogFields, name)
	}
	seen := map[string]bool{}
	for _, name := range knownCanonicalLogFields {
		assert.NotEmpty(t, name)
		assert.False(t, seen[name], "duplicate canonical field %q", name)
		seen[name] = true
	}
}

// TestAccountLogLabelMappings_WireShape pins the JSON spelling. `accountId` matches
// the default_filters blob the same form already writes; index_account_mapping uses
// `account_id`. Both are live wire formats — normalising either breaks saved configs.
func TestAccountLogLabelMappings_WireShape(t *testing.T) {
	var rows []core.AccountLogLabelMappings
	require.NoError(t, json.Unmarshal(
		[]byte(`[{"accountId":"acc-1","mappings":{"pod":"kubernetes.pod_name.keyword"}}]`), &rows))

	require.Len(t, rows, 1)
	assert.Equal(t, "acc-1", rows[0].AccountId)
	assert.Equal(t, "kubernetes.pod_name.keyword", rows[0].Mappings["pod"])
}
