/**
 * Format.* — DS V2 forward-spec namespace.
 * Spec: app/design-system/primitives/data-display/format.html
 *
 * Per D6: the existing `format/*` utilities stay canonical at the codebase
 * level. The `Format.*` namespace below is the design-system shape — the V2
 * surface presents one tokenised primitive per data type. Consumers in legacy
 * code can keep using utility paths until a future migration cycle is funded.
 *
 * Variants per spec:
 *   Number   = 'compact' | 'full' | 'signed'
 *   Currency = 'compact' | 'full' | 'with-period'
 *   Memory   = 'binary' | 'decimal'  (MiB vs MB)
 *   Datetime = 'absolute' | 'relative' | 'both'
 *   Text     = 'truncate' | 'copy' | 'link'
 *
 * Don't (per spec):
 *   - Don't format numbers in JSX manually. Every untokenised
 *     `.toLocaleString()` is a future inconsistency.
 *   - Don't nest Format primitives. Pick the most-specific one.
 *   - Don't relative-format timestamps older than 7 days — readers can't
 *     reverse-translate "27 days ago" into a date.
 *
 * Migration:
 *   `format/Currency`, `format/Number`, `format/Memory`, `format/Datetime`,
 *   `format/Text` → `Format.Currency`, etc. (forward-looking; legacy stays.)
 *   `CopyableText`, `ExpandableText`, `TextWithTooltipAndCopy` collapse into
 *   `Format.Text` with the appropriate prop.
 */
import * as React from 'react';
import { Box, SxProps, Theme, Tooltip } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import CheckIcon from '@mui/icons-material/Check';

// --- Number ----------------------------------------------------------------

export type FormatNumberVariant = 'full' | 'compact' | 'signed';

export interface FormatNumberProps {
  value: number;
  variant?: FormatNumberVariant;
  /** BCP-47 locale (default: browser). */
  locale?: string;
  className?: string;
  id?: string;
}

function NumberFmt({ value, variant = 'full', locale, className, id }: FormatNumberProps) {
  const text = React.useMemo(() => {
    if (variant === 'compact') {
      return new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(value);
    }
    if (variant === 'signed') {
      return new Intl.NumberFormat(locale, { signDisplay: 'always' }).format(value);
    }
    return new Intl.NumberFormat(locale).format(value);
  }, [value, variant, locale]);
  return (
    <Box component='span' className={className} id={id} sx={{ fontVariantNumeric: 'tabular-nums' }}>
      {text}
    </Box>
  );
}

// --- Currency --------------------------------------------------------------

export type FormatCurrencyVariant = 'full' | 'compact' | 'with-period';

export interface FormatCurrencyProps {
  value: number;
  /** ISO-4217 currency code (default 'USD'). */
  unit?: string;
  variant?: FormatCurrencyVariant;
  /** Period suffix for `with-period` variant (e.g. "/mo"). Auto-selects variant if provided alone. */
  period?: string;
  /** Alias for `period` — suffix text rendered after the value (e.g. "/mo"). */
  suffix?: string;
  /** Max fractional digits (default 0). */
  precision?: number;
  /** Wrap the value in a Tooltip showing the full unformatted number. */
  withTooltip?: boolean;
  locale?: string;
  className?: string;
  id?: string;
  sx?: SxProps<Theme>;
  sxSuffix?: SxProps<Theme>;
}

function CurrencyFmt({
  value,
  unit = 'USD',
  variant,
  period,
  suffix,
  precision,
  withTooltip,
  locale,
  className,
  id,
  sx,
  sxSuffix,
}: FormatCurrencyProps) {
  const resolvedSuffix = period ?? suffix;
  const resolvedVariant: FormatCurrencyVariant = variant ?? (resolvedSuffix ? 'with-period' : 'full');
  const main = React.useMemo(() => {
    const opts: Intl.NumberFormatOptions = { style: 'currency', currency: unit, maximumFractionDigits: precision ?? 0 };
    if (resolvedVariant === 'compact') {
      opts.notation = 'compact';
      opts.maximumFractionDigits = precision ?? 1;
    }
    return new Intl.NumberFormat(locale, opts).format(value);
  }, [value, unit, resolvedVariant, locale, precision]);
  const content = (
    <Box component='span' className={className} id={id} sx={{ fontVariantNumeric: 'tabular-nums', ...sx }}>
      {main}
      {resolvedVariant === 'with-period' && resolvedSuffix && (
        <Box component='span' sx={{ color: 'var(--ds-gray-500)', marginLeft: '4px', ...sxSuffix }}>
          {resolvedSuffix}
        </Box>
      )}
    </Box>
  );
  if (withTooltip) {
    return <Tooltip title={new Intl.NumberFormat(locale, { style: 'currency', currency: unit }).format(value)}>{content}</Tooltip>;
  }
  return content;
}

// --- Memory ----------------------------------------------------------------

export type FormatMemoryVariant = 'binary' | 'decimal';

export interface FormatMemoryProps {
  bytes: number;
  variant?: FormatMemoryVariant;
  /** Number of fractional digits to retain (default 1 for binary, 2 for decimal). */
  precision?: number;
  className?: string;
  id?: string;
}

function MemoryFmt({ bytes, variant = 'binary', precision, className, id }: FormatMemoryProps) {
  const text = React.useMemo(() => {
    const isBinary = variant === 'binary';
    const base = isBinary ? 1024 : 1000;
    const units = isBinary ? ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'] : ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let n = Math.abs(bytes);
    let i = 0;
    while (n >= base && i < units.length - 1) {
      n /= base;
      i += 1;
    }
    const frac = precision ?? (isBinary ? 1 : 2);
    const rounded = i === 0 ? Math.round(n) : Number(n.toFixed(frac));
    const sign = bytes < 0 ? '-' : '';
    return `${sign}${rounded} ${units[i]}`;
  }, [bytes, variant, precision]);
  return (
    <Box component='span' className={className} id={id} sx={{ fontVariantNumeric: 'tabular-nums' }}>
      {text}
    </Box>
  );
}

// --- Datetime --------------------------------------------------------------

export type FormatDatetimeVariant = 'absolute' | 'relative' | 'both';

export interface FormatDatetimeProps {
  value: Date | number | string;
  variant?: FormatDatetimeVariant;
  locale?: string;
  /** Override the absolute-format options (defaults to 'YYYY-MM-DD HH:mm UTC'-ish via Intl). */
  absoluteOptions?: Intl.DateTimeFormatOptions;
  className?: string;
  id?: string;
}

const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000;

function toRelative(deltaMs: number, locale?: string): string {
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  const past = deltaMs >= 0;
  const abs = Math.abs(deltaMs);
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (abs < minute) return rtf.format(past ? -Math.floor(abs / 1000) : Math.floor(abs / 1000), 'second');
  if (abs < hour) return rtf.format(past ? -Math.floor(abs / minute) : Math.floor(abs / minute), 'minute');
  if (abs < day) return rtf.format(past ? -Math.floor(abs / hour) : Math.floor(abs / hour), 'hour');
  return rtf.format(past ? -Math.floor(abs / day) : Math.floor(abs / day), 'day');
}

function DatetimeFmt({ value, variant = 'absolute', locale, absoluteOptions, className, id }: FormatDatetimeProps) {
  const ts = value instanceof Date ? value : new Date(value);
  const now = Date.now();
  const deltaMs = now - ts.getTime();

  // Spec Don't: don't relative-format timestamps older than 7 days. Fall back to absolute.
  const relativeIsValid = Math.abs(deltaMs) < SEVEN_DAYS_MS;
  const effectiveVariant: FormatDatetimeVariant = variant === 'relative' && !relativeIsValid ? 'absolute' : variant;

  const absText = React.useMemo(
    () =>
      ts.toLocaleString(
        locale,
        absoluteOptions ?? {
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          timeZone: 'UTC',
          timeZoneName: 'short',
        }
      ),
    [ts, locale, absoluteOptions]
  );
  const relText = React.useMemo(() => (relativeIsValid ? toRelative(deltaMs, locale) : absText), [deltaMs, locale, relativeIsValid, absText]);

  if (effectiveVariant === 'absolute') {
    return (
      <Box component='time' dateTime={ts.toISOString()} className={className} id={id}>
        {absText}
      </Box>
    );
  }
  if (effectiveVariant === 'relative') {
    return (
      <Tooltip title={absText} placement='top'>
        <Box component='time' dateTime={ts.toISOString()} className={className} id={id}>
          {relText}
        </Box>
      </Tooltip>
    );
  }
  // 'both' — relative as primary, absolute in muted small text after.
  return (
    <Box component='span' className={className} id={id} sx={{ display: 'inline-flex', gap: '6px', alignItems: 'baseline' }}>
      <Box component='time' dateTime={ts.toISOString()}>
        {relText}
      </Box>
      <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
        ({absText})
      </Box>
    </Box>
  );
}

// --- Text ------------------------------------------------------------------

export type FormatTextVariant = 'plain' | 'truncate' | 'copy' | 'link';

export interface FormatTextProps {
  value: string;
  /** Truncate to N chars, with full string in tooltip. Implies `variant='truncate'`. */
  truncate?: number;
  /** Render an inline copy button. Implies `variant='copy'`. */
  copy?: boolean;
  /** Render value as a link. */
  href?: string;
  /** When `href` is an external URL, open in a new tab. */
  external?: boolean;
  className?: string;
  id?: string;
}

function TextFmt({ value, truncate, copy, href, external, className, id }: FormatTextProps) {
  const [copied, setCopied] = React.useState(false);

  const display = truncate && value.length > truncate ? `${value.slice(0, truncate)}…` : value;
  const truncated = truncate && value.length > truncate;

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard failures are silent; user can re-trigger.
    }
  };

  const inner = href ? (
    <Box
      component='a'
      href={href}
      target={external ? '_blank' : undefined}
      rel={external ? 'noopener noreferrer' : undefined}
      sx={{ color: 'var(--ds-blue-600)', textDecoration: 'none', '&:hover': { textDecoration: 'underline' } }}
    >
      {display}
    </Box>
  ) : (
    <Box component='span'>{display}</Box>
  );

  const body = truncated ? (
    <Tooltip title={value} placement='top'>
      {inner}
    </Tooltip>
  ) : (
    inner
  );

  if (!copy) {
    return (
      <Box component='span' className={className} id={id}>
        {body}
      </Box>
    );
  }

  return (
    <Box component='span' className={className} id={id} sx={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
      {body}
      <Box
        component='button'
        type='button'
        onClick={handleCopy}
        aria-label={copied ? 'Copied' : 'Copy'}
        sx={{
          width: '18px',
          height: '18px',
          padding: 0,
          background: 'transparent',
          border: 0,
          cursor: 'pointer',
          color: 'var(--ds-gray-500)',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          borderRadius: 'var(--ds-radius-xs)',
          '&:hover': { color: 'var(--ds-gray-700)', backgroundColor: 'var(--ds-gray-100)' },
        }}
      >
        {copied ? <CheckIcon sx={{ fontSize: 11 }} /> : <ContentCopyIcon sx={{ fontSize: 11 }} />}
      </Box>
    </Box>
  );
}

// --- Namespace export ------------------------------------------------------

export const Format = {
  Number: NumberFmt,
  Currency: CurrencyFmt,
  Memory: MemoryFmt,
  Datetime: DatetimeFmt,
  Text: TextFmt,
};

export { NumberFmt as Number, CurrencyFmt as Currency, MemoryFmt as Memory, DatetimeFmt as Datetime, TextFmt as Text };

export default Format;
