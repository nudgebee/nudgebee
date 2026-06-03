import { Box, Stack, Typography } from '@mui/material';
import React, { useEffect, useState, useMemo, useCallback } from 'react';
import CloudProviderIcon from '@common/CloudIcon';
import apiAccount from '@api1/account';
import { useRouter } from 'next/router';
import { getCloudProviderLabel } from 'src/utils/common';
import { Skeleton } from '@components1/ds/Skeleton';
import { ds } from 'src/utils/colors';
import CustomTabs from '@common-new/CustomTabs';
import CustomSearch from '@common-new/CustomSearch';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Card } from '@components1/ds/Card';
import Chip from '@components1/ds/Chip';
import { Label } from '@components1/ds/Label';
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
    <Card
      id={`${id}-section-card`}
      variant='accent'
      tone='info'
      size='sm'
      interactive={!isDisabled}
      onClick={!isDisabled ? handleClick : undefined}
      aria-label={label}
      sx={{
        position: 'relative',
        boxSizing: 'border-box',
        width: '100%',
        minWidth: 0,
        height: ds.space.mul(0, 44),
        display: 'flex',
        alignItems: 'center',
        textAlign: 'left',
        ...(isDisabled && {
          opacity: 0.6,
          filter: 'grayscale(100%)',
          cursor: 'not-allowed',
          pointerEvents: 'none',
        }),
      }}
    >
      {/* Needs Mapping Badge */}
      {needsChannelMapping && (
        <Box sx={{ position: 'absolute', top: ds.space[2], right: ds.space[2] }}>
          <Label tone='warning' size='sm'>
            Channel Missing
          </Label>
        </Box>
      )}

      {/* Internal Layout: Flexbox instead of Grid for better vertical alignment */}
      <Box display='flex' alignItems='center' width='100%' minWidth={0} gap={ds.space[3]}>
        {/* Icon Section - Fixed Width to prevent shifting */}
        <Box
          sx={{
            width: ds.space.mul(0, 18),
            height: ds.space.mul(0, 18),
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          <CloudProviderIcon cloud_provider={cloud_provider} width='32px' height='32px' />
        </Box>

        {/* Text Section */}
        <Box display='flex' flexDirection='column' flexGrow={1} minWidth={0}>
          <Typography
            sx={{ fontFamily: 'var(--ds-font-display)' }}
            color={ds.gray[700]}
            fontSize={ds.text.body}
            fontWeight={ds.weight.semibold}
            noWrap
          >
            {label === 'Kubernetes' ? 'Kubernetes Clusters' : label}
          </Typography>

          <Typography color={ds.gray[600]} fontSize={ds.text.caption} fontWeight={ds.weight.regular} sx={{ mb: ds.space[1] }} noWrap>
            {label === 'Kubernetes' ? 'EKS, AKS, GKE, OpenShift...' : ''}
          </Typography>

          <Stack direction='row' spacing={0.5} alignItems='center' flexWrap='wrap' useFlexGap minHeight={ds.space.mul(0, 9)}>
            {active > 0 && (
              <Chip size='xs' tone='success' dot>
                Active {active}
              </Chip>
            )}

            {disabled > 0 && (
              <Chip size='xs' tone='neutral' dot dotVariant='filled'>
                Inactive {disabled}
              </Chip>
            )}

            {!hasAnyConnections && !isDisabled && (
              <Typography fontSize={ds.text.caption} color={ds.gray[600]} fontStyle='italic'>
                No connections
              </Typography>
            )}
          </Stack>
        </Box>
      </Box>
    </Card>
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

  const SectionTitle = ({ label, count, color, tone = 'neutral', icon }) => (
    <Box
      display='flex'
      alignItems='center'
      gap={ds.space[2]}
      mb={ds.space[3]}
      pb={ds.space[2]}
      borderBottom={`1px solid ${ds.gray[200]}`}
      width='100%'
    >
      <Box sx={{ width: ds.space[1], height: ds.space[4], borderRadius: ds.radius.sm, bgcolor: color }} />
      {icon && <SafeIcon src={icon} alt='' width={20} height={20} style={{ filter: 'grayscale(100%)' }} />}
      <Typography sx={{ fontFamily: 'var(--ds-font-display)' }} fontSize={ds.text.title} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
        {label}
      </Typography>
      <Chip size='sm' tone={tone}>
        {count}
      </Chip>
    </Box>
  );

  const renderGrid = (sections, titleProps) => {
    if (sections.length === 0) {
      return null;
    }
    const totalCount = sections.reduce((acc, s) => acc + s.accounts.length, 0);

    return (
      <Box width='100%' mt={ds.space[4]}>
        <SectionTitle {...titleProps} count={totalCount} />
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: {
              xs: '1fr',
              sm: 'repeat(2, minmax(0, 1fr))',
              lg: 'repeat(3, minmax(0, 1fr))',
            },
            gap: ds.space[5],
          }}
        >
          {sections.flatMap((section) =>
            section.accounts.map((acc, idx) => <AccountCard key={`${section.id}-${acc.cloud_provider}-${idx}`} {...acc} />)
          )}
        </Box>
      </Box>
    );
  };

  return (
    <>
      {!loading && (
        <Box sx={{ width: '100%', mb: ds.space[3], overflow: 'auto' }}>
          <CustomTabs value={selectedTab} onChange={setSelectedTab} options={tabOptions} variant='secondary' behavior='filter' p='0px' />
        </Box>
      )}
      <ListingLayout>
        <ListingLayout.Toolbar>
          <Box sx={{ display: 'flex', justifyContent: 'flex-start', width: '100%' }}>
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
        </ListingLayout.Toolbar>
        <ListingLayout.Body padding={`0 ${ds.space[5]} ${ds.space[5]}`}>
          {loading ? (
            <Box width='100%' mt={ds.space[4]}>
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns: {
                    xs: '1fr',
                    sm: 'repeat(2, minmax(0, 1fr))',
                    md: 'repeat(3, minmax(0, 1fr))',
                    lg: 'repeat(4, minmax(0, 1fr))',
                  },
                  gap: ds.space[5],
                }}
              >
                {Array.from({ length: 9 }).map((_, idx) => (
                  <Skeleton.Card key={`integration-skeleton-${idx}`} width='100%' lines={1} />
                ))}
              </Box>
            </Box>
          ) : (
            <Box display='flex' flexDirection='column' gap='var(--ds-space-4)' width='100%' marginBottom='var(--ds-space-2)'>
              {renderGrid(connectedSections, { label: 'Connected', color: ds.blue[500], tone: 'info' })}
              {renderGrid(availableSections, { label: 'Available', color: ds.amber[500], tone: 'warning' })}
            </Box>
          )}
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default Integrations;
