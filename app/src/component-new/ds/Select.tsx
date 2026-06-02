/**
 * Select — unified single + multi value picker (form-field flavor).
 * Spec: design-system/primitives/forms/select.html
 *
 * Successor to ds/Select (single-only stub) AND ds/MultiSelect (broken).
 * For toolbar / filter use cases use ds/FilterDropdown — that's a different
 * affordance (filter pill trigger, "+N" badge, blue-when-active).
 *
 * A clear (✕) button sits inline to the left of the chevron whenever there's a
 * selection, resetting the value (single → '', multi → []). The chevron stays
 * put so the open affordance and trigger layout don't shift. Suppressed on
 * `required` fields — clearing would leave a required field with no value.
 *
 * Composes the shared overlay primitives (`OverlaySurface`, `OverlayItem`)
 * so the popup is byte-identical to DropdownMenu / FilterDropdown / Popover.
 * The trigger is field-shaped (matches the chrome of ds/Input — same heights,
 * border, focus halo, label/help/error slots) so Select rows align with Input
 * rows in a form column.
 *
 * Single vs multi is a discriminated union on `multiple`:
 *   <Select value={s}     onChange={(v) => …} options={…} />          ← single
 *   <Select multiple value={[…]} onChange={(arr) => …} options={…} /> ← multi
 *
 * The `required` asterisk renders only alongside a non-empty `label`; when the
 * label is absent (undefined / null / empty / whitespace) the asterisk is
 * suppressed — it has nothing to attach to.
 *
 * Don't (per spec):
 *   - Don't use Select with > ~20 options without search — use Autocomplete.
 *   - Don't use Select for actions — use DropdownMenu.
 *   - Don't use Select inside a toolbar filter bar — use FilterDropdown.
 *   - Don't use Select for binary choices — use Switch or ToggleGroup.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';
import Tooltip from '@components1/ds/Tooltip';
import {
  OverlayCheckbox,
  OverlayItem,
  OverlayLoadingSkeleton,
  OverlayScrollBox,
  OverlaySearch,
  OverlaySelectAll,
  OverlaySurface,
} from './internal/Overlay';

export type SelectSize = 'sm' | 'md' | 'lg';

export interface SelectOption {
  value: string;
  label?: React.ReactNode;
  icon?: React.ReactNode;
  disabled?: boolean;
}

/** Options may be plain strings (label and value the same) or SelectOption objects. */
export type SelectOptionLike = string | SelectOption;

interface SelectBaseProps {
  options: SelectOptionLike[];
  label?: React.ReactNode;
  /** Renders between label and trigger. */
  instructionText?: React.ReactNode;
  /** Renders below trigger. Hidden when `error` is set. */
  help?: React.ReactNode;
  /** Presence ⇒ error state. */
  error?: string;
  placeholder?: string;
  required?: boolean;
  /**
   * Show the inline clear (✕) button (and, in multi mode, the popup "Clear all"
   * action) when there's a selection. Default `true`. Set `false` for pickers
   * where an empty value is not a meaningful state (e.g. a navigation selector
   * that must always have an active choice). `required` also suppresses clear.
   */
  clearable?: boolean;
  disabled?: boolean;
  size?: SelectSize;
  /** Min-width of the trigger AND the popup. Popup matches trigger width by default. */
  minWidth?: string | number;
  /**
   * Show a search input above the option list. Defaults to `true` when there
   * are more than 8 options, `false` otherwise. Pass explicit `true`/`false`
   * to override.
   */
  searchable?: boolean;
  /** Placeholder for the search input. Default `'Search…'`. */
  searchPlaceholder?: string;
  /** Show a skeleton placeholder list in the popup while options load. */
  loading?: boolean;
  className?: string;
  id?: string;
  name?: string;
  disablePortal?: boolean;
}

export interface SelectSingleProps extends SelectBaseProps {
  multiple?: false;
  value: string | null;
  onChange: (next: string) => void;
}

export interface SelectMultipleProps extends SelectBaseProps {
  multiple: true;
  value: string[];
  onChange: (next: string[]) => void;
  /** Max labels shown inline before collapsing to '+N'. Default 2. */
  maxChips?: number;
}

export type SelectProps = SelectSingleProps | SelectMultipleProps;

// Mirrors ds/Input's SIZE_TOKENS so a Select row sits next to an Input row at
// the same height with the same label scale.
const SIZE_TOKENS: Record<SelectSize, { height: string; fontSize: string; labelFontSize: string; padX: string; labelGap: string }> = {
  sm: { height: '32px', fontSize: 'var(--ds-text-body)', labelFontSize: 'var(--ds-text-small)', padX: 'var(--ds-space-3)', labelGap: '6px' },
  md: { height: '36px', fontSize: 'var(--ds-text-body)', labelFontSize: 'var(--ds-text-small)', padX: 'var(--ds-space-3)', labelGap: '6px' },
  lg: { height: '40px', fontSize: 'var(--ds-text-body)', labelFontSize: 'var(--ds-text-body)', padX: 'var(--ds-space-4)', labelGap: '6px' },
};

function normalizeOption(o: SelectOptionLike): SelectOption {
  if (typeof o === 'string') return { value: o, label: o };
  return o;
}

function Chevron({ open }: { open: boolean }) {
  // Raw <svg> — using <Box component='svg' width='12'> makes MUI treat width as
  // a CSS sx value, not an SVG viewport attribute, and the icon balloons to
  // the browser's default 300x150 SVG size.
  return (
    <svg
      width='12'
      height='12'
      viewBox='0 0 10 10'
      fill='none'
      style={{
        flexShrink: 0,
        color: 'var(--ds-gray-500)',
        transition: 'transform 0.2s ease',
        transform: open ? 'rotate(180deg)' : 'rotate(0deg)',
      }}
    >
      <path d='M2 3.5L5 6.5L8 3.5' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
    </svg>
  );
}

// Clear (cross) — shown inline to the left of the chevron whenever there's a
// selection, so the value can always be reset. The chevron is kept (unlike
// FilterDropdown, which swaps chevron→cross) so the open affordance never
// disappears and the field-shaped trigger keeps a stable layout. Rendered as a
// focusable <span role='button'> nested in the trigger <button>; onClick stops
// propagation so clearing never also opens the popup.
function ClearButton({ onClear, label }: { onClear: (e: React.SyntheticEvent) => void; label: string }) {
  return (
    <Box
      component='span'
      role='button'
      tabIndex={0}
      aria-label={label}
      data-testid='select-clear-btn'
      onClick={onClear}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClear(e);
        }
      }}
      sx={{
        flexShrink: 0,
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        color: 'var(--ds-gray-400)',
        cursor: 'pointer',
        borderRadius: 'var(--ds-radius-sm)',
        '&:hover': { color: 'var(--ds-gray-600)' },
        '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: '1px' },
      }}
    >
      <svg width='12' height='12' viewBox='0 0 12 12' fill='none'>
        <line x1='3' y1='3' x2='9' y2='9' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
        <line x1='9' y1='3' x2='3' y2='9' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
      </svg>
    </Box>
  );
}

export function Select(props: SelectProps) {
  const {
    options: rawOptions,
    label,
    instructionText,
    help,
    error,
    placeholder = 'Select…',
    required,
    clearable = true,
    disabled,
    size = 'md',
    minWidth,
    searchable,
    searchPlaceholder = 'Search…',
    loading = false,
    className,
    id,
    name,
    disablePortal = true,
  } = props;

  const options = React.useMemo(() => rawOptions.map(normalizeOption), [rawOptions]);
  const tokens = SIZE_TOKENS[size];
  const reactId = React.useId();
  const inputId = id ?? reactId;
  const hasError = typeof error === 'string' && error.length > 0;
  const helpId = `${inputId}-help`;
  const errorId = `${inputId}-error`;
  const instrId = `${inputId}-instr`;

  const [anchorEl, setAnchorEl] = React.useState<HTMLElement | null>(null);
  const [popupWidth, setPopupWidth] = React.useState<number | undefined>();
  const [search, setSearch] = React.useState('');
  const open = Boolean(anchorEl);

  // Auto-show search when there are many options; `searchable` prop overrides.
  const showSearch = searchable ?? options.length > 8;

  // Reset search whenever the popup closes.
  React.useEffect(() => {
    if (!open) setSearch('');
  }, [open]);

  // Case-insensitive substring match on the option's label (or value).
  const filteredOptions = React.useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return options;
    return options.filter((o) => {
      const labelStr = typeof o.label === 'string' ? o.label : String(o.value);
      return labelStr.toLowerCase().includes(q);
    });
  }, [options, search]);

  const isSelected = (optValue: string): boolean => {
    if (props.multiple) return props.value.includes(optValue);
    return props.value === optValue;
  };

  // In multi-mode, split filtered options into selected (rendered first) +
  // unselected so the user can see their picks without scrolling.
  const { selectedOpts, unselectedOpts } = React.useMemo(() => {
    if (!props.multiple) return { selectedOpts: [] as SelectOption[], unselectedOpts: filteredOptions };
    const sel: SelectOption[] = [];
    const unsel: SelectOption[] = [];
    filteredOptions.forEach((o) => (isSelected(o.value) ? sel.push(o) : unsel.push(o)));
    return { selectedOpts: sel, unselectedOpts: unsel };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filteredOptions, props.multiple, props.multiple ? props.value : null]);

  const allFilteredSelected = !!props.multiple && filteredOptions.length > 0 && filteredOptions.every((o) => isSelected(o.value));

  const handleItemClick = (opt: SelectOption) => {
    if (opt.disabled) return;
    if (props.multiple) {
      const current = props.value;
      const next = current.includes(opt.value) ? current.filter((v) => v !== opt.value) : [...current, opt.value];
      props.onChange(next);
      // Multi: stay open so user can pick several
    } else {
      props.onChange(opt.value);
      setAnchorEl(null);
    }
  };

  const handleSelectAll = () => {
    if (!props.multiple) return;
    const filteredVals = filteredOptions.filter((o) => !o.disabled).map((o) => o.value);
    const merged = Array.from(new Set([...props.value, ...filteredVals]));
    props.onChange(merged);
  };

  const handleClearAll = () => {
    if (!props.multiple) return;
    // Only clear visible (filtered) values — matches FilterDropdown
    const filteredVals = new Set(filteredOptions.map((o) => o.value));
    props.onChange(props.value.filter((v) => !filteredVals.has(v)));
  };

  const handleOpen = (e: React.MouseEvent<HTMLButtonElement>) => {
    if (disabled) return;
    setPopupWidth(e.currentTarget.offsetWidth);
    setAnchorEl(e.currentTarget);
  };

  // Reset to "no selection". Single mode emits '' rather than null: onChange is
  // typed (next: string) => void and widening to string|null would break every
  // existing typed caller — '' already reads as no-selection (see hasSelection).
  const handleClear = (e: React.SyntheticEvent) => {
    e.stopPropagation();
    if (disabled) return;
    if (props.multiple) props.onChange([]);
    else props.onChange('');
  };

  const hasSelection = props.multiple ? props.value.length > 0 : props.value != null && props.value !== '';

  const triggerContent = (() => {
    if (!hasSelection)
      return (
        <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
          {placeholder}
        </Box>
      );

    if (!props.multiple) {
      const opt = options.find((o) => o.value === props.value);
      return <>{opt?.label ?? props.value}</>;
    }

    // Multi-mode: render up to maxChips labels then '+N' for the rest
    const maxChips = props.maxChips ?? 2;
    const selectedOpts = props.value.map((v) => options.find((o) => o.value === v)).filter((o): o is SelectOption => Boolean(o));
    const visible = selectedOpts.slice(0, maxChips);
    const hidden = selectedOpts.length - visible.length;
    return (
      <>
        {visible.map((opt, i) => (
          <React.Fragment key={opt.value}>
            {i > 0 && (
              <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
                ,{' '}
              </Box>
            )}
            <span>{opt.label}</span>
          </React.Fragment>
        ))}
        {hidden > 0 && (
          <Tooltip
            title={
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
                {selectedOpts.slice(maxChips).map((o) => (
                  <span key={o.value}>{typeof o.label === 'string' ? o.label : String(o.value)}</span>
                ))}
              </Box>
            }
            placement='top'
          >
            <Box
              component='span'
              sx={{
                marginLeft: ds.space[1],
                padding: '0 5px',
                backgroundColor: 'var(--ds-gray-100)',
                color: 'var(--ds-gray-700)',
                fontSize: 'var(--ds-text-caption)',
                borderRadius: ds.radius.sm,
                fontVariantNumeric: 'tabular-nums',
                flexShrink: 0,
                cursor: 'default',
              }}
            >
              +{hidden}
            </Box>
          </Tooltip>
        )}
      </>
    );
  })();

  const describedBy =
    [hasError ? errorId : null, !hasError && help ? helpId : null, instructionText ? instrId : null].filter(Boolean).join(' ') || undefined;

  // A label counts as "present" only when it has renderable content — an empty
  // string / whitespace / null label is treated as absent, so the `required`
  // asterisk never renders on its own without a visible label to attach to.
  const hasLabel = label !== undefined && label !== null && !(typeof label === 'string' && label.trim() === '');

  return (
    <Box className={className} sx={{ display: 'flex', flexDirection: 'column', gap: tokens.labelGap, width: '100%' }}>
      {hasLabel && (
        <Box
          component='label'
          htmlFor={inputId}
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: tokens.labelFontSize,
            color: 'var(--ds-gray-700)',
            fontWeight: 'var(--ds-font-weight-medium)',
          }}
        >
          {label}
          {required && (
            <Box component='span' aria-hidden='true' sx={{ color: 'var(--ds-red-500)', marginLeft: ds.space[0] }}>
              *
            </Box>
          )}
        </Box>
      )}
      {instructionText !== undefined && (
        <Box component='span' id={instrId} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          {instructionText}
        </Box>
      )}
      <Box
        component='button'
        type='button'
        id={inputId}
        name={name}
        disabled={disabled}
        aria-haspopup='listbox'
        aria-expanded={open}
        aria-invalid={hasError || undefined}
        aria-describedby={describedBy}
        onClick={handleOpen}
        sx={{
          // Field chrome — mirrors ds/Input so Select rows align with Input rows.
          width: '100%',
          minWidth,
          height: tokens.height,
          padding: `0 ${tokens.padX}`,
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
          boxSizing: 'border-box',
          // No fontFamily — value text inherits the body default (Roboto).
          // The label above uses --ds-font-display (Poppins) explicitly.
          fontSize: tokens.fontSize,
          color: 'var(--ds-gray-700)',
          backgroundColor: disabled ? 'var(--ds-background-200)' : 'var(--ds-background-100)',
          border: `1px solid ${hasError ? 'var(--ds-red-500)' : 'var(--ds-gray-300)'}`,
          borderRadius: 'var(--ds-radius-md)',
          outline: 'none',
          cursor: disabled ? 'not-allowed' : 'pointer',
          textAlign: 'left',
          transition: 'border-color 120ms ease, box-shadow 120ms ease, background-color 120ms ease',
          '&:hover': disabled
            ? undefined
            : {
                borderColor: hasError ? 'var(--ds-red-600)' : 'var(--ds-gray-400)',
                backgroundColor: 'var(--ds-background-200)',
              },
          '&:focus-visible': {
            borderColor: hasError ? 'var(--ds-red-500)' : 'var(--ds-blue-500)',
            boxShadow: `0 0 0 3px ${hasError ? 'var(--ds-red-100)' : 'var(--ds-blue-100)'}`,
          },
          ...(open && {
            borderColor: hasError ? 'var(--ds-red-500)' : 'var(--ds-blue-500)',
            boxShadow: `0 0 0 3px ${hasError ? 'var(--ds-red-100)' : 'var(--ds-blue-100)'}`,
          }),
        }}
      >
        <Box
          component='span'
          sx={{
            flex: 1,
            minWidth: 0,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            display: 'flex',
            alignItems: 'center',
          }}
        >
          {triggerContent}
        </Box>
        {/* No clear button on required fields — clearing to empty would leave a
            required field invalid with no value to fall back to. */}
        {clearable && hasSelection && !disabled && !required && <ClearButton onClear={handleClear} label='Clear selection' />}
        <Chevron open={open} />
      </Box>
      {!hasError && help !== undefined && (
        <Box component='span' id={helpId} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          {help}
        </Box>
      )}
      {hasError && (
        <Box component='span' id={errorId} role='alert' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-600)' }}>
          {error}
        </Box>
      )}
      <OverlaySurface
        anchorEl={anchorEl}
        open={open}
        onClose={() => setAnchorEl(null)}
        side='bottom'
        align='start'
        width={popupWidth}
        role='listbox'
        disableAutoFocusItem={showSearch}
        disablePortal={disablePortal}
      >
        {/* Search input — pinned at the top, outside the scroll area, so it
            stays visible while the user scrolls through long option lists. */}
        {showSearch && <OverlaySearch value={search} onChange={setSearch} placeholder={searchPlaceholder} />}

        <OverlayScrollBox>
          {/* Select-all / Clear-all row — multi mode only, hidden while loading */}
          {!loading && props.multiple && filteredOptions.length > 0 && (
            <OverlaySelectAll
              checked={allFilteredSelected}
              onToggle={allFilteredSelected ? handleClearAll : handleSelectAll}
              showClear={clearable && selectedOpts.length > 0}
              onClear={handleClearAll}
            />
          )}

          {/* Loading → skeleton rows; else empty state distinguishes
              "no options at all" from "search returned nothing" */}
          {loading ? (
            <OverlayLoadingSkeleton size='md' showCheckbox={!!props.multiple} />
          ) : filteredOptions.length === 0 ? (
            <Box
              sx={{
                padding: '12px 14px',
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-500)',
                textAlign: 'center',
              }}
            >
              {options.length === 0 ? 'No options available' : 'No results found'}
            </Box>
          ) : (
            <>
              {/* Selected items first (multi mode only — single keeps natural order) */}
              {selectedOpts.map((opt) => (
                <OverlayItem
                  key={`sel-${opt.value}`}
                  size='md'
                  selected
                  disabled={opt.disabled}
                  icon={props.multiple ? <OverlayCheckbox checked /> : opt.icon}
                  onClick={() => handleItemClick(opt)}
                >
                  {opt.label ?? opt.value}
                </OverlayItem>
              ))}
              {props.multiple && selectedOpts.length > 0 && unselectedOpts.length > 0 && (
                <Box sx={{ borderBottom: '0.5px solid var(--ds-gray-200)', margin: '4px 10px' }} />
              )}
              {unselectedOpts.map((opt) => (
                <OverlayItem
                  key={`unsel-${opt.value}`}
                  size='md'
                  selected={isSelected(opt.value)}
                  disabled={opt.disabled}
                  icon={props.multiple ? <OverlayCheckbox checked={isSelected(opt.value)} /> : opt.icon}
                  onClick={() => handleItemClick(opt)}
                >
                  {opt.label ?? opt.value}
                </OverlayItem>
              ))}
            </>
          )}
        </OverlayScrollBox>
      </OverlaySurface>
    </Box>
  );
}

export default Select;
