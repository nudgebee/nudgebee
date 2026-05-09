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
import { CustomText, type ICustomTable2Row } from './Instances';
import Text from '@components1/common/format/Text';
import Charts from '@components1/common/charts/LineCharts';
import { formatMetricName } from '@utils/common';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { getLast7Days } from '@lib/datetime';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import DoughnutChart from '@components1/common/charts/DoughnutChart';

import TotalCostChart from '@components1/cloudaccount/CostChart';

const _INSTANCE_TABLE_ID = 'INSTANCE_TABLE_ID';
const _INSTANCE_HEADERS = ['Instances', 'Count', 'Name', 'Total Memory'];
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
  const isNumeric = typeof cluster.count === 'number' || /^\d+$/.test(String(cluster.count));
  return (
    <Box>
      <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'1px'}>
        {cluster.lable}
      </Typography>
      {isNumeric ? (
        <Typography color='#374151' fontSize={'24px'} lineHeight={'36px'} fontWeight={600}>
          {formatNumber(cluster.count)}
        </Typography>
      ) : (
        <Typography color='#374151' fontSize={'14px'} lineHeight={'22px'} fontWeight={600} sx={{ maxWidth: '220px', wordBreak: 'break-word' }}>
          {cluster.count}
        </Typography>
      )}
    </Box>
  );
};

// Multi-cloud label helpers
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

const InstanceTypeChip = ({ label }: { label: string }) => (
  <Box
    component='span'
    sx={{
      display: 'inline-block',
      px: '8px',
      py: '2px',
      borderRadius: '4px',
      backgroundColor: '#F3F4F6',
      border: '1px solid #E5E7EB',
      fontSize: '12px',
      fontWeight: 500,
      color: '#374151',
      lineHeight: '18px',
      whiteSpace: 'nowrap',
    }}
  >
    {label}
  </Box>
);

const StateLabel = ({ color, label, value, onClick }: { color: string; label: string; value: number; onClick?: () => void }) => (
  <Box
    display='flex'
    alignItems='center'
    onClick={onClick}
    sx={onClick ? { cursor: 'pointer', '&:hover .state-value': { color: '#3162D0' } } : undefined}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', flex: 1 }}>
      <Box component='span' sx={{ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: color, flexShrink: 0 }} />
      <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#737373', lineHeight: 1.3 }}>{label}</Typography>
    </Box>
    <Typography
      className='state-value'
      sx={{ fontSize: '13px', fontWeight: 500, color: '#374151', lineHeight: 1.3, textAlign: 'right', minWidth: '24px' }}
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
    // AWS
    if (resource.meta?.InstanceType) {
      return resource.meta.InstanceType;
    }
    // GCP - machine_type is stored with underscore (snake_case) and may be a full URL
    // e.g. "https://www.googleapis.com/compute/v1/projects/.../machineTypes/e2-micro"
    const gcpMachineType = resource.meta?.machine_type || resource.meta?.machineType;
    if (gcpMachineType) {
      return gcpMachineType.includes('/') ? gcpMachineType.split('/').pop() : gcpMachineType;
    }
    // GCP - try extracting from resource properties
    if (resource.machineType || resource.machine_type) {
      const machineType = resource.machineType || resource.machine_type;
      return machineType.includes('/') ? machineType.split('/').pop() : machineType;
    }
    // Azure
    if (resource.meta?.hardwareProfile?.vmSize) {
      return resource.meta.hardwareProfile.vmSize;
    }
    // Fallback - use resource type or service name (note: GCP uses service_name with underscore)
    return resource.resourceType || resource.service_name || resource.serviceName || 'N/A';
  };

  // Multi-cloud helper: check if instance is spot/preemptible
  const isSpotInstance = (resource: any) =>
    resource.meta?.InstanceLifecycle === 'spot' || // AWS
    resource.meta?.scheduling?.preemptible === true || // GCP
    resource.meta?.priority === 'Spot'; // Azure

  // Multi-cloud helper: get instance state
  const getInstanceState = (resource: any) => {
    // AWS
    if (resource.meta?.State?.Name) {
      return resource.meta.State.Name;
    }
    // Azure - check powerState from meta (set by collector) or instanceView statuses
    if (resource.meta?.powerState) {
      return resource.meta.powerState;
    }
    const instanceViewStatuses = resource.meta?.properties?.instanceView?.statuses || resource.meta?.instanceView?.statuses;
    if (instanceViewStatuses) {
      const powerState = instanceViewStatuses.find((s: any) => s.code?.startsWith('PowerState/'));
      if (powerState?.displayStatus) {
        return powerState.displayStatus.toLowerCase();
      }
    }
    // GCP / fallback - use resource status
    if (resource.status) {
      return resource.status.toLowerCase();
    }
    return '';
  };

  const instanceTypes = ec2Summary?.cloud_resourses?.map((resource: any) => getInstanceType(resource)) || [];
  const uniqueInstanceTypes = new Set(instanceTypes.filter((t: string) => t !== 'N/A' && t !== 'Unknown' && t !== 'Compute Engine'));
  const ebsVolumeCount = ec2Summary?.ebs_count?.aggregate?.count || 0;
  const nicsCount = ec2Summary?.nics_count?.aggregate?.count || ec2Summary?.cluster_data?.daemonSet || 0;
  // Use aggregate count if available (more accurate), fallback to array length
  const instancesCount = ec2Summary?.cloud_resourses_count || ec2Summary?.cloud_resourses?.length || 0;
  const [loading, _setLoading] = useState(false);
  const [instanceData, setInstanceData] = useState([]);
  const spotInstances = ec2Summary?.cloud_resourses?.filter((b: any) => isSpotInstance(b))?.length || 0;
  // For GCP/Azure where meta is empty, all non-spot instances are considered reserved
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

  const smallScreen = useMediaQuery('(max-width:1440px)');

  const handleNavigateToInstances = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '2');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/instances';
    router.push(url.toString());
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }
    const instanceGroupedData =
      ec2Summary?.cloud_resourses?.reduce((acc: any, current: any) => {
        const instanceType = getInstanceType(current);

        if (!acc[instanceType]) {
          acc[instanceType] = [];
        }

        acc[instanceType].push(current);
        return acc;
      }, {}) || {};

    const ec2ResourceData = Object.entries(instanceGroupedData)?.map((item: any) => {
      const data: ICustomTable2Row[] = [];
      const instanceTypeName = item[0];
      const instancesInGroup = item[1];

      // For GCP/Azure where meta is empty and all instances are grouped under service name,
      // use the aggregate count instead of the limited array length
      const isServiceNameFallback =
        instanceTypeName === 'Compute Engine' || instanceTypeName === 'microsoft.compute/virtualmachines' || instanceTypeName === 'N/A';
      const groupCount =
        isServiceNameFallback && Object.keys(instanceGroupedData).length === 1
          ? ec2Summary?.cloud_resourses_count || instancesInGroup.length
          : instancesInGroup.length;

      data.push({
        component: <CustomText text1={instanceTypeName} />,
      });
      data.push({
        component: <CustomText text1={groupCount} />,
      });
      const namesList = item[1].map((g: any) => g.name).join(', ');
      const maxNameLength = 80;
      const truncatedNames = namesList.length > maxNameLength ? namesList.substring(0, maxNameLength) + '...' : namesList;
      data.push({
        component: (
          <Box title={namesList} sx={{ cursor: namesList.length > maxNameLength ? 'pointer' : 'default' }}>
            <CustomText text1={truncatedNames} />
          </Box>
        ),
      });
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
    <Stack direction={'column'}>
      <Title title={'Summary'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
      <ClusterSummaryBlock
        sx={{
          padding: '18px 24px',
        }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: '48px',
            flexWrap: 'wrap',
          }}
        >
          <Box onClick={handleNavigateToInstances} sx={{ cursor: 'pointer', '&:hover .count-value': { color: '#3162D0' } }}>
            <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'1px'}>
              {labels.totalInstances}
            </Typography>
            <Typography className='count-value' color='#374151' fontSize={'24px'} lineHeight={'36px'} fontWeight={600}>
              {formatNumber(instancesCount)}
            </Typography>
          </Box>
          <ClusterBlock cluster={{ lable: labels.storageVolume, count: ebsVolumeCount || '-' }} />
          <ClusterBlock cluster={{ lable: labels.networkInterface, count: nicsCount || '-' }} />
        </Box>
        {uniqueInstanceTypes?.size > 0 && (
          <>
            <Box sx={{ borderTop: '1px dashed #E5E7EB', my: '14px' }} />
            <Box>
              <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'6px'}>
                {uniqueInstanceTypes.size} {uniqueInstanceTypes.size === 1 ? 'Instance Type' : labels.instanceType}
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
                {(Array.from(uniqueInstanceTypes) as string[]).map((type) => (
                  <InstanceTypeChip key={type} label={type} />
                ))}
              </Box>
            </Box>
          </>
        )}
      </ClusterSummaryBlock>

      <ClusterSummaryBlock>
        <Box display='flex' alignItems='center' gap='10px'>
          <DoughnutChart
            borderWidth={0}
            borderRadius={0}
            values={[spotInstances, reservedInstances]}
            labels={[labels.reservedLabel, labels.spotLabel]}
            displayValue={instancesCount}
            valueUnit=''
            colors={['#60A5FA', '#BFDBFE']}
            enableTooltip
            displayOnlyValueOnTooltip
            onItemClick={undefined}
          />
          <Box
            sx={{ display: 'flex', flexDirection: 'column', gap: '4px', marginBottom: '8px', paddingRight: '10px', borderRight: '1px solid #EBEBEB' }}
          >
            <StateLabel color='#60A5FA' label={labels.reservedLabel} value={reservedInstances} onClick={handleNavigateToInstances} />
            <StateLabel color='#BFDBFE' label={labels.spotLabel} value={spotInstances} onClick={handleNavigateToInstances} />
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
            colors={['#FBD961', '#4ADE80', 'orange', '#EF4444', '#EBEBEB', 'blue']}
            enableTooltip
            displayOnlyValueOnTooltip
            onItemClick={undefined}
          />
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', marginBottom: '8px' }}>
            <StateLabel color='#FBD961' label='Pending' value={pendingInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color='#4ADE80' label='Running' value={runningInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color='orange' label='Stopping' value={stoppingInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color='#EF4444' label='Stopped' value={stoppedInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color='#EBEBEB' label='Shutting Down' value={shuttingdownInstanceCount} onClick={handleNavigateToInstances} />
            <StateLabel color='blue' label='Terminated' value={terminatedInstanceCount} onClick={handleNavigateToInstances} />
          </Box>
        </Box>
      </ClusterSummaryBlock>

      <ClusterSummaryBlock
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: smallScreen ? 'space-between' : 'flex-start',
          flexWrap: 'wrap',
          gap: '28px',
        }}
      >
        <CustomTable2
          tableHeadingCenter={['Priority']}
          id={_INSTANCE_TABLE_ID}
          headers={_INSTANCE_HEADERS}
          tableData={instanceData}
          rowsPerPage={5}
          onPageChange={() => {
            return false;
          }}
          loading={loading}
          totalRows={5}
        />
      </ClusterSummaryBlock>
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
const UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState([]);
  const redirectUrl = eventUrl(accountId, serviceName);

  const _showEllipsis = true;

  // Navigate to Optimize subtab when clicking on Optimize Count
  const handleOptimizeClick = () => {
    const url = new URL(window.location.href);
    // Keep the current tab but change subtab to Optimize (1)
    url.searchParams.set('subtab', '1');
    // Update hash to include optimize path
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
              <Box sx={{ minWidth: _showEllipsis && '120px' }}>
                <Text showAutoEllipsis={_showEllipsis} value={item.subject_name} />
                {item.subject_namespace && <Text value={`service: ${item.subject_namespace}`} secondaryText />}
              </Box>
            ),
          });
          data.push({
            text: <Text value={item.aggregation_key} showAutoEllipsis />,
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
              <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151' }}>
                {clusterSummary?.events_aggregate?.aggregate?.count ?? '-'}
              </Typography>
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
                <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151' }}>
                  {clusterSummary?.recommendation_aggregate?.aggregate?.count ?? '-'}
                </Typography>
              </Box>
            </Box>
          </Box>
        </StackSummaryBlock>
      </Box>
      <StackSummaryBlock title={'Recent Events'}>
        <CustomTable2
          tableHeadingCenter={['Priority', 'Severity']}
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

export const OptimizeSummary = ({ accountId = '', serviceName = '', resourceId = '', showSummary = false }) => {
  const [loadingTrend, setLoadingTrend] = useState(false);
  const [loadingSummary, setLoadingSummary] = useState(false);
  const [renderMetricsData, setRenderMetricsData] = useState<any>({});
  const [summary, setSummary] = useState<any>({});
  const currencySymbol = useCurrencySymbol(accountId);
  const [selectedDateRange, _setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  useEffect(() => {
    if (!accountId) {
      return;
    }

    const fetchMetrics = async () => {
      setLoadingTrend(true);
      try {
        // Use direct database query (fast) instead of cloud_metric_groupings_v2 action
        // which calls cloud provider APIs (Azure Monitor/CloudWatch) in real-time and is very slow
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
      .cloudAccountEC2Summary(accountId, {
        serviceName: serviceName,
      })
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
    _setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const buildMetricChartData = (metricName: string, metricRows: any[]) => {
    const isCpu = metricName.replace(/[_\s]/g, '').toLowerCase() === 'cpuutilization';
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
        return isCpu ? val : formatMemory(val, 'bytes', 'gb', false);
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
      {showSummary && (loadingSummary || currencySymbol === undefined) && <SummarySkeletonLoader />}
      {showSummary && !(loadingSummary || currencySymbol === undefined) && summary && Object.keys(summary).length > 0 && (
        <Box sx={{ display: 'grid', gridTemplateColumns: '1.5fr 2fr 0.7fr', columnGap: '15px', rowGap: '20px', mb: '25px' }}>
          <ClusterSummary accountId={accountId} ec2Summary={summary} serviceName={serviceName} />
          <UtilizationAndHealth accountId={accountId} clusterSummary={summary} serviceName={serviceName} />
          <CostSummary clusterSummary={summary} currencySymbol={currencySymbol} />
        </Box>
      )}

      <TotalCostChart accountId={accountId} resourceServiceName={serviceName} resourceId={resourceId} />

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
