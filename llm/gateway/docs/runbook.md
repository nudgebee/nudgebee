# AI Gateway — Internal Runbook

Practical guide for rolling out and operating the NB AI Gateway for an internal team.
For architecture and routing internals, see [`architecture.md`](architecture.md) and
[`routing.md`](routing.md).

The gateway is an everyone-facing service: point any LLM tool (Claude Code, SDKs,
apps) at an NB endpoint with an NB token, and it forwards to the provider
(Anthropic / OpenAI / Gemini) with cost attribution, metering, routing, and limits —
reusing NB's existing auth/tenancy.

---

## 1. Connect (what a teammate does)

Everything a user needs is on **Optimize → AI Gateway → Connect**:

1. Copy the base URL for their provider (`…/anthropic`, `…/openai`, `…/genai`).
2. **Generate token** → mints an NB API token (used as the bearer).
3. Copy the setup snippet for their tool. For Claude Code:

   ```bash
   export ANTHROPIC_BASE_URL="https://<gateway-host>/anthropic"
   export ANTHROPIC_AUTH_TOKEN="<NB token>"
   claude
   ```

**Verify it worked** (order of cheapest → most definitive):
- Run the **curl** snippet from the Connect tab — a `200` means URL + token + provider key all work. A clear `4xx` tells you exactly what's wrong (see §5).
- In Claude Code, `/status` shows the base URL + that auth is a token (not a subscription).
- **Ground truth:** make one call, then open **AI Gateway → Requests**. If the call shows up, it went *through* the gateway. If the tool works but nothing appears in Requests, the base URL didn't apply (it's bypassing the gateway).

---

## 2. What works / doesn't through the gateway

Setting `ANTHROPIC_BASE_URL` (and the OpenAI/Gemini equivalents) only redirects the
**inference API**. It does not redirect a provider's account/cloud services, so:

| Works ✅ (local / inference) | Doesn't work ❌ (needs the vendor's subscription/cloud) |
|---|---|
| Chat/messages, streaming, tools, prompt caching | Claude web ↔ CLI handoff (**teleport** `--cloud`/`--teleport`) |
| **Session resume/continue** (`--resume`, `-c`) — transcripts are local | **Remote Control** (disabled when base URL ≠ `api.anthropic.com`, v2.1.196+) |
| Model selection, MCP servers, hooks, subagents | Voice dictation, Slack integration |

It's **one mode per session**: gateway (governed API) *or* the vendor subscription.
The bearer token takes precedence, so a broken config **fails loudly** (a clear 4xx) —
it doesn't silently fall back to a subscription. For someone who wants both, keep two
profiles and switch.

---

## 3. Pre-rollout readiness checklist

Verify before flipping the flag on for the team (mostly config, ~15 min):

- [ ] **Provider keys configured** for the org (Anthropic/OpenAI/Gemini) — else every call `403`s.
- [ ] **Auth mode = `user_auths`** on the deployed instance (`GATEWAY_AUTH_MODE`) — not `static`/`none`.
- [ ] **Ingress / read timeouts** generous enough for long streaming completions (the Helm chart sets streaming annotations; confirm the ingress read-timeout).
- [ ] **`UI_ENABLE_LLM_GATEWAY=true`** and **`LLM_GATEWAY_PUBLIC_URL=<gateway base>`** set per environment (the Connect tab needs the public URL).
- [ ] **≥2 replicas** — the gateway is in every teammate's request path; a single pod means an org-wide outage on every restart/deploy (Redis-backed limits stay correct across replicas).
- [ ] **Pricing catalog** covers the models the team uses (the pricer logs a one-time warning for any unpriced model → cost would show `$0`). If a model publishes no discounted cached-input rate, set its cached-input cost to the standard input rate (not `0`) so cached tokens aren't billed as free.

---

## 4. Governance knobs

- **Redirect an expensive model → a cheaper one** (built): add a per-tenant routing rule
  (Admin UI → Gateway → routing rules, or a row in `llm_gateway_routing_rules`). Same
  provider family only (e.g. `claude-fable-5 → claude-sonnet-4.6`); cross-provider is P2.
  ```sql
  INSERT INTO llm_gateway_routing_rules (id, tenant_id, priority, enabled, "match", target)
  VALUES (gen_random_uuid(), '<TENANT_ID>', 100, true,
          '{"provider":"anthropic","model":"claude-fable-5"}'::jsonb,
          '{"provider":"anthropic","model":"claude-sonnet-4.6"}'::jsonb);
  ```
- **Default per-user cost guardrail** (built): set `GATEWAY_DEFAULT_USER_COST_LIMIT=<USD>`
  (+ `GATEWAY_DEFAULT_USER_COST_PERIOD=day|hour|minute|month`) so a runaway client can't
  drain the org budget before an admin sets an explicit limit. `0` = disabled.
- **Per-tenant/user quotas** (built): configured in `llm_gateway_rate_limits`
  (metric × period × value × scope). Over-limit → a clear `429` with a reset time.
- **Block/deny a model** — planned (tracked separately); use redirect in the meantime.

---

## 5. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `401 Invalid or expired NB token` | token wrong / not picked up | regenerate in AI Gateway → Connect; check the env var is set in *that* shell |
| `403 No <provider> credential is configured for your organization` | org has no provider key | add the provider key in NB integrations |
| `429 … limit exceeded … resets at <UTC>` | rate/budget quota hit | wait for the window, or an admin raises the limit; honor the `Retry-After` header |
| Tool works but **nothing in Requests** | base URL not applied (bypassing gateway) | re-check `*_BASE_URL`; `/status` in Claude Code |
| Cost shows **$0** | model not in the pricing catalog | add a pricing row (see the pricing migrations); the pricer logs the unpriced model |
| **View body** shows "not captured" | request predates body capture, or capture off | body capture is off by default; only requests made after enabling it have bodies |

**Body capture** (debugging): off by default. Enable with `GATEWAY_CAPTURE_BODY=true`
(needs a sink DB configured) + restart. Bodies are PHI-adjacent — stored with a TTL
(`GATEWAY_BODY_TTL_HOURS`, default 168h / 7 days) and only viewable by the request's
own user.

---

## 6. Contacts

- Owner / on-call: _<fill in>_
- Support channel: _<fill in>_
- Dashboards / alerts: _<fill in>_
