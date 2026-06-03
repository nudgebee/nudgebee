import React, { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { Text } from '@components1/common';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import { formatDateTime } from '@lib/datetime';
import { colors } from 'src/utils/colors';
import CustomTable from '@common-new/tables/CustomTable2';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';

export const LOG_LEVEL_COLORS: any = {
  error: colors.high,
  info: colors.toDo,
  debug: colors.debug,
};

export function LogDate({ timestamp, log }: Readonly<{ timestamp: number; log: string }>) {
  let level = 'debug';
  const message = log?.toLowerCase() ?? '';
  if (message.includes('error') || message.includes('exception') || message.includes('critical')) {
    level = 'error';
  } else if (message.includes('info')) {
    level = 'info';
  }

  return (
    <Box display={'flex'} gap={2}>
      <div style={{ width: 2, backgroundColor: LOG_LEVEL_COLORS[level], paddingRight: 4 }} />
      <Text value={timestamp ? formatDateTime(new Date(timestamp).getTime()) : '--'} />{' '}
    </Box>
  );
}

interface SignozDatadogLogsProps {
  logData?: any[];
}

const signozHeaders = ['Date', { name: 'Message', width: '80%' }];
const k8sLogs = 'k8sLogs';

const SignozDatadogLogs: React.FC<SignozDatadogLogsProps> = ({ logData = [] }) => {
  const [queryResults, setQueryResults] = useState<any[]>([]);

  const formatSignozResults = (allResults: any[]) => {
    const result: any[] = allResults.map((res: any) => {
      return [
        {
          component: <LogDate timestamp={res.timestamp} log={res.severity} />,
          drilldownQuery: {
            data: res,
          },
        },
        {
          component: <Text value={res.message} showAutoEllipsis copyableTooltip={true} />,
        },
      ];
    });

    setQueryResults(result);
  };

  useEffect(() => {
    if (logData?.length) {
      formatSignozResults(logData);
    }
  }, [logData]);

  return (
    <div>
      <ListingLayout id='query-logs'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: k8sLogs })} />} />
        <ListingLayout.Body>
          <CustomTable
            id={`${k8sLogs}`}
            totalRows={queryResults.length}
            tableData={queryResults}
            loading={false}
            headers={signozHeaders}
            rowsPerPage={queryResults.length}
            onPageChange={undefined}
            onSortChange={undefined}
            stickyColumnIndex='3'
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'Log Details',
                  value: 0,
                  key: 'signoz-log',
                  componentFn: (_: any, query: any, _row: any) => {
                    const rawResourceData = query?.data?.labels;
                    if (rawResourceData && Object.keys(rawResourceData).length > 0) {
                      const { headers, convertedJson2 } = getTableData4([rawResourceData]);

                      return (
                        <CustomTable
                          headers={headers}
                          tableData={convertedJson2}
                          rowsPerPage={convertedJson2.length}
                          totalRows={convertedJson2.length}
                        />
                      );
                    }
                    return <div>No data available</div>;
                  },
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default SignozDatadogLogs;
