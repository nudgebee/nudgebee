import React, { useState } from 'react';
import { Box, Typography, Card, Divider, IconButton } from '@mui/material';
import { Checkbox } from '@ui/Checkbox';
import { Input } from '@ui/Input';
import { ExpandMore, ExpandLess } from '@mui/icons-material';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
import { Textarea } from '@components/k8s/common/TextArea';
import { Select } from '@ui/Select';

// Shared styles from the original CreateAgent component
const styles = {
  label: {
    fontSize: 'var(--ds-text-body)',
    fontWeight: 'var(--ds-font-weight-medium)',
    color: ds.brand[500],
  },
  inputField: {
    fontSize: 'var(--ds-text-small)',
    '& .MuiOutlinedInput-root': {
      borderRadius: 'var(--ds-radius-md)',
      backgroundColor: 'white',
      fontSize: 'var(--ds-text-body-lg)',

      '&.Mui-error fieldset': {
        borderColor: ds.red[500],
        borderWidth: '1px',
      },
      '& fieldset': {
        borderColor: ds.gray[200],
      },
      '&:hover fieldset': {
        borderColor: ds.blue[400],
      },
      '&.Mui-focused fieldset': {
        borderColor: ds.blue[500],
        borderWidth: ds.space[0],
      },
    },
    '& .MuiInputBase-input': {
      padding: 'var(--ds-space-2) var(--ds-space-3)',
      '&::placeholder': {
        color: ds.brand[200],
        fontWeight: 'var(--ds-font-weight-regular)',
        fontSize: 'var(--ds-text-small)',
        opacity: 1,
      },
    },
  },
  requiredField: {
    '& .MuiOutlinedInput-root': {
      '& fieldset': {
        borderColor: ds.red[500],
      },
    },
  },
  errorText: {
    color: ds.red[500],
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
    mt: 1,
  },
  instructionText: {
    fontSize: 'var(--ds-text-caption)',
    color: ds.gray[400],
    fontWeight: 'var(--ds-font-weight-regular)',
    mb: 'var(--ds-space-2)',
  },
  requiredStar: {
    color: ds.red[500],
  },
  // Card styles
  card: {
    backgroundColor: ds.background[100],
    borderRadius: 'var(--ds-radius-lg)',
    border: `1px solid ${ds.gray[200]}`,
    boxShadow: `0px ${ds.space.mul(0, 5)} 15px -6px ${ds.gray.alpha[300]}, 0px ${ds.space.mul(0, 3)} 14px -5px color-mix(in srgb, ${
      ds.brand[500]
    } 10%, transparent)`,
    overflow: 'visible',
    mb: 3,
    gap: 'var(--ds-space-5)',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: 'var(--ds-space-2)',
  },
  headerWithIcon: {
    display: 'flex',
    alignItems: 'center',
    gap: 'var(--ds-space-2)',
  },
  title: {
    fontSize: 'var(--ds-text-title)',
    fontWeight: 'var(--ds-font-weight-medium)',
    color: ds.brand[500],
  },
  description: {
    fontSize: 'var(--ds-text-small)',
    color: ds.gray[400],
    fontWeight: 'var(--ds-font-weight-regular)',
  },
  content: {
    padding: 'var(--ds-space-4) var(--ds-space-5)',
  },
  fieldsContainer: {
    display: 'grid',
    gridTemplateColumns: '1fr 1fr',
    gap: 'var(--ds-space-6)',
    padding: '0 var(--ds-space-4)',
  },
  singleColumnContainer: {
    display: 'flex',
    flexDirection: 'column',
    gap: 'var(--ds-space-5)',
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
          gap: 'var(--ds-space-3)',
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
                      width: ds.space.mul(0, 9),
                      height: ds.space.mul(0, 9),
                      borderRadius: '50%',
                      backgroundColor: ds.brand[500],
                      color: ds.background[100],
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-medium)',
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
                    color: ds.brand[500],
                    '&:hover': {
                      backgroundColor: ds.gray.alpha[100],
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
              //      borderColor: ds.brand[200],
              //      opacity: 0.3,
              //      borderImage: `repeating-linear-gradient(to right, ${ds.brand[200]} 0 5.5px, transparent 4px 10px) 30`,
              //    }}
              sx={{
                borderWidth: '0.4px',
                borderColor: ds.brand[200],
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
 *   onSelect?: (value: any) => void,
 *   isOptionsLoading?: boolean,
 *   minWidth?: string,
 *   sx?: object,
 *   fieldType?: 'textfield' | 'textarea' | 'select' | 'checkbox' | 'custom',
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
  onSelect,
  isOptionsLoading = false,
  minWidth,
  sx = {},
  fieldType = 'textfield', // 'textfield', 'textarea', 'select', 'checkbox', 'custom'
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
            fontSize={ds.text.bodyLg}
            {...props}
          />
        );

      case 'select':
        return (
          <Select
            id={inputId}
            value={multiple ? value ?? [] : value ?? ''}
            onChange={(next) => onChange?.({ target: { value: next } })}
            options={options}
            minWidth={minWidth || '50%'}
            disabled={disabled}
            required={required}
            error={error || undefined}
            placeholder={placeholder}
            loading={isOptionsLoading}
            {...(multiple ? { multiple: true } : {})}
          />
        );

      case 'checkbox':
        return (
          <Checkbox
            id={inputId}
            checked={!!value}
            onChange={(next) => {
              const syntheticEvent = {
                target: { id: inputId, checked: next },
                stopPropagation: () => {},
                preventDefault: () => {},
              };
              if (onChange) {
                onChange(syntheticEvent);
              } else if (onSelect) {
                onSelect(syntheticEvent, next);
              }
            }}
            disabled={disabled}
            label={`${label || placeholder || ''}${required ? ' *' : ''}`}
          />
        );

      case 'custom':
        return customRender || null;

      default:
        return (
          <Input
            id={inputId}
            value={value ?? ''}
            required={required}
            // Preserve callers' event-shape onChange contract: synthesize a minimal { target: { value } }
            // so the existing `(e) => setX(e.target.value)` call sites keep working unchanged.
            onChange={(next) => onChange?.({ target: { value: next } })}
            placeholder={placeholder}
            error={error || undefined}
            type={multiline ? 'textarea' : type}
            rows={multiline ? rows : undefined}
            minRows={multiline ? minRows : undefined}
            maxRows={multiline ? maxRows : undefined}
            disabled={disabled}
            size='sm'
            // Forward extra caller props (onBlur, onFocus, onKeyDown, autoComplete, name, etc.)
            // DS Input ignores anything outside its prop surface, so legacy MUI-only props
            // (variant, fullWidth, multiline) are a no-op rather than an error.
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

      {/* Fallback error text only for controls that don't render it inline: textarea
          gets a boolean error (no message), checkbox/custom have no error slot. DS Input
          (default/textfield) and Select already render the error themselves. */}
      {error && (fieldType === 'textarea' || fieldType === 'checkbox' || fieldType === 'custom') && (
        <Typography sx={styles.errorText}>{error}</Typography>
      )}
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
  onSelect: PropTypes.func,
  isOptionsLoading: PropTypes.bool,
  minWidth: PropTypes.string,
  sx: PropTypes.object,
  fieldType: PropTypes.oneOf(['textfield', 'textarea', 'select', 'checkbox', 'custom']),
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
              onSelect={field.onSelect}
              isOptionsLoading={field.isOptionsLoading || false}
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
          fieldType: PropTypes.oneOf(['textfield', 'textarea', 'select', 'checkbox', 'custom']),
          customRender: PropTypes.node,
          additionalProps: PropTypes.object,
        })
      ).isRequired,
    })
  ).isRequired,
  sx: PropTypes.object,
};
