import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import type { BriefingCallout as BriefingCalloutModel } from './resolveBriefing';

const TONE: Record<BriefingCalloutModel['tone'], { background: string; accent: string; kind: string }> = {
  warning: { background: ds.amber[100], accent: ds.amber[400], kind: ds.amber[700] },
  critical: { background: ds.red[100], accent: ds.red[400], kind: ds.red[700] },
  info: { background: ds.blue[100], accent: ds.blue[400], kind: ds.blue[700] },
};

const BriefingCallout = ({ callout }: { callout: BriefingCalloutModel }) => {
  const tone = TONE[callout.tone];

  return (
    <Box
      sx={{
        padding: 'var(--ds-space-2) var(--ds-space-2)',
        borderRadius: 'var(--ds-radius-sm)',
        borderLeft: `3px solid ${tone.accent}`,
        backgroundColor: tone.background,
      }}
      data-testid={`briefing-callout-${callout.key}`}
    >
      <Typography
        sx={{
          fontSize: '9px',
          fontWeight: ds.weight.semibold,
          color: tone.kind,
          letterSpacing: '0.06em',
          textTransform: 'uppercase',
          lineHeight: 1.6,
        }}
      >
        {callout.kind}
      </Typography>
      {callout.title && (
        <Typography
          sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.brand[600], lineHeight: 1.25, wordBreak: 'break-word' }}
        >
          {callout.title}
        </Typography>
      )}
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], lineHeight: 1.35 }}>{callout.detail}</Typography>
    </Box>
  );
};

export default BriefingCallout;
