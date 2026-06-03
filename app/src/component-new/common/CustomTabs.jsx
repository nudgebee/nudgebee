/**
 * @deprecated Use `Tabs` from '@components1/ds/Tabs' with `navigation="router"` instead.
 * V2 absorbs this + CustomTabsForDrilldown + ButtonTabs into one primitive with
 * visual (underline/segmented), navigation (state/router), overflow (scroll/more-menu).
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
import React, { useEffect, useLayoutEffect, useRef } from 'react';
import { Box, Tab, Tabs, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Chip } from '@components1/ds/Chip';
import { ds } from '@utils/colors';

let _customTabsWarned = false;
import SafeIcon from '@components1/common/SafeIcon';
import { BetaIcon } from '@assets';
import { useRouter } from 'next/router';
import Link from 'next/link';
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
  smallSize: _smallSize = false,
  disableIndicatorTransition: _disableIndicatorTransition = false,
  blueVariant: _blueVariant = false,
  showBorderBottom = false,
  showCustomRounded = false,
  p: _p = `${ds.space[2]} 0px 0px ${ds.space[2]}`,
  showGroupedTabs = false,
  tooltip: _tooltip = '',
  variant = 'secondary', // 'primary' or 'secondary'
  behavior = 'router', // 'router' or 'filter' - determines if tabs navigate or just filter
  ariaLabel = 'basic tabs example',
  showSurface = true, // wrap tabs in a container surface (bg + border). Pass false to render naked.
}) => {
  useEffect(() => {
    _warnCustomTabs();
  }, []);

  const tabsRootRef = useRef(null);

  // Custom sliding indicator: measures the selected tab's DOM rect and writes
  // it to CSS custom properties on the flexContainer. The ::before pseudo (in
  // sx below) renders the pill and animates via `transform: translateX(...)` +
  // `width`, which is GPU-accelerated and smoother than MUI's default left/width.
  useLayoutEffect(() => {
    const root = tabsRootRef.current;
    if (!root) return undefined;

    const flex = root.querySelector('.MuiTabs-flexContainer');
    if (!flex) return undefined;

    const update = () => {
      const selected = flex.querySelector('.MuiTab-root.Mui-selected');
      if (!selected) return;
      const containerRect = flex.getBoundingClientRect();
      const tabRect = selected.getBoundingClientRect();
      flex.style.setProperty('--ct-indicator-x', `${tabRect.left - containerRect.left}px`);
      flex.style.setProperty('--ct-indicator-width', `${tabRect.width}px`);
    };

    update();

    if (typeof ResizeObserver === 'undefined') return undefined;
    const ro = new ResizeObserver(update);
    ro.observe(flex);
    return () => ro.disconnect();
  });

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

  const inGroupedMode = showGroupedTabs && options?.tabOptions?.[0]?.tabName;
  const isFilter = behavior === 'filter';

  const cellHeight = variant === 'primary' ? ds.space.mul(0, 17) : ds.space.mul(0, 15);
  const iconBoxSize = variant === 'primary' ? ds.space.mul(0, 10) : ds.space.mul(0, 9);

  const showLeftLine = variant === 'primary';

  const tabCellSx = {
    position: 'relative',
    zIndex: 1, // keep label above the sliding indicator pill
    minHeight: 0,
    minWidth: 0,
    width: 'max-content',
    height: cellHeight,
    py: 0,
    px: 'var(--ds-space-2)',
    gap: 'var(--ds-space-1)',
    fontFamily: 'var(--ds-font-display)',
    fontSize: 'var(--ds-text-caption)',
    fontWeight: 'var(--ds-font-weight-regular)',
    lineHeight: 1,
    borderRadius: 'var(--ds-radius-md)',
    color: 'var(--ds-gray-700)',
    backgroundColor: 'transparent',
    textTransform: 'none',
    transition: 'color 200ms ease, background-color 200ms ease',
    '& .tab-icon': {
      width: iconBoxSize,
      height: iconBoxSize,
      display: 'inline-flex',
      alignItems: 'center',
      justifyContent: 'center',
      color: 'var(--ds-gray-600)',
      filter: 'grayscale(1) brightness(0.85)',
      transition: 'filter 200ms ease, color 200ms ease',
      '& img': { objectFit: 'contain' },
      '& svg': { maxWidth: '100%', maxHeight: '100%' },
      '& svg path, & svg circle, & svg rect, & svg polygon, & svg line': {
        stroke: 'var(--ds-gray-600)',
      },
    },
    '&:hover:not(.Mui-selected)': {
      // soft hover on idle tabs; selected tab is highlighted by the sliding pill, no hover needed
      backgroundColor: 'var(--ds-gray-100)',
      color: 'var(--ds-brand-700)',
    },
    '&.Mui-selected': {
      color: 'var(--ds-brand-700)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      '& .tab-icon': {
        color: 'var(--ds-gray-700)',
        filter: 'grayscale(1) brightness(0.45)',
        '& svg path, & svg circle, & svg rect, & svg polygon, & svg line': {
          stroke: 'var(--ds-gray-700)',
        },
      },
    },
    '&:focus-visible': {
      outline: 'none',
      boxShadow: '0 0 0 3px var(--ds-blue-100)',
    },
  };

  const containerSx = inGroupedMode
    ? {
        bgcolor: isFilter ? 'transparent' : 'var(--ds-background-100)',
        border: 'none',
        boxShadow: 'none',
        borderRadius: showCustomRounded ? `0px ${ds.radius.lg} 0px 0px` : `0px ${ds.radius.lg} ${ds.radius.lg} 0px`,
        padding: 'var(--ds-space-1) var(--ds-space-2)',
        width: '100%',
      }
    : showSurface
    ? {
        bgcolor: 'var(--ds-background-100)',
        border: '1px solid var(--ds-gray-200)',
        boxShadow: '0 1px 2px rgba(16,24,40,0.04)',
        borderRadius: 'var(--ds-radius-lg)',
        padding: 'var(--ds-space-1) var(--ds-space-2)',
        width: '100%',
      }
    : {
        bgcolor: 'transparent',
        border: 'none',
        boxShadow: 'none',
        padding: 0,
        width: '100%',
      };

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        ...(inGroupedMode &&
          !isFilter && {
            border: '1px solid var(--ds-gray-200)',
            boxShadow: '0 1px 2px rgba(16,24,40,0.04)',
            borderRadius: showCustomRounded ? `${ds.radius.lg} ${ds.radius.lg} 0px 0px` : 'var(--ds-radius-lg)',
            bgcolor: 'var(--ds-background-100)',
            overflow: 'hidden',
          }),
      }}
    >
      {inGroupedMode && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            alignSelf: 'stretch',
            gap: 'var(--ds-space-2)',
            bgcolor: 'var(--ds-background-100)',
            px: 'var(--ds-space-4)',
            paddingTop: 'var(--ds-space-1)',
            borderBottom: showBorderBottom && '1px solid var(--ds-gray-200)',
            borderRight: '1px solid var(--ds-gray-200)',
            flexShrink: 0,
          }}
        >
          <Typography
            sx={{
              color: 'var(--ds-gray-700)',
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              lineHeight: 1,
              textTransform: 'capitalize',
              whiteSpace: 'nowrap',
            }}
          >
            {options?.tabOptions[0]?.tabName}
          </Typography>
          <KeyboardArrowRightRoundedIcon sx={{ color: 'var(--ds-gray-500)', fontSize: 16 }} />
        </Box>
      )}
      {options?.tabOptions?.length > 0 && (
        <Box sx={containerSx}>
          <Tabs
            ref={tabsRootRef}
            value={value}
            onChange={(_event, newValue) => onChange(newValue)}
            aria-label={ariaLabel}
            variant='scrollable'
            TabIndicatorProps={{ style: { display: 'none' } }}
            sx={{
              minHeight: 0,
              p: 0,
              '& .MuiTabs-scrollButtons': {
                paddingBottom: 'var(--ds-space-2)',
                '& svg': {
                  fontSize: 'var(--ds-text-heading)',
                },
              },
              '& .MuiTabs-scrollButtons.Mui-disabled': {
                opacity: 0.3,
                paddingBottom: 'var(--ds-space-2)',
              },
              '& .MuiTabs-flexContainer': {
                position: 'relative',
                gap: 'var(--ds-space-1)',
                alignItems: 'flex-start',
                ...(showLeftLine && { paddingBottom: 'var(--ds-space-1)' }),
                '& .MuiTab-root': tabCellSx,
                '&::before': {
                  content: '""',
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  height: cellHeight,
                  width: 'var(--ct-indicator-width, 0px)',
                  transform: 'translate3d(var(--ct-indicator-x, 0px), 0, 0)',
                  backgroundColor: 'var(--ds-blue-200)',
                  borderRadius: 'var(--ds-radius-md)',
                  transition: 'transform 280ms cubic-bezier(0.2, 0.8, 0.2, 1), width 280ms cubic-bezier(0.2, 0.8, 0.2, 1)',
                  willChange: 'transform, width',
                  zIndex: 0,
                  pointerEvents: 'none',
                },
                ...(showLeftLine && {
                  '&::after': {
                    content: '""',
                    position: 'absolute',
                    bottom: 0,
                    left: 0,
                    height: ds.space[0],
                    width: 'var(--ct-indicator-width, 0px)',
                    transform: 'translate3d(var(--ct-indicator-x, 0px), 0, 0)',
                    backgroundColor: 'var(--ds-brand-600)',
                    borderRadius: 'var(--ds-radius-sm)',
                    transition: 'transform 280ms cubic-bezier(0.2, 0.8, 0.2, 1), width 280ms cubic-bezier(0.2, 0.8, 0.2, 1)',
                    willChange: 'transform, width',
                    zIndex: 2,
                    pointerEvents: 'none',
                  },
                }),
              },
              '& .MuiTabs-indicator': { display: 'none' },
            }}
          >
            {options.tabOptions
              ?.filter((opt) => !opt.hidden)
              .map((opt, _idx) => (
                <Tab
                  key={opt.value}
                  disableRipple
                  disableFocusRipple
                  label={
                    <Box display='flex' alignItems='center' gap={ds.space.mul(0, 3)}>
                      {opt.icon && (
                        <SafeIcon
                          src={opt.icon}
                          alt={opt.text}
                          className='tab-icon'
                          {...(opt.iconSize && {
                            width: opt.iconSize,
                            height: opt.iconSize,
                            style: { width: opt.iconSize, height: opt.iconSize },
                          })}
                        />
                      )}
                      <span>{opt.text}</span>
                      {opt.trailingIcon && (
                        <Box
                          component='span'
                          sx={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            color: opt.value === value ? 'var(--ds-brand-700)' : 'var(--ds-gray-500)',
                            '& svg': { fontSize: 16 },
                          }}
                        >
                          {opt.trailingIcon}
                        </Box>
                      )}
                      {opt.betaIcon && (
                        <Box
                          component='span'
                          sx={{
                            display: 'inline-flex',
                            marginTop: ds.space.mul(0, -5),
                          }}
                        >
                          <SafeIcon src={BetaIcon} alt='Beta icon' style={{ height: ds.space.mul(0, 10), width: ds.space.mul(0, 12) }} />
                        </Box>
                      )}
                    </Box>
                  }
                  component={behavior === 'router' ? Link : 'button'}
                  value={opt.value}
                  icon={
                    opt.count ? (
                      <Chip variant='count' size='xs' tone={opt.value === value ? 'info' : 'neutral'}>
                        {opt.count > 99 ? '99+' : opt.count}
                      </Chip>
                    ) : null
                  }
                  iconPosition={opt.iconPosition || 'start'}
                  disabled={opt.disabled || false}
                  {...a11yProps(opt.value, opt.id)}
                  {...(behavior === 'router' ? { href: getTabUrl(opt), scroll: false } : {})}
                  sx={{
                    '&.MuiTab-root': {
                      display: 'inline-flex !important',
                      flexDirection: 'row-reverse',
                      alignItems: 'center',
                    },
                    textTransform: 'none',
                    ...(opt.disabled ? { opacity: 0.5 } : {}),
                  }}
                />
              ))}
          </Tabs>
        </Box>
      )}
      {!showBottomMargin && <Box sx={{ bgcolor: 'transparent', height: ds.space[3] }} />}{' '}
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
  showSurface: PropTypes.bool,
};

// Each option in `options.tabOptions` may now declare a `trailingIcon`
// (React node) — rendered after the label, used by primary-variant tabs
// that open a dropdown (matches the chevron pattern in the screenshot).

export default CustomTabs;
