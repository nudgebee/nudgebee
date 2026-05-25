import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { Box } from '@mui/material';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { convertNumberToTimestamp } from 'src/utils/common';
import LineChart from '@common/charts/LineCharts';
import apiKubernetes1 from '@api1/kubernetes1';
import { v4 as uuidv4 } from 'uuid';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import { snackbar } from '@components1/common/snackbarService';
import { withErrorBoundary } from '@common/ErrorBoundary';

const ShowPrometheusLineChart = ({
  accountId,
  query = '',
  instant = false,
  selectedDateRange = {
    startDate: new Date(new Date().getTime() - 60 * 60 * 1000).getTime(),
    endDate: new Date().getTime(),
  },
}) => {
  const [loading, setLoading] = useState(false);
  const [chartData, setChartData] = useState([]);

  // Memoize parsed queries to avoid recalculation
  const parsedQueries = useMemo(() => {
    if (!query) {
      return [];
    }

    return query
      .replace(/^;+|;+$/g, '')
      .split(';')
      .filter((q) => q.trim()) // Remove empty queries
      .map((q) => ({
        key: uuidv4(),
        query: q.trim(),
      }));
  }, [query]);

  // Memoize query lookup function
  const getQueryByKey = useCallback(
    (key) => {
      const entry = parsedQueries.find((item) => item.key === key);
      return entry ? entry.query : null;
    },
    [parsedQueries]
  );

  // Extract data processing logic into separate functions
  const processSeriesData = useCallback((evidenceData, queryKey) => {
    const data = evidenceData[queryKey];

    if (!data?.series_list_result || data.series_list_result.length === 0) {
      return null;
    }

    const hasTimestamps = data.series_list_result[0].timestamps.length > 0;

    if (hasTimestamps) {
      // Process time series data
      const labels = [...new Set(data.series_list_result.flatMap((e) => e.timestamps))].sort();

      const chartDataDataset = data.series_list_result.map((item) => {
        // O(1) lookup via Map instead of O(n) indexOf per label
        const tsIndex = new Map();
        item.timestamps.forEach((ts, i) => tsIndex.set(ts, i));
        const values = labels.map((label) => {
          const index = tsIndex.get(label);
          return index !== undefined ? parseFloat(item.values[index]) || 0 : 0;
        });

        return {
          label: Object.entries(item.metric)
            .map(([key, value]) => `${key}=${value}`)
            .join('\n'),
          data: values,
        };
      });

      return {
        labels: labels.map((e) => convertNumberToTimestamp(e * 1000)),
        data: chartDataDataset,
      };
    }
    // Process metric data without timestamps
    return {
      labels: data.series_list_result[0].timestamps.map((e) => convertNumberToTimestamp(e * 1000)),
      data: [
        {
          label: 'Value',
          data: data.series_list_result[0].values?.map((e) => parseFloat(e) || 0) || [],
        },
      ],
    };
  }, []);

  const processInstantData = useCallback((evidenceData, queryKey) => {
    const dataItems = evidenceData[queryKey];

    if (Array.isArray(dataItems) && dataItems[0]?.value && Array.isArray(dataItems[0].value)) {
      return {
        labels: dataItems.map((item) => convertNumberToTimestamp(item.value[0] * 1000)),
        data: [
          {
            label: 'Total',
            data: dataItems.map((item) => parseFloat(item.value[1])),
          },
        ],
      };
    }

    return {
      labels: [],
      data: [],
      helperText: dataItems?.string_result || '',
    };
  }, []);

  const processChartData = useCallback(
    (response) => {
      try {
        const evidence = response?.data?.data?.metrics_query?.results;
        const evidenceData = evidence.reduce((res, data) => {
          return {
            ...res,
            [data.query_key]: {
              series_list_result: data.payload,
            },
          };
        }, {});

        return Object.keys(evidenceData).map((queryKey) => {
          const query = getQueryByKey(queryKey);

          // Try processing as series data first
          const seriesResult = processSeriesData(evidenceData, queryKey);
          if (seriesResult) {
            return { query, data: seriesResult };
          }

          // Fall back to instant data processing
          if (evidenceData[queryKey]) {
            const instantResult = processInstantData(evidenceData, queryKey);
            return { query, data: instantResult };
          }

          // Default empty result
          return {
            query,
            data: { labels: [], data: [] },
            helperText: '',
          };
        });
      } catch (error) {
        console.error('Error processing chart data:', error);
        throw new Error('Failed to process chart data');
      }
    },
    [getQueryByKey, processSeriesData, processInstantData]
  );

  const handleSubmit = useCallback(async () => {
    if (!query?.trim() || !accountId) {
      return;
    }

    setLoading(true);
    setChartData([]);

    try {
      const convertFormattedQuery = (promqls) => {
        return promqls.reduce((res, val) => {
          return {
            ...res,
            [val.key]: val.query,
          };
        }, {});
      };
      const requestBody = {
        account_id: accountId,
        queries: convertFormattedQuery(parsedQueries),
        start_time: selectedDateRange.startDate,
        end_time: selectedDateRange.endDate,
        instant: instant,
      };
      const response = await apiKubernetes1.consumePrometheusQueries(requestBody);
      if (response?.data?.data?.metrics_query?.results) {
        const processedData = processChartData(response);
        setChartData(processedData);
      } else {
        snackbar.error('Failed execute Query');
      }
    } catch (error) {
      snackbar.error('Failed to fetch the Data');
      console.error('Query execution error:', error);
    } finally {
      setLoading(false);
    }
  }, [query, accountId]);

  useEffect(() => {
    handleSubmit();
  }, [query]);

  // Memoize sharing options to prevent unnecessary re-renders
  const sharingOptions = useMemo(
    () => ({
      sharing: {
        enabled: false,
        onClick: null,
      },
      download: {
        enabled: true,
        onClick: () => ({ canvasId: 'k8sPromChart', tableId: '' }),
      },
    }),
    []
  );

  const dateTimeRange = useMemo(
    () => ({
      enabled: false,
    }),
    []
  );

  return (
    <div>
      <BoxLayout2 id='prometheus-line-chart' heading='' marginBottom='10px' dateTimeRange={dateTimeRange} sharingOptions={sharingOptions}>
        <ShimmerLoading isLoading={loading} height='400px' width='98%'>
          {chartData.map((cd, index) => (
            <Box sx={{ mb: 4 }} key={`chart-${index}`}>
              <LineChart
                id='k8sPromChart'
                dataset={cd.data.data}
                labels={cd.data.labels}
                legendOptions={{
                  renderer: 'html',
                }}
                interactionOptions={{
                  enabled: false,
                }}
              />
              {cd.helperText && <Box sx={{ mt: 1, fontSize: '0.875rem', color: 'text.secondary' }}>{cd.helperText}</Box>}
            </Box>
          ))}
        </ShimmerLoading>
      </BoxLayout2>
    </div>
  );
};

export default withErrorBoundary(ShowPrometheusLineChart);
