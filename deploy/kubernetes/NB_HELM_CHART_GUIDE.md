# Nudgebee Helm Chart Architecture Guide

## Overview

Nudgebee uses a **Helm Umbrella Chart** pattern where the main `nudgebee` chart includes multiple services as dependencies. This guide explains how the charts are built and how to add new services.

## Chart Architecture

### Main Nudgebee Chart
Location: `/deploy/kubernetes/nudgebee/`

The main chart aggregates all services and external dependencies into a single deployable unit.

**Key Files:**
- `Chart.yaml` - Defines all chart dependencies
- `values.yaml` - Default configuration for all services
- `charts/` - Contains packaged .tgz files of all dependencies
- `templates/` - Common Kubernetes resources (secrets, config maps)

### Dependency Types

#### 1. **External Dependencies** (From Public Registries)
Services NOT managed by Nudgebee, using official Helm charts.

**Examples:** PostgreSQL, RabbitMQ, Redis, ClickHouse

**Pattern:**
```yaml
# Chart.yaml
dependencies:
  - name: postgresql
    repository: oci://registry-1.docker.io/bitnamicharts
    version: '13.2.25'
    condition: postgresql.enabled
```

**Characteristics:**
- Use OCI or HTTPS repositories
- No local chart directory needed
- Only environment-specific values files in `/deploy/kubernetes/{service}/`
- Pull official Docker images from public registries

#### 2. **Internal Services** (Nudgebee-Managed)
Custom services built from Nudgebee source code.

**Examples:** llm-server, rag-server, api-server, app

**Pattern:**
```yaml
# Chart.yaml
dependencies:
  - name: rag-server
    repository: file://../rag-server/
    version: '0.1'
    condition: rag-server.enabled
```

**Characteristics:**
- Use `file://` local repository paths
- Full Helm chart in `/deploy/kubernetes/{service}/`
- Custom Docker images built in CI/CD workflows
- Images pushed to AWS ECR registry

#### 3. **External Services with Local Charts** (Hybrid Pattern)
External services using official Docker images but with custom Kubernetes manifests.

**Example:** nudgebee-qdrant-server

**Pattern:**
```yaml
# Chart.yaml
dependencies:
  - name: nudgebee-qdrant-server
    repository: file://../nudgebee-qdrant-server/
    version: '1.0.0'
    condition: nudgebee-qdrant-server.enabled
```

**Characteristics:**
- Local Helm chart with custom templates
- Uses official public Docker images
- No custom Docker build workflow needed
- Allows full control over Kubernetes resources

---

## How Charts Are Built

### Build Process Flow

```
┌─────────────────────────────────────────────────────┐
│  1. Trigger: Git Tag (e.g., 1.2.3-snapshot)        │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│  2. Fetch Latest Images from AWS ECR                │
│     - Query each ECR repo for latest image tag      │
│     - Update values.yaml with image tags            │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│  3. Clean Environment-Specific Values               │
│     - Remove values-dev.yaml, values-test.yaml      │
│     - Remove values-prod.yaml from each service     │
│     - Keep only default values.yaml                 │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│  4. Package Individual Charts                       │
│     - helm package ../app -d ./charts/              │
│     - helm package ../rag-server -d ./charts/       │
│     - ... (for each service)                        │
│     - Creates .tgz files in charts/ directory       │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│  5. Update Chart Version                            │
│     - Set version to git tag (e.g., 1.2.3-snapshot) │
│     - Update Chart.yaml and appVersion              │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│  6. Package Main Nudgebee Chart                     │
│     - helm package nudgebee                         │
│     - Creates nudgebee-{version}.tgz                │
└─────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────┐
│  7. Push to AWS ECR                                 │
│     - helm push to OCI registry                     │
│     - Available for deployment                      │
└─────────────────────────────────────────────────────┘
```

### Workflow Files

Three workflows handle building for different environments:

1. **nudgebee-build-dev.yaml** - Development builds (triggered on snapshot tags)
2. **nudgebee-build-test.yaml** - Test environment builds
3. **nudgebee-build-prod.yaml** - Production builds (triggered on release tags)

---

## Adding a New Service

### Decision Tree: What Type of Service?

```
Is it an external service (PostgreSQL, Redis, etc.)?
│
├─ YES → Do you need custom Kubernetes manifests?
│         │
│         ├─ NO → Use External Dependency Pattern (Option A)
│         │
│         └─ YES → Use Hybrid Pattern (Option C)
│
└─ NO → It's a Nudgebee service
          → Use Internal Service Pattern (Option B)
```

---

## Option A: Adding External Dependency (e.g., New Database)

**Use Case:** Adding PostgreSQL, Redis, Kafka, etc. from official charts

### Step 1: Add Dependency to Chart.yaml

```yaml
# /deploy/kubernetes/nudgebee/Chart.yaml
dependencies:
  # ... existing dependencies ...
  - name: mongodb
    repository: oci://registry-1.docker.io/bitnamicharts
    version: '14.12.0'
    condition: mongodb.enabled
```

### Step 2: Add Configuration to values.yaml

```yaml
# /deploy/kubernetes/nudgebee/values.yaml
mongodb:
  enabled: true
  fullnameOverride: mongodb
  auth:
    existingSecret: 'mongodb'  # Use existing secret for passwords
  image:
    registry: registry.nudgebee.com
    repository: mongodb
    tag: '7.0.5-debian-12'
  persistence:
    size: 50Gi
```

### Step 3: (Optional) Create Environment-Specific Values

```bash
mkdir -p /deploy/kubernetes/mongodb
```

Create `values-dev.yaml`, `values-test.yaml`, `values-prod.yaml`:

```yaml
# /deploy/kubernetes/mongodb/values-test.yaml
persistence:
  size: 100Gi
resources:
  limits:
    memory: 4Gi
```

### Step 4: Fetch Dependencies

```bash
cd deploy/kubernetes/nudgebee
helm dependency build
```

**That's it!** No workflow changes needed for external dependencies.

---

## Option B: Adding Internal Nudgebee Service

**Use Case:** New custom service built from Nudgebee source code

### Prerequisites
- Service source code exists (e.g., `/llm/new-service/`)
- Dockerfile exists for building the service
- CI/CD workflow builds and pushes Docker image to ECR

### Step 1: Create Helm Chart Directory

```bash
mkdir -p /deploy/kubernetes/new-service/templates
```

### Step 2: Create Chart.yaml

```yaml
# /deploy/kubernetes/new-service/Chart.yaml
apiVersion: v2
name: new-service
description: A Helm chart for New Service
type: application
version: 0.1.0
appVersion: "1.0.0"
```

### Step 3: Create values.yaml

```yaml
# /deploy/kubernetes/new-service/values.yaml
replicaCount: 1

image:
  repository: registry.nudgebee.com/nudgebee-new-service
  pullPolicy: Always
  tag: 'latest'

imagePullSecrets:
  - name: nudgebee-registry-secret

service:
  type: ClusterIP
  port: 8080

resources:
  limits:
    cpu: 1000m
    memory: 2Gi
  requests:
    cpu: 500m
    memory: 1Gi

labels:
  app: new-service
```

### Step 4: Create Kubernetes Templates

Create at minimum:
- `templates/_helpers.tpl` - Helper functions
- `templates/deployment.yaml` - Main deployment
- `templates/service.yaml` - Service definition
- `templates/serviceaccount.yaml` - Service account (if needed)

**Tip:** Copy templates from an existing service like `rag-server` and modify.

### Step 5: Create Environment-Specific Values

```yaml
# /deploy/kubernetes/new-service/values-dev.yaml
image:
  repository: registry.dev.nudgebee.pollux.in/nudgebee-new-service-dev
  tag: 'latest'

# /deploy/kubernetes/new-service/values-test.yaml
image:
  repository: registry.dev.nudgebee.pollux.in/nudgebee-new-service-test
  tag: 'latest'

# /deploy/kubernetes/new-service/values-prod.yaml
image:
  repository: registry.nudgebee.com/nudgebee-new-service
  tag: 'latest'
resources:
  limits:
    cpu: 2000m
    memory: 4Gi
```

### Step 6: Add to Main Chart.yaml

```yaml
# /deploy/kubernetes/nudgebee/Chart.yaml
dependencies:
  # ... existing dependencies ...
  - name: new-service
    version: '0.1.0'
    repository: file://../new-service/
    condition: new-service.enabled
```

### Step 7: Add to Main values.yaml

```yaml
# /deploy/kubernetes/nudgebee/values.yaml
new-service:
  enabled: true
  fullnameOverride: new-service
  image:
    repository: nudgebee-new-service
    tag: 'latest'
```

### Step 8: Update ALL Build Workflows

**CRITICAL:** You must update 3 workflow files.

#### File 1: nudgebee-build-dev.yaml

Add image fetching (after line ~96):
```yaml
new_service_image=`aws ecr describe-images --repository-name nudgebee-new-service \
  --query 'reverse(sort_by(imageDetails[?imageTags], &imagePushedAt))[0].{Tag: imageTags[0], PushedAt: imagePushedAt}' \
  --region us-east-1 --output json | jq -r .Tag`
echo "new_service_image: $new_service_image"
yq -i ".new-service.image.tag=$new_service_image" values.yaml
```

Add values cleanup (after line ~111):
```bash
rm -rf ../new-service/values-*.yaml
```

Add chart packaging (after line ~127):
```bash
helm package ../new-service -d ./charts/
```

#### File 2: nudgebee-build-test.yaml

Repeat the same 3 additions as dev.yaml.

#### File 3: nudgebee-build-prod.yaml

Repeat the same 3 additions.

### Step 9: Test Locally

```bash
cd deploy/kubernetes/nudgebee
helm dependency build
helm template nudgebee . --debug
```

---

## Option C: Adding External Service with Local Chart (Hybrid)

**Use Case:** Using official image (Qdrant, MinIO) but need custom K8s manifests

This is a combination of Options A and B.

### Step 1: Create Local Helm Chart

Follow Option B steps 1-4 to create the chart structure.

### Step 2: Use Official Docker Image

```yaml
# /deploy/kubernetes/nudgebee-new-service/values.yaml
image:
  repository: official/image-name  # NOT registry.nudgebee.com
  pullPolicy: IfNotPresent
  tag: "v1.2.3"
```

### Step 3: Add to Main Chart

```yaml
# /deploy/kubernetes/nudgebee/Chart.yaml
dependencies:
  - name: nudgebee-new-service
    version: '1.0.0'
    repository: file://../nudgebee-new-service/
    condition: nudgebee-new-service.enabled
```

### Step 4: Update Build Workflows

Add to all 3 workflow files (dev, test, prod):

```bash
# Add values cleanup
rm -rf ../nudgebee-new-service/values-*.yaml

# Add chart packaging
helm package ../nudgebee-new-service -d ./charts/
```

**Important:** Do NOT add image fetching from ECR since it uses a public image!

---

## Workflow Modification Checklist

When adding a new **internal service** or **hybrid service**, update:

### ✅ All 3 Build Workflows

**File Locations:**
- `.github/workflows/nudgebee-build-dev.yaml`
- `.github/workflows/nudgebee-build-test.yaml`
- `.github/workflows/nudgebee-build-prod.yaml`

**Changes Needed:**

1. **Image Tag Fetching** (Internal services only)
   ```bash
   service_image=`aws ecr describe-images --repository-name nudgebee-{service} \
     --query 'reverse(sort_by(imageDetails[?imageTags], &imagePushedAt))[0].{Tag: imageTags[0], PushedAt: imagePushedAt}' \
     --region us-east-1 --output json | jq -r .Tag`
   yq -i ".{service}.image.tag=$service_image" values.yaml
   ```

2. **Values Cleanup**
   ```bash
   rm -rf ../{service}/values-*.yaml
   ```

3. **Chart Packaging**
   ```bash
   helm package ../{service} -d ./charts/
   ```

### ✅ Chart Files

- `/deploy/kubernetes/nudgebee/Chart.yaml` - Add dependency
- `/deploy/kubernetes/nudgebee/values.yaml` - Add configuration
- `/deploy/kubernetes/{service}/` - Create chart directory

---

## Directory Structure Reference

```
deploy/kubernetes/
├── nudgebee/                          # Main umbrella chart
│   ├── Chart.yaml                     # All dependencies defined here
│   ├── values.yaml                    # Default config for all services
│   ├── charts/                        # Packaged .tgz files
│   │   ├── app-0.1.tgz
│   │   ├── rag-server-0.1.tgz
│   │   ├── nudgebee-qdrant-server-1.0.0.tgz
│   │   ├── postgresql-13.2.25.tgz    # External (Bitnami)
│   │   └── ...
│   └── templates/                     # Common resources
│       ├── secret.yaml
│       └── configmap.yaml
│
├── rag-server/                        # Internal service chart
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── values-dev.yaml
│   ├── values-test.yaml
│   ├── values-prod.yaml
│   └── templates/
│       ├── _helpers.tpl
│       ├── deployment.yaml
│       ├── service.yaml
│       └── serviceaccount.yaml
│
├── nudgebee-qdrant-server/            # Hybrid (external image, local chart)
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── values-dev.yaml
│   ├── values-test.yaml
│   ├── values-prod.yaml
│   └── templates/
│       ├── _helpers.tpl
│       ├── statefulset.yaml
│       └── service.yaml
│
└── rabbitmq/                          # External dependency (values only)
    ├── values-dev.yaml
    ├── values-test.yaml
    └── values-prod.yaml
```

---

## Common Patterns

### Naming Conventions

- **Chart names:** lowercase, hyphenated (e.g., `rag-server`)
- **Docker images:** `nudgebee-{service}` (e.g., `nudgebee-rag-server`)
- **Kubernetes resources:** Use `fullnameOverride` for consistency
- **Values keys:** Match chart names in Chart.yaml

### Image Tagging Strategy

**Development:**
- Format: `{timestamp}_{git-sha}`
- Example: `2024-12-17T10-30-45_abc123def`
- Always "latest" in workflow queries

**Production:**
- Same format as dev
- Fixed tag per release

### Resource Limits Best Practices

```yaml
resources:
  limits:
    cpu: 1000m      # Maximum CPU (1 core)
    memory: 2Gi     # Maximum memory
  requests:
    cpu: 500m       # Reserved CPU (0.5 core)
    memory: 1Gi     # Reserved memory
```

**Rules:**
- Requests ≤ Limits
- Test environment: Similar to prod
- Dev environment: Can be lower
- Prod environment: Based on actual usage

---

## Testing Changes

### Local Testing

```bash
# 1. Update dependencies
cd deploy/kubernetes/nudgebee
helm dependency build

# 2. Validate template rendering
helm template nudgebee . --debug

# 3. Lint the chart
helm lint .

# 4. Check specific service
helm template nudgebee . --debug | grep -A 50 "kind: Deployment" | grep -A 50 "new-service"

# 5. Dry-run against cluster
helm install nudgebee . --dry-run --debug
```

### Workflow Testing

```bash
# Test packaging step locally
cd deploy/kubernetes/nudgebee
rm -rf ../new-service/values-*.yaml
helm package ../new-service -d ./charts/
ls -la charts/ | grep new-service
```

---

## Troubleshooting

### Issue: Chart not found in dependency build

**Symptom:**
```
Error: chart not found in repo file://../new-service/
```

**Solution:**
- Ensure Chart.yaml exists in `/deploy/kubernetes/new-service/`
- Verify `repository: file://../new-service/` path is correct (relative to nudgebee/)
- Check Chart.yaml has valid `name:` field

### Issue: Image tag not updated in workflow

**Symptom:** Deployment uses old image version

**Solution:**
- Verify ECR repository name matches: `nudgebee-{service}`
- Check image fetching query in workflow file
- Ensure `yq -i` updates correct values.yaml key

### Issue: Helm dependency build fails

**Symptom:**
```
Error: can't get a valid version for repositories
```

**Solution:**
- For external charts: Verify version exists in remote repository
- For local charts: Check `version:` in Chart.yaml matches dependency declaration
- Run `helm repo update` for external repos

### Issue: Values not applied in deployment

**Symptom:** Default values used instead of custom values

**Solution:**
- Check values key matches chart name: `new-service:` not `new_service:`
- Verify `fullnameOverride` is set consistently
- Use `helm template --debug` to see actual values

---

## Quick Reference Commands

```bash
# Add dependency
helm dependency build

# List dependencies
helm dependency list

# Update dependencies
helm dependency update

# Package chart
helm package {chart-name}

# Push to OCI registry
helm push {chart-name}-{version}.tgz oci://{registry}

# Install/Upgrade
helm upgrade {release} {chart} --install

# Test rendering
helm template {release} {chart} --debug

# Lint chart
helm lint {chart}

# Show values
helm show values {chart}
```

---

## Summary Checklist for New Service

### For Internal Services (Custom Code):

- [ ] Create Helm chart directory `/deploy/kubernetes/{service}/`
- [ ] Create Chart.yaml with name, version, description
- [ ] Create values.yaml with image, resources, service config
- [ ] Create environment values (values-dev.yaml, values-test.yaml, values-prod.yaml)
- [ ] Create templates (deployment, service, helpers)
- [ ] Add dependency to `/deploy/kubernetes/nudgebee/Chart.yaml`
- [ ] Add config to `/deploy/kubernetes/nudgebee/values.yaml`
- [ ] Update nudgebee-build-dev.yaml: image fetch + cleanup + package
- [ ] Update nudgebee-build-test.yaml: image fetch + cleanup + package
- [ ] Update nudgebee-build-prod.yaml: image fetch + cleanup + package
- [ ] Test locally with `helm dependency build` and `helm template`

### For External Dependencies:

- [ ] Add dependency to `/deploy/kubernetes/nudgebee/Chart.yaml`
- [ ] Add config to `/deploy/kubernetes/nudgebee/values.yaml`
- [ ] (Optional) Create `/deploy/kubernetes/{service}/` for environment values
- [ ] Run `helm dependency build`
- [ ] No workflow changes needed ✅

### For Hybrid (External Image + Local Chart):

- [ ] Create Helm chart directory `/deploy/kubernetes/{service}/`
- [ ] Create Chart.yaml, values.yaml, templates
- [ ] Use official image in values.yaml (not ECR)
- [ ] Add dependency to `/deploy/kubernetes/nudgebee/Chart.yaml`
- [ ] Add config to `/deploy/kubernetes/nudgebee/values.yaml`
- [ ] Update all 3 workflows: cleanup + package (NO image fetch)
- [ ] Test locally

---

## Additional Resources

- **Helm Documentation:** https://helm.sh/docs/
- **Bitnami Charts:** https://github.com/bitnami/charts
- **AWS ECR with Helm:** https://docs.aws.amazon.com/AmazonECR/latest/userguide/push-oci-artifact.html

---

**Last Updated:** December 2025
