import React, { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Banner } from '@components1/ds/Banner';
import { EmptyState } from '@components1/ds/EmptyState';
import { Chip as DsChip } from '@components1/ds/Chip';
import { Button as DsButton } from '@components1/ds/Button';
import CustomTable2 from '@common-new/tables/CustomTable2';
import Datetime from '@common-new/format/Datetime';
import DownloadButton from '@common-new/DownloadButton';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import observability from '@api1/observability';
import { ds } from '@utils/colors';
import { useCloudLogsQueryPanel, type CloudLogsQueryParams } from './CloudLogsQueryPanel';
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

const TABLE_ID = 'cloudLogsViewerTable';

const LIMIT_OPTIONS = [
  { label: '50', value: '50' },
  { label: '100', value: '100' },
  { label: '200', value: '200' },
  { label: '500', value: '500' },
  { label: '1000', value: '1000' },
];

const SEVERITY_COLORS: Record<string, string> = {
  error: ds.red[500],
  critical: ds.red[700],
  fatal: ds.red[700],
  warning: ds.amber[500],
  warn: ds.amber[500],
  info: ds.blue[500],
  debug: ds.gray[400],
  notice: ds.green[500],
};

const MAX_DYNAMIC_COLUMNS = 5;
const LONG_VALUE_THRESHOLD = 80;

function getSeverityColor(severity: string): string {
  if (!severity) {
    return ds.gray[400];
  }
  return SEVERITY_COLORS[severity.toLowerCase()] || ds.gray[400];
}

function isUsefulValue(value: any): boolean {
  return value !== undefined && value !== null && value !== '' && value !== '<nil>';
}

const CopyableValue = ({ value }: { value: string }) => {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], minWidth: 0 }}>
      <Typography
        sx={{
          fontSize: ds.text.small,
          fontFamily: 'monospace',
          color: ds.gray[600],
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          maxWidth: 320,
        }}
        title={value}
      >
        {value}
      </Typography>
      <DsButton
        tone='ghost'
        size='xs'
        composition='icon-only'
        icon={<ContentCopyIcon fontSize='small' sx={{ color: copied ? ds.green[600] : undefined }} />}
        aria-label='Copy value'
        tooltip={copied ? 'Copied!' : 'Copy'}
        onClick={handleCopy}
      />
    </Box>
  );
};

const LogExpandedRow = ({ row }: { row: any[] }) => {
  const labels: Record<string, any> = row?.[row.length - 1]?._labels || {};
  const entries = Object.entries(labels).filter(([, v]) => isUsefulValue(v));

  if (entries.length === 0) {
    return (
      <Box p={ds.space[3]}>
        <Typography variant='body2' sx={{ color: ds.gray[500] }}>
          No additional details
        </Typography>
      </Box>
    );
  }

  return (
    <Box
      sx={{
        p: ds.space[3],
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
        gap: ds.space[2],
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
              gap: ds.space[2],
              alignItems: 'baseline',
              py: ds.space[1],
              borderBottom: `1px solid ${ds.gray[200]}`,
            }}
          >
            <Box sx={{ flexShrink: 0 }}>
              <DsChip variant='tag' tone='neutral' size='xs'>
                {key}
              </DsChip>
            </Box>
            {isLong ? (
              <CopyableValue value={strValue} />
            ) : (
              <Typography
                sx={{
                  fontSize: ds.text.small,
                  fontFamily: 'monospace',
                  wordBreak: 'break-all',
                  color: ds.gray[600],
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

  const handleQueryParamsChange = useCallback((params: CloudLogsQueryParams) => {
    queryParamsRef.current = params;
  }, []);

  const {
    filters: queryFilters,
    textarea: queryTextarea,
    regionHint,
    setQuery,
  } = useCloudLogsQueryPanel({ provider, accountId, onChange: handleQueryParamsChange });

  const fetchData = useCallback(async () => {
    const params = queryParamsRef.current;
    if (!params) {
      return;
    }

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
      const logs = response?.data?.data?.logs_list || [];
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

  useEffect(() => {
    if (
      queryParamsRef.current &&
      (provider !== 'AWS' || queryParamsRef.current.logGroup) &&
      (provider !== 'Azure' || queryParamsRef.current.resourceId)
    ) {
      fetchData();
    }
  }, [dateRange]);

  const hasMessages = useMemo(() => data.some((log) => !!log.message), [data]);

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
    return Object.entries(keyCounts)
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([key]) => key)
      .slice(0, MAX_DYNAMIC_COLUMNS);
  }, [data, hasMessages]);

  const hasLabels = useMemo(() => data.some((log) => Object.keys(log.labels || {}).length > 0), [data]);
  const useDynamicColumns = !hasMessages && dynamicLabelKeys.length > 0;

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

  const logTableData = useMemo(() => {
    return data.map((log) => {
      const severity = log.severity || '';
      const timestampCell = {
        // `whiteSpace: 'nowrap'` prevents `table-layout: auto` from shrinking
        // this column to min-content ("7h") on wide tables, which would wrap
        // "7h 12m ago" across multiple lines.
        text: (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], whiteSpace: 'nowrap' }}>
            <Box
              sx={{
                width: 3,
                height: 24,
                borderRadius: ds.radius.sm,
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
                  fontSize: ds.text.small,
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
        // Attach labels to the last cell so LogExpandedRow can read them.
        const lastLabel = labelCells[labelCells.length - 1];
        return [timestampCell, ...labelCells.slice(0, -1), { ...lastLabel, _labels: log.labels || {} }];
      }

      const messageCell = {
        text: (
          <Typography
            component='pre'
            sx={{
              fontSize: ds.text.small,
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
    setQuery(query);
  };

  const hasRows = logTableData.length > 0;

  const emptyDescription =
    provider === 'AWS' && !queryParamsRef.current?.logGroup
      ? 'Select a region and log group, then click "Run Query" to fetch logs.'
      : provider === 'Azure' && !queryParamsRef.current?.resourceId
      ? 'Select a Log Analytics Workspace, then click "Run Query" to fetch logs.'
      : 'No log entries found for the selected time range and query.';

  return (
    <ListingLayout id='cloud-logs-viewer'>
      <ListingLayout.Toolbar
        actions={
          <>
            <DsButton id='cloud-logs-run' tone='primary' size='md' onClick={fetchData} loading={loading} disabled={loading}>
              Run Query
            </DsButton>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={dateRange}
              onChange={(result: any) => {
                const val = result?.selection ?? result;
                if (val) handleDateRangeChange(val);
              }}
            />
            <DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />
          </>
        }
      >
        {queryFilters}
        <FilterDropdown
          id='cloud-logs-limit'
          label='Limit'
          value={LIMIT_OPTIONS.find((o) => o.value === String(logLimit)) ?? null}
          options={LIMIT_OPTIONS}
          onSelect={(_e: any, item: any) => setLogLimit(Number(item?.value) || 100)}
        />
        {regionHint && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{regionHint}</Typography>}
      </ListingLayout.Toolbar>

      <ListingLayout.Body padding={`${ds.space[3]} ${ds.space[5]}`}>
        {queryTextarea}

        <Box sx={{ mt: ds.space[2], mb: ds.space[3] }}>
          <CloudLogsQueryHelp provider={provider} onInsertQuery={handleInsertQuery} />
        </Box>

        {error && (
          <Box sx={{ mb: ds.space[3] }}>
            <Banner tone='critical' surface='section' message={error} />
          </Box>
        )}

        {!error && !loading && !hasRows ? (
          <EmptyState size='inline' illustration='no-results' title='No log entries' description={emptyDescription} />
        ) : (
          <CustomTable2
            id={TABLE_ID}
            headers={tableHeaders}
            tableData={logTableData}
            rowsPerPage={hasRows ? logTableData.length : 5}
            loading={loading}
            showExpandable={hasLabels}
            expandable={{ component: LogExpandedRow }}
          />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default CloudLogsViewer;
