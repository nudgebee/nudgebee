import React from 'react';
import { Box, Typography } from '@mui/material';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import type { FailedAutomationCount } from '@api1/workflow/types';

interface MostFailedAutomationsProps {
  entries: FailedAutomationCount[];
  /** True when more failures matched than the server could scan in one pass. */
  approximate: boolean;
  /** Temporal namespace retention, which is the window this ranking covers. */
  retentionDays: number;
  /** Narrows the table to this automation. Affects nothing else on the page. */
  onSelectAutomation: (workflowId: string) => void;
}

const NAME_MAX_CHARS = 24;

/**
 * Which automations broke most, as a single inline strip.
 *
 * Ranked server-side by tallying failures over the retention window, so it
 * answers "failed most recently", not "is currently red" — the Automations tab
 * already answers the latter. Like the cards above it is static: clicking a
 * chip filters the table and nothing else.
 */
const MostFailedAutomations: React.FC<MostFailedAutomationsProps> = ({ entries, approximate, retentionDays, onSelectAutomation }) => {
  // An empty strip is worse than no strip.
  if (!entries.length) return null;

  const caption = ['Most failed', retentionDays > 0 ? `last ${retentionDays} days` : null, approximate ? 'ranked over the first 1000 failures' : null]
    .filter(Boolean)
    .join(' · ');

  return (
    <Box sx={{ pb: 'var(--ds-space-4)' }} id='execution-dashboard-most-failed'>
      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-1)' }}>{caption}</Typography>
      <Box sx={{ display: 'flex', flexWrap: 'nowrap', gap: 'var(--ds-space-2)', overflowX: 'auto', pb: 'var(--ds-space-1)' }}>
        {entries.map((entry) => {
          const name = entry.workflow_name || entry.workflow_id;
          const truncated = name.length > NAME_MAX_CHARS;
          const chip = (
            <Chip
              size='xs'
              tone='critical'
              count={entry.failure_count}
              highlightCount
              id={`execution-dashboard-most-failed-${entry.workflow_id}`}
              data-testid={`execution-dashboard-most-failed-${entry.workflow_id}`}
              onClick={() => onSelectAutomation(entry.workflow_id)}
            >
              {truncated ? `${name.slice(0, NAME_MAX_CHARS)}…` : name}
            </Chip>
          );
          // Only tooltip what was actually shortened — a tooltip repeating the
          // visible label is noise.
          return truncated ? (
            <Tooltip key={entry.workflow_id} title={name}>
              <span>{chip}</span>
            </Tooltip>
          ) : (
            <React.Fragment key={entry.workflow_id}>{chip}</React.Fragment>
          );
        })}
      </Box>
    </Box>
  );
};

export default MostFailedAutomations;
