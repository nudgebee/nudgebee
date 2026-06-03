import React, { useState, useEffect } from 'react';
import Tooltip from '@components1/ds/Tooltip';
import PropTypes from 'prop-types';
import { Box, Stack, Typography, Table, TableHead, TableRow, TableCell, TableBody, CircularProgress } from '@mui/material';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import { Modal } from '@components1/ds/Modal';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { Button } from '@components1/ds/Button';
import { Divider } from '@components1/ds/Divider';
import { toast as snackbar } from '@components1/ds/Toast';
import { ds } from '@utils/colors';
import apiUser from '@api1/user';
import apiIntegrations from '@api1/integrations';
import apiAskNudgebee from '@api1/ask-nudgebee';

const PROVIDERS = ['anthropic', 'azure', 'bedrock', 'googleai', 'huggingface', 'openai', 'sagemaker', 'vertexai'];

const TIER_KEYS = ['reasoning', 'retrieval', 'summary'];

const TIER_LABELS = {
  reasoning: 'Reasoning',
  retrieval: 'Retrieval',
  summary: 'Summary',
};

const TIER_HINTS = {
  reasoning: 'Heavy investigation + critique. Highest-quality model recommended.',
  retrieval: 'Query / search generation. Fast / cheap model recommended.',
  summary: 'Summarisation, memory, acknowledgments. Fast / cheap model recommended.',
};

// Example model names per provider, keyed by tier — drive the placeholder
// text on tier and agent override rows so the hint matches the currently-
// selected provider instead of defaulting to a Gemini example. Each tier
// gets a capability-appropriate example: `reasoning` is the heavy model
// (highest quality, used for investigation/critique), `retrieval` is the
// mid-tier (often a newer preview model that's fast enough for query/search
// generation), `summary` is the light/stable model (cheap and reliable for
// summarisation, memory, acknowledgments).
const PROVIDER_EXAMPLES = {
  anthropic: {
    reasoning: 'claude-opus-4-7',
    retrieval: 'claude-sonnet-4-6',
    summary: 'claude-haiku-4-5',
  },
  azure: {
    reasoning: 'gpt-5-pro',
    retrieval: 'gpt-5',
    summary: 'gpt-5-mini',
  },
  bedrock: {
    reasoning: 'anthropic.claude-opus-4-7',
    retrieval: 'anthropic.claude-sonnet-4-6',
    summary: 'anthropic.claude-haiku-4-5',
  },
  googleai: {
    reasoning: 'gemini-3-pro-preview',
    retrieval: 'gemini-3-flash-preview',
    summary: 'gemini-2.5-flash',
  },
  huggingface: {
    reasoning: 'meta-llama/Llama-3-70b-instruct',
    retrieval: 'meta-llama/Llama-3-13b-instruct',
    summary: 'meta-llama/Llama-3-8b-instruct',
  },
  openai: {
    reasoning: 'gpt-5-pro',
    retrieval: 'gpt-5',
    summary: 'gpt-5-mini',
  },
  sagemaker: {
    reasoning: 'endpoint-name-reasoning',
    retrieval: 'endpoint-name-retrieval',
    summary: 'endpoint-name-summary',
  },
  vertexai: {
    reasoning: 'gemini-3-pro-preview',
    retrieval: 'gemini-3-flash-preview',
    summary: 'gemini-2.5-flash',
  },
};

const emptyConfig = () => ({ model: '', fallbacks: '' });

/**
 * Custom Add/Edit modal for the LLM Configuration. Writes flat config keys
 * into the LLM integration record (no JSON blob):
 *
 *   Global:       llm_provider, llm_model_name, llm_model_fallbacks
 *   Per tier:     llm_tier_provider_<tier>, llm_tier_model_<tier>,
 *                 llm_tier_model_fallbacks_<tier>
 *                 (provider is inherited from global; we still write it
 *                 because the resolver's tier layer requires both
 *                 provider+model to fire)
 *   Per agent:    llm_provider_<agent>, llm_model_name_<agent>,
 *                 llm_model_fallbacks_<agent>
 *
 * The api-server LLM schema needs entries for the tier and per-agent keys
 * for saves to validate — that's a follow-up. The modal renders correctly
 * regardless.
 */

// Password-typed input for secret values (API key, AWS access/secret).
// Deliberately has NO reveal toggle: the backend redacts secret values in
// integrations_list, so the UI never has the stored value to show.
// `isConfigured` reflects whether a secret is already set on the row being
// edited (derived from the per-row `has_value` flag the backend returns —
// see api-server/services/query/metadata.go); when set, the input renders
// a "✓ Configured — leave blank to keep existing" helper so the user
// understands an empty submit means "no change", not "clear". Typing any
// value overrides the existing secret on save.
const SecretInput = ({ label, value = '', onChange, onBlur, isConfigured, helperText, required }) => {
  const isEmpty = value.trim() === '';
  const showConfiguredHint = isConfigured && isEmpty;
  return (
    <Input
      label={label}
      size='sm'
      type='password'
      autoComplete='new-password'
      value={value}
      onChange={onChange}
      onBlur={onBlur}
      placeholder={showConfiguredHint ? '••••••••' : undefined}
      help={
        showConfiguredHint ? (
          <>
            <Box component='span' sx={{ color: 'var(--ds-green-600)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
              ✓
            </Box>{' '}
            Configured — leave blank to keep existing
          </>
        ) : (
          helperText
        )
      }
      // When configured, the field is no longer required at the form-validation
      // level — the stored secret stays. The user can still type a replacement.
      required={required && !isConfigured}
    />
  );
};

SecretInput.propTypes = {
  label: PropTypes.string.isRequired,
  value: PropTypes.string.isRequired,
  onChange: PropTypes.func.isRequired,
  onBlur: PropTypes.func,
  isConfigured: PropTypes.bool,
  helperText: PropTypes.string,
  required: PropTypes.bool,
};

const AddLLMConfigModal = ({ open, onClose, editData, onSaved, accountId }) => {
  const isEdit = !!editData?.id;

  // Account multi-select.
  const [accounts, setAccounts] = useState([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [selectedAccountIds, setSelectedAccountIds] = useState([]);

  // Agent list — fetched dynamically from llm-server's registered agents so
  // the dropdown stays in sync without enumerating agents in Go or
  // hardcoding them in the frontend.
  const [knownAgents, setKnownAgents] = useState([]);
  const [agentsLoading, setAgentsLoading] = useState(false);

  // Global fields.
  const [configName, setConfigName] = useState('');
  const [provider, setProvider] = useState('');
  const [model, setModel] = useState('');
  const [fallbacks, setFallbacks] = useState('');

  // Provider-specific credentials (shape mirrors integrations/llm.go schema —
  // see ShowWhen/RequiredWhen rules there for each field's provider mapping).
  const [apiKey, setApiKey] = useState('');
  const [apiEndpoint, setApiEndpoint] = useState('');
  const [apiVersion, setApiVersion] = useState('');
  const [region, setRegion] = useState('');
  const [accessKey, setAccessKey] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [apiType, setApiType] = useState('');
  const [adapterId, setAdapterId] = useState('');
  const [requireAdapterId, setRequireAdapterId] = useState('');

  // Track which secret fields are currently configured on the loaded
  // integration, keyed by field name. The backend redacts secret values in
  // integrations_list (returns value='' for matching field names) and
  // includes a per-row `has_value` boolean — that's what populates this map.
  // For backward compatibility with an older list response where `has_value`
  // is missing, we fall back to "value is a non-empty string".
  //
  // Used for two things only:
  //   1. Form validation: a configured secret satisfies its required-field
  //      check even when the input is blank (omit-to-keep).
  //   2. Field hint: SecretInput shows "✓ Configured — leave blank to keep"
  //      when the field is configured and the input is empty.
  //
  // The actual secret value never enters this map. The UI cannot decrypt or
  // reveal stored credentials at any time.
  const [secretsConfigured, setSecretsConfigured] = useState({});

  // Per-tier fields (just model + fallbacks; provider inherited from global).
  const [tiers, setTiers] = useState({
    reasoning: emptyConfig(),
    retrieval: emptyConfig(),
    summary: emptyConfig(),
  });

  // Per-agent overrides — array of { agent, model, fallbacks } rows.
  const [agentRows, setAgentRows] = useState([]);

  // initialOverrideKeys captures every llm_tier_* and llm_*_<agent> override
  // key that was present in the integration when the modal was opened. On
  // save we diff this against what's still in the form (tiers, agentRows) and
  // emit an empty value for any key that's been cleared — the backend
  // interprets empty non-secret LLM keys as DELETE so the row goes away. Without
  // this, clearing a tier model and saving would silently keep the stored row.
  const [initialOverrideKeys, setInitialOverrideKeys] = useState(new Set());

  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  // testStatus gates Save on a successful Test Connection. State machine:
  //   'idle'    — initial state for new integrations; also reset when any
  //               connection-relevant field (provider/model/secrets/endpoint
  //               /region) changes
  //   'pending' — Test is running
  //   'passed'  — last Test succeeded for the current field values
  //   'failed'  — last Test failed for the current field values
  //
  // Save is allowed only when testStatus === 'passed'. On edit, the initial
  // state bootstraps to 'passed' if the loaded integration's healthStatus
  // is CONNECTED — the user shouldn't be forced to re-test just to rename
  // an already-working config. Any conn-field edit drops it back to 'idle'.
  const [testStatus, setTestStatus] = useState('idle');
  const [testMessage, setTestMessage] = useState('');

  // Load the cloud-account list once when the modal opens.
  useEffect(() => {
    if (!open) {
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        setAccountsLoading(true);
        const res = await apiUser.listAccounts();
        if (cancelled) {
          return;
        }
        if (!Array.isArray(res)) {
          // listAccounts has historically returned [] on error rather than
          // throwing. Surface the empty-on-failure case so the operator
          // notices rather than seeing a silently empty dropdown.
          snackbar.error('Failed to load cloud accounts');
          return;
        }
        setAccounts(res.map((a) => ({ id: a.id, name: a.account_name })));
      } catch (err) {
        if (!cancelled) {
          // eslint-disable-next-line no-console
          console.error('AddLLMConfigModal: listAccounts failed', err);
          snackbar.error('Failed to load cloud accounts');
        }
      } finally {
        if (!cancelled) {
          setAccountsLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  // Fetch the dynamic agent list from llm-server (via ai_list_agents) when
  // the modal opens. The agent registry lives in llm-server, so this avoids
  // hardcoding any agent names on the frontend or in api-server.
  useEffect(() => {
    if (!open || !accountId) {
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        setAgentsLoading(true);
        const res = await apiAskNudgebee.listAgents({ accountId });
        // Fail-closed on GraphQL errors so the operator notices a partial
        // failure and doesn't end up with an empty agent-override dropdown.
        const gqlErrors = res?.data?.errors;
        if (Array.isArray(gqlErrors) && gqlErrors.length > 0) {
          if (!cancelled) {
            const msg = gqlErrors[0]?.message || 'Failed to load agent list';
            snackbar.error(msg);
          }
          return;
        }
        const raw = res?.data?.data?.ai_list_agents?.data || [];
        const items = raw
          // Match useAgentConfiguration.ts — only surface agents that are
          // currently enabled. Disabled agents won't be invoked even if
          // configured, so they shouldn't appear in the override dropdown.
          .filter((a) => a?.status === 'enabled')
          .map((a) => {
            const key = a?.aliases?.[0] ?? a?.name;
            if (!key) {
              return null;
            }
            return { key, label: a?.name || key, description: a?.description || '' };
          })
          .filter(Boolean);
        if (!cancelled) {
          setKnownAgents(items);
        }
      } catch (err) {
        if (!cancelled) {
          // eslint-disable-next-line no-console
          console.error('AddLLMConfigModal: listAgents failed', err);
          snackbar.error('Failed to load agent list');
        }
      } finally {
        if (!cancelled) {
          setAgentsLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, accountId]);

  // Reset / pre-fill the form whenever the modal opens or editData changes.
  useEffect(() => {
    if (!open) {
      return;
    }
    if (isEdit && editData) {
      // Build a name→value lookup from the integration's config rows. Also
      // track per-row `has_value` so secret fields (which the backend
      // redacts to value='') can still be rendered as "Configured".
      const cfg = {};
      const hasValueByName = {};
      const values = editData.integration_config_values;
      if (Array.isArray(values)) {
        values.forEach((v) => {
          if (v && v.name) {
            cfg[v.name] = v.value;
            // Prefer the backend-supplied has_value flag (post-redaction
            // contract). Fall back to "value is non-empty" so the modal
            // still works against an older list response that didn't yet
            // include has_value.
            hasValueByName[v.name] = typeof v.has_value === 'boolean' ? v.has_value : typeof v.value === 'string' && v.value !== '';
          }
        });
      } else if (values && typeof values === 'object') {
        Object.assign(cfg, values);
      }

      setSelectedAccountIds(
        Array.isArray(editData.integrations_cloud_accounts)
          ? editData.integrations_cloud_accounts.map((a) => a?.cloud_account_id).filter(Boolean)
          : []
      );
      setConfigName(editData.name || '');
      setProvider(cfg.llm_provider || '');
      setModel(cfg.llm_model_name || '');
      setFallbacks(cfg.llm_model_fallbacks || '');
      // Secret fields: keep the form input blank. The stored value is never
      // surfaced — the SecretInput renders "✓ Configured" when blank +
      // secretsConfigured[name] is true. User-typed values override on save.
      setApiKey('');
      setApiEndpoint(cfg.llm_provider_api_endpoint || '');
      setApiVersion(cfg.llm_provider_api_version || '');
      setRegion(cfg.llm_provider_region || '');
      setAccessKey('');
      setSecretKey('');
      setApiType(cfg.llm_provider_api_type || '');
      setAdapterId(cfg.llm_provider_adapter_id || '');
      setRequireAdapterId(cfg.llm_provider_require_adapter_id || '');
      setSecretsConfigured({
        llm_provider_api_key: !!hasValueByName.llm_provider_api_key,
        llm_provider_access_key: !!hasValueByName.llm_provider_access_key,
        llm_provider_secret_key: !!hasValueByName.llm_provider_secret_key,
      });

      setTiers({
        reasoning: {
          model: cfg.llm_tier_model_reasoning || '',
          fallbacks: cfg.llm_tier_model_fallbacks_reasoning || '',
        },
        retrieval: {
          model: cfg.llm_tier_model_retrieval || '',
          fallbacks: cfg.llm_tier_model_fallbacks_retrieval || '',
        },
        summary: {
          model: cfg.llm_tier_model_summary || '',
          fallbacks: cfg.llm_tier_model_fallbacks_summary || '',
        },
      });

      // Reconstruct agent rows by scanning for any llm_model_name_<agent> keys.
      const recoveredAgents = [];
      Object.keys(cfg).forEach((key) => {
        if (key.startsWith('llm_model_name_') && key !== 'llm_model_name') {
          const agentKey = key.slice('llm_model_name_'.length);
          // Skip the legacy summary_agent block — that's its own thing.
          if (agentKey === 'summary_agent') {
            return;
          }
          recoveredAgents.push({
            agent: agentKey,
            model: cfg[key] || '',
            fallbacks: cfg[`llm_model_fallbacks_${agentKey}`] || '',
          });
        }
      });
      setAgentRows(recoveredAgents);

      // Snapshot the set of tier / agent override keys present at load time
      // so we can detect cleared/removed ones at save time (see
      // buildConfigValues — those get emitted with empty value and the
      // backend DELETEs the row).
      //
      // Secret-shaped keys are excluded — they follow omit-to-keep semantics
      // and must never be emitted with value="". The prefix check must NOT
      // require a trailing underscore: the GLOBAL `llm_provider_api_key` is
      // just as much a secret as the per-agent `llm_provider_api_key_<agent>`.
      // Without the broader prefix match, the global key sneaks into the seed
      // set, gets emitted with value="" at save/test time, and the backend's
      // RequiredWhen validator rejects with
      // `llm_provider_api_key is required when llm_provider is "googleai"`
      // even though the stored secret is still valid.
      const isLLMSecretKey = (key) =>
        key.startsWith('llm_provider_api_key') ||
        key.startsWith('llm_provider_access_key') ||
        key.startsWith('llm_provider_secret_key') ||
        key.startsWith('llm_provider_session_token');
      const seedKeys = new Set();
      Object.keys(cfg).forEach((key) => {
        if (isLLMSecretKey(key)) {
          return;
        }
        if (
          key.startsWith('llm_tier_') ||
          (key.startsWith('llm_provider_') && key !== 'llm_provider') ||
          (key.startsWith('llm_model_name_') && key !== 'llm_model_name') ||
          (key.startsWith('llm_model_fallbacks_') && key !== 'llm_model_fallbacks')
        ) {
          seedKeys.add(key);
        }
      });
      setInitialOverrideKeys(seedKeys);

      // Bootstrap testStatus from the loaded integration's last health
      // probe. Only the explicit CONNECTED healthStatus counts — the
      // earlier `status === 'enabled'` fallback was too permissive:
      // `status === 'enabled'` only means the integration row is active
      // in the registry, it does NOT imply the last live probe passed.
      // An integration whose key was just rotated externally / quota
      // revoked / provider 5xx'ing would still come back enabled, and
      // bootstrapping testStatus to 'passed' there would let the user
      // re-save a broken config without re-testing.
      //
      // Any subsequent conn-field edit drops this back to 'idle' via
      // setConnField (see below).
      const wasConnected = editData?.healthStatus === 'CONNECTED' || editData?.health_status === 'CONNECTED';
      setTestStatus(wasConnected ? 'passed' : 'idle');
      setTestMessage('');
    } else {
      setSelectedAccountIds([]);
      setConfigName('');
      setProvider('');
      setModel('');
      setFallbacks('');
      setApiKey('');
      setApiEndpoint('');
      setApiVersion('');
      setRegion('');
      setAccessKey('');
      setSecretKey('');
      setApiType('');
      setAdapterId('');
      setRequireAdapterId('');
      setSecretsConfigured({});
      setInitialOverrideKeys(new Set());
      setTiers({
        reasoning: emptyConfig(),
        retrieval: emptyConfig(),
        summary: emptyConfig(),
      });
      setAgentRows([]);
      setTestStatus('idle');
      setTestMessage('');
    }
  }, [open, isEdit, editData]);

  // setConnField wraps a state setter for a connection-relevant field so any
  // user edit resets testStatus to 'idle' — Save is then re-blocked until the
  // user runs Test Connection again. The conn-relevant fields are: provider,
  // model, all secrets (apiKey/accessKey/secretKey), endpoint, region, version,
  // api type. Fallbacks / tier overrides / agent overrides / config name don't
  // affect connectivity and use the raw setters.
  //
  // Intentionally does NOT trim. Aggressive onChange-time trim would make
  // typing a space mid-edit impossible. Visible-trim happens on onBlur via
  // `trimOnBlur` below; save-time trim is handled by buildConfigValues
  // (`value.trim()`).
  const setConnField = (setter) => (value) => {
    setter(value);
    setTestStatus('idle');
    setTestMessage('');
  };

  // trimOnBlur returns an onBlur handler that strips leading/trailing
  // whitespace from the field's current value and writes back via setter
  // only when the trimmed value differs (avoids redundant re-renders). Used
  // for fields where pasted whitespace is almost always noise — secrets,
  // endpoint URLs, region codes. The save-time trim in buildConfigValues is
  // the real guarantee; this onBlur is the visible feedback so the user can
  // see "yes, the trailing whitespace I pasted was cleaned" before they
  // click Test.
  const trimOnBlur = (current, setter) => () => {
    const trimmed = (current || '').replace(/^\s+|\s+$/g, '');
    if (trimmed !== current) {
      setter(trimmed);
    }
  };

  // Provider-change handler: in addition to resetting testStatus (via
  // setConnField), clear all dependent state slots because they're
  // provider-specific. Switching openai → bedrock should not carry the openai
  // API key into the now-hidden bedrock access/secret slots, and certainly
  // should not let them ride into buildConfigValues() if the user later flips
  // back. The model and fallbacks are also wiped because a model name from one
  // provider is virtually never valid for another.
  const handleProviderChange = (value) => {
    setProvider(value);
    setModel('');
    setFallbacks('');
    setApiKey('');
    setApiEndpoint('');
    setApiVersion('');
    setRegion('');
    setAccessKey('');
    setSecretKey('');
    setApiType('');
    setAdapterId('');
    setRequireAdapterId('');
    // Reset the "✓ Configured" indicators too. Without this, an edit-flow
    // provider switch (e.g. openai → azure) would leave secretsConfigured
    // populated for the OLD provider's secret fields and the save-gate
    // would treat the new provider's blank credentials as already
    // configured, letting the form submit without re-entry.
    setSecretsConfigured({});
    // Category-tier and per-agent overrides reference provider-specific
    // model names (e.g. gemini-3-flash-preview, claude-3-5-sonnet,
    // gpt-4o). They are invalid for the new provider, so clear both so
    // the user is forced to pick fresh models if they want to keep
    // tier/agent overrides. initialOverrideKeys is intentionally kept
    // intact — the save-diff against it will then emit empty values for
    // every loaded override key, which the backend interprets as DELETE,
    // cleaning up the stale rows from the old provider.
    setTiers({
      reasoning: emptyConfig(),
      retrieval: emptyConfig(),
      summary: emptyConfig(),
    });
    setAgentRows([]);
    setTestStatus('idle');
    setTestMessage('');
  };

  // Tier and agent override edits change the set of (provider, model) pairs
  // the next Test Connection will probe. Any mutation here must therefore
  // reset testStatus to 'idle' so the user is forced to re-run Test before
  // Save re-enables — otherwise a "passed" status from a prior config could
  // carry over even after the user typed a new tier/agent model that was
  // never probed. Same invariant as setConnField for the global fields.
  const updateTier = (tier, field, value) => {
    setTiers((prev) => ({ ...prev, [tier]: { ...prev[tier], [field]: value } }));
    setTestStatus('idle');
    setTestMessage('');
  };

  const updateAgentRow = (idx, field, value) => {
    setAgentRows((prev) => prev.map((row, i) => (i === idx ? { ...row, [field]: value } : row)));
    setTestStatus('idle');
    setTestMessage('');
  };

  const removeAgentRow = (idx) => {
    setAgentRows((prev) => prev.filter((_, i) => i !== idx));
    // Removing a row shrinks the probe set; testStatus stays valid in the
    // sense that the previously-passed superset still covers the smaller
    // set, but we conservatively reset so the user re-confirms the
    // smaller config explicitly. Cheap to re-test.
    setTestStatus('idle');
    setTestMessage('');
  };

  const addAgentRow = () => {
    // Adding an empty row doesn't change the probe set until the user
    // fills it in (validation would prevent Save anyway), but we reset
    // testStatus eagerly so the footer status line flips to "Run Test
    // Connection before saving" immediately and the user isn't confused
    // by a stale "Connection verified" hint.
    setAgentRows((prev) => [...prev, { agent: '', model: '', fallbacks: '' }]);
    setTestStatus('idle');
    setTestMessage('');
  };

  // Agents already chosen — used to disable them in other rows' agent dropdowns.
  const usedAgents = new Set(agentRows.map((r) => r.agent).filter(Boolean));

  // Provider-aware example model names for tier/agent override placeholders.
  // Falls back to a neutral hint if the provider isn't in PROVIDER_EXAMPLES.
  const providerExample = PROVIDER_EXAMPLES[provider] || {
    reasoning: 'model name',
    retrieval: 'model name',
    summary: 'model name',
  };

  // Conditional credential visibility — mirrors integrations/llm.go ShowWhen rules.
  const showsApiKey = ['anthropic', 'azure', 'googleai', 'huggingface', 'openai', 'vertexai'].includes(provider);
  const showsApiEndpoint = ['azure', 'openai', 'sagemaker', 'anthropic', 'huggingface'].includes(provider);
  const showsApiVersion = provider === 'azure';
  const showsRegion = ['bedrock', 'sagemaker'].includes(provider);
  const showsBedrockKeys = provider === 'bedrock';
  const showsApiType = provider === 'openai';
  const showsAdapter = ['azure', 'huggingface'].includes(provider);

  // A secret field counts as "present" if either the user typed a non-empty
  // value OR an existing stored secret was reported by the backend
  // (secretsConfigured[name] = true). The form lets Edit-without-touching-
  // fields proceed because the backend's omit-to-keep semantics will
  // preserve the stored value when the field isn't included in the payload.
  const hasSecret = (current, configuredKey) => current.trim() !== '' || !!secretsConfigured[configuredKey];
  const credsReady =
    (!showsApiKey || hasSecret(apiKey, 'llm_provider_api_key')) &&
    (!showsBedrockKeys || (hasSecret(accessKey, 'llm_provider_access_key') && hasSecret(secretKey, 'llm_provider_secret_key')));

  // validateModelName: shape check for any model field.
  //   - non-empty after trim (the "field is required" case is the caller's
  //     responsibility; this only fires when value is non-empty)
  //   - no internal whitespace (no real provider model name has spaces in it)
  // Returns null when valid, an error message otherwise.
  const validateModelName = (value) => {
    if (!value || !value.trim()) {
      return null;
    }
    if (/\s/.test(value.trim())) {
      return 'Model name cannot contain spaces';
    }
    return null;
  };

  // validateFallbacks: shape check for any fallback-list field.
  //   - if empty, that's fine — fallbacks are optional
  //   - must be a comma-separated list
  //   - each token must be non-empty after trim and have no internal spaces
  //   - no duplicate tokens within the list
  //   - no token equal to the primary model (would be a no-op)
  const validateFallbacks = (value, primary) => {
    const trimmed = (value || '').trim();
    if (trimmed === '') {
      return null;
    }
    const tokens = trimmed.split(',').map((t) => t.trim());
    if (tokens.some((t) => t === '')) {
      return 'Fallbacks list has an empty entry — remove trailing/extra commas';
    }
    if (tokens.some((t) => /\s/.test(t))) {
      return 'Fallback model names cannot contain spaces';
    }
    const seen = new Set();
    for (const t of tokens) {
      if (seen.has(t)) {
        return `Fallbacks list has a duplicate entry: ${t}`;
      }
      seen.add(t);
    }
    if (primary && tokens.includes(primary.trim())) {
      return 'Fallbacks list cannot include the primary model';
    }
    return null;
  };

  // Per-field validation messages. Computed once per render so the helperText
  // / Save-gate can read them without recomputing.
  const errors = {
    accounts: selectedAccountIds.length === 0 ? 'At least one account must be selected' : null,
    model: validateModelName(model),
    fallbacks: validateFallbacks(fallbacks, model),
    tierModels: {
      reasoning: validateModelName(tiers.reasoning.model),
      retrieval: validateModelName(tiers.retrieval.model),
      summary: validateModelName(tiers.summary.model),
    },
    tierFallbacks: {
      reasoning: validateFallbacks(tiers.reasoning.fallbacks, tiers.reasoning.model || model),
      retrieval: validateFallbacks(tiers.retrieval.fallbacks, tiers.retrieval.model || model),
      summary: validateFallbacks(tiers.summary.fallbacks, tiers.summary.model || model),
    },
    agentRows: agentRows.map((row) => ({
      model: row.model ? validateModelName(row.model) : null,
      fallbacks: validateFallbacks(row.fallbacks, row.model || model),
    })),
  };
  const hasAnyError =
    !!errors.accounts ||
    !!errors.model ||
    !!errors.fallbacks ||
    Object.values(errors.tierModels).some(Boolean) ||
    Object.values(errors.tierFallbacks).some(Boolean) ||
    errors.agentRows.some((r) => r.model || r.fallbacks);

  const formComplete = configName.trim() !== '' && provider !== '' && model.trim() !== '' && credsReady && !hasAnyError;
  // canTest is a less-strict gate — Save needs Test to have passed, but Test
  // itself only needs the form to be filled in. Without this separation the
  // user would be stuck in a chicken-and-egg state on a new integration:
  // Test disabled because of testStatus !== 'passed', Save also disabled, no
  // way to make progress.
  const canTest = formComplete;
  const canSubmit = formComplete && testStatus === 'passed';

  const buildConfigValues = () => {
    const out = [
      { name: 'llm_provider', value: provider },
      { name: 'llm_model_name', value: model.trim() },
    ];
    if (fallbacks.trim()) {
      out.push({ name: 'llm_model_fallbacks', value: fallbacks.trim() });
    }
    // Provider-specific credential values — only write fields that apply to
    // the chosen provider so we don't pollute the config blob with empty keys
    // from previously-selected providers.
    //
    // Secret-field semantics (omit-to-keep): when the user typed a non-empty
    // value, push it as plaintext with is_encrypted=false. The backend
    // encrypts on save when the schema declares IsEncrypted=true (see
    // api-server/services/integrations/llm.go). When the field is blank, we
    // OMIT it from the payload entirely — the backend's
    // CreateIntegrationConfig save loop skips the upsert for an LLM
    // integration's secret field when the value is missing, preserving the
    // existing stored row intact. The UI never has the stored value to
    // re-send, and we don't want to round-trip ciphertext through the
    // browser.
    const pushPlain = (cond, name, value) => {
      if (cond && value && value.trim() !== '') {
        out.push({ name, value: value.trim(), is_encrypted: false });
      }
    };
    const pushSecret = (cond, name, value) => {
      // Omit-to-keep: empty + show-condition means "user didn't change it",
      // omit entirely so the backend preserves the stored secret. A typed
      // value goes as plaintext; the backend's IsEncrypted=true schema flag
      // triggers common.Encrypt before the INSERT.
      //
      // Null-guard on value: React state always inits these to '' so in
      // normal flow value is a string, but a future refactor could pass an
      // undefined (e.g. an uncontrolled field) and value.trim() would throw
      // before the omit-empty branch runs.
      if (!cond || !value || value.trim() === '') {
        return;
      }
      out.push({ name, value: value.trim(), is_encrypted: false });
    };
    pushSecret(showsApiKey, 'llm_provider_api_key', apiKey);
    pushPlain(showsApiEndpoint, 'llm_provider_api_endpoint', apiEndpoint);
    pushPlain(showsApiVersion, 'llm_provider_api_version', apiVersion);
    pushPlain(showsRegion, 'llm_provider_region', region);
    pushSecret(showsBedrockKeys, 'llm_provider_access_key', accessKey);
    pushSecret(showsBedrockKeys, 'llm_provider_secret_key', secretKey);
    pushPlain(showsApiType, 'llm_provider_api_type', apiType);
    pushPlain(showsAdapter, 'llm_provider_adapter_id', adapterId);
    pushPlain(showsAdapter, 'llm_provider_require_adapter_id', requireAdapterId);
    // Per-tier — write provider + model + fallbacks. Provider mirrors global
    // because the resolver requires both provider and model to fire the tier
    // layer; we don't ask the user for it.
    TIER_KEYS.forEach((tier) => {
      const t = tiers[tier];
      if (t.model.trim() === '') {
        return;
      }
      out.push({ name: `llm_tier_provider_${tier}`, value: provider });
      out.push({ name: `llm_tier_model_${tier}`, value: t.model.trim() });
      if (t.fallbacks.trim()) {
        out.push({ name: `llm_tier_model_fallbacks_${tier}`, value: t.fallbacks.trim() });
      }
    });
    // Per-agent — same reasoning: provider mirrors global so the resolver's
    // env-agent / db-agent layer fires.
    agentRows.forEach((row) => {
      if (!row.agent || row.model.trim() === '') {
        return;
      }
      out.push({ name: `llm_provider_${row.agent}`, value: provider });
      out.push({ name: `llm_model_name_${row.agent}`, value: row.model.trim() });
      if (row.fallbacks.trim()) {
        out.push({ name: `llm_model_fallbacks_${row.agent}`, value: row.fallbacks.trim() });
      }
    });
    // Emit explicit empty values for tier / agent override keys that were
    // present when the modal opened but are no longer in `out`. This signals
    // DELETE on the backend save path so cleared tier models / removed
    // agent rows don't linger in the DB and reappear on next edit.
    const currentNames = new Set(out.map((v) => v.name));
    initialOverrideKeys.forEach((name) => {
      if (!currentNames.has(name)) {
        out.push({ name, value: '', is_encrypted: false });
      }
    });
    return out;
  };

  const handleTest = async () => {
    if (!canTest) {
      return;
    }
    setTesting(true);
    setTestStatus('pending');
    setTestMessage('');
    try {
      // Always use the by-config path so the probe reflects what the user has
      // currently typed, not the stored DB row. On edit we pass
      // `editData.id` so the backend can augment any blank secret fields
      // with the stored (encrypted) values before probing — that's how
      // omit-to-keep on secrets stays compatible with "Test before Save".
      //
      // The previous by-id branch was the bug your edit-flow Test was hitting:
      // editing the model name and clicking Test would still verify the
      // stored model name, masking your typo.
      const result = await apiIntegrations.testIntegrationConnectionByConfig(
        'llm',
        selectedAccountIds,
        buildConfigValues(),
        editData?.source || 'user',
        isEdit ? editData?.id : undefined
      );
      if (result?.success) {
        setTestStatus('passed');
        setTestMessage(result.message || 'Connection successful');
        snackbar.success(result.message || 'Connection successful');
      } else {
        const errMsg = result?.error || result?.message || 'Connection failed';
        setTestStatus('failed');
        setTestMessage(errMsg);
        snackbar.error(errMsg);
      }
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('AddLLMConfigModal: testConnection threw', err);
      setTestStatus('failed');
      setTestMessage('Failed to test connection');
      snackbar.error('Failed to test connection');
    } finally {
      setTesting(false);
    }
  };

  const handleSave = async () => {
    if (!canSubmit) {
      return;
    }
    setSaving(true);
    try {
      const payload = {
        ...(isEdit && editData?.id && { integration_id: editData.id }),
        integration_name: 'llm',
        integration_config_name: configName.trim(),
        account_ids: selectedAccountIds,
        source: editData?.source || 'user',
        integration_config_values: buildConfigValues(),
      };
      const response = await apiIntegrations.addIntegrations(payload);
      // GraphQL errors are returned at response.data.errors (axios wraps the
      // HTTP body in response.data; the body itself has the GraphQL-spec
      // `data` and `errors` keys). The previous check looked at
      // response.errors which is always undefined, so every backend error —
      // including useful validation messages like
      // "account 'X' already has a 'llm' integration" — surfaced as the
      // generic "Failed to save LLM Provider" toast.
      const gqlErrors = response?.data?.errors;
      if (Array.isArray(gqlErrors) && gqlErrors.length > 0) {
        // eslint-disable-next-line no-console
        console.error('AddLLMConfigModal: save error', response);
        snackbar.error(gqlErrors[0]?.message || 'Failed to save LLM Provider');
        return;
      }
      const configs = response?.data?.data?.integrations_create_config?.configs || [];
      if (configs.length === 0) {
        // eslint-disable-next-line no-console
        console.error('AddLLMConfigModal: save returned empty configs', response);
        snackbar.error('Failed to save LLM Provider');
        return;
      }
      snackbar.success(isEdit ? 'LLM Provider updated' : 'LLM Provider added');
      if (onSaved) {
        onSaved();
      }
      onClose();
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('AddLLMConfigModal: save threw', err);
      snackbar.error('Failed to save LLM configuration');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title={isEdit ? 'Edit LLM Provider' : 'Add LLM Provider'} width='md'>
      <Box>
        {/* ---------- Global section ---------- */}
        <Stack spacing='var(--ds-space-4)'>
          <Select
            multiple
            size='sm'
            loading={accountsLoading}
            options={accounts.map((a) => ({ value: a.id, label: a.name || a.id }))}
            value={selectedAccountIds}
            onChange={setSelectedAccountIds}
            label='Accounts'
            required
            error={errors.accounts}
            help='At least one account must be selected. Auto-populated from listAccounts. The configuration applies to all selected accounts.'
          />

          <Input
            label='Integration Config Name'
            size='sm'
            value={configName}
            onChange={setConfigName}
            help='Unique name to identify this integration configuration.'
            required
          />

          <Select
            label='LLM Provider'
            size='sm'
            value={provider}
            onChange={handleProviderChange}
            options={PROVIDERS}
            help='Name of the LLM provider (openai, bedrock, sagemaker, huggingface, azure, googleai, vertexai, anthropic). Changing the provider clears model and credential fields.'
            required
          />

          <Input
            label='LLM Model Name'
            size='sm'
            value={model}
            onChange={setConnField(setModel)}
            onBlur={trimOnBlur(model, setModel)}
            error={errors.model}
            help='Name of the primary model (e.g., gpt-4, claude-opus-4-7, gemini-3.1-pro-preview).'
            required
          />

          <Input
            label='LLM Model Fallbacks'
            size='sm'
            value={fallbacks}
            onChange={setConnField(setFallbacks)}
            onBlur={trimOnBlur(fallbacks, setFallbacks)}
            error={errors.fallbacks}
            help='Comma-separated list of fallback model names tried in order when the primary fails. Optional.'
          />

          {/* Provider-specific credentials — visibility driven by selected provider */}
          {showsApiKey && (
            <SecretInput
              label='API Key *'
              value={apiKey}
              onChange={setConnField(setApiKey)}
              onBlur={trimOnBlur(apiKey, setApiKey)}
              isConfigured={secretsConfigured.llm_provider_api_key}
              helperText='API key for authenticating with the LLM provider. Surrounding whitespace is trimmed when you tab out of the field.'
              required
            />
          )}
          {showsApiEndpoint && (
            <Input
              label='API Endpoint'
              size='sm'
              value={apiEndpoint}
              onChange={setConnField(setApiEndpoint)}
              onBlur={trimOnBlur(apiEndpoint, setApiEndpoint)}
              help='Custom API endpoint for the LLM provider.'
              required={['azure', 'sagemaker', 'huggingface', 'anthropic'].includes(provider)}
            />
          )}
          {showsApiVersion && (
            <Input
              label='API Version'
              size='sm'
              value={apiVersion}
              onChange={setConnField(setApiVersion)}
              help='API version of the LLM provider (Azure).'
              required
            />
          )}
          {showsRegion && (
            <Input
              label='Region'
              size='sm'
              value={region}
              onChange={setConnField(setRegion)}
              onBlur={trimOnBlur(region, setRegion)}
              help='Geographic region (e.g., us-east-1).'
              required
            />
          )}
          {showsBedrockKeys && (
            <>
              <SecretInput
                label='AWS Access Key *'
                value={accessKey}
                onChange={setConnField(setAccessKey)}
                onBlur={trimOnBlur(accessKey, setAccessKey)}
                isConfigured={secretsConfigured.llm_provider_access_key}
                helperText='AWS Access Key ID for Bedrock. Surrounding whitespace is trimmed when you tab out of the field.'
                required
              />
              <SecretInput
                label='AWS Secret Key *'
                value={secretKey}
                onChange={setConnField(setSecretKey)}
                onBlur={trimOnBlur(secretKey, setSecretKey)}
                isConfigured={secretsConfigured.llm_provider_secret_key}
                helperText='AWS Secret Access Key for Bedrock. Surrounding whitespace is trimmed when you tab out of the field.'
                required
              />
            </>
          )}
          {showsApiType && <Input label='API Type' size='sm' value={apiType} onChange={setConnField(setApiType)} help='Type of the API. Optional.' />}
          {showsAdapter && (
            <>
              <Input label='Adapter ID' size='sm' value={adapterId} onChange={setAdapterId} help='Adapter ID for a fine-tuned model. Optional.' />
              <Input
                label='Require Adapter ID'
                size='sm'
                value={requireAdapterId}
                onChange={setRequireAdapterId}
                help='Whether an adapter ID is required.'
              />
            </>
          )}
        </Stack>

        <Divider />

        {/* ---------- Categories ---------- */}
        <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 'var(--ds-space-2)' }}>
          Categories
        </Typography>
        <Typography variant='caption' sx={{ display: 'block', mb: 'var(--ds-space-3)', color: 'var(--ds-gray-500)' }}>
          Per-category model overrides. Leave a row blank to inherit the global model above.
        </Typography>
        <Table size='small' sx={{ mb: 'var(--ds-space-4)' }}>
          <TableHead>
            <TableRow>
              <TableCell sx={{ width: ds.space.mul(4, 9), fontWeight: 'var(--ds-font-weight-semibold)' }}>Category</TableCell>
              <TableCell sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>Model</TableCell>
              <TableCell sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>Fallbacks</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {TIER_KEYS.map((tierKey) => (
              <TableRow key={tierKey}>
                <TableCell>
                  <Tooltip title={TIER_HINTS[tierKey]} placement='top'>
                    <Box sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>{TIER_LABELS[tierKey]}</Box>
                  </Tooltip>
                </TableCell>
                <TableCell>
                  <Input
                    size='sm'
                    value={tiers[tierKey].model}
                    placeholder={model || `e.g. ${providerExample[tierKey]}`}
                    onChange={(value) => updateTier(tierKey, 'model', value)}
                    error={errors.tierModels[tierKey]}
                  />
                </TableCell>
                <TableCell>
                  <Input
                    size='sm'
                    value={tiers[tierKey].fallbacks}
                    placeholder='comma-separated'
                    onChange={(value) => updateTier(tierKey, 'fallbacks', value)}
                    error={errors.tierFallbacks[tierKey]}
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>

        <Divider />

        {/* ---------- Agent Overrides ---------- */}
        <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 'var(--ds-space-2)' }}>
          Agent Overrides
        </Typography>
        <Typography variant='caption' sx={{ display: 'block', mb: 'var(--ds-space-3)', color: 'var(--ds-gray-500)' }}>
          For specific agents that need a different model than their category default. Provider is inherited from the global setting above.
        </Typography>

        {agentRows.length === 0 ? (
          <Box
            sx={{
              p: 'var(--ds-space-4)',
              textAlign: 'center',
              border: '1px dashed',
              borderColor: 'var(--ds-gray-300)',
              borderRadius: 'var(--ds-radius-sm)',
              mb: 'var(--ds-space-4)',
              color: 'var(--ds-gray-500)',
              fontSize: 'var(--ds-text-body)',
            }}
          >
            No agent overrides configured.
          </Box>
        ) : (
          <Table size='small' sx={{ mb: 'var(--ds-space-4)' }}>
            <TableHead>
              <TableRow>
                <TableCell sx={{ width: ds.space.mul(4, 14), fontWeight: 'var(--ds-font-weight-semibold)' }}>Agent</TableCell>
                <TableCell sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>Model</TableCell>
                <TableCell sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>Fallbacks</TableCell>
                <TableCell sx={{ width: ds.space.mul(2, 6), fontWeight: 'var(--ds-font-weight-semibold)' }}></TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {agentRows.map((row, idx) => (
                <TableRow key={`agent-row-${idx}`}>
                  <TableCell>
                    <Select
                      size='sm'
                      value={row.agent}
                      onChange={(value) => updateAgentRow(idx, 'agent', value)}
                      disabled={agentsLoading}
                      placeholder={agentsLoading ? '(loading agents…)' : '(select agent)'}
                      options={knownAgents.map((a) => ({
                        value: a.key,
                        label: a.label,
                        disabled: a.key !== row.agent && usedAgents.has(a.key),
                      }))}
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      size='sm'
                      value={row.model}
                      placeholder={`e.g. ${providerExample.reasoning}`}
                      onChange={(value) => updateAgentRow(idx, 'model', value)}
                      error={errors.agentRows[idx]?.model}
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      size='sm'
                      value={row.fallbacks}
                      placeholder='comma-separated'
                      onChange={(value) => updateAgentRow(idx, 'fallbacks', value)}
                      error={errors.agentRows[idx]?.fallbacks}
                    />
                  </TableCell>
                  <TableCell>
                    <Button
                      tone='ghost'
                      size='sm'
                      icon={<DeleteOutlineIcon />}
                      onClick={() => removeAgentRow(idx)}
                      data-testid={`remove-agent-row-${idx}`}
                      aria-label='Remove row'
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        <Button id='add-agent-override-row-btn' tone='secondary' size='md' onClick={addAgentRow}>
          + Add agent override
        </Button>

        {/* ---------- Footer ---------- */}
        {/*
          Layout matches the reference integration modals (PagerDuty / ServiceNow):
          right-aligned button group [Cancel] [Test Connection] [Save]. A small
          inline status indicator sits to the left, showing the testStatus state
          so the user can see at a glance why Save is or isn't enabled.

          Save is gated on testStatus === 'passed' (see canSubmit above). The
          gate is bootstrapped to 'passed' on edit of a CONNECTED integration so
          label-only edits don't force a re-test; any conn-field edit drops it
          back to 'idle' via setConnField.
        */}
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--ds-space-4)', mt: 'var(--ds-space-5)' }}>
          <Box sx={{ flex: 1, minHeight: ds.space.mul(2, 3), fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>
            {hasAnyError && (
              <Box component='span' sx={{ color: 'var(--ds-red-600)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                ✗ Fix the highlighted fields above before testing.
              </Box>
            )}
            {!hasAnyError && testStatus === 'passed' && (
              <Box component='span' sx={{ color: 'var(--ds-green-600)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                ✓ Connection verified — Save is enabled
              </Box>
            )}
            {!hasAnyError && testStatus === 'failed' && (
              // whiteSpace: 'pre-line' so the multi-line `\n  - ` bullets
              // built by buildAggregateProbeError in api-server render as
              // separate lines instead of collapsing to a single run-on
              // string. The aggregate-error work in the backend is wasted
              // without this — multiple failures would otherwise be
              // unreadable.
              <Box
                component='span'
                sx={{ color: 'var(--ds-red-600)', fontWeight: 'var(--ds-font-weight-semibold)', whiteSpace: 'pre-line', display: 'block' }}
              >
                ✗ {testMessage || 'Connection test failed — fix the configuration and re-test'}
              </Box>
            )}
            {!hasAnyError && testStatus === 'idle' && formComplete && <Box component='span'>Run Test Connection before saving.</Box>}
            {!hasAnyError && testStatus === 'pending' && <Box component='span'>Testing connection…</Box>}
          </Box>
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
            <Button id='add-llm-config-cancel-btn' tone='secondary' size='md' onClick={onClose} disabled={saving}>
              Cancel
            </Button>
            <Button
              id='add-llm-config-test-btn'
              tone='secondary'
              size='md'
              onClick={handleTest}
              disabled={!canTest || saving || testing}
              loading={testing}
            >
              {testing ? 'Testing…' : 'Test Connection'}
            </Button>
            <Button
              id='add-llm-config-save-btn'
              tone='primary'
              size='md'
              onClick={handleSave}
              disabled={!canSubmit || saving || testing}
              loading={saving}
            >
              {saving ? 'Saving…' : isEdit ? 'Save' : 'Add LLM Provider'}
            </Button>
          </Box>
        </Box>
      </Box>
      {accountsLoading && (
        <Box sx={{ position: 'absolute', top: ds.space[2], right: ds.space.mul(2, 7) }}>
          <CircularProgress size={16} />
        </Box>
      )}
    </Modal>
  );
};

AddLLMConfigModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  editData: PropTypes.object,
  onSaved: PropTypes.func,
  // Used to scope the ai_list_agents fetch for the per-agent override dropdown.
  accountId: PropTypes.string,
};

export default AddLLMConfigModal;
