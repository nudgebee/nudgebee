import React from 'react';
import { Box } from '@mui/material';
import { Card } from '@ui/Card';
import { Stat } from '@ui/Stat';
import { Skeleton } from '@ui/Skeleton';
import type { ExecutionAggregateResponse } from '@api1/workflow/types';

interface ExecutionSummaryCardsProps {
  aggregate: ExecutionAggregateResponse | null;
  loading: boolean;
  /** Temporal namespace retention, which is the window these counts cover. */
  retentionDays: number;
}

/**
 * The headline counts — a fixed, glanceable header.
 *
 * These are deliberately static: they come from one unfiltered call on page
 * load and do not move when the table below is filtered. The caption on each
 * card is what stops that reading as a bug, so don't drop it.
 *
 * Counts carry a "≈" prefix because Temporal documents its count API as
 * approximate; a bare number would overstate what we know.
 */
const ExecutionSummaryCards: React.FC<ExecutionSummaryCardsProps> = ({ aggregate, loading, retentionDays }) => {
  const approximate = aggregate?.counts_are_approximate;
  const formatCount = (count?: number) => {
    if (loading || !aggregate) return '—';
    return `${approximate ? '≈ ' : ''}${count ?? 0}`;
  };

  // Zero means the retention could not be read; say nothing rather than guess.
  const windowLabel = retentionDays > 0 ? `last ${retentionDays} days` : undefined;

  const cards = [
    { key: 'total', label: 'Total executions', value: aggregate?.total },
    { key: 'succeeded', label: 'Successful', value: aggregate?.succeeded },
    { key: 'failed', label: 'Failed', value: aggregate?.failed },
  ];

  return (
    <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', padding: `var(--ds-space-4) 0` }}>
      {cards.map((card) => (
        <Card
          key={card.key}
          variant='outlined'
          size='sm'
          id={`execution-dashboard-stat-${card.key}`}
          data-testid={`execution-dashboard-stat-${card.key}`}
          sx={{ flex: 1, minWidth: 0 }}
        >
          {loading && !aggregate ? (
            <Skeleton shape='rect' width='100%' height={48} />
          ) : (
            <Stat
              size='md'
              label={card.label}
              value={formatCount(card.value)}
              sub={windowLabel}
              info={
                card.key === 'failed'
                  ? { tooltip: 'Counts runs that failed, were terminated, or timed out. Cancelled runs are excluded.' }
                  : undefined
              }
            />
          )}
        </Card>
      ))}
    </Box>
  );
};

export default ExecutionSummaryCards;
