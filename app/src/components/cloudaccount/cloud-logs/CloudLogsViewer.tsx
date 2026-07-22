import React, { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import { KeyboardArrowDown as KeyboardArrowDownIcon } from '@mui/icons-material';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { DropdownMenu } from '@ui/DropdownMenu';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import { Chip as DsChip } from '@ui/Chip';
import { Button as DsButton } from '@ui/Button';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import QueryModeSwitcher from '@components/k8s/common/QueryModeSwitcher';
import { OperatorDescriptor } from '@components/k8s/common/operatorCatalog';
import observability from '@api1/observability';
import apiAccount from '@api1/account';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import { ds } from '@utils/colors';
import { useCloudLogsQueryPanel, type CloudLogsQueryParams } from './CloudLogsQueryPanel';
import CloudLogsQueryHelp from './CloudLogsQueryHelp';

// The cloud-native log path is a synthetic provider (`aws_cloudwatch`) the
// backend dispatches by cloud account type; it is never in available_providers,
// so we synthesize it as the first/default option.
const NATIVE_PROVIDER = 'aws_cloudwatch';
const NATIVE_LABEL: Record<string, string> = { AWS: 'CloudWatch', Azure: 'Azure Monitor', GCP: 'Cloud Logging' };

interface LogProviderOption {
  provider: string;
  label: string;
  iconKey: string;
  operatorDescriptors?: OperatorDescriptor[];
}

// Map builder-chip operators to backend tokens for the SaaS structured-query
// (Builder mode) path — mirrors the ES branch in the K8s Query Logs tab.
const SAAS_OPERATOR_MAP: Record<string, string> = { '=': '_eq', '!=': '_neq', is_one_of: '_in', is_not_one_of: '_not_in' };
const buildStructuredWhere = (items: any[]): any[] =>
  (items || []).map((item) => {
    let op = SAAS_OPERATOR_MAP[item.operator] || item.operator;
    let value: any = item.value;
    if (item.operator === 'exists') {
      op = '_is_null';
      value = false;
    } else if (item.operator === '!exists') {
      op = '_is_null';
      value = true;
    } else if (item.operator === 'is_one_of' || item.operator === 'is_not_one_of') {
      value = String(item.value)
        .split(',')
        .map((v) => v.trim())
        .filter(Boolean);
    }
    return { _binary: { [item.label]: { [op]: value } } };
  });

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
          maxWidth: ds.space.mul(0, 160),
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

  // Log-provider switcher: the native cloud-logs option plus any SaaS log
  // providers (datadog, observe, dynatrace, splunk, solarwinds, ES) attached to
  // this cloud account. The dropdown only appears when there's more than one.
  const [providers, setProviders] = useState<LogProviderOption[]>([]);
  const [selectedProvider, setSelectedProvider] = useState<string>(NATIVE_PROVIDER);
  // SaaS query state — used only when a non-native provider is selected; the
  // native CloudWatch path keeps using useCloudLogsQueryPanel below.
  const [saasQuery, setSaasQuery] = useState('');
  const [saasQueryItems, setSaasQueryItems] = useState<any[]>([]);
  const [saasQLEditor, setSaasQLEditor] = useState('code');
  const [saasEsIndex, setSaasEsIndex] = useState('');
  // The provider-native query the backend actually executed, returned alongside
  // the logs so Builder mode can show what was run.
  const [executedQuery, setExecutedQuery] = useState('');

  const isNative = selectedProvider === NATIVE_PROVIDER;
  const selectedOption = providers.find((p) => p.provider === selectedProvider);

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

  // Native cloud logs is always the first/default option; any SaaS log
  // providers attached to this cloud account come from available_providers
  // (aws_cloudwatch itself is synthetic and never listed there).
  useEffect(() => {
    const nativeOption: LogProviderOption = { provider: NATIVE_PROVIDER, label: NATIVE_LABEL[provider] || 'Cloud Logs', iconKey: provider };
    if (!accountId || accountId === 'demo') {
      setProviders([nativeOption]);
      setSelectedProvider(NATIVE_PROVIDER);
      return undefined;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await apiAccount.getDefaultProvider({ account_id: accountId, provider_type: 'logs' });
        const obs = res?.data?.data?.observability_get_default_provider;
        const available = Array.isArray(obs?.available_providers) ? obs.available_providers : [];
        const saasOptions: LogProviderOption[] = available
          .filter((p: any) => p?.provider && p.provider !== NATIVE_PROVIDER)
          .map((p: any) => ({
            provider: p.provider,
            label: snakeToTitleCase(p.provider),
            iconKey: p.provider,
            operatorDescriptors: p.supported_operator_descriptors,
          }));
        if (!cancelled) {
          setProviders([nativeOption, ...saasOptions]);
          setSelectedProvider(NATIVE_PROVIDER);
        }
      } catch {
        if (!cancelled) {
          setProviders([nativeOption]);
          setSelectedProvider(NATIVE_PROVIDER);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [accountId, provider]);

  // Switching provider is a fresh start: clear results + the SaaS query state.
  // The native CloudWatch path keeps its own query-panel state.
  const handleProviderChange = (next: string) => {
    if (next === selectedProvider) return;
    setSelectedProvider(next);
    setData([]);
    setError(null);
    setSaasQuery('');
    setSaasQueryItems([]);
    setSaasEsIndex('');
  };

  const fetchData = useCallback(async () => {
    let requestPayload: any;

    if (!isNative) {
      // SaaS provider attached to this cloud account — query it directly via
      // log_provider; no region/log-group (those are CloudWatch-specific).
      requestPayload = {
        account_id: accountId,
        log_provider: selectedProvider,
        log_provider_source: 'user',
        query: saasQuery,
        start_time: dateRange.startTime,
        end_time: dateRange.endTime,
        limit: logLimit,
        offset: 0,
      };
      if (selectedProvider === 'ES') {
        if (saasQLEditor === 'build' && saasQueryItems.length > 0) {
          requestPayload.query_request = { where: { _and: buildStructuredWhere(saasQueryItems) } };
          requestPayload.query = '';
        }
        requestPayload.request = { query_type: 'dsl', ...(saasEsIndex ? { index: saasEsIndex } : {}) };
      } else if (saasQLEditor === 'build') {
        // Non-ES Builder mode emits a JSON where-array via onQueryChange.
        const trimmed = typeof saasQuery === 'string' ? saasQuery.trim() : '';
        const parsed = trimmed.startsWith('[') ? safeJSONParse(trimmed) : null;
        if (Array.isArray(parsed) && parsed.length > 0) {
          requestPayload.query_request = { where: { _and: parsed } };
          requestPayload.query = '';
        }
      }
    } else {
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
      requestPayload = {
        account_id: accountId,
        log_provider: NATIVE_PROVIDER,
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
    }

    setLoading(true);
    setError(null);

    try {
      const response = await observability.fetchLogs(requestPayload);
      const logs = response?.data?.data?.logs_list?.logs || [];
      setData(logs);
      setExecutedQuery(response?.data?.data?.logs_list?.query || '');

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
  }, [accountId, provider, dateRange, logLimit, isNative, selectedProvider, saasQuery, saasQLEditor, saasQueryItems, saasEsIndex]);

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
    if (!isNative) {
      if (saasQuery) {
        fetchData();
      }
      return;
    }
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
                height: ds.space[5],
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
                  maxWidth: ds.space.mul(0, 150),
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
              maxHeight: ds.space.mul(0, 100),
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

  const emptyDescription = !isNative
    ? 'Build or write a query, then click "Run Query" to fetch logs.'
    : provider === 'AWS' && !queryParamsRef.current?.logGroup
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
        {providers.length > 1 && (
          <DropdownMenu
            align='start'
            minWidth={200}
            trigger={
              <Box
                component='button'
                type='button'
                id='cloud-logs-provider-switcher'
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: ds.space[2],
                  padding: `${ds.space[1]} ${ds.space[3]}`,
                  backgroundColor: 'var(--ds-gray-alpha-100)',
                  borderRadius: 'var(--ds-radius-md)',
                  border: '1px solid var(--ds-gray-alpha-200)',
                  cursor: 'pointer',
                  font: 'inherit',
                  whiteSpace: 'nowrap',
                  '&:hover': { borderColor: 'var(--ds-gray-300)', backgroundColor: 'var(--ds-gray-alpha-200)' },
                }}
              >
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Log Provider:</Typography>
                <CloudProviderIcon cloud_provider={selectedOption?.iconKey || selectedProvider} width='18px' height='18px' />
                <Typography sx={{ fontSize: ds.text.small, fontWeight: 600, color: ds.gray[700] }}>
                  {selectedOption?.label || snakeToTitleCase(selectedProvider)}
                </Typography>
                <KeyboardArrowDownIcon sx={{ fontSize: 18, color: ds.gray[500] }} />
              </Box>
            }
            items={providers.map((p) => ({
              id: `cloud-log-provider-${p.provider}`,
              label: p.label,
              icon: <CloudProviderIcon cloud_provider={p.iconKey} width='16px' height='16px' />,
              kbd: p.provider === selectedProvider ? '✓' : undefined,
              onSelect: () => handleProviderChange(p.provider),
            }))}
          />
        )}
        {!isNative && (
          <ToggleGroup
            selection='single'
            value={saasQLEditor}
            onChange={setSaasQLEditor}
            size='md'
            options={[...(selectedProvider !== 'datadog' ? [{ value: 'build', label: 'Builder' }] : []), { value: 'code', label: 'Code' }]}
          />
        )}
        {isNative && queryFilters}
        <FilterDropdown
          id='cloud-logs-limit'
          label='Limit'
          value={LIMIT_OPTIONS.find((o) => o.value === String(logLimit)) ?? null}
          options={LIMIT_OPTIONS}
          onSelect={(_e: any, item: any) => setLogLimit(Number(item?.value) || 100)}
        />
        {isNative && regionHint && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{regionHint}</Typography>}
      </ListingLayout.Toolbar>

      <ListingLayout.Body padding={`${ds.space[3]} ${ds.space[5]}`}>
        {isNative ? (
          <>
            {queryTextarea}

            <Box sx={{ mt: ds.space[2], mb: ds.space[3] }}>
              <CloudLogsQueryHelp provider={provider} onInsertQuery={handleInsertQuery} />
            </Box>
          </>
        ) : (
          <Box sx={{ mb: ds.space[3] }}>
            <QueryModeSwitcher
              accountId={accountId}
              logProvider={selectedProvider}
              providerOverride={selectedProvider}
              operatorDescriptors={selectedOption?.operatorDescriptors}
              params={{ startTime: dateRange.startTime, endTime: dateRange.endTime }}
              queryItems={saasQueryItems}
              setQueryItems={setSaasQueryItems}
              onQueryChange={(e: any) => {
                setSaasQuery(e.query);
                if (e.index !== undefined) setSaasEsIndex(e.index);
              }}
              qLEditor={saasQLEditor}
              setQLEditor={setSaasQLEditor}
              allowMultipleQueries={false}
              providerType='logs'
              initialEsIndex={saasEsIndex}
            />
          </Box>
        )}

        {executedQuery && (
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[1], mb: ds.space[2] }}>
            <Typography sx={{ fontSize: ds.text.small, fontWeight: 600, color: ds.gray[600], whiteSpace: 'nowrap' }}>
              {selectedProvider ? `${snakeToTitleCase(selectedProvider)} query:` : 'Query:'}
            </Typography>
            <Typography sx={{ fontFamily: 'monospace', fontSize: ds.text.small, color: ds.gray[700], wordBreak: 'break-all' }}>
              {executedQuery}
            </Typography>
          </Box>
        )}

        {error && (
          <Box sx={{ mb: ds.space[3] }}>
            <Banner tone='critical' surface='section' message={error} />
          </Box>
        )}

        {!error && !loading && !hasRows ? (
          <EmptyState size='inline' illustration='no-results' title='No log entries' description={emptyDescription} />
        ) : (
          <CustomTable
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
