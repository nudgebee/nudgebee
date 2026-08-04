/** Shared constants for the cross-automation execution dashboard. */

/** Statuses Temporal reports for a run. Kept in the order users scan for. */
export const EXECUTION_STATUS_OPTIONS = [
  { value: 'RUNNING', label: 'Running' },
  { value: 'COMPLETED', label: 'Completed' },
  { value: 'FAILED', label: 'Failed' },
  { value: 'TIMED_OUT', label: 'Timed Out' },
  { value: 'TERMINATED', label: 'Terminated' },
  { value: 'CANCELED', label: 'Canceled' },
  { value: 'CONTINUED_AS_NEW', label: 'Continued As New' },
];

export const TABLE_HEADERS = [
  { name: 'Date/Time', width: '12%' },
  { name: 'Automation', width: '21%' },
  // A page spans accounts now, and automation names are only unique within
  // one — without this column two rows can read identically.
  { name: 'Account', width: '12%' },
  { name: 'Status', width: '10%' },
  // Carries the trigger as a second line: how a run started is only ever read
  // together with who started it, and it does not earn a column of its own.
  { name: 'User', width: '13%' },
  { name: 'Duration', width: '8%' },
  // Failures only. A success has no error, and putting the trigger type here
  // as a stand-in made the column read as two unrelated things.
  'Error',
];

/**
 * Fallback page size. The table actually starts at the user's saved
 * preference (`apiUser.getUserPreferencesTablePageSize()`, the same one every
 * other table reads); this only applies when nothing is stored yet.
 */
export const DEFAULT_PAGE_SIZE = 20;

/**
 * How far back the table looks by default. Mirrors the deployed Temporal
 * namespace retention so an unfiltered table lines up with the summary header,
 * which always covers the whole retention window. The date picker's floor still
 * comes from the live `retention_days` on the aggregate response, not from here.
 */
export const DEFAULT_RANGE_DAYS = 10;

/**
 * Table refresh cadence while a run is in flight. Deliberately slower than the
 * 4s used inside a single automation's execution view — this page watches every
 * automation at once and a stale row costs nothing.
 */
export const DASHBOARD_POLL_INTERVAL_MS = 15000;

/** Cap on the automations pulled for the Automation filter's options. */
export const AUTOMATION_OPTIONS_LIMIT = 500;

export const TOP_FAILED_LIMIT = 5;

/**
 * The nil UUID the backend stamps on runs no human started (schedules, webhook
 * fan-out). It has no row in `users`, so without this it renders raw.
 */
export const SYSTEM_USER_ID = '00000000-0000-0000-0000-000000000000';

/** Shown in place of a user for runs that were not started by a person. */
export const SYSTEM_USER_LABEL = 'System';

/** Resolved display name for an execution's "User" column. */
export const executionUserLabel = (userName?: string, triggeredBy?: string): string => {
  if (userName) return userName;
  if (!triggeredBy || triggeredBy === SYSTEM_USER_ID) return SYSTEM_USER_LABEL;
  // A real user id whose display name could not be resolved — show the id
  // rather than pretending the run was automated.
  return triggeredBy;
};
