import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const SummaryLabels = ({ variant = 'info', label, grayText, sx = {} }) => {
  const getLabelColor = (variant) => {
    switch (variant) {
      case 'critical':
        return colors.critical; // '#c00000'
      case 'info':
        return colors.info; // '#3B82F6'
      case 'savings':
        return colors.success; // '#16A34A'
      default:
        return colors.info;
    }
  };

  const getLabelBgColor = (variant) => {
    switch (variant) {
      case 'critical':
        return '#FFEAEA';
      case 'info':
        return '#E6F1FF';
      case 'savings':
        return '#E5FFED';
      default:
        return '#E6F1FF';
    }
  };

  const labelColor = getLabelColor(variant);
  const labelBgColor = getLabelBgColor(variant);

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-1)',
        ...sx,
      }}
    >
      {/* Label */}
      <Box
        sx={{
          backgroundColor: labelBgColor,
          color: labelColor,
          padding: '0px var(--ds-space-1)',
          borderRadius: 'var(--ds-radius-sm)',
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-medium)',
          display: 'inline-flex',
          alignItems: 'center',
        }}
      >
        <Typography
          sx={{
            color: labelColor,
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: '16px',
            fontFamily: 'poppins',
          }}
        >
          {label}
        </Typography>
      </Box>

      {/* Gray Text */}
      {grayText && (
        <Typography
          sx={{
            color: colors.text.greyDark,
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-regular)',
            lineHeight: '16px',
            fontFamily: 'poppins',
          }}
        >
          {grayText}
        </Typography>
      )}
    </Box>
  );
};

SummaryLabels.propTypes = {
  variant: PropTypes.oneOf(['critical', 'info', 'savings']),
  label: PropTypes.string.isRequired,
  grayText: PropTypes.string,
  sx: PropTypes.object,
};

export default SummaryLabels;
