import { Box, Typography } from '@mui/material';
import HexagonOutlinedIcon from '@mui/icons-material/HexagonOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import { cloneElement, isValidElement, useState, type ReactElement } from 'react';
import { ds } from 'src/utils/colors';
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import CustomBorderCard from '@components1/ds/CustomBorderCard';
import CloudProviderIcon from '@common/CloudProviderIcon';
import CustomTooltip from '@components1/ds/Tooltip';
import { Label } from '@components1/ds/Label';
import { Chip } from '@components1/ds/Chip';
import { Button } from '@components1/ds/Button';
import { formatAge, type InsightItem } from './insights';

// Wraps a child in Tooltip only when the child's text is visually truncated.
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
  <Box component='span' sx={{ color: ds.gray[500], fontSize: ds.text.caption, lineHeight: 1, flexShrink: 0, userSelect: 'none' }}>
    •
  </Box>
);

// Severity → DS primitive. All severities use the tonal Chip / Label pastel palette
// for visual consistency; saturation is conveyed by tone (critical / warning / info /
// neutral), not by switching to a solid fill that would overpower neighbouring rows.
const SeverityBadge = ({ severity }: { severity: string }) => {
  const s = (severity || '').toLowerCase();
  if (s === 'critical') {
    return (
      <Chip size='xs' tone='critical' aria-label='Critical severity'>
        C
      </Chip>
    );
  }
  if (s === 'high') {
    return (
      <Label size='sm' tone='critical'>
        H
      </Label>
    );
  }
  if (s === 'medium') {
    return (
      <Label size='sm' tone='warning'>
        M
      </Label>
    );
  }
  if (s === 'low') {
    return (
      <Label size='sm' tone='info'>
        L
      </Label>
    );
  }
  return (
    <Label size='sm' tone='neutral'>
      {(severity || '?').charAt(0).toUpperCase()}
    </Label>
  );
};

// Inline mono-style metadata pill (used for resource id + cell context).
const metaPillSx = {
  display: 'flex',
  alignItems: 'center',
  gap: ds.space[1],
  height: '16px',
  px: ds.space[2],
  borderRadius: ds.radius.sm,
  backgroundColor: ds.gray[100],
  color: ds.gray[600],
  flexShrink: 0,
};

const metaPillTextSx = {
  fontSize: ds.text.caption,
  fontWeight: ds.weight.medium,
  color: ds.gray[600],
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
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
      borderColor={ds.gray[200]}
      padding={`${ds.space[2]} ${ds.space[3]}`}
      onClick={() => onClickResource(item.id)}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space[2],
        minHeight: '46px',
        p: `${ds.space[3]} ${ds.space[4]}`,
        borderRadius: 0,
      }}
    >
      {/* ── Left: summary + metadata ── */}
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <TruncatedTooltip title={item.summary} placement='top'>
          <Typography
            sx={{
              fontSize: ds.text.small,
              fontWeight: ds.weight.regular,
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              paddingBottom: ds.space[1],
              color: ds.gray[700],
            }}
          >
            {item.summary}
          </Typography>
        </TruncatedTooltip>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], mt: '1px', flexWrap: 'wrap' }}>
          {item.severity && <SeverityBadge severity={item.severity} />}
          {item.resourceId && (
            <Box sx={{ ...metaPillSx, maxWidth: '220px' }}>
              <HexagonOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.gray[500] }} />
              <TruncatedTooltip title={item.resourceId} placement='top'>
                <Typography sx={{ ...metaPillTextSx, fontFamily: ds.font.mono }}>{item.resourceId}</Typography>
              </TruncatedTooltip>
            </Box>
          )}
          {item.accountName && (
            <>
              <Dot />
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexShrink: 0, maxWidth: '220px' }}>
                <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], flexShrink: 0 }}>acc:</Typography>
                <CloudProviderIcon cloud_provider={item.provider} width='12px' height='12px' />
                <TruncatedTooltip title={item.accountName} placement='top'>
                  <Typography
                    sx={{
                      fontSize: ds.text.caption,
                      fontWeight: ds.weight.regular,
                      color: ds.gray[700],
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
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexShrink: 0 }}>
            <AccessTimeOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.gray[500] }} />
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{formatAge(item.ageDays)} ago</Typography>
          </Box>
        </Box>
      </Box>

      {/* ── Right: impact value + label, action link + Nubi ── */}
      <Box sx={{ textAlign: 'right', minWidth: '90px', flexShrink: 0 }}>
        {item.impactValue && (
          <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'flex-end', gap: ds.space[1] }}>
            <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], whiteSpace: 'nowrap' }}>
              {item.impactValue}
            </Typography>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{item.impactLabel}</Typography>
          </Box>
        )}
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: ds.space[1] }}>
          {onAskNubi && (
            <CustomTooltip title='Ask Nubi' placement='top'>
              <Button
                tone='ghost'
                size='xs'
                composition='icon-only'
                aria-label='Ask Nubi'
                id={`ask-nubi-${item.id}`}
                icon={<Box component='img' src={getNubiIconUrl()} sx={{ width: '16px', height: '16px' }} />}
                onClick={(e) => {
                  e.stopPropagation();
                  onAskNubi(item);
                }}
              />
            </CustomTooltip>
          )}
          <Button
            tone='link'
            size='xs'
            onClick={(e) => {
              e.stopPropagation();
              onClickResource(item.id);
            }}
          >
            {item.nextStep.label} →
          </Button>
        </Box>
      </Box>
    </CustomBorderCard>
  );
};

export default InsightCard;
