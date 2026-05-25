/**
 * Divider — DS V2 of legacy CustomDivider.
 * Spec: app/design-system/primitives/layout/divider.html
 *
 * Visual separator. Thin. Quiet. Use sparingly — whitespace usually does the
 * job better.
 *
 * Variants per spec:
 *   orientation = 'horizontal' | 'vertical'
 *   style       = 'solid' | 'dashed'
 *   composition = 'line' | 'line+label'  (auto from `label` prop presence)
 *
 * Don't (per spec):
 *   - Don't combine Divider with a heavy spacing token. The whole point is
 *     to use less space.
 *   - Don't use vertical Dividers between unrelated UI clusters. Whitespace
 *     and grouping by shape.
 *
 * Migration:
 *   `import CustomDivider from '@components1/common/CustomDivider'`
 * → `import { Divider } from '@components1/ds/Divider'`
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type DividerOrientation = 'horizontal' | 'vertical';
export type DividerStyle = 'solid' | 'dashed';

export interface DividerProps {
  orientation?: DividerOrientation;
  style?: DividerStyle;
  /** Optional inline label (e.g. "or"). Renders the line+label composition. Horizontal only. */
  label?: React.ReactNode;
  /** Override line color. Defaults to `var(--ds-gray-200)`. */
  color?: string;
  /** Override thickness in px. Defaults to 1. */
  thickness?: number;
  className?: string;
  id?: string;
  sx?: object;
}

export function Divider({
  orientation = 'horizontal',
  style = 'solid',
  label,
  color = 'var(--ds-gray-200)',
  thickness = 1,
  className,
  id,
  sx,
}: DividerProps) {
  const borderStyle = style === 'dashed' ? 'dashed' : 'solid';

  // Vertical: render an inline-block strip. Spec: vertical does not support label.
  if (orientation === 'vertical') {
    return (
      <Box
        component='span'
        role='separator'
        aria-orientation='vertical'
        id={id}
        className={className}
        sx={{
          display: 'inline-block',
          width: 0,
          alignSelf: 'stretch',
          minHeight: '1em',
          borderLeft: `${thickness}px ${borderStyle} ${color}`,
          marginLeft: 'var(--ds-space-2)',
          marginRight: 'var(--ds-space-2)',
          ...sx,
        }}
      />
    );
  }

  // Horizontal + label = "line+label" composition. Two flanking lines with the label between.
  if (label !== undefined) {
    return (
      <Box
        role='separator'
        aria-orientation='horizontal'
        id={id}
        className={className}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-3)',
          marginTop: 'var(--ds-space-3)',
          marginBottom: 'var(--ds-space-3)',
          ...sx,
        }}
      >
        <Box aria-hidden='true' sx={{ flex: 1, borderTop: `${thickness}px ${borderStyle} ${color}` }} />
        <Box
          component='span'
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
            fontWeight: 'var(--ds-font-weight-regular)',
            whiteSpace: 'nowrap',
          }}
        >
          {label}
        </Box>
        <Box aria-hidden='true' sx={{ flex: 1, borderTop: `${thickness}px ${borderStyle} ${color}` }} />
      </Box>
    );
  }

  // Horizontal + no label = "line" composition.
  return (
    <Box
      component='hr'
      role='separator'
      aria-orientation='horizontal'
      id={id}
      className={className}
      sx={{
        border: 0,
        borderTop: `${thickness}px ${borderStyle} ${color}`,
        marginTop: 'var(--ds-space-3)',
        marginBottom: 'var(--ds-space-3)',
        ...sx,
      }}
    />
  );
}

export default Divider;
