import { Box, Typography, Grid } from '@mui/material';
import React, { useEffect, useRef, useState, useMemo } from 'react';
import { formatMemory } from '@lib/formatter';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import Currency from '@common-new/format/Currency';
import KubernetesRightSizingUpdateForm from '@components1/recommendations/KubernetesRightSizingUpdateForm';
import KubernetesUtilization from '@components1/k8s/KubernetesUtilization';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { Modal } from '@components1/ds/Modal';
import AutoOptimizeForm from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingForm';
import Datetime from '@common-new/format/Datetime';
import { BetaIcon, ExternalLinkIcon, GithubIcon } from '@assets';
import PropTypes from 'prop-types';
import { toast as snackbar } from '@components1/ds/Toast';
import { hasWriteAccess } from '@lib/auth';
import { ANNOTATIONS, CI_PREFIX } from '@lib/annotationKeys';
import CustomDropdown from '@components1/common/CustomDropdown';
import apiIntegrations from '@api1/integrations';
import CustomButton from '@components1/common/NewCustomButton';
import apiAccount from '@api1/account';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import { useData } from '@context/DataContext';
import Title from '@components1/common/Title';
import Text from '@common-new/format/Text';
import AutoOptimizeVerticalRightSizingScheduledConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import AutoPilotHeaderCard from '@components1/autopilot/card/AutoOptimizeHeaderCard';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomPRLink from '@components1/common/CustomPRLink';
import { colors, ds } from 'src/utils/colors';
import apiHome from '@api1/home';
import useRecommendationExport from '@hooks/useRecommendationExport';
import { Link as CustomLink } from '@components1/ds/Link';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import SafeIcon from '@components1/common/SafeIcon';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/ds/Tooltip';
import { syncFilterFromQuery } from '@utils/common';

// DS V2 primitives — phased in alongside the legacy components. Visual swap only;
// API calls, handlers, and modal forms (AutoOptimizeForm and friends) untouched.
import WidgetCard from '@components1/ds/WidgetCard';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Stat } from '@components1/ds/Stat';
import { CostCallout } from '@components1/ds/CostCallout';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { ScanRefreshButton } from './ScanRefreshButton';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Comparison as DsComparison, ComparisonGroup as DsComparisonGroup } from '@components1/ds/Comparison';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';

const RIGHT_SIZING_HEADER = [
  'Name',
  { name: 'Namespace', width: '12%' },
  { name: 'Kind', width: '10%' },
  { name: 'CPU Req/Recommended', secondryText: '(Core)' },
  { name: 'Memory Req/Recommended', secondryText: '(MB)' },
  'Updated at',
  { name: 'Savings' },
  '',
];

const KubernetesRightSizing = ({ enabledSummary = true, enabledFilters = true, isOptimisePage = false, resourceIds, groupName, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const kubernetesRightSizingTable = 'kubernetesRightSizingTable';
  const { selectedCluster, allCluster } = useData();

  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [kubernetesRightSizing, setKubernetesRightSizing] = useState([]);
  const [kubernetesRightSizingCount, setKubernetesRightSizingCount] = useState(0);
  const [kubernetesRightSizingEstimatedSaving, setKubernetesRightSizingEstimatedSaving] = useState(0);
  const [openKubernetesRightSizingUpdateForm, setOpenKubernetesRightSizingUpdateForm] = useState(false);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace || '');
  const [workloadTypeFilter, setWorkloadTypeFilter] = useState([]);
  const [selectedWorkloadType, setSelectedWorkloadType] = useState('');
  const [workloadNameFilter, setWorkloadNameFilter] = useState([]);
  const [selectedWorkloadName, setSelectedWorkloadName] = useState('');
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [recommendationStatus, setRecommendationStatus] = useState([{ label: 'Open', value: 'Open' }]);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [isResolvedFormOpen, setIsResolvedFormOpen] = useState(false);
  const [isAutoPilotScheduledFormOpen, setIsAutoPilotScheduledFormOpen] = useState(false);
  const [isAutoPilotContinuousFormOpen, setIsAutoPilotContinuousFormOpen] = useState(false);
  const [autoPilotData, setAutoPilotData] = useState({});
  const [kubernetesTotalCost, setKubernetesTotalCost] = useState('-');
  const [kubernetesFixedCount, setKubernetesFixedCount] = useState(0);
  const [isLoading, setIsLoading] = useState(false);
  const [updatedData, setUpdatedData] = useState({});
  const [allocatedData, setAllocatedData] = useState({});
  const [isDailyTimeFrameOpen, setIsDailyTimeFrameOpen] = useState(false);
  const [isWeeklyTimeFrameOpen, setIsWeeklyTimeFrameOpen] = useState(false);
  const [algo, setAlgo] = useState('NBALGO');
  const [activeButton, setActiveButton] = useState(null);
  const [selectedButtons, setSelectedButtons] = useState({
    algo: 0,
    buffer: 0,
    memory: 0,
    memBuffer: 0,
    cpuLimit: 0,
    memLimit: 0,
  });
  const [notificationData, setNotificationData] = useState({
    email: false,
    slack: false,
    teams: false,
    google_chat: false,
    channelId: '',
    teamsId: '',
    msChannelId: '',
    gChatChannelId: '',
    gChatChannelName: '',
  });
  const [additionalCpuInfo, setAdditionalCpuInfo] = useState({});
  const [additionalMemInfo, setAdditionalMemInfo] = useState({});
  const [listAutoPilots, setListAutoPilots] = useState();
  const [googleChannelList, setGoogleChannelList] = useState([]);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');
  const [openCreatePR, setOpenCreatePR] = useState(false);
  const [allGitIntegrations, setAllGitIntegrations] = useState([]); // Combined GitHub + GitLab integrations
  const [selectedGitIntegration, setSelectedGitIntegration] = useState(''); // Format: "github:name" or "gitlab:name"
  const [selectedWorkloadAnnotations, setSelectedWorkloadAnnotations] = useState({});
  const [prLoading, setPRLoading] = useState(false);
  const [isGitReposLoading, setIsGitReposLoading] = useState(false);
  const [msTeamsData, setMsTeamsData] = useState([]);
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState(false);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState(false);
  const [ticketExists, setTicketExists] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(props?.kubernetes?.id || selectedCluster?.value);

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id || selectedCluster?.value);
  }, [props?.kubernetes?.id, selectedCluster?.value]);

  const accountsRef = useRef(accounts);

  // Helper to detect git provider from repo URL
  const detectGitProvider = (repoUrl) => {
    if (!repoUrl) return null;
    const url = repoUrl.toLowerCase();
    if (url.includes('github.com')) return 'github';
    if (url.includes('gitlab')) return 'gitlab'; // Covers gitlab.com and self-hosted GitLab instances
    return null;
  };

  // Filter integrations based on the repo URL in annotations
  const filteredGitIntegrations = useMemo(() => {
    const repoUrl = selectedWorkloadAnnotations[ANNOTATIONS.CI_GIT_REPO] || selectedWorkloadAnnotations[ANNOTATIONS.WORKLOAD_GIT_REPO];
    const detectedProvider = detectGitProvider(repoUrl);
    if (!detectedProvider) return allGitIntegrations; // Show all if can't detect
    return allGitIntegrations.filter((i) => i.type === detectedProvider);
  }, [selectedWorkloadAnnotations, allGitIntegrations]);

  // Auto-select first filtered integration when available
  useEffect(() => {
    if (filteredGitIntegrations.length > 0 && !selectedGitIntegration) {
      setSelectedGitIntegration(filteredGitIntegrations[0].key);
    }
  }, [filteredGitIntegrations, selectedGitIntegration]);

  useEffect(() => {
    accountsRef.current = accounts;
  }, [accounts]);

  const tableHeaders = React.useMemo(() => {
    if (!groupName) {
      return RIGHT_SIZING_HEADER;
    }
    const newHeaders = [...RIGHT_SIZING_HEADER];
    newHeaders[0] = `Name (${groupName} Group)`;
    return newHeaders;
  }, [groupName]);

  useEffect(() => {
    if (isOptimisePage) {
      if (allCluster?.length) {
        setAccounts(allCluster);
      } else {
        apiHome.getCloudAccounts('K8s').then((res) => {
          setAccounts(res);
        });
      }
    }
  }, [isOptimisePage, allCluster]);

  const [underProvisionedResources, setUnderProvisionedResources] = useState(0);

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const createTicket = () => {
    setTicketData(autoPilotData?.data);
    setIsTicketCreateFormOpen(true);
    setIsAutoPilotContinuousFormOpen(false);
    setIsAutoPilotScheduledFormOpen(false);
  };

  const listGitConfigurations = () => {
    setIsGitReposLoading(true);
    Promise.all([
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'github' }),
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'gitlab' }),
    ])
      .then(([githubRes, gitlabRes]) => {
        const githubData =
          githubRes?.data?.length > 0
            ? githubRes.data.map((g) => ({ name: g.name, type: 'github', key: `github:${g.name}`, label: `GitHub: ${g.name}` }))
            : [];
        const gitlabData =
          gitlabRes?.data?.length > 0
            ? gitlabRes.data.map((g) => ({ name: g.name, type: 'gitlab', key: `gitlab:${g.name}`, label: `GitLab: ${g.name}` }))
            : [];
        const combined = [...githubData, ...gitlabData];
        setAllGitIntegrations(combined);
      })
      .catch((error) => {
        console.error('Error fetching Git configurations:', error);
        setAllGitIntegrations([]);
      })
      .finally(() => {
        setIsGitReposLoading(false);
      });
  };

  const createPR = () => {
    closeResolveModal();
    setOpenCreatePR(true);
    listGitConfigurations();
  };

  const closeKubernetesRightSizingUpdateForm = () => {
    setOpenKubernetesRightSizingUpdateForm(false);
  };

  const onNamespaceFilterChange = (e, _p) => {
    setSelectedNamespace(e?.target?.value);
    setSelectedWorkloadName('');
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
    setPage(0);
  };

  const onWorkloadTypeFilterChange = (e, _p) => {
    setSelectedWorkloadType(e?.target?.value);
    setPage(0);
  };

  const onWorkloadNameFilterChange = (e, _p) => {
    setSelectedWorkloadName(e?.target?.value);
    setPage(0);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const handleResolved = (row) => {
    const recommendations = row?.data?.recommendation;
    if (Object.keys(recommendations).length > 0) {
      const newCpuInfo = {};
      const newMemInfo = {};
      const allocatedObject = {};
      const recommendedObject = {};
      for (const c of Object.keys(recommendations)) {
        const containerObject = recommendations[c];
        const cpuObject = containerObject.find((g) => g.resource === 'cpu') || {};
        const memoryObject = containerObject.find((g) => g.resource === 'memory') || {};
        newCpuInfo[c] = {
          ...newCpuInfo[c],
          p99: cpuObject?.add_info?.cpu_percentile_99 || null,
          p97: cpuObject?.add_info?.cpu_percentile_97 || null,
          p95: cpuObject?.add_info?.cpu_percentile_95 || null,
          nbalgo: cpuObject?.recommended?.request || null,
        };
        newMemInfo[c] = {
          ...newMemInfo[c],
          limit: memoryObject?.add_info?.actual_recommended_limit || null,
          req: memoryObject?.add_info?.actual_recommended_request || null,
          nbalgoReq: memoryObject?.recommended?.request || null,
          nbalgoLimit: memoryObject?.recommended?.limit || null,
        };
        allocatedObject[c] = {
          ...allocatedObject[c],
          cpu: {
            request: cpuObject?.allocated?.request || null,
            limit: cpuObject?.allocated?.limit || null,
          },
          memory: {
            request: formatMemory(memoryObject?.allocated?.request, 'bytes', 'mb', false) || undefined,
            limit: formatMemory(memoryObject?.allocated?.limit, 'bytes', 'mb', false) || null,
          },
        };
        recommendedObject[c] = {
          ...recommendedObject[c],
          cpu: {
            request: cpuObject?.recommended?.request || undefined,
            limit: cpuObject?.recommended?.limit || undefined,
          },
          memory: {
            request: formatMemory(memoryObject?.recommended?.request, 'bytes', 'mb', false) || undefined,
            limit: formatMemory(memoryObject?.recommended?.limit, 'bytes', 'mb', false) || undefined,
          },
        };
      }
      setAdditionalCpuInfo((prev) => ({ ...prev, ...newCpuInfo }));
      setAdditionalMemInfo((prev) => ({ ...prev, ...newMemInfo }));
      setAllocatedData(allocatedObject);
      setUpdatedData(recommendedObject);
    }
    setAutoPilotData({
      id: row.id,
      accountId: selectedAccountId || row?.data?.account_id,
      resourceId: row?.resourceId,
      data: row.data,
      saving: row.data.estimated_savings,
      resource_filter: [
        {
          // Workload entries have meta.config.namespace, Pod entries have meta.namespace
          namespace: row.data.cloud_resourse.meta?.config?.namespace || row.data.cloud_resourse.meta?.namespace,
          // If it's a workload entry, use .name directly
          // If it's a pod entry (old data), use .meta.controller
          name: row.data.cloud_resourse.type === 'Pod' ? row.data.cloud_resourse.meta.controller : row.data.cloud_resourse.name,
          // If it's a workload entry, use .type directly
          // If it's a pod entry (old data), use .meta.controllerKind
          type: row.data.cloud_resourse.type === 'Pod' ? row.data.cloud_resourse.meta.controllerKind : row.data.cloud_resourse.type,
        },
      ],
      recommendationId: row.data.id,
    });
    setIsResolvedFormOpen(true);
    setIsAutoPilotContinuousFormOpen(false);
    setIsAutoPilotScheduledFormOpen(false);
    getWorkloadDeploymentForSelectedRightSize(row.data);
    setTicketExists(row.data.ticket !== undefined && row.data.ticket !== null && row.data.ticket !== '');
  };

  const getWorkloadDeploymentForSelectedRightSize = async (data) => {
    try {
      // Use account_id from recommendation data, fallback to selectedAccountId
      const accountIdToUse = data?.account_id || selectedAccountId;
      const res = await k8sApi.getK8sWorkload(1, 0, {
        accountId: accountIdToUse,
        namespaceName: data?.cloud_resourse?.meta?.config?.namespace || data?.cloud_resourse?.meta?.namespace,
        workloadName: data?.cloud_resourse?.type === 'Pod' ? data?.cloud_resourse?.meta?.controller : data?.cloud_resourse?.name,
        workloadType: data?.cloud_resourse?.type === 'Pod' ? data?.cloud_resourse?.meta?.controllerKind : data?.cloud_resourse?.type,
        exactNameMatch: true,
      });

      const workloads = res?.data?.k8s_workloads || [];
      if (workloads && workloads.length == 1) {
        const workload = workloads[0];
        const annotations = workload.meta?.config?.annotations || {};

        // Filter for both Nudgebee and ArgoCD annotations from k8s annotations
        const filteredKeys = Object.keys(annotations).filter((key) => key.startsWith(CI_PREFIX) || key.startsWith('argocd.argoproj.io'));
        const filteredObject = {};
        if (filteredKeys && filteredKeys.length > 0) {
          filteredKeys.forEach((key) => {
            filteredObject[key] = annotations[key];
          });
          setSelectedWorkloadAnnotations(filteredObject);
          return;
        }

        // If no k8s annotations found, check cloud_resource_attributes for manual CI configuration
        // Only check ci.nudgebee.com (for PR creation), not workloads.nudgebee.com (for code tracking only)
        if (workload.cloud_resource_id) {
          const attributes = await k8sApi.getResourceAttributes(workload.cloud_resource_id);
          const manualConfig = {};
          attributes.forEach((attr) => {
            if (attr.name.startsWith(CI_PREFIX)) {
              manualConfig[attr.name] = attr.value;
            }
          });
          if (Object.keys(manualConfig).length > 0) {
            setSelectedWorkloadAnnotations(manualConfig);
            return;
          }
        }

        // No configuration found
        setSelectedWorkloadAnnotations({});
      }
    } catch (error) {
      console.error('Error fetching workload deployment:', error);
      setSelectedWorkloadAnnotations({});
    }
  };

  const closeResolvedFormModal = () => {
    setIsResolvedFormOpen(false);
    setIsAutoPilotContinuousFormOpen(false);
    setIsAutoPilotScheduledFormOpen(false);
    setSelectedButtons({
      algo: 0,
      buffer: 0,
      memory: 0,
      memBuffer: 0,
      cpuLimit: 0,
      memLimit: 0,
    });
  };

  const closeAutoPilotScheduledConfigModal = (success) => {
    setSelectedButtons({
      algo: 0,
      buffer: 0,
      memory: 0,
      memBuffer: 0,
    });
    setIsAutoPilotScheduledFormOpen(false);
    setIsWeeklyTimeFrameOpen(!isWeeklyTimeFrameOpen);
    setIsDailyTimeFrameOpen(!isDailyTimeFrameOpen);
    setNotificationData({
      ...notificationData,
      slack: false,
      teams: false,
      google_chat: false,
    });
    setGoogleChannelList([]);
    if (success) {
      snackbar.success('Auto Optimize Created Successfully');
      fetchActiveAutoPilots();
    }
  };

  const closeAutoPilotContinuousConfigModal = (success) => {
    setSelectedButtons({
      algo: 0,
      buffer: 0,
      memory: 0,
      memBuffer: 0,
    });
    setIsAutoPilotContinuousFormOpen(false);
    setIsWeeklyTimeFrameOpen(!isWeeklyTimeFrameOpen);
    setIsDailyTimeFrameOpen(!isDailyTimeFrameOpen);
    setNotificationData({
      ...notificationData,
      slack: false,
      teams: false,
      google_chat: false,
    });
    setGoogleChannelList([]);
    if (success) {
      snackbar.success('Auto Optimize Created Successfully');
      fetchActiveAutoPilots();
    }
  };

  const getTicketDescription = () => {
    if (!ticketData) {
      return '';
    }

    if (Object.entries(ticketData).length == 0) {
      return '';
    }

    let description = '';
    description += '**Name**: ' + ticketData?.cloud_resourse?.name + '\n';
    description += '**Namespace**: ' + ticketData?.cloud_resourse?.meta?.namespace + '\n\n';
    if (ticketData?.recommendation) {
      for (const key in ticketData?.recommendation) {
        description += '**Container Name**: ' + key + '\n';
        const memoryData = ticketData?.recommendation?.[key]?.filter((e) => e.resource == 'memory')[0];
        const cpuData = ticketData?.recommendation?.[key]?.filter((e) => e.resource == 'cpu')[0];
        let cpuExistingReq = cpuData?.allocated?.request || 'N/A';
        let cpuExistingLimit = cpuData?.allocated?.limit || 'N/A';
        let memoryExistingReq = formatMemory(memoryData?.allocated?.request) || 'N/A';
        let memoryExistingLimit = formatMemory(memoryData?.allocated?.limit) || 'N/A';

        let cpuReccReq = cpuData?.recommended?.request || 'N/A';
        let cpuReccLimit = cpuData?.recommended?.limit || 'N/A';
        let memoryReccReq = formatMemory(memoryData?.recommended?.request) || 'N/A';
        let memoryReccLimit = formatMemory(memoryData?.recommended?.limit) || 'N/A';
        description += '**Existing CPU Request**: ' + cpuExistingReq + '\n';
        description += '**Existing CPU Limit**: ' + cpuExistingLimit + '\n';
        description += '**Existing Memory Request**: ' + memoryExistingReq + '\n';
        description += '**Existing Memory Limit**: ' + memoryExistingLimit + '\n';
        description += '**Recommended CPU Request**: ' + cpuReccReq + '\n';
        description += '**Recommended CPU Limit**: ' + cpuReccLimit + '\n';
        description += '**Recommended Memory Request**: ' + memoryReccReq + '\n';
        description += '**Recommended Memory Limit**: ' + memoryReccLimit + '\n';
        description += '**Estimated Savings**: ' + ticketData?.estimated_savings + '\n\n';
      }
    }
    return description;
  };

  const fetchActiveAutoPilots = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    recommendationApi
      .getAutoOptimize({ accountId: selectedAccountId, status: 'Active', category: ['continuous_rightsize', 'vertical_rightsize'] })
      .then((res) => {
        let listRecommendation = res?.data?.auto_pilot ?? [];
        setListAutoPilots(listRecommendation);
      });
  };

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    setPage(0);
    fetchActiveAutoPilots();
  }, [selectedAccountId, recommendationStatus]);

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const listRecommendations = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (!Array.isArray(listAutoPilots)) {
      return;
    }
    if (isOptimisePage && accounts.length === 0) {
      return;
    }
    setLoading(true);
    setKubernetesRightSizing([]);
    setKubernetesRightSizingCount(0);
    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pod_right_sizing',
        resourceNamespace: selectedNamespace,
        resourceWorkloadType: selectedWorkloadType,
        resourceWorkloadName: selectedWorkloadName || undefined,
        status: recommendationStatus.length > 0 ? recommendationStatus.map((s) => s.value) : undefined,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
        accountObjectId: props?.accountObjectId,
        resource_ids: resourceIds,
      })
      .then((res) => {
        let k8sRecommendationData = res?.data?.recommendation?.map((item) => {
          const data = [];
          let hasAutopilotConfigured = false;
          let autoPilotId;
          let scheduledAutoPilotId;
          let continuousAutoPilotId;
          // Handle both Pod entries (use meta.controller) and Workload entries (use name directly)
          const itemWorkloadName = item.cloud_resourse.type === 'Pod' ? item.cloud_resourse?.meta?.controller : item.cloud_resourse?.name;
          for (let a of listAutoPilots) {
            let matched = false;
            for (let r of a.auto_optimize_resource_maps) {
              if (
                (r?.resource_identifier?.name == itemWorkloadName || r?.resource_identifier?.name == null) &&
                r?.resource_identifier?.namespace == item.resource_k8s_namespace
              ) {
                matched = true;
                break;
              }
            }
            if (matched) {
              hasAutopilotConfigured = true;
              autoPilotId = a.id;
              if (a.category === 'vertical_rightsize') {
                scheduledAutoPilotId = a.id;
              } else if (a.category === 'continuous_rightsize') {
                continuousAutoPilotId = a.id;
              }
            }
          }
          item.hasAutopilotConfigured = hasAutopilotConfigured;
          item.scheduledAutoPilotId = scheduledAutoPilotId;
          item.continuousAutoPilotId = continuousAutoPilotId;
          // Handle both Pod entries (use meta.controller) and Workload entries (use name directly)
          let workloadName = item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controller : item.cloud_resourse.name;
          data.push({
            component: (
              <>
                <CustomLink
                  href={{
                    pathname: `/kubernetes/details/${props?.kubernetes?.id || item.account_id}`,
                    query: {
                      subtab: 1,
                      tab: 3,
                      namespace: item.cloud_resourse.meta?.config?.namespace || item.cloud_resourse.meta?.namespace,
                      workloadName: workloadName,
                      workloadType: item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controllerKind : item.cloud_resourse.type,
                    },
                    hash: 'kubernetes/applications',
                  }}
                  target='_blank'
                >
                  {workloadName}
                </CustomLink>
                <Text value={`Pod- ` + item.cloud_resourse.name} secondaryText />
                {isOptimisePage && (
                  <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)' }}>
                    <Text value={'acc: '} secondaryText />
                    <CustomLink
                      href={{
                        pathname: `/kubernetes/details/${item.account_id}`,
                      }}
                      target='_blank'
                      secondaryText
                    >
                      {getAccountName(item.account_id)}
                    </CustomLink>
                  </Box>
                )}
                {item.ticket && <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} />}
                {item.resolution && <CustomPRLink prURL={item.resolution.type_reference_id} statusMessage={item.resolution.status_message} />}
              </>
            ),
            drilldownQuery: {
              podName: item.cloud_resourse.name,
              workloadName: item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controller : item.cloud_resourse.name,
              namespaceName: item.cloud_resourse.meta?.config?.namespace || item.cloud_resourse.meta?.namespace,
              resourceId: item.cloud_resourse.resourceId,
              recommendation: item,
              accountId: item.account_id,
            },
          });
          data.push({
            component: <Text value={item.cloud_resourse.meta?.config?.namespace || item.cloud_resourse.meta?.namespace} />,
          });
          data.push({
            component: <Text value={item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controllerKind : item.cloud_resourse.type} />,
          });
          // CPU column — one Comparison per container, stacked.
          // Units (Core / MB) live in the column header, so we omit `unit` on
          // each atom to keep the row narrow enough for a single line in the
          // 14%-wide cell. Re-enable `unit: 'Core'` if the column ever widens.
          data.push({
            component: (
              <DsComparisonGroup spacing='xs'>
                {Object.entries(item.recommendation)
                  .map(([key, value]) => [key, value?.filter((v) => v.resource === 'cpu')])
                  .map(([key, value]) => {
                    const allocated = value?.[0]?.allocated?.request;
                    const recommended = value?.[0]?.recommended?.request;
                    return (
                      <DsComparison
                        key={key}
                        label={key}
                        size='sm'
                        polarity='lower-is-better'
                        before={{ value: typeof allocated === 'number' ? allocated : null }}
                        after={{ value: typeof recommended === 'number' ? recommended : null }}
                      />
                    );
                  })}
              </DsComparisonGroup>
            ),
          });
          // Memory column — same structure as CPU; unit conveyed by header.
          // Convert raw bytes → MB directly; formatMemory returns a comma-formatted
          // string ("1,024") which Number() can't parse.
          data.push({
            component: (
              <DsComparisonGroup spacing='xs'>
                {Object.entries(item.recommendation)
                  .map(([key, value]) => [key, value?.filter((v) => v.resource === 'memory')])
                  .map(([key, value]) => {
                    const allocatedBytes = value?.[0]?.allocated?.request;
                    const recommendedBytes = value?.[0]?.recommended?.request;
                    const allocatedMb = typeof allocatedBytes === 'number' ? allocatedBytes / (1024 * 1024) : null;
                    const recommendedMb = typeof recommendedBytes === 'number' ? recommendedBytes / (1024 * 1024) : null;
                    return (
                      <DsComparison
                        key={key}
                        label={key}
                        size='sm'
                        polarity='lower-is-better'
                        before={{ value: allocatedMb }}
                        after={{ value: recommendedMb }}
                      />
                    );
                  })}
              </DsComparisonGroup>
            ),
          });
          data.push({
            component: <Datetime value={item.updated_at} />,
            data: item.updated_at,
          });
          data.push({
            component: <Currency value={item.estimated_savings} precison={1} suffix='/mo' />,
            data: item.estimated_savings,
          });
          data.push({
            component: (
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}
              >
                {hasWriteAccess(item.account_id || selectedAccountId) && (
                  <DsButton
                    tone='secondary'
                    size='xs'
                    id={`rs-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={() => {
                      handleResolved({
                        id: autoPilotId,
                        resourceId: item.cloud_resourse.id,
                        data: item,
                      });
                    }}
                  >
                    {hasAutopilotConfigured ? 'Configured' : 'Optimize'}
                  </DsButton>
                )}
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <span>
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      aria-label={`Ask ${assistantName}`}
                      id={`k8s-rs-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
                        const resourceName =
                          item.cloud_resourse?.type === 'Pod'
                            ? item.cloud_resourse?.meta?.controller || item.cloud_resourse?.name
                            : item.cloud_resourse?.name || item.resource_name || '';
                        const namespace = item.cloud_resourse?.meta?.config?.namespace || item.cloud_resourse?.meta?.namespace || '';
                        const prompt = buildNubiOptimizePrompt({
                          ruleName: item.rule_name || 'Pod Right Sizing',
                          category: item.category || 'RightSizing',
                          severity: item.severity || 'Info',
                          resourceName,
                          resourceType: item.cloud_resourse?.type || '',
                          namespace,
                          estimatedSavings: item.estimated_savings || undefined,
                        });
                        setNubiQuery(prompt);
                        setNubiAccountId(item.account_id || selectedAccountId);
                        setNubiConversationId(`recom_${item.id}`);
                        setNubiSidebarVisible(true);
                      }}
                    />
                  </span>
                </CustomTooltip>
              </Box>
            ),
          });
          return data;
        });
        setKubernetesRightSizing(k8sRecommendationData);
        setKubernetesRightSizingCount(res?.data?.recommendation_aggregate?.aggregate?.count);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listRecommendations();
  }, [
    selectedAccountId,
    page,
    rowsPerPage,
    selectedNamespace,
    selectedWorkloadType,
    selectedWorkloadName,
    recommendationStatus,
    listAutoPilots,
    accounts?.length,
    isOptimisePage,
    resourceIds,
  ]);

  useEffect(() => {
    if (router.isReady && router.query.namespace) {
      if (namespaceFilter && namespaceFilter.length > 0) {
        const namespaceExists = namespaceFilter.find((ns) => ns === router.query.namespace);
        if (namespaceExists) {
          setSelectedNamespace(router.query.namespace);
        } else {
          setSelectedNamespace('');
          applyFiltersOnRouter(router, { namespace: '' });
        }
      }
    } else {
      setSelectedNamespace('');
    }
  }, [router.isReady, router.query.namespace, namespaceFilter]);

  useEffect(() => {
    if (!router?.query?.status) return;
    const statusOptions = RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }));
    setRecommendationStatus(syncFilterFromQuery(statusOptions, router?.query?.status, (f) => f.value));
    setPage(0);
  }, [router?.query?.status]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (!enabledSummary) {
      return;
    }
    recommendationApi
      .getK8sRecommendationSummary({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pod_right_sizing',
        status: ['Open', 'InProgress'],
        resource_ids: resourceIds,
      })
      .then((res) => {
        setKubernetesFixedCount(res?.data?.recommendation_aggregate.aggregate.count);
        setKubernetesTotalCost(res?.data?.spends_aggregate?.aggregate?.sum?.amount);
        setKubernetesRightSizingEstimatedSaving(res?.data?.recommendation_aggregate.aggregate.sum.estimated_savings);
        setUnderProvisionedResources(res?.data?.recommendation_expense_aggregate?.aggregate?.count);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [selectedAccountId, enabledSummary, resourceIds]);

  useEffect(() => {
    if (!selectedAccountId) {
      return;
    }
    if (!enabledFilters) {
      return;
    }
    k8sApi.getK8sNamespaceNames(selectedAccountId).then((res) => {
      setNamespaceFilter(res?.data?.namespaces);
    });

    k8sApi.listK8sPodWorkloadType({ accountId: selectedAccountId }).then((res) => {
      let data = res.data.k8s_pods;
      setWorkloadTypeFilter(data?.map((d) => d.workload_type));
    });
  }, [selectedAccountId, enabledFilters]);

  useEffect(() => {
    if (!selectedAccountId || !enabledFilters) {
      return;
    }
    k8sApi.getK8sWorkloadNames({ accountId: selectedAccountId, namespace: selectedNamespace || undefined }).then((res) => {
      setWorkloadNameFilter(res?.data?.workloadNames || []);
    });
  }, [selectedAccountId, enabledFilters, selectedNamespace]);

  const submitRecommendation = () => {
    for (const d in updatedData) {
      if (updatedData[d].memory) {
        for (const key in updatedData[d].memory) {
          let value = updatedData[d].memory[key];
          if (value) {
            updatedData[d].memory[key] = value + 'Mi';
          }
        }
      }
    }
    recommendationApi.applyRecommendation(autoPilotData.accountId, autoPilotData.recommendationId, updatedData).then((res) => {
      if (res?.errors) {
        snackbar.error('An error occurred');
        setIsResolvedFormOpen(true);
        setIsAutoPilotContinuousFormOpen(false);
        setIsAutoPilotScheduledFormOpen(false);
      } else {
        snackbar.success('Deployed fix successfully');
        setIsResolvedFormOpen(false);
        setIsAutoPilotContinuousFormOpen(false);
        setIsAutoPilotScheduledFormOpen(false);
      }
    });
  };

  const getChannelsListSlackMsTeams = async () => {
    const platforms = ['slack', 'ms_teams', 'google_chat'];

    setIsMsTeamsLoading(true);
    try {
      const resMsTeams = await apiAccount.getNotificationChannelList(platforms[1]);
      const teamOptionsMsTeams =
        resMsTeams?.data?.data?.map((item) => ({
          label: item.name,
          value: item.id,
          channels: item.channels,
        })) || [];
      setMsTeamsData(teamOptionsMsTeams);
    } finally {
      setIsMsTeamsLoading(false);
    }

    setIsGoogleChannelsLoading(true);
    try {
      const resGoogle = await apiAccount.getNotificationChannelList(platforms[2]);
      const googleOptions =
        resGoogle?.data?.data?.map((item) => ({
          label: item.name,
          value: item.id,
        })) || [];
      setGoogleChannelList(googleOptions);
    } finally {
      setIsGoogleChannelsLoading(false);
    }
  };

  const addScheduleAutoPilot = () => {
    setIsResolvedFormOpen(false);
    getChannelsListSlackMsTeams();
    setIsAutoPilotScheduledFormOpen(true);
  };

  const addContinuousAutoPilot = () => {
    setIsResolvedFormOpen(false);
    getChannelsListSlackMsTeams();
    setIsAutoPilotContinuousFormOpen(true);
  };

  const closeResolveModal = () => {
    setIsResolvedFormOpen(false);
  };

  useEffect(() => {
    if (!isResolvedFormOpen) {
      setActiveButton(null);
    }
  }, [isResolvedFormOpen]);

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const getButtons = () => {
    return [
      {
        label: 'Cancel',
        backgroundColor: 'transparent',
        onClick: closeResolveModal,
      },
      {
        label: 'Create Ticket',
        backgroundColor: 'transparent',
        onClick: createTicket,
        isDisabled: ticketExists,
      },
      {
        label: 'Create Pull Request',
        backgroundColor: 'transparent',
        onClick: createPR,
        betaIcon: <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: 'var(--ds-space-1)' }} />,
      },
      {
        label: autoPilotData?.data?.continuousAutoPilotId ? 'View Continuous Auto Optimize' : 'Continuous Auto Optimize',
        backgroundColor: 'transparent',
        onClick: autoPilotData?.data?.continuousAutoPilotId
          ? () => {
              window.open(`/auto-pilot/task/${autoPilotData?.data?.continuousAutoPilotId}?accountId=${autoPilotData?.accountId}`, '_blank');
            }
          : addContinuousAutoPilot,
        isDisabled: !autoPilotData?.data?.continuousAutoPilotId && autoPilotData?.data?.scheduledAutoPilotId,
        betaIcon: autoPilotData?.data?.continuousAutoPilotId ? (
          <SafeIcon src={ExternalLinkIcon} alt='Open in new tab' width={12} height={12} />
        ) : (
          <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: 'var(--ds-space-1)' }} />
        ),
      },
      {
        label: autoPilotData?.data?.scheduledAutoPilotId ? 'View Schedule Auto Optimize' : 'Schedule Auto Optimize',
        backgroundColor: 'transparent',
        onClick: autoPilotData?.data?.scheduledAutoPilotId
          ? () => {
              window.open(`/auto-pilot/task/${autoPilotData?.data?.scheduledAutoPilotId}?accountId=${autoPilotData?.accountId}`, '_blank');
            }
          : addScheduleAutoPilot,
        isDisabled: !autoPilotData?.data?.scheduledAutoPilotId && autoPilotData?.data?.continuousAutoPilotId,
        betaIcon: autoPilotData?.data?.scheduledAutoPilotId ? <SafeIcon src={ExternalLinkIcon} alt='Open in new tab' width={12} height={12} /> : null,
      },
      {
        label: 'Deploy Fix',
        backgroundColor: 'transparent',
        onClick: submitRecommendation,
      },
    ];
  };

  const ActionButtons = ({ buttons, activeButton, setActiveButton }) => {
    const cancelIndex = buttons.findIndex((button) => button.label === 'Cancel');

    const leftButtons = buttons.slice(0, cancelIndex + 1);
    const rightButtons = buttons.slice(cancelIndex + 1);

    return (
      <Box
        sx={{
          display: 'flex',
          height: '56px',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
          flexShrink: 0,
          paddingX: '10px',
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center' }}>
          {leftButtons.map((button) => (
            <CustomButton
              key={button.label}
              sx={{
                background: button.label === activeButton ? colors.background.primaryDark : '',
                color: button.label === activeButton ? colors.text.white : 'inherit',
                textTransform: 'none',
                fontWeight: button.label === activeButton ? 'bold' : 'normal',
                width: '100px',
                '& img, & svg': {
                  filter: 'none !important',
                },
              }}
              variant={'secondary'}
              size='Medium'
              onClick={() => {
                setActiveButton(button.label);
                button.onClick();
              }}
              text={button.label}
              endIcon={button.betaIcon}
            />
          ))}
        </Box>

        <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', alignItems: 'center' }}>
          {rightButtons.map((button) => (
            <CustomButton
              key={button.label}
              sx={{
                background: button.label === activeButton ? colors.background.primaryDark : '',
                color: button.label === activeButton ? colors.text.white : 'inherit',
                fontWeight: 'inherit',
                '& img, & svg': {
                  filter: 'none !important',
                },
              }}
              // variant={button.label === activeButton ? 'contained' : 'outlined'}
              variant='secondary'
              size='Medium'
              onClick={() => {
                setActiveButton(button.label);
                button.onClick();
              }}
              disabled={button.isDisabled}
              text={button.label}
              endIcon={button.betaIcon}
            />
          ))}
        </Box>
      </Box>
    );
  };

  ActionButtons.propTypes = {
    buttons: PropTypes.arrayOf(
      PropTypes.shape({
        label: PropTypes.string.isRequired,
        backgroundColor: PropTypes.string.isRequired,
        onClick: PropTypes.func.isRequired,
        isDisabled: PropTypes.bool,
      })
    ).isRequired,
    activeButton: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
    setActiveButton: PropTypes.func.isRequired,
  };

  const updateDataBasedOnButtonValueForCpu = (value, containerName) => {
    let selectedKey = algo;
    selectedKey = selectedKey?.toLowerCase();

    // Helper to calculate limit based on cpuLimit selection and the new request
    const getCpuLimit = (newRequest) => {
      switch (selectedButtons.cpuLimit) {
        case 1: // KEEP_PREVIOUS
          return allocatedData[containerName]?.cpu?.limit || null;
        case 2: // PLUS_5
          return (newRequest * 1.05).toFixed(2);
        case 3: // PLUS_15
          return (newRequest * 1.15).toFixed(2);
        default: // NO_LIMIT (0 or undefined)
          return null;
      }
    };

    switch (value) {
      case 'NBALGO': {
        const newRequest = parseFloat(additionalCpuInfo[containerName].nbalgo) || 0;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(4),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      case 'P99': {
        const newRequest = parseFloat(additionalCpuInfo[containerName].p99) || 0;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(4),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      case 'P97': {
        const newRequest = parseFloat(additionalCpuInfo[containerName].p97) || 0;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(4),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      case 'P95': {
        const newRequest = parseFloat(additionalCpuInfo[containerName].p95) || 0;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(4),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      case 5: {
        const newRequest = (parseFloat(additionalCpuInfo[containerName][selectedKey]) || 0) * 1.05;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(4),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      case 10: {
        const newRequest = (parseFloat(additionalCpuInfo[containerName][selectedKey]) || 0) * 1.1;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(4),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      case 15: {
        const newRequest = (parseFloat(additionalCpuInfo[containerName][selectedKey]) || 0) * 1.15;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            cpu: {
              ...prev[containerName].cpu,
              request: newRequest.toFixed(2),
              limit: getCpuLimit(newRequest),
            },
          },
        }));
        break;
      }
      default:
        break;
    }
  };

  const updateDataBasedOnButtonValueForMemory = (value, containerName) => {
    // Helper to calculate limit based on memLimit selection
    // newRequestBytes: the new request value in bytes (before formatting)
    const getMemoryLimit = (newRequestBytes) => {
      switch (selectedButtons.memLimit) {
        case 1: // KEEP_PREVIOUS
          return allocatedData[containerName]?.memory?.limit || null;
        case 2: // PLUS_5 of request
          return formatMemory(newRequestBytes * 1.05, 'bytes', 'mb', false);
        case 3: // PLUS_15 of request
          return formatMemory(newRequestBytes * 1.15, 'bytes', 'mb', false);
        default: // RECOMMENDED (0 or undefined) - limit equals request
          return formatMemory(newRequestBytes, 'bytes', 'mb', false);
      }
    };

    switch (value) {
      case 0: {
        const newRequestBytes = additionalMemInfo[containerName].nbalgoReq;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            memory: {
              ...prev[containerName].memory,
              request: formatMemory(newRequestBytes, 'bytes', 'mb', false),
              limit: getMemoryLimit(newRequestBytes),
            },
          },
        }));
        break;
      }
      case 5: {
        const newRequestBytes = additionalMemInfo[containerName].nbalgoReq * 1.05;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            memory: {
              ...prev[containerName].memory,
              request: formatMemory(newRequestBytes, 'bytes', 'mb', false),
              limit: getMemoryLimit(newRequestBytes),
            },
          },
        }));
        break;
      }
      case 10: {
        const newRequestBytes = additionalMemInfo[containerName].nbalgoReq * 1.1;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            memory: {
              ...prev[containerName].memory,
              request: formatMemory(newRequestBytes, 'bytes', 'mb', false),
              limit: getMemoryLimit(newRequestBytes),
            },
          },
        }));
        break;
      }
      case 15: {
        const newRequestBytes = additionalMemInfo[containerName].nbalgoReq * 1.15;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            memory: {
              ...prev[containerName].memory,
              request: formatMemory(newRequestBytes, 'bytes', 'mb', false),
              limit: getMemoryLimit(newRequestBytes),
            },
          },
        }));
        break;
      }
      case 20: {
        const newRequestBytes = additionalMemInfo[containerName].nbalgoReq * 1.2;
        setUpdatedData((prev) => ({
          ...prev,
          [containerName]: {
            ...prev[containerName],
            memory: {
              ...prev[containerName].memory,
              request: formatMemory(newRequestBytes, 'bytes', 'mb', false),
              limit: getMemoryLimit(newRequestBytes),
            },
          },
        }));
        break;
      }
      default:
        break;
    }
  };

  const handleSelectedAlgo = (buttonId, buttonValue, containerName) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      algo: buttonId,
    }));
    setAlgo(buttonValue);
    updateDataBasedOnButtonValueForCpu(buttonValue, containerName);
  };

  const handleSelectedBuffer = (buttonId, buttonValue, containerName) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      buffer: buttonId,
    }));
    updateDataBasedOnButtonValueForCpu(buttonValue, containerName);
  };

  const handleSelectedMemoryBuffer = (buttonId, buttonValue, containerName) => {
    setSelectedButtons(buttonId);
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memBuffer: buttonId,
    }));
    updateDataBasedOnButtonValueForMemory(buttonValue, containerName);
  };

  const handleSelectedMemoryAlgo = (buttonId, buttonValue, containerName) => {
    setSelectedButtons(buttonId);
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memory: buttonId,
    }));
    updateDataBasedOnButtonValueForMemory(buttonValue, containerName);
  };

  const handleSelectedCpuLimit = (buttonId, buttonValue, containerName) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      cpuLimit: buttonId,
    }));
    const requestStr = String(updatedData[containerName]?.cpu?.request || '0').replace(/,/g, '');
    const currentRequest = parseFloat(requestStr) || 0;
    let newLimit = null;
    if (buttonValue === 'KEEP_PREVIOUS') {
      newLimit = allocatedData[containerName]?.cpu?.limit || null;
    } else if (buttonValue === 'PLUS_5') {
      newLimit = (currentRequest * 1.05).toFixed(2);
    } else if (buttonValue === 'PLUS_15') {
      newLimit = (currentRequest * 1.15).toFixed(2);
    }
    // For 'NO_LIMIT', newLimit stays null
    setUpdatedData((prev) => ({
      ...prev,
      [containerName]: {
        ...prev[containerName],
        cpu: {
          ...prev[containerName].cpu,
          limit: newLimit,
        },
      },
    }));
  };

  const handleSelectedMemLimit = (buttonId, buttonValue, containerName) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memLimit: buttonId,
    }));
    const requestStr = String(updatedData[containerName]?.memory?.request || '0').replace(/,/g, '');
    const currentRequest = parseFloat(requestStr) || 0;
    let newLimit = null;
    if (buttonValue === 'KEEP_PREVIOUS') {
      newLimit = allocatedData[containerName]?.memory?.limit || null;
    } else if (buttonValue === 'PLUS_5') {
      newLimit = Math.round(currentRequest * 1.05);
    } else if (buttonValue === 'PLUS_15') {
      newLimit = Math.round(currentRequest * 1.15);
    } else {
      // 'RECOMMENDED' - limit equals request
      newLimit = Math.round(currentRequest);
    }
    setUpdatedData((prev) => ({
      ...prev,
      [containerName]: {
        ...prev[containerName],
        memory: {
          ...prev[containerName].memory,
          limit: newLimit,
        },
      },
    }));
  };

  const shouldShowKeepPreviousCpuLimit = (containerName) => {
    const allocatedLimit = allocatedData[containerName]?.cpu?.limit;
    const recommendedRequest = updatedData[containerName]?.cpu?.request;
    return (
      allocatedLimit != null &&
      parseFloat(allocatedLimit) > 0 &&
      recommendedRequest != null &&
      parseFloat(recommendedRequest) < parseFloat(allocatedLimit)
    );
  };

  const shouldShowKeepPreviousMemLimit = (containerName) => {
    const allocatedLimit = allocatedData[containerName]?.memory?.limit;
    const recommendedRequestStr = String(updatedData[containerName]?.memory?.request || '0').replace(/,/g, '');
    const recommendedRequest = parseFloat(recommendedRequestStr) || 0;
    const allocatedLimitStr = String(allocatedLimit || '0').replace(/,/g, '');
    const allocatedLimitNum = parseFloat(allocatedLimitStr) || 0;
    // Only show "Keep Previous" if previous limit >= recommended request (limit must be >= request in K8s)
    return allocatedLimitNum > 0 && allocatedLimitNum >= recommendedRequest;
  };

  const handleInputChange = (value, type, type1, containerName) => {
    if (type == 'cpu' && type1 == 'request') {
      setUpdatedData((prevData) => ({
        ...prevData,
        [containerName]: {
          ...prevData[containerName],
          cpu: {
            ...prevData[containerName].cpu,
            request: value,
          },
        },
      }));
    } else if (type == 'cpu' && type1 == 'limit') {
      setUpdatedData((prevData) => ({
        ...prevData,
        [containerName]: {
          ...prevData[containerName],
          cpu: {
            ...prevData[containerName].cpu,
            limit: value,
          },
        },
      }));
    } else if (type == 'mem' && type1 == 'request') {
      setUpdatedData((prevData) => ({
        ...prevData,
        [containerName]: {
          ...prevData[containerName],
          memory: {
            ...prevData[containerName].memory,
            request: value,
          },
        },
      }));
    } else if (type == 'mem' && type1 == 'limit') {
      setUpdatedData((prevData) => ({
        ...prevData,
        [containerName]: {
          ...prevData[containerName],
          memory: {
            ...prevData[containerName].memory,
            limit: value,
          },
        },
      }));
    }
  };

  const handleTicketSuccess = () => {
    listRecommendations();
  };

  const handleGithubPRSuccessOrFail = (message, severity) => {
    if (severity === 'success') {
      snackbar.success(message);
    } else {
      snackbar.error(message);
    }
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const closeCreatePRModal = () => {
    setOpenCreatePR(false);
    setSelectedGitIntegration('');
    setAllGitIntegrations([]);
    setActiveButton('');
    setSelectedWorkloadAnnotations({});
  };

  const handleCreatePR = () => {
    if (!selectedGitIntegration) return;
    // Extract type and name from key (format: "github:name" or "gitlab:name")
    const [integrationType, ...nameParts] = selectedGitIntegration.split(':');
    const integrationName = nameParts.join(':'); // Handle names with colons
    setPRLoading(true);
    const data = JSON.parse(JSON.stringify(updatedData));
    for (const d in data) {
      if (data[d].memory) {
        for (const key in data[d].memory) {
          let value = data[d].memory[key];
          if (value) {
            data[d].memory[key] = value + 'Mi';
          }
        }
      }
    }
    recommendationApi
      .applyRecommendation(autoPilotData.accountId, autoPilotData.recommendationId, data, integrationType, {
        name: integrationName,
      })
      .then((res) => {
        if (res?.errors?.length > 0) {
          handleGithubPRSuccessOrFail('Failed to create Pull request', 'error');
        } else if (res?.data?.length > 0) {
          snackbar.success(
            'PR creation initiated successfully! The code agent is creating the PR in the background. Check the "Recommendation Resolution" tab below to track progress.',
            6000
          );
        }
        closeCreatePRModal();
        setPRLoading(false);
      })
      .catch((error) => {
        handleGithubPRSuccessOrFail('failed to raise pull request', 'error');
        console.error(error);
        closeCreatePRModal();
        setPRLoading(false);
      });
  };

  const statusValues = useMemo(
    () => (recommendationStatus.length > 0 ? recommendationStatus.map((s) => s.value) : undefined),
    [recommendationStatus]
  );

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'pod_right_sizing',
    namespace: selectedNamespace,
    workloadType: selectedWorkloadType,
    workloadName: selectedWorkloadName,
    status: statusValues,
  });

  return (
    <>
      <Modal width='md' open={openCreatePR} handleClose={closeCreatePRModal} title={'Create Pull Request'}>
        {prLoading && (
          <Box sx={{ position: 'absolute', top: 0, left: 0, right: 0, zIndex: 9999 }}>
            <LinearLoader />
          </Box>
        )}
        {isGitReposLoading || filteredGitIntegrations.length > 0 ? (
          <>
            {selectedWorkloadAnnotations && Object.keys(selectedWorkloadAnnotations).length > 0 ? (
              <>
                <Grid container xs={12} gap={3}>
                  <CustomDropdown
                    label={'Git Integration'}
                    value={filteredGitIntegrations.find((i) => i.key === selectedGitIntegration)?.label || ''}
                    minWidth={'175px'}
                    options={filteredGitIntegrations.map((i) => i.label)}
                    onChange={(e) => {
                      const selected = filteredGitIntegrations.find((i) => i.label === e.target.value);
                      if (selected) setSelectedGitIntegration(selected.key);
                    }}
                    showNormalField={true}
                    isLoading={isGitReposLoading}
                    disableClearable
                  />
                </Grid>
                <Typography sx={{ mt: 2, mb: 1, color: 'success.main', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  ✓ Source configuration detected
                </Typography>
                <ul>
                  {Object.entries(selectedWorkloadAnnotations).map(([key, value]) => (
                    <li key={key}>
                      <strong>{key}:</strong> {value}
                    </li>
                  ))}
                </ul>
                <Typography variant='body2' sx={{ mt: 1, color: 'text.secondary' }}>
                  The system will automatically detect the repository and values files to create the pull request.
                </Typography>
              </>
            ) : (
              <>
                <Typography sx={{ color: 'warning.main', mb: 1 }}>⚠ No source configuration detected</Typography>
                <Typography variant='body2' sx={{ mb: 2 }}>
                  To enable pull request creation, configure one of the following on your workload:
                </Typography>
                <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 1 }}>
                  Option 1: Nudgebee Annotations
                </Typography>
                <ul>
                  <li>
                    <strong>{ANNOTATIONS.CI_GIT_REPO}</strong> - Git repository URL
                  </li>
                  <li>
                    <strong>{ANNOTATIONS.CI_GIT_HASH}</strong> - Commit hash (optional)
                  </li>
                  <li>
                    <strong>{ANNOTATIONS.CI_HELM_VALUES_PATH}</strong> - Path to Helm values file (optional)
                  </li>
                </ul>
                <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 1, mt: 2 }}>
                  Option 2: ArgoCD Deployment
                </Typography>
                <ul>
                  <li>
                    <strong>argocd.argoproj.io/tracking-id</strong> - ArgoCD tracking annotation (automatically added by ArgoCD)
                  </li>
                </ul>
                <Typography variant='body2' sx={{ mt: 2, fontStyle: 'italic', color: 'text.secondary' }}>
                  If using ArgoCD, ensure your application uses multi-source configuration with a values repository.
                </Typography>
              </>
            )}
            <Grid
              container
              sx={{
                justifyContent: 'end',
                mb: 3,
                mt: 2,
                button: {
                  minWidth: '140px',
                },
              }}
              gap={1}
            >
              <Grid item>
                <CustomButton text='Cancel' variant='secondary' size='Medium' onClick={closeCreatePRModal} />
              </Grid>
              <Grid item>
                <CustomButton
                  disabled={!selectedGitIntegration || !Object.keys(selectedWorkloadAnnotations).length || prLoading}
                  text='Save'
                  onClick={handleCreatePR}
                  size='Medium'
                />
              </Grid>
            </Grid>
          </>
        ) : (
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              textAlign: 'center',
              py: 4,
              px: 3,
            }}
          >
            <Box
              sx={{
                width: 80,
                height: 80,
                borderRadius: '50%',
                backgroundColor: colors.background.totalRecomendation,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                mb: 3,
              }}
            >
              <SafeIcon src={GithubIcon} height={40} width={40} alt='Git' />
            </Box>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              No Git Integration Configured
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: colors.text.tertiary,
                mb: 3,
                maxWidth: '320px',
              }}
            >
              Connect a GitHub or GitLab repository to enable pull request creation for your recommendations.
            </Typography>
            <CustomButton
              text='Configure Git Integration'
              variant='primary'
              size='Medium'
              onClick={() => router.push('/accounts/account-form?cloudProvider=GITHUB')}
              sx={{ minWidth: '220px' }}
            />
          </Box>
        )}
      </Modal>
      <Modal
        width='lg'
        open={isResolvedFormOpen}
        handleClose={closeResolvedFormModal}
        title={'Resolve this issue'}
        actionButtons={<ActionButtons buttons={getButtons()} activeButton={activeButton} setActiveButton={setActiveButton} />}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: '1010px',
            '& .MuiDialogContent-root': {
              padding: 'var(--ds-space-4) var(--ds-space-6)',
            },
          },
        }}
      >
        <Box sx={{ pb: 'var(--ds-space-6)' }}>
          <AutoPilotHeaderCard header='' data={autoPilotData} />
          {Object.keys(updatedData).length > 0
            ? Object.keys(updatedData).map((e) => (
                <Box key={e} sx={{ display: 'flex', gap: 'var(--ds-space-4)', marginTop: 'var(--ds-space-4)' }}>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
                    <Typography>Container Name- {e}</Typography>
                    <AutoOptimizeForm
                      handleSelectedAlgo={(buttonId, buttonValue) => handleSelectedAlgo(buttonId, buttonValue, e)}
                      handleSelectedBuffer={(buttonId, buttonValue) => handleSelectedBuffer(buttonId, buttonValue, e)}
                      handleSelectedMemoryBuffer={(buttonId, buttonValue) => handleSelectedMemoryBuffer(buttonId, buttonValue, e)}
                      handleSelectedMemoryAlgo={(buttonId, buttonValue) => handleSelectedMemoryAlgo(buttonId, buttonValue, e)}
                      handleSelectedCpuLimit={(buttonId, buttonValue) => handleSelectedCpuLimit(buttonId, buttonValue, e)}
                      handleSelectedMemLimit={(buttonId, buttonValue) => handleSelectedMemLimit(buttonId, buttonValue, e)}
                      data={updatedData[e]}
                      currentData={allocatedData[e]}
                      activeButton={selectedButtons}
                      additionalInfoCPUAndMem={{ cpuInfo: additionalCpuInfo[e], memInfo: additionalMemInfo[e] }}
                      handleInputChange={handleInputChange}
                      containerName={e}
                      showKeepPreviousCpuLimit={shouldShowKeepPreviousCpuLimit(e)}
                      showKeepPreviousMemLimit={shouldShowKeepPreviousMemLimit(e)}
                    />
                  </Box>
                </Box>
              ))
            : null}
        </Box>
      </Modal>
      <Modal
        width='lg'
        loader={isLoading}
        open={isAutoPilotScheduledFormOpen}
        handleClose={() => closeAutoPilotScheduledConfigModal(false)}
        title={'Scheduled Auto Optimize Configuration'}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: '1010px',
            '& .MuiDialogContent-root': {
              padding: 'var(--ds-space-4) var(--ds-space-6)',
            },
          },
        }}
      >
        <AutoOptimizeVerticalRightSizingScheduledConfiguration
          autoOptimizeData={autoPilotData}
          closeAutoPilotSingleConfigModal={closeAutoPilotScheduledConfigModal}
          msTeamsData={msTeamsData}
          googleChannelList={googleChannelList}
          isMsTeamsLoading={isMsTeamsLoading}
          isGoogleChannelsLoading={isGoogleChannelsLoading}
          data={updatedData}
          currentData={allocatedData}
          additionalInfoCPUAndMem={{ cpuInfo: additionalCpuInfo, memInfo: additionalMemInfo }}
          isLoading={isLoading}
          setIsLoading={setIsLoading}
        />
      </Modal>
      <Modal
        width='lg'
        loader={isLoading}
        open={isAutoPilotContinuousFormOpen}
        handleClose={() => closeAutoPilotContinuousConfigModal(false)}
        title={'Continuous Auto Optimize Configuration'}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: '1010px',
            '& .MuiDialogContent-root': {
              padding: 'var(--ds-space-4) var(--ds-space-6)',
            },
          },
        }}
      >
        <AutoOptimizeContinuousVerticalRightSizingSingleConfiguration
          autoOptimizeData={autoPilotData}
          closeAutoPilotSingleConfigModal={closeAutoPilotContinuousConfigModal}
          msTeamsData={msTeamsData}
          googleChannelList={googleChannelList}
          isMsTeamsLoading={isMsTeamsLoading}
          isGoogleChannelsLoading={isGoogleChannelsLoading}
          data={updatedData}
          currentData={allocatedData}
          additionalInfoCPUAndMem={{ cpuInfo: additionalCpuInfo, memInfo: additionalMemInfo }}
          isLoading={isLoading}
          setIsLoading={setIsLoading}
        />
      </Modal>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Update Resource - ' + ticketData?.cloud_resourse?.name,
          description: getTicketDescription(),
          accountId: ticketData?.account_id || selectedAccountId,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      {enabledSummary && (
        <Box
          sx={{
            display: 'flex',
            flex: 1,
            flexDirection: 'row',
            gap: ds.space[3],
            '& > *': { maxWidth: `calc((100% - 3 * ${ds.space[3]}) / 4)` },
          }}
          mt={2}
          mb={2}
        >
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Total Recommendations'
              info={{ tooltip: 'Active right-sizing recommendations across all workloads' }}
              value={Number.isFinite(kubernetesFixedCount) ? kubernetesFixedCount.toLocaleString() : kubernetesFixedCount ?? '—'}
            />
          </WidgetCard>

          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Total Cost'
              info={{ tooltip: 'Current monthly spend across the workloads listed below' }}
              value={
                kubernetesTotalCost === '-' || kubernetesTotalCost == null ? (
                  '—'
                ) : (
                  <CostCallout size='lg' tone='neutral' value={Number(kubernetesTotalCost) || 0} period='/ mo' />
                )
              }
            />
          </WidgetCard>

          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Savings Potential'
              info={{ tooltip: 'Estimated yearly savings if every recommendation is applied' }}
              value={<CostCallout size='lg' tone='high-savings' value={(Number(kubernetesRightSizingEstimatedSaving) || 0) * 12} period='/ yr' />}
            />
          </WidgetCard>

          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Expense Recommendations'
              info={{
                tooltip:
                  'Recommendations that suggest allocating MORE resources to underprovisioned workloads. Applying them improves reliability but increases cost.',
              }}
              value={Number.isFinite(underProvisionedResources) ? underProvisionedResources.toLocaleString() : underProvisionedResources ?? '—'}
            />
          </WidgetCard>
        </Box>
      )}
      <KubernetesRightSizingUpdateForm
        open={openKubernetesRightSizingUpdateForm}
        onClose={closeKubernetesRightSizingUpdateForm}
        onSuccess={closeKubernetesRightSizingUpdateForm}
        onFailure={closeKubernetesRightSizingUpdateForm}
        data={{}}
      />

      <ListingLayout id='right-sizing'>
        <ListingLayout.Toolbar
          title={props.heading === undefined ? 'Right Sizing' : props.heading || undefined}
          data-testid='rs-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={selectedAccountId} jobName='krr_scan' idPrefix='rs' />}
              {selectedAccountId && (
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    { id: 'export-csv', label: 'Download CSV', onSelect: () => handleExportDownload('csv') },
                    { id: 'export-xlsx', label: 'Download Excel (XLSX)', onSelect: () => handleExportDownload('xlsx') },
                  ]}
                  trigger={
                    <DsButton
                      tone='secondary'
                      size='sm'
                      composition='icon-only'
                      icon={<FileDownloadOutlinedIcon />}
                      aria-label='Download'
                      id='rs-download'
                    />
                  }
                />
              )}
            </>
          }
        >
          {enabledFilters && (
            <>
              {isOptimisePage && (
                <FilterDropdown
                  id='rs-filter-account'
                  label='Account'
                  options={accounts.map((acc) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value }))}
                  value={
                    accounts
                      .map((acc) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value }))
                      .find((o) => o.value === selectedAccountId) ?? null
                  }
                  onSelect={(_e, item) => onAccountFilterChange({ target: { value: item?.value || '' } })}
                />
              )}
              <FilterDropdown
                id='rs-filter-namespace'
                label='Namespace'
                options={(namespaceFilter || []).map((n) => ({ label: n, value: n }))}
                value={selectedNamespace ? { label: selectedNamespace, value: selectedNamespace } : null}
                onSelect={(_e, item) => onNamespaceFilterChange({ target: { value: item?.value || '' } })}
              />
              <FilterDropdown
                id='rs-filter-workload'
                label='Workload'
                options={(workloadNameFilter || []).map((n) => ({ label: n, value: n }))}
                value={selectedWorkloadName ? { label: selectedWorkloadName, value: selectedWorkloadName } : null}
                onSelect={(_e, item) => onWorkloadNameFilterChange({ target: { value: item?.value || '' } })}
              />
              <FilterDropdown
                id='rs-filter-workload-type'
                label='Workload Type'
                options={(workloadTypeFilter || []).map((n) => ({ label: n, value: n }))}
                value={selectedWorkloadType ? { label: selectedWorkloadType, value: selectedWorkloadType } : null}
                onSelect={(_e, item) => onWorkloadTypeFilterChange({ target: { value: item?.value || '' } })}
              />
              <FilterDropdown
                id='rs-filter-status'
                label='Status'
                multiple
                options={RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }))}
                value={recommendationStatus.map((s) => ({
                  label: s.label || (s.value === 'InProgress' ? 'In Progress' : s.value),
                  value: s.value,
                }))}
                onSelect={(_e, items) => {
                  const next = (Array.isArray(items) ? items : []).map((it) => ({
                    label: it.label || (it.value === 'InProgress' ? 'In Progress' : it.value),
                    value: it.value,
                  }));
                  setRecommendationStatus(next);
                  setPage(0);
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={kubernetesRightSizingTable}
            headers={tableHeaders}
            tableData={kubernetesRightSizing}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesRightSizingCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            stickyColumnIndex='8'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'CPU & Memory Utilization',
                  componentFn: (_option, drilldownQuery) => (
                    <Box
                      sx={{
                        backgroundColor: colors.background.white,
                        padding: 'var(--ds-space-4) var(--ds-space-4)',
                        borderRadius: 'var(--ds-radius-lg)',
                        border: `1px solid ${colors.border.primaryLight}`,
                      }}
                    >
                      {Object.entries(drilldownQuery?.recommendation?.recommendation || {}).map(([containerName, entries]) => {
                        if (!Array.isArray(entries)) return null;
                        const cpu = entries.find((e) => e.resource === 'cpu') || {};
                        const mem = entries.find((e) => e.resource === 'memory') || {};
                        return (
                          <React.Fragment key={containerName}>
                            <Title title={'Container - ' + containerName} />
                            <KubernetesUtilization
                              account={drilldownQuery?.accountId || selectedAccountId}
                              namespaceName={drilldownQuery.namespaceName}
                              workloadName={drilldownQuery.workloadName}
                              containerName={containerName}
                              podName={drilldownQuery.podName}
                              recc={{
                                cpuRecc: cpu?.recommended?.request,
                                cpuRequest: cpu?.allocated?.request,
                                cpuLimit: cpu?.allocated?.limit,
                                memoryRecc: formatMemory(mem?.recommended?.request, 'bytes', 'mb', false),
                                memRequest: formatMemory(mem?.allocated?.request, 'bytes', 'mb', false),
                                memLimit: formatMemory(mem?.allocated?.limit, 'bytes', 'mb', false),
                              }}
                              datasource={'prometheus'}
                            />
                          </React.Fragment>
                        );
                      })}
                    </Box>
                  ),
                },
                {
                  text: 'Resolutions',
                  componentFn: (_option, drilldownQuery) => <RecommendationResolution recommendation={drilldownQuery.recommendation} />,
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={nubiAccountId}
        queryPrefix={nubiQuery}
        context={{ type: 'cluster', data: { conversationId: nubiConversationId } }}
        apiMode='investigate'
        categorySource='Optimize'
        position='right'
        mode='overlay'
      />
    </>
  );
};

// (RsResourceLine removed — replaced by the DS Comparison + ComparisonGroup primitives.)
KubernetesRightSizing.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
  namespaceName: PropTypes.string,
  workloadType: PropTypes.string,
  enabledFilters: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  accountObjectId: PropTypes.string,
  resourceIds: PropTypes.arrayOf(PropTypes.string),
  groupName: PropTypes.string,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesRightSizing;
