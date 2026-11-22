#!/usr/bin/env bash
# Tools the Nudgebee code agent needs to work on this repository.
#
# The agent's workspace runs the copy of this file on the default branch, once,
# and again whenever it changes, then puts $NB_TOOLS_BIN at the end of PATH for
# the commands it runs here. Only user-space installs work: the workspace has no
# root and a read-only system image. See llm/code-analysis/CLAUDE.md,
# "Repository setup script".
set -euo pipefail

# Atlas: api-server/migrations/new-migration.sh needs it to refresh atlas.sum.
# Keep the version and checksums in step with api-server/migrations/Dockerfile.
ATLAS_VERSION=v1.3.0
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)
    ATLAS_ARCH=amd64
    ATLAS_SHA256=10d7913e3dce43ab99b8d71534a4cbadaf11a16dc293adf3b91d10e83a0ac70b
    ;;
  Linux-aarch64 | Linux-arm64)
    ATLAS_ARCH=arm64
    ATLAS_SHA256=082188c57a53439596a3ee52173b12e785aa83ec718505674e2052db0fec0c4c
    ;;
  *)
    echo "atlas: no pinned build for $(uname -s)-$(uname -m), skipping"
    exit 0
    ;;
esac

# Bounded well inside the five minutes setup is given.
curl -fsSL --connect-timeout 20 --max-time 120 --retry 2 --retry-delay 5 --retry-max-time 240 \
  "https://release.ariga.io/atlas/atlas-community-linux-${ATLAS_ARCH}-${ATLAS_VERSION}" \
  -o "${NB_TOOLS_BIN}/atlas"
echo "${ATLAS_SHA256}  ${NB_TOOLS_BIN}/atlas" | sha256sum -c -
chmod +x "${NB_TOOLS_BIN}/atlas"
"${NB_TOOLS_BIN}/atlas" version
