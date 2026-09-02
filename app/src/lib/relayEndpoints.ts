// Allowlist of known relay-server endpoints reachable through the single-segment
// proxy at `/api/proxy/relay/[relay]`. The dynamic `[relay]` segment is interpolated
// into the upstream fetch URL, so it must be validated against this set to prevent
// forwarding to arbitrary paths (#28584).
//
// Only POST endpoints the app actually targets are allowed:
//   - `request` / `grafana` — used by `hitRelayServer` (@lib/HttpService).
//   - `ws` — used by the pod terminal (XtermTerminal). Despite the name this is a
//     plain JSON POST route upstream (relay-server `router.go`: `r.POST("/ws", ...)`,
//     whose handler does `c.BindJSON`), NOT a WebSocket upgrade; the terminal drives
//     the session by polling it with `action: start | exec | read | close`. Removing
//     it on the assumption that a `/ws` route must be an upgrade broke the pod shell
//     on every environment — see #36589.
//
// Lives here rather than beside the route because everything under `src/pages/` is
// compiled as a Next.js route, so the allowlist could not otherwise be unit-tested.
// `src/lib/__tests__/relayEndpoints.test.ts` asserts it stays a superset of the paths
// the app actually targets — keep that test in sync when adding a relay caller.
export const ALLOWED_RELAY_ENDPOINTS = new Set(['request', 'grafana', 'ws']);
