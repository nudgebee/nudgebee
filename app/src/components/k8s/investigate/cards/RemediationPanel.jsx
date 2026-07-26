import React, { useState, useEffect, useRef, forwardRef, useImperativeHandle } from 'react';
import PropTypes from 'prop-types';
import { Box, IconButton, CircularProgress, Tooltip } from '@mui/material';
import PlayArrowRoundedIcon from '@mui/icons-material/PlayArrowRounded';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Label } from '@ui/Label';
import { CodeBlock } from '@ui/CodeBlock';
import { ds } from '@utils/colors';
import { hasWriteAccess, hasFeatureAccess } from '@lib/auth';
import apiKubernetes from '@api1/kubernetes';
import apiRecommendations from '@api1/recommendation';
import apiTriage from '@api1/triage';

// Feature flag that opts an account into auto-generating the remediation plan once the investigation
// completes (off by default). Checked via hasFeatureAccess, the same mechanism that gates GENERATE_RCA.
const AUTO_GENERATE_REMEDIATION_FLAG = 'AUTO_GENERATE_REMEDIATION';

const STATUS_META = {
  SUCCESS: { text: 'Success', tone: 'success' },
  FAILED: { text: 'Failed', tone: 'critical' },
  RUNNING: { text: 'Running', tone: 'info' },
  // A verification asserts something about observed state. A command that exits 0 having observed
  // nothing has not verified anything — a selector that matches no object, a query that returns no
  // rows, a log tail with no lines all exit 0. Reporting that as Success tells the operator the fix
  // held when nothing was actually checked, so it gets its own state rather than a green tick.
  INCONCLUSIVE: { text: 'Inconclusive — no output', tone: 'warning' },
};

// A fix removes the root cause; a mitigation only restores service while the cause survives, so the
// problem recurs. The server classifies every action, so an unrecognized value means an older saved
// plan from before the field existed — show no chip rather than guess.
const KIND_META = {
  fix: { text: 'Fix', tone: 'success' },
  mitigation: { text: 'Mitigation', tone: 'warning' },
};

// Confidence is normalized to a 0-100 integer server-side, so just clamp defensively here. Returns
// null when the value is missing/non-numeric so the chip is simply omitted.
function formatConfidence(value) {
  if (typeof value !== 'number' || Number.isNaN(value)) return null;
  return Math.max(0, Math.min(100, Math.round(value)));
}

function trimmedString(value) {
  return typeof value === 'string' ? value.trim() : '';
}

// Cap on the diff we forward as context. A code fix is normally a handful of lines; anything much
// larger is a refactor the panel cannot meaningfully summarise into one action, and it would crowd
// out the investigation text in the prompt.
const MAX_DIFF_CHARS = 4000;

// extractCodeFix pulls the already-computed code fix off the log-analysis record. Returns null when
// code analysis produced nothing. The diff is the authoritative artifact — it is rendered and sent
// verbatim, never round-tripped through the model, so what the operator sees is the real change.
function extractCodeFix(rec) {
  const files = Array.isArray(rec?.files_modified) ? rec.files_modified.filter((f) => trimmedString(f)) : [];
  const diff = trimmedString(rec?.git_diff);
  const originalCode = trimmedString(rec?.original_code);
  const prUrl = trimmedString(rec?.automated_fix_pr?.url) || trimmedString(rec?.automated_fix_pr?.html_url);
  if (!files.length && !diff && !prUrl) return null;
  return { files, diff, originalCode, prUrl };
}

// formatCodeFixContext renders the code fix as a labelled block for the prompt. Returns '' when code
// analysis produced nothing, so the caller can filter it out.
function formatCodeFixContext(rec) {
  const fix = extractCodeFix(rec);
  if (!fix) return '';
  const { files, diff, originalCode, prUrl } = fix;

  const lines = ['## Code fix already identified by code analysis'];
  lines.push('This fix was derived from the source and is the durable fix for the root cause. Do not re-derive it.');
  if (files.length) lines.push(`Files: ${files.join(', ')}`);
  if (prUrl) lines.push(`An automated fix PR already exists: ${prUrl}`);
  if (originalCode) lines.push(`Current code:\n${originalCode}`);
  if (diff) {
    lines.push(`Proposed diff:\n${diff.length > MAX_DIFF_CHARS ? `${diff.slice(0, MAX_DIFF_CHARS)}\n[diff truncated]` : diff}`);
  }
  return lines.join('\n');
}

// Recommendation types that actually propose changing the alert. The rest are triage saying it could
// not reach a tuning conclusion (insufficient_data, investigate_signal, review_alert, none,
// not_eligible), and forwarding those as remediation candidates misreads "I don't know" as advice.
// An absent type means an older suggestion from before the field existed, which stays eligible.
const TUNABLE_RECOMMENDATION_TYPES = ['tune_threshold', 'increase_duration', 'tune_both', 'disable'];

// formatThresholdContext renders a triage-computed threshold suggestion. `res` is the
// event_get_threshold_suggestion response: the numbers live under .alert_definition (current) and
// .suggestion (proposed), with `available` telling us whether triage produced anything at all.
//
// Three of triage's own judgements decide eligibility, rather than a proxy invented here:
//   - risk_level "dangerous" — the apply service refuses these without an explicit override
//     (threshold_apply_service.go), so proposing one produces an action that cannot be carried out.
//   - recommendation_type — only the tuning verdicts are advice; the others are non-conclusions.
//   - confidence "low" — triage marks its own inputs unsound (a contaminated baseline, a saturated
//     histogram), and those produce absurd numbers: one live suggestion raises a 4xx-rate threshold
//     from 5 to 97.67, which would silence every error on the ingress.
//
// A "review" risk still goes through, carrying its warnings, because that is a judgement for the
// operator rather than a reason to hide the option.
function formatThresholdContext(res) {
  const suggestion = res?.suggestion;
  if (!res?.available || !suggestion || typeof suggestion.suggested_threshold !== 'number') return '';
  if (trimmedString(suggestion.confidence).toLowerCase() === 'low') return '';
  if (trimmedString(suggestion.risk_level).toLowerCase() === 'dangerous') return '';
  const recommendationType = trimmedString(suggestion.recommendation_type).toLowerCase();
  if (recommendationType && !TUNABLE_RECOMMENDATION_TYPES.includes(recommendationType)) return '';

  const def = res.alert_definition || {};
  const lines = ['## Threshold suggestion already computed by triage'];
  lines.push('This is a candidate only, not an established fix: it addresses alert noise, not a service fault.');
  lines.push('Propose it only if your hypotheses conclude the alert is miscalibrated. It is applied through the threshold panel, not a command.');
  if (trimmedString(def.alarm_name)) lines.push(`Alert: ${def.alarm_name}`);
  if (trimmedString(def.metric_name)) lines.push(`Metric: ${def.metric_name}`);
  lines.push(`Current threshold: ${def.current_threshold ?? 'unknown'} -> suggested: ${suggestion.suggested_threshold}`);
  if (recommendationType) lines.push(`Recommendation: ${recommendationType}`);
  if (trimmedString(suggestion.reason)) lines.push(`Reason: ${suggestion.reason}`);
  if (trimmedString(suggestion.confidence)) lines.push(`Confidence: ${suggestion.confidence}`);
  if (typeof suggestion.estimated_reduction === 'number') {
    lines.push(`Estimated noise reduction: ${suggestion.estimated_reduction}`);
  }
  if (trimmedString(res.alert_quality?.classification)) lines.push(`Alert quality: ${res.alert_quality.classification}`);
  // Carried through so a reviewable suggestion is proposed with its caveats rather than as a clean
  // recommendation the operator has no reason to question.
  if (trimmedString(suggestion.risk_level)) lines.push(`Operational risk: ${suggestion.risk_level}`);
  const warnings = Array.isArray(suggestion.risk_warnings) ? suggestion.risk_warnings.filter((w) => trimmedString(w)) : [];
  if (warnings.length) lines.push(`Risk warnings: ${warnings.join('; ')}`);
  return lines.join('\n');
}

// Human-readable timestamp for when an action was last applied; empty string if unparseable.
function formatAppliedAt(value) {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
}

// Reasoning can arrive as an array of points or (defensively) a newline-separated string; normalize
// to a clean list of short points for point-wise rendering.
function toPoints(value) {
  if (Array.isArray(value)) return value.filter((p) => typeof p === 'string' && p.trim()).map((p) => p.trim());
  if (typeof value === 'string' && value.trim()) {
    return value
      .split('\n')
      .map((p) => p.trim())
      .filter(Boolean);
  }
  return [];
}

// A compact bulleted list of reasoning points.
function ReasoningBullets({ points }) {
  if (!points.length) return null;
  return (
    <Box component='ul' sx={{ m: 0, pl: 'var(--ds-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      {points.map((p, i) => (
        <Box component='li' key={i} sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
          {p}
        </Box>
      ))}
    </Box>
  );
}

ReasoningBullets.propTypes = {
  points: PropTypes.array.isRequired,
};

// One candidate cause with the evidence behind it. Rendered once for the whole plan: several actions
// commonly address the same cause, and repeating the statement and its evidence on each of their
// cards made the operator read the same three bullets three times before reaching the differences.
function HypothesisBlock({ hypothesis }) {
  const text = trimmedString(hypothesis?.hypothesis);
  if (!text) return null;
  const confidence = formatConfidence(hypothesis?.confidence);
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ fontSize: 'var(--ds-text-small)' }}>
        {text}
        {confidence !== null ? <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>{` — ${confidence}% likely cause`}</Box> : null}
      </Box>
      <ReasoningBullets points={toPoints(hypothesis?.reasoning)} />
    </Box>
  );
}

HypothesisBlock.propTypes = {
  hypothesis: PropTypes.object,
};

// A single command with an inline play button and its output. The server re-derives read-vs-write
// and enforces write RBAC + the safety blocklist, so the click here is the human approval. Exposes an
// imperative run() (returning success) so the action block can sequence execute → verify.
const CommandRow = forwardRef(function CommandRow({ label, tone, command, accountId, eventId, canRun, disabled, onExecuted, slot }, ref) {
  const [status, setStatus] = useState('IDLE');
  const [result, setResult] = useState(null);

  // Reset row state when the command changes (e.g. after Regenerate) so a previous run's
  // status/output never lingers on a different command reconciled into the same position.
  useEffect(() => {
    setStatus('IDLE');
    setResult(null);
  }, [command]);

  const run = async () => {
    setStatus('RUNNING');
    setResult(null);
    try {
      const res = await apiKubernetes.executeRemediationCommand(accountId, command, eventId, undefined, slot);
      if (res && typeof res === 'object') {
        setResult(res);
        const observedNothing = slot === 'verify' && !trimmedString(res.stdout);
        setStatus(res.success ? (observedNothing ? 'INCONCLUSIVE' : 'SUCCESS') : 'FAILED');
        if (res.success && typeof onExecuted === 'function') onExecuted();
        return !!res.success;
      }
      setResult({ stderr: 'No response from server.' });
      setStatus('FAILED');
      return false;
    } catch (e) {
      setResult({ stderr: e?.message || 'Execution failed.' });
      setStatus('FAILED');
      return false;
    }
  };

  useImperativeHandle(ref, () => ({ run }));

  const meta = STATUS_META[status];
  const isDisabled = status === 'RUNNING' || !canRun || disabled;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
        <Label tone={tone} text={label} size='sm' />
        {meta ? <Label tone={meta.tone} text={meta.text} size='sm' /> : null}
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)' }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <CodeBlock code={command} language='bash' tone='dark' wrap copyToast='Command copied' />
        </Box>
        <Tooltip title={!canRun ? 'Requires write access' : status === 'RUNNING' ? 'Running…' : `Run ${label.toLowerCase()} command`}>
          <span>
            <IconButton
              size='small'
              onClick={run}
              disabled={isDisabled}
              aria-label={`run-${label.toLowerCase()}-command`}
              data-testid={`remediation-run-${label.toLowerCase()}`}
              sx={{ color: 'var(--ds-green-600)', mt: 'var(--ds-space-1)' }}
            >
              {status === 'RUNNING' ? <CircularProgress size={16} thickness={5} /> : <PlayArrowRoundedIcon fontSize='small' />}
            </IconButton>
          </span>
        </Tooltip>
      </Box>
      {result && (typeof result.exit_code === 'number' || result.duration) ? (
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
          {typeof result.exit_code === 'number' ? <Box component='span'>{`Exit code: ${result.exit_code}`}</Box> : null}
          {result.duration ? <Box component='span'>{`Duration: ${result.duration}`}</Box> : null}
        </Box>
      ) : null}
      {result?.stdout ? (
        <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
          <Box
            sx={{
              mb: 'var(--ds-space-1)',
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-500)',
            }}
          >
            stdout
          </Box>
          <Box
            sx={{
              maxHeight: ds.space.mul(2, 50),
              overflowY: 'auto',
              fontFamily: 'var(--ds-font-mono)',
              fontSize: 'var(--ds-text-small)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}
          >
            {result.stdout}
          </Box>
        </Card>
      ) : null}
      {result?.stderr || result?.error ? (
        <Card variant='tinted' tone='danger' size='sm' elevation='flat'>
          <Box sx={{ mb: 'var(--ds-space-1)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>stderr</Box>
          <Box
            sx={{
              maxHeight: ds.space.mul(2, 50),
              overflowY: 'auto',
              fontFamily: 'var(--ds-font-mono)',
              fontSize: 'var(--ds-text-small)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}
          >
            {result.stderr || result.error}
          </Box>
        </Card>
      ) : null}
    </Box>
  );
});

CommandRow.propTypes = {
  label: PropTypes.string.isRequired,
  tone: PropTypes.string.isRequired,
  command: PropTypes.string.isRequired,
  accountId: PropTypes.string.isRequired,
  eventId: PropTypes.string,
  canRun: PropTypes.bool.isRequired,
  disabled: PropTypes.bool,
  onExecuted: PropTypes.func,
  slot: PropTypes.oneOf(['execute', 'verify', 'rollback']).isRequired,
};

// CodeFixDetails shows the actual change behind a code-fix action: the files touched and the diff
// code analysis produced. Without it the card is a sentence telling the operator a fix exists
// somewhere else, which is not enough to act on or to judge. The diff comes straight from the
// analysis record, not from the remediation model.
function CodeFixDetails({ codeFix }) {
  if (!codeFix) return null;
  const { files, diff, prUrl } = codeFix;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      {files.length ? (
        <Box sx={{ fontSize: 'var(--ds-text-small)' }}>
          <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
            {files.length > 1 ? 'Files: ' : 'File: '}
          </Box>
          {files.join(', ')}
        </Box>
      ) : null}
      {diff ? <CodeBlock code={diff} language='diff' title={files.length === 1 ? files[0] : undefined} maxHeight={320} /> : null}
      {prUrl ? (
        <Box sx={{ fontSize: 'var(--ds-text-small)' }}>
          <a href={prUrl} target='_blank' rel='noreferrer' data-testid='remediation-fix-pr-link'>
            View the automated fix PR
          </a>
        </Box>
      ) : null}
    </Box>
  );
}

CodeFixDetails.propTypes = {
  codeFix: PropTypes.shape({
    files: PropTypes.arrayOf(PropTypes.string),
    diff: PropTypes.string,
    prUrl: PropTypes.string,
  }),
};

// One remediation action: header (label + confidence), the hypothesis it addresses (with reasoning +
// confidence, shown inline — there is no separate hypotheses box), and its command rows. A per-block
// "Run" button runs this action's execute → verify one-by-one, stopping on the first failure.
// A code-fix action carries no command, so it renders the diff instead of command rows.
function ActionCard({ index, action, hypothesis, accountId, eventId, canRun, appliedAt, onExecuted, codeFix }) {
  const [running, setRunning] = useState(false);
  const execRef = useRef(null);
  const verifyRef = useRef(null);
  const appliedLabel = appliedAt ? formatAppliedAt(appliedAt) : '';

  const confidence = formatConfidence(action?.confidence);
  const kindMeta = KIND_META[trimmedString(action?.kind).toLowerCase()] || null;
  const execCmd = trimmedString(action?.execute_command);
  const verifyCmd = trimmedString(action?.verify_command);
  const rollbackCmd = trimmedString(action?.rollback_command);

  const hypText = trimmedString(hypothesis?.hypothesis) || trimmedString(action?.hypothesis);
  const hypConfidence = formatConfidence(hypothesis?.confidence);
  // Only a command-less fix stands in for the code change; a mitigation with a command shows its
  // command rows instead, and must not carry someone else's diff.
  const showCodeFix = !execCmd && trimmedString(action?.kind).toLowerCase() === 'fix' && !!codeFix;

  // Run the action's execute command, then (only if it succeeded) its verify command. Rollback is
  // never auto-run. Each row shows its own output live as it runs.
  const runBlock = async () => {
    if (running || !canRun) return;
    setRunning(true);
    try {
      if (execRef.current) {
        const ok = await execRef.current.run();
        if (!ok) return;
      }
      if (verifyRef.current) {
        await verifyRef.current.run();
      }
    } finally {
      setRunning(false);
    }
  };

  return (
    <Card
      variant='outlined'
      tone='neutral'
      size='sm'
      elevation='flat'
      header={
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-2)', minWidth: 0 }}>
            <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-small)' }}>
              {action?.action || 'Remediation'}
            </Box>
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
            {kindMeta ? <Label tone={kindMeta.tone} size='sm' text={kindMeta.text} /> : null}
            {appliedAt ? <Label tone='success' size='sm' text={appliedLabel ? `Applied · ${appliedLabel}` : 'Applied'} /> : null}
            {confidence !== null ? (
              // Says what the number measures. The hypothesis below carries its own percentage
              // measuring something different (is this the cause), and two bare "confidence" chips
              // stacked read as a contradiction rather than as two distinct judgements.
              <Box
                sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', whiteSpace: 'nowrap' }}
              >{`${confidence}% likely to resolve`}</Box>
            ) : null}
            {execCmd ? (
              <Tooltip title={!canRun ? 'Requires write access' : 'Run this action (execute, then verify), stopping on failure'}>
                <span>
                  <Button
                    tone={appliedAt ? 'secondary' : 'primary'}
                    size='xs'
                    onClick={runBlock}
                    loading={running}
                    disabled={!canRun || running}
                    data-testid={`remediation-run-action-${index + 1}`}
                  >
                    {appliedAt ? 'Re-run' : 'Run'}
                  </Button>
                </span>
              </Tooltip>
            ) : null}
          </Box>
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
        {action?.title ? <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{action.title}</Box> : null}
        {hypText ? (
          // A reference, not a restatement — the cause and its evidence are listed once above, so the
          // card only needs to say which one it addresses.
          <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
            <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
              Addresses:{' '}
            </Box>
            {hypText}
            {hypConfidence !== null ? <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>{` — ${hypConfidence}% likely cause`}</Box> : null}
          </Box>
        ) : null}
        {showCodeFix ? <CodeFixDetails codeFix={codeFix} /> : null}
        {execCmd ? (
          <CommandRow
            ref={execRef}
            label='Execute'
            slot='execute'
            tone='info'
            command={execCmd}
            accountId={accountId}
            eventId={eventId}
            canRun={canRun}
            disabled={running}
            onExecuted={onExecuted}
          />
        ) : null}
        {verifyCmd ? (
          <CommandRow
            ref={verifyRef}
            label='Verify'
            slot='verify'
            tone='neutral'
            command={verifyCmd}
            accountId={accountId}
            eventId={eventId}
            canRun={canRun}
            disabled={running}
            onExecuted={onExecuted}
          />
        ) : null}
        {rollbackCmd ? (
          // Rollback is nested in its own box so it reads as separate from the block: "Run" never
          // executes it — it's a manual undo.
          <Card
            variant='tinted'
            tone='neutral'
            size='sm'
            elevation='flat'
            header={<Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>Rollback — manual undo, not run by “Run”</Box>}
          >
            <CommandRow
              label='Rollback'
              slot='rollback'
              tone='warning'
              command={rollbackCmd}
              accountId={accountId}
              eventId={eventId}
              canRun={canRun}
              disabled={running}
              onExecuted={onExecuted}
            />
          </Card>
        ) : null}
      </Box>
    </Card>
  );
}

ActionCard.propTypes = {
  index: PropTypes.number.isRequired,
  action: PropTypes.object.isRequired,
  hypothesis: PropTypes.object,
  accountId: PropTypes.string.isRequired,
  eventId: PropTypes.string,
  canRun: PropTypes.bool.isRequired,
  appliedAt: PropTypes.string,
  onExecuted: PropTypes.func,
  codeFix: PropTypes.object,
};

// RemediationPanel renders below the investigation analysis. "Generate Remediation" reuses the
// already-computed investigation as context (no re-investigation) and turns it into a structured plan
// of hypotheses + runnable actions, each with its own Run button.
// Statuses where the problem is genuinely over and remediation is likely moot.
//
// DUPLICATE is deliberately NOT here. It does not mean "already handled" — event_duplicates records
// it as occurrence N of a recurring problem, carrying first_event_id and occurrence_number, and the
// originating event is frequently still open. A problem on its 15th occurrence is the strongest case
// for a durable fix, not a reason to wave the operator off. DUPLICATE is also ~86% of events, so
// treating it as resolved suppressed the panel's whole point on almost every event.
const RESOLVED_STATUSES = ['RESOLVED', 'CLOSED'];

const RemediationPanel = ({ accountId, eventId, nbStatus }) => {
  const [plan, setPlan] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  // Successfully-executed commands for this event, keyed by command → applied timestamp. Lets the panel
  // show "already applied" and turn Run into Re-run on reload.
  const [executedCommands, setExecutedCommands] = useState({});
  const [initializing, setInitializing] = useState(true);
  // The code fix behind a command-less fix action ({files, diff, prUrl}), taken from the log-analysis
  // record rather than the plan, so the diff shown is always the real one.
  const [codeFix, setCodeFix] = useState(null);
  // Running any command requires write access; read-only users can still generate and read the plan
  // but the ▶ / Run buttons are disabled (the server 403s all three command kinds for them anyway).
  const canRun = hasWriteAccess(accountId);
  const autoTriggered = useRef(false);
  const mountedRef = useRef(true);
  useEffect(() => {
    // Set on the way in as well as cleared on the way out. Without the set, StrictMode's dev-mode
    // mount/unmount/mount on one instance leaves the ref false for the pass that actually renders,
    // and every state update guarded by it is silently dropped — the code-fix diff never appeared
    // and an applied command never got its badge, with no error to explain either.
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const isResolved = typeof nbStatus === 'string' && RESOLVED_STATUSES.includes(nbStatus.toUpperCase());

  const loadExecutions = async () => {
    if (!eventId) return;
    try {
      const rows = await apiRecommendations.listEventResolutions(eventId);
      if (!mountedRef.current) return;
      const map = {};
      (rows || []).forEach((r) => {
        if (r?.type === 'CommandExecution' && r?.status === 'Success') {
          const cmd = trimmedString(r.type_reference_id) || trimmedString(r?.data?.command);
          if (cmd && !map[cmd]) map[cmd] = r.updated_at || r.created_at || '';
        }
      });
      setExecutedCommands(map);
    } catch (err) {
      console.error('RemediationPanel: failed to load executions', err);
    }
  };

  const generate = async () => {
    setLoading(true);
    setError('');
    try {
      // Reuse the stored investigation/RCA text as context so we do not re-investigate.
      let context = '';
      // Surfaces that actually hold something applicable for this event. An action with no command is
      // carried out on one of these, so the server uses this list to decide whether such an action is
      // real — without it the model will still describe a code change it has no artifact for.
      const artifacts = [];
      try {
        const rec = await apiKubernetes.generateAiRecommendation(accountId, eventId, 'pod_log_analysis');
        if (rec && typeof rec === 'object') {
          const fix = extractCodeFix(rec);
          if (mountedRef.current) setCodeFix(fix);
          if (fix) artifacts.push('code_fix');
          context = [rec.summary, rec.investigation, rec.detailed_response, rec.root_cause_analysis]
            .filter((part) => typeof part === 'string' && part.trim())
            .join('\n\n');
          // Code analysis may already have produced the actual fix (file list + diff) on this same
          // record. Without it the model only sees prose and re-derives a fix from scratch — which is
          // how a code defect ends up "remediated" by a restart. Pass it through verbatim.
          context = [context, formatCodeFixContext(rec)].filter(Boolean).join('\n\n');
        }
      } catch (err) {
        // Non-fatal: fall through; the generate call reports an empty-context error if needed.
        console.error('RemediationPanel: failed to fetch investigation context', err);
      }
      // A tuned threshold is a remediation in its own right, and triage may already have computed one
      // for this event. Fetched separately (different service) and best-effort.
      try {
        const thresholdContext = formatThresholdContext(await apiTriage.getThresholdSuggestion(eventId));
        if (thresholdContext) artifacts.push('threshold');
        context = [context, thresholdContext].filter(Boolean).join('\n\n');
      } catch (err) {
        console.error('RemediationPanel: failed to fetch threshold suggestion', err);
      }
      // An investigation can complete with every field empty (the analyzer gave up, or produced only
      // structured data). The server rejects an empty context with a 400, which surfaced as a bare
      // failure; say plainly that there is nothing to work from instead.
      if (!context.trim()) {
        setError('This event has no investigation content to build a remediation from.');
        return;
      }
      const res = await apiKubernetes.generateRemediation(accountId, eventId, context, artifacts);
      if (res && typeof res === 'object' && Array.isArray(res.actions)) {
        setPlan(res);
      } else {
        setError('Could not generate a remediation plan for this event.');
      }
    } catch (e) {
      setError(e?.message || 'Failed to generate remediation.');
    } finally {
      setLoading(false);
    }
  };

  // On mount: restore the saved plan + past runs. Only if there's no saved plan and the account opts in
  // via the feature flag do we auto-generate. The panel is keyed on event + investigation refresh in the
  // parent, so a fresh mount corresponds to a completed investigation for this event.
  useEffect(() => {
    let cancelled = false;
    const init = async () => {
      let loaded = false;
      try {
        const saved = await apiKubernetes.getRemediation(accountId, eventId);
        if (saved && typeof saved === 'object' && Array.isArray(saved.actions)) {
          loaded = true;
          if (!cancelled) setPlan(saved);
          // A restored plan holds no diff (only generate() sees the analysis record), so fetch it
          // when the plan actually has a command-less fix to show it against. Skipped otherwise so
          // the common all-commands plan costs no extra request.
          const needsCodeFix = saved.actions.some((a) => trimmedString(a?.kind).toLowerCase() === 'fix' && !trimmedString(a?.execute_command));
          if (needsCodeFix) {
            const rec = await apiKubernetes.generateAiRecommendation(accountId, eventId, 'pod_log_analysis');
            if (!cancelled && mountedRef.current) setCodeFix(extractCodeFix(rec));
          }
        }
      } catch (err) {
        console.error('RemediationPanel: failed to load saved plan', err);
      }
      await loadExecutions();
      if (cancelled) return;
      setInitializing(false);
      if (!loaded && !autoTriggered.current) {
        const enabled = await hasFeatureAccess(AUTO_GENERATE_REMEDIATION_FLAG).catch(() => false);
        if (!cancelled && enabled) {
          autoTriggered.current = true;
          generate();
        }
      }
    };
    init();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const resolvedBanner = isResolved ? (
    <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', fontSize: 'var(--ds-text-small)' }}>
        <Box component='span' sx={{ color: 'var(--ds-green-600)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
          ✓
        </Box>
        <Box component='span'>
          <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
            {`This event is ${nbStatus.toLowerCase()}.`}
          </Box>{' '}
          Remediation may not be needed.
        </Box>
      </Box>
    </Card>
  ) : null;

  if (!plan) {
    return (
      <Box sx={{ mt: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
        {resolvedBanner}
        <Box sx={{ display: 'flex' }}>
          {initializing ? (
            <Box
              sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)' }}
            >
              <CircularProgress size={16} thickness={5} /> Loading remediation…
            </Box>
          ) : (
            <Button tone='primary' size='sm' onClick={generate} loading={loading} disabled={loading} data-testid='generate-remediation-btn'>
              Generate Remediation
            </Button>
          )}
        </Box>
        {error ? <Box sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)' }}>{error}</Box> : null}
      </Box>
    );
  }

  const hypotheses = Array.isArray(plan.hypotheses) ? plan.hypotheses : [];
  const hypByText = {};
  hypotheses.forEach((h) => {
    const key = trimmedString(h?.hypothesis);
    if (key) hypByText[key] = h;
  });
  const actions = Array.isArray(plan.actions) ? plan.actions : [];

  return (
    <Box sx={{ mt: 'var(--ds-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
        <Box sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body-lg)' }}>Remediation</Box>
        <Button tone='secondary' size='xs' onClick={generate} loading={loading} disabled={loading} data-testid='regenerate-remediation-btn'>
          Regenerate
        </Button>
      </Box>

      {error ? <Box sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)' }}>{error}</Box> : null}

      {resolvedBanner}

      {plan.root_cause ? (
        <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
          <Box sx={{ fontSize: 'var(--ds-text-small)' }}>
            <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
              Root cause:{' '}
            </Box>
            {plan.root_cause}
          </Box>
          {plan.summary ? (
            <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mt: 'var(--ds-space-1)' }}>{plan.summary}</Box>
          ) : null}
        </Card>
      ) : null}

      {actions.length === 0 ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
          <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
            {plan.summary || 'No automated remediation is recommended: the root cause is not conclusive enough.'}
          </Box>
          {hypotheses.map((h, i) => (
            <HypothesisBlock key={i} hypothesis={h} />
          ))}
        </Box>
      ) : (
        <>
          {hypotheses.length ? (
            <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
                <Box sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  {hypotheses.length > 1 ? 'Candidate causes' : 'Candidate cause'}
                </Box>
                {hypotheses.map((h, i) => (
                  <HypothesisBlock key={i} hypothesis={h} />
                ))}
              </Box>
            </Card>
          ) : null}
          {actions.map((action, idx) => (
            <ActionCard
              key={idx}
              index={idx}
              action={action}
              hypothesis={hypByText[trimmedString(action?.hypothesis)]}
              accountId={accountId}
              eventId={eventId}
              canRun={canRun}
              appliedAt={executedCommands[trimmedString(action?.execute_command)] || ''}
              onExecuted={loadExecutions}
              codeFix={codeFix}
            />
          ))}
        </>
      )}
    </Box>
  );
};

RemediationPanel.propTypes = {
  accountId: PropTypes.string,
  eventId: PropTypes.string,
  nbStatus: PropTypes.string,
};

export default RemediationPanel;
