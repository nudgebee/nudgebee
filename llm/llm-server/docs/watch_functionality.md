# Watch Functionality

The watch package lets agents register a long-running "watch" — a periodic
observation of an external resource that notifies the user's conversation
when a termination condition is met or a max duration elapses.

## Why

Conversation turns in `llm-server` are synchronous: an agent plans →
executes tools → replies → stops. There was no mechanism for the agent to
keep working after the HTTP response was sent. Users asking "watch the CI
runs and ping me when they're done" had to be told no.

The watch feature is the smallest persistent-task primitive that solves
this without re-introducing the heaviest cost: re-running the full
ReAct loop on every poll. Polls are narrow tool calls; agents do not
re-enter the conversation per poll.

## Lifecycle

```
                   ┌──────────────────────────────────────┐
                   │  agent calls watch_resource tool    │
                   └────────────────┬─────────────────────┘
                                    │
                                    ▼
                ┌─────────────────────────────────────┐
                │ watch.Manager.Create                │
                │  - validate source/predicate        │
                │  - clamp interval / duration        │
                │  - tenant-quota check               │
                │  - INSERT row (status=PENDING)      │
                └────────────────┬────────────────────┘
                                 │
                                 ▼
                ┌──────────────────────────────────────┐
                │ leader-elected dispatcher (gocron)   │
                │  every WatchDispatcherIntervalSec    │
                │  - SELECT due rows                   │
                │  - submit each to worker pool        │
                └────────────────┬─────────────────────┘
                                 │
                                 ▼
                ┌──────────────────────────────────────┐
                │ executor.RunOnePoll                  │
                │  - check expiry  → EXPIRED + notify  │
                │  - source.Observe (tool / sql)       │
                │  - predicate.Eval                    │
                │  - met       → COMPLETED + notify    │
                │  - not met   → reschedule            │
                │  - error     → backoff, then FAILED  │
                └──────────────────────────────────────┘
```

## States

| State       | Meaning                                          | Terminal? |
|-------------|--------------------------------------------------|-----------|
| `PENDING`   | Row created, dispatcher hasn't picked it up yet  | no        |
| `ACTIVE`    | At least one poll completed                      | no        |
| `COMPLETED` | Predicate met; user notified                     | yes       |
| `EXPIRED`   | `max_duration_sec` elapsed; user notified        | yes       |
| `FAILED`    | N consecutive poll errors; user notified         | yes       |
| `CANCELLED` | Cancelled via API / agent                        | yes       |

## Sources

A `Source` produces an `Observation` for a single poll. The interface
intentionally allows new kinds without a schema migration —
`source_kind` + `source_config jsonb` is the entire on-disk shape.

### v1 sources

#### `tool`

Re-invokes a registered NBTool with stored arguments.

```json
{
  "tool_name":  "github",
  "tool_input": { "command": "run list", "args": { "limit": 20 } },
  "command":    "<optional override of NBToolCallRequest.Command>"
}
```

**Constraint:** the named tool MUST be idempotent and read-only. Watches
that re-run mutating operations are unsupported and will produce
undefined behaviour.

#### `sql`

Runs a SQL query against the metastore. Result rows are JSON-encoded as
the observation; the predicate sees `{"row_count": n, "rows": [...]}`.

```json
{
  "datasource": "metastore",
  "query":      "SELECT count(*) AS n FROM jobs WHERE status='failed'",
  "params":     [],
  "max_rows":   100
}
```

**Hard limits:**
- Only the metastore is allowed in v1 (`datasource` MUST be `"metastore"`).
- Only `SELECT` and `WITH ...` queries pass validation. Belt-and-suspenders
  against a misbehaving agent — not a substitute for running watches under
  a least-privileged DB role at the infra layer.

### Adding new sources (future)

Implement `Source` and `RegisterSource` from an `init()`. No schema or
DAO changes required. Candidates explicitly out of scope for v1:

- `timer` — one-shot at wall-clock time
- `http` — generic GET to an allowlisted URL
- `listen` — Postgres `LISTEN/NOTIFY` push (requires a long-lived
  goroutine + a wakeup signal flipping `next_poll_at = now()`)
- `webhook` — inbound `POST /v1/watches/:id/event` that delivers the
  observation directly
- `composite` — observes other watches' states (`AND` / `OR`)

## Predicates

A `Predicate` evaluates one observation and returns `(met, summary)`.

### v1 predicates

| Kind         | Expression          | Match semantics                                  |
|--------------|---------------------|--------------------------------------------------|
| `regex`      | Go regex pattern    | met = pattern matched (non-empty match)          |
| `substring`  | substring           | met = `strings.Contains(observation, expr)`      |
| `llm_judge`  | yes/no question     | met = LLM returns `{"met": true, "summary": ...}` |

`predicate_negate=true` inverts `met`. Useful for "wait until X is
gone" semantics with `regex` / `substring`.

### LLM judge contract

Strict-JSON prompt, no code fences, low temperature. The model must reply
with an object that has `met: bool` and `summary: string`. Anything else
is treated as a transient error and counts against `failure_count`. We
do NOT short-circuit termination on a parse failure.

### Why not jq

`jq` over JSON observations is the right primitive for the CI use case
and many DB-state cases. It's deferred to a follow-up purely to keep
this PR's dependency footprint minimal — adding it is an `init()` in
`predicate.go` and a `go get github.com/itchyny/gojq`. Schema is
forward-compatible (the migration's CHECK on `predicate_kind` is the
only place to update).

## Notifications

Exactly one notification per watch, on terminal transition. Uses the
existing `notification-server` `/llm/response` endpoint with `type: "final"`
so completed watches show up as a normal in-thread message. Per-poll noise
NEVER reaches the user's conversation.

The watch row's `notify_template` (optional) is rendered with a single
substitution: `{summary}` → predicate summary. When unset, a canonical
default per status is used.

> **Open question** — `type: "final"` is overloaded with normal agent
> replies. A distinct `type: "watch_complete"` would let the
> notifications-server render watches differently. Deferred.

## Isolation: polls do not pollute conversations

Polls run under a fresh `*security.RequestContext` constructed by
`buildWatchSecurityContext` in `agents/core/watch_bootstrap.go`. The
`conversation_id` on the watch row is the **notification destination**
only — polls themselves don't carry a conversation ID into the source
context, so the user's chat history is not appended on every tick.

## Tenancy and limits

- **Per-tenant cap** (`LLM_SERVER_WATCH_MAX_PER_TENANT`, default 20):
  enforced at create time. The cap is soft — concurrent creates can
  race past it by a handful of rows.
- **Min poll interval** (`LLM_SERVER_WATCH_MIN_INTERVAL_SEC`, default
  30s): clamped up at create time.
- **Max duration** (`LLM_SERVER_WATCH_MAX_DURATION_SEC`, default 24h):
  clamped down at create time. Hard ceiling.
- **Max consecutive failures** (`LLM_SERVER_WATCH_MAX_FAILURES`,
  default 3): after N source/predicate errors in a row, the watch
  transitions to `FAILED`. Failure count resets on a successful
  observation.
- **Backoff**: on transient failure, `next_poll_at = now() + interval *
  2^min(failure_count, 4)`.
- **Budget**: there is no explicit budget pre-check on every poll.
  Polls that hit the LLM (the `llm_judge` predicate) flow through the
  existing `agents/core` LLM client which already enforces the budget
  package's daily / monthly limits. Tool-only and SQL-only polls don't
  cost LLM tokens, so they don't count against budget.

## Resume across restarts

Watches are persisted; on `llm-server` restart the leader reattaches
to the existing `watch_dispatcher` job and resumes any rows in
`PENDING` / `ACTIVE`. There is no in-memory state on the executor
beyond the worker pool itself.

## Configuration

All keys default to safe values; the feature is gated by
`LLM_SERVER_WATCH_ENABLED=true`.

| Env var                                          | Default | What it does                                          |
|--------------------------------------------------|---------|-------------------------------------------------------|
| `LLM_SERVER_WATCH_ENABLED`                       | false   | Master gate for the whole feature                     |
| `LLM_SERVER_WATCH_DISPATCHER_INTERVAL_SEC`       | 10      | How often the leader checks for due watches           |
| `LLM_SERVER_WATCH_WORKER_COUNT`                  | 10      | Concurrent polls per process                          |
| `LLM_SERVER_WATCH_WORKER_QUEUE_SIZE`             | 50      | Backpressure: drop overflow back to next tick         |
| `LLM_SERVER_WATCH_MIN_INTERVAL_SEC`              | 30      | Floor on `poll_interval_sec`                          |
| `LLM_SERVER_WATCH_MAX_DURATION_SEC`              | 86400   | Ceiling on `max_duration_sec` (24h)                   |
| `LLM_SERVER_WATCH_MAX_PER_TENANT`                | 20      | Concurrent active watches per tenant                  |
| `LLM_SERVER_WATCH_MAX_FAILURES`                  | 3       | Consecutive failures before terminal `FAILED`         |
| `LLM_SERVER_WATCH_PRIMING_POLL_TIMEOUT_SEC`      | 5       | (reserved for v2 priming poll)                        |
| `LLM_SERVER_WATCH_SUBMIT_TIMEOUT_SEC`            | 5       | Worker pool submission timeout                        |
| `LLM_SERVER_WATCH_POLL_TIMEOUT_SEC`              | 60      | Per-poll cap                                          |
| `LLM_SERVER_WATCH_DISPATCH_BATCH_SIZE`           | 100     | Max rows fetched per dispatcher tick                  |

## Schema

```
api-server/hasura/migrations/app/1777138945815_V710_llm_watch_tasks/up.sql
```

Notable choices:
- `id`, `conversation_id`, `account_id`, `tenant_id`, `user_id` are
  `uuid` to match `llm_conversations`.
- `source_kind` + `source_config jsonb` instead of `tool_name` /
  `query` so new sources don't require a schema change.
- `conversation_id` has a `FK ... ON DELETE CASCADE` to
  `llm_conversations(id)` — deleting a conversation removes its
  watches.
- `CHECK` constraints on `status`, `source_kind`, `predicate_kind` are
  intentional: adding a new value is a tiny migration that catches
  typos at insert time.

Indexes:
- `(next_poll_at) WHERE status IN ('PENDING','ACTIVE')` — partial,
  the only index hot-pathed by the dispatcher
- `(account_id, status)` — for future per-account listing
- `(tenant_id, status)` — for tenant-quota count
- `(conversation_id)` — for cancellations and conversation listing

## Known limitations / follow-ups

- **No synchronous priming poll.** The first poll lands within
  `WatchDispatcherIntervalSec` (default 10s) of registration. Adding a
  priming poll path lets the tool fail fast on bad configs and
  short-circuits "already done" cases.
- **`NewNbToolContext` makes a DB call per poll** to resolve tool
  configs. Cheap individually, costly at scale. Cache.
- **Predicate is recompiled per poll** (regex/substring are
  microseconds, fine; jq when added would benefit from caching).
- **`type: "final"` is overloaded** with regular agent replies — see
  open question above.
- **Time is read directly from `time.Now()`**, not from an injected
  clock. Tests of expiry / backoff should plan around this.
- **No admin escape hatch** to bulk-cancel pending watches when
  flipping `LLM_SERVER_WATCH_ENABLED=false`. Operator workaround:
  `UPDATE llm_watch_tasks SET status='CANCELLED' WHERE status IN
  ('PENDING','ACTIVE')`.
