import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import { fetchCloudDistinct } from './cloudDistinct';

// Single-select of the distinct cloud_resourses.type values (e.g. ec2_instance),
// optionally scoped to one cloud account. The backend matches the stored value
// against either `type` or `service_name`, so either form resolves.
export default function CloudResourceTypeSelect({ accountId, value, onChange, id, label, disabled }) {
  const [options, setOptions] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    fetchCloudDistinct('type', accountId)
      .then((opts) => active && setOptions(opts))
      .catch(() => active && setOptions([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [accountId]);

  return (
    <Select
      id={id || 'cloud-type-select'}
      label={label === undefined ? 'Resource type' : label}
      placeholder='Select a resource type'
      options={options}
      value={value || null}
      onChange={(next) => onChange(next || '')}
      loading={loading}
      disabled={disabled}
      searchable
      clearable
    />
  );
}

CloudResourceTypeSelect.propTypes = {
  accountId: PropTypes.string,
  value: PropTypes.string,
  onChange: PropTypes.func.isRequired,
  id: PropTypes.string,
  label: PropTypes.node,
  disabled: PropTypes.bool,
};
