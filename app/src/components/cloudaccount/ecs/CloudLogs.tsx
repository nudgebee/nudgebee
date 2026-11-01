import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import HelpOutlineIcon from '@mui/icons-material/HelpOutline';
import { ListingLayout } from '@ui/ListingLayout';
import { Input as DsInput } from '@ui/Input';
import { Button as DsButton } from '@ui/Button';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import observability from '@api1/observability';
import { unwrapGraphQL } from '@lib/HttpService';
import { ds } from '@utils/colors';

interface CloudLogsProps {
  accountId: string;
  resourceId: string;
  region: string;
}

interface LogEntry {
  timestamp: string;
  message: string;
  severity: string;
  labels: Record<string, any>;
}

const TABLE_ID = 'ecsTaskLogsTable';

// ECS tasks emit to CloudWatch via the awslogs driver; the backend resolves the
// awslogs-group from the task definition (amazonEcs.GetLogGroupName), so the UI
// only needs a CloudWatch Insights query.
const DEFAULT_QUERY = 'fields @timestamp, @message | sort @timestamp desc';
const QUERY_HELP = `AWS CloudWatch Insights syntax:
  fields @timestamp, @message | sort @timestamp desc
  filter @message like /error/
  stats count(*) by bin(5m)`;

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

const CloudLogs: React.FC<CloudLogsProps> = ({ accountId, resourceId, region }) => {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<LogEntry[]>([]);
  const [queryString, setQueryString] = useState(DEFAULT_QUERY);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: Date.now() - 3600000,
    endDate: Date.now(),
  });

  const fetchData = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);

      try {
        const response = await observability.fetchLogs(
          {
            account_id: accountId,
            log_provider: 'aws_cloudwatch',
            log_provider_source: 'user',
            query: queryString,
            start_time: selectedDateRange.startDate,
            end_time: selectedDateRange.endDate,
            limit: 200,
            request: {
              region: region,
              service_name: 'ecs',
              resource_id: resourceId,
            },
          },
          signal
        );

        // A failed query comes back as HTTP 200 with a top-level `errors` array;
        // unwrapGraphQL throws on it so the reason surfaces instead of an empty list.
        const result = unwrapGraphQL(response, 'Failed to fetch logs');
        // A non-GraphQL failure (502/504, network) leaves result undefined with no
        // errors[] to throw on — surface it instead of an empty list.
        if (!result) {
          throw new Error('Failed to fetch logs');
        }
        const logs = result?.logs_list?.logs || [];
        setData(logs);
      } catch (err: any) {
        // Ignore an aborted request (deps changed / unmount) so a stale response
        // can't overwrite the current logs or surface a spurious error.
        if (err.name === 'AbortError' || signal?.aborted) return;
        setError(err.message || 'Failed to fetch logs');
      } finally {
        if (!signal?.aborted) {
          setLoading(false);
        }
      }
    },
    [accountId, resourceId, region, queryString, selectedDateRange]
  );

  useEffect(() => {
    const controller = new AbortController();
    fetchData(controller.signal);
    return () => {
      controller.abort();
    };
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
              fontFamily: ds.font.mono,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              m: 0,
              maxHeight: ds.space.mul(0, 100),
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
    <ListingLayout id='ecs-task-logs'>
      <ListingLayout.Toolbar
        title='Task Logs'
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
            <DsInput id='ecs-task-logs-query' size='sm' value={queryString} onChange={setQueryString} placeholder='Query' />
          </Box>
          <DsButton
            tone='ghost'
            size='md'
            composition='icon-only'
            icon={<HelpOutlineIcon fontSize='small' />}
            aria-label='Query syntax help'
            tooltip={<Typography sx={{ whiteSpace: 'pre-line', fontSize: ds.text.caption, fontFamily: ds.font.mono }}>{QUERY_HELP}</Typography>}
            tooltipPlacement='bottom'
            onClick={() => undefined}
          />
          <DsButton id='ecs-task-logs-run' tone='primary' size='md' onClick={() => fetchData()} loading={loading} disabled={loading}>
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
            description='The selected time range has no matching logs. The task may not have the awslogs driver configured, or no logs match this query.'
          />
        ) : (
          <CustomTable
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
