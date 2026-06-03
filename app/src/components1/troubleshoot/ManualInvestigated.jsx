import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiUser from '@api1/user';
import { Box } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { UserIcon } from '@assets';
import { Label } from '@components1/ds/Label';
import Text from '@common-new/format/Text';
import CustomTable from '@common-new/tables/CustomTable2';
import Datetime from '@common-new/format/Datetime';
import apiHome from '@api1/home';
import { snakeToTitleCase } from 'src/utils/common';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import { Button } from '@components1/ds/Button';
import DownloadButton from '@common-new/DownloadButton';
import CustomSearch from '@common-new/CustomSearch';
import { FiArrowRight } from 'react-icons/fi';
import { ds } from 'src/utils/colors';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const variantColors = {
  COMPLETED: 'green',
  IN_PROGRESS: 'yellow',
  FAILED: 'red',
  WAITING: 'blue',
};

const statusOptions = ['COMPLETED', 'FAILED', 'IN_PROGRESS', 'WAITING', 'NEEDS_USER_INPUT', 'TERMINATED', 'KILLED'].map((s) => ({
  label: snakeToTitleCase(s),
  value: s,
}));

const ManualInvestigated = () => {
  const router = useRouter();
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(() => {
    const raw = router.query.accountId;
    return raw ? String(raw).split(',').filter(Boolean) : [];
  });
  const [selectedStatus, setSelectedStatus] = useState('');
  const [title, setTitle] = useState('');
  const [appliedTitle, setAppliedTitle] = useState('');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: new Date().getTime() - 24 * 3600 * 1000,
    endDate: new Date().getTime(),
    shortcutClickTime: 0,
  });

  const tableId = 'manualInvestigatedTable';

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
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <SafeIcon src={UserIcon} alt='User' width={18} height={18} style={{ opacity: 0.75 }} />
              <Text value={item?.user?.display_name} />
            </Box>
          ),
        },
        {
          component: (
            <>
              <Text value={item.title} showAutoEllipsis />
              {item.account_id && <Text value={`acc: ${getAccountName(item.account_id)}`} secondaryText />}
            </>
          ),
        },
        {
          component: <Datetime value={item.updated_at} sx={{ fontSize: ds.text.caption }} />,
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
              <Label margin='0' text={snakeToTitleCase(item.status)} variant={variantColors[item.status] || 'grey'} />
            </Box>
          ),
        },
        {
          component: (
            <Box display='flex' flexDirection='row' alignItems='center' gap={ds.space.mul(0, 3)} justifyContent='flex-end'>
              <Button
                tone='secondary'
                size='sm'
                trailingAccent={<FiArrowRight />}
                onClick={() => {
                  if (item.id) {
                    const href = `/ask-nudgebee?accountId=${item.account_id}&session_id=${item.session_id}`;
                    window.open(href, '_blank', 'noopener,noreferrer');
                  }
                }}
              >
                Check Details
              </Button>
            </Box>
          ),
          data: item.id ? `/ask-nudgebee?accountId=${item.account_id}&session_id=${item.session_id}` : '',
        },
      ]),
    [data, accounts]
  );

  const listManualInvestigations = () => {
    setLoading(true);
    apiAskNudgebee
      .llmConversationHistoryForInvestigation({
        source: 'UserInvestigation',
        limit: rowsPerPage,
        offset: rowsPerPage * currentPage,
        status: selectedStatus,
        title: appliedTitle,
        account_id: selectedAccountId.length ? selectedAccountId : undefined,
        startUpdatedAt: new Date(selectedDateRange.startDate).toISOString(),
        endUpdatedAt: new Date(selectedDateRange.endDate).toISOString(),
      })
      .then((res) => {
        const response = res?.data?.data?.llm_conversations || [];
        setTotalCount(res?.data?.data?.llm_conversations_aggregate?.aggregate?.count || 0);
        setData(response);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listManualInvestigations();
  }, [selectedAccountId, rowsPerPage, currentPage, selectedStatus, selectedDateRange, appliedTitle]);

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

  const onEnterPress = () => {
    setAppliedTitle(title);
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
              data-testid='manual-investigated-date-range'
              sx={{ height: ds.space[6] }}
            />
            <DownloadButton id='manual-investigated-download-btn' onClick={() => ({ tableId: tableId, fileName: 'manual-investigated.csv' })} />
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
          data-testid='manual-investigated-account-filter'
        />
        <FilterDropdown
          label='Status'
          options={statusOptions}
          value={statusOptions.find((s) => s.value === selectedStatus) || null}
          onSelect={(_e, option) => {
            setSelectedStatus(option?.value || '');
            setCurrentPage(0);
          }}
          data-testid='manual-investigated-status-filter'
        />
        <CustomSearch
          label='Search by title'
          value={title}
          onChange={(next) => {
            setTitle((prev) => {
              if (prev.trim() !== '' && next.trim() === '') {
                setAppliedTitle('');
                setCurrentPage(0);
              }
              return next;
            });
          }}
          onEnterPress={onEnterPress}
          onClear={() => {
            setTitle('');
            setAppliedTitle('');
            setCurrentPage(0);
          }}
          id='manual-investigated-search'
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CustomTable
          id={tableId}
          tableData={tableData}
          headers={[
            { name: 'User', width: '15%' },
            { name: 'Query', width: '32%' },
            { name: 'When', width: '8%' },
            { name: 'Status', width: '10%' },
            { name: '', width: '10%' },
          ]}
          tableHeadingCenter={['Status']}
          rowsPerPage={rowsPerPage}
          totalRows={totalCount}
          onPageChange={onPageChange}
          pageNumber={currentPage + 1}
          loading={loading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default ManualInvestigated;
