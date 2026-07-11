/**
 * Heading — section heading with a colored left-accent bar and an
 * optional trailing span / release icon.
 */
import * as React from 'react';
import { Box, Typography, type SxProps, type Theme } from '@mui/material';
import SafeIcon from '@shared/icons/SafeIcon';
import { ds } from '@utils/colors';

export const borderWidthMap = {
  zero: '0px',
  sm: '2px',
  md: '3px', // intentionally no ds tokens for border Widths
  lg: '4px',
};

const HeadingStyle = {
  '& p': {
    fontSize: 'var(--ds-text-title)',
    fontWeight: 'var(--ds-font-weight-semibold)',
    color: 'var(--ds-gray-700)',
  },
  padding: '0px var(--ds-space-2)',
};

const subHeadingStyle = {
  '& p': {
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-medium)',
    color: 'var(--ds-gray-700)',
  },
  padding: '0px var(--ds-space-1)',
};

const styleMap = {
  zero: {
    ...HeadingStyle,
    padding: '0',
  },
  sm: subHeadingStyle,
  md: HeadingStyle,
  lg: HeadingStyle,
};
export interface HeadingProps {
  value?: React.ReactNode;
  sx?: SxProps<Theme>;
  borderWidth: 'zero' | 'sm' | 'md' | 'lg';
  borderColor?: string;
  span?: React.ReactNode;
  spanSx?: SxProps<Theme>;
  releaseIcon?: string;
}

const Heading = ({ value = '', sx = {}, borderWidth = 'zero', borderColor = 'black', span = '', spanSx = {}, releaseIcon }: HeadingProps) => {
  return (
    <Box sx={{ borderLeft: `${borderWidthMap[borderWidth]} solid ${borderColor}`, ...styleMap[borderWidth], ...sx }}>
      {(value || value === 0) && (
        <Typography sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }} className='border_text'>
          {value}
          {releaseIcon && <SafeIcon src={releaseIcon} alt='Beta Icon' width={25} height={20} />}
          {span && (
            <Typography
              variant='inherit'
              component='span'
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-600)',
                ...spanSx,
              }}
            >
              {span}
            </Typography>
          )}
        </Typography>
      )}
    </Box>
  );
};

export default Heading;
