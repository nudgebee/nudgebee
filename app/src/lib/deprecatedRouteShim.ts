import type { NextApiHandler, NextApiRequest, NextApiResponse } from 'next';

/**
 * Wraps a Next.js API handler in a deprecation layer.
 *
 * Use for backward-compat URL shims when an endpoint has moved. The shim:
 *  1. Sets `Deprecation: true` response header (RFC 9745 draft) so any
 *     well-behaved client can surface the warning.
 *  2. Sets `Link: <new-url>; rel="successor-version"` (RFC 8594) pointing
 *     at the canonical replacement so clients can auto-discover it.
 *  3. Emits a `console.warn` on every call — ops can grep logs for
 *     `[deprecated-endpoint]` to confirm a shim is safe to remove.
 *  4. Forwards the request to the new handler (no redirect — preserves
 *     POST bodies, headers, OAuth `code`/`state` query params, etc.).
 *
 * The shim is intentionally a thin wrapper, NOT an HTTP redirect:
 *   - POST callbacks (some marketplace webhooks) would lose their body on
 *     a 302/303 redirect.
 *   - OAuth providers (Slack/Google/MS Teams/GitHub) sometimes treat a
 *     302 from the redirect_uri as a failure.
 *
 * @param handler — the new canonical handler this shim forwards to
 * @param newUrl  — the canonical URL (used in Link header + warn log)
 */
export function createDeprecatedShim(handler: NextApiHandler, newUrl: string): NextApiHandler {
  return (req: NextApiRequest, res: NextApiResponse) => {
    res.setHeader('Deprecation', 'true');
    res.setHeader('Link', `<${newUrl}>; rel="successor-version"`);
    // Single-line log so ops can grep + count occurrences over time.
    // Once this is silent for ~30 days, the shim file can be deleted.
    console.warn(`[deprecated-endpoint] ${req.method} ${req.url} → ${newUrl}`);
    return handler(req, res);
  };
}
