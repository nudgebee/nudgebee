import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import SafeIcon from './SafeIcon';

const TextWithBorder = ({
  value = '',
  sx = {},
  borderWidth = '',
  borderColor = '',
  lineHeight = '',
  padding = '0px 10px',
  span = '',
  spanSx = {},
  releaseIcon,
  fontSx = { fontSize: '20px', fontWeight: '700', lineHeight: lineHeight || 'auto' },
}) => {
  return (
    <Box sx={{ ...sx, borderLeft: `${borderWidth} solid ${borderColor}`, padding: padding }}>
      {value && (
        <Typography sx={fontSx} className='border_text'>
          {value}{' '}
          {releaseIcon && (
            <sup>
              <SafeIcon src={releaseIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '2px' }} />
            </sup>
          )}
          {span && (
            <Typography variant='span' sx={spanSx}>
              {span}
            </Typography>
          )}
        </Typography>
      )}
    </Box>
  );
};

export default TextWithBorder;

TextWithBorder.propTypes = {
  value: PropTypes.string,
  sx: PropTypes.object,
  borderWidth: PropTypes.string,
  borderColor: PropTypes.string,
  lineHeight: PropTypes.string,
  padding: PropTypes.string,
  span: PropTypes.any,
  spanSx: PropTypes.object,
  releaseIcon: PropTypes.any,
  fontSx: PropTypes.object,
};
