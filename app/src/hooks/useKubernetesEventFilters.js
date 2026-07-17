import { useState, useEffect, useMemo, useCallback } from 'react';
import k8sApi from '@api1/kubernetes';
import apiHome from '@api1/home';
import { useData } from '@context/DataContext';
import { NB_STATUS_OPTIONS } from '@api1/triage';
import { snakeToTitleCase, titleCaseForAggregationKey } from 'src/utils/common';

// Helper to transform values into label/value objects
const toLabelValuePairs = (values, labelTransform = (v) => v) => values.map((v) => ({ label: labelTransform(v), value: v }));

// Map raw nb_status values returned by the DB to curated label/value options.
// Intersecting with NB_STATUS_OPTIONS (a) keeps the canonical display labels,
// (b) preserves a stable logical order regardless of DB count ordering, and
// (c) drops deprecated/unknown statuses (e.g. ACKNOWLEDGED, INVESTIGATING) that
// were intentionally removed from the UI but may still exist on old events.
const toNbStatusOptions = (values) => {
  const present = new Set(values);
  return NB_STATUS_OPTIONS.filter((opt) => present.has(opt.value)).map((opt) => ({ label: opt.label, value: opt.value }));
};

/**
 * @param {Object} params
 * @param {string | string[]} [params.selectedAccountId]
 * @param {boolean} [params.isTroubleshootPage]
 * @param {boolean} [params.enableFilters]
 * @param {string[]} [params.disabledFilters]
 * @param {string[]} [params.resource_ids]
 * @param {string | string[] | null} [params.selectedNamespace] - filters workload options to this namespace
 * @param {string} [params.selectedWorkload] - filters namespace options to namespaces containing this workload
 * @param {string} [params.startTime] - ISO string for time filter start
 * @param {string} [params.endTime] - ISO string for time filter end
 */
const useKubernetesEventFilters = ({
  selectedAccountId,
  isTroubleshootPage,
  enableFilters,
  disabledFilters = [],
  resource_ids = [],
  selectedNamespace,
  selectedWorkload,
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
  const [nbStatusFilter, setNbStatusFilter] = useState([]);

  const [isOptionsLoading, setIsOptionsLoading] = useState({
    namespace: false,
    workload: false,
    subjectType: false,
    aggregationKey: false,
    source: false,
    nbStatus: false,
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
      nb_status: (values) => setNbStatusFilter(toNbStatusOptions(values)),
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

  // Helper: merge filter values from multiple account responses
  const mergeFilterResponses = useCallback((responses) => {
    const valueMap = new Map();
    responses.forEach((response) => {
      (response?.data?.filters || []).forEach((f) => {
        if (!valueMap.has(f.filter_type)) valueMap.set(f.filter_type, new Set());
        (f.values || []).filter((v) => v.value).forEach((v) => valueMap.get(f.filter_type).add(v.value));
      });
    });
    return Array.from(valueMap.entries()).map(([filter_type, valuesSet]) => ({
      filter_type,
      values: [...valuesSet].map((v) => ({ value: v })),
    }));
  }, []);

  // Helper: build per-account (or global) filter requests and resolve them
  const fetchFilterTypes = useCallback(
    (filterTypes, extraParams = {}) => {
      const accountIds = Array.isArray(selectedAccountId) ? selectedAccountId : selectedAccountId ? [selectedAccountId] : [];
      const requests = accountIds.length
        ? accountIds.map((id) => k8sApi.getEventFilterValues({ accountId: id, filterTypes, startTime, endTime, ...extraParams }))
        : [k8sApi.getEventFilterValues({ accountId: null, filterTypes, startTime, endTime, ...extraParams })];
      return Promise.all(requests);
    },
    [selectedAccountId, startTime, endTime]
  );

  // --- 4a. Namespace filter — re-fetches when selectedWorkload changes (cross-filter) ---
  useEffect(() => {
    if (!enableFilters || !shouldFetchData || disabledFilters.includes('namespace') || resource_ids.length > 0) {
      return;
    }
    let active = true;
    setIsOptionsLoading((prev) => ({ ...prev, namespace: true }));
    fetchFilterTypes(['namespace'], selectedWorkload ? { subjectName: selectedWorkload } : {})
      .then((responses) => {
        if (active) {
          processFilterResponse(mergeFilterResponses(responses));
        }
      })
      .finally(() => {
        if (active) {
          setIsOptionsLoading((prev) => ({ ...prev, namespace: false }));
        }
      });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    selectedAccountId,
    enableFilters,
    disabledFiltersStr, // serialized dependency to avoid unnecessary re-runs
    shouldFetchData,
    resourceIdsStr, // serialized dependency to avoid unnecessary re-runs
    startTime,
    endTime,
    selectedWorkload,
    fetchFilterTypes,
    mergeFilterResponses,
    processFilterResponse,
  ]);

  // --- 4b. Workload filter — re-fetches when selectedNamespace changes (cross-filter) ---
  useEffect(() => {
    if (!enableFilters || !shouldFetchData || disabledFilters.includes('workload') || resource_ids.length > 0) {
      return;
    }
    let active = true;
    setIsOptionsLoading((prev) => ({ ...prev, workload: true }));
    fetchFilterTypes(['workload'], selectedNamespace ? { subjectNamespace: selectedNamespace } : {})
      .then((responses) => {
        if (active) {
          processFilterResponse(mergeFilterResponses(responses));
        }
      })
      .finally(() => {
        if (active) {
          setIsOptionsLoading((prev) => ({ ...prev, workload: false }));
        }
      });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    selectedAccountId,
    enableFilters,
    disabledFiltersStr, // serialized dependency to avoid unnecessary re-runs
    shouldFetchData,
    resourceIdsStr, // serialized dependency to avoid unnecessary re-runs
    startTime,
    endTime,
    selectedNamespace,
    fetchFilterTypes,
    mergeFilterResponses,
    processFilterResponse,
  ]);

  // --- 4c. All other filters (subjectType, aggregationKey, source, nbStatus) ---
  useEffect(() => {
    if (!enableFilters || !shouldFetchData) {
      return;
    }
    let active = true;

    const filterTypes = [];
    if (!disabledFilters.includes('subjectType') && resource_ids.length === 0) filterTypes.push('subject_type');
    if (!disabledFilters.includes('aggregationKey')) filterTypes.push('aggregation_key');
    if (!disabledFilters.includes('source')) filterTypes.push('source');
    if (!disabledFilters.includes('nbStatus')) filterTypes.push('nb_status');

    if (filterTypes.length === 0) {
      return;
    }

    setIsOptionsLoading((prev) => ({
      ...prev,
      subjectType: filterTypes.includes('subject_type'),
      aggregationKey: filterTypes.includes('aggregation_key'),
      source: filterTypes.includes('source'),
      nbStatus: filterTypes.includes('nb_status'),
    }));

    fetchFilterTypes(filterTypes)
      .then((responses) => {
        if (active) {
          processFilterResponse(mergeFilterResponses(responses));
        }
      })
      .finally(() => {
        if (active) {
          setIsOptionsLoading((prev) => ({
            ...prev,
            subjectType: false,
            aggregationKey: false,
            source: false,
            nbStatus: false,
          }));
        }
      });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    selectedAccountId,
    enableFilters,
    disabledFiltersStr, // serialized dependency to avoid unnecessary re-runs
    shouldFetchData,
    resourceIdsStr, // serialized dependency to avoid unnecessary re-runs
    startTime,
    endTime,
    fetchFilterTypes,
    mergeFilterResponses,
    processFilterResponse,
  ]);

  return {
    accounts,
    accountType,
    namespaceFilter,
    workloadFilter,
    subjectTypeFilter,
    aggregationKeyFilter,
    sourceFilter,
    nbStatusFilter,
    isOptionsLoading,
  };
};

export default useKubernetesEventFilters;
