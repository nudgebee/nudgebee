import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import CheckIcon from '@mui/icons-material/Check';
import { FormCard, FormField } from '@shared/forms/FormComponents';
import type { Node } from 'reactflow';
import { ds } from 'src/utils/colors';
import CloseIcon from '@mui/icons-material/Close';
import k8sApi from '@api1/kubernetes';
import apiKubernetes1 from '@api1/kubernetes1';
import apiIntegrations from '@api1/integrations';
import homeApi from '@api1/home';
import recommendationApi from '@api1/recommendation';
import { formatRuleName } from '@components/optimise-new/utils';
import { titleCaseForAggregationKey } from 'src/utils/common';
import CopyButton from '@shared/buttons/CopyButton';
import { STRUCTURED_FILTER_FIELDS, buildFilterExpression, parseFilterExpression } from '../utils/eventFilter';
import { DOCS_BASE_URL, docsUrl } from '@lib/externalUrls';
import { validateCron } from '@utils/cron';
import ScheduleBuilder from './ScheduleBuilder';
import CloudProviderIcon from '@shared/icons/CloudIcon';

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

interface IntegrationConfigValue {
  name: string;
  value: string;
}

interface Integration {
  id: string;
  name: string;
  integration_config_values: IntegrationConfigValue[];
}

interface WebhookIntegrationInfo {
  token: string;
  integrationName: string;
}

interface TriggerConfigSidebarProps {
  open: boolean;
  selectedNode: Node | null;
  onClose: () => void;
  onSave: (nodeId: string, triggerConfig: { type: string; params: any }) => void;
  accountId?: string;
  workflowData: any;
  // Called when the user attempts to close the sidebar with unsaved edits. Parent
  // is expected to render a sibling Keep/Discard confirmation dialog and may pass
  // the in-flight `pendingConfig` back via the `pendingConfig` prop if the user cancels.
  onRequestCloseWithUnsaved?: (pendingConfig: { type: string; params: any }) => void;
  // In-flight trigger config to re-seed when the parent reopens the sidebar
  // after the user clicked Cancel on the close-confirmation dialog. Consumed once on
  // open and then cleared by parent.
  pendingConfig?: { type: string; params: any } | null;
  onPendingConfigConsumed?: () => void;
}

// Migrate legacy params.event_type (scalar or array) into a Jinja filter clause
// so old workflows can be edited via the structured-filter UI.
const legacyEventTypeToFilter = (rawEventType: unknown): string => {
  if (typeof rawEventType === 'string' && rawEventType !== '') {
    return `event.event_type == "${rawEventType.replace(/"/g, "'")}"`;
  }
  if (Array.isArray(rawEventType)) {
    const items = rawEventType.filter((v): v is string => typeof v === 'string' && v !== '');
    if (items.length > 0) {
      const sanitized = items.map((v) => v.replace(/"/g, "'"));
      return sanitized.map((v) => `event.event_type == "${v}"`).join(' or ');
    }
  }
  return '';
};

const mergeLegacyEventType = (rawEventType: unknown, existingFilter: string): string => {
  const legacy = legacyEventTypeToFilter(rawEventType);
  if (!legacy) {
    return existingFilter || '';
  }
  if (!existingFilter) {
    return `{{ ${legacy} }}`;
  }
  // Strip outer {{ }} from existing, AND with legacy clause, re-wrap.
  const inner = existingFilter
    .trim()
    .replace(/^\{\{\s*/, '')
    .replace(/\s*\}\}$/, '');
  return `{{ ${legacy} and ${inner} }}`;
};

const WORKFLOW_WEBHOOK_INTEGRATIONS_URL = '/accounts/account-form?cloudProvider=workflow_webhook';

const PRIORITY_OPTIONS = [
  { label: 'HIGH', value: 'HIGH' },
  { label: 'MEDIUM', value: 'MEDIUM' },
  { label: 'LOW', value: 'LOW' },
  { label: 'INFO', value: 'INFO' },
  { label: 'DEBUG', value: 'DEBUG' },
];

// Generic, source-agnostic example payload shown in the event-trigger helper.
// Keep in sync with runbook-server/services/service/model_event.go (Event struct).
const EVENT_PAYLOAD_SAMPLE = {
  event_type: 'KubePodCrashLooping',
  source: 'k8s-collector',
  cluster: 'prod-us-east-1',
  subject_namespace: 'payments',
  subject_name: 'checkout-api-7d9c',
  priority: 'HIGH',
  status: 'FIRING',
  labels: { team: 'payments' },
};

const EVENT_FILTER_EXAMPLE = '{{ event.event_type == "KubePodCrashLooping" and event.cluster == "prod-us-east-1" }}';

const EVENT_PAYLOAD_DOCS_URL = docsUrl('/docs/features/workflow-builder/#event-payload-schema');
const DOCS_HOSTNAME = (() => {
  try {
    return new URL(DOCS_BASE_URL).hostname;
  } catch {
    return 'docs';
  }
})();

const validateEventFilter = (filter: string, setError: (msg: string) => void): boolean => {
  if (!filter.trim()) {
    setError('');
    return true;
  }

  if (filter.includes('{{') && filter.includes('}}')) {
    const openBraces = (filter.match(/\{\{/g) || []).length;
    const closeBraces = (filter.match(/\}\}/g) || []).length;

    if (openBraces !== closeBraces) {
      setError('Filter expression has unmatched template braces {{ }}');
      return false;
    }
  } else if (filter.trim().length > 0) {
    setError('Filter expression should use template syntax like {{ event.property == "value" }}');
    return false;
  }

  setError('');
  return true;
};

const validateCatchupWindow = (window: string, setError: (msg: string) => void): boolean => {
  if (!window.trim()) {
    setError('');
    return true;
  }

  const durationRegex = /^\d+[smhd]$/;
  if (!durationRegex.test(window.trim())) {
    setError('Duration must be in Go format (e.g., "10m", "1h", "24h")');
    return false;
  }

  setError('');
  return true;
};

const validateIntegrationName = (name: string, setError: (msg: string) => void): boolean => {
  if (!name.trim()) {
    setError('Integration name is required for webhook triggers');
    return false;
  }

  const nameRegex = /^[a-zA-Z0-9._-]+$/;
  if (!nameRegex.test(name.trim())) {
    setError('Integration name should contain only letters, numbers, dots, hyphens, and underscores');
    return false;
  }

  setError('');
  return true;
};

const filterValuesToOptions = (filters: any[], filterType: string): { label: string; value: string }[] => {
  const result = filters.find((f: any) => f.filter_type === filterType);
  const values = result?.values || [];
  return values.map((item: any) => ({ label: item.value, value: item.value }));
};

const validateManualInputs = (inputs: string, setError: (msg: string) => void): boolean => {
  if (!inputs.trim()) {
    setError('');
    return true;
  }

  try {
    const parsed = JSON.parse(inputs);
    if (typeof parsed !== 'object' || Array.isArray(parsed)) {
      setError('Inputs must be a valid JSON object');
      return false;
    }
    setError('');
    return true;
  } catch (_error) {
    console.error(_error);
    setError('Invalid JSON format. Please provide a valid JSON object.');
    return false;
  }
};

const TriggerConfigSidebar: React.FC<TriggerConfigSidebarProps> = ({
  open,
  selectedNode,
  onClose,
  onSave,
  accountId,
  workflowData,
  onRequestCloseWithUnsaved,
  pendingConfig,
  onPendingConfigConsumed,
}) => {
  const [cronExpression, setCronExpression] = useState('');
  const [cronError, setCronError] = useState('');
  const [overlapPolicy, setOverlapPolicy] = useState('Skip');
  const [overlapPolicyError, setOverlapPolicyError] = useState('');
  const [catchupWindow, setCatchupWindow] = useState('60s');
  const [catchupWindowError, setCatchupWindowError] = useState('');
  const [integrationName, setIntegrationName] = useState('');
  const [integrationNameError, setIntegrationNameError] = useState('');
  const [eventFilter, setEventFilter] = useState('');
  const [eventFilterError, setEventFilterError] = useState('');
  const [aggregationKeyOptions, setAggregationKeyOptions] = useState<{ label: string; value: string }[]>([]);
  const [isLoadingEventTypes, setIsLoadingEventTypes] = useState(false);
  const [filterEventType, setFilterEventType] = useState('');
  const [filterNamespace, setFilterNamespace] = useState('');
  const [filterSource, setFilterSource] = useState('');
  const [filterPriority, setFilterPriority] = useState('');
  const [filterCluster, setFilterCluster] = useState('');
  const [isAdvancedFilter, setIsAdvancedFilter] = useState(false);
  const [namespaceOptions, setNamespaceOptions] = useState<{ label: string; value: string }[]>([]);
  const [sourceOptions, setSourceOptions] = useState<{ label: string; value: string }[]>([]);
  const [manualInputs, setManualInputs] = useState<string>('');
  const [manualInputsError, setManualInputsError] = useState('');
  const [webhookIntegrationInfo, setWebhookIntegrationInfo] = useState<WebhookIntegrationInfo | null>(null);
  const [isLoadingWebhookInfo, setIsLoadingWebhookInfo] = useState(false);
  const [webhookIntegrationOptions, setWebhookIntegrationOptions] = useState<{ label: string; value: string }[]>([]);
  const [isLoadingWebhookOptions, setIsLoadingWebhookOptions] = useState(false);
  const [webhookTokenByName, setWebhookTokenByName] = useState<Record<string, string>>({});
  const [webhookFilter, setWebhookFilter] = useState('');
  const [webhookFilterError, setWebhookFilterError] = useState('');

  // Optimization trigger state
  const [optimizationCategories, setOptimizationCategories] = useState<string[]>([]);
  const [optimizationRuleNames, setOptimizationRuleNames] = useState<string[]>([]);
  const [optimizationClusters, setOptimizationClusters] = useState<string[]>([]);
  const [k8sClusterOptions, setK8sClusterOptions] = useState<{ label: string; value: string }[]>([]);
  const [isLoadingK8sClusters, setIsLoadingK8sClusters] = useState(false);
  // Distinct (category, rule_name) pairs sourced from the tenant's actual optimization
  // recommendations, so the Rule Name dropdown always matches what Optimize Recommendations shows.
  const [optimizationRulePairs, setOptimizationRulePairs] = useState<{ category: string; rule_name: string }[]>([]);
  const [isLoadingOptimizationRules, setIsLoadingOptimizationRules] = useState(false);

  // Buffered trigger-config state. Edits go into `pendingTriggerConfig` instead
  // of straight to the parent; the header Save button flushes pending → parent and
  // refreshes `committedSnapshot`. Close-with-unsaved hands the in-flight buffer
  // up to the parent for the Keep/Discard confirmation modal.
  const [pendingTriggerConfig, setPendingTriggerConfig] = useState<{ type: string; params: any } | null>(null);
  const [committedSnapshot, setCommittedSnapshot] = useState<string>('');

  const commitTriggerConfig = useCallback((config: { type: string; params: any }) => {
    setPendingTriggerConfig(config);
  }, []);

  const triggerType = selectedNode?.data?.trigger?.type;
  const workflowId = workflowData?.id;

  const filterStateMap: Record<string, { value: string; setter: (v: string) => void }> = {
    event_type: { value: filterEventType, setter: setFilterEventType },
    namespace: { value: filterNamespace, setter: setFilterNamespace },
    source: { value: filterSource, setter: setFilterSource },
    priority: { value: filterPriority, setter: setFilterPriority },
    cluster: { value: filterCluster, setter: setFilterCluster },
  };

  const getFilterValues = (): Record<string, string> => {
    const values: Record<string, string> = {};
    for (const field of STRUCTURED_FILTER_FIELDS) {
      values[field.filterType] = filterStateMap[field.filterType].value;
    }
    return values;
  };

  const lastLoadedNodeIdRef = useRef<string | null>(null);

  const buildWorkflowInputDefaults = (): Record<string, any> => {
    const inputs = workflowData?.definition?.inputs;
    if (!Array.isArray(inputs)) return {};
    return inputs.reduce((acc: Record<string, any>, input: any) => {
      acc[input.id] = input.default;
      return acc;
    }, {});
  };

  useEffect(() => {
    // Adopt external selectedNode / workflowData updates into UI state only when
    // the form is clean. If the user has unsaved edits, parent prop churn (e.g.
    // an unrelated setNodes update that produced a new selectedNode reference)
    // should not clobber the in-flight buffer. Matches ActionDetailsSidebar.
    //
    // EXCEPTION: when the user clicks a different node in the canvas, the
    // selectedNode id itself changes. Honoring the dirty-gate there would leave
    // UI state from the previous node displayed against the new selectedNode,
    // and a subsequent Save would write the old buffer onto the new node. So
    // when the node id changes we always re-seed and discard the prior buffer.
    const nodeId = selectedNode?.id ?? null;
    const nodeIdChanged = nodeId !== lastLoadedNodeIdRef.current;
    const currentPendingStr = pendingTriggerConfig ? JSON.stringify(pendingTriggerConfig) : '';
    if (!nodeIdChanged && currentPendingStr && currentPendingStr !== committedSnapshot) {
      return;
    }
    lastLoadedNodeIdRef.current = nodeId;

    const params = selectedNode?.data?.trigger?.params || {};

    // Schedule trigger fields
    setCronExpression(params.cron || '');
    setOverlapPolicy(params.overlap_policy || 'Skip');
    setCatchupWindow(params.catchup_window || '60s');

    // Webhook trigger fields
    setIntegrationName(params.integration_name || '');
    // params.filter is shared with the event trigger; the webhook UI only
    // renders when triggerType === 'webhook' so the state can't bleed.
    setWebhookFilter(triggerType === 'webhook' ? params.filter || '' : '');

    // Event trigger fields — fold legacy params.event_type into the filter expression so it
    // surfaces inside the structured-filter UI alongside cluster/namespace/source/priority.
    const mergedFilter = mergeLegacyEventType(params.event_type, params.filter || '');
    setEventFilter(mergedFilter);

    // Parse existing filter expression into structured fields
    const parsed = parseFilterExpression(mergedFilter);
    setFilterEventType(parsed.event_type || '');
    setFilterNamespace(parsed.namespace || '');
    setFilterSource(parsed.source || '');
    setFilterPriority(parsed.priority || '');
    setFilterCluster(parsed.cluster || '');

    // If the filter has content that can't be fully represented by structured dropdowns, use advanced mode
    const existingFilter = mergedFilter.trim();
    if (existingFilter) {
      const reconstructed = buildFilterExpression(parsed).trim();
      setIsAdvancedFilter(existingFilter !== reconstructed);
    } else {
      setIsAdvancedFilter(false);
    }

    // Optimization trigger fields
    setOptimizationCategories(Array.isArray(params.categories) ? params.categories : []);
    setOptimizationRuleNames(Array.isArray(params.rule_names) ? params.rule_names : []);
    setOptimizationClusters(Array.isArray(params.clusters) ? params.clusters : []);

    // Manual trigger fields — prefer saved params.inputs; fall back to workflow
    // input schema defaults so a fresh manual trigger gets a useful template.
    // typeof null === 'object' and arrays are objects too, so reject both
    // explicitly even though validateManualInputs blocks them on the way in.
    if (params.inputs && typeof params.inputs === 'object' && !Array.isArray(params.inputs)) {
      setManualInputs(JSON.stringify(params.inputs, null, 2));
    } else {
      setManualInputs(JSON.stringify(buildWorkflowInputDefaults(), null, 2));
    }

    // Clear all errors. Cron is the exception: a stored expression can be
    // invalid (workflows saved before cron was validated), so surface it now
    // rather than waiting for the user to touch the field.
    setCronError(triggerType === 'schedule' && params.cron ? validateCron(params.cron).error || '' : '');
    setOverlapPolicyError('');
    setCatchupWindowError('');
    setIntegrationNameError('');
    setWebhookFilterError('');
    setEventFilterError('');
    setManualInputsError('');

    // Seed buffered trigger-config snapshot from the node's current trigger so
    // dirty-detection compares user edits against the persisted state, not against
    // a stale baseline from the previously-selected node.
    const baseConfig: { type: string; params: any } = {
      type: triggerType || '',
      params: { ...(selectedNode?.data?.trigger?.params || {}) },
    };
    setPendingTriggerConfig(baseConfig);
    setCommittedSnapshot(JSON.stringify(baseConfig));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedNode, workflowData]);

  // Re-seed the form with in-flight edits when the parent reopens the sidebar
  // after the user clicked Cancel on the close-confirmation dialog. Consumed once,
  // then parent clears it so subsequent renders do not overwrite live edits.
  useEffect(() => {
    if (open && pendingConfig) {
      setPendingTriggerConfig(pendingConfig);
      onPendingConfigConsumed?.();
    }
  }, [open, pendingConfig, onPendingConfigConsumed]);

  const isDirty = useMemo(() => {
    if (!pendingTriggerConfig) return false;
    return JSON.stringify(pendingTriggerConfig) !== committedSnapshot;
  }, [pendingTriggerConfig, committedSnapshot]);

  const handleSave = useCallback(() => {
    if (!selectedNode || !pendingTriggerConfig) return;
    // An invalid cron used to reach the API, which persisted the workflow and
    // only then failed registering the Temporal schedule (#34996). Stop it here
    // as well as server-side so the user gets the error inline.
    if (triggerType === 'schedule') {
      const { valid, error } = validateCron(cronExpression);
      if (!valid) {
        setCronError(error || 'Invalid cron expression');
        return;
      }
    }
    onSave(selectedNode.id, pendingTriggerConfig);
    setCommittedSnapshot(JSON.stringify(pendingTriggerConfig));
  }, [selectedNode, pendingTriggerConfig, onSave, triggerType, cronExpression]);

  const requestClose = useCallback(() => {
    if (!isDirty || !pendingTriggerConfig) {
      onClose();
      return;
    }
    // Don't call onClose() here — parent closes the sidebar without dropping the
    // selected node, so Cancel→reopen preserves the in-flight buffer.
    onRequestCloseWithUnsaved?.(pendingTriggerConfig);
  }, [isDirty, onClose, onRequestCloseWithUnsaved, pendingTriggerConfig]);

  const fetchEventFilterOptions = async () => {
    // Use the passed accountId prop first, then fall back to other sources
    const currentAccountId =
      accountId || selectedNode?.data?.accountId || selectedNode?.data?.account_id || localStorage.getItem('selectedAccountId');

    if (!currentAccountId || currentAccountId === 'demo') {
      setAggregationKeyOptions([]);
      setNamespaceOptions([]);
      setSourceOptions([]);
      return;
    }

    setIsLoadingEventTypes(true);
    const filterValuesPromise = k8sApi
      .getEventFilterValues({
        accountId: currentAccountId,
        filterTypes: ['namespace', 'source'],
      })
      .then((response: any) => {
        const filters = response?.data?.filters || [];
        setNamespaceOptions(filterValuesToOptions(filters, 'namespace'));
        setSourceOptions(filterValuesToOptions(filters, 'source'));
      })
      .catch((error) => {
        console.error('Failed to fetch event filter values:', error);
        setNamespaceOptions([]);
        setSourceOptions([]);
      });

    const eventRulesPromise = apiKubernetes1
      .getAllEventRuleNames({ accountId: currentAccountId })
      .then((response: any) => {
        const rows = response?.data?.event_rules || [];
        const seen = new Set<string>();
        const options: { label: string; value: string }[] = [];
        for (const row of rows) {
          const alert = typeof row?.alert === 'string' ? row.alert : '';
          if (!alert || seen.has(alert)) continue;
          seen.add(alert);
          options.push({ label: titleCaseForAggregationKey(alert), value: alert });
        }
        options.sort((a, b) => a.label.localeCompare(b.label));
        setAggregationKeyOptions(options);
      })
      .catch((error) => {
        console.error('Failed to fetch event rule names:', error);
        setAggregationKeyOptions([]);
      });

    try {
      await Promise.all([filterValuesPromise, eventRulesPromise]);
    } finally {
      setIsLoadingEventTypes(false);
    }
  };

  const fetchK8sClusterOptions = async () => {
    setIsLoadingK8sClusters(true);
    try {
      const accounts = await homeApi.getCloudAccounts('');
      const options = (accounts || [])
        .filter((a: any) => a.id !== 'demo')
        .map((a: any) => ({
          label: a.account_name,
          value: a.id,
          cloud_provider: a.cloud_provider,
          group: a.cloud_provider || 'Other',
        }));
      setK8sClusterOptions(options);
    } catch (error) {
      console.error('Failed to fetch K8s cluster options:', error);
      setK8sClusterOptions([]);
    } finally {
      setIsLoadingK8sClusters(false);
    }
  };

  const fetchOptimizationRuleOptions = async () => {
    setIsLoadingOptimizationRules(true);
    try {
      const pairs = await recommendationApi.getDistinctOptimizationRules();
      setOptimizationRulePairs(pairs || []);
    } catch (error) {
      console.error('Failed to fetch optimization rule options:', error);
      setOptimizationRulePairs([]);
    } finally {
      setIsLoadingOptimizationRules(false);
    }
  };

  useEffect(() => {
    if (open && triggerType === 'event') {
      fetchEventFilterOptions();
    }
    if (open && (triggerType === 'event' || triggerType === 'optimization')) {
      fetchK8sClusterOptions();
    }
    if (open && triggerType === 'optimization') {
      fetchOptimizationRuleOptions();
    }
  }, [open, triggerType, accountId]);

  const fetchWebhookIntegrationInfo = async () => {
    if (!workflowId) {
      setWebhookIntegrationInfo(null);
      return;
    }

    setIsLoadingWebhookInfo(true);
    try {
      const response: any = await apiIntegrations.getWebhookIntegrationByWorkflowId(workflowId);
      const integrations: Integration[] = response?.data?.data?.integrations || [];
      const matchingIntegration = integrations[0];

      if (matchingIntegration) {
        const token = matchingIntegration.integration_config_values?.find((v) => v.name === 'token')?.value || '';
        setWebhookIntegrationInfo({
          token,
          integrationName: matchingIntegration.name,
        });
      } else {
        setWebhookIntegrationInfo(null);
      }
    } catch (error) {
      console.error('Failed to fetch webhook integration info:', error);
      setWebhookIntegrationInfo(null);
    } finally {
      setIsLoadingWebhookInfo(false);
    }
  };

  useEffect(() => {
    if (open && triggerType === 'webhook' && workflowId) {
      fetchWebhookIntegrationInfo();
    }
  }, [open, triggerType, workflowId]);

  // Surface the webhook URL as soon as the user picks an integration from the
  // dropdown, not just for already-bound integrations on edit.
  useEffect(() => {
    if (triggerType !== 'webhook') return;
    if (!integrationName) {
      setWebhookIntegrationInfo(null);
      return;
    }
    const token = webhookTokenByName[integrationName];
    if (token) {
      setWebhookIntegrationInfo({ token, integrationName });
    }
  }, [integrationName, webhookTokenByName, triggerType]);

  const fetchWebhookIntegrationOptions = async () => {
    setIsLoadingWebhookOptions(true);
    try {
      const currentAccountId =
        accountId ||
        selectedNode?.data?.accountId ||
        selectedNode?.data?.account_id ||
        (typeof window !== 'undefined' ? localStorage.getItem('selectedAccountId') : null);
      // Server-side cloud_account_id pushdown — integrations_list now
      // restricts to integrations wired to this account via the
      // integrations_cloud_accounts join table. Skipped in demo mode.
      const scopedAccountId = currentAccountId && currentAccountId !== 'demo' ? currentAccountId : undefined;
      const response: any = await apiIntegrations.listIntegrations({
        type: 'workflow_webhook',
        status: 'enabled',
        limit: 100,
        offset: 0,
        cloudAccountId: scopedAccountId,
      });
      const rows = response?.data?.data?.integrations_list?.rows || [];
      // Cache token-by-name so the dropdown can render the webhook URL for
      // the currently selected integration in the trigger sidebar.
      const tokenMap: Record<string, string> = {};
      for (const row of rows) {
        const rawConfig = row?.integration_config_values;
        const configValues = Array.isArray(rawConfig) ? rawConfig : typeof rawConfig === 'string' ? JSON.parse(rawConfig || '[]') : [];
        const token = configValues.find((c: any) => c?.name === 'token')?.value || '';
        if (token) {
          tokenMap[row.name] = token;
        }
      }
      setWebhookTokenByName(tokenMap);
      const options = rows.map((row: any) => ({ label: row.name, value: row.name }));
      setWebhookIntegrationOptions(options);
    } catch (error) {
      console.error('Failed to fetch workflow_webhook integrations:', error);
      setWebhookIntegrationOptions([]);
    } finally {
      setIsLoadingWebhookOptions(false);
    }
  };

  useEffect(() => {
    if (open && triggerType === 'webhook') {
      fetchWebhookIntegrationOptions();
    }
  }, [open, triggerType, accountId]);

  // Rule Name options are derived from the tenant's real recommendation data (not a
  // hardcoded list that silently misses new rules like `unused_pvc`). When a category is
  // selected, scope the options to that category; otherwise show every distinct rule.
  // Memoized because this component re-renders on every keystroke of its text fields.
  const selectedOptimizationCategory = optimizationCategories[0] || '';
  const selectedOptimizationRule = optimizationRuleNames[0] || '';
  const optimizationRuleOptions = useMemo(() => {
    const seen = new Set<string>();
    const options = optimizationRulePairs
      .filter((pair) => !selectedOptimizationCategory || pair.category === selectedOptimizationCategory)
      .filter((pair) => {
        if (!pair.rule_name || seen.has(pair.rule_name)) {
          return false;
        }
        seen.add(pair.rule_name);
        return true;
      })
      .map((pair) => ({ label: formatRuleName(pair.rule_name), value: pair.rule_name }))
      .sort((a, b) => a.label.localeCompare(b.label));
    // Preserve an already-saved rule when editing a trigger, even if it isn't in the
    // freshly fetched/filtered set, so the select doesn't render blank.
    if (selectedOptimizationRule && !seen.has(selectedOptimizationRule)) {
      options.push({ label: formatRuleName(selectedOptimizationRule), value: selectedOptimizationRule });
    }
    return options;
  }, [optimizationRulePairs, selectedOptimizationCategory, selectedOptimizationRule]);

  if (!open || !selectedNode) {
    return null;
  }

  const renderWebhookInfoSection = () => {
    if (isLoadingWebhookInfo) {
      return (
        <Box sx={{ mt: 2, p: 2, textAlign: 'center' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400] }}>Loading webhook information...</Typography>
        </Box>
      );
    }

    if (webhookIntegrationInfo?.token) {
      const webhookUrl = `${typeof window !== 'undefined' ? window.location.origin : ''}/api/webhooks/workflow?token=${webhookIntegrationInfo.token}`;
      return (
        <Box
          sx={{
            mt: 2,
            p: 2,
            backgroundColor: ds.blue[100],
            borderRadius: ds.radius.sm,
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: ds.gray[700],
              mb: 1,
            }}
          >
            Webhook URL:
          </Typography>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'flex-start',
              gap: 1,
              backgroundColor: ds.background[100],
              padding: 'var(--ds-space-2) var(--ds-space-3)',
              borderRadius: 'var(--ds-radius-sm)',
              border: `1px solid ${ds.brand[200]}`,
              mb: 1,
            }}
          >
            <CopyButton text={webhookUrl} />
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: ds.gray[400],
                wordBreak: 'break-all',
                flex: 1,
              }}
            >
              {webhookUrl}
            </Typography>
          </Box>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[400] }}>
            Integration: <strong>{webhookIntegrationInfo.integrationName}</strong>
          </Typography>
        </Box>
      );
    }

    return null;
  };

  const renderEventInfoSection = () => {
    return (
      <Box
        data-testid='event-payload-helper'
        sx={{
          mt: 2,
          p: 2,
          backgroundColor: ds.blue[100],
          borderRadius: ds.radius.sm,
          border: `1px solid ${ds.gray[200]}`,
        }}
      >
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: ds.gray[700],
            mb: 1,
          }}
        >
          Event payload reference
        </Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            color: ds.gray[400],
            lineHeight: 1.5,
            mb: 1,
          }}
        >
          The matched event is available as <strong>Inputs.event.*</strong> in your tasks and as <strong>event.*</strong> in filter expressions. A
          trigger needs at least one Event Type selected, or a filter expression. Example payload:
        </Typography>
        <Box
          component='pre'
          sx={{
            margin: 0,
            mb: 1,
            padding: 'var(--ds-space-2) var(--ds-space-3)',
            backgroundColor: ds.background[100],
            border: `1px solid ${ds.brand[200]}`,
            borderRadius: 'var(--ds-radius-sm)',
            fontSize: 'var(--ds-text-caption)',
            lineHeight: 1.4,
            fontFamily: 'monospace',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            overflowX: 'auto',
            color: ds.gray[700],
          }}
        >
          {JSON.stringify(EVENT_PAYLOAD_SAMPLE, null, 2)}
        </Box>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            color: ds.gray[400],
            lineHeight: 1.5,
            mb: 1,
          }}
        >
          Filter example:{' '}
          <Box component='span' sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-caption)', wordBreak: 'break-all' }}>
            {EVENT_FILTER_EXAMPLE}
          </Box>
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>
          <a
            data-testid='event-payload-helper-docs-link'
            href={EVENT_PAYLOAD_DOCS_URL}
            target='_blank'
            rel='noopener noreferrer'
            style={{ textDecoration: 'none', color: ds.blue[500], fontWeight: 'var(--ds-font-weight-medium)' }}
          >
            Full schema → {DOCS_HOSTNAME}
          </a>
        </Typography>
      </Box>
    );
  };

  const updateScheduleTrigger = (updates: any) => {
    const triggerConfig = {
      type: triggerType,
      params: {
        ...(cronExpression?.trim() ? { cron: cronExpression.trim() } : {}),
        ...(overlapPolicy && overlapPolicy !== 'Skip' ? { overlap_policy: overlapPolicy } : {}),
        ...(catchupWindow && catchupWindow !== '60s' ? { catchup_window: catchupWindow } : {}),
        ...updates,
      },
    };
    commitTriggerConfig(triggerConfig);
  };

  const updateWebhookTrigger = (updates: any) => {
    const triggerConfig = {
      type: triggerType,
      params: {
        ...(integrationName?.trim() ? { integration_name: integrationName.trim() } : {}),
        ...(webhookFilter?.trim() ? { filter: webhookFilter.trim() } : {}),
        ...updates,
      },
    };
    commitTriggerConfig(triggerConfig);
  };

  const validateWebhookFilter = (filter: string): boolean => {
    if (!filter.trim()) {
      setWebhookFilterError('');
      return true;
    }
    if (filter.includes('{{') && filter.includes('}}')) {
      const openBraces = (filter.match(/\{\{/g) || []).length;
      const closeBraces = (filter.match(/\}\}/g) || []).length;
      if (openBraces !== closeBraces) {
        setWebhookFilterError('Filter expression has unmatched template braces {{ }}');
        return false;
      }
    } else {
      setWebhookFilterError('Filter expression should use template syntax like {{ webhook_payload.property == "value" }}');
      return false;
    }
    setWebhookFilterError('');
    return true;
  };

  const handleWebhookFilterChange = (value: string) => {
    const safeValue = value || '';
    setWebhookFilter(safeValue);
    if (webhookFilterError) {
      setWebhookFilterError('');
    }
    if (triggerType !== 'webhook') return;
    if (!validateWebhookFilter(safeValue)) return;
    const triggerConfig = {
      type: triggerType,
      params: {
        ...(integrationName?.trim() ? { integration_name: integrationName.trim() } : {}),
        ...(safeValue.trim() ? { filter: safeValue.trim() } : {}),
      },
    };
    commitTriggerConfig(triggerConfig);
  };

  const updateManualTrigger = (updates: any) => {
    const triggerConfig = {
      type: triggerType,
      params: {
        ...updates,
      },
    };
    commitTriggerConfig(triggerConfig);
  };

  const handleCronChange = (value: string) => {
    setCronExpression(value);
    const { valid, error } = validateCron(value);
    setCronError(valid ? '' : error || 'Invalid cron expression');
    if (triggerType === 'schedule') {
      updateScheduleTrigger({ cron: value.trim() });
    }
  };

  const AT_LEAST_ONE_FILTER_MSG = 'Set at least one filter (event type, cluster, namespace, source, priority, or advanced expression)';

  const saveEventTrigger = (filter: string): boolean => {
    const trimmedFilter = filter?.trim() || '';
    if (!trimmedFilter) {
      setEventFilterError(AT_LEAST_ONE_FILTER_MSG);
      return false;
    }
    setEventFilterError('');
    commitTriggerConfig({ type: 'event', params: { filter: trimmedFilter } });
    return true;
  };

  const handleEventFilterChange = (value: string) => {
    const safeValue = value || '';
    setEventFilter(safeValue);
    setEventFilterError('');

    if (triggerType === 'event' && validateEventFilter(safeValue, setEventFilterError)) {
      saveEventTrigger(safeValue);
    }
  };

  const handleStructuredFilterChange = (filterType: string, value: string) => {
    filterStateMap[filterType].setter(value);

    const newFilter = buildFilterExpression(getFilterValues(), { [filterType]: value });
    setEventFilter(newFilter);

    if (triggerType === 'event') {
      saveEventTrigger(newFilter);
    }
  };

  // Canonical recommendation categories, matching the Optimize Recommendations page
  // (CATEGORY_FILTER_OPTIONS / WIDGET_CATEGORIES). Values must match the DB `category`
  // verbatim — the backend matches the trigger's `categories` param against event.category.
  const OPTIMIZATION_CATEGORY_OPTIONS = [
    { label: 'Right Sizing', value: 'RightSizing' },
    { label: 'Infra Upgrade', value: 'InfraUpgrade' },
    { label: 'Config', value: 'Configuration' },
    { label: 'Spot Instance', value: 'K8sSpotRecommendation' },
  ];

  const updateOptimizationTrigger = (updates: Partial<{ categories: string[]; rule_names: string[]; clusters: string[] }>) => {
    const newCategories = updates.categories ?? optimizationCategories;
    const newRuleNames = updates.rule_names ?? optimizationRuleNames;
    const newClusters = updates.clusters ?? optimizationClusters;

    const params: Record<string, string[]> = {};
    if (newCategories.length > 0) {
      params.categories = newCategories;
    }
    if (newRuleNames.length > 0) {
      params.rule_names = newRuleNames;
    }
    if (newClusters.length > 0) {
      params.clusters = newClusters;
    }

    const triggerConfig = {
      type: 'optimization',
      params,
    };
    commitTriggerConfig(triggerConfig);
  };

  const renderOptimizationConfig = () => (
    <FormCard
      title='Optimization Trigger Configuration'
      description='Configure which optimization recommendations should trigger this automation'
      icon={null}
      number={1}
      columns={1}
    >
      <FormField
        label='Cluster'
        description='Filter by Kubernetes cluster (optional)'
        value={optimizationClusters[0] || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
          const value = e.target.value || '';
          const newClusters = value ? [value] : [];
          setOptimizationClusters(newClusters);
          updateOptimizationTrigger({ clusters: newClusters });
        }}
        placeholder='Select cluster'
        required={false}
        error=''
        fieldType='select'
        options={k8sClusterOptions}
        grouped
        groupIcon={renderAccountGroupIcon}
        onSelect={() => {}}
        customRender={null}
        limitTags={0}
        minWidth='100%'
        maxRows={1}
        minRows={1}
        maxLength={200}
        isOptionsLoading={isLoadingK8sClusters}
      />

      <FormField
        label='Category'
        description='Filter by recommendation category (optional)'
        value={optimizationCategories[0] || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
          const value = e.target.value || '';
          const newCategories = value ? [value] : [];
          setOptimizationCategories(newCategories);
          // Rule options are scoped to the selected category, so clear any stale rule
          // selection that no longer belongs to the newly chosen category.
          setOptimizationRuleNames([]);
          updateOptimizationTrigger({ categories: newCategories, rule_names: [] });
        }}
        placeholder='Select category'
        required={false}
        error=''
        fieldType='select'
        options={OPTIMIZATION_CATEGORY_OPTIONS}
        onSelect={() => {}}
        customRender={null}
        limitTags={0}
        minWidth='100%'
        maxRows={1}
        minRows={1}
        maxLength={200}
      />

      <FormField
        label='Rule Name'
        description='Filter by specific optimization rule (optional)'
        value={optimizationRuleNames[0] || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
          const value = e.target.value || '';
          const newRuleNames = value ? [value] : [];
          setOptimizationRuleNames(newRuleNames);
          updateOptimizationTrigger({ rule_names: newRuleNames });
        }}
        placeholder='Select rule name'
        required={false}
        error=''
        fieldType='select'
        options={optimizationRuleOptions}
        onSelect={() => {}}
        customRender={null}
        limitTags={0}
        minWidth='100%'
        maxRows={1}
        minRows={1}
        maxLength={200}
        isOptionsLoading={isLoadingOptimizationRules}
      />

      <Box
        sx={{
          mt: 2,
          p: 2,
          backgroundColor: ds.blue[100],
          borderRadius: ds.radius.sm,
          border: `1px solid ${ds.gray[200]}`,
        }}
      >
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: ds.gray[700],
            mb: 1,
          }}
        >
          How it works:
        </Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            color: ds.gray[400],
            lineHeight: 1.5,
          }}
          component='div'
        >
          • This workflow triggers when a new optimization recommendation is created
          <br />• Use the filters above to narrow which recommendations trigger this workflow
          <br />• The recommendation data is available in the workflow as{' '}
          <code style={{ backgroundColor: ds.background[100], padding: 'var(--ds-space-1) var(--ds-space-1)', borderRadius: 'var(--ds-radius-sm)' }}>
            event
          </code>
          <br />• Leave all filters empty to trigger on any recommendation
        </Typography>
      </Box>
    </FormCard>
  );

  const handleManualInputsChange = (value: string) => {
    setManualInputs(value);
    setManualInputsError('');

    if (triggerType === 'manual' && validateManualInputs(value, setManualInputsError)) {
      try {
        const inputsObject = value.trim() ? JSON.parse(value) : {};
        updateManualTrigger({ inputs: inputsObject });
      } catch (_error) {
        console.error(_error);
        updateManualTrigger({ inputs: {} });
      }
    }
  };

  return (
    <Box
      sx={{
        position: 'absolute',
        top: ds.space[6],
        right: ds.space[4],
        width: '580px',
        height: 'calc(100vh - 110px)',
        backgroundColor: 'white',
        zIndex: 150,
        border: '3px solid var(--ds-purple-300)',
        borderRadius: 'var(--ds-radius-xl)',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {/* Header */}
      <Box
        sx={{
          padding: 'var(--ds-space-4) var(--ds-space-4)',
          borderBottom: '1px solid var(--ds-brand-150)',
          borderTop: '1px solid var(--ds-brand-150)',
          borderRadius: 'var(--ds-radius-xl) var(--ds-radius-xl) 0 0',
          backgroundColor: ds.blue[100],
          display: 'flex',
          flexDirection: 'row',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
          Trigger Configuration - {triggerType?.charAt(0).toUpperCase() + triggerType?.slice(1)}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {isDirty && (
            <Button
              id='trigger-sidebar-save-btn'
              data-testid='trigger-sidebar-save-btn'
              tone='primary'
              size='sm'
              composition='text+icon'
              icon={<CheckIcon sx={{ fontSize: ds.text.title }} />}
              onClick={handleSave}
            >
              Save
            </Button>
          )}
          <Button
            id='wf-trigger-cfg-close-btn'
            composition='icon-only'
            tone='ghost'
            size='sm'
            aria-label='Close'
            icon={<CloseIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-600)' }} />}
            onClick={requestClose}
          />
        </Box>
      </Box>

      {/* Content */}
      <Box sx={{ flex: 1, overflowY: 'auto', padding: 'var(--ds-space-5)' }}>
        {triggerType === 'schedule' ? (
          <FormCard
            title='Schedule Configuration'
            description='Configure when this automation should run automatically'
            icon={null}
            number={1}
            columns={1}
          >
            <ScheduleBuilder value={cronExpression} onChange={handleCronChange} error={cronError} />

            <Box
              sx={{
                p: 0.5,
                backgroundColor: 'var(--ds-yellow-200)',
                borderRadius: ds.radius.sm,
                border: '1px solid var(--ds-amber-400)',
                display: 'flex',
                alignItems: 'flex-start',
                gap: 1,
              }}
            >
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-amber-700)',
                  lineHeight: 1.5,
                }}
              >
                <strong>Note:</strong> All scheduled times are calculated in UTC timezone. Please adjust your cron expression accordingly.
              </Typography>
            </Box>

            <FormField
              label='Overlap Policy'
              description='Behavior when a new schedule run is due while previous run is still active'
              value={overlapPolicy}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                const value = e.target.value || 'Skip';
                setOverlapPolicy(value);
                if (triggerType === 'schedule') {
                  updateScheduleTrigger({ overlap_policy: value });
                }
              }}
              placeholder='Skip'
              required={false}
              error={overlapPolicyError}
              fieldType='select'
              options={[
                { label: 'Skip (Default)', value: 'Skip' },
                { label: 'Buffer One', value: 'BufferOne' },
                { label: 'Buffer All', value: 'BufferAll' },
                { label: 'Allow All', value: 'AllowAll' },
                { label: 'Cancel Other', value: 'CancelOther' },
                { label: 'Terminate Other', value: 'TerminateOther' },
              ]}
              onSelect={() => {}}
              customRender={null}
              limitTags={0}
              minWidth='50%'
              maxRows={1}
              minRows={1}
              maxLength={100}
            />

            <FormField
              label='Catchup Window'
              description='Duration to look back for missed schedule runs after outage (Go format)'
              value={catchupWindow}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                const value = e.target.value || '60s';
                setCatchupWindow(value);
                if (validateCatchupWindow(value, setCatchupWindowError) && triggerType === 'schedule') {
                  updateScheduleTrigger({ catchup_window: value });
                }
              }}
              placeholder='60s'
              required={false}
              error={catchupWindowError}
              fieldType='textfield'
              maxRows={1}
              minRows={1}
              maxLength={50}
              onSelect={() => {}}
              customRender={null}
              limitTags={0}
              minWidth='50%'
            />
          </FormCard>
        ) : triggerType === 'webhook' ? (
          <FormCard title='Webhook Configuration' description='Configure webhook integration for this automation' icon={null} number={1} columns={1}>
            <FormField
              label='Webhook Integration'
              description='Pick an existing workflow webhook integration'
              value={integrationName}
              onChange={(e: any) => {
                const selected = e?.target?.value ?? '';
                setIntegrationName(selected);
                setIntegrationNameError('');
                if (selected && validateIntegrationName(selected, setIntegrationNameError)) {
                  updateWebhookTrigger({ integration_name: selected.trim() });
                }
              }}
              placeholder={isLoadingWebhookOptions ? 'Loading…' : 'Select a workflow webhook'}
              required={true}
              error={integrationNameError}
              fieldType='select'
              options={webhookIntegrationOptions}
              minWidth='50%'
              isOptionsLoading={isLoadingWebhookOptions}
            />

            {renderWebhookInfoSection()}

            <FormField
              label='Filter Expression (Optional)'
              description='Jinja2 expression evaluated against webhook_payload. Workflow runs only when the result is "true".'
              value={webhookFilter}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleWebhookFilterChange(e.target.value)}
              placeholder='{{ webhook_payload.action == "opened" }}'
              required={false}
              error={webhookFilterError}
              fieldType='textarea'
              maxRows={3}
              minRows={2}
              maxLength={500}
              onSelect={() => {}}
              customRender={null}
              limitTags={0}
              minWidth='50%'
            />

            <Box
              sx={{
                mt: 1,
                p: 1.5,
                backgroundColor: ds.blue[100],
                borderRadius: ds.radius.sm,
                border: `1px solid ${ds.gray[200]}`,
              }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], lineHeight: 1.5 }}>
                Need a new webhook? Create one in the{' '}
                <a
                  href={WORKFLOW_WEBHOOK_INTEGRATIONS_URL}
                  target='_blank'
                  rel='noopener noreferrer'
                  style={{ color: ds.blue[500], fontWeight: 'var(--ds-font-weight-medium)', textDecoration: 'none' }}
                >
                  Integrations tab → Workflow Webhook ↗
                </a>
              </Typography>
            </Box>
          </FormCard>
        ) : triggerType === 'event' ? (
          <FormCard
            title='Event Configuration'
            description='Configure which events should trigger this automation'
            icon={null}
            number={1}
            columns={1}
          >
            {!isAdvancedFilter ? (
              <>
                <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--ds-space-4)' }}>
                  <FormField
                    label='Event Type'
                    description='Aggregation key from the event rules catalog'
                    value={filterEventType}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleStructuredFilterChange('event_type', e.target.value || '')}
                    placeholder='Select event type'
                    required={false}
                    error={eventFilterError}
                    fieldType='select'
                    options={aggregationKeyOptions}
                    onSelect={() => {}}
                    customRender={null}
                    limitTags={0}
                    minWidth='100%'
                    maxRows={1}
                    minRows={1}
                    maxLength={200}
                    isOptionsLoading={isLoadingEventTypes}
                  />

                  <FormField
                    label='Cluster'
                    description='Kubernetes cluster'
                    value={filterCluster}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleStructuredFilterChange('cluster', e.target.value || '')}
                    placeholder='Select cluster'
                    required={false}
                    error=''
                    fieldType='select'
                    options={k8sClusterOptions}
                    grouped
                    groupIcon={renderAccountGroupIcon}
                    onSelect={() => {}}
                    customRender={null}
                    limitTags={0}
                    minWidth='100%'
                    maxRows={1}
                    minRows={1}
                    maxLength={200}
                    isOptionsLoading={isLoadingK8sClusters}
                  />

                  <FormField
                    label='Namespace'
                    description='Kubernetes namespace'
                    value={filterNamespace}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleStructuredFilterChange('namespace', e.target.value || '')}
                    placeholder='Select namespace'
                    required={false}
                    error=''
                    fieldType='select'
                    options={namespaceOptions}
                    onSelect={() => {}}
                    customRender={null}
                    limitTags={0}
                    minWidth='100%'
                    maxRows={1}
                    minRows={1}
                    maxLength={200}
                    isOptionsLoading={isLoadingEventTypes}
                  />

                  <FormField
                    label='Source'
                    description='Event source system'
                    value={filterSource}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleStructuredFilterChange('source', e.target.value || '')}
                    placeholder='Select source'
                    required={false}
                    error=''
                    fieldType='select'
                    options={sourceOptions}
                    onSelect={() => {}}
                    customRender={null}
                    limitTags={0}
                    minWidth='100%'
                    maxRows={1}
                    minRows={1}
                    maxLength={200}
                    isOptionsLoading={isLoadingEventTypes}
                  />

                  <FormField
                    label='Priority'
                    description='Severity level'
                    value={filterPriority}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleStructuredFilterChange('priority', e.target.value || '')}
                    placeholder='Select priority'
                    required={false}
                    error=''
                    fieldType='select'
                    options={PRIORITY_OPTIONS}
                    onSelect={() => {}}
                    customRender={null}
                    limitTags={0}
                    minWidth='100%'
                    maxRows={1}
                    minRows={1}
                    maxLength={200}
                  />
                </Box>

                {eventFilter && (
                  <Box
                    sx={{
                      mt: 1,
                      p: 1.5,
                      backgroundColor: ds.background[100],
                      borderRadius: ds.radius.sm,
                      border: `1px solid ${ds.brand[200]}`,
                    }}
                  >
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[400], fontFamily: 'monospace', wordBreak: 'break-all' }}>
                      {eventFilter}
                    </Typography>
                  </Box>
                )}
              </>
            ) : (
              <FormField
                label='Filter Expression'
                description='Use template syntax to filter events (e.g., {{ event.source == "integration-test" }})'
                value={eventFilter}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleEventFilterChange(e.target.value)}
                placeholder='{{ event.source == "my-source" }}'
                required={false}
                error={eventFilterError}
                fieldType='textarea'
                maxRows={3}
                minRows={2}
                maxLength={500}
                onSelect={() => {}}
                customRender={null}
                limitTags={0}
                minWidth='50%'
              />
            )}

            <Typography
              id='wf-trigger-cfg-filter-mode-toggle-btn'
              role='button'
              tabIndex={0}
              onClick={() => setIsAdvancedFilter(!isAdvancedFilter)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  setIsAdvancedFilter(!isAdvancedFilter);
                }
              }}
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: ds.blue[500],
                cursor: 'pointer',
                mt: 1,
                '&:hover': { textDecoration: 'underline' },
              }}
            >
              {isAdvancedFilter ? 'Switch to structured filters' : 'Switch to advanced filter expression'}
            </Typography>

            {renderEventInfoSection()}
          </FormCard>
        ) : triggerType === 'optimization' ? (
          renderOptimizationConfig()
        ) : triggerType === 'manual' ? (
          <FormCard
            title='Manual Trigger Configuration'
            description='Configure inputs that will be provided when manually triggering this automation'
            icon={null}
            number={1}
            columns={1}
          >
            <FormField
              label='Input Parameters (JSON)'
              description='JSON object containing parameters to pass when triggering the automation manually'
              value={manualInputs}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleManualInputsChange(e.target.value)}
              placeholder='{"param1": "value1", "param2": 42}'
              required={false}
              error={manualInputsError}
              fieldType='textarea'
              maxRows={8}
              minRows={3}
              maxLength={2000}
              onSelect={() => {}}
              customRender={null}
              limitTags={0}
              minWidth='50%'
            />

            <Box
              sx={{
                mt: 2,
                p: 2,
                backgroundColor: ds.blue[100],
                borderRadius: ds.radius.sm,
                border: `1px solid ${ds.gray[200]}`,
              }}
            >
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: ds.gray[700],
                  mb: 1,
                }}
              >
                Manual Trigger Configuration:
              </Typography>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  color: ds.gray[400],
                  lineHeight: 1.5,
                }}
                component='div'
              >
                • Define JSON inputs that will be passed to the workflow when triggered manually
                <br />• These inputs will be available to workflow tasks as variables
                <br />• Example:{' '}
                <code
                  style={{
                    backgroundColor: ds.background[100],
                    padding: 'var(--ds-space-1) var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                  }}
                >
                  {`{"param": "value", "count": 10}`}
                </code>
                <br />• Leave empty{' '}
                <code
                  style={{
                    backgroundColor: ds.background[100],
                    padding: 'var(--ds-space-1) var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                  }}
                >{`{}`}</code>{' '}
                for no inputs
              </Typography>
            </Box>
          </FormCard>
        ) : (
          <FormCard
            title={`${triggerType?.charAt(0).toUpperCase() + triggerType?.slice(1)} Trigger`}
            description='This trigger type does not require additional configuration'
            icon={null}
            number={1}
            columns={1}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: ds.gray[400],
                fontStyle: 'italic',
              }}
            >
              No additional parameters required for this trigger type.
            </Typography>
          </FormCard>
        )}
      </Box>
    </Box>
  );
};

export default TriggerConfigSidebar;
