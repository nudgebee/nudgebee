import { useState, useEffect } from 'react';
import apiRecommendations, { RECOMMENDATION_SERVERITY } from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import { snakeToTitleCase } from 'src/utils/common';
import apiResources from '@api1/resources';

const useCloudFilter = (accountId: string) => {
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilterType] = useState(RECOMMENDATION_SERVERITY);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    apiResources.getResourceServices(accountId).then((res: any) => {
      setServiceNamesFilter(res?.data || []);
    });
  }, [accountId]);

  return {
    serviceNamesFilter,
    severityFilterType,
  };
};

const useEventCloudFilter = (accountId: string | string[], data: any = {}) => {
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilterType] = useState(RECOMMENDATION_SERVERITY);
  const [eventNamesFilter, setEventNamesFilter] = useState([]);
  const [subjectNameFilter, setSubjectNamesFilter] = useState([]);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [isOptionsLoading, setIsOptionsLoading] = useState({ serviceName: false, namespace: false, aggregationKey: false, source: false });
  const workloadFilter: any[] = [];
  const [sourceFilter, setSourceFilter] = useState<{ label: string; value: string }[]>([]);
  const statusFilter = [
    { value: 'FIRING', label: 'Firing' },
    { value: 'CLOSED', label: 'Closed' },
    { value: 'RESOLVED', label: 'Resolved' },
  ];
  const nbStatusFilter = [
    { value: 'OPEN', label: 'Open' },
    { value: 'ACTION_REQUIRED', label: 'Action Required' },
    { value: 'SNOOZED', label: 'Snoozed' },
    { value: 'SUPPRESSED', label: 'Suppressed' },
    { value: 'DROPPED', label: 'Dropped' },
    { value: 'DUPLICATE', label: 'Duplicate' },
    { value: 'RESOLVED', label: 'Resolved' },
  ];

  useEffect(() => {
    if (!accountId) {
      return;
    }

    const accountIds = Array.isArray(accountId) ? accountId : [accountId];
    setIsOptionsLoading((prev) => ({ ...prev, serviceName: true }));
    Promise.all(accountIds.map((id) => apiRecommendations.listRecommendationNamesapces({ accountId: id, status: '', category: '', ruleName: '' })))
      .then((results: string[][]) => {
        const namespaces = [...new Set(results.flat())];
        setSubjectNamesFilter(namespaces as any);
        setNamespaceFilter(namespaces as any);
      })
      .finally(() => {
        setIsOptionsLoading((prev) => ({ ...prev, serviceName: false }));
      });

    const filterTypes = ['aggregation_key', 'source'];
    if (!data?.serviceName) {
      filterTypes.push('namespace');
    }

    setIsOptionsLoading((prev) => ({ ...prev, namespace: true, aggregationKey: true, source: true }));
    Promise.all(accountIds.map((id) => k8sApi.getEventFilterValues({ accountId: id, filterTypes })))
      .then((responses: any[]) => {
        const valueMap = new Map<string, Set<string>>();
        responses.forEach((res) => {
          (res?.data?.filters || []).forEach((f: any) => {
            if (!valueMap.has(f.filter_type)) valueMap.set(f.filter_type, new Set());
            (f.values || []).filter((v: any) => v.value).forEach((v: any) => valueMap.get(f.filter_type)!.add(v.value));
          });
        });

        const toArr = (type: string) => [...(valueMap.get(type) || [])];

        const namespaceValues = toArr('namespace');
        if (namespaceValues.length) setServiceNamesFilter(namespaceValues as any);

        const aggValues = toArr('aggregation_key');
        if (aggValues.length) setEventNamesFilter(aggValues.map((v) => ({ label: snakeToTitleCase(v), value: v })) as any);

        const sourceValues = toArr('source');
        if (sourceValues.length) setSourceFilter(sourceValues.map((v) => ({ label: snakeToTitleCase(v), value: v })));
      })
      .finally(() => {
        setIsOptionsLoading((prev) => ({ ...prev, namespace: false, aggregationKey: false, source: false }));
      });
  }, [accountId]);

  return {
    serviceNamesFilter,
    severityFilterType,
    eventNamesFilter,
    namespaceFilter,
    subjectNameFilter,
    workloadFilter,
    sourceFilter,
    statusFilter,
    nbStatusFilter,
    isOptionsLoading,
  };
};

const useMetricCloudFilter = (accountId: string, data: any = {}) => {
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilterType] = useState(RECOMMENDATION_SERVERITY);
  const [eventNamesFilter, setEventNamesFilter] = useState([]);
  const [subjectNameFilter, setSubjectNamesFilter] = useState([]);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [isOptionsLoading, setIsOptionsLoading] = useState({ serviceName: false, namespace: false, aggregationKey: false, source: false });
  const workloadFilter: any[] = [];
  const [sourceFilter, setSourceFilter] = useState<{ label: string; value: string }[]>([]);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    setIsOptionsLoading((prev) => ({ ...prev, serviceName: true }));
    apiRecommendations
      .listRecommendationNamesapces({ accountId: accountId, status: '', category: '', ruleName: '' })
      .then((res: any) => {
        const namespaces = res || [];
        setSubjectNamesFilter(namespaces);
        setNamespaceFilter(namespaces);
      })
      .finally(() => {
        setIsOptionsLoading((prev) => ({ ...prev, serviceName: false }));
      });

    const filterTypes = ['aggregation_key', 'source'];
    if (!data?.serviceName) {
      filterTypes.push('namespace');
    }

    setIsOptionsLoading((prev) => ({ ...prev, namespace: true, aggregationKey: true, source: true }));
    k8sApi
      .getEventFilterValues({ accountId, filterTypes })
      .then((res: any) => {
        const filters = res?.data?.filters || [];

        const namespaceResult = filters.find((f: any) => f.filter_type === 'namespace');
        if (namespaceResult) {
          setServiceNamesFilter(namespaceResult.values?.map((v: any) => v.value).filter(Boolean) || []);
        }

        const aggregationResult = filters.find((f: any) => f.filter_type === 'aggregation_key');
        if (aggregationResult) {
          setEventNamesFilter(
            aggregationResult.values?.filter((v: any) => v.value).map((v: any) => ({ label: snakeToTitleCase(v.value), value: v.value })) || []
          );
        }

        const sourceResult = filters.find((f: any) => f.filter_type === 'source');
        if (sourceResult) {
          setSourceFilter(
            sourceResult.values?.filter((v: any) => v.value).map((v: any) => ({ label: snakeToTitleCase(v.value), value: v.value })) || []
          );
        }
      })
      .finally(() => {
        setIsOptionsLoading((prev) => ({ ...prev, namespace: false, aggregationKey: false, source: false }));
      });
  }, [accountId]);

  return {
    serviceNamesFilter,
    severityFilterType,
    eventNamesFilter,
    namespaceFilter,
    subjectNameFilter,
    workloadFilter,
    sourceFilter,
    isOptionsLoading,
  };
};

const useRecommendationCloudFilter = (accountId: string, data: any = {}) => {
  const [ruleNamesFilter, setRuleNamesFilter] = useState([]);
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilter] = useState(RECOMMENDATION_SERVERITY);
  const [isOptionsLoading, setIsOptionsLoading] = useState({ ruleName: false, serviceName: false });

  useEffect(() => {
    if (!accountId) {
      return;
    }

    setIsOptionsLoading((prev) => ({ ...prev, ruleName: true }));
    apiRecommendations
      .listRecommendationFilter(accountId, ['rule_name'], data)
      .then((res: any) => {
        setRuleNamesFilter(
          res?.data?.data?.recommendation
            ?.filter((g: any) => g.rule_name)
            .map((e: any) => {
              const details = apiRecommendations.getRecommendationDetails(data?.category, e.rule_name);
              return { label: details?.title || snakeToTitleCase(e.rule_name), value: e.rule_name };
            }) || []
        );
      })
      .finally(() => {
        setIsOptionsLoading((prev) => ({ ...prev, ruleName: false }));
      });

    if (!data?.serviceName) {
      setIsOptionsLoading((prev) => ({ ...prev, serviceName: true }));
      apiRecommendations
        .listRecommendationFilter(accountId, ['resource_cloud_service'], data)
        .then((res: any) => {
          setServiceNamesFilter(
            res?.data?.data?.recommendation
              ?.filter((g: any) => g.resource_cloud_service)
              .map((e: any) => ({ label: e.resource_cloud_service, value: e.resource_cloud_service })) || []
          );
        })
        .finally(() => {
          setIsOptionsLoading((prev) => ({ ...prev, serviceName: false }));
        });
    }
  }, [accountId]);

  return {
    ruleNamesFilter,
    serviceNamesFilter,
    severityFilter,
    isOptionsLoading,
  };
};

export { useCloudFilter, useRecommendationCloudFilter, useEventCloudFilter, useMetricCloudFilter };

export default useCloudFilter;
