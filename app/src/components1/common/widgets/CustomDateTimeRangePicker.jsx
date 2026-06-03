import React, { useEffect, useState } from 'react';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { TextField, Button, Popover, Box, Stack, Typography } from '@mui/material';
import { calendarViewWeek, MenuArrowDownIcon } from '@assets';
import dayjs from 'dayjs';
import CustomButton from '@components1/common/NewCustomButton';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import Tooltip from '@components1/ds/Tooltip';
import { TIME_PICK_SHORTCUTS } from '@data/constants';
import SafeIcon from '@common/SafeIcon';

CustomDateTimeRangePicker.propTypes = {
  passedSelectedDateTime: PropTypes.shape({
    startTime: PropTypes.number.isRequired,
    endTime: PropTypes.number.isRequired,
  }),
  onChange: PropTypes.func.isRequired,
  views: PropTypes.arrayOf(PropTypes.oneOf(['day', 'hours', 'minutes'])),
  minDate: PropTypes.oneOfType([PropTypes.object, PropTypes.string, PropTypes.number]),
  resetDateTime: PropTypes.number,
  width: PropTypes.any,
  shortCuts: PropTypes.arrayOf(PropTypes.string),
  showAbsoluteRange: PropTypes.bool,
  showOnlyCalenderIcon: PropTypes.bool,
  sx: PropTypes.object,
};

const CalendarIcon = () => {
  return (
    <Box
      component='img'
      sx={{
        height: '24px',
        width: '16px',
      }}
      alt='calendar'
      src={calendarViewWeek.default.src}
    />
  );
};

function CustomDateTimeRangePicker({
  passedSelectedDateTime = {
    startTime: new Date().getTime() - 3600 * 1000,
    endTime: new Date().getTime(),
    shortcutClickTime: 0,
  },
  onChange,
  views = ['day', 'hours', 'minutes'],
  minDate = dayjs().subtract(2, 'month'),
  width = '170px',
  resetDateTime,
  shortCuts = TIME_PICK_SHORTCUTS,
  showAbsoluteRange = true,
  showOnlyCalenderIcon = false,
  sx = {},
}) {
  const [selectedDateTime, setSelectedDateTime] = useState({ startTime: passedSelectedDateTime.startTime, endTime: passedSelectedDateTime.endTime });
  const [isShortcutSelected, setIsShortcutSelected] = useState(false);
  const [selectedShortCut, setSelectedShortCut] = useState('');
  const [anchorEl, setAnchorEl] = useState(null);
  const [isPopoverOpen, setIsPopoverOpen] = useState(false);

  const normalizedMinDate = dayjs.isDayjs(minDate) ? minDate : dayjs(minDate);

  useEffect(() => {
    if (resetDateTime) {
      setSelectedDateTime({
        startTime: passedSelectedDateTime.startTime,
        endTime: passedSelectedDateTime.endTime,
      });
      setIsShortcutSelected(false);
      setSelectedShortCut('');
    }
  }, [resetDateTime, passedSelectedDateTime.startTime, passedSelectedDateTime.endTime]);

  const open = Boolean(anchorEl);
  const id = open ? 'simple-popover' : undefined;

  const handleOpenDatePicker = (event) => {
    setAnchorEl(event.currentTarget);
    setIsPopoverOpen(true);
  };

  const handleStartDateChange = (date) => {
    setIsShortcutSelected(false);
    setSelectedShortCut('');
    setSelectedDateTime((prevState) => ({
      ...prevState,
      startTime: date?.valueOf(),
      shortcutClickTime: 0,
    }));
  };

  const handleEndDateChange = (date) => {
    setIsShortcutSelected(false);
    setSelectedShortCut('');
    setSelectedDateTime((prevState) => ({
      ...prevState,
      endTime: date?.valueOf(),
      shortcutClickTime: 0,
    }));
  };

  const handleCloseDatePicker = () => {
    setAnchorEl(null);
    setIsPopoverOpen(false);
  };

  const handleApply = (fromSelectedShortHour) => {
    if (onChange) {
      if (!fromSelectedShortHour) {
        setSelectedShortCut('');
        setIsShortcutSelected(false);
      }

      // Use the current shortcutClickTime from selectedDateTime, but fallback properly
      let finalShortcutClickTime = 0;
      if (fromSelectedShortHour && selectedDateTime.shortcutClickTime) {
        finalShortcutClickTime = selectedDateTime.shortcutClickTime;
      } else if (isShortcutSelected && selectedDateTime.shortcutClickTime) {
        // If we know a shortcut is selected, preserve the shortcutClickTime
        finalShortcutClickTime = selectedDateTime.shortcutClickTime;
      }

      const selectionObject = {
        startTime: selectedDateTime.startTime,
        endTime: selectedDateTime.endTime,
        shortcutClickTime: finalShortcutClickTime,
      };

      onChange({
        selection: selectionObject,
      });
    }
    setAnchorEl(null);
  };

  const handleShortcutClick = (shortcut) => {
    const now = new Date();
    let shortcutClickTime = 0;
    let date = now.getTime();
    const minDateValue = new Date(minDate);
    let endTime = now.getTime();

    const timeAdjustments = {
      'Last 24 Hours': 24 * 60 * 60 * 1000,
      'Last 1 Hour': 1 * 60 * 60 * 1000,
      'Last 3 Hours': 3 * 60 * 60 * 1000,
      'Last 6 Hours': 6 * 60 * 60 * 1000,
      'Last 12 Hours': 12 * 60 * 60 * 1000,
      'Last 5 Minutes': 5 * 60 * 1000,
      'Last 10 Minutes': 10 * 60 * 1000,
      'Last 15 Minutes': 15 * 60 * 1000,
      'Last 30 Minutes': 30 * 60 * 1000,
      'Current Week': 7 * 24 * 60 * 60 * 1000,
    };

    if (timeAdjustments[shortcut]) {
      shortcutClickTime = timeAdjustments[shortcut];
      date = now.getTime() - shortcutClickTime;
    } else {
      switch (shortcut) {
        case 'Current Week': {
          const currentDayOfWeek = now.getDay();
          const startOfWeek = new Date(now);
          startOfWeek.setDate(now.getDate() - currentDayOfWeek);
          startOfWeek.setHours(0, 0, 0, 0);
          date = Math.max(startOfWeek.getTime(), minDateValue.getTime());
          break;
        }
        case 'Current Month': {
          const startOfMonth = new Date(now.getFullYear(), now.getMonth(), 1);
          startOfMonth.setHours(0, 0, 0, 0);
          date = Math.max(startOfMonth.getTime(), minDateValue.getTime());
          break;
        }
        case 'Last Month': {
          const startOfLastMonth = new Date(now.getFullYear(), now.getMonth() - 1, 1);
          startOfLastMonth.setHours(0, 0, 0, 0);
          date = Math.max(startOfLastMonth.getTime(), minDateValue.getTime());

          const endOfLastMonth = new Date(now.getFullYear(), now.getMonth(), 0);
          endOfLastMonth.setHours(23, 59, 59, 999);
          endTime = endOfLastMonth.getTime();
          break;
        }
        default:
          date = now.getTime();
      }
      shortcutClickTime = now.getTime() - date;
    }

    setSelectedShortCut(shortcut);
    setIsShortcutSelected(true);

    const newDateTimeObj = {
      startTime: date,
      endTime,
      shortcutClickTime,
    };

    setSelectedDateTime((prevState) => ({
      ...prevState,
      ...newDateTimeObj,
    }));

    // Call onChange directly with the new values instead of waiting for state update
    if (onChange) {
      onChange({
        selection: newDateTimeObj,
      });
    }

    // Close the popover
    setAnchorEl(null);
  };

  useEffect(() => {
    if (passedSelectedDateTime.shortcutClickTime > 0) {
      const shortcutMap = {
        [5 * 60 * 1000]: 'Last 5 Minutes',
        [10 * 60 * 1000]: 'Last 10 Minutes',
        [15 * 60 * 1000]: 'Last 15 Minutes',
        [30 * 60 * 1000]: 'Last 30 Minutes',
        [1 * 60 * 60 * 1000]: 'Last 1 Hour',
        [3 * 60 * 60 * 1000]: 'Last 3 Hours',
        [6 * 60 * 60 * 1000]: 'Last 6 Hours',
        [12 * 60 * 60 * 1000]: 'Last 12 Hours',
        [24 * 60 * 60 * 1000]: 'Last 24 Hours',
        [7 * 24 * 60 * 60 * 1000]: 'Current Week',
      };

      const shortcutName = shortcutMap[passedSelectedDateTime.shortcutClickTime];
      if (shortcutName) {
        setSelectedShortCut(shortcutName);
        setIsShortcutSelected(true);
      } else {
        setIsShortcutSelected(false);
        setSelectedShortCut('');
      }
    } else {
      setIsShortcutSelected(false);
      setSelectedShortCut('');
    }
  }, [passedSelectedDateTime.shortcutClickTime]);

  const displayText = React.useMemo(() => {
    if (isShortcutSelected) {
      return selectedShortCut;
    }

    const startDateFormate = new Date(selectedDateTime.startTime).toLocaleDateString('en-US', {
      day: 'numeric',
      month: 'short',
    });

    const endDateFormate = new Date(selectedDateTime.endTime).toLocaleDateString('en-US', {
      day: 'numeric',
      month: 'short',
    });

    return `${startDateFormate} - ${endDateFormate}`;
  }, [selectedDateTime.startTime, selectedDateTime.endTime, isShortcutSelected, selectedShortCut]);

  return (
    <>
      <Tooltip
        title={`${new Date(selectedDateTime?.startTime ?? Date.now()).toLocaleString()} - ${new Date(
          selectedDateTime?.endTime ?? Date.now()
        ).toLocaleString()}`}
      >
        <Button
          variant='outlined'
          aria-describedby={id}
          onClick={handleOpenDatePicker}
          sx={{
            ...(!showOnlyCalenderIcon && {
              boxShadow: '0 4px 4px rgba(0, 0, 0, 0.04)',
              border: '1px solid #e2e2e2c4',
              borderRadius: 'var(--ds-radius-md)',
              textTransform: 'none',
              fontSize: 'var(--ds-text-small)',
              padding: '0px var(--ds-space-3)',
              color: colors.text.secondary,
              backgroundColor: colors.background.white,
              height: '34px',
              width: width,
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              '&:hover': {
                backgroundColor: colors.background.tertiaryLightest,
                color: colors.text.secondary,
                border: '1px solid #e2e2e2c4',
              },
            }),
            ...sx, // Apply the passed sx prop, which will override any conflicting styles above
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
            <CalendarIcon />
            {!showOnlyCalenderIcon && displayText}
          </Box>
          <SafeIcon
            src={MenuArrowDownIcon}
            alt='dropdown arrow'
            className='custom-dropdown-icon'
            style={{
              height: '18px',
              width: '18px',
              transform: isPopoverOpen ? 'rotate(180deg)' : 'rotate(0deg)',
              opacity: '60%',
            }}
          />
        </Button>
      </Tooltip>
      <Popover
        id={id}
        open={open}
        anchorEl={anchorEl}
        onClose={handleCloseDatePicker}
        anchorOrigin={{
          vertical: 'bottom',
          horizontal: 'right',
        }}
        transformOrigin={{
          vertical: 'top',
          horizontal: 'right',
        }}
        sx={{
          '& .MuiPopover-paper': {
            width: showAbsoluteRange ? '500px' : '140px',
            height: '50vh',
            border: '1px solid var(--ds-blue-300)',
            boxShadow: '0 4px 8px rgba(0, 0, 0, 0.1)',
            borderRadius: 'var(--ds-radius-lg)',
          },
        }}
      >
        <LocalizationProvider dateAdapter={AdapterDayjs}>
          <Box
            container
            gap={2}
            sx={{ height: '100%', display: 'flex', flexDirection: 'row', justifyContent: 'space-between' }}
            gridTemplateColumns={'2 1'}
          >
            {showAbsoluteRange ? (
              <Box
                item
                xs={7}
                display={'flex'}
                justifyContent={'space-between'}
                flexDirection={'column'}
                sx={{ padding: 'var(--ds-space-4) var(--ds-space-5)', position: 'sticky', top: 0 }}
              >
                <Box>
                  <Typography variant='h6' color='textPrimary' marginBottom='16px'>
                    Absolute Date Range
                  </Typography>
                  <Box item xs={12} paddingBottom={2} paddingTop={2} marginBottom='8px'>
                    <DateTimePicker
                      label='From'
                      value={new Date(selectedDateTime.startTime)}
                      views={views}
                      components={{
                        OpenPickerIcon: CalendarIcon, // 👈 Replace this with any icon
                      }}
                      minDateTime={normalizedMinDate}
                      maxDateTime={dayjs(new Date())}
                      onChange={handleStartDateChange}
                      renderInput={(params) => (
                        <TextField
                          {...params}
                          size='small'
                          fullWidth
                          sx={{
                            fontSize: 'var(--ds-text-body-lg)',
                            '& .MuiOutlinedInput-root': {
                              height: '44px',
                              fontSize: 'var(--ds-text-body-lg)',
                              backgroundColor: 'var(--ds-background-100)',
                              '& fieldset': {
                                borderColor: 'var(--ds-brand-200)',
                              },
                              '&:hover fieldset': {
                                borderColor: 'var(--ds-brand-300)',
                              },
                              '&.Mui-focused fieldset': {
                                borderColor: 'var(--ds-blue-600)',
                                borderWidth: '2px',
                              },
                            },
                          }}
                        />
                      )}
                    />
                  </Box>
                  <Box item xs={12}>
                    <DateTimePicker
                      label='To'
                      value={new Date(selectedDateTime.endTime)}
                      views={views}
                      components={{
                        OpenPickerIcon: CalendarIcon, // 👈 Replace this with any icon
                      }}
                      minDateTime={normalizedMinDate}
                      maxDateTime={dayjs(new Date())}
                      onChange={handleEndDateChange}
                      renderInput={(params) => (
                        <TextField
                          {...params}
                          size='small'
                          fullWidth
                          sx={{
                            fontSize: 'var(--ds-text-body-lg)',
                            '& .MuiOutlinedInput-root': {
                              height: '44px',
                              fontSize: 'var(--ds-text-body-lg)',
                              backgroundColor: 'var(--ds-background-100)',
                              '& fieldset': {
                                borderColor: 'var(--ds-brand-200)',
                              },
                              '&:hover fieldset': {
                                borderColor: 'var(--ds-brand-300)',
                              },
                              '&.Mui-focused fieldset': {
                                borderColor: 'var(--ds-blue-600)',
                                borderWidth: '2px',
                              },
                            },
                          }}
                        />
                      )}
                    />
                  </Box>
                </Box>

                <Box item xs={12} paddingTop={2}>
                  <Box display='flex' justifyContent='flex-start'>
                    <CustomButton
                      variant='primary'
                      text='Apply Time Range'
                      onClick={() => handleApply(false)}
                      disabled={!(selectedDateTime.startTime && selectedDateTime.endTime)}
                      sx={{
                        fontSize: 'var(--ds-text-body-lg)',
                        padding: 'var(--ds-space-3) var(--ds-space-6)',
                        height: '40px',
                        minWidth: '240px',
                      }}
                    />
                  </Box>
                </Box>
              </Box>
            ) : null}

            <Box
              item
              xs={3}
              sx={{
                padding: 'var(--ds-space-2) var(--ds-space-3)',
                width: 'fit-content',
                overflowY: 'auto',
                borderLeft: '1px solid var(--ds-gray-200)',
                '&::-webkit-scrollbar': {
                  width: '4px',
                },
              }}
            >
              <Stack direction={'column'} gap={1}>
                {shortCuts.map((sc) => {
                  const isActive = selectedShortCut === sc && isShortcutSelected;
                  return (
                    <CustomButton
                      key={sc + 'key'}
                      text={sc}
                      variant='secondary'
                      onClick={() => handleShortcutClick(sc)}
                      sx={{
                        fontSize: 'var(--ds-text-small)',
                        width: '120px',
                        border: 'none',
                        backgroundColor: isActive ? colors.background.primaryLightest : 'transparent',
                        color: isActive ? colors.primary : 'inherit',
                        fontWeight: isActive ? 600 : 500,
                        boxShadow: 'none',
                      }}
                    />
                  );
                })}
              </Stack>
            </Box>
          </Box>
        </LocalizationProvider>
      </Popover>
    </>
  );
}

export default CustomDateTimeRangePicker;
