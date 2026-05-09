import SummaryWidget from '@components1/optimise/SummaryWidget';
import { Box, IconButton, Typography, Grid } from '@mui/material';
import React, { useEffect, useRef, useState, useMemo } from 'react';
import SyncIcon from '@mui/icons-material/Sync';
import { formatMemory } from '@lib/formatter';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Currency from '@components1/common/format/Currency';
import KubernetesRightSizingUpdateForm from '@components1/recommendations/KubernetesRightSizingUpdateForm';
import KubernetesUtilization from '@components1/k8s/KubernetesUtilization';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { Modal } from '@components1/common/modal';
import AutoOptimizeForm from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingForm';
import Datetime from '@components1/common/format/Datetime';
import { BetaIcon, DoubleArrowRight, ExternalLinkIcon, GithubIcon } from '@assets';
import PropTypes from 'prop-types';
import { snackbar } from '@components1/common/snackbarService';
import { hasWriteAccess } from '@lib/auth';
import CustomDropdown from '@components1/common/CustomDropdown';
import apiIntegrations from '@api1/integrations';
import CustomButton from '@components1/common/NewCustomButton';
import apiAccount from '@api1/account';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import { useData } from '@context/DataContext';
import Title from '@components1/common/Title';
import Text from '@components1/common/format/Text';
import AutoOptimizeVerticalRightSizingScheduledConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import ResolveButton from '@components1/common/ResolveButton';
import NumberComponent from '@components1/common/format/Number';
import AutoPilotHeaderCard from '@components1/autopilot/card/AutoOptimizeHeaderCard';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomPRLink from '@components1/common/CustomPRLink';
import { colors } from 'src/utils/colors';
import apiHome from '@api1/home';
import useRecommendationExport from '@hooks/useRecommendationExport';
import ButtonMenu from '@components1/common/ButtonMenu';
import CustomLink from '@components1/common/CustomLink';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import SafeIcon from '@components1/common/SafeIcon';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import { action } from 'src/utils/actionStyles';
import { syncFilterFromQuery } from '@utils/common';

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
  let jobName = 'krr_scan';

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
  const [refreshTime, setRefreshTime] = useState({});
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState(false);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState(false);
  const [ticketExists, setTicketExists] = useState(false);
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);
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
    const repoUrl = selectedWorkloadAnnotations['ci.nudgebee.com/git.repo'] || selectedWorkloadAnnotations['workloads.nudgebee.com/git.repo'];
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
    if (!jobName) {
      setRefreshTime({});
      return;
    }
    let job = {};
    for (let j of selectedCluster?.agent?.connection_status?.schedule_jobs ?? []) {
      if (j?.runnable_params?.action_func_name == jobName) {
        job = j;
        break;
      }
    }
    setRefreshTime(job);
  }, [jobName, selectedCluster]);

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
        const filteredKeys = Object.keys(annotations).filter((key) => key.startsWith('ci.nudgebee.com') || key.startsWith('argocd.argoproj.io'));
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
            if (attr.name.startsWith('ci.nudgebee.com')) {
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

  const calculatePercentage = (recommendedReq, allocatedReq) => {
    const epsilon = 1e-10;
    if (!isNaN(recommendedReq) && !isNaN(allocatedReq) && allocatedReq > epsilon) {
      return Math.abs(((allocatedReq - recommendedReq) / allocatedReq) * 100).toFixed() + '%';
    }
    return '-';
  };

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
                  <Box sx={{ display: 'flex', gap: '2px' }}>
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
          data.push({
            component: Object.entries(item.recommendation)
              .map(([key, value]) => {
                return [key, value?.filter((v) => v.resource === 'cpu')];
              })
              .map(([key, value]) => {
                return (
                  <React.Fragment key={key}>
                    <Text value={`${key}`} secondaryText sx={{ fontStyle: 'italic' }} />
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'baseline',
                        flexDirection: 'row',
                        gap: '13px',
                        img: {
                          filter:
                            'brightness(0) saturate(100%) invert(77%) sepia(22%) saturate(803%) hue-rotate(89deg) brightness(94%) contrast(92%)',
                        },
                        '@media (max-width: 1200px)': {
                          gap: '0px',
                        },
                      }}
                    >
                      <Box sx={{ width: '35px' }}>
                        <NumberComponent value={value[0].allocated.request ?? '-'} />
                      </Box>
                      {value[0].allocated.request !== value[0].recommended.request ? (
                        <Box
                          sx={{
                            width: '30px',
                            '@media (max-width: 1200px)': {
                              width: '28px',
                            },
                          }}
                        >
                          <SafeIcon src={DoubleArrowRight} height={10} width={20} alt='arrow right' />
                        </Box>
                      ) : (
                        <Typography sx={{ color: colors.text.secondaryDark, fontSize: '14px', width: '35px', fontWeight: 400 }}>===</Typography>
                      )}

                      <Typography
                        sx={{
                          color: value[0].allocated.request != value[0].recommended.request ? colors.text.lowest : colors.text.secondary,
                          fontWeight: value[0].allocated.request != value[0].recommended.request ? 500 : 400,
                          fontSize: '14px',
                          width: '35px',
                        }}
                      >
                        {value[0].recommended.request}
                      </Typography>
                      <Text
                        value={calculatePercentage(value[0].recommended.request, value[0].allocated.request)}
                        secondaryText
                        sx={{
                          '@media (max-width: 1200px)': {
                            pl: '5px',
                          },
                        }}
                      />
                    </Box>
                  </React.Fragment>
                );
              }),
          });
          data.push({
            component: Object.entries(item.recommendation)
              .map(([key, value]) => {
                return [key, value?.filter((v) => v.resource === 'memory')];
              })
              .map(([key, value]) => {
                return (
                  <React.Fragment key={key}>
                    <Text value={`${key}`} secondaryText sx={{ fontStyle: 'italic' }} />
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'baseline',
                        flexDirection: 'row',
                        gap: '13px',
                        img: {
                          filter:
                            'brightness(0) saturate(100%) invert(77%) sepia(22%) saturate(803%) hue-rotate(89deg) brightness(94%) contrast(92%)',
                        },
                        '@media (max-width: 1200px)': {
                          gap: '2px',
                        },
                      }}
                    >
                      <Text sx={{ width: '35px' }} value={formatMemory(value[0].allocated.request, 'bytes', 'mb', false)} />
                      {value[0].allocated.request != value[0].recommended.request ? (
                        <Box
                          sx={{
                            width: '30px',
                            '@media (max-width: 1200px)': {
                              gap: '0px',
                            },
                          }}
                        >
                          <SafeIcon src={DoubleArrowRight} height={10} width={20} alt='arrow right' />
                        </Box>
                      ) : (
                        <Typography sx={{ color: colors.text.secondaryDark, fontSize: '14px', width: '35px', fontWeight: 400 }}>==</Typography>
                      )}

                      <Typography
                        sx={{
                          color: value[0].allocated.request != value[0].recommended.request ? colors.text.lowest : colors.text.secondary,
                          fontWeight: value[0].allocated.request != value[0].recommended.request ? 500 : 400,
                          fontSize: '14px',
                          width: '35px',
                        }}
                      >
                        {formatMemory(value[0].recommended.request, 'bytes', 'mb', false)}
                      </Typography>
                      <Text value={calculatePercentage(value[0].recommended.request, value[0].allocated.request)} secondaryText />
                    </Box>
                  </React.Fragment>
                );
              }),
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
                display={'flex'}
                flexDirection={'row'}
                justifyContent={'flex-end'}
                alignItems={'center'}
                gap={'6px'}
                position={'sticky'}
                right={'130px'}
              >
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <IconButton
                    size='small'
                    data-testid={`k8s-rs-ask-nubi-${item.id}`}
                    onClick={(event) => {
                      event.stopPropagation();
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
                    sx={{ ...action.nubi }}
                  >
                    <SafeIcon src={getNubiIconUrl()} alt={`Ask ${assistantName}`} width={16} height={16} />
                  </IconButton>
                </CustomTooltip>
                {hasWriteAccess(item.account_id || selectedAccountId) && (
                  <ResolveButton
                    displayText
                    isResolvedConfigured={hasAutopilotConfigured}
                    onClick={(event) => {
                      event.stopPropagation();
                      handleResolved({
                        id: autoPilotId,
                        resourceId: item.cloud_resourse.id,
                        data: item,
                      });
                    }}
                  />
                )}
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
        betaIcon: <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '1px' }} />,
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
          <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '1px' }} />
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
          gap: '10px',
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

        <Box sx={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
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

  const triggerRecommendationJob = () => {
    setIsRefreshLoading(true);
    recommendationApi
      .createRecommendationJob(selectedAccountId, 'krr_scan')
      .then((_res) => {
        alert('Scan Triggered Successfully, Data will be updated in Sometime');
      })
      .finally(() => {
        setIsRefreshLoading(false);
      });
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
                <Typography sx={{ mt: 2, mb: 1, color: 'success.main', fontWeight: 500 }}>✓ Source configuration detected</Typography>
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
                <Typography variant='body2' sx={{ fontWeight: 600, mb: 1 }}>
                  Option 1: Nudgebee Annotations
                </Typography>
                <ul>
                  <li>
                    <strong>ci.nudgebee.com/git.repo</strong> - Git repository URL
                  </li>
                  <li>
                    <strong>ci.nudgebee.com/git.hash</strong> - Commit hash (optional)
                  </li>
                  <li>
                    <strong>ci.nudgebee.com/helm.values.filePath</strong> - Path to Helm values file (optional)
                  </li>
                </ul>
                <Typography variant='body2' sx={{ fontWeight: 600, mb: 1, mt: 2 }}>
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
                fontSize: '18px',
                fontWeight: 600,
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              No Git Integration Configured
            </Typography>
            <Typography
              sx={{
                fontSize: '14px',
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
              padding: '16px 40px',
            },
          },
        }}
      >
        <Box sx={{ pb: '30px' }}>
          <AutoPilotHeaderCard header='' data={autoPilotData} />
          {Object.keys(updatedData).length > 0
            ? Object.keys(updatedData).map((e) => (
                <Box key={e} sx={{ display: 'flex', gap: '16px', marginTop: '16px' }}>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
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
              padding: '16px 40px',
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
              padding: '16px 40px',
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
            gap: '12px',
          }}
          mt={2}
          mb={2}
        >
          <SummaryWidget title='Total Recommendations' value={kubernetesFixedCount} />
          <SummaryWidget
            title='Total Cost'
            value={
              <Currency
                value={kubernetesTotalCost}
                sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
                withTooltip={false}
              />
            }
          />
          <SummaryWidget
            title='Savings Potential'
            variant='savings'
            value={
              <Currency
                value={kubernetesRightSizingEstimatedSaving * 12}
                sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
                withTooltip={false}
                suffix='/yr'
                isSavingPotential={true}
                recommendationLabel='Some of rightsizing recommendations'
              />
            }
          />
          <SummaryWidget
            title='Expense Recommendations'
            value={underProvisionedResources}
            showInfoIcon
            tooltipContent='These recommendations suggest allocating more resources to underprovisioned resources/services. Implementing them will increase costs but improve performance and reliability.'
          />
        </Box>
      )}
      <BoxLayout2
        heading={props.heading === undefined ? 'Right Sizing' : props.heading}
        id='right-sizing'
        filterOptions={
          enabledFilters
            ? [
                ...(isOptimisePage
                  ? [
                      {
                        type: 'dropdown',
                        enabled: true,
                        options: accounts.map((acc) => ({
                          label: acc.label || acc.account_name,
                          value: acc.id || acc.value,
                        })),
                        onSelect: onAccountFilterChange,
                        label: 'Account',
                        value: selectedAccountId,
                      },
                    ]
                  : []),
                {
                  type: 'dropdown',
                  enabled: true,
                  options: namespaceFilter,
                  onSelect: onNamespaceFilterChange,
                  value: selectedNamespace,
                  minWidth: '150px',
                  label: 'Namespace',
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: workloadNameFilter,
                  onSelect: onWorkloadNameFilterChange,
                  minWidth: '150px',
                  label: 'Workload',
                  value: selectedWorkloadName,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: workloadTypeFilter,
                  onSelect: onWorkloadTypeFilterChange,
                  minWidth: '150px',
                  label: 'Workload Type',
                  value: selectedWorkloadType,
                },
                {
                  type: 'multi-dropdown',
                  label: 'Status',
                  options: RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s })),
                  value: recommendationStatus,
                  onSelect: function (e) {
                    setRecommendationStatus(e?.target?.value ?? []);
                    setPage(0);
                  },
                },
              ]
            : []
        }
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: kubernetesRightSizingTable,
              };
            },
          },
          sharing: { enabled: true },
        }}
        extraOptions={[
          selectedAccountId && (
            <ButtonMenu
              key='export-menu'
              title='Download'
              size='medium'
              variant='tertiary'
              items={[
                {
                  text: 'Download CSV',
                  onClick: () => handleExportDownload('csv'),
                },
                {
                  text: 'Download Excel (XLSX)',
                  onClick: () => handleExportDownload('xlsx'),
                },
              ]}
            />
          ),
          !isOptimisePage && (
            <CustomButton
              disabled={!hasWriteAccess(selectedAccountId)}
              showTooltip={true}
              className='custom-button-icon'
              toolTipTitle={
                <Box>
                  <Typography sx={{ color: colors.text.lastSync, fontSize: '10px', fontWeight: 400 }}>Last Sync</Typography>
                  <Datetime
                    value={refreshTime?.state?.last_exec_time_sec ? new Date(refreshTime?.state?.last_exec_time_sec * 1000) : '-'}
                    sx={{ color: 'white', fontWeight: 600 }}
                    sxSuffix={{ color: 'white', fontWeight: 600 }}
                    showTooltip={false}
                  />
                </Box>
              }
              variant='secondary'
              key='triggerRecommendation'
              id='triggerRecommendation'
              onClick={triggerRecommendationJob}
              text={
                <Datetime
                  value={new Date(refreshTime?.state?.last_exec_time_sec * 1000)}
                  sx={{
                    color: colors.text.secondaryDark,
                    '& .MuiButton-icon': {
                      backgroundColor: colors.background.red,
                    },
                    marginLeft: '8px',
                  }}
                  showTooltip={false}
                />
              }
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
                height: '34px',
                '& .MuiButton-startIcon': {
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  margin: 0,
                },
              }}
            />
          ),
        ]}
      >
        <KubernetesRightSizingUpdateForm
          open={openKubernetesRightSizingUpdateForm}
          onClose={closeKubernetesRightSizingUpdateForm}
          onSuccess={closeKubernetesRightSizingUpdateForm}
          onFailure={closeKubernetesRightSizingUpdateForm}
          data={{}}
        />
        <KubernetesTable2
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          id={kubernetesRightSizingTable}
          headers={tableHeaders}
          data={kubernetesRightSizing}
          stickyColumnIndex={'8'}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={kubernetesRightSizingCount}
          expandable={{
            tabs: [
              {
                componentFn: function (_accountId, drilldownQuery) {
                  function getResourceData(recommendationData, _key) {
                    let r = {
                      cpuRequest: undefined,
                      cpuLimit: undefined,
                      cpuRecc: undefined,
                      memoryRequest: undefined,
                      memoryLimit: undefined,
                      memoryRecc: undefined,
                    };

                    for (let recommendation of recommendationData) {
                      if (recommendation.resource === 'cpu') {
                        r.cpuRequest = recommendation.allocated?.request;
                        r.cpuLimit = recommendation.allocated?.limit;
                        r.cpuRecc = recommendation.recommended?.request;
                      } else if (recommendation.resource === 'memory') {
                        r.memoryRequest = recommendation.allocated?.request;
                        r.memoryLimit = recommendation.allocated?.limit;
                        r.memoryRecc = recommendation.recommended?.request;
                      }
                    }
                    return r;
                  }

                  return Object.entries(drilldownQuery?.recommendation?.recommendation).map(([key, value]) => {
                    let getResourceDataValue = getResourceData(value, key);
                    return (
                      <React.Fragment key={key}>
                        <Title title={'Container - ' + key} />
                        <KubernetesUtilization
                          account={drilldownQuery?.accountId || selectedAccountId}
                          namespaceName={drilldownQuery.namespaceName}
                          workloadName={drilldownQuery.workloadName}
                          containerName={key}
                          podName={drilldownQuery.podName}
                          recc={{
                            cpuRecc: getResourceDataValue.cpuRecc,
                            cpuRequest: getResourceDataValue.cpuRequest,
                            cpuLimit: getResourceDataValue.cpuLimit,
                            memoryRecc: formatMemory(getResourceDataValue?.memoryRecc, 'bytes', 'mb', false),
                            memRequest: formatMemory(getResourceDataValue.memoryRequest, 'bytes', 'mb', false),
                            memLimit: formatMemory(getResourceDataValue.memoryLimit, 'bytes', 'mb', false),
                          }}
                          datasource={'prometheus'}
                        />
                      </React.Fragment>
                    );
                  });
                },
                text: 'CPU & Memory Utilization',
              },
              {
                componentFn: RecommendationResolutionFn,
                text: 'Resolutions',
              },
            ],
          }}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
        />
      </BoxLayout2>

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

const RecommendationResolutionFn = (_accountId, drilldownQuery) => {
  return <RecommendationResolution recommendation={drilldownQuery.recommendation} />;
};
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
