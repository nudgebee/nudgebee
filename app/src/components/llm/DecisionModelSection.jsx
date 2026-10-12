import React from 'react';
import PropTypes from 'prop-types';
import { Box, Stack, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Banner } from '@ui/Banner';

// Decision models (#40902) answer fixed-choice AI decisions and report their
// confidence. They live in the integration's "decision" slot
// (llm_tier_*_decision), which is never offered for chat.
export const DECISION_PROVIDER = 'cloudflare';

const PROVIDER_OPTIONS = [
  { value: '', label: 'None' },
  { value: DECISION_PROVIDER, label: 'Cloudflare Workers AI' },
];

const MODEL_OPTIONS = [
  { value: 'clef', label: 'clef (recommended)' },
  { value: 'clef-flash', label: 'clef-flash (cheaper)' },
];

export const DEFAULT_MIN_CONFIDENCE = '0.9';

export const emptyDecisionState = () => ({
  provider: '',
  model: 'clef',
  endpoint: '',
  apiKey: '',
  minConfidence: DEFAULT_MIN_CONFIDENCE,
});

// decisionStateFromConfig reads the saved keys into form state.
export const decisionStateFromConfig = (cfg) => {
  const state = emptyDecisionState();
  if (!cfg) {
    return state;
  }
  state.provider = cfg.llm_tier_provider_decision || '';
  state.model = cfg.llm_tier_model_decision || state.model;
  state.endpoint = cfg.llm_tier_api_endpoint_decision || '';
  state.minConfidence = cfg.llm_tier_min_confidence_decision || DEFAULT_MIN_CONFIDENCE;
  return state;
};

const validThreshold = (raw) => {
  const n = Number(raw);
  return raw !== '' && Number.isFinite(n) && n > 0 && n <= 1;
};

// validateDecisionState returns field errors; an empty object means valid.
export const validateDecisionState = (state, apiKeyConfigured) => {
  const errors = {};
  if (!state.provider) {
    return errors;
  }
  if (!state.model) {
    errors.model = 'Model is required';
  }
  if (!state.endpoint.trim()) {
    errors.endpoint = 'Cloudflare account ID is required';
  }
  if (!state.apiKey.trim() && !apiKeyConfigured) {
    errors.apiKey = 'API token is required';
  }
  if (!validThreshold(String(state.minConfidence).trim())) {
    errors.minConfidence = 'Enter a number above 0 and at most 1';
  }
  return errors;
};

// decisionConfigValues returns the integration config values to save.
export const decisionConfigValues = (state) => {
  if (!state.provider) {
    return [];
  }
  const out = [
    { name: 'llm_tier_provider_decision', value: state.provider },
    { name: 'llm_tier_model_decision', value: state.model },
    { name: 'llm_tier_api_endpoint_decision', value: state.endpoint.trim() },
    { name: 'llm_tier_min_confidence_decision', value: String(state.minConfidence).trim() },
  ];
  if (state.apiKey.trim()) {
    out.push({ name: 'llm_tier_api_key_decision', value: state.apiKey.trim(), is_encrypted: false });
  }
  return out;
};

const DecisionModelSection = ({ state, errors = {}, apiKeyConfigured = false, onChange, renderSecretInput }) => (
  <Box>
    <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 'var(--ds-space-2)' }}>
      Decision models (beta)
    </Typography>
    <Typography variant='caption' sx={{ display: 'block', mb: 'var(--ds-space-3)', color: 'var(--ds-gray-500)' }}>
      A fast model for simple fixed choices, such as what the automation builder should do with a message. When it is less sure than the minimum
      confidence, your LLM decides as usual. It is never used for chat.
    </Typography>
    <Stack spacing='var(--ds-space-2)'>
      <Select
        id='llm-decision-provider'
        label='Decision model provider'
        size='sm'
        value={state.provider}
        onChange={(v) => onChange('provider', v)}
        options={PROVIDER_OPTIONS}
      />
      {state.provider && (
        <>
          <Banner
            tone='warning'
            surface='section'
            message='User messages are sent to this provider for these choices. Check its data-retention terms before saving.'
          />
          <Select
            id='llm-decision-model'
            label='Model'
            size='sm'
            required
            value={state.model}
            onChange={(v) => onChange('model', v)}
            options={MODEL_OPTIONS}
            error={errors.model}
          />
          <Input
            id='llm-decision-account-id'
            label='Cloudflare account ID'
            size='sm'
            required
            value={state.endpoint}
            onChange={(v) => onChange('endpoint', v)}
            error={errors.endpoint}
          />
          {renderSecretInput({
            label: 'API token',
            value: state.apiKey,
            onChange: (v) => onChange('apiKey', v),
            isConfigured: apiKeyConfigured,
            required: true,
            helperText: errors.apiKey,
          })}
          <Box sx={{ maxWidth: 240 }}>
            <Input
              id='llm-decision-min-confidence'
              label='Minimum confidence'
              size='sm'
              inputMode='decimal'
              value={state.minConfidence}
              onChange={(v) => onChange('minConfidence', v)}
              error={errors.minConfidence}
              help='0 to 1. Below this, the LLM decides.'
            />
          </Box>
        </>
      )}
    </Stack>
  </Box>
);

DecisionModelSection.propTypes = {
  state: PropTypes.shape({
    provider: PropTypes.string,
    model: PropTypes.string,
    endpoint: PropTypes.string,
    apiKey: PropTypes.string,
    minConfidence: PropTypes.string,
  }).isRequired,
  errors: PropTypes.object,
  apiKeyConfigured: PropTypes.bool,
  onChange: PropTypes.func.isRequired,
  renderSecretInput: PropTypes.func.isRequired,
};

export default DecisionModelSection;
