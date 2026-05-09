import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';

const PrimaryLink = ({ style = {}, onClick, children, prop }) => {
  return (
    <Box
      onClick={onClick}
      prop={prop}
      sx={{
        fontSize: '14px',
        fontWeight: 400,
        color: '#3047EC',
        textDecoration: 'none',
        display: 'inline',
        cursor: 'pointer',
        ...style,
      }}
    >
      {children}
    </Box>
  );
};

PrimaryLink.propTypes = {
  style: PropTypes.object,
  onClick: PropTypes.func,
  children: PropTypes.node.isRequired,
  prop: PropTypes.object,
};

export default PrimaryLink;
