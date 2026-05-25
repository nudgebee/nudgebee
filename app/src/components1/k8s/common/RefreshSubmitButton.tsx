import React from 'react';
import SyncIcon from '@mui/icons-material/Sync';
import { ToggleButtonGroup, ToggleButton, autocompleteClasses, Divider, Typography, Box } from '@mui/material';
import CustomDropdown from '@components1/common/CustomDropdown';
import { colors } from 'src/utils/colors';

interface RefreshSubmitButtonProps {
  loading: boolean;
  interval: number;
  onSubmit: () => void;
  setInterval: (interval: number) => void;
  disabled?: boolean;
}
const minWidth = 'auto';
const timerOptions = [
  {
    label: 'Off',
    value: 0,
  },
  {
    label: 'Live',
    value: 5,
  },
  {
    label: '10s',
    value: 10,
  },
  {
    label: '15s',
    value: 15,
  },
  {
    label: '30s',
    value: 30,
  },
  {
    label: '45s',
    value: 45,
  },
  {
    label: '60s',
    value: 60,
  },
];

export const RefreshSubmitButton: React.FC<RefreshSubmitButtonProps> = ({ loading = false, interval, onSubmit, setInterval, disabled = false }) => {
  return (
    <ToggleButtonGroup
      size='small'
      aria-label='text formatting'
      sx={{
        textTransform: 'unset',
        boxShadow: 'unset',
        color: 'black',
        fontSize: '0.875rem',
        fontWeight: 500,
        width: minWidth,
        height: '33px',
        border: 'none',
      }}
    >
      <ToggleButton
        value='underlined'
        onClick={() => {
          onSubmit();
        }}
        disabled={loading || disabled}
        sx={{
          minWidth: 108,
          gap: '4px',
          backgroundColor: '#3B82F6',
          '&:hover': {
            boxShadow: 'unset',
            backgroundColor: '#60A5FA',
          },
          '&.Mui-disabled': {
            backgroundColor: '#94A3B8',
            color: '#E5E7EB',
            cursor: 'not-allowed',
            opacity: 0.6,
          },
        }}
      >
        <SyncIcon
          sx={{
            color: 'white',
            animation: loading ? 'spin 2s linear infinite' : '',
            '@keyframes spin': {
              '0%': { transform: 'rotate(360deg)' },
              '100%': { transform: 'rotate(0deg)' },
            },
            '&.Mui-disabled': {
              color: '#E5E7EB',
            },
          }}
        />
        <Typography
          sx={{
            fontSize: '14px',
            color: colors.text.white,
            fontWeight: 500,
            textTransform: 'none',
            '&.Mui-disabled': {
              color: '#E5E7EB',
            },
          }}
        >
          Run Query
        </Typography>
      </ToggleButton>

      <Divider flexItem orientation='vertical' sx={{ mx: 0, my: 0, width: '1px', backgroundColor: 'rgba(255,255,255,0.2)' }} />
      <Box
        component='button'
        type='button'
        title='Auto Refresh'
        aria-label='Auto Refresh'
        sx={{
          background: 'none',
          border: 'none',
          font: 'inherit',
          m: 0,
          p: 0,
          minWidth: 60,
          width: 60,
          color: colors.text.white,
          backgroundColor: '#3B82F6',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          cursor: 'pointer',
          borderRadius: '0 4px 4px 0',

          '&:hover': {
            boxShadow: 'unset',
            backgroundColor: '#60A5FA',
          },
        }}
      >
        <CustomDropdown
          options={timerOptions}
          minWidth='100px'
          label=''
          onChange={(e) => setInterval(e.target.value as number)}
          value={interval}
          inputVariant='standard'
          customStyle={{
            m: '0 !important',
            p: '0 4px 0 !important',
            width: '100px !important',
            minHeight: '31px',
            border: '0px !important',
            color: colors.text.white,
            borderRadius: '2px',
            '&:hover': {
              boxShadow: 'unset',
              backgroundColor: '#60A5FA',
            },

            '& .MuiFormControl-root': {
              m: '3px 0 0px 0!important',
              p: '0 !important',
            },
            '& .MuiAutocomplete-popupIndicator svg': {
              color: colors.text.white,
            },
            '& .MuiAutocomplete-popupIndicator': {
              color: `${colors.text.white} !important`,
            },
            '& .MuiAutocomplete-popupIndicator img': {
              filter: 'brightness(0) saturate(100%) invert(100%)',
            },
            '& .MuiAutocomplete-clearIndicator svg': {
              display: 'none',
            },
            [`& .${autocompleteClasses.inputRoot}::before,  .${autocompleteClasses.input}::before `]: {
              border: '0px !important',
              color: colors.text.white,
            },
            [`& .${autocompleteClasses.inputRoot},  .${autocompleteClasses.input} `]: {
              border: '0px !important',
              color: colors.text.white,
              paddingRight: '0 !important',
            },
          }}
        />
      </Box>
    </ToggleButtonGroup>
  );
};
