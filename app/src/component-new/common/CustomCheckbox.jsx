import React from 'react';
import { Checkbox, FormControlLabel, Typography, styled } from '@mui/material';
import { colors } from 'src/utils/colors';

// Custom styled checkbox with brand colors
const StyledCheckbox = styled(Checkbox)({
  '&.MuiCheckbox-root': {
    color: colors.border.secondary,
    '&:hover': {
      backgroundColor: colors.background.primaryLightest,
    },
    '&.Mui-checked': {
      color: colors.primary,
      '&:hover': {
        backgroundColor: colors.background.primaryLightest,
      },
    },
    '&.Mui-disabled': {
      color: colors.text.disabledInput,
      '&.Mui-checked': {
        color: colors.text.disabledInput,
      },
    },
  },
  '& .MuiSvgIcon-root': {
    fontSize: 20,
    borderRadius: 'var(--ds-radius-sm)',
  },
});

const CheckboxComponent = ({ disabled, checked, onChange, checkboxStyle, ...rest }) => {
  return (
    <StyledCheckbox
      size='small'
      disabled={disabled}
      checked={checked ?? false}
      onChange={onChange}
      style={{
        paddingBlock: '6px',
        paddingInline: '4px',
        ...checkboxStyle,
      }}
      {...rest}
    />
  );
};

const CustomCheckBox = ({
  top = 0,
  bottom = 0,
  checked,
  onChange,
  disabled,
  text,
  startElement,
  endElement,
  checkboxStyle,
  checkboxClassName,
  indeterminate,
  className,
  name,
}) => {
  return text || startElement || endElement ? (
    <FormControlLabel
      name={name}
      className={className}
      style={{
        marginTop: top,
        marginBottom: bottom,
        alignItems: 'center',
        gap: '4px',
      }}
      control={
        <CheckboxComponent
          id={'checkbox'}
          className={checkboxClassName}
          disabled={disabled}
          checked={checked ?? false}
          onChange={onChange}
          checkboxStyle={checkboxStyle}
          indeterminate={indeterminate}
        />
      }
      label={
        <span
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            gap: '6px',
            minHeight: '24px',
          }}
        >
          {startElement ? startElement : ''}
          <Typography
            fontSize={'var(--ds-text-body-lg)'}
            fontWeight={400}
            color={disabled ? colors.text.disabledInput : colors.text.secondary}
            sx={{
              lineHeight: 1.4,
              transition: 'color 0.2s ease-in-out',
            }}
          >
            {text}
          </Typography>
          {endElement ? endElement : ''}
        </span>
      }
    />
  ) : (
    <CheckboxComponent
      className={checkboxClassName}
      disabled={disabled}
      checked={checked ?? false}
      onChange={onChange}
      checkboxStyle={checkboxStyle}
      indeterminate={indeterminate}
    />
  );
};

export default CustomCheckBox;
