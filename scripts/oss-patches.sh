#!/usr/bin/env bash
#
# scripts/oss-patches.sh — the single canonical implementation of the
# companion patches documented in the .oss-exclude footer. Both
# `scripts/build-oss-snapshot.sh` (in-repo) and the OSS_DROP.md runbook
# (in the public OSS repo) source this script so the patch logic doesn't
# drift across consumers.
#
# Usage:
#   source scripts/oss-patches.sh
#   apply_oss_patches   # mutates files in $PWD, asserts patches matched
#
# Exits non-zero on a silent strip (regex drift), printing which patch
# failed to find its target — turns a hidden corruption into a loud fail
# at drop time.

# Detects BSD sed (macOS) and emits the empty-suffix arg required for
# -i; on GNU sed, returns nothing. Lets the same script run under both.
_sed_inplace() {
  if sed --version >/dev/null 2>&1; then
    sed -i "$@"            # GNU
  else
    sed -i '' "$@"         # BSD (macOS)
  fi
}

apply_oss_patches() {
  local failed=0

  # .oss-exclude removes EE-only migrations. atlas.sum hashes the complete
  # migration directory, so it must be regenerated after those files are
  # stripped or every OSS migration job will stop on a checksum mismatch.
  if [ -d api-server/migrations/migrations/app ]; then
    if ! command -v atlas >/dev/null 2>&1; then
      echo "❌ oss-patches: atlas is required to re-hash the stripped OSS migration directory" >&2
      failed=1
    elif ! atlas migrate hash --dir 'file://api-server/migrations/migrations/app?format=golang-migrate'; then
      echo "❌ oss-patches: failed to re-hash the stripped OSS migration directory" >&2
      failed=1
    fi
  fi

  if [ -f api-server/services/cmd/main.go ]; then
    _sed_inplace '\,_ "nudgebee/services/ee/[^"]*",d' \
        api-server/services/cmd/main.go
    if grep -q '"nudgebee/services/ee/' api-server/services/cmd/main.go; then
      echo "❌ oss-patches: EE blank-import survived in cmd/main.go (sed regex drifted from source)" >&2
      failed=1
    fi
  fi

  if [ -f llm/llm-server/cmd/main.go ]; then
    _sed_inplace '\,_ "nudgebee/llm/ee/[^"]*",d' \
        llm/llm-server/cmd/main.go
    if grep -q '"nudgebee/llm/ee/' llm/llm-server/cmd/main.go; then
      echo "❌ oss-patches: llm-server EE blank-import survived in cmd/main.go" >&2
      failed=1
    fi
  fi

  if [ -f app/src/pages/_app.tsx ]; then
    _sed_inplace "\,^import ['\"]@ee/init['\"];,d" \
        app/src/pages/_app.tsx
    if grep -q '@ee/' app/src/pages/_app.tsx; then
      echo "❌ oss-patches: @ee/ import survived in _app.tsx (sed regex drifted from source)" >&2
      failed=1
    fi
  fi

  if [ -f app/src/instrumentation.ts ]; then
    # The EE init in instrumentation.ts is wrapped in a marker-delimited
    # try/catch (not a single import line) because it must live INSIDE the
    # `NEXT_RUNTIME === 'nodejs'` conditional — a top-level static import
    # would be pulled into Next.js's Edge runtime bundle, which can't
    # resolve Node's `crypto` (transitively imported by HttpService).
    # Range-delete between the markers keeps the surrounding conditional
    # tidy and avoids leaving an empty try/catch behind.
    _sed_inplace '/OSS-STRIP-EE-INIT-BEGIN/,/OSS-STRIP-EE-INIT-END/d' \
        app/src/instrumentation.ts
    if grep -q '@ee/' app/src/instrumentation.ts; then
      echo "❌ oss-patches: @ee/ import survived in instrumentation.ts (sed range markers drifted)" >&2
      failed=1
    fi
  fi

  if [ -f app/tsconfig.json ]; then
    # Strip the @ee path-alias entries — the ./src/ee target was removed
    # via .oss-exclude. tsc passes either way (no surviving import uses
    # them), but the alias would still resolve to a missing directory in
    # editors / turbo builds and leaks the @ee namespace into the public
    # snapshot. Comma-delimited sed regex avoids escaping the @ symbol.
    _sed_inplace '\,"@ee,d' app/tsconfig.json
    if grep -q '@ee' app/tsconfig.json; then
      echo "❌ oss-patches: @ee path alias survived in tsconfig.json (sed regex drifted)" >&2
      failed=1
    fi
  fi

  if [ -f app/jest.config.js ]; then
    # Same as tsconfig — Jest's moduleNameMapper has an @ee/* alias that
    # would point at a deleted directory after strip.
    _sed_inplace '\,@ee/,d' app/jest.config.js
    if grep -q '@ee' app/jest.config.js; then
      echo "❌ oss-patches: @ee moduleNameMapper survived in jest.config.js (sed regex drifted)" >&2
      failed=1
    fi
  fi

  # SettingsModal has three next/dynamic imports pointing at llm/memory2/
  # tabs — Preferences, Soul, Privacy — which is stripped from the OSS
  # tree. Next.js Turbopack's static analysis rejects dynamic() imports
  # of missing modules at build time, so the .catch() runtime fallback
  # can't rescue them. Replace the marker-delimited block with () => null
  # stub components. The tab strip still renders three entries in OSS,
  # but the body is a blank placeholder (matches the intended UX for
  # tenants whose MEMORY_MODULE flag is off, which is every tenant in an
  # OSS install because V717 is excluded).
  if [ -f app/src/components/llm/SettingsModal.jsx ]; then
    _sed_inplace '/OSS-STRIP-BEGIN-MEMORY-DYNAMIC-IMPORTS/,/OSS-STRIP-END-MEMORY-DYNAMIC-IMPORTS/c\
const PreferencesTab = () => null;\
const SoulTab = () => null;\
const PrivacyTab = () => null;' app/src/components/llm/SettingsModal.jsx
    if grep -qE "memory2/PreferencesTab|memory2/SoulTab|memory2/PrivacyTab" app/src/components/llm/SettingsModal.jsx; then
      echo "❌ oss-patches: memory2/ dynamic import survived in SettingsModal.jsx (sed range markers drifted)" >&2
      failed=1
    fi
  fi

  # SettingsModal's gateway-config dynamic import points at @ee/components/
  # gateway-config (enterprise: routing rules + quotas), stripped from the OSS
  # tree. Same Turbopack constraint as the memory2 tabs — replace the
  # marker-delimited block with a () => null stub. The tenant-admin-only Gateway
  # tab still lists in OSS with a blank body; the EE /rpc/config endpoints it
  # would call don't exist in an OSS install.
  if [ -f app/src/components/llm/SettingsModal.jsx ]; then
    _sed_inplace '/OSS-STRIP-BEGIN-GATEWAY-CONFIG-DYNAMIC-IMPORT/,/OSS-STRIP-END-GATEWAY-CONFIG-DYNAMIC-IMPORT/c\
const GatewayConfigTab = () => null;' app/src/components/llm/SettingsModal.jsx
    if grep -q "@ee/components/gateway-config" app/src/components/llm/SettingsModal.jsx; then
      echo "❌ oss-patches: gateway-config dynamic import survived in SettingsModal.jsx (sed range markers drifted)" >&2
      failed=1
    fi
  fi

  # RequestsView's per-request "view body" viewer dynamic-imports
  # @ee/components/gateway-usage/RequestBodyModal (enterprise: captured
  # request/response bodies — PHI-adjacent), stripped from the OSS tree. Same
  # Turbopack constraint as the gateway-config import — replace the
  # marker-delimited block with a () => null stub. The trigger only renders when
  # a row's can_view_body is true, which is always false in an OSS install
  # (body-logging is EE), so the stub is never actually rendered.
  if [ -f app/src/components/llm/gateway-usage/views/RequestsView.tsx ]; then
    _sed_inplace '/OSS-STRIP-BEGIN-GATEWAY-BODYVIEW-IMPORT/,/OSS-STRIP-END-GATEWAY-BODYVIEW-IMPORT/c\
const RequestBodyModal = (_props: Record<string, unknown>) => null;' app/src/components/llm/gateway-usage/views/RequestsView.tsx
    if grep -q "@ee/components/gateway-usage" app/src/components/llm/gateway-usage/views/RequestsView.tsx; then
      echo "❌ oss-patches: gateway-usage body-view dynamic import survived in RequestsView.tsx (sed range markers drifted)" >&2
      failed=1
    fi
  fi

  # NubiBrainNav has a single next/dynamic import for BCortexModal, which
  # is also stripped. Same story as the SettingsModal patch — replace the
  # marker-delimited line with a null stub.
  if [ -f app/src/components/common/layout/NubiBrainNav.jsx ]; then
    _sed_inplace '/OSS-STRIP-BEGIN-BCORTEX-MODAL-IMPORT/,/OSS-STRIP-END-BCORTEX-MODAL-IMPORT/c\
const BCortexModal = () => null;' app/src/components/common/layout/NubiBrainNav.jsx
    if grep -q "@ee/components/BCortexModal" app/src/components/common/layout/NubiBrainNav.jsx; then
      echo "❌ oss-patches: BCortexModal dynamic import survived in NubiBrainNav.jsx (sed range markers drifted)" >&2
      failed=1
    fi
  fi

  # Force OSS_MODE=true in useBCortexEnabled.jsx for physical OSS snapshots.
  # NUDGEBEE_DEPLOYMENT_MODE isn't set at OSS build time, so the runtime
  # env-var check would resolve to `false` and the sidebar b-Cortex button
  # / Settings memory tabs would still list (with null-stubbed bodies).
  # Hard-coding the constant here guarantees the OSS bundle hides them
  # unconditionally, regardless of the build environment.
  if [ -f app/src/hooks/useBCortexEnabled.jsx ]; then
    _sed_inplace "s|^export const isOSSDeploymentMode = () => .*;$|export const isOSSDeploymentMode = () => true;|" \
        app/src/hooks/useBCortexEnabled.jsx
    _sed_inplace "s|^const OSS_MODE = .*;$|const OSS_MODE = true;|" \
        app/src/hooks/useBCortexEnabled.jsx
    if ! grep -q '^export const isOSSDeploymentMode = () => true;$' app/src/hooks/useBCortexEnabled.jsx; then
      echo "❌ oss-patches: isOSSDeploymentMode was not pinned to true in useBCortexEnabled.jsx (sed regex drifted)" >&2
      failed=1
    fi
    if ! grep -q '^const OSS_MODE = true;$' app/src/hooks/useBCortexEnabled.jsx; then
      echo "❌ oss-patches: OSS_MODE was not pinned to true in useBCortexEnabled.jsx (sed regex drifted)" >&2
      failed=1
    fi
  fi

  return $failed
}

# Allow direct invocation: `scripts/oss-patches.sh apply`
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  case "${1:-}" in
    apply)
      apply_oss_patches
      ;;
    *)
      echo "Usage: $0 apply" >&2
      echo "       source $0   # then call apply_oss_patches" >&2
      exit 2
      ;;
  esac
fi
