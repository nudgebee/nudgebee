/**
 * CustomSearch — search-style input for toolbars (Enter to search, X to clear).
 *
 * Thin wrapper around `ds/Input` so all 15+ consumers automatically pick up
 * the unified field chrome (matches Input / Select / FilterDropdown — 32px
 * sm size, radius-md, gray-300 border, gray-400 hover, blue-500 focus halo).
 *
 * External API preserved verbatim:
 *   - label (used as placeholder)
 *   - value, onChange(newValue)
 *   - onEnterPress() — fires on Enter key
 *   - onClear() — fires when X is clicked. Also re-fires onEnterPress so the
 *     parent's filter re-runs with the empty value (matches the original behavior).
 *   - disabled, id, sx, ml, mr, minWidth, maxWidth
 */
import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { searchSvg } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { Input } from '@components1/ds/Input';
import { ds } from '@utils/colors';

const CustomSearch = ({
  label = '',
  minWidth = ds.space.mul(0, 110),
  maxWidth = ds.space.mul(0, 130),
  ml,
  mr,
  onChange,
  onEnterPress,
  sx,
  value,
  id,
  onClear,
  disabled = false,
}) => {
  const handleChange = (next) => {
    if (onChange) onChange(next);
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && onEnterPress) onEnterPress();
  };

  const handleClear = () => {
    // Notify parent that the value is cleared AND that the user intended to
    // clear via the X. The parent's onClear handler is responsible for any
    // filter re-run (most callsites already do this via router.push or a
    // refetch in onClear). We deliberately do NOT auto-fire onEnterPress
    // here — the original implementation did, but that races with onClear's
    // own navigation and causes Next.js "Cancel rendering route" errors when
    // both update the URL.
    if (onChange) onChange('');
    if (onClear) onClear();
  };

  return (
    <Box
      sx={{
        minWidth,
        maxWidth,
        ml,
        mr,
        // Set fontFamily directly on the <input> element — wrapper-level
        // fontFamily doesn't reach <input> because browsers apply a
        // higher-specificity user-agent rule that overrides inheritance.
        // DS tokens; placeholder is gray-400 so it reads as a soft
        // suggestion, not a value.
        '& input': {
          fontFamily: 'var(--ds-font-sans)',
          fontSize: 'var(--ds-text-small)',
        },
        '& input::placeholder': {
          fontFamily: 'var(--ds-font-sans)',
          fontSize: 'var(--ds-text-small)',
          color: 'var(--ds-gray-400)',
          opacity: 1,
          fontWeight: 'var(--ds-font-weight-regular)',
        },
        ...sx,
      }}
    >
      <Input
        id={id}
        size='sm'
        placeholder={label}
        value={value ?? ''}
        disabled={disabled}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        leadingIcon={<SafeIcon src={searchSvg} alt='search' height={16} width={16} />}
        trailingIcon={
          value ? (
            <CloseIcon aria-label='clear search' sx={{ fontSize: 16, cursor: 'pointer', pointerEvents: 'auto' }} onClick={handleClear} />
          ) : undefined
        }
      />
    </Box>
  );
};

CustomSearch.propTypes = {
  label: PropTypes.string,
  minWidth: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  maxWidth: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  ml: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  mr: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  onChange: PropTypes.func,
  onEnterPress: PropTypes.func,
  sx: PropTypes.object,
  value: PropTypes.string,
  id: PropTypes.string,
  onClear: PropTypes.func,
  disabled: PropTypes.bool,
};

export default CustomSearch;
