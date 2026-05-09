import {
  Accordion,
  AccordionDetails,
  AccordionSummary,
  Box,
  Checkbox,
  Divider,
  Grid,
  List,
  ListItem,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Paper,
  Radio,
  TextField,
  Typography,
} from '@mui/material';
import { useEffect, useState } from 'react';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import Autocomplete from '@mui/material/Autocomplete';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import CustomButtonsGroup from './CustomButtonsGroup';
import CustomSearch from './CustomSearch';
import CustomSwitch from './CustomSwitch';
import { inputSx, inputCustomSx } from '@data/themes/inputField';
import { FilterIcon } from '@assets';
import CustomDivider from './CustomDivider';
import ShareButton from './ShareButton';
import CustomDateTimeRangePicker from './widgets/CustomDateTimeRangePicker';
import DownloadButton from './DownloadButton';
import PropTypes from 'prop-types';
import Text from './format/Text';
import { colors } from 'src/utils/colors';
import { TIME_PICK_SHORTCUTS } from '@data/constants';
import SafeIcon from './SafeIcon';

const CustomPaper = (props) => {
  return <Paper sx={{ overflowY: 'auto' }} elevation={8} {...props} />;
};

const CustomTableFilters = ({
  filterOptions,
  showBorder,
  sharingOptions,
  dateTimeRange,
  handleSelectDates,
  minDate,
  onClearAll,
  resetDateTime,
  setExpandedAccordions,
  expandedAccordions,
}) => {
  const [searchTerms, setSearchTerms] = useState({});
  const [filteredOptions, setFilteredOptions] = useState({});

  useEffect(() => {
    const newFilteredOptions = {};
    filterOptions.forEach((option) => {
      if (option.type === 'multi-select' || option.type === 'single-select') {
        const searchTerm = searchTerms[option.label] || '';
        newFilteredOptions[option.label] = searchTerm
          ? option.options.filter((opt) => opt.toLowerCase().includes(searchTerm.toLowerCase()))
          : option.options;
      }
    });
    setFilteredOptions(newFilteredOptions);
  }, [searchTerms, filterOptions]);

  const handleSearchChange = (optionLabel, value) => {
    setSearchTerms((prev) => ({ ...prev, [optionLabel]: value }));
  };

  const getSelectedCount = (option) => {
    switch (option.type) {
      case 'dropdown':
        return Array.isArray(option.value) ? option.value.length : option.value ? 1 : 0;
      case 'single-select':
        return Array.isArray(option.value) ? option.value.length : option.value ? 1 : 0;
      case 'multi-dropdown':
        return Array.isArray(option.value) ? option.value.length : 0;
      case 'multi-select':
        return Array.isArray(option.value) ? option.value.length : 0;
      default:
        return 0;
    }
  };

  const isAnyFilterSelected = (filterOptions) => {
    return filterOptions?.some((option) => getSelectedCount(option) > 0);
  };

  return (
    <Box className={`custom-dropdown ${showBorder ? 'custom-dropdown-props' : ''}`}>
      <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'} py={'10px'}>
        <Box
          display={'flex'}
          alignItems={'center'}
          gap={'10px'}
          color={colors.text.tertiary}
          fontWeight={500}
          fontSize={'16px'}
          sx={{
            '& .filter_icon': {
              filter: isAnyFilterSelected(filterOptions)
                ? 'brightness(0) saturate(100%) invert(31%) sepia(97%) saturate(3613%) hue-rotate(216deg) brightness(96%) contrast(91%)'
                : 'none',
            },
          }}
        >
          <SafeIcon src={FilterIcon} alt='filter' className='filter_icon' />
          Filters
        </Box>
        <Typography color={colors.text.primary} fontWeight={500} fontSize={'12px'} sx={{ cursor: 'pointer' }} onClick={onClearAll}>
          Clear All
        </Typography>
      </Box>
      <CustomDivider />
      <Box display={'flex'} gap={'12px'} my={'10px'}>
        {dateTimeRange?.enabled && (
          <CustomDateTimeRangePicker
            width='190px'
            passedSelectedDateTime={dateTimeRange?.passedSelectedDateTime}
            onChange={handleSelectDates}
            minDate={minDate}
            resetDateTime={resetDateTime}
            shortCuts={dateTimeRange.shortCuts || TIME_PICK_SHORTCUTS}
          />
        )}
      </Box>
      {filterOptions
        ?.filter((f) => (f.enabled === undefined ? true : f.enabled))
        .map((option, index) => {
          return (
            <>
              <Accordion
                key={index}
                expanded={expandedAccordions[index] || false}
                onChange={() => {
                  setExpandedAccordions((prev) => ({ ...prev, [index]: !prev[index] }));
                }}
                className='custom-accordion'
              >
                <AccordionSummary
                  expandIcon={<ExpandMoreIcon />}
                  aria-controls={`panel${index}-content`}
                  id={`panel${index}-header`}
                  sx={{ p: '0px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
                >
                  <Typography color={colors.text.secondary} fontWeight={500} fontSize={'12px'}>
                    {option.label}
                  </Typography>
                  {getSelectedCount(option) > 0 && (
                    <Typography
                      color={colors.text.white}
                      fontWeight={500}
                      fontSize={'13px'}
                      ml='auto'
                      mr='8px'
                      bgcolor={colors.background.primary}
                      borderRadius={'4px'}
                      height={'17px'}
                      width={'26px'}
                      display={'flex'}
                      alignItems={'center'}
                      justifyContent={'center'}
                      lineHeight={'17px'}
                    >
                      {getSelectedCount(option)}
                    </Typography>
                  )}
                </AccordionSummary>
                <AccordionDetails>
                  {(() => {
                    if (option.type === 'buttons') {
                      return (
                        <Grid item sx={{ margin: '8px 0px 11px 0px', borderBottom: `1px solid ${colors.border.secondary}` }}>
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
                      const value = option.value ? option.options.find((op) => op.value == option.value) || option.value : null;
                      return (
                        <Autocomplete
                          size='small'
                          id={`auto-complete-${option.label}`}
                          sx={{
                            ...inputCustomSx,
                            maxWidth: option?.width || 200,
                          }}
                          blurOnSelect='mouse'
                          value={value}
                          options={option.options}
                          popupIcon={<KeyboardArrowDownIcon className='custom-dropdown-icon' />}
                          onChange={(event, value) => {
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
                          disabled={option?.isDisabled || false}
                          noOptionsText='No options available'
                          renderInput={(params) => <TextField {...params} sx={inputSx} size='small' />}
                          ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                          PaperComponent={CustomPaper}
                          isOptionEqualToValue={(option, _value) => {
                            if (value === '' || option.value === '') {
                              return true;
                            }
                            if (typeof option === 'string') {
                              return option === value;
                            }
                            return option.value === value.value;
                          }}
                        />
                      );
                    } else if (option.type === 'multi-dropdown') {
                      return (
                        <Autocomplete
                          size='small'
                          multiple
                          disablePortal
                          key={`auto-complete-${option.label}`}
                          id={`auto-complete-${option.label}`}
                          sx={{
                            ...inputCustomSx,
                            maxWidth: option?.width || 200,
                          }}
                          blurOnSelect='mouse'
                          value={option.value}
                          options={option.options}
                          popupIcon={<KeyboardArrowDownIcon className='custom-dropdown-icon' />}
                          onChange={(event, value) => {
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
                          disabled={option?.isDisabled || false}
                          noOptionsText='No options available'
                          limitTags={option?.limitTags || 2}
                          renderInput={(params) => <TextField {...params} label={option.label} sx={inputSx} size='small' />}
                          ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                          PaperComponent={CustomPaper}
                          isOptionEqualToValue={(option, value) => {
                            if (value === '' || option.value === '') {
                              return true;
                            }
                            if (typeof option === 'string') {
                              return option === value;
                            }
                            return option.value === value.value;
                          }}
                        />
                      );
                    } else if (option.type === 'multi-select') {
                      const allValues = option.options.map((opt) => opt.value || opt);
                      const allSelected = option.value.length === allValues.length;

                      const handleSelectAll = () => {
                        const newValue = allSelected ? [] : allValues;
                        option.onSelect({ target: { value: newValue } }, newValue);
                      };

                      const shouldValueBeChecked = (value) => {
                        if (Array.isArray(option.value)) {
                          return option.value.includes(value);
                        }
                        return option.value === value;
                      };

                      return (
                        <>
                          <CustomSearch
                            label={option.label}
                            minWidth={option.minWidth || '372px'}
                            blurOnSelect={'mouse'}
                            sx={{
                              ...inputCustomSx,
                              '& .MuiOutlinedInput-input': {
                                paddingRight: '0px',
                                paddingBottom: '19px',
                              },
                              '& .MuiOutlinedInput-root': {
                                paddingLeft: '6px',
                              },
                            }}
                            value={searchTerms[option.label] || ''}
                            onChange={(e) => handleSearchChange(option.label, e)}
                          />
                          {option.options.length > 0 && (
                            <>
                              <List className='option-dropdown-list'>
                                <ListItem disablePadding>
                                  <ListItemButton dense onClick={handleSelectAll}>
                                    <ListItemIcon>
                                      <Checkbox
                                        className='dropdown-checkbox'
                                        checked={option.value.length === option.options.length}
                                        indeterminate={option.value.length > 0 && option.value.length < option.options.length}
                                        edge='start'
                                        size='14px'
                                      />
                                    </ListItemIcon>
                                    <ListItemText primary={'Select All'} sx={{ fontSize: '12px !important' }} />
                                  </ListItemButton>
                                </ListItem>
                              </List>
                              <Divider sx={{ color: colors.border.vertical }} />
                              <List className='option-dropdown-list-with-overflow'>
                                {(filteredOptions[option.label] || []).map((value) => {
                                  return (
                                    <ListItem
                                      key={value}
                                      onClick={() => {
                                        const newValue = option.value.includes(value)
                                          ? option.value.filter((v) => v !== value)
                                          : [...option.value, value];
                                        option.onSelect({ target: { value: newValue } }, newValue);
                                      }}
                                      disablePadding
                                    >
                                      <ListItemButton role={undefined} dense>
                                        <ListItemIcon>
                                          <Checkbox className='dropdown-checkbox' edge='start' checked={shouldValueBeChecked(value)} size='14px' />
                                        </ListItemIcon>
                                        <ListItemText primary={value} className='input-labels' />
                                      </ListItemButton>
                                    </ListItem>
                                  );
                                })}
                              </List>
                            </>
                          )}
                        </>
                      );
                    } else if (option.type === 'single-select') {
                      const handleClear = () => {
                        option.onSelect({ target: { value: null } }, null);
                        handleSearchChange(option.label, '');
                      };
                      const displayedOptions = filteredOptions[option.label] || option.options;
                      return (
                        <>
                          <CustomSearch
                            label={option.label}
                            minWidth={option.minWidth || '372px'}
                            blurOnSelect={'mouse'}
                            value={searchTerms[option.label] || ''}
                            onChange={(e) => {
                              handleSearchChange(option.label, e);
                            }}
                            onClear={() => {
                              handleSearchChange(option.label, '');
                            }}
                            onEnterPress={option.onEnter}
                          />

                          {option.options.length > 0 && (
                            <>
                              <List className='option-dropdown-list'>
                                <ListItem disablePadding>
                                  <ListItemButton dense onClick={handleClear}>
                                    <ListItemIcon>
                                      <Checkbox
                                        edge='start'
                                        size='14px'
                                        sx={{
                                          display: 'none',
                                        }}
                                      />
                                    </ListItemIcon>
                                    <ListItemText primary={'Clear All'} sx={{ fontSize: '12px !important' }} />
                                  </ListItemButton>
                                </ListItem>
                              </List>
                              <Divider sx={{ color: colors.border.vertical }} />
                              <List className='option-dropdown-list-with-overflow'>
                                {displayedOptions.map((value) => {
                                  const isSelected = option.value === value;

                                  return (
                                    <ListItem
                                      key={value}
                                      onClick={() => {
                                        option.onSelect({ target: { value: value } }, value);
                                        handleSearchChange(option.label, '');
                                      }}
                                      disablePadding
                                    >
                                      <ListItemButton role={undefined} dense>
                                        <ListItemIcon>
                                          <Radio edge='start' checked={isSelected} size='14px' className='dropdown-checkbox' />
                                        </ListItemIcon>
                                        <ListItemText primary={value} className='input-labels' />
                                      </ListItemButton>
                                    </ListItem>
                                  );
                                })}
                              </List>
                            </>
                          )}
                        </>
                      );
                    } else if (option.type === 'search') {
                      return (
                        <CustomSearch
                          label={option.label}
                          minWidth={option.minWidth || '372px'}
                          maxWidth={option.width || '372px'}
                          blurOnSelect={'mouse'}
                          value={option.value}
                          onChange={(e) => {
                            option.onSelect(
                              {
                                target: {
                                  value: e,
                                },
                              },
                              e
                            );
                          }}
                          onEnterPress={option.onEnter}
                        />
                      );
                    } else if (option.type === 'textfield') {
                      return (
                        <TextField
                          sx={{
                            ...inputSx,
                            minWidth: '200px',
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
                      return option.component;
                    } else if (option.type == 'switch') {
                      return (
                        <>
                          <Text value={option.label} sx={{ mr: '8px' }} />
                          <CustomSwitch id={`${option?.id}`} onChange={(event) => option.onSelect(event.target.checked)} checked={option.value} />
                        </>
                      );
                    }
                  })()}
                </AccordionDetails>
              </Accordion>
              <CustomDivider />
            </>
          );
        })}
      <Box display={'flex'} gap={'12px'} my={'10px'}>
        {sharingOptions?.sharing?.enabled && (
          <ShareButton
            width={sharingOptions?.sharing?.enabled && sharingOptions?.download?.enabled ? '50%' : '100%'}
            height={'32px'}
            onClick={sharingOptions.sharing.onClick}
          />
        )}
        {sharingOptions?.download?.enabled && (
          <DownloadButton
            width={sharingOptions?.sharing?.enabled && sharingOptions?.download?.enabled ? '50%' : '100%'}
            height={'32px'}
            onClick={sharingOptions.download.onClick}
          />
        )}
      </Box>
    </Box>
  );
};

export default CustomTableFilters;

CustomTableFilters.propTypes = {
  filterOptions: PropTypes.array,
  showBorder: PropTypes.bool,
  minDate: PropTypes.instanceOf(Date),
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
  handleSelectDates: PropTypes.any,
  dateTimeRange: PropTypes.shape({
    enabled: PropTypes.bool.isRequired,
    onChange: PropTypes.func.isRequired,
    passedSelectedDateTime: PropTypes.shape({
      startTime: PropTypes.number.isRequired,
      endTime: PropTypes.number.isRequired,
    }),
  }),
  resetDateTime: PropTypes.number,
  onClearAll: PropTypes.func,
  expandedAccordions: PropTypes.object,
  setExpandedAccordions: PropTypes.func,
};
