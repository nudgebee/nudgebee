import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiAutoPilot from '@api1/autoPilot';
import { useRouter } from 'next/router';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import KubernetesTable2, {
  KubernetesUtilizationCharts,
  KubernetesCostCharts,
  KubernetesPVCUtilization,
} from '@components1/k8s/common/KubernetesTable2';
import CustomTable2 from '@common-new/tables/CustomTable2';

import KubernetesDeploymentHistory from '@components1/k8s/common/KubernetesDeploymentHistory';

import LinkIcon from '@mui/icons-material/Link';
import { Label } from '@components1/ds/Label';
const LISTING_HEADER = [
  { name: 'Scheduled Time', width: '10%' },
  { name: 'Status', width: '8%' },
  { name: 'Resource', width: '20%' },
  { name: 'Message', width: '35%' },
  { name: 'PR/Ticket', width: '10%' },
  { name: 'Recommendation', width: '12%' },
];
const DRILL_DOWN_LISTING_HEADER = ['Name', 'Old Value', 'New Value'];

import DateTime from '@common-new/format/Datetime';
import { titleCase } from '@lib/formatter';
import PropTypes from 'prop-types';
import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import Text from '@common-new/format/Text';
import { Link } from '@components1/ds/Link';
import CustomTabs from '@common-new/CustomTabs';
import { hasWriteAccess } from '@lib/auth';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import AutoPilotApprovalStatusListingModal from '@components1/autopilot/AutoPilotApprovalStatusListingModal';

import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { Modal } from '@components1/ds/Modal';
import apiAccount from '@api1/account';
import apiRecommendations from '@api1/recommendation';
import { ds } from 'src/utils/colors';

const PRTicketLink = ({ prResolution, ticketLink }) => {
  if (prResolution?.type_reference_id) {
    return (
      <Button
        tone='secondary'
        size='sm'
        icon={<LinkIcon />}
        href={prResolution.type_reference_id}
        target='_blank'
        onClick={(e) => e.stopPropagation()}
      >
        PR {`${prResolution?.status ? `- ${prResolution?.status}` : '- Open'}`}
      </Button>
    );
  }
  if (ticketLink) {
    return (
      <Button tone='secondary' size='sm' icon={<LinkIcon />} href={ticketLink} target='_blank' onClick={(e) => e.stopPropagation()}>
        Ticket
      </Button>
    );
  }
  return <Typography sx={{ color: ds.gray[400], fontSize: ds.text.body }}>-</Typography>;
};

PRTicketLink.propTypes = {
  prResolution: PropTypes.shape({
    type_reference_id: PropTypes.string,
    status: PropTypes.string,
  }),
  ticketLink: PropTypes.string,
};

const AutoOptimizeTasks = ({ enableFilters = true, title, actions }) => {
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
            { text: <Label margin='auto' text={item?.status} /> },
            {
              component: (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                  {resourceFilter?.name ? (
                    <Link
                      href={`/kubernetes/details/${item?.auto_pilot?.cloud_account?.id}?namespace=${resourceFilter?.namespace ?? ''}&workloadName=${
                        resourceFilter?.name ?? ''
                      }#kubernetes/applications`}
                      openInNew
                    >
                      <Typography
                        component={'a'}
                        style={{
                          color: ds.gray[700],
                          fontSize: ds.text.body,
                          textDecoration: 'underline',
                          textDecorationColor: ds.gray[300],
                        }}
                      >
                        {resourceFilter?.name}
                      </Typography>
                    </Link>
                  ) : (
                    <Typography sx={{ color: ds.gray[700], fontSize: ds.text.body }}>-</Typography>
                  )}
                  {resourceFilter?.namespace && (
                    <Typography sx={{ color: ds.gray[400], fontSize: ds.text.small }}>ns: {resourceFilter.namespace}</Typography>
                  )}
                  {resourceFilter?.type && <Typography sx={{ color: ds.gray[400], fontSize: ds.text.small }}>type: {resourceFilter.type}</Typography>}
                </Box>
              ),
            },
            { component: <Text value={item?.reason} format='markdown' showAutoEllipsis lineClamp={3} /> },
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
                  tone='secondary'
                  size='sm'
                  icon={<LinkIcon />}
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
    const url =
      item?.auto_pilot?.category == 'horizontal_rightsize'
        ? `/kubernetes/details/${id}#optimize/replica-rightsizing`
        : `/kubernetes/details/${id}?recommendation_id=${item?.recommendation_id}#optimize`;
    window.open(url, '_blank', 'noopener,noreferrer');
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
    <ListingLayout id='auto-pilot' sx={{ mt: 3 }}>
      <ListingLayout.Toolbar title={title} actions={actions}>
        {enableFilters && <FilterDropdown label='Status' options={statusFilter} value={selectedStatus} onSelect={onStatusFilterChange} />}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
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
                        <Box sx={{ mt: ds.space[3], px: ds.space[2] }}>
                          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium, mb: ds.space[1] }}>
                            Command
                          </Typography>
                          <Typography
                            component='pre'
                            sx={{
                              fontSize: ds.text.body,
                              color: ds.gray[700],
                              backgroundColor: ds.background[200],
                              border: `1px solid ${ds.gray[200]}`,
                              borderRadius: ds.radius.sm,
                              p: ds.space[3],
                              fontFamily: ds.font.mono,
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
      </ListingLayout.Body>
    </ListingLayout>
  );
};

AutoOptimizeTasks.propTypes = {
  enableFilters: PropTypes.bool,
  title: PropTypes.node,
  actions: PropTypes.node,
};

const AutoOptimizeSummary = ({ autoOptimizeData = {}, title, actions }) => {
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
    <ListingLayout sx={{ mt: 3 }}>
      <ListingLayout.Toolbar title={title} actions={actions}>
        <FilterDropdown
          label='Namespace'
          options={namespaceFilter}
          value={selectedNamespace}
          onSelect={(_e, v) => {
            setSelectedNamespace(v);
            setSelectedContainer('');
          }}
        />
        <FilterDropdown
          label='Workload'
          options={workloadFilter?.map((e) => e.name) ?? []}
          value={selectedWorkload}
          onSelect={(_e, v) => {
            setSelectedWorkload(v);
            let workload = workloadFilter?.find((e) => e.name === v);
            if (workload) {
              setSelectedWorkloadType(workload.type);
            }
            setSelectedContainer('');
          }}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', gap: ds.space[3], padding: `${ds.space[2]} 0` }}>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='sm'
              label='Namespace'
              value={
                <Link
                  href={`/kubernetes/details/${autoOptimizeData.account_id}?namespace=${selectedNamespace ?? ''}#kubernetes/namespaces`}
                  openInNew
                >
                  {selectedNamespace || '-'}
                </Link>
              }
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='sm'
              label={autoOptimizeData.category === 'pv_rightsize' ? 'Persistent Volume Claim' : 'Workload'}
              value={
                <Link
                  href={
                    autoOptimizeData.category === 'pv_rightsize'
                      ? `/kubernetes/details/${autoOptimizeData?.account_id}?namespace=${selectedNamespace}&pvcName=${selectedWorkload}#kubernetes/pvc`
                      : `/kubernetes/details/${autoOptimizeData?.account_id}?namespace=${selectedNamespace ?? ''}&workloadName=${
                          selectedWorkload ?? ''
                        }#kubernetes/applications`
                  }
                  openInNew
                >
                  {selectedWorkload || '-'}
                </Link>
              }
            />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat size='sm' label='Kind' value={selectedWorkloadType || '-'} />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat size='sm' label='Category' value={autoOptimizeData?.category || '-'} />
          </WidgetCard>
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat size='sm' label='Last Executed At' value={<DateTime value={autoOptimizeData?.last_executed_time} />} />
          </WidgetCard>
        </Box>
        <Box sx={{ mb: ds.space[4] }} />
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
        <Box sx={{ mb: ds.space[4] }} />
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
        <Box sx={{ mb: ds.space[4] }} />
        <KubernetesDeploymentHistory
          accountId={autoOptimizeData?.account_id}
          subjectName={selectedWorkload}
          subjectNamespace={selectedNamespace}
          subjectType={selectedWorkloadType}
          heading='Deployment History'
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

AutoOptimizeSummary.propTypes = {
  autoOptimizeData: PropTypes.object,
  title: PropTypes.node,
  actions: PropTypes.node,
};

const AutoOptimizeDetails = () => {
  const tabOptions = {
    tabOptions: [
      { id: 'tab-tasks', text: 'Tasks', value: 0, fragment: 'tasks' },
      { id: 'tab-details', text: 'Details', value: 1, fragment: 'details' },
    ],
  };
  const router = useRouter();

  const [autoOptimizeTab, setAutoOptimizeTab] = useState(0);

  // Sync tab from hash — runs on mount and on back/forward navigation
  useEffect(() => {
    const hash = router.asPath.split('#')[1] ?? '';
    const tab = tabOptions.tabOptions.find((t) => t.fragment === hash);
    if (tab) setAutoOptimizeTab(tab.value);
    else setAutoOptimizeTab(0);
  }, [router.asPath]);

  const [autoOptimizeData, setAutoOptimizeData] = useState({});

  const [disableAutoOptimize, setDisableAutoOptimize] = useState(false);
  const [approvalStatusModalOpen, setApprovalStatusModalOpen] = useState(false);
  const [loading, setLoading] = useState(false);

  const [openCreateAutoOptimize, setOpenCreateAutoOptimize] = useState(false);
  const [openCreateAutoOptimizeType, setOpenCreateAutoOptimizeType] = useState(null);
  const [modalLoading, setModalLoading] = useState(false);

  const [msTeamsData, setMsTeamsData] = useState([]);
  const [googleChannelList, setGoogleChannelList] = useState([]);

  const handleChangeRunbookTab = (value) => {
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

  useEffect(() => {
    if (!router?.query?.TaskDetails) {
      return;
    }
    apiAutoPilot.listAutoPilot(1, 0, { id: router?.query?.TaskDetails }).then((res) => {
      setAutoOptimizeData(res?.data?.auto_pilot_listing?.[0]);
    });
  }, [router?.query?.TaskDetails]);

  const pageTitle = 'Auto Optimize Task > ' + (autoOptimizeData?.name || '');
  const pageActions =
    autoOptimizeData && Object.keys(autoOptimizeData).length > 0 && hasWriteAccess(autoOptimizeData?.account_id || router.query?.accountId) ? (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
        {getMenuItems(autoOptimizeData).map((menuItem) => {
          if (menuItem.id === 1) {
            return (
              <Button key='edit' tone='primary' size='sm' onClick={handleEditClick} loading={modalLoading} disabled={modalLoading}>
                Edit
              </Button>
            );
          } else if (menuItem.id === 0) {
            return (
              <Button
                key='toggle'
                tone={autoOptimizeData.status?.toUpperCase() === 'ACTIVE' ? 'secondary' : 'primary'}
                size='sm'
                onClick={handleToggleStatusClick}
              >
                {menuItem.label}
              </Button>
            );
          } else if (menuItem.id === 2) {
            return (
              <Button key='check-status' tone='ghost' size='sm' onClick={handleCheckStatusClick}>
                Check Status
              </Button>
            );
          }
          return null;
        })}
      </Box>
    ) : null;

  return (
    <>
      <AutoPilotApprovalStatusListingModal
        id={autoOptimizeData?.id}
        name={autoOptimizeData?.name}
        open={approvalStatusModalOpen}
        handleClose={() => setApprovalStatusModalOpen(false)}
      />

      <Modal
        open={disableAutoOptimize}
        handleClose={handleCloseAutoOptimizePopUp}
        title={`${autoOptimizeData?.status == 'Active' ? 'Disable' : 'Enable'} Auto Optimize "${autoOptimizeData?.name}"`}
        confirmText='Confirm'
        onConfirm={handleSubmit}
        loader={loading}
        confirmDisabled={loading}
      >
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>
          {`Are you sure you want to ${autoOptimizeData?.status == 'Active' ? 'Disable' : 'Enable'} this "${autoOptimizeData?.name}" auto optimize?`}
        </Typography>
      </Modal>
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

      <Box sx={{ mt: ds.space[5] }}>
        <CustomTabs options={tabOptions} value={autoOptimizeTab} onChange={handleChangeRunbookTab} />
        {autoOptimizeTab == 0 && <AutoOptimizeTasks title={pageTitle} actions={pageActions} />}
        {autoOptimizeTab == 1 && <AutoOptimizeSummary autoOptimizeData={autoOptimizeData} title={pageTitle} actions={pageActions} />}
      </Box>
    </>
  );
};

export default AutoOptimizeDetails;
