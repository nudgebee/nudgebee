import { Box, Typography, Tooltip, Tabs, Tab, Avatar, Button, CircularProgress, Divider, Menu, MenuItem, ButtonGroup } from '@mui/material';
import { useRouter } from 'next/router';
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import apiKubernetes from '@api1/kubernetes';
import CollapsableCard from '@components1/common/widgets/CollapsableCard';
import Datetime from '@components1/common/format/Datetime';
import TroubleShootIcon from '@assets/home/node-errors-icon.svg';
import InvestigateDropdown from '@components1/k8s/investigate/InvestigateDropdown';
import { exitCodeMapping, safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketIcon from '@assets/TicketIcon';
import Text from '@components1/common/format/Text';
import { Modal } from '@components1/common/modal';
import WorkflowIcon from '@assets/WorkflowIcon';
import WorkflowTemplatesModal from '@components1/workflow/components/WorkflowTemplatesModal';
import AiGenerateWorkflowModal from '@components1/workflow/components/AiGenerateWorkflowModal';
import apiWorkflow from '@api1/workflow';
import TextWithTooltipAndCopy from '@components1/common/TextWithTooltipAndCopy';
import {
  BarsBlueOutlineIcon,
  ErrorFillIcon,
  FileOutlineIcon,
  GraphOutlineIcon,
  SparklesIconBG,
  WrenchIconOutline,
  infoIcon,
  LastStateIcon,
  ExternalLinkIcon,
} from '@assets';
import { getNubiIconUrl, useTenantBranding, DEFAULT_TITLE } from '@hooks/useTenantBranding';
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import CustomIconButton from '@components1/CustomIconButton';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import NBStatusBadge from '@components1/common/widgets/NBStatusBadge';
import apiRecommendations from '@api1/recommendation';
import apiTriage from '@api1/triage';
import { hasWriteAccess, hasFeatureAccess } from '@lib/auth';
import InvestigateResolution from '@components1/k8s/investigate/InvestigateResolution';
import CustomBorderCard from '@components1/common/CustomBorderCard';
import ArrowForwardRoundedIcon from '@mui/icons-material/ArrowForwardRounded';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import PropTypes from 'prop-types';
import CustomDivider from '@components1/common/CustomDivider';
import CustomLink from '@components1/common/CustomLink';
// K8s card imports
import MemoryAllocationCard from '@components1/k8s/investigate/cards/MemoryAllocationCard';
import NoisyNeighbourCard from '@components1/k8s/investigate/cards/NoisyNeighbourCard';
import LastDeploymentCard from '@components1/k8s/investigate/cards/LastDeploymentCard';
import LogsCard from '@components1/k8s/investigate/cards/LogsCard';
import AskAiCard from '@components1/k8s/investigate/cards/AskAiCard';
import TracesCard from '@components1/k8s/investigate/cards/TracesCard';
import ServiceMapCard from '@components1/k8s/investigate/cards/ServiceMapCard';
import SVGImageCard from '@components1/k8s/investigate/cards/SVGImageCard';
import PrometheusCPUUsageCard from '@components1/k8s/investigate/cards/PrometheusCPUUsageCard';
import ApiFailureCard from '@components1/k8s/investigate/cards/ApiFailureCard';
import PersistentVolumeUsageCard from '@components1/k8s/investigate/cards/PersistentVolumeUsageCard';
import NodeVolumeUsageCard from '@components1/k8s/investigate/cards/NodeVolumeUsageCard';
import SchedulingReport from '@components1/k8s/investigate/cards/SchedulingReport';
import NodeAllocatableResources from '@components1/k8s/investigate/cards/NodeAllocatableResources';
import ClusterMemoryRequestsSummary from '@components1/k8s/investigate/cards/ClusterMemoryRequestsSummary';
import ApplicationMetricsCard from '@components1/k8s/investigate/cards/ApplicationMetricsCard';
import ExpandableText from '@components1/common/ExpandableText';
import DatabaseQueryResponse from '@components1/k8s/investigate/cards/DatabaseQueryResponse';
import CorrespondingEvents from '@components1/k8s/investigate/cards/CorrespondingEvents';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import ConversationLoader from '@common/ConversationLoader';
import { colors } from 'src/utils/colors';
import CustomAction from '@components1/k8s/investigate/cards/CustomAction';
import RCACard from '@components1/k8s/investigate/cards/RCACard';
import { snackbar } from '@components1/common/snackbarService';
import AnomalyCard from '@components1/k8s/investigate/cards/AnomalyCard';
import PrometheusCard from '@components1/k8s/investigate/cards/PrometheusCard';
import SLOConfigReport from '@components1/k8s/investigate/cards/SLOConfigReport';
import apiIntegrations from '@api1/integrations';
import PVCDetails from '@components1/k8s/investigate/cards/PVCDetails';
import CustomButton from '@components1/common/NewCustomButton';
import TextEnricherDynamicCard from '@components1/k8s/investigate/cards/TextEnricherDynamicCard';
import ShowingTableCard from '@components1/k8s/investigate/cards/ShowingTableCard';
import LLMResponseCard from '@components1/k8s/investigate/cards/LLMResponseCard';
import WebhookEventDescription from '@components1/k8s/investigate/cards/WebhookEventDescription';
import KnowledgeGraphCard from '@components1/k8s/investigate/cards/KnowledgeGraphCard';
import DatadogError from '@components1/k8s/investigate/cards/datadog/DatadogError';
import FeedbackComponent from '@components1/common/ThumpsUpAndDown';
import apiAskNudgebee from '@api1/ask-nudgebee';
import SignozDatadogLogCard from '@components1/k8s/investigate/cards/SignozDatadogLogCard';
import apiKubernetes1 from '@api1/kubernetes1';
import { SUBJECT_STATUS, SUBJECT_TYPE, AGGREGATION_KEY, RESOLVABLE_ALERT_KEYS, RCA_STATUS } from '@data/investigateConstants';
import ShowMoreList from '@components1/common/CustomListWithShowMore';
import ExecutePrometheus from '@components1/k8s/investigate/cards/ExecutePrometheus';
import GithubReview from '@components1/k8s/investigate/cards/GithubReview';
import GithubPRHistoryCard from '@components1/k8s/investigate/cards/GithubPRHistoryCard';
import QueryMetricsCard from '@components1/k8s/investigate/cards/QueryMetricsCard';
import ShowingObjectCard from '@components1/k8s/investigate/cards/ShowingObjectCard';
import { useData } from '@context/DataContext';
import homeApi from '@api1/home';
import { transformClusters } from '@components1/common/UpdateDataContext';
import { useRcaPolling } from '@hooks/useRcaPolling';
import { useCardGeneration } from '@hooks/useCardGeneration';
import { IoBookOutline } from 'react-icons/io5';
import KnowledgeBase from '@components1/k8s/investigate/KnowledgeBase';
import { LuChartLine } from 'react-icons/lu';
import { LineChart } from '@components1/common';
import { getDateString } from '@lib/datetime';
import TimelineCard from '@components1/k8s/investigate/cards/TimelineCard';
import UpdateEvent from '@components1/events/UpdateEvent';
import EventClassifyModal from '@components1/events/EventClassifyModal';
import { LiaEditSolid } from 'react-icons/lia';
import { MdOutlineCategory } from 'react-icons/md';
import DatadogMonitorSearch from '@components1/k8s/investigate/cards/DatadogMonitorSearch';
import SafeIcon from '@components1/common/SafeIcon';
// Cloud-specific card imports
import CloudTrailEventCard from '@components1/cloudaccount/investigate/cards/CloudTrailEventCard';
import EventBridgeEventCard from '@components1/cloudaccount/investigate/cards/EventBridgeEventCard';
import SpendTrendChartCard from '@components1/k8s/investigate/cards/SpendTrendChartCard';
import SpendBreakdownChartCard from '@components1/k8s/investigate/cards/SpendBreakdownChartCard';
import KnowledgeBaseCard from '@components1/k8s/investigate/cards/KnowledgeBaseCard';
import CloudWatchAlarmEventCard from '@components1/cloudaccount/investigate/cards/CloudWatchAlarmEventCard';
import CloudWatchLogCard from '@components1/cloudaccount/investigate/cards/CloudWatchLogCard';
import CloudWatchMetricsCard from '@components1/cloudaccount/investigate/cards/CloudWatchMetricsCard';
import PerformanceInsightsCard from '@components1/cloudaccount/investigate/cards/PerformanceInsightsCard';
import ResourceCard from '@components1/cloudaccount/investigate/cards/ResourceCard';
import CloudLog from '@components1/cloudaccount/investigate/cards/CloudLog';
import CloudSsh from '@components1/cloudaccount/investigate/cards/CloudSsh';
import CloudCli from '@components1/cloudaccount/investigate/cards/CloudCli';
import CloudFoundryEvidenceCard, { CF_ACTION_TYPES } from '@components1/cloudaccount/investigate/cards/CloudFoundryEvidenceCard';
import ThresholdSuggestionCard from '@components1/cloudaccount/investigate/cards/ThresholdSuggestionCard';

const k8sAvailableCards = [
  SLOConfigReport,
  ThresholdSuggestionCard,
  MemoryAllocationCard,
  NoisyNeighbourCard,
  LogsCard,
  CorrespondingEvents,
  SVGImageCard,
  PrometheusCPUUsageCard,
  ApiFailureCard,
  PVCDetails,
  PersistentVolumeUsageCard,
  NodeVolumeUsageCard,
  SchedulingReport,
  NodeAllocatableResources,
  ClusterMemoryRequestsSummary,
  ApplicationMetricsCard,
  DatabaseQueryResponse,
  CustomAction,
  DatadogError,
];

const cloudAvailableCards = [
  AskAiCard,
  ThresholdSuggestionCard,
  CloudWatchMetricsCard,
  CloudWatchLogCard,
  CloudTrailEventCard,
  EventBridgeEventCard,
  CloudFoundryEvidenceCard,
  CustomAction,
  PrometheusCard,
  LogsCard,
  KnowledgeBaseCard,
  WebhookEventDescription,
  RCACard,
];

const K8S_TABLE_NAMES = [
  'Job pod events',
  'Job events',
  'Job information',
  'Alert labels',
  'Related Events',
  'related pods',
  'Possible Impacted APIs',
  'DaemonSet events',
  'HPA events',
  'Node events',
  'Node status details',
  'Pod events',
  'Pods running on the node',
  'StatefulSet events',
];

const CLOUD_TABLE_NAMES = [
  'Alert labels',
  'Spend Anomaly Summary',
  'Top Contributors to Spend Change',
  'Top Resources Contributing to Change',
  'Daily Spend Trend',
];

function TabPanel(props) {
  const { children, value, index, ...other } = props;

  return (
    <div role='tabpanel' hidden={value !== index} id={`simple-tabpanel-${index}`} aria-labelledby={`simple-tab-${index}`} {...other}>
      {value === index && <Box sx={{ p: 3 }}>{children}</Box>}
    </div>
  );
}

TabPanel.propTypes = {
  children: PropTypes.node,
  index: PropTypes.number.isRequired,
  value: PropTypes.number.isRequired,
};

function a11yProps(index) {
  return {
    id: `simple-tab-${index}`,
    'aria-controls': `simple-tabpanel-${index}`,
  };
}

const AIOrRcaCard = React.memo(({ option }) => {
  const ContentComponent = option?.getContentComponents?.()?.[0] ?? null;
  if (!ContentComponent) return null;
  return (
    <Box
      sx={{
        mb: 2,
      }}
    >
      <ContentComponent />
    </Box>
  );
});

AIOrRcaCard.displayName = 'AIOrRcaCard';

AIOrRcaCard.propTypes = {
  option: PropTypes.object,
};

// O(n log n) sort using Set.has() (O(1)) instead of Array.includes() (O(n)),
// and Schwartzian transform to instantiate each card class once instead of O(n log n) times.
function sortAvailableCards(cards, criticalCards, highCards, infoCards) {
  const toId = (c) => (typeof c === 'string' ? c : c.id);
  const criticalSet = new Set(criticalCards.map(toId));
  const highSet = new Set(highCards.map(toId));
  const infoSet = new Set(infoCards.map(toId));

  const getPriority = (cardId) => {
    if (criticalSet.has(cardId)) return 3;
    if (highSet.has(cardId)) return 2;
    if (infoSet.has(cardId)) return 1;
    return 0;
  };

  const prioritized = cards.map((C) => ({ C, priority: getPriority(new C().id) }));
  prioritized.sort((a, b) => b.priority - a.priority);
  return prioritized.map(({ C }) => C);
}

const Investigate = () => {
  const router = useRouter();
  const { id, autoInvestigate: k8sAutoInvestigate = 'false' } = router.query;
  const { startGeneration, cancelGeneration } = useCardGeneration();
  const isMountedRef = useRef(true);
  const pendingTimeoutsRef = useRef([]);
  const { selectedCluster, allCluster, setAllCluster } = useData();
  const { assistantName } = useTenantBranding();

  // Track timeouts for cleanup
  const trackTimeout = useCallback((fn, delay) => {
    const id = setTimeout(fn, delay);
    pendingTimeoutsRef.current.push(id);
    return id;
  }, []);

  // Cleanup all tracked timeouts and mark unmounted
  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
      pendingTimeoutsRef.current.forEach((id) => clearTimeout(id));
      pendingTimeoutsRef.current = [];
    };
  }, []);

  const handleRcaStatusChange = useCallback((response, status) => {
    // Mutate the card in-place (arrow functions bind `this` at construction,
    // so cloning doesn't help) and return a new array so React sees the change.
    setMatchedOptions((prev) => {
      const card = prev.find((o) => o.id === 'RCACard');
      if (!card) return prev;

      if (status === RCA_STATUS.COMPLETED) {
        card.rcaData = {
          file_details: {},
          status: response.status,
          summary: response.summary,
          analysis: response.analysis,
        };
        card.insightData = card.insightData.filter((i) => i.message !== 'RCA is underway — check back shortly for results');
        card.insightData.push({ message: 'RCA report is ready', severity: 'Info' });
      } else if (status === RCA_STATUS.FAILED) {
        card.rcaData = { status: response.status };
        card.insightData = card.insightData.filter((i) => i.message !== 'RCA is underway — check back shortly for results');
        card.insightData.push({ message: 'RCA hit a snag — try re-triggering or check back later', severity: 'Error' });
      }

      card.refreshRenderId = (card.refreshRenderId || 0) + 1;
      return [...prev];
    });

    if (status === RCA_STATUS.COMPLETED) {
      snackbar.success('Root cause analysis is ready.');
    } else if (status === RCA_STATUS.FAILED) {
      snackbar.error('Root cause analysis failed. Try re-triggering it.');
    }
  }, []);
  const {
    isPolling: isRcaPolling,
    startPolling: startRcaPolling,
    stopPolling: stopRcaPolling,
  } = useRcaPolling(id, router.query.accountId, handleRcaStatusChange);

  // Ensure allCluster is loaded for source detection (ClusterDropdown may not be rendered)
  useEffect(() => {
    if (!allCluster || allCluster.length === 0) {
      homeApi.getCloudAccounts().then((res) => {
        if (isMountedRef.current) setAllCluster(transformClusters(res));
      });
    }
  }, []);

  // Determine source from account's cloud_provider
  // K8s clusters have cloud_provider === 'K8s', everything else is cloud
  const source = useMemo(() => {
    // Primary: derive from selectedCluster's cloud_provider
    if (selectedCluster?.cloud_provider) {
      return selectedCluster.cloud_provider.toUpperCase() === 'K8S' ? 'kubernetes' : 'cloud';
    }
    // Secondary: look up accountId in allCluster list
    const accountId = router.query.accountId;
    if (accountId && allCluster?.length > 0) {
      const account = allCluster.find((c) => c.value === accountId);
      if (account?.cloud_provider) {
        return account.cloud_provider.toUpperCase() === 'K8S' ? 'kubernetes' : 'cloud';
      }
    }
    // Fallback: query param for backward compat (redirects, external links)
    if (router.query.source === 'cloud' || router.query.source === 'cloud-account') return 'cloud';
    if (router.query.source === 'kubernetes') return 'kubernetes';
    return 'kubernetes';
  }, [selectedCluster?.cloud_provider, allCluster, router.query.accountId, router.query.source]);

  const isK8s = source === 'kubernetes';
  const isCloud = source === 'cloud';

  const [row, setRow] = useState({});
  const [queryParam, setQueryParam] = useState({
    aggregationKey: '',
    serviceKey: '',
    id: '',
    start_at: '',
  });
  const [othersData, setOthersData] = useState([]);
  const [insightData, setInsightData] = useState([]);
  const [podDetails, setPodDetails] = useState({});
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [matchedOptions, setMatchedOptions] = useState([]);
  const [currentInvestigation, setCurrentInvestigation] = useState(null);
  const [isTroubleshootFormOpen, setIsTroubleshootFormOpen] = useState(false);
  const [isRenderInvestigationCard, setIsRenderInvestigationCard] = useState(false);
  const [openCardIndex, setOpenCardIndex] = useState(-1);
  const [collapsedObj, setCollapsedObj] = useState(Array.from({ length: matchedOptions?.length }, (_, index) => [index, false]));
  const [generateRcaVisible, setGenerateRcaVisible] = useState(false);
  const [ticketData, setTicketData] = useState({ id: '', url: '' });
  const [hasRcaFeatureAccess, setHasRcaFeatureAccess] = useState(false);
  const [sentFeedback, setSentFeedback] = useState({});
  const [alertLabels, setAlertLabels] = useState({});
  const [showAll, setShowAll] = useState(false);
  const [tabValue, setTabValue] = useState(1);
  const [openResolveComponentId, setOpenResolveComponentId] = useState(null);
  const [isGeneratingCards, setIsGeneratingCards] = useState(false);
  const [alertRules, setAlertRules] = useState([]);
  // K8s-only state
  const [showDemoMessage, setShowDemoMessage] = useState(false);
  const [openKB, setOpenKB] = useState(false);
  const [showTrendChart, setShowTrendChart] = useState(false);
  const [trendChartData, setTrendChartData] = useState({ data: [], labels: [] });
  const [isTrendChartLoading, setIsTrendChartLoading] = useState(false);
  const [isUpdateEvent, setIsUpdateEvent] = useState(false);
  const [isClassifyModalOpen, setIsClassifyModalOpen] = useState(false);
  const [recurrenceInfo, setRecurrenceInfo] = useState(null);
  const [eventResolutions, setEventResolutions] = useState([]);
  const [knowledgeBase, setKnowledgeBase] = useState([]);
  const [kbLoading, setKbLoading] = useState(false);
  const [anchorElRefresh, setAnchorElRefresh] = useState(null);
  const [showTemplatesModal, setShowTemplatesModal] = useState(false);
  const [showAiGenerateModal, setShowAiGenerateModal] = useState(false);
  const [aiGenerateLoading, setAiGenerateLoading] = useState(false);

  // Build event context for workflow template pre-fill
  const eventContextForTemplate = useMemo(() => {
    if (!row?.source) return {};
    const ctx = {};
    const labels = row.labels || {};
    const accountId = router.query.accountId;
    if (accountId) {
      ctx.account_id = accountId;
    }
    const src = row.source;
    if (src === 'prometheus' || src === 'kubernetes_api_server') {
      // K8s events
      if (row.subject_namespace) {
        ctx.namespace = row.subject_namespace;
      }
      if (row.subject_owner) {
        ctx.deployment = row.subject_owner;
      }
      if (row.subject_node) {
        ctx.node_name = row.subject_node;
      }
      if (labels.container) {
        ctx.container = labels.container;
      }
      if (row.subject_type === 'job' && row.subject_name) {
        ctx.job_name = row.subject_name;
      }
      if (row.subject_type === 'persistentvolumeclaim' && row.subject_name) {
        ctx.pvc_name = row.subject_name;
      }
    } else if (src === 'AWS_CloudWatch_Alarm' || src === 'AWS_EventBridge') {
      if (row.subject_name) {
        ctx.instance_id = row.subject_name;
        ctx.db_instance_id = row.subject_name;
        ctx.asg_name = row.subject_name;
      }
      ctx.region = labels.aws_region || row.subject_node || '';
    } else if (src === 'Azure_Monitor_Alert' || src === 'azure_monitor_webhook') {
      if (labels.resource_name) {
        ctx.vm_name = labels.resource_name;
        ctx.app_name = labels.resource_name;
        ctx.disk_name = labels.resource_name;
      }
      if (row.subject_node) {
        ctx.resource_group = row.subject_node;
      }
      ctx.region = labels.resource_region || '';
    } else if (src === 'GCP_Metric_Alert' || src === 'gcp_monitoring_webhook') {
      ctx.project = labels.gcp_project_id || '';
      ctx.zone = labels.resource_location || '';
      ctx.region = labels.gcp_region || '';
      if (labels.resource_name) {
        ctx.instance_name = labels.resource_name;
        ctx.disk_name = labels.resource_name;
      }
    }

    // Generic fallback: pick up common fields from labels/row for forwarded alerts
    // (e.g. PagerDuty, OpsGenie webhooks forwarding K8s or cloud alerts)
    if (!ctx.namespace && (row.subject_namespace || labels.namespace)) {
      ctx.namespace = row.subject_namespace || labels.namespace;
    }
    if (!ctx.deployment && (row.subject_owner || labels.deployment)) {
      ctx.deployment = row.subject_owner || labels.deployment;
    }
    if (!ctx.node_name && (labels.node || row.subject_node)) {
      ctx.node_name = labels.node || row.subject_node;
    }
    if (!ctx.container && labels.container) {
      ctx.container = labels.container;
    }
    if (!ctx.pvc_name && labels.persistentvolumeclaim) {
      ctx.pvc_name = labels.persistentvolumeclaim;
    }
    if (!ctx.job_name && labels.job_name) {
      ctx.job_name = labels.job_name;
    }

    // Include the event ID so the workflow trigger can link back to this event
    if (id) {
      ctx._event_id = id;
    }

    return ctx;
  }, [row, router.query.accountId, id]);

  const templateAlertName = useMemo(() => {
    if (!row?.source) return '';
    return row.labels?.alertname || row.aggregation_key || '';
  }, [row]);

  const handleAiGenerateWorkflow = useCallback(
    async (query) => {
      const accountId = router.query.accountId;
      if (!accountId || !query.trim()) {
        return;
      }
      setAiGenerateLoading(true);
      try {
        const response = await apiWorkflow.aiGenerateWorkflow(accountId, query);
        const aiData = response?.data?.ai_generate_workflow?.data;
        if (aiData?.response && aiData.response.length > 0) {
          sessionStorage.setItem('aiGeneratedWorkflow', aiData.response[0]);
          if (aiData.conversation_id) {
            sessionStorage.setItem('aiConversationId', aiData.conversation_id);
          }
          if (aiData.session_id) {
            sessionStorage.setItem('aiSessionId', aiData.session_id);
          }
          sessionStorage.setItem('aiInitialQuery', query);
          router.push(`/workflow/new?accountId=${accountId}&loadFromAI=true`);
          setShowAiGenerateModal(false);
        } else {
          snackbar.error('No automation data returned from AI');
        }
      } catch (error) {
        console.error('Error generating workflow:', error);
        snackbar.error('Failed to generate automation');
      } finally {
        setAiGenerateLoading(false);
      }
    },
    [router]
  );

  const handleGenerateWorkflowAsync = useCallback(
    async (query) => {
      const accountId = router.query.accountId;
      if (!accountId || !query.trim()) {
        return null;
      }
      try {
        const response = await apiWorkflow.aiGenerateWorkflow(accountId, query, undefined, undefined, undefined, true);
        const aiData = response?.data?.ai_generate_workflow?.data;
        if (aiData?.session_id) {
          sessionStorage.setItem('aiInitialQuery', query);
          return { sessionId: aiData.session_id, conversationId: aiData.conversation_id || '' };
        }
        return null;
      } catch (error) {
        console.error('Error starting async workflow generation:', error);
        return null;
      }
    },
    [router]
  );

  const handlePollWorkflowConversation = useCallback(
    async (sessionId) => {
      const accountId = router.query.accountId;
      if (!accountId) {
        return null;
      }
      try {
        const response = await apiAskNudgebee.getLlmConversation({ sessionId, accountId });
        const conversation = response?.data?.data?.llm_conversations?.[0];
        if (!conversation) {
          return null;
        }
        const status = conversation.status;
        const messages = conversation.llm_conversation_messages || [];
        const reversedMessages = [...messages].reverse();
        const lastGenMsg = reversedMessages.find((m) => m.message_type === 'generation');
        const lastFollowupMsg = reversedMessages.find((m) => m.message_type === 'followup');

        if (status === 'COMPLETED') {
          let workflowJson = '';
          const followupResponse = (lastFollowupMsg?.response || '').trimStart();
          if (followupResponse.startsWith('{')) {
            workflowJson = lastFollowupMsg?.response || '';
          }
          if (!workflowJson) {
            const genResponse = (lastGenMsg?.response || '').trimStart();
            if (genResponse.startsWith('{')) {
              workflowJson = lastGenMsg?.response || '';
            }
          }
          if (!workflowJson) {
            workflowJson = lastGenMsg?.response || '';
          }
          return { status: 'COMPLETED', workflowJson, conversationId: conversation.id };
        }

        if (status === 'FAILED') {
          return { status: 'FAILED', errorMessage: lastGenMsg?.response || 'Automation generation failed. Please try again.' };
        }

        if (status !== 'WAITING') {
          return null;
        }

        // WAITING: read the question from message_config.question (canonical
        // location, always populated). Fall back to followup message.message,
        // then legacy followup.response. llm-server #29309 stopped writing the
        // question to followup.response — relying on response alone leaves the
        // modal stuck on "Generating...".
        const agents = lastGenMsg?.llm_conversation_agents || [];
        const lastAgent = agents[agents.length - 1];
        let planOptions;
        let followupType;
        let followupData;
        let configQuestion = '';
        let agentId = lastAgent?.id;

        const rawConfig = lastFollowupMsg?.message_config;
        if (rawConfig) {
          try {
            const config = typeof rawConfig === 'string' ? JSON.parse(rawConfig) : rawConfig;
            planOptions = config.followupOptions;
            followupType = config.followupType;
            followupData = config.followupData;
            configQuestion = typeof config.question === 'string' ? config.question : '';
            if (config.agentId) {
              agentId = config.agentId;
            }
          } catch {
            // ignore parse errors
          }
        }

        const followupResponse = lastFollowupMsg?.response || '';
        const followupMessage = lastFollowupMsg?.message || '';
        const genResponse = lastGenMsg?.response || '';
        let planText;
        if (configQuestion) {
          planText = configQuestion;
        } else if (followupMessage) {
          planText = followupMessage;
        } else if (followupResponse && !followupResponse.trimStart().startsWith('{')) {
          planText = followupResponse;
        } else {
          planText = genResponse;
        }
        planText = planText.replace(/^Here's my plan for building your workflow:\s*/i, '');
        planText = planText.replace(/\s*Would you like to approve this plan or request changes\?\s*$/i, '');

        return {
          status: 'WAITING',
          planText,
          planOptions,
          followupType,
          followupData,
          conversationId: conversation.id,
          messageId: lastFollowupMsg?.id,
          messageUpdatedAt: lastFollowupMsg?.updated_at,
          agentId,
        };
      } catch (error) {
        console.error('Error polling workflow conversation:', error);
        return null;
      }
    },
    [router]
  );

  const handleApproveOrRespondWorkflow = useCallback(
    async (query, conversationId, sessionId, messageId, agentId) => {
      const accountId = router.query.accountId;
      if (!accountId) {
        return;
      }
      await apiWorkflow.aiGenerateWorkflow(accountId, query, conversationId, sessionId, undefined, true, messageId, agentId);
    },
    [router]
  );

  const handleWorkflowCompleted = useCallback(
    (workflowJson, _conversationId, sessionId) => {
      const accountId = router.query.accountId;
      sessionStorage.setItem('aiGeneratedWorkflow', workflowJson);
      sessionStorage.setItem('aiSessionId', sessionId);
      router.push(`/workflow/new?accountId=${accountId}&loadFromAI=true`);
      setShowAiGenerateModal(false);
    },
    [router]
  );

  const fitCustomLabelStyles = useMemo(
    () => ({
      width: 'auto',
      maxWidth: '100%',
      minWidth: 0,
      justifySelf: 'start',
      whiteSpace: 'normal',
      overflowWrap: 'anywhere',
      padding: '4px 10px',
      boxSizing: 'border-box',
    }),
    []
  );

  const handleOpenKB = async () => {
    if (!row?.aggregation_key) {
      snackbar.info('No data available!');
      return;
    }
    setKbLoading(true);
    try {
      const res = await apiKubernetes.getKnowledgeBase(row.aggregation_key);
      if (res?.data && res.data.length > 0) {
        setKnowledgeBase(res.data);
        setOpenKB(true);
      } else {
        snackbar.info('No data Available!');
      }
    } catch (err) {
      console.error('Failed to load knowledge base', err);
      snackbar.error('Failed to fetch data!');
    } finally {
      setKbLoading(false);
    }
  };

  const handleCloseKB = () => {
    setOpenKB(false);
    setKnowledgeBase([]);
  };

  const handleTabChange = (_event, newValue) => {
    setOpenCardIndex((prevOpenCardIndex) => {
      if (prevOpenCardIndex !== -1) {
        setCollapsedObj((prevCollapsedObj) => ({
          ...prevCollapsedObj,
          [prevOpenCardIndex]: false,
        }));
      }
      return -1;
    });
    setTabValue(Number(newValue));
  };

  // K8s: show demo message based on cluster attrs
  useEffect(() => {
    if (isK8s) {
      const cloudAccountAttrs = Array.isArray(selectedCluster?.cloud_account_attrs) ? selectedCluster.cloud_account_attrs : [];
      const findShowInvestigationButton = cloudAccountAttrs.find((g) => g.name == 'show_investigation_button');
      setShowDemoMessage(findShowInvestigationButton?.value == 'true' && k8sAutoInvestigate === 'false');
    } else {
      // Cloud: always show investigation cards directly
      setShowDemoMessage(false);
    }
  }, [selectedCluster, isK8s]);

  useEffect(() => {
    if (currentInvestigation) {
      setTabValue(1);
    } else {
      const timer = setTimeout(() => {
        setTabValue(0);
      }, 2000);
      return () => clearTimeout(timer);
    }
  }, [currentInvestigation]);

  useEffect(() => {
    let cancelled = false;
    setAlertRules([]);
    apiKubernetes1
      .getAllEventRuleNames({
        accountId: router.query.accountId,
      })
      .then((res) => {
        if (!cancelled && isMountedRef.current) setAlertRules(res?.data?.event_rules?.map((d) => d.alert) || []);
      });
    return () => {
      cancelled = true;
    };
  }, [router.query.accountId]);

  const handleCardDataUpdate = useCallback(
    async (updatedCard) => {
      setMatchedOptions((prevOptions) => prevOptions.map((option) => (option.id === updatedCard.id ? updatedCard : option)));

      if (isK8s) {
        const card = new GithubReview(updatedCard, row);
        if (await card.canRenderContent()) {
          setMatchedOptions((prevOptions) => {
            const exists = prevOptions.some((option) => option.id === card.id);
            if (exists) {
              return prevOptions.map((option) => (option.id === card.id ? card : option));
            }
            return [...prevOptions, card];
          });
        }
      }
    },
    [isK8s, row]
  );

  const removeCardFromMatched = useCallback((cardToRemove) => {
    setMatchedOptions((prevOptions) => prevOptions.filter((card) => card.id !== cardToRemove.id));
  }, []);

  async function loadData(eventId) {
    setLoading(true);
    let response;
    try {
      response = await apiKubernetes.resolveEventRecord(eventId, router.query.accountId);
    } catch (error) {
      console.error('Failed to load investigation data', error);
      snackbar.error('Failed to load investigation data.');
      if (loadedEventIdRef.current === eventId) {
        // only reset if this call set it
        loadedEventIdRef.current = null;
      }
      return;
    } finally {
      setLoading(false);
    }
    const result = response?.data?.events;
    let data = result?.[0] ?? {};
    if (Array.isArray(data.evidences)) {
      data.evidences = data.evidences.filter((item) => item !== null);
    }

    // Cloud: also set labels from data.labels directly
    if (isCloud && data.labels) {
      setAlertLabels(data.labels);
    }

    const processAlertLabels = (evidence) => {
      if (evidence.type === 'table' && evidence.data?.table_name === '*Alert labels*') {
        setAlertLabels(Object.fromEntries(evidence.data.rows));
        if (data.subject_name === SUBJECT_STATUS.UNRESOLVED) {
          for (const row of evidence.data.rows) {
            if (row[0] === SUBJECT_TYPE.POD) {
              data.subject_name = row[1];
              data.subject_type = SUBJECT_TYPE.POD;
              break;
            }
          }
        }
      }
    };

    // Process all evidences
    const processedInsights = [];
    for (const e of data.evidences ?? []) {
      if (isCloud && data.labels) {
        // Cloud: skip processAlertLabels if data.labels already set
      } else {
        processAlertLabels(e);
      }
      // Cloud: extract insights during load
      if (isCloud && e.insight && e.insight.length > 0) {
        processedInsights.push(...e.insight);
      }
    }

    if (isCloud && processedInsights.length > 0) {
      setInsightData(processedInsights);
    }

    setRow(data);

    if (result) {
      setQueryParam({
        aggregationKey: result[0]?.aggregation_key,
        serviceKey: result[0]?.service_key,
        id: result[0]?.id,
        start_at: result[0]?.starts_at,
      });
      const autoInvestigateHash = k8sAutoInvestigate === 'true' ? `&autoInvestigate=${k8sAutoInvestigate}` : '';
      router.push(`/investigate?id=${eventId}&accountId=${result[0]?.cloud_account_id}${autoInvestigateHash}`, undefined, { shallow: true });
    }
  }

  const loadedEventIdRef = useRef(null);

  useEffect(() => {
    if (!id || !allCluster || allCluster.length === 0) return;

    // Skip reload if we already loaded this event (URL rewrite just added accountId)
    if (String(loadedEventIdRef.current) === String(id) && router.query.accountId) return;
    loadedEventIdRef.current = id;

    let cancelled = false;
    resetState();
    loadData(id);
    apiTriage.getRecurrenceInfo(id).then((info) => {
      if (!cancelled && isMountedRef.current) setRecurrenceInfo(info);
    });
    apiRecommendations.listEventResolutions(id).then((resolutions) => {
      if (!cancelled && isMountedRef.current) setEventResolutions(resolutions);
    });
    return () => {
      cancelled = true;
    };
  }, [id, k8sAutoInvestigate, router.query.accountId, allCluster?.length]);

  // Show/hide investigation cards based on demo message
  useEffect(() => {
    setIsRenderInvestigationCard(!showDemoMessage);
  }, [showDemoMessage]);

  useEffect(() => {
    let cancelled = false;
    if (row.id) {
      apiAskNudgebee
        .getFeedbackForSessionId({
          account_id: row.cloud_account_id || router.query.accountId,
          session_id: row.id,
        })
        .then((res) => {
          if (cancelled || !isMountedRef.current) return;
          const response = res?.data?.data?.llm_conversation_feedback_v2?.rows ?? [];
          if (response.length == 1) {
            setSentFeedback({
              submitted: true,
              isPositive: response[0].useful ?? null,
              message: response[0].additional_notes ?? '',
            });
          }
        });

      if (row?.fingerprint) {
        getTicketData();
      }
    }
    return () => {
      cancelled = true;
    };
  }, [row.id]);

  function getTicketData() {
    apiIntegrations.getTicketByReferenceId(row?.fingerprint).then((response) => {
      if (!isMountedRef.current) return;
      const jiraData = response?.data || [];
      if (jiraData.length > 0) {
        setTicketData({ ticket_id: response.data[0]?.ticket_id, url: response.data[0]?.url });
      } else {
        setTicketData({ ticket_id: '', url: '' });
      }
    });
  }

  useEffect(() => {
    const { isCancelled: isGenCancelled, trackTimeout, safeSetState, safeDelay } = startGeneration();
    async function renderEvidenceCardAndRecommendations() {
      setIsGeneratingCards(true);
      const result = row?.evidences.filter((g) => g != null) || [];
      const criticalCards = [];
      const highCards = [];
      const infoCards = [];

      const dynamicCards = isCloud ? [] : [new AskAiCard(), new RCACard()];

      // K8s: Prometheus event graph
      if (isK8s && row.source === 'prometheus' && row?.labels?.alertname) {
        const response = await apiKubernetes1.getEventRules(
          {
            accountId: row.cloud_account_id,
            source: 'prometheus',
            searchByName: row.labels.alertname,
          },
          1,
          0
        );
        const query = response?.data?.event_rules?.[0]?.expr;
        if (query) {
          const card = new ExecutePrometheus(
            {
              additional_info: { title: 'Event Graph (Prometheus Graph)' },
              data: [{ query: query }],
            },
            row
          );
          if (await card.canRenderContent()) {
            dynamicCards.push(card);
          }
        }
      }

      const card = new TimelineCard(row);
      if (await card.canRenderContent()) {
        dynamicCards.push(card);
      }

      // Cloud: severity-based evidence processing
      if (isCloud) {
        const criticalEvidence = result.filter((evidence) => evidence.insight?.some((insight) => insight.severity === 'Critical'));
        const highEvidence = result.filter((evidence) => evidence.insight?.some((insight) => insight.severity === 'High'));
        const infoEvidence = result.filter((evidence) => evidence.insight?.some((insight) => insight.severity === 'Info'));

        function processEvidence(evidenceList, cardArray) {
          for (const d of evidenceList) {
            if (d.type === 'json' && d.data) {
              try {
                const jsonData = JSON.parse(d.data);
                switch (jsonData.name) {
                  case 'container_metric':
                  case 'node_metric':
                  case 'pod_metric':
                  case 'node_request_memory_metric':
                    cardArray.push('MemoryAllocationCard');
                    break;
                  case 'noisy_neighbours':
                    cardArray.push('NoisyNeighbourCard');
                    break;
                  case 'api_traces_enricher':
                    cardArray.push('TracesCard');
                    break;
                }
              } catch (err) {
                console.error('JSON parse error:', err);
              }
            } else if (d.type === 'diff') {
              cardArray.push('LastDeploymentCard');
            }
          }
        }

        processEvidence(criticalEvidence, criticalCards);
        processEvidence(highEvidence, highCards);
        processEvidence(infoEvidence, infoCards);
      }

      for (let i = 0; i < result.length; i++) {
        if (isGenCancelled()) break;
        const d = result[i];
        const actionType =
          d?.additional_info?.actual_action_name || d?.additional_info?.action_name || d?.additional_info?.type || d.type || d.source;
        const isCritical = d.insight?.some((insight) => insight.severity === 'Critical');
        const isHigh = d.insight?.some((insight) => insight.severity === 'High');
        const isInfo = d.insight?.some((insight) => insight.severity === 'Info');

        // Helper to push card and track severity (for cloud sorting)
        const pushCard = (card) => {
          dynamicCards.push(card);
          if (isCloud) {
            if (isCritical) criticalCards.push(card);
            else if (isHigh) highCards.push(card);
            else if (isInfo) infoCards.push(card);
          }
        };

        // Shared action types
        if (actionType === 'text_enricher') {
          const card = new TextEnricherDynamicCard(d, i);
          if (await card.canRenderContent()) pushCard(card);
        }
        if (actionType === 'nubi_enricher') {
          const card = new LLMResponseCard(d, row);
          if (await card.canRenderContent()) pushCard(card);
        }
        if (actionType == 'prometheus_enricher' || d.type == 'prometheus') {
          const card = new PrometheusCard(d, row);
          if (await card.canRenderContent()) pushCard(card);
        }
        if (actionType == 'webhook_event') {
          const card = new WebhookEventDescription(d, row, i);
          if (await card.canRenderContent()) pushCard(card);
        }

        if (!isK8s && (actionType == 'datadog_logs' || actionType == 'logs')) {
          const card = new SignozDatadogLogCard(d, row);
          if (await card.canRenderContent()) dynamicCards.push(card);
        }
        if (actionType == 'datadog_metrics_details' || actionType == 'metrics') {
          const card = new QueryMetricsCard('', d, row);
          if (await card.canRenderContent()) dynamicCards.push(card);
        }
        if (actionType == 'datadog_monitors_search') {
          const card = new DatadogMonitorSearch(d, i);
          if (await card.canRenderContent()) dynamicCards.push(card);
        }
        // K8s-only action types
        if (isK8s) {
          if (actionType == 'signoz_logs_enricher' || actionType == 'datadog_logs' || actionType == 'logs' || actionType == 'dynatrace_logs') {
            const card = new SignozDatadogLogCard(d, row);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          if (actionType == 'application_performance_metrics') {
            const card = new ExecutePrometheus(d, row);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          const hasAnomaly = d?.is_anomaly;
          if (actionType == 'metric_anomaly_enricher' || hasAnomaly) {
            const card = new AnomalyCard(d, row);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          if (actionType == 'argocd_app_history') {
            const card = new ShowingObjectCard(d, row, i);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          if (actionType == 'github_pr_history') {
            const card = new GithubPRHistoryCard(d, row, i);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          if (actionType == 'diff' || actionType == 'deployment_history') {
            const card = new LastDeploymentCard(d, row, i);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          if (
            actionType == 'api_traces_enricher' ||
            actionType == 'api_traces_enricher_v2' ||
            actionType == 'datadog_traces' ||
            actionType == 'traces' ||
            actionType == 'chronosphere_traces_enricher'
          ) {
            const card = new TracesCard(d, row, i);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          let serviceMap = false;
          if (d.data) {
            const jsonParsed = safeJSONParse(d.data);
            serviceMap = jsonParsed?.name === 'service_map';
          }
          if (actionType == 'service_map' || serviceMap) {
            const card = new ServiceMapCard(d, row, i);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
          if (actionType === 'knowledge_graph' || actionType === 'knowledge_graph_service_map') {
            const card = new KnowledgeGraphCard(d, row, i);
            if (await card.canRenderContent()) dynamicCards.push(card);
          }
        }

        // Cloud-only action types
        if (isCloud) {
          if (actionType == 'aws_get_resource' || actionType == 'cloud_resource' || actionType == 'cloud_resources') {
            const card = new ResourceCard(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (actionType == 'cloud_logs') {
            const card = new CloudLog(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (actionType == 'ssh') {
            const card = new CloudSsh(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (actionType == 'cloud_cli') {
            const card = new CloudCli(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          const isCloudWatchAlarm = row?.source == 'AWS_CloudWatch_Alarm';
          if (isCloudWatchAlarm && d?.additional_info?.source == 'Event.Raw') {
            const card = new CloudWatchAlarmEventCard(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (actionType == 'cloud_service_map' || d.type == 'service_map' || d.format == 'service_map') {
            const card = new ServiceMapCard(d, row, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (actionType == 'cloud_performance_insights') {
            const card = new PerformanceInsightsCard(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (CF_ACTION_TYPES.includes(d?.additional_info?.action_type)) {
            const card = new CloudFoundryEvidenceCard(d, i);
            if (await card.canRenderContent()) pushCard(card);
          }
          if (actionType === 'proxy_db_query') {
            const wrapped = { ...d, data: { headers: d.headers, rows: d.rows, table_name: d.additional_info?.title || actionType } };
            const card = new ShowingTableCard(wrapped, i);
            if (await card.canRenderContent()) pushCard(card);
          } else if (actionType === 'proxy_ssh_command' || actionType === 'proxy_http_request') {
            const card = new ShowingObjectCard(d, row, i);
            if (await card.canRenderContent()) pushCard(card);
          }
        }

        // Table name handling
        const tableName = d?.data?.table_name || '';
        const tableNames = isK8s ? K8S_TABLE_NAMES : CLOUD_TABLE_NAMES;
        if (tableNames.some((name) => tableName.includes(name))) {
          let card;
          if (isCloud && tableName.includes('Daily Spend Trend')) {
            card = new SpendTrendChartCard(d, i);
          } else if (isCloud && (tableName.includes('Top Contributors') || tableName.includes('Top Resources'))) {
            card = new SpendBreakdownChartCard(d, i);
          } else {
            card = new ShowingTableCard(d, i);
          }
          if (await card.canRenderContent()) pushCard(card);
        }
      }

      // Build final card list
      const availableCards = isK8s ? k8sAvailableCards : cloudAvailableCards;
      let allCards;
      if (isCloud) {
        const sortedAvailableCards = sortAvailableCards(availableCards, criticalCards, highCards, infoCards);
        allCards = [...sortedAvailableCards, ...dynamicCards];
      } else {
        allCards = [...dynamicCards, ...availableCards];
      }

      let canTracesRender = false;
      for (let C of allCards) {
        if (isGenCancelled()) break;
        let card = typeof C === 'function' ? new C() : C;

        if (card.setCleanupCallback) {
          card.setCleanupCallback(removeCardFromMatched);
        }
        if (card.setDataUpdateCallback) {
          card.setDataUpdateCallback(handleCardDataUpdate);
        }
        trackTimeout(() => {
          safeSetState(setCurrentInvestigation, card);
        }, 1);
        try {
          result.canTracesRender = canTracesRender;
          if (await card.canRenderContent(result, row)) {
            if (isGenCancelled()) break;
            await safeDelay(2000);
            if (isGenCancelled()) break;
            safeSetState(setMatchedOptions, (old) => {
              const cardExists = old.some((existingCard) => existingCard.id === card.id);
              if (cardExists) return old;
              return [...old, card];
            });
            if (card.id === 'TracesCard') {
              canTracesRender = true;
            }
            // Resume polling if RCA is still in progress (e.g., page refresh during analysis)
            if (card.id === 'RCACard' && card.rcaData?.status?.toUpperCase() === RCA_STATUS.IN_PROGRESS) {
              startRcaPolling();
            }
          } else if (card.id === 'RCACard') {
            safeSetState(setGenerateRcaVisible, true);
          }
        } catch (error) {
          console.error(error);
        } finally {
          safeSetState(setCurrentInvestigation, null);
        }
      }

      trackTimeout(() => {
        safeSetState(setCurrentInvestigation, null);
        safeSetState(setIsGeneratingCards, false);
      }, 1);
    }

    if (isRenderInvestigationCard && row?.id) {
      renderEvidenceCardAndRecommendations();
    }
    return () => {
      cancelGeneration();
    };
  }, [isRenderInvestigationCard, row?.id]);

  useEffect(() => {
    async function loadInsightsData() {
      try {
        let aggregation_key = queryParam.aggregationKey;
        let service_key = queryParam.serviceKey;
        let end_date = queryParam.start_at;
        const response = await apiKubernetes.getEventsSimilarDataAndInsights(aggregation_key, service_key, id, end_date);
        let last7Days = response?.data?.similar_issue_on_same_service_in_7days?.aggregate?.count;
        let last1Days = response?.data?.similar_issue_on_same_service_in_1days?.aggregate?.count;
        const othersData = [];
        if (last7Days !== 0) {
          othersData.push(`${last7Days} in Past 7 days`);
        }
        if (last1Days !== 0) {
          othersData.push(`${last1Days} in Past 1 days`);
        }
        setOthersData(othersData);
        const result = row?.evidences;
        if (result) {
          const parsedData = result;
          if (parsedData) {
            const filteredData = parsedData.find((item) => {
              if (item.type === 'json' && typeof item.data === 'string') {
                try {
                  let parsedData = JSON.parse(item.data);
                  if (parsedData.name === 'pod_details') {
                    return true;
                  }
                } catch {
                  // do nothing
                }
              }
              return false;
            });
            if (filteredData?.data) {
              const jsonData = JSON.parse(filteredData.data);
              setPodDetails(jsonData);
            }
          }
        }
        const insights = (row?.evidences || [])?.reduce((allInsights, evidence) => {
          const messages = evidence.insight?.filter((insight) => insight.severity === 'Critical').map((insight) => insight.message);
          if (messages) {
            allInsights.push(messages);
          }
          return allInsights;
        }, []);
        setInsightData(insights.flat());
      } catch (error) {
        console.error(error);
      }
      if (isMountedRef.current) setCurrentInvestigation(null);
    }

    const shouldLoad = Object.keys(queryParam).length > 0 && queryParam.aggregationKey?.trim() != '';
    // Cloud: additional guard - don't load if investigation cards are rendering
    if (shouldLoad && (isK8s || !isRenderInvestigationCard)) {
      setCurrentInvestigation(null);
      setMatchedOptions([]);
      loadInsightsData();
    }
  }, [queryParam?.id, queryParam?.aggregationKey, queryParam?.serviceKey, queryParam?.start_at, source]);

  const handleCardClick = useCallback((index) => {
    setOpenCardIndex((prevOpenCardIndex) => {
      const isCurrentlyOpen = prevOpenCardIndex === index;
      setCollapsedObj((prevCollapsedObj) => {
        const newCollapsedObj = { ...prevCollapsedObj };
        if (prevOpenCardIndex !== -1 && prevOpenCardIndex !== index) {
          newCollapsedObj[prevOpenCardIndex] = false;
        }
        newCollapsedObj[index] = !isCurrentlyOpen;
        return newCollapsedObj;
      });
      return isCurrentlyOpen ? -1 : index;
    });
  }, []);

  const handleCloseResolveComponent = useCallback(() => setOpenResolveComponentId(null), []);
  const handleOpenResolveComponent = useCallback((id) => setOpenResolveComponentId(id), []);

  const handleInsightClick = (text) => {
    const tasksCards = matchedOptions.filter((option) => option?.id !== 'AskAiCard' && option?.id !== 'RCACard');
    const filteredIndex = tasksCards.findIndex((item) => {
      const criticalMessage = (item?.highlightsData || item?.insightData || []).find((entry) => entry.message === text);
      return criticalMessage !== undefined;
    });

    if (filteredIndex !== -1) {
      setTabValue(1);
      handleCardClick(filteredIndex);
      trackTimeout(() => {
        const cardElement = document.querySelector(`[data-card-index="${filteredIndex}"]`);
        if (cardElement) {
          cardElement.scrollIntoView({ behavior: 'smooth', block: 'start', inline: 'nearest' });
        }
      }, 200);
    }
  };

  const handlePodClick = () => {
    if (row?.subject_type === SUBJECT_TYPE.POD && row?.cloud_resource_id) {
      router.push(`podDetails/${row?.cloud_resource_id}`);
    } else if (row?.aggregation_key === AGGREGATION_KEY.ANOMALY) {
      router.push(
        `/kubernetes/details/${row?.cloud_account_id}?namespace=${row?.subject_namespace}&workloadName=${row?.subject_name}#kubernetes/applications`
      );
    }
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = () => {
    let description = '';
    if (isCloud) {
      description = description + '**Where:** ' + row?.subject_namespace + '/' + row?.subject_name + '\n';
    } else {
      description = description + '**Where:** ' + row?.subject_name + '\n';
    }
    if (podDetails?.data?.containers[0]) {
      description = description + '**Image:** ' + podDetails?.data?.containers[0]?.imageName + '\n';
      description = description + '**Container Name:** ' + podDetails?.data?.containers[0]?.name + '\n';
      description =
        description + '**Reason & Exit Code:** ' + podDetails?.data?.containers[0]?.status?.reason ||
        '-' + ' & ' + podDetails?.data?.containers[0]?.status?.exitCode ||
        '-';
    }
    for (let option of matchedOptions) {
      const highlights = option?.getHighLightsData?.() ?? [];
      if (highlights.length > 0) {
        description = description + '\n' + '**' + option.text + ':** ' + highlights.map((f) => f.message).join(', ') + '\n';
      }
    }
    description = description + '\n' + `For more details. Please visit [${DEFAULT_TITLE}](${window.location.href})`;
    return description;
  };

  const updateInvestigateSuccessSnackBar = (severity, message) => {
    if (['success', 'error'].includes(severity)) {
      snackbar[severity](message);
    }
    handleCloseTroubleshootResolution();
  };

  const resetState = () => {
    cancelGeneration();
    stopRcaPolling();
    setRow({});
    setIsRenderInvestigationCard(!showDemoMessage);
    setInsightData([]);
    setPodDetails({});
    setOthersData([]);
    setLoading(false);
    setMatchedOptions([]);
    setCurrentInvestigation(null);
    setOpenCardIndex(-1);
    setCollapsedObj([]);
    setGenerateRcaVisible(false);
    setEventResolutions([]);
  };

  const getResolutionForCard = (cardId) => {
    for (const resolution of eventResolutions) {
      const d = typeof resolution.data === 'string' ? JSON.parse(resolution.data) : resolution.data;
      const input = d?.data;
      if (input?.card_id === cardId) return resolution;
    }
    for (const resolution of eventResolutions) {
      const d = typeof resolution.data === 'string' ? JSON.parse(resolution.data) : resolution.data;
      const input = d?.data;
      if (!input || input.card_id) continue;
      if (cardId === 'MemoryAllocationCard' && input.container_name) return resolution;
      if (cardId?.startsWith('LastDeploymentCard') && input.revert === true && !input.container_name) return resolution;
      if (cardId === 'AskAiCard' && (d?.provider === 'git' || d?.provider === 'github' || d?.provider === 'gitlab')) return resolution;
    }
    return null;
  };

  const handleTicketSuccess = () => {
    getTicketData();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const handleCloseTroubleshootResolution = () => {
    setIsTroubleshootFormOpen(false);
  };

  const getInvestigateDescription = (logText) => {
    if (isK8s && alertLabels?.nb_webhook_url) {
      logText = (logText || '') + `\n\n**Alert Url -** [${alertLabels.alertname || alertLabels.nb_webhook_event_id}](${alertLabels.nb_webhook_url})`;
    }
    if (logText) {
      const logSampleMatch = logText.match(/Log Sample:\s*(\{.*?\})\s*Failure Count:/s);
      const containerIdMatch = logText.match(/Container ID:\s*(\/[^\s]+)/);
      const failureCountMatch = logText.match(/Failure Count:\s*(\d+)/);
      return {
        logSample: logSampleMatch ? logSampleMatch[1] : logText,
        containerId: containerIdMatch ? containerIdMatch[1] : '',
        failureCount: failureCountMatch ? failureCountMatch[1] : '',
      };
    }
    return { logSample: '', containerId: '', failureCount: '' };
  };

  const handleGenerateRCA = () => {
    apiKubernetes.generateRCA(id, router.query.accountId, true).then((response) => {
      if (response?.status) {
        const responseStatus = response.status.toUpperCase();
        const rcaCard = new RCACard();
        rcaCard.event = row;
        rcaCard.renderContent = true;

        if (responseStatus === RCA_STATUS.COMPLETED) {
          // Already completed (re-trigger of existing RCA) — show results directly
          rcaCard.rcaData = {
            file_details: {},
            status: response.status,
            summary: response.summary,
            analysis: response.analysis,
          };
          rcaCard.insightData = [{ message: 'RCA report is ready', severity: 'Info' }];
        } else if (responseStatus === RCA_STATUS.FAILED) {
          rcaCard.rcaData = { status: response.status };
          rcaCard.insightData = [{ message: 'RCA hit a snag — try re-triggering or check back later', severity: 'Error' }];
        } else {
          // IN_PROGRESS — start polling
          rcaCard.rcaData = { status: RCA_STATUS.IN_PROGRESS };
          rcaCard.insightData = [{ message: 'RCA is underway — check back shortly for results', severity: 'Info' }];
          startRcaPolling();
        }

        if (handleCardDataUpdate) {
          rcaCard.setDataUpdateCallback(handleCardDataUpdate);
        }
        setMatchedOptions((prev) => {
          const existingIndex = prev.findIndex((option) => option.id === 'RCACard');
          if (existingIndex !== -1) {
            const next = [...prev];
            next[existingIndex] = rcaCard;
            return next;
          }
          return [...prev, rcaCard];
        });
        setGenerateRcaVisible(false);
        setTabValue(2); // Switch to RCA tab
      }
    });
  };

  useEffect(() => {
    hasFeatureAccess('GENERATE_RCA').then((res) => {
      setHasRcaFeatureAccess(res);
    });
  }, []);

  const aiCreateFeedback = async (createFeedbackObject) => {
    if (row.id) {
      await apiAskNudgebee.createAiFeedback({
        session_id: row.id,
        module: 'investigate',
        question: '',
        llm_response: '',
        user_corrected_response: '',
        additional_notes: createFeedbackObject.type == 'thumbs_up' ? 'User liked the Response' : createFeedbackObject.message,
        conversation_id: row.id,
        cloud_account_id: router.query.accountId || row.cloud_account_id,
        useful: createFeedbackObject.type == 'thumbs_up',
      });
    }
  };

  const mapLabels = (label) => {
    const labelArray = [];
    for (let item in label) {
      let name = item + '=' + label[item];
      labelArray.push(
        <CustomLabels
          textTransform='none'
          height='auto'
          margin='0px'
          wordBreak={'break-all'}
          displayTooltip
          key={item}
          text={name}
          variant={'grey'}
          maxWidth='260px'
          tooltipCharLimit={40}
        />
      );
    }
    return labelArray;
  };

  const labels = useMemo(() => mapLabels(alertLabels), [alertLabels]);
  const visibleLabels = useMemo(() => (showAll ? labels : labels.slice(0, 5)), [labels, showAll]);

  const toggleShow = () => setShowAll((prev) => !prev);

  const shouldShowResolveButton = (option) => {
    return (
      ((option?.id === 'AskAiCard' && !!option?.aiData?.source_updates?.gitDiff) ||
        (option?.id !== 'AskAiCard' && (option.resolveButton || option.ResolveComponent))) &&
      hasWriteAccess(router.query.accountId)
    );
  };

  const aiAnalysisFeedback = () => {
    const askAiCardObject = matchedOptions.find((option) => option?.id === 'AskAiCard');
    return (
      <>
        {row?.id && matchedOptions.some((option) => option?.id === 'AskAiCard') && (
          <Box sx={{ display: 'flex', gap: '32px', justifyContent: 'space-between' }}>
            {askAiCardObject?.isCompleted() && (
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'flex-start',
                  gap: '6px',
                  flexDirection: 'column',
                  mb: '24px',
                  mt: '12px',
                }}
              >
                <Typography sx={{ fontSize: '14px', color: '#374151' }}>Was this helpful?</Typography>
                <FeedbackComponent onFeedbackSubmit={(feedbackObject) => aiCreateFeedback(feedbackObject)} sentFeedback={sentFeedback} />
              </Box>
            )}
            {!askAiCardObject?.errorMessage && (
              <Box mt='auto' mb='24px' mr='32px'>
                <CustomButton
                  sx={{ maxHeight: '24px' }}
                  startIcon={<SafeIcon src={ExternalLinkIcon} alt='external link' height={16} width={16} />}
                  text='Continue With Analysis'
                  variant='tertiary'
                  size='xSmall'
                  disabled={!row.fingerprint}
                  onClick={() => {
                    if (row.fingerprint) {
                      let href = `/ask-nudgebee?accountId=${row.cloud_account_id || router.query.accountId}&session_id=event-${row.fingerprint}`;
                      window.open(href, '_blank');
                    }
                  }}
                />
              </Box>
            )}
          </Box>
        )}
      </>
    );
  };

  // K8s: Trend chart
  useEffect(() => {
    if (!showTrendChart) return;
    let cancelled = false;
    setIsTrendChartLoading(true);

    const query = {
      subject_namespace: row?.subject_namespace,
      subject_type: row?.subject_type,
      aggregation_key: row?.aggregation_key,
      start_date: new Date(new Date().getTime() - 24 * 60 * 60 * 1000),
      end_date: new Date(),
      subject_name: row?.subject_name,
    };

    apiKubernetes
      .getK8sEventGroupings(1000, 0, query)
      .then((res) => {
        if (cancelled || !isMountedRef.current) return;
        let data = [];
        let labels = [];
        res.data.event_groupings.forEach((item) => {
          data.push(item.event_count);
          labels.push(getDateString(item.created_at));
        });
        setTrendChartData({ data, labels });
      })
      .finally(() => {
        if (!cancelled && isMountedRef.current) setIsTrendChartLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [showTrendChart]);

  const showReferenceLinks = () => {
    const referenceLinks = (row?.evidences || [])
      ?.map((e, i) => ({
        title: e?.additional_info?.title || e?.additional_info?.action_name || `Reference ${i}`,
        url: e?.additional_info?.reference_url,
      }))
      .filter((f) => f.url);
    if (referenceLinks.length) {
      return (
        <Box sx={{ mt: 2 }}>
          <Typography sx={{ fontSize: '14px', fontWeight: 500, mb: 1 }}>References</Typography>
          <Divider sx={{ mb: 1.5 }} />
          <Box component='ul' sx={{ listStyleType: 'disc', pl: '20px', m: 0, display: 'flex', flexDirection: 'column', gap: '8px' }}>
            {referenceLinks.map((d) => (
              <li key={d.url}>
                <CustomLink href={d.url} openInNew style={{ fontSize: '14px', color: '#1a73e8' }}>
                  {d.title}
                </CustomLink>
              </li>
            ))}
          </Box>
        </Box>
      );
    }
  };

  // Determine details path prefix based on source
  const detailsPathPrefix = isCloud ? '/cloud-account/details' : '/kubernetes/details';

  return (
    <>
      {isTicketCreateFormOpen ? (
        <TicketCreatePopupForm
          open={isTicketCreateFormOpen}
          handleClose={closeTicketCreateForm}
          onClose={closeTicketCreateForm}
          onSuccess={handleTicketSuccess}
          onFailure={handleTicketFailure}
          ticketData={{
            subject: `Investigate Issue - ${row.title}`,
            description: getTicketDescription(),
            accountId: router.query.accountId,
          }}
          ticketUrl={{}}
          reference={{
            id: row?.fingerprint,
            type: 'kubernetes',
          }}
        />
      ) : null}
      <Modal
        width='lg'
        open={isTroubleshootFormOpen}
        handleClose={handleCloseTroubleshootResolution}
        title={`Resolve Event at ${row?.subject_name}`}
        loader={loading}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: '780px',
            '& .MuiDialogContent-root': {
              padding: '15px 40px 0px 40px',
            },
          },
        }}
      >
        <InvestigateResolution
          accountId={router.query.accountId}
          row={row}
          handleClose={handleCloseTroubleshootResolution}
          updateInvestigateSuccessSnackBar={updateInvestigateSuccessSnackBar}
        />
      </Modal>
      {/* K8s-only modals */}
      {isK8s && isUpdateEvent && (
        <UpdateEvent
          selectedEvent={row}
          handlePopupClose={() => {
            setIsUpdateEvent(false);
            loadData(row.id);
          }}
          isUpdateEvent={isUpdateEvent}
        />
      )}
      {isClassifyModalOpen && (
        <EventClassifyModal
          open={isClassifyModalOpen}
          handleClose={() => setIsClassifyModalOpen(false)}
          event={{
            id: row?.id,
            title: row?.title,
            fingerprint: row?.fingerprint,
            accountId: router.query.accountId || row?.cloud_account_id,
          }}
          onSuccess={() => loadData(row.id)}
        />
      )}
      {showTemplatesModal && (
        <WorkflowTemplatesModal
          open={showTemplatesModal}
          onClose={() => setShowTemplatesModal(false)}
          accountId={router.query.accountId}
          eventSources={row?.source ? [row.source] : undefined}
          alertNames={templateAlertName ? [templateAlertName] : undefined}
          subjectTypes={row?.subject_type ? [row.subject_type] : undefined}
          eventContext={eventContextForTemplate}
          onCreateWithAI={() => {
            setShowTemplatesModal(false);
            setShowAiGenerateModal(true);
          }}
        />
      )}
      {showAiGenerateModal && (
        <AiGenerateWorkflowModal
          open={showAiGenerateModal}
          onClose={() => {
            if (!aiGenerateLoading) {
              setShowAiGenerateModal(false);
            }
          }}
          onGenerate={handleAiGenerateWorkflow}
          onGenerateAsync={handleGenerateWorkflowAsync}
          onPollConversation={handlePollWorkflowConversation}
          onApproveOrRespond={handleApproveOrRespondWorkflow}
          onWorkflowCompleted={handleWorkflowCompleted}
          loading={aiGenerateLoading}
        />
      )}
      {isK8s && (
        <Modal width='md' open={openKB} handleClose={handleCloseKB} title={'Knowledge Base'} loader={loading} maxHeight='92vh'>
          <KnowledgeBase troubleShootingEvent={row} preLoadedKnowledgeBase={knowledgeBase} />
        </Modal>
      )}
      <Modal width='md' open={showTrendChart} handleClose={() => setShowTrendChart(false)} title={'Event Trend Chart'} loader={loading}>
        <Box sx={{ mb: 3 }}>
          <LineChart data={trendChartData.data} labels={trendChartData.labels} loading={isTrendChartLoading} integerYlabel={true} />
        </Box>
      </Modal>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: {
            xs: '280px 1fr',
            '@media (min-width: 1350px)': {
              gridTemplateColumns: '320px 1fr',
            },
          },
          gap: '8px',
          pt: '20px',
          alignItems: 'flex-start',
          position: 'relative',
          top: 0,
        }}
      >
        {/* SIDEBAR */}
        <ShimmerLoading isLoading={loading} height='calc(100vh - 120px)' width={'95%'}>
          <Box sx={{ position: 'sticky !important', top: '75px !important' }}>
            <Box
              sx={{
                border: `0.5px solid ${colors.border.secondary}`,
                borderRadius: '8px',
                padding: '0px 16px 16px 16px',
                maxHeight: 'calc(100vh - 110px)',
                overflowX: 'auto',
                '::-webkit-scrollbar': { width: '4px' },
              }}
            >
              <Box
                sx={{
                  position: 'sticky',
                  top: 0,
                  zIndex: 2,
                  background: colors.background.white,
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                  paddingTop: '16px',
                  paddingBottom: '12px',
                }}
              >
                <SafeIcon alt='kube-icon' src={TroubleShootIcon} />
                <Text value={row?.title || ''} showAutoEllipsis placement='right' sx={{ fontWeight: 600, fontSize: '16px', fontFamily: 'Roboto' }} />
              </Box>

              <Box sx={{ mt: '12px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
                <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                  <Text value={'Where'} secondaryText />
                  <Box onClick={handlePodClick} sx={{ cursor: 'pointer' }}>
                    <Text
                      value={row?.subject_name ? row?.subject_name : '-'}
                      secondaryText
                      showAutoEllipsis
                      sx={{
                        color:
                          (row?.subject_type === SUBJECT_TYPE.POD && row?.cloud_resource_id) || row?.aggregation_key === AGGREGATION_KEY.ANOMALY
                            ? '#2563EB'
                            : '#374151',
                        lineHeight: '1.4',
                      }}
                    />
                  </Box>
                </Box>
                <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                  <Text value={'When'} secondaryText />
                  <Text
                    value={
                      row?.starts_at ? (
                        <Datetime value={row?.starts_at} sx={{ fontSize: '12px' }} sxSuffix={{ color: colors.text.secondary, fontSize: '12px' }} />
                      ) : (
                        ''
                      )
                    }
                  />
                </Box>
                <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                  <Text value={'Severity'} secondaryText />
                  <Box>
                    <CustomLabels text={row?.priority || '-'} margin='0' width={'43px'} />
                  </Box>
                </Box>

                <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                  <Text value={'Triage Status'} secondaryText />
                  <Box>
                    <NBStatusBadge
                      eventId={row?.id}
                      currentStatus={row?.nb_status || 'OPEN'}
                      onStatusChange={() => loadData(row?.id)}
                      onCreateTicket={() => {
                        setTicketData(row);
                        setIsTicketCreateFormOpen(true);
                      }}
                    />
                  </Box>
                </Box>

                {row?.labels?.nb_duplicate_of && (
                  <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                    <Text value={'Duplicate of'} secondaryText />
                    <Box sx={{ minHeight: '18px', fontSize: '13px' }}>
                      <CustomLink
                        style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                        target={'_blank'}
                        href={`/investigate?id=${row.labels.nb_duplicate_of}&accountId=${row?.cloud_account_id}`}
                        openInNew={true}
                      >
                        <CustomLabels text={'View Original Event'} margin='0' />
                      </CustomLink>
                    </Box>
                  </Box>
                )}

                {recurrenceInfo?.isRecurrence && (
                  <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                    <Text value={'Recurrence'} secondaryText />
                    <Box sx={{ minHeight: '18px', fontSize: '13px' }}>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                        <Typography sx={{ fontSize: '12px', color: '#F57F17' }}>⚠️ Previously resolved</Typography>
                        <CustomLink
                          style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                          target={'_blank'}
                          href={`/investigate?id=${recurrenceInfo.previousEventId}&accountId=${row?.cloud_account_id}`}
                          openInNew={true}
                        >
                          <CustomLabels text={'View Previous'} margin='0' variant='yellow' />
                        </CustomLink>
                      </Box>
                    </Box>
                  </Box>
                )}

                {row?.labels?.nb_triage_rule_id && (
                  <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                    <Text value={'Triage Rule'} secondaryText />
                    <Box sx={{ minHeight: '18px', fontSize: '13px' }}>
                      <CustomLink
                        style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                        target={'_blank'}
                        href={`${detailsPathPrefix}/${row?.cloud_account_id}#events/triage-rules`}
                        openInNew={true}
                      >
                        <CustomLabels text={'View Triage Rule'} margin='0' />
                      </CustomLink>
                    </Box>
                  </Box>
                )}

                {/* Rule */}
                <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr' }}>
                  <Text value={'Rule'} secondaryText />
                  <Box sx={{ minHeight: '18px', fontSize: '13px' }}>
                    {row.aggregation_key && alertRules?.includes(row.aggregation_key) ? (
                      <CustomLink
                        style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                        target={'_blank'}
                        href={`${detailsPathPrefix}/${row?.cloud_account_id}?name=${row?.aggregation_key}#monitoring/alert-manager`}
                        openInNew={true}
                      >
                        <CustomLabels
                          text={(row?.aggregation_key?.includes('_') ? snakeToTitleCase(row?.aggregation_key) : row?.aggregation_key) || '-'}
                          margin='0'
                          height='auto'
                          wordBreak='break-word'
                          customLabelStyle={fitCustomLabelStyles}
                          displayTooltip={isCloud}
                          tooltipCharLimit={isCloud ? 25 : undefined}
                        />
                      </CustomLink>
                    ) : (
                      <CustomLabels
                        text={(row?.aggregation_key?.includes('_') ? snakeToTitleCase(row?.aggregation_key) : row?.aggregation_key) || '-'}
                        margin='0'
                        height='auto'
                        wordBreak='break-word'
                        customLabelStyle={fitCustomLabelStyles}
                        displayTooltip={isCloud}
                        tooltipCharLimit={isCloud ? 25 : undefined}
                      />
                    )}
                  </Box>
                </Box>
              </Box>

              {/* K8s-only: Crash Details, Last State, Impact sections */}
              {isK8s && (
                <>
                  {podDetails?.data?.containers?.some(
                    (pd) =>
                      pd?.status?.reason || (pd?.status?.exitCode !== undefined && pd?.status?.exitCode !== null) || pd?.name || pd?.status?.state
                  ) && (
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        background: colors.background.white,
                        gap: '6px',
                        mb: '16px',
                        mt: '24px',
                        '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: colors.border.secondaryLightest },
                      }}
                    >
                      <SafeIcon src={ErrorFillIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                      <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500, lineHeight: 'normal', minWidth: '102px' }}>
                        Crash Details
                      </Typography>
                    </Box>
                  )}

                  {podDetails?.data?.containers?.length > 0 && (
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                      {podDetails?.data?.containers?.map((pd, index) => (
                        <React.Fragment key={pd?.name || index}>
                          <Box
                            sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr', gap: '6px' }}
                          >
                            {pd?.name && (
                              <>
                                {pd?.status?.reason && (
                                  <>
                                    <Text value={'Reason'} secondaryText />
                                    <CustomLabels variant={'red'} customLabelStyle={{ padding: '2px 6px' }} text={pd.status.reason} />
                                  </>
                                )}
                                <>
                                  <Text value={'Container'} secondaryText />
                                  <TextWithTooltipAndCopy
                                    value={pd.name}
                                    sx={{ '& p': { fontSize: '12px !important', color: colors.text.secondary, lineHeight: '1.4' } }}
                                  />
                                </>
                                {pd?.status?.exitCode !== undefined && pd?.status?.exitCode !== null && (
                                  <>
                                    <Text value={'Exit code'} secondaryText />
                                    <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                                      <Text value={pd.status.exitCode} secondaryText sx={{ color: colors.text.secondary }} />
                                      <Tooltip title={exitCodeMapping[pd.status.exitCode] || 'Unknown'} arrow>
                                        <SafeIcon
                                          src={infoIcon}
                                          alt='info'
                                          style={{ width: '13px', height: '13px', cursor: 'pointer', filter: 'opacity(0.5)' }}
                                        />
                                      </Tooltip>
                                    </Box>
                                  </>
                                )}
                                {pd?.status?.state && (
                                  <>
                                    <Text value={'Status'} secondaryText />
                                    <Text value={pd.status.state} secondaryText sx={{ color: colors.text.secondary }} />
                                  </>
                                )}
                              </>
                            )}
                          </Box>
                        </React.Fragment>
                      ))}
                    </Box>
                  )}

                  {podDetails?.data?.containers?.some(
                    (pd) => pd?.lastStatus && (pd.lastStatus.state || pd.lastStatus.exitCode !== undefined || pd.lastStatus.reason)
                  ) && (
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        background: colors.background.white,
                        gap: '6px',
                        mb: '16px',
                        mt: '24px',
                        '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: colors.border.secondaryLightest },
                      }}
                    >
                      <SafeIcon src={LastStateIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                      <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500, lineHeight: 'normal', minWidth: '70px' }}>
                        Last State
                      </Typography>
                    </Box>
                  )}

                  {podDetails?.data?.containers?.map((pd, index) => (
                    <Box item xs={12} key={pd?.name || index}>
                      <Box className='container-box' sx={{ mb: 1, fontSize: '0.85rem' }}>
                        <Box>
                          {pd?.name && pd?.lastStatus && Object.values(pd.lastStatus).some((v) => v !== null && v !== undefined) && (
                            <Box
                              sx={{
                                display: 'grid',
                                flexDirection: 'column',
                                alignItems: 'flex-start',
                                gridTemplateColumns: '100px 1fr',
                                gap: '6px',
                              }}
                            >
                              {pd.lastStatus.state && (
                                <>
                                  <Text value={'State'} secondaryText />
                                  <Text value={pd.lastStatus.state} secondaryText sx={{ color: colors.text.secondary }} />
                                </>
                              )}
                              {pd.lastStatus.exitCode !== undefined && (
                                <>
                                  <Text value={'Exit Code'} secondaryText />
                                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                                    <Text value={pd.lastStatus.exitCode} secondaryText sx={{ color: colors.text.secondary }} />
                                    <Tooltip title={exitCodeMapping[pd.lastStatus.exitCode] || 'Unknown'} arrow>
                                      <SafeIcon
                                        src={infoIcon}
                                        alt='info'
                                        style={{ width: '12px', height: '12px', cursor: 'pointer', filter: 'opacity(0.5)' }}
                                      />
                                    </Tooltip>
                                  </Box>
                                </>
                              )}
                              {pd.lastStatus.reason && (
                                <>
                                  <Text value={'Reason'} secondaryText />
                                  <Text value={pd.lastStatus.reason} secondaryText sx={{ color: colors.text.secondary }} />
                                </>
                              )}
                            </Box>
                          )}
                        </Box>
                      </Box>
                    </Box>
                  ))}

                  {podDetails?.data?.containers?.some(
                    (pd) => pd?.status?.state && ((podDetails?.data?.restarts != null && podDetails?.data?.restarts > 0) || othersData?.length > 0)
                  ) && (
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        background: 'white',
                        gap: '6px',
                        mb: '16px',
                        mt: '24px',
                        '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: colors.border.secondaryLightest },
                      }}
                    >
                      <SafeIcon src={GraphOutlineIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                      <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500, lineHeight: 'normal', minWidth: '60px' }}>
                        Impact
                      </Typography>
                    </Box>
                  )}

                  <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr', gap: '6px' }}>
                    {podDetails?.data?.restarts != null && podDetails?.data?.restarts > 0 && (
                      <>
                        <Text value={'Restarts'} secondaryText />
                        <Text
                          value={podDetails?.data?.restarts}
                          secondaryText
                          sx={{ color: podDetails?.data?.restarts > 1 ? colors.text.cpuLimit : colors.text.secondary }}
                        />
                      </>
                    )}
                    {othersData?.length > 0 && (
                      <>
                        <Text value={'Frequency'} secondaryText />
                        <Box>
                          {othersData?.map((index) => (
                            <Text key={index} value={index} secondaryText sx={{ color: colors.text.secondary }} />
                          ))}
                        </Box>
                      </>
                    )}
                  </Box>
                </>
              )}

              {/* Context section */}
              {(row?.cluster || row?.subject_namespace || row?.subject_node || matchedOptions.filter((o) => o.infoData?.length > 0).length > 0) && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    background: 'white',
                    gap: '6px',
                    mb: '16px',
                    mt: '24px',
                    '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: colors.border.secondaryLightest },
                  }}
                >
                  <SafeIcon src={FileOutlineIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                  <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500, lineHeight: 'normal', minWidth: '55px' }}>
                    Context
                  </Typography>
                </Box>
              )}

              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr', gap: '6px' }}>
                {isK8s && podDetails?.data?.qosClass && (
                  <>
                    <Text value={'Cluster'} secondaryText />
                    <Text value={row?.cluster || '-'} secondaryText sx={{ color: colors.text.secondary }} />
                  </>
                )}
                {row?.subject_namespace && (
                  <>
                    <Text value={'Namespace'} secondaryText />
                    <Text value={row?.subject_namespace || '-'} secondaryText sx={{ color: colors.text.secondary }} />
                  </>
                )}
                {row?.subject_node && (
                  <>
                    <Text value={'Node'} secondaryText />
                    <TextWithTooltipAndCopy
                      value={row?.subject_node || '-'}
                      sx={{ '& p': { fontSize: '12px', color: colors.text.secondary, lineHeight: '1.4' } }}
                      maxSize={isK8s ? 20 : undefined}
                    />
                  </>
                )}
              </Box>

              {/* Others section */}
              {(row?.subject_name || row?.subject_type || podDetails?.data?.qosClass || podDetails?.data?.containers?.[0]?.imageName) && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    background: 'white',
                    gap: '6px',
                    mb: '16px',
                    mt: '24px',
                    '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: colors.border.secondaryLightest },
                  }}
                >
                  <SafeIcon src={BarsBlueOutlineIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                  <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500, lineHeight: 'normal', minWidth: '50px' }}>
                    Others
                  </Typography>
                </Box>
              )}
              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: '100px 1fr', gap: '6px' }}>
                {isK8s && podDetails?.data?.containers?.[0]?.imageName && (
                  <>
                    <Text value={'Image Name'} secondaryText />
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                      <ExpandableText
                        sx={{ fontSize: '12px', wordBreak: 'break-word', whiteSpace: 'normal', width: '100%', lineHeight: '1.4' }}
                        text={podDetails?.data?.containers[0].imageName || '-'}
                      />
                    </Box>
                  </>
                )}
                {row?.subject_node && (
                  <>
                    <Text value={'Subject Type'} secondaryText />
                    <Text value={row?.subject_type || '-'} secondaryText sx={{ color: colors.text.secondary }} />
                  </>
                )}
                {row?.subject_name && (
                  <>
                    <Text value={'Subject'} secondaryText />
                    <TextWithTooltipAndCopy
                      value={row?.subject_name || '-'}
                      sx={{ '& p': { fontSize: '12px', color: colors.text.secondary, lineHeight: '1.4' } }}
                      maxSize={isK8s ? 40 : undefined}
                    />
                  </>
                )}
                {podDetails?.data?.qosClass && (
                  <>
                    <Text value={'QOS Class'} secondaryText />
                    <Text value={podDetails?.data?.qosClass} secondaryText sx={{ color: colors.text.secondary }} />
                  </>
                )}
                {row?.description && (
                  <>
                    {getInvestigateDescription(row?.description).containerId && (
                      <>
                        <Text value={'Container Id'} secondaryText />
                        <TextWithTooltipAndCopy
                          value={getInvestigateDescription(row?.description).containerId || '-'}
                          maxSize={20}
                          sx={{ color: colors.text.secondary, '& p': { fontSize: '12px' } }}
                        />
                      </>
                    )}
                    {getInvestigateDescription(row?.description).failureCount && (
                      <>
                        <Text value={'Failure Count'} secondaryText />
                        <Text
                          className='text-value'
                          value={getInvestigateDescription(row?.description).failureCount}
                          secondaryText
                          sx={{ color: colors.text.secondary }}
                        />
                      </>
                    )}
                    {isCloud && (
                      <>
                        <Text value={'Description'} secondaryText />
                        <ExpandableText sx={{ fontSize: '12px' }} text={getInvestigateDescription(row?.description).logSample} />
                      </>
                    )}
                  </>
                )}
              </Box>

              {/* Alert labels */}
              {Object.keys(alertLabels).length > 0 && (
                <>
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      background: 'white',
                      gap: '6px',
                      mb: '16px',
                      mt: '24px',
                      '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: colors.border.secondaryLightest },
                      '& img': {
                        filter:
                          'brightness(0) saturate(100%) invert(44%) sepia(99%) saturate(2351%) hue-rotate(201deg) brightness(98%) contrast(97%)',
                      },
                    }}
                  >
                    <SafeIcon src={CubeIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                    <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 500, lineHeight: 'normal', minWidth: '85px' }}>
                      Alert labels
                    </Typography>
                  </Box>
                  <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '4px' }}>{visibleLabels}</Box>
                  {labels.length > 5 && (
                    <Box mt={1}>
                      <Button
                        onClick={toggleShow}
                        sx={{ fontSize: '11px', color: colors.text.primary, fontWeight: 400, textTransform: 'capitalize', padding: '0px' }}
                      >
                        Show {showAll ? 'Less' : `More (${labels.length - 5})`}
                      </Button>
                    </Box>
                  )}
                </>
              )}
              <InvestigateDropdown
                query={queryParam}
                inputMaxWidth='300px'
                subjectName={row?.subject_name}
                subjectNamespace={row?.subject_namespace}
                resetStateWhenItemSelected={resetState}
              />
            </Box>
          </Box>
        </ShimmerLoading>

        {/* MAIN CONTENT */}
        <ShimmerLoading isLoading={loading} height='calc(100vh - 120px)'>
          <Box
            sx={{
              minWidth: 0,
              width: '100%',
              maxWidth: '100%',
              overflowX: 'hidden',
            }}
          >
            <Box
              sx={{
                height: 'calc(100vh - 90px)',
                p: '0px 8px 12px 8px',
                display: 'flex',
                flexDirection: 'column',
                position: 'relative',
                overflow: 'auto',
                '::-webkit-scrollbar': { width: '6px' },
                '::-webkit-scrollbar-track': { background: '#f1f1f1', borderRadius: '10px' },
                '::-webkit-scrollbar-thumb': { background: '#c1c1c1', borderRadius: '10px' },
                '::-webkit-scrollbar-thumb:hover': { background: '#a8a8a8' },
              }}
            >
              {isRenderInvestigationCard ? (
                <>
                  {/* Sticky header section */}
                  <Box
                    sx={{
                      position: 'sticky',
                      top: '0px',
                      minHeight: '40px',
                      zIndex: 8,
                      backgroundColor: colors.background.white,
                      pt: '10px',
                      pb: '8px',
                    }}
                  >
                    <Box sx={{ display: 'flex', gap: '10px', width: '100%' }}>
                      <Box mt='6px'>
                        {isGeneratingCards ? (
                          <CircularProgress size={20} sx={{ color: '#FFD700', width: '24px', height: '24px' }} />
                        ) : (
                          <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={24} height={24} />
                        )}
                      </Box>
                      <Box
                        sx={{
                          borderBottom: 1,
                          borderColor: 'divider',
                          mb: 1,
                          textTransform: 'capitalize',
                          width: '100%',
                          '.MuiTabs-root': { minHeight: '42px !important' },
                          '.MuiTab-root': {
                            color: colors.text.secondary,
                            textTransform: 'none',
                            fontSize: '12px',
                            padding: '10px !important',
                            minHeight: '0px ',
                            borderRadius: '4px',
                          },
                          '.MuiTabs-indicator': { bgcolor: colors.text.primary, borderRadius: '4px' },
                          '.MuiTab-root.Mui-selected': { color: colors.text.secondary, bgcolor: colors.background.blueLabel },
                        }}
                      >
                        <Tabs value={tabValue} onChange={handleTabChange} aria-label='basic tabs example'>
                          {!isGeneratingCards && matchedOptions.some((option) => option?.id === 'AskAiCard') && (
                            <Tab label='Investigation Analysis' {...a11yProps(0)} disabled={!!currentInvestigation} value={0} />
                          )}
                          <Tab
                            label={
                              <Box component='span' sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                                Tasks
                                <Box component='span' sx={{ fontWeight: 400, color: colors.text.secondaryDark, fontSize: '12px' }}>
                                  ({matchedOptions.filter((option) => option?.id !== 'AskAiCard' && option?.id !== 'RCACard').length})
                                </Box>
                              </Box>
                            }
                            {...a11yProps(1)}
                            value={1}
                          />
                          {!isGeneratingCards && matchedOptions.some((option) => option?.id === 'RCACard') && (
                            <Tab label='RCA' {...a11yProps(2)} value={2} />
                          )}
                        </Tabs>
                      </Box>
                    </Box>
                    <Box sx={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', mt: '-50px' }}>
                      <Box sx={{ display: 'flex', alignItems: 'center' }}>
                        {/* Continue With Analysis */}
                        {row?.id &&
                          matchedOptions.some((option) => option?.id === 'AskAiCard') &&
                          !matchedOptions.find((option) => option?.id === 'AskAiCard')?.errorMessage && (
                            <Box sx={{ pr: '8px', display: 'flex', alignItems: 'center' }}>
                              <ButtonGroup variant='contained' disableElevation size='small' sx={{ height: '32px' }}>
                                <Button
                                  disabled={!row.fingerprint}
                                  onClick={() => {
                                    if (row.fingerprint) {
                                      let href = `/ask-nudgebee?accountId=${row.cloud_account_id || router.query.accountId}&session_id=event-${
                                        row.fingerprint
                                      }`;
                                      window.open(href, '_blank');
                                    }
                                  }}
                                  sx={{
                                    whiteSpace: 'nowrap',
                                    textTransform: 'none',
                                    backgroundColor: '#fff',
                                    color: '#374151',
                                    '&:hover': { backgroundColor: '#f9fafb' },
                                    border: '1px solid #d1d5db',
                                    borderColor: '#d1d5db !important',
                                    borderRight: 'none',
                                    px: 2,
                                    height: '100%',
                                  }}
                                >
                                  <Box sx={{ mr: '6px', display: 'flex', alignItems: 'center' }}>
                                    <SafeIcon src={ExternalLinkIcon} alt='external link' height={14} width={14} />
                                  </Box>
                                  <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>Continue With Analysis</Typography>
                                </Button>
                                {hasWriteAccess(router.query.accountId) && (
                                  <Button
                                    onClick={(e) => setAnchorElRefresh(e.currentTarget)}
                                    sx={{
                                      backgroundColor: '#fff',
                                      color: '#374151',
                                      '&:hover': { backgroundColor: '#f9fafb' },
                                      border: '1px solid #d1d5db',
                                      borderColor: '#d1d5db !important',
                                      minWidth: '32px',
                                      px: 0.5,
                                      height: '100%',
                                    }}
                                  >
                                    <KeyboardArrowDownIcon fontSize='small' />
                                  </Button>
                                )}
                              </ButtonGroup>
                              {hasWriteAccess(router.query.accountId) && (
                                <Menu
                                  anchorEl={anchorElRefresh}
                                  open={Boolean(anchorElRefresh)}
                                  onClose={() => setAnchorElRefresh(null)}
                                  anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
                                  transformOrigin={{ vertical: 'top', horizontal: 'right' }}
                                  sx={{ mt: 1 }}
                                >
                                  <MenuItem
                                    onClick={() => {
                                      setAnchorElRefresh(null);
                                      const askAiCard = matchedOptions.find((o) => o.id === 'AskAiCard');
                                      askAiCard?.refreshInvestigation?.();
                                    }}
                                    disabled={!matchedOptions.find((o) => o.id === 'AskAiCard')?.isCompleted()}
                                    sx={{ py: 1, px: 2 }}
                                  >
                                    <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151' }}>Refresh Investigation</Typography>
                                  </MenuItem>
                                </Menu>
                              )}
                            </Box>
                          )}
                        {/* Generate RCA */}
                        {hasWriteAccess(router.query.accountId) && hasRcaFeatureAccess && generateRcaVisible ? (
                          <CustomIconButton
                            sx={{ mr: '8px' }}
                            variant={'secondary'}
                            size={'xsmall'}
                            onClick={handleGenerateRCA}
                            disabled={!generateRcaVisible || isRcaPolling}
                          >
                            <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>
                              {isRcaPolling ? 'Generating RCA...' : 'Generate RCA'}
                            </Typography>
                          </CustomIconButton>
                        ) : null}
                        {/* Resolve Event */}
                        {hasWriteAccess(router.query.accountId) && RESOLVABLE_ALERT_KEYS.includes(row?.aggregation_key) ? (
                          <Box sx={{ pr: '8px' }}>
                            <CustomIconButton variant={'secondary'} size={'xsmall'} onClick={() => setIsTroubleshootFormOpen(true)}>
                              <Box sx={{ mr: '10px' }}>
                                <TicketIcon iconColor='#374151' iconStyle={{ cursor: 'pointer', width: '14px', height: '14px' }} />
                              </Box>
                              <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>Resolve Event</Typography>
                            </CustomIconButton>
                          </Box>
                        ) : null}
                        {/* Workflow Resolution Status (shown when a workflow has been linked to this event) */}
                        {(() => {
                          const res = eventResolutions.find((r) => r.type === 'WorkflowExecution');
                          if (!res) return null;
                          const statusColor = res.status === 'Success' ? '#16a34a' : res.status === 'InProgress' ? '#d97706' : '#dc2626';
                          const statusLabel =
                            res.status === 'Success'
                              ? 'Resolved via Workflow'
                              : res.status === 'InProgress'
                              ? 'Workflow Running...'
                              : 'Workflow Failed';
                          return (
                            <Box sx={{ pr: '8px' }}>
                              <CustomIconButton
                                variant={'secondary'}
                                size={'xsmall'}
                                onClick={() => {
                                  const parts = (res.type_reference_id || '').split(':');
                                  if (parts.length === 2) {
                                    router.push(`/workflow/${parts[0]}?tab=executions&executionId=${parts[1]}&accountId=${router.query.accountId}`);
                                  }
                                }}
                              >
                                <Box sx={{ mr: '6px' }}>
                                  <WorkflowIcon iconColor={statusColor} iconStyle={{ cursor: 'pointer', width: '14px', height: '14px' }} />
                                </Box>
                                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: statusColor }}>{statusLabel}</Typography>
                              </CustomIconButton>
                            </Box>
                          );
                        })()}
                        {/* Linked Ticket (shown inline when ticket exists) */}
                        {hasWriteAccess(router.query.accountId) && ticketData?.ticket_id ? (
                          <Box sx={{ pr: '8px' }}>
                            <CustomIconButton
                              variant={'secondary'}
                              size={'xsmall'}
                              onClick={() => window.open(ticketData?.url, '_blank', 'noopener,noreferrer')}
                            >
                              <Box sx={{ mr: '10px' }}>
                                <TicketIcon iconColor={colors.primary} iconStyle={{ cursor: 'pointer', width: '14px', height: '14px' }} />
                              </Box>
                              <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.primary }}>{ticketData?.ticket_id}</Typography>
                            </CustomIconButton>
                          </Box>
                        ) : null}
                        {/* More Actions Menu */}
                        <ThreeDotsMenu
                          data-testid='more-actions-btn'
                          menuItems={[
                            ...(isK8s
                              ? [
                                  {
                                    label: 'Knowledge Base',
                                    reactIcon: kbLoading ? <CircularProgress size={16} /> : <IoBookOutline color={colors.text.secondary} size={18} />,
                                    disabled: kbLoading,
                                    action: 'knowledge-base',
                                  },
                                  {
                                    label: 'Event Trend',
                                    reactIcon: <LuChartLine color={colors.text.secondary} size={18} />,
                                    action: 'event-trend',
                                  },
                                ]
                              : []),
                            ...(hasWriteAccess(router.query.accountId) && !ticketData?.ticket_id
                              ? [
                                  {
                                    label: 'Create Ticket',
                                    reactIcon: <TicketIcon iconColor={colors.text.secondary} iconStyle={{ width: '18px', height: '18px' }} />,
                                    action: 'create-ticket',
                                  },
                                ]
                              : []),
                            ...(hasWriteAccess(router.query.accountId)
                              ? [
                                  {
                                    label: 'Classify Event',
                                    reactIcon: <MdOutlineCategory color={colors.text.secondary} size={18} />,
                                    action: 'classify-event',
                                  },
                                ]
                              : []),
                            ...(isK8s && hasWriteAccess(router.query.accountId)
                              ? [
                                  {
                                    label: 'Update Event',
                                    reactIcon: <LiaEditSolid color={colors.text.secondary} size={18} />,
                                    action: 'update-event',
                                  },
                                ]
                              : []),
                            ...(hasWriteAccess(router.query.accountId)
                              ? [
                                  {
                                    label: 'Create Automation',
                                    reactIcon: <WorkflowIcon iconColor={colors.text.secondary} iconStyle={{ width: '18px', height: '18px' }} />,
                                    action: 'create-automation',
                                  },
                                ]
                              : []),
                          ]}
                          onMenuClick={(item) => {
                            switch (item.action) {
                              case 'knowledge-base':
                                handleOpenKB();
                                break;
                              case 'event-trend':
                                setShowTrendChart(true);
                                break;
                              case 'create-ticket':
                                setIsTicketCreateFormOpen(true);
                                break;
                              case 'classify-event':
                                setIsClassifyModalOpen(true);
                                break;
                              case 'update-event':
                                setIsUpdateEvent(true);
                                break;
                              case 'create-automation':
                                setShowTemplatesModal(true);
                                break;
                            }
                          }}
                          data={{}}
                          menuWidth={200}
                        />
                      </Box>
                    </Box>
                  </Box>
                  {/* Content area */}
                  <Box>
                    <Box
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        position: 'relative',
                        gap: '12px',
                        '& .custom-panel > .MuiBox-root:first-of-type': { padding: '8px 0px 24px 32px !important' },
                        '& .ai-custom-panel > .MuiBox-root:first-of-type': { padding: '8px 0px 24px 32px !important' },
                      }}
                    >
                      <TabPanel value={tabValue} index={0} className='ai-custom-panel'>
                        {loading ? (
                          <ConversationLoader />
                        ) : (
                          insightData.length > 0 && (
                            <Box>
                              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mb: '4px' }}>
                                <Avatar sx={{ width: '20px', borderRadius: '0px', height: '20px', flexShrink: 0, bgcolor: 'transparent' }}>
                                  <SafeIcon src={SparklesIconBG} alt='start-icon' width={20} height={20} priority={true} />
                                </Avatar>
                                <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600, lineHeight: 'normal' }}>
                                  Insights
                                </Typography>
                              </Box>
                              <ShowMoreList
                                data={
                                  isCloud ? insightData.map((item) => (typeof item === 'string' ? item : item?.message)).filter(Boolean) : insightData
                                }
                                initialCount={3}
                                onItemClick={handleInsightClick}
                              />
                            </Box>
                          )
                        )}
                        {matchedOptions
                          .filter((option) => option?.id === 'AskAiCard')
                          .map((option) => (
                            <AIOrRcaCard key={`ai-card-${option.id}`} option={option} noPadding />
                          ))}

                        {isK8s && !currentInvestigation?.text && matchedOptions.length > 0 && (
                          <Box sx={{ display: 'flex', flexDirection: 'column' }}>{showReferenceLinks()}</Box>
                        )}
                        {aiAnalysisFeedback()}
                        {matchedOptions.filter(shouldShowResolveButton).length > 0 && (
                          <>
                            <CustomDivider />
                            <Text value={'Take action to fix it'} sx={{ fontSize: '16px', fontWeight: 500, mb: '12px' }} />
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap' }}>
                              {matchedOptions.filter(shouldShowResolveButton).map((resolvableOption) => (
                                <CustomButton
                                  key={`fix-${resolvableOption.id}`}
                                  variant='tertiary'
                                  size='Small'
                                  sx={{ width: 'max-content', whiteSpace: 'nowrap', fontSize: '12px', fontWeight: '500' }}
                                  startIcon={<SafeIcon src={WrenchIconOutline} alt='fix-icon' width={14} height={14} />}
                                  text={`Fix ${resolvableOption.text}`}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setOpenResolveComponentId(resolvableOption.id);
                                  }}
                                />
                              ))}
                            </Box>
                          </>
                        )}
                        {matchedOptions
                          .filter((option) => option.id === openResolveComponentId)
                          .map((option) => {
                            const ResolveComponent = option?.getResolveComponent?.();
                            return ResolveComponent ? (
                              <ResolveComponent key={`resolve-${option.id}`} open={true} onCloseComponent={handleCloseResolveComponent} />
                            ) : null;
                          })}
                      </TabPanel>
                      <TabPanel value={tabValue} index={1} className='custom-panel'>
                        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                          {matchedOptions
                            .filter((option) => option?.id !== 'AskAiCard' && option?.id !== 'RCACard')
                            .map((option, index) => (
                              <div key={`${option?.id || option?.text}-${option?.refreshRenderId || 0}`} data-card-index={index}>
                                <CollapsableCard
                                  idx={index}
                                  icon={option?.icon}
                                  text={option?.text}
                                  resolveButton={option?.resolveButton}
                                  highlightsData={option?.getHighLightsData?.() ?? []}
                                  contentComponents={option?.getContentComponents?.() ?? []}
                                  onCardClick={handleCardClick}
                                  collapsedObj={collapsedObj}
                                  isCollapsed={collapsedObj[index]}
                                  expandedCardIndex={openCardIndex}
                                  resolveButtonClick={option?.resolveButton ? option?.handleResolveButtonClick : null}
                                  ResolveComponent={option?.resolveButton ? option?.getResolveComponent?.() ?? null : null}
                                  isBeta={option?.isBeta}
                                  newUI
                                  openResolveComponent={option.id === openResolveComponentId}
                                  onCloseResolveComponent={handleCloseResolveComponent}
                                  onOpenResolveComponent={handleOpenResolveComponent}
                                  maxWidth='100%'
                                  eventResolution={isK8s ? getResolutionForCard(option?.id) : undefined}
                                />
                              </div>
                            ))}
                          {currentInvestigation?.text && (
                            <CustomBorderCard
                              borderColor={colors.border.secondaryLightest}
                              sx={{ textAlign: 'center', mt: '0px' }}
                              showLeftBorder={false}
                            >
                              <ConversationLoader />
                            </CustomBorderCard>
                          )}
                        </Box>
                      </TabPanel>
                      <TabPanel value={tabValue} index={2} className='custom-panel'>
                        {matchedOptions
                          .filter((option) => option?.id === 'RCACard')
                          .map((option) => (
                            <AIOrRcaCard key={`ai-card-${option?.id || option?.text}-${option?.refreshRenderId || 0}`} option={option} noPadding />
                          ))}
                      </TabPanel>
                    </Box>
                  </Box>
                </>
              ) : (
                showDemoMessage && (
                  <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
                    <Box
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'center',
                        justifyContent: 'center',
                        textAlign: 'center',
                        backgroundColor: 'white',
                        gap: '12px',
                        width: '100%',
                        height: '180px',
                      }}
                    >
                      <CustomButton
                        size='Large'
                        text={`Ask ${assistantName} for Investigation`}
                        onClick={() => {
                          setIsRenderInvestigationCard(true);
                          setTabValue(1);
                        }}
                        endIcon={<ArrowForwardRoundedIcon />}
                        sx={{
                          padding: '8px 16px !important',
                          fontSize: '16px',
                          height: '36px',
                          alignItems: 'center',
                          gap: '4px',
                          minWidth: 'fit-content',
                          '& .MuiButton-endIcon svg,img': { height: '24px', width: '22px' },
                        }}
                        loading={loading}
                        disabled={row && Object.keys(row).length == 0}
                        variant={'primary'}
                      />
                      <Typography sx={{ fontSize: '14px', color: '#9F9F9F', lineHeight: '18px', fontWeight: 400, width: '35%' }}>
                        Get a troubleshooting checklist to find the issue&apos;s root cause and key contributing factors.
                      </Typography>
                    </Box>
                  </Box>
                )
              )}
            </Box>
          </Box>
        </ShimmerLoading>
      </Box>
    </>
  );
};

Investigate.propTypes = {};

export default Investigate;
