import { useState, useEffect, useMemo } from 'react';
import homeApi from '@api1/home'; // Adjust imports

export const useClusterInsights = (accountId) => {
  const [insightData, setInsightData] = useState([]);

  useEffect(() => {
    // Cleared before the early return so switching to a falsy account drops the
    // previous account's insights instead of leaving them on screen.
    setInsightData([]);
    if (!accountId) {
      return;
    }
    let cancelled = false;
    homeApi.getInsights(accountId).then((res) => {
      if (cancelled) return;
      setInsightData(res?.data?.data?.insights_list?.rows || []);
    });
    return () => {
      cancelled = true;
    };
  }, [accountId]);

  const troubleShootData = useMemo(() => insightData.filter((o) => o.type === 'Troubleshooting'), [insightData]);
  const optimizationData = useMemo(() => insightData.filter((o) => o.type === 'Optimization'), [insightData]);

  return { troubleShootData, optimizationData };
};
