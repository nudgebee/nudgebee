/**
 * Autocomplete — DS V2 of legacy CustomAutocomplete + AutoCompleteInput.
 * Spec:        app/design-system/primitives/forms/autocomplete.html
 * Variants:    size = 'sm' | 'md'
 *              multi = false | true
 *              create = false | true       (allow free-text values not in options)
 *              async = false | true        (fetch options on each keystroke)
 *
 * Migration:   `import CustomAutocomplete from '@common/CustomAutocomplete'`
 *              `import AutoCompleteInput from '@common/inputs/AutoCompleteInput'`
 *           →  `import { Autocomplete } from '@components1/ds/Autocomplete'`
 *
 * Don't (per spec):
 *   - Don't enable `create` for fields with a strict schema (cluster IDs, account IDs).
 *   - Don't show the dropdown until the user has typed one character.
 *   - Don't include the current input as an option (e.g. "san" → option "san").
 */
import * as React from 'react';
import {
  Autocomplete as MuiAutocomplete,
  Chip,
  CircularProgress,
  TextField,
  Typography,
  createFilterOptions,
  type AutocompleteRenderInputParams,
} from '@mui/material';

export type AutocompleteSize = 'sm' | 'md';

export interface AutocompleteOption {
  value: string;
  label: string;
  /** Optional group key. Pair with `groupBy` (G3) to render section headers. */
  group?: string;
  /** Internal flag for the "Create new" entry; never set this manually. */
  __create?: boolean;
  /** Caller-defined extra fields (e.g. `type`, `icon`) consumed in `renderOption`. */
  [key: string]: unknown;
}

export type AutocompleteOptionLike = string | AutocompleteOption;

interface AutocompleteCommonProps {
  label?: string;
  helpText?: string;
  options: AutocompleteOptionLike[];
  size?: AutocompleteSize;
  /** Allow free-text values not in `options`. Disabled by default. */
  create?: boolean;
  /** Mark options as remotely-fetched. Pair with `onSearch` (debounced upstream). */
  async?: boolean;
  /** Async query callback. Required when `async` is true. */
  onSearch?: (query: string) => void;
  /**
   * Fires on every input keystroke (regardless of `async`). Use for live
   * side-effects keyed off the typed text — e.g. debounced node-search
   * highlights in KnowledgeGraph. Distinct from `onSearch` (which is the
   * async-fetch hook) and `onSubmit` (which is the Enter-confirm hook).
   */
  onInputChange?: (value: string) => void;
  loading?: boolean;
  disabled?: boolean;
  required?: boolean;
  placeholder?: string;
  minWidth?: string;
  maxWidth?: string;
  id?: string;
  /** Render a custom option row */
  renderOption?: (opt: AutocompleteOption) => React.ReactNode;
  /**
   * G3 — render grouped sections in the dropdown. If a function is given it
   * derives the group key per option; if `true`, uses `option.group`.
   */
  groupBy?: true | ((opt: AutocompleteOption) => string);
  /**
   * G5 — fires when the user presses Enter on the input with no option
   * highlighted in the dropdown. Receives the raw trimmed input string.
   * Use for search-style flows (e.g. knowledge-graph node spotlight, tag
   * entry) where Enter should submit raw text. Independent of `create` —
   * pair with `create: true` only if you also want a "+ Create" item.
   */
  onSubmit?: (value: string) => void;
}

export type AutocompleteProps =
  | (AutocompleteCommonProps & { multi?: false; value: string | null; onChange: (next: string | null) => void })
  | (AutocompleteCommonProps & {
      multi: true;
      value: string[];
      onChange: (next: string[]) => void;
      /**
       * G4 — cap visible chips and collapse the rest behind a "+N" badge.
       * Default: render all chips. Useful for narrow form rows (CreateAgent).
       */
      visibleChipCount?: number;
    });

const isObj = (o: AutocompleteOptionLike): o is AutocompleteOption => typeof o === 'object' && o !== null;

function toOption(o: AutocompleteOptionLike): AutocompleteOption {
  return isObj(o) ? o : { value: String(o), label: String(o) };
}

const filterFactory = createFilterOptions<AutocompleteOption>();

const SIZE_HEIGHT: Record<AutocompleteSize, string> = { sm: '28px', md: '36px' };

export function Autocomplete(props: AutocompleteProps) {
  const {
    label,
    helpText,
    options,
    size = 'md',
    create = false,
    async: isAsync = false,
    onSearch,
    loading = false,
    disabled = false,
    required = false,
    placeholder,
    minWidth = '180px',
    maxWidth = '480px',
    id,
    renderOption,
    groupBy,
    onSubmit,
    onInputChange,
  } = props;

  const normalized = React.useMemo(() => options.map(toOption), [options]);

  // G3 — translate the public `groupBy` shape into MUI's groupBy callback.
  const groupByFn = React.useMemo<((o: AutocompleteOption) => string) | undefined>(() => {
    if (!groupBy) return undefined;
    if (groupBy === true) return (o) => o.group ?? '';
    return groupBy;
  }, [groupBy]);

  // G5 — fire onSubmit when the user presses Enter on a non-empty input
  // with no option highlighted in the dropdown. Independent of `create`:
  // some search-style flows (KnowledgeGraph node spotlight) want Enter to
  // submit raw text without showing a "+ Create" item. We intercept before
  // MUI's default Enter handler so we don't compete with option selection.
  const handleKeyDown = React.useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (!onSubmit || e.key !== 'Enter') return;
      const input = e.target as HTMLInputElement;
      const text = input?.value?.trim?.() ?? '';
      if (text === '') return;
      // MUI exposes the highlighted option via aria-activedescendant. If
      // something is highlighted, defer to MUI's selection logic.
      const hasHighlight = !!input.getAttribute?.('aria-activedescendant');
      if (hasHighlight) return;
      e.preventDefault();
      e.stopPropagation();
      onSubmit(text);
    },
    [onSubmit]
  );

  const handleInputChange = (_event: React.SyntheticEvent, inputValue: string, reason: 'input' | 'reset' | 'clear') => {
    if (reason === 'input') {
      if (isAsync && onSearch) onSearch(inputValue);
      onInputChange?.(inputValue);
    }
  };

  const filterOptions = (
    opts: AutocompleteOption[],
    state: { inputValue: string; getOptionLabel: (o: AutocompleteOption) => string }
  ): AutocompleteOption[] => {
    // Server-side filtering when async; client-side filtering otherwise.
    // Gap G2 (resolved 2026-05-07): the `create` option is appended in BOTH modes.
    const filtered = isAsync ? opts : filterFactory(opts, state);
    if (create && state.inputValue !== '') {
      const exists = filtered.some((o) => o.label === state.inputValue || o.value === state.inputValue);
      if (!exists) {
        filtered.push({
          value: state.inputValue,
          label: `+ Create "${state.inputValue}" as new`,
          __create: true,
        });
      }
    }
    return filtered;
  };

  const renderInput = (params: AutocompleteRenderInputParams) => (
    <TextField
      {...params}
      label={label}
      placeholder={placeholder}
      required={required}
      sx={{
        '& .MuiOutlinedInput-root': {
          minHeight: SIZE_HEIGHT[size],
          fontSize: 'var(--ds-text-body)',
          backgroundColor: 'var(--ds-background-100)',
          borderRadius: 'var(--ds-radius-sm)',
        },
        '& .MuiOutlinedInput-notchedOutline': {
          borderColor: 'var(--ds-gray-300)',
        },
        '&:hover .MuiOutlinedInput-notchedOutline': {
          borderColor: 'var(--ds-gray-400)',
        },
        '& .Mui-focused .MuiOutlinedInput-notchedOutline': {
          borderColor: 'var(--ds-blue-500)',
          borderWidth: '2px',
        },
        '& .MuiInputLabel-root': {
          fontSize: 'var(--ds-text-small)',
          color: 'var(--ds-gray-600)',
        },
      }}
      InputProps={{
        ...params.InputProps,
        endAdornment: (
          <>
            {loading && <CircularProgress size={14} sx={{ color: 'var(--ds-blue-500)' }} />}
            {params.InputProps.endAdornment}
          </>
        ),
      }}
    />
  );

  const sharedSx = { width: '100%', minWidth, maxWidth };

  if (props.multi) {
    const visibleChipCount = props.visibleChipCount;
    return (
      <>
        <MuiAutocomplete<AutocompleteOption, true, false, false>
          multiple
          id={id}
          disabled={disabled}
          loading={loading}
          options={normalized}
          value={props.value.map((v) => normalized.find((o) => o.value === v) ?? toOption(v))}
          onChange={(_e, next) => props.onChange(next.map((o) => o.value))}
          onInputChange={handleInputChange}
          onKeyDown={handleKeyDown}
          groupBy={groupByFn}
          getOptionLabel={(o) => o.label}
          isOptionEqualToValue={(a, b) => a.value === b.value}
          filterOptions={filterOptions}
          renderInput={renderInput}
          renderOption={renderOption ? (renderProps, opt) => <li {...renderProps}>{renderOption(opt)}</li> : undefined}
          renderTags={
            visibleChipCount && visibleChipCount > 0
              ? (selected, getTagProps) => {
                  // G4 — show the first N chips, collapse the rest behind a "+N" badge.
                  const shown = selected.slice(0, visibleChipCount);
                  const hidden = selected.length - shown.length;
                  return (
                    <>
                      {shown.map((opt, idx) => (
                        <Chip {...getTagProps({ index: idx })} key={opt.value} size='small' label={opt.label} />
                      ))}
                      {hidden > 0 && (
                        <Chip
                          size='small'
                          label={`+${hidden}`}
                          sx={{
                            backgroundColor: 'var(--ds-gray-200)',
                            color: 'var(--ds-gray-700)',
                            fontWeight: 'var(--ds-font-weight-medium)',
                          }}
                        />
                      )}
                    </>
                  );
                }
              : undefined
          }
          sx={sharedSx}
        />
        {helpText && <Typography sx={{ mt: 0.5, fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{helpText}</Typography>}
      </>
    );
  }

  const singleValue = props.value === null ? null : normalized.find((o) => o.value === props.value) ?? toOption(props.value);

  return (
    <>
      <MuiAutocomplete<AutocompleteOption, false, false, false>
        id={id}
        disabled={disabled}
        loading={loading}
        options={normalized}
        value={singleValue}
        onChange={(_e, next) => props.onChange(next ? next.value : null)}
        onInputChange={handleInputChange}
        onKeyDown={handleKeyDown}
        groupBy={groupByFn}
        getOptionLabel={(o) => (typeof o === 'string' ? o : o.label)}
        isOptionEqualToValue={(a, b) => a.value === b.value}
        filterOptions={filterOptions}
        renderInput={renderInput}
        renderOption={renderOption ? (renderProps, opt) => <li {...renderProps}>{renderOption(opt)}</li> : undefined}
        sx={sharedSx}
      />
      {helpText && <Typography sx={{ mt: 0.5, fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{helpText}</Typography>}
    </>
  );
}

export default Autocomplete;
