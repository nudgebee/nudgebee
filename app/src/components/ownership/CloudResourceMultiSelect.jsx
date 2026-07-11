import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import apiCloudAccount from '@api1/cloud-account';

// Multi-select of cloud resources within one account. value/onChange are arrays of
// cloud_resourses.id — the rule stores ids directly, so (unlike workloads) there's
// no name→id resolution and edit-prefill is just the stored id list. Disabled until
// an account is chosen.
export default function CloudResourceMultiSelect({ accountId, value, onChange, id, label, disabled }) {
  const [options, setOptions] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!accountId) {
      setOptions([]);
      return undefined;
    }
    let active = true;
    setLoading(true);
    apiCloudAccount
      .getCloudResource({ account_id: accountId }, 1000, 0)
      .then((res) => {
        if (!active) return;
        const list = res?.data?.data?.cloud_resourses || [];
        setOptions(list.filter((r) => r?.id).map((r) => ({ value: r.id, label: r.name || r.resourse_id || r.id })));
      })
      .catch(() => active && setOptions([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [accountId]);

  return (
    <Select
      multiple
      id={id || 'cloud-resource-multiselect'}
      label={label === undefined ? 'Resources' : label}
      placeholder='Select resources'
      options={options}
      value={value || []}
      onChange={(ids) => onChange(ids || [])}
      loading={loading}
      disabled={disabled || !accountId}
      searchable
    />
  );
}

CloudResourceMultiSelect.propTypes = {
  accountId: PropTypes.string,
  value: PropTypes.arrayOf(PropTypes.string),
  onChange: PropTypes.func.isRequired,
  id: PropTypes.string,
  label: PropTypes.node,
  disabled: PropTypes.bool,
};
