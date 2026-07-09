import React, { useEffect } from 'react';
import { Box } from '@mui/material';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import { withAccountGuard } from '@shared/AccountGuard';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useRouter } from 'next/router';
import { getUserSession } from '@lib/auth';
import { WorkflowIconBlue, PlayCircleIcon } from '@assets';
import WorkflowListing from '@components/workflow/WorkflowListing';
import TaskRunner from '@components/workflow/TaskRunner';

const Automation = () => {
  const router = useRouter();
  const session = getUserSession();
  const isAdmin = session?.roles?.includes('tenant_admin') || session?.roles?.includes('account_admin');

  // 1. Initialize state with defaults (0) instead of router.query
  const [selectedFilter, setSelectedFilter] = React.useState(null);
  const [subTab, setSubTab] = React.useState(0);

  const filterOptions = [
    {
      name: 'Automation',
      value: 0,
      disabled: false,
      fragment: 'workflow',
      icon: WorkflowIconBlue,
    },
    {
      name: 'Task Runner',
      value: 2,
      fragment: 'task-runner',
      disabled: !isAdmin,
      icon: PlayCircleIcon,
    },
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
    );
    return Anchor;
  };

  return (
    <>
      {getAnchorComponent()}

      <ErrorBoundary key={`${selectedFilter}-${subTab}`}>
        <Box>
          <Box>{selectedFilter === 0 && <WorkflowListing accountId={router?.query?.accountId} />}</Box>

          <Box>{selectedFilter === 2 && isAdmin && <TaskRunner accountId={router?.query?.accountId} />}</Box>
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default withAccountGuard(Automation, { hideContent: true });
