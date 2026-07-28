/**
 * InterpretationPanel — the "what to make of this" block rendered at the top of
 * a recommendation's Details tab. It re-frames data the panel already has
 * (catalog title/description + the computed insight + estimated savings) into a
 * fixed set of plain-language slots so a reader knows the verdict, the why, the
 * impact, and the risk without parsing raw evidence.
 *
 * Purely presentational — all slot text is assembled by `buildInterpretation`.
 * Actions (Resolve / Create Ticket / …) deliberately live in the sticky
 * ActionBar, not here, so they aren't duplicated.
 *
 * Built only from DS tokens (`@utils/colors`) + the DS `Label`, matching the
 * existing detail-panel callout (blue-100 surface, blue-200 border, 8px radius).
 */
import type { ReactNode } from 'react';
import { Box, Typography } from '@mui/material';
import HelpOutlineIcon from '@mui/icons-material/HelpOutline';
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import { ds } from 'src/utils/colors';
import { Label, type LabelTone } from '@ui/Label';
import { Link } from '@ui/Link';
import { Card } from '@ui/Card';
import Tooltip from '@ui/Tooltip';

export interface InterpretationData {
  /** ① One-line plain-language verdict — what to do. */
  verdict: string;
  /** Optional service/scope chip shown next to the verdict (e.g. "AWSLambda"). */
  serviceName?: string;
  /** Robustness chip shown next to the verdict (drives "is this a 3-day fluke?"). */
  confidence?: 'High' | 'Medium' | 'Low' | null;
  /** Lookback the call was computed over, e.g. "14 days · 1.25-min step". */
  observedWindow?: string | null;
  /** ② Why it matters — the rationale (catalog description or computed insight). */
  whyNow?: string;
  /** ③ Impact — dollar figure when present, else a reliability/security framing. */
  impact?: string;
  /** When true the impact is a real cost saving and is rendered in the savings tone. */
  impactIsCost?: boolean;
  /** ④ Risk / considerations — rule-family caveat (rollout, irreversibility, …). */
  risk?: string;
  /** Compact observed-usage strip rendered under "why" (e.g. CPU P99 / rec). */
  evidence?: { label: string; value: string }[];
  /** When provided, renders a "View usage trend →" link in the Observed-usage
   *  slot that deep-links into the Evidence tab's time-series chart/table. */
  onViewEvidence?: () => void;
}

const CONFIDENCE_TONE: Record<'High' | 'Medium' | 'Low', LabelTone> = {
  High: 'success',
  Medium: 'warning',
  Low: 'neutral',
};

const Slot = ({ label, action, children }: { label: string; action?: ReactNode; children: ReactNode }) => (
  <Box
    sx={{
      px: ds.space[4],
      py: ds.space[3],
      backgroundColor: 'rgba(255, 255, 255, 0.45)',
      '&:not(:last-of-type)': { borderBottom: `1px solid ${ds.gray[200]}` },
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2], mb: ds.space[1] }}>
      <Typography
        sx={{
          fontFamily: 'var(--ds-font-display)',
          fontSize: ds.text.caption,
          fontWeight: ds.weight.medium,
          textTransform: 'uppercase',
          letterSpacing: '0.04em',
          color: ds.gray[500],
        }}
      >
        {label}
      </Typography>
      {action}
    </Box>
    {children}
  </Box>
);

const ViewTrendLink = ({ onClick }: { onClick: () => void }) => (
  // DS Link styled as the affordance. The Evidence tab is React-state controlled
  // (no route), so the hash href is a no-op we preventDefault on, then switch tabs.
  <Link
    href='#evidence'
    secondaryText
    onClick={(e) => {
      e.preventDefault();
      onClick();
    }}
    style={{ whiteSpace: 'nowrap' }}
  >
    <Box component='span' data-testid='view-usage-trend' sx={{ display: 'inline-flex', alignItems: 'center', gap: '2px' }}>
      <TrendingUpIcon sx={{ fontSize: '14px' }} />
      View usage trend
    </Box>
  </Link>
);

const InterpretationPanel = ({
  verdict,
  serviceName,
  confidence,
  observedWindow,
  whyNow,
  impact,
  impactIsCost,
  risk,
  evidence,
  onViewEvidence,
}: InterpretationData) => {
  if (!verdict && !whyNow) return null;
  const hasEvidence = !!evidence && evidence.length > 0;

  return (
    <Card variant='outlined' tone='neutral' elevation='flat' data-testid='interpretation-panel' sx={{ p: 0, overflow: 'hidden' }}>
      {/* ① Verdict */}
      {verdict && (
        <Slot label='Verdict'>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], lineHeight: 1.45 }}>{verdict}</Typography>
            {whyNow && (
              <Tooltip
                title={<Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>{whyNow}</Typography>}
                tooltipStyle={{ maxWidth: '320px' }}
              >
                <HelpOutlineIcon
                  aria-label='Why it matters'
                  sx={{ fontSize: '16px', color: ds.gray[400], cursor: 'help', '&:hover': { color: ds.blue[600] } }}
                />
              </Tooltip>
            )}
            {serviceName && (
              <Label size='sm' tone='neutral'>
                {serviceName}
              </Label>
            )}
            {confidence && (
              <Label size='sm' tone={CONFIDENCE_TONE[confidence]}>
                {confidence} confidence
              </Label>
            )}
          </Box>
          {observedWindow && (
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mt: ds.space[1] }}>Computed over {observedWindow}</Typography>
          )}
        </Slot>
      )}

      {/* ② Observed-usage strip — the "why" prose now lives in the verdict help tooltip */}
      {hasEvidence && (
        <Slot label='Observed usage' action={onViewEvidence ? <ViewTrendLink onClick={onViewEvidence} /> : undefined}>
          <Box
            sx={{
              display: 'grid',
              // Equal-width columns so the metrics distribute evenly at any count
              // (1, 2, or 3) — space-between would strand 2 items at the far edges.
              gridTemplateColumns: `repeat(${evidence!.length}, minmax(0, 1fr))`,
              gap: ds.space[3],
              px: ds.space[3],
              py: ds.space[2],
              backgroundColor: ds.gray[100],
              border: `1px solid ${ds.gray[200]}`,
              borderRadius: ds.radius.sm,
            }}
          >
            {evidence!.map((e) => (
              <Box key={e.label} sx={{ display: 'flex', gap: ds.space[1], alignItems: 'baseline', minWidth: 0 }}>
                <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], whiteSpace: 'nowrap' }}>{e.label}</Typography>
                <Typography sx={{ fontSize: ds.text.caption, fontFamily: ds.font.mono, color: ds.gray[700] }}>{e.value}</Typography>
              </Box>
            ))}
          </Box>
        </Slot>
      )}

      {/* ③ Impact */}
      {impact && (
        <Slot label='Impact'>
          <Typography
            sx={{
              fontSize: ds.text.small,
              color: impactIsCost ? ds.green[600] : ds.gray[700],
              fontWeight: impactIsCost ? ds.weight.semibold : ds.weight.regular,
              lineHeight: 1.6,
            }}
          >
            {impact}
          </Typography>
        </Slot>
      )}

      {/* ④ Risk & considerations */}
      {risk && (
        <Slot label='Risk & considerations'>
          <Typography sx={{ fontSize: ds.text.small, color: ds.amber[700], lineHeight: 1.6 }}>{risk}</Typography>
        </Slot>
      )}
    </Card>
  );
};

export default InterpretationPanel;
