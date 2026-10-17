import { useEffect, useState } from 'react';
import { hasFeatureAccess } from '@lib/auth';

/**
 * Tri-state tenant feature-flag check (null = still resolving, then the
 * real boolean) — the async hasFeatureAccess() wrapped in a hook for
 * components that gate UI on a tenant flag. Unlike useBCortexEnabled's
 * "treat null as enabled" default, a still-resolving flag here is treated
 * as false by callers (fail-closed), so gated UI never shows until the
 * check resolves true.
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
