import React from 'react';
import { Box, Typography, Divider } from '@mui/material';
import Currency from '@common-new/format/Currency';
import DSCard from '@components1/ds/Card';
import HeadingWithBorder from '@common-new/HeadingWithBorder';
import { Button as DSButton } from '@components1/ds/Button';
import { ExternalLinkIcon, OptimiseIconBlue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useData } from '@context/DataContext';
import { useRouter } from 'next/router';
import { Skeleton } from '@components1/ds/Skeleton';

const CustomOptimizationsSummaryCard = ({ clusterSummary, accountId, loading = false }) => {
  const { selectedCluster } = useData();
  const router = useRouter();

  const buildUrl = (selectedCluster, id, fragment, navigate, additionalQuery = {}) => {
    let route;
    if (navigate === 'details') {
      let base = selectedCluster?.cloud_provider === 'K8s' ? '/kubernetes/details' : '/cloud-account/details';
      let accountIdKey = selectedCluster?.cloud_provider === 'K8s' ? 'KubernetesDetails' : 'accountId';
      route = `${base}/${id}?${accountIdKey}=${id}`;
      if (additionalQuery?.aggregation_key) {
        for (const [key, value] of Object.entries(additionalQuery)) {
          route = `${route}&${key}=${value}`;
        }
      }
      route = `${route}#${fragment}`;
    } else if (navigate === 'auto-pilot') {
      route = `/auto-pilot?accountId=${id}`;
    }
    return route;
  };

  const getCurrentMonthName = () => {
    const date = new Date();
    const month = date.toLocaleString('default', { month: 'long' });
    return month;
  };
  return (
    <DSCard variant='accent' tone='success' size='md' sx={{ height: '-webkit-fill-available' }}>
      <Box display='flex' alignItems={'center'}>
        <HeadingWithBorder
          span={
            <DSButton
              tone='ghost'
              composition='icon-only'
              aria-label='Open optimizations'
              icon={<SafeIcon src={ExternalLinkIcon} alt='redirect' />}
              onClick={() => router.push(buildUrl(selectedCluster, accountId, 'optimize/right-sizing', 'details', {}))}
            />
          }
          value='Optimizations'
          borderColor='var(--ds-blue-500)'
          borderWidth='3px'
          sx={{ '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' } }}
        />
      </Box>
      {loading ? (
        <Skeleton shape='rect' height={40} />
      ) : (
        <Box display={'grid'} gridTemplateColumns={'1fr 1fr'}>
          <Box mt='20px'>
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                justifyContent: 'center',
                alignItems: 'start',
              }}
            >
              <Box>
                <Typography color='var(--ds-gray-600)' fontSize={'var(--ds-text-small)'} fontWeight={'var(--ds-font-weight-medium)'}>
                  Monthly Spend
                </Typography>

                <Currency
                  value={clusterSummary?.current_month_spend}
                  sx={{ color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-medium)' }}
                  sxPrefix={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                  withTooltip={false}
                  suffix={` in ${getCurrentMonthName()}`}
                />
              </Box>
            </Box>
          </Box>
          <Box sx={{ borderLeft: '1px solid var(--ds-gray-200)', mt: '20px' }}>
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                justifyContent: 'center',
                alignItems: 'start',
                ml: '10px',
              }}
            >
              <Box display='flex' alignItems={'center'}>
                <Typography color='var(--ds-gray-600)' fontSize={'var(--ds-text-small)'} fontWeight={'var(--ds-font-weight-regular)'} width={'70px'}>
                  Prev. Mo.
                </Typography>

                <Currency
                  value={clusterSummary?.last_month_spend}
                  sx={{ color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
                  sxPrefix={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                  withTooltip={false}
                />
              </Box>
              <Box display='flex' alignItems={'center'}>
                <Typography color='var(--ds-gray-600)' fontSize={'var(--ds-text-small)'} fontWeight={'var(--ds-font-weight-regular)'} width={'70px'}>
                  This. Mo.
                </Typography>

                <Currency
                  value={clusterSummary?.current_month_projected_spend}
                  sx={{ color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
                  sxPrefix={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                  sxSuffix={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                  withTooltip={false}
                  suffix='(est.)'
                />
              </Box>
            </Box>
          </Box>
        </Box>
      )}

      <Divider sx={{ backgroundColor: 'var(--ds-gray-200)', marginTop: '20px' }} />
      <Box mt='20px'>
        {loading ? (
          <Skeleton shape='rect' height={40} />
        ) : (
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              alignItems: 'start',
            }}
          >
            <Box>
              <Typography color='var(--ds-gray-600)' fontSize={'var(--ds-text-small)'} fontWeight={'var(--ds-font-weight-medium)'}>
                Savings Potential
              </Typography>
              <Box display={'flex'} gap={'25px'}>
                <Currency
                  value={clusterSummary?.yearly_recommendation_saving}
                  sx={{ color: 'var(--ds-green-500)', fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-medium)' }}
                  sxPrefix={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                  withTooltip={false}
                  suffix='/yr'
                  isSavingPotential={true}
                  recommendationLabel='Some of custom optimization recommendations'
                />
                <Box sx={{ borderLeft: '1px solid var(--ds-gray-200)' }}>
                  <Currency
                    value={clusterSummary?.recommended_saving}
                    sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-medium)' }}
                    sxPrefix={{
                      color: 'var(--ds-gray-500)',
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-regular)',
                      ml: '10px',
                    }}
                    withTooltip={false}
                    suffix='/mo'
                  />
                </Box>
              </Box>
            </Box>
          </Box>
        )}
      </Box>
      <Divider sx={{ backgroundColor: 'var(--ds-gray-200)', marginTop: '20px' }} />
      <Box mt='20px'>
        {loading ? (
          <Skeleton shape='rect' height={40} />
        ) : (
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-end',
              flexWrap: 'wrap',
              gap: '12px',
            }}
          >
            <Box>
              <Typography
                color='var(--ds-gray-700)'
                fontSize={'var(--ds-text-heading)'}
                fontWeight={'var(--ds-font-weight-medium)'}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  span: {
                    color: 'var(--ds-gray-600)',
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    pl: '10px',
                  },
                }}
              >
                {clusterSummary?.total_recommendations}
                <span>Cost optimizations available</span>
              </Typography>
              <Box display={'flex'} gap={'25px'}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: 'var(--ds-gray-500)',
                    '& span': {
                      fontSize: 'var(--ds-text-caption)',
                      color: 'var(--ds-green-500)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                    },
                  }}
                />
              </Box>
            </Box>
            <DSButton
              tone='secondary'
              size='sm'
              icon={<SafeIcon src={OptimiseIconBlue} alt='optimize icon' />}
              onClick={() => {
                router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#optimize/summary`);
              }}
            >
              Optimize Now
            </DSButton>
          </Box>
        )}
      </Box>
    </DSCard>
  );
};

export default CustomOptimizationsSummaryCard;
