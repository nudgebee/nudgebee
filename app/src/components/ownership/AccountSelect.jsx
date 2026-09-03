import React, { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import apiUserManagement from '@api1/user';
import CloudProviderIcon from '@shared/icons/CloudIcon';

// 'Other' already means "no real cloud_provider"; 'All' is the synthetic group
// below — neither has a logo to show, unlike 'AWS'/'GCP'/'Azure'/'K8S'.
const renderAccountGroupIcon = (provider) =>
  provider === 'Other' || provider === 'All' ? null : <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

// ds/Select's own `hasSelection` check treats '' as "nothing picked" (renders
// the muted placeholder, not a real selected value) — so an "All accounts"
// state can't be represented by passing value=''. This sentinel is a real,
// non-empty option value the Select treats as a genuine selection (rendered
// in normal text, not placeholder gray); onChange below translates it back
// to '' for the caller, which already treats '' as "no account, tenant-wide".
const ALL_ACCOUNTS_VALUE = '__all__';

// Single-select of the tenant's cloud accounts. Shared by the rule modal, the
// bulk-assign modal, and (with includeAllOption) b-Cortex's header filter.
// Value is the account id; label is account_name.
// `providerFilter` optionally narrows the list by cloud_provider — e.g. K8S from
// ./accountProviders to show only Kubernetes clusters in K8s rule flows. Default = all.
// `includeAllOption` prepends a real "All accounts" (or `allOptionLabel`) entry
// instead of relying on the placeholder for the empty state — for callers where
// "nothing selected" is itself a meaningful, named state (e.g. tenant-wide),
// not just an unfilled field. Implies no clear button: picking that option
// back is how you return to it.
export default function AccountSelect({
  value,
  onChange,
  id,
  label,
  placeholder,
  required,
  clearable,
  disabled,
  providerFilter,
  includeAllOption,
  allOptionLabel,
}) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    apiUserManagement
      .listAccounts()
      .then((result) => {
        if (!active) {
          return;
        }
        setRows(Array.isArray(result) ? result : []);
      })
      .catch(() => active && setRows([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [providerFilter]);

  const options = useMemo(() => {
    const list = rows.filter((a) => (providerFilter ? providerFilter(a.cloud_provider) : true));
    const accountOptions = list.map((a) => ({ value: a.id, label: a.account_name || a.id, group: a.cloud_provider || 'Other' }));
    if (!includeAllOption) {
      return accountOptions;
    }
    return [{ value: ALL_ACCOUNTS_VALUE, label: allOptionLabel || 'All accounts', group: 'All' }, ...accountOptions];
  }, [rows, providerFilter, includeAllOption, allOptionLabel]);

  return (
    <Select
      id={id || 'account-select'}
      label={label}
      placeholder={placeholder || 'Select an account'}
      options={options}
      grouped
      groupIcon={renderAccountGroupIcon}
      value={includeAllOption ? value || ALL_ACCOUNTS_VALUE : value || null}
      onChange={(next) => onChange(includeAllOption && next === ALL_ACCOUNTS_VALUE ? '' : next || '')}
      loading={loading}
      searchable
      clearable={includeAllOption ? false : clearable !== false}
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
  includeAllOption: PropTypes.bool,
  allOptionLabel: PropTypes.string,
};
