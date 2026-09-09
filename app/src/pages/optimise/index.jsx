import { useState, useEffect, useMemo } from 'react';
import dynamic from 'next/dynamic';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import SummaryView from '@components/optimise-new/summary/SummaryView';
import { useRouter } from 'next/router';
import {
  OptimizeSummaryIcon,
  DollarIcon,
  RecommendationResolutionIcon,
  SecuritytoolsBlue,
  ToolIconBlue,
  LLMConsumptionIcon,
  IntegrationsIcon,
  AutomateBlue,
  BetaIcon,
} from '@assets';
import { hasFeatureAccess, hasPermission, hasReadAccess, hasWriteAccess, withAuth } from '@lib/auth';
import { useData } from '@context/DataContext';
import { DropdownMenu as DsDropdownMenu } from '@ui/DropdownMenu';
import { Button as DsButton } from '@ui/Button';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import SafeIcon from '@shared/icons/SafeIcon';
import { ds } from '@utils/colors';

// Only one tab is visible at a time; lazy-load the rest to cut initial JS.
const OptimizeNewPage = dynamic(() => import('@components/optimise-new/OptimizeNewPage'), { ssr: false });
const ResolutionsView = dynamic(() => import('@components/optimise-new/ResolutionsView'), { ssr: false });
const SecurityView = dynamic(() => import('@components/optimise-new/SecurityView'), { ssr: false });
const AutoOptimizeTabs = dynamic(() => import('@components/autopilot/tables/AutoOptimizeTabs'), { ssr: false });
const CostAnalyser = dynamic(() => import('@components/llm/cost-analyser/CostAnalyser'), { ssr: false });
const GatewayUsage = dynamic(() => import('@components/llm/gateway-usage/GatewayUsage'), { ssr: false });

export async function getServerSideProps() {
  return {
    props: {
      enableLlmGateway: process.env.UI_ENABLE_LLM_GATEWAY === 'true',
      // Public base URL of the AI Gateway, surfaced to the Connect tab's setup
      // snippets. Empty when unset — the Connect tab shows a "not configured" state.
      llmGatewayUrl: process.env.LLM_GATEWAY_PUBLIC_URL || '',
    },
  };
}

// Filter state OptimizeNewPage syncs to the URL (updateUrl). The Cost and
// Configuration tabs are the same component reading these params at mount, so
// a search applied on one tab would otherwise carry into the other — e.g.
// ?category=Configuration&search=… written by the Configuration tab turns the
// Cost tab into a second Configuration list. Dropped from tab links so every
// tab switch starts from that tab's own defaults.
const TAB_SCOPED_FILTER_PARAMS = ['category', 'search', 'severity', 'account', 'safety', 'rules', 'status', 'savings', 'seen'];

const Optimise = ({ enableLlmGateway, llmGatewayUrl }) => {
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
  // LLM_ANALYSER is a per-tenant feature flag (not a deployment env var), so it
  // can only be resolved client-side via hasFeatureAccess — same pattern as
  // UPGRADE_PLANNER.
  const [llmAnalyserEnabled, setLlmAnalyserEnabled] = useState(false);

  useEffect(() => {
    setIsMounted(true);
  }, []);

  useEffect(() => {
    hasFeatureAccess('LLM_ANALYSER')
      .then(setLlmAnalyserEnabled)
      .catch(() => setLlmAnalyserEnabled(false));
  }, []);

  // Show the LLM Analyser to anyone with read access to the account in scope —
  // tenant admins (read/write), account admins, and namespace admins all pass,
  // matching the backend authorization on the `ai_*` cost actions. `isTenantAdmin`
  // was too strict and hid the tab from account admins (#33341). Still gated by
  // the LLM_ANALYSER tenant feature flag.
  const filterOptions = useMemo(
    () =>
      [
        { name: 'Summary', id: 'summary', fragment: 'summary', value: 0, icon: OptimizeSummaryIcon },
        // Labelled "Cost" but keyed 'recommendations': the fragment is the deep-link
        // contract every notification, the FinOps agent prompt and the apply CTA
        // already write, and it is independent of what the strip displays.
        { name: 'Cost', id: 'recommendations', fragment: 'recommendations', value: 1, icon: DollarIcon, iconSize: 18 },
        { name: 'Configuration', id: 'configuration', fragment: 'configuration', value: 2, icon: ToolIconBlue, iconSize: 18 },
        {
          name: 'Security',
          id: 'security',
          fragment: 'security',
          value: 3,
          icon: SecuritytoolsBlue,
          tabOptions: [
            { id: 'image-scan', text: 'Image Scan', value: 0, fragment: 'image-scan' },
            { id: 'cis-scan', text: 'CIS Scan', value: 1, fragment: 'cis-scan' },
            { id: 'vm-vulnerabilities', text: 'VM Vulnerabilities', value: 2, fragment: 'vm-vulnerabilities' },
            { id: 'cloud-posture', text: 'Cloud Posture', value: 3, fragment: 'cloud-posture' },
          ],
        },
        // Resolutions sits after the three finding tabs because it is the record of
        // what was already actioned, not another list to triage.
        { name: 'Resolutions', id: 'resolutions', fragment: 'resolutions', value: 4, icon: RecommendationResolutionIcon, iconSize: 18 },
        {
          name: 'Auto Optimize',
          id: 'auto-optimize',
          fragment: 'auto-optimize',
          value: 5,
          icon: AutomateBlue,
          tabOptions: [
            { id: 'Optimizations', text: 'Optimizations', value: 0, fragment: 'optimizations' },
            { id: 'approvals', text: 'Approvals', value: 1, fragment: 'approvals' },
          ],
        },
        // Auto Optimize stays at a fixed index 5 so it sits BEFORE this
        // feature-flagged tab. AnchorComponent renders sub-tabs via
        // filterOptions[activeDropdownTab] (index === value), so a tab with
        // tabOptions (Security and Auto Optimize above) must keep
        // value === array index regardless of the flag.
        isMounted &&
          llmAnalyserEnabled &&
          hasReadAccess(selectedCluster?.value) && {
            name: 'LLM Analyser',
            id: 'llm-analyser',
            fragment: 'cost-analyser',
            value: 6,
            icon: LLMConsumptionIcon,
            iconSize: 18,
          },
        // AI Gateway usage — sibling analytics surface to the LLM Analyser, but over
        // all BYO-token traffic forwarded through the gateway (its own query API), not
        // agent conversations. Independently flag-gated for staged rollout. No
        // tabOptions, so its value need not equal its array index (see note above).
        //
        // Unlike the LLM Analyser above, this tab's actions (`llm_gateway_*`) are
        // TENANT-scoped — they classify to the `llm` module and their handlers take
        // no account id. So an `llm:Read` custom grant is a legitimate way to reach
        // it, and hasReadAccess alone could never admit one: a grants-only holder
        // carries no account ids in the session, so the tab vanished for precisely
        // the users an admin had granted it to.
        isMounted &&
          enableLlmGateway &&
          (hasReadAccess(selectedCluster?.value) || hasPermission('llm', 'Read')) && {
            name: 'AI Gateway',
            id: 'ai-gateway',
            fragment: 'ai-gateway',
            value: 7,
            icon: IntegrationsIcon,
            iconSize: 18,
          },
      ].filter(Boolean),
    [isMounted, llmAnalyserEnabled, enableLlmGateway, selectedCluster?.value]
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
    // Configuration used to be a card on the Recommendations tab, so shared and
    // bookmarked links still carry ?category=Configuration#recommendations. That
    // tab no longer queries the category, so honouring the hash would land the
    // reader on an empty list; send them to the tab that now owns it.
    const categoryParam = router.query.category;
    const wantsConfiguration = Array.isArray(categoryParam) ? categoryParam.includes('Configuration') : categoryParam === 'Configuration';
    if (hash.split('/')[0] === 'recommendations' && wantsConfiguration) {
      const configurationTab = filterOptions.find((option) => option.fragment === 'configuration');
      if (configurationTab) {
        // Rewrite the URL rather than only moving activeTab: the tab strip parses
        // the hash itself, so leaving #recommendations there would render the
        // Configuration list under a highlighted Recommendations tab. Dropping
        // the now-redundant category param keeps the link shareable.
        const { category: _legacyCategory, ...query } = router.query;
        router.replace({ pathname: router.pathname, query, hash: 'configuration' }, undefined, { shallow: true });
        setActiveTab(configurationTab.value);
        return;
      }
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
    activeTab === 5 && hasWriteAccess(router?.query?.accountId) ? (
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
        disableHoverSubmenu
        filterOptions={filterOptions}
        tabScopedQueryParams={TAB_SCOPED_FILTER_PARAMS}
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
          {activeTab === 2 && <OptimizeNewPage lockedCategory='Configuration' />}
          {activeTab === 3 && <SecurityView subTab={subTab} />}
          {activeTab === 4 && <ResolutionsView />}
          {activeTab === 5 && (
            <AutoOptimizeTabs
              subTab={subTab}
              openCreateAutoOptimize={openCreateAutoOptimize}
              openCreateAutoOptimizeType={openCreateAutoOptimizeType}
              handleOpenCreateAutoOptimize={handleOpenCreateAutoOptimize}
              handleCloseCreateAutoOptimize={handleCloseCreateAutoOptimize}
            />
          )}
          {activeTab === 6 && <CostAnalyser />}
          {activeTab === 7 && <GatewayUsage gatewayUrl={llmGatewayUrl} />}
        </ErrorBoundary>
      )}
    </>
  );
};

export default withAuth(Optimise);
