import { Box, Collapse } from '@mui/material';
import { useState, useEffect } from 'react';
import SafeIcon from '@common/SafeIcon';
import CustomSwitch from '@common/CustomSwitch';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const BlockWithToggle = ({ id = '', children, heading, icon, isActive, defaultStateOfExpand, handleSetToggle }) => {
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    if (defaultStateOfExpand) {
      setChecked(true);
    } else {
      setChecked(false);
    }
  }, [defaultStateOfExpand]);

  const handleChange = (e) => {
    setChecked(e.target.checked);
    handleSetToggle();
  };

  const styles = {
    lightBlueLabel: {
      padding: '9px 16px',
      fontSize: '14px',
      fontWeight: 600,
      color: colors.text.secondary,
      bgcolor: colors.background.tertiaryLight,
      borderRadius: '4px',
      flexGrow: 1,
      mb: '16px',
    },

    numberWithHeading: {
      display: 'flex',
      width: '100%',
      gap: '8px',

      '& .wrapper': {
        width: '100%',
      },

      '& .number-heading': {
        height: '40px',
        width: '40px',
        bgcolor: colors.background.tertiaryLight,
        borderRadius: '4px',
        fontSize: '16px',
        fontWeight: '600',
        color: colors.text.secondary,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      },

      '& .main-heading': {
        padding: '9px 16px',
        fontSize: '14px',
        fontWeight: 600,
        color: colors.text.tertiary,
        bgcolor: checked ? colors.background.primaryLightest : colors.background.tertiaryLight,
        borderRadius: checked ? '4px 4px 0px 0px' : '8px',
        height: '40px',
        boxSizing: 'border-box',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        width: '100% !important',
      },

      '& .main-wrapper': {
        display: 'flex',
        alignItems: 'center',
        gap: '10px',
      },
    },
    grayLabel: {
      color: colors.text.tertiary,
      fontSize: '12px',
      fontWeight: '500',
    },
    tabButton: {
      width: '180px',
      padding: '8px 12px',
      fontSize: '14px',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      textTransform: 'unset',
      borderRadius: '4px',
      bgcolor: colors.background.tertiaryLight,
      color: colors.text.secondary,
      fontWeight: '400',
      gap: '10px',

      '& img': {
        width: '14px',
        height: '14px',
        objectFit: 'contain',
      },

      '&.active': {
        bgcolor: colors.background.secondary,
        color: colors.text.white,
        fontWeight: '500',
      },
    },

    grid: {
      display: 'grid',
      gap: '10px',
      gridTemplateColumns: '1fr 36px',
    },
  };

  return (
    <Box sx={styles.numberWithHeading}>
      <Box className='wrapper'>
        <Box className='main-heading'>
          <Box className='main-wrapper'>
            <SafeIcon src={icon} alt='' width={20} height={20} />
            {heading}
          </Box>
          {isActive && <CustomSwitch id={id} defaultChecked={false} onChange={handleChange} checked={checked} />}
        </Box>
        <Collapse in={checked}>
          <Box bgcolor={colors.background.primaryLightest} p={'10px'} borderRadius={checked ? '0px 0px 4px 4px' : '8px'}>
            {children}
          </Box>
        </Collapse>
      </Box>
    </Box>
  );
};

BlockWithToggle.propTypes = {
  id: PropTypes.string,
  children: PropTypes.node,
  heading: PropTypes.string,
  icon: PropTypes.any,
  isActive: PropTypes.bool,
  defaultStateOfExpand: PropTypes.bool,
  handleSetToggle: PropTypes.func,
};

export default BlockWithToggle;
