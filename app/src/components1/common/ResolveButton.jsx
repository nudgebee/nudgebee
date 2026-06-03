import { Box, IconButton, Tooltip } from '@mui/material';
import { AutoPilotGreyIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { action } from 'src/utils/actionStyles';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { FiArrowRight } from 'react-icons/fi';

const ResolveButton = ({ displayText = false, onClick, sx, isResolvedConfigured = false }) => {
  return (
    <IconButton
      sx={{
        ...sx,
        ...action.investigateOutline,
        width: displayText ? 'max-content !important' : '28px',
        fontSize: 'var(--ds-text-body)',
        color: colors.text.secondary,
      }}
      onClick={onClick}
      aria-label={isResolvedConfigured ? 'Autopilot Configured' : 'Optimize'}
    >
      {displayText ? (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-1)',
            cursor: 'pointer',
            '&:hover .icon-box': {
              transform: 'translateX(3px)',
            },
          }}
        >
          {isResolvedConfigured && <SafeIcon priority={true} src={AutoPilotGreyIcon} alt='Autopilot Configured' />}
          {displayText && <span>{isResolvedConfigured ? 'Pilot on' : 'Optimize'}</span>}
          {!isResolvedConfigured && (
            <Box
              className='icon-box'
              sx={{
                backgroundColor: 'var(--ds-yellow-500)',
                borderRadius: 'var(--ds-radius-sm)',
                height: '14px',
                width: '14px',
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
                transition: 'transform 0.20s ease, background-color 0.15s ease',
              }}
            >
              <FiArrowRight size={12} />
            </Box>
          )}
        </Box>
      ) : (
        <Tooltip title={isResolvedConfigured ? 'Autopilot Configured' : 'Optimize'}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            {isResolvedConfigured && <SafeIcon priority={true} src={AutoPilotGreyIcon} alt='Autopilot Configured' />}
            {displayText && <span>{isResolvedConfigured ? 'Pilot on' : 'Optimize'}</span>}
            {!isResolvedConfigured && (
              <Box
                sx={{
                  backgroundColor: 'var(--ds-yellow-500)',
                  borderRadius: 'var(--ds-radius-sm)',
                  height: '14px',
                  width: '14px',
                  display: 'flex',
                  justifyContent: 'center',
                  alignItems: 'center',
                }}
              >
                <FiArrowRight />
              </Box>
            )}
          </Box>
        </Tooltip>
      )}
    </IconButton>
  );
};

ResolveButton.propTypes = {
  onClick: PropTypes.func,
  sx: PropTypes.object,
  isResolvedConfigured: PropTypes.bool,
  displayText: PropTypes.bool,
};

export default ResolveButton;
