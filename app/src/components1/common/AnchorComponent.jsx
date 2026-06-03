import { Box, Button, Typography, MenuItem, Popover } from '@mui/material';
import React, { useEffect, useState, useRef } from 'react';
import { useRouter } from 'next/router';
import { Button as DsButton } from '@components1/ds/Button';
import { BetaIcon, MenuArrowDownIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import CustomPill from './CustomPill';
import CustomTabs from './CustomTabs';
import Link from 'next/link';
import { ds } from '@utils/colors';

const AnchorComponent = ({
  p = '8px 32px 0px 32px',
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
  tabPadding = '8px 12px 0px 12px',
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

  const isSelected = (id) => activeDropdownTab == id;

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
          p: 'var(--ds-space-1) var(--ds-space-3) !important',
          m: showGroupedTabs && `${ds.space[1]} ${ds.space.mul(0, 3)} !important`,
          borderRadius: 'var(--ds-radius-md)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          fontSize: 'var(--ds-text-body-lg)',
          color: ds.gray[700],
          letterSpacing: '-1%',
          minWidth: ds.space.mul(0, 94),
          maxWidth: ds.space.mul(0, 120),
          ...(item.disabled
            ? {
                opacity: 0.5,
                cursor: 'not-allowed',
                pointerEvents: 'none',
                color: ds.gray[400],
              }
            : {
                '&:hover': {
                  backgroundColor: ds.blue[100],
                  p: { color: ds.blue[500] },
                },
              }),
          '&.Mui-selected': {
            color: ds.blue[500],
            background: ds.blue[100],
            fontWeight: 'var(--ds-font-weight-medium)',
          },
          '& img': { mr: 'var(--ds-space-2)' },
        }}
      >
        <SafeIcon src={item?.icon} height={item?.height || 20} width={item?.width || 20} alt={item.text} />
        <Typography
          marginLeft={ds.space[2]}
          textAlign='left'
          color='inherit'
          fontSize={ds.text.bodyLg}
          display='flex'
          alignItems='center'
          gap={ds.space[0]}
        >
          {item.text}
        </Typography>
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
          backgroundColor: ds.background[100],
          p,
          boxShadow,
          mt: marginTop,
          position: 'absolute',
          left: 0,
          right: 0,
          zIndex: 0,
          borderTop: `0.5px solid ${ds.gray[200]}`,
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
                  marginTop: 'var(--ds-space-2)',
                  zIndex: 10,
                  '& .MuiPopover-paper': {
                    padding: 'var(--ds-space-2)',
                    boxShadow: '0px 2px 32px 2px #0000001A',
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
                                p: 'var(--ds-space-1) var(--ds-space-2) !important',
                                borderRadius: 'var(--ds-radius-sm)',
                                fontWeight: 'var(--ds-font-weight-medium)',
                                fontSize: 'var(--ds-text-body-lg)',
                                color: ds.gray[700],
                                letterSpacing: '-1%',
                                backgroundColor: ds.blue[100],
                                textTransform: 'capitalize',
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
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--ds-space-3)',
                }}
              >
                {filterOptions.map((opt, _idx) => {
                  if (opt.hidden) return null;
                  const selected = activeDropdownTab === opt.value;
                  return (
                    <Box key={opt?.name} display='flex' flexDirection='column' gap={ds.space[1]} zIndex={20}>
                      <Button
                        component={Link}
                        href={getTabUrl(opt)}
                        onClick={() => {
                          setActiveDropdownTab(currentOpt.value);
                          setActiveDropdownSubtab(0);
                        }}
                        id={`anchor-tab-${opt?.id || opt?.name}`}
                        sx={{
                          width: 'max-content',
                          textTransform: 'unset',
                          cursor: 'pointer',
                          fontSize: 'var(--ds-text-body)',
                          lineHeight: '20px',
                          fontWeight: 'var(--ds-font-weight-regular)',
                          padding: 'var(--ds-space-1) var(--ds-space-2)',
                          borderRadius: 'var(--ds-radius-sm)',
                          bgcolor: selected ? ds.brand[500] : ds.blue[100],
                          color: selected ? ds.background[100] : ds.gray[700],
                          transition: 'all ease 0.25s',
                          display: 'flex',
                          alignItems: 'center',
                          gap: 'var(--ds-space-1)',
                          '& .tab-icon': {
                            width: ds.space.mul(0, 11),
                            height: ds.space.mul(0, 11),
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            filter:
                              selected && !opt?.doNotInvertIcon
                                ? 'brightness(0) invert(1)'
                                : 'invert(28%) sepia(78%) saturate(1804%) hue-rotate(201deg) brightness(95%) contrast(90%)',
                            transition: 'filter 0.25s ease',
                            '& img': {
                              objectFit: 'contain',
                            },
                            '& svg': {
                              maxWidth: '100%',
                              maxHeight: '100%',
                            },
                          },
                          '&:hover .arrow-icon': {
                            transform: 'rotate(180deg)',
                          },
                          '& .arrow-icon': {
                            transition: 'transform 0.3s ease',
                          },
                          '&:hover': {
                            color: ds.blue[500],
                          },
                        }}
                        disabled={opt.disabled || false}
                        aria-owns={anchorEl ? 'mouse-over-popover' : undefined}
                        aria-haspopup='true'
                        onMouseOver={(e) => {
                          handlePopoverOpen(e, opt);
                        }}
                      >
                        <SafeIcon src={opt.icon} alt={opt.name} className='tab-icon' />
                        <Box display={'flex'} alignItems={'center'} gap={ds.space.mul(0, 3)}>
                          <span>{opt.name}</span>
                          {opt.count && <CustomPill value={opt?.count} bgColor={ds.blue[100]} showBorder={isSelected(_idx)} />}
                        </Box>
                        {opt.betaIcon && <SafeIcon src={BetaIcon} alt='Beta icon' style={{ height: '20px', width: '20px', marginTop: '-10px' }} />}
                        {opt.tabOptions && (
                          <SafeIcon
                            src={MenuArrowDownIcon}
                            alt='down arrow'
                            className='arrow-icon'
                            style={{ height: '18px', width: '18px', opacity: '80%' }}
                          />
                        )}
                      </Button>

                      <Box
                        sx={{
                          width: '100%',
                          height: ds.space[0],
                          bgcolor: selected ? ds.brand[500] : ds.blue[100],
                          borderRadius: 'var(--ds-radius-xl)',
                        }}
                      />
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
        <Box mt={8} position={'relative'} mb={1}>
          <Box
            sx={{
              display: 'flex',
              backgroundColor: ds.background[100],
              p: 'var(--ds-space-2) var(--ds-space-6) 0px var(--ds-space-6)',
              boxShadow: '0px 4px 10px -1px rgba(229, 229, 229, 0.2), 0px 2px 10px 0px rgb(233, 233, 233)',
              alignItems: 'center',
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-lg)',
              gap: 'var(--ds-space-2)',
            }}
          >
            {filterOptions[activeDropdownTab]?.options.map((opt, _idx) => (
              <React.Fragment key={opt.id}>
                {_idx > 0 && <Box sx={{ height: ds.space[4], width: '1px', backgroundColor: ds.gray[300] }} />}
                <Button
                  onClick={() => handleClickScroll(opt.id)}
                  sx={{
                    textTransform: 'unset',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: ds.gray[700],
                    px: 'var(--ds-space-3)',
                    py: 'var(--ds-space-2)',
                    mb: 'var(--ds-space-2)',
                    minHeight: 0,
                    minWidth: 0,
                    borderRadius: 'var(--ds-radius-sm)',
                    transition: 'all 0.2s ease',
                    '&:hover': {
                      color: ds.blue[500],
                      backgroundColor: ds.blue[100],
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
        <Box mt={8} position={'relative'} mb={2}>
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
        <Box mt={8} mb={1} position={'relative'} />
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
