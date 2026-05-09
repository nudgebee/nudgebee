import { useEffect, useMemo, useState } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiUser from '@api1/user';
import { Box } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { UserIcon } from '@assets';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { BoxLayout2, Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import apiHome from '@api1/home';
import InvestigateButton from '@components1/common/InvestigateButton';
import { snakeToTitleCase } from 'src/utils/common';
import CloudProviderIcon from '@components1/common/CloudIcon';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const variantColors = {
  COMPLETED: 'green',
  IN_PROGRESS: 'yellow',
  FAILED: 'red',
  WAITING: 'blue',
};

const ManualInvestigated = () => {
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState('');
  const [selectedStatus, setSelectedStatus] = useState('');
  const [title, setTitle] = useState('');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: new Date().getTime() - 24 * 3600 * 1000,
    endDate: new Date().getTime(),
    shortcutClickTime: 0,
  });

  useEffect(() => {
    apiHome.getCloudAccounts().then((res) => {
      setAccounts(res);
    });
  }, []);

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const tableData = useMemo(
    () =>
      data.map((item) => [
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <SafeIcon src={UserIcon} alt='User' width={18} height={18} style={{ opacity: 0.75 }} />
              <Text value={item?.user?.display_name} />
            </Box>
          ),
        },
        {
          component: (
            <>
              <Text value={`${item.title}`} showAutoEllipsis />
              {item.account_id && <Text value={`acc: ${getAccountName(item.account_id)}`} secondaryText />}
            </>
          ),
        },
        {
          component: <Datetime value={item.updated_at} />,
        },
        {
          component: (
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: '4px',
                '@media(max-width: 1100px)': {
                  '& p': {
                    fontSize: '14px',
                  },
                },
              }}
            >
              <CustomLabels margin='0' text={snakeToTitleCase(item.status)} variant={variantColors[item.status] || 'grey'} />
            </Box>
          ),
        },
        {
          component: (
            <Box>
              <InvestigateButton
                displayText={true}
                text='Check Details'
                onClick={() => {
                  if (item.id) {
                    const href = `/ask-nudgebee?accountId=${item.account_id}&session_id=${item.session_id}`;
                    window.open(href, '_blank', 'noopener,noreferrer');
                  }
                }}
              />
            </Box>
          ),
        },
      ]),
    [data, accounts]
  );

  const listManualInvestigations = (clearTitle = false) => {
    setLoading(true);
    apiAskNudgebee
      .llmConversationHistoryForInvestigation({
        source: 'UserInvestigation',
        limit: rowsPerPage,
        offset: rowsPerPage * currentPage,
        status: selectedStatus,
        title: clearTitle ? '' : title,
        account_id: selectedAccountId,
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
  }, [selectedAccountId, rowsPerPage, currentPage, selectedStatus, selectedDateRange]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      shortcutClickTime: passedSelectedDateTime.shortcutClickTime || 0,
    });
    setCurrentPage(0);
  };

  const onEnterPress = () => {
    listManualInvestigations();
  };

  return (
    <BoxLayout2
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: selectedDateRange.startDate,
          endTime: selectedDateRange.endDate,
          shortcutClickTime: selectedDateRange.shortcutClickTime,
        },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          grouped: true,
          groupIcon: renderAccountGroupIcon,
          options: accounts.map((acc) => ({
            label: acc.label || acc.account_name,
            value: acc.id || acc.value,
            group: acc.cloud_provider || 'Other',
          })),
          onSelect: (e) => {
            setSelectedAccountId(e.target.value);
            setCurrentPage(0);
          },
          label: 'Account',
          value: selectedAccountId,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: ['COMPLETED', 'FAILED', 'IN_PROGRESS', 'WAITING', 'NEEDS_USER_INPUT', 'TERMINATED', 'KILLED'].map((s) => ({
            label: snakeToTitleCase(s),
            value: s,
          })),
          onSelect: (e) => {
            setSelectedStatus(e.target.value);
            setCurrentPage(0);
          },
          label: 'By Status',
          value: selectedStatus,
        },
        {
          type: 'search',
          enabled: true,
          onSelect: (e) => {
            setCurrentPage(0);
            setTitle((prev) => {
              if (prev.trim() !== '' && e.target.value.trim() === '') {
                listManualInvestigations(true);
              }
              return e.target.value;
            });
          },
          minWidth: '150px',
          label: 'Search By Title',
          onEnter: onEnterPress,
          value: title,
          onClear: () => {
            setTitle('');
            setCurrentPage(0);
            listManualInvestigations(true);
          },
        },
      ]}
    >
      <CustomTable
        tableData={tableData}
        headers={[
          { name: 'User Name', width: '15%' },
          { name: 'User Query', width: '32%' },
          { name: 'When', width: '8%' },
          { name: 'Status', width: '10%' },
          { name: '', width: '10%' },
        ]}
        rowsPerPage={rowsPerPage}
        totalRows={totalCount}
        onPageChange={onPageChange}
        pageNumber={currentPage + 1}
        loading={loading}
      />
    </BoxLayout2>
  );
};

export default ManualInvestigated;
