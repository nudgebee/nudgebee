#!/bin/bash
set -euo pipefail

# Only run in remote (web) environments
if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

REPO_ROOT="${CLAUDE_PROJECT_DIR:-/home/user/nudgebee}"

# Required versions
GO_VERSION="1.26.1"
NODE_MAJOR="25"

# ============================================================
# 1. Go 1.26.1 — build from source if not already installed
#    (dl.google.com / go.dev are blocked; GitHub source works)
# ============================================================
CURRENT_GO_VERSION=$(go version 2>/dev/null | grep -oP 'go\K[0-9]+\.[0-9]+\.[0-9]+' || echo "none")
if [ "$CURRENT_GO_VERSION" != "$GO_VERSION" ]; then
  echo "[session-start] Installing Go ${GO_VERSION} from source (current: ${CURRENT_GO_VERSION})..."
  GO_SRC_URL="https://codeload.github.com/golang/go/tar.gz/refs/tags/go${GO_VERSION}"
  GO_SRC_DIR="/tmp/go-go${GO_VERSION}"
  GO_INSTALL_DIR="/usr/local/go"

  # Download source
  curl -sL "$GO_SRC_URL" -o /tmp/go-src.tar.gz
  cd /tmp && tar xzf go-src.tar.gz

  # Build using existing Go as bootstrap
  BOOTSTRAP_GO="${GO_INSTALL_DIR}"
  if [ ! -f "${BOOTSTRAP_GO}/bin/go" ]; then
    # Fallback: look for any Go installation
    BOOTSTRAP_GO=$(dirname "$(dirname "$(which go 2>/dev/null || echo /usr/local/go/bin/go)")")
  fi

  cd "${GO_SRC_DIR}/src"
  GOROOT_BOOTSTRAP="${BOOTSTRAP_GO}" bash make.bash >/dev/null 2>&1

  # Install: back up old Go, move new one in
  if [ -d "${GO_INSTALL_DIR}" ] && [ "$CURRENT_GO_VERSION" != "none" ]; then
    mv "${GO_INSTALL_DIR}" "${GO_INSTALL_DIR}.${CURRENT_GO_VERSION}.bak"
  fi
  mv "${GO_SRC_DIR}" "${GO_INSTALL_DIR}"

  # Clean up
  rm -f /tmp/go-src.tar.gz

  echo "[session-start] Go $(go version) installed."
else
  echo "[session-start] Go ${GO_VERSION} already installed."
fi

# Ensure Go env vars are set for the session
echo "export GOROOT=/usr/local/go" >> "$CLAUDE_ENV_FILE"
echo 'export PATH="/usr/local/go/bin:${HOME}/go/bin:${PATH}"' >> "$CLAUDE_ENV_FILE"

# Rebuild golangci-lint if it was built with an older Go version
LINT_GO_VERSION=$(golangci-lint version 2>&1 | grep -oP 'go\K[0-9]+\.[0-9]+\.[0-9]+' || echo "unknown")
if [ "$LINT_GO_VERSION" != "$GO_VERSION" ]; then
  echo "[session-start] Rebuilding golangci-lint with Go ${GO_VERSION} (current built with go${LINT_GO_VERSION})..."
  GOLANGCI_LINT_VERSION=$(golangci-lint version --short 2>/dev/null || echo "2.5.0")
  go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}" >/dev/null 2>&1
  cp ~/go/bin/golangci-lint /usr/local/bin/golangci-lint 2>/dev/null || true
  echo "[session-start] golangci-lint rebuilt."
else
  echo "[session-start] golangci-lint already built with Go ${GO_VERSION}."
fi

# ============================================================
# 2. Node 25 via NVM (required by app/package.json engines)
# ============================================================
export NVM_DIR="/opt/nvm"
if [ -s "$NVM_DIR/nvm.sh" ]; then
  . "$NVM_DIR/nvm.sh"
  CURRENT_NODE_MAJOR=$(node --version 2>/dev/null | grep -oP 'v\K[0-9]+' || echo "0")
  if [ "$CURRENT_NODE_MAJOR" -lt "$NODE_MAJOR" ]; then
    echo "[session-start] Installing Node ${NODE_MAJOR} via NVM (current: v${CURRENT_NODE_MAJOR})..."
    nvm install "$NODE_MAJOR" >/dev/null 2>&1
    nvm alias default "$NODE_MAJOR" >/dev/null 2>&1
    echo "[session-start] Node $(node --version) installed."
  else
    echo "[session-start] Node ${NODE_MAJOR}+ already installed ($(node --version))."
  fi

  # Persist NVM setup for session
  echo "export NVM_DIR=\"/opt/nvm\"" >> "$CLAUDE_ENV_FILE"
  echo '[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"' >> "$CLAUDE_ENV_FILE"
fi

# ============================================================
# 3. Frontend npm dependencies (app/)
# ============================================================
APP_DIR="${REPO_ROOT}/app"
if [ -f "${APP_DIR}/package.json" ]; then
  if [ ! -d "${APP_DIR}/node_modules" ]; then
    echo "[session-start] Installing frontend npm dependencies..."
    cd "${APP_DIR}" && npm install --legacy-peer-deps >/dev/null 2>&1
    echo "[session-start] Frontend dependencies installed."
  else
    echo "[session-start] Frontend node_modules already present."
  fi
fi

# ============================================================
# 4. Python Poetry dependencies for services with Makefiles
# ============================================================
PYTHON_SERVICES=(
  "ml-k8s-server"
  "llm/rag-server"
  "collector-server/k8s-collector/app"
  "notifications-server"
)

if command -v poetry >/dev/null 2>&1; then
  for svc in "${PYTHON_SERVICES[@]}"; do
    SVC_DIR="${REPO_ROOT}/${svc}"
    if [ -f "${SVC_DIR}/pyproject.toml" ] && [ ! -d "${SVC_DIR}/.venv" ]; then
      echo "[session-start] Installing Python deps for ${svc}..."
      cd "${SVC_DIR}" && poetry install --no-interaction >/dev/null 2>&1 || true
    fi
  done
  echo "[session-start] Python dependencies checked."
fi

# ============================================================
# 5. Go module cache warm-up for llm-server (most common)
# ============================================================
LLM_SERVER="${REPO_ROOT}/llm/llm-server"
if [ -f "${LLM_SERVER}/go.mod" ]; then
  cd "${LLM_SERVER}"
  if ! go env GOMODCACHE >/dev/null 2>&1 || [ -z "$(ls "$(go env GOMODCACHE)" 2>/dev/null)" ]; then
    echo "[session-start] Downloading Go modules for llm-server..."
    go mod download >/dev/null 2>&1 || true
    echo "[session-start] Go modules downloaded."
  fi
fi

echo "[session-start] Environment setup complete."
