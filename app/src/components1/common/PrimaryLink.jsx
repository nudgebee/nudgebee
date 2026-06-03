import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';

const PrimaryLink = ({ style = {}, onClick, children, prop }) => {
  return (
    <Box
      onClick={onClick}
      prop={prop}
      sx={{
        fontSize: 'var(--ds-text-body-lg)',
        fontWeight: 'var(--ds-font-weight-regular)',
        color: 'var(--ds-blue-500)',
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
