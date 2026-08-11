import { Box, Typography } from '@mui/material';
import RemoveIcon from '@mui/icons-material/Remove';
import AddIcon from '@mui/icons-material/Add';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import { ds } from 'src/utils/colors';
import { formatCount } from './format';
import type { BriefingModel } from './resolveBriefing';

interface Props {
  model: BriefingModel;
  expanded: boolean;
  onToggle: () => void;
  assistantName: string;
  iconUrl?: string;
}

const funnelNumberSx = {
  fontSize: ds.text.body,
  fontWeight: ds.weight.semibold,
  color: ds.brand[600],
  fontVariantNumeric: 'tabular-nums' as const,
};

const funnelWordSx = { fontSize: ds.text.small, color: ds.gray[600] };

const arrow = (
  <Typography component='span' sx={{ fontSize: ds.text.small, color: ds.gray[400], mx: 'var(--ds-space-1)' }}>
    →
  </Typography>
);

const BriefingHeader = ({ model, expanded, onToggle, assistantName, iconUrl }: Props) => {
  const { ingested, issues, aboveP3, p1, p2 } = model.header;
  const displayName = assistantName.charAt(0).toUpperCase() + assistantName.slice(1);

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--ds-space-3)',
        flexWrap: 'wrap',
        padding: 'var(--ds-space-3) var(--ds-space-4)',
        borderBottom: `1px solid ${ds.gray[200]}`,
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', minWidth: 0 }}>
        {iconUrl && <Box component='img' src={iconUrl} alt='' aria-hidden sx={{ width: 18, height: 18, flexShrink: 0, objectFit: 'contain' }} />}
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.brand[600], whiteSpace: 'nowrap' }}>
          {displayName}&rsquo;s briefing
        </Typography>

        <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
          <Typography sx={funnelNumberSx}>{formatCount(ingested)}</Typography>
          <Typography sx={funnelWordSx}>in</Typography>
          {arrow}
          <Typography sx={funnelNumberSx}>{formatCount(issues)}</Typography>
          <Typography sx={funnelWordSx}>issues</Typography>
          {arrow}
          <Typography sx={funnelNumberSx}>{formatCount(aboveP3)}</Typography>
          <Typography sx={funnelWordSx}>ranked above P3</Typography>
        </Box>

        {p1 > 0 && (
          <Chip size='xs' tone='critical' dot data-testid='briefing-chip-p1'>
            {formatCount(p1)} P1
          </Chip>
        )}
        {p2 > 0 && (
          <Chip size='xs' tone='warning' dot data-testid='briefing-chip-p2'>
            {formatCount(p2)} P2
          </Chip>
        )}
      </Box>

      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
        <Button
          tone='secondary'
          size='xs'
          composition='icon-only'
          icon={expanded ? <RemoveIcon fontSize='small' /> : <AddIcon fontSize='small' />}
          onClick={onToggle}
          aria-expanded={expanded}
          aria-label={expanded ? 'Collapse briefing' : 'Expand briefing'}
          data-testid='briefing-collapse-toggle'
        />
      </Box>
    </Box>
  );
};

export default BriefingHeader;
