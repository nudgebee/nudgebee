import React, { useEffect, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Select } from '@ui/Select';
import k8sApi from '@api1/kubernetes';

// Multi-select of workloads within one account + namespace. value is an array of
// cloud_resource_id; onChange emits the selected workload objects
// [{ cloud_resource_id, name }] so callers can use either the ids (bulk manual
// assign) or the names (workload rule). Disabled until a namespace is chosen.
// initialNames pre-selects by name once (used when editing a workload rule, which
// stores names, not ids).
export default function WorkloadMultiSelect({ accountId, namespace, value, onChange, id, label, disabled, initialNames }) {
  const [options, setOptions] = useState([]);
  const [byId, setById] = useState({});
  const [loading, setLoading] = useState(false);
  const resolvedInitial = useRef(false);

  useEffect(() => {
    if (!accountId || !namespace) {
      setOptions([]);
      setById({});
      return undefined;
    }
    let active = true;
    setLoading(true);
    k8sApi
      .getK8sWorkload(500, 0, { accountId, namespaceName: namespace }, { name: '', order: '' }, false)
      .then((res) => {
        if (!active) return;
        const list = res?.data?.k8s_workloads || [];
        const opts = [];
        const map = {};
        list.forEach((w) => {
          if (w?.cloud_resource_id) {
            opts.push({ value: w.cloud_resource_id, label: w.name || w.cloud_resource_id });
            map[w.cloud_resource_id] = { cloud_resource_id: w.cloud_resource_id, name: w.name };
          }
        });
        setOptions(opts);
        setById(map);
        // Edit prefill: resolve the rule's stored names to ids on first load.
        if (!resolvedInitial.current && (!value || value.length === 0) && initialNames && initialNames.length) {
          const picked = list.filter((w) => initialNames.includes(w.name)).map((w) => ({ cloud_resource_id: w.cloud_resource_id, name: w.name }));
          if (picked.length) {
            resolvedInitial.current = true;
            onChange(picked);
          }
        }
      })
      .catch(() => active && setOptions([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, namespace]);

  return (
    <Select
      multiple
      id={id || 'workload-multiselect'}
      label={label === undefined ? 'Workloads' : label}
      placeholder='Select workloads'
      options={options}
      value={value || []}
      onChange={(ids) => onChange((ids || []).map((wid) => byId[wid]).filter(Boolean))}
      loading={loading}
      disabled={disabled || !namespace}
      searchable
    />
  );
}

WorkloadMultiSelect.propTypes = {
  accountId: PropTypes.string,
  namespace: PropTypes.string,
  value: PropTypes.arrayOf(PropTypes.string),
  onChange: PropTypes.func.isRequired,
  id: PropTypes.string,
  label: PropTypes.node,
  disabled: PropTypes.bool,
  initialNames: PropTypes.arrayOf(PropTypes.string),
};
