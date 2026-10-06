export type VersionStatus = 'ACTIVE' | 'PAUSED' | 'INACTIVE';

const VERSION_STATUSES: readonly string[] = ['ACTIVE', 'PAUSED', 'INACTIVE'];

// Publish and Make live both preselect the live version's status, so neither silently flips triggers.
// No live version yet: PAUSED, matching the backend's publish default (V746).
export function defaultVersionStatus(liveStatus: string | null | undefined): VersionStatus {
  return liveStatus && VERSION_STATUSES.includes(liveStatus) ? (liveStatus as VersionStatus) : 'PAUSED';
}

export const VERSION_STATUS_HELP =
  'Defaults to the live version’s current status. Active = all triggers fire. Paused = schedule, event and webhook triggers will not fire; manual runs still work. Inactive = blocked.';
