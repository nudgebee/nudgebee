import React from 'react';
import TextField from '@mui/material/TextField';
import Autocomplete from '@mui/material/Autocomplete';
import { inputSx, inputCustomSx } from '@data/themes/inputField';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { CircularProgress, Paper } from '@mui/material';
import { colors } from 'src/utils/colors';

interface AutocompleteProps {
  label: string;
  options: string[];
  value: string | null;
  onChange: (selectedValue: string | null) => void;
  toShowNoOption: boolean;
  width: number;
  onInputChange?: (selectedValue: string | null) => void;
  isLoading?: boolean;
}

const AutoCompleteInput: React.FC<AutocompleteProps> = ({
  width,
  value,
  label,
  options = [],
  toShowNoOption = true,
  onChange,
  onInputChange,
  isLoading = false,
}) => {
  const CustomPaper = (props: any) => {
    return <Paper sx={{ width: '100%', overflowY: 'auto' }} elevation={8} {...props} />;
  };

  return (
    <Autocomplete
      size='small'
      key={`auto-complete-${label}`}
      id={`auto-complete-${label}`}
      sx={{
        ...inputCustomSx,
        maxWidth: width || 200,
      }}
      blurOnSelect={'mouse'}
      value={value ?? null}
      options={options || []}
      popupIcon={<KeyboardArrowDownIcon className='custom-dropdown-icon' />}
      onChange={(_event, newValue) => {
        onChange(newValue);
      }}
      disabled={options.length == 0}
      renderInput={(params) => (
        <TextField
          {...params}
          label={label}
          sx={inputSx}
          onChange={(e) => onInputChange?.(e.target.value)}
          size='small'
          InputProps={{
            ...params.InputProps,
            endAdornment: (
              <>
                {isLoading ? <CircularProgress size={20} /> : null}
                {params.InputProps.endAdornment}
              </>
            ),
          }}
        />
      )}
      ListboxProps={{
        sx: {
          wordBreak: 'break-word',
          '& .MuiAutocomplete-option': {
            borderBottom: `1px solid ${colors.border.autocompleteOption}`,
            '&:last-child': {
              borderBottom: 'none',
            },
          },
        },
      }}
      PaperComponent={CustomPaper}
      isOptionEqualToValue={(option, _v) => option === _v}
      freeSolo={!toShowNoOption}
    />
  );
};

export default AutoCompleteInput;
