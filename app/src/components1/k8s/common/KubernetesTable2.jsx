import React, { useEffect, useState } from 'react';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Box, Typography, Grid } from '@mui/material';
import { Card } from '@components1/ds/Card';
import WidgetCard from '@components1/ds/WidgetCard';
import KubernetesPodsTable from '@components1/k8s/details/KubernetesPods';
import { useRouter } from 'next/router';
import LineChart from '@components1/common/charts/LineCharts';
import k8sApi from '@api1/kubernetes';
import MarkDowns from '@components1/common/MarkDowns';
import KubernetesEventsTable from '@components1/events/KubernetesEvents';
import ListingLayout from '@components1/ds/ListingLayout';
import { Button as DsButton } from '@components1/ds/Button';
import zlib from 'zlib';
import Datetime from '@common-new/format/Datetime';
import SafeIcon from '@components1/common/SafeIcon';
import Loader from '@components1/common/Loader';
import { getDateString, getLast30Days, getSpecificTime, getTimeString, timeFormatIn24HoursCompact } from '@lib/datetime';
import KubernetesPodYaml from '@components1/k8s/details/KubernetesPodYaml';
import {
  convertNumberToTimestamp,
  getMsInTimestamp,
  redK8sErrorCodes as redBanner,
  calculateTimeRange,
  generateRandomUUID,
  convertStringCase,
  formatDateForPlusMinusDuration,
  toSeverityLevel,
} from 'src/utils/common';
import BarChart from '@common/charts/BarChart';
import KubernetesServiceMap from '@components1/k8s/details/KubernetesServiceMap';
import { jsonrepair } from 'jsonrepair';
import KubernetesDeploymentHistory from './KubernetesDeploymentHistory';
import PropTypes from 'prop-types';
import KubernetesSecurity from '@components1/recommendations/KubernetesSecurity';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import RefreshIcon from '@mui/icons-material/Refresh';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import apiKubernetes1 from '@api1/kubernetes1';
import apiTriage from '@api1/triage';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import AddCircleOutlineIcon from '@mui/icons-material/AddCircleOutline';
import RemoveCircleOutlineIcon from '@mui/icons-material/RemoveCircleOutline';
import Title from '@components1/common/Title';
import Text from '@common-new/format/Text';
import DownloadButton from '@common-new/DownloadButton';
import { DEFAULT_TITLE, getNubiIconUrl } from '@hooks/useTenantBranding';
import Tooltip from '@components1/ds/Tooltip';
import ConversationPopup from '@components1/llm/ConversationPopup';
import KubernetesLogs from '@components1/k8s/details/KubernetesLogs';
import InvestigateButton from '@components1/common/InvestigateButton';
import NBStatusBadge from '@common-new/widgets/NBStatusBadge';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import KubernetesPlusMinusLogsGradual from '@components1/k8s/details/KubernetesPlusMinusLogsGradual';
import CodeMirrorDiffViewer from '@components1/common/DiffViewer';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiChartPrompt } from 'src/utils/nubiPromptBuilder';
import { ds } from 'src/utils/colors';

const chartContainerStyle = {
  border: '1px solid var(--ds-gray-200)',
  borderRadius: 'var(--ds-radius-md)',
  bgcolor: 'white',
  p: ds.space[4],
  width: '100%',
};

const updateDateRange = (setDateTimeRange, passedSelectedDateTime) => {
  setDateTimeRange({
    startDate: passedSelectedDateTime.startTime,
    endDate: passedSelectedDateTime.endTime,
  });
};

const KubernetesEventEvidences = ({ query }) => {
  let markdownData = '';
  if (query?.evidences) {
    const parsedData = query?.evidences?.map((evidence) => evidence.data);
    if (parsedData !== undefined) {
      const markdownObject = parsedData
        .filter((evidence) => evidence.type === 'markdown')
        .map((evidence) => evidence?.data)
        .join('  \n');
      markdownData = markdownObject;
    }
  }
  if (markdownData) {
    return <MarkDowns data={markdownData} />;
  }
  return (
    <Typography sx={{ mt: 'var(--ds-space-2)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}>
      No Events available.
    </Typography>
  );
};
KubernetesEventEvidences.propTypes = {
  row: PropTypes.any,
  query: PropTypes.object,
  accountId: PropTypes.any,
};

const KubernetesEventUtilization = ({ query }) => {
  let svgImages = [];
  if (query?.evidences) {
    const parsedData = query?.evidences?.map((evidence) => evidence.data);
    if (parsedData !== undefined) {
      if (parsedData.some((evidence) => evidence.type === 'svg')) {
        const svgObject = parsedData.filter((evidence) => evidence.type === 'svg').map((evidence) => evidence?.data);
        let svgData = svgObject;
        svgData.forEach((base64SVG) => {
          let svgData = base64SVG.replace("b'", '');
          svgData = svgData.replace("'", '');
          svgImages.push(svgData);
        });
      }
    }
  }
  return (
    <Box>
      {svgImages.map((data, idx) => (
        <SafeIcon key={idx} src={`data:image/svg+xml;base64,${data}`} alt='SVG Image' width={1000} height={400} />
      ))}
    </Box>
  );
};
KubernetesEventUtilization.propTypes = {
  row: PropTypes.any,
  query: PropTypes.object,
  accountId: PropTypes.any,
};

// Helper to get classification label
const getClassificationLabel = (classification) => {
  const labels = {
    duplicate: 'Duplicate',
    false_positive: 'False Positive',
    true_positive: 'True Positive',
  };
  return labels[classification] || classification || '-';
};

// Component to display events matched by a triage rule using the fast indexed API
export const TriageRuleEventsTable = ({ query, onOpenTicketForm }) => {
  const [events, setEvents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);

  const fetchEvents = async () => {
    if (!query?.triageRuleId) {
      setLoading(false);
      return;
    }

    try {
      const result = await apiTriage.getTriageRuleEvents({
        rule_id: query.triageRuleId,
        account_id: query.accountId,
        limit: 50,
        offset: 0,
        start_date: query.startDate?.toISOString(),
        end_date: query.endDate?.toISOString(),
      });

      if (result) {
        setEvents(result.events || []);
        setTotal(result.total || 0);
      }
    } catch (error) {
      console.error('Failed to fetch triage rule events:', error);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchEvents();
  }, [query?.triageRuleId, query?.accountId, query?.startDate, query?.endDate]);

  // Transform events to table rows
  const tableData = events.map((event) => {
    return [
      {
        component: <SeverityIcon level={toSeverityLevel(event.priority)} />,
        data: event.priority || 'INFO',
      },
      {
        component: <Text showAutoEllipsis value={event.title} />,
      },
      {
        component: (
          <>
            <Text value={event.subject_name || '-'} />
            {event.subject_namespace && <Text value={`ns: ${event.subject_namespace}`} secondaryText />}
          </>
        ),
      },
      {
        component: <Text value={getClassificationLabel(event.classification)} />,
      },
      {
        component: (
          <NBStatusBadge
            eventId={event.id}
            currentStatus={event.nb_status || 'OPEN'}
            snoozedUntil={event.snoozed_until}
            onStatusChange={() => fetchEvents()}
            onCreateTicket={() => onOpenTicketForm?.(event, query?.accountId)}
          />
        ),
      },
      {
        component: <Datetime value={event.classified_at} />,
      },
      {
        component: <InvestigateButton displayText url={`/investigate?id=${event.id}&accountId=${event.account_id}`} />,
      },
    ];
  });

  const headers = [
    { name: 'Severity', width: '8%' },
    { name: 'Title', width: '25%' },
    { name: 'Subject', width: '18%' },
    { name: 'Classification', width: '12%' },
    { name: 'Alert Status', width: '12%' },
    { name: 'Classified At', width: '15%' },
    { name: '', width: '10%' },
  ];

  if (loading) {
    return (
      <Box p={ds.space[4]}>
        <Loader style={{ height: ds.space.mul(0, 100), width: '100%' }} />
      </Box>
    );
  }

  if (!events || events.length === 0) {
    return (
      <Box p={ds.space[4]}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>
          No events matched by this rule in the selected time range.
        </Typography>
      </Box>
    );
  }

  return (
    <ListingLayout sx={{ mb: 0, pt: ds.space[4] }}>
      <ListingLayout.Body>
        <Typography variant='body2' sx={{ mb: 'var(--ds-space-1)', fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>
          Showing {events.length} of {total} matched events (last 30 days)
        </Typography>
        <CustomTable2 tableData={tableData} headers={headers} rowsPerPage={10} tableHeadingCenter={['Severity', 'Classification', 'Alert Status']} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};
TriageRuleEventsTable.propTypes = {
  query: PropTypes.object,
  onOpenTicketForm: PropTypes.func,
};

export const KubernetesEventLog = ({ query }) => {
  const [log, setLog] = useState('');
  const base64Converter = (data) => {
    data = data.replace("b'", '');
    data = data.replace("'", '');
    const bufferData = Buffer.from(data, 'base64');
    return bufferData;
  };

  function unzipData(gzData) {
    return new Promise((resolve, reject) => {
      zlib.unzip(gzData, (err, unzippedBuffer) => {
        if (err) {
          console.error('Error unzipping the file:', err);
          reject(err); // Reject the Promise if there's an error
        } else {
          const unzippedData = unzippedBuffer.toString('utf8'); // Change encoding if needed
          resolve(unzippedData); // Resolve the Promise with the unzipped data
        }
      });
    });
  }

  if (query?.evidences) {
    let gzObject = '';
    const parsedData = query?.evidences;
    if (parsedData !== undefined) {
      let evidencesData = query.evidences;
      if (typeof evidencesData === 'string') {
        evidencesData = JSON.parse(query.evidences);
      }
      gzObject = evidencesData
        .filter((item) => item.type === 'gz')
        .map((index) => index?.data)
        .join('  \n');
    }

    if (gzObject) {
      const gzData = base64Converter(gzObject);
      unzipData(gzData)
        .then((unzippedData) => {
          const combinedData = '```'.concat(unzippedData.concat('```'));
          setLog(combinedData);
        })
        .catch((error) => {
          console.error('Error:', error);
        });
    }
  }

  if (log) {
    return <MarkDowns data={log} sx={{ width: ds.space.mul(0, 400), overflowY: '', maxHeight: '' }} />;
  }
  return (
    <Typography sx={{ mt: 'var(--ds-space-2)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}>
      No logs available.
    </Typography>
  );
};

KubernetesEventLog.propTypes = {
  row: PropTypes.any,
  accountId: PropTypes.string,
  query: PropTypes.object,
};

const getMemory = (memLimit) => {
  if (!memLimit) {
    return 0;
  }
  if (typeof memLimit === 'number') {
    return memLimit / (1024 * 1024 * 1024);
  }

  const units = {
    Gi: 1,
    Mi: 1 / 1024,
    Ki: 1 / (1024 * 1024),
    Ti: 1024,
    Pi: 1024 * 1024,
    m: 1 / (1024 * 1024 * 1024 * 1024),
  };

  const match = memLimit.match(/(\d+)(\w+)/);
  if (match) {
    const [, value, unit] = match;
    return parseInt(value) * (units[unit] || 1 / (1024 * 1024 * 1024));
  }

  return parseInt(memLimit) / (1024 * 1024 * 1024);
};

const getCpu = (cpuLimit) => {
  if (!cpuLimit) {
    return 0;
  }
  if (typeof cpuLimit === 'number') {
    return cpuLimit;
  }

  if (cpuLimit.includes('m')) {
    return parseInt(cpuLimit) / 1000;
  } else if (cpuLimit.includes('u')) {
    return parseInt(cpuLimit) / (1000 * 1000);
  } else if (cpuLimit.includes('n')) {
    return parseInt(cpuLimit) / (1000 * 1000 * 1000);
  }

  return parseInt(cpuLimit);
};

const getLimitsAndRequests = (query, memLimit, cpuLimit) => {
  let newMemLimit = memLimit;
  let newCpuLimit = cpuLimit;
  let memReq, cpuReq;

  if (query?.workloadMeta?.config?.containers?.length > 0) {
    const container = query.workloadMeta.config.containers[0];
    const resources = container.resources || {};

    newMemLimit = newMemLimit || getMemory(resources.limits?.memory);
    newCpuLimit = newCpuLimit || getCpu(resources.limits?.cpu);
    memReq = getMemory(resources.requests?.memory);
    cpuReq = getCpu(resources.requests?.cpu);
  }

  newCpuLimit = newCpuLimit || getCpu(query.cpu_limit);
  newMemLimit = newMemLimit || getMemory(query.memory_limit);

  if (!memReq && !cpuReq) {
    memReq = getMemory(query.memory_request);
    cpuReq = getCpu(query.cpu_request);
  }

  return { memLimit: newMemLimit, cpuLimit: newCpuLimit, memReq, cpuReq };
};

const processChartData = (podGroupings, cpuLimit, memLimit, cpuReq, memReq, differenceInHours, gpuTrend) => {
  const cpuData = { data: [[], [], []], labels: [], timestamps: [] };
  const memData = { data: [[], [], []], labels: [], timestamps: [] };
  const gpuData = { data: [[], [], [], []], labels: [] };
  const diskData = { data: [[], []], labels: [] };

  podGroupings.forEach((e) => {
    cpuData.data[0].push(e.avg_cpu_used);
    cpuData.data[1].push(e.avg_cpu_request || cpuReq);
    cpuData.data[2].push(e.avg_cpu_limit || cpuLimit || 0);

    memData.data[0].push(e.avg_memory_used / (1024 * 1024 * 1024));
    memData.data[1].push(e.avg_memory_request ? e.avg_memory_request / (1024 * 1024 * 1024) : memReq || 0);
    memData.data[2].push(e.avg_memory_limit ? e.avg_memory_limit / (1024 * 1024 * 1024) : memLimit || 0);
    if (e.disk_used && e.disk_total && Number.isFinite(e.disk_used) && Number.isFinite(e.disk_total)) {
      diskData.data[0].push(e.disk_used / (1024 * 1024 * 1024));
      diskData.data[1].push(e.disk_total / (1024 * 1024 * 1024));
    }

    if (gpuTrend) {
      gpuData.data[0].push(e.sum_gpu_used);
      gpuData.data[1].push(e.sum_gpu_temp);
      gpuData.data[2].push(e.sum_gpu_mem_temp);
      gpuData.data[3].push(e.sum_gpu_mem_usage);
    }

    const label = differenceInHours > 24 ? getDateString(e.timestamp) : getTimeString(e.timestamp);
    cpuData.labels.push(label);
    memData.labels.push(label);
    const ts = new Date(e.timestamp).getTime();
    cpuData.timestamps.push(ts);
    memData.timestamps.push(ts);
    if (diskData.data[0].length > 0 && diskData.data[1].length > 0) {
      diskData.labels.push(label);
    }
    if (gpuTrend) {
      gpuData.labels.push(label);
    }
  });

  if (cpuData.labels.length === 1) {
    [cpuData, memData, diskData].forEach((data) => {
      data.data.forEach((arr) => {
        arr.unshift(null);
        arr.push(null);
      });
      data.labels.unshift('');
      data.labels.push('');
      if (data.timestamps) {
        data.timestamps.unshift(null);
        data.timestamps.push(null);
      }
    });
  }

  return { cpuData, memData, gpuData, diskData };
};

export const KubernetesUtilizationCharts = ({ accountId, query = {}, selectedDateRange, memLimit, cpuLimit }) => {
  return (
    <>
      <Box mb={ds.space[2]} />
      <KubernetesUtilizationCharts2
        accountId={accountId}
        query={{
          ...query,
          metrics: ['cpu_usage', 'memory_usage', 'cpu_limit', 'cpu_request', 'memory_limit', 'memory_request'],
        }}
        selectedDateRange={selectedDateRange}
        memLimit={memLimit}
        cpuLimit={cpuLimit}
      />
      <Box mb={ds.space[2]} />
      <KubernetesDiskTrend
        accountId={accountId}
        query={{
          ...query,
        }}
      />
    </>
  );
};

KubernetesUtilizationCharts.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.any,
  selectedDateRange: PropTypes.object,
  memLimit: PropTypes.any,
  cpuLimit: PropTypes.any,
};

export const KubernetesUtilizationCharts2 = ({
  accountId,
  query = {},
  memLimit,
  cpuLimit,
  selectedDateRange,
  additionalFilters = [],
  heading = '',
}) => {
  const [cpuData, setCpuData] = useState({ data: [[], [], []], labels: [], timestamps: [] });
  const [memData, setMemData] = useState({ data: [[], [], []], labels: [], timestamps: [] });
  const [diskData, setDiskData] = useState({ data: [[], []], labels: [] });
  const [showLoading, setShowLoading] = useState(false);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [promQueries, setPromQueries] = useState({});

  const handleAskNubi = (dataPointContext) => {
    // Derive the specific metric query from the clicked series label
    const labelLower = (dataPointContext.labelValue || '').toLowerCase();
    let metricQuery = '';
    if (labelLower.includes('cpu')) {
      metricQuery = promQueries.cpu_usage || 'cpu_usage';
    } else if (labelLower.includes('memory') || labelLower.includes('mem')) {
      metricQuery = promQueries.memory_usage || 'memory_usage';
    }

    const prompt = buildNubiChartPrompt({
      ...dataPointContext,
      podName: query.pod_name || query.podName || query.subject_name || '',
      namespaceName: query.namespace_name || query.namespaceName || query.subject_namespace || '',
      workloadName: query.workload_name || query.workloadName || '',
      workloadKind: query.kind || query.subject_kind || '',
      metricQuery,
    });
    setNubiQuery(prompt);
    setNubiSidebarVisible(true);
  };

  const [dateTimeRange, setDateTimeRange] = useState(
    selectedDateRange ?? {
      startDate: Date.now() - 1 * 3600 * 1000,
      endDate: Date.now(),
    }
  );

  const fetchData = async () => {
    setShowLoading(true);
    setCpuData({ data: [[], [], []], labels: [] });
    setMemData({ data: [[], [], []], labels: [] });
    setDiskData({ data: [[], []], labels: [] });

    try {
      const res = await k8sApi.getK8sPodGroupings2(1000, {
        ...query,
        accountId: accountId,
        startDate: new Date(dateTimeRange.startDate),
        endDate: new Date(dateTimeRange.endDate),
      });

      const podGroupings = res?.data?.k8s_pod_groupings || [];
      const differenceInHours = (dateTimeRange.endDate - dateTimeRange.startDate) / (1000 * 60 * 60);
      const { memReq, cpuReq } = getLimitsAndRequests(query, memLimit, cpuLimit);
      const processedChartData = processChartData(podGroupings, cpuLimit, memLimit, cpuReq, memReq, differenceInHours, query.gpuTrend);

      setCpuData(processedChartData.cpuData);
      setMemData(processedChartData.memData);
      setDiskData(processedChartData.diskData);
      setPromQueries(res?.data?.promQueries || {});
    } catch (error) {
      console.error('Error fetching Kubernetes pod groupings:', error);
    } finally {
      setShowLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, [accountId, JSON.stringify(query), dateTimeRange.startDate, dateTimeRange.endDate]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='box-utilization-charts'>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: dateTimeRange.startDate,
                endTime: dateTimeRange.endDate,
              }}
              onChange={({ selection }) => handleDateRangeChange(selection)}
            />
            <DsButton
              tone='secondary'
              size='sm'
              composition='icon-only'
              icon={<RefreshIcon />}
              aria-label='Refresh'
              tooltip='Refresh'
              onClick={fetchData}
              loading={showLoading}
            />
          </>
        }
      >
        {additionalFilters}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <Grid
          container
          spacing={ds.space[4]}
          pt={ds.space.mul(0, 10)}
          mb={ds.space.mul(0, 10)}
          sx={{
            borderRadius: '0 0 var(--ds-radius-lg) var(--ds-radius-lg)',
          }}
        >
          <Grid item xs={12}>
            <Grid container sx={chartContainerStyle}>
              <Grid item xs={12} md={6}>
                <LineChart
                  chartTitle='CPU Utilization (Core)'
                  colors={['#f5b400', '#5b9eff', '#e5484d']}
                  dataset={[
                    {
                      label: 'CPU Usage',
                      data: cpuData.data[0],
                      borderColor: '#f5b400',
                      backgroundColor: 'white',
                      pointRadius: 0,
                      borderWidth: 1,
                    },
                    {
                      label: 'CPU Requested',
                      data: cpuData.data[1],
                      borderColor: '#5b9eff',
                      backgroundColor: 'white',
                      pointRadius: 0,
                      borderWidth: 1,
                    },
                    {
                      label: 'CPU Limit',
                      data: cpuData.data[2],
                      borderColor: '#e5484d',
                      backgroundColor: 'white',
                      pointRadius: 0,
                      borderWidth: 1,
                    },
                  ]}
                  labels={cpuData.labels}
                  timestamps={cpuData.timestamps}
                  chartLabel={['Usage', 'Requested', 'Limit']}
                  loading={showLoading}
                  onAskNubi={handleAskNubi}
                />
              </Grid>
              <Grid item xs={12} md={6}>
                <LineChart
                  chartTitle='Memory Utilization (GB)'
                  colors={['#f5b400', '#5b9eff', '#e5484d']}
                  dataset={[
                    {
                      label: 'Memory Usage',
                      data: memData.data[0],
                      borderColor: '#f5b400',
                      backgroundColor: 'white',
                      pointRadius: 0,
                      borderWidth: 1,
                    },
                    {
                      label: 'Memory Requested',
                      data: memData.data[1],
                      borderColor: '#5b9eff',
                      backgroundColor: 'white',
                      pointRadius: 0,
                      borderWidth: 1,
                    },
                    {
                      label: 'Memory Limit',
                      data: memData.data[2],
                      borderColor: '#e5484d',
                      backgroundColor: 'white',
                      pointRadius: 0,
                      borderWidth: 1,
                    },
                  ]}
                  labels={memData.labels}
                  timestamps={memData.timestamps}
                  chartLabel={['Usage', 'Requested', 'Limit']}
                  loading={showLoading}
                  onAskNubi={handleAskNubi}
                />
              </Grid>
            </Grid>
          </Grid>
          {diskData.data[0].length > 0 && diskData.data[1].length > 0 && (
            <Grid item xs={12}>
              <Grid
                container
                sx={{
                  border: '1px solid var(--ds-gray-200)',
                  borderRadius: 'var(--ds-radius-md)',
                  bgcolor: 'white',
                  p: ds.space[4],
                  width: '100%',
                }}
              >
                <Grid item xs={12}>
                  <LineChart
                    chartTitle='Disk Utilization (GB)'
                    colors={['#f5b400', '#5b9eff']}
                    dataset={[
                      {
                        label: 'Disk Used',
                        data: diskData.data[0],
                        borderColor: '#f5b400',
                        backgroundColor: 'white',
                        pointRadius: 0,
                        borderWidth: 1,
                      },
                      {
                        label: 'Disk Total',
                        data: diskData.data[1],
                        borderColor: '#5b9eff',
                        backgroundColor: 'white',
                        pointRadius: 0,
                        borderWidth: 1,
                      },
                    ]}
                    labels={diskData.labels}
                    chartLabel={['Disk Used', 'Disk Total']}
                    loading={showLoading}
                  />
                </Grid>
              </Grid>
            </Grid>
          )}
        </Grid>
        {nubiSidebarVisible && (
          <NubiChatSidebar
            isVisible={nubiSidebarVisible}
            onClose={() => setNubiSidebarVisible(false)}
            accountId={accountId}
            queryPrefix={nubiQuery}
            context={{ type: 'cluster' }}
            apiMode='investigate'
            source='ask_nudgbee_chat'
            position='right'
            mode='overlay'
            width={ds.space.mul(0, 250)}
          />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesUtilizationCharts2.propTypes = {
  row: PropTypes.any,
  accountId: PropTypes.string,
  query: PropTypes.any,
  memLimit: PropTypes.string,
  cpuLimit: PropTypes.string,
  selectedDateRange: PropTypes.any,
  additionalFilters: PropTypes.array,
  heading: PropTypes.string,
};

export const KubernetesCostCharts = ({ accountId, query, selectedDateRange, heading, actualCostTrend = false }) => {
  let d = new Date();

  const [costData, setCostData] = useState({ data: { usageCost: [], actualCost: [] }, labels: [] });
  const [dateTimeRange, setDateTimeRange] = useState(selectedDateRange ?? { startDate: d.getTime() - 30 * 24 * 3600 * 1000, endDate: d.getTime() });
  const [showCostLoading, setShowCostLoading] = useState(false);

  const getResourceCostData = async (updatedCostData) => {
    const costQuery = {};

    query.accountId = accountId;
    if (query?.data?.node_flavor) {
      costQuery.resourceType = query.data.node_flavor;
    }
    if (query?.data?.node_region) {
      costQuery.resourceRegion = query.data.node_region;
    }
    try {
      const res = await k8sApi.getK8sResourceCost(costQuery);
      const resourceCost = res?.data?.cloud_resource_details_v2?.rows?.[0]?.resource_cost ?? 0;
      updatedCostData.data.actualCost = updatedCostData.data.usageCost.map(() => resourceCost * 24);
    } catch {
      updatedCostData.data.actualCost = [];
    }
    return updatedCostData;
  };

  const getUsageCostData = async () => {
    const groupBy = ['tenant_id', 'account_id', 'timestamp'];

    query.accountId = accountId;
    if (query.workloadName || query.workload_name) {
      groupBy.push('workload_name');
    }
    if (query.podName || query.pod_name) {
      groupBy.push('pod_name');
    }
    if (dateTimeRange?.startDate && dateTimeRange.endDate) {
      query.startDate = new Date(dateTimeRange.startDate);
      query.endDate = new Date(dateTimeRange.endDate);
    }

    setShowCostLoading(true);
    try {
      const res = await k8sApi.getK8sPodGroupings(1000, query, groupBy);
      let usageCostArray = [];
      let labelsArray = [];
      let updatedCostData = { ...costData };
      res.data?.k8s_pod_groupings?.forEach((e) => {
        usageCostArray.push(Number(e.pod_cost ?? 0).toFixed(2));
        labelsArray.push(getDateString(e.timestamp));
      });
      updatedCostData.data.usageCost = usageCostArray;
      updatedCostData.labels = labelsArray;
      if (actualCostTrend) {
        updatedCostData = await getResourceCostData(updatedCostData);
      }
      setCostData(updatedCostData);
    } finally {
      setShowCostLoading(false);
    }
  };

  useEffect(() => {
    getUsageCostData();
  }, [accountId, query, dateTimeRange.startDate, dateTimeRange.endDate]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='box-cost-charts'>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: dateTimeRange.startDate,
              endTime: dateTimeRange.endDate,
            }}
            onChange={({ selection }) => handleDateRangeChange(selection)}
          />
        }
      />
      <ListingLayout.Body>
        <Grid
          container
          p={ds.space.mul(0, 10)}
          sx={{
            borderRadius: '0 0 var(--ds-radius-lg) var(--ds-radius-lg)',
            boxShadow: '0px 4px 20px 0px #99999940',
            borderBottom: '1px solid var(--ds-gray-200)',
            borderInline: '1px solid var(--ds-gray-200)',
            bgcolor: 'white',
          }}
        >
          <Grid item md={12}>
            <LineChart
              colors={['#8a5cf6', 'gray']}
              labels={costData.labels}
              dataset={[
                {
                  borderColor: '#8a5cf6',
                  data: costData?.data?.usageCost ?? [],
                  label: 'Cost($)',
                },
                ...(actualCostTrend
                  ? [
                      {
                        borderColor: 'gray',
                        data: costData.data?.actualCost ?? [],
                        label: 'Actual cost($)',
                      },
                    ]
                  : []),
              ]}
              chartLabel={['Cost($)', ...(actualCostTrend ? ['Actual cost($)'] : [])]}
              loading={showCostLoading}
            />
          </Grid>
        </Grid>
      </ListingLayout.Body>
    </ListingLayout>
  );
};
KubernetesCostCharts.propTypes = {
  row: PropTypes.any,
  query: PropTypes.object,
  accountId: PropTypes.any,
  selectedDateRange: PropTypes.object,
  heading: PropTypes.string,
  actualCostTrend: PropTypes.bool,
};

const KubernetesRelatedEventTable = ({ query, tableName }) => {
  const evidencedata = query?.evidences;
  if (evidencedata) {
    let evidencesData = query?.evidences;
    if (typeof evidencesData === 'string') {
      evidencesData = JSON.parse(query.evidences);
    }
    const filterdata = evidencesData.filter((f) => f.type === 'table' && f.data.table_name.includes(tableName));
    if (filterdata && filterdata.length > 0) {
      const headers = filterdata[0].data.headers;
      const convertedJson = {
        rows: filterdata[0].data.rows.map((row) => {
          const rowData = {};
          headers.forEach((header, index) => {
            rowData[header] = row[index];
          });
          return rowData;
        }),
      };
      const convertedJson2 = convertedJson.rows.map((item) => {
        const isRowRed = Object.values(item).some((value) => typeof value === 'string' && redBanner.includes(value.toLowerCase()));
        const components = Object.entries(item).map(([key, value]) => ({
          component: (
            <Text
              sx={{
                color: isRowRed ? 'var(--ds-red-600)' : 'var(--ds-gray-700)',
                '@media (max-width: 1350px)': {
                  fontSize: 'var(--ds-text-caption)',
                },
              }}
              key={key}
              value={key === 'time' && typeof value === 'number' ? getMsInTimestamp(value) : value}
            />
          ),
        }));
        return components;
      });
      return <CustomTable2 headers={filterdata[0].data.headers} tableData={convertedJson2} rowsPerPage={convertedJson2.length} />;
    }
  }
  return <div>Nothing</div>;
};
KubernetesRelatedEventTable.propTypes = {
  query: PropTypes.object,
  tableName: PropTypes.string,
};

const KubernetesLogDetails = ({ query }) => {
  function cleanupMessage(message) {
    try {
      let jsonMessage = message;
      jsonMessage = jsonrepair(jsonMessage);
      jsonMessage = jsonMessage.replaceAll('\n', '\\n');
      message = JSON.stringify(jsonMessage, null, 2);
    } catch {
      message = message.replace(/\\n/g, '\n');
    }
    return message;
  }

  // Flatten nested objects into dot-notation key-value pairs
  function flattenObject(obj, prefix = '') {
    const result = [];
    for (const [k, v] of Object.entries(obj)) {
      const fullKey = prefix ? `${prefix}.${k}` : k;
      if (v && typeof v === 'object' && !Array.isArray(v)) {
        result.push(...flattenObject(v, fullKey));
      } else {
        result.push({ key: fullKey, value: Array.isArray(v) ? JSON.stringify(v) : String(v) });
      }
    }
    return result;
  }

  const renderLabelRow = (key, value, index) => (
    <Grid item xs={6} key={key} sx={{ backgroundColor: 'white', paddingX: ds.space.mul(0, 7), paddingY: ds.space.mul(0, 3) }}>
      <Typography variant='subtitle1' style={{ overflowY: 'overlay', display: 'flex', alignItems: 'baseline', wordBreak: 'break-all' }}>
        {query.callback && key !== 'timestamp' && (
          <Typography style={{ fontWeight: 'bold', minWidth: ds.space.mul(0, 40) }}>
            <DsButton
              tone='ghost'
              size='sm'
              composition='icon-only'
              aria-label='add-filter'
              icon={<AddCircleOutlineIcon />}
              onClick={() => {
                query.callback({
                  id: index,
                  label: key,
                  operator: '=',
                  value,
                });
              }}
            />
            <DsButton
              tone='ghost'
              size='sm'
              composition='icon-only'
              aria-label='remove-filter'
              icon={<RemoveCircleOutlineIcon />}
              onClick={() => {
                query.callback({
                  id: index,
                  label: key,
                  operator: '!=',
                  value,
                });
              }}
            />
          </Typography>
        )}
        {key === 'timestamp' && query.callback && <Typography style={{ fontWeight: 'bold', minWidth: ds.space.mul(0, 40) }} />}
        <Typography style={{ fontWeight: 'bold', minWidth: ds.space.mul(0, 65) }}>{key}:</Typography>
        {key === 'timestamp' ? <Datetime value={new Date(value / 1000000)} /> : <span style={{ fontFamily: 'monospace' }}>{value}</span>}
      </Typography>
    </Grid>
  );

  // Build flattened label entries from all labels
  const labelEntries = [];
  let entryIndex = 0;
  Object.keys(query?.data?.labels || {})
    .filter(
      (k) =>
        k !== 'message' &&
        k !== 'callback' &&
        k !== 'logQuery' &&
        query.data.labels[k] !== '' &&
        query.data.labels[k] !== '-' &&
        query.data.labels[k] != null
    )
    .forEach((key) => {
      const value = query.data.labels[key];
      if (value && typeof value === 'object' && !Array.isArray(value)) {
        // Flatten nested object into dot-notation entries
        const flattened = flattenObject(value, key);
        flattened.forEach((entry) => {
          labelEntries.push({ key: entry.key, value: entry.value, index: entryIndex++ });
        });
      } else {
        labelEntries.push({ key, value: Array.isArray(value) ? JSON.stringify(value) : value, index: entryIndex++ });
      }
    });

  return (
    <WidgetCard>
      {labelEntries.length > 0 ? (
        <Grid container sx={{ backgroundColor: 'white' }}>
          {labelEntries.map((entry) => renderLabelRow(entry.key, entry.value, entry.index))}

          <Grid item xs={12} sx={{ my: 'var(--ds-space-4)', px: 'var(--ds-space-3)' }}>
            <Card variant='outlined' size='md' header={<Typography sx={{ fontWeight: 'bold' }}>Message</Typography>}>
              <MarkDowns
                data={cleanupMessage(query.data.message)}
                sx={{ width: '100%', wordBreak: 'break-word', overflowY: 'none', fontFamily: 'monospace' }}
              />
            </Card>
          </Grid>
        </Grid>
      ) : (
        <Typography>No Data</Typography>
      )}
    </WidgetCard>
  );
};
KubernetesLogDetails.propTypes = {
  query: PropTypes.object,
};

const KubernetesLogstashDetails = ({ query }) => {
  return (
    <CodeMirror
      value={JSON.stringify(query, null, 4)}
      height={ds.space.mul(0, 150)}
      extensions={[json(), EditorView.lineWrapping]}
      editable={false}
      style={{
        border: '1px solid silver',
      }}
    />
  );
};
KubernetesLogstashDetails.propTypes = {
  query: PropTypes.object,
};

const KubernetesLogPatternDetails = ({ query }) => {
  const hasChart = query?.timestamps?.length > 1;
  const sampleCard = (
    <Card variant='outlined' size='md' header={<Typography sx={{ fontWeight: 'bold' }}>Sample</Typography>}>
      <Typography
        sx={{
          fontFamily: 'monospace',
          fontSize: 'var(--ds-text-body)',
          lineHeight: '1.6',
          wordBreak: 'break-word',
          overflowWrap: 'anywhere',
        }}
      >
        {query?.sample}
      </Typography>
    </Card>
  );

  if (!hasChart) {
    return sampleCard;
  }

  return (
    <WidgetCard>
      {sampleCard}
      <Box sx={{ mt: 'var(--ds-space-3)' }}>
        <BarChart
          data={query?.values}
          labels={query?.timestamps?.map((e) => convertNumberToTimestamp(e * 1000))}
          chartLabel={'Count'}
          style={{ width: '100%' }}
        />
      </Box>
    </WidgetCard>
  );
};
KubernetesLogPatternDetails.propTypes = {
  query: PropTypes.object,
};

const KubernetesPlusMinusLogs = ({ accountId, query }) => {
  const plusMinusTime = formatDateForPlusMinusDuration(new Date(query.data.timestamp).getTime(), 5);
  return (
    <KubernetesLogs
      showPicker={false}
      showDateFilter={false}
      showQueryTextBox={false}
      showTrend={false}
      accountId={accountId}
      queryFromProps={query.logQuery}
      dateTime={{
        endTime: plusMinusTime.datePlusMinutes,
        startTime: plusMinusTime.dateMinusMinutes,
      }}
      showPlusMinusTab={false}
      showPolling={false}
    />
  );
};
KubernetesPlusMinusLogs.propTypes = {
  query: PropTypes.object,
  accountId: PropTypes.any,
};

const KubernetesPlusMinusLogsFromPrometheus = ({ accountId, query }) => {
  const [logQuery, setLogQuery] = useState('');
  const [plusMinusTime, setPlusMinusTime] = useState({});

  useEffect(() => {
    const time = '1:h';
    const clusterIp = query?.metric?.destination?.split(':')[0];
    const calculateInterval = calculateTimeRange(time);
    const requestBody = {
      accountId: accountId,
      metrics: ['service_info_by_cluster_ip'],
      startDate: calculateInterval.startTime,
      endDate: calculateInterval.endTime,
      internalIp: clusterIp,
      kind: 'workload',
      step: time.replace(':', '').toString(),
    };
    apiKubernetes1.utilisationApi(requestBody).then((res) => {
      const seriesListResult = res?.[0]?.payload || [];
      if (seriesListResult.length) {
        const service = seriesListResult[0].metric?.service;
        const namespace = seriesListResult[0].metric?.namespace;
        const plusMinusTime = formatDateForPlusMinusDuration(seriesListResult[0].timestamps[1], 5);
        setLogQuery(`{"namespaceName": "${namespace}", "workloadName": "${service}"}`);
        setPlusMinusTime(plusMinusTime);
      } else {
        setLogQuery('');
      }
    });
  }, []);

  return (
    <>
      {logQuery && plusMinusTime ? (
        <KubernetesLogs
          showPicker={false}
          showDateFilter={false}
          showPolling={false}
          showQueryTextBox={false}
          showTrend={false}
          accountId={accountId}
          queryFromProps={logQuery}
          dateTime={{
            startTime: plusMinusTime.dateMinusMinutes,
            endTime: plusMinusTime.datePlusMinutes,
          }}
          showPlusMinusTab={false}
        />
      ) : (
        <Typography>No Logs</Typography>
      )}
    </>
  );
};
KubernetesPlusMinusLogsFromPrometheus.propTypes = {
  query: PropTypes.object,
  accountId: PropTypes.any,
};

const renderDiffData = (row) => {
  const dataString = row?.evidences;
  if (dataString) {
    const parsedData = dataString;
    const diff = parsedData.filter((item) => item.type === 'diff');
    if (diff.length > 0) {
      return (
        <Box
          sx={{
            borderRadius: 'var(--ds-radius-md)',
            border: '1px solid var(--ds-gray-200)',
            backgroundColor: 'var(--ds-background-100)',
            marginTop: 'var(--ds-space-1)',
            marginBottom: 'var(--ds-space-2)',
          }}
        >
          <CodeMirrorDiffViewer originalCode={diff[0]?.data?.old} newCode={diff[0]?.data?.new} />
        </Box>
      );
    }
    return (
      <Typography sx={{ mt: 'var(--ds-space-2)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}>
        No diff available.
      </Typography>
    );
  }
};

const DiffViewer = ({ query }) => {
  const [diffData, setDiffData] = useState({});
  useEffect(() => {
    async function loadData(id) {
      const response = await k8sApi.resolveEventRecord(id);
      const _result = response?.data?.events;
      setDiffData(_result[0]);
    }
    if (query?.id) {
      loadData(query?.id);
    }
  }, [query?.id]);
  return renderDiffData(diffData);
};

export const KubernetesSecurityDrilldown = ({ query }) => {
  let workloadMeta = query?.workloadMeta;
  let images = workloadMeta?.config?.containers?.map((container) => container.image) ?? [];
  if (images.length == 0) {
    return <>No Data</>;
  }
  return (
    <KubernetesSecurity
      workload_name={query.workloadName || query.workload_name}
      kubernetes={{
        id: query?.accountId,
      }}
      enableFilters={['status', 'severity']}
      filters={{
        image: images[0],
      }}
      disableInfographic={true}
      heading={''}
    />
  );
};

KubernetesSecurityDrilldown.propTypes = {
  query: PropTypes.object,
};

const KubernetesSLOConfig = ({ query }) => {
  const [availability, setAvailability] = useState({});
  const [latency, setLatency] = useState({});
  const [isSLOReportExists, setIsSLOReportExists] = useState(false);

  useEffect(() => {
    apiKubernetes1
      .getSLOReport({
        workload_namespace: query.namespaceName,
        workload_name: query.workloadName,
        start_date: new Date(new Date().getTime() - 24 * 60 * 60 * 1000).toISOString(),
        end_date: new Date().toISOString(),
      })
      .then((res) => {
        const reportData = res?.data?.data?.slo_report || [];
        if (reportData && reportData.length > 0) {
          setIsSLOReportExists(true);
          const firstTwoReports = reportData.slice(0, 2);
          for (const element of firstTwoReports) {
            const sloConfig = element.slo_config;
            if (sloConfig && sloConfig.name == 'availability') {
              setAvailability({
                burnRate: element.error_budget_burn_rate,
                goal: sloConfig.goal,
                window: sloConfig.window,
                status: element.status,
              });
            } else if (sloConfig && sloConfig.name == 'latency') {
              setLatency({
                burnRate: element.error_budget_burn_rate,
                goal: sloConfig.goal,
                threshold: sloConfig.threshold,
                window: sloConfig.window,
                status: element.status,
              });
            }
          }
        }
      });
  }, []);

  return (
    <>
      {!isSLOReportExists ? (
        <Typography>Either No SLO is Configured Or SLO is NOT yet triggered</Typography>
      ) : (
        <>
          {availability && Object.keys(availability).length > 0 ? (
            <>
              <div style={{ display: 'flex', alignItems: 'center' }}>
                <SeverityIcon level={toSeverityLevel(availability.status)} />
                <Typography sx={{ paddingLeft: 'var(--ds-space-1)' }}>
                  Availability: error budget burn rate is {availability.burnRate}x within {availability.window / 3600} hour
                </Typography>
              </div>
              <Typography sx={{ marginLeft: 'var(--ds-space-5)', color: 'var(--ds-green-600)' }}>
                Condition: the successful request percentage &lt; {availability.goal}%
              </Typography>
            </>
          ) : null}
          {latency && Object.keys(latency).length > 0 ? (
            <>
              <div style={{ display: 'flex', alignItems: 'center' }}>
                <SeverityIcon level={toSeverityLevel(latency.status)} />
                <Typography sx={{ paddingLeft: 'var(--ds-space-1)' }}>
                  Latency: error budget burn rate is {latency.burnRate}x within {latency.window / 3600} hour
                </Typography>
              </div>
              <Typography sx={{ marginLeft: 'var(--ds-space-5)', color: 'var(--ds-green-600)' }}>
                Condition: the percentage of requests served faster than {latency.threshold}ms &lt; {latency.goal}%
              </Typography>
            </>
          ) : null}
        </>
      )}
    </>
  );
};

KubernetesSLOConfig.propTypes = {
  query: PropTypes.object,
};

export const KubernetesNetwork = ({ accountId, query }) => {
  const [showLoading, setShowLoading] = useState(false);
  const [data, setData] = useState([]);
  const [labels, setLabels] = useState([]);
  const [dateTimeRange, setDateTimeRange] = useState({
    startDate: getSpecificTime(60),
    endDate: new Date().getTime(),
  });

  const fetchData = async () => {
    setShowLoading(true);
    setData([]);
    setLabels([]);

    try {
      const response = await apiKubernetes1.utilisationApi({
        ...query,
        accountId,
        startDate: dateTimeRange.startDate,
        endDate: dateTimeRange.endDate,
        metrics: ['network_receive_packet', 'network_transmit_packets'],
      });

      const getSeriesData = (key) => {
        const found = response.find((r) => r.query_key === key);
        return found?.payload?.[0] || null;
      };

      const rxPayload = getSeriesData('network_receive_packet');
      const txPayload = getSeriesData('network_transmit_packets');
      const rawTimestamps = rxPayload?.timestamps || txPayload?.timestamps || [];

      if (rawTimestamps.length > 0) {
        const formattedLabels = rawTimestamps.map((ts) => convertNumberToTimestamp(ts * 1000));
        setLabels(formattedLabels);
      }
      const newDataset = [];
      if (rxPayload?.values?.length) {
        newDataset.push({
          label: 'Received',
          borderWidth: 1,
          data: rxPayload.values.map((v) => v / (1024 * 1024)),
          borderColor: 'orange',
          backgroundColor: 'white',
          pointRadius: 0,
        });
      }

      if (txPayload?.values?.length) {
        newDataset.push({
          label: 'Transmitted',
          borderWidth: 1,
          data: txPayload.values.map((v) => v / (1024 * 1024)),
          borderColor: 'red',
          backgroundColor: 'white',
          pointRadius: 0,
        });
      }

      setData(newDataset);
    } catch (error) {
      console.error('Error fetching Kubernetes network metrics:', error);
    } finally {
      setShowLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, [dateTimeRange, query, accountId]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='network-chart'>
      <ListingLayout.Toolbar
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: dateTimeRange.startDate,
                endTime: dateTimeRange.endDate,
              }}
              onChange={({ selection }) => handleDateRangeChange(selection)}
            />
            <DsButton
              tone='secondary'
              size='sm'
              composition='icon-only'
              icon={<RefreshIcon />}
              aria-label='Refresh'
              tooltip='Refresh'
              onClick={() => void fetchData()}
              loading={showLoading}
            />
          </>
        }
      />
      <ListingLayout.Body>
        <Grid
          container
          spacing={ds.space[4]}
          pt={ds.space.mul(0, 10)}
          mb={ds.space.mul(0, 10)}
          sx={{
            borderRadius: '0 0 var(--ds-radius-lg) var(--ds-radius-lg)',
          }}
        >
          <Grid item xs={12}>
            <Grid container sx={chartContainerStyle}>
              <Grid item xs={12}>
                <LineChart
                  chartTitle='Network - Bandwidth (MB)'
                  dataset={data}
                  labels={labels}
                  chartLabel={['Received', 'Transmitted']}
                  loading={showLoading}
                />
              </Grid>
            </Grid>
          </Grid>
        </Grid>
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesNetwork.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
};

export const KubernetesDiskTrend = ({ accountId, query }) => {
  const [showLoading, setShowLoading] = useState(false);
  const [data, setData] = useState([]);
  const [labels, setLabels] = useState([]);
  const [dateTimeRange, setDateTimeRange] = useState({
    startDate: getSpecificTime(60),
    endDate: new Date().getTime(),
  });

  const fetchData = async () => {
    setShowLoading(true);
    setData([]);
    setLabels([]);

    try {
      const response = await apiKubernetes1.utilisationApi({
        ...query,
        accountId,
        startDate: dateTimeRange.startDate,
        endDate: dateTimeRange.endDate,
        metrics: ['disk_used', 'disk_total'],
      });

      const getSeriesData = (key) => {
        const found = response.find((r) => r.query_key === key);
        return found?.payload?.[0] || null;
      };

      const diskUsedPayload = getSeriesData('disk_used');
      const diskTotalPayload = getSeriesData('disk_total');
      const rawTimestamps = diskUsedPayload?.timestamps || diskTotalPayload?.timestamps || [];

      if (rawTimestamps.length > 0) {
        const formattedLabels = rawTimestamps.map((ts) => convertNumberToTimestamp(ts * 1000));
        setLabels(formattedLabels);
      }
      const newDataset = [];
      if (diskUsedPayload?.values?.length) {
        newDataset.push({
          label: 'Disk Used',
          borderWidth: 1,
          data: diskUsedPayload.values.map((d) => d / (1024 * 1024 * 1024)),
          borderColor: 'orange',
          backgroundColor: 'white',
          pointRadius: 0,
        });
      }

      if (diskTotalPayload?.values?.length) {
        newDataset.push({
          label: 'Disk Total',
          borderWidth: 1,
          data: diskTotalPayload.values.map((d) => d / (1024 * 1024 * 1024)),
          borderColor: 'red',
          backgroundColor: 'white',
          pointRadius: 0,
        });
      }

      setData(newDataset);
    } catch (error) {
      console.error('Error fetching Kubernetes disk trend metrics:', error);
    } finally {
      setShowLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, [dateTimeRange, query, accountId]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='disk-trend-chart'>
      <ListingLayout.Toolbar
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: dateTimeRange.startDate,
                endTime: dateTimeRange.endDate,
              }}
              onChange={({ selection }) => handleDateRangeChange(selection)}
            />
            <DsButton
              tone='secondary'
              size='sm'
              composition='icon-only'
              icon={<RefreshIcon />}
              aria-label='Refresh'
              tooltip='Refresh'
              onClick={() => void fetchData()}
              loading={showLoading}
            />
          </>
        }
      />
      <ListingLayout.Body>
        <Grid
          container
          spacing={ds.space[4]}
          pt={ds.space.mul(0, 10)}
          mb={ds.space.mul(0, 10)}
          sx={{
            borderRadius: '0 0 var(--ds-radius-lg) var(--ds-radius-lg)',
          }}
        >
          <Grid item xs={12}>
            <Grid container sx={chartContainerStyle}>
              <Grid item xs={12}>
                <LineChart
                  chartTitle='Disk Utilization (GB)'
                  dataset={data}
                  labels={labels}
                  chartLabel={['Disk Used', 'Disk Total']}
                  loading={showLoading}
                />
              </Grid>
            </Grid>
          </Grid>
        </Grid>
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesDiskTrend.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
};

export const KubernetesPVCUtilization = ({ accountId, query, heading }) => {
  const [showLoading, setShowLoading] = useState(false);
  const [data, setData] = useState([]);
  const [labels, setLabels] = useState([]);
  const [dateTimeRange, setDateTimeRange] = useState({
    startDate: getLast30Days().getTime(),
    endDate: new Date().getTime(),
  });

  useEffect(() => {
    const fetchData = async () => {
      try {
        setShowLoading(true);
        setData([]);

        const response = await apiKubernetes1.utilisationApi({
          ...query,
          accountId: query?.recommendation?.account_id || accountId,
          startDate: dateTimeRange.startDate,
          endDate: dateTimeRange.endDate,
          metrics: ['pvc_usage', 'pvc_requests'],
        });

        const getSeriesData = (key) => {
          const found = response.find((r) => r.query_key === key);
          return found?.payload?.[0] || null;
        };

        const pvcUsage = getSeriesData('pvc_usage');
        const pvcRequest = getSeriesData('pvc_requests');
        const rawTimestamps = pvcUsage?.timestamps || pvcRequest?.timestamps || [];

        if (rawTimestamps.length > 0) {
          const formattedLabels = rawTimestamps.map((ts) => convertNumberToTimestamp(ts * 1000));
          setLabels(formattedLabels);
        }

        const newDataset = [];

        if (pvcUsage?.values?.length) {
          newDataset.push({
            label: 'PVC Usage',
            borderWidth: 1,
            data: pvcUsage.values.map((v) => v / (1024 * 1024 * 1024)),
            borderColor: 'orange',
            backgroundColor: 'white',
            pointRadius: 0,
          });
        }

        if (pvcRequest?.values?.length) {
          newDataset.push({
            label: 'PVC Request',
            borderWidth: 1,
            data: pvcRequest.values.map((v) => v / (1024 * 1024 * 1024)),
            borderColor: 'red',
            backgroundColor: 'white',
            pointRadius: 0,
          });
        }

        setData(newDataset);
      } catch (error) {
        console.error(error);
      } finally {
        setShowLoading(false);
      }
    };

    fetchData();
  }, [dateTimeRange]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='pvc-chart'>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: dateTimeRange.startDate,
              endTime: dateTimeRange.endDate,
            }}
            onChange={({ selection }) => handleDateRangeChange(selection)}
          />
        }
      />
      <ListingLayout.Body>
        <LineChart
          chartTitle='PVC - Utilization (GB)'
          dataset={data}
          labels={labels}
          colors={['#5b9eff', '#f5b400']}
          chartLabel={['Request', 'Usage']}
          loading={showLoading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesPVCUtilization.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
  heading: PropTypes.string,
};

export const KubernetesNodeStorageUtilization = ({ accountId, query, heading }) => {
  const [showLoading, setShowLoading] = useState(false);
  const [data, setData] = useState([]);
  const [labels, setLabels] = useState([]);
  const [dateTimeRange, setDateTimeRange] = useState({
    startDate: getLast30Days().getTime(),
    endDate: new Date().getTime(),
  });

  useEffect(() => {
    setShowLoading(true);
    const requestBody = {
      account_id: accountId,
      startDate: dateTimeRange?.startDate,
      endDate: dateTimeRange?.endDate,
      metrics: ['pvc_usage'],
      nodeIp: query.nodeIp,
      nodeName: query.nodeName,
    };
    setData([]);
    apiKubernetes1
      .utilisationApi(requestBody)
      .then((response) => {
        const pvcUsage = response?.find((data) => data.query_key === 'pvc_usage')?.payload || [];
        let data = [];
        if (pvcUsage.length > 0) {
          const timestampsLabel = pvcUsage[0].timestamps.map((e) => timeFormatIn24HoursCompact(e * 1000));
          const values = pvcUsage[0].values.map((g) => g);
          data.push({
            label: 'Storage Usage',
            borderWidth: 1,
            data: values,
            borderColor: 'gray',
            backgroundColor: 'white',
            pointRadius: 0,
          });
          setLabels(timestampsLabel);
        }
        setData(data);
      })
      .finally(() => {
        setShowLoading(false);
      });
  }, [dateTimeRange]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='node-storage-chart'>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: dateTimeRange.startDate,
              endTime: dateTimeRange.endDate,
            }}
            onChange={({ selection }) => handleDateRangeChange(selection)}
          />
        }
      />
      <ListingLayout.Body>
        <LineChart
          chartTitle='Node Disk - Utilization (%)'
          dataset={data}
          labels={labels}
          chartLabel={['Usage']}
          colors={['#f5b400']}
          loading={showLoading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesNodeStorageUtilization.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
  heading: PropTypes.string,
};

export const KubernetesRequestResponseTrend = ({ accountId, query = {}, selectedDateRange, additionalFilters = [], heading = '' }) => {
  const [httpStatusData, setHttpStatusData] = useState({ data: [], labels: [] });
  const [httpResponseTimeData, setHttpResponseTimeData] = useState({ data: [], labels: [] });
  const [dateTimeRange, setDateTimeRange] = useState(
    selectedDateRange ?? {
      startDate: Date.now() - 1 * 3600 * 1000,
      endDate: Date.now(),
    }
  );
  const [showLoading, setShowLoading] = useState(false);

  const fetchData = async () => {
    setShowLoading(true);
    // Reset data before fetching
    setHttpStatusData({ data: [], labels: [], chartLabels: [] });
    setHttpResponseTimeData({ data: [], labels: [], chartLabels: [] });

    try {
      const response = await apiKubernetes1.utilisationApi({
        ...query,
        startDate: dateTimeRange.startDate,
        endDate: dateTimeRange.endDate,
        metrics: ['http_status', 'http_max_response_time'],
      });

      const processMetricData = (key, labelGenerator) => {
        const metricResult = response.find((r) => r.query_key === key);
        const payload = metricResult?.payload || [];

        if (!payload.length) {
          return { data: [], labels: [], chartLabels: [] };
        }

        const labels = payload[0].timestamps.map((ts) => convertNumberToTimestamp(ts * 1000));
        const data = payload.map((item) => item.values);
        const chartLabels = payload.map((item) => labelGenerator(item.metric));

        return { labels, data, chartLabels };
      };

      const statusProcessed = processMetricData('http_status', (metric) => (metric.status ? `Status ${metric.status}` : 'Unknown Status'));
      setHttpStatusData(statusProcessed);

      const responseTimeProcessed = processMetricData(
        'http_max_response_time',
        (metric) => metric.actual_destination_workload_namespace || 'Max Response Time'
      );
      setHttpResponseTimeData(responseTimeProcessed);
    } catch (error) {
      console.error('Error fetching Kubernetes pod groupings:', error);
    } finally {
      setShowLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, [accountId, query, dateTimeRange.startDate, dateTimeRange.endDate]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='box-request-response-charts'>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: dateTimeRange.startDate,
                endTime: dateTimeRange.endDate,
              }}
              onChange={({ selection }) => handleDateRangeChange(selection)}
            />
            <DsButton
              tone='secondary'
              size='sm'
              composition='icon-only'
              icon={<RefreshIcon />}
              aria-label='Refresh'
              tooltip='Refresh'
              onClick={fetchData}
              loading={showLoading}
            />
          </>
        }
      >
        {additionalFilters}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <Grid
          container
          spacing={ds.space[4]}
          pt={ds.space.mul(0, 10)}
          mb={ds.space.mul(0, 10)}
          sx={{
            borderRadius: '0 0 var(--ds-radius-lg) var(--ds-radius-lg)',
          }}
        >
          <Grid item xs={12}>
            <Grid container sx={chartContainerStyle}>
              <Grid item xs={12} md={6}>
                <LineChart
                  colors={['#f5b400', '#5b9eff', '#e5484d']}
                  data={httpStatusData.data}
                  labels={httpStatusData.labels}
                  chartLabel={httpStatusData.chartLabels ?? ['Http Status']}
                  loading={showLoading}
                />
              </Grid>
              <Grid item xs={12} md={6}>
                <LineChart
                  colors={['#f5b400', '#5b9eff', '#e5484d']}
                  data={httpResponseTimeData.data}
                  labels={httpResponseTimeData.labels}
                  chartLabel={['Http Max Response Time']}
                  loading={showLoading}
                />
              </Grid>
            </Grid>
          </Grid>
        </Grid>
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesRequestResponseTrend.propTypes = {
  row: PropTypes.any,
  accountId: PropTypes.string,
  query: PropTypes.any,
  selectedDateRange: PropTypes.any,
  additionalFilters: PropTypes.array,
  heading: PropTypes.string,
};

export const KubernetesUtilizationCharts3 = ({ accountId, query }) => {
  return (
    <>
      <Box mb={ds.space[2]} />
      <KubernetesUtilizationCharts2
        accountId={accountId}
        query={{
          ...query,
          metrics: ['cpu_usage', 'memory_usage', 'cpu_limit', 'cpu_request', 'memory_limit', 'memory_request'],
        }}
      />
      <Title title={'Request & Latency'} />
      <Box mb={ds.space[2]} />
      <KubernetesRequestResponseTrend accountId={accountId} query={query} />
      <Title title={'Network'} />
      <Box mb={ds.space[2]} />
      <KubernetesNetwork accountId={accountId} query={query} />
    </>
  );
};

KubernetesUtilizationCharts3.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
};

export const KubernetesReplicaTrend = ({ accountId, query, heading }) => {
  const [showLoading, setShowLoading] = useState(false);
  const [data, setData] = useState([]);
  const [labels, setLabels] = useState([]);
  const [dateTimeRange, setDateTimeRange] = useState({
    startDate: getLast30Days().getTime(),
    endDate: new Date().getTime(),
  });

  useEffect(() => {
    setShowLoading(true);
    const requestBody = {
      account_id: accountId,
      startDate: dateTimeRange?.startDate,
      endDate: dateTimeRange?.endDate,
      metrics: ['replica_defined', 'replica_ready'],
      workloadType: 'workload',
      namespace_name: query.subject_namespace,
      workload_name: query.controller,
    };
    setData([]);
    apiKubernetes1
      .utilisationApi(requestBody)
      .then((response) => {
        const replicaDefined = response?.find((data) => data.query_key === 'replica_defined')?.payload || [];
        const replicaReady = response?.find((data) => data.query_key === 'replica_ready')?.payload || [];
        let data = [];
        if (replicaDefined.length > 0) {
          const timestampsLabel = replicaDefined[0].timestamps.map((e) => timeFormatIn24HoursCompact(e * 1000));
          data.push({
            label: 'Replica Defined',
            borderWidth: 1,
            data: replicaDefined[0].values,
            backgroundColor: 'white',
            borderColor: '#5b9eff',
            pointRadius: 0,
          });
          data.push({
            label: 'Replica Ready',
            borderWidth: 1,
            data: replicaReady[0].values,
            backgroundColor: 'white',
            borderColor: '#f5b400',
            pointRadius: 0,
          });
          setLabels(timestampsLabel);
        }
        setData(data);
      })
      .finally(() => {
        setShowLoading(false);
      });
  }, [dateTimeRange]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    updateDateRange(setDateTimeRange, passedSelectedDateTime);
  };

  return (
    <ListingLayout id='replica_trend'>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: dateTimeRange.startDate,
              endTime: dateTimeRange.endDate,
            }}
            onChange={({ selection }) => handleDateRangeChange(selection)}
          />
        }
      />
      <ListingLayout.Body>
        <LineChart
          chartTitle='Replica Over Time'
          dataset={data}
          labels={labels}
          chartLabel={['Replicas']}
          colors={['#f5b400']}
          loading={showLoading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesReplicaTrend.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
  heading: PropTypes.string,
};

export const KubernetesPodProfilerHistory = ({ accountId, query }) => {
  const [showLoading, setShowLoading] = useState(false);
  const [data, setData] = useState([]);

  const [totalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [analysisQuery, setAnalysisQuery] = useState('');
  const [analysisType, setAnalysisType] = useState('');
  const [sessionId, setSessionId] = useState('');
  const [analysisModalOpen, setAnalysisModalOpen] = useState(false);

  const onPageChange = (page, _limit) => {
    setCurrentPage(page - 1);
    //setRecordsPerPage(limit);
  };

  const findPySpyCmdReplaceWithPid = (data) => {
    const regex = /py-spy[^\n]*\.svg/g;
    const matches = data.match(regex);

    if (!matches?.length) {
      return data;
    }

    let _result = data;
    for (const match of matches) {
      const pidMatch = match.match(/--pid=(\d+)/);
      if (pidMatch?.[1]) {
        _result = _result.replace(match, 'Process Id: ' + pidMatch[1]);
      }
    }
    return _result;
  };

  const base64Converter = (data) => {
    const cleanData = data.replace(/^b'|'$/g, '');
    return Buffer.from(cleanData, 'base64');
  };

  const unzipData = async (gzData) => {
    return new Promise((resolve, reject) => {
      zlib.unzip(gzData, (err, unzippedBuffer) => {
        if (err) {
          console.error('Error unzipping the file:', err);
          reject(err);
        } else {
          resolve(unzippedBuffer.toString('utf8').replace(/\n$/, ''));
        }
      });
    });
  };

  const handleGenerateAnalysis = (type, data) => {
    let queryPrompt = '';

    // Truncate the data to a reasonable size for LLM analysis
    const maxDataLength = 100000;
    const truncatedData = data.data?.length > maxDataLength ? data.data.substring(0, maxDataLength) + '... [truncated]' : data.data;

    switch (type) {
      case 'threaddump':
        queryPrompt = `@llm Analyse this thread dump on pod ${query?.pod_name} and namespace ${query?.namespace_name}:\n\n${truncatedData}`;
        break;
      case 'heapdump':
        queryPrompt = `@llm Analyse this heap dump on pod ${query?.pod_name} and namespace ${query?.namespace_name}:\n\n${truncatedData}`;
        break;
      default:
        queryPrompt = `@llm Analyse this profiler data on pod ${query?.pod_name} and namespace ${query?.namespace_name}:\n\n${truncatedData}`;
    }

    setAnalysisQuery(queryPrompt);
    setAnalysisType(type);
    setSessionId(generateRandomUUID(`${query.pod_name}-${type}`));
    setAnalysisModalOpen(true);
  };

  useEffect(() => {
    const fetchData = async () => {
      setShowLoading(true);
      setData([]);
      try {
        const res = await apiKubernetes1.applicationProfileHistory({
          accountId: accountId,
          podName: query.podName,
          workloadName: query.workloadName,
          namespace: query.namespaceName,
        });

        const rows = res?.data?.data?.applications_list_profiles?.rows || [];

        const data = await Promise.all(
          rows.map(async (ap) => {
            let fileObject = {};
            let analysisType = '';
            let showNubeIcon = false;
            try {
              const dataObjects = JSON.parse(ap.profile);

              // Process SVG data
              const svgItems = dataObjects.filter((item) => item.type === 'svg');
              for (const item of svgItems) {
                fileObject = {
                  fileName: item.filename,
                  downloadableData: findPySpyCmdReplaceWithPid(atob(item.data.replace(/^b'|'$/g, ''))),
                };
              }

              // Process SVG.GZ data
              const svgGzItems = dataObjects.filter(
                (item) => item.type === 'gz' && (item.filename.endsWith('svg.gz') || item.filename.endsWith('pprof.svg.gz'))
              );
              if (svgGzItems.length) {
                const gzData = base64Converter(svgGzItems[0].data);
                const unzippedData = await unzipData(gzData);
                fileObject = {
                  fileName: `${ap.pod_name}.svg`,
                  downloadableData: unzippedData,
                };
              }

              // Process TXT.GZ data
              const txtGzItems = dataObjects.filter((item) => item.type === 'gz' && item.filename.endsWith('txt.gz'));
              if (txtGzItems.length) {
                const gzData = base64Converter(txtGzItems[0].data);
                const txtData = await unzipData(gzData);
                if (txtGzItems[0].filename.toLowerCase().includes('heap')) {
                  analysisType = 'heapdump';
                  showNubeIcon = true;
                } else if (txtGzItems[0].filename.toLowerCase().includes('thread')) {
                  analysisType = 'threaddump';
                  showNubeIcon = true;
                }
                fileObject = {
                  fileName: `${ap.pod_name}.txt`,
                  downloadableData: txtData,
                };
              }

              // Process JFR.GZ data
              const jfrGzItems = dataObjects.filter((item) => item.type === 'gz' && item.filename.endsWith('jfr.gz'));
              if (jfrGzItems.length) {
                const gzData = base64Converter(jfrGzItems[0].data);
                const jfrData = await unzipData(gzData);
                fileObject = {
                  fileName: `${ap.pod_name}.jfr`,
                  downloadableData: jfrData,
                };
              }

              // Process PPROF.GZ data
              const pprofGzItems = dataObjects.filter((item) => item.type === 'gz' && item.filename.endsWith('pprof.gz'));
              if (pprofGzItems.length) {
                const gzData = base64Converter(pprofGzItems[0].data);
                fileObject = {
                  fileName: `${ap.pod_name}.pprof.gz`,
                  downloadableData: gzData,
                };
              }
            } catch (error) {
              console.error('Error processing evidence data:', error);
            }

            return [
              { text: ap.pod_name },
              { text: ap.namespace },
              {
                component: (
                  <DownloadButton
                    onClick={() => ({
                      fileName: fileObject.fileName,
                      data: fileObject.downloadableData,
                    })}
                    id={'pod-profiler-1'}
                  />
                ),
              },
              {
                component: <Datetime value={ap.updated_at} />,
              },
              {
                text: ap.profile_duration ? (
                  <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--ds-space-1)' }}>{ap.profile_duration}</Box>
                ) : (
                  '-'
                ),
              },
              {
                text: ap.profile_type || ap.output_type || '-',
              },
              {
                text: ap.profile_language,
              },
              {
                component: showNubeIcon && (
                  <Tooltip title={`Ask ${DEFAULT_TITLE} for analysis`}>
                    <Box sx={{ display: 'inline-flex', mr: ds.space[2] }}>
                      <DsButton
                        tone='secondary'
                        size='md'
                        composition='icon-only'
                        aria-label={`Ask ${DEFAULT_TITLE}`}
                        onClick={(e) => {
                          e.stopPropagation();
                          handleGenerateAnalysis(analysisType, { data: fileObject.downloadableData });
                        }}
                        icon={<SafeIcon src={getNubiIconUrl()} width={24} height={24} alt={`Ask ${DEFAULT_TITLE}`} />}
                      />
                    </Box>
                  </Tooltip>
                ),
              },
            ];
          })
        );

        setData(data);
      } catch (error) {
        console.error('Error fetching application profile history:', error);
      } finally {
        setShowLoading(false);
      }
    };

    fetchData();
  }, [accountId, JSON.stringify(query)]);

  return (
    <ListingLayout>
      <ListingLayout.Body>
        <ConversationPopup
          open={analysisModalOpen}
          query={analysisQuery}
          sessionId={sessionId}
          accountId={accountId}
          handleClose={() => {
            setAnalysisQuery('');
            setSessionId('');
            setAnalysisModalOpen(false);
          }}
          title={analysisType ? `${convertStringCase(analysisType)} Analysis` : 'Analysis'}
        />
        <CustomTable2
          id={'k8s-profiler'}
          totalRows={totalCount}
          headers={['Pod Name', 'Namespace', 'Profile', 'Created At', 'Duration (sec)', 'Profile Type', 'Language', '']}
          rowsPerPage={data.length}
          onPageChange={onPageChange}
          pageNumber={currentPage + 1}
          tableData={data}
          loading={showLoading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesPodProfilerHistory.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.shape({
    podName: PropTypes.string,
    workloadName: PropTypes.string,
    namespaceName: PropTypes.string,
  }),
};

/**
 * @param {{
 *   data?: any[],
 *   headers?: any[],
 *   upperHeaders?: any[],
 *   expandable?: object,
 *   rowsPerPage?: number,
 *   totalRows?: number,
 *   onPageChange?: (page: number, limit: number) => void,
 *   sort?: object,
 *   onSortChange?: any,
 *   id?: string,
 *   showExpandable?: boolean,
 *   loading?: boolean,
 *   errorMessage?: string,
 *   pageNumber?: number,
 *   selectedDateRange?: object,
 *   rounded?: any,
 *   borderColor?: string,
 *   minWidth?: string | number,
 *   timeStampMinWidth?: boolean,
 *   tableHeadingCenter?: any[],
 *   stickyColumnIndex?: string,
 *   showUpdatedEmptyData?: boolean,
 *   onRowClick?: (data: any) => void,
 *   tabPadding?: string,
 * }} props
 */
const KubernetesTable2 = ({
  data = [],
  headers = [],
  upperHeaders = [],
  expandable = {},
  rowsPerPage,
  totalRows,
  onPageChange,
  sort = {},
  onSortChange,
  id = '',
  showExpandable = false,
  loading = false,
  errorMessage = '',
  pageNumber = 1,
  selectedDateRange = {},
  rounded,
  borderColor = 'var(--ds-gray-200)',
  minWidth,
  timeStampMinWidth = false,
  tableHeadingCenter = [],
  stickyColumnIndex = '',
  showUpdatedEmptyData = false,
  onRowClick,
  tabPadding,
  resizableColumns = false,
  resetPage = '',
  disableDateFilterForPodsTable = false,
}) => {
  const router = useRouter();
  const [accountId, setAccountId] = useState(router.query.KubernetesDetails || router.query.accountId);
  const [requiredTabs, setRequiredTabs] = useState(expandable || {});

  // Ticket creation popup state - lifted here so it persists across expandable row re-renders
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Title**: ' + (data?.title || '') + '\n';
    description += '**Priority**: ' + (data?.priority || '') + '\n';
    description += '**Aggregation Key**: ' + (data?.aggregation_key || '') + '\n';
    description += '**Subject Type**: ' + (data?.subject_type || '') + '\n';
    description += '**Subject Name**: ' + (data?.subject_name || '') + '\n';
    description += '**Subject Namespace**: ' + (data?.subject_namespace || '') + '\n';
    return description;
  };

  const handleOpenTicketForm = (eventData, eventAccountId) => {
    setTicketData({ ...eventData, accountId: eventData?.account_id || eventAccountId });
    setIsTicketCreateFormOpen(true);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
    setTicketData({});
  };

  const handleTicketSuccess = () => {
    closeTicketCreateForm();
  };

  const handleTicketFailure = (error) => {
    console.error('Failed to create ticket:', error);
  };

  useEffect(() => {
    setAccountId(router.query.KubernetesDetails || router.query.accountId);
  }, [router.query.KubernetesDetails, router.query.accountId]);

  function expandedComponentFn(option, query, row) {
    const componentMap = {
      pods: () => (
        <KubernetesPodsTable
          accountId={accountId}
          recordsPerPage={5}
          defaultQuery={query}
          enableFilters={false}
          disableDateFilterForPodsTable={disableDateFilterForPodsTable}
        />
      ),
      utilization: () => <KubernetesUtilizationCharts accountId={accountId} query={query} />,
      cost: () => <KubernetesCostCharts row={row} accountId={accountId} query={query} selectedDateRange={selectedDateRange} />,
      eventEvidence: () => <KubernetesEventEvidences row={row} accountId={accountId} query={query} />,
      eventLog: () => <KubernetesEventLog row={row} accountId={accountId} query={query} />,
      eventUtilization: () => <KubernetesEventUtilization row={row} accountId={accountId} query={query} />,
      events: () => <KubernetesEventsTable row={row} accountId={accountId || query.accountId} defaultQuery={query} enableFilters={false} />,
      utilization3: () => <KubernetesUtilizationCharts3 row={row} accountId={accountId} query={query} />,
      'triage-rule-events': () => <TriageRuleEventsTable query={query} onOpenTicketForm={handleOpenTicketForm} />,
      'events-drilldown-events': () => (
        <KubernetesEventsTable
          row={row}
          accountId={accountId}
          defaultQuery={query}
          enableFilters={false}
          tableColumns={[
            { name: 'Severity', width: '10%' },
            { name: 'Subject Name', width: '20%' },
            { name: 'Message', width: '40%' },
            { name: 'Alert Status', width: '4%' },
            '',
          ]}
        />
      ),
      'events-drilldown-applications': () => (
        <KubernetesEventsTable
          row={row}
          accountId={accountId}
          defaultQuery={query}
          enableFilters={false}
          tableColumns={[
            { name: 'Severity', width: '10%' },
            { name: 'Message', width: '40%' },
            { name: 'Alert Status', width: '4%' },
            { name: 'Error Type', width: '20%' },
            '',
          ]}
        />
      ),
      yaml: () => <KubernetesPodYaml accountId={accountId} query={query} />,
      related_events: () => <KubernetesRelatedEventTable query={query} tableName={'Related Events'} />,
      job_information: () => <KubernetesRelatedEventTable query={query} tableName={'Job information'} />,
      job_events: () => <KubernetesRelatedEventTable query={query} tableName={'Job events'} />,
      job_pod_events: () => <KubernetesRelatedEventTable query={query} tableName={'Job pod events'} />,
      alert_labels: () => <KubernetesRelatedEventTable query={query} tableName={'Alert labels'} />,
      pod_events: () => <KubernetesRelatedEventTable query={query} tableName={'Pod events'} />,
      pod_node_oom_killed: () => <KubernetesRelatedEventTable query={query} tableName={'Pod and Node OOMKilled data'} />,
      'log-details': () => <KubernetesLogDetails query={query} />,
      'logstash-log': () => <KubernetesLogstashDetails query={query} />,
      'log-group-detail': () => <KubernetesLogPatternDetails query={query} />,
      serviceMap: () => (
        <KubernetesServiceMap
          accountId={query.accountId}
          appName={query.workloadName}
          namespaceName={query.namespaceName}
          disableNamespaceFilter={true}
        />
      ),
      'log-plus-minus': () => <KubernetesPlusMinusLogsGradual accountId={accountId} query={query} />,
      'loki-plus-minus-log-from-prometheus': () => <KubernetesPlusMinusLogsFromPrometheus accountId={accountId} query={query} />,
      deployments: () => (
        <KubernetesDeploymentHistory
          accountId={accountId}
          subjectName={query?.subject_name}
          subjectNamespace={query?.subject_namespace}
          subjectType={query?.subject_type ?? query?.subject_kind}
        />
      ),
      'deployment-diff': () => <DiffViewer query={query} />,
      security: () => <KubernetesSecurityDrilldown accountId={accountId} query={query} />,
      slo: () => <KubernetesSLOConfig accountId={accountId} query={query} />,
      network: () => <KubernetesNetwork accountId={accountId} query={query} />,
      pvc_utilization: () => <KubernetesPVCUtilization accountId={accountId} query={query} />,
      'node-storage': () => <KubernetesNodeStorageUtilization accountId={accountId} query={query} />,
      profilers: () => <KubernetesPodProfilerHistory accountId={accountId} query={query} />,
    };

    const renderComponent = componentMap[option.key];
    return renderComponent ? renderComponent() : <>No Data</>;
  }

  function wrapperFunction(fn) {
    return (_option, query, row) => fn(accountId, query, row);
  }

  const checkForTabsWithData = (rowData) => {
    const tabs = [];

    // Configuration for evidence-based tabs
    const evidenceTabConfig = [
      { match: 'Related Events', text: 'Related Events', key: 'related_events' },
      { match: 'Job information', text: 'Job Information', key: 'job_information' },
      { match: 'Job events', text: 'Job Events', key: 'job_events' },
      { match: 'Job pod events', text: 'Job Pod Events', key: 'job_pod_events' },
      { match: 'Pod events', text: 'Pod Events', key: 'pod_events' },
      { match: 'Alert labels', text: 'Alert Labels', key: 'alert_labels' },
      { match: 'Pod and Node OOMKilled data', text: 'Pod and Node OOMKilled data', key: 'pod_node_oom_killed' },
    ];

    // Process evidences if present
    if (rowData?.evidences) {
      const evidencesData = typeof rowData.evidences === 'string' ? JSON.parse(rowData.evidences) : rowData.evidences;

      for (const evidence of evidencesData) {
        if (evidence?.type === 'table') {
          const tableName = evidence?.data?.table_name || '';
          const matchedConfig = evidenceTabConfig.find((config) => tableName.includes(config.match));
          if (matchedConfig) {
            tabs.push({ text: matchedConfig.text, value: tabs.length, key: matchedConfig.key, componentFn: expandedComponentFn });
          }
        } else if (evidence?.type === 'gz') {
          tabs.push({ text: 'Logs', value: tabs.length, key: 'eventLog', componentFn: expandedComponentFn });
        }
      }
    }

    // Process expandable tabs configuration
    if (expandable) {
      const isTabsPropFunction = typeof expandable.tabs === 'function';
      const configuredTabs = isTabsPropFunction ? expandable.tabs(rowData) : expandable.tabs || [];

      for (const tab of configuredTabs) {
        const componentFn = tab.componentFn ? (isTabsPropFunction ? tab.componentFn : wrapperFunction(tab.componentFn)) : expandedComponentFn;
        tabs.push({ text: tab.text, value: tabs.length, key: tab.key, componentFn });
      }
    }

    setRequiredTabs({ tabs });
  };

  return (
    <>
      <CustomTable2
        id={id}
        tableData={data}
        headers={headers}
        upperHeaders={upperHeaders}
        expandable={requiredTabs}
        rowsPerPage={rowsPerPage}
        onPageChange={onPageChange}
        sort={sort}
        onSortChange={onSortChange}
        totalRows={totalRows || data?.length}
        checkForTabsWithData={onRowClick ? undefined : checkForTabsWithData}
        showExpandable={showExpandable}
        loading={loading}
        errorMessage={errorMessage}
        pageNumber={pageNumber}
        rounded={rounded}
        borderColor={borderColor}
        minWidth={minWidth}
        tableHeadingCenter={tableHeadingCenter}
        timeStampMinWidth={timeStampMinWidth}
        stickyColumnIndex={stickyColumnIndex}
        showUpdatedEmptyData={showUpdatedEmptyData}
        onRowClick={onRowClick}
        tabPadding={tabPadding}
        resizableColumns={resizableColumns}
        resetPage={resetPage}
      />
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Event - ' + (ticketData?.title || ''),
          description: getTicketDescription(ticketData),
          accountId: ticketData?.accountId,
        }}
        ticketUrl={{ url: `/investigate?id=${ticketData?.id}&accountId=${ticketData?.accountId}` }}
        reference={{
          id: ticketData?.fingerprint || ticketData?.id,
          type: 'kubernetes',
        }}
      />
    </>
  );
};

KubernetesTable2.propTypes = {
  id: PropTypes.string,
  data: PropTypes.array,
  headers: PropTypes.array,
  upperHeaders: PropTypes.array,
  expandable: PropTypes.object,
  rowsPerPage: PropTypes.number,
  totalRows: PropTypes.number,
  onPageChange: PropTypes.func,
  sort: PropTypes.object,
  onSortChange: PropTypes.any,
  showExpandable: PropTypes.bool,
  loading: PropTypes.bool,
  errorMessage: PropTypes.string,
  pageNumber: PropTypes.number,
  selectedDateRange: PropTypes.object,
  rounded: PropTypes.any,
  minWidth: PropTypes.any,
  timeStampMinWidth: PropTypes.bool,
  borderColor: PropTypes.string,
  stickyColumnIndex: PropTypes.string,
  tableHeadingCenter: PropTypes.array,
  showUpdatedEmptyData: PropTypes.bool,
  onRowClick: PropTypes.func,
  tabPadding: PropTypes.string,
  resizableColumns: PropTypes.bool,
  resetPage: PropTypes.string,
  disableDateFilterForPodsTable: PropTypes.bool,
};

export default KubernetesTable2;
