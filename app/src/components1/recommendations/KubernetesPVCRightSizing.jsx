import { Box, IconButton, Stack, TextField, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import SyncIcon from '@mui/icons-material/Sync';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import Currency from '@components1/common/format/Currency';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Memory from '@components1/common/format/Memory';
import { formatMemory } from '@lib/formatter';
import Datetime from '@components1/common/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import { action } from 'src/utils/actionStyles';
import { inputSx } from '@data/themes/inputField';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import ButtonMenu from '@components1/common/ButtonMenu';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import ResolveButton from '@components1/common/ResolveButton';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import { Text } from '@components1/common';
import { colors } from 'src/utils/colors';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import { snackbar } from '@components1/common/snackbarService';
import { Modal } from '@components1/common/modal';
import apiHome from '@api1/home';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomLink from '@components1/common/CustomLink';
import useRecommendationExport from '@hooks/useRecommendationExport';
import EmptyData from '@components1/common/EmptyData';
import Link from 'next/link';
import { DataNotAvailable } from '@assets';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';

const KubernetesPVCRightSizingPopupForm = ({
  open,
  title = 'Kindly confirm if you wish to update the volume size?',
  onClose,
  onFailure,
  onSuccess,
  data = {},
}) => {
  const [loading, setLoading] = useState(false);
  const [updatedData, setUpdatedData] = useState({
    ...data?.recommendation?.recommendation,
    recommendedSize: data?.recommendation?.recommendation?.recommend_size,
  });

  useEffect(() => {
    setUpdatedData({
      ...data?.recommendation?.recommendation,
      recommendedSize: data?.recommendation?.recommendation?.recommend_size,
    });
  }, [data]);

  const updateVolumeRequest = (e, _v) => {
    updatedData.recommendedSize = e.target.value;
    setUpdatedData({ ...updatedData });
  };

  const submitRecommendation = () => {
    try {
      let parsedData = Number(updatedData.recommendedSize);
      if (isNaN(parsedData)) {
        throw new Error('Invalid Volume Size');
      }
      if (parsedData <= 0) {
        throw new Error('Invalid Volume Size');
      }
      updatedData.recommendedSize = parsedData;
    } catch {
      snackbar.error('Invalid Volume Size');
      return;
    }
    setLoading(true);
    recommendationApi.applyRecommendation(data.account_id, data.id, updatedData).then((res) => {
      setLoading(false);
      if (res?.errors) {
        snackbar.error('Failed to update volume size');
        onFailure(false);
      } else {
        snackbar.success('Volume Size Updated Successfully');
        onSuccess(true);
      }
    });
  };

  return (
    <>
      <Modal open={open} handleClose={() => onClose(false)} title={title}>
        <Box display='flex' justifyContent='end' />
        <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left' m='0px 12px 20px 12px'>
          <TextField
            sx={inputSx}
            size='small'
            value={updatedData?.recommendedSize}
            margin='normal'
            fullWidth
            id='volume-size'
            label='New Volume Size (GB)'
            type='number'
            onChange={updateVolumeRequest}
          />
        </Box>
        <Stack spacing={1} direction='row' sx={{ float: 'right' }} mb={2} mx='20px'>
          <CustomButton size='Medium' text={'Cancel'} variant='secondary' onClick={() => onClose(false)} sx={{ minWidth: '140px' }} />
          <CustomButton
            size='Medium'
            text={'Update Resource'}
            variant='primary'
            onClick={() => submitRecommendation()}
            disabled={!updatedData.recommendedSize || loading}
            sx={{ minWidth: '140px' }}
            loading={loading}
          />
        </Stack>
      </Modal>
    </>
  );
};

KubernetesPVCRightSizingPopupForm.propTypes = {
  open: PropTypes.bool,
  title: PropTypes.string,
  onClose: PropTypes.func,
  onSuccess: PropTypes.func,
  onFailure: PropTypes.func,
  data: PropTypes.object,
};

const PV_RIGHTSIZING_HEADER = [
  'PVC Name',
  'Namespaces',
  'Current Allocation',
  'Current Usage',
  'Recommended Allocation',
  'Observation Duration',
  'Estimated Savings',
  'Updated At',
  '',
];
const KubernetesPVCRightSizing = ({ isOptimisePage = false, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const jobName = 'volume_analyzer';
  const kubernetesPvcRightSizingTable = 'kubernetesPvcRightSizingTable';
  const { selectedCluster, allCluster } = useData();
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace || '');
  const [selectedAccountId, setSelectedAccountId] = useState(props?.kubernetes?.id);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id);
  }, [props?.kubernetes?.id]);

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'pv_rightsize',
    namespace: selectedNamespace,
    status: recommendationStatus,
  });

  const [kubernetesAbandonedWorkloads, setKubernetesAbandonedWorkloads] = useState([]);
  const [kubernetesAbandonedWorkloadsCount, setKubernetesAbandonedWorkloadsCount] = useState(0);
  const [totalRecommendationsCount, setTotalRecommendationsCount] = useState(0);
  const [totalEstimatedSavings, setTotalEstimatedSavings] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [namespaces, setNamespaces] = useState([]);
  const [accounts, setAccounts] = useState([]);

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
  const [loading, setLoading] = useState(false);
  const [refreshTime, setRefreshTime] = useState({});
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);
  const [openKubernetesPVRightSizingUpdatePopupForm, setOpenKubernetesPVRightSizingUpdatePopupForm] = useState(false);
  const [kubernetesPVRightSizingUpdatePopupFormFormData, setKubernetesPVRightSizingUpdatePopupFormFormData] = useState({});

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

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Name**: ' + data?.recommendation?.spec?.claimRef?.name + '\n';
    description += '**Namespaces**: ' + data?.recommendation?.spec?.claimRef?.namespace + '\n';
    description += '**Current Capacity**: ' + formatMemory(data?.recommendation?.recommendation?.capacity) + '\n';
    description += '**Current Usage**: ' + formatMemory(data?.recommendation?.recommendation?.usage?.current) + '\n';
    description += '**Recommended Capacity**: ' + formatMemory(data?.recommendation?.recommendation?.recommend_size, 'gb') + '\n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  useEffect(() => {
    listPVCRightSizingRecommendations();
  }, [selectedAccountId, page, recommendationStatus, selectedNamespace, rowsPerPage, accounts.length]);

  const listPVCRightSizingRecommendations = () => {
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
        ruleName: 'pv_rightsize',
        resourceNamespace: selectedNamespace,
        status: recommendationStatus ? [recommendationStatus] : [],
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        setKubernetesAbandonedWorkloadsCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        let k8sRecommendationData = res?.data?.recommendation?.map((item) => {
          let data = [];
          let MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
              disabled: item.ticket !== undefined,
            },
          ];
          let name = item?.recommendation?.spec?.claimRef?.name;
          let nameSpace = item?.recommendation?.spec?.claimRef?.namespace;

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
              </>
            ),
            drilldownQuery: {
              data: item,
              pvcName: item?.recommendation?.spec?.claimRef?.name,
              namespaceName: nameSpace,
              recommendation: item,
            },
          });
          data.push({ component: <Text value={nameSpace || '-'} /> });
          data.push({
            component: <Memory value={item?.recommendation?.recommendation?.capacity || null} />,
          });
          data.push({
            component: <Memory value={item?.recommendation?.recommendation?.usage?.current || null} />,
          });
          data.push({
            component: <Memory value={item?.recommendation?.recommendation?.recommend_size || null} sourceUnit='gb' />,
          });
          data.push({
            component: <Text value={(item?.recommendation?.duration || '7') + ' D'} />,
          });
          data.push({
            component: <Currency value={item?.estimated_savings} precison={1} />,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <IconButton
                    size='small'
                    data-testid={`pvc-rs-ask-nubi-${item.id}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      const prompt = buildNubiOptimizePrompt({
                        ruleName: 'PVC Right Sizing',
                        category: 'RightSizing',
                        severity: item.severity || 'Info',
                        resourceName: name || '',
                        resourceType: 'PersistentVolumeClaim',
                        namespace: nameSpace || '',
                        accountName: isOptimisePage ? getAccountName(item.account_id) : undefined,
                        estimatedSavings: item.estimated_savings || undefined,
                        brief: `PVC ${name} current allocation: ${formatMemory(
                          item?.recommendation?.recommendation?.capacity
                        )}, usage: ${formatMemory(item?.recommendation?.recommendation?.usage?.current)}, recommended: ${formatMemory(
                          item?.recommendation?.recommendation?.recommend_size,
                          'gb'
                        )}.`,
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
                      resolvePVRightSizing(item);
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
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    recommendationApi
      .getK8sRecommendationSummary({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pv_rightsize',
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
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    recommendationApi
      .listRecommendationNamesapces({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pv_rightsize',
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaces(res);
      });
  }, [selectedAccountId, recommendationStatus]);

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

  const handleTicketSuccess = () => {
    listPVCRightSizingRecommendations();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const triggerRecommendationJob = () => {
    setIsRefreshLoading(true);
    recommendationApi
      .createRecommendationJob(selectedAccountId, 'volume_analyzer')
      .then(() => {
        alert('Scan Triggered Successfully, Data will be updated in Sometime');
      })
      .finally(() => {
        setIsRefreshLoading(false);
      });
  };

  const closePVRightSizingUpdatePopupForm = (isSuccess) => {
    setOpenKubernetesPVRightSizingUpdatePopupForm(false);
    if (isSuccess) {
      listPVCRightSizingRecommendations();
    }
  };

  const resolvePVRightSizing = (row) => {
    setKubernetesPVRightSizingUpdatePopupFormFormData(row);
    setOpenKubernetesPVRightSizingUpdatePopupForm(true);
  };

  if (!isOptimisePage && !selectedCluster?.agent?.connection_status?.prometheusConnection) {
    return (
      <BoxLayout2
        heading={props.heading === undefined ? 'PVC Right Sizing' : props.heading}
        id='pvc-right-sizing'
        sharingOptions={{ sharing: { enabled: false } }}
      >
        <EmptyData
          img={DataNotAvailable}
          heading='Agent Not Connected'
          subHeading='Prometheus is not connected for this cluster. Connect an agent to start monitoring.'
        >
          <Typography sx={{ fontSize: '13px', color: colors.text.tertiary, mt: '8px' }}>
            Check the{' '}
            <Link href={`/agentHealth?accountId=${selectedAccountId}#agent`} style={{ color: colors.text.primary }}>
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
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Update PVC Size - ' + ticketData.recommendation?.spec?.claimRef?.name,
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
              recommendationLabel='Some of PVC rightsizing recommendations'
            />
          }
        />
      </Box>
      <BoxLayout2
        heading={props.heading === undefined ? 'PVC Right Sizing' : props.heading}
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
            onSelect: function (e) {
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
                tableId: kubernetesPvcRightSizingTable,
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
          isOptimisePage && (
            <CustomButton
              disabled={!hasWriteAccess(selectedAccountId)}
              showTooltip={!refreshTime}
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
                      backgroundColor: 'red',
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
                  marginLeft: '4px',
                },
              }}
            />
          ),
        ]}
      >
        <KubernetesTable2
          id={kubernetesPvcRightSizingTable}
          headers={PV_RIGHTSIZING_HEADER}
          data={kubernetesAbandonedWorkloads}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesAbandonedWorkloadsCount}
          onPageChange={changePage}
          stickyColumnIndex='9'
          sort={{
            name: 'Savings',
            order: 'desc',
          }}
          loading={loading}
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          showExpandable={true}
          expandable={{
            tabs: [
              { text: 'Utilization Trends', value: 0, key: 'pvc_utilization' },
              {
                componentFn: RecommendationResolutionFn,
                text: 'Resolutions',
              },
            ],
          }}
          pageNumber={page + 1}
        />
        <KubernetesPVCRightSizingPopupForm
          open={openKubernetesPVRightSizingUpdatePopupForm}
          onClose={(isSuccess) => closePVRightSizingUpdatePopupForm(isSuccess)}
          onSuccess={(isSuccess) => closePVRightSizingUpdatePopupForm(isSuccess)}
          onFailure={(isSuccess) => closePVRightSizingUpdatePopupForm(isSuccess)}
          data={kubernetesPVRightSizingUpdatePopupFormFormData}
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

KubernetesPVCRightSizing.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
};

const RecommendationResolutionFn = (accountId, drilldownQuery) => {
  return <RecommendationResolution recommendation={drilldownQuery.recommendation} />;
};

export default KubernetesPVCRightSizing;
