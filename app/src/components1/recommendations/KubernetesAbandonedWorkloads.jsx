import { Box, IconButton, TextField, Typography } from '@mui/material';
import SyncIcon from '@mui/icons-material/Sync';

import { action } from 'src/utils/actionStyles';
import { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2, { KubernetesNetwork } from '@components1/k8s/common/KubernetesTable2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import NumberComponent from '@components1/common/format/Number';
import Currency from '@components1/common/format/Currency';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Datetime from '@components1/common/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import KubernetesTracesListing from '@components1/k8s/details/KubernetesTracesListing';
import { formatDateForPlusMinusDuration } from 'src/utils/common';
import { useData } from '@context/DataContext';
import ResolveButton from '@components1/common/ResolveButton';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomButton from '@components1/common/NewCustomButton';
import ButtonMenu from '@components1/common/ButtonMenu';
import NDialog from '@components1/common/modal/NDialog';
import { colors } from 'src/utils/colors';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import { snackbar } from '@components1/common/snackbarService';
import apiHome from '@api1/home';
import CustomLink from '@components1/common/CustomLink';
import useRecommendationExport from '@hooks/useRecommendationExport';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';

const TracesTabComponent = ({ drilldownQuery, accountId }) => {
  const plusMinusDuration = formatDateForPlusMinusDuration(drilldownQuery.updatedAt, 5040);

  return (
    <KubernetesTracesListing
      showNamespaceFilter={false}
      showWorkloadFilter={false}
      destinationNamespace={''}
      destinationWorkload={''}
      namespace={drilldownQuery.namespace}
      workloadName={drilldownQuery.workloadName}
      accountId={accountId}
      passedSelectedTimestamp={{
        startTimestamp: plusMinusDuration.dateMinusMinutes,
        endTimestamp: plusMinusDuration.datePlusMinutes,
      }}
    />
  );
};

TracesTabComponent.propTypes = {
  drilldownQuery: PropTypes.shape({
    updatedAt: PropTypes.string,
    namespace: PropTypes.string,
    workloadName: PropTypes.string,
  }),
  accountId: PropTypes.string,
};

const NetworkTrafficTabComponent = ({ drilldownQuery, accountId }) => {
  return (
    <KubernetesNetwork
      accountId={accountId}
      query={{
        namespaceName: drilldownQuery.namespace,
        workloadName: drilldownQuery.workloadName,
        type: 'workload',
      }}
    />
  );
};

NetworkTrafficTabComponent.propTypes = {
  drilldownQuery: PropTypes.shape({
    namespace: PropTypes.string,
    workloadName: PropTypes.string,
  }),
  accountId: PropTypes.string,
};

export const KubernetesUnusedWorkloadUpdatePopupForm = ({ open, onClose, onSuccess, onFailure, data = {} }) => {
  const [confirmationText, setConfirmationText] = useState('');
  const [errorText, setErrorText] = useState('');

  const submitRecommendation = () => {
    if (confirmationText === (data?.cloud_resourse?.meta?.controller || data?.cloud_resourse?.name)) {
      recommendationApi.applyRecommendation(data.accountId, data.id, data).then((res) => {
        if (res?.errors) {
          onFailure(res?.errors);
        } else {
          onSuccess(res?.data);
        }
      });
    } else {
      setErrorText('Please enter the correct Workload name to confirm scale-down');
    }
  };

  const handleClose = () => {
    setConfirmationText('');
    setErrorText('');
    onClose();
  };

  return (
    <NDialog
      buttonText='Scale Down'
      handleClose={handleClose}
      dialogTitle={''}
      handleSubmit={() => submitRecommendation()}
      open={open}
      dialogContent={
        <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left'>
          <Typography component='h2' variant='h5' fontWeight={600} color={colors.text.signinDark} mb='15px'>
            {`Are you sure you want to scale down ${data?.cloud_resourse?.name} ?`}
          </Typography>

          <TextField
            label='Enter workload Name'
            value={confirmationText}
            onChange={(e) => {
              setConfirmationText(e.target.value);
              setErrorText('');
            }}
            variant='outlined'
            margin='normal'
            error={!!errorText}
            helperText={errorText}
            size='small'
          />
        </Box>
      }
      additionalComponent={undefined}
    />
  );
};

KubernetesUnusedWorkloadUpdatePopupForm.propTypes = {
  open: PropTypes.bool,
  onClose: PropTypes.func,
  onSuccess: PropTypes.func,
  onFailure: PropTypes.func,
  data: PropTypes.object,
};

const ABANDONED_WORKLOADS_HEADER = [
  'Resource Name',
  'Object Type',
  'Namespaces',
  'Network Traffic (bytes)',
  'Observation Duration',
  'Estimated Savings',
  'Updated At',
  '',
];

const KubernetesAbandonedWorkloads = ({ isOptimisePage = false, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();

  const [kubernetesAbandonedWorkloads, setKubernetesAbandonedWorkloads] = useState([]);
  const [kubernetesAbandonedWorkloadsCount, setKubernetesAbandonedWorkloadsCount] = useState(0);
  const [estimatedSavings, setEstimatedSavings] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [namespaces, setNamespaces] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace || '');
  const [page, setPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [loading, setLoading] = useState(false);
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(props?.kubernetes?.id);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id);
  }, [props?.kubernetes?.id]);

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

  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const { selectedCluster, allCluster } = useData();
  const [refreshTime, setRefreshTime] = useState({});

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'abandoned_resource',
    namespace: selectedNamespace,
    status: recommendationStatus,
  });

  let jobName = 'abandoned_workload_scan';

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
  const kubernetesAbandonedWorkloadsTable = 'kubernetesAbandonedWorkloadsTable';

  const [openKubernetesAbandonedWorkloadUpdatePopupForm, setOpenKubernetesAbandonedWorkloadUpdatePopupForm] = useState(false);
  const [kubernetesAbandonedWorkloadUpdatePopupFormData, setKubernetesAbandonedWorkloadUpdatePopupFormData] = useState({});

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRecordsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    //generate ticket description
    let description = '';
    description += '**Name**: ' + data?.cloud_resourse?.name + '\n';
    description += '**Object Type**: ' + data?.cloud_resourse?.type + '\n';
    description += '**Namespaces**: ' + data?.cloud_resourse?.meta?.namespace + '\n';
    description += '**Description**: ' + data?.recommendation?.message + '\n';
    return description;
  };
  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const resolveAbandonedWorkloads = (row) => {
    setKubernetesAbandonedWorkloadUpdatePopupFormData(row);
    setOpenKubernetesAbandonedWorkloadUpdatePopupForm(true);
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

  const listAbandonedWorkloads = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (isOptimisePage && accounts.length === 0) {
      return;
    }
    setLoading(true);
    setKubernetesAbandonedWorkloads([]);

    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'abandoned_resource',
        status: recommendationStatus ? [recommendationStatus] : [],
        resourceNamespace: selectedNamespace,
        limit: recordsPerPage,
        offset: page * recordsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        let k8sRecommendationData = res?.data?.recommendation?.map((item) => {
          let data = [];
          let MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
            },
          ];

          const name = item?.cloud_resourse?.name;
          const nameSpace = item?.cloud_resourse?.meta?.namespace;
          const objectType = item?.cloud_resourse?.meta?.controllerKind;
          const workloadName =
            item?.resource_name || item.cloud_resourse.meta?.controller || item.cloud_resourse.meta?.config?.labels?.['app.kubernetes.io/name'];
          item.accountId = item.account_id || selectedAccountId;
          data.push({
            component: (
              <>
                <Text value={workloadName} showAutoEllipsis />
                <Text value={`Pod - ${name}`} secondaryText showAutoEllipsis />
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
              </>
            ),
            drilldownQuery: {
              namespace: nameSpace,
              workloadName: workloadName,
              updatedAt: item.updated_at,
              recommendation: item,
            },
          });
          data.push({ component: <Text value={objectType || '-'} showAutoEllipsis /> });
          data.push({ component: <Text value={nameSpace || '-'} showAutoEllipsis /> });
          data.push({
            component: (
              <Box>
                <Text value={'Current: '} display={'inline'} />
                <NumberComponent value={item?.recommendation?.traffic} sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }} />
                <br />
                <Text value={'Threshold: '} display={'inline'} />
                <NumberComponent value={item?.recommendation?.threshold} sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }} />
              </Box>
            ),
            data: item?.recommendation?.traffic,
          });
          data.push({
            component: <Text value={(item?.recommendation?.duration || '7') + ' D'} />,
          });
          data.push({
            component: <Currency value={item?.estimated_savings} precison={1} />,
            data: item?.estimated_savings,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <IconButton
                    size='small'
                    data-testid={`abandoned-ask-nubi-${item.id}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      const prompt = buildNubiOptimizePrompt({
                        ruleName: 'Abandoned Resource',
                        category: 'RightSizing',
                        severity: item.severity || 'Info',
                        resourceName: workloadName || name || '',
                        resourceType: objectType || '',
                        namespace: nameSpace || '',
                        accountName: isOptimisePage ? getAccountName(item.account_id) : undefined,
                        estimatedSavings: item.estimated_savings || undefined,
                        brief: `Workload ${workloadName} has low network traffic (current: ${item?.recommendation?.traffic}, threshold: ${
                          item?.recommendation?.threshold
                        }). Observation duration: ${item?.recommendation?.duration || '7'} days.`,
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
                    sx={{ ...action.primary }}
                    onClick={(e) => {
                      e.stopPropagation();
                      resolveAbandonedWorkloads(item);
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
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listAbandonedWorkloads();
  }, [selectedAccountId, page, recommendationStatus, selectedNamespace, setRecordsPerPage, accounts.length]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }

    recommendationApi
      .getK8sRecommendationSummary({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'abandoned_resource',
        status: ['Open', 'InProgress'],
      })
      .then((res) => {
        setKubernetesAbandonedWorkloadsCount(res?.data?.recommendation_aggregate.aggregate.count);
        setEstimatedSavings(res?.data?.recommendation_aggregate.aggregate.sum?.estimated_savings);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [selectedAccountId]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }

    recommendationApi
      .listRecommendationNamesapces({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'abandoned_resource',
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaces(res);
      });
  }, [selectedAccountId, recommendationStatus]);

  const handleTicketSuccess = () => {
    listAbandonedWorkloads();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    setIsRefreshLoading(true);
    recommendationApi
      .createRecommendationJob(selectedAccountId, 'abandoned_workload_scan')
      .then(() => {
        alert('Scan Triggered Successfully, Data will be updated in Sometime');
      })
      .finally(() => {
        setIsRefreshLoading(false);
      });
  };

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
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
          subject: 'Remove Unused Resource - ' + ticketData?.cloud_resourse?.name,
          description: getTicketDescription(ticketData),
          accountId: ticketData?.account_id || selectedAccountId,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.cloud_resourse?.id,
          type: 'kubernetes',
        }}
      />
      <KubernetesUnusedWorkloadUpdatePopupForm
        open={openKubernetesAbandonedWorkloadUpdatePopupForm}
        onClose={() => {
          setOpenKubernetesAbandonedWorkloadUpdatePopupForm(false);
        }}
        onSuccess={() => {
          setOpenKubernetesAbandonedWorkloadUpdatePopupForm(false);
          snackbar.success('Requested Submitted to scale down workload');
        }}
        onFailure={() => {
          setOpenKubernetesAbandonedWorkloadUpdatePopupForm(false);
          snackbar.error('Failed to request scale down workload');
        }}
        data={kubernetesAbandonedWorkloadUpdatePopupFormData}
      />

      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={kubernetesAbandonedWorkloadsCount} />
        <SummaryWidget
          title='Savings Potential'
          variant='savings'
          value={
            <Currency
              value={estimatedSavings}
              precison={1}
              suffix='/mo'
              sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
              withTooltip={false}
              isSavingPotential={true}
              recommendationLabel='Some of abandoned workload recommendations'
            />
          }
        />
      </Box>
      <BoxLayout2
        heading={props.heading === undefined ? 'Abandoned Workloads' : props.heading}
        id='abandoned-workloads'
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
            },
          },
        ]}
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: kubernetesAbandonedWorkloadsTable,
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
          ...(hasWriteAccess(selectedAccountId) && !isOptimisePage
            ? [
                <CustomButton
                  showTooltip={!refreshTime}
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
                  variant='secondary'
                  key='triggerRecommendation'
                  id='triggerRecommendation'
                  onClick={triggerRecommendationJob}
                  text='Refresh'
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
                    },
                  }}
                />,
              ]
            : []),
        ]}
      >
        <KubernetesTable2
          headers={ABANDONED_WORKLOADS_HEADER}
          data={kubernetesAbandonedWorkloads}
          rowsPerPage={recordsPerPage}
          totalRows={kubernetesAbandonedWorkloadsCount}
          onPageChange={changePage}
          sort={{
            name: 'Savings',
            order: 'desc',
          }}
          id={kubernetesAbandonedWorkloadsTable}
          loading={loading}
          showExpandable={true}
          stickyColumnIndex='8'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          expandable={{
            tabs: [
              {
                componentFn: (opt, drilldownQuery) => <TracesTabComponent drilldownQuery={drilldownQuery} accountId={selectedAccountId} />,
                text: 'Traces',
                value: 0,
                key: 'traces',
              },
              {
                componentFn: (opt, drilldownQuery) => <NetworkTrafficTabComponent drilldownQuery={drilldownQuery} accountId={selectedAccountId} />,
                text: 'Network Traffic',
                value: 1,
                key: 'networkTraffic',
              },
              {
                componentFn: RecommendationResolutionFn,
                text: 'Resolutions',
              },
            ],
          }}
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

const RecommendationResolutionFn = (accountId, drilldownQuery) => {
  return <RecommendationResolution recommendation={drilldownQuery.recommendation} />;
};

KubernetesAbandonedWorkloads.propTypes = {
  kubernetes: PropTypes.object,
  heading: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesAbandonedWorkloads;
