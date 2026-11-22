import { computeTierDefaults } from '@utils/tierDefaults';

const DEFAULTS = {
  openai: { reasoning: 'gpt-5.5-pro', retrieval: 'gpt-5.5', summary: 'gpt-5.4-mini' },
  partial: { reasoning: 'only-reasoning' },
};

describe('computeTierDefaults', () => {
  it('returns empty tiers when provider is falsy', () => {
    expect(computeTierDefaults('', DEFAULTS)).toEqual({ reasoning: '', retrieval: '', summary: '' });
    expect(computeTierDefaults(undefined, DEFAULTS)).toEqual({ reasoning: '', retrieval: '', summary: '' });
  });

  it('returns the curated models for the provider', () => {
    expect(computeTierDefaults('openai', DEFAULTS)).toEqual({ reasoning: 'gpt-5.5-pro', retrieval: 'gpt-5.5', summary: 'gpt-5.4-mini' });
  });

  it('returns empty tiers for a provider without curated defaults', () => {
    expect(computeTierDefaults('sagemaker', DEFAULTS)).toEqual({ reasoning: '', retrieval: '', summary: '' });
    expect(computeTierDefaults('openai', undefined)).toEqual({ reasoning: '', retrieval: '', summary: '' });
  });

  it('blanks tiers missing from a partial entry', () => {
    expect(computeTierDefaults('partial', DEFAULTS)).toEqual({ reasoning: 'only-reasoning', retrieval: '', summary: '' });
  });
});
