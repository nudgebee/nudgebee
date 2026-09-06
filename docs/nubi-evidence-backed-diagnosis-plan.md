# Nubi evidence-backed diagnosis — delivery plan

Epic: https://github.com/nudgebee/nudgebee-enterprise/issues/37223
Prerequisite: https://github.com/nudgebee/nudgebee-enterprise/pull/37735

## Progress tracker — updated 2026-09-06

PR #37735 merged as `055fb974291a27638ea65a903a87844c1516f489` and was reported deployed to dev. PR #37762 fixed the two baseline regressions and merged as `623bd381ad4077ccaed5abf9ec6ac0fc57c6f6f3`. PR #37774 added normalized health fields and merged as `65e22d12d5e5b965fd70be21d2e37078ee7199d7`. Deployed Phase 2 testing confirmed the normalized contract for Kubernetes, VM/proxy, and OpenCost, then exposed remaining planner scope and agentless evidence-precedence failures. The current follow-up keeps the existing tool surface and tightens how every orchestrator consumes it.

- [x] Foundation: epic #37223 records #37222 as complete (existing issue status; not revalidated here).
- [x] Follow-up plan captured; dedicated worktree prepared.
- [x] PR #37735 merged and deployed to dev — merge verified; deployment reported by the user and exercised successfully through live tools.
- [x] Phase 1: all four baseline cases passed after #37762 was deployed.
- [ ] Phase 2: #37774 normalized Kubernetes and VM/proxy health. Current follow-up covers selected-account scope and normalized-field precedence. Authoritative agentless sync/freshness, VM datasource health, and safe error categorization remain pending.
- [ ] Phase 3: implement bounded product-to-environment handoff, scoped runtime investigation, and version-aware Helm guidance.
- [ ] Phase 4: trace missing alerts/notifications through the delivery pipeline, stopping where evidence ends.
- [ ] Phase 5: audit and complete installation, health, alerts, automation, and integration docs; verify retrieval and citations. Coordinate with #37299.
- [ ] Phase 6: add the ten reusable diagnostic runbooks with evidence, decision trees, and stopping conditions.
- [ ] Phase 7: introduce controlled remediation only after dependable diagnosis, with server-side RBAC, target resolution, confirmation, and audit evidence.
- [ ] Phase 8: establish permanent evaluations and expand them throughout all phases.

### How to resume and record completion

1. Verify #37735 is merged and deployed to dev; update this worktree from origin/main before implementation.
2. Run Phase 1 first. Record the deployed revision, sanitized nbctl commands/results, selected agents, tool calls/parameters, and final answers for every baseline case.
3. Deliver small reviewable PRs under #37223 in the sequence below. Perform the repository-required adversarial pass before implementation.
4. Check an item only after its acceptance criteria are demonstrated. Attach the implementation PR, merge/deployment revision, validation evidence, and remaining gaps; an open PR or green unit tests alone do not mean done.
5. Keep this tracker and epic #37223 synchronized. Preserve existing epic workstreams; this plan expands diagnosis and does not mark unrelated work complete.

| Completed item | PR / deployed revision | Evidence | Remaining gaps |
|---|---|---|---|
| Plan captured, 2026-09-05 | Planning only | Full supplied plan preserved below | All post-merge phases remain pending |
| PR #37735 merged and dev endpoint exercised, 2026-09-05 | `055fb974291a27638ea65a903a87844c1516f489` | Live Nudgebee tools introduced by the PR returned account-scoped integration and agent health | Deployment SHA was not independently exposed by `nbctl`; attribution combines the verified merge with the user's deployment report |
| Phase 1 routing and grounding fixed, 2026-09-06 | PR #37762, `623bd381ad4077ccaed5abf9ec6ac0fc57c6f6f3` | Post-deploy conversations: Datadog `b38592c0-3c9e-4d90-b318-f1e141ade4e2`; health `4701ff67-ec4a-4b1a-a398-7864bc056b8c`; Prometheus premise `788b552a-3a2a-4022-8cfc-45fc803b957a`; agent premise `5f8340e6-768c-480e-a575-dfd9564a44a4` | Phase 2 normalization and later diagnosis phases remain |
| Phase 2 health normalization, 2026-09-06 | PR #37774, `65e22d12d5e5b965fd70be21d2e37078ee7199d7` | Deployed matrices recorded in epic comments [`5556767726`](https://github.com/nudgebee/nudgebee-enterprise/issues/37223#issuecomment-5556767726) and [`5556785922`](https://github.com/nudgebee/nudgebee-enterprise/issues/37223#issuecomment-5556785922) | Planner scope, agentless evidence precedence, safe errors, sync/freshness, and VM datasource health remain |
| Baseline: exact Datadog integration | Conversation `63a21d03-d7be-4099-af6f-abf6193ee3c2` | Selected `k8s_orchestrator` -> `nudgebee`; exact-name lookup; diagnosis returned `disabled`, `not_supported`, `unknown`, and `CONNECTION_TEST_NOT_SUPPORTED` | Final answer treated `updated_at` as proof of when it was disabled; remove that unsupported temporal claim |
| Baseline: explicit Nubi agent health | Conversation `bc0b8dec-0781-4d24-9a5b-da245db26caa` | Selected only `nudgebee`; called `nudgebee_agent_health_get`; reported heartbeat and individual feature fields | `opencostConnection=false` plus `opencostServerSide=true` was described as disconnected; Phase 2 should normalize this as server-managed |
| Baseline: Prometheus disconnected | Conversation `0dd533f3-d37d-4c1c-9d65-f3160a6d19ab` | Delegated to `nudgebee` first; health evidence said the agent and Prometheus were connected | Final answer ignored the live connected state, presented generic disconnected causes, and suggested a mutating `kubectl run`; answer-grounding failed |
| Baseline: agent disconnected | Conversation `9055f061-cd7a-40f3-91f4-d515372b48d4` | Conversation and complete tool trace retained | Never selected `nudgebee`; assumed namespace `nudgebee-agent`, scanned unrelated namespaces/resources, fetched up to 5,000 log lines, and stopped `WAITING` on a broad shell confirmation |

### Phase 1 live-test verdict — 2026-09-05

| Case | Final answer | Agent selection | Tool/evidence | Scope and safety | Verdict |
|---|---|---|---|---|---|
| `datadog-dev-alert` | Correct status and unsupported-test classification; one unsupported date inference | Correct delegation | Correct exact integration and diagnosis calls | Read-only; no secret or URL exposure | Pass with minor claim fix |
| Explicit `@nubi` health | Correct heartbeat and feature inventory | Correct | Correct health call with no prompt-supplied scope | Read-only; no secret or URL exposure | Pass; OpenCost semantics deferred to Phase 2 |
| Prometheus disconnected | Contradicted current evidence by answering the hypothetical disconnected state | Correct initial delegation | Product health was authoritative and current; later generic docs/runtime evidence displaced it | Included a suggested pod-creating command despite diagnosis-only scope | Fail |
| Agent disconnected | No completed diagnosis | Incorrect: no Nudgebee agent | Environment inspection ran before product health and used a guessed namespace; investigation expanded across namespaces | No approved mutation, but the run waited on a broadly scoped shell confirmation | Fail |

Required fixes before Phase 2:

1. Bind the final synthesis to current product evidence. If the feature is connected, say so and treat the user's premise as unconfirmed.
2. Route generic Nudgebee-agent health questions through `nudgebee_agent_health_get` before any environment investigation.
3. Pass the returned installation namespace/account context into bounded environment checks; never guess `nudgebee-agent` or scan other namespaces.
4. Keep diagnosis read-only end to end. Do not propose or request approval for pod-creating verification commands during the baseline.
5. Treat timestamps only according to their field contract; `updated_at` does not prove when an integration entered its current status.

## Detailed scope and acceptance criteria

After PR #37735, the plan is to move from basic self-awareness into complete, evidence-backed diagnosis. The order should be: validate the foundation, improve diagnostic depth, close documentation gaps, then add controlled remediation.

## Phase 1 — Validate PR #37735 in dev

Once merged and deployed, rerun the same baseline matrix:

| Question | Expected behavior |
|---|---|
| Why is `datadog-dev-alert` not connected? | Finds exact integration; reports disabled; reports connection test as unsupported/unknown. |
| `@nubi` check Nudgebee K8s agent health | Calls `nudgebee_agent_health_get`; reports heartbeat and individual features. |
| Why is Prometheus disconnected in Nudgebee? | Default orchestrator delegates product health to Nudgebee, then optionally investigates Kubernetes. |
| Why is my Nudgebee agent disconnected? | Checks platform health first; does not conclude from ready pods alone. |

Validation must check:

- Final answer correctness.
- Selected agents.
- Tool calls and parameters.
- Returned evidence.
- Absence of secrets/internal URLs.
- Absence of unsupported claims.
- No mutations during diagnosis.

Any discovered routing or interpretation issue should be fixed before expanding the feature.

---

## Phase 2 — Improve agent and feature-health diagnosis

The first health tool provides authoritative raw facts. Next, Nubi needs to interpret them correctly.

### 2.1 Normalize deployment models

Nubi should recognize:

- **Kubernetes agent**
  - Agent heartbeat.
  - Relay.
  - Prometheus.
  - Alertmanager.
  - Logs.
  - Traces.
  - Node agents.
  - OpenCost.
  - Agent version and K8s details.

- **VM/proxy agent**
  - Proxy heartbeat.
  - Configured data sources.
  - Per-data-source connectivity.
  - Collection freshness.
  - Version and recent failures.

- **Agentless AWS/Azure/GCP**
  - Must not describe this as an installed agent.
  - Account credential status.
  - Events, spend, resource and recommendation sync.
  - Last successful sync and next scheduled sync.
  - Per-feature errors and stale data.

### 2.2 Compute an explicit health verdict

Avoid making the LLM derive everything from raw fields. Ideally, the tool should return:

```json
{
  "deployment_model": "kubernetes_agent",
  "overall_health": "degraded",
  "heartbeat": {
    "status": "connected",
    "last_connected_at": "..."
  },
  "features": {
    "prometheus": {
      "status": "disconnected",
      "reason_code": "AUTHENTICATION_FAILED"
    },
    "logs": {
      "status": "connected"
    }
  }
}
```

Useful verdicts:

- `healthy`
- `degraded`
- `disconnected`
- `stale`
- `unknown`
- `not_configured`
- `server_managed`

### 2.3 Normalize feature errors

Raw agent errors might expose URLs or credentials and are difficult for Nubi to interpret. Convert them into safe categories:

- Authentication failed.
- Authorization/permission failure.
- DNS failure.
- TLS/certificate failure.
- Endpoint unreachable.
- Timeout.
- Invalid configuration.
- Provider unavailable.
- No recent data.
- Unknown safe failure.

Return sanitized summaries and recommended checks, not raw provider payloads.

---

## Phase 3 — Add bounded runtime investigation

Nudgebee health establishes the product-side symptom. Environment agents should then investigate why it happened.

### 3.1 Delegation contract

For:

> Why is Prometheus disconnected in Nudgebee?

Every environment orchestrator that exposes the Nudgebee agent should share one product-health gate. The K8s, AWS, GCP and Azure orchestrators should:

1. Call the Nudgebee agent for authoritative feature health.
2. Use the returned account, feature and safe reason.
3. Delegate or inspect only the relevant Kubernetes runtime, using the returned account and installation namespace.
4. Correlate both evidence sets.
5. Return one combined answer.

Nudgebee answers:

> What does Nudgebee currently report?

The environment orchestrator answers:

> What is happening in the customer environment that could explain it?

### 3.2 Kubernetes investigation

Possible evidence:

- Relevant Nudgebee agent workloads.
- Restart count and readiness.
- Recent warning events.
- Service/endpoints availability.
- Configuration presence—not secret values.
- DNS connectivity.
- TLS failures.
- Agent/forwarder logs with secret filtering.
- Installed chart and application version.
- Expected versus actual replicas.
- Required service-account permissions.

The investigation must stay scoped to the account/agent being diagnosed. It should not scan every namespace or dump broad integration lists.

### 3.3 Helm awareness

Add version-aware knowledge for:

- Nudgebee Kubernetes agent chart.
- VM agent installation where applicable.
- Required values for Prometheus, logs, traces, Alertmanager and relay.
- Upgrade and compatibility rules.
- Common invalid configuration combinations.

This should primarily live in curated documentation/runbooks, not as another broad tool exposing raw Helm values.

Platform/operator chart documentation should remain separate from tenant-facing agent documentation.

---

## Phase 4 — Missing-alert diagnosis

This is broader than checking whether an integration is enabled. Nubi needs to trace the delivery pipeline.

```text
Source produced an alert
    ↓
Agent/webhook forwarded it
    ↓
Nudgebee ingested an event
    ↓
Rule matched and created an alert
    ↓
Alert was not snoozed/suppressed/deduplicated
    ↓
Automation matched
    ↓
Destination delivered notification
```

### Required read capabilities

- Last event received from a source/account.
- Recent matching alert events.
- Alert-rule status and match conditions.
- Suppression/snooze state.
- Deduplication or grouping result.
- Matching automation status.
- Automation execution history.
- Destination integration status.
- Delivery attempt and safe failure category.

### Example output

> Prometheus is connected and metrics are current. Alertmanager has not delivered an event to Nudgebee in the last two hours. No alert rule or notification failure can be evaluated because the event never reached ingestion. Check the configured Alertmanager receiver and recent delivery attempts.

Or:

> The event arrived and matched rule `HighCPU`, but the resulting alert is snoozed until 18:00 UTC. Notification automation was therefore not triggered.

### Important behavior

When a stage has no evidence, stop at that stage. Do not invent a downstream root cause.

---

## Phase 5 — Documentation and retrieval coverage

This is one of the largest remaining areas.

### 5.1 Installation and health documentation

- Kubernetes agent installation.
- VM/proxy agent installation.
- AWS/Azure/GCP agentless onboarding.
- Agent heartbeat interpretation.
- Prometheus setup and troubleshooting.
- Alertmanager setup and troubleshooting.
- Logs providers.
- Traces providers.
- OpenCost modes.
- Relay and node-agent health.
- Version upgrades and compatibility.

### 5.2 Alert documentation

- Difference between alert, event and notification.
- How to snooze an alert.
- Snooze versus suppress versus disable.
- Why no alert was created.
- Why no notification was sent.
- Deduplication/grouping behavior.
- How to test the full alert path safely.

### 5.3 Automation documentation

- Automation concepts and lifecycle.
- Event-triggered automation walkthrough.
- Optimization-triggered automation walkthrough.
- Conditions and account scope.
- Destination/action configuration.
- Execution history.
- Common failure modes.
- Safe testing procedure.

### 5.4 Integration troubleshooting

Per important integration class:

- Inbound webhooks.
- Outbound notification webhooks.
- API-based integrations.
- Kubernetes-native providers.
- Proxy-based integrations.

Each page should state:

- What "enabled" means.
- Whether an active connection test exists.
- What successful operation looks like.
- How to verify data arrival.
- Common failure modes.
- Safe next steps.

### 5.5 Docs gap audit

Use three inputs:

1. Public API/action surface.
2. Product UI workflows.
3. Common user questions/support incidents.

The API surface is useful for discovering undocumented capabilities, but documentation should be organized around user goals rather than endpoint names.

### 5.6 Retrieval evaluation

Create questions such as:

- How do I snooze an alert?
- How do I configure event-based automation?
- How do I configure optimization automation?
- Why are Prometheus alerts not arriving?
- What is the difference between an account and integration?
- Does a Datadog webhook support connection testing?

For every question, verify:

- Correct document is retrieved.
- Citation is returned.
- Answer does not mix documentation with tenant state.
- Missing documentation is reported honestly.
- One good search is sufficient in normal cases.

---

## Phase 6 — Diagnostic runbooks or skills

Tools retrieve state; runbooks/skills teach Nubi how to reason through problems.

Initial runbooks:

1. Agent disconnected.
2. Prometheus disconnected.
3. Alertmanager disconnected.
4. Logs/traces disconnected.
5. Cloud synchronization stale.
6. Integration not working.
7. Alerts missing.
8. Notifications missing.
9. Snooze/suppression questions.
10. Automation setup and failure.

Each runbook should define:

- Triggering questions.
- Required product evidence.
- Optional runtime evidence.
- Decision tree.
- Safe error classifications.
- Stopping conditions.
- Documentation references.
- Recommended next action.
- Prohibited assumptions or unsafe data.

Use a skill/runbook when the procedure is complex and reusable. Do not create one tool per troubleshooting step.

---

## Phase 7 — Controlled remediation

Diagnosis should come first. After it is dependable, introduce mutations.

Potential actions:

- Run cloud synchronization now.
- Retry a supported integration connection test.
- Enable or disable an integration.
- Snooze or unsnooze an alert.
- Retry a notification.
- Create or update an automation.
- Generate a corrected Helm values fragment.

Requirements:

- Separate read and write tools.
- Existing RBAC enforced server-side.
- Exact target resolution.
- Explicit confirmation for meaningful mutations.
- Preview/dry-run when possible.
- Audit record.
- Clear before/after result.
- No broad tenant-admin fallback.
- Never accept tenant/user scope from prompt text.

We can initially allow everyone who can see health to trigger read-only investigation. Fine-grained remediation RBAC can follow once the problem-solving flow works.

---

## Phase 8 — Permanent evaluation suite

Convert the successful live questions into repeatable evaluation cases.

Coverage dimensions:

- Explicit `@nubi`.
- Default K8s orchestrator.
- AWS, Azure and GCP orchestrators.
- Connected, disconnected and partially connected agents.
- Agentless accounts.
- Supported and unsupported connection tests.
- Missing/inaccessible resources.
- Ambiguous integration names.
- Stale versus current data.
- Permission-restricted users.
- Secret-bearing backend payloads.
- No-tool and wrong-tool regressions.

Assertions should cover:

- Agent selection.
- Exact tool names.
- Maximum/bounded calls.
- Account filters.
- Response classification.
- Required claims.
- Forbidden claims.
- Data-exposure checks.

## Suggested delivery sequence

1. Merge and validate PR #37735.
2. Normalize agent/collector health verdicts and errors.
3. Validate K8s, VM and agentless scenarios.
4. Implement Nudgebee-to-environment investigation handoff.
5. Build the missing-alert pipeline.
6. Complete the high-priority documentation audit and pages.
7. Add reusable diagnostic runbooks/skills.
8. Add controlled remediation actions.
9. Expand the permanent evaluation suite throughout every phase.

Each step should remain a small reviewable PR under epic #37223, with live `nbctl` evidence before merging.
