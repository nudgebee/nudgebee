import { useEffect, useMemo, useState } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import { Box } from '@mui/material';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { BoxLayout2, Text } from '@components1/common';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import InvestigateButton from '@components1/common/InvestigateButton';
import CustomTable from '@components1/common/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import apiHome from '@api1/home';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import CloudProviderIcon from '@components1/common/CloudIcon';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const statusFilter = [
  { value: 'FIRING', label: 'Open' },
  { value: 'CLOSED', label: 'Closed' },
];

const AutoInvestigated = () => {
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [emptyMessage, setEmptyMessage] = useState('');
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState('');
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: new Date().getTime() - 24 * 3600 * 1000,
    endDate: new Date().getTime(),
    shortcutClickTime: 0,
  });

  const tableId = 'autoInvestigatedTable';

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
            <Box display={'flex'} justifyContent={'center'} alignItems={'center'}>
              <SeverityIcon severityType={item.priority} />
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
                    fontSize: '14px',
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
            font: '12px',
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
                gap: '4px',
                '@media(max-width: 1100px)': {
                  '& p': {
                    fontSize: '14px',
                  },
                },
              }}
            >
              <CustomLabels
                margin='0'
                text={item.status === 'FIRING' ? 'Open' : item.status === 'CLOSED' ? 'Closed' : item.status}
                variant={item.status === 'FIRING' ? 'red' : item.status === 'CLOSED' ? 'grey' : ''}
                customLabelStyle={{ height: '12px' }}
              />
              <Datetime value={item.updated_at} sx={{ fontSize: '11px' }} />
            </Box>
          ),
        },
        {
          component: (
            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
              <InvestigateButton displayText url={`/investigate?id=${item.id}&accountId=${item.account_id}&autoInvestigate=true`} />
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
      account_id: selectedAccountId,
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
            account_id: selectedAccountId,
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
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      shortcutClickTime: passedSelectedDateTime.shortcutClickTime || 0,
    });
    setCurrentPage(0);
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
          options: statusFilter,
          onSelect: (e) => {
            setSelectedStatus(e.target.value);
            setCurrentPage(0);
          },
          minWidth: '90px',
          label: 'Status',
          value: selectedStatus,
        },
      ]}
      sharingOptions={{
        sharing: {
          enabled: false,
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
    </BoxLayout2>
  );
};

export default AutoInvestigated;
