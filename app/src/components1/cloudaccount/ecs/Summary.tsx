import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import Loader from '@components1/common/Loader';
import { formatMemory } from '@lib/formatter';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import Charts from '@components1/common/charts/LineCharts';
import { convertStringCase, formatMetricName } from '@utils/common';
import { getYesterday } from '@lib/datetime';
import TotalCostChart from '@components1/cloudaccount/CostChart';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DSCard from '@components1/ds/Card';
import { Stat } from '@components1/ds/Stat';
import Text from '@common-new/format/Text';
import { ds } from '@utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import { CloudCostSummary } from '@components1/cloudaccount/CloudCostSummary';
import { CloudRecentEvents } from '@components1/cloudaccount/CloudRecentEvents';

// Section heading style — matches CloudAccountSummary's `SectionHeading` helper.
// Rendered inside DSCard's `header` slot; DSCard owns the spacing below the heading.
const SectionTitle = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{children}</Typography>
);

const ECSResourceCounts = ({ ecsSummaryData = {} }: any) => {
  const totalClusters = ecsSummaryData?.cloud_resourses?.filter((r: any) => r.type == 'cluster')?.length || 0;
  const totalServices = ecsSummaryData?.cloud_resourses?.filter((r: any) => r.type == 'service')?.length || 0;
  const totalTasks = ecsSummaryData?.cloud_resourses?.filter((r: any) => r.type == 'task')?.length || 0;

  return (
    <DSCard size='md' elevation='flat' header={<SectionTitle>Summary</SectionTitle>}>
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[6], flexWrap: 'wrap' }}>
        <Stat size='md' label='Total Clusters' value={totalClusters} />
        <Stat size='md' label='Total Services' value={totalServices} />
        <Stat size='md' label='Total Tasks' value={totalTasks} />
      </Box>
    </DSCard>
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
  const router = useRouter();
  // Legacy file hardcoded subjectNamespace='AmazonECS' for the events fetch
  // even though `serviceName` may differ for GCP/Azure ECS-equivalents.
  // Preserving that behaviour faithfully — see eventUrl above for the redirect.
  const redirectUrl = eventUrl(accountId, serviceName);
  const failedTaskCount = clusterSummary?.cloud_resourses?.filter((r: any) => r.type == 'task' && r.status == 'Failed')?.length || 0;

  const handleOptimizeClick = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '1');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/optimize';
    router.push(url.toString());
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], width: '100%', minWidth: 0, overflow: 'hidden' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[5], mb: ds.space[3] }}>
        <DSCard size='md' elevation='flat' header={<SectionTitle>Errors</SectionTitle>}>
          <Stat id='ecs-summary-failed-task-count' size='md' label='Failed Task Count' value={failedTaskCount} />
        </DSCard>
        <DSCard size='md' elevation='flat' header={<SectionTitle>Optimizations</SectionTitle>}>
          <Stat
            id='ecs-summary-recommendation-count'
            size='md'
            label='Recommendation Count'
            value={
              <Box component='span' className='stat-value-affordance' sx={{ transition: `color ${ds.motion.micro} ${ds.motion.ease}` }}>
                {clusterSummary?.recommendation_aggregate?.aggregate?.count || 0}
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
      <CloudRecentEvents
        accountId={accountId}
        serviceName='AmazonECS'
        redirectUrl={redirectUrl}
        // ECS subject_name is the full task/service ARN
        // (`arn:aws:ecs:us-east-1:…/my-cluster`); strip down to the last
        // segment so the table cell shows a readable name. Matches legacy
        // behaviour.
        transformSubjectName={(item: any) => item.subject_name?.split('/').pop() ?? ''}
        // Legacy ECS rendered `Service: {meta.serviceName}` as the secondary
        // line (not `subject_namespace` which is what the default uses).
        secondaryRender={(item: any) => (item.meta?.serviceName ? <Text value={`Service: ${item.meta.serviceName}`} secondaryText /> : null)}
      />
    </Box>
  );
};

const ECSSummaryView = ({ accountId = '', serviceName = 'AmazonECS', resourceId = null, _resourceType = 'cluster' }: any) => {
  const [loadingMetrics, setLoadingMetrics] = useState(false);
  const [loadingSummary, setLoadingSummary] = useState(false);
  const [renderMetricsData, setRenderMetricsData] = useState<any>({});
  const currencySymbol = useCurrencySymbol(accountId);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getYesterday().getTime(),
    endDate: new Date().getTime(),
  });
  const [summaryData, setSummaryData] = useState<any>({});

  useEffect(() => {
    if (!accountId) return;

    // The top 3-column summary is only relevant for the account-wide ECS view;
    // when scoped to a specific `resourceId`, skip this fetch entirely.
    if (!resourceId) {
      setLoadingSummary(true);
      apiCloudAccount
        .cloudAccountECSSummary(accountId, { serviceName: 'AmazonECS' })
        .then((res: any) => setSummaryData(res || {}))
        .catch((err) => {
          console.error(`Error fetching ECS summary for account ${accountId}:`, err);
          snackbar.error(`Failed to load ECS summary: ${err.message}`);
          setSummaryData({});
        })
        .finally(() => setLoadingSummary(false));
    }
  }, [accountId, serviceName, resourceId]);

  useEffect(() => {
    if (!accountId) return;
    setLoadingMetrics(true);
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
        if (metricsData.length > 0) {
          const groupedByMetrics = metricsData.reduce((acc: any, curr: any) => {
            if (!acc[curr.metric]) acc[curr.metric] = [];
            acc[curr.metric].push(curr);
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
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const renderMetricsSummary = () => {
    if (loadingMetrics) return <Loader style={{ height: '100%', width: '100%' }} />;
    const metricKeys = Object.keys(renderMetricsData);
    if (metricKeys.length === 0) {
      return (
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], textAlign: 'center', py: ds.space[5] }}>
          No metrics data available for the selected period.
        </Typography>
      );
    }
    return metricKeys.map((metricName: string) => {
      const labels = renderMetricsData[metricName].map((h: any) => new Date(h.timestamp).toLocaleString());
      const values = renderMetricsData[metricName].map((h: any) => h.avg_value);
      const formattedValues =
        metricName.toLowerCase().includes('memory') && metricName.toLowerCase() != 'memoryutilization'
          ? values.map((v: any) => parseFloat(formatMemory(v, 'bytes', 'mb', true) as string))
          : values;
      const chartDataset = [{ label: convertStringCase(metricName), data: formattedValues }];

      return (
        <DSCard size='md' elevation='flat' key={metricName} sx={{ mb: ds.space[4], padding: ds.space[5] }}>
          <Charts chartTitle={formatMetricName(metricName)} dataset={chartDataset} labels={labels} data={[]} loading={loadingMetrics} />
        </DSCard>
      );
    });
  };

  // Resource-scoped view — only the metrics panel, with a friendly resource name
  // (last `/`-segment of the ARN) in the heading.
  if (resourceId) {
    return (
      <ListingLayout id={`ecs-resource-metrics-${resourceId}`}>
        <ListingLayout.Toolbar
          title={`Metrics for ${resourceId.split('/').pop()}`}
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
    );
  }

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
          <ECSResourceCounts ecsSummaryData={summaryData} />
          <ECSUtilizationAndHealth accountId={accountId} clusterSummary={summaryData} serviceName={serviceName} />
          <CloudCostSummary clusterSummary={summaryData} currencySymbol={currencySymbol} />
        </Box>
      )}

      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} />

      <ListingLayout id='ecs-general-metrics'>
        <ListingLayout.Toolbar
          title='ECS Cluster Metrics'
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

export default ECSSummaryView;
