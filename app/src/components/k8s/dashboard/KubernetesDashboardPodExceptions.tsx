import React, { useCallback, useEffect, useRef, useState } from 'react';
import { podExceptionHeader } from '@lib/kubernetesData';
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
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import type { TicketDataPojo } from 'src/utils/common';
import Tabs from '@shared/navigation/TabsForDrilldown';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import ticketsApi from '@api1/tickets';
import TicketLink from '@shared/links/TicketLink';

interface KubernetesTableProps {
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

const KubernetesDashboardPodExceptions: React.FC<KubernetesTableProps> = ({ id, allClusters, tab, clusterOption, allNameSpaces }) => {
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
  const rawEventsRef = useRef<any[]>([]);
  const ticketReferenceMapRef = useRef<Map<string, any>>(new Map());
  const buildRowDataRef = useRef<((evts: any[], map: Map<string, any>) => any[][]) | null>(null);

  const [podExceptionData, setPodExceptionData] = useState<any[][]>([]);
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
      if (!shouldFetch) return;
      const limit = 5;
      const start_date = selectedDates[0].startDate;
      const end_date = selectedDates[0].endDate;
      const aggregation_key = aggregationKeys;
      setLoading(true);
      apiKubernetes
        .getK8sEvents(limit, 0, { start_date, end_date, aggregation_key, ...filters })
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
              data.push({ text: e?.subject_type });
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
                      maxWidth='150px'
                      namespaceFont={undefined}
                    />
                    {existingTicket && <TicketLink ticketURL={existingTicket.url} ticketID={existingTicket.ticket_id} />}
                  </Box>
                ),
              });
              data.push({ text: e?.subject_namespace });
              data.push({ text: e?.restart_count || '-' });
              data.push({ component: <Datetime baseDate={new Date()} value={e?.starts_at} /> });
              data.push({ component: <SeverityIcon level={e?.priority} />, data: e?.priority });
              data.push({
                component: (
                  <Box
                    display='flex'
                    flexDirection='row'
                    alignItems='center'
                    gap='calc(var(--ds-space-0) * 3)'
                    position='sticky'
                    right='0px'
                    justifyContent='flex-end'
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
          setPodExceptionData(buildRowData(events, ticketReferenceMap));
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

  const handleTicketSuccess = ({ ticketId, url }: { ticketId?: string; url?: string } = {}) => {
    const referenceId = ticketData?.id;
    if (!referenceId || !buildRowDataRef.current) return;
    ticketReferenceMapRef.current.set(referenceId, { ticket_id: ticketId, url });
    const idx = rawEventsRef.current.findIndex((e: any) => e.id === referenceId);
    if (idx === -1) return;
    setPodExceptionData((prev) => {
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
          title='Pod Exception'
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
          <Tabs options={filterOptions[0].tabOptions} value={aggregationKey} onChange={handleChangeAggregationKey} />
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default KubernetesDashboardPodExceptions;
