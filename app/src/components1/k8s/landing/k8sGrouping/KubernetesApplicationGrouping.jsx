import { BoxLayout2, Text, ThreeDotsMenu } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import CustomTable from '@components1/common/tables/CustomTable2';
import { action } from 'src/utils/actionStyles';
import Datetime from '@components1/common/format/Datetime';
import { Link, Typography } from '@mui/material';
import { EditIcon } from '@assets';
import KubernetesInsertApplicationGroupingModal from './KubernetesInsertApplicationGroupingModal';
import apiAppGrouping from '@api1/application-groupings';
import apiUser from '@api1/user';
import { useRouter } from 'next/router';
import { snackbar } from '@components1/common/snackbarService';
import apiKubernetes from '@api1/kubernetes';
import { getSpecificTime } from '@lib/datetime';
import CustomTooltip from '@components1/common/CustomTooltip';
import { isValidSeverity } from 'src/utils/common';
import { hasWriteAccess } from '@lib/auth';
import apiKubernetes1 from '@api1/kubernetes1';

const KubernetesApplicationGrouping = () => {
  // Keep critical state variables separate for proper React updates
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [searchByName, setSearchByName] = useState('');
  const [groupingModalOpen, setGroupingModalOpen] = useState(false);
  const [isEdit, setIsEdit] = useState(false);
  const [groupId, setGroupId] = useState('');
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize() || 10);
  const [accountIdK8sWorkloadMap, setAccountIdK8sWorkloadMap] = useState({});
  const [eventData, setEventData] = useState({});
  const [errorRateData, setErrorRateData] = useState({});

  const router = useRouter();
  const k8sGroupigId = 'k8sGrouping';

  // Memoized menu items
  const MENU_ITEMS = useMemo(
    () => [
      {
        icon: EditIcon,
        label: 'Edit',
        id: 0,
      },
    ],
    []
  );

  // Event handlers with useCallback
  const onEnterPress = useCallback(() => {
    getGroupingTableData();
  }, [currentPage, rowsPerPage, searchByName]);

  const onPageChange = useCallback((page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  }, []);

  const closeGroupingModalOpen = useCallback(() => {
    setGroupingModalOpen(false);
    getGroupingTableData();
  }, []);

  const onMenuClick = useCallback((menuItem, dataItem) => {
    if (menuItem.id === 0) {
      setIsEdit(true);
      setGroupingModalOpen(true);
      setGroupId(dataItem.id);
    }
  }, []);

  const redirectToGrouping = useCallback(
    (id) => {
      router.push(`/grouping?groupId=${id}`);
    },
    [router]
  );

  // Data transformation helpers
  const buildAccountWorkloadsMap = useCallback((applicationGroup) => {
    const accountWorkloadsMap = {};

    applicationGroup.forEach((item) => {
      const mappings = item.application_group_mappings || [];

      mappings.forEach((mapping) => {
        const { cloud_account, k8s_workload } = mapping;
        const accountId = cloud_account?.id;
        const workload = k8s_workload;

        if (accountId && workload?.name && workload?.namespace) {
          const workloadKey = `${workload.namespace}:${workload.name}`;

          if (!accountWorkloadsMap[accountId]) {
            accountWorkloadsMap[accountId] = new Set();
          }

          accountWorkloadsMap[accountId].add(workloadKey);
        }
      });
    });

    return accountWorkloadsMap;
  }, []);

  const transformTableData = useCallback(
    (applicationGroup) => {
      return applicationGroup.map((item) => {
        const mappings = item.application_group_mappings || [];
        const primaryAccount = mappings[0]?.cloud_account || {};
        const k8sWorkloads = mappings.map((mapping) => mapping.k8s_workload).filter(Boolean);

        return [
          {
            component: (
              <>
                <Link color='#2563EB' underline='hover' onClick={() => redirectToGrouping(item.id)} sx={{ cursor: 'pointer' }}>
                  <Text value={item.name} sx={{ color: '#2563EB' }} />
                </Link>
                <Text value={`Account: ${primaryAccount.account_name}`} secondaryText />
              </>
            ),
            drilldownQuery: {
              appGroupName: item.name || '',
              accountId: primaryAccount.id || '',
              accountName: primaryAccount.account_name || '',
              k8sWorkloads,
            },
          },
          {
            text: (
              <CustomTooltip placement='top' title={createDataTooltip(k8sWorkloads, tooltipConfig1)}>
                <Typography>{mappings.length}</Typography>
              </CustomTooltip>
            ),
          },
          { component: <Datetime margin='auto' value={item.updated_at || item.created_at} /> },
          { text: item?.created_by_user?.display_name || '' },
          { text: '-' }, // Will be updated with event data
          { text: '-' }, // Will be updated with error rate data
          {
            component: <ThreeDotsMenu sx={{ ...action.primary }} onMenuClick={onMenuClick} data={item} menuItems={MENU_ITEMS} />,
          },
        ];
      });
    },
    [redirectToGrouping, onMenuClick, MENU_ITEMS]
  );

  // Main data fetching function
  const getGroupingTableData = useCallback(async () => {
    try {
      setLoading(true);
      setData([]);
      setTotalCount(0);

      const response = await apiAppGrouping.getApplicationGroupings({
        limit: rowsPerPage,
        offset: currentPage * rowsPerPage,
        search: searchByName,
      });

      setLoading(false);

      const totalCount = response?.application_group_aggregate?.aggregate?.count || 0;
      const applicationGroup = response?.application_group || [];

      setTotalCount(totalCount);

      if (applicationGroup.length > 0) {
        const accountWorkloadsMap = buildAccountWorkloadsMap(applicationGroup);
        const transformedData = transformTableData(applicationGroup);

        setData(transformedData);
        setAccountIdK8sWorkloadMap(accountWorkloadsMap);
      }
    } catch (error) {
      console.error('Error fetching grouping data:', error);
      setLoading(false);
    }
  }, [rowsPerPage, currentPage, searchByName, buildAccountWorkloadsMap, transformTableData]);

  // Fetch event data with better error handling
  const fetchEventData = useCallback(async (accountWorkloadsMap) => {
    if (!accountWorkloadsMap || Object.keys(accountWorkloadsMap).length === 0) {
      return {};
    }

    const eventDataPromises = Object.keys(accountWorkloadsMap).map(async (accountId) => {
      const workloads = Array.from(accountWorkloadsMap[accountId]);
      const uniqueNamespaces = [...new Set(workloads.map((w) => w.split(':')[0]))];
      const uniqueWorkloads = [...new Set(workloads.map((w) => w.split(':')[1]))];

      try {
        const response = await apiKubernetes.getK8sEventGroupings(
          100,
          0,
          {
            account_id: accountId,
            subject_namespace: uniqueNamespaces,
            subject_owner: uniqueWorkloads,
            start_date: new Date(getSpecificTime(60)),
            end_date: new Date(),
            status: 'FIRING',
          },
          ['tenant_id', 'account_id', 'subject_namespace', 'subject_owner'],
          ['account_id', 'subject_namespace', 'subject_owner', 'event_count', 'count_priority_high']
        );

        return { accountId, events: response?.data?.event_groupings || [] };
      } catch (error) {
        console.error(`Error fetching event data for account ${accountId}:`, error);
        return { accountId, events: [] };
      }
    });

    const results = await Promise.all(eventDataPromises);
    return results.reduce((acc, { accountId, events }) => {
      acc[accountId] = events;
      return acc;
    }, {});
  }, []);

  const fetchErrorRateData = useCallback(async (accountWorkloadsMap) => {
    if (!accountWorkloadsMap || Object.keys(accountWorkloadsMap).length === 0) {
      return {};
    }

    const errorRateDataPromises = Object.keys(accountWorkloadsMap).map(async (accountId) => {
      const workloads = Array.from(accountWorkloadsMap[accountId]);
      const uniqueNamespaces = [...new Set(workloads.map((w) => w.split(':')[0]))].join('|');
      const uniqueWorkloads = [...new Set(workloads.map((w) => w.split(':')[1]))].join('|');

      const requestBody = {
        accountId: accountId,
        metrics: ['workload_http_error_rate'],
        startDate: getSpecificTime(60),
        endDate: new Date().getTime(),
        namespaceName: uniqueNamespaces,
        workloadName: uniqueWorkloads,
        instant: true,
      };
      try {
        const res = await apiKubernetes1.utilisationApi(requestBody);
        if (res?.[0]?.payload?.length) {
          const result = res[0].payload;
          return {
            accountId,
            errorRate:
              result?.map((item) => ({
                subject_namespace: item?.metric?.destination_workload_namespace || '-',
                subject_owner: item?.metric?.destination_workload_name || '-',
                error_rate: Number((parseFloat(item?.values?.[0] * 100) || 0).toFixed(4)),
              })) || [],
          };
        }
        return { accountId, errorRate: [] };
      } catch (err) {
        console.error(`Error fetching event data for account ${accountId}:`, err);
        return { accountId, errorRate: [] };
      }
    });

    const results = await Promise.all(errorRateDataPromises);
    return results.reduce((acc, { accountId, errorRate }) => {
      acc[accountId] = errorRate;
      return acc;
    }, {});
  }, []);

  // Dynamic tooltip configuration
  const tooltipConfig = useMemo(
    () => ({
      title: 'Last 1 Hour (Firing Events)',
      columns: [
        { key: 'subject_namespace', label: 'Namespace' },
        { key: 'subject_owner', label: 'Workload' },
        { key: 'count_priority_high', label: 'High Priority' },
        { key: 'event_count', label: 'Event Count' },
      ],
      styles: {
        container: { minWidth: '400px' },
        title: { fontWeight: 'bold', marginBottom: 8 },
        header: {
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr',
          fontWeight: 'bold',
          borderBottom: '1px solid #ccc',
          paddingBottom: 4,
          marginBottom: 4,
        },
        row: {
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr',
          padding: '4px 0',
          borderBottom: '1px solid #eee',
        },
      },
    }),
    []
  );
  const tooltipConfig1 = useMemo(
    () => ({
      columns: [
        { key: 'namespace', label: 'Namespace' },
        { key: 'name', label: 'Workload' },
      ],
      styles: {
        container: { minWidth: '400px' },
        title: { fontWeight: 'bold', marginBottom: 8 },
        header: {
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr',
          fontWeight: 'bold',
          borderBottom: '1px solid #ccc',
          paddingBottom: 4,
          marginBottom: 4,
        },
        row: {
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr',
          padding: '4px 0',
          borderBottom: '1px solid #eee',
        },
      },
    }),
    []
  );
  const tooltipConfig2 = useMemo(
    () => ({
      title: 'Last 1 Hour',
      columns: [
        { key: 'subject_namespace', label: 'Namespace' },
        { key: 'subject_owner', label: 'Workload' },
        { key: 'error_rate', label: 'Error Rate' },
      ],
      styles: {
        container: { minWidth: '400px' },
        title: { fontWeight: 'bold', marginBottom: 8 },
        header: {
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr',
          fontWeight: 'bold',
          borderBottom: '1px solid #ccc',
          paddingBottom: 4,
          marginBottom: 4,
        },
        row: {
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr 1fr',
          padding: '4px 0',
          borderBottom: '1px solid #eee',
        },
      },
    }),
    []
  );

  // Generic tooltip table creator
  const createDataTooltip = useCallback((data, config) => {
    if (!data || data.length === 0) {
      return null;
    }

    const { title, columns, styles } = config;
    const gridColumns = `repeat(${columns.length}, 1fr)`;

    return (
      <div style={styles.container}>
        {title && <div style={styles.title}>{title}</div>}
        <div style={{ ...styles.header, gridTemplateColumns: gridColumns }}>
          {columns.map((col) => (
            <div key={col.key}>{col.label}</div>
          ))}
        </div>
        {data.map((item, index) => (
          <div key={`${item[columns[0].key]}-${item[columns[1].key]}-${index}`} style={{ ...styles.row, gridTemplateColumns: gridColumns }}>
            {columns.map((col) => (
              <div key={col.key}>{item[col.key] || '-'}</div>
            ))}
          </div>
        ))}
      </div>
    );
  }, []);

  // Effects
  useEffect(() => {
    getGroupingTableData();
  }, [currentPage, rowsPerPage]);

  useEffect(() => {
    if (Object.keys(accountIdK8sWorkloadMap).length > 0) {
      fetchEventData(accountIdK8sWorkloadMap).then(setEventData);
      fetchErrorRateData(accountIdK8sWorkloadMap).then(setErrorRateData);
    }
  }, [accountIdK8sWorkloadMap, fetchEventData]);

  // Update table data with event information
  useEffect(() => {
    if (Object.keys(eventData).length > 0 && data.length > 0) {
      const updatedData = data.map((dataItem) => {
        const { accountId, k8sWorkloads } = dataItem[0].drilldownQuery;

        if (!accountId || !eventData[accountId] || !k8sWorkloads?.length) {
          return [...dataItem.slice(0, 4), { text: '-' }, ...dataItem.slice(5)];
        }
        const workloadEvents = eventData[accountId].filter((event) =>
          k8sWorkloads.some((workload) => event.subject_owner === workload.name && event.subject_namespace === workload.namespace)
        );
        if (workloadEvents.length === 0) {
          return [...dataItem.slice(0, 4), { text: '-' }, ...dataItem.slice(5)];
        }
        const totalEventCount = workloadEvents.reduce((sum, event) => sum + event.event_count, 0);
        return [
          ...dataItem.slice(0, 4),
          {
            component: (
              <CustomTooltip placement='top' title={createDataTooltip(workloadEvents, tooltipConfig)}>
                <Typography>{totalEventCount}</Typography>
              </CustomTooltip>
            ),
          },
          ...dataItem.slice(5),
        ];
      });

      setData(updatedData);
    }
  }, [eventData]);

  useEffect(() => {
    if (Object.keys(errorRateData).length > 0 && data.length > 0) {
      const updatedData = data.map((dataItem) => {
        const { accountId, k8sWorkloads } = dataItem[0].drilldownQuery;

        if (!accountId || !errorRateData[accountId] || !k8sWorkloads?.length) {
          return [...dataItem.slice(0, 5), { text: '-' }, ...dataItem.slice(6)];
        }

        const workloadErrorRate = errorRateData[accountId].filter((er) =>
          k8sWorkloads.some((workload) => er.subject_owner === workload.name && er.subject_namespace === workload.namespace)
        );

        if (workloadErrorRate.length === 0) {
          return [...dataItem.slice(0, 5), { text: '-' }, ...dataItem.slice(6)];
        }
        const totalErrorRateCount = workloadErrorRate?.reduce((sum, er) => sum + er.error_rate, 0) || 0;

        return [
          ...dataItem.slice(0, 5),
          {
            component: (
              <CustomTooltip placement='top' title={createDataTooltip(workloadErrorRate, tooltipConfig2)}>
                <Typography>{totalErrorRateCount?.toFixed(4) || '-'}</Typography>
              </CustomTooltip>
            ),
          },
          ...dataItem.slice(6),
        ];
      });

      setData(updatedData);
    }
  }, [errorRateData]);

  // Memoized components and options
  const filterOptions = useMemo(
    () => [
      {
        type: 'search',
        enabled: true,
        onSelect: (e) => {
          setSearchByName(e.target.value);
          setCurrentPage(0);
        },
        minWidth: '150px',
        label: 'Search Grouping',
        onEnter: onEnterPress,
      },
    ],
    [onEnterPress]
  );

  const extraOptions = useMemo(
    () => [
      hasWriteAccess() ? (
        <CustomButton
          key='add-app-group'
          variant='blueButton'
          text='Create Application Group'
          onClick={() => {
            setGroupingModalOpen(true);
            setIsEdit(false);
          }}
        />
      ) : (
        <></>
      ),
    ],
    []
  );

  const tableHeaders = useMemo(
    () => [
      { name: 'Group name', width: '20%' },
      { name: 'Total Applications', width: '10%' },
      { name: 'Updated at', width: '10%' },
      { name: 'Created by', width: '20%' },
      { name: 'Event Count (1 Hour)', width: '20%' },
      { name: 'Error Rate (1 Hour)', width: '20%' },
      { name: '', width: '10%' },
    ],
    []
  );

  const sharingOptions = useMemo(
    () => ({
      sharing: { enabled: false, onClick: null },
      download: {
        enabled: true,
        onClick: () => ({ tableId: k8sGroupigId }),
      },
    }),
    []
  );

  return (
    <div>
      {groupingModalOpen && (
        <KubernetesInsertApplicationGroupingModal
          open={groupingModalOpen}
          handleClose={closeGroupingModalOpen}
          isUpdateGroup={isEdit}
          groupId={groupId}
          handleSnackBarData={(data) => {
            const severity = data.severity?.toLowerCase();
            if (severity && isValidSeverity(severity)) {
              snackbar[severity](data.message);
            }
          }}
        />
      )}

      <BoxLayout2 id='k8s-grouping' heading='' sharingOptions={sharingOptions} filterOptions={filterOptions} extraOptions={extraOptions}>
        <CustomTable
          id={k8sGroupigId}
          totalRows={totalCount}
          tableData={data}
          headers={tableHeaders}
          rowsPerPage={rowsPerPage}
          showExpandable={false}
          loading={loading}
          onPageChange={onPageChange}
          onSortChange={undefined}
          pageNumber={currentPage + 1}
          stickyColumnIndex='9'
        />
      </BoxLayout2>
    </div>
  );
};

export default KubernetesApplicationGrouping;
