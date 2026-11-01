import React, { useEffect, useMemo, useRef, useState } from 'react';
import { ListingLayout } from '@ui/ListingLayout';
import { withAccountGuard } from '@shared/AccountGuard';
import { Button as DsButton } from '@ui/Button';
import { ds } from 'src/utils/colors';
import k8sApi from '@api1/kubernetes';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
import { useRouter } from 'next/router';
import { Box, Typography, Stack, IconButton } from '@mui/material';
import { AgentIconBlue } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { hasWriteAccess } from '@lib/auth';
import CustomTable from '@shared/tables/CustomTable';
import { useData } from '@context/DataContext';
import Tabs from '@shared/navigation/Tabs';
import SyncIcon from '@mui/icons-material/Sync';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import ShowChartOutlinedIcon from '@mui/icons-material/ShowChartOutlined';
import NotificationsNoneOutlinedIcon from '@mui/icons-material/NotificationsNoneOutlined';
import SubjectOutlinedIcon from '@mui/icons-material/SubjectOutlined';
import AccountTreeOutlinedIcon from '@mui/icons-material/AccountTreeOutlined';
import PaidOutlinedIcon from '@mui/icons-material/PaidOutlined';
import DnsOutlinedIcon from '@mui/icons-material/DnsOutlined';
import { toast as snackbar } from '@ui/Toast';
import VmAgents from '@components/vm/VmAgents';
import { Label } from '@ui/Label';
import Banner from '@ui/Banner';
import Card from '@ui/Card';
import ToggleGroup from '@ui/ToggleGroup';
import Tooltip from '@ui/Tooltip';
import { describeCron, nextCronRunAfter } from '@utils/cron';
import { docsUrl } from '@lib/externalUrls';

const HEADERS_CLOUD = ['Status', 'Last Connected', 'Cloud', 'Account'];
const HEADERS_PROXY = ['Status', 'Last Connected'];
const HEADERS_PROXY_DATASOURCES = ['Name', 'Type', 'Proxy Type', 'Status', 'Last Check', 'Error'];
// error_class values the proxy agent (forager) attaches to a datasource in status "error".
const PROXY_ERROR_CLASS_LABELS = {
  datasource_integration_failure: 'Datasource integration failure',
  ssh_integration_failure: 'SSH integration failure',
};
const HEADERS_COMPONENTS = ['Component', 'Status', 'Endpoint', 'Details'];
// Executions is right-aligned: a column of counts is read by magnitude, and CustomTable
// takes the alignment from the header rather than from each cell.
const HEADERS_SCHEDULED_JOBS = ['Action Name', 'Job Status', 'Schedule', 'Last Execution', { name: 'Executions', align: 'right' }];
const HEADERS_CLOUD_FEATURES = ['Feature', 'Status', 'Last Sync', 'Next Sync', 'Error'];
const LATEST_CF_TEMPLATE_VERSION = '2';

// Where the "View update guide" action goes. Same document the global header snackbar
// links to (components/common/header/Header1.jsx) — one upgrade path, one page describing it.
const AGENT_UPGRADE_DOCS_URL = docsUrl('/docs/installation/agent/installation/upgrade/');

const AGENT_TAB = { text: 'Agent', value: 0, fragment: 'agent', id: 'tab-agent' };
const OBSERVABILITY_TAB = { text: 'Observability', value: 1, fragment: 'observability', id: 'tab-observability' };
const PROXY_AGENT_TAB = { text: 'Proxy Agent', value: 2, fragment: 'proxy-agent', id: 'tab-proxy-agent' };

/** The signals the Observability tab reports on, in display order. */
const OBSERVABILITY_SIGNALS = [
  { key: 'logs', label: 'Logs' },
  { key: 'metrics', label: 'Metrics' },
  { key: 'traces', label: 'Traces' },
];

/** cloud_provider of a self-hosted VM fleet — reached only through a proxy agent. */
const SELF_HOSTED = 'SelfHosted';

/**
 * The entry for a signal (logs / metrics / traces) whose provider is a NON-agent
 * integration — a hosted Loki, Datadog, Elasticsearch — or null when the agent serves
 * it. Stamped onto connection_status.providerStatus by the provider-status cron; an
 * `integration_id` is the marker (see api-server observability/provider_status.go).
 *
 * The agent's own *Connection flag says nothing about such a backend, so wherever this
 * returns an entry the page must report the entry's probe result instead of that flag.
 */
export const integrationServed = (providerStatus, signal) => {
  const entry = providerStatus?.[signal];
  return entry?.integration_id ? entry : null;
};

/**
 * The services named in the red "… are disconnected" banner on the Agent tab. This banner
 * speaks for the agent only: a signal an integration serves is omitted entirely, whether it
 * is healthy or not, because the agent's flag says nothing about that backend and its real
 * state belongs to the Telemetry Sources tab. Everything else keeps the agent's own flags.
 */
export const buildDisconnectedServices = (connectionStatus, providerStatus) => {
  const disconnected = [];
  if (!connectionStatus?.relayConnection) {
    disconnected.push('Relay');
  }
  if (!integrationServed(providerStatus, 'metrics') && !connectionStatus?.prometheusConnection) {
    disconnected.push('Prometheus');
  }
  if (!connectionStatus?.alertManagerConnection) {
    disconnected.push('Alert Manager');
  }
  if (!integrationServed(providerStatus, 'logs') && !connectionStatus?.logsConnection) {
    disconnected.push('Logs');
  }
  if (!connectionStatus?.nodeAgentConnection) {
    disconnected.push('NodeAgent');
  }
  return disconnected;
};

/** What each component state reports in the Components table's Status column. */
const COMPONENT_STATE_LABEL = {
  connected: { text: 'Connected', tone: 'success' },
  disconnected: { text: 'Disconnected', tone: 'critical' },
  'server-side': { text: 'Managed server-side', tone: 'info' },
  integration: { text: 'Served by integration', tone: 'neutral' },
};

/** The three states a scheduled job's `job_status` code can hold, and how each reads. */
const JOB_STATUS = { 1: { text: 'New', tone: 'neutral' }, 2: { text: 'Running', tone: 'info' } };
const JOB_STATUS_DONE = { text: 'Done', tone: 'success' };

/**
 * Whether a job's last run is older than its own schedule allows — the next fire time
 * after that run is already in the past. Derived from the cron rather than from a fixed
 * staleness window, so an hourly job and a monthly one are each judged on their own terms.
 *
 * Unknowable (and so not overdue) in three cases: a job that has never run, an
 * unparseable expression, and an `@every` descriptor, whose cadence is anchored to when
 * the agent registered it rather than to the wall clock (see utils/cron nextCronRunAfter).
 */
const isJobOverdue = (cron, lastExec) => {
  if (!lastExec) return false;
  const due = nextCronRunAfter(cron ?? '', lastExec);
  return due !== null && due.getTime() < Date.now();
};

/** One row's worth of a scheduled job, in the shape the table, the filter and the counts all read. */
export const toScheduledJob = (job) => {
  const cron = job?.scheduling_params?.cron_expression ?? '';
  const lastExecSec = job?.state?.last_exec_time_sec;
  const lastExec = lastExecSec && lastExecSec > 0 ? new Date(lastExecSec * 1000) : null;
  return {
    name: (job?.runnable_params?.action_func_name ?? '').replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()),
    status: JOB_STATUS[job?.state?.job_status] ?? JOB_STATUS_DONE,
    cron,
    // Readable form of the same expression, shown above it. Empty when cronstrue cannot
    // describe it, in which case the cron alone is all the row can honestly say.
    schedule: describeCron(cron),
    lastExec,
    execCount: job?.state?.exec_count ?? 0,
    overdue: isJobOverdue(cron, lastExec),
  };
};

const ProxyDatasourceExpandedRow = ({ row }) => {
  const raw = row[row.length - 1]?._raw || {};
  const entries = Object.entries(raw).filter(([k]) => !['name', 'type', 'proxy_type', 'status', 'last_check', 'error', 'error_class'].includes(k));

  if (entries.length === 0) {
    return (
      <Box p={ds.space[3]}>
        <Typography variant='body2' sx={{ color: ds.gray[500] }}>
          No additional details
        </Typography>
      </Box>
    );
  }

  return (
    <Box
      sx={{
        p: ds.space[3],
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))',
        gap: ds.space[2],
      }}
    >
      {entries.map(([key, value]) => (
        <Box key={key}>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], mb: 0.5, textTransform: 'capitalize' }}>
            {key.replace(/_/g, ' ')}
          </Typography>
          <Typography sx={{ fontSize: ds.text.body, color: ds.gray[800], wordBreak: 'break-all' }}>
            {typeof value === 'object' ? JSON.stringify(value) : String(value)}
          </Typography>
        </Box>
      ))}
    </Box>
  );
};

const AgentHealth = () => {
  const { selectedCluster } = useData();
  // A self-hosted VM fleet has no in-cluster/cloud agent — the proxy agent is the
  // only thing there is to report on, so the Agent tab is dropped for it and the
  // fleet-side agent view (moved off /vm) rides along under Proxy Agent.
  const isVmAccount = selectedCluster?.cloud_provider === SELF_HOSTED;
  const [agentHealthData, setAgentHealthData] = useState([]);
  const [agentType, setAgentType] = useState('k8s');
  const [data, setData] = useState([]);
  // Which backend actually serves each signal, and its probe result — see
  // integrationServed. Null for a cluster the provider-status cron has never stamped
  // (i.e. everything is agent-served), which is the pre-existing behaviour.
  const [providerStatus, setProviderStatus] = useState(null);
  const [agentFeatures, setAgentFeatures] = useState({
    isRelayConnected: false,
    isPrometheusConnected: false,
    prometheusUrl: '',
    isAlertManagerConnected: false,
    isLogsManagerConnected: false,
    logsProvider: '',
    logsProviderUrl: '',
    isTracesManagerConnected: false,
    traceProvider: '',
    tracesUrl: '',
    isOpenCostConnected: false,
    isOpenCostServerSide: false,
    opencostUrl: '',
    isNodeAgentConnected: false,
    nodeAgentCount: 0,
    namespace: '',
    prometheusRetentionTime: 0,
    // When the agent last reported the flags above, stamped by the collector on the
    // telemetry post that carried them. Deliberately NOT gated on the agent being
    // connected, unlike every boolean here: a stale `true` is a lie, but a stale
    // timestamp states its own age, which is the whole point of showing it.
    healthCheckedAt: '',

    // per-feature failure reasons reported by the agent's health checks
    prometheusError: '',
    alertManagerError: '',
    logsError: '',
    tracesError: '',
    opencostError: '',
    nodeAgentError: '',

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
  // The K8s agent's own facts, for the summary card that replaced the one-row agent
  // table. Kept separate from `agentFeatures` (which holds what the agent reported
  // *about its components*) because these come off the account row, not connection_status.
  const [agentSummary, setAgentSummary] = useState(null);
  const [latestVersions, setLatestVersions] = useState({});
  const [shouldUpgrade, setShouldUpgrade] = useState(false);
  const [disconnectedService, setDisconnectedService] = useState([]);
  const [loading, setLoading] = useState(false);
  // Raw job records, not table rows: the overdue count, the filter and the summary line
  // all read the same fields, so the rows are built at render time from these.
  const [scheduledJobsData, setScheduledJobsData] = useState([]);
  const [jobFilter, setJobFilter] = useState('all');
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(50);
  const [syncLoading, setSyncLoading] = useState(false);
  const [cfStack, setCfStack] = useState(null);

  // Proxy agent state
  const [proxyData, setProxyData] = useState([]);
  const [proxyAgentHealthData, setProxyAgentHealthData] = useState([]);
  const [proxyLoading, setProxyLoading] = useState(false);
  const [activeTab, setActiveTab] = useState(null);
  const [proxyDsPage, setProxyDsPage] = useState(0);
  const [proxyDsRowsPerPage, setProxyDsRowsPerPage] = useState(10);

  const latestVersionsRef = useRef(latestVersions);
  const router = useRouter();

  useEffect(() => {
    k8sApi.getLatestVersions().then((res) => {
      setLatestVersions(res.data?.nudgebee_list_versions || {});
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
    if (!router.query.accountId) return;
    if (!selectedCluster?.cloud_provider && !selectedCluster?.type) return;
    // No k8s/cloud agent behind a self-hosted account, and no Agent tab to fill.
    if (selectedCluster?.cloud_provider === SELF_HOSTED) return;
    const accountType = selectedCluster?.cloud_provider || selectedCluster?.type;
    const query = {
      accountId: router.query.accountId,
      type: accountType,
    };
    let cancelled = false;
    setLoading(true);
    k8sApi
      .getAgentHealth(query)
      .then((res) => {
        if (cancelled || res?.error) {
          return;
        }
        const rawData = Array.isArray(res?.data) ? res.data : [];
        setData(rawData);
        let result = rawData;
        let tableData = [];
        let scheduledJobsTableData = [];
        let disconnectedService = [];
        let isAgentActive = false;
        let agentType = 'k8s';

        for (let acc of result) {
          agentType = acc.type;
          // Reset per account, outside the k8s branch: a cloud account carries no telemetry
          // stamp, and leaving the previous k8s cluster's value in state would show one
          // account's backends under another's name after switching.
          setProviderStatus(acc.type === 'k8s' ? acc.connection_status?.providerStatus ?? null : null);
          isAgentActive = acc.status === 'CONNECTED';
          const latestVersionsData = latestVersionsRef.current;

          acc.connection_status = acc.connection_status || {};

          if (agentType === 'k8s') {
            setAgentSummary({
              status: acc.status ?? '',
              version: acc.version ?? '',
              lastConnectedAt: acc.last_connected_at ?? '',
              k8sProvider: acc.k8s_provider ?? '',
              k8sVersion: acc.k8s_version ?? '',
            });
            if (latestVersionsData?.agent_version_latest && acc.version !== latestVersionsData?.agent_version_latest) {
              setShouldUpgrade(true);
            } else {
              setShouldUpgrade(false);
            }

            const connectionStatus = result?.[0].connection_status;
            disconnectedService.push(...buildDisconnectedServices(connectionStatus, connectionStatus?.providerStatus));

            setCfStack(null);
            setAgentFeatures({
              isRelayConnected: (isAgentActive && acc.connection_status?.relayConnection) ?? false,
              isPrometheusConnected: (isAgentActive && acc.connection_status?.prometheusConnection) ?? false,
              prometheusUrl: acc.connection_status?.prometheusUrl ?? '',
              isAlertManagerConnected: (isAgentActive && acc.connection_status?.alertManagerConnection) ?? false,
              isLogsManagerConnected: (isAgentActive && acc.connection_status?.logsConnection) ?? false,
              isTracesManagerConnected: (isAgentActive && acc.connection_status?.tracesEnabled) ?? false,
              traceProvider: acc.connection_status?.traceProvider ?? '',
              tracesUrl: acc.connection_status?.tracesUrl ?? '',
              logsProvider: acc.connection_status?.logsConnectionProvider ?? '',
              logsProviderUrl: acc.connection_status?.logProviderUrl ?? '',
              isOpenCostConnected: (isAgentActive && acc.connection_status?.opencostConnection) ?? false,
              // opencostServerSide is stamped by the backend spend sync when OpenCost is collected
              // server-side (post-migration default, agent OpenCost off) — surface that rather than
              // "Disconnected".
              isOpenCostServerSide: (isAgentActive && acc.connection_status?.opencostServerSide) ?? false,
              opencostUrl: acc.connection_status?.opencostUrl ?? '',
              isNodeAgentConnected: (isAgentActive && acc.connection_status?.nodeAgentConnection) ?? false,
              nodeAgentCount: acc.connection_status?.nodeAgentCount ?? 0,
              namespace: acc.connection_status?.installationNamespace ?? '',
              prometheusRetentionTime: acc.connection_status?.prometheusRetentionTime ?? 0,
              prometheusError: acc.connection_status?.prometheusConnectionError ?? '',
              alertManagerError: acc.connection_status?.alertManagerConnectionError ?? '',
              logsError: acc.connection_status?.logsConnectionError ?? '',
              tracesError: acc.connection_status?.tracesConnectionError ?? '',
              opencostError: acc.connection_status?.opencostConnectionError ?? '',
              nodeAgentError: acc.connection_status?.nodeAgentConnectionError ?? '',
              agentUrl: acc.connection_status?.agentUrl,
              healthCheckedAt: acc.connection_status?.healthCheckedAt ?? '',
              prometheusAdditionalLabels: acc.connection_status?.prometheusAdditionalLabels
                ? JSON.stringify(acc.connection_status?.prometheusAdditionalLabels)
                : '',
            });

            // Add scheduled jobs data
            if (acc.connection_status?.schedule_jobs) {
              scheduledJobsTableData.push(...acc.connection_status.schedule_jobs.map(toScheduledJob));
            }
          } else {
            tableData.push([
              { component: <Text value={acc.status.replace('_', ' ') ?? '-'} /> },
              { component: <Datetime value={acc.last_connected_at} /> },
              { text: acc.type },
              { text: acc?.connection_status?.account_number },
            ]);
            // Same reason as the providerStatus reset above: a cloud account has no agent
            // summary, and last cluster's card must not survive the switch.
            setAgentSummary(null);
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
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [router.query.accountId, selectedCluster]);

  // Fetch proxy agent data
  useEffect(() => {
    if (!router.query.accountId) return;

    let cancelled = false;
    setProxyLoading(true);
    k8sApi
      .getAgentHealth({ accountId: router.query.accountId, type: 'proxy' })
      .then((res) => {
        if (cancelled || res?.error) {
          return;
        }
        setProxyData(Array.isArray(res?.data) ? res.data : []);
      })
      .finally(() => {
        if (!cancelled) {
          setProxyLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
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
        return Object.values(datasources).map((datasource) => [
          { text: datasource.name || '-' },
          { text: datasource.type || '-' },
          { text: datasource.proxy_type || '-' },
          {
            component: <Label tone={datasource.status === 'healthy' ? 'success' : 'critical'} dot text={datasource.status || '-'} />,
          },
          { component: <Datetime value={datasource.last_check} /> },
          {
            // Older agents send no error_class; they fall back to the bare message.
            component: datasource.error ? (
              <Box>
                {typeof datasource.error_class === 'string' && (
                  <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.red[500], textTransform: 'capitalize' }}>
                    {PROXY_ERROR_CLASS_LABELS[datasource.error_class] ?? datasource.error_class.replace(/_/g, ' ')}
                  </Typography>
                )}
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], wordBreak: 'break-word' }}>{datasource.error}</Typography>
              </Box>
            ) : (
              <Text value='-' />
            ),
            _raw: datasource,
          },
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

  // One row per signal: which backend actually serves it, and — when that backend is an
  // integration rather than the agent — whether it answered its last connection test.
  // Agent-served rows carry no status here on purpose; the Agent tab owns that.
  // What the agent itself last reported for a signal. The provider-status check only runs
  // for clusters with a non-agent integration, so on an agent-served cluster there is no
  // stamp to read — but this page already holds those fields (the Agent tab renders them),
  // so the row is filled in from there rather than left blank waiting on a cron.
  //
  // traceProvider alone proves nothing — every agent reports "otel_clickhouse" whether or
  // not traces are wired — so it counts only when the agent also enabled traces. Mirrors
  // agentReportedProvider in the api-server's provider_status.go.
  const agentReportedProvider = (signal) => {
    const isChronosphere = (agentFeatures.prometheusUrl || '').includes('chronosphere');
    if (signal === 'logs') return agentFeatures.logsProvider || '';
    if (signal === 'metrics') return isChronosphere ? 'chronosphere' : agentFeatures.prometheusUrl ? 'prometheus' : '';
    if (signal === 'traces' && agentFeatures.isTracesManagerConnected) {
      return isChronosphere ? 'chronosphere' : agentFeatures.traceProvider || '';
    }
    return '';
  };

  // Whether the agent reports this signal as working. Same flags the Agent tab renders, so
  // the two tabs cannot disagree — and already gated on the agent being live, so a
  // disconnected agent reports Disconnected here too.
  const agentSignalConnected = (signal) =>
    ({
      logs: agentFeatures.isLogsManagerConnected,
      metrics: agentFeatures.isPrometheusConnected,
      traces: agentFeatures.isTracesManagerConnected,
    }[signal] ?? false);

  // One entry per signal, as the Observability cards read it. Same resolution as before —
  // only the presentation moved from a table row to a card.
  const observabilitySignals = OBSERVABILITY_SIGNALS.map(({ key, label }) => {
    const entry = providerStatus?.[key];
    const viaIntegration = integrationServed(providerStatus, key);
    const provider = entry?.provider || agentReportedProvider(key);
    // Both sources of "as of when", picked by whoever answered the status: the cron's probe
    // time for an integration, the agent's own report time for a signal it serves. A signal
    // with no provider at all reports neither — nothing was checked.
    return {
      key,
      label,
      provider,
      // A probed integration reports its probe; an agent-served signal reports the agent's
      // own flag for it. `source` is what says which of the two answered, and `lastChecked`
      // dates whichever it was.
      connected: viaIntegration ? !!viaIntegration.connected : agentSignalConnected(key),
      configured: !!provider,
      source: viaIntegration ? `Integration "${viaIntegration.integration_name}"` : provider ? 'Agent' : '',
      lastChecked: viaIntegration ? providerStatus?.checkedAt : provider ? agentFeatures.healthCheckedAt : '',
      error: entry?.error || '',
      // Advisory, and separate from `error` — the signal works, something else
      // about its integration does not.
      note: entry?.note || '',
    };
  });

  const connectedSignalCount = observabilitySignals.filter((signal) => signal.configured && signal.connected).length;

  const copyToClipboard = (value) => {
    navigator.clipboard
      ?.writeText(value)
      .then(() => snackbar.success('Copied to clipboard'))
      .catch(() => snackbar.error('Could not copy to clipboard'));
  };

  // The components of the agent, in the order the Components table lists them. `state` is
  // what the Status column reports; `details` are the name/value pairs that column carries
  // for the components that have any.
  //
  // 'integration' is not a health verdict: for a signal a non-agent backend serves, green
  // or red would both be the agent's opinion about something it never touches, so the row
  // states who owns the answer and the Observability tab reports it. Same rule the Features
  // list followed before the redesign.
  const buildComponents = () => [
    { key: 'relay', label: 'Relay', icon: <HubOutlinedIcon />, state: agentFeatures.isRelayConnected ? 'connected' : 'disconnected' },
    {
      key: 'prometheus',
      label: 'Prometheus',
      icon: <ShowChartOutlinedIcon />,
      state: integrationServed(providerStatus, 'metrics') ? 'integration' : agentFeatures.isPrometheusConnected ? 'connected' : 'disconnected',
      endpoint: agentFeatures.prometheusUrl,
      error: agentFeatures.prometheusError,
      details: [
        { label: 'Retention', value: getPrometheusRetentionTime(agentFeatures.prometheusRetentionTime) },
        { label: 'Additional labels', value: agentFeatures.prometheusAdditionalLabels || 'None' },
      ],
    },
    {
      key: 'alert-manager',
      label: 'Alert Manager',
      icon: <NotificationsNoneOutlinedIcon />,
      state: agentFeatures.isAlertManagerConnected ? 'connected' : 'disconnected',
      error: agentFeatures.alertManagerError,
    },
    {
      key: 'logs',
      label: 'Logs',
      icon: <SubjectOutlinedIcon />,
      state: integrationServed(providerStatus, 'logs') ? 'integration' : agentFeatures.isLogsManagerConnected ? 'connected' : 'disconnected',
      endpoint: agentFeatures.logsProviderUrl,
      error: agentFeatures.logsError,
      details: agentFeatures.logsProvider ? [{ label: 'Provider', value: agentFeatures.logsProvider }] : [],
    },
    {
      key: 'traces',
      label: 'Traces',
      icon: <AccountTreeOutlinedIcon />,
      state: integrationServed(providerStatus, 'traces') ? 'integration' : agentFeatures.isTracesManagerConnected ? 'connected' : 'disconnected',
      endpoint: agentFeatures.tracesUrl,
      error: agentFeatures.tracesError,
      details: agentFeatures.traceProvider ? [{ label: 'Provider', value: agentFeatures.traceProvider }] : [],
    },
    {
      key: 'opencost',
      label: 'OpenCost',
      icon: <PaidOutlinedIcon />,
      // opencostServerSide is stamped by the backend spend sync when OpenCost is collected
      // server-side (post-migration default, agent OpenCost off) — a third state, not a failure.
      state: agentFeatures.isOpenCostConnected ? 'connected' : agentFeatures.isOpenCostServerSide ? 'server-side' : 'disconnected',
      endpoint: agentFeatures.isOpenCostConnected ? agentFeatures.opencostUrl : '',
      error: agentFeatures.opencostError,
    },
    {
      key: 'node-agent',
      label: 'Node Agent',
      icon: <DnsOutlinedIcon />,
      state: agentFeatures.isNodeAgentConnected ? 'connected' : 'disconnected',
      error: agentFeatures.nodeAgentError,
      details: agentFeatures.isNodeAgentConnected ? [{ label: 'Instances', value: String(agentFeatures.nodeAgentCount) }] : [],
    },
  ];

  // Endpoint cell: the URL the agent reported, in mono with a copy affordance because these
  // are long in-cluster service addresses nobody retypes. An em dash where the component
  // reports no endpoint of its own (Relay, Alert Manager, Node Agent).
  const renderEndpoint = (endpoint, label) =>
    endpoint ? (
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[1], minWidth: 0 }}>
        <Typography sx={{ fontFamily: ds.font.mono, fontSize: ds.text.small, color: ds.gray[700], overflowWrap: 'anywhere', minWidth: 0 }}>
          {endpoint}
        </Typography>
        <Tooltip title='Copy endpoint'>
          <IconButton size='small' aria-label={`Copy ${label} endpoint`} onClick={() => copyToClipboard(endpoint)} sx={{ p: ds.space[1] }}>
            <ContentCopyOutlinedIcon sx={{ fontSize: 14, color: ds.gray[500] }} />
          </IconButton>
        </Tooltip>
      </Box>
    ) : (
      <Text value='-' />
    );

  // Details cell: the component's own name/value facts, and — when it is reporting
  // disconnected — the reason its health check gave, which is the whole value of the cell
  // on a broken cluster.
  const renderComponentDetails = (component) => {
    const showReason = component.state === 'disconnected' && component.error;
    if (component.state === 'integration') {
      return <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Reported on the Observability tab</Typography>;
    }
    if (!component.details?.length && !showReason) {
      return <Text value='-' />;
    }
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
        {!!component.details?.length && (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: ds.space[3], rowGap: ds.space[1] }}>
            {component.details.map((detail) => (
              <Typography key={detail.label} sx={{ fontSize: ds.text.small, color: ds.gray[600], whiteSpace: 'nowrap' }}>
                {detail.label}{' '}
                <Box component='span' sx={{ color: ds.gray[700], fontWeight: ds.weight.medium }}>
                  {detail.value || '-'}
                </Box>
              </Typography>
            ))}
          </Box>
        )}
        {showReason && <Typography sx={{ fontSize: ds.text.small, color: ds.red[500] }}>Reason - {component.error}</Typography>}
      </Box>
    );
  };

  const buildComponentsTableData = () =>
    buildComponents().map((component) => {
      const state = COMPONENT_STATE_LABEL[component.state];
      return [
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }} data-testid={`component-${component.key}`}>
              <Box
                sx={{
                  flexShrink: 0,
                  width: ds.space.mul(0, 14),
                  height: ds.space.mul(0, 14),
                  borderRadius: ds.radius.md,
                  backgroundColor: ds.blue[100],
                  color: ds.brand[500],
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  '& svg': { width: 16, height: 16 },
                }}
              >
                {component.icon}
              </Box>
              <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{component.label}</Typography>
            </Box>
          ),
        },
        { component: <Label tone={state.tone} dot text={state.text} /> },
        {
          // Blank for an integration-served signal, like the Details cell beside it: the URL
          // the agent last reported is its own check's address, not the backend actually
          // serving the signal, so printing it here would contradict the row's own status.
          component: renderEndpoint(component.state === 'integration' ? '' : component.endpoint, component.label),
        },
        { component: renderComponentDetails(component) },
      ];
    });

  // "6 connected · 1 managed server-side" — the states actually present, so a healthy
  // cluster does not carry a "0 disconnected" clause it has no use for.
  const componentsSummary = () => {
    const states = buildComponents().map((component) => component.state);
    const count = (state) => states.filter((entry) => entry === state).length;
    return [
      `${count('connected')} connected`,
      count('disconnected') > 0 && `${count('disconnected')} disconnected`,
      count('server-side') > 0 && `${count('server-side')} managed server-side`,
      count('integration') > 0 && `${count('integration')} served by an integration`,
    ]
      .filter(Boolean)
      .join(' · ');
  };

  // The status the summary card's chip reports. CONNECTED is the only healthy value the
  // API returns; anything else (NOT_CONNECTED, and any state added later) reads as a
  // problem rather than being silently shown as green.
  const agentStatus = (() => {
    const raw = (agentSummary?.status ?? '').replace(/_/g, ' ');
    if (!raw) return { text: 'Unknown', tone: 'neutral' };
    return { text: raw === 'CONNECTED' ? 'Connected' : raw, tone: raw === 'CONNECTED' ? 'success' : 'critical' };
  })();

  const proxyStatus = (() => {
    const raw = (proxyData[0]?.status ?? '').replace(/_/g, ' ');
    if (!raw) return { text: 'Unknown', tone: 'neutral' };
    return { text: raw === 'CONNECTED' ? 'Connected' : raw, tone: raw === 'CONNECTED' ? 'success' : 'critical' };
  })();

  const overdueJobCount = scheduledJobsData.filter((job) => job.overdue).length;
  // The filter control is only offered while something is overdue, so a filter left on
  // 'overdue' after the last one caught up would leave an empty table and no visible
  // control to clear it. Derived rather than reset in an effect: nothing needs to happen
  // on the transition, the filter simply does not apply when there is nothing to filter to.
  const showOverdueOnly = jobFilter === 'overdue' && overdueJobCount > 0;
  const filteredJobs = showOverdueOnly ? scheduledJobsData.filter((job) => job.overdue) : scheduledJobsData;

  const buildScheduledJobsTableData = () =>
    filteredJobs.slice(currentPage * recordsPerPage, (currentPage + 1) * recordsPerPage).map((job) => {
      const row = [
        {
          component: <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{job.name}</Typography>,
        },
        { component: <Label tone={job.status.tone} dot text={job.status.text} /> },
        {
          component: (
            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              {job.schedule && <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>{job.schedule}</Typography>}
              <Typography sx={{ fontFamily: ds.font.mono, fontSize: ds.text.caption, color: ds.gray[500] }}>{job.cron || '-'}</Typography>
            </Box>
          ),
        },
        {
          component: job.lastExec ? (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              <Datetime value={job.lastExec} />
              {job.overdue && <Label tone='warning' text='Overdue' />}
            </Box>
          ) : (
            <Text value='-' />
          ),
        },
        { text: String(job.execCount) },
      ];
      // An overdue row is tinted the same amber family as its chip — the row is the unit a
      // reader scans, and the chip alone is easy to miss in a long list. Mixed with the
      // surface so a page of overdue jobs does not read as a solid block of warning.
      if (job.overdue) {
        row.sx = { backgroundColor: 'color-mix(in srgb, var(--ds-amber-100) 45%, var(--ds-background-100))' };
      }
      return row;
    });

  // Observability describes a K8s cluster's log/metric/trace backends. A cloud account has
  // none of that surface — the provider-status check only ever stamps K8s accounts — so it
  // would render an all-empty table; it is left out rather than offered and disappointing.
  const isK8sAccount = (selectedCluster?.cloud_provider || selectedCluster?.type || '').toLowerCase() === 'k8s';
  const optionsToDisplay = useMemo(() => {
    if (isVmAccount) return { tabOptions: [PROXY_AGENT_TAB] };
    return { tabOptions: isK8sAccount ? [AGENT_TAB, OBSERVABILITY_TAB, PROXY_AGENT_TAB] : [AGENT_TAB, PROXY_AGENT_TAB] };
  }, [isVmAccount, isK8sAccount]);

  // Sync tab from hash — runs on mount and on back/forward navigation
  useEffect(() => {
    const hash = router.asPath.split('#')[1] ?? '';
    const tab = optionsToDisplay.tabOptions.find((t) => t.fragment === hash);
    setActiveTab(tab ? tab.value : optionsToDisplay.tabOptions[0].value);
  }, [router.asPath, optionsToDisplay]);

  return (
    <Box sx={{ position: 'relative' }}>
      <Box sx={{ mt: 3 }}>
        <Tabs value={activeTab} onChange={setActiveTab} options={optionsToDisplay} />
      </Box>

      {activeTab === 0 && (
        <>
          {agentType === 'k8s' ? (
            <>
              {/* One card for what the agent IS: its status, when it last reported, and the
                  four facts that identify the install. The one-row table this replaced spent
                  a full table on five values and still hid the namespace and URL in a bullet
                  list below it. */}
              <ListingLayout id='agent-health' sx={{ mt: 3 }}>
                <ListingLayout.Header
                  icon={<SafeIcon src={AgentIconBlue} alt='' width={22} height={22} />}
                  title={
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                      <Box component='span'>Agent Health</Box>
                      <Label id='agent-status' size='md' dot tone={agentStatus.tone} text={agentStatus.text} />
                    </Box>
                  }
                  description={
                    agentSummary?.lastConnectedAt ? (
                      <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                        Last heartbeat <Datetime value={agentSummary.lastConnectedAt} />
                      </Box>
                    ) : (
                      'This cluster has not reported a heartbeat yet.'
                    )
                  }
                  actions={
                    shouldUpgrade && (
                      <DsButton
                        tone='secondary'
                        size='sm'
                        data-testid='agent-update-guide-btn'
                        icon={<OpenInNewIcon fontSize='small' />}
                        iconPlacement='end'
                        onClick={() => window.open(AGENT_UPGRADE_DOCS_URL, '_blank', 'noopener,noreferrer')}
                      >
                        View update guide
                      </DsButton>
                    )
                  }
                />
                <ListingLayout.Body sx={{ pt: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
                  {/* One banner, most severe first — per the Banner spec, two stacked on the
                      same surface is not allowed, and a disconnected agent makes the version
                      it last reported the less interesting of the two facts. */}
                  {agentSummary?.status === 'NOT_CONNECTED' ? (
                    <Banner surface='section' tone='critical' id='agent-disconnected-banner' message='The Agent is not connected' />
                  ) : disconnectedService.length > 0 ? (
                    <Banner
                      surface='section'
                      tone='critical'
                      id='agent-services-banner'
                      message={`The ${disconnectedService.join(', ')} services are disconnected.`}
                    />
                  ) : shouldUpgrade ? (
                    <Banner
                      surface='section'
                      tone='warning'
                      id='agent-update-banner'
                      title='Update available.'
                      message={`This cluster runs agent ${agentSummary?.version || 'an unreported version'}; the latest release is ${
                        latestVersions?.agent_version_latest ?? 'newer'
                      }.`}
                    />
                  ) : null}
                  <Box
                    sx={{
                      mt: disconnectedService.length > 0 || shouldUpgrade || agentSummary?.status === 'NOT_CONNECTED' ? 'var(--ds-space-4)' : 0,
                      display: 'grid',
                      gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr', lg: 'repeat(4, minmax(0, 1fr))' },
                      gap: ds.space[4],
                    }}
                  >
                    {[
                      { key: 'version', label: 'Agent version', value: agentSummary?.version, mono: true },
                      {
                        key: 'kubernetes',
                        label: 'Kubernetes',
                        value: [agentSummary?.k8sProvider, agentSummary?.k8sVersion].filter(Boolean).join(' · '),
                      },
                      { key: 'namespace', label: 'Namespace', value: agentFeatures.namespace, mono: true },
                      { key: 'agent-url', label: 'Agent URL', value: agentFeatures.agentUrl, mono: true },
                    ].map((fact) => (
                      <Box key={fact.key} data-testid={`agent-fact-${fact.key}`} sx={{ minWidth: 0 }}>
                        <Typography
                          sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[600], fontFamily: ds.font.display }}
                        >
                          {fact.label}
                        </Typography>
                        <Typography
                          sx={{
                            mt: ds.space[1],
                            fontSize: fact.mono ? ds.text.small : ds.text.body,
                            fontFamily: fact.mono ? ds.font.mono : undefined,
                            fontWeight: fact.mono ? ds.weight.regular : ds.weight.medium,
                            color: ds.gray[700],
                            overflowWrap: 'anywhere',
                          }}
                        >
                          {fact.value || '-'}
                        </Typography>
                      </Box>
                    ))}
                  </Box>
                </ListingLayout.Body>
              </ListingLayout>

              {/* And one card for what the agent TALKS TO. The bullet list this replaced
                  nested status, URL and per-component facts three levels deep, so no two
                  components could be compared without reading both of them in full. */}
              <ListingLayout id='agent-components' sx={{ mt: 3 }}>
                <ListingLayout.Toolbar title='Components'>
                  {/* lineHeight matches ToolbarTitle's own 32px line box — without it this
                      sits at the top of the flex-start row and reads as a superscript. */}
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: '32px' }}>{componentsSummary()}</Typography>
                </ListingLayout.Toolbar>
                <ListingLayout.Body>
                  <CustomTable
                    id='agent-components-table'
                    headers={HEADERS_COMPONENTS}
                    tableData={buildComponentsTableData()}
                    loading={loading}
                    // Centre each row: the name cell's icon tile is taller than one text
                    // line, so CustomTable's default top-alignment would leave the component
                    // name sitting below the status chip beside it.
                    sx={{ '&& tbody td': { verticalAlign: 'middle' } }}
                  />
                </ListingLayout.Body>
              </ListingLayout>
            </>
          ) : (
            <ListingLayout id='agent-health' sx={{ mt: 3 }}>
              <ListingLayout.Toolbar title='Agent Health' />
              <ListingLayout.Body>
                {data[0]?.status === 'NOT_CONNECTED' && <Typography color='red'>The Agent is not connected</Typography>}
                <CustomTable headers={HEADERS_CLOUD} tableData={agentHealthData} loading={loading} />

                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mt: 2, mb: 1 }}>Features</Typography>
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
              </ListingLayout.Body>
            </ListingLayout>
          )}
          {agentType === 'k8s' && scheduledJobsData.length > 0 && (
            <ListingLayout id='scheduled-jobs-table' sx={{ mt: 3 }}>
              <ListingLayout.Toolbar title='Scheduled Jobs'>
                {/* Same 32px line box as the title beside it — see the Components toolbar. */}
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: '32px' }}>
                  {scheduledJobsData.length} jobs
                  {overdueJobCount > 0 ? ` · ${overdueJobCount} past their expected run` : ''}
                </Typography>
                <ListingLayout.ToolbarSpacer />
                {/* Offered only when there is something to filter to — a lone "Overdue 0"
                    control is a dead end, and ToggleGroup's single mode always has a
                    selection, so there is no neutral state to fall back to. */}
                {overdueJobCount > 0 && (
                  <ToggleGroup
                    id='scheduled-jobs-filter'
                    selection='single'
                    size='sm'
                    ariaLabel='Filter scheduled jobs'
                    value={jobFilter}
                    onChange={(next) => {
                      setJobFilter(next);
                      // The filtered list is shorter, so whatever page the reader was on may
                      // no longer exist — landing them on an empty page reads as no data.
                      setCurrentPage(0);
                    }}
                    options={[
                      { value: 'all', label: `All ${scheduledJobsData.length}` },
                      { value: 'overdue', label: `Overdue ${overdueJobCount}` },
                    ]}
                  />
                )}
              </ListingLayout.Toolbar>
              <ListingLayout.Body>
                <CustomTable
                  id='scheduled-jobs'
                  headers={HEADERS_SCHEDULED_JOBS}
                  tableData={buildScheduledJobsTableData()}
                  loading={loading}
                  // Same reason as the Components table: the Schedule cell is two lines
                  // (readable form above the cron), so top-alignment would strand the job
                  // name, its status and its last run on the first of those two lines.
                  sx={{ '&& tbody td': { verticalAlign: 'middle' } }}
                  rowsPerPage={recordsPerPage}
                  totalRows={filteredJobs.length}
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

      {activeTab === 2 && (
        <>
          {!proxyLoading && proxyAgentHealthData.length === 0 ? (
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                justifyContent: 'center',
                padding: 'var(--ds-space-7) var(--ds-space-6)',
                borderRadius: 'var(--ds-radius-xl)',
                border: '1px solid var(--ds-gray-300)',
                background: 'var(--ds-background-100)',
                mt: 2,
              }}
            >
              <Box
                sx={{
                  width: 64,
                  height: 64,
                  borderRadius: 'var(--ds-radius-xl)',
                  background: 'linear-gradient(135deg, var(--ds-blue-100) 0%, var(--ds-blue-200) 100%)',
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

              <Typography
                sx={{
                  fontSize: 'var(--ds-text-title)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-foreground)',
                  mb: 1,
                  fontFamily: ds.font.display,
                }}
              >
                Get started with Proxy Agent monitoring
              </Typography>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body-lg)',
                  color: 'var(--ds-gray-600)',
                  mb: 4,
                  textAlign: 'center',
                  maxWidth: 'calc(var(--ds-space-0) * 230)',
                  lineHeight: 1.6,
                }}
              >
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
                  <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                    <Box
                      sx={{
                        width: 32,
                        height: 32,
                        borderRadius: 'var(--ds-radius-lg)',
                        background: 'var(--ds-blue-100)',
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
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-body)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: 'var(--ds-brand-500)',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {item.label}
                    </Typography>
                  </Box>
                ))}
              </Stack>

              {hasWriteAccess() ? (
                <DsButton tone='primary' size='md' onClick={() => router.push('/accounts/account-form?cloudProvider=VM_AGENT')}>
                  Connect VM Agent
                </DsButton>
              ) : (
                <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
                  Need admin permission to connect a VM agent
                </Typography>
              )}
            </Box>
          ) : (
            <>
              {isVmAccount ? (
                // Moved off /vm#agents: the foragers configured for this fleet and
                // the SSH connections each one can drive. Its Proxy Agents table
                // replaces Proxy Agent Health rather than sitting under it —
                // apiVm.listAgents decorates each vm_agent integration with this
                // same agents_list_health row (status → Connection, last_connected_at,
                // version), so the health card would only repeat it with less detail.
                <VmAgents accountId={router.query.accountId} />
              ) : (
                <ListingLayout id='proxy-agent-health' sx={{ mt: 3 }}>
                  <ListingLayout.Header
                    icon={<SafeIcon src={AgentIconBlue} alt='' width={22} height={22} />}
                    title={
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                        <Box component='span'>Proxy Agent</Box>
                        <Label id='proxy-agent-status' size='md' dot tone={proxyStatus.tone} text={proxyStatus.text} />
                      </Box>
                    }
                    description={
                      proxyData[0]?.last_connected_at ? (
                        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                          Last heartbeat <Datetime value={proxyData[0].last_connected_at} />
                        </Box>
                      ) : (
                        'This proxy agent has not reported a heartbeat yet.'
                      )
                    }
                  />
                  <ListingLayout.Body sx={{ pt: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
                    {proxyData[0]?.status === 'NOT_CONNECTED' && (
                      <Banner surface='section' tone='critical' id='proxy-agent-disconnected-banner' message='The Proxy Agent is not connected' />
                    )}
                    <Box
                      sx={{
                        mt: proxyData[0]?.status === 'NOT_CONNECTED' ? 'var(--ds-space-4)' : 0,
                        display: 'grid',
                        gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' },
                        gap: ds.space[4],
                      }}
                    >
                      <Box>
                        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: 2 }}>Instances</Typography>
                        <CustomTable headers={HEADERS_PROXY} tableData={proxyAgentHealthData} loading={proxyLoading} />
                      </Box>
                      <Box>
                        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: 2 }}>Features</Typography>
                        <CustomTable
                          headers={['Component', 'Status']}
                          tableData={[
                            [
                              { text: 'Relay' },
                              {
                                component: (
                                  <Label tone={proxyStatus.tone} dot text={proxyData[0]?.status === 'CONNECTED' ? 'Connected' : 'Disconnected'} />
                                ),
                              },
                            ],
                          ]}
                          loading={proxyLoading}
                        />
                      </Box>
                    </Box>
                  </ListingLayout.Body>
                </ListingLayout>
              )}
              {proxyDatasourcesData.length > 0 && (
                <ListingLayout id='proxy-datasources' sx={{ mt: 3 }}>
                  <ListingLayout.Toolbar title='Datasources'>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: '32px' }}>
                      {proxyDatasourcesData.length} datasources
                    </Typography>
                  </ListingLayout.Toolbar>
                  <ListingLayout.Body>
                    <CustomTable
                      id='proxy-datasources-table'
                      headers={HEADERS_PROXY_DATASOURCES}
                      tableData={proxyDatasourcesData.slice(proxyDsPage * proxyDsRowsPerPage, (proxyDsPage + 1) * proxyDsRowsPerPage)}
                      loading={proxyLoading}
                      rowsPerPage={proxyDsRowsPerPage}
                      totalRows={proxyDatasourcesData.length}
                      pageNumber={proxyDsPage + 1}
                      onPageChange={(page, limit) => {
                        setProxyDsPage(page - 1);
                        setProxyDsRowsPerPage(limit);
                      }}
                      expandable={{ component: ProxyDatasourceExpandedRow }}
                    />
                  </ListingLayout.Body>
                </ListingLayout>
              )}
            </>
          )}
        </>
      )}

      {activeTab === 1 && (
        <ListingLayout id='observability' sx={{ mt: 3 }}>
          <ListingLayout.Header
            title={
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                <Box component='span'>Observability</Box>
                <Label
                  id='observability-summary'
                  size='md'
                  dot
                  tone={connectedSignalCount === OBSERVABILITY_SIGNALS.length ? 'success' : 'warning'}
                  text={`${connectedSignalCount} of ${OBSERVABILITY_SIGNALS.length} connected`}
                />
              </Box>
            }
            description={
              <>
                Where this cluster&apos;s logs, metrics and traces actually come from. A signal served by an integration is checked directly against
                that backend, so its status is independent of the agent. Last Checked is when that check ran &mdash; for a signal the agent serves,
                when the agent last reported it.
              </>
            }
          />
          {/* One card per signal rather than three rows of a six-column table: every cell in
              that table was a short label, so most of its width went to whitespace while the
              provider and error strings — the two that actually vary — were the ones squeezed. */}
          <ListingLayout.Body sx={{ pt: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: { xs: '1fr', md: 'repeat(3, minmax(0, 1fr))' },
                gap: ds.space[4],
              }}
            >
              {observabilitySignals.map((signal) => (
                <Card
                  key={signal.key}
                  variant='outlined'
                  size='sm'
                  elevation='flat'
                  data-testid={`observability-${signal.key}`}
                  header={
                    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2] }}>
                      <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], fontFamily: ds.font.display }}>
                        {signal.label}
                      </Typography>
                      {signal.configured ? (
                        <Label dot tone={signal.connected ? 'success' : 'critical'} text={signal.connected ? 'Connected' : 'Disconnected'} />
                      ) : (
                        <Label tone='neutral' text='Not configured' />
                      )}
                    </Box>
                  }
                >
                  <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                    {[
                      { label: 'Provider', value: signal.provider, mono: true },
                      { label: 'Source', value: signal.source },
                      { label: 'Last checked', value: signal.lastChecked ? <Datetime value={signal.lastChecked} /> : '' },
                      { label: 'Error', value: signal.error, tone: signal.error ? 'error' : undefined },
                    ].map((row) => (
                      <Box
                        key={row.label}
                        sx={{
                          display: 'flex',
                          alignItems: 'baseline',
                          justifyContent: 'space-between',
                          gap: ds.space[2],
                          py: ds.space[2],
                          borderBottom: `1px solid ${ds.gray[200]}`,
                          '&:last-of-type': { borderBottom: 0 },
                        }}
                      >
                        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], flexShrink: 0 }}>{row.label}</Typography>
                        <Typography
                          component='div'
                          sx={{
                            fontSize: row.mono ? ds.text.small : ds.text.body,
                            fontFamily: row.mono ? ds.font.mono : undefined,
                            color: row.tone === 'error' ? ds.red[500] : row.value ? ds.gray[700] : ds.gray[500],
                            textAlign: 'right',
                            overflowWrap: 'anywhere',
                            minWidth: 0,
                          }}
                        >
                          {/* "None" for an error, "-" everywhere else: an empty Error row is
                              good news and should read that way, an empty Provider row is
                              simply an answer the page does not have. */}
                          {row.value || (row.label === 'Error' ? 'None' : '-')}
                        </Typography>
                      </Box>
                    ))}
                    {/* Full width, not a label/value row: it is a sentence about the
                        integration, and the value column holds short strings. */}
                    {signal.note && (
                      <Box
                        data-testid={`observability-${signal.key}-note`}
                        sx={{
                          mt: ds.space[2],
                          p: ds.space[2],
                          borderRadius: 'var(--ds-radius-2)',
                          backgroundColor: ds.amber[100],
                          border: `1px solid ${ds.amber[300]}`,
                        }}
                      >
                        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{signal.note}</Typography>
                      </Box>
                    )}
                  </Box>
                </Card>
              ))}
            </Box>
          </ListingLayout.Body>
        </ListingLayout>
      )}
    </Box>
  );
};

export default withAccountGuard(AgentHealth, { hideContent: true });
