/**
 * Headless hook for the Cloud Logs filter UI. Returns JSX slots the caller
 * places into distant DOM regions of ListingLayout (toolbar filters, body
 * textarea, optional region hint).
 */
import React, { useEffect, useState, useCallback } from 'react';
import { Box } from '@mui/material';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Input as DsInput } from '@components1/ds/Input';
import apiCloudAccount from '@api1/cloud-account';

export interface CloudLogsQueryParams {
  query: string;
  region: string;
  logGroup: string;
  serviceName: string;
  resourceId: string;
}

interface UseCloudLogsQueryPanelProps {
  provider: 'AWS' | 'Azure' | 'GCP';
  accountId: string;
  onChange: (params: CloudLogsQueryParams) => void;
  initialRegion?: string;
}

interface UseCloudLogsQueryPanelResult {
  filters: React.ReactNode;
  textarea: React.ReactNode;
  regionHint: React.ReactNode;
  setQuery: (query: string) => void;
}

const AWS_DEFAULT_QUERY = 'fields @timestamp, @message | sort @timestamp desc';
const AZURE_DEFAULT_QUERY = 'AzureDiagnostics | project TimeGenerated, Message, Category | order by TimeGenerated desc';
const GCP_DEFAULT_QUERY = '';

const GCP_SEVERITY_OPTIONS = ['DEFAULT', 'DEBUG', 'INFO', 'NOTICE', 'WARNING', 'ERROR', 'CRITICAL', 'ALERT', 'EMERGENCY'].map((s) => ({
  label: s,
  value: s,
}));

function getDefaultQuery(provider: string): string {
  switch (provider) {
    case 'Azure':
      return AZURE_DEFAULT_QUERY;
    case 'GCP':
      return GCP_DEFAULT_QUERY;
    default:
      return AWS_DEFAULT_QUERY;
  }
}

export function useCloudLogsQueryPanel({ provider, accountId, onChange, initialRegion }: UseCloudLogsQueryPanelProps): UseCloudLogsQueryPanelResult {
  const [regions, setRegions] = useState<string[]>([]);
  const [selectedRegion, setSelectedRegion] = useState(initialRegion || '');
  const [logGroups, setLogGroups] = useState<any[]>([]);
  const [selectedLogGroup, setSelectedLogGroup] = useState<any>(null);
  const [logGroupsLoading, setLogGroupsLoading] = useState(false);
  const [query, setQuery] = useState(getDefaultQuery(provider));
  const [gcpSeverity, setGcpSeverity] = useState('');

  const [azureWorkspaces, setAzureWorkspaces] = useState<any[]>([]);
  const [selectedWorkspace, setSelectedWorkspace] = useState<any>(null);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    const fetchRegions = async () => {
      try {
        const resp = await apiCloudAccount.getCloudResource({
          account_id: accountId,
          type: provider === 'AWS' ? 'log-group' : provider === 'Azure' ? 'workspaces' : 'cloud-logging',
          status: 'Active',
        });
        const resources = resp?.data?.data?.cloud_resourses || [];
        const uniqueRegions = [...new Set(resources.map((r: any) => r.region).filter(Boolean))] as string[];
        setRegions(uniqueRegions.sort((a, b) => a.localeCompare(b)));
        if (uniqueRegions.length === 1 && !selectedRegion) {
          setSelectedRegion(uniqueRegions[0]);
        }
        if (provider === 'Azure') {
          setAzureWorkspaces(resources);
          if (resources.length === 1) {
            setSelectedWorkspace(resources[0]);
          }
        }
        if (provider === 'GCP') {
          setLogGroups(resources);
        }
      } catch (err) {
        console.error('Failed to fetch regions for cloud logs', err);
      }
    };
    fetchRegions();
  }, [accountId, provider]);

  useEffect(() => {
    if (provider !== 'AWS' || !accountId || !selectedRegion) {
      return;
    }
    const fetchLogGroups = async () => {
      setLogGroupsLoading(true);
      try {
        const resp = await apiCloudAccount.getCloudResource({
          account_id: accountId,
          type: 'log-group',
          region: selectedRegion,
          status: 'Active',
        });
        const resources = resp?.data?.data?.cloud_resourses || [];
        setLogGroups(resources);
        setSelectedLogGroup(null);
      } catch (err) {
        console.error('Failed to fetch log groups', err);
      } finally {
        setLogGroupsLoading(false);
      }
    };
    fetchLogGroups();
  }, [provider, accountId, selectedRegion]);

  const emitChange = useCallback(() => {
    const params: CloudLogsQueryParams = {
      query,
      region: selectedRegion,
      logGroup: '',
      serviceName: '',
      resourceId: '',
    };
    if (provider === 'AWS' && selectedLogGroup) {
      params.logGroup = selectedLogGroup.name || '';
    }
    if (provider === 'Azure' && selectedWorkspace) {
      params.resourceId = selectedWorkspace.resourse_id || '';
    }
    if (provider === 'GCP' && gcpSeverity) {
      params.query = gcpSeverity ? `severity="${gcpSeverity}"${query ? ' AND ' + query : ''}` : query;
    }
    onChange(params);
  }, [provider, query, selectedRegion, selectedLogGroup, selectedWorkspace, gcpSeverity, onChange]);

  useEffect(() => {
    emitChange();
  }, [emitChange]);

  const regionOptions = regions.map((r) => ({ label: r, value: r }));
  const logGroupOptions = logGroups.map((lg) => ({ label: lg.name, value: lg.name }));
  const workspaceOptions = azureWorkspaces.map((ws) => ({ label: ws.name, value: ws.name }));

  let filters: React.ReactNode = null;
  if (provider === 'AWS') {
    filters = (
      <>
        <FilterDropdown
          id='cloud-logs-aws-region'
          label='Region'
          value={regionOptions.find((o) => o.value === selectedRegion) ?? null}
          options={regionOptions}
          onSelect={(_e: any, item: any) => setSelectedRegion(item?.value || '')}
        />
        <FilterDropdown
          id='cloud-logs-aws-log-group'
          label='Log Group'
          value={selectedLogGroup ? { label: selectedLogGroup.name, value: selectedLogGroup.name } : null}
          options={logGroupOptions}
          onSelect={(_e: any, item: any) => setSelectedLogGroup(item ? logGroups.find((lg) => lg.name === item.value) || null : null)}
          isOptionsLoading={logGroupsLoading}
        />
      </>
    );
  } else if (provider === 'Azure') {
    filters = (
      <FilterDropdown
        id='cloud-logs-azure-workspace'
        label='Workspace'
        value={selectedWorkspace ? { label: selectedWorkspace.name, value: selectedWorkspace.name } : null}
        options={workspaceOptions}
        onSelect={(_e: any, item: any) => setSelectedWorkspace(item ? azureWorkspaces.find((ws) => ws.name === item.value) || null : null)}
      />
    );
  } else if (provider === 'GCP') {
    filters = (
      <>
        <FilterDropdown
          id='cloud-logs-gcp-severity'
          label='Severity'
          value={GCP_SEVERITY_OPTIONS.find((o) => o.value === gcpSeverity) ?? null}
          options={GCP_SEVERITY_OPTIONS}
          onSelect={(_e: any, item: any) => setGcpSeverity(item?.value || '')}
        />
        {regions.length > 1 && (
          <FilterDropdown
            id='cloud-logs-gcp-region'
            label='Region'
            value={regionOptions.find((o) => o.value === selectedRegion) ?? null}
            options={regionOptions}
            onSelect={(_e: any, item: any) => setSelectedRegion(item?.value || '')}
          />
        )}
      </>
    );
  }

  const regionHint = provider === 'Azure' && selectedWorkspace?.region ? `Region: ${selectedWorkspace.region}` : null;

  const textarea = (
    <Box sx={{ width: '100%' }}>
      <DsInput
        id={`cloud-logs-${provider.toLowerCase()}-query`}
        size='sm'
        type='textarea'
        rows={3}
        value={query}
        onChange={setQuery}
        placeholder={
          provider === 'AWS'
            ? 'CloudWatch Insights Query'
            : provider === 'Azure'
            ? 'KQL Query'
            : 'e.g. textPayload:"error" AND resource.type="gce_instance"'
        }
      />
    </Box>
  );

  return { filters, textarea, regionHint, setQuery };
}
