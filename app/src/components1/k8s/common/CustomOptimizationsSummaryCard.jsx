import React from 'react';
import { Box, Typography, Divider } from '@mui/material';
import Currency from '@components1/common/format/Currency';
import CustomBorderCard from '@components1/common/CustomBorderCard';
import TextWithBorder from '@components1/common/TextWithBorder';
import CustomIconButton from '@components1/CustomIconButton';
import { ExternalLinkIcon, OptimiseIconBlue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useData } from '@context/DataContext';
import { useRouter } from 'next/router';
import CustomButton from '@components1/common/NewCustomButton';
import ShimmerLoading from '@components1/common/ShimmerLoading';

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
    <CustomBorderCard padding='20px 24px' borderLeftColor={'#BBF7D0'} borderColor='transparent' sx={{ height: '-webkit-fill-available' }}>
      <Box display='flex' alignItems={'center'}>
        <TextWithBorder
          span={
            <CustomIconButton onClick={() => router.push(buildUrl(selectedCluster, accountId, 'optimize/right-sizing', 'details', {}))}>
              <SafeIcon src={ExternalLinkIcon} alt='redirect' />
            </CustomIconButton>
          }
          value='Optimizations'
          borderColor='#3B82F6'
          borderWidth='3px'
          sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: '#374151' } }}
        />
      </Box>
      <ShimmerLoading isLoading={loading} height='40px'>
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
                <Typography color='#737373' fontSize={'12px'} fontWeight={500}>
                  Monthly Spend
                </Typography>

                <Currency
                  value={clusterSummary?.current_month_spend}
                  sx={{ color: '#374151', fontSize: '20px', fontWeight: 500 }}
                  sxPrefix={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400 }}
                  withTooltip={false}
                  suffix={` in ${getCurrentMonthName()}`}
                />
              </Box>
            </Box>
          </Box>
          <Box sx={{ borderLeft: '1px solid #EBEBEB', mt: '20px' }}>
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
                <Typography color='#737373' fontSize={'12px'} fontWeight={400} width={'70px'}>
                  Prev. Mo.
                </Typography>

                <Currency
                  value={clusterSummary?.last_month_spend}
                  sx={{ color: '#374151', fontSize: '12px', fontWeight: 500 }}
                  sxPrefix={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400 }}
                  withTooltip={false}
                />
              </Box>
              <Box display='flex' alignItems={'center'}>
                <Typography color='#737373' fontSize={'12px'} fontWeight={400} width={'70px'}>
                  This. Mo.
                </Typography>

                <Currency
                  value={clusterSummary?.current_month_projected_spend}
                  sx={{ color: '#374151', fontSize: '12px', fontWeight: 500 }}
                  sxPrefix={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400 }}
                  sxSuffix={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400 }}
                  withTooltip={false}
                  suffix='(est.)'
                />
              </Box>
            </Box>
          </Box>
        </Box>
      </ShimmerLoading>

      <Divider sx={{ backgroundColor: '#EBEBEB', marginTop: '20px' }} />
      <Box mt='20px'>
        <ShimmerLoading isLoading={loading} height='40px'>
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              alignItems: 'start',
            }}
          >
            <Box>
              <Typography color='#737373' fontSize={'12px'} fontWeight={500}>
                Savings Potential
              </Typography>
              <Box display={'flex'} gap={'25px'}>
                <Currency
                  value={clusterSummary?.yearly_recommendation_saving}
                  sx={{ color: '#22C55E', fontSize: '20px', fontWeight: 500 }}
                  sxPrefix={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400 }}
                  withTooltip={false}
                  suffix='/yr'
                  isSavingPotential={true}
                  recommendationLabel='Some of custom optimization recommendations'
                />
                <Box sx={{ borderLeft: '1px solid #EBEBEB' }}>
                  <Currency
                    value={clusterSummary?.recommended_saving}
                    sx={{ color: '#737373', fontSize: '20px', fontWeight: 500 }}
                    sxPrefix={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400, ml: '10px' }}
                    withTooltip={false}
                    suffix='/mo'
                  />
                </Box>
              </Box>
            </Box>
          </Box>
        </ShimmerLoading>
      </Box>
      <Divider sx={{ backgroundColor: '#EBEBEB', marginTop: '20px' }} />
      <Box mt='20px'>
        <ShimmerLoading isLoading={loading} height='40px'>
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
                color='#374151'
                fontSize={'20px'}
                fontWeight={500}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  span: {
                    color: '#737373',
                    fontSize: '12px',
                    fontWeight: '400',
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
                    fontSize: '10px',
                    fontWeight: 400,
                    color: '#9F9F9F',
                    '& span': {
                      fontSize: '10px',
                      color: '#22C55E',
                      fontWeight: 500,
                    },
                  }}
                />
              </Box>
            </Box>
            <CustomButton
              variant='tertiary'
              size='Small'
              text={'Optimize Now'}
              onClick={() => {
                router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#optimize/summary`);
              }}
              startIcon={<SafeIcon src={OptimiseIconBlue} alt='optimize icon' />}
            />
          </Box>
        </ShimmerLoading>
      </Box>
    </CustomBorderCard>
  );
};

export default CustomOptimizationsSummaryCard;
