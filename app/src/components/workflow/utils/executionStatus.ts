/**
 * Shared execution-status helpers.
 *
 * These were previously duplicated across ExecutionsView, WorkflowListing and
 * WorkflowBuilderNotebook. The execution dashboard needs the same formatting,
 * so they live here rather than being copied a fourth time.
 */
import { ds } from '@utils/colors';
import type { LabelTone } from '@ui/Label';

// Statuses that indicate Temporal has finished with an execution.
const TERMINAL_EXECUTION_STATUSES = [
  'CANCELED',
  'CANCELLED',
  'COMPLETED',
  'COMPLETE',
  'COMPLETE_WITH_ERROR',
  'CONTINUED_AS_NEW',
  'FAILED',
  'TERMINATED',
  'TIMED_OUT',
];

export const isExecutionCompleted = (status?: string): boolean => !!status && TERMINAL_EXECUTION_STATUSES.includes(status.toUpperCase());

/**
 * Human duration between two ISO timestamps. Timestamps arriving without a
 * trailing `Z` are treated as UTC (Temporal omits it on some paths). An open
 * ended run is measured against now.
 */
export const getDuration = (startTime?: string, endTime?: string): string => {
  if (!startTime) {
    return 'N/A';
  }
  const start = new Date(startTime.endsWith('Z') ? startTime : startTime + 'Z');
  const end = endTime ? new Date(endTime.endsWith('Z') ? endTime : endTime + 'Z') : new Date();
  // An open-ended run is measured against the *client* clock while start_time
  // came from the server, so a client running behind would otherwise render a
  // negative duration like "-1.2s".
  const diffMs = Math.max(0, end.getTime() - start.getTime());

  if (diffMs < 1000) {
    return `${diffMs}ms`;
  } else if (diffMs < 60000) {
    return `${(diffMs / 1000).toFixed(1)}s`;
  }
  return `${(diffMs / 60000).toFixed(1)}m`;
};

/** Raw colour token — used for canvas node accents and status dots. */
export const getStatusColor = (status: string): string => {
  switch (status.toUpperCase()) {
    case 'COMPLETED':
    case 'COMPLETE':
      return ds.green[500];
    case 'COMPLETE_WITH_ERROR':
    case 'CONTINUED_AS_NEW':
      return ds.amber[400];
    case 'FAILED':
      return ds.red[600];
    case 'TERMINATED':
      return ds.red[500];
    case 'TIMED_OUT':
      return ds.amber[400];
    case 'RUNNING':
    case 'IN_PROGRESS':
    case 'SCHEDULED':
      return ds.blue[500];
    case 'CANCELED':
    case 'CANCELLED':
      return ds.gray[600];
    case 'SKIPPED':
      return ds.gray[400];
    case 'UNSPECIFIED':
    default:
      return ds.brand[200];
  }
};

/**
 * Label tone for an execution status. Label's built-in text auto-detection
 * only knows a handful of words — `running`, `canceled`, `terminated`,
 * `timed_out` and `complete_with_error` all fall through to neutral — so
 * execution statuses must pass an explicit tone.
 */
export const getStatusTone = (status: string): LabelTone => {
  switch (status.toUpperCase()) {
    case 'COMPLETED':
    case 'COMPLETE':
    case 'SUCCEEDED':
      return 'success';
    case 'FAILED':
    case 'TERMINATED':
      return 'critical';
    case 'COMPLETE_WITH_ERROR':
    case 'TIMED_OUT':
    case 'CONTINUED_AS_NEW':
      return 'warning';
    case 'RUNNING':
    case 'IN_PROGRESS':
    case 'SCHEDULED':
      return 'info';
    default:
      return 'neutral';
  }
};
