/**
 * useBCortexEnabled — resolve whether the tenant has the new memory layer
 * (backend feature flag `MEMORY_MODULE`) enabled. Tri-state result so
 * consumers can render optimistically while the async flag lookup resolves:
 *
 *   null  → still resolving (render as if enabled — matches the majority
 *           case on prod today and avoids a "disabled" flash before the
 *           result arrives)
 *   true  → tenant has MEMORY_MODULE enabled → render b-Cortex tabs
 *   false → tenant does NOT have MEMORY_MODULE → tabs should render the
 *           BCortexDisabled placeholder + legacy memory link
 *
 * The underlying feature flag stays `MEMORY_MODULE` on the backend (same
 * one that gates the memory-module write path in llm-server). Only the
 * user-facing surface uses "b-Cortex" naming.
 */
import { createContext, useContext, useEffect, useState } from 'react';
import { hasFeatureAccess } from '@lib/auth';

export const BCORTEX_FEATURE_FLAG = 'MEMORY_MODULE';

/**
 * Hook — resolves the flag once. Callers should invoke this at the top of
 * a modal (BCortexModal, SettingsModal) and provide the value via
 * BCortexFlagContext to their memory-backed tab children.
 */
// NUDGEBEE_DEPLOYMENT_MODE=oss is the shared runtime kill-switch used by
// api-server (services/ee/license/deployment_mode.go) and llm-server
// (memory_v2.go init). Same env var name on both sides — the Next.js
// `env:` block in next.config.js bridges the plain (unprefixed) name
// into the client bundle at build time. When set, the hook short-
// circuits to `false` for every tenant — same behaviour as an
// OSS-stripped bundle.
export const isOSSDeploymentMode = () => true;
const OSS_MODE = true;

export function useBCortexEnabled(retryKey) {
  const [enabled, setEnabled] = useState(OSS_MODE ? false : null);

  useEffect(() => {
    // OSS-mode short-circuit: never fire the flag lookup, stay at `false`.
    // Matches an OSS-stripped bundle where useBCortexEnabled resolves to
    // false because the MEMORY_MODULE flag row is stripped from the OSS
    // migration set (V717).
    if (OSS_MODE) return undefined;
    // Skip the network call once we've already resolved the flag —
    // subsequent retryKey changes are only meaningful when the previous
    // attempt failed and left `enabled` at `null`.
    if (enabled !== null) return undefined;
    // Also skip while the caller signals it's not active (e.g. modal
    // closed). Avoids retrying a failed lookup in the background before
    // the user opens the surface again.
    if (!retryKey) return undefined;
    let active = true;
    hasFeatureAccess(BCORTEX_FEATURE_FLAG)
      .then((v) => {
        if (active) {
          setEnabled(Boolean(v));
        }
      })
      .catch(() => {
        // Do not downgrade to `false` on a transient fetch error — that
        // would swap a working b-Cortex to the placeholder on a flaky
        // network. Leave the tri-state at whatever it currently is; the
        // caller renders optimistically while `null`. `retryKey` lets the
        // caller (e.g. modal `open` prop) trigger another attempt.
      });
    return () => {
      active = false;
    };
  }, [enabled, retryKey]);

  return enabled;
}

/**
 * Context — provided by the modals (BCortexModal, SettingsModal); consumed
 * by the memory-backed tabs so they don't need prop drilling.
 *
 * Shape:
 *   { enabled: boolean|null, onOpenLegacy: () => void }
 *
 * `enabled` mirrors the hook's tri-state.
 * `onOpenLegacy` opens the legacy memory modal (owned by the parent modal).
 *
 * When a tab is rendered without a provider (e.g. Jest test), the default
 * value is `null` enabled + a no-op onOpenLegacy — the tab renders as
 * enabled (backward compatible with pre-flag behavior).
 */
export const BCortexFlagContext = createContext({
  enabled: null,
  onOpenLegacy: () => {},
});

/**
 * useBCortexFlag — small consumer hook for tabs. Returns the context value.
 */
export function useBCortexFlag() {
  return useContext(BCortexFlagContext);
}
