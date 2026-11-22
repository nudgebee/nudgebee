/**
 * Variable substitution for dashboard panel queries.
 *
 * Values come from the host page's context (namespace / workload / pod on a
 * detail page) and from the dropdowns a dashboard declares in `variables`.
 *
 * The legacy AppDashboard renderer looped `String.replaceAll` over map keys, so
 * `$namespaceName` only survived because it happened to be inserted before
 * `$namespace` — correct by accident of object insertion order. A user-authored
 * variable named `$pod` would silently corrupt `$podName`.
 *
 * This does a single pass with a real token regex, so no substitution can ever
 * eat a longer variable's prefix regardless of declaration order. Unknown tokens
 * are left verbatim rather than blanked, so a typo shows up in the rendered
 * query instead of producing a valid-looking query over the wrong series.
 *
 * A dropdown's value can be written in a chosen format, `${name:csv}`, the
 * Grafana syntax for it. A plain `$name` stays a PromQL regex: `a|b`, or `.*`.
 */
const TOKEN_RE = /\$\{(\w+)(?::([\w-]+))?\}|\$(\w+)/g;

/** What a dropdown stands for until someone picks a value: every series, in a `=~` matcher. */
export const ALL_VALUES = '.*';
const ALL_LOG_VALUES = '.+';

/** A dropdown's state. `values` is every option when `all` is set, so a list format can spell them out. */
export interface PickedValues {
  values: string[];
  all: boolean;
  /** Sent as written in place of All, for the author to fit to the data source. */
  allValue?: string;
}

export type VariableValue = string | PickedValues;
export type VariableValues = Record<string, VariableValue>;

export type VariableFormat = 'promql' | 'logql' | 'regex' | 'pipe' | 'csv' | 'raw' | 'singlequote' | 'doublequote' | 'sqlstring' | 'json' | 'lucene';

/** One value as regex text, for a reader that takes the pattern directly rather than in a quoted string. */
export function plainRegexValue(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** A picked value as PromQL regex text, inside a double-quoted string: `a.b` must match only `a.b`. */
export function regexValue(value: string): string {
  return plainRegexValue(value).replace(/[\\"]/g, '\\$&');
}

const quoted = (values: string[], mark: string, escaped: string) => values.map((v) => mark + v.split(mark).join(escaped) + mark).join(',');

const FORMATTERS: Record<VariableFormat, (values: string[]) => string> = {
  promql: (values) => values.map(regexValue).join('|'),
  logql: (values) => values.map(regexValue).join('|'),
  regex: (values) => (values.length > 1 ? `(${values.map(plainRegexValue).join('|')})` : plainRegexValue(values[0] ?? '')),
  pipe: (values) => values.join('|'),
  csv: (values) => values.join(','),
  raw: (values) => values.join(','),
  singlequote: (values) => quoted(values, "'", "\\'"),
  doublequote: (values) => quoted(values, '"', '\\"'),
  sqlstring: (values) => quoted(values, "'", "''"),
  json: (values) => JSON.stringify(values),
  lucene: (values) => {
    const terms = values.map((v) => `"${v.replace(/(["\\])/g, '\\$1')}"`);
    return terms.length > 1 ? `(${terms.join(' OR ')})` : terms[0] ?? '';
  },
};

/** The formats that read as a pattern, where every value is one wildcard rather than a list. */
const PATTERN_FORMATS = new Set<VariableFormat>(['promql', 'regex', 'pipe']);

function formatPicked(picked: PickedValues, format: VariableFormat): string {
  if (picked.all && picked.allValue) return picked.allValue;
  // Loki refuses a selector whose every matcher can match nothing, which `.*` does.
  if (picked.all && format === 'logql') return ALL_LOG_VALUES;
  if (picked.all && PATTERN_FORMATS.has(format)) return ALL_VALUES;
  return FORMATTERS[format](picked.values);
}

export function renderTemplate(input: string, values: VariableValues, defaultFormat: VariableFormat = 'promql'): string {
  if (!input || input.indexOf('$') === -1) return input;
  return input.replace(TOKEN_RE, (match, braced?: string, format?: string, bare?: string) => {
    const name = braced || bare;
    if (!name) return match;
    const value = values[name];
    if (value === undefined) return match;
    if (typeof value === 'string') return value;
    if (format && !(format in FORMATTERS)) return match;
    return formatPicked(value, (format as VariableFormat) || defaultFormat);
  });
}

/** Collects the variable names a query string actually references. */
export function referencedVariables(input: string): string[] {
  const found = new Set<string>();
  if (!input) return [];
  for (const m of input.matchAll(TOKEN_RE)) {
    const name = m[1] || m[3];
    if (name) found.add(name);
  }
  return [...found];
}
