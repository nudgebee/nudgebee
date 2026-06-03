/**
 * Toggle — DS V2 retokenisation of `components1/workflow/NewToggleButtons`.
 * Spec: design-system/primitives/action/toggle.html
 *
 * Same visual structure as the legacy component (button-row inside a track,
 * active option gets a contrasting fill, no sliding pill). Only the colour /
 * shadow / radius / spacing layer has been moved onto `--ds-*` tokens, and
 * the icon-filter recolour hack has been kept for src-based SafeIcon icons
 * so callers don't have to change their option shape.
 *
 * Migration: `import ToggleButtons from '@components1/workflow/NewToggleButtons'`
 *         →  `import { Toggle } from '@components1/ds/Toggle'`
 *
 * API delta vs. legacy (drop-in, no rename required):6
 *   - All legacy props (`options`, `activeValue`, `width`, `size`, `noShadow`,
 *     `onChange`) preserved exactly.
 *   - `option.icon` now accepts EITHER a SafeIcon-compatible src (legacy) OR a
 *     React element (MUI icon, inline SVG). Element icons are coloured via
 *     `currentColor`; src icons keep the CSS-filter trick for parity.
 *
 * Variants:
 *   size = 'default' | 'large' | 'sm'   — preserved from legacy
 *
 * Don't (per spec):
 *   - Don't use Toggle as a form-value picker. Use Radio/Select instead.
 *   - Don't disable the option that's currently active. Pick a different default.
 *   - Don't pass more than 4 options — switch to Select / Tabs.6
 *   - Don't override colours via `sx`. Variant tokens are intentional.
 */
import * as React from 'react';
import { Box, Button } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';

export type ToggleSize = 'default' | 'large' | 'sm';

export interface ToggleOption {
  value: string;
  label: string;
  /** SafeIcon-compatible src (legacy) OR a React element (MUI icon, inline SVG). */
  icon?: unknown;
  disabled?: boolean;
}

export interface ToggleProps {
  options: ToggleOption[];
  activeValue: string;
  /** CSS width passed to the track. Use `'100%'` to fill the parent. */
  width?: string;
  size?: ToggleSize;
  /** Drops the outer shadow on `default` / `large` sizes. Ignored for `sm`. */
  noShadow?: boolean;
  onChange: (value: string) => void;
  id?: string;
  className?: string;
}

interface OptionStyles {
  background: string;
  color: string;
  boxShadow: string;
  hoverBackground: string;
  /** CSS filter that recolours a black SafeIcon src to match `color`. */
  iconFilter: string;
}

/**
 * Resolve the colour-state token bundle for an option.
 *
 * Three visual modes survive from the legacy component:
 *   1. default/large + active → brand-navy filled, white text/icon
 *   2. sm + active           → white pill, brand-navy text/icon
 *   3. inactive (both modes) → transparent track, gray text
 *
 * Filters: SafeIcon src icons can't inherit colour, so we recolour them via a
 * pre-computed CSS filter. ReactElement icons ignore this and use currentColor.
 */
function getOptionStyles(isActive: boolean, isSmall: boolean): OptionStyles {
  if (isActive && isSmall) {
    return {
      background: 'var(--ds-background-100)',
      color: 'var(--ds-brand-600)',
      // Soft elevation lifts the active pill off the gray track without
      // competing with the card-level shadow above it.
      boxShadow: '0 1px 3px var(--ds-gray-alpha-200), 0 1px 2px var(--ds-gray-alpha-100)',
      hoverBackground: 'var(--ds-background-100)',
      // Filter chain converts black source SVG → brand-navy #1B2D4A.
      iconFilter: 'brightness(0) saturate(100%) invert(15%) sepia(28%) saturate(1352%) hue-rotate(184deg) brightness(96%) contrast(96%)',
    };
  }
  if (isActive) {
    return {
      background: 'var(--ds-brand-600)',
      color: 'var(--ds-background-100)',
      boxShadow: '0 4px 12px var(--ds-gray-alpha-300)',
      hoverBackground: 'var(--ds-brand-500)',
      // Black SVG → white.
      iconFilter: 'brightness(0) invert(1)',
    };
  }
  if (isSmall) {
    return {
      background: 'transparent',
      color: 'var(--ds-gray-600)',
      boxShadow: 'none',
      hoverBackground: 'var(--ds-gray-200)',
      // Black SVG → mid-gray, matching --ds-gray-600.
      iconFilter: 'brightness(0) saturate(100%) invert(43%) sepia(0%) hue-rotate(0deg)',
    };
  }
  return {
    background: 'transparent',
    color: 'var(--ds-gray-700)',
    boxShadow: 'none',
    hoverBackground: 'var(--ds-gray-100)',
    iconFilter: 'none',
  };
}

interface SizeConfig {
  containerPadding: string;
  containerBorderRadius: string;
  optionPadding: string;
  optionFontSize: string;
  optionBorderRadius: string;
  iconSize: number;
  gap: string;
}

const SIZE_CONFIG: Record<ToggleSize, SizeConfig> = {
  default: {
    containerPadding: 'var(--ds-space-2)',
    containerBorderRadius: 'var(--ds-radius-md)',
    optionPadding: 'var(--ds-space-2) var(--ds-space-3)',
    optionFontSize: 'var(--ds-text-body)',
    optionBorderRadius: 'var(--ds-radius-sm)',
    iconSize: 18,
    gap: 'var(--ds-space-2)',
  },
  large: {
    containerPadding: '0',
    containerBorderRadius: 'var(--ds-radius-lg)',
    optionPadding: 'var(--ds-space-2) var(--ds-space-5)',
    optionFontSize: 'var(--ds-text-title)',
    optionBorderRadius: 'var(--ds-radius-md)',
    iconSize: 22,
    gap: 'var(--ds-space-2)',
  },
  sm: {
    containerPadding: 'var(--ds-space-1)',
    containerBorderRadius: 'var(--ds-radius-md)',
    optionPadding: 'var(--ds-space-1) var(--ds-space-2)',
    optionFontSize: 'var(--ds-text-small)',
    optionBorderRadius: 'var(--ds-radius-sm)',
    iconSize: 14,
    gap: 'var(--ds-space-1)',
  },
};

/**
 * Render the option's icon. Accepts either a React element (rendered as-is,
 * coloured via currentColor) or a SafeIcon-compatible src (wrapped in
 * SafeIcon, recoloured via CSS filter).
 */
function renderIcon(icon: unknown, iconSize: number, iconFilter: string): React.ReactNode {
  if (icon == null) return null;
  if (React.isValidElement(icon)) {
    return (
      <Box
        sx={{
          display: 'inline-flex',
          color: 'currentColor',
          '& svg': { width: iconSize, height: iconSize, color: 'currentColor' },
        }}
      >
        {icon}
      </Box>
    );
  }
  return (
    <Box
      sx={{
        display: 'inline-flex',
        '& img, & svg': {
          filter: iconFilter,
          transition: 'filter var(--ds-motion-micro, 120ms) var(--ds-motion-ease, ease)',
        },
      }}
    >
      <SafeIcon src={icon as never} alt='' height={iconSize} width={iconSize} />
    </Box>
  );
}

export const Toggle: React.FC<ToggleProps> = ({ options, activeValue, width, size = 'default', noShadow, onChange, id, className }) => {
  const config = SIZE_CONFIG[size];
  const isSmall = size === 'sm';

  return (
    <Box
      id={id}
      className={className}
      role='tablist'
      sx={{
        display: 'flex',
        backgroundColor: isSmall ? 'var(--ds-gray-100)' : 'var(--ds-background-100)',
        borderRadius: config.containerBorderRadius,
        border: isSmall ? 'none' : '1px solid var(--ds-gray-200)',
        // Outer shadow only on default/large (matches legacy). `sm` lives
        // inside cards already, so a second shadow would compound.
        boxShadow: noShadow || isSmall ? 'none' : '0 4px 14px var(--ds-gray-alpha-100), 0 2px 8px var(--ds-gray-alpha-100)',
        padding: config.containerPadding,
        width,
        boxSizing: 'border-box',
      }}
    >
      {options.map((option) => {
        const isActive = activeValue === option.value;
        const styles = getOptionStyles(isActive, isSmall);

        return (
          <Button
            key={option.value}
            id={`workflow-tab-${option.value}`}
            role='tab'
            aria-selected={isActive}
            aria-disabled={option.disabled || undefined}
            onClick={() => onChange(option.value)}
            disabled={option.disabled}
            disableRipple
            sx={{
              background: styles.background,
              border: 'none',
              padding: config.optionPadding,
              color: option.disabled ? 'var(--ds-gray-400)' : styles.color,
              fontFamily: 'var(--ds-font-sans)',
              fontSize: config.optionFontSize,
              fontWeight: isActive ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
              cursor: option.disabled ? 'not-allowed' : 'pointer',
              boxShadow: styles.boxShadow,
              borderRadius: config.optionBorderRadius,
              textTransform: 'none',
              flex: 1,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: config.gap,
              minWidth: 0,
              whiteSpace: 'nowrap',
              lineHeight: 1,
              opacity: option.disabled ? 0.5 : 1,
              transition:
                'background var(--ds-motion-micro, 120ms) var(--ds-motion-ease, ease), color var(--ds-motion-micro, 120ms) var(--ds-motion-ease, ease), box-shadow var(--ds-motion-micro, 120ms) var(--ds-motion-ease, ease)',
              '&.Mui-disabled': {
                color: 'var(--ds-gray-400)',
              },
              '&:hover': {
                background: option.disabled ? styles.background : styles.hoverBackground,
              },
              '&:focus-visible': {
                outline: '2px solid var(--ds-blue-500)',
                outlineOffset: 2,
              },
            }}
          >
            {renderIcon(option.icon, config.iconSize, styles.iconFilter)}
            {option.label}
          </Button>
        );
      })}
    </Box>
  );
};

export default Toggle;
