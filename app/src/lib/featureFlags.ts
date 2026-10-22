// Single source of truth for "is a tenant feature effectively on?" — shared by the
// Tenant Settings features table (the toggles the user sees) and the admin Overview
// card's "features on" count, so the two never disagree.
//
// Two layers decide a feature's real state, and a naive `status === 'enabled'` count
// gets BOTH wrong:
//   1. polarity — a `default_on` feature is ON with no stored row at all.
//   2. stored_value_inverted — for these flags a stored `enabled` means OFF (and a
//      stored `disabled` means ON).
// Counting stored `enabled` rows therefore misses every default-on feature and
// inverts every inverted one. Resolve through here instead.

// Rows in public.feature that are catalog entries but not tenant toggles (TROUBLESHOOT
// anchors a billing dimension). Kept out of both the settings table and the count.
export const HIDDEN_FEATURES = new Set(['TROUBLESHOOT']);

export interface FeatureCatalogEntry {
  value: string;
  polarity?: string;
  stored_value_inverted?: boolean;
}

export interface FeatureOverride {
  feature_id: string;
  status: string;
}

// A flag can store the opposite of its name ('enabled' means OFF); stored_value_inverted marks those.
export const displayedFromStored = (status: string, inverted?: boolean): 'on' | 'off' => {
  const on = status === 'enabled';
  return (inverted ? !on : on) ? 'on' : 'off';
};

// Resolves the tri-state stored choice to what the flag is actually doing, using the catalog's polarity when unset.
export const effectiveFeatureChoice = (feature: { polarity?: string }, choice: string): string =>
  choice === 'default' ? (feature.polarity === 'default_on' ? 'on' : 'off') : choice;

// True when a feature is effectively ON, given its catalog entry and its stored override
// status (undefined when the tenant has no stored row for it). An explicit enabled/disabled
// override wins (respecting inversion); otherwise the catalog's default polarity decides.
export const isFeatureOn = (feature: FeatureCatalogEntry, overrideStatus?: string): boolean => {
  const choice =
    overrideStatus === 'enabled' || overrideStatus === 'disabled' ? displayedFromStored(overrideStatus, feature.stored_value_inverted) : 'default';
  return effectiveFeatureChoice(feature, choice) === 'on';
};

// Count of tenant-wide features effectively ON — matches the toggles the Tenant Settings
// features tab renders as on. `null` when the catalog is unavailable, so the card shows a
// dash rather than a wrong 0.
export const countFeaturesOn = (
  catalog: FeatureCatalogEntry[] | null | undefined,
  overrides: FeatureOverride[] | null | undefined
): number | null => {
  if (!Array.isArray(catalog)) return null;
  const statusByFeature = new Map<string, string>((overrides || []).map((o): [string, string] => [o.feature_id, o.status]));
  return catalog.filter((f) => !HIDDEN_FEATURES.has(f.value) && isFeatureOn(f, statusByFeature.get(f.value))).length;
};
