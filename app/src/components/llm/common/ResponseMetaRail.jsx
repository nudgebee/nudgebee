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

const formatDuration = (createdAt, updatedAt) => {
  if (!createdAt || !updatedAt) {
    return null;
  }
  const start = new Date(createdAt).getTime();
  const end = new Date(updatedAt).getTime();
  const diffMs = end - start;
  if (Number.isNaN(diffMs) || diffMs < 0) {
    return null;
  }
  return formatDurationInTrace(diffMs * 1000000, false);
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

const buildItems = (props) => {
  const items = [];
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
    // `boundary: true` swaps the trailing separator from `·` to `|` — visually distinguishes
    // "how long it took" from "when it happened".
    items.push({
      key: 'duration',
      node: (
        <Chip variant='tag' size='xs' tone='neutral'>
          {props.duration}
        </Chip>
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
}) => {
  const duration = formatDuration(createdAt, updatedAt);
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
};

export default ResponseMetaRail;

// Exported for unit tests only. Not part of the public component API.
export { egressfilterItem as __egressfilterItemForTest, piiScrubItem as __piiScrubItemForTest };
