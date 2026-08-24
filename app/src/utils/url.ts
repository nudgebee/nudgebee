/**
 * URL helpers for workflow fields.
 *
 * `isFetchableHttpUrl` mirrors the syntactic half of `safehttp.ValidateURL`
 * in runbook-server (internal/tasks/safehttp/ssrf.go), which `mcp_task.go`
 * runs before fetching tools. The backend stays the authority — this exists
 * so the sidebar can suppress per-keystroke tool requests and display inline
 * URL validity indicators without round-tripping to the server.
 */

/**
 * Workflow input fields accept Jinja/Go templates (e.g. `{{ Inputs.mcp_url }}`).
 * Templated values cannot be validated at design time and should be treated as
 * neither valid nor invalid in UI indicators.
 */
export const isTemplateString = (v?: string | null): boolean => {
  if (!v || typeof v !== 'string') return false;
  return /\{\{|\{%/.test(v);
};

/**
 * Returns true if the value parses as a valid URL via `new URL(v)`.
 * Works for any valid scheme (e.g. `http://`, `https://`, `socks5://`).
 */
export const isValidUrl = (v?: string | null): boolean => {
  if (!v || typeof v !== 'string' || !v.trim()) return false;
  try {
    new URL(v);
    return true;
  } catch {
    return false;
  }
};

/**
 * Checks if the value is a valid HTTP/HTTPS URL with a non-empty hostname.
 * Used as a gate for direct-mode MCP tool listing to avoid fetching on
 * partial/invalid URLs or non-HTTP schemes (e.g. `socks5://`).
 */
export const isFetchableHttpUrl = (v?: string | null): boolean => {
  if (!v || typeof v !== 'string' || !v.trim() || isTemplateString(v)) return false;
  try {
    const parsed = new URL(v);
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && !!parsed.hostname;
  } catch {
    return false;
  }
};

/**
 * Returns true if the schema field name represents a URL or endpoint field.
 */
export const isUrlFieldName = (name?: string | null): boolean => {
  if (!name || typeof name !== 'string') return false;
  const lower = name.toLowerCase();
  return lower.includes('url') || lower.includes('endpoint');
};

/**
 * Computes the inline validity status for a URL field.
 * Returns `undefined` for blank values or template strings (suppresses indicators),
 * `'valid'` if the URL parses cleanly, and `'invalid'` otherwise.
 */
export const urlFieldStatus = (v?: string | null): 'valid' | 'invalid' | undefined => {
  if (!v || typeof v !== 'string' || !v.trim() || isTemplateString(v)) {
    return undefined;
  }
  return isValidUrl(v) ? 'valid' : 'invalid';
};
