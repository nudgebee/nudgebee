import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography, CircularProgress, Alert, Chip, Tooltip } from '@mui/material';
import BoxLayout2 from '@common/BoxLayout2';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import Charts from '@components1/common/charts/LineCharts';
import SafeIcon from '@components1/common/SafeIcon';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomIconButton from '@components1/CustomIconButton';
import { ThreeDotsMenu } from '@components1/common';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import useTicketFliter from '@hooks/useTicketFliter';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { md5 } from '@lib/encode';
import { action } from '@utils/actionStyles';
import {
  queryDatabasePerformance,
  type QueryDatabasePerformanceResponse,
  type PerformanceQuery,
  type PerformanceWaitEvent,
  type PerformanceMetric,
} from '@api1/cloud-account/performance-insights';
import { getLast7Days } from '@lib/datetime';
import CustomTabs from '@components1/common/CustomTabs';
import { getClusterData } from '@context/DataContext';

interface PerformanceInsightsProps {
  accountId: string;
  databaseIdentifier: string;
  region: string;
}

const CustomText = ({ text1, subtext1 }: { text1: string; subtext1?: string }) => (
  <Box>
    <Typography sx={{ fontSize: 13 }}>{text1}</Typography>
    {subtext1 && <Typography sx={{ fontSize: 11, color: '#9F9F9F' }}>{subtext1}</Typography>}
  </Box>
);

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
  // Check if it's a hex string (like FE2BA6E3EF049AC6AC...)
  return /^[A-F0-9]{20,}$/i.test(text.replace(/\s/g, ''));
};

// Format timestamp for display
const formatTimestamp = (ts: number): string => {
  const date = new Date(ts);
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
};

// Helper to get background color based on DB load value
const getDbLoadBackgroundColor = (load: number): string => {
  if (load > 5) {
    return '#FEE2E2';
  }
  if (load > 1) {
    return '#FEF3C7';
  }
  if (load > 0.5) {
    return '#FEF9C3';
  }
  return '#D1FAE5';
};

// Helper to get text color based on DB load value
const getDbLoadTextColor = (load: number): string => {
  if (load > 5) {
    return '#DC2626';
  }
  if (load > 1) {
    return '#D97706';
  }
  if (load > 0.5) {
    return '#CA8A04';
  }
  return '#059669';
};

const LoadMetricsChart = ({ data, loading }: { data: QueryDatabasePerformanceResponse; loading: boolean }) => {
  const loadMetrics = data.load_metrics || [];

  if (loadMetrics.length === 0) {
    return <Alert severity='info'>No load metrics available</Alert>;
  }

  return (
    <Box sx={{ width: '100%' }}>
      {loadMetrics.map((metric: PerformanceMetric) => {
        const labels = metric.timestamps.map((ts) => formatTimestamp(ts));
        const chartData = [
          {
            label: metric.name,
            data: metric.values,
          },
        ];

        return (
          <Box
            key={metric.name}
            sx={{
              mb: '24px',
              background: 'white',
              borderRadius: '8px',
              border: '1px solid #EBEBEB',
              boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
              p: '20px',
            }}
          >
            <Charts chartTitle={`${metric.name} (${metric.unit})`} dataset={chartData} labels={labels} data={[]} loading={loading} />
          </Box>
        );
      })}
    </Box>
  );
};

// Component for SQL query cell display
const SqlQueryCell = ({ query }: { query: PerformanceQuery }) => {
  const queryText = query.query_text || query.query_id;
  const isJustId = isSqlId(queryText);

  if (isJustId) {
    return (
      <Tooltip title='Full SQL text not available. This shows the query digest/hash.' arrow>
        <Box>
          <Typography
            sx={{
              fontSize: 12,
              fontFamily: 'monospace',
              color: '#6B7280',
              fontStyle: 'italic',
            }}
          >
            Query Digest: {queryText.substring(0, 40)}...
          </Typography>
          <Typography sx={{ fontSize: 10, color: '#9CA3AF' }}>ID: {query.query_id}</Typography>
        </Box>
      </Tooltip>
    );
  }

  return (
    <Tooltip title={queryText} arrow>
      <Box>
        <Typography
          sx={{
            fontSize: 12,
            fontFamily: 'monospace',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            maxHeight: 60,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {queryText.substring(0, 150)}
          {queryText.length > 150 ? '...' : ''}
        </Typography>
        <Typography sx={{ fontSize: 10, color: '#9F9F9F' }}>ID: {query.query_id}</Typography>
      </Box>
    </Tooltip>
  );
};

// Component for DB load chip display
const DbLoadChip = ({ load }: { load: number }) => (
  <Chip
    label={load?.toFixed(2) || '0.00'}
    size='small'
    sx={{
      fontWeight: 600,
      backgroundColor: getDbLoadBackgroundColor(load),
      color: getDbLoadTextColor(load),
    }}
  />
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
    return <Alert severity='info'>No top queries available. Enable Performance Insights and wait for data to be collected.</Alert>;
  }

  const isGCP = provider === 'gcp';

  const headers = isGCP
    ? ['SQL Query', 'Load by Total Time (s)', 'Avg Execution Time (ms)', 'Times Called', 'Avg Rows Returned', '']
    : ['SQL Query', 'DB Load (AAS)', 'Executions', 'Avg Duration', ''];

  const tableData = queries.map((query: PerformanceQuery) => {
    const sqlCell = {
      component: (
        <Box sx={{ maxWidth: 500 }}>
          <SqlQueryCell query={query} />
        </Box>
      ),
    };

    const actionsCell = {
      component: (
        <Box display={'flex'} justifyContent={'flex-end'}>
          <Tooltip title={`Analyze query performance with ${assistantName}`} placement='top' arrow>
            <CustomIconButton
              id={`analyze-query-${query.query_id}`}
              data-testid={`analyze-query-${query.query_id}`}
              aria-label={`Analyze query performance for ${query.query_id}`}
              onClick={(event: React.MouseEvent) => {
                event.stopPropagation();
                onAnalyzeQuery(query);
              }}
              variant={'secondary'}
              size={'xsmall'}
              sx={{ height: '28px', mr: '4px', width: '28px' }}
            >
              <SafeIcon alt={`Ask ${assistantName}`} src={getNubiIconUrl()} width={24} height={24} />
            </CustomIconButton>
          </Tooltip>
          <ThreeDotsMenu
            sx={{ ...action.primary }}
            menuItems={getMenuItem()}
            data={{
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
            }}
            onMenuClick={onMenuClick}
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

  return <CustomTable2 id='topQueriesTable' headers={headers} tableData={tableData} rowsPerPage={10} />;
};

const WaitEventsTable = ({ events, provider }: { events: PerformanceWaitEvent[]; provider?: string }) => {
  if (!events || events.length === 0) {
    return <Alert severity='info'>No wait events available</Alert>;
  }

  const isGCP = provider === 'gcp';
  const headers = ['Wait Event Type', 'Wait Event Name', isGCP ? 'Total Wait Time (s)' : 'DB Load (AAS)', 'Percentage'];

  // Color coding for wait event types
  const getEventTypeColor = (type: string) => {
    const colors: Record<string, string> = {
      CPU: '#3B82F6',
      IO: '#F59E0B',
      Lock: '#EF4444',
      Network: '#8B5CF6',
      Other: '#6B7280',
    };
    return colors[type] || colors['Other'];
  };

  const tableData = events.map((event: PerformanceWaitEvent) => [
    {
      component: (
        <Chip
          label={event.event_type}
          size='small'
          sx={{
            backgroundColor: getEventTypeColor(event.event_type) + '20',
            color: getEventTypeColor(event.event_type),
            fontWeight: 500,
          }}
        />
      ),
    },
    { component: <CustomText text1={event.event_name} /> },
    {
      component: (
        <Chip
          label={event.database_load?.toFixed(2) || '0'}
          size='small'
          sx={{
            backgroundColor: event.database_load > 1 ? '#FEF3C7' : '#E5E7EB',
            color: event.database_load > 1 ? '#D97706' : '#374151',
          }}
        />
      ),
    },
    {
      component: (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Box
            sx={{
              width: 60,
              height: 6,
              backgroundColor: '#E5E7EB',
              borderRadius: 3,
              overflow: 'hidden',
            }}
          >
            <Box
              sx={{
                width: `${Math.min(event.percentage || 0, 100)}%`,
                height: '100%',
                backgroundColor: getEventTypeColor(event.event_type),
              }}
            />
          </Box>
          <Typography sx={{ fontSize: 12, minWidth: 45 }}>{(event.percentage || 0).toFixed(1)}%</Typography>
        </Box>
      ),
    },
  ]);

  return <CustomTable2 id='waitEventsTable' headers={headers} tableData={tableData} rowsPerPage={10} />;
};

const ResourceMetricsChart = ({ data, loading }: { data: QueryDatabasePerformanceResponse; loading: boolean }) => {
  const resourceMetrics = data.resource_metrics || [];

  if (resourceMetrics.length === 0) {
    return <Alert severity='info'>No resource metrics available</Alert>;
  }

  return (
    <Box sx={{ width: '100%' }}>
      {resourceMetrics.map((metric: PerformanceMetric) => {
        const labels = metric.timestamps.map((ts) => formatTimestamp(ts));
        const chartData = [
          {
            label: metric.name,
            data: metric.values,
          },
        ];

        return (
          <Box
            key={metric.name}
            sx={{
              mb: '24px',
              background: 'white',
              borderRadius: '8px',
              border: '1px solid #EBEBEB',
              boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
              p: '20px',
            }}
          >
            <Charts chartTitle={`${metric.name} (${metric.unit})`} dataset={chartData} labels={labels} data={[]} loading={loading} />
          </Box>
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
  const [tabValue, setTabValue] = useState(0);
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

  const handleTabChange = (newValue: number) => {
    setTabValue(newValue);
  };

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  if (error) {
    return <Alert severity='error'>{error}</Alert>;
  }

  if (!loading && !data) {
    return <Alert severity='info'>No performance insights data available</Alert>;
  }

  const isGCP = data?.provider === 'gcp';

  if (!loading && data && !data.performance_enabled) {
    const message = isGCP
      ? 'Query Insights is not enabled for this database. Enable it in the GCP Console to view performance data.'
      : 'Performance Insights is not enabled for this database. Enable it in the AWS Console to view performance data.';
    return <Alert severity='warning'>{message}</Alert>;
  }

  const tabs = [
    ...(!isGCP ? [{ label: 'Database Load', key: 'load' }] : []),
    { label: 'Top Queries', key: 'queries' },
    { label: 'Wait Events', key: 'events' },
    { label: 'Resource Metrics', key: 'metrics' },
  ];

  const activeTabKey = tabs[tabValue]?.key || tabs[0]?.key;

  const dateTimeRangeProps = {
    enabled: true,
    onChange: handleDateRangeChange,
    passedSelectedDateTime: {
      startTime: selectedDateRange.startDate,
      endTime: selectedDateRange.endDate,
      shortcutClickTime: 0,
    },
    // Backend enforces a 7-day max for performance insights queries
    shortCuts: [
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
    ],
  };

  const loadingSpinner = (
    <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
      <CircularProgress />
    </Box>
  );

  const optionsToDisplay = {
    tabOptions: tabs.map((tab, index) => {
      return {
        text: tab.label,
        value: index,
      };
    }),
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
        width='500px'
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

      <Box sx={{ borderBottom: 1, borderColor: 'divider', mb: 2 }}>
        <CustomTabs value={tabValue} onChange={handleTabChange} options={optionsToDisplay} behavior='filter' />
      </Box>

      {activeTabKey === 'load' && (
        <BoxLayout2
          id='db-load'
          heading='Database Load (Average Active Sessions)'
          sharingOptions={{
            sharing: { enabled: false, onClick: null },
            download: { enabled: true, onClick: () => ({ tableId: 'db-load' }) },
          }}
          dateTimeRange={dateTimeRangeProps}
        >
          {loading ? loadingSpinner : data && <LoadMetricsChart data={data} loading={loading} />}
        </BoxLayout2>
      )}

      {activeTabKey === 'queries' && (
        <BoxLayout2
          id='top-queries'
          heading={isGCP ? 'Top SQL Queries by Total Time' : 'Top SQL Queries by DB Load'}
          sharingOptions={{
            sharing: { enabled: false, onClick: null },
            download: { enabled: true, onClick: () => ({ tableId: 'topQueriesTable' }) },
          }}
          dateTimeRange={dateTimeRangeProps}
        >
          {loading
            ? loadingSpinner
            : data && (
                <TopQueriesTable
                  queries={data.top_queries}
                  provider={data.provider}
                  databaseIdentifier={databaseIdentifier}
                  region={region}
                  getMenuItem={getMenuItem}
                  onMenuClick={onMenuClick}
                  onAnalyzeQuery={handleAnalyzeQuery}
                />
              )}
        </BoxLayout2>
      )}

      {activeTabKey === 'events' && (
        <BoxLayout2
          id='wait-events'
          heading='Wait Events'
          sharingOptions={{
            sharing: { enabled: false, onClick: null },
            download: { enabled: true, onClick: () => ({ tableId: 'waitEventsTable' }) },
          }}
          dateTimeRange={dateTimeRangeProps}
        >
          {loading ? loadingSpinner : data && <WaitEventsTable events={data.wait_events} provider={data.provider} />}
        </BoxLayout2>
      )}

      {activeTabKey === 'metrics' && (
        <BoxLayout2
          id='resource-metrics'
          heading='Resource Metrics (CPU, Memory, I/O)'
          sharingOptions={{
            sharing: { enabled: false, onClick: null },
            download: { enabled: true, onClick: () => ({ tableId: 'resource-metrics' }) },
          }}
          dateTimeRange={dateTimeRangeProps}
        >
          {loading ? loadingSpinner : data && <ResourceMetricsChart data={data} loading={loading} />}
        </BoxLayout2>
      )}
    </Box>
  );
};

export default PerformanceInsights;
