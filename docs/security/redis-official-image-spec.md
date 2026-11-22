# Spec: replace the Bitnami Redis subchart with the official Redis image

Part of #590 (image-hardening Phase 2). Status: **approved and implemented** (D2 = same day in EE and OSS, D3 = fail with a message).

## 1. Goal and acceptance

The umbrella chart deploys `ghcr.io/nudgebee/redis:7.0.12-debian-11-r0-nb2`, a rebuilt Bitnami-legacy image with 64 Critical+High Grype findings. Customers scan with Grype.

Done means all of the following:

| # | Acceptance check | Evidence |
|---|---|---|
| A1 | The deployed Redis image has 0 Critical/High in Grype | `scan-deployed` job in `security-scan.yaml` |
| A2 | Fresh install comes up, and every Redis consumer connects | `helm install` on kind, all pods Ready, no Redis auth/connect errors in the logs |
| A3 | `helm upgrade` from the **1.12.2 chart**, on a cluster with data, completes with no manual step | kind: install 1.12.2, then `helm upgrade` to this branch; exit 0, all pods Ready |
| A4 | `helm rollback` to 1.12.2 after A3 works | kind: rollback exit 0, Bitnami Redis Ready again |
| A5 | `redis.enabled=false` with an external Redis still works unchanged | `helm template` diff shows only the Redis objects removed |

## 2. Facts this plan rests on

- **Redis only holds data that can be thrown away.** Every consumer either caches with a TTL or holds a short lock: cloud-collector sync lock 2 min (`collector-server/cloud-collector/common/redis.go:32`), k8s-collector lock 600 s (`collector-server/k8s-collector/app/db/redis_client.py:26`), llm-server pending tokens 1 h (`llm/llm-server/common/pending_tokens.go:38`), relay MCP sessions (losing them is accepted in `docs/architecture-decisions.md`), plus namespaced caches in api-server, llm-server, runbook-server, ticket-server, notifications and rag-server. **Starting with an empty Redis is safe.**
- **Consumers only know** `REDIS_SERVER_HOST` (default `redis-master`), `REDIS_SERVER_PORT` (6379), `REDIS_USER_NAME` (`default`) and `REDIS_USER_PASSWORD`, all set in `templates/secrets-nudgebee.yaml:100-103`. Nothing references the Bitnami labels or the `redis-headless` / `redis-replicas` Services.
- **The official image already runs our stack:** `docker-compose.yaml:113` uses `ghcr.io/nudgebee/redis:7-alpine`, and so does the llm-gateway init image (`deploy/kubernetes/llm-gateway/values.yaml:85`).
- **Grype on the candidate images:** `redis:7.2.17-alpine` has 0 C/H. `redis:7.2.17-bookworm` has 74.

## 3. Design: same StatefulSet, different pod

**Approach:** stop using the Bitnami subchart, but render a StatefulSet whose **frozen fields are identical** to the one Bitnami created. Kubernetes allows a StatefulSet's pod template to be changed in place, but rejects any change to its name, `selector`, `serviceName` or `volumeClaimTemplates`. If we keep those four identical, `helm upgrade` simply rolls the pod onto the new image, and the existing PVC stays attached with its data intact. There's nothing to migrate, delete or adopt, and no `lookup`, so `helm template` and ArgoCD behave the same as `helm upgrade`.

Bitnami's current object (17.11.8, rendered from `main`), which must be reproduced exactly:

| Field | Value that must stay identical |
|---|---|
| `metadata.name` | `redis-master` |
| `spec.selector.matchLabels` | `app.kubernetes.io/name: redis`, `app.kubernetes.io/instance: {{ .Release.Name }}`, `app.kubernetes.io/component: master` |
| `spec.serviceName` | `redis-headless` |
| `volumeClaimTemplates[0]` | name `redis-data`; labels name/instance/component as above (no `helm.sh/chart`); `accessModes: [ReadWriteOnce]`; `storage` from `redis.master.persistence.size` (default `8Gi`); `storageClassName` resolved exactly like Bitnami's `common.storage.class` (`global.storageClass` wins, then `redis.master.persistence.storageClass`; `"-"` renders `""`; empty renders nothing) |

Free to change (pod template): the image becomes a ghcr mirror of official `redis:7.2.17-alpine`, plus the command, probes, config and security context.

1. Remove the `redis` dependency (Bitnami 17.11.8) from `Chart.yaml`, `Chart.lock` and the vendored `charts/redis-17.11.8.tgz`.
2. Add `templates/redis/` behind `redis.enabled`: the StatefulSet above, plus Services `redis-master` and `redis-headless` (same names, ports and selectors as today), ServiceAccount `redis` (same name), and a ConfigMap `redis-configuration`.
3. **Same Redis behaviour as today.** Bitnami's rendered config is `appendonly yes`, `save ""`, `dir /data`, `rename-command FLUSHDB ""` and `rename-command FLUSHALL ""`. We keep all of it. We add one setting, `aof-use-rdb-preamble no`. Without it, Redis 7.2 writes its AOF base in RDB format v11, which Redis 7.0 can't read, so a `helm rollback` would crash-loop. With it the AOF stays in plain command format, which both versions read.
4. **The same files on the same volume.** The pod runs as uid/gid 1001 with `fsGroup: 1001`, matching the ownership of the existing PVC; the official image runs fine as a non-root uid. The volume is mounted at `/data`, Bitnami's path. Redis 7.2 loads the AOF that 7.0 wrote.
5. Same password: from Secret `redis`, key `redis-password` (`templates/secrets-redis.yaml`). It's written into a config file under `/tmp` at container start, so it never appears in `ps`. `readOnlyRootFilesystem`, all capabilities dropped, `redis-cli ping` probes.
6. Values: every Bitnami key the new template touches keeps its name and default: `redis.enabled`, `redis.image.*`, `redis.auth.{password,existingSecret,existingSecretPasswordKey}`, `redis.master.{resources,nodeSelector,tolerations,affinity}`, `redis.master.persistence.{enabled,size,storageClass,accessModes}` and `global.storageClass`. Keys we don't support are covered by D3.
7. Images: `clone-third-party.sh` becomes the only producer of `ghcr.io/nudgebee/redis` (`7.2.17-alpine`, plus the `7-alpine` tag that compose and llm-gateway already use). Remove the Debian Redis rows from `infra-mirrors-image.yaml:96,177` and `security-scan.yaml:204`.

## 4. Backward-compatibility matrix

| # | Area | Effect on an existing install | Result |
|---|---|---|---|
| B1 | `helm upgrade` mechanics | Same StatefulSet; only the pod template changes | ✅ No error (A3 proves it) |
| B2 | Data | Same PVC, same `/data`, AOF loaded by 7.2 | ✅ Kept |
| B3 | Downtime | One pod restart (same as any image bump today) | ⚠️ About 10–30 s; clients reconnect |
| B4 | PVCs | Same claim `redis-data-redis-master-0`; nothing orphaned | ✅ |
| B5 | Values overrides | All keys the template uses keep their names and defaults, so custom sizes and storage classes render the same frozen fields | ✅ for supported keys; D3 for the rest |
| B6 | Redis 7.0 to 7.2 | go-redis and redis-py are compatible; compose already runs 7.x on Alpine | ✅ |
| B7 | Auth and commands | Same user, password and Secret; FLUSHDB/FLUSHALL stay disabled | ✅ |
| B8 | Pod labels | name/instance/component unchanged; `helm.sh/chart` and `app.kubernetes.io/version` change values | ✅ (selectors don't use them) |
| B9 | External Redis (`redis.enabled=false`) | No change | ✅ (A5) |
| B10 | Rollback to 1.12.2 | Pod template goes back to the Bitnami image on the same PVC; `aof-use-rdb-preamble no` keeps the AOF readable by 7.0 | ✅ (A4 proves it) |
| B11 | Orphaned `redis-replicas` StatefulSet (0 replicas, no PVC) | Helm deletes it | ✅ Harmless |
| B12 | EE / OSS sync | EE `prod` still ships the Bitnami subchart | ⚠️ D2 |

**What could still break, and how the test catches it:** an install with a non-default persistence setting we failed to mirror. Then the frozen fields differ, the upgrade is rejected, and nothing changes (Helm leaves the release on the previous revision). A3 runs with default values and also with `global.storageClass` and `redis.master.persistence.size` overridden.

## 5. Decisions (taken 2026-10-31)

- **D2: where it lands first.** (a) EE `main` first, then the normal pick into OSS. (b) OSS and EE on the same day. *Recommendation: (b), with one shared commit.* These are files the sync carries, so they have to move together.
- **D3: Bitnami values we don't support** (`redis.architecture=replication` with `redis.replica.replicaCount>0`, Sentinel, TLS, `redis.master.persistence.enabled=false` on an install that already has a PVC). (a) Ignore them without comment. (b) Fail the install in `validate-install.yaml` with a message. *Recommendation: (b).* Today's values set `replicaCount: 0`, so a default install never triggers the failure. Anyone who set one of these keys gets a clear message instead of quietly losing HA or TLS.

## 6. Test plan

1. `helm lint` and `helm template` of the new chart. Diff the rendered objects against `main`: only Redis objects change.
2. kind cluster: install the 1.12.2 chart and write test keys. Then `helm upgrade` to the branch (A3), once with default values and once with `global.storageClass` plus `redis.master.persistence.size` overridden. The test keys must survive the upgrade. Check every Redis consumer's logs for connect/auth errors (A2). Write keys on 7.2, then `helm rollback` (A4): the Bitnami 7.0 pod must start and load them.
3. Grype on `ghcr.io/nudgebee/redis:7.2.17-alpine`, and the `scan-deployed` job on the PR (A1).
4. Put the output of each step in the PR body.

## 7. Out of scope

RabbitMQ and PostgreSQL, which get their own specs later in the same order. Redis HA, which nobody runs today (`replica.replicaCount: 0`).
