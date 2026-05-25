import { Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const Title = ({ isUnderline = true, title, lightVariant = false, sx }) => {
  return (
    <div style={{ display: 'inline-flex', flexDirection: 'column', gap: lightVariant ? 1 : -2 }}>
      <Typography
        sx={{
          fontSize: lightVariant ? '16px' : '14px',
          fontWeight: '600',
          display: 'inline-block',
          color: lightVariant ? colors.text.tertiary : colors.text.title,
          ...sx,
        }}
      >
        {title}
      </Typography>
      {isUnderline && (
        <span
          style={
            lightVariant
              ? {
                  height: '2px',
                  borderRadius: '2px',
                  width: '16px',
                  backgroundColor: colors.text.primaryLight,
                }
              : {
                  height: 2,
                  width: '24px',
                  backgroundColor: colors.background.titleUnderline,
                }
          }
        />
      )}
    </div>
  );
};

export default Title;

Title.propTypes = {
  title: PropTypes.any,
  lightVariant: PropTypes.bool,
  sx: PropTypes.any,
  isUnderline: PropTypes.bool,
};
