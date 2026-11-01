import { getCurrentTenant } from '@lib/auth';

/**
 * A Kubernetes account that was created in the UI but whose agent has never
 * reported in — i.e. the user closed the install step without running the
 * install command. The account row exists, the agent table has nothing.
 *
 * Held per tenant in localStorage so the reminder banner survives navigation
 * and reloads, and a re-login as another tenant reads a different key entirely.
 */
/**
 * What the user still has to do. 'k8s' means run the install command from the
 * add-cluster modal; 'vm' means add a VM Agent integration, which is where a
 * self-hosted account's install command actually comes from. Different next
 * step, so the reminder has to say something different.
 */
export type PendingAgentKind = 'k8s' | 'vm';

export interface PendingAgentInstall {
  id: string;
  name: string;
  kind: PendingAgentKind;
  /** Unix ms the entry was recorded. Entries expire, so a deleted account can't strand the banner forever. */
  at: number;
  /** Unix ms until which the user asked not to be reminded. The entry stays tracked (and polled) meanwhile. */
  snoozedUntil?: number;
}

/** Same-tab notification — the `storage` event only fires in *other* tabs. */
export const PENDING_AGENT_INSTALL_EVENT = 'nudgebee:pending-agent-install';

/**
 * The only agent state that proves the install command was actually run. An
 * agent ROW exists from the moment the account is created, so its presence says
 * nothing — see the Agent Health page, which reads the same field.
 */
export const AGENT_CONNECTED = 'CONNECTED';

const TTL_MS = 7 * 24 * 60 * 60 * 1000;

/** How long a dismissed reminder stays hidden before it comes back. */
export const SNOOZE_MS = 24 * 60 * 60 * 1000;

/** True while the user's dismissal is still in effect for this entry. */
export const isPendingAgentInstallSnoozed = (entry: PendingAgentInstall, now: number = Date.now()): boolean =>
  typeof entry.snoozedUntil === 'number' && entry.snoozedUntil > now;

/**
 * The localStorage key this store writes to, exported so a `storage` listener
 * can filter on it without re-deriving the string (and drifting from it).
 */
export const pendingAgentInstallKey = (): string => {
  const tenant = getCurrentTenant();
  return `nudgebee_pending_agent_install:${tenant?.id || tenant?.name || ''}`;
};

const keyForTenant = pendingAgentInstallKey;

const notify = () => {
  window.dispatchEvent(new Event(PENDING_AGENT_INSTALL_EVENT));
};

export const readPendingAgentInstalls = (): PendingAgentInstall[] => {
  if (typeof window === 'undefined') {
    return [];
  }
  try {
    const raw = localStorage.getItem(keyForTenant());
    const list = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(list)) {
      return [];
    }
    const now = Date.now();
    return (
      list
        .filter((entry: PendingAgentInstall) => entry?.id && now - (entry.at || 0) < TTL_MS)
        // Entries written before VM accounts were tracked carry no kind; they are
        // all clusters.
        .map((entry: PendingAgentInstall) => ({ ...entry, kind: entry.kind || 'k8s' }))
    );
  } catch {
    return [];
  }
};

const write = (list: PendingAgentInstall[]): void => {
  try {
    localStorage.setItem(keyForTenant(), JSON.stringify(list));
    notify();
  } catch {
    // Private mode / quota — a lost reminder is acceptable; the account is still created.
  }
};

export const addPendingAgentInstall = (id: string, name: string, kind: PendingAgentKind = 'k8s'): void => {
  if (typeof window === 'undefined' || !id) {
    return;
  }
  const list = readPendingAgentInstalls().filter((entry) => entry.id !== id);
  write([...list, { id, name, kind, at: Date.now() }]);
};

/**
 * Hides the reminder for the given accounts for SNOOZE_MS. The entries are kept,
 * not dropped: the agent-health poll keeps running against them, so one that
 * connects during the snooze is cleared for good rather than resurfacing.
 */
export const snoozePendingAgentInstalls = (ids: string[]): void => {
  if (typeof window === 'undefined' || ids.length === 0) {
    return;
  }
  const snoozedUntil = Date.now() + SNOOZE_MS;
  write(readPendingAgentInstalls().map((entry) => (ids.includes(entry.id) ? { ...entry, snoozedUntil } : entry)));
};

/** Drops the given accounts. Called once their agent shows up in the agent table. */
export const clearPendingAgentInstalls = (ids: string[]): void => {
  if (typeof window === 'undefined' || ids.length === 0) {
    return;
  }
  const list = readPendingAgentInstalls();
  const remaining = list.filter((entry) => !ids.includes(entry.id));
  if (remaining.length !== list.length) {
    write(remaining);
  }
};
