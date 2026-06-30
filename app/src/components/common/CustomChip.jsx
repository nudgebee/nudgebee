import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

// NudgeBee CustomChip — small inline element for tags, counts, statuses, and meta info.
//
// Variants:
//  - tag: low-key inline label (e.g. region/agent name like `us-east-1`, `nb_audits`)
//  - count: clickable pill showing a count + label (e.g. "4 tasks") for opening drawers
//  - status: status indicator (success / error / warning / running / skipped / waiting)
//  - info: muted text-only meta info (e.g. "Sonnet 4.5", "2m ago"). No background, no border.
//  - filter: pill-shaped toggle button used for filter rows; pair with the `selected` prop.
//
// Optional `tone` prop applies a pastel palette to the `tag` and `count` variants:
// 'green' | 'blue' | 'pink' | 'lavender'. Use it to add subtle accent colour to count
// chips (tasks/contexts/memories) or category tags (memory_type, etc).

const baseInline = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 'var(--ds-space-1)',
  lineHeight: 1.4,
  boxSizing: 'border-box',
  whiteSpace: 'nowrap',
};

const variantStyles = {
  tag: {
    ...baseInline,
    backgroundColor: colors.background.tertiaryLightest,
    color: colors.text.secondary,
    border: `1px solid ${colors.border.secondaryLightest}`,
    borderRadius: 'var(--ds-radius-sm)',
    padding: 'var(--ds-space-1) var(--ds-space-2)',
    fontSize: 'var(--ds-text-caption)',
    fontWeight: 'var(--ds-font-weight-medium)',
    fontFamily: '"Roboto Mono", monospace',
  },
  count: {
    ...baseInline,
    backgroundColor: colors.background.white,
    color: colors.text.secondary,
    border: `1px solid ${colors.border.primary}`,
    borderRadius: 'var(--ds-radius-pill)',
    padding: 'var(--ds-space-1) var(--ds-space-2)',
    fontSize: 'var(--ds-text-caption)',
    fontWeight: 'var(--ds-font-weight-medium)',
    fontFamily: 'Roboto',
    cursor: 'pointer',
    transition: 'background-color 0.15s ease, border-color 0.15s ease',
    '&:hover': {
      backgroundColor: colors.background.primaryLightest,
      borderColor: colors.border.primaryLight,
    },
    '&:focus-visible': {
      outline: `2px solid ${colors.border.primary}`,
      outlineOffset: '2px',
    },
  },
  status: {
    ...baseInline,
    border: '1px solid transparent',
    borderRadius: 'var(--ds-radius-sm)',
    padding: 'var(--ds-space-1) var(--ds-space-2)',
    fontSize: 'var(--ds-text-caption)',
    fontWeight: 'var(--ds-font-weight-medium)',
    fontFamily: 'Roboto',
  },
  info: {
    ...baseInline,
    color: colors.text.tertiary,
    fontSize: 'var(--ds-text-caption)',
    fontWeight: 'var(--ds-font-weight-regular)',
    fontFamily: 'Roboto',
  },
  filter: {
    ...baseInline,
    borderRadius: 'var(--ds-radius-pill)',
    padding: 'var(--ds-space-1) var(--ds-space-3)',
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
    fontFamily: 'Roboto',
    cursor: 'pointer',
    transition: 'background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease',
  },
};

const statusPalettes = {
  success: { bg: colors.background.greenLabel, text: colors.text.green, border: colors.border.success },
  error: { bg: colors.background.lightRedLabel, text: colors.text.red, border: colors.border.error },
  warning: { bg: colors.background.yellowLabel, text: colors.text.yellowLabel, border: colors.border.warning },
  running: { bg: colors.background.primaryLightest, text: colors.primary, border: colors.border.primaryLightest },
  skipped: { bg: colors.background.tertiaryLightest, text: colors.text.tertiary, border: colors.border.secondary },
  waiting: { bg: colors.background.yellowLabel, text: colors.text.yellowLabel, border: colors.border.warning },
};

const tonePalettes = {
  green: { bg: colors.background.greenChip, text: colors.text.greenChip },
  blue: { bg: colors.background.blueChip, text: colors.text.blueChip },
  lavender: { bg: colors.background.purpleLabel, text: colors.text.purpleLabel },
  pink: { bg: colors.background.pastelPink, text: colors.text.pastelPink },
};

// Per-variant style overrides — extracted so the main render function stays shallow
// (Sonar S3776 cognitive-complexity limit).
const buildStatusOverrides = (status) => {
  const p = statusPalettes[status] || statusPalettes.success;
  return { backgroundColor: p.bg, color: p.text, borderColor: p.border };
};

const buildFilterOverrides = (selected) => ({
  backgroundColor: selected ? colors.primary : colors.background.tertiaryLightest,
  color: selected ? colors.text.white : colors.text.secondary,
  border: `1px solid ${selected ? colors.primary : colors.border.secondary}`,
  '&:hover': {
    backgroundColor: selected ? colors.primary : colors.background.primaryLightest,
    borderColor: selected ? colors.primary : colors.border.primaryLight,
  },
});

const buildToneOverrides = (variant, palette) => {
  const overrides = {
    backgroundColor: palette.bg,
    color: palette.text,
    border: '1px solid transparent',
  };
  if (variant === 'count') {
    overrides['&:hover'] = { backgroundColor: palette.bg, borderColor: palette.text, filter: 'brightness(0.97)' };
    overrides['&:focus-visible'] = { outline: `2px solid ${palette.text}`, outlineOffset: '2px' };
  }
  return overrides;
};

const tonePaletteFor = (variant, tone) => {
  if (!tone) return null;
  if (variant !== 'tag' && variant !== 'count') return null;
  return tonePalettes[tone] || null;
};

const computeMergedStyles = ({ variant, status, selected, tone }) => {
  const base = variantStyles[variant] || variantStyles.tag;
  if (variant === 'status' && status) {
    return { ...base, ...buildStatusOverrides(status) };
  }
  if (variant === 'filter') {
    return { ...base, ...buildFilterOverrides(selected) };
  }
  const palette = tonePaletteFor(variant, tone);
  if (palette) {
    return { ...base, ...buildToneOverrides(variant, palette) };
  }
  return base;
};

const interactiveOverrides = {
  cursor: 'pointer',
  '&:focus-visible': {
    outline: `2px solid ${colors.border.primary}`,
    outlineOffset: '2px',
  },
};

const CountLabel = ({ count, label, hasTonePalette }) => (
  <>
    <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', ...(hasTonePalette ? null : { color: colors.text.secondary }) }}>
      {count ?? 0}
    </Box>
    <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-regular)', ...(hasTonePalette ? null : { color: colors.text.secondaryDark }) }}>
      {label}
    </Box>
  </>
);

CountLabel.propTypes = {
  count: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  label: PropTypes.node,
  hasTonePalette: PropTypes.bool,
};

const CustomChip = ({ variant = 'tag', label, count, status, icon, onClick, selected = false, tone, sx = {}, id, ariaLabel }) => {
  const isInteractive = Boolean(onClick);
  const merged = computeMergedStyles({ variant, status, selected, tone });
  const hasTonePalette = Boolean(tonePaletteFor(variant, tone));
  const labelNode = variant === 'count' ? <CountLabel count={count} label={label} hasTonePalette={hasTonePalette} /> : label;

  return (
    <Box
      component={isInteractive ? 'button' : 'span'}
      type={isInteractive ? 'button' : undefined}
      onClick={onClick}
      id={id}
      aria-label={ariaLabel}
      sx={{
        ...(isInteractive ? { all: 'unset' } : null),
        ...merged,
        ...(isInteractive ? interactiveOverrides : null),
        ...sx,
      }}
    >
      {icon}
      {labelNode}
    </Box>
  );
};

CustomChip.propTypes = {
  variant: PropTypes.oneOf(['tag', 'count', 'status', 'info', 'filter']),
  label: PropTypes.node,
  count: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  status: PropTypes.oneOf(['success', 'error', 'warning', 'running', 'skipped', 'waiting']),
  icon: PropTypes.node,
  onClick: PropTypes.func,
  selected: PropTypes.bool,
  tone: PropTypes.oneOf(['green', 'blue', 'pink', 'lavender']),
  sx: PropTypes.object,
  id: PropTypes.string,
  ariaLabel: PropTypes.string,
};

export default CustomChip;
