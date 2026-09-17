import React, { useEffect, useMemo, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { getBrandTitle } from '@hooks/useTenantBranding';
import { FormControlLabel, Box, Typography, Grid, Collapse } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import VisibilityIcon from '@mui/icons-material/Visibility';
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff';
import { Switch } from '@ui/Switch';
import { Checkbox } from '@ui/Checkbox';
import { Input } from '@ui/Input';
import FilterDropdown from '@ui/FilterDropdown';
import Tooltip from '@ui/Tooltip';
import apiUser from '@api1/user';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import apiIntegrations from '@api1/integrations';
import observability from '@api1/observability';
import { ENCRYPTED_MASK } from '@api1/integrations/helpers';
import { ds } from 'src/utils/colors';
import CopyButton from '@shared/buttons/CopyButton';
import { titleCase } from '@lib/formatter';
import { getAccountCreationSuccessMsg, parseHttpResponseBodyMessage, safeJSONParse, snakeToTitleCase, toKebabCase } from 'src/utils/common';
import { toast as snackbar } from '@ui/Toast';
import { DeleteIconRed as NewDelete, infoIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import apiTicketIntegrations from '@api1/tickets';
import cache from '@lib/cache';
import VmAgentCredentialsDialog from './VmAgentCredentialsDialog';
import { docsUrl } from '@lib/externalUrls';
import ModelAliasList from '@components/common/forms/ModelAliasList';
import LabelMappingCards, { parseLogLabelMappings, serializeLogLabelMappings } from './LabelMappingCards';
import useLogFieldOptions, { fieldOptionsKey, indexForAccount } from './useLogFieldOptions';
import DefaultFiltersCard, { emptyFilterCard, hasCardMissingAccount, parseDefaultFilters, serializeDefaultFilters } from './DefaultFiltersCard';
import { TRACE_LABEL_ADVANCED_FIELDS, TRACE_LABEL_FIELDS } from '@components/common/settings/labelMapperFields';

// Array-typed config fields travel as one comma-joined string in
// integration_config_values; chips may be plain values or {label, value}.
const joinConfigArray = (values) => values.map((v) => (v && typeof v === 'object' ? v.value : v)).join(',');

// Group-header icon for the account dropdown — maps a group (the account's
// cloud_provider: K8S/AWS/Azure/GCP) to its provider icon. Same pattern as the
// Account filter on the Troubleshoot/Events page.
const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='16px' height='16px' />;

// Drop the on-demand validation results the edit just invalidated. Cards are matched
// by index, so any change in card count clears everything; otherwise only the cards
// whose contents actually differ lose their result.
const dropStaleValidation = (validation, prevCards, nextCards) => {
  if (prevCards.length !== nextCards.length) return {};
  const next = { ...validation };
  nextCards.forEach((card, idx) => {
    if (JSON.stringify(card) !== JSON.stringify(prevCards[idx])) next[idx] = undefined;
  });
  return next;
};

// Log/observability integrations that support per-account "Default Log Filters"
// (always-apply where-clause filters injected into every log query for the account).
// The add flow receives an empty object rather than undefined, so presence alone does not
// distinguish add from edit.
const hasEditData = (editData) => !!(editData && Object.keys(editData).length > 0);

const LOG_FILTER_INTEGRATIONS = new Set([
  'pinot',
  'ES',
  'elasticsearch',
  'loki',
  'signoz',
  'openobserve',
  'cubeapm',
  'datadog',
  'dynatrace',
  'chronosphere',
  'splunk_enterprise',
]);

// Trace integrations that support per-account "Default Trace Filters". Members are the
// providers getTraceSource actually serves AND that have an integration form, so the
// card never appears where a saved value could not be read back. Note the differences
// from the log set above: 'otel_clickhouse' (the agent trace provider) rather than
// 'clickhouse' (an unrelated database integration), and no 'signoz' — signoz has no
// trace source today. 'gcp' is resolver-synthesized and has no form at all.
// Also gates the Trace Label Mapping section. Deliberately NOT keyed off
// `config.properties?.trace_label_mappings` the way the log mapping is: the backend
// injects that property for every Log AND ObservabilityPlatform integration, which is
// broader than the set getTraceSource serves — a schema-driven gate would render a dead
// trace section on loki, pinot and hive.
const TRACE_INTEGRATIONS = new Set([
  'otel_clickhouse',
  'jaeger',
  'chronosphere',
  'datadog',
  'dynatrace',
  'ES',
  'elasticsearch',
  'openobserve',
  'newrelic',
  'splunk_observability_platform',
  'solarwinds',
  'azure_app_insights',
  'splunk_enterprise',
  'cubeapm',
]);

// Canonical trace field names, reused from the Settings trace label mapper so the two
// screens cannot drift: the server stores and applies trace filters in exactly this
// vocabulary (canonicalTraceFields in observability/service.go).
const ALL_TRACE_LABEL_FIELDS = [...TRACE_LABEL_FIELDS, ...TRACE_LABEL_ADVANCED_FIELDS];

// Label stays the bare canonical name on purpose: these dropdowns are inputs whose VALUE
// is the canonical field, and the Default Trace Filters card round-trips the displayed
// text. The human names are used where they read as prose — the effective-mapping panel.
const CANONICAL_TRACE_FIELD_OPTIONS = ALL_TRACE_LABEL_FIELDS.map(({ field }) => ({
  label: field,
  value: field,
}));

// canonical -> human name, and the order an operator meets these fields on the Settings
// trace mapper: the five most-retuned first, the rarer ones behind its disclosure after.
// Reused rather than re-listed so the panel, the mapper and the filter dropdown cannot
// drift.
const TRACE_CONCEPT_LABELS = Object.fromEntries(ALL_TRACE_LABEL_FIELDS.map(({ field, label }) => [field, label]));
const TRACE_CONCEPT_ORDER = ALL_TRACE_LABEL_FIELDS.map(({ field }) => field);

// Alert Template body NudgeBee expects from OpenObserve. Every `{variable}` is
// substituted by OpenObserve at delivery; the k8s_* keys resolve from the
// matching stream row and are simply dropped when the stream has no such field,
// so the same template works for log, metric and trace alerts.
const OPENOBSERVE_ALERT_TEMPLATE = `{
  "alert_name": "{alert_name}",
  "alert_type": "{alert_type}",
  "stream_name": "{stream_name}",
  "stream_type": "{stream_type}",
  "org_name": "{org_name}",
  "alert_period": "{alert_period}",
  "alert_operator": "{alert_operator}",
  "alert_threshold": "{alert_threshold}",
  "alert_count": "{alert_count}",
  "alert_agg_value": "{alert_agg_value}",
  "alert_start_time": "{alert_start_time}",
  "alert_end_time": "{alert_end_time}",
  "alert_url": "{alert_url}",
  "severity": "{severity}",
  "k8s_cluster_name": "{k8s_cluster_name}",
  "k8s_namespace_name": "{k8s_namespace_name}",
  "k8s_pod_name": "{k8s_pod_name}",
  "k8s_deployment_name": "{k8s_deployment_name}",
  "k8s_node_name": "{k8s_node_name}",
  "service_name": "{service_name}"
}`;

// Display labels for enum values whose stored form doesn't title-case into
// something readable. Values not listed here fall back to snakeToTitleCase.
const ENUM_VALUE_LABELS = {
  vm_agent: 'Proxy Agent',
  // Prometheus auth_type values that snakeToTitleCase mangles ("Aws Sigv4",
  // "Azure Ad"). The scheme's other values ('none', 'basic', 'bearer_token',
  // 'coralogix') title-case correctly and are shared with other integrations,
  // so they are deliberately left alone.
  aws_sigv4: 'AWS SigV4 (Amazon Managed Prometheus)',
  azure_ad: 'Azure AD (Azure Monitor managed Prometheus)',
  // snakeToTitleCase would render "Mimir Cortex", which names neither the API nor
  // Grafana Cloud — the deployments an operator is actually choosing between.
  mimir_cortex: 'Mimir / Cortex / Grafana Cloud',
  cloud_api_token: 'Confluence Cloud — email + API token',
  datacenter_pat: 'Data Center / Server — personal access token',
  datacenter_password: 'Data Center / Server — username + password',
};

const COMMON_WEBHOOK_LABEL_KEYS = [
  'alertname',
  'severity',
  'priority',
  'level',
  'namespace',
  'destination_workload_namespace',
  'k8s_namespace',
  'k8s.namespace.name',
  'service_name',
  'service.name',
  'app_id',
  'app_name',
  'deployment',
  'daemonset',
  'statefulset',
  'pod',
  'pod_name',
  'container',
  'job',
  'instance',
  'cluster',
  'team',
  'env',
  'environment',
  'executor_name',
  'saas_env',
  'destination_workload_name',
  'src_workload_name',
  'nb_alert_job',
  'nb_resource_id',
  'monitorName',
  'rulename',
  'related_logs',
  'rule_id',
  'rule_type',
];

const IntegrationDynamicFormModal = ({
  integrationName,
  openModal,
  handleClose,
  title,
  integrationData = [],
  editData = null,
  listIntegrationConfigurationById,
}) => {
  const [errors, setErrors] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [config, setConfig] = useState({});
  const [formValues, setFormValues] = useState({});
  const [response, setResponse] = useState({});
  const [showModal, setShowModal] = useState(false);
  const [loadingOptions, setLoadingOptions] = useState({});
  const [isLoadingSchema, setIsLoadingSchema] = useState(false);
  // Per-field results for form-context-dependent autogen functions (the
  // ones whose AutoGenerateFunc is not the built-in `listAccounts`).
  // Shape: { [fieldKey]: { options: [{label,value}], message: string, loading: bool } }
  const [autogenState, setAutogenState] = useState({});
  // Shared cache so multiple fields with the same (autogen_func, deps) tuple
  // (e.g. all 6 Hive column fields) only fetch once per dep-values change.
  const autogenCacheRef = useRef(new Map());
  const autogenDebounceRef = useRef(null);
  const [rules, setRules] = useState([{ match: [{ key: '', value: '' }], accountId: '' }]);
  // Per-account "Default Log Filters" (log integrations only): each card is an
  // account + a list of key=value filters always AND-ed into that account's log queries.
  const [defaultFilterRules, setDefaultFilterRules] = useState([{ accountId: '', filters: [{ key: '', value: '' }] }]);
  // Per-account "Default Trace Filters" (trace integrations only). Same editor and the
  // same stored shape as the log filters above, but a SEPARATE config value: one
  // integration record commonly serves both logs and traces, so a shared list would
  // apply log filters to trace queries.
  const [defaultTraceFilterRules, setDefaultTraceFilterRules] = useState([emptyFilterCard()]);
  // Per-account ES index override (Advanced Settings): each card maps an account to
  // its own log/metrics/trace index; unmapped accounts fall back to the top-level index.
  const [indexRules, setIndexRules] = useState([{ accountId: '', log_index: '', metrics_index: '', trace_index: '' }]);
  // Per-account canonical -> provider log field mapping (Advanced Settings). Highest
  // precedence layer of the mapping merge, above the account and tenant settings.
  const [labelMappingCards, setLabelMappingCards] = useState([{ accountId: '', rows: [{ canonical: '', field: '' }] }]);
  // The same cards as first hydrated from the saved config, so each card can tell an
  // in-progress edit from a mapping that is already live.
  const [savedLabelMappingCards, setSavedLabelMappingCards] = useState([]);
  // Per-account canonical -> provider TRACE field mapping (Advanced Settings). Same
  // tier and the same stored shape as the log mapping above, under a separate config
  // value for the reason the trace filters are separate: one integration record
  // commonly serves both signals.
  const [traceLabelMappingCards, setTraceLabelMappingCards] = useState([{ accountId: '', rows: [{ canonical: '', field: '' }] }]);
  const [savedTraceLabelMappingCards, setSavedTraceLabelMappingCards] = useState([]);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  // On-demand column validation per card: { [cardIdx]: { loading, done, invalid: [colNames] } }.
  const [columnValidation, setColumnValidation] = useState({});
  // Per-source subject/namespace/severity label mapping (webhook_label_mapping).
  // Each field is a comma-separated list of label key specs; persisted as arrays.
  const [labelMapping, setLabelMapping] = useState({ subject_name_labels: [], namespace_labels: [], severity_labels: [] });
  const [agentAccountProviders, setAgentAccountProviders] = useState([]);
  const [providerFields, setProviderFields] = useState([]);
  const [vmAgentCredentials, setVmAgentCredentials] = useState(null);
  const [isTesting, setIsTesting] = useState(false);
  // Object.keys, not !!editData: ListIntegrations passes `editData={selectedIntegration || {}}`,
  // so the add flow receives an empty object and `!!{}` is true — a brand-new integration
  // started out "verified" and every Advanced Settings editor rendered before a single
  // connection had been proven. Matches the `isEdit` test used further down.
  const [connectionVerified, setConnectionVerified] = useState(hasEditData(editData));
  // Which encrypted secret fields are currently revealed (eye toggle), keyed by field key.
  const [revealedSecrets, setRevealedSecrets] = useState({});
  // Cluster indices for the ES per-account index picker; fetched once the
  // connection is verified. Cleared when a testable field changes (handleChange).
  const [esIndexes, setEsIndexes] = useState([]);
  const [esIndexesLoading, setEsIndexesLoading] = useState(false);

  const isTestable = (() => {
    if (!config?.testable) return false;
    if (!config?.testable_when || Object.keys(config.testable_when).length === 0) return true;
    return Object.entries(config.testable_when).every(([key, value]) => formValues[key] === value);
  })();

  const isAgentSource = editData?.source === 'agent';

  // Advanced Settings editors are gated on a proven connection, because their dropdowns
  // are filled from the backend. But a provider whose schema is not `testable` renders no
  // Test Connection button — agent-sourced ES and Loki are the live cases — so gating them
  // on it strands the add flow behind an instruction the user cannot follow. Nothing to
  // verify means nothing to wait for; their config is the relay agent, not a URL in this
  // form, so there is no unsaved endpoint to preview either.
  const advancedSettingsUnlocked = !isTestable || connectionVerified;

  // Gated on the backend's own answer — it emits the `log_label_mappings` property
  // only for integrations that resolve to a real log source. No provider list lives
  // here, so this cannot drift from what a save will accept. LOG_FILTER_INTEGRATIONS is
  // deliberately not reused: it excludes ES, the provider with no other way to set a
  // mapping at all.
  const supportsLogLabelMapping = !!config.properties?.log_label_mappings;

  useEffect(() => {
    if (openModal && isAgentSource && editData?.integrations_cloud_accounts) {
      setAgentAccountProviders(
        editData.integrations_cloud_accounts.map((acc) => ({
          cloud_account_id: acc.cloud_account_id,
          account_name: acc.cloud_account_name,
          default_log_provider: acc.default_log_provider || false,
          default_traces_provider: acc.default_traces_provider || false,
          default_metrics_provider: acc.default_metrics_provider || false,
        }))
      );

      // Derive provider fields from cloud accounts data for agent integrations
      const providerKeyLabels = {
        default_log_provider: 'Logs',
        default_traces_provider: 'Traces',
        default_metrics_provider: 'Metrics',
      };
      const firstAcc = editData.integrations_cloud_accounts[0];
      if (firstAcc) {
        const fields = Object.keys(providerKeyLabels)
          .filter((key) => key in firstAcc)
          .map((key) => ({ key, label: providerKeyLabels[key] }));
        setProviderFields(fields);
      }
    }
  }, [openModal, isAgentSource, editData]);

  useEffect(() => {
    if (openModal) {
      setConnectionVerified(hasEditData(editData));
      setIsLoadingSchema(true);
      const fetchData = async (configs) => {
        const updatedConfig = { ...configs };
        // Which integrations may bind to CLOUD accounts (AWS/Azure/GCP/CloudFoundry)?
        // - Webhooks route incoming alerts to any account, so they list everything.
        // - Otherwise only providers whose log source serves arbitrary cloud-account
        //   logs (a raw-query pass-through against a generic logs backend) are allowed.
        //   K8s-only sources whose query is keyed on proprietary k8s columns/paths
        //   (loggly, pinot, hive, azure_app_insights, signoz) stay K8s-only, as does
        //   New Relic by product decision (issue #29403) even though it is technically
        //   capable. Agent-sourced providers (loki, prometheus, otel, ES-agent) are
        //   never cloud-eligible, hence !isAgentSource.
        // 'es' is the ES page's integrationName (account-form.jsx passes {'ES'}); the
        // check lowercases it, so 'es' — not 'elasticsearch' — is what matches here.
        const CLOUD_CAPABLE_INTEGRATIONS = ['datadog', 'observe', 'dynatrace', 'splunk_observability_platform', 'solarwinds', 'elasticsearch', 'es'];
        const isWebhook = configs.category === 'incident_webhook' || (integrationName || '').toLowerCase().includes('webhook');
        // The VM agent (forager) is the exception to the !isAgentSource rule above.
        // That rule excludes agent-sourced providers because they need an
        // in-cluster relay agent a cloud account does not have — but a forager is
        // not in-cluster. It is a per-network-segment process dialling out to the
        // relay, and reaching EC2 or Azure VMs inside a customer's VPC is exactly
        // what it is for. Filtering cloud accounts out here is what forces VM
        // fleets to be onboarded under a Kubernetes account (#35683).
        const isVmAgent = (integrationName || '').toLowerCase() === 'vm_agent' || configs.type === 'vm_agent';
        const showAllAccounts =
          isWebhook || isVmAgent || (!isAgentSource && CLOUD_CAPABLE_INTEGRATIONS.includes((integrationName || '').toLowerCase()));
        for (const key in updatedConfig.properties) {
          const field = updatedConfig.properties[key];
          if (field.auto_generate_func && field.auto_generate_func === 'listAccounts') {
            try {
              setLoadingOptions((prev) => ({ ...prev, [key]: true }));
              const res = await apiUser.listAccounts();
              if (res.length > 0) {
                // Drop cloud accounts for integrations that only support K8s
                // clusters (they'd have no relay agent / no cloud data path and
                // fail at save). Allow-listed providers and webhooks keep the
                // full list (see showAllAccounts above).
                const accounts = showAllAccounts
                  ? res
                  : res.filter((account) => !['AWS', 'Azure', 'GCP', 'CloudFoundry'].includes(account.cloud_provider));
                // Group each option by its cloud_provider (K8S/AWS/Azure/GCP) so the
                // dropdown separates cloud accounts from K8s clusters — same grouping
                // the Account filter on the Troubleshoot page uses.
                const cloudAccounts = accounts.map((account) => ({
                  label: account.account_name,
                  value: account.id,
                  group: account.cloud_provider || 'Other',
                }));
                updatedConfig.properties[key].possible_values = cloudAccounts;
                // Group only when there's more than one account type to separate
                // (e.g. K8S + AWS for Datadog); a single-type list stays flat so it
                // doesn't hide every option behind a collapsed group header.
                updatedConfig.properties[key].grouped = new Set(cloudAccounts.map((a) => a.group)).size > 1;
                // Forcing default=[] makes the renderer pick the multi-select
                // branch. Skip when the schema marks the field single_select.
                if (!field.single_select) {
                  updatedConfig.properties[key].default = [];
                }
              }
            } finally {
              setLoadingOptions((prev) => ({ ...prev, [key]: false }));
            }
          } else if (field.enum && field.enum.length > 0) {
            updatedConfig.properties[key].possible_values = field.enum.map((value) => ({
              label: ENUM_VALUE_LABELS[value] || snakeToTitleCase(value),
              value: value,
            }));
          }
        }
        setConfig(updatedConfig);
        setIsLoadingSchema(false);
      };
      apiIntegrations
        .listIntegrationSchema({
          integration_name: integrationName,
          source: editData?.source ?? 'user',
        })
        .then((res) => {
          const configs = res?.data?.data?.integrations_get_schema?.data || {};
          if (Object.keys(configs).length > 0) {
            // Extract provider fields from schema (boolean fields ending with _provider)
            const extractedProviderFields = Object.entries(configs.properties || {})
              .filter(([key, prop]) => (prop.type === 'bool' || prop.type === 'boolean') && key.endsWith('_provider'))
              .map(([key, prop]) => ({ key, label: prop.display_name || snakeToTitleCase(key) }));
            if (extractedProviderFields.length > 0) {
              setProviderFields(extractedProviderFields);
            }
            const filteredProperties = Object.fromEntries(
              Object.entries(configs.properties || {}).filter(([_key, prop]) => {
                // If it's true, filter it out.
                // If it's undefined, null, or false, keep it.
                return prop.avoid_to_show !== true;
              })
            );
            const cleanConfigs = {
              ...configs,
              properties: filteredProperties,
            };
            setConfig(cleanConfigs);
            setFormValues(() => {
              const initialValues = {};
              Object.keys(configs?.properties || {}).forEach((key) => {
                const prop = configs.properties[key];
                let val = editData?.integration_config_values?.[key] ?? prop.default ?? '';
                if (prop.type === 'boolean' || prop.type === 'bool') {
                  if (typeof val === 'string') {
                    val = val.toLowerCase() === 'true';
                  } else {
                    val = Boolean(val);
                  }
                }
                // editData stores account_id as an array (from integrations_cloud_accounts).
                // For single-select fields, collapse to the first value so the renderer
                // and submit path see a scalar.
                if (prop.single_select && Array.isArray(val)) {
                  val = val[0] ?? '';
                }
                // Array-typed config fields (other than account_id, which has its own
                // pivot table) are stored as one comma-joined string.
                if (prop.type === 'array' && key !== 'account_id' && typeof val === 'string') {
                  val = val
                    .split(',')
                    .map((v) => v.trim())
                    .filter(Boolean);
                }
                if (key == 'default_log_provider' || key == 'default_traces_provider' || key == 'default_metrics_provider') {
                  if (editData?.integrations_cloud_accounts?.length) {
                    val = editData?.integrations_cloud_accounts?.[0]?.[key] || false;
                  }
                }
                if (prop.is_encrypted && editData?.integration_config_values?.[key]) {
                  initialValues[key] = '*************************************************';
                } else {
                  initialValues[key] = val;
                }
              });
              return initialValues;
            });

            fetchData(cleanConfigs);
          } else {
            setIsLoadingSchema(false);
          }
        })
        .catch(() => {
          setIsLoadingSchema(false);
        });
    }
  }, [openModal]);

  // Fetch form-context-dependent autocomplete options for any field whose
  // schema declares both `auto_generate_func` (not the built-in 'listAccounts')
  // and `depends_on`. The effect:
  //   1. Groups fields by (autogen_func, dep-values) so we de-duplicate the
  //      fetch across siblings that share the same deps (e.g. all 6 Hive
  //      column fields).
  //   2. Skips fetching when a required dep is missing. "Required" follows
  //      the dep field's own `required_when` (via isFieldRequired) — no
  //      integration-specific hardcoding. So Hive's `password` is treated
  //      as optional when auth_type≠ldap because that's how the schema says
  //      so; future integrations get the same behaviour for free.
  //   3. Debounces 500ms so typing in a dep field doesn't thrash the network.
  //   4. Caches by dep-key so flipping back to a previously-seen state is
  //      instant.
  useEffect(() => {
    if (!openModal || !config?.properties) return undefined;

    const candidates = Object.entries(config.properties).filter(
      ([, field]) =>
        field?.auto_generate_func && field.auto_generate_func !== 'listAccounts' && Array.isArray(field.depends_on) && field.depends_on.length > 0
    );
    if (candidates.length === 0) return undefined;

    // (autogen_func + depKey) → { fn, formValues, fieldKeys[] }
    const groups = new Map();
    for (const [key, field] of candidates) {
      const formContext = {};
      let ready = true;
      for (const dep of field.depends_on) {
        const depField = config.properties[dep];
        const raw = formValues[dep];
        let val = raw == null ? '' : String(raw);
        // Encrypted secrets are shown as a mask when editing an existing
        // integration — the real plaintext never reaches the form. Never
        // forward the mask as a value: the handler would use it verbatim as
        // the password/token and fail auth upstream (e.g. Pinot HTTP 403).
        // Blank it so the handler sees "no secret" and can prompt for re-entry.
        if (depField?.is_encrypted && val === ENCRYPTED_MASK) val = '';
        formContext[dep] = val;
        if (val === '') {
          // Empty dep — only blocks the fetch if the dep field is *currently*
          // required (base required[] or a satisfied required_when clause).
          // Conditionally-required deps that aren't required right now are
          // skipped, so e.g. an LDAP password field doesn't gate a NONE-auth
          // fetch.
          if (depField && !isFieldRequired(dep, depField)) continue;
          // A required *encrypted* secret that's empty (new integration) or
          // masked (edit mode) shouldn't silently block the fetch. Let it
          // through so the handler can return its own "re-enter <secret> to
          // load suggestions" hint instead of leaving the field blank with no
          // explanation.
          if (depField?.is_encrypted) continue;
          ready = false;
          break;
        }
      }
      if (!ready) continue;
      const depKey = `${field.auto_generate_func}:` + field.depends_on.map((d) => `${d}=${formContext[d] || ''}`).join('|');
      if (!groups.has(depKey)) {
        groups.set(depKey, {
          fn: field.auto_generate_func,
          formValues: formContext,
          fieldKeys: [],
        });
      }
      groups.get(depKey).fieldKeys.push(key);
    }
    if (groups.size === 0) return undefined;

    if (autogenDebounceRef.current) {
      clearTimeout(autogenDebounceRef.current);
    }
    autogenDebounceRef.current = setTimeout(() => {
      groups.forEach(async (group, depKey) => {
        let result = autogenCacheRef.current.get(depKey);
        if (!result) {
          setAutogenState((prev) => {
            const next = { ...prev };
            for (const k of group.fieldKeys) {
              next[k] = { ...(prev[k] || { options: [], message: '' }), loading: true };
            }
            return next;
          });
          try {
            result = await apiIntegrations.getAutogenOptions({
              autogen_func: group.fn,
              form_values: group.formValues,
            });
          } catch {
            result = { options: [], message: 'Failed to load suggestions.' };
          }
          if (!result || !Array.isArray(result.options)) {
            result = { options: [], message: result?.message || '' };
          }
          autogenCacheRef.current.set(depKey, result);
        }
        setAutogenState((prev) => {
          const next = { ...prev };
          for (const k of group.fieldKeys) {
            next[k] = {
              options: result.options || [],
              message: result.message || '',
              loading: false,
            };
          }
          return next;
        });
      });
    }, 500);

    return () => {
      if (autogenDebounceRef.current) {
        clearTimeout(autogenDebounceRef.current);
        autogenDebounceRef.current = null;
      }
    };
  }, [formValues, config, openModal]);

  // Hydrate rules from saved account_mapping. Tolerates three on-disk shapes:
  //  1) Canonical:   { rules: [{ match: {k:v,...}, accountId }] }
  //  2) Legacy flat: { labelName: "env", "<value>": "<accId>" | {label,value} }
  //  3) Empty / malformed → start with one blank rule
  useEffect(() => {
    if (!editData?.integration_config_values?.account_mapping) return;
    const parsed = safeJSONParse(editData.integration_config_values.account_mapping) || {};

    const normalizeAcc = (a) => (typeof a === 'object' && a !== null ? a.value || '' : a || '');

    if (Array.isArray(parsed.rules) && parsed.rules.length > 0) {
      // Match values may be a single string or an array of strings
      // (value-OR within a key, e.g. {"env": ["na","eu"]}). Render arrays
      // as comma-separated text in the input — save() splits them back out.
      const formatMatchValue = (value) => {
        if (Array.isArray(value))
          return value
            .map((v) => String(v ?? '').trim())
            .filter(Boolean)
            .join(', ');
        return String(value ?? '');
      };
      const next = parsed.rules.map((r) => ({
        match: Object.entries(r.match || {}).map(([key, value]) => ({ key, value: formatMatchValue(value) })),
        accountId: normalizeAcc(r.accountId),
      }));
      // Ensure every rule has at least one editable condition row
      next.forEach((r) => {
        if (r.match.length === 0) r.match.push({ key: '', value: '' });
      });
      setRules(next);
      return;
    }

    const labelName = parsed.labelName;
    if (labelName) {
      const legacyRules = Object.entries(parsed)
        .filter(([k]) => k !== 'labelName')
        .map(([value, accountId]) => ({
          match: [{ key: labelName, value }],
          accountId: normalizeAcc(accountId),
        }));
      if (legacyRules.length > 0) setRules(legacyRules);
    }
  }, [editData]);

  // Hydrate per-account Default Log Filters from the saved default_filters config.
  useEffect(() => {
    const raw = editData?.integration_config_values?.default_filters;
    if (!raw) return;
    const parsed = safeJSONParse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) return;
    setDefaultFilterRules(parseDefaultFilters(parsed));
  }, [editData]);

  // Same, for Default Trace Filters. Kept as its own effect rather than folded into
  // the one above because the two configs are independent: an integration can carry
  // one, the other, or both.
  useEffect(() => {
    const raw = editData?.integration_config_values?.default_trace_filters;
    if (!raw) return;
    const parsed = safeJSONParse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) return;
    setDefaultTraceFilterRules(parseDefaultFilters(parsed));
  }, [editData]);

  // Hydrate the ES per-account index override from the saved index_account_mapping
  // blob (JSON array of { account_id, log_index?, metrics_index?, trace_index? }).
  useEffect(() => {
    const raw = editData?.integration_config_values?.index_account_mapping;
    if (!raw) return;
    const parsed = safeJSONParse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) return;
    const normalizeAcc = (a) => (typeof a === 'object' && a !== null ? a.value || '' : a || '');
    setIndexRules(
      parsed.map((e) => ({
        accountId: normalizeAcc(e.account_id ?? e.accountId),
        log_index: e.log_index || '',
        metrics_index: e.metrics_index || '',
        trace_index: e.trace_index || '',
      }))
    );
  }, [editData]);

  // Hydrate the per-account log label mapping from the saved log_label_mappings blob
  // (JSON array of { accountId, mappings }).
  useEffect(() => {
    const raw = editData?.integration_config_values?.log_label_mappings;
    if (!raw) {
      setSavedLabelMappingCards([]);
      return;
    }
    const parsed = parseLogLabelMappings(raw);
    setLabelMappingCards(parsed);
    setSavedLabelMappingCards(parsed);
  }, [editData]);

  // Same, for the trace label mapping. Its own effect rather than folded into the one
  // above because the two configs are independent: an integration can carry one, the
  // other, or both.
  useEffect(() => {
    const raw = editData?.integration_config_values?.trace_label_mappings;
    if (!raw) {
      setSavedTraceLabelMappingCards([]);
      return;
    }
    const parsed = parseLogLabelMappings(raw);
    setTraceLabelMappingCards(parsed);
    setSavedTraceLabelMappingCards(parsed);
  }, [editData]);

  // Advanced Settings stays collapsed on open, for every provider.
  //
  // Elasticsearch used to force it open so the Per-Account Index cards were visible
  // without an extra click. That made sense when the section held one card; it now
  // holds three (default filters, per-account index, label mapping), so opening the
  // form dumped the whole panel on you before you had touched anything — and it was
  // the one provider that behaved differently.

  // Hydrate the per-source label mapping (webhook_label_mapping) from the saved
  // config value. Stored as arrays; rendered as arrays for FilterDropdown multi-select.
  useEffect(() => {
    const raw = editData?.integration_config_values?.webhook_label_mapping;
    if (!raw) return;
    const parsed = safeJSONParse(raw) || {};
    const toArr = (arr) => (Array.isArray(arr) ? arr.filter(Boolean) : []);
    setLabelMapping({
      subject_name_labels: toArr(parsed.subject_name_labels),
      namespace_labels: toArr(parsed.namespace_labels),
      severity_labels: toArr(parsed.severity_labels),
    });
  }, [editData]);

  // Helper function to check if condition values match current value
  const checkConditionMatch = (conditionValues, currentValue) => {
    if (Array.isArray(conditionValues)) {
      return conditionValues.includes(currentValue);
    }
    return conditionValues === currentValue;
  };

  // Helper function to check if a field should be visible
  const shouldShowField = (_key, field, valuesOverride, _visiting) => {
    const values = valuesOverride || formValues;
    if (field.hidden) {
      return false;
    }
    // Always show fields without show_when or required_when conditions
    if (!field.show_when && !field.required_when) {
      return true;
    }

    // Helper to check that a dependency field is itself visible (with recursion guard)
    const isDependencyVisible = (conditionKey) => {
      const depField = config?.properties?.[conditionKey];
      if (!depField) return true; // unknown field, assume visible
      // Prevent infinite recursion: if we're already checking this key up the call stack
      if (_visiting && _visiting.has(conditionKey)) return true;
      const visiting = new Set(_visiting || []);
      visiting.add(_key);
      return shouldShowField(conditionKey, depField, values, visiting);
    };

    let shouldShow = false;
    // Check show_when conditions (all must be satisfied)
    if (field.show_when) {
      shouldShow = true;
      for (const [conditionKey, conditionValues] of Object.entries(field.show_when)) {
        if (!isDependencyVisible(conditionKey)) {
          shouldShow = false;
          break;
        }
        const currentValue = values[conditionKey];
        if (!checkConditionMatch(conditionValues, currentValue)) {
          shouldShow = false;
          break;
        }
      }
    }

    // Check required_when conditions (all must be satisfied)
    if (field.required_when) {
      let allRequiredConditionsMet = true;
      for (const [conditionKey, conditionValues] of Object.entries(field.required_when)) {
        if (!isDependencyVisible(conditionKey)) {
          allRequiredConditionsMet = false;
          break;
        }
        const currentValue = values[conditionKey];
        if (!checkConditionMatch(conditionValues, currentValue)) {
          allRequiredConditionsMet = false;
          break;
        }
      }
      if (allRequiredConditionsMet) {
        shouldShow = true;
      }
    }

    return shouldShow;
  };

  // Helper function to check if a field is required based on current form state
  const isFieldRequired = (key, field) => {
    // account_id and integration_config_name are always required
    if (key === 'account_id' || key === 'integration_config_name') return true;

    // Check if it's in the base required array
    const isBaseRequired = config.required?.includes(key);

    // Check required_when conditions (all must be satisfied)
    if (field.required_when) {
      let allRequiredConditionsMet = true;
      for (const [conditionKey, conditionValues] of Object.entries(field.required_when)) {
        // If the dependency field is hidden, the required condition is not met
        const depField = config?.properties?.[conditionKey];
        if (depField && !shouldShowField(conditionKey, depField)) {
          allRequiredConditionsMet = false;
          break;
        }
        const currentValue = formValues[conditionKey];
        if (!checkConditionMatch(conditionValues, currentValue)) {
          allRequiredConditionsMet = false;
          break;
        }
      }
      if (allRequiredConditionsMet) {
        return true;
      }
    }

    return isBaseRequired;
  };

  // Helper function to sort fields by priority (highest first)
  const getSortedFieldKeys = () => {
    const visibleFields = Object.keys(config?.properties || {}).filter((key) => shouldShowField(key, config.properties[key]));

    return visibleFields.sort((a, b) => {
      const fieldA = config.properties[a];
      const fieldB = config.properties[b];

      // Get priority values, default to 0 if not specified
      const priorityA = fieldA.priority || 0;
      const priorityB = fieldB.priority || 0;

      // Sort in descending order (highest priority first)
      return priorityB - priorityA;
    });
  };

  const renderAdvancedToggle = () => (
    <Box
      sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer', mb: ds.space[2] }}
      onClick={() => setAdvancedOpen((v) => !v)}
      data-testid='advanced-settings-toggle'
    >
      <KeyboardArrowDownIcon
        sx={{ transform: advancedOpen ? 'rotate(0deg)' : 'rotate(-90deg)', transition: 'transform 0.2s', color: ds.brand[500] }}
      />
      <Typography sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}>
        Advanced Settings
      </Typography>
    </Box>
  );

  // Form-context-dependent autocomplete (e.g. Hive column names, Confluence
  // pages), fed by the field's auto_generate_func. freeSolo lets the customer
  // adopt a typed value that isn't in the suggestions. Array-typed fields
  // collect several values as chips; the stored form is the comma-joined
  // values (see joinConfigArray).
  const renderAutogenField = (key, field, isRequired, errorText) => {
    const ag = autogenState[key] || { options: [], message: '', loading: false };
    const isMulti = field.type === 'array';
    const fieldLabel = field.display_name || snakeToTitleCase(key);
    // Selected entries also render as a removable row below the field. The
    // dropdown can only untick what it can list, so a picker whose suggestions
    // failed to load — an edit form with no re-typed secret, most of all —
    // leaves the field-level clear as the only way out, and that drops every
    // entry at once.
    const selectedValues = isMulti && Array.isArray(formValues[key]) ? formValues[key] : [];
    const labelForValue = (v) => ag.options.find((opt) => String(opt?.value ?? opt) === String(v))?.label ?? String(v);
    return (
      <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
        <Typography
          variant='body2'
          sx={{
            color: ds.gray[400],
            fontSize: 'var(--ds-text-small)',
            lineHeight: 1.5,
            mb: ds.space[2],
            pl: ds.space[1],
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[1],
          }}
        >
          {field.advanced ? fieldLabel : field.description}
          {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
          {field.advanced && (
            <Tooltip title={field.description} maxWidth='360px'>
              <Box component='span' sx={{ cursor: 'pointer', display: 'inline-flex', alignItems: 'center' }}>
                <SafeIcon src={infoIcon} alt='info' width={14} height={14} />
              </Box>
            </Tooltip>
          )}
        </Typography>
        <FilterDropdown
          key={key}
          id={toKebabCase(field.display_name || key)}
          label={fieldLabel}
          options={ag.options}
          multiple={isMulti}
          limitTag={3}
          value={isMulti ? (Array.isArray(formValues[key]) ? formValues[key] : []) : formValues[key] || ''}
          freeSolo
          onSelect={(_event, value) =>
            handleChange(key, isMulti ? (value || []).map((v) => (v && typeof v === 'object' ? v.value : v)) : value?.value ?? value)
          }
          isOptionsLoading={ag.loading}
          sx={{ width: '100%' }}
          disabled={field.disabled || field.allow_edit === false}
          searchPlaceholder={field.search_placeholder || 'Search or type to add…'}
        />
        {selectedValues.length > 0 && (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[2], mt: ds.space[2] }} data-testid={`${key}-selected`}>
            {selectedValues.map((v) => (
              <Box
                key={String(v)}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: ds.space[1],
                  border: `1px solid ${ds.gray[200]}`,
                  borderRadius: 'var(--ds-radius-sm)',
                  pl: ds.space[2],
                  pr: ds.space[1],
                  py: ds.space[1],
                  fontSize: 'var(--ds-text-small)',
                  color: ds.gray[600],
                }}
              >
                <span>{labelForValue(v)}</span>
                <Box
                  component='button'
                  type='button'
                  aria-label={`Remove ${labelForValue(v)}`}
                  data-testid={`${key}-remove-${String(v)}`}
                  onClick={() =>
                    handleChange(
                      key,
                      selectedValues.filter((x) => x !== v)
                    )
                  }
                  sx={{
                    border: 'none',
                    background: 'none',
                    cursor: 'pointer',
                    lineHeight: 1,
                    padding: ds.space[1],
                    color: ds.gray[500],
                    fontSize: 'var(--ds-text-small)',
                    '&:hover': { color: ds.red[500] },
                  }}
                >
                  ×
                </Box>
              </Box>
            ))}
          </Box>
        )}
        {errorText && (
          <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)' }}>
            {errorText}
          </Typography>
        )}
        {!errorText && ag.message && (
          <Typography variant='body2' sx={{ mt: 0.5, fontSize: 'var(--ds-text-caption)', color: ds.gray[400] }}>
            {ag.message}
          </Typography>
        )}
        {!errorText && !ag.message && !ag.loading && ag.options.length > 0 && (
          <Typography variant='body2' sx={{ mt: 0.5, fontSize: 'var(--ds-text-caption)', color: ds.gray[400] }}>
            {ag.options.length} suggestion{ag.options.length === 1 ? '' : 's'}
          </Typography>
        )}
      </Box>
    );
  };

  const handleChange = (key, value) => {
    if (isTestable && config.properties?.[key]?.is_testable) {
      setConnectionVerified(false);
      setEsIndexes([]);
    }
    setFormValues((prevValues) => {
      let updatedValues = { ...prevValues, [key]: value };
      if (key == 'account_id' && Array.isArray(value)) {
        // Multi-select autocomplete passes [{label, value}, ...] — flatten to ids.
        // Single-select passes a scalar already (handled by the spread above).
        updatedValues = {
          ...updatedValues,
          account_id: value.map((a) => (typeof a === 'object' && a !== null ? a.value : a)),
        };
      }
      if (integrationName == 'LLM' && key == 'account_id' && value) {
        if (integrationData) {
          const selectedAccount = integrationData.find((it) => it.account_id === value);
          if (selectedAccount) {
            updatedValues = { ...updatedValues, integration_config_name: selectedAccount.name };
            setConfig((prevConfig) => ({
              ...prevConfig,
              properties: {
                ...prevConfig.properties,
                integration_config_name: {
                  ...prevConfig.properties.integration_config_name,
                  disabled: true,
                },
              },
            }));
          }
        }
      }

      // Clear values for fields that are no longer visible
      const newConfig = { ...config };
      Object.keys(newConfig.properties || {}).forEach((fieldKey) => {
        const field = newConfig.properties[fieldKey];
        // If this field should not be shown anymore, clear its value
        if (!shouldShowField(fieldKey, field, updatedValues) && fieldKey !== key) {
          delete updatedValues[fieldKey];
        }
      });

      return updatedValues;
    });

    // Clear any existing errors for this field
    if (errors[key]) {
      setErrors((prev) => {
        const newErrors = { ...prev };
        delete newErrors[key];
        return newErrors;
      });
    }
  };

  const handleAgentProviderToggle = async (cloudAccountId, providerKey, newValue) => {
    setIsSubmitting(true);
    const allAccountIds = agentAccountProviders.map((acc) => acc.cloud_account_id);
    const providerMap = {};
    agentAccountProviders.forEach((acc) => {
      providerMap[acc.cloud_account_id] = acc.cloud_account_id === cloudAccountId ? String(newValue) : String(acc[providerKey]);
    });

    const payload = {
      ...(editData?.id && { integration_id: editData.id }),
      integration_name: integrationName,
      account_ids: allAccountIds,
      integration_config_name: editData?.name,
      skip_validation: true,
      source: editData?.source || 'user',
      integration_config_values: [{ name: providerKey, value: JSON.stringify(providerMap), is_encrypted: false }],
    };

    try {
      const res = await apiIntegrations.addIntegrations(payload);
      if (res?.data?.data?.integrations_create_config) {
        setAgentAccountProviders((prev) => prev.map((acc) => (acc.cloud_account_id === cloudAccountId ? { ...acc, [providerKey]: newValue } : acc)));
        if (editData?.id) {
          listIntegrationConfigurationById(editData?.id);
        }

        // Invalidate cached provider for the affected account
        const providerCacheSuffix = { default_log_provider: '-log', default_traces_provider: '-traces', default_metrics_provider: '-metrics' };
        const suffix = providerCacheSuffix[providerKey];
        if (suffix) {
          cache.del(`${cloudAccountId}${suffix}`);
        }

        snackbar.success(`Account mapping ${newValue ? 'enabled' : 'disabled'} successfully`);
      } else {
        snackbar.error(`${parseHttpResponseBodyMessage(res?.data)}`);
      }
    } catch {
      snackbar.error('Failed to update account mapping');
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleAddRule = () => {
    setRules([...rules, { match: [{ key: '', value: '' }], accountId: '' }]);
  };

  const handleRemoveRule = (ruleIdx) => {
    const next = rules.filter((_, i) => i !== ruleIdx);
    setRules(next.length > 0 ? next : [{ match: [{ key: '', value: '' }], accountId: '' }]);
  };

  const handleRuleAccountChange = (ruleIdx, accountId) => {
    setRules(rules.map((r, i) => (i === ruleIdx ? { ...r, accountId } : r)));
  };

  const handleAddCondition = (ruleIdx) => {
    setRules(rules.map((r, i) => (i === ruleIdx ? { ...r, match: [...r.match, { key: '', value: '' }] } : r)));
  };

  const handleRemoveCondition = (ruleIdx, condIdx) => {
    setRules(
      rules.map((r, i) => {
        if (i !== ruleIdx) return r;
        const nextMatch = r.match.filter((_, j) => j !== condIdx);
        return { ...r, match: nextMatch.length > 0 ? nextMatch : [{ key: '', value: '' }] };
      })
    );
  };

  const handleConditionChange = (ruleIdx, condIdx, field, value) => {
    setRules(
      rules.map((r, i) => {
        if (i !== ruleIdx) return r;
        const nextMatch = r.match.map((c, j) => (j === condIdx ? { ...c, [field]: value } : c));
        return { ...r, match: nextMatch };
      })
    );
  };

  // --- Default Log / Trace Filters (per-account) handlers ---
  // DefaultFiltersCard owns the row and card mutations; the modal only stores the
  // result and drops validation results the edit invalidated. Only the cards that
  // actually changed lose their result — clearing all of them would throw away a
  // neighbouring card's answer on every keystroke.
  const handleDefaultFilterRulesChange = (next) => {
    setDefaultFilterRules(next);
    setColumnValidation((prev) => dropStaleValidation(prev, defaultFilterRules, next));
  };

  // On-demand: fetch the card's account log labels and flag any column that isn't a known label.
  const handleValidateCard = async (cardIdx) => {
    const card = defaultFilterRules[cardIdx];
    const cols = (card.filters || []).map((f) => (f.key || '').trim()).filter(Boolean);
    if (!card.accountId || cols.length === 0) return;
    setColumnValidation((prev) => ({ ...prev, [cardIdx]: { loading: true, done: false, invalid: [] } }));
    try {
      const res = await observability.fetchLogLabels({ account_id: card.accountId });
      // logs_list_labels returns [{ label, attributes }] — compare against the label names.
      const labels = (res?.data?.data?.logs_list_labels || []).map((l) => (typeof l === 'string' ? l : l?.label ?? l?.name)).filter(Boolean);
      const invalid = labels.length > 0 ? cols.filter((c) => !labels.includes(c)) : [];
      setColumnValidation((prev) => ({ ...prev, [cardIdx]: { loading: false, done: true, invalid } }));
    } catch {
      setColumnValidation((prev) => ({ ...prev, [cardIdx]: { loading: false, done: false, invalid: [] } }));
      snackbar.error('Could not fetch log labels to validate columns.');
    }
  };

  // --- Per-account ES index (Advanced Settings) handlers ---
  const handleAddIndexCard = () => {
    setIndexRules([...indexRules, { accountId: '', log_index: '', metrics_index: '', trace_index: '' }]);
  };
  const handleRemoveIndexCard = (cardIdx) => {
    setIndexRules(indexRules.filter((_, i) => i !== cardIdx));
  };
  const handleIndexCardChange = (cardIdx, field, value) => {
    setIndexRules(indexRules.map((c, i) => (i === cardIdx ? { ...c, [field]: value } : c)));
  };

  const handleCloseModal = (trigger) => {
    setIsSubmitting(false);
    setConfig({});
    setFormValues({});
    setErrors({});
    setShowModal(false);
    setRules([{ match: [{ key: '', value: '' }], accountId: '' }]);
    setDefaultFilterRules([emptyFilterCard()]);
    setDefaultTraceFilterRules([emptyFilterCard()]);
    setColumnValidation({});
    setIndexRules([{ accountId: '', log_index: '', metrics_index: '', trace_index: '' }]);
    setLabelMappingCards([{ accountId: '', rows: [{ canonical: '', field: '' }] }]);
    setTraceLabelMappingCards([{ accountId: '', rows: [{ canonical: '', field: '' }] }]);
    setSavedTraceLabelMappingCards([]);
    setSavedLabelMappingCards([]);
    setAdvancedOpen(false);
    setLabelMapping({ subject_name_labels: [], namespace_labels: [], severity_labels: [] });
    setAgentAccountProviders([]);
    setProviderFields([]);
    setVmAgentCredentials(null);
    setIsTesting(false);
    setConnectionVerified(hasEditData(editData));
    handleClose(trigger);
  };

  const validateForm = () => {
    const visibleFields = Object.keys(config.properties || {}).filter((key) => shouldShowField(key, config.properties[key]));
    const filledKeys = visibleFields.filter(
      (key) =>
        formValues[key] !== '' &&
        formValues[key] !== null &&
        formValues[key] !== undefined &&
        !(Array.isArray(formValues[key]) && formValues[key].length === 0)
    );
    const requiredVisibleFields = visibleFields.filter((key) => isFieldRequired(key, config.properties[key]));
    const missingElements = requiredVisibleFields.filter((item) => !filledKeys.includes(item));
    if (missingElements.length > 0) {
      setErrors(Object.fromEntries(missingElements.map((key) => [key, `${key} param is required`])));
      return false;
    }

    const ENCRYPTED_MASK = '*************************************************';
    const patternErrors = {};
    for (const key of filledKeys) {
      const field = config.properties[key];
      if (!field?.pattern) continue;
      const value = formValues[key];
      if (typeof value !== 'string') continue;
      // Skip encrypted fields whose value is the mask placeholder (unchanged on edit).
      if (field.is_encrypted && value === ENCRYPTED_MASK) continue;
      let regex;
      try {
        regex = new RegExp(field.pattern);
      } catch {
        continue;
      }
      if (!regex.test(value)) {
        patternErrors[key] = `${field.display_name || snakeToTitleCase(key)} format is invalid`;
      }
    }
    if (Object.keys(patternErrors).length > 0) {
      setErrors(patternErrors);
      return false;
    }
    return true;
  };

  // Skip validation when editing if no testable fields changed
  const shouldSkipValidation = () => {
    if (!editData) return false; // New integration - always validate

    // Check if any testable field changed
    const testableFieldChanged = Object.keys(formValues).some((key) => {
      const field = config.properties?.[key];
      if (!field?.is_testable) return false;

      const currentValue = formValues[key];

      // For encrypted fields, if value is masked, it hasn't changed
      if (field?.is_encrypted && currentValue === '*************************************************') {
        return false;
      }

      const originalValue = editData?.integration_config_values?.[key];
      return currentValue !== originalValue;
    });

    // Skip validation if no testable fields changed
    return !testableFieldChanged;
  };

  // Build the config-values payload for connectivity / index probes: booleans and
  // integers stringified; untyped encrypted secrets sent as the stored ciphertext
  // with is_encrypted=true (omit-to-keep). Shared by Test Connection and the ES
  // index fetch.
  const buildProbeConfigValues = () => {
    const { account_id: _a, integration_config_name: _n, account_mapping: _m, ...restFormValues } = formValues;
    return Object.entries(restFormValues).map(([key, value]) => {
      const field = config.properties?.[key];
      const fieldType = field?.type;
      let transformedValue = value;
      if (fieldType === 'boolean' || fieldType === 'bool' || fieldType === 'integer') {
        transformedValue = String(value);
      } else if (field?.is_encrypted && editData?.integration_config_values?.[key] && value === ENCRYPTED_MASK) {
        transformedValue = editData?.integration_config_values?.[key];
      } else if (Array.isArray(value)) {
        transformedValue = joinConfigArray(value);
      }
      return {
        name: key,
        // Both coerced, never left undefined. `&&` yields undefined when the schema
        // carries no is_encrypted, and callers that inline this into a GraphQL string
        // (gqlStringify) then emit the bare token `undefined`, which the Go decoder
        // rejects with "expected type 'bool', got unconvertible type 'string'". The
        // variables-based callers never saw it, because JSON.stringify drops undefined.
        value: transformedValue ?? '',
        is_encrypted: !!(field?.is_encrypted && editData?.integration_config_values?.[key] && value === ENCRYPTED_MASK),
      };
    });
  };

  // One field-list probe shared by both Advanced Settings sections that need it:
  // Default Log Filters (which column to filter on) and Log Label Mapping (which field
  // holds each concept) ask the backend the same question. Fires only once the
  // connection is verified, and clears when it is invalidated.
  const logFieldAccountPairs = useMemo(() => {
    const pairs = [];
    const seen = new Set();
    const add = (accountId) => {
      if (!accountId) return;
      const index = indexForAccount(indexRules, accountId, formValues.log_index);
      const key = fieldOptionsKey(accountId, index);
      if (seen.has(key)) return;
      seen.add(key);
      pairs.push({ accountId, index });
    };
    labelMappingCards.forEach((c) => add(c.accountId));
    defaultFilterRules.forEach((c) => add(c.accountId));
    return pairs;
  }, [labelMappingCards, defaultFilterRules, indexRules, formValues.log_index]);

  const logFieldOptions = useLogFieldOptions({
    enabled: advancedSettingsUnlocked,
    provider: integrationName,
    providerSource: editData?.source || 'user',
    buildProbeConfigValues,
    accountIndexPairs: logFieldAccountPairs,
  });

  const currentAccountIds = () =>
    Array.isArray(formValues.account_id) ? formValues.account_id : formValues.account_id ? [formValues.account_id] : [];

  // Fetch the ES cluster's queryable indices for the per-account index picker,
  // using the current (unsaved) config values. Called once the connection is verified.
  const fetchESIndexes = async () => {
    setEsIndexesLoading(true);
    try {
      const res = await apiIntegrations.listESIndexes(
        integrationName === 'elasticsearch' ? 'ES' : integrationName,
        currentAccountIds(),
        buildProbeConfigValues(),
        editData?.source || 'user',
        editData?.id
      );
      setEsIndexes(Array.isArray(res?.indexes) ? res.indexes : []);
      if (res?.error) snackbar.error(`Failed to load cluster indices: ${res.error}`);
    } catch {
      setEsIndexes([]);
    } finally {
      setEsIndexesLoading(false);
    }
  };

  // Populate the ES index picker once the connection is verified — on edit-open
  // (connectionVerified starts true) or after a successful Test Connection. The
  // esIndexesLoading guard prevents a duplicate fetch when deps change together.
  useEffect(() => {
    const isES = integrationName === 'ES' || integrationName === 'elasticsearch';
    if (!openModal || isLoadingSchema || !isES || !connectionVerified) return;
    if (!formValues?.url || esIndexesLoading) return;
    fetchESIndexes();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openModal, connectionVerified, isLoadingSchema, formValues?.url]);

  // Label names available on the connected Prometheus, shown under the
  // additional-labels field so the operator knows what to put in the JSON.
  // metrics_list_labels resolves the endpoint from the SAVED integration row, so
  // this only runs on edit — a not-yet-created integration has nothing to read.
  const [promLabels, setPromLabels] = useState([]);

  // Alert delivery for a Prometheus connected without an agent. There is no
  // runner in the cluster to receive Alertmanager's webhook, so alerts reach
  // Nudgebee through the public Alertmanager-webhook integration instead — and
  // an operator setting this up has no way to discover that from this form. On
  // edit, list the account's Alertmanager webhooks and show the receiver URL
  // with the account name pinned as the cluster, the same value an agent-
  // delivered alert would carry.
  const isDirectPrometheus = integrationName === 'prometheus' && !!editData?.id && editData?.source !== 'agent';
  const [alertmanagerWebhooks, setAlertmanagerWebhooks] = useState([]);

  useEffect(() => {
    if (!openModal || !isDirectPrometheus) return;
    let cancelled = false;
    apiIntegrations
      .listIntegrations({ type: 'prometheus_alertmanager_webhook', limit: 50 })
      .then((res) => {
        if (!cancelled) {
          setAlertmanagerWebhooks(res?.data?.data?.integrations_list?.rows || []);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setAlertmanagerWebhooks([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [openModal, isDirectPrometheus]);

  // The endpoint returns EVERY label in the Prometheus — hundreds — which is
  // unusable as a list and overflows the modal. Surface only the ones that
  // plausibly identify a cluster (what this field is for) and report the rest
  // as a count, so the hint stays one or two lines.
  const promLabelHint = useMemo(() => {
    if (promLabels.length === 0) {
      return null;
    }
    const candidates = promLabels.filter((l) => /cluster|region|environment|^env$|tenant|datacenter|^dc$|site/i.test(l)).slice(0, 8);
    if (candidates.length === 0) {
      return `${promLabels.length} labels available on this endpoint — none look cluster-identifying. Type the label name your setup uses.`;
    }
    return `Likely cluster labels: ${candidates.join(', ')} — ${promLabels.length} labels available in total.`;
  }, [promLabels]);

  useEffect(() => {
    if (!openModal || isLoadingSchema || integrationName !== 'prometheus' || !editData?.id) return;
    const accountId = currentAccountIds()[0];
    if (!accountId) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await observability.metricsLabelList(accountId, '');
        const raw = res?.data?.data?.metrics_list_labels || [];
        const names = raw
          .map((l) => (typeof l === 'string' ? l : l?.label))
          .filter(Boolean)
          .filter((l) => l !== '__name__');
        if (!cancelled) {
          setPromLabels(names);
        }
      } catch {
        // A failed lookup must not block editing: the field still accepts input.
        if (!cancelled) {
          setPromLabels([]);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openModal, isLoadingSchema, integrationName, editData?.id, formValues?.account_id]);

  const handleTestConnection = async () => {
    if (!validateForm()) return;
    setIsTesting(true);
    try {
      const result = await apiIntegrations.testIntegrationConnectionByConfig(
        integrationName === 'elasticsearch' ? 'ES' : integrationName,
        currentAccountIds(),
        buildProbeConfigValues(),
        editData?.source || 'user',
        editData?.id
      );
      if (result?.success) {
        setConnectionVerified(true);
        snackbar.success(`${snakeToTitleCase(integrationName)} connection successful`);
      } else {
        snackbar.error(result?.error || `${snakeToTitleCase(integrationName)} connection test failed`);
      }
    } catch {
      snackbar.error(`Failed to test ${snakeToTitleCase(integrationName)} connection`);
    } finally {
      setIsTesting(false);
    }
  };

  const submitForm = async () => {
    if (!validateForm()) {
      return;
    }
    if (LOG_FILTER_INTEGRATIONS.has(integrationName) && hasCardMissingAccount(defaultFilterRules)) {
      // Any card that has a filter entered must have an account selected.
      snackbar.error('Default Log Filters: please select an account for each filter card.');
      return;
    }
    if (TRACE_INTEGRATIONS.has(integrationName) && hasCardMissingAccount(defaultTraceFilterRules)) {
      snackbar.error('Default Trace Filters: please select an account for each filter card.');
      return;
    }
    if (supportsLogLabelMapping) {
      // Any mapping card with a row entered must have an account selected — without
      // one there is nothing to attach the mapping to and it would be silently dropped.
      const mappingCardMissingAccount = labelMappingCards.some(
        (c) => !c.accountId && (c.rows || []).some((r) => (r.canonical || '').trim() || (r.field || '').trim())
      );
      if (mappingCardMissingAccount) {
        snackbar.error('Log Label Mapping: please select an account for each mapping card.');
        return;
      }
    }
    if (showTraceLabelMapping) {
      const traceMappingCardMissingAccount = traceLabelMappingCards.some(
        (c) => !c.accountId && (c.rows || []).some((r) => (r.canonical || '').trim() || (r.field || '').trim())
      );
      if (traceMappingCardMissingAccount) {
        snackbar.error('Trace Label Mapping: please select an account for each mapping card.');
        return;
      }
    }
    if (integrationName === 'ES' || integrationName === 'elasticsearch') {
      // Any index card with an index entered must have an account selected.
      const indexCardMissingAccount = indexRules.some(
        (c) => !c.accountId && ((c.log_index || '').trim() || (c.metrics_index || '').trim() || (c.trace_index || '').trim())
      );
      if (indexCardMissingAccount) {
        snackbar.error('Per-Account Index: please select an account for each index card.');
        return;
      }
      // Validate each provided index resolves against the cluster — exact name or a
      // glob pattern (logs-generic.* matches logs-generic.otel-default). Blank fields
      // are ignored (they fall back to the top-level index). Skipped when the index
      // list couldn't be loaded, so a fetch failure never blocks saving.
      if (esIndexes.length > 0) {
        const matchesCluster = (raw) => {
          const p = raw.trim();
          if (!p || esIndexes.includes(p)) return true;
          const rx = new RegExp('^' + p.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*') + '$');
          return esIndexes.some((idx) => rx.test(idx));
        };
        const accLabel = (id) => accountOptions.find((o) => o.value === id)?.label || id;
        for (const card of indexRules) {
          const bad = [
            ['log_index', 'Log Index'],
            ['metrics_index', 'Metrics Index'],
            ['trace_index', 'Trace Index'],
          ].find(([field]) => (card[field] || '').trim() && !matchesCluster(card[field]));
          if (bad) {
            snackbar.error(`${bad[1]} "${(card[bad[0]] || '').trim()}" not found in the cluster for account ${accLabel(card.accountId)}.`);
            return;
          }
        }
      }
    }
    setIsSubmitting(true);

    if (['pagerduty', 'servicenow', 'github', 'gitlab', 'zenduty', 'freshdesk'].includes(integrationName)) {
      const { integration_config_name, ...restFormValues } = formValues;
      const transformedValues = Object.entries(restFormValues).reduce((acc, [key, value]) => {
        const field = config.properties?.[key];
        const fieldType = field?.type;
        let transformedValue = value;
        if (fieldType === 'boolean' || fieldType === 'bool') {
          transformedValue = String(value);
        } else if (
          field?.is_encrypted &&
          editData?.integration_config_values?.[key] &&
          value === '*************************************************'
        ) {
          // User didn't re-enter the secret. Send empty so the backend preserves the stored value.
          transformedValue = '';
        }
        acc[key] = transformedValue;
        return acc;
      }, {});
      const configValuesArray = Object.entries(transformedValues)
        .filter(([key]) => {
          const field = config.properties?.[key];
          return field && (field.type === 'boolean' || field.type === 'bool');
        })
        .map(([key, value]) => ({
          name: key,
          value: String(value),
        }));

      const bodyData = {
        name: integration_config_name,
        password: transformedValues.password,
        url: transformedValues.url,
        username: transformedValues.username,
        tool: integrationName,
        ...(configValuesArray.length > 0 && { config_values: configValuesArray }),
      };

      try {
        const configRes = await apiTicketIntegrations.listTicketConfigurations({
          tool: integrationName,
        });
        const toolConfList = configRes?.data || [];
        const isEditMode = editData && Object.keys(editData).length > 0;
        const duplicateExists = toolConfList.some((config) => config.name === bodyData.name && (!isEditMode || config.id !== editData.id));
        if (duplicateExists) {
          setErrors({
            integration_config_name: `${bodyData.name} already exists. Please choose a different name.`,
          });
          setIsSubmitting(false);
          return;
        }

        const res = await apiIntegrations.createTicketIntegration(bodyData);

        // Check for GraphQL errors first (errors are at res.data.errors, not res.data.data.errors)
        if (res?.data?.errors?.length > 0) {
          snackbar.error(res.data.errors[0]?.message || `Failed to Add ${integrationName} Account`);
          handleCloseModal(false);
          return;
        }

        // Check for success
        if (res?.data?.data?.ticket_integration_create_config) {
          snackbar.success(getAccountCreationSuccessMsg(integrationName.toUpperCase()));
          handleCloseModal(true);
        } else {
          snackbar.error(`Failed to Add ${integrationName} Account`);
          handleCloseModal(false);
        }
      } catch (error) {
        const errorMessage = error?.response?.data?.errors?.[0]?.message || `Failed to Add ${integrationName} Account`;
        snackbar.error(errorMessage);
        handleCloseModal(false);
      } finally {
        setIsSubmitting(false);
      }
      return;
    }

    const { account_id, integration_config_name, account_mapping: _, ...restFormValues } = formValues;
    const transformedValues = Object.entries(restFormValues).map(([key, value]) => {
      const field = config.properties?.[key];
      const fieldType = field?.type;
      let transformedValue = value;
      if (fieldType === 'boolean' || fieldType === 'bool' || fieldType === 'integer') {
        transformedValue = String(value);
      } else if (field?.is_encrypted && editData?.integration_config_values?.[key] && value === '*************************************************') {
        transformedValue = editData?.integration_config_values?.[key];
      } else if (Array.isArray(value)) {
        transformedValue = joinConfigArray(value);
      }
      return {
        name: key,
        value: transformedValue,
        is_encrypted:
          field?.is_encrypted && editData?.integration_config_values?.[key] && value === '*************************************************'
            ? true
            : false,
      };
    });

    // Account mapping rules — emit { rules: [{ match: {k:v,...}, accountId }] }.
    // A rule is kept only if it has an account selected and at least one
    // non-empty (key, value) condition. Empty fields within an otherwise valid
    // rule are dropped silently so users aren't blocked by trailing blank rows.
    //
    // Comma-separated values in a single condition serialize as a JSON array
    // for value-OR semantics on the backend (e.g. "na, eu" → ["na","eu"]).
    // Single values stay as strings to keep the wire format compact and to
    // avoid churn on existing single-value configs.
    if (integrationName.includes('_webhook') && integrationName !== 'workflow_webhook') {
      const cleanedRules = rules
        .map((r) => {
          const match = {};
          (r.match || []).forEach((c) => {
            const k = (c.key || '').trim();
            const raw = (c.value || '').trim();
            if (!k || !raw) return;
            const values = raw
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean);
            if (values.length === 0) return;
            match[k] = values.length === 1 ? values[0] : values;
          });
          return { match, accountId: r.accountId || '' };
        })
        .filter((r) => r.accountId && Object.keys(r.match).length > 0);

      if (cleanedRules.length > 0) {
        transformedValues.push({
          name: 'account_mapping',
          value: JSON.stringify({ rules: cleanedRules }),
          is_encrypted: false,
        });
      }
    }

    // Per-account Default Log Filters → default_filters. Keep a card only if it has
    // an account and at least one non-empty (key, value); each row is stored as
    // { key, op:'_eq', value } (equality only for now). Emit an empty array when a
    // previously-saved config is cleared so filters can be removed.
    if (LOG_FILTER_INTEGRATIONS.has(integrationName)) {
      const cleanedFilters = serializeDefaultFilters(defaultFilterRules);
      const previouslySet = !!editData?.integration_config_values?.default_filters;
      if (cleanedFilters.length > 0 || previouslySet) {
        transformedValues.push({
          name: 'default_filters',
          value: JSON.stringify(cleanedFilters),
          is_encrypted: false,
        });
      }
    }

    // Per-account Default Trace Filters → default_trace_filters. Same shape and the
    // same clear-to-empty-array rule as the log filters above, under a separate config
    // name: one integration record often serves both logs and traces, so sharing
    // `default_filters` would apply log filters to trace queries.
    if (TRACE_INTEGRATIONS.has(integrationName)) {
      const cleanedTraceFilters = serializeDefaultFilters(defaultTraceFilterRules);
      const previouslySet = !!editData?.integration_config_values?.default_trace_filters;
      if (cleanedTraceFilters.length > 0 || previouslySet) {
        transformedValues.push({
          name: 'default_trace_filters',
          value: JSON.stringify(cleanedTraceFilters),
          is_encrypted: false,
        });
      }
    }

    // Per-account log label mapping → log_label_mappings. Emitted for every log /
    // observability-platform integration; the backend auto-allows the key for exactly
    // those categories. Emit an empty array when a previously-saved mapping is cleared,
    // because config values are upserted per name and omitting the key would leave the
    // old mapping in force.
    if (supportsLogLabelMapping) {
      const cleanedMappings = serializeLogLabelMappings(labelMappingCards);
      const mappingPreviouslySet = !!editData?.integration_config_values?.log_label_mappings;
      if (cleanedMappings.length > 0 || mappingPreviouslySet) {
        transformedValues.push({
          name: 'log_label_mappings',
          value: JSON.stringify(cleanedMappings),
          is_encrypted: false,
        });
      }
    }

    // Per-account trace label mapping → trace_label_mappings. Same shape and the same
    // clear-to-empty-array rule as the log mapping above, under a separate config name:
    // one integration record often serves both signals, so sharing `log_label_mappings`
    // would apply a log field mapping to trace queries.
    if (showTraceLabelMapping) {
      const cleanedTraceMappings = serializeLogLabelMappings(traceLabelMappingCards);
      const tracePreviouslySet = !!editData?.integration_config_values?.trace_label_mappings;
      if (cleanedTraceMappings.length > 0 || tracePreviouslySet) {
        transformedValues.push({
          name: 'trace_label_mappings',
          value: JSON.stringify(cleanedTraceMappings),
          is_encrypted: false,
        });
      }
    }

    // Per-account ES index override → index_account_mapping. Keep a card only if it
    // has an account and at least one non-empty index. Emit an empty array when a
    // previously-saved config is cleared, so a stale mapping can be removed — config
    // values are upserted per-name (no full wipe), so omitting it would leave the old value.
    if (integrationName === 'ES' || integrationName === 'elasticsearch') {
      const cleanedIndexRows = indexRules
        .map((c) => {
          const row = { account_id: c.accountId || '' };
          const log = (c.log_index || '').trim();
          const metrics = (c.metrics_index || '').trim();
          const trace = (c.trace_index || '').trim();
          if (log) row.log_index = log;
          if (metrics) row.metrics_index = metrics;
          if (trace) row.trace_index = trace;
          return row;
        })
        .filter((r) => r.account_id && (r.log_index || r.metrics_index || r.trace_index));
      const indexPreviouslySet = !!editData?.integration_config_values?.index_account_mapping;
      if (cleanedIndexRows.length > 0 || indexPreviouslySet) {
        transformedValues.push({
          name: 'index_account_mapping',
          value: JSON.stringify(cleanedIndexRows),
          is_encrypted: false,
        });
      }
    }

    // Per-source subject/namespace/severity mapping → webhook_label_mapping.
    // Each field is a comma-separated list of label key specs; serialize to
    // arrays. Emit when any field is set, or when a saved mapping is being
    // cleared (empty arrays parse to a no-op on the backend) so users can
    // remove a previously-configured mapping.
    if (integrationName.includes('_webhook') && integrationName !== 'workflow_webhook') {
      const labelMappingPayload = {
        subject_name_labels: (labelMapping.subject_name_labels || []).filter(Boolean),
        namespace_labels: (labelMapping.namespace_labels || []).filter(Boolean),
        severity_labels: (labelMapping.severity_labels || []).filter(Boolean),
      };
      const hasAnyLabelMapping =
        labelMappingPayload.subject_name_labels.length > 0 ||
        labelMappingPayload.namespace_labels.length > 0 ||
        labelMappingPayload.severity_labels.length > 0;
      const previouslySet = !!editData?.integration_config_values?.webhook_label_mapping;
      if (hasAnyLabelMapping || previouslySet) {
        transformedValues.push({
          name: 'webhook_label_mapping',
          value: JSON.stringify(labelMappingPayload),
          is_encrypted: false,
        });
      }
    }

    setIsSubmitting(true);

    // Ticketing systems use ticket_integration_create_config (ticket server)
    // Webhooks and other integrations use integrations_create_config (services server)
    const ticketingIntegrations = ['pagerduty', 'jira', 'github', 'gitlab', 'zenduty', 'servicenow', 'freshdesk'];
    const isTicketingSystem = ticketingIntegrations.includes(integrationName);

    if (isTicketingSystem) {
      // Transform payload for ticket server format
      // Use transformed values for top-level fields so masked encrypted values
      // (like password) are correctly resolved to the original encrypted ciphertext
      const getTransformed = (fieldName) => transformedValues.find((v) => v.name === fieldName)?.value ?? formValues[fieldName];
      const ticketPayload = {
        name: integration_config_name,
        tool: integrationName,
        url: getTransformed('url'),
        username: getTransformed('username'),
        password: getTransformed('password'),
        auth_type: getTransformed('auth_type'),
        config_values: transformedValues.map((v) => ({ name: v.name, value: v.value })),
      };

      apiIntegrations
        .createTicketIntegration(ticketPayload)
        .then((res) => {
          const successId = res?.data?.data?.ticket_integration_create_config?.id;
          if (successId) {
            handleCloseModal(true);
          } else {
            snackbar.error(`${parseHttpResponseBodyMessage(res?.data)}`);
          }
        })
        .catch((err) => {
          console.error('Failed to create ticket integration:', err);
          snackbar.error(parseHttpResponseBodyMessage(err) || 'Failed to create ticket integration');
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    } else {
      if (!editData && integrationName.includes('_webhook')) {
        transformedValues.push({
          name: 'token',
          value: '',
          is_encrypted: false,
        });
      }

      let normalizedAccountIds;
      if (Array.isArray(account_id)) {
        normalizedAccountIds = account_id;
      } else if (account_id) {
        normalizedAccountIds = [account_id];
      } else {
        normalizedAccountIds = [];
      }

      const payload = {
        ...(editData?.id && { integration_id: editData.id }),
        integration_name: integrationName === 'elasticsearch' ? 'ES' : integrationName,
        account_ids: normalizedAccountIds,
        integration_config_name,
        skip_validation: shouldSkipValidation(),
        source: editData?.source || 'user',
        integration_config_values: transformedValues,
      };

      apiIntegrations
        .addIntegrations(payload)
        .then((res) => {
          const configs = res?.data?.data?.integrations_create_config?.configs || [];
          const isNewCreation = !editData?.name;
          if (configs.length > 0) {
            if (isNewCreation && integrationName.endsWith('_webhook')) {
              const findToken = configs.find((f) => f.name == 'token');
              if (findToken) {
                setResponse(findToken);
                setShowModal(true);
              }
            } else if (isNewCreation && integrationName === 'vm_agent') {
              const accessKey = configs.find((f) => f.name === 'access_key');
              const accessSecret = configs.find((f) => f.name === 'access_secret');
              if (accessKey && accessSecret) {
                setVmAgentCredentials({ accessKey: accessKey.value, accessSecret: accessSecret.value });
                setShowModal(true);
              } else {
                handleCloseModal(true);
              }
            } else {
              handleCloseModal(true);
            }
          } else {
            snackbar.error(`${parseHttpResponseBodyMessage(res?.data)}`);
          }
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    }
  };

  const webhookConfig = {
    pagerduty_webhook: {
      endpoint: 'pagerduty',
      message: 'Configure the following url in pagerduty webhook subscription',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/pagerduty_webhook/'),
        text: 'how to configure PagerDuty Webhook',
      },
    },
    zenduty_webhook: {
      endpoint: 'zenduty',
      message: 'Configure the following URL in ZenDuty outgoing webhook',
      learnMore: {
        url: 'https://docs.zenduty.com/docs/outgoing-webhooks',
        text: 'how to create Outgoing Webhooks in ZenDuty',
      },
    },
    prometheus_alertmanager_webhook: {
      endpoint: 'prometheus-alertmanager',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
    },
    datadog_webhook: {
      endpoint: 'datadog',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/datadog_webhook/#step-2-configure-datadog-webhook-integration'),
        text: 'how to configure Datadog Webhook payload',
      },
    },
    azure_monitor_webhook: {
      endpoint: 'azure-monitor',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
    },
    gcp_monitoring_webhook: {
      endpoint: 'gcp-monitoring',
      message: 'Configure the following URL as a webhook notification channel in GCP Cloud Monitoring',
    },
    servicenow_webhook: {
      endpoint: 'servicenow',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/servicenow_webhook/'),
        text: 'how to configure ServiceNow Webhook',
      },
    },
    newrelic_webhook: {
      endpoint: 'newrelic',
      message: 'Configure the following URL in New Relic notification destination',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/newrelic_webhook/'),
        text: 'how to configure New Relic Webhook',
      },
    },
    dynatrace_webhook: {
      endpoint: 'dynatrace',
      message: 'Configure the following URL in Dynatrace Settings \u2192 Integrations \u2192 Problem notifications',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/dynatrace_webhook/'),
        text: 'how to configure Dynatrace Webhook',
      },
    },
    splunk_webhook: {
      endpoint: 'splunk',
      message: 'Configure the following URL in your Splunk alerting webhook subscription',
    },
    grafana_webhook: {
      endpoint: 'grafana',
      message: 'Configure the following URL in your Grafana alerting webhook contact point',
    },
    solarwinds_webhook: {
      endpoint: 'solarwinds',
      message: 'Configure the following URL in SolarWinds Observability alert webhook action',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/solarwinds_webhook/'),
        text: 'how to configure SolarWinds Observability Webhook',
      },
    },
    elasticsearch_webhook: {
      endpoint: 'elasticsearch',
      message: 'Configure the following URL in a Kibana Webhook connector (Stack Management \u2192 Connectors)',
      learnMore: {
        url: docsUrl('/docs/integrations/Webhooks/elasticsearch_webhook/'),
        text: 'how to configure Elasticsearch/Kibana Webhook',
      },
    },
    openobserve_webhook: {
      endpoint: 'openobserve',
      message: 'Configure the following URL as a Webhook destination in OpenObserve (Management → Alert Destinations)',
      // OpenObserve delivers whatever the alert Template renders — there is no
      // fixed payload schema — so the destination is only half the setup. The
      // template below is what NudgeBee parses best; it is shown inline because
      // a user who skips it gets an event with no severity, subject or link.
      template: OPENOBSERVE_ALERT_TEMPLATE,
      templateMessage:
        'OpenObserve has no fixed webhook payload — the body is whatever the alert Template renders. Create a Template (Management → Templates, type: Webhook) with the JSON below and select it on the destination above.',
    },
    workflow_webhook: {
      endpoint: 'workflow',
      message: 'Point your external system at the following URL to trigger the associated automation',
    },    cubeapm_webhook: {
      endpoint: 'cubeapm',
      message:
        'Add the following URL as a Webhook notification channel in CubeAPM. Leave the payload template unset — CubeAPM’s default body is already Alertmanager-compatible, which is what NudgeBee parses',
    },
  };

  // List rows carry integrations_cloud_accounts / integration_config_values as
  // JSON strings; the edit row arrives already parsed. Accept either.
  const asList = (value) => {
    if (Array.isArray(value)) return value;
    const parsed = safeJSONParse(value);
    return Array.isArray(parsed) ? parsed : [];
  };
  const webhookToken = (row) => {
    const values = row?.integration_config_values;
    const parsed = typeof values === 'string' ? safeJSONParse(values) : values;
    if (Array.isArray(parsed)) {
      return parsed.find((c) => c?.name === 'token')?.value || '';
    }
    return parsed?.token || '';
  };

  const renderAlertDelivery = () => {
    if (!isDirectPrometheus) {
      return null;
    }
    const receivers = [];
    asList(editData?.integrations_cloud_accounts).forEach((acc) => {
      alertmanagerWebhooks.forEach((row) => {
        const linked = asList(row?.integrations_cloud_accounts).some((a) => a?.cloud_account_id === acc?.cloud_account_id);
        const token = webhookToken(row);
        if (!linked || !token) {
          return;
        }
        const accountName = acc?.cloud_account_name || '';
        const cluster = accountName ? `&cluster=${encodeURIComponent(accountName)}` : '';
        receivers.push({
          key: `${row.id}-${acc.cloud_account_id}`,
          accountName: accountName || acc.cloud_account_id,
          webhookName: row.name,
          url: `${window.location.origin}/api/webhooks/prometheus-alertmanager?token=${encodeURIComponent(token)}${cluster}`,
        });
      });
    });

    return (
      <Box data-testid='prometheus-alert-delivery' sx={{ mt: ds.space[5] }}>
        <Typography variant='subtitle1' sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
          Alert delivery
        </Typography>
        <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[400], mt: ds.space[1] }}>
          {`A Prometheus connected without an agent delivers alerts through its Alertmanager. Add a webhook receiver pointing at the URL below; the account name rides along as the cluster. Rules created in ${getBrandTitle()} are written to the ruler configured above.`}
        </Typography>
        {receivers.length === 0 ? (
          <Typography
            variant='body2'
            sx={{ mt: ds.space[2], fontSize: 'var(--ds-text-body)', color: ds.gray[400] }}
            data-testid='prometheus-alert-delivery-missing'
          >
            No Prometheus Alertmanager Webhook integration is linked to this account yet —{' '}
            <Link href='/accounts/account-form?cloudProvider=prometheus_alertmanager_webhook' openInNew>
              create one
            </Link>{' '}
            for the same account, then come back here for its URL.
          </Typography>
        ) : (
          receivers.map((receiver) => (
            <Box
              key={receiver.key}
              sx={{
                mt: ds.space[3],
                p: 2,
                borderRadius: ds.radius.lg,
                border: `1px solid ${ds.brand[200]}`,
                backgroundColor: ds.gray[100],
                display: 'flex',
                alignItems: 'flex-start',
                gap: ds.space[2],
              }}
            >
              <Box sx={{ flex: 1 }}>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[600], mb: ds.space[1] }}>
                  {receiver.accountName} · {receiver.webhookName}
                </Typography>
                <Typography
                  sx={{ color: ds.gray[600], fontSize: 'var(--ds-text-body)', wordBreak: 'break-all', lineHeight: 1.6 }}
                  data-testid='prometheus-alert-delivery-url'
                >
                  {receiver.url}
                </Typography>
              </Box>
              <CopyButton text={receiver.url} />
            </Box>
          ))
        )}
      </Box>
    );
  };

  const renderWebhookContent = (integrationName, response) => {
    const config = webhookConfig[integrationName];
    if (!config) {
      return null;
    }

    const url = `${window.location.origin}/api/webhooks/${config.endpoint}?token=${response.value}`;

    return (
      <Grid container mt={2} mb={1} mr={1} sx={{ display: 'flex', flexDirection: 'column' }}>
        <Typography variant='subtitle1' sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
          {config.message}
        </Typography>

        <Box
          sx={{
            mt: 'var(--ds-space-4)',
            mb: 'var(--ds-space-4)',
            p: 2,
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.brand[200]}`,
            backgroundColor: ds.gray[100],
            display: 'flex',
            alignItems: 'flex-start',
            gap: ds.space[2],
          }}
        >
          <Typography
            sx={{ color: ds.gray[600], fontSize: 'var(--ds-text-body-lg)', wordBreak: 'break-all', lineHeight: 1.6, flex: 1 }}
            variant='body1'
            id={`${config.endpoint}-info`}
          >
            {url}
          </Typography>
          <CopyButton text={url} />
        </Box>

        {config.template && (
          <>
            <Typography variant='subtitle1' sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
              {config.templateMessage}
            </Typography>
            <Box
              sx={{
                mt: 'var(--ds-space-4)',
                mb: 'var(--ds-space-4)',
                p: 2,
                borderRadius: ds.radius.lg,
                border: `1px solid ${ds.brand[200]}`,
                backgroundColor: ds.gray[100],
                display: 'flex',
                alignItems: 'flex-start',
                gap: ds.space[2],
              }}
            >
              <Typography
                component='pre'
                sx={{
                  color: ds.gray[600],
                  fontSize: 'var(--ds-text-body)',
                  fontFamily: 'monospace',
                  lineHeight: 1.6,
                  flex: 1,
                  m: 0,
                  maxHeight: ds.space.mul(0, 160),
                  overflow: 'auto',
                }}
                id={`${config.endpoint}-template`}
              >
                {config.template}
              </Typography>
              <CopyButton text={config.template} />
            </Box>
          </>
        )}

        {integrationName === 'workflow_webhook' ? (
          <Box
            sx={{
              mb: ds.space[4],
              p: 1.5,
              borderRadius: ds.radius.sm,
              backgroundColor: ds.blue[100],
              border: `1px solid ${ds.brand[200]}`,
            }}
          >
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.brand[500], lineHeight: 1.6, textAlign: 'justify' }}>
              <strong>Next step:</strong> This webhook is not yet attached to any automation. First, open the workflow you want this URL to trigger
              and select this integration in its <em>Webhook</em> trigger configuration. Only after the workflow is bound should you paste the URL
              above into your external system (Prometheus Alertmanager, Grafana, custom service, etc.) — incoming requests are dropped until the
              binding exists.
            </Typography>
          </Box>
        ) : (
          <Box
            sx={{
              mb: ds.space[4],
              p: 1.5,
              borderRadius: ds.radius.sm,
              backgroundColor: ds.blue[100],
              border: `1px solid ${ds.brand[200]}`,
            }}
          >
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.brand[500], lineHeight: 1.6, textAlign: 'justify' }}>
              <strong>Tip (optional):</strong> When you paste this URL into your webhook provider, you can optionally append extra query parameters
              (e.g. <code>&amp;env=prod</code>, <code>&amp;cluster=us-east-1</code>) directly to the URL inside the provider&apos;s configuration.
              {` Every event delivered through that URL will be tagged with those labels in ${getBrandTitle()}.`}
              <br />
              <br />
              <strong>Why add them?</strong> The webhook payload itself rarely carries deployment context like environment or cluster, so multiple
              {`senders pointing at the same ${getBrandTitle()} webhook (e.g. dev and prod alertmanagers) produce events that look identical. Adding query labels on the provider side lets you tell those events apart, route them to different accounts, and filter them in the ${getBrandTitle()} inbox without changing alert payloads.`}{' '}
              Reserved keys (<code>token</code>, <code>authorization</code>) are stripped automatically and any label the integration extracts from
              the payload wins on collision.
            </Typography>
          </Box>
        )}

        {config.learnMore && (
          <Typography sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
            Learn more about{' '}
            <Link href={config.learnMore.url} openInNew>
              {config.learnMore.text}
            </Link>
          </Typography>
        )}
      </Grid>
    );
  };

  const renderContent = () => {
    return renderWebhookContent(integrationName, response);
  };

  // With rule-based mapping the same account may legitimately appear in
  // multiple rules (different label-combos routing to the same target), so
  // we don't filter the dropdown — just expose the full account list.
  const accountOptions = config.properties?.account_id?.possible_values || [];

  // ES (integrationName is 'ES' or 'elasticsearch') shows the Advanced Settings
  // section for the Per-Account Index mapping in BOTH add and edit flows, and now
  // for Default Log Filters too — the backend applies them provider-agnostically
  // in FetchLogs, so ES honoured a saved default_filters value all along while the
  // form was the only thing hiding it. Every LOG_FILTER_INTEGRATIONS entry shows
  // the filter editor in both add and edit flows.
  const isESIntegration = integrationName === 'ES' || integrationName === 'elasticsearch';
  const showLogFilters = LOG_FILTER_INTEGRATIONS.has(integrationName);
  const showTraceFilters = TRACE_INTEGRATIONS.has(integrationName);
  // Same gate as the trace filters: the providers getTraceSource actually serves and
  // that have an integration form. See the comment on TRACE_INTEGRATIONS for why this
  // is not derived from the schema property the way supportsLogLabelMapping is.
  const showTraceLabelMapping = TRACE_INTEGRATIONS.has(integrationName);

  // A fresh tenant with no onboarded cluster / cloud account has nothing to link
  // an account-scoped integration to. The `account_id` schema field is present
  // only on non-tenant-scoped integrations (observability, metrics, logs, traces,
  // database, proxy…); ticketing/messaging omit it and are exempt. When that field
  // exists but has zero options, core.CreateIntegrationConfig would reject the save
  // with the cryptic "integrations: accountId is required" — so block submission
  // here with actionable guidance instead. Edit mode is exempt: an existing
  // integration already has a linked account.
  const isEdit = !!(editData && Object.keys(editData).length > 0);
  const noAccountsAvailable =
    !isLoadingSchema && !!config.properties?.account_id && !isEdit && !loadingOptions.account_id && accountOptions.length === 0;

  return (
    <>
      <VmAgentCredentialsDialog
        open={showModal && integrationName === 'vm_agent'}
        onClose={() => handleCloseModal(true)}
        accessKey={vmAgentCredentials?.accessKey}
        accessSecret={vmAgentCredentials?.accessSecret}
      />
      <Modal
        handleClose={() => {
          handleCloseModal(true);
        }}
        title={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <Typography component='h2' variant='h6' fontWeight={600}>
              {`Set up ${titleCase(integrationName)}`}
            </Typography>
          </Box>
        }
        open={
          showModal &&
          [
            'pagerduty_webhook',
            'zenduty_webhook',
            'prometheus_alertmanager_webhook',
            'datadog_webhook',
            'azure_monitor_webhook',
            'gcp_monitoring_webhook',
            'servicenow_webhook',
            'newrelic_webhook',
            'dynatrace_webhook',
            'splunk_webhook',
            'grafana_webhook',
            'solarwinds_webhook',
            'elasticsearch_webhook',
            'openobserve_webhook',
            'workflow_webhook',
          ].includes(integrationName)
        }
        width='md'
        isConfirmRequired={false}
      >
        {renderContent()}
      </Modal>
      <Modal
        width='md'
        open={openModal && !showModal}
        handleClose={() => handleCloseModal(false)}
        title={title}
        loader={isSubmitting || isLoadingSchema}
      >
        {isAgentSource ? (
          <Box sx={{ minHeight: ds.space.mul(0, 100), pt: ds.space[2], pb: ds.space[2] }}>
            <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-body)', mb: ds.space[4] }}>
              Enable or disable provider settings for each account
            </Typography>
            {agentAccountProviders.map((acc) => (
              <Box
                key={acc.cloud_account_id}
                sx={{
                  py: ds.space[3],
                  px: ds.space[4],
                  mb: ds.space[2],
                  borderRadius: ds.radius.sm,
                  border: `1px solid ${ds.brand[200]}`,
                }}
              >
                <Typography
                  variant='body2'
                  sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', mb: providerFields.length > 1 ? 1 : 0 }}
                >
                  {acc.account_name || acc.cloud_account_id}
                </Typography>
                {providerFields.map((provider) => (
                  <Box
                    key={provider.key}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                    }}
                  >
                    <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[400] }}>
                      {provider.label}
                    </Typography>
                    <FormControlLabel
                      control={
                        <Switch
                          checked={!!acc[provider.key]}
                          onChange={(e) => handleAgentProviderToggle(acc.cloud_account_id, provider.key, e.target.checked)}
                          size='sm'
                          disabled={isSubmitting}
                        />
                      }
                      label={acc[provider.key] ? 'Enabled' : 'Disabled'}
                      labelPlacement='start'
                      sx={{ mr: 0, gap: ds.space[2] }}
                    />
                  </Box>
                ))}
              </Box>
            ))}
            <Box
              sx={{
                display: 'flex',
                gap: 'var(--ds-space-3)',
                justifyContent: 'flex-end',
                mt: ds.space[5],
                mb: ds.space[6],
                button: {
                  minWidth: ds.space.mul(0, 70),
                },
              }}
            >
              <Button id='done-btn' tone='secondary' size='md' onClick={() => handleCloseModal(false)}>
                Done
              </Button>
            </Box>
          </Box>
        ) : (
          <>
            <Box sx={{ minHeight: ds.space.mul(0, 100), pt: ds.space[5], pb: ds.space[2] }}>
              {isLoadingSchema ? (
                <Box
                  sx={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    justifyContent: 'center',
                    py: 8,
                  }}
                >
                  <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-body)' }}>
                    Loading configuration...
                  </Typography>
                </Box>
              ) : (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
                  {webhookConfig[integrationName]?.learnMore && (
                    <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[400] }}>
                      Learn more about{' '}
                      <Link href={webhookConfig[integrationName].learnMore.url} openInNew>
                        {webhookConfig[integrationName].learnMore.text}
                      </Link>
                    </Typography>
                  )}
                  {config?.description && (
                    <Box
                      sx={{
                        mb: ds.space[4],
                        p: 1.5,
                        borderRadius: ds.radius.sm,
                        border: `1px solid ${ds.brand[200]}`,
                        backgroundColor: ds.gray[100],
                        display: 'flex',
                        alignItems: 'flex-start',
                        gap: ds.space[2],
                      }}
                    >
                      <SafeIcon src={infoIcon} alt='info' width={16} height={16} style={{ marginTop: 2, flexShrink: 0 }} />
                      <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[400], lineHeight: 1.5 }}>
                        {config.description}
                      </Typography>
                    </Box>
                  )}
                  {noAccountsAvailable && (
                    <Box
                      sx={{
                        mb: ds.space[4],
                        p: 1.5,
                        borderRadius: ds.radius.sm,
                        border: `1px solid ${ds.amber[300]}`,
                        backgroundColor: ds.amber[100],
                        display: 'flex',
                        alignItems: 'flex-start',
                        gap: ds.space[2],
                      }}
                      data-testid='integration-no-accounts-warning'
                    >
                      <SafeIcon src={infoIcon} alt='info' width={16} height={16} style={{ marginTop: 2, flexShrink: 0 }} />
                      <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.amber[700], lineHeight: 1.5 }}>
                        No clusters or cloud accounts found for this tenant. Onboard a cluster or cloud account first — this integration must be
                        linked to at least one account before it can be saved.
                      </Typography>
                    </Box>
                  )}
                  {Object.keys(config?.properties || {}).length > 0 ? (
                    (() => {
                      const renderField = (key) => {
                        const field = config.properties[key];
                        let inputComponent;
                        const errorText = errors[key] || '';

                        if (!field.description) {
                          return null;
                        }

                        const isRequired = isFieldRequired(key, field);

                        switch (field.type) {
                          case 'array':
                          case 'list':
                            if (field.possible_values) {
                              if (Array.isArray(field.default)) {
                                const rawValue = formValues[key];
                                const value =
                                  rawValue != null
                                    ? field.possible_values?.filter(
                                        (op) =>
                                          Array.isArray(rawValue)
                                            ? rawValue.includes(op.value) || rawValue.includes(op) // array case
                                            : op.value === rawValue || op === rawValue // single value case
                                      )
                                    : null;
                                inputComponent = (
                                  <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                    <Typography
                                      variant='body2'
                                      sx={{
                                        color: ds.gray[400],
                                        fontSize: 'var(--ds-text-small)',
                                        lineHeight: 1.5,
                                        mb: ds.space[2],
                                        pl: ds.space[1],
                                      }}
                                    >
                                      {field.description}
                                      {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
                                    </Typography>
                                    <Box>
                                      <FilterDropdown
                                        key={`auto-complete-${key}`}
                                        multiple
                                        label={field.display_name || snakeToTitleCase(key)}
                                        value={value || []}
                                        options={field.possible_values ?? []}
                                        grouped={field.grouped}
                                        groupIcon={field.grouped ? renderAccountGroupIcon : undefined}
                                        disabled={field.possible_values?.length === 0}
                                        onSelect={(_, value) => handleChange(key, value)}
                                        isOptionsLoading={loadingOptions[key]}
                                        sx={{ width: '100%' }}
                                      />
                                      {errorText && (
                                        <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)' }}>
                                          {errorText}
                                        </Typography>
                                      )}
                                    </Box>
                                  </Box>
                                );
                              } else {
                                const value =
                                  formValues[key] != null
                                    ? field.possible_values?.find((op) => op.value == formValues[key] || op == formValues[key])
                                    : null;
                                inputComponent = (
                                  <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                    <Typography
                                      variant='body2'
                                      sx={{
                                        color: ds.gray[400],
                                        fontSize: 'var(--ds-text-small)',
                                        lineHeight: 1.5,
                                        mb: ds.space[2],
                                        pl: ds.space[1],
                                      }}
                                    >
                                      {field.description}
                                      {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
                                    </Typography>
                                    <Box>
                                      <FilterDropdown
                                        key={`auto-complete-${key}`}
                                        label={field.display_name || snakeToTitleCase(key)}
                                        value={value}
                                        options={field.possible_values || []}
                                        grouped={field.grouped}
                                        groupIcon={field.grouped ? renderAccountGroupIcon : undefined}
                                        disabled={
                                          field.possible_values?.length == 0 ||
                                          (editData?.integration_config_values?.account_id && key == 'account_id') ||
                                          false
                                        }
                                        onSelect={(_, _value) => handleChange(key, _value?.value || _value)}
                                        isOptionsLoading={loadingOptions[key]}
                                        sx={{ width: '100%' }}
                                      />
                                      {errorText && (
                                        <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)' }}>
                                          {errorText}
                                        </Typography>
                                      )}
                                    </Box>
                                  </Box>
                                );
                              }
                            } else if (field.auto_generate_func && field.auto_generate_func !== 'listAccounts') {
                              // No fixed option list: a multi-value picker fed by the
                              // backend (e.g. Confluence page trees).
                              inputComponent = renderAutogenField(key, field, isRequired, errorText);
                            }
                            break;

                          case 'int':
                          case 'integer':
                            inputComponent = (
                              <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                <Typography
                                  variant='body2'
                                  sx={{
                                    color: ds.gray[400],
                                    fontSize: 'var(--ds-text-small)',
                                    lineHeight: 1.5,
                                    mb: ds.space[2],
                                    pl: ds.space[1],
                                  }}
                                >
                                  {field.description}
                                  {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
                                </Typography>
                                <Box>
                                  <Input
                                    id={toKebabCase(field.display_name || key)}
                                    key={key}
                                    label={field.display_name || snakeToTitleCase(key)}
                                    type='number'
                                    value={String(formValues[key] ?? '')}
                                    onChange={(value) => handleChange(key, parseInt(value, 10))}
                                    size='sm'
                                    error={errorText || undefined}
                                  />
                                  {key === 'prometheus_additional_labels' && promLabelHint && (
                                    <Box
                                      sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[2], mt: ds.space[2] }}
                                      data-testid='prometheus-available-labels'
                                    >
                                      <SafeIcon src={infoIcon} alt='info' width={16} height={16} style={{ marginTop: 2, flexShrink: 0 }} />
                                      <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[400], lineHeight: 1.5 }}>
                                        {promLabelHint}
                                      </Typography>
                                    </Box>
                                  )}
                                </Box>
                              </Box>
                            );
                            break;

                          case 'bool':
                          case 'boolean':
                            inputComponent = (
                              <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                <Typography
                                  variant='body2'
                                  sx={{
                                    color: ds.gray[400],
                                    fontSize: 'var(--ds-text-small)',
                                    lineHeight: 1.5,
                                    mb: ds.space[2],
                                    pl: ds.space[1],
                                  }}
                                >
                                  {field.description}
                                </Typography>
                                <Box>
                                  <Checkbox
                                    key={key}
                                    id={toKebabCase(field.display_name || key)}
                                    checked={!!formValues[key]}
                                    onChange={(next) => handleChange(key, next)}
                                    label={`${field.display_name || snakeToTitleCase(key)}${isRequired ? ' *' : ''}`}
                                  />
                                  {errorText && (
                                    <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)' }}>
                                      {errorText}
                                    </Typography>
                                  )}
                                </Box>
                              </Box>
                            );
                            break;

                          case 'string':
                            if (field.widget === 'model_alias_list') {
                              inputComponent = (
                                <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                  <Typography
                                    variant='body2'
                                    sx={{
                                      color: ds.gray[400],
                                      fontSize: 'var(--ds-text-small)',
                                      lineHeight: 1.5,
                                      mb: ds.space[2],
                                      pl: ds.space[1],
                                    }}
                                  >
                                    {field.description}
                                    {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
                                  </Typography>
                                  <ModelAliasList
                                    value={formValues[key] || ''}
                                    onChange={(value) => handleChange(key, value)}
                                    disabled={field.disabled || field.allow_edit === false}
                                  />
                                  {errorText && (
                                    <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)' }}>
                                      {errorText}
                                    </Typography>
                                  )}
                                </Box>
                              );
                            } else if (field.possible_values?.length > 0) {
                              inputComponent = (
                                <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                  <Typography
                                    variant='body2'
                                    sx={{
                                      color: ds.gray[400],
                                      fontSize: 'var(--ds-text-small)',
                                      lineHeight: 1.5,
                                      mb: ds.space[2],
                                      pl: ds.space[1],
                                    }}
                                  >
                                    {field.description}
                                    {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
                                  </Typography>
                                  <Box>
                                    <FilterDropdown
                                      key={key}
                                      label={field.display_name || snakeToTitleCase(key)}
                                      options={field.possible_values}
                                      value={formValues[key] || ''}
                                      onSelect={(_event, value) => handleChange(key, value?.value ?? value)}
                                      isOptionsLoading={loadingOptions[key]}
                                      sx={{ width: '100%' }}
                                    />
                                    {errorText && (
                                      <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)' }}>
                                        {errorText}
                                      </Typography>
                                    )}
                                  </Box>
                                </Box>
                              );
                            } else if (field.auto_generate_func && field.auto_generate_func !== 'listAccounts') {
                              inputComponent = renderAutogenField(key, field, isRequired, errorText);
                            } else {
                              inputComponent = (
                                <Box key={`wrapper-${key}`} sx={{ mb: ds.space[1] }}>
                                  <Typography
                                    variant='body2'
                                    sx={{
                                      color: ds.gray[400],
                                      fontSize: 'var(--ds-text-small)',
                                      lineHeight: 1.5,
                                      mb: ds.space[2],
                                      pl: ds.space[1],
                                    }}
                                  >
                                    {field.description}
                                    {isRequired && <span style={{ color: ds.red[500] }}> *</span>}
                                  </Typography>
                                  <Box>
                                    <Input
                                      key={key}
                                      id={toKebabCase(field.display_name || key)}
                                      label={field.display_name || snakeToTitleCase(key)}
                                      type={
                                        // Password masking (with the eye toggle) applies to SINGLE-LINE secrets only —
                                        // a multiline encrypted field (e.g. a service-account JSON) can't be a password
                                        // input, so it renders as a textarea (still encrypted at rest + masked on edit).
                                        field.is_encrypted && !field.multiline
                                          ? // Reveal only applies to a freshly-typed value. A stored secret is
                                            // never sent to the UI — on edit the field holds the mask, which stays
                                            // masked (and offers no eye), so a saved key can't be exposed.
                                            revealedSecrets[key] && formValues[key] && formValues[key] !== ENCRYPTED_MASK
                                            ? 'text'
                                            : 'password'
                                          : field.multiline
                                          ? 'textarea'
                                          : 'text'
                                      }
                                      value={formValues[key] || ''}
                                      onChange={(value) => handleChange(key, value)}
                                      trailingIcon={
                                        // Eye toggle only while inserting a new single-line value (not for the stored
                                        // mask, and not for multiline secrets which render as a textarea).
                                        field.is_encrypted && !field.multiline && formValues[key] && formValues[key] !== ENCRYPTED_MASK ? (
                                          <Box
                                            component='button'
                                            type='button'
                                            aria-label={revealedSecrets[key] ? 'Hide value' : 'Show value'}
                                            onClick={() => setRevealedSecrets((prev) => ({ ...prev, [key]: !prev[key] }))}
                                            sx={{
                                              cursor: 'pointer',
                                              display: 'inline-flex',
                                              alignItems: 'center',
                                              padding: 0,
                                              border: 'none',
                                              background: 'none',
                                              color: ds.gray[500],
                                            }}
                                          >
                                            {revealedSecrets[key] ? (
                                              <VisibilityOffIcon sx={{ fontSize: 16 }} />
                                            ) : (
                                              <VisibilityIcon sx={{ fontSize: 16 }} />
                                            )}
                                          </Box>
                                        ) : undefined
                                      }
                                      size='sm'
                                      error={errorText || undefined}
                                      minRows={field.multiline ? 3 : undefined}
                                      disabled={
                                        field.disabled ||
                                        field.allow_edit === false ||
                                        (editData?.integration_config_values?.integration_config_name && key == 'integration_config_name') ||
                                        false
                                      }
                                    />
                                  </Box>
                                </Box>
                              );
                            }
                            break;

                          default:
                            inputComponent = null;
                        }

                        return inputComponent || null;
                      };
                      // Schema fields flagged `advanced` render inside the same
                      // collapsed section the ES/log integrations use, so every
                      // integration's "Advanced Settings" looks and behaves alike.
                      const sortedKeys = getSortedFieldKeys();
                      const advancedKeys = sortedKeys.filter((key) => config.properties[key]?.advanced);
                      return (
                        <>
                          {sortedKeys.filter((key) => !config.properties[key]?.advanced).map(renderField)}
                          {advancedKeys.length > 0 && (
                            <Box sx={{ mt: ds.space[6] }}>
                              {renderAdvancedToggle()}
                              <Collapse in={advancedOpen}>{advancedKeys.map(renderField)}</Collapse>
                            </Box>
                          )}
                        </>
                      );
                    })()
                  ) : (
                    <Box
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'center',
                        justifyContent: 'center',
                        py: 6,
                        px: 3,
                      }}
                    >
                      <Typography
                        variant='body1'
                        sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}
                      >
                        Nothing to Configure
                      </Typography>
                      <Typography variant='body2' sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-body)', mt: ds.space[2], textAlign: 'center' }}>
                        This integration has been set up and doesn't require additional configuration
                      </Typography>
                    </Box>
                  )}
                </Box>
              )}
            </Box>
            {integrationName.includes('_webhook') && integrationName !== 'workflow_webhook' && editData?.name && (
              <>
                <Typography
                  variant='body2'
                  sx={{
                    color: ds.brand[500],
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    mb: ds.space[2],
                  }}
                >
                  Account Mapping (Optional)
                </Typography>
                <Typography
                  variant='body2'
                  sx={{
                    color: ds.gray[400],
                    fontSize: 'var(--ds-text-small)',
                    mb: ds.space[4],
                    pl: ds.space[1],
                  }}
                >
                  Define rules that route incoming webhooks to a specific account based on payload labels. Conditions inside a rule are combined with
                  AND. Rules are evaluated top-to-bottom; the first matching rule wins. Enter multiple values separated by commas to match any of them
                  (e.g. <em>na, eu</em>).
                </Typography>

                <Box sx={{ mt: ds.space[2] }}>
                  {rules.map((rule, ruleIdx) => (
                    <Box
                      key={ruleIdx}
                      sx={{
                        border: `1px solid ${ds.blue[400]}`,
                        borderRadius: 'var(--ds-radius-lg)',
                        p: 2,
                        mb: ds.space[4],
                        backgroundColor: ds.blue[100],
                      }}
                      data-testid={`rule-card-${ruleIdx}`}
                    >
                      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[3] }}>
                        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.blue[500] }}>
                          Rule {ruleIdx + 1}
                        </Typography>
                        {rules.length > 1 && (
                          <Button
                            tone='danger'
                            size='xs'
                            composition='icon-only'
                            icon={<SafeIcon src={NewDelete} alt='Remove rule' style={{ width: ds.space.mul(0, 7), height: ds.space.mul(0, 7) }} />}
                            data-testid={`remove-rule-btn-${ruleIdx}`}
                            aria-label='Remove rule'
                            onClick={() => handleRemoveRule(ruleIdx)}
                          />
                        )}
                      </Box>

                      <Typography
                        sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[500], mb: ds.space[2] }}
                      >
                        WHEN
                      </Typography>
                      {rule.match.map((cond, condIdx) => (
                        <Box key={condIdx}>
                          {condIdx > 0 && (
                            <Typography
                              sx={{
                                fontSize: 'var(--ds-text-caption)',
                                fontWeight: 'var(--ds-font-weight-semibold)',
                                color: ds.brand[500],
                                my: ds.space[1],
                                pl: ds.space[1],
                              }}
                            >
                              AND
                            </Typography>
                          )}
                          <Box sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-end', mb: ds.space[2] }}>
                            <Box sx={{ flex: 1 }}>
                              <Input
                                label={condIdx === 0 ? 'Label name' : ''}
                                placeholder='e.g. env'
                                value={cond.key}
                                onChange={(value) => handleConditionChange(ruleIdx, condIdx, 'key', value)}
                                size='sm'
                              />
                            </Box>
                            <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.brand[500], pb: ds.space[1] }}>=</Typography>
                            <Box sx={{ flex: 1 }}>
                              <Input
                                label={condIdx === 0 ? 'Label value(s)' : ''}
                                placeholder='e.g. prod or na, eu'
                                value={cond.value}
                                onChange={(value) => handleConditionChange(ruleIdx, condIdx, 'value', value)}
                                size='sm'
                              />
                            </Box>
                            <Box sx={{ paddingBottom: ds.space[1] }}>
                              <Button
                                tone='secondary'
                                size='xs'
                                composition='icon-only'
                                icon={<SafeIcon src={NewDelete} alt='Remove' style={{ width: ds.space.mul(0, 7), height: ds.space.mul(0, 7) }} />}
                                data-testid={`remove-condition-btn-${ruleIdx}-${condIdx}`}
                                aria-label='Remove condition'
                                disabled={rule.match.length === 1}
                                onClick={() => handleRemoveCondition(ruleIdx, condIdx)}
                              />
                            </Box>
                          </Box>
                        </Box>
                      ))}
                      <Box sx={{ mb: ds.space[4] }}>
                        <Button id={`add-condition-btn-${ruleIdx}`} tone='secondary' size='sm' onClick={() => handleAddCondition(ruleIdx)}>
                          + Add condition
                        </Button>
                      </Box>

                      <Typography
                        sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.brand[500], mb: ds.space[2] }}
                      >
                        THEN use account
                      </Typography>
                      <FilterDropdown
                        label='Account'
                        grouped
                        groupIcon={renderAccountGroupIcon}
                        options={accountOptions}
                        value={rule.accountId}
                        onSelect={(_event, value) => handleRuleAccountChange(ruleIdx, value?.value ?? value)}
                        isOptionsLoading={loadingOptions.account_id}
                        disabled={!accountOptions.length}
                        sx={{ height: ds.space.mul(0, 22) }}
                      />
                    </Box>
                  ))}

                  <Box sx={{ mt: ds.space[2] }}>
                    <Button id='add-rule-btn' tone='secondary' size='md' onClick={handleAddRule}>
                      + Add rule
                    </Button>
                  </Box>

                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      color: ds.gray[400],
                      mt: ds.space[3],
                      fontStyle: 'italic',
                    }}
                  >
                    If no rule matches, the webhook is routed to the account selected above.
                  </Typography>
                </Box>

                <Typography
                  variant='body2'
                  sx={{
                    color: ds.brand[500],
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    mt: ds.space[6],
                    mb: ds.space[2],
                  }}
                >
                  Webhook Label Mapping (Optional)
                </Typography>
                <Typography
                  variant='body2'
                  sx={{
                    color: ds.gray[400],
                    fontSize: 'var(--ds-text-small)',
                    mb: ds.space[4],
                    pl: ds.space[1],
                  }}
                >
                  Map alert label keys to event fields. Order matters — first non-empty value is used. You can type custom label keys not in the
                  suggestions. For advanced extraction, use Jinja2 templates (e.g. {"{{ labels.app_id | split(sep='/') | last }}"}) or regex (e.g.
                  app_id|/k8s/[^/]+/(.+)).
                </Typography>
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
                  <FilterDropdown
                    multiple
                    freeSolo
                    label='Subject Name Labels'
                    value={labelMapping.subject_name_labels}
                    options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...labelMapping.subject_name_labels])]}
                    onSelect={(e) => setLabelMapping((prev) => ({ ...prev, subject_name_labels: e.target.value }))}
                    limitTag={3}
                    data-testid='label-mapping-subject-input'
                  />
                  <FilterDropdown
                    multiple
                    freeSolo
                    label='Namespace Labels'
                    value={labelMapping.namespace_labels}
                    options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...labelMapping.namespace_labels])]}
                    onSelect={(e) => setLabelMapping((prev) => ({ ...prev, namespace_labels: e.target.value }))}
                    limitTag={3}
                    data-testid='label-mapping-namespace-input'
                  />
                  <FilterDropdown
                    multiple
                    freeSolo
                    label='Severity Labels'
                    value={labelMapping.severity_labels}
                    options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...labelMapping.severity_labels])]}
                    onSelect={(e) => setLabelMapping((prev) => ({ ...prev, severity_labels: e.target.value }))}
                    limitTag={3}
                    data-testid='label-mapping-severity-input'
                  />
                </Box>
              </>
            )}
            {(showLogFilters || showTraceFilters || isESIntegration || supportsLogLabelMapping || showTraceLabelMapping) && (
              <Box sx={{ mt: ds.space[6] }}>
                {renderAdvancedToggle()}
                <Collapse in={advancedOpen}>
                  {showLogFilters && (
                    <DefaultFiltersCard
                      title='Default Log Filters (Optional)'
                      helpText={
                        <>
                          Filters always applied to every log query for the selected account (e.g. a central provider scoped to one cluster:{' '}
                          <em>cluster_id = nudgebee</em>). Enter the provider-native column name. Conditions in a card are combined with AND.
                        </>
                      }
                      lockedText="Run Test Connection to load the backend's columns and configure default filters."
                      unlocked={advancedSettingsUnlocked}
                      cards={defaultFilterRules}
                      onCardsChange={handleDefaultFilterRulesChange}
                      accountOptions={accountOptions}
                      accountOptionsLoading={loadingOptions.account_id}
                      renderAccountGroupIcon={renderAccountGroupIcon}
                      fieldLabel='Column'
                      fieldPlaceholder='e.g. cluster_id'
                      valuePlaceholder='e.g. nudgebee'
                      fieldOptionsForCard={(card) =>
                        logFieldOptions[fieldOptionsKey(card.accountId, indexForAccount(indexRules, card.accountId, formValues.log_index))] || {
                          loading: false,
                          options: [],
                        }
                      }
                      validation={columnValidation}
                      onValidateCard={handleValidateCard}
                      validateLabel='Validate columns'
                      invalidFieldError='Not a known log column for this account.'
                      testIdPrefix='default-filter'
                    />
                  )}
                  {showTraceFilters && (
                    <DefaultFiltersCard
                      title='Default Trace Filters (Optional)'
                      helpText={
                        <>
                          Filters always applied to every trace query for the selected account — the traces screen, investigation evidence and
                          nubi&apos;s answers (e.g. a shared backend scoped to one environment: <em>workload_namespace = production</em>). Use the
                          canonical field name, not the backend&apos;s: it is translated per provider, so it survives a provider change. Conditions in
                          a card are combined with AND.
                        </>
                      }
                      lockedText='Run Test Connection to configure default trace filters.'
                      unlocked={advancedSettingsUnlocked}
                      cards={defaultTraceFilterRules}
                      onCardsChange={setDefaultTraceFilterRules}
                      accountOptions={accountOptions}
                      accountOptionsLoading={loadingOptions.account_id}
                      renderAccountGroupIcon={renderAccountGroupIcon}
                      fieldLabel='Field'
                      fieldPlaceholder='e.g. workload_namespace'
                      valuePlaceholder='e.g. production'
                      fieldOptionsForCard={() => ({ loading: false, options: CANONICAL_TRACE_FIELD_OPTIONS })}
                      testIdPrefix='default-trace-filter'
                      sx={{ mt: showLogFilters ? ds.space[6] : 0 }}
                    />
                  )}
                  {isESIntegration && (
                    <>
                      <Typography
                        sx={{
                          color: ds.brand[500],
                          fontSize: 'var(--ds-text-body)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          mt: showLogFilters || showTraceFilters ? ds.space[6] : 0,
                          mb: ds.space[1],
                        }}
                      >
                        Per-Account Index (Optional)
                      </Typography>
                      <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', mb: ds.space[4], pl: ds.space[1] }}>
                        When one Elasticsearch endpoint serves multiple accounts, map each account to its own index. Leave a field blank to fall back
                        to the index configured above.
                      </Typography>
                      {!advancedSettingsUnlocked ? (
                        <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', pl: ds.space[1], mb: ds.space[3] }}>
                          Run Test Connection to load the cluster&apos;s indices and configure per-account mapping.
                        </Typography>
                      ) : (
                        <>
                          {indexRules.map((card, cardIdx) => {
                            const cardNeedsAccount =
                              !card.accountId &&
                              ((card.log_index || '').trim() || (card.metrics_index || '').trim() || (card.trace_index || '').trim());
                            return (
                              <Box
                                key={cardIdx}
                                sx={{
                                  border: `1px solid ${ds.blue[400]}`,
                                  borderRadius: 'var(--ds-radius-lg)',
                                  p: 2,
                                  mb: ds.space[4],
                                  backgroundColor: ds.blue[100],
                                }}
                                data-testid={`index-map-card-${cardIdx}`}
                              >
                                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[3] }}>
                                  <Typography
                                    sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.blue[500] }}
                                  >
                                    Account {cardIdx + 1}
                                  </Typography>
                                  {indexRules.length > 1 && (
                                    <Button
                                      tone='secondary'
                                      size='xs'
                                      composition='icon-only'
                                      icon={
                                        <SafeIcon
                                          src={NewDelete}
                                          alt='Remove account'
                                          style={{ width: ds.space.mul(0, 7), height: ds.space.mul(0, 7) }}
                                        />
                                      }
                                      aria-label='Remove account'
                                      onClick={() => handleRemoveIndexCard(cardIdx)}
                                    />
                                  )}
                                </Box>
                                <FilterDropdown
                                  label='Account'
                                  grouped={config.properties?.account_id?.grouped}
                                  groupIcon={config.properties?.account_id?.grouped ? renderAccountGroupIcon : undefined}
                                  options={accountOptions}
                                  value={card.accountId}
                                  onSelect={(_event, value) => handleIndexCardChange(cardIdx, 'accountId', value?.value ?? value)}
                                  isOptionsLoading={loadingOptions.account_id}
                                  disabled={!accountOptions.length}
                                  sx={{
                                    height: ds.space.mul(0, 22),
                                    mb: cardNeedsAccount ? ds.space[1] : ds.space[3],
                                    ...(cardNeedsAccount ? { borderColor: 'var(--ds-red-500)', boxShadow: '0 0 0 3px var(--ds-red-100)' } : {}),
                                  }}
                                />
                                {cardNeedsAccount && (
                                  <Typography
                                    sx={{ color: 'var(--ds-red-600)', fontSize: 'var(--ds-text-caption)', mb: ds.space[3], pl: ds.space[1] }}
                                  >
                                    Select an account to apply these indices.
                                  </Typography>
                                )}
                                <Box sx={{ display: 'flex', gap: ds.space[3], flexWrap: 'wrap' }}>
                                  <Box sx={{ flex: '1 1 30%', minWidth: 180 }}>
                                    <FilterDropdown
                                      label='Log Index'
                                      freeSolo
                                      options={esIndexes}
                                      value={card.log_index}
                                      onSelect={(_e, v) => handleIndexCardChange(cardIdx, 'log_index', v?.value ?? v ?? '')}
                                      isOptionsLoading={esIndexesLoading}
                                      placeholder={formValues.log_index ? `inherit: ${formValues.log_index}` : 'inherit top-level'}
                                    />
                                  </Box>
                                  <Box sx={{ flex: '1 1 30%', minWidth: 180 }}>
                                    <FilterDropdown
                                      label='Metrics Index'
                                      freeSolo
                                      options={esIndexes}
                                      value={card.metrics_index}
                                      onSelect={(_e, v) => handleIndexCardChange(cardIdx, 'metrics_index', v?.value ?? v ?? '')}
                                      isOptionsLoading={esIndexesLoading}
                                      placeholder={formValues.metrics_index ? `inherit: ${formValues.metrics_index}` : 'inherit top-level'}
                                    />
                                  </Box>
                                  <Box sx={{ flex: '1 1 30%', minWidth: 180 }}>
                                    <FilterDropdown
                                      label='Trace Index'
                                      freeSolo
                                      options={esIndexes}
                                      value={card.trace_index}
                                      onSelect={(_e, v) => handleIndexCardChange(cardIdx, 'trace_index', v?.value ?? v ?? '')}
                                      isOptionsLoading={esIndexesLoading}
                                      placeholder={formValues.trace_index ? `inherit: ${formValues.trace_index}` : 'inherit top-level'}
                                    />
                                  </Box>
                                </Box>
                              </Box>
                            );
                          })}
                          <Button tone='secondary' size='md' onClick={handleAddIndexCard}>
                            + Add account
                          </Button>
                        </>
                      )}
                    </>
                  )}
                  {supportsLogLabelMapping && (
                    <Box sx={{ mt: showLogFilters || showTraceFilters || isESIntegration ? ds.space[6] : 0 }}>
                      <LabelMappingCards
                        cards={labelMappingCards}
                        setCards={setLabelMappingCards}
                        savedCards={savedLabelMappingCards}
                        connectionVerified={advancedSettingsUnlocked}
                        fieldOptions={logFieldOptions}
                        indexRules={indexRules}
                        topLevelIndex={formValues.log_index}
                        accountOptions={accountOptions}
                        accountOptionsLoading={loadingOptions.account_id}
                        grouped={config.properties?.account_id?.grouped}
                        renderAccountGroupIcon={renderAccountGroupIcon}
                        provider={integrationName}
                        providerSource={editData?.source || 'user'}
                      />
                    </Box>
                  )}
                  {showTraceLabelMapping && (
                    <Box sx={{ mt: showLogFilters || showTraceFilters || isESIntegration || supportsLogLabelMapping ? ds.space[6] : 0 }}>
                      <LabelMappingCards
                        cards={traceLabelMappingCards}
                        setCards={setTraceLabelMappingCards}
                        savedCards={savedTraceLabelMappingCards}
                        connectionVerified={advancedSettingsUnlocked}
                        accountOptions={accountOptions}
                        accountOptionsLoading={loadingOptions.account_id}
                        grouped={config.properties?.account_id?.grouped}
                        renderAccountGroupIcon={renderAccountGroupIcon}
                        provider={integrationName}
                        providerSource={editData?.source || 'user'}
                        providerType='traces'
                        signalNoun='trace'
                        testIdPrefix='trace-label-mapping'
                        title='Trace Label Mapping (Optional)'
                        helpText={
                          <>
                            {`Tell ${getBrandTitle()} which span or resource attribute in this backend holds each canonical trace field (e.g. `}
                            <em>service_name → service.name</em>
                            {`). What you set here wins over the account and tenant mappings. Leave a field out and it falls through to those.`}
                          </>
                        }
                        lockedText='Run Test Connection to configure the trace field mapping.'
                        canonicalOptions={CANONICAL_TRACE_FIELD_OPTIONS}
                        canonicalPlaceholder='e.g. service_name'
                        fieldPlaceholder='e.g. service.name'
                        // No field probe for traces: traces_list_labels has no client wrapper and needs a
                        // saved integration, so the right-hand input is free text and the panel below is
                        // what confirms a typed value actually took.
                        fieldOptionsForCard={() => ({ loading: false, options: [], message: '' })}
                        conceptLabels={TRACE_CONCEPT_LABELS}
                        conceptOrder={TRACE_CONCEPT_ORDER}
                      />
                    </Box>
                  )}
                </Collapse>
              </Box>
            )}
            {!isLoadingSchema && renderAlertDelivery()}
            <Box
              sx={{
                display: 'flex',
                gap: 'var(--ds-space-3)',
                justifyContent: 'flex-end',
                mt: ds.space[5],
                mb: ds.space[6],
                button: {
                  minWidth: ds.space.mul(0, 70),
                },
              }}
            >
              <Button id='cancel-btn' tone='secondary' size='md' onClick={() => handleCloseModal(false)} disabled={isSubmitting || isTesting}>
                Cancel
              </Button>
              {isTestable && (
                <Button
                  id='test-connection-btn'
                  tone='secondary'
                  size='md'
                  onClick={handleTestConnection}
                  loading={isTesting}
                  disabled={isSubmitting || isTesting || isLoadingSchema || noAccountsAvailable}
                >
                  Test Connection
                </Button>
              )}
              <Button
                size='md'
                tone='primary'
                id='create-integration-acc'
                aria-label='Save Webhook'
                disabled={isSubmitting || isTesting || isLoadingSchema || (isTestable && !connectionVerified) || noAccountsAvailable}
                loading={isSubmitting}
                onClick={() => {
                  submitForm();
                }}
              >
                {editData && Object.keys(editData).length ? 'Update' : 'Save'}
              </Button>
            </Box>
          </>
        )}
      </Modal>
    </>
  );
};

IntegrationDynamicFormModal.propTypes = {
  integrationName: PropTypes.string,
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  title: PropTypes.string,
  integrationData: PropTypes.array,
  editData: PropTypes.object,
  listIntegrationConfigurationById: PropTypes.func,
};

export default IntegrationDynamicFormModal;
