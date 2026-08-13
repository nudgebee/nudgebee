import { useState, useEffect, useMemo } from 'react';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import OptimizeNewPage from '@components/optimise-new/OptimizeNewPage';
import SummaryView from '@components/optimise-new/summary/SummaryView';
import ResolutionsView from '@components/optimise-new/ResolutionsView';
import CostAnalyser from '@components/llm/cost-analyser/CostAnalyser';
import GatewayUsage from '@components/llm/gateway-usage/GatewayUsage';
import AutoOptimizeTabs from '@components/autopilot/tables/AutoOptimizeTabs';
import { useRouter } from 'next/router';
import {
  OptimizeSummaryIcon,
  RecommendationIcon,
  RecommendationResolutionIcon,
  LLMConsumptionIcon,
  IntegrationsIcon,
  AutomateBlue,
  BetaIcon,
} from '@assets';
import { hasReadAccess, hasWriteAccess } from '@lib/auth';
import { useData } from '@context/DataContext';
import { DropdownMenu as DsDropdownMenu } from '@ui/DropdownMenu';
import { Button as DsButton } from '@ui/Button';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import SafeIcon from '@shared/icons/SafeIcon';
import { ds } from '@utils/colors';

export async function getServerSideProps() {
  return {
    props: {
      enableLlmAnalyser: process.env.UI_ENABLE_LLM_ANALYSER === 'true',
      enableLlmGateway: process.env.UI_ENABLE_LLM_GATEWAY === 'true',
      // Public base URL of the AI Gateway, surfaced to the Connect tab's setup
      // snippets. Empty when unset — the Connect tab shows a "not configured" state.
      llmGatewayUrl: process.env.LLM_GATEWAY_PUBLIC_URL || '',
    },
  };
}

const Optimise = ({ enableLlmAnalyser, enableLlmGateway, llmGatewayUrl }) => {
  const router = useRouter();
  const { selectedCluster } = useData();
  const [activeTab, setActiveTab] = useState(null);
  const [subTab, setSubTab] = useState(0);
  const [openCreateAutoOptimize, setOpenCreateAutoOptimize] = useState(false);
  const [openCreateAutoOptimizeType, setOpenCreateAutoOptimizeType] = useState(null);
  // Gate the admin-only tab on mount so the first client render matches the
  // server HTML (hasReadAccess reads a client-populated session) — avoids any
  // hydration mismatch; the tab resolves on the next tick.
  const [isMounted, setIsMounted] = useState(false);

  useEffect(() => {
    setIsMounted(true);
  }, []);

  // Show the LLM Analyser to anyone with read access to the account in scope —
  // tenant admins (read/write), account admins, and namespace admins all pass,
  // matching the backend authorization on the `ai_*` cost actions. `isTenantAdmin`
  // was too strict and hid the tab from account admins (#33341). Still gated by
  // the UI_ENABLE_LLM_ANALYSER feature flag.
  const filterOptions = useMemo(
    () =>
      [
        { name: 'Summary', id: 'summary', fragment: 'summary', value: 0, icon: OptimizeSummaryIcon },
        { name: 'Recommendations', id: 'recommendations', fragment: 'recommendations', value: 1, icon: RecommendationIcon, iconSize: 18 },
        { name: 'Resolutions', id: 'resolutions', fragment: 'resolutions', value: 2, icon: RecommendationResolutionIcon, iconSize: 18 },
        {
          name: 'Auto Optimize',
          id: 'auto-optimize',
          fragment: 'auto-optimize',
          value: 3,
          icon: AutomateBlue,
          tabOptions: [
            { id: 'Optimizations', text: 'Optimizations', value: 0, fragment: 'optimizations' },
            { id: 'approvals', text: 'Approvals', value: 1, fragment: 'approvals' },
          ],
        },
        // Auto Optimize stays at a fixed index 3 so it sits BEFORE this
        // feature-flagged tab. AnchorComponent renders sub-tabs via
        // filterOptions[activeDropdownTab] (index === value), so a tab with
        // tabOptions must keep value === array index regardless of the flag.
        isMounted &&
          enableLlmAnalyser &&
          hasReadAccess(selectedCluster?.value) && {
            name: 'LLM Analyser',
            id: 'llm-analyser',
            fragment: 'cost-analyser',
            value: 4,
            icon: LLMConsumptionIcon,
            iconSize: 18,
          },
        // AI Gateway usage — sibling analytics surface to the LLM Analyser, but over
        // all BYO-token traffic forwarded through the gateway (its own query API), not
        // agent conversations. Independently flag-gated for staged rollout. No
        // tabOptions, so its value need not equal its array index (see note above).
        isMounted &&
          enableLlmGateway &&
          hasReadAccess(selectedCluster?.value) && {
            name: 'AI Gateway',
            id: 'ai-gateway',
            fragment: 'ai-gateway',
            value: 5,
            icon: IntegrationsIcon,
            iconSize: 18,
          },
      ].filter(Boolean),
    [isMounted, enableLlmAnalyser, enableLlmGateway, selectedCluster?.value]
  );

  useEffect(() => {
    if (!router.isReady) return;
    const hash = router.asPath.split('#')[1];
    // Per-recommendation notification links (?id=) historically targeted #summary,
    // which ignores the id. Route them to the Recommendations tab, whose detail
    // panel opens the linked recommendation.
    const hasRecDeepLink = typeof router.query.id === 'string' && router.query.id;
    if (hasRecDeepLink && (!hash || hash === 'summary')) {
      const recommendationsTab = filterOptions.find((option) => option.fragment === 'recommendations');
      if (recommendationsTab) {
        setActiveTab(recommendationsTab.value);
        return;
      }
    }
    if (!hash || !filterOptions.length) {
      setActiveTab(0);
      return;
    }
    const [fragment, subFragment] = hash.split('/');
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (!filter) {
      setActiveTab(0);
      return;
    }
    setActiveTab(filter.value);
    if (subFragment && filter.tabOptions) {
      const sub = filter.tabOptions.find((tab) => tab.fragment === subFragment);
      if (sub) {
        setSubTab(sub.value);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterOptions, router.query.id, router.isReady]);

  const handleOpenCreateAutoOptimize = (type) => {
    setOpenCreateAutoOptimizeType(type);
    setOpenCreateAutoOptimize(true);
  };

  const handleCloseCreateAutoOptimize = () => {
    setOpenCreateAutoOptimizeType('');
    setOpenCreateAutoOptimize(false);
  };

  const createAutoOptimizeButton =
    activeTab === 3 && hasWriteAccess(router?.query?.accountId) ? (
      <DsDropdownMenu
        align='end'
        disablePortal={false}
        items={[
          {
            id: 'continuous_rightsize',
            label: (
              <span style={{ display: 'flex', alignItems: 'center' }}>
                Continuous Vertical Right Sizing
                <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: ds.space[1] }} />
              </span>
            ),
            onSelect: () => handleOpenCreateAutoOptimize('continuous_rightsize'),
          },
          { id: 'horizontal_rightsize', label: 'Horizontal Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('horizontal_rightsize') },
          { id: 'vertical_rightsize', label: 'Scheduled Vertical Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('vertical_rightsize') },
          { id: 'pvc_rightsize', label: 'PVC Right Sizing', onSelect: () => handleOpenCreateAutoOptimize('pvc_rightsize') },
        ]}
        trigger={
          <DsButton id='create-auto-optimize' tone='primary' size='md' composition='text+icon' icon={<KeyboardArrowDownIcon fontSize='small' />}>
            Create Auto Optimize
          </DsButton>
        }
      />
    ) : null;

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        filterOptions={filterOptions}
        onChangeFilter={(val, subVal) => {
          setActiveTab(val);
          setSubTab(subVal || 0);
        }}
        buttonComponent={createAutoOptimizeButton}
      />
      {activeTab !== null && (
        <ErrorBoundary key={activeTab}>
          {activeTab === 0 && <SummaryView />}
          {activeTab === 1 && <OptimizeNewPage />}
          {activeTab === 2 && <ResolutionsView />}
          {activeTab === 3 && (
            <AutoOptimizeTabs
              subTab={subTab}
              openCreateAutoOptimize={openCreateAutoOptimize}
              openCreateAutoOptimizeType={openCreateAutoOptimizeType}
              handleOpenCreateAutoOptimize={handleOpenCreateAutoOptimize}
              handleCloseCreateAutoOptimize={handleCloseCreateAutoOptimize}
            />
          )}
          {activeTab === 4 && <CostAnalyser />}
          {activeTab === 5 && <GatewayUsage gatewayUrl={llmGatewayUrl} />}
        </ErrorBoundary>
      )}
    </>
  );
};

export default Optimise;
