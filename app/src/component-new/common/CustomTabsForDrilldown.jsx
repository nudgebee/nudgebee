/**
 * @deprecated Use `Tabs` from '@components1/ds/Tabs' instead.
 * V2 absorbs this + CustomTabs + ButtonTabs into one primitive with visual + navigation variants.
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
import { Box, Tab, Tabs } from '@mui/material';
import React, { useEffect } from 'react';

let _customTabsForDrilldownWarned = false;
import PropTypes from 'prop-types';
import CustomPill from './CustomPill';
import { AlphaIcon, BetaIcon } from '@assets';
import CustomButton from './NewCustomButton';
import { colors } from 'src/utils/colors';
import SafeIcon from './SafeIcon';

function a11yProps(index) {
  return {
    id: `${index}`,
    'aria-controls': `simple-tabpanel-${index}`,
  };
}

/**
 * @param {{
 *   value?: number | string,
 *   onChange: (e: any, value: number) => void,
 *   options?: Array<{
 *     value?: number | string,
 *     text?: string,
 *     icon?: React.ReactNode,
 *     disabled?: boolean
 *   }>,
 *   smallSize?: boolean,
 *   disableIndicatorTransition?: boolean,
 *   blueVariant?: boolean,
 *   padding?: string,
 *   rightButton?: {
 *     children?: React.ReactNode,
 *     onClick?: () => void,
 *     visible?: boolean
 *   }
 * }} props
 */

function _warnCustomTabsForDrilldown() {
  if (_customTabsForDrilldownWarned) return;
  _customTabsForDrilldownWarned = true;
  // eslint-disable-next-line no-console
  console.warn(
    '[deprecated] CustomTabsForDrilldown is deprecated. Use `import { Tabs } from "@components1/ds/Tabs"` instead. ' +
      'Tracked for removal 2026-06-06.'
  );
}

const CustomTabs = ({
  value,
  onChange,
  options = [],
  smallSize = false,
  disableIndicatorTransition = false,
  blueVariant = false,
  padding = '6px 32px 0px',
  rightButton = {
    children: <></>,
    onClick: () => {
      return;
    },
    visible: false,
  },
}) => {
  useEffect(() => {
    _warnCustomTabsForDrilldown();
  }, []);

  const selectedOption = options.find((opt) => opt.value === value);
  const showBottomMargin = selectedOption?.showBottomMargin || false;
  const showCustomRounded = selectedOption?.showCustomRounded || false;

  return (
    <Box
      sx={{
        width: '100%',
        position: 'relative',
        overflowX: 'hidden',
        bgcolor: '#FFFFFF',
        pt: '6px',
        borderRadius: showCustomRounded ? '8px 8px 0px 0px' : '8px',
      }}
    >
      <Tabs
        value={value}
        onChange={onChange}
        aria-label='scrollable auto tabs example'
        indicatorColor='primary'
        variant='scrollable'
        scrollButtons='auto'
        allowScrollButtonsMobile
        sx={{
          minHeight: 0,
          bgcolor: '#FFFFFF',
          maxWidth: '100%',
          overflowX: 'hidden',
          p: padding,
          borderRadius: showCustomRounded ? '8px 8px 0px 0px' : '8px',
          '& .MuiTabs-scrollButtons.Mui-disabled': {
            opacity: 0.3,
          },
          '& .MuiTabs-flexContainer': {
            display: 'flex',
            maxWidth: '100%',
            gap: '18px',
            '& .MuiTab-root': {
              minHeight: 0,
              minWidth: 0,
              p: 0,
              py: smallSize ? '5px' : '8px',
              fontSize: smallSize ? '10px' : '13px',
              borderRadius: smallSize ? '2px' : '4px',
              color: colors.text.secondary,
              borderColor: 'transparent',
              backgroundColor: colors.background.white,
              fontWeight: 400,
              mb: '5px',

              '&.Mui-selected': {
                color: colors.text.secondary,
                bgcolor: colors.background.white,
                fontWeight: 500,
              },
            },
          },
          '& .MuiTabs-indicator': {
            backgroundColor: blueVariant ? colors.background.primary : '#414F66',
            height: '2px',
            borderRadius: '19px',
          },
        }}
        TabIndicatorProps={
          disableIndicatorTransition
            ? {
                style: { display: 'none', transition: 'none' },
              }
            : {}
        }
      >
        {options?.map((opt, _idx) => (
          <Tab
            key={opt.value}
            label={
              <Box display='flex' alignItems='center' gap='6px'>
                <span>{opt.text}</span>
                {opt.betaIcon && (
                  <Box
                    component='span'
                    sx={{
                      display: 'inline-flex',
                      marginTop: '-10px',
                    }}
                  >
                    <SafeIcon src={BetaIcon} alt='Beta icon' style={{ height: '12px', width: '12px' }} />
                  </Box>
                )}
                {opt.alphaIcon && (
                  <Box
                    component='span'
                    sx={{
                      display: 'inline-flex',
                      marginTop: '-10px',
                    }}
                  >
                    <SafeIcon src={AlphaIcon} alt='Beta icon' style={{ height: '30px', width: '30px' }} />
                  </Box>
                )}
              </Box>
            }
            value={opt.value}
            icon={opt.count ? <CustomPill sx={{ ml: '6px' }} value={opt.count} showBorder={opt.value === value} /> : opt.icon}
            iconPosition={opt.iconPosition || 'start'}
            disabled={opt.disabled || false}
            {...a11yProps(opt.value)}
            sx={{
              '&.MuiTab-root': {
                display: 'flex !important',
                flexDirection: 'row-reverse',
                alignItems: 'center',
              },
              textTransform: 'capitalize',
              fontWeight: '600',
              borderBottom: disableIndicatorTransition ? `2px solid` : 'none',
              ...(opt.disabled ? { opacity: 0.5 } : {}),
            }}
          />
        ))}
        {rightButton?.visible && (
          <Box sx={{ position: 'absolute', right: '0px', top: '0px' }}>
            <CustomButton
              variant='primary'
              size='Small'
              startIcon={rightButton?.startIcon || <></>}
              onClick={rightButton?.onClick}
              text={rightButton?.text}
            />
          </Box>
        )}
      </Tabs>
      {!showBottomMargin && <Box sx={{ bgcolor: 'transparent', height: '12px' }} />}{' '}
    </Box>
  );
};

CustomTabs.propTypes = {
  value: PropTypes.any,
  onChange: PropTypes.func.isRequired,
  options: PropTypes.arrayOf(
    PropTypes.shape({
      value: PropTypes.number,
      text: PropTypes.string,
      icon: PropTypes.node,
      disabled: PropTypes.bool,
    })
  ),
  smallSize: PropTypes.bool,
  disableIndicatorTransition: PropTypes.bool,
  blueVariant: PropTypes.bool,
  padding: PropTypes.string,
};

export default CustomTabs;
