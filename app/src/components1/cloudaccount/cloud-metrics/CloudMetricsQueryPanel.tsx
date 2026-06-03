/**
 * Headless hook for the Cloud Metrics filter UI. Returns JSX slots the caller
 * places into the toolbar — `secondaryFilters` is null until a resource is
 * picked (the metrics list depends on the chosen resource).
 */
import React, { useEffect, useState, useCallback } from 'react';
import { Box } from '@mui/material';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Input as DsInput } from '@components1/ds/Input';
import apiCloudAccount from '@api1/cloud-account';
import observability from '@api1/observability';

export interface CloudMetricsQueryParams {
  serviceName: string;
  region: string;
  resourceIds: string[];
  resourceType: string;
  metricNames: string[];
  statistics: string[];
}

interface UseCloudMetricsQueryPanelProps {
  provider: 'AWS' | 'Azure' | 'GCP';
  accountId: string;
  onChange: (params: CloudMetricsQueryParams) => void;
}

interface UseCloudMetricsQueryPanelResult {
  primaryFilters: React.ReactNode;
  secondaryFilters: React.ReactNode;
}

const STATISTIC_OPTIONS = ['Average', 'Sum', 'Maximum', 'Minimum'].map((s) => ({ label: s, value: s }));

export function useCloudMetricsQueryPanel({
  provider: _provider,
  accountId,
  onChange,
}: UseCloudMetricsQueryPanelProps): UseCloudMetricsQueryPanelResult {
  const [services, setServices] = useState<{ label: string; value: string }[]>([]);
  const [selectedServiceName, setSelectedServiceName] = useState('');

  const [regions, setRegions] = useState<string[]>([]);
  const [selectedRegion, setSelectedRegion] = useState('');

  const [resources, setResources] = useState<any[]>([]);
  const [selectedResource, setSelectedResource] = useState<any>(null);
  const [resourcesLoading, setResourcesLoading] = useState(false);

  const [availableMetrics, setAvailableMetrics] = useState<{ name: string; statistics?: string[] }[]>([]);
  const [selectedMetrics, setSelectedMetrics] = useState<string[]>([]);
  const [metricsLoading, setMetricsLoading] = useState(false);

  const [selectedStatistic, setSelectedStatistic] = useState('Average');
  const [customMetricInput, setCustomMetricInput] = useState('');

  useEffect(() => {
    if (!accountId) {
      return;
    }
    const fetchServices = async () => {
      try {
        const resp = await apiCloudAccount.getCloudResource({ account_id: accountId, status: 'Active' }, 1000);
        const allResources = resp?.data?.data?.cloud_resourses || [];

        const serviceSet = new Set<string>();
        for (const r of allResources) {
          if (r.service_name) {
            serviceSet.add(r.service_name);
          }
        }
        const sortedServices = [...serviceSet].sort((a, b) => a.localeCompare(b));
        const serviceList = sortedServices.map((s) => ({ label: s, value: s }));
        setServices(serviceList);

        if (serviceList.length > 0 && !selectedServiceName) {
          setSelectedServiceName(serviceList[0].value);
        }
      } catch (err) {
        console.error('Failed to fetch services for cloud metrics', err);
      }
    };
    fetchServices();
  }, [accountId]);

  useEffect(() => {
    if (!accountId || !selectedServiceName) {
      return;
    }
    setSelectedRegion('');
    setResources([]);
    setSelectedResource(null);
    setAvailableMetrics([]);
    setSelectedMetrics([]);

    const fetchRegions = async () => {
      try {
        const resp = await apiCloudAccount.getCloudResource({
          account_id: accountId,
          serviceName: selectedServiceName,
          status: 'Active',
        });
        const allResources = resp?.data?.data?.cloud_resourses || [];
        const uniqueRegions = [...new Set(allResources.map((r: any) => r.region).filter(Boolean))] as string[];
        uniqueRegions.sort((a, b) => a.localeCompare(b));
        setRegions(uniqueRegions);
        if (uniqueRegions.length === 1) {
          setSelectedRegion(uniqueRegions[0]);
        }
      } catch (err) {
        console.error('Failed to fetch regions for cloud metrics', err);
      }
    };
    fetchRegions();
  }, [accountId, selectedServiceName]);

  useEffect(() => {
    if (!accountId || !selectedRegion || !selectedServiceName) {
      return;
    }
    setSelectedResource(null);

    const fetchResources = async () => {
      setResourcesLoading(true);
      try {
        const resp = await apiCloudAccount.getCloudResource({
          account_id: accountId,
          serviceName: selectedServiceName,
          region: selectedRegion,
          status: 'Active',
        });
        const allResources = resp?.data?.data?.cloud_resourses || [];
        setResources(allResources);
      } catch (err) {
        console.error('Failed to fetch resources for cloud metrics', err);
      } finally {
        setResourcesLoading(false);
      }
    };
    fetchResources();
  }, [accountId, selectedRegion, selectedServiceName]);

  useEffect(() => {
    if (!accountId || !selectedServiceName) {
      setAvailableMetrics([]);
      setSelectedMetrics([]);
      return;
    }

    const fetchMetrics = async () => {
      setMetricsLoading(true);
      try {
        const resp = await observability.metricsList(accountId, {
          metricProvider: 'aws_cloudwatch',
          metricProviderSource: 'user',
          serviceName: selectedServiceName,
        });
        const metrics = resp?.data?.data?.metrics_list_names || [];
        const metricItems = metrics.map((m: any) => ({
          name: m.metric,
          statistics: m.attributes?.statistics || [],
        }));
        setAvailableMetrics(metricItems);
        setSelectedMetrics(metricItems.map((m: any) => m.name));

        if (metricItems.length > 0 && metricItems[0].statistics?.length > 0) {
          setSelectedStatistic(metricItems[0].statistics[0]);
        }
      } catch (err) {
        console.error('Failed to fetch metrics list', err);
        setAvailableMetrics([]);
        setSelectedMetrics([]);
      } finally {
        setMetricsLoading(false);
      }
    };
    fetchMetrics();
  }, [accountId, selectedServiceName]);

  const emitChange = useCallback(() => {
    const customMetrics = customMetricInput
      .split(',')
      .map((m) => m.trim())
      .filter(Boolean);
    const allMetrics = [...new Set([...selectedMetrics, ...customMetrics])];

    const params: CloudMetricsQueryParams = {
      serviceName: selectedServiceName,
      region: selectedRegion,
      resourceIds: selectedResource ? [selectedResource.resourse_id] : [],
      resourceType: selectedResource?.type || '',
      metricNames: allMetrics,
      statistics: [selectedStatistic],
    };
    onChange(params);
  }, [selectedServiceName, selectedRegion, selectedResource, selectedMetrics, selectedStatistic, customMetricInput, onChange]);

  useEffect(() => {
    emitChange();
  }, [emitChange]);

  const regionOptions = regions.map((r) => ({ label: r, value: r }));
  const resourceOptions = resources.map((r: any) => ({
    label: r.name || r.resourse_id,
    value: r.resourse_id,
  }));
  const metricOptions = availableMetrics.map((m) => ({ label: m.name, value: m.name }));
  const selectedMetricOptions = metricOptions.filter((o) => selectedMetrics.includes(o.value));

  const primaryFilters = (
    <>
      <FilterDropdown
        id='cloud-metrics-service'
        label='Service'
        value={services.find((s) => s.value === selectedServiceName) ?? null}
        options={services}
        onSelect={(_e: any, item: any) => setSelectedServiceName(item?.value || '')}
      />
      <FilterDropdown
        id='cloud-metrics-region'
        label='Region'
        value={regionOptions.find((o) => o.value === selectedRegion) ?? null}
        options={regionOptions}
        onSelect={(_e: any, item: any) => setSelectedRegion(item?.value || '')}
      />
      <FilterDropdown
        id='cloud-metrics-resource'
        label='Resource'
        value={selectedResource ? { label: selectedResource.name || selectedResource.resourse_id, value: selectedResource.resourse_id } : null}
        options={resourceOptions}
        onSelect={(_e: any, item: any) => {
          const id = item?.value;
          const r = resources.find((res: any) => res.resourse_id === id);
          setSelectedResource(r || null);
        }}
        isOptionsLoading={resourcesLoading}
      />
    </>
  );

  const secondaryFilters = selectedResource ? (
    <>
      {availableMetrics.length > 0 && (
        <FilterDropdown
          id='cloud-metrics-metrics'
          label='Metrics'
          multiple
          value={selectedMetricOptions}
          options={metricOptions}
          onSelect={(_e: any, item: any) => {
            if (Array.isArray(item)) {
              setSelectedMetrics(item.map((v: any) => (typeof v === 'string' ? v : v.value)));
            }
          }}
          isOptionsLoading={metricsLoading}
          limitTag={1}
        />
      )}
      <FilterDropdown
        id='cloud-metrics-statistic'
        label='Statistic'
        value={STATISTIC_OPTIONS.find((o) => o.value === selectedStatistic) ?? null}
        options={STATISTIC_OPTIONS}
        onSelect={(_e: any, item: any) => setSelectedStatistic(item?.value || 'Average')}
      />
      <Box sx={{ minWidth: 300, maxWidth: 300 }}>
        <DsInput
          id='cloud-metrics-additional'
          size='sm'
          value={customMetricInput}
          onChange={setCustomMetricInput}
          placeholder={availableMetrics.length > 0 ? 'Additional metrics (comma-separated)' : 'Metric names (comma-separated)'}
        />
      </Box>
    </>
  ) : null;

  return { primaryFilters, secondaryFilters };
}
