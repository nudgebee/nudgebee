import { Box } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import SafeIcon from './SafeIcon';

function CustomIcon({ icon }) {
  return (
    <Box display='flex' alignItems='center' gap='12px'>
      <Box
        sx={{
          width: '27px',
          height: '27px',
          minWidth: '27px',
          minHeight: '27px',
          borderRadius: 'var(--ds-radius-sm)',
          bgcolor: 'var(--ds-blue-100)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        {icon &&
          (React.isValidElement(icon) ? (
            icon
          ) : typeof icon === 'function' ? (
            React.createElement(icon, { width: 16, height: 16 })
          ) : (
            <SafeIcon src={icon} alt='icon' height={16} width={16} />
          ))}
      </Box>
    </Box>
  );
}

export default CustomIcon;

CustomIcon.propTypes = {
  icon: PropTypes.any,
};
