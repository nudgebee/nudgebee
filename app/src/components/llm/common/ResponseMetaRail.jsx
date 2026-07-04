import dayjs from 'dayjs';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { formatDurationInTrace } from 'src/utils/common';
import { ds } from '@utils/colors';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import { MessageTokenUsage } from './TokenUsageDisplay';

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
// count and pick the strongest mode for tone:
//   - any "enforce" hit → 'critical' (the call was blocked)
//   - else any "audit" hit → 'warning' (call went through but secrets detected)
//   - else nothing rendered
// Tones must come from the design system's ChipTone union (see Chip.tsx) —
// passing an unrecognised tone crashes the resolveColors call.
//
// Chip label says WHAT was caught ("secret blocked" / "secret detected"),
// not just "flagged" — a reader shouldn't have to hover to know whether the
// call was refused or merely noted. The tooltip then lists the rule ids that
// fired and the audit ids so support can correlate against backend logs.
const egressfilterItem = (events) => {
  if (!Array.isArray(events) || events.length === 0) {
    return null;
  }
  const hasEnforce = events.some((e) => e?.mode === 'enforce');
  const hasAudit = events.some((e) => e?.mode === 'audit');
  if (!hasEnforce && !hasAudit) {
    return null;
  }

  const tone = hasEnforce ? 'critical' : 'warning';
  const verb = hasEnforce ? 'blocked' : 'detected';

  // hit_count may be missing on a malformed event row; min 1 so the chip
  // never renders "0 secret blocked".
  const totalHits = Math.max(
    1,
    events.reduce((n, e) => n + (Number(e?.hit_count) || 0), 0)
  );
  const noun = totalHits === 1 ? 'secret' : 'secrets';
  const label = `${noun} ${verb}`;

  // Distinct rule ids across all events on this message, for the tooltip.
  // Deduped because the same rule firing on multiple LLM calls would
  // otherwise repeat in the list.
  const ruleSet = new Set();
  events.forEach((e) => {
    if (Array.isArray(e?.rule_ids)) {
      e.rule_ids.forEach((r) => r && ruleSet.add(r));
    }
  });
  const ruleList = Array.from(ruleSet).join(', ');

  const auditIds = events
    .map((e) => e?.audit_id)
    .filter(Boolean)
    .join(', ');

  // Build the tooltip with structured periods so each fact reads as its own
  // sentence: what fired, the cross-call scope (only when relevant), audit
  // ids for support correlation.
  const tooltipParts = [];
  if (ruleList) {
    tooltipParts.push(`${hasEnforce ? 'Blocked' : 'Detected'}: ${ruleList}`);
  }
  if (events.length > 1) {
    tooltipParts.push(`${totalHits} hit${totalHits === 1 ? '' : 's'} across ${events.length} calls`);
  }
  if (auditIds) {
    tooltipParts.push(`Audit: ${auditIds}`);
  }
  const tooltip = tooltipParts.join('. ') || label;

  return {
    key: 'egressfilter',
    node: (
      <Tooltip title={tooltip} placement='top'>
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center' }}>
          <Chip variant='count' tone={tone} count={totalHits} aria-label={tooltip} size='xs'>
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
  const filterItem = egressfilterItem(props.egressfilterEvents);
  if (filterItem) {
    items.push(filterItem);
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
  onOpenTasks,
  onOpenContexts,
  onOpenMemories,
  messageTokenData,
  onTokenUsageHover,
  isFetchingTokenData,
  egressfilterEvents,
}) => {
  const duration = formatDuration(createdAt, updatedAt);
  const absoluteTime = formatAbsoluteTime(updatedAt || createdAt);

  const items = buildItems({
    taskCount,
    contextCount,
    memoryCount,
    onOpenTasks,
    onOpenContexts,
    onOpenMemories,
    messageTokenData,
    onTokenUsageHover,
    isFetchingTokenData,
    egressfilterEvents,
    duration,
    absoluteTime,
  });

  if (items.length === 0) {
    return null;
  }

  return (
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
  );
};

ResponseMetaRail.propTypes = {
  createdAt: PropTypes.string,
  updatedAt: PropTypes.string,
  taskCount: PropTypes.number,
  contextCount: PropTypes.number,
  memoryCount: PropTypes.number,
  onOpenTasks: PropTypes.func,
  onOpenContexts: PropTypes.func,
  onOpenMemories: PropTypes.func,
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
