import React, { useEffect, useMemo } from 'react';
import { Box } from '@mui/material';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import PermissionDeniedContent from '@components/common/navigation/PermissionDeniedContent';
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
  // Automations/Executions are read-only listings. Matches the sidebar's own
  // gate on this nav item (module: 'workflows', layout/index.jsx) -- without
  // this, a user the sidebar hides Automations from could still reach these
  // two tabs by typing the URL, since nothing here re-checked workflows:Read.
  const canReadWorkflows =
    isAdmin || hasPermission('workflows', 'Read') || hasPermission('workflows', 'Execute') || hasPermission('workflows', 'Write');

  const [selectedFilter, setSelectedFilter] = React.useState(0);

  // `value` and the render block below are index-based and must stay in step:
  // reordering these entries reorders the tabs, so every value/render pair has
  // to move together. Automations stays at 0 — selectedFilter defaults to it.
  const filterOptions = useMemo(
    () => [
      {
        name: 'Automations',
        id: 'automations',
        value: 0,
        fragment: 'automations',
        icon: AutomateBlue,
        disabled: !canReadWorkflows,
        disabledTooltip: missingPermissionMessage('workflows:Read'),
      },
      {
        name: 'Executions',
        id: 'executions',
        value: 1,
        fragment: 'executions',
        icon: dashboardIcon1,
        disabled: !canReadWorkflows,
        disabledTooltip: missingPermissionMessage('workflows:Read'),
      },
      {
        name: 'Task Runner',
        id: 'task-runner',
        value: 2,
        fragment: 'task-runner',
        disabled: !canRunTasks,
        disabledTooltip: missingPermissionMessage('workflows:Execute'),
        icon: PlayCircleIcon,
      },
    ],
    [canReadWorkflows, canRunTasks]
  );

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) return;
    const [fragment] = hash.split('/');
    // Match regardless of disabled -- a deep link to #task-runner still
    // highlights Task Runner and settles here, with a permission-denied
    // placeholder rendered in its content area below instead of this effect
    // silently ignoring the request.
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setSelectedFilter(filter.value);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.asPath, router.isReady]);

  const activeOption = filterOptions[selectedFilter];

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={filterOptions} onChangeFilter={(val) => setSelectedFilter(val)} />
      <Box sx={{ position: 'relative', mt: 3 }}>
        <ErrorBoundary key={selectedFilter}>
          {activeOption?.disabled && <PermissionDeniedContent message={activeOption.disabledTooltip} />}
          {/* Tenant-level: each tab resolves its own accounts. The listing and
              Executions carry an Account filter; Task Runner an account picker. */}
          {selectedFilter === 0 && !activeOption?.disabled && <WorkflowListing />}
          {selectedFilter === 1 && !activeOption?.disabled && <ExecutionDashboard />}
          {selectedFilter === 2 && !activeOption?.disabled && <TaskRunner />}
        </ErrorBoundary>
      </Box>
    </>
  );
};

export default withAccountGuard(Automation, { hideContent: true });
