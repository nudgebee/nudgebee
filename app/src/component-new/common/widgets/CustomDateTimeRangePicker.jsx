/**
 * CustomDateTimeRangePicker (v2) — DS-tokenised port of
 *   app/src/components1/common/widgets/CustomDateTimeRangePicker.jsx.
 *
 * Range date+time picker with a trigger button and a popover that holds:
 *  - "Absolute Date Range" with From/To DateTimePickers + Apply
 *  - A side rail of shortcuts (Last 5 min … Current Week)
 *
 * External API preserved: `passedSelectedDateTime: { startTime, endTime,
 * shortcutClickTime? }` + `onChange({ selection })`. Trigger / shortcut /
 * apply buttons are now `ds/Button`. All raw hex / colors.* values
 * migrated to ds.* tokens.
 */
import React, { useEffect, useState } from 'react';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { TextField, Popover, Box, Stack, Typography, Fade } from '@mui/material';
import { calendarViewWeek, MenuArrowDownIcon } from '@assets';
import dayjs from 'dayjs';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { TIME_PICK_SHORTCUTS } from '@data/constants';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTooltip from '@components1/common/CustomTooltip';
import { Button as DsButton } from '@components1/ds/Button';

// Shortcut → window-size in ms. Hoisted to module scope so it isn't reallocated
// on every shortcut click.
const TIME_ADJUSTMENTS = {
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

const CalendarIcon = () => (
  <Box
    component='img'
    sx={{
      height: ds.space[5],
      width: ds.space[4],
    }}
    alt='calendar'
    src={calendarViewWeek.default.src}
  />
);

function CustomDateTimeRangePicker({
  passedSelectedDateTime = {
    startTime: new Date().getTime() - 3600 * 1000,
    endTime: new Date().getTime(),
    shortcutClickTime: 0,
  },
  onChange,
  views = ['day', 'hours', 'minutes'],
  minDate = dayjs().subtract(2, 'month'),
  width = ds.space.mul(0, 90),
  resetDateTime,
  shortCuts = TIME_PICK_SHORTCUTS,
  showAbsoluteRange = true,
  showOnlyCalenderIcon = false,
  sx = {},
}) {
  const [selectedDateTime, setSelectedDateTime] = useState({
    startTime: passedSelectedDateTime.startTime,
    endTime: passedSelectedDateTime.endTime,
  });
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

      let finalShortcutClickTime = 0;
      if (fromSelectedShortHour && selectedDateTime.shortcutClickTime) {
        finalShortcutClickTime = selectedDateTime.shortcutClickTime;
      } else if (isShortcutSelected && selectedDateTime.shortcutClickTime) {
        finalShortcutClickTime = selectedDateTime.shortcutClickTime;
      }

      const selectionObject = {
        startTime: selectedDateTime.startTime,
        endTime: selectedDateTime.endTime,
        shortcutClickTime: finalShortcutClickTime,
      };

      onChange({ selection: selectionObject });
    }
    setAnchorEl(null);
  };

  const handleShortcutClick = (shortcut) => {
    const now = new Date();
    let shortcutClickTime = 0;
    let date = now.getTime();
    const minDateValue = normalizedMinDate.toDate();
    let endTime = now.getTime();

    if (TIME_ADJUSTMENTS[shortcut]) {
      shortcutClickTime = TIME_ADJUSTMENTS[shortcut];
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

    const newDateTimeObj = { startTime: date, endTime, shortcutClickTime };
    setSelectedDateTime((prevState) => ({ ...prevState, ...newDateTimeObj }));

    if (onChange) onChange({ selection: newDateTimeObj });
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
    if (isShortcutSelected) return selectedShortCut;

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

  const fieldSx = {
    fontSize: ds.text.bodyLg,
    '& .MuiOutlinedInput-root': {
      height: ds.space.mul(0, 22),
      fontSize: ds.text.bodyLg,
      backgroundColor: ds.background[100],
      '& fieldset': { borderColor: ds.gray[300] },
      '&:hover fieldset': { borderColor: ds.gray[400] },
      '&.Mui-focused fieldset': { borderColor: ds.brand[500], borderWidth: ds.space[0] },
    },
  };

  return (
    <>
      <CustomTooltip
        title={`${new Date(selectedDateTime?.startTime ?? Date.now()).toLocaleString()} - ${new Date(
          selectedDateTime?.endTime ?? Date.now()
        ).toLocaleString()}`}
      >
        <Box
          component='button'
          type='button'
          aria-describedby={id}
          onClick={handleOpenDatePicker}
          sx={{
            ...(!showOnlyCalenderIcon && {
              // Field chrome — mirrors ds/Input and ds/Select so the date trigger
              // sits cleanly beside Input / Select rows in a toolbar.
              // Height 32px matches the sm default of FilterDropdown (toolbar density).
              boxShadow: 'none',
              outline: 'none',
              border: `1px solid ${ds.gray[300]}`,
              borderRadius: ds.radius.md,
              textTransform: 'none',
              fontFamily: 'inherit',
              fontSize: ds.text.small,
              padding: `0 ${ds.space[3]}`,
              color: ds.gray[700],
              backgroundColor: ds.background[100],
              height: ds.space[6],
              width: width,
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              gap: ds.space[2],
              cursor: 'pointer',
              boxSizing: 'border-box',
              transition: `border-color ${ds.motion.micro} ${ds.motion.ease}, box-shadow ${ds.motion.micro} ${ds.motion.ease}, background-color ${ds.motion.micro} ${ds.motion.ease}`,
              '&:hover': {
                backgroundColor: ds.background[200],
                borderColor: ds.gray[400],
              },
              '&:focus-visible': {
                borderColor: ds.blue[500],
                boxShadow: `0 0 0 3px ${ds.blue[100]}`,
              },
              ...(isPopoverOpen && {
                borderColor: ds.blue[500],
                boxShadow: `0 0 0 3px ${ds.blue[100]}`,
              }),
            }),
            ...sx,
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <CalendarIcon />
            {!showOnlyCalenderIcon && displayText}
          </Box>
          <SafeIcon
            src={MenuArrowDownIcon}
            alt='dropdown arrow'
            className='custom-dropdown-icon'
            style={{
              height: ds.space.mul(0, 9),
              width: ds.space.mul(0, 9),
              transform: isPopoverOpen ? 'rotate(180deg)' : 'rotate(0deg)',
              opacity: '60%',
            }}
          />
        </Box>
      </CustomTooltip>
      <Popover
        id={id}
        open={open}
        anchorEl={anchorEl}
        onClose={handleCloseDatePicker}
        TransitionComponent={Fade}
        TransitionProps={{ timeout: 200 }}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        sx={{
          // Surface chrome mirrors the shared --ds-overlay-* tokens used by
          // DropdownMenu / Select / FilterDropdown — so this popup feels like
          // part of the same family. Not using OverlaySurface itself because it
          // wraps MUI Menu (MenuList semantics) and the date picker's content
          // (DateTimePickers + Apply + shortcuts rail) isn't a list of items.
          '& .MuiPopover-paper': {
            width: showAbsoluteRange ? ds.space.mul(0, 250) : ds.space.mul(0, 70),
            height: '51vh',
            marginTop: 'var(--ds-overlay-anchor-gap)',
            backgroundColor: 'var(--ds-overlay-bg)',
            borderRadius: 'var(--ds-overlay-radius)',
            border: 'none',
            boxShadow: 'var(--ds-overlay-shadow)',
            overflow: 'hidden',
            animation: 'dateRangePopoverEnter var(--ds-overlay-enter-duration) var(--ds-overlay-enter-easing)',
            animationFillMode: 'backwards',
            '@keyframes dateRangePopoverEnter': {
              '0%': { transform: 'scaleY(0.9) translateY(-8px)' },
              '100%': { transform: 'scaleY(1) translateY(0)' },
            },
          },
        }}
      >
        <LocalizationProvider dateAdapter={AdapterDayjs}>
          <Box sx={{ height: '100%', display: 'flex', flexDirection: 'row', justifyContent: 'space-between' }}>
            {showAbsoluteRange ? (
              <Box display='flex' justifyContent='space-between' flexDirection='column' sx={{ padding: `${ds.space[5]} ${ds.space[5]}`, flex: 1 }}>
                <Box>
                  <Typography
                    sx={{
                      fontSize: ds.text.title,
                      fontWeight: ds.weight.semibold,
                      color: ds.gray[700],
                      mb: ds.space[3],
                    }}
                  >
                    Absolute Date Range
                  </Typography>
                  <Box sx={{ pt: ds.space[2], pb: ds.space[2], mb: ds.space[2] }}>
                    <DateTimePicker
                      label='From'
                      value={new Date(selectedDateTime.startTime)}
                      views={views}
                      components={{ OpenPickerIcon: CalendarIcon }}
                      minDateTime={normalizedMinDate}
                      maxDateTime={dayjs(new Date())}
                      onChange={handleStartDateChange}
                      renderInput={(params) => <TextField {...params} size='small' fullWidth sx={fieldSx} />}
                    />
                  </Box>
                  <Box>
                    <DateTimePicker
                      label='To'
                      value={new Date(selectedDateTime.endTime)}
                      views={views}
                      components={{ OpenPickerIcon: CalendarIcon }}
                      minDateTime={normalizedMinDate}
                      maxDateTime={dayjs(new Date())}
                      onChange={handleEndDateChange}
                      renderInput={(params) => <TextField {...params} size='small' fullWidth sx={fieldSx} />}
                    />
                  </Box>
                </Box>

                <Box sx={{ pt: ds.space[3], display: 'flex', justifyContent: 'flex-start' }}>
                  <DsButton
                    tone='primary'
                    size='md'
                    onClick={() => handleApply(false)}
                    disabled={!(selectedDateTime.startTime && selectedDateTime.endTime)}
                    data-testid='date-range-apply'
                  >
                    Apply Time Range
                  </DsButton>
                </Box>
              </Box>
            ) : null}

            <Box
              sx={{
                padding: `${ds.space[2]} ${ds.space[3]}`,
                width: 'fit-content',
                overflowY: 'auto',
                borderLeft: `1px solid ${ds.gray[200]}`,
                '&::-webkit-scrollbar': { width: ds.space[1] },
              }}
            >
              <Stack direction='column' gap={ds.space[2]}>
                {shortCuts.map((sc) => {
                  const isActive = selectedShortCut === sc && isShortcutSelected;
                  return (
                    <DsButton
                      key={sc + 'key'}
                      tone='ghost'
                      size='sm'
                      onClick={() => handleShortcutClick(sc)}
                      data-testid={`date-range-shortcut-${sc.replace(/\s+/g, '-').toLowerCase()}`}
                      // The ghost tone already paints brand-tinted backgrounds on hover,
                      // but the *active* (selected) shortcut needs a persistent tint to
                      // signal which one drove the current range — that's what the wrapper
                      // span below provides.
                    >
                      <Box
                        component='span'
                        sx={{
                          width: ds.space.mul(0, 60),
                          textAlign: 'left',
                          fontWeight: isActive ? ds.weight.semibold : ds.weight.medium,
                          color: isActive ? ds.brand[600] : 'inherit',
                          backgroundColor: isActive ? ds.brand[100] : 'transparent',
                          padding: `${ds.space[1]} ${ds.space[2]}`,
                          borderRadius: ds.radius.sm,
                        }}
                      >
                        {sc}
                      </Box>
                    </DsButton>
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
