import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import apiUserManagement from '@api1/user';

// Single-select of the tenant's cloud accounts. Shared by the rule modal and the
// bulk-assign modal. Value is the account id; label is account_name.
// `providerFilter` optionally narrows the list by cloud_provider — e.g. K8S from
// ./accountProviders to show only Kubernetes clusters in K8s rule flows. Default = all.
export default function AccountSelect({ value, onChange, id, label, placeholder, required, clearable, disabled, providerFilter }) {
  const [options, setOptions] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    apiUserManagement
      .listAccounts()
      .then((rows) => {
        if (!active) return;
        const list = (Array.isArray(rows) ? rows : []).filter((a) => (providerFilter ? providerFilter(a.cloud_provider) : true));
        setOptions(list.map((a) => ({ value: a.id, label: a.account_name || a.id })));
      })
      .catch(() => active && setOptions([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [providerFilter]);

  return (
    <Select
      id={id || 'account-select'}
      label={label}
      placeholder={placeholder || 'Select an account'}
      options={options}
      value={value || null}
      onChange={(next) => onChange(next || '')}
      loading={loading}
      searchable
      clearable={clearable !== false}
      required={required}
      disabled={disabled}
    />
  );
}

AccountSelect.propTypes = {
  value: PropTypes.string,
  onChange: PropTypes.func.isRequired,
  id: PropTypes.string,
  label: PropTypes.node,
  placeholder: PropTypes.string,
  required: PropTypes.bool,
  clearable: PropTypes.bool,
  disabled: PropTypes.bool,
  providerFilter: PropTypes.func,
};
