import { Box, Grid, IconButton, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import Currency from '@components1/common/format/Currency';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import NumberComponent from '@components1/common/format/Number';
import Text from '@components1/common/format/Text';
import Datetime from '@components1/common/format/Datetime';
import PropTypes from 'prop-types';
import LineChart from '@components1/common/charts/LineCharts';
import { action } from 'src/utils/actionStyles';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { colors } from 'src/utils/colors';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import { timeFormatIn24Hours } from '@lib/datetime';
import { snackbar } from '@components1/common/snackbarService';
import { Modal } from '@components1/common/modal';
import SafeIcon from '@components1/common/SafeIcon';
import { AutoPilotGreyIcon, DataNotAvailable } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import apiAccount from '@api1/account';
import CustomButton from '@components1/common/NewCustomButton';
import ButtonMenu from '@components1/common/ButtonMenu';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';
import CustomLink from '@components1/common/CustomLink';
import useRecommendationExport from '@hooks/useRecommendationExport';
import EmptyData from '@components1/common/EmptyData';
import Link from 'next/link';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';

const BEST_PRACTICES_HEADER = [
  { name: 'App Name', width: '10%' },
  { name: 'Namespaces', width: '5%' },
  { name: 'Current Replicas', width: '5%' },
  { name: 'Recommended Replicas', width: '10%' },
  { name: 'Rule', width: '10%' },
  { name: 'Details', width: '20%' },
  { name: 'Observation Duration', width: '10%' },
  { name: 'Estimated Savings', width: '10%' },
  { name: 'Updated At', width: '10%', sortEnabled: true },
  { name: '', width: '5%' },
];

const KubernetesReplicaRightSizingDrilldown = (props) => {
  const loading = !props.data?.recommendation?.recommendation;

  function formatTime(t) {
    let d = new Date(t);
    return timeFormatIn24Hours(d.getTime());
  }

  return (
    <>
      <Grid container sx={{ mb: '24px' }} spacing={2}>
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
                  callback: (value) => (Number.isInteger(value) ? value : null), // Only show integers
                  stepSize: 1, // Force steps of 1
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
                  callback: (value) => (Number.isInteger(value) ? value : null), // Only show integers
                  stepSize: 1, // Force steps of 1
                },
              },
            }}
          />
        </Grid>
      </Grid>
      <Grid container sx={{ mb: '24px' }}>
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
      <Grid container sx={{ mb: '24px' }}>
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
    </>
  );
};

KubernetesReplicaRightSizingDrilldown.propTypes = {
  data: PropTypes.object,
};

function kubernetesReplicaRightSizingDrilldownFn(accountId, drilldownQuery, _row) {
  return <KubernetesReplicaRightSizingDrilldown data={drilldownQuery.data} />;
}

const KubernetesReplicaRightSizing = ({ isOptimisePage, ...props }) => {
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

  const [sortObject, setSortObject] = useState({});

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
    //generate ticket description
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

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
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
        const utcDateTime = localDate.toISOString().replace('T', ' ').substring(0, 19); // 2025-01-12 20:00:00
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
    const orderBy = {};
    if (sortObject?.name == 'Updated At') {
      orderBy.name = 'updated_at';
      orderBy.order = sortObject.order;
    } else if (sortObject?.name == 'Estimated Savings') {
      orderBy.name = 'estimated_savings';
      orderBy.order = sortObject.order;
    }
    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'replica_right_sizing',
        status: recommendationStatus ? [recommendationStatus] : [],
        resourceNamespace: selectedNamespace,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        orderBy: orderBy,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        setKubernetesAbandonedWorkloadsCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        let rIds = [];
        let k8sRecommendationData = res?.data?.recommendation.map((item) => {
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
          let MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
              disabled: item?.ticket !== undefined,
            },
          ];
          let name = item?.recommendation?.metadata?.name;
          let nameSpace = item?.recommendation?.metadata?.namespace;
          rIds.push(item.cloud_resourse.id);

          data.push({
            component: (
              <>
                <Text value={name} showAutoEllipsis />
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
                {item.ticket !== undefined ? <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} /> : ''}
                {item?.recommendation?.recommendation?.error !== undefined && (
                  <Text sx={{ color: colors.text.red, fontSize: '11px', fontWeight: '500' }} value={item?.recommendation?.recommendation?.error} />
                )}
              </>
            ),
            drilldownQuery: {
              data: item,
              name: name,
            },
          });
          data.push({ component: <Text value={nameSpace || '-'} showAutoEllipsis /> });
          data.push({
            component: (
              <NumberComponent
                value={getRecommendedReplicas(item?.recommendation?.recommendation, 'allocated')}
                sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}
              />
            ),
          });
          data.push({
            component: <NumberComponent value={getRecommendedReplicas(item?.recommendation?.recommendation, 'recommended')} />,
            data: getRecommendedReplicas(item?.recommendation?.recommendation, 'recommended'),
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
            text: <Text value={(item?.recommendation?.duration || '7') + ' D'} />,
          });
          data.push({
            component: <Currency value={item?.estimated_savings || '-'} precison={1} />,
            data: item?.estimated_savings,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
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
                    data-testid={`replica-rs-ask-nubi-${item.id}`}
                    onClick={(event) => {
                      event.stopPropagation();
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
                        )}: Current replicas: ${getRecommendedReplicas(
                          item?.recommendation?.recommendation,
                          'allocated'
                        )}, Recommended: ${getRecommendedReplicas(item?.recommendation?.recommendation, 'recommended')}.`,
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
                {hasWriteAccess(item.account_id || selectedAccountId) && hasAutopilotConfigured && (
                  <CustomButton
                    text='Pilot on'
                    variant='secondary'
                    startIcon={<SafeIcon src={AutoPilotGreyIcon} alt='autopilot' width={14} height={14} />}
                    size='Small'
                    onClick={(event) => {
                      event.stopPropagation();
                      handleResolved({
                        id: autoPilotId,
                        resourceId: item.cloud_resourse.id,
                        data: item,
                        category: autoPilotCategory,
                      });
                    }}
                    sx={{
                      fontSize: '12px',
                      padding: '4px 8px',
                      minWidth: '90px',
                    }}
                  />
                )}
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
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
  }, [selectedAccountId, page, recommendationStatus, selectedNamespace, rowsPerPage, sortObject, listAutoPilots?.length, accounts.length]);

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
          kubernetesAbandonedWorkloads[index][2] = {
            component: <NumberComponent value={foundItem.total_pods} sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }} />,
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

  if (!isOptimisePage && !selectedCluster?.agent?.connection_status?.prometheusConnection) {
    return (
      <BoxLayout2
        heading={props.heading === undefined ? 'Replica Rightsizing' : props.heading}
        id='replica-rightsizing'
        sharingOptions={{ sharing: { enabled: false } }}
      >
        <EmptyData
          img={DataNotAvailable}
          heading='Agent Not Connected'
          subHeading='Prometheus is not connected for this cluster. Connect an agent to start monitoring.'
        >
          <Typography sx={{ fontSize: '13px', color: '#6B7280', mt: '8px' }}>
            Check the{' '}
            <Link href={`/agentHealth?accountId=${selectedAccountId}#agent`} style={{ color: '#3B82F6' }}>
              Agent Health
            </Link>{' '}
            page for connection details.
          </Typography>
        </EmptyData>
      </BoxLayout2>
    );
  }

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
              padding: '16px 40px',
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
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={totalRecommendationsCount} />
        <SummaryWidget
          title='Savings Potential'
          variant='savings'
          value={
            <Currency
              value={totalEstimatedSavings}
              suffix='/mo'
              sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
              withTooltip={false}
              isSavingPotential={true}
              recommendationLabel='Some of replica rightsizing recommendations'
            />
          }
        />
      </Box>
      <BoxLayout2
        heading={props.heading === undefined ? 'Best Practices' : props.heading}
        id='best-practices'
        filterOptions={[
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
            label: 'Status',
            options: RECOMMENDATION_STATUS,
            value: recommendationStatus,
            onSelect: function (e, _rule) {
              setRecommendationStatus(e?.target?.value);
              setPage(0);
            },
          },
          {
            type: 'dropdown',
            label: 'Namespace',
            options: namespaces,
            value: selectedNamespace,
            onSelect: function (e) {
              setSelectedNamespace(e?.target?.value);
              applyFiltersOnRouter(router, { namespace: e?.target?.value });
              setPage(0);
            },
          },
        ]}
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
          />,
        ]}
      >
        <KubernetesTable2
          id={kubernetesRightSizingTable}
          headers={BEST_PRACTICES_HEADER}
          data={kubernetesAbandonedWorkloads}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesAbandonedWorkloadsCount}
          onPageChange={changePage}
          stickyColumnIndex='10'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          sort={sortObject}
          loading={loading}
          showExpandable={true}
          expandable={{
            tabs: [
              {
                componentFn: kubernetesReplicaRightSizingDrilldownFn,
                text: 'Replica Prediction',
              },
            ],
          }}
          pageNumber={page + 1}
          onSortChange={(e) => setSortObject(e)}
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

KubernetesReplicaRightSizing.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesReplicaRightSizing;
