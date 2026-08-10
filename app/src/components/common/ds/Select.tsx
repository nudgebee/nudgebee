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
 * `grouped` renders options under collapsible section headers keyed by each
 * option's `group` field — same contract and behavior as ds/FilterDropdown's
 * grouped mode (`group` + `groupIcon(rawGroup)`, snakeToTitleCase'd header
 * text, groups collapsed by default, auto-expanded while searching,
 * selection-count badge on the header, auto-collapse to a flat list when the
 * full options list spans only one group). In `multiple` mode the
 * selected-first ordering applies WITHIN each group (selected rows above a
 * divider inside their own section, mirroring FilterDropdown) instead of the
 * flat list's global selected-first split, so options never leave their
 * group. Unlike FilterDropdown there are no per-group Clear / Select All
 * actions — the trigger-level clear (✕) and the list-level select-all row
 * already cover both modes.
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
import { snakeToTitleCase } from '@utils/common';
import Tooltip from '@ui/Tooltip';
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
  /** Section this option renders under when `grouped` is set. Missing ⇒ 'Other'. */
  group?: string;
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
  /**
   * Render options under collapsible section headers keyed by each option's
   * `group` field. Groups start collapsed and auto-expand while searching,
   * mirroring FilterDropdown's grouped mode; in `multiple` mode selected
   * rows sort first within their own group (never globally). Auto-collapses
   * to the flat list when the full options list spans only one group, where
   * headers would be noise.
   */
  grouped?: boolean;
  /** Renders beside each group header. Receives the raw (un-normalized) `group` value. */
  groupIcon?: (group: string) => React.ReactNode;
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
  /**
   * Hide the per-option (and select-all) checkboxes in the popup. Default
   * `false`. Use for add-only multi pickers — where picks are surfaced
   * elsewhere (e.g. a table) and the popup options are filtered down to the
   * not-yet-selected set, so a checkbox would always render unchecked and read
   * as noise. Clicking a row still toggles selection.
   */
  hideOptionCheckbox?: boolean;
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

function SelectedLabel({ label }: { label: string }) {
  const [isOverflowing, setIsOverflowing] = React.useState(false);

  return (
    <Tooltip title={isOverflowing ? label : ''} placement='top'>
      <Box
        component='span'
        onMouseEnter={(e) => {
          const el = e.currentTarget;
          setIsOverflowing(el.scrollWidth > el.clientWidth);
        }}
        sx={{
          color: 'var(--ds-blue-500)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          maxWidth: ds.space.mul(0, 50),
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          display: 'inline-block',
          verticalAlign: 'middle',
          whiteSpace: 'nowrap',
        }}
      >
        {label}
      </Box>
    </Tooltip>
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
    grouped = false,
    groupIcon,
    className,
    id,
    name,
    disablePortal = true,
  } = props;

  const options = React.useMemo(() => rawOptions.map(normalizeOption), [rawOptions]);
  // O(1) value→option lookup; loop preserves first-match-wins for duplicate values
  const optionsByValue = React.useMemo(() => {
    const map = new Map<string, SelectOption>();
    for (const o of options) {
      if (!map.has(o.value)) {
        map.set(o.value, o);
      }
    }
    return map;
  }, [options]);
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
  const [openGroups, setOpenGroups] = React.useState<Record<string, boolean>>({});
  const open = Boolean(anchorEl);

  const toggleGroup = (header: string) => setOpenGroups((prev) => ({ ...prev, [header]: !prev[header] }));

  // Auto-show search when there are many options; `searchable` prop overrides.
  const showSearch = searchable ?? options.length > 8;

  // Reset search + group expansion whenever the popup closes, so each open
  // starts from the same collapsed-groups baseline (FilterDropdown's grouped
  // list gets this for free by unmounting its popup content).
  React.useEffect(() => {
    if (!open) {
      setSearch('');
      setOpenGroups({});
    }
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

  // Group headers only render when the full options list actually spans more
  // than one group — with a single group (e.g. a tenant that has only K8s
  // accounts) headers add noise, so we fall back to the flat list. Computed
  // over `options` (not the search-filtered list) so a search that narrows to
  // one group doesn't make the headers flicker mid-typing.
  const effectiveGrouped = React.useMemo(() => {
    if (!grouped) return false;
    const distinctGroups = new Set(options.map((o) => snakeToTitleCase(o.group || 'Other')));
    return distinctGroups.size > 1;
  }, [grouped, options]);

  // Ordered [headerText, {rawGroup, opts}] tuples over the filtered list.
  // Insertion order — callers control group order via their options order,
  // matching FilterDropdown's grouped mode.
  const groupedFilteredOptions = React.useMemo(() => {
    if (!effectiveGrouped) return null;
    const map = new Map<string, { rawGroup: string; opts: SelectOption[] }>();
    for (const o of filteredOptions) {
      const rawGroup = o.group || 'Other';
      const header = snakeToTitleCase(rawGroup);
      if (!map.has(header)) map.set(header, { rawGroup, opts: [] });
      map.get(header)!.opts.push(o);
    }
    return Array.from(map.entries());
  }, [effectiveGrouped, filteredOptions]);

  // Set for O(1) membership checks in multi-select mode
  const selectedValueSet = React.useMemo(() => (props.multiple ? new Set(props.value) : null), [props.value, props.multiple]);

  const isSelected = (optValue: string): boolean => {
    if (props.multiple) return selectedValueSet!.has(optValue);
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

  // Multi mode renders a checkbox per option (+ select-all) unless the caller
  // opts out for an add-only picker.
  const showOptionCheckbox = props.multiple && !props.hideOptionCheckbox;

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
      const opt = optionsByValue.get(props.value as string);
      return <>{opt?.label ?? props.value}</>;
    }

    // Multi-mode: render up to maxChips labels then '+N' for the rest
    const maxChips = props.maxChips ?? 2;
    const selectedOpts = props.value.map((v) => optionsByValue.get(v)).filter((o): o is SelectOption => Boolean(o));
    const visible = selectedOpts.slice(0, maxChips);
    const hidden = selectedOpts.length - visible.length;
    return (
      <>
        {visible.map((opt, i) => (
          <React.Fragment key={opt.value}>
            {i > 0 && (
              <Box component='span' sx={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-regular)', mr: ds.space[1] }}>
                ,
              </Box>
            )}
            <SelectedLabel label={typeof opt.label === 'string' ? opt.label : String(opt.value)} />
          </React.Fragment>
        ))}
        {hidden > 0 && (
          <Tooltip
            variant='interactive'
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
                ml: ds.space[1],
                backgroundColor: 'var(--ds-gray-100)',
                color: 'var(--ds-gray-700)',
                border: '1px solid var(--ds-gray-200)',
                borderRadius: ds.radius.sm,
                padding: `0 ${ds.space[1]}`,
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-medium)',
                lineHeight: ds.space[4],
                display: 'inline-block',
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
          {!loading && showOptionCheckbox && filteredOptions.length > 0 && (
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
            <OverlayLoadingSkeleton size='md' showCheckbox={showOptionCheckbox} />
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
          ) : effectiveGrouped && groupedFilteredOptions ? (
            // Grouped mode: collapsible section per group, collapsed by
            // default — mirrors FilterDropdown's grouped mode. While
            // searching, a group that survived filtering matched at least one
            // of its options, so it auto-expands to keep the match visible. A
            // count badge marks groups holding selections so they stay
            // findable while collapsed. In multi mode the selected-first
            // ordering applies within each group — never globally — so rows
            // don't leave their section.
            groupedFilteredOptions.map(([header, { rawGroup, opts }]) => {
              const isGroupOpen = search.trim().length > 0 || !!openGroups[header];
              const selectedInGroup = opts.filter((opt) => isSelected(opt.value));
              const unselectedInGroup = opts.filter((opt) => !isSelected(opt.value));
              const orderedOpts = props.multiple ? [...selectedInGroup, ...unselectedInGroup] : opts;
              return (
                <React.Fragment key={header}>
                  <Box
                    role='button'
                    tabIndex={0}
                    onClick={() => toggleGroup(header)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        toggleGroup(header);
                      }
                    }}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      padding: 'var(--ds-overlay-item-padding-md)',
                      margin: '0 var(--ds-overlay-item-margin-x)',
                      borderRadius: 'var(--ds-overlay-item-radius)',
                      height: '36px',
                      boxSizing: 'border-box',
                      position: 'sticky',
                      top: 0,
                      zIndex: 10,
                      backgroundColor: 'var(--ds-background-100)',
                      cursor: 'pointer',
                      '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
                    }}
                  >
                    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space.mul(0, 3), minWidth: 0 }}>
                      {groupIcon && groupIcon(rawGroup)}
                      <Box
                        component='span'
                        sx={{
                          fontSize: 'var(--ds-text-caption)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: 'var(--ds-gray-700)',
                          textTransform: 'uppercase',
                          letterSpacing: '0.02em',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {header}
                      </Box>
                      {selectedInGroup.length > 0 && (
                        <Box
                          component='span'
                          sx={{
                            backgroundColor: 'var(--ds-blue-600)',
                            color: 'var(--ds-background-100)',
                            borderRadius: ds.radius.sm,
                            minWidth: ds.space.mul(0, 5),
                            height: ds.space.mul(0, 9),
                            padding: `0 ${ds.space[1]}`,
                            fontSize: 'var(--ds-text-caption)',
                            fontWeight: 'var(--ds-font-weight-semibold)',
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            flexShrink: 0,
                          }}
                        >
                          {selectedInGroup.length}
                        </Box>
                      )}
                    </Box>
                    <Chevron open={isGroupOpen} />
                  </Box>
                  {isGroupOpen && (
                    <Box sx={{ paddingLeft: ds.space[3] }}>
                      {orderedOpts.map((opt, idx) => (
                        <React.Fragment key={`${header}-${opt.value}`}>
                          {/* Divider between the group's selected block and its unselected rest (multi mode) */}
                          {props.multiple && selectedInGroup.length > 0 && unselectedInGroup.length > 0 && idx === selectedInGroup.length && (
                            <Box sx={{ borderBottom: '0.5px solid var(--ds-gray-200)', margin: '4px 10px' }} />
                          )}
                          <OverlayItem
                            size='md'
                            selected={isSelected(opt.value)}
                            disabled={opt.disabled}
                            icon={showOptionCheckbox ? <OverlayCheckbox checked={isSelected(opt.value)} /> : opt.icon}
                            onClick={() => handleItemClick(opt)}
                          >
                            {opt.label ?? opt.value}
                          </OverlayItem>
                        </React.Fragment>
                      ))}
                    </Box>
                  )}
                </React.Fragment>
              );
            })
          ) : (
            <>
              {/* Selected items first (multi mode only — single keeps natural order) */}
              {selectedOpts.map((opt) => (
                <OverlayItem
                  key={`sel-${opt.value}`}
                  size='md'
                  selected
                  disabled={opt.disabled}
                  icon={showOptionCheckbox ? <OverlayCheckbox checked /> : opt.icon}
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
                  icon={showOptionCheckbox ? <OverlayCheckbox checked={isSelected(opt.value)} /> : opt.icon}
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
