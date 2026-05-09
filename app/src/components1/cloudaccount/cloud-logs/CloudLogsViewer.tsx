import React, { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { Box, Typography, Alert, CircularProgress, Chip, IconButton, Tooltip } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import CustomDropdown from '@components1/common/CustomDropdown';
import BoxLayout2 from '@common/BoxLayout2';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import CustomButton from '@components1/common/NewCustomButton';
import observability from '@api1/observability';
import CloudLogsQueryPanel, { type CloudLogsQueryParams, type CloudLogsQueryPanelHandle } from './CloudLogsQueryPanel';
import CloudLogsQueryHelp from './CloudLogsQueryHelp';

interface CloudLogsViewerProps {
  accountId: string;
  provider: 'AWS' | 'Azure' | 'GCP';
}

interface LogEntry {
  timestamp: string;
  message: string;
  severity: string;
  labels: Record<string, any>;
}

const SEVERITY_COLORS: Record<string, string> = {
  error: '#f44336',
  critical: '#b71c1c',
  fatal: '#b71c1c',
  warning: '#ff9800',
  warn: '#ff9800',
  info: '#2196f3',
  debug: '#9e9e9e',
  notice: '#4caf50',
};

const MAX_DYNAMIC_COLUMNS = 5;

function getSeverityColor(severity: string): string {
  if (!severity) {
    return '#9e9e9e';
  }
  return SEVERITY_COLORS[severity.toLowerCase()] || '#9e9e9e';
}

function isUsefulValue(value: any): boolean {
  return value !== undefined && value !== null && value !== '' && value !== '<nil>';
}

const LONG_VALUE_THRESHOLD = 80;

const CopyableValue = ({ value }: { value: string }) => {
  const [copied, setCopied] = React.useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, minWidth: 0 }}>
      <Typography
        sx={{
          fontSize: 12,
          fontFamily: 'monospace',
          color: 'text.secondary',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          maxWidth: 320,
        }}
        title={value}
      >
        {value}
      </Typography>
      <Tooltip title={copied ? 'Copied!' : 'Copy'} placement='top'>
        <IconButton size='small' onClick={handleCopy} sx={{ p: 0.25, flexShrink: 0 }}>
          <ContentCopyIcon sx={{ fontSize: 13, color: copied ? 'success.main' : 'action.active' }} />
        </IconButton>
      </Tooltip>
    </Box>
  );
};

/** Expanded row component showing all label key-value pairs */
const LogExpandedRow = ({ row }: { row: any[] }) => {
  const labels: Record<string, any> = row?.[row.length - 1]?._labels || {};
  const entries = Object.entries(labels).filter(([, v]) => isUsefulValue(v));

  if (entries.length === 0) {
    return (
      <Box p={2}>
        <Typography variant='body2' color='text.secondary'>
          No additional details
        </Typography>
      </Box>
    );
  }

  return (
    <Box
      sx={{
        p: 2,
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
        gap: 1,
      }}
    >
      {entries.map(([key, value]) => {
        const strValue = String(value);
        const isLong = strValue.length > LONG_VALUE_THRESHOLD || key === '@ptr';

        return (
          <Box
            key={key}
            sx={{
              display: 'flex',
              gap: 1,
              alignItems: 'baseline',
              py: 0.5,
              borderBottom: '1px solid',
              borderColor: 'divider',
            }}
          >
            <Chip label={key} size='small' sx={{ fontSize: 11, fontWeight: 600, height: 20, flexShrink: 0 }} />
            {isLong ? (
              <CopyableValue value={strValue} />
            ) : (
              <Typography
                sx={{
                  fontSize: 12,
                  fontFamily: 'monospace',
                  wordBreak: 'break-all',
                  color: 'text.secondary',
                }}
              >
                {strValue}
              </Typography>
            )}
          </Box>
        );
      })}
    </Box>
  );
};

const CloudLogsViewer: React.FC<CloudLogsViewerProps> = ({ accountId, provider }) => {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<LogEntry[]>([]);
  const [logLimit, setLogLimit] = useState(100);
  const [dateRange, setDateRange] = useState({
    startTime: Date.now() - 3600000,
    endTime: Date.now(),
    shortcutClickTime: 0,
  });

  const queryParamsRef = useRef<CloudLogsQueryParams | null>(null);
  const queryPanelRef = useRef<CloudLogsQueryPanelHandle>(null);

  const handleQueryParamsChange = useCallback((params: CloudLogsQueryParams) => {
    queryParamsRef.current = params;
  }, []);

  const fetchData = useCallback(async () => {
    const params = queryParamsRef.current;
    if (!params) {
      return;
    }

    // AWS requires a log group; Azure requires a workspace
    if (provider === 'AWS' && !params.logGroup) {
      setError('Please select a log group');
      setData([]);
      return;
    }
    if (provider === 'Azure' && !params.resourceId) {
      setError('Please select a Log Analytics Workspace');
      setData([]);
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const requestPayload: any = {
        account_id: accountId,
        log_provider: 'aws_cloudwatch',
        log_provider_source: 'user',
        query: params.query,
        start_time: dateRange.startTime,
        end_time: dateRange.endTime,
        limit: logLimit,
        request: {
          region: params.region,
        },
      };

      // Add log_group or resource_id depending on provider
      if (provider === 'AWS' && params.logGroup) {
        requestPayload.request.log_group = params.logGroup;
      }
      if (provider === 'Azure' && params.resourceId) {
        requestPayload.request.resource_id = params.resourceId;
        requestPayload.request.service_name = 'azure_sql';
      }
      if (provider === 'GCP') {
        requestPayload.request.service_name = 'cloud sql';
      }

      const response = await observability.fetchLogs(requestPayload);
      const logs = response?.data?.data?.logs_query || [];
      setData(logs);

      if (logs.length === 0) {
        setError(null);
      }
    } catch (err: any) {
      const msg = err?.response?.data?.errors?.[0]?.message || err?.message || 'Failed to fetch logs';
      setError(msg);
      setData([]);
    } finally {
      setLoading(false);
    }
  }, [accountId, provider, dateRange, logLimit]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    if (passedSelectedDateTime.shortcutClickTime > 0) {
      setDateRange({
        startTime: Date.now() - passedSelectedDateTime.shortcutClickTime,
        endTime: Date.now(),
        shortcutClickTime: passedSelectedDateTime.shortcutClickTime,
      });
    } else {
      setDateRange({
        startTime: passedSelectedDateTime.startTime,
        endTime: passedSelectedDateTime.endTime,
        shortcutClickTime: 0,
      });
    }
  };

  // Auto-fetch when date range changes
  useEffect(() => {
    if (
      queryParamsRef.current &&
      (provider !== 'AWS' || queryParamsRef.current.logGroup) &&
      (provider !== 'Azure' || queryParamsRef.current.resourceId)
    ) {
      fetchData();
    }
  }, [dateRange]);

  // Determine if we should use dynamic label columns
  const hasMessages = useMemo(() => data.some((log) => !!log.message), [data]);

  // Extract dynamic column keys from labels (ordered by frequency, filtered)
  const dynamicLabelKeys = useMemo(() => {
    if (hasMessages || data.length === 0) {
      return [];
    }
    const keyCounts: Record<string, number> = {};
    for (const log of data) {
      for (const [key, value] of Object.entries(log.labels || {})) {
        if (isUsefulValue(value)) {
          keyCounts[key] = (keyCounts[key] || 0) + 1;
        }
      }
    }
    // Sort by frequency (most common first), then alphabetically
    return Object.entries(keyCounts)
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([key]) => key)
      .slice(0, MAX_DYNAMIC_COLUMNS);
  }, [data, hasMessages]);

  const hasLabels = useMemo(() => data.some((log) => Object.keys(log.labels || {}).length > 0), [data]);
  const useDynamicColumns = !hasMessages && dynamicLabelKeys.length > 0;

  // Build table headers
  const tableHeaders = useMemo(() => {
    const headers: { name: string; width: string }[] = [{ name: 'Timestamp', width: '160px' }];
    if (useDynamicColumns) {
      for (const key of dynamicLabelKeys) {
        headers.push({ name: key, width: 'auto' });
      }
    } else {
      headers.push({ name: 'Message', width: '90%' });
    }
    return headers;
  }, [useDynamicColumns, dynamicLabelKeys]);

  // Build table data
  const logTableData = useMemo(() => {
    return data.map((log) => {
      const severity = log.severity || '';
      const timestampCell = {
        text: (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Box
              sx={{
                width: 3,
                height: 24,
                borderRadius: 1,
                bgcolor: getSeverityColor(severity),
                flexShrink: 0,
              }}
            />
            <Datetime value={log.timestamp} />
          </Box>
        ),
      };

      if (useDynamicColumns) {
        const labelCells = dynamicLabelKeys.map((key) => {
          const value = log.labels?.[key];
          return {
            text: (
              <Typography
                sx={{
                  fontSize: 12,
                  fontFamily: 'monospace',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  maxWidth: 300,
                }}
                title={isUsefulValue(value) ? String(value) : ''}
              >
                {isUsefulValue(value) ? String(value) : '-'}
              </Typography>
            ),
            data: isUsefulValue(value) ? String(value) : '',
          };
        });
        // Attach labels as hidden metadata on the last cell for the expanded row
        const lastLabel = labelCells[labelCells.length - 1];
        return [timestampCell, ...labelCells.slice(0, -1), { ...lastLabel, _labels: log.labels || {} }];
      }

      const messageCell = {
        text: (
          <Typography
            component='pre'
            sx={{
              fontSize: 12,
              fontFamily: 'monospace',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              m: 0,
              maxHeight: 200,
              overflow: 'auto',
            }}
          >
            {log.message}
          </Typography>
        ),
        _labels: log.labels || {},
      };

      return [timestampCell, messageCell];
    });
  }, [data, useDynamicColumns, dynamicLabelKeys]);

  const handleInsertQuery = (query: string) => {
    queryPanelRef.current?.setQuery(query);
  };

  return (
    <Box>
      <BoxLayout2
        id='cloud-logs-viewer'
        heading='Cloud Logs'
        sharingOptions={{
          sharing: { enabled: false, onClick: null },
          download: { enabled: true, onClick: () => ({ tableId: 'cloudLogsViewerTable' }) },
        }}
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: dateRange,
        }}
        extraOptions={[
          <CustomDropdown
            key='limit'
            label='Limit'
            value={String(logLimit)}
            options={['50', '100', '200', '500', '1000']}
            onChange={(_, val) => setLogLimit(Number(val) || 100)}
            minWidth='80px'
            disableClearable
          />,
          <CustomButton key='run' text='Run Query' size='Small' onClick={fetchData} disabled={loading} />,
        ]}
      >
        <CloudLogsQueryPanel ref={queryPanelRef} provider={provider} accountId={accountId} onChange={handleQueryParamsChange} />

        <CloudLogsQueryHelp provider={provider} onInsertQuery={handleInsertQuery} />

        {error && (
          <Alert severity='error' sx={{ mb: 2, fontSize: 12 }}>
            {error}
          </Alert>
        )}

        {loading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
            <CircularProgress />
          </Box>
        ) : logTableData.length > 0 ? (
          <CustomTable2
            id='cloudLogsViewerTable'
            headers={tableHeaders}
            tableData={logTableData}
            rowsPerPage={logTableData.length}
            showExpandable={hasLabels}
            expandable={{ component: LogExpandedRow }}
          />
        ) : !error ? (
          <Alert severity='info' sx={{ fontSize: 12 }}>
            {provider === 'AWS' && !queryParamsRef.current?.logGroup
              ? 'Select a region and log group, then click "Run Query" to fetch logs.'
              : provider === 'Azure' && !queryParamsRef.current?.resourceId
              ? 'Select a Log Analytics Workspace, then click "Run Query" to fetch logs.'
              : 'No log entries found for the selected time range and query.'}
          </Alert>
        ) : null}
      </BoxLayout2>
    </Box>
  );
};

export default CloudLogsViewer;
