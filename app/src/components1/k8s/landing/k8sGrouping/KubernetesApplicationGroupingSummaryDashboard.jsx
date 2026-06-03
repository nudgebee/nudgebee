import PropTypes from 'prop-types';
import { LineChart } from '@components1/common';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import { Box } from '@mui/material';
import React, { useEffect, useState } from 'react';
import { convertNumberToTimestamp } from 'src/utils/common';
import { getSpecificTime } from '@lib/datetime';
import { Skeleton } from '@components1/ds/Skeleton';
import FilterDropdown from '@components1/ds/FilterDropdown';
import TextWithBorder from '@components1/common/TextWithBorder';
import LazyLoadComponent from '@components1/common/LazyLoadComponent';
import apiKubernetes1 from '@api1/kubernetes1';
import { ds } from '@utils/colors';

const KubernetesApplicationGroupingSummaryDashboard = ({ accountId, applications }) => {
  const [loading, setLoading] = useState({
    request: false,
    status: false,
    latency: false,
    services: false,
  });
  const [chartData, setChartData] = useState({
    request: {
      data: [],
      labels: [],
    },
    status: {
      data: [],
      labels: [],
    },
    latency: {
      data: [],
      labels: [],
    },
    services: {
      data: [],
      labels: [],
    },
  });
  // const [errors, setErrors] = useState({
  //   request: null,
  //   status: null,
  //   latency: null,
  //   services: null,
  // });
  const [selectedServiceMetric, setSelectedServiceMetric] = useState('http');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getSpecificTime(60),
    endDate: new Date().getTime(),
  });

  // Helper function to update individual chart data
  const updateChartData = (key, data) => {
    setChartData((prev) => ({
      ...prev,
      [key]: data,
    }));

    setLoading((prev) => ({
      ...prev,
      [key]: false,
    }));
  };

  // Helper function to handle errors
  const handleError = (key, _error) => {
    // setErrors((prev) => ({
    //   ...prev,
    //   [key]: error,
    // }));

    setLoading((prev) => ({
      ...prev,
      [key]: false,
    }));
  };

  useEffect(() => {
    if (accountId && applications) {
      // Set all to loading initially
      setLoading({
        request: true,
        status: true,
        latency: true,
        services: true,
      });

      // Clear previous errors
      // setErrors({
      //   request: null,
      //   status: null,
      //   latency: null,
      //   services: null,
      // });

      const fetchData = async () => {
        const containerIdRegex = applications.map((a) => `.*/${a.namespace_name}/${a.workload_name}.*`).join('|');

        // Helper function to process individual latency response
        const processLatencyResponse = (response, labelName) => {
          const seriesList = response?.[0]?.payload || [];
          let seriesData = {
            labels: [],
            data: [],
          };
          if (seriesList.length) {
            const labels = [...new Set(seriesList?.flatMap((e) => e.timestamps) ?? [])];
            labels.sort();
            const chartDataDataset = [];
            seriesList.map((item) => {
              // O(1) lookup via Map instead of O(n) indexOf per label
              const tsIndex = new Map();
              item.timestamps.forEach((ts, i) => tsIndex.set(ts, i));
              const values = [];
              labels.forEach((label) => {
                const index = tsIndex.get(label);
                if (index !== undefined) {
                  values.push(parseFloat(item.values[index]));
                } else {
                  values.push(0);
                }
              });

              chartDataDataset.push({
                label: labelName, // Use the provided labelName directly
                data: values,
              });
            });
            seriesData = {
              labels: labels.map((e) => convertNumberToTimestamp(e * 1000)),
              data: chartDataDataset,
            };
          }
          return seriesData;
        };

        // Helper function to combine all latency data
        const combineLatencyData = (latencyResponses) => {
          const combinedLabels = [];
          const combinedDatasets = [];

          // Get all unique timestamps across all latency metrics
          latencyResponses.forEach((seriesData) => {
            seriesData.labels.forEach((label) => {
              if (!combinedLabels.includes(label)) {
                combinedLabels.push(label);
              }
            });
          });

          // Sort labels chronologically
          combinedLabels.sort();

          // Create datasets for each latency metric
          latencyResponses.forEach((seriesData) => {
            seriesData.data.forEach((dataset) => {
              // Align data with combined labels
              const alignedData = combinedLabels.map((label) => {
                const originalIndex = seriesData.labels.indexOf(label);
                return originalIndex >= 0 ? dataset.data[originalIndex] : 0;
              });

              combinedDatasets.push({
                ...dataset,
                data: alignedData,
              });
            });
          });

          const combinedLatencyData = {
            labels: combinedLabels,
            data: combinedDatasets,
          };

          updateChartData('latency', combinedLatencyData);
        };

        // Fetch latency data separately
        const fetchLatencyData = async () => {
          try {
            const latencyApiCalls = [
              {
                labelName: 'p90',
                promise: apiKubernetes1.utilisationApi({
                  accountId: accountId,
                  metrics: ['container_http_latency_p90'],
                  startDate: selectedDateRange.startDate,
                  endDate: selectedDateRange.endDate,
                  containerName: containerIdRegex,
                  kind: 'workload',
                }),
              },
              {
                labelName: 'p99',
                promise: apiKubernetes1.utilisationApi({
                  accountId: accountId,
                  metrics: ['container_http_latency_p99'],
                  startDate: selectedDateRange.startDate,
                  endDate: selectedDateRange.endDate,
                  containerName: containerIdRegex,
                  kind: 'workload',
                }),
              },
              {
                labelName: 'p95',
                promise: apiKubernetes1.utilisationApi({
                  accountId: accountId,
                  metrics: ['container_http_latency_p95'],
                  startDate: selectedDateRange.startDate,
                  endDate: selectedDateRange.endDate,
                  containerName: containerIdRegex,
                  kind: 'workload',
                }),
              },
              {
                labelName: 'p50',
                promise: apiKubernetes1.utilisationApi({
                  accountId: accountId,
                  metrics: ['container_http_latency_p50'],
                  startDate: selectedDateRange.startDate,
                  endDate: selectedDateRange.endDate,
                  containerName: containerIdRegex,
                  kind: 'workload',
                }),
              },
              {
                labelName: 'mean',
                promise: apiKubernetes1.utilisationApi({
                  accountId: accountId,
                  metrics: ['container_http_latency_mean'],
                  startDate: selectedDateRange.startDate,
                  endDate: selectedDateRange.endDate,
                  containerName: containerIdRegex,
                  kind: 'workload',
                }),
              },
            ];
            // Wait for all latency API calls to complete
            const latencyResults = await Promise.allSettled(
              latencyApiCalls.map(({ labelName, promise }) => promise.then((response) => ({ response, labelName })))
            );

            // Process successful responses
            const processedLatencyData = [];
            latencyResults.forEach((result, index) => {
              if (result.status === 'fulfilled') {
                const { response, labelName } = result.value;
                const processedData = processLatencyResponse(response, labelName);
                processedLatencyData.push(processedData);
              } else {
                console.error(`Error fetching latency data for ${latencyApiCalls[index].labelName}:`, result.reason);
              }
            });

            // Combine all processed latency data
            if (processedLatencyData.length > 0) {
              combineLatencyData(processedLatencyData);
            } else {
              handleError('latency', new Error('All latency API calls failed'));
            }
          } catch (error) {
            console.error('Error in fetchLatencyData:', error);
            handleError('latency', error);
          }
        };

        // Define non-latency API calls
        const apiCalls = [
          {
            key: 'request',
            labelName: 'Http Request',
            promise: apiKubernetes1.utilisationApi({
              accountId: accountId,
              metrics: ['container_http_request_count'],
              startDate: selectedDateRange.startDate,
              endDate: selectedDateRange.endDate,
              containerName: containerIdRegex,
              kind: 'workload',
            }),
          },
          {
            key: 'status',
            promise: apiKubernetes1.utilisationApi({
              accountId: accountId,
              metrics: ['container_http_error_status_count'],
              startDate: selectedDateRange.startDate,
              endDate: selectedDateRange.endDate,
              containerName: containerIdRegex,
              kind: 'workload',
            }),
          },
          {
            key: 'services',
            promise: apiKubernetes1.utilisationApi({
              accountId: accountId,
              metrics: ['container_top_destination_services'],
              startDate: selectedDateRange.startDate,
              endDate: selectedDateRange.endDate,
              containerName: containerIdRegex,
              kind: 'workload',
            }),
          },
        ];

        // Execute non-latency API calls
        apiCalls.forEach(({ key, labelName, promise }) => {
          promise
            .then((response) => {
              const seriesList = response?.[0]?.payload || [];
              let seriesData = {
                labels: [],
                data: [],
              };
              if (seriesList.length) {
                const labels = [...new Set(seriesList?.flatMap((e) => e.timestamps) ?? [])];
                labels.sort();
                const chartDataDataset = [];
                seriesList.map((item) => {
                  // O(1) lookup via Map instead of O(n) indexOf per label
                  const tsIndex = new Map();
                  item.timestamps.forEach((ts, i) => tsIndex.set(ts, i));
                  const values = [];
                  labels.forEach((label, _i) => {
                    const index = tsIndex.get(label);
                    if (index !== undefined) {
                      values.push(parseFloat(item.values[index]));
                    } else {
                      values.push(0);
                    }
                  });
                  chartDataDataset.push({
                    label:
                      Object.entries(item.metric)
                        .map(([_key, value]) => `${key}=${value}`)
                        .join('\n') || labelName,
                    data: values,
                  });
                });
                seriesData = {
                  labels: labels.map((e) => convertNumberToTimestamp(e * 1000)),
                  data: chartDataDataset,
                };
              }
              updateChartData(key, seriesData);
            })
            .catch((error) => {
              console.error(`Error fetching ${key} data:`, error);
              handleError(key, error);
            });
        });

        // Fetch latency data separately
        fetchLatencyData();
      };
      fetchData();
    }
  }, [selectedDateRange]);

  useEffect(() => {
    const containerIdRegex = applications.map((a) => `.*/${a.namespace_name}/${a.workload_name}.*`).join('|');
    const workloadRegex = [...new Set(applications.map((a) => `${a.workload_name}.*`))].join('|');
    const namespace = [...new Set(applications.map((a) => a.namespace_name))].join('|');
    const serviceMetricOptions = {
      http: {
        label: 'HTTP Request',
        queryKey: 'container_top_http_requests',
        params: {
          containerName: containerIdRegex,
        },
      },
      cpu: {
        label: 'CPU',
        queryKey: 'container_top_cpu_usage',
        params: {
          namespaceName: namespace,
          workloadName: workloadRegex,
        },
      },
      memory: {
        label: 'Memory',
        queryKey: 'container_top_memory_usage',
        params: {
          namespaceName: namespace,
          workloadName: workloadRegex,
        },
      },
      error: {
        label: 'Error Calls',
        queryKey: 'container_top_http_error_calls',
        params: {
          containerName: containerIdRegex,
        },
      },
    };
    const fetchServiceMetric = async () => {
      setLoading((prev) => ({ ...prev, services: true }));

      try {
        const { queryKey, params } = serviceMetricOptions[selectedServiceMetric];

        const response = await apiKubernetes1.utilisationApi({
          accountId: accountId,
          metrics: [queryKey],
          startDate: getSpecificTime(60),
          endDate: new Date().getTime(),
          kind: 'workload',
          ...params,
        });

        const seriesList = response?.[0]?.payload || [];
        let parsed = { data: [], labels: [] };

        if (seriesList.length) {
          const labels = [...new Set(seriesList.flatMap((e) => e.timestamps))].sort();
          const datasets = seriesList.map((item) => {
            // O(1) lookup via Map instead of O(n) indexOf per label
            const tsIndex = new Map();
            item.timestamps.forEach((ts, i) => tsIndex.set(ts, i));
            const values = labels.map((label) => {
              const idx = tsIndex.get(label);
              return idx !== undefined ? parseFloat(item.values[idx]) : 0;
            });

            return {
              label:
                Object.entries(item.metric)
                  .map(([k, v]) => `${k}=${v}`)
                  .join('\n') || serviceMetricOptions[selectedServiceMetric].label,
              data: values,
            };
          });

          parsed = {
            labels: labels.map((e) => convertNumberToTimestamp(e * 1000)),
            data: datasets,
          };
        }
        updateChartData('services', parsed);
      } catch (error) {
        console.error('Failed to fetch service metric:', error);
        handleError('services', error);
      }

      setLoading((prev) => ({ ...prev, services: false }));
    };

    fetchServiceMetric();
  }, [selectedServiceMetric]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <ListingLayout>
      <ListingLayout.Toolbar
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: selectedDateRange.startDate,
              endTime: selectedDateRange.endDate,
            }}
            onChange={({ selection }) => handleDateRangeChange(selection)}
          />
        }
      />
      <ListingLayout.Body>
        {/* Row 1 */}
        <Box sx={{ display: 'flex', gap: ds.space.mul(0, 40), mb: ds.space[6] }}>
          <Box sx={{ flex: 1 }}>
            <TextWithBorder
              value='HTTP Status'
              borderColor={ds.blue[500]}
              borderWidth='3px'
              sx={{
                minWidth: 'auto',
                height: ds.space.mul(0, 11),
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' },
              }}
            />
            {loading.status ? (
              <Skeleton shape='rect' height={ds.space.mul(0, 140)} />
            ) : (
              <LineChart
                id='k8s-status-k8s-chart'
                dataset={chartData.status.data}
                labels={chartData.status.labels}
                interactionOptions={{ enabled: false }}
              />
            )}
          </Box>
          <Box sx={{ flex: 1 }}>
            <TextWithBorder
              value='HTTP Request'
              borderColor={ds.blue[500]}
              borderWidth='3px'
              sx={{
                minWidth: 'auto',
                height: ds.space.mul(0, 11),
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' },
              }}
            />
            {loading.request ? (
              <Skeleton shape='rect' height={ds.space.mul(0, 140)} />
            ) : (
              <LineChart
                id='k8s-request-k8s-chart'
                dataset={chartData.request.data}
                labels={chartData.request.labels}
                interactionOptions={{ enabled: false }}
              />
            )}
          </Box>
        </Box>

        {/* Row 2 */}
        <Box sx={{ display: 'flex', gap: ds.space.mul(0, 40), mb: ds.space[4] }}>
          <Box sx={{ flex: 1 }}>
            <TextWithBorder
              value='HTTP Latency'
              borderColor={ds.blue[500]}
              borderWidth='3px'
              sx={{
                minWidth: 'auto',
                height: ds.space.mul(0, 11),
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' },
              }}
            />
            {loading.latency ? (
              <Skeleton shape='rect' height={ds.space.mul(0, 140)} />
            ) : (
              <LineChart
                id='k8s-latency-k8s-chart'
                dataset={chartData.latency.data}
                labels={chartData.latency.labels}
                interactionOptions={{ enabled: false }}
              />
            )}
          </Box>
          <Box sx={{ flex: 1 }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[2], height: ds.space.mul(0, 14) }}>
              <TextWithBorder
                value='Top 5 Services'
                borderColor={ds.blue[500]}
                borderWidth='3px'
                sx={{
                  minWidth: 'auto',
                  height: ds.space.mul(0, 11),
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                  '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' },
                }}
              />
              <FilterDropdown
                value={selectedServiceMetric}
                onSelect={(e) => {
                  setSelectedServiceMetric(e.target.value || '');
                }}
                label='Filter'
                options={[
                  { label: 'HTTP Request', value: 'http' },
                  { label: 'CPU', value: 'cpu' },
                  { label: 'Memory', value: 'memory' },
                  { label: 'Error Calls', value: 'error' },
                ]}
              />
            </Box>
            {loading.services ? (
              <Skeleton shape='rect' height={ds.space.mul(0, 140)} />
            ) : (
              <LineChart
                id='k8s-services-k8s-chart'
                dataset={chartData.services.data}
                labels={chartData.services.labels}
                interactionOptions={{ enabled: false }}
                dynamicHeight={false}
              />
            )}
          </Box>
        </Box>
        <LazyLoadComponent
          component={() => import('@components1/k8s/dashboard/HttpLatencyTable')}
          props={{
            accountId: accountId,
            data: {
              workloadName: [...new Set(applications.map((i) => i.workload_name))].join('|'),
              namespaceName: [...new Set(applications.map((i) => i.namespace_name))].join('|'),
            },
          }}
          fallback={<div>Loading latency data...</div>}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};
KubernetesApplicationGroupingSummaryDashboard.propTypes = {
  accountId: PropTypes.string.isRequired,
  applications: PropTypes.array.isRequired,
};

export default KubernetesApplicationGroupingSummaryDashboard;
