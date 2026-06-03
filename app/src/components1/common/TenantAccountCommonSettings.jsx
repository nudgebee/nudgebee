import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Input } from '@components1/ds/Input';

const TenantAccountCommonSettings = ({ logSettings, setLogSettings }) => {
  const handleChange = (field) => (value) => {
    setLogSettings((prev) => ({
      ...prev,
      [field]: value,
    }));
  };

  const fields = [
    { label: 'Pod', field: 'logPodLabel', placeholder: 'Log Pod label' },
    { label: 'Namespace', field: 'logNamespaceLabel', placeholder: 'Log Namespace label' },
    { label: 'App', field: 'logAppLabel', placeholder: 'Log App label' },
    { label: 'Default query', field: 'logDefaultQuery', placeholder: 'Default Query' },
  ];

  return (
    <Box display='grid' gridTemplateColumns='1fr 1fr' gap='16px'>
      {fields.map(({ label, field, placeholder }) => (
        <Input key={field} size='sm' label={label} value={logSettings[field] || ''} placeholder={placeholder} onChange={handleChange(field)} />
      ))}
    </Box>
  );
};

TenantAccountCommonSettings.propTypes = {
  logSettings: PropTypes.object.isRequired,
  setLogSettings: PropTypes.func.isRequired,
};

export default TenantAccountCommonSettings;
