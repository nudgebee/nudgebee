import { useState, useEffect, useRef, useMemo } from 'react';
import { headerMenuExtras } from '@lib/authHooks';
import Box from '@mui/material/Box';
import { Typography } from '@mui/material';
import DOMPurify from 'dompurify';
import { colors, ds } from 'src/utils/colors';
import { Button as DsButton } from '@components1/ds/Button';
import DsTooltip from '@components1/ds/Tooltip';
import { Divider as DsDivider } from '@components1/ds/Divider';
import { Banner } from '@components1/ds/Banner';
import AdminIconBlue from '@assets/header/AdminIconBlue.icon.svg';
import AutopilotIconBlue from '@assets/header/AutopilotIconBlue.icon.svg';
import ClusterIconBlue from '@assets/header/ClusterIconBlue.icon.svg';
import AgentIconBlue from '@assets/header/agent_icon_blue.icon.svg';
import HomeIconBlue from '@assets/header/HomeIconBlue.icon.svg';
import OptimiseIconBlue from '@assets/header/OptimiseIconBlue.icon.svg';
import TicketIconBlue from '@assets/header/TicketIconBlue.icon.svg';
import TroubleshootIconBlue from '@assets/header/TroubleshootIconBlue.icon.svg';
import HelpOutlineDarkIcon from '@assets/new/help-circle-dark.svg';
import NotificationOutlineIconDark from '@assets/new/bell-icon-dark.svg';
import DocumentationIcon from '@assets/header/Documentation.svg';
import newAwsLogo from '@assets/logo/aws_logo.png';
import GroupingIcon from '@assets/header/group-icon.svg';
import OuK8sIcon from '@assets/ou-management/kubernetes_icon.icon.svg';
import JiraIcon from '@assets/jira_icon.icon.svg';
import GithubIcon from '@assets/github-icon.icon.svg';
import SlackIcon from '@assets/slack_icon.icon.svg';
import MsTeamsIcon from '@assets/ou-management/ms_teams.icon.svg';
import GChatIcon from '@assets/gchat-icon.icon.svg';
import ServiceNowIcon from '@assets/auto-pilot/service-now.svg';
import ExternalLinkIcon from '@assets/external-link-icon.svg';
import PodsIcon from '@assets/home/new/pods_icon.icon.svg';
import azureAuth from '@assets/authentication/azure.svg';
import googleAuth from '@assets/authentication/google.svg';
import WorkflowIconBlue from '@assets/workflow/workflow-icon-blue.icon.svg';

import SafeIcon from '@components1/common/SafeIcon';
import { useRouter } from 'next/router';
import { useData } from '@context/DataContext';
import { hasWriteAccess } from '@lib/auth';
import apiKubernetes from '@api1/kubernetes';
import IconButton from '@mui/material/IconButton';
import ClusterDropdown from '@common/ClusterDropDown';
import { useSession } from 'next-auth/react';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import K8sAccountModal from '@common/K8sAccountModal';
import JiraAccountModal from '@common/JiraAccountModal';
import GithubAccountModal from '@common/GithubAccountModal';
import ServiceNowAccountModal from '@common/ServiceNowAccountModal';
import { Select } from '@components1/ds/Select';
import apiAppGrouping from '@api1/application-groupings';
import CustomBackButton from '@common-new/CustomBackButton';
import Link from 'next/link';
import apiAccount from '@api1/account';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { docsUrl } from '@lib/externalUrls';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';

const Header1 = ({ showBorder = false }) => {
  const { data } = useSession({ required: true });
  const router = useRouter();
  const { assistantName, baseTitle, nubiIconUrl, nubiIconLightUrl } = useTenantBranding();
  const { selectedCluster, allCluster } = useData();
  const selectedClusterRef = useRef(selectedCluster);
  selectedClusterRef.current = selectedCluster;
  const isAlertOpen = useRef(false);

  const [anchorActiveTab, setAnchorActiveTab] = useState('');
  const [snackbarOpen, setSnackbarOpen] = useState(false);
  const [snackbarMsg, setSnackbarMsg] = useState('');
  const [showReloadNotification, setShowReloadNotification] = useState(false);
  const [reloadMsg, setReloadMsg] = useState('');
  const [showK8sAccountModal, setShowK8sAccountModal] = useState(false);
  const [showJiraAccountModal, setShowJiraAccountModal] = useState(false);
  const [showGitHubAccountModal, setShowGitHubAccountModal] = useState(false);
  const [showServiceNowAccountModal, setShowServiceNowAccountModal] = useState(false);
  const [activeGroup, setActiveGroup] = useState({ label: ' ', value: ' ' });
  const [allAppGroupNames, setAllAppGroupNames] = useState([{}]);
  const [groupId, setGroupId] = useState(router.query.groupId ?? '');
  const [loading, setLoading] = useState(false);
  const [slackAccountsCount, setSlackAccountsCount] = useState(0);
  const [gChatAccountsCount, setGChatAccountsCount] = useState(0);

  const isGroupingRoute = useMemo(() => {
    return router.pathname.includes('grouping');
  }, [router.pathname]);

  useEffect(() => {
    const fetchIntegrationCounts = async () => {
      try {
        const [slackRes, gChatRes] = await Promise.all([apiAccount.getMessagingPlatform('slack'), apiAccount.getMessagingPlatform('google_chat')]);
        setSlackAccountsCount(slackRes?.data?.length || 0);
        const configuredGChatCount = gChatRes?.data?.filter((item) => item?.channels).length || 0;
        setGChatAccountsCount(configuredGChatCount);
      } catch (error) {
        console.error('Failed to fetch Slack/GChat integration counts:', error);
        setSlackAccountsCount(0);
        setGChatAccountsCount(0);
      }
    };

    if (anchorActiveTab.connectAccountButton && hasWriteAccess()) {
      fetchIntegrationCounts();
    }
  }, [anchorActiveTab]);

  useEffect(() => {
    if (isGroupingRoute) {
      setLoading(true);
      apiAppGrouping
        .listAllApplicationGroupNames()
        .then((res) => {
          const allNames =
            res?.map((item) => ({
              label: item.name,
              value: item.id,
              id: item.id,
            })) || [];
          setAllAppGroupNames(allNames);
        })
        .finally(() => {
          setLoading(false);
        });
    }
  }, [isGroupingRoute, groupId]);

  useEffect(() => {
    if (groupId && allAppGroupNames.length) {
      const group = allAppGroupNames.find((item) => item.id == groupId);
      if (group) {
        setActiveGroup(group);
      }
    }
  }, [allAppGroupNames]);

  useEffect(() => {
    if (router.query.groupId != groupId) {
      setGroupId(router.query.groupId);
    }
  }, [router.query]);

  const handleChangeGroup = (groupValue) => {
    router.push(groupValue ? `/grouping?groupId=${groupValue}` : '/grouping');
  };

  // Help-menu items for the DS DropdownMenu. Computed inline (NOT memoised) so
  // late-loading EE bundles whose `registerHeaderMenuExtra(...)` side-effect
  // runs AFTER Header1's first render are still picked up on the next
  // re-render. The `close` callback is a no-op — DropdownMenu auto-closes on
  // item select; extras may invoke it before deferred side-effects anyway.
  const buildHelpMenuItems = () => {
    const close = () => {};
    const items = [
      {
        label: 'Documentation',
        icon: <SafeIcon src={DocumentationIcon} alt='Documentation' />,
        onSelect: () => window.open(docsUrl('/docs/features/'), '_blank', 'noopener'),
      },
    ];
    headerMenuExtras().forEach(({ id, getItem }) => {
      const item = getItem?.(data, close);
      if (item) items.push({ ...item, id });
    });
    return items;
  };

  const headerItems = useMemo(() => {
    const baseOptions = [
      {
        name: 'Home',
        route: '/home',
        icon: HomeIconBlue,
        showActiveCluster: true,
        clusterDetailButton: true,
        showAskNudgebee: true,
      },
      {
        name: 'Cluster Overview',
        route: '/kubernetes',
        icon: ClusterIconBlue,
        showActiveCluster: false,
        showAskNudgebee: false,
        connectClusterButton: true,
      },
      {
        name: 'Cluster Details',
        route: '/kubernetes/details/',
        icon: ClusterIconBlue,
        showActiveCluster: true,
        showAskNudgebee: true,
      },
      {
        name: 'Agent Details',
        route: '/agentHealth',
        icon: AgentIconBlue,
        showActiveCluster: true,
        showBackButton: true,
      },
      {
        name: 'Accounts',
        route: '/accounts/account-form',
        icon: AdminIconBlue,
        showActiveCluster: false,
        showBackButton: true,
      },
      {
        name: 'Troubleshoot',
        route: '/troubleshoot',
        icon: TroubleshootIconBlue,
        showActiveCluster: false,
        clusterDetailButton: false,
        showAskNudgebee: false,
      },
      {
        name: 'Troubleshoot',
        route: '/investigate',
        icon: TroubleshootIconBlue,
        showActiveCluster: true,
        disableDropdown: true,
        connectAccountButton: false,
        showAskNudgebee: true,
        showBackButton: true,
      },
      {
        name: 'Optimize',
        route: '/optimise',
        icon: OptimiseIconBlue,
        showActiveCluster: false,
        clusterDetailButton: false,
        showAskNudgebee: false,
      },
      {
        name: 'Optimize',
        route: '/optimise-old',
        icon: OptimiseIconBlue,
        showActiveCluster: false,
        clusterDetailButton: false,
        showAskNudgebee: false,
      },
      {
        name: 'Automation',
        route: '/auto-pilot',
        icon: WorkflowIconBlue,
        clusterDetailButton: true,
        showActiveCluster: true,
        showAskNudgebee: true,
      },
      {
        name: 'Auto Pilot',
        route: '/auto-pilot/task/[TaskDetails]',
        icon: AutopilotIconBlue,
        clusterDetailButton: false,
        showActiveCluster: false,
        showAskNudgebee: true,
        showBackButton: true,
      },
      {
        name: 'Auto Pilot',
        route: '/auto-pilot/auto-playbook/task/[ExecutionDetails]',
        icon: AutopilotIconBlue,
        clusterDetailButton: false,
        showActiveCluster: false,
        showAskNudgebee: true,
      },
      {
        name: 'Tickets',
        route: '/tickets',
        icon: TicketIconBlue,
        showActiveCluster: false,
        showAskNudgebee: false,
        connectAccountButton: false,
      },
      {
        name: 'Admin',
        route: '/user-management',
        icon: AdminIconBlue,
        showActiveCluster: false,
        showAskNudgebee: false,
        connectAccountButton: true,
      },
      {
        name: 'Accounts',
        route: '/user-management#integrations',
        icon: ClusterIconBlue,
        showActiveCluster: false,
        connectAccountButton: true,
      },
      {
        name: 'Agent Health',
        route: '/agentHealth',
        showActiveCluster: true,
      },
      {
        name: 'Monitoring',
        route: '/monitoring',
      },
      {
        name: 'Investigation',
        route: '/investigate',
        icon: TroubleshootIconBlue,
        showActiveCluster: true,
        disableDropdown: true,
        connectAccountButton: false,
        showAskNudgebee: true,
        showBackButton: true,
      },
      {
        name: 'Account Detail',
        route: '/cloud-account',
        icon:
          selectedCluster?.cloud_provider == 'Azure'
            ? azureAuth
            : selectedCluster?.cloud_provider == 'AWS'
            ? newAwsLogo
            : selectedCluster?.cloud_provider == 'GCP'
            ? googleAuth
            : '',
        showActiveCluster: true,
        connectAccountButton: false,
        showAskNudgebee: true,
      },
      {
        name: 'Application Group',
        route: '/grouping',
        icon: GroupingIcon,
        connectAccountButton: false,
        showGroupingDropdown: true,
        showBackButton: true,
      },
      {
        name: (
          <>
            {assistantName}, <span style={{ color: colors.text.darkGray, fontWeight: 'var(--ds-font-weight-regular)' }}>your AI assistant.</span>
          </>
        ),
        route: '/ask-nudgebee',
        icon: nubiIconUrl,
        connectAccountButton: false,
        showGroupingDropdown: false,
        showActiveCluster: true,
      },
      {
        name: (
          <>
            {assistantName}, <span style={{ color: colors.text.darkGray, fontWeight: 'var(--ds-font-weight-regular)' }}>your AI assistant.</span>
          </>
        ),
        route: '/ask-nudgebee-v2',
        icon: nubiIconUrl,
        connectAccountButton: false,
        showGroupingDropdown: false,
        showActiveCluster: true,
      },
      {
        name: 'Pod Details',
        route: '/kubernetes/podDetails/[PodDetails]',
        icon: PodsIcon,
        showAskNudgebee: true,
        showBackButton: true,
      },
      {
        name: 'User Feedbacks',
        route: '/internal/user-feedback',
        showActiveCluster: true,
      },
      {
        name: 'Automation Builder',
        route: '/workflow',
        icon: WorkflowIconBlue,
        showActiveCluster: true,
        showAskNudgebee: false,
        connectClusterButton: false,
        showBackButton: true,
      },
      {
        name: 'Optimize',
        route: '/agentic/optimize',
        icon: OptimiseIconBlue,
        showActiveCluster: false,
        showAskNudgebee: false,
      },
    ];
    return baseOptions;
  }, [selectedCluster]);

  useEffect(() => {
    setSnackbarOpen(false);
    setSnackbarMsg('');

    if (!selectedCluster || Object.keys(selectedCluster).length === 0 || selectedCluster.cloud_provider !== 'K8s') {
      return;
    }

    const hasClosed = localStorage.getItem(`latest-${selectedCluster.value}-K8sAgentSnackbar`);
    if (hasClosed && hasClosed == 'false') {
      return;
    }

    apiKubernetes.getLatestVersions().then((res) => {
      // Use ref to read the latest selectedCluster, not the stale closure value
      const cluster = selectedClusterRef.current;
      if (!cluster || cluster.cloud_provider !== 'K8s') return;

      if (cluster?.agent?.status === 'NOT_CONNECTED') {
        setSnackbarOpen(true);
        setSnackbarMsg(`The ${baseTitle} Agent is not connected.`);
        localStorage.setItem(`latest-${cluster.value}-K8sAgentSnackbar`, 'true');
      } else if (res.data?.nudgebee_list_versions && res.data?.nudgebee_list_versions?.agent_version_latest != cluster.agent?.version) {
        let snackMessage = '';
        let disconnectedService = [];
        setSnackbarOpen(true);
        snackMessage = `<span>Update the ${baseTitle} Agent Version to ${res.data?.nudgebee_list_versions.agent_version_latest}. Refer to <a href="/help/docs/installation/agent/installation/" target="_blank" rel="noopener noreferrer">this document</a> for instructions on how to update the agent.</span>`;
        if (cluster.agent?.connection_status) {
          const connectionStatus = cluster.agent?.connection_status;
          if (!connectionStatus.relayConnection) {
            disconnectedService.push('Relay');
          }
          if (!connectionStatus.prometheusConnection) {
            disconnectedService.push('Prometheus');
          }
          if (!connectionStatus.alertManagerConnection) {
            disconnectedService.push('Alert Manager');
          }
        }
        if (disconnectedService && disconnectedService.length > 0) {
          snackMessage = snackMessage + ` The ${disconnectedService.join(', ')} services are disconnected.`;
        }
        setSnackbarMsg(snackMessage);
        localStorage.setItem(`latest-${cluster.value}-K8sAgentSnackbar`, 'true');
      }
    });
  }, [selectedCluster]);

  useEffect(() => {
    const matchedTab = headerItems.find((item) => {
      if (router.pathname === item.route && item.name !== 'Cluster Details') {
        return true;
      }
      return !!(
        (item.name === 'Cluster Details' && item.route === router.asPath.slice(0, 20)) ||
        (item.name === 'Account Detail' && item.route === router.asPath.slice(0, 14))
      );
    });
    if (matchedTab) {
      setAnchorActiveTab({
        name: matchedTab.name,
        icon: matchedTab.icon,
        showActiveCluster: matchedTab.showActiveCluster,
        disableDropdown: matchedTab.disableDropdown ?? false,
        connectClusterButton: matchedTab.connectClusterButton,
        connectAccountButton: matchedTab.connectAccountButton,
        showGroupingDropdown: matchedTab.showGroupingDropdown,
        clusterDetailButton: matchedTab.clusterDetailButton,
        showAskNudgebee: matchedTab.showAskNudgebee ?? false,
        showBackButton: matchedTab.showBackButton ?? false,
      });
    } else {
      setAnchorActiveTab({ name: '', icon: '' });
    }
    if (['/tickets', '/user-management', '/kubernetes'].some((path) => router.pathname.startsWith(path))) {
      setSnackbarOpen(false);
    }
  }, [router.pathname, headerItems]);

  useEffect(() => {
    const localAppVersion = localStorage.getItem('appVersion');
    if (data.appVersion != localAppVersion) {
      setShowReloadNotification(true);
      setReloadMsg('A new Application version is deployed. Please consider refreshing the page');
    } else {
      setShowReloadNotification(false);
      setReloadMsg('');
    }
  }, [anchorActiveTab]);

  useEffect(() => {
    const state = window.history.state;

    if (state && !state.reloaded) {
      setShowReloadNotification(false);
      localStorage.setItem('appVersion', data.appVersion);
    }
  }, []);

  const handleDropdownChange = (e) => {
    const currentRouter = router;
    // Extract the hash (e.g., "auto-runbooks")
    const fragment = currentRouter?.asPath?.split('#')?.[1] || '';
    const hashString = fragment ? `#${fragment}` : '';

    // 1. Handle switching TO Kubernetes FROM Cloud Account
    if (currentRouter.pathname.indexOf('/cloud-account/details/') > -1 && e.cloud_provider == 'K8s') {
      updateClusterState(e);
      currentRouter.push(`/kubernetes/details/${e.value}`);
      return;
    }

    // 2. Handle switching TO Cloud Account FROM Kubernetes
    else if (
      e.value &&
      currentRouter.pathname.indexOf('/kubernetes/details/') > -1 &&
      ['AWS', 'Azure', 'GCP', 'CloudFoundry'].includes(e.cloud_provider)
    ) {
      updateClusterState(e);
      currentRouter.push(`/cloud-account/details/${e.value}`);
      return;
    }

    // 3. Handle Auto Pilot Route (NEW)
    else if (currentRouter.pathname.indexOf('/auto-pilot') > -1) {
      const currentAccountId = currentRouter.query.accountId;
      if (currentAccountId !== e.value) {
        updateClusterState(e);
        // Switch account but keep the same tab (hash)
        currentRouter.push(`/auto-pilot?accountId=${e.value}${hashString}`);
        return;
      }
    }

    // 4. Handle same-view switching (staying on details page)
    const accountId = currentRouter?.query?.accountId || currentRouter.query?.KubernetesDetails || currentRouter.query?.CloudAccountDetails || '';

    if (accountId && accountId != e.value) {
      if (
        ['AWS', 'Azure', 'GCP', 'CloudFoundry'].includes(e.cloud_provider) &&
        !['Home', 'Auto Pilot', 'Agent Details'].includes(anchorActiveTab.name)
      ) {
        // Cloud accounts usually default to tab=0 (Summary)
        currentRouter.push(`/cloud-account/details/${e.value}#summary`);
      } else if (e.cloud_provider == 'K8s' && !['Home', 'Auto Pilot', 'Agent Details'].includes(anchorActiveTab.name)) {
        // Kubernetes preserves the specific view via hash
        currentRouter.push(`/kubernetes/details/${e.value}${hashString}`);
      }
    }

    updateClusterState(e);
  };

  // Helper to reduce repetitive state updates
  const updateClusterState = (_e) => {
    setSnackbarOpen(false);
    setSnackbarMsg('');
  };

  const handleCloseSnackbar = () => {
    setSnackbarOpen(false);
    localStorage.setItem(`latest-${selectedCluster.value}-K8sAgentSnackbar`, 'false');
  };

  const handleClusterData = (clusterOption) => {
    if (clusterOption.length == 0 && !isAlertOpen.current) {
      isAlertOpen.current = true;
      alert('Currently No kubernetes cluster is configured, Please add a kubernetes cluster');
      router.push('/accounts/account-form?cloudProvider=K8S');
    }
  };

  return (
    <>
      <K8sAccountModal openModal={showK8sAccountModal} handleClose={() => setShowK8sAccountModal(false)} />
      <JiraAccountModal openModal={showJiraAccountModal} handleClose={() => setShowJiraAccountModal(false)} />
      <GithubAccountModal openModal={showGitHubAccountModal} handleClose={() => setShowGitHubAccountModal(false)} />
      <ServiceNowAccountModal openModal={showServiceNowAccountModal} handleClose={() => setShowServiceNowAccountModal(false)} />
      {snackbarOpen && (
        <Banner
          tone='warning'
          dismissible
          onDismiss={handleCloseSnackbar}
          message={
            <span
              dangerouslySetInnerHTML={{
                __html: DOMPurify.sanitize(snackbarMsg, { ADD_ATTR: ['target', 'rel'] }),
              }}
            />
          }
        />
      )}
      {showReloadNotification && (
        <Banner
          tone='warning'
          dismissible
          onDismiss={() => setShowReloadNotification(false)}
          message={
            <span
              dangerouslySetInnerHTML={{
                __html: DOMPurify.sanitize(reloadMsg),
              }}
            />
          }
        />
      )}
      {allCluster && allCluster.length > 0 && !allCluster.some((c) => c.value !== 'demo') && !loading && (
        <Box
          data-testid='demo-account-banner'
          sx={{
            width: '100%',
            background: ds.amber[100],
            borderBottom: `1px solid ${ds.amber[300]}`,
          }}
        >
          <Box
            sx={{
              px: ds.space[4],
              py: ds.space[3],
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[3],
              flexWrap: { xs: 'wrap', md: 'nowrap' },
            }}
          >
            <Box
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: ds.space[3],
                color: ds.amber[700],
                fontSize: ds.text.small,
                fontWeight: ds.weight.regular,
                flex: 1,
                lineHeight: 1.5,
                '& b': { fontWeight: ds.weight.semibold },
              }}
            >
              <InfoOutlinedIcon sx={{ fontSize: 18, color: ds.amber[500], flexShrink: 0 }} />
              <span>
                <b>Demo data shown.</b> Connect your AWS, Azure, GCP, or Kubernetes account to see your own infrastructure.
              </span>
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3], flexShrink: 0 }}>
              <DsButton
                size='sm'
                tone='primary'
                data-testid='demo-banner-connect-account'
                onClick={() => router.push('/user-management#integrations')}
              >
                Connect Account
              </DsButton>
              <Typography
                component='span'
                data-testid='demo-banner-help'
                sx={{
                  fontSize: ds.text.caption,
                  color: ds.amber[700],
                  cursor: 'pointer',
                  textDecoration: 'underline',
                  '&:hover': { color: ds.amber[700] },
                }}
                onClick={() => window.open(docsUrl('/docs/features/'), '_blank', 'noopener')}
              >
                Need help?
              </Typography>
            </Box>
          </Box>
        </Box>
      )}

      <Box sx={{ zIndex: 20, boxShadow: '0px 2px 24px 2px #00000010', width: '100%', position: 'sticky', top: '0px' }}>
        <Box
          sx={{
            width: '100%',
            background: ds.background[100],
            borderBottom: `2px solid ${ds.blue[100]}`,
          }}
        >
          <Box
            sx={{
              display: 'flex',
              background: ds.background[100],
              justifyContent: 'space-between',
              alignItems: 'center',
              height: '56px',
              px: ds.space[6],
            }}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              {anchorActiveTab.showBackButton && <CustomBackButton />}
              <Box sx={{ display: 'flex', alignItems: 'center' }}>
                {anchorActiveTab.icon && (
                  <Box sx={{ mx: ds.space[2], display: 'flex', alignItems: 'center' }}>
                    <SafeIcon src={anchorActiveTab.icon} width={26} height={26} alt={anchorActiveTab.name} />
                  </Box>
                )}
                <Typography
                  sx={{ fontFamily: 'Poppins, sans-serif', fontSize: 'var(--ds-text-heading)', fontWeight: ds.weight.semibold, color: ds.gray[700] }}
                >
                  {anchorActiveTab.name}
                </Typography>
              </Box>
            </Box>
            <Box display={'flex'} alignItems={'center'} justifyContent={'center'} gap={ds.space[3]}>
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: ds.space[2] }}>
                {anchorActiveTab.connectClusterButton &&
                  hasWriteAccess() &&
                  allCluster?.some((c) => c.value !== 'demo' && c.cloud_provider?.toUpperCase() === 'K8S') && (
                    <DsButton
                      onClick={() => {
                        router.push(`/accounts/account-form?cloudProvider=K8S`);
                      }}
                      size='md'
                      tone='primary'
                    >
                      Connect cluster
                    </DsButton>
                  )}
                {anchorActiveTab.connectAccountButton && hasWriteAccess() && (
                  <DropdownMenu
                    align='end'
                    trigger={
                      <DsButton tone='secondary' size='md' icon={<KeyboardArrowDownIcon />} iconPlacement='end'>
                        Connect Account
                      </DsButton>
                    }
                    items={[
                      {
                        label: 'Kubernetes',
                        icon: <OuK8sIcon width={18} height={18} />,
                        onSelect: () => setShowK8sAccountModal(true),
                      },
                      {
                        label: 'Jira',
                        icon: <JiraIcon width={18} height={18} />,
                        onSelect: () => setShowJiraAccountModal(true),
                      },
                      {
                        label: 'Github',
                        icon: <GithubIcon width={18} height={18} />,
                        onSelect: () => setShowGitHubAccountModal(true),
                      },
                      {
                        label: 'ServiceNow',
                        icon: <SafeIcon src={ServiceNowIcon} alt='servicenow' width={18} height={18} />,
                        onSelect: () => setShowServiceNowAccountModal(true),
                      },
                      {
                        label: 'Slack',
                        icon: <SlackIcon width={18} height={18} />,
                        // Match old ButtonMenu semantics: already-configured integrations
                        // are disabled to communicate "can't add another".
                        disabled: slackAccountsCount > 0,
                        onSelect: () => window.open('/api/integrations/slack/install', '_blank'),
                      },
                      {
                        label: 'Teams',
                        icon: <MsTeamsIcon width={18} height={18} />,
                        onSelect: () => window.open('/api/integrations/ms-teams/install', '_blank', 'noopener'),
                      },
                      {
                        label: 'Google Chat',
                        icon: <GChatIcon width={18} height={18} />,
                        disabled: gChatAccountsCount > 0,
                        onSelect: () => window.open('/api/integrations/google/install', '_blank'),
                      },
                    ]}
                  />
                )}
                {anchorActiveTab.showAskNudgebee && (
                  <IconButton
                    size='small'
                    aria-label={`Ask ${assistantName}`}
                    sx={{
                      // Brand-navy gradient — tokenised across the brand-* scale so the
                      // pill restains brand-tone consistency with primary DsButtons but
                      // keeps its bespoke gradient + hover-expand affordance (no DS
                      // equivalent for this pattern).
                      background: `linear-gradient(120deg, ${ds.brand[500]} 0%, ${ds.brand[500]} 28%, ${ds.brand[500]} 85%, ${ds.brand[500]} 100%)`,
                      borderRadius: ds.radius.lg,
                      height: '32px',
                      width: 'fit-content',
                      padding: `0 0 0 ${ds.space[2]}`,
                      display: 'flex',
                      alignItems: 'center',
                      gap: ds.space[2],
                      overflow: 'hidden',
                      transition: 'transform 0.3s ease, box-shadow 0.3s ease',
                      '&:hover': {
                        transform: 'scale(1)',
                        '& img, & svg': {
                          animation: 'popSmooth 1.5s infinite ease-in-out',
                          filter: 'brightness(1.5) contrast(1.3) saturate(1.2)',
                        },
                        '& .nubi-text': {
                          opacity: 1,
                          maxWidth: '200px',
                          marginRight: ds.space[2],
                        },
                      },
                      '@keyframes popSmooth': {
                        '0%, 100%': { transform: 'scale(1)' },
                        '50%': { transform: 'scale(1.2)' },
                      },
                    }}
                    onClick={() =>
                      router.push(
                        `/ask-nudgebee?accountId=${
                          router?.query?.accountId || router?.query?.KubernetesDetails || router?.query?.CloudAccountDetails || selectedCluster?.value
                        }`
                      )
                    }
                  >
                    <SafeIcon alt={`Ask ${assistantName}`} src={nubiIconLightUrl} height={22} width={22} />
                    <Typography
                      className='nubi-text'
                      sx={{
                        color: ds.background[100],
                        fontSize: ds.text.small,
                        opacity: 0,
                        maxWidth: 0,
                        whiteSpace: 'nowrap',
                        overflow: 'hidden',
                        transition: 'opacity 0.3s ease, max-width 0.3s ease, margin 0.3s ease',
                      }}
                    >
                      <Box component='span' fontWeight={ds.weight.medium}>
                        {assistantName}:
                      </Box>{' '}
                      how can I assist you?
                    </Typography>
                  </IconButton>
                )}
                {anchorActiveTab.showActiveCluster && (
                  <Box
                    sx={{
                      // Height aligned with ds/Button size='md' (32px) so the cluster
                      // picker sits flush next to the "detail view" button. Border,
                      // radius, hover, and focus halo borrowed from ds/Select trigger.
                      display: 'flex',
                      alignItems: 'center',
                      position: 'relative',
                      height: '32px',
                      backgroundColor: ds.background[100],
                      border: `1px solid ${ds.gray[300]}`,
                      borderRadius: ds.radius.md,
                      transition: 'border-color 120ms ease, box-shadow 120ms ease, background-color 120ms ease',
                      '&:hover': {
                        borderColor: ds.gray[400],
                        backgroundColor: ds.background[200],
                      },
                      '&:focus-within': {
                        borderColor: ds.blue[500],
                        boxShadow: `0 0 0 3px ${ds.blue[100]}`,
                      },
                      // Force the inner MUI Autocomplete (CustomDropdown.jsx hardcodes
                      // minHeight: '36px' on the Autocomplete root + MUI defaults push
                      // the OutlinedInput to ~40px) to fit the 32px shell. Needs
                      // !important to beat the consumer's own sx.
                      '& .MuiFormControl-root': {
                        margin: '0px !important',
                        fontSize: 'var(--ds-text-title) !important',
                      },
                      '& .MuiAutocomplete-root': {
                        height: '32px !important',
                        minHeight: '32px !important',
                        borderRadius: 'inherit !important',
                      },
                      '& .MuiInputBase-root, & .MuiOutlinedInput-root, & .MuiOutlinedInput-root.MuiInputBase-sizeSmall': {
                        height: '32px !important',
                        minHeight: '32px !important',
                        paddingTop: '0 !important',
                        paddingBottom: '0 !important',
                        borderRadius: 'inherit !important',
                        backgroundColor: 'transparent !important',
                      },
                      '& .MuiOutlinedInput-root input, & .MuiAutocomplete-input': {
                        height: '32px !important',
                        fontSize: 'var(--ds-text-body-lg) !important',
                        paddingTop: '0 !important',
                        paddingBottom: '0 !important',
                        paddingLeft: '0 !important',
                        boxSizing: 'border-box',
                      },

                      '& .MuiAutocomplete-input.MuiOutlinedInput-input.MuiInputBase-input': {
                        padding: '0 !important',
                      },
                      '& .MuiInputAdornment-positionStart': {
                        marginRight: '0 !important',
                      },
                      '& .MuiOutlinedInput-notchedOutline': {
                        border: showBorder ? 'inherit' : 'none !important',
                      },
                      '& li': {
                        paddingLeft: '0px !important',
                      },
                    }}
                  >
                    <ClusterDropdown
                      showStatusIndicator={true}
                      headerStyle={true}
                      showIndicator={true}
                      onChange={handleDropdownChange}
                      noLabel
                      onClusterDataLoaded={handleClusterData}
                      minWidth={'224px'}
                      groupByCloudProvider
                      showPadding={true}
                      showAutoEllipsis={true}
                      isDisabled={Boolean(anchorActiveTab.disableDropdown)}
                    />
                  </Box>
                )}
                {anchorActiveTab.clusterDetailButton && (
                  <DsTooltip title='Go to Cluster Details' placement='bottom'>
                    <Link
                      href={
                        selectedCluster?.cloud_provider === 'K8s'
                          ? `/kubernetes/details/${selectedCluster?.value}#summary`
                          : `/cloud-account/details/${selectedCluster?.value}#summary`
                      }
                      target='_blank'
                    >
                      <DsButton tone='secondary' size='md' icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />} iconPlacement='start'>
                        detail view
                      </DsButton>
                    </Link>
                  </DsTooltip>
                )}
                {anchorActiveTab.showGroupingDropdown && (
                  <Select
                    id='app-group'
                    minWidth='250px'
                    label=''
                    value={activeGroup.value?.trim() ? activeGroup.value : ''}
                    options={allAppGroupNames}
                    onChange={(next) => handleChangeGroup(next)}
                    loading={loading}
                    required={false}
                    clearable={false}
                  />
                )}
              </Box>
              <DsDivider orientation='vertical' sx={{ minHeight: '32px', marginLeft: 0, marginRight: 0 }} />
              <Box sx={{ display: 'flex', flexDirection: 'row', gap: ds.space[2] }}>
                <DsButton
                  composition='icon-only'
                  tone='secondary'
                  size='md'
                  tooltip='Coming Soon'
                  tooltipPlacement='bottom'
                  aria-label='Notifications'
                  icon={<SafeIcon alt='Notification Icon' src={NotificationOutlineIconDark} />}
                />
                <Box className='headerHelpMenu'>
                  <DropdownMenu
                    align='end'
                    trigger={
                      <DsButton
                        composition='icon-only'
                        tone='secondary'
                        size='md'
                        tooltip='Help'
                        tooltipPlacement='bottom'
                        aria-label='Help'
                        icon={<SafeIcon alt='Help Icon' src={HelpOutlineDarkIcon} />}
                      />
                    }
                    items={buildHelpMenuItems()}
                  />
                </Box>
              </Box>
            </Box>
          </Box>
        </Box>
      </Box>
    </>
  );
};

export default Header1;
