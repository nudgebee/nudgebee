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

## Priming poll

`watch_resource` runs the source **once, synchronously, under the caller's own
security context** before any row is written (`watch.Prime`, bounded by
`LLM_SERVER_WATCH_PRIMING_POLL_TIMEOUT_SEC`, default 15s — matching the poll budget, because it runs the same command; set to 0 to disable).
Three outcomes:

| Priming result | What happens | Why |
|---|---|---|
| Predicate already met | No watch is created; the tool returns `{"status":"ALREADY_MET","watch_id":null,...}` | Registering a background job to rediscover a finished state costs the user a poll interval of silence for an answer the agent already has. |
| `observe` failed, not a timeout | No watch is created; the error is returned to the agent | **Every** FAILED watch in the dev metastore is an `observe failed: ...` — a tool not enabled for the account, an expired GitHub App token, an agent-style tool that cannot poll. The agent can correct the call in the same turn instead of the user hearing about it three polls later, in a turn that ended long ago. |
| Timed out, or the predicate errored | The watch is registered normally, reason logged | A slow command is not a broken one, and an `llm_judge` failure is usually a transient model call. Neither should block registration. |

Priming runs `Source.ValidateConfig` **before** it runs anything. That gate is
not a formatting check — for `tool` sources it carries the agent-tool reject and
the destructive-verb deny-list (`kubectl delete`, `rm -rf`, `helm uninstall`, …).
`Manager.Create` used to be the only caller, which meant a mutating source was
refused without ever executing; since priming executes the command for real,
skipping the gate would turn a rejected registration into one live
`kubectl delete`. Pinned by `TestPrime_MutatingCommandIsRejectedWithoutExecuting`.

Priming is otherwise read-only: it writes nothing, and its synthetic watch ID
keeps the probe out of the user's conversation exactly as a dispatcher poll does.
It runs under the **caller's** security context rather than the dispatcher's
static admin context, so registration reflects what the requesting user can
actually do. Accepted asymmetry: a caller narrower than that admin context can
fail a probe whose real polls would have succeeded. Rejecting too much beats
validating a command the user cannot run and then driving it unattended. The
idea is borrowed from gemini-cli's `run_shell_command`, which races the process
against a short delay and only backgrounds it if it is still running.

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
| `regex`      | Go regex, compiled with `(?m)` | met = pattern located a match (an empty match counts) |
| `substring`  | substring           | met = `strings.Contains(observation, expr)`      |
| `llm_judge`  | yes/no question     | met = LLM returns `{"met": true, "summary": ...}` |

#### Regex predicates are line-anchored

`regex` expressions are compiled as `(?m)` + expr, so `^` and `$` anchor to
line boundaries rather than to the whole observation. This reverses the
original "never enable multiline under the user" rule, on production
evidence: agents are instructed by
`prompts/default/v1/_fragments/async_completion_rules.yaml` to write
predicates like `^0$`, but Go's `$` without `(?m)` matches only at
end-of-text, so `^0$` could never match a real, newline-terminated `0`
observation. A dev watch polling `gh run list … | wc -l` ran 45 polls with
zero failures and expired.

Trade-off accepted: `^done$` now matches a `done` line anywhere in a
multi-line observation. Anchors still discriminate — `^done$` does not match
`not done yet`.

A match is decided by whether the pattern *located* a match, not by the
matched text being non-empty. `^$` is therefore a usable "wait until this
command prints nothing" predicate. The cost is that a pathological
always-empty-matching pattern (`a*`, `.*`) is met on the first poll; that is
the documented contract and such a predicate is broken regardless.

#### Observation normalization

The `*_execute` tools do not agree on a wire shape: `github_execute` returns
raw stdout, while `shell_execute` and `kubectl_execute` wrap it in a JSON
envelope (`{"stdout":"…","cluster":"…"}`) whose newlines are
backslash-escaped. Predicates are written against the command's output, never
the envelope, so `toolSource.Observe` normalizes before the predicate runs
(`normalizeToolObservation`):

1. CRLF → LF — Go's `(?m)$` matches before `\n` but not before `\r\n`.
2. If the payload is a JSON **object** with a string `stdout` key, unwrap to
   that string. Raw output that merely happens to be JSON (e.g.
   `gh … --json`) has no `stdout` key and is left untouched.
3. Trim surrounding whitespace.

The normalized text is what gets stored in `last_poll_result`, so the row
shows what the predicate actually saw.

`predicate_negate=true` inverts `met`. Useful for "wait until X is
gone" semantics with `regex` / `substring`.

### Observations are untrusted input

Both prompts that see raw observation text — the `llm_judge` predicate and the
terminal summarizer — wrap it with `watch.WrapUntrusted`, which frames it in
`<untrusted_observation>` tags and escapes any closing tag inside the payload.

An observation is not operator-authored text. It is whatever the polled command
printed, and parts of that are routinely controlled by other people: pod names
and annotations in `kubectl get` output, workflow names from a repository,
application log lines echoing user input. The judge's answer decides whether a
watch terminates, and the summarizer's output is appended verbatim to the
user's chat thread, so injected text in either one makes the watch lie about a
mutating action's outcome. The previous delimiter was a markdown rule (`---`),
which any such output can forge.

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

### Routing back to a chat thread

`notifications-server` routes `/llm/response` by the **chat session key**
(Slack `<channel>-<thread_ts>`, Teams `a:`/`19:` ids, Google Chat
`spaces/…`), not by the internal conversation UUID — a UUID fails its
`_parse_conversation` and 404s. The watch therefore stores the session it
was registered from in `llm_watch_tasks.notify_session` (captured from the
tool-call context at create time) and sends that as `conversation_id` /
`session_id`. Legacy rows with a NULL column fall back to deriving it from
`llm_conversations.session_id`. Web-originated watches are delivered by the
responder (in-thread DB append) only — the notifier **skips the webhook
entirely** when the resolved session is not chat-routable.
`notifications-server`'s `_parse_conversation` splits the id on `-` and 404s
anything that is not exactly two parts, so posting a conversation UUID was a
guaranteed failed delivery that logged an ERROR on every healthy web watch
(the majority of traffic). A 404 from a *routable* chat session is still a
genuine delivery failure and is still returned and logged.

The POST also carries `X-ACTION-TOKEN` when `NOTIFICATION_SERVER_TOKEN` is
set — the endpoint is guarded by `verify_action_token`.

### In-progress indicator (chat only)

On registration, a chat-routable watch fires a `type: "watch_registered"`
signal carrying `watch_id`. `notifications-server` claims the slot atomically
(`SET NX`, keyed by `watch_id`, 25h TTL) but does **not** post yet — that
callback fires mid-turn, ~20s before the agent's answer reaches the thread, so
posting there would strand "🔭 Watching…" above the message it refers to. The
turn's own `final` posts it, so it lands as the last word.

The terminal `final` — which carries the same `watch_id` — posts the result as
a **new** message and then deletes the placeholder. A `chat.update` fires no
Slack notification, so editing in place would resolve silently in a thread the
user had stopped reading; the delete happens only after the post lands, since
delete-then-failed-post would leave them with nothing. A missing/`PENDING` ts
just means there is no placeholder to retire.

### Cancellation

`Manager.Cancel` is a bare UPDATE and never runs `Executor.terminate`, so it
delivers nothing on its own. Both cancel paths (the `watch_cancel` tool and
`POST /v1/watches/cancel`) therefore call `Manager.DeliverCancelled` after a
successful cancel, which runs the same responder + notifier pair with
`StatusCancelled`. Without it a cancelled watch left its "🔭 Watching…"
indicator in the thread promising a result that would never arrive.

The watch row's `notify_template` (optional) is rendered with a single
substitution: `{summary}` → predicate summary. When unset, a canonical
default per status is used.

> **Open question** — `type: "final"` is overloaded with normal agent
> replies. A distinct `type: "watch_complete"` would let the
> notifications-server render watches differently. Deferred.

## Isolation: polls do not pollute conversations

Polls run under a fresh `*security.RequestContext` constructed by
`buildWatchSecurityContext` in `watch/bootstrap/watch_bootstrap.go`. The
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
| `LLM_SERVER_WATCH_PRIMING_POLL_TIMEOUT_SEC`      | 15      | Budget for the synchronous priming poll; 0 disables   |
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
- `notify_session text` (V879) — the chat routing key captured at
  registration. Nullable: legacy rows and web watches leave it NULL.
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

- **First scheduled poll is a full interval away.** `Manager.Create` sets
  `next_poll_at = now + poll_interval_sec` (plus up to
  `WatchDispatcherIntervalSec`) — verified on a live dev watch created at
  14:52:29 with `next_poll_at` 14:52:59. The priming poll below covers the
  "already done" and "misconfigured" cases at registration; a watch that is
  genuinely still in progress does wait out its first interval.
- **`NewNbToolContext` makes a DB call per poll** to resolve tool
  configs. Cheap individually, costly at scale. Cache.
- **Predicate is recompiled per poll** (regex/substring are
  microseconds, fine; jq when added would benefit from caching).
- **`type: "final"` is overloaded** with regular agent replies — see
  open question above.
- **Terminal status is observable before the follow-up lands.** `terminate`
  commits `MarkTerminal` first, then spends up to `WatchSummarizerTimeoutSec`
  (default 30s) on the summarizer before the responder appends the block — so
  in that window a watch reads `COMPLETED` while its in-thread follow-up does
  not exist yet. The ordering is deliberate: `MarkTerminal` is the claim that
  stops a duplicate dispatch (the dispatcher has no in-flight guard, and
  `WatchPollTimeoutSec` > `WatchDispatcherIntervalSec`) from paying for a
  second summarizer call. Consequence: **clients must not treat terminal
  status as delivery.** The chat UI polls for the responder's
  `<!-- watch-update:<id> -->` marker instead
  (`app/src/components/llm/utils/watchFollowup.js`). A `follow_up_appended_at`
  column would let clients read the delivered state directly, at the cost of a
  migration.
- **Poll accounting on terminal transitions** runs through
  `MarkTerminal(TerminalUpdate{…, Polled, Observation})`: a poll that ends the
  watch increments `poll_count` and persists its observation to
  `last_poll_result`. `TerminalUpdate.Observation` is a `*string`, so an empty
  observation (the `^$` "wait until this prints nothing" case) is recorded as
  the evidence rather than being mistaken for "no observation taken"; nil means
  `Source.Observe` never returned, and the last real observation is left in
  place for post-mortem. Expiry short-circuits before `Source.Observe`, so it
  sets neither. Cancellation is a user action, not a poll, and touches no
  counters. Before this, a watch that completed on its first poll reported
  `poll_count = 0` with an empty `last_poll_result`.
- **Time is read directly from `time.Now()`**, not from an injected
  clock. Tests of expiry / backoff should plan around this.
- **No admin escape hatch** to bulk-cancel pending watches when
  flipping `LLM_SERVER_WATCH_ENABLED=false`. Operator workaround:
  `UPDATE llm_watch_tasks SET status='CANCELLED' WHERE status IN
  ('PENDING','ACTIVE')`.
