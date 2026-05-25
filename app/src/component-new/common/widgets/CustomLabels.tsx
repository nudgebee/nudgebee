/**
 * CustomLabels — domain composition: a status-axis pill that auto-detects its
 * tone from the rendered text (e.g. text='Failed' → critical, text='Active' →
 * success), with optional truncation tooltip and a small dropdown-arrow icon.
 *
 * NOT a generic primitive — the auto-tone-from-text behavior is app-specific
 * (Set membership for English status words). For new code prefer
 * `{ Label } from '@components1/ds/Label'` directly with an explicit `tone`.
 *
 * When to use (CustomLabels/Label vs Chip):
 *   - CustomLabels / Label → read-only status pill for table cells: `Active`,
 *     `Failed`, `Pending`. 5 Status-axis tones only. No click handler.
 *   - Chip → interactive or categorical pill: filters, dismissible tags,
 *     counts, categorical hues, avatars.
 *   Rule: clickable or categorical → Chip. Just status text → Label
 *   (or CustomLabels if you need legacy auto-tone-from-text + tooltip-on-truncate).
 *
 * Previously deprecated 2026-05-07 → demoted to domain composition 2026-05-07.
 * Internals now use `ds/Label` for the visual pill; the wrapper kept for the
 * auto-detect, tooltip-on-truncate, and dropdown-arrow chrome that 75 sites
 * depend on. Inlining those behaviors per-site was rejected as code spread.
 *
 * Item-shape preserved from V1:
 *   - text, height, variant, margin, displayTooltip, wordBreak, textTransform,
 *     maxWidth, width, customLabelStyle, tooltipCharLimit, showDropdownArrow
 */
import React, { useEffect } from 'react';
import Box from '@mui/material/Box';
import { Label, type LabelTone } from '@components1/ds/Label';
import CustomTooltip from '@common/CustomTooltip';
import { MenuArrowDownIcon } from '@assets';
import SafeIcon from '@common/SafeIcon';

// V1 variant strings → V2 ds/Label tones. The two odd-ones-out:
//   - 'criticalRed' renders white-on-bright-red in V1 (stronger than V2 'critical' which is red-on-red-100).
//     Mapped to 'critical' here — visual flattening accepted; the small handful of 'critical' text matches still get a status pill.
//   - 'orange' was a near-duplicate of yellow in V1. Mapped to 'warning' (same V2 tone as yellow).
const VARIANT_TO_TONE: Record<string, LabelTone> = {
  green: 'success',
  red: 'critical',
  criticalRed: 'critical',
  yellow: 'warning',
  orange: 'warning',
  blue: 'info',
  grey: 'neutral',
  null: 'neutral',
};

const RED_LABELS = new Set(['error', 'firing', 'failed', 'suspended', 'high', 'disabled', 'highest', 'rejected', 'unhealthy', 'incompatible']);
const GREEN_LABELS = new Set([
  'complete',
  'active',
  'succeeded',
  'resolved',
  'closed',
  'done',
  'ok',
  'enabled',
  'approved',
  'success',
  'completed',
  'healthy',
  'compatible',
]);
const YELLOW_LABELS = new Set(['pending', 'inactive', 'in progress', 'skipped', 'medium', 'in_progress']);
const CRITICAL_LABELS = new Set(['critical']);
const BLUE_LABELS = new Set(['low']);

function resolveToneByText(textLower: string): LabelTone {
  if (RED_LABELS.has(textLower)) return 'critical';
  if (GREEN_LABELS.has(textLower)) return 'success';
  if (YELLOW_LABELS.has(textLower)) return 'warning';
  if (CRITICAL_LABELS.has(textLower)) return 'critical';
  if (BLUE_LABELS.has(textLower)) return 'info';
  return 'neutral';
}

interface CustomLabelsProps {
  text: string;
  height?: string;
  variant?: string;
  margin?: string;
  displayTooltip?: boolean;
  wordBreak?: string;
  textTransform?: string;
  maxWidth?: string;
  width?: string;
  customLabelStyle?: Record<string, unknown>;
  tooltipCharLimit?: number;
  showDropdownArrow?: boolean;
  /** Status dot before the text. Mutually exclusive with `icon` — Label drops dot if both passed. */
  dot?: boolean;
  /** Leading icon before the text. Mutually exclusive with `dot`. */
  icon?: React.ReactNode;
}

const CustomLabels: React.FC<CustomLabelsProps> = ({
  text,
  variant = '',
  margin,
  displayTooltip = false,
  textTransform = 'capitalize',
  maxWidth = '350px',
  width = 'max-content',
  customLabelStyle = {},
  tooltipCharLimit,
  showDropdownArrow = false,
  dot = false,
  icon,
}) => {
  // No-op effect retained from previous deprecation header — kept to preserve
  // the React.memo identity surface; the deprecation warning itself was dropped
  // when this V1 demoted to a domain composition.
  useEffect(() => {}, []);

  const tone: LabelTone = variant && VARIANT_TO_TONE[variant] ? VARIANT_TO_TONE[variant] : resolveToneByText((text ?? '').toLowerCase());

  const shouldShowTooltip = !!(displayTooltip && text && tooltipCharLimit && text.length > tooltipCharLimit);
  const displayText = shouldShowTooltip ? `${text.substring(0, tooltipCharLimit)}...` : text || '-';

  const labelNode = (
    <Label
      tone={tone}
      size='sm'
      dot={dot}
      icon={icon}
      title={shouldShowTooltip ? text : undefined}
      // Style overrides preserved at the wrapper level — ds/Label is intentionally
      // tokenized, but the V1 callers expect tunable margin/width/maxWidth/textTransform.
    >
      <Box
        component='span'
        sx={{
          textTransform: textTransform as React.CSSProperties['textTransform'],
          maxWidth,
          width,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          ...customLabelStyle,
        }}
      >
        {displayText}
      </Box>
      {showDropdownArrow && (
        <SafeIcon
          src={MenuArrowDownIcon}
          alt='dropdown'
          width={12}
          height={12}
          style={{
            marginLeft: '4px',
            opacity: 0.8,
          }}
        />
      )}
    </Label>
  );

  // Outer Box preserves the legacy `margin` prop attachment point.
  const wrapped = margin ? <Box sx={{ display: 'inline-flex', margin }}>{labelNode}</Box> : labelNode;

  if (shouldShowTooltip) {
    return (
      <CustomTooltip title={text} tooltipClassName={''} placement={'bottom'}>
        {wrapped}
      </CustomTooltip>
    );
  }
  return wrapped;
};

// React.memo retained — this component renders in virtually every table row
// across 75+ consumers; preventing re-renders on prop equality is critical.
export default React.memo(CustomLabels);
