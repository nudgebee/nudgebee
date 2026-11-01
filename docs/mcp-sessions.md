# MCP Sessions — Service Guide

How Nudgebee opens, reuses and closes sessions on customers' MCP servers, and why.
Tracking issue: #39634.

**The central idea:** a stateful MCP server (a browser, a shell, a database
session) keeps state per *MCP session*. Each Nudgebee **chat** gets its own
session, and so does each **automation run**. A session is never shared between
chats or runs, and it is never silently swapped for a fresh one while the chat or
run is still using it. The relay keeps sessions for direct-mode integrations;
forager keeps them for agent-proxied ones.

> **Status.** Sections 1–4 describe the code. Section 5 is the agreed design for
> the next change and is marked _(planned)_ until it lands.
> Update this file in the same PR as any change to MCP session handling.

---

## 1. Where MCP calls go

| Caller | Mode | Path | Session key sent |
|---|---|---|---|
| llm-server MCP integration tools (`tools/core/tool_mcp_integration.go`) | direct | llm-server → relay `/request` (`mcp_request`, `connection_mode: direct`) → MCP server | `session_id` = the chat's `SessionId` (falls back to `ConversationId`). Tool discovery (`tools/list`) sends none and shares one session per account. |
| llm-server MCP integration tools | agent-proxied (`vm_agent`) | llm-server → relay → RabbitMQ → **forager** on the customer's host → MCP server | same `session_id`, forwarded to forager |
| runbook-server MCP task, integration | direct / agent-proxied | runbook-server → relay (same as above) | `session_id` = the workflow run id |
| runbook-server MCP task, raw URL | — | runbook-server → MCP server, directly | none. One session per task, closed with `DELETE` when the task ends |
| llm-server custom MCP tool (`tools/core/tool_custom_mcp.go`) | — | llm-server → MCP server, directly | none. A new client per call, closed after the call |

The relay handles direct-mode MCP **itself**, before it checks for any agent
(`relay-server/pkg/server/handlers/request.go` → `handleDirectMCP`). No k8s agent
is needed; llm-server marks these calls `NoAgent` so its own agent check skips them.

## 2. What an MCP session is

- It starts with the `initialize` handshake. The server answers with an
  `Mcp-Session-Id` header, the client sends `notifications/initialized`, and every
  later request carries that header. The session ends with an HTTP `DELETE` that
  carries the header.
- A session id is **just a string the server issued**. Any client that has the id
  and the server's credentials can continue the session or end it. Nothing ties it
  to a TCP connection or to one relay pod. (Agents are different: an agent's
  websocket lives on one relay pod, which is why agent requests are routed through
  RabbitMQ.)
- Some servers are **sessionless**: they issue no id. Nudgebee remembers that, so
  it does not repeat the handshake on every call.
- The server forgets a session when it restarts or hits its own timeout. It then
  answers **404** for that id. A **400** is *not* treated as "session gone",
  because tool errors can carry that text.

## 3. Lifecycle

**Relay, direct mode** (`relay-server/pkg/server/handlers/`: `mcp_direct.go` for
the protocol, `mcp_session.go` for the lifecycle, `mcp_session_store*.go` for the
store). Sessions are keyed by account, caller session id and MCP server URL, in a
session store: Redis shared by all relay pods by default, or per-pod memory for
installs without Redis (Section 4).

| Event | Behaviour |
|---|---|
| First call for a key | `initialize`; cache the id, or a sessionless marker. If two requests race, one session wins and the other is `DELETE`d. |
| Later calls | Reuse the id. Every use pushes the **30-minute idle** expiry out again. |
| Upstream 404 | Drop the entry (only if it still holds the failed id), re-initialize, retry once. |
| Sessionless entry, call rejected 400/404 | Drop the marker, so the next call re-initializes (the server may now need sessions). |
| Idle 30 min | A sweeper (every 5 min) claims the entry and sends a best-effort `DELETE`. At most 8 run at once. |

**Forager, agent-proxied** (`nudgebee/forager`, `pkg/proxy/mcp/session.go`). The
same rules, per datasource. Sessions are also `DELETE`d when the proxy closes. The
stdio transport is not isolated: one process is shared by all callers, which is a
documented limitation.

**runbook-server raw-URL tasks.** One session per task, `DELETE`d when the task
returns, including on error.

### Multiple relay pods

Medium and large installs run **2 relay pods** with no session affinity (also 2
llm-server and 2 workflow-server pods). With the per-pod memory store, call 1 of a
chat can open a browser on pod A while call 2 lands on pod B, which opens a second
session, and the chat silently loses its browser state. The relay therefore uses
the Redis store by default (Section 4). Single-pod installs (the SaaS default) are
unaffected either way.

## 4. Multiple relay pods: shared session directory

**Redis becomes the shared directory of sessions.** Any pod can continue or close
any session. Credentials are **not** stored in Redis.

- **Data**
  - One Redis hash per caller session: `mcp:sess:{account}:{session_id}`.
    Field = MCP server URL; value = `{mcp_session_id, integration_id}`.
  - One sorted set `mcp:sess:lastused`. Member = JSON
    `{"a":account,"s":session_id,"u":url}` (URLs contain `:`); score = last-used
    time. Tool-discovery sessions (empty `session_id`) are included.
  - Hashes get an `EXPIRE` of 45 minutes (idle timeout plus three sweep
    intervals), refreshed on use. That is only a safety net, sized so a slow
    sweep still finds the record; closing is done by the sweeper.
- **Callers send `integration_id`** on direct-mode calls (llm-server and
  runbook-server, one field each), including tool discovery. It is how a pod that
  never saw the original request gets the credentials to close the session.
- **A dedicated session-store interface** (get, store-if-absent, mark used,
  compare-and-delete, claim idle) has two implementations: Redis, which talks to
  Redis directly because it needs hashes, sorted sets and scripts, and per-pod
  memory. The tests run the shared behaviour against both (Redis via miniredis).
- **Every call**
  1. Read the hash field.
  2. If it is present, use it. Otherwise `initialize` and store the result with a
     small Lua script: "set if absent, or replace a sessionless marker with a real
     session". The loser of a race uses the winner's id and `DELETE`s its own.
  3. On success, update the last-used score.
  4. On a 404, a Lua compare-and-delete removes the entry only if it still holds
     the failed id; then re-initialize and retry once, as today.
- **Idle close**
  1. Every few minutes, each pod reads entries idle for more than 30 minutes from
     the sorted set, and **claims** each one by removing it (`ZREM` is atomic, so
     exactly one pod wins).
  2. The winner removes the hash field first (compare-and-delete), so no later call
     can reuse a session that is about to be closed.
  3. It loads the integration's credentials from Postgres (`pkg/db/db_mcp_auth.go`)
     with a **loader for direct-mode integrations**, and falls back to this pod's
     in-memory copy of the last request's credentials only when that load is not
     possible (no `integration_id`, e.g. during a rollout). The relay's existing loader
     (`QueryProxyDatasources`) covers only agent-proxied ones. The new loader:
     - checks that the integration belongs to the same account, and that its
       configured URL matches the stored URL;
     - builds auth from the **stored** `auth_type`, not from what the caller sent;
       runbook-server rewrites OAuth2 to a bearer token before calling the relay.
  4. It sends the `DELETE`, with an OAuth2 exchange when needed.

  A sent `DELETE` is logged at info level (`MCP session ended upstream`, with the
  status; a 404 means the server had already ended it). A credential, decrypt or
  `DELETE` failure is logged at error level and counted in
  `nb_relay_mcp_session_close_failures_total` (by reason); the MCP server's own
  timeout is the backstop.

  This keeps working through relay restarts and rollouts.
- **Store selection is relay-only.** A relay setting, `MCP_SESSION_STORE`
  (`redis` | `memory`, default `redis`), is set in the relay chart, not in the
  shared secret. Four other services read the shared `CACHE_PROVIDER`, and the
  Redis host there is always set. Only an install without Redis sets `memory`,
  and it must then run a single relay pod.
- **Redis error on a call.** Fall back to the pod's memory store, and skip Redis
  for the next 10 seconds so an outage costs one timeout rather than one per
  call. Log at error level and count it in `nb_relay_mcp_session_store_fallbacks_total`.
  While Redis is down, a multi-pod relay behaves like the per-pod store; sessions
  opened then are still swept. The relay logs which store it uses at startup.

## 5. Automation runs _(planned, PR 2)_

- **One session id per run.** AI steps, integration MCP tasks and raw-URL MCP tasks
  all send `session_id = "wf__<workflowId>__<runId>"`. AI steps already use this
  string for the default execution scope (`runbook-server/internal/tasks/ai/workflow_trace.go`).
  Steps with workflow scope (`wf__<workflowId>`) keep sharing across runs and are
  never closed at run end.
- **Raw-URL MCP tasks go through the relay**, so they share the run's session. The
  relay's MCP client is brought up to the raw-URL client's standard first:
  - per-event SSE parsing that matches the reply id, with a large buffer;
  - `notifications/initialized`;
  - the task's timeout;
  - an indication of an unparseable reply, so runbook-server keeps its
    don't-retry behaviour;
  - task headers passed through and never logged.

  Raw-URL sessions have no integration id, so how they are closed is decided in
  that PR.
- **Close at run end.** A new relay action, `mcp_close_sessions {session_ids}`,
  handled before the agent check. It closes every MCP server's session under those
  ids, using the Section 4 machinery. runbook-server calls it from an activity when
  a run completes, fails or is cancelled. The call is made for parent runs and for
  child workflows, which have their own run id.
- **Deploy order.** A runbook-server flag, default off, enables both raw-URL
  routing and the run-end close. It is flipped on once the relay change is deployed
  everywhere.

## 6. Decisions and limitations

**Decided**
- Sessions are keyed by the caller's chat or run id, never shared per account.
  (Sharing per account caused #39634.)
- Only a 404 means "session gone".
- Credentials stay in Postgres and in request memory, not in Redis. Closing
  reloads them by integration id.

**Rejected**
- *Run sessions on the relay's per-pod memory.* Sharing and closing would silently
  work only some of the time on multi-pod installs.
- *Forwarding calls to the pod that opened the session* (as agent requests are
  routed). It isn't needed, because a session id is portable.
- *Storing encrypted credentials in Redis* so any pod could close sessions. It adds
  a new place where secrets live; reloading by integration id avoids that.

**Limitations**
- **Pod crash mid-close.** A pod that crashes after claiming an idle session but
  before sending its `DELETE` leaves that session to the MCP server's own timeout.
  The window is milliseconds; a lease-and-retry scheme was judged not worth the
  extra phase.
- **Use at the idle boundary.** A call that reads a session in the same
  milliseconds that the sweeper claims it (exactly 30 minutes idle) can have it
  closed under it; the next call opens a fresh session.
- **Idle reuse window.** A session idle past 30 minutes stays usable until the
  next sweep (up to 5 minutes later) instead of being replaced on the next call.
  Harmless: if the server already dropped it, the 404 path opens a new one.
- **Index race.** If one pod removes a session at the same moment another stores
  a new session for the same key, the new one can drop out of the idle index. It
  is then ended by the MCP server's own timeout, not by the relay. Closing the gap
  would need a multi-key script, which Redis Cluster does not allow.
- **Server forgets the session.** On a server restart, the next call silently gets
  a fresh session, and the state is lost without an error.
- **Cancel race.** A cancelled run's still-running step can re-open a session after
  the run-end close. That session is closed by the idle sweep.
- **Chat deletion.** Sessions are not closed when a chat is deleted; that is
  deferred.
- **Forager.** Agent-proxied sessions follow forager's own idle rules, and stdio is
  shared.

## 7. Testing

- Relay unit tests use a fake MCP server: `mcp_session_test.go` covers isolation,
  idle expiry, 404 recovery, sessionless servers and the init race.
- **Multi-pod check:** scale the relay to 2 pods on dev. In one chat on a browser
  MCP integration, alternate "navigate" and "snapshot" 10 times. Every snapshot
  must show the navigated page.
- **Isolation check:** two chats open different pages at the same time, and each
  sees only its own page.
- **Run check** _(PR 2)_: a workflow with a raw-URL navigate step, then an AI step
  using the same MCP integration, then a raw-URL snapshot step. All steps see the
  same page. After the run, the old session id gets a 404 from the server.
