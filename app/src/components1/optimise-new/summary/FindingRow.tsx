import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import type { InsightItem } from './insights';
import SeverityBadge from '../SeverityBadge';

interface FindingRowProps {
  item: InsightItem;
  onClickResource: (id: string) => void;
}

const FindingRow = ({ item, onClickResource }: FindingRowProps) => (
  <Box
    onClick={() => onClickResource(item.id)}
    data-testid={`finding-row-${item.id}`}
    sx={{
      display: 'flex',
      alignItems: 'center',
      gap: '8px',
      px: '12px',
      py: '5px',
      minHeight: '34px',
      borderBottom: `1px solid ${colors.border.secondaryLightest}`,
      cursor: 'pointer',
      '&:hover': { backgroundColor: colors.background.tertiaryLightestestest },
    }}
  >
    <Box sx={{ width: '52px', flexShrink: 0 }}>
      <SeverityBadge severity={(item.severity.charAt(0).toUpperCase() + item.severity.slice(1)) as any} size='small' />
    </Box>

    <Typography
      className='nb-text-body-compact'
      sx={{ flex: 1, fontWeight: 500, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
    >
      {item.summary}
    </Typography>

    <Typography
      sx={{
        width: '130px',
        flexShrink: 0,
        fontSize: '10.5px',
        fontFamily: 'monospace',
        color: colors.text.tertiary,
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
      }}
    >
      {item.resourceId}
    </Typography>

    <Typography sx={{ width: '75px', flexShrink: 0, fontSize: '10.5px', color: colors.text.tertiary }}>{item.provider.toUpperCase()}</Typography>

    <Box sx={{ width: '80px', flexShrink: 0, textAlign: 'right' }}>
      <Typography sx={{ fontSize: '11.5px', fontWeight: 700, color: colors.text.secondary, lineHeight: 1.1 }}>{item.impactValue}</Typography>
      <Typography sx={{ fontSize: '9px', color: colors.text.quaternary, lineHeight: 1.1 }}>{item.impactLabel}</Typography>
    </Box>

    <Box sx={{ width: '110px', flexShrink: 0, textAlign: 'right' }} onClick={(e: React.MouseEvent) => e.stopPropagation()}>
      <Typography
        component='a'
        onClick={(e: React.MouseEvent) => {
          e.preventDefault();
          onClickResource(item.id);
        }}
        sx={{
          fontSize: '11px',
          fontWeight: 500,
          color: colors.text.primary,
          whiteSpace: 'nowrap',
          cursor: 'pointer',
          textDecoration: 'none',
          borderBottom: `1px dashed ${colors.text.primary}`,
          pb: '1px',
          '&:hover': { opacity: 0.75 },
        }}
      >
        {item.nextStep.label}
      </Typography>
    </Box>
  </Box>
);

export default FindingRow;
