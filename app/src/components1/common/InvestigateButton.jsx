import { Box, IconButton } from '@mui/material';
import Link from 'next/link';
import { action } from 'src/utils/actionStyles';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { FiArrowRight } from 'react-icons/fi';
import CustomTooltip from './CustomTooltip';

const InvestigateButton = ({ displayText = false, sx = {}, url, onClick, text = 'Investigate' }) => {
  return (
    <IconButton
      id='investigate-btn'
      component={url ? Link : 'button'}
      href={url}
      sx={{
        ...action.investigateOutline,
        ...sx,
        width: displayText ? 'max-content !important' : '28px',
        fontSize: '13px',
        color: colors.text.secondary,
      }}
      aria-label='Troubleshoot'
      onClick={(event) => {
        event.stopPropagation();
        if (onClick) {
          onClick(event);
        }
      }}
    >
      {displayText ? (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            cursor: 'pointer',
            '&:hover .icon-box': {
              transform: 'translateX(3px)',
            },
          }}
        >
          {displayText && <span>{text}</span>}
          <Box
            className='icon-box'
            sx={{
              backgroundColor: 'var(--nb-color-yellow)',
              color: 'var(--nb-text-on-yellow)',
              borderRadius: '2px',
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
        </Box>
      ) : (
        <CustomTooltip title='Troubleshoot'>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            {displayText && <span>{text}</span>}
            <Box
              sx={{
                backgroundColor: 'var(--nb-color-yellow)',
                color: 'var(--nb-text-on-yellow)',
                borderRadius: '2px',
                height: '14px',
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
              }}
            >
              <FiArrowRight />
            </Box>{' '}
          </Box>
        </CustomTooltip>
      )}
    </IconButton>
  );
};

InvestigateButton.propTypes = {
  displayText: PropTypes.bool,
  sx: PropTypes.object,
  url: PropTypes.string,
  onClick: PropTypes.func,
  text: PropTypes.string,
};

export default InvestigateButton;
