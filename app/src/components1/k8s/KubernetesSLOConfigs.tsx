import apiKubernetes1 from '@api1/kubernetes1';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import { Button } from '@components1/ds/Button';
import Charts from '@components1/common/charts/LineCharts';
import CustomTable from '@common-new/tables/CustomTable2';
import { getYesterday, getLast30Days } from '@lib/datetime';
import React, { useEffect, useState } from 'react';
import { convertDateStringForSLOReportChart, formatSeconds, snakeToTitleCase } from 'src/utils/common';
import KubernetesTracesListing from './details/KubernetesTracesListing';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { Text } from '@components1/common';
import { Typography } from '@mui/material';
import KubernetesEventsTable from '@components1/events/KubernetesEvents';
import SLOConfigDialog from '@components1/k8s/common/SLOConfigDialog';
import { snackbar } from '@components1/common/snackbarService';
import { hasWriteAccess } from '@lib/auth';

interface KubernetesSLOConfigsProps {
  accountId: string;
}

const KubernetesSLOConfigs: React.FC<KubernetesSLOConfigsProps> = ({ accountId }) => {
  const [tableData, setTableData] = useState<any>([]);
  const [loading, setLoading] = useState(false);
  const [workloadFqdn, setWorkloadFqdn] = useState<string[]>([]);
  const [sharedVariableForFindingIds, setSharedVariableForFindingIds] = useState([]);
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [workloadFilter, setWorkloadFilter] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>('');
  const [selectedWorkload, setSelectedWorkload] = useState<string>('');
  const [openSLODialog, setOpenSLODialog] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const header = [
    { name: 'Workload Name', width: '20%' },
    { name: 'Window', width: '12%' },
    { name: 'Objective', width: '12%' },
    { name: 'Status (30D)', width: '12%' },
    { name: 'Latency', width: '12%' },
    { name: 'Availability', width: '12%' },
    { name: 'Observation (30D)', width: '30%' },
  ];

  useEffect(() => {
    if (!accountId) {
      return;
    }

    setLoading(true);
    const params: any = {
      cloud_account_id: accountId,
    };

    if (selectedNamespace) {
      params.namespace = selectedNamespace;
    }
    if (selectedWorkload) {
      params.workload_name = selectedWorkload;
    }

    apiKubernetes1
      .listSLOConfigs(params)
      .then((res) => {
        const filteredData = res?.data?.data?.slo_config || [];

        if (filteredData && filteredData.length > 0) {
          const uniqueNamespaces = Array.from(new Set<string>(filteredData.map((item: any) => item.workload_namespace)));
          const uniqueWorkloads = Array.from(new Set<string>(filteredData.map((item: any) => item.workload_name)));
          setNamespaceFilter(uniqueNamespaces);
          setWorkloadFilter(uniqueWorkloads);
        } else {
          setNamespaceFilter([]);
          setWorkloadFilter([]);
        }

        const workloadFqdn: string[] = [];
        if (filteredData.length > 0) {
          const groupedWorkloads = filteredData.reduce((acc: any, cur: any) => {
            const { workload_name, workload_namespace, name, threshold, goal, id, window, created_at, updated_at } = cur;
            const key = `${workload_namespace}_${workload_name}`;

            if (!acc[key]) {
              acc[key] = {
                workload_name,
                workload_namespace,
                window,
                config: [],
                created_at,
                updated_at,
              };
            }

            acc[key].config.push({ name, threshold, goal, id });

            return acc;
          }, {});

          const result = Object.values(groupedWorkloads);
          const tableData = result.map((m: any) => {
            let objective;
            let availability;
            let latency;
            let availabilityConfig = [];
            let latencyConfig = [];
            if (m.config && m.config.length > 0) {
              availabilityConfig = m.config.filter((n: any) => n.name == 'availability');
              if (availabilityConfig && availabilityConfig.length == 1) {
                availability = (availabilityConfig[0].goal * 100).toFixed();
              }
              latencyConfig = m.config.filter((n: any) => n.name == 'latency');
              if (latencyConfig && latencyConfig.length == 1) {
                objective = (latencyConfig[0].goal * 100).toFixed();
                latency = latencyConfig[0].threshold;
              }
            }
            workloadFqdn.push(m.workload_namespace + '.' + m.workload_name);
            return [
              {
                component: (
                  <>
                    <Text value={m.workload_name} />
                    <Text value={`ns: ${m.workload_namespace}`} secondaryText />
                  </>
                ),
                drilldownQuery: {
                  availabilityConfig: availabilityConfig[0],
                  latencyConfig: latencyConfig[0],
                  workloadName: m.workload_name,
                  workloadNamespace: m.workload_namespace,
                },
              },
              {
                component: <Text value={formatSeconds(m.window, false)} />,
              },
              {
                component: <Text value={objective ?? '-'} />,
              },
              {
                component: <Text value={'-'} />,
              },
              {
                component: <Text value={latency ? formatSeconds(latency / 1000, false) : '-'} />,
              },
              {
                component: <Text value={availability ?? '-'} />,
              },
              {
                component: <ThreeDotLoader />,
              },
            ];
          });
          setWorkloadFqdn(workloadFqdn);
          setTableData(tableData);
        } else {
          setTableData([]);
          setWorkloadFqdn([]);
        }
      })
      .catch((error) => {
        console.error('Error fetching SLO configs:', error);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId, selectedNamespace, selectedWorkload, refreshKey]);

  const onNamespaceFilterChange = (e: any) => {
    const value = e?.target?.value;
    setSelectedNamespace(value || '');
    setSelectedWorkload('');
  };

  const onWorkloadFilterChange = (e: any) => {
    setSelectedWorkload(e?.target?.value || '');
  };

  const handleOpenSLODialog = () => {
    if (!hasWriteAccess(accountId)) {
      snackbar.error('You do not have write access to create SLO configs');
      return;
    }
    setOpenSLODialog(true);
  };

  useEffect(() => {
    if (workloadFqdn && workloadFqdn.length > 0) {
      apiKubernetes1
        .getSLOObservation({
          accountId: accountId,
          workloads: [...new Set(workloadFqdn.map((wn) => wn.split('.')[1]))],
          namespaces: [...new Set(workloadFqdn.map((wn) => wn.split('.')[0]))],
          timestamp: getLast30Days(new Date()).toISOString(),
        })
        .then((res) => {
          const sloReportObservationData = res?.data?.data?.slo_report_observation_v2?.rows ?? [];
          if (sloReportObservationData && sloReportObservationData.length > 0) {
            for (const element of tableData) {
              const item =
                sloReportObservationData?.filter(
                  (item: any) =>
                    item.workload_namespace === element[0].drilldownQuery.workloadNamespace &&
                    item.workload_name === element[0].drilldownQuery.workloadName
                ) ?? [];
              let observation = '';
              if (item && item.length > 0) {
                const availabilityConfigs = item.filter((g: any) => g.config_name === 'availability');
                if (availabilityConfigs.length > 0) {
                  const totalGood = availabilityConfigs.reduce((sum: number, g: any) => sum + Number(g.total_good_events), 0);
                  const totalEvents = availabilityConfigs.reduce((sum: number, g: any) => sum + Number(g.total_events), 0);
                  if (totalEvents > 0) {
                    observation = `${((totalGood / totalEvents) * 100).toFixed(1)}% Available.\n`;
                  }
                }

                const latencyConfigs = item.filter((g: any) => g.config_name === 'latency');
                if (latencyConfigs.length > 0) {
                  const totalGood = latencyConfigs.reduce((sum: number, g: any) => sum + Number(g.total_good_events), 0);
                  const totalEvents = latencyConfigs.reduce((sum: number, g: any) => sum + Number(g.total_events), 0);
                  if (totalEvents > 0) {
                    observation += `${((totalGood / totalEvents) * 100).toFixed(1)}% configured latency passed.`;
                  }
                }

                const filterFiringStatusConfig = item.filter((g: any) => g.status == 'FIRING');
                const firingStatusConfig = filterFiringStatusConfig.map((g: any) => snakeToTitleCase(g.config_name))?.join(', ');
                element[3] = {
                  component: (
                    <>
                      <CustomLabels text={filterFiringStatusConfig.length > 0 ? `FIRING` : 'OK'} />
                      {firingStatusConfig ? <Text secondaryText value={`[${firingStatusConfig}]`} /> : null}
                    </>
                  ),
                };
                element[6] = {
                  text: <Typography style={{ whiteSpace: 'pre-line' }}>{observation}</Typography>,
                };
              } else {
                element[6] = {
                  text: '-',
                };
              }
            }
            setTableData([...tableData]);
          } else {
            for (const element of tableData) {
              element[6] = {
                text: '-',
              };
            }
            setTableData([...tableData]);
          }
        });
    }
  }, [workloadFqdn]);

  return (
    <>
      <ListingLayout id={'k8s-slo-configs'}>
        <ListingLayout.Toolbar
          actions={
            <>
              <DownloadButton id='k8s-slo-configs-download' onClick={async () => ({ tableId: 'table-k8s-slo-configs' })} />
              <Button onClick={handleOpenSLODialog} id='add-slo-config-btn'>
                Add SLO
              </Button>
            </>
          }
        >
          <FilterDropdown
            label='Namespace'
            options={namespaceFilter.map((o) => ({ value: o, label: o }))}
            value={selectedNamespace}
            onSelect={onNamespaceFilterChange}
          />
          <FilterDropdown
            label='Workload'
            options={workloadFilter.map((o) => ({ value: o, label: o }))}
            value={selectedWorkload}
            onSelect={onWorkloadFilterChange}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={'table-k8s-slo-configs'}
            expandable={{
              tabs: [
                {
                  componentFn: function (opt: any, drilldownQuery: any) {
                    return (
                      <SLOReport
                        drilldownQuery={drilldownQuery}
                        accountId={accountId}
                        setSharedVariableForFindingIds={setSharedVariableForFindingIds}
                      />
                    );
                  },
                  text: 'Report',
                  value: 0,
                  key: 'report',
                },
                {
                  componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                    return <SLOTraces drilldownQuery={drilldownQuery} accountId={accountId} type='latency' />;
                  },
                  text: 'Latency Traces',
                  value: 1,
                  key: 'slo-latency-traces',
                },
                {
                  componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                    return <SLOTraces drilldownQuery={drilldownQuery} accountId={accountId} type='availability' />;
                  },
                  text: 'Availability Traces',
                  value: 2,
                  key: 'slo-availability-traces',
                },
                ...(sharedVariableForFindingIds.length > 0
                  ? [
                      {
                        componentFn: function (opt: any, drilldownQuery: any) {
                          return (
                            <SLOEvent
                              drilldownQuery={drilldownQuery}
                              accountId={accountId}
                              sharedVariableForFindingIds={sharedVariableForFindingIds}
                            />
                          );
                        },
                        text: 'Event',
                        value: 3,
                        key: 'slo-event',
                      },
                    ]
                  : []),
              ],
            }}
            showExpandable={true}
            tableData={tableData}
            rowsPerPage={tableData.length}
            totalRows={tableData.length}
            headers={header}
            loading={loading}
          />
        </ListingLayout.Body>
      </ListingLayout>
      <SLOConfigDialog
        open={openSLODialog}
        onClose={() => setOpenSLODialog(false)}
        accountId={accountId}
        onSuccess={() => setRefreshKey((prev) => prev + 1)}
      />
    </>
  );
};

export const SLOReport = ({
  drilldownQuery = {
    availabilityConfig: {} as any,
    latencyConfig: {} as any,
  },
  accountId = '',
  dateTime = {
    startTime: getYesterday().getTime(),
    endTime: new Date().getTime(),
  },
  setSharedVariableForFindingIds,
}: {
  drilldownQuery?: {
    availabilityConfig: any;
    latencyConfig: any;
  };
  accountId?: string;
  dateTime?: {
    startTime: number;
    endTime: number;
  };
  setSharedVariableForFindingIds: any;
}) => {
  const [availabilityData, setAvailabilityData] = useState({
    data: [
      {
        label: '',
        data: [],
      },
    ],
    label: [],
  });
  const [latencyData, setLatencyData] = useState({
    data: [
      {
        label: '',
        data: [],
      },
    ],
    label: [],
  });
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: dateTime.startTime,
    endDate: dateTime.endTime,
  });
  const [loadingAvailability, setLoadingAvailability] = useState(false);
  const [loadingLatency, setLoadingLatency] = useState(false);

  useEffect(() => {
    let availabilityFindingIds: string[] = [];
    let latencyFindingIds: string[] = [];
    setLoadingAvailability(true);
    setLoadingLatency(true);

    const fetchAvailability = drilldownQuery.availabilityConfig?.id
      ? apiKubernetes1
          .getSLOReport({
            config_id: drilldownQuery.availabilityConfig.id,
            account_id: accountId,
            start_date: new Date(selectedDateRange.startDate).toISOString(),
            end_date: new Date(selectedDateRange.endDate).toISOString(),
          })
          .then((res) => {
            const data = res?.data?.data?.slo_report || [];
            if (data.length > 0) {
              availabilityFindingIds = data.filter((g: any) => g.bad_events_count > 0).map((g: any) => g.id);
              const label = data.map((n: any) => convertDateStringForSLOReportChart(n.updated_at));
              const goodEventCount = data.map((n: any) => n.good_events_count);
              const badEventCount = data.map((n: any) => n.bad_events_count);
              setAvailabilityData({
                label,
                data: [
                  { label: 'Good Event', data: goodEventCount },
                  { label: 'Bad Event', data: badEventCount },
                ],
              });
            } else {
              setAvailabilityData({ label: [], data: [{ label: '', data: [] }] });
            }
          })
          .catch(() => {
            setAvailabilityData({ label: [], data: [{ label: '', data: [] }] });
          })
          .finally(() => setLoadingAvailability(false))
      : Promise.resolve();

    const fetchLatency = drilldownQuery.latencyConfig?.id
      ? apiKubernetes1
          .getSLOReport({
            config_id: drilldownQuery.latencyConfig.id,
            start_date: new Date(selectedDateRange.startDate).toISOString(),
            end_date: new Date(selectedDateRange.endDate).toISOString(),
          })
          .then((res) => {
            const data = res?.data?.data?.slo_report || [];
            if (data.length > 0) {
              latencyFindingIds = data.filter((g: any) => g.bad_events_count > 0).map((g: any) => g.id);
              const label = data.map((n: any) => convertDateStringForSLOReportChart(n.updated_at));
              const goodEventCount = data.map((n: any) => n.good_events_count);
              const badEventCount = data.map((n: any) => n.bad_events_count);
              setLatencyData({
                label,
                data: [
                  { label: 'Good Event', data: goodEventCount },
                  { label: 'Bad Event', data: badEventCount },
                ],
              });
            } else {
              setLatencyData({ label: [], data: [{ label: '', data: [] }] });
            }
          })
          .catch(() => {
            setLatencyData({ label: [], data: [{ label: '', data: [] }] });
          })
          .finally(() => setLoadingLatency(false))
      : Promise.resolve();

    // Once both promises are resolved
    Promise.all([fetchAvailability, fetchLatency]).then(() => {
      const combined = [...availabilityFindingIds, ...latencyFindingIds];
      if (combined.length > 0) {
        setSharedVariableForFindingIds(combined);
      }
    });
  }, [selectedDateRange]);

  const handleDateRangeChange = (passedSelectedDateTime: { startTime: number; endTime: number }) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <ListingLayout id={'slo-report-graphs'}>
      <ListingLayout.Toolbar
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: selectedDateRange.startDate,
              endTime: selectedDateRange.endDate,
              shortcutClickTime: 0,
            }}
            onChange={({ selection }) => handleDateRangeChange(selection)}
          />
        }
      />
      <ListingLayout.Body>
        <div className='chart-container'>
          <Charts
            chartTitle='Availiabity Report'
            dataset={availabilityData.data}
            labels={availabilityData.label}
            data={[]}
            loading={loadingAvailability}
          />
          <Charts chartTitle='Latency Report' dataset={latencyData.data} labels={latencyData.label} data={[]} loading={loadingLatency} />
        </div>
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const SLOEvent = ({ drilldownQuery = {} as any, accountId = '', sharedVariableForFindingIds = [] }) => {
  if (sharedVariableForFindingIds.length > 0) {
    return (
      <KubernetesEventsTable
        accountId={accountId}
        enableTrendChart={false}
        enableFilters={false}
        heading={''}
        defaultQuery={{
          namespace: drilldownQuery.workloadNamespace,
          workloadName: drilldownQuery.workloadName,
          startTime: getYesterday().getTime(),
          endTime: new Date().getTime(),
          finding_id: sharedVariableForFindingIds,
          aggregation_key: 'SLOViolation',
          finding_type: '',
        }}
        showTimeFilter={false}
      />
    );
  }
  return <Typography>No Event Available</Typography>;
};

const SLOTraces = ({ drilldownQuery = {} as any, accountId = '', type = '' }) => {
  const defaultDateTimeRange = {
    startDate: getYesterday().getTime(),
    endDate: new Date().getTime(),
  };
  let duration = null;
  let statusCode: string[] = [];
  if (type == 'latency') {
    if (drilldownQuery.latencyConfig && drilldownQuery.latencyConfig.threshold > 0) {
      duration = drilldownQuery.latencyConfig.threshold * 1000 * 1000;
    }
  } else if (type == 'availability') {
    statusCode = ['500', '400', '404', '501', '503', '401', '429'];
  }

  return (
    <KubernetesTracesListing
      showNamespaceFilter={false}
      showWorkloadFilter={false}
      destinationNamespace={drilldownQuery.workloadNamespace}
      destinationWorkload={drilldownQuery.workloadName}
      namespace={''}
      workloadName={''}
      accountId={accountId}
      passedSelectedTimestamp={{
        startTimestamp: defaultDateTimeRange.startDate,
        endTimestamp: defaultDateTimeRange.endDate,
      }}
      destinationName={''}
      showTimeFilter={true}
      httpStatus={statusCode}
      duration={duration}
      showStatusFilter={false}
    />
  );
};

export default KubernetesSLOConfigs;
