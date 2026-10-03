// Whether an event is still going on, has ended, or came back.
//
// `status` is the source's lifecycle, not the triage status: FIRING until the source reports an end,
// then CLOSED (the source ended it, or the inactive-resource sweep closed it) or RESOLVED (a
// remediation applied, or a source that says "resolved"). Both of the latter mean "not firing".
//
// FIRING is not the same as "still happening". One-time signals — an OOM kill, a crash — are never
// reported as ended by their source; they sit in FIRING until something closes them. So an open
// event is described as "no end reported", and whether the problem is actually still occurring is
// answered by recurrence: newer events with the same fingerprint.

import { formatClockCompact, formatTooltip, parseDateValue } from '@shared/format/Datetime';

const SEC = 1000;
const MIN = 60 * SEC;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

const ENDED_STATUSES = ['CLOSED', 'RESOLVED'];

export const OPEN_EVENT_HINT =
  'The source has not reported this as ended. One-time signals such as an OOM kill or a crash are never reported as ended and stay open until they are closed automatically, so check whether it recurred to know if it is still happening.';

/** Compact duration: 45s, 12m, 2h 46m, 3d 4h. */
export function formatDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < MIN) return `${Math.max(1, Math.round(ms / SEC))}s`;
  if (ms < HOUR) return `${Math.floor(ms / MIN)}m`;
  if (ms < DAY) {
    const h = Math.floor(ms / HOUR);
    const m = Math.floor((ms % HOUR) / MIN);
    return m ? `${h}h ${m}m` : `${h}h`;
  }
  const d = Math.floor(ms / DAY);
  const h = Math.floor((ms % DAY) / HOUR);
  return h ? `${d}d ${h}h` : `${d}d`;
}

// Same "HH:MM TZ, DD-Mon-YYYY" form as the app's other timestamp tooltips.
const formatClock = (ms) => formatTooltip(new Date(ms));

const toMs = (value) => {
  if (!value) return NaN;
  return parseDateValue(value).getTime();
};

/**
 * The moment the sidebar's "When" row shows: when the event started, or, when the source gave no
 * start (an automation's Store Event step with Starts At left empty), when it was recorded. These
 * are the same two fields the Events list takes its time from, so the row is never blank for an
 * event the list shows a time for.
 */
export const eventWhen = (row) => row?.starts_at || row?.created_at || null;

/**
 * Describe the event's own lifecycle state.
 *
 * @returns {null | { state: 'open'|'ended', label: string, tone: string, detail: string, hint?: string, endedAgo?: string }}
 *   null when the status is missing or unknown — say nothing rather than guess.
 */
export function describeEventState({ status, startsAt, endsAt, now = Date.now() }) {
  const s = String(status || '').toUpperCase();
  const startMs = toMs(startsAt);
  const endMs = toMs(endsAt);

  // The start time is already on the "When" row, so the detail only adds what that row can't say.
  if (s === 'FIRING') {
    return { state: 'open', label: 'Open', tone: 'critical', detail: 'no end reported', hint: OPEN_EVENT_HINT };
  }

  if (ENDED_STATUSES.includes(s)) {
    if (!Number.isFinite(endMs)) {
      return { state: 'ended', label: 'Ended', tone: 'success', detail: 'end time unknown' };
    }
    const endedAgo = `${formatDuration(now - endMs)} ago`;
    // A one-time signal is stored with ends_at == starts_at; "lasted 1s" would be noise.
    const hasDuration = Number.isFinite(startMs) && endMs - startMs >= SEC;
    const detail = hasDuration ? `lasted ${formatDuration(endMs - startMs)}` : '';
    // Exact times behind the relative ones, so "lasted 5m" can be checked against the alert.
    const hint = hasDuration ? `Started ${formatClock(startMs)}, ended ${formatClock(endMs)}` : `Ended ${formatClock(endMs)}`;
    return { state: 'ended', label: 'Ended', tone: 'success', detail, endedAgo, hint };
  }

  return null;
}

/**
 * Place this event in the run of firings of the same problem.
 *
 * Counted over ONE population — events with the same fingerprint, before and
 * after this one — so the position and the total mean the same thing. The dedup
 * chain's own occurrence_number cannot be mixed in: it resets to 1 whenever the
 * chain restarts (resolved, or quiet past the source's dedup window), so
 * "3rd of 6" built from both numbers would be wrong on exactly the alerts that
 * repeat.
 *
 * @param {object} input
 * @param {number} input.before  same fingerprint up to and including this event
 * @param {number} input.after   same fingerprint after it
 * @param {object} [input.first] the oldest one seen: { id, starts_at }
 * @param {object} [input.latest] the newest one seen: { id, starts_at }
 * @returns {null | { text: string, lastSeen: string, firstId?: string, latestId?: string, total: number }}
 */
export function describeOccurrence({ before, after, first, latest, now = Date.now() }) {
  const position = Number(before) || 0;
  const total = position + (Number(after) || 0);
  // position counts this event, so 0 means the lookup failed rather than "never
  // happened". One occurrence is not a pattern and gets no row.
  if (position <= 0 || total <= 1) return null;
  // Short enough for the sidebar's value column: the chip carries the count and
  // the day it started; how long ago the latest one was sits beside it and wraps.
  const firstMs = toMs(first?.starts_at);
  const since = Number.isFinite(firstMs) ? ` since ${formatClockCompact(firstMs).replace(/ \d{2}:\d{2}$/, '')}` : '';
  const latestMs = toMs(latest?.starts_at);
  const hasLater = Number.isFinite(latestMs) && latest?.id && String(latest.id) !== String(first?.id);
  return {
    text: `${ordinal(position)} of ${total}${since}`,
    lastSeen: hasLater ? `${formatDuration(now - latestMs)} ago` : '',
    firstId: first?.id ? String(first.id) : undefined,
    latestId: latest?.id ? String(latest.id) : undefined,
    total,
  };
}

function ordinal(n) {
  const rest = n % 100;
  if (rest >= 11 && rest <= 13) return `${n}th`;
  return `${n}${{ 1: 'st', 2: 'nd', 3: 'rd' }[n % 10] || 'th'}`;
}
