import SummaryWidget from '@components1/optimise/SummaryWidget';
import { Box, Typography } from '@mui/material';
import SyncIcon from '@mui/icons-material/Sync';
import { action } from 'src/utils/actionStyles';
import React, { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Currency from '@components1/common/format/Currency';
import apiHome from '@api1/home';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Datetime from '@components1/common/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import { snackbar } from '@components1/common/snackbarService';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import ButtonMenu from '@components1/common/ButtonMenu';
import ResolveButton from '@components1/common/ResolveButton';
import Title from '@components1/common/Title';
import CodeMirror from '@uiw/react-codemirror';
import { yaml } from '@codemirror/lang-yaml';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import { colors } from 'src/utils/colors';
import { Modal } from '@components1/common/modal';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomLink from '@components1/common/CustomLink';
import useRecommendationExport from '@hooks/useRecommendationExport';

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

const KubernetesSpotRecommendation = (props) => {
  const router = useRouter();
  const [kubernetesSpotRecommendation, setKubernetesSpotRecommendation] = useState([]);
  const [kubernetesSpotRecommendationCount, setKubernetesSpotRecommendationCount] = useState(0);
  const [totalSpotRecommendationCount, setTotalSpotRecommendationCount] = useState(0);
  const [totalSpotEstimatedSaving, setTotalSpotEstimatedSaving] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);
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
  const { selectedCluster, allCluster } = useData();
  const [refreshTime, setRefreshTime] = useState({});
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id);
  }, [props?.kubernetes?.id]);

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'K8sSpotRecommendation',
    namespace: selectedNamespace,
    workloadType: selectedWorkloadType,
    status: recommendationStatus,
  });

  let jobName = 'spot_scan';

  useEffect(() => {
    if (props.isOptimisePage) {
      if (allCluster?.length) {
        setAccounts(allCluster);
      } else {
        apiHome.getCloudAccounts('K8s').then((res) => {
          setAccounts(res);
        });
      }
    }
  }, [props.isOptimisePage, allCluster]);

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

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const listSpotRecommendation = () => {
    if (!selectedAccountId && !props.isOptimisePage) {
      return;
    }
    if (props.isOptimisePage && accounts.length === 0) {
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
                <Text value={item.recommendation?.controller_name} sx={{ color: colors.text.primary }} />
                {props.isOptimisePage && (
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
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
                {hasWriteAccess(item.account_id || selectedAccountId) && (
                  <ResolveButton displayText sx={{ ...action.primary }} onClick={() => resolveSpotWorkloads(item)} />
                )}
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
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
    if (!selectedAccountId && !props.isOptimisePage) {
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

  const triggerRecommendationJob = () => {
    setIsRefreshLoading(true);
    recommendationApi
      .createRecommendationJob(selectedAccountId, 'spot_scan')
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
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={totalSpotRecommendationCount} />
        <SummaryWidget
          title='Total Cost'
          value={
            <Currency
              value={totalSpotEstimatedSaving}
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
              value={totalSpotEstimatedSaving * 12}
              sx={{ color: colors.text.secondary, fontSize: '28px', lineHeight: '28px', fontWeight: 600 }}
              withTooltip={false}
              suffix='/yr'
              isSavingPotential={true}
              recommendationLabel='Some of spot recommendations'
            />
          }
        />
      </Box>
      <BoxLayout2
        heading={props.heading === undefined ? 'Spot Recommendation' : props.heading}
        id='spot-recommendation'
        filterOptions={[
          ...(props.isOptimisePage
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
          {
            type: 'dropdown',
            label: 'Workload Type',
            options: ['Job', 'Deployment', 'Rollout', 'CronJob'],
            onSelect: function (e) {
              setSelectedWorkloadType(e?.target?.value);
              setPage(0);
            },
            value: selectedWorkloadType,
          },
        ]}
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: kubernetesSpotTable,
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
          ...(hasWriteAccess(selectedAccountId)
            ? [
                <CustomButton
                  showTooltip={!refreshTime}
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
          id={kubernetesSpotTable}
          headers={tableHeaders}
          data={kubernetesSpotRecommendation}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesSpotRecommendationCount}
          onPageChange={chanegPage}
          pageNumber={page + 1}
          stickyColumnIndex='6'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          sort={{
            name: 'Savings/mo',
            order: 'desc',
          }}
          loading={loading}
        />
      </BoxLayout2>
    </>
  );
};

KubernetesSpotRecommendation.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  resourceIds: PropTypes.arrayOf(PropTypes.string),
  groupName: PropTypes.string,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesSpotRecommendation;
