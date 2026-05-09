import { Box, Typography, IconButton } from '@mui/material';
import HexagonOutlinedIcon from '@mui/icons-material/HexagonOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import { cloneElement, isValidElement, useState, type ReactElement } from 'react';
import { colors } from 'src/utils/colors';
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import CustomBorderCard from '@common/CustomBorderCard';
import CloudProviderIcon from '@common/CloudProviderIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import CustomTooltip from '@components1/common/CustomTooltip';
import { formatAge, type InsightItem } from './insights';

// Wraps a child in CustomTooltip only when the child's text is visually truncated.
// Measures on mouseEnter and uses controlled `open` — we can't rely on a ref because
// CustomTooltip forcibly overrides refs set via cloneElement.
const TruncatedTooltip = ({
  title,
  children,
  placement = 'top',
}: {
  title: string;
  children: ReactElement;
  placement?: 'top' | 'bottom' | 'left' | 'right';
}) => {
  const [open, setOpen] = useState(false);

  const handleEnter = (e: React.MouseEvent<HTMLElement>) => {
    const el = e.currentTarget;
    if (el.scrollWidth > el.clientWidth + 1) setOpen(true);
  };
  const handleLeave = () => setOpen(false);

  const childProps = (children.props as Record<string, any>) || {};
  const child = isValidElement(children)
    ? cloneElement(children as ReactElement<any>, {
        onMouseEnter: (e: React.MouseEvent<HTMLElement>) => {
          handleEnter(e);
          childProps.onMouseEnter?.(e);
        },
        onMouseLeave: (e: React.MouseEvent<HTMLElement>) => {
          handleLeave();
          childProps.onMouseLeave?.(e);
        },
      })
    : children;

  return (
    <CustomTooltip title={title} placement={placement} open={open} onClose={handleLeave}>
      {child}
    </CustomTooltip>
  );
};

// Subtle middle-dot separator between metadata groups.
const Dot = () => (
  <Box component='span' sx={{ color: colors.text.quaternary, fontSize: '10px', lineHeight: 1, flexShrink: 0, userSelect: 'none' }}>
    •
  </Box>
);

// Label-style chip that supports a leading icon (CustomLabels itself only takes text).
const iconLabelSx = {
  display: 'flex',
  alignItems: 'center',
  gap: '4px',
  height: '16px',
  px: '6px',
  borderRadius: '4px',
  backgroundColor: colors.background.tertiaryLight,
  color: colors.text.tertiary,
  flexShrink: 0,
};
const iconLabelTextSx = {
  fontFamily: 'Roboto',
  fontSize: '10px',
  fontWeight: 500,
  color: colors.text.tertiary,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
};

// Severity → (short letter, CustomLabels variant). We pass an explicit variant
// because CustomLabels' text-based color lookup won't match a single letter.
// Critical uses an inline override one step darker than High on the red ramp
// (background.error / text.white) so the two read at distinctly different weights.
// CustomLabels applies the variant background to BOTH the outer Box and the
// inner Typography, so we override both to avoid a stale lighter pill behind
// the glyph.
const severityToChip = (sev: string): { letter: string; variant: string; sx?: Record<string, unknown> } => {
  switch ((sev || '').toLowerCase()) {
    case 'critical':
      return {
        letter: 'C',
        variant: 'criticalRed',
        sx: {
          background: colors.editIcon,
          color: colors.text.white,
          '& .MuiTypography-root': { background: colors.editIcon, color: colors.text.white },
        },
      };
    case 'high':
      return { letter: 'H', variant: 'red' };
    case 'medium':
      return { letter: 'M', variant: 'yellow' };
    case 'low':
      return { letter: 'L', variant: 'blue' };
    default:
      return { letter: (sev || '?').charAt(0).toUpperCase(), variant: 'grey' };
  }
};

// ─── Component ─────────────────────────────────────────────────────────────

interface InsightCardProps {
  item: InsightItem;
  onClickResource: (id: string) => void;
  onAskNubi?: (item: InsightItem) => void;
}

const InsightCard = ({ item, onClickResource, onAskNubi }: InsightCardProps) => {
  return (
    <CustomBorderCard
      borderLeftColor='transparent'
      borderLeftWidth='0px'
      borderColor={colors.border.secondaryLight}
      padding='6px 10px'
      onClick={() => onClickResource(item.id)}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: '8px',
        minHeight: '46px',
        p: '10px 16px',
        borderRadius: 0,
      }}
    >
      {/* ── Left: summary + metadata ── */}
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <TruncatedTooltip title={item.summary} placement='top'>
          <Typography
            className='nb-text-body-compact'
            sx={{ fontSize: '12px', fontWeight: 400, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', paddingBottom: '4px' }}
          >
            {item.summary}
          </Typography>
        </TruncatedTooltip>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '5px', mt: '1px', flexWrap: 'wrap' }}>
          {item.severity &&
            (() => {
              const chip = severityToChip(item.severity);
              const overrideTypo = (chip.sx as any)?.['& .MuiTypography-root'] || {};
              const { ['& .MuiTypography-root']: _ignored, ...overrideRest } = (chip.sx as any) || {};
              return (
                <CustomLabels
                  text={chip.letter}
                  variant={chip.variant}
                  height='14px'
                  customLabelStyle={{
                    minWidth: '16px',
                    px: '4px',
                    ...overrideRest,
                    '& .MuiTypography-root': { fontSize: '10px', fontWeight: 700, ...overrideTypo },
                  }}
                />
              );
            })()}
          {item.resourceId && (
            <Box sx={{ ...iconLabelSx, maxWidth: '220px' }}>
              <HexagonOutlinedIcon sx={{ fontSize: '12px', color: colors.text.quaternary }} />
              <TruncatedTooltip title={item.resourceId} placement='top'>
                <Typography sx={{ ...iconLabelTextSx, fontFamily: 'monospace' }}>{item.resourceId}</Typography>
              </TruncatedTooltip>
            </Box>
          )}
          {item.accountName && (
            <>
              <Dot />
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px', flexShrink: 0, maxWidth: '220px' }}>
                <Typography sx={{ fontSize: '10.5px', color: colors.text.tertiary, flexShrink: 0 }}>acc:</Typography>
                <CloudProviderIcon cloud_provider={item.provider} width='12px' height='12px' />
                <TruncatedTooltip title={item.accountName} placement='top'>
                  <Typography
                    sx={{
                      fontSize: '10px',
                      fontWeight: 400,
                      color: colors.text.secondary,
                      whiteSpace: 'nowrap',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                    }}
                  >
                    {item.accountName}
                  </Typography>
                </TruncatedTooltip>
              </Box>
            </>
          )}

          <Dot />
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px', flexShrink: 0 }}>
            <AccessTimeOutlinedIcon sx={{ fontSize: '11px', color: colors.text.quaternary }} />
            <Typography sx={{ fontSize: '9.5px', color: colors.text.quaternary }}>{formatAge(item.ageDays)} ago</Typography>
          </Box>
        </Box>
      </Box>

      {/* ── Right: impact value + label, action link + Nubi ── */}
      <Box sx={{ textAlign: 'right', minWidth: '90px', flexShrink: 0 }}>
        {item.impactValue && (
          <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'flex-end', gap: '3px' }}>
            <Typography sx={{ fontSize: '11px', fontWeight: 500, color: colors.text.secondary, whiteSpace: 'nowrap' }}>{item.impactValue}</Typography>
            <Typography sx={{ fontSize: '9px', color: colors.text.quaternary }}>{item.impactLabel}</Typography>
          </Box>
        )}
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: '4px' }}>
          {onAskNubi && (
            <CustomTooltip title='Ask Nubi' placement='top'>
              <IconButton
                size='small'
                data-testid={`ask-nubi-${item.id}`}
                onClick={(e) => {
                  e.stopPropagation();
                  onAskNubi(item);
                }}
                sx={{ p: '2px' }}
              >
                <Box component='img' src={getNubiIconUrl()} sx={{ width: '16px', height: '16px' }} />
              </IconButton>
            </CustomTooltip>
          )}
          <Typography
            component='a'
            onClick={(e: React.MouseEvent) => {
              e.stopPropagation();
              e.preventDefault();
              onClickResource(item.id);
            }}
            sx={{ fontSize: '10.5px', color: colors.text.primary, cursor: 'pointer', '&:hover': { textDecoration: 'underline' } }}
          >
            {item.nextStep.label} &rarr;
          </Typography>
        </Box>
      </Box>
    </CustomBorderCard>
  );
};

export default InsightCard;
