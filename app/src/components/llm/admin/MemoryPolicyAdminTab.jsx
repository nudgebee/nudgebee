import { useMemo } from 'react';
import { BCortexFlagContext, useBCortexEnabled } from '@hooks/useBCortexEnabled';

/**
 * Admin → AI & Tools → Memory Policy — the tenant-policy half of Privacy
 * (docs/ia-consolidation-plan.md; the personal half stays at b-Cortex →
 * Memory → Privacy). Same component as b-Cortex/Settings, mounted with
 * `scope='global'` — PrivacyTab itself resolves readOnly for a non-admin.
 *
 * PrivacyTab is EE-only (app/src/ee/), stripped from the OSS snapshot — same
 * Turbopack dynamic-import constraint as GatewayAdminTab.jsx/
 * EgressFilterAdminTab.jsx. scripts/oss-patches.sh range-replaces the
 * marker-delimited block below (import included, so no import survives
 * unused post-strip) with a () => null stub for the OSS build.
 *
 * PrivacyTab also reads BCortexFlagContext (gates on the MEMORY_MODULE
 * flag) — Admin's tab shell doesn't provide one today, so this wrapper
 * supplies its own, same as BCortexModal/SettingsModal do, rather than
 * relying on the context's enabled-by-default fallback (which would show
 * Memory Policy as available even for a tenant that has MEMORY_MODULE
 * explicitly off).
 */
const PrivacyTab = () => null;

const MemoryPolicyAdminTab = () => {
  const bcortexEnabled = useBCortexEnabled(true);
  const flagContextValue = useMemo(() => ({ enabled: bcortexEnabled, onOpenLegacy: () => {} }), [bcortexEnabled]);

  return (
    <BCortexFlagContext.Provider value={flagContextValue}>
      <PrivacyTab scope='global' readOnly={false} />
    </BCortexFlagContext.Provider>
  );
};

export default MemoryPolicyAdminTab;
