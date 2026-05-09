import React, { useEffect, useState } from 'react';
import { Box, Stack, Tooltip, Typography, useMediaQuery } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import Loader from '@components1/common/Loader';
import Title from '@common/Title';
import TrendArrowPercentage from '@components1/common/widgets/TrendArrowPercentage'; // Keep if used for cost trends
import { formatMemory, formatNumber } from '@lib/formatter';
import DoughnutChartK8s from '@components1/common/charts/DoughnutChartK8s'; // Keep if used for savings
import Currency from '@components1/common/format/Currency';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import type { ICustomTable2Row } from './Instances'; // Path to Instances.tsx for ICustomTable2Row if needed for events
import Text from '@components1/common/format/Text';
import Charts from '@components1/common/charts/LineCharts';
import { convertStringCase, formatMetricName } from 'src/utils/common';
import { getYesterday } from '@lib/datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import TotalCostChart from '@components1/cloudaccount/CostChart';
import { snackbar } from '@components1/common/snackbarService';

const EVENT_TABLE_ID = 'ECS_EVENT_TABLE_ID';
const EVENT_HEADERS = ['Resource ID', 'Event', 'Severity', 'Created at']; // Adjusted Resource ID for generic use

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
        // title={title} // This prop doesn't seem to be used by ClusterSummaryBlock
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

const ECSResourceBlock = ({ label, count }: any) => {
  return (
    <Box>
      <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'1px'}>
        {label}
      </Typography>
      <Typography color='#374151' fontSize={'24px'} lineHeight={'36px'} fontWeight={600}>
        {formatNumber(count)}
      </Typography>
    </Box>
  );
};

const ECSServiceSummary = ({ _accountId, ecsSummaryData = {} }: any) => {
  const totalClusters = ecsSummaryData?.cloud_resourses?.filter((r: any) => r.type == 'cluster')?.length || 0;
  const totalServices = ecsSummaryData?.cloud_resourses?.filter((r: any) => r.type == 'service')?.length || 0;
  const totalTasks = ecsSummaryData?.cloud_resourses?.filter((r: any) => r.type == 'task')?.length || 0;
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
          paddingY: '20px',
        }}
      >
        <ECSResourceBlock label='Total Clusters' count={totalClusters} />
        <ECSResourceBlock label='Total Services' count={totalServices} />
        <ECSResourceBlock label='Total Tasks' count={totalTasks} />
      </ClusterSummaryBlock>
    </Stack>
  );
};

const eventUrl = (accountId: string, serviceName: string) => {
  if (!accountId) return '';
  if (serviceName === 'Cloud SQL') return `/cloud-account/details/${accountId}#cloud-sql/events`;
  if (serviceName === 'AmazonECS') return `/cloud-account/details/${accountId}#ecs/events`;
  if (serviceName === 'microsoft.sql/servers') return `/cloud-account/details/${accountId}#sql/events`;
  return '';
};

const ECSUtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState<ICustomTable2Row[]>([]);
  const _showEllipsis = true;
  const redirectUrl = eventUrl(accountId, serviceName);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .listEvents(
        {
          accountId: accountId,
          subjectNamespace: 'AmazonECS',
        },
        5, // limit
        0, // offset
        { light: true }
      )
      .then((res: any) => {
        setLoading(false);
        const ecsEventData = (res.data?.events || []).map((item: any) => {
          const data: ICustomTable2Row[] = [];
          data.push({
            // Resource ID (Cluster/Service ARN or Name)
            component: (
              <Box sx={{ minWidth: _showEllipsis && '120px' }}>
                <Text showAutoEllipsis value={item.subject_name?.split('/').pop()} />
                {item.meta?.serviceName && <Text value={`Service: ${item.meta.serviceName}`} />}
              </Box>
            ),
          });
          data.push({
            // Event
            text: item.aggregation_key,
          });
          data.push({ component: <SeverityIcon severityType={item.priority} />, data: item.priority });
          data.push({ component: <Datetime value={item.starts_at} />, data: item.starts_at });
          return data;
        });
        setEventData(ecsEventData as ICustomTable2Row[]);
      })
      .catch((error) => {
        setLoading(false);
        console.error(`Error fetching ${serviceName} events:`, error);
        setEventData([]);
      });
  }, [accountId, serviceName, _showEllipsis]);

  return (
    <Box>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: '15px', rowGap: '20px', mb: '10px' }}>
        <StackSummaryBlock title={'Errors'}>
          <Box>
            <Typography color='#737373' fontSize={'12px'}>
              Failed Task Count
            </Typography>
            <Box display='flex' alignItems='center' gap={'7px'}>
              <Text value={clusterSummary?.cloud_resourses?.filter((r: any) => r.type == 'task' && r.status == 'Failed')?.length || 0} />
            </Box>
          </Box>
        </StackSummaryBlock>
        <StackSummaryBlock title={'Optimizations'}>
          <Box>
            <Typography color='#737373' fontSize={'12px'}>
              Recommendation Count
            </Typography>
            <Box display='flex' alignItems='center' gap={'7px'}>
              <Currency prefix='' value={clusterSummary?.recommendation_aggregate?.aggregate?.count || 0} />
            </Box>
          </Box>
        </StackSummaryBlock>
      </Box>
      <StackSummaryBlock title={'Recent Events'}>
        <CustomTable2
          tableHeadingCenter={['Severity']}
          id={EVENT_TABLE_ID}
          headers={EVENT_HEADERS}
          tableData={eventData}
          rowsPerPage={5}
          onPageChange={() => false} // No pagination for this small list
          loading={loading}
          totalRows={eventData.length}
          showAllLink={true}
          linkToShowAll={redirectUrl}
        />
      </StackSummaryBlock>
    </Box>
  );
};

const ECSCostSummary = ({ clusterSummary = {}, currencySymbol = '$' }: any) => {
  const smallScreen = useMediaQuery('(max-width:1440px)');

  // Use gross spend (positive amounts only) for calculations to avoid near-zero when credits offset costs
  const currentGrossSpend =
    clusterSummary?.gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthGrossSpend =
    clusterSummary?.lm_gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.lm_spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthCredits = Math.abs(clusterSummary?.lm_credits_aggregate?.aggregate?.sum?.amount || 0);
  const _lastMonthNetSpend = clusterSummary?.lm_spends_aggregate?.aggregate?.sum?.amount || 0;
  const currentCredits = Math.abs(clusterSummary?.credits_aggregate?.aggregate?.sum?.amount || 0);
  const currentNetSpend = clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;

  const monthlyForecast = getBudgetExpectedMonthlyExpense(currentGrossSpend);
  const dailyAvgCost = currentGrossSpend / (new Date().getDate() || 1);

  // Percentage change using gross spend to avoid division by near-zero
  const hasValidPercentage = lastMonthGrossSpend > 0;
  const percentageChange = hasValidPercentage ? ((monthlyForecast - lastMonthGrossSpend) * 100) / lastMonthGrossSpend : 0;

  // Savings calculation using gross yearly spend
  const savingsEstimatedYearly = (clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings || 0) * 12;
  const yearlyGrossSpend =
    clusterSummary?.yearly_gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.yearly_spends_aggregate?.aggregate?.sum?.amount || 0;
  const yearlySpendForSavingsCalc = getExpectedYearlyExpense(currentGrossSpend, yearlyGrossSpend);
  const savingsPercentage = yearlySpendForSavingsCalc > 1 ? Math.min(Math.round((savingsEstimatedYearly * 100) / yearlySpendForSavingsCalc), 100) : 0;

  return (
    <Stack>
      <Title title={'Cost Summary'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
      <ClusterSummaryBlock
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
      {savingsEstimatedYearly > 0 && (
        <ClusterSummaryBlock
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: smallScreen ? 'space-between' : 'flex-start',
            flexWrap: 'wrap',
            padding: '20px',
            gap: '20px',
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
              <Typography color='#737373' fontSize={'12px'}>
                Savings
              </Typography>
              <Currency prefix={currencySymbol} value={savingsEstimatedYearly} suffix='/yr' />
              <Typography color='#D0D0D0' fontSize={'10px'}>
                estimated 12 mos (vs gross spend)
              </Typography>
            </Box>
            {savingsPercentage > 0 && <DoughnutChartK8s size={'61px'} value={savingsPercentage} isDecimal={true} />}
          </Box>
        </ClusterSummaryBlock>
      )}
    </Stack>
  );
};

const ECSSummaryView = ({ accountId = '', serviceName = 'AmazonECS', resourceId = null, _resourceType = 'cluster' }: any) => {
  const [loadingMetrics, setLoadingMetrics] = useState(false);
  const [loadingSummary, setLoadingSummary] = useState(false);
  const [renderMetricsData, setRenderMetricsData] = useState<any>({});
  const currencySymbol = useCurrencySymbol(accountId);
  const [selectedDateRange, _setSelectedDateRange] = useState({
    startDate: getYesterday().getTime(),
    endDate: new Date().getTime(),
  });
  const [summaryData, setSummaryData] = useState<any>({});

  useEffect(() => {
    if (!accountId) {
      return;
    }

    // Fetch general ECS summary data (not for a specific resourceId)
    if (!resourceId) {
      setLoadingSummary(true);
      apiCloudAccount
        .cloudAccountECSSummary(accountId, {
          // Using a hypothetical generic summary endpoint
          serviceName: 'AmazonECS',
        })
        .then((res: any) => {
          setSummaryData(res || {});
        })
        .catch((err) => {
          console.error(`Error fetching ECS summary for account ${accountId}:`, err);
          snackbar.error(`Failed to load ECS summary: ${err.message}`);
          setSummaryData({});
        })
        .finally(() => setLoadingSummary(false));
    }
  }, [accountId, serviceName, resourceId]); // Added resourceId to dependencies

  useEffect(() => {
    if (!accountId) {
      return;
    }

    setLoadingMetrics(true);
    // Use direct database query (fast) instead of cloud_metric_groupings_v2 action
    // which calls cloud provider APIs in real-time and is very slow
    apiCloudAccount
      .getCloudResourceMetricsDirect({
        account_id: accountId,
        serviceName: serviceName || undefined,
        resourceId: resourceId || undefined,
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
      })
      .then((res) => {
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
        } else {
          setRenderMetricsData({});
        }
      })
      .catch((error) => {
        console.error(`Error fetching ECS metrics for ${resourceId || 'account ' + accountId}:`, error);
        setRenderMetricsData({});
      })
      .finally(() => setLoadingMetrics(false));
  }, [accountId, serviceName, resourceId, selectedDateRange]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    _setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const renderMetricsSummary = () => {
    if (loadingMetrics) {
      return <Loader style={{ height: '100%', width: '100%' }} />;
    }

    const metricKeys = Object.keys(renderMetricsData);
    if (metricKeys.length === 0) {
      return <Typography>No metrics data available for the selected period.</Typography>;
    }

    return metricKeys.map((metricName: string) => {
      const labels = renderMetricsData[metricName].map((h: any) => new Date(h.timestamp).toLocaleString());
      const values = renderMetricsData[metricName].map((h: any) => h.avg_value);
      const formattedValues =
        metricName.toLowerCase().includes('memory') && metricName.toLowerCase() != 'memoryutilization'
          ? values.map((v: any) => parseFloat(formatMemory(v, 'bytes', 'mb', true) as string)) // ensure number for chart
          : values;

      const chartDataset = [
        {
          label: convertStringCase(metricName), // More friendly name
          data: formattedValues,
          // Add styling/colors for charts if needed
        },
      ];

      return (
        <Box
          key={metricName}
          sx={{
            mb: '24px',
            background: 'white',
            borderRadius: '8px',
            border: '1px solid #EBEBEB',
            boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
            p: '20px',
          }}
        >
          <Charts chartTitle={formatMetricName(metricName)} dataset={chartDataset} labels={labels} data={[]} loading={loadingMetrics} />
        </Box>
      );
    });
  };

  if (resourceId) {
    return (
      <BoxLayout2
        id={`ecs-resource-metrics-${resourceId}`}
        heading={`Metrics for ${resourceId.split('/').pop()}`} // Show a cleaner name
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: '',
              };
            },
          },
          sharing: { enabled: false, onClick: null },
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
    );
  }

  return (
    <>
      {loadingSummary || currencySymbol === undefined ? (
        <SummarySkeletonLoader />
      ) : (
        <Box sx={{ display: 'grid', gridTemplateColumns: '1.5fr 2fr 0.7fr', columnGap: '15px', rowGap: '20px', mb: '25px' }}>
          <ECSServiceSummary accountId={accountId} ecsSummaryData={summaryData} />
          <ECSUtilizationAndHealth accountId={accountId} clusterSummary={summaryData} serviceName={serviceName} />
          <ECSCostSummary clusterSummary={summaryData} currencySymbol={currencySymbol} />
        </Box>
      )}

      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} />

      <BoxLayout2
        id={'ecs-general-metrics'}
        heading='ECS Cluster Metrics'
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: '',
              };
            },
          },
          sharing: { enabled: false, onClick: null },
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

export default ECSSummaryView;
