import { useMemo } from 'react';
import { isCronValid } from 'src/utils/common';

// Validation function for trigger configurations - validates required fields only
export const validateTriggerData = (triggerType: string, params: any): { errors: Record<string, string>; isValid: boolean } => {
  const errors: Record<string, string> = {};
  let isValid = true;

  switch (triggerType) {
    case 'schedule':
      // Cron expression is required for schedule triggers
      if (!params?.cron?.trim()) {
        errors.cron = 'Cron expression is required';
        isValid = false;
      }
      break;

    case 'webhook':
      // Integration name is required for webhook triggers
      if (!params?.integration_name?.trim()) {
        errors.integration_name = 'Integration name is required';
        isValid = false;
      }
      break;

    case 'event':
      break;

    case 'manual':
      // Manual trigger has no required fields
      break;

    default:
      // Unknown trigger types are considered valid
      break;
  }

  return { errors, isValid };
};

// Helper function to get nested value from object using dot notation
const getNestedValue = (obj: any, path: string): any => {
  return path.split('.').reduce((current, key) => current?.[key], obj);
};

export const useTaskValidation = (actionType: string, data: any) => {
  const validationResult = useMemo(() => {
    const rules: any[] = [];
    const errors: Record<string, string> = {};
    let isValid = true;

    for (const rule of rules) {
      const value = getNestedValue(data, rule.field);

      if (rule.required && (!value || value === '')) {
        errors[rule.field] = `${rule.field.replace(/([A-Z])/g, ' $1').toLowerCase()} is required`;
        isValid = false;
        continue;
      }

      if (rule.validator) {
        const error = rule.validator(value, data);
        if (error) {
          errors[rule.field] = error;
          isValid = false;
        }
      }
    }

    return { errors, isValid };
  }, [actionType, data]);

  return validationResult;
};

export const validateTaskData = (actionType: string, data: any, validationRules: any) => {
  // Handle case where validationRules is undefined or not an array
  if (!validationRules || !Array.isArray(validationRules)) {
    return { errors: {}, isValid: true };
  }

  const taskDefinition = validationRules.find((item: any) => item.name === actionType);
  if (!taskDefinition?.input_schema) {
    return { errors: {}, isValid: true };
  }

  const inputSchema = taskDefinition.input_schema;

  const errors: Record<string, string> = {};
  let isValid = true;

  // Validate each field in the input schema
  for (const [fieldName, fieldSchema] of Object.entries(inputSchema)) {
    const fieldConfig = fieldSchema as any;
    const value = getNestedValue(data, fieldName) ?? fieldConfig.default;

    // Check conditional required (required_when)
    let isFieldRequired = !!fieldConfig.required;
    if (!isFieldRequired && fieldConfig.required_when) {
      const { field, value: allowedValues } = fieldConfig.required_when;
      const depValue = getNestedValue(data, field) ?? inputSchema[field]?.default;
      if (depValue !== undefined && depValue !== null && Array.isArray(allowedValues) && allowedValues.includes(depValue)) {
        isFieldRequired = true;
      }
    }

    // Check required fields
    if (isFieldRequired && (value === undefined || value === null || value === '')) {
      errors[fieldName] = `${fieldName
        .replace(/_/g, ' ')
        .replace(/([A-Z])/g, ' $1')
        .toLowerCase()
        .trim()} is required`;
      isValid = false;
      continue;
    }

    // Skip further validation if field is empty and not required
    if (value === undefined || value === null || value === '') {
      continue;
    }

    // Type-specific validation
    if (fieldConfig.type) {
      switch (fieldConfig.type) {
        case 'number':
        case 'integer':
          if (isNaN(Number(value))) {
            errors[fieldName] = `${fieldName
              .replace(/_/g, ' ')
              .replace(/([A-Z])/g, ' $1')
              .toLowerCase()
              .trim()} must be a valid number`;
            isValid = false;
          } else if (fieldConfig.type === 'integer' && !Number.isInteger(Number(value))) {
            errors[fieldName] = `${fieldName
              .replace(/_/g, ' ')
              .replace(/([A-Z])/g, ' $1')
              .toLowerCase()
              .trim()} must be an integer`;
            isValid = false;
          }
          break;

        case 'boolean':
          if (typeof value !== 'boolean' && value !== 'true' && value !== 'false') {
            errors[fieldName] = `${fieldName
              .replace(/_/g, ' ')
              .replace(/([A-Z])/g, ' $1')
              .toLowerCase()
              .trim()} must be a boolean value`;
            isValid = false;
          }
          break;

        case 'object':
          if (typeof value === 'string') {
            // Template references (Jinja/Go) resolve to a map at runtime via ProcessValue,
            // so a bare template string is a valid object-field value despite not being JSON.
            const isTemplate = /\{\{|\{%/.test(value);
            if (!isTemplate) {
              try {
                JSON.parse(value);
              } catch {
                errors[fieldName] = `${fieldName
                  .replace(/_/g, ' ')
                  .replace(/([A-Z])/g, ' $1')
                  .toLowerCase()
                  .trim()} must be valid JSON`;
                isValid = false;
              }
            }
          } else if (typeof value !== 'object') {
            errors[fieldName] = `${fieldName
              .replace(/_/g, ' ')
              .replace(/([A-Z])/g, ' $1')
              .toLowerCase()
              .trim()} must be an object`;
            isValid = false;
          }
          break;

        case 'string':
          if (typeof value !== 'string') {
            errors[fieldName] = `${fieldName
              .replace(/_/g, ' ')
              .replace(/([A-Z])/g, ' $1')
              .toLowerCase()
              .trim()} must be a string`;
            isValid = false;
          }
          break;
      }
    }

    // Enum validation
    if (fieldConfig.enum && Array.isArray(fieldConfig.enum)) {
      if (!fieldConfig.enum.includes(value)) {
        errors[fieldName] = `${fieldName
          .replace(/_/g, ' ')
          .replace(/([A-Z])/g, ' $1')
          .toLowerCase()
          .trim()} must be one of: ${fieldConfig.enum.join(', ')}`;
        isValid = false;
      }
    }

    // Special validation for cron expressions
    if (fieldName.toLowerCase().includes('cron') || fieldName.toLowerCase().includes('schedule')) {
      if (typeof value === 'string' && value.trim() && !isCronValid(value)) {
        errors[fieldName] = `${fieldName
          .replace(/_/g, ' ')
          .replace(/([A-Z])/g, ' $1')
          .toLowerCase()
          .trim()} must be a valid cron expression`;
        isValid = false;
      }
    }

    // Email validation — covers single string fields (e.g. reply_to) and array
    // fields like `recipients` where users may type custom addresses via freeSolo.
    const fieldNameLower = fieldName.toLowerCase();
    const isEmailField = fieldNameLower.includes('email') || fieldNameLower === 'recipients' || fieldNameLower === 'recipient';
    if (isEmailField) {
      const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
      const valuesToCheck: string[] = Array.isArray(value)
        ? value.filter((v): v is string => typeof v === 'string' && v.trim().length > 0)
        : typeof value === 'string' && value.trim()
        ? [value]
        : [];

      const invalid = valuesToCheck.find((v) => !emailRegex.test(v));
      if (invalid) {
        errors[fieldName] = `"${invalid}" is not a valid email address`;
        isValid = false;
      }
    }

    // URL validation
    if ((fieldName.toLowerCase().includes('url') || fieldName.toLowerCase().includes('endpoint')) && typeof value === 'string' && value.trim()) {
      try {
        new URL(value);
      } catch {
        errors[fieldName] = `${fieldName
          .replace(/_/g, ' ')
          .replace(/([A-Z])/g, ' $1')
          .toLowerCase()
          .trim()} must be a valid URL`;
        isValid = false;
      }
    }
  }

  return { errors, isValid };
};
