import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import { applyFiltersOnRouter } from '@lib/router';
import { titleCaseForAggregationKey, toSeverityLevel } from 'src/utils/common';
import Datetime from '@common-new/format/Datetime';
import CustomTable2 from '@common-new/tables/CustomTable2';
import SeverityIcon from '@components1/ds/SeverityIcon';
import CustomLabels from '@common-new/widgets/CustomLabels';
import { Text } from '@components1/common';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Button } from '@components1/ds/Button';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';

interface KubernetesGroupedApplicationsProps {
  accountId: string;
}

type Option = { label: string; value: string };

const toOptions = (values: string[]): Option[] => values.map((v) => ({ label: v, value: v }));
const findOption = (options: Option[], value: string | null | undefined) => (value ? options.find((o) => o.value === value) ?? null : null);

const HEADERS = [
  { name: 'Application', width: '30%' },
  { name: 'Event Type', width: '30%' },
  { name: 'Last Occurred', width: '10%' },
  { name: 'Event Count', width: '10%' },
  { name: 'Severity', width: '10%' },
  { name: 'Alert Status', width: '10%' },
  '',
];

const KubernetesGroupedApplications: React.FC<KubernetesGroupedApplicationsProps> = ({ accountId }) => {
  const componentId = 'Grouped Events';
  const router = useRouter();

  const [currentPage, setCurrentPage] = useState<number>(1);
  const [totalRows, setTotalRows] = useState<number>(0);
  const [eventGroupings, setEventGroupings] = useState<any[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: new Date().getTime() - 60 * 60 * 24 * 1000,
    endDate: new Date().getTime(),
    key: 'selection',
  });
  const [perPage, setPerPage] = useState(apiUser.getUserPreferencesTablePageSize() ?? 10);
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [workloadFilter, setWorkloadFilter] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>((router?.query?.namespace ?? router?.query?.eventNamespace ?? '') as string);
  const [allWorkload, setAllWorkload] = useState<any[]>([]);
  const [selectedWorkload, setSelectedWorkload] = useState<string>((router?.query?.eventSubjectName ?? '') as string);
  const [aggregationKeyFilter, setAggregationKeyFilter] = useState<Option[]>([]);
  const [selectedAggregationKey, setSelectedAggregationKey] = useState<Option[]>([]);
  const [isAggregationKeyReady, setIsAggregationKeyReady] = useState(false);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      key: 'selection',
    });
    setCurrentPage(1);
  };

  const onNamespaceFilterChange = (value: string) => {
    setSelectedWorkload('');
    if (value) {
      setSelectedNamespace(value);
      const filterWorkloads = allWorkload?.filter((f: any) => f.namespace == value).map((d: any) => d.name as string);
      setWorkloadFilter(filterWorkloads);
    } else {
      const names = (allWorkload || []).map((e: any) => e.name as string);
      setWorkloadFilter([...new Set(names)]);
      setSelectedNamespace('');
    }
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventNamespace: value, eventSubjectName: '' });
  };

  const onWorkloadFilterChange = (value: string) => {
    setSelectedWorkload(value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventSubjectName: value });
  };

  const onAggregationKeyFilterChange = (items: Option[]) => {
    setSelectedAggregationKey(items || []);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventAggregationKey: (items || []).map((v) => v.value) });
  };

  useEffect(() => {
    if (!accountId) return;

    k8sApi.getK8sNamespaceNames(accountId).then((res: any) => {
      setNamespaceFilter(res.data.namespaces as string[]);
    });

    k8sApi
      .getAllK8sWorkload({ accountId })
      .then((res) => {
        const names = (res?.data || []).map((e: any) => e.name as string);
        setWorkloadFilter(Array.from(new Set(names)));
        setAllWorkload(res?.data);
      })
      .catch((error) => error);
  }, [accountId]);

  useEffect(() => {
    if (!accountId) return;
    setIsAggregationKeyReady(false);

    k8sApi
      .getEventFilterValues({
        accountId,
        filterTypes: ['aggregation_key'],
      })
      .then((res: any) => {
        let selectedKeys: any[] = [];
        const selectedValues: Option[] = [];
        if (router.query.eventAggregationKey) {
          if (Array.isArray(router.query.eventAggregationKey)) {
            selectedKeys = router.query.eventAggregationKey;
          } else if (typeof router.query.eventAggregationKey === 'string') {
            selectedKeys = router.query.eventAggregationKey.split(',');
          }
        }

        const aggregationFilter = res?.data?.filters?.find((f: any) => f.filter_type === 'aggregation_key');
        setAggregationKeyFilter(
          (aggregationFilter?.values || []).map((d: any) => {
            const data: Option = {
              label: titleCaseForAggregationKey(d.value),
              value: d.value,
            };
            if (selectedKeys.includes(d.value)) {
              selectedValues.push(data);
            }
            return data;
          })
        );
        setSelectedAggregationKey(selectedValues);
        setIsAggregationKeyReady(true);
      })
      .catch((error) => console.error(error));
  }, [accountId]);

  useEffect(() => {
    if (!accountId || !isAggregationKeyReady) return;

    const query: any = {
      account_id: accountId,
      start_date: new Date(selectedDateRange.startDate),
      end_date: new Date(selectedDateRange.endDate),
      subject_name: selectedWorkload,
      subject_namespace: selectedNamespace,
      aggregation_key: selectedAggregationKey?.map((e) => e.value),
    };

    const cols: string[] = [
      'max_created_at',
      'event_count',
      'subject_name',
      'subject_namespace',
      'aggregation_key',
      'distinct_priority',
      'distinct_status',
    ];

    setLoading(true);
    k8sApi
      .getK8sEventGroupings(
        perPage,
        (currentPage - 1) * perPage,
        query,
        ['tenant_id', 'account_id', 'subject_name', 'subject_namespace', 'aggregation_key'],
        cols,
        { name: 'event_count', order: 'desc' }
      )
      .then((res: any) => {
        setEventGroupings(res.data?.event_groupings || []);
        setTotalRows(res.data?.event_groupings_aggregate?.aggregate?.count || 0);
      })
      .catch((error) => console.error(error))
      .finally(() => setLoading(false));
  }, [
    currentPage,
    perPage,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    accountId,
    selectedNamespace,
    selectedWorkload,
    selectedAggregationKey,
    isAggregationKeyReady,
  ]);

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
            component: (
              <ClusterNameWithRegion
                name={item.subject_name}
                maxWidth='60'
                hideIcon={true}
                namespace={item.subject_namespace ? 'Namespace: ' + item.subject_namespace : ''}
                namespaceFont='12px'
              />
            ),
            drilldownQuery: {
              subject_name: [item.subject_name],
              subject_namespace: item.subject_namespace,
              finding_type: '',
              startTime: selectedDateRange.startDate,
              endTime: selectedDateRange.endDate,
              aggregation_key: item.aggregation_key,
            },
          },
          { component: <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} /> },
          { component: <Datetime baseDate={new Date()} value={item.max_created_at} /> },
          { component: <Text showAutoEllipsis value={item.event_count} /> },
          {
            component: <SeverityIcon level={toSeverityLevel(severity)} aria-label={`Severity: ${severity || 'unknown'}`} />,
            data: severity,
          },
          { component: <CustomLabels margin='auto' text={status} /> },
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
    const headerNames = ['Application', 'Namespace', 'Event Type', 'Last Occurred', 'Event Count', 'Severity', 'Alert Status'];
    const rows = eventGroupings.map((item: any) => {
      const severity = deriveSeverity(item.distinct_priority);
      const status = item.distinct_status?.indexOf('FIRING') > 0 ? 'FIRING' : 'CLOSED';
      return [
        item.subject_name || '',
        item.subject_namespace || '',
        titleCaseForAggregationKey(item.aggregation_key),
        item.max_created_at || '',
        item.event_count || 0,
        severity,
        status,
      ];
    });
    const csv = [headerNames, ...rows].map((row) => row.map(escape).join(',')).join('\r\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${componentId}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [eventGroupings]);

  const namespaceOptions = useMemo(() => toOptions(namespaceFilter), [namespaceFilter]);
  const workloadOptions = useMemo(() => toOptions(workloadFilter), [workloadFilter]);

  return (
    <ListingLayout id='grouped-applications'>
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
              id={`${componentId}-download`}
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
          id='grouped-apps-namespace'
          label='Namespace'
          options={namespaceOptions}
          value={findOption(namespaceOptions, selectedNamespace)}
          onSelect={(_e: any, item: any) => onNamespaceFilterChange(item?.value || '')}
        />
        <FilterDropdown
          id='grouped-apps-workload'
          label='Workload'
          options={workloadOptions}
          value={findOption(workloadOptions, selectedWorkload)}
          onSelect={(_e: any, item: any) => onWorkloadFilterChange(item?.value || '')}
        />
        <FilterDropdown
          id='grouped-apps-aggregation-key'
          label='Event Type'
          multiple
          options={aggregationKeyFilter}
          value={selectedAggregationKey}
          onSelect={(_e: any, items: any) => onAggregationKeyFilterChange(Array.isArray(items) ? items : [])}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CustomTable2
          id={componentId}
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
            tabs: [{ text: 'Events', key: 'events-drilldown-applications' }],
          }}
          tableHeadingCenter={['Severity', 'Alert Status']}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default KubernetesGroupedApplications;
