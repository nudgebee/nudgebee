import React, { useEffect, useState, useCallback } from 'react';
import { Box } from '@mui/material';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import CustomSearch from '@components1/common/CustomSearch';
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

interface CloudMetricsQueryPanelProps {
  provider: 'AWS' | 'Azure' | 'GCP';
  accountId: string;
  onChange: (params: CloudMetricsQueryParams) => void;
}

const STATISTICS = ['Average', 'Sum', 'Maximum', 'Minimum'];

const CloudMetricsQueryPanel: React.FC<CloudMetricsQueryPanelProps> = ({ provider: _provider, accountId, onChange }) => {
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

  // Fetch all resources for the account to extract distinct services and regions
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

  // Fetch regions when service changes
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

  // Fetch resources when region changes
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

  // Fetch available metrics from ListMetrics API when service is selected
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
        const metrics = resp?.data?.data?.metrics_list || [];
        const metricItems = metrics.map((m: any) => ({
          name: m.metric,
          statistics: m.attributes?.statistics || [],
        }));
        setAvailableMetrics(metricItems);

        // Auto-select all metrics
        setSelectedMetrics(metricItems.map((m: any) => m.name));

        // If first metric has a preferred statistic, use it
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

  // Emit params on change
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

  const resourceOptions = resources.map((r: any) => ({
    label: r.name || r.resourse_id,
    value: r.resourse_id,
  }));

  const metricOptions = availableMetrics.map((m) => ({ label: m.name, value: m.name }));
  const selectedMetricOptions = metricOptions.filter((o) => selectedMetrics.includes(o.value));

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, mb: 1.5 }}>
      <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
        <FilterDropdownButton
          label='Service'
          value={selectedServiceName ? services.find((s: any) => s.value === selectedServiceName) || null : null}
          options={services}
          onSelect={(_: any, val: any) => {
            setSelectedServiceName(val?.value || val || '');
          }}
        />
        <FilterDropdownButton
          label='Region'
          value={selectedRegion || null}
          options={regions}
          onSelect={(_: any, val: any) => setSelectedRegion(val || '')}
        />
        <FilterDropdownButton
          label='Resource'
          value={selectedResource ? { label: selectedResource.name || selectedResource.resourse_id, value: selectedResource.resourse_id } : null}
          options={resourceOptions}
          onSelect={(_: any, val: any) => {
            const id = val?.value || val;
            const r = resources.find((res: any) => res.resourse_id === id);
            setSelectedResource(r || null);
          }}
          isOptionsLoading={resourcesLoading}
        />
      </Box>
      {selectedResource && (
        <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
          {availableMetrics.length > 0 && (
            <FilterDropdownButton
              label='Metrics'
              multiple
              value={selectedMetricOptions}
              options={metricOptions}
              onSelect={(_: any, val: any) => {
                if (Array.isArray(val)) {
                  setSelectedMetrics(val.map((v: any) => (typeof v === 'string' ? v : v.value)));
                }
              }}
              isOptionsLoading={metricsLoading}
              limitTag={1}
            />
          )}
          <FilterDropdownButton
            label='Statistic'
            value={selectedStatistic}
            options={STATISTICS}
            onSelect={(_: any, val: any) => setSelectedStatistic(val || 'Average')}
          />
          <CustomSearch
            label={availableMetrics.length > 0 ? 'Additional Metrics' : 'Metric Names'}
            value={customMetricInput}
            onChange={(val: string) => setCustomMetricInput(val)}
            onClear={() => setCustomMetricInput('')}
            minWidth='300px'
            maxWidth='300px'
          />
        </Box>
      )}
    </Box>
  );
};

export default CloudMetricsQueryPanel;
