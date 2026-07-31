import React, { useEffect } from 'react';
import { Box } from '@mui/material';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import { withAccountGuard } from '@shared/AccountGuard';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useRouter } from 'next/router';
import { getUserSession, hasPermission, missingPermissionMessage } from '@lib/auth';
import { AutomateBlue, PlayCircleIcon, dashboardIcon1 } from '@assets';
import WorkflowListing from '@components/workflow/WorkflowListing';
import TaskRunner from '@components/workflow/TaskRunner';
import ExecutionDashboard from '@components/workflow/execution-dashboard/ExecutionDashboard';

const Automation = () => {
  const router = useRouter();
  const session = getUserSession();
  const isAdmin = session?.roles?.includes('tenant_admin') || session?.roles?.includes('account_admin');
  // Task Runner executes workflow tasks. Reachable by built-in admins, or by a
  // dynamic-RBAC holder of workflows:Execute (or workflows:Write, which covers it).
  const canRunTasks = isAdmin || hasPermission('workflows', 'Execute') || hasPermission('workflows', 'Write');

  const [selectedFilter, setSelectedFilter] = React.useState(0);

  // `value` and the render block below are index-based and must stay in step:
  // reordering these entries reorders the tabs, so every value/render pair has
  // to move together. Automations stays at 0 — selectedFilter defaults to it.
  const filterOptions = [
    { name: 'Automations', id: 'automations', value: 0, fragment: 'automations', icon: AutomateBlue },
    { name: 'Executions', id: 'executions', value: 1, fragment: 'executions', icon: dashboardIcon1 },
    {
      name: 'Task Runner',
      id: 'task-runner',
      value: 2,
      fragment: 'task-runner',
      disabled: !canRunTasks,
      disabledTooltip: missingPermissionMessage('workflows:Execute'),
      icon: PlayCircleIcon,
    },
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
        filterOptions={filterOptions}
        onChangeFilter={(val) => {
          // AnchorComponent resolves the tab from the URL hash without consulting
          // `disabled`, so a deep link to #task-runner would select an unreachable
          // tab and render nothing. Ignore those selections here.
          if (filterOptions.find((opt) => opt.value === val)?.disabled) return;
          setSelectedFilter(val);
        }}
      />
      <Box sx={{ position: 'relative', mt: 3 }}>
        <ErrorBoundary key={selectedFilter}>
          {/* Tenant-level: each tab resolves its own accounts. The listing and
              Executions carry an Account filter; Task Runner an account picker. */}
          {selectedFilter === 0 && <WorkflowListing />}
          {selectedFilter === 1 && <ExecutionDashboard />}
          {selectedFilter === 2 && canRunTasks && <TaskRunner />}
        </ErrorBoundary>
      </Box>
    </>
  );
};

export default withAccountGuard(Automation, { hideContent: true });
