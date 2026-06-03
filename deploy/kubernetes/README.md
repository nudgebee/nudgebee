# Deploying Nudgebee on Kubernetes

This directory holds the Helm charts that install Nudgebee on any Kubernetes cluster. **For the quickest install, see the [root README's "Deploy to Kubernetes (Helm)"](../../README.md#deploy-to-kubernetes-helm) section** — it walks through the OCI install in five commands.

This document is the **production reference**: required values, toggleable subcharts, bringing your own infrastructure, ingress + TLS, upgrade flow, and troubleshooting.

> The chart architecture (umbrella pattern, build pipeline, how subcharts are wired) is documented separately in [`NB_HELM_CHART_GUIDE.md`](./NB_HELM_CHART_GUIDE.md). Read that if you're modifying the charts themselves rather than installing them.

## What's in this directory

```
deploy/kubernetes/
├── nudgebee/                       # Umbrella chart — what users actually install
│   ├── Chart.yaml                  # Declares all subchart dependencies
│   ├── values.yaml                 # Default values across the platform
│   ├── values-enterprise.yaml      # Enterprise overrides
│   └── templates/                  # Cross-cutting templates (secrets, migrations)
│
├── app/                            # Frontend (Next.js)
├── services-server/                # Core Go backend
├── ticket-server/                  # Ticket lifecycle service
├── notifications/                  # Notification delivery
├── workflow-server/                # Runbook / workflow orchestration
├── llm-server/                     # LLM session state + routing
├── rag-server/                     # RAG retrieval against Qdrant
├── ml-k8s-server/                  # ML pipelines for rightsizing + anomaly
├── cloud-collector-server/         # Cloud-scan collector
├── k8s-collector/                  # K8s metrics collector
├── relay-server/                   # K8s relay gateway
├── benchmark-server/               # LLM benchmark harness
├── migrations/                     # Database migration job (post-install/post-upgrade hook)
│
├── nudgebee-qdrant-server/         # Qdrant vector DB (local chart over upstream image)
├── nudgebee-python-base-image/     # Shared Python base image
├── nudgebee-ml-base-image/         # Shared ML base image
├── nudgebee-cloud-collector-base-image/
├── nudgebee-debug/                 # Debug image
├── otel/                           # OpenTelemetry collector
└── NB_HELM_CHART_GUIDE.md          # Chart architecture for contributors
```

The umbrella chart (`nudgebee/`) is what end users install. It declares the per-service charts (under `file://../<service>/`) plus external subcharts (Postgres, RabbitMQ, Redis, ClickHouse, Temporal) as dependencies.

## Required configuration

The chart's `$knownKeys` validator (in `nudgebee/templates/secrets-nudgebee.yaml`) rejects unknown keys at install time. Misspelled values fail loud, not silent.

### Always-required

| Value                                       | Why required                                                                         |
|---------------------------------------------|--------------------------------------------------------------------------------------|
| `nudgebee_secret.NUDGEBEE_ENCRYPTION_KEY`   | 32-byte hex (`openssl rand -hex 32`). Encrypts integration credentials at rest. **Losing it makes previously encrypted DB rows unreadable** — generate once, store in your secret manager. The chart `fail`s if empty or left as `__REPLACE__`. |
| `nudgebee_secret.BASE_URL`                  | Public URL the app is reachable at (e.g. `https://nudgebee.acme.io`). Used as the OAuth redirect base and in email links. |

`NEXTAUTH_SECRET` and `ACTION_API_SERVER_TOKEN` are **auto-generated** by the chart on fresh installs (lookup-or-generate pattern) and reused on upgrade — you don't need to set them. GitOps users who do offline render (Argo CD, Flux) must set them explicitly because `lookup` returns empty.

### Often-set

| Value                                       | Purpose                                                                   |
|---------------------------------------------|---------------------------------------------------------------------------|
| `nudgebee_secret.NUDGEBEE_LICENSE`          | License JWT (commercial / EE). Leave empty for OSS — server short-circuits to `licenseType=free`. |
| `nudgebee_secret.LICENSE_PUBLIC_KEY`        | RSA public key for verifying the license JWT. Empty for OSS.              |
| `nudgebee_secret.EMAIL_*`                   | SMTP host / port / user / password / from-address for outbound email     |
| `global.imagePullSecrets`                   | If pulling images from a private registry                                 |

For the full set of values + their defaults, see [`nudgebee/values.yaml`](./nudgebee/values.yaml).

## Toggleable subcharts

Each major subchart is gated by an `enabled` flag in the umbrella `values.yaml`. Defaults shown:

| Subchart                  | Default     | Notes                                                                    |
|---------------------------|-------------|--------------------------------------------------------------------------|
| `postgresql`              | `true`      | Primary RDBMS. Disable + point `APP_DATABASE_URL` at an external Postgres |
| `rabbitmq`                | `true`      | Message bus. Disable + set `RABBIT_MQ_*` env vars for an external broker  |
| `redis`                   | `true`      | Cache. Disable + set `REDIS_SERVER_*` for an external Redis               |
| `temporal`                | `true`      | Workflow engine. Disable only if the workflow-server is also disabled     |
| `nudgebee-qdrant-server`  | `true`      | Vector DB for RAG. Disable if the LLM / RAG features aren't used          |
| `clickhouse`              | `false`     | Analytics / observability. Enable for trace + log analytics pipelines     |

Each Nudgebee service (e.g. `services-server`, `ticket-server`) is similarly gated by an `enabled` flag — disable any service you don't need.

## Bringing your own infrastructure

The pattern is the same for every bundled subchart: disable it, then set the connection env vars in `nudgebee_secret`.

```yaml
# values-prod.yaml
postgresql:
  enabled: false

nudgebee_secret:
  APP_DATABASE_URL: 'postgresql://user:pass@your-rds.example.com:5432/nudgebee?sslmode=require'
  # If you change APP_DATABASE_URL, you must also update the temporal.server.config.persistence
  # section so Temporal points at the same Postgres host.
```

Apply at install time:

```bash
helm install nudgebee oci://ghcr.io/nudgebee/charts/nudgebee \
  -n nudgebee --create-namespace \
  -f values-prod.yaml \
  --wait --timeout 20m
```

Equivalents for the other bundled subcharts:

| Disabling | Set in `nudgebee_secret`                                                |
|-----------|-------------------------------------------------------------------------|
| Postgres  | `APP_DATABASE_URL` (+ matching Temporal persistence config)             |
| RabbitMQ  | `RABBIT_MQ_HOST` / `_PORT` / `_USERNAME` / `_PASSWORD`                  |
| Redis     | `REDIS_SERVER_HOST` / `_PORT` / `REDIS_USER_NAME` / `REDIS_USER_PASSWORD` |
| ClickHouse | `CLICKHOUSE_HOST` / `CLICKHOUSE_USER` / `CLICKHOUSE_PASSWORD`          |
| Qdrant    | (set the Qdrant client URL via the relevant service's env)              |

## Ingress + TLS

The umbrella chart does not bundle an ingress controller or cert-manager — install them once per cluster, then write your own `Ingress` resource that points at the `app` service.

### Install ingress-nginx (one-time per cluster)

```bash
helm upgrade --install ingress-nginx ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  -n ingress-nginx --create-namespace
```

Reference: <https://kubernetes.github.io/ingress-nginx/deploy/>

### Install cert-manager (one-time per cluster)

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update

helm install cert-manager jetstack/cert-manager \
  -n cert-manager --create-namespace \
  --version v1.15.0 \
  --set installCRDs=true
```

Reference: <https://cert-manager.io/docs/installation/helm/>

### Example Ingress for Nudgebee

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: nudgebee
  namespace: nudgebee
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: 50m
spec:
  ingressClassName: nginx
  tls:
    - hosts: [nudgebee.example.com]
      secretName: nudgebee-tls
  rules:
    - host: nudgebee.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: app
                port:
                  number: 80
```

Remember to also set `nudgebee_secret.BASE_URL=https://nudgebee.example.com` in the chart values so the app advertises the correct public URL.

## Production checklist

Before flipping production traffic onto the install:

- [ ] **`NUDGEBEE_ENCRYPTION_KEY` is stored in your secret manager** (not just in values.yaml). Lose this and every encrypted DB row becomes unreadable.
- [ ] **`BASE_URL` matches the ingress host.** Wrong value breaks OAuth redirects + email links.
- [ ] **Postgres backup is configured.** Bundled `postgresql.enabled=true` is fine for getting started but doesn't ship with automated backups — for prod, point at an external managed Postgres.
- [ ] **Resource requests/limits set** on at least `services-server`, `llm-server`, `rag-server` to avoid OOM under load.
- [ ] **Pod-level network policies** restrict pod-to-pod traffic to expected paths.
- [ ] **Observability sidecars** (Datadog / NewRelic agents / Prometheus exporters) wired in if you run them — Nudgebee already exposes Prometheus metrics on each service's `/metrics`.
- [ ] **HPA / KEDA** (optional) — bundled charts don't add autoscalers by default.
- [ ] **Image registry mirrored**, if you don't want to fetch from `ghcr.io/nudgebee` at deploy time.
- [ ] **For GitOps (Argo CD / Flux):** explicitly set `NEXTAUTH_SECRET` and `ACTION_API_SERVER_TOKEN` — offline rendering can't use the chart's lookup-or-generate fallback.

## Upgrade flow

```bash
helm upgrade nudgebee oci://ghcr.io/nudgebee/charts/nudgebee \
  -n nudgebee \
  -f values-prod.yaml \
  --wait --timeout 20m
```

A post-install / post-upgrade Helm hook runs DB migrations via golang-migrate (see [`api-server/migrations/README.md`](../../api-server/migrations/README.md)). If the migration job fails, the Helm release is marked failed — fix the root cause and retry the upgrade. Pass `--atomic` if you want Helm to automatically roll back on failure.

To pin a specific chart version, add `--version <X.Y.Z>`. `helm history nudgebee -n nudgebee` shows past releases for rollback (`helm rollback nudgebee <revision>`).

## Troubleshooting

**Install fails with `[ERROR] Unknown key '<NAME>' found in nudgebee_secret`**
The chart rejects misspelled or stale keys at install time. Either correct the key name or move it to `additional_env_vars` (the escape hatch).

**Install fails with `[ERROR] NUDGEBEE_ENCRYPTION_KEY is required`**
The chart refuses to auto-generate this — it's the single value you must persist outside the cluster. Generate one with `openssl rand -hex 32` and pass via `--set nudgebee_secret.NUDGEBEE_ENCRYPTION_KEY=<value>`. Upgraders coming from old defaults: see the chart error message for the migration value.

**Pods stuck `Pending` because of pull-secret errors**
Set `global.imagePullSecrets` and provide credentials via `nudgebee_registry_secret`, or pre-create the pull secret in the `nudgebee` namespace and reference it by name.

**App shows wrong OAuth redirect / email links**
`nudgebee_secret.BASE_URL` doesn't match the ingress host. Update + re-run `helm upgrade`.

**Bootstrap admin password**
On a fresh install, the chart provisions a dummy-credentials admin so you can log in once. Retrieve with:
```bash
kubectl -n nudgebee get secret nudgebee \
  -o jsonpath='{.data.NEXTAUTH_DUMMY_CREDS_PASSWORD}' | base64 -d
```

## Going deeper

- [`NB_HELM_CHART_GUIDE.md`](./NB_HELM_CHART_GUIDE.md) — chart architecture: umbrella pattern, external vs internal dependencies, image build pipeline (read this if modifying charts).
- [`nudgebee/values.yaml`](./nudgebee/values.yaml) — every available knob with inline comments.
- [`api-server/migrations/README.md`](../../api-server/migrations/README.md) — DB migration mechanics + recovery from `dirty=true`.
- Root [`README.md`](../../README.md) — quickstart, local-dev compose, architecture diagram.
