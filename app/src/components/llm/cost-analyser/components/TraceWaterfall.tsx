/**
 * TraceWaterfall — Gantt-style per-conversation trace (spec §6b).
 *
 * One row per sub-task; bar length = duration; colour segments split
 * model-latency vs tool-time vs (hatched) wait-time so approval delays are
 * obvious. Each bar is annotated with step cost and a cost-heat shade so the
 * expensive steps pop. Pure Box + DS tokens — no charting dependency.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { fmtCost, fmtDuration } from '../format';
import type { Run, Step } from '../types';

interface TraceWaterfallProps {
  run: Run;
}

const LABEL_W = 168;

// Hatched fill for human wait-time gaps (approval gates).
const WAIT_FILL = 'repeating-linear-gradient(45deg, var(--ds-amber-200), var(--ds-amber-200) 4px, var(--ds-amber-100) 4px, var(--ds-amber-100) 8px)';

function heatColor(ratio: number): string {
  // 0 → soft blue, 1 → red. Used as the bar's base latency colour.
  if (ratio > 0.66) return 'var(--ds-red-400)';
  if (ratio > 0.33) return 'var(--ds-amber-400)';
  return 'var(--ds-blue-400)';
}

function Segment({ widthPct, fill, title }: { widthPct: number; fill: string; title: string }) {
  if (widthPct <= 0) return null;
  return (
    <Tooltip title={title} placement='top'>
      <Box sx={{ width: `${widthPct}%`, height: '100%', background: fill, flexShrink: 0 }} />
    </Tooltip>
  );
}

export function TraceWaterfall({ run }: TraceWaterfallProps) {
  const runStart = Date.parse(run.startedAt);
  const total = Math.max(run.wallClockMs, 1);
  const maxStepCost = Math.max(...run.steps.map((s) => s.stepCost), 0.000001);

  return (
    <Box id='cost-trace-waterfall' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      {/* Legend */}
      <Box
        sx={{
          display: 'flex',
          gap: 'var(--ds-space-4)',
          flexWrap: 'wrap',
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-600)',
          mb: 'var(--ds-space-1)',
        }}
      >
        <LegendDot fill='var(--ds-blue-400)' label='Model latency' />
        <LegendDot fill='var(--ds-gray-300)' label='Tool / execution' />
        <LegendDot fill={WAIT_FILL} label='Wait (approval)' />
      </Box>

      {run.steps.map((step: Step) => {
        const offset = ((Date.parse(step.startedAt) - runStart) / total) * 100;
        const stepDuration = Date.parse(step.endedAt) - Date.parse(step.startedAt) || 1;
        const barPct = (stepDuration / total) * 100;
        const latPct = (step.stepLatencyMs / stepDuration) * 100;
        const toolPct = (step.toolTimeMs / stepDuration) * 100;
        const waitPct = (step.waitTimeMs / stepDuration) * 100;
        const heat = step.stepCost / maxStepCost;

        return (
          <Box key={step.stepId} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
            <Box
              sx={{
                width: LABEL_W,
                flexShrink: 0,
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-700)',
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
              }}
              title={step.agent}
            >
              {step.sequence}. {step.agent}
            </Box>
            <Box sx={{ position: 'relative', flex: 1, height: 20, backgroundColor: 'var(--ds-background-300)', borderRadius: 'var(--ds-radius-sm)' }}>
              <Box
                sx={{
                  position: 'absolute',
                  left: `${offset}%`,
                  width: `${Math.max(barPct, 0.8)}%`,
                  height: '100%',
                  display: 'flex',
                  borderRadius: 'var(--ds-radius-sm)',
                  overflow: 'hidden',
                  border: `1px solid ${heatColor(heat)}`,
                }}
              >
                <Segment widthPct={latPct} fill={heatColor(heat)} title={`Model latency · ${fmtDuration(step.stepLatencyMs)}`} />
                <Segment widthPct={toolPct} fill='var(--ds-gray-300)' title={`Tool time · ${fmtDuration(step.toolTimeMs)}`} />
                <Segment widthPct={waitPct} fill={WAIT_FILL} title={`Wait · ${fmtDuration(step.waitTimeMs)}`} />
              </Box>
            </Box>
            <Box
              sx={{
                width: 64,
                flexShrink: 0,
                textAlign: 'right',
                fontSize: 'var(--ds-text-caption)',
                fontVariantNumeric: 'tabular-nums',
                color: heat > 0.66 ? 'var(--ds-red-700)' : 'var(--ds-gray-700)',
                fontWeight: heat > 0.66 ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-regular)',
              }}
            >
              {fmtCost(step.stepCost)}
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}

function LegendDot({ fill, label }: { fill: string; label: string }) {
  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ width: 10, height: 10, borderRadius: 2, background: fill, flexShrink: 0 }} />
      {label}
    </Box>
  );
}

export default TraceWaterfall;
