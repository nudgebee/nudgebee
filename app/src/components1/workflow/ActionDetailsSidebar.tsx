import React, { Fragment, useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Box, Typography, Switch, Dialog, DialogContent, Chip, Tabs, Tab, Autocomplete, TextField, Alert } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Modal } from '@components1/ds/Modal';
import { PlayArrow, Timer, Storage, GridView, ErrorOutline, Close, AltRoute, Check } from '@mui/icons-material';
import { FormCard, FormField } from '@components1/common/NewReusabeFormComponents';
import { colors } from 'src/utils/colors';
import JsonTreeView from '@components1/common/JsonTreeView';
import type { Node } from 'reactflow';
import { DraggableOutputField } from './components/action-modal';
import { useTaskFormData } from './hooks/data-fetchers/useTaskFormData';
import { useTicketDynamicFields } from './hooks/data-fetchers/useTicketDynamicFields';
import { useOptionsSource } from './hooks/data-fetchers/useOptionsSource';
import { useSelectedNodeConfig } from './hooks/useSelectedNodeConfig';
import TemplateTextField, { TemplateSuggestion } from './components/TemplateTextField';
import PlatformFieldItem from './components/PlatformFieldItem';
import HybridField from './components/HybridField';
import AccountField from './components/AccountField';
import CallWorkflowFields from './components/CallWorkflowFields';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import { getPreviousTasksForNode, getSwitchChildNodeIds, getSwitchDryRunEligibility } from './utils/templateUtils';
import { parseDurationToSeconds, sanitizeTaskId } from './utils/taskUtils';
import apiWorkflow from '@api1/workflow';
import apiAccount from '@api1/account';
import { DurationField, TemplateExpressionField, FailurePolicyField, HooksField, KeyValueField, MatrixField } from './components/advanced-config';
import CollapsableCard from '@components1/ds/CollapsableCard';
import {
  TimestampPicker,
  JsonEditor,
  ArrayEditor,
  PasswordField,
  DurationInput,
  KeyValueHybridField,
  MultiSelectChips,
  CodeEditorWithLanguage,
  NestedSchemaEditor,
} from './components/WorkflowFieldComponents';
import { StableTextField, StableTextarea, StableNumberField } from './components/StableFormFields';
import { SlackIcon, MSTeamsIcon, GChatIcon, PostgresIcon, MySqlIcon, ClickhouseIcon, ouMssql, ouOracle } from '@assets';

// Icon mapping for notification providers
const PROVIDER_ICONS: Record<string, any> = {
  slack: SlackIcon,
  ms_teams: MSTeamsIcon,
  google_chat: GChatIcon,
};

// Built-in dynamic variables exposed to subject/body of the workflow email action.
// Mirrors the keys added to the Gonja context in runbook-server templating.go so the
// frontend picker offers exactly what the backend can resolve at render time.
const EMAIL_BUILTIN_VARIABLES: TemplateSuggestion[] = [
  { type: 'builtin', text: 'date', description: 'Current date — DDMMYYYY (e.g. 14042026)', insertText: '{{ date }}' },
  { type: 'builtin', text: 'date_iso', description: 'Current date — YYYY-MM-DD (e.g. 2026-04-14)', insertText: '{{ date_iso }}' },
  { type: 'builtin', text: 'date_us', description: 'Current date — MM/DD/YYYY (e.g. 04/14/2026)', insertText: '{{ date_us }}' },
  { type: 'builtin', text: 'time', description: 'Current time — HHMM (e.g. 1530)', insertText: '{{ time }}' },
  { type: 'builtin', text: 'time_hms', description: 'Current time — HH:MM:SS (e.g. 15:30:45)', insertText: '{{ time_hms }}' },
  { type: 'builtin', text: 'datetime', description: 'Date + time — DDMMYYYY_HHMM (e.g. 14042026_1530)', insertText: '{{ datetime }}' },
  { type: 'builtin', text: 'timestamp_iso', description: 'Full ISO 8601 timestamp', insertText: '{{ timestamp_iso }}' },
];

// Task names whose subject/body fields should expose the email built-in variable picker.
const EMAIL_TASK_NAMES = new Set(['notifications.email']);

// Tasks that don't support individual task execution (task trigger)
const TASKS_WITHOUT_INDIVIDUAL_RUN = new Set([
  'core.group',
  'core.switch',
  'core.foreach',
  'core.call-workflow',
  'ai.router',
  'core.approval',
  'core.wait',
  'ai.llm_event_investigate',
]);

// Tasks that don't support dry run (these skip execution during dry run)
const TASKS_WITHOUT_DRY_RUN = new Set(['k8s.vertical_rightsize', 'k8s.horizontal_rightsize', 'k8s.pv_rightsize']);

type DryRunEntry = { status: string; output: any; error?: string };

// Split a dry-run result's tasks into: the selected task's result, per-child-branch results (for
// switch tasks, keyed by original child id), and a map of other task outputs (previous tasks).
// Extracted from handleDryRunCurrentTask to keep its cognitive complexity under the SonarQube cap.
const splitDryRunResult = (
  result: any,
  selectedNodeId: string,
  selectedTaskConfigId: string | undefined,
  isSwitchTask: boolean,
  switchSanitizedId: string,
  switchChildTaskIds: string[]
): {
  current: DryRunEntry | null;
  children: Array<{ id: string } & DryRunEntry>;
  previous: Record<string, DryRunEntry>;
} => {
  const tasks: any[] = Array.isArray(result?.tasks) ? result.tasks : [];
  const isSelected = (taskId: string) => taskId === selectedTaskConfigId || taskId === selectedNodeId;

  const currentTask = tasks.find((t: any) => isSelected(t.id));
  const workflowFailed = result?.status === 'FAILED';
  let current: DryRunEntry | null;
  if (currentTask) {
    // If the workflow as a whole failed, surface the workflow error on the current task card
    // even when the task row itself came back as COMPLETED (e.g. the switch's output trace was
    // captured before the failure, or an inline task errored after writing partial output).
    const taskStatus = workflowFailed ? 'FAILED' : currentTask.status;
    const taskError = currentTask.error || (workflowFailed ? result?.error : undefined);
    current = { status: taskStatus, output: currentTask.output, error: taskError };
  } else if (isSwitchTask) {
    current = { status: result?.status || 'COMPLETED', output: null, error: result?.error };
  } else if (workflowFailed) {
    current = { status: 'FAILED', output: null, error: result?.error };
  } else {
    current = tasks.length > 0 ? { status: result?.status || 'UNKNOWN', output: result?.output, error: result?.error } : null;
  }

  const expectedChildRenamedIds = new Set(switchChildTaskIds.map((cid) => `${switchSanitizedId}-${cid}`));
  const previous: Record<string, DryRunEntry> = {};
  const childTraces: Record<string, any> = {};
  tasks.forEach((task: any) => {
    if (isSelected(task.id)) return;
    if (isSwitchTask && expectedChildRenamedIds.has(task.id)) {
      childTraces[task.id] = task;
      return;
    }
    previous[task.id] = { status: task.status, output: task.output, error: task.error };
  });

  const children: Array<{ id: string } & DryRunEntry> = [];
  if (isSwitchTask) {
    switchChildTaskIds.forEach((childId) => {
      const trace = childTraces[`${switchSanitizedId}-${childId}`];
      if (trace) children.push({ id: childId, status: trace.status, output: trace.output, error: trace.error });
    });
  }

  return { current, children, previous };
};

// Helper function to get dropdown options based on field schema and available data
const getDropdownOptionsForField = (
  fieldName: string,
  fieldSchema: any,
  data: {
    cloudAccounts: { label: string; value: string; cloud_provider?: string; account_type?: string }[];
    integrations: { label: string; value: string }[];
    notifications: { label: string; value: string }[];
    ticketConfigurations: { label: string; value: string; icon?: any }[];
    namespaces: { label: string; value: string }[];
    resourceTypes: { label: string; value: string }[];
    workloadKinds: { label: string; value: string }[];
    dbmsOptions: { label: string; value: string }[];
  }
): { label: string; value: string; icon?: any }[] => {
  if (fieldSchema.type === 'account') {
    return data.cloudAccounts;
  }
  if (fieldSchema.type === 'integration') {
    return data.integrations;
  }
  if (fieldSchema.type === 'notification') {
    return data.notifications;
  }
  if (fieldSchema.type === 'ticket') {
    return data.ticketConfigurations;
  }
  if (fieldName.toLowerCase() === 'namespace') {
    return data.namespaces;
  }
  if (fieldName.toLowerCase() === 'kind') {
    // Merge schema-declared kinds (e.g. Pod, Deployment, DaemonSet) with the
    // kinds actually present in the selected cluster. Using only the dynamic
    // list would hide the schema default ("Pod") when the cluster has no
    // standalone pods, which in turn trips HybridField's detectMode and
    // flips the field to Expression mode on first render.
    const schemaOptions = fieldSchema.enum || fieldSchema.options;
    const merged: { label: string; value: string }[] = [];
    const seen = new Set<string>();
    const addKind = (value: string) => {
      if (!value || seen.has(value)) return;
      seen.add(value);
      merged.push({
        label: value.charAt(0).toUpperCase() + value.slice(1).replace(/_/g, ' '),
        value,
      });
    };
    (data.workloadKinds || []).forEach((opt) => addKind(opt.value));
    if (Array.isArray(schemaOptions)) {
      schemaOptions.forEach((v: string) => addKind(v));
    }
    if (merged.length > 0) return merged;
    return data.resourceTypes;
  }
  if (fieldName === 'dbms_type') {
    return data.dbmsOptions;
  }
  if (fieldSchema.enum || fieldSchema.options) {
    const options = fieldSchema.enum || fieldSchema.options || [];
    return options.map((value: string) => {
      const option: { label: string; value: string; icon?: any } = {
        label: value.charAt(0).toUpperCase() + value.slice(1).replace(/_/g, ' '),
        value: value,
      };
      // Add icons for provider fields (notification providers)
      if (fieldName === 'provider' && PROVIDER_ICONS[value]) {
        option.icon = PROVIDER_ICONS[value];
      }
      return option;
    });
  }
  return [];
};

interface ActionDetailsSidebarProps {
  open: boolean;
  onClose: () => void;
  selectedActionType: string | null;
  nodes: Node[];
  edges?: any[];
  onTaskDataChange: (taskData: any) => void;
  onTaskConfigChange?: (field: string, value: any) => void;
  taskDefinitions: any[];
  taskData: any;
  validationErrors?: Record<string, string>;
  viewOnlyMode?: boolean;
  previousNodeOutputSchema?: any;
  accountId?: string;
  onRunPreviousSteps?: (taskId: string) => Promise<Record<string, any>>;
  onDryRunToTask?: (taskId: string) => Promise<any>;
  onRunTask?: (taskType: string, params: any) => Promise<any>;
  workflowInputs?: Array<{ id: string; type: string; description?: string; default?: any }>;
  workflowTimeout?: string;
  onToggleDisable?: (disable: boolean) => void;
  // Currently-edited workflow id. Used by the Call Workflow node editor to exclude
  // the current workflow from its picker (a workflow cannot recursively call itself).
  currentWorkflowId?: string;
  // Called when the user attempts to close the sidebar with unsaved edits. Parent
  // is expected to render a sibling Keep/Discard confirmation dialog and may pass
  // the in-flight pendingData back via the `pendingData` prop if the user cancels.
  onRequestCloseWithUnsaved?: (pendingData: any) => void;
  // In-flight edits to re-seed into the form on reopen (after user Cancelled the
  // close-confirmation dialog). Consumed once on open and then cleared by parent.
  pendingData?: any;
  onPendingDataConsumed?: () => void;
}

interface SchemaProperty {
  type: string;
  description?: string;
  required?: boolean;
  default?: any;
  enum?: string[];
  integration_type?: string;
  is_encrypted?: boolean;
  sub_type?: string;
  schema?: {
    properties?: Record<string, SchemaProperty>;
  };
  options?: string[];
  order?: number;
  title?: string;
  hidden?: boolean;
  depends_on?: string[];
  visible_when?: {
    field: string;
    value: string[];
  };
  required_when?: {
    field: string;
    value: string[];
  };
  options_source?: {
    type: string;
    dependency_mapping?: Record<string, string>;
  };
  dynamic_fields_source?: {
    type: string;
    dependency_mapping?: Record<string, string>;
  };
}

// Constants and configuration
const FIELD_TYPE_MAP: Record<string, string> = {
  account: 'dropdown',
  integration: 'dropdown',
  notification: 'dropdown',
  ticket: 'dropdown',
  resource_name: 'resource_dropdown',
  boolean: 'switch',
  number: 'number',
  integer: 'number',
  timestamp: 'timestamp',
  array: 'array',
  object: 'json',
  any: 'json',
  'map[string]string': 'keyvalue',
  'map[string]any': 'json',
};

// Fields that should use duration input
const DURATION_FIELD_PATTERNS = ['timeout', 'duration', 'interval', 'delay', 'wait'];

// Fields that should use key-value editor instead of JSON
const KEYVALUE_FIELD_PATTERNS = ['headers', 'env', 'environment', 'labels', 'annotations', 'tags', 'metadata'];

// Jinja (default engine) uses {{ }} for expressions and {% %} for statements;
// Go text/template only uses {{ }}. A string matching either is a template
// reference the backend resolves at runtime, not a user-entered literal.
const isTemplateString = (s: string): boolean => /\{\{|\{%/.test(s);

// Detect code language from field name or sub_type
const getCodeLanguage = (fieldName: string, subType?: string): 'bash' | 'javascript' | 'sql' | 'json' | 'jsonata' => {
  if (subType) {
    const subTypeLower = subType.toLowerCase();
    if (subTypeLower === 'javascript' || subTypeLower === 'js') {
      return 'javascript';
    }
    if (subTypeLower === 'sql') {
      return 'sql';
    }
    if (subTypeLower === 'jsonata') {
      return 'jsonata';
    }
    if (subTypeLower === 'json') {
      return 'json';
    }
  }
  const fieldLower = fieldName.toLowerCase();
  if (fieldLower.includes('query') || fieldLower.includes('sql')) {
    return 'sql';
  }
  if (fieldLower.includes('expression') || fieldLower.includes('jsonata')) {
    return 'jsonata';
  }
  if (fieldLower.includes('javascript') || fieldLower.includes('js')) {
    return 'javascript';
  }
  return 'bash';
};

const DBMS_OPTIONS = [
  { label: 'PostgreSQL', value: 'postgresql', icon: PostgresIcon },
  { label: 'MySQL', value: 'mysql', icon: MySqlIcon },
  { label: 'ClickHouse', value: 'clickhouse', icon: ClickhouseIcon },
  { label: 'SQL Server', value: 'mssql', icon: ouMssql },
  { label: 'Oracle', value: 'oracle', icon: ouOracle },
];

const DEFAULT_FORM_FIELD_PROPS = {
  limitTags: 0,
  minWidth: '',
  onSelect: () => {},
  customRender: null,
  rows: 1,
  maxRows: 1,
  minRows: 1,
  maxLength: 500,
};

const FIELD_PLACEHOLDERS: Record<string, string> = {
  script: "#!/bin/bash\necho 'Starting script execution...'\ncurl -X GET 'https://api.example.com/data'\necho 'Script completed successfully'",
  env: '{"API_KEY": "your-key", "ENV": "production"}',
  resources: '{\n  "cpu_request": "100m",\n  "cpu_limit": "500m",\n  "memory_request": "128Mi",\n  "memory_limit": "512Mi"\n}',
};

const TEXTAREA_FIELDS = ['script', 'command', 'expression', 'query'];

// Utility functions
const formatFieldLabel = (fieldName: string): string => {
  return fieldName.charAt(0).toUpperCase() + fieldName.slice(1).replace(/_/g, ' ');
};

// Status color mapping for dry-run results
const getStatusColor = (status: string) => {
  switch (status?.toUpperCase()) {
    case 'COMPLETED':
      return { bg: 'var(--ds-green-100)', text: 'var(--ds-green-700)', border: 'var(--ds-green-300)' };
    case 'FAILED':
      return { bg: 'var(--ds-red-200)', text: 'var(--ds-red-700)', border: 'var(--ds-red-300)' };
    case 'RUNNING':
    case 'IN_PROGRESS':
      return { bg: 'var(--ds-blue-200)', text: 'var(--ds-blue-700)', border: 'var(--ds-blue-300)' };
    case 'PENDING':
      return { bg: 'var(--ds-background-300)', text: 'var(--ds-brand-500)', border: 'var(--ds-brand-200)' };
    default:
      return { bg: 'var(--ds-background-300)', text: 'var(--ds-brand-500)', border: 'var(--ds-brand-200)' };
  }
};

/** Renders an Autocomplete option with a bold name and muted description line. */
const renderDescriptiveOption = (
  props: React.HTMLAttributes<HTMLLIElement>,
  option: string,
  options: Array<{ value: string; description?: string }>
) => {
  const found = options.find((o) => o.value === option);
  return (
    <li {...props} key={option}>
      <Box sx={{ py: 0.25 }}>
        <Typography
          component='span'
          sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body)', color: colors.text.primary, fontFamily: 'monospace' }}
        >
          {option}
        </Typography>
        {found?.description && (
          <Typography component='p' sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondary, mt: 0.25, lineHeight: 1.3 }}>
            {found.description}
          </Typography>
        )}
      </Box>
    </li>
  );
};

const ActionDetailsSidebar: React.FC<ActionDetailsSidebarProps> = ({
  open,
  onClose,
  selectedActionType,
  nodes = [],
  edges = [],
  taskDefinitions = [],
  taskData,
  onTaskDataChange,
  onTaskConfigChange,
  validationErrors = {},
  viewOnlyMode = false,
  previousNodeOutputSchema,
  accountId,
  onRunPreviousSteps: _onRunPreviousSteps,
  onDryRunToTask,
  onRunTask,
  workflowInputs = [],
  workflowTimeout,
  onToggleDisable,
  currentWorkflowId,
  onRequestCloseWithUnsaved,
  pendingData,
  onPendingDataConsumed,
}) => {
  const [localData, setLocalData] = useState(taskData || {});
  // Snapshot of the last data committed to the parent (via Save or external sync).
  // Used to detect "is this edit dirty vs. what's persisted in workflow state?" so
  // the Save button can tick out only when there's something to save and the close
  // confirmation can ask Keep/Discard only when changes exist.
  const [committedSnapshot, setCommittedSnapshot] = useState<string>(() => JSON.stringify(taskData || {}));

  // Workflow configs state
  const [workflowConfigs, setWorkflowConfigs] = useState<Array<{ key: string; value: string; type: string }>>([]);
  const [showAllConfigs, setShowAllConfigs] = useState(false);

  // Default provider (logs / metrics / traces) for the currently selected
  // account, shown as a read-only chip below the account dropdown for any
  // observability task whose schema declares `account_provider_type`. The
  // resolved kind is driven by the selected action type so the Traces action
  // shows the trace provider, Metrics shows the metric provider, etc.
  const [defaultProvider, setDefaultProvider] = useState<string>('');

  // Use ref to track the latest localData for stable callbacks
  const localDataRef = useRef(localData);
  localDataRef.current = localData;

  // Refs for stable access in callbacks without adding to dependency arrays
  const taskDefinitionsRef = useRef(taskDefinitions);
  taskDefinitionsRef.current = taskDefinitions;
  const selectedActionTypeRef = useRef(selectedActionType);
  selectedActionTypeRef.current = selectedActionType;

  // Task capability checks
  const supportsIndividualRun = selectedActionType ? !TASKS_WITHOUT_INDIVIDUAL_RUN.has(selectedActionType) : false;

  // Previous tasks dry-run state (left column)
  const [previousDryRunLoading, setPreviousDryRunLoading] = useState(false);
  const [previousDryRunResults, setPreviousDryRunResults] = useState<Record<string, any> | null>(null);
  const [previousDryRunError, setPreviousDryRunError] = useState<string>('');

  // Current task dry-run state (right column)
  const [currentDryRunLoading, setCurrentDryRunLoading] = useState(false);
  const [currentTaskResult, setCurrentTaskResult] = useState<{ status: string; output: any; error?: string } | null>(null);
  // Switch-only: dry-run results for direct children so the user sees which branch ran + its output.
  const [switchChildrenResults, setSwitchChildrenResults] = useState<Array<{ id: string; status: string; output: any; error?: string }>>([]);

  // Run task state (isolated task execution)
  const [runTaskLoading, setRunTaskLoading] = useState(false);
  const [runTaskResult, setRunTaskResult] = useState<{ status: string; output: any; error?: string } | null>(null);

  // Drag state
  const [isDraggingOver, setIsDraggingOver] = useState<string | null>(null);

  // Tab state for middle column
  const [activeTab, setActiveTab] = useState<'parameters' | 'condition' | 'settings'>('parameters');

  // State for IM notification channel dropdown (notifications.im task)
  const [imChannelOptions, setImChannelOptions] = useState<{ label: string; value: string }[]>([]);
  const [imTeamOptions, setImTeamOptions] = useState<{ label: string; value: string; channels?: { label: string; value: string }[] }[]>([]);
  const [imChannelLoading, setImChannelLoading] = useState(false);

  // State for DM notification user dropdown (notifications.dm task)
  const [dmUserOptions, setDmUserOptions] = useState<{ label: string; value: string }[]>([]);
  const [dmUserLoading, setDmUserLoading] = useState(false);

  // State for Slack join_channel channel dropdown (slack.join_channel task)
  const [slackJoinChannelOptions, setSlackJoinChannelOptions] = useState<{ label: string; value: string }[]>([]);
  const [slackJoinChannelLoading, setSlackJoinChannelLoading] = useState(false);

  // State for Approval task IM channel dropdown (core.approval task)
  const [approvalChannelOptions, setApprovalChannelOptions] = useState<{ label: string; value: string }[]>([]);
  const [approvalChannelLoading, setApprovalChannelLoading] = useState(false);

  // Time Range mode (Relative vs Absolute) for tasks exposing duration + start_time + end_time
  const [timeMode, setTimeMode] = useState<'relative' | 'absolute'>(() => {
    if (taskData?.duration) return 'relative';
    if (taskData?.start_time || taskData?.end_time) return 'absolute';
    return 'relative';
  });

  // Change mode (By percentage vs To absolute) for tasks exposing change_by + change_to
  const [changeMode, setChangeMode] = useState<'by' | 'to'>(() => {
    if (taskData?.change_to) return 'to';
    return 'by';
  });

  // Find the current task definition
  const currentTaskDefinition = taskDefinitions.find((def) => def.name === selectedActionType);

  // Use centralized hook for node config access
  const { selectedNode: hookSelectedNode, taskConfig } = useSelectedNodeConfig(nodes);

  // Confirmation state for disabling a task that has downstream dependents
  const [disableConfirmOpen, setDisableConfirmOpen] = useState(false);

  const directDependentsCount = useMemo(() => {
    if (!hookSelectedNode) return 0;
    return (edges || []).filter((e: any) => e.source === hookSelectedNode.id).length;
  }, [edges, hookSelectedNode]);

  const handleDisableToggle = (nextChecked: boolean) => {
    if (!onToggleDisable) return;
    if (nextChecked && directDependentsCount > 0) {
      setDisableConfirmOpen(true);
      return;
    }
    onToggleDisable(nextChecked);
  };

  // Use custom hook for API data
  const {
    cloudAccounts,
    integrations,
    namespaces,
    namespacesLoading,
    notifications,
    ticketConfigurations,
    resourceTypes,
    resourceNames,
    resourceNamesLoading,
    workloadKinds,
    workloadKindsLoading,
    hasResourceNameField,
  } = useTaskFormData(currentTaskDefinition, selectedActionType, localData);

  // Stable ref for cloudAccounts so handleDataChange can resolve the cloud
  // provider type for the selected account without adding cloudAccounts to its
  // dep array (which would break callback identity on every fetch).
  const cloudAccountsRef = useRef(cloudAccounts);
  cloudAccountsRef.current = cloudAccounts;

  // Ticket create task - dynamic field support
  const isTicketCreateTask = selectedActionType === 'tickets.create';
  // Any ticket.* task; opts the hook into resolving the tool type from the
  // selected integration so VisibleWhen-driven fields can branch on it.
  const isTicketTask = !!selectedActionType?.startsWith('tickets.');
  const {
    ticketProjects,
    ticketProjectsLoading,
    ticketIssueTypes,
    ticketIssueTypesLoading,
    ticketDynamicFields,
    ticketFieldOptions,
    ticketFieldOptionsLoading,
    ticketTool,
    ticketSeverityField,
    searchTicketField,
  } = useTicketDynamicFields({
    isTicketCreateTask,
    isTicketTask,
    integrationId: localData?.integration_id || '',
    projectKey: localData?.project_key || '',
    ticketType: localData?.ticket_type || '',
  });

  // Generic options_source data fetching (e.g., onboarded_users for email recipients)
  // Enrich formValues with accountId so fetchers like mcp_tools can access it
  const enrichedFormValues = useMemo(() => ({ ...localData, _accountId: accountId }), [localData, accountId]);
  const optionsSourceData = useOptionsSource(currentTaskDefinition, enrichedFormValues);

  // Get selected node and previous tasks - memoized to prevent unnecessary re-renders
  const selectedNode = useMemo(() => nodes.find((node) => node.selected && (node.type === 'action' || node.type === 'switch')), [nodes]);

  const previousTasks = useMemo(
    () => (selectedNode ? getPreviousTasksForNode(selectedNode.id, nodes, edges, taskDefinitions) : []),
    [selectedNode, nodes, edges, taskDefinitions]
  );

  // Switch tasks need direct children connected and no chained switch as a child to be dry-runnable.
  const switchDryRunStatus = useMemo(() => {
    if (selectedActionType !== 'core.switch' || !selectedNode) {
      return { allowed: true, reason: '' };
    }
    const { allowed, reason } = getSwitchDryRunEligibility(selectedNode.id, nodes, edges ?? []);
    return { allowed, reason };
  }, [selectedActionType, selectedNode, nodes, edges]);
  const isTaskDisabled = selectedNode?.data?.taskConfig?.disabled === true;
  const supportsDryRun =
    (selectedActionType ? !TASKS_WITHOUT_DRY_RUN.has(selectedActionType) : false) && switchDryRunStatus.allowed && !isTaskDisabled;

  useEffect(() => {
    const next = taskData || {};
    const nextStr = JSON.stringify(next);
    // Adopt external parent updates into BOTH buffers (snapshot + localData) only
    // when the form is clean. If the user has in-flight edits, leave both
    // unchanged — otherwise a parent push (including our own applyAutoDefault
    // round-tripping through onTaskDataChange) would absorb the in-flight edits
    // into the baseline and silently hide the Save button.
    if (JSON.stringify(localData) === committedSnapshot) {
      setCommittedSnapshot(nextStr);
      setLocalData(next);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskData]);

  // Re-seed the form with in-flight edits when the parent reopens the sidebar
  // after the user clicked Cancel on the close-confirmation dialog. Consumed once,
  // then parent clears it so subsequent renders do not overwrite live edits.
  useEffect(() => {
    if (open && pendingData) {
      setLocalData(pendingData);
      onPendingDataConsumed?.();
    }
  }, [open, pendingData, onPendingDataConsumed]);

  const isDirty = useMemo(() => {
    if (viewOnlyMode) return false;
    return JSON.stringify(localData) !== committedSnapshot;
  }, [localData, committedSnapshot, viewOnlyMode]);

  const handleSave = useCallback(() => {
    onTaskDataChange(localData);
    setCommittedSnapshot(JSON.stringify(localData));
  }, [localData, onTaskDataChange]);

  // Auto-populated changes (schema defaults, normalization, derived fields) are
  // not user edits — they should persist to parent silently and not flag the
  // form as dirty. Updates localData, pushes to parent, and resyncs the
  // committed snapshot so isDirty stays false on first load.
  // Also sync-writes localDataRef so sibling auto-default effects in the same
  // render tick build on the freshly merged object instead of clobbering each
  // other from the stale render-time ref value.
  const applyAutoDefault = useCallback(
    (updated: any) => {
      const prevLocalStr = JSON.stringify(localDataRef.current);
      localDataRef.current = updated;
      setLocalData(updated);
      // Only refresh the committed baseline when the form was clean — otherwise
      // a derived auto-default (ticket_tool mirror, provider backfill, etc.)
      // firing AFTER the user has typed would absorb the in-flight edits into
      // the baseline and silently hide the Save button.
      setCommittedSnapshot((prev) => (prevLocalStr === prev ? JSON.stringify(updated) : prev));
      onTaskDataChange(updated);
    },
    [onTaskDataChange]
  );

  const requestClose = useCallback(() => {
    if (viewOnlyMode || !isDirty) {
      onClose();
      return;
    }
    // Don't call onClose() here — parent closes the sidebar without deselecting
    // the node, so Cancel→reopen preserves the selected node and its taskData.
    onRequestCloseWithUnsaved?.(localData);
  }, [viewOnlyMode, isDirty, onClose, onRequestCloseWithUnsaved, localData]);

  // Resync Time Range mode when switching to a different action node
  useEffect(() => {
    if (taskData?.duration) setTimeMode('relative');
    else if (taskData?.start_time || taskData?.end_time) setTimeMode('absolute');
    else setTimeMode('relative');
    if (taskData?.change_to) setChangeMode('to');
    else setChangeMode('by');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedActionType]);

  // Commit schema defaults to localData on task selection so values are
  // included in the trigger payload even when the user never touches the
  // field. Covers locked (read_only) defaults like
  // kind=PersistentVolumeClaim on PV Rightsize as well as any other
  // primitive default (e.g. boolean toggles, enum selectors).
  useEffect(() => {
    const schema = taskDefinitions.find((td: any) => td.name === selectedActionType);
    const inputSchema = schema?.input_schema;
    if (!inputSchema) return;
    const current = localDataRef.current || {};
    const updates: Record<string, any> = {};
    for (const [fieldName, fs] of Object.entries(inputSchema)) {
      const f = fs as SchemaProperty;
      if (f.default === undefined || f.default === null || f.default === '') continue;
      if (current[fieldName] !== undefined && current[fieldName] !== '') continue;
      updates[fieldName] = f.default;
    }
    if (Object.keys(updates).length === 0) return;
    const updated = { ...current, ...updates };

    applyAutoDefault(updated);
  }, [selectedActionType, taskDefinitions, applyAutoDefault]);

  // Ensure duration default is committed when Time Range group defaults to 'relative' but taskData is missing duration
  useEffect(() => {
    const schema = taskDefinitions.find((td: any) => td.name === selectedActionType);
    const inputSchema = schema?.input_schema || {};
    const hasAll = inputSchema.duration && inputSchema.start_time && inputSchema.end_time;
    if (!hasAll || timeMode !== 'relative') return;
    const current = localDataRef.current || {};
    if (current.duration || current.start_time || current.end_time) return;
    const defaultD = ((inputSchema.duration as SchemaProperty)?.default as string) ?? '1h';
    const updated = { ...current, duration: defaultD };

    applyAutoDefault(updated);
  }, [selectedActionType, timeMode, taskDefinitions, applyAutoDefault]);

  // Normalize object-type fields that may have empty string values (e.g., headers: "" → {}).
  // Template strings like "{{workflow.secrets.mcp_headers}}" are preserved so the backend
  // can resolve them to a map at runtime via ProcessValue (runbook-server templating.go).
  useEffect(() => {
    const schema = taskDefinitions.find((td: any) => td.name === selectedActionType);
    if (!schema?.input_schema) {
      return;
    }

    let needsNormalize = false;
    const normalized = { ...localDataRef.current };

    for (const [key, fieldSchema] of Object.entries(schema.input_schema)) {
      const fs = fieldSchema as SchemaProperty;
      const isObjectType = fs.type === 'object' || fs.type === 'map[string]string' || fs.type === 'map[string]any';
      if (isObjectType && key in normalized && typeof normalized[key] === 'string' && !isTemplateString(normalized[key])) {
        normalized[key] = {};
        needsNormalize = true;
      }
    }

    if (needsNormalize) {
      applyAutoDefault(normalized);
    }
  }, [selectedActionType, taskDefinitions, applyAutoDefault]);

  // Prepopulate account_id from the workflow's accountId when the task schema
  // declares an account_id field of type "account" and the user hasn't picked
  // one yet. This keeps the logs task (and every other task with an account
  // dropdown) in sync with the account the workflow belongs to.
  useEffect(() => {
    if (!accountId || !open) return;
    const schema = taskDefinitions.find((td: any) => td.name === selectedActionType);
    if (!schema?.input_schema?.account_id) return;
    // Only prepopulate when the field is empty (don't overwrite user selection)
    if (localDataRef.current?.account_id) return;

    const updated = { ...localDataRef.current, account_id: accountId };
    applyAutoDefault(updated);
  }, [open, accountId, selectedActionType, taskDefinitions, applyAutoDefault]);

  // Resolve a cloud account ID to a normalized provider type token for use in
  // the synthetic `account_provider_type` form field. Tasks use this to drive
  // VisibleWhen on provider-specific fields (e.g. region/log_group for AWS,
  // log_analytics_workspace for Azure) without baking the provider into the
  // form schema directly. Returns "" when the account is not in the dropdown
  // (which clears the field downstream).
  const resolveAccountProviderType = useCallback((accountId: string | undefined): string => {
    if (!accountId) return '';
    const account = cloudAccountsRef.current.find((a) => a.value === accountId);
    if (!account) return '';
    if (account.account_type?.toLowerCase() === 'kubernetes') return 'k8s';
    switch (account.cloud_provider) {
      case 'AWS':
        return 'aws';
      case 'Azure':
        return 'azure';
      case 'GCP':
        return 'gcp';
      default:
        return '';
    }
  }, []);

  // Resolve the chip text shown below the account dropdown that tells the
  // user which log provider their query will actually hit. For cloud accounts
  // we route through the cloud-collector regardless of any default override
  // configured on the account, so the chip is hardcoded per cloud type to
  // match the actual routing in Execute. For k8s accounts the agent picks
  // the provider, so we delegate to the existing observability_get_default_provider RPC
  // action.
  // Resolve which observability provider kind (logs / metrics / traces) the
  // currently selected task is asking about. Drives both the backend
  // observability_get_default_provider call and the chip label so the Traces action shows
  // the trace provider, not the log provider. Falls back to 'logs' to preserve
  // existing behavior for any future task whose name doesn't match.
  const defaultProviderKind: 'logs' | 'metrics' | 'traces' = useMemo(() => {
    if (selectedActionType === 'observability.traces') return 'traces';
    if (selectedActionType === 'observability.metrics') return 'metrics';
    return 'logs';
  }, [selectedActionType]);

  useEffect(() => {
    const schema = taskDefinitionsRef.current.find((td: any) => td.name === selectedActionTypeRef.current);
    if (!schema?.input_schema?.account_provider_type) {
      setDefaultProvider('');
      return;
    }
    const accountIdValue = localData?.account_id;
    if (!accountIdValue) {
      setDefaultProvider('');
      return;
    }
    const providerType = resolveAccountProviderType(accountIdValue);
    // Cloud account types: hardcoded display per (kind, cloud) matches the
    // cloud-collector routing in logs_task.go / metrics_task.go. Traces have
    // no cloud-specific routing — the agent's TraceProvider feature decides,
    // so we always fall through to the backend resolver for traces.
    const cloudChipText: Record<'logs' | 'metrics', Record<string, string>> = {
      logs: {
        aws: 'AWS CloudWatch Logs',
        azure: 'Azure Log Analytics',
        gcp: 'GCP Cloud Logging',
      },
      metrics: {
        // Note: metrics_task.go routes Azure through the aws_cloudwatch provider
        // key internally, but the user-facing label still names the actual cloud
        // service to avoid misbranding.
        aws: 'AWS CloudWatch Metrics',
        azure: 'Azure Monitor Metrics',
      },
    };
    const kindChips = defaultProviderKind === 'traces' ? undefined : cloudChipText[defaultProviderKind];
    if (providerType && kindChips && kindChips[providerType]) {
      setDefaultProvider(kindChips[providerType]);
      return;
    }
    // k8s accounts (or unknown / traces on any account): ask services-server
    // to resolve via the agent's default provider feature for this kind.
    let cancelled = false;
    apiAccount
      .getDefaultProvider({ account_id: accountIdValue, provider_type: defaultProviderKind })
      .then((res: any) => {
        if (cancelled) return;
        const provider = res?.data?.data?.observability_get_default_provider?.provider || '';
        setDefaultProvider(provider);
      })
      .catch(() => {
        if (!cancelled) setDefaultProvider('');
      });
    return () => {
      cancelled = true;
    };
  }, [localData?.account_id, selectedActionType, resolveAccountProviderType, defaultProviderKind]);

  // Single effect that resolves and writes account_provider_type.
  // Combines back-fill (for saved workflows) and ES refinement (k8s → k8s_es
  // when default log provider is ES) into one place so there is exactly one
  // writer and no effect loops. The ES refinement is logs-only — for traces
  // / metrics, `defaultProvider` reflects a different kind and must not flip
  // the k8s account type.
  useEffect(() => {
    if (cloudAccounts.length === 0) return;
    const schema = taskDefinitionsRef.current.find((td: any) => td.name === selectedActionTypeRef.current);
    if (!schema?.input_schema?.account_provider_type) return;
    const accountId = localDataRef.current?.account_id;
    if (!accountId) return;
    let expected = resolveAccountProviderType(accountId);
    if (!expected) return;
    if (expected === 'k8s' && defaultProviderKind === 'logs' && defaultProvider?.toLowerCase() === 'es') {
      expected = 'k8s_es';
    }
    if (localDataRef.current?.account_provider_type === expected) return;

    const updated = { ...localDataRef.current, account_provider_type: expected };
    applyAutoDefault(updated);
  }, [cloudAccounts, selectedActionType, resolveAccountProviderType, defaultProvider, defaultProviderKind, applyAutoDefault]);

  // Mirror the resolved ticket tool (from useTicketDynamicFields) into the
  // synthetic `ticket_tool` form field so VisibleWhen-driven fields on ticket
  // tasks can branch on it. Handles both initial open (back-fill from saved
  // integration_id) and user changes. When the tool changes, clear any now-
  // hidden fields so stale values are not submitted.
  useEffect(() => {
    const schema = taskDefinitionsRef.current.find((td: any) => td.name === selectedActionTypeRef.current);
    if (!schema?.input_schema?.ticket_tool) return;
    const current = localDataRef.current?.ticket_tool || '';
    const next = ticketTool || '';
    if (current === next) return;
    // On reopen, integration_id is restored from saved data but useTicketDynamicFields
    // hasn't loaded the integration configs yet, so `ticketTool` is transiently ''.
    // Don't treat that as the user clearing the integration — otherwise we'd wipe
    // `ticket_tool` and every visible_when-gated field (project_key, …) before the
    // config fetch completes, permanently losing the saved values.
    const integrationId = localDataRef.current?.integration_id || '';
    if (integrationId && !next && current) return;

    const updated = { ...localDataRef.current };
    if (next) {
      updated.ticket_tool = next;
    } else {
      delete updated.ticket_tool;
    }
    // Drop any field now hidden by the new ticket_tool value.
    for (const [fieldName, fieldSchema] of Object.entries(schema.input_schema)) {
      const fs = fieldSchema as SchemaProperty;
      if (fs.visible_when?.field === 'ticket_tool') {
        const isVisible = next && fs.visible_when.value.includes(next);
        if (!isVisible && fieldName in updated) delete updated[fieldName];
      }
    }
    applyAutoDefault(updated);
  }, [ticketTool, selectedActionType, applyAutoDefault]);

  // Reset state when action type changes or modal closes
  useEffect(() => {
    if (!open || !selectedActionType) {
      setPreviousDryRunResults(null);
      setPreviousDryRunError('');
      setCurrentTaskResult(null);
      setRunTaskResult(null);
    }
  }, [open, selectedActionType]);

  // Fetch workflow configs when sidebar opens
  useEffect(() => {
    const fetchConfigs = async () => {
      if (!open || !accountId) {
        return;
      }
      try {
        const response: any = await apiWorkflow.listConfigs(accountId);
        if (response?.data?.config_list) {
          setWorkflowConfigs(
            response.data.config_list.map((config: any) => ({
              key: config.key,
              value: config.value,
              type: config.type || 'config',
            }))
          );
        }
      } catch (error) {
        console.error('Failed to fetch workflow configs:', error);
      }
    };
    fetchConfigs();
  }, [open, accountId]);

  // Helper to map MS Teams channels to dropdown options
  const mapMsTeamsChannels = useCallback(
    (channels: any[]) =>
      channels?.map((ch: any) => ({
        label: ch.name,
        value: ch.id,
      })) || [],
    []
  );

  const isImNotificationTask =
    selectedActionType === 'notifications.im' ||
    selectedActionType === 'notifications.add_reaction' ||
    selectedActionType === 'notifications.read_thread';

  useEffect(() => {
    if (!isImNotificationTask) {
      setImChannelOptions([]);
      setImTeamOptions([]);
      return;
    }

    const provider = localData?.provider;
    if (!provider) {
      setImChannelOptions([]);
      setImTeamOptions([]);
      return;
    }

    setImChannelLoading(true);
    apiAccount
      .getNotificationChannelList(provider)
      .then((res: any) => {
        const response = res?.data?.data || [];

        if (provider === 'ms_teams') {
          // MS Teams has nested team/channel structure
          const teams = response.map((item: any) => ({
            label: item.name,
            value: item.id,
            channels: mapMsTeamsChannels(item.channels),
          }));
          setImTeamOptions(teams);
          setImChannelOptions([]); // Channels populated when team selected
        } else {
          // Slack and Google Chat have flat channel list
          const channels = response.map((item: any) => ({
            label: item.name,
            value: item.id,
          }));
          setImChannelOptions(channels);
          setImTeamOptions([]);
        }
      })
      .catch((error) => {
        console.error('Failed to fetch IM channels:', error);
        setImChannelOptions([]);
        setImTeamOptions([]);
      })
      .finally(() => {
        setImChannelLoading(false);
      });
  }, [selectedActionType, localData?.provider, mapMsTeamsChannels]);

  // Update channel options when MS Teams team is selected
  useEffect(() => {
    if (!isImNotificationTask || localData?.provider !== 'ms_teams') {
      return;
    }

    const selectedTeamId = localData?.team_id;
    if (!selectedTeamId) {
      setImChannelOptions([]);
      return;
    }

    const selectedTeam = imTeamOptions.find((t) => t.value === selectedTeamId);
    if (selectedTeam?.channels) {
      setImChannelOptions(selectedTeam.channels);
    } else {
      setImChannelOptions([]);
    }
  }, [selectedActionType, localData?.provider, localData?.team_id, imTeamOptions]);

  // Check if this is a DM notification task
  const isDmNotificationTask = selectedActionType === 'notifications.dm';

  // Fetch users for DM notification task
  useEffect(() => {
    if (!isDmNotificationTask) {
      setDmUserOptions([]);
      return;
    }

    const provider = localData?.provider;
    if (!provider) {
      setDmUserOptions([]);
      return;
    }

    setDmUserLoading(true);
    apiAccount
      .getNotificationUserList(provider)
      .then((res: any) => {
        const response = res?.data?.data || [];
        const users = response.map((item: any) => ({
          label: item.display_name || item.real_name || item.name || item.id,
          value: item.id,
        }));
        setDmUserOptions(users);
      })
      .catch((error) => {
        console.error('Failed to fetch DM users:', error);
        setDmUserOptions([]);
      })
      .finally(() => {
        setDmUserLoading(false);
      });
  }, [selectedActionType, localData?.provider]);

  // Check if this is a Slack join_channel task
  const isSlackJoinChannelTask = selectedActionType === 'slack.join_channel';

  // Fetch channels for Slack join_channel task — provider is hardcoded to slack
  useEffect(() => {
    if (!isSlackJoinChannelTask) {
      setSlackJoinChannelOptions([]);
      return;
    }

    setSlackJoinChannelLoading(true);
    apiAccount
      .getNotificationChannelList('slack')
      .then((res: any) => {
        const response = res?.data?.data || [];
        const channels = response.map((item: any) => ({
          label: item.name,
          value: item.id,
        }));
        setSlackJoinChannelOptions(channels);
      })
      .catch((error) => {
        console.error('Failed to fetch Slack channels for join_channel:', error);
        setSlackJoinChannelOptions([]);
      })
      .finally(() => {
        setSlackJoinChannelLoading(false);
      });
  }, [selectedActionType]);

  // Check if this is an Approval task
  const isApprovalTask = selectedActionType === 'core.approval';

  // Fetch channels for Approval task IM notification
  useEffect(() => {
    if (!isApprovalTask) {
      setApprovalChannelOptions([]);
      return;
    }

    const provider = localData?.im_provider;
    if (!provider) {
      setApprovalChannelOptions([]);
      return;
    }

    setApprovalChannelLoading(true);
    apiAccount
      .getNotificationChannelList(provider)
      .then((res: any) => {
        const response = res?.data?.data || [];
        const channels = response.map((item: any) => ({
          label: item.name,
          value: item.id,
        }));
        setApprovalChannelOptions(channels);
      })
      .catch((error) => {
        console.error('Failed to fetch approval channels:', error);
        setApprovalChannelOptions([]);
      })
      .finally(() => {
        setApprovalChannelLoading(false);
      });
  }, [selectedActionType, localData?.im_provider]);

  // Clean up dependent field values when a controlling field changes
  const cleanupDependentFields = useCallback((data: Record<string, any>, changedField: string, newValue: any) => {
    const schema = taskDefinitionsRef.current.find((td: any) => td.name === selectedActionTypeRef.current);
    if (!schema?.input_schema) return;

    for (const [fieldName, fieldSchema] of Object.entries(schema.input_schema)) {
      const fs = fieldSchema as SchemaProperty;
      // Clear fields hidden by visible_when
      if (fs.visible_when && fs.visible_when.field === changedField) {
        const isNowVisible = newValue && fs.visible_when.value.includes(newValue);
        if (!isNowVisible && fieldName in data) delete data[fieldName];
      }
      // Clear always-visible fields that depend on the changed field
      if (fs.depends_on?.includes(changedField) && !fs.visible_when && fieldName in data) {
        delete data[fieldName];
      }
    }
  }, []);

  // When an account-type field changes, derive the synthetic
  // `account_provider_type` field from the picked cloud account so that
  // VisibleWhen-driven provider-specific fields know which branch to show.
  // Schema declares `account_provider_type` with `Hidden: true` so it never
  // renders. Also force-clears any field whose depends_on includes the account
  // field — switching scope (e.g. AWS account A -> AWS account B) must drop
  // the previous account's region/log_group even though they stay visible.
  const applyAccountFieldChange = useCallback(
    (data: Record<string, any>, schema: any, field: string, value: any) => {
      const providerType = resolveAccountProviderType(value);
      if (providerType) {
        data.account_provider_type = providerType;
      } else {
        delete data.account_provider_type;
      }
      cleanupDependentFields(data, 'account_provider_type', data.account_provider_type);
      for (const [depFieldName, depFieldSchema] of Object.entries(schema.input_schema)) {
        const dfs = depFieldSchema as SchemaProperty;
        if (dfs.depends_on?.includes(field) && depFieldName in data) {
          delete data[depFieldName];
        }
      }
    },
    [cleanupDependentFields, resolveAccountProviderType]
  );

  // Stable callback for handling data changes - uses ref to avoid stale closures
  const handleDataChange = useCallback(
    (field: string, value: any) => {
      const updatedData = { ...localDataRef.current };
      if (value === undefined || value === null) {
        delete updatedData[field];
      } else {
        updatedData[field] = value;
      }

      const schema = taskDefinitionsRef.current.find((td: any) => td.name === selectedActionTypeRef.current);
      const fieldSchema = schema?.input_schema?.[field];
      const isAccountFieldWithProviderType = fieldSchema?.type === 'account' && !!schema?.input_schema?.account_provider_type;
      if (isAccountFieldWithProviderType) {
        applyAccountFieldChange(updatedData, schema, field, value);
      }

      cleanupDependentFields(updatedData, field, value);
      setLocalData(updatedData);
    },
    [cleanupDependentFields, applyAccountFieldChange]
  );

  // Set a field value WITHOUT running depends_on cascade or provider
  // resolution. Used by AccountField when toggling Select/Expression: a saved
  // template like `{{ Configs.k8s_dev_account_id }}` resolved to the underlying
  // UUID is logically the same account, so dependent fields (namespace/kind/
  // name/command) must not be cleared.
  const setFieldValueNoCascade = useCallback((field: string, value: any) => {
    const updatedData = { ...localDataRef.current };
    if (value === undefined || value === null || value === '') {
      delete updatedData[field];
    } else {
      updatedData[field] = value;
    }
    setLocalData(updatedData);
  }, []);

  // Once the filtered cloudAccounts list arrives, drop any saved account-type
  // value that isn't in it. Catches the case where a task previously stored an
  // account_id from a different provider (e.g. an AWS account on a k8s task)
  // and the dropdown would otherwise render the raw UUID. Clearing the value
  // also cascades to dependent namespace/kind/name fields via depends_on.
  //
  // Skip the clear when the saved value is a templating expression (Jinja
  // {{ ... }} or {% ... %}) — those resolve at runtime to a UUID and the
  // user explicitly authored them. Without this guard, opening a workflow
  // whose account_id is "{{ Configs.k8s_dev_account_id }}" wipes the
  // template AND every field that depends on account_id (e.g. k8s.cli's
  // command), so the form shows up empty when re-edited.
  useEffect(() => {
    if (cloudAccounts.length === 0) return;
    const inputSchema = currentTaskDefinition?.input_schema;
    if (!inputSchema) return;
    const validValues = new Set(cloudAccounts.map((a) => a.value));
    const data = localDataRef.current || {};
    for (const [fieldName, fs] of Object.entries(inputSchema)) {
      if ((fs as any).type !== 'account') continue;
      const current = data[fieldName];
      if (typeof current === 'string' && (current.includes('{{') || current.includes('{%'))) continue;
      if (current && !validValues.has(current)) {
        handleDataChange(fieldName, '');
      }
    }
  }, [cloudAccounts, currentTaskDefinition?.input_schema, handleDataChange]);

  // Handle dry-run for previous tasks only (left column)
  const handleDryRunPreviousTasks = async () => {
    if (!selectedNode?.id || !onDryRunToTask || previousTasks.length === 0) {
      return;
    }

    setPreviousDryRunLoading(true);
    setPreviousDryRunError('');

    try {
      // Get the last previous task ID to run up to (not including current task)
      const lastPreviousTaskId = previousTasks[previousTasks.length - 1]?.id;
      if (!lastPreviousTaskId) {
        setPreviousDryRunError('No previous tasks to run');
        return;
      }

      const result = await onDryRunToTask(lastPreviousTaskId);

      if (result?.tasks && Array.isArray(result.tasks)) {
        const outputs: Record<string, any> = {};
        result.tasks.forEach((task: any) => {
          outputs[task.id] = {
            status: task.status,
            output: task.output,
            error: task.error,
          };
        });
        setPreviousDryRunResults(outputs);
      } else if (result?.error) {
        setPreviousDryRunError(result.error);
      }
    } catch (error: any) {
      console.error('Failed to dry-run previous tasks:', error);
      setPreviousDryRunError(error?.message || 'Failed to run previous tasks');
    } finally {
      setPreviousDryRunLoading(false);
    }
  };

  // Handle dry-run for current task (right column)
  const handleDryRunCurrentTask = async () => {
    if (!selectedNode?.id || !onDryRunToTask) {
      return;
    }

    setCurrentDryRunLoading(true);
    setCurrentTaskResult(null);
    setSwitchChildrenResults([]);

    // For switch tasks, collect the direct-child task IDs we asked the backend to include.
    // Children executed by the switch come back at the top level of `tasks` with renamed IDs
    // `{switchID}-{childID}` (see runbook-server executor.go:1142). The switch task itself is
    // not persisted in the execution trace, so we cannot show a "current task" result for it.
    const isSwitchTask = selectedNode.type === 'switch';
    const switchSanitizedId = isSwitchTask ? sanitizeTaskId(selectedNode.id) : '';
    const switchChildTaskIds = isSwitchTask ? getSwitchChildNodeIds(selectedNode.id, edges ?? []).map(sanitizeTaskId) : [];

    try {
      const result = await onDryRunToTask(selectedNode.id);

      if (result?.tasks && Array.isArray(result.tasks)) {
        const { current, children, previous } = splitDryRunResult(
          result,
          selectedNode.id,
          selectedNode.data?.taskConfig?.id,
          isSwitchTask,
          switchSanitizedId,
          switchChildTaskIds
        );
        if (current) setCurrentTaskResult(current);
        if (children.length > 0) setSwitchChildrenResults(children);
        if (Object.keys(previous).length > 0) setPreviousDryRunResults(previous);
      } else if (result?.error) {
        setCurrentTaskResult({ status: 'FAILED', output: null, error: result.error });
      }
    } catch (error: any) {
      console.error('Failed to dry-run current task:', error);
      setCurrentTaskResult({
        status: 'FAILED',
        output: null,
        error: error?.message || 'Failed to run current task',
      });
    } finally {
      setCurrentDryRunLoading(false);
    }
  };

  // Handle running the current task in isolation (real execution)
  const handleRunTask = async () => {
    if (!selectedActionType || !onRunTask) {
      return;
    }

    setRunTaskLoading(true);
    setRunTaskResult(null);

    try {
      // Use localData which contains the current task configuration
      const result = await onRunTask(selectedActionType, localData);

      if (result?.output !== undefined) {
        setRunTaskResult({
          status: result.status || 'COMPLETED',
          output: result.output,
          error: result.error,
        });
      } else if (result?.error) {
        setRunTaskResult({
          status: 'FAILED',
          output: null,
          error: result.error,
        });
      } else {
        // Successful execution with the full result
        setRunTaskResult({
          status: 'COMPLETED',
          output: result,
          error: undefined,
        });
      }
    } catch (error: any) {
      console.error('Failed to run task:', error);
      setRunTaskResult({
        status: 'FAILED',
        output: null,
        error: error?.message || 'Failed to run task',
      });
    } finally {
      setRunTaskLoading(false);
    }
  };

  // Handle drop on input field
  const handleDrop = (e: React.DragEvent, fieldName: string, currentValue: string) => {
    e.preventDefault();
    setIsDraggingOver(null);

    const template = e.dataTransfer.getData('text/plain');
    if (template) {
      // Append or replace based on current value
      const newValue = currentValue ? `${currentValue} ${template}` : template;
      handleDataChange(fieldName, newValue);
    }
  };

  const handleDragOver = (e: React.DragEvent, fieldName: string) => {
    e.preventDefault();
    setIsDraggingOver(fieldName);
  };

  const handleDragLeave = () => {
    setIsDraggingOver(null);
  };

  // Render schema output fields for a task (for dragging)
  const renderTaskOutputFields = (task: any, taskOutput?: any) => {
    const outputSchema = task.outputSchema || {};
    const hasOutput = taskOutput?.output;

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, ml: 1 }}>
        {Object.entries(outputSchema).map(([fieldName, fieldSchema]: [string, any]) => (
          <DraggableOutputField
            key={`${task.id}-${fieldName}`}
            taskId={task.id}
            taskName={task.name || task.id}
            fieldName={fieldName}
            fieldType={fieldSchema.type || 'any'}
            value={hasOutput ? taskOutput.output[fieldName] : undefined}
          />
        ))}
        {/* If no schema, show generic output field */}
        {Object.keys(outputSchema).length === 0 && (
          <DraggableOutputField
            taskId={task.id}
            taskName={task.name || task.id}
            fieldName='output'
            fieldType='any'
            value={hasOutput ? taskOutput.output : undefined}
          />
        )}
      </Box>
    );
  };

  // Render workflow inputs as draggable fields
  const renderWorkflowInputFields = () => {
    if (!workflowInputs || workflowInputs.length === 0) {
      return (
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontStyle: 'italic' }}>
          No automation inputs defined
        </Typography>
      );
    }

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
        {workflowInputs.map((input) => (
          <DraggableOutputField
            key={input.id}
            taskId=''
            taskName='Automation'
            fieldName={input.id}
            fieldType={input.type || 'string'}
            isInput={true}
            value={input.default}
            description={input.description}
          />
        ))}
      </Box>
    );
  };

  // Render workflow configs as draggable fields
  const renderWorkflowConfigFields = () => {
    if (!workflowConfigs || workflowConfigs.length === 0) {
      return (
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontStyle: 'italic' }}>
          No automation configs defined
        </Typography>
      );
    }

    const INITIAL_CONFIGS_COUNT = 3;
    const displayedConfigs = showAllConfigs ? workflowConfigs : workflowConfigs.slice(0, INITIAL_CONFIGS_COUNT);
    const hasMoreConfigs = workflowConfigs.length > INITIAL_CONFIGS_COUNT;
    const remainingCount = workflowConfigs.length - INITIAL_CONFIGS_COUNT;

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
        {displayedConfigs.map((config) => (
          <DraggableOutputField
            key={config.key}
            taskId=''
            taskName={config.type === 'secret' ? 'Secret' : 'Config'}
            fieldName={config.key}
            fieldType={config.type === 'secret' ? 'secret' : 'string'}
            isConfig={config.type !== 'secret'}
            isSecret={config.type === 'secret'}
            value={config.type === 'secret' ? '••••••' : config.value}
            description={config.type === 'secret' ? 'Encrypted secret value' : undefined}
          />
        ))}
        {hasMoreConfigs && (
          <Button id='action-sidebar-toggle-configs-btn' tone='ghost' size='xs' onClick={() => setShowAllConfigs(!showAllConfigs)}>
            {showAllConfigs ? 'Show less' : `Show ${remainingCount} more`}
          </Button>
        )}
      </Box>
    );
  };

  // ===================
  // LEFT COLUMN: Previous Actions Section
  // ===================
  const renderPreviousActionsColumn = () => {
    const hasPreviousTasks = previousTasks.length > 0;

    return (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
          borderRight: '1px solid var(--ds-brand-150)',
          overflow: 'hidden',
          bgcolor: 'var(--ds-background-200)',
        }}
      >
        {/* Header */}
        <Box
          sx={{
            px: 2,
            py: 1.5,
            borderBottom: '1px solid var(--ds-brand-150)',
            bgcolor: 'var(--ds-background-100)',
          }}
        >
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary, mb: 0.5 }}>
            Available Data
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark }}>Drag fields to use in configuration</Typography>
        </Box>

        {/* Dry Run Button */}
        <Box sx={{ px: 2, py: 1.5, borderBottom: '1px solid var(--ds-brand-150)', bgcolor: 'var(--ds-background-100)' }}>
          <Button
            id='action-sidebar-test-prev-actions-btn'
            tone='secondary'
            size='sm'
            fullWidth
            icon={<PlayArrow sx={{ fontSize: 16 }} />}
            loading={previousDryRunLoading}
            disabled={previousDryRunLoading || !hasPreviousTasks || viewOnlyMode}
            onClick={handleDryRunPreviousTasks}
          >
            {previousDryRunLoading ? 'Running...' : 'Test Previous Actions'}
          </Button>
          {previousDryRunError && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.errorText, mt: 0.5 }}>{previousDryRunError}</Typography>
          )}
        </Box>

        {/* Content - Full height scrollable area */}
        <Box
          sx={{
            flex: 1,
            overflow: 'auto',
            p: 2,
            display: 'flex',
            flexDirection: 'column',
            gap: 2,
          }}
        >
          {/* Workflow Inputs Section - Always show if inputs exist */}
          {workflowInputs && workflowInputs.length > 0 && (
            <Box
              sx={{
                p: 1.5,
                bgcolor: 'var(--ds-background-100)',
                border: '1px solid var(--ds-blue-200)',
                borderRadius: 1,
              }}
            >
              <Typography
                sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary, mb: 1 }}
              >
                Automation Inputs
              </Typography>
              {renderWorkflowInputFields()}
            </Box>
          )}

          {/* Workflow Configs Section - Always show if configs exist */}
          {workflowConfigs && workflowConfigs.length > 0 && (
            <Box
              sx={{
                p: 1.5,
                bgcolor: 'var(--ds-background-100)',
                border: '1px solid var(--ds-yellow-300)',
                borderRadius: 1,
              }}
            >
              <Typography
                sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary, mb: 1 }}
              >
                Automation Configs
              </Typography>
              {renderWorkflowConfigFields()}
            </Box>
          )}

          {/* Previous Tasks Section */}
          {hasPreviousTasks && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
              {previousTasks.map((task) => {
                const taskOutput = previousDryRunResults?.[task.id];
                const statusColors = taskOutput ? getStatusColor(taskOutput.status) : null;

                return (
                  <Box
                    key={task.id}
                    sx={{
                      p: 1.5,
                      bgcolor: 'var(--ds-background-100)',
                      border: '1px solid var(--ds-brand-150)',
                      borderRadius: 1,
                    }}
                  >
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        mb: 0.75,
                      }}
                    >
                      <Typography
                        sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}
                      >
                        {task.name || task.id}
                      </Typography>
                      {statusColors && (
                        <Chip
                          label={taskOutput.status}
                          size='small'
                          sx={{
                            height: 18,
                            fontSize: 'var(--ds-text-caption)',
                            fontWeight: 'var(--ds-font-weight-semibold)',
                            bgcolor: statusColors.bg,
                            color: statusColors.text,
                            border: `1px solid ${statusColors.border}`,
                          }}
                        />
                      )}
                    </Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark, mb: 1 }}>{task.type}</Typography>
                    {renderTaskOutputFields(task, taskOutput)}

                    {/* Show actual output after dry-run */}
                    {taskOutput?.output && (
                      <Box
                        sx={{
                          position: 'relative',
                          mt: 1.5,
                          p: 1.5,
                          bgcolor: 'var(--ds-green-100)',
                          border: '1px solid var(--ds-green-200)',
                          borderRadius: 1,
                          flex: 1,
                          minHeight: 0,
                          overflow: 'auto',
                        }}
                      >
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-caption)',
                            color: 'var(--ds-green-700)',
                            mb: 0.5,
                            fontWeight: 'var(--ds-font-weight-semibold)',
                          }}
                        >
                          Actual Output:
                        </Typography>
                        <JsonTreeView
                          data={taskOutput.output}
                          defaultExpanded={2}
                          maxHeight='none'
                          fontSize='10px'
                          bare
                          showCopy
                          templatePrefix={task.id ? `Tasks['${task.id}'].output` : undefined}
                        />
                      </Box>
                    )}

                    {taskOutput?.error && (
                      <Box
                        sx={{
                          mt: 1.5,
                          p: 1.5,
                          bgcolor: 'var(--ds-red-100)',
                          border: '1px solid var(--ds-red-200)',
                          borderRadius: 1,
                        }}
                      >
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-700)' }}>{taskOutput.error}</Typography>
                      </Box>
                    )}
                  </Box>
                );
              })}
            </Box>
          )}

          {/* Empty state - only show if no inputs, no configs and no previous tasks */}
          {(!workflowInputs || workflowInputs.length === 0) && (!workflowConfigs || workflowConfigs.length === 0) && !hasPreviousTasks && (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, fontStyle: 'italic' }}>
              No workflow inputs, configs or previous actions available
            </Typography>
          )}
        </Box>
      </Box>
    );
  };

  // ===================
  // RIGHT COLUMN: Test Current Action Section
  // ===================
  const renderTestCurrentActionColumn = () => {
    const dryRunStatusColors = currentTaskResult ? getStatusColor(currentTaskResult.status) : null;
    const runTaskStatusColors = runTaskResult ? getStatusColor(runTaskResult.status) : null;

    // Dry-run button helper text: prioritise specific disabled-reasons, else tailor copy per task type.
    let dryRunHelperText: string;
    if (isTaskDisabled) {
      dryRunHelperText = 'Dry run is not available for disabled tasks';
    } else if (!supportsDryRun) {
      dryRunHelperText = switchDryRunStatus.reason || 'Dry run is not supported for this task type';
    } else if (selectedActionType === 'core.switch') {
      dryRunHelperText = 'Simulates from start, through the switch and its direct children';
    } else {
      dryRunHelperText = 'Simulates automation from start to here';
    }

    // Helper to render result section
    const renderResultSection = (
      result: { status: string; output: any; error?: string } | null,
      statusColors: { bg: string; text: string; border: string } | null,
      title: string
    ) => {
      if (!result) {
        return null;
      }

      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
          {/* Section Title & Status */}
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
              {title}:
            </Typography>
            {statusColors && (
              <Chip
                label={result.status}
                size='small'
                sx={{
                  height: 18,
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  bgcolor: statusColors.bg,
                  color: statusColors.text,
                  border: `1px solid ${statusColors.border}`,
                }}
              />
            )}
          </Box>

          {/* Output */}
          {result.output && (
            <Box
              sx={{
                position: 'relative',
                p: 1.5,
                bgcolor: 'var(--ds-green-100)',
                border: '1px solid var(--ds-green-200)',
                borderRadius: 1,
                overflow: 'auto',
                maxHeight: '200px',
              }}
            >
              <Typography
                sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-green-700)', mb: 0.5, fontWeight: 'var(--ds-font-weight-semibold)' }}
              >
                Output:
              </Typography>
              <JsonTreeView
                data={result.output}
                defaultExpanded={2}
                maxHeight='none'
                fontSize='10px'
                bare
                showCopy
                templatePrefix={selectedNode?.id ? `Tasks['${selectedNode.data?.taskConfig?.id || selectedNode.id}'].output` : undefined}
              />
            </Box>
          )}

          {/* Error */}
          {result.error && (
            <Box
              sx={{
                p: 1.5,
                bgcolor: 'var(--ds-red-100)',
                border: '1px solid var(--ds-red-200)',
                borderRadius: 1,
                overflow: 'auto',
                maxHeight: '200px',
              }}
            >
              <Typography
                sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-700)', fontWeight: 'var(--ds-font-weight-semibold)', mb: 0.25 }}
              >
                Error:
              </Typography>
              <Typography
                component='pre'
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  fontFamily: 'monospace',
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                  color: 'var(--ds-red-700)',
                  m: 0,
                  lineHeight: 1.4,
                }}
              >
                {result.error}
              </Typography>
            </Box>
          )}
        </Box>
      );
    };

    return (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
          borderLeft: '1px solid var(--ds-brand-150)',
          overflow: 'hidden',
          bgcolor: 'var(--ds-background-200)',
        }}
      >
        {/* Header */}
        <Box
          sx={{
            px: 2,
            py: 1.5,
            borderBottom: '1px solid var(--ds-brand-150)',
            bgcolor: 'var(--ds-background-100)',
          }}
        >
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary, mb: 0.5 }}>
            Test Action
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark }}>
            Test this action with dry run or live execution
          </Typography>
        </Box>

        {/* Buttons Section */}
        <Box
          sx={{
            px: 2,
            py: 1.5,
            borderBottom: '1px solid var(--ds-brand-150)',
            bgcolor: 'var(--ds-background-100)',
            display: 'flex',
            flexDirection: 'column',
            gap: 1,
          }}
        >
          {/* Dry Run Button */}
          <Box>
            <Button
              tone='secondary'
              size='md'
              fullWidth
              icon={<PlayArrow sx={{ fontSize: 16 }} />}
              loading={currentDryRunLoading}
              disabled={currentDryRunLoading || runTaskLoading || viewOnlyMode || !onDryRunToTask || !supportsDryRun}
              onClick={handleDryRunCurrentTask}
            >
              {currentDryRunLoading ? 'Running...' : 'Dry Run'}
            </Button>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: !supportsDryRun ? colors.errorText : colors.text.secondaryDark,
                mt: 0.5,
                textAlign: 'center',
              }}
            >
              {dryRunHelperText}
            </Typography>
          </Box>

          {/* Run Task Button */}
          <Box>
            <Button
              tone='primary'
              size='md'
              fullWidth
              icon={<PlayArrow sx={{ fontSize: 16 }} />}
              loading={runTaskLoading}
              disabled={runTaskLoading || currentDryRunLoading || viewOnlyMode || !onRunTask || !supportsIndividualRun}
              onClick={handleRunTask}
            >
              {runTaskLoading ? 'Executing...' : 'Run Task'}
            </Button>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: !supportsIndividualRun ? colors.errorText : colors.text.secondaryDark,
                mt: 0.5,
                textAlign: 'center',
              }}
            >
              {!supportsIndividualRun ? 'Individual task execution is not supported for this task type' : 'Executes only this task in isolation'}
            </Typography>
          </Box>
        </Box>

        {/* Content - Full height scrollable area */}
        <Box
          sx={{
            flex: 1,
            overflow: 'auto',
            p: 2,
            display: 'flex',
            flexDirection: 'column',
            gap: 2,
          }}
        >
          {/* Dry Run Result */}
          {renderResultSection(currentTaskResult, dryRunStatusColors, 'Dry Run Result')}

          {/* Switch children dry-run results — surfaced here so the user sees which branch ran + its output. */}
          {switchChildrenResults.map((child) => (
            <Fragment key={child.id}>
              {renderResultSection(
                { status: child.status, output: child.output, error: child.error },
                getStatusColor(child.status),
                `Branch: ${child.id}`
              )}
            </Fragment>
          ))}

          {/* Run Task Result */}
          {renderResultSection(runTaskResult, runTaskStatusColors, 'Run Task Result')}

          {/* Empty state */}
          {!currentTaskResult && !runTaskResult && switchChildrenResults.length === 0 && (
            <Box
              sx={{
                flex: 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: colors.text.secondaryDark,
              }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-small)', fontStyle: 'italic', textAlign: 'center' }}>
                Use &quot;Dry Run&quot; to simulate the workflow
                <br />
                or &quot;Run Task&quot; to execute in isolation
              </Typography>
            </Box>
          )}
        </Box>
      </Box>
    );
  };

  // Dynamic form generator based on schema
  const renderDynamicForm = (schema: any, _title: string, description: string) => {
    if (!schema?.input_schema) {
      return (
        <Box>
          <FormCard title={'Input'} description={description || 'No schema available for this task type'} icon={null} number={1} columns={1}>
            <FormField
              label='Raw Configuration'
              description='Edit task configuration manually as JSON'
              value={JSON.stringify(localData, null, 2)}
              onChange={(e: any) => {
                try {
                  const parsed = JSON.parse(e.target.value);
                  setLocalData(parsed);
                } catch (error) {
                  console.error('Invalid JSON:', error);
                }
              }}
              placeholder='{}'
              disabled={viewOnlyMode}
              fieldType='textarea'
              rows={8}
              maxRows={12}
              minRows={6}
              maxLength={500000}
              limitTags={0}
              minWidth=''
              onSelect={() => {}}
              customRender={null}
            />
          </FormCard>
        </Box>
      );
    }

    const inputSchema = schema.input_schema;
    const requiredFields = new Set(Object.keys(inputSchema).filter((key) => inputSchema[key].required));
    const isSwitchTask = selectedActionType === 'core.switch';

    const renderField = (fieldName: string, fieldSchema: SchemaProperty) => {
      let isRequired = requiredFields.has(fieldName);
      if (!isRequired && fieldSchema.required_when) {
        const { field, value } = fieldSchema.required_when;
        const currentValue = localData?.[field] ?? inputSchema[field]?.default;
        if (currentValue !== undefined && currentValue !== null && value.includes(currentValue)) {
          isRequired = true;
        }
      }
      const isReadOnly = !!(fieldSchema as any).read_only;
      const isObjectType = fieldSchema.type === 'object' || fieldSchema.type === 'map[string]string' || fieldSchema.type === 'map[string]any';
      const defaultFallback = isObjectType ? {} : '';
      const rawValue = localData[fieldName] ?? fieldSchema.default ?? defaultFallback;
      // Coerce string values to empty objects for object-type fields (e.g., headers: "" → {}).
      // Template strings are passed through as-is so the KeyValueHybridField below can detect
      // expression mode and render a TemplateTextField instead of an empty KeyValueEditor.
      let fieldValue = rawValue;
      if (isObjectType && typeof rawValue === 'string') {
        if (isTemplateString(rawValue)) {
          fieldValue = rawValue;
        } else if (rawValue.trim()) {
          try {
            fieldValue = JSON.parse(rawValue);
          } catch {
            fieldValue = {};
          }
        } else {
          fieldValue = {};
        }
      }
      const isDropTarget = isDraggingOver === fieldName;

      // Generic visible_when: hide fields when the condition is not met.
      // Runs before any task-specific rendering so per-task branches (e.g. approval
      // IM dropdowns) never render fields that should be hidden by the schema.
      if (fieldSchema.visible_when) {
        const { field, value } = fieldSchema.visible_when;
        const controllingFieldSchema = currentTaskDefinition?.input_schema?.[field] as SchemaProperty | undefined;
        const currentValue = localData?.[field] ?? controllingFieldSchema?.default;
        if (currentValue === undefined || currentValue === null || !value.includes(currentValue)) {
          return null;
        }
      }

      // Special handling for Slack join_channel task — dynamic channel dropdown.
      // HybridField auto-detects whether the saved value is a channel id (Select
      // mode, shows channel name) or a {{ }} template (Expression mode), so the
      // field renders consistently when the task is closed and reopened.
      if (isSlackJoinChannelTask && fieldName === 'channel_id') {
        const getChannelPlaceholder = () => {
          if (slackJoinChannelLoading) return 'Loading channels...';
          if (slackJoinChannelOptions.length === 0) return 'No channels available';
          return 'Select channel';
        };

        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
              <HybridField
                fieldName={fieldName}
                value={fieldValue}
                onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                placeholder={getChannelPlaceholder()}
                disabled={isReadOnly || viewOnlyMode}
                error={validationErrors[fieldName] || ''}
                required={isRequired}
                options={slackJoinChannelOptions}
                optionsLoading={slackJoinChannelLoading}
                previousTasks={previousTasks}
                workflowInputs={workflowInputs}
                workflowConfigs={workflowConfigs}
                onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                isDropTarget={isDraggingOver === fieldName}
              />
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      // Special handling for IM notification tasks - dynamic channel dropdown
      if (isImNotificationTask) {
        // Handle channel/channel_id field as dynamic dropdown based on provider
        if (fieldName === 'channel' || fieldName === 'channel_id') {
          const provider = localData?.provider;
          const isTeamsWithoutTeam = provider === 'ms_teams' && !localData?.team_id;

          const getChannelPlaceholder = () => {
            if (!provider) return 'Select a provider first';
            if (isTeamsWithoutTeam) return 'Select a team first';
            if (imChannelLoading) return 'Loading channels...';
            if (imChannelOptions.length === 0) return 'No channels available';
            return 'Select channel';
          };

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                  placeholder={getChannelPlaceholder()}
                  disabled={isReadOnly || viewOnlyMode || !provider || isTeamsWithoutTeam}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={imChannelOptions}
                  optionsLoading={imChannelLoading}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDraggingOver === fieldName}
                />
                {fieldSchema.description && (
                  <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                    {fieldSchema.description}
                  </Typography>
                )}
              </Box>
            </Box>
          );
        }

        // Handle team_id field for MS Teams (only show when provider is ms_teams)
        if (fieldName === 'team_id') {
          const provider = localData?.provider;
          if (provider !== 'ms_teams') {
            return null; // Hide team_id field for non-MS Teams providers
          }

          const getTeamPlaceholder = () => {
            if (imChannelLoading) return 'Loading teams...';
            if (imTeamOptions.length === 0) return 'No teams available';
            return 'Select team';
          };

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                Team<span style={{ color: colors.border.error }}> *</span>
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => {
                    // Batch update: clear channel and set team_id in single state update

                    const updatedData = { ...localDataRef.current, channel: '', [fieldName]: newValue };
                    setLocalData(updatedData);
                  }}
                  placeholder={getTeamPlaceholder()}
                  disabled={isReadOnly || viewOnlyMode || imChannelLoading}
                  error={validationErrors[fieldName] || ''}
                  required={true}
                  options={imTeamOptions.map((t) => ({ label: t.label, value: t.value }))}
                  optionsLoading={imChannelLoading}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDraggingOver === fieldName}
                />
                <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                  MS Teams Team
                </Typography>
              </Box>
            </Box>
          );
        }

        // Handle provider field - clear dependent fields when changed
        if (fieldName === 'provider') {
          const options = getDropdownOptionsForField(fieldName, fieldSchema, {
            cloudAccounts,
            integrations,
            notifications,
            ticketConfigurations,
            namespaces,
            resourceTypes,
            workloadKinds,
            dbmsOptions: DBMS_OPTIONS,
          });

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <FormField
                  {...DEFAULT_FORM_FIELD_PROPS}
                  description={fieldSchema.description || ''}
                  value={fieldValue}
                  onChange={(e: any) => {
                    const newProvider = e.target.value;
                    // Batch update: clear team_id, channel and set provider in single state update

                    const updatedData = { ...localDataRef.current, team_id: '', channel: '', [fieldName]: newProvider };
                    setLocalData(updatedData);
                  }}
                  placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                  disabled={isReadOnly || viewOnlyMode}
                  error={validationErrors[fieldName] || ''}
                  fieldType='dropdown'
                  options={options as any}
                  required={isRequired}
                  minWidth='100%'
                  maxLength={0}
                />
              </Box>
            </Box>
          );
        }
      }

      // Special handling for DM notification task - dynamic user dropdown
      if (isDmNotificationTask) {
        // Handle user_id field as dynamic dropdown based on provider
        if (fieldName === 'user_id') {
          const provider = localData?.provider;

          const getUserPlaceholder = () => {
            if (!provider) return 'Select a provider first';
            if (dmUserLoading) return 'Loading users...';
            if (dmUserOptions.length === 0) return 'No users available';
            return 'Select user';
          };

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                  placeholder={getUserPlaceholder()}
                  disabled={isReadOnly || viewOnlyMode || !provider}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={dmUserOptions}
                  optionsLoading={dmUserLoading}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDraggingOver === fieldName}
                />
                {fieldSchema.description && (
                  <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                    {fieldSchema.description}
                  </Typography>
                )}
              </Box>
            </Box>
          );
        }

        // Handle team_id field for MS Teams in DM task (only show when provider is ms_teams)
        if (fieldName === 'team_id') {
          const provider = localData?.provider;
          if (provider !== 'ms_teams') {
            return null; // Hide team_id field for non-MS Teams providers
          }
        }

        // Handle provider field - clear user_id when changed
        if (fieldName === 'provider') {
          const options = getDropdownOptionsForField(fieldName, fieldSchema, {
            cloudAccounts,
            integrations,
            notifications,
            ticketConfigurations,
            namespaces,
            resourceTypes,
            workloadKinds,
            dbmsOptions: DBMS_OPTIONS,
          });

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <FormField
                  {...DEFAULT_FORM_FIELD_PROPS}
                  description={fieldSchema.description || ''}
                  value={fieldValue}
                  onChange={(e: any) => {
                    const newProvider = e.target.value;
                    // Batch update: clear user_id and set provider

                    const updatedData = { ...localDataRef.current, user_id: '', [fieldName]: newProvider };
                    setLocalData(updatedData);
                  }}
                  placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                  disabled={isReadOnly || viewOnlyMode}
                  error={validationErrors[fieldName] || ''}
                  fieldType='dropdown'
                  options={options as any}
                  required={isRequired}
                  minWidth='100%'
                  maxLength={0}
                />
              </Box>
            </Box>
          );
        }
      }

      // Special handling for Approval task - dynamic channel dropdown for IM
      if (isApprovalTask) {
        // Handle im_channel field as dynamic dropdown based on im_provider
        if (fieldName === 'im_channel') {
          const provider = localData?.im_provider;

          const getChannelPlaceholder = () => {
            if (!provider) {
              return 'Select a provider first';
            }
            if (approvalChannelLoading) {
              return 'Loading channels...';
            }
            if (approvalChannelOptions.length === 0) {
              return 'No channels available';
            }
            return 'Select channel';
          };

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                  placeholder={getChannelPlaceholder()}
                  disabled={isReadOnly || viewOnlyMode || !provider}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={approvalChannelOptions}
                  optionsLoading={approvalChannelLoading}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDraggingOver === fieldName}
                />
                {fieldSchema.description && (
                  <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                    {fieldSchema.description}
                  </Typography>
                )}
              </Box>
            </Box>
          );
        }

        // Handle im_team_id - hide for non-MS Teams providers (only Slack for now)
        if (fieldName === 'im_team_id') {
          const provider = localData?.im_provider;
          if (provider !== 'ms_teams') {
            return null;
          }
        }

        // Handle im_provider field - clear im_channel when changed
        if (fieldName === 'im_provider') {
          const options = getDropdownOptionsForField(fieldName, fieldSchema, {
            cloudAccounts,
            integrations,
            notifications,
            ticketConfigurations,
            namespaces,
            resourceTypes,
            workloadKinds,
            dbmsOptions: DBMS_OPTIONS,
          });

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <FormField
                  {...DEFAULT_FORM_FIELD_PROPS}
                  description={fieldSchema.description || ''}
                  value={fieldValue}
                  onChange={(e: any) => {
                    const newProvider = e.target.value;
                    // Batch update: clear im_channel and im_team_id when provider changes

                    const updatedData = { ...localDataRef.current, im_channel: '', im_team_id: '', [fieldName]: newProvider };
                    setLocalData(updatedData);
                  }}
                  placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                  disabled={isReadOnly || viewOnlyMode}
                  error={validationErrors[fieldName] || ''}
                  fieldType='dropdown'
                  options={options as any}
                  required={isRequired}
                  minWidth='100%'
                  maxLength={0}
                />
              </Box>
            </Box>
          );
        }
      }

      // Special handling for ticket create task - dynamic cascading dropdowns
      if (isTicketCreateTask) {
        // Handle integration_id field - cascade clear dependent fields
        if (fieldName === 'integration_id') {
          const options = getDropdownOptionsForField(fieldName, fieldSchema, {
            cloudAccounts,
            integrations,
            notifications,
            ticketConfigurations,
            namespaces,
            resourceTypes,
            workloadKinds,
            dbmsOptions: DBMS_OPTIONS,
          });

          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => {
                    // Cascade clear: project_key, ticket_type, severity, additional_fields

                    const updatedData = {
                      ...localDataRef.current,
                      project_key: '',
                      ticket_type: '',
                      severity: '',
                      additional_fields: undefined,
                      [fieldName]: newValue,
                    };
                    setLocalData(updatedData);
                  }}
                  placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                  disabled={isReadOnly || viewOnlyMode}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={options}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDraggingOver === fieldName}
                />
              </Box>
            </Box>
          );
        }

        // Handle project_key field - render as HybridField with dynamic options
        if (fieldName === 'project_key') {
          const dependenciesMet = !!localData?.integration_id;
          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => {
                    // Cascade clear: ticket_type, severity, additional_fields

                    const updatedData = {
                      ...localDataRef.current,
                      ticket_type: '',
                      severity: '',
                      additional_fields: undefined,
                      [fieldName]: newValue,
                    };
                    setLocalData(updatedData);
                  }}
                  placeholder={!dependenciesMet ? 'Select an integration first' : 'Select project'}
                  disabled={isReadOnly || viewOnlyMode || !dependenciesMet}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={ticketProjects}
                  optionsLoading={ticketProjectsLoading}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDropTarget}
                />
              </Box>
            </Box>
          );
        }

        // Handle ticket_type field - render as HybridField with dynamic options
        if (fieldName === 'ticket_type') {
          const dependenciesMet = !!localData?.integration_id && !!localData?.project_key;
          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => {
                    // Cascade clear: severity, additional_fields

                    const updatedData = {
                      ...localDataRef.current,
                      severity: '',
                      additional_fields: undefined,
                      [fieldName]: newValue,
                    };
                    setLocalData(updatedData);
                  }}
                  placeholder={!dependenciesMet ? 'Select a project first' : 'Select issue type'}
                  disabled={isReadOnly || viewOnlyMode || !dependenciesMet}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={ticketIssueTypes}
                  optionsLoading={ticketIssueTypesLoading}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDropTarget}
                />
              </Box>
            </Box>
          );
        }

        // Handle severity field - render with dynamic options from ticket metadata
        if (fieldName === 'severity') {
          const dependenciesMet = !!localData?.integration_id && !!localData?.project_key && !!localData?.ticket_type;
          // Once create-meta has resolved, hide Severity for tools that have no
          // severity source (e.g. GitHub/GitLab) instead of showing an empty
          // dropdown. While metadata is still loading we keep the field visible.
          const fieldsResolved = Object.keys(ticketDynamicFields).length > 0;
          if (dependenciesMet && fieldsResolved && !ticketSeverityField) {
            return null;
          }
          const severityOptions = ticketFieldOptions['priority'] || [];
          // Label the field with the platform's own term (Jira "Priority",
          // PagerDuty/ZenDuty/ServiceNow "Urgency") when known.
          const severityLabel = ticketSeverityField?.name || fieldSchema.title || formatFieldLabel(fieldName);
          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {severityLabel}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                <HybridField
                  fieldName={fieldName}
                  value={fieldValue}
                  onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                  placeholder={!dependenciesMet ? 'Select issue type first' : `Select ${severityLabel.toLowerCase()}`}
                  disabled={isReadOnly || viewOnlyMode || !dependenciesMet}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  options={severityOptions}
                  optionsLoading={ticketFieldOptionsLoading['priority'] || false}
                  previousTasks={previousTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                  onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                  onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                  isDropTarget={isDropTarget}
                />
              </Box>
            </Box>
          );
        }

        // Hide additional_fields from standard rendering - it's rendered separately as dynamic fields
        if (fieldName === 'additional_fields') {
          return null;
        }
      }

      // Non-create ticket tasks (add_comment, update, assign, transition, get,
      // get_comments) render project_key with HybridField so users get a real
      // dropdown affordance instead of the generic freeSolo Autocomplete that
      // looks like a plain text input.
      if (isTicketTask && !isTicketCreateTask && fieldName === 'project_key' && optionsSourceData[fieldName]) {
        const projectsSource = optionsSourceData[fieldName];
        const dependenciesMet = !!localData?.integration_id;
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
              <HybridField
                fieldName={fieldName}
                value={fieldValue}
                onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                placeholder={!dependenciesMet ? 'Select an integration first' : 'Select project'}
                disabled={isReadOnly || viewOnlyMode || !dependenciesMet}
                error={validationErrors[fieldName] || ''}
                required={isRequired}
                options={projectsSource.options}
                optionsLoading={projectsSource.loading}
                previousTasks={previousTasks}
                workflowInputs={workflowInputs}
                workflowConfigs={workflowConfigs}
                onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                isDropTarget={isDraggingOver === fieldName}
              />
            </Box>
          </Box>
        );
      }

      // Wrapper for droppable fields

      // Generic options_source rendering (e.g., onboarded_users for email recipients)
      const sourceData = optionsSourceData[fieldName];
      if (sourceData) {
        const normalizedValue = fieldValue ? [fieldValue] : [];
        const currentValue = Array.isArray(fieldValue) ? fieldValue : normalizedValue;
        const isArray = fieldSchema.type === 'array';
        const optionValues = sourceData.options.map((o) => o.value);
        const getOptionLabel = (option: string) => {
          const found = sourceData.options.find((o) => o.value === option);
          return found ? found.label : option;
        };
        const placeholder = sourceData.loading ? 'Loading...' : `Select or type ${fieldName.replace(/_/g, ' ')} and press Enter`;

        // Rich option rendering for sources that provide descriptions (e.g. mcp_tools)
        const hasDescriptions = sourceData.options.some((o) => o.description);
        const renderOptionStyled = hasDescriptions
          ? (props: React.HTMLAttributes<HTMLLIElement>, option: string) => renderDescriptiveOption(props, option, sourceData.options)
          : undefined;

        if (isArray) {
          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }} data-testid={`options-source-${fieldName}`}>
                <FilterDropdownButton
                  id={fieldName}
                  multiple
                  freeSolo
                  options={sourceData.options}
                  value={currentValue}
                  onSelect={(_e: any, newValue: any) => {
                    const arr = Array.isArray(newValue) ? newValue : newValue ? [newValue] : [];
                    const stringified = arr
                      .map((v: any) => (typeof v === 'string' ? v : v?.value ?? v?.label ?? ''))
                      .filter((v: any) => typeof v === 'string' && v !== '');
                    const expanded = stringified.flatMap((v: string) =>
                      /[;,\s]/.test(v)
                        ? v
                            .split(/[;,\s]+/)
                            .map((t) => t.trim())
                            .filter(Boolean)
                        : [v]
                    );
                    handleDataChange(fieldName, Array.from(new Set(expanded)));
                  }}
                  disabled={isReadOnly || viewOnlyMode}
                  isOptionsLoading={sourceData.loading}
                  required={isRequired}
                  placeholder={
                    sourceData.loading
                      ? 'Loading...'
                      : /^(recipients|cc|bcc)$|email|recipient/i.test(fieldName)
                      ? 'Type or paste emails, separate with ; or ,'
                      : `Select or type ${fieldName.replace(/_/g, ' ')} and press Enter`
                  }
                  searchPlaceholder={`Search or type ${fieldName.replace(/_/g, ' ')}...`}
                  limitTag={3}
                  sx={{
                    width: '100%',
                    ...(validationErrors[fieldName] && {
                      border: `1px solid ${colors.border?.error || '#d32f2f'}`,
                      boxShadow: 'none',
                    }),
                  }}
                />
                {validationErrors[fieldName] && (
                  <Typography variant='caption' sx={{ color: colors.border.error, mt: 0.5, display: 'block' }}>
                    {validationErrors[fieldName]}
                  </Typography>
                )}
                {fieldSchema.description && (
                  <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                    {fieldSchema.description}
                  </Typography>
                )}
              </Box>
            </Box>
          );
        }

        // Strict-pick FilterDropdown for the per-node LLM model picker —
        // freeSolo is disabled so users can't type a model that isn't in the
        // ai_list_models registry. parseModelParam on the backend tolerates
        // legacy `"provider / model"` label-form values, but new edits go
        // through the dropdown's exact value (`provider/model`) only.
        if (fieldSchema.options_source?.type === 'llm_models') {
          return (
            <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }} data-testid={`options-source-${fieldName}`}>
                <FilterDropdownButton
                  id={fieldName}
                  options={sourceData.options}
                  value={(fieldValue as string) || ''}
                  onSelect={(_e: any, newValue: any) => {
                    const v = typeof newValue === 'string' ? newValue : newValue?.value ?? '';
                    handleDataChange(fieldName, v || '');
                  }}
                  disabled={isReadOnly || viewOnlyMode}
                  isOptionsLoading={sourceData.loading}
                  required={isRequired}
                  placeholder={sourceData.loading ? 'Loading...' : 'Account default'}
                  searchPlaceholder='Search models...'
                  sx={{
                    width: '100%',
                    ...(validationErrors[fieldName] && {
                      border: `1px solid ${colors.border?.error || '#d32f2f'}`,
                      boxShadow: 'none',
                    }),
                  }}
                />
                {validationErrors[fieldName] && (
                  <Typography variant='caption' sx={{ color: colors.border.error, mt: 0.5, display: 'block' }}>
                    {validationErrors[fieldName]}
                  </Typography>
                )}
                {fieldSchema.description && (
                  <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                    {fieldSchema.description}
                  </Typography>
                )}
              </Box>
            </Box>
          );
        }

        // Single-select with freeSolo
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
              <Autocomplete
                freeSolo
                autoSelect
                options={optionValues}
                getOptionLabel={getOptionLabel}
                {...(renderOptionStyled && { renderOption: renderOptionStyled })}
                value={(fieldValue as string) || ''}
                onChange={(_e, newValue) => handleDataChange(fieldName, newValue || '')}
                loading={sourceData.loading}
                disabled={isReadOnly || viewOnlyMode}
                renderInput={(params) => (
                  <TextField
                    {...params}
                    id={`action-sidebar-options-source-${fieldName}-input`}
                    size='small'
                    placeholder={placeholder}
                    error={!!validationErrors[fieldName]}
                    helperText={validationErrors[fieldName] || ''}
                  />
                )}
                size='small'
                data-testid={`options-source-${fieldName}`}
              />
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      // Determine field type based on schema
      const getFieldType = (): string => {
        // Check for encrypted fields first - always use password
        if (fieldSchema.is_encrypted) {
          return 'password';
        }

        // Check sub_type from backend schema (e.g., "textarea" for prompt fields)
        if (fieldSchema.sub_type === 'textarea') {
          return 'textarea';
        }

        // Check for nested schema objects
        // Schema can have properties directly under schema or under schema.properties
        if (fieldSchema.type === 'object' && fieldSchema.schema && Object.keys(fieldSchema.schema).length > 0) {
          // Check if schema has direct properties (not wrapped in 'properties' key)
          const schemaObj = fieldSchema.schema as Record<string, any>;
          const hasDirectProperties = Object.keys(schemaObj).some((key) => key !== 'properties' && typeof schemaObj[key] === 'object');
          const hasNestedProperties = fieldSchema.schema.properties && Object.keys(fieldSchema.schema.properties).length > 0;
          if (hasDirectProperties || hasNestedProperties) {
            return 'nested_schema';
          }
        }

        // Check for array with options (multi-select)
        if (fieldSchema.type === 'array' && (fieldSchema.options || fieldSchema.enum)) {
          return 'multiselect';
        }

        // Check predefined type mappings
        if (FIELD_TYPE_MAP[fieldSchema.type]) {
          return FIELD_TYPE_MAP[fieldSchema.type];
        }

        // Special cases based on field name
        const fieldNameLower = fieldName.toLowerCase();

        // Duration fields
        if (DURATION_FIELD_PATTERNS.some((pattern) => fieldNameLower.includes(pattern))) {
          return 'duration';
        }

        // Key-value fields
        if (fieldSchema.type === 'object' && KEYVALUE_FIELD_PATTERNS.some((pattern) => fieldNameLower.includes(pattern))) {
          return 'keyvalue';
        }

        // Resource name dropdown when name field appears alongside account/namespace/kind
        if (fieldNameLower === 'name' && hasResourceNameField) {
          return 'resource_dropdown';
        }

        // Dropdown for specific field names, enums, or options
        if (fieldName === 'dbms_type' || fieldNameLower === 'namespace' || fieldNameLower === 'kind' || fieldSchema.enum || fieldSchema.options) {
          return 'dropdown';
        }

        // Check if field name suggests script editor (with syntax highlighting)
        if (TEXTAREA_FIELDS.some((field) => fieldNameLower.includes(field))) {
          return 'script';
        }

        // Default to textfield
        return 'textfield';
      };

      const fieldType = getFieldType();

      // Resource name dropdown with cascading filters and Select/Expression toggle
      if (fieldType === 'resource_dropdown') {
        const accountValue = localData?.account || localData?.account_id || localData?.cloud_account_id || '';
        const namespaceValue = localData?.namespace || '';
        const kindValue = localData?.kind || '';

        // Build context chips to show current filter state
        const contextChips: Array<{ label: string; value: string }> = [];
        const accountLabel = cloudAccounts.find((a) => a.value === accountValue)?.label;
        if (accountLabel) contextChips.push({ label: 'Account', value: accountLabel });
        if (namespaceValue) contextChips.push({ label: 'Namespace', value: namespaceValue });
        if (kindValue) contextChips.push({ label: 'Kind', value: kindValue });

        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              <HybridField
                fieldName={fieldName}
                value={fieldValue}
                onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                placeholder={fieldSchema.description || `Select or enter ${fieldName.replace(/_/g, ' ')}`}
                disabled={isReadOnly || viewOnlyMode}
                error={validationErrors[fieldName] || ''}
                required={isRequired}
                options={resourceNames}
                optionsLoading={resourceNamesLoading}
                previousTasks={previousTasks}
                workflowInputs={workflowInputs}
                workflowConfigs={workflowConfigs}
                contextChips={contextChips}
                onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                isDropTarget={isDraggingOver === fieldName}
              />
            </Box>
          </Box>
        );
      }

      // Handle different field types
      if (fieldType === 'switch') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 0.5,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px', display: 'flex', flexDirection: 'column' }}>
              <Switch
                id={`action-sidebar-bool-${fieldName}-switch`}
                checked={fieldValue || false}
                onChange={(e) => handleDataChange(fieldName, e.target.checked)}
                disabled={isReadOnly || viewOnlyMode}
                sx={{ alignSelf: 'flex-start', ml: -1 }}
              />
              {fieldSchema.description && (
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: colors.text.secondaryDark,
                    fontWeight: 'var(--ds-font-weight-regular)',
                    mt: 0.5,
                  }}
                >
                  {fieldSchema.description}
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      // Password field for encrypted/sensitive values
      if (fieldType === 'password') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
              <PasswordField
                value={fieldValue}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
                placeholder={fieldSchema.description || `Enter ${fieldName.replace(/_/g, ' ')}`}
              />
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      // Duration field for timeout/duration values
      if (fieldType === 'duration') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
              <DurationInput
                value={fieldValue}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
              />
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      // Key-value editor for headers, env, etc.
      if (fieldType === 'keyvalue') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
              <KeyValueHybridField
                value={fieldValue as Record<string, string> | string}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
                previousTasks={previousTasks}
                workflowInputs={workflowInputs}
                workflowConfigs={workflowConfigs}
              />
            </Box>
          </Box>
        );
      }

      // Multi-select chips for arrays with options
      if (fieldType === 'multiselect') {
        const options = fieldSchema.options || fieldSchema.enum || [];
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
              <MultiSelectChips
                value={Array.isArray(fieldValue) ? fieldValue : []}
                options={options}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
              />
            </Box>
          </Box>
        );
      }

      // Nested schema editor for complex objects with defined properties
      if (fieldType === 'nested_schema' && fieldSchema.schema) {
        // Schema can have properties directly or wrapped in 'properties' key
        const schemaProperties = (fieldSchema.schema.properties || fieldSchema.schema) as Record<string, any>;
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
              <NestedSchemaEditor
                value={typeof fieldValue === 'object' ? fieldValue : {}}
                schema={schemaProperties}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
                title={formatFieldLabel(fieldName)}
                cloudAccounts={cloudAccounts}
                ticketConfigurations={ticketConfigurations}
              />
            </Box>
          </Box>
        );
      }

      if (fieldType === 'dropdown') {
        const options = getDropdownOptionsForField(fieldName, fieldSchema, {
          cloudAccounts,
          integrations,
          notifications,
          ticketConfigurations,
          namespaces,
          resourceTypes,
          workloadKinds,
          dbmsOptions: DBMS_OPTIONS,
        });

        // Use HybridField for dropdowns that could also accept template expressions
        const fieldNameLower = fieldName.toLowerCase();
        const isTemplateEligibleDropdown =
          ((fieldNameLower === 'namespace' || fieldNameLower === 'kind') && fieldSchema.type === 'string') ||
          fieldSchema.type === 'integration' ||
          fieldName === 'integration_id';

        if (isTemplateEligibleDropdown) {
          const showMcpGuidance = fieldName === 'integration_id' && selectedActionType === 'llm.mcp_call';
          return (
            <React.Fragment key={fieldName}>
              <Box sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: colors.text.secondary,
                    minWidth: '120px',
                    maxWidth: '120px',
                    pt: 1,
                  }}
                >
                  {fieldSchema.title || formatFieldLabel(fieldName)}
                  {isRequired && <span style={{ color: colors.border.error }}> *</span>}
                </Typography>
                <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                  <HybridField
                    fieldName={fieldName}
                    value={fieldValue}
                    onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                    placeholder={fieldSchema.description || `Select ${fieldName.replace(/_/g, ' ')}`}
                    disabled={isReadOnly || viewOnlyMode}
                    error={validationErrors[fieldName] || ''}
                    required={isRequired}
                    options={options}
                    optionsLoading={fieldNameLower === 'namespace' ? namespacesLoading : fieldNameLower === 'kind' ? workloadKindsLoading : undefined}
                    previousTasks={previousTasks}
                    workflowInputs={workflowInputs}
                    workflowConfigs={workflowConfigs}
                    onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
                    onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
                    onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
                    isDropTarget={isDraggingOver === fieldName}
                  />
                </Box>
              </Box>
              {showMcpGuidance && (
                <Box sx={{ display: 'flex', gap: 2, mb: 2 }}>
                  <Box sx={{ minWidth: '120px', flexShrink: 0 }} />
                  <Alert
                    severity='info'
                    sx={{
                      flex: 1,
                      py: 0.5,
                      fontSize: 'var(--ds-text-caption)',
                      '& .MuiAlert-icon': { py: 0.5 },
                      '& .MuiAlert-message': { fontSize: 'var(--ds-text-caption)', py: 0.25 },
                    }}
                    data-testid='mcp-integration-alert'
                  >
                    To add an MCP integration, go to Admin &gt; Integrations &gt; MCP.
                    <br />
                    <Button
                      id='action-sidebar-mcp-manage-integrations-btn'
                      tone='link'
                      size='xs'
                      onClick={() => window.open('/accounts/account-form?cloudProvider=mcp', '_blank', 'noopener,noreferrer')}
                    >
                      Manage MCP Integrations &rarr;
                    </Button>
                  </Alert>
                </Box>
              )}
            </React.Fragment>
          );
        }

        const isAccountField = fieldSchema.type === 'account';
        // Defensive sanitization for non-account autocompletes: drop a saved
        // value that's no longer in the filtered options list. The Account
        // branch handles its own sanitization inside AccountField.
        const renderedValue =
          !isAccountField && fieldValue && options.length > 0 && !(options as any[]).some((o) => o?.value === fieldValue) ? '' : fieldValue;
        // Show the resolved default provider (logs / metrics / traces) next
        // to the account field for tasks whose schema declares the synthetic
        // account_provider_type field.
        const showDefaultProviderChip = isAccountField && !!inputSchema.account_provider_type && !!fieldValue && !!defaultProvider;
        const defaultProviderChipLabel =
          defaultProviderKind === 'metrics'
            ? 'Default metric provider'
            : defaultProviderKind === 'traces'
            ? 'Default trace provider'
            : 'Default log provider';

        return (
          <React.Fragment key={fieldName}>
            <Box sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
                {isAccountField ? (
                  <AccountField
                    fieldName={fieldName}
                    value={fieldValue}
                    options={options as any[]}
                    description={fieldSchema.description}
                    placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                    disabled={isReadOnly || viewOnlyMode}
                    error={validationErrors[fieldName] || ''}
                    required={isRequired}
                    defaultFormFieldProps={DEFAULT_FORM_FIELD_PROPS}
                    onChange={(v: string) => handleDataChange(fieldName, v)}
                    onResolveTemplate={(v: string) => setFieldValueNoCascade(fieldName, v)}
                    previousTasks={previousTasks}
                    workflowInputs={workflowInputs}
                    workflowConfigs={workflowConfigs}
                  />
                ) : (options as any[]).some((o) => o?.icon) ? (
                  <FilterDropdownButton
                    id={fieldName}
                    options={options as any[]}
                    value={(options as any[]).find((o) => o?.value === renderedValue) ?? null}
                    onSelect={(_e: any, selected: any) => handleDataChange(fieldName, selected?.value ?? '')}
                    disabled={isReadOnly || viewOnlyMode}
                    required={isRequired}
                    placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                    searchPlaceholder={`Search ${fieldName.replace(/_/g, ' ')}`}
                    sx={{
                      width: '100%',
                      ...(validationErrors[fieldName] && {
                        border: `1px solid ${colors.border?.error || '#d32f2f'}`,
                        boxShadow: 'none',
                      }),
                    }}
                  />
                ) : (
                  <FormField
                    {...DEFAULT_FORM_FIELD_PROPS}
                    description={fieldSchema.description || ''}
                    value={renderedValue}
                    onChange={(e: any) => handleDataChange(fieldName, e.target.value)}
                    onSelect={(e: any) => handleDataChange(fieldName, e?.target?.value)}
                    placeholder={`Select ${fieldName.replace(/_/g, ' ')}`}
                    disabled={isReadOnly || viewOnlyMode}
                    error={validationErrors[fieldName] || ''}
                    fieldType='autocomplete'
                    options={options as any}
                    required={isRequired}
                    minWidth='100%'
                    maxLength={0}
                  />
                )}
                {!isAccountField && (options as any[]).some((o) => o?.icon) && validationErrors[fieldName] && (
                  <Typography sx={{ mt: 0.5, fontSize: 'var(--ds-text-small)', color: colors.border?.error || '#d32f2f' }}>
                    {validationErrors[fieldName]}
                  </Typography>
                )}
                {showDefaultProviderChip && (
                  <Box sx={{ mt: 0.75 }}>
                    <Chip
                      size='small'
                      label={`${defaultProviderChipLabel}: ${defaultProvider}`}
                      sx={{ fontSize: 'var(--ds-text-caption)', height: '20px', bgcolor: colors.lowestLight, color: colors.text.secondary }}
                    />
                  </Box>
                )}
              </Box>
            </Box>
            {/* MCP integration guidance — shown directly below the integration_id dropdown */}
            {fieldName === 'integration_id' && selectedActionType === 'llm.mcp_call' && (
              <Box sx={{ display: 'flex', gap: 2, mb: 2 }}>
                <Box sx={{ minWidth: '120px', flexShrink: 0 }} />
                <Alert
                  severity='info'
                  sx={{
                    flex: 1,
                    py: 0.5,
                    fontSize: 'var(--ds-text-caption)',
                    '& .MuiAlert-icon': { py: 0.5 },
                    '& .MuiAlert-message': { fontSize: 'var(--ds-text-caption)', py: 0.25 },
                  }}
                  data-testid='mcp-integration-alert'
                >
                  To add an MCP integration, go to Admin &gt; Integrations &gt; MCP.
                  <br />
                  <Button
                    id='action-sidebar-mcp-manage-integrations-id-btn'
                    tone='link'
                    size='xs'
                    onClick={() => window.open('/accounts/account-form?cloudProvider=mcp', '_blank', 'noopener,noreferrer')}
                  >
                    Manage MCP Integrations &rarr;
                  </Button>
                </Alert>
              </Box>
            )}
            {/* SSM permission guidance — shown directly below the executor_type dropdown */}
            {fieldName === 'executor_type' && selectedActionType === 'scripting.run_script' && localData?.executor_type === 'aws_ssm' && (
              <Box sx={{ display: 'flex', gap: 2, mb: 2 }}>
                <Box sx={{ minWidth: '120px', flexShrink: 0 }} />
                <Alert
                  severity='info'
                  sx={{
                    flex: 1,
                    py: 0.5,
                    fontSize: 'var(--ds-text-caption)',
                    '& .MuiAlert-icon': { py: 0.5 },
                    '& .MuiAlert-message': { fontSize: 'var(--ds-text-caption)', py: 0.25 },
                  }}
                  data-testid='ssm-permission-alert'
                >
                  Ensure SSM access is enabled for the selected AWS account. If you encounter permission errors, update the account permissions to
                  enable SSM access via CloudFormation. Ignore if already done.
                  <br />
                  <Button
                    id='action-sidebar-ssm-manage-permissions-btn'
                    tone='link'
                    size='xs'
                    onClick={() => window.open('/accounts/account-form?cloudProvider=AWS', '_blank', 'noopener,noreferrer')}
                  >
                    Manage AWS Integrations &rarr;
                  </Button>
                </Alert>
              </Box>
            )}
          </React.Fragment>
        );
      }

      if (fieldType === 'textarea') {
        const isJsonField = fieldSchema.type === 'object' || fieldSchema.type === 'array';

        // Use StableTextarea to prevent focus loss on re-renders
        return (
          <StableTextarea
            key={fieldName}
            fieldName={fieldName}
            value={fieldValue}
            onChange={handleDataChange}
            label={fieldSchema.title || formatFieldLabel(fieldName)}
            isRequired={isRequired}
            description={fieldSchema.description || ''}
            placeholder={FIELD_PLACEHOLDERS[fieldName] || fieldSchema.description || `Enter ${fieldName.replace(/_/g, ' ')}`}
            disabled={isReadOnly || viewOnlyMode}
            error={validationErrors[fieldName] || ''}
            rows={8}
            maxRows={20}
            minRows={6}
            maxLength={150000}
            isJsonField={isJsonField}
          />
        );
      }

      if (fieldType === 'number') {
        // Use StableNumberField to prevent focus loss on re-renders
        return (
          <StableNumberField
            key={fieldName}
            fieldName={fieldName}
            value={fieldValue}
            onChange={handleDataChange}
            label={fieldSchema.title || formatFieldLabel(fieldName)}
            isRequired={isRequired}
            description={fieldSchema.description || ''}
            disabled={isReadOnly || viewOnlyMode}
            error={validationErrors[fieldName] || ''}
            isInteger={fieldSchema.type === 'integer'}
          />
        );
      }

      if (fieldType === 'timestamp') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 300px', minWidth: '200px' }}>
              <TimestampPicker
                value={fieldValue}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
                includeTime={true}
                label={fieldSchema.title || formatFieldLabel(fieldName)}
              />
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mt: 0.5, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      if (fieldType === 'array') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
              <ArrayEditor
                value={Array.isArray(fieldValue) ? fieldValue : []}
                onChange={(value) => handleDataChange(fieldName, value)}
                error={validationErrors[fieldName]}
                itemSchema={(() => {
                  const raw = (fieldSchema.schema?.properties || fieldSchema.schema) as Record<string, any> | undefined;
                  if (!raw) return undefined;
                  // For switch cases, hide edge-derived fields (next, tasks, default)
                  if (isSwitchTask && fieldName === 'cases') {
                    const { next: _n, tasks: _t, ...rest } = raw;
                    return Object.keys(rest).length > 0 ? rest : undefined;
                  }
                  return raw;
                })()}
              />
            </Box>
          </Box>
        );
      }

      if (fieldType === 'json') {
        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
              <JsonEditor value={fieldValue || {}} onChange={(value) => handleDataChange(fieldName, value)} error={validationErrors[fieldName]} />
            </Box>
          </Box>
        );
      }

      if (fieldType === 'script') {
        const getScriptPlaceholder = () => {
          return FIELD_PLACEHOLDERS[fieldName] || fieldSchema.description || `Enter ${fieldName.replace(/_/g, ' ')}`;
        };

        const isScriptField = fieldName === 'script';
        const scriptHeight = isScriptField ? '300px' : '200px';
        const language = getCodeLanguage(fieldName, fieldSchema.sub_type);

        return (
          <Box key={fieldName} sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: colors.text.secondary,
                minWidth: '120px',
                maxWidth: '120px',
                pt: 1,
              }}
            >
              {fieldSchema.title || formatFieldLabel(fieldName)}
              {isRequired && <span style={{ color: colors.border.error }}> *</span>}
            </Typography>
            <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
              {fieldSchema.description && (
                <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                  {fieldSchema.description}
                </Typography>
              )}
              <CodeEditorWithLanguage
                value={fieldValue}
                onChange={(value) => handleDataChange(fieldName, value)}
                language={language}
                error={validationErrors[fieldName]}
                disabled={isReadOnly || viewOnlyMode}
                placeholder={getScriptPlaceholder()}
                height={scriptHeight}
              />
            </Box>
          </Box>
        );
      }

      // Check if this field should use template-aware text field
      const shouldUseTemplateField = () => {
        // Use template field for string fields that commonly reference other task outputs
        const templateSuggestedFields = [
          'message',
          'content',
          'text',
          'body',
          'description',
          'prompt',
          'query',
          'command',
          'script',
          'payload',
          'data',
          'input',
          'subject',
          'title',
          'name',
          'path',
          'url',
          'endpoint',
        ];

        const fieldNameLower = fieldName.toLowerCase();
        return templateSuggestedFields.some((field) => fieldNameLower.includes(field)) && fieldSchema.type === 'string';
      };

      // Get previous tasks for template suggestions
      const getPreviousTasks = () => {
        const selectedNode = nodes.find((node) => node.selected && node.type === 'action');
        if (!selectedNode) {
          return [];
        }

        return getPreviousTasksForNode(selectedNode.id, nodes, edges, taskDefinitions);
      };

      // Use TemplateTextField for template-aware fields
      if (shouldUseTemplateField()) {
        const prevTasks = getPreviousTasks();

        // Check if field should be multiline (textarea) - exclude _id fields
        const fieldNameLower = fieldName.toLowerCase();
        const isMultilineField =
          (fieldNameLower.includes('message') || fieldNameLower.includes('summary') || fieldNameLower.includes('body')) &&
          !fieldNameLower.endsWith('_id');

        // Expose date/time built-in variables on the email action's subject and body
        // fields so users can compose dynamic subjects without learning template syntax.
        const fieldBuiltins =
          selectedActionType && EMAIL_TASK_NAMES.has(selectedActionType) && (fieldNameLower === 'subject' || fieldNameLower === 'body')
            ? EMAIL_BUILTIN_VARIABLES
            : undefined;

        // Don't use DroppableWrapper here - it causes component remounting and focus loss
        // Instead, apply drop handlers directly to the outer Box
        const dropHandlers = !viewOnlyMode
          ? {
              onDrop: (e: React.DragEvent) => handleDrop(e, fieldName, fieldValue),
              onDragOver: (e: React.DragEvent) => handleDragOver(e, fieldName),
              onDragLeave: handleDragLeave,
            }
          : {};

        return (
          <Box
            key={fieldName}
            {...dropHandlers}
            sx={{
              transition: 'all 0.2s ease',
              borderRadius: 1,
              ...(isDropTarget && {
                outline: '2px dashed var(--ds-blue-400)',
                outlineOffset: 2,
                backgroundColor: 'var(--ds-blue-100)',
              }),
            }}
          >
            <Box sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: colors.text.secondary,
                  minWidth: '120px',
                  maxWidth: '120px',
                  pt: 1,
                }}
              >
                {fieldSchema.title || formatFieldLabel(fieldName)}
                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
              </Typography>
              <Box sx={{ flex: '1 1 400px', minWidth: '300px' }}>
                <TemplateTextField
                  value={fieldValue}
                  onChange={(newValue: string) => handleDataChange(fieldName, newValue)}
                  placeholder={fieldSchema.description || `Enter ${fieldName.replace(/_/g, ' ')}`}
                  disabled={isReadOnly || viewOnlyMode}
                  error={validationErrors[fieldName] || ''}
                  required={isRequired}
                  previousTasks={prevTasks}
                  workflowInputs={workflowInputs}
                  workflowConfigs={workflowConfigs}
                  builtinVariables={fieldBuiltins}
                  multiline={isMultilineField}
                  rows={isMultilineField ? 6 : undefined}
                  maxRows={isMultilineField ? 10 : undefined}
                  fullWidth={true}
                />
              </Box>
            </Box>
          </Box>
        );
      }

      // Use StableTextField to prevent focus loss on re-renders
      return (
        <StableTextField
          key={fieldName}
          fieldName={fieldName}
          value={fieldValue}
          onChange={handleDataChange}
          label={fieldSchema.title || formatFieldLabel(fieldName)}
          isRequired={isRequired}
          description={fieldSchema.description || ''}
          placeholder={fieldSchema.description || `Enter ${fieldName.replace(/_/g, ' ')}`}
          disabled={isReadOnly || viewOnlyMode}
          error={validationErrors[fieldName] || ''}
          isDropTarget={isDropTarget}
          onDrop={viewOnlyMode ? undefined : (e) => handleDrop(e, fieldName, fieldValue)}
          onDragOver={viewOnlyMode ? undefined : (e) => handleDragOver(e, fieldName)}
          onDragLeave={viewOnlyMode ? undefined : handleDragLeave}
        />
      );
    };

    // Combined function to render schema information (inputs from previous node or outputs from current task)
    const renderSchemaInfo = (
      schemaData: any,
      config: { title: string; description: string; number?: string | number; formatFieldName?: boolean }
    ) => {
      if (!schemaData || Object.keys(schemaData).length === 0) {
        return null;
      }

      return (
        <FormCard title={config.title} description={config.description} icon={null} number={config.number || ''} columns={1}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', width: '100%' }}>
            {Object.entries(schemaData)
              .filter(([_, fieldSchema]: [string, any]) => !fieldSchema?.hidden)
              .map(([fieldName, fieldSchema]: [string, any]) => (
                <Box
                  key={fieldName}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 1,
                    p: 'var(--ds-space-2)',
                    border: '1px solid',
                    borderColor: colors.lowestLight,
                    borderRadius: 0.5,
                  }}
                >
                  <Box
                    sx={{
                      bgcolor: colors.text.secondary,
                      color: 'white',
                      px: 0.75,
                      py: 0.125,
                      borderRadius: 0.25,
                      fontSize: '0.675rem',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      minWidth: 40,
                      textAlign: 'center',
                      textTransform: 'lowercase',
                    }}
                  >
                    {fieldSchema.type || 'any'}
                  </Box>

                  <Box sx={{ flex: 1, minHeight: 0 }}>
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 0.25,
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        color: colors.text.secondary,
                        fontSize: '0.8rem',
                        lineHeight: 1.2,
                      }}
                    >
                      {config.formatFieldName ? formatFieldLabel(fieldName) : fieldName}
                      {fieldSchema.required && (
                        <Box
                          component='span'
                          sx={{
                            color: 'warning.main',
                            fontSize: '0.675rem',
                            fontWeight: 'var(--ds-font-weight-semibold)',
                          }}
                        >
                          required
                        </Box>
                      )}
                    </Box>

                    {fieldSchema.description && (
                      <Box
                        sx={{
                          color: colors.text.secondary,
                          fontSize: '0.675rem',
                          mt: 0.125,
                          lineHeight: 1.2,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          display: '-webkit-box',
                          WebkitLineClamp: 2,
                          WebkitBoxOrient: 'vertical',
                        }}
                      >
                        {fieldSchema.description}
                      </Box>
                    )}

                    {fieldSchema.default !== null && fieldSchema.default !== undefined && (
                      <Box
                        sx={{
                          fontSize: '0.625rem',
                          mt: 0.125,
                          fontFamily: 'monospace',
                          bgcolor: colors.text.secondary,
                          color: 'white',
                          px: 0.375,
                          py: 0.0625,
                          borderRadius: 0.125,
                          display: 'inline-block',
                        }}
                      >
                        default: {typeof fieldSchema.default === 'object' ? JSON.stringify(fieldSchema.default) : String(fieldSchema.default)}
                      </Box>
                    )}
                  </Box>
                </Box>
              ))}
          </Box>
        </FormCard>
      );
    };

    // Render Parameters tab content
    const renderParametersTab = () => {
      // Call Workflow is a special inline node — backend resolves the child workflow at execute
      // time by name, so the generic schema-driven form is replaced with a workflow picker that
      // surfaces the called workflow's declared inputs as a typed sub-form.
      if (selectedActionType === 'core.call-workflow') {
        return (
          <CallWorkflowFields
            accountId={accountId}
            taskData={localData}
            onTaskDataChange={(next) => {
              setLocalData(next);
            }}
            viewOnlyMode={viewOnlyMode}
            validationErrors={validationErrors}
            previousTasks={previousTasks}
            workflowInputs={workflowInputs}
            workflowConfigs={workflowConfigs}
            currentWorkflowId={currentWorkflowId}
          />
        );
      }

      // For switch tasks, hide fields that are derived from canvas edges (not user-editable)
      const hiddenFields = isSwitchTask ? new Set(['default_next', 'default']) : new Set<string>();

      // Sort fields based on order and required status
      const fields = Object.entries(inputSchema)
        .filter(([fieldName, fieldSchema]) => !hiddenFields.has(fieldName) && !(fieldSchema as SchemaProperty).hidden)
        .sort(([_, schemaA], [__, schemaB]) => {
          const sA = schemaA as SchemaProperty;
          const sB = schemaB as SchemaProperty;

          // Primary sort: Order (ascending)
          const orderA = sA.order ?? 0;
          const orderB = sB.order ?? 0;
          if (orderA !== orderB) {
            return orderA - orderB;
          }

          // Secondary sort: Required (true first)
          const requiredA = sA.required === true;
          const requiredB = sB.required === true;
          if (requiredA !== requiredB) {
            return requiredA ? -1 : 1;
          }

          return 0;
        });

      // Detect paired timestamp fields (start_time + end_time) to render them together
      const hasDuration = fields.some(([name]) => name === 'duration');
      const hasStartTime = fields.some(([name]) => name === 'start_time');
      const hasEndTime = fields.some(([name]) => name === 'end_time');
      const hasFullTimeGroup = hasDuration && hasStartTime && hasEndTime;
      const hasTimePair = !hasFullTimeGroup && hasStartTime && hasEndTime;
      const timeFieldNames = new Set(hasFullTimeGroup ? ['duration', 'start_time', 'end_time'] : ['start_time', 'end_time']);
      const fullTimeGroupTrigger = hasFullTimeGroup ? fields.find(([n]) => timeFieldNames.has(n))?.[0] : null;

      // Detect paired resize fields (change_by + change_to) — only one is meaningful at a time
      const hasChangeBy = fields.some(([name]) => name === 'change_by');
      const hasChangeTo = fields.some(([name]) => name === 'change_to');
      const hasChangeGroup = hasChangeBy && hasChangeTo;
      const changeFieldNames = new Set(['change_by', 'change_to']);
      const changeGroupTrigger = hasChangeGroup ? fields.find(([n]) => changeFieldNames.has(n))?.[0] : null;

      const handleChangeModeChange = (newMode: 'by' | 'to') => {
        if (newMode === changeMode) return;
        setChangeMode(newMode);
        const { change_by: _cb, change_to: _ct, ...rest } = localDataRef.current;
        const updated = { ...rest };

        setLocalData(updated);
      };

      const handleTimeModeChange = (newMode: 'relative' | 'absolute') => {
        if (newMode === timeMode) return;
        setTimeMode(newMode);
        const durationSchema = inputSchema['duration'] as SchemaProperty | undefined;
        const defaultDuration = (durationSchema?.default as string) || '1h';
        const { duration: _d, start_time: _st, end_time: _et, ...rest } = localDataRef.current;
        const updated = newMode === 'relative' ? { ...rest, duration: defaultDuration } : { ...rest };

        setLocalData(updated);
      };

      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {/* Form Description */}
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondaryDark, mb: 1 }}>{description}</Typography>

          {/* Input Fields */}
          {fields.map(([fieldName, fieldSchema], _index) => {
            // Unified Time Range group: mode dropdown + either duration or absolute pickers
            if (hasFullTimeGroup && timeFieldNames.has(fieldName)) {
              if (fieldName !== fullTimeGroupTrigger) return null;
              const durationSchema = inputSchema['duration'] as SchemaProperty;
              const startSchema = inputSchema['start_time'] as SchemaProperty;
              const endSchema = inputSchema['end_time'] as SchemaProperty;
              const durationOptionsRaw = (durationSchema?.options as string[]) || (durationSchema?.enum as string[]) || [];
              const durationOptions = durationOptionsRaw.map((v: string) => ({ label: v, value: v }));
              const modeOptions = [
                { label: 'Relative Range', value: 'relative' },
                { label: 'Absolute Range', value: 'absolute' },
              ];
              return (
                <Box key='time-range-group' sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2 }}>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-body)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: colors.text.secondary,
                      minWidth: '120px',
                      maxWidth: '120px',
                      pt: 1,
                    }}
                  >
                    Time Range
                  </Typography>
                  <Box sx={{ flex: '1 1 300px', minWidth: '200px', display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <FormField
                      {...DEFAULT_FORM_FIELD_PROPS}
                      fieldType='autocomplete'
                      value={timeMode}
                      onSelect={(e: any) => handleTimeModeChange(e?.target?.value === 'absolute' ? 'absolute' : 'relative')}
                      options={modeOptions as any}
                      placeholder='Select time range type'
                      disabled={viewOnlyMode}
                      minWidth='100%'
                    />
                    {timeMode === 'relative' ? (
                      <FormField
                        {...DEFAULT_FORM_FIELD_PROPS}
                        fieldType='autocomplete'
                        value={localData?.['duration'] ?? (durationSchema?.default as string) ?? '1h'}
                        onSelect={(e: any) => handleDataChange('duration', e?.target?.value)}
                        options={durationOptions as any}
                        placeholder='Select duration'
                        description={durationSchema?.description || ''}
                        disabled={viewOnlyMode}
                        minWidth='100%'
                      />
                    ) : (
                      <>
                        <TimestampPicker
                          value={localData?.['start_time'] ?? ''}
                          onChange={(value: string) => handleDataChange('start_time', value)}
                          error={validationErrors['start_time']}
                          disabled={viewOnlyMode}
                          includeTime={true}
                          label={startSchema?.title || 'Start Time'}
                        />
                        <TimestampPicker
                          value={localData?.['end_time'] ?? ''}
                          onChange={(value: string) => handleDataChange('end_time', value)}
                          error={validationErrors['end_time']}
                          disabled={viewOnlyMode}
                          includeTime={true}
                          label={endSchema?.title || 'End Time'}
                        />
                      </>
                    )}
                  </Box>
                </Box>
              );
            }
            // Change group: change_by + change_to are exclusive; render mode dropdown + single input
            if (hasChangeGroup && changeFieldNames.has(fieldName)) {
              if (fieldName !== changeGroupTrigger) return null;
              const changeBySchema = inputSchema['change_by'] as SchemaProperty;
              const changeToSchema = inputSchema['change_to'] as SchemaProperty;
              const modeOptions = [
                { label: 'Change By (%)', value: 'by' },
                { label: 'Change To (absolute)', value: 'to' },
              ];
              const activeSchema = changeMode === 'by' ? changeBySchema : changeToSchema;
              const activeFieldName = changeMode === 'by' ? 'change_by' : 'change_to';
              const activePlaceholder = changeMode === 'by' ? "e.g. '10%'" : "e.g. '20Gi'";
              return (
                <Box key='change-group' sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2 }}>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-body)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: colors.text.secondary,
                      minWidth: '120px',
                      maxWidth: '120px',
                      pt: 1,
                    }}
                  >
                    Resize
                  </Typography>
                  <Box sx={{ flex: '1 1 300px', minWidth: '200px', display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <FormField
                      {...DEFAULT_FORM_FIELD_PROPS}
                      fieldType='autocomplete'
                      value={changeMode}
                      onSelect={(e: any) => handleChangeModeChange(e?.target?.value === 'to' ? 'to' : 'by')}
                      options={modeOptions as any}
                      placeholder='Select resize mode'
                      disabled={viewOnlyMode}
                      minWidth='100%'
                    />
                    <FormField
                      {...DEFAULT_FORM_FIELD_PROPS}
                      fieldType='textfield'
                      value={localData?.[activeFieldName] ?? ''}
                      onChange={(e: any) => handleDataChange(activeFieldName, e.target.value)}
                      placeholder={activePlaceholder}
                      description={activeSchema?.description || ''}
                      disabled={viewOnlyMode}
                      minWidth='100%'
                    />
                  </Box>
                </Box>
              );
            }
            // Legacy: only start_time + end_time (no duration field)
            if (hasTimePair && timeFieldNames.has(fieldName)) {
              if (fieldName === 'end_time') return null;
              const startSchema = inputSchema['start_time'] as SchemaProperty;
              const endSchema = inputSchema['end_time'] as SchemaProperty;
              return (
                <Box key='time-range-group' sx={{ mb: 2, display: 'flex', alignItems: 'flex-start', gap: 2 }}>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-body)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: colors.text.secondary,
                      minWidth: '120px',
                      maxWidth: '120px',
                      pt: 1,
                    }}
                  >
                    Time Range
                  </Typography>
                  <Box sx={{ flex: '1 1 300px', minWidth: '200px', display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <TimestampPicker
                      value={localData?.['start_time'] ?? ''}
                      onChange={(value: string) => handleDataChange('start_time', value)}
                      error={validationErrors['start_time']}
                      disabled={viewOnlyMode}
                      includeTime={true}
                      label={startSchema?.title || 'Start Time'}
                    />
                    <TimestampPicker
                      value={localData?.['end_time'] ?? ''}
                      onChange={(value: string) => handleDataChange('end_time', value)}
                      error={validationErrors['end_time']}
                      disabled={viewOnlyMode}
                      includeTime={true}
                      label={endSchema?.title || 'End Time'}
                    />
                  </Box>
                </Box>
              );
            }
            return renderField(fieldName, fieldSchema as SchemaProperty);
          })}

          {/* Dynamic ticket fields from DynamicFieldsSource */}
          {isTicketCreateTask &&
            (() => {
              // Platform Fields = create-meta fields the backend did NOT tag as a basic
              // field. Fields with a group ('severity'/'title'/'description') are surfaced
              // by their static control (Severity/Title/Description), so excluding them
              // here guarantees no field renders twice — regardless of tool or key.
              const platformFields = Object.entries(ticketDynamicFields).filter(([, fieldMeta]) => !fieldMeta?.group);
              if (platformFields.length === 0) return null;
              return (
                <Box sx={{ mt: 1 }}>
                  <Typography
                    sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary, mb: 1 }}
                  >
                    Platform Fields
                  </Typography>
                  {platformFields.map(([fieldKey, fieldMeta]) => (
                    <PlatformFieldItem
                      key={fieldKey}
                      fieldKey={fieldKey}
                      fieldMeta={fieldMeta}
                      localData={localData}
                      dynamicOptions={ticketFieldOptions[fieldKey] || []}
                      dynamicLoading={ticketFieldOptionsLoading[fieldKey] || false}
                      viewOnlyMode={viewOnlyMode}
                      previousTasks={previousTasks}
                      workflowInputs={workflowInputs}
                      workflowConfigs={workflowConfigs}
                      onDataChange={handleDataChange}
                      onSearchField={searchTicketField}
                    />
                  ))}
                </Box>
              );
            })()}
        </Box>
      );
    };

    // Render Condition tab content
    const renderConditionTab = () => {
      const hasCondition = !!taskConfig.if;

      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.5 }}>
            <AltRoute sx={{ fontSize: 18, color: colors.text.secondary }} />
            <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
              {'Conditional Execution'}
            </Typography>
            {hasCondition && (
              <Box
                sx={{
                  width: 6,
                  height: 6,
                  borderRadius: '50%',
                  bgcolor: 'primary.main',
                }}
              />
            )}
          </Box>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondary, mb: 1 }}>
            {'Define a condition to control whether this task executes. The task runs only when the condition evaluates to true.'}
          </Typography>
          <TemplateExpressionField
            label='Condition'
            value={taskConfig.if}
            onChange={(value) => onTaskConfigChange?.('if', value || undefined)}
            disabled={viewOnlyMode}
            previousTasks={previousTasks}
          />
        </Box>
      );
    };

    // Render Settings tab content
    const renderSettingsTab = () => {
      const hasExecutionControl = !!taskConfig.timeout;
      const hasDataManagement = !!(taskConfig.set_state || taskConfig.set_vars);
      const hasParallelExecution = !!taskConfig.matrix;
      const hasErrorHandling = !!(taskConfig.hooks || taskConfig.failure_policy);

      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          {/* Disable Task — disconnects the node from the DAG so it's skipped at runtime */}
          <Box
            sx={{
              display: 'flex',
              alignItems: 'flex-start',
              justifyContent: 'space-between',
              gap: 1.5,
              px: 1.5,
              py: 1.25,
              border: `1px solid ${colors.lowestLight}`,
              borderRadius: 1,
              bgcolor: taskConfig.disabled ? '#f9fafb' : 'transparent',
            }}
          >
            <Box sx={{ flex: 1, minWidth: 0 }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.primary }}>
                Disable task
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary, mt: 0.25 }}>
                {taskConfig.disabled
                  ? 'Task is muted. The workflow has been rerouted around it. Toggle off to restore its original connections.'
                  : 'Skip this task without deleting it. The workflow will be rerouted: predecessors connect directly to successors. Re-enable to restore the original chain.'}
              </Typography>
            </Box>
            <Switch
              data-testid='task-disable-switch'
              checked={!!taskConfig.disabled}
              onChange={(e) => handleDisableToggle(e.target.checked)}
              disabled={viewOnlyMode || !onToggleDisable}
              size='small'
            />
          </Box>

          {/* Execution Control Group */}
          <Box id='execution-control' sx={{ mb: 2, scrollMarginTop: '16px' }}>
            <CollapsableCard
              composition='header+meta+body'
              elevation='flat'
              defaultOpen={hasExecutionControl}
              header={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  <Timer sx={{ fontSize: 16, color: colors.text.secondary }} />
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: colors.text.secondary,
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                    }}
                  >
                    Execution Control
                  </Typography>
                </Box>
              }
              meta={hasExecutionControl ? <Box sx={{ width: 6, height: 6, borderRadius: '50%', bgcolor: 'primary.main' }} /> : undefined}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <DurationField
                  label='Task Timeout'
                  value={taskConfig.timeout}
                  onChange={(value) => onTaskConfigChange?.('timeout', value || undefined)}
                  disabled={viewOnlyMode}
                  warningMessage={(() => {
                    if (!taskConfig.timeout || !workflowTimeout) return undefined;
                    const taskSec = parseDurationToSeconds(taskConfig.timeout);
                    const workflowSec = parseDurationToSeconds(workflowTimeout);
                    if (!isNaN(taskSec) && !isNaN(workflowSec) && taskSec > workflowSec) {
                      return `Task timeout (${taskConfig.timeout}) exceeds workflow timeout (${workflowTimeout}). The workflow will terminate before this task completes.`;
                    }
                    return undefined;
                  })()}
                />
              </Box>
            </CollapsableCard>
          </Box>

          {/* Data Management Group */}
          <Box id='data-management' sx={{ mb: 2, scrollMarginTop: '16px' }}>
            <CollapsableCard
              composition='header+meta+body'
              elevation='flat'
              defaultOpen={hasDataManagement}
              header={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  <Storage sx={{ fontSize: 16, color: colors.text.secondary }} />
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: colors.text.secondary,
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                    }}
                  >
                    Data Management
                  </Typography>
                </Box>
              }
              meta={hasDataManagement ? <Box sx={{ width: 6, height: 6, borderRadius: '50%', bgcolor: 'primary.main' }} /> : undefined}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <KeyValueField
                  label='Set State (Persistent)'
                  field='set_state'
                  value={taskConfig.set_state}
                  onChange={(value) => onTaskConfigChange?.('set_state', value)}
                  disabled={viewOnlyMode}
                  showTtl={true}
                />
                <KeyValueField
                  label='Set Variables (Automation Scope)'
                  field='set_vars'
                  value={taskConfig.set_vars}
                  onChange={(value) => onTaskConfigChange?.('set_vars', value)}
                  disabled={viewOnlyMode}
                  showTtl={false}
                />
              </Box>
            </CollapsableCard>
          </Box>

          {/* Parallel Execution Group */}
          <Box id='parallel-execution' sx={{ mb: 2, scrollMarginTop: '16px' }}>
            <CollapsableCard
              composition='header+meta+body'
              elevation='flat'
              defaultOpen={hasParallelExecution}
              header={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  <GridView sx={{ fontSize: 16, color: colors.text.secondary }} />
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: colors.text.secondary,
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                    }}
                  >
                    Parallel Execution
                  </Typography>
                </Box>
              }
              meta={hasParallelExecution ? <Box sx={{ width: 6, height: 6, borderRadius: '50%', bgcolor: 'primary.main' }} /> : undefined}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <MatrixField value={taskConfig.matrix} onChange={(value) => onTaskConfigChange?.('matrix', value)} disabled={viewOnlyMode} />
              </Box>
            </CollapsableCard>
          </Box>

          {/* Error Handling Group */}
          <Box id='error-handling' sx={{ mb: 2, scrollMarginTop: '16px' }}>
            <CollapsableCard
              composition='header+meta+body'
              elevation='flat'
              defaultOpen={hasErrorHandling}
              header={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  <ErrorOutline sx={{ fontSize: 16, color: colors.text.secondary }} />
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: colors.text.secondary,
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                    }}
                  >
                    Error Handling
                  </Typography>
                </Box>
              }
              meta={hasErrorHandling ? <Box sx={{ width: 6, height: 6, borderRadius: '50%', bgcolor: 'primary.main' }} /> : undefined}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <FailurePolicyField
                  value={taskConfig.failure_policy}
                  onChange={(value) => onTaskConfigChange?.('failure_policy', value)}
                  disabled={viewOnlyMode}
                />
                <HooksField value={taskConfig.hooks} onChange={(value) => onTaskConfigChange?.('hooks', value)} disabled={viewOnlyMode} />
              </Box>
            </CollapsableCard>
          </Box>

          {/* Schema Information */}
          <Box sx={{ mt: 2, pt: 2, borderTop: `1px solid ${colors.lowestLight}` }}>
            <Typography
              variant='subtitle2'
              sx={{ mb: 1, fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}
            >
              Schema Information
            </Typography>
            {renderSchemaInfo(previousNodeOutputSchema, {
              title: 'Available Inputs',
              description: 'Output from previous node',
              formatFieldName: false,
            })}
            {renderSchemaInfo(schema?.output_schema, {
              title: 'Expected Output',
              description: 'Output fields from this task',
              number: '',
              formatFieldName: true,
            })}
          </Box>
        </Box>
      );
    };

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        {/* Tabs Header */}
        <Box sx={{ borderBottom: '1px solid var(--ds-brand-150)', px: 1, bgcolor: 'var(--ds-background-200)' }}>
          <Tabs
            id='action-sidebar-tabs'
            value={activeTab}
            onChange={(_, newValue) => setActiveTab(newValue)}
            sx={{
              minHeight: 40,
              '& .MuiTabs-indicator': {
                backgroundColor: colors.primary,
                height: 2,
              },
            }}
          >
            <Tab
              id='action-sidebar-parameters-tab'
              value='parameters'
              label='Parameters'
              sx={{
                textTransform: 'none',
                fontSize: 'var(--ds-text-body)',
                fontWeight: activeTab === 'parameters' ? 600 : 400,
                color: activeTab === 'parameters' ? colors.primary : colors.text.secondary,
                minHeight: 40,
                py: 1,
                px: 2,
                '&.Mui-selected': {
                  color: colors.primary,
                },
              }}
            />
            <Tab
              id='action-sidebar-condition-tab'
              value='condition'
              label={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                  {'Condition'}
                  {taskConfig.if && (
                    <Box
                      sx={{
                        width: 6,
                        height: 6,
                        borderRadius: '50%',
                        bgcolor: colors.primary,
                      }}
                    />
                  )}
                </Box>
              }
              data-testid='condition-tab'
              sx={{
                textTransform: 'none',
                fontSize: 'var(--ds-text-body)',
                fontWeight: activeTab === 'condition' ? 600 : 400,
                color: activeTab === 'condition' ? colors.primary : colors.text.secondary,
                minHeight: 40,
                py: 1,
                px: 2,
                '&.Mui-selected': {
                  color: colors.primary,
                },
              }}
            />
            <Tab
              id='action-sidebar-settings-tab'
              value='settings'
              label='Settings'
              sx={{
                textTransform: 'none',
                fontSize: 'var(--ds-text-body)',
                fontWeight: activeTab === 'settings' ? 600 : 400,
                color: activeTab === 'settings' ? colors.primary : colors.text.secondary,
                minHeight: 40,
                py: 1,
                px: 2,
                '&.Mui-selected': {
                  color: colors.primary,
                },
              }}
            />
          </Tabs>
        </Box>

        {/* Tab Content */}
        <Box sx={{ flex: 1, overflow: 'auto', p: 2 }}>
          {activeTab === 'parameters' && renderParametersTab()}
          {activeTab === 'condition' && renderConditionTab()}
          {activeTab === 'settings' && renderSettingsTab()}
        </Box>
      </Box>
    );
  };

  const renderWorkflowActionContent = () => {
    // Use currentTaskDefinition if available, otherwise create a minimal title/description from selectedActionType
    const description = currentTaskDefinition?.description || `Configure ${selectedActionType} task parameters`;

    return renderDynamicForm(currentTaskDefinition, '', description);
  };

  if (!open) {
    return null;
  }

  return (
    <Dialog
      open={open}
      onClose={requestClose}
      fullWidth
      maxWidth={false}
      PaperProps={{
        sx: {
          width: '95vw',
          height: '90vh',
          maxWidth: '1600px',
          maxHeight: '900px',
          borderRadius: 'var(--ds-radius-lg)',
          border: '1px solid var(--ds-brand-150)',
          overflow: 'hidden',
          boxShadow: '0 25px 50px -12px rgba(0, 0, 0, 0.25)',
        },
      }}
    >
      {/* Header */}
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          px: 3,
          py: 1.5,
          borderBottom: '1px solid var(--ds-brand-150)',
          background: 'var(--ds-background-100)',
        }}
      >
        <Box>
          <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-700)' }}>
            {`Action Details - ${
              selectedNode?.data?.label || currentTaskDefinition?.display_name || currentTaskDefinition?.description || selectedActionType
            }`}
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mt: 0.25 }}>
            {'Configure and test this automation action'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {!viewOnlyMode && isDirty && (
            <Button id='action-sidebar-save-btn' tone='primary' size='sm' icon={<Check sx={{ fontSize: 16 }} />} onClick={handleSave}>
              Save
            </Button>
          )}
          <Button
            id='action-sidebar-close-btn'
            composition='icon-only'
            tone='ghost'
            size='sm'
            aria-label='Close'
            icon={<Close sx={{ fontSize: 20, color: 'var(--ds-gray-600)' }} />}
            onClick={requestClose}
          />
        </Box>
      </Box>

      {/* Content - 3 Columns */}
      <DialogContent sx={{ p: 0, display: 'flex', height: 'calc(100% - 72px)' }}>
        {/* Left Column - Previous Actions (25%) */}
        <Box sx={{ width: '25%', minWidth: 280 }}>{renderPreviousActionsColumn()}</Box>

        {/* Middle Column - Configuration (50%) */}
        <Box sx={{ width: '50%', overflow: 'hidden', bgcolor: 'var(--ds-background-100)', display: 'flex', flexDirection: 'column' }}>
          {renderWorkflowActionContent()}
        </Box>

        {/* Right Column - Test Current Action (25%) */}
        <Box sx={{ width: '25%', minWidth: 280 }}>{renderTestCurrentActionColumn()}</Box>
      </DialogContent>

      {/* Confirm before disabling a task with downstream dependents */}
      <Modal
        open={disableConfirmOpen}
        handleClose={() => setDisableConfirmOpen(false)}
        width='xs'
        title='Disable this task?'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, p: 2 }}>
            <Button id='action-sidebar-disable-cancel-btn' tone='secondary' size='sm' onClick={() => setDisableConfirmOpen(false)}>
              Cancel
            </Button>
            <Button
              id='action-sidebar-disable-confirm-btn'
              tone='primary'
              size='sm'
              onClick={() => {
                setDisableConfirmOpen(false);
                onToggleDisable?.(true);
              }}
            >
              Disable
            </Button>
          </Box>
        }
      >
        <Box sx={{ p: 'var(--ds-space-4) 0' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondary }}>
            {directDependentsCount === 1
              ? '1 task currently depends on this one. Disabling will reroute the workflow to skip this task — its predecessors will connect directly to its successor.'
              : `${directDependentsCount} tasks currently depend on this one. Disabling will reroute the workflow to skip this task — its predecessors will connect directly to its successors.`}
          </Typography>
        </Box>
      </Modal>
    </Dialog>
  );
};

export default ActionDetailsSidebar;
