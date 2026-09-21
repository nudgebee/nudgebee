package observability

import "sort"

// providerRef identifies the specific integration a label mapping is read from.
//
// It is carried as a struct rather than two bare strings so the two values cannot
// silently swap at a call site, and so the trace mirror (trace_labels.go) can reuse
// it verbatim when it gains an integration tier.
//
// Provider is the registered integration name ("pinot", "ES", "loki", ...) and
// Source is the integration source ("user" | "agent"). Both empty means "resolve
// the account's default log integration" — the same contract
// getLogsMetricsTracesProviderWithIntegration already honours.
type providerRef struct {
	Provider string
	Source   string
}

// LabelMappingTier names one layer of the canonical -> provider field merge.
type LabelMappingTier string

const (
	// LabelTierProviderDefault is the provider's hard-coded map (source.GetLabelMapping()).
	LabelTierProviderDefault LabelMappingTier = "provider_default"
	// LabelTierTenant is tenant_attrs.log_labels, edited in Tenant Settings.
	LabelTierTenant LabelMappingTier = "tenant"
	// LabelTierAccount is cloud_account_attrs.log_labels, edited on the account tile.
	LabelTierAccount LabelMappingTier = "account"
	// LabelTierProviderConfig is DynamicLabelMappingSource — the dedicated column
	// fields Hive and Pinot already carry on their integration forms.
	LabelTierProviderConfig LabelMappingTier = "provider_config"
	// LabelTierIntegration is the generic per-account mapping an operator fills in on
	// any log integration's Advanced Settings form.
	LabelTierIntegration LabelMappingTier = "integration"
)

// labelMappingTierOrder is the SINGLE source of truth for precedence, lowest to
// highest. flattenLayers, the resolver's layer list and the UI's column order all
// derive from it, so a new tier is added in exactly one place.
//
// Integration sits above provider_config deliberately. GetPinotConfig/GetHiveConfig
// seed defaults for their column fields (PodCol: "pod", MessageCol: "log", ...), so
// GetDynamicLabelMapping cannot distinguish "the operator typed this" from "this is
// the built-in default" and is never empty for those keys. Were provider_config on
// top, an operator's explicit mapping would be swallowed by a default nobody typed —
// exactly the failure this feature exists to remove.
var labelMappingTierOrder = []LabelMappingTier{
	LabelTierProviderDefault,
	LabelTierTenant,
	LabelTierAccount,
	LabelTierProviderConfig,
	LabelTierIntegration,
}

// LabelMappingLayer is one tier's contribution to the merge.
type LabelMappingLayer struct {
	Tier     LabelMappingTier  `json:"tier"`
	Mappings map[string]string `json:"mappings"`
}

// ResolvedLabelMapping is the full precedence stack plus the map that actually
// reaches a query. Effective is computed by the constructor and is BY CONSTRUCTION
// the flatten of Layers — no caller can build one whose Effective disagrees with its
// Layers, which is what keeps the "what mapping is in effect?" panel and the query
// path from ever drifting apart.
type ResolvedLabelMapping struct {
	// Layers always has one entry per labelMappingTierOrder element, in that order
	// (lowest to highest), even when a tier contributed nothing.
	Layers    []LabelMappingLayer `json:"layers"`
	Effective map[string]string   `json:"effective"`
}

// newResolvedLabelMapping is the only constructor. Tiers absent from byTier become
// empty layers rather than being skipped, so Layers is always a complete, ordered
// picture of the merge.
func newResolvedLabelMapping(byTier map[LabelMappingTier]map[string]string) ResolvedLabelMapping {
	layers := make([]LabelMappingLayer, 0, len(labelMappingTierOrder))
	for _, tier := range labelMappingTierOrder {
		m := byTier[tier]
		if m == nil {
			m = map[string]string{}
		}
		layers = append(layers, LabelMappingLayer{Tier: tier, Mappings: m})
	}
	return ResolvedLabelMapping{Layers: layers, Effective: flattenLayers(layers)}
}

// flattenLayers collapses the stack lowest to highest.
//
// Entries with an empty key or an empty value are skipped, and that is not mere
// tidiness: convertWhereClauseWithMApping renames on key PRESENCE, so a
// mapping["pod"] == "" would rewrite the column to the empty string and break every
// query filtering on it. Skipping also means a blank cell in a higher tier leaves
// the lower tier's working mapping intact instead of erasing it.
func flattenLayers(layers []LabelMappingLayer) map[string]string {
	merged := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer.Mappings {
			if k == "" || v == "" {
				continue
			}
			merged[k] = v
		}
	}
	return merged
}

// LabelMappingField is the per-canonical-field view the Advanced Settings panel
// renders: what the field resolves to, which tier won, and what every tier that had
// an opinion contributed.
type LabelMappingField struct {
	Canonical string `json:"canonical"`
	// Effective is empty when no tier maps this field. The field is still listed:
	// showing an operator what they COULD map is half the point of the panel, and an
	// unmapped canonical name is passed to the backend verbatim, which is usually the
	// reason a filter silently matches nothing.
	Effective string `json:"effective"`
	// WinningTier is empty exactly when Effective is empty.
	WinningTier LabelMappingTier `json:"winning_tier,omitempty"`
	// Contributions carries only the tiers that supplied a non-empty value.
	Contributions map[LabelMappingTier]string `json:"contributions"`
}

// Fields projects the stack into the per-field view, sorted by canonical name so the
// panel is stable across reloads (Go map iteration order is not).
//
// alsoList names canonical fields to include even when no tier maps them; it exists
// so the panel can advertise the vocabulary rather than only what is already set.
// Nothing here re-implements the merge: Effective is read out of r.Effective.
func (r ResolvedLabelMapping) Fields(alsoList []string) []LabelMappingField {
	byTier := make(map[LabelMappingTier]map[string]string, len(r.Layers))
	names := map[string]struct{}{}
	for _, layer := range r.Layers {
		byTier[layer.Tier] = layer.Mappings
		for k, v := range layer.Mappings {
			if k != "" && v != "" {
				names[k] = struct{}{}
			}
		}
	}
	for _, name := range alsoList {
		if name != "" {
			names[name] = struct{}{}
		}
	}

	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	fields := make([]LabelMappingField, 0, len(sorted))
	for _, name := range sorted {
		field := LabelMappingField{
			Canonical:     name,
			Effective:     r.Effective[name],
			Contributions: map[LabelMappingTier]string{},
		}
		// Highest tier first, so the first hit is the winner.
		for i := len(labelMappingTierOrder) - 1; i >= 0; i-- {
			tier := labelMappingTierOrder[i]
			v := byTier[tier][name]
			if v == "" {
				continue
			}
			field.Contributions[tier] = v
			if field.WinningTier == "" {
				field.WinningTier = tier
			}
		}
		fields = append(fields, field)
	}
	return fields
}
