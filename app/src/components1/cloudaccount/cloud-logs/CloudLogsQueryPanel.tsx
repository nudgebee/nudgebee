import React, { useEffect, useState, useCallback, useImperativeHandle, forwardRef } from 'react';
import { Box, TextField, Typography } from '@mui/material';
import CustomDropdown from '@components1/common/CustomDropdown';
import apiCloudAccount from '@api1/cloud-account';

export interface CloudLogsQueryParams {
  query: string;
  region: string;
  logGroup: string;
  serviceName: string;
  resourceId: string;
}

interface CloudLogsQueryPanelProps {
  provider: 'AWS' | 'Azure' | 'GCP';
  accountId: string;
  onChange: (params: CloudLogsQueryParams) => void;
  initialRegion?: string;
}

const AWS_DEFAULT_QUERY = 'fields @timestamp, @message | sort @timestamp desc';
const AZURE_DEFAULT_QUERY = 'AzureDiagnostics | project TimeGenerated, Message, Category | order by TimeGenerated desc';
const GCP_DEFAULT_QUERY = '';

export interface CloudLogsQueryPanelHandle {
  setQuery: (query: string) => void;
}

const GCP_SEVERITIES = ['DEFAULT', 'DEBUG', 'INFO', 'NOTICE', 'WARNING', 'ERROR', 'CRITICAL', 'ALERT', 'EMERGENCY'];

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

const CloudLogsQueryPanel = forwardRef<CloudLogsQueryPanelHandle, CloudLogsQueryPanelProps>(
  ({ provider, accountId, onChange, initialRegion }, ref) => {
    const [regions, setRegions] = useState<string[]>([]);
    const [selectedRegion, setSelectedRegion] = useState(initialRegion || '');
    const [logGroups, setLogGroups] = useState<any[]>([]);
    const [selectedLogGroup, setSelectedLogGroup] = useState<any>(null);
    const [logGroupsLoading, setLogGroupsLoading] = useState(false);
    const [query, setQuery] = useState(getDefaultQuery(provider));

    useImperativeHandle(ref, () => ({
      setQuery: (newQuery: string) => setQuery(newQuery),
    }));
    const [gcpSeverity, setGcpSeverity] = useState('');

    // Azure workspaces
    const [azureWorkspaces, setAzureWorkspaces] = useState<any[]>([]);
    const [selectedWorkspace, setSelectedWorkspace] = useState<any>(null);

    // Fetch regions for the account
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
          // For Azure, store workspaces
          if (provider === 'Azure') {
            setAzureWorkspaces(resources);
            if (resources.length === 1) {
              setSelectedWorkspace(resources[0]);
            }
          }
          // For GCP, store logging resources
          if (provider === 'GCP') {
            setLogGroups(resources);
          }
        } catch (err) {
          console.error('Failed to fetch regions for cloud logs', err);
        }
      };
      fetchRegions();
    }, [accountId, provider]);

    // Fetch log groups when region changes (AWS only)
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

    // Emit params on change
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

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, mb: 1.5 }}>
        {/* AWS: Region + Log Group + Query */}
        {provider === 'AWS' && (
          <>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
              <CustomDropdown
                label='Region'
                value={selectedRegion}
                options={regions}
                onChange={(_, val) => setSelectedRegion(val || '')}
                minWidth='160px'
                disableClearable
              />
              <CustomDropdown
                label='Log Group'
                value={selectedLogGroup ? { label: selectedLogGroup.name, value: selectedLogGroup.name } : null}
                options={logGroups.map((lg) => ({ label: lg.name, value: lg.name, ...lg }))}
                onChange={(_, val) => setSelectedLogGroup(val ? logGroups.find((lg) => lg.name === (val?.value || val)) || null : null)}
                minWidth='300px'
                isLoading={logGroupsLoading}
              />
            </Box>
            <TextField
              size='small'
              label='CloudWatch Insights Query'
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              multiline
              minRows={2}
              maxRows={5}
              sx={{ '& .MuiInputBase-input': { fontSize: 12, fontFamily: 'monospace' } }}
            />
          </>
        )}

        {/* Azure: Workspace + KQL Query */}
        {provider === 'Azure' && (
          <>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
              <CustomDropdown
                label='Log Analytics Workspace'
                value={selectedWorkspace ? { label: selectedWorkspace.name, value: selectedWorkspace.name } : null}
                options={azureWorkspaces.map((ws) => ({ label: ws.name, value: ws.name }))}
                onChange={(_, val) => setSelectedWorkspace(val ? azureWorkspaces.find((ws) => ws.name === (val?.value || val)) || null : null)}
                minWidth='300px'
              />
              {selectedWorkspace?.region && <Typography sx={{ fontSize: 12, color: '#888' }}>Region: {selectedWorkspace.region}</Typography>}
            </Box>
            <TextField
              size='small'
              label='KQL Query'
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              multiline
              minRows={2}
              maxRows={5}
              sx={{ '& .MuiInputBase-input': { fontSize: 12, fontFamily: 'monospace' } }}
            />
          </>
        )}

        {/* GCP: Severity + Filter */}
        {provider === 'GCP' && (
          <>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
              <CustomDropdown
                label='Severity'
                value={gcpSeverity || null}
                options={GCP_SEVERITIES}
                onChange={(_, val) => setGcpSeverity(val || '')}
                minWidth='160px'
              />
              {regions.length > 1 && (
                <CustomDropdown
                  label='Region'
                  value={selectedRegion}
                  options={regions}
                  onChange={(_, val) => setSelectedRegion(val || '')}
                  minWidth='160px'
                  disableClearable
                />
              )}
            </Box>
            <TextField
              size='small'
              label='Cloud Logging Filter'
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              multiline
              minRows={2}
              maxRows={5}
              placeholder='e.g. textPayload:"error" AND resource.type="gce_instance"'
              sx={{ '& .MuiInputBase-input': { fontSize: 12, fontFamily: 'monospace' } }}
            />
          </>
        )}
      </Box>
    );
  }
);

export default CloudLogsQueryPanel;
