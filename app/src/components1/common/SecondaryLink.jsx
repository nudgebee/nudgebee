import React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';

const SecondaryLink = ({ style, onClick, children, prop }) => {
  return (
    <Box
      prop={prop}
      sx={{
        '&:hover .count': {
          color: 'var(--ds-blue-500) !important',
        },
        '& .count': {
          color: 'var(--ds-brand-500)',
          fontWeight: 'var(--ds-font-weight-medium)',
          fontSize: 'var(--ds-text-caption)',
        },

        fontSize: 'var(--ds-text-caption)',
        fontWeight: 'var(--ds-font-weight-regular)',
        textDecoration: 'none',
        color: 'var(--ds-brand-500)',
        '&:hover': {
          cursor: 'pointer',
          color: 'var(--ds-blue-500)',
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
