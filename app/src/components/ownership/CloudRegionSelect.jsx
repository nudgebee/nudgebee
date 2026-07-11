import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import { fetchCloudDistinct } from './cloudDistinct';

// Single-select of the distinct regions seen in cloud_resourses, optionally scoped
// to one cloud account. Used by the rule modal's cloud_region scope.
export default function CloudRegionSelect({ accountId, value, onChange, id, label, disabled }) {
  const [options, setOptions] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    fetchCloudDistinct('region', accountId)
      .then((opts) => active && setOptions(opts))
      .catch(() => active && setOptions([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [accountId]);

  return (
    <Select
      id={id || 'cloud-region-select'}
      label={label === undefined ? 'Region' : label}
      placeholder='Select a region'
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

CloudRegionSelect.propTypes = {
  accountId: PropTypes.string,
  value: PropTypes.string,
  onChange: PropTypes.func.isRequired,
  id: PropTypes.string,
  label: PropTypes.node,
  disabled: PropTypes.bool,
};
