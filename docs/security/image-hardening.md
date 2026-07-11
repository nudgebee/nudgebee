# Container Image Hardening — Driver

**Goal:** `0 CRITICAL / 0 HIGH / 0 MEDIUM` (Trivy) across **every first-party image we publish**, and keep it there.

Scope = images this org **builds and publishes**, across three repos:

| Repo | Images |
|---|---|
| `nudgebee-oss` | services-server, services-server-sidecar, migrations, app, ticket-server, code-analysis-agent, notifications, k8s-collector, cloud-collector-server, cost-server, relay-server, workflow-server, llm-server, rag-server, ml-k8s-server |
| `k8s-agent` | nudgebee-agent, application-profiler-bpf |
| `node-agent` | node-agent |

**Out of scope (Phase 1):** discarded build-stage/base images.

> **Phase 2 (added 2026-07-11):** a live scan of the running `nudgebee-oss` namespace showed the *deployed runtime dependencies* — Temporal, Qdrant, and the `ghcr.io/nudgebee/*` infra mirrors (redis / rabbitmq / postgres / postgres-exporter / nginx / kubewatch) — carry **~35× more fixable findings than all first-party services combined** (~2,000 vs ~61), including ~100 CRITICALs vs 0 on the services. Phase 1 measured ~3% of the deployed attack surface. Phase 2 brings the deployed runtime dependencies into scope. See [Phase 2 — deployed runtime dependencies](#phase-2--deployed-runtime-dependencies).

---

## The core constraint (why this is multi-phase)

Not every CVE is patchable today. Root-cause split of the 672 CRITICAL/HIGH/MEDIUM findings at baseline:

| Root cause | Count | Fixable now | Note |
|---|---|---|---|
| App deps (Go / Python / Node / Rust / .NET) | 205 | **198 (97%)** | dependency bump + rebuild |
| Alpine OS packages | 34 | **34 (100%)** | rebuild on current Alpine |
| Debian OS packages | 242 | **0** | no upstream fix published |
| RedHat / UBI9 OS packages | 137 | 38 | 99 unpatched upstream |
| Ubuntu OS packages | 54 | 6 | 48 unpatched upstream |

~389 findings sit in Debian/UBI9/Ubuntu base-OS packages with **no upstream fix available** (the two `perl-base` CRITICALs are `fix_deferred`/`affected`). You cannot patch your way out of those — the fixed version does not exist yet. Reaching absolute zero on the five affected images therefore requires **migrating their base image** off Debian/UBI9 onto a continuously-patched distro (Wolfi/Chainguard, distroless, or Alpine). That is Iter 3.

---

## Plan

| Iter | Objective | Status |
|---|---|---|
| **0** | Trivy scan workflow + committed baseline + this tracker (measurement ratchet, report-only) | 🟡 in progress |
| **1** | Drive the 13 fully-fixable images to `0/0/0` (Go 1.25.7+, Alpine rebase, dep bumps) | ⬜ |
| **2** | Patch the fixable floor on the 5 base-blocked images (residual = upstream-unfixable only) | ⬜ |
| **3** | Base migration on the 5 blocked images (Debian/UBI9/Ubuntu → Wolfi/distroless/Alpine) for absolute zero | ⬜ |
| **4** | Flip the CI gate to hard-fail on any **fixable** C/H/M; `.trivyignore` (CVE + reason + review-by) for unpatched residual; scheduled re-scan | ⬜ |

---

## Baseline (scanned at Iter 0)

Fixable = has an upstream FixedVersion (patchable now). Unfixable = no fix published yet.
Machine-readable snapshot: [`baseline.json`](baseline.json).

**Totals:** fixable `3 / 83 / 190` · unfixable `4 / 94 / 298` (C/H/M).

### Fully fixable → target `0/0/0` in Iter 1

| Image | Repo | Fixable C/H/M | Unfixable C/H/M | Base |
|---|---|---|---|---|
| services-server | nudgebee-oss | 0/0/0 | 0/0/0 | alpine |
| services-server-sidecar | nudgebee-oss | 0/0/0 | 0/0/0 | alpine |
| cost-server | nudgebee-oss | 0/0/0 | 0/0/0 | alpine |
| k8s-collector | nudgebee-oss | 0/0/0 | 0/0/0 | alpine |
| relay-server | nudgebee-oss | 0/0/0 | 0/0/0 | alpine |
| ticket-server | nudgebee-oss | 0/0/0 | 0/0/0 | alpine |
| cloud-collector-server | nudgebee-oss | 0/1/1 | 0/0/0 | alpine |
| code-analysis-agent | nudgebee-oss | 0/1/1 | 0/0/0 | alpine |
| workflow-server | nudgebee-oss | 0/3/0 | 0/0/0 | alpine |
| app | nudgebee-oss | 0/2/7 | 0/0/0 | node-alpine |
| notifications | nudgebee-oss | 0/2/14 | 0/0/0 | alpine |
| application-profiler-bpf | k8s-agent | 0/9/18 | 0/0/0 | alpine |
| nudgebee-agent | k8s-agent | 1/20/36 | 0/0/0 | alpine |

### Base-blocked → fixable floor in Iter 2, absolute zero in Iter 3

| Image | Repo | Fixable C/H/M | Unfixable C/H/M | Base |
|---|---|---|---|---|
| llm-server | nudgebee-oss | 0/0/3 | 0/1/29 | ubuntu-noble |
| migrations | nudgebee-oss | 0/0/3 | 0/0/18 | ubuntu:24.04 |
| node-agent | node-agent | 1/29/64 | 0/16/86 | UBI9-minimal |
| ml-k8s-server | nudgebee-oss | 0/1/1 | 2/37/82 | debian (ml-base) |
| rag-server | nudgebee-oss | 1/15/42 | 2/40/83 | debian (ml-base) |

### The 7 CRITICALs

| Image | CVE | Package | Fix |
|---|---|---|---|
| node-agent | CVE-2025-68121 | Go stdlib 1.25.0 | Go 1.25.7+ ✅ fixable |
| nudgebee-agent | CVE-2025-68121 | Go stdlib 1.23.4 | Go 1.25.7+ ✅ fixable |
| rag-server | CVE-2025-14009 | nltk 3.9.1 | nltk 3.9.3 ✅ fixable |
| ml-k8s-server | CVE-2026-42496 | perl-base 5.40.1-6 | ❌ no fix (Debian `fix_deferred`) |
| ml-k8s-server | CVE-2026-8376 | perl-base 5.40.1-6 | ❌ no fix (Debian `affected`) |
| rag-server | CVE-2026-42496 | perl-base 5.40.1-6 | ❌ no fix (Debian `fix_deferred`) |
| rag-server | CVE-2026-8376 | perl-base 5.40.1-6 | ❌ no fix (Debian `affected`) |

---

## Phase 2 — deployed runtime dependencies

Phase 1 hardened the images we **build**. A live Trivy scan of every image **running** in the
`nudgebee-oss` namespace (2026-07-11, 26 unique images incl. initContainers) showed those are a
small slice of the real attack surface. Full per-image + CRITICAL detail: the namespace scan report
(`nudgebee-oss-namespace-scan-2026-07-11`).

### What the live scan found

**Fleet:** 26 images · **2,148 fixable** + 526 unfixable C/H/M · **104 CRITICAL**.

| Category | Images | Fixable C/H/M | CRIT | Ownership |
|---|---|---|---|---|
| First-party services | 13 | ~61 | 0 | we build — Phase 1 |
| First-party agents (`nudgebee-agent`, `code-analysis-agent`) | 2 | ~89 | 1 | we build (agent repo / `:latest` tag) |
| **First-party infra mirrors** (redis, rabbitmq, postgres, postgres-exporter, nginx, kubewatch) | 7 | **~805** | ~53 | **we publish the `ghcr.io/nudgebee/*` mirror** |
| **Third-party** (Temporal ×3, Qdrant) | 4 | **~1,193** | 48 | upstream builds it |

Worst single offenders: `temporalio/admin-tools:1.29.1` (506 / 24 CRIT), `temporalio/server:1.29.1`
(357 / 15 CRIT), the debian-11 bitnami mirrors (`postgres-exporter`/`redis`/`postgresql` ~150–290
each). The mirror CRITICALs are stale **debian-11 (bullseye)** OS packages; the Temporal CRITICALs
are bundled Go deps (grpc, pgx, stdlib) only a newer Temporal release fixes.

### What "done" means here (differs by who builds the image)

- **Images we build** (services, agents, and the mirrors we bring into OSS): drive to **0 fixable
  C/H/M** modulo a documented `.trivyignore` vendored floor → **enforce** gate.
- **Images upstream owns** (Temporal, Qdrant): we can only **bump to the latest patched tag + pin**.
  Residual is **report-only** — never a hard gate, because we can't patch upstream on our schedule.

### Decisions taken (2026-07-11)

- **Infra mirrors → build them in OSS.** The `ghcr.io/nudgebee/*` mirrors are not built in this repo
  today (published by the enterprise/infra pipeline, only referenced in `values.yaml`). We will add
  GHCR publish workflows here — same pattern as the python / cloud-collector / llm / ml base-image
  workflows — that build a current bitnami tag (debian-12/13) with the weekly `OS_PKG_EPOCH` refresh.

### Workstreams

| WS | Scope | Fixable | Approach | Risk |
|---|---|---|---|---|
| **WS1** | First-party services | ~61 | Land #587/#588/#589; rag-server dep bump (safe transitive now, hold pypdf/transformers majors); `.trivyignore` vendored floor → flip services gate to **enforce** | low |
| **WS2** | Agents | ~89 | Re-point `code-analysis-agent` deploy off `:latest` to the hardened edge tag; `nudgebee-agent` bump + release in the agent repo (with node-agent #291) | low–med · cross-repo |
| **WS3** | Infra mirrors | ~805 | Add OSS publish workflows building current bitnami (debian-12/13) within the same app major; **test stateful DBs in `nudgebee-oss-qa` before prod** | **med–high** (stateful) |
| **WS4** | Temporal + Qdrant | ~1,193 | Bump Temporal chart `0.72.0`→latest (newer server/ui/admin-tools) and Qdrant `v1.16.0`→latest; test in qa; residual report-only | med (compat) |
| **WS5** | Gate + guardrail | — | Add all deployed images to `security-scan.yaml` as a **report-only** second matrix group; first-party group flips to **enforce**; third-party/mirror group stays report-only with a documented upstream floor | low |

### Phase 2 plan

| Iter | Objective | Status |
|---|---|---|
| **5** | WS5 report-only scan expansion (measure the whole deployed surface every run) | ⬜ |
| **6** | WS1 finish + services gate → enforce (+ `.trivyignore` vendored floor) | ⬜ |
| **7** | WS3 infra-mirror publish workflows on debian-12/13 (qa-tested) | ⬜ |
| **8** | WS4 Temporal + Qdrant bumps (qa-tested); WS2 agent bumps | ⬜ |

**Recommended order:** WS5 → WS1 → WS3 → WS4/WS2. Do WS5 first so progress on the big buckets is
measured every run.

---

## Progress log

| Date | Iter | Change | Fixable C/H/M after |
|---|---|---|---|
| 2026-07-09 | 0 | Baseline scan of all 18 published images; tracker + scan workflow added | 3 / 83 / 190 |
| 2026-07-10 | 1 | Go toolchain 1.26.4→1.26.5 across all 8 Go images (CVE-2026-42505 stdlib); `GOTOOLCHAIN=local` on all builders (#587 merged) | fleet 88 fixable |
| 2026-07-11 | 1 | migrations `migrate` builder Go 1.26.5 (#588, open); committed `OS_PKG_EPOCH` cache-bust for c-ares + gzip/tar across 7 Dockerfiles (#589, open) | services → ~61 fixable |
| 2026-07-11 | 2 | **Live scan of running `nudgebee-oss` namespace** (26 images) → deployed third-party surface is ~2,000 fixable / ~100 CRIT, ~35× the first-party services. Phase 2 opened. | fleet 2,148 fixable |

_Update this table after every merged iteration PR. Re-run the scan workflow (or `trivy image --ignore-unfixed --severity CRITICAL,HIGH,MEDIUM <image>`) to get the new numbers._
