import { unwrapTypedValueString } from '@utils/common';

// Mirrors the llm-server approval check (executor_planner.go): only these answers
// (trimmed, case-insensitive) let the write run; anything else is a rejection.
const APPROVAL_ANSWERS = ['ok', 'yes', 'true'];
const ENDED_STATUSES = ['TERMINATED', 'KILLED', 'FAILED'];
const COMMAND_MARKER = 'Command - ';

const parseConfig = (raw) => {
  if (!raw) {
    return {};
  }
  if (typeof raw === 'object') {
    return raw;
  }
  try {
    return JSON.parse(raw) || {};
  } catch {
    return {};
  }
};

// Tool inputs arrive as JSON like {"command":"…"}; show the bare command, as the
// approval audit does (llm-server followup.go unwrapCommand).
const unwrapCommand = (text) => {
  const trimmed = text.trim();
  if (!trimmed.startsWith('{')) {
    return trimmed;
  }
  try {
    const parsed = JSON.parse(trimmed);
    for (const key of ['command', 'query']) {
      if (typeof parsed[key] === 'string' && parsed[key].trim()) {
        return parsed[key].trim();
      }
    }
    const values = Object.values(parsed);
    if (values.length === 1 && typeof values[0] === 'string') {
      return values[0].trim();
    }
  } catch {
    // not JSON — show as stored
  }
  return trimmed;
};

// A dismissed followup stores either {"resolution":"dismissed","reason":"…"} or a
// plain sentence such as "Conversation terminated by user".
const endedReason = (answer) => {
  if (!answer) {
    return '';
  }
  try {
    const parsed = JSON.parse(answer);
    if (parsed && typeof parsed === 'object') {
      return typeof parsed.reason === 'string' ? parsed.reason : '';
    }
  } catch {
    // plain text
  }
  return answer;
};

const asString = (value) => (typeof value === 'string' ? value : '');

const formatAnswer = (answer, followupType) => {
  if (followupType !== 'multi_select') {
    return answer;
  }
  try {
    const parsed = JSON.parse(answer);
    return Array.isArray(parsed) ? parsed.join(', ') : answer;
  } catch {
    return answer;
  }
};

/**
 * Describes one followup task (as built by useLLMInvestigationControl) for the
 * finished-turn history: what was asked, how it ended, and when.
 *
 * outcome: 'approved' | 'denied' | 'answered' | 'dismissed' | 'unanswered'.
 * A followup still WAITING once its turn has finished was never answered.
 */
export const describeFollowup = (task) => {
  const response = task?.response || {};
  const config = parseConfig(response.message_config);
  const followupType = config.followupType || '';
  const isApproval = followupType === 'tool_confirmation';

  let question = unwrapTypedValueString(asString(config.question) || asString(task?.text)).trim();
  let command = '';
  if (isApproval) {
    const idx = question.lastIndexOf(COMMAND_MARKER);
    if (idx !== -1) {
      command = unwrapCommand(question.slice(idx + COMMAND_MARKER.length));
      question = question.slice(0, idx).trim();
    }
  }

  const answer = asString(response.text).trim();
  const status = response.status;
  let outcome = 'unanswered';
  let detail = '';
  if (status === 'COMPLETED') {
    if (isApproval) {
      outcome = APPROVAL_ANSWERS.includes(answer.toLowerCase()) ? 'approved' : 'denied';
      // A rejection can carry an instruction ("no - just summarize"); keep it.
      detail = outcome === 'denied' && answer.toLowerCase() !== 'no' ? answer : '';
    } else {
      outcome = 'answered';
      detail = formatAnswer(answer, followupType);
    }
  } else if (ENDED_STATUSES.includes(status)) {
    outcome = 'dismissed';
    detail = endedReason(answer);
  }

  // Where an approval's write lands (llm-server followup.go FollowupTarget).
  const target = isApproval && config.target?.name ? config.target : null;

  return {
    isApproval,
    toolName: config.toolName || '',
    target: target
      ? {
          kind: asString(target.kind),
          name: target.name,
          integration: asString(target.integration),
          crossAccount: Boolean(config.crossAccount),
        }
      : null,
    question,
    command,
    outcome,
    detail,
    askedAt: task?.asked_at || null,
    // responded_at equals updated_at on answered rows; for a dismissed row it is
    // when it was dismissed. An unanswered row only has the time it was asked.
    endedAt: outcome === 'unanswered' ? null : task?.last_updated_at || null,
  };
};

export const isFollowupTask = (task) => (task?.tool ?? task?.type) === 'followup-question';

const NEEDS_ATTENTION = ['denied', 'dismissed', 'unanswered'];

const tally = (records) => ({
  count: records.length,
  needsAttention: records.some((r) => NEEDS_ATTENTION.includes(r.outcome)),
});

/**
 * Pill summary for a finished turn's followups, split by kind: write approvals, and
 * clarifications (everything else the assistant asked — which integration, which
 * account, a free-text detail). needsAttention marks a kind where something did not
 * go ahead (denied / dismissed / unanswered) — the part a reviewer should not miss.
 */
export const summarizeFollowups = (followups) => {
  const records = (followups || []).map(describeFollowup);
  return {
    approvals: tally(records.filter((r) => r.isApproval)),
    clarifications: tally(records.filter((r) => !r.isApproval)),
  };
};
