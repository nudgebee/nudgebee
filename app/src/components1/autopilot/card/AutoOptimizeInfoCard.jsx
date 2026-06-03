import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

const AutoPilotInfoCard = ({ shadow, cardTag, title, button, children, _data = {}, border, height }) => {
  return (
    <Box sx={{ minHeight: '180px' }}>
      {cardTag && (
        <Box sx={{ marginBottom: ds.space[3], marginLeft: ds.space[1] }}>
          <Typography sx={{ color: ds.gray[700], fontSize: ds.text.body, fontWeight: ds.weight.semibold }}>{cardTag}</Typography>
          <Box sx={{ backgroundColor: ds.blue[400], height: '2px', width: '28px' }} />
        </Box>
      )}
      <Box
        sx={{
          height: height ?? '190px',
          borderRadius: ds.radius.md,
          border: border || `1px solid ${ds.gray[200]}`,
          display: 'flex',
          minWidth: '370px',
          padding: `${ds.space[4]} ${ds.space[4]} ${ds.space[2]} ${ds.space[4]}`,
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: ds.space[3],
          boxShadow: shadow,
        }}
      >
        <Box sx={{ width: '100%', alignItem: 'cneter', display: 'flex', justifyContent: 'space-between' }}>
          <Box>
            <Typography sx={{ color: ds.gray[700], fontSize: ds.text.small, fontWeight: ds.weight.semibold }}>{title}</Typography>
          </Box>
          {button && <Box>{button}</Box>}
        </Box>
        {children}
      </Box>
    </Box>
  );
};

export default AutoPilotInfoCard;

AutoPilotInfoCard.propTypes = {
  shadow: PropTypes.any,
  cardTag: PropTypes.any,
  title: PropTypes.any,
  button: PropTypes.any,
  children: PropTypes.any,
  data: PropTypes.any,
  border: PropTypes.any,
  height: PropTypes.any,
};
