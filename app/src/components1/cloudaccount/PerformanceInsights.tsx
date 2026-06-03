import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Tabs as DsTabs, type TabItem } from '@components1/ds/Tabs';
import Banner from '@components1/ds/Banner';
import Chip from '@components1/ds/Chip';
import Tooltip from '@components1/ds/Tooltip';
import Skeleton from '@components1/ds/Skeleton';
import WidgetCard from '@components1/ds/WidgetCard';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { Button as DsButton } from '@components1/ds/Button';
import DownloadButton from '@common-new/DownloadButton';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import CustomTable2 from '@common-new/tables/CustomTable2';
import Charts from '@components1/common/charts/LineCharts';
import SafeIcon from '@components1/common/SafeIcon';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import useTicketFliter from '@hooks/useTicketFliter';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { md5 } from '@lib/encode';
import {
  queryDatabasePerformance,
  type QueryDatabasePerformanceResponse,
  type PerformanceQuery,
  type PerformanceWaitEvent,
  type PerformanceMetric,
} from '@api1/cloud-account/performance-insights';
import { getLast7Days } from '@lib/datetime';
import { getClusterData } from '@context/DataContext';
import { ds } from '@utils/colors';
import { CustomText } from '@components1/cloudaccount/common';

interface PerformanceInsightsProps {
  accountId: string;
  databaseIdentifier: string;
  region: string;
}

const TAB_KEYS = {
  LOAD: 'load',
  QUERIES: 'queries',
  EVENTS: 'events',
  METRICS: 'metrics',
} as const;
type TabKey = (typeof TAB_KEYS)[keyof typeof TAB_KEYS];

const CARD_ID = 'performance-insights-card';
const TOP_QUERIES_TABLE_ID = 'topQueriesTable';
const WAIT_EVENTS_TABLE_ID = 'waitEventsTable';

const formatNumber = (num: number): string => {
  if (num >= 1000000) {
    return (num / 1000000).toFixed(2) + 'M';
  }
  if (num >= 1000) {
    return (num / 1000).toFixed(2) + 'K';
  }
  return num.toFixed(2);
};

// Helper to detect if text looks like a SQL query ID (hex string)
const isSqlId = (text: string): boolean => {
  if (!text) {
    return false;
  }
  return /^[A-F0-9]{20,}$/i.test(text.replace(/\s/g, ''));
};

const formatTimestamp = (ts: number): string => {
  const date = new Date(ts);
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
};

// Map a raw db-load number to a DS Chip tone.
//   > 5  → critical   (red)
//   > 1  → warning    (amber)
//   > 0.5→ warning    (amber, narrower headroom)
//   else → success    (green)
const getDbLoadTone = (load: number): 'critical' | 'warning' | 'success' => {
  if (load > 5) return 'critical';
  if (load > 0.5) return 'warning';
  return 'success';
};

// Map a wait-event-type category to a DS Chip hue (categorical, not semantic).
const getEventTypeHue = (type: string): 'blue' | 'amber' | 'red' | 'violet' | 'slate' => {
  const map: Record<string, 'blue' | 'amber' | 'red' | 'violet' | 'slate'> = {
    CPU: 'blue',
    IO: 'amber',
    Lock: 'red',
    Network: 'violet',
    Other: 'slate',
  };
  return map[type] || 'slate';
};

// Categorical color used by the inline progress bar in WaitEventsTable.
// Tier C: hex literals required because Chart.js renders to <canvas>, which
// cannot resolve CSS var() at paint time. Hues are semantically aligned with
// DS tokens (ds.blue/amber/red/purple/gray-500) so the chart reads coherent
// with the rest of the surface. Keep in sync if DS palettes shift.
const CHART_EVENT_TYPE_PALETTE: Record<string, string> = {
  CPU: '#3B82F6', // ≈ ds.blue[500]
  IO: '#F59E0B', // ≈ ds.amber[500]
  Lock: '#EF4444', // ≈ ds.red[500]
  Network: '#8B5CF6', // ≈ ds.purple[500]
  Other: '#6B7280', // ≈ ds.gray[500]
};

const getEventTypeBarColor = (type: string): string => CHART_EVENT_TYPE_PALETTE[type] || CHART_EVENT_TYPE_PALETTE['Other'];

const LoadMetricsChart = ({ data, loading }: { data: QueryDatabasePerformanceResponse; loading: boolean }) => {
  const loadMetrics = data.load_metrics || [];

  if (loadMetrics.length === 0) {
    return <Banner tone='info' surface='section' message='No load metrics available' />;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
      {loadMetrics.map((metric: PerformanceMetric) => {
        const labels = metric.timestamps.map((ts) => formatTimestamp(ts));
        const chartData = [{ label: metric.name, data: metric.values }];

        return (
          <WidgetCard key={metric.name}>
            <Charts chartTitle={`${metric.name} (${metric.unit})`} dataset={chartData} labels={labels} data={[]} loading={loading} />
          </WidgetCard>
        );
      })}
    </Box>
  );
};

const SqlQueryCell = ({ query }: { query: PerformanceQuery }) => {
  const queryText = query.query_text || query.query_id;
  const isJustId = isSqlId(queryText);

  if (isJustId) {
    return (
      <Tooltip title='Full SQL text not available. This shows the query digest/hash.'>
        <Box>
          <Typography
            sx={{
              fontSize: ds.text.small,
              fontFamily: 'monospace',
              color: ds.gray[500],
              fontStyle: 'italic',
            }}
          >
            Query Digest: {queryText.substring(0, 40)}...
          </Typography>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[400] }}>ID: {query.query_id}</Typography>
        </Box>
      </Tooltip>
    );
  }

  return (
    <Tooltip title={queryText}>
      <Box>
        <Typography
          sx={{
            fontSize: ds.text.small,
            fontFamily: 'monospace',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            maxHeight: ds.space.mul(0, 30),
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {queryText.substring(0, 150)}
          {queryText.length > 150 ? '...' : ''}
        </Typography>
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>ID: {query.query_id}</Typography>
      </Box>
    </Tooltip>
  );
};

const DbLoadChip = ({ load }: { load: number }) => (
  <Chip variant='count' tone={getDbLoadTone(load)} size='xs'>
    {load?.toFixed(2) || '0.00'}
  </Chip>
);

const TopQueriesTable = ({
  queries,
  provider,
  databaseIdentifier,
  region,
  getMenuItem,
  onMenuClick,
  onAnalyzeQuery,
}: {
  queries: PerformanceQuery[];
  provider?: string;
  databaseIdentifier: string;
  region: string;
  getMenuItem: any;
  onMenuClick: any;
  onAnalyzeQuery: (query: PerformanceQuery) => void;
}) => {
  const { assistantName } = useTenantBranding();
  if (!queries || queries.length === 0) {
    return (
      <Banner tone='info' surface='section' message='No top queries available. Enable Performance Insights and wait for data to be collected.' />
    );
  }

  const isGCP = provider === 'gcp';

  const headers = isGCP
    ? ['SQL Query', 'Load by Total Time (s)', 'Avg Execution Time (ms)', 'Times Called', 'Avg Rows Returned', '']
    : ['SQL Query', 'DB Load (AAS)', 'Executions', 'Avg Duration', ''];

  const tableData = queries.map((query: PerformanceQuery) => {
    const sqlCell = {
      component: (
        <Box sx={{ maxWidth: ds.space.mul(0, 250) }}>
          <SqlQueryCell query={query} />
        </Box>
      ),
    };

    const menuDataPayload = {
      data: query.query_text || query.query_id,
      stream: { labels: { app: 'database', namespace: provider || 'unknown', container: databaseIdentifier } },
      investigation_reason: 'High Database Load / Slow Query Performance',
      database_instance: databaseIdentifier,
      region: region,
      database_provider: provider,
      query_digest_id: query.query_id,
      query_text: query.query_text ? query.query_text.substring(0, 500) + (query.query_text.length > 500 ? '...' : '') : '',
      db_load_aas: query.database_load,
      average_duration_ms: query.avg_duration,
      execution_count: query.execution_count,
      average_rows_processed: query.avg_rows_processed,
    };

    const legacyItems = getMenuItem();
    const dsMenuItems = (legacyItems || []).map((m: any) => ({
      id: `${TOP_QUERIES_TABLE_ID}-action-${query.query_id}-${m.id}`,
      icon: m.icon ? <SafeIcon src={m.icon} alt='' width={14} height={14} /> : undefined,
      label: m.label,
      disabled: m.disabled,
      onSelect: () => onMenuClick(m, menuDataPayload),
    }));

    const actionsCell = {
      component: (
        <Box display='flex' justifyContent='flex-end' alignItems='center'>
          <Tooltip title={`Analyze query performance with ${assistantName}`} placement='top'>
            <span>
              <DsButton
                id={`analyze-query-${query.query_id}`}
                data-testid={`analyze-query-${query.query_id}`}
                aria-label={`Analyze query performance for ${query.query_id}`}
                tone='ghost'
                size='xs'
                composition='icon-only'
                onClick={(event: React.MouseEvent) => {
                  event.stopPropagation();
                  onAnalyzeQuery(query);
                }}
                icon={<SafeIcon alt='' src={getNubiIconUrl()} width={16} height={16} />}
              />
            </span>
          </Tooltip>
          <DsDropdownMenu
            align='end'
            size='sm'
            items={dsMenuItems}
            trigger={<DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon />} />}
          />
        </Box>
      ),
    };

    if (isGCP) {
      return [
        sqlCell,
        { component: <CustomText text1={formatNumber(query.database_load)} /> },
        { component: <CustomText text1={query.avg_duration > 0 ? `${query.avg_duration.toFixed(2)}` : '-'} /> },
        { component: <CustomText text1={query.execution_count > 0 ? formatNumber(query.execution_count) : '-'} /> },
        { component: <CustomText text1={query.avg_rows_processed != null ? formatNumber(query.avg_rows_processed) : '-'} /> },
        actionsCell,
      ];
    }

    return [
      sqlCell,
      { component: <DbLoadChip load={query.database_load} /> },
      {
        component: (
          <CustomText
            text1={query.execution_count > 0 ? formatNumber(query.execution_count) : '-'}
            subtext1={query.execution_count > 0 ? 'total' : 'N/A'}
          />
        ),
      },
      {
        component: (
          <CustomText
            text1={query.avg_duration > 0 ? `${query.avg_duration.toFixed(2)} ms` : '-'}
            subtext1={query.avg_duration > 0 ? 'average' : 'N/A'}
          />
        ),
      },
      actionsCell,
    ];
  });

  return <CustomTable2 id={TOP_QUERIES_TABLE_ID} headers={headers} tableData={tableData} rowsPerPage={10} />;
};

const WaitEventsTable = ({ events, provider }: { events: PerformanceWaitEvent[]; provider?: string }) => {
  if (!events || events.length === 0) {
    return <Banner tone='info' surface='section' message='No wait events available' />;
  }

  const isGCP = provider === 'gcp';
  const headers = ['Wait Event Type', 'Wait Event Name', isGCP ? 'Total Wait Time (s)' : 'DB Load (AAS)', 'Percentage'];

  const tableData = events.map((event: PerformanceWaitEvent) => [
    {
      component: (
        <Chip variant='tag' hue={getEventTypeHue(event.event_type)} size='xs'>
          {event.event_type}
        </Chip>
      ),
    },
    { component: <CustomText text1={event.event_name} /> },
    {
      component: (
        <Chip variant='count' tone={event.database_load > 1 ? 'warning' : 'neutral'} size='xs'>
          {event.database_load?.toFixed(2) || '0'}
        </Chip>
      ),
    },
    {
      component: (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
          <Box
            sx={{
              width: ds.space.mul(0, 30),
              height: ds.space.mul(0, 3),
              backgroundColor: ds.gray[200],
              borderRadius: ds.radius.lg,
              overflow: 'hidden',
            }}
          >
            <Box
              sx={{
                width: `${Math.min(event.percentage || 0, 100)}%`,
                height: '100%',
                backgroundColor: getEventTypeBarColor(event.event_type),
              }}
            />
          </Box>
          <Typography sx={{ fontSize: ds.text.small, minWidth: ds.space.mul(0, 22) }}>{(event.percentage || 0).toFixed(1)}%</Typography>
        </Box>
      ),
    },
  ]);

  return <CustomTable2 id={WAIT_EVENTS_TABLE_ID} headers={headers} tableData={tableData} rowsPerPage={10} />;
};

const ResourceMetricsChart = ({ data, loading }: { data: QueryDatabasePerformanceResponse; loading: boolean }) => {
  const resourceMetrics = data.resource_metrics || [];

  if (resourceMetrics.length === 0) {
    return <Banner tone='info' surface='section' message='No resource metrics available' />;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
      {resourceMetrics.map((metric: PerformanceMetric) => {
        const labels = metric.timestamps.map((ts) => formatTimestamp(ts));
        const chartData = [{ label: metric.name, data: metric.values }];

        return (
          <WidgetCard key={metric.name}>
            <Charts chartTitle={`${metric.name} (${metric.unit})`} dataset={chartData} labels={labels} data={[]} loading={loading} />
          </WidgetCard>
        );
      })}
    </Box>
  );
};

const getSourceType = (cloudProvider: string) => {
  const provider = cloudProvider?.toLowerCase();
  if (provider === 'aws') return 'aws';
  if (provider === 'azure') return 'azure';
  if (provider === 'gcp') return 'gcp';
  return 'unknown';
};

const PerformanceInsights: React.FC<PerformanceInsightsProps> = ({ accountId, databaseIdentifier, region }) => {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<QueryDatabasePerformanceResponse | null>(null);
  const [activeTab, setActiveTab] = useState<TabKey>(TAB_KEYS.LOAD);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiSessionId, setNubiSessionId] = useState('');

  const {
    ticketData,
    isTicketCreateFormOpen,
    getMenuItem,
    onMenuClick,
    closeTicketCreateForm,
    getTicketDescription,
    getTicketReferenceId,
    handleTicketSuccess,
    handleTicketFailure,
  } = useTicketFliter();

  const handleAnalyzeQuery = useCallback(
    (query: PerformanceQuery) => {
      const dbProvider = data?.provider || 'Unknown';
      const analysisPrompt = `Analyze the following database query performance and provide insights and possible optimizations.

Database Connection Details:
- Host/Database Identifier: ${databaseIdentifier}
- Region: ${region}
- Provider: ${dbProvider}

Query Performance Metrics:
- Database Load (AAS): ${query.database_load ?? '-'}
- Average Duration: ${query.avg_duration ?? '-'} ms
- Execution Count: ${query.execution_count ?? '-'}
- Average Rows Processed: ${query.avg_rows_processed ?? '-'}

Query:
${query.query_text || query.query_id}`;

      setNubiQuery(analysisPrompt);
      setNubiSessionId(md5([JSON.stringify(query)]));
      setNubiSidebarVisible(true);
    },
    [data?.provider, databaseIdentifier, region]
  );

  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const response = await queryDatabasePerformance({
        account_id: accountId,
        database_identifier: databaseIdentifier,
        region: region,
        start_time: new Date(selectedDateRange.startDate).toISOString(),
        end_time: new Date(selectedDateRange.endDate).toISOString(),
        granularity_seconds: 300, // 5 minute granularity
        include_top_queries: true,
        include_wait_events: true,
        include_top_users: true,
        include_top_hosts: true,
        top_n: 10,
      });

      setData(response);
    } catch (err: any) {
      setError(err.message || 'Failed to fetch performance insights');
    } finally {
      setLoading(false);
    }
  }, [accountId, databaseIdentifier, region, selectedDateRange]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  if (error) {
    return <Banner tone='critical' surface='page' message={error} />;
  }

  if (!loading && !data) {
    return <Banner tone='info' surface='page' message='No performance insights data available' />;
  }

  const isGCP = data?.provider === 'gcp';

  if (!loading && data && !data.performance_enabled) {
    const message = isGCP
      ? 'Query Insights is not enabled for this database. Enable it in the GCP Console to view performance data.'
      : 'Performance Insights is not enabled for this database. Enable it in the AWS Console to view performance data.';
    return <Banner tone='warning' surface='page' message={message} />;
  }

  // GCP doesn't expose the Database Load tab.
  const tabs: TabItem[] = [
    ...(!isGCP ? [{ id: TAB_KEYS.LOAD, label: 'Database Load' }] : []),
    { id: TAB_KEYS.QUERIES, label: 'Top Queries' },
    { id: TAB_KEYS.EVENTS, label: 'Wait Events' },
    { id: TAB_KEYS.METRICS, label: 'Resource Metrics' },
  ];

  // If the previously-active tab isn't in the current set (e.g. provider flipped to GCP and 'load' vanished), fall back to the first tab.
  const safeActiveTab: TabKey = tabs.find((t) => t.id === activeTab) ? activeTab : (tabs[0]?.id as TabKey);

  const dateTimePicker = (
    <CustomDateTimeRangePicker
      passedSelectedDateTime={{
        startTime: selectedDateRange.startDate,
        endTime: selectedDateRange.endDate,
        shortcutClickTime: 0,
      }}
      onChange={handleDateRangeChange}
      shortCuts={[
        'Last 5 Minutes',
        'Last 10 Minutes',
        'Last 15 Minutes',
        'Last 30 Minutes',
        'Last 1 Hour',
        'Last 3 Hours',
        'Last 6 Hours',
        'Last 12 Hours',
        'Last 24 Hours',
        'Current Week',
      ]}
    />
  );

  const tabTitle: Record<TabKey, string> = {
    load: 'Database Load (Average Active Sessions)',
    queries: isGCP ? 'Top SQL Queries by Total Time' : 'Top SQL Queries by DB Load',
    events: 'Wait Events',
    metrics: 'Resource Metrics (CPU, Memory, I/O)',
  };

  // Only the table-shaped tabs are downloadable — DownloadButton scrapes a DOM table by id,
  // so chart tabs (load, metrics) would export an empty CSV. Hide the button on those tabs.
  const tabDownloadTableId: Partial<Record<TabKey, string>> = {
    queries: TOP_QUERIES_TABLE_ID,
    events: WAIT_EVENTS_TABLE_ID,
  };
  const downloadTableId = tabDownloadTableId[safeActiveTab];

  const loadingPlaceholder = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
      {Array.from({ length: 3 }).map((_, idx) => (
        <Skeleton key={idx} shape='rect' height={260} ariaLabel={`Loading ${tabTitle[safeActiveTab]}`} />
      ))}
    </Box>
  );

  const renderTabBody = () => {
    if (loading || !data) return loadingPlaceholder;
    switch (safeActiveTab) {
      case TAB_KEYS.LOAD:
        return <LoadMetricsChart data={data} loading={loading} />;
      case TAB_KEYS.QUERIES:
        return (
          <TopQueriesTable
            queries={data.top_queries}
            provider={data.provider}
            databaseIdentifier={databaseIdentifier}
            region={region}
            getMenuItem={getMenuItem}
            onMenuClick={onMenuClick}
            onAnalyzeQuery={handleAnalyzeQuery}
          />
        );
      case TAB_KEYS.EVENTS:
        return <WaitEventsTable events={data.wait_events} provider={data.provider} />;
      case TAB_KEYS.METRICS:
        return <ResourceMetricsChart data={data} loading={loading} />;
      default:
        return null;
    }
  };

  return (
    <Box>
      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={accountId}
        query={nubiQuery}
        context={{ type: 'general', data: { conversationId: nubiSessionId, databaseIdentifier } }}
        apiMode='investigate'
        source='query_analysis'
        position='right'
        mode='overlay'
        width={ds.space.mul(0, 250)}
      />

      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: `Investigate Database Query Performance - ${databaseIdentifier}`,
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        reference={{
          id: getTicketReferenceId(ticketData),
          type: getSourceType(getClusterData(accountId)?.cloud_provider),
        }}
      />

      <Box sx={{ mb: ds.space[3] }}>
        <DsTabs tabs={tabs} value={safeActiveTab} onChange={(next) => setActiveTab(next as TabKey)} ariaLabel='Performance insights sections' />
      </Box>

      <ListingLayout id={CARD_ID}>
        <ListingLayout.Toolbar
          title={tabTitle[safeActiveTab]}
          data-testid={`${CARD_ID}-toolbar`}
          actions={
            <>
              {dateTimePicker}
              {downloadTableId && <DownloadButton id={`${CARD_ID}-download`} onClick={() => ({ tableId: downloadTableId })} />}
            </>
          }
        />
        <ListingLayout.Body>{renderTabBody()}</ListingLayout.Body>
      </ListingLayout>
    </Box>
  );
};

export default PerformanceInsights;
