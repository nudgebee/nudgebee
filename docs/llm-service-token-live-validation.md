# Focused live LLM token upgrade validation

On 2026-11-01, a disposable kind v0.33.0 / Kubernetes v1.37.0 cluster
ran Helm v3.22.1 upgrades from `58552017c24b596fe5bf01ec2878b6c70eb43cca`
to `3ec59d73b124810763e05d64f68e11eee10cc5c7`.

This was a focused chart harness, not a full Nudgebee installation. Each arm
copied ten production templates byte-for-byte: the parent Secret and helpers,
and the LLM/RAG deployment, service, service-account and helper templates.
The parent helpers are necessary to retain the umbrella chart's shared-Secret
selection. All managed upgrade cases were rerun with those helpers present.

The server image ran the full compiled `llm/llm-server/cmd` binary. The RAG
probe image imported the actual capability-sync, configuration and database
modules, wrapped by a small health process rather than the complete RAG service.
PostgreSQL used the actual V977 capability migration with minimal account/KB
prerequisites. A local workflow-task registry fixture supplied the downstream
`/tasks` response; this did not validate a deployed workflow-server registry.

## Observed results

| Check | Result |
|---|---|
| Managed old-to-new upgrade | Generated a distinct 64-character LLM token; retained the action token and encryption key; replaced both LLM/RAG pods. |
| Second managed upgrade | Retained the LLM token and rolled both consumers again. |
| Actual RAG catalog caller | Fetched 196 entries through the real LLM HTTP handler and stored 196 PostgreSQL rows before/after upgrade and with an external Secret. |
| Sync preservation | Removed an obsolete platform row and retained a KB-collection row. Failed authentication left existing rows intact. |
| Configured server | Wrong, empty and absent headers returned 401. The matching actual catalog request returned 200. |
| Unconfigured server / empty external Secret | Private requests returned 503; public health remained 200. |
| Supplied external Kubernetes Secret | Both consumers used its supplied token; matching catalog sync succeeded and incorrect credentials failed. |
| Offline explicit value | Two separate Helm renders retained the same explicitly supplied token. |
| Fresh Secret-only Helm installation and upgrade | Generated a distinct 64-character token and preserved it on upgrade; no application pods were installed in this additional namespace. |

The four-header matrix used an empty catalog request body: an accepted token
therefore reached request validation and returned **400**, whereas an incorrect
token returned **401**. The separate actual RAG fetch supplied a valid account
and returned **200**; these statuses must not be conflated.

The sanitized [evidence record](evidence/llm-service-token-upgrade.json) contains
the source revisions, exact template hashes, test results and runtime versions.
Raw harness scripts and logs were kept in the local worker validation directory;
generated credentials and the owned cluster were removed after verification.
Initial harness setup failures (image packaging, Python driver and an omitted
parent helper) were corrected before the final managed/external checks. One
probe raced service-network propagation; the harness now waits for health
transport readiness before asserting authentication, without retrying incorrect
authentication statuses.

## Remaining deployment validation

The full default chart, all service callers, RabbitMQ/background work, an
External Secrets operator, actual GitOps reconciliation and deployed model
generation were not exercised. The documented Small full-stack deployment needs
[4 CPU / 16 GiB](sizing-and-tiers.md); the local Docker engine had about 7 GiB.
Repeat the upgrade using the actual release images and installation values on
a suitably sized disposable/staging cluster before claiming full-installation
compatibility. Follow the [upgrade instructions](llm-service-token-upgrade.md)
when supplying externally managed or offline-rendered Secrets.
