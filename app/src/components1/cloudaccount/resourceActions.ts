import React from 'react';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import StopIcon from '@mui/icons-material/Stop';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import RefreshIcon from '@mui/icons-material/Refresh';

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
}

function normalizeState(state: string): string {
  return (state || '').toLowerCase().trim();
}

export const EC2_ACTIONS: ResourceAction[] = [
  {
    id: 'start',
    command: 'start',
    label: 'Start Instance',
    reactIcon: React.createElement(PlayArrowIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to start this instance?',
    destructive: false,
    // GCP Compute Engine reports the stopped state as "TERMINATED"
    // (see stateFilter.ts NATIVE_STATE_OPTIONS).
    isAvailable: (state) => ['stopped', 'deallocated', 'terminated'].includes(normalizeState(state)),
  },
  {
    id: 'stop',
    command: 'stop',
    label: 'Stop Instance',
    reactIcon: React.createElement(StopIcon, { fontSize: 'small' }),
    confirmationType: 'strict',
    confirmationMessage: 'This will stop the instance. All unsaved data may be lost.',
    destructive: true,
    isAvailable: (state) => ['running', 'active'].includes(normalizeState(state)),
  },
  {
    id: 'reboot',
    command: 'reboot',
    label: 'Reboot Instance',
    reactIcon: React.createElement(RestartAltIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to reboot this instance? It will be temporarily unavailable.',
    destructive: false,
    isAvailable: (state) => ['running', 'active'].includes(normalizeState(state)),
  },
];

export const RDS_ACTIONS: ResourceAction[] = [
  {
    id: 'start',
    command: 'start',
    label: 'Start Instance',
    reactIcon: React.createElement(PlayArrowIcon, { fontSize: 'small' }),
    confirmationType: 'standard',
    confirmationMessage: 'Are you sure you want to start this database instance?',
    destructive: false,
    isAvailable: (state) => ['stopped'].includes(normalizeState(state)),
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
  },
];

export function getActionsForService(serviceName: string, resourceType?: string): ResourceAction[] {
  const sn = (serviceName || '').toLowerCase();
  if (sn.includes('ecs')) {
    if (resourceType === 'service') return ECS_SERVICE_ACTIONS;
    return [];
  }
  if (sn.includes('rds') || sn.includes('sql') || sn.includes('cloudsql')) {
    return RDS_ACTIONS;
  }
  return EC2_ACTIONS;
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
