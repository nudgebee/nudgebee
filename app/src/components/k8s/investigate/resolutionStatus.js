import { safeJSONParse } from 'src/utils/common';
import { parseDateValue } from '@shared/format/Datetime';

// One status vocabulary for every remediation path on an event.
//
// The stored values differ by which system ran the fix: the card/agent_task path writes
// InProgress/Success/Failed, the remediation panel writes SUCCESS/FAILED/RUNNING/INCONCLUSIVE, and
// agents write PullRequest lifecycles. An operator should read one set of words regardless, so the
// mapping lives here rather than being re-derived at each call site.

// A run still InProgress after this long gets a "taking longer than expected" note. Retry is offered
// on Failed only, so without it a task that never reports back leaves no affordance at all — the
// state every revert sat in while the agent was rejecting the payload.
export const RESOLUTION_SLOW_AFTER_MS = 2 * 60 * 1000;

/**
 * Map a stored resolution onto the words shown to the operator.
 *
 * @param {object} resolution  row from listEventResolutions
 * @param {number} now         injectable clock, so the slow-run threshold is testable
 * @returns {{label: string, tone: string, actor: string, isSlow: boolean}}
 */
// readData is a stored resolution row's data, parsed: jsonb arrives parsed or as a string depending on the caller.
export const readData = (resolution) => (typeof resolution?.data === 'string' ? safeJSONParse(resolution.data) : resolution?.data) || {};

const readVerify = (resolution) => readData(resolution).verify ?? null;

/**
 * Whether a remediation run is a fix NudgeBee saw work: it ran and reported exit code 0, or the automation
 * it ran completed (not: no exit code, unknown outcome, operator-marked). Mirrors llm-server verifiedSuccess.
 * @param {object} resolution
 */
export const verifiedSuccess = (resolution) => {
  const data = readData(resolution);
  return resolution?.status === 'Success' && (data.exit_code_reported === true || data.automation_completed === true) && data.manual !== true;
};

export const describeResolution = (resolution, now = Date.now()) => {
  const status = resolution?.status;
  const startedAt = resolution?.updated_at || resolution?.created_at;
  const startedMs = startedAt ? new Date(startedAt).getTime() : NaN;
  const isRunning = status !== 'Success' && status !== 'Failed' && status !== 'Unknown' && status !== 'Declined';
  const isSlow = isRunning && Number.isFinite(startedMs) && now - startedMs > RESOLUTION_SLOW_AFTER_MS;

  // resolver_type distinguishes a person from Nubi or an automation acting on its own. For a person
  // the display name answers "who", which is the question actually asked; for the others the type is
  // the more useful label.
  const actor = resolution?.resolver_type === 'User' ? resolution?.resolver_display_name || 'a user' : resolution?.resolver_type || '';

  // A person declined the proposed step: nothing ran.
  if (status === 'Declined') return { label: 'Declined', tone: 'neutral', actor, isSlow: false };
  // A remediation run whose result never came back may have run: neither done nor failed.
  if (status === 'Unknown') {
    return { label: 'Outcome unknown', tone: 'warning', actor, isSlow: false, detail: 'no result came back; the command may have run' };
  }
  if (status === 'Success' && resolution?.type === 'CommandExecution' && !verifiedSuccess(resolution)) {
    if (readData(resolution).manual === true) {
      return { label: 'Done', tone: 'success', actor, isSlow: false, detail: 'reported done, not verified' };
    }
    return { label: 'Outcome unverified', tone: 'warning', actor, isSlow: false, detail: 'no exit code was reported' };
  }
  if (status === 'Success') {
    // A verify, when one ran, is what makes "Done" mean more than "dispatched". Its result lives on
    // the attempt it checked (data.verify), never as a resolution of its own.
    const verify = readVerify(resolution);
    if (verify?.ran && verify.passed !== true) {
      return {
        label: 'Needs checking',
        tone: 'warning',
        actor,
        isSlow: false,
        // passed === false means the check ran and failed; null means it proved nothing.
        detail: verify.passed === false ? 'the check did not confirm it' : 'the check confirmed nothing',
      };
    }
    return { label: 'Done', tone: 'success', actor, isSlow: false, verified: verify?.passed === true };
  }
  if (status === 'Failed') return { label: 'Failed', tone: 'critical', actor, isSlow: false };
  // Configuring, InProgress, and anything a future writer adds all read as in-flight rather than as
  // a bare enum leaking to the screen.
  return { label: 'Running', tone: 'info', actor, isSlow };
};

/**
 * The reason a failed attempt recorded, as text. A failed agent task used to be stored as the
 * agent's whole response — {"msg": "...", "success": false, ...} — and those rows cannot be
 * rewritten, so the reason is read out of them here. Anything else is returned unchanged.
 * @param {string} message
 */
export const readableStatusMessage = (message) => {
  const msg = safeJSONParse(message)?.msg;
  return typeof msg === 'string' && msg.trim() ? msg : message;
};

// Card ids are not uniform: some carry a positional suffix (LastDeploymentCard_0) and some do not
// (MemoryAllocationCard). Every per-card lookup here must key on the stable part, or it silently
// misses exactly the cards that are numbered — which is how the revert card lost its Fix label,
// its effect line and its Undo button while the resource card kept all three.
const cardKey = (cardId) => (typeof cardId === 'string' ? cardId.replace(/_\d+$/, '') : '');

// Whether an action removes the cause or only restores service. The wording matches what the
// remediation panel already tells the operator about Nubi's actions:
//
//   "A fix removes the root cause; a mitigation only restores service while the cause survives,
//    so the problem recurs."
//
// Only the card actions are classified here — Nubi's carry a server-assigned kind of their own.
// Anything not listed shows no chip: an unlabelled action is honest, a wrongly labelled one is not.
//
// A rollback is deliberately not classified. It removes the cause only when the change caused the
// problem, and nothing here knows that: the chip rode on every alert whose workload had changed in
// the past week — a Slack rate-limit spike, an OOM three weeks after an env edit — and it stayed on
// after "Recurred 5 times since — this did not stop it". The row states when the change landed and
// what it touched instead, which is the evidence the operator actually needs.
const ACTION_KIND_BY_CARD = {
  // Raising the limit stops this class of OOM rather than papering over it — the workload genuinely
  // needed the headroom. It is a fix in the sense that matters here: the condition does not return.
  MemoryAllocationCard: 'fix',
  // The analysis identified the cause in the source and the agent changes it there, so once the PR
  // is merged and deployed the condition does not return. The effect line below carries the caveat
  // that it changes nothing in the cluster before then.
  AskAiCard: 'fix',
};

export const ACTION_KIND_META = {
  fix: { text: 'Fix', tone: 'success', help: 'Removes the cause' },
  mitigation: { text: 'Mitigation', tone: 'warning', help: 'Restores service; the cause survives' },
};

/**
 * Classify a card-based action, or return null when it cannot be classified honestly.
 * @param {string} cardId
 */
export const describeActionKind = (cardId) => {
  const kind = ACTION_KIND_BY_CARD[cardKey(cardId)];
  return kind ? { kind, ...ACTION_KIND_META[kind] } : null;
};

// What running an action actually does to the cluster, in the operator's terms.
//
// The confirmation step names the object and shows a diff, but never says the change replaces every
// running pod. Someone approving a revert at 3am should not have to know that a pod-template change
// triggers a rolling restart in order to understand what they are agreeing to.
//
// Kept deliberately non-numeric: replica counts and field counts vary per event, and a wrong number
// here is worse than no number.
const ACTION_EFFECT_BY_CARD = {
  // Only the changed fields are written back, not the whole spec — and whatever deploys the workload
  // (Helm, Argo CD, an operator) may put them back again, which the dialog says when it can tell.
  LastDeploymentCard: 'Writes back the values this change replaced · restarts the pods',
  MemoryAllocationCard: 'Changes the container resource requests and limits · rolling restart · reversible',
  AskAiCard: 'Opens a pull request against the mapped source repository · nothing changes in the cluster until it is merged and deployed',
};

/**
 * One line describing an action's effect, or null when we have nothing accurate to say.
 * @param {string} cardId
 */
export const describeActionEffect = (cardId) => ACTION_EFFECT_BY_CARD[cardKey(cardId)] || null;

// Whether a completed attempt can be put back, and what to send to do it.
//
// Two shapes, because two mechanisms exist. A revert is undone by reading the event's diff the other
// way round — the data was always there. Everything else is undone by re-applying the before-state
// the agent recorded at apply time. Actions that recorded nothing cannot be undone, and a restart or
// a PVC resize never can be: they have already happened, or Kubernetes will not shrink a volume.
export const describeUndo = (resolution, cardId, card) => {
  if (!resolution || resolution.status !== 'Success') return null;

  if (cardKey(cardId) === 'LastDeploymentCard') {
    // Undo the change the revert acted on. Without its id the server falls back to the alert's own
    // evidence, which refuses (or, before that guard, merged) an alert carrying several changes.
    const recorded = typeof resolution.data === 'string' ? safeJSONParse(resolution.data) : resolution.data;
    const changeEventId = recorded?.data?.change_event_id;
    return {
      label: 'Undo rollback',
      payload: { revert: true, undo: true, card_id: cardId, ...(changeEventId && { change_event_id: changeEventId }) },
      preview: describeRevertUndoPreview(card),
    };
  }

  const data = typeof resolution.data === 'string' ? safeJSONParse(resolution.data) : resolution.data;
  if (data?.before && Object.keys(data.before).length > 0) {
    // Keyed on the attempt, so the server re-dispatches that action with what it replaced.
    return {
      label: 'Undo',
      payload: { undo_resolution_id: resolution.id, ...(cardId && { card_id: cardId }) },
      preview: describeBeforeStatePreview(data.before),
    };
  }
  return null;
};

// ── What an undo will actually do ────────────────────────────────────────────
// Undo is a cluster mutation dispatched on one click. Firing it without stating the target and the
// resulting values asks someone to change a live workload on trust. Everything below is derived from
// the same recorded data the server re-applies, so the preview cannot describe one change and the
// server perform another — and where we genuinely cannot enumerate the change, it says so rather
// than inventing a diff that looks authoritative.

const RESOURCE_FIELD_LABELS = {
  cpu_request: 'CPU request',
  cpu_limit: 'CPU limit',
  memory_request: 'Memory request',
  memory_limit: 'Memory limit',
};

/**
 * The values a generic undo restores, read from the pre-change state the agent reported.
 * @param {{containers?: Array<Record<string, string>>}} before
 */
export const describeBeforeStatePreview = (before) => {
  // A scale records a single number rather than per-container fields.
  if (before?.replicas !== undefined && before?.replicas !== null && before?.containers === undefined) {
    const replicas = Number(before.replicas);
    if (Number.isFinite(replicas)) {
      return {
        kind: 'values',
        summary: 'Restores the replica count:',
        groups: [{ name: 'Replicas', fields: [{ label: 'Replicas', value: String(replicas) }] }],
      };
    }
  }

  const containers = Array.isArray(before?.containers) ? before.containers : [];
  const groups = containers
    .map((container) => ({
      name: container?.container_name || 'container',
      // An absent key means the field was unset before the change, and restoring it means removing
      // it — not setting it to "". Only fields actually recorded are listed.
      fields: Object.entries(RESOURCE_FIELD_LABELS)
        .filter(([key]) => container?.[key] !== undefined && container?.[key] !== null && container?.[key] !== '')
        .map(([key, label]) => ({ label, value: String(container[key]) })),
    }))
    .filter((group) => group.fields.length > 0);

  if (groups.length === 0) return { kind: 'unknown', summary: 'Restores the values recorded before this change.' };
  return { kind: 'values', summary: 'Restores these values:', groups };
};

/**
 * The change an "Undo rollback" re-applies. The event's diff holds the deployment that preceded the
 * problem: the revert applied its `old` side, so undoing it re-applies the `new` side — which is
 * what is running now versus what undo will put back.
 * @param {{diff?: {data?: {old?: string, new?: string}}, deploymentHistory?: object}} card
 */
export const describeRevertUndoPreview = (card) => {
  const old = card?.diff?.data?.old;
  const next = card?.diff?.data?.new;
  if (typeof old === 'string' && typeof next === 'string' && old !== next) {
    return {
      kind: 'diff',
      summary: 'Re-applies the deployment change that was reverted:',
      before: old,
      after: next,
      beforeLabel: 'Running now',
      afterLabel: 'After undo',
    };
  }
  return {
    kind: 'unknown',
    summary: 'Re-applies the deployment change that was reverted. The exact fields are not recorded on this event.',
  };
};

// What the action DOES, phrased as an instruction. The card's own text names the finding — "Last
// Deployment Change", "Check if Resource allocation is sufficient" — which reads as nonsense once a
// state is attached to it: "Last Deployment Change · Done" says the deployment change is done, not
// that it was reverted. Rows are named by the action so the state has something to attach to.
const ACTION_TITLE_BY_CARD = {
  LastDeploymentCard: 'Roll back the last deployment change',
  MemoryAllocationCard: 'Adjust resource requests and limits',
  AskAiCard: 'Raise a pull request with the proposed code fix',
};

/**
 * Name an action. Falls back to the card's own text when we have no better phrasing, so a new card
 * still reads as something rather than nothing.
 * @param {string} cardId
 * @param {string} fallback
 */
export const describeActionTitle = (cardId, fallback, subject) => {
  const title = ACTION_TITLE_BY_CARD[cardKey(cardId)];
  if (!title) return fallback || 'Run this action';
  // "the last deployment change" is only unambiguous when there was one. A workload with several
  // recent changes gets several rows that read identically, and the operator cannot tell which
  // change a row would undo — the very confusion this area is being fixed for. When the card knows
  // which change it acts on, say so.
  return subject ? `${title.replace(/ the last deployment change$/, '')} the change deployed ${subject}`.trim() : title;
};

// The row's name when the operator picks which change the action applies to, rather than the row
// naming one change itself.
const CHOICE_TITLE_BY_CARD = {
  LastDeploymentCard: 'Roll back a deployment change',
};

/**
 * Name an action whose target is chosen in the row. Falls back to describeActionTitle.
 * @param {string} cardId
 * @param {string} fallback
 */
export const describeChoiceTitle = (cardId, fallback) => CHOICE_TITLE_BY_CARD[cardKey(cardId)] || describeActionTitle(cardId, fallback);

/**
 * Whether a card is offering a way to act on the event right now.
 *
 * Most cards advertise one with `resolveButton`; a few only carry a `ResolveComponent`, so either
 * counts. AskAiCard is the exception: it always has a ResolveComponent, and sets `resolveButton`
 * only once log analysis has stored a fix diff for a mapped source repo — judged by the OR it would
 * list a row on every event, most of them opening on nothing to raise.
 *
 * @param {{id?: string, resolveButton?: boolean, ResolveComponent?: unknown}} option
 */
export const cardOffersAction = (option) =>
  cardKey(option?.id) === 'AskAiCard' ? Boolean(option?.resolveButton) : Boolean(option?.resolveButton || option?.ResolveComponent);

/**
 * The object an undo will change, for the confirmation header. The revert card carries the real
 * target ("deployment/<namespace>/<name>.yaml"); the event's own subject is the pod, which a rollout
 * replaces as a side effect rather than the thing being edited. Falls back to the event subject when
 * the card has no resource of its own.
 * @param {object} card
 * @param {object} event
 */
export const describeUndoTarget = (card, event) => {
  const resource = card?.diff?.data?.resource_name;
  if (typeof resource === 'string' && resource) return resource.replace(/\.yaml$/i, '');
  if (event?.subject_namespace && event?.subject_name) return `${event.subject_namespace}/${event.subject_name}`;
  return event?.subject_name || '';
};

const trimmed = (value) => (typeof value === 'string' ? value.trim() : '');

// decisionsByStep keys runs by plan version + step, newest wins. not_sent runs decide nothing and are skipped,
// so they never hide an earlier unknown outcome (the server's rule too).
export function decisionsByStep(rows) {
  const out = {};
  (rows || []).forEach((r) => {
    if (r?.type !== 'CommandExecution') return;
    const data = readData(r);
    if (!data.plan_version || !data.step || data.not_sent === true) return;
    const key = `${data.plan_version}|${data.step}`;
    if (!out[key] || (r.created_at || '') > (out[key].created_at || '')) out[key] = r;
  });
  return out;
}

// appliedCommands maps each command (or "change:<action>") NudgeBee saw succeed, or the operator marked
// done, to when. A run with no exit code or an unknown outcome is not applied.
export function appliedCommands(rows) {
  const map = {};
  (rows || []).forEach((r) => {
    if (r?.type !== 'CommandExecution' || !(verifiedSuccess(r) || (r.status === 'Success' && readData(r).manual === true))) return;
    const cmd = trimmed(r.type_reference_id) || trimmed(readData(r).command);
    if (cmd && !map[cmd]) map[cmd] = r.updated_at || r.created_at || '';
  });
  return map;
}

// latestFollowup is NudgeBee's follow-up check of the newest run that has one (#40734): the run's
// data.followup, with the run's id. Null when no run has been checked.
export function latestFollowup(rows) {
  let best = null;
  (rows || []).forEach((r) => {
    const f = r?.type === 'CommandExecution' ? readData(r).followup : null;
    if (f && (!best || (r.created_at || '') > best.createdAt)) best = { ...f, runId: r.id, createdAt: r.created_at || '' };
  });
  return best;
}

// FOLLOWUP_WAIT_MS: how long after a run settles the page looks for its follow-up check. The server claims one
// within a minute (leader tick), only with the tenant's loop on; elsewhere none comes, so the page stops soon after.
const FOLLOWUP_WAIT_MS = 3 * 60 * 1000;

// awaitingFollowup: the newest run settled within FOLLOWUP_WAIT_MS and has no follow-up check yet, so one
// may start while the page is open.
export function awaitingFollowup(rows, now = Date.now()) {
  let newest = null;
  (rows || []).forEach((r) => {
    if (r?.type === 'CommandExecution' && (!newest || (r.created_at || '') > (newest.created_at || ''))) newest = r;
  });
  if (!newest || newest.status === 'InProgress' || newest.status === 'Declined') return false;
  const data = readData(newest);
  if (data.followup || data.not_sent === true) return false;
  const at = parseDateValue(newest.updated_at || newest.created_at || '').getTime();
  return !Number.isNaN(at) && now - at < FOLLOWUP_WAIT_MS;
}

// FOLLOWUP_GIVE_UP_MS: the server gives up a follow-up this long after it was claimed (followupGiveUp). One it
// never ends (the tenant's loop turned off mid-check) would otherwise keep the page polling for as long as it is open.
const FOLLOWUP_GIVE_UP_MS = 2 * 60 * 60 * 1000;
const timeOf = (value) => (value ? parseDateValue(value).getTime() : NaN);

// followupPoll: whether the page should keep polling for NudgeBee's follow-up check (one is due or checking
// now), and, while one waits for a re-check it asked for, when that is due: the page then waits until then
// rather than polling.
export function followupPoll(rows, now = Date.now()) {
  const f = latestFollowup(rows);
  if (!f || f.done_at) return { live: awaitingFollowup(rows, now), wakeAt: null };
  const recheck = timeOf(f.recheck_at);
  if (recheck > now) return { live: false, wakeAt: recheck };
  const last = Math.max(timeOf(f.claimed_at) || 0, recheck || 0);
  return { live: !last || now - last < FOLLOWUP_GIVE_UP_MS, wakeAt: null };
}
