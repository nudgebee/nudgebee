// What the Real-Time Events column says about an AWS account's EventBridge
// feed. Only a received event proves the feed works: `last_event_at` is
// stamped by the collector when an event from the account arrives, while
// `connected_at` is stamped when someone clicks "Connect EventBridge" — before
// the AWS stack exists, and whether or not it is ever created.
const DAY_MS = 24 * 60 * 60 * 1000;

// How long after "Connect EventBridge" the column waits for a first event
// before saying none has come. Creating the stack takes minutes, and the rules
// forward routine API activity, so a working feed delivers well within a day.
export const FIRST_EVENT_GRACE_MS = DAY_MS;

// How long after the last event the feed still counts as active.
export const ACTIVE_WINDOW_MS = 7 * DAY_MS;

export interface EventBridgeConnection {
  connected_at?: string;
  last_event_at?: string;
}

export interface EventBridgeStatus {
  text: string;
  tone: 'success' | 'info' | 'warning';
  tooltip: string;
}

const parseTime = (value?: string): number | null => {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
};

const formatDate = (ms: number): string => new Date(ms).toLocaleString();

/**
 * The later of two timestamps. One that is missing or unreadable never wins
 * over one that can be read.
 */
export function newestTimestamp(a?: string, b?: string): string | undefined {
  const ta = parseTime(a);
  const tb = parseTime(b);
  if (tb === null) return ta === null ? undefined : a;
  if (ta === null) return b;
  return tb > ta ? b : a;
}

/**
 * Status of the account's EventBridge feed from connection_status.eventbridge of
 * its AWS agent; null when EventBridge was never connected and no event was ever
 * received.
 */
export function eventBridgeStatus(eventbridge: EventBridgeConnection | undefined | null, now: number = Date.now()): EventBridgeStatus | null {
  const lastEventAt = parseTime(eventbridge?.last_event_at);
  const connectedAt = parseTime(eventbridge?.connected_at);

  if (lastEventAt !== null) {
    if (now - lastEventAt <= ACTIVE_WINDOW_MS) {
      return { text: 'active', tone: 'success', tooltip: `Last event received ${formatDate(lastEventAt)}` };
    }
    return {
      text: 'no recent events',
      tone: 'warning',
      tooltip: `No event received since ${formatDate(lastEventAt)}. Check that the Nudgebee EventBridge stack still exists in this AWS account.`,
    };
  }
  if (connectedAt === null) {
    return null;
  }
  if (now - connectedAt < FIRST_EVENT_GRACE_MS) {
    return {
      text: 'waiting for events',
      tone: 'info',
      tooltip: `Connect EventBridge was started ${formatDate(connectedAt)}. The first event shows here once the stack is created in AWS.`,
    };
  }
  return {
    text: 'no events received',
    tone: 'warning',
    tooltip: `Connect EventBridge was started ${formatDate(
      connectedAt
    )}, and no event has arrived since. The CloudFormation stack may not have been created, or it is not delivering.`,
  };
}
