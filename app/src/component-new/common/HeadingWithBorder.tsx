/**
 * HeadingWithBorder — section heading with a colored left-accent bar and an
 * optional trailing span / release icon.
 *
 * Copied from the legacy `@components1/common/TextWithBorder` into the
 * component-new tree for the redesigned K8s Cluster-Summary surfaces. The
 * markup is intentionally identical to the legacy component so call sites can
 * swap 1:1; callers pass `--ds-*` design tokens for `borderColor` and text
 * colors (via `sx` / `fontSx`).
 */
import * as React from 'react';
import { Box, Typography, type SxProps, type Theme } from '@mui/material';
import SafeIcon from '@common/SafeIcon';

export interface HeadingWithBorderProps {
  value?: React.ReactNode;
  sx?: SxProps<Theme>;
  borderWidth?: string;
  borderColor?: string;
  lineHeight?: string;
  padding?: string;
  span?: React.ReactNode;
  spanSx?: SxProps<Theme>;
  releaseIcon?: string;
  fontSx?: SxProps<Theme>;
}

const HeadingWithBorder = ({
  value = '',
  sx = {},
  borderWidth = '',
  borderColor = '',
  lineHeight = '',
  padding = '0px 10px',
  span = '',
  spanSx = {},
  releaseIcon,
  fontSx = { fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', lineHeight: lineHeight || 'auto' },
}: HeadingWithBorderProps) => {
  return (
    <Box sx={{ ...sx, borderLeft: `${borderWidth} solid ${borderColor}`, padding }}>
      {value && (
        <Typography sx={fontSx} className='border_text'>
          {value}{' '}
          {releaseIcon && (
            <sup>
              <SafeIcon src={releaseIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '2px' }} />
            </sup>
          )}
          {span && (
            <Typography variant='inherit' component='span' sx={spanSx}>
              {span}
            </Typography>
          )}
        </Typography>
      )}
    </Box>
  );
};

export default HeadingWithBorder;
