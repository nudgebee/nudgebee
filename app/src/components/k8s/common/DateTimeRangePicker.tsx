import { TextField, styled } from '@mui/material';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import dayjs, { type Dayjs } from 'dayjs';
import type { CalendarOrClockPickerView } from '@mui/x-date-pickers/internals/models';

interface DateTimeRangePickerProps {
  handleStartDateEndDate: (type: string, date: Dayjs | null) => void;
  startDate: Dayjs;
  endDate: Dayjs | null;
  views: CalendarOrClockPickerView[];
  minDate: Dayjs | null;
  maxDateTime: Dayjs | null;
  disableStartDate: boolean;
  disableEndDate?: boolean;
}

const DateTimeRangePicker: React.FC<DateTimeRangePickerProps> = ({
  handleStartDateEndDate,
  startDate,
  endDate,
  views,
  minDate = dayjs().subtract(1, 'month'),
  maxDateTime = dayjs(new Date()),
  disableStartDate = false,
  disableEndDate = false,
}) => {
  const handleStartDateChange = (date: Dayjs) => {
    handleStartDateEndDate('start', date);
  };

  const handleEndDateChange = (date: Dayjs | null) => {
    handleStartDateEndDate('end', date);
  };

  const StyledTextField = styled(TextField)({
    '& .MuiInputBase-root': {
      height: '42px',
    },
  });

  return (
    <LocalizationProvider dateAdapter={AdapterDayjs}>
      <DateTimePicker
        disabled={disableStartDate}
        label='Start Date'
        value={startDate}
        onChange={(newValue) => handleStartDateChange(newValue as Dayjs)}
        views={views}
        maxDateTime={maxDateTime}
        minDate={!disableStartDate ? minDate : undefined}
        renderInput={(props) => <StyledTextField {...props} id='start-date' />}
      />
      <DateTimePicker
        disabled={disableEndDate}
        label='End Date'
        value={endDate}
        minDate={minDate}
        maxDateTime={maxDateTime}
        views={views}
        onChange={(newValue) => handleEndDateChange(newValue as Dayjs)}
        renderInput={(props) => (
          <StyledTextField
            {...props}
            error={false}
            id='end-date'
            onChange={(e) => {
              if (e.target.value === '') {
                handleEndDateChange(null);
              }
            }}
          />
        )}
      />
    </LocalizationProvider>
  );
};

export default DateTimeRangePicker;
