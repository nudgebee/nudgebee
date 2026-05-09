import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { TextField, FormControlLabel, Checkbox, Box, Typography, Grid, Switch, IconButton } from '@mui/material';
import FilterDropdownButton from './FilterDropdownButton';
import CustomTextField from './CustomTextField';
import apiUser from '@api1/user';
import { Modal } from './modal';
import CustomButton from './NewCustomButton';
import apiIntegrations from '@api1/integrations';
import NDialog from './modal/NDialog';
import { colors } from 'src/utils/colors';
import CopyableText from './CopyableText';
import { titleCase } from '@lib/formatter';
import { getAccountCreationSuccessMsg, parseHttpResponseBodyMessage, safeJSONParse, snakeToTitleCase, toKebabCase } from 'src/utils/common';
import { snackbar } from './snackbarService';
import { PlusIcon, DeleteIconRed as NewDelete } from '@assets';
import SafeIcon from './SafeIcon';
import apiTicketIntegrations from '@api1/tickets';
import cache from '@lib/cache';
import VmAgentCredentialsDialog from './VmAgentCredentialsDialog';

const IntegrationDynamicFormModal = ({
  integrationName,
  openModal,
  handleClose,
  title,
  integrationData = [],
  editData = null,
  listIntegrationConfigurationById,
}) => {
  const [errors, setErrors] = useState({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [config, setConfig] = useState({});
  const [formValues, setFormValues] = useState({});
  const [response, setResponse] = useState({});
  const [showModal, setShowModal] = useState(false);
  const [loadingOptions, setLoadingOptions] = useState({});
  const [isLoadingSchema, setIsLoadingSchema] = useState(false);
  const [simpleLabelValue, setSimpleLabelValue] = useState('');
  const [accountMappings, setAccountMappings] = useState([{ label: '', accountId: '' }]);
  const [agentAccountProviders, setAgentAccountProviders] = useState([]);
  const [providerFields, setProviderFields] = useState([]);
  const [vmAgentCredentials, setVmAgentCredentials] = useState(null);
  const [isTesting, setIsTesting] = useState(false);
  const [connectionVerified, setConnectionVerified] = useState(!!editData);

  const isTestable = (() => {
    if (!config?.testable) return false;
    if (!config?.testable_when || Object.keys(config.testable_when).length === 0) return true;
    return Object.entries(config.testable_when).every(([key, value]) => formValues[key] === value);
  })();

  const isAgentSource = editData?.source === 'agent';

  useEffect(() => {
    if (openModal && isAgentSource && editData?.integrations_cloud_accounts) {
      setAgentAccountProviders(
        editData.integrations_cloud_accounts.map((acc) => ({
          cloud_account_id: acc.cloud_account_id,
          account_name: acc.cloud_account_name,
          default_log_provider: acc.default_log_provider || false,
          default_traces_provider: acc.default_traces_provider || false,
          default_metrics_provider: acc.default_metrics_provider || false,
        }))
      );

      // Derive provider fields from cloud accounts data for agent integrations
      const providerKeyLabels = {
        default_log_provider: 'Logs',
        default_traces_provider: 'Traces',
        default_metrics_provider: 'Metrics',
      };
      const firstAcc = editData.integrations_cloud_accounts[0];
      if (firstAcc) {
        const fields = Object.keys(providerKeyLabels)
          .filter((key) => key in firstAcc)
          .map((key) => ({ key, label: providerKeyLabels[key] }));
        setProviderFields(fields);
      }
    }
  }, [openModal, isAgentSource, editData]);

  useEffect(() => {
    if (openModal) {
      setConnectionVerified(!!editData);
      setIsLoadingSchema(true);
      const fetchData = async (configs) => {
        const updatedConfig = { ...configs };
        for (const key in updatedConfig.properties) {
          const field = updatedConfig.properties[key];
          if (field.auto_generate_func && field.auto_generate_func === 'listAccounts') {
            try {
              setLoadingOptions((prev) => ({ ...prev, [key]: true }));
              const res = await apiUser.listAccounts();
              if (res.length > 0) {
                const cloudAccounts = res.map((account) => ({ label: account.account_name, value: account.id }));
                updatedConfig.properties[key].possible_values = cloudAccounts;
                updatedConfig.properties[key].default = [];
              }
            } finally {
              setLoadingOptions((prev) => ({ ...prev, [key]: false }));
            }
          } else if (field.enum && field.enum.length > 0) {
            updatedConfig.properties[key].possible_values = field.enum.map((value) => ({
              label: { vm_agent: 'Proxy Agent' }[value] || snakeToTitleCase(value),
              value: value,
            }));
          }
        }
        setConfig(updatedConfig);
        setIsLoadingSchema(false);
      };
      apiIntegrations
        .listIntegrationSchema({
          integration_name: integrationName,
          source: editData?.source ?? 'user',
        })
        .then((res) => {
          const configs = res?.data?.data?.integrations_get_schema?.data || {};
          if (Object.keys(configs).length > 0) {
            // Extract provider fields from schema (boolean fields ending with _provider)
            const extractedProviderFields = Object.entries(configs.properties || {})
              .filter(([key, prop]) => (prop.type === 'bool' || prop.type === 'boolean') && key.endsWith('_provider'))
              .map(([key, prop]) => ({ key, label: prop.display_name || snakeToTitleCase(key) }));
            if (extractedProviderFields.length > 0) {
              setProviderFields(extractedProviderFields);
            }
            const filteredProperties = Object.fromEntries(
              Object.entries(configs.properties || {}).filter(([_, prop]) => {
                // If it's true, filter it out.
                // If it's undefined, null, or false, keep it.
                return prop.avoid_to_show !== true;
              })
            );
            const cleanConfigs = {
              ...configs,
              properties: filteredProperties,
            };
            setConfig(cleanConfigs);
            setFormValues(() => {
              const initialValues = {};
              Object.keys(configs?.properties || {}).forEach((key) => {
                const prop = configs.properties[key];
                let val = editData?.integration_config_values?.[key] ?? prop.default ?? '';
                if (prop.type === 'boolean' || prop.type === 'bool') {
                  if (typeof val === 'string') {
                    val = val.toLowerCase() === 'true';
                  } else {
                    val = Boolean(val);
                  }
                }
                if (key == 'default_log_provider' || key == 'default_traces_provider' || key == 'default_metrics_provider') {
                  if (editData?.integrations_cloud_accounts?.length) {
                    val = editData?.integrations_cloud_accounts?.[0]?.[key] || false;
                  }
                }
                if (prop.is_encrypted && editData?.integration_config_values?.[key]) {
                  initialValues[key] = '*************************************************';
                } else {
                  initialValues[key] = val;
                }
              });
              return initialValues;
            });

            fetchData(cleanConfigs);
          } else {
            setIsLoadingSchema(false);
          }
        })
        .catch(() => {
          setIsLoadingSchema(false);
        });
    }
  }, [openModal]);

  // Handle account_mapping initialization for edit mode
  useEffect(() => {
    if (editData?.integration_config_values?.account_mapping) {
      const accountMapping = safeJSONParse(editData.integration_config_values.account_mapping) || {};

      if (accountMapping.labelName) {
        setSimpleLabelValue(accountMapping.labelName);
      }

      const mappings = Object.entries(accountMapping)
        .filter(([key]) => key !== 'labelName')
        .map(([label, accountId]) => ({
          label,
          // Tolerate legacy persisted shape where accountId was a {label, value}
          // object instead of a plain string. Normalize on read so the dropdown
          // and the resubmit path both see a string.
          accountId: typeof accountId === 'object' && accountId !== null ? accountId.value || '' : accountId,
        }));

      if (mappings.length > 0) {
        setAccountMappings(mappings);
      }
    }
  }, [editData]);

  // Helper function to check if condition values match current value
  const checkConditionMatch = (conditionValues, currentValue) => {
    if (Array.isArray(conditionValues)) {
      return conditionValues.includes(currentValue);
    }
    return conditionValues === currentValue;
  };

  // Helper function to check if a field should be visible
  const shouldShowField = (_key, field, valuesOverride, _visiting) => {
    const values = valuesOverride || formValues;
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
      return shouldShowField(conditionKey, depField, values, visiting);
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
  const isFieldRequired = (key, field) => {
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
        if (depField && !shouldShowField(conditionKey, depField)) {
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
  const getSortedFieldKeys = () => {
    const visibleFields = Object.keys(config?.properties || {}).filter((key) => shouldShowField(key, config.properties[key]));

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

  const handleChange = (key, value) => {
    if (isTestable && config.properties?.[key]?.is_testable) {
      setConnectionVerified(false);
    }
    setFormValues((prevValues) => {
      let updatedValues = { ...prevValues, [key]: value };
      if (key == 'account_id') {
        updatedValues = {
          ...updatedValues,
          account_id: value.map((a) => a.value),
        };
      }
      if (integrationName == 'LLM' && key == 'account_id' && value) {
        if (integrationData) {
          const selectedAccount = integrationData.find((it) => it.account_id === value);
          if (selectedAccount) {
            updatedValues = { ...updatedValues, integration_config_name: selectedAccount.name };
            setConfig((prevConfig) => ({
              ...prevConfig,
              properties: {
                ...prevConfig.properties,
                integration_config_name: {
                  ...prevConfig.properties.integration_config_name,
                  disabled: true,
                },
              },
            }));
          }
        }
      }

      // Clear values for fields that are no longer visible
      const newConfig = { ...config };
      Object.keys(newConfig.properties || {}).forEach((fieldKey) => {
        const field = newConfig.properties[fieldKey];
        // If this field should not be shown anymore, clear its value
        if (!shouldShowField(fieldKey, field, updatedValues) && fieldKey !== key) {
          delete updatedValues[fieldKey];
        }
      });

      return updatedValues;
    });

    // Clear any existing errors for this field
    if (errors[key]) {
      setErrors((prev) => {
        const newErrors = { ...prev };
        delete newErrors[key];
        return newErrors;
      });
    }
  };

  const handleAgentProviderToggle = async (cloudAccountId, providerKey, newValue) => {
    setIsSubmitting(true);
    const allAccountIds = agentAccountProviders.map((acc) => acc.cloud_account_id);
    const providerMap = {};
    agentAccountProviders.forEach((acc) => {
      providerMap[acc.cloud_account_id] = acc.cloud_account_id === cloudAccountId ? String(newValue) : String(acc[providerKey]);
    });

    const payload = {
      ...(editData?.id && { integration_id: editData.id }),
      integration_name: integrationName,
      account_ids: allAccountIds,
      integration_config_name: editData?.name,
      skip_validation: true,
      source: editData?.source || 'user',
      integration_config_values: [{ name: providerKey, value: JSON.stringify(providerMap), is_encrypted: false }],
    };

    try {
      const res = await apiIntegrations.addIntegrations(payload);
      if (res?.data?.data?.integrations_create_config) {
        setAgentAccountProviders((prev) => prev.map((acc) => (acc.cloud_account_id === cloudAccountId ? { ...acc, [providerKey]: newValue } : acc)));
        if (editData?.id) {
          listIntegrationConfigurationById(editData?.id);
        }

        // Invalidate cached provider for the affected account
        const providerCacheSuffix = { default_log_provider: '-log', default_traces_provider: '-traces', default_metrics_provider: '-metrics' };
        const suffix = providerCacheSuffix[providerKey];
        if (suffix) {
          cache.del(`${cloudAccountId}${suffix}`);
        }

        snackbar.success(`Account mapping ${newValue ? 'enabled' : 'disabled'} successfully`);
      } else {
        snackbar.error(`${parseHttpResponseBodyMessage(res?.data)}`);
      }
    } catch {
      snackbar.error('Failed to update account mapping');
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleAddMapping = () => {
    setAccountMappings([...accountMappings, { label: '', accountId: '' }]);
  };

  const handleRemoveMapping = (index) => {
    const newMappings = accountMappings.filter((_, i) => i !== index);
    setAccountMappings(newMappings.length > 0 ? newMappings : [{ label: '', accountId: '' }]);
  };

  const handleMappingChange = (index, field, value) => {
    const newMappings = [...accountMappings];
    newMappings[index][field] = value;
    setAccountMappings(newMappings);
  };

  const handleCloseModal = (trigger) => {
    setIsSubmitting(false);
    setConfig({});
    setFormValues({});
    setErrors({});
    setShowModal(false);
    setSimpleLabelValue('');
    setAccountMappings([{ label: '', accountId: '' }]);
    setAgentAccountProviders([]);
    setProviderFields([]);
    setVmAgentCredentials(null);
    setIsTesting(false);
    setConnectionVerified(!!editData);
    handleClose(trigger);
  };

  const validateForm = () => {
    const visibleFields = Object.keys(config.properties || {}).filter((key) => shouldShowField(key, config.properties[key]));
    const filledKeys = visibleFields.filter(
      (key) =>
        formValues[key] !== '' &&
        formValues[key] !== null &&
        formValues[key] !== undefined &&
        !(Array.isArray(formValues[key]) && formValues[key].length === 0)
    );
    const requiredVisibleFields = visibleFields.filter((key) => isFieldRequired(key, config.properties[key]));
    const missingElements = requiredVisibleFields.filter((item) => !filledKeys.includes(item));
    if (missingElements.length > 0) {
      setErrors(Object.fromEntries(missingElements.map((key) => [key, `${key} param is required`])));
      return false;
    }
    return true;
  };

  // Skip validation when editing if no testable fields changed
  const shouldSkipValidation = () => {
    if (!editData) return false; // New integration - always validate

    // Check if any testable field changed
    const testableFieldChanged = Object.keys(formValues).some((key) => {
      const field = config.properties?.[key];
      if (!field?.is_testable) return false;

      const currentValue = formValues[key];

      // For encrypted fields, if value is masked, it hasn't changed
      if (field?.is_encrypted && currentValue === '*************************************************') {
        return false;
      }

      const originalValue = editData?.integration_config_values?.[key];
      return currentValue !== originalValue;
    });

    // Skip validation if no testable fields changed
    return !testableFieldChanged;
  };

  const handleTestConnection = async () => {
    if (!validateForm()) return;
    setIsTesting(true);
    try {
      const { account_id, integration_config_name: _, account_mapping: _m, ...restFormValues } = formValues;
      const configValues = Object.entries(restFormValues).map(([key, value]) => {
        const field = config.properties?.[key];
        const fieldType = field?.type;
        let transformedValue = value;
        if (fieldType === 'boolean' || fieldType === 'bool' || fieldType === 'integer') {
          transformedValue = String(value);
        } else if (
          field?.is_encrypted &&
          editData?.integration_config_values?.[key] &&
          value === '*************************************************'
        ) {
          transformedValue = editData?.integration_config_values?.[key];
        }
        return {
          name: key,
          value: transformedValue,
          is_encrypted:
            field?.is_encrypted && !!editData?.integration_config_values?.[key] && value === '*************************************************',
        };
      });

      const accountIds = Array.isArray(account_id) ? account_id : account_id ? [account_id] : [];
      const result = await apiIntegrations.testIntegrationConnectionByConfig(
        integrationName === 'elasticsearch' ? 'ES' : integrationName,
        accountIds,
        configValues,
        editData?.source || 'user'
      );
      if (result?.success) {
        setConnectionVerified(true);
        snackbar.success(`${snakeToTitleCase(integrationName)} connection successful`);
      } else {
        snackbar.error(result?.error || `${snakeToTitleCase(integrationName)} connection test failed`);
      }
    } catch {
      snackbar.error(`Failed to test ${snakeToTitleCase(integrationName)} connection`);
    } finally {
      setIsTesting(false);
    }
  };

  const submitForm = async () => {
    if (!validateForm()) {
      return;
    }
    setIsSubmitting(true);

    if (['pagerduty', 'servicenow', 'github', 'gitlab', 'zenduty'].includes(integrationName)) {
      const { integration_config_name, ...restFormValues } = formValues;
      const transformedValues = Object.entries(restFormValues).reduce((acc, [key, value]) => {
        const field = config.properties?.[key];
        const fieldType = field?.type;
        let transformedValue = value;
        if (fieldType === 'boolean' || fieldType === 'bool') {
          transformedValue = String(value);
        } else if (
          field?.is_encrypted &&
          editData?.integration_config_values?.[key] &&
          value === '*************************************************'
        ) {
          // User didn't re-enter the secret. Send empty so the backend preserves the stored value.
          transformedValue = '';
        }
        acc[key] = transformedValue;
        return acc;
      }, {});
      const configValuesArray = Object.entries(transformedValues)
        .filter(([key]) => {
          const field = config.properties?.[key];
          return field && (field.type === 'boolean' || field.type === 'bool');
        })
        .map(([key, value]) => ({
          name: key,
          value: String(value),
        }));

      const bodyData = {
        name: integration_config_name,
        password: transformedValues.password,
        url: transformedValues.url,
        username: transformedValues.username,
        tool: integrationName,
        ...(configValuesArray.length > 0 && { config_values: configValuesArray }),
      };

      try {
        const configRes = await apiTicketIntegrations.listTicketConfigurations({
          tool: integrationName,
        });
        const toolConfList = configRes?.data || [];
        const isEditMode = editData && Object.keys(editData).length > 0;
        const duplicateExists = toolConfList.some((config) => config.name === bodyData.name && (!isEditMode || config.id !== editData.id));
        if (duplicateExists) {
          setErrors({
            integration_config_name: `${bodyData.name} already exists. Please choose a different name.`,
          });
          setIsSubmitting(false);
          return;
        }

        const res = await apiIntegrations.createTicketIntegration(bodyData);

        // Check for GraphQL errors first (errors are at res.data.errors, not res.data.data.errors)
        if (res?.data?.errors?.length > 0) {
          snackbar.error(res.data.errors[0]?.message || `Failed to Add ${integrationName} Account`);
          handleCloseModal(false);
          return;
        }

        // Check for success
        if (res?.data?.data?.ticket_integration_create_config) {
          snackbar.success(getAccountCreationSuccessMsg(integrationName.toUpperCase()));
          handleCloseModal(true);
        } else {
          snackbar.error(`Failed to Add ${integrationName} Account`);
          handleCloseModal(false);
        }
      } catch (error) {
        const errorMessage = error?.response?.data?.errors?.[0]?.message || `Failed to Add ${integrationName} Account`;
        snackbar.error(errorMessage);
        handleCloseModal(false);
      } finally {
        setIsSubmitting(false);
      }
      return;
    }

    const { account_id, integration_config_name, account_mapping: _, ...restFormValues } = formValues;
    const transformedValues = Object.entries(restFormValues).map(([key, value]) => {
      const field = config.properties?.[key];
      const fieldType = field?.type;
      let transformedValue = value;
      if (fieldType === 'boolean' || fieldType === 'bool' || fieldType === 'integer') {
        transformedValue = String(value);
      } else if (field?.is_encrypted && editData?.integration_config_values?.[key] && value === '*************************************************') {
        transformedValue = editData?.integration_config_values?.[key];
      }
      return {
        name: key,
        value: transformedValue,
        is_encrypted:
          field?.is_encrypted && editData?.integration_config_values?.[key] && value === '*************************************************'
            ? true
            : false,
      };
    });

    // Add account_mapping for pagerduty_webhook and datadog_webhook
    if (integrationName.includes('_webhook')) {
      const accountMapping = {};

      // Add simple label mapping if provided
      if (simpleLabelValue.trim()) {
        accountMapping.labelName = simpleLabelValue.trim();
      }

      // Add dynamic label to account mappings from the array
      accountMappings.forEach((mapping) => {
        if (mapping.label.trim() && mapping.accountId) {
          accountMapping[mapping.label.trim()] = mapping.accountId;
        }
      });

      // Only add to transformedValues if there's at least one mapping
      if (Object.keys(accountMapping).length > 0) {
        transformedValues.push({
          name: 'account_mapping',
          value: JSON.stringify(accountMapping),
          is_encrypted: false,
        });
      }
    }

    setIsSubmitting(true);

    // Ticketing systems use ticket_integration_create_config (ticket server)
    // Webhooks and other integrations use integrations_create_config (services server)
    const ticketingIntegrations = ['pagerduty', 'jira', 'github', 'gitlab', 'zenduty', 'servicenow'];
    const isTicketingSystem = ticketingIntegrations.includes(integrationName);

    if (isTicketingSystem) {
      // Transform payload for ticket server format
      // Use transformed values for top-level fields so masked encrypted values
      // (like password) are correctly resolved to the original encrypted ciphertext
      const getTransformed = (fieldName) => transformedValues.find((v) => v.name === fieldName)?.value ?? formValues[fieldName];
      const ticketPayload = {
        name: integration_config_name,
        tool: integrationName,
        url: getTransformed('url'),
        username: getTransformed('username'),
        password: getTransformed('password'),
        auth_type: getTransformed('auth_type'),
        config_values: transformedValues.map((v) => ({ name: v.name, value: v.value })),
      };

      apiIntegrations
        .createTicketIntegration(ticketPayload)
        .then((res) => {
          const successId = res?.data?.data?.ticket_integration_create_config?.id;
          if (successId) {
            handleCloseModal(true);
          } else {
            snackbar.error(`${parseHttpResponseBodyMessage(res?.data)}`);
          }
        })
        .catch((err) => {
          console.error('Failed to create ticket integration:', err);
          snackbar.error(parseHttpResponseBodyMessage(err) || 'Failed to create ticket integration');
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    } else {
      if (!editData && integrationName.includes('_webhook')) {
        transformedValues.push({
          name: 'token',
          value: '',
          is_encrypted: false,
        });
      }

      let normalizedAccountIds;
      if (Array.isArray(account_id)) {
        normalizedAccountIds = account_id;
      } else if (account_id) {
        normalizedAccountIds = [account_id];
      } else {
        normalizedAccountIds = [];
      }

      const payload = {
        ...(editData?.id && { integration_id: editData.id }),
        integration_name: integrationName === 'elasticsearch' ? 'ES' : integrationName,
        account_ids: normalizedAccountIds,
        integration_config_name,
        skip_validation: shouldSkipValidation(),
        source: editData?.source || 'user',
        integration_config_values: transformedValues,
      };

      apiIntegrations
        .addIntegrations(payload)
        .then((res) => {
          const configs = res?.data?.data?.integrations_create_config?.configs || [];
          const isNewCreation = !editData?.name;
          if (configs.length > 0) {
            if (isNewCreation && integrationName.endsWith('_webhook')) {
              const findToken = configs.find((f) => f.name == 'token');
              if (findToken) {
                setResponse(findToken);
                setShowModal(true);
              }
            } else if (isNewCreation && integrationName === 'vm_agent') {
              const accessKey = configs.find((f) => f.name === 'access_key');
              const accessSecret = configs.find((f) => f.name === 'access_secret');
              if (accessKey && accessSecret) {
                setVmAgentCredentials({ accessKey: accessKey.value, accessSecret: accessSecret.value });
                setShowModal(true);
              } else {
                handleCloseModal(true);
              }
            } else {
              handleCloseModal(true);
            }
          } else {
            snackbar.error(`${parseHttpResponseBodyMessage(res?.data)}`);
          }
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    }
  };

  const webhookConfig = {
    pagerduty_webhook: {
      endpoint: 'pagerduty',
      message: 'Configure the following url in pagerduty webhook subscription',
      learnMore: {
        url: 'https://docs.nudgebee.com/docs/integrations/Webhooks/pagerduty_webhook/',
        text: 'how to configure PagerDuty Webhook',
      },
    },
    zenduty_webhook: {
      endpoint: 'zenduty',
      message: 'Configure the following URL in ZenDuty outgoing webhook',
      learnMore: {
        url: 'https://docs.zenduty.com/docs/outgoing-webhooks',
        text: 'how to create Outgoing Webhooks in ZenDuty',
      },
    },
    prometheus_alertmanager_webhook: {
      endpoint: 'prometheus-alertmanager',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
    },
    datadog_webhook: {
      endpoint: 'datadog',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
      learnMore: {
        url: 'https://docs.nudgebee.com/docs/integrations/Webhooks/datadog_webhook/#step-2-configure-datadog-webhook-integration',
        text: 'how to configure Datadog Webhook payload',
      },
    },
    azure_monitor_webhook: {
      endpoint: 'azure-monitor',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
    },
    gcp_monitoring_webhook: {
      endpoint: 'gcp-monitoring',
      message: 'Configure the following URL as a webhook notification channel in GCP Cloud Monitoring',
    },
    servicenow_webhook: {
      endpoint: 'servicenow',
      message: 'Configure the following url in your monitoring and alerting webhook subscription',
      learnMore: {
        url: 'https://docs.nudgebee.com/docs/integrations/Webhooks/servicenow_webhook/',
        text: 'how to configure ServiceNow Webhook',
      },
    },
    newrelic_webhook: {
      endpoint: 'newrelic',
      message: 'Configure the following URL in New Relic notification destination',
      learnMore: {
        url: 'https://docs.nudgebee.com/docs/integrations/Webhooks/newrelic_webhook/',
        text: 'how to configure New Relic Webhook',
      },
    },
    dynatrace_webhook: {
      endpoint: 'dynatrace',
      message: 'Configure the following URL in Dynatrace Settings \u2192 Integrations \u2192 Problem notifications',
      learnMore: {
        url: 'https://docs.nudgebee.com/docs/integrations/Webhooks/dynatrace_webhook/',
        text: 'how to configure Dynatrace Webhook',
      },
    },
    splunk_webhook: {
      endpoint: 'splunk',
      message: 'Configure the following URL in your Splunk alerting webhook subscription',
    },
    grafana_webhook: {
      endpoint: 'grafana',
      message: 'Configure the following URL in your Grafana alerting webhook contact point',
    },
    solarwinds_webhook: {
      endpoint: 'solarwinds',
      message: 'Configure the following URL in SolarWinds Observability alert webhook action',
      learnMore: {
        url: 'https://docs.nudgebee.com/docs/integrations/Webhooks/solarwinds_webhook/',
        text: 'how to configure SolarWinds Observability Webhook',
      },
    },
  };

  const renderWebhookContent = (integrationName, response) => {
    const config = webhookConfig[integrationName];
    if (!config) {
      return null;
    }

    const url = `${window.location.origin}/api/webhooks/${config.endpoint}?token=${response.value}`;

    return (
      <Grid container mt={2} mb={1} mr={1} sx={{ display: 'flex', flexDirection: 'column' }}>
        <Typography variant='subtitle1' sx={{ fontSize: '14px' }}>
          {config.message}
        </Typography>

        <Box
          sx={{
            mt: '15px',
            mb: '20px',
            p: 2,
            borderRadius: 2,
            border: `1px solid ${colors.border.secondary}`,
            backgroundColor: colors.background?.tertiaryLight || '#F3F3F3',
            display: 'flex',
            alignItems: 'flex-start',
            gap: 1,
          }}
        >
          <Typography
            sx={{ color: colors.text.greyDark, fontSize: '14px', wordBreak: 'break-all', lineHeight: 1.6, flex: 1 }}
            variant='body1'
            id={`${config.endpoint}-info`}
          >
            {url}
          </Typography>
          <CopyableText copyableText={url} iconSize={16} iconOnly />
        </Box>

        <Box
          sx={{
            mb: 2,
            p: 1.5,
            borderRadius: 1,
            backgroundColor: colors.background?.lightInfo || '#f1f6ff',
            border: `1px solid ${colors.border.secondary}`,
          }}
        >
          <Typography sx={{ fontSize: '13px', color: colors.text.secondary, lineHeight: 1.6, textAlign: 'justify' }}>
            <strong>Tip (optional):</strong> When you paste this URL into your webhook provider, you can optionally append extra query parameters
            (e.g. <code>&amp;env=prod</code>, <code>&amp;cluster=us-east-1</code>) directly to the URL inside the provider&apos;s configuration. Every
            event delivered through that URL will be tagged with those labels in Nudgebee.
            <br />
            <br />
            <strong>Why add them?</strong> The webhook payload itself rarely carries deployment context like environment or cluster, so multiple
            senders pointing at the same Nudgebee webhook (e.g. dev and prod alertmanagers) produce events that look identical. Adding query labels on
            the provider side lets you tell those events apart, route them to different accounts, and filter them in the Nudgebee inbox without
            changing alert payloads. Reserved keys (<code>token</code>, <code>authorization</code>) are stripped automatically and any label the
            integration extracts from the payload wins on collision.
          </Typography>
        </Box>

        {config.learnMore && (
          <Typography sx={{ fontSize: '14px' }}>
            Learn more about{' '}
            <a style={{ textDecoration: 'none', color: colors.primary }} href={config.learnMore.url} target='_blank' rel='noopener noreferrer'>
              {config.learnMore.text}
            </a>
          </Typography>
        )}
      </Grid>
    );
  };

  const renderContent = () => {
    return renderWebhookContent(integrationName, response);
  };

  const usedAccountIdsKey = accountMappings
    .map((m) => m.accountId)
    .filter(Boolean)
    .join('|');
  const availableAccounts = React.useMemo(() => {
    const allPossible = config.properties?.account_id?.possible_values || [];
    const usedSet = new Set(usedAccountIdsKey ? usedAccountIdsKey.split('|') : []);
    return allPossible.filter((opt) => !usedSet.has(opt.value));
  }, [config.properties?.account_id?.possible_values, usedAccountIdsKey]);

  return (
    <>
      <VmAgentCredentialsDialog
        open={showModal && integrationName === 'vm_agent'}
        onClose={() => handleCloseModal(true)}
        accessKey={vmAgentCredentials?.accessKey}
        accessSecret={vmAgentCredentials?.accessSecret}
      />
      <NDialog
        handleClose={() => {
          handleCloseModal(true);
        }}
        dialogTitle={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px' }}>
            <Typography component='h2' variant='h6' fontWeight={600}>
              {`Set up ${titleCase(integrationName)}`}
            </Typography>
          </Box>
        }
        open={
          showModal &&
          [
            'pagerduty_webhook',
            'zenduty_webhook',
            'prometheus_alertmanager_webhook',
            'datadog_webhook',
            'azure_monitor_webhook',
            'gcp_monitoring_webhook',
            'servicenow_webhook',
            'newrelic_webhook',
            'dynatrace_webhook',
            'splunk_webhook',
            'grafana_webhook',
            'solarwinds_webhook',
          ].includes(integrationName)
        }
        dialogContent={renderContent()}
        additionalComponent={undefined}
        isSubmitRequired={false}
      />
      <Modal
        width='md'
        open={openModal && !showModal}
        handleClose={() => handleCloseModal(false)}
        title={title}
        loader={isSubmitting || isLoadingSchema}
      >
        {isAgentSource ? (
          <Box sx={{ minHeight: '200px', pt: 3, pb: 1 }}>
            <Typography variant='body2' sx={{ color: colors.text.secondaryDark, fontSize: '13px', mb: 2 }}>
              Enable or disable provider settings for each account
            </Typography>
            {agentAccountProviders.map((acc) => (
              <Box
                key={acc.cloud_account_id}
                sx={{
                  py: 1.5,
                  px: 2,
                  mb: 1,
                  borderRadius: 1,
                  border: `1px solid ${colors.border.secondary}`,
                }}
              >
                <Typography variant='body2' sx={{ fontSize: '14px', fontWeight: 500, mb: providerFields.length > 1 ? 1 : 0 }}>
                  {acc.account_name || acc.cloud_account_id}
                </Typography>
                {providerFields.map((provider) => (
                  <Box
                    key={provider.key}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                    }}
                  >
                    <Typography variant='body2' sx={{ fontSize: '13px', color: colors.text.secondaryDark }}>
                      {provider.label}
                    </Typography>
                    <FormControlLabel
                      control={
                        <Switch
                          checked={!!acc[provider.key]}
                          onChange={(e) => handleAgentProviderToggle(acc.cloud_account_id, provider.key, e.target.checked)}
                          size='small'
                          disabled={isSubmitting}
                        />
                      }
                      label={acc[provider.key] ? 'Enabled' : 'Disabled'}
                      labelPlacement='start'
                      sx={{ mr: 0 }}
                    />
                  </Box>
                ))}
              </Box>
            ))}
            <Box
              sx={{
                display: 'flex',
                gap: '12px',
                justifyContent: 'flex-end',
                mt: 3,
                mb: 4,
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <CustomButton id='cancel-btn' text='Close' variant='secondary' size='Medium' onClick={() => handleCloseModal(false)} />
            </Box>
          </Box>
        ) : (
          <>
            <Box sx={{ minHeight: '200px', pt: 3, pb: 1 }}>
              {isLoadingSchema ? (
                <Box
                  sx={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    justifyContent: 'center',
                    py: 8,
                  }}
                >
                  <Typography variant='body2' sx={{ color: colors.text.secondaryDark, fontSize: '13px' }}>
                    Loading configuration...
                  </Typography>
                </Box>
              ) : (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                  {webhookConfig[integrationName]?.learnMore && (
                    <Typography variant='body2' sx={{ fontSize: '13px', color: colors.text.secondaryDark }}>
                      Learn more about{' '}
                      <a
                        style={{ textDecoration: 'none', color: colors.text.primaryDark }}
                        href={webhookConfig[integrationName].learnMore.url}
                        target='_blank'
                        rel='noopener noreferrer'
                      >
                        {webhookConfig[integrationName].learnMore.text}
                      </a>
                    </Typography>
                  )}
                  {Object.keys(config?.properties || {}).length > 0 ? (
                    getSortedFieldKeys().map((key) => {
                      const field = config.properties[key];
                      let inputComponent;
                      const errorText = errors[key] || '';

                      if (!field.description) {
                        return null;
                      }

                      const isRequired = isFieldRequired(key, field);

                      switch (field.type) {
                        case 'array':
                        case 'list':
                          if (field.possible_values) {
                            if (Array.isArray(field.default)) {
                              const rawValue = formValues[key];
                              const value =
                                rawValue != null
                                  ? field.possible_values?.filter(
                                      (op) =>
                                        Array.isArray(rawValue)
                                          ? rawValue.includes(op.value) || rawValue.includes(op) // array case
                                          : op.value === rawValue || op === rawValue // single value case
                                    )
                                  : null;
                              inputComponent = (
                                <Box key={`wrapper-${key}`} sx={{ mb: 0.5 }}>
                                  <Typography
                                    variant='body2'
                                    sx={{
                                      color: colors.text.secondaryDark,
                                      fontSize: '12px',
                                      lineHeight: 1.5,
                                      mb: 1,
                                      pl: 0.5,
                                    }}
                                  >
                                    {field.description}
                                    {isRequired && <span style={{ color: colors.border.error }}> *</span>}
                                  </Typography>
                                  <Box>
                                    <FilterDropdownButton
                                      key={`auto-complete-${key}`}
                                      multiple
                                      label={field.display_name || snakeToTitleCase(key)}
                                      value={value || []}
                                      options={field.possible_values ?? []}
                                      disabled={field.possible_values?.length === 0}
                                      onSelect={(_, value) => handleChange(key, value)}
                                      isOptionsLoading={loadingOptions[key]}
                                    />
                                    {errorText && (
                                      <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: '12px' }}>
                                        {errorText}
                                      </Typography>
                                    )}
                                  </Box>
                                </Box>
                              );
                            } else {
                              const value =
                                formValues[key] != null
                                  ? field.possible_values?.find((op) => op.value == formValues[key] || op == formValues[key])
                                  : null;
                              inputComponent = (
                                <Box key={`wrapper-${key}`} sx={{ mb: 0.5 }}>
                                  <Typography
                                    variant='body2'
                                    sx={{
                                      color: colors.text.secondaryDark,
                                      fontSize: '12px',
                                      lineHeight: 1.5,
                                      mb: 1,
                                      pl: 0.5,
                                    }}
                                  >
                                    {field.description}
                                    {isRequired && <span style={{ color: colors.border.error }}> *</span>}
                                  </Typography>
                                  <Box>
                                    <FilterDropdownButton
                                      key={`auto-complete-${key}`}
                                      label={field.display_name || snakeToTitleCase(key)}
                                      value={value}
                                      options={field.possible_values || []}
                                      disabled={
                                        field.possible_values?.length == 0 ||
                                        (editData?.integration_config_values?.account_id && key == 'account_id') ||
                                        false
                                      }
                                      onSelect={(_, _value) => handleChange(key, _value?.value || _value)}
                                      isOptionsLoading={loadingOptions[key]}
                                    />
                                    {errorText && (
                                      <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: '12px' }}>
                                        {errorText}
                                      </Typography>
                                    )}
                                  </Box>
                                </Box>
                              );
                            }
                          }
                          break;

                        case 'int':
                        case 'integer':
                          inputComponent = (
                            <Box key={`wrapper-${key}`} sx={{ mb: 0.5 }}>
                              <Typography
                                variant='body2'
                                sx={{
                                  color: colors.text.secondaryDark,
                                  fontSize: '12px',
                                  lineHeight: 1.5,
                                  mb: 1,
                                  pl: 0.5,
                                }}
                              >
                                {field.description}
                                {isRequired && <span style={{ color: colors.border.error }}> *</span>}
                              </Typography>
                              <Box>
                                <TextField
                                  id={toKebabCase(field.display_name || key)}
                                  key={key}
                                  label={field.display_name || snakeToTitleCase(key)}
                                  type='number'
                                  value={formValues[key] || ''}
                                  onChange={(e) => handleChange(key, parseInt(e.target.value, 10))}
                                  size='small'
                                  error={!!errorText}
                                  helperText={errorText}
                                  fullWidth
                                />
                              </Box>
                            </Box>
                          );
                          break;

                        case 'bool':
                        case 'boolean':
                          inputComponent = (
                            <Box key={`wrapper-${key}`} sx={{ mb: 0.5 }}>
                              <Typography
                                variant='body2'
                                sx={{
                                  color: colors.text.secondaryDark,
                                  fontSize: '12px',
                                  lineHeight: 1.5,
                                  mb: 1,
                                  pl: 0.5,
                                }}
                              >
                                {field.description}
                              </Typography>
                              <Box>
                                <FormControlLabel
                                  key={key}
                                  id={toKebabCase(field.display_name || key)}
                                  control={<Checkbox checked={!!formValues[key]} onChange={(e) => handleChange(key, e.target.checked)} />}
                                  label={field.display_name || snakeToTitleCase(key)}
                                  required={isRequired}
                                />
                                {errorText && (
                                  <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: '12px' }}>
                                    {errorText}
                                  </Typography>
                                )}
                              </Box>
                            </Box>
                          );
                          break;

                        case 'string':
                          if (field.possible_values?.length > 0) {
                            inputComponent = (
                              <Box key={`wrapper-${key}`} sx={{ mb: 0.5 }}>
                                <Typography
                                  variant='body2'
                                  sx={{
                                    color: colors.text.secondaryDark,
                                    fontSize: '12px',
                                    lineHeight: 1.5,
                                    mb: 1,
                                    pl: 0.5,
                                  }}
                                >
                                  {field.description}
                                  {isRequired && <span style={{ color: colors.border.error }}> *</span>}
                                </Typography>
                                <Box>
                                  <FilterDropdownButton
                                    key={key}
                                    label={field.display_name || snakeToTitleCase(key)}
                                    options={field.possible_values}
                                    value={formValues[key] || ''}
                                    onSelect={(_event, value) => handleChange(key, value?.value ?? value)}
                                    isOptionsLoading={loadingOptions[key]}
                                  />
                                  {errorText && (
                                    <Typography variant='body2' color='error' sx={{ mt: 0.5, fontSize: '12px' }}>
                                      {errorText}
                                    </Typography>
                                  )}
                                </Box>
                              </Box>
                            );
                          } else {
                            inputComponent = (
                              <Box key={`wrapper-${key}`} sx={{ mb: 0.5 }}>
                                <Typography
                                  variant='body2'
                                  sx={{
                                    color: colors.text.secondaryDark,
                                    fontSize: '12px',
                                    lineHeight: 1.5,
                                    mb: 1,
                                    pl: 0.5,
                                  }}
                                >
                                  {field.description}
                                  {isRequired && <span style={{ color: colors.border.error }}> *</span>}
                                </Typography>
                                <Box>
                                  <TextField
                                    key={key}
                                    id={toKebabCase(field.display_name || key)}
                                    label={field.display_name || snakeToTitleCase(key)}
                                    type='text'
                                    value={formValues[key] || ''}
                                    onChange={(e) => handleChange(key, e.target.value)}
                                    size='small'
                                    error={!!errorText}
                                    helperText={errorText}
                                    fullWidth
                                    multiline={!!field.multiline}
                                    minRows={field.multiline ? 3 : undefined}
                                    disabled={
                                      field.disabled ||
                                      field.allow_edit === false ||
                                      (editData?.integration_config_values?.integration_config_name && key == 'integration_config_name') ||
                                      false
                                    }
                                  />
                                </Box>
                              </Box>
                            );
                          }
                          break;

                        default:
                          inputComponent = null;
                      }

                      return inputComponent || null;
                    })
                  ) : (
                    <Box
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'center',
                        justifyContent: 'center',
                        py: 6,
                        px: 3,
                      }}
                    >
                      <Typography variant='body1' sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500 }}>
                        Nothing to Configure
                      </Typography>
                      <Typography variant='body2' sx={{ color: colors.text.secondaryDark, fontSize: '13px', mt: 1, textAlign: 'center' }}>
                        This integration has been set up and doesn't require additional configuration
                      </Typography>
                    </Box>
                  )}
                </Box>
              )}
            </Box>
            {integrationName.includes('_webhook') && editData?.name && (
              <>
                <Typography
                  variant='body2'
                  sx={{
                    color: colors.text.secondary,
                    fontSize: '14px',
                    fontWeight: 500,
                    mb: 2,
                  }}
                >
                  Account Mapping (Optional)
                </Typography>
                <Typography
                  variant='body2'
                  sx={{
                    color: colors.text.secondaryDark,
                    fontSize: '12px',
                    mb: 2,
                    pl: 0.5,
                  }}
                >
                  Map a label from your webhook to a specific account. This allows automatic account selection based on webhook payload.
                </Typography>
                {/* Simple Label Field */}
                <Box sx={{ mt: 3 }}>
                  <CustomTextField
                    label='Label'
                    placeholder='Enter Label Name'
                    value={simpleLabelValue}
                    onChange={(e) => setSimpleLabelValue(e.target.value)}
                    fullWidth
                    size='small'
                    instructionText='Optional: Specify a label name for account mapping'
                  />
                </Box>

                {/* Account Mapping Section */}
                <Box sx={{ mt: 3 }}>
                  {accountMappings.map((mapping, index) => (
                    <Box key={index} sx={{ display: 'flex', gap: 2, mb: 2 }}>
                      <Box sx={{ flex: 5 }}>
                        <CustomTextField
                          label={index === 0 ? 'Label Value' : ''}
                          placeholder='Enter Label Value'
                          value={mapping.label}
                          onChange={(e) => handleMappingChange(index, 'label', e.target.value)}
                          fullWidth
                          size='small'
                        />
                      </Box>
                      <Box sx={{ flex: 5, display: 'flex', alignItems: 'flex-end' }}>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
                          <FilterDropdownButton
                            label='Account'
                            options={[
                              ...availableAccounts,
                              ...(mapping.accountId
                                ? config.properties?.account_id?.possible_values?.filter((g) => g.value === mapping.accountId) || []
                                : []),
                            ]}
                            value={mapping.accountId}
                            onSelect={(_event, value) => handleMappingChange(index, 'accountId', value?.value ?? value)}
                            isOptionsLoading={loadingOptions.account_id}
                            disabled={!config.properties?.account_id?.possible_values?.length}
                            sx={{ height: '45px' }}
                          />
                          {index === accountMappings.length - 1 ? (
                            availableAccounts.length > 0 && (
                              <IconButton
                                onClick={handleAddMapping}
                                sx={{
                                  backgroundColor: colors.background.primaryLight,
                                  borderRadius: '6px',
                                  padding: '8px',
                                  '&:hover': { backgroundColor: '#93C5FD' },
                                }}
                                data-testid='add-mapping-btn'
                                disabled={!mapping.label || !mapping.accountId}
                              >
                                <SafeIcon src={PlusIcon} alt='Add' style={{ width: '15px', height: '15px' }} />
                              </IconButton>
                            )
                          ) : (
                            <IconButton
                              onClick={() => handleRemoveMapping(index)}
                              sx={{
                                backgroundColor: '#FEE2E2',
                                borderRadius: '6px',
                                padding: '8px',
                                '&:hover': { backgroundColor: colors.background.errorLight },
                              }}
                              data-testid={`delete-mapping-btn-${index}`}
                            >
                              <SafeIcon
                                src={NewDelete}
                                alt='Delete'
                                style={{
                                  width: '15px',
                                  height: '15px',
                                  filter: 'invert(22%) sepia(93%) saturate(6245%) hue-rotate(355deg) brightness(97%) contrast(95%)',
                                }}
                              />
                            </IconButton>
                          )}
                        </Box>
                      </Box>
                    </Box>
                  ))}
                </Box>
              </>
            )}
            <Box
              sx={{
                display: 'flex',
                gap: '12px',
                justifyContent: 'flex-end',
                mt: 3,
                mb: 4,
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <CustomButton
                id='cancel-btn'
                text='Cancel'
                variant='secondary'
                size='Medium'
                onClick={() => handleCloseModal(false)}
                disabled={isSubmitting || isTesting}
              />
              {isTestable && (
                <CustomButton
                  id='test-connection-btn'
                  text={isTesting ? 'Testing...' : 'Test Connection'}
                  variant='primary'
                  size='Medium'
                  onClick={handleTestConnection}
                  disabled={isSubmitting || isTesting}
                />
              )}
              <CustomButton
                size='Medium'
                id={'create-integration-acc'}
                text={editData && Object.keys(editData).length ? 'Update' : 'Save'}
                disabled={isSubmitting || isTesting || (isTestable && !connectionVerified)}
                onClick={() => {
                  submitForm();
                }}
                label='Save Webhook'
              />
            </Box>
          </>
        )}
      </Modal>
    </>
  );
};

IntegrationDynamicFormModal.propTypes = {
  integrationName: PropTypes.string,
  openModal: PropTypes.bool,
  handleClose: PropTypes.func,
  title: PropTypes.string,
  integrationData: PropTypes.array,
  editData: PropTypes.object,
  listIntegrationConfigurationById: PropTypes.func,
};

export default IntegrationDynamicFormModal;
