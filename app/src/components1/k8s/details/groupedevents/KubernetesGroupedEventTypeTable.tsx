import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import { getSpecificTime } from '@lib/datetime';
import { applyFiltersOnRouter } from '@lib/router';
import { titleCaseForAggregationKey, toSeverityLevel } from 'src/utils/common';
import Datetime from '@common-new/format/Datetime';
import CustomTable2 from '@common-new/tables/CustomTable2';
import SeverityIcon from '@components1/ds/SeverityIcon';
import CustomLabels from '@common-new/widgets/CustomLabels';
import { Text } from '@components1/common';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Button } from '@components1/ds/Button';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';

interface KubernetesGroupedEventTypeTableProps {
  accountId: string;
}

type Option = { label: string; value: string };

const findOption = (options: Option[], value: string | null | undefined) => (value ? options.find((o) => o.value === value) ?? null : null);

const STATUS_OPTIONS: Option[] = [
  { value: 'FIRING', label: 'Firing' },
  { value: 'CLOSED', label: 'Closed' },
];
const PRIORITY_OPTIONS: Option[] = [
  { value: 'HIGH', label: 'High' },
  { value: 'MEDIUM', label: 'Medium' },
  { value: 'DEBUG', label: 'Debug' },
  { value: 'LOW', label: 'Low' },
  { value: 'INFO', label: 'Info' },
];

const HEADERS = [
  'Event Type',
  'Last Occurred',
  'Event Count',
  { name: 'Severity', width: '10%' },
  { name: 'Alert Status', width: '10%' },
  'Subjects',
  '',
];

const TABLE_ID = 'kubernetesEventType';

const KubernetesGroupedEventTypeTable: React.FC<KubernetesGroupedEventTypeTableProps> = ({ accountId }) => {
  const router = useRouter();

  const [currentPage, setCurrentPage] = useState<number>(1);
  const [totalRows, setTotalRows] = useState<number>(0);
  const [eventGroupings, setEventGroupings] = useState<any[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [selectedDateRange, setSelectedDateRange] = useState<{ startDate: number; endDate: number }>({
    startDate: getSpecificTime(1440),
    endDate: new Date().getTime(),
  });

  const [selectedStatus, setSelectedStatus] = useState<string>('');
  const [selectedPriority, setSelectedPriority] = useState<string>((router.query.eventPriority ?? '') as string);
  const [selectedAggregationKey, setSelectedAggregationKey] = useState<string>((router.query.aggregation_key ?? '') as string);
  const [aggregationKeyFilter, setAggregationKeyFilter] = useState<Option[]>([]);
  const [perPage, setPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const onStatusFilterChange = (value: string) => {
    setSelectedStatus(value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventStatus: value });
  };

  const onPriorityFilterChange = (value: string) => {
    setSelectedPriority(value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventPriority: value });
  };

  const onAggregationKeyFilterChange = (value: string) => {
    setSelectedAggregationKey(value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { aggregation_key: value });
  };

  useEffect(() => {
    if (!accountId) return;

    const query: any = {
      account_id: accountId,
      start_date: new Date(selectedDateRange.startDate),
      end_date: new Date(selectedDateRange.endDate),
    };
    if (selectedPriority) query.priority = selectedPriority;
    if (selectedStatus) query.status = selectedStatus;
    if (selectedAggregationKey) query.aggregation_key = selectedAggregationKey;

    const cols: string[] = ['max_created_at', 'event_count', 'aggregation_key', 'count_subject_name', 'distinct_priority', 'distinct_status'];

    setLoading(true);
    k8sApi
      .getK8sEventGroupings(perPage, (currentPage - 1) * perPage, query, ['tenant_id', 'account_id', 'aggregation_key'], cols, {
        name: 'event_count',
        order: 'desc',
      })
      .then((res: any) => {
        setEventGroupings(res.data?.event_groupings || []);
        setTotalRows(res.data?.event_groupings_aggregate?.aggregate?.count || 0);
      })
      .catch((error) => console.error(error))
      .finally(() => setLoading(false));
  }, [
    currentPage,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    accountId,
    perPage,
    selectedPriority,
    selectedStatus,
    selectedAggregationKey,
  ]);

  useEffect(() => {
    if (!accountId) return;

    k8sApi
      .getEventFilterValues({
        accountId,
        filterTypes: ['aggregation_key'],
      })
      .then((res: any) => {
        const aggregationFilter = res?.data?.filters?.find((f: any) => f.filter_type === 'aggregation_key');
        setAggregationKeyFilter(
          (aggregationFilter?.values || []).map((d: any) => ({
            label: titleCaseForAggregationKey(d.value),
            value: d.value,
          }))
        );
      });
  }, [accountId]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setCurrentPage(1);
  };

  const deriveSeverity = (distinctPriority: string | undefined): string => {
    if (!distinctPriority) return 'INFO';
    if (distinctPriority.indexOf('HIGH') >= 0) return 'HIGH';
    if (distinctPriority.indexOf('MEDIUM') >= 0) return 'MEDIUM';
    if (distinctPriority.indexOf('LOW') >= 0) return 'LOW';
    if (distinctPriority.indexOf('DEBUG') >= 0) return 'DEBUG';
    return 'INFO';
  };

  const tableData = useMemo(
    () =>
      eventGroupings.map((item: any) => {
        const severity = deriveSeverity(item.distinct_priority);
        const status = item.distinct_status?.indexOf('FIRING') > 0 ? 'FIRING' : 'CLOSED';

        return [
          {
            component: <Text value={titleCaseForAggregationKey(item.aggregation_key)} showAutoEllipsis sx={{ minWidth: '300px' }} />,
            drilldownQuery: {
              aggregation_key: item.aggregation_key,
              startTime: selectedDateRange.startDate,
              endTime: selectedDateRange.endDate,
              namespace: '',
            },
          },
          { component: <Datetime baseDate={new Date()} value={item.max_created_at} /> },
          { component: <Text value={item.event_count} /> },
          {
            component: <SeverityIcon level={toSeverityLevel(severity)} aria-label={`Severity: ${severity || 'unknown'}`} />,
            data: severity,
          },
          { component: <CustomLabels margin='auto' text={status} /> },
          { component: <Text sx={{ textAlign: 'center' }} value={item.count_subject_name ?? '-'} /> },
          {},
        ];
      }),
    [eventGroupings, selectedDateRange.startDate, selectedDateRange.endDate]
  );

  const handleDownloadCsv = useCallback(() => {
    const escape = (v: unknown) => {
      const str = v == null ? '' : String(v);
      return `"${str.replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    };
    const headerNames = ['Event Type', 'Last Occurred', 'Event Count', 'Severity', 'Alert Status', 'Subjects'];
    const rows = eventGroupings.map((item: any) => {
      const severity = deriveSeverity(item.distinct_priority);
      const status = item.distinct_status?.indexOf('FIRING') > 0 ? 'FIRING' : 'CLOSED';
      return [
        titleCaseForAggregationKey(item.aggregation_key),
        item.max_created_at || '',
        item.event_count || 0,
        severity,
        status,
        item.count_subject_name ?? '',
      ];
    });
    const csv = [headerNames, ...rows].map((row) => row.map(escape).join(',')).join('\r\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${TABLE_ID}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [eventGroupings]);

  return (
    <ListingLayout id='grouped-event-type'>
      <ListingLayout.Toolbar
        actions={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
                shortcutClickTime: 0,
              }}
              onChange={({ selection }: any) => handleDateRangeChange(selection)}
            />
            <Button
              id={`${TABLE_ID}-download`}
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
          id='grouped-event-type-severity'
          label='Severity'
          options={PRIORITY_OPTIONS}
          value={findOption(PRIORITY_OPTIONS, selectedPriority)}
          onSelect={(_e: any, item: any) => onPriorityFilterChange(item?.value || '')}
        />
        <FilterDropdown
          id='grouped-event-type-status'
          label='Status'
          options={STATUS_OPTIONS}
          value={findOption(STATUS_OPTIONS, selectedStatus)}
          onSelect={(_e: any, item: any) => onStatusFilterChange(item?.value || '')}
        />
        <FilterDropdown
          id='grouped-event-type-aggregation-key'
          label='Event Type'
          options={aggregationKeyFilter}
          value={findOption(aggregationKeyFilter, selectedAggregationKey)}
          onSelect={(_e: any, item: any) => onAggregationKeyFilterChange(item?.value || '')}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CustomTable2
          id={TABLE_ID}
          headers={HEADERS}
          loading={loading}
          tableData={tableData}
          rowsPerPage={perPage}
          totalRows={totalRows}
          onPageChange={(e: number, limit: number) => {
            setCurrentPage(e);
            setPerPage(limit);
          }}
          pageNumber={currentPage}
          onSortChange={undefined}
          showExpandable
          expandable={{
            tabs: [{ text: 'Events', key: 'events-drilldown-events' }],
          }}
          tableHeadingCenter={['Severity', 'Alert Status', 'Subjects']}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default KubernetesGroupedEventTypeTable;
