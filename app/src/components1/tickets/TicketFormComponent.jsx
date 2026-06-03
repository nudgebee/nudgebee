import React, { useEffect, useState } from 'react';
import { Grid } from '@mui/material';
import apiTickets from '@api1/tickets';
import CustomDateTimePicker from '@common-new/widgets/CustomDateTimePicker';
import PropTypes from 'prop-types';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';

const TicketFormComponent = ({ fields, initialValues, onChanges, configurationId, forceValidate }) => {
  const [options, setOptions] = useState({});
  const [loadingOptions, setLoadingOptions] = useState({});
  const [touched, setTouched] = useState({});
  const [errors, setErrors] = useState({});

  const validateField = (key, value) => {
    const field = fields[key];
    if (field?.required) {
      if (
        value === null ||
        value === undefined ||
        value === '' ||
        (Array.isArray(value) && value.length === 0) ||
        (typeof value === 'object' && !Array.isArray(value) && !value?.id)
      ) {
        return `${field.name} is required`;
      }
    }
    return '';
  };

  useEffect(() => {
    const newErrors = {};
    Object.keys(fields).forEach((key) => {
      if (fields[key]?.required) {
        const error = validateField(key, initialValues[key]);
        if (error && (touched[key] || forceValidate)) {
          newErrors[key] = error;
        }
      }
    });
    setErrors(newErrors);
  }, [forceValidate, fields, initialValues, touched]);

  useEffect(() => {
    if (fields) {
      const updates = {};
      let hasUpdates = false;

      Object.keys(fields).forEach((key) => {
        const field = fields[key];
        if ((field.type === 'datetime' || field.type === 'datepicker') && field.required && !initialValues[key]) {
          updates[key] = Date.now();
          hasUpdates = true;
        }
      });

      if (hasUpdates) {
        onChanges({
          ...initialValues,
          ...updates,
        });
      }
    }
  }, [fields]);

  const handleChange = (key, value) => {
    setTouched((prev) => ({ ...prev, [key]: true }));
    const error = validateField(key, value);
    setErrors((prev) => ({
      ...prev,
      [key]: error,
    }));

    let keyValues;
    if (fields[key].type == 'array') {
      keyValues = value;
    } else if (fields[key].type == 'select') {
      if (key.includes('customfield_')) {
        keyValues = { id: value };
      } else {
        keyValues = value;
      }
    } else if (fields[key].type == 'multicheckboxes') {
      keyValues = (value || []).map((v) => ({ id: v }));
    } else if (fields[key].type == 'datetime') {
      keyValues = value?.valueOf();
    } else if (fields[key].type == 'datepicker') {
      keyValues = value?.valueOf();
    } else {
      keyValues = value;
    }
    onChanges({
      ...initialValues,
      [key]: keyValues,
    });
  };

  useEffect(() => {
    if (fields) {
      Object.keys(fields).forEach((f) => {
        getAllowedValues(fields[f]);
      });
    }
  }, [fields]);

  const getAllowedValues = async (field) => {
    if (field?.allowedValues && field?.allowedValues.length > 0) {
      const newOptions = {
        [field.key]: field.allowedValues.map((j) => ({ value: j.key || j.id, label: j?.name || j.value })),
      };
      setOptions((prevOptions) => ({
        ...prevOptions,
        ...newOptions,
      }));
    } else if (field?.autoCompleteUrl) {
      setLoadingOptions((prev) => ({ ...prev, [field.key]: true }));
      apiTickets
        .getTicketFieldValues(configurationId, field.key, field.autoCompleteUrl, '')
        .then((res) => {
          const fieldValues = res?.data?.tickets_get_field_values?.data || [];
          if (fieldValues && fieldValues.length > 0) {
            const newOptions = {
              [field.key]: fieldValues.map((m) => ({
                value: m.id || m.value,
                label: m.name,
              })),
            };
            setOptions((prevOptions) => ({
              ...prevOptions,
              ...newOptions,
            }));
          } else {
            const newOptions = {
              [field.key]: [],
            };
            setOptions((prevOptions) => ({
              ...prevOptions,
              ...newOptions,
            }));
          }
        })
        .catch((error) => {
          console.error(error);
        })
        .finally(() => {
          setLoadingOptions((prev) => ({ ...prev, [field.key]: false })); // Stop loading when API call is finished
        });
    }
    return [];
  };

  const renderFields = () => {
    return Object.keys(fields)
      .filter((b) => b != 'summary' && b != 'description')
      .map((fieldName) => {
        const field = fields[fieldName];
        const showError = errors[field.key];
        switch (field.type) {
          case 'string':
            return (
              <Grid item xs={6} key={field.key}>
                <Input
                  id={field.key}
                  size='sm'
                  value={initialValues[field.key] || ''}
                  required={field?.required || false}
                  label={field.name}
                  onChange={(next) => handleChange(field.key, next)}
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
                  error={showError || ''}
                />
              </Grid>
            );
          case 'select': {
            const rawVal = initialValues[field.key];
            const resolvedVal = typeof rawVal === 'object' && rawVal !== null ? rawVal?.id : rawVal;
            const selectVal = resolvedVal == null ? null : String(resolvedVal);
            return (
              <Grid item xs={6} key={field.key} onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}>
                <Select
                  id={field.key}
                  size='sm'
                  label={field.name}
                  required={field?.required || false}
                  options={options[field.key] || []}
                  value={selectVal}
                  placeholder={loadingOptions[field.key] ? 'Loading...' : 'Select…'}
                  disabled={loadingOptions[field.key]}
                  onChange={(next) => handleChange(field.key, next)}
                  error={showError || ''}
                />
              </Grid>
            );
          }
          case 'array':
            return (
              <Grid item xs={12} key={field.key} onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}>
                <Select
                  id={field.key}
                  multiple
                  size='sm'
                  label={field.name}
                  required={field?.required || false}
                  options={options[field.key] || []}
                  value={
                    Array.isArray(initialValues[field.key])
                      ? initialValues[field.key].map((val) => (typeof val === 'object' && val !== null ? val?.id : val))
                      : []
                  }
                  placeholder={loadingOptions[field.key] ? 'Loading...' : 'Select…'}
                  disabled={loadingOptions[field.key]}
                  onChange={(next) => handleChange(field.key, next)}
                  error={showError || ''}
                />
              </Grid>
            );
          case 'multicheckboxes':
            return (
              <Grid item xs={12} key={field.key} onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}>
                <Select
                  id={field.key}
                  multiple
                  size='sm'
                  label={field.name}
                  required={field?.required || false}
                  options={options[field.key] || []}
                  value={initialValues[field.key]?.map((o) => o.id) || []}
                  onChange={(next) => handleChange(field.key, next)}
                  error={showError || ''}
                />
              </Grid>
            );
          case 'datetime':
            return (
              <Grid item xs={6} key={field.key}>
                <CustomDateTimePicker
                  label={field.name}
                  value={initialValues[field.key]}
                  onChange={(e) => handleChange(field.key, e)}
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
                  error={!!showError}
                  helperText={showError || ''}
                />
              </Grid>
            );
          case 'datepicker':
            return (
              <Grid item xs={6} key={field.key}>
                <CustomDateTimePicker
                  views={['day']}
                  label={field.name}
                  value={initialValues[field.key]}
                  onChange={(e) => handleChange(field.key, e)}
                  format='MM/DD/YYYY'
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
                  error={!!showError}
                  helperText={showError || ''}
                />
              </Grid>
            );
          default:
            return null;
        }
      });
  };

  return (
    <Grid container columnSpacing={2}>
      {renderFields()}
    </Grid>
  );
};

export default TicketFormComponent;

TicketFormComponent.propTypes = {
  fields: PropTypes.object,
  initialValues: PropTypes.object,
  onChanges: PropTypes.func,
  configurationId: PropTypes.any,
  forceValidate: PropTypes.bool,
};
