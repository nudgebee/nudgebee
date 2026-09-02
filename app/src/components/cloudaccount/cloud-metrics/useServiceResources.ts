import { useEffect, useState } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import type { MetricResourceRef } from './useLiveResourceMetrics';
import { SERVICE_TO_RESOURCE_TYPES } from './serviceResourceTypes';

// Fetches the Active resources of a service (id + region + type + name) so the account
// Summary tab can query their metrics live, multi-resource. Pass an empty serviceName
// to skip the fetch (e.g. the single-resource drilldown supplies its own resource).
// `loading` lets callers avoid an empty-state flash before the resources resolve.
export function useServiceResources(accountId: string, serviceName: string): { resources: MetricResourceRef[]; loading: boolean } {
  const [resources, setResources] = useState<MetricResourceRef[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!accountId || !serviceName) {
      setResources([]);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    // Filter to the metric-bearing resource type where a service spans several (e.g.
    // AmazonEC2 also holds EBS volumes / ENIs) so the query only targets instances.
    const type = SERVICE_TO_RESOURCE_TYPES[serviceName];
    apiCloudAccount
      .getCloudResource({ account_id: accountId, serviceName, status: 'Active', ...(type ? { type } : {}) }, 1000)
      .then((resp: any) => {
        if (cancelled) return;
        const rows = resp?.data?.data?.cloud_resourses || [];
        setResources(rows.map((r: any) => ({ resourse_id: r.resourse_id, region: r.region, type: r.type, name: r.name })));
      })
      .catch((err: any) => {
        if (cancelled) return;
        console.error('Failed to fetch service resources for live metrics', err);
        setResources([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [accountId, serviceName]);

  return { resources, loading };
}
