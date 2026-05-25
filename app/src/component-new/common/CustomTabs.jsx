/**
 * @deprecated Use `Tabs` from '@components1/ds/Tabs' with `navigation="router"` instead.
 * V2 absorbs this + CustomTabsForDrilldown + ButtonTabs into one primitive with
 * visual (underline/segmented), navigation (state/router), overflow (scroll/more-menu).
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
import React, { useEffect } from 'react';
import { Box, Tab, Tabs, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import CustomPill from './CustomPill';

let _customTabsWarned = false;
import SafeIcon from '@components1/common/SafeIcon';
import { BetaIcon } from '@assets';
import { useRouter } from 'next/router';
import Link from 'next/link';
import { colors } from 'src/utils/colors';
import KeyboardArrowRightRoundedIcon from '@mui/icons-material/KeyboardArrowRightRounded';

function a11yProps(index, customId) {
  return {
    id: customId || `tab-${index}`,
    'aria-controls': `simple-tabpanel-${index}`,
  };
}

function _warnCustomTabs() {
  if (_customTabsWarned) return;
  _customTabsWarned = true;
  // eslint-disable-next-line no-console
  console.warn(
    '[deprecated] CustomTabs is deprecated. Use `import { Tabs } from "@components1/ds/Tabs"` with navigation="router" instead. ' +
      'Tracked for removal 2026-06-06.'
  );
}

const CustomTabs = ({
  value,
  onChange,
  options = {},
  smallSize = false,
  disableIndicatorTransition = false,
  blueVariant = false,
  showBorderBottom = false,
  showCustomRounded = false,
  p = '8px 0px 0px 8px',
  showGroupedTabs = false,
  tooltip = '',
  variant = 'secondary', // 'primary' or 'secondary'
  behavior = 'router', // 'router' or 'filter' - determines if tabs navigate or just filter
  ariaLabel = 'basic tabs example',
}) => {
  useEffect(() => {
    _warnCustomTabs();
  }, []);

  const router = useRouter();
  const selectedOption = options?.tabOptions?.find((opt) => opt.value === value);
  const showBottomMargin = selectedOption?.showBottomMargin || false;

  const getTabUrl = (opt) => {
    // 1. Get the current resolved path (e.g., "/kubernetes/details/123?tab=1")
    // Split to separate path/query from the existing hash
    const [pathAndQuery] = router.asPath.split('#');

    // 2. Separate Path from Query String
    const [path, queryString] = pathAndQuery.split('?');

    // 3. Clean up the query params (remove old 'tab'/'subtab' params)
    const searchParams = new URLSearchParams(queryString);
    searchParams.delete('tab');
    searchParams.delete('subtab');

    // Ensure accountId is preserved if it exists in router query but not in URL yet
    if (router.query.accountId && !searchParams.has('accountId')) {
      searchParams.set('accountId', router.query.accountId);
    }

    // 4. Construct the new Fragment (Hash)
    const parentFragment = options?.fragment || '';
    const childFragment = opt?.fragment || '';

    let hash = parentFragment;
    if (childFragment) {
      hash = hash ? `${hash}/${childFragment}` : childFragment;
    }

    // 5. Reassemble the URL string
    // Returns: "/kubernetes/details/123?accountId=xyz#optimize/right-sizing"
    const cleanQuery = searchParams.toString();
    return `${path}${cleanQuery ? `?${cleanQuery}` : ''}#${hash}`;
  };

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
      }}
    >
      {options?.tabOptions?.[0]?.tabName && showGroupedTabs && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            alignSelf: 'stretch',
            gap: '6px',
            bgcolor: colors.background.white,
            px: '20px',
            paddingTop: '5px',
            borderBottom: showBorderBottom && `1px solid ${colors.border.secondaryLight}`,
            borderRadius: showCustomRounded ? '8px 0px 0px 0px' : '8px 0px 0px 8px',
            borderRight: `1px solid ${colors.border.secondaryLight}`,
            flexShrink: 0,
          }}
        >
          <Typography
            sx={{
              color: colors.text.tabGroupLabel,
              fontSize: '13px',
              fontWeight: 600,
              lineHeight: 1,
              textTransform: 'capitalize',
              whiteSpace: 'nowrap',
            }}
          >
            {options?.tabOptions[0]?.tabName}
          </Typography>
          <KeyboardArrowRightRoundedIcon sx={{ color: colors.text.tabGroupArrow, fontSize: '16px' }} />
        </Box>
      )}
      {options?.tabOptions?.length > 0 && (
        <Tabs
          value={value}
          onChange={(_event, newValue) => onChange(newValue)}
          aria-label={ariaLabel}
          indicatorColor='primary'
          variant='scrollable'
          sx={{
            minHeight: 0,
            bgcolor: behavior === 'filter' ? 'transparent' : colors.background.white,
            p: showGroupedTabs && options?.tabOptions?.[0]?.tabName ? '8px 32px 0px 20px' : p,
            borderBottom: showBorderBottom && `1px solid ${colors.border.secondaryLight}`,
            border: behavior === 'filter' ? 'none' : `1px solid ${colors.border.secondaryLight}`,
            boxShadow: behavior === 'filter' ? 'none' : colors.shadow.tab,
            borderRadius: showGroupedTabs && options?.tabOptions?.[0]?.tabName ? '0px' : showCustomRounded ? '8px 8px 0px 0px' : '8px',
            width: '100%',
            '& .MuiTabs-scrollButtons': {
              paddingBottom: '8px',
              '& svg': {
                fontSize: '24px',
              },
            },
            '& .MuiTabs-scrollButtons.Mui-disabled': {
              opacity: 0.3,
              paddingBottom: '8px',
            },
            '& .MuiTabs-flexContainer': {
              gap: smallSize ? '6px' : '8px',
              '& .MuiTab-root': {
                minHeight: 0,
                minWidth: 0,
                p: 0,
                px: smallSize ? '8px' : '12px',
                py: smallSize ? '5px' : '8px',
                fontSize: smallSize ? '12px' : '13px',
                borderRadius: smallSize ? '2px' : '4px',
                color: colors.text.secondary,
                borderColor: 'transparent',
                backgroundColor: variant === 'secondary' ? colors.background.white : colors.background.primaryLightest,
                fontWeight: 400,
                mb: '5px',

                '&.Mui-selected': {
                  color: variant === 'secondary' ? colors.text.secondary : colors.text.white,
                  bgcolor:
                    variant === 'secondary'
                      ? colors.background.white
                      : blueVariant
                      ? colors.background.activeAnchorButton
                      : colors.background.tabPrimarySelected,
                  fontWeight: variant === 'secondary' ? 500 : 400,
                },
              },
            },
            '& .MuiTabs-indicator': {
              backgroundColor: blueVariant ? colors.background.primary : colors.background.tabPrimarySelected,
              height: '1.5px',
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
          {options.tabOptions
            ?.filter((opt) => !opt.hidden)
            .map((opt, _idx) => (
              <Tab
                key={opt.value}
                label={
                  <Box display='flex' alignItems='center' gap='6px'>
                    {opt.icon && (
                      <Box
                        sx={{
                          display: 'inline-flex',
                          '& img, & svg': {
                            filter: opt.value === value && variant === 'primary' ? 'brightness(0) invert(1)' : 'none',
                            transition: 'filter 0.25s ease',
                          },
                        }}
                      >
                        <SafeIcon src={opt.icon} alt={opt.text} height={opt?.height || 18} width={opt?.width || 18} />
                      </Box>
                    )}
                    <span>{opt.text}</span>
                    {opt.betaIcon && (
                      <Box
                        component='span'
                        sx={{
                          display: 'inline-flex',
                          marginTop: '-10px',
                        }}
                      >
                        <SafeIcon src={BetaIcon} alt='Beta icon' style={{ height: '20px', width: '25px' }} />
                      </Box>
                    )}
                  </Box>
                }
                component={behavior === 'router' ? Link : 'button'}
                value={opt.value}
                icon={opt.count ? <CustomPill sx={{ ml: '6px' }} value={opt.count} showBorder={opt.value === value} tooltip={tooltip} /> : null}
                iconPosition={opt.iconPosition || 'start'}
                disabled={opt.disabled || false}
                {...a11yProps(opt.value, opt.id)}
                {...(behavior === 'router' ? { href: getTabUrl(opt), scroll: false } : {})}
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
        </Tabs>
      )}
      {!showBottomMargin && <Box sx={{ bgcolor: 'transparent', height: '12px' }} />}{' '}
    </Box>
  );
};

CustomTabs.propTypes = {
  value: PropTypes.any,
  onChange: PropTypes.func.isRequired,
  options: PropTypes.object.isRequired,
  smallSize: PropTypes.bool,
  disableIndicatorTransition: PropTypes.bool,
  blueVariant: PropTypes.bool,
  showBorderBottom: PropTypes.bool,
  p: PropTypes.string,
  showGroupedTabs: PropTypes.bool,
  showCustomRounded: PropTypes.bool,
  tooltip: PropTypes.string,
  variant: PropTypes.oneOf(['primary', 'secondary']),
  behavior: PropTypes.oneOf(['router', 'filter']),
  ariaLabel: PropTypes.string,
};

export default CustomTabs;
