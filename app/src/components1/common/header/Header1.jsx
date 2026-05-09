import { useState, useEffect, useRef, useMemo } from 'react';
import Box from '@mui/material/Box';
import { Typography, MenuItem } from '@mui/material';
import DOMPurify from 'dompurify';
import { colors } from 'src/utils/colors';
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
import ChatOutlineDarkIcon from '@assets/new/chat-dark-icon.svg';
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
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import Tooltip, { tooltipClasses } from '@mui/material/Tooltip';
import IconButton from '@mui/material/IconButton';
import Menu from '@mui/material/Menu';
import ClusterDropdown from '@common/ClusterDropDown';
import { useSession } from 'next-auth/react';
import PendoInitializer from '@pages/PendoInitializer';
import ButtonMenu from '@common/ButtonMenu';
import K8sAccountModal from '@common/K8sAccountModal';
import JiraAccountModal from '@common/JiraAccountModal';
import GithubAccountModal from '@common/GithubAccountModal';
import ServiceNowAccountModal from '@common/ServiceNowAccountModal';
import CustomButton from '@common/NewCustomButton';
import CustomDropdown from '@common/CustomDropdown';
import apiAppGrouping from '@api1/application-groupings';
import CustomBackButton from '@common/CustomBackButton';
import Link from 'next/link';
import apiAccount from '@api1/account';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { docsUrl } from '@lib/externalUrls';

const Header1 = ({ showBorder = false }) => {
  const { data } = useSession({ required: true });
  const router = useRouter();
  const { assistantName, baseTitle, nubiIconUrl, nubiIconLightUrl } = useTenantBranding();
  const { selectedCluster, allCluster } = useData();
  const selectedClusterRef = useRef(selectedCluster);
  selectedClusterRef.current = selectedCluster;
  const isAlertOpen = useRef(false);
  const avatarSubMenu = data.onPrem ? ['Documentation'] : ['Documentation', 'Chat with us'];

  const [anchorActiveTab, setAnchorActiveTab] = useState('');
  const [snackbarOpen, setSnackbarOpen] = useState(false);
  const [snackbarMsg, setSnackbarMsg] = useState('');
  const [anchorElUser, setAnchorElUser] = useState(null);
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

  const handleChangeGroup = (e, v) => {
    router.push(`/grouping?groupId=${v.id}`);
  };

  const handleOpenUserMenu = (event) => {
    setAnchorElUser(event.currentTarget);
  };

  const handleCloseUserMenu = () => {
    setAnchorElUser(null);
  };

  const getMenuItem = (setting) => {
    if (setting === 'Documentation') {
      return (
        <MenuItem
          key={setting}
          onClick={() => {
            window.open(docsUrl('/docs/features/'), '_blank', 'noopener');
          }}
        >
          <Typography textAlign='left' fontSize={14} display={'flex'} alignItems={'center'} gap={'8px'}>
            <SafeIcon src={DocumentationIcon} alt='Documentation' />
            Documentation
          </Typography>
        </MenuItem>
      );
    } else if (setting === 'Chat with us') {
      return (
        <MenuItem
          key={setting}
          onClick={() => {
            handleCloseUserMenu();
            window?.$chatwoot?.toggle('open');
          }}
        >
          <Typography textAlign='left' fontSize={14} display={'flex'} alignItems={'center'} gap={'8px'}>
            <SafeIcon src={ChatOutlineDarkIcon} alt='Documentation' />
            Chat with us
          </Typography>
        </MenuItem>
      );
    }
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
            {assistantName}, <span style={{ color: colors.text.darkGray, fontWeight: '400' }}>your AI assistant.</span>
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
            {assistantName}, <span style={{ color: colors.text.darkGray, fontWeight: '400' }}>your AI assistant.</span>
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
      } else if (res.data?.nb_versions && res.data?.nb_versions?.agent_version_latest != cluster.agent?.version) {
        let snackMessage = '';
        let disconnectedService = [];
        setSnackbarOpen(true);
        snackMessage = `<span>Update the ${baseTitle} Agent Version to ${res.data?.nb_versions.agent_version_latest}. Refer to <a href="/help/docs/installation/agent/installation/" target="_blank" rel="noopener noreferrer">this document</a> for instructions on how to update the agent.</span>`;
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
      setReloadMsg('A New Application Version is deployed. Please consider Refreshing the page');
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
      {!data.onPrem && data.pendoEnable == 'true' ? <PendoInitializer clusterData={selectedCluster} /> : null}
      <K8sAccountModal openModal={showK8sAccountModal} handleClose={() => setShowK8sAccountModal(false)} />
      <JiraAccountModal openModal={showJiraAccountModal} handleClose={() => setShowJiraAccountModal(false)} />
      <GithubAccountModal openModal={showGitHubAccountModal} handleClose={() => setShowGitHubAccountModal(false)} />
      <ServiceNowAccountModal openModal={showServiceNowAccountModal} handleClose={() => setShowServiceNowAccountModal(false)} />
      <Stack sx={{ width: '100%' }} spacing={2}>
        {snackbarOpen ? (
          <Alert severity='warning' onClose={handleCloseSnackbar}>
            <Typography
              dangerouslySetInnerHTML={{
                __html: DOMPurify.sanitize(snackbarMsg, { ADD_ATTR: ['target', 'rel'] }),
              }}
            />
          </Alert>
        ) : null}
      </Stack>
      <Stack sx={{ width: '100%' }} spacing={2}>
        {showReloadNotification ? (
          <Alert severity='warning' onClose={() => setShowReloadNotification(false)}>
            <Typography
              dangerouslySetInnerHTML={{
                __html: DOMPurify.sanitize(reloadMsg),
              }}
            />
          </Alert>
        ) : null}
      </Stack>
      {allCluster && allCluster.length > 0 && !allCluster.some((c) => c.value !== 'demo') && !loading && (
        <Stack sx={{ width: '100%', justifyContent: 'center' }} spacing={2}>
          <Alert
            icon={false}
            variant='filled'
            severity='error'
            sx={{ display: 'flex', justifyContent: 'center', borderRadius: '0px', backgroundColor: '#800000' }}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px', width: '100%' }}>
              <Typography sx={{ fontSize: '16px', color: '#fff', fontWeight: '400', '& b': { fontWeight: '500' } }}>
                You&lsquo;re currently using a demo account. <b>Add your own K8s account</b> for the full experience!
              </Typography>
              <CustomButton
                text={'Add K8s Account'}
                size='xSmall'
                sx={{
                  padding: '4px 12px',
                  fontSize: '12px',
                  fontWeight: '500',
                  lineHeight: '20px',
                  color: '#fff',
                  backgroundColor: '#213F6D',
                  boxShadow: '0px 2px 4px 0px #1C2E4B7D',
                  borderRadius: '4px',
                  cursor: 'pointer',
                  '&:hover': {
                    backgroundColor: colors.text.secondary,
                  },
                }}
                onClick={() => setShowK8sAccountModal(true)}
              />
              <Typography
                variant='button'
                sx={{ textTransform: 'inherit', textDecoration: 'underline', cursor: 'pointer' }}
                onClick={() => window.open(docsUrl('/docs/features/'), '_blank', 'noopener')}
              >
                need help?
              </Typography>
            </Box>
          </Alert>
        </Stack>
      )}

      <Box sx={{ zIndex: 20, boxShadow: '0px 2px 24px 2px #00000010', width: '100%', position: 'sticky', top: '0px' }}>
        <Box
          sx={{
            width: '100%',
            background: 'white',
            borderBottom: '2px solid #EFF6FF',
          }}
        >
          <Box sx={{ display: 'flex', background: '#ffffff', justifyContent: 'space-between', alignItems: 'center', height: '56px', px: '32px' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              {anchorActiveTab.showBackButton && <CustomBackButton />}
              <Box sx={{ display: 'flex', alignItems: 'center' }}>
                {anchorActiveTab.icon && (
                  <Box sx={{ mx: '8px', display: 'flex', alignItems: 'center' }}>
                    <SafeIcon src={anchorActiveTab.icon} width={26} height={26} alt={anchorActiveTab.name} />
                  </Box>
                )}
                <Typography sx={{ fontFamily: 'Roboto', fontSize: '26px', fontWeight: 600, color: '#374151' }}>{anchorActiveTab.name}</Typography>
              </Box>
            </Box>
            <Box display={'flex'} alignItems={'center'} justifyContent={'center'} gap={'12px'}>
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '6px' }}>
                {anchorActiveTab.connectClusterButton &&
                  hasWriteAccess() &&
                  allCluster?.some((c) => c.value !== 'demo' && c.cloud_provider?.toUpperCase() === 'K8S') && (
                    <CustomButton
                      onClick={() => {
                        router.push(`/accounts/account-form?cloudProvider=K8S`);
                      }}
                      text='Connect cluster'
                      sx={{ mr: '8px' }}
                      size='Medium'
                    />
                  )}
                {anchorActiveTab.connectAccountButton && hasWriteAccess() && (
                  <ButtonMenu
                    title={'Connect Account'}
                    variant='tertiary'
                    size='medium'
                    items={[
                      {
                        text: 'Kubernetes',
                        icon: <OuK8sIcon />,
                        onClick: () => setShowK8sAccountModal(true),
                      },
                      {
                        text: 'Jira',
                        icon: <JiraIcon />,
                        onClick: () => setShowJiraAccountModal(true),
                      },
                      {
                        text: 'Github',
                        icon: <GithubIcon />,
                        onClick: () => setShowGitHubAccountModal(true),
                      },
                      {
                        text: 'ServiceNow',
                        icon: <SafeIcon src={ServiceNowIcon} alt='servicenow' />,
                        onClick: () => setShowServiceNowAccountModal(true),
                      },
                      {
                        text: 'Slack',
                        icon: <SlackIcon />,
                        onClick: () => {
                          const slackURL = '/api/slack/install';
                          window.open(slackURL, '_blank');
                        },
                        accountsCount: slackAccountsCount,
                      },
                      {
                        text: 'Teams',
                        icon: <MsTeamsIcon />,
                        onClick: () => {
                          const MSTEAMS_OAUTH_URL = '/api/integrations/install/ms-teams';
                          window.open(MSTEAMS_OAUTH_URL, '_blank', '"noopener"');
                        },
                      },
                      {
                        text: 'Google Chat',
                        icon: <GChatIcon />,
                        onClick: () => {
                          const gChatURL = '/api/integrations/install/google';
                          window.open(gChatURL, '_blank');
                        },
                        accountsCount: gChatAccountsCount,
                      },
                    ]}
                  />
                )}
                {anchorActiveTab.showAskNudgebee && (
                  <IconButton
                    size='small'
                    sx={{
                      background: 'linear-gradient(120deg, #2C4466 0%,rgb(44, 71, 109) 28%, #2C4466 85%, #355277 100%)',
                      borderRadius: '8px',
                      height: '36px',
                      width: 'fit-content',
                      padding: '0px 0px 0px 8px',
                      display: 'flex',
                      alignItems: 'center',
                      gap: '8px',
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
                          marginRight: '8px',
                        },
                      },
                      '@keyframes popSmooth': {
                        '0%, 100%': {
                          transform: 'scale(1)',
                        },
                        '50%': {
                          transform: 'scale(1.2)',
                        },
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
                    <SafeIcon alt={`Ask ${assistantName}`} src={nubiIconLightUrl} height={26} width={26} />
                    <Typography
                      className='nubi-text'
                      sx={{
                        color: colors.text.white,
                        fontSize: '13px',
                        opacity: 0,
                        maxWidth: 0,
                        whiteSpace: 'nowrap',
                        overflow: 'hidden',
                        transition: 'opacity 0.3s ease, max-width 0.3s ease, margin 0.3s ease',
                      }}
                    >
                      <Box component='span' fontWeight='500'>
                        {assistantName}:
                      </Box>{' '}
                      how can I assist you?
                    </Typography>
                  </IconButton>
                )}
                {anchorActiveTab.showActiveCluster && (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      border: '1px solid #D5DEED',
                      borderRadius: '4px',
                      position: 'relative',
                      height: '36px',
                      '&:hover': {
                        borderColor: '#3B82F6',
                      },
                      '& .MuiFormControl-root': {
                        margin: '0px !important',
                        fontSize: '16px !important',
                      },
                      '& .MuiOutlinedInput-root input': {
                        fontSize: '14px !important',
                      },
                      '& .MuiAutocomplete-input': {
                        fontSize: '14px !important',
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
                  <Tooltip
                    title='Go to Cluster Details'
                    placement='bottom'
                    slotProps={{
                      popper: {
                        sx: {
                          [`&.${tooltipClasses.popper}[data-popper-placement*="right"] .${tooltipClasses.tooltip}`]: {
                            marginLeft: '2px',
                          },
                        },
                      },
                    }}
                  >
                    <Link
                      href={
                        selectedCluster?.cloud_provider === 'K8s'
                          ? `/kubernetes/details/${selectedCluster?.value}#summary`
                          : `/cloud-account/details/${selectedCluster?.value}#summary`
                      }
                      target='_blank'
                    >
                      <CustomButton
                        variant='tertiary'
                        size='Medium'
                        text={'detail view'}
                        startIcon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
                        sx={{
                          fontWeight: 400,
                          colors: 'tertiarymedium',
                          '& img': {
                            width: '16px',
                            height: '16px',
                          },
                        }}
                      />
                    </Link>
                  </Tooltip>
                )}
                {anchorActiveTab.showGroupingDropdown && (
                  <CustomDropdown
                    id={'app-group'}
                    minWidth='250px'
                    value={activeGroup.label ?? ' '}
                    onChange={(e, v) => {
                      handleChangeGroup(e, v);
                    }}
                    options={allAppGroupNames}
                    customStyle={{
                      '.MuiFormControl-root': {
                        marginTop: '8px',
                      },
                    }}
                    loading={loading}
                    label='Select Application Group'
                  />
                )}
              </Box>
              <Box sx={{ width: '0.5px', height: '32px', background: colors.background.tertiarymedium }} />
              <Box sx={{ display: 'flex', flexDirection: 'row', gap: '6px' }}>
                <Tooltip
                  title='Coming Soon'
                  placement='bottom'
                  slotProps={{
                    popper: {
                      sx: {
                        [`&.${tooltipClasses.popper}[data-popper-placement*="right"] .${tooltipClasses.tooltip}`]: {
                          marginLeft: '2px',
                        },
                      },
                    },
                  }}
                >
                  <IconButton
                    size='small'
                    sx={{
                      border: '1px solid #D0D0D0',
                      borderRadius: '4px',
                      height: '36px',
                      width: '36px',
                    }}
                  >
                    <SafeIcon alt='Help Icon' src={NotificationOutlineIconDark} />
                  </IconButton>
                </Tooltip>
                <Box className='headerHelpMenu'>
                  <Tooltip
                    title='Help'
                    placement='bottom'
                    slotProps={{
                      popper: {
                        sx: {
                          [`&.${tooltipClasses.popper}[data-popper-placement*="right"] .${tooltipClasses.tooltip}`]: {
                            marginLeft: '2px',
                          },
                        },
                      },
                    }}
                  >
                    <IconButton
                      onClick={handleOpenUserMenu}
                      size='small'
                      sx={{
                        border: '1px solid #D0D0D0',
                        borderRadius: '4px',
                        height: '36px',
                        width: '36px',
                      }}
                    >
                      <SafeIcon alt='Help Icon' src={HelpOutlineDarkIcon} />
                    </IconButton>
                  </Tooltip>
                  <Menu
                    id='help-menu'
                    anchorEl={anchorElUser}
                    anchorOrigin={{
                      vertical: 'bottom',
                      horizontal: 'right',
                    }}
                    keepMounted
                    transformOrigin={{
                      vertical: 'top',
                      horizontal: 'right',
                    }}
                    open={Boolean(anchorElUser)}
                    onClose={handleCloseUserMenu}
                  >
                    {avatarSubMenu.map((setting) => getMenuItem(setting))}
                  </Menu>
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
