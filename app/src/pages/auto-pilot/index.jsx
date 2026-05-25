import React, { useEffect } from 'react';
import { Box } from '@mui/material';
import AnchorComponent from '@components1/common/AnchorComponent';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import { useRouter } from 'next/router';
import { hasWriteAccess } from '@lib/auth';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { Button as DsButton } from '@components1/ds/Button';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { BetaIcon } from '@assets';

import AutoOptimizeTabs from '@components1/autopilot/tables/AutoOptimizeTabs';
import WorkflowListing from '@components1/workflow/WorkflowListing';
import SafeIcon from '@components1/common/SafeIcon';

const AutoPilot = () => {
  const router = useRouter();

  // 1. Initialize state with defaults (0) instead of router.query
  const [selectedFilter, setSelectedFilter] = React.useState(0);
  const [subTab, setSubTab] = React.useState(0);
  const [openCreateAutoOptimize, setOpenCreateAutoOptimize] = React.useState(false);
  const [openCreateAutoOptimizeType, setOpenCreateAutoOptimizeType] = React.useState(null);

  const filterOptions = [
    {
      name: 'Automation',
      value: 0,
      disabled: false,
      fragment: 'workflow',
    },
    {
      name: 'Auto Optimize',
      value: 1,
      fragment: 'auto-optimize',
      disabled: false,
      tabOptions: [
        { id: 'Optimizations', text: 'Optimizations', value: 0, fragment: 'optimizations' },
        { id: 'approvals', text: 'Approvals', value: 1, fragment: 'approvals' },
      ],
    },
  ];

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) return;
    const [fragment, subFragment] = hash.split('/');
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setSelectedFilter(filter.value);
      if (!subFragment) return;
      const subTab = (filter?.tabOptions || []).find((tab) => tab.fragment === subFragment);
      if (subTab) {
        setSubTab(subTab.value);
      }
    }
  }, []);

  const handleOpenCreateAutoOptimize = (type) => {
    setOpenCreateAutoOptimizeType(type);
    setOpenCreateAutoOptimize(true);
  };

  const handleCloseCreateAutoOptimize = () => {
    setOpenCreateAutoOptimizeType('');
    setOpenCreateAutoOptimize(false);
  };

  const getAnchorComponent = () => {
    let buttonComponent = null;
    if (hasWriteAccess(router?.query?.accountId)) {
      if (selectedFilter === 1) {
        buttonComponent = (
          <DsDropdownMenu
            align='end'
            items={[
              {
                id: 'continuous_rightsize',
                label: (
                  <span style={{ display: 'flex', alignItems: 'center' }}>
                    Continuous Vertical Right Sizing
                    <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '5px' }} />
                  </span>
                ),
                onSelect: () => handleOpenCreateAutoOptimize('continuous_rightsize'),
              },
              { id: 'horizontal_rightsize', label: 'Horizontal Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('horizontal_rightsize') },
              {
                id: 'vertical_rightsize',
                label: 'Scheduled Vertical Right Sizing',
                onSelect: () => handleOpenCreateAutoOptimize('vertical_rightsize'),
              },
              { id: 'pvc_rightsize', label: 'PVC Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('pvc_rightsize') },
            ]}
            trigger={
              <DsButton id='create-auto-optimize' tone='primary' size='md' composition='text+icon' icon={<KeyboardArrowDownIcon fontSize='small' />}>
                Create Auto Optimize
              </DsButton>
            }
          />
        );
      }
    }

    let Anchor = (
      <AnchorComponent
        manageRoute={true}
        options={filterOptions[selectedFilter]?.options || []}
        filterOptions={filterOptions}
        // Updated Handler: Pushes new Hash URL instead of setting state directly
        onChangeFilter={(val, subVal) => {
          setSelectedFilter(val);
          setSubTab(subVal);
        }}
        buttonComponent={buttonComponent}
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

          <Box>
            {selectedFilter === 1 && (
              <AutoOptimizeTabs
                subTab={subTab}
                accountId={router?.query?.accountId}
                openCreateAutoOptimize={openCreateAutoOptimize}
                openCreateAutoOptimizeType={openCreateAutoOptimizeType}
                handleCloseCreateAutoOptimize={() => {
                  handleCloseCreateAutoOptimize();
                }}
                handleOpenCreateAutoOptimize={handleOpenCreateAutoOptimize}
              />
            )}
          </Box>
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default AutoPilot;
