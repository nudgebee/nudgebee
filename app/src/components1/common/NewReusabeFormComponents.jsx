import React, { useState } from 'react';
import { Box, Typography, Card, TextField, FormControlLabel, Checkbox, Divider, IconButton } from '@mui/material';
import { ExpandMore, ExpandLess } from '@mui/icons-material';
import PropTypes from 'prop-types';
import { colors } from '@utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import { Textarea } from '@components1/k8s/common/TextArea';
import CustomDropdown from '@components1/common/CustomDropdown';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';

// Shared styles from the original CreateAgent component
const styles = {
  label: {
    fontSize: '13px',
    fontWeight: 500,
    color: colors.text.secondary,
  },
  inputField: {
    fontSize: '12px',
    '& .MuiOutlinedInput-root': {
      borderRadius: '6px',
      backgroundColor: 'white',
      fontSize: '14px',

      '&.Mui-error fieldset': {
        borderColor: colors.border.error,
        borderWidth: '1px',
      },
      '& fieldset': {
        borderColor: colors.border.vertical,
      },
      '&:hover fieldset': {
        borderColor: colors.border.primaryLightest,
      },
      '&.Mui-focused fieldset': {
        borderColor: colors.border.primary,
        borderWidth: '2px',
      },
    },
    '& .MuiInputBase-input': {
      padding: '8px 12px',
      '&::placeholder': {
        color: colors.text.tertiarymedium,
        fontWeight: 400,
        fontSize: '12px',
        opacity: 1,
      },
    },
  },
  requiredField: {
    '& .MuiOutlinedInput-root': {
      '& fieldset': {
        borderColor: colors.border.error,
      },
    },
  },
  errorText: {
    color: colors.border.error,
    fontSize: '12px',
    fontWeight: 500,
    mt: 1,
  },
  instructionText: {
    fontSize: '11px',
    color: colors.text.secondaryDark,
    fontWeight: 300,
    mb: '8px',
  },
  requiredStar: {
    color: colors.border.error,
  },
  // Card styles
  card: {
    backgroundColor: colors.background.white,
    borderRadius: '8px',
    border: `1px solid ${colors.border.vertical}`,
    boxShadow: '0px 10px 15px -6px rgba(0, 0, 0, 0.1), 0px 6px 14px -5px rgba(50, 37, 93, 0.1)',
    overflow: 'visible',
    mb: 3,
    gap: '24px',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
  },
  headerWithIcon: {
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
  },
  title: {
    fontSize: '16px',
    fontWeight: 500,
    color: colors.text.secondary,
  },
  description: {
    fontSize: '12px',
    color: colors.text.secondaryDark,
    fontWeight: 300,
  },
  content: {
    padding: '20px 24px',
  },
  fieldsContainer: {
    display: 'grid',
    gridTemplateColumns: '1fr 1fr',
    gap: '36px',
    padding: '0 16px',
  },
  singleColumnContainer: {
    display: 'flex',
    flexDirection: 'column',
    gap: '24px',
  },
  fieldContainer: {
    display: 'flex',
    flexDirection: 'column',
    width: '100%',
  },
};

// ============================
// FormCard Component
// ============================
export const FormCard = ({ title, description, icon, number, children, columns = 2, showHeader = true, expand = false, sx = {}, ...props }) => {
  const [isExpanded, setIsExpanded] = useState(!expand);
  const containerStyle = columns === 1 ? styles.singleColumnContainer : styles.fieldsContainer;

  return (
    <Card sx={{ ...styles.card, ...sx }} {...props}>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          gap: '14px',
          ...styles.content,
        }}
      >
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {showHeader && (title || icon || number) && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                width: '100%',
              }}
            >
              <Box sx={icon || number ? styles.headerWithIcon : styles.header}>
                {number && (
                  <Box
                    sx={{
                      width: '18px',
                      height: '18px',
                      borderRadius: '50%',
                      backgroundColor: colors.text.secondary,
                      color: colors.white,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      fontSize: '12px',
                      fontWeight: 500,
                      flexShrink: 0,
                    }}
                  >
                    {number}
                  </Box>
                )}
                {icon && !number && <SafeIcon src={icon} alt={title || 'Form section'} width={20} height={20} />}
                {title && <Typography sx={styles.title}>{title}</Typography>}
              </Box>
              {expand && (
                <IconButton
                  onClick={() => setIsExpanded(!isExpanded)}
                  size='small'
                  sx={{
                    color: colors.text.secondary,
                    '&:hover': {
                      backgroundColor: 'rgba(0, 0, 0, 0.04)',
                    },
                  }}
                >
                  {isExpanded ? <ExpandLess /> : <ExpandMore />}
                </IconButton>
              )}
            </Box>
          )}

          {description && <Typography sx={styles.description}>{description}</Typography>}
        </Box>

        {/* Show divider and content only when expanded or expand is false (always visible) */}
        {(!expand || isExpanded) && (
          <>
            {/* Horizontal dashed line separator */}
            <Divider
              //    sx={{
              //      borderStyle: 'dashed',
              //      borderWidth: '0.4px',
              //      borderColor: colors.border.secondary,
              //      opacity: 0.3,
              //      borderImage: `repeating-linear-gradient(to right, ${colors.border.secondary} 0 5.5px, transparent 4px 10px) 30`,
              //    }}
              sx={{
                borderWidth: '0.4px',
                borderColor: colors.border.secondary,
                opacity: 0.3,
              }}
            />

            <Box sx={containerStyle}>{children}</Box>
          </>
        )}
      </Box>
    </Card>
  );
};

FormCard.propTypes = {
  title: PropTypes.string,
  description: PropTypes.string,
  icon: PropTypes.any,
  number: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  children: PropTypes.node.isRequired,
  columns: PropTypes.oneOf([1, 2]),
  showHeader: PropTypes.bool,
  expand: PropTypes.bool,
  sx: PropTypes.object,
};

// ============================
// FormField Component
// ============================

/**
 * @param {{
 *   label?: string,
 *   description?: string,
 *   value?: any,
 *   onChange?: (value: any) => void,
 *   placeholder?: string,
 *   required?: boolean,
 *   error?: string,
 *   type?: string,
 *   multiline?: boolean,
 *   rows?: number,
 *   maxRows?: number,
 *   minRows?: number,
 *   maxLength?: number,
 *   disabled?: boolean,
 *   options?: any[],
 *   multiple?: boolean,
 *   grouped?: boolean,
 *   onSelect?: (value: any) => void,
 *   isOptionsLoading?: boolean,
 *   limitTags?: number,
 *   minWidth?: string,
 *   sx?: object,
 *   fieldType?: 'textfield' | 'textarea' | 'dropdown' | 'autocomplete' | 'checkbox' | 'custom',
 *   customRender?: React.ReactNode,
 *   [key: string]: any
 *   id?: string
 * }} props
 */

export const FormField = ({
  id = '',
  label,
  description,
  value,
  onChange,
  placeholder,
  required = false,
  error = '',
  type = 'text',
  multiline = false,
  rows = 4,
  maxRows,
  minRows,
  maxLength,
  disabled = false,
  options = [],
  multiple = false,
  grouped = false,
  onSelect,
  isOptionsLoading = false,
  limitTags,
  minWidth,
  sx = {},
  fieldType = 'textfield', // 'textfield', 'textarea', 'dropdown', 'autocomplete', 'checkbox', 'custom'
  customRender,
  ...props
}) => {
  const inputId = id || `field-for-${label?.replace(/\s+/g, '-').toLowerCase() || 'label'}`;
  const getFieldStyle = () => {
    const hasError = !!error;
    return {
      ...styles.inputField,
      ...(hasError ? styles.requiredField : {}),
      ...sx,
    };
  };

  const renderField = () => {
    switch (fieldType) {
      case 'textarea':
        return (
          <Textarea
            id={inputId}
            value={value}
            onChange={onChange}
            placeholder={placeholder}
            width='100%'
            minRows={minRows || rows}
            maxRows={maxRows || rows + 2}
            maxLength={maxLength}
            disabled={disabled}
            sx={getFieldStyle()}
            error={!!error}
            fontSize='14px'
            {...props}
          />
        );

      case 'dropdown':
        return (
          <CustomDropdown
            id={inputId}
            value={value}
            onChange={onChange}
            options={options}
            minWidth={minWidth || '50%'}
            isDisabled={disabled}
            margin='none'
            isRequired={required}
            error={!!error}
            helperText={error}
            placeholder={placeholder}
            {...props}
          />
        );

      case 'autocomplete':
        return (
          <FilterDropdownButton
            id={inputId}
            options={options}
            multiple={multiple}
            grouped={grouped}
            value={value}
            onSelect={onSelect || onChange}
            placeholder={placeholder}
            required={required}
            isOptionsLoading={isOptionsLoading}
            limitTag={limitTags}
            disabled={disabled}
            sx={{ minWidth: minWidth, ...sx }}
            {...props}
          />
        );

      case 'checkbox':
        return (
          <FormControlLabel
            control={
              <Checkbox
                id={inputId}
                checked={!!value}
                onChange={(e) => (onChange ? onChange(e) : onSelect ? onSelect(e, e.target.checked) : null)}
                disabled={disabled}
              />
            }
            label={label || placeholder}
            required={required}
            {...props}
          />
        );

      case 'custom':
        return customRender || null;

      default:
        return (
          <TextField
            id={inputId}
            value={value}
            required={required}
            onChange={onChange}
            variant='outlined'
            fullWidth
            placeholder={placeholder}
            sx={
              multiline
                ? {
                    ...getFieldStyle(),
                    '& .MuiInputBase-root': {
                      padding: 0,
                    },
                  }
                : getFieldStyle()
            }
            error={!!error}
            multiline={multiline}
            rows={multiline ? rows : undefined}
            maxRows={multiline ? maxRows : undefined}
            disabled={disabled}
            type={type}
            {...props}
          />
        );
    }
    return null;
  };

  return (
    <Box sx={styles.fieldContainer}>
      {label && (
        <Typography sx={styles.label}>
          {label} {required && <span style={styles.requiredStar}>*</span>}
        </Typography>
      )}

      {description && <Typography sx={styles.instructionText}>{description}</Typography>}

      {renderField()}

      {/* Only show error text for field types that don't display it internally */}
      {error && fieldType !== 'dropdown' && <Typography sx={styles.errorText}>{error}</Typography>}
    </Box>
  );
};

FormField.propTypes = {
  label: PropTypes.string,
  description: PropTypes.string,
  value: PropTypes.any,
  onChange: PropTypes.func,
  placeholder: PropTypes.string,
  required: PropTypes.bool,
  error: PropTypes.string,
  type: PropTypes.string,
  multiline: PropTypes.bool,
  rows: PropTypes.number,
  maxRows: PropTypes.number,
  minRows: PropTypes.number,
  maxLength: PropTypes.number,
  disabled: PropTypes.bool,
  options: PropTypes.array,
  multiple: PropTypes.bool,
  grouped: PropTypes.bool,
  onSelect: PropTypes.func,
  isOptionsLoading: PropTypes.bool,
  limitTags: PropTypes.number,
  minWidth: PropTypes.string,
  sx: PropTypes.object,
  fieldType: PropTypes.oneOf(['textfield', 'textarea', 'dropdown', 'autocomplete', 'checkbox', 'custom']),
  customRender: PropTypes.node,
  id: PropTypes.string,
};

// ============================
// FormBuilder Component
// ============================
export const FormBuilder = ({ sections, sx: _sx = {} }) => {
  return (
    <>
      {sections.map((section, sectionIndex) => (
        <FormCard
          key={sectionIndex}
          title={section.title}
          description={section.description}
          icon={section.icon}
          number={section.number}
          columns={section.columns || 2}
          showHeader={section.showHeader !== false}
          expand={section.expand || false}
          sx={section.cardSx || {}}
        >
          {section.fields.map((field, fieldIndex) => (
            <FormField
              key={fieldIndex}
              label={field.label}
              description={field.description}
              value={field.value}
              onChange={field.onChange}
              placeholder={field.placeholder}
              required={field.required || false}
              error={field.error || ''}
              type={field.type || 'text'}
              multiline={field.multiline || false}
              rows={field.rows || 4}
              maxRows={field.maxRows}
              minRows={field.minRows}
              maxLength={field.maxLength}
              disabled={field.disabled || false}
              options={field.options || []}
              multiple={field.multiple || false}
              grouped={field.grouped || false}
              onSelect={field.onSelect}
              isOptionsLoading={field.isOptionsLoading || false}
              limitTags={field.limitTags}
              minWidth={field.minWidth}
              sx={field.sx || {}}
              fieldType={field.fieldType || 'textfield'}
              customRender={field.customRender}
              {...(field.additionalProps || {})}
            />
          ))}
        </FormCard>
      ))}
    </>
  );
};

FormBuilder.propTypes = {
  sections: PropTypes.arrayOf(
    PropTypes.shape({
      title: PropTypes.string,
      description: PropTypes.string,
      icon: PropTypes.any,
      number: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
      columns: PropTypes.oneOf([1, 2]),
      showHeader: PropTypes.bool,
      expand: PropTypes.bool,
      cardSx: PropTypes.object,
      fields: PropTypes.arrayOf(
        PropTypes.shape({
          label: PropTypes.string,
          description: PropTypes.string,
          value: PropTypes.any,
          onChange: PropTypes.func,
          placeholder: PropTypes.string,
          required: PropTypes.bool,
          error: PropTypes.string,
          type: PropTypes.string,
          multiline: PropTypes.bool,
          rows: PropTypes.number,
          maxRows: PropTypes.number,
          minRows: PropTypes.number,
          maxLength: PropTypes.number,
          disabled: PropTypes.bool,
          options: PropTypes.array,
          multiple: PropTypes.bool,
          grouped: PropTypes.bool,
          onSelect: PropTypes.func,
          isOptionsLoading: PropTypes.bool,
          limitTags: PropTypes.number,
          minWidth: PropTypes.string,
          sx: PropTypes.object,
          fieldType: PropTypes.oneOf(['textfield', 'textarea', 'dropdown', 'autocomplete', 'checkbox', 'custom']),
          customRender: PropTypes.node,
          additionalProps: PropTypes.object,
        })
      ).isRequired,
    })
  ).isRequired,
  sx: PropTypes.object,
};
