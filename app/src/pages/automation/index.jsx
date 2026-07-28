import React, { useEffect } from 'react';
import { Box } from '@mui/material';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import { withAccountGuard } from '@shared/AccountGuard';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useRouter } from 'next/router';
import { getUserSession } from '@lib/auth';
import { AutomateBlue, PlayCircleIcon, dashboardIcon1 } from '@assets';
import WorkflowListing from '@components/workflow/WorkflowListing';
import TaskRunner from '@components/workflow/TaskRunner';
import ExecutionDashboard from '@components/workflow/execution-dashboard/ExecutionDashboard';

const Automation = () => {
  const router = useRouter();
  const session = getUserSession();
  const isAdmin = session?.roles?.includes('tenant_admin') || session?.roles?.includes('account_admin');

  // 1. Initialize state with defaults (0) instead of router.query
  const [selectedFilter, setSelectedFilter] = React.useState(null);
  const [subTab, setSubTab] = React.useState(0);

  // Executions is appended rather than inserted first: selectedFilter defaults
  // to 0 and the render block below is index-based, so putting it first would
  // silently change which tab everyone lands on.
  const filterOptions = [
    { name: 'Automations', id: 'automations', value: 0, fragment: 'automations', icon: AutomateBlue },
    { name: 'Task Runner', id: 'task-runner', value: 1, fragment: 'task-runner', disabled: !isAdmin, icon: PlayCircleIcon },
    { name: 'Executions', id: 'executions', value: 2, fragment: 'executions', icon: dashboardIcon1 },
  ];

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) {
      setSelectedFilter(0);
      return;
    }
    const [fragment, subFragment] = hash.split('/');
    const filter = filterOptions.find((option) => option.fragment === fragment && !option.disabled);
    if (filter) {
      setSelectedFilter(filter.value);
      if (!subFragment) return;
      const subTab = (filter?.tabOptions || []).find((tab) => tab.fragment === subFragment);
      if (subTab) {
        setSubTab(subTab.value);
      }
    } else {
      setSelectedFilter(0);
    }
  }, []);

  const getAnchorComponent = () => {
    let Anchor = (
      <AnchorComponent
        manageRoute={true}
        options={filterOptions[selectedFilter]?.options || []}
        filterOptions={filterOptions.filter((opt) => !opt.disabled)}
        // Updated Handler: Pushes new Hash URL instead of setting state directly
        onChangeFilter={(val, subVal) => {
          setSelectedFilter(val);
          setSubTab(subVal);
        }}
      />
      <Box sx={{ position: 'relative', mt: 3 }}>
        <ErrorBoundary key={selectedFilter}>
          {selectedFilter === 0 && <WorkflowListing accountId={router?.query?.accountId} />}
          {selectedFilter === 1 && isAdmin && <TaskRunner accountId={router?.query?.accountId} />}
          {selectedFilter === 2 && <ExecutionDashboard accountId={router?.query?.accountId} />}
        </ErrorBoundary>
      </Box>
    </>
  );
};

export default withAccountGuard(Automation, { hideContent: true });
