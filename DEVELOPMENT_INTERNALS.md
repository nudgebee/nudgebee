# Nudgebee Monorepo - Development Internals

This document contains detailed technical specifications, CI/CD patterns, and infrastructure details for the Nudgebee platform. For a high-level overview and navigation, see [GEMINI.md](./GEMINI.md).

---

## 🌳 Environment & Branches

```
main (dev) ─PR─> test (staging) ─PR─> prod (production)
   ↑                   ↑                    ↑
   └─── Backmerge ───┴─── Backmerge ──────┘ (hotfixes)
```

### CI/CD Automation:
- **Every merge to `main`** → Automated PR to `test`.
- **Every merge to `test`** → Automated PR to `prod`.
- **Hotfix Flow:** Direct PR to `prod` → Backmerge to `test` → Backmerge to `main`.

---

## 🐳 Docker Build Process (Standardized)

All services use a multi-stage build process. Images are built only on `prod` or `test` branches in GitHub Actions and pushed to AWS ECR.

**ECR URL:** `740395098545.dkr.ecr.us-east-1.amazonaws.com`
**Tag Format:** `{timestamp}_{git-sha}` (e.g., `2025-11-12T14-30-45_abc123def`)

### Go Services Example
```dockerfile
FROM golang:1.25-alpine AS build
  COPY go.mod go.sum ./
  RUN go mod download
  RUN CGO_ENABLED=0 GOOS=linux go build -o /app/service cmd/*.go

FROM alpine:3.19
  RUN apk add git graphviz
  COPY --from=build /app/service /app/
  EXPOSE 8000
  CMD ["/app/service"]
```

### Python Services Example
```dockerfile
FROM registry.nudgebee.com/nudgebee-ml-base:* AS builder
  COPY poetry.lock pyproject.toml ./
  RUN uv pip install --system --requirements pyproject.toml
  RUN conda clean --all

FROM registry.nudgebee.com/nudgebee-ml-base:*
  COPY --from=builder /opt/conda /opt/conda
  COPY server ./server
  EXPOSE 9999 8081
  CMD ["sh", "-c", "conda run -n myenv gunicorn ... & wait"]
```

---

## 🏗️ CI/CD Workflow Pattern

Each service has workflows located in `.github/workflows/{service}-{env}.yaml`.

### Typical Workflow Steps:
1. **Checkout:** `actions/checkout@v4`.
2. **Environment Setup:** Go/Python/Node installation.
3. **Dependency Resolution:** `go mod download`, `poetry install`, or `npm ci`.
4. **Validation:** Linters, type checks, and unit tests.
5. **Image Build:** (Prod/Test only) Multi-arch Docker build via `buildx`.
6. **Push:** (Prod/Test only) Push to AWS ECR.
7. **Deploy:** (Prod/Test only) Helm upgrade to EKS cluster.

---

## 🗄️ Hasura Migrations & Metadata

### Migrations
- Located in: `api-server/hasura/migrations/default/`.
- **Naming Rule:** Use current timestamp (ms): `$(python3 -c "import time; print(int(time.time() * 1000))")_V{N}_{name}`.
- **Strict Rule:** NEVER use `CREATE INDEX CONCURRENTLY` (unsupported in Hasura transaction blocks). Use plain `CREATE INDEX`.

### Metadata Management
Always test metadata and migrations locally before pushing:
```bash
cd api-server/hasura
hasura metadata apply
hasura migrate apply
```

---

## 🛠️ Troubleshooting & Debugging

### Format/Lint Failures
- **Go:** `make fmt`
- **Python:** `poetry run black .`
- **TypeScript:** `npm run lint2:fix`

### Dependency Issues
- **Go:** `go mod tidy`
- **Python:** `poetry update`
- **Node:** `rm -rf node_modules package-lock.json && npm ci --legacy-peer-deps`

---

## 📝 Important Notes
- **No Invented Tools:** Use only commands that exist in actual Makefiles or CI workflows.
- **Docker as Truth:** If unsure about a service's build process, check its root `Dockerfile`.
- **Validation Before Commit:** Always run the local validation command (`make validate` or equivalent) before committing changes.
