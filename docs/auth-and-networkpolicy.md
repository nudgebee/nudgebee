# Authentication and NetworkPolicy

This doc captures the trust model the Nudgebee backend services rely on.
It exists so engineers don't accidentally weaken assumptions that
upstream handlers depend on, and so operators can configure
NetworkPolicy correctly when running outside the default Helm chart.

## The big picture

Nudgebee has one user-facing application — the Next.js `app/` pod —
and a fleet of internal services (`api-server`, `llm-server`,
`rag-server`, `runbook-server`, `ticket-server`,
`collector-server/*`). User requests follow exactly one path:

```
browser ──HTTPS──▶ ingress ──▶ app/ (BFF)
                                 │
                                 │  in-process /rpc/* dispatch
                                 │  (no network hop)
                                 ▼
                          internal services
                          (Postgres, RabbitMQ, ...)
```

**The `app/` pod is the auth boundary.** It runs NextAuth, terminates
user sessions, resolves the calling user's identity, tenant, and roles,
and stamps them onto every outbound RPC. Downstream services trust
that envelope and do not re-authenticate the user.

This design is deliberate — re-validating sessions at every hop would
double the work for no gain, since downstream services cannot reach
the identity provider, the session JWT keys, or the user database
without trusting *something* the BFF already proved.

## What the trust contract actually is

Every internal RPC carries:

| Header / field | Set by | Trusted to mean |
|---|---|---|
| `X-ACTION-TOKEN: <per-service token>` | `app/` BFF, `llm-server`, and any other service-to-service caller | Caller is a legitimate Nudgebee service. Same header name across api-server, rag-server, services-server, etc. — but the *value* is per-destination (`ACTION_API_SERVER_TOKEN` for api/services-server, `RAG_SERVER_TOKEN` for rag-server, etc.), so a leak of one backend's token doesn't open the others. |
| `X-SECRET-KEY: <RELAY_SERVER_SECRET_KEY>` | `app/` BFF | Caller is a legitimate Nudgebee service, on the relay-server's *internal* (cloud-platform-facing) routes only. |
| `X-Tenant-Id` | `app/` BFF | The session-resolved tenant ID. Downstream uses this for row scoping. |
| `X-User-Id` | `app/` BFF | The session-resolved user ID. Downstream uses this for audit and per-user checks. |
| Body fields like `account_id`, `tenant_id` | Caller (possibly user) | Validated against the session-resolved tenant inside the handler. |

The shared static tokens (`ACTION_API_SERVER_TOKEN`,
`RAG_SERVER_TOKEN`, `RELAY_SERVER_SECRET_KEY`, ...) live in the
`nudgebee` k8s secret and are mounted into every pod that needs to
talk to a backend service. They are *not* per-user tokens — they
authenticate the *caller pod*, not the *end user*.

**Consequence:** if any pod that mounts the `nudgebee` secret is
compromised, the attacker can mint the headers above and reach every
backend service whose token sits in that secret. NetworkPolicy is the
layer keeping that blast radius contained; per-service token values
ensure that compromising one *config map's value* doesn't yield the
others.

## External-facing endpoints (not protected by the BFF boundary)

A handful of services accept traffic from outside the cluster. The
BFF-is-the-auth-boundary model does not apply to them — they each
enforce their own per-source authentication.

### relay-server (`collector-server/k8s-collector/relay-server`)

The relay-server is the gateway that customer-installed Kubernetes
agents connect to from outside the cluster. It serves two distinct
audiences on the same pod:

- **Agent routes** (`/register`, `/ws`): Basic-auth using an agent's
  `accessKey` + `secret`. The secret is bcrypt-hashed in the
  `agent.access_secret_v2` column on the api-server's Postgres; the
  middleware (`AgentAuthMiddleware` at
  `relay-server/pkg/server/middleware/auth.go`) validates against it
  via `store.ValidateAgent`. There's a legacy v1 AES path retained for
  dormant rows; cleanup is tracked separately.
- **Cloud-platform routes** (`/request`, `/grafana/*`,
  `/prometheus-v2/*`, `/api/proxy/*`): static `X-SECRET-KEY` header
  against `RELAY_SERVER_SECRET_KEY` (`ClientAuthMiddleware`,
  constant-time compared with SHA-256 length-hiding).
  Only the BFF / cloud-control-plane is expected to call these.
- **Prometheus legacy** (`/prometheus/api/v1/*`): `Authorization:
  Bearer <RELAY_SERVER_SECRET_KEY>` + `X-Scope-OrgID`.
- **Outbound responses to agents**: signed with a per-relay Ed25519
  private key (`SIGNING_PRIVATE_KEY`) so agents can verify the
  control-plane wasn't impersonated. Public key is distributed via
  `SIGNING_PUBLIC_KEY`.

The relay-server ingress is therefore exposed to the internet (or at
minimum to customer VPCs); NetworkPolicy is *not* what protects it.
Authentication does. Misconfiguring `RELAY_SERVER_SECRET_KEY` or the
signing keys breaks the whole agent fleet.

### k8s-collector app (`collector-server/k8s-collector/app`)

The Python k8s-collector ingress receives metrics and queries from
the in-cluster agent. Auth path:
`AuthTokenMiddleware` (`middleware/auth_token_middleware.py`)
extracts `Authorization: Basic <accessKey:secret>`, resolves the
`agent` row, and validates the secret with bcrypt (v2) or the legacy
plaintext path (v1). Same `agent` table as the relay-server, so the
two pods stay consistent.

### Webhook endpoints (`app/src/pages/api/webhooks/*` and friends)

The BFF *itself* exposes a set of webhook routes that accept
unauthenticated traffic from external providers. Each route verifies
the provider's signature before doing anything with the payload:

- **Slack** (`webhooks/slack/events.ts`,
  `webhooks/slack/interactive.ts`): HMAC-SHA256 over
  `v0:<timestamp>:<body>` with `SLACK_SIGNING_SECRET`, constant-time
  compared.
- **Google Chat** (`webhooks/google-chat/events.ts`): JWT verification
  against Google's rotating JWKS; `iss` and `aud` enforced.
- **Microsoft Teams** (`webhooks/msteams/events.ts`): HMAC-SHA256 with
  `MS_TEAMS_CLIENT_SECRET`.
- **GitHub** (`webhooks/github/*`): HMAC-SHA256 with
  `GITHUB_WEBHOOK_SECRET`.
- **Marketplace** (Azure: `marketplace/azure/webhook.ts`; AWS:
  `marketplace/aws/subscribe.ts`): Azure verifies the JWT against
  Microsoft's JWKS; AWS uses a signed subscription token.
- **Jira / PagerDuty / NewRelic** (`webhooks/{jira,pagerduty,newrelic}/`):
  currently stub endpoints; signature verification is required before
  wiring forwarding logic.

These routes must **fail closed** when their signing secret is unset
(do not accept HMAC over an empty key). Reuse the shared
verifier helper rather than reimplementing per provider.

### OAuth install / callback routes

`api/integrations/callback/*` and `api/slack/{install,oauth_redirect}.ts`
are unauthenticated by browser session — the request lands directly
from the upstream IdP / install flow. They must verify a signed
`state` parameter (set at install-start, checked here) to prevent CSRF
attachment of an attacker's integration to the victim's tenant. Same
pattern across all OAuth providers; gaps in any of them are tracked
in the security backlog.

## NetworkPolicy expectations

The Helm chart installs default deny-all `NetworkPolicy` resources
plus per-service allow rules. **The invariant**: only the BFF and the
internal services should be able to reach internal-service ports.
External ingress lands on the BFF, never on a backend directly.

If you run outside the bundled chart, the equivalent set of rules is:

1. Default deny-all ingress in the `nudgebee` namespace.
2. Each backend service allows ingress only from:
   - The BFF service account (`app`)
   - Other backends that legitimately call it (e.g. `llm-server` → `rag-server`)
   - `kube-system` / kubelet for probes
3. Egress from backend services restricted to:
   - The Postgres / Redis / RabbitMQ / Qdrant / Temporal endpoints they need
   - LLM provider endpoints (only from `llm-server`)
   - The relay-server for k8s collector traffic (only from services that need it)
4. The `kube-apiserver`, `etcd`, kubelet ports, and cloud metadata IPs
   are denied from every workload pod.

The runbook-server and llm-server workspace pods get a tighter egress
policy because they execute tenant-authored code; they explicitly
deny everything except the relay-server and the LLM provider.

## Why backend handlers don't re-verify the session

Downstream Go handlers receive a
`*security.RequestContext` already populated with `tenantId`,
`userId`, `roles`, etc. They use those *for authorization* (which
rows the caller can see, whether they can perform an action) but they
do not re-verify the bearer JWT or any other auth artifact.

If you find yourself wanting to "double-check" something at the
backend, the right question is: *can I do it from the values already
on the context?* If the answer is no, the BFF didn't put them there,
which means either (a) it's a missing field in the BFF →backend
plumbing, or (b) the action is being called from a non-BFF caller
that shouldn't exist.

The one *handler-side* check that has to happen per-row is **target
identity vs. session identity** — e.g. "the username the request
wants to modify must equal the username on the session, unless the
session is super-admin." That can't be enforced at the BFF gateway
because the BFF doesn't know per-handler semantics; the handler is
the only place that knows whose row is being touched.

## What to do when adding a new internal endpoint

1. **Use the `X-ACTION-TOKEN` header convention** — same name as every
   other internal backend, so callers don't have to special-case your
   service. The *value* it expects should be a service-specific env
   var (e.g. `<YOUR_SERVICE>_TOKEN`); reusing `ACTION_API_SERVER_TOKEN`
   widens the blast radius of a single token leak. Relay-server's
   client-facing routes are the exception (they use `X-SECRET-KEY` /
   `RELAY_SERVER_SECRET_KEY`) because they live alongside externally
   exposed routes and need a separate secret to limit blast radius.
2. **Use constant-time comparison** (`subtle.ConstantTimeCompare` in
   Go, `hmac.compare_digest` in Python).
3. **Fail closed when the secret env is empty.** Refuse the request,
   return 503, log loudly. Do not accept empty-vs-empty as a match.
4. **Take tenant from the request context, not the body.** If a body
   field looks like a tenant or account id, validate it against
   `ctx.GetSecurityContext().GetTenantId()` before using it.
5. **Add a NetworkPolicy entry** for the new ingress path. The
   default deny-all means a freshly-deployed service is unreachable
   until you do.

## What to do when adding a new internal-service caller

1. Mount the shared `nudgebee` secret into the caller pod.
2. Read the matching env var via your config layer.
3. Set the header on every outbound request — use a helper if there
   are more than two callsites, so it can't drift.
4. Update the corresponding NetworkPolicy on the destination service
   to allow ingress from the new caller.

## Known holes (tracked separately)

The pieces of this trust model we know are imperfect today:

- **Static shared tokens are coarse-grained.** A compromise of the
  `nudgebee` secret gives access to every internal service. Long-term,
  per-pair signed tokens (or mTLS via a service mesh) would be the
  fix. Tracked in the second-pass security review.
- **`X-Tenant-Id` is unsigned.** Any pod with the shared token can
  send any tenant id. The mitigation today is NetworkPolicy (only the
  BFF talks to backends). A future hardening step is to bind tenant
  into a signed envelope so a stolen token alone isn't enough.
- **NetworkPolicy on per-tenant workspace pods deserves its own
  audit.** Workspace pods execute tenant code, so the egress policy
  matters more there than anywhere else.

These items are tracked in the project's security backlog.
