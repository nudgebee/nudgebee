# CLAUDE.md — NB AI Gateway (`llm/gateway`)

Guidance for agents working in this service. The design lives in [`docs/`](docs/README.md) —
read the doc for the area you touch before changing it. This file only adds what the docs
don't say: where things are, and the rules a change must not break.

## What it is

A multi-tenant LLM proxy. Clients point their provider base URL at the gateway and send an
**NB token** as the API key; the gateway authenticates it, routes, rate-limits, injects the
tenant's provider credential, forwards via embedded `bifrost-core`, and meters usage.
Provider keys never leave the server. See [`docs/architecture.md`](docs/architecture.md).

## Commands

```bash
make validate   # lint + test — must pass before commit
make fmt
make run
```

## Request path

`auth.Middleware` (`auth/`) → pipeline in `proxy/handler.go`:
`routeStage` → `ratelimitStage` → `resolverStage` → `filterStage` → passthrough → metering.

- **Auth** resolves the token via `user_auths` (`auth/user_auths.go`). Only keys with
  `kind IN ('gateway','all')`, `status='active'` and an unexpired `expires_at` authenticate.
- **Route** may rewrite the model (alias / tier / substitution / failover targets).
- Key kinds, expiry and per-key limits are created and validated in api-server
  (`api-server/services/user/token_policy.go`); the gateway only reads them.

| Topic | Doc |
|---|---|
| Rule engine, aliases, tiers | [`docs/routing.md`](docs/routing.md) |
| `/v1` OpenAI-compatible endpoint | [`docs/generic-endpoint.md`](docs/generic-endpoint.md) |
| Cross-provider substitution | [`docs/substitution.md`](docs/substitution.md) |
| Failover | [`docs/failover.md`](docs/failover.md) |
| Model deprecation shield | [`docs/deprecation.md`](docs/deprecation.md) |
| Egress secret detection | [`docs/guardrails.md`](docs/guardrails.md) |
| Provider config, keyless (IRSA) invariant | [`docs/config.md`](docs/config.md) |
| Deploy / client setup | [`docs/runbook.md`](docs/runbook.md) |

## Security invariants

Any check that restricts what a key may do (auth, rate limits, per-key model/provider/endpoint
policy, block rules) must hold all of these. Each one has been broken in review before.

1. **Judge the request that is actually sent.** A policy decision must use the model, provider
   and path that reach the provider *after* routing, aliasing, substitution and failover — not
   the name the client asked for. If a field is read from two places (body `model` vs. URL path),
   enforce on the one that is forwarded, or reject when they disagree.
2. **Normalise before you match.** Clean the request path (`..`, `//`, encoded segments) before
   any prefix or endpoint classification. Bifrost's HTTP clients resolve `..` when sending, so an
   unnormalised path can reach an endpoint the check never saw.
3. **One body decode.** Policy and forwarding must read the same parsed body. `encoding/json`
   matches keys case-insensitively and keeps the last duplicate (`"model"` vs `"MODEL"`), so a
   second, different parse is a bypass.
4. **Fail closed.** Unknown kind, missing identity, DB error, empty allow-list → deny. An empty
   selection must never mean "unrestricted"; only an explicit `NULL`/omitted field does.
5. **Every HTTP method counts.** Metadata/listing shortcuts must be limited to safe methods; a
   `DELETE`/`POST` on a provider admin path is a write with the tenant's credential.
6. **Exact matches for `provider/model`.** A model id may itself contain `/`
   (`anthropic/claude-…`); don't let a bare-model match unpin the provider.
7. **Never log or meter content by default.** Body logging is opt-in; structure-only attributes
   otherwise (`proxy/capture.go`).

Add a regression test for each invariant your change touches; probe it with a crafted request,
not only the happy path.

## Keep the docs current

New gateway capability → update or add the matching file under `docs/` in the same PR, and the
index in [`docs/README.md`](docs/README.md). Cross-service decisions go in the root
[`docs/architecture-decisions.md`](../../docs/architecture-decisions.md).
