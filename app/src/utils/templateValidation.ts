// Client-side validation/lint for the date/time template filters supported by the
// runbook templating engine. This MIRRORS the backend validator in
// runbook-server/internal/workflow/template_validation.go — keep the two in sync.
// The backend remains the source of truth (it re-validates on save); this exists only
// to give fast inline feedback in the workflow builder.

// Pull the literal string argument out of a date-filter call. Only literal args (e.g.
// tz("Asia/Kolkata")) can be checked statically; dynamic args like tz(some_var) are
// skipped and left to the filters' runtime behavior.
const TZ_ARG_RE = /tz\(\s*["']([^"']*)["']/g;
const DATE_FORMAT_ARG_RE = /date_format\(\s*["']([^"']*)["']/g;
const STRFTIME_ARG_RE = /strftime\(\s*["']([^"']*)["']/g;
const STRFTIME_CODE_RE = /%[a-zA-Z]/;

// isValidTimeZone matches the backend's time.LoadLocation semantics: "" resolves to UTC
// (valid), otherwise the name must be a real IANA zone. We rely on the DateTimeFormat
// constructor, which canonicalizes aliases (e.g. Asia/Kolkata) and throws RangeError on a
// bad zone — unlike Intl.supportedValuesOf, whose list is canonical-only and varies by
// runtime ICU (it omits common aliases like Asia/Kolkata).
export function isValidTimeZone(zone: string): boolean {
  if (zone === '') return true; // LoadLocation("") == UTC
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}

export interface DateTemplateResult {
  error?: string; // hard problem — should block save
  warnings: string[]; // advisory — likely mistakes
}

// validateDateTemplate inspects one template string for invalid tz() zones (hard error)
// and mixed date-format dialects (soft warnings).
export function validateDateTemplate(value: string): DateTemplateResult {
  const result: DateTemplateResult = { warnings: [] };
  if (typeof value !== 'string' || (!value.includes('{{') && !value.includes('{%'))) {
    return result;
  }

  // Hard: invalid timezone in a literal tz() call.
  for (const m of value.matchAll(TZ_ARG_RE)) {
    const zone = m[1];
    if (!isValidTimeZone(zone)) {
      result.error = `Invalid timezone "${zone}" in tz() — use an IANA name like "Asia/Kolkata"`;
      return result; // first hard error wins
    }
  }

  // Soft: strftime %-codes inside date_format() (which wants a Go reference layout).
  for (const m of value.matchAll(DATE_FORMAT_ARG_RE)) {
    if (STRFTIME_CODE_RE.test(m[1])) {
      result.warnings.push(`date_format("${m[1]}") looks like strftime — date_format uses a Go layout (e.g. "2006-01-02 15:04"), not %-codes`);
    }
  }

  // Soft: the Go reference year 2006 inside strftime() (which wants C-style codes).
  for (const m of value.matchAll(STRFTIME_ARG_RE)) {
    if (m[1].includes('2006')) {
      result.warnings.push(`strftime("${m[1]}") looks like a Go layout — strftime uses C-style codes (e.g. "%Y-%m-%d %H:%M"), not 2006-01-02`);
    }
  }

  return result;
}
