import { Autocomplete, Grid, TextField, Paper, Box, Typography } from '@mui/material';
import React from 'react';
import CustomSearch from '@components1/common/CustomSearch';
import CustomButtonsGroup from '@components1/common/CustomButtonsGroup';
import { inputSx, inputCustomSx } from '@data/themes/inputField';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { colors } from 'src/utils/colors';
import { DateRangePicker } from '@components1/ds/DateRangePicker';
interface FilterGroupProps {
  filterOptions?: any[];
  dateTimeRange?: any;
}

const FilterGroup: React.FC<FilterGroupProps> = ({
  filterOptions = [],
  dateTimeRange = {
    enabled: false,
    onChange: (_e: any) => {
      return;
    },
    passedSelectedDateTime: {
      startTime: new Date().getTime() - 3600 * 1000,
      endTime: new Date().getTime(),
    },
  },
}) => {
  const CustomPaper = (props: any) => {
    return <Paper sx={{ width: 'fit-content', overflowY: 'auto' }} elevation={8} {...props} />;
  };
  const handleSelectDates = (ranges: any) => {
    if (dateTimeRange?.onChange) {
      dateTimeRange.onChange(ranges.selection);
    }
  };
  return (
    <Box
      sx={{
        mt: 'var(--ds-space-3)',
        mb: 'var(--ds-space-4)',
        padding: '10px 12px',
        borderRadius: 'var(--ds-radius-md)',
        boxShadow: '0px 4px 4px 0px #00000026',
        alignSelf: 'stretch',
        alignItems: 'center',
        justifyContent: 'space-between',
        backgroundColor: 'var(--ds-background-100)',
        display: 'flex',
        flexDirection: 'row',
        flexWrap: 'wrap',
        '& .MuiAutocomplete-hasPopupIcon': {
          width: '200px !important',
        },
        '& .MuiOutlinedInput-notchedOutline': {
          border: 'inherit',
          borderColor: colors.text.primary,
        },
        '& .MuiOutlinedInput-root': {
          height: '32px',
          padding: '6px 14px',
          '& > fieldset': { border: '0.5px solid', borderColor: colors.border.secondary },
        },
        '& .MuiOutlinedInput-root:hover': {
          '& input': {
            color: colors.text.secondary,
          },

          '& > fieldset': { border: '0.5px solid', borderColor: colors.text.primary },
        },
      }}
    >
      <Box sx={{ display: 'flex', gap: '6px' }}>
        {filterOptions
          .filter((f) => (f.enabled === undefined ? true : f.enabled))
          .map((option) => {
            if (option.type === 'buttons') {
              return (
                <Grid item key={option.id} sx={{ margin: '8px 0px 11px 0px', borderBottom: '1px solid var(--ds-gray-300)' }}>
                  <CustomButtonsGroup
                    borderColor='var(--ds-background-100)'
                    fontWeight={500}
                    tabType={true}
                    options={option.options}
                    selected={option.selected || option.options?.[0]?.value}
                    onClick={option.onSelect}
                  />
                </Grid>
              );
            } else if (option.type === 'dropdown') {
              return (
                <Autocomplete
                  size='small'
                  key={`auto-complete-${option.label}`}
                  id={`auto-complete-${option.label}`}
                  blurOnSelect={'mouse'}
                  sx={{
                    ...inputCustomSx,
                    maxWidth: option?.width || 200,
                  }}
                  value={option.value}
                  options={option.options}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: 'var(--ds-gray-600)' }} />}
                  onChange={(_event, newValue) => {
                    if (!option.onSelect) {
                      return;
                    }
                    const newEventObj = {
                      target: {
                        value: newValue?.value || newValue,
                      },
                    };
                    option.onSelect(newEventObj, newValue);
                  }}
                  disabled={option?.options?.length == 0 || (option?.disabled ?? false)}
                  renderInput={(params) => <TextField {...params} label={option.label} margin='normal' sx={inputSx} size='small' />}
                  ListboxProps={{ sx: { width: 'max-content' } }}
                  PaperComponent={CustomPaper}
                  isOptionEqualToValue={(option, _value) => {
                    if (option.value === '' || option.value === '') {
                      return true;
                    }
                    if (typeof option === 'string') {
                      return option === _value;
                    }
                    return option.value === _value.value;
                  }}
                />
              );
            } else if (option.type === 'search') {
              return (
                <CustomSearch
                  key={option.id}
                  label={option.label}
                  minWidth={option.minWidth || '372px'}
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
                />
              );
            } else if (option.type === 'custom') {
              return <React.Fragment key={option.id}>{option.component}</React.Fragment>;
            }
          })}
      </Box>
      <Typography sx={{ color: 'var(--ds-gray-600)', mr: 'var(--ds-space-5)' }}>Last 24 hours</Typography>
      {dateTimeRange.enabled && (
        <DateRangePicker
          valueShape='epoch'
          value={
            dateTimeRange?.passedSelectedDateTime
              ? {
                  start: dateTimeRange.passedSelectedDateTime.startTime,
                  end: dateTimeRange.passedSelectedDateTime.endTime,
                }
              : null
          }
          onChange={(next) => handleSelectDates({ startTime: next.start, endTime: next.end, shortcutClickTime: 0 })}
        />
      )}
    </Box>
  );
};

export default FilterGroup;
