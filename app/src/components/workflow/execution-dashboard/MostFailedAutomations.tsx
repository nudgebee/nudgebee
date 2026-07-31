import React from 'react';
import { Box, Typography } from '@mui/material';
import { Card } from '@ui/Card';
import { Label } from '@ui/Label';
import { List } from '@ui/List';
import { ProgressBar } from '@ui/ProgressBar';
import Tooltip from '@ui/Tooltip';
import type { FailedAutomationCount } from '@api1/workflow/types';

interface MostFailedAutomationsProps {
  entries: FailedAutomationCount[];
  /** True when more failures matched than the server could scan in one pass. */
  approximate: boolean;
  /** Every failure in the window — what each row's share is taken of. */
  totalFailures: number;
  /** Narrows the table to this automation. Affects nothing else on the page. */
  onSelectAutomation: (workflowId: string) => void;
}

const NAME_MAX_CHARS = 28;

/** Share of all failures at which a row stops being routine and starts being the problem. */
const CRITICAL_SHARE_PCT = 25;
const WARNING_SHARE_PCT = 5;

const shareTone = (pct: number): 'critical' | 'warning' | 'neutral' => {
  if (pct >= CRITICAL_SHARE_PCT) return 'critical';
  if (pct >= WARNING_SHARE_PCT) return 'warning';
  return 'neutral';
};

/**
 * Which automations the failures are coming from.
 *
 * Ranked server-side by tallying failures over the retention window, so it
 * answers "failed most recently", not "is currently red" — the Automations tab
 * already answers the latter. Clicking a row filters the table and nothing else.
 *
 * Two numbers per row, measuring different things:
 *   - the bar is relative to the worst automation, so the ranking stays
 *     readable even when every share is small;
 *   - the badge is the row's share of *all* failures in the window, which is
 *     what says whether fixing this one automation would move the needle.
 *
 * While the ranking is approximate the badge is a floor rather than an exact
 * share — hence the header caption. (Exact counts: #35307.)
 */
const MostFailedAutomations: React.FC<MostFailedAutomationsProps> = ({ entries, approximate, totalFailures, onSelectAutomation }) => {
  // An empty panel is worse than no panel.
  if (!entries.length) return null;

  const worstCount = entries[0]?.failure_count || 1;
  // Falls back to the ranked entries when the aggregate could not be read, so a
  // share is never taken of zero.
  const shareBase = totalFailures > 0 ? totalFailures : entries.reduce((sum, entry) => sum + entry.failure_count, 0) || 1;

  const renderRow = (entry: FailedAutomationCount) => {
    const name = entry.workflow_name || entry.workflow_id;
    const truncated = name.length > NAME_MAX_CHARS;
    const pct = (entry.failure_count / shareBase) * 100;
    const nameNode = (
      <Typography
        sx={{
          fontFamily: 'var(--ds-font-mono)',
          fontSize: 'var(--ds-text-small)',
          color: 'var(--ds-gray-700)',
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
        }}
      >
        {truncated ? `${name.slice(0, NAME_MAX_CHARS)}…` : name}
      </Typography>
    );

    return (
      // The row is the click target rather than the name alone — a 4px-tall bar
      // and a badge are not things to aim at. No DS primitive models a
      // clickable list row with custom content (Card interactive would nest a
      // Card inside a Card), so the row carries its own button semantics.
      <Box
        role='button'
        tabIndex={0}
        id={`execution-dashboard-most-failed-${entry.workflow_id}`}
        data-testid={`execution-dashboard-most-failed-${entry.workflow_id}`}
        aria-label={`Filter executions to ${name}`}
        onClick={() => onSelectAutomation(entry.workflow_id)}
        onKeyDown={(event: React.KeyboardEvent) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            onSelectAutomation(entry.workflow_id);
          }
        }}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-3)',
          width: '100%',
          minWidth: 0,
          cursor: 'pointer',
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          borderRadius: 'var(--ds-radius-sm)',
          '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
          '&:focus-visible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '2px' },
        }}
      >
        <Box sx={{ width: '38%', minWidth: 0 }}>
          {/* Only tooltip what was actually shortened — a tooltip repeating the
              visible label is noise. */}
          {truncated ? <Tooltip title={name}>{nameNode}</Tooltip> : nameNode}
        </Box>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          {/* Explicit tone: a failure count is a status read-out, not a
              utilisation level with thresholds to resolve against. */}
          <ProgressBar value={entry.failure_count} max={worstCount} tone='critical' size='md' />
        </Box>
        <Typography
          sx={{
            fontFamily: 'var(--ds-font-mono)',
            fontSize: 'var(--ds-text-small)',
            fontVariantNumeric: 'tabular-nums',
            color: 'var(--ds-gray-700)',
            textAlign: 'right',
            minWidth: '48px',
          }}
        >
          {entry.failure_count.toLocaleString()}
        </Typography>
        <Box sx={{ minWidth: '58px', display: 'flex', justifyContent: 'flex-end' }}>
          <Label tone={shareTone(pct)}>{pct > 0 && pct < 0.1 ? '<0.1%' : `${pct.toFixed(1)}%`}</Label>
        </Box>
      </Box>
    );
  };

  return (
    <Card
      variant='outlined'
      size='md'
      id='execution-dashboard-most-failed'
      data-testid='execution-dashboard-most-failed'
      sx={{ minWidth: 0 }}
      header={
        <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
            Where the failures are
          </Typography>
          {approximate && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}>
              ranked over the first 1,000 failures
            </Typography>
          )}
        </Box>
      }
    >
      {/* List pads every row by space-3 on all four sides, which on five rows
          is ~120px of air and leaves this panel towering over the summary
          beside it. The rows carry their own hover target, so the padding is
          tightened to the vertical rhythm the rows actually need. */}
      <Box sx={{ '& [role="listitem"]': { padding: 0 } }}>
        <List items={entries} keyFor={(entry) => entry.workflow_id} renderItem={renderRow} divider='none' bordered={false} />
      </Box>
    </Card>
  );
};

export default MostFailedAutomations;
