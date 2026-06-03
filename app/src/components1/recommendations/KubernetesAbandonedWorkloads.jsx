import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Input } from '@components1/ds/Input';

import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import { KubernetesNetwork } from '@components1/k8s/common/KubernetesTable2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import NumberComponent from '@common-new/format/Number';
import Currency from '@common-new/format/Currency';
import Datetime from '@common-new/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import KubernetesTracesListing from '@components1/k8s/details/KubernetesTracesListing';
import { formatDateForPlusMinusDuration } from 'src/utils/common';
import { useData } from '@context/DataContext';
import RecommendationResolution from '@components1/k8s/common/RecommendationResolution';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import Text from '@common-new/format/Text';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import NDialog from '@common-new/modal/NDialog';
import { colors, ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import apiHome from '@api1/home';
import { Link as CustomLink } from '@components1/ds/Link';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/ds/Tooltip';
import SafeIcon from '@components1/common/SafeIcon';

// DS V2 primitives — phased in alongside the legacy components. Visual swap only;
// API calls, handlers, and modal forms untouched.
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

          <Box sx={{ mt: 2 }}>
            <Input
              label='Enter workload Name'
              value={confirmationText}
              onChange={(value) => {
                setConfirmationText(value);
                setErrorText('');
              }}
              error={errorText || undefined}
              size='sm'
            />
          </Box>
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
  { name: 'Resource Name', width: '16%' },
  { name: 'Object Type', width: '10%' },
  { name: 'Namespaces', width: '12%' },
  { name: 'Network Traffic (bytes)', width: '18%' },
  { name: 'Observation Duration', width: '14%' },
  { name: 'Savings/mo', width: '10%' },
  { name: 'Updated At', width: '10%' },
  { name: '', width: '14%' },
];

const KubernetesAbandonedWorkloads = ({ enabledSummary = true, enabledFilters = true, isOptimisePage = false, ...props }) => {
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
  const { allCluster } = useData();

  const handleExportDownload = async (format) => {
    try {
      const exportFormat = format === 'xlsx' ? 'xlsx' : 'csv';
      const response = await recommendationApi.exportRecommendations({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'abandoned_resource',
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
                <NumberComponent
                  value={item?.recommendation?.traffic}
                  sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}
                />
                <br />
                <Text value={'Threshold: '} display={'inline'} />
                <NumberComponent
                  value={item?.recommendation?.threshold}
                  sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}
                />
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
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}
              >
                {hasWriteAccess(item.account_id || selectedAccountId) && (
                  <DsButton
                    tone='secondary'
                    size='xs'
                    id={`aw-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={() => {
                      resolveAbandonedWorkloads(item);
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
                      id={`abandoned-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
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
                    />
                  </span>
                </CustomTooltip>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `aw-action-ticket-${item.id}`,
                      label: item.ticket?.ticket_id ? `Ticket: ${item.ticket.ticket_id}` : 'Create ticket',
                      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
                      disabled: !!item.ticket?.ticket_id,
                      onSelect: () => {
                        onMenuClick({ id: 0 }, item);
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
                      id={`aw-action-menu-${item.id}`}
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
              info={{ tooltip: 'Active abandoned-app recommendations across the cluster' }}
              value={
                Number.isFinite(kubernetesAbandonedWorkloadsCount)
                  ? kubernetesAbandonedWorkloadsCount.toLocaleString()
                  : kubernetesAbandonedWorkloadsCount ?? '—'
              }
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Savings Potential'
              info={{ tooltip: 'Estimated monthly savings if abandoned workloads are removed' }}
              value={
                estimatedSavings == null || estimatedSavings === '-' ? (
                  '—'
                ) : (
                  <CostCallout size='lg' tone='high-savings' value={Number(estimatedSavings) || 0} period='/ mo' />
                )
              }
            />
          </WidgetCard>
        </Box>
      )}

      <ListingLayout id='abandoned-workloads'>
        <ListingLayout.Toolbar
          title={props.heading === undefined ? 'Abandoned Apps' : props.heading}
          data-testid='aw-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={selectedAccountId} jobName='abandoned_workload_scan' idPrefix='aw' />}
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
                    id='aw-download'
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
                  id='aw-filter-account'
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
                id='aw-filter-namespace'
                label='Namespace'
                options={(namespaces || []).map((n) => ({ label: n, value: n }))}
                value={selectedNamespace ? { label: selectedNamespace, value: selectedNamespace } : null}
                onSelect={(_e, item) => {
                  const next = item?.value || '';
                  setSelectedNamespace(next);
                  applyFiltersOnRouter(router, { namespace: next });
                }}
              />
              <FilterDropdown
                id='aw-filter-status'
                label='Status'
                options={RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }))}
                value={
                  recommendationStatus
                    ? { label: recommendationStatus === 'InProgress' ? 'In Progress' : recommendationStatus, value: recommendationStatus }
                    : null
                }
                onSelect={(_e, item) => {
                  setRecommendationStatus(item?.value || '');
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={kubernetesAbandonedWorkloadsTable}
            headers={ABANDONED_WORKLOADS_HEADER}
            tableData={kubernetesAbandonedWorkloads}
            rowsPerPage={recordsPerPage}
            totalRows={kubernetesAbandonedWorkloadsCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'Traces',
                  componentFn: (_option, drilldownQuery) => <TracesTabComponent drilldownQuery={drilldownQuery} accountId={selectedAccountId} />,
                },
                {
                  text: 'Network Traffic',
                  componentFn: (_option, drilldownQuery) => (
                    <NetworkTrafficTabComponent drilldownQuery={drilldownQuery} accountId={selectedAccountId} />
                  ),
                },
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

KubernetesAbandonedWorkloads.propTypes = {
  kubernetes: PropTypes.object,
  heading: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  enabledFilters: PropTypes.bool,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesAbandonedWorkloads;
