import React, { useState } from 'react';
import { Box } from '@mui/material';
import { Select } from '@ui/Select';

// Keep custom-tag creation local to KBs; other Select consumers are unchanged.
export default function KnowledgeContextTags({ value: passedValue, options = [], loading = false, onChange }) {
  const value = Array.isArray(passedValue) ? passedValue.filter((tag) => typeof tag === 'string') : [];
  const [error, setError] = useState('');
  const mergedByKey = new Map();
  (Array.isArray(options) ? options : []).forEach((option) => {
    if (typeof option?.value === 'string' && !mergedByKey.has(option.value.toLowerCase())) {
      mergedByKey.set(option.value.toLowerCase(), option);
    }
  });
  value.forEach((tag) => {
    const key = tag.toLowerCase();
    // Select uses exact values for selection: retain saved spelling when a
    // resource suggestion differs only in case, while keeping its group.
    const existing = mergedByKey.get(key);
    mergedByKey.set(key, existing ? { ...existing, value: tag } : { value: tag, label: tag, group: 'Saved tags' });
  });
  const mergedOptions = [...mergedByKey.values()];
  const addTag = (tag) => {
    if (!tag) return false;
    if ([...tag].length > 128 || /[\u0000-\u001f\u007f-\u009f]/.test(tag)) {
      setError('Use at most 128 characters without control characters.');
      return false;
    }
    if (value.some((item) => item.toLowerCase() === tag.toLowerCase())) {
      setError('This tag is already selected.');
      return false;
    }
    if (value.length >= 32) {
      setError('You can add up to 32 tags.');
      return false;
    }
    onChange([...value, tag]);
    setError('');
    return true;
  };
  return (
    <Box>
      <Select
        multiple
        disablePortal={false}
        grouped
        size='sm'
        placeholder='Select or add tags…'
        searchPlaceholder='Search or add a tag…'
        onCreateOption={addTag}
        error={error}
        value={value}
        options={mergedOptions}
        loading={loading}
        onChange={(next) => {
          if (next.length > 32) {
            setError('You can add up to 32 tags.');
            return false;
          }
          onChange(next);
          setError('');
        }}
        id='kb-context-tags'
      />
    </Box>
  );
}
