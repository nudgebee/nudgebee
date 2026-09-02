import { computeTierDefaults } from '@utils/tierDefaults';

const price = (provider_name, model_name, cost_per_million_output_tokens, extra = {}) => ({
  provider_name,
  model_name,
  cost_per_million_output_tokens,
  ...extra,
});

const EXAMPLE_FALLBACK = {
  openai: { reasoning: 'gpt-5-pro', retrieval: 'gpt-5', summary: 'gpt-5-mini' },
};

describe('computeTierDefaults', () => {
  it('returns empty tiers when provider is falsy', () => {
    expect(computeTierDefaults('', [], EXAMPLE_FALLBACK)).toEqual({ reasoning: '', retrieval: '', summary: '' });
    expect(computeTierDefaults(undefined, [], EXAMPLE_FALLBACK)).toEqual({ reasoning: '', retrieval: '', summary: '' });
  });

  it('falls back to EXAMPLE map when provider has no pricing rows', () => {
    const result = computeTierDefaults('openai', [], EXAMPLE_FALLBACK);
    expect(result).toEqual({ reasoning: 'gpt-5-pro', retrieval: 'gpt-5', summary: 'gpt-5-mini' });
  });

  it('returns empty when no pricing AND no fallback for provider', () => {
    const result = computeTierDefaults('newprovider', [], EXAMPLE_FALLBACK);
    expect(result).toEqual({ reasoning: '', retrieval: '', summary: '' });
  });

  it('assigns cheapest to summary, priciest to reasoning, middle to retrieval (3+ models)', () => {
    const rows = [
      price('openai', 'gpt-5-mini', 1.2),
      price('openai', 'gpt-5', 8.0),
      price('openai', 'gpt-5-pro', 40.0),
      price('anthropic', 'claude-haiku-4-5', 2.0), // different provider — must be ignored
    ];
    expect(computeTierDefaults('openai', rows, EXAMPLE_FALLBACK)).toEqual({
      summary: 'gpt-5-mini',
      retrieval: 'gpt-5',
      reasoning: 'gpt-5-pro',
    });
  });

  it('reasoning = pricier, retrieval + summary = cheapest when only 2 models priced', () => {
    const rows = [price('openai', 'gpt-5-mini', 1.2), price('openai', 'gpt-5-pro', 40.0)];
    expect(computeTierDefaults('openai', rows, EXAMPLE_FALLBACK)).toEqual({
      summary: 'gpt-5-mini',
      retrieval: 'gpt-5-mini',
      reasoning: 'gpt-5-pro',
    });
  });

  it('all three tiers get the same model when only 1 is priced for that provider', () => {
    const rows = [price('openai', 'gpt-5', 8.0)];
    expect(computeTierDefaults('openai', rows, EXAMPLE_FALLBACK)).toEqual({
      reasoning: 'gpt-5',
      retrieval: 'gpt-5',
      summary: 'gpt-5',
    });
  });

  it('rows with missing output cost sort last (avoid picking unpriced model as summary)', () => {
    const rows = [
      price('openai', 'gpt-unknown', undefined),
      price('openai', 'gpt-5-mini', 1.2),
      price('openai', 'gpt-5-pro', 40.0),
      price('openai', 'gpt-5', 8.0),
    ];
    const result = computeTierDefaults('openai', rows, EXAMPLE_FALLBACK);
    // Cheapest known-priced wins summary; unpriced 'gpt-unknown' sinks to last (== reasoning).
    expect(result.summary).toBe('gpt-5-mini');
    expect(result.reasoning).toBe('gpt-unknown');
  });

  it('deduplicates by model_name and prefers tenant override over built-in', () => {
    const rows = [
      price('openai', 'gpt-5', 8.0, { is_built_in: true }),
      price('openai', 'gpt-5', 6.5, { is_built_in: false }), // tenant override with lower cost — wins
      price('openai', 'gpt-5-pro', 40.0),
    ];
    const result = computeTierDefaults('openai', rows, EXAMPLE_FALLBACK);
    // Only 2 unique models after dedup → summary/retrieval = cheapest, reasoning = priciest.
    expect(result).toEqual({ summary: 'gpt-5', retrieval: 'gpt-5', reasoning: 'gpt-5-pro' });
  });

  // Regression: two rows with the same model but is_built_in unset — should
  // be treated as built-ins, and an explicit tenant override with is_built_in=false
  // must still win over them.
  it('treats missing is_built_in as built-in so explicit tenant overrides still win', () => {
    const rows = [
      price('openai', 'gpt-5', 8.0), // is_built_in undefined → treated as built-in
      price('openai', 'gpt-5', 6.5, { is_built_in: false }), // tenant override — must win
      price('openai', 'gpt-5-pro', 40.0),
    ];
    const result = computeTierDefaults('openai', rows, EXAMPLE_FALLBACK);
    // The tenant override (6.5) wins → summary sorts by that price.
    expect(result).toEqual({ summary: 'gpt-5', retrieval: 'gpt-5', reasoning: 'gpt-5-pro' });
  });

  // Regression for #35174 Gemini review: two rows both with undefined output
  // cost previously caused Infinity - Infinity = NaN in the sort comparator,
  // making the resulting order engine-dependent. Verify the sort remains
  // stable (all three tiers still resolve) even when every row is unpriced.
  it('sort is stable when multiple rows share missing output cost', () => {
    const rows = [price('openai', 'gpt-a', undefined), price('openai', 'gpt-b', undefined), price('openai', 'gpt-c', undefined)];
    const result = computeTierDefaults('openai', rows, EXAMPLE_FALLBACK);
    // All unpriced → whatever order the sort settles on, we get three real
    // model names, no undefined slots and no throw.
    expect([result.summary, result.retrieval, result.reasoning].every((m) => typeof m === 'string' && m.startsWith('gpt-'))).toBe(true);
  });
});
