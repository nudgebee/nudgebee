import { CronExpressionParser } from 'cron-parser';
import cronstrue from 'cronstrue';

/**
 * Cron helpers for schedule triggers.
 *
 * These mirror `model.ValidateCronExpression` in runbook-server
 * (internal/model/validation.go), which parses with robfig/cron's
 * `ParseStandard`. The backend stays the authority — this exists so the builder
 * can reject a bad expression inline instead of after a failed save round trip.
 * Two places where cron-parser is more permissive than robfig and we tighten it
 * back: field count (robfig requires exactly 5) and day-of-week (robfig allows
 * 0-6, cron-parser allows 0-7).
 */

export interface CronValidation {
  valid: boolean;
  error?: string;
}

const DESCRIPTOR_ALIASES = ['@yearly', '@annually', '@monthly', '@weekly', '@daily', '@midnight', '@hourly'];

const EVERY_DESCRIPTOR = /^@every\s+(\S+)$/i;
// Go's time.ParseDuration format, e.g. `1h`, `90s`, `1h30m`.
const GO_DURATION = /^[+-]?(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;
const TZ_PREFIX = /^(CRON_TZ|TZ)=(\S*)(\s+(.*))?$/;

const DESCRIPTOR_DESCRIPTIONS: Record<string, string> = {
  '@yearly': 'Once a year, at midnight on 1 January',
  '@annually': 'Once a year, at midnight on 1 January',
  '@monthly': 'Once a month, at midnight on the 1st',
  '@weekly': 'Once a week, at midnight on Sunday',
  '@daily': 'Every day at midnight',
  '@midnight': 'Every day at midnight',
  '@hourly': 'Every hour, on the hour',
};

interface StrippedExpression {
  spec: string;
  timezone?: string;
  error?: string;
}

/**
 * Temporal accepts an optional `CRON_TZ=<tz>` / `TZ=<tz>` prefix that neither
 * robfig/cron nor cron-parser understands, so it is peeled off before parsing.
 */
const stripTimezonePrefix = (expr: string): StrippedExpression => {
  const trimmed = expr.trim();
  const match = TZ_PREFIX.exec(trimmed);
  if (!match) {
    return { spec: trimmed };
  }

  const [, keyword, timezone, , remainder] = match;
  if (!timezone || !remainder?.trim()) {
    return { spec: '', error: `${keyword}= prefix must be followed by a timezone and a schedule` };
  }
  return { spec: remainder.trim(), timezone };
};

const describeDescriptor = (spec: string): string | undefined => {
  const lower = spec.toLowerCase();
  if (DESCRIPTOR_DESCRIPTIONS[lower]) {
    return DESCRIPTOR_DESCRIPTIONS[lower];
  }
  const every = EVERY_DESCRIPTOR.exec(spec);
  return every ? `Every ${every[1]}` : undefined;
};

const validateDescriptor = (spec: string): CronValidation => {
  const lower = spec.toLowerCase();
  if (DESCRIPTOR_ALIASES.includes(lower)) {
    return { valid: true };
  }
  const every = EVERY_DESCRIPTOR.exec(spec);
  if (every) {
    return GO_DURATION.test(every[1])
      ? { valid: true }
      : { valid: false, error: `"${every[1]}" is not a valid duration — use a Go duration such as 1h, 90s or 1h30m` };
  }
  return { valid: false, error: `"${spec}" is not a supported cron descriptor (${DESCRIPTOR_ALIASES.join(', ')} or @every <duration>)` };
};

/**
 * robfig/cron caps day-of-week at 6, cron-parser allows 7 as a second Sunday.
 * Reject 7 here so the builder does not accept something the API will refuse.
 */
const hasOutOfRangeDayOfWeek = (dayOfWeekField: string): boolean =>
  dayOfWeekField.split(/[,\-/]/).some((token) => /^\d+$/.test(token) && Number(token) > 6);

export const validateCron = (expr: string): CronValidation => {
  if (!expr?.trim()) {
    return { valid: false, error: 'Cron expression is required' };
  }

  const { spec, error } = stripTimezonePrefix(expr);
  if (error) {
    return { valid: false, error };
  }

  if (spec.startsWith('@')) {
    return validateDescriptor(spec);
  }

  const fields = spec.split(/\s+/);
  if (fields.length !== 5) {
    return {
      valid: false,
      error: `Expected exactly 5 fields (minute hour day-of-month month day-of-week), found ${fields.length}`,
    };
  }
  if (hasOutOfRangeDayOfWeek(fields[4])) {
    return { valid: false, error: 'Day of week must be between 0 (Sunday) and 6 (Saturday)' };
  }

  try {
    CronExpressionParser.parse(spec, { tz: 'UTC' });
    return { valid: true };
  } catch (e: any) {
    return { valid: false, error: e?.message || 'Invalid cron expression' };
  }
};

export const isCronExpressionValid = (expr: string): boolean => validateCron(expr).valid;

/** Human-readable summary, e.g. "At 09:00 AM, Monday through Friday". Empty when unparseable. */
export const describeCron = (expr: string): string => {
  if (!validateCron(expr).valid) {
    return '';
  }
  const { spec } = stripTimezonePrefix(expr);
  const descriptor = describeDescriptor(spec);
  if (descriptor) {
    return descriptor;
  }
  try {
    return cronstrue.toString(spec, { throwExceptionOnParseError: true, verbose: false });
  } catch {
    return '';
  }
};

/**
 * Next `count` fire times in UTC. Returns an empty array for `@every`
 * descriptors, whose next run depends on when the schedule was registered.
 */
export const nextCronRuns = (expr: string, count = 3): Date[] => {
  if (!validateCron(expr).valid) {
    return [];
  }
  const { spec } = stripTimezonePrefix(expr);
  if (EVERY_DESCRIPTOR.test(spec)) {
    return [];
  }
  try {
    const iterator = CronExpressionParser.parse(spec, { tz: 'UTC' });
    return Array.from({ length: count }, () => iterator.next().toDate());
  } catch {
    return [];
  }
};
