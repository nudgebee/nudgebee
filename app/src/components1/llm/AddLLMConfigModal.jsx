import React, { useState, useEffect } from 'react';
import Tooltip from '@components1/ds/Tooltip';
import PropTypes from 'prop-types';
import { Box, Stack, Typography, CircularProgress } from '@mui/material';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ExpandLessIcon from '@mui/icons-material/ExpandLess';
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

// Per-tier and per-agent override shape. provider === '' means "inherit from
// global"; any other value triggers conditional credential fields for that
// provider. Secret fields (apiKey / accessKey / secretKey) follow the same
// omit-to-keep contract as the global secrets — see SecretInput + buildConfigValues.
//
// providerOverrideOpen controls a per-card collapse for the Provider +
// credential inputs. When false, the card shows only Model + Fallbacks and a
// "+ Override provider" affordance; when true, the override pane is rendered.
// Opens on demand, and the render block also auto-treats it open if the row
// has a saved provider override on edit-mode load — so users never hit a
// "where did my setting go" moment.
const emptyConfig = () => ({
  model: '',
  fallbacks: '',
  provider: '',
  apiKey: '',
  apiEndpoint: '',
  apiVersion: '',
  apiType: '',
  region: '',
  accessKey: '',
  secretKey: '',
  providerOverrideOpen: false,
});

// Sentinel value used by the per-tier and per-agent Provider dropdowns to
// represent "Inherit from global". The shared DS Select treats value === '' as
// "no selection" and renders the placeholder, so an empty-string option label
// would never display. State still stores '' for inherit — the sentinel only
// lives at the Select-binding boundary.
const INHERIT_SENTINEL = '__inherit__';

// providerFieldShape returns which credential inputs apply for a given provider.
// Mirrors the global section's showsApiKey / showsApiEndpoint / ... booleans so
// the tier and agent cards can render the same provider-conditional inputs.
const providerFieldShape = (p) => ({
  showsApiKey: ['anthropic', 'azure', 'googleai', 'huggingface', 'openai', 'vertexai'].includes(p),
  showsApiEndpoint: ['azure', 'openai', 'sagemaker', 'anthropic', 'huggingface'].includes(p),
  showsApiVersion: p === 'azure',
  showsRegion: ['bedrock', 'sagemaker'].includes(p),
  showsBedrockKeys: p === 'bedrock',
  showsApiType: ['openai', 'huggingface'].includes(p),
});

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

  // Per-agent overrides — array of { agent, model, fallbacks, provider, apiKey,
  // apiEndpoint, apiVersion, apiType, region, accessKey, secretKey, collapsed }
  // rows. provider === '' = inherit from global; collapsed controls the card
  // expand/collapse state.
  const [agentRows, setAgentRows] = useState([]);

  // Free-text filter applied to agent override cards. Only shown when there
  // are more than 3 rows — at small counts the filter just adds noise.
  const [agentFilter, setAgentFilter] = useState('');

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
      // Capture every secret-shaped key (global, per-tier, per-agent) so the
      // tier and agent cards can render "✓ Configured" hints on their own
      // SecretInputs after edit-mode load. The set keys here mirror the names
      // we read in buildConfigValues / pushSecret below.
      const secretsCfg = {
        llm_provider_api_key: !!hasValueByName.llm_provider_api_key,
        llm_provider_access_key: !!hasValueByName.llm_provider_access_key,
        llm_provider_secret_key: !!hasValueByName.llm_provider_secret_key,
      };
      ['reasoning', 'retrieval', 'summary'].forEach((t) => {
        secretsCfg[`llm_tier_api_key_${t}`] = !!hasValueByName[`llm_tier_api_key_${t}`];
        secretsCfg[`llm_tier_access_key_${t}`] = !!hasValueByName[`llm_tier_access_key_${t}`];
        secretsCfg[`llm_tier_secret_key_${t}`] = !!hasValueByName[`llm_tier_secret_key_${t}`];
      });
      Object.keys(hasValueByName).forEach((k) => {
        if (k.startsWith('llm_provider_api_key_') || k.startsWith('llm_provider_access_key_') || k.startsWith('llm_provider_secret_key_')) {
          secretsCfg[k] = !!hasValueByName[k];
        }
      });
      setSecretsConfigured(secretsCfg);

      // Per-tier load. A tier's provider field is "Inherit" (empty in form
      // state) when the saved llm_tier_provider_<tier> matches the global
      // llm_provider — that's the legacy "provider mirrors global" write we
      // emit when the user hasn't overridden the provider. A different saved
      // provider means an explicit per-tier provider override; populate it.
      const globalProvider = cfg.llm_provider || '';
      const tierFor = (t) => {
        const savedProvider = cfg[`llm_tier_provider_${t}`] || '';
        // Inherit when empty or equal to global — so existing tenants that
        // saved tier_provider = global_provider don't surface a stale override.
        const isInherit = savedProvider === '' || savedProvider === globalProvider;
        return {
          model: cfg[`llm_tier_model_${t}`] || '',
          fallbacks: cfg[`llm_tier_model_fallbacks_${t}`] || '',
          provider: isInherit ? '' : savedProvider,
          apiKey: '', // secret — always blank in form; secretsConfigured tracks "✓"
          apiEndpoint: cfg[`llm_tier_api_endpoint_${t}`] || '',
          apiVersion: cfg[`llm_tier_api_version_${t}`] || '',
          apiType: cfg[`llm_tier_api_type_${t}`] || '',
          region: cfg[`llm_tier_region_${t}`] || '',
          accessKey: '',
          secretKey: '',
          // Auto-open the override pane on load when a non-global provider
          // was saved — otherwise the user would think their setting vanished.
          providerOverrideOpen: !isInherit,
        };
      };
      setTiers({
        reasoning: tierFor('reasoning'),
        retrieval: tierFor('retrieval'),
        summary: tierFor('summary'),
      });

      // Reconstruct agent rows by scanning for any llm_model_name_<agent> keys.
      // Same inherit-when-matches-global semantics as tiers above.
      const recoveredAgents = [];
      Object.keys(cfg).forEach((key) => {
        if (key.startsWith('llm_model_name_') && key !== 'llm_model_name') {
          const agentKey = key.slice('llm_model_name_'.length);
          // Skip the legacy summary_agent block — that's its own thing.
          if (agentKey === 'summary_agent') {
            return;
          }
          const savedProvider = cfg[`llm_provider_${agentKey}`] || '';
          const isInherit = savedProvider === '' || savedProvider === globalProvider;
          recoveredAgents.push({
            agent: agentKey,
            model: cfg[key] || '',
            fallbacks: cfg[`llm_model_fallbacks_${agentKey}`] || '',
            provider: isInherit ? '' : savedProvider,
            apiKey: '',
            apiEndpoint: cfg[`llm_provider_api_endpoint_${agentKey}`] || '',
            apiVersion: cfg[`llm_provider_api_version_${agentKey}`] || '',
            apiType: cfg[`llm_provider_api_type_${agentKey}`] || '',
            region: cfg[`llm_provider_region_${agentKey}`] || '',
            accessKey: '',
            secretKey: '',
            collapsed: true, // default to collapsed on load; expand on user click
            // Open the inner override pane when a saved override exists, so
            // users see their setting immediately on expanding the card.
            providerOverrideOpen: !isInherit,
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
  // Reset every credential field when the override provider changes — old
  // values are for a different provider's auth scheme and would mislead the
  // user (a stale Bedrock access_key sitting around after switching to
  // GoogleAI). Save-side tombstoning still purges any DB row that was loaded.
  const credResetForProvider = {
    apiKey: '',
    apiEndpoint: '',
    apiVersion: '',
    apiType: '',
    region: '',
    accessKey: '',
    secretKey: '',
  };
  const updateTier = (tier, field, value) => {
    setTiers((prev) => ({
      ...prev,
      [tier]: {
        ...prev[tier],
        [field]: value,
        ...(field === 'provider' ? credResetForProvider : {}),
      },
    }));
    setTestStatus('idle');
    setTestMessage('');
  };

  const updateAgentRow = (idx, field, value) => {
    setAgentRows((prev) =>
      prev.map((row, i) =>
        i === idx
          ? {
              ...row,
              [field]: value,
              ...(field === 'provider' ? credResetForProvider : {}),
            }
          : row
      )
    );
    setTestStatus('idle');
    setTestMessage('');
  };

  // Revert handlers — close the override pane, clear the provider + creds so
  // the row goes back to inheriting from global. Save-side tombstoning then
  // clears any stale DB row.
  const revertTierOverride = (tier) => {
    setTiers((prev) => ({
      ...prev,
      [tier]: {
        ...prev[tier],
        provider: '',
        ...credResetForProvider,
        providerOverrideOpen: false,
      },
    }));
    setTestStatus('idle');
    setTestMessage('');
  };

  const revertAgentOverride = (idx) => {
    setAgentRows((prev) =>
      prev.map((row, i) =>
        i === idx
          ? {
              ...row,
              provider: '',
              ...credResetForProvider,
              providerOverrideOpen: false,
            }
          : row
      )
    );
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
    setAgentRows((prev) => [
      ...prev,
      {
        agent: '',
        model: '',
        fallbacks: '',
        provider: '',
        apiKey: '',
        apiEndpoint: '',
        apiVersion: '',
        apiType: '',
        region: '',
        accessKey: '',
        secretKey: '',
        collapsed: false, // newly added row starts expanded so the user can fill it in
        providerOverrideOpen: false, // override pane is opt-in
      },
    ]);
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
  const showsApiType = ['openai', 'huggingface'].includes(provider);
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

  // credKeyFor builds the scope-qualified secret name (e.g.
  // 'llm_tier_api_key_reasoning', 'llm_provider_api_key_aws_debug') so
  // secretsConfigured lookups match the load-time seed.
  const validateOverrideCreds = (row, credKeyFor) => {
    if (!row.provider) {
      return null;
    }
    const shape = providerFieldShape(row.provider);
    if (shape.showsApiKey && !hasSecret(row.apiKey || '', credKeyFor('api_key'))) {
      return 'API Key is required for the selected provider';
    }
    if (shape.showsBedrockKeys) {
      if (!hasSecret(row.accessKey || '', credKeyFor('access_key'))) {
        return 'AWS Access Key is required for Bedrock';
      }
      if (!hasSecret(row.secretKey || '', credKeyFor('secret_key'))) {
        return 'AWS Secret Key is required for Bedrock';
      }
    }
    return null;
  };

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
  //
  // tierProviderModel and agentProviderModel guard the half-set case: setting
  // a tier/agent provider override without a matching model would write
  // llm_tier_provider_<tier> with no llm_tier_model_<tier>, which the backend
  // resolver silently no-ops with a "half-set" Warn log. Easier to catch at
  // form time than to diagnose a missing override at runtime.
  //
  // tierCreds and agentCreds gate Save when a row has a non-global provider
  // override but is missing the credentials that provider requires. Without
  // this gate the user could Save a Bedrock-tier override with no AWS keys
  // and only discover the misconfig on the next runtime call's 401.
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
    tierProviderModel: {
      reasoning: tiers.reasoning.provider && !tiers.reasoning.model.trim() ? 'Model is required when an override Provider is set' : null,
      retrieval: tiers.retrieval.provider && !tiers.retrieval.model.trim() ? 'Model is required when an override Provider is set' : null,
      summary: tiers.summary.provider && !tiers.summary.model.trim() ? 'Model is required when an override Provider is set' : null,
    },
    tierCreds: {
      reasoning: validateOverrideCreds(tiers.reasoning, (c) => `llm_tier_${c}_reasoning`),
      retrieval: validateOverrideCreds(tiers.retrieval, (c) => `llm_tier_${c}_retrieval`),
      summary: validateOverrideCreds(tiers.summary, (c) => `llm_tier_${c}_summary`),
    },
    agentRows: agentRows.map((row) => ({
      model: row.model ? validateModelName(row.model) : null,
      fallbacks: validateFallbacks(row.fallbacks, row.model || model),
      providerModel: row.provider && !row.model.trim() ? 'Model is required when an override Provider is set' : null,
      // Cred validation gates on provider, not agent name. When the user
      // hasn't picked an agent yet, the secretsConfigured lookup misses on
      // the empty suffix, so any required secret defaults to "not configured"
      // — which is correct for a new row with no stored secret to inherit.
      creds: validateOverrideCreds(row, (c) => `llm_provider_${c}_${row.agent || ''}`),
    })),
  };
  const hasAnyError =
    !!errors.accounts ||
    !!errors.model ||
    !!errors.fallbacks ||
    Object.values(errors.tierModels).some(Boolean) ||
    Object.values(errors.tierFallbacks).some(Boolean) ||
    Object.values(errors.tierProviderModel).some(Boolean) ||
    Object.values(errors.tierCreds).some(Boolean) ||
    errors.agentRows.some((r) => r.model || r.fallbacks || r.providerModel || r.creds);

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
    // Per-tier — write provider + model + fallbacks, plus credentials when the
    // tier overrides the provider. When the user picked "Inherit" (form
    // provider is empty), we still write the tier_provider key as the global
    // provider — the resolver requires both provider and model to fire the
    // tier layer.
    TIER_KEYS.forEach((tier) => {
      const t = tiers[tier];
      if (t.model.trim() === '') {
        return;
      }
      const tierProvider = t.provider || provider;
      out.push({ name: `llm_tier_provider_${tier}`, value: tierProvider });
      out.push({ name: `llm_tier_model_${tier}`, value: t.model.trim() });
      if (t.fallbacks.trim()) {
        out.push({ name: `llm_tier_model_fallbacks_${tier}`, value: t.fallbacks.trim() });
      }
      // Inherited rows (empty t.provider) reuse global creds.
      if (t.provider) {
        const shape = providerFieldShape(t.provider);
        pushSecret(shape.showsApiKey, `llm_tier_api_key_${tier}`, t.apiKey);
        pushPlain(shape.showsApiEndpoint, `llm_tier_api_endpoint_${tier}`, t.apiEndpoint);
        pushPlain(shape.showsApiVersion, `llm_tier_api_version_${tier}`, t.apiVersion);
        pushPlain(shape.showsRegion, `llm_tier_region_${tier}`, t.region);
        pushSecret(shape.showsBedrockKeys, `llm_tier_access_key_${tier}`, t.accessKey);
        pushSecret(shape.showsBedrockKeys, `llm_tier_secret_key_${tier}`, t.secretKey);
        pushPlain(shape.showsApiType, `llm_tier_api_type_${tier}`, t.apiType);
      }
    });
    // Per-agent — same pattern as tiers.
    agentRows.forEach((row) => {
      if (!row.agent || row.model.trim() === '') {
        return;
      }
      const agentProvider = row.provider || provider;
      out.push({ name: `llm_provider_${row.agent}`, value: agentProvider });
      out.push({ name: `llm_model_name_${row.agent}`, value: row.model.trim() });
      if (row.fallbacks.trim()) {
        out.push({ name: `llm_model_fallbacks_${row.agent}`, value: row.fallbacks.trim() });
      }
      if (row.provider) {
        const shape = providerFieldShape(row.provider);
        pushSecret(shape.showsApiKey, `llm_provider_api_key_${row.agent}`, row.apiKey);
        pushPlain(shape.showsApiEndpoint, `llm_provider_api_endpoint_${row.agent}`, row.apiEndpoint);
        pushPlain(shape.showsApiVersion, `llm_provider_api_version_${row.agent}`, row.apiVersion);
        pushPlain(shape.showsRegion, `llm_provider_region_${row.agent}`, row.region);
        pushSecret(shape.showsBedrockKeys, `llm_provider_access_key_${row.agent}`, row.accessKey);
        pushSecret(shape.showsBedrockKeys, `llm_provider_secret_key_${row.agent}`, row.secretKey);
        pushPlain(shape.showsApiType, `llm_provider_api_type_${row.agent}`, row.apiType);
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
          Per-category overrides. Leave Provider as <strong>Inherit</strong> to use the global setting above; pick a different provider to route this
          category to a separate LLM.
        </Typography>
        <Stack spacing='var(--ds-space-3)' sx={{ mb: 'var(--ds-space-4)' }}>
          {TIER_KEYS.map((tierKey) => {
            const t = tiers[tierKey];
            const overrideProvider = t.provider || '';
            const shape = providerFieldShape(overrideProvider);
            const tierExample = (PROVIDER_EXAMPLES[overrideProvider || provider] || {})[tierKey] || providerExample[tierKey];
            // Auto-open the override pane whenever a cred or half-set error
            // exists, so the user can SEE what's wrong even if they closed it.
            const overridePaneOpen = t.providerOverrideOpen || !!errors.tierCreds[tierKey] || !!errors.tierProviderModel[tierKey];
            return (
              <Box
                key={tierKey}
                sx={{
                  border: '1px solid',
                  borderColor: 'var(--ds-gray-300)',
                  borderRadius: 'var(--ds-radius-sm)',
                  p: 'var(--ds-space-3)',
                }}
              >
                <Tooltip title={TIER_HINTS[tierKey]} placement='top'>
                  <Box sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 'var(--ds-space-2)' }}>{TIER_LABELS[tierKey]}</Box>
                </Tooltip>
                <Stack spacing='var(--ds-space-2)'>
                  <Input
                    label='Model'
                    size='sm'
                    value={t.model}
                    placeholder={model || `e.g. ${tierExample}`}
                    onChange={(value) => updateTier(tierKey, 'model', value)}
                    error={errors.tierModels[tierKey] || errors.tierProviderModel[tierKey]}
                  />
                  <Input
                    label='Fallbacks'
                    size='sm'
                    value={t.fallbacks}
                    placeholder='comma-separated'
                    onChange={(value) => updateTier(tierKey, 'fallbacks', value)}
                    error={errors.tierFallbacks[tierKey]}
                  />
                  {!overridePaneOpen ? (
                    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
                      <Typography variant='caption' sx={{ color: 'var(--ds-gray-500)' }}>
                        Provider, credentials inherit from global ({provider || 'set global provider above'}).
                      </Typography>
                      <Button
                        tone='ghost'
                        size='sm'
                        onClick={() => updateTier(tierKey, 'providerOverrideOpen', true)}
                        data-testid={`tier-override-open-${tierKey}`}
                      >
                        + Override provider
                      </Button>
                    </Box>
                  ) : (
                    <>
                      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mt: 'var(--ds-space-2)' }}>
                        <Typography variant='caption' sx={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                          Override
                        </Typography>
                        <Button tone='ghost' size='sm' onClick={() => revertTierOverride(tierKey)} data-testid={`tier-override-revert-${tierKey}`}>
                          × Revert to inherit
                        </Button>
                      </Box>
                      <Select
                        size='sm'
                        label='Provider'
                        value={t.provider || INHERIT_SENTINEL}
                        onChange={(value) => updateTier(tierKey, 'provider', value === INHERIT_SENTINEL ? '' : value)}
                        options={[
                          { value: INHERIT_SENTINEL, label: provider ? `Inherit (${provider}, from global)` : 'Inherit (set global provider first)' },
                          ...PROVIDERS.map((p) => ({ value: p, label: p })),
                        ]}
                      />
                      {overrideProvider && errors.tierCreds[tierKey] && (
                        <Box sx={{ color: 'var(--ds-red-600)', fontSize: 'var(--ds-text-caption)' }}>✗ {errors.tierCreds[tierKey]}</Box>
                      )}
                      {overrideProvider && shape.showsApiKey && (
                        <SecretInput
                          label='API Key'
                          value={t.apiKey}
                          onChange={(value) => updateTier(tierKey, 'apiKey', value)}
                          isConfigured={secretsConfigured[`llm_tier_api_key_${tierKey}`]}
                        />
                      )}
                      {overrideProvider && shape.showsApiEndpoint && (
                        <Input label='API Endpoint' size='sm' value={t.apiEndpoint} onChange={(value) => updateTier(tierKey, 'apiEndpoint', value)} />
                      )}
                      {overrideProvider && shape.showsApiVersion && (
                        <Input label='API Version' size='sm' value={t.apiVersion} onChange={(value) => updateTier(tierKey, 'apiVersion', value)} />
                      )}
                      {overrideProvider && shape.showsRegion && (
                        <Input label='Region' size='sm' value={t.region} onChange={(value) => updateTier(tierKey, 'region', value)} />
                      )}
                      {overrideProvider && shape.showsBedrockKeys && (
                        <>
                          <SecretInput
                            label='AWS Access Key'
                            value={t.accessKey}
                            onChange={(value) => updateTier(tierKey, 'accessKey', value)}
                            isConfigured={secretsConfigured[`llm_tier_access_key_${tierKey}`]}
                          />
                          <SecretInput
                            label='AWS Secret Key'
                            value={t.secretKey}
                            onChange={(value) => updateTier(tierKey, 'secretKey', value)}
                            isConfigured={secretsConfigured[`llm_tier_secret_key_${tierKey}`]}
                          />
                        </>
                      )}
                      {shape.showsApiType && (
                        <Input label='API Type' size='sm' value={t.apiType} onChange={(value) => updateTier(tierKey, 'apiType', value)} />
                      )}
                    </>
                  )}
                </Stack>
              </Box>
            );
          })}
        </Stack>

        <Divider />

        {/* ---------- Agent Overrides ---------- */}
        <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 'var(--ds-space-2)' }}>
          Agent Overrides
        </Typography>
        <Typography variant='caption' sx={{ display: 'block', mb: 'var(--ds-space-3)', color: 'var(--ds-gray-500)' }}>
          For specific agents that need a different model — or a different provider — than their category default. Agent overrides take precedence
          over category settings.
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
          <>
            {(agentRows.length > 3 || agentFilter) && (
              <Box sx={{ mb: 'var(--ds-space-3)' }}>
                <Input size='sm' placeholder='Filter agents…' value={agentFilter} onChange={setAgentFilter} />
              </Box>
            )}
            <Stack spacing='var(--ds-space-2)' sx={{ mb: 'var(--ds-space-4)' }}>
              {agentRows.map((row, idx) => {
                if (agentFilter && row.agent && !row.agent.toLowerCase().includes(agentFilter.toLowerCase())) {
                  return null;
                }
                const overrideProvider = row.provider || '';
                const shape = providerFieldShape(overrideProvider);
                const providerLabel = row.provider || (provider ? `Inherit (${provider})` : 'Inherit');
                const collapsed = row.collapsed && row.agent && row.model;
                // Auto-open the override pane on any cred or half-set error so
                // the user sees what's wrong even when they closed the section.
                const agentOverridePaneOpen = row.providerOverrideOpen || !!errors.agentRows[idx]?.creds || !!errors.agentRows[idx]?.providerModel;
                return (
                  <Box
                    key={`agent-row-${idx}`}
                    sx={{
                      border: '1px solid',
                      borderColor: 'var(--ds-gray-300)',
                      borderRadius: 'var(--ds-radius-sm)',
                      p: 'var(--ds-space-3)',
                    }}
                  >
                    {collapsed ? (
                      <Box
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 'var(--ds-space-3)',
                          cursor: 'pointer',
                        }}
                        onClick={() => updateAgentRow(idx, 'collapsed', false)}
                        data-testid={`agent-row-expand-${idx}`}
                      >
                        <Box sx={{ fontWeight: 'var(--ds-font-weight-semibold)', flex: '0 0 auto' }}>{row.agent}</Box>
                        <Box sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body)', flex: 1 }}>
                          {providerLabel} · {row.model}
                        </Box>
                        <Button
                          tone='ghost'
                          size='sm'
                          icon={<ExpandMoreIcon />}
                          onClick={(e) => {
                            e.stopPropagation();
                            updateAgentRow(idx, 'collapsed', false);
                          }}
                          aria-label='Expand'
                        />
                        <Button
                          tone='ghost'
                          size='sm'
                          icon={<DeleteOutlineIcon />}
                          onClick={(e) => {
                            e.stopPropagation();
                            removeAgentRow(idx);
                          }}
                          data-testid={`remove-agent-row-${idx}`}
                          aria-label='Remove row'
                        />
                      </Box>
                    ) : (
                      <Stack spacing='var(--ds-space-2)'>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                          <Box sx={{ flex: 1 }}>
                            <Select
                              size='sm'
                              label='Agent'
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
                          </Box>
                          {row.agent && row.model && (
                            <Button
                              tone='ghost'
                              size='sm'
                              icon={<ExpandLessIcon />}
                              onClick={() => updateAgentRow(idx, 'collapsed', true)}
                              aria-label='Collapse'
                            />
                          )}
                          <Button
                            tone='ghost'
                            size='sm'
                            icon={<DeleteOutlineIcon />}
                            onClick={() => removeAgentRow(idx)}
                            data-testid={`remove-agent-row-${idx}`}
                            aria-label='Remove row'
                          />
                        </Box>
                        <Input
                          label='Model'
                          size='sm'
                          value={row.model}
                          placeholder={`e.g. ${providerExample.reasoning}`}
                          onChange={(value) => updateAgentRow(idx, 'model', value)}
                          error={errors.agentRows[idx]?.model || errors.agentRows[idx]?.providerModel}
                        />
                        <Input
                          label='Fallbacks'
                          size='sm'
                          value={row.fallbacks}
                          placeholder='comma-separated'
                          onChange={(value) => updateAgentRow(idx, 'fallbacks', value)}
                          error={errors.agentRows[idx]?.fallbacks}
                        />
                        {!agentOverridePaneOpen ? (
                          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
                            <Typography variant='caption' sx={{ color: 'var(--ds-gray-500)' }}>
                              Provider, credentials inherit from global ({provider || 'set global provider above'}).
                            </Typography>
                            <Button
                              tone='ghost'
                              size='sm'
                              onClick={() => updateAgentRow(idx, 'providerOverrideOpen', true)}
                              data-testid={`agent-override-open-${idx}`}
                            >
                              + Override provider
                            </Button>
                          </Box>
                        ) : (
                          <>
                            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mt: 'var(--ds-space-2)' }}>
                              <Typography variant='caption' sx={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                                Override
                              </Typography>
                              <Button tone='ghost' size='sm' onClick={() => revertAgentOverride(idx)} data-testid={`agent-override-revert-${idx}`}>
                                × Revert to inherit
                              </Button>
                            </Box>
                            <Select
                              size='sm'
                              label='Provider'
                              value={row.provider || INHERIT_SENTINEL}
                              onChange={(value) => updateAgentRow(idx, 'provider', value === INHERIT_SENTINEL ? '' : value)}
                              options={[
                                {
                                  value: INHERIT_SENTINEL,
                                  label: provider ? `Inherit (${provider}, from global)` : 'Inherit (set global provider first)',
                                },
                                ...PROVIDERS.map((p) => ({ value: p, label: p })),
                              ]}
                            />
                            {overrideProvider && errors.agentRows[idx]?.creds && (
                              <Box sx={{ color: 'var(--ds-red-600)', fontSize: 'var(--ds-text-caption)' }}>✗ {errors.agentRows[idx].creds}</Box>
                            )}
                            {overrideProvider && shape.showsApiKey && (
                              <SecretInput
                                label='API Key'
                                value={row.apiKey}
                                onChange={(value) => updateAgentRow(idx, 'apiKey', value)}
                                isConfigured={secretsConfigured[`llm_provider_api_key_${row.agent}`]}
                              />
                            )}
                            {overrideProvider && shape.showsApiEndpoint && (
                              <Input
                                label='API Endpoint'
                                size='sm'
                                value={row.apiEndpoint}
                                onChange={(value) => updateAgentRow(idx, 'apiEndpoint', value)}
                              />
                            )}
                            {overrideProvider && shape.showsApiVersion && (
                              <Input
                                label='API Version'
                                size='sm'
                                value={row.apiVersion}
                                onChange={(value) => updateAgentRow(idx, 'apiVersion', value)}
                              />
                            )}
                            {overrideProvider && shape.showsRegion && (
                              <Input label='Region' size='sm' value={row.region} onChange={(value) => updateAgentRow(idx, 'region', value)} />
                            )}
                            {overrideProvider && shape.showsBedrockKeys && (
                              <>
                                <SecretInput
                                  label='AWS Access Key'
                                  value={row.accessKey}
                                  onChange={(value) => updateAgentRow(idx, 'accessKey', value)}
                                  isConfigured={secretsConfigured[`llm_provider_access_key_${row.agent}`]}
                                />
                                <SecretInput
                                  label='AWS Secret Key'
                                  value={row.secretKey}
                                  onChange={(value) => updateAgentRow(idx, 'secretKey', value)}
                                  isConfigured={secretsConfigured[`llm_provider_secret_key_${row.agent}`]}
                                />
                              </>
                            )}
                            {overrideProvider && shape.showsApiType && (
                              <Input label='API Type' size='sm' value={row.apiType} onChange={(value) => updateAgentRow(idx, 'apiType', value)} />
                            )}
                          </>
                        )}
                      </Stack>
                    )}
                  </Box>
                );
              })}
            </Stack>
          </>
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
                ✗ Please fix the highlighted errors above to enable Test Connection.
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
