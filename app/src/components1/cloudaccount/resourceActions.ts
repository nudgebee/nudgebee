import React from 'react';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import StopIcon from '@mui/icons-material/Stop';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import RefreshIcon from '@mui/icons-material/Refresh';
import TuneIcon from '@mui/icons-material/Tune';
import TerminalIcon from '@mui/icons-material/Terminal';

export type ConfirmationType = 'standard' | 'strict';

export interface ResourceAction {
  id: string;
  command: string;
  label: string;
  reactIcon: React.ReactElement;
  confirmationType: ConfirmationType;
  confirmationMessage: string;
  destructive: boolean;
  isAvailable: (state: string) => boolean;
  requiresArgs?: boolean;
  argsConfig?: { field: string; label: string; type: 'number' | 'text' }[];
  // For actions that need to invoke a different cloud-collector service than
  // the row's service_name (e.g. SSM Run Command on an EC2 row is dispatched
  // to "AWSSystemsManager", not "AmazonEC2").
  serviceNameOverride?: string;
  // Marks actions that render a bespoke dialog instead of ConfirmActionDialog.
  // The consumer component is responsible for rendering the matching dialog.
  customDialog?: 'ssm_run_command';
  // Args derived from the resource row itself (not from user input). The
  // backend needs these as part of `args` but they aren't filled by the
  // confirmation dialog. Returned values are merged BEFORE user-supplied
  // args, so the user can override if needed.
  extraArgs?: (resource: any) => Record<string, any>;
}

function normalizeState(state: string): string {
  return (state || '').toLowerCase().trim();
}

// Running states across AWS EC2 ("running"), Azure VM ("running"), GCP CE ("RUNNING"),
// and the generic "active" status fallback.
const VM_RUNNING_STATES = new Set(['running', 'active']);
// Stopped states across AWS EC2 ("stopped", "terminated" for shut-down),
// Azure VM ("deallocated", "stopped"), GCP CE ("TERMINATED"). Lowercased.
const VM_STOPPED_STATES = new Set(['stopped', 'deallocated', 'terminated']);

export const EC2_ACTIONS: ResourceAction[] = [
  {
    id: 'start',
    command: 'start',
    label: 'Start Instance',
    reactIcon: React.createElement(PlayArrowIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to start this instance?',
    destructive: false,
    isAvailable: (state) => VM_STOPPED_STATES.has(normalizeState(state)),
  },
  {
    id: 'stop',
    command: 'stop',
    label: 'Stop Instance',
    reactIcon: React.createElement(StopIcon, { fontSize: 'small' }),
    confirmationType: 'strict',
    confirmationMessage: 'This will stop the instance. All unsaved data may be lost.',
    destructive: true,
    isAvailable: (state) => VM_RUNNING_STATES.has(normalizeState(state)),
  },
  {
    id: 'reboot',
    command: 'reboot',
    label: 'Reboot Instance',
    reactIcon: React.createElement(RestartAltIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to reboot this instance? It will be temporarily unavailable.',
    destructive: false,
    isAvailable: (state) => VM_RUNNING_STATES.has(normalizeState(state)),
  },
];

// AWS-only: SSM Run Command on EC2 instances. Dispatched to the Systems Manager
// service, not EC2 itself. Opens RunSsmCommandDialog for template + parameter input.
export const RUN_SSM_COMMAND_ACTION: ResourceAction = {
  id: 'run_command',
  command: 'run_command',
  label: 'Run Command...',
  reactIcon: React.createElement(TerminalIcon, { fontSize: 'small' }),
  confirmationType: 'standard',
  confirmationMessage: '',
  destructive: false,
  // Available on any running EC2 (SSM agent reachability is verified server-side).
  isAvailable: (state) => VM_RUNNING_STATES.has(normalizeState(state)),
  serviceNameOverride: 'AWSSystemsManager',
  customDialog: 'ssm_run_command',
};

export const RDS_ACTIONS: ResourceAction[] = [
  {
    id: 'start',
    command: 'start',
    label: 'Start Instance',
    reactIcon: React.createElement(PlayArrowIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to start this database instance?',
    destructive: false,
    isAvailable: (state) => normalizeState(state) === 'stopped',
  },
  {
    id: 'stop',
    command: 'stop',
    label: 'Stop Instance',
    reactIcon: React.createElement(StopIcon, { fontSize: 'small' }),
    confirmationType: 'strict',
    confirmationMessage: 'This will stop the database instance. Active connections will be dropped.',
    destructive: true,
    isAvailable: (state) => ['available', 'running'].includes(normalizeState(state)),
  },
  {
    id: 'reboot',
    command: 'reboot',
    label: 'Reboot Instance',
    reactIcon: React.createElement(RestartAltIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to reboot this database? Active connections may be dropped.',
    destructive: false,
    isAvailable: (state) => ['available', 'running'].includes(normalizeState(state)),
  },
];

// GCP Cloud SQL uses `restart` (not `reboot`) and its native states are
// RUNNABLE / SUSPENDED. Reusing RDS_ACTIONS produced always-disabled buttons.
export const GCP_CLOUDSQL_ACTIONS: ResourceAction[] = [
  {
    id: 'start',
    command: 'start',
    label: 'Start Instance',
    reactIcon: React.createElement(PlayArrowIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to start this Cloud SQL instance?',
    destructive: false,
    isAvailable: (state) => ['suspended', 'stopped'].includes(normalizeState(state)),
  },
  {
    id: 'stop',
    command: 'stop',
    label: 'Stop Instance',
    reactIcon: React.createElement(StopIcon, { fontSize: 'small' }),
    confirmationType: 'strict',
    confirmationMessage: 'This will stop the Cloud SQL instance. Active connections will be dropped.',
    destructive: true,
    isAvailable: (state) => normalizeState(state) === 'runnable',
  },
  {
    id: 'restart',
    command: 'restart',
    label: 'Restart Instance',
    reactIcon: React.createElement(RestartAltIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to restart this Cloud SQL instance? Active connections may be dropped.',
    destructive: false,
    isAvailable: (state) => normalizeState(state) === 'runnable',
  },
];

// Azure SQL Database has distinct pause/resume semantics (not start/stop).
// Backend cases: pause / resume. Native states: Online / Paused.
export const AZURE_SQL_ACTIONS: ResourceAction[] = [
  {
    id: 'resume',
    command: 'resume',
    label: 'Resume Database',
    reactIcon: React.createElement(PlayArrowIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to resume this database? Billing will resume.',
    destructive: false,
    isAvailable: (state) => normalizeState(state) === 'paused',
  },
  {
    id: 'pause',
    command: 'pause',
    label: 'Pause Database',
    reactIcon: React.createElement(StopIcon, { fontSize: 'small' }),
    confirmationType: 'strict',
    confirmationMessage: 'This will pause the database. Active connections will be dropped and compute billing will stop.',
    destructive: true,
    isAvailable: (state) => normalizeState(state) === 'online',
  },
];

// Backend (aws_ecs.go ApplyCommand) requires `args.cluster` on every ECS
// service command. Pull it from the row's meta.ClusterName for all three.
const ecsClusterArgs = (resource: any): Record<string, any> => ({
  cluster: resource?.meta?.ClusterName ?? '',
});

export const ECS_SERVICE_ACTIONS: ResourceAction[] = [
  {
    id: 'redeploy',
    command: 'redeploy',
    label: 'Redeploy Service',
    reactIcon: React.createElement(RefreshIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'This will trigger a rolling deployment. Continue?',
    destructive: false,
    isAvailable: (state) => normalizeState(state) === 'active',
    extraArgs: ecsClusterArgs,
  },
  {
    id: 'force_redeploy',
    command: 'force_redeploy',
    label: 'Force Redeploy',
    reactIcon: React.createElement(RefreshIcon, { fontSize: 'small' }),
    confirmationType: 'strict',
    confirmationMessage: 'This will force a new deployment, replacing all running tasks.',
    destructive: true,
    isAvailable: (state) => normalizeState(state) === 'active',
    extraArgs: ecsClusterArgs,
  },
  {
    id: 'scale',
    command: 'scale',
    label: 'Scale Service',
    reactIcon: React.createElement(TuneIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Set the new desired task count for this service.',
    destructive: false,
    isAvailable: (state) => normalizeState(state) === 'active',
    requiresArgs: true,
    argsConfig: [{ field: 'desired_count', label: 'Desired tasks', type: 'number' }],
    extraArgs: ecsClusterArgs,
  },
];

// Map canonical service_name (matches META_STATE_KEY in stateFilter.ts) to its
// action catalog. Substring matching is intentionally avoided — AWS RDS,
// Azure SQL, and GCP Cloud SQL all match `.includes('sql')` but require
// different commands and state vocabularies.
const ACTION_CATALOGS: Record<string, ResourceAction[]> = {
  AmazonEC2: [...EC2_ACTIONS, RUN_SSM_COMMAND_ACTION],
  'Compute Engine': EC2_ACTIONS,
  'microsoft.compute/virtualmachines': EC2_ACTIONS,
  AmazonRDS: RDS_ACTIONS,
  'Cloud SQL': GCP_CLOUDSQL_ACTIONS,
  'microsoft.sql/servers': AZURE_SQL_ACTIONS,
};

export function getActionsForService(serviceName: string, resourceType?: string): ResourceAction[] {
  // ECS is the only resource_type-discriminated catalog: services get actions,
  // tasks/clusters don't.
  if ((serviceName || '').toLowerCase().includes('ecs')) {
    if (resourceType === 'service') {
      return ECS_SERVICE_ACTIONS;
    }
    return [];
  }
  return ACTION_CATALOGS[serviceName] ?? [];
}

export function buildMenuItems(
  actions: ResourceAction[],
  resourceState: string,
  hasWrite: boolean
): { id: string; label: string; reactIcon: React.ReactElement; disabled: boolean }[] {
  return actions.map((action) => ({
    id: action.id,
    label: action.label,
    reactIcon: action.reactIcon,
    disabled: !hasWrite || !action.isAvailable(resourceState),
  }));
}
