import * as React from 'react';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import homeApi from '@api1/home';
import userApi, { PREFERENCE_LAST_ACCOUNT_ID } from '@api1/user';
import apiAccount from '@api1/account';
import { useData } from '@context/DataContext';
import { transformClusters } from './UpdateDataContext';
import CustomDropdown from './CustomDropdown';

/**
 * Custom Hook: Handles fetching and Context synchronization.
 */
const useClusterData = (allCluster, setAllCluster) => {
  const [isLoading, setIsLoading] = React.useState(false);

  const fetchClusters = React.useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await homeApi.getCloudAccounts('', false, true); // Pass `includeDemoAccount=true` to ensure demo accounts are included in the dropdown
      const transformed = transformClusters(response);
      setAllCluster(transformed);
      return transformed;
    } catch (error) {
      console.error('Failed to fetch clusters', error);
      return [];
    } finally {
      setIsLoading(false);
    }
  }, [allCluster, setAllCluster]); // Added setAllCluster for strict linting

  return { isLoading, fetchClusters };
};

const ClusterDropdown = ({ onChange, onClusterDataLoaded, disableRouteChanges = false, noLabel = false, ...dropdownProps }) => {
  const router = useRouter();
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

  const urlAccountId = router.query?.accountId || router.query?.KubernetesDetails || router.query?.CloudAccountDetails; // OPTIMIZATION: Centralize the logic for finding the ID in the URL

  React.useEffect(() => {
    // Effect: Initialization (Fetch Data & Set Initial Selection)
    let isMounted = true;

    const init = async () => {
      let currentClusters = allCluster;
      // Only fetch if we don't have any cluster data yet (including demo accounts)
      if (!allCluster || allCluster.length === 0) {
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
          const userPreferences = userApi.getUserPreferences();
          const preferredId = urlAccountId || userPreferences[PREFERENCE_LAST_ACCOUNT_ID];
          const validCluster = currentClusters?.find((item) => item.value === preferredId);
          const initialId = validCluster ? validCluster.value : currentClusters?.[0]?.value; // Fallback to first cluster if no preference/url match
          if (initialId) {
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
            userApi.storeUserPreferences(PREFERENCE_LAST_ACCOUNT_ID, initialId);
            fetchProviderCapabilities(initialId);
          }
        }
      }
    };

    init();

    return () => {
      isMounted = false;
    };
  }, [selectedCluster, clusterValue, allCluster, disableRouteChanges, router, urlAccountId, fetchProviderCapabilities]);

  React.useEffect(() => {
    // Effect: Sync state when Browser Back/Forward button is used
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
      userApi.storeUserPreferences(PREFERENCE_LAST_ACCOUNT_ID, newValue); // 2. Persist Preference

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
    [allCluster, disableRouteChanges, router, onChange, setSelectedCluster, fetchProviderCapabilities]
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
