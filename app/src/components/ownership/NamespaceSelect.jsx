import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import k8sApi from '@api1/kubernetes';

// Single-select of namespaces for a given cloud account. When accountId is empty
// it lists the tenant's namespaces across accounts — unless requireAccount is set,
// in which case it waits for an account before fetching (the cascade case). Used
// by the rule modal (namespace + workload scopes) so admins pick from real
// namespaces instead of free-typing.
export default function NamespaceSelect({ accountId, value, onChange, id, label, disabled, requireAccount }) {
  const [options, setOptions] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    // Don't fetch until an account is chosen when the account drives the list.
    if (requireAccount && !accountId) {
      setOptions([]);
      setLoading(false);
      return undefined;
    }
    let active = true;
    setLoading(true);
    k8sApi
      .getK8sNamespaces(500, 0, accountId ? { accountId } : {})
      .then((res) => {
        if (!active) return;
        const rows = res?.data?.k8s_namespaces || [];
        const seen = new Set();
        const opts = [];
        rows.forEach((n) => {
          if (n?.name && !seen.has(n.name)) {
            seen.add(n.name);
            opts.push({ value: n.name, label: n.name });
          }
        });
        setOptions(opts);
      })
      .catch(() => active && setOptions([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [accountId, requireAccount]);

  return (
    <Select
      id={id || 'namespace-select'}
      label={label === undefined ? 'Namespace' : label}
      placeholder='Select a namespace'
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

NamespaceSelect.propTypes = {
  accountId: PropTypes.string,
  value: PropTypes.string,
  onChange: PropTypes.func.isRequired,
  id: PropTypes.string,
  label: PropTypes.node,
  disabled: PropTypes.bool,
  requireAccount: PropTypes.bool,
};
