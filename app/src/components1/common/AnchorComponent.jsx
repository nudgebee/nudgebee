import { Box, Button, Typography, MenuItem, Popover } from '@mui/material';
import React, { useEffect, useState, useRef } from 'react';
import { useRouter } from 'next/router';
import CustomIconButton from '@components1/CustomIconButton';
import { BetaIcon, MenuArrowDownIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import CustomPill from './CustomPill';
import CustomTabs from './CustomTabs';
import Link from 'next/link';
import { colors } from 'src/utils/colors';

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
          p: '6px 12px !important',
          m: showGroupedTabs && '4px 6px !important',
          borderRadius: '6px',
          fontWeight: '600',
          fontSize: '14px',
          color: colors.text.secondary,
          letterSpacing: '-1%',
          minWidth: '188px',
          maxWidth: '240px',
          ...(item.disabled
            ? {
                opacity: 0.5,
                cursor: 'not-allowed',
                pointerEvents: 'none',
                color: colors.text.disabled || '#9CA3AF',
              }
            : {
                '&:hover': {
                  backgroundColor: colors.background.primaryLightest,
                  p: { color: colors.text.primary },
                },
              }),
          '&.Mui-selected': {
            color: colors.text.primary,
            background: colors.background.primaryLightest,
            fontWeight: 500,
          },
          '& img': { mr: '8px' },
        }}
      >
        <SafeIcon src={item?.icon} height={item?.height || 20} width={item?.width || 20} alt={item.text} />
        <Typography marginLeft={1} textAlign='left' color='inherit' fontSize={14} display='flex' alignItems='center' gap='2px'>
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
          backgroundColor: colors.background.white,
          p,
          boxShadow,
          mt: marginTop,
          position: 'absolute',
          left: 0,
          right: 0,
          zIndex: 0,
          borderTop: `0.5px solid ${colors.border.vertical}`,
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
              paddingTop: '10px',
              width: '0.4em',
              height: '4px',
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
                  marginTop: '8px',
                  zIndex: 10,
                  '& .MuiPopover-paper': {
                    padding: '8px',
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
                        columnGap: isGroupedTabs && '12px',
                        maxWidth: isGroupedTabs ? '490px' : '100%',
                      }}
                    >
                      {Object.entries(groupByTabName(currentOpt.tabOptions)).map(([tabName, options]) => (
                        <Box key={tabName}>
                          {tabName && tabName !== 'undefined' && (
                            <Typography
                              sx={{
                                p: '2px 8px !important',
                                borderRadius: '4px',
                                fontWeight: '500',
                                fontSize: '14px',
                                color: colors.secondary.default,
                                letterSpacing: '-1%',
                                backgroundColor: colors.background.primaryLightest,
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
                  gap: '11px',
                }}
              >
                {filterOptions.map((opt, _idx) => {
                  if (opt.hidden) return null;
                  const selected = activeDropdownTab === opt.value;
                  return (
                    <Box key={opt?.name} display='flex' flexDirection='column' gap='4px' zIndex={20}>
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
                          fontSize: '13px',
                          lineHeight: '20px',
                          fontWeight: 400,
                          padding: '6px 10px',
                          borderRadius: '4px',
                          bgcolor: selected ? colors.background.tabPrimarySelected : colors.background.primaryLightest,
                          color: selected ? colors.text.white : colors.text.secondary,
                          transition: 'all ease 0.25s',
                          display: 'flex',
                          alignItems: 'center',
                          gap: '6px',
                          '& .tab-icon': {
                            width: '22px',
                            height: '22px',
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
                            color: colors.text.primary,
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
                        <Box display={'flex'} alignItems={'center'} gap={'6px'}>
                          <span>{opt.name}</span>
                          {opt.count && <CustomPill value={opt?.count} bgColor={colors.background.primaryLightest} showBorder={isSelected(_idx)} />}
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
                          height: '2px',
                          bgcolor: selected ? colors.background.tabPrimarySelected : colors.background.primaryLightest,
                          borderRadius: '19px',
                        }}
                      />
                    </Box>
                  );
                })}
              </Box>
            )}
          </Box>
          {buttonTitle && (
            <Box sx={{ mr: '24px' }}>
              <CustomIconButton onClick={handleButtonAction} size={'large'} variant={'primary'}>
                {buttonTitle}
              </CustomIconButton>
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
              backgroundColor: colors.background.white,
              p: '8px 32px 0px 32px',
              boxShadow: '0px 4px 10px -1px rgba(229, 229, 229, 0.2), 0px 2px 10px 0px rgb(233, 233, 233)',
              alignItems: 'center',
              border: '1px solid #EBEBEB',
              borderRadius: '8px',
              gap: '8px',
            }}
          >
            {filterOptions[activeDropdownTab]?.options.map((opt, _idx) => (
              <React.Fragment key={opt.id}>
                {_idx > 0 && <Box sx={{ height: '16px', width: '1px', backgroundColor: colors.border.nudgebeeSuggestion }} />}
                <Button
                  onClick={() => handleClickScroll(opt.id)}
                  sx={{
                    textTransform: 'unset',
                    fontSize: '13px',
                    fontWeight: 400,
                    color: colors.text.secondary,
                    px: '12px',
                    py: '8px',
                    mb: '8px',
                    minHeight: 0,
                    minWidth: 0,
                    borderRadius: '4px',
                    transition: 'all 0.2s ease',
                    '&:hover': {
                      color: colors.text.primary,
                      backgroundColor: colors.background.primaryLightest,
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
