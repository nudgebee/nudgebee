import React, { useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Box, Typography } from '@mui/material';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import LabelOutlinedIcon from '@mui/icons-material/LabelOutlined';
import TuneOutlinedIcon from '@mui/icons-material/TuneOutlined';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import VisibilityOffOutlinedIcon from '@mui/icons-material/VisibilityOffOutlined';
import TenantAccountCommonSettings from '@shared/settings/TenantAccountCommonSettings';
import {
  EMPTY_TRACE_LABEL_SETTINGS,
  TRACE_LABEL_ADVANCED_FIELDS,
  TRACE_LABEL_FIELDS,
  traceLabelsToSettings,
  traceSettingsToLabelsValue,
} from '@shared/settings/labelMapperFields';
import { Input } from '@ui/Input';
import Loader from '@shared/Loader';
import { Button } from '@ui/Button';
import { Checkbox } from '@ui/Checkbox';
import Tabs from '@shared/navigation/Tabs';
import { Card } from '@ui/Card';
import { Switch } from '@ui/Switch';
import { Banner } from '@ui/Banner';
import FilterDropdown from '@ui/FilterDropdown';
import CustomTable from '@shared/tables/CustomTable';
import DsTooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import { infoIcon } from '@assets';
import { ds } from '@utils/colors';
import { useBrandingConfig, fillBrandTokens, toAssistantLabel } from '@hooks/useTenantBranding';
import {
  deleteTenantAttributes,
  getFeatures,
  getTenantAttributes,
  updateTenantFeatureFlag,
  updateTenantName,
  upsertTenantAttributes,
} from '@lib/UserService';
import { toast as snackbar } from '@ui/Toast';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import { canEditTenantSettings, fetchFeatureFlagsForTenant, missingPermissionMessage } from '@lib/auth';
import { useSession } from 'next-auth/react';
import apiUserManagement from '@api1/user';

const DEFAULT_SUBJECT_NAME_LABELS = [
  'destination_workload_name',
  'src_workload_name',
  'deployment',
  'daemonset',
  'statefulset',
  'app_id',
  'nb_alert_job',
  'service_name',
  'service.name',
  'pod',
  'pod_name',
  'container',
  'nb_resource_id',
  'job',
];
const DEFAULT_NAMESPACE_LABELS = ['destination_workload_namespace', 'namespace', 'k8s_namespace', 'k8s.namespace.name'];
const DEFAULT_SEVERITY_LABELS = ['severity'];

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

const SectionHeader = ({ title, description }) => (
  <Box>
    {title && <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>}
    {description && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[0] }}>{description}</Typography>}
  </Box>
);

// Hand-maintained: the DB catalog (public.feature) has no when/why columns yet, so a flag missing here just falls back to its own description.
// A function taking the brand, not a constant: two entries name the product, which
// is tenant-branded and only resolved once /api/public/app_config has landed.
const featureRecommendedContent = (baseTitle, assistantName) => ({
  ANOMALY_DETECTION: {
    description:
      'Watches CPU, memory, latency, replica counts, error rates and cloud spend, and raises an event when a metric leaves its learned baseline.',
    whenToTurnOn: 'You want regressions surfaced that nobody wrote an alert rule for.',
    why: 'Baseline detection creates events of its own, so tenants already alerting on the same signals would get duplicates.',
  },
  TRIAGE_LLM_SCORING: {
    description: 'Classifies each alert and applies a fixed policy to derive a P0-P3 priority, replacing the severity x environment formula.',
    whenToTurnOn: 'Your P1 queue is full of things nobody would page for, and severity labels differ across alert sources.',
    why: 'The legacy formula only sees declared severity and environment. Classification reads the alert itself; the deterministic policy keeps the result reproducible and auditable.',
  },
  WEBHOOK_LLM_RESOLUTION: {
    description: 'Works out which service, workload or cluster a generic webhook alert is about when the payload has no matchable labels.',
    whenToTurnOn: 'A meaningful share of your alerts arrive over generic webhooks rather than a first-class integration.',
    why: 'Free-text webhook alerts land unattached and cannot be investigated or grouped.',
  },
  EVENT_AUTO_AI_SUMMARY: {
    description:
      'Analyses each event on arrival and attaches a summary, so responders open an event that already has context. Master switch for auto-analysis.',
    whenToTurnOn: 'Leave on. Turn off per account when trialling, or when a noisy account should only be analysed on request.',
    why: 'Top of the automatic pipeline: every downstream stage depends on it. Also the single lever to stop model spend on unattended events.',
  },
  GENERATE_RCA: {
    description: 'Produces a written root cause analysis: the evidence chain from symptom to cause, not just a description of what fired.',
    whenToTurnOn: 'You want investigation output that survives into postmortems and tickets.',
    why: 'A summary says what happened; an RCA argues why. It costs materially more to produce, so it is separately switchable.',
  },
  EVENT_DEBUG_ANALYSIS_DISABLED: {
    description: 'The deeper investigation pass that pulls logs and traces beyond the initial summary.',
    whenToTurnOn: 'Leave on. Turn off for high-volume accounts, or where log access is not configured yet.',
    why: 'Most expensive stage in the pipeline and the most likely to hit log-volume limits. Switching it off per account keeps the cheap stages running.',
  },
  EVENT_INVESTIGATION_SKIP_SERVICE_LABEL_CHECK: {
    description:
      'Lets investigation run on events that carry no service label instead of skipping them. Accuracy drops, because the subject has to be inferred.',
    whenToTurnOn: 'Your alerts genuinely lack service labels and you accept lower-confidence results while labelling is fixed upstream.',
    why: 'The label check is a quality gate. Some estates do not label consistently, and no investigation at all is worse than an approximate one.',
  },
  EVENT_AUTO_RAISE_PR_ENABLED: {
    description: 'Opens a draft pull request with a proposed fix when log analysis localises a code-level cause. Nothing merges automatically.',
    whenToTurnOn: 'Repository mapping is in place and your team will triage bot-authored PRs.',
    why: `The only flag that causes ${baseTitle} to write outside its own system, so it stays opt-in and requires a mapped repository.`,
  },
  MEMORY_MODULE: {
    description:
      'Lets the assistant carry context across sessions (your environment, decisions already made, patterns it has seen) instead of starting cold.',
    whenToTurnOn: 'You use the assistant regularly and want it to improve with use.',
    why: 'The difference between re-asking which cluster is production every session and knowing. It stores tenant-specific material, so enrolment is deliberate.',
  },
  AI_WORKFLOW_TOOLS: {
    description:
      "Lets the assistant run automations you have explicitly marked as AI-invokable, plus curated built-in actions. Every run needs in-chat confirmation and the user's own permissions.",
    whenToTurnOn: 'You have automations you trust and want them reachable from a conversation.',
    why: "Moves the assistant from advising to acting, so it carries three guardrails: per-workflow opt-in, confirmation, and the caller's permissions. This flag is the outermost.",
  },
  LLM_FUNCTION: {
    description: 'Lets your team author and edit custom AI functions and attach them to alerts.',
    whenToTurnOn: 'You have repeatable per-alert investigation steps worth encoding.',
    why: 'Built-in analysis is general. A function encodes the check a team always runs for a particular alert and makes it automatic.',
  },
  CHANNEL_AWARENESS: {
    description: `${toAssistantLabel(
      assistantName
    )} follows the messaging channels you opt in and uses the recent conversation as context when mentioned.`,
    whenToTurnOn: 'Incident work happens in chat and re-pasting context into every mention is the friction.',
    why: 'Reading a channel is sensitive, so it is opt-in per channel: enabling joins the channel and posts a visible disclosure, and reverts if the disclosure fails.',
  },
  EVENT_ANALYSIS_ON_CHANNEL: {
    description: 'Delivers event analysis into the incident channel as it completes, instead of leaving it to be found in the UI.',
    whenToTurnOn: 'Responders live in the channel and would not otherwise open the event.',
    why: 'Analysis is most useful during the incident, in the place the incident is being run. The trade-off is channel volume.',
  },
  AI_COST_REPORT: {
    description: 'A daily and month-to-date digest of your AI and LLM spend, delivered to Slack and shown on the Accounts tab.',
    whenToTurnOn: 'Someone owns the AI budget and wants the number without opening the dashboard.',
    why: `Model usage is the one ${baseTitle} cost that moves with how much you use it. The digest makes that visible daily rather than at invoice time.`,
  },
  LLM_ANALYSER: {
    description: 'Adds the LLM Analyser tab to Optimise, breaking model cost and usage down by model, feature and account.',
    whenToTurnOn: 'You are actively tuning which features justify their model cost.',
    why: 'The spend report answers how much; this answers on what. Most tenants want the number long before the breakdown.',
  },
  UPGRADE_PLANNER: {
    description:
      'Reads your cluster configuration and produces a step-by-step Kubernetes upgrade plan, including deprecated APIs and workloads needing attention.',
    whenToTurnOn: 'An upgrade is on the roadmap and you want the blockers enumerated before starting.',
    why: 'Upgrade planning is periodic rather than continuous, and the plan is only as good as the collected inventory.',
  },
  AI_ANSWER_CONFIDENCE: {
    description:
      'Rates how well each investigation answer is backed by the tools that actually ran, and shows the result as a high/medium/low badge on the answer.',
    whenToTurnOn: 'Responders act on investigation answers directly and you want the weakly-supported ones to say so before someone does.',
    why: 'A separate grading pass, so a model that wrote a thin answer cannot also mark it confident. It costs one extra AI call per investigation, which is why it is per-tenant rather than always on.',
  },
  CUSTOM_ROLES: {
    description: 'Tenant-defined roles carrying module-level Read, Write and Execute grants, additive to the built-in roles.',
    whenToTurnOn:
      'You need a role the built-ins do not express: auditor, workflow operator, cost analyst. Turning it back off is safe, definitions just stop applying.',
    why: 'Built-in roles are coarse. Because this switches on the grant mechanism that performs permission checks, it is admin-only and has a deployment-wide kill switch.',
  },
  TROUBLESHOOT: {
    description: "AI incident investigation product tier, metered against your plan's monthly incident allowance.",
    whenToTurnOn: 'Not a toggle. It changes with the plan.',
    why: 'An entitlement set by the subscription, not a product setting.',
  },
  OPTIMIZE: {
    description: 'Cost optimisation product tier: rightsizing, spend analysis and recommendations.',
    whenToTurnOn: 'Not a toggle. It changes with the plan.',
    why: 'Same entitlement mechanism as Troubleshoot, tracked on the honour system rather than metered.',
  },
});

// Recommended Category and Who Can Change It are in the sheet but weren't part of the reviewed design, so they're omitted here.
const FEATURE_TABLE_HEADERS = [
  { name: 'Feature', width: '44%' },
  { name: 'When to turn it on', width: '27%' },
  { name: 'Why / notes', width: '12%', align: 'center' },
  { name: 'Toggle', width: '17%', align: 'right' },
];

// Where a flag surfaces once it's on: `route` for a plain in-app path, or `path`/`reason` when it needs context this modal doesn't have.
const FEATURE_NAVIGATION = {
  LLM_ANALYSER: { route: '/optimise#llm-analyser' },
  AI_COST_REPORT: { route: '/optimise#llm-analyser' },
  CUSTOM_ROLES: { route: '/user-management#access-users/roles' },
  CHANNEL_AWARENESS: { route: '/user-management#integrations' },
  TROUBLESHOOT: { route: '/troubleshoot' },
  OPTIMIZE: { route: '/optimise' },
  UPGRADE_PLANNER: {
    path: 'Kubernetes › Details › Upgrade Planner',
    reason: 'Needs a cluster selected first, so it has no standalone link.',
  },
  ANOMALY_DETECTION: {
    path: 'Kubernetes › Details › Events › Anomaly',
    reason: 'Needs a cluster selected first, so it has no standalone link.',
  },
  AI_WORKFLOW_TOOLS: {
    path: 'Automation › a workflow’s settings',
    reason: 'Needs a specific workflow selected first, so it has no standalone link.',
  },
  GENERATE_RCA: {
    path: 'Investigate › an event’s analysis',
    reason: 'Only appears while investigating a specific event, not as a standalone screen.',
  },
  LLM_FUNCTION: {
    path: 'Ask {Assistant} Settings, or Kubernetes › Create Alert',
    reason: 'Lives inside existing modals — there is no dedicated screen to link to.',
  },
  MEMORY_MODULE: {
    path: 'Ask {Assistant} › Memory',
    reason: 'Lives inside the chat assistant panel — there is no dedicated screen to link to.',
  },
  AI_ANSWER_CONFIDENCE: {
    path: 'Ask {Assistant} › a completed investigation answer',
    reason: 'The badge sits on the answer itself, so it only appears once an investigation has run.',
  },
  WEBHOOK_LLM_RESOLUTION: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
  TRIAGE_LLM_SCORING: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
  EVENT_AUTO_AI_SUMMARY: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
  EVENT_ANALYSIS_ON_CHANNEL: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
  EVENT_DEBUG_ANALYSIS_DISABLED: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
  EVENT_INVESTIGATION_SKIP_SERVICE_LABEL_CHECK: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
  EVENT_AUTO_RAISE_PR_ENABLED: { reason: 'Backend-only behavior — there is no screen it turns on or off.' },
};

const FeatureNavigationIcon = ({ feature }) => {
  const nav = FEATURE_NAVIGATION[feature.value];
  if (!nav) return null;
  const label = feature.display_name || feature.value;
  if (nav.route) {
    return (
      <DsTooltip title={`Open ${label}`} placement='top'>
        <Link href={nav.route} target='_blank' aria-label={`Open ${label}`} style={{ display: 'inline-flex', color: ds.gray[400] }}>
          <OpenInNewIcon sx={{ fontSize: 13 }} />
        </Link>
      </DsTooltip>
    );
  }
  return (
    <DsTooltip title={nav.path ? `${fillBrandTokens(nav.path)} — ${nav.reason}` : nav.reason} placement='top'>
      <Box component='span' aria-label={`${label} has no direct link`} sx={{ display: 'inline-flex', opacity: 0.4 }}>
        <OpenInNewIcon sx={{ fontSize: 13 }} />
      </Box>
    </DsTooltip>
  );
};

const FeatureNameCell = ({ feature, description, isOn, showFlagIds }) => (
  <Box minWidth={0} display='flex' flexDirection='column' gap='4px'>
    <Box display='flex' alignItems='flex-start' justifyContent='space-between' gap='8px'>
      <Box minWidth={0} display='flex' alignItems='center' gap='4px'>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>
          {feature.display_name || feature.value}
        </Typography>
        {/* Only once the flag is on -- for an off feature there's nothing to navigate to. */}
        {isOn && <FeatureNavigationIcon feature={feature} />}
      </Box>
      {/* Raw id, right-aligned on the same line; omitted when there's no display name since the title is already the id. */}
      {showFlagIds && feature.display_name && (
        <Typography sx={{ fontSize: ds.text.caption, fontFamily: ds.font.mono, color: ds.gray[400], whiteSpace: 'nowrap', flexShrink: 0 }}>
          {feature.value}
        </Typography>
      )}
    </Box>
    {description && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: 1.5 }}>{description}</Typography>}
  </Box>
);

const FeatureWhyCell = ({ text }) => {
  if (!text) return null;
  return (
    <DsTooltip title={text} placement='top'>
      <Box component='span' sx={{ display: 'inline-flex', opacity: 0.6 }}>
        <SafeIcon src={infoIcon} alt='Why it exists' width={14} height={14} />
      </Box>
    </DsTooltip>
  );
};

const FeatureToggleCell = ({ feature, choice, canEdit, onChange }) => {
  // Plain on/off switch: with no override it shows the flag's shipped polarity instead of an unselected state.
  const effectiveChoice = effectiveFeatureChoice(feature, choice);
  return (
    <Switch
      size='md'
      checked={effectiveChoice === 'on'}
      onChange={(_, checked) => onChange(checked ? 'on' : 'off')}
      disabled={!canEdit}
      aria-label={feature.display_name || feature.value}
    />
  );
};

const FEATURE_STATUS_DEFAULT = 'default';

// A few flags store the opposite of their name (e.g. EVENT_DEBUG_ANALYSIS_DISABLED='enabled' means OFF); feature.stored_value_inverted marks those.
const displayedFromStored = (status, inverted) => {
  const on = status === 'enabled';
  return (inverted ? !on : on) ? 'on' : 'off';
};

const storedFromDisplayed = (choice, inverted) => {
  if (choice === 'default') {
    return FEATURE_STATUS_DEFAULT;
  }
  const on = choice === 'on';
  return (inverted ? !on : on) ? 'enabled' : 'disabled';
};

// Resolves the tri-state stored choice to what the flag is actually doing, using the catalog's polarity when unset.
const effectiveFeatureChoice = (feature, choice) => (choice === 'default' ? (feature.polarity === 'default_on' ? 'on' : 'off') : choice);

// Synthetic id for the "All" tab ahead of the per-category feature tabs.
const ALL_FEATURES_GROUP_ID = 'all';

// Admin > Tenant Settings tab body — relocated here from an avatar-menu modal
// (see docs/ia-consolidation-plan.md), so this now mounts once as a page
// section rather than being opened/closed. `initialLoading` gates a
// full-section loader for the first fetch only, so a subsequent Save doesn't
// blank the page out from under the user — that used `loading` alone via the
// old modal wrapper's own overlay, which no longer exists here.
const TenantSettings = () => {
  const { title: baseTitle, assistantName } = useBrandingConfig();
  const { data: session, update } = useSession();
  const VALID_ROLES = ['tenant_admin', 'tenant_admin_readonly'];

  const [logSettings, setLogSettings] = useState({
    logPodLabel: '',
    logNamespaceLabel: '',
    logAppLabel: '',
  });
  const [traceSettings, setTraceSettings] = useState({ ...EMPTY_TRACE_LABEL_SETTINGS });
  const [loading, setLoading] = useState(false);
  const [initialLoading, setInitialLoading] = useState(true);
  const [activeTab, setActiveTab] = useState('general');
  const [activeLabelTab, setActiveLabelTab] = useState('log');
  const [activeFeatureGroup, setActiveFeatureGroup] = useState(ALL_FEATURES_GROUP_ID);
  const [showFlagIds, setShowFlagIds] = useState(false);
  // Each flag is on/off/default, since "default" (no explicit row) and "off" are distinct states for a default-on flag.
  const [featureChoices, setFeatureChoices] = useState({});
  const [initialChoices, setInitialChoices] = useState({});
  const [featureOptions, setFeatureOptions] = useState([]);
  const [tenantName, setTenantName] = useState(session?.tenant?.name);
  const [checkboxEnabled, setCheckboxEnabled] = useState(false);
  const [allowDomainValue, setAllowDomainValue] = useState('');
  const [defaultAuthRole, setDefaultAuthRole] = useState('');
  const [selectedObservabilityPlatform, setSelectedObservabilityPlatform] = useState('');
  const [webhookLabelMapping, setWebhookLabelMapping] = useState({
    subject_name_labels: DEFAULT_SUBJECT_NAME_LABELS,
    namespace_labels: DEFAULT_NAMESPACE_LABELS,
    severity_labels: DEFAULT_SEVERITY_LABELS,
  });
  const [webhookMappingSaved, setWebhookMappingSaved] = useState(false);
  const [webhookMappingModified, setWebhookMappingModified] = useState(false);

  // Read-only for anyone but a tenant admin, super admin, or `tenants:Write` holder -- mirrors the backend CanManage gate every write goes through.
  const canEdit = canEditTenantSettings();

  useEffect(() => {
    let cancelled = false;
    const fetchTenantAttributes = async () => {
      try {
        setLoading(true);
        const [tenantAttributes, features] = await Promise.all([getTenantAttributes(), getFeatures()]);
        if (cancelled) {
          return;
        }
        if (features.length > 0) {
          setFeatureOptions(features);
        }

        if (tenantAttributes) {
          const logLabelValues = tenantAttributes.find((attr) => attr.name === 'log_labels');
          const allowedDomains = tenantAttributes.find((attr) => attr.name === 'allowed_domains');
          const defaultLogProvider = tenantAttributes.find((attr) => attr.name === 'default_log_provider');
          setSelectedObservabilityPlatform(defaultLogProvider?.value || '');
          if (logLabelValues && Object.keys(logLabelValues).length > 0) {
            const labels = safeJSONParse(logLabelValues.value) ?? logLabelValues.value;
            setLogSettings({
              logPodLabel: labels.pod || '',
              logNamespaceLabel: labels.namespace || '',
              logAppLabel: labels.app || '',
            });
          }
          // Guarded on the stored value, not on the row having keys the way the log
          // block above does — a found attribute row always has keys, so that test is
          // always true and says nothing about whether a mapping was configured.
          const traceLabelValues = tenantAttributes.find((attr) => attr.name === 'trace_labels');
          if (traceLabelValues?.value) {
            // safeJSONParse returns null on a malformed blob; traceLabelsToSettings then
            // yields the all-empty shape rather than spraying undefined into the inputs.
            setTraceSettings(traceLabelsToSettings(safeJSONParse(traceLabelValues.value)));
          }
          if (allowedDomains && Object.keys(allowedDomains).length > 0) {
            try {
              const parsedDomains = safeJSONParse(allowedDomains.value) ?? allowedDomains.value;
              const validDomains = Array.isArray(parsedDomains) ? parsedDomains.filter((d) => d?.trim()) : [];
              setCheckboxEnabled(validDomains.length > 0);
              setAllowDomainValue(validDomains.join(','));
            } catch (error) {
              console.error('Failed to parse allowed domains', error);
              setCheckboxEnabled(false);
              setAllowDomainValue('');
            }
          }
          const defaultRoleAttr = tenantAttributes.find((attr) => attr.name === 'auth_default_role');
          if (defaultRoleAttr && defaultRoleAttr.value) {
            setDefaultAuthRole(defaultRoleAttr.value);
          }

          const webhookMapping = tenantAttributes.find((attr) => attr.name === 'webhook_label_mapping');
          if (webhookMapping?.value) {
            try {
              const parsed = safeJSONParse(webhookMapping.value) ?? webhookMapping.value;
              setWebhookLabelMapping({
                subject_name_labels: parsed.subject_name_labels ?? DEFAULT_SUBJECT_NAME_LABELS,
                namespace_labels: parsed.namespace_labels ?? DEFAULT_NAMESPACE_LABELS,
                severity_labels: parsed.severity_labels ?? DEFAULT_SEVERITY_LABELS,
              });
              setWebhookMappingSaved(true);
            } catch (e) {
              console.error('Failed to parse webhook_label_mapping', e);
            }
          }
        }

        const tenantFeatureFlags = await fetchFeatureFlagsForTenant();
        if (cancelled) {
          return;
        }
        if (tenantFeatureFlags?.length > 0) {
          // Uses `features` fetched above, not featureOptions state, which React hasn't committed yet at this point.
          const invertedIds = new Set((features || []).filter((f) => f.stored_value_inverted).map((f) => f.value));
          const choices = {};
          tenantFeatureFlags.forEach((g) => {
            if (g.status === 'enabled' || g.status === 'disabled') {
              choices[g.feature_id] = displayedFromStored(g.status, invertedIds.has(g.feature_id));
            }
          });
          setFeatureChoices(choices);
          setInitialChoices(choices);
        }
      } catch (error) {
        if (!cancelled) {
          snackbar.error(`Failed to fetch Tenant settings - ${parseHttpResponseBodyMessage(error)}`);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
          setInitialLoading(false);
        }
      }
    };

    const fetchTenant = async () => {
      try {
        apiUserManagement.listUserTenants(session?.user?.email).then((res) => {
          if (cancelled) {
            return;
          }
          const tenants = res.data ?? [];
          if (tenants.length > 0) {
            // Falls back to the session name (not blank) to guard against a momentarily stale session after a rename. See issue #32696.
            setTenantName(tenants.find((t) => t.name === session?.tenant?.name)?.name || session?.tenant?.name || '');
          }
        });
      } catch {
        if (!cancelled) {
          setTenantName('');
        }
      }
    };

    fetchTenantAttributes();
    fetchTenant();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- mount-once fetch, same as the modal version's `[open]` effect it replaces
  }, []);

  const handleSaveSettings = async () => {
    // Belt-and-braces: the Save button isn't rendered without canEdit, but this handler is the only path to these mutations.
    if (!canEdit) return;
    setLoading(true);
    // Only true when every op below succeeds -- without it, the success toast fired on the catch path too. See issue #32868.
    let saveSucceeded = false;
    try {
      if (checkboxEnabled && !allowDomainValue.trim()) {
        snackbar.error('Allowed Domains field cannot be empty when domain login is enabled.');
        setLoading(false);
        return;
      }
      if (checkboxEnabled && defaultAuthRole.trim() !== '' && !VALID_ROLES.includes(defaultAuthRole.trim())) {
        snackbar.error("Invalid role. Allowed roles are 'tenant_Admin' or 'tenant_admin_readonly'.");
        setLoading(false);
        return;
      }
      const attrsToSave = [
        {
          name: 'log_labels',
          value: JSON.stringify({
            pod: logSettings.logPodLabel,
            namespace: logSettings.logNamespaceLabel,
            app: logSettings.logAppLabel,
          }),
        },
        // Written unconditionally, like log_labels and unlike webhook_label_mapping:
        // trace_labels has no non-empty defaults, so an untouched form serialises to all
        // empty strings, which getTenantTraceLabels discards. Gating the write instead
        // would make clearing the last override a silent no-op.
        { name: 'trace_labels', value: traceSettingsToLabelsValue(traceSettings) },
        { name: 'default_log_provider', value: selectedObservabilityPlatform ? selectedObservabilityPlatform : '' },
      ];
      if (webhookMappingModified || webhookMappingSaved) {
        attrsToSave.push({
          name: 'webhook_label_mapping',
          value: JSON.stringify({
            subject_name_labels: webhookLabelMapping.subject_name_labels,
            namespace_labels: webhookLabelMapping.namespace_labels,
            severity_labels: webhookLabelMapping.severity_labels,
          }),
        });
      }
      const response = await upsertTenantAttributes(attrsToSave);

      if (response?.data?.errors) {
        snackbar.error(`Failed to save label configuration - ${parseHttpResponseBodyMessage(response.data)}`);
        return;
      }

      // 'default' is sent through to the API, which deletes the row rather than storing that word.
      const updatePayload = featureOptions
        .filter((f) => (featureChoices[f.value] || 'default') !== (initialChoices[f.value] || 'default'))
        .map((f) => ({
          feature_id: f.value,
          status: storedFromDisplayed(featureChoices[f.value] || 'default', f.stored_value_inverted),
        }));

      if (updatePayload.length > 0) {
        const updateFeatureFlagResponse = await updateTenantFeatureFlag(updatePayload);
        if (updateFeatureFlagResponse?.data?.errors) {
          snackbar.error(`Failed to save feature configuration - ${parseHttpResponseBodyMessage(updateFeatureFlagResponse.data)}`);
          return;
        }
        snackbar.success('Feature configuration saved.');
        fetchFeatureFlagsForTenant(true); // refresh cache
      }
      if (checkboxEnabled) {
        const updateAllowedLoginDomainResponse = await upsertTenantAttributes([
          {
            name: 'allowed_domains',
            value: JSON.stringify(
              allowDomainValue
                ? allowDomainValue
                    .split(',')
                    .map((d) => d.trim())
                    .filter(Boolean)
                : []
            ),
          },
          {
            name: 'auth_default_role',
            value: defaultAuthRole || '',
          },
        ]);
        if (updateAllowedLoginDomainResponse?.data?.errors) {
          snackbar.error(`Failed to save allowed login domain - ${parseHttpResponseBodyMessage(updateAllowedLoginDomainResponse.data)}`);
          return;
        }
      } else if (!checkboxEnabled) {
        const deleteAllowedLoginDomainResponse = await deleteTenantAttributes(['allowed_domains', 'auth_default_role']);
        if (deleteAllowedLoginDomainResponse?.data?.errors) {
          snackbar.error(`Failed to delete allowed login domain - ${parseHttpResponseBodyMessage(deleteAllowedLoginDomainResponse.data)}`);
          return;
        }
      }
      if (tenantName !== session?.tenant?.name) {
        const tenantId = session?.tenant?.id;
        if (!tenantId) {
          snackbar.error('Failed to update tenant name - no active tenant session found');
          return;
        }
        const response = await updateTenantName(tenantId, tenantName);
        if (response?.data?.errors) {
          snackbar.error(`Failed to update tenant name - ${parseHttpResponseBodyMessage(response?.data)}`);
          return;
        }
        // Refreshes the JWT so the renamed tenant propagates; otherwise the sidebar and next modal open stay stale. See issue #32696.
        await update({ ...session, tenantId });
      }
      saveSucceeded = true;
    } catch (error) {
      snackbar.error(`Error while saving settings - ${parseHttpResponseBodyMessage(error)}`);
    } finally {
      // Best-effort refresh -- a failure here must not leave the Save button stuck spinning.
      await getTenantAttributes(true).catch((e) => console.error('Failed to refresh tenant attributes:', e));
      setLoading(false);
    }

    if (saveSucceeded) {
      snackbar.success('Tenant Settings saved successfully');
    }
  };

  const handleFeatureChoice = (featureValue, choice) => {
    setFeatureChoices((prev) => ({ ...prev, [featureValue]: choice }));
  };

  const buildFeatureRow = (f) => {
    const content = featureRecommendedContent(baseTitle, assistantName)[f.value];
    return [
      {
        component: (
          <FeatureNameCell
            feature={f}
            description={content?.description || f.description}
            isOn={effectiveFeatureChoice(f, featureChoices[f.value] || 'default') === 'on'}
            showFlagIds={showFlagIds}
          />
        ),
      },
      {
        component: <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: 1.5 }}>{content?.whenToTurnOn || '—'}</Typography>,
      },
      { component: <FeatureWhyCell text={content?.why} />, align: 'center' },
      {
        component: (
          <FeatureToggleCell
            feature={f}
            choice={featureChoices[f.value] || 'default'}
            canEdit={canEdit}
            onChange={(choice) => handleFeatureChoice(f.value, choice)}
          />
        ),
        align: 'right',
      },
    ];
  };

  // Memoized: this runs for the Tabs list and again inside currentFeatureGroup, and the modal re-renders on every keystroke elsewhere.
  const featureGroups = useMemo(() => {
    const groups = new Map();
    featureOptions.forEach((f) => {
      const id = f.category || 'other';
      if (!groups.has(id)) {
        groups.set(id, { id, label: f.category_label || 'Other', order: f.category_sort_order ?? 999, items: [] });
      }
      groups.get(id).items.push(f);
    });
    // Groups keep the catalog's curated order; rows inside a group go A-Z by the name shown.
    return [...groups.values()]
      .map((g) => ({ ...g, items: [...g.items].sort((x, y) => (x.display_name || x.value || '').localeCompare(y.display_name || y.value || '')) }))
      .sort((a, b) => a.order - b.order);
  }, [featureOptions]);

  // Every flag in one list, so finding one doesn't require knowing its category first.
  const allFeaturesGroup = useMemo(
    () => ({
      id: ALL_FEATURES_GROUP_ID,
      label: 'All',
      items: [...featureOptions].sort((x, y) => (x.display_name || x.value || '').localeCompare(y.display_name || y.value || '')),
    }),
    [featureOptions]
  );

  // Falls back to the first group so the panel still renders before a sub-tab is picked.
  const currentFeatureGroup = () => {
    if (activeFeatureGroup === ALL_FEATURES_GROUP_ID) return allFeaturesGroup;
    return featureGroups.find((g) => g.id === activeFeatureGroup) || featureGroups[0] || null;
  };

  if (initialLoading) {
    return <Loader />;
  }

  return (
    <>
      {/* No background/padding chrome here -- matches how AnchorComponent itself
          wraps a tabOptions sub-tab strip elsewhere (just position + bottom
          margin), rather than boxing it in its own card-like surface. Not
          sticky either (unlike the modal version this replaced): the page's own
          top-level tab strip may already be sticky, and stacking two sticky
          bars without a live browser to check the result risks an overlap this
          diff can't verify. */}
      <Box sx={{ position: 'relative', mb: ds.space[4] }}>
        <Tabs
          options={{
            tabOptions: [
              { value: 'general', text: 'General', icon: SettingsOutlinedIcon, iconSize: 16, showBottomMargin: true },
              { value: 'labels', text: 'Label Mapping', icon: LabelOutlinedIcon, iconSize: 16, showBottomMargin: true },
              { value: 'features', text: 'Features', icon: TuneOutlinedIcon, iconSize: 16, showBottomMargin: true },
            ],
          }}
          value={activeTab}
          onChange={(next) => setActiveTab(next)}
          smallSize
          behavior='filter'
          variant='primary'
          ariaLabel='Tenant settings'
        />
      </Box>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[5], pb: ds.space[5] }}>
        {/* Stated once here rather than as a tooltip on each of the ~12 controls, since every field is inert for a read-only viewer. */}
        {!canEdit && <Banner tone='info' message={missingPermissionMessage('tenants:Write')} />}
        {activeTab === 'general' && (
          <Card variant='outlined' elevation='flat'>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[5], alignItems: 'start' }}>
              <Box display='flex' flexDirection='column' gap={ds.space[3]}>
                <SectionHeader title='Tenant Identity' description='Display name shown across the app.' />
                <Input size='sm' label='Tenant Name' value={tenantName} onChange={setTenantName} disabled={!canEdit} />
              </Box>

              <Box display='flex' flexDirection='column' gap={ds.space[3]}>
                <SectionHeader title='Self-Onboarding' description='Self-onboarding rules for users joining this tenant.' />
                <Checkbox
                  size='sm'
                  checked={checkboxEnabled}
                  label='Allow self-onboarding via domain login'
                  onChange={() => setCheckboxEnabled((prev) => !prev)}
                  disabled={!canEdit}
                />
                <Box display='flex' flexDirection='column' gap={ds.space[3]}>
                  <Input
                    size='sm'
                    label='Allowed Domains'
                    value={allowDomainValue}
                    onChange={setAllowDomainValue}
                    disabled={!canEdit || !checkboxEnabled}
                    placeholder='Enter allowed login domains, such as gmail.com'
                  />
                  <Input
                    size='sm'
                    label='Default Auth Role'
                    value={defaultAuthRole}
                    instructionText='Only "tenant_admin" or "tenant_admin_readonly" are allowed'
                    onChange={(value) => setDefaultAuthRole(value?.trim())}
                    disabled={!canEdit || !checkboxEnabled}
                    placeholder='Enter default auth role for self-onboarding'
                  />
                </Box>
              </Box>
            </Box>

            {/* handleSaveSettings commits pending changes from all tabs at once, not just
                General's -- each tab/subtab just carries its own Save button to this one handler. */}
            {canEdit && (
              <Box
                sx={{
                  display: 'flex',
                  justifyContent: 'flex-end',
                  mt: ds.space[5],
                  pt: ds.space[4],
                  borderTop: `1px solid ${ds.gray[200]}`,
                }}
              >
                <Button size='md' onClick={handleSaveSettings} loading={loading} sx={{ minWidth: ds.space.mul(0, 70) }}>
                  Save
                </Button>
              </Box>
            )}
          </Card>
        )}

        {activeTab === 'labels' && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
            <Tabs
              options={{
                tabOptions: [
                  { value: 'log', text: 'Logs' },
                  { value: 'trace', text: 'Traces' },
                  { value: 'webhook', text: 'Webhook alerts' },
                ],
              }}
              value={activeLabelTab}
              onChange={(next) => setActiveLabelTab(next)}
              smallSize
              behavior='filter'
              variant='secondary'
              ariaLabel='Label mapping'
            />
            {activeLabelTab === 'log' && (
              <Card
                variant='outlined'
                elevation='flat'
                header={
                  <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: ds.space[3] }}>
                    <SectionHeader description='Map Logs label keys to product concepts.' />
                    {canEdit && (
                      <Button size='sm' onClick={handleSaveSettings} loading={loading} sx={{ minWidth: ds.space.mul(0, 70) }}>
                        Save
                      </Button>
                    )}
                  </Box>
                }
              >
                <Box display='flex' flexDirection='column' gap={ds.space[3]}>
                  <TenantAccountCommonSettings idPrefix='log-label' settings={logSettings} setSettings={setLogSettings} disabled={!canEdit} />
                  {/* This card maps label names only. Two boxes used to sit below the grid and
                      both wrote keys no reader ever consumed: "Default query" (#37402) and
                      "Cluster Label" (tenant_attrs.log_cluster_label, frontend-only since it
                      shipped). The working setting is per-account on the log integration, so
                      name it here rather than leave operators hunting for it. */}
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
                    Looking for filters applied to every log query? Those are per-account on the log integration: Integrations &rarr; your log
                    integration &rarr; Advanced Settings &rarr; Default Log Filters.
                  </Typography>
                </Box>
              </Card>
            )}

            {activeLabelTab === 'trace' && (
              <Card
                variant='outlined'
                elevation='flat'
                header={
                  <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: ds.space[3] }}>
                    <SectionHeader description="Map your trace backend's field names onto the canonical trace fields. Leave a field blank to use the provider default." />
                    {canEdit && (
                      <Button size='sm' onClick={handleSaveSettings} loading={loading} sx={{ minWidth: ds.space.mul(0, 70) }}>
                        Save
                      </Button>
                    )}
                  </Box>
                }
              >
                <TenantAccountCommonSettings
                  title='Trace Label Mapper'
                  idPrefix='trace-label'
                  fields={TRACE_LABEL_FIELDS}
                  advancedFields={TRACE_LABEL_ADVANCED_FIELDS}
                  advancedLabel='advanced trace fields'
                  settings={traceSettings}
                  setSettings={setTraceSettings}
                  disabled={!canEdit}
                />
              </Card>
            )}

            {activeLabelTab === 'webhook' && (
              <Card
                variant='outlined'
                elevation='flat'
                header={
                  <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: ds.space[3] }}>
                    <SectionHeader description='Map alert label keys to event fields. Order matters — first non-empty value is used.' />
                    {canEdit && (
                      <Button size='sm' onClick={handleSaveSettings} loading={loading} sx={{ minWidth: ds.space.mul(0, 70) }}>
                        Save
                      </Button>
                    )}
                  </Box>
                }
              >
                <Box display='flex' flexDirection='column' gap={ds.space[3]}>
                  <Typography sx={{ color: ds.gray[600], fontSize: ds.text.body, lineHeight: ds.space.mul(0, 10) }}>
                    You can type custom label keys not in the suggestions. For advanced extraction, use Jinja2 templates (e.g.{' '}
                    {"{{ labels.app_id | split(sep='/') | last }}"}) or regex (e.g. app_id|/k8s/[^/]+/(.+)).
                  </Typography>
                  <FilterDropdown
                    multiple
                    freeSolo
                    label='Subject Name Labels'
                    value={webhookLabelMapping.subject_name_labels}
                    options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...webhookLabelMapping.subject_name_labels])]}
                    onSelect={(e) => {
                      setWebhookLabelMapping((prev) => ({ ...prev, subject_name_labels: e.target.value }));
                      setWebhookMappingModified(true);
                    }}
                    limitTag={3}
                    disabled={!canEdit}
                  />
                  <FilterDropdown
                    multiple
                    freeSolo
                    label='Namespace Labels'
                    value={webhookLabelMapping.namespace_labels}
                    options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...webhookLabelMapping.namespace_labels])]}
                    onSelect={(e) => {
                      setWebhookLabelMapping((prev) => ({ ...prev, namespace_labels: e.target.value }));
                      setWebhookMappingModified(true);
                    }}
                    limitTag={3}
                    disabled={!canEdit}
                  />
                  <FilterDropdown
                    multiple
                    freeSolo
                    label='Severity Labels'
                    value={webhookLabelMapping.severity_labels}
                    options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...webhookLabelMapping.severity_labels])]}
                    onSelect={(e) => {
                      setWebhookLabelMapping((prev) => ({ ...prev, severity_labels: e.target.value }));
                      setWebhookMappingModified(true);
                    }}
                    limitTag={3}
                    disabled={!canEdit}
                  />
                </Box>
              </Card>
            )}
          </Box>
        )}

        {activeTab === 'features' && (
          <Box>
            <Card
              variant='outlined'
              elevation='flat'
              header={
                <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: ds.space[3] }}>
                  <SectionHeader
                    title='Feature Flags'
                    description={`Control which ${toAssistantLabel(
                      assistantName
                    )} capabilities are active for your tenant. Toggle a feature on or off, changes apply immediately.`}
                  />
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3] }}>
                    {canEdit && (
                      <Button size='sm' onClick={handleSaveSettings} loading={loading} sx={{ minWidth: ds.space.mul(0, 70) }}>
                        Save
                      </Button>
                    )}
                    <Button
                      composition='icon-only'
                      tone='ghost'
                      size='sm'
                      icon={showFlagIds ? <VisibilityOffOutlinedIcon sx={{ fontSize: 16 }} /> : <VisibilityOutlinedIcon sx={{ fontSize: 16 }} />}
                      aria-label={showFlagIds ? 'Hide flag ids' : 'Show flag ids'}
                      tooltip={showFlagIds ? 'Hide flag ids' : 'Show flag ids'}
                      onClick={() => setShowFlagIds((v) => !v)}
                    />
                  </Box>
                </Box>
              }
            >
              <Box display='flex' flexDirection='column' gap={ds.space[4]}>
                <Tabs
                  options={{
                    tabOptions: [
                      { value: allFeaturesGroup.id, text: allFeaturesGroup.label, count: allFeaturesGroup.items.length },
                      ...featureGroups.map((g) => ({ value: g.id, text: g.label, count: g.items.length })),
                    ],
                  }}
                  value={currentFeatureGroup()?.id || ''}
                  onChange={(next) => setActiveFeatureGroup(next)}
                  smallSize
                  behavior='filter'
                  variant='secondary'
                  ariaLabel='Feature groups'
                />
                <CustomTable
                  id='tenant-features-table'
                  headers={FEATURE_TABLE_HEADERS}
                  tableData={(currentFeatureGroup()?.items || []).map(buildFeatureRow)}
                />
              </Box>
            </Card>
          </Box>
        )}
      </Box>
      {/* No Cancel/Close here -- there's no dialog to dismiss now that this is a
          page tab, not a modal. Switching to another Admin tab remounts this one
          fresh (ErrorBoundary's key in user-management/index.jsx), so navigating
          away already discards unsaved edits the same way Cancel used to.
          Every tab (General, Labels' three subtabs, Features) carries its own Save
          in its card header/footer now -- no shared bottom bar. */}
    </>
  );
};

export default TenantSettings;
