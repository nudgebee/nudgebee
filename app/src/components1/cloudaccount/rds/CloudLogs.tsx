import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import HelpOutlineIcon from '@mui/icons-material/HelpOutline';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Input as DsInput } from '@components1/ds/Input';
import { Button as DsButton } from '@components1/ds/Button';
import { Banner } from '@components1/ds/Banner';
import { EmptyState } from '@components1/ds/EmptyState';
import CustomTable2 from '@common-new/tables/CustomTable2';
import Datetime from '@common-new/format/Datetime';
import DownloadButton from '@common-new/DownloadButton';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import observability from '@api1/observability';
import { ds } from '@utils/colors';

interface CloudLogsProps {
  accountId: string;
  resourceId: string;
  region: string;
  serviceName?: string;
}

interface LogEntry {
  timestamp: string;
  message: string;
  severity: string;
  labels: Record<string, any>;
}

const TABLE_ID = 'cloudLogsTable';

const AWS_DEFAULT_QUERY = 'fields @timestamp, @message | sort @timestamp desc';
const GCP_DEFAULT_QUERY = '';
const AZURE_DEFAULT_QUERY = 'AzureDiagnostics | project TimeGenerated, Message, Category, OperationName | order by TimeGenerated desc';

const AWS_QUERY_HELP = `AWS CloudWatch Insights syntax:
  fields @timestamp, @message | sort @timestamp desc
  filter @message like /error/
  stats count(*) by bin(5m)`;

const GCP_QUERY_HELP = `GCP Cloud Logging filter syntax:
  severity="ERROR"
  textPayload:"connection"
  jsonPayload.message="hello"`;

const AZURE_QUERY_HELP = `Azure Log Analytics (KQL) syntax:
  AzureDiagnostics | order by TimeGenerated desc
  AzureDiagnostics | where Category == "SQLInsights"
  AzureDiagnostics | where Message contains "error"
  AzureDiagnostics | summarize count() by Category, bin(TimeGenerated, 5m)`;

function getCloudProvider(serviceName?: string): { provider: string; service: string; defaultQuery: string; queryHelp: string } {
  if (!serviceName) {
    return { provider: 'aws_cloudwatch', service: 'rds', defaultQuery: AWS_DEFAULT_QUERY, queryHelp: AWS_QUERY_HELP };
  }
  const lower = serviceName.toLowerCase();
  if (lower.startsWith('microsoft.')) {
    return { provider: 'aws_cloudwatch', service: lower, defaultQuery: AZURE_DEFAULT_QUERY, queryHelp: AZURE_QUERY_HELP };
  }
  // provider 'aws_cloudwatch' routes to cloudLogs backend which dispatches by account type (AWS/GCP)
  if (lower.includes('cloud sql')) {
    return { provider: 'aws_cloudwatch', service: 'cloud sql', defaultQuery: GCP_DEFAULT_QUERY, queryHelp: GCP_QUERY_HELP };
  }
  return { provider: 'aws_cloudwatch', service: 'rds', defaultQuery: AWS_DEFAULT_QUERY, queryHelp: AWS_QUERY_HELP };
}

function getLogDisplayText(log: LogEntry): string {
  if (log.message) {
    return log.message;
  }
  if (log.labels && Object.keys(log.labels).length > 0) {
    return Object.entries(log.labels)
      .map(([k, v]) => `${k}=${v}`)
      .join(', ');
  }
  return '';
}

const CloudLogs: React.FC<CloudLogsProps> = ({ accountId, resourceId, region, serviceName }) => {
  const cloudConfig = getCloudProvider(serviceName);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<LogEntry[]>([]);
  const [queryString, setQueryString] = useState(cloudConfig.defaultQuery);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: Date.now() - 3600000,
    endDate: Date.now(),
  });

  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const response = await observability.fetchLogs({
        account_id: accountId,
        log_provider: cloudConfig.provider,
        log_provider_source: 'user',
        query: queryString,
        start_time: selectedDateRange.startDate,
        end_time: selectedDateRange.endDate,
        limit: 200,
        request: {
          region: region,
          service_name: cloudConfig.service,
          resource_id: resourceId,
        },
      });

      const logs = response?.data?.data?.logs_list || [];
      setData(logs);
    } catch (err: any) {
      setError(err.message || 'Failed to fetch logs');
    } finally {
      setLoading(false);
    }
  }, [accountId, resourceId, region, queryString, selectedDateRange]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const logTableData = data.map((log) => {
    return [
      { text: <Datetime value={log.timestamp} /> },
      {
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
            {getLogDisplayText(log)}
          </Typography>
        ),
      },
    ];
  });

  const hasRows = logTableData.length > 0;

  return (
    <ListingLayout id='cloud-logs'>
      <ListingLayout.Toolbar
        title='Database Logs'
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
                shortcutClickTime: 0,
              }}
              onChange={(result: any) => {
                const val = result?.selection ?? result;
                if (val) handleDateRangeChange(val);
              }}
            />
            <DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />
          </>
        }
      />

      <ListingLayout.Body padding={`${ds.space[3]} ${ds.space[5]}`}>
        <Box sx={{ display: 'flex', gap: ds.space[2], alignItems: 'center', mb: ds.space[3] }}>
          <Box sx={{ flex: 1 }}>
            <DsInput id='cloud-logs-query' size='sm' value={queryString} onChange={setQueryString} placeholder='Query' />
          </Box>
          <DsButton
            tone='ghost'
            size='md'
            composition='icon-only'
            icon={<HelpOutlineIcon fontSize='small' />}
            aria-label='Query syntax help'
            tooltip={
              <Typography sx={{ whiteSpace: 'pre-line', fontSize: ds.text.caption, fontFamily: 'monospace' }}>{cloudConfig.queryHelp}</Typography>
            }
            tooltipPlacement='bottom'
            onClick={() => undefined}
          />
          <DsButton id='cloud-logs-run' tone='primary' size='md' onClick={fetchData} loading={loading} disabled={loading}>
            Run
          </DsButton>
        </Box>

        {error && (
          <Box sx={{ mb: ds.space[3] }}>
            <Banner tone='critical' surface='section' message={error} />
          </Box>
        )}

        {!error && !loading && !hasRows ? (
          <EmptyState
            size='inline'
            illustration='no-results'
            title='No log entries'
            description='The selected time range has no matching logs. The resource may not have logging enabled, or no logs match this query.'
          />
        ) : (
          <CustomTable2
            id={TABLE_ID}
            headers={[
              { name: 'Timestamp', width: '120px' },
              { name: 'Message', width: '85%' },
            ]}
            tableData={logTableData}
            rowsPerPage={hasRows ? logTableData.length : 5}
            loading={loading}
          />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default CloudLogs;
