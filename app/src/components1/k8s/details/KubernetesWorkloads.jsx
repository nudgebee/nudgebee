import React, { useState, useEffect, useRef } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import KubernetesTable2, { KubernetesReplicaTrend } from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import Datetime from '@components1/common/format/Datetime';
import Currency from '@components1/common/format/Currency';
import Memory from '@components1/common/format/Memory';
import { getLast30Days, getSpecificTime, getYesterday } from '@lib/datetime';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import AutoPilotSettingIcon from '@assets/application/auto-pilot-new.svg';
import { Modal } from '@components1/ds/Modal';
import { Button as DsButton } from '@components1/ds/Button';
import { colors } from 'src/utils/colors';
import ReloadIcon from '@assets/application/restart-new.svg';
import ScaleIcon from '@assets/application/scale-new.svg';
import KubernetesScaleUpdateForm from '@components1/recommendations/KubernetesScaleUpdateForm';
import ReactLink from 'next/link';
import { DeleteIconRed as DeleteIcon } from '@assets';
import { Typography, Box, Grid, Divider, FormControlLabel, Switch, CircularProgress, IconButton } from '@mui/material';
import { Input } from '@components1/ds/Input';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import { hasWriteAccess } from '@lib/auth';
import LogFileIcon from '@assets/application/logs-new.svg';
import EditFileIcon from '@assets/application/edit-new.svg';
import { useRouter } from 'next/router';
import GitSvg from '@assets/application/github-new.svg';
import SecurityScanSvg from '@assets/security-scan.svg';
import NumberComponent from '@components1/common/format/Number';
import { action } from 'src/utils/actionStyles';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import SLOInspectionIcon from '@assets/kubernetes/slo-inspection.svg';
import apiKubernetes1 from '@api1/kubernetes1';
import CustomSelectDropdown from '@components1/common/CustomSelectDropdown';
import CopyableText from '@components1/common/CopyableText';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ContainerDetails from '@components1/k8s/pods/ContainerDetails';
import Title from '@components1/common/Title';
import AccordionSmall from '@components1/common/AccordionSmall';
import VolumeDetails from '@components1/k8s/pods/VolumeDetails';
import KubernetesTracesListing from './KubernetesTracesListing';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import recommendationApi from '@api1/recommendation';
import Text from '@components1/common/format/Text';
import { ANNOTATIONS } from '@lib/annotationKeys';
import KubernetesLogs from './KubernetesLogs';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import PropTypes from 'prop-types';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import CodeMirror from '@uiw/react-codemirror';
import { yaml as yamlLang } from '@codemirror/lang-yaml';
import { EditorView } from '@codemirror/view';
import yaml from 'js-yaml';
import Dashboard from '@components1/dashboards/AppDashboard';
import KubernetesPodYaml from '@components1/k8s/details/KubernetesPodYaml';
import KubernetesRightSizing from '@components1/recommendations/KubernetesRightSizing';
import { snackbar } from '@components1/common/snackbarService';
import apiIntegrations from '@api1/integrations';
import KubernetesLogsPattern from './KubernetesLogsPattern';
import KubernetesPodsTable from './KubernetesPods';
import LazyLoadComponent from '@components1/common/LazyLoadComponent';
import SLOConfigDialog from '@components1/k8s/common/SLOConfigDialog';
import CustomTooltip from '@components1/common/CustomTooltip';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';

export const WORKLOAD_HEADERS = [
  { name: 'Application Name', width: '30%' },
  { name: 'Created At', width: '5%' },
  { name: 'Cost', width: '5%' },
  { name: 'Replicas', width: '5%', sortEnabled: true },
  { name: 'CPU', width: '10%' },
  { name: 'Memory', width: '10%' },
  { name: 'Error Count (24h)', width: '5%' },
  { name: 'SLO (24h)', width: '5%' },
  { name: 'Events (High)', width: '10%' },
  { name: 'Optimisations/Autopilots', width: '10%' },
  '',
];

const KubernetesWorkloadsTable = ({ accountId, resource_ids = [] }) => {
  const router = useRouter();
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [kubeid, setKubeid] = useState(router.query.KubernetesDetails);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace ?? '');
  const [selectedName, setSelectedName] = useState(router.query.workloadName ?? '');
  const [inputName, setInputName] = useState(router.query.workloadName ?? '');
  const [workloadTypeFilter, setWorkloadTypeFilter] = useState([]);
  const [selectedWorkloadType, setSelectedWorkloadType] = useState(router.query.workloadType ?? '');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast30Days().getTime() + 60 * 1000,
    endDate: new Date().getTime(),
  });
  const [loading, setLoading] = useState(false);
  const [restartWorkload, setRestartWorkload] = useState(false);
  const [scaleWorkload, setScaleWorkload] = useState(false);
  const [editWorkload, setEditWorkload] = useState(false);
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState('');
  const kubernetesWorkloadTable = 'kubernetesWorkloadTable';
  const [deleteWorkload, setDeleteWorkload] = useState(false);
  const [selectedWorkload, setSelectedWorkload] = useState({});
  const [deleteWorkloadNameConfirmation, setDeleteWorkloadNameConfirmation] = useState('');
  const [errorInWorkloadName, setErrorInWorkloadName] = useState('');
  const [openSLOConfig, setOpenSLOConfig] = useState(false);
  const [sloInitialConfig, setSloInitialConfig] = useState([]);
  const [isSloEdit, setIsSloEdit] = useState(false);
  const sloRequestKeyRef = useRef(null);
  const [workloadFqdn, setWorkloadFqdn] = useState([]);
  const [isLoading, setIsLoading] = useState(false);
  const [applicationSummary, setApplicationSummary] = useState({});
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [isAutoPilotReplicaModalOpen, setIsAutoPilotReplicaModalOpen] = useState(false);
  const [autoPilotReplicaModalData, setAutoPilotReplicaModalData] = useState({});
  const [isAutoPilotVerticalModalOpen, setIsAutoPilotVerticalModalOpen] = useState(false);
  const [autoPilotVerticalModalData, setAutoPilotVerticalModalData] = useState({});
  const [sortObject, setSortObject] = useState({
    name: '',
    order: '',
  });
  const [disableOptions, setDisableOptions] = useState(false);

  // Git modal state
  const [isGitModalOpen, setIsGitModalOpen] = useState(false);
  const [gitModalWorkload, setGitModalWorkload] = useState({});
  const [gitDetails, setGitDetails] = useState({
    codeRepo: '', // workloads.nudgebee.com/git.repo - actual source code repo
    ciRepo: '', // ci.nudgebee.com/git.repo - CI/deployment repo (if different from code repo)
    ciRepoSameAsCode: true, // checkbox: CI repo same as code repo
    branch: 'main',
    hash: '',
    valuesFilePath: '',
    source: null, // 'ci_annotation' | 'workload_annotation' | 'cloud_resource_attributes' | null
  });
  const [isGitDetailsLoading, setIsGitDetailsLoading] = useState(false);
  const [isGitEditMode, setIsGitEditMode] = useState(false);
  const [allGitIntegrations, setAllGitIntegrations] = useState([]); // Combined integrations for dropdown
  const [selectedGitIntegration, setSelectedGitIntegration] = useState(''); // Selected integration key for code repo (format: "type:name")
  const [reposFromIntegration, setReposFromIntegration] = useState([]); // Repos from selected integration for code repo
  const [isGitReposLoading, setIsGitReposLoading] = useState(false);
  // CI repo integration selection (separate from code repo)
  const [selectedCiGitIntegration, setSelectedCiGitIntegration] = useState('');
  const [ciReposFromIntegration, setCiReposFromIntegration] = useState([]);

  const MENU_ITEMS = [
    {
      icon: LogFileIcon,
      label: 'Logs',
      id: '4',
    },
    {
      icon: EditFileIcon,
      label: 'Edit',
      id: '5',
    },
    {
      icon: SecurityScanSvg,
      label: 'Scan Image',
      id: '6',
    },
    {
      icon: SLOInspectionIcon,
      label: 'SLO',
      id: '8',
    },
  ];

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const onNamespaceFilterChange = (e) => {
    setSelectedNamespace(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
  };

  const onWorkloadTypeFilterChange = (e) => {
    setSelectedWorkloadType(e?.target?.value);
    setCurrentPage(0);
  };

  const onNameFilterChange = (value) => {
    if (selectedName && value.trim() == '') {
      setSelectedName('');
      setCurrentPage(0);
      applyFiltersOnRouter(router, { workloadName: '' });
    }
    setInputName(value);
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setCurrentPage(0);
  };

  const onMenuClick = (menuItem, data) => {
    if (hasWriteAccess(data?.cloud_account_id)) {
      if (menuItem.id === '00') {
        setAutoPilotReplicaModalData(data);
        setIsAutoPilotReplicaModalOpen(true);
      } else if (menuItem.id === '01') {
        setAutoPilotVerticalModalData(data);
        setIsAutoPilotVerticalModalOpen(true);
      } else if (menuItem.id === '1') {
        setRestartWorkload(true);
        setSelectedWorkload(data);
      } else if (menuItem.id === '2') {
        setScaleWorkload(true);
        setSelectedWorkload(data);
      } else if (menuItem.id === '3') {
        setDeleteWorkload(true);
        setSelectedWorkload(data);
      } else if (menuItem.id === '4') {
        if (!router.pathname.includes('/kubernetes/details')) {
          router.push(
            `kubernetes/details/${accountId}?filter={"namespaceName":"${data.namespace}","workloadName":"${data.name}"}&time=1:h#monitoring/logs`
          );
        } else {
          router.push(`${accountId}?filter={"namespaceName":"${data.namespace}","workloadName":"${data.name}"}&time=1:h#monitoring/logs`);
        }
      } else if (menuItem.id === '5') {
        setEditWorkload(true);
        setSelectedWorkload(data);
      } else if (menuItem.id === '6') {
        k8sApi.scanImage({ accountId: accountId, namespace: data.namespace, workloadName: data.name }).then((res) => {
          const errMsg = parseHttpResponseBodyMessage(res?.data);
          if (errMsg) {
            snackbar.error(errMsg);
          } else if (res?.status >= 200 && res?.status < 300) {
            snackbar.success('Scan Image API triggered successfully');
          } else {
            snackbar.error('Failed to trigger image scan');
          }
        });
      } else if (menuItem.id === '7') {
        setGitModalWorkload(data);
        fetchGitDetails(data);
        setIsGitModalOpen(true);
      } else if (menuItem.id === '8') {
        setSelectedWorkload(data);
        const requestKey = `${data.namespace}/${data.name}`;
        sloRequestKeyRef.current = requestKey;
        apiKubernetes1
          .getSLOConfig({
            cloud_account_id: accountId,
            namespace: data.namespace,
            workload_name: data.name,
          })
          .then((res) => {
            if (sloRequestKeyRef.current !== requestKey) return;
            const workloadSloConfig = res?.data?.data?.slo_config_list?.data || [];
            if (workloadSloConfig && workloadSloConfig.length > 0) {
              setSloInitialConfig(workloadSloConfig.map((n) => n.config[0]));
              setIsSloEdit(true);
            } else {
              setSloInitialConfig([]);
              setIsSloEdit(false);
            }
            setOpenSLOConfig(true);
          })
          .catch(() => {
            if (sloRequestKeyRef.current !== requestKey) return;
            setSloInitialConfig([]);
            setIsSloEdit(false);
            setOpenSLOConfig(true);
          });
      }
    } else {
      snackbar.error(`User is not allowed to perform ${menuItem.label} operation`);
    }
  };

  function getMenuItems(item, rightSizeCounts) {
    if (!hasWriteAccess(accountId)) {
      return [];
    }

    let menus = [...MENU_ITEMS];
    if (item.kind == 'Deployment' || item.kind == 'StatefulSet' || item.kind == 'Rollout') {
      menus.push(
        {
          icon: AutoPilotSettingIcon,
          label: 'Auto Optimize',
          id: '0',
          subMenu: [
            {
              label: 'Horizontal Rightsizing',
              id: '00',
              disabled: rightSizeCounts.horizontal_rightsize > 0 || rightSizeCounts.continuous_rightsize > 0,
            },
            {
              label: 'Vertical Rightsizing',
              id: '01',
              disabled: rightSizeCounts.vertical_rightsize > 0,
            },
          ],
        },
        {
          icon: ReloadIcon,
          label: 'Restart',
          id: '1',
        },
        {
          icon: ScaleIcon,
          label: 'Scale',
          id: '2',
        },
        {
          icon: DeleteIcon,
          label: 'Delete',
          id: '3',
        }
      );
    }
    // Always show Git menu - opens modal to view/configure git details
    menus.push({
      icon: GitSvg,
      label: 'Git',
      id: '7',
    });
    return menus.sort((a, b) => a.label.localeCompare(b.label));
  }

  // List Git configurations (GitHub + GitLab) for dropdown
  const listGitConfigurations = () => {
    setIsGitReposLoading(true);
    setSelectedGitIntegration('');
    setReposFromIntegration([]);
    setSelectedCiGitIntegration('');
    setCiReposFromIntegration([]);

    // Fetch both GitHub and GitLab integrations in parallel
    Promise.all([
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'github' }),
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'gitlab' }),
    ])
      .then(([githubRes, gitlabRes]) => {
        const githubData =
          githubRes?.data?.length > 0
            ? githubRes.data.map((g) => ({
                name: g.name,
                type: 'github',
                url: 'https://github.com',
                projects: Array.isArray(g.projects) ? g.projects : [],
              }))
            : [];

        const gitlabData =
          gitlabRes?.data?.length > 0
            ? gitlabRes.data.map((g) => ({
                name: g.name,
                type: 'gitlab',
                url: g.url || 'https://gitlab.com',
                projects: Array.isArray(g.projects) ? g.projects : [],
              }))
            : [];

        // Combine for dropdown with type prefix for unique identification
        const combined = [
          ...githubData.map((g) => ({ ...g, key: `github:${g.name}`, label: `GitHub: ${g.name}` })),
          ...gitlabData.map((g) => ({ ...g, key: `gitlab:${g.name}`, label: `GitLab: ${g.name}` })),
        ];
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

  // Handle Git integration selection - populate repos from that integration
  const handleGitIntegrationSelect = (integrationKey) => {
    setSelectedGitIntegration(integrationKey);
    const integration = allGitIntegrations.find((i) => i.key === integrationKey);
    if (integration && Array.isArray(integration.projects) && integration.projects.length > 0) {
      // projects is array of {key: "owner/repo", name: "repo_name"}
      // Store integration info for URL construction
      setReposFromIntegration(integration.projects.map((p) => ({ ...p, integrationUrl: integration.url, integrationType: integration.type })));
    } else {
      setReposFromIntegration([]);
    }
  };

  // Handle repo selection - construct full URL for code repo based on integration type
  const handleRepoSelect = (repoKey) => {
    // Find the repo to get its integration URL
    const repo = reposFromIntegration.find((r) => r.key === repoKey);
    const baseUrl = repo?.integrationUrl || 'https://github.com';
    // repoKey is in format "owner/repo" or "group/project"
    const repoUrl = `${baseUrl}/${repoKey}`;
    setGitDetails((prev) => ({ ...prev, codeRepo: repoUrl }));
  };

  // Handle CI Git integration selection - populate repos from that integration
  const handleCiGitIntegrationSelect = (integrationKey) => {
    setSelectedCiGitIntegration(integrationKey);
    const integration = allGitIntegrations.find((i) => i.key === integrationKey);
    if (integration && Array.isArray(integration.projects) && integration.projects.length > 0) {
      setCiReposFromIntegration(integration.projects.map((p) => ({ ...p, integrationUrl: integration.url, integrationType: integration.type })));
    } else {
      setCiReposFromIntegration([]);
    }
  };

  // Handle CI repo selection - construct full URL for CI repo based on integration type
  const handleCiRepoSelect = (repoKey) => {
    // Find the repo to get its integration URL
    const repo = ciReposFromIntegration.find((r) => r.key === repoKey);
    const baseUrl = repo?.integrationUrl || 'https://github.com';
    // repoKey is in format "owner/repo" or "group/project"
    const repoUrl = `${baseUrl}/${repoKey}`;
    setGitDetails((prev) => ({ ...prev, ciRepo: repoUrl }));
  };

  // Fetch git details from annotations or cloud_resource_attributes
  const fetchGitDetails = async (workload) => {
    setIsGitDetailsLoading(true);
    setIsGitEditMode(false);

    const annotations = workload.meta?.config?.annotations || {};

    // Check both annotation types from k8s annotations
    const ciRepoAnnotation = annotations[ANNOTATIONS.CI_GIT_REPO];
    const ciHash = annotations[ANNOTATIONS.CI_GIT_HASH];
    const ciBranch = annotations[ANNOTATIONS.CI_GIT_BRANCH] || 'main';
    const ciValuesPath = annotations[ANNOTATIONS.CI_HELM_VALUES_PATH] || '';
    const workloadRepoAnnotation = annotations[ANNOTATIONS.WORKLOAD_GIT_REPO];
    const workloadHash = annotations[ANNOTATIONS.WORKLOAD_GIT_HASH];

    // If we have annotations, show them
    if (ciRepoAnnotation || workloadRepoAnnotation) {
      const codeRepo = workloadRepoAnnotation || ciRepoAnnotation;
      const ciRepo = ciRepoAnnotation || '';
      const ciRepoSameAsCode = !ciRepoAnnotation || ciRepoAnnotation === workloadRepoAnnotation;

      setGitDetails({
        codeRepo: codeRepo,
        ciRepo: ciRepoSameAsCode ? '' : ciRepo,
        ciRepoSameAsCode: ciRepoSameAsCode,
        branch: ciBranch,
        hash: ciHash || workloadHash || '',
        valuesFilePath: ciValuesPath,
        source: ciRepoAnnotation ? 'ci_annotation' : 'workload_annotation',
      });
      setIsGitDetailsLoading(false);
      return;
    }

    // Check cloud_resource_attributes for manual configuration
    try {
      const attributes = await k8sApi.getResourceAttributes(workload.cloud_resource_id);

      const ciRepoAttr = attributes.find((a) => a.name === ANNOTATIONS.CI_GIT_REPO);
      const branchAttr = attributes.find((a) => a.name === ANNOTATIONS.CI_GIT_BRANCH);
      const valuesAttr = attributes.find((a) => a.name === ANNOTATIONS.CI_HELM_VALUES_PATH);
      const workloadRepoAttr = attributes.find((a) => a.name === ANNOTATIONS.WORKLOAD_GIT_REPO);

      const codeRepoValue = workloadRepoAttr?.value || ciRepoAttr?.value;
      const ciRepoValue = ciRepoAttr?.value || '';

      if (codeRepoValue) {
        const ciRepoSameAsCode = !ciRepoValue || ciRepoValue === codeRepoValue;
        setGitDetails({
          codeRepo: codeRepoValue,
          ciRepo: ciRepoSameAsCode ? '' : ciRepoValue,
          ciRepoSameAsCode: ciRepoSameAsCode,
          branch: branchAttr?.value || 'main',
          hash: '',
          valuesFilePath: valuesAttr?.value || '',
          source: 'cloud_resource_attributes',
        });
      } else {
        // No git config found - show edit mode and load GitHub repos
        setGitDetails({
          codeRepo: '',
          ciRepo: '',
          ciRepoSameAsCode: true,
          branch: 'main',
          hash: '',
          valuesFilePath: '',
          source: null,
        });
        setIsGitEditMode(true);
        listGitConfigurations();
      }
    } catch (error) {
      console.error('Error fetching git details:', error);
      setGitDetails({
        codeRepo: '',
        ciRepo: '',
        ciRepoSameAsCode: true,
        branch: 'main',
        hash: '',
        valuesFilePath: '',
        source: null,
      });
      setIsGitEditMode(true);
      listGitConfigurations();
    } finally {
      setIsGitDetailsLoading(false);
    }
  };

  // Save git details to cloud_resource_attributes
  // Saves both ci.nudgebee.com/* (for PR creation) and workloads.nudgebee.com/* (for llm-server agent_code_2)
  const handleSaveGitDetails = async () => {
    setIsGitDetailsLoading(true);
    try {
      // Determine CI repo value - use codeRepo if same, otherwise use separate ciRepo
      const ciRepoValue = gitDetails.ciRepoSameAsCode ? gitDetails.codeRepo : gitDetails.ciRepo;

      await k8sApi.upsertResourceAttributes(gitModalWorkload.cloud_resource_id, accountId, [
        // Workload annotation for code repo (llm-server agent_code_2)
        { name: ANNOTATIONS.WORKLOAD_GIT_REPO, value: gitDetails.codeRepo },
        // CI annotations for PR creation
        { name: ANNOTATIONS.CI_GIT_REPO, value: ciRepoValue },
        { name: ANNOTATIONS.CI_GIT_BRANCH, value: gitDetails.branch },
        { name: ANNOTATIONS.CI_HELM_VALUES_PATH, value: gitDetails.valuesFilePath },
      ]);
      setGitDetails((prev) => ({ ...prev, source: 'cloud_resource_attributes' }));
      setIsGitEditMode(false);
      snackbar.success('Git configuration saved successfully');
    } catch (error) {
      console.error('Error saving git details:', error);
      // Extract meaningful error message
      let errorMessage = 'Failed to save git configuration';
      if (error?.response?.errors?.[0]?.message) {
        errorMessage = error.response.errors[0].message;
      } else if (error?.message) {
        errorMessage = error.message;
      }
      // Check for common RPC permission errors
      if (errorMessage.includes('not found in type') || errorMessage.includes('permission')) {
        errorMessage = 'Save failed: Missing database permissions. Please contact your administrator.';
      }
      snackbar.error(errorMessage);
    } finally {
      setIsGitDetailsLoading(false);
    }
  };

  const getNoOfPodsCount = (item) => {
    return <Typography sx={{ color: '#374151' }}>{item.ready_pods + '/' + item.total_pods}</Typography>;
  };

  const listWorkloads = () => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    setDisableOptions(true);
    setData([]);
    setTotalCount(0);
    let query = {
      accountId: accountId,
      namespaceName: selectedNamespace,
      workloadName: selectedName,
      workloadType: selectedWorkloadType,
      resource_ids: resource_ids,
    };
    k8sApi
      .getK8sWorkload(recordsPerPage, currentPage * recordsPerPage, query, sortObject)
      .then((res) => {
        setLoading(false);
        let workloadFqdn = [];
        let data = res.data.k8s_workloads?.map((item) => {
          workloadFqdn.push(item.namespace + '.' + item.name + '.' + item.kind);
          const drilldownQuery = {
            workloadName: item.name,
            namespaceName: item.namespace,
            workloadType: item.kind,
            subject_namespace: item.namespace,
            subject_name: item.name,
            subject_kind: item.kind,
            controller: item.name,
            workloadMeta: item.meta,
            accountId: accountId,
            type: 'workload',
            data: item,
          };
          return [
            {
              component: (
                <>
                  <Box display='flex'>
                    <CopyableText copyableText={item.name} />
                    <Text showAutoEllipsis value={item.name} />
                  </Box>
                  <Text showAutoEllipsis value={`ns: ${item.namespace + ' | ' + item.kind}`} secondaryText />
                </>
              ),
              drilldownQuery: drilldownQuery,
            },
            {
              component: <Datetime value={item.creation_time} />,
              data: item.creation_time,
            },
            { text: '-' },
            { component: getNoOfPodsCount(item) },
            { text: '-' },
            { text: '-' },
            {
              component: <Text value={'0'} />,
            },
            { text: '-' },
            { text: '-' },
            { text: '-' },
            {
              component: (
                <Box display={'flex'} justifyContent={'flex-end'} alignItems={'center'}>
                  <ThreeDotsMenu
                    sx={{ ...action.primary }}
                    menuItems={getMenuItems(drilldownQuery, {
                      continuous_rightsize: 0,
                      vertical_rightsize: 0,
                      horizontal_rightsize: 0,
                    })}
                    data={item}
                    onMenuClick={onMenuClick}
                  />
                </Box>
              ),
            },
          ];
        });

        if (resource_ids.length) {
          setNamespaceFilter([...new Set(res?.data?.k8s_workloads.map((item) => item.namespace))]);
          setWorkloadTypeFilter([...new Set(res?.data?.k8s_workloads.map((item) => item.kind))]);
        }
        let totalCount = res.data.k8s_workloads_aggregate?.aggregate?.count;
        setData(data);
        setTotalCount(totalCount);
        setWorkloadFqdn(workloadFqdn);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    setData([]);
    listWorkloads();
  }, [accountId, currentPage, recordsPerPage, selectedNamespace, selectedWorkloadType, JSON.stringify(resource_ids), sortObject, selectedName]);

  useEffect(() => {
    if (!accountId || resource_ids.length > 0) {
      return;
    }
    setCurrentPage(0);
    k8sApi.getK8sNamespaceNames(accountId).then((res) => {
      let namespaces = res.data.namespaces;
      setNamespaceFilter(namespaces);
    });

    k8sApi.listK8sWorkloadWorkloadType({ accountId }).then((res) => {
      let workloads = res.data.k8s_workloads?.map((e) => e.workload_type)?.filter((e) => e?.toLowerCase() != 'replicaset');
      setWorkloadTypeFilter(workloads);
    });
  }, [accountId]);

  useEffect(() => {
    async function applicationSummaryData() {
      let applicationSummary = {};
      const workloadResponse = await apiKubernetes1.listK8sWorkloadKindCount(accountId, selectedNamespace, resource_ids);
      const k8sWorkloadCountData = workloadResponse?.data?.data?.workload_counts?.rows?.[0] ?? {};
      if (k8sWorkloadCountData && Object.keys(k8sWorkloadCountData).length > 0) {
        applicationSummary = k8sWorkloadCountData;
      }

      const recommendationResponse = await recommendationApi.getK8sRecommendationSummary({
        accountId: accountId,
        category: 'RightSizing',
        ruleName: ['pod_right_sizing', 'replica_right_sizing', 'abandoned_resource'],
        status: ['Open', 'InProgress'],
        resourceNamespace: selectedNamespace,
        resource_ids: resource_ids,
      });
      const estimatedSaving = recommendationResponse?.data?.recommendation_aggregate.aggregate.sum.estimated_savings * 12 || '-';
      applicationSummary.estimatedSaving = estimatedSaving;

      const recommendationAggregate = await recommendationApi.getK8sRecommendationAggregate({
        accountId: accountId,
        category: 'RightSizing',
        ruleName: ['pod_right_sizing', 'replica_right_sizing', 'abandoned_resource'],
        resourceNamespace: selectedNamespace,
        status: ['Open', 'InProgress'],
        resource_ids: resource_ids,
      });
      const recommendationCount = recommendationAggregate?.data?.recommendation_aggregate?.aggregate?.count ?? '-';
      applicationSummary.recommendation_count = recommendationCount;

      const eventAggregate = await apiKubernetes1.getEventAggregate(
        {
          account_id: accountId,
          namespace: selectedNamespace,
          startDate: new Date(selectedDateRange.startDate),
          endDate: new Date(selectedDateRange.endDate),
          resource_ids: resource_ids,
        },
        ['count_application_issues', 'event_count']
      );
      const eventAndErrorAggregate = eventAggregate?.data?.data?.event_groupings_v2 ?? '-';
      applicationSummary.event_count = eventAndErrorAggregate?.rows?.[0]?.event_count ?? '-';
      applicationSummary.error_count = eventAndErrorAggregate?.rows?.[0]?.count_application_issues ?? '-';

      const mtdAggregate = await apiKubernetes1.getWorkloadMTDAggregate({
        account_id: accountId,
        namespace: selectedNamespace,
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
        resource_ids: resource_ids,
      });
      const mtdCost = mtdAggregate?.data?.data?.k8s_metrics_groupings_v2?.rows[0]?.cost?.toFixed() ?? '-';
      applicationSummary.mtd_cost = mtdCost;

      setApplicationSummary(applicationSummary);
    }
    setApplicationSummary({});
    applicationSummaryData();
  }, [accountId, selectedNamespace, selectedWorkloadType, JSON.stringify(resource_ids)]);

  useEffect(() => {
    if (!accountId || workloadFqdn.length == 0) {
      setDisableOptions(false);
      return;
    }

    const fetchAllData = async () => {
      try {
        // First batch of fetches
        const [metricsResponse, sloResponse, eventResponse] = await Promise.all([fetchMetrics(), fetchSLOReport(), fetchEventCounts()]);

        const updatedData = [...data];
        updateMetricsData(metricsResponse, updatedData);
        updateSLOData(sloResponse, updatedData);
        updateEventCounts(eventResponse, updatedData);
        setData(updatedData);
        await getErrorCounts(workloadFqdn);

        const [recommendationResponse, autopilotResponse] = await Promise.all([fetchRecommendationCounts(), fetchAutoPilot()]);

        const finalData = [...updatedData];
        updateRecommendationAndAutoPilot(recommendationResponse, autopilotResponse, finalData, accountId);
        setData(finalData);
      } catch (error) {
        console.error('Error fetching data:', error);
      } finally {
        setDisableOptions(false);
      }
    };

    fetchAllData();
  }, [workloadFqdn, selectedDateRange.startDate, selectedDateRange.endDate, accountId]);

  const fetchAutoPilot = () => {
    return recommendationApi.getAutoOptimize({
      accountId: accountId,
      category: ['continuous_rightsize', 'vertical_rightsize', 'horizontal_rightsize'],
    });
  };

  const fetchMetrics = () => {
    return k8sApi.getK8sMetrices({
      accountId: accountId,
      workloadFqdn: workloadFqdn.map((item) => item.split('.').slice(0, -1).join('.')),
      startDate: new Date(selectedDateRange.startDate),
      endDate: new Date(selectedDateRange.endDate),
    });
  };

  const fetchSLOReport = () => {
    return apiKubernetes1.getSLOReport({
      accountId,
      workload_namespace: Array.from(new Set(workloadFqdn.map((d) => d.split('.')[0]))),
      workload_name: Array.from(new Set(workloadFqdn.map((d) => d.split('.')[1]))),
      start_date: new Date(getSpecificTime(1440)).toISOString(),
      end_date: new Date().toISOString(),
    });
  };

  const fetchEventCounts = () => {
    return apiKubernetes1.getWorkloadEventCounts(
      workloadFqdn,
      new Date(selectedDateRange.startDate).toISOString(),
      new Date(selectedDateRange.endDate).toISOString(),
      accountId
    );
  };

  const fetchRecommendationCounts = () => {
    const accountObjectIds = data.map(
      (f) =>
        f[0].drilldownQuery.namespaceName.replace(/-/g, '__') +
        '___' +
        f[0].drilldownQuery.subject_kind +
        '___' +
        f[0].drilldownQuery.workloadName.replace(/-/g, '__')
    );
    return apiKubernetes1.getWorkloadRecommendationCounts(accountObjectIds, accountId);
  };

  const countAutoPilots = (listAutoPilots, namespaceName, workloadName) => {
    let count = 0,
      continuousCount = 0,
      verticalCount = 0,
      horizontalCount = 0;

    for (let autopilot of listAutoPilots) {
      const hasAutopilotConfigured = autopilot.auto_optimize_resource_maps.some(
        (r) =>
          (r?.resource_identifier?.name === workloadName && r?.resource_identifier?.namespace === namespaceName) ||
          (r?.resource_identifier?.name == null && r?.resource_identifier?.namespace === namespaceName)
      );

      if (hasAutopilotConfigured) {
        count++;
        switch (autopilot.category) {
          case 'continuous_rightsize':
            continuousCount++;
            break;
          case 'vertical_rightsize':
            verticalCount++;
            break;
          case 'horizontal_rightsize':
            horizontalCount++;
            break;
        }
      }
    }

    return { count, continuousCount, verticalCount, horizontalCount };
  };

  const createComponentLink = (href, text) => (
    <ReactLink
      href={href}
      onClick={(e) => {
        e.stopPropagation();
      }}
    >
      {text}
    </ReactLink>
  );

  const createComponentSpan = (recommendationCount, count, accountId) => {
    const recommendationLink =
      recommendationCount > 0
        ? createComponentLink(`/kubernetes/details/${accountId}?accountId=${accountId}#optimize/right-sizing`, recommendationCount)
        : '-';

    const countLink = count > 0 ? createComponentLink(`/auto-pilot?accountId=${accountId}#auto-optimize`, count) : '-';

    return (
      <span>
        {recommendationLink}/{countLink}
      </span>
    );
  };

  const updateRecommendationAndAutoPilot = (recommendationResponse, autopilotResponse, updatedData, accountId) => {
    const recommendationWorkloadObject = recommendationResponse?.data?.data?.recommendation_groupings_v2?.rows || [];
    const listAutoPilots = autopilotResponse?.data?.auto_pilot || [];

    return updatedData.map((item) => {
      let recommendationCount = '-';
      const { namespaceName, workloadName, workloadType } = item[0].drilldownQuery;
      const keyName = `${namespaceName}/${workloadType}/${workloadName}`;
      const matchedItem = recommendationWorkloadObject.find((item) => item.account_object_id === keyName);
      if (matchedItem) {
        recommendationCount = matchedItem?.count || '-';
      }
      const { count, continuousCount, verticalCount, horizontalCount } = countAutoPilots(listAutoPilots, namespaceName, workloadName);

      item[9] = {
        component: createComponentSpan(recommendationCount, count, accountId),
      };

      const menus = getMenuItems(item[0].drilldownQuery.data, {
        continuous_rightsize: continuousCount,
        vertical_rightsize: verticalCount,
        horizontal_rightsize: horizontalCount,
      });

      item[10] = {
        component: (
          <Box display={'flex'} justifyContent={'flex-end'} alignItems={'center'}>
            <ThreeDotsMenu sx={{ ...action.primary }} menuItems={menus} data={item[0].drilldownQuery.data} onMenuClick={onMenuClick} />
          </Box>
        ),
      };
      return item;
    });
  };

  const updateMetricsData = (metricsResponse, updatedData) => {
    for (let dataItem of updatedData) {
      const matchedItem = metricsResponse.data?.k8s_pod_groupings?.find(
        (item) => item.namespace_name === dataItem[0].drilldownQuery.namespaceName && item.workload_name === dataItem[0].drilldownQuery.workloadName
      );

      if (matchedItem) {
        dataItem[2] = {
          component: (
            <Currency
              value={matchedItem.cost}
              precison={1}
              sxPrefix={{
                fontSize: '12px',
                color: '#9F9F9F',
                fontWeight: 400,
              }}
            />
          ),
        };
        dataItem[4] = {
          component: (
            <Typography
              sx={{
                '& .suffix': {
                  color: '#B9B9B9',
                  fontSize: '12px',
                },
                '& span': {
                  color: '#B9B9B9',
                  fontSize: '12px',
                },
              }}
            >
              <NumberComponent value={matchedItem.avg_cpu_used} suffix={'vCPU'} />

              <span style={{ paddingLeft: '5px' }}>
                {matchedItem.avg_cpu_request && matchedItem.avg_cpu_used
                  ? `(${((matchedItem.avg_cpu_used / matchedItem.avg_cpu_request) * 100).toFixed(1)}%)`
                  : ''}
              </span>
              <br />
              <span>
                req:
                <NumberComponent
                  value={matchedItem.avg_cpu_request || null}
                  suffix={'vCPU'}
                  sx={{
                    color: '#B9B9B9',
                    fontSize: '12px',
                  }}
                />
              </span>
            </Typography>
          ),
        };

        dataItem[5] = {
          component: (
            <Typography
              sx={{
                '& .sufix': {
                  color: '#B9B9B9',
                  fontSize: '12px',
                },
                '& span': {
                  color: '#B9B9B9',
                  fontSize: '12px',
                },
              }}
            >
              <Memory value={matchedItem.avg_memory_used || null} />
              <span style={{ paddingLeft: '5px' }}>
                {matchedItem.avg_memory_request && matchedItem.avg_memory_used
                  ? `(${((matchedItem.avg_memory_used / matchedItem.avg_memory_request) * 100).toFixed(1)}%)`
                  : ''}
              </span>
              <br />
              <span>
                req:
                <Memory
                  value={matchedItem.avg_memory_request || null}
                  sx={{
                    color: '#B9B9B9',
                    fontSize: '12px',
                  }}
                />
              </span>
            </Typography>
          ),
        };
      }
    }
  };

  const updateSLOData = (sloResponse, updatedData) => {
    const sloReportData = sloResponse?.data?.data?.slo_report ?? [];
    if (sloReportData.length > 0) {
      updatedData.forEach((item, index) => {
        const matchedItem = sloReportData.find(
          (sloItem) =>
            sloItem.workload_namespace === item[0].drilldownQuery.namespaceName && sloItem.workload_name === item[0].drilldownQuery.workloadName
        );
        if (matchedItem) {
          const status = matchedItem.status === 'FIRING' ? 'FIRING' : 'OK';
          updatedData[index][7] = {
            component: <CustomLabels textTransform={'none'} text={status} />,
          };
        }
      });
    }
  };

  const updateEventCounts = (eventResponse, updatedData) => {
    const eventCountData = eventResponse?.data?.data?.event_groupings_v2?.rows || [];
    if (eventCountData.length > 0) {
      for (const itemData of updatedData) {
        const keyName = `${itemData[0].drilldownQuery.namespaceName}/${itemData[0].drilldownQuery.workloadType}/${itemData[0].drilldownQuery.workloadName}`;
        const matchedItem = eventCountData.find((item) => item.service_key === keyName);
        if (matchedItem) {
          itemData[8] = {
            component: <NumberComponent value={matchedItem.count} />,
          };
        }
      }
    }
  };

  const extractNamespaceAndApplication = (value, type) => {
    if (!value) {
      return value;
    }
    const valueArray = value.split('/').filter((e) => e != '');
    if (type === 'namespace') {
      return valueArray[1];
    } else if (type === 'application') {
      const regex = /-?(\w{9,10})?-(\w{1}|(\w{5}))$/;
      return valueArray[2].replace(regex, '');
    }
  };

  const _deploymentChangeRender = (deployment_data) => {
    const deploymentChanges = [];
    const currentStats = deployment_data?.current_stats || {};
    const previousStats = deployment_data?.previous_stats || {};
    const newDeploymentChangeAt = deployment_data?.last_deployment_date_time || '';
    if (previousStats.cpu_p99 && currentStats.cpu_p99 && previousStats.cpu_p99 < currentStats.cpu_p99) {
      const precentDiff = ((currentStats.cpu_p99 - previousStats.cpu_p99) / previousStats.cpu_p99) * 100;
      deploymentChanges.push({
        label: 'cpu_p99',
        message: `CPU P99 increased by ${precentDiff.toFixed(2)}%`,
        previous: previousStats.cpu_p99,
        current: currentStats.cpu_p99,
      });
    }
    if (previousStats.memory_p99 && currentStats.memory_p99 && previousStats.memory_p99 < currentStats.memory_p99) {
      const precentDiff = ((currentStats.memory_p99 - previousStats.memory_p99) / previousStats.memory_p99) * 100;
      deploymentChanges.push({
        label: 'mem_p99',
        message: `Memory P99 increased by ${precentDiff.toFixed(2)}%`,
        previous: previousStats.memory_p99,
        current: currentStats.memory_p99,
      });
    }
    if (
      ((previousStats.total_request_count ?? 0) || (currentStats.total_request_count ?? 0)) &&
      (previousStats.total_request_count ?? 0) < (currentStats.total_request_count ?? 0)
    ) {
      const previousLimit = previousStats.total_request_count ?? 0;
      const currentLimit = currentStats.total_request_count ?? 0;
      const percentDiff = ((currentLimit - previousLimit) / previousLimit) * 100;
      deploymentChanges.push({
        label: 'mem_limit',
        message: `Total Request increased by ${percentDiff.toFixed(2)}%`,
        previous: previousLimit,
        current: currentLimit,
      });
    }
    if (previousStats.log_failure_count && currentStats.log_failure_count && previousStats.log_failure_count < currentStats.log_failure_count) {
      const previousLimit = previousStats.log_failure_count;
      const currentLimit = currentStats.log_failure_count;
      const percentDiff = ((currentLimit - previousLimit) / previousLimit) * 100;
      deploymentChanges.push({
        label: 'log_fail_count',
        message: `Failures in Log increased by ${percentDiff.toFixed(2)}%`,
        previous: previousLimit,
        current: currentLimit,
      });
    }
    if (previousStats.latency && currentStats.latency && previousStats.latency < currentStats.latency) {
      const previousLimit = previousStats.latency;
      const currentLimit = currentStats.latency;
      const percentDiff = ((currentLimit - previousLimit) / previousLimit) * 100;
      deploymentChanges.push({
        label: 'latency',
        message: `Latency increased by ${percentDiff.toFixed(2)}%`,
        previous: previousLimit,
        current: currentLimit,
      });
    }
    if (deploymentChanges.length > 0) {
      const obj = {
        deploymentChanges,
        newDeploymentChangeAt: new Date(newDeploymentChangeAt).toLocaleString(),
      };
      return obj;
    }
    return '-';
  };

  useEffect(() => {
    if (editWorkload) {
      const data = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'get_resource_yaml',
          action_params: { name: selectedWorkload.name, namespace: selectedWorkload.namespace, kind: selectedWorkload?.kind },
          origin: 'Nudgebee UI',
        },
      };
      k8sApi
        .relayForwardRequest(data)
        .then((res) => {
          if (res?.data?.success) {
            const findings = res?.data.findings;
            if (findings && findings.length > 0) {
              for (const element of findings) {
                if (element?.evidence?.length > 0) {
                  for (const evi of element.evidence) {
                    if (evi?.data) {
                      const parsedData = JSON.parse(evi.data);
                      for (const d of parsedData) {
                        if (d.type === 'yaml') {
                          setFileName(d.filename);
                          const text = atob(d.data.slice(2, -1));
                          setText(text);
                          break;
                        }
                      }
                    }
                  }
                }
              }
            }
          } else {
            snackbar.error('No Yaml Found');
          }
        })
        .catch(() => {
          snackbar.error('Failed to fetch the Yaml');
        });
    }
  }, [editWorkload, selectedWorkload, accountId]);

  function getErrorCounts(workloadFqdn) {
    if (!workloadFqdn && workloadFqdn.length === 0) {
      return;
    }
    const workloadNames = workloadFqdn.map((element) => '/k8s/' + element.replace(/\./g, '/') + '.*').join('|');

    const requestBody = {
      accountId: accountId,
      metrics: ['container_error_log_count_with_workload'],
      startDate: getYesterday().getTime(),
      endDate: new Date().getTime(),
      workloadName: workloadNames,
      kind: 'workload',
    };
    apiKubernetes1
      .utilisationApi(requestBody)
      .then((res) => {
        if (res?.length > 0) {
          const series_list_result = res?.[0]?.payload || [];
          if (series_list_result && series_list_result.length > 0) {
            for (const element of data) {
              let item = series_list_result?.filter(
                (item) =>
                  extractNamespaceAndApplication(item.metric.container_id, 'namespace') === element[0].drilldownQuery.namespaceName &&
                  extractNamespaceAndApplication(item.metric.container_id, 'application') === element[0].drilldownQuery.workloadName
              );
              if (item && item.length > 0) {
                const sum = item
                  .map((f) => f.values)
                  .flat()
                  ?.reduce(function (accumulator, currentValue) {
                    return accumulator + parseInt(currentValue, 10);
                  }, 0);
                element[6] = {
                  component:
                    sum > 0 ? (
                      <ReactLink
                        href={`/kubernetes/details/${accountId}?workloadName=${element[0].drilldownQuery.workloadName}&workloadNamespace=${element[0].drilldownQuery.namespaceName}#monitoring/groups`}
                        onClick={(event) => {
                          event.stopPropagation();
                        }}
                      >
                        {sum}
                      </ReactLink>
                    ) : (
                      0
                    ),
                };
              }
            }
            setData([...data]);
          }
        }
      })
      .catch((error) => {
        console.error(error);
      });
  }

  useEffect(() => {
    if (router.query.accountId != kubeid) {
      setKubeid(router.query.accountId);
    }
  }, [router.query.accountId]);

  const closeAutoPilotReplicaModal = (success) => {
    if (success) {
      snackbar.success('Horizontal Rightsizing Created Successfully');
    }
    setIsAutoPilotReplicaModalOpen(false);
  };

  const closeAutoPilotVerticalModal = (success) => {
    if (success) {
      snackbar.success('Vertical Rightsizing Created Successfully');
    }
    setIsAutoPilotVerticalModalOpen(false);
  };

  const handleCloseRestartPopUp = () => {
    setRestartWorkload(false);
    setSelectedWorkload({});
    setScaleWorkload(false);
    setIsLoading(false);
  };

  const handleCloseDeletePopUp = () => {
    setSelectedWorkload({});
    setDeleteWorkload(false);
    setErrorInWorkloadName('');
  };

  const handleCloseEditPopUp = () => {
    setSelectedWorkload({});
    setEditWorkload(false);
    setText('');
    setErrorInWorkloadName('');
  };

  const handleSubmitOfEdit = () => {
    let jsonObj;
    try {
      jsonObj = yaml.load(text);
    } catch {
      snackbar.error('Invalid YAML');
      return;
    }
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'replace_workload',
        action_params: {
          name: selectedWorkload.name,
          namespace: selectedWorkload.namespace,
          kind: selectedWorkload?.kind,
          [selectedWorkload?.kind?.toLowerCase()]: jsonObj,
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(data)
      .then((res) => {
        if (res?.data?.success) {
          handleCloseEditPopUp();
          snackbar.success(`${selectedWorkload?.kind} ${selectedWorkload?.name} is updated`);
        } else {
          let message = res.data.msg;
          let httpBodyError = message.split('HTTP response body: ')[1];
          if (httpBodyError) {
            let httpBody = JSON.parse(httpBodyError);
            let msg = httpBody.details.causes.map((c) => c.field + ':' + c.reason).join(';');
            snackbar.error(msg);
            return;
          }
          snackbar.error(`${selectedWorkload?.kind} ${selectedWorkload?.name} is failed`);
        }
      })
      .catch(() => {
        snackbar.error(`${selectedWorkload?.kind} ${selectedWorkload?.name} is failed`);
      });
  };

  const handleSubmit = () => {
    const requestBody = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'rollout_restart',
        action_params: {
          name: selectedWorkload.name,
          namespace: selectedWorkload.namespace,
          kind: selectedWorkload?.kind.toLowerCase(),
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(requestBody)
      .then((res) => {
        if (res?.data?.success) {
          snackbar.success(`${selectedWorkload?.kind} ${selectedWorkload?.name} restarted successfully`);
        } else {
          snackbar.error(`Failed to restart ${selectedWorkload?.kind} ${selectedWorkload?.name}`);
        }
        handleCloseRestartPopUp();
      })
      .catch(() => {
        snackbar.error(`Failed to restart ${selectedWorkload?.kind} ${selectedWorkload?.name}`);
        handleCloseRestartPopUp();
      });
  };

  const handleSubmitOfScale = (value) => {
    setIsLoading(true);
    const requestBody = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'replica_rightsizing',
        action_params: {
          name: selectedWorkload.name,
          namespace: selectedWorkload.namespace,
          kind: selectedWorkload?.kind,
          replica_count: parseInt(value),
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(requestBody)
      .then((res) => {
        if (res?.data?.success) {
          snackbar.success(`${selectedWorkload?.kind} ${selectedWorkload?.name} scaled successfully`);
        } else {
          snackbar.error(`Failed to scale ${selectedWorkload?.kind} ${selectedWorkload?.name}`);
        }
        handleCloseRestartPopUp();
      })
      .catch(() => {
        snackbar.error(`Failed to scale ${selectedWorkload?.kind} ${selectedWorkload?.name}`);
        handleCloseRestartPopUp();
      })
      .finally(() => {
        setIsLoading(false);
      });
  };

  const handleSubmitOfDelete = () => {
    if (selectedWorkload.name !== deleteWorkloadNameConfirmation) {
      setErrorInWorkloadName("Workload Name doesn't Match");
      return;
    }
    const requestBody = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'delete_workload',
        action_params: {
          name: selectedWorkload.name,
          namespace: selectedWorkload.namespace,
          kind: selectedWorkload?.kind.toLowerCase(),
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(requestBody)
      .then((res) => {
        if (res?.data?.success) {
          snackbar.success(`${selectedWorkload?.kind} ${selectedWorkload?.name} deleted successfully`);
        } else {
          snackbar.error(`Failed to delete ${selectedWorkload?.kind} ${selectedWorkload?.name}`);
        }
        handleCloseDeletePopUp();
      })
      .catch(() => {
        snackbar.error(`Failed to delete ${selectedWorkload?.kind} ${selectedWorkload?.name}`);
        handleCloseDeletePopUp();
      });
  };

  const additionalComponent = () => {
    return (
      <>
        <Typography fontSize='14px' mb='12px'>
          To confirm the deletion of this workload, type the name of workload in the box.
        </Typography>
        <Input
          size='sm'
          id='outlined-basic'
          label='Workload Name'
          value={deleteWorkloadNameConfirmation}
          onChange={(value) => {
            setErrorInWorkloadName('');
            setDeleteWorkloadNameConfirmation(value);
          }}
          error={errorInWorkloadName || undefined}
        />
      </>
    );
  };

  const additionalEditComponent = () => {
    return (
      <Box sx={{ width: '100%' }}>
        <CodeMirror
          value={text}
          height='500px'
          extensions={[yamlLang(), EditorView.lineWrapping]}
          onChange={(value) => {
            setText(value);
          }}
          editable={true}
          style={{
            border: '1px solid silver',
          }}
        />
      </Box>
    );
  };

  useEffect(() => {
    if (!openSLOConfig) {
      setSelectedWorkload({});
      setSloInitialConfig([]);
      setIsSloEdit(false);
    }
  }, [openSLOConfig]);

  const onEnterPress = () => {
    setSelectedName(inputName);
    setCurrentPage(0);
  };

  const handleClearFilters = () => {
    setSelectedName('');
    setInputName('');
    setCurrentPage(0);
    applyFiltersOnRouter(router, { workloadName: '' });
  };

  const sortEventChange = (e) => {
    setSortObject(e);
  };

  return (
    <>
      <Modal
        open={restartWorkload}
        handleClose={handleCloseRestartPopUp}
        title={'Restart the ' + selectedWorkload.kind + ' ' + selectedWorkload.name}
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
            <DsButton id='cancel-restart-btn' tone='secondary' size='md' onClick={handleCloseRestartPopUp}>
              Cancel
            </DsButton>
            <DsButton id='confirm-restart-btn' tone='primary' size='md' onClick={handleSubmit}>
              Restart
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ padding: '24px' }}>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: colors.text.secondary,
              lineHeight: 1.5,
            }}
          >
            Are you sure you want to restart this workload?
          </Typography>
        </Box>
      </Modal>
      <Modal
        open={deleteWorkload}
        handleClose={handleCloseDeletePopUp}
        title={'Delete the ' + selectedWorkload.kind + ' ' + selectedWorkload.name}
        width='md'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
            <DsButton id='cancel-delete-workload-btn' tone='secondary' size='md' onClick={handleCloseDeletePopUp}>
              Cancel
            </DsButton>
            <DsButton id='confirm-delete-workload-btn' tone='danger' size='md' onClick={handleSubmitOfDelete}>
              Delete
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ padding: '24px' }}>{additionalComponent()}</Box>
      </Modal>
      <Modal
        open={editWorkload}
        handleClose={handleCloseEditPopUp}
        title={'Edit the ' + selectedWorkload.kind + ' ' + selectedWorkload.name + ' - ' + fileName}
        width='lg'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
            <DsButton id='cancel-edit-workload-btn' tone='secondary' size='md' onClick={handleCloseEditPopUp}>
              Cancel
            </DsButton>
            <DsButton id='confirm-edit-workload-btn' tone='primary' size='md' onClick={handleSubmitOfEdit}>
              Save
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ padding: '24px' }}>{additionalEditComponent()}</Box>
      </Modal>
      <SLOConfigDialog
        open={openSLOConfig}
        onClose={() => setOpenSLOConfig(false)}
        accountId={accountId}
        workload={openSLOConfig ? selectedWorkload : null}
        initialConfig={sloInitialConfig}
        isEdit={isSloEdit}
      />
      <KubernetesScaleUpdateForm
        handleClose={handleCloseRestartPopUp}
        handleSubmit={(value) => handleSubmitOfScale(value)}
        open={scaleWorkload}
        selectedRow={selectedWorkload}
        loading={isLoading}
      />
      <ListingLayout id='all-workloads'>
        <ListingLayout.Toolbar
          actions={
            <>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                }}
                onChange={({ selection }) => handleDateRangeChange(selection)}
              />
              <DownloadButton onClick={() => ({ tableId: kubernetesWorkloadTable })} />
            </>
          }
        >
          <FilterDropdown
            label='Namespace'
            options={namespaceFilter.map((o) => ({ value: o, label: o }))}
            value={selectedNamespace}
            onSelect={onNamespaceFilterChange}
            disabled={disableOptions}
          />
          <FilterDropdown
            label='Workload Type'
            options={workloadTypeFilter.map((o) => ({ value: o, label: o }))}
            value={selectedWorkloadType}
            onSelect={onWorkloadTypeFilterChange}
            disabled={disableOptions}
          />
          <CustomSearch
            label='Application Name'
            value={inputName}
            onChange={onNameFilterChange}
            onEnterPress={onEnterPress}
            onClear={handleClearFilters}
            disabled={disableOptions}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <Modal
            width='lg'
            open={isAutoPilotReplicaModalOpen}
            handleClose={() => closeAutoPilotReplicaModal(false)}
            title={'Auto Optimize - Replica Rightsizing'}
          >
            <AutoOptimizeHorizontalRightSizingSingleConfiguration
              autoOptimizeData={{
                auto_optimize_resource_maps: [
                  {
                    resource_identifier: {
                      namespace: autoPilotReplicaModalData.namespace,
                      name: autoPilotReplicaModalData.name,
                      type: autoPilotReplicaModalData.kind,
                    },
                  },
                ],
              }}
              closeAutoPilotSingleConfigModal={closeAutoPilotReplicaModal}
              msTeamsData={[]}
              googleChannelList={[]}
              setIsLoading={setLoading}
            />
          </Modal>
          <Modal
            width='md'
            open={isAutoPilotVerticalModalOpen}
            handleClose={() => closeAutoPilotVerticalModal(false)}
            title={'Auto Optimize - Vertical Rightsizing'}
          >
            <AutoOptimizeContinuousVerticalRightSizingSingleConfiguration
              autoOptimizeData={{
                auto_optimize_resource_maps: [
                  {
                    resource_identifier: {
                      namespace: autoPilotVerticalModalData.namespace,
                      name: autoPilotVerticalModalData.name,
                      type: autoPilotVerticalModalData.kind,
                    },
                  },
                ],
              }}
              closeAutoPilotSingleConfigModal={closeAutoPilotVerticalModal}
              msTeamsData={[]}
              googleChannelList={[]}
              setIsLoading={setLoading}
              currentData={{}}
            />
          </Modal>

          {/* Git Configuration Modal */}
          <Modal
            width='md'
            open={isGitModalOpen}
            handleClose={() => {
              setIsGitModalOpen(false);
              setIsGitEditMode(false);
            }}
            title={`Git Configuration - ${gitModalWorkload.name || ''}`}
            loader={isGitDetailsLoading}
            actionButtons={
              isGitEditMode ? (
                <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
                  <DsButton id='git-cancel-btn' tone='secondary' size='md' onClick={() => setIsGitModalOpen(false)} disabled={isGitDetailsLoading}>
                    Cancel
                  </DsButton>
                  <DsButton
                    id='git-save-btn'
                    tone='primary'
                    size='md'
                    onClick={handleSaveGitDetails}
                    disabled={!gitDetails.codeRepo || (!gitDetails.ciRepoSameAsCode && !gitDetails.ciRepo) || isGitDetailsLoading}
                  >
                    Save
                  </DsButton>
                </Box>
              ) : (
                <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
                  <DsButton id='git-close-btn' tone='secondary' size='md' onClick={() => setIsGitModalOpen(false)}>
                    Close
                  </DsButton>
                  {String(gitDetails.source) === 'cloud_resource_attributes' && (
                    <DsButton
                      id='git-edit-btn'
                      tone='secondary'
                      size='md'
                      onClick={() => {
                        setIsGitEditMode(true);
                        listGitConfigurations();
                      }}
                    >
                      Edit
                    </DsButton>
                  )}
                  {gitDetails.hash && (
                    <DsButton
                      id='git-view-commit-btn'
                      tone='primary'
                      size='md'
                      onClick={() => window.open(`${gitDetails.codeRepo}/commit/${gitDetails.hash}`, '_blank')}
                    >
                      View Commit
                    </DsButton>
                  )}
                </Box>
              )
            }
          >
            <Box sx={{ p: 2 }}>
              {isGitDetailsLoading ? (
                <Box display='flex' justifyContent='center' p={4}>
                  <CircularProgress />
                </Box>
              ) : isGitEditMode ? (
                // EDIT MODE - Form to enter details
                <>
                  <Typography variant='body2' sx={{ mb: 2, color: 'text.secondary' }}>
                    Configure Git repository details for this workload.
                  </Typography>

                  {/* Section 1: Code Repository (workloads.nudgebee.com/git.repo) */}
                  <Typography variant='subtitle2' sx={{ mb: 1, fontWeight: 600 }}>
                    Source Code Repository
                  </Typography>
                  <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                    The repository containing the actual application source code (used for code analysis)
                  </Typography>

                  {/* Git Integration Selection (GitHub/GitLab if configured) */}
                  {allGitIntegrations.length > 0 && (
                    <Box sx={{ mb: 2 }}>
                      <CustomSelectDropdown
                        value={allGitIntegrations.find((i) => i.key === selectedGitIntegration)?.label || ''}
                        onChange={(e) => {
                          const selected = allGitIntegrations.find((i) => i.label === e.target.value);
                          if (selected) {
                            handleGitIntegrationSelect(selected.key);
                          }
                        }}
                        options={allGitIntegrations.map((i) => i.label)}
                        placeholder='Select a Git integration'
                        isLoading={isGitReposLoading}
                        label='Git Integration'
                      />
                    </Box>
                  )}

                  {/* Repo Selection from Integration */}
                  {selectedGitIntegration && reposFromIntegration.length > 0 && (
                    <Box sx={{ mb: 2 }}>
                      <CustomSelectDropdown
                        value={reposFromIntegration.find((r) => gitDetails.codeRepo === `${r.integrationUrl}/${r.key}`)?.name || ''}
                        onChange={(e) => {
                          const selectedRepo = reposFromIntegration.find((r) => r.name === e.target.value);
                          if (selectedRepo) {
                            handleRepoSelect(selectedRepo.key);
                          }
                        }}
                        options={reposFromIntegration.map((r) => r.name)}
                        placeholder='Select a repository'
                        label='Repository'
                      />
                    </Box>
                  )}

                  {/* Message if integration selected but no repos found */}
                  {selectedGitIntegration && reposFromIntegration.length === 0 && (
                    <Typography variant='body2' color='text.secondary' sx={{ mb: 2 }}>
                      No repositories found for this integration. Enter URL manually below.
                    </Typography>
                  )}

                  {/* Show selected code repo URL or allow manual input */}
                  {gitDetails.codeRepo ? (
                    <Box sx={{ mb: 2 }}>
                      <Box display='flex' alignItems='center' gap={1}>
                        <Typography>{gitDetails.codeRepo}</Typography>
                        <IconButton
                          size='small'
                          onClick={() => {
                            setGitDetails((prev) => ({ ...prev, codeRepo: '' }));
                            setSelectedGitIntegration('');
                            setReposFromIntegration([]);
                          }}
                          title='Clear and enter manually'
                        >
                          <Typography variant='caption' color='primary' sx={{ cursor: 'pointer' }}>
                            Change
                          </Typography>
                        </IconButton>
                      </Box>
                    </Box>
                  ) : (
                    (!selectedGitIntegration || reposFromIntegration.length === 0) && (
                      <Box sx={{ mb: 2 }}>
                        <Input
                          size='sm'
                          label='Code Repository URL'
                          required
                          placeholder='https://github.com/org/repo or https://gitlab.com/group/project'
                          value={gitDetails.codeRepo}
                          onChange={(value) => setGitDetails((prev) => ({ ...prev, codeRepo: value }))}
                          help='e.g., https://github.com/myorg/myrepo or https://gitlab.com/mygroup/myproject'
                        />
                      </Box>
                    )
                  )}

                  <Divider sx={{ my: 2 }} />

                  {/* Section 2: CI/Deployment Repository */}
                  <Typography variant='subtitle2' sx={{ mb: 1, fontWeight: 600 }}>
                    CI/Deployment Repository
                  </Typography>
                  <Typography variant='caption' color='text.secondary' sx={{ mb: 1, display: 'block' }}>
                    Repository containing deployment configs (Helm charts, values files) for PR creation
                  </Typography>

                  <FormControlLabel
                    control={
                      <Switch
                        checked={gitDetails.ciRepoSameAsCode}
                        onChange={(e) => {
                          setGitDetails((prev) => ({ ...prev, ciRepoSameAsCode: e.target.checked, ciRepo: '' }));
                          // Reset CI repo integration state when toggle changes
                          setSelectedCiGitIntegration('');
                          setCiReposFromIntegration([]);
                        }}
                      />
                    }
                    label='CI repo is same as code repo'
                    sx={{ mb: 2 }}
                  />

                  {/* CI Repo selection (only if different from code repo) */}
                  {!gitDetails.ciRepoSameAsCode && (
                    <>
                      {/* CI Repo Integration Selection */}
                      {allGitIntegrations.length > 0 && (
                        <Box sx={{ mb: 2 }}>
                          <CustomSelectDropdown
                            value={allGitIntegrations.find((i) => i.key === selectedCiGitIntegration)?.label || ''}
                            onChange={(e) => {
                              const selected = allGitIntegrations.find((i) => i.label === e.target.value);
                              if (selected) {
                                handleCiGitIntegrationSelect(selected.key);
                              }
                            }}
                            options={allGitIntegrations.map((i) => i.label)}
                            placeholder='Select a Git integration'
                            label='Git Integration'
                          />
                        </Box>
                      )}

                      {/* CI Repo Selection from Integration */}
                      {selectedCiGitIntegration && ciReposFromIntegration.length > 0 && (
                        <Box sx={{ mb: 2 }}>
                          <CustomSelectDropdown
                            value={ciReposFromIntegration.find((r) => gitDetails.ciRepo === `${r.integrationUrl}/${r.key}`)?.name || ''}
                            onChange={(e) => {
                              const selectedRepo = ciReposFromIntegration.find((r) => r.name === e.target.value);
                              if (selectedRepo) {
                                handleCiRepoSelect(selectedRepo.key);
                              }
                            }}
                            options={ciReposFromIntegration.map((r) => r.name)}
                            placeholder='Select a repository'
                            label='CI Repository'
                          />
                        </Box>
                      )}

                      {/* Message if integration selected but no repos found */}
                      {selectedCiGitIntegration && ciReposFromIntegration.length === 0 && (
                        <Typography variant='body2' color='text.secondary' sx={{ mb: 2 }}>
                          No repositories found for this integration. Enter URL manually below.
                        </Typography>
                      )}

                      {/* Show selected CI repo URL or allow manual input */}
                      {gitDetails.ciRepo ? (
                        <Box sx={{ mb: 2 }}>
                          <Box display='flex' alignItems='center' gap={1}>
                            <Typography>{gitDetails.ciRepo}</Typography>
                            <IconButton
                              size='small'
                              onClick={() => {
                                setGitDetails((prev) => ({ ...prev, ciRepo: '' }));
                                setSelectedCiGitIntegration('');
                                setCiReposFromIntegration([]);
                              }}
                              title='Clear and enter manually'
                            >
                              <Typography variant='caption' color='primary' sx={{ cursor: 'pointer' }}>
                                Change
                              </Typography>
                            </IconButton>
                          </Box>
                        </Box>
                      ) : (
                        (!selectedCiGitIntegration || ciReposFromIntegration.length === 0) && (
                          <Box sx={{ mb: 2 }}>
                            <Input
                              size='sm'
                              label='CI Repository URL'
                              required
                              placeholder='https://github.com/org/infra-repo or https://gitlab.com/group/infra'
                              value={gitDetails.ciRepo}
                              onChange={(value) => setGitDetails((prev) => ({ ...prev, ciRepo: value }))}
                              help='Repository containing deployment configurations'
                            />
                          </Box>
                        )
                      )}
                    </>
                  )}

                  <Box sx={{ mb: 2 }}>
                    <Input
                      size='sm'
                      label='Branch'
                      placeholder='main'
                      value={gitDetails.branch}
                      onChange={(value) => setGitDetails((prev) => ({ ...prev, branch: value }))}
                    />
                  </Box>

                  <Box sx={{ mb: 2 }}>
                    <Input
                      size='sm'
                      label='Helm Values File Path'
                      placeholder='deploy/kubernetes/values.yaml'
                      value={gitDetails.valuesFilePath}
                      onChange={(value) => setGitDetails((prev) => ({ ...prev, valuesFilePath: value }))}
                      help='Path to values file for PR creation'
                    />
                  </Box>
                </>
              ) : (
                // VIEW MODE - Show configured details
                <>
                  <Box sx={{ mb: 2 }}>
                    <Typography variant='caption' color='text.secondary'>
                      Source
                    </Typography>
                    <Box>
                      <CustomLabels
                        text={
                          String(gitDetails.source) === 'ci_annotation'
                            ? 'CI Annotation'
                            : String(gitDetails.source) === 'workload_annotation'
                            ? 'Workload Annotation'
                            : 'Manual Configuration'
                        }
                        variant='grey'
                      />
                    </Box>
                  </Box>

                  <Grid container spacing={2}>
                    {/* Code Repository */}
                    <Grid item xs={12}>
                      <Typography variant='caption' color='text.secondary'>
                        Source Code Repository
                      </Typography>
                      <Box display='flex' alignItems='center' gap={1}>
                        <Typography>{gitDetails.codeRepo}</Typography>
                        <IconButton size='small' onClick={() => window.open(gitDetails.codeRepo, '_blank')}>
                          <OpenInNewIcon fontSize='small' />
                        </IconButton>
                      </Box>
                    </Grid>

                    {/* CI Repository (if different) */}
                    {!gitDetails.ciRepoSameAsCode && gitDetails.ciRepo && (
                      <Grid item xs={12}>
                        <Typography variant='caption' color='text.secondary'>
                          CI/Deployment Repository
                        </Typography>
                        <Box display='flex' alignItems='center' gap={1}>
                          <Typography>{gitDetails.ciRepo}</Typography>
                          <IconButton size='small' onClick={() => window.open(gitDetails.ciRepo, '_blank')}>
                            <OpenInNewIcon fontSize='small' />
                          </IconButton>
                        </Box>
                      </Grid>
                    )}

                    <Grid item xs={6}>
                      <Typography variant='caption' color='text.secondary'>
                        Branch
                      </Typography>
                      <Typography>{gitDetails.branch || 'main'}</Typography>
                    </Grid>

                    {gitDetails.hash && (
                      <Grid item xs={6}>
                        <Typography variant='caption' color='text.secondary'>
                          Commit
                        </Typography>
                        <Box display='flex' alignItems='center' gap={1}>
                          <Typography>{gitDetails.hash.substring(0, 8)}</Typography>
                          <IconButton size='small' onClick={() => window.open(`${gitDetails.codeRepo}/commit/${gitDetails.hash}`, '_blank')}>
                            <OpenInNewIcon fontSize='small' />
                          </IconButton>
                        </Box>
                      </Grid>
                    )}

                    {gitDetails.valuesFilePath && (
                      <Grid item xs={12}>
                        <Typography variant='caption' color='text.secondary'>
                          Values File Path
                        </Typography>
                        <Typography>{gitDetails.valuesFilePath}</Typography>
                      </Grid>
                    )}
                  </Grid>
                </>
              )}
            </Box>
          </Modal>

          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2.2fr 0.8fr 1fr 0.7fr',
              gap: '15px',
              m: '15px 0px',
              flexWrap: 'wrap',
              '@media (max-width: 1350px)': {
                gridTemplateColumns: '1.6fr 0.8fr 1.2fr 0.7fr',
                gap: '10px',
              },
            }}
          >
            <Box>
              <SummaryBlock
                hideTitle
                sx={{
                  borderRadius: '4px',
                  minHeight: '50px',
                  backgroundColor: '#ffffff !important',
                  border: '0.5px solid #60A5FA !important',
                  boxShadow: '0px 4px 6px -1px #E5E5E599',
                  '@media (max-width: 1350px)': {
                    padding: '16px 10px',
                  },
                }}
              >
                <Box
                  display={'grid'}
                  gridTemplateColumns={'0.8fr 5px 1fr 1fr'}
                  gap='20px'
                  sx={{
                    '@media (max-width: 1350px)': {
                      gap: '10px',
                    },
                  }}
                >
                  <Box>
                    <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>Total Apps</Typography>
                    <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                      {applicationSummary?.count || '-'}
                    </Typography>
                  </Box>
                  <Divider orientation='vertical' flexItem />

                  <Box>
                    <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                      <Typography sx={{ fontSize: '11px', fontWeight: 400, color: '#9F9F9F' }}>Deployment</Typography>
                      <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                        {applicationSummary?.deployment_count || '-'}
                      </Typography>
                    </Box>
                    <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                      <Typography sx={{ fontSize: '11px', fontWeight: 400, color: '#9F9F9F' }}>Daemonset</Typography>
                      <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                        {applicationSummary?.daemonset_count || '-'}
                      </Typography>
                    </Box>
                    <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                      <Typography sx={{ fontSize: '11px', fontWeight: 400, color: '#9F9F9F' }}>Job</Typography>
                      <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                        {applicationSummary?.job_count || '-'}
                      </Typography>
                    </Box>
                  </Box>
                  <Box>
                    <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                      <Typography sx={{ fontSize: '11px', fontWeight: 400, color: '#9F9F9F' }}>Statefulset</Typography>
                      <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                        {applicationSummary?.statefulset_count || '-'}
                      </Typography>
                    </Box>
                    <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                      <Typography sx={{ fontSize: '11px', fontWeight: 400, color: '#9F9F9F' }}>Rollout</Typography>
                      <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                        {applicationSummary?.rollout_count || '-'}
                      </Typography>
                    </Box>
                    <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                      <Typography sx={{ fontSize: '11px', fontWeight: 400, color: '#9F9F9F' }}>CronJob</Typography>
                      <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                        {applicationSummary?.cronjob_count || '-'}
                      </Typography>
                    </Box>
                  </Box>
                </Box>
              </SummaryBlock>
            </Box>
            <Box>
              <SummaryBlock
                hideTitle
                sx={{
                  borderRadius: '4px',
                  minHeight: '50px',
                  backgroundColor: '#ffffff !important',
                  border: '0.5px solid #FCA5A5 !important',
                  boxShadow: '0px 4px 6px -1px #E5E5E599',
                  '@media (max-width: 1350px)': {
                    padding: '16px 10px',
                  },
                }}
              >
                <Box display={'flex'} gap='12px' justifyContent={'space-between'}>
                  <Box>
                    <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>Errors</Typography>
                    <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                      {applicationSummary?.error_count ?? 0}
                    </Typography>
                  </Box>
                  <Divider orientation='vertical' />
                  <Box>
                    <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>Events</Typography>
                    <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                      {applicationSummary?.event_count ?? 0}
                    </Typography>
                  </Box>
                </Box>
              </SummaryBlock>
            </Box>
            <Box>
              <SummaryBlock
                hideTitle
                sx={{
                  borderRadius: '4px',
                  minHeight: '50px',
                  backgroundColor: '#ffffff !important',
                  border: '0.5px solid #4ADE80 !important',
                  boxShadow: '0px 4px 6px -1px #E5E5E599',
                  '@media (max-width: 1350px)': {
                    padding: '16px 10px',
                  },
                }}
              >
                <Box display={'flex'} gap='12px' justifyContent={'space-between'}>
                  <Box>
                    <Box display='flex' alignItems='center' gap='4px'>
                      <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>Optimization</Typography>
                      <CustomTooltip
                        title={
                          <Box sx={{ fontSize: '12px' }}>
                            <Typography sx={{ fontSize: '12px', fontWeight: 500, mb: 0.5 }}>How is this calculated?</Typography>
                            <Typography sx={{ fontSize: '12px', fontWeight: 400 }}>
                              Total count of Right Sizing, Replica Right Sizing, and Abandoned Resource recommendations.
                            </Typography>
                          </Box>
                        }
                        placement='top'
                        tooltipStyle={{ maxWidth: '260px', padding: '12px' }}
                      >
                        <InfoOutlinedIcon data-testid='optimization-info-icon' sx={{ fontSize: '14px', color: '#9F9F9F', cursor: 'pointer' }} />
                      </CustomTooltip>
                    </Box>
                    <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                      {applicationSummary.recommendation_count}
                    </Typography>
                  </Box>
                  <Box>
                    <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>Savings Potential</Typography>
                    <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                      <Currency
                        value={applicationSummary.estimatedSaving}
                        suffix='/yr'
                        sx={{
                          fontWeight: 500,
                          fontSize: '20px',
                          color: '#374151',
                        }}
                        isSavingPotential={true}
                        recommendationLabel='Some of workload recommendations'
                      />
                    </Typography>
                  </Box>
                </Box>
              </SummaryBlock>
            </Box>
            <Box>
              <SummaryBlock
                hideTitle
                sx={{
                  borderRadius: '4px',
                  minHeight: '50px',
                  backgroundColor: '#ffffff !important',
                  border: '0.5px solid #4ADE80 !important',
                  boxShadow: '0px 4px 6px -1px #E5E5E599',
                  display: 'grid',
                  '@media (max-width: 1350px)': {
                    padding: '16px 10px',
                  },
                }}
              >
                <Box display={'grid'} gridTemplateColumns={'2fr'} gap='12px' alignItems={'center'}>
                  <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'}>
                    <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>MTD Cost</Typography>
                    <Typography variant='h4' sx={{ fontSize: '11px', fontWeight: 500, color: '#374151' }}>
                      <Currency
                        value={applicationSummary.mtd_cost}
                        sx={{
                          fontWeight: 500,
                          fontSize: '14px',
                          color: '#374151',
                        }}
                        sxPrefix={{
                          fontSize: '12px',
                          fontWeight: 400,
                          color: '#9F9F9F',
                        }}
                      />
                    </Typography>
                  </Box>
                </Box>
              </SummaryBlock>
            </Box>
          </Box>
          <KubernetesTable2
            id={kubernetesWorkloadTable}
            headers={WORKLOAD_HEADERS}
            data={data}
            tabPadding={'6px 0px 0px'}
            expandable={{
              tabs: [
                {
                  text: 'Details',
                  value: 0,
                  key: 'WorkloadDetails',
                  componentFn: workloadDetailsFn,
                },
                { text: 'Pods', value: 1, key: 'pods', componentFn: podsWithChartFn },
                { text: 'Utilization Trends', value: 2, key: 'utilization3' },
                { text: 'Cost Trends', value: 3, key: 'cost' },
                { text: 'Recent Events', value: 4, key: 'events' },
                { text: 'Service Map', value: 5, key: 'serviceMap' },
                { text: 'Deployments', value: 6, key: 'deployments' },
                { text: 'Security', value: 7, key: 'security' },
                { text: 'SLO', value: 8, key: 'slo' },
                { text: 'Profilers', value: 9, key: 'profilers' },
                {
                  text: 'Traces',
                  value: 11,
                  key: 'workload-traces',
                  componentFn: function (opt, drilldownQuery, _row) {
                    return (
                      <KubernetesTracesListing
                        showNamespaceFilter={false}
                        showWorkloadFilter={false}
                        destinationNamespace={drilldownQuery.namespaceName}
                        destinationWorkload={drilldownQuery.workloadName}
                        namespace={drilldownQuery.namespaceName}
                        workloadName={drilldownQuery.workloadName}
                        accountId={accountId}
                        passedSelectedTimestamp={{
                          startTimestamp: getYesterday().getTime(),
                          endTimestamp: new Date().getTime(),
                        }}
                        destinationName={''}
                        showTimeFilter={true}
                        httpStatus={''}
                        duration={''}
                        showStatusFilter={false}
                        fromWorkload={true}
                      />
                    );
                  },
                },
                {
                  text: 'Logs',
                  value: 12,
                  key: 'workload-logs',
                  componentFn: function (opt, drilldownQuery, _row) {
                    return (
                      <KubernetesLogs
                        accountId={accountId}
                        showTrend={false}
                        showQueryTextBox={false}
                        dateTime={{
                          startTime: getSpecificTime(60),
                          endTime: new Date().getTime(),
                        }}
                        queryFromProps={`{"namespaceName":"${drilldownQuery.namespaceName}","workloadName":"${drilldownQuery.workloadName}"}`}
                        showPolling={false}
                      />
                    );
                  },
                },
                {
                  text: 'App Dashboard',
                  value: 13,
                  key: 'app-dashboard',
                  componentFn: function (opt, drilldownQuery, _row) {
                    return (
                      <Dashboard accountId={accountId} namespaceName={drilldownQuery.namespaceName} workloadName={drilldownQuery.workloadName} />
                    );
                  },
                },
                {
                  text: 'Recommendations',
                  value: 14,
                  key: 'recommendations',
                  componentFn: function (opt, drilldownQuery, _row) {
                    return (
                      <KubernetesRightSizing
                        kubernetes={{ id: accountId }}
                        namespaceName={drilldownQuery.namespaceName}
                        workloadType={drilldownQuery.workloadType}
                        accountObjectId={`${drilldownQuery.namespaceName}/${drilldownQuery.workloadType}/${drilldownQuery.workloadName}`}
                        enabledFilters={false}
                        enabledSummary={false}
                      />
                    );
                  },
                },
                {
                  text: 'Log Group',
                  value: 15,
                  key: 'log-group-workload',
                  componentFn: function (opt, drilldownQuery, _row) {
                    return (
                      <KubernetesLogsPattern
                        accountId={accountId}
                        workloadName={drilldownQuery.workloadName}
                        workloadNamespace={drilldownQuery.namespaceName}
                      />
                    );
                  },
                },
              ],
            }}
            rowsPerPage={recordsPerPage}
            onPageChange={onPageChange}
            totalRows={totalCount}
            showExpandable
            loading={loading}
            selectedDateRange={selectedDateRange}
            stickyColumnIndex='11'
            pageNumber={currentPage + 1}
            sort={sortObject}
            onSortChange={(e) => {
              sortEventChange(e);
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

function workloadDetailsFn(accountId, drilldownQuery) {
  return (
    <>
      <WorkloadDetails accountId={drilldownQuery.accountId || drilldownQuery?.data?.cloud_account_id} drilldownQuery={drilldownQuery} />
      <LazyLoadComponent
        component={() => import('../dashboard/HttpLatencyTable')}
        props={{ accountId: drilldownQuery.accountId || drilldownQuery?.data?.cloud_account_id, data: drilldownQuery }}
        fallback={<div>Loading latency data...</div>}
      />
    </>
  );
}

function podsWithChartFn(accountId, drilldownQuery) {
  return <PodsWithChart accountId={drilldownQuery.accountId || drilldownQuery?.data?.cloud_account_id} drilldownQuery={drilldownQuery} />;
}

function PodsWithChart({ accountId, drilldownQuery }) {
  const [showReplicaTrend, setShowReplicaTrend] = useState(false);

  return (
    <ListingLayout id='workloadDetails'>
      <ListingLayout.Toolbar
        actions={
          <FormControlLabel
            control={<Switch checked={showReplicaTrend} onChange={(e) => setShowReplicaTrend(e.target.checked)} />}
            label='Show Replica Trend'
          />
        }
      />
      <ListingLayout.Body>
        <>
          {showReplicaTrend && <KubernetesReplicaTrend accountId={accountId} query={drilldownQuery} />}
          <KubernetesPodsTable
            accountId={accountId}
            recordsPerPage={5}
            defaultQuery={drilldownQuery}
            enableFilters={false}
            disableDateFilterForPodsTable={true}
          />
        </>
      </ListingLayout.Body>
    </ListingLayout>
  );
}

function WorkloadDetails({ accountId, drilldownQuery }) {
  const [showYaml, setShowYaml] = useState(false);

  const mapLabels = (label) => {
    const labelArray = [];
    if (label == null || label == undefined || typeof label === 'string') {
      return labelArray;
    }

    for (let [k, v] of Object.entries(label)) {
      let name = k + '=' + v;
      labelArray.push(
        <Box>
          <CustomLabels
            displayTooltip
            textTransform={'none'}
            height='auto'
            margin='0px'
            wordBreak={'break-word'}
            maxWidth='500px'
            key={k}
            text={name}
            variant={'grey'}
            width='max-content'
          />
        </Box>
      );
    }
    return labelArray;
  };

  const MapContainerHeader = ({ containers, _children }) => {
    const containersArray = [];
    if (containers && containers.length > 0) {
      containers?.forEach((item) => {
        containersArray.push(
          <Box key={item.name} sx={{ marginBottom: '10px' }}>
            <AccordionSmall header={item.name}>
              <ContainerDetails containerItem={item} />
            </AccordionSmall>
          </Box>
        );
      });
    }
    return containersArray;
  };

  const MapVolumeHeader = ({ volumes, _children }) => {
    const volumesArray = [];
    volumes?.forEach((item) => {
      volumesArray.push(
        <Box key={item.name} sx={{ marginBottom: '10px' }}>
          <AccordionSmall header={item.name}>
            <VolumeDetails volumeItem={item} />
          </AccordionSmall>
        </Box>
      );
    });
    return volumesArray;
  };

  return (
    <ListingLayout id='workloadDetails'>
      <ListingLayout.Toolbar
        actions={<FormControlLabel control={<Switch checked={showYaml} onChange={(e) => setShowYaml(e.target.checked)} />} label='Show Yaml' />}
      />
      <ListingLayout.Body>
        {showYaml ? (
          <KubernetesPodYaml
            accountId={accountId}
            query={{
              workload_name: drilldownQuery.workloadName,
              namespace_name: drilldownQuery.namespaceName,
              kind: drilldownQuery.subject_kind,
            }}
          />
        ) : (
          <>
            <Grid container sx={{ marginBottom: '8px' }}>
              <Grid item md={3}>
                <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
                  Labels:
                </Typography>
              </Grid>
              <Grid
                item
                md={9}
                sx={{
                  display: 'flex',
                  flexDirection: 'row',
                  flexWrap: 'wrap',
                  gap: '12px',
                  fontFamily: 'Roboto',
                  fontSize: '14px',
                  fontWeight: '500',
                  lineHeight: '20px',
                  color: '#2563EB',
                  maxWidth: '360px',
                }}
              >
                {mapLabels(drilldownQuery?.data?.meta?.config?.labels ?? drilldownQuery?.data?.meta?.job_data?.labels ?? {})}
              </Grid>
            </Grid>
            <Grid container sx={{ marginBottom: '8px' }}>
              <Grid item md={3}>
                <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
                  Annotations:
                </Typography>
              </Grid>
              <Grid
                item
                md={9}
                sx={{
                  display: 'flex',
                  flexDirection: 'row',
                  flexWrap: 'wrap',
                  fontFamily: 'Roboto',
                  gap: '12px',
                  fontSize: '14px',
                  fontWeight: '500',
                  lineHeight: '20px',
                  color: '#2563EB',
                  maxWidth: '360px',
                }}
              >
                {mapLabels(drilldownQuery?.data?.meta?.config?.annotations ?? drilldownQuery?.data?.meta?.job_data?.annotations ?? {})}
              </Grid>
            </Grid>

            <Box marginBottom={'28px'}>
              <Title title={'Containers'} fontSize={'16px'} height={'2px'} />
              <Box sx={{ padding: '20px 20px 0 20px' }}>
                <MapContainerHeader
                  containers={drilldownQuery?.data?.meta?.config?.containers ?? drilldownQuery?.data?.meta?.job_data?.containers ?? []}
                />
              </Box>
            </Box>
            {drilldownQuery?.data?.meta?.config?.volumes.length > 0 && (
              <Box marginBottom={'28px'}>
                <Title title={'Volumes'} fontSize={'16px'} height={'2px'} />
                <Box sx={{ padding: '20px 20px 0 20px' }}>
                  <MapVolumeHeader volumes={drilldownQuery?.data?.meta?.config?.volumes} />
                </Box>
              </Box>
            )}
          </>
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
}

WorkloadDetails.propTypes = {
  accountId: PropTypes.string,
  drilldownQuery: PropTypes.object,
};

KubernetesWorkloadsTable.propTypes = {
  accountId: PropTypes.string,
  resource_ids: PropTypes.arrayOf(PropTypes.string),
};

export default KubernetesWorkloadsTable;
