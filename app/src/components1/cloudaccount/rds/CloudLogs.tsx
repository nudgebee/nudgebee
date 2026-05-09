import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography, CircularProgress, Alert, TextField } from '@mui/material';
import HelpOutlineIcon from '@mui/icons-material/HelpOutline';
import BoxLayout2 from '@common/BoxLayout2';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import CustomTooltip from '@components1/common/CustomTooltip';
import CustomButton from '@components1/common/NewCustomButton';
import Datetime from '@components1/common/format/Datetime';
import observability from '@api1/observability';

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
  // Azure resource types start with "microsoft."
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
    startDate: Date.now() - 3600000, // last 1 hour
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

      const logs = response?.data?.data?.logs_query || [];
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
    const row = [
      { text: <Datetime value={log.timestamp} /> },
      {
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
            {getLogDisplayText(log)}
          </Typography>
        ),
      },
    ];
    return row;
  });

  return (
    <Box>
      <BoxLayout2
        id='cloud-logs'
        heading='Database Logs'
        sharingOptions={{
          sharing: { enabled: false, onClick: null },
          download: { enabled: true, onClick: () => ({ tableId: 'cloudLogsTable' }) },
        }}
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
      >
        <Box sx={{ display: 'flex', gap: 2, alignItems: 'center', px: 2, pt: 2, pb: 1.5 }}>
          <TextField
            size='small'
            label='Query'
            value={queryString}
            onChange={(e) => setQueryString(e.target.value)}
            sx={{ flex: 1, '& .MuiInputBase-input': { fontSize: 12, fontFamily: 'monospace' } }}
          />
          <CustomTooltip
            title={<Typography sx={{ whiteSpace: 'pre-line', fontSize: 11, fontFamily: 'monospace' }}>{cloudConfig.queryHelp}</Typography>}
            arrow
            placement='bottom-start'
          >
            <Box sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer' }}>
              <HelpOutlineIcon fontSize='small' sx={{ color: '#9E9E9E' }} />
            </Box>
          </CustomTooltip>
          <CustomButton text='Run' size='Small' onClick={fetchData} disabled={loading} />
        </Box>

        {error && (
          <Alert severity='error' sx={{ mx: 2, mb: 2 }}>
            {error}
          </Alert>
        )}

        <Box sx={{ px: 2, pb: 2 }}>
          {loading ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
              <CircularProgress />
            </Box>
          ) : logTableData.length > 0 ? (
            <CustomTable2
              id='cloudLogsTable'
              headers={[
                { name: 'Timestamp', width: '120px' },
                { name: 'Message', width: '85%' },
              ]}
              tableData={logTableData}
              rowsPerPage={logTableData.length}
            />
          ) : (
            <Alert severity='info' sx={{ py: 1.5 }}>
              No log entries found for the selected time range. The database may not have logging enabled, or no logs match the query.
            </Alert>
          )}
        </Box>
      </BoxLayout2>
    </Box>
  );
};

export default CloudLogs;
