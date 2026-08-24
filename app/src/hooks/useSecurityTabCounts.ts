import { useEffect, useState } from 'react';
import apiHome from '@api1/home';
import recommendationApi from '@api1/recommendation';

export interface SecurityTabCounts {
  imageScan: number;
  cisScan: number;
  vmVulnerabilities: number;
  cloudPosture: number;
}

/**
 * Open-finding counts for the four Security sub-tabs.
 *
 * Lives here rather than in SecurityView because the strip that shows the counts
 * is built in the Optimise page (`filterOptions[].tabOptions`), one level above
 * the view. Fetching them in the view would mean a count only appeared once you
 * had already opened that sub-tab — which is when you no longer need it.
 *
 * `enabled` is not an optimisation detail, it is the whole contract: a tab's
 * sub-tab strip only renders while that tab is active, so these badges are
 * unobservable from Summary, Cost, Configuration or Resolutions. Fetching them
 * on every Optimise page load would buy nothing and cost a request on the four
 * tabs that never display them.
 *
 * `getCloudAccounts` is cached for an hour, so calling it here as well as in
 * SecurityView costs one request per hour, not one per mount.
 */
export const useSecurityTabCounts = (enabled: boolean): SecurityTabCounts | null => {
  const [counts, setCounts] = useState<SecurityTabCounts | null>(null);

  useEffect(() => {
    if (!enabled) return undefined;
    let cancelled = false;

    const load = async () => {
      try {
        const res: any = await apiHome.getCloudAccounts();
        const accountIds = (Array.isArray(res) ? res : []).map((a: any) => a.id).filter(Boolean);
        const next = await recommendationApi.getSecurityTabCounts({ accountIds });
        if (!cancelled) setCounts(next);
      } catch (error) {
        // The strip must still render; it simply shows no badges.
        console.error('Failed to load security tab counts:', error);
        if (!cancelled) setCounts(null);
      }
    };

    load();
    return () => {
      cancelled = true;
    };
  }, [enabled]);

  return counts;
};

export default useSecurityTabCounts;
