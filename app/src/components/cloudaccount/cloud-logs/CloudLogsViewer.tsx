import React, { useState, useCallback, useRef, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import { KeyboardArrowDown as KeyboardArrowDownIcon } from '@mui/icons-material';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { DropdownMenu } from '@ui/DropdownMenu';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import { Button as DsButton } from '@ui/Button';
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
import CloudLogsTable, { type LogEntry } from './CloudLogsTable';

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
    // The IS NULL chip is value-less, so the builder gives it value ''. _is_null
    // carries its meaning in the value and the backend requires a boolean there,
    // so '' was rejected and the whole filter was dropped.
    if (op === '_is_null' && typeof value !== 'boolean') {
      value = true;
    }
    return { _binary: { [item.label]: { [op]: value } } };
  });

interface CloudLogsViewerProps {
  accountId: string;
  provider: 'AWS' | 'Azure' | 'GCP';
}

const TABLE_ID = 'cloudLogsViewerTable';

const LIMIT_OPTIONS = [
  { label: '50', value: '50' },
  { label: '100', value: '100' },
  { label: '200', value: '200' },
  { label: '500', value: '500' },
  { label: '1000', value: '1000' },
];

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

  const handleInsertQuery = (query: string) => {
    setQuery(query);
  };

  const hasRows = data.length > 0;

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
          <CloudLogsTable id={TABLE_ID} logs={data} loading={loading} />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default CloudLogsViewer;
