// Tier-default computation for the LLM config modal (#35174, #41118).
//
// Users historically defaulted every category tier (reasoning / retrieval /
// summary) to the same expensive primary model because the modal shipped with
// blank tier fields. That silently inflates cost — retrieval and summary calls
// don't need the flagship. When the user picks a provider, we pre-fill the
// three tier fields with a sensible split so the cheap tiers are cheap by
// default; the user can override any field before saving.
//
// The source is a curated map of current chat models per provider
// (PROVIDER_DEFAULT_MODELS in AddLLMConfigModal). It used to be derived from
// llm_model_pricing, but that table exists to price past usage: it keeps
// retired models, non-API names and $0-output embedding models, so the
// pre-filled models failed Test Connection and blocked Save (#41118).

// Return a {reasoning, retrieval, summary} object of model_name strings to
// pre-fill into the tier fields. `provider` is the provider slug the user just
// selected; `defaultsMap` is the curated map keyed by provider. Providers not
// in the map (model names are deployment- or endpoint-specific) get blanks.
export const computeTierDefaults = (provider, defaultsMap) => {
  const defaults = (provider && defaultsMap?.[provider]) || {};
  return { reasoning: defaults.reasoning || '', retrieval: defaults.retrieval || '', summary: defaults.summary || '' };
};
