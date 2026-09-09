// Closing the loop on a remediation.
//
// A resolution's status says the ACTION ran, never that the PROBLEM stopped. A revert can apply
// cleanly and the pod keeps OOMing; a PR can be raised and never merged. The only evidence available
// without asking a human is whether the same problem fired again afterwards — so that is what we
// report, and we are careful to report it as correlation rather than proof.
//
// Silence is judged against the problem's OWN cadence. Two hours of quiet means nothing for
// something that fires every seven hours, and everything for something that fires every ten minutes.

/** Below this many prior occurrences there is no usable cadence to compare against. */
const MIN_BASELINE_OCCURRENCES = 2;

/** Silence must outlast this multiple of the typical interval before it counts as "held". */
const HELD_INTERVAL_MULTIPLE = 2;

const HOUR_MS = 60 * 60 * 1000;

const formatHours = (hours) => {
  if (hours < 1) return `${Math.max(1, Math.round(hours * 60))}m`;
  if (hours < 48) return `${Math.round(hours)}h`;
  return `${Math.round(hours / 24)}d`;
};

/**
 * Describe what happened to the problem after an action ran.
 *
 * @param {object}  input
 * @param {number}  input.recurrencesSince   occurrences of this fingerprint after the action
 * @param {number}  input.baselineCount      occurrences in the window BEFORE the action
 * @param {number}  input.baselineWindowMs   length of that window
 * @param {number}  input.actedAtMs          when the action settled
 * @param {number}  input.now                injectable clock
 * @returns {{text: string, tone: 'success'|'warning'|'neutral'}|null}
 */
export const describeRecurrence = ({ recurrencesSince, baselineCount, baselineWindowMs, actedAtMs, now = Date.now() }) => {
  if (!Number.isFinite(recurrencesSince) || !Number.isFinite(actedAtMs)) return null;

  const quietHours = Math.max(0, (now - actedAtMs) / HOUR_MS);

  // It came back. That is the one unambiguous signal here, and the most useful: an action that ran
  // successfully and did not stop the problem is a mitigation at best.
  if (recurrencesSince > 0) {
    return {
      tone: 'warning',
      text: `Recurred ${recurrencesSince} ${recurrencesSince === 1 ? 'time' : 'times'} since — this did not stop it`,
    };
  }

  const usableBaseline = Number.isFinite(baselineCount) && baselineCount >= MIN_BASELINE_OCCURRENCES && baselineWindowMs > 0;
  if (!usableBaseline) {
    // Without a cadence, silence is not evidence — say so rather than implying success.
    return { tone: 'neutral', text: `No recurrence in ${formatHours(quietHours)} — too little history to compare` };
  }

  const typicalIntervalHours = baselineWindowMs / HOUR_MS / baselineCount;

  // Held long enough to mean something.
  if (quietHours >= typicalIntervalHours * HELD_INTERVAL_MULTIPLE) {
    return {
      tone: 'success',
      text: `No recurrence in ${formatHours(quietHours)} — was firing about every ${formatHours(typicalIntervalHours)}`,
    };
  }

  // Quiet, but not yet longer than it is normally quiet anyway.
  return {
    tone: 'neutral',
    text: `Quiet ${formatHours(quietHours)} — normally recurs about every ${formatHours(typicalIntervalHours)}, too early to tell`,
  };
};

export const RECURRENCE_BASELINE_WINDOW_MS = 7 * 24 * HOUR_MS;
