import {
  Autocomplete,
  Box,
  Grid,
  TextField,
  Paper,
  Typography,
  ToggleButtonGroup,
  ToggleButton,
  InputAdornment,
  IconButton,
  Collapse,
} from '@mui/material';
import React, { useState, useRef, useEffect } from 'react';
import SearchIcon from '@mui/icons-material/Search';
import CloseIcon from '@mui/icons-material/Close';
import CustomSearch from '@components1/common/CustomSearch';
import CustomButtonsGroup from '@components1/common/CustomButtonsGroup';
import DownloadButton from '@components1/common/DownloadButton';
import ShareButton from '@components1/common/ShareButton';
import CopyButton from '@components1/common/CopyButton';
import CustomDateTimeRangePicker from './widgets/CustomDateTimeRangePicker';
import PropTypes from 'prop-types';
import CustomIconButton from '@components1/CustomIconButton';
import { inputSx, inputCustomSx } from '@data/themes/inputField';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { AlphaIcon } from '@assets';
import CustomSwitch from './CustomSwitch';
import CustomTableFilters from './CustomTableFilters';
import TextWithBorder from './TextWithBorder';
import RefreshIcon from '@mui/icons-material/Refresh';
import { keyframes } from '@mui/system';
import CustomButton from './NewCustomButton';
import FilterDropdownButton, { MoreFiltersButton } from './FilterDropdownButton';
import CustomDivider from './CustomDivider';
import { colors } from 'src/utils/colors';
import { TIME_PICK_SHORTCUTS } from '@data/constants';
import SafeIcon from './SafeIcon';

const spin = keyframes`
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
`;

const CustomPaper = (props) => {
  return <Paper sx={{ width: '100%', overflowY: 'auto' }} elevation={8} {...props} />;
};

const BoxLayout2 = ({
  id,
  showBorder = true,
  heading = '',
  marginTop = 0,
  marginBottom = '24px',
  rowGap = '16px',
  children,
  filterOptions = [],
  copyingOption = {
    enabled: false,
    onClick: null,
  },
  minDate,
  alphaIcon = false,
  modalButton = {
    enabled: false,
    text: '',
    onClick: () => {
      return;
    },
    id: '',
  },
  customButton = null,
  sharingOptions = {
    sharing: {
      enabled: true,
      onClick: null,
    },
    download: {
      enabled: true,
      onClick: () => {
        return {
          tableId: '',
        };
      },
    },
  },
  extraOptions = [],
  leftExtraOptions = [],
  dateTimeRange = {
    enabled: false,
    onChange: (_e) => {
      return;
    },
    passedSelectedDateTime: {
      startTime: new Date().getTime() - 3600 * 1000,
      endTime: new Date().getTime(),
      shortcutClickTime: 0,
    },
    shortCuts: TIME_PICK_SHORTCUTS,
    showAbsoluteRange: true,
  },
  sx = {},
  onRefresh = {
    enabled: false,
    text: '',
    onClick: () => {
      return;
    },
    loading: false,
  },
  toggleButtons = {
    options: [],
    activeButton: '',
    handleSelectToggle: () => {
      return;
    },
  },
  searchOption = {
    enabled: false,
    placeholder: 'Search...',
    value: '',
    onChange: () => {},
    onClear: () => {},
    onEnter: () => {},
    width: '250px',
  },

  displaySideFilters = false,
  onClearAll = () => {
    return;
  },
  resetDateTime,
  showFiltersOnRightSide = {},
  expandedAccordions,
  setExpandedAccordions,
}) => {
  const [showAllFilters, setShowAllFilters] = useState(false);
  const [isSearchExpanded, setIsSearchExpanded] = useState(false);
  const searchInputRef = useRef(null);

  // Focus the input when search expands
  useEffect(() => {
    if (isSearchExpanded && searchInputRef.current) {
      searchInputRef.current.focus();
    }
  }, [isSearchExpanded]);

  // Auto-expand if there's a value
  useEffect(() => {
    if (searchOption?.enabled && searchOption?.value) {
      setIsSearchExpanded(true);
    }
  }, [searchOption?.enabled, searchOption?.value]);

  const handleSearchToggle = () => {
    if (isSearchExpanded) {
      setIsSearchExpanded(false);
      if (searchOption?.onClear) {
        searchOption.onClear();
      } else if (searchOption?.onChange) {
        searchOption.onChange({ target: { value: '' } });
      }
    } else {
      setIsSearchExpanded(true);
    }
  };

  const handleSearchChange = (e) => {
    if (searchOption?.onChange) {
      searchOption.onChange(e);
    }
  };

  const handleSearchKeyDown = (e) => {
    if (e.key === 'Escape') {
      handleSearchToggle();
    }
    if (e.key === 'Enter' && searchOption?.onEnter) {
      searchOption.onEnter();
    }
  };

  const handleSelectDates = (ranges) => {
    if (dateTimeRange?.onChange) {
      dateTimeRange.onChange(ranges.selection);
    }
  };

  if (!sharingOptions) {
    sharingOptions = {
      sharing: {
        enabled: false,
        onClick: null,
      },
      download: {
        enabled: false,
        onClick: () => {
          return '';
        },
      },
    };
  } else if (!sharingOptions.sharing) {
    sharingOptions.sharing = {
      enabled: false,
      onClick: null,
    };
  } else if (!sharingOptions.download) {
    sharingOptions.download = {
      enabled: false,
      onClick: () => {
        return '';
      },
    };
  }
  let keyCounter = 0;

  return (
    <Box
      display={displaySideFilters && 'grid'}
      gridTemplateColumns={displaySideFilters ? '220px 1fr' : '1fr'}
      gap={'8px'}
      height={'min-content'}
      mt={displaySideFilters ? '20px' : undefined}
      sx={{
        '@media (max-width: 1350px)': {
          gridTemplateColumns: displaySideFilters ? '200px 1fr' : '1fr',
        },
      }}
    >
      {displaySideFilters && (
        <Box
          bgcolor={colors.background.white}
          borderRadius={'12px'}
          sx={{
            p: '16px 20px !important',
            '@media (max-width: 1100px)': {
              padding: '16px !important',
            },
          }}
          boxShadow='0px 4px 4px 0px #00000010'
        >
          {onRefresh?.enabled && (
            <CustomIconButton variant='outline' enabled={onRefresh.loading} onClick={onRefresh.onClick}>
              <RefreshIcon />
            </CustomIconButton>
          )}
          <CustomDivider />
          <CustomTableFilters
            filterOptions={filterOptions}
            showBorder={showBorder}
            sharingOptions={sharingOptions}
            dateTimeRange={dateTimeRange}
            handleSelectDates={handleSelectDates}
            minDate={minDate}
            onClearAll={onClearAll}
            resetDateTime={resetDateTime}
            expandedAccordions={expandedAccordions}
            setExpandedAccordions={setExpandedAccordions}
          />
        </Box>
      )}

      <Box id={id} display='flex' flexDirection='column' alignItems='flex-start' sx={{ marginTop, marginBottom, scrollMarginTop: '80px' }}>
        <Box
          sx={{
            borderRadius: '12px',
            alignSelf: 'stretch',
            ...sx,
            boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 0.4), 0px 2px 20px 0px rgb(233, 233, 233)',
            border: '1px solid #EBEBEB',
            padding: '16px 24px',
            backgroundColor: 'white',
            '@media (max-width: 1350px)': {
              padding: '16px 8px 20px 8px',
            },
            ...sx,
          }}
        >
          <Box className={`filter-container ${showBorder ? 'filter-container-with-border' : ''}`}>
            {heading && (
              <Box sx={{ paddingBottom: '4px' }}>
                <TextWithBorder
                  value={heading}
                  borderColor={colors.border.primary}
                  borderWidth='3px'
                  sx={{
                    '& p': {
                      fontSize: '14px',
                      fontWeight: '600',
                      color: colors.text.secondary,
                    },
                  }}
                />
              </Box>
            )}

            <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'flex-start', justifyContent: 'space-between', width: '100%' }}>
              {!displaySideFilters && (
                <Grid
                  item
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '8px',
                    rowGap: rowGap,
                    paddingTop: '8px',
                    maxWidth: '100%',
                    flexWrap: 'wrap',
                  }}
                >
                  {(() => {
                    const enabledFilters = filterOptions?.filter((f) => (f.enabled === undefined ? true : f.enabled)) || [];
                    const hasFilterValue = (opt) => {
                      if (opt.type === 'multi-dropdown' || opt.type === 'multi-select') {
                        return Array.isArray(opt.value) && opt.value.length > 0;
                      }
                      return opt.value != null && opt.value !== '';
                    };
                    const filtersToShow = showAllFilters ? enabledFilters : enabledFilters.filter((opt, idx) => idx < 3 || hasFilterValue(opt));
                    const hiddenCount = enabledFilters.length - filtersToShow.length;
                    const hasMoreFilters = !showAllFilters ? hiddenCount > 0 : enabledFilters.length > 3;

                    return (
                      <>
                        {filtersToShow.map((option) => {
                          if (option.type === 'buttons') {
                            return (
                              <Grid
                                item
                                key={'boxlayout-filter-button'}
                                sx={{ margin: '8px 0px 11px 0px', borderBottom: `1px solid ${colors.border.secondary}` }}
                              >
                                <CustomButtonsGroup
                                  borderColor={colors.border.white}
                                  fontWeight={500}
                                  tabType={true}
                                  options={option.options}
                                  selected={option.selected || option.options?.[0]?.value}
                                  onClick={option.onSelect}
                                />
                              </Grid>
                            );
                          } else if (option.type === 'dropdown') {
                            // option.value and null is not a strict comparision so that it works for both null and undefined
                            const value =
                              option.value != null ? option?.options?.find((op) => op.value === option.value || op === option.value) : null;
                            return (
                              <FilterDropdownButton
                                key={`filter-btn-${option.label}`}
                                label={option.label}
                                value={value}
                                options={option.options ?? []}
                                grouped={option.grouped || false}
                                groupIcon={option.groupIcon}
                                disabled={((option?.options ?? []).length === 0 && !option.isOptionsLoading) || Boolean(option.isDisabled)}
                                onSelect={(_event, value) => {
                                  if (!option.onSelect) {
                                    return;
                                  }
                                  const newEventObj = {
                                    target: {
                                      value: value?.value || value,
                                    },
                                  };
                                  option.onSelect(newEventObj, value);
                                }}
                                isOptionsLoading={option.isOptionsLoading || false}
                              />
                            );
                          } else if (option.type === 'multi-dropdown') {
                            return (
                              <FilterDropdownButton
                                multiple={option.multiple ?? true}
                                key={`filter-btn-${option.label}`}
                                label={option.label}
                                value={option.value}
                                options={option.options}
                                grouped={option.grouped || false}
                                groupIcon={option.groupIcon}
                                disabled={((option?.options ?? []).length === 0 && !option.isOptionsLoading) || Boolean(option.isDisabled)}
                                onSelect={(_event, value) => {
                                  if (!option.onSelect) {
                                    return;
                                  }
                                  const newEventObj = {
                                    target: {
                                      value: value?.value || value,
                                    },
                                  };
                                  option.onSelect(newEventObj, value);
                                }}
                                isOptionsLoading={option.isOptionsLoading || false}
                              />
                            );
                          } else if (option.type === 'search') {
                            return (
                              <CustomSearch
                                id={option.id ?? 'search'}
                                key={'search-' + option.label}
                                label={option.label}
                                minWidth={option.minWidth || '372px'}
                                maxWidth={option.maxWidth || '372px'}
                                blurOnSelect={'mouse'}
                                value={option.value}
                                onChange={(value) => {
                                  option.onSelect(
                                    {
                                      target: {
                                        value: value,
                                      },
                                    },
                                    value
                                  );
                                }}
                                onEnterPress={option.onEnter}
                                onClear={option.onClear}
                                disabled={option.isDisabled || false}
                              />
                            );
                          } else if (option.type === 'textfield') {
                            return (
                              <TextField
                                key={'boxlayout-filter-text'}
                                sx={{
                                  ...inputSx,
                                  minWidth: '300px',
                                }}
                                value={option.value}
                                id={option.id}
                                label={option.label}
                                onChange={option.onChange}
                                onKeyDown={option.handleKeyDown}
                                error={option.queryWrong}
                                helperText={option.helperText}
                              />
                            );
                          } else if (option.type === 'custom') {
                            return <React.Fragment key={option.key || 'custom-filter'}>{option.component}</React.Fragment>;
                          } else if (option.type === 'switch') {
                            return (
                              <>
                                <Typography sx={{ fontSize: '14px', fontWeight: 400, mr: '8px' }}>{option.label}</Typography>
                                <CustomSwitch
                                  id={`${option?.id}`}
                                  onChange={(event) => option.onSelect(event.target.checked)}
                                  checked={option.value}
                                />
                              </>
                            );
                          }
                        })}
                        {hasMoreFilters && (
                          <MoreFiltersButton count={hiddenCount} expanded={showAllFilters} onClick={() => setShowAllFilters(!showAllFilters)} />
                        )}
                      </>
                    );
                  })()}
                </Grid>
              )}
              {leftExtraOptions?.length > 0 && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '8px',
                    paddingTop: '0px',
                  }}
                >
                  {leftExtraOptions.map((option, index) => (
                    <React.Fragment key={`left-extra-option-${index}`}>{option}</React.Fragment>
                  ))}
                </Box>
              )}
              <Grid
                item
                sx={{
                  display: 'flex',
                  justifyContent: 'flex-end',
                  alignItems: 'center',
                  flexGrow: 1,
                  gap: '6px',
                  mt: !displaySideFilters && '6px',
                  '& svg': {
                    width: '16px',
                    height: '16px',
                  },
                  '.custom-button-refresh': {
                    padding: onRefresh?.text ? '4px 8px !important' : '8px !important',
                    borderRadius: '6px !important',
                    border: '1px solid #efefef !important',
                  },
                }}
              >
                {toggleButtons && (
                  <ToggleButtonGroup
                    className='toggle-buttons-group'
                    key='toggle-button-group'
                    aria-label='Platform'
                    style={{ border: 0 }}
                    value={toggleButtons.activeButton}
                    onChange={(event) => toggleButtons.handleSelectToggle(event)}
                  >
                    {toggleButtons.options.map((item) => {
                      return (
                        <ToggleButton sx={{}} key={item.id} value={item.id} aria-label='centered'>
                          {item.text}
                        </ToggleButton>
                      );
                    })}
                  </ToggleButtonGroup>
                )}

                {/* Search Option */}
                {searchOption?.enabled && (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '4px',
                    }}
                  >
                    <Collapse in={isSearchExpanded} orientation='horizontal' timeout={200}>
                      <TextField
                        id={id ? `${id}-search-input-text` : 'search-input-text'}
                        inputRef={searchInputRef}
                        placeholder={searchOption.placeholder || 'Search...'}
                        value={searchOption.value || ''}
                        onChange={handleSearchChange}
                        onKeyDown={handleSearchKeyDown}
                        size='small'
                        InputProps={{
                          startAdornment: (
                            <InputAdornment position='start'>
                              <SearchIcon sx={{ color: colors.text.tertiary, width: '18px', height: '18px' }} />
                            </InputAdornment>
                          ),
                          endAdornment: searchOption.value && (
                            <InputAdornment position='end'>
                              <IconButton
                                size='small'
                                onClick={() => {
                                  if (searchOption?.onClear) {
                                    searchOption.onClear();
                                  } else if (searchOption?.onChange) {
                                    searchOption.onChange({ target: { value: '' } });
                                  }
                                }}
                                sx={{ padding: '2px' }}
                              >
                                <CloseIcon sx={{ width: '14px', height: '14px', color: colors.text.tertiary }} />
                              </IconButton>
                            </InputAdornment>
                          ),
                        }}
                        sx={{
                          width: searchOption.width || '250px',
                          '& .MuiOutlinedInput-root': {
                            bgcolor: 'white',
                            borderRadius: '6px',
                            height: '34px',
                            '& fieldset': {
                              borderColor: '#efefef',
                            },
                            '&:hover fieldset': {
                              borderColor: colors.border.primary,
                            },
                            '&.Mui-focused fieldset': {
                              borderColor: colors.border.primary,
                            },
                          },
                        }}
                      />
                    </Collapse>
                    <IconButton
                      id={id ? `${id}-search-toggle-button` : 'search-toggle-button'}
                      onClick={handleSearchToggle}
                      sx={{
                        width: '34px',
                        height: '34px',
                        borderRadius: '6px',
                        border: `1px solid ${isSearchExpanded ? colors.border.primary : '#efefef'}`,
                        bgcolor: isSearchExpanded ? colors.background.primaryLight : 'white',
                        '&:hover': {
                          bgcolor: colors.background.primaryLight,
                          borderColor: colors.border.primary,
                        },
                      }}
                    >
                      {isSearchExpanded ? (
                        <CloseIcon
                          id={id ? `${id}-close-search` : 'close-search'}
                          sx={{ width: '18px', height: '18px', color: colors.text.secondary }}
                        />
                      ) : (
                        <SearchIcon
                          id={id ? `${id}-open-search` : 'open-search'}
                          sx={{ width: '18px', height: '18px', color: colors.text.secondary }}
                        />
                      )}
                    </IconButton>
                  </Box>
                )}

                {copyingOption?.enabled && <CopyButton onClick={copyingOption?.onClick} />}
                {extraOptions?.map((option) => {
                  keyCounter += 1;
                  return <React.Fragment key={'boxlayout-filter-options-' + keyCounter}>{option}</React.Fragment>;
                })}
                {showFiltersOnRightSide?.enabled && (
                  <Autocomplete
                    size='small'
                    key={`auto-complete-${showFiltersOnRightSide.label}`}
                    id={`auto-complete-${showFiltersOnRightSide.label}`}
                    sx={{
                      ...inputCustomSx,
                      maxWidth: showFiltersOnRightSide?.width || 200,
                    }}
                    blurOnSelect={'mouse'}
                    value={showFiltersOnRightSide.value}
                    options={showFiltersOnRightSide.options}
                    popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                    onChange={(event, value) => {
                      if (!showFiltersOnRightSide.onSelect) {
                        return;
                      }
                      const newEventObj = {
                        target: {
                          value: value?.value || value,
                        },
                      };
                      showFiltersOnRightSide.onSelect(newEventObj, value);
                    }}
                    disabled={showFiltersOnRightSide?.isDisabled || false}
                    noOptionsText='No options available'
                    renderInput={(params) => <TextField {...params} label={showFiltersOnRightSide.label} sx={inputSx} size='small' />}
                    ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                    PaperComponent={CustomPaper}
                    isOptionEqualToValue={(option, value) => {
                      if (value === '' || option.value === '') {
                        return true;
                      }
                      return option.value === value.value;
                    }}
                  />
                )}

                {!displaySideFilters && onRefresh?.enabled && (
                  <CustomIconButton variant='outline' isDisabled={onRefresh.loading} onClick={onRefresh.onClick}>
                    <RefreshIcon
                      sx={{
                        animation: onRefresh.loading ? `${spin} 1s linear infinite` : 'none',
                      }}
                    />
                  </CustomIconButton>
                )}
                {!displaySideFilters && dateTimeRange?.enabled && (
                  <CustomDateTimeRangePicker
                    passedSelectedDateTime={dateTimeRange?.passedSelectedDateTime}
                    onChange={handleSelectDates}
                    minDate={minDate}
                    shortCuts={dateTimeRange.shortCuts || TIME_PICK_SHORTCUTS}
                    showAbsoluteRange={dateTimeRange.showAbsoluteRange}
                  />
                )}
                {!displaySideFilters && sharingOptions?.sharing?.enabled && <ShareButton onClick={sharingOptions.sharing.onClick} />}
                {customButton && (
                  <Box
                    sx={{
                      display: 'flex',
                      justifyContent: 'flex-end',
                      '& button': { height: '34px' },
                    }}
                  >
                    {customButton}
                  </Box>
                )}
                {modalButton?.enabled && (
                  <Box
                    sx={{
                      display: 'flex',
                      justifyContent: 'flex-end',
                      '& button': { height: '34px' },
                    }}
                  >
                    <CustomButton text={modalButton?.text} id={modalButton?.id ?? ''} onClick={modalButton.onClick} />
                    {alphaIcon && (
                      <SafeIcon src={AlphaIcon} alt='Alpha Icon' width={38} height={38} style={{ marginLeft: '4px', marginTop: '-8px' }} />
                    )}
                  </Box>
                )}

                {!displaySideFilters && sharingOptions?.download?.enabled && (
                  <DownloadButton id={`${id}-download`} onClick={sharingOptions.download?.onClick} />
                )}
              </Grid>
            </Box>
          </Box>

          {children}
        </Box>
      </Box>
    </Box>
  );
};

BoxLayout2.propTypes = {
  id: PropTypes.string.isRequired,
  heading: PropTypes.string,
  marginTop: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  marginBottom: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  rowGap: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  filterOptions: PropTypes.array,
  minDate: PropTypes.instanceOf(Date),
  copyingOption: PropTypes.shape({
    enabled: PropTypes.bool.isRequired,
    onClick: PropTypes.func,
  }),
  sharingOptions: PropTypes.shape({
    sharing: PropTypes.shape({
      enabled: PropTypes.bool.isRequired,
      onClick: PropTypes.func,
    }),
    download: PropTypes.shape({
      enabled: PropTypes.bool.isRequired,
      onClick: PropTypes.func,
    }),
  }),
  extraOptions: PropTypes.array,
  leftExtraOptions: PropTypes.array,
  dateTimeRange: PropTypes.shape({
    enabled: PropTypes.bool.isRequired,
    onChange: PropTypes.func.isRequired,
    passedSelectedDateTime: PropTypes.shape({
      startTime: PropTypes.number.isRequired,
      endTime: PropTypes.number.isRequired,
      shortcutClickTime: PropTypes.number,
    }),
    shortCuts: PropTypes.array,
  }),
  sx: PropTypes.object,
  alphaIcon: PropTypes.bool,
  showBorder: PropTypes.bool,
  children: PropTypes.any,
  modalButton: PropTypes.shape({
    enabled: PropTypes.bool,
    text: PropTypes.string,
    onClick: PropTypes.func,
  }),
  resetDateTime: PropTypes.number,
  onClearAll: PropTypes.func,
  displaySideFilters: PropTypes.bool,
  onRefresh: PropTypes.shape({
    enabled: PropTypes.bool,
    text: PropTypes.string,
    onClick: PropTypes.any,
    loading: PropTypes.bool,
  }),
  showFiltersOnRightSide: PropTypes.object,
  toggleButtons: PropTypes.shape({
    options: PropTypes.array,
    activeButton: PropTypes.string,
    handleSelectToggle: PropTypes.func,
  }),
  searchOption: PropTypes.shape({
    enabled: PropTypes.bool,
    placeholder: PropTypes.string,
    value: PropTypes.string,
    onChange: PropTypes.func,
    onClear: PropTypes.func,
    onEnter: PropTypes.func,
    width: PropTypes.string,
  }),
  expandedAccordions: PropTypes.object,
  setExpandedAccordions: PropTypes.func,
};
export default BoxLayout2;
