import { useState, useEffect, useCallback } from 'react';
import { Grid } from '@mui/material';
import K8sMemoryCpuIndicator from './K8sMemoryCpuIndicator';
import PropTypes from 'prop-types';
import apiKubernetes1 from '@api1/kubernetes1';
import { getSpecificTime } from '@lib/datetime';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import CustomDateTimeRangePicker from '@components1/common/widgets/CustomDateTimeRangePicker';

const KubernetesMemoryCpuOverView = ({
  showUpdatedUi = false,
  requiredTooltip = false,
  clusterSummary,
  updatedOverview = true,
  showUsage = false,
  hideLabels = false,
  sx = {},
  accountId,
}) => {
  const [memoryCpuData, setMemoryCpuData] = useState({
    memory: [
      { name: 'Total', total: 0 },
      { name: 'Usage', usage: 0 },
      { name: 'Limit', limit: 0 },
      { name: 'Request', request: 0 },
    ],
    cpu: [
      { name: 'Total', total: 0 },
      { name: 'Usage', usage: 0 },
      { name: 'Limit', limit: 0 },
      { name: 'Request', request: 0 },
    ],
  });
  const [loading, setLoading] = useState(false);
  const [loadingStates, setLoadingStates] = useState({
    cpu: false,
    memory: false,
    percentile: false,
  });
  // Track which data sections have been loaded
  const [dataLoaded, setDataLoaded] = useState({
    cpu: false,
    memory: false,
    percentile: false,
  });
  // Store individual data sections as they become available
  const [individualData, setIndividualData] = useState({
    cpu: {},
    memory: {},
    percentile: {},
  });

  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getSpecificTime(60),
    endDate: new Date().getTime(),
    shortcutClickTime: 1 * 60 * 60 * 1000,
  });

  const formattedDateRange = {
    startTime: selectedDateRange.startDate,
    endTime: selectedDateRange.endDate,
    shortcutClickTime: selectedDateRange.shortcutClickTime || 0,
  };

  useEffect(() => {
    if (accountId) {
      getClusterDataFromPromethethus(accountId);
    }
  }, [accountId, selectedDateRange]);

  // Effect to handle loading state synchronization - set loading false as soon as any data is available
  useEffect(() => {
    const anyDataLoaded = Object.values(dataLoaded).some((loaded) => loaded);
    if (anyDataLoaded && loading) {
      // Set loading to false as soon as any data section is loaded
      setLoading(false);
    }
  }, [dataLoaded, loading]);

  // Update display data whenever individual data sections change
  useEffect(() => {
    updateDisplayData();
  }, [individualData, dataLoaded]);

  const createMetricsParser = (combinedData) => {
    const getSeriesValue = (series) => {
      const value = series?.[0]?.value?.at?.(-1);
      return value ? parseFloat(value) : 0;
    };

    const getFormattedValue = (series) => {
      const value = getSeriesValue(series);
      return value;
    };

    const metrics = {
      memory: {
        total: () => getFormattedValue(combinedData.mem_total),
        usage: () => getFormattedValue(combinedData.mem_real),
        limit: () => getFormattedValue(combinedData.memory_limit),
        request: () => getFormattedValue(combinedData.memory_request),
        p50usage: () => getFormattedValue(combinedData.p50_mem),
        p90usage: () => getFormattedValue(combinedData.p90_mem),
        maxusage: () => getFormattedValue(combinedData.max_usage_mem),
      },
      cpu: {
        total: () => getFormattedValue(combinedData.cpu_total),
        usage: () => getFormattedValue(combinedData.cpu_real),
        limit: () => getFormattedValue(combinedData.cpu_limit),
        request: () => getFormattedValue(combinedData.cpu_request),
        p50usage: () => getFormattedValue(combinedData.p50_cpu),
        p90usage: () => getFormattedValue(combinedData.p90_cpu),
        maxusage: () => getFormattedValue(combinedData.max_usage_cpu),
      },
    };

    const generateMetricsData = (type, includeExtended = false) => {
      const baseMetrics = ['total', 'usage', 'limit', 'request'];
      const extendedMetrics = [...baseMetrics, 'p50usage', 'p90usage', 'maxusage'];
      const metricsToUse = includeExtended ? extendedMetrics : baseMetrics;

      return metricsToUse.map((metric) => {
        const value = metrics[type][metric]();
        return {
          name: metric.charAt(0).toUpperCase() + metric.slice(1),
          [metric]: value,
        };
      });
    };

    return {
      getMetricsData: (updatedOverview = false) => ({
        memory: generateMetricsData('memory', updatedOverview),
        cpu: generateMetricsData('cpu', updatedOverview),
      }),
    };
  };

  // Update display data based on available individual data sections
  // Use useCallback to prevent unnecessary re-renders and ensure stable reference
  const updateDisplayData = useCallback(() => {
    const { cpu, memory, percentile } = individualData;

    // Combine available data
    const combinedData = {
      ...cpu,
      ...memory,
      ...percentile,
    };

    if (Object.keys(combinedData).length > 0) {
      const metricsParser = createMetricsParser(combinedData);
      const newData = metricsParser.getMetricsData(updatedOverview);

      // Use functional update to prevent race conditions
      setMemoryCpuData((prevData) => {
        const updatedData = { ...prevData };

        // Only update sections that have new data available
        if (dataLoaded.cpu && newData.cpu) {
          updatedData.cpu = newData.cpu;
        }
        if (dataLoaded.memory && newData.memory) {
          updatedData.memory = newData.memory;
        }

        return updatedData;
      });
    }
  }, [individualData, dataLoaded, updatedOverview]);

  // Helper function to extract data from API response
  const extractDataFromResponse = (results) => {
    const formattedData = {};
    results?.forEach((item) => {
      const key = item.query_key;
      const payload = item.payload || [];

      // Map each series in the payload to the target format
      formattedData[key] = payload.map((series) => {
        // We grab the LAST available data point to match the "Instant Vector" format
        // (value: [timestamp, "value"])
        const lastIndex = series.timestamps?.length - 1;
        const timestamp = series.timestamps?.[lastIndex];
        const value = series.values?.[lastIndex];

        return {
          metric: series.metric || {},
          value: [timestamp, String(value)],
        };
      });
    });

    return formattedData;
  };

  // Make API call for a specific query group with individual response handling
  const makeApiCall = async (accountId, metrics, apiType) => {
    try {
      setLoadingStates((prev) => ({ ...prev, [apiType]: true }));

      const response = await apiKubernetes1.utilisationApi({
        accountId: accountId,
        metrics: metrics,
        startDate: selectedDateRange.startDate,
        endDate: selectedDateRange.endDate,
        instant: true,
      });

      const data = extractDataFromResponse(response);

      // Update individual data section immediately when available
      if (Object.keys(data).length > 0) {
        // Use functional updates to prevent race conditions
        setIndividualData((prev) => ({
          ...prev,
          [apiType]: data,
        }));

        // Set data loaded flag using functional update
        setDataLoaded((prev) => ({
          ...prev,
          [apiType]: true,
        }));
      }

      return data;
    } catch (error) {
      console.error(`Error fetching ${apiType} data:`, error);
      return {};
    } finally {
      setLoadingStates((prev) => ({ ...prev, [apiType]: false }));
    }
  };

  const getClusterDataFromPromethethus = async (accountId) => {
    // Reset data loaded state when starting new fetch
    setDataLoaded({
      cpu: false,
      memory: false,
      percentile: false,
    });
    setIndividualData({
      cpu: {},
      memory: {},
      percentile: {},
    });
    // Reset memoryCpuData to initial state to prevent stale data
    setMemoryCpuData({
      memory: [
        { name: 'Total', total: 0 },
        { name: 'Usage', usage: 0 },
        { name: 'Limit', limit: 0 },
        { name: 'Request', request: 0 },
      ],
      cpu: [
        { name: 'Total', total: 0 },
        { name: 'Usage', usage: 0 },
        { name: 'Limit', limit: 0 },
        { name: 'Request', request: 0 },
      ],
    });

    if (!updatedOverview) {
      // Use the old single API call for backward compatibility
      try {
        setLoading(true);
        const response = await apiKubernetes1.utilisationApi({
          accountId: accountId,
          metrics: ['cpu_real', 'cpu_request', 'cpu_limit', 'cpu_total', 'mem_real', 'mem_total', 'memory_limit', 'memory_request'],
          startDate: selectedDateRange.startDate,
          endDate: selectedDateRange.endDate,
          instant: true,
        });
        const data = extractDataFromResponse(response);
        if (Object.keys(data).length > 0) {
          const metricsParser = createMetricsParser(data);
          setMemoryCpuData(metricsParser.getMetricsData(updatedOverview));
        }
      } finally {
        setLoading(false);
      }
      return;
    }

    // New implementation with 3 separate API calls that update UI independently
    try {
      setLoading(true);

      // Execute all 3 API calls in parallel, but each updates UI as soon as it completes
      // Don't await here - let each call complete independently
      makeApiCall(accountId, ['cpu_real', 'cpu_total', 'cpu_request', 'cpu_limit'], 'cpu');
      makeApiCall(accountId, ['mem_real', 'mem_total', 'memory_limit', 'memory_request'], 'memory');
      makeApiCall(accountId, ['p90_mem', 'p90_cpu', 'p50_mem', 'p50_cpu', 'max_usage_mem', 'max_usage_cpu'], 'percentile');
    } catch (error) {
      console.error('Error fetching cluster data:', error);
    }
    // Note: loading state is managed by the useEffect that watches dataLoaded
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      shortcutClickTime: passedSelectedDateTime.shortcutClickTime || 0,
    });
  };

  return (
    <>
      {updatedOverview ? (
        <Grid container mt='10px' spacing={'20px'} sx={{ position: 'relative' }}>
          <Grid item sm={6}>
            <ShimmerLoading isLoading={!dataLoaded.cpu && loadingStates.cpu} height={'200px'} width={'100%'}>
              <K8sMemoryCpuIndicator
                showUpdatedUi={showUpdatedUi}
                requiredTooltip={requiredTooltip}
                clusterSummary={clusterSummary}
                key='CPU'
                unit=''
                title='CPU'
                data={memoryCpuData?.cpu ?? []}
                updatedOverview={updatedOverview}
                showUsage={showUsage}
                hideLabels={hideLabels}
              />
              {/* Loading indicator for CPU data */}
              {loadingStates.cpu && !dataLoaded.cpu && (
                <div
                  style={{
                    position: 'absolute',
                    top: '10px',
                    left: '10px',
                    backgroundColor: '#1976d2',
                    color: 'white',
                    padding: '2px 8px',
                    borderRadius: '4px',
                    fontSize: '12px',
                    zIndex: 1000,
                  }}
                >
                  Loading CPU...
                </div>
              )}
            </ShimmerLoading>
          </Grid>
          <Grid item sm={6}>
            <ShimmerLoading isLoading={!dataLoaded.memory && loadingStates.memory} height={'200px'} width={'100%'}>
              <K8sMemoryCpuIndicator
                showUpdatedUi={showUpdatedUi}
                requiredTooltip={requiredTooltip}
                clusterSummary={clusterSummary}
                key='Memory'
                unit=''
                title='Memory'
                data={memoryCpuData?.memory ?? []}
                updatedOverview={updatedOverview}
                showUsage={showUsage}
                hideLabels={hideLabels}
              />
              {/* Loading indicator for Memory data */}
              {loadingStates.memory && !dataLoaded.memory && (
                <div
                  style={{
                    position: 'absolute',
                    top: '10px',
                    left: '10px',
                    backgroundColor: '#1976d2',
                    color: 'white',
                    padding: '2px 8px',
                    borderRadius: '4px',
                    fontSize: '12px',
                    zIndex: 1000,
                  }}
                >
                  Loading Memory...
                </div>
              )}
            </ShimmerLoading>
          </Grid>
          <CustomDateTimeRangePicker
            showAbsoluteRange={false}
            showOnlyCalenderIcon={false}
            passedSelectedDateTime={formattedDateRange}
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
            ]}
            onChange={(dr) => handleDateRangeChange(dr.selection)}
            sx={{
              position: 'absolute !important',
              top: '-40px !important',
              right: '0px !important',
              border: '1px solid #D0D0D0 !important',
              borderRadius: '4px !important',
            }}
          />
        </Grid>
      ) : (
        <ShimmerLoading isLoading={loading} height={'200px'} width={'100%'}>
          <Grid
            sx={{
              ...sx,
              minHeight: clusterSummary ? '100px' : '124px',
              height: '100%',
              boxSizing: 'border-box',
              flexShrink: 0,
              position: 'relative',
              display: 'flex',
              alignItems: 'center',
              gap: '25px',
              justifyContent: 'space-between',
              p: clusterSummary ? '15px 24px 8px 24px' : '10px 12px 6px 12px',
              borderRadius: '10px',
              border: clusterSummary ? 'none' : '1px solid #E8ECF1',
              backgroundColor: '#FFF',
              boxShadow: 'none',
              overflowWrap: 'break-word',
            }}
          >
            <Grid item sm={6}>
              <K8sMemoryCpuIndicator
                showUpdatedUi={showUpdatedUi}
                requiredTooltip={requiredTooltip}
                clusterSummary={clusterSummary}
                key='CPU'
                unit=''
                title='CPU'
                data={memoryCpuData?.cpu ?? []}
                updatedOverview={updatedOverview}
                hideLabels={hideLabels}
              />
            </Grid>
            <Grid item sm={6} sx={{ padding: '0px' }}>
              <K8sMemoryCpuIndicator
                showUpdatedUi={showUpdatedUi}
                requiredTooltip={requiredTooltip}
                clusterSummary={clusterSummary}
                key='Memory'
                unit=''
                title='Memory'
                data={memoryCpuData?.memory ?? []}
                updatedOverview={updatedOverview}
                hideLabels={hideLabels}
              />
            </Grid>
            <CustomDateTimeRangePicker
              showAbsoluteRange={false}
              showOnlyCalenderIcon={false}
              passedSelectedDateTime={formattedDateRange}
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
              ]}
              onChange={(dr) => handleDateRangeChange(dr.selection)}
              sx={{
                position: 'absolute !important',
                top: '10px !important',
                right: '10px !important',
                border: '1px solid #E8ECF1 !important',
                borderRadius: '6px !important',
              }}
            />
          </Grid>
        </ShimmerLoading>
      )}
    </>
  );
};

export default KubernetesMemoryCpuOverView;

KubernetesMemoryCpuOverView.propTypes = {
  showUpdatedUi: PropTypes.bool,
  requiredTooltip: PropTypes.bool,
  clusterSummary: PropTypes.any,
  sx: PropTypes.object,
  updatedOverview: PropTypes.bool,
  showUsage: PropTypes.bool,
  hideLabels: PropTypes.bool,
  accountId: PropTypes.string,
};
