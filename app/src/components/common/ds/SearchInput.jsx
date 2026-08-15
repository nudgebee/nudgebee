/**
 * SearchInput — search-style input for toolbars (Enter to search, X to clear).
 *
 * Thin wrapper around `ds/Input` so all 15+ consumers automatically pick up
 * the unified field chrome (matches Input / Select / FilterDropdown — 32px
 * sm size, radius-md, gray-300 border, gray-400 hover, blue-500 focus halo).
 *
 * External API preserved verbatim:
 *   - label (used as placeholder)
 *   - value, onChange(newValue)
 *   - onEnterPress() — fires on Enter key
 *   - onClear() — fires when X is clicked, and is then the ONLY callback fired
 *     for that click: it owns both resetting `value` and re-running the filter.
 *     Callsites without an onClear fall back to onChange('').
 *   - disabled, id, sx, ml, mr, minWidth, maxWidth
 */
import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { searchSvg } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { Input } from '@ui/Input';
import { ds } from '@utils/colors';

const SearchInput = ({
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
    // The X is a single "clear" intent, so it fires a single callback.
    //
    // onClear owns the whole reset (value + applied filter + any refetch or
    // router update); every callsite that passes it already clears its own
    // value there. Firing onChange('') alongside it made consumers whose
    // onChange *also* treats "went from non-empty to empty" as a clear — the
    // Events message-search filters — run their clear twice, which pushed the
    // same route twice in one tick. The second push cancels the first, so
    // Next.js rejects the in-flight navigation ("Cancel rendering route") and
    // the URL can be left still carrying ?messageSearch.
    //
    // Without an onClear we still fall back to onChange('') so value-only
    // callsites keep clearing. We deliberately do NOT auto-fire onEnterPress
    // here — it would read the pre-clear value from a stale closure.
    if (onClear) {
      onClear();
      return;
    }
    if (onChange) onChange('');
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

SearchInput.propTypes = {
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

export default SearchInput;
