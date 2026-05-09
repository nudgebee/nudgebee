import React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';

const SecondaryLink = ({ style, onClick, children, prop }) => {
  return (
    <Box
      prop={prop}
      sx={{
        '&:hover .count': {
          color: '#3047ec !important',
        },
        '& .count': {
          color: '#374151',
          fontWeight: 500,
          fontSize: '11px',
        },

        fontSize: '11px',
        fontWeight: 400,
        textDecoration: 'none',
        color: '#374151',
        '&:hover': {
          cursor: 'pointer',
          color: '#3047ec',
        },
        ...style,
      }}
      onClick={onClick}
    >
      {children}
    </Box>
  );
};

SecondaryLink.propTypes = {
  style: PropTypes.object,
  onClick: PropTypes.func,
  children: PropTypes.node.isRequired,
  prop: PropTypes.object,
};

export default SecondaryLink;
