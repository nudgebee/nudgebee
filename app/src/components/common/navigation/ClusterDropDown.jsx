import * as React from 'react';
import { useRouter } from 'next/router';
import { useSession } from 'next-auth/react';
import PropTypes from 'prop-types';
import homeApi, { isCloudAccountsLoadFailure } from '@api1/home';
import userApi, { PREFERENCE_LAST_ACCOUNT_ID } from '@api1/user';
import apiAccount from '@api1/account';
import { useData } from '@context/DataContext';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import CustomDropdown from '@shared/CustomDropdown';
import { isAccountGuardActive } from '@lib/accountGuard';

// Waits before asking for the account list again after it failed to load. The list ends on purpose:
// once it runs out the dropdown stops asking, so a backend that stays down is not polled forever.
const RELOAD_DELAYS_MS = [2000, 5000, 10000, 20000];

// Demo is a sample account the frontend adds to every list, not one the tenant owns.
const isDemo = (accountId) => accountId === 'demo';

/**
 * Custom Hook: Handles fetching and Context synchronization.
 */
const useClusterData = (allCluster, setAllCluster) => {
  const [isLoading, setIsLoading] = React.useState(false);
  // Reloads tried since the list last held a real account; at RELOAD_DELAYS_MS.length it stops.
  const [reloads, setReloads] = React.useState(0);

  // A list with no real account is either a tenant that has none, or the Demo-only stand-in
  // getCloudAccounts returns when the request failed. The stand-in would otherwise stay until the
  // page is reloaded, so ask again. Keyed on the list, not on who fetched it, so a stand-in written
  // by another caller or before this dropdown mounted recovers too.
  React.useEffect(() => {
    if (allCluster == null || allCluster.some((cluster) => cluster?.value !== 'demo')) {
      setReloads(0); // a later failure gets the full set of tries again
      return undefined;
    }
    const delay = RELOAD_DELAYS_MS[reloads];
    if (delay === undefined) {
      return undefined;
    }
    const timer = setTimeout(async () => {
      const response = await homeApi.getCloudAccounts('', false, true);
      if (isCloudAccountsLoadFailure(response)) {
        setReloads((count) => count + 1); // still failing: wait longer, then give up
      } else if (response.some((account) => account?.id !== 'demo')) {
        setAllCluster(transformClusters(response));
      } else {
        setReloads(RELOAD_DELAYS_MS.length); // loaded fine: this tenant has no accounts
      }
    }, delay);
    return () => clearTimeout(timer);
  }, [allCluster, reloads, setAllCluster]);

  const fetchClusters = React.useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await homeApi.getCloudAccounts('', false, true); // Pass `includeDemoAccount=true` to ensure demo accounts are included in the dropdown
      const transformed = transformClusters(response);
      setAllCluster(transformed);
      return transformed;
    } catch (error) {
      console.error('Failed to fetch clusters', error);
      // Move allCluster out of the `null` (never-loaded) state on the FIRST
      // failure so the init effect's `allCluster == null` gate doesn't re-fetch
      // in a loop. Guard on `== null` so a later refresh failure (transient
      // network blip) preserves the already-loaded list instead of blanking the
      // dropdown. setAllCluster (DataContext) takes a value, not a functional
      // updater, so this guard — not `(prev) => ...` — is how we read prior state.
      if (allCluster == null) setAllCluster([]);
      return [];
    } finally {
      setIsLoading(false);
    }
  }, [allCluster, setAllCluster]); // Added setAllCluster for strict linting

  return { isLoading, fetchClusters };
};

const ClusterDropdown = ({ onChange, onClusterDataLoaded, disableRouteChanges = false, noLabel = false, ...dropdownProps }) => {
  const router = useRouter();
  const { data: session } = useSession();
  const tenantId = session?.tenant?.id;
  const { selectedCluster, setSelectedCluster, allCluster, setAllCluster, setProviderCapabilities } = useData();

  const fetchProviderCapabilities = React.useCallback(
    (accountId) => {
      if (!accountId) return;
      apiAccount
        .listProviderCapabilities({ account_id: accountId })
        .then((res) => {
          const entries = res?.data?.data?.observability_list_provider_capabilities ?? [];
          setProviderCapabilities(entries);
        })
        .catch(() => setProviderCapabilities([]));
    },
    [setProviderCapabilities]
  );
  const { isLoading, fetchClusters } = useClusterData(allCluster, setAllCluster);

  const [clusterValue, setClusterValue] = React.useState('');
  const dataLoadedRef = React.useRef(false);
  // True while the selection is a Demo that nobody asked for, so it can be left once a real account loads.
  const autoPickedDemoRef = React.useRef(false);
  // A ?accountId= Demo the selection overrode. The URL still shows it until the route update lands, and
  // the Back/Forward sync below must not read that as the user navigating back to it.
  const overriddenUrlIdRef = React.useRef(null);

  const urlAccountId = router.query?.accountId || router.query?.KubernetesDetails || router.query?.CloudAccountDetails; // OPTIMIZATION: Centralize the logic for finding the ID in the URL

  // Picks the account to open with: the URL's, else the saved one, else the first real account.
  // While the tenant has a real account, Demo is never opened on its own: a saved or ?accountId=
  // Demo is skipped, because that is what the first login writes on an install whose cluster had
  // not registered yet, and honouring it kept the tenant on Demo indefinitely. Demo still opens when
  // the page itself is Demo's (/kubernetes/details/demo) or when someone picks it in the dropdown.
  const selectInitialCluster = React.useCallback(
    (currentClusters, requestedId) => {
      const hasRealAccount = currentClusters?.some((c) => !isDemo(c?.value));
      const pinnedByPath = Boolean(requestedId) && !router.query?.accountId;
      const candidateId = requestedId || userApi.getUserPreferences()[PREFERENCE_LAST_ACCOUNT_ID];
      const preferredId = hasRealAccount && isDemo(candidateId) && !pinnedByPath ? undefined : candidateId;
      const validCluster = currentClusters?.find((item) => item.value === preferredId);
      const fallbackCluster = currentClusters?.find((c) => !isDemo(c?.value)) ?? currentClusters?.[0];
      const initialId = validCluster ? validCluster.value : fallbackCluster?.value;
      if (!initialId) {
        return;
      }
      overriddenUrlIdRef.current = requestedId && requestedId !== initialId ? requestedId : null;
      const newClusterObj = currentClusters?.find((item) => item.value === initialId);
      if (!disableRouteChanges && router.pathname !== '/kubernetes/details/[KubernetesDetails]') {
        // Only push route if ID is different or missing to avoid redundant history entries
        const query = { ...router.query, accountId: initialId };
        if (urlAccountId !== initialId) {
          // Simple check to prevent replacing URL with identical URL
          router.push({ pathname: router.pathname, query }, undefined, { shallow: true });
        }
      }
      setSelectedCluster(newClusterObj);
      setClusterValue(initialId);
      autoPickedDemoRef.current = isDemo(initialId) && !pinnedByPath;
      // Not saved when Demo was only the fallback, so it cannot outlive the empty account list.
      if (!autoPickedDemoRef.current) {
        userApi.storeUserPreferences(PREFERENCE_LAST_ACCOUNT_ID, initialId);
        if (newClusterObj?.cloud_provider) {
          userApi.setLastAccountIdForProvider(newClusterObj.cloud_provider, initialId, tenantId);
        }
      }
      fetchProviderCapabilities(initialId);
    },
    [disableRouteChanges, router, urlAccountId, setSelectedCluster, tenantId, fetchProviderCapabilities]
  );

  React.useEffect(() => {
    // Effect: Initialization (Fetch Data & Set Initial Selection)
    let isMounted = true;

    const init = async () => {
      let currentClusters = allCluster;
      // Fetch only when the accounts list has never been loaded. `null` = not yet
      // fetched; `[]` = fetched and genuinely empty (or a transient upstream
      // failure — getCloudAccounts returns [] without caching). Gating on
      // `length === 0` instead conflates those two states: an empty result reads
      // as "still unloaded", and because fetchClusters() writes a new []
      // reference into allCluster (an effect dependency), the effect re-runs and
      // re-fetches forever, hammering /rpc/query while the upstream is down.
      if (allCluster == null) {
        currentClusters = await fetchClusters();
      }

      if (!isMounted) {
        return;
      }

      if (currentClusters?.length > 0 && !dataLoadedRef.current && onClusterDataLoaded) {
        onClusterDataLoaded(currentClusters);
        dataLoadedRef.current = true;
      }
      if (!clusterValue) {
        // Cross-tenant deep link (ENTERPRISE only): the URL points at an account
        // that isn't one of the user's clusters. Leave the URL untouched so the
        // EE AccountGuard can show the "not in your tenant" banner, instead of
        // silently reverting it to a valid cluster. In OSS (single-tenant) the
        // guard isn't registered, so we keep the original self-heal behavior of
        // reverting a stale/invalid id to a valid cluster.
        if (isAccountGuardActive() && urlAccountId && currentClusters?.length > 0 && !currentClusters.some((c) => c?.value === urlAccountId)) {
          return;
        }
        if (selectedCluster?.value) {
          setClusterValue(selectedCluster.value);
          if (!disableRouteChanges && router.pathname !== '/kubernetes/details/[KubernetesDetails]') {
            const query = { ...router.query, accountId: selectedCluster.value };
            if (urlAccountId !== selectedCluster.value) {
              // Simple check to prevent replacing URL with identical URL
              router.push({ pathname: router.pathname, query }, undefined, { shallow: true });
            }
          }
        } else {
          selectInitialCluster(currentClusters, urlAccountId);
        }
      }
    };

    init();

    return () => {
      isMounted = false;
    };
  }, [selectedCluster, clusterValue, allCluster, disableRouteChanges, router, urlAccountId, selectInitialCluster]);

  React.useEffect(() => {
    // Effect: Leave a Demo that was only picked because no real account had loaded yet — on a
    // fresh install the first login usually beats the bundled cluster's registration.
    if (autoPickedDemoRef.current && isDemo(clusterValue) && allCluster?.some((c) => !isDemo(c?.value))) {
      selectInitialCluster(allCluster, urlAccountId);
    }
  }, [allCluster, clusterValue, urlAccountId, selectInitialCluster]);

  React.useEffect(() => {
    // Effect: Sync state when Browser Back/Forward button is used
    if (urlAccountId === overriddenUrlIdRef.current) {
      return;
    }
    overriddenUrlIdRef.current = null; // the URL has moved on; a later visit back to it is real navigation
    if (urlAccountId !== clusterValue && allCluster?.some((c) => c.value === urlAccountId)) {
      setClusterValue(urlAccountId);
      const restoredCluster = allCluster.find((c) => c.value === urlAccountId); // Optional: Ensure context is synced if user hits back button
      if (restoredCluster) {
        setSelectedCluster(restoredCluster);
      }
    }
  }, [urlAccountId, clusterValue, allCluster]);

  const handleDropdownChange = React.useCallback(
    (e) => {
      // Handler: User Manually Changes Dropdown
      const newValue = e.target.value;
      const newClusterObj = allCluster?.find((item) => item.value === newValue);
      setClusterValue(newValue); // 1. Update Local State
      autoPickedDemoRef.current = false; // a Demo chosen here is a choice, not a fallback
      userApi.storeUserPreferences(PREFERENCE_LAST_ACCOUNT_ID, newValue); // 2. Persist Preference
      if (newClusterObj?.cloud_provider) {
        userApi.setLastAccountIdForProvider(newClusterObj.cloud_provider, newValue, tenantId); // 2b. Persist per-provider preference
      }

      if (!disableRouteChanges && router.pathname !== '/kubernetes/details/[KubernetesDetails]') {
        // 3. Update Router
        const query = { ...router.query, accountId: newValue };
        router.push({ pathname: router.pathname, query }, undefined, { shallow: true });
      }

      if (newClusterObj) {
        // 4. Trigger Parent Callback & Context Note: We check `newClusterObj` existence to be safe
        setSelectedCluster(newClusterObj);
        fetchProviderCapabilities(newValue);
        if (onChange) {
          onChange(newClusterObj);
        }
      }
    },
    [allCluster, disableRouteChanges, router, onChange, setSelectedCluster, fetchProviderCapabilities, tenantId]
  );

  return (
    <CustomDropdown
      id='global-cluster'
      key='all-clusters-dropdown'
      label={noLabel ? '' : 'Change Cluster'}
      value={clusterValue}
      options={allCluster || []}
      onChange={handleDropdownChange}
      isLoading={isLoading}
      showAll={false}
      disableClearable={true}
      clusterData={selectedCluster}
      {...dropdownProps}
    />
  );
};

ClusterDropdown.propTypes = {
  onChange: PropTypes.func,
  onClusterDataLoaded: PropTypes.func,
  disableRouteChanges: PropTypes.bool,
  noLabel: PropTypes.bool,
  // Explicitly defining commonly used props helps with documentation/IDE hinting
  // even if they are passed via ...dropdownProps
  rounded: PropTypes.oneOfType([PropTypes.string, PropTypes.bool]),
  minWidth: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  // ... other propTypes
};

export default React.memo(ClusterDropdown);
