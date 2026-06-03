import React, { useEffect, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { formatMemory, formatNumber } from '@lib/formatter';
import CustomTable2 from '@common-new/tables/CustomTable2';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import type { ICustomTable2Row } from './Instances';
import { CustomText } from '@components1/cloudaccount/common';
import Text from '@common-new/format/Text';
import Charts from '@components1/common/charts/LineCharts';
import { formatMetricName } from '@utils/common';
import { getLast7Days } from '@lib/datetime';
import DoughnutChart from '@components1/common/charts/DoughnutChart';
import TotalCostChart from '@components1/cloudaccount/CostChart';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DSCard from '@components1/ds/Card';
import { Stat } from '@components1/ds/Stat';
import Chip from '@components1/ds/Chip';
import { ds } from '@utils/colors';
import { CloudCostSummary } from '@components1/cloudaccount/CloudCostSummary';
import { CloudRecentEvents } from '@components1/cloudaccount/CloudRecentEvents';

const _INSTANCE_TABLE_ID = 'INSTANCE_TABLE_ID';

// Column widths sized to typical values: Count is a single number (10%), Name
// is the longest free-form string (~50%), the rest are short labels (~20%).
const _INSTANCE_HEADERS = [
  { name: 'Instances', width: '20%' },
  { name: 'Count', width: '10%' },
  { name: 'Name', width: '40%' },
  { name: 'Total Memory', width: '30%' },
];

// Section heading style — matches CloudAccountSummary's `SectionHeading` helper.
// Rendered inside DSCard's `header` slot; DSCard owns the spacing below the heading
// (paddingBottom + borderBottom + marginBottom on the header wrapper).
const SectionTitle = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{children}</Typography>
);

// Multi-cloud label helpers — kept as-is from the legacy file; service name
// from the cloud collector drives which provider's terminology is used.
const CLOUD_LABEL_MAP: Record<string, Record<string, string>> = {
  AmazonEC2: {
    totalInstances: 'Total Instances',
    instanceType: 'Instance Types',
    storageVolume: 'EBS Volumes',
    networkInterface: 'ENIs',
    spotLabel: 'Spot',
    reservedLabel: 'Reserved',
  },
  'microsoft.compute/virtualmachines': {
    totalInstances: 'Total VMs',
    instanceType: 'VM Types',
    storageVolume: 'Managed Disks',
    networkInterface: 'NICs',
    spotLabel: 'Spot',
    reservedLabel: 'On-Demand',
  },
  'Compute Engine': {
    totalInstances: 'Total Instances',
    instanceType: 'Machine Types',
    storageVolume: 'Persistent Disks',
    networkInterface: 'NICs',
    spotLabel: 'Preemptible',
    reservedLabel: 'On-Demand',
  },
};

const DEFAULT_CLOUD_LABELS: Record<string, string> = {
  totalInstances: 'Total Instances',
  instanceType: 'Instance Types',
  storageVolume: 'Storage Volumes',
  networkInterface: 'Network Interfaces',
  spotLabel: 'Spot',
  reservedLabel: 'Reserved',
};

const getCloudLabels = (serviceName: string) => {
  const labels = CLOUD_LABEL_MAP[serviceName] || DEFAULT_CLOUD_LABELS;
  return labels;
};

// Tier C: chart palettes are hex literals because DoughnutChart renders to <canvas>,
// which cannot resolve CSS var() at paint time. Legend StateLabel colors share the
// same arrays so the legend swatches pair 1:1 with chart slices. Hues are
// semantically aligned with DS tokens; resync if DS palettes shift.
const RESERVATION_CHART_COLORS = ['#60A5FA', '#BFDBFE']; // ≈ ds.blue[400], ds.blue[200]
const INSTANCE_STATE_CHART_COLORS = ['#FBD961', '#4ADE80', 'orange', '#EF4444', '#EBEBEB', 'blue'];
// ≈ ds.amber[400], ds.green[400], ds.amber[600] (orange), ds.red[500], ds.gray[200], ds.blue[500]

const StateLabel = ({ color, label, value, onClick }: { color: string; label: string; value: number; onClick?: () => void }) => (
  <Box
    display='flex'
    alignItems='center'
    onClick={onClick}
    sx={onClick ? { cursor: 'pointer', '&:hover .state-value': { color: ds.blue[600] } } : undefined}
  >
    {/* Dot + label cluster on the left. `flex: 1` lets it take the row's
        remaining width so the value pin to the right edge. */}
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flex: 1 }}>
      <Box component='span' sx={{ width: '8px', height: '8px', borderRadius: ds.radius.sm, backgroundColor: color, flexShrink: 0 }} />
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], lineHeight: 1.3 }}>{label}</Typography>
    </Box>
    {/* Value is right-aligned in a min-24px column. Without `minWidth`, single-
        digit values (0, 1) sit flush against the label text — the visual gap
        the legacy file relied on disappears. `textAlign: 'right'` keeps the
        digit anchored to the right edge of the reserved column. */}
    <Typography
      className='state-value'
      sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], lineHeight: 1.3, textAlign: 'right', minWidth: '24px' }}
    >
      {value}
    </Typography>
  </Box>
);

const ClusterSummary = ({ accountId, ec2Summary = {}, serviceName = '' }: any) => {
  const labels = getCloudLabels(serviceName);
  const router = useRouter();

  // Multi-cloud helper: get instance type from different meta structures
  const getInstanceType = (resource: any) => {
    if (resource.meta?.InstanceType) return resource.meta.InstanceType;
    const gcpMachineType = resource.meta?.machine_type || resource.meta?.machineType;
    if (gcpMachineType) return gcpMachineType.includes('/') ? gcpMachineType.split('/').pop() : gcpMachineType;
    if (resource.machineType || resource.machine_type) {
      const machineType = resource.machineType || resource.machine_type;
      return machineType.includes('/') ? machineType.split('/').pop() : machineType;
    }
    if (resource.meta?.hardwareProfile?.vmSize) return resource.meta.hardwareProfile.vmSize;
    return resource.resourceType || resource.service_name || resource.serviceName || 'N/A';
  };

  const isSpotInstance = (resource: any) =>
    resource.meta?.InstanceLifecycle === 'spot' || resource.meta?.scheduling?.preemptible === true || resource.meta?.priority === 'Spot';

  const getInstanceState = (resource: any) => {
    if (resource.meta?.State?.Name) return resource.meta.State.Name;
    if (resource.meta?.powerState) return resource.meta.powerState;
    const instanceViewStatuses = resource.meta?.properties?.instanceView?.statuses || resource.meta?.instanceView?.statuses;
    if (instanceViewStatuses) {
      const powerState = instanceViewStatuses.find((s: any) => s.code?.startsWith('PowerState/'));
      if (powerState?.displayStatus) return powerState.displayStatus.toLowerCase();
    }
    if (resource.status) return resource.status.toLowerCase();
    return '';
  };

  const instanceTypes = ec2Summary?.cloud_resourses?.map((resource: any) => getInstanceType(resource)) || [];
  const uniqueInstanceTypes = new Set(instanceTypes.filter((t: string) => t !== 'N/A' && t !== 'Unknown' && t !== 'Compute Engine'));
  const ebsVolumeCount = ec2Summary?.ebs_count?.aggregate?.count || 0;
  const nicsCount = ec2Summary?.nics_count?.aggregate?.count || ec2Summary?.cluster_data?.daemonSet || 0;
  const instancesCount = ec2Summary?.cloud_resourses_count || ec2Summary?.cloud_resourses?.length || 0;
  const [loading, _setLoading] = useState(false);
  const [instanceData, setInstanceData] = useState([]);
  const spotInstances = ec2Summary?.cloud_resourses?.filter((b: any) => isSpotInstance(b))?.length || 0;
  const reservedInstances = instancesCount - spotInstances;
  const runningInstanceCount = ec2Summary?.cloud_resourses?.filter((b: any) => ['running', 'active'].includes(getInstanceState(b)))?.length || 0;
  const stoppedInstanceCount =
    ec2Summary?.cloud_resourses?.filter((b: any) => ['stopped', 'deallocated', 'inactive'].includes(getInstanceState(b)))?.length || 0;
  const pendingInstanceCount =
    ec2Summary?.cloud_resourses?.filter((b: any) => ['pending', 'provisioning', 'staging'].includes(getInstanceState(b)))?.length || 0;
  const stoppingInstanceCount =
    ec2Summary?.cloud_resourses?.filter((b: any) => ['stopping', 'suspending'].includes(getInstanceState(b)))?.length || 0;
  const shuttingdownInstanceCount =
    ec2Summary?.cloud_resourses?.filter((b: any) => ['shutting-down', 'deleting'].includes(getInstanceState(b)))?.length || 0;
  const terminatedInstanceCount =
    ec2Summary?.cloud_resourses?.filter((b: any) => ['terminated', 'deleted'].includes(getInstanceState(b)))?.length || 0;

  // "View all instances" deeplink — subtab=2 + `#…/instances` hash convention
  // shared across cloud-account Summary tabs. Populated post-mount via useEffect
  // to avoid an SSR hydration mismatch: a render-time `window` access would emit
  // `''` on the server but a real URL on the client, breaking React hydration.
  // Click handlers route through it; CustomTable2's `linkToShowAll` consumes it
  // via `window.open(linkToShowAll, '_blank')` (not an <a href>).
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
    if (!accountId) return;
    const instanceGroupedData =
      ec2Summary?.cloud_resourses?.reduce((acc: any, current: any) => {
        const instanceType = getInstanceType(current);
        if (!acc[instanceType]) acc[instanceType] = [];
        acc[instanceType].push(current);
        return acc;
      }, {}) || {};

    const ec2ResourceData = Object.entries(instanceGroupedData)?.map((item: any) => {
      const data: ICustomTable2Row[] = [];
      const instanceTypeName = item[0];
      const instancesInGroup = item[1];

      // For GCP/Azure where meta is empty and all instances are grouped under service name,
      // use the aggregate count instead of the limited array length.
      const isServiceNameFallback =
        instanceTypeName === 'Compute Engine' || instanceTypeName === 'microsoft.compute/virtualmachines' || instanceTypeName === 'N/A';
      const groupCount =
        isServiceNameFallback && Object.keys(instanceGroupedData).length === 1
          ? ec2Summary?.cloud_resourses_count || instancesInGroup.length
          : instancesInGroup.length;

      data.push({ component: <Text value={instanceTypeName} showAutoEllipsis /> });
      data.push({ component: <CustomText text1={groupCount} /> });
      const namesList = item[1].map((g: any) => g.name).join(', ');
      data.push({ component: <Text value={namesList} showAutoEllipsis lineClamp={1} /> });
      data.push({
        component: (
          <CustomText
            text1={formatMemory(
              item[1].reduce(
                (acc: any, item: any) =>
                  acc + (item?.meta?.InstanceTypeDetails?.MemoryInfo?.SizeInMiB || item?.meta?.machineTypeDetails?.memoryMb || 0),
                0
              ),
              'mb'
            )}
          />
        ),
      });
      return data;
    });
    setInstanceData(ec2ResourceData as any);
  }, [accountId, ec2Summary]);

  return (
    <Stack direction='column' gap={ds.space[2]}>
      {/* Card 1 — counts + instance type chips */}
      <DSCard size='md' elevation='flat' header={<SectionTitle>Summary</SectionTitle>}>
        <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[6], flexWrap: 'wrap' }}>
          <Stat
            id='ec2-summary-total-instances'
            size='md'
            label={labels.totalInstances}
            value={
              <Box component='span' className='stat-value-affordance' sx={{ transition: `color ${ds.motion.micro} ${ds.motion.ease}` }}>
                {formatNumber(instancesCount)}
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
          <Stat size='md' label={labels.storageVolume} value={ebsVolumeCount || '-'} />
          <Stat size='md' label={labels.networkInterface} value={nicsCount || '-'} />
        </Box>
        {uniqueInstanceTypes?.size > 0 && (
          <>
            <Box sx={{ borderTop: `1px dashed ${ds.gray[200]}`, my: ds.space[3] }} />
            <Box>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[2] }}>
                {uniqueInstanceTypes.size} {uniqueInstanceTypes.size === 1 ? 'Instance Type' : labels.instanceType}
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
                {(Array.from(uniqueInstanceTypes) as string[]).map((type) => (
                  <Chip key={type} variant='tag' tone='neutral' size='sm'>
                    {type}
                  </Chip>
                ))}
              </Box>
            </Box>
          </>
        )}
      </DSCard>

      {/* Card 2 — Reserved/Spot doughnut + Instance State doughnut */}
      <DSCard size='md' elevation='flat'>
        <Box display='flex' alignItems='center' gap={ds.space[3]} flexWrap='wrap'>
          <DoughnutChart
            borderWidth={0}
            borderRadius={0}
            values={[spotInstances, reservedInstances]}
            labels={[labels.reservedLabel, labels.spotLabel]}
            displayValue={instancesCount}
            valueUnit=''
            colors={RESERVATION_CHART_COLORS}
            enableTooltip
            displayOnlyValueOnTooltip
            onItemClick={undefined}
          />
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              gap: ds.space[1],
              marginBottom: ds.space[2],
              paddingRight: ds.space[2],
              borderRight: `1px solid ${ds.gray[200]}`,
            }}
          >
            <StateLabel
              color={RESERVATION_CHART_COLORS[0]}
              label={labels.reservedLabel}
              value={reservedInstances}
              onClick={handleNavigateToInstances}
            />
            <StateLabel color={RESERVATION_CHART_COLORS[1]} label={labels.spotLabel} value={spotInstances} onClick={handleNavigateToInstances} />
          </Box>
          <DoughnutChart
            borderWidth={0}
            borderRadius={0}
            values={[
              pendingInstanceCount,
              runningInstanceCount,
              stoppingInstanceCount,
              stoppedInstanceCount,
              shuttingdownInstanceCount,
              terminatedInstanceCount,
            ]}
            labels={['Pending', 'Running', 'Stopping', 'Stopped', 'Shutting Down', 'Terminated']}
            displayValue={instancesCount}
            valueUnit=''
            colors={INSTANCE_STATE_CHART_COLORS}
            enableTooltip
            displayOnlyValueOnTooltip
            onItemClick={undefined}
          />
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], marginBottom: ds.space[2] }}>
            <StateLabel color={INSTANCE_STATE_CHART_COLORS[0]} label='Pending' value={pendingInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color={INSTANCE_STATE_CHART_COLORS[1]} label='Running' value={runningInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color={INSTANCE_STATE_CHART_COLORS[2]} label='Stopping' value={stoppingInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color={INSTANCE_STATE_CHART_COLORS[3]} label='Stopped' value={stoppedInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel
              color={INSTANCE_STATE_CHART_COLORS[4]}
              label='Shutting Down'
              value={shuttingdownInstanceCount}
              onClick={handleNavigateToInstances}
            />
            <StateLabel
              color={INSTANCE_STATE_CHART_COLORS[5]}
              label='Terminated'
              value={terminatedInstanceCount}
              onClick={handleNavigateToInstances}
            />
          </Box>
        </Box>
      </DSCard>

      {/* Card 3 — instance type detail table. Mirrors Recent Events: shows first
          3 grouped rows; "View all" appears only when there's more to show.
          Card is hidden entirely when there are no instances at all. */}
      {instanceData.length > 0 && (
        <DSCard size='md' elevation='flat' sx={{ py: 0, px: ds.space[3], overflow: 'hidden' }}>
          <CustomTable2
            tableHeadingCenter={['Priority']}
            id={_INSTANCE_TABLE_ID}
            headers={_INSTANCE_HEADERS}
            tableData={instanceData.slice(0, 3)}
            rowsPerPage={3}
            onPageChange={undefined}
            loading={loading}
            totalRows={instanceData.length}
            showAllLink={instanceData.length > 3}
            linkToShowAll={instancesUrl}
          />
        </DSCard>
      )}
    </Stack>
  );
};

const eventUrl = (accountId: string, serviceName: string) => {
  if (!accountId) return '';
  if (serviceName === 'Compute Engine') return `/cloud-account/details/${accountId}#compute-engine/events`;
  if (serviceName === 'AmazonEC2') return `/cloud-account/details/${accountId}#ec2/events`;
  if (serviceName === 'microsoft.compute/virtualmachines') return `/cloud-account/details/${accountId}#vm/events`;
  return '';
};

const EC2UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
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
            id='ec2-summary-fired-alarm-count'
            size='md'
            label='Fired Alarm Count'
            value={
              <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[2] }}>
                <Box component='span'>{clusterSummary?.events_aggregate?.aggregate?.count ?? '-'}</Box>
                <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], whiteSpace: 'nowrap' }}>
                  last 7 days
                </Typography>
              </Box>
            }
          />
        </DSCard>
        <DSCard size='md' elevation='flat' header={<SectionTitle>Optimizations</SectionTitle>}>
          <Stat
            id='ec2-summary-optimize-count'
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

export const OptimizeSummary = ({ accountId = '', serviceName = '', resourceId = '', showSummary = false }) => {
  const [loadingTrend, setLoadingTrend] = useState(false);
  const [loadingSummary, setLoadingSummary] = useState(false);
  const [renderMetricsData, setRenderMetricsData] = useState<any>({});
  const [summary, setSummary] = useState<any>({});
  const currencySymbol = useCurrencySymbol(accountId);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

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
      .cloudAccountEC2Summary(accountId, { serviceName })
      .then((res) => {
        setSummary(res);
        setLoadingSummary(false);
      })
      .catch((error) => {
        console.error(error);
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
    const isCpu = metricName.replace(/[_\s]/g, '').toLowerCase() === 'cpuutilization';
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
        return isCpu ? val : formatMemory(val, 'bytes', 'gb', false);
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
      {showSummary && (loadingSummary || currencySymbol === undefined) && <SummarySkeletonLoader />}
      {showSummary && !(loadingSummary || currencySymbol === undefined) && summary && Object.keys(summary).length > 0 && (
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
          <ClusterSummary accountId={accountId} ec2Summary={summary} serviceName={serviceName} />
          <EC2UtilizationAndHealth accountId={accountId} clusterSummary={summary} serviceName={serviceName} />
          <CloudCostSummary clusterSummary={summary} currencySymbol={currencySymbol} />
        </Box>
      )}

      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} resourceId={resourceId} />

      <ListingLayout id='ec2-metrics'>
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
