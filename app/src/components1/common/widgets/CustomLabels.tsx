import React from 'react';
import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import Tooltip from '@components1/ds/Tooltip';
import { colors } from 'src/utils/colors';
import { MenuArrowDownIcon } from '@assets';
import SafeIcon from '@common/SafeIcon';

// Hoisted to module scope — these depend only on the module-level `colors` import,
// so re-creating them on every render was pure waste (~74 consumer components).
const labelStyles = {
  null: { borderColor: ' ', background: colors.background.null, color: colors.text.null },
  yellow: { borderColor: ' ', background: colors.background.yellowLabel, color: colors.text.yellowLabel },
  orange: { borderColor: ' ', background: colors.background.white, color: colors.text.orangeLabel },
  green: { borderColor: ' ', background: colors.background.greenLabel, color: colors.success },
  red: { borderColor: ' ', background: colors.background.lightRedLabel, color: colors.errorText },
  criticalRed: { borderColor: ' ', background: colors.background.criticalRed, color: colors.text.white },
  grey: { background: colors.background.tertiaryLight, color: colors.text.tertiary },
  blue: { background: colors.background.blueLabel, color: colors.text.primaryDark },
};

// Pre-built Sets for O(1) text→style lookups (replaces Array.find on every render).
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
const CRITICAL_RED_LABELS = new Set(['critical']);
const BLUE_LABELS = new Set(['low']);

function resolveStyleByText(textLower: string) {
  if (RED_LABELS.has(textLower)) return labelStyles.red;
  if (GREEN_LABELS.has(textLower)) return labelStyles.green;
  if (YELLOW_LABELS.has(textLower)) return labelStyles.yellow;
  if (CRITICAL_RED_LABELS.has(textLower)) return labelStyles.criticalRed;
  if (BLUE_LABELS.has(textLower)) return labelStyles.blue;
  return labelStyles.grey;
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
}

const CustomLabels: React.FC<CustomLabelsProps> = ({
  wordBreak = '',
  text,
  height = '20px',
  variant = '',
  margin,
  displayTooltip = false,
  textTransform = 'capitalize',
  maxWidth = '350px',
  width = 'max-content',
  customLabelStyle = {},
  tooltipCharLimit,
  showDropdownArrow = false,
}) => {
  let currentLabelStyle = {};
  if (variant) {
    currentLabelStyle = labelStyles[variant as keyof typeof labelStyles] ?? {};
  } else {
    // Single toLowerCase() call instead of 5 repeated calls per render.
    currentLabelStyle = resolveStyleByText(text.toLowerCase());
  }

  const labelStyle = {
    padding: 'var(--ds-space-1) var(--ds-space-2)',
    margin: margin ?? 0,
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'center',
    borderRadius: 'var(--ds-radius-sm)',
    maxWidth: maxWidth,
    width: width,
    height: height,
    flexShrink: 0,
    flexDirection: 'row',
    boxShadow: 'none',
    gap: '0px',
    ...currentLabelStyle,
    ...customLabelStyle,
  };

  const textStyle = {
    fontFamily: 'Roboto',
    fontSize: 'var(--ds-text-caption)',
    fontStyle: 'normal',
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: 'normal',
    textTransform: textTransform ?? 'capitalize',
    wordBreak: wordBreak ?? '',
    ...currentLabelStyle,
  };

  // Check if text should be truncated and tooltip should be shown
  const shouldShowTooltip = displayTooltip && text && tooltipCharLimit && text.length > tooltipCharLimit;
  const displayText = shouldShowTooltip ? `${text.substring(0, tooltipCharLimit)}...` : text;

  return (
    <Box sx={labelStyle}>
      <Tooltip title={shouldShowTooltip ? text : ''} tooltipClassName={''} placement={'bottom'}>
        <Typography sx={textStyle}>{displayText || '-'}</Typography>
      </Tooltip>
      {showDropdownArrow && (
        <SafeIcon
          src={MenuArrowDownIcon}
          alt='dropdown'
          width={16}
          height={16}
          style={{
            marginLeft: 'var(--ds-space-1)',
            filter: 'inherit',
            opacity: '80%',
          }}
        />
      )}
    </Box>
  );
};

// React.memo prevents re-renders when props are unchanged — critical because
// this component is rendered in virtually every table row across 74+ consumers.
export default React.memo(CustomLabels);
