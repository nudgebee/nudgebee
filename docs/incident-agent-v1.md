# Incident agent v1 (#40734)

A person approves every change. NudgeBee investigates, proposes, runs the command a person confirmed, and checks what happened. This note covers what each part guarantees and where it lives.

| Part | Guarantee | Where |
|---|---|---|
| Run (PR 1) | A plan step is decided once, from any surface: the decision is recorded before its command is sent, through the executor main uses for its kind (kubectl, helm, argocd and shell through the relay, a cloud CLI in the workspace), and its outcome is recorded honestly (`Unknown` when it cannot be known) | `api/remediation_run.go`, `events/remediation_decisions.go` |
| Confirm (PR 2) | A decision counts only for the plan version, account and person it was made for; a step that runs after another runs only once that one succeeded; checks NudgeBee runs without asking are strict reads (kubectl, or a cloud CLI call its own rules classify as a read) | `events/remediation_decisions.go`, `api/remediation_run.go` |
| Follow-up (PR 3, 4) | After a run settles, a read-only turn in the alert's own conversation checks on it by itself (metrics, events and strict kubectl reads; no cloud CLI) and decides what comes next; a change that took effect is reported only after a second look (one re-check at 5 min, at least); a plan it writes needs its own approval | `api/remediation_followup.go`, `api/remediation_tools.go`, `agents/agent_remediation.go` |
| Chat | In the alert's conversation, on the web and in its Slack thread, a person reads the plan, asks for a different one, and has a step run, rolled back, declined or marked done after confirming, recorded like a button | `api/remediation_tools.go`, `notifications-server/.../services/events.py` |
| Slack | The plan is posted in the alert's thread (cause, fix, next, then one message per step with Run and Decline), decided as the clicker's Identity Sync user; each decision and each follow-up check's report follow in the thread. Posts are markdown, converted as an investigation's answer is. A plan lost to a notifications-server outage is sent again before the next step note | `api/remediation_slack.go`, `notifications-server/.../services/remediation_thread.py` |

Why each part is shaped as it is: the three #40734 entries in [`architecture-decisions.md`](architecture-decisions.md).

## Settings

The loop is off by default and turned on per tenant, for all of the tenant's accounts, by the `EVENT_REMEDIATION_LOOP` feature flag (Settings > Tenant Settings > Feature Flags). Only the tenant-wide row counts: an account row does not turn it on. Both services read the same flag.

| Setting | Where | Effect |
|---|---|---|
| `EVENT_REMEDIATION_LOOP` | tenant feature flag | The follow-up, the alert-plan chat tools, alert-thread chat in Slack, and the plan posted in the alert's Slack thread |
| `NOTIFICATION_SERVER_TOKEN` | shared `nudgebee` secret (llm-server and notifications-server both load it) | Needed for anything to be posted in Slack; without it nothing is sent |
| `LLM_SERVER_REMEDIATION_FOLLOWUP_MAX_RUNS` (3; 0 for no cap) | llm-server env | Plans of one alert in 24 hours whose runs get a follow-up and a proposal; every step of a counted plan is checked |

People who should Run from Slack need their Slack account linked in Identity Sync.

## Not covered yet

- In the alert's chat, agents keep main's tools: a person's approved `kubectl_execute` there still runs outside the plan's record, as on main. `run_step` is offered and its description steers the model to it, but nothing refuses the other path.
- A confirmation left unanswered in the alert's conversation keeps the follow-up from starting until it gives up for a person (2 hours).
- The Slack step messages show no live status: a step decided on the panel is noted in the thread, and its buttons stay; a later click is refused and only the clicker is told. A step's buttons end when it is decided from Slack or its plan is replaced.
- Durable fixes are not verified: the follow-up watches for about an hour (at most 2 re-checks of 30 min), and a raised fix PR gets no follow-up. A code or chart fix deployed later is not checked.
- The plan is built for every completed investigation of a flag-on tenant, not only for alerts a person sees: one plan-model call per investigation.
- The follow-up check reads no cloud CLI: on a cloud account it uses metrics and event tools only.
- No Teams cards: a Teams alert thread gets no plan and no Run button.
- Run sends each command once and never retries it, but does not guard against the relay's queue redelivering a request after a relay pod crash, as on main.
- `get_event_by_id` follows an event to its own account when the person can read that account.
