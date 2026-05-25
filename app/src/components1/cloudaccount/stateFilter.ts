/**
 * Native cloud-provider state filter helpers.
 *
 * The DB `status` column only stores `Active` / `Inactive`. Detailed native states
 * (running / stopped / terminated / deallocated / Online / RUNNABLE etc.) live inside
 * the `meta` JSON field at a service-specific path, and are filtered via Hasura's
 * `meta: { _contains: "..." }` operator.
 */

export const META_STATE_KEY: Record<string, string[]> = {
  AmazonEC2: ['State', 'Name'],
  AmazonRDS: ['DBInstanceStatus'],
  'Compute Engine': ['status'],
  'Cloud SQL': ['state'],
  'microsoft.compute/virtualmachines': ['powerState'],
  'microsoft.sql/servers': ['properties', 'status'],
  'microsoft.sql/managedinstances': ['properties', 'state'],
  'microsoft.dbforpostgresql/flexibleservers': ['properties', 'state'],
};

const NATIVE_STATE_OPTIONS: Record<string, string[]> = {
  AmazonEC2: ['running', 'stopped', 'terminated', 'shutting-down', 'pending'],
  AmazonRDS: ['available', 'deleting', 'backing-up', 'modifying', 'configuring-enhanced-monitoring', 'inaccessible-encryption-credentials'],
  'Compute Engine': ['RUNNING', 'TERMINATED', 'STOPPING', 'STAGING'],
  'Cloud SQL': ['RUNNABLE'],
  'microsoft.compute/virtualmachines': ['running', 'deallocated', 'stopped'],
  'microsoft.sql/servers': ['Online', 'Paused'],
  'microsoft.sql/managedinstances': ['Ready', 'Stopped', 'Stopping', 'Starting', 'Provisioning'],
  'microsoft.dbforpostgresql/flexibleservers': ['Ready'],
};

const ALL_OPTION = { label: 'All', value: 'all' };
const FALLBACK_OPTIONS = [
  { label: 'Active', value: 'Active' },
  { label: 'Inactive', value: 'Inactive' },
];

export type StateOption = { label: string; value: string };

export function hasNativeStates(serviceName: string | undefined): boolean {
  return !!(serviceName && META_STATE_KEY[serviceName]);
}

// Format raw provider state (e.g. "running", "RUNNING", "shutting-down", "backing-up")
// into a consistent Title Case label for display ("Running", "Shutting Down", "Backing Up").
// The underlying value is preserved unchanged for the API filter to match.
function toTitleCase(raw: string): string {
  return raw
    .toLowerCase()
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}

export function getStateDropdownOptions(serviceName: string | undefined): StateOption[] {
  if (!serviceName) return [ALL_OPTION, ...FALLBACK_OPTIONS];
  const native = NATIVE_STATE_OPTIONS[serviceName];
  if (native) {
    return [ALL_OPTION, ...native.map((s) => ({ label: toTitleCase(s), value: s }))];
  }
  return [ALL_OPTION, ...FALLBACK_OPTIONS];
}

// Native states that indicate the resource is up/healthy — rendered as a green chip.
const HEALTHY_STATES = new Set(['running', 'active', 'available', 'online', 'runnable', 'ready']);

export function getStateColor(state: string | null | undefined): '' | 'green' {
  if (!state) return '';
  return HEALTHY_STATES.has(state.toLowerCase()) ? 'green' : '';
}

export function getInstanceState(serviceName: string | undefined, meta: any): string | null {
  if (!serviceName) return null;
  const keys = META_STATE_KEY[serviceName];
  if (!keys || !meta) return null;
  let v: any = meta;
  for (const k of keys) {
    if (v == null || typeof v !== 'object') return null;
    v = v[k];
  }
  return typeof v === 'string' ? v : null;
}

/**
 * Build the JSON blob for Hasura `meta: { _contains: <json> }`, nested per service.
 * Example: AmazonEC2 + "running" → `{"State":{"Name":"running"}}`.
 */
export function buildStateFilter(serviceName: string | undefined, stateValue: string): string | null {
  if (!serviceName) return null;
  const keys = META_STATE_KEY[serviceName];
  if (!keys) return null;
  let obj: any = stateValue;
  for (let i = keys.length - 1; i >= 0; i--) {
    obj = { [keys[i]]: obj };
  }
  return JSON.stringify(obj);
}

/**
 * Build the API params for `getCloudResource` given the current dropdown selection
 * and the service. Returns either `metaStateFilter` (native states) or `status`
 * (fallback Active/Inactive), or neither (show all).
 */
export function buildStateApiParams(serviceName: string | undefined, selected: string): { metaStateFilter?: string; status?: string | string[] } {
  if (!selected || selected === 'all') {
    // Show all — no status/state filter. Explicitly pass both Active and Inactive
    // so we override the API's default "Active only" behavior.
    return { status: ['Active', 'Inactive'] };
  }
  if (hasNativeStates(serviceName)) {
    const metaFilter = buildStateFilter(serviceName, selected);
    // Native state match — also include Inactive records so terminated/stopped appear.
    return { metaStateFilter: metaFilter || undefined, status: ['Active', 'Inactive'] };
  }
  return { status: selected };
}
