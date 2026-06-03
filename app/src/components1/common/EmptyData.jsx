import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';

const EmptyData = ({ id, img, heading, subHeading, height = '308px', sx = {}, children }) => {
  return (
    <Box>
      <Box display={'flex'} justifyContent={'center'} alignItems={'center'} height={height} gap={'var(--ds-space-2)'} sx={{ ...sx }}>
        {img && <SafeIcon src={img} alt='empty data' height={128} width={128} />}
        <Box
          sx={{
            '& h2': {
              fontSize: 'var(--ds-text-heading)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              margin: '0px !important',
              color: 'var(--ds-gray-600)',
            },
            '& p': {
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-regular)',
              margin: '0px !important',
              color: 'var(--ds-gray-500)',
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
