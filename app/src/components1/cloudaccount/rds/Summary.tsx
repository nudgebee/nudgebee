import React, { useEffect, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { formatMemory, formatNumber } from '@lib/formatter';
import CustomTable2 from '@common-new/tables/CustomTable2';
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
import Chip from '@components1/ds/Chip';
import Text from '@common-new/format/Text';
import { ds } from '@utils/colors';
import { CloudCostSummary } from '@components1/cloudaccount/CloudCostSummary';
import { CloudRecentEvents } from '@components1/cloudaccount/CloudRecentEvents';

const _INSTANCE_TABLE_ID = 'INSTANCE_TABLE_ID';

// Column widths sized to typical values: Engine takes the most (often hyphenated
// like "aurora-postgresql"); Count / Memory / vCPU are short single-line.
const _INSTANCE_HEADERS = [
  { name: 'Instance Type', width: '22%' },
  { name: 'Engine', width: '28%' },
  { name: 'Count', width: '10%' },
  { name: 'Memory', width: '20%' },
  { name: 'vCPU', width: '20%' },
];

// Section heading style — matches CloudAccountSummary's `SectionHeading` helper.
// Rendered inside DSCard's `header` slot; DSCard owns the spacing below the heading.
const SectionTitle = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{children}</Typography>
);

// Static fallback specs for common AWS instance types (used when cloud_resource_details is empty).
// Maps instance family.size to { memory_gb, vcpu }. Kept verbatim from the
// legacy file — this is domain knowledge that doesn't change with the DS swap.
const AWS_INSTANCE_SPECS: Record<string, { memory_gb: string; vcpu: string }> = {
  // T3 family
  't3.micro': { memory_gb: '1', vcpu: '2' },
  't3.small': { memory_gb: '2', vcpu: '2' },
  't3.medium': { memory_gb: '4', vcpu: '2' },
  't3.large': { memory_gb: '8', vcpu: '2' },
  't3.xlarge': { memory_gb: '16', vcpu: '4' },
  't3.2xlarge': { memory_gb: '32', vcpu: '8' },
  // T4g family
  't4g.micro': { memory_gb: '1', vcpu: '2' },
  't4g.small': { memory_gb: '2', vcpu: '2' },
  't4g.medium': { memory_gb: '4', vcpu: '2' },
  't4g.large': { memory_gb: '8', vcpu: '2' },
  't4g.xlarge': { memory_gb: '16', vcpu: '4' },
  't4g.2xlarge': { memory_gb: '32', vcpu: '8' },
  // M5 family
  'm5.large': { memory_gb: '8', vcpu: '2' },
  'm5.xlarge': { memory_gb: '16', vcpu: '4' },
  'm5.2xlarge': { memory_gb: '32', vcpu: '8' },
  'm5.4xlarge': { memory_gb: '64', vcpu: '16' },
  'm5.8xlarge': { memory_gb: '128', vcpu: '32' },
  'm5.12xlarge': { memory_gb: '192', vcpu: '48' },
  'm5.16xlarge': { memory_gb: '256', vcpu: '64' },
  'm5.24xlarge': { memory_gb: '384', vcpu: '96' },
  // M5d family
  'm5d.large': { memory_gb: '8', vcpu: '2' },
  'm5d.xlarge': { memory_gb: '16', vcpu: '4' },
  'm5d.2xlarge': { memory_gb: '32', vcpu: '8' },
  'm5d.4xlarge': { memory_gb: '64', vcpu: '16' },
  'm5d.8xlarge': { memory_gb: '128', vcpu: '32' },
  'm5d.12xlarge': { memory_gb: '192', vcpu: '48' },
  'm5d.16xlarge': { memory_gb: '256', vcpu: '64' },
  'm5d.24xlarge': { memory_gb: '384', vcpu: '96' },
  // M6g family (Graviton2)
  'm6g.large': { memory_gb: '8', vcpu: '2' },
  'm6g.xlarge': { memory_gb: '16', vcpu: '4' },
  'm6g.2xlarge': { memory_gb: '32', vcpu: '8' },
  'm6g.4xlarge': { memory_gb: '64', vcpu: '16' },
  'm6g.8xlarge': { memory_gb: '128', vcpu: '32' },
  'm6g.12xlarge': { memory_gb: '192', vcpu: '48' },
  'm6g.16xlarge': { memory_gb: '256', vcpu: '64' },
  // M6i family
  'm6i.large': { memory_gb: '8', vcpu: '2' },
  'm6i.xlarge': { memory_gb: '16', vcpu: '4' },
  'm6i.2xlarge': { memory_gb: '32', vcpu: '8' },
  'm6i.4xlarge': { memory_gb: '64', vcpu: '16' },
  'm6i.8xlarge': { memory_gb: '128', vcpu: '32' },
  'm6i.12xlarge': { memory_gb: '192', vcpu: '48' },
  'm6i.16xlarge': { memory_gb: '256', vcpu: '64' },
  'm6i.24xlarge': { memory_gb: '384', vcpu: '96' },
  // M7g family (Graviton3)
  'm7g.medium': { memory_gb: '4', vcpu: '1' },
  'm7g.large': { memory_gb: '8', vcpu: '2' },
  'm7g.xlarge': { memory_gb: '16', vcpu: '4' },
  'm7g.2xlarge': { memory_gb: '32', vcpu: '8' },
  'm7g.4xlarge': { memory_gb: '64', vcpu: '16' },
  'm7g.8xlarge': { memory_gb: '128', vcpu: '32' },
  'm7g.12xlarge': { memory_gb: '192', vcpu: '48' },
  'm7g.16xlarge': { memory_gb: '256', vcpu: '64' },
  // R5 family (memory optimized)
  'r5.large': { memory_gb: '16', vcpu: '2' },
  'r5.xlarge': { memory_gb: '32', vcpu: '4' },
  'r5.2xlarge': { memory_gb: '64', vcpu: '8' },
  'r5.4xlarge': { memory_gb: '128', vcpu: '16' },
  'r5.8xlarge': { memory_gb: '256', vcpu: '32' },
  'r5.12xlarge': { memory_gb: '384', vcpu: '48' },
  'r5.16xlarge': { memory_gb: '512', vcpu: '64' },
  'r5.24xlarge': { memory_gb: '768', vcpu: '96' },
  // R6g family (Graviton2 memory optimized)
  'r6g.large': { memory_gb: '16', vcpu: '2' },
  'r6g.xlarge': { memory_gb: '32', vcpu: '4' },
  'r6g.2xlarge': { memory_gb: '64', vcpu: '8' },
  'r6g.4xlarge': { memory_gb: '128', vcpu: '16' },
  'r6g.8xlarge': { memory_gb: '256', vcpu: '32' },
  'r6g.12xlarge': { memory_gb: '384', vcpu: '48' },
  'r6g.16xlarge': { memory_gb: '512', vcpu: '64' },
  // R7g family (Graviton3 memory optimized)
  'r7g.large': { memory_gb: '16', vcpu: '2' },
  'r7g.xlarge': { memory_gb: '32', vcpu: '4' },
  'r7g.2xlarge': { memory_gb: '64', vcpu: '8' },
  'r7g.4xlarge': { memory_gb: '128', vcpu: '16' },
  'r7g.8xlarge': { memory_gb: '256', vcpu: '32' },
  'r7g.12xlarge': { memory_gb: '384', vcpu: '48' },
  'r7g.16xlarge': { memory_gb: '512', vcpu: '64' },
};

/**
 * Look up instance specs from the static fallback map.
 * Handles both EC2-style (m5d.2xlarge) and RDS-style (db.m5d.2xlarge) names.
 */
function getStaticInstanceSpecs(instanceType: string): { memory_gb: string; vcpu: string } | null {
  if (AWS_INSTANCE_SPECS[instanceType]) return AWS_INSTANCE_SPECS[instanceType];
  if (instanceType.startsWith('db.')) {
    const stripped = instanceType.replace('db.', '');
    if (AWS_INSTANCE_SPECS[stripped]) return AWS_INSTANCE_SPECS[stripped];
  }
  return null;
}

// Multi-cloud field mapping helpers (aligned with Instances.tsx patterns).
function getInstanceTypeFromMeta(r: any): string {
  if (r?.meta?.DBInstanceClass) return r.meta.DBInstanceClass;
  if (r?.meta?.settings?.tier) return r.meta.settings.tier;
  if (r?.meta?.sku?.name) return r.meta.sku.name;
  return r?.meta?.InstanceType || '-';
}

function getEngineFromMeta(r: any): string {
  if (r?.meta?.Engine) return r.meta.Engine;
  if (r?.meta?.databaseVersion) return r.meta.databaseVersion.split('_')[0].toLowerCase();
  if (r?.meta?.kind) return r.meta.kind;
  if (r?.meta?.properties?.version) return 'SQL Server';
  return '-';
}

/**
 * Parse GCP Cloud SQL tier to extract vCPU and memory.
 * GCP tiers: "db-custom-{vcpu}-{memoryMB}", "db-n1-standard-{vcpu}", etc.
 */
function parseGcpTierSpecs(tier: string): { memory_gb: string; vcpu: string } | null {
  if (!tier) return null;
  const customMatch = /^db-custom-(\d+)-(\d+)$/.exec(tier);
  if (customMatch) {
    const vcpu = customMatch[1];
    const memMb = parseInt(customMatch[2], 10);
    const memGb = (memMb / 1024).toFixed(1).replace(/\.0$/, '');
    return { vcpu, memory_gb: memGb };
  }
  const stdMatch = /^db-(?:n\d+|e2|c\d+)-(standard|highmem|highcpu)-(\d+)$/.exec(tier);
  if (stdMatch) {
    const type = stdMatch[1];
    const vcpu = stdMatch[2];
    const vcpuNum = parseInt(vcpu, 10);
    let memGb = '';
    if (type === 'standard') memGb = String(vcpuNum * 4);
    else if (type === 'highmem') memGb = String(vcpuNum * 8);
    else if (type === 'highcpu') memGb = String(Math.ceil(vcpuNum * 0.9));
    return { vcpu, memory_gb: memGb };
  }
  return null;
}

const ClusterSummary = ({ accountId, clusterSummary = {} }: any) => {
  const router = useRouter();
  const [tableData, setTableData] = useState<any[][]>([]);

  // "View all instances" link — see EC2 Summary's variant for the rationale.
  // Populated post-mount via useEffect (not at render time) to avoid an SSR
  // hydration mismatch between server (`''`) and client (real URL).
  const [instancesUrl, setInstancesUrl] = useState('');

  useEffect(() => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '2');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/instances';
    setInstancesUrl(url.toString());
  }, []);

  const handleNavigateToInstances = () => {
    if (instancesUrl) router.push(instancesUrl);
  };

  useEffect(() => {
    let stale = false;

    const buildTable = async () => {
      const resources = clusterSummary?.cloud_resourses || [];
      // Clear stale rows when the new clusterSummary has no instances — otherwise
      // navigating from a populated service (e.g. AWS RDS) to an empty one (e.g.
      // Azure SQL MI) would keep showing the previous service's table.
      if (resources.length === 0) {
        setTableData([]);
        return;
      }

      // Group resources by `{instanceType}__{engine}` so each row in the
      // detail table represents one (type, engine) combination.
      const summaryData: any = {};
      resources.forEach((r: any) => {
        const instanceType = getInstanceTypeFromMeta(r);
        const engine = getEngineFromMeta(r);
        const groupKey = `${instanceType}__${engine}`;

        if (!(groupKey in summaryData)) {
          // Tier 1: try AWS InstanceTypeDetails metadata from the cloud collector.
          let memory = r?.meta?.InstanceTypeDetails?.product?.attributes?.memory || '';
          let vcpu = r?.meta?.InstanceTypeDetails?.product?.attributes?.vcpu || '';

          // Tier 2 (GCP only): parse Cloud SQL tier name when meta is empty.
          if (!memory && !vcpu) {
            const gcpSpecs = parseGcpTierSpecs(instanceType);
            if (gcpSpecs) {
              memory = `${gcpSpecs.memory_gb} GiB`;
              vcpu = gcpSpecs.vcpu;
            }
          }

          summaryData[groupKey] = { count: 0, instanceType, engine, memory, vcpu };
        }
        summaryData[groupKey].count += 1;
      });

      // Tier 3: fetch specs from `cloud_resource_details` for instance types
      // still missing Memory/vCPU.
      const missingTypes = Object.values(summaryData)
        .filter((s: any) => !s.memory && !s.vcpu && s.instanceType !== '-')
        .map((s: any) => s.instanceType);

      if (missingTypes.length > 0) {
        try {
          const specsMap = await apiCloudAccount.getInstanceTypeSpecs({ resourceTypes: missingTypes });
          if (stale) return;
          Object.values(summaryData).forEach((s: any) => {
            const specs = specsMap[s.instanceType];
            if (specs) {
              if (!s.memory && specs.memory_gb) s.memory = `${specs.memory_gb} GiB`;
              if (!s.vcpu && specs.cpu_virtual) s.vcpu = specs.cpu_virtual;
            }
          });
        } catch (e) {
          console.log('Failed to fetch instance type specs', e);
        }
      }

      // Tier 4: static fallback for known AWS types (covers cases where both
      // the metadata and the API are silent — e.g. older t3 instances on an
      // un-refreshed cloud_resource_details table).
      Object.values(summaryData).forEach((s: any) => {
        if ((!s.memory || !s.vcpu) && s.instanceType !== '-') {
          const staticSpecs = getStaticInstanceSpecs(s.instanceType);
          if (staticSpecs) {
            if (!s.memory) s.memory = `${staticSpecs.memory_gb} GiB`;
            if (!s.vcpu) s.vcpu = staticSpecs.vcpu;
          }
        }
      });

      if (stale) return;

      const data = Object.keys(summaryData).map((_key) => {
        const item = summaryData[_key];
        // For the two free-form text columns (Instance Type, Engine) use Text's
        // `showAutoEllipsis lineClamp={1}` so hyphenated values like
        // "aurora-postgresql" stay on a single line within their column width.
        // Numeric/short cells (Count, Memory, vCPU) stay as plain `text`.
        return [
          { component: <Text value={item.instanceType} showAutoEllipsis lineClamp={1} /> },
          { component: <Text value={item.engine} showAutoEllipsis lineClamp={1} /> },
          { text: item.count },
          { text: item.memory || '-' },
          { text: item.vcpu || '-' },
        ];
      });

      setTableData(data);
    };

    buildTable();
    return () => {
      stale = true;
    };
  }, [accountId, clusterSummary]);

  const uniqueTypes = [...new Set(clusterSummary?.cloud_resourses?.map((r: any) => getInstanceTypeFromMeta(r)))].filter((t) => t !== '-');
  const uniqueEngines = [...new Set(clusterSummary?.cloud_resourses?.map((r: any) => getEngineFromMeta(r)))].filter((e) => e !== '-');

  return (
    <Stack direction='column' gap={ds.space[2]}>
      {/* Card 1 — Total Instances + Instance Type chips + Engine chips */}
      <DSCard size='md' elevation='flat' header={<SectionTitle>Summary</SectionTitle>}>
        <Box sx={{ display: 'flex', alignItems: 'stretch', gap: ds.space[4] }}>
          <Stat
            id='rds-summary-total-instances'
            size='md'
            label='Total Instances'
            value={
              <Box component='span' className='stat-value-affordance' sx={{ transition: `color ${ds.motion.micro} ${ds.motion.ease}` }}>
                {formatNumber(clusterSummary?.cloud_resourses?.length ?? 0)}
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
          {(uniqueTypes.length > 0 || uniqueEngines.length > 0) && (
            <>
              <Box sx={{ width: '1px', backgroundColor: ds.gray[200], flexShrink: 0 }} />
              <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: ds.space[2] }}>
                {uniqueTypes.length > 0 && (
                  <Box>
                    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[2] }}>
                      {uniqueTypes.length} Instance {uniqueTypes.length === 1 ? 'Type' : 'Types'}
                    </Typography>
                    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
                      {uniqueTypes.map((type) => (
                        <Chip key={type as string} variant='tag' tone='neutral' size='sm'>
                          {type as string}
                        </Chip>
                      ))}
                    </Box>
                  </Box>
                )}
                {uniqueEngines.length > 0 && (
                  <Box>
                    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[2] }}>
                      {uniqueEngines.length} {uniqueEngines.length === 1 ? 'Engine' : 'Engines'}
                    </Typography>
                    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
                      {uniqueEngines.map((engine) => (
                        <Chip key={engine as string} variant='tag' tone='neutral' size='sm'>
                          {engine as string}
                        </Chip>
                      ))}
                    </Box>
                  </Box>
                )}
              </Box>
            </>
          )}
        </Box>
      </DSCard>

      {/* Card 2 — Instance type + engine detail table. Mirrors Recent Events:
          shows first 3 grouped rows; "View all" appears only when there's more.
          Card is hidden entirely when there are no instances. */}
      {tableData.length > 0 && (
        <DSCard size='md' elevation='flat' sx={{ py: 0, px: ds.space[3], overflow: 'hidden' }}>
          <CustomTable2
            tableHeadingCenter={['Priority']}
            id={_INSTANCE_TABLE_ID}
            headers={_INSTANCE_HEADERS}
            tableData={tableData.slice(0, 3)}
            rowsPerPage={3}
            onPageChange={undefined}
            loading={false}
            totalRows={tableData.length}
            showAllLink={tableData.length > 3}
            linkToShowAll={instancesUrl}
          />
        </DSCard>
      )}
    </Stack>
  );
};

const eventUrl = (accountId: string, serviceName: string) => {
  if (!accountId) return '';
  if (serviceName === 'Cloud SQL') return `/cloud-account/details/${accountId}#cloud-sql/events`;
  if (serviceName === 'AmazonRDS') return `/cloud-account/details/${accountId}#rds/events`;
  if (serviceName === 'microsoft.sql/servers') return `/cloud-account/details/${accountId}#sql/events`;
  if (serviceName === 'microsoft.sql/managedinstances') return `/cloud-account/details/${accountId}#sql-mi/events`;
  return '';
};

const RDSUtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const redirectUrl = eventUrl(accountId, serviceName);

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
          <Stat
            id='rds-summary-fired-alarm-count'
            size='md'
            label='Fired Alarm Count'
            value={clusterSummary?.events_aggregate?.aggregate?.count ?? '-'}
          />
        </DSCard>
        <DSCard size='md' elevation='flat' header={<SectionTitle>Optimizations</SectionTitle>}>
          <Stat
            id='rds-summary-optimize-count'
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
      .cloudAccountRDSSummary(accountId, { serviceName })
      .then((res) => {
        setSummary(res);
        setLoadingSummary(false);
      })
      .catch((error) => {
        console.error('Error fetching RDS summary:', error);
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
          <ClusterSummary accountId={accountId} clusterSummary={summary} />
          <RDSUtilizationAndHealth accountId={accountId} clusterSummary={summary} serviceName={serviceName} />
          <CloudCostSummary clusterSummary={summary} currencySymbol={currencySymbol} />
        </Box>
      )}
      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} />

      <ListingLayout id='rds-metrics'>
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
