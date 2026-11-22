# Spec: replace the Bitnami PostgreSQL subchart with the official PostgreSQL image

Part of #590 (image-hardening Phase 2). Follows the Redis and RabbitMQ approach (`redis-official-image-spec.md`, `rabbitmq-official-image-spec.md`). Status: **implemented and live-tested (2026-10-31). D1 trixie, D2 accept 90, D3 refresh: decided (an init container since 2026-11-01, see 6b).**

## 1. Goal and acceptance

The umbrella chart deploys `ghcr.io/nudgebee/postgresql:16.1.0-debian-11-r16-nb1` (Bitnami-legacy rebuild of PostgreSQL 16.1): **132 Critical+High** in Grype. It holds all of Nudgebee's data (141Gi on the gke dev cluster), so data safety comes before anything else.

| # | Acceptance check | Evidence |
|---|---|---|
| A1 | Fewer Critical/High findings, and every PostgreSQL-engine CVE gone | `scan-deployed` job |
| A2 | `helm upgrade` from the 1.12.2 chart keeps every database, role and row, every index passes `amcheck`, and needs no manual step | docker-desktop |
| A3 | `helm rollback` to 1.12.2 works on the same data | docker-desktop |
| A4 | Fresh install works, and every service plus the migration Job connects | docker-desktop |
| A5 | `postgresql.enabled=false` (external PostgreSQL) unchanged | render diff |

## 2. Facts (checked 2026-10-31)

**Images (Grype C+H):**

| Image | C+H | Notes |
|---|---|---|
| current Bitnami 16.1 rebuild | 132 | includes about 35 PostgreSQL-engine CVEs (fixed in 16.2–16.15), plus GDAL/PostGIS |
| official `postgres:16.15-bookworm` (Debian 12, glibc 2.36) | 107 | 6 C, 101 H, 2 fixable |
| official `postgres:16.15-trixie` (Debian 13, glibc 2.41) | **90** | 1 C, 89 H, 0 fixable: Debian packages with no upstream fix yet |
| official `postgres:16.15-alpine` (musl) | 0 | **not safe for existing data**, see collation below |

**Collation is the deciding constraint.** The existing databases are `en_US.UTF-8` with the libc provider and `datcollversion 2.31` (Bitnami's Debian 11). Text indexes are ordered by glibc's collation:
- **Alpine (musl)** has no glibc collation. Existing text indexes would be silently mis-ordered: wrong query results with no error. Ruled out for existing installs.
- **glibc 2.31 vs 2.36 vs 2.41:** a 64,000-string `en_US.UTF-8` sort (accents, case, punctuation, symbols, emoji) gave **an identical result (same md5) on all three**. So bookworm and trixie are both safe. The spike confirms it with `amcheck` on real indexes (below).

**PostgreSQL 16.1 → 16.15** is a minor upgrade: same data format, no dump/restore. Release-note actions for 16.2–16.15 are all conditional, and none applies to our schema:
- BRIN bloom and `numeric_minmax_multi_ops` indexes: our one BRIN index, `events_account_starts_brin_idx`, uses the default operator class.
- `btree_gist` on floats, and `ltree`: neither is used.
- Self-referential foreign keys on partitioned tables: none found.
- `pgcrypto` legacy ciphers: the app encrypts in Go, not with pgcrypto ciphers.
- GIN indexes, only "if you suspect corruption" from a 16.1 bug. That bug exists today too; it's not introduced by this change.

**How Bitnami 13.2.25 runs it today** (rendered from `main`):

| Item | Value that must carry over |
|---|---|
| StatefulSet frozen fields | name `postgresql`; selector `app.kubernetes.io/{name: postgresql, instance: <release>, component: primary}`; `serviceName: postgresql-hl`; volumeClaimTemplate `data` (no labels), 50Gi (our values), RWO |
| Data | `PGDATA=/bitnami/postgresql/data`, PVC mounted at `/bitnami/postgresql`, uid 1001, fsGroup 1001 |
| **Config** | `postgresql.conf` and `pg_hba.conf` live **outside** PGDATA (`/opt/bitnami/postgresql/conf`), so the official image won't find them. The chart must render them |
| Effective settings | `listen_addresses='*'`, `port=5432`, `wal_level=replica`, `max_wal_size=400MB`, `max_wal_senders=16`, `wal_keep_size=128MB`, `hot_standby=on`, `log_connections/disconnections/hostname=off`, `client_min_messages=error`, `shared_preload_libraries=pgaudit` (with no `pgaudit.log` set it logs nothing) |
| pg_hba | `md5` for all hosts (v4 and v6), local and loopback |
| Auth | Secret `postgresql`, key `postgres-password` (superuser) |
| Extensions in use | `pgcrypto`, `citext`, `pg_trgm`, `btree_gin` (+ `aws_s3` on RDS only). All except `aws_s3` ship in the official image |
| Sidecar | `postgres-exporter` (our mirror `v0.20.1-nb1`), port 9187, Service `postgresql-metrics` |
| Services | `postgresql` and `postgresql-hl` (5432), `postgresql-metrics` (9187) |

## 2b. Spike results (docker-desktop; only the pod template changed, to official `postgres:16.15-trixie`)

| Check | Result |
|---|---|
| Boots on Bitnami 16.1 data, with `PGDATA` unchanged, uid 1001, and config from a ConfigMap (Bitnami's settings minus `pgaudit`, plus `unix_socket_directories='/tmp'`) | ✅ `database system is ready to accept connections` |
| 50,000 rows: text, `citext` unique, `en_US` and `C` text btrees, trigram GIN, `btree_gin` | ✅ all rows present. **`bt_index_check(..., heapallindexed)` passes on every btree.** Trigram search and `citext` lookup correct |
| Extensions `pgcrypto`, `citext`, `pg_trgm`, `btree_gin`, `amcheck` | ✅ present |
| App role login through the `postgresql` Service; metrics sidecar | ✅ `pg_up 1` |
| **Rollback** to Bitnami 16.1 after writing 500 rows on 16.15 | ✅ boots, all 50,500 rows present, every btree passes `amcheck` |
| Collation version | PostgreSQL notices `datcollversion 2.31` vs actual `2.41` and logs a mismatch warning for each new connection until refreshed. See D3 |

## 3. Design (same pattern as Redis and RabbitMQ)

1. Remove the Bitnami `postgresql` 13.2.25 dependency and render it in `templates/postgresql/`. Keep the same StatefulSet frozen fields, `PGDATA`, uid/fsGroup, Secrets, Services and metrics sidecar.
2. Render `postgresql.conf` and `pg_hba.conf` into a ConfigMap reproducing Bitnami's effective settings, and start `postgres -c config_file=… -c hba_file=…`. Honor Bitnami's config keys (`primary.extendedConfiguration`, `primary.pgHbaConfiguration`, `primary.configuration`, the `postgresql.*` audit/log keys) the same key-by-key way as RabbitMQ.
3. Drop `pgaudit` from `shared_preload_libraries` (the official image doesn't ship it, and nothing is audited today). Reject the install if `audit.pgAuditLog` is set.
4. Fresh installs: the official entrypoint's `initdb` with `--locale=en_US.UTF-8`, matching Bitnami, and `POSTGRES_PASSWORD` from the same Secret.
5. Values audit: honor or reject every Bitnami 13.2.25 key. Replication or read replicas, LDAP, TLS, backup CronJobs, `initdb` scripts and similar are each checked.
6. Images: mirror `postgres:16.15-trixie` (D1), drop the Debian PostgreSQL build from `infra-mirrors-image.yaml`, update `security-scan.yaml`.

## 4. Backward-compatibility matrix

| # | Area | Effect | Status |
|---|---|---|---|
| B1 | `helm upgrade` mechanics | Same StatefulSet, only the pod changes | ✅ by design (to prove: A2) |
| B2 | Data, roles, extensions | Same PGDATA on the same PVC; minor upgrade | ✅ spike |
| B3 | Text-index ordering | Same glibc collation output (2.31 = 2.41); `amcheck` passes | ✅ spike |
| B4 | Config | Bitnami's effective settings and pg_hba carried over | ✅ spike |
| B5 | `pgaudit` | Dropped; it audited nothing with today's settings | ✅ no behaviour change |
| B6 | Rollback to 1.12.2 | Minor-version downgrade on the same data works | ✅ spike |
| B7 | Downtime | One PostgreSQL restart; services reconnect | ⚠️ release note |
| B8 | Collation-version warning | Logged for each new connection until refreshed | D3 |
| B9 | Private-registry and air-gapped installs | Must mirror the new image | ⚠️ release note |
| B10 | Custom Bitnami values | Honor or reject key by key | to build (same method as RabbitMQ) |

## 5. Decisions

- **D1: Base image.** **trixie (90)** or bookworm (107). Both are proven collation-safe. *Recommendation: trixie.* It has fewer findings and longer Debian support.
- **D2: Accept 90 remaining findings.** Getting to 0 needs Alpine, which is unsafe on existing data; an install could only move to it through a dump/restore migration. *Recommendation: accept 90 now.* That's 132 → 90 and every PostgreSQL-engine CVE gone. Track the Alpine dump/restore path separately, if ever.
- **D3: Collation-version warning.** PostgreSQL logs a mismatch warning on each new connection until `ALTER DATABASE … REFRESH COLLATION VERSION` is run. Options:
  - (a) A post-upgrade hook refreshes every database. That's justified by the identical sort results and `amcheck`.
  - (b) Leave the warning and document the manual command.

  *Recommendation: (a)*, with the hook running `amcheck` on text btrees of the `postgres` and app databases before it refreshes, and skipping the refresh if a check fails. *Superseded on 2026-11-01: the same checks now run in an init container, because the hook ran too late (see 6b).*

## 6. Test plan (docker-desktop)

1. Render comparison of the frozen fields against Bitnami for every persistence and naming override, as for RabbitMQ.
2. Upgrade from 1.12.2 with seeded data, then `amcheck`, the D3 hook, a service connection and the migration Job.
3. Rollback to 1.12.2 after writing on 16.15.
4. Fresh install, then the full stack's DB-using services connect.

## 6b. Live test results (docker-desktop, real chart templates, `helm upgrade`)

| Test | Result |
|---|---|
| Render comparison against Bitnami 13.2.25: StatefulSet name, selector, serviceName, PVC template, data volume and mount, PGDATA, and all three Services (ports, node ports, selectors) | ✅ identical (key order aside) in all 20 cases tried: default, `global.storageClass` (named and `-`), size + storageClass, PVC labels/annotations, `existingClaim`, persistence off, `nameOverride`, empty `fullnameOverride`, `commonLabels` (including an `app.kubernetes.io/name` override), `podLabels`, `subPath`, `mountPath` + `postgresqlDataDir`, `primary.service.ports`, `global.postgresql.service.ports`, NodePort, metrics off, `accessModes`, `dataSource` |
| Metrics sidecar (image, env, ports, probes, resources), security contexts, probe timings, affinity, `/dev/shm` | ✅ identical |
| Upgrade 16.1 → 16.15 with 50,000 seeded rows | ✅ every row, `amcheck` passes on every btree, trigram and `citext` lookups correct, all extensions present, app role over the Service, `pg_up 1` |
| Collation hook | ✅ checked 36–39 collatable btree indexes per database with `bt_index_check`, then refreshed every database to 2.41, including ones named `my db` and `q"uote`. Each refresh is confirmed by re-reading the recorded version; no further mismatch warnings. It works to a ~4 min budget so it can't hold `helm upgrade` past Helm's 5 min hook timeout. On a large database it may leave some databases unrefreshed; those keep logging a harmless warning, and the log names them with the command to run later |
| `helm rollback` to 16.1 after writing 500 rows on 16.15 | ✅ boots; all 50,500 rows; every btree passes `amcheck` |
| Fresh install (custom password) | ✅ `en_US.utf8` database, login through the Service with the Secret password, `pg_up 1` |

**Bug found and fixed during testing:** an unconditional `include_dir = 'conf.d'` would stop PostgreSQL from starting when no extended configuration is set. That directory doesn't exist on a ConfigMap mount, whereas Bitnami's image always had one. The line is now written only when there is extended configuration.

**Fixed after review:** the hook's time budget (above); database names with spaces or quotes; and `persistence.enabled=false`, which now mounts an `emptyDir` at the data path because the official image has no writable `/bitnami`.

**Moved from the hook to an init container after a live GKE upgrade (1.12.2 → 1.13.0-rc.4).** A post-upgrade hook only runs after `--wait` returns, and Temporal's schema Job starts with the rollout. Until the hook ran, PostgreSQL refused that Job's `CREATE DATABASE` (`template database "template1" has a collation version mismatch`); the Job recovered only after 4 retries. The same checks now run in a `refresh-collation-version` init container, which starts PostgreSQL on the Unix socket only, so no client connects before the versions are refreshed. On the live install it verified 561 collatable btree indexes across four databases in 3 seconds, and Temporal's schema Job then succeeded on its first attempt. The live run also turned up two things the init container must do that the official entrypoint does for the main container: remove a `postmaster.pid` left by an unclean Bitnami stop (its PID matches the init shell's PID 1, so PostgreSQL treats it as live), and `chmod 700` the group-writable PGDATA Bitnami left.

**Values audit:** 215 Bitnami paths honored, 186 rejected or replication-only, 9 ignored with a stated reason, 0 unclassified.

**Note:** our values set `metrics.prometheusRule.additionalRules`, a key Bitnami 13.2.25 never reads, so those alert rules have never rendered. This chart keeps that behaviour (only `prometheusRule.rules` renders). Moving them to `rules` is a separate change.

## 7. Out of scope

A PostgreSQL major upgrade (17/18), Alpine via dump/restore, and RDS/external databases (unchanged).
