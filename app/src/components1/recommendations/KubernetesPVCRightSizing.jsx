import React, { useEffect, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Input } from '@components1/ds/Input';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import Currency from '@common-new/format/Currency';
import Memory from '@common-new/format/Memory';
import { formatMemory } from '@lib/formatter';
import Datetime from '@common-new/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import { KubernetesPVCUtilization } from '@components1/k8s/common/KubernetesTable2';
import Text from '@common-new/format/Text';
import { colors, ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import { Modal } from '@components1/ds/Modal';
import apiHome from '@api1/home';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { Link as CustomLink } from '@components1/ds/Link';
import EmptyData from '@components1/common/EmptyData';
import Link from 'next/link';
import { DataNotAvailable } from '@assets';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/ds/Tooltip';
import SafeIcon from '@components1/common/SafeIcon';

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

  const updateVolumeRequest = (value) => {
    updatedData.recommendedSize = value;
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
          <Box sx={{ mt: 2 }}>
            <Input
              size='sm'
              value={String(updatedData?.recommendedSize ?? '')}
              id='volume-size'
              label='New Volume Size (GB)'
              type='number'
              onChange={updateVolumeRequest}
            />
          </Box>
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

const KubernetesPVCRightSizing = ({ enabledSummary = true, enabledFilters = true, isOptimisePage = false, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
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

  const handleExportDownload = async (format) => {
    try {
      const exportFormat = format === 'xlsx' ? 'xlsx' : 'csv';
      const response = await recommendationApi.exportRecommendations({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pv_rightsize',
        namespace: selectedNamespace || undefined,
        status: recommendationStatus ? [recommendationStatus] : undefined,
        format: exportFormat,
      });

      if (response?.data?.data?.recommendation_export) {
        const { file_data, filename, content_type } = response.data.data.recommendation_export;
        const byteCharacters = atob(file_data);
        const byteNumbers = new Array(byteCharacters.length);
        for (let i = 0; i < byteCharacters.length; i++) {
          byteNumbers[i] = byteCharacters.charCodeAt(i);
        }
        const byteArray = new Uint8Array(byteNumbers);
        const blob = new Blob([byteArray], { type: content_type });
        const url = window.URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = filename;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
        window.URL.revokeObjectURL(url);
        snackbar.success('Export downloaded successfully');
      } else {
        snackbar.error('Export failed: No data received');
      }
    } catch (error) {
      console.error('Export error:', error);
      snackbar.error(`Export failed: ${error.message}`);
    }
  };

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
  const [openKubernetesPVRightSizingUpdatePopupForm, setOpenKubernetesPVRightSizingUpdatePopupForm] = useState(false);
  const [kubernetesPVRightSizingUpdatePopupFormFormData, setKubernetesPVRightSizingUpdatePopupFormFormData] = useState({});

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
        const rawItems = res?.data?.recommendation || [];
        setKubernetesAbandonedWorkloadsCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        let k8sRecommendationData = rawItems.map((item) => {
          let data = [];
          let name = item?.recommendation?.spec?.claimRef?.name;
          let nameSpace = item?.recommendation?.spec?.claimRef?.namespace;

          data.push({
            component: (
              <>
                <Text value={name} showAutoEllipsis />
                {nameSpace && <Text value={nameSpace} secondaryText />}
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
              </>
            ),
            drilldownQuery: {
              data: item,
              pvcName: item?.recommendation?.spec?.claimRef?.name,
              namespaceName: nameSpace,
              recommendation: item,
            },
          });
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
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}
              >
                {hasWriteAccess(item.account_id || selectedAccountId) && (
                  <DsButton
                    tone='secondary'
                    size='xs'
                    id={`pvc-rs-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={(e) => {
                      e.stopPropagation();
                      resolvePVRightSizing(item);
                    }}
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
                      id={`pvc-rs-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
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
                    />
                  </span>
                </CustomTooltip>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `pvc-action-ticket-${item.id}`,
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
                      id={`pvc-action-menu-${item.id}`}
                    />
                  }
                />
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

  const tableHeaders = [
    'PVC Name',
    'Current Allocation',
    'Current Usage',
    'Recommended Allocation',
    'Observation Duration',
    'Savings/mo',
    'Updated At',
    '',
  ];

  if (!isOptimisePage && !selectedCluster?.agent?.connection_status?.prometheusConnection) {
    return (
      <WidgetCard id='pvc-right-sizing' sx={{ mt: 0, mb: 0 }}>
        <EmptyData
          img={DataNotAvailable}
          heading='Agent Not Connected'
          subHeading='Prometheus is not connected for this cluster. Connect an agent to start monitoring.'
        >
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.tertiary, mt: 'var(--ds-space-2)' }}>
            Check the{' '}
            <Link href={`/agentHealth?accountId=${selectedAccountId}#agent`} style={{ color: colors.text.primary }}>
              Agent Health
            </Link>{' '}
            page for connection details.
          </Typography>
        </EmptyData>
      </WidgetCard>
    );
  }

  const heading = props.heading === undefined ? 'PVC Right Sizing' : props.heading;

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
              info={{ tooltip: 'Active PVC right-sizing recommendations across all namespaces' }}
              value={Number.isFinite(totalRecommendationsCount) ? totalRecommendationsCount.toLocaleString() : totalRecommendationsCount ?? '—'}
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Savings Potential'
              info={{ tooltip: 'Estimated monthly savings if every PVC recommendation is applied' }}
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

      <ListingLayout id='pvc-rightsizing'>
        <ListingLayout.Toolbar
          title={heading || undefined}
          data-testid='pvc-rs-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={selectedAccountId} jobName='volume_analyzer' idPrefix='pvc' />}
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
                    id='pvc-download'
                  />
                }
              />
            </>
          }
        >
          {enabledFilters && (
            <>
              {isOptimisePage && (
                <FilterDropdown
                  id='pvc-filter-account'
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
                id='pvc-filter-status'
                label='Status'
                options={RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }))}
                value={
                  recommendationStatus
                    ? { label: recommendationStatus === 'InProgress' ? 'In Progress' : recommendationStatus, value: recommendationStatus }
                    : null
                }
                onSelect={(_e, item) => {
                  setRecommendationStatus(item?.value || '');
                  setPage(0);
                }}
              />
              <FilterDropdown
                id='pvc-filter-namespace'
                label='Namespace'
                options={(namespaces || []).map((n) => ({ label: n, value: n }))}
                value={selectedNamespace ? { label: selectedNamespace, value: selectedNamespace } : null}
                onSelect={(_e, item) => {
                  const value = item?.value || '';
                  setSelectedNamespace(value);
                  applyFiltersOnRouter(router, { namespace: value });
                  setPage(0);
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={kubernetesPvcRightSizingTable}
            headers={tableHeaders}
            tableData={kubernetesAbandonedWorkloads}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesAbandonedWorkloadsCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'Utilization Trends',
                  componentFn: (_option, drilldownQuery) => (
                    <KubernetesPVCUtilization accountId={drilldownQuery?.recommendation?.account_id} query={drilldownQuery} />
                  ),
                },
                {
                  text: 'Resolutions',
                  componentFn: (_option, drilldownQuery) => <RecommendationResolution recommendation={drilldownQuery?.recommendation} />,
                },
              ],
            }}
          />
        </ListingLayout.Body>

        <KubernetesPVCRightSizingPopupForm
          open={openKubernetesPVRightSizingUpdatePopupForm}
          onClose={(isSuccess) => closePVRightSizingUpdatePopupForm(isSuccess)}
          onSuccess={(isSuccess) => closePVRightSizingUpdatePopupForm(isSuccess)}
          onFailure={(isSuccess) => closePVRightSizingUpdatePopupForm(isSuccess)}
          data={kubernetesPVRightSizingUpdatePopupFormFormData}
        />
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

KubernetesPVCRightSizing.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  enabledFilters: PropTypes.bool,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesPVCRightSizing;
