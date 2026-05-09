import React, { useCallback, useEffect, useRef, useState } from 'react';
import { podExceptionHeader } from '@lib/kubernetesData';
import apiKubernetes from '@api1/kubernetes';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import ReactLink from 'next/link';
import BoxLayout2 from '@components1/common/BoxLayout2';
import Datetime from '@components1/common/format/Datetime';
import InvestigateButton from '@components1/common/InvestigateButton';
import CreateTicketButton from '@components1/common/CreateTicketButton';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import type { TicketDataPojo } from 'src/utils/common';
import { action } from 'src/utils/actionStyles';
import CustomTabs from '@components1/common/CustomTabsForDrilldown';
import CustomTable from '@components1/common/tables/CustomTable2';
import { snackbar } from '@components1/common/snackbarService';

interface KubernetesTable2Props {
  id: string;
  headers: string[];
  data: any[];
  rowsPerPage: number;
  onPageChange?: () => void;
  totalRows: number;
  expandable: any;
  loading: boolean;
  clusterData: any[];
  startDate: Date;
  endDate: Date;
  allClusters: any[];
  tab: number;
  clusterOption: [
    {
      label: string;
      value: string;
    }
  ];
  allNameSpaces: any[];
}

interface FilterRequest {
  account_id?: string;
  subject_namespace?: string;
}

const KubernetesDashboardPodExceptions: React.FC<KubernetesTable2Props> = ({ id, allClusters, tab, clusterOption, allNameSpaces }) => {
  const filterOptions = [
    {
      name: 'Pod Exception',
      value: 0,
      tabOptions: [
        { id: 'oom-killed', text: 'OOM Killed', value: 0, aggregationKeys: ['pod_oom_killer_enricher'] },
        { id: 'image-pull-backoff', text: 'Image Pull Backoff', value: 1, aggregationKeys: ['image_pull_backoff_reporter'] },
        { id: 'high-restarts', text: 'High Restarts', value: 2, aggregationKeys: ['report_crash_loop', 'KubePodCrashLooping'] },
        { id: 'high-cpu-utilization', text: 'CPU Throttling', value: 3, aggregationKeys: ['CPUThrottlingHigh'] },
      ],
    },
  ];
  const currentDate = new Date();
  const startDate = new Date(currentDate);
  startDate.setDate(currentDate.getDate() - 1);
  const tableRef = useRef<HTMLDivElement>(null);
  const router = useRouter();

  const [podExceptionData, setPodExceptionData] = useState([]);
  const [aggregationKeys, setAggregationKeys] = useState(['pod_oom_killer_enricher']);
  const [aggregationKey, setAggregationKey] = useState(0);
  const [filterObj, setFilterObj] = useState<FilterRequest>({});
  const [selectedDates, setSelectedDates] = useState([
    {
      startDate: startDate,
      endDate: currentDate,
      key: 'selection',
    },
  ]);
  const [loading, setLoading] = useState(false);
  const [ticketData, setTicketData] = useState<TicketDataPojo>({
    id: '',
    title: '',
    priority: '',
    aggregation_key: '',
    subject_type: '',
    subject_name: '',
    subject_namespace: '',
    account_id: '',
  });
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [isElementVisible, setIsElementVisible] = useState(false);
  const [shouldFetch, setShouldFetch] = useState(true);
  const [namespaces, setNamespaces] = useState<any[]>([]);
  const tableId = `${id}-table`;

  useEffect(() => {
    setNamespaces([...new Set(allNameSpaces?.map((b) => b.namespace_name) || [])]);
  }, [allNameSpaces]);

  useEffect(() => {
    const observerCallback = (entries: any) => {
      const entry = entries[0];
      if (entry.isIntersecting) {
        setIsElementVisible(entry.isIntersecting);
      }
    };
    const observerOptions = {
      root: null,
      rootMargin: '0px',
      threshold: 0.5,
    };
    const observer = new IntersectionObserver(observerCallback, observerOptions);
    if (tableRef.current) {
      observer.observe(tableRef.current);
    }
    return () => {
      if (tableRef.current) {
        observer.unobserve(tableRef.current);
      }
    };
  }, []);

  const getKubernetesPodsExceptionData = useCallback(
    (filters: any) => {
      if (!shouldFetch) {
        return;
      }
      const limit = 5;
      const start_date = selectedDates[0].startDate;
      const end_date = selectedDates[0].endDate;
      const aggregation_key = aggregationKeys;
      setLoading(true);
      apiKubernetes
        .getK8sEvents(limit, 0, {
          start_date,
          end_date,
          aggregation_key,
          ...filters,
        })
        .then((response: any) => {
          setLoading(false);
          const clusterIdNameMap: { [accountId: string]: string } = {};
          allClusters.forEach((e) => {
            clusterIdNameMap[e.account_id] = e.account_name;
          });
          response?.data?.events.forEach((e: any) => {
            e.cluster = clusterIdNameMap[e.account_id];
          });

          const allIssuesData = response?.data?.events.map((e: any) => {
            const data = [];
            data.push({
              component: (
                <ClusterNameWithRegion
                  name={e?.subject_name}
                  nameOnClick={(event: any) => {
                    event.stopPropagation();
                    handlePodClick(e?.resource_id, e?.account_id);
                  }}
                  additionalContent={makeAccountClicklable(e?.account_id, e?.cluster)}
                  hideIcon={true}
                  cursorPointer
                  font={undefined}
                  region={undefined}
                  namespace={undefined}
                  namespaceFont={undefined}
                />
              ),
              drilldownQuery: { workloadName: e?.workload_name, namespaceName: e?.namespace_name },
            });
            data.push({ component: <CustomLabels margin='auto' text={e?.status} /> });
            data.push({ text: e?.subject_type });
            data.push({
              component: (
                <ClusterNameWithRegion
                  name={e?.title}
                  nameOnClick={undefined}
                  additionalContent={undefined}
                  hideIcon={true}
                  cursorPointer={false}
                  font={undefined}
                  region={undefined}
                  namespace={undefined}
                  maxWidth='150px'
                  namespaceFont={undefined}
                />
              ),
            });
            data.push({ text: e?.subject_namespace });
            data.push({ text: e?.restart_count || '-' });
            data.push({ component: <Datetime baseDate={new Date()} value={e?.starts_at} /> });
            data.push({ component: <SeverityIcon severityType={e?.priority} /> });
            data.push({
              component: (
                <Box
                  display={'flex'}
                  flexDirection={'row'}
                  alignItems={'center'}
                  gap={'6px'}
                  position={'sticky'}
                  right={'0px'}
                  justifyContent={'flex-end'}
                >
                  <InvestigateButton url={`/investigate?id=${e?.id}&accountId=${e?.account_id}`} />
                  <CreateTicketButton
                    sx={{ ...action.primary }}
                    onClick={(event: React.MouseEvent) => {
                      event.stopPropagation();
                      openTicketModal(e);
                    }}
                  />
                </Box>
              ),
            });
            return data;
          });
          setPodExceptionData(allIssuesData);
        })
        .finally(() => {
          setLoading(false);
          setShouldFetch(false);
        });
    },
    [shouldFetch]
  );

  useEffect(() => {
    if (isElementVisible) {
      getKubernetesPodsExceptionData(filterObj);
    }
  }, [isElementVisible, getKubernetesPodsExceptionData]);

  useEffect(() => {
    setShouldFetch(true);
  }, [tab, aggregationKeys, filterObj, selectedDates]);

  const handlePodClick = (cloud_resource_id: string, account_id: string) => {
    router.push(`/kubernetes/podDetails/${cloud_resource_id}?PodDetails=${cloud_resource_id}&accountId=${account_id}#pod-details`);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
    setTicketData({
      id: '',
      title: '',
      priority: '',
      aggregation_key: '',
      subject_type: '',
      subject_name: '',
      subject_namespace: '',
      account_id: '',
    });
  };

  const handleChangeAggregationKey = (e: any, value: number) => {
    setAggregationKey(value);
    if (value === 1) {
      setAggregationKeys(filterOptions[0].tabOptions.filter((e) => e.value === 1)[0].aggregationKeys);
    } else if (value === 2) {
      setAggregationKeys(filterOptions[0].tabOptions.filter((e) => e.value === 2)[0].aggregationKeys);
    } else if (value === 0) {
      setAggregationKeys(filterOptions[0].tabOptions.filter((e) => e.value === 0)[0].aggregationKeys);
    } else if (value === 3) {
      setAggregationKeys(filterOptions[0].tabOptions.filter((e) => e.value === 3)[0].aggregationKeys);
    }
  };

  const makeAccountClicklable = (account_id: string, account_name: string) => {
    return (
      <Typography style={{ fontSize: 12 }}>
        Cluster:{' '}
        <ReactLink
          href={'/kubernetes/details/' + account_id + '#summary'}
          onClick={(event) => {
            event.stopPropagation();
          }}
        >
          {account_name}
        </ReactLink>
      </Typography>
    );
  };

  const filterByCluster = (e: any) => {
    let accountId = '';
    if (e) {
      accountId = e.target.value;
    }
    setFilterObj((prevFilterObj) => ({
      ...prevFilterObj,
      account_id: accountId,
      subject_namespace: '',
    }));
    const namespaces = allNameSpaces.filter((g) => g.account_id == accountId).map((g) => g.namespace_name);
    setNamespaces(namespaces);
  };

  const onNamespaceFilterChange = (e: any) => {
    let namespace = '';
    if (e) {
      namespace = e.target.value;
    }
    setFilterObj((prevFilterObj) => ({
      ...prevFilterObj,
      subject_namespace: namespace,
    }));
  };

  const openTicketModal = (row: any) => {
    setTicketData({
      ...row,
    });
    setIsTicketCreateFormOpen(true);
  };

  const getTicketDescription = (data: TicketDataPojo) => {
    let description = '';
    description += '**Title**: ' + data.title + '\n';
    description += '**Priority**: ' + data.priority + '\n';
    description += '**Aggregation Key**: ' + data.aggregation_key + '\n';
    description += '**Subject Type**: ' + data.subject_type + '\n';
    description += '**Subject Name**: ' + data.subject_name + '\n';
    description += '**Subject Namespace**: ' + data.subject_namespace + '\n';
    return description;
  };

  const handleDateRangeChange = (selectedRange: any) => {
    const startTime = new Date(selectedRange.startTime);
    const endTime = new Date(selectedRange.endTime);

    const updatedDates = [
      {
        startDate: startTime,
        endDate: endTime,
        key: 'selection',
      },
    ];

    setSelectedDates(updatedDates);
  };

  const handleTicketSuccess = () => {
    getKubernetesPodsExceptionData(filterObj);
  };

  const handleTicketFailure = (res: string) => {
    snackbar.error(`Failed! ${res}.`);
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
          subject: 'Investigate Event - ' + ticketData.title,
          description: getTicketDescription(ticketData),
          accountId: ticketData.account_id,
        }}
        ticketUrl={{
          url: `/investigate?id=${ticketData?.id}`,
        }}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />

      <BoxLayout2
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: startDate.getTime(),
            endTime: currentDate.getTime(),
            shortcutClickTime: 0,
          },
        }}
        heading={'Pod Exception'}
        id={id}
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: clusterOption,
            onSelect: filterByCluster,
            minWidth: '150px',
            label: 'Cluster',
            value: filterObj.account_id,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: namespaces,
            onSelect: onNamespaceFilterChange,
            minWidth: '150px',
            label: 'Namespace',
            value: filterObj.subject_namespace,
          },
        ]}
        extraOptions={[]}
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: tableId,
              };
            },
          },
        }}
      >
        <CustomTabs options={filterOptions[0].tabOptions} value={aggregationKey} onChange={handleChangeAggregationKey} />
        <div ref={tableRef}>
          <CustomTable
            id={tableId}
            headers={podExceptionHeader}
            tableData={podExceptionData}
            rowsPerPage={5}
            totalRows={podExceptionData.length}
            loading={loading}
            showExpandable={false}
            onPageChange={undefined}
            onSortChange={undefined}
            tableHeadingCenter={['Status', 'Severity']}
            stickyColumnIndex='9'
          />
        </div>
      </BoxLayout2>
    </>
  );
};

export default KubernetesDashboardPodExceptions;
