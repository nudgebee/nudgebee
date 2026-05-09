import PropTypes from 'prop-types';
import { ExternalLinkIcon } from '@assets';
import { Text } from '@components1/common';
import Currency from '@components1/common/format/Currency';
import Datetime from '@components1/common/format/Datetime';
import CustomTable from '@components1/common/tables/CustomTable2';
import TextWithBorder from '@components1/common/TextWithBorder';
import CustomIconButton from '@components1/CustomIconButton';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { useRouter } from 'next/router';
import React, { useEffect, useState, useRef } from 'react';
import k8sApi from '@api1/kubernetes';
import CustomLink from '@components1/common/CustomLink';
import apiKubernetes1 from '@api1/kubernetes1';
import apiAppGrouping from '@api1/application-groupings';
import { formatDateForTrace } from 'src/utils/common';
import { getLast30Days, getSpecificTime } from '@lib/datetime';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import KubernetesApplicationGroupingSummaryDashboard from './KubernetesApplicationGroupingSummaryDashboard';
import { colors } from 'src/utils/colors';
import apiTrace from '@api1/kubernetes/trace';
import CustomTooltip from '@components1/common/CustomTooltip';

const KubernetesApplicationGroupingSummary = ({ accountId, applications, setTab, setRenderForApplicationIssue }) => {
  const router = useRouter();
  const endDate = new Date();
  const startDate = new Date(endDate.getTime() - 24 * 60 * 60 * 1000);

  const dateRange = { startDate, endDate };

  // Separate loading states for each API call
  const [loadingStates, setLoadingStates] = useState({
    workloadKind: false,
    clusterSummary: false,
    eventSummary: false,
    eventType: false,
    applicationEvent: false,
    traceGroup: false,
  });
  const resourceIds = React.useMemo(() => applications?.map((item) => item?.cloud_resource_id) || [], [applications]);
  const [groupName, setGroupName] = useState('');
  const [eventTypeData, setEventTypeData] = useState([]);
  const [applicationEventData, setApplicationEventData] = useState([]);
  const [traceGroupData, setTraceGroupData] = useState([]);
  const [clusterSummary, setClusterSummary] = useState();
  const [eventSummaryData, setEventSummaryData] = useState({
    severityData: [
      {
        value: 0,
        label: 'High',
        color: '#FFFFFF',
        background: '#E95252',
      },
      {
        value: 0,
        label: 'Med',
        color: '#EF4444',
        background: '#FFF1F1',
      },
      {
        value: 0,
        label: 'Low',
        color: '#AEA124',
        background: '#FFFCE5',
      },
      {
        value: 0,
        label: 'Debug',
        color: '#3B82F6',
        background: '#EFF6FF',
      },
    ],
    highEvents: 0,
    applicationEvents: 0,
    podEvents: 0,
    nodeEvents: 0,
  });
  const [workloadKindCounts, setWorkloadKindCounts] = useState([]);
  const [sloData, setSLOData] = useState({ count: 0, firingCount: 0, firingWorkloads: [] });
  const eventTypeApplicationTypeTraceGroupRef = useRef(null);

  // Helper function to update loading state
  const updateLoadingState = (key, value) => {
    setLoadingStates((prev) => ({ ...prev, [key]: value }));
  };

  // Helper functions moved outside of useEffect
  const formatWorkloadName = (key) => {
    return key.replace('_count', '').replace(/\b\w/g, (char) => char.toUpperCase());
  };

  const formatWorkloadKinds = (workloadCounts) => {
    return Object.entries(workloadCounts)
      .filter(([_key, value]) => value > 0)
      .reduce((result, [key, value]) => {
        result[formatWorkloadName(key)] = value;
        return result;
      }, {});
  };

  const createEventTableData = (items, isAggregation = false) => {
    return (
      items?.map((item) => {
        const keyField = isAggregation ? item.aggregation_key : item.subject_owner;
        const linkParams = isAggregation ? `eventAggregationKey=${item.aggregation_key}&eventPriority=HIGH` : '';

        return [
          {
            component: (
              <Box>
                <Text value={keyField} showAutoEllipsis />
                <Box display={'flex'} alignItems={'center'}>
                  <Text secondaryText value={'Last occ:'} />
                  <Datetime value={item.max_created_at} sx={{ fontSize: '11px', pl: '3px', textAlign: 'right' }} sxSuffix={{ fontSize: '11px' }} />
                </Box>
              </Box>
            ),
          },
          {
            component: (
              <Typography textAlign={'end'}>
                <CustomLink
                  href={`/kubernetes/details/${accountId}?${linkParams}#events/all-events`}
                  style={{ color: '#374151', fontSize: '12px', fontWeight: 500 }}
                >
                  {item?.event_count}
                </CustomLink>
              </Typography>
            ),
          },
        ];
      }) || []
    );
  };

  const createTraceGroupData = (items) => {
    return (
      items?.map((item) => [
        {
          component: (
            <Box>
              <Text value={item.count || '-'} showAutoEllipsis />
              <Text secondaryText value={`Error Count: ${item.error_count} `} showAutoEllipsis />
            </Box>
          ),
        },
        {
          component: (
            <Box>
              <Text value={item.destination_workload_name || '-'} showAutoEllipsis />

              <Box display='flex' justifyContent='space-between'>
                <Text secondaryText value={`Namespace: ${item.destination_workload_namespace}`} showAutoEllipsis />
                <Text secondaryText value={`Status: ${item.http_status_code}`} showAutoEllipsis />
              </Box>

              <Box display='flex' justifyContent='space-between'>
                <Text secondaryText value={`Resource: ${item.resource}`} showAutoEllipsis />
                <Text secondaryText value={`Method: ${item.span_name}`} showAutoEllipsis />
              </Box>
            </Box>
          ),
        },
      ]) || []
    );
  };

  // Individual API call functions that handle their own loading states
  const fetchWorkloadKindCount = async (accountId, resource_ids) => {
    updateLoadingState('workloadKind', true);
    try {
      const response = await apiKubernetes1.listK8sWorkloadKindCount(accountId, '', resource_ids);
      const data = response?.data?.data?.workload_counts?.rows[0] ?? {};
      if (data) {
        const workloadKindsArray = formatWorkloadKinds(data);
        setWorkloadKindCounts(workloadKindsArray);
      }
    } catch (error) {
      console.error('Error fetching workload kind count:', error);
    } finally {
      updateLoadingState('workloadKind', false);
    }
  };

  const fetchClusterSummary = async (accountId, resource_ids) => {
    updateLoadingState('clusterSummary', true);
    try {
      const response = await apiAppGrouping.getK8sClusterSummaryData(accountId, { resource_ids: resource_ids });
      setClusterSummary(response?.data);
    } catch (error) {
      console.error('Error fetching cluster summary:', error);
    } finally {
      updateLoadingState('clusterSummary', false);
    }
  };

  const fetchEventSummary = async (accountId, resource_ids) => {
    updateLoadingState('eventSummary', true);
    try {
      const response = await k8sApi.getK8sEventGroupings(
        10,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          resource_ids: resource_ids,
        },
        ['tenant_id', 'account_id'],
        [
          'event_count',
          'count_priority_high',
          'count_priority_medium',
          'count_priority_low',
          'count_priority_debug',
          'count_priority_info',
          'count_application_issues',
          'count_node_issues',
          'count_pod_issues',
        ]
      );

      const firstRow = response.data?.event_groupings?.[0];
      if (firstRow) {
        setEventSummaryData((prev) => ({
          ...prev,
          nodeEvents: firstRow.count_node_issues,
          highEvents: firstRow.count_priority_high,
          applicationEvents: firstRow.count_application_issues,
          podEvents: firstRow.count_pod_issues,
          severityData: [
            { ...prev.severityData[0], value: firstRow.count_priority_high },
            { ...prev.severityData[1], value: firstRow.count_priority_medium },
            { ...prev.severityData[2], value: firstRow.count_priority_low },
            { ...prev.severityData[3], value: firstRow.count_priority_debug },
          ],
        }));
      }
    } catch (error) {
      console.error('Error fetching event summary:', error);
    } finally {
      updateLoadingState('eventSummary', false);
    }
  };

  const fetchEventTypeData = async (accountId, resource_ids) => {
    updateLoadingState('eventType', true);
    try {
      const response = await k8sApi.getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          priority: 'HIGH',
          resource_ids: resource_ids,
        },
        ['tenant_id', 'account_id', 'aggregation_key'],
        ['max_created_at', 'event_count', 'aggregation_key'],
        { name: 'event_count', order: 'desc' }
      );

      const tableData = createEventTableData(response?.data?.event_groupings, true);
      setEventTypeData(tableData);
    } catch (error) {
      console.error('Error fetching event type data:', error);
    } finally {
      updateLoadingState('eventType', false);
    }
  };

  const fetchApplicationEventData = async (accountId, resource_ids) => {
    updateLoadingState('applicationEvent', true);
    try {
      const response = await k8sApi.getK8sEventGroupings(
        5,
        0,
        {
          account_id: accountId,
          start_date: dateRange.startDate,
          end_date: dateRange.endDate,
          aggregation_key: [],
          priority: 'HIGH',
          resource_ids: resource_ids,
        },
        ['tenant_id', 'account_id', 'subject_owner'],
        ['max_created_at', 'event_count', 'subject_owner'],
        { name: 'event_count', order: 'desc' }
      );

      const tableData = createEventTableData(response?.data?.event_groupings, false);
      setApplicationEventData(tableData);
    } catch (error) {
      console.error('Error fetching application event data:', error);
    } finally {
      updateLoadingState('applicationEvent', false);
    }
  };

  const fetchTraceGroup = async (accountId, namespaceNames, workloadNames) => {
    updateLoadingState('traceGroup', true);
    try {
      const response = await apiTrace.traceGroupV2(
        accountId,
        '',
        '',
        namespaceNames,
        workloadNames,
        '',
        5,
        0,
        formatDateForTrace(getSpecificTime(60)),
        formatDateForTrace(new Date().getTime()),
        '',
        '',
        ''
      );

      const tableData = createTraceGroupData(response?.traces_groupings?.rows || []);
      setTraceGroupData(tableData);
    } catch (error) {
      console.error('Error fetching recent events:', error);
    } finally {
      updateLoadingState('traceGroup', false);
    }
  };

  const fetchSLOObsersavation = async (accountId, namespaces, workloads, timestamp) => {
    try {
      const response = await apiKubernetes1.getSLOObservation({ accountId, namespaces, workloads, timestamp });
      const sloResponseData = response?.data?.data?.slo_report_observation_v2?.rows || [];
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
        const firingCount = Object.values(statusMap).filter((status) => status === 'FIRING').length;
        const distinctCount = Object.keys(statusMap).length;
        const firingArray = Object.entries(statusMap)
          .filter(([_, _status]) => status === 'FIRING')
          .map(([key, _status]) => {
            const [workload_namespace, workload_name] = key.split('/');
            return { workload_namespace, workload_name };
          });
        setSLOData({
          count: distinctCount,
          firingCount,
          firingWorkloads: firingArray.length > 0 ? firingArray.map((f) => `${f.workload_namespace}/${f.workload_name}`) : [],
        });
      }
    } catch (error) {
      console.error('Error fetching SLO observations:', error);
    }
  };

  // Main effect for all data fetching with parallel execution
  useEffect(() => {
    if (!accountId) {
      return;
    }

    const resource_ids = applications?.map((item) => item?.cloud_resource_id) || [];
    if (!resource_ids.length) {
      return;
    }

    const workloadNames = [...new Set(applications?.map((item) => item.workload_name))];
    const namespaceNames = [...new Set(applications?.map((item) => item.namespace_name))];

    // Execute all API calls in parallel without waiting for each other
    // Each function handles its own loading state and renders independently
    fetchWorkloadKindCount(accountId, resource_ids);
    fetchClusterSummary(accountId, resource_ids);
    fetchEventSummary(accountId, resource_ids);
    fetchSLOObsersavation(accountId, namespaceNames, workloadNames, getLast30Days(new Date()).toISOString());
  }, [accountId, applications]);

  // Lazy load eventTypeData, applicationEventData, and traceGroupData when the user scrolls to the section
  useEffect(() => {
    if (!accountId || applications.length === 0) {
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) {
          const resource_ids = applications?.map((item) => item?.cloud_resource_id) || [];
          if (!resource_ids.length) {
            return;
          }

          const workloadNames = [...new Set(applications?.map((item) => item.workload_name))];
          const namespaceNames = [...new Set(applications?.map((item) => item.namespace_name))];
          fetchEventTypeData(accountId, resource_ids);
          fetchApplicationEventData(accountId, resource_ids);
          fetchTraceGroup(accountId, namespaceNames, workloadNames);
          observer.disconnect();
        }
      },
      { threshold: 0.1 }
    );

    if (eventTypeApplicationTypeTraceGroupRef.current) {
      observer.observe(eventTypeApplicationTypeTraceGroupRef.current);
    }

    return () => observer.disconnect();
  }, [accountId, applications]);

  useEffect(() => {
    if (!router?.query?.groupId) {
      return;
    }
    apiAppGrouping.getAppGroupByPK(router?.query?.groupId).then((res) => {
      setGroupName(res?.data?.data?.application_group_by_pk?.name || '');
    });
  }, [router?.query?.groupId]);
  // Helper function to check if a specific section is loading
  const isSectionLoading = (sections) => {
    return sections.some((section) => loadingStates[section]);
  };

  if (!accountId || !applications || applications.length === 0) {
    return (
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          minHeight: '200px',
          backgroundColor: '#ffffff',
          borderRadius: '8px',
          boxShadow: '0px 4px 20px 0px #B4B4B41F',
          margin: '16px 0',
        }}
      >
        <Typography
          sx={{
            fontSize: '16px',
            fontWeight: 500,
            color: '#6B7280',
            textAlign: 'center',
          }}
        >
          No data available. Please configure application.
        </Typography>
      </Box>
    );
  }

  return (
    <Box>
      <SummaryBlock
        hideTitle
        sx={{
          borderColor: 'transparent',
          backgroundColor: '#ffffff',
          boxShadow: '0px 4px 20px 0px #B4B4B41F',
          padding: '12px 16px !important',
          minHeight: 'unset',
          mb: '24px',
          mt: '16px',
        }}
      >
        <Box display='flex' alignItems={'center'} justifyContent={'space-between'} mb={2}>
          <TextWithBorder
            value='Application Summary'
            borderColor='#3B82F6'
            borderWidth='3px'
            sx={{
              minWidth: 'auto',
              height: '22px',
              padding: '2px 8px',
              '& p': { fontSize: '16px', fontWeight: 600, color: '#374151' },
            }}
          />
        </Box>

        <ShimmerLoading isLoading={isSectionLoading(['workloadKind', 'podStatus', 'eventSummary', 'clusterSummary'])} height={'48px'}>
          <Box display='grid' gridTemplateColumns={{ xs: '1fr', sm: '3fr 2fr 5fr' }} gap={2}>
            {/* Applications */}
            <Box sx={{ p: '0 8px', borderRight: '1px solid #EBEBEB' }}>
              <Typography variant='subtitle2' color='textSecondary'>
                Applications
              </Typography>
              <Box display='flex' alignItems='baseline' gap={1} mt={0.5}>
                {loadingStates.workloadKind ? (
                  <ShimmerLoading isLoading={true} height={'24px'} width={'60px'} />
                ) : (
                  <Typography
                    variant='h5'
                    fontWeight={600}
                    onClick={() => {
                      if (workloadKindCounts?.Count > 0) {
                        setRenderForApplicationIssue(false);
                        setTab(2);
                      }
                    }}
                    sx={{
                      cursor: workloadKindCounts?.Count > 0 ? 'pointer' : 'default',
                      '&:hover': workloadKindCounts?.Count > 0 ? { color: '#3047ec' } : {},
                    }}
                  >
                    {workloadKindCounts?.Count ?? '-'}
                  </Typography>
                )}
                <Box display='flex' flexWrap='wrap' gap={1} ml={1}>
                  {loadingStates.workloadKind ? (
                    <ShimmerLoading isLoading={true} height={'16px'} width={'100px'} />
                  ) : (
                    Object.entries(workloadKindCounts)
                      .filter(([name]) => name !== 'Count')
                      .map(([name, count]) => (
                        <Box key={name} display='flex' alignItems='center' gap={0.5} mr={1}>
                          <Typography sx={{ fontSize: '12px', fontWeight: 400 }} color={colors.text.secondaryDark}>
                            {name}
                          </Typography>
                          <Typography sx={{ fontSize: '12px', fontWeight: 400 }} color={colors.text.secondary}>
                            {count}
                          </Typography>
                        </Box>
                      ))
                  )}
                </Box>
              </Box>
            </Box>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                borderRight: '1px solid #EBEBEB',
                padding: '16px 8px',
              }}
            >
              <Box>
                <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>SLO</Typography>
                <CustomTooltip
                  placement='top'
                  title={
                    sloData.firingWorkloads.length > 0 ? (
                      <div>
                        <span style={{ fontWeight: 'bold', marginBottom: 4 }}>SLO Status for Selected Application</span>
                        <div style={{ fontWeight: 'bold', marginBottom: 4 }}>Attention: Firing SLO (30 Days Observation)</div>
                        {sloData.firingWorkloads.map((workload, index) => (
                          <div key={index}>{workload}</div>
                        ))}
                        <div style={{ fontWeight: 'bold', marginTop: 4 }}>{sloData.count} SLO Configured</div>
                      </div>
                    ) : (
                      ''
                    )
                  }
                >
                  <Typography
                    variant='h4'
                    sx={{
                      fontSize: '24px',
                      fontWeight: 600,
                      color: '#374151',
                      cursor: sloData.count > 0 ? 'pointer' : 'default',
                    }}
                    onClick={() => sloData.count > 0 && router.push(`/kubernetes/details/${accountId}#monitoring/slo`)}
                  >
                    <span style={{ color: sloData.firingCount > 0 ? 'red' : '#374151' }}>{sloData.firingCount}</span> / {sloData.count}
                  </Typography>
                </CustomTooltip>
              </Box>
            </Box>

            {/* Events & Optimizations */}
            <Box sx={{ p: '0 8px', display: 'flex', flexDirection: 'row', justifyContent: 'space-around' }}>
              <Box mb={2}>
                <Typography variant='subtitle2' color='textSecondary'>
                  Events
                </Typography>
                {loadingStates.eventSummary ? (
                  <ShimmerLoading isLoading={true} height={'24px'} width={'60px'} />
                ) : (
                  <CustomTooltip placement='top' title='Application issues (High Error Critical Logs & API Failures)'>
                    <Typography
                      variant='h5'
                      fontWeight={600}
                      onClick={() => {
                        if (eventSummaryData.applicationEvents > 0) {
                          setRenderForApplicationIssue(true);
                          setTab(1);
                        }
                      }}
                    >
                      <Currency
                        prefix=''
                        sx={{
                          fontSize: '20px',
                          fontWeight: 600,
                          color: '#374151',
                          cursor: eventSummaryData.applicationEvents > 0 ? 'pointer' : 'default',
                          '&:hover': eventSummaryData.applicationEvents > 0 ? { color: '#3047ec' } : {},
                        }}
                        withTooltip={false}
                        value={eventSummaryData.applicationEvents}
                      />
                    </Typography>
                  </CustomTooltip>
                )}
              </Box>
              <Box>
                <Typography variant='subtitle2' color='textSecondary'>
                  Optimizations
                </Typography>
                <Box display='flex' alignItems='baseline' gap={1} mt={0.5}>
                  {loadingStates.clusterSummary ? (
                    <ShimmerLoading isLoading={true} height={'24px'} width={'60px'} />
                  ) : clusterSummary?.total_recommendations?.length > 0 ? (
                    <>
                      <Typography variant='h5' fontWeight={600}>
                        {clusterSummary?.total_recommendations.reduce((totalCounts, recommendation) => totalCounts + recommendation.count, 0) ?? '-'}
                      </Typography>
                      <Box display='flex' flexWrap='wrap' gap={1} ml={1}>
                        {clusterSummary?.total_recommendations.map(({ category, count }) => {
                          const hashMap = {
                            'Right Sizing': 'optimize/right-sizing',
                            'Unused Volumes': 'optimize/unused-volume',
                            'Spot Recommendations': 'optimize/spot-recommendation',
                          };
                          const hash = hashMap[category];
                          return (
                            <Box key={category} display='flex' alignItems='center' gap={0.5} mr={1}>
                              <Typography sx={{ fontSize: '12px', fontWeight: 400 }} color={colors.text.secondaryDark}>
                                {category}
                              </Typography>
                              <Typography
                                sx={{
                                  fontSize: '12px',
                                  fontWeight: 400,
                                  cursor: count > 0 && hash !== undefined ? 'pointer' : 'default',
                                  '&:hover': count > 0 && hash !== undefined ? { color: '#3047ec' } : colors.text.secondary,
                                }}
                                color={colors.text.secondary}
                                onClick={() => {
                                  if (count > 0 && hash !== undefined) {
                                    router.push({
                                      pathname: `/kubernetes/details/${accountId}`,
                                      query: {
                                        resourceIds,
                                        groupName,
                                      },
                                      hash: hash,
                                    });
                                  }
                                }}
                              >
                                {count}
                              </Typography>
                            </Box>
                          );
                        })}
                      </Box>
                    </>
                  ) : (
                    '-'
                  )}
                </Box>
              </Box>
            </Box>
          </Box>
        </ShimmerLoading>
      </SummaryBlock>

      <KubernetesApplicationGroupingSummaryDashboard accountId={accountId} applications={applications} />

      <SummaryBlock
        hideTitle
        sx={{
          borderColor: 'transparent',
          backgroundColor: '#ffffff',
          boxShadow: '0px 4px 20px 0px #B4B4B41F',
          '@media(max-width: 1170px)': {
            padding: '20px 24px !important',
          },
        }}
      >
        <Box display='flex' alignItems={'center'} justifyContent={'space-between'}>
          <Box display='flex' alignItems={'center'}>
            <TextWithBorder
              value='Events/Errors'
              borderColor='#3B82F6'
              borderWidth='3px'
              sx={{ '& p': { fontSize: '20px', fontWeight: 600, color: '#374151' } }}
            />
            {loadingStates.eventSummary ? (
              <ShimmerLoading isLoading={true} height={'24px'} width={'40px'} />
            ) : (
              <Typography
                sx={{
                  border: '0.5px solid #F87171',
                  backgroundColor: '#FEF2F2',
                  p: '2px 8px',
                  borderRadius: '4px',
                  color: '#374151',
                  fontWeight: 500,
                }}
              >
                {eventSummaryData.applicationEvents}
              </Typography>
            )}
          </Box>
          <Box
            display={'flex'}
            gap='10px'
            sx={{
              '@media(max-width: 1130px)': {
                gap: '5px',
              },
            }}
          >
            {loadingStates.eventSummary ? (
              <ShimmerLoading isLoading={true} height={'24px'} width={'200px'} />
            ) : (
              eventSummaryData.severityData
                .filter((h) => h.value > 0)
                .map((data, index, filteredSeverityData) => (
                  <Box
                    display='flex'
                    alignItems={'center'}
                    key={data.label}
                    sx={{
                      '&::after': index !== filteredSeverityData.length - 1 && {
                        content: '" "',
                        height: '16px',
                        border: '0.5px solid #D0D0D0',
                      },
                    }}
                  >
                    <Typography
                      sx={{
                        minWidth: 'auto',
                        height: '22px',
                        padding: '2px 10px',
                        backgroundColor: data.background,
                        borderRadius: '4px',
                        color: data.color,
                        boxShadow: '0px 1px 3px 0px #0000001A',
                        fontWeight: 700,
                        fontSize: '11px',
                        mr: '5px',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                      }}
                    >
                      {data.value}
                    </Typography>
                    <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151', mr: '5px' }}>{data.label}</Typography>
                  </Box>
                ))
            )}
          </Box>
        </Box>

        <Box display={'grid'} gridTemplateColumns={'1.2fr 1.2fr 1.7fr'} gap={'8px'} mt={'10px'}>
          <Box ref={eventTypeApplicationTypeTraceGroupRef}>
            <TextWithBorder
              value='By Event type'
              borderColor='#FACF39'
              borderWidth='2px'
              sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: '#374151' } }}
              span={
                <CustomIconButton
                  onClick={() => {
                    router.push(`/kubernetes/details/${accountId}?accountId=${accountId}&section=0#events/grouped-events`);
                  }}
                >
                  <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                </CustomIconButton>
              }
            />
            <CustomTable
              tableData={eventTypeData}
              headers={[
                { name: 'Event type', width: '80%' },
                { name: 'Count', width: '20%' },
              ]}
              showUpdatedTable
              showEmptyStateText
              loading={loadingStates.eventType}
            />
          </Box>
          <Box ref={eventTypeApplicationTypeTraceGroupRef}>
            <TextWithBorder
              value='By Applications'
              borderColor='#FACF39'
              borderWidth='2px'
              sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: '#374151' } }}
              span={
                <CustomIconButton
                  onClick={() => {
                    router.push(`/kubernetes/details/${accountId}?accountId=${accountId}&section=1#events/grouped-events`);
                  }}
                >
                  <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                </CustomIconButton>
              }
            />
            <CustomTable
              tableData={applicationEventData}
              headers={[
                { name: 'Application name', width: '80%' },
                { name: 'Count', width: '20%' },
              ]}
              showUpdatedTable
              showEmptyStateText
              loading={loadingStates.applicationEvent}
            />
          </Box>
          <Box ref={eventTypeApplicationTypeTraceGroupRef}>
            <TextWithBorder
              value='Trace Group'
              borderColor='#FACF39'
              borderWidth='2px'
              sx={{ '& p': { fontSize: '13px !important', fontWeight: 500, color: '#374151' } }}
              span={
                <CustomIconButton
                  onClick={() => {
                    router.push(`/kubernetes/details/${accountId}?accountId=${accountId}#monitoring/grouping`);
                  }}
                >
                  <SafeIcon src={ExternalLinkIcon} alt='redirect' />
                </CustomIconButton>
              }
            />
            <CustomTable
              tableData={traceGroupData}
              headers={[
                { name: 'Request Count', width: '25%' },
                { name: 'Resource Info', width: '75%' },
              ]}
              showUpdatedTable
              showEmptyStateText
              loading={loadingStates.traceGroup}
            />
          </Box>
        </Box>
      </SummaryBlock>
    </Box>
  );
};

KubernetesApplicationGroupingSummary.propTypes = {
  accountId: PropTypes.string.isRequired,
  applications: PropTypes.array.isRequired,
  setTab: PropTypes.func.isRequired,
  setRenderForApplicationIssue: PropTypes.func.isRequired,
};

export default KubernetesApplicationGroupingSummary;
