import { useEffect, useState } from 'react';
import { hasFeatureAccess } from '@lib/auth';

/**
 * Tri-state tenant feature-flag check (null = still resolving, then the
 * real boolean) — same async hasFeatureAccess() SettingsModal.jsx's own
 * initializeTabs used to gate its Functions tab on 'LLM_FUNCTION', pulled
 * out into a hook since Admin's AI & Tools page, its Nubi-rail modal, and
 * b-Cortex all now need the same check. Unlike useBCortexEnabled's
 * "treat null as enabled" default, a still-resolving flag here is treated
 * as false by callers (fail-closed) — matches Settings' own behaviour,
 * which never showed the tab at all until the check resolved true.
 */
export function useFeatureAccess(featureName) {
  const [enabled, setEnabled] = useState(null);

  useEffect(() => {
    let active = true;
    setEnabled(null);
    hasFeatureAccess(featureName)
      .then((v) => {
        if (active) {
          setEnabled(Boolean(v));
        }
      })
      .catch(() => {
        if (active) {
          setEnabled(false);
        }
      });
    return () => {
      active = false;
    };
  }, [featureName]);

  return enabled;
}
