import { Box, Typography } from '@mui/material';
import React, { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import Currency from '@common-new/format/Currency';
import apiHome from '@api1/home';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import Datetime from '@common-new/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import { toast as snackbar } from '@components1/ds/Toast';
import { useData } from '@context/DataContext';
import Title from '@components1/common/Title';
import CodeMirror from '@uiw/react-codemirror';
import { yaml } from '@codemirror/lang-yaml';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import Text from '@common-new/format/Text';
import { colors, ds } from 'src/utils/colors';
import { Modal } from '@common-new/modal';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { Link as CustomLink } from '@components1/ds/Link';

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

export const KubernetesSpotUpdatePopupForm = ({ open, onClose }) => {
  return (
    <Modal
      width='lg'
      open={open}
      handleClose={onClose}
      title={'Spot Recommendation'}
      sx={{
        '& .MuiPaper-root': {
          maxWidth: '1010px',
          '& .MuiDialogContent-root': {
            padding: '16px 40px',
          },
        },
      }}
    >
      <Box display='flex' justifyContent='end' />
      <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left' my='5px' mx='10px' py='1px'>
        <Box mt={2}>
          <Title title='Why Use Spot Instances with EKS?' />
          <Typography variant='body1' color='textSecondary' paragraph>
            While Spot Instances come with the risk of interruption, they are ideal for certain Kubernetes workloads:
          </Typography>
          <Typography variant='body1' color='textSecondary' component='ul' sx={{ pl: 3 }}>
            <li>
              <strong>Stateless applications:</strong> These applications have no persistent data and can be easily restarted without data loss.
              Examples include batch processing tasks, data analysis pipelines, microservices, and high-performance computing jobs.
            </li>
            <li>
              <strong>Fault-tolerant applications:</strong> Applications designed to handle failures gracefully are well-suited for Spot Instances.
              Kubernetes&apos;s built-in features like pod scheduling and autoscaling further enhance fault tolerance.
            </li>
          </Typography>
        </Box>

        <Box mt={3}>
          <Title title='Mitigating the Side Effects of Spot Instances' />
          <Typography variant='body1' color='textSecondary' component='ul' sx={{ pl: 3 }}>
            <li>
              <strong>Interruptions:</strong> Kubernetes helps manage disruptions with features like Pod Disruption Budgets (PDBs) and draining nodes,
              but be prepared for potential downtime during Spot Instance terminations.
            </li>
            <li>
              <strong>Price Fluctuation:</strong> Spot Instance pricing is dynamic. Consider setting up autoscaling policies in your HPA to adjust the
              number of Pods based on changing Spot prices. This helps optimize costs by scaling down when prices rise and scaling up when prices are
              favorable.
            </li>
            <li>
              <strong>Limited Availability:</strong> There&apos;s no guarantee of securing the desired number of Spot Instances. Monitor Spot Instance
              capacity and adjust your bid prices or instance types if needed.
            </li>
          </Typography>
        </Box>

        <Box mt={3}>
          <Title title='Node Selector Vs Node Affinity' />
          <Typography variant='body1' color='textSecondary' component='div'>
            <Typography component='ul' sx={{ pl: 3 }}>
              <strong>Use nodeSelector when:</strong>
              <li>You need basic label-based matching for scheduling Pods.</li>
              <li>You want simplicity and ease of use.</li>
            </Typography>
            <Typography component='ul' sx={{ pl: 3, mt: 2 }}>
              <strong>Use nodeAffinity when:</strong>
              <li>You require more control over scheduling based on various node attributes.</li>
              <li>You need to define complex scheduling rules (e.g., required resources, anti-affinity).</li>
            </Typography>
          </Typography>
        </Box>
        <br />
        <Title title={'Using Node Selector'} />
        <CodeMirror
          value={
            "apiVersion: v1\nkind: Pod\nmetadata:\n  name: batch-processor\nspec:\n  nodeSelector:\n    eks.amazonaws.com/capacitytype: SPOT\n  containers:\n    - name: batch-processor\n      image: your-batch-processor-image\n      command:\n        - /bin/sh\n        - '-c'\n        - process data.txt"
          }
          height='300px'
          extensions={[yaml()]}
          editable={false}
          style={{
            border: '1px solid silver',
            marginTop: '10px',
          }}
        />
        <br />
        <Title title={'Using Node Affinity'} />
        <CodeMirror
          value={
            "apiVersion: v1\nkind: Pod\nmetadata:\n  name: with-node-affinity\nspec:\n  affinity:\n    nodeAffinity:\n      requiredDuringSchedulingIgnoredDuringExecution:\n        nodeSelectorTerms:\n          - matchExpressions:\n              - key: eks.amazonaws.com/capacitytype\n                operator: In\n                values:\n                  - SPOT\n  containers:\n    - name: batch-processor\n      image: your-batch-processor-image\n      command:\n        - /bin/sh\n        - '-c'\n        - process data.txt"
          }
          height='300px'
          extensions={[yaml()]}
          editable={false}
          style={{
            border: '1px solid silver',
            marginTop: '10px',
          }}
        />
      </Box>
    </Modal>
  );
};

KubernetesSpotUpdatePopupForm.propTypes = {
  open: PropTypes.bool,
  onClose: PropTypes.func,
  onSuccess: PropTypes.func,
  onFailure: PropTypes.func,
  data: PropTypes.object,
};

const RECOMMENDATION_HEADER = [
  { name: 'Application', width: '40%' },
  { name: 'Type', width: '15%' },
  { name: 'Namespace', width: '18%' },
  { name: 'Estimated Savings', width: '10%' },
  { name: 'Updated At', width: '5%' },
  '',
];

const KubernetesSpotRecommendation = ({ enabledSummary = true, enabledFilters = true, isOptimisePage = false, ...props }) => {
  const router = useRouter();
  const [kubernetesSpotRecommendation, setKubernetesSpotRecommendation] = useState([]);
  const [kubernetesSpotRecommendationCount, setKubernetesSpotRecommendationCount] = useState(0);
  const [totalSpotRecommendationCount, setTotalSpotRecommendationCount] = useState(0);
  const [totalSpotEstimatedSaving, setTotalSpotEstimatedSaving] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [namespaces, setNamespaces] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace || '');

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
  const [selectedWorkloadType, setSelectedWorkloadType] = useState('');
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [selectedAccountId, setSelectedAccountId] = useState(props?.kubernetes?.id);
  const [accounts, setAccounts] = useState([]);
  const { allCluster } = useData();
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id);
  }, [props?.kubernetes?.id]);

  const handleExportDownload = async (format) => {
    try {
      const exportFormat = format === 'xlsx' ? 'xlsx' : 'csv';
      const response = await recommendationApi.exportRecommendations({
        accountId: selectedAccountId,
        category: 'K8sSpotRecommendation',
        namespace: selectedNamespace || undefined,
        workloadType: selectedWorkloadType || undefined,
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

  const [openKubernetesSpotUpdatePopupForm, setOpenKubernetesSpotUpdatePopupForm] = useState(false);
  const [kubernetesSpotUpdatePopupFormData, setKubernetesSpotUpdatePopupFormData] = useState({});

  const kubernetesSpotTable = 'kubernetesSpotTable';

  const chanegPage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };
  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const resolveSpotWorkloads = (row) => {
    setKubernetesSpotUpdatePopupFormData(row);
    setOpenKubernetesSpotUpdatePopupForm(true);
  };

  const closeKubernetesSpotUpdatePopupForm = () => {
    setOpenKubernetesSpotUpdatePopupForm(false);
  };

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**App Name**: ' + data?.recommendation?.controller_name + '\n';
    description += '**App Type**: ' + (data?.recommendation?.controller_type || 'Job') + '\n';
    description += '**Namespace**: ' + data?.recommendation?.namespace + '\n';
    description += '**Estimated Savings**: ' + data?.estimated_savings + '\n';
    return description;
  };

  const openTicketForItem = (data) => {
    setTicketData(data);
    setIsTicketCreateFormOpen(true);
  };

  const listSpotRecommendation = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (isOptimisePage && accounts.length === 0) {
      return;
    }
    setLoading(true);
    setKubernetesSpotRecommendation([]);
    let recommendation = null;
    if (selectedNamespace && selectedWorkloadType) {
      recommendation = { namespace: selectedNamespace, type: selectedWorkloadType };
    } else if (selectedNamespace) {
      recommendation = { namespace: selectedNamespace };
    } else if (selectedWorkloadType) {
      recommendation = { type: selectedWorkloadType };
    }

    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'K8sSpotRecommendation',
        status: recommendationStatus ? [recommendationStatus] : [],
        recommendation: recommendation,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
        resource_ids: props?.resourceIds,
      })
      .then((res) => {
        setLoading(false);
        setKubernetesSpotRecommendationCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        let k8sRecommendationData = (res?.data?.recommendation || []).map((item) => {
          let data = [];
          data.push({
            component: (
              <>
                <Text value={item.recommendation?.controller_name} sx={{ color: colors.text.primary }} />
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
          });
          data.push({
            component: <Text value={item.recommendation?.type || 'Job'} />,
          });
          data.push({
            component: <Text value={item.resource_k8s_namespace || '-'} />,
          });
          data.push({
            component: (
              <Currency
                value={item.estimated_savings || '-'}
                precison={1}
                sxPrefix={{ fontSize: '12px', fontWeight: 400, color: colors.text.secondaryDark }}
              />
            ),
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
                    id={`spot-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={() => {
                      resolveSpotWorkloads(item);
                    }}
                  >
                    Optimize
                  </DsButton>
                )}
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `spot-action-ticket-${item.id}`,
                      label: item.ticket?.ticket_id ? `Ticket: ${item.ticket.ticket_id}` : 'Create ticket',
                      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
                      disabled: !!item.ticket?.ticket_id,
                      onSelect: () => {
                        openTicketForItem(item);
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
                      id={`spot-action-menu-${item.id}`}
                    />
                  }
                />
              </Box>
            ),
          });
          return data;
        });
        setKubernetesSpotRecommendation(k8sRecommendationData);
      })
      .catch((error) => {
        console.error(error);
        setLoading(false);
      });
  };

  useEffect(() => {
    listSpotRecommendation();
  }, [selectedAccountId, page, recommendationStatus, selectedNamespace, selectedWorkloadType, rowsPerPage, accounts.length, props?.resourceIds]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }

    recommendationApi
      .getK8sRecommendationSummary({
        accountId: selectedAccountId,
        category: 'K8sSpotRecommendation',
        status: ['Open', 'InProgress'],
        resource_ids: props?.resourceIds,
      })
      .then((res) => {
        setTotalSpotRecommendationCount(res?.data?.recommendation_aggregate.aggregate.count);
        setTotalSpotEstimatedSaving(res?.data?.recommendation_aggregate.aggregate.sum.estimated_savings);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [selectedAccountId, props?.resourceIds]);

  useEffect(() => {
    if (!selectedAccountId) {
      return;
    }

    recommendationApi
      .listRecommendationNamesapces({
        accountId: selectedAccountId,
        category: 'K8sSpotRecommendation',
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaces(res);
      });
  }, [selectedAccountId, recommendationStatus]);

  useEffect(() => {
    if (!props?.groupName) {
      return;
    }
    RECOMMENDATION_HEADER[0].name = `Application (${props?.groupName} Group)`;
  }, [props?.groupName]);

  const tableHeaders = React.useMemo(() => {
    if (!props?.groupName) {
      return RECOMMENDATION_HEADER;
    }
    const newHeaders = [...RECOMMENDATION_HEADER];
    newHeaders[0] = `Application (${props?.groupName} Group)`;
    return newHeaders;
  }, [props?.groupName]);

  const handleTicketSuccess = () => {
    listSpotRecommendation();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const accountOptions = React.useMemo(
    () => accounts.map((acc) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value })),
    [accounts]
  );

  const namespaceOptions = React.useMemo(() => (namespaces || []).map((n) => ({ label: n, value: n })), [namespaces]);

  const workloadTypeOptions = React.useMemo(() => ['Job', 'Deployment', 'Rollout', 'CronJob'].map((t) => ({ label: t, value: t })), []);

  const statusOptions = React.useMemo(() => RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s })), []);

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: `Move Workload ${ticketData.recommendation?.controller_name || ''} to Spot Instances`,
          description: getTicketDescription(ticketData),
          accountId: ticketData?.account_id || selectedAccountId,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      <KubernetesSpotUpdatePopupForm
        open={openKubernetesSpotUpdatePopupForm}
        onClose={closeKubernetesSpotUpdatePopupForm}
        onSuccess={closeKubernetesSpotUpdatePopupForm}
        onFailure={closeKubernetesSpotUpdatePopupForm}
        data={kubernetesSpotUpdatePopupFormData}
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
              info={{ tooltip: 'Active spot-eligible workload recommendations across the cluster' }}
              value={
                Number.isFinite(totalSpotRecommendationCount) ? totalSpotRecommendationCount.toLocaleString() : totalSpotRecommendationCount ?? '—'
              }
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Total Cost'
              info={{ tooltip: 'Estimated current monthly cost for spot-eligible workloads' }}
              value={
                totalSpotEstimatedSaving == null || totalSpotEstimatedSaving === '-' ? (
                  '—'
                ) : (
                  <CostCallout size='lg' tone='neutral' value={Number(totalSpotEstimatedSaving) || 0} period='/ mo' />
                )
              }
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Savings Potential'
              info={{ tooltip: 'Estimated yearly savings if eligible workloads move to spot instances' }}
              value={
                totalSpotEstimatedSaving == null || totalSpotEstimatedSaving === '-' ? (
                  '—'
                ) : (
                  <CostCallout size='lg' tone='high-savings' value={(Number(totalSpotEstimatedSaving) || 0) * 12} period='/ yr' />
                )
              }
            />
          </WidgetCard>
        </Box>
      )}

      <ListingLayout id='spot-recommendation'>
        <ListingLayout.Toolbar
          title={props.heading === undefined ? 'Spot Recommendations' : props.heading}
          data-testid='spot-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={selectedAccountId} jobName='spot_scan' idPrefix='spot' />}
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
                    id='spot-download'
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
                  id='spot-filter-account'
                  label='Account'
                  options={accountOptions}
                  value={accountOptions.find((o) => o.value === selectedAccountId) ?? null}
                  onSelect={(_e, item) => onAccountFilterChange({ target: { value: item?.value || '' } })}
                />
              )}
              <FilterDropdown
                id='spot-filter-status'
                label='Status'
                options={statusOptions}
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
                id='spot-filter-namespace'
                label='Namespace'
                options={namespaceOptions}
                value={selectedNamespace ? { label: selectedNamespace, value: selectedNamespace } : null}
                onSelect={(_e, item) => {
                  const next = item?.value || '';
                  setSelectedNamespace(next);
                  applyFiltersOnRouter(router, { namespace: next });
                  setPage(0);
                }}
              />
              <FilterDropdown
                id='spot-filter-workload-type'
                label='Workload Type'
                options={workloadTypeOptions}
                value={selectedWorkloadType ? { label: selectedWorkloadType, value: selectedWorkloadType } : null}
                onSelect={(_e, item) => {
                  setSelectedWorkloadType(item?.value || '');
                  setPage(0);
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={kubernetesSpotTable}
            headers={tableHeaders}
            tableData={kubernetesSpotRecommendation}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesSpotRecommendationCount}
            onPageChange={chanegPage}
            pageNumber={page + 1}
            loading={loading}
            stickyColumnIndex='6'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

KubernetesSpotRecommendation.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  resourceIds: PropTypes.arrayOf(PropTypes.string),
  groupName: PropTypes.string,
  isOptimisePage: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  enabledFilters: PropTypes.bool,
};

export default KubernetesSpotRecommendation;
