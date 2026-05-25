/**
 * DateRangePicker — DS V2 of legacy CustomDateTimeRangePicker + CustomDateTimePicker
 *                    + k8s/common/DateTimeRangePicker.
 * Spec: app/design-system/primitives/forms/date-range-picker.html
 *
 * Variants: mode = 'range' | 'single'
 *           precision = 'day' | 'minute' | 'second'
 *           shortcuts = subset of ['5m','15m','1h','6h','24h','7d','30d','custom']
 *
 * Value shape:
 *   mode='range'  → { start: Dayjs, end: Dayjs, shortcut?: ShortcutKey }
 *   mode='single' → Dayjs | null
 *
 * Migration:
 *   `import CustomDateTimeRangePicker from '@common/widgets/CustomDateTimeRangePicker'`
 *   `import CustomDateTimePicker from '@common/widgets/CustomDateTimePicker'`
 *   `import DateTimeRangePicker from '@components1/k8s/common/DateTimeRangePicker'`
 *   →  `import { DateRangePicker } from '@components1/ds/DateRangePicker'`
 *
 *   k8s variant's `(type, date) => void` paired callback rewrites to one onChange call
 *   with the merged { start, end } object.
 *
 * Don't (per spec):
 *   - Don't omit shortcuts. Consistency is muscle memory.
 *   - Don't allow a custom range that exceeds upstream data retention. Validate at pick time.
 */
import * as React from 'react';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
// Cast the MUI X DateTimePicker to a less-strict component type so call sites
// can pass Dayjs values + the `format` prop without TS narrowing the picker's
// internal TDate generic to `unknown`. (Inherited from #30554; surfaced under
// Next 16 + @mui/x-date-pickers v7 stricter typings.)
import { DateTimePicker as DateTimePickerOrig } from '@mui/x-date-pickers/DateTimePicker';
const DateTimePicker = DateTimePickerOrig as React.ComponentType<any>;
import { Box, Button, Popover, Stack, Typography, Divider } from '@mui/material';
import CalendarTodayIcon from '@mui/icons-material/CalendarToday';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import dayjs, { Dayjs } from 'dayjs';

export type DateRangePickerMode = 'range' | 'single';
export type DateRangePickerPrecision = 'day' | 'minute' | 'second';
export type ShortcutKey = '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '30d' | 'custom';

export interface DateRangeValue {
  start: Dayjs;
  end: Dayjs;
  shortcut?: ShortcutKey;
}

/** Epoch-shape value (resolves Gap G7 for V1 callers using `passedSelectedDateTime: { startTime, endTime }`) */
export interface DateRangeEpochValue {
  start: number;
  end: number;
  shortcut?: ShortcutKey;
}

export type DateRangeValueShape = 'dayjs' | 'epoch';

interface DateRangePickerCommon {
  precision?: DateRangePickerPrecision;
  shortcuts?: ShortcutKey[];
  minDate?: Dayjs;
  maxDate?: Dayjs;
  label?: string;
  disabled?: boolean;
  minWidth?: string;
  /** Value/onChange shape. 'dayjs' (default) uses Dayjs objects; 'epoch' uses ms-since-epoch numbers. */
  valueShape?: DateRangeValueShape;
  // ── Form integration (Gap G7 part A) ────────────────────────────────────
  /** Apply error styling to the trigger button */
  error?: boolean;
  /** Helper / error message rendered under the trigger */
  helperText?: React.ReactNode;
  /** Fired on trigger blur; useful for form-touched validation */
  onBlur?: () => void;
  /** Override the precision-derived display format (e.g. 'MM/DD/YYYY hh:mm A') */
  format?: string;
  /** Set to true on form submit to mark the field as required */
  required?: boolean;
  // ── Per-field disable (Subgap G7b) ──────────────────────────────────────
  /** Lock the start date picker independently. Range mode only. */
  disableStart?: boolean;
  /** Lock the end date picker independently. Range mode only. */
  disableEnd?: boolean;
}

export type DateRangePickerProps =
  | (DateRangePickerCommon & {
      mode?: 'range';
      value: DateRangeValue | DateRangeEpochValue | null;
      onChange: (next: DateRangeValue | DateRangeEpochValue) => void;
    })
  | (DateRangePickerCommon & {
      mode: 'single';
      value: Dayjs | number | null;
      onChange: (next: Dayjs | number | null) => void;
    });

// ── Value-shape conversion helpers (Gap G7 part C) ──────────────────────────

function toDayjsRange(v: DateRangeValue | DateRangeEpochValue | null): DateRangeValue | null {
  if (!v) return null;
  const start = typeof v.start === 'number' ? dayjs(v.start) : v.start;
  const end = typeof v.end === 'number' ? dayjs(v.end) : v.end;
  return { start, end, shortcut: v.shortcut };
}

function fromDayjsRange(v: DateRangeValue, shape: DateRangeValueShape): DateRangeValue | DateRangeEpochValue {
  if (shape === 'epoch') {
    return { start: v.start.valueOf(), end: v.end.valueOf(), shortcut: v.shortcut };
  }
  return v;
}

function toDayjsSingle(v: Dayjs | number | null): Dayjs | null {
  if (v === null || v === undefined) return null;
  if (typeof v === 'number') return dayjs(v);
  return v;
}

function fromDayjsSingle(v: Dayjs | null, shape: DateRangeValueShape): Dayjs | number | null {
  if (v === null) return null;
  return shape === 'epoch' ? v.valueOf() : v;
}

const SHORTCUT_LABELS: Record<ShortcutKey, string> = {
  '5m': 'Last 5 min',
  '15m': 'Last 15 min',
  '1h': 'Last 1 hour',
  '6h': 'Last 6 hours',
  '24h': 'Last 24 hours',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  custom: 'Custom',
};

const SHORTCUT_DURATION_MS: Record<Exclude<ShortcutKey, 'custom'>, number> = {
  '5m': 5 * 60 * 1000,
  '15m': 15 * 60 * 1000,
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
  '30d': 30 * 24 * 60 * 60 * 1000,
};

const PRECISION_VIEWS: Record<DateRangePickerPrecision, Array<'year' | 'month' | 'day' | 'hours' | 'minutes' | 'seconds'>> = {
  day: ['year', 'month', 'day'],
  minute: ['year', 'month', 'day', 'hours', 'minutes'],
  second: ['year', 'month', 'day', 'hours', 'minutes', 'seconds'],
};

const PRECISION_FORMAT: Record<DateRangePickerPrecision, string> = {
  day: 'YYYY-MM-DD',
  minute: 'YYYY-MM-DD HH:mm',
  second: 'YYYY-MM-DD HH:mm:ss',
};

function makeTriggerSx(hasError: boolean) {
  return {
    height: '32px',
    px: 'var(--ds-space-3)',
    minWidth: '180px',
    justifyContent: 'flex-start',
    textTransform: 'none' as const,
    borderRadius: 'var(--ds-radius-sm)',
    border: hasError ? '1px solid var(--ds-red-500)' : '1px solid var(--ds-gray-300)',
    backgroundColor: 'var(--ds-background-100)',
    color: 'var(--ds-gray-700)',
    fontSize: 'var(--ds-text-body)',
    fontWeight: 'var(--ds-font-weight-regular)',
    '&:hover': {
      backgroundColor: 'var(--ds-background-100)',
      borderColor: hasError ? 'var(--ds-red-500)' : 'var(--ds-gray-400)',
    },
  };
}

function formatRangeValue(v: DateRangeValue | null, precision: DateRangePickerPrecision, formatOverride?: string): string {
  if (!v) return 'Select range';
  if (v.shortcut && v.shortcut !== 'custom') return SHORTCUT_LABELS[v.shortcut];
  const fmt = formatOverride ?? PRECISION_FORMAT[precision];
  return `${v.start.format(fmt)} → ${v.end.format(fmt)}`;
}

function formatSingleValue(v: Dayjs | null, precision: DateRangePickerPrecision, formatOverride?: string): string {
  if (!v) return 'Select date';
  return v.format(formatOverride ?? PRECISION_FORMAT[precision]);
}

export function DateRangePicker(props: DateRangePickerProps) {
  const {
    precision = 'minute',
    shortcuts = ['5m', '15m', '1h', '6h', '24h', '7d', 'custom'],
    minDate,
    maxDate,
    label,
    disabled = false,
    minWidth,
    valueShape = 'dayjs',
    error = false,
    helperText,
    onBlur,
    format,
    required = false,
    disableStart = false,
    disableEnd = false,
  } = props;
  const mode: DateRangePickerMode = props.mode ?? 'range';

  const [anchorEl, setAnchorEl] = React.useState<HTMLElement | null>(null);
  const [showCustom, setShowCustom] = React.useState(false);
  const open = Boolean(anchorEl);
  const views = PRECISION_VIEWS[precision];

  const close = () => {
    setAnchorEl(null);
    setShowCustom(false);
  };

  const handleShortcut = (key: ShortcutKey) => {
    if (key === 'custom') {
      setShowCustom(true);
      return;
    }
    if (mode === 'single') {
      const next = dayjs();
      (props as Extract<DateRangePickerProps, { mode: 'single' }>).onChange(fromDayjsSingle(next, valueShape));
      close();
      return;
    }
    const end = dayjs();
    const start = end.subtract(SHORTCUT_DURATION_MS[key], 'millisecond');
    const dayjsValue: DateRangeValue = { start, end, shortcut: key };
    (props as Extract<DateRangePickerProps, { mode?: 'range' }>).onChange(fromDayjsRange(dayjsValue, valueShape));
    close();
  };

  // Normalize incoming value to Dayjs for internal use (handles epoch shape)
  const dayjsRangeValue = mode === 'range' ? toDayjsRange(props.value as DateRangeValue | DateRangeEpochValue | null) : null;
  const dayjsSingleValue = mode === 'single' ? toDayjsSingle(props.value as Dayjs | number | null) : null;

  const triggerLabel =
    mode === 'single' ? formatSingleValue(dayjsSingleValue, precision, format) : formatRangeValue(dayjsRangeValue, precision, format);

  return (
    <LocalizationProvider dateAdapter={AdapterDayjs}>
      <Box sx={{ display: 'inline-flex', flexDirection: 'column', minWidth }}>
        {label && (
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: error ? 'var(--ds-red-600)' : 'var(--ds-gray-600)', mb: 0.5 }}>
            {label}
            {required && <span style={{ color: 'var(--ds-red-600)', marginLeft: 4 }}>*</span>}
          </Typography>
        )}
        <Button
          id={props.label ? `${props.label.replace(/\s+/g, '-')}-trigger` : undefined}
          disabled={disabled}
          onClick={(e) => setAnchorEl(e.currentTarget)}
          onBlur={onBlur}
          startIcon={<CalendarTodayIcon sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />}
          endIcon={<KeyboardArrowDownIcon sx={{ fontSize: 18, color: 'var(--ds-gray-600)' }} />}
          sx={makeTriggerSx(error)}
        >
          <Box component='span' sx={{ flex: 1, textAlign: 'left' }}>
            {triggerLabel}
          </Box>
        </Button>
        <Popover
          open={open}
          anchorEl={anchorEl}
          onClose={close}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
          transformOrigin={{ vertical: 'top', horizontal: 'left' }}
          slotProps={{
            paper: {
              sx: {
                mt: 1,
                borderRadius: 'var(--ds-radius-md)',
                boxShadow: '0px 4px 20px 0px var(--ds-gray-alpha-200)',
                p: 'var(--ds-space-3)',
                minWidth: showCustom ? 320 : 200,
              },
            },
          }}
        >
          {!showCustom ? (
            <Stack spacing={0.5}>
              {shortcuts.map((key) => (
                <Button
                  key={key}
                  onClick={() => handleShortcut(key)}
                  sx={{
                    justifyContent: 'flex-start',
                    textTransform: 'none',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: 'var(--ds-gray-700)',
                    px: 'var(--ds-space-3)',
                    py: 'var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                    '&:hover': { backgroundColor: 'var(--ds-blue-100)' },
                  }}
                >
                  {SHORTCUT_LABELS[key]}
                </Button>
              ))}
            </Stack>
          ) : (
            <Stack spacing={2} sx={{ width: '100%' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                {mode === 'single' ? 'Pick a date' : 'Custom range'}
              </Typography>
              {mode === 'single' ? (
                <DateTimePicker
                  value={dayjsSingleValue}
                  onChange={(next: Dayjs | null) => {
                    (props as Extract<DateRangePickerProps, { mode: 'single' }>).onChange(fromDayjsSingle(next, valueShape));
                  }}
                  views={views}
                  format={format ?? PRECISION_FORMAT[precision]}
                  minDate={minDate}
                  maxDate={maxDate}
                />
              ) : (
                <Stack direction='row' spacing={1}>
                  <DateTimePicker
                    label='Start'
                    value={dayjsRangeValue?.start ?? null}
                    disabled={disableStart}
                    onChange={(next: Dayjs | null) => {
                      if (!next) return;
                      const cur = dayjsRangeValue ?? { start: next, end: next };
                      const updated: DateRangeValue = { ...cur, start: next, shortcut: 'custom' };
                      (props as Extract<DateRangePickerProps, { mode?: 'range' }>).onChange(fromDayjsRange(updated, valueShape));
                    }}
                    views={views}
                    format={format ?? PRECISION_FORMAT[precision]}
                    minDate={minDate}
                    maxDate={maxDate}
                  />
                  <DateTimePicker
                    label='End'
                    value={dayjsRangeValue?.end ?? null}
                    disabled={disableEnd}
                    onChange={(next: Dayjs | null) => {
                      if (!next) return;
                      const cur = dayjsRangeValue ?? { start: next, end: next };
                      const updated: DateRangeValue = { ...cur, end: next, shortcut: 'custom' };
                      (props as Extract<DateRangePickerProps, { mode?: 'range' }>).onChange(fromDayjsRange(updated, valueShape));
                    }}
                    views={views}
                    format={format ?? PRECISION_FORMAT[precision]}
                    minDate={minDate}
                    maxDate={maxDate}
                  />
                </Stack>
              )}
              <Divider />
              <Stack direction='row' justifyContent='space-between'>
                <Button
                  size='small'
                  onClick={() => setShowCustom(false)}
                  sx={{ textTransform: 'none', fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}
                >
                  ← Shortcuts
                </Button>
                <Button
                  size='small'
                  variant='contained'
                  onClick={close}
                  sx={{
                    textTransform: 'none',
                    fontSize: 'var(--ds-text-small)',
                    backgroundColor: 'var(--ds-blue-500)',
                    '&:hover': { backgroundColor: 'var(--ds-blue-600)' },
                  }}
                >
                  Apply
                </Button>
              </Stack>
            </Stack>
          )}
        </Popover>
        {helperText !== undefined && (
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: error ? 'var(--ds-red-600)' : 'var(--ds-gray-600)',
              mt: 0.5,
              ml: 0.5,
            }}
          >
            {helperText}
          </Typography>
        )}
      </Box>
    </LocalizationProvider>
  );
}

export default DateRangePicker;
