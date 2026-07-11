/**
 * KpiRow — the Overview KPI strip (spec §4a). Each card shows a value (and, where
 * a prior period exists, a delta beside it) so the strip reads as a set. The
 * Total AI cost card is the `hero` and shows the all-in figure (token + prorated
 * cache storage); a dedicated Cache storage card breaks out the storage portion.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Stat, type StatDelta } from '@ui/Stat';
import { Card } from '@ui/Card';
import { fmtCost, fmtDuration, fmtTokens, type KpiTotals } from '../format';

interface KpiRowProps {
  current: KpiTotals;
  previous: KpiTotals;
  /** Cache-lifecycle storage cost (prorated) — token cost lives in `current.totalCost`. */
  storageCost?: number;
}

function pctDelta(now: number, before: number): number {
  if (!before) return 0;
  return Math.round(((now - before) / before) * 100);
}

// Percentage delta, pre-formatted with sign + "%" (so Stat shows "+5% vs prev",
// not a bare "5"), with the arrow direction derived from the sign.
function pctDeltaProps(pct: number, tone: StatDelta['tone']): StatDelta {
  return {
    value: `${pct > 0 ? '+' : ''}${pct}%`,
    period: 'vs prev',
    tone,
    direction: pct > 0 ? 'up' : pct < 0 ? 'down' : 'flat',
  };
}

const cardSx = { flex: '1 1 180px', minWidth: 160 } as const;

export function KpiRow({ current, previous, storageCost = 0 }: KpiRowProps) {
  const costDelta = pctDelta(current.totalCost, previous.totalCost);
  const avgDelta = pctDelta(current.avgCostPerRun, previous.avgCostPerRun);
  const runsDelta = pctDelta(current.runs, previous.runs);
  const latDelta = pctDelta(current.avgLatencyMs, previous.avgLatencyMs);
  // All-in = token cost + prorated cache-storage cost. The hero shows this so
  // the headline isn't under-reporting; the split is spelled out on hover and in
  // the dedicated storage card.
  const allInCost = current.totalCost + storageCost;

  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }} id='cost-kpi-row'>
      {/* Hero figure — the single sanctioned cost headline (all-in). Cost-axis
          tone rides on the delta (DS rule: don't tone the value). */}
      <Card sx={{ ...cardSx, flex: '1 1 240px' }}>
        <Stat
          label='Total AI cost'
          value={fmtCost(allInCost)}
          delta={pctDeltaProps(costDelta, costDelta > 0 ? 'waste' : 'savings')}
          deltaPlacement='inline'
          info={{ tooltip: `Tokens ${fmtCost(current.totalCost)} + cache storage ${fmtCost(storageCost)} (prorated to the window).` }}
        />
      </Card>

      <Card sx={cardSx}>
        <Stat
          label='Cache storage'
          value={fmtCost(storageCost)}
          info={{
            tooltip:
              'Prorated cache-lifecycle storage cost over the window. Scoped by account + model only — not affected by source/user/agent/status filters.',
          }}
        />
      </Card>

      <Card sx={cardSx}>
        <Stat
          label='Avg cost / run'
          value={fmtCost(current.avgCostPerRun)}
          delta={pctDeltaProps(avgDelta, avgDelta > 0 ? 'waste' : 'savings')}
          deltaPlacement='inline'
        />
      </Card>

      <Card sx={cardSx}>
        <Stat label='Runs' value={current.runs} delta={pctDeltaProps(runsDelta, 'neutral')} deltaPlacement='inline' />
      </Card>

      <Card sx={cardSx}>
        <Stat label='Tokens (in/out)' value={`${fmtTokens(current.inputTokens)} / ${fmtTokens(current.outputTokens)}`} />
      </Card>

      <Card sx={cardSx}>
        <Stat
          label='Avg latency / run'
          value={fmtDuration(current.avgLatencyMs)}
          delta={pctDeltaProps(latDelta, latDelta > 0 ? 'waste' : 'savings')}
          deltaPlacement='inline'
          info={{ tooltip: 'Model latency only — excludes human approval wait time.' }}
        />
      </Card>
    </Box>
  );
}

export default KpiRow;
