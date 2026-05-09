import { Box, Grid } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi from '@api1/recommendation';
import Currency from '@components1/common/format/Currency';
import SummaryWidget from './SummaryWidget';
import PropTypes from 'prop-types';

// Move static configurations outside to prevent recreation on every render
const K8S_PARAMS = {
  category: ['RightSizing', 'K8sSpotRecommendation'],
  ruleName: ['pod_right_sizing', 'unused_pvc', 'abandoned_resource', 'replica_right_sizing', 'pv_rightsize', 'Spot instance recommendation'],
  status: ['Open', 'InProgress'],
};

const CLOUD_PARAMS = {
  category: ['RightSizing', 'InfraUpgrade'],
  serviceName: [
    'AmazonEC2',
    'AmazonRDS',
    'microsoft.compute/disks',
    'microsoft.compute/virtualmachines',
    'microsoft.compute/virtualmachinescalesets',
    'microsoft.sql/servers',
    'microsoft.storage/storageaccounts',
    'Compute Engine',
    'Cloud SQL',
    'Cloud Storage',
  ],
  status: ['Open', 'InProgress'],
};

const OptimizationSummary = ({ accountId }) => {
  const [loading, setLoading] = useState(true);
  const [summaryData, setSummaryData] = useState({
    totalRecommendations: 0,
    totalActionables: 0,
    monthlySavings: 0,
    yearlySavings: 0,
    nudgebeeSavings: 0,
  });

  useEffect(() => {
    let isMounted = true;

    const fetchAllSummaryData = async () => {
      setLoading(true);
      try {
        // Run requests in parallel to fix race condition and improve speed
        const [k8sResponse, cloudResponse] = await Promise.all([
          recommendationApi.getK8sRecommendationSummaryByRuleName(K8S_PARAMS),
          recommendationApi.getK8sRecommendationSummaryByRuleName(CLOUD_PARAMS),
        ]);

        if (!isMounted) {
          return;
        }

        // Helper to sum up counts and savings safely
        const aggregateData = (data) => {
          if (!data || !Array.isArray(data)) {
            return { count: 0, savings: 0 };
          }
          return data.reduce(
            (acc, item) => ({
              count: acc.count + (item.count || 0),
              savings: acc.savings + (item.sum_estimated_savings || 0),
            }),
            { count: 0, savings: 0 }
          );
        };

        const k8sTotals = aggregateData(k8sResponse);
        const cloudTotals = aggregateData(cloudResponse);

        const totalMonthlySavings = k8sTotals.savings + cloudTotals.savings;

        // Single atomic update combining both sources
        setSummaryData({
          totalRecommendations: k8sTotals.count + cloudTotals.count,
          totalActionables: 0,
          monthlySavings: totalMonthlySavings,
          yearlySavings: totalMonthlySavings * 12,
          nudgebeeSavings: 0,
        });
      } catch (error) {
        console.error('Failed to fetch optimization summary:', error);
      } finally {
        if (isMounted) {
          setLoading(false);
        }
      }
    };

    fetchAllSummaryData();

    // Cleanup function to prevent state updates if component unmounts
    return () => {
      isMounted = false;
    };
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ padding: '20px 32px 0 32px' }}>
        <Grid container spacing={2}>
          {[1, 2, 3, 4].map((i) => (
            <Grid item xs={12} sm={6} md={3} key={i}>
              <Box
                sx={{
                  height: '120px',
                  borderRadius: '12px',
                  backgroundColor: '#f5f5f5',
                  // Pulse animation
                  animation: 'pulse 1.5s ease-in-out infinite',
                  '@keyframes pulse': {
                    '0%, 100%': { opacity: 1 },
                    '50%': { opacity: 0.5 },
                  },
                }}
              />
            </Grid>
          ))}
        </Grid>
      </Box>
    );
  }

  return (
    <Box sx={{ padding: '20px 0px' }}>
      <Grid container spacing={2}>
        {/* Total Recommendations */}
        <Grid item xs={12} sm={6} md={3}>
          <SummaryWidget title='Total Recommendations' value={summaryData.totalRecommendations} variant='default' />
        </Grid>
        {/* Total Estimated Savings */}
        <Grid item xs={12} sm={6} md={3}>
          <SummaryWidget
            title='Total Estimated Savings'
            value={
              <Box sx={{ display: 'flex', flexDirection: 'row', gap: '40px' }}>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '4px' }}>
                  <Currency
                    value={summaryData.monthlySavings}
                    precision={0}
                    showSymbol
                    withTooltip={false}
                    sx={{ fontSize: '32px', lineHeight: '32px', fontWeight: 600 }}
                  />
                  <Box sx={{ color: '#717886', fontSize: '16px', fontWeight: 400 }}>/mo</Box>
                </Box>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '4px' }}>
                  <Currency
                    value={summaryData.yearlySavings}
                    precision={0}
                    showSymbol
                    withTooltip={false}
                    sx={{ fontSize: '32px', lineHeight: '32px', fontWeight: 600 }}
                  />
                  <Box sx={{ color: '#717886', fontSize: '14px', fontWeight: 400 }}>/yr</Box>
                </Box>
              </Box>
            }
            variant='savings'
          />
        </Grid>
      </Grid>
    </Box>
  );
};

OptimizationSummary.propTypes = {
  accountId: PropTypes.string,
};

export default OptimizationSummary;
