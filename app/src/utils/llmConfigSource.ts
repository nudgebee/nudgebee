/**
 * Helpers for `llm_config_source` — the id that pins a request to one
 * configured LLM slot. Shared because the schema is a cross-service contract
 * (llm-server's `parseConfigSourceId`), so a second copy is a second place for
 * it to drift.
 *
 * Schema: `{layer}:{scope}[:{name}]`
 *
 *   env:global                       the operator's default slot
 *   env:tier:<t>                     t = reasoning | retrieval | summary
 *   env:agent:<a>
 *   env:all                          the whole ENV config
 *   db:<uuid>                        an integration's base slot
 *   db:<uuid>:tier:<t> | :agent:<a>
 *   db:<uuid>:all                    the whole integration
 *
 * `:all` is the only scope that does not name a single slot: it names a config
 * and lets each call's tier pick the slot inside it.
 */

/** Every slot of one integration shares a key; every env slot shares 'env'. */
export const configKeyForSource = (source?: string): string => {
  if (!source) return '';
  if (source.startsWith('db:')) {
    const parts = source.split(':');
    return parts.length >= 2 ? `db:${parts[1]}` : source;
  }
  return 'env';
};

/** The whole-config pin for a config key. */
export const wholeConfigSourceFor = (configKey: string): string => (configKey === 'env' ? 'env:all' : `${configKey}:all`);

/**
 * The config's own base slot. Identified by equality rather than by "has no
 * `:tier:`" — an agent slot has no `:tier:` either and would otherwise be
 * mistaken for the base.
 */
export const baseConfigSourceFor = (configKey: string): string => (configKey === 'env' ? 'env:global' : configKey);

/** A whole-config pin names a config rather than a slot. */
export const isWholeConfigSource = (source?: string): boolean => typeof source === 'string' && source.endsWith(':all');
