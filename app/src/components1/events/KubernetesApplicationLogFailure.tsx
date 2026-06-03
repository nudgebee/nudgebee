import React, { useEffect, useMemo, useState, useCallback } from 'react';
import k8sApi, { priorityFilter, statusFilter } from '@api1/kubernetes';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import apiUser from '@api1/user';
import { applyFiltersOnRouter } from '@lib/router';
import { safeJSONParse } from 'src/utils/common';
import { getLast7Days } from '@lib/datetime';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import Text from '@common-new/format/Text';
import Datetime from '@common-new/format/Datetime';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Button } from '@components1/ds/Button';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { toast as snackbar } from '@components1/ds/Toast';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';

interface DefaulQueryProps {
  aggregation_key: string[];
  namespace: string;
  workloadName: string;
  eventPriority: string;
  eventStatus: string;
}

interface KubernetesApplicationLogFailureProps {
  accountId: string;
  defaultQuery: DefaulQueryProps;
  stickyColumnIndex: string;
}

type Option = { label: string; value: string };

const toOptions = (values: string[]): Option[] => values.map((v) => ({ label: v, value: v }));
const findOption = (options: Option[], value: string | null | undefined) => (value ? options.find((o) => o.value === value) ?? null : null);

// Container ID has shape `/path/<namespace>/<pod>/<container>` — pull
// namespace and a stripped-pod-name as a stand-in for application name.
const extractNamespaceAndApplication = (value: string, type: 'namespace' | 'application') => {
  if (!value) return value;
  const valueArray = value.split('/').filter((e) => e != '');
  if (valueArray.length === 4) {
    if (type === 'namespace') return valueArray[1];
    const secondLastHyphenIndex = valueArray[2].lastIndexOf('-', valueArray[2].lastIndexOf('-') - 1);
    return secondLastHyphenIndex !== -1 ? valueArray[2].substring(0, secondLastHyphenIndex) : valueArray[2];
  }
  return value;
};

const KubernetesApplicationLogFailure: React.FC<KubernetesApplicationLogFailureProps> = ({ accountId, defaultQuery, stickyColumnIndex = '' }) => {
  const router = useRouter();

  const [events, setEvents] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [currentPage, setCurrentPage] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState<any>({});
  const [totalCount, setTotalCount] = useState(0);
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [workloadFilter, setWorkloadFilter] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>(
    (defaultQuery?.namespace ?? router?.query?.namespace ?? router?.query?.eventNamespace ?? '') as string
  );
  const [allWorkload, setAllWorkload] = useState<any[]>([]);
  const [selectedStatus, setSelectedStatus] = useState<string>((defaultQuery?.eventStatus ?? router.query.eventStatus ?? '') as string);
  const [selectedPriority, setSelectedPriority] = useState<string>((defaultQuery?.eventPriority ?? router.query.eventPriority ?? '') as string);
  const [selectedWorkload, setSelectedWorkload] = useState<string>((defaultQuery?.workloadName ?? router?.query?.eventSubjectName ?? '') as string);
  const [selectedDateRange, setSelectedDateRange] = useState<{ startDate: number; endDate: number }>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const k8sProm = 'k8sAppLogFailure';
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  useEffect(() => {
    if (!accountId) return;
    k8sApi.getK8sNamespaceNames(accountId).then((res: any) => {
      setNamespaceFilter(res.data.namespaces);
    });
  }, [accountId]);

  useEffect(() => {
    if (!accountId) return;
    k8sApi
      .getAllK8sWorkload({ accountId })
      .then((res) => {
        setWorkloadFilter([...new Set(res?.data.map((e: any) => e.name as string))] as string[]);
        setAllWorkload(res?.data);
      })
      .catch((error) => error);
  }, [accountId]);

  const handleSubmit = useCallback(() => {
    setLoading(true);
    const query: any = {};
    if (selectedNamespace) query.subject_namespace = selectedNamespace;
    if (accountId) query.account_id = accountId;
    if (selectedPriority) query.priority = selectedPriority;
    if (selectedStatus) query.status = selectedStatus;
    if (selectedWorkload) query.subject_name = selectedWorkload;
    if (selectedDateRange?.startDate) query.startDate = new Date(selectedDateRange.startDate);
    if (selectedDateRange?.endDate) query.endDate = new Date(selectedDateRange.endDate);
    k8sApi
      .getK8sEvents(recordsPerPage, currentPage * recordsPerPage, { ...query, ...defaultQuery })
      .then((res: any) => {
        setEvents(res.data?.events || []);
        setTotalCount(res.data.events_aggregate?.aggregate?.count);
      })
      .catch((error) => console.error(error))
      .finally(() => setLoading(false));
  }, [
    accountId,
    currentPage,
    recordsPerPage,
    selectedNamespace,
    selectedWorkload,
    selectedPriority,
    selectedStatus,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    defaultQuery,
  ]);

  useEffect(() => {
    handleSubmit();
  }, [handleSubmit]);

  const parseLabels = (item: any) => {
    if (!item?.labels) return {};
    let labelData = item.labels;
    if (typeof labelData === 'string') labelData = safeJSONParse(labelData);
    if (labelData && typeof labelData === 'object' && Object.keys(labelData).length > 0) return labelData;
    return {};
  };

  const tableData = useMemo(
    () =>
      events.map((item: any) => {
        const labels = parseLabels(item);
        const investigateUrl = `/investigate?id=${item.id}&accountId=${accountId}`;
        return [
          {
            component: <Text value={labels.sample || '-'} copyableTooltip={true} showAutoEllipsis />,
            drilldownQuery: item,
          },
          {
            component: <Text showAutoEllipsis value={extractNamespaceAndApplication(labels.container_id, 'namespace')} />,
          },
          {
            component: <Text showAutoEllipsis value={extractNamespaceAndApplication(labels.container_id, 'application')} />,
          },
          { component: <Datetime value={item.starts_at} baseDate={new Date()} />, data: item.starts_at },
          {
            component: (
              <Box
                onClick={(e: React.MouseEvent) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], justifyContent: 'flex-end' }}
              >
                <Button id={`action-investigate-${item.id}`} tone='secondary' size='sm' href={investigateUrl} trailingAccent={<ArrowForwardIcon />}>
                  Investigate
                </Button>
                <DropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `action-ticket-${item.id}`,
                      label: 'Create Ticket',
                      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
                      onSelect: () => {
                        setTicketData(item);
                        setIsTicketCreateFormOpen(true);
                      },
                    },
                  ]}
                  trigger={
                    <Button
                      tone='ghost'
                      size='sm'
                      composition='icon-only'
                      icon={<MoreVertIcon />}
                      aria-label='More actions'
                      id={`action-menu-${item.id}`}
                    />
                  }
                />
              </Box>
            ),
          },
        ];
      }),
    [events, accountId]
  );

  const closeTicketCreateForm = () => setIsTicketCreateFormOpen(false);

  const getTicketDescription = (data: any) => {
    let description = '';
    description += '**Title**: ' + data.title + '\n';
    description += '**Priority**: ' + data.priority + '\n';
    description += '**Aggregation Key**: ' + data.aggregation_key + '\n';
    description += '**Subject Type**: ' + data.subject_type + '\n';
    description += '**Subject Name**: ' + data.subject_name + '\n';
    description += '**Subject Namespace**: ' + data.subject_namespace + '\n';
    return description;
  };

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const onNamespaceFilterChange = (value: string) => {
    if (value) {
      setSelectedNamespace(value);
      const filterWorkloads = allWorkload.filter((f: any) => f.namespace == value).map((d: any) => d.name);
      setWorkloadFilter(filterWorkloads);
    } else {
      const names = allWorkload.map((e: any) => e.name as string);
      setWorkloadFilter(Array.from(new Set(names)));
      setSelectedNamespace('');
    }
    setSelectedWorkload('');
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventNamespace: value, eventSubjectName: '' });
  };

  const handleTicketSuccess = () => handleSubmit();
  const handleTicketFailure = (res: string) => snackbar.error(`Failed! ${res}.`);

  const handleDownloadCsv = useCallback(() => {
    const escape = (v: unknown) => {
      const str = v == null ? '' : String(v);
      return `"${str.replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    };
    const headers = ['Sample', 'Namespace', 'Application', 'Created At'];
    const rows = events.map((item: any) => {
      const labels = parseLabels(item);
      return [
        labels.sample || '',
        extractNamespaceAndApplication(labels.container_id, 'namespace') || '',
        extractNamespaceAndApplication(labels.container_id, 'application') || '',
        item.starts_at || '',
      ];
    });
    const csv = [headers, ...rows].map((row) => row.map(escape).join(',')).join('\r\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${k8sProm}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [events]);

  const namespaceOptions = useMemo(() => toOptions(namespaceFilter), [namespaceFilter]);
  const workloadOptions = useMemo(() => toOptions(workloadFilter), [workloadFilter]);

  return (
    <Box>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Event - ' + ticketData?.title,
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        ticketUrl={{ url: `/investigate?id=${ticketData?.id}` }}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      <ListingLayout id='k8s-log-failures'>
        <ListingLayout.Toolbar
          actions={
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                  shortcutClickTime: 0,
                }}
                onChange={({ selection }: any) => setSelectedDateRange({ startDate: selection.startTime, endDate: selection.endTime })}
              />
              <Button
                id={`${k8sProm}-download`}
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<FileDownloadOutlinedIcon />}
                aria-label='Download as CSV'
                tooltip='Download as CSV'
                onClick={handleDownloadCsv}
              />
            </Box>
          }
        >
          <FilterDropdown
            id={`${k8sProm}-namespace`}
            label='Namespace'
            options={namespaceOptions}
            value={findOption(namespaceOptions, selectedNamespace)}
            onSelect={(_e: any, item: any) => onNamespaceFilterChange(item?.value || '')}
          />
          <FilterDropdown
            id={`${k8sProm}-workload`}
            label='Workload'
            options={workloadOptions}
            value={findOption(workloadOptions, selectedWorkload)}
            onSelect={(_e: any, item: any) => {
              setSelectedWorkload(item?.value || '');
              setCurrentPage(0);
              applyFiltersOnRouter(router, { eventSubjectName: item?.value || '' });
            }}
          />
          <FilterDropdown
            id={`${k8sProm}-severity`}
            label='Severity'
            options={priorityFilter as Option[]}
            value={findOption(priorityFilter as Option[], selectedPriority)}
            onSelect={(_e: any, item: any) => {
              setSelectedPriority(item?.value || '');
              setCurrentPage(0);
              applyFiltersOnRouter(router, { eventPriority: item?.value || '' });
            }}
          />
          <FilterDropdown
            id={`${k8sProm}-status`}
            label='Status'
            options={statusFilter as Option[]}
            value={findOption(statusFilter as Option[], selectedStatus)}
            onSelect={(_e: any, item: any) => {
              setSelectedStatus(item?.value || '');
              setCurrentPage(0);
              applyFiltersOnRouter(router, { eventStatus: item?.value || '' });
            }}
          />
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={k8sProm}
            totalRows={totalCount}
            tableData={tableData}
            headers={[
              { name: 'Sample', width: '50%' },
              { name: 'Namespace', width: '15%' },
              { name: 'Application', width: '15%' },
              { name: 'Created At', width: '10%' },
              { name: '', width: '10%', align: 'right' as const },
            ]}
            rowsPerPage={recordsPerPage}
            showExpandable={false}
            loading={loading}
            onSortChange={undefined}
            onPageChange={onPageChange}
            pageNumber={currentPage + 1}
            stickyColumnIndex={stickyColumnIndex}
            onRowClick={(row: any) => {
              router.push(`/investigate?id=${row.id}&accountId=${accountId}`);
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </Box>
  );
};

export default KubernetesApplicationLogFailure;
