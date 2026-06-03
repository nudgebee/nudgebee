import { useEffect, useState } from 'react';
import { Box, Stack, Typography, useMediaQuery } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { useRouter } from 'next/router';
// TODO(ds-migration): SummarySkeletonLoader is a page-level loader — replace with composed ds/Skeleton blocks.
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { formatNumber } from '@lib/formatter';
// TODO(ds-migration): DoughnutChartK8s is canvas/Chart.js code — no DS equivalent yet.
import DoughnutChartK8s from '@components1/common/charts/DoughnutChartK8s';
import Currency from '@common-new/format/Currency';
import CustomTable2 from '@common-new/tables/CustomTable2';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import apiResources from '@api1/resources';
import apiHome from '@api1/home';
import type { ICustomTable2Row } from './ec2/Instances';
import Text from '@common-new/format/Text';
import SeverityIcon from '@components1/ds/SeverityIcon';
import Datetime from '@common-new/format/Datetime';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import TotalCostChart from '@components1/cloudaccount/CostChart';
// TODO(ds-migration): GetInsightIcon is a leaf icon-resolver helper, not a UI component.
import { GetInsightIcon } from '@components1/common/GetInsightIcon';
import Modal from '@components1/ds/Modal';
import DSCard from '@components1/ds/Card';
import { v4 as uuidv4 } from 'uuid';
import { StarsIcon } from '@assets';
// TODO(ds-migration): SafeIcon is a pass-through img/svg renderer.
import SafeIcon from '@components1/common/SafeIcon';
import { getInsightRoute } from '@components1/k8s/common/insightRoutes';
import { ds } from '@utils/colors';
import { Stat } from '@components1/ds/Stat';
import Trend from '@components1/ds/Trend';
import { Button } from '@components1/ds/Button';
import Tooltip from '@components1/ds/Tooltip';

const _INSTANCE_TABLE_ID = 'INSTANCE_TABLE_ID';
const _INSTANCE_HEADERS = ['Service Name', 'Account Name', 'Savings', 'Actions'];
const EVENT_TABLE_ID = 'EVENT_TABLE_ID';
const EVENT_HEADERS = ['Instance ID', 'Events', 'Severity', 'Created at'];

// Bumps a Stat's label one weight notch (regular → medium) without touching value or sub.
// Selector targets the label Typography (`component='span'`) inside Stat's header row only —
// Stat's value uses `component='div'`, and `sub` Typographies live outside the header row.
const STAT_LABEL_BOLD_SX = {
  '& > div:first-of-type span.MuiTypography-root': { fontWeight: 'var(--ds-font-weight-medium)' },
};

// Section heading — replaces the legacy `Title` component (lightVariant, no underline).
const SectionHeading = ({ title, sx }: { title: string; sx?: object }) => (
  <Typography
    sx={{
      fontSize: ds.text.bodyLg,
      fontWeight: ds.weight.medium,
      color: ds.gray[700],
      ...sx,
    }}
  >
    {title}
  </Typography>
);

const getServiceDisplayName = (serviceName: string): string => {
  const serviceMap: { [key: string]: string } = {
    // AWS Services
    AmazonEC2: 'EC2 Instances',
    AmazonS3: 'S3 Buckets',
    AmazonRDS: 'RDS Instances',
    AmazonECS: 'ECS Services',
    AWSLambda: 'Lambda Functions',
    AmazonDynamoDB: 'DynamoDB Tables',
    AmazonCloudFront: 'CloudFront Distributions',
    AmazonEKS: 'EKS Clusters',
    AmazonElastiCache: 'ElastiCache Clusters',
    AmazonRedshift: 'Redshift Clusters',
    AmazonVPC: 'VPCs',
    AmazonEFS: 'EFS File Systems',
    AmazonRoute53: 'Route 53 Hosted Zones',
    AmazonCloudWatch: 'CloudWatch Alarms',
    AWSDirectConnect: 'Direct Connect Connections',
    AWSELB: 'Load Balancers',
    AmazonNeptune: 'Neptune Clusters',
    AmazonSageMaker: 'SageMaker Resources',
    AmazonElasticsearch: 'Elasticsearch Domains',
    AmazonMQ: 'Amazon MQ Brokers',
    AWSBatch: 'Batch Job Queues',
    AmazonFSx: 'FSx File Systems',
    AmazonLightsail: 'Lightsail Instances',
    AWSBackup: 'Backup Vaults',
    AWSConfig: 'Config Rules',
    AWSCloudTrail: 'CloudTrail Trails',
    // Azure Services
    'microsoft.compute/virtualmachines': 'Virtual Machines',
    'microsoft.sql/servers': 'SQL Databases',
    'microsoft.sql/managedinstances': 'SQL Managed Instances',
    'microsoft.storage/storageaccounts': 'Storage Accounts',
    'microsoft.containerservice/managedclusters': 'AKS Clusters',
    'microsoft.web/sites': 'App Services',
    'microsoft.network/virtualnetworks': 'Virtual Networks',
    'microsoft.network/loadbalancers': 'Load Balancers',
    'microsoft.cache/redis': 'Redis Cache',
    'microsoft.documentdb/databaseaccounts': 'Cosmos DB',
    'microsoft.keyvault/vaults': 'Key Vaults',
    // GCP Services
    'Compute Engine': 'Compute Instances',
    'Cloud SQL': 'Cloud SQL Instances',
    'Cloud Storage': 'Cloud Storage Buckets',
    'Kubernetes Engine': 'GKE Clusters',
    'Cloud Functions': 'Cloud Functions',
    BigQuery: 'BigQuery Datasets',
    'Cloud Pub/Sub': 'Pub/Sub Topics',
    'Cloud Spanner': 'Spanner Instances',
    'Cloud Memorystore': 'Memorystore Instances',
    'Cloud Run': 'Cloud Run Services',
    // Cloud Foundry Services
    apps: 'Apps',
    organizations: 'Organizations',
    spaces: 'Spaces',
    routes: 'Routes',
    service_instances: 'Service Instances',
  };
  return serviceMap[serviceName] || serviceName.replace(/Amazon|AWS|microsoft\.|google\./gi, '').trim() || serviceName;
};

// Maps the legacy 13-value `event.priority` enum to the DS canonical 5-level Severity axis.
// Reference: design-system/primitives/data-display/severity-icon.html — "New code MUST pick one
// of the canonical 5 levels via SeverityIcon2.tsx". The `ok`/`open` priorities are technically
// Status (not Severity) per the DS spec; collapsing them to `info` here is a pragmatic stop-gap
// — TODO(ds-status-axis): migrate Status-flavoured priorities to a `Label` component instead.
type CanonicalSeverityLevel = 'critical' | 'high' | 'medium' | 'low' | 'info';
const toCanonicalSeverityLevel = (priority: string | undefined): CanonicalSeverityLevel => {
  const p = (priority || '').toLowerCase();
  if (p === 'critical' || p === 'highest' || p === 'firing') return 'critical';
  if (p === 'high') return 'high';
  if (p === 'medium') return 'medium';
  if (p === 'low' || p === 'lowest') return 'low';
  return 'info';
};

// Replaces currency symbols in text with the target currency symbol.
const replaceCurrencyInText = (text: string, targetCurrencySymbol: string): string => {
  if (!text || targetCurrencySymbol === '$') return text;
  return text.replace(/\$(\d[\d,]*\.?\d*)/g, `${targetCurrencySymbol}$1`);
};

const ClusterSummary = ({ accountId = '', cloudProvider = '' }: any) => {
  const router = useRouter();
  const [dynamicServices, setDynamicServices] = useState<any[]>([]);
  const [loadingServices, setLoadingServices] = useState(false);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [displayLimit] = useState(6);
  const [rawInsights, setRawInsights] = useState<any[]>([]);
  const [loadingInsights, setLoadingInsights] = useState(false);
  const [isInsightsModalOpen, setIsInsightsModalOpen] = useState(false);
  const currencySymbol = useCurrencySymbol(accountId);

  const navigateToService = (serviceName: string) => {
    const url = new URL(window.location.href);
    url.searchParams.set('serviceName', serviceName);
    url.hash = 'services';
    router.push(url.pathname + url.search + url.hash);
  };

  // Process insights with correct currency symbol
  const insights = rawInsights.map((item: any) => {
    const symbol = currencySymbol || '$';
    return {
      ...item,
      title: replaceCurrencyInText(item.rawTitle, symbol),
      label: replaceCurrencyInText(item.rawLabel, symbol),
    };
  });

  useEffect(() => {
    if (!accountId) {
      return;
    }

    const fetchServices = async () => {
      setLoadingServices(true);
      try {
        // Use filtered resource counts to get actual resources (not billing line items)
        const res = await apiResources.getFilteredResourceCounts(accountId);
        const services = res.data || [];
        setDynamicServices(services);
      } catch (error) {
        console.error('Error fetching filtered resource counts:', error);
        // Fallback to old method if new one fails
        try {
          const fallbackRes: any = await apiResources.getResourceGroupings(
            100,
            0,
            { account_id: accountId },
            ['resource_service_name'],
            ['resource_service_name', 'count_resource'],
            { name: 'count_resource', order: 'desc' }
          );
          const services = (fallbackRes.data?.resource_groupings || []).map((item: any) => ({
            service_name: item.resource_service_name,
            count: item.count_resource || 0,
          }));
          setDynamicServices(services);
        } catch (fallbackError) {
          console.error('Fallback also failed:', fallbackError);
        }
      } finally {
        setLoadingServices(false);
      }
    };

    const fetchInsights = async () => {
      setLoadingInsights(true);
      try {
        const res = await apiHome.getInsights(accountId);
        const insightsData = (res?.data?.data?.insights_list?.rows || []).map((item: any) => {
          const id = uuidv4();
          return {
            ...item,
            id,
            // Store raw values for later currency processing
            rawTitle: item.title,
            rawLabel: item.label,
            icon: GetInsightIcon({ ...item, id }),
          };
        });
        setRawInsights(insightsData);
      } catch (error) {
        console.error('Error fetching insights:', error);
      } finally {
        setLoadingInsights(false);
      }
    };

    fetchServices();
    fetchInsights();
  }, [accountId]);

  const allActiveServices = dynamicServices.map((service) => ({
    service_name: service.service_name,
    label: getServiceDisplayName(service.service_name),
    count: service.count,
  }));

  const activeServices = allActiveServices.slice(0, displayLimit);
  const hasMoreServices = allActiveServices.length > displayLimit;

  const renderServiceContent = () => {
    if (loadingServices) {
      return (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: ds.space[5],
            color: ds.gray[500],
            fontSize: ds.text.bodyLg,
          }}
        >
          Loading services...
        </Box>
      );
    }
    if (activeServices.length > 0) {
      return (
        <DSCard size='md' header={<SectionHeading title='Resource Summary' />} elevation='flat'>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: 'repeat(2, 1fr)',
                columnGap: ds.space[4],
                rowGap: ds.space[3],
              }}
            >
              {activeServices.map((service, index) => (
                <Stat
                  key={service.service_name || index}
                  id={`cloud-summary-service-${service.service_name || index}`}
                  size='sm'
                  label={service.label}
                  value={
                    <Box component='span' className='stat-value-affordance' sx={{ transition: `color ${ds.motion.micro} ${ds.motion.ease}` }}>
                      {formatNumber(service.count)}
                    </Box>
                  }
                  onClick={() => navigateToService(service.service_name)}
                  sx={{
                    '&:hover': {
                      backgroundColor: 'transparent',
                      '& .stat-value-affordance': { color: ds.blue[600] },
                    },
                  }}
                />
              ))}
            </Box>
            {hasMoreServices && (
              <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Button data-testid='cloud-summary-show-all-services-btn' tone='secondary' size='sm' onClick={() => setIsModalOpen(true)}>
                  Show {allActiveServices.length - displayLimit} more
                </Button>
              </Box>
            )}
          </Box>
        </DSCard>
      );
    }
    return (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: ds.space[5],
          color: ds.gray[500],
          fontSize: ds.text.bodyLg,
        }}
      >
        No active resources found
      </Box>
    );
  };

  return (
    <>
      <Stack direction={'column'}>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', columnGap: ds.space[4], rowGap: ds.space[3], mb: ds.space[3] }}>
          <Stack direction={'column'} gap={ds.space[3]}>
            {renderServiceContent()}

            {(loadingInsights || insights.length > 0) && (
              <Box>
                {loadingInsights ? (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      padding: ds.space[5],
                      color: ds.gray[500],
                      fontSize: ds.text.bodyLg,
                    }}
                  >
                    Loading insights...
                  </Box>
                ) : (
                  <DSCard size='md' header={<SectionHeading title='Insights' />} elevation='flat'>
                    <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', gap: ds.space[2] }}>
                      {insights.slice(0, 4).map((insight: any, index: number) => {
                        const insightText = insight.title || insight.label;
                        const route = getInsightRoute(insightText, accountId, cloudProvider, insight.rule);
                        return (
                          <Box
                            key={insight.id || index}
                            data-testid={`cloud-summary-insight-${index}`}
                            onClick={() => route && router.push(route)}
                            sx={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: ds.space[2],
                              cursor: route ? 'pointer' : 'default',
                              borderRadius: ds.radius.sm,
                              py: ds.space[1],
                              px: ds.space[1],
                              transition: `background-color ${ds.motion.micro} ${ds.motion.ease}`,
                              '&:hover': route ? { backgroundColor: ds.gray[100] } : {},
                            }}
                          >
                            <Box
                              sx={{
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                height: '18px',
                                width: '24px',
                                backgroundColor: ds.background[200],
                                borderRadius: ds.radius.sm,
                                flexShrink: 0,
                              }}
                            >
                              <SafeIcon src={insight.icon} alt='insight icon' width={16} height={16} />
                            </Box>
                            <Typography
                              sx={{
                                fontSize: ds.text.small,
                                fontWeight: ds.weight.regular,
                                color: ds.gray[700],
                              }}
                            >
                              {insightText}
                            </Typography>
                          </Box>
                        );
                      })}
                      {insights.length > 4 && (
                        <Box sx={{ display: 'flex', justifyContent: 'flex-end', marginTop: ds.space[2] }}>
                          <Button
                            data-testid='cloud-summary-show-more-insights-btn'
                            tone='secondary'
                            size='sm'
                            onClick={() => setIsInsightsModalOpen(true)}
                          >
                            Show {insights.length - 4} more
                          </Button>
                        </Box>
                      )}
                    </Box>
                  </DSCard>
                )}
              </Box>
            )}
          </Stack>
        </Box>
      </Stack>

      <Modal
        width='sm'
        open={isInsightsModalOpen}
        onClose={() => setIsInsightsModalOpen(false)}
        title={
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[3],
              fontSize: ds.text.title,
              fontWeight: ds.weight.semibold,
              color: ds.gray[700],
            }}
          >
            <SafeIcon src={StarsIcon} alt='star icon' height={28} width={28} /> Insights
          </Box>
        }
        contentStyles={{
          padding: `${ds.space[5]} ${ds.space[6]}`,
        }}
      >
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', gap: ds.space[1], maxHeight: '60vh', overflowY: 'auto' }}>
          {insights.map((insight: any, index: number) => {
            const insightText = insight.title || insight.label;
            const route = getInsightRoute(insightText, accountId, cloudProvider, insight.rule);
            return (
              <Box
                key={insight.id || index}
                data-testid={`cloud-summary-insight-modal-row-${index}`}
                onClick={() => {
                  if (route) {
                    setIsInsightsModalOpen(false);
                    router.push(route);
                  }
                }}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: ds.space[2],
                  padding: `${ds.space[2]} ${ds.space[1]}`,
                  cursor: route ? 'pointer' : 'default',
                  borderRadius: ds.radius.sm,
                  transition: `background-color ${ds.motion.micro} ${ds.motion.ease}`,
                  '&:hover': route ? { backgroundColor: ds.gray[100] } : {},
                }}
              >
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    height: '24px',
                    width: '24px',
                    backgroundColor: ds.background[200],
                    borderRadius: ds.radius.sm,
                    flexShrink: 0,
                  }}
                >
                  <SafeIcon src={insight.icon} alt='insight icon' width={16} height={16} />
                </Box>
                <Typography
                  sx={{
                    fontSize: ds.text.bodyLg,
                    fontWeight: ds.weight.regular,
                    color: ds.gray[700],
                  }}
                >
                  {insightText}
                </Typography>
              </Box>
            );
          })}
        </Box>
      </Modal>

      <Modal
        width='lg'
        open={isModalOpen}
        onClose={() => setIsModalOpen(false)}
        title={
          <Typography
            sx={{
              fontSize: ds.text.heading,
              fontWeight: ds.weight.semibold,
              color: ds.gray[700],
            }}
          >
            All Services ({allActiveServices.length})
          </Typography>
        }
        contentStyles={{
          padding: `${ds.space[2]} ${ds.space[5]} ${ds.space[5]}`,
        }}
      >
        {allActiveServices.length > 0 ? (
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: 'repeat(3, 1fr)',
              gap: ds.space[4],
              maxHeight: '60vh',
              overflowY: 'auto',
              pr: ds.space[1],
            }}
          >
            {allActiveServices.map((service, index) => (
              <Box
                key={service.service_name || index}
                data-testid={`cloud-summary-all-services-card-${service.service_name || index}`}
                onClick={() => {
                  setIsModalOpen(false);
                  navigateToService(service.service_name);
                }}
                sx={{
                  border: `1px solid ${ds.gray[200]}`,
                  borderRadius: ds.radius.md,
                  padding: ds.space[4],
                  backgroundColor: ds.background[200],
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'flex-start',
                  cursor: 'pointer',
                  transition: `background-color ${ds.motion.micro} ${ds.motion.ease}, border-color ${ds.motion.micro} ${ds.motion.ease}`,
                  '&:hover': {
                    borderColor: ds.blue[600],
                    backgroundColor: ds.blue[100],
                  },
                }}
              >
                {/* Intentional tight lineHeights below (1.2 on label, 1 on number) override the DS-paired
                    line-heights to keep "label + big number + service-name caption" stacked tight inside
                    this small service card. Per typography.html the override is normally a Don't, but
                    layout-compaction for card-bound metric stacks is the documented exception. */}
                <Typography
                  sx={{
                    fontSize: ds.text.bodyLg,
                    fontWeight: ds.weight.medium,
                    color: ds.gray[700],
                    mb: ds.space[2],
                    lineHeight: 1.2,
                  }}
                >
                  {service.label}
                </Typography>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2] }}>
                  <Typography
                    sx={{
                      fontSize: ds.text.heading,
                      fontWeight: ds.weight.semibold,
                      color: ds.gray[700],
                      lineHeight: 1,
                    }}
                  >
                    {formatNumber(service.count)}
                  </Typography>
                  <Typography
                    sx={{
                      fontSize: ds.text.small,
                      color: ds.gray[600],
                      fontWeight: ds.weight.regular,
                    }}
                  >
                    {service.count === 1 ? 'resource' : 'resources'}
                  </Typography>
                </Box>
                <Typography
                  sx={{
                    fontSize: ds.text.caption,
                    color: ds.gray[500],
                    mt: ds.space[1],
                    fontFamily: ds.font.mono,
                  }}
                >
                  {service.service_name}
                </Typography>
              </Box>
            ))}
          </Box>
        ) : (
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'center',
              alignItems: 'center',
              minHeight: '200px',
              color: ds.gray[500],
              fontSize: ds.text.bodyLg,
            }}
          >
            No services found
          </Box>
        )}
      </Modal>
    </>
  );
};

const UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState([]);
  const _showEllipsis = true;

  // "View all events" deeplink — top-level Events tab on the cloud-account page
  // is reached via the `#events` hash (see [CloudAccountDetails].jsx tab config,
  // top-level fragment with value=0). Populated post-mount to avoid an SSR
  // hydration mismatch between server (`''`) and client (real URL).
  const [eventsUrl, setEventsUrl] = useState('');

  useEffect(() => {
    const url = new URL(window.location.href);
    url.hash = 'events';
    setEventsUrl(url.toString());
  }, []);

  const handleOptimizeClick = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('tab', '1');
    url.searchParams.set('subtab', '0');
    url.hash = 'optimize/right-sizing';
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
                <Text showAutoEllipsis value={item.subject_name || item.resource_id || item.id || 'N/A'} />
                {item.subject_namespace && <Text secondaryText value={`service: ${item.subject_namespace}`} />}
              </Box>
            ),
          });
          data.push({
            component: (
              <Box sx={{ minWidth: '150px', maxWidth: '220px' }}>
                <Text showAutoEllipsis={true} value={item.aggregation_key} />
              </Box>
            ),
            data: item.aggregation_key,
          });
          data.push({
            component: <SeverityIcon level={toCanonicalSeverityLevel(item.priority)} aria-label={`Severity: ${item.priority || 'unknown'}`} />,
            data: item.priority,
          });
          data.push({ component: <Datetime value={item.starts_at} />, data: item.starts_at });

          return data;
        });
        setEventData(ec2ResourceData as any);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [accountId, serviceName, _showEllipsis]);

  // Trend percentage for the alarm count vs last month spend ratio
  const projectedSpend = clusterSummary?.current_month_projected_spend || 0;
  const lastMonthSpend = clusterSummary?.last_month_spend || 0;
  const hasTrend = projectedSpend > 0 && lastMonthSpend > 0;
  const trendValue = hasTrend ? (Math.abs(lastMonthSpend - projectedSpend) * 100) / lastMonthSpend : 0;
  const trendSign: 1 | -1 = lastMonthSpend > projectedSpend ? 1 : -1;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], width: '100%', minWidth: 0, overflow: 'hidden' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[5], mb: ds.space[3] }}>
        <DSCard size='md' header={<SectionHeading title='Errors' />} elevation='flat'>
          <Stat
            id='cloud-summary-fired-alarm-count'
            size='md'
            label='Fired Alarm Count'
            value={
              <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[2] }}>
                <Box component='span'>{clusterSummary?.events_aggregate?.aggregate?.count ?? '-'}</Box>
                {hasTrend && <Trend sign={trendSign} value={trendValue} width='auto' />}
                <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], whiteSpace: 'nowrap' }}>
                  last 7 days
                </Typography>
              </Box>
            }
          />
        </DSCard>
        <DSCard size='md' header={<SectionHeading title='Optimizations' />} elevation='flat'>
          <Stat
            id='cloud-summary-optimize-count'
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
      <DSCard
        size='md'
        header={<SectionHeading title='Recent Events' />}
        elevation='flat'
        sx={{ px: ds.space[3], pb: ds.space[2], overflow: 'hidden' }}
      >
        <CustomTable2
          tableHeadingCenter={['Severity']}
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
          linkToShowAll={eventsUrl}
        />
      </DSCard>
    </Box>
  );
};

const CostSummary = ({ clusterSummary = {}, currencySymbol = '$' }: any) => {
  const smallScreen = useMediaQuery('(max-width:1440px)');

  // Use gross spend (positive amounts only) for percentage calculations to avoid division by near-zero when credits offset costs
  const currentGrossSpend =
    clusterSummary?.gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthGrossSpend =
    clusterSummary?.lm_gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.lm_spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthCredits = Math.abs(clusterSummary?.lm_credits_aggregate?.aggregate?.sum?.amount || 0);
  const lastMonthNetSpend = clusterSummary?.lm_spends_aggregate?.aggregate?.sum?.amount || 0;

  const monthlyForecast = getBudgetExpectedMonthlyExpense(currentGrossSpend);

  // Calculate percentage change using gross spend to avoid division by near-zero
  const hasValidPercentage = lastMonthGrossSpend > 0;
  const percentageChange = hasValidPercentage ? ((monthlyForecast - lastMonthGrossSpend) * 100) / lastMonthGrossSpend : 0;

  const renderForecastValue = () => {
    if (currentGrossSpend <= 0) {
      return <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption }}>No data available</Typography>;
    }
    return (
      <Box display='flex' alignItems='center' gap={ds.space[1]}>
        <Currency prefix={currencySymbol} value={monthlyForecast} />
        {hasValidPercentage && Math.abs(percentageChange) < 1000 && (
          <Trend sign={percentageChange > 0 ? -1 : 1} value={Math.abs(percentageChange)} width='auto' />
        )}
      </Box>
    );
  };

  return (
    <Stack gap={ds.space[2]}>
      <DSCard size='md' header={<SectionHeading title='Cost Summary' />} elevation='flat'>
        <Stack direction={'column'} gap={ds.space[4]}>
          <Stat
            id='cloud-summary-monthly-forecast'
            size='md'
            label='Monthly forecast'
            sx={STAT_LABEL_BOLD_SX}
            value={renderForecastValue()}
            info={{
              tooltip:
                'Projected end-of-month cost based on current month-to-date spending. Percentage compares to last month gross usage (excludes credits).',
            }}
            sub={
              lastMonthGrossSpend > 0 ? (
                <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[1] }}>
                  <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                    Prev mo (gross):
                  </Typography>
                  <Currency prefix={currencySymbol} value={lastMonthGrossSpend} />
                </Box>
              ) : (
                <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                  Prev mo (gross): No data
                </Typography>
              )
            }
          />
          <Stat
            id='cloud-summary-current-month'
            size='md'
            label='Current Month (MTD)'
            sx={STAT_LABEL_BOLD_SX}
            value={
              currentGrossSpend > 0 ? (
                <Currency prefix={currencySymbol} value={currentGrossSpend} />
              ) : (
                <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption }}>No data available</Typography>
              )
            }
            sub={
              currentGrossSpend > 0 ? (
                <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[1] }}>
                  <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                    Avg daily cost:
                  </Typography>
                  <Currency prefix={currencySymbol} value={currentGrossSpend / new Date().getDate()} />
                </Box>
              ) : (
                <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                  Avg daily cost: No data
                </Typography>
              )
            }
          />
          {lastMonthCredits > 0 && (
            <Stat
              id='cloud-summary-prev-month-net'
              size='md'
              label='Prev mo (net)'
              sx={STAT_LABEL_BOLD_SX}
              value={<Currency prefix={currencySymbol} value={lastMonthNetSpend} />}
              sub={
                <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.green[600] }}>
                  Credits: -{currencySymbol}
                  {formatNumber(lastMonthCredits)}
                </Typography>
              }
            />
          )}
        </Stack>
      </DSCard>
      <DSCard
        size='md'
        elevation='flat'
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: smallScreen ? 'space-between' : 'flex-start',
          flexWrap: 'wrap',
          gap: ds.space[6],
          border: `1px solid ${ds.green[200]}`,
          backgroundColor: ds.green[100],
        }}
      >
        <Box
          sx={{
            display: 'inherit',
            alignItems: 'inherit',
            justifyContent: 'inherit',
            gap: ds.space[5],
            flexGrow: smallScreen ? 1 : 0,
          }}
        >
          <Box display='flex' flexDirection='column'>
            <Box display='flex' alignItems='center' gap={ds.space[1]}>
              <Typography sx={{ color: ds.gray[600], fontSize: ds.text.small, fontWeight: 'var(--ds-font-weight-medium)' }}>Savings</Typography>
              <Tooltip
                title="Savings are estimated by the cloud provider based on the account's full usage. On newly connected accounts they may appear higher than visible spend until cost reports accumulate enough history."
                arrow
              >
                <InfoOutlinedIcon sx={{ fontSize: 14, color: ds.gray[400], cursor: 'help' }} />
              </Tooltip>
            </Box>
            {clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings ? (
              <Currency prefix={currencySymbol} value={clusterSummary.recommendation_aggregate.aggregate.sum.estimated_savings * 12} suffix='/yr' />
            ) : (
              <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption }}>No savings available</Typography>
            )}
            <Typography sx={{ color: ds.gray[400], fontSize: ds.text.caption, whiteSpace: 'nowrap' }}>estimated 12 mos</Typography>
          </Box>
          <DoughnutChartK8s
            size={'60px'}
            value={(() => {
              if (!clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings) {
                return 0;
              }

              const yearlyGrossSpend =
                clusterSummary?.yearly_gross_spends_aggregate?.aggregate?.sum?.amount ||
                clusterSummary?.yearly_spends_aggregate?.aggregate?.sum?.amount ||
                0;
              const yearlyExpense = getExpectedYearlyExpense(currentGrossSpend, yearlyGrossSpend);

              // Handle edge case: if yearly expense is too small (less than $1), return 0
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
      </DSCard>
    </Stack>
  );
};

const CloudAccountSummary = ({ accountId = '', clusterSummary = {}, loading = false, cloudProvider = '' }) => {
  const currencySymbol = useCurrencySymbol(accountId);
  const isCF = cloudProvider === 'CloudFoundry';

  if ((!accountId || !clusterSummary) && !loading) {
    return <>No Data Available!</>;
  }

  return (
    <>
      {loading || currencySymbol === undefined ? (
        <SummarySkeletonLoader />
      ) : (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: isCF ? '1.5fr 2fr' : '1.5fr 2fr 0.7fr',
            alignItems: 'start',
            columnGap: ds.space[4],
            rowGap: ds.space[5],
            mb: ds.space[6],
            mt: ds.space[7],
          }}
        >
          <ClusterSummary accountId={accountId} cloudProvider={cloudProvider} />
          <UtilizationAndHealth accountId={accountId} clusterSummary={clusterSummary} />
          {!isCF && <CostSummary clusterSummary={clusterSummary} currencySymbol={currencySymbol} />}
        </Box>
      )}
      {!isCF && (
        <>
          <TotalCostChart accountId={accountId} />
        </>
      )}
    </>
  );
};

export default CloudAccountSummary;
