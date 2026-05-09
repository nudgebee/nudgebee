import React, { useEffect, useState } from 'react';
import { TextField, Autocomplete, Grid, CircularProgress } from '@mui/material';
import apiTickets from '@api1/tickets';
import CustomDateTimePicker from '@components1/common/widgets/CustomDateTimePicker';
import { inputCustomSx } from '@data/themes/inputField';
import PropTypes from 'prop-types';

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
      keyValues = value.map((m) => m.id);
    } else if (fields[key].type == 'select') {
      if (key.includes('customfield_')) {
        keyValues = { id: value?.id || value?.value };
      } else {
        keyValues = value?.id || value?.value;
      }
    } else if (fields[key].type == 'multicheckboxes') {
      keyValues = value.map((obj) => {
        return { id: obj.value };
      });
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
                label: m.name,
                id: m.id || m.value,
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
                <TextField
                  fullWidth
                  size='small'
                  margin='normal'
                  value={initialValues[field.key] || ''}
                  required={field?.required || false}
                  key={field.name}
                  label={field.name}
                  onChange={(e) => handleChange(field.key, e.target.value)}
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
                  error={!!showError}
                  helperText={showError || ''}
                />
              </Grid>
            );
          case 'select':
            return (
              <Grid item xs={6} key={field.key}>
                <Autocomplete
                  value={
                    options[field.key]?.find((o) => {
                      // Handle undefined or null values
                      if (initialValues[field.key] == null) {
                        return false;
                      }
                      // Handle primitive values
                      if (typeof initialValues[field.key] !== 'object') {
                        return o.value === initialValues[field.key] || o.id === initialValues[field.key];
                      }
                      // Handle object values
                      if (initialValues[field.key]?.id) {
                        return o.value === initialValues[field.key].id || o.id === initialValues[field.key].id;
                      }
                      return false;
                    }) || null
                  }
                  disablePortal
                  key={field.key}
                  blurOnSelect={'mouse'}
                  sx={{ ...inputCustomSx }}
                  options={options[field.key] || []}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={field.name}
                      margin='normal'
                      size='small'
                      required={field?.required || false}
                      error={!!showError}
                      helperText={showError || ''}
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <>
                            {loadingOptions[field.key] ? <CircularProgress color='inherit' size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </>
                        ),
                      }}
                    />
                  )}
                  onChange={(e, newValue) => handleChange(field.key, newValue)}
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
                />
              </Grid>
            );
          case 'array':
            return (
              <Grid item xs={12} key={field.key}>
                <Autocomplete
                  value={options[field.key]?.filter((o) => initialValues[field.key]?.includes(o.id)) || []}
                  multiple
                  blurOnSelect={'mouse'}
                  sx={{ ...inputCustomSx }}
                  options={options[field.key] || []}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={field.name}
                      margin='normal'
                      size='small'
                      required={field?.required || false}
                      error={!!showError}
                      helperText={showError || ''}
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <>
                            {loadingOptions[field.key] ? <CircularProgress color='inherit' size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </>
                        ),
                      }}
                    />
                  )}
                  onChange={(e, newValue) => handleChange(field.key, newValue)}
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
                />
              </Grid>
            );
          case 'multicheckboxes':
            return (
              <Grid item xs={12} key={field.key}>
                <Autocomplete
                  value={options[field.key]?.filter((o) => initialValues[field.key]?.map((o) => o.id).includes(o.value)) || []}
                  multiple
                  blurOnSelect={'mouse'}
                  sx={{ ...inputCustomSx }}
                  options={options[field.key] || []}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={field.name}
                      margin='normal'
                      size='small'
                      required={field?.required || false}
                      error={!!showError}
                      helperText={showError || ''}
                    />
                  )}
                  onChange={(e, newValue) => handleChange(field.key, newValue)}
                  onBlur={() => setTouched((prev) => ({ ...prev, [field.key]: true }))}
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
