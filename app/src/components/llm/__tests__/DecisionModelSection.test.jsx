import React from 'react';
import { render, screen } from '@testing-library/react';
import DecisionModelSection, {
  DECISION_PROVIDER,
  DEFAULT_MIN_CONFIDENCE,
  decisionConfigValues,
  decisionStateFromConfig,
  emptyDecisionState,
  validateDecisionState,
} from '../DecisionModelSection';

const configured = () => ({
  ...emptyDecisionState(),
  provider: DECISION_PROVIDER,
  model: 'clef-flash',
  endpoint: 'acct',
  apiKey: 'token',
});

describe('decision model settings', () => {
  it('reads saved keys into form state', () => {
    const state = decisionStateFromConfig({
      llm_tier_provider_decision: DECISION_PROVIDER,
      llm_tier_model_decision: 'clef',
      llm_tier_api_endpoint_decision: 'acct',
      llm_tier_min_confidence_decision: '0.85',
    });
    expect(state).toMatchObject({ provider: DECISION_PROVIDER, model: 'clef', endpoint: 'acct', minConfidence: '0.85' });
  });

  it('fills the default minimum confidence', () => {
    expect(decisionStateFromConfig({}).minConfidence).toBe(DEFAULT_MIN_CONFIDENCE);
    expect(decisionStateFromConfig(null)).toEqual(emptyDecisionState());
  });

  it('writes nothing when no decision model is chosen', () => {
    expect(decisionConfigValues(emptyDecisionState())).toEqual([]);
  });

  it('writes the slot with its minimum confidence', () => {
    const values = Object.fromEntries(decisionConfigValues({ ...configured(), minConfidence: '0.8' }).map((v) => [v.name, v.value]));
    expect(values).toEqual({
      llm_tier_provider_decision: DECISION_PROVIDER,
      llm_tier_model_decision: 'clef-flash',
      llm_tier_api_endpoint_decision: 'acct',
      llm_tier_min_confidence_decision: '0.8',
      llm_tier_api_key_decision: 'token',
    });
  });

  it('keeps a stored token when the field is left blank', () => {
    const state = { ...configured(), apiKey: '' };
    expect(decisionConfigValues(state).map((v) => v.name)).not.toContain('llm_tier_api_key_decision');
    expect(validateDecisionState(state, true)).toEqual({});
    expect(validateDecisionState(state, false).apiKey).toBeTruthy();
  });

  it('rejects a minimum confidence outside (0, 1]', () => {
    expect(validateDecisionState({ ...configured(), minConfidence: '1.5' }, false).minConfidence).toBeTruthy();
    expect(validateDecisionState({ ...configured(), minConfidence: '' }, false).minConfidence).toBeTruthy();
    expect(validateDecisionState({ ...configured(), minConfidence: '0.75' }, false)).toEqual({});
  });

  it('shows provider fields only once a provider is chosen', () => {
    const props = { errors: {}, onChange: jest.fn(), renderSecretInput: () => <input aria-label='API token' /> };
    const { rerender } = render(<DecisionModelSection state={emptyDecisionState()} {...props} />);
    expect(screen.queryByLabelText('API token')).toBeNull();
    expect(screen.queryByText(/data-retention terms/)).toBeNull();
    rerender(<DecisionModelSection state={configured()} {...props} />);
    expect(screen.getByLabelText('API token')).toBeTruthy();
    expect(screen.getByText(/data-retention terms/)).toBeTruthy();
  });
});
