import { useState, useEffect, useMemo, useCallback } from 'react';
import k8sApi from '@api1/kubernetes';
import apiHome from '@api1/home';
import { useData } from '@context/DataContext';
import { snakeToTitleCase, titleCaseForAggregationKey } from 'src/utils/common';

// Helper to transform values into label/value objects
const toLabelValuePairs = (values, labelTransform = (v) => v) => values.map((v) => ({ label: labelTransform(v), value: v }));

/**
 * @param {Object} params
 * @param {string | string[]} [params.selectedAccountId]
 * @param {boolean} [params.isTroubleshootPage]
 * @param {boolean} [params.enableFilters]
 * @param {string[]} [params.disabledFilters]
 * @param {string[]} [params.resource_ids]
 * @param {string | string[] | null} [params.selectedNamespace] <-- UPDATED: Matches incoming type
 * @param {string} [params.startTime] - ISO string for time filter start
 * @param {string} [params.endTime] - ISO string for time filter end
 */
const useKubernetesEventFilters = ({
  selectedAccountId,
  isTroubleshootPage,
  enableFilters,
  disabledFilters = [],
  resource_ids = [],
  _selectedNamespace = [],
  startTime,
  endTime,
}) => {
  const { allCluster } = useData();

  // --- State ---
  const [accounts, setAccounts] = useState([]);
  const [accountType, setAccountType] = useState('K8s');

  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [workloadFilter, setWorkloadFilter] = useState([]);
  const [subjectTypeFilter, setSubjectTypeFilter] = useState([]);
  const [aggregationKeyFilter, setAggregationKeyFilter] = useState([]);
  const [sourceFilter, setSourceFilter] = useState([]);

  const [isOptionsLoading, setIsOptionsLoading] = useState({
    namespace: false,
    workload: false,
    subjectType: false,
    aggregationKey: false,
    source: false,
  });

  // Helper to stabilize array dependencies for useEffect
  const disabledFiltersStr = JSON.stringify(disabledFilters);
  const resourceIdsStr = JSON.stringify(resource_ids);

  // Process filter response to reduce nesting in useEffect
  const processFilterResponse = useCallback((filters) => {
    const filterHandlers = {
      namespace: (values) => setNamespaceFilter(values),
      workload: (values) => setWorkloadFilter(values),
      subject_type: (values) => setSubjectTypeFilter(toLabelValuePairs(values)),
      aggregation_key: (values) => setAggregationKeyFilter(toLabelValuePairs(values, titleCaseForAggregationKey)),
      source: (values) => setSourceFilter(toLabelValuePairs(values, snakeToTitleCase)),
    };

    for (const filter of filters) {
      const values = (filter.values || []).map((v) => v.value).filter(Boolean);
      const handler = filterHandlers[filter.filter_type];
      if (handler) {
        handler(values);
      }
    }
  }, []);

  // --- 1. Account Logic ---
  useEffect(() => {
    if (isTroubleshootPage) {
      apiHome.getCloudAccounts().then((res) => {
        setAccounts(res);
      });
    }
  }, [isTroubleshootPage, allCluster]);

  useEffect(() => {
    if (accounts?.length && selectedAccountId) {
      const id = Array.isArray(selectedAccountId) ? selectedAccountId[0] : selectedAccountId;
      if (!id) return;
      const account = accounts.find((acc) => (acc.id || acc.value) === id);
      if (account) {
        setAccountType(account.cloud_provider);
      }
    }
  }, [accounts, selectedAccountId]);

  const shouldFetchData = useMemo(() => {
    const hasSelectedAccount = Array.isArray(selectedAccountId) ? selectedAccountId.length > 0 : Boolean(selectedAccountId);
    return Boolean(isTroubleshootPage || hasSelectedAccount);
  }, [selectedAccountId, isTroubleshootPage]);

  // --- 2. Workload Logic (only when resource_ids are provided) ---
  useEffect(() => {
    // Only fetch from k8s_workloads when resource_ids are provided
    // This is needed to derive namespace and subjectType from workload data
    if (enableFilters && !disabledFilters.includes('workload') && resource_ids.length > 0) {
      const query = {};
      if (selectedAccountId) {
        query.accountId = selectedAccountId;
      }
      query.resource_ids = resource_ids;

      setIsOptionsLoading((prev) => ({ ...prev, workload: true }));

      k8sApi
        .getAllK8sWorkload(query)
        .then((res) => {
          const workloadObjects = res?.data ?? [];
          setWorkloadFilter([...new Set(workloadObjects.map((e) => e.name))]);

          if (workloadObjects.length > 0) {
            const distinctNamespaces = [...new Set(workloadObjects.map((item) => item.namespace))];
            setNamespaceFilter(distinctNamespaces);

            const distinctKinds = [...new Set(workloadObjects.map((item) => item.kind))];
            setSubjectTypeFilter(
              distinctKinds.map((item) => ({
                label: item,
                value: item,
              }))
            );
          }
        })
        .finally(() => {
          setIsOptionsLoading((prev) => ({ ...prev, workload: false }));
        });
    }
    // FIX: distinct dependency on stringified arrays
  }, [selectedAccountId, enableFilters, disabledFiltersStr, resourceIdsStr]);

  // --- 4. Consolidated Filter Values (namespace, workload, subjectType, aggregationKey, source) ---
  useEffect(() => {
    // Skip if resource_ids are provided (handled by workload logic above)

    if (!enableFilters || !shouldFetchData) {
      return;
    }

    // Determine which filters to fetch
    const filterTypes = [];
    if (!disabledFilters.includes('namespace')) {
      filterTypes.push('namespace');
    }
    if (!disabledFilters.includes('workload')) {
      filterTypes.push('workload');
    }
    if (!disabledFilters.includes('subjectType')) {
      filterTypes.push('subject_type');
    }
    if (!disabledFilters.includes('aggregationKey')) {
      filterTypes.push('aggregation_key');
    }
    if (!disabledFilters.includes('source')) {
      filterTypes.push('source');
    }

    if (filterTypes.length === 0) {
      return;
    }

    // Set loading state for requested filters
    setIsOptionsLoading((prev) => ({
      ...prev,
      namespace: filterTypes.includes('namespace'),
      workload: filterTypes.includes('workload'),
      subjectType: filterTypes.includes('subject_type'),
      aggregationKey: filterTypes.includes('aggregation_key'),
      source: filterTypes.includes('source'),
    }));

    const accountIds = Array.isArray(selectedAccountId) ? selectedAccountId : selectedAccountId ? [selectedAccountId] : [];
    const filterRequests = accountIds.length
      ? accountIds.map((id) => k8sApi.getEventFilterValues({ accountId: id, filterTypes, startTime, endTime }))
      : [k8sApi.getEventFilterValues({ accountId: null, filterTypes, startTime, endTime })];

    Promise.all(filterRequests)
      .then((responses) => {
        const valueMap = new Map();
        responses.forEach((response) => {
          (response?.data?.filters || []).forEach((f) => {
            if (!valueMap.has(f.filter_type)) valueMap.set(f.filter_type, new Set());
            (f.values || []).filter((v) => v.value).forEach((v) => valueMap.get(f.filter_type).add(v.value));
          });
        });
        const mergedFilters = Array.from(valueMap.entries()).map(([filter_type, valuesSet]) => ({
          filter_type,
          values: [...valuesSet].map((v) => ({ value: v })),
        }));
        processFilterResponse(mergedFilters);
      })
      .finally(() => {
        setIsOptionsLoading((prev) => ({
          ...prev,
          namespace: false,
          workload: false,
          subjectType: false,
          aggregationKey: false,
          source: false,
        }));
      });
  }, [selectedAccountId, enableFilters, disabledFiltersStr, shouldFetchData, resourceIdsStr, startTime, endTime, processFilterResponse]);

  return {
    accounts,
    accountType,
    namespaceFilter,
    workloadFilter,
    subjectTypeFilter,
    aggregationKeyFilter,
    sourceFilter,
    isOptionsLoading,
  };
};

export default useKubernetesEventFilters;
