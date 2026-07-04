/**
 * BreakdownWidgets — Overview breakdown row (spec §4c).
 *   - Cost by model: pastel donut + ranked list with call count, share and cost.
 *   - Cost by trigger type / assistant: ranked pastel bars.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Card } from '@ui/Card';
import { Button } from '@ui/Button';
import Chart from '@ui/Chart';
import { seriesColor } from './palette';
import { fmtCost, fmtPct } from '../format';
import type { RankedSlice } from '../types';

export interface ModelSlice {
  key: string;
  cost: number;
  calls: number;
  tokens: number;
  share: number;
}

interface BreakdownWidgetsProps {
  byModel: ModelSlice[];
  /** Cost share by backend `source` (the trigger-type proxy). */
  bySource: RankedSlice[];
  /** Click a model (donut slice or list row) to filter the report to it. */
  onSelectModel?: (model: string) => void;
  /** Click a source bar to filter the report to it. */
  onSelectSource?: (source: string) => void;
}

// Title rendered through Card's `header` slot — Card owns the spacing + divider,
// so this only carries the title typography.
const cardTitleSx = {
  fontSize: 'var(--ds-text-small)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  color: 'var(--ds-gray-700)',
  fontFamily: 'var(--ds-font-display)',
} as const;

// Collapse long lists to the top few; the rest is one tap away.
const RANKED_VISIBLE = 4;

function RankedBars({ slices, onSelect }: { slices: RankedSlice[]; onSelect?: (key: string) => void }) {
  const [expanded, setExpanded] = React.useState(false);
  const max = Math.max(...slices.map((s) => s.cost), 0.000001);
  const visible = expanded ? slices : slices.slice(0, RANKED_VISIBLE);
  const hidden = slices.length - RANKED_VISIBLE;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      {visible.map((s, i) => (
        <Box
          key={s.key}
          role={onSelect ? 'button' : undefined}
          tabIndex={onSelect ? 0 : undefined}
          onClick={onSelect ? () => onSelect(s.key) : undefined}
          onKeyDown={
            onSelect
              ? (e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    onSelect(s.key);
                  }
                }
              : undefined
          }
          sx={onSelect ? { cursor: 'pointer', '&:hover .ranked-fill': { filter: 'brightness(0.92)' } } : undefined}
        >
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-700)',
              mb: 'var(--ds-space-1)',
            }}
          >
            <Box component='span' sx={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '60%' }}>
              {s.key}
            </Box>
            <Box component='span' sx={{ fontVariantNumeric: 'tabular-nums', color: 'var(--ds-gray-600)' }}>
              {fmtCost(s.cost)} · {fmtPct(s.share)}
            </Box>
          </Box>
          <Box sx={{ height: 8, borderRadius: 'var(--ds-radius-pill)', backgroundColor: 'var(--ds-background-300)', overflow: 'hidden' }}>
            <Box
              className='ranked-fill'
              sx={{
                width: `${(s.cost / max) * 100}%`,
                height: '100%',
                backgroundColor: seriesColor(s.key, i),
                borderRadius: 'var(--ds-radius-pill)',
              }}
            />
          </Box>
        </Box>
      ))}
      {hidden > 0 && (
        <Box sx={{ display: 'flex' }}>
          <Button tone='link' size='sm' onClick={() => setExpanded((v) => !v)} id='cost-by-source-show-all'>
            {expanded ? 'Show less' : `Show all ${slices.length}`}
          </Button>
        </Box>
      )}
    </Box>
  );
}

export function BreakdownWidgets({ byModel, bySource, onSelectModel, onSelectSource }: BreakdownWidgetsProps) {
  const donutValues = byModel.map((s) => Number(s.cost.toFixed(2)));
  const donutLabels = byModel.map((s) => s.key);
  const donutColors = byModel.map((s, i) => seriesColor(s.key, i));
  const modelTotal = byModel.reduce((sum, s) => sum + s.cost, 0);

  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }} id='cost-breakdowns'>
      {/* Cost by model — donut + ranked list (model · count · share · cost) */}
      <Card header={<Box sx={cardTitleSx}>Cost by model</Box>} sx={{ flex: '1 1 360px', minWidth: 320 }}>
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-6)', alignItems: 'center' }}>
          <Chart.Doughnut
            values={donutValues}
            labels={donutLabels}
            colors={donutColors}
            size={132}
            cutout='66%'
            formatValue={(raw: number) => fmtCost(raw)}
            centerLabel='Total'
            centerValue={fmtCost(modelTotal)}
            externalTooltip
            onItemClick={onSelectModel}
          />
          <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--ds-space-2)',
                fontSize: '10px',
                letterSpacing: '0.04em',
                textTransform: 'uppercase',
                color: 'var(--ds-gray-500)',
              }}
            >
              <Box sx={{ width: 10 }} />
              <Box sx={{ flex: 1 }}>Model</Box>
              <Box sx={{ width: 44, textAlign: 'right' }}>Count</Box>
              <Box sx={{ width: 40, textAlign: 'right' }}>Share</Box>
              <Box sx={{ width: 56, textAlign: 'right' }}>Cost</Box>
            </Box>
            {byModel.map((s, i) => (
              <Box
                key={s.key}
                onClick={onSelectModel ? () => onSelectModel(s.key) : undefined}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--ds-space-2)',
                  fontSize: 'var(--ds-text-caption)',
                  ...(onSelectModel
                    ? { cursor: 'pointer', borderRadius: 'var(--ds-radius-sm)', '&:hover': { backgroundColor: 'var(--ds-background-200)' } }
                    : {}),
                }}
              >
                <Box sx={{ width: 10, height: 10, borderRadius: 2, backgroundColor: seriesColor(s.key, i), flexShrink: 0 }} />
                <Box
                  component='span'
                  sx={{ flex: 1, color: 'var(--ds-gray-700)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                >
                  {s.key}
                </Box>
                <Box component='span' sx={{ width: 44, textAlign: 'right', color: 'var(--ds-gray-600)', fontVariantNumeric: 'tabular-nums' }}>
                  {s.calls}
                </Box>
                <Box component='span' sx={{ width: 40, textAlign: 'right', color: 'var(--ds-gray-600)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtPct(s.share)}
                </Box>
                <Box component='span' sx={{ width: 56, textAlign: 'right', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtCost(s.cost)}
                </Box>
              </Box>
            ))}
          </Box>
        </Box>
      </Card>

      <Card header={<Box sx={cardTitleSx}>Cost by source</Box>} sx={{ flex: '1 1 240px', minWidth: 220 }}>
        <RankedBars slices={bySource} onSelect={onSelectSource} />
      </Card>
    </Box>
  );
}

export default BreakdownWidgets;
