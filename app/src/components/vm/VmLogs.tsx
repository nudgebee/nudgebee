import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import { Button } from '@ui/Button';
import { Skeleton } from '@ui/Skeleton';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import QueryModeSwitcher from '@components/k8s/common/QueryModeSwitcher';
import { OperatorDescriptor } from '@components/k8s/common/operatorCatalog';
import CloudLogsTable, { type LogEntry } from '@components/cloudaccount/cloud-logs/CloudLogsTable';
import observability from '@api1/observability';
import apiAccount from '@api1/account';
import { unwrapGraphQL } from '@lib/HttpService';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import { ds } from '@utils/colors';

/**
 * Query-only log search for a self-hosted VM account.
 *
 * A VM account has no Kubernetes agent and no cloud log service, so its logs come
 * from the one log store the account's default log provider points at (Loki or
 * SigNoz, reached through a forager). This tab asks the backend which provider that
 * is, then drives the same query builder and result table the K8s and cloud log
 * tabs use. It does no per-VM filtering: that is a label filter the user adds.
 */

const TABLE_ID = 'vmLogsViewerTable';

/** The Observability tab of the integrations page, where Loki and SigNoz are listed. */
const CONNECT_URL = '/user-management?integration=observability#integrations';

const LIMIT_OPTIONS = ['50', '100', '200', '500', '1000'].map((value) => ({ label: value, value }));

interface LogStore {
  provider: string;
  source: string;
  label: string;
  operatorDescriptors?: OperatorDescriptor[];
}

type StoreState = { kind: 'loading' } | { kind: 'none' } | { kind: 'error'; message: string } | { kind: 'ready'; store: LogStore };

const VmLogs: React.FC<{ accountId: string }> = ({ accountId }) => {
  const router = useRouter();
  const [storeState, setStoreState] = useState<StoreState>({ kind: 'loading' });

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<LogEntry[]>([]);
  const [hasRun, setHasRun] = useState(false);
  const [logLimit, setLogLimit] = useState(100);
  const [dateRange, setDateRange] = useState({
    startTime: Date.now() - 3600000,
    endTime: Date.now(),
    shortcutClickTime: 0,
  });

  const [query, setQuery] = useState('');
  const [queryItems, setQueryItems] = useState<any[]>([]);
  const [qLEditor, setQLEditor] = useState('build');
  // The provider-native query the backend actually executed, returned with the logs
  // so Builder mode can show what was run.
  const [executedQuery, setExecutedQuery] = useState('');

  // Which request is current, so a slow response for a previous account (or a
  // previous Run) cannot overwrite a newer one.
  const requestSeq = useRef(0);

  useEffect(() => {
    let cancelled = false;
    setStoreState({ kind: 'loading' });
    setData([]);
    setError(null);
    setHasRun(false);
    setExecutedQuery('');
    (async () => {
      let res: any;
      try {
        res = await apiAccount.getDefaultProvider({ account_id: accountId, provider_type: 'logs' });
      } catch (err: any) {
        // A network failure never reaches the resolver's errors[]; without this the tab
        // would stay on its loading state.
        if (!cancelled) setStoreState({ kind: 'error', message: err?.message || 'Failed to resolve the log store' });
        return;
      }
      if (cancelled) return;
      // The resolver reports "multiple providers, none is default" and similar as a
      // top-level error. Showing it verbatim tells the user what to change.
      const failure = res?.data?.errors?.[0]?.message;
      if (failure) {
        setStoreState({ kind: 'error', message: failure });
        return;
      }
      const result = res?.data?.data?.observability_get_default_provider;
      if (!result?.provider) {
        setStoreState({ kind: 'none' });
        return;
      }
      const available = Array.isArray(result.available_providers) ? result.available_providers : [];
      const entry = available.find((p: any) => p?.provider === result.provider);
      setStoreState({
        kind: 'ready',
        store: {
          provider: result.provider,
          source: result.integration_source || 'user',
          label: snakeToTitleCase(result.provider),
          operatorDescriptors: entry?.supported_operator_descriptors,
        },
      });
      // Datadog has no Builder mode; every other provider opens in it.
      setQLEditor(result.provider === 'datadog' ? 'code' : 'build');
    })();
    return () => {
      cancelled = true;
    };
  }, [accountId]);

  const store = storeState.kind === 'ready' ? storeState.store : undefined;

  const fetchData = useCallback(async () => {
    if (!store) return;

    const requestPayload: any = {
      account_id: accountId,
      log_provider: store.provider,
      log_provider_source: store.source,
      query,
      start_time: dateRange.startTime,
      end_time: dateRange.endTime,
      limit: logLimit,
      offset: 0,
    };
    if (qLEditor === 'build') {
      // Builder mode emits a JSON where-array through onQueryChange.
      const trimmed = typeof query === 'string' ? query.trim() : '';
      const parsed = trimmed.startsWith('[') ? safeJSONParse(trimmed) : null;
      if (!Array.isArray(parsed) || parsed.length === 0) {
        setError('Select at least one label filter, or switch to Code and write a query.');
        setData([]);
        return;
      }
      requestPayload.query_request = { where: { _and: parsed } };
      requestPayload.query = '';
    } else if (!query.trim()) {
      setError('Write a query, or switch to Builder and select a label filter.');
      setData([]);
      return;
    }

    const seq = ++requestSeq.current;
    setLoading(true);
    setError(null);
    try {
      const response = await observability.fetchLogs(requestPayload);
      // A failed query (e.g. a syntax error) comes back as HTTP 200 with a top-level
      // errors array; unwrapGraphQL turns that into the message the user can act on
      // instead of rendering an empty "No log entries".
      const result = unwrapGraphQL(response, 'Failed to fetch logs');
      // A non-GraphQL failure (502/504, network) leaves result undefined with no
      // errors[] to throw on; that is a failure too, not an empty result.
      if (!result) throw new Error('Failed to fetch logs');
      if (seq !== requestSeq.current) return;
      setData(result?.logs_list?.logs || []);
      setExecutedQuery(result?.logs_list?.query || '');
      setHasRun(true);
    } catch (err: any) {
      if (seq !== requestSeq.current) return;
      setError(err?.response?.data?.errors?.[0]?.message || err?.message || 'Failed to fetch logs');
      setData([]);
    } finally {
      if (seq === requestSeq.current) setLoading(false);
    }
  }, [accountId, store, query, qLEditor, dateRange, logLimit]);

  const handleDateRangeChange = (selection: any) => {
    if (selection.shortcutClickTime > 0) {
      setDateRange({
        startTime: Date.now() - selection.shortcutClickTime,
        endTime: Date.now(),
        shortcutClickTime: selection.shortcutClickTime,
      });
    } else {
      setDateRange({ startTime: selection.startTime, endTime: selection.endTime, shortcutClickTime: 0 });
    }
  };

  // Changing the time range re-runs a query the user has already run, like the cloud viewer.
  const firstRange = useRef(true);
  useEffect(() => {
    if (firstRange.current) {
      firstRange.current = false;
      return;
    }
    if (hasRun) fetchData();
  }, [dateRange]);

  if (storeState.kind === 'loading') {
    return (
      <Box sx={{ p: ds.space[5] }} id='vm-logs-loading'>
        <Skeleton />
      </Box>
    );
  }

  if (storeState.kind === 'error') {
    return (
      <Box sx={{ p: ds.space[5] }}>
        <Banner id='vm-logs-provider-error' surface='section' tone='critical' message={storeState.message} />
      </Box>
    );
  }

  if (storeState.kind === 'none' || !store) {
    return (
      <EmptyState
        size='page'
        illustration='no-results'
        title='No log store connected'
        description='Connect Loki or SigNoz to this account (connection mode: VM agent for a store only a forager can reach) and make it the default log provider.'
        action={{ id: 'vm-logs-connect', label: 'Connect a log store', onClick: () => router.push(CONNECT_URL) }}
      />
    );
  }

  const hasRows = data.length > 0;

  return (
    <ListingLayout id='vm-logs-viewer'>
      <ListingLayout.Toolbar
        actions={
          <>
            <Button id='vm-logs-run' tone='primary' size='md' onClick={fetchData} loading={loading} disabled={loading}>
              Run Query
            </Button>
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
        <Typography id='vm-logs-store' sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
          {`Log store: ${store.label}`}
        </Typography>
        <ToggleGroup
          selection='single'
          value={qLEditor}
          onChange={setQLEditor}
          size='md'
          options={[...(store.provider !== 'datadog' ? [{ value: 'build', label: 'Builder' }] : []), { value: 'code', label: 'Code' }]}
        />
        <FilterDropdown
          id='vm-logs-limit'
          label='Limit'
          value={LIMIT_OPTIONS.find((o) => o.value === String(logLimit)) ?? null}
          options={LIMIT_OPTIONS}
          onSelect={(_e: any, item: any) => setLogLimit(Number(item?.value) || 100)}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body padding={`${ds.space[3]} ${ds.space[5]}`}>
        <Box sx={{ mb: ds.space[3] }}>
          <QueryModeSwitcher
            accountId={accountId}
            logProvider={store.provider}
            providerOverride={store.provider}
            operatorDescriptors={store.operatorDescriptors}
            params={{ startTime: dateRange.startTime, endTime: dateRange.endTime }}
            limit={logLimit}
            queryItems={queryItems}
            setQueryItems={setQueryItems}
            onQueryChange={(e: any) => setQuery(e.query)}
            qLEditor={qLEditor}
            setQLEditor={setQLEditor}
            allowMultipleQueries={false}
            providerType='logs'
          />
        </Box>

        {executedQuery && (
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[1], mb: ds.space[2] }}>
            <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[600], whiteSpace: 'nowrap' }}>
              {`${store.label} query:`}
            </Typography>
            <Typography sx={{ fontFamily: ds.font.mono, fontSize: ds.text.small, color: ds.gray[700], wordBreak: 'break-all' }}>
              {executedQuery}
            </Typography>
          </Box>
        )}

        {error && (
          <Box sx={{ mb: ds.space[3] }}>
            <Banner id='vm-logs-error' tone='critical' surface='section' message={error} />
          </Box>
        )}

        {!error && !loading && !hasRows ? (
          <EmptyState
            size='inline'
            illustration='no-results'
            title={hasRun ? 'No log entries' : 'Run a query'}
            description={
              hasRun
                ? 'No log entries found for the selected time range and query.'
                : 'Select a label filter (or write a query), then click "Run Query" to fetch logs.'
            }
          />
        ) : (
          <CloudLogsTable id={TABLE_ID} logs={data} loading={loading} />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default VmLogs;
