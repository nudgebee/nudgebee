import * as React from 'react';
import dayjs from 'dayjs';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { formatDurationInTrace } from 'src/utils/common';
import { ds } from '@utils/colors';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import { MessageTokenUsage } from './TokenUsageDisplay';
import EgressFilterDetailModal from './EgressFilterDetailModal';

// The chip shows the model's own time — followup wait (inside [createdAt,
// updatedAt] since the row isn't COMPLETED until it resumes) is subtracted
// out and broken back out in the tooltip when present.
const formatDuration = (createdAt, updatedAt, followupWaitSeconds) => {
  if (!createdAt || !updatedAt) {
    return null;
  }
  const start = new Date(createdAt).getTime();
  const end = new Date(updatedAt).getTime();
  const totalMs = end - start;
  if (Number.isNaN(totalMs) || totalMs < 0) {
    return null;
  }
  const waitMs = Math.max(0, Number(followupWaitSeconds) || 0) * 1000;
  const answerMs = Math.max(0, totalMs - waitMs);
  return {
    // formatDurationInTrace returns '0ns' at exactly 0 — guard against that
    // unit leaking through if a wait is ever clamped to the full span.
    text: answerMs === 0 ? '0s' : formatDurationInTrace(answerMs * 1000000, false),
    waitText: waitMs > 0 ? formatDurationInTrace(waitMs * 1000000, false) : null,
  };
};

// `DD-MMM HH:mm` in the browser's local timezone, e.g. "28-Apr 17:02".
const formatAbsoluteTime = (iso) => {
  if (!iso) {
    return null;
  }
  const d = dayjs(iso);
  if (!d.isValid()) {
    return null;
  }
  return d.format('DD-MMM HH:mm');
};

const Dot = () => (
  <Box component='span' sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-caption)', userSelect: 'none', lineHeight: 1 }}>
    ·
  </Box>
);

const Bar = () => (
  <Box
    component='span'
    sx={{
      color: 'var(--ds-gray-300)',
      fontSize: 'var(--ds-text-small)',
      userSelect: 'none',
      lineHeight: 1,
      mx: ds.space[0],
    }}
  >
    |
  </Box>
);

// Per-row builders — extracted so `buildItems` stays shallow under Sonar S3776
// (cognitive-complexity limit).
const tokenUsageItem = ({ messageTokenData, onTokenUsageHover, isFetchingTokenData }) => ({
  key: 'tokens',
  node: (
    <Box onMouseEnter={onTokenUsageHover} sx={{ display: 'inline-flex', alignItems: 'center' }}>
      <MessageTokenUsage messageData={messageTokenData} onHover={onTokenUsageHover} isLoading={isFetchingTokenData} />
    </Box>
  ),
});

// Plural-aware label helper for count chips (`tasks`, `contexts`, `memories`).
const COUNT_LABELS = {
  tasks: ['task', 'tasks'],
  contexts: ['context', 'contexts'],
  memories: ['memory', 'memories'],
  channels: ['channel', 'channels'],
  watches: ['watch', 'watches'],
};

const countItem = (key, tone, count, onClick) => {
  const [singular, plural] = COUNT_LABELS[key];
  const label = count === 1 ? singular : plural;
  return {
    key,
    node: (
      <Chip variant='count' tone={tone} count={count} onClick={onClick} aria-label={`${count} ${label}, open details`} size='xs'>
        {label}
      </Chip>
    ),
  };
};

// Per-message egressfilter signal — a single chip summarising the events
// the outbound filter emitted on this turn. One message can produce multiple
// FilterEvents (the planner may make several LLM calls), so we surface a
// count and pick the strongest mode for tone (enforce > redact > detect):
//   - any "enforce" hit → 'critical' + "blocked" (the call was refused)
//   - else any "redact" hit → 'warning' + "redacted" (call went through but the payload was mutated)
//   - else any "detect" hit → 'warning' + "detected" (call went through, no action taken)
//   - else nothing rendered
//
// Mode string compatibility: the Go backend uses "detect" as the canonical
// mode string (renamed from "audit" in PR #33187 — see docs/llm-egress-filter.md
// §6) and added "redact" in the redact-mode PR. We accept:
//   - "detect" (canonical) and "audit" (legacy alias, pre-#33187 rows)
//   - "enforce"
//   - "redact"
// Without accepting each new backend mode string here, the chip silently
// returns null for every event and no chip renders — same-shape lesson as
// the audit→detect regression fixed in PR #33334.
//
// Tones must come from the design system's ChipTone union (see Chip.tsx) —
// passing an unrecognised tone crashes the resolveColors call.
//
// Chip label says WHAT was done ("secret blocked" / "secret redacted" /
// "secret detected") so a reader doesn't have to hover to know whether the
// call was refused, its payload rewritten, or merely noted. The tooltip
// then lists the rule ids that fired and the audit ids so support can
// correlate against backend logs.
//
// Polymorphic array (PR #31514): `metadata.egressfilter[]` now holds events
// from every outbound-inspection detector in the family (secrets +
// EE PII scrubber), discriminated by each entry's `detector` field
// ("secrets" / "pii"). This chip owns ONLY the secrets slice — PII gets its
// own chip in a follow-up. We filter first so hit_count sums and rule_ids
// tooltips never accidentally include PII entries (which have no `mode` /
// `rule_ids` and would silently corrupt the tallies).
// Legacy compat: rows persisted before the `detector` field landed lack
// the key; absent === "secrets" so historical events keep rendering.
const egressfilterItem = (events, onClickDetails) => {
  if (!Array.isArray(events) || events.length === 0) {
    return null;
  }
  const secretEvents = events.filter((e) => e?.detector === 'secrets' || !e?.detector);
  if (secretEvents.length === 0) {
    return null;
  }
  const hasEnforce = secretEvents.some((e) => e?.mode === 'enforce');
  const hasRedact = secretEvents.some((e) => e?.mode === 'redact');
  const hasDetect = secretEvents.some((e) => e?.mode === 'detect' || e?.mode === 'audit');
  if (!hasEnforce && !hasRedact && !hasDetect) {
    return null;
  }

  const tone = hasEnforce ? 'critical' : 'warning';
  const verb = hasEnforce ? 'blocked' : hasRedact ? 'redacted' : 'detected';

  // hit_count may be missing on a malformed event row; min 1 so the chip
  // never renders "0 secret blocked".
  const totalHits = Math.max(
    1,
    secretEvents.reduce((n, e) => n + (Number(e?.hit_count) || 0), 0)
  );
  const noun = totalHits === 1 ? 'secret' : 'secrets';
  const label = `${noun} ${verb}`;

  // Distinct rule ids across all events on this message, for the tooltip.
  // Deduped because the same rule firing on multiple LLM calls would
  // otherwise repeat in the list.
  const ruleSet = new Set();
  secretEvents.forEach((e) => {
    if (Array.isArray(e?.rule_ids)) {
      e.rule_ids.forEach((r) => r && ruleSet.add(r));
    }
  });
  const ruleList = Array.from(ruleSet).join(', ');

  const auditIds = secretEvents
    .map((e) => e?.audit_id)
    .filter(Boolean)
    .join(', ');

  // Build the tooltip with structured periods so each fact reads as its own
  // sentence: what fired, the cross-call scope (only when relevant), audit
  // ids for support correlation.
  const tooltipParts = [];
  if (ruleList) {
    const tooltipVerb = hasEnforce ? 'Blocked' : hasRedact ? 'Redacted' : 'Detected';
    tooltipParts.push(`${tooltipVerb}: ${ruleList}`);
  }
  if (secretEvents.length > 1) {
    tooltipParts.push(`${totalHits} hit${totalHits === 1 ? '' : 's'} across ${secretEvents.length} calls`);
  }
  if (auditIds) {
    tooltipParts.push(`Audit: ${auditIds}`);
  }
  const tooltip = tooltipParts.join('. ') || label;

  // Tooltip hints at click affordance so users know there's more detail
  // than the compact chip surfaces.
  const clickHint = onClickDetails ? ' — click to see details' : '';

  return {
    key: 'egressfilter',
    node: (
      <Tooltip title={tooltip + clickHint} placement='top'>
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center' }}>
          <Chip variant='count' tone={tone} count={totalHits} aria-label={tooltip + clickHint} size='xs' onClick={onClickDetails}>
            {label}
          </Chip>
        </Box>
      </Tooltip>
    ),
  };
};

// Per-message PII scrubbing signal — sibling of egressfilterItem for the EE
// ee/scrubbing wrapper. PIIScrubEvent has a different shape from FilterEvent
// (no `mode` — the wrapper always tokenizes reversibly; the detect/enforce
// distinction lives on outage policy, not per-event). We show `N PII
// scrubbed` with a warning tone and a tooltip listing distinct categories
// detected (`EMAIL, PERSON`) + audit ids for support correlation.
// Deliberately does NOT surface payload_bytes or agent_name in the chip —
// those are for dashboards, not the message rail.
const piiScrubItem = (events, onClickDetails) => {
  if (!Array.isArray(events) || events.length === 0) {
    return null;
  }
  const piiEvents = events.filter((e) => e?.detector === 'pii');
  if (piiEvents.length === 0) {
    return null;
  }

  const totalHits = Math.max(
    1,
    piiEvents.reduce((n, e) => n + (Number(e?.hit_count) || 0), 0)
  );

  // Distinct categories across all PII events, sorted for stable rendering.
  const catSet = new Set();
  piiEvents.forEach((e) => {
    if (Array.isArray(e?.categories)) {
      e.categories.forEach((c) => c && catSet.add(String(c)));
    }
  });
  const catList = Array.from(catSet).sort().join(', ');

  // Dedupe audit ids — two PII events on the same message could carry the
  // same id (retries, or a badly-behaved caller emitting duplicates), and
  // "scrub-abc, scrub-abc" is confusing noise in the tooltip.
  const auditIds = Array.from(new Set(piiEvents.map((e) => e?.audit_id).filter(Boolean))).join(', ');

  const tooltipParts = [];
  if (catList) {
    tooltipParts.push(`Scrubbed: ${catList}`);
  }
  if (piiEvents.length > 1) {
    tooltipParts.push(`${totalHits} value${totalHits === 1 ? '' : 's'} across ${piiEvents.length} calls`);
  }
  if (auditIds) {
    tooltipParts.push(`Audit: ${auditIds}`);
  }
  const label = totalHits === 1 ? 'PII scrubbed' : 'PII values scrubbed';
  const tooltip = tooltipParts.join('. ') || label;
  const clickHint = onClickDetails ? ' — click to see details' : '';

  return {
    key: 'pii',
    node: (
      <Tooltip title={tooltip + clickHint} placement='top'>
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center' }}>
          <Chip variant='count' tone='warning' count={totalHits} aria-label={tooltip + clickHint} size='xs' onClick={onClickDetails}>
            {label}
          </Chip>
        </Box>
      </Tooltip>
    ),
  };
};

// Per-message AI confidence signal — the agent's self-assessment of how well
// its answer is supported by the evidence it gathered, written by llm-server to
// `metadata.confidence` (see agents/core/answer_confidence.go).
//
// Only investigation turns produce one, and only when the model emitted a
// parseable `<confidence>` block — a missing or unrecognised level means the
// backend stored nothing and we render nothing. We never infer a level here:
// a fabricated badge is worse than an absent one.
//
// Levels are the closed set the backend normalises to (high / medium / low);
// anything else is treated as absent, mirroring normalizeConfidenceLevel. Tones
// must come from the design system's ChipTone union (see Chip.tsx).
//
// The tooltip carries the model's rationale and the concrete gaps it could not
// correlate — the "say what you couldn't verify" half of the feature. The
// answer body states the same thing in prose (the prompt requires it for
// medium/low), so the chip is a glance summary, not the only disclosure.
const CONFIDENCE_TONES = {
  high: 'success',
  medium: 'warning',
  low: 'critical',
};

// Heading colour per level — the same green/amber/red families the chip tones
// resolve to (Chip.tsx TONE_PALETTE), so the tooltip header reads as the same
// signal as the pill the user is hovering.
const CONFIDENCE_HEADING_COLORS = {
  high: 'var(--ds-green-700)',
  medium: 'var(--ds-amber-700)',
  low: 'var(--ds-red-700)',
};

// One-line gloss of what each level means, so the tooltip explains the badge
// rather than assuming the reader knows the rubric. Mirrors the rubric in
// llm-server's planner_react_3_base.txt (CONFIDENCE SELF-ASSESSMENT).
const CONFIDENCE_SUMMARIES = {
  high: 'Root cause identified and corroborated by multiple independent sources.',
  medium: 'Root cause identified, but corroboration is partial or some data was unavailable.',
  low: 'No specific root cause could be confirmed from the available data.',
};

// Plain-text mirror of the tooltip, for `aria-label` — the visual tooltip is
// JSX, and a screen reader needs the same content as a flat string on the chip
// itself (a hover-only tooltip is not reliably announced).
const confidenceAriaLabel = (level, rationale, limitations) => {
  const parts = [`${level} confidence.`, CONFIDENCE_SUMMARIES[level]];
  if (rationale) {
    parts.push(rationale);
  }
  if (limitations.length > 0) {
    parts.push(`Could not verify: ${limitations.join('; ')}.`);
  }
  return parts.join(' ');
};

// Structured tooltip body: a tone-coloured heading, the level gloss, the
// model's own rationale, then the unverified gaps as a real bullet list.
// Previously this was one run-on sentence, which buried the gaps — the part a
// reader most needs to scan. Tooltip's `title` takes a ReactNode and its
// default variant already scrolls past 400px, so a list is safe here.
const confidenceTooltip = (level, rationale, limitations) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
    <Box
      component='span'
      sx={{
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-semibold)',
        color: CONFIDENCE_HEADING_COLORS[level],
        textTransform: 'capitalize',
      }}
    >
      {level} confidence
    </Box>

    <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
      {CONFIDENCE_SUMMARIES[level]}
    </Box>

    {rationale && (
      <Box component='span' sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-foreground)' }}>
        {rationale}
      </Box>
    )}

    {limitations.length > 0 && (
      <Box sx={{ mt: ds.space[0] }}>
        <Box
          component='span'
          sx={{
            display: 'block',
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-600)',
            textTransform: 'uppercase',
            letterSpacing: '0.04em',
            mb: ds.space[0],
          }}
        >
          Could not verify
        </Box>
        <Box
          component='ul'
          sx={{
            listStyle: 'none',
            m: 0,
            p: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: ds.space[0],
          }}
        >
          {limitations.map((item, idx) => (
            <Box
              component='li'
              // Index-suffixed: the list is model-generated and never reordered,
              // so two identical gaps would otherwise collide on key.
              key={`${idx}-${item}`}
              sx={{
                position: 'relative',
                pl: ds.space[3],
                fontSize: 'var(--ds-text-small)',
                color: 'var(--ds-foreground)',
                // Bullet drawn via ::before rather than list-style so it aligns
                // with the first line of a wrapped multi-line gap.
                '&::before': {
                  content: '"•"',
                  position: 'absolute',
                  left: 0,
                  color: 'var(--ds-gray-500)',
                },
              }}
            >
              {item}
            </Box>
          ))}
        </Box>
      </Box>
    )}
  </Box>
);

const confidenceItem = (confidence) => {
  const level = typeof confidence?.level === 'string' ? confidence.level.toLowerCase() : '';
  const tone = CONFIDENCE_TONES[level];
  if (!tone) {
    return null;
  }

  const label = `${level} confidence`;
  const rationale = confidence.rationale ? String(confidence.rationale) : '';
  const limitations = Array.isArray(confidence.limitations) ? confidence.limitations.filter(Boolean).map(String) : [];

  // Pinned tooltip width: Tooltip's auto-sizing jumps between 300px and 550px
  // on a text-length threshold, which would render the same chip at two
  // different widths depending on how verbose the model was. 380px keeps bullet
  // lines at a scannable length either way.
  return {
    key: 'confidence',
    node: (
      <Tooltip title={confidenceTooltip(level, rationale, limitations)} placement='top' tooltipStyle={{ maxWidth: '380px' }}>
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center' }}>
          <Chip
            variant='tag'
            tone={tone}
            size='xs'
            aria-label={confidenceAriaLabel(level, rationale, limitations)}
            sx={{ textTransform: 'capitalize' }}
          >
            {label}
          </Chip>
        </Box>
      </Tooltip>
    ),
  };
};

const buildItems = (props) => {
  const items = [];
  // Confidence leads the rail: it qualifies how much to trust everything the
  // answer says, so it should be read before the counts and timings.
  const confidence = confidenceItem(props.confidence);
  if (confidence) {
    items.push(confidence);
  }
  // Token-usage widget always renders for response messages — the widget itself shows a
  // placeholder until data arrives, and `onTokenUsageHover` lazy-fetches on first hover.
  if (props.onTokenUsageHover) {
    items.push(tokenUsageItem(props));
  }
  if (props.taskCount > 0 && props.onOpenTasks) {
    items.push(countItem('tasks', 'info', props.taskCount, props.onOpenTasks));
  }
  if (props.contextCount > 0 && props.onOpenContexts) {
    items.push(countItem('contexts', 'agent', props.contextCount, props.onOpenContexts));
  }
  if (props.memoryCount > 0 && props.onOpenMemories) {
    items.push(countItem('memories', 'savings', props.memoryCount, props.onOpenMemories));
  }
  if (props.channelCount > 0 && props.onOpenChannels) {
    // 'agent' tone, same as contexts — both are grounded-context categories;
    // the label carries the distinction. Renders only on Slack-originated
    // turns that actually drew on a watched channel.
    items.push(countItem('channels', 'agent', props.channelCount, props.onOpenChannels));
  }
  const filterItem = egressfilterItem(props.egressfilterEvents, props.onOpenEgressDetails);
  if (filterItem) {
    items.push(filterItem);
  }
  // PII chip renders alongside (or in place of) the secrets chip. The two are
  // independent — a turn can trigger secrets only, PII only, both, or
  // neither. Order: secrets then PII so the higher-severity/policy chip
  // (secrets, which can carry an 'enforce' verdict) reads first left-to-right.
  const piiItem = piiScrubItem(props.egressfilterEvents, props.onOpenEgressDetails);
  if (piiItem) {
    items.push(piiItem);
  }
  if (props.watchCount > 0 && props.onOpenWatches) {
    // 'success' tone (green family) — visually separate from tasks/contexts/
    // memories so the user clocks "this is a different category" at a glance.
    // Watches imply forward-motion ("agent is still working"), which green
    // carries well. MUST be a valid ChipTone (see Chip.tsx) — a raw hue like
    // 'green' is not a tone and crashes TONE_PALETTE lookup.
    items.push(countItem('watches', 'success', props.watchCount, props.onOpenWatches));
  }
  if (props.duration) {
    const chip = (
      <Chip variant='tag' size='xs' tone='neutral'>
        {props.duration.text}
      </Chip>
    );
    // `boundary: true` swaps the trailing separator from `·` to `|` — visually distinguishes
    // "how long it took" from "when it happened".
    items.push({
      key: 'duration',
      node: props.duration.waitText ? (
        <Tooltip
          title={
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
              <Box component='span' sx={{ display: 'flex', justifyContent: 'space-between', gap: ds.space[3] }}>
                <Box component='span'>Answer</Box>
                <Box component='span'>{props.duration.text}</Box>
              </Box>
              <Box component='span' sx={{ display: 'flex', justifyContent: 'space-between', gap: ds.space[3] }}>
                <Box component='span'>Waited for approval</Box>
                <Box component='span'>{props.duration.waitText}</Box>
              </Box>
            </Box>
          }
          placement='top'
        >
          {/* MUI clones the Tooltip child with a ref; Chip.tsx is a plain function
              component (no forwardRef), so it can't hold one directly — same
              pattern the other three Tooltip+Chip pairs in this file already use. */}
          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center' }}>
            {chip}
          </Box>
        </Tooltip>
      ) : (
        chip
      ),
      boundary: true,
    });
  }
  if (props.absoluteTime) {
    items.push({
      key: 'time',
      node: (
        <Chip variant='tag' size='xs' tone='neutral'>
          {props.absoluteTime}
        </Chip>
      ),
    });
  }
  return items;
};

const ResponseMetaRail = ({
  createdAt,
  updatedAt,
  followupWaitSeconds,
  taskCount = 0,
  contextCount = 0,
  memoryCount = 0,
  channelCount = 0,
  watchCount = 0,
  onOpenTasks,
  onOpenContexts,
  onOpenMemories,
  onOpenChannels,
  onOpenWatches,
  messageTokenData,
  onTokenUsageHover,
  isFetchingTokenData,
  egressfilterEvents,
  confidence,
}) => {
  const duration = formatDuration(createdAt, updatedAt, followupWaitSeconds);
  const absoluteTime = formatAbsoluteTime(updatedAt || createdAt);

  // Modal state lives here (not in the chip factories) so the chips can
  // stay pure render functions and the modal is a single instance per rail.
  const [detailsOpen, setDetailsOpen] = React.useState(false);
  const hasEgressEvents = Array.isArray(egressfilterEvents) && egressfilterEvents.length > 0;
  const onOpenEgressDetails = hasEgressEvents ? () => setDetailsOpen(true) : undefined;

  const items = buildItems({
    taskCount,
    contextCount,
    memoryCount,
    channelCount,
    watchCount,
    onOpenTasks,
    onOpenContexts,
    onOpenMemories,
    onOpenChannels,
    onOpenWatches,
    messageTokenData,
    onTokenUsageHover,
    isFetchingTokenData,
    egressfilterEvents,
    confidence,
    onOpenEgressDetails,
    duration,
    absoluteTime,
  });

  if (items.length === 0) {
    return null;
  }

  // Pre-split for the modal so it doesn't re-do the filter every render.
  const secretEvents = hasEgressEvents ? egressfilterEvents.filter((e) => e?.detector === 'secrets' || !e?.detector) : [];
  const piiEvents = hasEgressEvents ? egressfilterEvents.filter((e) => e?.detector === 'pii') : [];

  return (
    <>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          gap: ds.space[2],
          rowGap: ds.space.mul(0, 3),
          justifyContent: 'flex-end',
          '@media (max-width: 768px)': {
            justifyContent: 'flex-start',
          },
        }}
      >
        {items.map((item, idx) => (
          <Box key={item.key} sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
            {item.node}
            {idx < items.length - 1 && (item.boundary ? <Bar /> : <Dot />)}
          </Box>
        ))}
      </Box>
      {detailsOpen && (
        <EgressFilterDetailModal open={detailsOpen} onClose={() => setDetailsOpen(false)} secretEvents={secretEvents} piiEvents={piiEvents} />
      )}
    </>
  );
};

ResponseMetaRail.propTypes = {
  createdAt: PropTypes.string,
  updatedAt: PropTypes.string,
  followupWaitSeconds: PropTypes.number,
  taskCount: PropTypes.number,
  contextCount: PropTypes.number,
  memoryCount: PropTypes.number,
  channelCount: PropTypes.number,
  watchCount: PropTypes.number,
  onOpenTasks: PropTypes.func,
  onOpenContexts: PropTypes.func,
  onOpenMemories: PropTypes.func,
  onOpenChannels: PropTypes.func,
  onOpenWatches: PropTypes.func,
  messageTokenData: PropTypes.any,
  onTokenUsageHover: PropTypes.func,
  isFetchingTokenData: PropTypes.bool,
  // Parsed `metadata.egressfilter` array from the message — one entry per
  // outbound LLM call that produced hits. Null/empty/undefined renders no chip.
  egressfilterEvents: PropTypes.arrayOf(
    PropTypes.shape({
      audit_id: PropTypes.string,
      mode: PropTypes.string,
      hit_count: PropTypes.number,
      rule_ids: PropTypes.arrayOf(PropTypes.string),
    })
  ),
  // Parsed `metadata.confidence` object from the message — the agent's
  // investigation self-assessment. Null/undefined (query turns, or an
  // investigation whose answer carried no parseable block) renders no chip.
  confidence: PropTypes.shape({
    level: PropTypes.oneOf(['high', 'medium', 'low']),
    rationale: PropTypes.string,
    limitations: PropTypes.arrayOf(PropTypes.string),
  }),
};

export default ResponseMetaRail;

// Exported for unit tests only. Not part of the public component API.
export {
  egressfilterItem as __egressfilterItemForTest,
  piiScrubItem as __piiScrubItemForTest,
  confidenceItem as __confidenceItemForTest,
  formatDuration as __formatDurationForTest,
};
