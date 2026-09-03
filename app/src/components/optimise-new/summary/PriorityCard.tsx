import { Box, Typography } from '@mui/material';
import HexagonOutlinedIcon from '@mui/icons-material/HexagonOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import { ds } from 'src/utils/colors';
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudProviderIcon';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import { SeverityIcon, type SeverityLevel } from '@ui/SeverityIcon';
import { resourceAnchorName, truncateMiddle } from './ResourceLabel';
import { formatAge, secondaryLine, type InsightItem } from './insights';

/**
 * A single row in the "Do this first" queue at the top of the Summary tab.
 * Rendered inside one shared `Card` in `SummaryView`, with a hairline divider
 * between rows instead of each item being its own card.
 *
 * Deliberately richer than the "All findings" table below: this one leads with
 * severity + environment and ends in the recommended action, because it is the
 * row a user is meant to act on rather than scan.
 *
 * Only fields that exist on `InsightItem` are rendered. Notably absent, because
 * the data does not carry them: blast radius (`safety_band` lives on `_raw`, not
 * on `InsightItem`), change previews ("1 policy change · no restart"), grouped
 * roll-ups ("across 7 workloads"), and snooze.
 */

// Environment → categorical hue. Prod is the one that should catch the eye.
const ENV_HUE: Record<string, 'amber' | 'slate'> = {
  prod: 'amber',
  non_prod: 'slate',
};

const ENV_LABEL: Record<string, string> = {
  prod: 'prod',
  non_prod: 'non-prod',
};

// Age past which an unactioned finding is worth calling out in amber.
const STALE_DAYS = 7;

// Subtle middle-dot separator between metadata groups.
const Dot = () => (
  <Box component='span' sx={{ color: ds.gray[400], fontSize: ds.text.caption, lineHeight: 1, flexShrink: 0, userSelect: 'none' }}>
    •
  </Box>
);

const metaTextSx = { fontSize: ds.text.caption, color: ds.gray[500], whiteSpace: 'nowrap' } as const;

// Resource pill — icon and text share one surface.
const resourcePillSx = {
  display: 'flex',
  alignItems: 'center',
  gap: ds.space[1],
  height: ds.space[4],
  px: ds.space[2],
  borderRadius: ds.radius.sm,
  backgroundColor: ds.gray[100],
  flexShrink: 0,
  minWidth: 0,
} as const;

// A shade darker than the rest of the meta row (gray[500]) and the old pill
// (gray[600]) — the resource name is the anchor a reader scans for.
const resourcePillTextSx = {
  fontSize: ds.text.small,
  fontFamily: ds.font.mono,
  color: ds.gray[700],
  whiteSpace: 'nowrap',
} as const;

interface PriorityCardProps {
  item: InsightItem;
  /** Opens the detail panel for this finding. */
  onOpen: (id: string) => void;
  onAskNubi?: (item: InsightItem) => void;
  assistantName?: string;
  /** Renders a thin bottom divider — off for the last row in the list. */
  showDivider?: boolean;
}

const PriorityCard = ({ item, onOpen, onAskNubi, assistantName, showDivider = true }: PriorityCardProps) => {
  const severity = (item.severity || 'info').toLowerCase() as SeverityLevel;
  const detail = secondaryLine(item) || item.summary;
  const isStale = item.ageDays >= STALE_DAYS;
  // Only ever shown for genuine dollar savings — a CPU/Mem % delta isn't a
  // saving, so non-monetary impact values don't render here at all. Split
  // "$1,420/mo" into the figure (green) and the "/mo" cadence (gray).
  const isMoney = item.dollarImpact > 0;
  const impactSuffix = isMoney && item.impactValue.endsWith('/mo') ? '/mo' : '';
  const impactAmount = impactSuffix ? item.impactValue.slice(0, -impactSuffix.length) : item.impactValue;

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[2],
        py: ds.space[4],
        px: ds.space[2],
        '&:first-of-type': { pt: ds.space[2] },
        '&:last-of-type': { pb: ds.space[2] },
        borderBottom: showDivider ? `1px solid ${ds.gray[300]}` : 'none',
      }}
    >
      {/* Row 1 — severity icon leads, then the brief as the headline, env/type as tags */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2], flexWrap: 'wrap', minWidth: 0 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[2], minWidth: 0 }}>
          <SeverityIcon level={severity} size={12} aria-label={`${item.severity} severity`} />
          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, fontFamily: ds.font.display, color: ds.gray[700] }}>
            {detail || item.title}
          </Typography>
          {item.env && (
            <Chip variant='tag' size='2xs' hue={ENV_HUE[item.env] || 'slate'}>
              {ENV_LABEL[item.env] || item.env}
            </Chip>
          )}
          {/* Optimization type (the rule/category name) — only shown as a tag once the
            brief above has taken over the title slot; otherwise it'd repeat itself. */}
          {detail && (
            <Chip variant='tag' size='2xs' hue='slate'>
              {item.title}
            </Chip>
          )}
        </Box>
        {isMoney && (
          <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'flex-end', gap: '2px' }}>
            <Typography
              sx={{
                fontSize: ds.text.body,
                fontWeight: ds.weight.semibold,
                color: ds.green[600],
                fontVariantNumeric: 'tabular-nums',
              }}
            >
              {impactAmount}
            </Typography>
            {impactSuffix && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{impactSuffix}</Typography>}
          </Box>
        )}
      </Box>

      {/* Row 2 — provenance on the left, the action on the right */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[3], flexWrap: 'wrap' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap', minWidth: 0 }}>
          <Tooltip title={item.resourceId} placement='top'>
            <Box sx={{ ...resourcePillSx, maxWidth: 260 }}>
              <HexagonOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.gray[500], flexShrink: 0 }} />
              <Typography sx={{ ...resourcePillTextSx, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {truncateMiddle(resourceAnchorName(item.resourceId), 38)}
              </Typography>
            </Box>
          </Tooltip>
          {item.accountName && (
            <>
              <Dot />
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexShrink: 0 }}>
                <Typography sx={metaTextSx}>acc:</Typography>
                <CloudProviderIcon cloud_provider={item.provider} width='12px' height='12px' />
                <Typography sx={metaTextSx}>{item.accountName}</Typography>
              </Box>
            </>
          )}
          <Dot />
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexShrink: 0 }}>
            <AccessTimeOutlinedIcon sx={{ fontSize: ds.text.small, color: isStale ? ds.amber[600] : ds.gray[500] }} />
            <Typography sx={{ ...metaTextSx, color: isStale ? ds.amber[600] : ds.gray[500] }}>{formatAge(item.ageDays)} ago</Typography>
          </Box>
          <Dot />
          <Typography sx={metaTextSx}>{item.confidence}% confidence</Typography>
        </Box>

        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: ds.space[1], flexShrink: 0 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
            {onAskNubi && (
              <Tooltip title={`Ask ${assistantName || 'Nubi'}`} placement='top'>
                <span>
                  <Button
                    tone='ghost'
                    size='xs'
                    composition='icon-only'
                    icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                    aria-label={`Ask ${assistantName || 'Nubi'} about ${item.title}`}
                    id={`priority-ask-nubi-${item.id}`}
                    onClick={() => onAskNubi(item)}
                  />
                </span>
              </Tooltip>
            )}
            {item.nextStep.destructive ? (
              <Button tone='danger' size='xs' id={`priority-action-${item.id}`} onClick={() => onOpen(item.id)}>
                {item.nextStep.label}
              </Button>
            ) : (
              <Button
                tone='secondary'
                size='xs'
                trailingAccent={<ArrowForwardIcon />}
                id={`priority-action-${item.id}`}
                onClick={() => onOpen(item.id)}
              >
                {item.nextStep.label}
              </Button>
            )}
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default PriorityCard;
