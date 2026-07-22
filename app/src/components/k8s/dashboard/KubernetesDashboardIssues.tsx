import React, { useCallback, useEffect, useRef, useState } from 'react';
import { clusterIssuesHeader } from '@lib/kubernetesData';
import apiKubernetes from '@api1/kubernetes';
import SeverityIcon from '@ui/SeverityIcon';
import { Label } from '@ui/Label';
import ClusterNameWithRegion from '@components/k8s/common/ClusterNameWithRegion';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import ReactLink from 'next/link';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@shared/buttons/DownloadButton';
import Datetime from '@shared/format/Datetime';
import { FiArrowRight } from 'react-icons/fi';
import type { TicketDataPojo } from 'src/utils/common';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import Tabs from '@shared/navigation/TabsForDrilldown';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import ticketsApi from '@api1/tickets';
import TicketLink from '@shared/links/TicketLink';

interface KubernetesTableProps {
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

const KubernetesDashboardIssues: React.FC<KubernetesTableProps> = ({ id, allClusters, clusterOption, allNameSpaces }) => {
  const currentDate = new Date();
  const startDate = new Date(currentDate);
  startDate.setDate(currentDate.getDate() - 1);
  const router = useRouter();
  const tableRef = useRef<HTMLDivElement>(null);
  const rawEventsRef = useRef<any[]>([]);
  const ticketReferenceMapRef = useRef<Map<string, any>>(new Map());
  const buildRowDataRef = useRef<((evts: any[], map: Map<string, any>) => any[][]) | null>(null);

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
  const [allIssuesData, setAllIssuesData] = useState<any[][]>([]);
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
      if (!shouldFetch) return;
      setLoading(true);
      setAllIssuesData([]);
      const limit = 5;
      const finding_type = findingType;
      const subject_type = issueSubType || ['pod'];
      const start_date = selectedDates[0].startDate;
      const end_date = selectedDates[0].endDate;
      apiKubernetes
        .getK8sEvents(limit, 0, { finding_type, subject_type, start_date, end_date, ...filters })
        .then(async (response: any) => {
          const clusterIdNameMap: { [accountId: string]: string } = {};
          allClusters.forEach((c) => {
            clusterIdNameMap[c.account_id] = c.account_name;
          });
          const events: any[] = response?.data?.events || [];
          events.forEach((e: any) => {
            e.cluster = clusterIdNameMap[e.account_id];
          });

          const ticketReferenceMap = new Map<string, any>();
          const eventIds = events.map((e: any) => e.id).filter(Boolean);
          if (eventIds.length > 0) {
            try {
              const ticketRes: any = await ticketsApi.listTicketsSummary({ reference_id: eventIds });
              ticketRes?.data?.tickets?.forEach((t: any) => {
                ticketReferenceMap.set(t.reference_id, t);
              });
            } catch (err) {
              console.error('Error fetching ticket summaries', err);
            }
          }

          const buildRowData = (evts: any[], ticketMap: Map<string, any>): any[][] =>
            evts.map((e: any) => {
              const data: any[] = [];
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
              data.push({ component: <Label margin='auto' text={e?.status} /> });
              const existingTicket = ticketMap.get(e.id);
              data.push({
                component: (
                  <Box>
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
                    {existingTicket && <TicketLink ticketURL={existingTicket.url} ticketID={existingTicket.ticket_id} />}
                  </Box>
                ),
              });
              data.push({ text: e?.subject_namespace });
              data.push({ text: e?.finding_type });
              data.push({ component: <Datetime baseDate={new Date()} value={e?.starts_at} /> });
              data.push({ component: <SeverityIcon level={e?.priority} />, data: e?.priority });
              data.push({
                component: (
                  <Box
                    display='flex'
                    flexDirection='row'
                    alignItems='center'
                    justifyContent='flex-end'
                    gap='calc(var(--ds-space-0) * 3)'
                    position='sticky'
                    right='0px'
                  >
                    <Button
                      tone='secondary'
                      size='xs'
                      trailingAccent={<FiArrowRight />}
                      href={`/investigate?id=${e?.id}&accountId=${e?.account_id}`}
                      data-testid='investigate-btn'
                    >
                      Investigate
                    </Button>
                    <Button
                      tone='ghost'
                      size='sm'
                      composition='icon-only'
                      icon={<SafeIcon priority src={TicketsIcon} alt='Create Ticket' />}
                      tooltip={existingTicket ? 'Ticket already exists' : 'Create Ticket'}
                      aria-label='Create Ticket'
                      id='create-ticket'
                      disabled={!!existingTicket}
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

          rawEventsRef.current = events;
          ticketReferenceMapRef.current = ticketReferenceMap;
          buildRowDataRef.current = buildRowData;
          setAllIssuesData(buildRowData(events, ticketReferenceMap));
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
      <Typography style={{ fontSize: 'var(--ds-text-small)' }}>
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

  const handleTicketSuccess = ({ ticketId, url }: { ticketId?: string; url?: string } = {}) => {
    const referenceId = ticketData?.id;
    if (!referenceId || !buildRowDataRef.current) return;
    ticketReferenceMapRef.current.set(referenceId, { ticket_id: ticketId, url });
    const idx = rawEventsRef.current.findIndex((e: any) => e.id === referenceId);
    if (idx === -1) return;
    setAllIssuesData((prev: any[]) => {
      const next = [...prev];
      next[idx] = buildRowDataRef.current!([rawEventsRef.current[idx]], ticketReferenceMapRef.current)[0];
      return next;
    });
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
      <ListingLayout id={id}>
        <ListingLayout.Toolbar
          title='Issues'
          actions={
            <>
              <CustomDateTimeRangePicker
                onChange={({ selection }) => handleDateRangeChange(selection)}
                passedSelectedDateTime={{
                  startTime: startDate.getTime(),
                  endTime: currentDate.getTime(),
                  shortcutClickTime: 0,
                }}
              />
              <DownloadButton onClick={() => ({ tableId: tableId })} />
            </>
          }
        >
          <FilterDropdown label='Cluster' options={clusterOption} value={filterObj.account_id || ''} onSelect={filterByCluster} />
          <FilterDropdown
            label='Namespace'
            options={namespaces.map((o) => ({ value: o, label: o }))}
            value={filterObj.subject_namespace || ''}
            onSelect={onNamespaceFilterChange}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <Tabs options={filterOptions[0].tabOptions} value={issueSubTab} onChange={handleChangeIssueSubTab} />
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default KubernetesDashboardIssues;
