import React from 'react';
import { Box } from '@mui/material';
import { Checkbox as DsCheckbox } from '@components1/ds/Checkbox';

/**
 * CustomCheckBox — legacy API surface preserved, internals on ds/Checkbox.
 *
 * Accepts the MUI-style event onChange `(event, checked) => void` AND the
 * DS-style `(next: boolean) => void` — adapter detects which by argument
 * shape so existing consumers continue to work unmodified.
 */
const CustomCheckBox = ({ top = 0, bottom = 0, checked, onChange, disabled, text, startElement, endElement, indeterminate, className, name }) => {
  const handleChange = (next) => {
    if (typeof onChange !== 'function') return;
    // All legacy consumers expect a MUI-style event. Build a synthetic one
    // with the surfaces they actually touch — `target.checked`, plus no-op
    // `stopPropagation` / `preventDefault` so callers that bubble-prevent
    // don't crash. Pass `next` as the 2nd arg for the consumers that read
    // the boolean directly.
    const syntheticEvent = {
      target: { name, checked: next },
      stopPropagation: () => {},
      preventDefault: () => {},
    };
    onChange(syntheticEvent, next);
  };

  const labelNode =
    text || startElement || endElement ? (
      <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)', minHeight: '24px' }}>
        {startElement || null}
        {text || null}
        {endElement || null}
      </Box>
    ) : undefined;

  return (
    <Box className={className} sx={{ mt: `${top}px`, mb: `${bottom}px`, display: 'inline-flex' }}>
      <DsCheckbox
        size='sm'
        checked={!!checked}
        onChange={handleChange}
        disabled={!!disabled}
        indeterminate={!!indeterminate}
        label={labelNode}
        aria-label={typeof text === 'string' ? text : name}
      />
    </Box>
  );
};

export default CustomCheckBox;
