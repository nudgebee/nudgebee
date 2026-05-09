import React, { useCallback, useEffect, useRef, useState } from 'react';
import { clusterIssuesHeader } from '@lib/kubernetesData';
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
import type { TicketDataPojo } from 'src/utils/common';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import CreateTicketButton from '@components1/common/CreateTicketButton';
import { action } from 'src/utils/actionStyles';
import CustomTabs from '@components1/common/CustomTabsForDrilldown';
import CustomTable from '@components1/common/tables/CustomTable2';
import { snackbar } from '@components1/common/snackbarService';

interface KubernetesTable2Props {
  id: string;
  allClusters: any[];
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

const KubernetesDashboardIssues: React.FC<KubernetesTable2Props> = ({ id, allClusters, clusterOption, allNameSpaces }) => {
  const currentDate = new Date();
  const startDate = new Date(currentDate);
  startDate.setDate(currentDate.getDate() - 1);
  const router = useRouter();
  const tableRef = useRef<HTMLDivElement>(null);

  const [filterOptions, setFilterOptions] = useState([
    {
      name: 'Issues',
      value: 1,
      tabOptions: [
        { id: 'pods', text: 'Pods', value: 0, count: '' },
        { id: 'workloads', text: 'Workloads', value: 1, count: '' },
        { id: 'clusters', text: 'Clusters', value: 2, count: '' },
      ],
    },
  ]);
  const [allIssuesData, setAllIssuesData] = useState([]);
  const [issueSubTab, setIssueSubTab] = useState(0);
  const [findingType, setFindingType] = useState('');
  const [issueSubType, setIssueSubType] = useState(['pod']);
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

  const getKubernetesIssuesData = useCallback(
    (filters: any) => {
      if (!shouldFetch) {
        return;
      }
      setLoading(true);
      setAllIssuesData([]);
      const limit = 5;
      const finding_type = findingType;
      const subject_type = issueSubType || ['pod'];
      const start_date = selectedDates[0].startDate;
      const end_date = selectedDates[0].endDate;
      apiKubernetes
        .getK8sEvents(limit, 0, {
          finding_type,
          subject_type,
          start_date,
          end_date,
          ...filters,
        })
        .then((response: any) => {
          const clusterIdNameMap: { [accountId: string]: string } = {};
          allClusters.forEach((e) => {
            clusterIdNameMap[e.account_id] = e.account_name;
          });
          response?.data?.events?.forEach((e: any) => {
            e.cluster = clusterIdNameMap[e.account_id];
          });

          const allIssuesData = response?.data?.events?.map((e: any) => {
            const data = [];
            data.push({
              component: (
                <ClusterNameWithRegion
                  name={e?.subject_name}
                  nameOnClick={(event: any) => {
                    event.stopPropagation();
                    handlePodClick(e?.resource_id, e?.account_id);
                  }}
                  additionalCohandlePodClntent={makeAccountClicklable(e?.account_id, e?.cluster)}
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
                  namespaceFont={undefined}
                />
              ),
            });
            data.push({ text: e?.subject_namespace });
            data.push({ text: e?.finding_type });
            data.push({ component: <Datetime baseDate={new Date()} value={e?.starts_at} /> });
            data.push({ component: <SeverityIcon severityType={e?.priority} /> });
            data.push({
              component: (
                <Box
                  display={'flex'}
                  flexDirection={'row'}
                  alignItems={'center'}
                  justifyContent={'flex-end'}
                  gap={'6px'}
                  position={'sticky'}
                  right={'0px'}
                >
                  <InvestigateButton url={`/investigate?id=${e?.id}&accountId=${e?.account_id}`} />

                  <CreateTicketButton
                    sx={{ ...action.primary }}
                    onClick={(event: any) => {
                      event.stopPropagation();
                      openTicketModal(e);
                    }}
                  />
                </Box>
              ),
            });
            return data;
          });
          setAllIssuesData(allIssuesData);
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
      const start_date = selectedDates[0].startDate;
      const end_date = selectedDates[0].endDate;
      const updatedFilterOptions = filterOptions.map((option) => ({
        ...option,
        tabOptions: option.tabOptions.map((tab) => {
          if (tab.id === 'pods') {
            return { ...tab, count: '' };
          } else if (tab.id === 'workloads') {
            return { ...tab, count: '' };
          } else if (tab.id === 'clusters') {
            return { ...tab, count: '' };
          }
          return tab;
        }),
      }));

      setFilterOptions(updatedFilterOptions);

      apiKubernetes
        .getIssueEventCounts(
          {
            start_date,
            end_date,
            ...filterObj,
          },
          ['event_count', 'subject_type']
        )
        .then((response) => {
          if (response.length > 0) {
            const podCount = response.find((row: any) => row.subject_type === 'pod')?.event_count || 0;
            const clusterCount = response.find((row: any) => row.subject_type === 'cluster')?.event_count || 0;

            apiKubernetes
              .getIssueEventCounts(
                {
                  start_date,
                  end_date,
                  finding_type: 'issue',
                  ...filterObj,
                },
                ['event_count', 'subject_type']
              )
              .then((issueResponse) => {
                const workloadCount = issueResponse
                  .filter((row: any) => ['Job', 'StatefulSet', 'Deployment', 'DaemonSet'].includes(row.subject_type))
                  .reduce((acc: any, row: any) => acc + row.event_count, 0);
                setFilterOptions((prevFilterOptions) => {
                  // prevFilterOptions is the state after counts were cleared on L240
                  return prevFilterOptions.map((option) => ({
                    ...option,
                    tabOptions: option.tabOptions.map((tab) => {
                      if (tab.id === 'workloads') {
                        return { ...tab, count: workloadCount };
                      }
                      if (tab.id === 'pods') {
                        return { ...tab, count: podCount };
                      }
                      if (tab.id === 'clusters') {
                        return { ...tab, count: clusterCount };
                      }
                      return tab;
                    }),
                  }));
                });
              });
          }
        });
    }
  }, [isElementVisible, JSON.stringify(filterObj), selectedDates]);

  useEffect(() => {
    if (isElementVisible) {
      getKubernetesIssuesData(filterObj);
    }
  }, [isElementVisible, getKubernetesIssuesData]);

  useEffect(() => {
    setShouldFetch(true);
  }, [findingType, issueSubType, filterObj, selectedDates]);

  const handlePodClick = (cloud_resource_id: string, account_id: string) => {
    router.push(`/kubernetes/podDetails/${cloud_resource_id}?PodDetails=${cloud_resource_id}&accountId=${account_id}#pod-details`);
  };

  const handleChangeIssueSubTab = (e: any, value: number) => {
    setIssueSubTab(value);
    if (value === 1) {
      setFindingType('issue');
      setIssueSubType(['Job', 'StatefulSet', 'Deployment', 'DaemonSet']);
    } else if (value === 2) {
      setIssueSubType(['cluster']);
    } else if (value === 0) {
      setIssueSubType(['pod']);
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

  const openTicketModal = (row: any) => {
    setTicketData({
      ...row,
    });
    setIsTicketCreateFormOpen(true);
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
    getKubernetesIssuesData(filterObj);
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
        id={id}
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: startDate.getTime(),
            endTime: currentDate.getTime(),
            shortcutClickTime: 0,
          },
        }}
        heading={'Issues'}
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
        <CustomTabs options={filterOptions[0].tabOptions} value={issueSubTab} onChange={handleChangeIssueSubTab} />
        <div ref={tableRef}>
          <CustomTable
            id={tableId}
            headers={clusterIssuesHeader}
            tableData={allIssuesData}
            rowsPerPage={5}
            onPageChange={undefined}
            totalRows={allIssuesData?.length}
            loading={loading}
            showExpandable={false}
            onSortChange={undefined}
            tableHeadingCenter={['Status', 'Severity']}
            stickyColumnIndex='8'
          />
        </div>
      </BoxLayout2>
    </>
  );
};

export default KubernetesDashboardIssues;
