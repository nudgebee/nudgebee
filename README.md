## Developer Setup

### Softwares

- [Docker](https://www.docker.com/products/docker-desktop/)
- [Python3 & pip](https://www.python.org/downloads/)
- [Nodejs & npm](https://nodejs.org/en/download)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [AWS Cli](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- [psql (DBeaver or similar)](https://dbeaver.io/)
- [Helm](https://helm.sh/)

### Configurations

- Get AWS Credentials
- [Configure Kubectl for EKS](https://docs.aws.amazon.com/eks/latest/userguide/create-kubeconfig.html)
  - `aws sts get-caller-identity` - varify AWS is configured correctly
  - `aws eks update-kubeconfig --region us-east-1 --name nudgebee-dev` update K8s Configs

#### Test Configurations

Varify AWS configured properly

```
aws sts get-caller-identity
```

Varify k8s configured properly

```
kubectl --namespace nudgebee get pods
```

### Accessing Dev Servers

Dev servers can be accessed using port-forwarding using kubectl

- Hasura (Graphql)

```
kubectl --namespace nudgebee port-forward svc/hasura 8080:80
```

- Nudgebee Postgres

```
kubectl --namespace nudgebee port-forward svc/pg-main-proxy-service 5432:5432
```

- Clickhouse

```
kubectl --namespace clickhouse port-forward svc/clickhouse 8123:8123
```

- RabbitMQ

```
kubectl --namespace rabbit port-forward svc/rabbitmq 15672:15672
```

## Project Structure

Each module has its own README with setup and development instructions. Refer to them for module-specific guidance.

### Frontend

| Module | Description | README |
|--------|-------------|--------|
| `app/` | Frontend dashboard (Next.js + React, NextAuth) | [app/README.md](app/README.md) |

### API & GraphQL Layer

| Module | Description | README |
|--------|-------------|--------|
| `api-server/` | GraphQL API layer overview | [api-server/README.md](api-server/README.md) |
| `api-server/services/` | Core backend Go services (Gin) | [api-server/services/README.md](api-server/services/README.md) |
| `api-server/hasura/` | Hasura GraphQL engine, DB migrations (Postgres, Clickhouse, RabbitMQ) | [api-server/hasura/README.md](api-server/hasura/README.md) |

### Data Collection

| Module | Description | README |
|--------|-------------|--------|
| `collector-server/cloud-collector/` | AWS/cloud data collection | [collector-server/cloud-collector/README.md](collector-server/cloud-collector/README.md) |
| `collector-server/otel-collector/` | OpenTelemetry collector | [collector-server/otel-collector/README.md](collector-server/otel-collector/README.md) |
| `collector-server/k8s-collector/app/` | K8s metrics aggregation (Python) | [collector-server/k8s-collector/app/README.md](collector-server/k8s-collector/app/README.md) |
| `collector-server/k8s-collector/relay-server/` | K8s relay gateway (WebSocket) | [collector-server/k8s-collector/relay-server/README.md](collector-server/k8s-collector/relay-server/README.md) |

### ML & LLM

| Module | Description | README |
|--------|-------------|--------|
| `ml-k8s-server/` | ML models & K8s autoscaling | [ml-k8s-server/README.md](ml-k8s-server/README.md) |
| `llm/llm-server/` | LLM inference service | [llm/llm-server/README.md](llm/llm-server/README.md) |
| `llm/code-analysis/` | Code analysis engine | [llm/code-analysis/README.md](llm/code-analysis/README.md) |
| `llm/rag-server/` | RAG (Retrieval Augmented Generation) | [llm/rag-server/README.md](llm/rag-server/README.md) |
| `llm/benchmark/` | LLM benchmarking | [llm/benchmark/README.md](llm/benchmark/README.md) |

### Automation & Operations

| Module | Description | README |
|--------|-------------|--------|
| `auto-pilot/` | Automation & remediation engine | *(no README — see CLAUDE.md)* |
| `runbook-server/` | Runbook management | [runbook-server/README.md](runbook-server/README.md) |
| `ticket-server/` | Ticket management (Jira integration) | [ticket-server/README.md](ticket-server/README.md) |
| `notifications-server/` | Notification delivery (Slack, Teams, etc.) | [notifications-server/README.md](notifications-server/README.md) |

### Deploy & Infrastructure

| Module | Description | README |
|--------|-------------|--------|
| `deploy/kubernetes/` | Helm charts & Kubernetes config files | [deploy/kubernetes/README.md](deploy/kubernetes/README.md) |

### Tests

| Module | Description |
|--------|-------------|
| `app-e2e-tests/` | End-to-end integration tests |

## App Deployments

- Currently using Github Actions for deployment
- Most of the apps are deployed on Kubernetes

## Infrastructure Components

Current Infra Components

### Logs

- Loki

### Ingress

- Nginx Ingress using Network load balancer
- Within Kubernetes `ingress-nginx` namespace

### SSL/TLS

- [Cert Manager](https://cert-manager.io/)
- Within Kubernetes `cert-manager` namespace

### Metrics Collection

- [Prometheus](https://prometheus.io/)
- Within Kubernetes `prometheus` namespace

### Messaging Queue

- [RabbitMQ](https://www.rabbitmq.com/)
- Within Kubernetes `rabbit` namespace

### Analytics DB

- [Clickhouse](https://clickhouse.com)
- Within Kubernetes `clickhouse` namespace

### RDBMS

- Primary DB, Postgres, Deployed on AWS

## Tech Docs -

https://drive.google.com/drive/folders/1l8q5SsZa-lQVCDbV6NQPOE565erullEi?usp=sharing
