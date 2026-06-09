import { useState, useEffect, useRef } from 'react';
import { useRouter } from 'next/router';
import AnchorComponent from '@common-new/AnchorComponent';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import KuberneteUtilizationSummary from '@components1/k8s/KuberneteUtilizationSummary';
import KubernetesRightSizing from '@components1/recommendations/KubernetesRightSizing';
import KubernetesUnusedVolumes from '@components1/recommendations/KubernetesUnusedVolumes';
import KubernetesBestPractices from '@components1/recommendations/KubernetesBestPractices';
import KubernetesWorkloadsTable from '@components1/k8s/details/KubernetesWorkloads';
import KubernetesNodesTable from '@components1/k8s/details/KubernetesNodes';
import KubernetesPodsTable from '@components1/k8s/details/KubernetesPods';
import KubernetesNamespaceTable from '@components1/k8s/details/KubernetesNamespace';
import k8sApi from '@api1/kubernetes';
import KubernetesAbandonedWorkloads from '@components1/recommendations/KubernetesAbandonedWorkloads';
import KubernetesPVCRightSizing from '@components1/recommendations/KubernetesPVCRightSizing';
import KubernetesReplicaRightSizing from '@components1/recommendations/KubernetesReplicaRightSizing';
import KubernetesSpotRecommendation from '@components1/recommendations/KubernetesSpotRecommendation';
import KubernetesSecurity from '@components1/recommendations/KubernetesSecurity';
import KubernetesLogsPattern from '@components1/k8s/details/KubernetesLogsPattern';
import KubernetesEventsTable from '@components1/events/KubernetesEvents';
import KubernetesApplicationApiFailure from '@components1/events/KubernetesApplicationApiFailure';
import { useData } from '@context/DataContext';
import LogsIcon from '@assets/kubernetes/logs-icon.svg';
import { Box, Typography } from '@mui/material';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import { ds } from '@utils/colors';
import Loader from '@components1/common/Loader';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { snackbar } from '@components1/common/snackbarService';
import KubernetesApplicationLogFailure from '@components1/events/KubernetesApplicationLogFailure';
import KubernetesGroupedEvents from '@components1/events/KubernetesGroupedEvents';
import {
  BetaIcon,
  FullScreenIcon,
  AutoScalerIcon,
  OptimizeSummaryIcon,
  RightSizingIcon,
  UnusedVolumeIcon,
  BestPracticesIcon,
  AbandonedAppsIcon,
  PvcSightSizing,
  ReplicaRightSizingIcon,
  SpotRecommendationIcon,
  RecommendationResolutionIcon,
  EventSummaryIcon,
  GroupedEventsIcon,
  PodErrorsIcon,
  NodeErrorsIcon,
  ApplicationErrorsIcon,
  AllEventsIcon,
  AppNodesPodsIcon,
  ApplicationsIconblue,
  NamespaceIcon,
  NodesIcon,
  ServicesIcon,
  PVCIcon,
  PVIcon,
  LogGroupsIcon,
  QueryLogIcon,
  ServiceMapIcon,
  TracesGroupIcon,
  CrossZoneCommunicationIcon,
  LogsTracesIcon,
  MarticsPromQLIcon,
  AlertManagerIcon,
  ClusterUpgradeIcon,
  CertificateIssuesIcon,
  HelmUpgradeIcon,
  SensitiveLogIcon,
  CISScanIcon,
  ImageScanIcon,
  ScalerNodePoolIcon,
  ScalerNodeClassIcon,
  EventIcon,
  OptimizeIconBlue,
  SummaryIconBlue,
  TroubleshootIconBlue,
  AppsInfraBlue,
  MonitoringIconBlue,
  SecuritytoolsBlue,
  DataBaseBlueIcon,
  QueueBlueIcon,
  AnomalyIcon,
  SLOInspectionIcon,
  GrafanaIconBlue,
} from '@assets';
import KubernetesLogs from '@components1/k8s/details/KubernetesLogs';
import KubernetesSSLCertificateRecommendation from '@components1/recommendations/KubernetesSSLCertificateRecommendation';
import KubernetesCisSecurityV2 from '@components1/recommendations/KubernetesCisSecurityV2';
import KubernetesHelmUpgradeRecommendation from '@components1/recommendations/KubernetesHelmUpgradeRecommendation';
import KubernetesClusterSummary from '@components1/k8s/KubernetesClusterSummary';
import KuberneteComputeSummary from '@components1/k8s/KubernetesComputeSummary';
import PropTypes from 'prop-types';
import KubernetesPVCTable from '@components1/k8s/details/KubernetesPVC';
import KubernetesPVTable from '@components1/k8s/details/KubernetesPV';
import KubernetesServices from '@components1/k8s/details/KubernetesServices';
import KubernetesOptimizeSummary from '@components1/recommendations/KubernetesOptimizeSummary';
import KubernetesEventsSummary from '@components1/events/KubernetesEventsSummary';
import KubernetesClusterSummaryUtilization from '@components1/k8s/KubernetesClusterSummaryUtilization';
import { KubernetesNodesTrends } from '@components1/k8s/details/KubernetesNodesTrends';
import KubernetesNodeClass from '@components1/k8s/details/KubernetesNodeClass';
import apiKubernetes1 from '@api1/kubernetes1';
import CustomPill from '@components1/common/CustomPill';
import KubernetesAutoScalerLogs from '@components1/k8s/details/KubernetesAutoScalerLogs';
import apiRecommendations from '@api1/recommendation';
import ClusterUpgradeFeature from '@components1/k8s/ClusterUpgradeFeature';
import KubernetesLogSensitiveInfo from '@components1/k8s/details/KubernetesLogSensitiveInfo';
import ListingRecommendationResolution from '@components1/recommendations/ListingRecommendationResolution';
import KubernetesAnomaly from '@components1/k8s/details/KubernetesAnomaly';
import DefaultAutoScaler from '@components1/k8s/details/DefaultAutoScaler';
import KubernetesAutoScalerNodePool from '@components1/k8s/details/KubernetesAutoScalerNodePool';
import EmptyData from '@components1/common/EmptyData';
import KubernetesDbmsTable from '@components1/k8s/details/KubernetesDbms';
import KubernetesQueueTable from '@components1/k8s/details/KubernetesQueue';
import KubernetesAlertManager from '@components1/k8s/details/KubernetesAlertManager';
import TriageRulesManager from '@components1/triage/TriageRulesManager';
import KubernetesTracesListing from '@components1/k8s/details/KubernetesTracesListing';
import KubernetesServiceMapWrapper from '@components1/k8s/details/KubernetesServiceMap';
import KubernetesTracesGroupListing from '@components1/k8s/details/KubernetesTracesGroupListing';
import KubernetesTracesCrossZoneListing from '@components1/k8s/details/KubernetesTracesCrossZone';
import KubernetesSLOConfigs from '@components1/k8s/KubernetesSLOConfigs';
import KubernetesClusterUpgradePlanner from '@components1/k8s/clusterUpgradePlanner/KubernetesClusterUpgradePlanner';
import QueryMetrics from '@components1/k8s/details/QueryMetrics';
import KubernetesGroupedEventsTable from '@components1/k8s/details/groupedevents/KubernetesGroupedEventsTable';
import SafeIcon from '@components1/common/SafeIcon';
import { hasWriteAccess } from '@lib/auth';

const GrafanaIframe = ({ accountId }) => {
  const iframeRef = useRef(null);

  const toggleFullscreen = () => {
    if (!document.fullscreenElement) {
      if (iframeRef.current.requestFullscreen) {
        iframeRef.current.requestFullscreen();
      } else if (iframeRef.current.mozRequestFullScreen) {
        iframeRef.current.mozRequestFullScreen();
      } else if (iframeRef.current.webkitRequestFullscreen) {
        iframeRef.current.webkitRequestFullscreen();
      } else if (iframeRef.current.msRequestFullscreen) {
        iframeRef.current.msRequestFullscreen();
      }
    } else {
      if (document.exitFullscreen) {
        document.exitFullscreen();
      } else if (document.mozCancelFullScreen) {
        document.mozCancelFullScreen();
      } else if (document.webkitExitFullscreen) {
        document.webkitExitFullscreen();
      } else if (document.msExitFullscreen) {
        document.msExitFullscreen();
      }
    }
  };

  return (
    <>
      <Box display='flex' justifyContent='space-between' alignItems='center' marginBottom='5px'>
        <Typography style={{ margin: 'auto', fontWeight: 600 }}>Grafana Dashboard</Typography>
        <SafeIcon alt='full screen' src={FullScreenIcon} onClick={toggleFullscreen} style={{ cursor: 'pointer' }} width={20} height={20} />
      </Box>
      <iframe
        id='grafanaIframe'
        ref={iframeRef}
        title='Grafana Dashboard'
        referrerPolicy='unsafe-url'
        src={`${window.location.origin}/api/proxy/grafana/gr-${accountId}?orgId=1`}
        width={(window?.innerWidth * 0.85).toFixed() + 'px'}
        height={(window?.innerHeight * 0.8).toFixed() + 'px'}
      />
    </>
  );
};
GrafanaIframe.propTypes = {
  accountId: PropTypes.any,
};

const KubernetesDetails = () => {
  const router = useRouter();
  const { selectedCluster } = useData();
  const { podTab } = router.query;

  useEffect(() => {
    if (podTab) {
      setPodRadioTabValue(podTab);
    }
  }, [podTab]);

  const [kubeId, setKubeId] = useState(router.query.KubernetesDetails);
  const [clusterSummary, setClusterSummary] = useState({});
  const [selectedTab, setSelectedTab] = useState(0);
  const [selectedSubTab, setSelectedSubTab] = useState(0);
  const [podRadioTabValue, setPodRadioTabValue] = useState('__all__');
  const [scalerRadioTabValue, setScalerRadioTabValue] = useState(0);
  const [applicationRadioTabValue, setApplicationRadioTabValue] = useState('__all__');
  const [tabOptions, setTabOptions] = useState([
    {
      name: 'Summary',
      value: 0,
      icon: SummaryIconBlue,
      options: [
        { id: 'cluster-summary', name: 'Cluster Summary' },
        { id: 'cost-summary', name: 'Cost Summary' },
        { id: 'utilization', name: 'Utilization' },
      ],
      fragment: 'summary',
    },
    {
      name: 'Optimize',
      fragment: 'optimize',
      value: 1,
      icon: OptimizeIconBlue,
      tabOptions: [
        { id: 'summary', text: 'Summary', value: 7, fragment: 'summary', icon: OptimizeSummaryIcon },
        { id: 'right-sizing', text: 'Right Sizing', value: 0, fragment: 'right-sizing', icon: RightSizingIcon },
        { id: 'auto-scaler', text: 'Auto Scaler', value: 8, fragment: 'auto-scaler', icon: AutoScalerIcon },
        { id: 'unused-volume', text: 'Unused Volumes', value: 1, fragment: 'unused-volume', icon: UnusedVolumeIcon },
        { id: 'best-practices', text: 'Best Practices', value: 2, fragment: 'best-practices', icon: BestPracticesIcon },
        { id: 'abandoned-resources', text: 'Abandoned Apps', value: 3, fragment: 'abandoned-resources', icon: AbandonedAppsIcon },
        { id: 'pv-rightsizing', text: 'PVC Rightsizing', value: 4, fragment: 'pv-rightsizing', icon: PvcSightSizing },
        { id: 'replica-rightsizing', text: 'Replica Rightsizing', value: 5, fragment: 'replica-rightsizing', icon: ReplicaRightSizingIcon },
        { id: 'spot-recommendation', text: 'Spot Recommendations', value: 6, fragment: 'spot-recommendation', icon: SpotRecommendationIcon },
        {
          id: 'recommendation-resolution-status',
          text: 'Recommendation Resolution',
          value: 9,
          fragment: 'recommendation-resolution',
          icon: RecommendationResolutionIcon,
        },
      ],
    },
    {
      name: 'Troubleshoot',
      fragment: 'events',
      value: 2,
      icon: TroubleshootIconBlue,
      tabOptions: [
        { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: EventSummaryIcon },
        { id: 'fingerprint', text: 'Triage Inbox', value: 7, fragment: 'inbox', icon: AllEventsIcon },
        { id: 'grouped_events', text: 'Events - By Type', value: 1, fragment: 'grouped-events', icon: GroupedEventsIcon },
        { id: 'pod_error', text: 'Pod Errors', value: 2, fragment: 'pod-errors', icon: PodErrorsIcon },
        { id: 'node_errors', text: 'Node Errors', value: 3, fragment: 'node-errors', icon: NodeErrorsIcon },
        { id: 'app_errors', text: 'Application Errors', value: 4, fragment: 'app-errors', icon: ApplicationErrorsIcon },
        { id: 'all_events', text: 'All Events', value: 5, fragment: 'all-events', icon: AllEventsIcon },
        { id: 'anomaly', text: 'Anomaly', value: 6, fragment: 'anomaly', icon: AnomalyIcon, betaIcon: true },
        { id: 'triage-rules', text: 'Triage Rules', value: 8, fragment: 'triage-rules', icon: AlertManagerIcon },
      ],
    },
    {
      name: 'Apps & Infra',
      fragment: 'kubernetes',
      value: 3,
      icon: AppsInfraBlue,
      tabOptions: [
        { id: 'nodes', text: 'Nodes', value: 0, fragment: 'nodes', icon: NodesIcon },
        { id: 'applications', text: 'Applications', value: 1, fragment: 'applications', icon: ApplicationsIconblue },
        { id: 'pods', text: 'Pods', value: 3, fragment: 'pods', icon: AppNodesPodsIcon },
        { id: 'namespaces', text: 'Namespace', value: 2, fragment: 'namespaces', icon: NamespaceIcon },
        { id: 'services', text: 'Services', value: 4, fragment: 'services', icon: ServicesIcon },
        { id: 'pvc', text: 'PVC', value: 5, fragment: 'pvc', icon: PVCIcon },
        { id: 'pv', text: 'PV', value: 6, fragment: 'pv', icon: PVIcon },
        { id: 'dbms', text: 'Databases', value: 7, fragment: 'dbms', icon: DataBaseBlueIcon, betaIcon: true },
        { id: 'queue', text: 'Queues', value: 8, fragment: 'queue', icon: QueueBlueIcon, betaIcon: true },
      ],
    },
    {
      name: 'Monitoring',
      fragment: 'monitoring',
      value: 4,
      icon: MonitoringIconBlue,
      groupedTab: true,
      tabOptions: [
        { id: 'query-log', text: 'Query Log', value: 0, fragment: 'logs', icon: QueryLogIcon, tabName: 'logs' },
        { id: 'log-groups', text: 'Log Groups', value: 1, fragment: 'groups', icon: LogGroupsIcon, tabName: 'logs' },
        { id: 'prom-query', text: 'Query Metrics', value: 2, fragment: 'query', icon: MarticsPromQLIcon, tabName: 'Metrics' },
        { id: 'alert-manager', text: 'Alert Manager', value: 3, fragment: 'alert-manager', icon: AlertManagerIcon, tabName: 'Metrics' },
        { id: 'service-map', text: 'Service Map', value: 4, fragment: 'service-map', icon: ServiceMapIcon, tabName: 'traces' },
        { id: 'Traces', text: 'Traces', value: 5, fragment: 'traces', icon: LogsTracesIcon, tabName: 'traces' },
        { id: 'trace-grouping', text: 'Trace Group', value: 6, fragment: 'grouping', icon: TracesGroupIcon, tabName: 'traces' },
        {
          id: 'trace-cross-zon',
          text: 'Cross-Zone Communication',
          value: 7,
          fragment: 'cross-zone',
          icon: CrossZoneCommunicationIcon,
          tabName: 'traces',
        },
        { id: 'slo', text: 'SLO', value: 8, fragment: 'slo', icon: SLOInspectionIcon, tabName: 'Others' },
        { id: 'grafana', text: 'Grafana', value: 9, fragment: 'grafana', icon: GrafanaIconBlue, tabName: 'Others' },
      ],
    },
    {
      name: 'Security & Tools',
      fragment: 'security',
      value: 5,
      icon: SecuritytoolsBlue,
      tabOptions: [
        { id: 'image-scan', text: 'Image Scan', fragment: 'image-scan', value: 0, icon: ImageScanIcon, tabName: 'security' },
        { id: 'cis-scan', text: 'CIS Scan', value: 1, fragment: 'cis-scan', icon: CISScanIcon, tabName: 'security' },
        { id: 'sensitive-log', text: 'Sensitive Logs', value: 2, fragment: 'sensitive-log', icon: SensitiveLogIcon, tabName: 'security' },
        {
          id: 'cluster-upgrade',
          text: 'Cluster Upgrade',
          fragment: 'cluster-upgrade',
          value: 3,
          icon: ClusterUpgradeIcon,
          tabName: 'tools',
          betaIcon: <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '2px' }} />,
        },
        {
          id: 'Upgrade Planner',
          text: 'Upgrade Planner',
          fragment: 'upgrade-planner',
          value: 4,
          icon: ClusterUpgradeIcon,
          tabName: 'tools',
          betaIcon: <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '2px' }} />,
        },
        {
          id: 'ssl-certificate-issues',
          text: 'Certificate Issues',
          value: 5,
          fragment: 'ssl-certificate-issues',
          icon: CertificateIssuesIcon,
          tabName: 'tools',
        },
        { id: 'helm-upgrade', text: 'Helm Upgrade', value: 6, fragment: 'helm-upgrade', icon: HelmUpgradeIcon, tabName: 'tools' },
      ],
    },
  ]);
  const [aggregationKeyCount, setAggregationKeyCount] = useState({});

  useEffect(() => {
    if (kubeId !== router.query.KubernetesDetails) {
      setKubeId(router.query.KubernetesDetails);
    }
  }, [router.query.KubernetesDetails]);

  useEffect(() => {
    const init = async () => {
      const grafana = selectedCluster?.agent?.connection_status?.grafanaEnabled || false;
      const isJaeger = selectedCluster?.cloud_provider === 'jaeger';
      // Grafana embeds a full query/explore surface, so restrict it to users
      // with write access on this cluster — read-only roles get the tab hidden.
      const canAccessGrafana = grafana && hasWriteAccess(kubeId);
      setTabOptions((prevOptions) =>
        prevOptions.map((option) => {
          if (option.name === 'Monitoring') {
            return {
              ...option,
              tabOptions: option.tabOptions.map((tab) => {
                if (tab.id === 'trace-grouping') return { ...tab, hidden: isJaeger };
                if (tab.id === 'grafana') return { ...tab, hidden: !canAccessGrafana };
                return tab;
              }),
            };
          }
          return option;
        })
      );
    };

    init();
  }, [selectedCluster]);

  const filterRecommendationCategoryRuleNameFromResponse = (response, category, ruleName) => {
    if (ruleName && category) {
      const countForRule = response.find((r) => r.category === category && r.rule_name === ruleName);
      return countForRule ? countForRule.count : '';
    }
    const totalCategoryCount = response.filter((r) => r.category === category).reduce((acc, curr) => acc + curr.count, 0);
    return totalCategoryCount || '';
  };

  useEffect(() => {
    const hash = typeof window !== 'undefined' ? window.location.hash.slice(1) : '';
    if (selectedTab === 0 && hash && hash !== 'summary') return;
    setClusterSummary({});

    const handleClusterDataFetch = () => {
      k8sApi.getk8ClusterData(kubeId).then((res) => {
        if (res.errors) {
          snackbar.error('Failed to load cluster data');
          return;
        }
        setClusterSummary(res.data);
      });
    };

    const handleRecommendationCountsFetch = () => {
      apiRecommendations.getIndividualRecommendationRuleTypeCount(kubeId).then((res) => {
        const response = res?.data?.data?.recommendation_groupings_v2?.rows ?? [];
        if (response.length > 0) {
          const counts = {
            'right-sizing': filterRecommendationCategoryRuleNameFromResponse(response, 'RightSizing', 'pod_right_sizing'),
            'unused-volume': filterRecommendationCategoryRuleNameFromResponse(response, 'RightSizing', 'unused_pvc'),
            'best-practices': filterRecommendationCategoryRuleNameFromResponse(response, 'Configuration', ''),
            'abandoned-resources': filterRecommendationCategoryRuleNameFromResponse(response, 'RightSizing', 'abandoned_resource'),
            'pv-rightsizing': filterRecommendationCategoryRuleNameFromResponse(response, 'RightSizing', 'pv_rightsize'),
            'replica-rightsizing': filterRecommendationCategoryRuleNameFromResponse(response, 'RightSizing', 'replica_right_sizing'),
            'spot-recommendation': filterRecommendationCategoryRuleNameFromResponse(response, 'K8sSpotRecommendation', ''),
          };
          updateFilterOptions(1, counts);
        }
      });
    };

    const handleEventCountsFetch = () => {
      apiKubernetes1
        .getIndividualEventTypeCount(kubeId, new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString(), new Date().toISOString())
        .then((res) => {
          const counts = {
            pod_error: res?.data?.data?.pod_error_count?.rows[0]?.count,
            node_errors: res?.data?.data?.node_error_count?.rows[0]?.count,
            app_errors: res?.data?.data?.application_error_count?.rows[0]?.count,
            all_events: res?.data?.data?.event_count?.rows[0]?.count,
          };
          updateFilterOptions(2, counts);
        });
    };

    const handleAggregationKeyCountsFetch = () => {
      apiKubernetes1
        .getIndividualAggregationKeyCount({
          account_id: kubeId,
          subject_type: 'pod',
          finding_type: 'issue',
          status: 'FIRING',
          aggregation_key: [
            'pod_oom_killer_enricher',
            'image_pull_backoff_reporter',
            'report_crash_loop',
            'CPUThrottlingHigh',
            'KubeDeploymentReplicasMismatch',
          ],
        })
        .then((res) => {
          const eventGroupings = res?.data?.data?.event_groupings_v2?.rows || [];
          const counts = {
            pod_oom_killer_enricher: eventGroupings.find((row) => row.aggregation_key === 'pod_oom_killer_enricher')?.count || '',
            image_pull_backoff_reporter: eventGroupings.find((row) => row.aggregation_key === 'image_pull_backoff_reporter')?.count || '',
            report_crash_loop: eventGroupings.find((row) => row.aggregation_key === 'report_crash_loop')?.count || '',
            CPUThrottlingHigh: eventGroupings.find((row) => row.aggregation_key === 'CPUThrottlingHigh')?.count || '',
            KubeDeploymentReplicasMismatch: eventGroupings.find((row) => row.aggregation_key === 'KubeDeploymentReplicasMismatch')?.count || '',
          };
          setAggregationKeyCount(counts);
        });
    };

    const updateFilterOptions = (filterValue, counts) => {
      setTabOptions((prevOptions) =>
        prevOptions.map((option) => {
          if (option.value === filterValue) {
            return {
              ...option,
              tabOptions: option.tabOptions.map((tabOption) => ({
                ...tabOption,
                count: counts[tabOption.id] ?? tabOption.count,
              })),
            };
          }
          return option;
        })
      );
    };

    if (selectedTab === 2 && selectedSubTab === 2) {
      const counts = {
        pod_oom_killer_enricher: '',
        image_pull_backoff_reporter: '',
        report_crash_loop: '',
        CPUThrottlingHigh: '',
        KubeDeploymentReplicasMismatch: '',
      };
      setAggregationKeyCount(counts);
      handleAggregationKeyCountsFetch();
    } else if (selectedTab === 0) {
      handleClusterDataFetch();
    } else if (selectedTab === 1) {
      handleRecommendationCountsFetch();
    } else if (selectedTab === 2) {
      handleEventCountsFetch();
    }
  }, [selectedTab, selectedSubTab, kubeId]);

  const renderingNodePool = () => {
    if (selectedCluster?.agent?.connection_status?.autoScalerEnabled) {
      if (selectedCluster?.agent?.connection_status?.autoScalerType === 'cluster-autoscaler') {
        return <DefaultAutoScaler accountId={kubeId} namespace={selectedCluster?.agent?.connection_status?.autoScalerNamespace} />;
      } else if (selectedCluster?.agent?.connection_status?.autoScalerType === 'karpenter') {
        return <KubernetesAutoScalerNodePool accountId={kubeId} />;
      }
    } else if (selectedCluster?.agent?.connection_status?.karpenterEnabled) {
      return <KubernetesAutoScalerNodePool accountId={kubeId} />;
    } else {
      return <EmptyData sx={{ textAlign: 'center' }} heading='Auto Scaler is NOT configured' subHeading='' />;
    }
  };

  const renderAutoscalerLogs = () => {
    if (selectedCluster?.agent?.connection_status?.autoScalerEnabled) {
      return (
        <KubernetesAutoScalerLogs
          accountId={kubeId}
          namespace={selectedCluster?.agent?.connection_status?.autoScalerNamespace}
          autoscalerType={selectedCluster?.agent?.connection_status?.autoScalerType}
        />
      );
    }
  };

  const renderingAutoscalerConfiguringSuggestion = () => {
    if (selectedCluster?.k8s_provider === 'EKS') {
      return (
        <>
          <a
            style={{ display: 'block', marginBottom: '8px', marginLeft: '-7%' }}
            target='_blank'
            href='https://karpenter.sh/docs/getting-started/migrating-from-cas/'
            rel='noreferrer'
          >
            Karpenter: For a smooth setup, please follow the instructions in this documentation.
          </a>

          <p style={{ textAlign: 'center', fontWeight: 'bold', margin: '8px 0' }}>Or</p>

          <a
            style={{ display: 'block', marginLeft: '-7%' }}
            target='_blank'
            href='https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/aws/README.md'
            rel='noreferrer'
          >
            AWS Auto Scaling: Learn how to configure the Kubernetes Cluster Autoscaler.
          </a>
        </>
      );
    } else if (selectedCluster?.k8s_provider === 'Civo') {
      return (
        <a
          style={{ display: 'block', marginLeft: '-7%' }}
          target='_blank'
          href='https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/civo/README.md'
          rel='noreferrer'
        >
          Civo Auto Scaling: Learn how to configure the Kubernetes Cluster Autoscaler.
        </a>
      );
    } else if (selectedCluster?.k8s_provider === 'AKS') {
      return (
        <a
          style={{ display: 'block', marginLeft: '-7%' }}
          target='_blank'
          href='https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/azure/README.md'
          rel='noreferrer'
        >
          Azure Auto Scaling: Learn how to configure the Kubernetes Cluster Autoscaler.
        </a>
      );
    } else if (selectedCluster?.k8s_provider === 'GKE') {
      return (
        <a
          style={{ display: 'block', marginLeft: '-7%' }}
          target='_blank'
          href='https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-autoscaler'
          rel='noreferrer'
        >
          GKE Auto Scaling: Learn how to configure the Kubernetes Cluster Autoscaler.
        </a>
      );
    }
  };

  const getAutoscalerTabBasedOnAutoscalerType = () => {
    const commonTabs = [
      { value: 0, label: 'Summary', icon: OptimizeSummaryIcon },
      {
        value: 4,
        label: 'Logs',
        icon: LogsIcon,
        height: 17,
        width: 17,
      },
    ];
    const eventTab = { value: 3, label: 'Events', icon: EventIcon, height: 17, width: 17 };
    if (!selectedCluster?.agent?.connection_status?.autoScalerEnabled) {
      return [];
    }
    const autoScalerType = selectedCluster?.agent?.connection_status?.autoScalerType;
    switch (autoScalerType) {
      case 'karpenter':
        return [
          ...commonTabs,
          {
            value: 1,
            label: 'Node Pool',
            icon: ScalerNodePoolIcon,
          },
          { value: 2, label: 'Node Class', icon: ScalerNodeClassIcon },
          eventTab,
        ];

      case 'gke':
        return commonTabs;

      case 'cluster-autoscaler':
        return [
          ...commonTabs,
          {
            value: 1,
            label: 'Deployment File',
            icon: ScalerNodePoolIcon,
          },
          eventTab,
        ];

      default:
        return [];
    }
  };

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !tabOptions.length) return;
    const [fragment, subFragment] = hash.split('/');
    const filter = tabOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setSelectedTab(filter.value);
      if (!subFragment) return;
      const subTab = (filter?.tabOptions || []).find((tab) => tab.fragment === subFragment);
      if (subTab) {
        setSelectedSubTab(subTab.value);
      }
    }
  }, []);

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        p='var(--ds-space-1) var(--ds-space-5) 0px var(--ds-space-5)'
        tabPadding='var(--ds-space-1) var(--ds-space-3) 0px var(--ds-space-3)'
        options={tabOptions[selectedTab]?.options || []}
        filterOptions={tabOptions}
        showGroupedTabs={true}
        onChangeFilter={(val, subTabVal) => {
          setSelectedTab(val);
          setSelectedSubTab(subTabVal);
        }}
        showCustomRounded={(selectedSubTab === 8 && selectedTab === 1) || (selectedSubTab === 4 && selectedTab === 2)}
        showBorderBottom={(selectedSubTab === 8 && selectedTab === 1) || (selectedSubTab === 4 && selectedTab === 2)}
        tooltip={selectedTab === 2 ? 'Open events from the past 24 hours.' : ''}
      />
      <ErrorBoundary key={`${kubeId}-${selectedTab}-${selectedSubTab}`}>
        <Box>
          {[tabOptions[0].value].includes(selectedTab) &&
            (clusterSummary && Object.keys(clusterSummary).length > 0 ? (
              <>
                <KubernetesClusterSummary accountId={kubeId} clusterSummary={clusterSummary} />
                <KubernetesClusterSummaryUtilization accountId={kubeId} />
                <KuberneteComputeSummary
                  accountId={kubeId}
                  clusterSummary={clusterSummary}
                  id={tabOptions[0].options[1].id}
                  heading={tabOptions[0].options[1].name}
                />
                <KuberneteUtilizationSummary accountId={kubeId} id={tabOptions[0].options[2].id} heading={tabOptions[0].options[2].name} />
              </>
            ) : (
              <Loader style={{ width: '100%' }} />
            ))}
          {[tabOptions[1].value].includes(selectedTab) && (
            <>
              {selectedSubTab === 0 && (
                <KubernetesRightSizing
                  showUpdatedEmptyData={true}
                  kubernetes={{ id: kubeId }}
                  heading={''}
                  resourceIds={router?.query?.resourceIds}
                  groupName={router?.query?.groupName}
                />
              )}
              {selectedSubTab === 1 && (
                <KubernetesUnusedVolumes
                  showUpdatedEmptyData={true}
                  kubernetes={{ id: kubeId }}
                  heading={''}
                  resourceIds={router?.query?.resourceIds}
                  groupName={router?.query?.groupName}
                />
              )}
              {selectedSubTab === 2 && <KubernetesBestPractices showUpdatedEmptyData={true} kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 3 && <KubernetesAbandonedWorkloads showUpdatedEmptyData={true} kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 4 && <KubernetesPVCRightSizing showUpdatedEmptyData={true} kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 5 && <KubernetesReplicaRightSizing showUpdatedEmptyData={true} kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 6 && (
                <KubernetesSpotRecommendation
                  showUpdatedEmptyData={true}
                  kubernetes={{ id: kubeId }}
                  heading={''}
                  resourceIds={router?.query?.resourceIds}
                  groupName={router?.query?.groupName}
                />
              )}
              {selectedSubTab === 7 && <KubernetesOptimizeSummary showUpdatedEmptyData={true} kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 8 && (
                <>
                  <Box
                    sx={{
                      display: 'flex',
                      bgcolor: ds.background[100],
                      p: `${ds.space[1]} ${ds.space[3]} ${ds.space[3]} 0`,
                      borderRadius: `0 0 ${ds.radius.md} ${ds.radius.md}`,
                    }}
                  >
                    <ToggleGroup
                      selection='single'
                      ariaLabel='Autoscaler view'
                      value={String(scalerRadioTabValue)}
                      onChange={(v) => setScalerRadioTabValue(Number(v))}
                      options={getAutoscalerTabBasedOnAutoscalerType().map((item) => ({
                        value: String(item.value),
                        label: (
                          <Box display='flex' gap={'6px'} alignItems={'center'}>
                            {item?.icon && <SafeIcon src={item?.icon} height={item?.height || 20} width={item?.width || 20} alt={item.label} />}
                            {item.label}
                          </Box>
                        ),
                      }))}
                    />
                  </Box>
                  {!selectedCluster?.agent?.connection_status?.autoScalerEnabled && !selectedCluster?.agent?.connection_status?.karpenterEnabled ? (
                    <ListingLayout>
                      <ListingLayout.Body>
                        <EmptyData sx={{ textAlign: 'center' }} heading='Auto Scaler is NOT configured' subHeading=''>
                          {renderingAutoscalerConfiguringSuggestion()}
                        </EmptyData>
                      </ListingLayout.Body>
                    </ListingLayout>
                  ) : (
                    <>
                      {scalerRadioTabValue === 0 && (
                        <KubernetesNodesTrends
                          accountId={kubeId}
                          showZoneTrend={
                            (selectedCluster?.agent?.connection_status?.autoScalerEnabled &&
                              selectedCluster?.agent?.connection_status?.autoScalerType === 'karpenter') ||
                            selectedCluster?.agent?.connection_status?.karpenterEnabled
                          }
                        />
                      )}
                      {scalerRadioTabValue === 1 && renderingNodePool()}
                      {scalerRadioTabValue === 2 && <KubernetesNodeClass accountId={kubeId} />}
                      {scalerRadioTabValue === 3 && (
                        <KubernetesEventsTable
                          accountId={kubeId}
                          enableTrendChart={false}
                          heading={''}
                          defaultQuery={{ subject_type: ['nodeclaim'] }}
                          disabledFilters={['subjectType', 'namespace', 'workload', 'aggregationKey']}
                          stickyColumnIndex={'6'}
                        />
                      )}
                      {scalerRadioTabValue === 4 && renderAutoscalerLogs()}
                    </>
                  )}
                </>
              )}

              {selectedSubTab === 9 && <ListingRecommendationResolution accountId={kubeId} />}
            </>
          )}
          {[tabOptions[2].value].includes(selectedTab) && (
            <>
              {selectedSubTab === 0 && <KubernetesEventsSummary accountId={kubeId} />}
              {selectedSubTab === 1 && (
                <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                  <KubernetesGroupedEvents accountId={kubeId} />
                </Box>
              )}
              {selectedSubTab === 2 && (
                <>
                  <Box
                    sx={{
                      display: 'flex',
                      bgcolor: ds.background[100],
                      p: `${ds.space[1]} ${ds.space[3]} ${ds.space[3]} 0`,
                      borderRadius: `0 0 ${ds.radius.md} ${ds.radius.md}`,
                    }}
                  >
                    <ToggleGroup
                      selection='single'
                      ariaLabel='Pod error type'
                      value={podRadioTabValue}
                      onChange={setPodRadioTabValue}
                      options={[
                        { value: 'pod_oom_killer_enricher', label: 'OOM Killed', count: aggregationKeyCount?.pod_oom_killer_enricher ?? '' },
                        {
                          value: 'image_pull_backoff_reporter',
                          label: 'Image Pull Backoff',
                          count: aggregationKeyCount?.image_pull_backoff_reporter ?? '',
                        },
                        { value: 'report_crash_loop', label: 'High Restarts', count: aggregationKeyCount?.report_crash_loop ?? '' },
                        { value: 'CPUThrottlingHigh', label: 'CPU Throttling', count: aggregationKeyCount?.CPUThrottlingHigh ?? '' },
                        {
                          value: 'KubeDeploymentReplicasMismatch',
                          label: 'Replica Mismatch',
                          count: aggregationKeyCount?.KubeDeploymentReplicasMismatch ?? '',
                        },
                        { value: '__all__', label: 'All' },
                      ].map((item) => ({
                        value: item.value,
                        label: (
                          <Box display='flex' gap={'6px'} alignItems={'center'}>
                            {item.label}
                            {item.count && <CustomPill value={item.count} />}
                          </Box>
                        ),
                      }))}
                    />
                  </Box>
                  <KubernetesEventsTable
                    accountId={kubeId}
                    enableTrendChart={false}
                    heading={''}
                    defaultQuery={{
                      subject_type: podRadioTabValue == 'KubeDeploymentReplicasMismatch' ? 'deployment' : 'pod',
                      aggregation_key: podRadioTabValue === '__all__' ? undefined : podRadioTabValue,
                    }}
                    disabledFilters={['subjectType', 'source']}
                    podAllTabRadio={podRadioTabValue === '__all__' ? 'Error Type' : ''}
                  />
                </>
              )}
              {selectedSubTab === 3 && (
                <KubernetesEventsTable
                  podAllTabRadio={selectedSubTab == 3 ? 'Error Type' : ''}
                  accountId={kubeId}
                  enableTrendChart={false}
                  heading={''}
                  defaultQuery={{ subject_type: 'node' }}
                  disabledFilters={['subjectType', 'namespace', 'workload', 'source']}
                  stickyColumnIndex='7'
                />
              )}
              {selectedSubTab === 4 && (
                <>
                  <Box
                    sx={{
                      display: 'flex',
                      bgcolor: ds.background[100],
                      p: `${ds.space[1]} ${ds.space[3]} ${ds.space[3]} 0`,
                      borderRadius: `0 0 ${ds.radius.md} ${ds.radius.md}`,
                    }}
                  >
                    <ToggleGroup
                      selection='single'
                      ariaLabel='Application error type'
                      value={applicationRadioTabValue}
                      onChange={setApplicationRadioTabValue}
                      options={[
                        { value: 'application_api_error', label: 'Api Errors' },
                        { value: 'application_log_error', label: 'Log Errors' },
                        { value: '__all__', label: 'All' },
                      ]}
                    />
                  </Box>
                  {applicationRadioTabValue == '__all__' && (
                    <KubernetesEventsTable
                      accountId={kubeId}
                      enableTrendChart={false}
                      heading={''}
                      defaultQuery={{ aggregation_key: ['HighErrorCriticalLogs', 'ApplicationAPIFailures'] }}
                      disabledFilters={['aggregationKey', 'source']}
                      stickyColumnIndex={'6'}
                    />
                  )}
                  {applicationRadioTabValue == 'application_api_error' && (
                    <KubernetesApplicationApiFailure
                      stickyColumnIndex={'6'}
                      accountId={kubeId}
                      defaultQuery={{ aggregation_key: ['ApplicationAPIFailures'] }}
                    />
                  )}
                  {applicationRadioTabValue == 'application_log_error' && (
                    <KubernetesApplicationLogFailure
                      stickyColumnIndex={'5'}
                      accountId={kubeId}
                      defaultQuery={{ aggregation_key: ['HighErrorCriticalLogs'] }}
                    />
                  )}
                </>
              )}
              {selectedSubTab === 5 && (
                <KubernetesEventsTable
                  podAllTabRadio={selectedSubTab == 5 ? 'Error Type' : ''}
                  accountId={kubeId}
                  enableTrendChart={false}
                  heading={''}
                  defaultQuery={{}}
                  stickyColumnIndex={'7'}
                />
              )}
              {selectedSubTab === 6 && <KubernetesAnomaly accountId={kubeId} />}
              {selectedSubTab == 7 && <KubernetesGroupedEventsTable accountId={kubeId} groupEventType={'fingerprint'} isTroubleshootPage={false} />}
              {selectedSubTab == 8 && <TriageRulesManager accountId={kubeId} />}
            </>
          )}
          {[tabOptions[3].value].includes(selectedTab) && (
            <>
              {selectedSubTab == 0 && <KubernetesNodesTable accountId={kubeId} heading={''} />}
              {selectedSubTab == 1 && <KubernetesWorkloadsTable accountId={kubeId} />}
              {selectedSubTab == 2 && <KubernetesNamespaceTable accountId={kubeId} />}
              {selectedSubTab == 3 && <KubernetesPodsTable accountId={kubeId} />}
              {selectedSubTab == 4 && <KubernetesServices accountId={kubeId} />}
              {selectedSubTab == 5 && <KubernetesPVCTable accountId={kubeId} />}
              {selectedSubTab == 6 && <KubernetesPVTable accountId={kubeId} />}
              {selectedSubTab == 7 && <KubernetesDbmsTable accountId={kubeId} />}
              {selectedSubTab == 8 && <KubernetesQueueTable accountId={kubeId} />}
            </>
          )}
          {[tabOptions[4].value].includes(selectedTab) && (
            <>
              {selectedSubTab == 0 && (
                <KubernetesLogs
                  accountId={kubeId}
                  showTrend={false}
                  showQueryTextBox={true}
                  dateTime={{ startTime: new Date().getTime() - 3600 * 1000, endTime: new Date().getTime() }}
                  queryFromProps={''}
                />
              )}
              {selectedSubTab == 1 && <KubernetesLogsPattern accountId={kubeId} />}
              {selectedSubTab == 2 && <QueryMetrics accountId={kubeId} queriesToExecute={[]} />}
              {selectedSubTab == 3 && <KubernetesAlertManager accountId={kubeId} />}
              {selectedSubTab == 4 && <KubernetesServiceMapWrapper accountId={kubeId} showSourceType={true} />}
              {selectedSubTab == 5 && <KubernetesTracesListing accountId={kubeId} />}
              {selectedSubTab == 6 && <KubernetesTracesGroupListing accountId={kubeId} />}
              {selectedSubTab == 7 && <KubernetesTracesCrossZoneListing accountId={kubeId} />}
              {selectedSubTab == 8 && <KubernetesSLOConfigs accountId={kubeId} />}
              {selectedSubTab == 9 && hasWriteAccess(kubeId) && <GrafanaIframe accountId={kubeId} />}
            </>
          )}
          {[tabOptions[5].value].includes(selectedTab) && (
            <>
              {selectedSubTab === 0 && <KubernetesSecurity kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 1 && <KubernetesCisSecurityV2 kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 2 && <KubernetesLogSensitiveInfo accountId={kubeId} />}
              {selectedSubTab === 3 && <ClusterUpgradeFeature kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 4 && <KubernetesClusterUpgradePlanner accountId={kubeId} />}
              {selectedSubTab === 5 && <KubernetesSSLCertificateRecommendation kubernetes={{ id: kubeId }} heading={''} />}
              {selectedSubTab === 6 && <KubernetesHelmUpgradeRecommendation accountId={kubeId} heading={''} />}
            </>
          )}
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default KubernetesDetails;
