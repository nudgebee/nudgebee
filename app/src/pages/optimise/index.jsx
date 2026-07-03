import { useState, useEffect, useMemo } from 'react';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import OptimizeNewPage from '@components/optimise-new/OptimizeNewPage';
import SummaryView from '@components/optimise-new/summary/SummaryView';
import ResolutionsView from '@components/optimise-new/ResolutionsView';
import CostAnalyser from '@components/llm/cost-analyser/CostAnalyser';
import { useRouter } from 'next/router';
import { OptimizeSummaryIcon, RecommendationIcon, RecommendationResolutionIcon, LLMConsumptionIcon } from '@assets';
import { hasReadAccess } from '@lib/auth';
import { useData } from '@context/DataContext';

export async function getServerSideProps() {
  return {
    props: {
      enableLlmAnalyser: process.env.UI_ENABLE_LLM_ANALYSER === 'true',
    },
  };
}

const Optimise = ({ enableLlmAnalyser }) => {
  const router = useRouter();
  const { selectedCluster } = useData();
  const [activeTab, setActiveTab] = useState(0);
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
        isMounted &&
          enableLlmAnalyser &&
          hasReadAccess(selectedCluster?.value) && {
            name: 'LLM Analyser',
            id: 'llm-analyser',
            fragment: 'cost-analyser',
            value: 3,
            icon: LLMConsumptionIcon,
            iconSize: 18,
          },
      ].filter(Boolean),
    [isMounted, enableLlmAnalyser, selectedCluster?.value]
  );

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) {
      setActiveTab(0);
      return;
    }
    const fragment = hash;
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setActiveTab(filter.value);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterOptions]);

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={filterOptions} onChangeFilter={(val) => setActiveTab(val)} />
      <ErrorBoundary key={activeTab}>
        {activeTab === 0 && <SummaryView />}
        {activeTab === 1 && <OptimizeNewPage />}
        {activeTab === 2 && <ResolutionsView />}
        {activeTab === 3 && <CostAnalyser />}
      </ErrorBoundary>
    </>
  );
};

export default Optimise;
