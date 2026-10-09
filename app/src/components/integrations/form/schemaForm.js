// Schema-driven form machinery shared by integration forms. Moved verbatim out
// of IntegrationDynamicFormModal (the only transformation: the modal's closure
// variables — its `config`, `formValues`, `editData` — became explicit
// parameters), so the conditional-field rules and the probe payload stay a
// single implementation for every consumer. The modal keeps thin local
// wrappers that bind its state back in; new consumers (the LLM provider form)
// call these directly.
import { ENCRYPTED_MASK } from '@api1/integrations/helpers';

export const joinConfigArray = (values) => values.map((v) => (v && typeof v === 'object' ? v.value : v)).join(',');

// Helper function to check if condition values match current value
export const checkConditionMatch = (conditionValues, currentValue) => {
  if (Array.isArray(conditionValues)) {
    return conditionValues.includes(currentValue);
  }
  return conditionValues === currentValue;
};

// Helper function to check if a field should be visible
export const shouldShowField = (config, values, _key, field, _visiting) => {
  if (field.hidden) {
    return false;
  }
  // Always show fields without show_when or required_when conditions
  if (!field.show_when && !field.required_when) {
    return true;
  }

  // Helper to check that a dependency field is itself visible (with recursion guard)
  const isDependencyVisible = (conditionKey) => {
    const depField = config?.properties?.[conditionKey];
    if (!depField) return true; // unknown field, assume visible
    // Prevent infinite recursion: if we're already checking this key up the call stack
    if (_visiting && _visiting.has(conditionKey)) return true;
    const visiting = new Set(_visiting || []);
    visiting.add(_key);
    return shouldShowField(config, values, conditionKey, depField, visiting);
  };

  let shouldShow = false;
  // Check show_when conditions (all must be satisfied)
  if (field.show_when) {
    shouldShow = true;
    for (const [conditionKey, conditionValues] of Object.entries(field.show_when)) {
      if (!isDependencyVisible(conditionKey)) {
        shouldShow = false;
        break;
      }
      const currentValue = values[conditionKey];
      if (!checkConditionMatch(conditionValues, currentValue)) {
        shouldShow = false;
        break;
      }
    }
  }

  // Check required_when conditions (all must be satisfied)
  if (field.required_when) {
    let allRequiredConditionsMet = true;
    for (const [conditionKey, conditionValues] of Object.entries(field.required_when)) {
      if (!isDependencyVisible(conditionKey)) {
        allRequiredConditionsMet = false;
        break;
      }
      const currentValue = values[conditionKey];
      if (!checkConditionMatch(conditionValues, currentValue)) {
        allRequiredConditionsMet = false;
        break;
      }
    }
    if (allRequiredConditionsMet) {
      shouldShow = true;
    }
  }

  return shouldShow;
};

// Helper function to check if a field is required based on current form state
export const isFieldRequired = (config, formValues, key, field) => {
  // account_id and integration_config_name are always required
  if (key === 'account_id' || key === 'integration_config_name') return true;

  // Check if it's in the base required array
  const isBaseRequired = config.required?.includes(key);

  // Check required_when conditions (all must be satisfied)
  if (field.required_when) {
    let allRequiredConditionsMet = true;
    for (const [conditionKey, conditionValues] of Object.entries(field.required_when)) {
      // If the dependency field is hidden, the required condition is not met
      const depField = config?.properties?.[conditionKey];
      if (depField && !shouldShowField(config, formValues, conditionKey, depField)) {
        allRequiredConditionsMet = false;
        break;
      }
      const currentValue = formValues[conditionKey];
      if (!checkConditionMatch(conditionValues, currentValue)) {
        allRequiredConditionsMet = false;
        break;
      }
    }
    if (allRequiredConditionsMet) {
      return true;
    }
  }

  return isBaseRequired;
};

// Helper function to sort fields by priority (highest first)
export const getSortedFieldKeys = (config, formValues) => {
  const visibleFields = Object.keys(config?.properties || {}).filter((key) => shouldShowField(config, formValues, key, config.properties[key]));

  return visibleFields.sort((a, b) => {
    const fieldA = config.properties[a];
    const fieldB = config.properties[b];

    // Get priority values, default to 0 if not specified
    const priorityA = fieldA.priority || 0;
    const priorityB = fieldB.priority || 0;

    // Sort in descending order (highest priority first)
    return priorityB - priorityA;
  });
};

// Build the config-values payload for connectivity / index probes: booleans and
// integers stringified; untyped encrypted secrets sent as the stored ciphertext
// with is_encrypted=true (omit-to-keep). Shared by Test Connection and the ES
// index fetch.
export const buildProbeConfigValues = (config, formValues, editConfigValues) => {
  const { account_id: _a, integration_config_name: _n, account_mapping: _m, ...restFormValues } = formValues;
  return Object.entries(restFormValues).map(([key, value]) => {
    const field = config.properties?.[key];
    const fieldType = field?.type;
    let transformedValue = value;
    if (fieldType === 'boolean' || fieldType === 'bool' || fieldType === 'integer') {
      transformedValue = String(value);
    } else if (field?.is_encrypted && editConfigValues?.[key] && value === ENCRYPTED_MASK) {
      transformedValue = editConfigValues?.[key];
    } else if (Array.isArray(value)) {
      transformedValue = joinConfigArray(value);
    }
    return {
      name: key,
      // Both coerced, never left undefined. `&&` yields undefined when the schema
      // carries no is_encrypted, and callers that inline this into a GraphQL string
      // (gqlStringify) then emit the bare token `undefined`, which the Go decoder
      // rejects with "expected type 'bool', got unconvertible type 'string'". The
      // variables-based callers never saw it, because JSON.stringify drops undefined.
      value: transformedValue ?? '',
      is_encrypted: !!(field?.is_encrypted && editConfigValues?.[key] && value === ENCRYPTED_MASK),
    };
  });
};
