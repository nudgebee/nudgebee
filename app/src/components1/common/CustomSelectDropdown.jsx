import * as React from 'react';
import InputLabel from '@mui/material/InputLabel';
import MenuItem from '@mui/material/MenuItem';
import FormControl from '@mui/material/FormControl';
import Select from '@mui/material/Select';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import PropTypes from 'prop-types';

const CustomSelectDropdown = ({
  value,
  onChange,
  options,
  color = '',
  label = '',
  minWidth = '150px',
  height,
  inputMaxWidth = '500px',
  ml = 0,
  mr = 0,
  showAll = false,
  customIcon: CustomIcon = KeyboardArrowDownIcon,
  isDisabled = false,
  noBorder = false,
  fontSize = '13px',
}) => {
  const styles = {
    noBorder: {
      '.MuiOutlinedInput-notchedOutline': {
        border: '0px',
      },
      '&.MuiOutlinedInput-root:hover .MuiOutlinedInput-notchedOutline': {
        border: 0,
      },
      '&.MuiOutlinedInput-root.Mui-focused .MuiOutlinedInput-notchedOutline': {
        border: 0,
      },
      '.MuiOutlinedInput-input': { padding: 'var(--ds-space-1) var(--ds-space-4) var(--ds-space-1) 0px', maxWidth: inputMaxWidth },
      '.MuiBox-root': { overflowX: 'clip' },
    },
    withBorder: {
      '.MuiOutlinedInput-notchedOutline': {
        border: '1px solid var(--ds-brand-200)',
        borderColor: 'var(--ds-brand-300)',
        borderRadius: 'var(--ds-radius-md)',
      },
      '.MuiOutlinedInput-input': { padding: 'var(--ds-space-1) var(--ds-space-4)', maxWidth: inputMaxWidth },
      '.MuiBox-root': { overflowX: 'clip' },
    },
  };

  let borderStyle;
  if (noBorder) {
    borderStyle = styles.noBorder;
  } else {
    borderStyle = styles.withBorder;
  }

  const [selectedValue, setSelectedValue] = React.useState(value || '');

  React.useEffect(() => {
    setSelectedValue(value);
  }, [value]);

  const handleOnChange = (e, v) => {
    setSelectedValue(e?.target?.value);
    onChange(e, v);
  };

  return (
    <FormControl size='small' sx={{ borderRadius: 'var(--ds-radius-xl)', minWidth, ml, mr }}>
      <InputLabel
        sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', overflowX: 'clip' }}
        id='demo-simple-select-label'
      >
        {label}
      </InputLabel>
      <Select
        labelId='demo-simple-select-label'
        id='demo-simple-select'
        value={selectedValue || ''}
        label={label}
        onChange={handleOnChange}
        defaultValue={''}
        disabled={options?.length == 0 || isDisabled ? true : false}
        IconComponent={(props) => (
          <>
            <CustomIcon sx={{ height: '18px', width: '18px', color: 'var(--ds-brand-500)', top: 'unset !important' }} {...props} />{' '}
          </>
        )}
        sx={{
          backgroundColor: color,
          color: 'var(--ds-brand-500)',
          fontWeight: 'var(--ds-font-weight-medium)',
          fontSize,
          height: height ?? 'auto',

          ...borderStyle,
        }}
      >
        {showAll && (
          <MenuItem key={'__ALL__'} value={'__ALL__'}>
            <b>
              <em>All</em>
            </b>
          </MenuItem>
        )}
        {options?.map((option, idx) => (
          <MenuItem key={idx} value={option?.value || option}>
            {option?.label || option}
          </MenuItem>
        ))}
      </Select>
    </FormControl>
  );
};

export default CustomSelectDropdown;

CustomSelectDropdown.propTypes = {
  height: PropTypes.any,
};
