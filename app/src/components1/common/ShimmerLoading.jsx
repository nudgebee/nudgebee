import PropTypes from 'prop-types';
import { Box } from '@mui/material';

const ShimmerLoading = ({ isLoading, height, width, children, lines, lineHeight = '24px', lineSpacing = '12px' }) => {
  if (isLoading) {
    // If lines prop is provided, render multiple shimmer lines
    if (lines && lines > 0) {
      const widths = ['80%', '90%', '70%', '85%', '75%', '95%', '65%', '88%', '72%', '83%'];

      const shimmerStyles = {
        height: lineHeight,
        borderRadius: '4px',
        background: '#f0f0f0',
        position: 'relative',
        overflow: 'hidden',
        '&::before': {
          content: '""',
          position: 'absolute',
          top: 0,
          left: '-100%',
          width: '100%',
          height: '100%',
          background: 'linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.6), transparent)',
          animation: 'shimmer 1.5s infinite',
        },
        '@keyframes shimmer': {
          '0%': { left: '-100%' },
          '100%': { left: '100%' },
        },
      };

      return (
        <Box>
          {Array.from({ length: lines }, (_, index) => (
            <Box
              key={index}
              sx={{
                ...shimmerStyles,
                marginBottom: index === lines - 1 ? '0' : lineSpacing,
                width: widths[index % widths.length],
              }}
            />
          ))}
        </Box>
      );
    }

    // Default shimmer behavior (single block)
    return (
      <div
        className='shimmer'
        style={{
          height: height || '280px',
          width: width || '100%',
        }}
      />
    );
  }
  return children;
};

ShimmerLoading.propTypes = {
  isLoading: PropTypes.bool.isRequired,
  height: PropTypes.string,
  width: PropTypes.string,
  children: PropTypes.node,
  lines: PropTypes.number,
  lineHeight: PropTypes.string,
  lineSpacing: PropTypes.string,
};

export default ShimmerLoading;
