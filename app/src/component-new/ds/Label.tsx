/**
 * Label — DS V2 status-axis pill.
 *
 * This is the feature-complete Label brought over from the redesigned
 * `CustomLabels` on the `new-color-tokens-table-component` branch (commit
 * 1485f3bcc2 — "UI components enhancements with the Design System"). It
 * replaces the earlier from-scratch `ds/Label.tsx`, which lacked the
 * legacy CustomLabels feature set (tone auto-detection from text, legacy
 * `variant` mapping, truncation tooltip, dropdown arrow, style overrides).
 *
 * Spec:        app/design-system/primitives/navigation/label.html
 * Variants:    tone = 'neutral' | 'info' | 'success' | 'warning' | 'critical'  (Status axis only)
 *              size = 'sm' | 'md'
 *              composition = 'text-only' | 'icon+text' | 'dot+text' (auto from props)
 *
 * Content:     pass either `children` (preferred for new DS code) or the
 *              legacy `text` prop. `children` wins when both are supplied.
 *
 * Tone precedence:  explicit `tone`  →  legacy `variant`  →  auto-detect from `text`.
 *
 * Migration:   `import CustomLabels from '@common/widgets/CustomLabels'`
 *              `import StatusBadge from '@common/StatusBadge'`
 *           →  `import { Label } from '@components1/ds/Label'`
 *
 *   V1 CustomLabels variant string  →  V2 tone (Status axis only)
 *     'green'                          'success'
 *     'red' / 'criticalRed'            'critical'
 *     'yellow' / 'orange'              'warning'
 *     'blue'                           'info'
 *     'grey' / 'null'                  'neutral'
 *
 * Don't (per spec):
 *   - Don't add a click handler. If interactive, use Chip.
 *   - Don't pick tone outside Status axis. No `purple` Label.
 *   - Don't combine `dot` and `icon`. Pick one (icon wins if both passed).
 *   - Don't use Label for cost. Use CostCallout (cost has its own primitive).
 */
import React from 'react';
import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import CustomTooltip from '@common/CustomTooltip';
import { MenuArrowDownIcon } from '@assets';
import SafeIcon from '@common/SafeIcon';

export type LabelTone = 'neutral' | 'info' | 'success' | 'warning' | 'critical';
export type LabelSize = 'sm' | 'md';

const TONE_PALETTE: Record<LabelTone, { bg: string; text: string; border: string; dot: string }> = {
  neutral: {
    bg: 'var(--ds-gray-100)',
    text: 'var(--ds-gray-700)',
    border: 'var(--ds-gray-200)',
    dot: 'var(--ds-gray-500)',
  },
  info: {
    bg: 'var(--ds-blue-100)',
    text: 'var(--ds-blue-700)',
    border: 'var(--ds-blue-200)',
    dot: 'var(--ds-blue-500)',
  },
  success: {
    bg: 'var(--ds-green-100)',
    text: 'var(--ds-green-700)',
    border: 'var(--ds-green-200)',
    dot: 'var(--ds-green-500)',
  },
  warning: {
    bg: 'var(--ds-amber-100)',
    text: 'var(--ds-amber-700)',
    border: 'var(--ds-amber-200)',
    dot: 'var(--ds-amber-500)',
  },
  critical: {
    bg: 'var(--ds-red-100)',
    text: 'var(--ds-red-700)',
    border: 'var(--ds-red-200)',
    dot: 'var(--ds-red-500)',
  },
};

const SIZE_TOKENS: Record<
  LabelSize,
  { fontSize: string; padX: string; padY: string; height: string; gap: string; iconSize: number; dotSize: number }
> = {
  sm: {
    fontSize: '10px',
    padX: 'var(--ds-space-2)',
    padY: '1px',
    height: '18px',
    gap: '4px',
    iconSize: 10,
    dotSize: 6,
  },
  md: {
    fontSize: 'var(--ds-text-small)',
    padX: 'var(--ds-space-3)',
    padY: '2px',
    height: '22px',
    gap: '6px',
    iconSize: 12,
    dotSize: 8,
  },
};

// Legacy `variant` strings → DS V2 tones.
const LEGACY_VARIANT_TO_TONE: Record<string, LabelTone> = {
  green: 'success',
  red: 'critical',
  criticalRed: 'critical',
  yellow: 'warning',
  orange: 'warning',
  blue: 'info',
  grey: 'neutral',
  null: 'neutral',
};

// Auto-resolve tone from label text (preserves the legacy behaviour for the
// many call sites that pass `text` only and rely on the implicit colour map).
const CRITICAL_TEXTS = new Set([
  'critical',
  'error',
  'firing',
  'failed',
  'suspended',
  'high',
  'disabled',
  'highest',
  'rejected',
  'unhealthy',
  'incompatible',
]);
const SUCCESS_TEXTS = new Set([
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
const WARNING_TEXTS = new Set(['pending', 'inactive', 'in progress', 'skipped', 'medium', 'in_progress']);
const INFO_TEXTS = new Set(['low']);

function resolveToneByText(textLower: string): LabelTone {
  if (CRITICAL_TEXTS.has(textLower)) return 'critical';
  if (SUCCESS_TEXTS.has(textLower)) return 'success';
  if (WARNING_TEXTS.has(textLower)) return 'warning';
  if (INFO_TEXTS.has(textLower)) return 'info';
  return 'neutral';
}

export interface LabelProps {
  /** Preferred content slot for new DS code. Wins over `text` when both are set. */
  children?: React.ReactNode;
  /** Legacy text content. Also feeds tone auto-detection and the truncation tooltip. */
  text?: string;
  /** DS V2 tone (Status axis). Wins over legacy `variant` and text auto-detection. */
  tone?: LabelTone;
  /** DS V2 size — 'sm' (default, 18px) or 'md' (22px). */
  size?: LabelSize;
  /** Optional left icon. Mutually exclusive with `dot`. */
  icon?: React.ReactNode;
  /** Render a status dot before the content. Mutually exclusive with `icon`. */
  dot?: boolean;
  /** Legacy override — mapped to a tone via LEGACY_VARIANT_TO_TONE. */
  variant?: string;
  /** Legacy explicit height (overrides the `size` token). */
  height?: string;
  margin?: string;
  displayTooltip?: boolean;
  wordBreak?: string;
  textTransform?: string;
  maxWidth?: string;
  width?: string;
  customLabelStyle?: Record<string, unknown>;
  tooltipCharLimit?: number;
  showDropdownArrow?: boolean;
  /** Native title attribute (tooltip for ellipsized labels). */
  title?: string;
  className?: string;
  id?: string;
}

const Label: React.FC<LabelProps> = ({
  children,
  text,
  tone,
  size = 'sm',
  icon,
  dot = false,
  variant = '',
  height,
  margin,
  displayTooltip = false,
  wordBreak = '',
  textTransform = 'capitalize',
  maxWidth = '350px',
  width = 'max-content',
  customLabelStyle = {},
  tooltipCharLimit,
  showDropdownArrow = false,
  title,
  className,
  id,
}) => {
  // Precedence: explicit `tone` → legacy `variant` → text auto-detection.
  let resolvedTone: LabelTone;
  if (tone) {
    resolvedTone = tone;
  } else if (variant && LEGACY_VARIANT_TO_TONE[variant]) {
    resolvedTone = LEGACY_VARIANT_TO_TONE[variant];
  } else {
    resolvedTone = resolveToneByText((text ?? '').toLowerCase());
  }

  const palette = TONE_PALETTE[resolvedTone];
  const tokens = SIZE_TOKENS[size];

  if (dot && icon && process.env.NODE_ENV !== 'production') {
    // eslint-disable-next-line no-console
    console.warn('[Label] `dot` and `icon` are mutually exclusive per spec. Rendering icon, dropping dot.');
  }
  const showDot = dot && !icon;

  const labelStyle = {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: tokens.gap,
    height: height ?? tokens.height,
    paddingLeft: tokens.padX,
    paddingRight: tokens.padX,
    paddingTop: tokens.padY,
    paddingBottom: tokens.padY,
    margin: margin ?? 0,
    maxWidth,
    width,
    flexShrink: 0,
    backgroundColor: palette.bg,
    color: palette.text,
    border: `1px solid ${palette.border}`,
    borderRadius: 'var(--ds-radius-sm)',
    whiteSpace: 'nowrap' as const,
    verticalAlign: 'middle',
    boxShadow: 'none',
    ...customLabelStyle,
  };

  const textStyle = {
    fontFamily: 'var(--ds-font-display)',
    fontSize: tokens.fontSize,
    fontStyle: 'normal',
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: 1,
    color: palette.text,
    textTransform: (textTransform ?? 'capitalize') as React.CSSProperties['textTransform'],
    wordBreak: (wordBreak ?? '') as React.CSSProperties['wordBreak'],
  };

  const shouldShowTooltip = !!(displayTooltip && text && tooltipCharLimit && text.length > tooltipCharLimit);
  const displayText = shouldShowTooltip ? `${text!.substring(0, tooltipCharLimit)}...` : text;
  // `children` is the preferred slot; fall back to the legacy `text` prop.
  const content = children ?? (displayText || '-');

  return (
    // `component='span'` keeps Label valid when rendered inline inside text /
    // a <Typography> (a <div> there triggers invalid-nesting hydration warnings).
    <Box component='span' id={id} className={className} title={title} sx={labelStyle}>
      {showDot && (
        <Box
          component='span'
          aria-hidden='true'
          sx={{
            width: tokens.dotSize,
            height: tokens.dotSize,
            borderRadius: 'var(--ds-radius-pill)',
            backgroundColor: palette.dot,
            flexShrink: 0,
          }}
        />
      )}
      {icon && (
        <Box
          component='span'
          aria-hidden='true'
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            width: tokens.iconSize,
            height: tokens.iconSize,
            color: palette.text,
            flexShrink: 0,
            '& svg': { width: '100%', height: '100%' },
          }}
        >
          {icon}
        </Box>
      )}
      <CustomTooltip title={shouldShowTooltip ? text! : ''} tooltipClassName={''} placement={'bottom'}>
        <Typography component='span' sx={textStyle}>
          {content}
        </Typography>
      </CustomTooltip>
      {showDropdownArrow && (
        <SafeIcon
          src={MenuArrowDownIcon}
          alt='dropdown'
          width={tokens.iconSize + 4}
          height={tokens.iconSize + 4}
          style={{
            marginLeft: '0px',
            filter: 'inherit',
            opacity: '60%',
          }}
        />
      )}
    </Box>
  );
};

// React.memo prevents re-renders when props are unchanged — critical because
// Label is rendered in virtually every table row across the app.
const MemoLabel = React.memo(Label);

export { MemoLabel as Label };
export default MemoLabel;
