import React, { useEffect, useState } from 'react';
import { Box, Typography, Button } from '@mui/material';
import apiAutoPilot from '@api1/autoPilot';
import { useRouter } from 'next/router';
import TextWithToolTip from '@components1/common/TextWithToolTip';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2, {
  KubernetesUtilizationCharts,
  KubernetesCostCharts,
  KubernetesPVCUtilization,
} from '@components1/k8s/common/KubernetesTable2';
import CustomTable2 from '@components1/common/tables/CustomTable2';

import KubernetesDeploymentHistory from '@components1/k8s/common/KubernetesDeploymentHistory';

import LinkIcon from '@mui/icons-material/Link';
import CustomLabels from '@components1/common/widgets/CustomLabels';
const LISTING_HEADER = [
  { name: 'Scheduled Time', width: '10%' },
  { name: 'Status', width: '8%' },
  { name: 'Resource', width: '20%' },
  { name: 'Message', width: '35%' },
  { name: 'PR/Ticket', width: '10%' },
  { name: 'Recommendation', width: '12%' },
];
const DRILL_DOWN_LISTING_HEADER = ['Name', 'Old Value', 'New Value'];

import DateTime from '@components1/common/format/Datetime';
import { titleCase } from '@lib/formatter';
import PropTypes from 'prop-types';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import CustomLink from '@components1/common/CustomLink';
import CustomBackButton from '@components1/common/CustomBackButton';
import CustomTabs from '@components1/common/CustomTabsForDrilldown';
import { hasWriteAccess } from '@lib/auth';
import CustomButton from '@components1/common/NewCustomButton';
import NDialog from '@components1/common/modal/NDialog';
import { snackbar } from '@components1/common/snackbarService';
import AutoPilotApprovalStatusListingModal from '@components1/autopilot/AutoPilotApprovalStatusListingModal';

import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { Modal } from '@components1/common/modal';
import apiAccount from '@api1/account';
import apiRecommendations from '@api1/recommendation';

const PRTicketLink = ({ prResolution, ticketLink }) => {
  if (prResolution?.type_reference_id) {
    return (
      <Box
        component='a'
        href={prResolution.type_reference_id}
        target='_blank'
        rel='noopener'
        onClick={(e) => e.stopPropagation()}
        sx={{ display: 'flex', alignItems: 'center', gap: '4px', color: '#2563EB', fontSize: '13px', textDecoration: 'none' }}
      >
        <LinkIcon sx={{ fontSize: '14px' }} />
        PR
        <CustomLabels margin='0' text={prResolution.status} />
      </Box>
    );
  }
  if (ticketLink) {
    return (
      <Box
        component='a'
        href={ticketLink}
        target='_blank'
        rel='noopener'
        onClick={(e) => e.stopPropagation()}
        sx={{ display: 'flex', alignItems: 'center', gap: '4px', color: '#2563EB', fontSize: '13px', textDecoration: 'none' }}
      >
        <LinkIcon sx={{ fontSize: '14px' }} />
        Ticket
      </Box>
    );
  }
  return <Typography sx={{ color: '#9CA3AF', fontSize: '13px' }}>-</Typography>;
};

PRTicketLink.propTypes = {
  prResolution: PropTypes.shape({
    type_reference_id: PropTypes.string,
    status: PropTypes.string,
  }),
  ticketLink: PropTypes.string,
};

const AutoOptimizeTasks = ({ enableFilters = true }) => {
  const router = useRouter();
  const statusFilter = [
    { label: 'Complete', value: 'Complete' },
    { label: 'Scheduled', value: 'Scheduled' },
    { label: 'Executed', value: 'Executed' },
    { label: 'Failed', value: 'Failed' },
    { label: 'Skipped', value: 'Skipped' },
    { label: 'Dryrun', value: 'Dryrun' },
    { label: 'In Progress', value: 'In_Progress' },
  ];

  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [selectedStatus, setSelectedStatus] = useState('Complete');
  const [loading, setLoading] = useState(false);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  useEffect(() => {
    let query = {
      accountId: router?.query?.TaskDetails,
      status: selectedStatus,
    };
    setLoading(true);
    apiAutoPilot
      .listAutoPilotTask(recordsPerPage, currentPage * recordsPerPage, query)
      .then(async (res) => {
        const tasks = res?.data?.auto_pilot_task_listing || [];

        // Fetch PR resolutions for tasks with recommendation_ids
        const recIds = [...new Set(tasks.map((t) => t.recommendation_id).filter(Boolean))];
        let prMap = {};
        if (recIds.length > 0) {
          try {
            const resolutionMap = await apiRecommendations.listPRResolutionsByRecommendationIds(recIds);
            resolutionMap.forEach((value, key) => {
              prMap[key] = value;
            });
          } catch (e) {
            console.error('Error fetching PR resolutions:', e);
          }
        }
        let data = tasks.map((item) => {
          let resourceFilter = item.resource_filter && Object.keys(item.resource_filter).length > 0 ? item.resource_filter : {};

          if (Object.keys(resourceFilter).length === 0) {
            resourceFilter = item?.auto_pilot?.auto_optimize_resource_maps?.[0]?.resource_identifier ?? {};
          }

          if (Object.keys(resourceFilter).length === 0 && item?.meta) {
            resourceFilter = {
              namespace: item?.meta?.namespace,
              name: item?.meta?.name,
              type: item?.meta?.kind,
            };
          }

          return [
            { component: <DateTime value={item?.scheduled_time} />, drilldownQuery: item },
            { text: <CustomLabels margin='auto' text={item?.status} /> },
            {
              component: (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                  {resourceFilter?.name ? (
                    <CustomLink
                      href={`/kubernetes/details/${item?.auto_pilot?.cloud_account?.id}?namespace=${resourceFilter?.namespace ?? ''}&workloadName=${
                        resourceFilter?.name ?? ''
                      }#kubernetes/applications`}
                      passHref={true}
                      onClick={(e) => e.stopPropagation()}
                    >
                      <Typography
                        component={'a'}
                        style={{
                          color: '#374151',
                          fontSize: '13px',
                          textDecoration: 'underline',
                          textDecorationColor: 'lightgray',
                        }}
                      >
                        {resourceFilter?.name}
                      </Typography>
                    </CustomLink>
                  ) : (
                    <Typography sx={{ color: '#374151', fontSize: '13px' }}>-</Typography>
                  )}
                  {resourceFilter?.namespace && <Typography sx={{ color: '#9CA3AF', fontSize: '12px' }}>ns: {resourceFilter.namespace}</Typography>}
                  {resourceFilter?.type && <Typography sx={{ color: '#9CA3AF', fontSize: '12px' }}>type: {resourceFilter.type}</Typography>}
                </Box>
              ),
            },
            { component: <TextWithToolTip text={item?.reason} markdown lines={3} /> },
            {
              component: (
                <PRTicketLink
                  prResolution={item?.recommendation_id ? prMap[item.recommendation_id] : null}
                  ticketLink={item?.attributes?.ticket_link}
                />
              ),
            },
            {
              component: (
                <Button
                  variant='outlined'
                  startIcon={<LinkIcon />}
                  sx={{ marginLeft: '20px', height: '22px', textTransform: 'none' }}
                  size='small'
                  onClick={(e) => {
                    e.stopPropagation();
                    handleButtonClick(item?.auto_pilot?.cloud_account?.id, item);
                  }}
                >
                  link
                </Button>
              ),
            },
          ];
        });
        let totalCount = res?.data?.auto_pilot_task_aggregate?.aggregate?.count;
        setData(data);
        setTotalCount(totalCount);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [currentPage, recordsPerPage, selectedStatus, router?.query?.TaskDetails]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const handleButtonClick = (id, item) => {
    if (item?.auto_pilot?.category == 'horizontal_rightsize') {
      router.push(`/kubernetes/details/${id}#optimize/replica-rightsizing`);
    } else {
      router.push(`/kubernetes/details/${id}?recommendation_id=${item?.recommendation_id}#optimize`);
    }
  };

  const onStatusFilterChange = (e, _p) => {
    setCurrentPage(0);
    setSelectedStatus(e?.target?.value);
  };

  const getCPUMemoryConfigs = (data) => {
    return data.map((item) => {
      return [{ text: titleCase(item.property_name) || '-' }, { text: item.current_value || '-' }, { text: item.new_value || '-' }];
    });
  };

  const parseRecommendationMeta = (meta) => {
    if (meta?.changes && meta?.changes?.length > 0) {
      return getCPUMemoryConfigs(meta.changes);
    }

    const recommendation = meta?.recommendation;
    const name = meta?.name;

    if (recommendation && name && recommendation[name]) {
      const configs = [];
      recommendation[name].forEach((rec) => {
        const resource = rec.resource;
        if (rec.allocated) {
          Object.keys(rec.allocated).forEach((key) => {
            if (rec.allocated[key] !== null || (rec.recommended && rec.recommended[key] !== null)) {
              configs.push([
                { text: titleCase(`${resource} ${key}`) },
                { text: rec.allocated[key] ?? '-' },
                { text: rec.recommended ? rec.recommended[key] ?? '-' : '-' },
              ]);
            }
          });
        }
      });
      return configs;
    }

    return [];
  };

  return (
    <BoxLayout2
      filterOptions={
        enableFilters
          ? [
              {
                type: 'dropdown',
                enabled: true,
                options: statusFilter,
                onSelect: onStatusFilterChange,
                minWidth: '150px',
                label: 'Status',
                value: selectedStatus,
              },
            ]
          : []
      }
      id='auto-pilot'
    >
      <KubernetesTable2
        id='auto-pilot'
        headers={LISTING_HEADER}
        expandable={{
          tabs: [
            {
              componentFn: function (opt, drilldownQuery, _row) {
                return (
                  <Box>
                    <CustomTable2 headers={DRILL_DOWN_LISTING_HEADER} tableData={parseRecommendationMeta(drilldownQuery?.meta)} />
                    {drilldownQuery?.command && (
                      <Box sx={{ mt: 1.5, px: 1 }}>
                        <Typography sx={{ fontSize: '12px', color: '#6B7280', fontWeight: 500, mb: '4px' }}>Command</Typography>
                        <Typography
                          component='pre'
                          sx={{
                            fontSize: '13px',
                            color: '#374151',
                            backgroundColor: '#F9FAFB',
                            border: '1px solid #E5E7EB',
                            borderRadius: '6px',
                            p: 1.5,
                            fontFamily: 'monospace',
                            whiteSpace: 'pre-wrap',
                            wordBreak: 'break-word',
                            m: 0,
                          }}
                        >
                          {drilldownQuery.command}
                        </Typography>
                      </Box>
                    )}
                  </Box>
                );
              },
              text: 'Details',
              value: 0,
              key: 'details',
            },
          ],
        }}
        rowsPerPage={recordsPerPage}
        data={data}
        onPageChange={onPageChange}
        totalRows={totalCount}
        showExpandable
        loading={loading}
        textAlign='center'
        tableHeadingCenter={['Status']}
        pageNumber={currentPage + 1}
      />
    </BoxLayout2>
  );
};

AutoOptimizeTasks.propTypes = {
  enableFilters: PropTypes.bool,
};

const AutoOptimizeSummary = ({ autoOptimizeData = {} }) => {
  const [workloadData, setWorkloadData] = useState({});
  const [selectedContainer, setSelectedContainer] = useState('');
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [workloadFilter, setWorkloadFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState();
  const [selectedWorkload, setSelectedWorkload] = useState();
  const [selectedWorkloadType, setSelectedWorkloadType] = useState();

  useEffect(() => {
    if (!autoOptimizeData?.account_id) {
      return;
    }
    if (!selectedNamespace) {
      return;
    }
    if (!selectedWorkload) {
      return;
    }

    k8sApi
      .getK8sWorkload(1, 0, {
        accountId: autoOptimizeData?.account_id,
        namespaceName: selectedNamespace,
        workloadName: selectedWorkload,
      })
      .then((res) => {
        setWorkloadData(res?.data?.k8s_workloads?.[0] ?? {});
        setSelectedContainer(res?.data?.k8s_workloads?.[0]?.meta?.config?.containers?.[0]?.name);
      });
  }, [autoOptimizeData?.account_id, selectedNamespace, selectedWorkload]);

  useEffect(() => {
    if (!selectedNamespace) {
      return;
    }
    let workloads =
      autoOptimizeData?.auto_optimize_resource_maps
        ?.filter((e) => e.resource_identifier?.namespace === selectedNamespace && e.resource_identifier?.name)
        ?.map((e) => {
          return { name: e.resource_identifier?.name, type: e.resource_identifier?.type };
        }) ?? [];

    if (workloads?.length === 0) {
      k8sApi
        .getK8sWorkload(100, 0, {
          accountId: autoOptimizeData?.account_id,
          namespaceName: selectedNamespace,
        })
        .then((res) => {
          let workloads = res?.data?.k8s_workloads?.map((w) => {
            return { name: w.name, type: w.kind };
          });
          setWorkloadFilter(workloads);
          if (workloads?.length > 0) {
            setSelectedWorkload(workloads[0].name);
            setSelectedWorkloadType(workloads[0].type);
          }
        })
        .catch((error) => {
          console.error(error);
        });
    } else {
      setWorkloadFilter(workloads);
      if (workloads?.length > 0) {
        setSelectedWorkload(workloads[0].name);
        setSelectedWorkloadType(workloads[0].type);
      }
    }
  }, [autoOptimizeData?.account_id, selectedNamespace]);

  useEffect(() => {
    if (autoOptimizeData?.auto_optimize_resource_maps?.length > 0) {
      let namespaces = [...new Set(autoOptimizeData?.auto_optimize_resource_maps?.map((e) => e.resource_identifier?.namespace) ?? [])];
      setNamespaceFilter(namespaces);
      if (namespaces?.length > 0) {
        setSelectedNamespace(namespaces[0]);
      }
    }
  }, [autoOptimizeData?.auto_optimize_resource_maps?.length]);

  const onContainerChange = (e) => {
    setSelectedContainer(e?.target?.value);
  };

  return (
    <BoxLayout2
      sx={{ position: 'relative', width: '100%' }}
      filterOptions={[
        {
          type: 'dropdown',
          options: namespaceFilter,
          lable: 'Namespace',
          onSelect: (_e, v) => {
            setSelectedNamespace(v);
            setSelectedContainer('');
          },
          value: selectedNamespace,
        },
        {
          type: 'dropdown',
          options: workloadFilter?.map((e) => e.name) ?? [],
          lable: 'Workload',
          onSelect: (_e, v) => {
            setSelectedWorkload(v);
            let workload = workloadFilter?.find((e) => e.name === v);
            if (workload) {
              setSelectedWorkloadType(workload.type);
            }
            setSelectedContainer('');
          },
          value: selectedWorkload,
        },
      ]}
    >
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr 1fr',
          gap: '15px',
          m: '15px 0px',
          flexWrap: 'wrap',
          '@media (max-width: 1350px)': {
            gridTemplateColumns: '1fr 1fr 1fr 1fr 1fr',
            gap: '10px',
          },
        }}
      >
        <Box>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              borderRadius: '4px',
              minHeight: '50px',
              height: '100% !important',
              backgroundColor: '#ffffff !important',
              border: '0.5px solid #4ADE80 !important',
              boxShadow: '0px 4px 6px -1px #E5E5E599',
              '@media (max-width: 1350px)': {
                padding: '16px 10px',
              },
            }}
          >
            <Box display={'flex'} gap='12px' justifyContent={'space-between'} alignItems={'center'} height={'100%'}>
              <Box>
                <Text value={'Namespace'} secondaryText />
                <CustomLink
                  href={`/kubernetes/details/${autoOptimizeData.account_id}?namespace=${selectedNamespace ?? ''}#kubernetes/namespaces`}
                  passHref={true}
                >
                  <Typography
                    style={{
                      color: '#374151',
                      textDecoration: 'underline',
                      textDecorationColor: 'lightgray',
                      fontSize: '20px',
                      fontWeight: 500,
                    }}
                    variant='h5'
                  >
                    {selectedNamespace || '-'}
                  </Typography>
                </CustomLink>
              </Box>
            </Box>
          </SummaryBlock>
        </Box>
        <Box>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              borderRadius: '4px',
              minHeight: '50px',
              height: '100% !important',
              backgroundColor: '#ffffff !important',
              border: '0.5px solid #4ADE80 !important',
              boxShadow: '0px 4px 6px -1px #E5E5E599',
              '@media (max-width: 1350px)': {
                padding: '16px 10px',
              },
            }}
          >
            <Box Box display={'flex'} gap='12px' justifyContent={'space-between'} alignItems={'center'} height={'100%'}>
              <Box>
                <Text value={autoOptimizeData.category === 'pv_rightsize' ? 'Persistent Volume Claim' : 'Workload'} secondaryText />
                <CustomLink
                  href={
                    autoOptimizeData.category === 'pv_rightsize'
                      ? `/kubernetes/details/${autoOptimizeData?.account_id}?namespace=${selectedNamespace}&pvcName=${selectedWorkload}#kubernetes/pvc`
                      : `/kubernetes/details/${autoOptimizeData?.account_id}?namespace=${selectedNamespace ?? ''}&workloadName=${
                          selectedWorkload ?? ''
                        }#kubernetes/applications`
                  }
                  passHref={true}
                >
                  <Typography
                    style={{
                      color: '#374151',
                      textDecoration: 'underline',
                      textDecorationColor: 'lightgray',
                      fontSize: '20px',
                      fontWeight: 500,
                    }}
                    variant='h4'
                  >
                    {selectedWorkload}
                  </Typography>
                </CustomLink>
              </Box>
            </Box>
          </SummaryBlock>
        </Box>
        <Box>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              borderRadius: '4px',
              minHeight: '50px',
              height: '100% !important',
              backgroundColor: '#ffffff !important',
              border: '0.5px solid #4ADE80 !important',
              boxShadow: '0px 4px 6px -1px #E5E5E599',
              '@media (max-width: 1350px)': {
                padding: '16px 10px',
              },
            }}
          >
            <Box Box display={'flex'} gap='12px' justifyContent={'space-between'} alignItems={'center'} height={'100%'}>
              <Box>
                <Text value={'Kind'} secondaryText />
                <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                  {selectedWorkloadType}
                </Typography>
              </Box>
            </Box>
          </SummaryBlock>
        </Box>
        <Box>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              borderRadius: '4px',
              minHeight: '50px',
              height: '100% !important',
              backgroundColor: '#ffffff !important',
              border: '0.5px solid #4ADE80 !important',
              boxShadow: '0px 4px 6px -1px #E5E5E599',
              '@media (max-width: 1350px)': {
                padding: '16px 10px',
              },
            }}
          >
            <Box Box display={'flex'} gap='12px' justifyContent={'space-between'} alignItems={'center'} height={'100%'}>
              <Box>
                <Text value={'Category'} secondaryText />
                <Typography variant='h4' sx={{ fontSize: '20px', fontWeight: 500, color: '#374151' }}>
                  {autoOptimizeData?.category}
                </Typography>
              </Box>
            </Box>
          </SummaryBlock>
        </Box>
        <Box>
          <SummaryBlock
            hideTitle
            height='100%'
            sx={{
              borderRadius: '4px',
              minHeight: '50px',
              height: '100% !important',
              backgroundColor: '#ffffff !important',
              border: '0.5px solid #4ADE80 !important',
              boxShadow: '0px 4px 6px -1px #E5E5E599',
              '@media (max-width: 1350px)': {
                padding: '16px 10px',
              },
            }}
          >
            <Box Box display={'flex'} gap='12px' justifyContent={'space-between'} alignItems={'center'} height={'100%'}>
              <Box>
                <Text value={'Last Executed At'} secondaryText />
                <DateTime sx={{ fontSize: '20px', fontWeight: 500 }} value={autoOptimizeData?.last_executed_time} />
              </Box>
            </Box>
          </SummaryBlock>
        </Box>
      </Box>
      <Box mb={2} />
      {autoOptimizeData.category == 'pv_rightsize' && (
        <KubernetesPVCUtilization
          heading='Memory & CPU Utilization Trend'
          accountId={autoOptimizeData?.account_id}
          query={{
            namespaceName: selectedNamespace,
            pvcName: selectedWorkload,
          }}
        />
      )}
      {autoOptimizeData.category != 'pv_rightsize' && selectedContainer && (
        <KubernetesUtilizationCharts
          heading='Memory & CPU Utilization Trend'
          accountId={autoOptimizeData?.account_id}
          query={{
            namespaceName: selectedNamespace,
            workloadName: selectedWorkload,
            containerName: selectedContainer,
          }}
          additionalFilters={[
            {
              type: 'dropdown',
              enabled: true,
              options: workloadData?.meta?.config?.containers?.map((m) => m.name) ?? [],
              onSelect: onContainerChange,
              minWidth: '150px',
              label: 'Container',
              value: selectedContainer,
            },
          ]}
        />
      )}
      <Box mb={2} />
      {selectedNamespace && selectedWorkload && (
        <KubernetesCostCharts
          heading={'Cost Trend'}
          accountId={autoOptimizeData?.account_id}
          query={{
            namespaceName: selectedNamespace,
            workloadName: selectedWorkload,
            containerName: selectedContainer,
          }}
        />
      )}
      <Box mb={2} />
      <KubernetesDeploymentHistory
        accountId={autoOptimizeData?.account_id}
        subjectName={selectedWorkload}
        subjectNamespace={selectedNamespace}
        subjectType={selectedWorkloadType}
        heading='Deployment History'
      />
    </BoxLayout2>
  );
};

AutoOptimizeSummary.propTypes = {
  autoOptimizeData: PropTypes.object,
};

const AutoOptimizeDetails = () => {
  const tabOptions = [
    { id: 'tasks', text: 'Tasks', value: 0 },
    { id: 'details', text: 'Details', value: 1 },
  ];
  const router = useRouter();

  const [autoOptimizeTab, setAutoOptimizeTab] = useState(0);
  const [autoOptimizeData, setAutoOptimizeData] = useState({});

  const [disableAutoOptimize, setDisableAutoOptimize] = useState(false);
  const [approvalStatusModalOpen, setApprovalStatusModalOpen] = useState(false);
  const [loading, setLoading] = useState(false);

  const [openCreateAutoOptimize, setOpenCreateAutoOptimize] = useState(false);
  const [openCreateAutoOptimizeType, setOpenCreateAutoOptimizeType] = useState(null);
  const [modalLoading, setModalLoading] = useState(false);

  const [msTeamsData, setMsTeamsData] = useState([]);
  const [googleChannelList, setGoogleChannelList] = useState([]);

  const handleChangeRunbookTab = (_e, value) => {
    setAutoOptimizeTab(value);
  };

  const getMenuItems = (item) => {
    if (!hasWriteAccess(item?.account_id || router.query?.accountId)) {
      return [];
    }

    let menuItems = [];
    menuItems.push({
      label: 'Edit',
      id: 1,
    });

    const status = item?.status?.toUpperCase();

    if (status === 'ACTIVE') {
      menuItems.push({
        label: 'Disable',
        id: 0,
      });
    } else if (status !== 'DRAFT') {
      menuItems.push({
        label: 'Enable',
        id: 0,
      });
    } else if (status === 'DRAFT') {
      menuItems.push({
        label: 'Check Status',
        id: 2,
      });
    }

    return menuItems;
  };

  const handleEditClick = () => {
    setModalLoading(true);
    fetchNotificationChannels()
      .then(() => {
        handleOpenCreateAutoOptimize(autoOptimizeData?.category);
        setModalLoading(false);
      })
      .catch(() => {
        setModalLoading(false);
      });
  };

  const handleOpenCreateAutoOptimize = (category) => {
    setOpenCreateAutoOptimizeType(category);
    setOpenCreateAutoOptimize(true);
  };

  const closeAutoPilotSingleConfigModal = (success) => {
    setOpenCreateAutoOptimize(false);
    setOpenCreateAutoOptimizeType(null);
    setModalLoading(false);
    // Refresh data after modal close if successful
    if (success && router?.query?.TaskDetails) {
      apiAutoPilot.listAutoPilot(1, 0, { id: router?.query?.TaskDetails }).then((res) => {
        setAutoOptimizeData(res?.data?.auto_pilot_listing?.[0]);
      });
    }
  };

  const fetchNotificationChannels = async () => {
    try {
      const [msTeamsRes, googleRes] = await Promise.all([
        apiAccount.getNotificationChannelList('ms_teams'),
        apiAccount.getNotificationChannelList('google_chat'),
      ]);

      setMsTeamsData(
        msTeamsRes?.data?.data?.map((item) => ({
          label: item.name,
          value: item.id,
          channels: item.channels,
        })) || []
      );

      setGoogleChannelList(
        googleRes?.data?.data?.map((item) => ({
          label: item.name,
          value: item.id,
        })) || []
      );
    } catch (error) {
      console.error('Error fetching notification channels:', error);
    }
  };

  const handleToggleStatusClick = () => {
    setDisableAutoOptimize(true);
  };

  const handleCheckStatusClick = () => {
    setApprovalStatusModalOpen(true);
  };

  const handleSubmit = () => {
    setLoading(true);
    apiAutoPilot
      .updateAutoPilotStatus(
        autoOptimizeData.id,
        autoOptimizeData?.account_id || router.query?.accountId,
        autoOptimizeData.status == 'Active' ? 'Disabled' : 'Active'
      )
      .then((res) => {
        if (res?.data.errors) {
          snackbar.error(
            `Failed to update ${autoOptimizeData.status == 'Active' ? 'Disabled' : 'Active'} status on autopilot "${autoOptimizeData.name}"`
          );
        } else {
          snackbar.success(`Autopilot "${autoOptimizeData.name}" ${autoOptimizeData.status == 'Active' ? 'disabled' : 'enabled'} Successfully`);
          // Refresh data
          apiAutoPilot.listAutoPilot(1, 0, { id: router?.query?.TaskDetails }).then((res) => {
            setAutoOptimizeData(res?.data?.auto_pilot_listing?.[0]);
          });
        }
        handleCloseAutoOptimizePopUp();
      })
      .catch(() => {
        snackbar.error(
          `Failed to update ${autoOptimizeData.status == 'Active' ? 'Disabled' : 'Active'} status on autopilot "${autoOptimizeData.name}"`
        );
        handleCloseAutoOptimizePopUp();
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const handleCloseAutoOptimizePopUp = () => {
    setDisableAutoOptimize(false);
    setApprovalStatusModalOpen(false);
  };

  const handleGoBack = () => {
    router.push('/auto-pilot?accountId=' + router.query.accountId + '#auto-optimize');
  };

  useEffect(() => {
    if (!router?.query?.TaskDetails) {
      return;
    }
    apiAutoPilot.listAutoPilot(1, 0, { id: router?.query?.TaskDetails }).then((res) => {
      setAutoOptimizeData(res?.data?.auto_pilot_listing?.[0]);
    });
  }, [router?.query?.TaskDetails]);

  return (
    <>
      <AutoPilotApprovalStatusListingModal
        id={autoOptimizeData?.id}
        name={autoOptimizeData?.name}
        open={approvalStatusModalOpen}
        handleClose={() => setApprovalStatusModalOpen(false)}
      />

      <NDialog
        buttonText='Confirm'
        handleClose={handleCloseAutoOptimizePopUp}
        dialogTitle={`${autoOptimizeData?.status == 'Active' ? 'Disable' : 'Enable'} Auto Optimize "${autoOptimizeData?.name}"`}
        handleSubmit={handleSubmit}
        open={disableAutoOptimize}
        loading={loading}
        disabled={loading}
        dialogContent={`Are you sure you want to ${autoOptimizeData?.status == 'Active' ? 'Disable' : 'Enable'} this "${
          autoOptimizeData?.name
        }" auto optimize?`}
      />
      <Modal
        width={openCreateAutoOptimizeType === 'horizontal_rightsize' ? 'lg' : 'md'}
        open={openCreateAutoOptimize}
        handleClose={() => closeAutoPilotSingleConfigModal(false)}
        title={
          openCreateAutoOptimizeType === 'vertical_rightsize'
            ? `Update Auto Optimize Configuration - Scheduled Vertical RightSizing`
            : openCreateAutoOptimizeType === 'horizontal_rightsize'
            ? `Update Auto Optimize - Replica RightSizing`
            : openCreateAutoOptimizeType === 'pvc_rightsize'
            ? `Update Auto Optimize - Persistent Volume Claim Rightsizing`
            : openCreateAutoOptimizeType === 'continuous_rightsize'
            ? `Update Auto Optimize Configuration - Vertical RightSizing`
            : `Update Auto Optimize`
        }
        loader={loading || modalLoading}
      >
        {openCreateAutoOptimizeType === 'vertical_rightsize' && (
          <AutoOptimizeVerticalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={false}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={false}
            setIsLoading={setLoading}
            currentData={{}}
          />
        )}
        {openCreateAutoOptimizeType === 'horizontal_rightsize' && (
          <AutoOptimizeHorizontalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={false}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={false}
            setIsLoading={setLoading}
            currentData={{}}
          />
        )}
        {openCreateAutoOptimizeType === 'pvc_rightsize' && (
          <AutoOptimizePVRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={false}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={false}
            setIsLoading={setLoading}
          />
        )}
        {openCreateAutoOptimizeType === 'continuous_rightsize' && (
          <AutoOptimizeContinuousVerticalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={false}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={false}
            setIsLoading={setLoading}
          />
        )}
      </Modal>

      <Box sx={{ position: 'relative' }}>
        <Box sx={{ position: 'absolute', left: '-43px', top: '24px' }}>
          <CustomBackButton onClick={handleGoBack} />
        </Box>

        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', margin: '30px 0 10px 0' }}>
          <Typography
            sx={{
              color: '#374151',
              fontSize: '24px',
              fontWeight: 600,
              lineHeight: '120.418%',
            }}
          >
            {'Auto Optimize Task > ' + (autoOptimizeData?.name || '')}
          </Typography>

          {autoOptimizeData &&
            Object.keys(autoOptimizeData).length > 0 &&
            hasWriteAccess(autoOptimizeData?.account_id || router.query?.accountId) && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                {getMenuItems(autoOptimizeData).map((menuItem) => {
                  if (menuItem.id === 1) {
                    return (
                      <CustomButton
                        key='edit'
                        variant='primary'
                        size='Small'
                        text='Edit'
                        onClick={handleEditClick}
                        loading={modalLoading}
                        disabled={modalLoading}
                        sx={{ minWidth: '80px', padding: '6px 12px' }}
                      />
                    );
                  } else if (menuItem.id === 0) {
                    return (
                      <CustomButton
                        key='toggle'
                        variant={autoOptimizeData.status?.toUpperCase() === 'ACTIVE' ? 'secondary' : 'primary'}
                        size='Small'
                        text={menuItem.label}
                        onClick={handleToggleStatusClick}
                        sx={{ minWidth: '80px', padding: '6px 12px' }}
                      />
                    );
                  } else if (menuItem.id === 2) {
                    return <CustomButton key='check-status' variant='tertiary' size='Small' text='Check Status' onClick={handleCheckStatusClick} />;
                  }
                  return null;
                })}
              </Box>
            )}
        </Box>

        <CustomTabs options={tabOptions} value={autoOptimizeTab} onChange={handleChangeRunbookTab} />
        {autoOptimizeTab == 0 && <AutoOptimizeTasks />}
        {autoOptimizeTab == 1 && <AutoOptimizeSummary autoOptimizeData={autoOptimizeData} />}
      </Box>
    </>
  );
};

export default AutoOptimizeDetails;
