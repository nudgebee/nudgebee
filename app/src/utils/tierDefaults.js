// Tier-default computation for the LLM config modal (#35174).
//
// Users historically defaulted every category tier (reasoning / retrieval /
// summary) to the same expensive primary model because the modal shipped with
// blank tier fields. That silently inflates cost — retrieval and summary calls
// don't need the flagship. When the user picks a provider, we now pre-fill the
// three tier fields with a sensible split so the cheap tiers are cheap by
// default; the user can override any field before saving.
//
// The map source is llm_model_pricing (per Shiv/Sundar's call): we derive the
// split from actual per-provider pricing rows rather than maintaining a
// separate curated family map. Fallback: an EXAMPLE_MODELS map curated in
// AddLLMConfigModal for providers whose pricing rows aren't loaded (fresh
// tenant, provider without any pricing entries).

// Sort by output-token cost ascending. Output cost dominates the bill for
// generation-heavy calls, and the reasoning tier is the only one that heavily
// generates — so cost_per_million_output_tokens is the right sort key. Rows
// with no output cost are treated as "unknown, sort last" to avoid picking a
// model with unmeasured pricing as the summary default.
// Rank by explicit comparison rather than subtraction — avoids the classic
// Infinity - Infinity = NaN result when both rows are unpriced (which would
// leave sort order engine-dependent). Array elements populated by our own
// dedup can't be null, so no optional chaining on `a` / `b`.
const byOutputCostAsc = (a, b) => {
  const av = Number.isFinite(a.cost_per_million_output_tokens) ? a.cost_per_million_output_tokens : Infinity;
  const bv = Number.isFinite(b.cost_per_million_output_tokens) ? b.cost_per_million_output_tokens : Infinity;
  if (av < bv) return -1;
  if (av > bv) return 1;
  return 0;
};

// When the provider has multiple tenant-scoped rows for the same model (a
// tenant override on a built-in), the tenant row wins — matches how the
// server's own resolver picks. `is_built_in` may be undefined on rows that
// predate the flag; treat missing as built-in (only an explicit `false`
// counts as a tenant override).
const preferTenantRow = (a, b) => Number(a?.is_built_in !== false ? 1 : 0) - Number(b?.is_built_in !== false ? 1 : 0);

// Collapse duplicate model_name rows down to the tenant-preferred one before
// ranking. Without this dedup the price ranking can list the same model twice
// (once as built-in, once as override) and tier assignment gets skewed.
const uniqueByModel = (rows) => {
  const seen = new Map();
  for (const row of rows) {
    if (!row?.model_name) continue;
    const existing = seen.get(row.model_name);
    if (!existing || preferTenantRow(row, existing) < 0) {
      seen.set(row.model_name, row);
    }
  }
  return [...seen.values()];
};

// Return a {reasoning, retrieval, summary} object of model_name strings to
// pre-fill into empty tier fields. `provider` is the provider slug the user
// just selected. `pricingRows` is the flat array from apiAskNudgebee.listModelPricing().
// `fallbackMap` is the pre-curated EXAMPLE_MODELS map keyed by provider; used
// only when pricingRows has nothing for this provider.
export const computeTierDefaults = (provider, pricingRows, fallbackMap) => {
  const empty = { reasoning: '', retrieval: '', summary: '' };
  if (!provider) return empty;

  const forProvider = uniqueByModel((pricingRows || []).filter((r) => r?.provider_name === provider));

  if (forProvider.length === 0) {
    const fallback = fallbackMap?.[provider];
    return fallback ? { reasoning: fallback.reasoning || '', retrieval: fallback.retrieval || '', summary: fallback.summary || '' } : empty;
  }

  const sorted = forProvider.slice().sort(byOutputCostAsc);

  // Only one model priced for this provider — every tier gets the same model.
  // Better than leaving two blanks and a filled one.
  if (sorted.length === 1) {
    const only = sorted[0].model_name;
    return { reasoning: only, retrieval: only, summary: only };
  }
  // Two models — cheapest handles both summary and retrieval; the pricier one
  // is reasoning. Retrieval leans toward the cheap side because it's usually
  // short-prompt / short-response.
  if (sorted.length === 2) {
    return { summary: sorted[0].model_name, retrieval: sorted[0].model_name, reasoning: sorted[1].model_name };
  }
  // Three or more — cheapest → summary, most-expensive → reasoning, the
  // middle-most row → retrieval. Middle by index gives a stable pick even
  // when several models share a middle price.
  const cheapest = sorted[0].model_name;
  const priciest = sorted[sorted.length - 1].model_name;
  const middle = sorted[Math.floor(sorted.length / 2)].model_name;
  return { summary: cheapest, retrieval: middle, reasoning: priciest };
};
