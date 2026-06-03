import { useState, useEffect, useMemo } from 'react';
import homeApi from '@api1/home'; // Adjust imports

export const useClusterInsights = (accountId) => {
  const [insightData, setInsightData] = useState([]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setInsightData([]);
    homeApi.getInsights(accountId).then((res) => {
      setInsightData(res?.data?.data?.insights_list?.rows || []);
    });
  }, [accountId]);

  const troubleShootData = useMemo(() => insightData.filter((o) => o.type === 'Troubleshooting'), [insightData]);
  const optimizationData = useMemo(() => insightData.filter((o) => o.type === 'Optimization'), [insightData]);

  return { troubleShootData, optimizationData };
};
