import { Box, Button, Grid, Stack, Typography } from '@mui/material';
import React, { useEffect, useState, useMemo, useCallback } from 'react';
import CloudProviderIcon from '@common/CloudIcon';
import apiAccount from '@api1/account';
import { useRouter } from 'next/router';
import { getCloudProviderLabel } from 'src/utils/common';
import Loader from '@components1/common/Loader';
import { ds } from 'src/utils/colors';
import CustomTabs from '@common-new/CustomTabs';
import CustomSearch from '@common-new/CustomSearch';
import { ListingLayout } from '@components1/ds/ListingLayout';
import {
  CloudAccountIcon,
  MessageBlueIcon,
  TicketBlueIcon,
  DataBaseBlueIcon,
  QueueBlueIcon,
  InMemoryIcon,
  RepoBlueIcon,
  DatadogIcon,
  TerminalIcon,
  TroubleshootIconBlue,
} from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

// --- CONFIGURATION ---
const DISABLED_PROVIDERS = new Set(['SPLUNK', 'SPLUNK_OBSERVABILITY_PLATFORM', 'SPLUNK_WEBHOOK', 'GRAFANA-TEMPO', 'BITBUCKET', 'LAST9']);
// Constants moved to top level for better organization
const PROVIDERS = {
  CLOUD: ['K8S', 'AWS', 'AZURE', 'GCP', 'CLOUDFOUNDRY'],
  MGMNT_TOOL: ['JIRA', 'SERVICENOW', 'PAGERDUTY'],
  WEBHOOKS: [
    'PAGERDUTY_WEBHOOK',
    'PROMETHEUS_ALERTMANAGER_WEBHOOK',
    'GRAFANA_WEBHOOK',
    'DATADOG_WEBHOOK',
    'AZURE_MONITOR_WEBHOOK',
    'SERVICENOW_WEBHOOK',
    'SPLUNK_WEBHOOK',
    'SOLARWINDS_WEBHOOK',
    'WORKFLOW_WEBHOOK',
  ],
  REPOS: ['GITHUB'],
  CI_CD: ['ARGOCD', 'GITHUB'],
  QUEUE: ['RABBITMQ'],
  DATABASE: ['POSTGRES', 'MYSQL', 'CLICKHOUSE', 'MSSQL', 'ORACLE'],
  IN_MEMORY: ['REDIS'],
  DOCS: ['CONFLUENCE'],
  MESSAGING: ['SLACK', 'MSTEAMS', 'GOOGLE_CHAT'],
  OBSERVABITY_PLATFORM: [
    'DATADOG',
    'DYNATRACE',
    'LAST9',
    'LOGGLY',
    'LOKI',
    'SIGNOZ',
    'OBSERVE',
    'AZURE_APP_INSIGHTS',
    'PROMETHEUS',
    'CHRONOSPHERE',
    'OTEL',
    'ES',
    'PINOT',
    'HIVE',
    'SOLARWINDS',
  ],
  LLM: ['LLM'],
  SERVER: ['SSH', 'VM_AGENT'],
};

const SECTIONS_CONFIG = [
  {
    id: 'cloud',
    label: 'Kubernetes & Cloud',
    icon: CloudAccountIcon,
    providers: ['K8S', 'AWS', 'AZURE', 'GCP', 'CLOUDFOUNDRY'],
    tab: 1,
  },
  {
    id: 'messaging',
    label: 'Messaging & Alerting',
    icon: MessageBlueIcon,
    providers: ['SLACK', 'MSTEAMS', 'GOOGLE_CHAT'],
    tab: 2,
  },
  {
    id: 'ticket',
    label: 'Ticketing',
    icon: TicketBlueIcon,
    providers: ['JIRA', 'SERVICENOW', 'PAGERDUTY', 'ZENDUTY'],
    tab: 3,
  },
  {
    id: 'webhooks',
    label: 'Webhooks',
    icon: TicketBlueIcon,
    providers: [
      'PAGERDUTY_WEBHOOK',
      'ZENDUTY_WEBHOOK',
      'PROMETHEUS_ALERTMANAGER_WEBHOOK',
      'DATADOG_WEBHOOK',
      'AZURE_MONITOR_WEBHOOK',
      'SERVICENOW_WEBHOOK',
      'NEWRELIC_WEBHOOK',
      'GRAFANA_WEBHOOK',
      'SPLUNK_WEBHOOK',
      'GCP_MONITORING_WEBHOOK',
      'DYNATRACE_WEBHOOK',
      'SOLARWINDS_WEBHOOK',
      'WORKFLOW_WEBHOOK',
    ],
    tab: 4,
  },
  {
    id: 'database',
    label: 'Databases',
    icon: DataBaseBlueIcon,
    providers: ['POSTGRES', 'MYSQL', 'CLICKHOUSE', 'MSSQL', 'ORACLE'],
    tab: 5,
  },
  {
    id: 'observability',
    label: 'Observability',
    icon: DatadogIcon,
    providers: [
      'DATADOG',
      'DYNATRACE',
      'LAST9',
      'LOGGLY',
      'LOKI',
      'SIGNOZ',
      'OBSERVE',
      'AZURE_APP_INSIGHTS',
      'PROMETHEUS',
      'CHRONOSPHERE',
      'OTEL',
      'JAEGER',
      'NEWRELIC',
      'SPLUNK_OBSERVABILITY_PLATFORM',
      'SOLARWINDS',
      'GRAFANA-TEMPO',
      'ES',
      'PINOT',
      'HIVE',
    ],
    tab: 6,
  },
  {
    id: 'repo',
    label: 'Repos',
    icon: RepoBlueIcon,
    providers: ['GITHUB', 'BITBUCKET', 'GITLAB'],
    tab: 7,
  },
  {
    id: 'queue',
    label: 'Messaging Queue',
    icon: QueueBlueIcon,
    providers: ['RABBITMQ'],
    tab: 8,
  },
  {
    id: 'ci_cd',
    label: 'CI/CD',
    icon: RepoBlueIcon,
    providers: ['ARGOCD', 'GITHUB'],
    tab: 9,
  },
  {
    id: 'in-memory',
    label: 'In-Memory',
    icon: InMemoryIcon,
    providers: ['REDIS'],
    tab: 10,
  },
  {
    id: 'docs',
    label: 'Docs',
    icon: RepoBlueIcon,
    providers: ['CONFLUENCE'],
    tab: 11,
  },
  {
    id: 'llm',
    label: 'LLM',
    icon: TroubleshootIconBlue,
    providers: ['LLM', 'MCP'],
    tab: 12,
  },
  {
    id: 'server',
    label: 'Servers',
    icon: TerminalIcon,
    providers: ['SSH', 'VM_AGENT'],
    tab: 13,
  },
];

// Optimized CSS Styles
const ACCOUNT_CARD_STYLES = {
  container: {
    // Layout & Sizing
    width: '100%', // FIXED: Was 420px, causing layout breaks
    minHeight: '100px', // Use minHeight to allow text wrapping if needed
    display: 'flex',
    justifyContent: 'flex-start', // Aligns content to the left
    alignItems: 'center', // Vertically centers content
    padding: ds.space[4], // consistent padding

    // Appearance
    background: 'var(--ds-background-100)',
    boxShadow: `0px 4px 10px 0px ${ds.gray.alpha[100]}`,
    borderRadius: ds.radius.md,
    border: `1px solid ${ds.gray[200]}`,
    borderLeft: `6px solid ${ds.blue[500]}`,

    // Interaction
    cursor: 'pointer',
    transition: 'all ease-in-out 0.15s',
    textTransform: 'none', // Prevents uppercase text
    textAlign: 'left',
    position: 'relative',

    '&:hover': {
      border: `1px solid ${ds.blue[500]}`,
      boxShadow: `0px 4px 10px 0px ${ds.gray.alpha[100]}`,
      background: 'var(--ds-background-100)', // typo fix from original 'intigrationCard' if applicable
      borderLeft: `6px solid ${ds.blue[500]}`,
    },
  },
};

// Optimized Component
const AccountCard = React.memo(({ cloud_provider = 'AWS', active = 0, disabled = 0, label, activeClouds = [] }) => {
  const router = useRouter();
  const hasAnyConnections = active > 0 || disabled > 0;

  const handleClick = useCallback(() => {
    router.push(`/accounts/account-form?cloudProvider=${cloud_provider}`);
  }, [router, cloud_provider]);

  const isMessagingProvider = ['SLACK', 'MSTEAMS', 'GOOGLE_CHAT'].includes(cloud_provider?.toUpperCase());
  const needsChannelMapping =
    isMessagingProvider &&
    active > 0 &&
    (!activeClouds || activeClouds.length === 0 || activeClouds.some((account) => account.channels.length === 0));
  const isDisabled = DISABLED_PROVIDERS.has(cloud_provider);

  const id =
    cloud_provider
      ?.split('_')
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
      .join('-') || '';

  return (
    <Button
      id={`${id}-section-card`}
      sx={{
        ...ACCOUNT_CARD_STYLES.container,
        ...(isDisabled && {
          opacity: 0.6,
          filter: 'grayscale(100%)',
          cursor: 'not-allowed',
          pointerEvents: 'none',
        }),
      }}
      // Remove onClick if disabled to be safe, though pointerEvents: none handles it mostly
      onClick={!isDisabled ? handleClick : undefined}
    >
      {/* Needs Mapping Badge */}
      {needsChannelMapping && (
        <Box
          sx={{
            position: 'absolute',
            top: ds.space[2],
            right: ds.space[2],
            bgcolor: ds.amber[100],
            border: `1px solid ${ds.amber[200]}`,
            borderRadius: ds.radius.sm,
            px: 1,
            py: 0.5,
          }}
        >
          <Typography fontSize='10px' color={ds.amber[700]} fontWeight={ds.weight.semibold} lineHeight={1}>
            Channel Missing
          </Typography>
        </Box>
      )}

      {/* Internal Layout: Flexbox instead of Grid for better vertical alignment */}
      <Box display='flex' alignItems='center' width='100%' gap={2}>
        {/* Icon Section - Fixed Width to prevent shifting */}
        <Box
          sx={{
            width: '50px',
            height: '50px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          <CloudProviderIcon cloud_provider={cloud_provider} width='50px' height='50px' />
        </Box>

        {/* Text Section */}
        <Box display='flex' flexDirection='column' flexGrow={1} minWidth={0}>
          <Typography color={ds.gray[700]} fontSize={ds.text.title} fontWeight={ds.weight.semibold} noWrap>
            {label === 'Kubernetes' ? 'Kubernetes Clusters' : label}
          </Typography>

          <Typography color={ds.gray[600]} fontSize={ds.text.small} fontWeight={ds.weight.regular} sx={{ mb: 0.5 }} noWrap>
            {label === 'Kubernetes' ? 'EKS, AKS, GKE, OpenShift...' : ''}
          </Typography>

          <Stack direction='row' spacing={2} alignItems='center' height='20px'>
            {active > 0 && (
              <Stack direction='row' spacing={0.5} alignItems='center'>
                <Box sx={{ width: 6, height: 6, borderRadius: '50%', bgcolor: ds.green[500] }} />
                <Typography fontSize={ds.text.small} color={ds.gray[600]}>
                  Active
                </Typography>
                <Typography fontSize={ds.text.small} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
                  {active}
                </Typography>
              </Stack>
            )}

            {disabled > 0 && (
              <Stack direction='row' spacing={0.5} alignItems='center'>
                <Box sx={{ width: 6, height: 6, borderRadius: '50%', bgcolor: ds.gray[300] }} />
                <Typography fontSize={ds.text.small} color={ds.gray[600]}>
                  Inactive
                </Typography>
                <Typography fontSize={ds.text.small} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
                  {disabled}
                </Typography>
              </Stack>
            )}

            {!hasAnyConnections && !isDisabled && (
              <Typography fontSize={ds.text.small} color={ds.gray[600]} fontStyle='italic'>
                No connections
              </Typography>
            )}
          </Stack>
        </Box>
      </Box>
    </Button>
  );
});

AccountCard.displayName = 'AccountCard';

const accountHelpers = {
  getCloudProviderCount: (cloudAccounts, accData, awsAccData, azureAccData, gcpAccData) => {
    return PROVIDERS.CLOUD.map((cp) => {
      const cloudProvider = cp.toLowerCase();
      let activeClouds;

      if (cloudProvider === 'k8s') {
        activeClouds = accData;
      } else if (cloudProvider === 'aws') {
        activeClouds = awsAccData;
      } else if (cloudProvider === 'gcp') {
        activeClouds = gcpAccData;
      } else if (cloudProvider === 'azure') {
        activeClouds = azureAccData;
      } else {
        activeClouds = [];
      }

      const CPCountVal = {
        cloud_provider: cp,
        active: 0,
        disabled: 0,
        label: getCloudProviderLabel(cp),
        activeClouds: activeClouds,
      };

      cloudAccounts.forEach((ca) => {
        if (ca?.cloud_provider?.toUpperCase() === cp.toUpperCase()) {
          ca.status === 'active' ? CPCountVal.active++ : CPCountVal.disabled++;
        }
      });

      return CPCountVal;
    });
  },

  mapAccountsToActiveStatus: (accounts) =>
    accounts?.map((item) => ({
      accName: item.name,
      status: item.is_active ? 'active' : 'disabled',
    })) || [],

  mapWebhooksToActiveStatus: (accounts) =>
    accounts?.map((item) => ({
      accName: item.name,
      status: item.status === 'enabled' ? 'active' : 'disabled',
    })) || [],

  getTicketManagementCount: (accData, serviceNowAccData, pagerdutyAccounts) => {
    return PROVIDERS.MGMNT_TOOL.map((tool) => {
      let activeClouds = [];

      if (tool?.toLowerCase() === 'jira') {
        activeClouds = accountHelpers.mapAccountsToActiveStatus(accData);
      } else if (tool?.toLowerCase() === 'servicenow') {
        activeClouds = accountHelpers.mapAccountsToActiveStatus(serviceNowAccData);
      } else if (tool?.toLowerCase() === 'pagerduty') {
        activeClouds = accountHelpers.mapAccountsToActiveStatus(pagerdutyAccounts);
      }

      return {
        cloud_provider: tool,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(tool),
        activeClouds: activeClouds,
      };
    });
  },

  getWebhookManagementCount: (
    pagerdutyWebhookAccounts,
    prometheusAlertmanagerWebhookAccounts,
    datadogWebhookAccounts,
    azureMonitorWebhookAccounts,
    serviceNowWebhookAccounts,
    grafanaWebhookAccounts
  ) => {
    return PROVIDERS.WEBHOOKS.map((tool) => {
      let activeClouds = [];

      if (tool?.toLowerCase() === 'pagerduty_webhook') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(pagerdutyWebhookAccounts);
      } else if (tool?.toLowerCase() === 'prometheus_alertmanager_webhook') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(prometheusAlertmanagerWebhookAccounts);
      } else if (tool?.toLowerCase() === 'grafana_webhook') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(grafanaWebhookAccounts);
      } else if (tool?.toLowerCase() === 'datadog_webhook') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(datadogWebhookAccounts);
      } else if (tool?.toLowerCase() === 'azure_monitor_webhook') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(azureMonitorWebhookAccounts);
      } else if (tool?.toLowerCase() === 'servicenow_webhook') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(serviceNowWebhookAccounts);
      }

      return {
        cloud_provider: tool,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(tool),
        activeClouds: activeClouds,
      };
    });
  },

  getRepoManagementCount: (gitHubAccData) => {
    return PROVIDERS.REPOS.map((repo) => {
      const activeClouds = accountHelpers.mapAccountsToActiveStatus(gitHubAccData);

      return {
        cloud_provider: repo,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(repo),
        activeClouds: activeClouds,
      };
    });
  },

  getCiCdManagementCount: (argoCdData) => {
    return PROVIDERS.CI_CD.map((provider) => {
      let activeClouds = [];
      if (provider?.toLowerCase() === 'argocd') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(argoCdData);
      }
      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getInMemoryCount: (redisAccounts) => {
    return PROVIDERS.IN_MEMORY.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'redis') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(redisAccounts);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getDocsCounts: (docAccounts) => {
    return PROVIDERS.DOCS.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'confluence') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(docAccounts);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getObservabilityCounts: (observabilityAccounts) => {
    return PROVIDERS.OBSERVABITY_PLATFORM.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'datadog') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.datadog);
      } else if (provider?.toLowerCase() === 'loggly') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.loggly);
      } else if (provider?.toLowerCase() === 'loki') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.loki);
      } else if (provider?.toLowerCase() === 'signoz') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.signoz);
      } else if (provider?.toLowerCase() === 'azure_app_insights') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.azure);
      } else if (provider?.toLowerCase() === 'prometheus') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.prometheus);
      } else if (provider?.toLowerCase() === 'otel') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.otel);
      } else if (provider?.toLowerCase() === 'chronosphere') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.chronosphere);
      } else if (provider?.toLowerCase() === 'observe') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.observe);
      } else if (provider?.toLowerCase() === 'es') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.es);
      } else if (provider?.toLowerCase() === 'pinot') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.pinot);
      } else if (provider?.toLowerCase() === 'hive') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(observabilityAccounts.hive);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getLLMCounts: (llmAccounts) => {
    return PROVIDERS.LLM.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'llm') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(llmAccounts);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getServerCounts: (serverAccounts) => {
    return PROVIDERS.SERVER.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'ssh') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(serverAccounts);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getDatabaseCount: (postgresAccounts, mysqlAccounts, clickhouseAccounts) => {
    return PROVIDERS.DATABASE.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'postgres') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(postgresAccounts);
      } else if (provider?.toLowerCase() === 'mysql') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(mysqlAccounts);
      } else if (provider?.toLowerCase() === 'clickhouse') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(clickhouseAccounts);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getQueueCount: (rabbitmqAccounts) => {
    return PROVIDERS.QUEUE.map((provider) => {
      let activeClouds = [];

      if (provider?.toLowerCase() === 'rabbitmq') {
        activeClouds = accountHelpers.mapWebhooksToActiveStatus(rabbitmqAccounts);
      }

      return {
        cloud_provider: provider,
        active: activeClouds.filter((item) => item.status === 'active').length,
        disabled: activeClouds.filter((item) => item.status === 'disabled').length,
        label: getCloudProviderLabel(provider),
        activeClouds: activeClouds,
      };
    });
  },

  getMessagingPlatformCount: (data, slackAccData, msTeamAccData, gChatAccData) => {
    return PROVIDERS.MESSAGING.map((platform) => {
      let activeClouds = [];
      let platformKey = '';

      if (platform.toLowerCase() === 'slack') {
        activeClouds = slackAccData;
        platformKey = 'slack';
      } else if (platform.toLowerCase() === 'msteams') {
        activeClouds = msTeamAccData;
        platformKey = 'ms_teams';
      } else if (platform.toLowerCase() === 'google_chat') {
        activeClouds = gChatAccData;
        platformKey = 'google_chat';
      } else {
        activeClouds = [];
      }

      return {
        cloud_provider: platform,
        active: data.filter((d) => d.platform === platformKey)?.length || 0,
        disabled: 0,
        label: getCloudProviderLabel(platform),
        activeClouds: activeClouds,
      };
    });
  },
};

const Integrations = () => {
  const [sectionsData, setSectionsData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [selectedTab, setSelectedTab] = useState(0);
  const [searchQuery, setSearchQuery] = useState('');

  const tabOptions = useMemo(
    () => ({
      tabOptions: [
        { text: 'All', value: 0, id: 'all' },
        ...SECTIONS_CONFIG.map((s) => ({ text: s.label.replace(' & Cloud Account', ''), value: s.tab, id: s.id })),
      ],
    }),
    []
  );

  // --- DATA PROCESSING HELPERS ---

  const normalizeApiResponse = (resData) => {
    const map = {}; // Structure: { [ProviderKey]: Array<Account> }

    const addToMap = (key, item) => {
      const k = key?.toUpperCase();
      if (!k) {
        return;
      }
      if (!map[k]) {
        map[k] = [];
      }
      map[k].push(item);
    };

    // 1. Process Cloud Accounts
    resData?.all_accounts?.forEach((acc) => {
      addToMap(acc.cloud_provider, { ...acc, status: acc.status || (acc.is_active ? 'active' : 'disabled') });
    });

    // 2. Process Messaging Platforms (Custom parsing logic preservation)
    resData?.messaging_platforms?.forEach((acc) => {
      let channels = [];
      if (acc.platform === 'slack' && acc.channels) {
        // ... (Keep existing complex slack parsing logic or simplify if API is consistent)
        channels = [acc.team_name]; // simplified based on code analysis
      } else if (acc.platform === 'ms_teams' && acc.channels?.team_name) {
        channels = [acc.channels.team_name];
      } else if (acc.platform === 'google_chat') {
        try {
          const parsed = typeof acc.channels === 'string' ? JSON.parse(acc.channels) : acc.channels;
          if (parsed?.name) {
            channels = [parsed.name];
          }
        } catch {
          // Ignore parsing errors
        }
      }

      const key = acc.platform === 'ms_teams' ? 'MSTEAMS' : acc.platform;
      // Messaging platforms in this API usually don't have 'disabled' status in same way, assuming active if exists
      addToMap(key, { ...acc, status: 'active', channels });
    });

    // 3. Process Generic Integrations
    resData?.integrations?.forEach((acc) => {
      // Map 'status: enabled' to 'active' for consistency
      const status = acc.status === 'enabled' ? 'active' : 'disabled';

      // Special case mapping for OTel/Clickhouse naming if needed
      let key = acc.type;
      if (acc.type === 'postgresql') {
        key = 'POSTGRES';
      }
      if (acc.type === 'otel_clickhouse') {
        key = 'OTEL';
      }

      addToMap(key, { ...acc, status });
    });

    return map;
  };

  const generateSectionData = (dataMap) => {
    return SECTIONS_CONFIG.map((section) => {
      const sectionAccounts = section.providers.map((providerKey) => {
        const accounts = dataMap[providerKey.toUpperCase()] || [];

        // Calculate counts
        const activeCount = accounts.filter((a) => a.status === 'active' || a.is_active).length;
        const disabledCount = accounts.filter((a) => a.status === 'disabled' || a.status === 'inactive').length;

        return {
          cloud_provider: providerKey,
          label: getCloudProviderLabel(providerKey),
          active: activeCount,
          disabled: disabledCount,
          activeClouds: accounts, // Passed down for Messaging channel checks
        };
      });

      return {
        ...section,
        accounts: sectionAccounts,
      };
    });
  };

  useEffect(() => {
    const fetchAllData = async () => {
      setLoading(true);
      try {
        const res = await apiAccount.getAllAccount();
        const dataMap = normalizeApiResponse(res?.data);
        const processedSections = generateSectionData(dataMap);
        setSectionsData(processedSections);
      } catch (error) {
        console.error('Error fetching integration data:', error);
      } finally {
        setLoading(false);
      }
    };
    fetchAllData();
  }, []);

  // --- FILTERING & SORTING LOGIC ---

  const { connectedSections, availableSections } = useMemo(() => {
    const connected = [];
    const available = [];

    // Filter by Tab
    const sectionsToProcess = selectedTab === 0 ? sectionsData : sectionsData.filter((s) => s.tab === selectedTab);

    // Deduplicate providers across sections when showing "All"
    const seenConnected = selectedTab === 0 ? new Set() : null;
    const seenAvailable = selectedTab === 0 ? new Set() : null;

    sectionsToProcess.forEach((section) => {
      // Filter by Search
      const matchesSearch = (acc) =>
        !searchQuery ||
        acc.label.toLowerCase().includes(searchQuery.toLowerCase()) ||
        acc.cloud_provider.toLowerCase().includes(searchQuery.toLowerCase());

      let filteredAccounts = section.accounts.filter(matchesSearch);

      if (filteredAccounts.length === 0) {
        return;
      }

      let connectedAccs = filteredAccounts.filter((acc) => acc.active > 0);
      let availableAccs = filteredAccounts.filter((acc) => acc.active === 0);

      // Deduplicate when "All" tab is active
      if (seenConnected) {
        connectedAccs = connectedAccs.filter((acc) => {
          if (seenConnected.has(acc.cloud_provider)) return false;
          seenConnected.add(acc.cloud_provider);
          return true;
        });
      }
      if (seenAvailable) {
        availableAccs = availableAccs.filter((acc) => {
          if (seenAvailable.has(acc.cloud_provider)) return false;
          seenAvailable.add(acc.cloud_provider);
          return true;
        });
      }

      // **OPTIMIZED SORTING:** // Available items: Non-disabled providers go to the top.
      availableAccs.sort((a, b) => {
        const aDisabled = DISABLED_PROVIDERS.has(a.cloud_provider);
        const bDisabled = DISABLED_PROVIDERS.has(b.cloud_provider);
        if (!aDisabled && bDisabled) {
          return -1;
        }
        if (aDisabled && !bDisabled) {
          return 1;
        }
        return 0;
      });

      if (connectedAccs.length > 0) {
        connected.push({ ...section, accounts: connectedAccs });
      }
      if (availableAccs.length > 0) {
        available.push({ ...section, accounts: availableAccs });
      }
    });

    return { connectedSections: connected, availableSections: available };
  }, [sectionsData, selectedTab, searchQuery]);

  const SectionTitle = ({ label, count, color, icon }) => (
    <Box display='flex' alignItems='center' gap='10px' mb={2} pb={1} borderBottom={`2px solid ${ds.gray[200]}`} width='100%'>
      <Box sx={{ width: '4px', height: '20px', borderRadius: ds.radius.sm, bgcolor: color }} />
      {icon && <SafeIcon src={icon} alt='' width={20} height={20} style={{ filter: 'grayscale(100%)' }} />}
      <Typography fontSize='18px' fontWeight={ds.weight.semibold} color={ds.gray[700]}>
        {label}
      </Typography>
      <Typography fontSize={ds.text.bodyLg} fontWeight={ds.weight.medium} color={ds.gray[600]}>
        ({count})
      </Typography>
    </Box>
  );

  const renderGrid = (sections, titleProps) => {
    if (sections.length === 0) {
      return null;
    }
    const totalCount = sections.reduce((acc, s) => acc + s.accounts.length, 0);

    return (
      <Box width='100%' mt={2}>
        <SectionTitle {...titleProps} count={totalCount} />
        <Grid container columnSpacing={1.5} rowSpacing={2}>
          {sections.flatMap((section) =>
            section.accounts.map((acc, idx) => (
              <Grid item xs={12} sm={6} md={6} lg={4} key={`${section.id}-${acc.cloud_provider}-${idx}`}>
                <AccountCard {...acc} />
              </Grid>
            ))
          )}
        </Grid>
      </Box>
    );
  };

  return (
    <ListingLayout>
      <ListingLayout.Toolbar>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '8px', width: '100%' }}>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
            <CustomSearch
              value={searchQuery}
              onChange={(next) => {
                setSearchQuery(next);
              }}
              onClear={() => {
                setSearchQuery('');
              }}
              label='Search integrations...'
              id='integrations-search'
            />
          </Box>
          {!loading && (
            <Box flexGrow={1} minWidth={0} overflow='auto'>
              <CustomTabs value={selectedTab} onChange={setSelectedTab} options={tabOptions} variant='secondary' behavior='filter' p='0px' />
            </Box>
          )}
        </Box>
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        {loading ? (
          <Loader style={{ width: '100%' }} />
        ) : (
          <Box display='flex' flexDirection='column' gap='var(--ds-space-4)' width='100%' marginBottom='var(--ds-space-2)'>
            {renderGrid(connectedSections, { label: 'Connected', color: ds.blue[500] })}
            {renderGrid(availableSections, { label: 'Available', color: ds.amber[500] })}
          </Box>
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default Integrations;
