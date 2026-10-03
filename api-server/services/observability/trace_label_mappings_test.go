package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The trace doubles carry no ProviderRef of their own — traces thread the resolved
// (provider, source) pair from the call site rather than deriving it from the source
// type (see resolveTraceLabelMapping). Tests therefore pass an explicit ref, and this
// is the one they seed under.
var traceDoubleRef = providerRef{Provider: "datadog", Source: "user"}

// seedTraceIntegrationMapping writes an integration-tier mapping straight into the
// nb_trace_label_mappings cache, which is how these tests supply that layer without
// standing up an integration row. Mirrors seedIntegrationMapping in label_mapping_test.go.
func seedTraceIntegrationMapping(t *testing.T, accountId string, ref providerRef, value map[string]string) {
	t.Helper()
	key := traceLabelMappingsCacheKey(accountId, ref)
	b, err := json.Marshal(value)
	require.NoError(t, err)
	// Tagged exactly as getIntegrationTraceLabels writes it — InvalidateTraceLabelMappingsCache
	// deletes by tag, so an untagged seed would make invalidation look broken here.
	require.NoError(t, common.CacheSet(traceLabelMappingsCacheNamespace, key, b,
		common.CacheSetWithTags(traceLabelMappingsAccountTag(accountId))))
	t.Cleanup(func() { _ = common.CacheDelete(traceLabelMappingsCacheNamespace, key) })
}

// clearTraceIntegrationMapping drops the integration-tier cache entry before and after
// a test, so a test that expects "nothing configured" cannot inherit another's seed.
func clearTraceIntegrationMapping(t *testing.T, accountId string, ref providerRef) {
	t.Helper()
	key := traceLabelMappingsCacheKey(accountId, ref)
	_ = common.CacheDelete(traceLabelMappingsCacheNamespace, key)
	t.Cleanup(func() { _ = common.CacheDelete(traceLabelMappingsCacheNamespace, key) })
}

// ---------------------------------------------------------------------------
// The anti-drift anchor
// ---------------------------------------------------------------------------

// TestGetMergedTraceLabelMapping_IsProjectionOfResolver is the trace twin of
// TestGetMergedLabelMapping_IsProjectionOfResolver, and exists for the same reason:
// the panel's whole value is that it reports what queries actually do, and two
// implementations of the precedence rule is how that stops being true, silently.
func TestGetMergedTraceLabelMapping_IsProjectionOfResolver(t *testing.T) {
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
			static: map[string]string{"span_name": "operation_name"},
		},
		{
			name:    "account only",
			account: map[string]string{"service_name": "account_service"},
		},
		{
			name:        "integration only",
			integration: map[string]string{"service_name": "integration_service"},
		},
		{
			name:        "one key set in every tier",
			static:      map[string]string{"service_name": "static_service"},
			account:     map[string]string{"service_name": "account_service"},
			dynamic:     map[string]string{"service_name": "dynamic_service"},
			integration: map[string]string{"service_name": "integration_service"},
		},
		{
			name:        "disjoint keys across tiers",
			static:      map[string]string{"span_name": "operation_name"},
			account:     map[string]string{"workload_name": "account_workload"},
			dynamic:     map[string]string{"status_code": "status"},
			integration: map[string]string{"service_name": "integration_service"},
		},
		{
			name:        "higher tier carries an empty value",
			static:      map[string]string{"service_name": "static_service"},
			integration: map[string]string{"service_name": ""},
		},
		{
			name:        "higher tier carries an empty key",
			static:      map[string]string{"service_name": "static_service"},
			integration: map[string]string{"": "orphan"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accountId := "unit-trace-projection-" + tc.name
			ref := traceDoubleRef
			clearTraceCache(t, accountId)
			clearTraceIntegrationMapping(t, accountId, ref)
			if tc.account != nil {
				seedTraceCache(t, accountId, tc.account)
			}
			seedTraceIntegrationMapping(t, accountId, ref, tc.integration)

			source := &mockDynamicTraceSource{
				mockTraceSource: mockTraceSource{staticMapping: tc.static},
				dynamicMapping:  tc.dynamic,
			}

			resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref, source, nil)
			merged := getMergedTraceLabelMapping(newLogLabelCtx(), accountId, ref, source)

			assert.Equal(t, resolved.Effective, merged,
				"getMergedTraceLabelMapping must be a projection of resolveTraceLabelMapping")
			assert.Equal(t, flattenLayers(resolved.Layers), merged,
				"Effective must be derivable from Layers alone")
		})
	}
}

// TestResolveTraceLabelMapping_LayersCoverEveryTierInOrder fails the moment a tier is
// added to the trace resolver without being added to labelMappingTierOrder (or the
// other way round) — which would leave a layer silently unmerged or unrendered. It also
// pins that traces reuse the log tier order rather than forking one.
func TestResolveTraceLabelMapping_LayersCoverEveryTierInOrder(t *testing.T) {
	const accountId = "unit-trace-layer-order"
	ref := traceDoubleRef
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceIntegrationMapping(t, accountId, ref, nil)

	resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref,
		&mockTraceSource{staticMapping: map[string]string{}}, nil)

	require.Len(t, resolved.Layers, len(labelMappingTierOrder))
	for i, tier := range labelMappingTierOrder {
		assert.Equal(t, tier, resolved.Layers[i].Tier)
		assert.NotNil(t, resolved.Layers[i].Mappings, "an empty tier must be an empty map, never nil")
	}
}

// ---------------------------------------------------------------------------
// Per-tier precedence
// ---------------------------------------------------------------------------

// TestResolveTraceLabelMapping_IntegrationBeatsProviderConfig is the boundary the
// labelMappingTierOrder comment justifies: a provider that seeds defaults for its own
// column fields would otherwise swallow a mapping the operator explicitly typed.
func TestResolveTraceLabelMapping_IntegrationBeatsProviderConfig(t *testing.T) {
	const accountId = "unit-trace-integration-vs-providerconfig"
	ref := traceDoubleRef
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "integration_service"})

	source := &mockDynamicTraceSource{
		mockTraceSource: mockTraceSource{staticMapping: map[string]string{"service_name": "static_service"}},
		dynamicMapping:  map[string]string{"service_name": "provider_config_service"},
	}

	resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref, source, nil)
	assert.Equal(t, "integration_service", resolved.Effective["service_name"])
	assert.Equal(t, LabelTierIntegration, fieldFor(t, resolved, "service_name").WinningTier)
}

// TestResolveTraceLabelMapping_IntegrationBeatsAccount covers the tier PR #37411 gave
// operators a Settings screen for: the integration mapping is per-account too, and
// outranks it.
func TestResolveTraceLabelMapping_IntegrationBeatsAccount(t *testing.T) {
	const accountId = "unit-trace-integration-vs-account"
	ref := traceDoubleRef
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceCache(t, accountId, map[string]string{"service_name": "account_service"})
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "integration_service"})

	resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref,
		&mockTraceSource{staticMapping: map[string]string{"service_name": "static_service"}}, nil)

	assert.Equal(t, "integration_service", resolved.Effective["service_name"])
	// The overridden values must still be reported, or the panel cannot show what lost.
	field := fieldFor(t, resolved, "service_name")
	assert.Equal(t, "account_service", field.Contributions[LabelTierAccount])
	assert.Equal(t, "static_service", field.Contributions[LabelTierProviderDefault])
}

// TestResolveTraceLabelMapping_AccountBeatsProviderDefault is the tier an operator
// reaches through the account tile.
func TestResolveTraceLabelMapping_AccountBeatsProviderDefault(t *testing.T) {
	const accountId = "unit-trace-account-vs-default"
	ref := traceDoubleRef
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceCache(t, accountId, map[string]string{"service_name": "account_service"})

	resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref,
		&mockTraceSource{staticMapping: map[string]string{
			"service_name": "static_service",
			"span_name":    "operation_name",
		}}, nil)

	assert.Equal(t, "account_service", resolved.Effective["service_name"])
	assert.Equal(t, "operation_name", resolved.Effective["span_name"], "an unset key falls through")
	assert.Equal(t, LabelTierAccount, fieldFor(t, resolved, "service_name").WinningTier)
	assert.Equal(t, LabelTierProviderDefault, fieldFor(t, resolved, "span_name").WinningTier)
}

// fieldFor pulls one canonical field out of the panel projection.
func fieldFor(t *testing.T, r ResolvedLabelMapping, canonical string) LabelMappingField {
	t.Helper()
	for _, f := range r.Fields(nil) {
		if f.Canonical == canonical {
			return f
		}
	}
	t.Fatalf("canonical field %q not present in the resolved fields", canonical)
	return LabelMappingField{}
}

// ---------------------------------------------------------------------------
// Cache scoping
// ---------------------------------------------------------------------------

// TestGetIntegrationTraceLabels_ScopedToOneAccount verifies the cache key keeps two
// accounts on the SAME integration apart — the reason this tier is per-account at all.
func TestGetIntegrationTraceLabels_ScopedToOneAccount(t *testing.T) {
	const accountA = "unit-trace-scope-a"
	const accountB = "unit-trace-scope-b"
	ref := traceDoubleRef
	clearTraceIntegrationMapping(t, accountA, ref)
	clearTraceIntegrationMapping(t, accountB, ref)
	seedTraceIntegrationMapping(t, accountA, ref, map[string]string{"service_name": "a_service"})
	seedTraceIntegrationMapping(t, accountB, ref, map[string]string{"service_name": "b_service"})

	assert.Equal(t, map[string]string{"service_name": "a_service"},
		getIntegrationTraceLabels(newLogLabelCtx(), accountA, ref))
	assert.Equal(t, map[string]string{"service_name": "b_service"},
		getIntegrationTraceLabels(newLogLabelCtx(), accountB, ref))
}

// TestGetIntegrationTraceLabels_ScopedToOneIntegration verifies the same for two trace
// integrations on ONE account: querying a non-default provider must read that
// integration's mapping, not the default's.
func TestGetIntegrationTraceLabels_ScopedToOneIntegration(t *testing.T) {
	const accountId = "unit-trace-scope-integration"
	datadog := providerRef{Provider: "datadog", Source: "user"}
	clickhouse := providerRef{Provider: "otel_clickhouse", Source: "agent"}
	clearTraceIntegrationMapping(t, accountId, datadog)
	clearTraceIntegrationMapping(t, accountId, clickhouse)
	seedTraceIntegrationMapping(t, accountId, datadog, map[string]string{"service_name": "datadog_service"})
	seedTraceIntegrationMapping(t, accountId, clickhouse, map[string]string{"service_name": "clickhouse_service"})

	assert.Equal(t, map[string]string{"service_name": "datadog_service"},
		getIntegrationTraceLabels(newLogLabelCtx(), accountId, datadog))
	assert.Equal(t, map[string]string{"service_name": "clickhouse_service"},
		getIntegrationTraceLabels(newLogLabelCtx(), accountId, clickhouse))
}

// TestGetIntegrationTraceLabels_SeparateFromLogMapping pins the reason the config key
// and the cache namespace are distinct: datadog, dynatrace, chronosphere, signoz and ES
// are ONE integration record serving both signals, so a shared key would apply an
// operator's log field mapping to their trace queries.
func TestGetIntegrationTraceLabels_SeparateFromLogMapping(t *testing.T) {
	const accountId = "unit-trace-vs-log-namespace"
	ref := providerRef{Provider: "datadog", Source: "user"}
	clearIntegrationMapping(t, accountId, ref)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "log_pod"})
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "trace_service"})

	assert.Equal(t, map[string]string{"service_name": "trace_service"},
		getIntegrationTraceLabels(newLogLabelCtx(), accountId, ref),
		"the trace tier must not see the log integration mapping")
	assert.Equal(t, map[string]string{"pod": "log_pod"},
		getIntegrationLogLabels(newLogLabelCtx(), accountId, ref),
		"and the log tier must not see the trace one")
	assert.NotEqual(t, core.LogLabelMappingsConfigName, core.TraceLabelMappingsConfigName)
}

func TestGetIntegrationTraceLabels_EmptyAccountId(t *testing.T) {
	assert.Empty(t, getIntegrationTraceLabels(newLogLabelCtx(), "", traceDoubleRef))
}

// TestGetIntegrationTraceLabels_FailsOpen verifies that an unresolvable integration
// yields an empty tier rather than an error: a misconfigured integration must never
// break a trace query.
func TestGetIntegrationTraceLabels_FailsOpen(t *testing.T) {
	const accountId = "unit-trace-fail-open"
	ref := providerRef{Provider: "no-such-provider", Source: "user"}
	clearTraceIntegrationMapping(t, accountId, ref)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	assert.Empty(t, getIntegrationTraceLabels(newLogLabelCtx(), accountId, ref))
}

// TestLoadIntegrationTraceLabels_Sanitize covers the parse contract: a malformed blob
// is an empty tier, and half-filled rows are dropped rather than renaming a field to "".
func TestLoadIntegrationTraceLabels_Sanitize(t *testing.T) {
	assert.Equal(t, map[string]string{"service_name": "svc"},
		sanitizeLabelMapping(map[string]string{
			"service_name": " svc ",
			"span_name":    "",
			"":             "orphan",
		}))
}

// ---------------------------------------------------------------------------
// Draft preview
// ---------------------------------------------------------------------------

// TestResolveTraceLabelMapping_DraftReplacesSavedIntegrationTier lets the panel preview
// rows the operator has typed but not yet saved.
func TestResolveTraceLabelMapping_DraftReplacesSavedIntegrationTier(t *testing.T) {
	const accountId = "unit-trace-draft-replaces"
	ref := traceDoubleRef
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "saved_service"})

	resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref,
		&mockTraceSource{staticMapping: map[string]string{}},
		&labelMappingOverride{Set: true, Mappings: map[string]string{"service_name": "draft_service"}})

	assert.Equal(t, "draft_service", resolved.Effective["service_name"])
}

// TestResolveTraceLabelMapping_DraftSetEmptyClearsTier pins why Set is separate from a
// nil/empty Mappings: "the operator deleted every row" is a different answer from "read
// what is saved".
func TestResolveTraceLabelMapping_DraftSetEmptyClearsTier(t *testing.T) {
	const accountId = "unit-trace-draft-clears"
	ref := traceDoubleRef
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "saved_service"})

	resolved := resolveTraceLabelMapping(newLogLabelCtx(), accountId, ref,
		&mockTraceSource{staticMapping: map[string]string{"service_name": "static_service"}},
		&labelMappingOverride{Set: true})

	assert.Equal(t, "static_service", resolved.Effective["service_name"],
		"an emptied draft must clear the integration tier, not fall back to what is saved")
}

// ---------------------------------------------------------------------------
// Invalidation
// ---------------------------------------------------------------------------

// TestInvalidateTraceLabelMappingsCache_DropsEveryProviderForTheAccount covers the
// account tag: an integration save must clear the mapping under every provider/source
// it was cached with, or a saved mapping reads as broken for up to ten minutes.
func TestInvalidateTraceLabelMappingsCache_DropsEveryProviderForTheAccount(t *testing.T) {
	const accountId = "unit-trace-invalidate"
	datadog := providerRef{Provider: "datadog", Source: "user"}
	clickhouse := providerRef{Provider: "otel_clickhouse", Source: "agent"}
	seedTraceIntegrationMapping(t, accountId, datadog, map[string]string{"service_name": "a"})
	seedTraceIntegrationMapping(t, accountId, clickhouse, map[string]string{"service_name": "b"})

	InvalidateTraceLabelMappingsCache(accountId)

	_, okA := common.CacheGet(traceLabelMappingsCacheNamespace, traceLabelMappingsCacheKey(accountId, datadog))
	_, okB := common.CacheGet(traceLabelMappingsCacheNamespace, traceLabelMappingsCacheKey(accountId, clickhouse))
	assert.False(t, okA, "the datadog entry must be dropped")
	assert.False(t, okB, "the otel_clickhouse entry must be dropped too")
}

// TestInvalidateTraceLabelsCacheForAccount_AlsoDropsIntegrationTier pins the chaining
// that makes the account-attrs save path (api/actions_account.go) cover BOTH per-account
// tiers, mirroring InvalidateLogLabelsCacheForAccount.
func TestInvalidateTraceLabelsCacheForAccount_AlsoDropsIntegrationTier(t *testing.T) {
	const accountId = "unit-trace-invalidate-chained"
	ref := traceDoubleRef
	seedTraceCache(t, accountId, map[string]string{"service_name": "account_service"})
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "integration_service"})

	InvalidateTraceLabelsCacheForAccount(accountId)

	_, okAccount := common.CacheGet(traceLabelsCacheNamespace, accountId)
	_, okIntegration := common.CacheGet(traceLabelMappingsCacheNamespace, traceLabelMappingsCacheKey(accountId, ref))
	assert.False(t, okAccount, "the account tier must be dropped")
	assert.False(t, okIntegration, "the integration tier must be dropped in the same call")
}

// TestTraceLabelMappingsAccountTagIsPrefixed pins the gocache trap trace_default_filters.go
// documents: invalidation tags match across the WHOLE store, so a bare "account:<id>" tag
// here would make every trace-mapping invalidation also drop that account's log-filter and
// log-label-mapping entries.
func TestTraceLabelMappingsAccountTagIsPrefixed(t *testing.T) {
	tag := traceLabelMappingsAccountTag("acc-1")
	assert.Equal(t, "trace_label_mappings_account:acc-1", tag)
	assert.NotEqual(t, logLabelMappingsAccountTag("acc-1"), tag)
}

// TestInvalidateTraceLabelMappingsCache_LeavesLogMappingAlone is the same trap observed
// rather than asserted on the string: invalidating the trace tier must not disturb the
// log tier for the same account.
func TestInvalidateTraceLabelMappingsCache_LeavesLogMappingAlone(t *testing.T) {
	const accountId = "unit-trace-invalidate-isolation"
	ref := traceDoubleRef
	seedIntegrationMapping(t, accountId, ref, map[string]string{"pod": "log_pod"})
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "trace_service"})

	InvalidateTraceLabelMappingsCache(accountId)

	_, okLog := common.CacheGet(logLabelMappingsCacheNamespace, logLabelMappingsCacheKey(accountId, ref))
	assert.True(t, okLog, "the log integration mapping must survive a trace-tier invalidation")
}

// ---------------------------------------------------------------------------
// The provider_config tier is empty for traces, on purpose
// ---------------------------------------------------------------------------

// TestTraceSources_DoNotCarryProviderConfigMappings records the decision behind the
// empty provider_config tier. That tier exists for integrations carrying user-typed
// FIELD names — Pinot's pinot_pod_col, Hive's hive_message_col — and both are log-only
// providers absent from getTraceSource. Trace integrations carry CONTAINERS
// (trace_index, openobserve_trace_stream), not field names.
//
// If this ever fails, a trace source has gained a dynamic mapping: that is fine, but it
// must be a deliberate change, because it lands BELOW the integration tier and a source
// that seeds defaults would otherwise swallow an operator's explicit mapping.
func TestTraceSources_DoNotCarryProviderConfigMappings(t *testing.T) {
	for _, tc := range traceSourceCanonicalCases {
		t.Run(tc.provider+"/"+tc.source, func(t *testing.T) {
			source, err := getTraceSource(tc.provider, tc.source)
			require.NoError(t, err)
			_, dynamic := source.(DynamicLabelMappingSource)
			assert.False(t, dynamic,
				"%s carries a provider_config mapping; see the comment on this test before accepting it",
				tc.provider)
		})
	}
}

// ---------------------------------------------------------------------------
// The advertisement guard (hard constraint 1)
// ---------------------------------------------------------------------------

// TestIntegrationOverrideDoesNotCollapsePassthroughAdvertisement is the invariant
// providerDeclaresTraceFields exists to hold, now that a THIRD tier can supply a
// mapping. A passthrough backend consumes the canonical names unchanged, so one
// operator override must not flip it into "declared" mode and collapse its advertised
// vocabulary from ten fields to that one key — which is what would happen if
// providerDeclaresTraceFields ever read the merged map.
func TestIntegrationOverrideDoesNotCollapsePassthroughAdvertisement(t *testing.T) {
	const accountId = "unit-trace-passthrough-guard"
	ref := providerRef{Provider: "otel_clickhouse", Source: "agent"}
	clearTraceCache(t, accountId)
	clearTraceIntegrationMapping(t, accountId, ref)
	seedTraceIntegrationMapping(t, accountId, ref, map[string]string{"service_name": "ServiceName"})

	source, err := getTraceSource("otel_clickhouse", "agent")
	require.NoError(t, err)

	merged := getMergedTraceLabelMapping(newLogLabelCtx(), accountId, ref, source)
	require.Equal(t, "ServiceName", merged["service_name"], "the override must be in effect")

	require.False(t, providerDeclaresTraceFields(source),
		"providerDeclaresTraceFields must still read the STATIC mapping")
	advertised := canonicalTraceFieldNames(buildTraceLabels(merged, providerDeclaresTraceFields(source), nil))
	assert.Equal(t, allCanonicalTraceFieldNames(), advertised,
		"an operator override must not withdraw the other canonical fields")
}

// ---------------------------------------------------------------------------
// No-override parity (the regression guard for the seven switched call sites)
// ---------------------------------------------------------------------------

// TestGetMergedTraceLabelMapping_NoOverridesEqualsStatic is what makes switching the
// trace query paths off source.GetLabelMapping() safe: for an account with nothing
// configured, the resolved map is the provider's static map, so every one of those
// queries is byte-identical to what it built before.
func TestGetMergedTraceLabelMapping_NoOverridesEqualsStatic(t *testing.T) {
	patches := mockDBUnavailable(t)
	defer patches.Reset()

	for _, tc := range traceSourceCanonicalCases {
		t.Run(tc.provider+"/"+tc.source, func(t *testing.T) {
			accountId := "unit-trace-parity-" + tc.provider + "-" + tc.source
			ref := providerRef{Provider: tc.provider, Source: tc.source}
			clearTraceCache(t, accountId)
			clearTraceIntegrationMapping(t, accountId, ref)

			source, err := getTraceSource(tc.provider, tc.source)
			require.NoError(t, err)

			merged := getMergedTraceLabelMapping(newLogLabelCtx(), accountId, ref, source)
			assert.Equal(t, source.GetLabelMapping(), merged,
				"with no override configured the resolved map must equal the static one")
		})
	}
}

// ---------------------------------------------------------------------------
// What the API advertises
// ---------------------------------------------------------------------------

// TestCanonicalTraceFieldsOnly_DropsProviderAliases is the readability fix, pinned at the
// layer that decides it. Datadog publishes 14 OTel-semconv aliases beside 7 canonical
// keys, and '@' sorts below every letter, so before this filter the panel opened on
// fourteen rows of provider plumbing.
func TestCanonicalTraceFieldsOnly_DropsProviderAliases(t *testing.T) {
	source, err := getTraceSource("datadog", "user")
	require.NoError(t, err)

	// The full projection, as Fields() builds it: every layer key ∪ the canonical set.
	all := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: source.GetLabelMapping(),
	}).Fields(allCanonicalTraceFieldNames())

	aliasesBefore := 0
	for _, f := range all {
		if strings.HasPrefix(f.Canonical, "@") {
			aliasesBefore++
		}
	}
	require.Positive(t, aliasesBefore, "datadog must still publish aliases, else this test proves nothing")

	got := canonicalTraceFieldsOnly(all)

	assert.Len(t, got, len(canonicalTraceFields), "exactly the canonical vocabulary is advertised")
	canonical := map[string]struct{}{}
	for _, f := range canonicalTraceFields {
		canonical[f.name] = struct{}{}
	}
	for _, f := range got {
		assert.NotContains(t, f.Canonical, "@", "no provider alias may survive")
		assert.Contains(t, canonical, f.Canonical)
	}
}

// TestCanonicalTraceFieldsOnly_KeepsResolvedValues verifies the filter drops ROWS, never
// rewrites one: a canonical field that the provider does map keeps its value and winning
// tier untouched.
func TestCanonicalTraceFieldsOnly_KeepsResolvedValues(t *testing.T) {
	source, err := getTraceSource("datadog", "user")
	require.NoError(t, err)
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: source.GetLabelMapping(),
	})

	for _, f := range canonicalTraceFieldsOnly(resolved.Fields(allCanonicalTraceFieldNames())) {
		if f.Canonical == "span_name" {
			assert.Equal(t, "operation_name", f.Effective)
			assert.Equal(t, LabelTierProviderDefault, f.WinningTier)
			return
		}
	}
	t.Fatal("span_name must still be advertised — datadog maps it")
}

// TestCanonicalTraceFieldsOnly_LeavesEffectiveAlone is the invariant that keeps this a
// DISPLAY filter. Effective is what convertWhereClauseWithMApping rewrites queries with,
// so the aliases must survive there even though the panel no longer lists them. If this
// ever fails, dropping an alias row has started changing query behaviour.
func TestCanonicalTraceFieldsOnly_LeavesEffectiveAlone(t *testing.T) {
	source, err := getTraceSource("datadog", "user")
	require.NoError(t, err)
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: source.GetLabelMapping(),
	})

	advertised := canonicalTraceFieldsOnly(resolved.Fields(allCanonicalTraceFieldNames()))
	for _, f := range advertised {
		require.NotContains(t, f.Canonical, "@")
	}

	assert.Equal(t, "pod_name", resolved.Effective["@k8s.pod.name"],
		"the alias must stay in Effective — queries are rewritten with it")
	assert.Equal(t, source.GetLabelMapping(), resolved.Effective,
		"Effective is untouched by the advertisement filter")
}

// TestCanonicalTraceFieldsOnly_PassthroughAdvertisesEverything covers the other extreme:
// otel_clickhouse publishes three alias keys and NO canonical ones, so the panel used to
// show three rows of aliases and nothing mappable. It must now show the full vocabulary,
// every field unmapped.
func TestCanonicalTraceFieldsOnly_PassthroughAdvertisesEverything(t *testing.T) {
	source, err := getTraceSource("otel_clickhouse", "agent")
	require.NoError(t, err)
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: source.GetLabelMapping(),
	})

	got := canonicalTraceFieldsOnly(resolved.Fields(allCanonicalTraceFieldNames()))
	require.Len(t, got, len(canonicalTraceFields))
	for _, f := range got {
		assert.Empty(t, f.Effective, "%s: a passthrough provider maps nothing by name", f.Canonical)
		assert.Empty(t, f.WinningTier)
	}
}

// TestCanonicalTraceFieldsOnly_OperatorKeyIsDropped records the accepted consequence
// rather than leaving it to be discovered. A non-canonical key an operator sets is no
// longer advertised, though it still rewrites queries — visible instead as an editable
// row in the mapping cards, and on the tenant/account Settings screens.
func TestCanonicalTraceFieldsOnly_OperatorKeyIsDropped(t *testing.T) {
	resolved := newResolvedLabelMapping(map[LabelMappingTier]map[string]string{
		LabelTierIntegration: {"my_custom_field": "X", "service_name": "svc"},
	})

	got := canonicalTraceFieldsOnly(resolved.Fields(allCanonicalTraceFieldNames()))
	for _, f := range got {
		assert.NotEqual(t, "my_custom_field", f.Canonical)
	}
	assert.Equal(t, "X", resolved.Effective["my_custom_field"],
		"it must still be in force even though it is not advertised")
}
