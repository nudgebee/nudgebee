import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import CustomTextField from '@components1/common/CustomTextField';
import TextWithBorder from '@components1/common/TextWithBorder'; // Update path as needed

const TenantAccountCommonSettings = ({ logSettings, setLogSettings }) => {
  const handleChange = (field) => (e) => {
    setLogSettings((prev) => ({
      ...prev,
      [field]: e.target.value,
    }));
  };

  const fields = [
    { label: 'Pod', field: 'logPodLabel', placeholder: 'Log Pod label' },
    { label: 'Namespace', field: 'logNamespaceLabel', placeholder: 'Log Namespace label' },
    { label: 'App', field: 'logAppLabel', placeholder: 'Log App label' },
    { label: 'Default query', field: 'logDefaultQuery', placeholder: 'Default Query' },
  ];

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
      <TextWithBorder
        value='Log Label Mapper'
        borderColor='#3B82F6'
        borderWidth='3px'
        sx={{
          '& p': {
            fontSize: '16px',
            fontWeight: 500,
            color: '#374151',
            lineHeight: '24px',
          },
        }}
      />

      <Box display='grid' gridTemplateColumns='1fr 1fr' gap='16px'>
        {fields.map(({ label, field, placeholder }) => (
          <CustomTextField key={field} label={label} value={logSettings[field] || ''} placeholder={placeholder} onChange={handleChange(field)} />
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
