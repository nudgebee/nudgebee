import { Box, Button, Typography, MenuItem, Popover } from '@mui/material';
import React, { useEffect, useLayoutEffect, useState, useRef } from 'react';
import { useRouter } from 'next/router';
import { Button as DsButton } from '@components1/ds/Button';
import { BetaIcon, MenuArrowDownIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { Chip } from '@components1/ds/Chip';
import { ds } from '@utils/colors';
import CustomTabs from './CustomTabs';
import Link from 'next/link';

const AnchorComponent = ({
  p = `${ds.space.mul(0, 3)} ${ds.space[6]} 0 ${ds.space[6]}`,
  filterOptions = [],
  marginTop = 0,
  onChangeFilter,
  boxShadow = '0px 2px 24px 2px #00000014',
  buttonTitle = '',
  buttonComponent,
  handleButtonAction,
  manageRoute,
  showCustomRounded = false,
  showBorderBottom = false,
  tabPadding = `${ds.space[2]} ${ds.space[3]} 0 ${ds.space[3]}`,
  groupedTabs = false,
  showGroupedTabs = false,
  tooltip = '',
}) => {
  const router = useRouter();
  const [currentOpt, setCurrentOpt] = useState([]);

  // --- FIX: Lazy State Initialization ---
  // Calculates the correct tab immediately based on the URL hash.
  // This prevents the component from starting at "0" and triggering a redirect.
  const getInitialState = () => {
    if (typeof window === 'undefined' || !manageRoute) {
      return { tab: 0, subtab: 0 };
    }

    const hashRaw = window.location.hash || '';
    if (!hashRaw) {
      return { tab: 0, subtab: 0 };
    }

    const hash = decodeURIComponent(hashRaw.replace('#', ''));
    const [parentFrag, childFrag] = hash.split('/');

    if (filterOptions && filterOptions.length > 0) {
      const parent = filterOptions.find((opt) => opt.fragment === parentFrag);
      if (parent) {
        let subtab = 0;
        if (childFrag && parent.tabOptions) {
          const child = parent.tabOptions.find((opt) => opt.fragment === childFrag);
          if (child) {
            subtab = child.value;
          }
        }
        return { tab: parent.value, subtab };
      }
    }
    // If hash exists but options aren't loaded yet, default to 0 safely
    return { tab: 0, subtab: 0 };
  };

  // Initialize state using the function above
  const [activeDropdownTab, setActiveDropdownTab] = useState(() => getInitialState().tab);
  const [activeDropdownSubtab, setActiveDropdownSubtab] = useState(() => getInitialState().subtab);

  const [range, setRange] = useState([0, 3]);

  // Ref to block the initial "onChange" firing if a hash is present
  const isFirstMount = useRef(true);

  // Sliding-pill indicator: measures the selected button's DOM rect and writes
  // it to CSS custom properties on the tabs row. The ::before pseudo on the
  // container renders the pill and animates via `transform: translateX(...)` +
  // `width`, which is GPU-accelerated and matches the CustomTabs primitive.
  const tabsRootRef = useRef(null);
  useLayoutEffect(() => {
    const root = tabsRootRef.current;
    if (!root) return undefined;

    const update = () => {
      const selected = root.querySelector('[data-tab-selected="true"]');
      if (!selected) {
        root.style.setProperty('--at-indicator-width', '0px');
        return;
      }
      const containerRect = root.getBoundingClientRect();
      const tabRect = selected.getBoundingClientRect();
      root.style.setProperty('--at-indicator-x', `${tabRect.left - containerRect.left}px`);
      root.style.setProperty('--at-indicator-width', `${tabRect.width}px`);
    };

    update();

    if (typeof ResizeObserver === 'undefined') return undefined;
    const ro = new ResizeObserver(update);
    ro.observe(root);
    return () => ro.disconnect();
  });

  const isGroupedTabs = groupedTabs || currentOpt?.groupedTab;

  const groupByTabName = (options) => {
    if (!options) {
      return {};
    }
    return options.reduce((acc, option) => {
      const { tabName = 'undefined' } = option;
      if (!acc[tabName]) {
        acc[tabName] = [];
      }
      acc[tabName].push(option);
      return acc;
    }, {});
  };

  const handleClickScroll = (elementId) => {
    const element = document.getElementById(elementId);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth' });
    }
  };

  const [anchorEl, setAnchorEl] = useState(null);

  const handlePopoverOpen = (event, opt) => {
    if (opt) {
      setCurrentOpt(opt);
    }
    if (anchorEl !== event.currentTarget) {
      setAnchorEl(event.currentTarget);
    }
  };

  const handlePopoverClose = () => {
    setCurrentOpt([]);
    setAnchorEl(null);
  };

  const getBaseUrl = () => {
    const currentPath = router.asPath || '';
    const [pathAndQuery] = currentPath.split('#');
    const [path, queryString] = (pathAndQuery || '').split('?');

    const searchParams = new URLSearchParams(queryString);
    searchParams.delete('tab');
    searchParams.delete('subtab');

    return { path, searchParams };
  };

  const getTabUrl = (opt) => {
    const { path, searchParams } = getBaseUrl();
    const hash = opt.fragment || '';
    const cleanQuery = searchParams.toString();
    return `${path}${cleanQuery ? `?${cleanQuery}` : ''}#${hash}`;
  };

  const getTabUrlForDropdown = (opt, tabValue) => {
    const { path, searchParams } = getBaseUrl();
    const parent = filterOptions.find((p) => p.value === tabValue);
    if (!parent) {
      return '#';
    }

    let hash = parent.fragment || '';
    if (opt.fragment) {
      hash = hash ? `${hash}/${opt.fragment}` : opt.fragment;
    }

    const cleanQuery = searchParams.toString();
    return `${path}${cleanQuery ? `?${cleanQuery}` : ''}#${hash}`;
  };

  const getMenuItem = (item, tabValue, anchorActiveTab) => {
    if (item.hidden) return null;
    return (
      <MenuItem
        id={`dropdown-${item.id}`}
        key={item.id}
        component={item.disabled ? 'div' : Link}
        {...(!item.disabled && { href: getTabUrlForDropdown(item, tabValue) })}
        onClick={(e) => {
          if (item.disabled) {
            e.preventDefault();
            return;
          }
          setActiveDropdownTab(currentOpt.value);
          setActiveDropdownSubtab(item.value);
          handlePopoverClose();
        }}
        selected={item.value == activeDropdownSubtab && anchorActiveTab}
        disabled={item.disabled || false}
        sx={{
          padding: 'var(--ds-overlay-item-padding-md)',
          margin: '0 var(--ds-overlay-item-margin-x)',
          borderRadius: 'var(--ds-overlay-item-radius)',
          fontSize: 'var(--ds-text-body)',
          color: 'var(--ds-gray-700)',
          fontWeight: 'var(--ds-font-weight-regular)',
          gap: 'var(--ds-space-2)',
          minWidth: ds.space.mul(0, 94),
          maxWidth: ds.space.mul(0, 120),
          display: 'flex',
          alignItems: 'center',
          textDecoration: 'none',
          transition: 'background var(--ds-motion-micro) var(--ds-motion-ease)',
          '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
          '&.Mui-selected': {
            color: 'var(--ds-blue-600)',
            backgroundColor: 'var(--ds-overlay-item-selected-bg)',
            fontWeight: 'var(--ds-font-weight-medium)',
          },
          '&.Mui-selected:hover': { backgroundColor: 'var(--ds-overlay-item-selected-bg)' },
          ...(item.disabled
            ? {
                opacity: 0.5,
                cursor: 'not-allowed',
                pointerEvents: 'none',
                color: 'var(--ds-gray-500)',
              }
            : {}),
        }}
      >
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', color: 'inherit', flexShrink: 0 }}>
          <SafeIcon src={item?.icon} height={item?.height || 18} width={item?.width || 18} alt={item.text} />
        </Box>
        <Box component='span' sx={{ flex: 1, fontSize: 'var(--ds-text-body)', color: 'inherit' }}>
          {item.text}
        </Box>
      </MenuItem>
    );
  };

  useEffect(() => {
    if (showGroupedTabs && filterOptions[activeDropdownTab]) {
      const options = filterOptions[activeDropdownTab]?.tabOptions || [];
      const totalLength = options.length;

      if (activeDropdownTab === 5) {
        if (activeDropdownSubtab >= 0 && activeDropdownSubtab <= 2) {
          setRange([0, 3]);
        } else if (activeDropdownSubtab >= 3 && activeDropdownSubtab <= 6) {
          setRange([3, 7]);
        } else {
          setRange([0, totalLength]);
        }
      } else if (activeDropdownTab === 4) {
        if (activeDropdownSubtab >= 0 && activeDropdownSubtab <= 1) {
          setRange([0, 2]);
        } else if (activeDropdownSubtab >= 2 && activeDropdownSubtab <= 3) {
          setRange([2, 4]);
        } else if (activeDropdownSubtab >= 4 && activeDropdownSubtab <= 7) {
          setRange([4, 8]);
        } else if (activeDropdownSubtab >= 8 && activeDropdownSubtab <= 9) {
          setRange([8, 10]);
        } else {
          setRange([0, totalLength]);
        }
      } else {
        setRange([0, totalLength]);
      }
    }
  }, [activeDropdownTab, activeDropdownSubtab, filterOptions, showGroupedTabs]);

  // Sync internal state with URL Hash (Reactive Updates)
  useEffect(() => {
    if (manageRoute) {
      const hashRaw = window.location.hash || '';
      const hash = decodeURIComponent(hashRaw.replace('#', ''));
      const [parentFrag, childFrag] = hash.split('/');

      if (filterOptions.length > 0 && hash) {
        const parent = filterOptions.find((opt) => opt.fragment === parentFrag);
        if (parent) {
          // Only update if strictly different to avoid loop
          if (activeDropdownTab !== parent.value) {
            setActiveDropdownTab(parent.value);
          }

          let newSub = 0;
          if (childFrag && parent.tabOptions) {
            const child = parent.tabOptions.find((opt) => opt.fragment === childFrag);
            if (child) {
              newSub = child.value;
            }
          }

          if (activeDropdownSubtab !== newSub) {
            setActiveDropdownSubtab(newSub);
          }
        }
      } else {
        setActiveDropdownTab(getInitialState().tab);
        setActiveDropdownSubtab(getInitialState().subtab);
      }
    }
  }, [router.asPath, filterOptions, manageRoute]);

  // Trigger Parent onChange callback
  useEffect(() => {
    if (isFirstMount.current) {
      isFirstMount.current = false;
      // If managing route and hash exists, DO NOT fire the initial callback.
      // This prevents overwriting the URL on load.
      if (manageRoute && window.location.hash && window.location.hash !== '#') {
        return;
      }
    }

    if (onChangeFilter && activeDropdownTab !== undefined) {
      onChangeFilter(activeDropdownTab, activeDropdownSubtab || 0, {
        tab: activeDropdownTab,
        subtab: activeDropdownSubtab || 0,
      });
    }
  }, [activeDropdownSubtab, activeDropdownTab]);

  return (
    <>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          backgroundColor: 'var(--ds-background-100)',
          p,
          boxShadow,
          mt: marginTop,
          position: 'absolute',
          left: 0,
          right: 0,
          zIndex: 0,
          borderTop: '0.5px solid var(--ds-gray-200)',
        }}
        onMouseLeave={handlePopoverClose}
      >
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            overflowX: 'auto',
            height: '100%',
            scrollbarWidth: 'thin',
            '&::-webkit-scrollbar': {
              paddingTop: 'var(--ds-space-2)',
              width: '0.4em',
              height: ds.space[1],
            },
            '&::-webkit-scrollbar-track': {
              boxShadow: 'inset 0 0 6px rgba(0,0,0,0.00)',
              webkitBoxShadow: 'inset 0 0 6px rgba(0,0,0,0.00)',
            },
            '&::-webkit-scrollbar-thumb': {
              backgroundColor: 'rgba(0,0,0,.1)',
            },
          }}
        >
          <Box>
            {currentOpt?.tabOptions && (
              <Popover
                sx={{
                  zIndex: 10,
                  '& .MuiPopover-paper': {
                    backgroundColor: 'var(--ds-overlay-bg)',
                    borderRadius: 'var(--ds-overlay-radius)',
                    border: 'none',
                    boxShadow: 'var(--ds-overlay-shadow)',
                    overflow: 'hidden',
                    marginTop: 'var(--ds-overlay-anchor-gap)',
                    padding: 'var(--ds-overlay-padding-y) 0',
                    animation: 'overlaySurfaceEnter var(--ds-overlay-enter-duration) var(--ds-overlay-enter-easing)',
                    '@keyframes overlaySurfaceEnter': {
                      '0%': { opacity: 0, transform: `scaleY(0.9) translateY(${ds.space.mul(0, -4)})` },
                      '100%': { opacity: 1, transform: 'scaleY(1) translateY(0)' },
                    },
                  },
                }}
                id='mouse-over-popover'
                anchorEl={anchorEl}
                open={Boolean(anchorEl)}
                onClose={handlePopoverClose}
                anchorOrigin={{
                  vertical: 'bottom',
                  horizontal: 'left',
                }}
                transformOrigin={{
                  vertical: 'top',
                  horizontal: 'left',
                }}
              >
                <Box onMouseLeave={handlePopoverClose}>
                  {showGroupedTabs ? (
                    <Box
                      sx={{
                        display: isGroupedTabs && 'grid',
                        gridTemplateColumns: isGroupedTabs && '1fr 1fr',
                        columnGap: isGroupedTabs && ds.space[3],
                        maxWidth: isGroupedTabs ? ds.space.mul(0, 245) : '100%',
                      }}
                    >
                      {Object.entries(groupByTabName(currentOpt.tabOptions)).map(([tabName, options]) => (
                        <Box key={tabName}>
                          {tabName && tabName !== 'undefined' && (
                            <Typography
                              sx={{
                                padding: 'var(--ds-space-2) var(--ds-space-3) var(--ds-space-1)',
                                fontSize: 'var(--ds-text-caption)',
                                color: 'var(--ds-gray-700)',
                                fontWeight: 'var(--ds-font-weight-semibold)',
                                textTransform: 'uppercase',
                                letterSpacing: '0.02em',
                              }}
                            >
                              {tabName}
                            </Typography>
                          )}
                          {options.map((item) => getMenuItem(item, currentOpt.value, currentOpt.value === activeDropdownTab))}
                        </Box>
                      ))}
                    </Box>
                  ) : (
                    currentOpt.tabOptions.map((item) => getMenuItem(item, currentOpt.value, currentOpt.value === activeDropdownTab))
                  )}
                </Box>
              </Popover>
            )}
            {!!filterOptions.length && (
              <Box
                ref={tabsRootRef}
                sx={{
                  position: 'relative',
                  display: 'flex',
                  alignItems: 'flex-start',
                  gap: 'var(--ds-space-1)',
                  paddingBottom: 'var(--ds-space-1)',
                  '&::before': {
                    content: '""',
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    height: ds.space.mul(0, 17),
                    width: 'var(--at-indicator-width, 0px)',
                    transform: 'translate3d(var(--at-indicator-x, 0px), 0, 0)',
                    backgroundColor: 'var(--ds-blue-200)',
                    borderRadius: 'var(--ds-radius-md)',
                    transition: 'transform 280ms cubic-bezier(0.2, 0.8, 0.2, 1), width 280ms cubic-bezier(0.2, 0.8, 0.2, 1)',
                    willChange: 'transform, width',
                    zIndex: 0,
                    pointerEvents: 'none',
                  },
                  '&::after': {
                    content: '""',
                    position: 'absolute',
                    bottom: 0,
                    left: 0,
                    height: ds.space[0],
                    width: 'var(--at-indicator-width, 0px)',
                    transform: 'translate3d(var(--at-indicator-x, 0px), 0, 0)',
                    backgroundColor: 'var(--ds-brand-500)',
                    borderRadius: 'var(--ds-radius-sm)',
                    transition: 'transform 280ms cubic-bezier(0.2, 0.8, 0.2, 1), width 280ms cubic-bezier(0.2, 0.8, 0.2, 1)',
                    willChange: 'transform, width',
                    zIndex: 2,
                    pointerEvents: 'none',
                  },
                }}
              >
                {filterOptions.map((opt, _idx) => {
                  if (opt.hidden) return null;
                  const selected = activeDropdownTab === opt.value;
                  return (
                    <Box key={opt?.name} display='flex' flexDirection='column' zIndex={1} position='relative'>
                      <Button
                        component={Link}
                        href={getTabUrl(opt)}
                        disableRipple
                        disableFocusRipple
                        data-tab-selected={selected ? 'true' : 'false'}
                        onClick={() => {
                          setActiveDropdownTab(currentOpt.value);
                          setActiveDropdownSubtab(0);
                        }}
                        id={`anchor-tab-${opt?.id || opt?.name}`}
                        sx={{
                          position: 'relative',
                          width: 'max-content',
                          textTransform: 'none',
                          cursor: 'pointer',
                          fontFamily: 'var(--ds-font-display)',
                          fontSize: 'var(--ds-text-small)',
                          lineHeight: 1,
                          fontWeight: selected ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-regular)',
                          height: ds.space.mul(0, 17),
                          padding: `0 ${ds.space[2]}`,
                          borderRadius: 'var(--ds-radius-md)',
                          backgroundColor: 'transparent',
                          color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-700)',
                          transition: 'color 200ms ease, background-color 200ms ease',
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 'var(--ds-space-1)',
                          '& .tab-icon': {
                            width: ds.space.mul(0, 11),
                            height: ds.space.mul(0, 11),
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            color: selected ? 'var(--ds-gray-700)' : 'var(--ds-gray-600)',
                            filter: selected ? 'grayscale(1) brightness(0.45)' : 'grayscale(1) brightness(0.85)',
                            transition: 'filter 200ms ease, color 200ms ease',
                            '& img': { objectFit: 'contain' },
                            '& svg': { maxWidth: '100%', maxHeight: '100%' },
                            '& svg path, & svg circle, & svg rect, & svg polygon, & svg line': {
                              stroke: selected ? 'var(--ds-gray-700)' : 'var(--ds-gray-600)',
                            },
                          },
                          '&:hover .arrow-icon': { transform: 'rotate(180deg)' },
                          '& .arrow-icon': {
                            transition: 'transform 0.3s ease',
                            color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-500)',
                          },
                          '&:hover:not([data-tab-selected="true"])': {
                            backgroundColor: 'var(--ds-gray-100)',
                            color: 'var(--ds-brand-700)',
                          },
                          '&:hover': {
                            // selected tab keeps the sliding pill as its visual; no hover bg
                            backgroundColor: 'transparent',
                          },
                          '&:focus-visible': {
                            outline: 'none',
                            boxShadow: '0 0 0 3px var(--ds-blue-100)',
                          },
                          '&.Mui-disabled': { opacity: 0.5, pointerEvents: 'none' },
                        }}
                        disabled={opt.disabled || false}
                        aria-owns={anchorEl ? 'mouse-over-popover' : undefined}
                        aria-haspopup='true'
                        aria-current={selected ? 'page' : undefined}
                        onMouseOver={(e) => {
                          handlePopoverOpen(e, opt);
                        }}
                      >
                        <SafeIcon
                          src={opt.icon}
                          alt={opt.name}
                          className='tab-icon'
                          {...(opt.iconSize && { width: opt.iconSize, height: opt.iconSize, style: { width: opt.iconSize, height: opt.iconSize } })}
                        />
                        <Box display={'inline-flex'} alignItems={'center'} gap={'var(--ds-space-2)'}>
                          <span>{opt.name}</span>
                          {opt.count && (
                            <Chip variant='count' size='xs' tone={selected ? 'info' : 'neutral'}>
                              {opt.count > 99 ? '99+' : opt.count}
                            </Chip>
                          )}
                        </Box>
                        {opt.betaIcon && (
                          <SafeIcon
                            src={BetaIcon}
                            alt='Beta icon'
                            style={{ height: ds.space.mul(0, 10), width: ds.space.mul(0, 10), marginTop: ds.space.mul(0, -5) }}
                          />
                        )}
                        {opt.tabOptions && (
                          <SafeIcon
                            src={MenuArrowDownIcon}
                            alt='down arrow'
                            className='arrow-icon'
                            style={{ height: ds.space[4], width: ds.space[4] }}
                          />
                        )}
                      </Button>
                    </Box>
                  );
                })}
              </Box>
            )}
          </Box>
          {buttonTitle && (
            <Box sx={{ mr: 'var(--ds-space-5)' }}>
              <DsButton onClick={handleButtonAction} size='lg' tone='primary'>
                {buttonTitle}
              </DsButton>
            </Box>
          )}
          {buttonComponent}
        </Box>
      </Box>

      {filterOptions?.[activeDropdownTab]?.options?.length > 0 ? (
        <Box mt={ds.space.mul(0, 32)} position={'relative'} mb={ds.space[2]}>
          <Box
            sx={{
              display: 'flex',
              backgroundColor: 'var(--ds-background-100)',
              p: 'var(--ds-space-2) var(--ds-space-5) 0 var(--ds-space-5)',
              boxShadow: '0 1px 2px rgba(16,24,40,0.04)',
              alignItems: 'center',
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-md)',
              gap: 'var(--ds-space-2)',
            }}
          >
            {filterOptions[activeDropdownTab]?.options.map((opt, _idx) => (
              <React.Fragment key={opt.id}>
                {_idx > 0 && <Box sx={{ height: ds.space[4], width: '1px', backgroundColor: 'var(--ds-gray-200)' }} />}
                <Button
                  onClick={() => handleClickScroll(opt.id)}
                  sx={{
                    textTransform: 'unset',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: 'var(--ds-gray-700)',
                    px: 'var(--ds-space-3)',
                    py: 'var(--ds-space-2)',
                    mb: 'var(--ds-space-2)',
                    minHeight: 0,
                    minWidth: 0,
                    borderRadius: 'var(--ds-radius-sm)',
                    transition: 'color 120ms ease, background-color 120ms ease',
                    '&:hover': {
                      color: 'var(--ds-gray-700)',
                      backgroundColor: 'var(--ds-brand-100)',
                    },
                  }}
                >
                  {opt.name}
                </Button>
              </React.Fragment>
            ))}
          </Box>
        </Box>
      ) : filterOptions[activeDropdownTab]?.tabOptions?.length > 0 ? (
        <Box mt={ds.space.mul(0, 32)} position={'relative'} mb={ds.space[4]}>
          <CustomTabs
            options={{
              ...filterOptions[activeDropdownTab],
              tabOptions: showGroupedTabs
                ? filterOptions[activeDropdownTab]?.tabOptions?.slice(range[0], range[1])
                : filterOptions[activeDropdownTab]?.tabOptions?.map((opt) => ({
                    ...opt,
                    showBottomMargin: true,
                  })),
            }}
            value={activeDropdownSubtab}
            smallSize={true}
            onChange={(v) => {
              setActiveDropdownSubtab(v);
            }}
            showBorderBottom={showBorderBottom}
            showCustomRounded={showCustomRounded}
            p={tabPadding}
            showGroupedTabs={showGroupedTabs}
            tooltip={tooltip}
          />
        </Box>
      ) : (
        <Box mt={ds.space.mul(0, 32)} mb={ds.space[2]} position={'relative'} />
      )}
    </>
  );
};

AnchorComponent.propTypes = {
  p: PropTypes.string,
  filterOptions: PropTypes.array,
  marginTop: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  onChangeFilter: PropTypes.func,
  boxShadow: PropTypes.string,
  buttonTitle: PropTypes.string,
  handleButtonAction: PropTypes.func,
  buttonComponent: PropTypes.element,
  manageRoute: PropTypes.bool,
  showCustomRounded: PropTypes.bool,
  showBorderBottom: PropTypes.bool,
  tabPadding: PropTypes.string,
  groupedTabs: PropTypes.bool,
  showGroupedTabs: PropTypes.bool,
  tooltip: PropTypes.string,
};

export default AnchorComponent;
