import { formatClockCompact, parseDateValue } from '@shared/format/Datetime';
import { safeJSONParse } from 'src/utils/common';
import { readData } from './resolutionStatus';

// The remediation loop (#40734), read side. With the tenant's loop on, ai_remediation_get keeps the plan's
// fields at the top level and adds a `loop` object (llm-server api/remediation_get_loop.go):
//   plan_status ready|preparing|none, plan_status_reason, preparing_since,
//   relevance {alert_state, alert_state_at, firings_since_plan, runs_since_plan, newer_event_id, stale, stale_reason},
//   pending_approvals [{message_id, agent_id, tool, command, created_at, origin: web|slack}],
//   primary_event_id.
// With the loop off there is no `loop`, and the page shows today's tab. Everything here is pure, so each state
// the tab can be in is testable from a fixture.

const trimmed = (value) => (typeof value === 'string' ? value.trim() : '');

// loopOf is the response's loop object, or null when the tenant's loop is off.
export const loopOf = (res) => (res && typeof res === 'object' && res.loop && typeof res.loop === 'object' ? res.loop : null);

// The plan's options: its actions, numbered from 1 as the run records number them (C1 data.option).
export const planOptions = (res) => (Array.isArray(res?.actions) ? res.actions : []);

const timeOf = (value) => (value ? parseDateValue(value).getTime() : NaN);
const newestFirst = (a, b) => (timeOf(b.at) || 0) - (timeOf(a.at) || 0);

// chosenOption is the plan option a person set under way on the plan shown: the newest run (C1) or pull request
// (C3) that names an option on this plan version, not a rollback and not one that provably never ran. Null when no
// option is under way. `done` once the run's check said fixed or the event is resolved.
export function chosenOption(res, rows, nbStatus) {
  const version = trimmed(res?.plan_version);
  const count = planOptions(res).length;
  let best = null;
  (rows || []).forEach((r) => {
    if (r?.type !== 'CommandExecution' && r?.type !== 'PullRequest') return;
    const data = readData(r);
    const option = Number(data.option);
    if (!Number.isInteger(option) || option < 1 || option > count) return;
    if (version && trimmed(data.plan_version) && trimmed(data.plan_version) !== version) return;
    if (data.slot === 'rollback' || r.status === 'Declined' || data.not_sent === true) return;
    // A pull request closed without merging is no longer under way.
    if (r.type === 'PullRequest' && prOf(r)?.state === 'closed') return;
    if (!best || (r.created_at || '') > (best.row.created_at || '')) best = { option, row: r };
  });
  if (!best) return null;
  const verdict = readData(best.row).followup?.verdict;
  const resolved = String(nbStatus || '').toUpperCase() === 'RESOLVED';
  return { option: best.option, row: best.row, done: resolved || verdict === 'fixed' };
}

// The reason codes the verdict code writes (llm-server remediation_verdict.go), as sentences. A code with no entry
// here is never shown as-is.
export const REASON_TEXT = {
  rollback_not_a_fix: 'a rollback is not counted as a fix',
  change_failed: 'the change failed',
  change_outcome_unknown: 'it is not known how the change ended',
  alert_state_unread: 'the alert’s state could not be read',
  fired_again_after_change: 'the alert fired again after the change',
  clear_time_unknown: 'the alert cleared, but it is not known when',
  cleared_before_change: 'the alert cleared before the change ran',
  cleared_after_change_and_held: 'the alert cleared after the change and stayed clear',
  cleared_after_change: 'the alert cleared after the change; checking that it holds',
  still_firing: 'the alert is still firing after the change',
  too_early: 'too early to tell',
};

const looksLikeCode = (value) => /^[a-z0-9]+(_[a-z0-9]+)+$/.test(trimmed(value));
const capitalize = (value) => (value ? value.charAt(0).toUpperCase() + value.slice(1) : value);

// plainReason turns a reason into a sentence: a known code is mapped, an unknown code is dropped (''), and text that
// is already a sentence is kept.
export function plainReason(value) {
  const v = trimmed(value);
  if (!v) return '';
  if (REASON_TEXT[v]) return REASON_TEXT[v];
  return looksLikeCode(v) ? '' : v;
}

// sameSentence: two lines say the same thing ("Closed on its own at 19:20 UTC." and "The alert closed on its own at
// 19:20 UTC."), so only one of them is shown. Compared on their words, ignoring case, punctuation and a leading
// "the alert" / "it".
const sentenceWords = (value) =>
  trimmed(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
    .replace(/^(the alert|this alert|the event|it) /, '');
export function sameSentence(a, b) {
  const x = sentenceWords(a);
  const y = sentenceWords(b);
  if (!x || !y) return false;
  return x === y || (x.length >= 12 && y.includes(x)) || (y.length >= 12 && x.includes(y));
}

// dedupeSentences drops a sentence that repeats an earlier one in the same text ("Closed on its own at 19:20 UTC.
// The alert closed on its own at 19:20 UTC. …" keeps the first).
export function dedupeSentences(value) {
  const parts = trimmed(value).match(/[^.!?]+[.!?]*(\s+|$)/g) || [];
  const kept = [];
  parts.forEach((part) => {
    if (!kept.some((k) => sameSentence(k, part))) kept.push(part);
  });
  return kept.join('').trim();
}

// planExists: the read returned a stored plan, with options or with a summary of why there are none (the same test
// the server uses). With none, the person can ask for one.
export const planExists = (res) => planOptions(res).length > 0 || !!trimmed(res?.root_cause) || !!trimmed(res?.summary);

const commandLine = (value) => {
  const first = trimmed(value).split('\n')[0];
  return trimmed(value).includes('\n') ? `${first} …` : first;
};

// pendingFor splits the approvals waiting on a person by where they can be answered: the tab answers only
// web-started ones; a Slack-started one is answered in its thread, which is where the outcome is posted.
export function pendingFor(loop) {
  const all = Array.isArray(loop?.pending_approvals) ? loop.pending_approvals.filter((p) => trimmed(p?.message_id)) : [];
  return { web: all.filter((p) => p.origin !== 'slack'), slack: all.filter((p) => p.origin === 'slack') };
}

// FOLLOWUP_CLAIM_WINDOW_MS is how long after a run settles the server still starts its follow-up (llm-server
// ClaimDueFollowups: within an hour); a run older than that with none never gets one.
const FOLLOWUP_CLAIM_WINDOW_MS = 60 * 60 * 1000;

// attemptMoving: an attempt is still moving without the person while it runs, while its follow-up is under way or
// still to start, or while its pull request is open. One whose follow-up ended (fixed, not fixed, unclear) waits on
// a person.
const attemptMoving = (row, now) => {
  if (row?.type === 'PullRequest' || row?.status === 'InProgress') return true;
  const followup = readData(row).followup;
  if (followup) return !followup.done_at;
  const created = timeOf(row?.created_at);
  return !created || now - created < FOLLOWUP_CLAIM_WINDOW_MS;
};

// loopNeedsPoll: the tab looks again while something is moving without the person: a plan being written, an
// approval waiting (answered elsewhere it disappears), or an option under way.
export function loopNeedsPoll({ loop, res, rows, nbStatus, now = Date.now() }) {
  if (!loop) return false;
  // A parked re-check starts on the planner's next tick once its approval is answered.
  if (loop.plan_status === 'preparing' || loop.conversation_busy || loop.parked_recheck) return true;
  const { web, slack } = pendingFor(loop);
  if (web.length || slack.length) return true;
  const chosen = chosenOption(res, rows, nbStatus);
  // A likely-fixed attempt waits on a person (Resolve), and a failed or unclear one on their next step, so the tab
  // stops reading by itself until they act.
  return Boolean(chosen && !chosen.done && attemptMoving(chosen.row, now));
}

// Labels T3 writes on an event: nb_resolved_by (C5) and nb_refire (C6). Stored as JSON, read either way.
const labelObject = (labels, key) => {
  const v = labels?.[key];
  if (!v) return null;
  if (typeof v === 'object') return v;
  const parsed = safeJSONParse(v);
  return parsed && typeof parsed === 'object' ? parsed : null;
};

// refireLabel is the C6 label in words: "Fix did not hold" only when the cause keys matched, else "Fired again".
export function refireLabel(labels) {
  const r = labelObject(labels, 'nb_refire');
  if (!r) return null;
  return r.kind === 'fix_did_not_hold'
    ? { text: 'Fix did not hold', tone: 'warning', previousRunId: trimmed(r.previous_run_id) }
    : { text: 'Fired again', tone: 'neutral', previousRunId: trimmed(r.previous_run_id) };
}

const causeKeyText = (key) => (key && typeof key === 'object' ? [key.what, key.how, key.against].map(trimmed).filter(Boolean).join(' · ') : '');

// confirmedByName is the person who confirmed the loop's verdict and resolved the event (nb_resolved_by), or ''.
export function confirmedByName(labels) {
  const by = labelObject(labels, 'nb_resolved_by');
  return trimmed(by?.confirmed_by_name) || '';
}

// resolutionSummary is what the Investigation tab says once the event was resolved for the loop (C5): the cause, the fix
// that was run and who approved it, and the check that proved it, or that the alert cleared before the change.
// Null unless the event is resolved and carries nb_resolved_by.
export function resolutionSummary({ labels, nbStatus, res, rows }) {
  if (String(nbStatus || '').toUpperCase() !== 'RESOLVED') return null;
  const by = labelObject(labels, 'nb_resolved_by');
  if (!by) return null;
  const run = (rows || []).find((r) => r?.id && String(r.id) === String(by.run_id)) || null;
  const data = run ? readData(run) : {};
  const verdict = trimmed(by.verdict) || trimmed(data.followup?.verdict);
  const who = run ? (run.resolver_type === 'User' && trimmed(run.resolver_display_name)) || trimmed(data.approved_by) : '';
  const command = trimmed(data.command) || trimmed(run?.type_reference_id);
  return {
    clearedBeforeChange: verdict === 'cleared_before_change',
    resolvedAt: data.followup?.verdict_at || '',
    cause: trimmed(res?.root_cause) || causeKeyText(by.cause_key),
    fix: command,
    fixedBy: who,
    proof: capitalize(plainReason(data.followup?.verdict_reason)),
  };
}

// ── Where it stands / Attempts ───────────────────────────────────────────────────────────────────────────────────

const hasSpace = (value) => /\s/.test(trimmed(value));

// optionTitle is an option's name in words. Some plans name an option with a slug ("set-image"); its description
// line then names it instead.
export function optionTitle(action) {
  const name = trimmed(action?.action);
  const description = trimmed(action?.title);
  if (name && hasSpace(name)) return name;
  if (description) return description;
  return name ? capitalize(name.replace(/[-_]+/g, ' ')) : 'Option';
}

// optionDescription is the option's description line, unless it already stands in as the title.
export const optionDescription = (action) => {
  const description = trimmed(action?.title);
  return description && optionTitle(action) !== description ? description : '';
};

// ── Changes no command applies (required_change) ──
// A fix whose change is made in code, a deployment source, infrastructure, a dependency or a process carries
// required_change {where, target, change, repo?, reason?} and no command. A code change with a repository can be
// raised as a pull request from the alert's chat; any other is made by a person, who then marks it done.
export const CHANGE_LABEL = {
  code: 'Code fix',
  deployment_source: 'Change in the deployment source',
  infrastructure: 'Infrastructure change',
  dependency: 'Dependency change',
  process: 'Process change',
};

export function requiredChangeOf(action) {
  const rc = action?.required_change;
  if (!rc || typeof rc !== 'object' || !trimmed(rc.change)) return null;
  return {
    where: trimmed(rc.where).toLowerCase(),
    target: trimmed(rc.target),
    change: trimmed(rc.change),
    repo: trimmed(rc.repo),
    reason: trimmed(rc.reason),
  };
}

// A change always made in a repository (code, a deployment source) is raised as a pull request; an infrastructure or
// dependency change may be made elsewhere (a cloud quota, another team's service), so only when the plan names a
// repository. The same rule as llm-server's repoChange.
const IN_REPO_WHERES = ['code', 'deployment_source'];
const PR_WHERES = [...IN_REPO_WHERES, 'infrastructure', 'dependency'];

export function repoChangeOf(action) {
  const rc = requiredChangeOf(action);
  return Boolean(rc) && (IN_REPO_WHERES.includes(rc.where) || (PR_WHERES.includes(rc.where) && Boolean(rc.repo)));
}

// optionRepoOf is how option n's repository is known (loop.option_repos, llm-server optionRepos): {repo, known} with
// known 'workload' (the workload comes from it), 'integration' (a connected repository) or '' (none known).
export function optionRepoOf(loop, n) {
  const found = (Array.isArray(loop?.option_repos) ? loop.option_repos : []).find((r) => Number(r?.option) === n);
  return { repo: trimmed(found?.repo), known: trimmed(found?.known) };
}

// automationOf is the account's own automation an option runs (#42459): {id, name, inputs: [[key, text]]}, or null.
export function automationOf(action) {
  const a = action?.automation;
  if (!a || typeof a !== 'object' || !trimmed(a.id)) return null;
  const inputs = Object.entries(a.inputs && typeof a.inputs === 'object' ? a.inputs : {})
    .sort(([x], [y]) => x.localeCompare(y))
    .map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)]);
  return { id: trimmed(a.id), name: trimmed(a.name) || trimmed(a.id), inputs };
}

// optionOfCommand is the option (from 1) an approval's command runs: its execute_command, or automation:<id> for an
// option that runs the account's automation (llm-server stepRef); 0 for none.
export function optionOfCommand(res, command) {
  const c = trimmed(command);
  if (!c) return 0;
  const i = planOptions(res).findIndex((a) => trimmed(a?.execute_command) === c || (automationOf(a) && `automation:${automationOf(a).id}` === c));
  return i + 1;
}

// automationHref is the automation's page on the account.
export const automationHref = (accountId, id) => `/automation/${encodeURIComponent(id)}?accountId=${encodeURIComponent(trimmed(accountId))}`;

// optionMode is how an option is carried out from the tab: 'pr' (a change in the repository the workload comes from:
// Raise a PR), 'pr_pick' (a change in a repository the person confirms or picks first: Raise a PR…), 'manual' (a
// change a person makes, then marks done), or 'run' (the assistant runs it after an approval).
export function optionMode(action, repo) {
  if (!requiredChangeOf(action)) return 'run';
  if (!repoChangeOf(action)) return 'manual';
  return repo?.known === 'workload' ? 'pr' : 'pr_pick';
}

// changeOnly: the plan has options and none of them is run by a command (each is a code or other required change).
export const changeOnly = (options) => options.length > 0 && options.every((a) => optionMode(a) !== 'run');

// PR_REPLY_ASK ends a Raise a PR request, as llm-server's prReplyAsk: the reply led with the whole diff.
export const PR_REPLY_ASK = "Reply with the pull request's link first and one line on what it changes; the diff is in the PR.";

// prRequestText is the message sent into the alert's chat to raise a PR for option n.
export function prRequestText({ index, res, action, repo }) {
  const rc = requiredChangeOf(action) || {};
  const version = Number.isInteger(res?.version) && res.version > 0 ? ` of plan v${res.version}` : '';
  const cause = firstLine(res?.root_cause);
  const where = trimmed(repo) || rc.repo;
  return `Raise a PR for option ${index}${version}: ${rc.change}${rc.target ? ` in ${rc.target}` : ''}${where ? ` (repo ${where})` : ''}.${
    cause ? ` Root cause: ${cause}` : ''
  } ${PR_REPLY_ASK}`;
}

// The first sentence (or line) of a text, ending in its stop.
function firstLine(value) {
  const text = trimmed(value).split('\n')[0].trim();
  if (!text) return '';
  const m = /^(.+?[.!?])(\s|$)/.exec(text);
  const line = m ? m[1] : text;
  return /[.!?]$/.test(line) ? line : `${line}.`;
}

// prOf reads a PullRequest run row: its link, number (data.pr_number, else from the URL), state and when it ended.
// The state comes from pr_lifecycle_state (created, needs_followup, addressing -> open; stale; merged; closed;
// unresolvable -> unknown); with none, from the row's status (in progress -> open, Success -> merged, Failed -> closed).
// last_pr_check_at is the merge or close time once the PR has ended.
const PR_STATE = {
  created: 'open',
  needs_followup: 'open',
  addressing: 'open',
  stale: 'stale',
  merged: 'merged',
  closed: 'closed',
  unresolvable: 'unknown',
};
const PR_STATUS_STATE = { InProgress: 'open', Pending: 'open', Success: 'merged', Failed: 'closed' };
// How the timeline says each state after "PR raised · #N".
export const PR_STATE_TEXT = {
  open: 'open',
  stale: 'went stale (no activity 3 days)',
  merged: 'merged',
  closed: 'closed',
  unknown: 'state unknown',
};
export function prOf(row) {
  if (row?.type !== 'PullRequest') return null;
  const data = readData(row);
  const url = [data.pr_url, row.type_reference_id].map(trimmed).find((u) => /^https?:\/\//.test(u)) || '';
  const n = Number(data.pr_number);
  const fromUrl = /\/pull\/(\d+)/.exec(url) || /\/merge_requests\/(\d+)/.exec(url);
  const number = Number.isInteger(n) && n > 0 ? n : fromUrl ? Number(fromUrl[1]) : null;
  const lifecycle = trimmed(row.pr_lifecycle_state || data.pr_lifecycle_state).toLowerCase();
  const state = PR_STATE[lifecycle] || (lifecycle ? 'unknown' : PR_STATUS_STATE[row.status] || '');
  const endedAt = state === 'merged' || state === 'closed' ? trimmed(row.last_pr_check_at) || '' : '';
  return { url, number, state, endedAt };
}

// phrase lower-cases a title's first letter to sit inside a sentence, unless it starts an acronym ("AWS …").
export const phrase = (value) => {
  const v = trimmed(value);
  return v && !/^[A-Z]{2}/.test(v) ? v.charAt(0).toLowerCase() + v.slice(1) : v;
};

// isPlanStepRun: the run carries out a step of the plan (from the tab, Slack, run_step, or a chat write the server
// matched to an option), as opposed to a change approved in the chat outside the plan. Only a plan step is the
// attempt the tab reports on; the others are recorded and listed, never judged (llm-server verdictRun.planStep).
export const isPlanStepRun = (row) => {
  if (row?.type !== 'CommandExecution') return true;
  const data = readData(row);
  return trimmed(data.source) !== 'chat' || (data.option !== undefined && data.option !== null) || Boolean(trimmed(data.step));
};

// A run as the person counts them: a recorded change, not a decline or one that provably never ran.
const isAttempt = (row) =>
  (row?.type === 'CommandExecution' || row?.type === 'PullRequest' || row?.type === 'WorkflowExecution') &&
  row.status !== 'Declined' &&
  readData(row).not_sent !== true;

const byCreated = (a, b) => (timeOf(a.created_at) || 0) - (timeOf(b.created_at) || 0);
const attemptRuns = (rows) => (rows || []).filter(isAttempt).sort(byCreated);

// answerText is how an approval was answered, after the person's name: where (" in Slack"; the tab and chat say
// nothing) and the note they gave (: “…”), both as the server recorded them.
export function answerText(from, note) {
  const where = trimmed(from) === 'slack' ? ' in Slack' : '';
  const said = trimmed(note);
  return `${where}${said ? `: “${said}”` : ''}`;
}

// runApprover is who approved (or made) a run.
export const runApprover = (row) => {
  const data = readData(row);
  return (row?.resolver_type === 'User' && trimmed(row?.resolver_display_name)) || trimmed(data.approved_by);
};

const runCommand = (row) => {
  const data = readData(row);
  return trimmed(data.command) || (row?.type === 'CommandExecution' ? trimmed(row?.type_reference_id) : '');
};

// runOptionTitle is the plan option a run carried out, in words, or '' when the run names none on this plan.
export function runOptionTitle(res, row) {
  const data = readData(row);
  const options = planOptions(res);
  const option = Number(data.option);
  if (!Number.isInteger(option) || option < 1 || option > options.length) return '';
  if (trimmed(data.plan_version) && trimmed(res?.plan_version) && data.plan_version !== res.plan_version) return '';
  return optionTitle(options[option - 1]);
}

// summaryStatusLine is the "Status" line of a follow-up's markdown summary, as plain text on one line ('' when the
// summary has none; then its first line).
export function summaryStatusLine(summary) {
  const text = trimmed(summary);
  if (!text) return '';
  const lines = text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
  const status = lines.find((l) => /^\W*status\W*:/i.test(l.replace(/[*_`]/g, '')));
  const line = (status || lines[0] || '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`#>]/g, '')
    .replace(/^\s*status\s*:\s*/i, '')
    .replace(/\s+/g, ' ')
    .trim();
  return line.length > 160 ? `${line.slice(0, 157)}…` : line;
}

const CLI_TOOLS = ['kubectl', 'helm', 'gh', 'aws', 'az', 'gcloud', 'gsutil', 'bq', 'argocd', 'oc'];
const RESOURCE_KINDS = [
  'pod',
  'pods',
  'deployment',
  'deploy',
  'service',
  'svc',
  'configmap',
  'cm',
  'secret',
  'statefulset',
  'sts',
  'daemonset',
  'ds',
  'job',
  'cronjob',
  'node',
  'namespace',
  'ns',
  'ingress',
  'pvc',
  'hpa',
];
const SUB_VERBS = ['image', 'resources', 'env', 'undo', 'restart', 'status', 'pause', 'resume', 'run', 'merge', 'sync', 'rollback'];

// commandSummary is a short name for a command: verb, then target or the key value it sets, e.g.
// "kubectl set image deployment/web web=nginx:1.27-alpine -n demo" → "set image → nginx:1.27-alpine".
export function commandSummary(value) {
  const tokens = commandLine(value).replace(/\s…$/, '').split(/\s+/).filter(Boolean);
  if (!tokens.length) return '';
  if (CLI_TOOLS.includes(tokens[0])) tokens.shift();
  const words = [];
  let skipNext = false;
  tokens.forEach((t) => {
    if (skipNext) {
      skipNext = false;
      return;
    }
    if (t.startsWith('-')) {
      if (!t.includes('=') && /^-(n|c|f|o)$|^--(namespace|context|container|output|region|profile)$/.test(t)) skipNext = true;
      if (t.includes('=') && !/^--(namespace|context|region|profile|output)=/.test(t)) words.push({ kv: t.replace(/^-+/, ''), flag: true });
      return;
    }
    words.push(t.includes('=') ? { kv: t } : { word: t });
  });
  const plain = words.filter((w) => w.word).map((w) => w.word);
  const twoWordVerb = SUB_VERBS.includes(plain[1]);
  const verb = plain[0] && twoWordVerb ? `${plain[0]} ${plain[1]}` : plain[0] || '';
  let rest = plain.slice(twoWordVerb ? 2 : 1);
  if (RESOURCE_KINDS.includes(rest[0]) && rest[1]) rest = [`${rest[0]} ${rest[1]}`];
  const target = rest[0] || '';
  const last = words.filter((w) => w.kv).pop();
  const keyValue = last ? (last.flag ? last.kv : last.kv.split('=').slice(1).join('=')) : '';
  const out = keyValue ? `${verb} → ${keyValue}` : [verb, target.replace(/^[a-z.]+\//i, '')].filter(Boolean).join(' ');
  return out.length > 60 ? `${out.slice(0, 57)}…` : out;
}

// Why a follow-up handed the alert to a person, in words.
const NEEDS_HUMAN_TEXT = { attempt_limit: 'several steps were tried for this alert in the last 24 hours' };

// runPhase is where a run is: running, checking (no first check yet), verifying (waiting for the hold re-check;
// until = followup.hold_until when the server writes it), its verdict with the reason as a sentence, or, when the
// follow-up ended with no verdict, checked (the assistant's own check) or needs_human. A follow-up with done_at is
// over: never checking or verifying.
export function runPhase(row) {
  if (!row) return { key: '' };
  // A pull request has no automatic verdict: it is open, merged or closed, and a person says when it fixed the alert.
  if (row.type === 'PullRequest') {
    const pr = prOf(row);
    return { key: `pr_${['merged', 'closed', 'stale', 'unknown'].includes(pr.state) ? pr.state : 'open'}`, at: pr.endedAt };
  }
  const data = readData(row);
  const f = data.followup || {};
  if (f.verdict) return { key: f.verdict, reason: plainReason(f.verdict_reason), at: f.verdict_at || '' };
  if (row.status === 'Failed') return { key: 'not_fixed', reason: plainReason('change_failed') };
  if (f.done_at) {
    if (f.outcome === 'needs_human') {
      return { key: 'needs_human', reason: NEEDS_HUMAN_TEXT[f.reason] || plainReason(f.reason) || summaryStatusLine(f.summary), at: f.done_at };
    }
    return { key: 'checked', reason: summaryStatusLine(f.summary), at: f.done_at };
  }
  if (row.status !== 'Success' && row.status !== 'Unknown') return { key: 'running' };
  if (f.hold_until || f.recheck_at) return { key: 'verifying', until: f.hold_until || '' };
  return { key: 'checking' };
}

// RESULT_CHIP is the chip for each phase; "checked" names the assistant, so it is filled in by resultChip.
export const RESULT_CHIP = {
  // The verdict is code's reading of the alert's own firings, a suggestion until a person resolves the event.
  fixed: { text: 'Likely fixed', tone: 'success' },
  cleared_before_change: { text: 'Cleared on its own', tone: 'neutral' },
  not_fixed: { text: 'Not fixed', tone: 'critical' },
  unclear: { text: 'Couldn’t confirm', tone: 'warning' },
  needs_human: { text: 'Needs you', tone: 'warning' },
  checked: { text: 'Checked by {assistant}', tone: 'neutral' },
  running: { text: 'Running', tone: 'info' },
  checking: { text: 'Checking', tone: 'info' },
  verifying: { text: 'Verifying', tone: 'info' },
  pr_open: { text: 'PR open', tone: 'info' },
  pr_merged: { text: 'PR merged', tone: 'success' },
  pr_closed: { text: 'PR closed', tone: 'neutral' },
  pr_stale: { text: 'PR stale', tone: 'warning' },
  pr_unknown: { text: 'PR state unknown', tone: 'warning' },
};

export const resultChip = (key, assistant = 'The assistant') =>
  RESULT_CHIP[key] ? { ...RESULT_CHIP[key], text: RESULT_CHIP[key].text.replace('{assistant}', assistant) } : null;

// attemptsOf is every run on this alert, numbered from the first, with the shown plan as a dated line among them;
// newest first.
export function attemptsOf(res, rows, assistant = 'The assistant') {
  const out = attemptRuns(rows).map((r, i) => {
    const data = readData(r);
    const phase = runPhase(r);
    const command = runCommand(r);
    const prUrl = r.type === 'PullRequest' && /^https?:\/\//.test(trimmed(r.type_reference_id)) ? trimmed(r.type_reference_id) : '';
    const title =
      runOptionTitle(res, r) ||
      (r.type === 'PullRequest'
        ? 'PR raised'
        : r.type === 'WorkflowExecution' || data.tool === 'automation' || data.kind === 'automation'
        ? 'Automation run'
        : data.slot === 'rollback'
        ? 'Rollback'
        : commandSummary(command) || 'Change');
    return {
      id: `attempt-${r.id}`,
      kind: 'attempt',
      k: i + 1,
      at: r.created_at || r.updated_at || '',
      row: r,
      title,
      result: resultChip(phase.key, assistant),
      reason: phase.reason ? capitalize(phase.reason) : '',
      approver: runApprover(r),
      manual: data.manual === true,
      command: commandLine(command),
      exitCode: data.exit_code_reported === true && typeof data.exit_code === 'number' ? data.exit_code : null,
      prUrl,
    };
  });
  if (planExists(res) && res?.generated_at) {
    out.push({ id: `plan-${trimmed(res?.plan_version) || 'current'}`, kind: 'plan', at: res.generated_at, title: 'Fix options prepared' });
  }
  return out.sort(newestFirst);
}

// planChanges turns plan.revision.changes ("Added: scale_to_zero, restore_image", "Removed: update_image") into
// chips: a sign and the action's title where the slug names one of the plan's actions, else the slug in words.
export function planChanges(res) {
  const rev = res?.revision || {};
  const options = planOptions(res);
  const titleOf = (slug) => {
    const action = options.find((a) => trimmed(a?.action) === slug);
    return action ? optionTitle(action) : capitalize(slug.replace(/[-_]+/g, ' '));
  };
  const SIGN = { added: '+', removed: '−', changed: '~' };
  const out = [];
  (Array.isArray(rev.changes) ? rev.changes : [])
    .map(trimmed)
    .filter(Boolean)
    .forEach((change) => {
      const m = /^(added|removed|changed)\s*:\s*(.+)$/i.exec(change);
      if (!m) {
        out.push({ sign: '', text: change });
        return;
      }
      m[2]
        .split(',')
        .map(trimmed)
        .filter(Boolean)
        .forEach((slug) => out.push({ sign: SIGN[m[1].toLowerCase()], text: titleOf(slug) }));
    });
  return out;
}

// planKind is how the shown fix options came about: 'replan' (the person's note), 'recheck' (a re-check, including a
// plan that answers the person's Re-check though its revision does not say so), or '' (made from the investigation).
export function planKind(res, loop) {
  const kind = trimmed(res?.revision?.kind);
  if (kind === 'replan' || kind === 'recheck') return kind;
  const answersRecheck =
    trimmed(loop?.last_request_mode) === 'recheck' &&
    loop?.last_request_at &&
    (timeOf(res?.generated_at) || 0) >= (timeOf(loop.last_request_at) || Infinity);
  return answersRecheck ? 'recheck' : '';
}

// fixOptionsName is what the tab calls the plan: the runnable form of the investigation's next steps, named for where
// it came from and when: "Fix options · from the investigation at 16:08", "· re-check at 16:40", "· after your note at
// 16:52". The plan's version and hash are details, not its name.
export function fixOptionsName(res, loop) {
  const at = clockOnly(res?.generated_at);
  const kind = planKind(res, loop);
  const from = kind === 'replan' ? 'after your note' : kind === 'recheck' ? 're-check' : 'from the investigation';
  return `Fix options · ${from}${at ? ` at ${at}` : ''}`;
}

// planDetails is the plan's version and hash, for its Details line.
export function planDetails(res) {
  const version = Number.isInteger(res?.version) && res.version > 0 ? `plan version ${res.version}` : '';
  const hash = trimmed(res?.plan_version);
  return [version, hash ? `hash ${hash.slice(0, 12)}` : ''].filter(Boolean).join(', ');
}

// planRow is the shown fix options' row in the timeline: "Fix options prepared", or "Fix options updated (re-check)"
// when they replaced earlier ones, with the changes as chips.
function planRow(res, refire, loop) {
  const rev = res?.revision || {};
  const kind = planKind(res, loop);
  // Options written after the alert came back say so, unless they are the person's own replan.
  const afterRefire = refire?.fired_at && kind !== 'replan' && (timeOf(res.generated_at) || 0) > (timeOf(refire.fired_at) || 0);
  const how = kind === 'replan' ? 'your note' : afterRefire ? 'after the alert came back' : kind === 'recheck' ? 're-check' : '';
  const updated = Boolean(how) || Boolean(trimmed(rev.previous_version)) || (Number.isInteger(res?.version) && res.version > 1);
  return {
    type: 'plan',
    id: `plan-${trimmed(res?.plan_version) || 'current'}`,
    at: res.generated_at,
    icon: 'plan',
    text: `Fix options ${updated ? 'updated' : 'prepared'}${how ? ` (${how})` : ''}`,
    changes: planChanges(res),
  };
}

// The check's status line as "Fixed — the deployment image was restored": the verdict word, then its first clause.
export function checkSentence(summary) {
  const line = summaryStatusLine(summary);
  const m = /^([^;:—-]{1,40})[;:—-]\s*(.+)$/.exec(line);
  if (!m) return line;
  const rest = m[2].split(/[;.](\s|$)/)[0];
  return `${m[1].trim()} — ${rest.trim()}`;
}

// What the re-fire says about the cause, in words.
export const REFIRE_CAUSE = {
  same_cause: 'Same cause is back (the fix was undone or didn’t stick)',
  different: 'A different cause this time',
};

// alertEpisodes groups the alert's firings (loop.firings) into episodes: an episode lasts while any of its firings
// is still firing; the next firing that starts after every firing so far has ended opens a new episode. Repeat
// notifications of a still-firing alert (and its duplicates, which end together) stay in one episode.
export function alertEpisodes(loop) {
  const firings = (Array.isArray(loop?.firings) ? loop.firings : [])
    .filter((f) => f?.starts_at)
    .slice()
    .sort((a, b) => (timeOf(a.starts_at) || 0) - (timeOf(b.starts_at) || 0));
  const episodes = [];
  firings.forEach((f) => {
    const cur = episodes[episodes.length - 1];
    const start = timeOf(f.starts_at) || 0;
    if (cur && (!cur.ended || start <= cur.endedAt)) {
      cur.firings.push(f);
    } else {
      episodes.push({ firings: [f] });
    }
    const ep = episodes[episodes.length - 1];
    ep.ended = ep.firings.every((x) => x.ends_at);
    ep.endedAt = ep.ended ? Math.max(...ep.firings.map((x) => timeOf(x.ends_at) || 0)) : 0;
  });
  return episodes;
}

// cameBackOf says whether the alert came back after a run: the alert cleared after the change (an episode ended after
// the run), and a new episode started after it. {firedAt, minutes (from the run), cause} or null. loop.refire_after_run
// describes only the newest run, so it gives the cause (and the server's minutes) only for that run; without
// loop.firings it is the only source.
export function cameBackOf(row, loop, episodes = alertEpisodes(loop)) {
  if (!row) return null;
  const refire = loop?.refire_after_run;
  const match = refire?.run_id && String(refire.run_id) === String(row.id);
  const ran = timeOf(row.created_at) || 0;
  let firedAt = '';
  if (episodes.length) {
    const i = episodes.findIndex(
      (ep, n) => n > 0 && (timeOf(ep.firings[0].starts_at) || 0) > ran && episodes[n - 1].ended && episodes[n - 1].endedAt >= ran
    );
    if (i > 0) firedAt = episodes[i].firings[0].starts_at;
  } else if (match) {
    firedAt = refire.fired_at || '';
  }
  if (!firedAt) return null;
  const serverMinutes = match ? Number(refire.minutes_after) : NaN;
  return {
    firedAt,
    minutes: Number.isFinite(serverMinutes) ? serverMinutes : Math.round(((timeOf(firedAt) || 0) - ran) / 60000),
    cause: match ? REFIRE_CAUSE[refire.cause] || '' : '',
  };
}

// alertRows are the alert's own transitions, one row each: "Alert fired" (with "still firing · N alerts" when it kept
// notifying), "Alert cleared" once for all firings that ended together, "Alert fired again" for a new episode after a
// clear, and "Still firing since HH:MM · N alerts" for its repeats. The re-fire after a fix is told on its attempt.
// The read lists only the newest firings: when the alert first fired (firstFiredAt, the event's start) before the
// oldest one listed, that first firing leads the timeline at its own time, and the oldest listed episode is told as
// firing on, not as the alert's first firing.
function alertRows(loop, toldAt = [], firstFiredAt = '') {
  const out = [];
  const episodes = alertEpisodes(loop);
  const oldestListed = episodes.length ? timeOf(episodes[0].firings[0].starts_at) || 0 : 0;
  const cut = Boolean(firstFiredAt) && Number.isFinite(timeOf(firstFiredAt)) && oldestListed - timeOf(firstFiredAt) > 60000;
  if (cut) {
    out.push({
      type: 'alert',
      id: 'fired-first',
      kind: 'fired',
      at: firstFiredAt,
      text: 'Alert fired',
      detail: 'Earlier firings are not listed here',
      first: true,
    });
  }
  episodes.forEach((ep, i) => {
    const first = ep.firings[0];
    const n = ep.firings.length;
    const last = ep.firings[n - 1];
    if (i === 0 && cut) {
      out.push({
        type: 'alert',
        id: `firing-${first.event_id || i}`,
        kind: 'still',
        at: first.starts_at,
        eventId: trimmed(last.event_id),
        text: n > 1 ? `Firing · ${n} alerts ${clockOnly(first.starts_at)}–${clockOnly(last.starts_at)}` : 'Firing',
      });
    } else if (i === 0) {
      out.push({
        type: 'alert',
        id: `fired-${first.event_id || i}`,
        kind: 'fired',
        at: first.starts_at,
        eventId: trimmed(first.event_id),
        text: n > 1 ? `Alert fired · still firing, ${n} alerts ${clockOnly(first.starts_at)}–${clockOnly(last.starts_at)}` : 'Alert fired',
        first: true,
      });
    } else {
      const toldOnAttempt = toldAt.some((t) => Math.abs((timeOf(first.starts_at) || 0) - (timeOf(t) || 0)) < 60000);
      if (!toldOnAttempt) {
        out.push({
          type: 'alert',
          id: `fired-${first.event_id || i}`,
          kind: 'fired',
          at: first.starts_at,
          eventId: trimmed(first.event_id),
          text: 'Alert fired again',
        });
      }
      if (n > 1) {
        out.push({
          type: 'alert',
          id: `still-${first.event_id || i}`,
          kind: 'still',
          at: ep.firings[1].starts_at,
          eventId: trimmed(last.event_id),
          text: `Still firing since ${clockOnly(first.starts_at)} · ${n} alerts`,
        });
      }
    }
    if (ep.ended) {
      out.push({
        type: 'alert',
        id: `cleared-${first.event_id || i}`,
        kind: 'cleared',
        at: new Date(ep.endedAt).toISOString(),
        text: 'Alert cleared',
      });
    }
  });
  return out;
}

// cameBackAfter is the re-fire after a recorded run, when no plan has been written since: {run, k, firedAt, minutes,
// cause}. Null otherwise.
export function cameBackAfter({ res, rows, loop }) {
  const runs = attemptRuns(rows);
  const newest = runs[runs.length - 1];
  const back = cameBackOf(newest, loop);
  if (!back) return null;
  // A plan written since the re-fire is the story now.
  if (res?.generated_at && (timeOf(res.generated_at) || 0) > (timeOf(back.firedAt) || 0)) return null;
  return { run: newest, k: runs.length, firedAt: back.firedAt, minutes: back.minutes, cause: back.cause };
}

// An approval the platform closed unanswered (loop.denied_approvals[].reason): its result chip and how it ended.
const CLOSED_APPROVAL = {
  lapsed: { result: 'Lapsed', text: 'Not answered within 12 hours, so it was closed' },
  superseded: { result: 'Superseded', text: 'Closed when the alert cleared and fired again' },
};

const VERDICT_ICON = { fixed: 'fixed', cleared_before_change: 'fixed', not_fixed: 'not-fixed', unclear: 'unclear' };

// timelineOf is the alert's timeline in one order, oldest first: plan rows and attempts by time, each attempt's
// steps oldest first, the step under way last. A step is {key, icon, text, at, live, tone, command, clamp}. Only
// steps the read or the run rows back are listed. `pending` is what this tab knows that the read does not:
// {pick: {option, title, at}, approvals: [...], approvedAt} for the attempt not yet recorded as a run. The read
// carries only the current plan, so only its row is listed.
export function timelineOf({ res, rows, labels, loop, nbStatus, pending = {}, assistant = 'The assistant', firstFiredAt = '' }) {
  const refire = loop?.refire_after_run && loop.refire_after_run.run_id ? loop.refire_after_run : null;
  const episodes = alertEpisodes(loop);
  const toldAt = [];
  const resolved = String(nbStatus || '').toUpperCase() === 'RESOLVED';
  const won = resolved ? winningRun(rows, labels) : null;
  const runs = attemptRuns(rows);
  const heads = attemptsOf(res, rows, assistant).filter((e) => e.kind === 'attempt');
  const items = runs.map((r) => {
    const head = heads.find((h) => h.row === r);
    const data = readData(r);
    const f = data.followup || {};
    const phase = runPhase(r);
    const steps = [];
    const who = runApprover(r);
    if (who)
      steps.push({
        key: 'approved',
        icon: 'approved',
        text: `${data.manual === true ? 'Made by' : 'Approved by'} ${who}${
          data.manual === true ? '' : answerText(data.approved_from, data.approval_note)
        }`,
        at: r.created_at || '',
      });
    const full = runCommand(r);
    const summary = commandSummary(full);
    const exit = data.exit_code_reported === true && typeof data.exit_code === 'number' ? data.exit_code : null;
    const ranText = (word) => `${word}${summary ? ` ${summary}` : ''}${exit !== null ? ` · exit ${exit}` : ''}`;
    const pr = prOf(r);
    if (pr) {
      steps.push({
        key: 'pr',
        icon: 'ran',
        text: pr.url ? 'PR raised' : ['PR raised', pr.number ? `#${pr.number}` : '', PR_STATE_TEXT[pr.state]].filter(Boolean).join(' · '),
        href: pr.url,
        linkText: pr.number ? `#${pr.number}` : 'View PR',
        suffix: PR_STATE_TEXT[pr.state] || '',
        at: r.created_at || '',
      });
      if (pr.state === 'merged') {
        steps.push({
          key: 'pr-merged',
          icon: 'clock',
          text: 'Merged — mark it fixed once it is deployed',
          at: pr.endedAt || r.updated_at || '',
          live: true,
        });
      } else if (pr.state === 'closed') {
        steps.push({ key: 'pr-closed', icon: 'not-fixed', text: 'Closed without merging', at: pr.endedAt || r.updated_at || '' });
      }
    } else if (data.kind === 'automation') {
      // A run of the account's automation (#42459): its state, its run's link, and how it ended when not cleanly.
      const word = { InProgress: 'Running', Success: 'Completed', Failed: 'Failed', Unknown: 'Outcome unknown' }[r.status] || 'Ran';
      const running = r.status === 'InProgress';
      steps.push({
        key: 'ran',
        icon: 'ran',
        tone: r.status === 'Failed' ? 'critical' : '',
        text: `${word}: automation ${trimmed(data.name) || trimmed(data.workflow_id)}`,
        href: trimmed(data.url),
        linkText: 'View run',
        detail: running || r.status === 'Success' ? '' : trimmed(r.status_message),
        at: running ? r.created_at || '' : r.updated_at || r.created_at || '',
        live: running,
      });
    } else if (r.status === 'Failed') {
      steps.push({ key: 'ran', icon: 'ran', tone: 'critical', text: ranText('Failed'), command: full, at: r.updated_at || r.created_at || '' });
    } else if (r.status === 'Success' || r.status === 'Unknown') {
      steps.push({
        key: 'ran',
        icon: 'ran',
        tone: exit ? 'critical' : '',
        // A change a person made outside (no command): marked done, not run.
        text: data.manual === true && !full ? 'Marked done' : ranText('Ran'),
        command: full,
        at: r.updated_at || r.created_at || '',
      });
    } else {
      steps.push({ key: 'ran', icon: 'ran', text: ranText('Running'), command: full, at: r.created_at || '', live: true });
    }
    const over = Boolean(f.verdict || f.done_at);
    if (f.claimed_at && !over && !f.hold_until)
      steps.push({ key: 'checking', icon: 'clock', text: 'Checking the change', at: f.claimed_at, live: true });
    if (f.hold_until && !f.verdict) {
      const at = f.verdict_checked_at || f.claimed_at || '';
      const until = clockOnly(f.hold_until);
      steps.push(
        over
          ? { key: 'held', icon: 'clock', text: `Held until ${until}`, at }
          : { key: 'holding', icon: 'clock', text: `Holding until ${until}`, at, live: true }
      );
    }
    if (f.verdict) {
      const why = plainReason(f.verdict_reason);
      steps.push({
        key: 'recheck',
        icon: VERDICT_ICON[f.verdict] || 'check',
        text: `Re-check: ${why || (RESULT_CHIP[f.verdict]?.text || 'checked').toLowerCase()}`,
        at: f.verdict_at || '',
      });
    } else if (f.done_at) {
      if (phase.key === 'needs_human') {
        steps.push({
          key: 'needs-human',
          icon: 'unclear',
          text: `${assistant} needs a person${phase.reason ? `: ${phase.reason}` : ''}`,
          at: f.done_at,
          clamp: true,
        });
      } else {
        const sentence = checkSentence(f.summary);
        steps.push({
          key: 'nubi-check',
          icon: 'check',
          text: `${assistant}’s check${sentence ? `: ${sentence}` : ' finished'}`,
          at: f.done_at,
          clamp: true,
        });
      }
    }
    if (won && won.id === r.id) {
      const at = (loop?.relevance?.alert_state === 'resolved' && loop?.relevance?.alert_state_at) || f.verdict_at || '';
      steps.push({ key: 'resolved', icon: 'resolved', text: 'Resolved', at });
    }
    // The alert came back after this run: that is the attempt's last word, not its check.
    // Came back after this run, worked out per attempt from the alert's own firings.
    const back = cameBackOf(r, loop, episodes);
    const cameBack = Boolean(back);
    if (back) {
      toldAt.push(back.firedAt);
      steps.push({
        key: 'came-back',
        icon: 'alert',
        tone: 'warning',
        text: `Alert fired again at ${clockOnly(back.firedAt)}${Number.isFinite(back.minutes) ? ` — ${back.minutes} min after this fix` : ''}`,
        detail: back.cause,
        at: back.firedAt,
      });
    }
    // A PR row that names no option is the code fix picked from this tab before it, when there is one.
    const prPick = pending.prPick;
    const pickedTitle =
      pr && !runOptionTitle(res, r) && prPick?.title && (timeOf(r.created_at) || 0) >= (timeOf(prPick.at) || Infinity) - 60 * 1000
        ? prPick.title
        : '';
    return {
      type: 'attempt',
      id: head.id,
      k: head.k,
      title: pickedTitle || head.title,
      result: cameBack ? { text: 'Came back', tone: 'warning' } : head.result,
      phase: cameBack ? 'came_back' : phase.key,
      row: r,
      prUrl: head.prUrl,
      at: r.created_at || '',
      steps,
    };
  });
  // A denied approval wrote no run, but it was an attempt: it stays on the timeline (loop.denied_approvals).
  const options = planOptions(res);
  (Array.isArray(loop?.denied_approvals) ? loop.denied_approvals : [])
    .filter((d) => d?.denied_at)
    .forEach((d) => {
      const option = Number(d.option);
      const known =
        Number.isInteger(option) && option >= 1 && option <= options.length && (!trimmed(d.plan_version) || d.plan_version === res?.plan_version);
      // denied_by is a user id, never shown; the name comes as denied_by_name when the server has it.
      const by = trimmed(d.denied_by_name);
      const title = known ? optionTitle(options[option - 1]) : commandSummary(d.command) || 'Change';
      const closed = CLOSED_APPROVAL[d.reason];
      if (closed) {
        // Closed unanswered by the platform (no one denied it): told at the time it was asked, so one superseded by a
        // new firing sits under the earlier firing, with who asked it.
        const asker = trimmed(d.asked_by_name);
        const askedAt = d.asked_at || d.denied_at;
        items.push({
          type: 'attempt',
          id: `closed-${d.message_id || d.denied_at}`,
          k: 0,
          title,
          result: { text: closed.result, tone: 'neutral' },
          phase: 'denied',
          at: askedAt,
          steps: [
            {
              key: 'asked',
              icon: 'asked',
              text: `${Number.isInteger(option) && option > 0 ? `Option ${option} asked for approval` : 'Asked for approval'}${
                asker ? ` by ${asker}` : ''
              }`,
              command: trimmed(d.command),
              at: askedAt,
            },
            { key: 'closed', icon: 'clock', text: `${closed.text} · nothing ran`, at: d.denied_at },
          ],
        });
        return;
      }
      items.push({
        type: 'attempt',
        id: `denied-${d.message_id || d.denied_at}`,
        k: 0,
        title,
        result: { text: 'Denied', tone: 'neutral' },
        phase: 'denied',
        at: d.denied_at,
        steps: [
          {
            key: 'denied',
            icon: 'asked',
            text: `${Number.isInteger(option) && option > 0 ? `Option ${option} denied` : 'Denied'}${by ? ` by ${by}` : ''}${answerText(
              d.answered_from,
              d.note
            )} · nothing ran`,
            command: trimmed(d.command),
            at: d.denied_at,
          },
        ],
      });
    });
  // The newest re-fire (a later episode of the alert), for "Plan vN · after the alert came back".
  // Only when a change ran before that re-fire in this chain: an alert that fired, cleared on its own and fired again
  // with nothing tried did not "come back" after anything.
  const newestRefire = episodes.length > 1 ? { fired_at: episodes[episodes.length - 1].firings[0].starts_at } : refire;
  const triedBefore = (at) => items.some((it) => it.type === 'attempt' && it.phase !== 'denied' && (timeOf(it.at) || Infinity) < (timeOf(at) || 0));
  const lastRefire = newestRefire?.fired_at && triedBefore(newestRefire.fired_at) ? newestRefire : null;
  if (planExists(res) && res?.generated_at) items.push(planRow(res, lastRefire, loop));
  // An alert transition during an attempt (after its run, up to its last step) is told inside that attempt, in time
  // order, so the attempt reads "ran → alert cleared → check → fired again"; the rest stand as their own rows.
  alertRows(loop, toldAt, firstFiredAt).forEach((alert) => {
    const t = timeOf(alert.at) || 0;
    const host = items.find((it) => {
      if (it.type !== 'attempt') return false;
      const times = it.steps.map((st) => timeOf(st.at)).filter(Number.isFinite);
      return times.length && t >= (timeOf(it.at) || 0) && t <= Math.max(...times);
    });
    if (!host) {
      items.push(alert);
      return;
    }
    const step = { key: `alert-${alert.kind}`, icon: alert.kind === 'cleared' ? 'alert-cleared' : 'alert', text: alert.text, at: alert.at };
    const before = host.steps.findIndex((st) => Number.isFinite(timeOf(st.at)) && (timeOf(st.at) || 0) > t);
    host.steps.splice(before < 0 ? host.steps.length : before, 0, step);
  });
  items.sort((a, b) => (timeOf(a.at) || 0) - (timeOf(b.at) || 0));
  // Attempts are numbered in time order, runs and denied approvals alike.
  let attemptNo = 0;
  items.forEach((it) => {
    if (it.type === 'attempt') {
      attemptNo += 1;
      it.k = attemptNo;
    }
  });
  // The attempt under way that has no run yet comes last: a pick made on this tab, an approval waiting, or one just
  // approved.
  const approvals = pending.approvals || [];
  // Without a pick on this tab, the approval's own command names its option.
  const asked = optionOfCommand(res, approvals[0]?.command);
  if (pending.pick || approvals.length || pending.approvedAt) {
    const steps = [];
    if (pending.pick) {
      steps.push({
        key: 'picked',
        icon: 'approved',
        text: `You picked option ${pending.pick.option} — ${pending.pick.title}`,
        at: pending.pick.at || '',
      });
    }
    approvals.forEach((p) =>
      steps.push({ key: 'asked', icon: 'asked', text: `${assistant} asked for approval`, at: p.created_at || '', live: !pending.approvedAt })
    );
    if (pending.approvedAt)
      steps.push({ key: 'approved', icon: 'approved', text: 'Approved — running the change', at: pending.approvedAt, live: true });
    if (!approvals.length && !pending.approvedAt)
      steps.push({ key: 'preparing', icon: 'clock', text: `${assistant} is preparing the change`, at: pending.pick?.at || '', live: true });
    items.push({
      type: 'attempt',
      id: 'attempt-pending',
      k: attemptNo + 1,
      title: pending.pick?.title || (asked ? optionTitle(planOptions(res)[asked - 1]) : '') || 'Change',
      result: { text: 'In progress', tone: 'info' },
      phase: 'running',
      steps,
      pending: true,
    });
  }
  return items;
}

// clockOnly is a time as HH:MM, 24-hour, in the viewer's zone.
export function clockOnly(value) {
  const t = timeOf(value);
  if (!Number.isFinite(t)) return '';
  const d = new Date(t);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

// dayLabel is "Today" for today, else "23 Oct".
export function dayLabel(value, now = Date.now()) {
  const t = timeOf(value);
  if (!Number.isFinite(t)) return '';
  const d = new Date(t);
  const n = new Date(now);
  if (d.getFullYear() === n.getFullYear() && d.getMonth() === n.getMonth() && d.getDate() === n.getDate()) return 'Today';
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' });
}

// winningRun is the run that resolved the event: the one nb_resolved_by names, else the newest with a fixed or
// cleared-before-change verdict.
export function winningRun(rows, labels) {
  const by = labelObject(labels, 'nb_resolved_by');
  const runs = attemptRuns(rows);
  if (by?.run_id) {
    const named = runs.find((r) => String(r.id) === String(by.run_id));
    if (named) return named;
  }
  return runs.filter((r) => ['fixed', 'cleared_before_change'].includes(readData(r).followup?.verdict)).pop() || null;
}

// durationText: "12 min", "3 h 5 min", "2 days".
export function durationText(fromValue, toValue) {
  const from = timeOf(fromValue);
  const to = timeOf(toValue);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to < from) return '';
  const min = Math.round((to - from) / 60000);
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  if (h < 48) return min % 60 ? `${h} h ${min % 60} min` : `${h} h`;
  return `${Math.round(h / 24)} days`;
}

// resolvedStory is what the resolved card says: who fixed it, what fixed it, how we know, and when. `assistant` is
// the assistant's name, from branding. Lines the read cannot back are left out.
export function resolvedStory({ res, rows, labels, loop, alertStartedAt, assistant }) {
  const run = winningRun(rows, labels);
  const runs = attemptRuns(rows);
  const data = run ? readData(run) : {};
  const f = data.followup || {};
  const clearedOnOwn = f.verdict === 'cleared_before_change' || labelObject(labels, 'nb_resolved_by')?.verdict === 'cleared_before_change';
  const who = run ? runApprover(run) : '';
  const headline = clearedOnOwn
    ? '✓ Resolved — cleared on its own'
    : data.manual === true
    ? `✓ Fixed by ${who || 'a person'}`
    : run
    ? `✓ Fixed by ${assistant}${who ? ` · approved by ${who}` : ''}`
    : '✓ Resolved';
  const what = run && !clearedOnOwn ? [runOptionTitle(res, run), commandLine(runCommand(run))].filter(Boolean).join(' — ') : '';
  const held = run && f.verdict_at ? durationText(run.created_at, f.verdict_at) : '';
  const how = clearedOnOwn
    ? 'The alert cleared before the change ran, so the change is not credited'
    : run && f.verdict === 'fixed' && f.verdict_at
    ? `The alert cleared after the change and was still clear at ${formatClockCompact(f.verdict_at)}${held ? `, ${held} after it ran` : ''}`
    : '';
  // When the person resolved it (confirmed_at); before that field, the alert's own resolve or the verdict.
  const resolvedAt =
    labelObject(labels, 'nb_resolved_by')?.confirmed_at ||
    (loop?.relevance?.alert_state === 'resolved' && loop?.relevance?.alert_state_at) ||
    f.verdict_at ||
    '';
  const after = durationText(alertStartedAt, resolvedAt);
  // Attempts are counted as the Timeline numbers them: runs and denied approvals alike, in time order.
  const denials = (Array.isArray(loop?.denied_approvals) ? loop.denied_approvals : []).map((d) => timeOf(d?.denied_at)).filter(Number.isFinite);
  const runAt = run ? timeOf(run.created_at) : NaN;
  const k = run ? runs.findIndex((r) => r.id === run.id) + 1 + denials.filter((t) => t < runAt).length : 0;
  const when = resolvedAt
    ? [
        `Resolved ${formatClockCompact(resolvedAt)}${confirmedByName(labels) ? ` by ${confirmedByName(labels)}` : ''}`,
        after ? `${after} after the alert` : '',
        k ? `attempt ${k} of ${runs.length + denials.length}` : '',
      ]
        .filter(Boolean)
        .join(' · ')
    : '';
  return { headline, what, how, when, run };
}
