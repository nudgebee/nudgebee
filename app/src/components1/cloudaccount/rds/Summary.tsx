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
import BoxLayout2 from '@components1/common/BoxLayout2';
import { getLast7Days } from '@lib/datetime';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import TotalCostChart from '@components1/cloudaccount/CostChart';

const _INSTANCE_TABLE_ID = 'INSTANCE_TABLE_ID';
const _INSTANCE_HEADERS = ['Instance Type', 'Engine', 'Count', 'Memory', 'vCPU'];

// Static fallback specs for common AWS instance types (used when cloud_resource_details is empty)
// Specs based on AWS documentation - maps instance family.size to { memory_gb, vcpu }
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
  // Try direct lookup
  if (AWS_INSTANCE_SPECS[instanceType]) return AWS_INSTANCE_SPECS[instanceType];
  // Try stripping 'db.' prefix for RDS types
  if (instanceType.startsWith('db.')) {
    const stripped = instanceType.replace('db.', '');
    if (AWS_INSTANCE_SPECS[stripped]) return AWS_INSTANCE_SPECS[stripped];
  }
  return null;
}

// Multi-cloud field mapping helpers (aligned with Instances.tsx patterns)
function getInstanceTypeFromMeta(r: any): string {
  // AWS
  if (r?.meta?.DBInstanceClass) return r.meta.DBInstanceClass;
  // GCP Cloud SQL tier (e.g., "db-custom-4-15360", "db-n1-standard-4")
  if (r?.meta?.settings?.tier) return r.meta.settings.tier;
  // Azure
  if (r?.meta?.sku?.name) return r.meta.sku.name;
  return r?.meta?.InstanceType || '-';
}

function getEngineFromMeta(r: any): string {
  // AWS
  if (r?.meta?.Engine) return r.meta.Engine;
  // GCP (e.g., "POSTGRES_15" → "POSTGRES")
  if (r?.meta?.databaseVersion) return r.meta.databaseVersion.split('_')[0].toLowerCase();
  // Azure
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
  // db-custom-4-15360 → 4 vCPU, 15360 MB = ~15 GB
  const customMatch = /^db-custom-(\d+)-(\d+)$/.exec(tier);
  if (customMatch) {
    const vcpu = customMatch[1];
    const memMb = parseInt(customMatch[2], 10);
    const memGb = (memMb / 1024).toFixed(1).replace(/\.0$/, '');
    return { vcpu, memory_gb: memGb };
  }
  // db-n1-standard-4, db-n1-highmem-8, etc.
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

const ClusterSummary = ({ accountId, clusterSummary = {} }: any) => {
  const smallScreen = useMediaQuery('(max-width:1440px)');
  const router = useRouter();

  const [tableData, setTableData] = useState<any[][]>([[]]);

  const handleNavigateToInstances = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('subtab', '2');
    const currentHash = url.hash.split('/')[0] || '';
    url.hash = currentHash.replace('#', '') + '/instances';
    router.push(url.toString());
  };

  useEffect(() => {
    let stale = false;

    const buildTable = async () => {
      const resources = clusterSummary?.cloud_resourses || [];
      if (resources.length === 0) return;

      const summaryData: any = {};
      resources.forEach((r: any) => {
        const instanceType = getInstanceTypeFromMeta(r);
        const engine = getEngineFromMeta(r);
        const groupKey = `${instanceType}__${engine}`;

        if (!(groupKey in summaryData)) {
          // Try getting specs from AWS InstanceTypeDetails metadata
          let memory = r?.meta?.InstanceTypeDetails?.product?.attributes?.memory || '';
          let vcpu = r?.meta?.InstanceTypeDetails?.product?.attributes?.vcpu || '';

          // For GCP Cloud SQL, parse specs from the tier name (e.g., db-custom-4-15360)
          if (!memory && !vcpu) {
            const gcpSpecs = parseGcpTierSpecs(instanceType);
            if (gcpSpecs) {
              memory = `${gcpSpecs.memory_gb} GiB`;
              vcpu = gcpSpecs.vcpu;
            }
          }

          summaryData[groupKey] = {
            count: 0,
            instanceType,
            engine,
            memory,
            vcpu,
          };
        }
        summaryData[groupKey].count += 1;
      });

      // Tier 2: Fetch specs from cloud_resource_details for instance types missing Memory/vCPU
      const missingTypes = Object.values(summaryData)
        .filter((s: any) => !s.memory && !s.vcpu && s.instanceType !== '-')
        .map((s: any) => s.instanceType);

      if (missingTypes.length > 0) {
        try {
          const specsMap = await apiCloudAccount.getInstanceTypeSpecs({
            resourceTypes: missingTypes,
          });
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

      // Tier 3: Static fallback for known AWS instance types
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
        return [{ text: item.instanceType }, { text: item.engine }, { text: item.count }, { text: item.memory || '-' }, { text: item.vcpu || '-' }];
      });

      setTableData(data);
    };

    buildTable();
    return () => {
      stale = true;
    };
  }, [accountId, clusterSummary]);

  return (
    <Stack direction={'column'}>
      <Title title={'Summary'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
      <ClusterSummaryBlock
        sx={{
          padding: '18px 24px',
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'stretch' }}>
          <Box
            onClick={handleNavigateToInstances}
            sx={{ flex: '0 0 25%', display: 'flex', alignItems: 'center', cursor: 'pointer', '&:hover .count-value': { color: '#3162D0' } }}
          >
            <Box>
              <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'1px'}>
                Total Instances
              </Typography>
              <Typography className='count-value' color='#374151' fontSize={'24px'} lineHeight={'36px'} fontWeight={600}>
                {formatNumber(clusterSummary?.cloud_resourses?.length ?? 0)}
              </Typography>
            </Box>
          </Box>
          {(() => {
            const uniqueTypes = [...new Set(clusterSummary?.cloud_resourses?.map((r: any) => getInstanceTypeFromMeta(r)))].filter((t) => t !== '-');
            const uniqueEngines = [...new Set(clusterSummary?.cloud_resourses?.map((r: any) => getEngineFromMeta(r)))].filter((e) => e !== '-');
            if (uniqueTypes.length === 0 && uniqueEngines.length === 0) return null;
            return (
              <>
                <Box sx={{ width: '1px', backgroundColor: '#E5E7EB', flexShrink: 0 }} />
                <Box sx={{ flex: 1, pl: '24px', display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: '10px' }}>
                  {uniqueTypes.length > 0 && (
                    <Box>
                      <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'6px'}>
                        {uniqueTypes.length} Instance {uniqueTypes.length === 1 ? 'Type' : 'Types'}
                      </Typography>
                      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
                        {uniqueTypes.map((type) => (
                          <InstanceTypeChip key={type as string} label={type as string} />
                        ))}
                      </Box>
                    </Box>
                  )}
                  {uniqueEngines.length > 0 && (
                    <Box>
                      <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'6px'}>
                        {uniqueEngines.length} {uniqueEngines.length === 1 ? 'Engine' : 'Engines'}
                      </Typography>
                      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
                        {uniqueEngines.map((engine) => (
                          <InstanceTypeChip key={engine as string} label={engine as string} />
                        ))}
                      </Box>
                    </Box>
                  )}
                </Box>
              </>
            );
          })()}
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
          tableData={tableData}
          rowsPerPage={5}
          onPageChange={undefined}
          loading={false}
          totalRows={tableData.length}
        />
      </ClusterSummaryBlock>
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

const UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState([]);
  const redirectUrl = eventUrl(accountId, serviceName);

  const _showEllipsis = true;

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
        const ec2ResourceData =
          res.data?.events?.map((item: any) => {
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
          }) || [];
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
          tableHeadingCenter={['Priority']}
          id={EVENT_TABLE_ID}
          headers={EVENT_HEADERS}
          tableData={eventData}
          rowsPerPage={5}
          onPageChange={undefined}
          loading={loading}
          totalRows={eventData?.length}
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
            <Typography color='#737373' fontSize={'12px'}>
              Savings
            </Typography>
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
      .cloudAccountRDSSummary(accountId, {
        serviceName: serviceName,
      })
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
          <ClusterSummary accountId={accountId} clusterSummary={summary} />
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
