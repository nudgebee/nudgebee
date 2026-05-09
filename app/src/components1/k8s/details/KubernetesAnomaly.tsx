import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import PropTypes from 'prop-types';
import apiKubernetes1 from '@api1/kubernetes1';
import { BoxLayout2, LineChart } from '@components1/common';
import { Grid, Alert, AlertTitle, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import apiUser from '@api1/user';
import apiKubernetes from '@api1/kubernetes';
import { useRouter } from 'next/router';
import InvestigateButton from '@components1/common/InvestigateButton';
import { getLastThreeMonths } from '@lib/datetime';
import Datetime from '@components1/common/format/Datetime';
import { prettifyName } from 'src/utils/common';
import SyncIcon from '@mui/icons-material/Sync';
import CustomButton from '@components1/common/NewCustomButton';
import { hasWriteAccess } from '@lib/auth';
import useTriggerAnomaly from '@hooks/useTriggerAnomaly';
import { formatInsight, getSeverityColor, AnomalyInsight } from '@lib/anomalyInsights';
import KubernetesLogs from './KubernetesLogs';
import KubernetesTracesListing from './KubernetesTracesListing';

export const DrilldownChartComponent: React.FC<{ value: any; chartId?: string }> = ({ value, chartId }) => {
  let data: any = {};
  let historicalData: any = {};
  let insights: any[] = [];

  if (typeof value.reference_value === 'string') {
    try {
      data = JSON.parse(value.reference_value);
    } catch {
      console.error('Invalid JSON string in value:', value);
      data = [];
    }
  } else {
    data = value?.reference_value || {};
  }
  if (typeof value?.reference_value?.historical_data === 'string') {
    try {
      historicalData = JSON.parse(value.reference_value.historical_data);
    } catch {
      console.error('Invalid JSON string in value:', value);
      historicalData = [];
    }
  } else {
    historicalData = value?.reference_value?.historical_data || [];
  }

  // Parse insights: prefer top-level insights column, fall back to insights embedded in reference_value
  if (typeof value.insights === 'string') {
    try {
      insights = JSON.parse(value.insights);
    } catch {
      console.error('Invalid JSON string in insights:', value.insights);
      insights = [];
    }
  } else {
    insights = value?.insights || data?.insights || [];
  }

  // Extract baseline from insights (all anomaly points share the same baseline).
  // Skip when comparison_window is "first detection" — baseline_value is 0 and meaningless.
  const firstInsight: AnomalyInsight | null = insights.length > 0 ? (insights[0] as AnomalyInsight) : null;
  const comparisonWindow: string = firstInsight?.comparison_window ?? 'Avg';
  const baselineRaw: number | null = firstInsight && comparisonWindow !== 'first detection' ? firstInsight.baseline_value ?? null : null;
  let baselineValue: number | null = null;
  if (baselineRaw !== null) {
    if (value?.anomaly_type?.toLowerCase() === 'memory') {
      baselineValue = baselineRaw / (1024 * 1024); // bytes → MB, same as dataset
    } else {
      baselineValue = baselineRaw;
    }
  }

  const labels = data.data?.map((item: any) => item.timestamp) || [];
  const dataset =
    data.data?.map((item: any) => {
      if (value?.anomaly_type?.toLowerCase() === 'memory') {
        return (item.data / (1024 * 1024)).toFixed(5);
      } else if (value?.anomaly_type?.toLowerCase() === 'cpu') {
        return item.data.toFixed(5);
      }
      return item.data;
    }) || [];

  let anomaly = data.data?.map((item: any) => (item.anomaly ? item.data : 0)) || [];
  if (value?.anomaly_type?.toLowerCase() === 'memory') {
    anomaly = anomaly.map((item: number) => item / (1024 * 1024));
  }

  const labelsHistorical = historicalData?.map((item: any) => item.timestamp) || [];
  const datasetHistorical =
    historicalData?.map((item: any) => {
      if (value?.anomaly_type?.toLowerCase() === 'memory') {
        return (item.data / (1024 * 1024)).toFixed(5);
      } else if (value?.anomaly_type?.toLowerCase() === 'cpu') {
        return item.data.toFixed(5);
      }
      return item.data;
    }) || [];

  let anomalyHistorical = historicalData?.map((item: any) => (item.anomaly ? item.data : 0)) || [];
  if (value?.anomaly_type?.toLowerCase() === 'memory') {
    anomalyHistorical = anomalyHistorical.map((item: number) => item / (1024 * 1024));
  }

  let label = value?.anomaly_type || '';
  if (label) {
    switch (label.toLowerCase()) {
      case 'memory':
        label = 'Memory (MB)';
        break;
      case 'cpu':
        label = 'CPU';
        break;
      case 'latency':
        label = 'Latency';
        break;
      default:
        label = prettifyName(value.anomaly_type);
        break;
    }
  }

  // Phase annotation: find boundary index between training and evaluation
  const trainingEndTime: string | null = data.training_end_time || null;
  let boundaryIndex = -1;
  if (trainingEndTime && labels.length > 0) {
    const normalizedBoundary = trainingEndTime.replace('T', ' ').replace('Z', '');
    for (let i = 0; i < labels.length; i++) {
      const normalizedLabel = labels[i].replace('T', ' ').replace('Z', '');
      if (normalizedLabel >= normalizedBoundary) {
        boundaryIndex = i;
        break;
      }
    }
  }

  // Scrollable chart: when data is large, render a wide chart and auto-scroll to show evaluation boundary.
  // MAX_CHART_WIDTH caps canvas size within browser limits (Safari ~16384px, Chrome ~32767px).
  const SCROLL_THRESHOLD = 100;
  const MIN_PX_PER_POINT = 5;
  const MAX_CHART_WIDTH = 15000;
  const chartMinWidth = labels.length > SCROLL_THRESHOLD ? Math.min(labels.length * MIN_PX_PER_POINT, MAX_CHART_WIDTH) : undefined;

  // Single ref: measures available width on mount, then doubles as the scroll container.
  // Using one div avoids remounting <LineChart> when containerWidth is set (which would
  // reinitialize Chart.js into a broken blank state).
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState<number | null>(null);

  useLayoutEffect(() => {
    if (scrollContainerRef.current) {
      setContainerWidth(scrollContainerRef.current.offsetWidth);
    }
  }, []);

  // Apply scroll AFTER Chart.js finishes its own rAF initialization cycle.
  useEffect(() => {
    if (!scrollContainerRef.current || boundaryIndex < 0 || !chartMinWidth || !containerWidth) return;
    const el = scrollContainerRef.current;
    const boundaryPixel = chartMinWidth * (boundaryIndex / Math.max(labels.length - 1, 1));
    const targetScroll = Math.max(0, boundaryPixel - containerWidth * 0.15);
    const rafId = requestAnimationFrame(() => {
      if (el?.isConnected) el.scrollLeft = targetScroll;
    });
    return () => cancelAnimationFrame(rafId);
  }, [boundaryIndex, chartMinWidth, labels.length, containerWidth]);

  const phaseAnnotationPlugin = {
    id: 'phaseAnnotation',
    afterDraw: (chart: any) => {
      if (boundaryIndex < 0 || boundaryIndex >= chart.data.labels.length) return;
      const ctx = chart.ctx;
      const xScale = chart.scales.x;
      const { top, bottom, left, right } = chart.chartArea;
      const boundaryX = xScale.getPixelForValue(boundaryIndex);

      ctx.save();
      // Evaluation phase background
      ctx.fillStyle = 'rgba(59, 130, 246, 0.06)';
      ctx.fillRect(boundaryX, top, right - boundaryX, bottom - top);
      // Vertical dashed line
      ctx.beginPath();
      ctx.setLineDash([6, 4]);
      ctx.strokeStyle = 'rgba(107, 114, 128, 0.6)';
      ctx.lineWidth = 1.5;
      ctx.moveTo(boundaryX, top);
      ctx.lineTo(boundaryX, bottom);
      ctx.stroke();
      ctx.setLineDash([]);
      // Phase labels
      ctx.fillStyle = 'rgba(107, 114, 128, 0.8)';
      ctx.font = '11px Roboto, sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText('Training', (left + boundaryX) / 2, top - 6);
      ctx.fillText('Evaluation', (boundaryX + right) / 2, top - 6);
      ctx.restore();
    },
  };

  const baselinePlugin = {
    id: 'baselineLine',
    afterDraw: (chart: any) => {
      if (baselineValue === null) return;
      const ctx = chart.ctx;
      const yScale = chart.scales.y;
      const { left, right, top, bottom } = chart.chartArea;
      const y = yScale.getPixelForValue(baselineValue);

      // Skip if baseline falls outside the visible chart area (e.g. extreme anomaly spikes
      // cause Chart.js to auto-scale so the tiny baseline is off the bottom).
      if (y < top || y > bottom) return;

      ctx.save();
      ctx.beginPath();
      ctx.setLineDash([6, 4]);
      ctx.strokeStyle = 'rgba(16, 185, 129, 0.75)';
      ctx.lineWidth = 1.5;
      ctx.moveTo(left, y);
      ctx.lineTo(right, y);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = 'rgba(16, 185, 129, 0.9)';
      ctx.font = '11px Roboto, sans-serif';
      ctx.textAlign = 'right';
      // Flip label below the line when it would otherwise be clipped above chartArea.top
      const labelY = y - 4 < top + 12 ? y + 13 : y - 4;
      ctx.fillText(comparisonWindow, right - 4, labelY);
      ctx.restore();
    },
  };

  return (
    <Grid key={value?.anomaly_type} container spacing={2}>
      <Grid item xs={12}>
        {/* Outer div measures width on first render, then becomes the scroll container.
            Keeping a single <LineChart> here (no ternary) prevents Chart.js from remounting
            into a blank state when containerWidth is set. */}
        <div
          ref={scrollContainerRef}
          style={{
            width: containerWidth ? `${containerWidth}px` : '100%',
            overflowX: containerWidth && chartMinWidth ? 'auto' : 'visible',
          }}
        >
          <div style={{ width: containerWidth && chartMinWidth ? `${chartMinWidth}px` : '100%' }}>
            <LineChart
              id={chartId}
              colors={[colors.text.lineChart, 'gray']}
              labels={labels}
              dataset={[
                {
                  borderColor: colors.text.lineChart,
                  data: dataset,
                  label: label,
                },
                {
                  borderColor: colors.text.cpuLimit,
                  data: anomaly,
                  label: 'Anomaly',
                  pointStyle: 'star',
                  pointRadius: anomaly.map((val: number) => (val !== 0 ? 3.5 : 0)),
                  pointBackgroundColor: anomaly.map((val: number) => (val !== 0 ? colors.text.cpuLimit : 'transparent')),
                  showLine: false,
                },
              ]}
              chartLabel={''}
              customPlugins={[...(boundaryIndex >= 0 ? [phaseAnnotationPlugin] : []), ...(baselineValue !== null ? [baselinePlugin] : [])]}
              fixedWidth={chartMinWidth}
            />
          </div>
        </div>
      </Grid>
      {labelsHistorical.length > 0 && (
        <Grid item xs={12}>
          <LineChart
            colors={[colors.text.lineChart, 'gray']}
            labels={labelsHistorical}
            dataset={[
              {
                borderColor: colors.text.lineChart,
                data: datasetHistorical,
                label: label,
              },
              {
                borderColor: colors.text.cpuLimit,
                data: anomalyHistorical,
                label: 'Anomaly',
                pointStyle: 'star',
                pointRadius: anomalyHistorical.map((val: number) => (val !== 0 ? 10.5 : 0)),
                pointBackgroundColor: anomalyHistorical.map((val: number) => (val !== 0 ? colors.text.cpuLimit : 'transparent')),
                showLine: false,
              },
            ]}
            chartLabel={'Historial Data'}
          />
        </Grid>
      )}
      {insights && insights.length > 0 && (
        <Grid item xs={12}>
          <Typography variant='h6' sx={{ mb: 1, mt: 2 }}>
            Detected Anomalies ({insights.length})
          </Typography>
          {insights.map((insight: AnomalyInsight) => (
            <Alert key={insight.timestamp} severity={getSeverityColor(insight.severity)} sx={{ mb: 1 }}>
              <AlertTitle>
                <Datetime value={insight.timestamp} />
                <strong>{insight.severity.toUpperCase()}</strong>
              </AlertTitle>
              {formatInsight(insight, value.anomaly_type)}
            </Alert>
          ))}
        </Grid>
      )}
    </Grid>
  );
};

// Returns a 4-hour window centred on updated_at (or now as fallback) as Unix ms timestamps.
// KubernetesLogs expects dateTime.startTime/endTime as numbers (ms); the GraphQL variable is Float.
function getAnomalyTimeRange(drilldownQuery: any): { startTime: number; endTime: number } {
  const center = drilldownQuery.updated_at ? new Date(drilldownQuery.updated_at) : new Date();
  // Guard against an invalid date string coming from the server
  const centerMs = isNaN(center.getTime()) ? Date.now() : center.getTime();
  return {
    startTime: centerMs - 2 * 3600 * 1000,
    endTime: centerMs + 2 * 3600 * 1000,
  };
}

export const KubernetesAnomalyTable = ({ accountId, filterData }: { accountId: string; filterData: any }) => {
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize() ?? 10);
  const [findingIds, setFindingIds] = useState<string[]>([]);

  const router = useRouter();

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page);
    setRecordsPerPage(limit);
  };

  useEffect(() => {
    if (router.query.accountId !== accountId) {
      setCurrentPage(1);
    }
  }, [router.query]);

  useEffect(() => {
    setLoading(true);
    setData([]);

    const query = {
      accountId: accountId,
      offset: (currentPage - 1) * recordsPerPage,
      limit: recordsPerPage,
      namespace: filterData.namespace,
      workload: filterData.name,
      anomalyType: filterData.anomaly_type,
    };
    apiKubernetes1
      .listK8sAnomaliesData(query)
      .then((res) => {
        const anomaliesData = res?.data?.data?.anomaly_v2?.rows || [];
        const findingIds: any = [];
        const tableData = anomaliesData.map((item: any) => {
          findingIds.push(item.id);
          return [
            { text: item.namespace, drilldownQuery: item },
            { text: item.name },
            {
              component: <div key={item.anomalyType}>{item.reference_value ? <p>{item.anomaly_type} anomaly detected.</p> : '-'}</div>,
            },
            {
              component: <Datetime value={item.updated_at} />,
            },
            {
              component: <></>,
            },
          ];
        });
        setData(tableData);
        setTotalCount(res?.data?.data?.anomaly_grouping_v2?.rows?.[0]?.count || 0);
        setFindingIds(findingIds);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId, currentPage, recordsPerPage]);

  useEffect(() => {
    if (!accountId || findingIds.length == 0) {
      return;
    }
    apiKubernetes
      .getK8sEvents(100, 0, {
        aggregation_key: 'Anomaly',
        finding_id: findingIds,
        onlyData: true,
        startDate: getLastThreeMonths(),
        endData: new Date(),
      })
      .then((res: any) => {
        const eventsData = res?.data?.events || [];
        if (eventsData.length > 0) {
          for (const itemData of data as any[]) {
            const item = itemData[0]?.drilldownQuery;
            if (!item) {
              continue;
            }
            const event = eventsData.find((event: any) => {
              return event.finding_id === item.id;
            });

            if (event) {
              (itemData as any)[4].component = (
                <div>
                  <InvestigateButton displayText url={`/investigate?id=${event.id}&accountId=${accountId}`} />
                </div>
              );
            }
          }
          setData([...data]);
        }
      });
  }, [findingIds]);

  return (
    <BoxLayout2
      id={'anomaly-table-container'}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: 'anomaly-table-data',
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
    >
      <KubernetesTable2
        id={'anomaly-table-data'}
        data={data}
        headers={[
          { name: 'Namespace', width: '20%' },
          { name: 'Workload', width: '20%' },
          { name: 'Summary', width: '50%' },
          { name: 'Updated At', width: '10%' },
          { name: '', width: '0%' },
        ]}
        rowsPerPage={recordsPerPage}
        onPageChange={onPageChange}
        pageNumber={currentPage}
        totalRows={totalCount}
        showExpandable={true}
        expandable={{
          tabs: [
            {
              text: 'Trends',
              value: 0,
              key: 'anomaly-trends',
              componentFn: (opt: any, drilldownQuery: any) => {
                const canvasId = `anomaly-trend-chart-${drilldownQuery.id || drilldownQuery.name}`;
                return (
                  <BoxLayout2
                    id='box-trend-charts'
                    sharingOptions={{
                      download: {
                        enabled: true,
                        onClick: () => ({ canvasId, tableId: '' }),
                      },
                      sharing: { enabled: false, onClick: null },
                    }}
                  >
                    <Grid container p={'20px'}>
                      <DrilldownChartComponent value={drilldownQuery} chartId={canvasId} />
                    </Grid>
                  </BoxLayout2>
                );
              },
            },
            {
              text: '+/- Logs',
              value: 1,
              key: 'anomaly-logs',
              componentFn: (_opt: any, drilldownQuery: any) => {
                const { startTime, endTime } = getAnomalyTimeRange(drilldownQuery);
                const queryFromProps = JSON.stringify({
                  namespaceName: drilldownQuery.namespace ?? '',
                  workloadName: drilldownQuery.name ?? '',
                });
                return (
                  <KubernetesLogs
                    accountId={accountId}
                    showTrend={false}
                    showQueryTextBox={false}
                    showPolling={false}
                    dateTime={{ startTime, endTime }}
                    queryFromProps={queryFromProps}
                  />
                );
              },
            },
            {
              text: 'Traces',
              value: 2,
              key: 'anomaly-traces',
              componentFn: (_opt: any, drilldownQuery: any) => {
                const { startTime, endTime } = getAnomalyTimeRange(drilldownQuery);
                return (
                  <KubernetesTracesListing
                    showNamespaceFilter={false}
                    showWorkloadFilter={false}
                    destinationNamespace={drilldownQuery.namespace ?? ''}
                    destinationWorkload={drilldownQuery.name ?? ''}
                    namespace={''}
                    workloadName={''}
                    accountId={accountId}
                    passedSelectedTimestamp={{
                      startTimestamp: new Date(startTime).getTime(),
                      endTimestamp: new Date(endTime).getTime(),
                    }}
                    destinationName={''}
                    showTimeFilter={false}
                    httpStatus={''}
                  />
                );
              },
            },
          ],
        }}
        loading={loading}
      />
    </BoxLayout2>
  );
};

KubernetesAnomalyTable.propTypes = {
  accountId: PropTypes.string,
  data: PropTypes.object,
};

const KubernetesAnomaly = ({ accountId }: { accountId: string }) => {
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize() ?? 10);
  const [anomalyTypes, setAnomalyTypes] = useState<string[]>([]);
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [workloadFilter, setWorkloadFilter] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState('');
  const [selectedAnomalyType, setSelectedAnomalyType] = useState('');
  const [selectedWorkload, setSelectedWorkload] = useState<string>('');

  const router = useRouter();
  const { triggerAnomaly, isLoading: isRefreshLoading } = useTriggerAnomaly(accountId);

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page);
    setRecordsPerPage(limit);
  };

  const getDistinctAnomalyTypes = () => {
    apiKubernetes1.listDistinctAnomalyTypes().then((res) => {
      setAnomalyTypes(res?.anomaly_type.map((item: any) => item.value));
    });
  };

  useEffect(() => {
    if (router.query.accountId !== accountId) {
      setCurrentPage(1);
    }
  }, [router.query]);

  const listAnomalies = () => {
    setLoading(true);
    setData([]);

    const query = {
      accountId: accountId,
      offset: (currentPage - 1) * recordsPerPage,
      limit: recordsPerPage,
      namespace: selectedNamespace,
      workload: selectedWorkload,
      anomalyType: selectedAnomalyType,
    };
    apiKubernetes1
      .listK8sAnomalies(query)
      .then((res) => {
        const anomaliesData = res?.data?.data?.anomaly_v3?.rows || [];
        const tableData = anomaliesData.map((item: any) => {
          return [
            { text: item.namespace, drilldownQuery: item },
            { text: item.name },
            {
              text: item.anomaly_type,
            },
            {
              text: item.anomaly_count,
            },
            {
              component: <Datetime value={item.evaluated_at} />,
            },
          ];
        });
        setData(tableData);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const anomaliesCount = () => {
    setLoading(true);
    setData([]);

    const query = {
      accountId: accountId,
      namespace: selectedNamespace,
      workload: selectedWorkload,
      anomalyType: selectedAnomalyType,
    };
    apiKubernetes1
      .listK8sAnomaliesCount(query)
      .then((res) => {
        const anomaliesData = res?.data?.data?.anomaly_v3?.rows || [];
        if (anomaliesData.length > 0) {
          setTotalCount(anomaliesData[0]?.count || 0);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listAnomalies();
    anomaliesCount();
  }, [accountId, currentPage, recordsPerPage, selectedNamespace, selectedWorkload, selectedAnomalyType]);

  useEffect(() => {
    const workloadQuery: any = {
      namespace: selectedNamespace,
      accountId: accountId,
    };
    apiKubernetes.getAllK8sWorkload(workloadQuery).then((res) => {
      const data = res?.data as any[];
      const workloadNames = data.map((e: any) => e.name) as string[];
      setWorkloadFilter([...new Set(workloadNames)]);
    });
  }, [selectedNamespace]);

  useEffect(() => {
    getDistinctAnomalyTypes();
    apiKubernetes.getK8sNamespaceNames(accountId).then((res) => {
      const namespaces = res.data.namespaces as string[];
      setNamespaceFilter(namespaces);
    });
  }, []);

  return (
    <BoxLayout2
      id={'anomaly-table-container'}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          value: selectedNamespace,
          options: namespaceFilter,
          onSelect: (e: React.ChangeEvent<HTMLSelectElement>) => {
            setSelectedNamespace(e.target.value);
            setSelectedWorkload('');
            setCurrentPage(1);
          },
          minWidth: '150px',
          label: 'Namespace',
        },
        {
          type: 'dropdown',
          enabled: true,
          value: selectedWorkload,
          options: workloadFilter,
          onSelect: (e: React.ChangeEvent<HTMLSelectElement>) => {
            setSelectedWorkload(e.target.value);
            setCurrentPage(1);
          },
          minWidth: '150px',
          label: 'Workloads',
        },
        {
          type: 'dropdown',
          enabled: true,
          value: selectedAnomalyType,
          options: anomalyTypes,
          onSelect: (e: React.ChangeEvent<HTMLSelectElement>) => {
            setSelectedAnomalyType(e.target.value);
            setCurrentPage(1);
          },
          minWidth: '150px',
          label: 'Anomaly Type',
        },
      ]}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: 'anomaly-table',
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
      extraOptions={[
        <CustomButton
          key='triggerAnomalyExecute'
          id='triggerAnomalyExecute'
          data-testid='trigger-anomaly-btn'
          disabled={!hasWriteAccess(accountId)}
          showTooltip={true}
          toolTipTitle={'Trigger Manually'}
          variant='secondary'
          onClick={triggerAnomaly}
          text=''
          startIcon={
            <SyncIcon
              sx={{
                color: colors.text.secondaryDark,
                animation: isRefreshLoading ? 'spin 2s linear infinite' : '',
                fontSize: '20px',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                '@keyframes spin': {
                  '0%': {
                    transform: 'rotate(360deg)',
                  },
                  '100%': {
                    transform: 'rotate(0deg)',
                  },
                },
              }}
            />
          }
          sx={{
            '& .MuiButton-startIcon': {
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              margin: 0,
            },
          }}
        />,
      ]}
    >
      <KubernetesTable2
        id={'anomaly-table'}
        data={data}
        headers={[
          { name: 'Namespace', width: '25%' },
          { name: 'Workload', width: '25%' },
          { name: 'Anomaly Type', width: '30%' },
          { name: 'Count', width: '10%', info: 'Anomaly Count for Last Month' },
          { name: 'Evaluated At', width: '10%' },
          { name: '', width: '0%' },
        ]}
        rowsPerPage={recordsPerPage}
        onPageChange={onPageChange}
        pageNumber={currentPage}
        totalRows={totalCount}
        showExpandable={true}
        expandable={{
          tabs: [
            {
              text: 'Anomalies',
              value: 0,
              key: 'anomaly',
              componentFn: (opt: any, drilldownQuery: any) => <KubernetesAnomalyTable accountId={accountId} filterData={drilldownQuery} />,
            },
          ],
        }}
        loading={loading}
      />
    </BoxLayout2>
  );
};

KubernetesAnomaly.propTypes = {
  accountId: PropTypes.string,
};

export default KubernetesAnomaly;
