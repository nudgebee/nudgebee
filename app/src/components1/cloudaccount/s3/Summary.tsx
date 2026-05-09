import React, { useEffect, useState } from 'react';
import { Box, Stack, Tooltip, Typography, useMediaQuery } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { useRouter } from 'next/router';
import Title from '@common/Title';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import TrendArrowPercentage from '@components1/common/widgets/TrendArrowPercentage';
import { formatMemory, formatNumber } from '@lib/formatter';
import DoughnutChartK8s from '@components1/common/charts/DoughnutChartK8s';
import Currency from '@components1/common/format/Currency';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import type { ICustomTable2Row } from './Instances';
import Text from '@components1/common/format/Text';
import Charts from '@components1/common/charts/LineCharts';
import { formatMetricName } from 'src/utils/common';
import { getLast7Days } from '@lib/datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import TotalCostChart from '@components1/cloudaccount/CostChart';

const EVENT_TABLE_ID = 'EVENT_TABLE_ID';
const EVENT_HEADERS = ['Instance ID', 'Events', 'Severity', 'Created at'];

const ClusterSummaryBlock = ({ children, sx }: any) => {
  return (
    <Box display='flex' flexDirection='column' justifyContent='flex-start'>
      <Box
        sx={{
          borderColor: 'rgba(255, 255, 255, 1)',
          backgroundColor: 'rgba(255, 255, 255, 1)',
          padding: '18px 24px 10px 24px',
          minHeight: '80px',
          borderRadius: '8px',
          marginTop: '10px',
          boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
          ...sx,
        }}
      >
        {children}
      </Box>
    </Box>
  );
};

const StackSummaryBlock = ({ title, children, sx }: any) => {
  return (
    <Stack>
      <Title title={title} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151', ...sx }} lightVariant={true} isUnderline={false} />
      <ClusterSummaryBlock
        title={title}
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: '',
          flexWrap: 'wrap',
          gap: '28px',
          padding: '5px 20px',
        }}
      >
        {children}
      </ClusterSummaryBlock>
    </Stack>
  );
};

const ClusterBlock = ({ cluster = {} }: any) => {
  return (
    <Box>
      <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'1px'}>
        {cluster.lable}
      </Typography>
      <Typography color='#374151' fontSize={'24px'} lineHeight={'36px'} fontWeight={600}>
        {formatNumber(cluster.count)}
      </Typography>
    </Box>
  );
};

const ClusterSummary = ({ _accountId, s3Summary = {} }: any) => {
  const buckets = s3Summary?.s3_count?.aggregate?.count || 0;
  const smallScreen = useMediaQuery('(max-width:1440px)');

  return (
    <Stack direction={'column'}>
      <Title title={'Summary'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
      <ClusterSummaryBlock
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: smallScreen ? 'space-between' : 'flex-start',
          flexWrap: 'wrap',
          gap: '15px',
        }}
      >
        <ClusterBlock cluster={{ lable: 'Total Buckets', count: buckets }} />
      </ClusterSummaryBlock>
    </Stack>
  );
};

const eventUrl = (accountId: string, serviceName: string) => {
  if (!accountId) return '';
  if (serviceName === 'AmazonS3') return `/cloud-account/details/${accountId}#s3/events`;
  if (serviceName === 'microsoft.storage/storageaccounts') return `/cloud-account/details/${accountId}#blob/events`;
  return '';
};

const UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState([]);
  const _showEllipsis = true;

  const redirectUrl = eventUrl(accountId, serviceName);

  const handleOptimizeClick = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '1');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/optimize';
    router.push(url.toString());
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .listEvents(
        {
          accountId: accountId,
          subjectNamespace: serviceName,
        },
        5,
        0,
        { light: true }
      )
      .then((res: any) => {
        setLoading(false);
        const ec2ResourceData = res.data?.events?.map((item: any) => {
          const data: ICustomTable2Row[] = [];

          data.push({
            component: (
              <Box sx={{ minWidth: _showEllipsis && '200px' }}>
                <Text showAutoEllipsis value={item.subject_name} />
                {item.subject_namespace && <Text value={`service: ${item.subject_namespace}`} />}
              </Box>
            ),
          });
          data.push({
            text: item.aggregation_key,
          });
          data.push({ component: <SeverityIcon severityType={item.priority} />, data: item.priority });
          data.push({ component: <Datetime value={item.starts_at} />, data: item.starts_at });

          return data;
        });
        setEventData(ec2ResourceData as any);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [accountId, serviceName, _showEllipsis]);

  return (
    <Box>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: '15px', rowGap: '20px', mb: '10px' }}>
        <StackSummaryBlock title={'Errors'}>
          <Box>
            <Typography color='#737373' fontSize={'12px'}>
              Fired Alarm Count
            </Typography>
            <Box display='flex' alignItems='center' gap={'7px'}>
              <Currency prefix='' value={clusterSummary?.current_month_projected_spend} />
              {clusterSummary?.current_month_projected_spend > 0 ? (
                <>
                  {' '}
                  <TrendArrowPercentage
                    sign={clusterSummary?.last_month_spend > clusterSummary?.current_month_projected_spend ? 1 : -1}
                    value={
                      (Math.abs(clusterSummary?.last_month_spend - clusterSummary?.current_month_projected_spend) * 100) /
                      clusterSummary?.last_month_spend
                    }
                  />
                </>
              ) : (
                <Box sx={{ width: '12px' }} />
              )}
              <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#B9B9B9', pt: '3px', pr: '5px' }}>last 7 days</Typography>
            </Box>
          </Box>
        </StackSummaryBlock>
        <StackSummaryBlock title={'Optimizations'}>
          <Box>
            <Typography color='#737373' fontSize={'12px'}>
              Optimize Count
            </Typography>
            <Box display='flex' alignItems='center' gap={'7px'}>
              <Box
                onClick={handleOptimizeClick}
                sx={{
                  cursor: 'pointer',
                  '&:hover': {
                    '& .MuiTypography-root': {
                      color: '#3162D0',
                    },
                  },
                }}
              >
                <Currency prefix='' value={clusterSummary?.recommendation_aggregate?.aggregate?.count} />
              </Box>
            </Box>
          </Box>
        </StackSummaryBlock>
      </Box>
      <StackSummaryBlock title={'Recent Events'}>
        <CustomTable2
          tableHeadingCenter={['Priority']}
          id={EVENT_TABLE_ID}
          headers={EVENT_HEADERS}
          tableData={eventData}
          rowsPerPage={5}
          onPageChange={() => {
            return false;
          }}
          loading={loading}
          totalRows={5}
          showAllLink={true}
          linkToShowAll={redirectUrl}
        />
      </StackSummaryBlock>
    </Box>
  );
};

const CostSummary = ({ clusterSummary = {}, currencySymbol = '$' }: any) => {
  const smallScreen = useMediaQuery('(max-width:1440px)');

  const currentGrossSpend =
    clusterSummary?.gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthGrossSpend =
    clusterSummary?.lm_gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.lm_spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthCredits = Math.abs(clusterSummary?.lm_credits_aggregate?.aggregate?.sum?.amount || 0);
  const currentCredits = Math.abs(clusterSummary?.credits_aggregate?.aggregate?.sum?.amount || 0);
  const currentNetSpend = clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;

  const monthlyForecast = getBudgetExpectedMonthlyExpense(currentGrossSpend);
  const dailyAvgCost = currentGrossSpend / (new Date().getDate() || 1);

  const hasValidPercentage = lastMonthGrossSpend > 0;
  const percentageChange = hasValidPercentage ? ((monthlyForecast - lastMonthGrossSpend) * 100) / lastMonthGrossSpend : 0;

  return (
    <Stack>
      <Title title={'Cost Summary'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
      <ClusterSummaryBlock
        title='Cost Summary'
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: smallScreen ? 'space-between' : 'flex-start',
          flexWrap: 'wrap',
          gap: '28px',
          padding: '20px 20px 20px 20px',
        }}
      >
        <Stack direction={'column'}>
          <Box>
            <Box display='flex' alignItems='center' gap='4px'>
              <Typography color='#737373' fontSize={'12px'}>
                Monthly forecast
              </Typography>
              <Tooltip
                title='Projected end-of-month cost based on current month-to-date gross spend (excludes credits). Percentage compares to last month gross usage.'
                arrow
              >
                <InfoOutlinedIcon sx={{ fontSize: 14, color: '#9CA3AF', cursor: 'help' }} />
              </Tooltip>
            </Box>
            <Box display='flex' alignItems='center' gap={'7px'}>
              {currentGrossSpend > 0 ? (
                <>
                  <Currency prefix={currencySymbol} value={monthlyForecast} />
                  {hasValidPercentage && Math.abs(percentageChange) < 1000 && (
                    <TrendArrowPercentage sign={percentageChange > 0 ? -1 : 1} value={Math.abs(percentageChange)} />
                  )}
                </>
              ) : (
                <Typography color='#999' fontSize={'10px'}>
                  No data available
                </Typography>
              )}
            </Box>
            <Stack direction={'row'}>
              <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#B9B9B9', pt: '3px', pr: '5px' }}>Prev mo (gross):</Typography>
              {lastMonthGrossSpend > 0 ? (
                <Currency prefix={currencySymbol} value={lastMonthGrossSpend} />
              ) : (
                <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#999', pt: '3px' }}>No data</Typography>
              )}
            </Stack>
            <br />
          </Box>
          <Box>
            <Typography color='#737373' fontSize={'12px'}>
              Current Month (MTD)
            </Typography>
            <Box display='flex' alignItems='center' gap={'7px'}>
              {currentGrossSpend > 0 ? (
                <Currency prefix={currencySymbol} value={currentGrossSpend} />
              ) : (
                <Typography color='#999' fontSize={'10px'}>
                  No data available
                </Typography>
              )}
            </Box>
            <Stack direction={'row'}>
              <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#B9B9B9', pt: '3px', pr: '5px' }}>Avg daily cost:</Typography>
              {currentGrossSpend > 0 ? (
                <Currency prefix={currencySymbol} value={dailyAvgCost} />
              ) : (
                <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#999', pt: '3px' }}>No data</Typography>
              )}
            </Stack>
            <br />
          </Box>
          {(currentCredits > 0 || lastMonthCredits > 0) && (
            <Box>
              <Typography color='#737373' fontSize={'12px'}>
                Credits / Discounts
              </Typography>
              {currentCredits > 0 && (
                <Stack direction={'row'}>
                  <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#16A34A', pt: '3px', pr: '5px' }}>
                    This mo: -{currencySymbol}
                    {formatNumber(currentCredits)}
                  </Typography>
                </Stack>
              )}
              {lastMonthCredits > 0 && (
                <Stack direction={'row'}>
                  <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#16A34A', pt: '3px', pr: '5px' }}>
                    Prev mo: -{currencySymbol}
                    {formatNumber(lastMonthCredits)}
                  </Typography>
                </Stack>
              )}
              {currentCredits > 0 && (
                <Stack direction={'row'} mt={'4px'}>
                  <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#B9B9B9', pt: '3px', pr: '5px' }}>Net spend (MTD):</Typography>
                  <Currency prefix={currencySymbol} value={currentNetSpend} />
                </Stack>
              )}
            </Box>
          )}
        </Stack>
      </ClusterSummaryBlock>
      <ClusterSummaryBlock
        title='Cost Summary'
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: smallScreen ? 'space-between' : 'flex-start',
          flexWrap: 'wrap',
          padding: '20px 20px 20px 20px',
          gap: '28px',
          borderColor: '#C1ECC0',
          backgroundColor: '#F0FDF4',
        }}
      >
        <Box
          sx={{
            display: 'inherit',
            alignItems: 'inherit',
            justifyContent: 'inherit',
            gap: '20px',
            flexGrow: smallScreen ? 1 : 0,
          }}
        >
          <Box display='flex' flexDirection='column'>
            <Box display='flex' alignItems='center' gap='4px'>
              <Typography color='#737373' fontSize={'12px'}>
                Savings
              </Typography>
              <Tooltip
                title="Savings are estimated by the cloud provider based on the account's full usage. On newly connected accounts they may appear higher than visible spend until cost reports accumulate enough history."
                arrow
              >
                <InfoOutlinedIcon sx={{ fontSize: 14, color: '#9CA3AF', cursor: 'help' }} />
              </Tooltip>
            </Box>
            <Currency prefix={currencySymbol} value={clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings * 12} suffix='/yr' />
            <Typography color='#D0D0D0' fontSize={'10px'}>
              estimated 12 mos
            </Typography>
          </Box>
          <DoughnutChartK8s
            size={'61px'}
            value={(() => {
              if (!clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings) {
                return 0;
              }

              const yearlyGrossSpend =
                clusterSummary?.yearly_gross_spends_aggregate?.aggregate?.sum?.amount ||
                clusterSummary?.yearly_spends_aggregate?.aggregate?.sum?.amount ||
                0;
              const yearlyExpense = getExpectedYearlyExpense(currentGrossSpend, yearlyGrossSpend);

              if (yearlyExpense < 1) {
                return 0;
              }

              const savingsPercentage = Math.round(
                (clusterSummary.recommendation_aggregate.aggregate.sum.estimated_savings * 12 * 100) / yearlyExpense
              );

              return Math.min(savingsPercentage, 100);
            })()}
            isDecimal={true}
          />
        </Box>
      </ClusterSummaryBlock>
    </Stack>
  );
};

const STORAGE_TYPE_MAP: Record<string, string> = {
  AmazonS3: 'storage',
  'Cloud Storage': 'storage.googleapis.com/Bucket',
};

const getStorageType = (serviceName: string): string => STORAGE_TYPE_MAP[serviceName] || 'storageaccounts';

const OptimizeSummary = ({ accountId = '', serviceName = '', resourceId = '' }) => {
  const [loadingTrend, setLoadingTrend] = useState(false);
  const [loadingSummary, setLoadingSummary] = useState(false);
  const [renderMetricsData, setRenderMetricsData] = useState<any>({});
  const currencySymbol = useCurrencySymbol(accountId);
  const [selectedDateRange, _setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });
  const [summary, setSummary] = useState<any>({});

  useEffect(() => {
    if (!accountId) {
      return;
    }

    const fetchMetrics = async () => {
      setLoadingTrend(true);
      try {
        // Use direct database query (fast) instead of cloud_metric_groupings_v2 action
        const res = await apiCloudAccount.getCloudResourceMetricsDirect({
          account_id: accountId,
          serviceName: serviceName || undefined,
          resourceId: resourceId || undefined,
          startDate: new Date(selectedDateRange.startDate),
          endDate: new Date(selectedDateRange.endDate),
        });
        const metricsData = res?.data?.data?.cloud_metric_groupings_v2?.rows || [];
        if (metricsData && metricsData.length > 0) {
          const groupedByMetrics = metricsData.reduce((acc: any, curr: any) => {
            const metric = curr.metric;
            if (!acc[metric]) {
              acc[metric] = [];
            }
            acc[metric].push(curr);
            return acc;
          }, {});
          setRenderMetricsData(groupedByMetrics);
        }
      } catch (error) {
        console.error(error);
      } finally {
        setLoadingTrend(false);
      }
    };

    fetchMetrics();
  }, [accountId, selectedDateRange]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoadingSummary(true);
    apiCloudAccount
      .cloudAccountS3Summary(accountId, {
        serviceName: serviceName,
        storageType: getStorageType(serviceName),
      })
      .then((res) => {
        setSummary(res);
        setLoadingSummary(false);
      })
      .catch((error) => {
        console.error('Error fetching S3 summary:', error);
        setLoadingSummary(false);
      });
  }, [accountId, serviceName]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    _setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const buildMetricChartData = (metricName: string, metricRows: any[]) => {
    const byResource: Record<string, any[]> = {};
    metricRows.forEach((row: any) => {
      const resourceKey = row.resource_name || row.resource_id || 'Unknown';
      if (!byResource[resourceKey]) {
        byResource[resourceKey] = [];
      }
      byResource[resourceKey].push(row);
    });

    const resourceKeys = Object.keys(byResource);
    const allTimestamps = new Set<string>();
    metricRows.forEach((row: any) => allTimestamps.add(row.timestamp));
    const sortedTimestamps = Array.from(allTimestamps).sort((a, b) => a.localeCompare(b));
    const labels = sortedTimestamps.map((ts: string) => new Date(ts).toLocaleString());

    const datasets = resourceKeys.map((resourceKey) => {
      const tsMap: Record<string, number> = {};
      byResource[resourceKey].forEach((row: any) => {
        tsMap[row.timestamp] = row.avg_value;
      });
      const data = sortedTimestamps.map((ts) => {
        const val = tsMap[ts];
        if (val === undefined) return null;
        return metricName !== 'CPUUtilization' ? formatMemory(val, 'bytes', 'gb', false) : val;
      });
      return {
        label: resourceKeys.length === 1 ? 'Utilization' : resourceKey,
        data,
      };
    });

    return { labels, datasets };
  };

  const renderMetricsSummary = () => {
    if (renderMetricsData && Object.keys(renderMetricsData).length > 0) {
      return Object.keys(renderMetricsData).map((g: string) => {
        const { labels, datasets } = buildMetricChartData(g, renderMetricsData[g]);

        return (
          <Box
            key={`${g}`}
            sx={{
              mb: '24px',
              background: 'white',
              borderRadius: '8px',
              border: '1px solid #EBEBEB',
              boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
              p: '20px',
            }}
          >
            <Charts chartTitle={formatMetricName(g)} dataset={datasets} labels={labels} data={[]} loading={loadingTrend} />
          </Box>
        );
      });
    }
    return <Charts dataset={[]} labels={[]} data={[]} loading={loadingTrend} />;
  };

  return (
    <>
      {loadingSummary || currencySymbol === undefined ? (
        <SummarySkeletonLoader />
      ) : (
        <Box sx={{ display: 'grid', gridTemplateColumns: '1.5fr 2fr 0.7fr', columnGap: '15px', rowGap: '20px', mb: '25px' }}>
          <ClusterSummary accountId={accountId} s3Summary={summary} />
          <UtilizationAndHealth accountId={accountId} clusterSummary={summary} serviceName={serviceName} />
          <CostSummary clusterSummary={summary} currencySymbol={currencySymbol} />
        </Box>
      )}
      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} />
      <BoxLayout2
        id={''}
        heading='Metrics'
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: '',
              };
            },
          },
        }}
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
      >
        {renderMetricsSummary()}
      </BoxLayout2>
    </>
  );
};

export default OptimizeSummary;
