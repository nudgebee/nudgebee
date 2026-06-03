import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { formatMemory, formatNumber } from '@lib/formatter';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import Charts from '@components1/common/charts/LineCharts';
import { formatMetricName } from '@utils/common';
import { getLast7Days } from '@lib/datetime';
import TotalCostChart from '@components1/cloudaccount/CostChart';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DSCard from '@components1/ds/Card';
import { Stat } from '@components1/ds/Stat';
import { ds } from '@utils/colors';
import { CloudCostSummary } from '@components1/cloudaccount/CloudCostSummary';
import { CloudRecentEvents } from '@components1/cloudaccount/CloudRecentEvents';

// Section heading style — matches CloudAccountSummary's `SectionHeading` helper
// (`bodyLg + medium + gray[700]`) so all cloud-account Summary surfaces share
// the same typography scale. Rendered inside DSCard's `header` slot; DSCard owns
// the spacing below the heading (its own paddingBottom + divider + marginBottom).
const SectionTitle = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{children}</Typography>
);

const S3ClusterSummary = ({ s3Summary = {} }: any) => {
  const router = useRouter();
  const buckets = s3Summary?.s3_count?.aggregate?.count || 0;

  // Mirrors the subtab=2 / `#…/instances` deeplink convention used by EC2 /
  // RDS / ECS Summary tabs and CloudAccountSummary's service cards. Keeps the
  // current top-level tab hash (e.g. `#s3`) and appends `/instances`.
  const handleNavigateToInstances = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '2');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/instances';
    router.push(url.toString());
  };

  return (
    <DSCard size='md' elevation='flat' header={<SectionTitle>Summary</SectionTitle>}>
      <Stat
        id='s3-summary-total-buckets'
        size='md'
        label='Total Buckets'
        value={
          <Box component='span' className='stat-value-affordance' sx={{ transition: `color ${ds.motion.micro} ${ds.motion.ease}` }}>
            {formatNumber(buckets)}
          </Box>
        }
        onClick={handleNavigateToInstances}
        sx={{
          '&:hover': {
            backgroundColor: 'transparent',
            '& .stat-value-affordance': { color: ds.blue[600] },
          },
        }}
      />
    </DSCard>
  );
};

const eventUrl = (accountId: string, serviceName: string) => {
  if (!accountId) return '';
  if (serviceName === 'AmazonS3') return `/cloud-account/details/${accountId}#s3/events`;
  if (serviceName === 'microsoft.storage/storageaccounts') return `/cloud-account/details/${accountId}#blob/events`;
  return '';
};

const S3UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const redirectUrl = eventUrl(accountId, serviceName);

  const handleOptimizeClick = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '1');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/optimize';
    router.push(url.toString());
  };

  // Use the same `events_aggregate.aggregate.count` that EC2/RDS use — the
  // legacy S3 file populated this block with `current_month_projected_spend`
  // (a cost value, not an alarm count), which was a label-vs-data mismatch
  // flagged in PR review and fixed here in the same change.
  const firedAlarmCount = clusterSummary?.events_aggregate?.aggregate?.count ?? 0;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], width: '100%', minWidth: 0, overflow: 'hidden' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[5], mb: ds.space[3] }}>
        <DSCard size='md' elevation='flat' header={<SectionTitle>Errors</SectionTitle>}>
          <Stat
            id='s3-summary-fired-alarm-count'
            size='md'
            label='Fired Alarm Count'
            value={
              <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[2] }}>
                <Box component='span'>{firedAlarmCount}</Box>
                <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], whiteSpace: 'nowrap' }}>
                  last 7 days
                </Typography>
              </Box>
            }
          />
        </DSCard>
        <DSCard size='md' elevation='flat' header={<SectionTitle>Optimizations</SectionTitle>}>
          <Stat
            id='s3-summary-optimize-count'
            size='md'
            label='Optimize Count'
            value={
              <Box component='span' className='stat-value-affordance' sx={{ transition: `color ${ds.motion.micro} ${ds.motion.ease}` }}>
                {clusterSummary?.recommendation_aggregate?.aggregate?.count ?? '-'}
              </Box>
            }
            onClick={handleOptimizeClick}
            sx={{
              '&:hover': {
                backgroundColor: 'transparent',
                '& .stat-value-affordance': { color: ds.blue[600] },
              },
            }}
          />
        </DSCard>
      </Box>
      <CloudRecentEvents accountId={accountId} serviceName={serviceName} redirectUrl={redirectUrl} />
    </Box>
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
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });
  const [summary, setSummary] = useState<any>({});

  useEffect(() => {
    if (!accountId) return;
    const fetchMetrics = async () => {
      setLoadingTrend(true);
      try {
        const res = await apiCloudAccount.getCloudResourceMetricsDirect({
          account_id: accountId,
          serviceName: serviceName || undefined,
          resourceId: resourceId || undefined,
          startDate: new Date(selectedDateRange.startDate),
          endDate: new Date(selectedDateRange.endDate),
        });
        const metricsData = res?.data?.data?.cloud_metric_groupings_v2?.rows || [];
        if (metricsData.length > 0) {
          const groupedByMetrics = metricsData.reduce((acc: any, curr: any) => {
            if (!acc[curr.metric]) acc[curr.metric] = [];
            acc[curr.metric].push(curr);
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
  }, [accountId, selectedDateRange, serviceName, resourceId]);

  useEffect(() => {
    if (!accountId) return;
    setLoadingSummary(true);
    apiCloudAccount
      .cloudAccountS3Summary(accountId, { serviceName, storageType: getStorageType(serviceName) })
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
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const buildMetricChartData = (metricName: string, metricRows: any[]) => {
    const byResource: Record<string, any[]> = {};
    metricRows.forEach((row: any) => {
      const resourceKey = row.resource_name || row.resource_id || 'Unknown';
      if (!byResource[resourceKey]) byResource[resourceKey] = [];
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
      return { label: resourceKeys.length === 1 ? 'Utilization' : resourceKey, data };
    });
    return { labels, datasets };
  };

  const renderMetricsSummary = () => {
    if (renderMetricsData && Object.keys(renderMetricsData).length > 0) {
      return Object.keys(renderMetricsData).map((g: string) => {
        const { labels, datasets } = buildMetricChartData(g, renderMetricsData[g]);
        return (
          <DSCard size='md' elevation='flat' key={g} sx={{ mb: ds.space[4], padding: ds.space[5] }}>
            <Charts chartTitle={formatMetricName(g)} dataset={datasets} labels={labels} data={[]} loading={loadingTrend} />
          </DSCard>
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
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: '1.5fr 2fr 0.7fr',
            alignItems: 'start',
            columnGap: ds.space[3],
            rowGap: ds.space[4],
            mb: ds.space[5],
          }}
        >
          <S3ClusterSummary s3Summary={summary} />
          <S3UtilizationAndHealth accountId={accountId} clusterSummary={summary} serviceName={serviceName} />
          <CloudCostSummary clusterSummary={summary} currencySymbol={currencySymbol} />
        </Box>
      )}

      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} />

      <ListingLayout id='s3-metrics'>
        <ListingLayout.Toolbar
          title='Metrics'
          actions={
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
                shortcutClickTime: 0,
              }}
              onChange={(result: any) => {
                const val = result?.selection ?? result;
                if (val) handleDateRangeChange(val);
              }}
            />
          }
        />
        <ListingLayout.Body padding={`${ds.space[4]} ${ds.space[5]}`}>{renderMetricsSummary()}</ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default OptimizeSummary;
