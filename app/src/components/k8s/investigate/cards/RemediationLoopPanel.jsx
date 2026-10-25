import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, CircularProgress } from '@mui/material';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { CodeBlock } from '@ui/CodeBlock';
import { Input } from '@ui/Input';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { DropdownMenu } from '@ui/DropdownMenu';
import {
  CancelOutlined,
  CheckCircleOutlined,
  ChevronRight,
  DescriptionOutlined,
  ExpandMore,
  HowToRegOutlined,
  MoreVert,
  NotificationsOutlined,
  PanToolOutlined,
  ScheduleOutlined,
  TerminalOutlined,
  Verified,
  VisibilityOutlined,
  WarningAmberOutlined,
} from '@mui/icons-material';
import CopyButton from '@shared/buttons/CopyButton';
import { hasWriteAccess } from '@lib/auth';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiKubernetes from '@api1/kubernetes';
import { toAssistantLabel } from '@hooks/useTenantBranding';
import { formatClockCompact } from '@shared/format/Datetime';
import { truncateText } from '@utils/common';
import { OptionalHardening } from './RemediationPanel';
import { readData } from '../resolutionStatus';
import { alertSessionOf } from '../investigationSession';
import {
  alertEpisodes,
  attemptsOf,
  clockOnly,
  dayLabel,
  timelineOf,
  cameBackAfter,
  chosenOption,
  optionDescription,
  optionTitle,
  pendingFor,
  phrase,
  plainReason,
  planExists,
  fixOptionsName,
  planDetails,
  planOptions,
  resolvedStory,
  resultChip,
  runOptionTitle,
  runPhase,
  sameSentence,
  dedupeSentences,
  isPlanStepRun,
  CHANGE_LABEL,
  changeOnly,
  optionMode,
  prOf,
  prRequestText,
  requiredChangeOf,
} from '../remediationLoop';

// The Remediation tab while the tenant's remediation loop is on (#40734). It only reads: the assistant writes the
// plan in the background, every change waits on the assistant's own approval (answered here, in web chat, or in
// Slack when asked from Slack), and the run records say what happened. Three parts: where it stands (one headline,
// one detail, at most one action), the plan, and the attempts. No Run, Decline or "I ran this" here.

// A mitigation restores service while the cause survives: it is labelled for what it does.
const KIND_META = {
  fix: { text: 'Fix', tone: 'success' },
  mitigation: { text: 'Buys time', tone: 'warning' },
};

const REPLAN_REASONS = ['Different cause', 'Too risky', 'Needs a change window', 'We fix this another way', 'Add a step', 'Already done'];

const trimmed = (value) => (typeof value === 'string' ? value.trim() : '');
const textSx = { fontSize: 'var(--ds-text-small)' };
const hintSx = { fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' };
const headSx = { fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-small)' };

// APPROVAL_NOTE_MAX is the longest note the server keeps with an answer (llm-server maxApprovalNote).
export const APPROVAL_NOTE_MAX = 500;

// answerApproval answers the assistant's pending approval the way web chat does: the follow-up response on the
// approval's message in the conversation holding it, "yes" to approve, "no" to deny, with the person's optional note
// (kept on the run record, or on the denial). Without that conversation nothing is sent: the person answers in chat.
export async function answerApproval({ accountId, approval, approve, note = '' }) {
  const conversationId = trimmed(approval?.conversation_id);
  if (!conversationId) throw new Error('Answer it in chat.');
  const reason = trimmed(note).slice(0, APPROVAL_NOTE_MAX);
  const res = await apiAskNudgebee.aiFollowupResponse({
    account_id: accountId,
    conversation_id: conversationId,
    message_id: approval.message_id,
    agent_id: approval.agent_id,
    query: approve ? 'yes' : 'no',
    ...(reason ? { reason } : {}),
  });
  const errors = res?.data?.errors;
  if (Array.isArray(errors) && errors.length) throw new Error(errors[0]?.message || 'Could not send the answer.');
  return res;
}

// sendToConversation posts a message into the alert's conversation as the signed-in person, the same call the chat's
// own composer makes, scoped to the event. A pick ({option, kind, planVersion}) is sent as a structured pick: the
// server checks it (write access, the fixes still current, nothing under way) and writes the message's words. Throws
// with the server's reason when the message was not taken.
export async function sendToConversation({ accountId, sessionId, eventId, text, pick }) {
  if (!sessionId) throw new Error('This event has no conversation to send to.');
  const res = await apiAskNudgebee.aiGenerateInvestigate({
    account_id: accountId,
    query: text,
    session_id: sessionId,
    ...(eventId ? { config: { event_id: eventId } } : {}),
    ...(pick ? { remediation_pick: { event_id: eventId || '', plan_version: pick.planVersion || '', option: pick.option, kind: pick.kind } } : {}),
  });
  const result = res?.data?.data?.ai_execute_investigation;
  const errors = res?.data?.errors?.length ? res.data.errors : result?.errors;
  if ((Array.isArray(errors) && errors.length) || !result?.data?.query) {
    throw new Error(errors?.[0]?.message || 'The message was not sent. Try again.');
  }
  return result.data;
}

// CommandBlock is a command on one scrolling line with its copy button beside it, so the button never covers the end.
function CommandBlock({ command }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <CodeBlock code={command} tone='dark' showCopy={false} />
      </Box>
      <CopyButton text={command} size='sm' toastMessage='Command copied' />
    </Box>
  );
}

CommandBlock.propTypes = { command: PropTypes.string.isRequired };

function OptionRow({
  index,
  action,
  disabled,
  onAsk,
  onRaisePr,
  onMarkDone,
  marking,
  askLabel,
  assistantLabel,
  sending,
  error,
  showAsk = true,
  showConfidence = true,
}) {
  const [details, setDetails] = useState(false);
  // A change no command applies: a code fix (raised as a PR from the alert's chat when it names a repository), or a
  // change a person makes and then marks done. It is never offered as "Use this fix" with a command.
  const mode = optionMode(action);
  const change = requiredChangeOf(action);
  const kind = change
    ? { text: CHANGE_LABEL[change.where] || 'Change to make', tone: 'success' }
    : KIND_META[trimmed(action?.kind).toLowerCase()] || null;
  const confidence = typeof action?.confidence === 'number' ? Math.max(0, Math.min(100, Math.round(action.confidence))) : null;
  const tradeOff = trimmed(action?.trade_off);
  const undo = trimmed(action?.rollback_command);
  const run = mode === 'run' ? trimmed(action?.execute_command) : '';
  const warning = mode === 'run' && trimmed(action?.delivery) === 'operator' ? trimmed(action?.delivery_reason) || 'A person runs this one.' : '';
  let askButton = null;
  if (showAsk && mode === 'pr') {
    askButton = (
      <Button
        tone='secondary'
        size='xs'
        disabled={disabled || sending}
        onClick={() => onRaisePr?.(index, action)}
        data-testid={`remediation-loop-raise-pr-${index}`}
      >
        {sending ? `Asking ${assistantLabel}…` : 'Raise a PR'}
      </Button>
    );
  } else if (showAsk && mode === 'manual') {
    askButton = (
      <Button
        tone='secondary'
        size='xs'
        disabled={disabled || marking || !onMarkDone}
        loading={marking}
        onClick={() => onMarkDone?.(index, action)}
        data-testid={`remediation-loop-mark-done-${index}`}
      >
        Mark done
      </Button>
    );
  } else if (showAsk) {
    askButton = (
      <Button
        tone='secondary'
        size='xs'
        disabled={disabled || sending}
        onClick={() => onAsk(index, action)}
        data-testid={`remediation-loop-ask-option-${index}`}
      >
        {sending ? `Asking ${assistantLabel}…` : askLabel || 'Use this fix'}
      </Button>
    );
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', minWidth: 0 }} data-testid={`remediation-loop-option-${index}`}>
      <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--ds-space-2)', minWidth: 0 }}>
          <Box component='span' sx={headSx}>{`${index} · ${optionTitle(action)}`}</Box>
          {kind ? <Label tone={kind.tone} size='sm' text={kind.text} /> : null}
          {/* Only a number the plan carries and that tells the options apart, said for what it is. */}
          {showConfidence && confidence ? (
            <Box component='span' sx={{ ...textSx, color: 'var(--ds-gray-500)' }}>{`${confidence}% likely to resolve`}</Box>
          ) : null}
        </Box>
        {askButton ? <Box sx={{ flexShrink: 0 }}>{askButton}</Box> : null}
      </Box>
      {optionDescription(action) ? <Box sx={hintSx}>{optionDescription(action)}</Box> : null}
      {change ? (
        <Box
          sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', ...textSx }}
          data-testid={`remediation-loop-option-change-${index}`}
        >
          {change.target ? (
            <Box sx={{ overflowWrap: 'anywhere' }}>
              <Box component='span' sx={{ color: 'var(--ds-gray-600)' }}>
                {'Where: '}
              </Box>
              <Box component='code' sx={{ fontFamily: 'var(--ds-font-mono)' }}>
                {change.target}
              </Box>
              {change.repo ? <Box component='span' sx={{ color: 'var(--ds-gray-600)' }}>{` · repo ${change.repo}`}</Box> : null}
            </Box>
          ) : null}
          <Box sx={{ overflowWrap: 'anywhere' }}>
            <Box component='span' sx={{ color: 'var(--ds-gray-600)' }}>
              {'Change: '}
            </Box>
            {change.change}
          </Box>
          {change.reason ? <Box sx={hintSx}>{`Why: ${change.reason}`}</Box> : null}
          {mode === 'manual' ? (
            <Box sx={hintSx} data-testid={`remediation-loop-option-manual-${index}`}>
              Make this change yourself, then mark it done.
            </Box>
          ) : null}
        </Box>
      ) : null}
      {/* What runs, on one line that scrolls rather than breaking mid-word. */}
      {run ? (
        <Box data-testid={`remediation-loop-option-command-${index}`}>
          <CommandBlock command={run} />
        </Box>
      ) : null}
      {warning ? <Box sx={{ ...textSx, color: 'var(--ds-amber-700)' }}>{warning}</Box> : null}
      {tradeOff || undo ? (
        <Box>
          <Button
            tone='link'
            size='xs'
            onClick={() => setDetails((v) => !v)}
            aria-expanded={details}
            data-testid={`remediation-loop-option-details-${index}`}
          >
            {details ? 'Hide details' : 'Details'}
          </Button>
        </Box>
      ) : null}
      {details && tradeOff ? <Box sx={hintSx}>{`Trade-off: ${tradeOff}`}</Box> : null}
      {details && undo ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
          <Box sx={hintSx}>Undo:</Box>
          <CommandBlock command={undo} />
        </Box>
      ) : null}
      {error ? (
        <Box sx={{ ...textSx, color: 'var(--ds-red-600)' }} data-testid={`remediation-loop-option-error-${index}`}>
          {error}
        </Box>
      ) : null}
    </Box>
  );
}

OptionRow.propTypes = {
  index: PropTypes.number.isRequired,
  action: PropTypes.object.isRequired,
  disabled: PropTypes.bool,
  onAsk: PropTypes.func.isRequired,
  // A code fix with a repository: sends the PR request into the alert's chat.
  onRaisePr: PropTypes.func,
  // A change the person makes: records it as done (the plan step's manual decision).
  onMarkDone: PropTypes.func,
  marking: PropTypes.bool,
  // The run option's button text ("Use this fix anyway" when the investigation is newer than the plan).
  askLabel: PropTypes.string,
  assistantLabel: PropTypes.string.isRequired,
  sending: PropTypes.bool,
  error: PropTypes.node,
  // The "Use this fix" button: only while the person is picking.
  showAsk: PropTypes.bool,
  // Confidence is shown only when it tells the options apart.
  showConfidence: PropTypes.bool,
};

// ClampText is a labelled paragraph kept to two lines, with "more" when it is longer.
function ClampText({ label, text, testId }) {
  const [open, setOpen] = useState(false);
  const long = text.length > 140;
  return (
    <Box sx={textSx} data-testid={testId}>
      <Box sx={long && !open ? { display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' } : undefined}>
        <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>{`${label} `}</Box>
        {text}
      </Box>
      {long ? (
        <Button tone='link' size='xs' onClick={() => setOpen((v) => !v)}>
          {open ? 'less' : 'more'}
        </Button>
      ) : null}
    </Box>
  );
}

ClampText.propTypes = { label: PropTypes.string.isRequired, text: PropTypes.string.isRequired, testId: PropTypes.string };

// FirstSentence shows a text's first sentence, with "more" for the rest (a server reason can be a long answer excerpt).
function FirstSentence({ text, testId }) {
  const [open, setOpen] = useState(false);
  const full = text.replace(/\s+/g, ' ').trim();
  const m = /^(.+?[.!?])(\s|$)/.exec(full);
  const first = m ? m[1] : full;
  const more = first.length < full.length;
  return (
    <Box component='span' sx={{ minWidth: 0 }} data-testid={testId}>
      {open || !more ? full : first}
      {more ? (
        <Button tone='link' size='xs' onClick={() => setOpen((v) => !v)} data-testid={testId ? `${testId}-more` : undefined}>
          {open ? 'less' : 'more'}
        </Button>
      ) : null}
    </Box>
  );
}

FirstSentence.propTypes = { text: PropTypes.string.isRequired, testId: PropTypes.string };

function ReplanForm({ onSubmit, onCancel, assistantLabel, canAct }) {
  const [reasons, setReasons] = useState([]);
  const [note, setNote] = useState('');
  const [sending, setSending] = useState(false);
  const [message, setMessage] = useState(null);
  const toggle = (r) => setReasons((prev) => (prev.includes(r) ? prev.filter((x) => x !== r) : [...prev, r]));
  const submit = async () => {
    setSending(true);
    setMessage(null);
    try {
      await onSubmit({ reasons, note: note.trim() });
      setReasons([]);
      setNote('');
    } catch (e) {
      setMessage({ tone: 'critical', text: (e?.message || 'Could not ask for other fixes.').replace(/^remediation: /, '') });
    } finally {
      setSending(false);
    }
  };
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }} data-testid='remediation-loop-replan'>
      <Box sx={headSx}>{`Other fixes: what should ${assistantLabel} change?`}</Box>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-1)' }}>
        {REPLAN_REASONS.map((r) => (
          <Chip key={r} variant='filter' size='xs' pressed={reasons.includes(r)} onClick={() => toggle(r)}>
            {r}
          </Chip>
        ))}
      </Box>
      <Input
        type='textarea'
        minRows={2}
        value={note}
        onChange={setNote}
        placeholder='What is wrong with these options, or what should the fix do instead?'
        id='remediation-loop-replan-note'
        data-testid='remediation-loop-replan-note'
      />
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
        <Button
          tone='primary'
          size='xs'
          onClick={submit}
          disabled={sending || !canAct || (!reasons.length && !note.trim())}
          data-testid='remediation-loop-replan-submit'
        >
          {sending ? `Asking ${assistantLabel}…` : `Ask ${assistantLabel} for other fixes`}
        </Button>
        <Button tone='secondary' size='xs' onClick={onCancel} disabled={sending}>
          Cancel
        </Button>
      </Box>
      <Box
        sx={hintSx}
      >{`${assistantLabel} prepares new fix options in the background and says what changed. Your note is posted in the alert’s chat.`}</Box>
      {message ? <Box sx={{ ...textSx, color: message.tone === 'critical' ? 'var(--ds-red-600)' : 'var(--ds-gray-600)' }}>{message.text}</Box> : null}
    </Box>
  );
}

ReplanForm.propTypes = {
  onSubmit: PropTypes.func.isRequired,
  onCancel: PropTypes.func.isRequired,
  assistantLabel: PropTypes.string.isRequired,
  canAct: PropTypes.bool,
};

// refreshedAt: the investigation finished after this plan was written (loop.investigation_after_plan.at). The step
// can still be approved as it is; denying it lets the assistant re-check the plan against the new investigation.
function ApprovalCard({ approval, canAct, onAnswer, refreshedAt, assistantLabel }) {
  const [sending, setSending] = useState('');
  const [error, setError] = useState('');
  const [sent, setSent] = useState('');
  // An optional note with the answer, kept on the run record (or the denial) and shown on the timeline.
  const [noteOpen, setNoteOpen] = useState(false);
  const [note, setNote] = useState('');
  const answer = async (approve) => {
    setSending(approve ? 'approve' : 'deny');
    setError('');
    try {
      await onAnswer(approval, approve, note.trim());
      setSent(
        approve
          ? 'Approved. It runs once and the result is recorded below.'
          : refreshedAt
          ? `Denied. Nothing runs, and ${assistantLabel || 'the assistant'} re-checks the fix.`
          : 'Denied. Nothing runs.'
      );
    } catch (e) {
      setError(e?.message || 'Could not send the answer.');
    } finally {
      setSending('');
    }
  };
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }} data-testid={`remediation-loop-approval-${approval.message_id}`}>
      {refreshedAt && clockOnly(refreshedAt) ? (
        <Box sx={{ ...textSx, color: 'var(--ds-amber-700)' }} data-testid='remediation-loop-approval-refreshed'>
          {`The investigation was refreshed at ${clockOnly(refreshedAt)}, after these fixes`}
        </Box>
      ) : null}
      <CodeBlock code={trimmed(approval.command) || '(no command shown)'} language='bash' tone='dark' wrap copyToast='Command copied' />
      {sent ? (
        <Box sx={hintSx} data-testid='remediation-loop-approval-sent'>
          {sent}
        </Box>
      ) : (
        <>
          {noteOpen ? (
            <Input
              type='textarea'
              minRows={2}
              value={note}
              onChange={(v) => setNote(String(v ?? '').slice(0, APPROVAL_NOTE_MAX))}
              placeholder='Optional: why you approve or deny it (kept with the decision)'
              id={`remediation-loop-approval-note-${approval.message_id}`}
              data-testid='remediation-loop-approval-note'
            />
          ) : null}
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center' }}>
            <Button
              tone='primary'
              size='xs'
              onClick={() => answer(true)}
              loading={sending === 'approve'}
              disabled={!canAct || !!sending}
              tooltip={!canAct ? 'Requires write access' : undefined}
              data-testid='remediation-loop-approve'
            >
              {refreshedAt ? 'Approve anyway' : 'Approve'}
            </Button>
            <Button
              tone='secondary'
              size='xs'
              onClick={() => answer(false)}
              loading={sending === 'deny'}
              disabled={!canAct || !!sending}
              data-testid='remediation-loop-deny'
            >
              {refreshedAt ? 'Deny and re-check the fix' : 'Deny'}
            </Button>
            {!noteOpen ? (
              <Button
                tone='ghost'
                size='xs'
                onClick={() => setNoteOpen(true)}
                disabled={!canAct || !!sending}
                data-testid='remediation-loop-approval-add-note'
              >
                Add a note
              </Button>
            ) : null}
          </Box>
        </>
      )}
      {error ? <Box sx={{ ...textSx, color: 'var(--ds-red-600)' }}>{error}</Box> : null}
      <Box sx={hintSx}>
        This is the same approval as in chat: answering it here answers it there. It runs this exact command once and records who approved it.
      </Box>
    </Box>
  );
}

ApprovalCard.propTypes = {
  approval: PropTypes.object.isRequired,
  canAct: PropTypes.bool,
  onAnswer: PropTypes.func.isRequired,
  refreshedAt: PropTypes.string,
  assistantLabel: PropTypes.string,
};

// The rail's icon per row type, and its colour.
const RAIL_ICON = {
  plan: { Icon: DescriptionOutlined, color: 'var(--ds-gray-600)' },
  asked: { Icon: PanToolOutlined, color: 'var(--ds-gray-600)' },
  approved: { Icon: HowToRegOutlined, color: 'var(--ds-gray-600)' },
  ran: { Icon: TerminalOutlined, color: 'var(--ds-gray-600)' },
  clock: { Icon: ScheduleOutlined, color: 'var(--ds-gray-600)' },
  check: { Icon: VisibilityOutlined, color: 'var(--ds-gray-600)' },
  fixed: { Icon: CheckCircleOutlined, color: 'var(--ds-green-600)' },
  'not-fixed': { Icon: CancelOutlined, color: 'var(--ds-red-600)' },
  unclear: { Icon: WarningAmberOutlined, color: 'var(--ds-amber-600)' },
  resolved: { Icon: Verified, color: 'var(--ds-green-600)' },
  alert: { Icon: NotificationsOutlined, color: 'var(--ds-amber-600)' },
  'alert-cleared': { Icon: NotificationsOutlined, color: 'var(--ds-gray-600)' },
};

// An attempt's left accent, by how it ended.
const ATTEMPT_ACCENT = {
  fixed: 'var(--ds-green-500)',
  cleared_before_change: 'var(--ds-green-500)',
  not_fixed: 'var(--ds-red-500)',
  unclear: 'var(--ds-amber-500)',
  needs_human: 'var(--ds-amber-500)',
  checked: 'var(--ds-gray-400)',
  came_back: 'var(--ds-amber-500)',
  denied: 'var(--ds-gray-400)',
};

const timeSx = { ...textSx, color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums', width: 40, flexShrink: 0, pt: 'var(--ds-space-0)' };

// The rail: a thin line down the dot column (time column 40 + gap 8 + half a 20px dot).
const railLineSx = {
  '&::before': {
    content: '""',
    position: 'absolute',
    top: 'var(--ds-space-2)',
    bottom: 'var(--ds-space-2)',
    left: 57,
    width: '1px',
    backgroundColor: 'var(--ds-gray-200)',
  },
};

// RailRow is one row of the timeline: its time, its dot on the rail, and what happened.
function RailRow({ icon, tone, live, at, testId, children }) {
  const meta = RAIL_ICON[icon] || RAIL_ICON.check;
  const color = tone === 'critical' ? 'var(--ds-red-600)' : tone === 'warning' ? 'var(--ds-amber-600)' : meta.color;
  return (
    <Box
      component='li'
      sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)', minWidth: 0 }}
      data-testid={testId}
      data-icon={icon}
    >
      <Box component='span' sx={timeSx}>
        {clockOnly(at)}
      </Box>
      <Box
        component='span'
        sx={{
          position: 'relative',
          zIndex: 1,
          flexShrink: 0,
          width: 20,
          height: 20,
          borderRadius: 'var(--ds-radius-pill)',
          border: '1px solid var(--ds-gray-200)',
          backgroundColor: 'var(--ds-background-100)',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          color,
          '& svg': { fontSize: 12 },
        }}
        data-testid='remediation-loop-rail-dot'
      >
        {live ? <CircularProgress size={10} thickness={6} data-testid='remediation-loop-step-live' /> : <meta.Icon />}
      </Box>
      <Box sx={{ ...textSx, minWidth: 0, flex: 1, pt: 'var(--ds-space-0)', overflowWrap: 'anywhere' }}>{children}</Box>
    </Box>
  );
}

RailRow.propTypes = {
  icon: PropTypes.string.isRequired,
  tone: PropTypes.string,
  live: PropTypes.bool,
  at: PropTypes.string,
  testId: PropTypes.string,
  children: PropTypes.node,
};

// CommandLine shows a command on one line, cut with an ellipsis; a click shows it whole, on a line that scrolls.
function CommandLine({ command }) {
  const [open, setOpen] = useState(false);
  if (!command) return null;
  return open ? (
    <Box
      sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', mt: 'var(--ds-space-1)' }}
      data-testid='remediation-loop-command-open'
    >
      <CommandBlock command={command} />
      <Box>
        <Button tone='link' size='xs' onClick={() => setOpen(false)}>
          Show less
        </Button>
      </Box>
    </Box>
  ) : (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0, mt: 'var(--ds-space-1)' }}>
      <Box
        component='button'
        type='button'
        title={command}
        aria-label='Show the whole command'
        onClick={() => setOpen(true)}
        sx={{
          border: 'none',
          textAlign: 'left',
          display: 'block',
          flex: 1,
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          fontFamily: 'var(--ds-font-mono)',
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-600)',
          backgroundColor: 'var(--ds-gray-100)',
          borderRadius: 'var(--ds-radius-sm)',
          px: 'var(--ds-space-2)',
          py: 'var(--ds-space-0)',
          cursor: 'pointer',
        }}
        data-testid='remediation-loop-command-line'
      >
        {command}
      </Box>
      <CopyButton text={command} size='xs' toastMessage='Command copied' />
    </Box>
  );
}

CommandLine.propTypes = { command: PropTypes.string };

// ClampLine keeps a sentence to two lines, with "more" when it is longer.
function ClampLine({ text }) {
  const [open, setOpen] = useState(false);
  const long = text.length > 120;
  return (
    <Box>
      <Box
        sx={{
          minWidth: 0,
          overflowWrap: 'anywhere',
          ...(long && !open ? { display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' } : null),
        }}
      >
        {text}
      </Box>
      {long ? (
        <Button tone='link' size='xs' onClick={() => setOpen((v) => !v)}>
          {open ? 'less' : 'more'}
        </Button>
      ) : null}
    </Box>
  );
}

ClampLine.propTypes = { text: PropTypes.string.isRequired };

function TimelineList({ items, onOpenChat, onOpenEvent }) {
  const [alertRunsOpen, setAlertRunsOpen] = useState({});
  // The latest attempt is open; older ones open on click.
  const [open, setOpen] = useState({});
  if (!items.length) return null;
  const attempts = items.filter((it) => it.type === 'attempt');
  const latestId = attempts.length ? attempts[attempts.length - 1].id : '';
  let lastDay = dayLabel(new Date().toISOString());
  // A day row only where the day changes (and above the first row when that is not today).
  const dayRow = (at, key) => {
    const day = dayLabel(at);
    if (!day || day === lastDay) return null;
    lastDay = day;
    return (
      <Box
        component='li'
        key={`day-${key}`}
        sx={{ ...textSx, color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-medium)', py: 'var(--ds-space-1)' }}
        data-testid='remediation-loop-day'
      >
        {day}
      </Box>
    );
  };
  const listSx = { m: 0, p: 0, listStyle: 'none', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' };
  const rows = [];
  const alertRow = (a) => (
    <RailRow key={a.id} icon={a.kind === 'cleared' ? 'alert-cleared' : 'alert'} at={a.at} testId={`remediation-loop-alert-${a.kind}`}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <Box component='span'>{a.text}</Box>
        {a.detail ? (
          <Box component='span' sx={hintSx}>
            {a.detail}
          </Box>
        ) : null}
        {a.kind === 'fired' && !a.first && a.eventId && onOpenEvent ? (
          <Button tone='link' size='xs' onClick={() => onOpenEvent(a.eventId)} data-testid='remediation-loop-alert-open'>
            open ↗
          </Button>
        ) : null}
      </Box>
    </RailRow>
  );
  // Consecutive alert rows: when the alert fired again three or more times with nothing in between, the run folds
  // into "Alert fired again · N more times since", which opens on click.
  const groups = [];
  items.forEach((a) => {
    const last = groups[groups.length - 1];
    if (a.type === 'alert' && last?.alerts) last.alerts.push(a);
    else groups.push(a.type === 'alert' ? { alerts: [a] } : { item: a });
  });
  const flat = [];
  groups.forEach((g, gi) => {
    if (!g.alerts) {
      flat.push(g.item);
      return;
    }
    const fired = g.alerts.filter((a) => a.kind === 'fired');
    if (fired.length < 3 || alertRunsOpen[gi]) {
      g.alerts.forEach((a) => flat.push(a));
      return;
    }
    const firstFired = g.alerts.indexOf(fired[0]);
    g.alerts.slice(0, firstFired + 1).forEach((a) => flat.push(a));
    flat.push({ type: 'alert-more', id: `alert-more-${gi}`, at: fired[0].at, more: fired.length - 1, group: gi });
  });
  flat.forEach((a) => {
    if (a.type === 'alert') {
      const d = dayRow(a.at, a.id);
      if (d) rows.push(d);
      rows.push(alertRow(a));
      return;
    }
    if (a.type === 'alert-more') {
      rows.push(
        <RailRow key={a.id} icon='alert' at='' testId='remediation-loop-alert-more'>
          <Button tone='link' size='xs' onClick={() => setAlertRunsOpen((o) => ({ ...o, [a.group]: true }))}>
            {`Alert fired again · ${a.more} more ${a.more === 1 ? 'time' : 'times'} since`}
          </Button>
        </RailRow>
      );
      return;
    }
    if (a.type === 'plan') {
      const d = dayRow(a.at, a.id);
      if (d) rows.push(d);
      rows.push(
        <RailRow key={a.id} icon='plan' at={a.at} testId='remediation-loop-timeline-plan'>
          <Box sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>{a.text}</Box>
          {a.changes.some((c) => c.sign) ? (
            <Box
              sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-1)', mt: 'var(--ds-space-1)', minWidth: 0 }}
              data-testid='remediation-loop-timeline-plan-changes'
            >
              {a.changes
                .filter((c) => c.sign)
                .map((c, i) => (
                  <Chip key={i} variant='tag' size='2xs' tone='subtle' displayTooltip tooltipCharLimit={40}>
                    {`${c.sign} ${c.text}`}
                  </Chip>
                ))}
            </Box>
          ) : null}
          {/* Changes that are sentences ("Cause: …") read as text, two lines at most, inside the column. */}
          {a.changes
            .filter((c) => !c.sign)
            .map((c, i) => (
              <Box key={`text-${i}`} sx={{ ...hintSx, mt: 'var(--ds-space-1)' }} data-testid='remediation-loop-timeline-plan-note'>
                <ClampLine text={c.text} />
              </Box>
            ))}
        </RailRow>
      );
      return;
    }
    const isLatest = a.id === latestId;
    const isOpen = open[a.id] ?? isLatest;
    const d = dayRow(a.at || a.steps[0]?.at, a.id);
    if (d) rows.push(d);
    const stepRows = [];
    if (isOpen) {
      a.steps.forEach((step, n) => {
        const sd = dayRow(step.at, `${a.id}-${n}`);
        if (sd) stepRows.push(sd);
        stepRows.push(
          <RailRow
            key={`${step.key}-${n}`}
            icon={step.icon}
            tone={step.tone}
            live={Boolean(step.live && isLatest)}
            at={step.at}
            testId={`remediation-loop-step-${step.key}`}
          >
            {step.clamp ? (
              <ClampLine text={step.text} />
            ) : step.href ? (
              // "PR raised · #123 ↗ · open"
              <Box sx={{ overflowWrap: 'anywhere' }}>
                {`${step.text} · `}
                <Link href={step.href} openInNew secondaryText>
                  {`${step.linkText || 'View'} ↗`}
                </Link>
                {step.suffix ? ` · ${step.suffix}` : ''}
              </Box>
            ) : (
              <Box sx={{ overflowWrap: 'anywhere' }}>{step.text}</Box>
            )}
            {step.detail ? <Box sx={{ ...hintSx, overflowWrap: 'anywhere' }}>{step.detail}</Box> : null}
            {step.command ? <CommandLine command={step.command} /> : null}
          </RailRow>
        );
      });
    }
    rows.push(
      <Box
        component='li'
        key={a.id}
        sx={{
          border: '1px solid var(--ds-gray-200)',
          borderLeft: `3px solid ${ATTEMPT_ACCENT[a.phase] || 'var(--ds-blue-500)'}`,
          borderRadius: 'var(--ds-radius-md)',
          position: 'relative',
          backgroundColor: 'var(--ds-background-100)',
          p: 'var(--ds-space-2)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--ds-space-2)',
          minWidth: 0,
        }}
        data-testid='remediation-loop-activity-run'
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
          <Box sx={{ flexShrink: 0 }}>
            <Button
              tone='ghost'
              size='xs'
              composition='icon-only'
              icon={isOpen ? <ExpandMore /> : <ChevronRight />}
              onClick={() => setOpen((o) => ({ ...o, [a.id]: !isOpen }))}
              aria-expanded={isOpen}
              aria-label={isOpen ? `Hide the steps of attempt ${a.k}` : `Show the steps of attempt ${a.k}`}
              data-testid='remediation-loop-attempt-toggle'
            />
          </Box>
          <Box
            component='span'
            title={a.title}
            sx={{
              ...textSx,
              fontWeight: 'var(--ds-font-weight-medium)',
              minWidth: 0,
              flex: 1,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
            data-testid='remediation-loop-attempt-title'
          >
            {`Attempt ${a.k} · ${a.title}`}
          </Box>
          {a.result ? (
            <Box sx={{ flexShrink: 0 }}>
              <Label tone={a.result.tone} size='sm' text={a.result.text} textTransform='none' />
            </Box>
          ) : null}
          <Box sx={{ flexShrink: 0 }}>
            {a.prUrl ? (
              <Link href={a.prUrl} openInNew secondaryText>
                View PR
              </Link>
            ) : (
              <Button tone='link' size='xs' onClick={() => onOpenChat()} data-testid='remediation-loop-activity-chat'>
                in chat ↗
              </Button>
            )}
          </Box>
        </Box>
        {isOpen ? (
          <Box component='ol' sx={{ ...listSx, position: 'relative', ...railLineSx }}>
            {stepRows}
          </Box>
        ) : null}
      </Box>
    );
  });
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }} data-testid='remediation-loop-activity'>
      <Box sx={headSx}>Timeline</Box>
      <Box component='ol' sx={{ ...listSx, position: 'relative', ...railLineSx }} data-testid='remediation-loop-timeline-rows'>
        {rows}
      </Box>
    </Box>
  );
}

TimelineList.propTypes = {
  items: PropTypes.array.isRequired,
  onOpenChat: PropTypes.func.isRequired,
  onOpenEvent: PropTypes.func,
};

// friendlySendError: the server's reason for a message that was not taken, in the person's words where we know them.
function friendlySendError(message, assistantLabel) {
  const m = trimmed(message);
  if (/terminated and cannot be resumed/i.test(m)) return `This alert’s chat was interrupted — ${assistantLabel} can’t continue it`;
  if (/conversation is in progress/i.test(m)) return `${assistantLabel} is still finishing its last reply — try again in a moment`;
  return m || 'The message was not sent. Try again.';
}

const timeMs = (value) => (value ? new Date(value).getTime() : NaN);

const RemediationLoopPanel = ({
  accountId,
  eventId,
  fingerprint,
  nbStatus,
  remediation,
  resolutions,
  labels,
  alertStartedAt,
  alertTitle,
  investigation,
  assistantName,
  onOpenChat,
  onOpenEvent,
  onReload,
  onResolved,
  onWaitingChange,
  onReinvestigate,
  reinvestigateDisabled,
  moreActions,
  headerExtras,
}) => {
  const loop = remediation?.loop || {};
  const assistantLabel = toAssistantLabel(assistantName) || 'The assistant';
  const canAct = hasWriteAccess(accountId);
  const [replanOpen, setReplanOpen] = useState(false);
  const [planOpen, setPlanOpen] = useState(false);
  const [planDetailsOpen, setPlanDetailsOpen] = useState(false);
  // The investigation's progress (the analysis card's): the fix is made from it once it completes.
  const investigationRunning = investigation?.state === 'running';
  const [askState, setAskState] = useState({ sending: '', message: '' });
  // "Resolve" on a likely-fixed attempt: the event is resolved in this person's name (api-server checks the run's
  // verdict and their write access).
  const [resolveState, setResolveState] = useState({ sending: false, error: '' });
  const confirmResolve = async (run) => {
    if (resolveState.sending || !run?.id || !eventId) return;
    setResolveState({ sending: true, error: '' });
    try {
      await apiKubernetes.confirmRemediationResolve({ accountId, eventId: String(eventId), runId: String(run.id) });
      setResolveState({ sending: false, error: '' });
      onResolved?.();
      onReload?.();
    } catch (e) {
      setResolveState({ sending: false, error: e?.message || 'Could not resolve the event.' });
    }
  };
  const options = planOptions(remediation);
  const hardening = Array.isArray(remediation?.optional_hardening) ? remediation.optional_hardening.filter((h) => trimmed(h?.change)) : [];
  const hasPlan = planExists(remediation);
  const preparing = loop.plan_status === 'preparing';
  // An investigation finished after this plan was written: the plan still stands (a step of it can be approved), and
  // the assistant re-checks it once nothing waits on an approval (loop.parked_recheck while one does).
  const refreshedAfterPlan = trimmed(loop.investigation_after_plan?.at);
  // Denied here while the investigation was newer than the plan: the parked re-check starts on the planner's next
  // tick (about a minute). {at, sawPreparing}; it ends when the read says preparing and that ends, a newer plan
  // lands, or 3 minutes pass without preparing (the re-check was dropped).
  const [recheckAsked, setRecheckAsked] = useState(null);
  useEffect(() => {
    if (!recheckAsked) return;
    const landed = remediation?.generated_at && (timeMs(remediation.generated_at) || 0) > recheckAsked.at;
    if (preparing) {
      if (!recheckAsked.sawPreparing) setRecheckAsked((r) => (r ? { ...r, sawPreparing: true } : r));
    } else if (recheckAsked.sawPreparing || landed || Date.now() - recheckAsked.at > 3 * 60 * 1000) {
      setRecheckAsked(null);
    }
    // Re-evaluated on every read of the remediation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [remediation, recheckAsked]);
  const recheckWaiting = Boolean(recheckAsked && !recheckAsked.sawPreparing && !preparing);
  // "Raise a PR" sent from here: {option, planVersion, at (ISO), title}. A pull request row names no plan option, so
  // the newest one opened after this send is taken as that option's attempt (kept for this tab only).
  const [prPick, setPrPick] = useState(null);
  const prRowForPick = prPick
    ? (resolutions || [])
        .filter(
          (r) =>
            r?.type === 'PullRequest' &&
            !(Number(readData(r).option) >= 1) &&
            (timeMs(r.created_at) || 0) >= (timeMs(prPick.at) || Infinity) - 60 * 1000
        )
        .sort((a, b) => (timeMs(b.created_at) || 0) - (timeMs(a.created_at) || 0))[0] || null
    : null;
  const recorded =
    chosenOption(remediation, resolutions, nbStatus) ||
    (prRowForPick && prPick.planVersion === trimmed(remediation?.plan_version) && prOf(prRowForPick)?.state !== 'closed'
      ? { option: prPick.option, row: prRowForPick, done: String(nbStatus || '').toUpperCase() === 'RESOLVED' }
      : null);
  // Approvals answered here leave the card at once, before the read stops listing them.
  const [answeredIds, setAnsweredIds] = useState([]);
  const pendingNow = pendingFor(loop);
  const web = pendingNow.web.filter((p) => !answeredIds.includes(p.message_id));
  const slack = pendingNow.slack;
  // "Go with N" sent from here: {option, planVersion, sending, at, sawBusy}. The pick is pending while the alert's
  // conversation is busy, or until an approval or a run appears; when the conversation goes idle with neither, the
  // assistant's turn ended without running it, and the card says so until the person's next action. A failed send
  // leaves the options as they were, with the error on the option.
  const busy = Boolean(loop.conversation_busy);
  const [picked, setPicked] = useState(null);
  const [pickError, setPickError] = useState({ option: 0, message: '', notRun: false });
  const pickedHere =
    !recorded && picked && !picked.sending && picked.planVersion === trimmed(remediation?.plan_version)
      ? { option: picked.option, preparing: true }
      : null;
  const chosen = recorded || pickedHere;
  // Right after the send the conversation may not read busy yet; a pick that never saw it busy ends after a grace.
  const PICK_GRACE_MS = 60 * 1000;
  useEffect(() => {
    if (!picked || picked.sending || recorded) return;
    if (busy) {
      if (!picked.sawBusy) setPicked((p) => (p ? { ...p, sawBusy: true } : p));
      return;
    }
    if (web.length || slack.length) return;
    if (picked.sawBusy || Date.now() - picked.at > PICK_GRACE_MS) {
      setPickError({ option: picked.option, message: '', notRun: true });
      setPicked(null);
    }
    // Re-evaluated on every read of the remediation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [remediation, busy, web.length, slack.length, recorded, picked]);
  const resolved = String(nbStatus || '').toUpperCase() === 'RESOLVED';
  const stale = Boolean(loop?.relevance?.stale);
  const duplicate = trimmed(loop?.primary_event_id);
  // A duplicate's conversation, plan, runs and approvals are its main event's, so every event-scoped call names that
  // event (a call naming the duplicate is refused as "plan changed").
  const sessionId = alertSessionOf(loop?.primary_fingerprint) || alertSessionOf(fingerprint);
  const targetEventId = duplicate || eventId;
  const openChat = (draft) => onOpenChat({ sessionId, draft, eventId: targetEventId });

  // The request goes straight into the alert's conversation; the drawer is not opened. The page keeps reading the
  // remediation, and the approval the assistant raises appears in the card at the top.
  const sendPick = async (n, text, { pr = false } = {}) => {
    if (picked?.sending) return false;
    setPickError({ option: 0, message: '', notRun: false });
    setPicked({ option: n, planVersion: trimmed(remediation?.plan_version), sending: true, pr });
    try {
      // The text is what the chat shows until the server's own words replace it; the server checks and words the pick.
      const pick = { option: n, kind: pr ? 'pr' : 'run', planVersion: trimmed(remediation?.plan_version) };
      await sendToConversation({ accountId, sessionId, eventId: targetEventId, text, pick });
      setPicked({ option: n, planVersion: trimmed(remediation?.plan_version), sending: false, at: Date.now(), sawBusy: false, pr });
      onReload?.();
      return true;
    } catch (e) {
      setPicked(null);
      setPickError({ option: n, message: friendlySendError(e?.message, assistantLabel), notRun: false });
      return false;
    }
  };
  const goWith = (n, action) => sendPick(n, `Go with option ${n}: ${trimmed(action?.action)}.`);
  // A code fix with a repository: the alert's chat raises the PR (the assistant's own code-change flow).
  const raisePr = async (n, action) => {
    const at = new Date().toISOString();
    if (await sendPick(n, prRequestText({ index: n, res: remediation, action }), { pr: true })) {
      setPrPick({ option: n, planVersion: trimmed(remediation?.plan_version), at, title: optionTitle(action) });
    }
  };
  // A change the person made (or a merged PR that is deployed): recorded as the plan step done by a person.
  const [marking, setMarking] = useState(0);
  const markDone = async (n, action) => {
    if (marking) return;
    setMarking(n);
    setPickError({ option: 0, message: '', notRun: false });
    try {
      const res = await apiKubernetes.executeRemediationCommand(
        accountId,
        trimmed(action?.execute_command),
        targetEventId,
        undefined,
        'manual',
        undefined,
        trimmed(action?.action),
        { planVersion: trimmed(remediation?.plan_version) }
      );
      if (!res) throw new Error('No response from the server.');
      if (eventId) window.dispatchEvent(new CustomEvent('nb:event-resolution-changed'));
      onReload?.();
    } catch (e) {
      setPickError({
        option: n,
        message: (e?.message || '').replace(/^remediation: /, '') || 'Could not record the change as done.',
        notRun: false,
      });
    } finally {
      setMarking(0);
    }
  };

  // The on-demand asks: a plan when there is none, a re-check of an out-of-date one, a replan with the person's
  // reasons. Each is queued and returns at once; the tab shows "preparing" until the plan lands.
  const [lastAsk, setLastAsk] = useState(null);
  const ask = async (mode, extra = {}) => {
    if (askState.sending) return;
    setPickError({ option: 0, message: '', notRun: false });
    setAskState({ sending: mode, message: '' });
    setLastAsk({
      mode,
      at: new Date().toISOString(),
      note: trimmed(extra.note),
      replacedVersion: Number.isInteger(remediation?.version) ? remediation.version : null,
    });
    try {
      await apiKubernetes.askRemediationPlan({ accountId, eventId: targetEventId, mode, ...extra });
      setAskState({ sending: '', message: '' });
      setLastAsk((a) => (a ? { ...a, ok: true, sentAt: Date.now() } : a));
      if (mode === 'replan') setReplanOpen(false);
      onReload?.();
    } catch (e) {
      const reason = (e?.message || 'Could not reach the assistant.').replace(/^remediation: /, '');
      setAskState({ sending: '', message: friendlySendError(reason, assistantLabel) });
      throw e;
    }
  };
  const askQuietly = (mode) => ask(mode).catch(() => {});
  // After a successful ask the read may not say "preparing" yet: the card does, until the read shows it and it ends,
  // or 30 s pass without it.
  useEffect(() => {
    if (!lastAsk?.ok || lastAsk.settled) return;
    if (loop.plan_status === 'preparing') {
      if (!lastAsk.sawPreparing) setLastAsk((a) => (a ? { ...a, sawPreparing: true } : a));
    } else if (lastAsk.sawPreparing || Date.now() - lastAsk.sentAt > 30000) {
      setLastAsk((a) => (a ? { ...a, settled: true } : a));
    }
    // Re-evaluated on every read of the remediation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [remediation, lastAsk]);
  // Preparing ends as soon as the read shows the new plan (a version past the one being replaced, or a plan written
  // after the newest request), even before the read stops saying "preparing".
  const newerPlanLanded =
    (Number.isInteger(lastAsk?.replacedVersion) && Number.isInteger(remediation?.version) && remediation.version > lastAsk.replacedVersion) ||
    Boolean(loop.last_request_at && remediation?.generated_at && timeMs(remediation.generated_at) >= timeMs(loop.last_request_at));
  const preparingView = (preparing || Boolean(lastAsk?.ok && !lastAsk.settled)) && !newerPlanLanded;
  const askingLabel = `Asking ${assistantLabel}…`;
  const askButton = (mode, label, testId, tone = 'primary') => (
    <Button tone={tone} size='xs' onClick={() => askQuietly(mode)} disabled={!canAct || !!askState.sending} data-testid={testId}>
      {askState.sending === mode ? askingLabel : label}
    </Button>
  );

  // The run the alert is on now: the newest attempt since the shown plan was written.
  const runs = (resolutions || []).filter(
    (r) => (r?.type === 'CommandExecution' || r?.type === 'PullRequest' || r?.type === 'WorkflowExecution') && r.status !== 'Declined'
  );

  // Approve clicked here: {messageId, at, option, knownRunIds, sawBusy}. From the click the card says the change is
  // running; it stays so until a run row that was not there at the click appears, and the tab reads again meanwhile.
  // If the conversation goes idle with no new run, the assistant ran nothing.
  const [approved, setApproved] = useState(null);
  const [approveError, setApproveError] = useState('');
  const newRunSinceApprove = approved ? runs.some((r) => !approved.knownRunIds.includes(String(r.id))) : false;
  const approvedPending = Boolean(approved) && !newRunSinceApprove;
  useEffect(() => {
    if (!approved) return;
    if (newRunSinceApprove) {
      setApproved(null);
      return;
    }
    if (busy) {
      if (!approved.sawBusy) setApproved((a) => (a ? { ...a, sawBusy: true } : a));
      return;
    }
    if (approved.sawBusy || Date.now() - approved.at > PICK_GRACE_MS) {
      setPickError({ option: approved.option || 0, message: '', notRun: true });
      setPicked(null);
      setApproved(null);
    }
    // Re-evaluated on every read of the remediation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [remediation, resolutions, busy, approved, newRunSinceApprove]);
  const answer = async (approval, approve, note = '') => {
    setApproveError('');
    if (approve) {
      setApproved({
        messageId: approval.message_id,
        at: Date.now(),
        option: pickedHere?.option || picked?.option || 0,
        knownRunIds: runs.map((r) => String(r.id)),
        sawBusy: false,
      });
    }
    try {
      await answerApproval({ accountId, approval, approve, note });
      if (!approve) {
        // Denied: nothing runs, the approval is answered, and the options can be picked again.
        setAnsweredIds((ids) => [...ids, approval.message_id]);
        setPicked(null);
        setPickError({ option: 0, message: '', notRun: false });
        // With the investigation newer than the fixes, the server asks the assistant to re-check them on this denial
        // (taking over a re-check parked behind it); the tab waits for it.
        if (refreshedAfterPlan) setRecheckAsked({ at: Date.now(), sawPreparing: false });
      }
      onReload?.();
    } catch (e) {
      setApproved(null);
      setApproveError(e?.message || 'Could not send the answer.');
      throw e;
    }
  };
  const webOthers = approved ? web.filter((p) => p.message_id !== approved.messageId) : web;
  // A denial answered elsewhere (chat, or another tab) after this tab's pick ends the pick too.
  const latestDeniedAt = Math.max(0, ...(Array.isArray(loop.denied_approvals) ? loop.denied_approvals : []).map((d) => timeMs(d?.denied_at) || 0));
  useEffect(() => {
    if (picked && !picked.sending && picked.at && latestDeniedAt > picked.at) {
      setPicked(null);
      setPickError({ option: 0, message: '', notRun: false });
    }
  }, [latestDeniedAt, picked]);
  // The attempt the tab reports on is the newest plan step: a change approved in the chat outside the plan is listed
  // in Activity, never taken for the attempt.
  const newestRun = runs.filter(isPlanStepRun).sort((a, b) => (timeMs(b.created_at) || 0) - (timeMs(a.created_at) || 0))[0] || null;
  const currentRun =
    newestRun && (!remediation?.generated_at || (timeMs(newestRun.created_at) || 0) >= (timeMs(remediation.generated_at) || 0)) ? newestRun : null;
  const phase = runPhase(currentRun);
  const pickedOptionNumber = pickedHere?.option || picked?.option || pickError.option || 0;
  const titleOfOption = (n) => (n >= 1 && n <= options.length ? optionTitle(options[n - 1]) : '');
  const currentTitle =
    (currentRun &&
      (runOptionTitle(remediation, currentRun) ||
        (recorded?.row === currentRun ? titleOfOption(recorded.option) : '') ||
        attemptsOf(remediation, [currentRun], assistantLabel)[0]?.title)) ||
    '';
  const account = trimmed(remediation?.target?.account_name) || trimmed(remediation?.target?.account_id);
  const approvalWhat = titleOfOption(pickedOptionNumber) || currentTitle || (options.length === 1 ? optionTitle(options[0]) : '');
  const approvalHeadline = `Approve: ${assistantLabel} wants to ${approvalWhat ? phrase(approvalWhat) : 'run this'}${
    account ? ` on ${account}` : ''
  }`;
  // While a pick waits for its approval or an approved change waits for its run, the page keeps reading: it owns the
  // reads (one at a time), the panel only says it is waiting.
  const waitingForApproval = (Boolean(pickedHere) && !web.length && !slack.length) || approvedPending || recheckWaiting;
  useEffect(() => {
    onWaitingChange?.(waitingForApproval);
  }, [waitingForApproval, onWaitingChange]);
  useEffect(() => () => onWaitingChange?.(false), [onWaitingChange]);
  const cameBack = cameBackAfter({ res: remediation, rows: resolutions, loop });
  // Re-investigate runs the page's own investigation re-run: idle → running (clicked) → busy (the page says it is
  // running) → done (it finished), when Re-check plan becomes the next step.
  const [reinv, setReinv] = useState('idle');
  useEffect(() => {
    if (reinv === 'running' && reinvestigateDisabled) setReinv('busy');
    else if (reinv === 'busy' && !reinvestigateDisabled) setReinv('done');
  }, [reinv, reinvestigateDisabled]);
  const firings = Number(loop?.relevance?.firings_since_plan) || 0;
  const stillFiring = !stale && firings > 0 && !resolved ? `Still firing · ${firings} ${firings === 1 ? 'firing' : 'firings'} since these fixes` : '';

  // The newest planning request ended without a plan while an older plan is still shown.
  const noPlanReason = hasPlan && !preparing ? plainReason(loop.plan_status_reason) : '';
  // The investigation is newer than the plan. A re-check is under way only while one is preparing or parked behind an
  // approval; otherwise (the automatic one ended without a plan) the plan stands, can still be picked, and the person
  // can ask for the re-check.
  const recheckActive = Boolean(refreshedAfterPlan) && (preparing || loop.parked_recheck === true);
  const refreshIdle =
    Boolean(refreshedAfterPlan) &&
    !recheckActive &&
    !recheckWaiting &&
    !preparingView &&
    !web.length &&
    !slack.length &&
    !currentRun &&
    String(nbStatus || '').toUpperCase() !== 'RESOLVED';
  // No plan yet: why (the server's reason, or that nobody has asked), and what fired, in one line each.
  const noPlanText = plainReason(loop.plan_status_reason);
  // Firing since the start of the episode still firing (its first firing, not its newest repeat).
  const lastEpisode = alertEpisodes(loop).slice(-1)[0];
  const firingSince =
    (lastEpisode && !lastEpisode.ended ? lastEpisode.firings[0].starts_at : '') || (loop?.relevance?.alert_state === 'firing' ? alertStartedAt : '');
  const noPlanFired = trimmed(alertTitle) ? `${trimmed(alertTitle)}${clockOnly(firingSince) ? ` · firing since ${clockOnly(firingSince)}` : ''}` : '';
  const ASK_NAMES = {
    auto: 'Preparing the fix automatically',
    ask: 'Prepare the fix',
    plan: 'Prepare the fix',
    recheck: 'Re-check',
    replan: 'Other fixes',
  };
  const reqMode = trimmed(loop.last_request_mode) || lastAsk?.mode || '';
  const reqAt = loop.last_request_at || lastAsk?.at || '';
  const askName = reqMode || reqAt ? `${ASK_NAMES[reqMode] || 'The request'}${reqAt ? ` at ${formatClockCompact(reqAt)}` : ''}` : 'The last request';

  // ── A. Where it stands: one headline, one detail line, at most one primary action. ──
  let where;
  if (resolved) {
    const story = resolvedStory({ res: remediation, rows: resolutions, labels, loop, alertStartedAt, assistant: assistantLabel });
    where = {
      key: 'resolved',
      tone: 'success',
      headline: story.headline,
      lines: [story.what, story.how, story.when].filter(Boolean),
      body: hardening.length ? <OptionalHardening title='Prevent it next time' items={hardening} /> : null,
      action: (
        <Button tone='secondary' size='xs' onClick={() => openChat('')} data-testid='remediation-loop-see-conversation'>
          See conversation
        </Button>
      ),
    };
  } else if (approvedPending && !webOthers.length) {
    where = {
      key: 'fixing',
      headline: `Fixing: ${approvalWhat || 'the approved change'}`,
      spinner: true,
      detail: (
        <Box component='span' data-testid='remediation-loop-running-change'>
          Running the change
        </Box>
      ),
    };
  } else if (webOthers.length) {
    where = {
      key: 'approval',
      tone: 'info',
      headline: approvalHeadline,
      body: (
        <>
          {webOthers.map((p) => (
            <ApprovalCard
              key={p.message_id}
              approval={p}
              canAct={canAct}
              onAnswer={answer}
              refreshedAt={refreshedAfterPlan || undefined}
              assistantLabel={assistantLabel}
            />
          ))}
          {approveError ? <Box sx={{ ...textSx, color: 'var(--ds-red-600)' }}>{approveError}</Box> : null}
        </>
      ),
    };
  } else if (slack.length) {
    where = {
      key: 'approval-slack',
      tone: 'info',
      headline: `Approve in Slack: ${assistantLabel} wants to ${approvalWhat ? phrase(approvalWhat) : 'run this'}${account ? ` on ${account}` : ''}`,
      lines: ['This was asked from the alert’s Slack thread, so it is answered there; the result is posted back to that thread.'],
      body: slack.map((p) => (
        <Box
          key={p.message_id}
          sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}
          data-testid='remediation-loop-waiting-slack'
        >
          <CodeBlock code={trimmed(p.command) || '(no command shown)'} language='bash' tone='dark' wrap copyToast='Command copied' />
          {/^https?:\/\//.test(trimmed(p.thread_url)) ? (
            <Box sx={textSx} data-testid='remediation-loop-slack-thread'>
              <Link href={p.thread_url} openInNew secondaryText>
                Open Slack thread
              </Link>
            </Box>
          ) : null}
        </Box>
      )),
    };
  } else if (recheckWaiting) {
    where = {
      key: 'recheck-pending',
      headline: `${assistantLabel} will re-check the fix`,
      spinner: true,
      detail: (
        <Box component='span' data-testid='remediation-loop-recheck-pending'>
          {`Against the investigation refreshed at ${clockOnly(refreshedAfterPlan) || 'its last run'}. It starts within about a minute.`}
        </Box>
      ),
    };
  } else if (preparingView) {
    const replanning = (lastAsk?.mode || trimmed(loop.last_request_mode)) === 'replan';
    const rechecking =
      hasPlan &&
      (lastAsk?.mode === 'recheck' || Boolean(recheckAsked) || (Boolean(refreshedAfterPlan) && trimmed(loop.last_request_mode) === 'auto'));
    const since = loop.preparing_since || lastAsk?.at || '';
    where = {
      key: 'preparing',
      headline: replanning
        ? `${assistantLabel} is preparing other fixes from your note`
        : rechecking
        ? `${assistantLabel} is re-checking the fix`
        : hasPlan
        ? `${assistantLabel} is preparing new fix options`
        : 'Preparing the fix from the investigation',
      spinner: true,
      lines:
        replanning && lastAsk?.note
          ? [
              <Box component='span' key='note' data-testid='remediation-loop-replan-note-quote'>{`“${truncateText(
                lastAsk.note.replace(/\s+/g, ' '),
                120
              )}”`}</Box>,
            ]
          : [],
      detail: (
        <>
          {since ? `started ${formatClockCompact(since)} · ` : ''}
          <Button tone='link' size='xs' onClick={() => openChat('')} data-testid='remediation-loop-watch-chat'>
            Watch in chat
          </Button>
        </>
      ),
    };
  } else if (pickError.notRun) {
    where = {
      key: 'not-run',
      tone: 'warning',
      headline: (
        <>
          {`${assistantLabel} didn’t run it — `}
          <Button tone='link' size='xs' onClick={() => openChat('')} data-testid='remediation-loop-not-run-chat'>
            see the reply in chat
          </Button>
        </>
      ),
      action:
        pickError.option >= 1 && pickError.option <= options.length ? (
          <Button
            tone='primary'
            size='xs'
            onClick={() => goWith(pickError.option, options[pickError.option - 1])}
            disabled={!canAct}
            data-testid='remediation-loop-not-run-retry'
          >
            Try again
          </Button>
        ) : null,
    };
  } else if (cameBack && !resolved) {
    const reinvRunning = reinv === 'running' || reinv === 'busy';
    const recheck = (tone) =>
      askButton(
        'recheck',
        'Re-check the fix',
        tone === 'primary' ? 'remediation-loop-came-back-recheck' : 'remediation-loop-came-back-recheck-secondary',
        tone
      );
    where = {
      key: 'came-back',
      tone: 'warning',
      headline: `The alert came back after attempt ${cameBack.k}`,
      detail: [
        `Fired again at ${clockOnly(cameBack.firedAt)}${Number.isFinite(cameBack.minutes) ? `, ${cameBack.minutes} min after the fix` : ''}`,
        cameBack.cause,
      ]
        .filter(Boolean)
        .join(' · '),
      action:
        onReinvestigate && canAct && reinv !== 'done' ? (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            <Button
              tone='primary'
              size='xs'
              onClick={() => {
                setReinv('running');
                onReinvestigate();
              }}
              disabled={reinvRunning || Boolean(reinvestigateDisabled && reinv === 'idle')}
              data-testid='remediation-loop-came-back-reinvestigate'
            >
              {reinvRunning ? 'Re-investigating…' : 'Re-investigate'}
            </Button>
            {recheck('secondary')}
          </Box>
        ) : (
          recheck('primary')
        ),
    };
  } else if (pickedHere) {
    where = {
      key: 'fixing',
      headline: `${picked?.pr ? 'Raising a PR' : 'Fixing'}: ${titleOfOption(pickedHere.option)}`,
      spinner: true,
      detail: (
        <Box component='span' data-testid='remediation-loop-preparing-change'>{`${assistantLabel} is preparing the ${
          picked?.pr ? 'pull request' : 'change'
        }…`}</Box>
      ),
    };
  } else if (currentRun && phase.key.startsWith('pr_')) {
    // A pull request has no automatic verdict: open → merged (the person marks it fixed once deployed) or closed.
    const pr = prOf(currentRun);
    const prOption = recorded?.row === currentRun ? recorded.option : 0;
    const prLink = pr?.url ? (
      <Link href={pr.url} openInNew secondaryText>
        {`${pr.number ? `#${pr.number}` : 'View PR'} ↗`}
      </Link>
    ) : null;
    where =
      phase.key === 'pr_merged'
        ? {
            key: 'pr-merged',
            tone: 'success',
            headline: 'PR merged — mark fixed after it’s deployed',
            detail: (
              <>
                {currentTitle ? `${currentTitle} · ` : ''}
                {pr?.endedAt && clockOnly(pr.endedAt) ? `merged ${clockOnly(pr.endedAt)} · ` : ''}
                {prLink}
              </>
            ),
            action:
              prOption >= 1 && prOption <= options.length ? (
                <Button
                  tone='primary'
                  size='xs'
                  onClick={() => markDone(prOption, options[prOption - 1])}
                  loading={marking === prOption}
                  disabled={!canAct || Boolean(marking)}
                  data-testid='remediation-loop-pr-mark-fixed'
                >
                  Mark fixed
                </Button>
              ) : null,
          }
        : phase.key === 'pr_closed'
        ? {
            key: 'pr-closed',
            tone: 'warning',
            headline: 'The PR was closed without merging',
            detail: (
              <>
                {currentTitle ? `${currentTitle} · ` : ''}
                {prLink}
              </>
            ),
            action: askButton('recheck', 'Re-check the fix', 'remediation-loop-pr-closed-recheck', 'secondary'),
          }
        : phase.key === 'pr_stale' || phase.key === 'pr_unknown'
        ? {
            key: phase.key === 'pr_stale' ? 'pr-stale' : 'pr-unknown',
            tone: 'warning',
            headline: phase.key === 'pr_stale' ? 'PR went stale (no activity 3 days)' : 'PR state unknown',
            detail: (
              <>
                {currentTitle ? `${currentTitle} · ` : ''}
                {prLink}
                {prLink ? ' · ' : ''}
                {phase.key === 'pr_stale' ? 'Review or close it' : 'Its state could not be read; check it on the PR'}
              </>
            ),
          }
        : {
            key: 'pr-open',
            headline: `PR open: ${currentTitle || 'code fix'}`,
            detail: (
              <>
                {prLink}
                {prLink ? ' · ' : ''}
                Merge it, then mark it fixed once it is deployed
              </>
            ),
          };
  } else if (currentRun && ['running', 'checking', 'verifying'].includes(phase.key)) {
    where = {
      key: 'fixing',
      headline: `Fixing: ${currentTitle}`,
      spinner: true,
      detail:
        phase.key === 'running'
          ? 'Running the change'
          : phase.key === 'checking'
          ? 'Checking the change'
          : phase.until
          ? `Verifying it holds until ${formatClockCompact(phase.until)}`
          : 'Verifying it holds',
    };
  } else if (currentRun && ['fixed', 'cleared_before_change'].includes(phase.key)) {
    // The verdict is a suggestion: a person confirms it and resolves the event.
    const clearedBefore = phase.key === 'cleared_before_change';
    where = {
      key: 'likely-fixed',
      tone: 'success',
      headline: clearedBefore ? 'Cleared before the change' : `Likely fixed${currentTitle ? `: ${currentTitle}` : ''}`,
      lines: [
        phase.reason ? `${phase.reason.charAt(0).toUpperCase()}${phase.reason.slice(1)}.` : '',
        `Based on the alert’s own firings: ${assistantLabel} did not check that the alert’s monitoring is healthy.`,
        canAct ? '' : 'A person with write access resolves the event.',
      ].filter(Boolean),
      detail: resolveState.error ? (
        <Box component='span' sx={{ color: 'var(--ds-red-600)' }} data-testid='remediation-loop-resolve-error'>
          {resolveState.error}
        </Box>
      ) : (
        ''
      ),
      action: canAct ? (
        <Button
          tone='primary'
          size='xs'
          onClick={() => confirmResolve(currentRun)}
          disabled={resolveState.sending}
          loading={resolveState.sending}
          data-testid='remediation-loop-resolve'
        >
          Resolve
        </Button>
      ) : null,
    };
  } else if (currentRun && (phase.key === 'not_fixed' || phase.key === 'unclear')) {
    where = {
      key: phase.key,
      tone: phase.key === 'not_fixed' ? 'critical' : 'warning',
      headline:
        phase.key === 'not_fixed'
          ? `Not fixed — ${phase.reason || 'the alert is still firing after the change'}`
          : `Couldn’t confirm the fix — ${phase.reason || 'it is not known whether the change worked'}`,
      detail: currentTitle ? `Attempt: ${currentTitle}` : '',
      action: askButton('recheck', 'Re-check the fix', 'remediation-loop-recheck-plan'),
    };
  } else if (currentRun && phase.key === 'checked') {
    where = {
      key: 'checked',
      headline: `${assistantLabel} checked the change${phase.reason ? ` — ${phase.reason}` : ''}`,
      detail: currentTitle ? `Attempt: ${currentTitle}` : '',
      action: askButton('recheck', 'Re-check the fix', 'remediation-loop-recheck-plan', 'secondary'),
    };
  } else if (currentRun && phase.key === 'needs_human') {
    where = {
      key: 'needs_human',
      tone: 'warning',
      headline: `${assistantLabel} needs a person${phase.reason ? ` — ${phase.reason}` : ''}`,
      detail: currentTitle ? `Attempt: ${currentTitle}` : '',
      action: (
        <Button tone='primary' size='xs' onClick={() => openChat('')} data-testid='remediation-loop-needs-human-chat'>
          Open the conversation
        </Button>
      ),
    };
  } else if (!hasPlan && investigationRunning) {
    // The fix is made from the investigation's next steps, automatically: while the investigation runs there is
    // nothing to ask for.
    where = {
      key: 'investigating',
      headline: `${assistantLabel} turns the investigation’s next steps into a fix it can run`,
      spinner: true,
      lines: [noPlanFired].filter(Boolean),
      detail: trimmed(investigation?.stage) ? `Investigation ${phrase(investigation.stage)}` : 'The investigation is running',
    };
  } else if (!hasPlan) {
    const requested = Boolean(loop.last_request_at) || Boolean(trimmed(loop.last_request_mode));
    where = {
      key: 'no-plan',
      headline: requested ? `No fix was prepared${noPlanText ? `: ${noPlanText}` : ''}` : 'No fix was prepared from the investigation yet',
      lines: [noPlanFired].filter(Boolean),
      action: requested
        ? askButton('plan', 'Try again', 'remediation-loop-ask-plan', 'secondary')
        : askButton('plan', 'Prepare the fix', 'remediation-loop-ask-plan', 'secondary'),
    };
  } else if (stale) {
    where = {
      key: 'stale',
      tone: 'warning',
      headline: 'These fixes are out of date',
      detail: plainReason(loop.relevance?.stale_reason) || 'Things changed after they were prepared.',
      action: askButton('recheck', 'Re-check', 'remediation-loop-recheck'),
    };
  } else if (!options.length) {
    where = {
      key: 'nothing',
      headline: `${assistantLabel} found nothing to change`,
      // The plan's root cause shows below; a summary that says the same is not repeated here.
      detail: [sameSentence(remediation?.summary, remediation?.root_cause) ? '' : dedupeSentences(remediation?.summary), stillFiring]
        .filter(Boolean)
        .join(' · '),
      action: askButton('recheck', 'Re-check', 'remediation-loop-nothing-recheck', 'secondary'),
    };
  } else {
    where = {
      key: 'ready',
      headline: changeOnly(options)
        ? options.some((a) => requiredChangeOf(a)?.where === 'code')
          ? 'Cause is in code — fix needs a change'
          : 'The fix is a change made outside the cluster'
        : fixOptionsName(remediation, loop),
      detail: [`${options.length} ${options.length === 1 ? 'option' : 'options'} below`, stillFiring].filter(Boolean).join(' · '),
      ...(refreshIdle
        ? {
            lines: [
              <Box component='span' key='refreshed' data-testid='remediation-loop-refreshed-after-plan'>
                {`The investigation was refreshed after these fixes${
                  noPlanReason ? ` · the automatic re-check ended without new fixes: ${noPlanReason.split('\n')[0]}` : ''
                }`}
              </Box>,
            ],
            action: askButton('recheck', 'Re-check the fix', 'remediation-loop-refreshed-recheck', 'secondary'),
          }
        : {}),
    };
  }

  const menuItems = (moreActions || []).filter(Boolean);
  const timeline = timelineOf({
    res: remediation,
    rows: resolutions,
    labels,
    loop,
    nbStatus,
    assistant: assistantLabel,
    firstFiredAt: alertStartedAt || '',
    pending: {
      prPick,
      pick: pickedHere
        ? { option: pickedHere.option, title: titleOfOption(pickedHere.option), at: picked?.at ? new Date(picked.at).toISOString() : '' }
        : null,
      approvals: approvedPending ? [] : [...web, ...slack],
      approvedAt: approvedPending ? new Date(approved.at).toISOString() : '',
    },
  });
  const hasAttempts = timeline.some((it) => it.type === 'attempt');
  // The timeline shows once anything happened: an attempt, or the alert's own firings (even before any plan).
  const showTimeline = hasAttempts || (Array.isArray(loop.firings) && loop.firings.length > 0 && timeline.length > 0);

  // ── The plan, inside the same card: open while picking; one line once an option is chosen, the event is resolved,
  // or the state above needs the whole card (an approval). ──
  const picking = hasPlan && options.length > 0 && !chosen && !resolved && !preparingView && !stale && !cameBack && !web.length && !slack.length;
  // "Being re-checked" only while a re-check is queued, running or parked.
  const planTag =
    preparingView && (recheckAsked || recheckActive || lastAsk?.mode === 'recheck')
      ? 'being re-checked'
      : preparingView
      ? 'being replaced'
      : recheckWaiting || recheckActive
      ? 'being re-checked'
      : cameBack && !resolved
      ? 'written before the alert came back'
      : '';
  const goDisabledReason = busy ? `${assistantLabel} is finishing its last reply` : !canAct ? 'Requires write access' : '';
  const wholeCard = ['approval', 'approval-slack', 'resolved'].includes(where.key);
  const collapsed = Boolean(chosen) || resolved || wholeCard;
  const chosenTitle = chosen ? titleOfOption(chosen.option) : '';
  const chosenPhase = recorded ? runPhase(recorded.row) : null;
  const chosenChip = resolved
    ? resultChip('fixed', assistantLabel)
    : pickedHere
    ? { text: 'Preparing', tone: 'info' }
    : chosenPhase
    ? resultChip(chosenPhase.key, assistantLabel) || { text: 'In progress', tone: 'info' }
    : null;
  // Confidence only when it tells the options apart: not when every option carries the same number, and never the
  // 50 the model fills in when it has none.
  const confidences = options.map((a) => (typeof a?.confidence === 'number' ? Math.round(a.confidence) : null));
  const distinctConfidence = new Set(confidences.filter((c) => c !== null)).size > 1;
  const canReplan = !chosen && !resolved && options.length > 0 && !preparingView;
  const planBody = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', opacity: planTag && !collapsed ? 0.55 : 1 }}>
      {replanOpen && canReplan ? (
        <ReplanForm
          assistantLabel={assistantLabel}
          canAct={canAct}
          onCancel={() => setReplanOpen(false)}
          onSubmit={({ reasons, note }) => ask('replan', { reasons, note })}
        />
      ) : null}
      {trimmed(remediation?.root_cause) ? <ClampText label='Root cause:' text={remediation.root_cause} testId='remediation-loop-root-cause' /> : null}
      {trimmed(remediation?.goal) ? <ClampText label='Done when:' text={remediation.goal} testId='remediation-loop-goal' /> : null}
      {chosen ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
          {options.map((a, i) =>
            i + 1 === chosen.option ? (
              <Box key={i} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }} data-testid='remediation-loop-chosen'>
                <Box component='span' sx={headSx}>
                  {optionTitle(a)}
                </Box>
                {chosenChip ? <Label tone={chosenChip.tone} size='sm' text={chosenChip.text} /> : null}
              </Box>
            ) : (
              <Box key={i} sx={hintSx} data-testid='remediation-loop-not-chosen'>{`${optionTitle(a)} · not chosen`}</Box>
            )
          )}
          {!resolved ? (
            <Box>
              <Button tone='ghost' size='xs' onClick={() => openChat('')}>
                {`Change course? Tell ${assistantLabel} in chat`}
              </Button>
            </Box>
          ) : null}
        </Box>
      ) : options.length ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          {picking && goDisabledReason ? (
            <Box sx={{ ...textSx, color: 'var(--ds-amber-700)' }} data-testid='remediation-loop-go-disabled-reason'>
              {goDisabledReason}
            </Box>
          ) : null}
          {options.map((a, i) => (
            <Box key={i} sx={{ opacity: stale ? 0.55 : 1 }}>
              <OptionRow
                index={i + 1}
                action={a}
                showAsk={picking}
                showConfidence={distinctConfidence && confidences[i] !== 50}
                disabled={Boolean(goDisabledReason) || Boolean(picked?.sending)}
                sending={picked?.sending === true && picked.option === i + 1}
                error={!pickError.notRun && pickError.option === i + 1 ? pickError.message : ''}
                assistantLabel={assistantLabel}
                onAsk={goWith}
                askLabel={refreshIdle ? 'Use this fix anyway' : undefined}
                onRaisePr={raisePr}
                onMarkDone={canAct ? markDone : undefined}
                marking={marking === i + 1}
              />
            </Box>
          ))}
        </Box>
      ) : null}
      {hardening.length ? <OptionalHardening items={hardening} /> : null}
    </Box>
  );
  const planSection =
    hasPlan && (trimmed(remediation?.root_cause) || options.length || hardening.length) ? (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--ds-space-3)',
          borderTop: '1px solid var(--ds-gray-200)',
          pt: 'var(--ds-space-3)',
        }}
        data-testid='remediation-loop-options'
      >
        {collapsed ? (
          <Box>
            <Button tone='ghost' size='xs' onClick={() => setPlanOpen((v) => !v)} aria-expanded={planOpen} data-testid='remediation-loop-plan-toggle'>
              {`${fixOptionsName(remediation, loop)}${chosenTitle ? ` · ${chosenTitle}` : ''}${
                hardening.length ? ` · ${hardening.length} hardening ${hardening.length === 1 ? 'idea' : 'ideas'}` : ''
              }${planTag ? ` · ${planTag}` : ''} ${planOpen ? '▾' : '▸'}`}
            </Button>
          </Box>
        ) : (
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <Box component='span' sx={headSx}>
                Fix options
              </Box>
              {planDetails(remediation) ? (
                <Button
                  tone='link'
                  size='xs'
                  onClick={() => setPlanDetailsOpen((v) => !v)}
                  aria-expanded={planDetailsOpen}
                  data-testid='remediation-loop-plan-details-toggle'
                >
                  Details
                </Button>
              ) : null}
              {planTag ? (
                <Box component='span' data-testid='remediation-loop-plan-replaced'>
                  <Label tone='neutral' size='sm' text={planTag} textTransform='none' />
                </Box>
              ) : null}
            </Box>
            {canReplan && !replanOpen ? (
              <Button tone='link' size='xs' onClick={() => setReplanOpen(true)} disabled={!canAct} data-testid='remediation-loop-replan-open'>
                {`None of these · ask ${assistantLabel} for other fixes`}
              </Button>
            ) : null}
          </Box>
        )}
        {planDetailsOpen && planDetails(remediation) ? (
          <Box sx={hintSx} data-testid='remediation-loop-plan-details'>
            {planDetails(remediation)}
          </Box>
        ) : null}
        {!collapsed || planOpen ? planBody : null}
      </Box>
    ) : null;

  // ── One card: where it stands (headline, detail, the state's one action), then the plan. ──
  const whereEl = (
    <Card
      variant='tinted'
      tone={where.tone === 'critical' ? 'danger' : where.tone || 'neutral'}
      size='sm'
      elevation='flat'
      data-testid='remediation-loop-where'
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }} data-state={where.key}>
        <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-2)',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              minWidth: 0,
            }}
            data-testid='remediation-loop-status'
          >
            {where.spinner ? <CircularProgress size={14} thickness={5} /> : null}
            <Box component='span'>{where.headline}</Box>
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', flexShrink: 0 }}>
            {!resolved ? (
              <Button tone='ghost' size='xs' onClick={() => openChat('')} data-testid='remediation-loop-continue-chat'>
                Continue in chat →
              </Button>
            ) : null}
            {menuItems.length ? (
              <DropdownMenu
                align='end'
                size='sm'
                minWidth={220}
                items={menuItems}
                trigger={
                  <Button
                    tone='secondary'
                    size='xs'
                    composition='icon-only'
                    icon={<MoreVert />}
                    aria-label='More actions'
                    data-testid='remediation-loop-more-actions'
                  />
                }
              />
            ) : null}
            {headerExtras}
          </Box>
        </Box>
        {where.detail ? (
          <Box sx={hintSx} data-testid='remediation-loop-where-detail'>
            {where.detail}
          </Box>
        ) : null}
        {(where.lines || []).map((line, i) => (
          <Box key={i} sx={textSx} data-testid='remediation-loop-where-line'>
            {line}
          </Box>
        ))}
        {where.body}
        {/* An older request's failure is told only while the person picks (options shown, nothing under way): it
            competes with an approval, a fix being made or checked, and a result. */}
        {noPlanReason && !refreshIdle && where.key === 'ready' ? (
          <Box
            sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', ...hintSx }}
            data-testid='remediation-loop-request-no-plan'
          >
            <FirstSentence text={`${askName} didn’t produce a fix — ${noPlanReason}`} testId='remediation-loop-request-no-plan-text' />
            {!resolved ? (
              <Button
                tone='link'
                size='xs'
                onClick={() => askQuietly(reqMode === 'plan' || reqMode === 'ask' ? 'plan' : 'recheck')}
                disabled={!canAct || !!askState.sending}
                data-testid='remediation-loop-request-no-plan-retry'
              >
                {askState.sending ? askingLabel : 'Try again'}
              </Button>
            ) : null}
          </Box>
        ) : null}
        {askState.message ? <Box sx={{ ...textSx, color: 'var(--ds-red-600)' }}>{askState.message}</Box> : null}
        {where.action ? <Box>{where.action}</Box> : null}
        {planSection}
      </Box>
    </Card>
  );

  return (
    // Capped so lines do not run edge to edge on a wide screen.
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', maxWidth: 1280 }} data-testid='remediation-loop'>
      {duplicate ? (
        <Box
          sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', ...hintSx }}
          data-testid='remediation-loop-duplicate'
        >
          <Box component='span'>{`Duplicate of ${duplicate.slice(0, 8)} — this alert’s remediation runs there`}</Box>
          <Button tone='link' size='xs' onClick={() => onOpenEvent(duplicate)} data-testid='remediation-loop-open-main'>
            Open main event
          </Button>
          {onReinvestigate && canAct && !reinvestigateDisabled ? (
            <Button tone='link' size='xs' onClick={onReinvestigate} data-testid='remediation-loop-reinvestigate'>
              Re-investigate this firing
            </Button>
          ) : null}
        </Box>
      ) : null}
      {showTimeline ? (
        <Box
          sx={{ display: 'flex', flexDirection: { xs: 'column', md: 'row-reverse' }, alignItems: { md: 'flex-start' }, gap: 'var(--ds-space-5)' }}
          data-testid='remediation-loop-columns'
        >
          <Box
            sx={{
              flex: { md: '0 0 60%' },
              minWidth: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--ds-space-4)',
              position: { md: 'sticky' },
              top: { md: 'var(--ds-space-4)' },
            }}
            data-testid='remediation-loop-actions-column'
          >
            {whereEl}
          </Box>
          <Box sx={{ flex: { md: '1 1 40%' }, minWidth: 0 }} data-testid='remediation-loop-timeline-column'>
            <TimelineList items={timeline} onOpenChat={() => openChat('')} onOpenEvent={onOpenEvent} />
          </Box>
        </Box>
      ) : (
        // Nothing has happened yet: one column, no empty timeline beside it.
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }} data-testid='remediation-loop-actions-column'>
          {whereEl}
        </Box>
      )}
    </Box>
  );
};

RemediationLoopPanel.propTypes = {
  accountId: PropTypes.string.isRequired,
  eventId: PropTypes.string,
  // The alert's fingerprint: its conversation is event-<fingerprint>.
  fingerprint: PropTypes.string,
  nbStatus: PropTypes.string,
  // The ai_remediation_get response: the plan's fields at the top level, plus `loop`.
  remediation: PropTypes.object.isRequired,
  resolutions: PropTypes.array.isRequired,
  // The event's labels (nb_resolved_by names the run that resolved it) and when the alert started.
  labels: PropTypes.object,
  alertStartedAt: PropTypes.string,
  // The event's title, named on "No plan yet".
  alertTitle: PropTypes.string,
  // The investigation's progress, from the analysis card: {state: 'running' | 'complete' | 'failed', stage: "Step 2 of 4 · …"}.
  // The fix options are made from it once it completes.
  investigation: PropTypes.shape({ state: PropTypes.string, stage: PropTypes.string }),
  assistantName: PropTypes.string,
  // Opens the alert's conversation in the chat drawer: {sessionId, draft (a message ready to send), eventId (the event
  // the chat is about: the main event on a duplicate)}.
  onOpenChat: PropTypes.func.isRequired,
  // Opens another event of this alert (the main event of a duplicate).
  onOpenEvent: PropTypes.func.isRequired,
  // Reads the remediation and the run records again (the page coalesces it with a read in flight).
  onReload: PropTypes.func,
  // Called once a person resolved the event from a likely-fixed attempt, so the page reads the event again.
  onResolved: PropTypes.func,
  // Says whether the panel is waiting on something it sent (a pick, an approval), so the page keeps reading.
  onWaitingChange: PropTypes.func,
  // Re-runs this event's own investigation (the page's refresh-investigation action), offered on a duplicate.
  onReinvestigate: PropTypes.func,
  reinvestigateDisabled: PropTypes.bool,
  // The ⋮ "More actions" items ({id, label, onSelect, disabled}): actions on the alert that are not the plan's.
  moreActions: PropTypes.array,
  // Rendered beside the menu: the hidden anchors of the menus and dialogs those items open.
  headerExtras: PropTypes.node,
};

export default RemediationLoopPanel;
