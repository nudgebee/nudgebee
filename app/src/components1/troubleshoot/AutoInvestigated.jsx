import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiAskNudgebee from '@api1/ask-nudgebee';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import { Box } from '@mui/material';
import CustomLabels from '@common-new/widgets/CustomLabels';
import { ds } from 'src/utils/colors';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import { Text } from '@components1/common';
import SeverityIcon from '@components1/ds/SeverityIcon';
import { Button } from '@components1/ds/Button';
import { FiArrowRight } from 'react-icons/fi';
import CustomTable from '@common-new/tables/CustomTable2';
import DownloadButton from '@common-new/DownloadButton';
import Datetime from '@common-new/format/Datetime';
import apiHome from '@api1/home';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import CloudProviderIcon from '@components1/common/CloudIcon';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const statusFilter = [
  { value: 'FIRING', label: 'Open' },
  { value: 'CLOSED', label: 'Closed' },
];

const SEVERITY_LEVEL_MAP = {
  critical: 'critical',
  high: 'high',
  warning: 'medium',
  medium: 'medium',
  low: 'low',
  info: 'info',
};

const toSeverityLevel = (priority) => SEVERITY_LEVEL_MAP[priority?.toLowerCase()] ?? 'info';

const AutoInvestigated = () => {
  const router = useRouter();
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [emptyMessage, setEmptyMessage] = useState('');
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(() => {
    const raw = router.query.accountId;
    return raw ? String(raw).split(',').filter(Boolean) : [];
  });
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: new Date().getTime() - 24 * 3600 * 1000,
    endDate: new Date().getTime(),
    shortcutClickTime: 0,
  });

  const tableId = 'autoInvestigatedTable';

  useEffect(() => {
    const raw = router.query.accountId;
    setSelectedAccountId(raw ? String(raw).split(',').filter(Boolean) : []);
  }, [router.query.accountId]);

  useEffect(() => {
    apiHome.getCloudAccounts().then((res) => {
      setAccounts(res);
    });
  }, []);

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const accountOptions = useMemo(
    () =>
      accounts.map((acc) => ({
        label: acc.label || acc.account_name,
        value: acc.id || acc.value,
        group: acc.cloud_provider || 'Other',
      })),
    [accounts]
  );

  const accountValue = useMemo(
    () =>
      accounts
        .filter((acc) => selectedAccountId.includes(acc.id || acc.value))
        .map((acc) => ({
          label: acc.label || acc.account_name,
          value: acc.id || acc.value,
          group: acc.cloud_provider || 'Other',
        })),
    [accounts, selectedAccountId]
  );

  const tableData = useMemo(
    () =>
      data.map((item) => [
        {
          component: (
            <Box display={'flex'} justifyContent={'center'} alignItems={'center'}>
              <SeverityIcon level={toSeverityLevel(item.priority)} />
            </Box>
          ),
          data: item.priority || '',
        },
        {
          component: (
            <Box
              sx={{
                '@media(max-width: 1100px)': {
                  '& p': {
                    fontSize: ds.text.bodyLg,
                  },
                },
              }}
            >
              <Text showAutoEllipsis value={item.subject_name} />
              {item.subject_namespace && <Text value={`ns: ${item.subject_namespace}`} secondaryText showAutoEllipsis />}
              {item.account_id && <Text value={`acc: ${getAccountName(item.account_id)}`} secondaryText />}
            </Box>
          ),
        },
        {
          component: ClusterNameWithRegion({
            name: item.title,
            hideIcon: true,
            smallScreenWidth: '120px',
            maxWidth: '100%',
            showAutoEllipsis: true,
            lineClamp: 3,
            showTooltip: false,
            cursorPointer: false,
            wordBreak: true,
            font: ds.text.small,
            sx: {
              fontStyle: 'italic',
            },
          }),
        },
        {
          component: (
            <Box display={'flex'} justifyContent={'center'} alignItems={'center'}>
              <CustomLabels margin='auto' text={item.urgency} />
            </Box>
          ),
        },
        {
          component: (
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                gap: ds.space[1],
                '@media(max-width: 1100px)': {
                  '& p': {
                    fontSize: ds.text.bodyLg,
                  },
                },
              }}
            >
              <CustomLabels
                margin='0'
                text={item.status === 'FIRING' ? 'Open' : item.status === 'CLOSED' ? 'Closed' : item.status}
                variant={item.status === 'FIRING' ? 'red' : item.status === 'CLOSED' ? 'grey' : ''}
                customLabelStyle={{ height: ds.space[3] }}
              />
              <Datetime value={item.updated_at} sx={{ fontSize: ds.text.caption }} />
            </Box>
          ),
        },
        {
          component: (
            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
              <Button
                tone='secondary'
                size='sm'
                href={`/investigate?id=${item.id}&accountId=${item.account_id}&autoInvestigate=true`}
                trailingAccent={<FiArrowRight />}
                onClick={(e) => e.stopPropagation()}
              >
                Investigate
              </Button>
            </Box>
          ),
          data: `/investigate?id=${item.id}&accountId=${item.account_id}&autoInvestigate=true`,
        },
      ]),
    [data, accounts]
  );

  useEffect(() => {
    setLoading(true);
    setEmptyMessage('');
    const requestParams = {
      source: 'Investigation',
      account_id: selectedAccountId.length ? selectedAccountId : undefined,
      limit: rowsPerPage,
      offset: rowsPerPage * currentPage,
      startUpdatedAt: new Date(selectedDateRange.startDate).toISOString(),
      endUpdatedAt: new Date(selectedDateRange.endDate).toISOString(),
      extractEventIdsFromTitle: true,
      event_status: selectedStatus || undefined,
    };

    apiAskNudgebee.llmConversationHistoryForInvestigation(requestParams).then((res) => {
      const response = res?.data?.data?.llm_conversations || [];
      setTotalCount(res?.data?.data?.llm_conversations_aggregate?.aggregate?.count || 0);
      if (response?.length > 0) {
        const eventIds = response
          .map((item) => item.title?.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)?.[0])
          .filter(Boolean);

        if (eventIds.length < response.length) {
          console.warn(`[AutoInvestigated] UUID extraction failed for ${response.length - eventIds.length} of ${response.length} conversation(s)`);
        }

        if (eventIds.length === 0) {
          setData([]);
          setEmptyMessage('Could not match any events from investigations. Event data may be unavailable.');
          setLoading(false);
          return;
        }

        k8sApi
          .getK8sEvents(rowsPerPage, 0, {
            eventIds,
            account_id: selectedAccountId.length ? selectedAccountId : undefined,
            timeFilter: false,
          })
          .then((eventRes) => {
            const events = eventRes?.data?.events || [];
            setData(events);
          })
          .finally(() => {
            setLoading(false);
          });
      } else {
        setData([]);
        setTotalCount(0);
        setLoading(false);
      }
    });
  }, [selectedAccountId, selectedStatus, rowsPerPage, currentPage, selectedDateRange]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    const dt = passedSelectedDateTime?.selection || passedSelectedDateTime;
    setSelectedDateRange({
      startDate: dt.startTime,
      endDate: dt.endTime,
      shortcutClickTime: dt.shortcutClickTime || 0,
    });
    setCurrentPage(0);
  };

  return (
    <ListingLayout>
      <ListingLayout.Toolbar
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{ startTime: selectedDateRange.startDate, endTime: selectedDateRange.endDate }}
              onChange={handleDateRangeChange}
              data-testid='auto-investigated-date-range'
              sx={{ height: '32px' }}
            />
            <DownloadButton id='auto-investigated-download-btn' onClick={() => ({ tableId: tableId, fileName: 'auto-investigated.csv' })} />
          </>
        }
      >
        <FilterDropdown
          label='Account'
          options={accountOptions}
          value={accountValue}
          multiple
          grouped
          groupIcon={renderAccountGroupIcon}
          onSelect={(_e, value) => {
            const ids = (value || []).map((v) => v.value);
            setSelectedAccountId(ids);
            setCurrentPage(0);
            applyFiltersOnRouter(router, { accountId: ids.join(',') });
          }}
          data-testid='auto-investigated-account-filter'
        />
        <FilterDropdown
          label='Status'
          options={statusFilter}
          value={statusFilter.find((s) => s.value === selectedStatus) || null}
          onSelect={(_e, option) => {
            setSelectedStatus(option?.value || '');
            setCurrentPage(0);
          }}
          data-testid='auto-investigated-status-filter'
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CustomTable
          id={tableId}
          tableData={tableData}
          headers={[
            { name: 'Severity', width: '5%' },
            { name: 'Resource/Instance', width: '15%' },
            { name: 'Message', width: '30%' },
            { name: 'Triage Priority', width: '10%' },
            { name: 'Status', width: '10%' },
            { name: '', width: '10%' },
          ]}
          tableHeadingCenter={['Severity', 'Triage Priority', 'Status']}
          rowsPerPage={rowsPerPage}
          totalRows={totalCount}
          onPageChange={onPageChange}
          pageNumber={currentPage + 1}
          loading={loading}
          emptyStateText={emptyMessage || undefined}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default AutoInvestigated;
