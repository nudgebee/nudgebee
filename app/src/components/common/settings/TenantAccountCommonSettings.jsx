import React from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { ds } from '@utils/colors';

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
    <Box>
      <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[4] }}>
        Log Label Mapper
      </Typography>
      <Box display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]}>
        {fields.map(({ label, field, placeholder }) => (
          <Input key={field} size='sm' label={label} value={logSettings[field] || ''} placeholder={placeholder} onChange={handleChange(field)} />
        ))}
      </Box>
    </Box>
  );
};

TenantAccountCommonSettings.propTypes = {
  logSettings: PropTypes.object.isRequired,
  setLogSettings: PropTypes.func.isRequired,
};

export default TenantAccountCommonSettings;
