/**
 * CustomBorderCard — DS redesign of legacy CustomBorderCard.
 * Spec: app/design-system/primitives/layout/custom-border-card.html
 *
 * A lightweight surface with a bottom hairline and an optional coloured
 * left border. Used for stacked summary rows and accent panels where the
 * heavier `Card` primitive would be too much.
 *
 * Prefer `Card` for general card surfaces. Reach for CustomBorderCard only
 * when you specifically want the bottom-hairline + left-accent look.
 *
 * API preserved from V1 so existing consumers keep working:
 *   - borderColor      colour of the bottom hairline (transparent to hide)
 *   - borderLeftColor  colour of the left accent border
 *   - borderLeftWidth  width of the left accent border
 *   - showLeftBorder   toggle the left border on/off
 *   - padding          inner padding
 *   - onClick          makes the card interactive (cursor: pointer)
 *   - sx               escape hatch — avoid for token-equivalent changes
 */
import * as React from 'react';
import { Box, SxProps, Theme } from '@mui/material';

export interface CustomBorderCardProps {
  children?: React.ReactNode;
  /** Bottom hairline colour. Pass 'transparent' to hide it. */
  borderColor?: string;
  /** Left accent border colour. */
  borderLeftColor?: string;
  /** Left accent border width (e.g. '4px'). */
  borderLeftWidth?: string | number;
  /** Render the left accent border. */
  showLeftBorder?: boolean;
  /** Inner padding. */
  padding?: string | number;
  onClick?: (e: React.MouseEvent<HTMLDivElement>) => void;

  id?: string;
  className?: string;
  'data-testid'?: string;

  /** Escape hatch — do not use for token-equivalent changes. */
  sx?: SxProps<Theme>;
}

const DEFAULT_BORDER_COLOR = 'var(--ds-blue-200)';
const DEFAULT_PADDING = 'var(--ds-space-4) 25px var(--ds-space-4) var(--ds-space-4)';

export const CustomBorderCard: React.FC<CustomBorderCardProps> = ({
  children,
  borderColor = DEFAULT_BORDER_COLOR,
  borderLeftColor,
  borderLeftWidth,
  showLeftBorder = true,
  padding = DEFAULT_PADDING,
  onClick,
  id,
  className,
  sx,
  ...rest
}) => {
  return (
    <Box
      id={id}
      className={className}
      onClick={onClick}
      sx={{
        padding,
        backgroundColor: 'var(--ds-background-100)',
        borderRadius: 'var(--ds-radius-md)',
        ...sx,
        cursor: onClick ? 'pointer' : 'default',
        borderBottom: `1px solid ${borderColor || 'transparent'}`,
        borderLeftColor,
        borderLeftWidth,
        borderLeftStyle: showLeftBorder ? 'solid' : 'none',
      }}
      {...rest}
    >
      {children}
    </Box>
  );
};

export default CustomBorderCard;
