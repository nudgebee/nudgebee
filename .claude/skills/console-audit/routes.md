# Console-audit route & drilldown matrix

The `console-audit` skill reads this file. **Edit here to change coverage** — add a row to
audit a new tab, add a `### drilldown:` block to exercise an interaction.

**Preferred cluster: `k8s-dev`** — the default audit context (pinned by name; the id is resolved
live). Every non-`here` run switches the header cluster selector to this before auditing, so
findings stay comparable across runs (and so baseline/diff works). Override per-run with
`cluster=<name>`, or `cluster=current` to keep whatever you've manually selected. Change this
line to re-point the default for your environment.

Placeholders are filled at runtime from the live `/home` session (never hardcode ids):
- `{origin}` — base URL, default `http://localhost:3000`
- `{accountId}` — current account query param
- `{cloudDetailId}` — a cloud-account detail id (Cloud → detail view)
- `{clusterId}` — a kubernetes cluster id (Clusters list)

`waitFor` is a text string expected on the loaded tab, used to settle before collecting.
Leave it blank to use a fixed short settle.

## Tabs (L1 — load-time audit)

| id | url | waitFor |
|----|-----|---------|
| home | {origin}/home?accountId={accountId} | Home |
| troubleshoot | {origin}/troubleshoot?accountId={accountId} | Troubleshoot |
| troubleshoot-all-events | {origin}/troubleshoot?accountId={accountId}#all-events | Events |
| automations | {origin}/auto-pilot?accountId={accountId} | Automations |
| optimize | {origin}/optimise?accountId={accountId} | Optimize |
| clusters | {origin}/kubernetes?accountId={accountId} | Clusters |
| cloud | {origin}/cloud-account?accountId={accountId} | Cloud |
| tickets | {origin}/tickets?accountId={accountId} | Tickets |
| admin | {origin}/user-management?accountId={accountId} | Admin |
| ask-nudgebee | {origin}/ask-nudgebee?accountId={accountId} | Ask |
| agent-health | {origin}/agentHealth?accountId={accountId}#agent | Health |
| cloud-summary | {origin}/cloud-account/details/{cloudDetailId}#summary | Summary |
| cloud-services | {origin}/cloud-account/details/{cloudDetailId}#services | Services |
| cloud-traces | {origin}/cloud-account/details/{cloudDetailId}#monitoring/traces | Traces |
| cloud-logs | {origin}/cloud-account/details/{cloudDetailId}#monitoring/cloud-logs | Logs |
| cloud-events | {origin}/cloud-account/details/{cloudDetailId}#events/events | Events |
| cloud-rightsizing | {origin}/cloud-account/details/{cloudDetailId}#optimize/right-sizing | Right |
| cloud-ec2 | {origin}/cloud-account/details/{cloudDetailId}#ec2/summary | EC2 |
| cloud-rds | {origin}/cloud-account/details/{cloudDetailId}#rds/summary | RDS |
| cloud-s3 | {origin}/cloud-account/details/{cloudDetailId}#s3/summary | S3 |
| k8s-details | {origin}/kubernetes/details/{clusterId}#summary | Summary |
| k8s-monitoring-traces | {origin}/kubernetes/details/{clusterId}#monitoring/traces | Traces |
| k8s-troubleshoot | {origin}/kubernetes/details/{clusterId}#troubleshoot | Troubleshoot |
| k8s-optimize | {origin}/kubernetes/details/{clusterId}#optimize | Optimize |
| k8s-apps-infra | {origin}/kubernetes/details/{clusterId}#kubernetes/applications | Applications |

> The kubernetes details page lazy-loads ~46 tab components — add more `k8s-*`
> rows here as coverage needs grow.

## Drilldowns (L2 — interaction audit)

Each block: land on `tab`, then run the steps. Match targets by accessible name/text.
On a missing target, log `drilldown-skipped` and move on. **Read/expand only — never mutate.**

### drilldown: ask-nudgebee submit
- tab: ask-nudgebee
- steps:
  1. fill the message textbox ("How can I assist…") with `@aws can you get me active ec2 instances?`
  2. click **Send**
  3. wait_for text `Tool Details` (or `Waiting`)
  4. re-collect console/network — this exercises MessageStream / MessageItem / ConversationCollapsableCard / Tooltip / TextAreaV2

### drilldown: cloud right-sizing recommendation
- tab: cloud-rightsizing
- steps:
  1. take_snapshot; click the first recommendation row / **View Details**
  2. wait_for the drilldown panel (text `Description` or `Evidence`)
  3. switch the inner tab to **Description** / **Mitigation** if present
  4. re-collect console/network — exercises CloudOptimizeRecommendationsTable + CustomTable tabs + MarkDowns

### drilldown: k8s traces drilldown
- tab: k8s-monitoring-traces
- steps:
  1. take_snapshot; click the first trace/span row in the table
  2. wait_for the drilldown (text `Duration` or `Span`)
  3. re-collect console/network — exercises KubernetesTracesListing + the tab-value guard

### drilldown: troubleshoot triage drilldown
- tab: troubleshoot-all-events
- steps:
  1. take_snapshot; click the first event in the Triage/Events table
  2. wait_for the drilldown detail panel
  3. re-collect console/network
