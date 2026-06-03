import React, { useState, useEffect } from 'react';
import { Box, Stack, Typography, Grid, Divider } from '@mui/material';
import DSModal from '@components1/ds/Modal';
import ClusterNode from './common/ClusterNode';
import Title from '@common/Title';
import { formatNumber } from '@lib/formatter';
import Currency from '@common-new/format/Currency';
import KubernetesMemoryCpuOverView from '@components1/k8s/common/KubernetesMemoryCpuOverView';
import PropTypes from 'prop-types';
import DSCard from '@components1/ds/Card';
import { Stat } from '@components1/ds/Stat';
import DSTooltip from '@components1/ds/Tooltip';
import DSLink from '@components1/ds/Link';
import HeadingWithBorder from '@common-new/HeadingWithBorder';
import { RecentErrorIcon, MatricsIcon, ServiceMapsIcon, PodsIcon, StarsIcon, ApplicationsIconblue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import apiKubernetes1 from '@api1/kubernetes1';
import { getLast24Hrs } from '@lib/datetime';
import EC2Icon from '@assets/cloud-account/ec2-icon.icon.svg';
import RDSIcon from '@assets/cloud-account/rds-icon.icon.svg';
import ServiceIcon from '@assets/cloud-account/service-icon.icon.svg';
import TraceIcon from '@assets/home/traces-icon.icon.svg';
import SecurityIcon from '@assets/home/security-icon.icon.svg';
import LogsIcon from '@assets/home/logs-icon.icon.svg';
import { useData } from '@context/DataContext';
import { v4 as uuidv4 } from 'uuid';
import { colorsArray } from 'src/utils/common';
import { GetInsightIcon } from '@components1/common/GetInsightIcon';
import CustomOptimizationsSummaryCard from './common/CustomOptimizationsSummaryCard';
import SecondaryLink from '@components1/common/SecondaryLink';
import HighlightText from './common/HighlightComponent';
import { useRouter } from 'next/router';
import { Button as DSButton } from '@components1/ds/Button';
import { getInsightRoute } from './common/insightRoutes';
import Link from 'next/link';

export const SummaryBlock = ({ title, children, greenColor, redColor, sx, hideTitle = false, height = '' }) => {
  const getBorderColor = (greenColor, redColor) => {
    if (greenColor) {
      return '#C1ECC0';
    }
    if (redColor) {
      return '#FFD9D9';
    }
    return '#3162D04D';
  };
  const getBgColor = (greenColor, redColor) => {
    if (greenColor) {
      return '#F7FFF6';
    }
    if (redColor) {
      return '#FFF9F9';
    }
    return '#F3F6FD';
  };

  const borderColor = getBorderColor(greenColor, redColor);
  const backgroundColor = getBgColor(greenColor, redColor);

  return (
    <Box
      display='flex'
      flexDirection='column'
      justifyContent='flex-start'
      sx={{
        height: height,
      }}
    >
      {!hideTitle && <Title title={title} fontSize={'16px'} height={'2px'} />}
      <Box
        sx={{
          border: '1px solid',
          borderColor: borderColor,
          backgroundColor: backgroundColor,
          padding: redColor ? '9px 20px' : '16px 24px',
          borderRadius: '10px',
          marginTop: hideTitle ? 0 : '10px',
          ...sx,
        }}
      >
        {children}
      </Box>
    </Box>
  );
};
SummaryBlock.propTypes = {
  title: PropTypes.any,
  children: PropTypes.any,
  greenColor: PropTypes.bool,
  redColor: PropTypes.bool,
  sx: PropTypes.object,
  hideTitle: PropTypes.bool,
  height: PropTypes.any,
};

export const ClusterSummaryBlock = ({ children, sx }) => {
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

ClusterSummaryBlock.propTypes = {
  children: PropTypes.any,
  sx: PropTypes.object,
};

export const PotentialSavings = ({ clusterSummary = {} }) => {
  return (
    <SummaryBlock
      title='Potential savings'
      hideTitle={false}
      greenColor
      sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '18px' }}
    >
      <Box display='flex' flexDirection='column' alignItems='flex-end'>
        <Typography color='#737373' fontSize={'14px'}>
          Savings{''}
        </Typography>
        <Currency sx={{ color: '#2F4267', fontSize: '36px', fontWeight: 600 }} value={clusterSummary?.yearly_recommendation_saving} suffix='/yr' />
      </Box>
    </SummaryBlock>
  );
};

PotentialSavings.propTypes = {
  clusterSummary: PropTypes.any,
};

const ClusterBlock = ({ cluster = {} }) => {
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
ClusterBlock.propTypes = {
  cluster: PropTypes.any,
};

const ClusterSummary = ({ clusterSummary = {}, accountId }) => {
  const [firingSlo, setFiringSlo] = useState(0);
  const [totalSlo, setTotalSlo] = useState(0);
  const [firingWorkloads, setFiringWorkloads] = useState([]);

  const totalNodes = clusterSummary?.cluster_data?.node_count || 0;
  const podStatusArray = Object.entries(clusterSummary?.cluster_data?.pod_status_counts ?? {})
    .filter(([, count]) => count > 0)
    .map(([type, count]) => ({
      type,
      count,
    }))
    .sort((a, b) => b.count - a.count);

  const totalPodCount = Object.values(clusterSummary?.cluster_data?.pod_status_counts ?? {})
    .filter((count) => count > 0)
    .reduce((sum, count) => sum + count, 0);
  const kindArray = Object.entries(clusterSummary?.cluster_data?.workload_type_counts ?? {})
    .filter(([_, count]) => count > 0)
    .map(([type, count]) => ({
      type,
      count,
    }))
    .sort((a, b) => b.count - a.count);
  const totalKindCount = Object.values(clusterSummary?.cluster_data?.workload_type_counts ?? {})
    .filter((count) => count > 0)
    .reduce((sum, count) => sum + count, 0);
  const router = useRouter();

  useEffect(() => {
    if (!accountId) {
      return;
    }

    const fetchSLOData = async () => {
      try {
        const last24Hours = getLast24Hrs(new Date()).toISOString();

        // Fetch configured SLOs created in last 24 hours
        const configResponse = await apiKubernetes1.listSLOConfigs({
          cloud_account_id: accountId,
          created_after: last24Hours,
        });
        const configuredSLOs = configResponse?.data?.data?.slo_config || [];
        const configuredWorkloads = new Set();
        configuredSLOs.forEach((config) => {
          const key = `${config.workload_namespace}/${config.workload_name}`;
          configuredWorkloads.add(key);
        });
        const totalConfiguredCount = configuredWorkloads.size;
        setTotalSlo(totalConfiguredCount);

        // Fetch SLO observations for last 24 hours to get firing status
        const observationResponse = await apiKubernetes1.getSLOObservation({
          accountId,
          timestamp: last24Hours,
        });
        const sloResponseData = observationResponse?.data?.data?.slo_report_observation_v2?.rows || [];

        if (sloResponseData.length > 0) {
          const statusMap = {};
          sloResponseData.forEach((item) => {
            const key = `${item.workload_namespace}/${item.workload_name}`;
            if (!statusMap[key]) {
              statusMap[key] = item.status;
            } else if (item.status === 'FIRING') {
              statusMap[key] = 'FIRING';
            }
          });

          const firingArray = Object.entries(statusMap)
            .filter(([, status]) => status === 'FIRING')
            .map(([key]) => key);

          setFiringSlo(firingArray.length);
          setFiringWorkloads(firingArray);
        } else {
          setFiringSlo(0);
          setFiringWorkloads([]);
        }
      } catch (error) {
        console.error(error);
      }
    };

    fetchSLOData();
  }, [accountId]);

  return (
    <Stack direction={'column'}>
      <DSCard variant='accent' tone='success' size='md'>
        <HeadingWithBorder
          value='Cluster Summary'
          borderWidth='3px'
          borderColor='var(--ds-blue-500)'
          sx={{
            '& p': {
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
            },
          }}
        />
        <Box
          sx={{
            borderRadius: 'var(--ds-radius-sm)',
            minHeight: '50px',
            backgroundColor: 'var(--ds-background-100)',
            padding: 'var(--ds-space-4) 0px',
            mt: 'var(--ds-space-4)',
          }}
        >
          <Box
            display={'grid'}
            gridTemplateColumns={'0.6fr 5px 2fr'}
            gap='20px'
            sx={{
              '@media (max-width: 1350px)': {
                gap: '10px',
              },
            }}
          >
            <Stat
              label='Nodes'
              value={totalNodes}
              size='md'
              onClick={() => router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#kubernetes/nodes`)}
            />
            <Divider orientation='vertical' flexItem />
            <ClusterNode
              largeVariant
              node={{
                demand: clusterSummary?.cluster_data?.ondemand_node_count || 0,
                fallback: clusterSummary?.cluster_data?.fallback_node_count || 0,
                spot: clusterSummary?.cluster_data?.spot_node_count || 0,
              }}
              clusterSummary={true}
              width='100%'
              updatedNode
              accountId={accountId}
            />
            <></>
          </Box>
        </Box>
        <Divider sx={{ backgroundColor: 'var(--ds-gray-200)', mt: '5px' }} />
        <Box
          sx={{
            borderRadius: 'var(--ds-radius-sm)',
            minHeight: '50px',
            backgroundColor: 'var(--ds-background-100)',
            padding: 'var(--ds-space-4) 0px',
            mt: 'var(--ds-space-4)',
          }}
        >
          <Box
            display={'grid'}
            gridTemplateColumns={'0.6fr 5px 2fr'}
            gap='20px'
            sx={{
              '@media (max-width: 1350px)': {
                gap: '10px',
              },
            }}
          >
            <Stat
              label='Applications'
              value={totalKindCount}
              size='md'
              onClick={() => router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#kubernetes/applications`)}
            />
            <Divider orientation='vertical' flexItem />
            <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} width={'100%'} gap={'20px'} rowGap={'2px'}>
              {kindArray && kindArray.length > 0 ? (
                kindArray.map((kind) => (
                  <SecondaryLink
                    key={kind.type}
                    onClick={() => router.push(`/kubernetes/details/${accountId}?workloadType=${kind.type}#kubernetes/applications`)}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      width: '100%',
                      gap: '10px',
                      color:
                        kind.type === 'Deployment'
                          ? 'var(--ds-blue-500)'
                          : kind.type === 'DaemonSet'
                          ? 'var(--ds-amber-400)'
                          : kind.type === 'Job'
                          ? 'var(--ds-green-500)'
                          : 'var(--ds-gray-400)',
                    }}
                  >
                    <Typography
                      sx={{
                        color: 'var(--ds-gray-500)',
                        fontSize: 'var(--ds-text-caption)',
                        '& span': {
                          fontWeight: 'var(--ds-font-weight-medium)',
                          color: 'var(--ds-gray-700)',
                          paddingLeft: '5px',
                        },
                      }}
                    >
                      {kind.type}
                    </Typography>
                    <Typography
                      id={`${kind.type}-count`}
                      variant='h4'
                      sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}
                    >
                      {kind.count}
                    </Typography>
                  </SecondaryLink>
                ))
              ) : (
                <></>
              )}
            </Box>
          </Box>
        </Box>
        <Divider sx={{ backgroundColor: 'var(--ds-gray-200)', mt: '5px' }} />

        <Box
          sx={{
            borderRadius: 'var(--ds-radius-sm)',
            minHeight: '50px',
            backgroundColor: 'var(--ds-background-100)',
            padding: 'var(--ds-space-4) 0px',
            mt: 'var(--ds-space-4)',
          }}
        >
          <Box
            display={'grid'}
            gridTemplateColumns={'0.6fr 5px 2fr'}
            gap='20px'
            sx={{
              '@media (max-width: 1350px)': {
                gap: '10px',
              },
            }}
          >
            <Stat
              label='Pods'
              value={totalPodCount}
              size='md'
              onClick={() => router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#kubernetes/pods`)}
            />
            <Divider orientation='vertical' flexItem />
            <Box
              display={'grid'}
              gridTemplateColumns={'1fr 1fr'}
              width={'100%'}
              gap={'12px'}
              rowGap={'2px'}
              flexWrap={'wrap'}
              sx={{
                '@media (max-width: 1200px)': {
                  gridTemplateColumns: '1fr',
                },
              }}
            >
              {podStatusArray && podStatusArray.length > 0
                ? podStatusArray.map((p, index) => {
                    // Always assign red for Failed type
                    const backgroundColor =
                      p.type === 'Failed'
                        ? 'var(--ds-red-500)'
                        : p.type === 'Running'
                        ? 'var(--ds-green-400)'
                        : colorsArray[index % colorsArray.length];
                    return (
                      <Box key={`${p.type}-box`} display={'flex'} alignItems={'center'} justifyContent={'space-between'} width={'100%'} gap={'6px'}>
                        <Box display={'flex'} alignItems={'center'}>
                          <Box
                            sx={{
                              height: '6px',
                              width: '6px',
                              backgroundColor: backgroundColor,
                              borderRadius: '2px',
                              display: 'inline-block',
                              mr: '6px',
                            }}
                          />
                          <Typography
                            sx={{
                              color: 'var(--ds-gray-500)',
                              fontSize: 'var(--ds-text-caption)',
                              '& span': {
                                fontWeight: 'var(--ds-font-weight-medium)',
                                color: 'var(--ds-gray-700)',
                                pl: '5px',
                              },
                            }}
                          >
                            {p.type}
                          </Typography>
                        </Box>
                        <Typography
                          id={`${p.type}-count`}
                          variant='h4'
                          sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}
                        >
                          {p.count}
                        </Typography>
                      </Box>
                    );
                  })
                : null}
            </Box>
          </Box>
        </Box>
        <Divider sx={{ backgroundColor: 'var(--ds-gray-100)', mt: '5px' }} />

        <Box
          sx={{
            borderRadius: 'var(--ds-radius-sm)',
            minHeight: '50px',
            backgroundColor: 'var(--ds-background-100)',
            padding: 'var(--ds-space-4) 0px',
            mt: 'var(--ds-space-4)',
          }}
        >
          <Box
            display={'grid'}
            gridTemplateColumns={'0.8fr 5px 1fr 1fr'}
            gap='20px'
            sx={{
              '@media (max-width: 1350px)': {
                gap: '10px',
              },
            }}
          >
            <Box>
              <Box sx={{ display: 'flex' }}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-500)' }}>
                  SLO
                </Typography>
              </Box>
              <DSTooltip
                placement='top'
                title={
                  firingWorkloads.length > 0 ? (
                    <div>
                      <span style={{ fontWeight: 'bold', marginBottom: 4 }}>SLO Status</span>
                      <div style={{ fontWeight: 'bold', marginBottom: 4 }}>Attention: {firingSlo} Firing SLO (Last 24 Hours)</div>
                      {firingWorkloads.slice(0, 10).map((workload) => (
                        <div key={workload}>{workload}</div>
                      ))}
                      {firingWorkloads.length > 10 && <div>...and {firingWorkloads.length - 10} more workloads</div>}
                      <div style={{ fontWeight: 'bold', marginTop: 4 }}>{totalSlo} SLO Configured (Last 24 Hours)</div>
                    </div>
                  ) : (
                    ''
                  )
                }
              >
                <Typography
                  variant='h4'
                  sx={{
                    fontSize: 'var(--ds-text-heading)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: 'var(--ds-gray-700)',
                    cursor: firingSlo > 0 ? 'pointer' : 'default',
                  }}
                  onClick={() => {
                    if (firingSlo > 0) {
                      router.push(`/kubernetes/details/${accountId}#monitoring/slo`);
                    }
                  }}
                >
                  <span style={{ color: firingSlo > 0 ? 'var(--ds-red-500)' : 'var(--ds-gray-700)' }}>{firingSlo}</span> / {totalSlo}
                </Typography>
              </DSTooltip>
            </Box>
          </Box>
        </Box>
      </DSCard>
    </Stack>
  );
};

ClusterSummary.propTypes = {
  clusterSummary: PropTypes.any,
  accountId: PropTypes.string,
};

const HealthBlock = ({ healthData = {} }) => {
  return (
    <Box
      display='flex'
      flexDirection='row'
      height={'52px'}
      sx={{
        borderRadius: '6px',
        backgroundColor: healthData?.status === 'error' ? '#FEF2F2' : '#F0FDF4',
        boxShadow: '0px 4px 10px 0px rgba(232, 232, 232, 0.25)',
        padding: '4px 0px',
      }}
    >
      <Grid container alignItems={'center'}>
        <Grid item xs={8}>
          <Typography
            pl={'10px'}
            textAlign={'left'}
            color={'var(--grey-100, #737373)'}
            fontSize={'var(--ds-text-body)'}
            fontWeight={'var(--ds-font-weight-regular)'}
          >
            {healthData?.lable}
          </Typography>
        </Grid>
        <Grid item xs={4}>
          <Typography
            pr={'15px'}
            textAlign={'right'}
            color={'#374151'}
            fontSize={'var(--ds-text-heading)'}
            fontWeight={'var(--ds-font-weight-semibold)'}
          >
            {healthData?.value}
          </Typography>
        </Grid>
      </Grid>
    </Box>
  );
};

HealthBlock.propTypes = {
  healthData: PropTypes.any,
};

const UtilizationAndHealth = ({ clusterSummary = {}, accountId }) => {
  const [isUtilization, setIsUtilization] = useState(false);
  const [utilizationInsights, setUtilizationInsights] = useState([]);

  const events = clusterSummary?.cluster_data?.event ?? [];
  getEvents(events);
  const initialDisplayCount = 4;

  function getEvents(events) {
    let eventsData = [];
    let eventFound = {
      report_crash_loop: {
        count: 0,
        label: 'Container restarts',
      },
      KubePodNotReady: {
        count: 0,
        label: 'Pending pods',
      },
      image_pull_backoff_reporter: {
        count: 0,
        label: 'Image not found',
      },
      pod_oom_killer_enricher: {
        count: 0,
        label: 'OOM',
      },
      KubeStatefulSetReplicasMismatch: {
        count: 0,
        label: 'Unhealthy Statefulsets',
      },
    };

    for (let i = 0; i < events?.length; i++) {
      if (events[i].aggregation_key in eventFound) {
        eventFound[events[i].aggregation_key].count += events[i].event_count;
      }
    }

    for (const value of Object.values(eventFound)) {
      eventsData.push({ lable: value.label, value: value.count });
    }

    return eventsData;
  }

  const getUtilizationInsights = () => {
    apiKubernetes1
      .listInsights([accountId])
      .then((res) => {
        const transformedData = Object.keys(res).reduce((acc, key) => {
          const id = uuidv4();
          acc[key] = res[key].map((item) => {
            const appCount = Array.isArray(item.applications) ? item.applications.length : 0;
            const updatedTitle = appCount > 0 ? `${appCount} ${item.title}` : item.title;

            return {
              ...item,
              id,
              title: updatedTitle,
              icon: GetInsightIcon({ ...item, id }),
              label: updatedTitle,
            };
          });
          return acc;
        }, {});
        setUtilizationInsights(transformedData[accountId]);
      })
      .catch((err) => {
        console.error(err);
      });
  };

  useEffect(() => {
    getUtilizationInsights();
  }, []);

  const styles = {
    iconContainer: {
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      height: '22px',
      width: '22px',
      backgroundColor: 'var(--ds-gray-100)',
      borderRadius: 'var(--ds-radius-sm)',
    },
  };

  const highlightWords = ['OOMKilled', 'Hi-Restarts', 'right', 'sized'];

  const resolveInsightRoute = (insight) => getInsightRoute(insight.label || insight.title, accountId, 'K8s', insight.rule);

  const closeUtilizationModal = () => {
    setIsUtilization(false);
  };

  // Shared renderer for one insight row (used by both the inline list and the modal).
  const renderInsightRow = (list, onNavigate) => {
    const insightRoute = resolveInsightRoute(list);
    const rowContent = (
      <>
        <Box sx={styles.iconContainer}>
          <SafeIcon src={list.icon} alt='icon' style={{ height: '14px', width: '14px' }} />
        </Box>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-700)' }}>
          <HighlightText message={list.label} highlightWords={highlightWords} cluster={accountId} />
        </Typography>
      </>
    );
    const rowSx = {
      display: 'flex',
      alignItems: 'center',
      gap: 'var(--ds-space-2)',
      borderRadius: 'var(--ds-radius-sm)',
      px: 'var(--ds-space-1)',
      py: '2px',
      textDecoration: 'none',
      color: 'inherit',
    };
    return insightRoute ? (
      <Link key={list.label} href={insightRoute} onClick={onNavigate} style={{ textDecoration: 'none', color: 'inherit' }}>
        <Box sx={{ ...rowSx, cursor: 'pointer', '&:hover': { backgroundColor: 'var(--ds-gray-100)' } }}>{rowContent}</Box>
      </Link>
    ) : (
      <Box key={list.label} sx={rowSx}>
        {rowContent}
      </Box>
    );
  };

  return (
    <>
      <DSModal
        width='sm'
        open={isUtilization}
        onClose={closeUtilizationModal}
        title={
          <Box
            display={'flex'}
            alignItems={'center'}
            gap={'var(--ds-space-2)'}
            fontSize={'var(--ds-text-title)'}
            fontWeight={'var(--ds-font-weight-semibold)'}
            color='var(--ds-gray-700)'
          >
            <SafeIcon src={StarsIcon} alt='star icon' height={28} width={28} /> Insights
          </Box>
        }
        contentStyles={{
          padding: 'var(--ds-space-5) 40px',
        }}
      >
        {utilizationInsights?.map((list) => renderInsightRow(list, closeUtilizationModal))}
      </DSModal>
      <Stack direction={'column'} height='100%' gap='10px'>
        <DSCard variant='accent' tone='success' size='md' sx={{ height: '100%' }}>
          <HeadingWithBorder
            value='Insights'
            borderWidth='3px'
            borderColor='var(--ds-blue-500)'
            sx={{
              '& p': {
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
              },
            }}
          />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', gap: 'var(--ds-space-1)', mt: 'var(--ds-space-3)' }}>
            {utilizationInsights?.slice(0, initialDisplayCount)?.map((list) => renderInsightRow(list))}
            {utilizationInsights?.length > initialDisplayCount && (
              <Box sx={{ display: 'flex', justifyContent: 'flex-start', marginTop: 'var(--ds-space-2)' }}>
                <DSButton tone='secondary' size='xs' onClick={() => setIsUtilization(true)}>
                  Show {utilizationInsights.length - initialDisplayCount} more
                </DSButton>
              </Box>
            )}
          </Box>
        </DSCard>
        <DSCard variant='accent' tone='success' size='md' sx={{ height: '100%' }}>
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              marginBottom: 'var(--ds-space-2)',
            }}
          >
            <HeadingWithBorder
              value='Utilization & Health'
              borderWidth='3px'
              borderColor='var(--ds-blue-500)'
              sx={{
                '& p': {
                  fontSize: 'var(--ds-text-title)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-700)',
                },
              }}
            />
          </Box>
          <Box>
            <KubernetesMemoryCpuOverView requiredTooltip={true} showUpdatedUi={true} showUsage={true} accountId={accountId} />
          </Box>
        </DSCard>
      </Stack>
    </>
  );
};

UtilizationAndHealth.propTypes = {
  accountId: PropTypes.string,
  clusterSummary: PropTypes.any,
};

const CostSummary = ({ clusterSummary = {}, accountId }) => {
  const cluster = accountId;
  const { selectedCluster } = useData();

  const buildUrl = (selectedCluster, id, fragment, navigate, additionalQuery = {}) => {
    let route;
    if (navigate === 'details') {
      let base = selectedCluster?.cloud_provider === 'K8s' ? '/kubernetes/details' : '/cloud-account/details';
      let accountIdKey = selectedCluster?.cloud_provider === 'K8s' ? 'KubernetesDetails' : 'accountId';
      route = `${base}/${id}?${accountIdKey}=${id}`;
      if (additionalQuery?.aggregation_key) {
        for (const [key, value] of Object.entries(additionalQuery)) {
          route = `${route}&${key}=${value}`;
        }
      }
      route = `${route}#${fragment}`;
    } else if (navigate === 'auto-pilot') {
      route = `/auto-pilot?accountId=${id}`;
    }
    return route;
  };

  const QuickLinksData = [
    {
      links: [
        {
          name: 'Query Logs',
          fragment: 'monitoring/logs', // Tab 4, Subtab 0
          icon: LogsIcon,
        },
        {
          name: 'Recent Errors',
          fragment: 'monitoring/groups', // Tab 4, Subtab 1
          icon: RecentErrorIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Query Metrices',
          fragment: 'monitoring/query', // Tab 4, Subtab 2
          icon: MatricsIcon,
        },
      ],
      insights: [],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },

    {
      links: [
        {
          name: 'View Traces',
          fragment: 'monitoring/traces', // Tab 4, Subtab 5
          icon: TraceIcon,
        },
        {
          name: 'Service Maps',
          fragment: 'monitoring/service-map', // Tab 4, Subtab 6
          icon: ServiceMapsIcon,
        },
      ],
      insights: [],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'View Applications',
          fragment: 'kubernetes/applications', // Tab 3, Subtab 1
          icon: ApplicationsIconblue,
        },
        {
          name: 'View Pods',
          fragment: 'kubernetes/pods', // Tab 3, Subtab 3
          icon: PodsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Security',
          fragment: 'security/image-scan', // Tab 5, Subtab 0
          icon: SecurityIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Ec2 Instances',
          fragment: 'ec2/instances',
          icon: EC2Icon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'RDS Instances',
          fragment: 'rds/instances',
          icon: RDSIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Service Health',
          fragment: 'services',
          icon: ServiceIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
  ];

  const uniqueLinks = React.useMemo(() => {
    const links = QuickLinksData.filter((d) => d.cloudProvider === selectedCluster?.cloud_provider)
      .map((data) => data.links.map((link) => ({ ...link })))
      .flat();
    return links;
  }, [QuickLinksData, selectedCluster]);

  return (
    <Stack height='100%' gap='var(--ds-space-2)'>
      <CustomOptimizationsSummaryCard accountId={accountId} clusterSummary={clusterSummary} loading={clusterSummary.length > 0} />
      <Box>
        <DSCard variant='accent' tone='info' size='md'>
          <HeadingWithBorder
            value='Quick Links'
            borderWidth='3px'
            borderColor='var(--ds-blue-500)'
            sx={{
              '& p': {
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
              },
            }}
          />
          <Box display='grid' gridTemplateColumns={'repeat(2,1fr)'} mt='5px'>
            {uniqueLinks.map((link) => (
              <Box display={'flex'} alignItems={'center'} key={link.name} my={'3px'} gap={'var(--ds-space-2)'}>
                <Box
                  sx={{
                    height: '16px',
                    width: '16px',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    img: {
                      height: '100%',
                      width: '100%',
                    },
                  }}
                >
                  <SafeIcon src={link.icon} alt={link.name} />
                </Box>
                <DSLink href={buildUrl(selectedCluster, accountId, link.fragment, 'details', {})}>
                  <Typography fontSize={'var(--ds-text-body)'} fontWeight={'var(--ds-font-weight-regular)'} color={'var(--ds-gray-600)'}>
                    {link.name}
                  </Typography>
                </DSLink>
              </Box>
            ))}
          </Box>
        </DSCard>
      </Box>
    </Stack>
  );
};

CostSummary.propTypes = {
  clusterSummary: PropTypes.any,
};

const KubernetesClusterSummary = ({ accountId, clusterSummary }) => {
  if (!accountId || !clusterSummary) {
    return <></>;
  }

  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: '1fr 1fr 1fr',
        columnGap: 'var(--ds-space-4)',
        rowGap: 'var(--ds-space-5)',
        mb: 'var(--ds-space-5)',
      }}
    >
      <ClusterSummary clusterSummary={clusterSummary} accountId={accountId} />
      <UtilizationAndHealth clusterSummary={clusterSummary} accountId={accountId} />
      <CostSummary clusterSummary={clusterSummary} accountId={accountId} />
    </Box>
  );
};

KubernetesClusterSummary.propTypes = {
  accountId: PropTypes.any,
  clusterSummary: PropTypes.any,
};

export default KubernetesClusterSummary;
