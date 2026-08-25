/**
 * ScheduleBuilder — cron picker for the schedule trigger.
 *
 * Two modes: a visual picker whose output is valid by construction, and an
 * advanced raw-cron field that is validated live against `@utils/cron` (which
 * mirrors the runbook-server validator). An expression that does not map onto
 * one of the visual presets opens in Advanced rather than being rewritten.
 */
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import Input from '@ui/Input';
import Select from '@ui/Select';
import ToggleGroup from '@ui/ToggleGroup';
import { ds } from '@utils/colors';
import { describeCron, nextCronRuns, validateCron } from '@utils/cron';

type BuilderMode = 'visual' | 'advanced';
type Frequency = 'minutes' | 'hourly' | 'daily' | 'weekly' | 'monthly';

interface VisualState {
  frequency: Frequency;
  /** Step for the "every N minutes" preset. */
  intervalMinutes: number;
  minute: number;
  hour: number;
  /** Day-of-week numbers, 0 = Sunday. */
  weekdays: number[];
  dayOfMonth: number;
}

interface ScheduleBuilderProps {
  value: string;
  onChange: (next: string) => void;
  /** Validation message owned by the parent (e.g. "Cron expression is required"). */
  error?: string;
}

const DEFAULT_VISUAL: VisualState = {
  frequency: 'daily',
  intervalMinutes: 15,
  minute: 0,
  hour: 9,
  weekdays: [1, 2, 3, 4, 5],
  dayOfMonth: 1,
};

const MINUTE_STEPS = [1, 2, 5, 10, 15, 20, 30];

const WEEKDAY_LABELS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

const FREQUENCY_OPTIONS = [
  { value: 'minutes', label: 'Every N minutes' },
  { value: 'hourly', label: 'Hourly' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
];

const pad = (n: number) => String(n).padStart(2, '0');

const numberOptions = (count: number, label: (n: number) => string) =>
  Array.from({ length: count }, (_, n) => ({ value: String(n), label: label(n) }));

const MINUTE_OPTIONS = numberOptions(60, (n) => pad(n));
const HOUR_OPTIONS = numberOptions(24, (n) => `${pad(n)}:00`);
const DAY_OF_MONTH_OPTIONS = Array.from({ length: 31 }, (_, i) => ({ value: String(i + 1), label: String(i + 1) }));
const WEEKDAY_OPTIONS = WEEKDAY_LABELS.map((label, index) => ({ value: String(index), label }));

const buildCron = (state: VisualState): string => {
  const { frequency, intervalMinutes, minute, hour, weekdays, dayOfMonth } = state;
  switch (frequency) {
    case 'minutes':
      return `*/${intervalMinutes} * * * *`;
    case 'hourly':
      return `${minute} * * * *`;
    case 'weekly':
      return `${minute} ${hour} * * ${[...weekdays].sort((a, b) => a - b).join(',')}`;
    case 'monthly':
      return `${minute} ${hour} ${dayOfMonth} * *`;
    case 'daily':
    default:
      return `${minute} ${hour} * * *`;
  }
};

/** Expands a day-of-week field of plain numbers, lists and ranges (e.g. `1-5,0`). */
const parseWeekdays = (field: string): number[] | null => {
  const days = new Set<number>();
  for (const token of field.split(',')) {
    const range = /^(\d)-(\d)$/.exec(token);
    if (range) {
      const [from, to] = [Number(range[1]), Number(range[2])];
      if (from > to || to > 6) return null;
      for (let d = from; d <= to; d += 1) days.add(d);
      continue;
    }
    if (!/^\d$/.test(token)) return null;
    days.add(Number(token));
  }
  return days.size ? [...days].sort((a, b) => a - b) : null;
};

/**
 * Best-effort reverse mapping of a cron expression onto the visual presets.
 * Returns null when the expression is richer than the pickers can represent —
 * the caller then opens Advanced mode and leaves the expression untouched.
 */
const parseCronToVisual = (expr: string): VisualState | null => {
  const fields = expr.trim().split(/\s+/);
  if (fields.length !== 5) return null;

  const [minute, hour, dayOfMonth, month, dayOfWeek] = fields;
  if (month !== '*') return null;

  const step = /^\*\/(\d+)$/.exec(minute);
  if (step && hour === '*' && dayOfMonth === '*' && dayOfWeek === '*') {
    const intervalMinutes = Number(step[1]);
    if (!MINUTE_STEPS.includes(intervalMinutes)) return null;
    return { ...DEFAULT_VISUAL, frequency: 'minutes', intervalMinutes };
  }

  if (!/^\d{1,2}$/.test(minute)) return null;
  const minuteValue = Number(minute);
  if (minuteValue > 59) return null;

  if (hour === '*' && dayOfMonth === '*' && dayOfWeek === '*') {
    return { ...DEFAULT_VISUAL, frequency: 'hourly', minute: minuteValue };
  }

  if (!/^\d{1,2}$/.test(hour)) return null;
  const hourValue = Number(hour);
  if (hourValue > 23) return null;

  if (dayOfMonth === '*' && dayOfWeek === '*') {
    return { ...DEFAULT_VISUAL, frequency: 'daily', minute: minuteValue, hour: hourValue };
  }

  if (dayOfMonth === '*') {
    const weekdays = parseWeekdays(dayOfWeek);
    if (!weekdays) return null;
    return { ...DEFAULT_VISUAL, frequency: 'weekly', minute: minuteValue, hour: hourValue, weekdays };
  }

  if (dayOfWeek === '*' && /^\d{1,2}$/.test(dayOfMonth)) {
    const dayValue = Number(dayOfMonth);
    if (dayValue < 1 || dayValue > 31) return null;
    return { ...DEFAULT_VISUAL, frequency: 'monthly', minute: minuteValue, hour: hourValue, dayOfMonth: dayValue };
  }

  return null;
};

const SchedulePreview: React.FC<{ expression: string }> = ({ expression }) => {
  if (!validateCron(expression).valid) {
    return null;
  }

  const description = describeCron(expression);
  const upcoming = nextCronRuns(expression, 3);

  return (
    <Box
      sx={{
        p: 'var(--ds-space-3)',
        backgroundColor: ds.blue[100],
        borderRadius: ds.radius.sm,
        border: `1px solid ${ds.gray[200]}`,
      }}
      data-testid='schedule-builder-preview'
    >
      <Box
        component='code'
        sx={{
          display: 'inline-block',
          backgroundColor: ds.background[100],
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          borderRadius: 'var(--ds-radius-sm)',
          fontSize: 'var(--ds-text-small)',
          color: ds.gray[700],
        }}
      >
        {expression}
      </Box>
      {description && <Typography sx={{ mt: 1, fontSize: 'var(--ds-text-small)', color: ds.gray[700] }}>{description} (UTC)</Typography>}
      {upcoming.length > 0 && (
        <Typography component='div' sx={{ mt: 1, fontSize: 'var(--ds-text-caption)', color: ds.gray[400], lineHeight: 1.6 }}>
          Next runs:
          {upcoming.map((run) => (
            <Box key={run.toISOString()} component='span' sx={{ display: 'block' }}>
              {run.toISOString().replace('T', ' ').slice(0, 16)} UTC
            </Box>
          ))}
        </Typography>
      )}
    </Box>
  );
};

const ScheduleBuilder: React.FC<ScheduleBuilderProps> = ({ value, onChange, error }) => {
  const [mode, setMode] = useState<BuilderMode>(() => (value.trim() && !parseCronToVisual(value) ? 'advanced' : 'visual'));
  const [visual, setVisual] = useState<VisualState>(() => parseCronToVisual(value) ?? DEFAULT_VISUAL);
  // Tracks what this component last pushed upward so an echoed prop doesn't
  // re-derive (and possibly re-open Advanced mode) on every keystroke. Starts
  // null so the first run always adopts the incoming value.
  const lastEmittedRef = useRef<string | null>(null);

  const emit = useCallback(
    (next: string) => {
      lastEmittedRef.current = next;
      onChange(next);
    },
    [onChange]
  );

  useEffect(() => {
    if (value === lastEmittedRef.current) return;
    lastEmittedRef.current = value;

    // A trigger with no cron yet (freshly added, or a node switch) adopts the
    // builder's default so what the pickers show is what gets saved.
    if (!value.trim()) {
      setVisual(DEFAULT_VISUAL);
      setMode('visual');
      emit(buildCron(DEFAULT_VISUAL));
      return;
    }

    const parsed = parseCronToVisual(value);
    if (parsed) {
      setVisual(parsed);
      setMode('visual');
    } else {
      setMode('advanced');
    }
  }, [value, emit]);

  const applyVisual = useCallback(
    (patch: Partial<VisualState>) => {
      setVisual((prev) => {
        const next = { ...prev, ...patch };
        emit(buildCron(next));
        return next;
      });
    },
    [emit]
  );

  const handleModeChange = useCallback(
    (next: BuilderMode) => {
      setMode(next);
      // Switching to the visual picker adopts its current selection, so the
      // expression always matches what the pickers show.
      if (next === 'visual') {
        emit(buildCron(visual));
      }
    },
    [emit, visual]
  );

  const advancedError = useMemo(() => {
    if (mode !== 'advanced' || !value.trim()) return error;
    const result = validateCron(value);
    return result.valid ? error : result.error;
  }, [mode, value, error]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
          Schedule{' '}
          <Box component='span' sx={{ color: ds.red[500] }}>
            *
          </Box>
        </Typography>
        <ToggleGroup
          selection='single'
          size='sm'
          ariaLabel='Schedule editing mode'
          id='schedule-builder-mode'
          value={mode}
          onChange={(next) => handleModeChange(next as BuilderMode)}
          options={[
            { value: 'visual', label: 'Builder' },
            { value: 'advanced', label: 'Cron expression' },
          ]}
        />
      </Box>

      {mode === 'visual' ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <Select
            label='Frequency'
            size='sm'
            required
            id='schedule-builder-frequency'
            options={FREQUENCY_OPTIONS}
            value={visual.frequency}
            onChange={(next) => applyVisual({ frequency: next as Frequency })}
          />

          {visual.frequency === 'minutes' && (
            <Select
              label='Run every'
              size='sm'
              required
              id='schedule-builder-interval'
              options={MINUTE_STEPS.map((step) => ({ value: String(step), label: step === 1 ? 'Minute' : `${step} minutes` }))}
              value={String(visual.intervalMinutes)}
              onChange={(next) => applyVisual({ intervalMinutes: Number(next) })}
            />
          )}

          {visual.frequency === 'weekly' && (
            <Select
              multiple
              label='Days of week'
              size='sm'
              required
              id='schedule-builder-weekdays'
              options={WEEKDAY_OPTIONS}
              value={visual.weekdays.map(String)}
              onChange={(next) => applyVisual({ weekdays: next.length ? next.map(Number) : visual.weekdays })}
            />
          )}

          {visual.frequency === 'monthly' && (
            <Select
              label='Day of month'
              size='sm'
              required
              id='schedule-builder-day-of-month'
              options={DAY_OF_MONTH_OPTIONS}
              value={String(visual.dayOfMonth)}
              onChange={(next) => applyVisual({ dayOfMonth: Number(next) })}
            />
          )}

          {visual.frequency !== 'minutes' && (
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)' }}>
              {visual.frequency !== 'hourly' && (
                <Select
                  label='Hour (UTC)'
                  size='sm'
                  required
                  id='schedule-builder-hour'
                  minWidth='120px'
                  options={HOUR_OPTIONS}
                  value={String(visual.hour)}
                  onChange={(next) => applyVisual({ hour: Number(next) })}
                />
              )}
              <Select
                label='Minute'
                size='sm'
                required
                id='schedule-builder-minute'
                minWidth='120px'
                options={MINUTE_OPTIONS}
                value={String(visual.minute)}
                onChange={(next) => applyVisual({ minute: Number(next) })}
              />
            </Box>
          )}
        </Box>
      ) : (
        <Input
          label='Cron Expression'
          size='sm'
          required
          id='schedule-builder-cron'
          placeholder='0 9 * * 1-5'
          instructionText='Five fields: minute hour day-of-month month day-of-week'
          value={value}
          error={advancedError}
          onChange={emit}
        />
      )}

      <SchedulePreview expression={value} />
    </Box>
  );
};

export default ScheduleBuilder;
