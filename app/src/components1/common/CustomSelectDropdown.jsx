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
      '.MuiOutlinedInput-input': { padding: '5px 15px 5px 0px', maxWidth: inputMaxWidth },
      '.MuiBox-root': { overflowX: 'clip' },
    },
    withBorder: {
      '.MuiOutlinedInput-notchedOutline': {
        border: '1px solid #D5D5D5',
        borderColor: '#B9B9B9',
        borderRadius: '6px',
      },
      '.MuiOutlinedInput-input': { padding: '5px 15px', maxWidth: inputMaxWidth },
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
    <FormControl size='small' sx={{ borderRadius: '20px', minWidth, ml, mr }}>
      <InputLabel sx={{ color: '#374151', fontSize: '12px', fontWeight: 500, overflowX: 'clip' }} id='demo-simple-select-label'>
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
            <CustomIcon sx={{ height: '18px', width: '18px', color: '#374151', top: 'unset !important' }} {...props} />{' '}
          </>
        )}
        sx={{
          backgroundColor: color,
          color: '#374151',
          fontWeight: 500,
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
