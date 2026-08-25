/**
 * Variable substitution for dashboard panel queries.
 *
 * Values come only from the host page's context (namespace / workload / pod on
 * a detail page) — dashboards do not declare their own variables.
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
 */
const TOKEN_RE = /\$\{(\w+)\}|\$(\w+)/g;

export type VariableValues = Record<string, string>;

export function renderTemplate(input: string, values: VariableValues): string {
  if (!input || input.indexOf('$') === -1) return input;
  return input.replace(TOKEN_RE, (match, braced?: string, bare?: string) => {
    const name = braced || bare;
    if (!name) return match;
    const value = values[name];
    return value === undefined ? match : value;
  });
}

/** Collects the variable names a query string actually references. */
export function referencedVariables(input: string): string[] {
  const found = new Set<string>();
  if (!input) return [];
  for (const m of input.matchAll(TOKEN_RE)) {
    const name = m[1] || m[2];
    if (name) found.add(name);
  }
  return [...found];
}
