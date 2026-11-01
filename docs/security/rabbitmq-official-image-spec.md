# Spec: replace the Bitnami RabbitMQ subchart with the official RabbitMQ image

Part of #590 (image-hardening Phase 2). Follows the Redis approach (`redis-official-image-spec.md`). Status: **implemented and live-tested (2026-10-31).**

## 1. Goal and acceptance

The umbrella chart deploys `ghcr.io/nudgebee/rabbitmq:3.13.7-debian-12-r5-nb1` (Bitnami-legacy rebuild): **160 Critical+High** in Grype.

| # | Acceptance check | Evidence |
|---|---|---|
| A1 | The deployed RabbitMQ image has 0 Critical/High in Grype | `scan-deployed` job |
| A2 | Fresh install comes up, and every producer and consumer works | docker-desktop: full stack, queues bound, messages flow |
| A3 | `helm upgrade` from the **1.12.2 chart** keeps every queue, binding, policy, user, vhost and durable message, with no manual step | docker-desktop: install 1.12.2 RabbitMQ, publish persistent messages, upgrade, consume them |
| A4 | The A3 upgrade also works when the install has stable feature flags disabled (installs that started on RabbitMQ 3.12) | Same test, with a flag disabled before upgrading |
| A5 | Forward-only (D1): a failed rollback recovers by upgrading again, with no data loss | docker-desktop (shown in the spike) |
| A6 | `rabbitmq.enabled=false` (external RabbitMQ) unchanged | render diff |

## 2. Facts (checked 2026-10-31)

**Image candidates (Grype C+H):**

| Image | C+H |
|---|---|
| current Bitnami 3.13.7 rebuild | 160 |
| official `rabbitmq:3.13.7-management-alpine` | **154** (21 C, 133 H). Staying on 3.13 doesn't help |
| official `4.0.9` / `4.1.8` / `4.2.9` / `4.3.7` `-management-alpine` | **0** |

**So this is also a major RabbitMQ upgrade (3.13 → 4.x).** From the RabbitMQ upgrade docs:
- 3.13 can roll directly to **4.0, 4.1 or 4.2**, but **not 4.3**: "must first upgrade to 4.2.x".
- All **stable feature flags must be enabled before** upgrading, or the 4.x node may refuse to boot.
- "RabbitMQ does not officially support downgrades": see D1.

**Deprecated features we use, checked by booting official 4.0.9 and 4.2.9 on docker-desktop:**
- api-server consumers declare **non-durable, non-exclusive queues** by default (`api-server/services/common/mq.go:417-431`), which is the `transient_nonexcl_queues` feature. It is still **permitted by default in 4.0.9 and 4.2.9**. Relay's reply queue is exclusive, and every other queue is durable.
- Bitnami's generated `rabbitmq.conf` (including the renamed `queue_master_locator` key) **boots unchanged** on both.
- We use no classic queue mirroring (removed in 4.0), global QoS or RAM nodes.

**How Bitnami 12.6.1 runs it today** (rendered from `main`):

| Item | Value that must carry over |
|---|---|
| StatefulSet frozen fields | name `rabbitmq`; selector `app.kubernetes.io/{name: rabbitmq, instance: <release>}` (no component); `serviceName: rabbitmq-headless`; `podManagementPolicy: OrderedReady`; volumeClaimTemplate `data`, 8Gi, RWO, labels name+instance |
| **Node name** | `rabbit@$(POD_NAME).rabbitmq-headless.$(NAMESPACE).svc.cluster.local`, with long names |
| **Data dir** | `/bitnami/rabbitmq/mnesia/<node name>` (PVC mounted at `/bitnami/rabbitmq/mnesia`). A different node name means RabbitMQ starts empty and the old queues become invisible |
| Identity | uid 1001, fsGroup 1001; Erlang cookie from Secret `rabbitmq` key `rabbitmq-erlang-cookie`; password key `rabbitmq-password`; user `user` |
| Plugins | `rabbitmq_management`, `rabbitmq_peer_discovery_k8s`, `rabbitmq_auth_backend_ldap`, `rabbitmq_prometheus`: all included in the official image |
| Config | `default_user/pass`, k8s peer discovery (Role `rabbitmq-endpoint-reader`), `cluster_partition_handling = autoheal`, `queue_master_locator`, `loopback_users.user = false`, `consumer_timeout = 3600000` (ours), `prometheus.tcp.port = 9419` |
| Services | `rabbitmq` (amqp 5672, epmd 4369, dist 25672, http-stats 15672, metrics 9419) and `rabbitmq-headless` |
| Pod | preStop graceful shutdown, `terminationGracePeriodSeconds: 120`, our custom readiness probe (tcp `amqp`) |
| Optional objects | `metrics.serviceMonitor` and `metrics.prometheusRule` (our values ship 11 alert rules, off by default) |
| Consumers | Every service via `RABBIT_MQ_HOST` (`rabbitmq`); the migration Job's `rabbitmq/*.sh` scripts use the management HTTP API |

## 2b. Spike results (docker-desktop, Bitnami 12.6.1 → official `rabbitmq:4.2.9-management-alpine`)

Only the pod template changed: same StatefulSet, PVC, node name and data path.

| Check | Result |
|---|---|
| 4.2.9 boots on Bitnami 3.13.7 data | ✅ `data dir: /bitnami/rabbitmq/mnesia/rabbit@rabbitmq-0.rabbitmq-headless.<ns>.svc.cluster.local` |
| Durable classic queue (50 persistent msgs) and quorum queue (50) | ✅ all messages kept |
| Users, vhosts, policies | ✅ kept |
| Transient (non-durable) queue | Dropped, as on any broker restart today (3.13 does the same) |
| Management API through the `rabbitmq` Service (used by the migration Job) | ✅ HTTP 200 |
| Deprecated features in use after the upgrade | ✅ none reported |
| Install that started on **3.12.11** (Bitnami 12.6.1 default), then moved to 3.13.7 | Had 6 stable flags disabled. Single node: 4.2.9 **auto-enabled the 3 required flags** and kept all data. Multi-node can't do that, so the pre-upgrade flag hook stays |
| **Rollback to 3.13 on 4.2-touched data** | ❌ `BOOT FAILED: timeout_waiting_for_khepri_projections`. **Rolling forward to 4.2.9 again recovered every message.** Data is not lost; 3.13 just can't read it |

Image details the templates must handle (found in the spike):
- With `RABBITMQ_ENABLED_PLUGINS`, RabbitMQ writes `enabled_plugins`, and `/etc/rabbitmq` isn't writable as uid 1001. Set `RABBITMQ_ENABLED_PLUGINS_FILE` under the writable HOME.
- An init container puts the Erlang cookie (from Secret key `rabbitmq-erlang-cookie`) into an `emptyDir` HOME with mode 0600.
- Point `RABBITMQ_CONFIG_FILE` at the rendered config. Bitnami's config boots unchanged on 4.2.9.
- Bitnami's preStop runs `/bin/bash`, which the Alpine image doesn't have. Use `sh` with `rabbitmqctl stop_app`.
- The image has no curl, so the probes call Bitnami's management health endpoints (`/api/health/checks/virtual-hosts`, `/local-alarms`) with BusyBox `wget` and `base64 -w0`. That avoids starting an Erlang VM per probe. When Bitnami would have used the CLI (`loadDefinition` on, or no management plugin), the probes use `rabbitmq-diagnostics` too.
- Set `RABBITMQ_MNESIA_BASE` to the PVC mount path, so the feature-flags file stays where Bitnami kept it.

## 3. Design (same pattern as Redis)

1. Remove the Bitnami `rabbitmq` 12.6.1 dependency. Render it ourselves in `templates/rabbitmq/` with the **same frozen StatefulSet fields, node name, data path, uid, cookie, Secret keys, config, plugins and Services**, using the official `rabbitmq:<4.x>-management-alpine` mirrored to GHCR.
2. **Feature-flag pre-step.** A `pre-upgrade` hook Job runs `rabbitmqctl enable_feature_flag all` from the **new RabbitMQ image** against the still-running 3.x node, authenticating with the Erlang cookie. That enables every stable flag and leaves experimental ones like `khepri_db` off. Using the new image means there's no extra image to mirror and no dependency on the management password (verified: the 4.2.9 CLI works against a 3.13 node). It does nothing on a 4.x broker or a fresh install. An unreachable broker is skipped; only a multi-replica install fails the upgrade on error.
3. **Do not enable new 4.x feature flags** after the upgrade. They change on-disk formats and nothing needs them now, so operators can enable them later, deliberately.
4. Cookie: an init container copies it from the Secret into an `emptyDir` HOME with mode 0600 as uid 1001, because Erlang refuses a group-readable cookie. Config comes from a ConfigMap plus a password conf rendered from the Secret.
5. Values: the same key-by-key audit as Redis, so every Bitnami 12.6.1 key is honored, rejected with a message, or ignored with a stated reason.
6. Images and docs: mirror via `clone-third-party.sh`, drop the Debian RabbitMQ build from `infra-mirrors-image.yaml`, update `security-scan.yaml` and the hardening log.

## 4. Backward-compatibility matrix

| # | Area | Effect | Status |
|---|---|---|---|
| B1 | `helm upgrade` mechanics | Same StatefulSet, only the pod template changes | ✅ by design (to prove: A3) |
| B2 | Queues, bindings, users, vhosts, policies, durable messages | Same node name and data dir, so 4.x boots on the same data | ✅ by design (to prove: A3) |
| B3 | Feature flags off on older installs | The pre-upgrade hook enables them on 3.13 first | ✅ by design (to prove: A4) |
| B4 | App compatibility (transient non-exclusive queues, config keys) | Permitted, and boots on 4.0.9 and 4.2.9 | ✅ checked |
| B5 | Downtime | One broker restart. Non-durable queues lose their messages, the same as any broker restart today | ⚠️ release note |
| B6 | Rollback to 1.12.2 (3.13) | Not possible: 3.13 can't boot on 4.x data. Recovery is upgrading again, which keeps all data (spike) | **Forward-only (D1)**. Release note |
| B7 | Private-registry and air-gapped installs | Must mirror the new image first | ⚠️ release note |
| B8 | EE / OSS | Same-day EE port, like Redis | ⚠️ tomorrow's plan |

## 5. Decisions

- **D1: Rollback, decided forward-only (2026-10-31).** RabbitMQ doesn't support downgrades, and the spike confirmed 3.13 won't boot on 4.x data. There's no rollback path. The release note says so, and says that re-running the upgrade recovers a failed rollback with no data loss. Effort goes into making the forward upgrade safe instead.
- **D2: Target version.**
  - **4.2.x** is the newest version 3.13 can reach in one hop, and the only path to 4.3+.
  - **4.0.x** is the smallest jump.
  - All of 4.0.9, 4.1.8 and 4.2.9 scan at 0.
  - **Decided: 4.2.9.** Choosing 4.0 would mean another major upgrade soon, and 4.3 can't be reached from 3.13 at all.

## 6. Test plan (docker-desktop)

1. A spike before full templating. Install the 1.12.2 Bitnami RabbitMQ and create durable and transient queues, a policy, and persistent messages. Upgrade by changing only the image to the official 4.2.9 with the Bitnami node name and data path. Check that every queue and message survives, then try the D1(a) rollback.
2. Run step 1 again with a stable feature flag disabled beforehand, to prove the A4 hook.
3. Full templates: the render comparison (frozen fields for the default and every persistence override) plus the A2/A3/A5 live runs.
4. Run the full stack once against the new broker: api-server, the collectors, notifications, ml-k8s-server, and the migration Job's rabbitmq scripts.

## 6b. Live test results (docker-desktop, real chart templates, `helm upgrade`)

| Test | Result |
|---|---|
| Render comparison against Bitnami 12.6.1: StatefulSet name, selector, serviceName, podManagementPolicy, PVC template, data volume, node name, data path | ✅ identical in all 18 cases tried: default, `global.storageClass` (named and `-`), size + storageClass, PVC labels/annotations, `existingClaim`, persistence off, `nameOverride`, `commonLabels` (including an `app.kubernetes.io/name` override), `podLabels`, `servicenameOverride`, `podManagementPolicy=Parallel`, `subPath`, `mountPath`, `clustering.addressType=ip`, `clusterDomain`, `namespaceOverride` |
| `rabbitmq.conf`, enabled plugins, both Services (ports, selectors, `publishNotReadyAddresses`) | ✅ identical to Bitnami's |
| Upgrade from 1.12.2 (3.13.7) with persistent messages in classic and quorum queues, a user, a vhost and a policy | ✅ 4.2.9 on the same PVC and node name, every message and object kept |
| Upgrade of an install that started on 3.12.11, then moved to 3.13.7 (6 stable flags off) | ✅ the pre-upgrade hook enabled all 6 on 3.13, 4.2.9 booted with them, and a pod restart kept them |
| AMQP clients (pika): durable and transient non-exclusive queues, topic binding, prefetch, exclusive reply queue | ✅ |
| Migration Job's `rabbitmq/*.sh` (management API) | ✅ both scripts apply their policies |
| Fresh install, our values (`securePassword: false`) and Bitnami's default (`securePassword: true`, Secret password) | ✅ management login with the Secret password and the metrics endpoint both return 200 |

**Bug found and fixed during testing:** the official image keeps the feature-flags file under `RABBITMQ_MNESIA_BASE` (default `/var/lib/rabbitmq/mnesia`, an `emptyDir` here), while Bitnami kept it on the PVC. Without `RABBITMQ_MNESIA_BASE` pointed at the PVC, every restart forgot which feature flags were enabled.

**Follow-up (not in this PR):** api-server still declares non-durable, non-exclusive queues (`api-server/services/common/mq.go`). RabbitMQ 4.2 reports this as a deprecated feature in use but still allows it. Move those queues to durable or exclusive before going to RabbitMQ 4.3+, where it may be denied by default.

## 7. Out of scope

PostgreSQL (next), RabbitMQ clustering (we run one replica), Khepri migration (still on Mnesia; that's a later and separate decision).
