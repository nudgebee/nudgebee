import { styled } from '@mui/material/styles';
import Switch from '@mui/material/Switch';
import { colors } from 'src/utils/colors';

export const inputCustomSx = {
  '&.MuiTextField-root': {
    '& .MuiOutlinedInput-root': {
      padding: '0px',
    },
  },
  '& .MuiFormLabel-root': {
    overflow: 'visible !important',
  },
  '&.MuiTextField-root:hover .css-5wvipj-MuiFormLabel-root-MuiInputLabel-root': {
    color: `${colors.text.primary} !important`,
  },
  '&.MuiAutocomplete-root:hover .css-5wvipj-MuiFormLabel-root-MuiInputLabel-root': {
    color: `${colors.text.primary} !important`,
  },

  '& .MuiOutlinedInput-root': {
    padding: 'var(--ds-space-1) var(--ds-space-3)',
  },

  '& .MuiOutlinedInput-root:hover': {
    label: {
      color: `${colors.text.red} !important`,
    },
    '& input': {
      color: colors.text.secondary,
    },

    '& > fieldset': { border: '0.5px solid', borderColor: colors.text.primary },
  },
  '& .MuiOutlinedInput-root.Mui-focused': {
    '& > fieldset': {
      border: '0.5px solid',
      borderColor: colors.text.primary,
    },
  },
};

export const inputSx = {
  '& .MuiFormHelperText-root': {
    ml: '0',
  },
  '& .MuiFormLabel-root': {
    lineHeight: '1.2 !important',
    overFlow: 'initial !important',
  },
  '& .MuiInputLabel-root': { color: colors.text.tertiary, fontWeight: 'var(--ds-font-weight-regular)', fontSize: 'var(--ds-text-body)' },
  '& .MuiInputLabel-root.Mui-focused': { color: colors.text.primary },
  '& .MuiInputLabel-root.Mui-error': { color: colors.text.errorText },
  '& .MuiInputLabel-root.Mui-error.Mui-focused': { color: colors.text.errorText },
  '& .MuiOutlinedInput-root': {
    backgroundColor: colors.background.white,
    cursor: 'pointer !important',
    padding: 'var(--ds-space-1) var(--ds-space-3)',
    borderRadius: 'var(--ds-radius-sm)',
    color: colors.text.secondary,
    border: 'none !important',

    '& input': {
      cursor: 'pointer !important',
      color: colors.text.secondary,
      fontSize: 'var(--ds-text-body)',
      border: 'none !important',
      padding: '0px !important',
      fontWeight: 'var(--ds-font-weight-medium)',
      '& ::placeholder': {
        color: colors.text.secondaryDark,
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-regular)',
      },
      '&.MuiAutocomplete-input': {
        padding: 'var(--ds-space-1) var(--ds-space-1) 0px var(--ds-space-2) !important',
      },
    },
  },
  '& .MuiOutlinedInput-root.Mui-error:hover': {
    '& > fieldset': {
      border: '1px solid',
      borderColor: colors.border.errorOutline,
    },
  },
  '& .MuiOutlinedInput-root.Mui-error.Mui-focused': {
    '& > fieldset': {
      border: '1px solid',
      borderColor: colors.border.errorOutline,
    },
  },
  '& .MuiOutlinedInput-root.Mui-focused': {
    '& > fieldset': {
      border: '2px solid',
      borderColor: colors.text.primary,
    },
  },
};

export const IOSSwitch = styled((props) => <Switch focusVisibleClassName='.Mui-focusVisible' disableRipple {...props} />)(({ theme }) => ({
  width: 42,
  height: 26,
  padding: 0,
  '& .MuiSwitch-switchBase': {
    padding: 0,
    margin: 2,
    transitionDuration: '300ms',
    '&.Mui-checked': {
      transform: 'translateX(16px)',
      color: colors.text.white,
      '& + .MuiSwitch-track': {
        backgroundColor: theme.palette.mode === 'dark' ? colors.border.switchTrack : colors.border.switchTrackLight,
        opacity: 1,
        border: 0,
      },
      '&.Mui-disabled + .MuiSwitch-track': {
        opacity: 0.5,
      },
    },
    '&.Mui-focusVisible .MuiSwitch-thumb': {
      color: colors.text.switchThumb,
      border: `6px solid ${colors.border.white}`,
    },
    '&.Mui-disabled .MuiSwitch-thumb': {
      color: theme.palette.mode === 'light' ? theme.palette.grey[100] : theme.palette.grey[600],
    },
    '&.Mui-disabled + .MuiSwitch-track': {
      opacity: theme.palette.mode === 'light' ? 0.7 : 0.3,
    },
  },
  '& .MuiSwitch-thumb': {
    boxSizing: 'border-box',
    width: 22,
    height: 22,
  },
  '& .MuiSwitch-track': {
    borderRadius: 26 / 2,
    backgroundColor: theme.palette.mode === 'light' ? '#E9E9EA' : '#39393D',
    opacity: 1,
    transition: theme.transitions.create(['background-color'], {
      duration: 500,
    }),
  },
}));

export const TGSwitch = styled((props) => <Switch focusVisibleClassName='.Mui-focusVisible' disableRipple {...props} />)(({ theme }) => ({
  width: 54,
  height: 24,
  padding: 0,
  '& .MuiSwitch-switchBase': {
    padding: 0,
    margin: 2,
    transitionDuration: '300ms',
    '&.Mui-checked': {
      transform: 'translateX(28px)',
      color: colors.text.white,
      '& + .MuiSwitch-track': {
        backgroundColor: theme.palette.mode === 'dark' ? colors.background.switchTrackDark : colors.background.switchTrackLightest,
        opacity: 1,
        border: 0,
      },
      '&.Mui-disabled + .MuiSwitch-track': {
        opacity: 0.5,
      },
    },
    '&.Mui-focusVisible .MuiSwitch-thumb': {
      color: colors.text.switchThumb,
      border: `6px solid ${colors.border.white}`,
    },
    '&.Mui-disabled .MuiSwitch-thumb': {
      color: theme.palette.mode === 'light' ? theme.palette.grey[100] : theme.palette.grey[600],
    },
    '&.Mui-disabled + .MuiSwitch-track': {
      opacity: theme.palette.mode === 'light' ? 0.7 : 0.3,
    },
  },
  '& .MuiSwitch-thumb': {
    boxSizing: 'border-box',
    width: 20,
    height: 20,
  },
  '& .MuiSwitch-track': {
    borderRadius: 26 / 2,
    backgroundColor: theme.palette.mode === 'light' ? '#E9E9EA' : '#39393D',
    opacity: 1,
    transition: theme.transitions.create(['background-color'], {
      duration: 500,
    }),

    '&:before, &:after': {
      position: 'absolute',
      top: '50%',
      transform: 'translateY(-50%)',
      width: 16,
      height: 15,
      fontSize: 'var(--ds-text-caption)',
      fontWeight: 'var(--ds-font-weight-semibold)',
    },
    '&:before': {
      content: '"ON"',
      left: 8,
      color: colors.text.white,
    },
    '&:after': {
      content: '"OFF"',
      right: 8,
    },
  },
}));
