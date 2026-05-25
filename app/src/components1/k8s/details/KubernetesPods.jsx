import React, { useState, useEffect } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Datetime from '@components1/common/format/Datetime';
import Memory from '@components1/common/format/Memory';
import Currency from '@components1/common/format/Currency';
import { useRouter } from 'next/router';
import { getLast30Days, getYesterday } from '@lib/datetime';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import LogFileIcon from '@assets//auto-pilot/log-events.svg';
import KubernetesPodConnection from '@components1/k8s/common/KubernetesPodConnection';
import NDialog from '@components1/common/modal/NDialog';
import { useData } from '@context/DataContext';
import { hasWriteAccess } from '@lib/auth';
import { Box, Typography } from '@mui/material';
import NumberComponent from '@components1/common/format/Number';
import PropTypes from 'prop-types';
import { action } from 'src/utils/actionStyles';
import CopyableText from '@components1/common/CopyableText';
import KubernetesPodDebugger from './KubernetesPodDebugger';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import CustomLink from '@components1/common/CustomLink';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { TerminalIcon, DeleteIconRed as DeleteIcon } from '@assets';
import apiKubernetes1 from '@api1/kubernetes1';

const POD_HEADERS = [
  {
    name: 'Pod Name',
    width: '25%',
  },
  { name: 'Namespace', width: '10%' },
  { name: 'Cost', width: '10%' },
  { name: 'CPU', width: '15%' },
  { name: 'Memory', width: '15%' },
  'Status/State',
  'Restarts',
  'Created At',
  'Error Count (24h)',
  '',
];

const KubernetesPodsTable = ({ accountId, defaultQuery = {}, enableFilters = true, disableDateFilterForPodsTable = false }) => {
  const router = useRouter();
  const { setPodLogRequest } = useData();
  const stateOptions = ['Active', 'Deleted'];
  const kubernetesPodsTable = 'kubernetesPodsTable';

  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace ?? null);
  const [workloadTypeFilter, setWorkloadTypeFilter] = useState([]);
  const [selectedWorkloadType, setSelectedWorkloadType] = useState(router.query.workloadType ?? null);
  const [selectedIsActive, setSelectedIsActive] = useState('Active');
  const [statusFilter, setStatusFilter] = useState([]);
  const [selectedStatus, setSelectedStatus] = useState(null);
  const [loading, setLoading] = useState(false);
  const [selectedPodName, setSelectedPodName] = useState({});
  const [deletePod, setDeletePod] = useState(false);
  const [podData, setPodData] = useState({});
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast30Days().getTime() + 60 * 1000,
    endDate: new Date().getTime(),
  });
  const [selectedName, setSelectedName] = useState('');
  const [inputName, setInputName] = useState('');
  const [open, setOpen] = React.useState(false);
  const [podFqdn, setPodFqdn] = useState([]);
  const [debugPodOpen, setDebugPodOpen] = useState(false);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [disableOptions, setDisableOptions] = useState(false);

  const handleClickOpen = () => {
    setOpen(true);
  };

  const handleClose = () => {
    setOpen(false);
  };

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const onNamespaceFilterChange = (e, _p) => {
    setSelectedNamespace(e?.target?.value);
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
    setCurrentPage(0);
  };

  const onWorkloadTypeFilterChange = (e, _p) => {
    setSelectedWorkloadType(e?.target?.value);
    setCurrentPage(0);
  };

  const onIsActiveFilterChange = (e, _p) => {
    setSelectedIsActive(e?.target?.value);
    setCurrentPage(0);
  };

  const onStatusFilterChange = (e, _p) => {
    setSelectedStatus(e?.target?.value);
    setCurrentPage(0);
  };

  const onNameFilterChange = (value) => {
    if (selectedName && value.trim() == '') {
      setSelectedName('');
      setCurrentPage(0);
    }
    setInputName(value);
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setCurrentPage(0);
  };

  const handlePodClick = (row) => {
    let route = `/kubernetes/podDetails/${row.id}?PodDetails=${row.id}&accountId=${router.query.KubernetesDetails}#pod-details`;
    router.push(route);
    setObjectForContext(row);
  };

  const handleLogClick = (row) => {
    let route = `/kubernetes/podDetails/${row.id}?PodDetails=${row.id}&accountId=${router.query.KubernetesDetails}#logs`;
    router.push(route);
  };

  const handleClickDebugPod = () => {
    setDebugPodOpen(true);
  };

  const handleCloseShellPopUp = () => {
    setDebugPodOpen(false);
  };

  const setObjectForContext = (data) => {
    setPodLogRequest(data.account_id, {
      subject_name: data.pod_name,
      subject_namespace: data.namespace_name,
    });
  };

  const onMenuClick = (menuItem, data) => {
    if (hasWriteAccess(data?.cloud_account_id)) {
      if (menuItem.id === 0) {
        handleClickOpen();
        k8sApi.getPodDetails(data.id).then((res) => {
          setPodData(res.data.cloud_resourses[0]);
        });
      } else if (menuItem.id === 1) {
        setSelectedPodName(data);
        setDeletePod(true);
      } else if (menuItem.id === 2) {
        setObjectForContext(data);
        handleLogClick(data);
      } else if (menuItem.id === 3) {
        setSelectedPodName(data);
        handleClickDebugPod();
      }
    } else {
      snackbar.error(`User is not allowed to perform ${menuItem.label} operation`);
    }
  };

  const handleCloseDeletePopUp = () => {
    setSelectedPodName({});
    setDeletePod(false);
  };

  const onEnterPress = () => {
    setSelectedName(inputName);
    setCurrentPage(0);
  };

  const handleSubmit = () => {
    k8sApi
      .relayForwardRequest({
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'delete_pod',
          action_params: {
            name: selectedPodName.name,
            namespace: selectedPodName.namespace,
            previous: 'false',
          },
          origin: 'Nudgebee UI',
        },
      })
      .then((res) => {
        handleCloseDeletePopUp();
        const errorMessage = parseHttpResponseBodyMessage(res);
        if (errorMessage) {
          snackbar.error(`Failed to delete pod: ${errorMessage}`);
        } else {
          snackbar.success(`Pod ${selectedPodName.name} deleted successfully`);
          setTimeout(() => listPod(), 3000);
        }
      })
      .catch((err) => {
        handleCloseDeletePopUp();
        snackbar.error(`Failed to delete pod: ${parseHttpResponseBodyMessage(err) || err?.message || 'Unknown error'}`);
      });
  };

  const listPod = () => {
    if (!accountId) {
      return;
    }
    let isActiveValue = null;
    if (selectedIsActive === 'Deleted') {
      isActiveValue = false;
    } else if (selectedIsActive === 'Active') {
      isActiveValue = true;
    }

    let query = {
      accountId: accountId,
      namespaceName: selectedNamespace,
      workloadType: selectedWorkloadType,
      status: selectedStatus,
      isActive: isActiveValue,
      podName: selectedName,
      ...(!disableDateFilterForPodsTable && {
        startDate: selectedDateRange.startDate,
        endDate: selectedDateRange.endDate,
      }),
    };

    if (defaultQuery) {
      query = { ...query, ...defaultQuery };
    }
    setData([]);
    setTotalCount(0);
    setLoading(true);
    setDisableOptions(true);
    k8sApi
      .getK8sPods(recordsPerPage, currentPage * recordsPerPage, query)
      .then((res) => {
        let podFqdn = [];
        let data = res.data.k8s_pods?.map((item) => {
          podFqdn.push(item.namespace + '.' + item.name);
          let restartCount = 0;
          if (item.restart_count) {
            for (const value of Object.values(item.restart_count)) {
              restartCount += value;
            }
          }

          let MENU_ITEMS = [
            {
              icon: LogFileIcon,
              label: 'Logs',
              id: 2,
            },
          ];

          if (hasWriteAccess(accountId)) {
            MENU_ITEMS = [
              {
                icon: LogFileIcon,
                label: 'Logs',
                id: 2,
              },
              {
                icon: DeleteIcon,
                label: 'Delete Pod',
                id: 1,
              },
              {
                icon: TerminalIcon,
                label: 'Debug Pod',
                id: 3,
              },
            ];
          }
          return [
            {
              component: (
                <Box display='flex' alignItems='center'>
                  <CopyableText copyableText={item.name} />
                  <ClusterNameWithRegion
                    cursorPointer={true}
                    name={item.name}
                    namespace={`
      ${item.node_name ?? '-'}
      | ${item.workload_type ?? '-'}
    `}
                    onClick={() => handlePodClick(item)}
                    isTargetURL={true}
                    hideIcon={true}
                    nameMaxLength={70}
                    namespaceFont='12px'
                  />
                </Box>
              ),
              drilldownQuery: {
                workload_name: item.workload_name,
                subject_namespace: item.namespace,
                subject_name: item.name,
                pod_name: item.name,
                namespace_name: item.namespace,
                type: 'pod',
                account_id: accountId,
              },
              data: item.name,
            },
            { component: <Text value={item.namespace} showAutoEllipsis /> },
            { text: '-' },
            { text: '-' },
            { text: '-' },
            { component: <Text value={item.status + '/' + (item.is_active ? 'Ready' : 'Deleted')} showAutoEllipsis /> },
            { component: <Text value={restartCount} /> },
            { component: <Datetime value={item.timestamp} /> },
            { component: <Text value={'-'} /> },

            {
              component: hasWriteAccess(accountId) ? (
                <Box display={'flex'} justifyContent={'flex-end'} alignItems={'center'}>
                  <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
                </Box>
              ) : (
                <></>
              ),
            },
          ];
        });
        let totalCount = res.data.k8s_pods_aggregate?.aggregate?.count;
        setData(data);
        setTotalCount(totalCount);
        setLoading(false);
        setPodFqdn(podFqdn);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listPod();
  }, [
    accountId,
    currentPage,
    recordsPerPage,
    selectedNamespace,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    selectedWorkloadType,
    selectedStatus,
    selectedIsActive,
    selectedName,
  ]);

  const processItemData = (itemData, item) => {
    itemData[2] = { component: <Currency value={item.cost} precison={2} />, data: item.cost };
    itemData[3] = { component: createCpuComponent(item) };
    itemData[4] = { component: createMemoryComponent(item) };
  };

  const createCpuComponent = (item) => (
    <Typography
      sx={{
        '& .suffix': {
          color: colors.text.lastSync,
          fontSize: '12px',
        },
        '& span': {
          color: colors.text.lastSync,
          fontSize: '12px',
        },
      }}
    >
      <NumberComponent value={item.avg_cpu_used} suffix={'vCPU'} />
      <span style={{ paddingLeft: '5px' }}>
        {item.avg_cpu_request && item.avg_cpu_used ? `(${((item.avg_cpu_used / item.avg_cpu_request) * 100).toFixed(1)}%)` : ''}
      </span>
      <br />
      <span>
        req:
        <NumberComponent
          value={item.avg_cpu_request || null}
          suffix={'vCPU'}
          sx={{
            color: colors.text.lastSync,
            fontSize: '12px',
          }}
        />
      </span>
    </Typography>
  );

  const createMemoryComponent = (item) => (
    <Typography
      sx={{
        '& .sufix': {
          color: colors.text.lastSync,
          fontSize: '12px',
        },
        '& span': {
          color: colors.text.lastSync,
          fontSize: '12px',
        },
      }}
    >
      <Memory value={item.avg_memory_used || null} />
      <span style={{ paddingLeft: '5px' }}>
        {item.avg_memory_request && item.avg_memory_used ? `(${((item.avg_memory_used / item.avg_memory_request) * 100).toFixed(1)}%)` : ''}
      </span>
      <br />
      <span>
        req:
        <Memory
          value={item.avg_memory_request || null}
          sx={{
            color: colors.text.lastSync,
            fontSize: '12px',
          }}
        />
      </span>
    </Typography>
  );

  useEffect(() => {
    if (!accountId || !data || data.length === 0) {
      setDisableOptions(false);
      return;
    }
    k8sApi
      .getK8sMetrices({
        accountId: accountId,
        podFqdn: podFqdn,
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
      })
      .then((res) => {
        for (const itemData of data) {
          const item = res.data?.k8s_pod_groupings?.find(
            (item) => item.namespace_name === itemData[0].drilldownQuery.namespace_name && item.pod_name === itemData[0].drilldownQuery.pod_name
          );
          if (item) {
            processItemData(itemData, item);
          }
        }
        setData([...data]);
      });
  }, [accountId, podFqdn, selectedDateRange.startDate, selectedDateRange.endDate]);

  useEffect(() => {
    if (!accountId) {
      setDisableOptions(false);
      return;
    }
    if (data == undefined || data.length == 0) {
      setDisableOptions(false);
      return;
    }
    getErrorCounts(podFqdn);
  }, [accountId, podFqdn, selectedDateRange.startDate, selectedDateRange.endDate]);

  const extractNamespaceAndApplication = (value, type) => {
    if (!value) {
      return value;
    }
    const valueArray = value.split('/').filter((e) => e != '');
    if (type === 'namespace') {
      return valueArray[1];
    } else if (type === 'application') {
      return valueArray[2];
    }
  };

  function getErrorCounts(podFqdn) {
    if (!podFqdn && podFqdn.length === 0) {
      return;
    }
    const podNames = podFqdn.map((element) => '/k8s/' + element.replace(/\./g, '/') + '/.*').join('|');
    const requestBody = {
      accountId: accountId,
      metrics: ['container_error_log_count_with_pod'],
      startDate: getYesterday().getTime(),
      endDate: new Date().getTime(),
      workloadName: podNames,
      kind: 'workload',
    };
    apiKubernetes1
      .utilisationApi(requestBody)
      .then((res) => {
        if (res?.length > 0) {
          const series_list_result = res?.[0]?.payload || [];
          const startTime = getYesterday().getTime();
          const endTime = new Date().getTime();
          if (series_list_result && series_list_result.length > 0) {
            for (const itemData of data) {
              const matchingPodLogData = series_list_result?.find(
                (item) =>
                  extractNamespaceAndApplication(item.metric.container_id, 'namespace') === itemData[0].drilldownQuery.namespace_name &&
                  extractNamespaceAndApplication(item.metric.container_id, 'application') === itemData[0].drilldownQuery.pod_name
              );

              if (matchingPodLogData) {
                const sum = matchingPodLogData.values?.reduce(function (accumulator, currentValue) {
                  return accumulator + parseInt(currentValue, 10);
                }, 0);
                itemData[8] = {
                  component:
                    sum > 0 ? (
                      <CustomLink
                        href={`/kubernetes/details/${accountId}?KubernetesDetails=${accountId}&namespace=${itemData[0].drilldownQuery.namespace_name}&workloadName=${itemData[0].drilldownQuery.workload_name}&startTime=${startTime}&endTime=${endTime}#monitoring/groups`}
                        onClick={(event) => event.stopPropagation()}
                      >
                        {sum}
                      </CustomLink>
                    ) : (
                      0
                    ),
                };
              }
            }
            setData([...data]);
          }
        }
      })
      .finally(() => {
        setDisableOptions(false);
      });
  }

  useEffect(() => {
    if (!accountId) {
      return;
    }
    if (!enableFilters) {
      return;
    }
    k8sApi.getK8sNamespaceNames(accountId).then((res) => {
      let namespaces = res.data.namespaces;
      setNamespaceFilter(namespaces);
    });

    k8sApi.listK8sPodWorkloadType({ accountId }).then((res) => {
      let data = res?.data?.k8s_pods;
      setWorkloadTypeFilter(data?.map((d) => d.workload_type));
    });

    k8sApi.listK8sPodStatusType({ accountId }).then((res) => {
      let data = res?.data?.k8s_pods;
      setStatusFilter(data?.map((d) => d.status));
    });
  }, [accountId, enableFilters]);

  const handleClearFilters = () => {
    setSelectedName('');
    setInputName('');
    setCurrentPage(0);
  };

  return (
    <>
      {open && Object.keys(podData).length > 0 ? <KubernetesPodConnection open={open} handleClose={handleClose} podData={podData} /> : null}
      <NDialog
        buttonText='Submit'
        handleClose={handleCloseDeletePopUp}
        dialogTitle={'Delete the Pod ' + selectedPodName.name}
        handleSubmit={handleSubmit}
        open={deletePod}
      />
      {debugPodOpen ? (
        <KubernetesPodDebugger
          accountId={accountId}
          debugPodOpen={debugPodOpen}
          selectedPodName={selectedPodName}
          closeDebugPod={handleCloseShellPopUp}
        />
      ) : null}
      <ListingLayout id='all-pods'>
        <ListingLayout.Toolbar
          actions={
            <>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                }}
                onChange={({ selection }) => handleDateRangeChange(selection)}
              />
              <DownloadButton onClick={() => ({ tableId: kubernetesPodsTable })} />
            </>
          }
        >
          {enableFilters && (
            <>
              <FilterDropdown
                label='Namespace'
                options={namespaceFilter.map((o) => ({ value: o, label: o }))}
                value={selectedNamespace}
                onSelect={onNamespaceFilterChange}
                disabled={disableOptions}
              />
              <FilterDropdown
                label='Workload Type'
                options={workloadTypeFilter.map((o) => ({ value: o, label: o }))}
                value={selectedWorkloadType}
                onSelect={onWorkloadTypeFilterChange}
                disabled={disableOptions}
              />
              <FilterDropdown
                label='State'
                options={stateOptions.map((o) => ({ value: o, label: o }))}
                value={selectedIsActive}
                onSelect={onIsActiveFilterChange}
                disabled={disableOptions}
              />
              <FilterDropdown
                label='Status'
                options={statusFilter.map((o) => ({ value: o, label: o }))}
                value={selectedStatus}
                onSelect={onStatusFilterChange}
                disabled={disableOptions}
              />
              <CustomSearch
                label='Pod Name'
                value={inputName}
                onChange={onNameFilterChange}
                onEnterPress={onEnterPress}
                onClear={handleClearFilters}
                disabled={disableOptions}
              />
            </>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <KubernetesTable2
            id={kubernetesPodsTable}
            headers={POD_HEADERS}
            data={data}
            rowsPerPage={recordsPerPage}
            onPageChange={onPageChange}
            totalRows={totalCount}
            loading={loading}
            pageNumber={currentPage + 1}
            disableDateFilterForPodsTable={disableDateFilterForPodsTable}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

KubernetesPodsTable.propTypes = {
  accountId: PropTypes.string,
  defaultQuery: PropTypes.object,
  enableFilters: PropTypes.bool,
  disableDateFilterForPodsTable: PropTypes.bool,
};

export default KubernetesPodsTable;
