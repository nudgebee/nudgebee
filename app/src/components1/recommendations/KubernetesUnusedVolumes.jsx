import { Box } from '@mui/material';
import React, { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import Currency from '@common-new/format/Currency';
import Datetime from '@common-new/format/Datetime';
import KubernetesUnusedVolumeUpdatePopupForm from './KubernetesUnusedVolumeUpdateForm';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import { useData } from '@context/DataContext';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import apiUser from '@api1/user';
import Text from '@common-new/format/Text';
import { colors, ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import apiHome from '@api1/home';
import { applyFiltersOnRouter } from '@lib/router';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { Link as CustomLink } from '@components1/ds/Link';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/ds/Tooltip';
import SafeIcon from '@components1/common/SafeIcon';
import useRecommendationExport from '@hooks/useRecommendationExport';

// DS V2 primitives — phased in alongside the legacy components. Visual swap only;
// API calls, handlers, and modal forms are untouched.
import WidgetCard from '@components1/ds/WidgetCard';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Stat } from '@components1/ds/Stat';
import { CostCallout } from '@components1/ds/CostCallout';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { ScanRefreshButton } from './ScanRefreshButton';
import FilterDropdown from '@components1/ds/FilterDropdown';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';

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
  const { allCluster } = useData();
  const accountsRef = useRef(accounts);

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'unused_pvc',
    status: recommendationStatus,
  });

  useEffect(() => {
    accountsRef.current = accounts;
  }, [accounts]);

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

  const openTicketForData = (data) => {
    setTicketData(data);
    setIsTicketCreateFormOpen(true);
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
        const rawItems = res?.data?.recommendation || [];
        let k8sRecommendationData = rawItems.map((item) => {
          let data = [];

          data.push({
            component: (
              <>
                <Text value={item.recommendation?.metadata?.name} sx={{ color: colors.text.primary }} />
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
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}
              >
                {hasWriteAccess(item.account_id || selectedAccountId) && recommendationStatus !== 'InProgress' && (
                  <DsButton
                    tone='secondary'
                    size='xs'
                    id={`uv-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={() => resolveUnusedVolume(item)}
                  >
                    Optimize
                  </DsButton>
                )}
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <span>
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      aria-label={`Ask ${assistantName}`}
                      id={`unused-vol-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
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
                    />
                  </span>
                </CustomTooltip>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `uv-action-ticket-${item.id}`,
                      label: item.ticket?.ticket_id ? `Ticket: ${item.ticket.ticket_id}` : 'Create ticket',
                      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
                      disabled: !!item.ticket?.ticket_id,
                      onSelect: () => openTicketForData(item),
                    },
                  ]}
                  trigger={
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<MoreVertIcon />}
                      aria-label='More actions'
                      id={`uv-action-menu-${item.id}`}
                    />
                  }
                />
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

  const heading = props.heading === undefined ? 'Unused Volumes' : props.heading;

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
            info={{ tooltip: 'Active unused-volume recommendations across the selected scope' }}
            value={Number.isFinite(totalRecommendationsCount) ? totalRecommendationsCount.toLocaleString() : totalRecommendationsCount ?? '—'}
          />
        </WidgetCard>
        <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
          <Stat
            size='md'
            label='Total Cost'
            info={{ tooltip: 'Current monthly spend across the unused volumes listed below' }}
            value={
              kubernetesUnusedVolumeEstimatedSaving == null ? (
                '—'
              ) : (
                <CostCallout size='lg' tone='neutral' value={Number(kubernetesUnusedVolumeEstimatedSaving) || 0} period='/ mo' />
              )
            }
          />
        </WidgetCard>
        <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
          <Stat
            size='md'
            label='Savings Potential'
            info={{ tooltip: 'Estimated yearly savings if every unused volume is removed' }}
            value={<CostCallout size='lg' tone='high-savings' value={(Number(kubernetesUnusedVolumeEstimatedSaving) || 0) * 12} period='/ yr' />}
          />
        </WidgetCard>
      </Box>

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

      <ListingLayout id='unused-volume'>
        <ListingLayout.Toolbar
          title={heading || undefined}
          data-testid='uv-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={selectedAccountId} jobName='unused_pv' idPrefix='uv' />}
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
                    id='uv-download'
                  />
                }
              />
            </>
          }
        >
          {isOptimisePage && (
            <FilterDropdown
              id='uv-filter-account'
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
            id='uv-filter-status'
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
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={kubernetesUnusedVolumeTable}
            headers={tableHeaders}
            tableData={kubernetesUnusedVolume}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesUnusedVolumeCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            stickyColumnIndex='8'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'Resolutions',
                  componentFn: (_option, drilldownQuery) => <RecommendationResolution recommendation={drilldownQuery.recommendation} />,
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

KubernetesUnusedVolumes.propTypes = {
  kubernetes: PropTypes.object,
  heading: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
  resourceIds: PropTypes.arrayOf(PropTypes.string),
  groupName: PropTypes.string,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesUnusedVolumes;
