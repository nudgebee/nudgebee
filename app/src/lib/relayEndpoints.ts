// Allowlist of known relay-server endpoints reachable through the single-segment
// proxy at `/api/proxy/relay/[relay]`. The dynamic `[relay]` segment is interpolated
// into the upstream fetch URL, so it must be validated against this set to prevent
// forwarding to arbitrary paths (#28584).
//
// Only POST endpoints the app actually targets are allowed:
//   - `ws` — used by the pod terminal (XtermTerminal). Despite the name this is a
//     plain JSON POST route upstream (relay-server `router.go`: `r.POST("/ws", ...)`,
//     whose handler does `c.BindJSON`), NOT a WebSocket upgrade; the terminal drives
//     the session by polling it with `action: start | exec | read | close`. Removing
//     it on the assumption that a `/ws` route must be an upgrade broke the pod shell
//     on every environment — see #36589.
//
// `request` and `grafana` were removed. Nothing in the app posted to them any more —
// live resources and workload logs go through the `relay_forward_request` RPC, which
// checks the role per action and builds the kubectl logs command server-side — and
// they forwarded any agent action without a per-action role check.
//
// Lives here rather than beside the route because everything under `src/pages/` is
// compiled as a Next.js route, so the allowlist could not otherwise be unit-tested.
// `src/lib/__tests__/relayEndpoints.test.ts` asserts it stays a superset of the paths
// the app actually targets — keep that test in sync when adding a relay caller.
export const ALLOWED_RELAY_ENDPOINTS = new Set(['ws']);

export type RelayAccountResolution = { ok: true; accountId: string | undefined } | { ok: false; error: string };

const presentAccountId = (value: unknown): string | undefined | null => {
  if (value === undefined || value === null || value === '') return undefined;
  return typeof value === 'string' ? value : null;
};

/**
 * Picks the account id the proxy must authorise for a relay call: exactly the field
 * the relay endpoint will act on, so the field that is authorised and the field
 * that is used are always the same one.
 *
 *   - `ws` binds `InteractiveShellRequest` and reads only top-level `account_id`
 *     (relay-server `handlers/ws.go`).
 *   - `request` reads `body.account_id`, falling back to top-level `account_id` when
 *     it is empty (relay-server `handlers/request.go`). `grafana` uses the same rule; relay
 *     `/grafana` itself reads the account from the `X-NB-ACCOUNT-ID` header, which this
 *     proxy does not forward, so such calls get a 400 from the relay.
 *
 * Both fields set to different values is rejected outright, as is a non-string id.
 */
export function resolveRelayAccountId(segment: string, body: any): RelayAccountResolution {
  const nested = presentAccountId(body?.body?.account_id);
  const top = presentAccountId(body?.account_id);
  if (nested === null || top === null) {
    return { ok: false, error: 'account_id must be a string' };
  }
  if (nested !== undefined && top !== undefined && nested !== top) {
    return { ok: false, error: 'body.account_id and account_id differ' };
  }
  return { ok: true, accountId: segment === 'ws' ? top : nested ?? top };
}

// Fields of relay-server `models.InteractiveShellRequest`, the struct `/ws` binds.
const WS_REQUEST_FIELDS = ['account_id', 'action', 'session_id', 'command', 'name', 'namespace', 'request_id', 'container'];

/**
 * The body to forward to the relay. For `ws` it is rebuilt from the
 * `InteractiveShellRequest` fields alone: Go's encoding/json (gin `BindJSON`) matches
 * keys case-insensitively and keeps the last match, so a forwarded
 * `{"account_id":"mine","ACCOUNT_ID":"other"}` would be authorised here as `mine` and
 * acted on there as `other`. `/ws` re-marshals that struct, so nothing else reaches
 * the agent anyway. Other endpoints read fields case-sensitively (gjson) and get the
 * body unchanged.
 */
export function relayForwardBody(segment: string, body: any): any {
  if (segment !== 'ws' || !body || typeof body !== 'object') return body;
  const out: Record<string, unknown> = {};
  for (const field of WS_REQUEST_FIELDS) {
    if (Object.prototype.hasOwnProperty.call(body, field)) out[field] = body[field];
  }
  return out;
}
