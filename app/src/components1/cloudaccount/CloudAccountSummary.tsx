import React, { useEffect, useState } from 'react';
import { Box, Button, Dialog, DialogContent, DialogTitle, IconButton, Stack, Tooltip, Typography, useMediaQuery } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import CloseIcon from '@mui/icons-material/Close';
import { useRouter } from 'next/router';
import Title from '@common/Title';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import TrendArrowPercentage from '@components1/common/widgets/TrendArrowPercentage';
import { formatNumber } from '@lib/formatter';
import DoughnutChartK8s from '@components1/common/charts/DoughnutChartK8s';
import Currency from '@components1/common/format/Currency';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import apiCloudAccount from '@api1/cloud-account';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import apiResources from '@api1/resources';
import apiHome from '@api1/home';
import type { ICustomTable2Row } from './ec2/Instances';
import Text from '@components1/common/format/Text';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import TotalCostChart from '@components1/cloudaccount/CostChart';
import { GetInsightIcon } from '@components1/common/GetInsightIcon';
import CustomButton from '@components1/common/NewCustomButton';
import { Modal } from '@components1/common/modal';
import { v4 as uuidv4 } from 'uuid';
import { StarsIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { getInsightRoute } from '@components1/k8s/common/insightRoutes';

const _INSTANCE_TABLE_ID = 'INSTANCE_TABLE_ID';
const _INSTANCE_HEADERS = ['Service Name', 'Account Name', 'Savings', 'Actions'];
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
          marginTop: '6px',
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

const ClusterBlock = ({ cluster = {}, onClick }: any) => {
  return (
    <Box
      onClick={onClick}
      sx={{
        cursor: onClick ? 'pointer' : 'default',
        '&:hover': onClick
          ? {
              '& .cluster-count': {
                color: '#3162D0',
              },
            }
          : {},
      }}
    >
      <Typography
        color='#737373'
        fontSize={'12px'}
        fontWeight={400}
        lineHeight={'14px'}
        mb={'1px'}
        sx={{
          wordBreak: 'break-word',
          overflowWrap: 'break-word',
          whiteSpace: 'normal',
        }}
      >
        {cluster.lable}
      </Typography>
      <Typography className='cluster-count' color='#374151' fontSize={'24px'} lineHeight={'36px'} fontWeight={600}>
        {formatNumber(cluster.count)}
      </Typography>
    </Box>
  );
};

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
        const insightsData = (res?.data?.data?.insight_v2?.rows || []).map((item: any) => {
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

  const getGridColumns = () => 'repeat(2, 1fr)';

  const renderServiceContent = () => {
    if (loadingServices) {
      return (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '20px',
            color: '#999',
            fontSize: '14px',
          }}
        >
          Loading services...
        </Box>
      );
    }
    if (activeServices.length > 0) {
      return (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: getGridColumns(),
            columnGap: '15px',
            rowGap: '10px',
            mb: '10px',
          }}
        >
          {activeServices.map((service, index) => (
            <StackSummaryBlock key={service.service_name || index}>
              <ClusterBlock
                cluster={{
                  lable: service.label,
                  count: service.count,
                }}
                onClick={() => navigateToService(service.service_name)}
              />
            </StackSummaryBlock>
          ))}
        </Box>
      );
    }
    return (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '20px',
          color: '#999',
          fontSize: '14px',
        }}
      >
        No active resources found
      </Box>
    );
  };

  return (
    <>
      <Stack direction={'column'}>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', columnGap: '15px', rowGap: '10px', mb: '10px' }}>
          <Stack direction={'column'}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Title
                title={'Resource Summary'}
                sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }}
                lightVariant={true}
                isUnderline={false}
              />
            </Box>
            {renderServiceContent()}
            {hasMoreServices && !loadingServices && (
              <Box
                sx={{
                  display: 'flex',
                  justifyContent: 'center',
                  marginTop: '15px',
                }}
              >
                <Button
                  variant='outlined'
                  size='small'
                  onClick={() => setIsModalOpen(true)}
                  sx={{
                    borderColor: '#3162D0',
                    color: '#3162D0',
                    fontSize: '12px',
                    textTransform: 'none',
                    '&:hover': {
                      borderColor: '#2451B6',
                      backgroundColor: 'rgba(49, 98, 208, 0.04)',
                    },
                  }}
                >
                  Show All Services ({allActiveServices.length})
                </Button>
              </Box>
            )}

            {(loadingInsights || insights.length > 0) && (
              <Box sx={{ mt: 3 }}>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
                  <Title title={'Insights'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
                </Box>
                {loadingInsights ? (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      padding: '20px',
                      color: '#999',
                      fontSize: '14px',
                    }}
                  >
                    Loading insights...
                  </Box>
                ) : (
                  <Box
                    sx={{
                      backgroundColor: 'rgba(255, 255, 255, 1)',
                      padding: '18px 24px 10px 24px',
                      borderRadius: '8px',
                      boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
                      display: 'grid',
                      gridTemplateColumns: '1fr',
                      gap: '8px',
                    }}
                  >
                    {insights.slice(0, 4).map((insight: any, index: number) => {
                      const insightText = insight.title || insight.label;
                      const route = getInsightRoute(insightText, accountId, cloudProvider, insight.rule);
                      return (
                        <Box
                          key={insight.id || index}
                          onClick={() => route && router.push(route)}
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '8px',
                            cursor: route ? 'pointer' : 'default',
                            borderRadius: '6px',
                            py: '2px',
                            px: '4px',
                            transition: 'background-color 0.15s ease',
                            '&:hover': route ? { backgroundColor: '#F3F4F6' } : {},
                          }}
                        >
                          <Box
                            sx={{
                              display: 'flex',
                              alignItems: 'center',
                              justifyContent: 'center',
                              height: '24px',
                              width: '24px',
                              backgroundColor: '#F8F8F8',
                              borderRadius: '4px',
                              flexShrink: 0,
                            }}
                          >
                            <SafeIcon src={insight.icon} alt='insight icon' width={16} height={16} />
                          </Box>
                          <Typography
                            sx={{
                              fontSize: '12px',
                              fontWeight: 400,
                              color: '#374151',
                              lineHeight: 1.4,
                            }}
                          >
                            {insightText}
                          </Typography>
                        </Box>
                      );
                    })}
                    {insights.length > 4 && (
                      <Box sx={{ display: 'flex', justifyContent: 'flex-start', marginTop: '10px' }}>
                        <CustomButton
                          text={`Show ${insights.length - 4} more`}
                          variant='secondary'
                          onClick={() => setIsInsightsModalOpen(true)}
                          size='xSmall'
                          sx={{
                            fontSize: '12px',
                            textTransform: 'none',
                          }}
                        />
                      </Box>
                    )}
                  </Box>
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
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px', fontSize: '17px', fontWeight: 600, color: '#374151' }}>
            <SafeIcon src={StarsIcon} alt='star icon' height={28} width={28} /> Insights
          </Box>
        }
        contentStyles={{
          padding: '24px 40px',
        }}
      >
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', gap: '1px', maxHeight: '60vh', overflowY: 'auto' }}>
          {insights.map((insight: any, index: number) => {
            const insightText = insight.title || insight.label;
            const route = getInsightRoute(insightText, accountId, cloudProvider, insight.rule);
            return (
              <Box
                key={insight.id || index}
                onClick={() => {
                  if (route) {
                    setIsInsightsModalOpen(false);
                    router.push(route);
                  }
                }}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  padding: '8px 4px',
                  cursor: route ? 'pointer' : 'default',
                  borderRadius: '6px',
                  transition: 'background-color 0.15s ease',
                  '&:hover': route ? { backgroundColor: '#F3F4F6' } : {},
                }}
              >
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    height: '24px',
                    width: '24px',
                    backgroundColor: '#F8F8F8',
                    borderRadius: '4px',
                    flexShrink: 0,
                  }}
                >
                  <SafeIcon src={insight.icon} alt='insight icon' width={16} height={16} />
                </Box>
                <Typography
                  sx={{
                    fontSize: '14px',
                    fontWeight: 400,
                    color: '#374151',
                    lineHeight: 1.4,
                  }}
                >
                  {insightText}
                </Typography>
              </Box>
            );
          })}
        </Box>
      </Modal>

      <Dialog
        open={isModalOpen}
        onClose={() => setIsModalOpen(false)}
        maxWidth='lg'
        fullWidth
        PaperProps={{
          sx: {
            borderRadius: '12px',
            minHeight: '550px',
          },
        }}
      >
        <DialogTitle sx={{ pb: 2, pr: 6 }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <Typography variant='h6' sx={{ fontSize: '18px', fontWeight: 600, color: '#374151' }}>
              All Services ({allActiveServices.length})
            </Typography>
            <IconButton
              onClick={() => setIsModalOpen(false)}
              sx={{
                position: 'absolute',
                right: 8,
                top: 8,
                color: '#9CA3AF',
                '&:hover': {
                  backgroundColor: 'rgba(156, 163, 175, 0.1)',
                },
              }}
            >
              <CloseIcon />
            </IconButton>
          </Box>
        </DialogTitle>
        <DialogContent sx={{ pt: 0 }}>
          {allActiveServices.length > 0 ? (
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: 'repeat(3, 1fr)',
                gap: '16px',
                maxHeight: '60vh',
                overflowY: 'auto',
                pr: 1,
              }}
            >
              {allActiveServices.map((service, index) => (
                <Box
                  key={service.service_name || index}
                  onClick={() => {
                    setIsModalOpen(false);
                    navigateToService(service.service_name);
                  }}
                  sx={{
                    border: '1px solid #E5E7EB',
                    borderRadius: '8px',
                    padding: '16px',
                    backgroundColor: '#FAFAFA',
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                    cursor: 'pointer',
                    '&:hover': {
                      borderColor: '#3162D0',
                      backgroundColor: '#F8FAFF',
                    },
                  }}
                >
                  <Typography
                    sx={{
                      fontSize: '14px',
                      fontWeight: 500,
                      color: '#374151',
                      mb: 1,
                      lineHeight: 1.2,
                    }}
                  >
                    {service.label}
                  </Typography>
                  <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1 }}>
                    <Typography
                      sx={{
                        fontSize: '24px',
                        fontWeight: 600,
                        color: '#1F2937',
                        lineHeight: 1,
                      }}
                    >
                      {formatNumber(service.count)}
                    </Typography>
                    <Typography
                      sx={{
                        fontSize: '12px',
                        color: '#6B7280',
                        fontWeight: 400,
                      }}
                    >
                      {service.count === 1 ? 'resource' : 'resources'}
                    </Typography>
                  </Box>
                  <Typography
                    sx={{
                      fontSize: '11px',
                      color: '#9CA3AF',
                      mt: 0.5,
                      fontFamily: 'monospace',
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
                color: '#9CA3AF',
                fontSize: '14px',
              }}
            >
              No services found
            </Box>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
};

const UtilizationAndHealth = ({ accountId, clusterSummary = {}, serviceName }: any) => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState([]);
  const _showEllipsis = true;

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
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '10px', width: '100%', minWidth: 0, overflow: 'hidden' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: '15px', rowGap: '20px', mb: '10px' }}>
        <StackSummaryBlock title={'Errors'}>
          <Box>
            <Typography color='#737373' fontSize={'12px'}>
              Fired Alarm Count
            </Typography>
            <Box display='flex' alignItems='center' gap={'7px'}>
              <Currency prefix='' value={clusterSummary?.events_aggregate?.aggregate?.count || '-'} />
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
                <Currency prefix='' value={clusterSummary?.recommendation_aggregate?.aggregate.count || '-'} />
              </Box>
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
          onPageChange={() => {
            return false;
          }}
          loading={loading}
          totalRows={5}
          showAllLink={true}
        />
      </StackSummaryBlock>
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
                title='Projected end-of-month cost based on current month-to-date spending. Percentage compares to last month gross usage (excludes credits).'
                arrow
              >
                <InfoOutlinedIcon sx={{ fontSize: 14, color: '#9CA3AF', cursor: 'help' }} />
              </Tooltip>
            </Box>
            <Box display='flex' alignItems='center' gap={'7px'}>
              {currentGrossSpend > 0 ? (
                <>
                  <Currency prefix={currencySymbol} value={monthlyForecast} />
                  {hasValidPercentage && Math.abs(percentageChange) < 1000 ? (
                    <TrendArrowPercentage sign={percentageChange > 0 ? -1 : 1} value={Math.abs(percentageChange)} />
                  ) : (
                    <Box sx={{ width: '12px' }} />
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
                <Currency prefix={currencySymbol} value={currentGrossSpend / new Date().getDate()} />
              ) : (
                <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#999', pt: '3px' }}>No data</Typography>
              )}
            </Stack>
            <br />
          </Box>
          {lastMonthCredits > 0 && (
            <Box>
              <Typography color='#737373' fontSize={'12px'}>
                Prev mo (net)
              </Typography>
              <Box display='flex' alignItems='center' gap={'7px'}>
                <Currency prefix={currencySymbol} value={lastMonthNetSpend} />
              </Box>
              <Stack direction={'row'}>
                <Typography sx={{ fontWeight: 400, fontSize: '10px', color: '#16A34A', pt: '3px', pr: '5px' }}>
                  Credits: -{currencySymbol}
                  {formatNumber(lastMonthCredits)}
                </Typography>
              </Stack>
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
            {clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings ? (
              <Currency prefix={currencySymbol} value={clusterSummary.recommendation_aggregate.aggregate.sum.estimated_savings * 12} suffix='/yr' />
            ) : (
              <Typography color='#999' fontSize={'10px'}>
                No savings available
              </Typography>
            )}
            <Typography color='#D0D0D0' fontSize={'10px'}>
              estimated 12 mos
            </Typography>
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
      </ClusterSummaryBlock>
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
            columnGap: '15px',
            rowGap: '20px',
            mb: '25px',
            mt: '80px',
          }}
        >
          <ClusterSummary accountId={accountId} cloudProvider={cloudProvider} />
          <UtilizationAndHealth accountId={accountId} clusterSummary={clusterSummary} />
          {!isCF && <CostSummary clusterSummary={clusterSummary} currencySymbol={currencySymbol} />}
        </Box>
      )}
      {!isCF && (
        <>
          <Title title={'Total Cost'} sx={{ fontSize: '14px', fontWeight: 500, color: '#374151' }} lightVariant={true} isUnderline={false} />
          <TotalCostChart accountId={accountId} />
        </>
      )}
    </>
  );
};

export default CloudAccountSummary;
