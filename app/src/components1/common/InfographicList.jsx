import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const InfographicList = ({ sequence }) => {
  return (
    <Box
      sx={{
        background: colors.background.infoGraphic,
        border: `0.5px solid ${colors.button.tertiaryBorder}`,
        borderRadius: '4px',
        display: 'flex',
        height: '36px',
        alignItems: 'center',
        p: '0px 16px',
        boxShadow: '0px 4px 6px -1px #EFF6FF',
      }}
    >
      {sequence.length &&
        sequence.map((item) => {
          return (
            <>
              <Box key={item.text} sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', minWidth: '100px' }}>
                <Typography sx={{ fontWeight: 400, fontSize: '12px', color: colors.text.secondary, mr: '20px' }}>{item.text}</Typography>
                <Typography sx={{ fontWeight: 600, fontSize: '16px', color: colors.text.secondary }}>{item.value}</Typography>
              </Box>
              {item != sequence[sequence.length - 1] && <Box sx={{ height: '15px', width: '0.5px', background: colors.iconColor, mx: '24px' }} />}
            </>
          );
        })}
    </Box>
  );
};

export default InfographicList;

InfographicList.propTypes = {
  sequence: PropTypes.array,
};
