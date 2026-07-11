import React, { useMemo } from 'react';
import PropTypes from 'prop-types';
import PersonOutlinedIcon from '@mui/icons-material/PersonOutlined';
import GroupsOutlinedIcon from '@mui/icons-material/GroupsOutlined';
import { Select } from '@ui/Select';
import useOwnerDirectory from './useOwnerDirectory';

// Single Select listing users AND groups. The option value is encoded
// "user:<id>" / "group:<id>"; onChange emits { ownerType, ownerId } (or null
// when cleared). Each option carries a user/group icon so the two are
// distinguishable. This is the one place the "either user or group" encoding
// lives, reused across every assign surface + the rule modal.
export default function OwnerPicker({ value, onChange, disabled, id, label }) {
  const { options, loading } = useOwnerDirectory();
  const selected = value ? `${value.ownerType}:${value.ownerId}` : null;

  // Attach a user/group icon to each option so groups are visually identifiable.
  const iconOptions = useMemo(
    () =>
      options.map((o) => ({
        ...o,
        icon:
          o.kind === 'group' ? (
            <GroupsOutlinedIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)' }} />
          ) : (
            <PersonOutlinedIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)' }} />
          ),
      })),
    [options]
  );

  const handleChange = (next) => {
    if (!next) {
      onChange(null);
      return;
    }
    const idx = next.indexOf(':');
    onChange({ ownerType: next.slice(0, idx), ownerId: next.slice(idx + 1) });
  };

  return (
    <Select
      id={id || 'owner-picker'}
      label={label === undefined ? 'Owner' : label}
      placeholder='Select a user or group'
      options={iconOptions}
      value={selected}
      onChange={handleChange}
      disabled={disabled}
      loading={loading}
      searchable
      clearable
    />
  );
}

OwnerPicker.propTypes = {
  value: PropTypes.shape({ ownerType: PropTypes.string, ownerId: PropTypes.string }),
  onChange: PropTypes.func.isRequired,
  disabled: PropTypes.bool,
  id: PropTypes.string,
  label: PropTypes.node,
};
