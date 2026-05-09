import { Box, IconButton, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import SyncIcon from '@mui/icons-material/Sync';
import recommendationApi, { RECOMMENDATION_STATUS, RECOMMENDATION_SERVERITY } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { unique } from '@lib/collections';
import SeverityIcon from '@common/widgets/SeverityIcon';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Datetime from '@components1/common/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { action } from 'src/utils/actionStyles';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { colors } from 'src/utils/colors';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import { snackbar } from '@components1/common/snackbarService';
import { snakeToTitleCase } from 'src/utils/common';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';

const BEST_PRACTICES_HEADER = [
  { name: 'Name', width: '10%' },
  { name: 'Severity', width: '5%' },
  { name: 'Object Type', width: '10%' },
  { name: 'Namespaces', width: '5%' },
  { name: 'Object Names', width: '15%' },
  { name: 'Updated At', width: '10%' },
  { name: 'Description', width: '45%' },
  '',
];
const RULE_LABEL_MAP = {
  configmaps_misconfigurations: 'Unused ConfigMaps',
  misconfigurations: 'Misconfiguration',
  clusterroles_misconfigurations: 'ClusterRole Issues',
  namespaces_misconfigurations: 'Namespace Config Issues',
  nodes_misconfigurations: 'Node Issues',
  persistentvolumeclaims_misconfigurations: 'PVC Issues',
  persistentvolumes_misconfigurations: 'PV Issues',
  poddisruptionbudgets_misconfigurations: 'Pod Disruption Budget Issues',
  pods_misconfigurations: 'Pod Disruption Budget Issues',
  serviceaccounts_misconfigurations: 'Service Account Issues',
  services_misconfigurations: 'Service Issues',
  statefulsets_misconfigurations: 'Staefulsets Issues',
  health_check: 'Health Check',
};

const KubernetesBestPractices = (props) => {
  const { assistantName } = useTenantBranding();
  const [kubernetesBestPractice, setKubernetesBestPractice] = useState([]);
  const [kubernetesBestPracticeCount, setKubernetesBestPracticeCount] = useState(0);
  const [totalBestPracticeCount, setTotalBestPracticeCount] = useState(0);
  const [ruleName, setRuleName] = useState('');
  const [severity, setSeverity] = useState('');
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [namespace, setNamespace] = useState('');
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [refreshTime, setRefreshTime] = useState({});
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  const kubernetesBestPracticesTable = 'kubernetesBestPracticesTable';
  const { selectedCluster } = useData();
  let jobName = 'popeye_scan';

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
    if (!props?.kubernetes?.id) {
      return;
    }
    recommendationApi
      .listRecommendationNamesapces({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: ruleName,
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaceFilter(res);
      })
      .catch(() => {
        setNamespaceFilter([]);
      });
  }, [props?.kubernetes?.id, recommendationStatus, ruleName]);

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    //generate ticket description
    let description = '';
    description += '**Name**: ' + (RULE_LABEL_MAP[data.rule_name] || snakeToTitleCase(data.rule_name)) + '\n';
    description += '**Severity**: ' + data?.severity + '\n';

    if (data.rule_name === 'health_check' && data.recommendation?.workload) {
      description += '**Object Type**: ' + data.recommendation.workload.kind + '\n';
      description += '**Namespace**: ' + data.recommendation.workload.namespace + '\n';
      description += '**Object Name**: ' + data.recommendation.workload.name + '\n';
      description += '**Issues**: ' + (data.recommendation.messages?.join(', ') || '-') + '\n';
    } else if (Array.isArray(data.recommendation)) {
      description += '**Object Type**: ' + unique(data.recommendation?.map?.((r) => r?.kind))?.join(', ') + '\n';
      description += '**Namespaces**: ' + unique(data.recommendation?.map?.((r) => r?.namespace))?.join(', ') + '\n';
      description += '**Object Names**: ' + unique(data.recommendation?.map?.((r) => r?.name))?.join(', ') + '\n';
      description += '**Description**: ' + unique(data.recommendation?.map?.((r) => r?.message))?.join(', ') + '\n';
    }
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const getRecommendation = (item) => {
    if (item.rule_name === 'certificate_expiry') {
      return (
        <>
          <li style={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>
            Date until expiry: {item.recommendation.days_until_expiry}
          </li>
          <li style={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>Expiry Date: {item.recommendation.expiry_date}</li>
        </>
      );
    }
    if (item.rule_name === 'health_check' && item.recommendation?.messages) {
      return (
        <>
          {item.recommendation.messages.map((message, index) => (
            <li key={index} style={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>
              {message}
            </li>
          ))}
        </>
      );
    }
    return <li style={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>No Data Available</li>;
  };

  const listBestPracticesRecommendations = () => {
    if (!props?.kubernetes?.id) {
      return;
    }
    setLoading(true);
    setKubernetesBestPractice([]);
    recommendationApi
      .getK8sRecommendation({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: ruleName,
        severity: severity,
        status: recommendationStatus ? [recommendationStatus] : [],
        resourceNamespace: namespace,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        setKubernetesBestPracticeCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        let k8sRecommendationData = res?.data?.recommendation.map((item) => {
          let data = [];
          let MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
              disabled: item.ticket !== undefined,
            },
          ];
          let name = RULE_LABEL_MAP[item.rule_name] || snakeToTitleCase(item.rule_name);
          let nameSpace = '-';
          let objectType = '-';
          let objectNames = '-';

          if (Array.isArray(item.recommendation)) {
            nameSpace = unique(item.recommendation?.map((r) => r?.namespace))?.join(', ') ?? '-';
            objectType = unique(item.recommendation?.map?.((r) => r?.kind))?.join(', ') ?? '-';
            objectNames = unique(item.recommendation?.map?.((r) => r?.name))?.join(', ') ?? '-';
          } else if (item.rule_name === 'health_check' && item.recommendation?.workload) {
            nameSpace = item.recommendation.workload.namespace ?? '-';
            objectType = item.recommendation.workload.kind ?? '-';
            objectNames = item.recommendation.workload.name ?? '-';
          } else if (item.recommendation) {
            nameSpace = item.recommendation?.namespace ?? '-';
            objectType = item.recommendation?.kind ?? '-';
            objectNames = item.recommendation?.name ?? '-';
          }
          data.push({
            component: ClusterNameWithRegion({
              name: name,
              hideIcon: true,
              showAutoEllipsis: true,
              maxWidth: '100%',
              region:
                item.ticket !== undefined ? (
                  <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} showAutoEllipsis={true} />
                ) : (
                  ''
                ),
            }),
          });
          data.push({ component: <SeverityIcon severityType={item.severity || '-'} />, data: item.severity });
          data.push({ component: <Text value={objectType || '-'} showAutoEllipsis /> });
          data.push({ component: <Text value={nameSpace || '-'} showAutoEllipsis /> });
          data.push({ component: <Text value={objectNames || '-'} showAutoEllipsis /> });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <ul style={{ padding: '0 0 0 16px' }}>
                {item.recommendation && item.recommendation.length > 0
                  ? [...new Map(item.recommendation.map((r) => [r?.message, r])).values()].map((r) => {
                      if (r?.container) {
                        return (
                          <li key={r?.message} style={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>
                            {r?.message} in container <b>{r?.container}</b>
                          </li>
                        );
                      }
                      return (
                        <li key={r?.message} style={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>
                          {r?.message}
                        </li>
                      );
                    })
                  : getRecommendation(item)}
              </ul>
            ),
          });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <IconButton
                    size='small'
                    data-testid={`bp-ask-nubi-${item.id}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      const prompt = buildNubiOptimizePrompt({
                        ruleName: name,
                        category: 'Configuration',
                        severity: item.severity || 'Info',
                        resourceName: objectNames || '-',
                        resourceType: objectType || '',
                        namespace: nameSpace || '',
                        brief: Array.isArray(item.recommendation)
                          ? item.recommendation
                              .map((r) => r?.message)
                              .filter(Boolean)
                              .join('; ')
                          : item.recommendation?.messages?.join('; ') || '',
                      });
                      setNubiQuery(prompt);
                      setNubiAccountId(props?.kubernetes?.id);
                      setNubiConversationId(`recom_${item.id}`);
                      setNubiSidebarVisible(true);
                    }}
                    sx={{ ...action.nubi }}
                  >
                    <SafeIcon src={getNubiIconUrl()} alt={`Ask ${assistantName}`} width={16} height={16} />
                  </IconButton>
                </CustomTooltip>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });

          return data;
        });
        setKubernetesBestPractice(k8sRecommendationData);
      })
      .catch(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    listBestPracticesRecommendations();
  }, [props?.kubernetes?.id, page, ruleName, severity, recommendationStatus, namespace, rowsPerPage]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }

    recommendationApi
      .getK8sRecommendationSummary({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        status: ['Open', 'InProgress'],
      })
      .then((res) => {
        setTotalBestPracticeCount(res?.data?.recommendation_aggregate.aggregate.count);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [props?.kubernetes?.id]);

  const handleTicketSuccess = () => {
    listBestPracticesRecommendations();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    setIsRefreshLoading(true);
    recommendationApi
      .createRecommendationJob(props?.kubernetes?.id, 'popeye_scan')
      .then(() => {
        alert('Scan Triggered Successfully, Data will be updated in Sometime');
      })
      .finally(() => {
        setIsRefreshLoading(false);
      });
  };

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Remove Unused Volume - ' + RULE_LABEL_MAP[ticketData.rule_name] || ticketData.rule_name,
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData.id,
          type: 'kubernetes',
        }}
      />
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={totalBestPracticeCount} />
      </Box>
      <BoxLayout2
        heading={props.heading === undefined ? 'Best Practices' : props.heading}
        id='best-practices'
        filterOptions={[
          {
            type: 'dropdown',
            label: 'Rule Name',
            options: Object.entries(RULE_LABEL_MAP).map(([k, v], _i) => {
              return { label: v, value: k };
            }),
            onSelect: function (e, _rule) {
              setRuleName(e?.target?.value);
              setPage(0);
            },
            value: ruleName,
          },
          {
            type: 'dropdown',
            label: 'Namespace',
            options: namespaceFilter,
            value: namespace,
            onSelect: function (e) {
              setNamespace(e?.target?.value);
              setPage(0);
            },
          },
          {
            type: 'dropdown',
            label: 'Severity',
            options: RECOMMENDATION_SERVERITY,
            onSelect: function (e, _rule) {
              setSeverity(e?.target?.value);
              setPage(0);
            },
            value: severity,
          },
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
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: kubernetesBestPracticesTable,
              };
            },
          },
          sharing: { enabled: true },
        }}
        extraOptions={[
          <CustomButton
            disabled={!hasWriteAccess(props?.kubernetes?.id)}
            showTooltip={!refreshTime}
            className='custom-button-icon'
            toolTipTitle={
              <Box>
                <Typography sx={{ color: colors.text.lastSync, fontSize: '10px', fontWeight: 400 }}>Last Sync</Typography>
                <Datetime
                  value={refreshTime?.state?.last_exec_time_sec ? new Date(refreshTime?.state?.last_exec_time_sec * 1000) : '-'}
                  sx={{ color: colors.text.white, fontWeight: 600 }}
                  sxSuffix={{ color: colors.text.white, fontWeight: 600 }}
                  showTooltip={false}
                />
              </Box>
            }
            variant='iconButton'
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
                }}
                sxSuffix={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}
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
              '& .MuiButton-startIcon': {
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              },
            }}
          />,
        ]}
      >
        <KubernetesTable2
          id={kubernetesBestPracticesTable}
          headers={BEST_PRACTICES_HEADER}
          data={kubernetesBestPractice}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesBestPracticeCount}
          onPageChange={changePage}
          sort={{
            name: 'Savings',
            order: 'desc',
          }}
          loading={loading}
          tableHeadingCenter={['Severity']}
          stickyColumnIndex='8'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
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

KubernetesBestPractices.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
};

export default KubernetesBestPractices;
