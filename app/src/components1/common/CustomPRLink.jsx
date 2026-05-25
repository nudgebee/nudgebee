import { Box, Tooltip, Typography } from '@mui/material';
import React from 'react';
import CallMergeIcon from '@mui/icons-material/CallMerge';
import PropTypes from 'prop-types';

const extractPRIdentifier = (url) => {
  if (!url) {
    return url;
  }
  const match = url.match(/\/pull\/(\d+)/);
  if (match) {
    return `#${match[1]}`;
  }
  const parts = url.split('/').filter(Boolean);
  return parts.length > 0 ? `#${parts[parts.length - 1]}` : url;
};

const CustomPRLink = ({ prURL, statusMessage }) => {
  if (!prURL) {
    return null;
  }

  const prId = extractPRIdentifier(prURL);

  return (
    <Tooltip title={statusMessage || 'Open Pull Request'} arrow>
      <Box
        component='a'
        href={prURL}
        target='_blank'
        rel='noopener noreferrer'
        onClick={(e) => e.stopPropagation()}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: '3px',
          backgroundColor: '#EEF6EE',
          border: '1px solid #C0DFC0',
          borderRadius: '4px',
          padding: '1px 6px',
          textDecoration: 'none',
          cursor: 'pointer',
          mt: '2px',
          '&:hover': {
            backgroundColor: '#DCF0DC',
          },
        }}
      >
        <CallMergeIcon sx={{ fontSize: '13px', color: '#2E7D32' }} />
        <Typography sx={{ fontSize: '11px', fontWeight: 500, color: '#2E7D32', lineHeight: '18px' }}>PR {prId}</Typography>
      </Box>
    </Tooltip>
  );
};

CustomPRLink.propTypes = {
  prURL: PropTypes.string,
  statusMessage: PropTypes.string,
};

export default CustomPRLink;
