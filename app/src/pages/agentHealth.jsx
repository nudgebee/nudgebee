import React, { useEffect, useRef, useState } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Button as DsButton } from '@components1/ds/Button';
import { ds } from 'src/utils/colors';
import k8sApi from '@api1/kubernetes';
import Text from '@common-new/format/Text';
import Datetime from '@common-new/format/Datetime';
import { useRouter } from 'next/router';
import { Box, Typography, Stack } from '@mui/material';
import { AgentIconBlue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { hasWriteAccess } from '@lib/auth';
import CustomTable from '@common-new/tables/CustomTable2';
import { useData } from '@context/DataContext';
import CustomTabs from '@common-new/CustomTabs';
import SyncIcon from '@mui/icons-material/Sync';
import { toast as snackbar } from '@components1/ds/Toast';

const HEADERS_K8S = ['Status', 'Agent Version', 'Latest Version', 'Last Connected', 'K8s(Provider/Version)'];
const HEADERS_CLOUD = ['Status', 'Last Connected', 'Cloud', 'Account'];
const HEADERS_PROXY = ['Status', 'Last Connected'];
const HEADERS_PROXY_DATASOURCES = ['Name', 'Type', 'Proxy Type', 'Status', 'Last Check', 'Error'];
const HEADERS_SCHEDULED_JOBS = ['Action Name', 'Job Status', 'Execution Count', 'Last Execution Time', 'Cron'];
const HEADERS_CLOUD_FEATURES = ['Feature', 'Status', 'Last Sync', 'Next Sync', 'Error'];
const LATEST_CF_TEMPLATE_VERSION = '2';

const AgentHealth = () => {
  const { selectedCluster } = useData();
  const [agentHealthData, setAgentHealthData] = useState([]);
  const [agentType, setAgentType] = useState('k8s');
  const [data, setData] = useState([]);
  const [agentFeatures, setAgentFeatures] = useState({
    isRelayConnected: false,
    isPrometheusConnected: false,
    prometheusUrl: '',
    isAlertManagerConnected: false,
    isLogsManagerConnected: false,
    logsProvider: '',
    logsProviderUrl: '',
    isTracesManagerConnected: false,
    tracesUrl: '',
    isOpenCostConnected: false,
    opencostUrl: '',
    isNodeAgentConnected: false,
    nodeAgentCount: 0,
    namespace: '',
    prometheusRetentionTime: 0,

    // cloud accounts related
    isCloudEventsConnected: false,
    cloudEventsLastConnectedAt: '',
    isCloudRecommendationsConnected: false,
    cloudRecommendationsLastConnectedAt: '',
    isCloudResourcesConnected: false,
    cloudResourcesLastConnectedAt: '',
    isCloudSpendsConnected: false,
    cloudSpendLastConnectedAt: '',
    cloudEventsError: '',
    cloudResourcesError: '',
    cloudRecommendationsError: '',
    cloudSpendsError: '',
    prometheusAdditionalLabels: '',
  });
  const [latestVersions, setLatestVersions] = useState({});
  const [shouldUpgrade, setShouldUpgrade] = useState(false);
  const [disconnectedService, setDisconnectedService] = useState([]);
  const [loading, setLoading] = useState(false);
  const [scheduledJobsData, setScheduledJobsData] = useState([]);
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(50);
  const [syncLoading, setSyncLoading] = useState(false);
  const [cfStack, setCfStack] = useState(null);

  // Proxy agent state
  const [proxyData, setProxyData] = useState([]);
  const [proxyAgentHealthData, setProxyAgentHealthData] = useState([]);
  const [proxyLoading, setProxyLoading] = useState(false);
  const [activeTab, setActiveTab] = useState(0);

  const latestVersionsRef = useRef(latestVersions);
  const router = useRouter();

  useEffect(() => {
    k8sApi.getLatestVersions().then((res) => {
      setLatestVersions(res.data?.nb_versions || {});
    });
  }, []);

  useEffect(() => {
    latestVersionsRef.current = latestVersions;
  }, [latestVersions]);

  function isConnectedUsingDate(lastConnectedDateStr) {
    if (!lastConnectedDateStr) {
      return false;
    }
    // if last connected is 2 days ago then mark it disconnected
    let lastConnectedDate = new Date(lastConnectedDateStr);
    return new Date().getTime() - lastConnectedDate.getTime() < 2 * 24 * 3600 * 1000;
  }

  useEffect(() => {
    const accountType = selectedCluster?.cloud_provider || selectedCluster?.type || agentType;

    const query = {
      accountId: router.query.accountId,
      type: accountType,
    };
    setLoading(true);
    k8sApi
      .getAgentHealth(query)
      .then((res) => {
        if (res?.error) {
          return;
        }
        setData(res?.data ?? []);
        let result = res.data;
        let tableData = [];
        let scheduledJobsTableData = [];
        let disconnectedService = [];
        let isAgentActive = false;
        let agentType = 'k8s';

        for (let acc of result || []) {
          agentType = acc.type;
          isAgentActive = acc.status === 'CONNECTED';
          const latestVersionsData = latestVersionsRef.current;

          acc.connection_status = acc.connection_status || {};

          if (agentType === 'k8s') {
            tableData.push([
              { component: <Text value={acc.status.replace('_', ' ') ?? '-'} /> },
              { component: <Text value={acc.version ?? '-'} /> },
              { component: <Text value={latestVersionsData?.agent_version_latest ?? '-'} /> },
              { component: <Datetime value={acc.last_connected_at} /> },
              { text: `${acc.k8s_provider || '-'} / ${acc.k8s_version ?? '-'}` },
            ]);
            if (latestVersionsData?.agent_version_latest && acc.version !== latestVersionsData?.agent_version_latest) {
              setShouldUpgrade(true);
            } else {
              setShouldUpgrade(false);
            }

            const connectionStatus = result?.[0].connection_status;
            if (!connectionStatus?.relayConnection) {
              disconnectedService.push('Relay');
            }
            if (!connectionStatus?.prometheusConnection) {
              disconnectedService.push('Prometheus');
            }
            if (!connectionStatus?.alertManagerConnection) {
              disconnectedService.push('Alert Manager');
            }
            if (!connectionStatus?.logsConnection) {
              disconnectedService.push('Logs');
            }
            if (!connectionStatus?.nodeAgentConnection) {
              disconnectedService.push('NodeAgent');
            }

            setCfStack(null);
            setAgentFeatures({
              isRelayConnected: (isAgentActive && acc.connection_status?.relayConnection) ?? false,
              isPrometheusConnected: (isAgentActive && acc.connection_status?.prometheusConnection) ?? false,
              prometheusUrl: acc.connection_status?.prometheusUrl ?? '',
              isAlertManagerConnected: (isAgentActive && acc.connection_status?.alertManagerConnection) ?? false,
              isLogsManagerConnected: (isAgentActive && acc.connection_status?.logsConnection) ?? false,
              isTracesManagerConnected: (isAgentActive && acc.connection_status?.tracesEnabled) ?? false,
              tracesUrl: acc.connection_status?.tracesUrl ?? '',
              logsProvider: acc.connection_status?.logsConnectionProvider ?? '',
              logsProviderUrl: acc.connection_status?.logProviderUrl ?? '',
              isOpenCostConnected: (isAgentActive && acc.connection_status?.opencostConnection) ?? false,
              opencostUrl: acc.connection_status?.opencostUrl ?? '',
              isNodeAgentConnected: (isAgentActive && acc.connection_status?.nodeAgentConnection) ?? false,
              nodeAgentCount: acc.connection_status?.nodeAgentCount ?? 0,
              namespace: acc.connection_status?.installationNamespace ?? '',
              prometheusRetentionTime: acc.connection_status?.prometheusRetentionTime ?? 0,
              agentUrl: acc.connection_status?.agentUrl,
              prometheusAdditionalLabels: acc.connection_status?.prometheusAdditionalLabels
                ? JSON.stringify(acc.connection_status?.prometheusAdditionalLabels)
                : '',
            });

            // Add scheduled jobs data
            if (acc.connection_status?.schedule_jobs) {
              scheduledJobsTableData.push(
                ...acc.connection_status.schedule_jobs.map((job) => [
                  { text: job.runnable_params.action_func_name.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()) },
                  { text: job.state.job_status === 1 ? 'New' : job.state.job_status === 2 ? 'Running' : 'Done' },
                  { text: job.state.exec_count },
                  {
                    component:
                      job.state.last_exec_time_sec && job.state.last_exec_time_sec > 0 ? (
                        <Datetime value={new Date(job.state.last_exec_time_sec * 1000)} />
                      ) : (
                        <Text value='-' />
                      ),
                  },
                  { text: job.scheduling_params.cron_expression },
                ])
              );
            }
          } else {
            tableData.push([
              { component: <Text value={acc.status.replace('_', ' ') ?? '-'} /> },
              { component: <Datetime value={acc.last_connected_at} /> },
              { text: acc.type },
              { text: acc?.connection_status?.account_number },
            ]);
            setShouldUpgrade(false);

            setAgentFeatures({
              isCloudEventsConnected: isConnectedUsingDate(acc.connection_status?.events?.end),
              cloudEventsLastConnectedAt: acc.connection_status?.events?.end ?? '',
              cloudEventsError: acc.connection_status?.events?.err ?? '',

              isCloudResourcesConnected: isConnectedUsingDate(acc.connection_status?.resources?.updated_at),
              cloudResourcesLastConnectedAt: acc.connection_status?.resources?.updated_at ?? '',
              cloudResourcesError: acc.connection_status?.resources?.err ?? '',

              isCloudRecommendationsConnected: isConnectedUsingDate(acc.connection_status?.recommendations?.updated_at),
              cloudRecommendationsLastConnectedAt: acc.connection_status?.recommendations?.updated_at ?? '',
              cloudRecommendationsError: acc.connection_status?.recommendations?.err ?? '',

              isCloudSpendsConnected: isConnectedUsingDate(acc.connection_status?.spends?.updated_at),
              cloudSpendLastConnectedAt: acc.connection_status?.spends?.updated_at ?? '',
              cloudSpendsError: acc.connection_status?.spends?.err ?? '',
            });

            setCfStack(acc.connection_status?.cf_stack ?? null);
          }
        }

        setAgentType(agentType);
        setAgentHealthData(tableData);
        setScheduledJobsData(scheduledJobsTableData);
        setDisconnectedService(disconnectedService);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [router.query.accountId, selectedCluster]);

  // Fetch proxy agent data
  useEffect(() => {
    if (!router.query.accountId) return;

    setProxyLoading(true);
    k8sApi
      .getAgentHealth({ accountId: router.query.accountId, type: 'proxy' })
      .then((res) => {
        if (res?.error) {
          return;
        }
        setProxyData(res?.data ?? []);
      })
      .finally(() => {
        setProxyLoading(false);
      });
  }, [router.query.accountId]);

  // Build proxy table data — re-runs when proxyData changes
  const [proxyDatasourcesData, setProxyDatasourcesData] = useState([]);

  useEffect(() => {
    if (proxyData.length === 0) {
      setProxyAgentHealthData([]);
      setProxyDatasourcesData([]);
      return;
    }
    const tableData = proxyData.map((acc) => [
      { component: <Text value={acc.status?.replace('_', ' ') ?? '-'} /> },
      { component: <Datetime value={acc.last_connected_at} /> },
    ]);
    setProxyAgentHealthData(tableData);

    // Parse datasources from connection_status of all proxy agents
    try {
      const dsTableData = proxyData.flatMap((acc) => {
        const connStatus = typeof acc.connection_status === 'string' ? JSON.parse(acc.connection_status) : acc.connection_status;
        const datasources = connStatus?.datasources || {};
        return Object.values(datasources).map((ds) => [
          { text: ds.name || '-' },
          { text: ds.type || '-' },
          { text: ds.proxy_type || '-' },
          {
            component: (
              <Typography variant='body2' sx={{ color: ds.status === 'healthy' ? 'green' : 'red', fontWeight: 500, textTransform: 'capitalize' }}>
                {ds.status || '-'}
              </Typography>
            ),
          },
          { component: <Datetime value={ds.last_check} /> },
          { text: ds.error || '-' },
        ]);
      });
      setProxyDatasourcesData(dsTableData);
    } catch {
      setProxyDatasourcesData([]);
    }
  }, [proxyData]);

  const getPrometheusRetentionTime = (timeString) => {
    if (!timeString) {
      return '-';
    }
    if (isNaN(timeString.slice(-1))) {
      return timeString;
    }
    return timeString + ' Days';
  };

  const handleSyncNow = async () => {
    const accountId = router.query.accountId;
    if (!accountId) {
      return;
    }
    setSyncLoading(true);
    try {
      const result = await k8sApi.triggerCloudSync(accountId);
      if (result?.data?.success) {
        snackbar.success('Sync triggered successfully. Data will be available shortly.');
      } else {
        const errorMessage = result?.data?.message || result?.errors?.[0]?.message || 'Sync trigger failed';
        snackbar.error(errorMessage);
      }
    } catch (error) {
      console.error('Sync failed:', error);
      snackbar.error('Failed to trigger sync');
    } finally {
      setSyncLoading(false);
    }
  };

  const getNextSyncText = (type, lastSync) => {
    const now = new Date();
    if (type === 'events') {
      if (!lastSync) {
        return '~10 min';
      }
      const last = new Date(lastSync);
      const nextRun = new Date(last.getTime() + 10 * 60 * 1000);
      if (nextRun <= now) {
        return 'any moment';
      }
      const diffMin = Math.ceil((nextRun - now) / 60000);
      return `in ~${diffMin} min`;
    }
    if (type === 'spends') {
      // Runs daily at 1:00 UTC
      const nextRun = new Date(now);
      nextRun.setUTCHours(1, 0, 0, 0);
      if (nextRun <= now) {
        nextRun.setUTCDate(nextRun.getUTCDate() + 1);
      }
      const diffMs = nextRun - now;
      const diffHours = Math.floor(diffMs / 3600000);
      const diffMin = Math.floor((diffMs % 3600000) / 60000);
      if (diffHours > 0) {
        return `in ~${diffHours}h ${diffMin}m`;
      }
      return `in ~${diffMin} min`;
    }
    // Resources and Recommendations run after Spends
    return 'after Spends sync';
  };

  const buildCloudFeaturesTableData = () => {
    const features = [
      {
        name: 'Events',
        connected: agentFeatures.isCloudEventsConnected,
        lastSync: agentFeatures.cloudEventsLastConnectedAt,
        nextSync: getNextSyncText('events', agentFeatures.cloudEventsLastConnectedAt),
        error: agentFeatures.cloudEventsError,
      },
      {
        name: 'Spends',
        connected: agentFeatures.isCloudSpendsConnected,
        lastSync: agentFeatures.cloudSpendLastConnectedAt,
        nextSync: getNextSyncText('spends', agentFeatures.cloudSpendLastConnectedAt),
        error: agentFeatures.cloudSpendsError,
      },
      {
        name: 'Resources',
        connected: agentFeatures.isCloudResourcesConnected,
        lastSync: agentFeatures.cloudResourcesLastConnectedAt,
        nextSync: getNextSyncText('resources', agentFeatures.cloudResourcesLastConnectedAt),
        error: agentFeatures.cloudResourcesError,
      },
      {
        name: 'Recommendations',
        connected: agentFeatures.isCloudRecommendationsConnected,
        lastSync: agentFeatures.cloudRecommendationsLastConnectedAt,
        nextSync: getNextSyncText('recommendations', agentFeatures.cloudRecommendationsLastConnectedAt),
        error: agentFeatures.cloudRecommendationsError,
      },
    ];

    return features.map((f) => [
      { text: f.name },
      {
        component: (
          <Typography color={f.connected ? 'green' : 'error'} variant='body2'>
            {f.connected ? 'Connected' : 'Disconnected'}
          </Typography>
        ),
      },
      { component: f.lastSync ? <Datetime value={f.lastSync} /> : <Text value='-' /> },
      { text: f.nextSync },
      {
        component: f.error ? (
          <Typography color='error' variant='body2'>
            {f.error}
          </Typography>
        ) : (
          <Text value='-' />
        ),
      },
    ]);
  };

  const paginatedScheduledJobsData = scheduledJobsData.slice(currentPage * recordsPerPage, (currentPage + 1) * recordsPerPage);

  const optionsToDisplay = {
    tabOptions: [
      { text: 'Agent', value: 0, fragment: 'agent', id: 'tab-agent' },
      { text: 'Proxy Agent', value: 1, fragment: 'proxy-agent', id: 'tab-proxy-agent' },
    ],
  };

  // Sync tab from hash — runs on mount and on back/forward navigation
  useEffect(() => {
    const hash = router.asPath.split('#')[1] ?? '';
    const tab = optionsToDisplay.tabOptions.find((t) => t.fragment === hash);
    if (tab) setActiveTab(tab.value);
    else setActiveTab(0);
  }, [router.asPath]);

  return (
    <Box sx={{ position: 'relative' }}>
      <Box sx={{ mt: 3 }}>
        <CustomTabs value={activeTab} onChange={setActiveTab} options={optionsToDisplay} />
      </Box>

      {activeTab === 0 && (
        <>
          <ListingLayout id='agent-health' sx={{ mt: 3 }}>
            <ListingLayout.Toolbar title='Agent Health' />
            <ListingLayout.Body>
              {data[0]?.status === 'NOT_CONNECTED' ? (
                <Typography color='red'>The Agent is not connected</Typography>
              ) : (
                shouldUpgrade && <Typography color='red'>Please update your agent version</Typography>
              )}
              {disconnectedService && disconnectedService.length > 0 ? (
                <Typography color='red'>{`The ${disconnectedService.join(', ')} services are disconnected.`}</Typography>
              ) : null}
              <CustomTable headers={agentType === 'k8s' ? HEADERS_K8S : HEADERS_CLOUD} tableData={agentHealthData} loading={loading} />

              <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mt: 2, mb: 1 }}>Features</Typography>
              {agentType === 'k8s' ? (
                <ul>
                  <li>
                    <b>Relay - </b>
                    {agentFeatures.isRelayConnected ? 'Connected' : 'Disconnected'}
                  </li>
                  {agentFeatures.agentUrl && (
                    <li>
                      <b>Agent URL - </b>
                      {agentFeatures.agentUrl}
                    </li>
                  )}
                  <li>
                    <b>Prometheus - </b>
                    <ul>
                      <li>Status - {agentFeatures.isPrometheusConnected ? 'Connected' : 'Disconnected'}</li>
                      <li>Data Retention - {getPrometheusRetentionTime(agentFeatures.prometheusRetentionTime)}</li>
                      <li>URL - {agentFeatures.prometheusUrl}</li>
                      <li>Additional Labels - {agentFeatures.prometheusAdditionalLabels}</li>
                    </ul>
                  </li>
                  <li>
                    <b>Alert Manager - </b> {agentFeatures.isAlertManagerConnected ? 'Connected' : 'Disconnected'}
                  </li>
                  <li>
                    <b>Logs - </b>
                    <ul>
                      <li>Status - {agentFeatures.isLogsManagerConnected ? 'Connected' : 'Disconnected'}</li>
                      <li>Provider - {agentFeatures.logsProvider}</li>
                      <li>URL - {agentFeatures.logsProviderUrl}</li>
                    </ul>
                  </li>
                  <li>
                    <b>Traces - </b>
                    <ul>
                      <li>Status - {agentFeatures.isTracesManagerConnected ? 'Connected' : 'Disconnected'}</li>
                      <li>URL - {agentFeatures.tracesUrl}</li>
                    </ul>
                  </li>
                  <li>
                    <b>OpenCost - </b>
                    <ul>
                      <li>Status - {agentFeatures.isOpenCostConnected ? 'Connected' : 'Disconnected'}</li>
                      <li>URL - {agentFeatures.opencostUrl}</li>
                    </ul>
                  </li>
                  <li>
                    <b>Node Agent - </b> {agentFeatures.isNodeAgentConnected ? 'Connected (' + agentFeatures.nodeAgentCount + ')' : 'Disconnected'}
                  </li>
                  <li>
                    <b>Agent Namespace - </b> {agentFeatures.namespace}
                  </li>
                </ul>
              ) : (
                <>
                  <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 1 }}>
                    <DsButton
                      tone='primary'
                      size='sm'
                      onClick={handleSyncNow}
                      disabled={syncLoading}
                      loading={syncLoading}
                      icon={<SyncIcon fontSize='small' />}
                    >
                      Sync Now
                    </DsButton>
                  </Box>
                  <CustomTable headers={HEADERS_CLOUD_FEATURES} tableData={buildCloudFeaturesTableData()} loading={loading} />

                  {cfStack && (
                    <>
                      <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mt: 2, mb: 1 }}>
                        CloudFormation Stack
                      </Typography>
                      <ul>
                        <li>
                          <b>Template Version - </b>
                          {cfStack.template_version || '-'}
                          {cfStack.template_version && cfStack.template_version !== LATEST_CF_TEMPLATE_VERSION && (
                            <Typography component='span' color='error' variant='body2' sx={{ ml: 1 }}>
                              (update available)
                            </Typography>
                          )}
                        </li>
                        <li>
                          <b>Stack Name - </b>
                          {cfStack.stack_name || '-'}
                        </li>
                        <li>
                          <b>Stack Region - </b>
                          {cfStack.stack_region || '-'}
                        </li>
                        <li>
                          <b>Stack Status - </b>
                          {cfStack.stack_status || '-'}
                        </li>
                        <li>
                          <b>Last Checked - </b>
                          {cfStack.updated_at ? <Datetime value={cfStack.updated_at} /> : '-'}
                        </li>
                      </ul>
                    </>
                  )}
                </>
              )}
            </ListingLayout.Body>
          </ListingLayout>
          {agentType === 'k8s' && scheduledJobsData.length > 0 && (
            <ListingLayout id='scheduled-jobs-table' sx={{ mt: 3 }}>
              <ListingLayout.Toolbar title='Scheduled Jobs' />
              <ListingLayout.Body>
                <CustomTable
                  headers={HEADERS_SCHEDULED_JOBS}
                  tableData={paginatedScheduledJobsData}
                  loading={loading}
                  rowsPerPage={recordsPerPage}
                  totalRows={scheduledJobsData.length}
                  pageNumber={currentPage + 1}
                  onPageChange={(page, limit) => {
                    setCurrentPage(page - 1);
                    setRecordsPerPage(limit);
                  }}
                />
              </ListingLayout.Body>
            </ListingLayout>
          )}
        </>
      )}

      {activeTab === 1 && (
        <>
          {!proxyLoading && proxyAgentHealthData.length === 0 ? (
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                justifyContent: 'center',
                padding: '50px 32px',
                borderRadius: '12px',
                border: '1px solid #E4E4E4',
                background: '#FFF',
                mt: 2,
              }}
            >
              <Box
                sx={{
                  width: 64,
                  height: 64,
                  borderRadius: '16px',
                  background: 'linear-gradient(135deg, #EBF2FF 0%, #DBEAFE 100%)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  mb: 3,
                  boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.06)',
                  '& img': {
                    filter: 'brightness(0) saturate(100%) invert(33%) sepia(93%) saturate(1752%) hue-rotate(213deg) brightness(97%) contrast(93%)',
                  },
                }}
              >
                <SafeIcon src={AgentIconBlue} alt='Proxy Agent' width={36} height={36} />
              </Box>

              <Typography sx={{ fontSize: '18px', fontWeight: 600, color: '#101828', mb: 1, fontFamily: 'Poppins' }}>
                Get started with Proxy Agent monitoring
              </Typography>
              <Typography sx={{ fontSize: '14px', color: '#667085', mb: 4, textAlign: 'center', maxWidth: '460px', lineHeight: 1.6 }}>
                Connect a VM agent to start monitoring your on-premise or virtual machine infrastructure with real-time visibility and actionable
                insights.
              </Typography>

              <Stack direction='row' spacing={4} sx={{ mb: 4 }}>
                {[
                  {
                    label: 'VM monitoring',
                    icon: 'M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z',
                  },
                  {
                    label: 'Network visibility',
                    icon: 'M9 3H5a2 2 0 00-2 2v4m6-6h10a2 2 0 012 2v4M9 3v18m0 0h10a2 2 0 002-2V9M9 21H5a2 2 0 01-2-2V9m0 0h18',
                  },
                  {
                    label: 'Datasource health',
                    icon: 'M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z',
                  },
                ].map((item) => (
                  <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <Box
                      sx={{
                        width: 32,
                        height: 32,
                        borderRadius: '8px',
                        background: '#F5F8FF',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        flexShrink: 0,
                      }}
                    >
                      <svg
                        width='16'
                        height='16'
                        viewBox='0 0 24 24'
                        fill='none'
                        stroke='#2563EB'
                        strokeWidth='1.5'
                        strokeLinecap='round'
                        strokeLinejoin='round'
                      >
                        <path d={item.icon} />
                      </svg>
                    </Box>
                    <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#344054', whiteSpace: 'nowrap' }}>{item.label}</Typography>
                  </Box>
                ))}
              </Stack>

              {hasWriteAccess() ? (
                <DsButton tone='primary' size='md' onClick={() => router.push('/accounts/account-form?cloudProvider=VM_AGENT')}>
                  Connect VM Agent
                </DsButton>
              ) : (
                <Typography sx={{ fontSize: '13px', color: '#667085', fontStyle: 'italic' }}>Need admin permission to connect a VM agent</Typography>
              )}
            </Box>
          ) : (
            <>
              <ListingLayout id='proxy-agent-health' sx={{ mt: 3 }}>
                <ListingLayout.Toolbar title='Proxy Agent Health' />
                <ListingLayout.Body>
                  {proxyData[0]?.status === 'NOT_CONNECTED' && (
                    <Typography color='red' sx={{ p: 2 }}>
                      The Proxy Agent is not connected
                    </Typography>
                  )}
                  <CustomTable headers={HEADERS_PROXY} tableData={proxyAgentHealthData} loading={proxyLoading} />
                </ListingLayout.Body>
              </ListingLayout>
              {proxyDatasourcesData.length > 0 && (
                <ListingLayout id='proxy-datasources' sx={{ mt: 3 }}>
                  <ListingLayout.Toolbar title='Datasources' />
                  <ListingLayout.Body>
                    <CustomTable headers={HEADERS_PROXY_DATASOURCES} tableData={proxyDatasourcesData} loading={proxyLoading} />
                  </ListingLayout.Body>
                </ListingLayout>
              )}
            </>
          )}
        </>
      )}
    </Box>
  );
};

export default AgentHealth;
