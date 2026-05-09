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

const useEventCloudFilter = (accountId: string, data: any = {}) => {
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilterType] = useState(RECOMMENDATION_SERVERITY);
  const [eventNamesFilter, setEventNamesFilter] = useState([]);
  const [subjectNameFilter, setSubjectNamesFilter] = useState([]);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const workloadFilter: any[] = [];
  const [sourceFilter, setSourceFilter] = useState<{ label: string; value: string }[]>([]);
  const statusFilter = [
    { value: 'FIRING', label: 'Firing' },
    { value: 'CLOSED', label: 'Closed' },
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

    // Single API call - set both filters from same response (fixed duplicate call)
    apiRecommendations.listRecommendationNamesapces({ accountId: accountId, status: '', category: '', ruleName: '' }).then((res: any) => {
      const namespaces = res?.data?.namespaces || [];
      setSubjectNamesFilter(namespaces);
      setNamespaceFilter(namespaces);
    });

    // Fetch namespace, aggregation_key, and source filters in a single API call
    const filterTypes = ['aggregation_key', 'source'];
    if (!data?.serviceName) {
      filterTypes.push('namespace');
    }

    k8sApi
      .getEventFilterValues({
        accountId,
        filterTypes,
      })
      .then((res: any) => {
        const filters = res?.data?.filters || [];

        // Set namespace filter (serviceName)
        const namespaceResult = filters.find((f: any) => f.filter_type === 'namespace');
        if (namespaceResult) {
          setServiceNamesFilter(namespaceResult.values?.map((v: any) => v.value).filter(Boolean) || []);
        }

        // Set aggregation_key filter (eventNames)
        const aggregationResult = filters.find((f: any) => f.filter_type === 'aggregation_key');
        if (aggregationResult) {
          setEventNamesFilter(
            aggregationResult.values?.filter((v: any) => v.value).map((v: any) => ({ label: snakeToTitleCase(v.value), value: v.value })) || []
          );
        }

        // Set source filter (dynamic per cloud provider)
        const sourceResult = filters.find((f: any) => f.filter_type === 'source');
        if (sourceResult) {
          setSourceFilter(
            sourceResult.values?.filter((v: any) => v.value).map((v: any) => ({ label: snakeToTitleCase(v.value), value: v.value })) || []
          );
        }
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
  };
};

const useMetricCloudFilter = (accountId: string, data: any = {}) => {
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilterType] = useState(RECOMMENDATION_SERVERITY);
  const [eventNamesFilter, setEventNamesFilter] = useState([]);
  const [subjectNameFilter, setSubjectNamesFilter] = useState([]);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const workloadFilter: any[] = [];
  const [sourceFilter, setSourceFilter] = useState<{ label: string; value: string }[]>([]);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    // Single API call - set both filters from same response (fixed duplicate call)
    apiRecommendations.listRecommendationNamesapces({ accountId: accountId, status: '', category: '', ruleName: '' }).then((res: any) => {
      const namespaces = res?.data?.namespaces || [];
      setSubjectNamesFilter(namespaces);
      setNamespaceFilter(namespaces);
    });

    // Fetch namespace, aggregation_key, and source filters in a single API call
    const filterTypes = ['aggregation_key', 'source'];
    if (!data?.serviceName) {
      filterTypes.push('namespace');
    }

    k8sApi
      .getEventFilterValues({
        accountId,
        filterTypes,
      })
      .then((res: any) => {
        const filters = res?.data?.filters || [];

        // Set namespace filter (serviceName)
        const namespaceResult = filters.find((f: any) => f.filter_type === 'namespace');
        if (namespaceResult) {
          setServiceNamesFilter(namespaceResult.values?.map((v: any) => v.value).filter(Boolean) || []);
        }

        // Set aggregation_key filter (eventNames)
        const aggregationResult = filters.find((f: any) => f.filter_type === 'aggregation_key');
        if (aggregationResult) {
          setEventNamesFilter(
            aggregationResult.values?.filter((v: any) => v.value).map((v: any) => ({ label: snakeToTitleCase(v.value), value: v.value })) || []
          );
        }

        // Set source filter (dynamic per cloud provider)
        const sourceResult = filters.find((f: any) => f.filter_type === 'source');
        if (sourceResult) {
          setSourceFilter(
            sourceResult.values?.filter((v: any) => v.value).map((v: any) => ({ label: snakeToTitleCase(v.value), value: v.value })) || []
          );
        }
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
  };
};

const useRecommendationCloudFilter = (accountId: string, data: any = {}) => {
  const [ruleNamesFilter, setRuleNamesFilter] = useState([]);
  const [serviceNamesFilter, setServiceNamesFilter] = useState([]);
  const [severityFilter] = useState(RECOMMENDATION_SERVERITY);
  useEffect(() => {
    if (!accountId) {
      return;
    }
    apiRecommendations.listRecommendationFilter(accountId, ['rule_name'], data).then((res: any) => {
      setRuleNamesFilter(
        res?.data?.data?.recommendation
          ?.filter((g: any) => g.rule_name)
          .map((e: any) => {
            const details = apiRecommendations.getRecommendationDetails(data?.category, e.rule_name);
            return { label: details?.title || snakeToTitleCase(e.rule_name), value: e.rule_name };
          }) || []
      );
    });

    if (!data?.serviceName) {
      apiRecommendations.listRecommendationFilter(accountId, ['resource_cloud_service'], data).then((res: any) => {
        setServiceNamesFilter(
          res?.data?.data?.recommendation
            ?.filter((g: any) => g.resource_cloud_service)
            .map((e: any) => ({ label: e.resource_cloud_service, value: e.resource_cloud_service })) || []
        );
      });
    }
  }, [accountId]);

  return {
    ruleNamesFilter,
    serviceNamesFilter,
    severityFilter,
  };
};

export { useCloudFilter, useRecommendationCloudFilter, useEventCloudFilter, useMetricCloudFilter };

export default useCloudFilter;
