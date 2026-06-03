import { Box, Grid, Typography } from '@mui/material';
import React, { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import Currency from '@common-new/format/Currency';
import Text from '@common-new/format/Text';
import Datetime from '@common-new/format/Datetime';
import PropTypes from 'prop-types';
import LineChart from '@components1/common/charts/LineCharts';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { colors, ds } from 'src/utils/colors';
import { timeFormatIn24Hours } from '@lib/datetime';
import { toast as snackbar } from '@components1/ds/Toast';
import { Modal } from '@components1/ds/Modal';
import SafeIcon from '@components1/common/SafeIcon';
import { DataNotAvailable } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import apiAccount from '@api1/account';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';
import { Link as CustomLink } from '@components1/ds/Link';
import useRecommendationExport from '@hooks/useRecommendationExport';
import EmptyData from '@components1/common/EmptyData';
import Link from 'next/link';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/ds/Tooltip';

import WidgetCard from '@components1/ds/WidgetCard';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Stat } from '@components1/ds/Stat';
import { CostCallout } from '@components1/ds/CostCallout';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Comparison as DsComparison, ComparisonGroup as DsComparisonGroup } from '@components1/ds/Comparison';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';

const KubernetesReplicaRightSizingDrilldown = (props) => {
  const loading = !props.data?.recommendation?.recommendation;

  function formatTime(t) {
    let d = new Date(t);
    return timeFormatIn24Hours(d.getTime());
  }

  return (
    <Box
      sx={{
        width: '100%',
        overflow: 'hidden',
        backgroundColor: colors.background.white,
        border: `1px solid ${colors.border.secondaryLight}`,
        borderRadius: 'var(--ds-radius-md)',
        p: 'var(--ds-space-3)',
      }}
    >
      <Grid container sx={{ mb: 'var(--ds-space-5)' }} spacing={2}>
        <Grid item xs={6}>
          <LineChart
            colors={[colors.text.cpuRecommendation]}
            data={
              Array.isArray(props?.data?.recommendation?.recommendation?.allocated)
                ? props.data.recommendation.recommendation.allocated.map((v) => v.replicas)
                : []
            }
            labels={
              Array.isArray(props?.data?.recommendation?.recommendation?.allocated)
                ? props.data.recommendation.recommendation.allocated.map((v) => formatTime(v.timestamp))
                : []
            }
            chartLabel={['Historical Replicas']}
            loading={loading}
            scaleOptions={{
              y: {
                ticks: {
                  callback: (value) => (Number.isInteger(value) ? value : null),
                  stepSize: 1,
                },
              },
            }}
          />
        </Grid>
        <Grid item xs={6}>
          <LineChart
            colors={[colors.text.red]}
            data={
              Array.isArray(props?.data?.recommendation?.recommendation?.recommended)
                ? props.data.recommendation.recommendation.recommended.map((v) => v.replicas)
                : []
            }
            labels={
              Array.isArray(props?.data?.recommendation?.recommendation?.recommended)
                ? props.data.recommendation.recommendation.recommended.map((v) => formatTime(v.timestamp))
                : []
            }
            chartLabel={['Predicted Replicas']}
            loading={loading}
            scaleOptions={{
              y: {
                ticks: {
                  callback: (value) => (Number.isInteger(value) ? value : null),
                  stepSize: 1,
                },
              },
            }}
          />
        </Grid>
      </Grid>
      <Grid container sx={{ mb: 'var(--ds-space-5)' }} spacing={2}>
        <Grid item xs={6}>
          <LineChart
            colors={[colors.text.cpuUsage]}
            data={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => v.cpu)
                : []
            }
            labels={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => formatTime(v.timestamp))
                : []
            }
            chartLabel={['CPU Usage (Cores)']}
            loading={loading}
          />
        </Grid>
        <Grid item xs={6}>
          <LineChart
            colors={[colors.text.memoryRecommendation]}
            data={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => v.memory)
                : []
            }
            labels={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => formatTime(v.timestamp))
                : []
            }
            chartLabel={['Memory Usage (Mi)']}
            loading={loading}
          />
        </Grid>
      </Grid>
      <Grid container sx={{ mb: 'var(--ds-space-5)' }} spacing={2}>
        <Grid item xs={6}>
          <LineChart
            colors={[colors.text.tertiary]}
            data={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => v.rps)
                : []
            }
            labels={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => formatTime(v.timestamp))
                : []
            }
            chartLabel={['Requests Per Second']}
            loading={loading}
          />
        </Grid>
        <Grid item xs={6}>
          <LineChart
            colors={[colors.text.purple]}
            data={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => v.latency)
                : []
            }
            labels={
              Array.isArray(props?.data?.recommendation?.recommendation.evidence)
                ? props.data.recommendation.recommendation.evidence.map((v) => formatTime(v.timestamp))
                : []
            }
            chartLabel={['Latency (s)']}
            loading={loading}
          />
        </Grid>
      </Grid>
    </Box>
  );
};

KubernetesReplicaRightSizingDrilldown.propTypes = {
  data: PropTypes.object,
};

const KubernetesReplicaRightSizing = ({ isOptimisePage, enabledSummary = true, enabledFilters = true, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const kubernetesRightSizingTable = 'kubernetesRightSizingTable';

  const [kubernetesAbandonedWorkloads, setKubernetesAbandonedWorkloads] = useState([]);
  const [kubernetesAbandonedWorkloadsCount, setKubernetesAbandonedWorkloadsCount] = useState(0);
  const [totalRecommendationsCount, setTotalRecommendationsCount] = useState(0);
  const [totalEstimatedSavings, setTotalEstimatedSavings] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [resourceIds, setResourceIds] = useState([]);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace || '');
  const [recommendationStatus, setRecommendationStatus] = useState('InProgress');
  const [loading, setLoading] = useState(false);
  const [namespaces, setNamespaces] = useState([]);
  const [listAutoPilots, setListAutoPilots] = useState();
  const [isAutoPilotHorizontalFormOpen, setIsAutoPilotHorizontalFormOpen] = useState(false);
  const [autoPilotData, setAutoPilotData] = useState({});
  const [isLoading, setIsLoading] = useState(false);
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
  const [msTeamsData, setMsTeamsData] = useState([]);
  const [googleChannelList, setGoogleChannelList] = useState([]);
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState(false);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(props?.kubernetes?.id);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id);
  }, [props?.kubernetes?.id]);

  const { allCluster, selectedCluster } = useData();

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'replica_right_sizing',
    namespace: selectedNamespace,
    status: recommendationStatus,
  });

  useEffect(() => {
    if (router.isReady && router.query.namespace) {
      if (namespaces && namespaces.length > 0) {
        const namespaceExists = namespaces.find((ns) => ns === router.query.namespace);
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
  }, [router.isReady, router.query.namespace, namespaces]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    setPage(0);
    recommendationApi.getAutoOptimize({ accountId: selectedAccountId, category: ['horizontal_rightsize'], status: 'Active' }).then((res) => {
      const activeAutoPilots = res?.data?.auto_pilot ?? [];
      setListAutoPilots(activeAutoPilots);
    });
  }, [selectedAccountId, recommendationStatus]);

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

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

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Name**: ' + data?.recommendation?.metadata?.name + '\n';
    description += '**Namespaces**: ' + data?.recommendation?.metadata?.namespace + '\n';
    description += '**Rule**: ' + data?.recommendation?.recommendation?.recommended_type + '\n';
    const recommendationType = data?.recommendation?.recommendation?.recommended_type;
    let recommendationDescription = '';

    if (recommendationType === 'SPOT_INSTANCE_DEPLOYMENT') {
      recommendationDescription = 'Application deployed on spot instances should have more than 1 replica or HPA enabled for High Availability';
    } else if (recommendationType === 'NB_ML') {
      recommendationDescription = 'AI/ML Optimised Resource Allocation with Replica Right Sizing';
    } else {
      recommendationDescription = 'Application Usage are higher than configured Requests, Its recommended to enable HPA or increase replicas';
    }

    description += '**Description**: ' + recommendationDescription + '\n';
    return description;
  };

  const getRecommendedReplicas = (recommendation, type) => {
    if (type === 'allocated') {
      if (recommendation?.allocated_replica) {
        return recommendation?.allocated_replica;
      } else if (recommendation?.allocated) {
        return recommendation?.allocated[recommendation?.allocated.length - 1]?.replicas || '-';
      }
    } else if (type === 'recommended') {
      if (recommendation?.recommended_replica) {
        return recommendation?.recommended_replica;
      } else if (recommendation?.recommended && recommendation?.recommended.length > 0) {
        const localDate = new Date();
        localDate.setUTCMinutes(0, 0, 0);
        localDate.setUTCHours(localDate.getUTCHours() + 1);
        const utcDateTime = localDate.toISOString().replace('T', ' ').substring(0, 19);
        return recommendation?.recommended?.filter((g) => g.timestamp === utcDateTime)?.[0]?.replicas || '-';
      }
    }
    return '-';
  };

  const getRecommendedTypeText = (recommendedType) => {
    switch (recommendedType) {
      case 'SPOT_INSTANCE_DEPLOYMENT':
        return 'Spot Deployment';
      case 'NB_ML':
        return 'Replica RightSizing';
      default:
        return 'Usage';
    }
  };

  const getRecommendedTypeComponent = (recommendedType, item) => {
    switch (recommendedType) {
      case 'SPOT_INSTANCE_DEPLOYMENT':
        return (
          <Text
            value={'Application deployed on spot instances should have more than 1 replica or HPA enabled for High Availability'}
            showAutoEllipsis
          />
        );
      case 'NB_ML':
        return <Text value={'AI/ML Optimised Resource Allocation with Replica Right Sizing'} showAutoEllipsis />;
      default:
        return (
          <Box>
            <Text
              value={'Application Usage are higher than configured Requests, Its recommended to enable HPA or increase replicas'}
              showAutoEllipsis
            />
            <br />
            <Text value={'Memory Request: ' + item?.recommendation?.recommendation?.usage?.memory_request} showAutoEllipsis />
            <Text value={'Memory Usage: ' + item?.recommendation?.recommendation?.usage?.memory_usage} showAutoEllipsis />
          </Box>
        );
    }
  };

  const handleResolved = (row) => {
    const existingAutoPilot = listAutoPilots?.find((pilot) => pilot.id === row.id);

    if (existingAutoPilot) {
      setAutoPilotData(existingAutoPilot);
    }

    addHorizontalAutoPilot();
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

  const addHorizontalAutoPilot = () => {
    getChannelsListSlackMsTeams();
    setIsAutoPilotHorizontalFormOpen(true);
  };

  const closeAutoPilotHorizontalConfigModal = (success) => {
    setIsAutoPilotHorizontalFormOpen(false);
    setNotificationData({
      ...notificationData,
      slack: false,
      teams: false,
      google_chat: false,
    });
    setGoogleChannelList([]);
    if (success) {
      snackbar.success('Auto Optimize Created Successfully');
    }
  };

  const listRecommendations = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (!Array.isArray(listAutoPilots)) {
      return;
    }
    setLoading(true);
    setKubernetesAbandonedWorkloads([]);
    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'replica_right_sizing',
        status: recommendationStatus ? [recommendationStatus] : [],
        resourceNamespace: selectedNamespace,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        const rawItems = res?.data?.recommendation || [];
        setKubernetesAbandonedWorkloadsCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        let rIds = [];
        let k8sRecommendationData = rawItems.map((item) => {
          let data = [];
          let hasAutopilotConfigured = false;
          let autoPilotId;
          let autoPilotCategory;
          const itemName = item?.recommendation?.metadata?.name;
          const itemNamespace = item?.recommendation?.metadata?.namespace;

          for (let a of listAutoPilots) {
            for (let r of a.auto_optimize_resource_maps) {
              if (r?.resource_identifier?.name == itemName && r?.resource_identifier?.namespace == itemNamespace) {
                hasAutopilotConfigured = true;
                autoPilotId = a.id;
                autoPilotCategory = a.category;
                break;
              }
              if (r?.resource_identifier?.name == null && r?.resource_identifier?.namespace == itemNamespace) {
                hasAutopilotConfigured = true;
                autoPilotId = a.id;
                autoPilotCategory = a.category;
                break;
              }
            }
            if (hasAutopilotConfigured) {
              break;
            }
          }

          item.hasAutopilotConfigured = hasAutopilotConfigured;
          item.autoPilotCategory = autoPilotCategory;
          let name = item?.recommendation?.metadata?.name;
          let nameSpace = item?.recommendation?.metadata?.namespace;
          rIds.push(item.cloud_resourse.id);

          data.push({
            component: (
              <>
                <Text value={name} showAutoEllipsis />
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
                {item.ticket !== undefined ? <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} /> : ''}
                {item?.recommendation?.recommendation?.error !== undefined && (
                  <Text
                    sx={{ color: colors.text.red, fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}
                    value={item?.recommendation?.recommendation?.error}
                  />
                )}
              </>
            ),
            drilldownQuery: {
              data: item,
              name: name,
            },
          });
          data.push({ component: <Text value={nameSpace || '-'} showAutoEllipsis /> });
          const allocatedReplicas = getRecommendedReplicas(item?.recommendation?.recommendation, 'allocated');
          const recommendedReplicas = getRecommendedReplicas(item?.recommendation?.recommendation, 'recommended');
          data.push({
            component: (
              <DsComparisonGroup spacing='xs'>
                <DsComparison
                  size='sm'
                  polarity='lower-is-better'
                  before={{ value: typeof allocatedReplicas === 'number' ? allocatedReplicas : Number(allocatedReplicas) || null }}
                  after={{ value: typeof recommendedReplicas === 'number' ? recommendedReplicas : Number(recommendedReplicas) || null }}
                />
              </DsComparisonGroup>
            ),
            data: recommendedReplicas,
          });
          data.push({
            component: <Text value={getRecommendedTypeText(item?.recommendation?.recommendation?.recommended_type)} showAutoEllipsis />,
            data: getRecommendedTypeText(item?.recommendation?.recommendation?.recommended_type),
          });
          data.push({
            component: getRecommendedTypeComponent(item?.recommendation?.recommendation?.recommended_type, item),
            data: getRecommendedTypeText(item?.recommendation?.recommendation?.recommended_type),
          });
          data.push({
            component: <Text value={(item?.recommendation?.duration || '7') + ' D'} />,
          });
          data.push({
            component: <Currency value={item?.estimated_savings || '-'} precison={1} />,
            data: item?.estimated_savings,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          const autoPilotIdRef = autoPilotId;
          const hasAutopilotConfiguredRef = hasAutopilotConfigured;
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
                    id={`rrs-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={() => {
                      handleResolved({
                        id: autoPilotIdRef,
                        resourceId: item.cloud_resourse.id,
                        data: item,
                        category: autoPilotCategory,
                      });
                    }}
                  >
                    {hasAutopilotConfiguredRef ? 'Configured' : 'Optimize'}
                  </DsButton>
                )}
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <span>
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      aria-label={`Ask ${assistantName}`}
                      id={`replica-rs-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
                        const prompt = buildNubiOptimizePrompt({
                          ruleName: 'Replica Right Sizing',
                          category: 'RightSizing',
                          severity: item.severity || 'Info',
                          resourceName: name || '',
                          namespace: nameSpace || '',
                          accountName: isOptimisePage ? getAccountName(item.account_id) : undefined,
                          estimatedSavings: item.estimated_savings || undefined,
                          brief: `${getRecommendedTypeText(
                            item?.recommendation?.recommendation?.recommended_type
                          )}: Current replicas: ${allocatedReplicas}, Recommended: ${recommendedReplicas}.`,
                        });
                        setNubiQuery(prompt);
                        setNubiAccountId(item.account_id || selectedAccountId);
                        setNubiConversationId(`recom_${item.id}`);
                        setNubiSidebarVisible(true);
                      }}
                    />
                  </span>
                </CustomTooltip>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `rrs-action-ticket-${item.id}`,
                      label: item.ticket?.ticket_id ? `Ticket: ${item.ticket.ticket_id}` : 'Create ticket',
                      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
                      disabled: !!item.ticket?.ticket_id,
                      onSelect: () => {
                        setTicketData(item);
                        setIsTicketCreateFormOpen(true);
                      },
                    },
                  ]}
                  trigger={
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<MoreVertIcon />}
                      aria-label='More actions'
                      id={`rrs-action-menu-${item.id}`}
                    />
                  }
                />
              </Box>
            ),
          });

          return data;
        });
        setKubernetesAbandonedWorkloads(k8sRecommendationData);
        setResourceIds(rIds);
      })
      .catch(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    listRecommendations();
  }, [selectedAccountId, page, recommendationStatus, selectedNamespace, rowsPerPage, listAutoPilots?.length, accounts.length]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (isOptimisePage && accounts.length === 0) {
      return;
    }
    setTotalRecommendationsCount(0);
    setTotalEstimatedSavings(0);
    recommendationApi
      .getK8sRecommendationSummary({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'replica_right_sizing',
        status: ['Open', 'InProgress'],
      })
      .then((res) => {
        setTotalRecommendationsCount(res?.data?.recommendation_aggregate.aggregate.count);
        setTotalEstimatedSavings(res?.data?.recommendation_aggregate.aggregate.sum?.estimated_savings);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [selectedAccountId]);

  useEffect(() => {
    if (resourceIds?.length === 0) {
      return;
    }
    k8sApi.listCurrentReplicaByResourceIds(resourceIds).then((res) => {
      for (const [index] of kubernetesAbandonedWorkloads.entries()) {
        const foundItem = res.data?.find(
          (item) =>
            item.namespace === kubernetesAbandonedWorkloads[index][0].drilldownQuery.data.resource_k8s_namespace &&
            item.name === kubernetesAbandonedWorkloads[index][0]?.drilldownQuery.name
        );
        if (foundItem) {
          const existingCell = kubernetesAbandonedWorkloads[index][2];
          const existingAfter = (existingCell && existingCell.data) || null;
          const recommendedNum = typeof existingAfter === 'number' ? existingAfter : Number(existingAfter) || null;
          kubernetesAbandonedWorkloads[index][2] = {
            component: (
              <DsComparisonGroup spacing='xs'>
                <DsComparison
                  size='sm'
                  polarity='lower-is-better'
                  before={{ value: typeof foundItem.total_pods === 'number' ? foundItem.total_pods : Number(foundItem.total_pods) || null }}
                  after={{ value: recommendedNum }}
                />
              </DsComparisonGroup>
            ),
            data: foundItem.total_pods,
          };
        }
      }
      setKubernetesAbandonedWorkloads([...kubernetesAbandonedWorkloads]);
    });
  }, [resourceIds]);

  useEffect(() => {
    if (!selectedAccountId) {
      return;
    }

    recommendationApi
      .listRecommendationNamesapces({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'replica_right_sizing',
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaces(res);
      });
  }, [selectedAccountId, recommendationStatus]);

  const handleTicketSuccess = () => {
    listRecommendations();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const tableHeaders = [
    { name: 'App Name', width: '14%' },
    { name: 'Namespace', width: '10%' },
    { name: 'Replicas (Current → Recommended)', width: '14%' },
    { name: 'Rule', width: '10%' },
    { name: 'Details', width: '20%' },
    { name: 'Observation Duration', width: '8%' },
    { name: 'Savings/mo', width: '10%' },
    { name: 'Updated At', width: '10%' },
    { name: '', width: '10%' },
  ];

  if (!isOptimisePage && !selectedCluster?.agent?.connection_status?.prometheusConnection) {
    return (
      <WidgetCard id='replica-rightsizing' sx={{ mt: 0, mb: 0, padding: ds.space[4] }}>
        <EmptyData
          img={DataNotAvailable}
          heading='Agent Not Connected'
          subHeading='Prometheus is not connected for this cluster. Connect an agent to start monitoring.'
        >
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mt: 'var(--ds-space-2)' }}>
            Check the{' '}
            <Link href={`/agentHealth?accountId=${selectedAccountId}#agent`} style={{ color: 'var(--ds-blue-600)' }}>
              Agent Health
            </Link>{' '}
            page for connection details.
          </Typography>
        </EmptyData>
      </WidgetCard>
    );
  }

  const heading = props.heading === undefined ? 'Replica Rightsizing' : props.heading;

  return (
    <>
      <Modal
        width='lg'
        loader={isLoading}
        open={isAutoPilotHorizontalFormOpen}
        handleClose={() => closeAutoPilotHorizontalConfigModal(false)}
        title={'Horizontal Auto Optimize Configuration'}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: '1100px',
            '& .MuiDialogContent-root': {
              padding: 'var(--ds-space-4) var(--ds-space-6)',
            },
          },
        }}
      >
        <AutoOptimizeHorizontalRightSizingSingleConfiguration
          autoOptimizeData={autoPilotData}
          closeAutoPilotSingleConfigModal={closeAutoPilotHorizontalConfigModal}
          msTeamsData={msTeamsData}
          googleChannelList={googleChannelList}
          isMsTeamsLoading={isMsTeamsLoading}
          isGoogleChannelsLoading={isGoogleChannelsLoading}
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
          subject: 'Update Replica Size - ' + ticketData.recommendation?.metadata?.name,
          description: getTicketDescription(ticketData),
          accountId: ticketData.account_id || selectedAccountId,
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
              info={{ tooltip: 'Active replica right-sizing recommendations' }}
              value={Number.isFinite(totalRecommendationsCount) ? totalRecommendationsCount.toLocaleString() : totalRecommendationsCount ?? '—'}
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Savings Potential'
              info={{ tooltip: 'Estimated monthly savings if every recommendation is applied' }}
              value={
                totalEstimatedSavings == null || totalEstimatedSavings === '-' ? (
                  '—'
                ) : (
                  <CostCallout size='lg' tone='high-savings' value={Number(totalEstimatedSavings) || 0} period='/ mo' />
                )
              }
            />
          </WidgetCard>
        </Box>
      )}
      <ListingLayout id='replica-rightsizing'>
        <ListingLayout.Toolbar
          title={heading}
          data-testid='rrs-filter-toolbar'
          actions={
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
                  id='rrs-download'
                />
              }
            />
          }
        >
          {enabledFilters && (
            <>
              {isOptimisePage && (
                <FilterDropdown
                  id='rrs-filter-account'
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
                id='rrs-filter-namespace'
                label='Namespace'
                options={(namespaces || []).map((n) => ({ label: n, value: n }))}
                value={selectedNamespace ? { label: selectedNamespace, value: selectedNamespace } : null}
                onSelect={(_e, item) => {
                  const next = item?.value || '';
                  setSelectedNamespace(next);
                  applyFiltersOnRouter(router, { namespace: next });
                  setPage(0);
                }}
              />
              <FilterDropdown
                id='rrs-filter-status'
                label='Status'
                options={RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }))}
                value={
                  recommendationStatus
                    ? {
                        label: recommendationStatus === 'InProgress' ? 'In Progress' : recommendationStatus,
                        value: recommendationStatus,
                      }
                    : null
                }
                onSelect={(_e, item) => {
                  setRecommendationStatus(item?.value || '');
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
            tableData={kubernetesAbandonedWorkloads}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesAbandonedWorkloadsCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            stickyColumnIndex='9'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'Details',
                  componentFn: (_option, drilldownQuery) => (
                    <Box sx={{ pt: 'var(--ds-space-3)' }}>
                      <KubernetesReplicaRightSizingDrilldown data={drilldownQuery.data} />
                    </Box>
                  ),
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

KubernetesReplicaRightSizing.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
  isOptimisePage: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  enabledFilters: PropTypes.bool,
};

export default KubernetesReplicaRightSizing;
