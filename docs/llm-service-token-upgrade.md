# LLM service token upgrade

Private llm-server routes require a configured `LLM_SERVER_TOKEN` and a matching
`X-ACTION-TOKEN` request header. An unconfigured server returns 503; a missing or
incorrect request token returns 401. Existing health, docs, and workspace-route
authentication remain as implemented by their respective middleware.

## Deploying the change

Chart-managed installations generate a distinct LLM token and retain it through
the existing Secret lookup on upgrade. The shared Secret supplies both callers
and llm-server, and existing pod-template rollout annotations restart consumers
on a Helm upgrade. Do not rotate the token on only one side of a connection.

External Secret managers must supply a non-empty `LLM_SERVER_TOKEN` in the shared
Secret before upgrading. Offline GitOps renders cannot read the existing Secret:
set a stable `nudgebee_secret.LLM_SERVER_TOKEN` explicitly or reference an external
Secret containing it. Otherwise private requests return 503 or fail to authenticate.

Docker Compose supplies the matching local-development value from the root
`.env.example`. For standalone processes, set the same destination token in each
caller listed below and in llm-server. Generate a fresh value for shared deployments
with `openssl rand -hex 32`; development example values are not production secrets.

## Caller audit

| Caller | Token path |
|---|---|
| App RPC gateway | `LLM_SERVER_TOKEN` in the action routing headers |
| Services-server | LLM chat/poll, connectivity check and playbook enrichment use the configured LLM token/header |
| Notifications | Chat, asynchronous chat and event analysis use the existing LLM auth helper |
| Workflow-server (`runbook-server`) | LLM service calls and scheduled trigger headers use the configured LLM token |
| Rag-server | Capability catalog sync now reads the token/header and authenticates its POST |
| Kubernetes collector | LLM trigger requests now read the token/header and retain tenant/user headers |
| Benchmark tools | Standard client uses `LLM_SERVER_TOKEN`; tbench/orcabench/swebench supply `NUBI_TOKEN`, which must match the server token |

`LLM_SERVER_TOKEN_HEADER` defaults to `X-ACTION-TOKEN`. If overriding it, configure
the same name at both endpoints. Code-analysis workspace clients use workspace
credentials on separately authenticated workspace routes; do not replace those
credentials with the shared service token.
