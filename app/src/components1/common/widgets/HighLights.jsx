import React from 'react';
import { Box } from '@mui/material';
import Text from '@common/format/Text';
import PropTypes from 'prop-types';

function HighLights({ text, styles = {}, containerStyles = {}, component = null }) {
  const defaultStyle = {
    color: '#9F9F9F',
    fontSize: '14px',
    fontStyle: 'normal',
    fontWeight: 400,
    gap: '5px',
  };
  return (
    <Box component='div' sx={{ padding: '2px 10px', ...containerStyles }}>
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
