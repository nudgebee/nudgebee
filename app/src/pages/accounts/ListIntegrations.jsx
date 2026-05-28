import apiAccount from '@api1/account';
import apiIntegrations from '@api1/integrations';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import apiWorkflow from '@api1/workflow';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { getDateDiff } from '@lib/datetime';
import IntegrationDynamicFormModal from '@components1/common/IntegrationDynamicFormModal';
import AddLLMConfigModal from '@components1/llm/AddLLMConfigModal';
import VmAgentCredentialsDialog from '@components1/common/VmAgentCredentialsDialog';
import CustomLabels from '@common-new/widgets/CustomLabels';
import NDialog from '@common-new/modal/NDialog';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomTable from '@common-new/tables/CustomTable2';
import { hasWriteAccess } from '@lib/auth';
import { action } from 'src/utils/actionStyles';
import FiberManualRecordIcon from '@mui/icons-material/FiberManualRecord';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import { Grid, Stack, Typography } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import { useSession } from 'next-auth/react';
import { useRouter } from 'next/router';
import { useEffect, useMemo, useState } from 'react';
import { parseHttpResponseBodyMessage, safeJSONParse, snakeToTitleCase, toKebabCase } from 'src/utils/common';

const getDisplayName = (name) => {
  if (name === 'ES') {
    return 'Elasticsearch';
  }
  if (name === 'vm_agent') {
    return 'Proxy Agent';
  }
  if (name === 'ssh') {
    return 'SSH';
  }
  return snakeToTitleCase(name);
};

// Maps integration name to the config key that holds its primary connection info
const integrationConnectionKey = {
  postgresql: 'host',
  postgres: 'host',
  clickhouse: 'host',
  mysql: 'host',
  mssql: 'host',
  oracle: 'host',
  mongodb_proxy: 'host',
  redis: 'host',
  rabbitmq: 'host',
  ssh: 'host',
  jira: 'url',
  servicenow: 'url',
  pagerduty: 'url',
  zenduty: 'url',
  github: 'url',
  gitlab: 'url',
  confluence: 'host',
  ES: 'url',
  pinot: 'pinot_url',
  hive: 'hive_url',
  jaeger: 'jaeger_query_url',
  datadog: 'site',
  newrelic: 'region',
  splunk_observability_platform: 'realm',
  chronosphere: 'chronosphere_url',
  signoz: 'signoz_url',
  observe: 'domain',
  azure_app_insights: 'azure_app_insights_app_id',
  dynatrace: 'base_url',
  solarwinds: 'data_center',
  last9: 'endpoint',
  loggly: 'subdomain',
  http_proxy: 'base_url',
  mcp_proxy: 'url',
  mcp: 'url',
  kafka_proxy: 'brokers',
  llm: 'llm_provider_api_endpoint',
  argocd: 'server',
};

const getConnectionInfo = (integrationName, configValues) => {
  const key = integrationConnectionKey[integrationName];
  if (!key || !configValues) return '-';
  const entry = configValues.find((c) => c.name === key);
  return entry?.value || '-';
};

const ListIntegrations = ({ integrationName }) => {
  const integrationsWithCautionMessage = [
    'pagerduty_webhook',
    'zenduty_webhook',
    'prometheus_alertmanager_webhook',
    'datadog_webhook',
    'postgres',
    'postgresql',
    'clickhouse',
    'datadog',
    'argocd',
    'mysql',
    'mssql',
    'oracle',
    'rabbitmq',
    'redis',
    'azure_monitor_webhook',
    'servicenow_webhook',
    'newrelic_webhook',
    'splunk_webhook',
    'gcp_monitoring_webhook',
    'dynatrace_webhook',
    'solarwinds_webhook',
  ];
  const integrationWebhooks = [
    'pagerduty_webhook',
    'zenduty_webhook',
    'prometheus_alertmanager_webhook',
    'datadog_webhook',
    'azure_monitor_webhook',
    'servicenow_webhook',
    'newrelic_webhook',
    'splunk_webhook',
    'gcp_monitoring_webhook',
    'dynatrace_webhook',
    'solarwinds',
    'solarwinds_webhook',
  ];
  const agentManagedIntegrations = ['ES', 'loki', 'prometheus', 'otel_clickhouse', 'jaeger'];
  const hideConnectionInfo =
    integrationName === 'LLM' ||
    integrationName === 'vm_agent' ||
    integrationName?.endsWith('_webhook') ||
    integrationWebhooks.includes(integrationName);
  const headers = hideConnectionInfo
    ? ['Name', 'Account', 'Created By', 'Updated By', 'Status', '']
    : ['Name', 'Connection Info', 'Account', 'Created By', 'Updated By', 'Status', ''];

  const { data: session } = useSession();
  const router = useRouter();
  // accountId for the LLM modal's agent-overrides fetch (ai_list_agents
  // requires an account_id). Admin Integrations is tenant-wide so it has
  // no accountId in the URL by default. Fallback chain:
  //   1. ?accountId= query param (set when navigating from Nubi via the
  //      "Manage in Admin" banner so context follows the user)
  //   2. session.accountIds[0] — first account the user has access to
  //   3. apiUser.listAccounts() → first row — fetched once on mount for
  //      pages reached directly from side-nav where neither (1) nor (2)
  //      are populated yet
  const [fetchedAccountId, setFetchedAccountId] = useState('');
  const urlAccountId = typeof router?.query?.accountId === 'string' ? router.query.accountId : '';
  const sessionAccountId = session?.accountIds?.[0] || '';
  const llmModalAccountId = urlAccountId || sessionAccountId || fetchedAccountId;
  useEffect(() => {
    if (integrationName !== 'LLM') {
      return;
    }
    if (urlAccountId || sessionAccountId || fetchedAccountId) {
      return;
    }
    let cancelled = false;
    apiUser
      .listAccounts()
      .then((rows) => {
        if (cancelled) {
          return;
        }
        const first = Array.isArray(rows) ? rows.find((r) => r?.id)?.id : undefined;
        if (first) {
          setFetchedAccountId(first);
        }
      })
      .catch(() => {
        // No-op: leaves agent dropdown empty rather than blocking the
        // modal. The user can still set provider/model globally.
      });
    return () => {
      cancelled = true;
    };
  }, [integrationName, urlAccountId, sessionAccountId, fetchedAccountId]);
  const [openModal, setOpenModal] = useState(false);
  const [loading, setLoading] = useState(false);
  const [modalAction, setModalAction] = useState(null);
  const [isActionLoading, setIsActionLoading] = useState(false);
  const [selectedIntegration, setSelectedIntegration] = useState({});
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState(0);
  const [totalCount, setTotalCount] = useState(0);
  const [integrationData, setIntegrationData] = useState([]);
  const [openSetupGuide, setOpenSetupGuide] = useState(false);
  const [regeneratedCredentials, setRegeneratedCredentials] = useState(null);
  const [testingConnectionId, setTestingConnectionId] = useState(null);
  const [testableConfig, setTestableConfig] = useState({ testable: false, testable_when: null });
  const [agentHealthMap, setAgentHealthMap] = useState({});
  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('enabled');

  const handleTestConnection = async (item) => {
    setTestingConnectionId(item.id);
    try {
      const result = await apiIntegrations.testIntegrationConnection(item.id);
      if (result?.success) {
        snackbar.success(`${getDisplayName(integrationName)} connection successful`);
      } else {
        snackbar.error(result?.error || `${getDisplayName(integrationName)} connection test failed`);
      }
    } catch {
      snackbar.error(`Failed to test ${getDisplayName(integrationName)} connection`);
    } finally {
      setTestingConnectionId(null);
    }
  };

  const getMenuItems = (item) => {
    if (!hasWriteAccess()) return [];
    const status = item.status || 'enabled';
    const items = [];
    if (item?.source === 'agent' && agentManagedIntegrations.includes(integrationName)) {
      if (status === 'disabled') {
        items.push({ label: 'Enable', id: 'enable' });
      } else {
        items.push({ label: 'Disable', id: 'disable' });
        items.push({ label: 'Edit', id: 'edit' });
      }
    } else if (item?.source !== 'agent') {
      if (status === 'disabled') {
        items.push({ label: 'Enable', id: 'enable' });
      } else {
        items.push({ label: 'Disable', id: 'disable' });
        items.push({ label: 'Edit', id: 'edit' });
      }
      if (!integrationWebhooks.includes(integrationName)) {
        items.push({ label: 'Delete', id: 'delete' });
      }
      if (integrationName === 'vm_agent' && status !== 'disabled') {
        items.push({ label: 'Regenerate Token', id: 'regenerate' });
      }
    }
    const isItemTestable = (() => {
      if (!testableConfig.testable) return false;
      if (!testableConfig.testable_when) return true;
      const configValues = item?.integration_config_values || [];
      return Object.entries(testableConfig.testable_when).every(([key, value]) => {
        const cv = configValues.find((c) => c.name === key);
        return cv?.value === value;
      });
    })();
    if (status !== 'disabled' && isItemTestable) {
      items.push({
        label: testingConnectionId === item.id ? 'Testing...' : 'Test Connection',
        id: 'test_connection',
        disabled: testingConnectionId === item.id,
      });
    }
    return items;
  };

  const findAssociatedWorkflowForWebhook = async (item) => {
    if (integrationName !== 'workflow_webhook') return null;
    const configValues = Array.isArray(item?.integration_config_values) ? item.integration_config_values : [];
    const workflowId = configValues.find((c) => c.name === 'workflow_id')?.value;
    if (!workflowId) return null;
    const accountId = item?.integrations_cloud_accounts?.[0]?.cloud_account_id;
    if (!accountId) return null;
    try {
      const response = await apiWorkflow.getWorkflowById(accountId, workflowId);
      return response?.data?.workflow_get || null;
    } catch {
      return null;
    }
  };

  const onMenuClick = async (menuItem, item) => {
    if (menuItem.id === 'edit') {
      // AddLLMConfigModal consumes the raw integration row directly; the
      // flatten-into-integration_config_values transform below is what
      // IntegrationDynamicFormModal expects, not the LLM modal.
      if (integrationName === 'LLM') {
        setSelectedIntegration(item);
      } else {
        setSelectedIntegration({
          ...item,
          integration_config_values: {
            ...(item?.integration_config_values || []).reduce((acc, { name, value }) => {
              acc[name] = value;
              return acc;
            }, {}),
            account_id: item?.integrations_cloud_accounts?.map((d) => d.cloud_account_id),
            integration_config_name: item?.name,
          },
        });
      }
      setOpenModal(true);
    } else if (menuItem.id === 'delete' || menuItem.id === 'disable') {
      if (integrationName === 'workflow_webhook') {
        const workflow = await findAssociatedWorkflowForWebhook(item);
        if (workflow) {
          snackbar.error(
            `Cannot ${menuItem.id} this webhook - automation "${workflow.name || workflow.id}" is using it. Remove or update the automation first.`
          );
          return;
        }
      }
      setSelectedIntegration({ ...item });
      setModalAction(menuItem.id);
    } else if (menuItem.id === 'enable') {
      setSelectedIntegration({ ...item });
      setModalAction('enable');
    } else if (menuItem.id === 'regenerate') {
      setSelectedIntegration({ ...item });
      setModalAction('regenerate');
    } else if (menuItem.id === 'test_connection') {
      handleTestConnection(item);
    }
  };

  const handleStatusFilterChange = (e) => {
    setSelectedStatusFilter(e.target.value);
    setCurrentPage(0);
  };

  useEffect(() => {
    apiIntegrations
      .listIntegrationSchema({
        integration_name: integrationName === 'postgres' ? 'postgresql' : integrationName,
        source: 'user',
      })
      .then((res) => {
        const schema = safeJSONParse(res?.data?.data?.integrations_get_schema?.data);
        setTestableConfig({ testable: !!schema?.testable, testable_when: schema?.testable_when || null });
      });
  }, [integrationName]);

  useEffect(() => {
    listIntegrationConfiguration();
  }, [recordsPerPage, currentPage, selectedNameFilter, selectedStatusFilter, integrationName]);

  const getAgentConnectionInfo = (item) => {
    const accountId = item?.integrations_cloud_accounts?.[0]?.cloud_account_id;
    const health = accountId ? agentHealthMap[accountId] : null;
    if (!health) {
      return { text: '-' };
    }
    const isConnected = health.status === 'CONNECTED';
    const statusColor = isConnected ? '#12B76A' : '#F04438';
    let timeAgo = '';
    if (health.last_connected_at) {
      const diff = getDateDiff(health.last_connected_at);
      if (diff.days > 0) {
        timeAgo = `${diff.days}d ago`;
      } else if (diff.hours > 0) {
        timeAgo = `${diff.hours}h ago`;
      } else if (diff.minutes > 0) {
        timeAgo = `${diff.minutes}m ago`;
      } else {
        timeAgo = 'just now';
      }
    }
    return {
      component: (
        <Stack
          direction='row'
          alignItems='center'
          spacing={0.5}
          onClick={() => (window.location.href = `/agentHealth?accountId=${accountId}#proxy-agent`)}
          sx={{ cursor: 'pointer', '&:hover .details-icon': { opacity: 1 } }}
        >
          <FiberManualRecordIcon sx={{ fontSize: 8, color: statusColor }} />
          <Typography component='span' sx={{ fontSize: 'var(--ds-text-body)', color: statusColor, fontWeight: 'var(--ds-font-weight-medium)' }}>
            {isConnected ? 'Connected' : 'Disconnected'}
          </Typography>
          {timeAgo && (
            <Typography component='span' sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
              &middot; {timeAgo}
            </Typography>
          )}
          <OpenInNewIcon className='details-icon' sx={{ fontSize: 14, color: 'var(--ds-gray-700)', opacity: 0, transition: 'opacity 0.2s' }} />
        </Stack>
      ),
    };
  };

  const parseIntegrationItem = (item) => {
    return {
      ...item,
      integrations_cloud_accounts: safeJSONParse(item?.integrations_cloud_accounts) || [],
      integration_config_values: safeJSONParse(item?.integration_config_values) || [],
    };
  };

  const buildIntegrationRow = (item) => {
    const connectionInfoCell =
      integrationName === 'vm_agent'
        ? getAgentConnectionInfo(item)
        : { text: getConnectionInfo(integrationName === 'postgres' ? 'postgresql' : integrationName, item.integration_config_values) };
    const baseRow = [
      { text: item.name },
      { text: item?.integrations_cloud_accounts?.map((d) => d?.cloud_account_name)?.join(', ') || '-' },
      { text: item?.source == 'agent' ? 'agent' : item?.created_by_display_name || '-' },
      { text: item?.source == 'agent' ? 'agent' : item?.updated_by_display_name || '-' },
      { component: <CustomLabels text={item.status || '-'} /> },
      {
        component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />,
      },
    ];
    if (hideConnectionInfo) {
      return baseRow;
    }
    return [baseRow[0], connectionInfoCell, ...baseRow.slice(1)];
  };

  // Derive tableData from source state to avoid stale async updates
  const tableData = useMemo(
    () => integrationData.map(buildIntegrationRow),
    [integrationData, agentHealthMap, testingConnectionId, testableConfig, integrationName]
  );

  const listIntegrationConfigurationById = (id) => {
    apiIntegrations.listIntegrations({ id, limit: 1, offset: 0 }).then((res) => {
      const integrations = res?.data?.data?.admin_get_integrations_v2?.rows || [];
      if (integrations?.length > 0) {
        const updated = parseIntegrationItem(integrations[0]);
        setIntegrationData((prev) => prev.map((item) => (item.id === id ? updated : item)));
      }
    });
  };

  const fetchAgentHealth = () => {
    k8sApi.getAgentHealth({ type: 'proxy' }).then((res) => {
      const healthData = res?.data || [];
      const healthMap = {};
      for (const agent of healthData) {
        if (agent.cloud_account_id) {
          healthMap[agent.cloud_account_id] = agent;
        }
      }
      setAgentHealthMap(healthMap);
    });
  };

  const listIntegrationConfiguration = () => {
    setLoading(true);
    apiIntegrations
      .listIntegrations({
        type: integrationName === 'postgres' ? 'postgresql' : integrationName === 'ES' ? 'ES' : integrationName.toLowerCase(),
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
        name: selectedNameFilter || undefined,
        status: selectedStatusFilter || undefined,
      })
      .then((res) => {
        setLoading(false);
        const rawIntegrations = res?.data?.data?.admin_get_integrations_v2?.rows || [];
        setTotalCount(res?.data?.data?.admin_get_integrations_grouping_v2?.rows?.[0]?.count || 0);
        const integrations = rawIntegrations.map(parseIntegrationItem);
        setIntegrationData(integrations);
        if (integrationName === 'vm_agent' && integrations.length > 0) {
          fetchAgentHealth();
        }
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const closeModal = (trigger) => {
    setOpenModal(false);
    setSelectedIntegration({});
    if (trigger) {
      if (selectedIntegration.name) {
        snackbar.success(`${getDisplayName(integrationName)} account updated successfully`);
      } else {
        snackbar.success(`${getDisplayName(integrationName)} account created successfully`);
      }
      listIntegrationConfiguration();
    }
  };

  const handleDelete = () => {
    if (!selectedIntegration.name) {
      snackbar.error(`Name is missing`);
      return;
    }
    const requestBody = {
      integration_config_name: selectedIntegration.name,
      integration_name: integrationName == 'postgres' ? 'postgresql' : integrationName,
      source: 'user',
    };
    setIsActionLoading(true);
    apiIntegrations
      .deleteIntegrations(requestBody)
      .then((res) => {
        const response = res?.data?.data?.integrations_delete_config?.status || '';
        if (response == 'success') {
          snackbar.success(`Deleted the ${integrationName} subscription`);
          setModalAction(null);
          listIntegrationConfiguration();
        } else {
          snackbar.error(`Failed to delete the ${getDisplayName(integrationName)} subscription - ${parseHttpResponseBodyMessage(res?.data)}`);
        }
      })
      .finally(() => {
        setIsActionLoading(false);
      });
  };

  const handleStatusChange = () => {
    if (!selectedIntegration.name) {
      snackbar.error(`Name is missing`);
      return;
    }
    const requestBody = {
      integration_config_name: selectedIntegration.name,
      integration_name: integrationName == 'postgres' ? 'postgresql' : integrationName.toLowerCase(),
      integration_config_status: (selectedIntegration.status || 'enabled') == 'enabled' ? 'disabled' : 'enabled',
    };
    setIsActionLoading(true);
    apiIntegrations
      .updateIntegrationStatus(requestBody)
      .then((res) => {
        const response = res?.data?.data?.integrations_update_status?.status || '';
        if (response == 'success') {
          snackbar.success(`${(selectedIntegration.status || 'enabled') == 'enabled' ? 'Disabled' : 'Enabled'} the ${integrationName} subscription`);
          setModalAction(null);
          listIntegrationConfiguration();
        } else {
          snackbar.error(`Failed to disable the ${getDisplayName(integrationName)} subscription - ${parseHttpResponseBodyMessage(res?.data)}`);
        }
      })
      .finally(() => {
        setIsActionLoading(false);
      });
  };

  const handleRegenerate = () => {
    const accountId = selectedIntegration?.integrations_cloud_accounts?.[0]?.cloud_account_id;
    if (!accountId) {
      snackbar.error('No account associated with this agent');
      return;
    }
    setIsActionLoading(true);
    apiAccount
      .generateAgentToken(accountId, 'proxy')
      .then((res) => {
        const data = res?.data?.data?.agent_token_create;
        if (data?.access_secret) {
          setRegeneratedCredentials({
            accessKey: data.access_key,
            accessSecret: data.access_secret,
          });
          setModalAction(null);
        } else {
          snackbar.error('Failed to regenerate token');
        }
      })
      .catch(() => {
        snackbar.error('Failed to regenerate token');
      })
      .finally(() => {
        setIsActionLoading(false);
      });
  };

  const handleConfirmationClose = () => {
    setModalAction(null);
    setSelectedIntegration({});
  };

  const handleConfirmationSubmit = () => {
    if (modalAction === 'delete') {
      handleDelete();
    } else if (modalAction === 'disable' || modalAction == 'enable') {
      handleStatusChange();
    } else if (modalAction === 'regenerate') {
      handleRegenerate();
    }
  };

  return (
    <>
      <VmAgentCredentialsDialog
        open={!!regeneratedCredentials}
        onClose={() => setRegeneratedCredentials(null)}
        accessKey={regeneratedCredentials?.accessKey}
        accessSecret={regeneratedCredentials?.accessSecret}
      />
      {integrationName === 'solarwinds' && (
        <NDialog
          handleClose={() => setOpenSetupGuide(false)}
          buttonText='Close'
          dialogTitle={
            <Typography component='h2' variant='h6' fontWeight={600}>
              SolarWinds Observability Setup Guide
            </Typography>
          }
          dialogContent={
            <Stack spacing={2} sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
              <Typography variant='subtitle2' fontWeight={600}>
                Step 1: Choose your Data Center Region
              </Typography>
              <Typography variant='body2' component='div'>
                Your region is visible in the URL when you log in to SolarWinds Observability (e.g. <code>my.ap-01.cloud.solarwinds.com</code>).
                Available regions:
                <ul style={{ paddingLeft: '18px', marginTop: '6px' }}>
                  <li>
                    <code>na-01</code> — North America 1
                  </li>
                  <li>
                    <code>na-02</code> — North America 2
                  </li>
                  <li>
                    <code>eu-01</code> — Europe 1
                  </li>
                  <li>
                    <code>ap-01</code> — Asia Pacific 1
                  </li>
                </ul>
              </Typography>
              <Typography variant='subtitle2' fontWeight={600}>
                Step 2: Create an API Access Token
              </Typography>
              <Typography variant='body2' component='div'>
                <ol style={{ paddingLeft: '18px', marginTop: '6px' }}>
                  <li>Log in to SolarWinds Observability</li>
                  <li>
                    Go to <strong>Settings → API Tokens</strong>
                  </li>
                  <li>
                    Click <strong>Add Token</strong>, select the <strong>API Access</strong> scope (not Ingestion)
                  </li>
                  <li>Copy the generated token value</li>
                </ol>
              </Typography>
              <Typography variant='body2' sx={{ color: 'text.secondary' }}>
                Enter the <strong>Data Center Region</strong> and <strong>API Access Token</strong> in the form above to connect Nudgebee to your
                SolarWinds account.
              </Typography>
            </Stack>
          }
          open={openSetupGuide}
          additionalComponent={undefined}
          handleSubmit={() => setOpenSetupGuide(false)}
        />
      )}
      {integrationName === 'splunk_observability_platform' && (
        <NDialog
          handleClose={() => setOpenSetupGuide(false)}
          buttonText='Close'
          dialogTitle={
            <Typography component='h2' variant='h6' fontWeight={600}>
              Splunk Observability Cloud Setup Guide
            </Typography>
          }
          dialogContent={
            <Stack spacing={2} sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
              <Typography variant='subtitle2' fontWeight={600}>
                Step 1: Find your Realm
              </Typography>
              <Typography variant='body2' component='div'>
                The realm identifies your Splunk Observability Cloud region (e.g. <code>us1</code>, <code>us0</code>, <code>eu0</code>).
                <ol style={{ paddingLeft: '18px', marginTop: '6px' }}>
                  <li>Log in to Splunk Observability Cloud</li>
                  <li>
                    Go to <strong>Settings → Organizations</strong> and note the <strong>Realm</strong> value
                  </li>
                </ol>
              </Typography>
              <Typography variant='subtitle2' fontWeight={600}>
                Step 2: Create an Organization Access Token
              </Typography>
              <Typography variant='body2' component='div'>
                <ol style={{ paddingLeft: '18px', marginTop: '6px' }}>
                  <li>
                    Go to <strong>Settings → Access Tokens</strong>
                  </li>
                  <li>
                    Click <strong>New Token</strong>, give it a name, and select the <strong>API</strong> authorization scope
                  </li>
                  <li>Copy the generated token value</li>
                </ol>
              </Typography>
              <Typography variant='body2' sx={{ color: 'text.secondary' }}>
                Enter the <strong>Realm</strong> and <strong>Access Token</strong> in the form above to connect Nudgebee to your Splunk Observability
                Cloud account.
              </Typography>
            </Stack>
          }
          open={openSetupGuide}
          additionalComponent={undefined}
          handleSubmit={() => setOpenSetupGuide(false)}
        />
      )}
      <NDialog
        handleClose={handleConfirmationClose}
        buttonText='Submit'
        dialogTitle={
          <Typography component='h2' variant='h6' fontWeight={600}>
            {`${snakeToTitleCase(modalAction || '')} ${getDisplayName(integrationName)} Integration: ${selectedIntegration.name}`}
          </Typography>
        }
        dialogContent={
          <>
            {modalAction === 'regenerate'
              ? 'Are you sure you want to regenerate the token? Existing agent connections will stop working until updated with new credentials.'
              : `Are you sure you want to ${modalAction} this "${selectedIntegration.name}" - "${getDisplayName(integrationName)}" integration?`}
            {(modalAction === 'delete' || modalAction === 'disable') && integrationsWithCautionMessage.includes(integrationName) ? (
              <Grid
                container
                mt={2}
                mb={1}
                mr={1}
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                }}
              >
                <Typography variant='subtitle1' sx={{ fontSize: 'var(--ds-text-body-lg)' }}>
                  Caution:
                </Typography>
                <Grid
                  container
                  borderRadius={2}
                  p={2}
                  sx={{
                    margin: '15px 0 30px 0px',
                    display: 'flex',
                    flexDirection: 'row',
                    justifyContent: 'space-between',
                    border: '1px solid var(--ds-gray-300)',
                  }}
                >
                  <Grid
                    item
                    xs={11}
                    sx={{
                      overflowY: 'auto',
                      maxHeight: '100px',
                      display: 'flex',
                    }}
                  >
                    <span style={{ textAlign: 'justify' }}>
                      {`Deleting the ${snakeToTitleCase(
                        integrationName
                      )} subscription token may disrupt active integrations and prevent critical alerts from being delivered.
                If this token is configured in any application, it will no longer receive event notifications from ${snakeToTitleCase(
                  integrationName
                )}. Ensure that the token
                is not in use before proceeding with deletion. 🚨`}
                    </span>
                  </Grid>
                </Grid>
              </Grid>
            ) : // --- CHANGED ---
            modalAction === 'delete' && integrationName === 'confluence' ? (
              <span>{`Deleting the confluence integration will lead to loss of documentation access. 🚨`}</span>
            ) : null}
            {/* --- END CHANGE --- */}
          </>
        }
        open={!!modalAction} // Opens for 'delete', 'disable', or 'enable'
        additionalComponent={undefined}
        handleSubmit={handleConfirmationSubmit}
        loading={isActionLoading}
      />

      {integrationName === 'LLM' ? (
        // LLM uses the custom modal (tier overrides + per-agent overrides +
        // dynamic agent list + secret reveal). The generic dynamic form
        // can't express these capabilities.
        //
        // onClose passes trigger=false so ListIntegrations.closeModal does
        // not emit its generic "<integration> account updated/created"
        // snackbar on plain dismiss. AddLLMConfigModal emits its own
        // success snackbar from within its save handler, then calls
        // onSaved — that's the only signal we need to refetch.
        <AddLLMConfigModal
          open={openModal}
          onClose={() => closeModal(false)}
          editData={selectedIntegration && selectedIntegration.id ? selectedIntegration : null}
          onSaved={listIntegrationConfigurationById}
          accountId={llmModalAccountId}
        />
      ) : (
        <IntegrationDynamicFormModal
          openModal={openModal}
          handleClose={closeModal}
          integrationName={integrationName == 'postgres' ? 'postgresql' : integrationName}
          title={selectedIntegration.name ? `Edit ${getDisplayName(integrationName)} Account` : `Add ${getDisplayName(integrationName)} Account`}
          integrationData={integrationData}
          editData={selectedIntegration || {}}
          listIntegrationConfigurationById={listIntegrationConfigurationById}
        />
      )}
      <ListingLayout id={`${integrationName}-integrations`}>
        <ListingLayout.Toolbar
          title={
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={'var(--ds-gray-700)'} fontSize='16px' fontWeight={600}>
                {getDisplayName(integrationName)}
              </Typography>
              <CloudProviderIcon cloud_provider={integrationName} />
              {integrationName === 'splunk_observability_platform' && (
                <Tooltip title='View Splunk Observability Cloud setup guide'>
                  <span>
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      size='xs'
                      icon={<InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)' }} />}
                      aria-label='View Splunk Observability Cloud setup guide'
                      onClick={() => setOpenSetupGuide(true)}
                    />
                  </span>
                </Tooltip>
              )}
              {integrationName === 'solarwinds' && (
                <Tooltip title='View SolarWinds Observability setup guide'>
                  <span>
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      size='xs'
                      icon={<InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)' }} />}
                      aria-label='View SolarWinds Observability setup guide'
                      onClick={() => setOpenSetupGuide(true)}
                    />
                  </span>
                </Tooltip>
              )}
            </Stack>
          }
          actions={
            integrationName != 'loki' && integrationName != 'prometheus' && integrationName != 'otel_clickhouse' && hasWriteAccess() ? (
              <DsButton
                id={`add-${toKebabCase(integrationName)}-account-btn`}
                tone='primary'
                size='md'
                onClick={() => {
                  setSelectedIntegration({});
                  setOpenModal(true);
                }}
                aria-label={`Add ${integrationName} Account`}
              >
                {`Add ${getDisplayName(integrationName)} Account`}
              </DsButton>
            ) : undefined
          }
        >
          {(() => {
            const statusOptions = [
              { label: 'Enabled', value: 'enabled' },
              { label: 'Disabled', value: 'disabled' },
            ];
            const selectedStatusOption = statusOptions.find((o) => o.value === selectedStatusFilter) ?? null;
            return (
              <FilterDropdown
                id={`${integrationName}-status-filter`}
                label='Status'
                options={statusOptions}
                value={selectedStatusOption}
                onSelect={(_e, item) => handleStatusFilterChange({ target: { value: item?.value || '' } })}
              />
            );
          })()}
          <CustomSearch
            id={`${integrationName}-name-search`}
            value={nameInput}
            onChange={(next) => {
              setNameInput((prev) => {
                if (prev.trim() !== '' && next.trim() === '') {
                  setSelectedNameFilter('');
                  setCurrentPage(0);
                }
                return next;
              });
            }}
            onEnterPress={() => {
              setSelectedNameFilter(nameInput);
              setCurrentPage(0);
            }}
            onClear={() => {
              setNameInput('');
              setSelectedNameFilter('');
              setCurrentPage(0);
            }}
            label='Enter Name'
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={`${integrationName}`}
            loading={loading}
            tableData={tableData}
            headers={headers}
            totalRows={totalCount}
            rowsPerPage={recordsPerPage}
            pageNumber={currentPage + 1}
            onPageChange={(page, limit) => {
              setCurrentPage(page - 1);
              setRecordsPerPage(limit);
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default ListIntegrations;
