import React, { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import apiKubernetes1 from '@api1/kubernetes1';
import { getSpecificTime } from '@lib/datetime';
import { BoxLayout2, Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import { formatNumber } from '@lib/formatter';
import CustomLink from '@components1/common/CustomLink';
import ShowPrometheusLineChart from '@components1/common/charts/ShowPrometheusLineChart';

const HttpLatencyTable = ({ accountId, data }) => {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [rowMap, setRowMap] = useState(new Map());
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getSpecificTime(60),
    endDate: new Date().getTime(),
  });

  // Helper function to convert rowMap to table rows
  const convertRowMapToTableRows = (currentRowMap) => {
    return Array.from(currentRowMap.values()).map((row) => [
      { text: <Text value={row.method} />, drilldownQuery: { ...row, accountId } },
      {
        component: (
          <CustomLink
            href={`/kubernetes/details/${accountId}?destinationWorkload=${row.destinationWorkloadName}&destinationNamespace=${row.destinationWorkloadNamespace}&resource=${row.path}#monitoring/traces`}
            target={'_blank'}
          >
            <Text showAutoEllipsis value={row.path} />
          </CustomLink>
        ),
      },
      {
        component: (
          <>
            <Box display='flex'>
              <Text showAutoEllipsis value={row.destinationWorkloadName} />
            </Box>
            <Text showAutoEllipsis value={`ns: ${row.destinationWorkloadNamespace}`} secondaryText />
          </>
        ),
      },
      { text: row.count },
      { text: row.total ? formatNumber(row.total) : '-' },
      { text: row.p99 ? formatNumber(row.p99) : '-' },
      { text: row.p95 ? formatNumber(row.p95) : '-' },
      { text: row.errorRate ? formatNumber(row.errorRate) : '-' },
    ]);
  };

  useEffect(() => {
    const fetchData = async () => {
      try {
        // Step 1: Fetch initial data (method & path counts)

        const requestBody1 = {
          account_id: accountId,
          instant: true,
          startDate: selectedDateRange?.startDate,
          endDate: selectedDateRange?.endDate,
          metrics: ['http_throughput'],
          workloadType: 'workload',
          regex: true,
        };

        if (data.workloadName && data.namespaceName) {
          requestBody1.namespace_name = data.namespaceName;
          requestBody1.workload_name = data.workloadName;
        }

        const response = await apiKubernetes1.utilisationApi(requestBody1);
        const vectorList = response?.find((data) => data.query_key === 'http_throughput')?.payload || [];

        // Create initial row map with method+path as key
        const initialRowMap = new Map();

        vectorList.forEach((item) => {
          const method = item.metric.method || '-';
          const path = item.metric.path || '-';
          const count = item.values[0] || '0';
          const destinationWorkloadName = item.metric.destination_workload_name || data.workloadName || '';
          const destinationWorkloadNamespace = item.metric.destination_workload_namespace || data.namespaceName || '';

          initialRowMap.set(`${method}_${path}`, {
            method,
            path,
            count,
            p95: null,
            p99: null,
            total: null,
            errorRate: null,
            destinationWorkloadName,
            destinationWorkloadNamespace,
          });
        });

        // Update state with initial data - this will render the table immediately
        setRowMap(initialRowMap);
        setLoading(false);

        // Step 2: Only fetch additional metrics if we have initial data
        if (initialRowMap.size === 0) {
          return; // No data to enhance, exit early
        }
        // Step 3: Fetch additional metrics in parallel

        const requestBody2 = {
          account_id: accountId,
          instant: true,
          startDate: selectedDateRange?.startDate,
          endDate: selectedDateRange?.endDate,
          metrics: [],
          workloadType: 'workload',
          regex: true,
        };

        if (data.workloadName && data.namespaceName) {
          requestBody2.namespace_name = data.namespaceName;
          requestBody2.workload_name = data.workloadName;
        }

        const queries = [
          { key: 'p95', value: 'http_latency_p95' },
          { key: 'p99', value: 'http_latency_p99' },
          { key: 'total', value: 'http_latency_sum' },
          { key: 'errorRate', value: 'http_error_rate' },
        ];
        const queryKeyMap = {
          http_latency_p95: 'p95',
          http_latency_p99: 'p99',
          http_latency_sum: 'total',
          http_error_rate: 'errorRate',
        };

        const results = await apiKubernetes1.utilisationApi({
          ...requestBody2,
          metrics: Object.values(queries).map((e) => e.value),
        });

        // Update rowMap with each result
        setRowMap((currentRowMap) => {
          const updatedRowMap = new Map(currentRowMap);

          for (const result of results) {
            const key = queryKeyMap[result.query_key];
            const vectors = result?.payload || [];
            vectors.forEach((item) => {
              const method = item.metric.method || '-';
              const path = item.metric.path || '-';
              const value = item.values[0];

              const rowKey = `${method}_${path}`;
              if (updatedRowMap.has(rowKey)) {
                updatedRowMap.get(rowKey)[key] = value;
              }
            });
          }

          return updatedRowMap;
        });
      } catch (error) {
        console.error('Error fetching initial data', error);
        setLoading(false);
      }
    };

    fetchData();
  }, [accountId, data.workloadName, data.namespaceName, selectedDateRange]);

  // Update rows whenever rowMap changes
  useEffect(() => {
    if (rowMap.size > 0) {
      setRows(convertRowMapToTableRows(rowMap));
    }
  }, [rowMap]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <Box>
      <BoxLayout2
        heading='HTTP Endpoints'
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: '',
              };
            },
          },
        }}
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
          },
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
          ],
          showAbsoluteRange: false,
        }}
      >
        <CustomTable
          loading={loading}
          headers={[
            'Method',
            'Path',
            { name: 'App Name', width: '25%' },
            { name: 'Count', width: '10%' },
            { name: 'Total Time', width: '10%' },
            { name: 'p99', width: '10%' },
            { name: 'p95', width: '10%' },
            'Error Rate%',
          ]}
          tableData={rows}
          onPageChange={undefined}
          rowsPerPage={rows.length}
          totalRows={rows.length}
          expandable={{
            tabs: [
              {
                text: 'Failure Trend',
                value: 0,
                key: 'failure-trend-state',
                componentFn: FailureTrend,
              },
            ],
          }}
        />
      </BoxLayout2>
    </Box>
  );
};

const FailureTrend = (option, drilldownQuery, _row) => {
  return (
    <ShowPrometheusLineChart
      accountId={drilldownQuery.accountId}
      query={`sort_desc(sum by(method, path)(increase(container_http_requests_total{ __CLUSTER__ path="${drilldownQuery.path}", destination_workload_name="${drilldownQuery.destinationWorkloadName}", destination_workload_namespace="${drilldownQuery.destinationWorkloadNamespace}", status=~"5..|4.."}[1h])))`}
    />
  );
};

HttpLatencyTable.propTypes = {
  accountId: PropTypes.string.isRequired,
  data: PropTypes.object.isRequired,
};

export default HttpLatencyTable;
