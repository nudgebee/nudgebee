import React from 'react';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';

import { TextField, Typography, Box } from '@mui/material';
import { colors } from 'src/utils/colors';

const CustomDateTimePicker = ({ label, value, onChange, views = ['day', 'hours', 'minutes'], format = 'MM/DD/YYYY hh:mm A' }) => {
  const handleChange = (newValue) => {
    if (onChange) {
      onChange(newValue);
    }
  };

  return (
    <LocalizationProvider dateAdapter={AdapterDayjs}>
      <Box sx={{ width: '221px' }}>
        <Typography sx={{ color: colors.text.tertiary, fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
          {label}
        </Typography>
        <DateTimePicker
          value={value}
          onChange={handleChange}
          renderInput={(params) => (
            <TextField
              sx={{ borderRadius: 'var(--ds-radius-sm)', border: `0.5px solid ${colors.border.secondary}` }}
              size='small'
              variant='outlined'
              {...params}
              fullWidth
            />
          )}
          views={views}
          inputFormat={format}
        />
      </Box>
    </LocalizationProvider>
  );
};

export default CustomDateTimePicker;
