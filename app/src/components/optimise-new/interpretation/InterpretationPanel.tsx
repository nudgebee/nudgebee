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
import LightbulbOutlinedIcon from '@mui/icons-material/LightbulbOutlined';
import { ds } from 'src/utils/colors';
import { Label } from '@ui/Label';

export interface InterpretationData {
  /** ① One-line plain-language verdict — what to do. */
  verdict: string;
  /** Optional service/scope chip shown next to the verdict (e.g. "AWSLambda"). */
  serviceName?: string;
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
}

const Slot = ({ label, children }: { label: string; children: ReactNode }) => (
  <Box
    sx={{
      px: ds.space[3],
      py: ds.space[2],
      backgroundColor: 'rgba(255, 255, 255, 0.45)',
      '&:not(:last-of-type)': { borderBottom: `1px solid ${ds.blue[200]}` },
    }}
  >
    <Typography
      sx={{
        fontFamily: 'var(--ds-font-display)',
        fontSize: ds.text.caption,
        fontWeight: ds.weight.semibold,
        letterSpacing: '0.06em',
        textTransform: 'uppercase',
        color: ds.gray[500],
        mb: ds.space[1],
      }}
    >
      {label}
    </Typography>
    {children}
  </Box>
);

const InterpretationPanel = ({ verdict, serviceName, whyNow, impact, impactIsCost, risk, evidence }: InterpretationData) => {
  if (!verdict && !whyNow) return null;
  const hasEvidence = !!evidence && evidence.length > 0;

  return (
    <Box
      data-testid='interpretation-panel'
      sx={{
        borderRadius: ds.radius.lg,
        border: `1px solid ${ds.blue[200]}`,
        backgroundColor: ds.blue[100],
        overflow: 'hidden',
      }}
    >
      {/* Header */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[1],
          px: ds.space[3],
          py: ds.space[2],
          borderBottom: `1px solid ${ds.blue[200]}`,
        }}
      >
        <LightbulbOutlinedIcon sx={{ fontSize: '16px', color: ds.blue[700] }} />
        <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.blue[700] }}>
          What this means
        </Typography>
      </Box>

      {/* ① Verdict */}
      {verdict && (
        <Slot label='Verdict'>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], lineHeight: 1.45 }}>
              {verdict}
            </Typography>
            {serviceName && (
              <Label size='sm' tone='neutral'>
                {serviceName}
              </Label>
            )}
          </Box>
        </Slot>
      )}

      {/* ② Why it matters (+ observed-usage strip) */}
      {(whyNow || hasEvidence) && (
        <Slot label='Why it matters'>
          {whyNow && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: 1.6 }}>{whyNow}</Typography>}
          {hasEvidence && (
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: ds.space[3],
                mt: whyNow ? ds.space[2] : 0,
                px: ds.space[2],
                py: ds.space[1],
                backgroundColor: ds.gray[100],
                border: `1px solid ${ds.gray[200]}`,
                borderRadius: ds.radius.sm,
              }}
            >
              {evidence!.map((e) => (
                <Box key={e.label} sx={{ display: 'flex', gap: ds.space[1], alignItems: 'baseline' }}>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{e.label}</Typography>
                  <Typography sx={{ fontSize: ds.text.caption, fontFamily: ds.font.mono, color: ds.gray[700] }}>{e.value}</Typography>
                </Box>
              ))}
            </Box>
          )}
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
    </Box>
  );
};

export default InterpretationPanel;
