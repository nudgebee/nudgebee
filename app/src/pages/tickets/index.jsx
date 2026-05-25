import React, { useState, useEffect } from 'react';
import ErrorBoundary from '@common-new/ErrorBoundary';
import TicketListTable from '@components1/tickets/TicketListTable';
import TicketListInfograph from '@components1/tickets/TicketListInfograph';
import { Box } from '@mui/material';
import { getUserSession } from '@lib/auth';
import AnchorComponent from '@common-new/AnchorComponent';
import { useRouter } from 'next/router';

const tabOptions = [
  { name: 'All Tickets', value: 0, defaultQuery: {}, fragment: 'tickets' },
  { name: 'Assigned to me', value: 1, disabled: false, defaultQuery: {}, enableAssigneeFilter: false, fragment: 'assigned-me' },
];

const Tickets = () => {
  const router = useRouter();
  const [tab, setTab] = useState(0);
  const [selectedPriority, setSelectedPriority] = useState(null);
  const [statusFilter, setStatusFilter] = useState([]);
  const [selectedStatus, setSelectedStatus] = useState(null);
  const [assigneeFilter, setAssigneeFilter] = useState([]);
  const [selectedAssignee, setSelectedAssignee] = useState(null);
  const [selectedTitle, setSelectedTitle] = useState(null);
  const [toolFilter, setToolFilter] = useState([]);
  const [selectedTool, setSelectedTool] = useState(null);
  const [accountFilter, setAccountFilter] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState(null);

  const handleChangeTab = (value) => {
    if (value === 1) {
      tabOptions[value].defaultQuery['assignee'] = getUserSession()?.user?.email;
    }
    setTab(value);
  };

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !tabOptions.length) return;
    const fragment = hash;
    const filter = tabOptions.find((option) => option.fragment === fragment);
    if (filter) {
      if (filter.value === 1) {
        tabOptions[filter.value].defaultQuery['assignee'] = getUserSession()?.user?.email;
      }
      setTab(filter.value);
    }
  }, []);

  const onPriorityFilterChange = (e, label) => {
    setSelectedPriority(e?.target?.value || label);
  };

  const onStatusFilterChange = (e, label) => {
    setSelectedStatus(e?.target?.value || label);
  };

  const onAssigneeFilterChange = (e) => {
    setSelectedAssignee(e?.target?.value);
  };

  const onTitleFilterChange = (e) => {
    setSelectedTitle(e?.target?.value);
  };

  const onToolFilterChange = (e, label) => {
    setSelectedTool(e?.target?.value || label);
  };

  const onAccountFilterChange = (e, label) => {
    setSelectedAccount(e?.target?.value || label);
  };

  const onClearAllFilters = () => {
    setSelectedPriority(null);
    setSelectedStatus(null);
    setSelectedAssignee(null);
    setSelectedTitle(null);
    setSelectedTool(null);
    setSelectedAccount(null);
  };

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={tabOptions} onChangeFilter={(t, s) => handleChangeTab(t, s)} />

      <ErrorBoundary key={tab}>
        <Box>
          <TicketListInfograph
            heading=''
            id={'tickets-infographics'}
            defaultQuery={{
              ...tabOptions?.[tab]?.defaultQuery,
              tool: selectedTool,
              account_id: selectedAccount,
            }}
            selectedStatus={selectedStatus}
            selectedPriority={selectedPriority}
            setSelectedPriority={setSelectedPriority}
            setSelectedStatus={setSelectedStatus}
          />
          <Box mb={2} />
          <TicketListTable
            heading=''
            id={'all-tickets'}
            defaultQuery={tabOptions?.[tab]?.defaultQuery}
            enableAssigneeFilter={tabOptions?.[tab]?.enableAssigneeFilter === undefined ? true : tabOptions?.[tab]?.enableAssigneeFilter}
            selectedPriority={selectedPriority}
            statusFilter={statusFilter}
            setStatusFilter={setStatusFilter}
            selectedStatus={selectedStatus}
            assigneeFilter={assigneeFilter}
            setAssigneeFilter={setAssigneeFilter}
            selectedAssignee={selectedAssignee}
            selectedTitle={selectedTitle}
            onPriorityFilterChange={onPriorityFilterChange}
            onStatusFilterChange={onStatusFilterChange}
            onAssigneeFilterChange={onAssigneeFilterChange}
            onTitleFilterChange={onTitleFilterChange}
            toolFilter={toolFilter}
            setToolFilter={setToolFilter}
            selectedTool={selectedTool}
            onToolFilterChange={onToolFilterChange}
            accountFilter={accountFilter}
            setAccountFilter={setAccountFilter}
            selectedAccount={selectedAccount}
            onAccountFilterChange={onAccountFilterChange}
            onClearAllFilters={onClearAllFilters}
          />
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default Tickets;
