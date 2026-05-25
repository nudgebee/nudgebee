import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const EmptyData = ({ id, img, heading, subHeading, height = '308px', sx = {}, children }) => {
  return (
    <Box>
      <Box display={'flex'} justifyContent={'center'} alignItems={'center'} height={height} gap={'10px'} sx={{ ...sx }}>
        {img && <SafeIcon src={img} alt='empty data' height={128} width={128} />}
        <Box
          sx={{
            '& h2': {
              fontSize: '24px',
              fontWeight: 600,
              margin: '0px !important',
              color: colors.text.secondary,
            },
            '& p': {
              fontSize: '12px',
              fontWeight: 400,
              margin: '0px 0px !important',
              color: colors.text.tertiary,
            },
          }}
        >
          <h2 id={`${id}-no-data`}>{heading}</h2>
          {subHeading && <Typography>{subHeading}</Typography>}
          {children}
        </Box>
      </Box>
    </Box>
  );
};
export default EmptyData;

EmptyData.propTypes = {
  id: PropTypes.string,
  img: PropTypes.any,
  heading: PropTypes.string,
  subHeading: PropTypes.string,
  height: PropTypes.any,
  sx: PropTypes.object,
  children: PropTypes.any,
};
