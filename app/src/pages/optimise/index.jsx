import { useState, useEffect } from 'react';
import AnchorComponent from '@components1/common/AnchorComponent';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import OptimizeNewPage from '@components1/optimise-new/OptimizeNewPage';
import SummaryView from '@components1/optimise-new/summary/SummaryView';
import { useRouter } from 'next/router';

const filterOptions = [
  { name: 'Summary', id: 'summary', fragment: 'summary', value: 0 },
  { name: 'Recommendations', id: 'recommendations', fragment: 'recommendations', value: 1 },
];

const Optimise = () => {
  const router = useRouter();
  const [activeTab, setActiveTab] = useState(0);

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
  }, []);

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={filterOptions} onChangeFilter={(val) => setActiveTab(val)} />
      <ErrorBoundary key={activeTab}>
        {activeTab === 0 && <SummaryView />}
        {activeTab === 1 && <OptimizeNewPage />}
      </ErrorBoundary>
    </>
  );
};

export default Optimise;
