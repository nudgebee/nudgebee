import React from 'react';
import { Box } from '@mui/material';
import Text from '@common/format/Text';
import PropTypes from 'prop-types';

function HighLights({ text, styles = {}, containerStyles = {}, component = null }) {
  const defaultStyle = {
    color: 'var(--ds-gray-400)',
    fontSize: 'var(--ds-text-body-lg)',
    fontStyle: 'normal',
    fontWeight: 'var(--ds-font-weight-regular)',
    gap: 'var(--ds-space-1)',
  };
  return (
    <Box component='div' sx={{ padding: 'var(--ds-space-1) var(--ds-space-2)', ...containerStyles }}>
      {component ? <>{component}</> : <Text value={text} showAutoEllipsis sx={{ ...defaultStyle, ...styles }} />}
    </Box>
  );
}

export default HighLights;

HighLights.propTypes = {
  text: PropTypes.any,
  styles: PropTypes.any,
  containerStyles: PropTypes.any,
  component: PropTypes.any,
};
