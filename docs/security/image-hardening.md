# Container Image Hardening — Driver

**Goal:** `0 CRITICAL / 0 HIGH / 0 MEDIUM` (Trivy) across **every first-party image we publish**, and keep it there.

Scope = images this org **builds and publishes**, across three repos:

| Repo | Images |
|---|---|
| `nudgebee-oss` | services-server, services-server-sidecar, migrations, app, ticket-server, code-analysis-agent, notifications, k8s-collector, cloud-collector-server, cost-server, relay-server, workflow-server, llm-server, rag-server, ml-k8s-server |
| `k8s-agent` | nudgebee-agent, application-profiler-bpf |
| `node-agent` | node-agent |

**Out of scope:** vendored/mirrored third-party images (kubewatch, opencost, otel-collector-contrib, clickhouse, nova, popeye, kube-bench, cert-scanner, curl, trivy, postgres) and discarded build-stage/base images.

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

## Progress log

| Date | Iter | Change | Fixable C/H/M after |
|---|---|---|---|
| 2026-07-09 | 0 | Baseline scan of all 18 published images; tracker + scan workflow added | 3 / 83 / 190 |

_Update this table after every merged iteration PR. Re-run the scan workflow (or `trivy image --ignore-unfixed --severity CRITICAL,HIGH,MEDIUM <image>`) to get the new numbers._
