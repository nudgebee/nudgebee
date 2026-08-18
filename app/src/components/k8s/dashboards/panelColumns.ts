/**
 * A table panel's column settings: what each column shows, and where a row can
 * take you.
 *
 * ONE list, `options.columns`, rather than a side-table per feature. An entry
 * with `name` configures a column the query returns; an entry without one
 * describes a column the panel adds. Everything a column can do — hiding,
 * formatting, a link, and whatever comes next — is an optional key on that
 * entry, so the next attribute is additive rather than another array keyed by
 * column name that every reader has to join against.
 *
 * Links are deliberately generic. The events listing hardcodes an Investigate
 * button, but a dashboard table can be events, recommendations, spend, tickets
 * or a Postgres snapshot, and each has its own "open the thing this row is
 * about" destination. Attaching the link to a column that already holds the
 * right text is what reads the same across all of them.
 *
 * Two rules the rest of this file exists to enforce:
 *
 *  1. A link URL is a RELATIVE in-app path. A panel is authored by one user and
 *     rendered by every viewer of the dashboard, so an unrestricted href is a
 *     stored-XSS vector (`javascript:…`) and an off-site link is a phishing one.
 *     `ValidateDefinition` in api-server/services/dashboard refuses the same
 *     shapes at save; this refuses them again at render, because a definition
 *     can also arrive by dashboard import.
 *  2. Row values are percent-encoded on the way in. Ids are uuids today, but a
 *     resource name with a `&` in it would otherwise silently rewrite the query
 *     string.
 */
import type { Panel, PanelColumn } from '@api1/dashboards';

/**
 * `{{column}}`, with optional inner spaces. Distinct from the `$var` dashboard
 * variables in templating.ts on purpose — these are filled from the ROW, not
 * from the host page, and the two would otherwise be indistinguishable in a URL.
 */
const ROW_TOKEN_RE = /\{\{\s*(\w+)\s*\}\}/g;

/** Column names a template fills in, in first-seen order. */
export function referencedColumns(url: string): string[] {
  const found = new Set<string>();
  if (!url) return [];
  for (const m of url.matchAll(ROW_TOKEN_RE)) found.add(m[1]);
  return [...found];
}

/**
 * Whether a link may point at this URL.
 *
 * Relative paths only: it must start with a single `/` (a leading `//` is
 * protocol-relative — a different origin wearing a path's clothes), and carry no
 * whitespace, control characters or backslashes.
 */
export function isSafeLinkUrl(url: string): boolean {
  // Trimmed, because the server trims before validating — otherwise a path with
  // a stray leading space saves happily and then renders as no link at all.
  const path = (url || '').trim();
  if (!path.startsWith('/') || path.startsWith('//')) return false;
  // Whitespace and control characters are how a scheme gets smuggled past a
  // prefix check — `/\tjavascript:…` reads as a path here and as a scheme to a
  // browser that strips them. A BACKSLASH is the same hole in another hat: the
  // URL spec treats `\` as `/` for http(s), so `/\evil.example` parses as
  // `//evil.example` and navigates off-site.
  // eslint-disable-next-line no-control-regex
  return !/[\s\u0000-\u001f\u007f\\]/.test(path);
}

function isNamed(value: unknown): boolean {
  return typeof value === 'string' && value.trim().length > 0;
}

/**
 * Reads a panel's column settings, tolerating anything an import may have
 * stored — including the two parallel arrays this shape replaced. The server
 * folds those forward on read, but an imported dashboard is raw JSON that has
 * never been through it.
 */
export function panelColumnsOf(panel: Panel): PanelColumn[] {
  const raw = panel.options?.columns;
  const columns = Array.isArray(raw) ? raw.filter((c): c is PanelColumn => Boolean(c) && typeof c === 'object') : [];
  return columns.length > 0 ? columns : legacyColumns(panel);
}

/**
 * `link_columns` + `hidden_columns` as one `columns` list. Mirrors
 * upgradeTableOptions in api-server/services/dashboard — the two must agree, or
 * an imported dashboard renders differently from the same dashboard saved.
 */
function legacyColumns(panel: Panel): PanelColumn[] {
  const links = Array.isArray(panel.options?.link_columns) ? panel.options.link_columns : [];
  const hidden = Array.isArray(panel.options?.hidden_columns) ? panel.options.hidden_columns : [];
  const columns: PanelColumn[] = [];
  const byName = new Map<string, PanelColumn>();

  for (const name of hidden) {
    if (!isNamed(name)) continue;
    const column: PanelColumn = { name, visibility: 'hidden' };
    byName.set(name, column);
    columns.push(column);
  }
  for (const link of links) {
    if (!link || typeof link !== 'object' || typeof link.url !== 'string') continue;
    // A link on a column that is also hidden merges into that one entry: two
    // entries naming the same column is the ambiguity this shape exists to
    // remove, and validation rejects it.
    if (isNamed(link.column)) {
      const name = (link.column as string).trim();
      const existing = byName.get(name);
      if (existing) existing.link = { url: link.url };
      else {
        const column: PanelColumn = { name, link: { url: link.url } };
        byName.set(name, column);
        columns.push(column);
      }
    } else if (isNamed(link.title)) {
      columns.push({ title: link.title, link: { url: link.url } });
    }
  }
  return columns;
}

/** Settings for the columns the query returns, keyed by column name. */
export function columnSettings(columns: PanelColumn[]): Map<string, PanelColumn> {
  const byName = new Map<string, PanelColumn>();
  for (const column of columns) {
    if (isNamed(column.name)) byName.set((column.name as string).trim(), column);
  }
  return byName;
}

/** The columns the panel ADDS, in the order they were authored. */
export function addedColumns(columns: PanelColumn[]): PanelColumn[] {
  return columns.filter((c) => !isNamed(c.name) && isNamed(c.title));
}

/**
 * Applies a hidden-column selection, keeping whatever else those columns say.
 * An entry that only ever said "hidden" is dropped rather than left behind as
 * `{ name }` with nothing on it.
 */
export function setHiddenColumns(columns: PanelColumn[], hidden: string[]): PanelColumn[] {
  const wanted = new Set(hidden);
  const next: PanelColumn[] = [];
  for (const column of columns) {
    // An added column names no query column, so hiding cannot apply to it.
    if (!isNamed(column.name)) {
      next.push(column);
      continue;
    }
    const name = (column.name as string).trim();
    if (wanted.delete(name)) {
      next.push({ ...column, visibility: 'hidden' });
      continue;
    }
    const { visibility: _hidden, ...rest } = column;
    if (rest.link || rest.title || rest.format) next.push(rest);
  }
  for (const name of wanted) next.push({ name, visibility: 'hidden' });
  return next;
}

/** Whether a column is finished enough to save: something to click, somewhere to go. */
export function isCompleteColumn(column: PanelColumn): boolean {
  // A settings-only entry (hide it, rename it, format it) needs no link.
  if (!column.link) return isNamed(column.name) || isNamed(column.title);
  if (!isSafeLinkUrl(column.link.url || '')) return false;
  return isNamed(column.name) || isNamed(column.title);
}

/**
 * Builds one cell's href, or null when it cannot be built — an unsafe template,
 * a column the query does not return, or a row whose value for it is empty.
 *
 * Null rather than a half-substituted path: `/investigate?id=` is a link that
 * looks live and lands nowhere, which is worse than an empty cell.
 */
export function renderRowUrl(url: string, columns: string[], row: (string | null)[]): string | null {
  const path = (url || '').trim();
  if (!isSafeLinkUrl(path)) return null;
  let missing = false;
  const rendered = path.replace(ROW_TOKEN_RE, (_match, name: string) => {
    const index = columns.indexOf(name);
    const value = index === -1 ? '' : row[index];
    if (!value) {
      missing = true;
      return '';
    }
    return encodeURIComponent(value);
  });
  return missing ? null : rendered;
}
