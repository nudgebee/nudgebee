import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Tooltip from '@ui/Tooltip';

// A field label with a trailing info icon that reveals a tooltip on hover. Passed
// as the `label` prop to DS Input/Select so the field keeps its native label slot
// (and required asterisk) while gaining inline help.
export default function LabelWithInfo({ text, info }) {
  return (
    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
      {text}
      <Tooltip title={info} arrow>
        <InfoOutlinedIcon sx={{ fontSize: 14, color: 'var(--ds-gray-400)', cursor: 'help' }} />
      </Tooltip>
    </Box>
  );
}

LabelWithInfo.propTypes = {
  text: PropTypes.node.isRequired,
  info: PropTypes.node.isRequired,
};
