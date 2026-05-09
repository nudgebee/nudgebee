import SummaryWidget from '@components1/optimise/SummaryWidget';
import { Box, IconButton, Typography } from '@mui/material';
import React, { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import SyncIcon from '@mui/icons-material/Sync';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Currency from '@components1/common/format/Currency';
import Datetime from '@components1/common/format/Datetime';
import KubernetesUnusedVolumeUpdatePopupForm from './KubernetesUnusedVolumeUpdateForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import ResolveButton from '@components1/common/ResolveButton';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import { action } from 'src/utils/actionStyles';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import ButtonMenu from '@components1/common/ButtonMenu';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import apiHome from '@api1/home';
import { applyFiltersOnRouter } from '@lib/router';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomLink from '@components1/common/CustomLink';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import useRecommendationExport from '@hooks/useRecommendationExport';

const UNUSED_VOLUME_HEADER = [
  { name: 'Name', width: '30%' },
  { name: 'Last Namespace', width: '20%' },
  { name: 'Last Claim', width: '15%' },
  { name: 'Size', width: '5%' },
  { name: 'Savings/mo', width: '5%' },
  { name: 'Created At', width: '5%' },
  { name: 'Updated At', width: '5%' },
  { name: '', width: '5%' },
];

const KubernetesUnusedVolumes = ({ isOptimisePage = false, resourceIds, groupName, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const [kubernetesUnusedVolume, setKubernetesUnusedVolume] = useState([]);
  const [kubernetesUnusedVolumeCount, setKubernetesUnusedVolumeCount] = useState(0);
  const [totalRecommendationsCount, setTotalRecommendationsCount] = useState(0);
  const [kubernetesUnusedVolumeEstimatedSaving, setKubernetesUnusedVolumeEstimatedSaving] = useState(0);
  const [openKubernetesUnusedVolumeUpdatePopupForm, setOpenKubernetesUnusedVolumeUpdatePopupForm] = useState(false);
  const [kubernetesUnusedVolumeUpdatePopupFormData, setKubernetesUnusedVolumeUpdatePopupFormData] = useState({});
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [refreshTime, setRefreshTime] = useState({});
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

  const kubernetesUnusedVolumeTable = 'kubernetesUnusedVolumeTable';
  const { selectedCluster, allCluster } = useData();
  const accountsRef = useRef(accounts);

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'unused_pvc',
    status: recommendationStatus,
  });

  let jobName = 'unused_pv';

  useEffect(() => {
    accountsRef.current = accounts;
  }, [accounts]);

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

  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const resolveUnusedVolume = (row) => {
    setKubernetesUnusedVolumeUpdatePopupFormData({
      id: row.id,
      name: row.recommendation?.metadata?.name,
      accountId: props.kubernetes?.id || row?.account_id,
    });
    setOpenKubernetesUnusedVolumeUpdatePopupForm(true);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Name**: ' + data?.recommendation?.metadata?.name + '\n';
    description += '**Size**: ' + data?.recommendation?.spec?.capacity?.storage + '\n';
    description += '**Last Namespace**: ' + data?.recommendation?.spec?.claimRef?.namespace + '\n';
    description += '**Last Claim**: ' + data?.recommendation?.spec?.claimRef?.name + '\n';
    description += '**Age**: ' + data?.recommendation?.metadata?.creationTimestamp + '\n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
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

  useEffect(() => {
    listUnusedVolumes();
  }, [selectedAccountId, page, recommendationStatus, rowsPerPage, accounts.length, resourceIds]);

  const tableHeaders = React.useMemo(() => {
    if (!groupName) {
      return UNUSED_VOLUME_HEADER;
    }
    const newHeaders = [...UNUSED_VOLUME_HEADER];
    newHeaders[0] = `Name (${groupName} Group)`;
    return newHeaders;
  }, [groupName]);

  const getAccountName = (id) => {
    const filteredAcc = accountsRef.current?.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const listUnusedVolumes = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (isOptimisePage && accounts.length === 0) {
      return;
    }
    setLoading(true);
    setKubernetesUnusedVolume([]);
    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'unused_pvc',
        status: recommendationStatus ? [recommendationStatus] : [],
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
        resource_ids: resourceIds,
      })
      .then((res) => {
        setLoading(false);
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

          data.push({
            component: (
              <>
                <Text value={item.recommendation?.metadata?.name} sx={{ color: colors.text.primary }} />
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
              </>
            ),
            drilldownQuery: {
              recommendation: item,
            },
          });
          data.push({
            component: <Text value={item.recommendation?.spec?.claimRef?.namespace} showAutoEllipsis />,
          });
          data.push({
            component: <Text value={item.recommendation?.spec?.claimRef?.name} showAutoEllipsis />,
          });
          data.push({
            component: <Text value={item.recommendation?.spec?.capacity?.storage} />,
          });
          data.push({ component: <Currency value={item.estimated_savings} precison={1} /> });
          data.push({
            component: <Datetime value={item.recommendation?.metadata?.creationTimestamp} />,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <IconButton
                    size='small'
                    data-testid={`unused-vol-ask-nubi-${item.id}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      const prompt = buildNubiOptimizePrompt({
                        ruleName: 'Unused PVC',
                        category: 'RightSizing',
                        severity: item.severity || 'Info',
                        resourceName: item.recommendation?.metadata?.name || '',
                        resourceType: 'PersistentVolume',
                        namespace: item.recommendation?.spec?.claimRef?.namespace || '',
                        accountName: isOptimisePage ? getAccountName(item.account_id) : undefined,
                        estimatedSavings: item.estimated_savings || undefined,
                        brief: `Volume ${item.recommendation?.metadata?.name} appears unused. Last claim: ${
                          item.recommendation?.spec?.claimRef?.name || 'N/A'
                        }, Size: ${item.recommendation?.spec?.capacity?.storage || 'N/A'}.`,
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
                {hasWriteAccess(item.account_id || selectedAccountId) && recommendationStatus !== 'InProgress' && (
                  <ResolveButton
                    displayText
                    sx={{ ...action.primary }}
                    onClick={(e) => {
                      e.stopPropagation();
                      resolveUnusedVolume(item);
                    }}
                  />
                )}
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });
          return data;
        });
        setKubernetesUnusedVolume(k8sRecommendationData);
        setKubernetesUnusedVolumeCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
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
        ruleName: 'unused_pvc',
        status: ['Open', 'InProgress'],
        resource_ids: resourceIds,
      })
      .then((res) => {
        setTotalRecommendationsCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        setKubernetesUnusedVolumeEstimatedSaving(res?.data?.recommendation_aggregate.aggregate.sum.estimated_savings);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [selectedAccountId, resourceIds]);

  const handleTicketSuccess = () => {
    listUnusedVolumes();
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
      .createRecommendationJob(selectedAccountId, 'unused_pv')
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
          subject: 'Remove Unused Volume - ' + ticketData?.recommendation?.metadata?.name,
          description: getTicketDescription(ticketData),
          accountId: ticketData?.account_id || selectedAccountId,
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
          title='Total Cost'
          value={
            <Currency
              value={kubernetesUnusedVolumeEstimatedSaving}
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
              value={kubernetesUnusedVolumeEstimatedSaving * 12}
              sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
              withTooltip={false}
              suffix='/yr'
              isSavingPotential={true}
              recommendationLabel='Some of unused volume recommendations'
            />
          }
        />
      </Box>
      <BoxLayout2
        heading={props.heading === undefined ? 'Unused Volumes' : props.heading}
        id='unused-volume'
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
        ]}
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: kubernetesUnusedVolumeTable,
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
              variant='iconButton'
              key='triggerRecommendation'
              id='triggerRecommendation'
              onClick={triggerRecommendationJob}
              text={
                <Datetime
                  value={new Date(refreshTime?.state?.last_exec_time_sec * 1000)}
                  sx={{
                    color: colors.text.secondaryDark,
                    fontSize: '14px',
                    fontWeight: 400,
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
            />
          ),
        ]}
      >
        <KubernetesUnusedVolumeUpdatePopupForm
          open={openKubernetesUnusedVolumeUpdatePopupForm}
          onClose={() => setOpenKubernetesUnusedVolumeUpdatePopupForm(false)}
          onSuccess={() => {
            setOpenKubernetesUnusedVolumeUpdatePopupForm(false);
            snackbar.success('Volume Deleted Successfully ');
            listUnusedVolumes();
          }}
          onFailure={() => {
            setOpenKubernetesUnusedVolumeUpdatePopupForm(false);
            snackbar.error(`Failed to delete Volume ${kubernetesUnusedVolumeUpdatePopupFormData.name}`);
          }}
          data={kubernetesUnusedVolumeUpdatePopupFormData}
        />
        <KubernetesTable2
          id={kubernetesUnusedVolumeTable}
          headers={tableHeaders}
          data={kubernetesUnusedVolume}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesUnusedVolumeCount}
          onPageChange={changePage}
          loading={loading}
          pageNumber={page + 1}
          stickyColumnIndex='8'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          showExpandable={true}
          expandable={{
            tabs: [
              {
                componentFn: RecommendationResolutionFn,
                text: 'Resolutions',
              },
            ],
          }}
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

const RecommendationResolutionFn = (accountId, drilldownQuery, _row) => {
  return <RecommendationResolution recommendation={drilldownQuery.recommendation} />;
};

KubernetesUnusedVolumes.propTypes = {
  kubernetes: PropTypes.object,
  heading: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
  resourceIds: PropTypes.arrayOf(PropTypes.string),
  groupName: PropTypes.string,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesUnusedVolumes;
