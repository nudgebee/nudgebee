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

  const [selectedFilter, setSelectedFilter] = React.useState(0);

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
    if (!hash || !filterOptions.length) return;
    const [fragment] = hash.split('/');
    const filter = filterOptions.find((option) => option.fragment === fragment && !option.disabled);
    if (filter) {
      setSelectedFilter(filter.value);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.asPath, router.isReady]);

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        filterOptions={filterOptions.filter((opt) => !opt.disabled)}
        onChangeFilter={(val) => {
          setSelectedFilter(val);
        }}
      />
      <Box sx={{ position: 'relative', mt: 3 }}>
        <ErrorBoundary key={selectedFilter}>
          {/* Tenant-level: each tab resolves its own accounts. The listing and
              Executions carry an Account filter; Task Runner an account picker. */}
          {selectedFilter === 0 && <WorkflowListing />}
          {selectedFilter === 1 && isAdmin && <TaskRunner />}
          {selectedFilter === 2 && <ExecutionDashboard />}
        </ErrorBoundary>
      </Box>
    </>
  );
};

export default withAccountGuard(Automation, { hideContent: true });
