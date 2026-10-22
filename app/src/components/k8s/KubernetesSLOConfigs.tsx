import apiKubernetes1 from '@api1/kubernetes1';
import { ListingLayout } from '@ui/ListingLayout';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import FilterDropdown from '@ui/FilterDropdown';
import DownloadButton from '@shared/buttons/DownloadButton';
import { Button } from '@ui/Button';
import Chart from '@ui/Chart';
import CustomTable from '@shared/tables/CustomTable';
import { getYesterday, getLast30Days } from '@lib/datetime';
import React, { useEffect, useMemo, useState } from 'react';
import { convertDateStringForSLOReportChart, formatObjectivePercent, formatSeconds, snakeToTitleCase } from 'src/utils/common';
import KubernetesTracesListing from './details/KubernetesTracesListing';
import ThreeDotLoader from '@shared/ThreeDotLoader';
import { Label } from '@ui/Label';
import Text from '@shared/format/Text';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { DeleteIconRed as DeleteIcon, EditIcon } from '@assets';
import KubernetesEventsTable from '@components/events/KubernetesEvents';
import SLOConfigDialog from '@components/k8s/common/SLOConfigDialog';
import { sloFingerprint } from '@lib/eventFingerprint';
import { toast as snackbar } from '@ui/Toast';
import { hasWriteAccess } from '@lib/auth';
import SafeIcon from '@shared/icons/SafeIcon';
import { CPU_SLO_TYPES, forecastStatus, forecastSummary, isBreachedCpuSlo, isCpuSloName, parseForecast } from '@components/k8s/common/cpuForecast';

// The four event keys a CPU SLO raises (see the backend forecast package).
const CPU_FORECAST_EVENT_KEYS = ['CPUThrottlingPredicted', 'CPUSaturationPredicted', 'CPUThrottlingSLOBreached', 'CPUSaturationSLOBreached'];

// The status cell: FIRING with the firing SLOs' names, or OK.
const sloStatusCell = (firingNames: string[]) => ({
  component: (
    <>
      <Label text={firingNames.length > 0 ? `FIRING` : 'OK'} />
      {firingNames.length > 0 ? <Text secondaryText value={`[${firingNames.join(', ')}]`} /> : null}
    </>
  ),
});

// CPU SLOs write no report rows, so a breach is read from their latest forecast.
const breachedCpuSloNames = (row: any[]) =>
  (row[0]?.drilldownQuery?.cpuConfigs ?? []).filter((c: any) => isBreachedCpuSlo(c)).map((c: any) => snakeToTitleCase(String(c.name)));

// One line per CPU SLO on the workload: its forecast status and summary.
const CpuForecastCell = ({ configs }: { configs: any[] }) => {
  if (configs.length === 0) return <Text value='-' />;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      {configs.map((c: any) => {
        const forecast = parseForecast(c.last_forecast);
        const status = forecastStatus(forecast);
        const type = CPU_SLO_TYPES.find((t) => t.name === String(c.name).toLowerCase());
        return (
          <Box key={c.name}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <Text value={`${type?.title ?? c.name} ≤ ${Number(c.threshold)}%`} />
              <Label text={status.text} tone={status.tone} size='sm' />
            </Box>
            <Text secondaryText value={forecastSummary(forecast)} />
          </Box>
        );
      })}
    </Box>
  );
};

interface KubernetesSLOConfigsProps {
  accountId: string;
}

const KubernetesSLOConfigs: React.FC<KubernetesSLOConfigsProps> = ({ accountId }) => {
  const [tableData, setTableData] = useState<any>([]);
  const [loading, setLoading] = useState(false);
  const [workloadFqdn, setWorkloadFqdn] = useState<string[]>([]);
  const [sharedVariableForFindingIds, setSharedVariableForFindingIds] = useState([]);
  const [allSLOData, setAllSLOData] = useState<any[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>('');
  const [selectedWorkload, setSelectedWorkload] = useState<string>('');
  const [openSLODialog, setOpenSLODialog] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [deleteTarget, setDeleteTarget] = useState<{ workloadName: string; workloadNamespace: string } | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [editTarget, setEditTarget] = useState<{
    workload: { name: string; namespace: string; cloud_resource_id: string };
    config: any[];
  } | null>(null);

  // Cross-filter: namespace options narrow when a workload is selected
  const namespaceFilter = useMemo(() => {
    const base = selectedWorkload ? (allSLOData || []).filter((d) => d.workload_name === selectedWorkload) : allSLOData || [];
    return Array.from(new Set(base.map((d: any) => d.workload_namespace as string).filter(Boolean)));
  }, [allSLOData, selectedWorkload]);

  // Cross-filter: workload options narrow when a namespace is selected
  const workloadFilter = useMemo(() => {
    const base = selectedNamespace ? (allSLOData || []).filter((d) => d.workload_namespace === selectedNamespace) : allSLOData || [];
    return Array.from(new Set(base.map((d: any) => d.workload_name as string).filter(Boolean)));
  }, [allSLOData, selectedNamespace]);

  const handleDelete = (workloadName: string, workloadNamespace: string) => {
    if (!hasWriteAccess(accountId)) {
      snackbar.error('You do not have write access to delete SLO configs');
      return;
    }
    setDeleteTarget({ workloadName, workloadNamespace });
  };

  const handleEdit = (workloadName: string, workloadNamespace: string, config: any[]) => {
    if (!hasWriteAccess(accountId, workloadNamespace)) {
      snackbar.error('You do not have write access to update SLO configs');
      return;
    }
    // slo_config stores no workload id and the upsert keys on name/namespace, so none is needed.
    setEditTarget({ workload: { name: workloadName, namespace: workloadNamespace, cloud_resource_id: '' }, config });
  };

  const confirmDelete = () => {
    if (!deleteTarget) return;
    setDeleting(true);
    apiKubernetes1
      .deleteSLOConfig({
        cloud_account_id: accountId,
        workload_name: deleteTarget.workloadName,
        namespace: deleteTarget.workloadNamespace,
      })
      .then((res: any) => {
        if (res?.data?.data?.slo_config_delete?.data?.success) {
          snackbar.success('SLO Config deleted');
          setRefreshKey((prev) => prev + 1);
        } else {
          snackbar.error('Failed to delete SLO Config');
        }
      })
      .catch(() => snackbar.error('Failed to delete SLO Config'))
      .finally(() => {
        setDeleting(false);
        setDeleteTarget(null);
      });
  };

  const header = [
    { name: 'Workload Name', width: '20%' },
    { name: 'Window', width: '12%' },
    { name: 'Objective', width: '12%' },
    { name: 'Status (30D)', width: '12%' },
    { name: 'Latency', width: '12%' },
    { name: 'Availability', width: '12%' },
    { name: 'Observation (30D)', width: '20%' },
    { name: 'CPU Forecast', width: '20%' },
    { name: '', width: '8%' },
  ];

  // Fetch all SLO configs unfiltered — drives the cross-filtered dropdown options
  useEffect(() => {
    if (!accountId) return;
    apiKubernetes1
      .listSLOConfigs({ cloud_account_id: accountId })
      .then((res) => setAllSLOData(res?.data?.data?.slo_config || []))
      .catch((err) => console.error('Failed to fetch SLO configs:', err));
  }, [accountId, refreshKey]);

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

        const workloadFqdn: string[] = [];
        if (filteredData.length > 0) {
          const groupedWorkloads = filteredData.reduce((acc: any, cur: any) => {
            const { workload_name, workload_namespace, name, threshold, goal, id, enabled, window, created_at, updated_at, last_forecast } = cur;
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

            acc[key].config.push({ name, threshold, goal, id, enabled, last_forecast });

            return acc;
          }, {});

          const result = Object.values(groupedWorkloads);
          const tableData = result.map((m: any) => {
            let objective;
            let availability;
            let latency;
            let availabilityConfig = [];
            let latencyConfig = [];
            const cpuConfigs = (m.config || []).filter((n: any) => isCpuSloName(n.name));
            if (m.config && m.config.length > 0) {
              availabilityConfig = m.config.filter((n: any) => n.name == 'availability');
              if (availabilityConfig && availabilityConfig.length == 1) {
                availability = formatObjectivePercent(availabilityConfig[0].goal);
              }
              latencyConfig = m.config.filter((n: any) => n.name == 'latency');
              if (latencyConfig && latencyConfig.length == 1) {
                objective = formatObjectivePercent(latencyConfig[0].goal);
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
                  cpuConfigs,
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
              {
                component: <CpuForecastCell configs={cpuConfigs} />,
              },
              {
                // Namespace admins may update SLOs in their namespaces; delete stays account-level.
                component: hasWriteAccess(accountId, m.workload_namespace) ? (
                  <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
                    <Button
                      tone='secondary'
                      composition='icon-only'
                      aria-label={`Edit SLO config for ${m.workload_name}`}
                      id={`slo-edit-${m.workload_namespace}-${m.workload_name}`}
                      onClick={() => handleEdit(m.workload_name, m.workload_namespace, m.config)}
                      icon={<SafeIcon src={EditIcon} alt='edit' width={20} height={20} />}
                    />
                    {hasWriteAccess(accountId) && (
                      <Button
                        tone='secondary'
                        composition='icon-only'
                        aria-label={`Delete SLO config for ${m.workload_name}`}
                        disabled={deleting}
                        id={`slo-delete-${m.workload_namespace}-${m.workload_name}`}
                        onClick={() => handleDelete(m.workload_name, m.workload_namespace)}
                        icon={<SafeIcon src={DeleteIcon} alt='delete' width={20} height={20} />}
                      />
                    )}
                  </Box>
                ) : null,
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

                const firingNames = item.filter((g: any) => g.status == 'FIRING').map((g: any) => snakeToTitleCase(g.config_name));
                element[3] = sloStatusCell([...firingNames, ...breachedCpuSloNames(element)]);
                element[6] = {
                  text: <Typography style={{ whiteSpace: 'pre-line' }}>{observation}</Typography>,
                };
              } else {
                element[6] = {
                  text: '-',
                };
                if (breachedCpuSloNames(element).length > 0) {
                  element[3] = sloStatusCell(breachedCpuSloNames(element));
                }
              }
            }
            setTableData([...tableData]);
          } else {
            for (const element of tableData) {
              element[6] = {
                text: '-',
              };
              if (breachedCpuSloNames(element).length > 0) {
                element[3] = sloStatusCell(breachedCpuSloNames(element));
              }
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
              {hasWriteAccess(accountId) && (
                <Button onClick={handleOpenSLODialog} id='add-slo-config-btn'>
                  Add SLO
                </Button>
              )}
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
                {
                  componentFn: function (opt: any, drilldownQuery: any) {
                    return <SLOCpuAlerts drilldownQuery={drilldownQuery} accountId={accountId} />;
                  },
                  text: 'CPU Alerts',
                  value: 4,
                  key: 'slo-cpu-alerts',
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
      <SLOConfigDialog
        open={editTarget !== null}
        onClose={() => setEditTarget(null)}
        accountId={accountId}
        workload={editTarget?.workload}
        initialConfig={editTarget?.config}
        isEdit
        onSuccess={() => setRefreshKey((prev) => prev + 1)}
      />
      <Modal
        open={deleteTarget !== null}
        handleClose={() => setDeleteTarget(null)}
        title='Delete SLO Config'
        width='sm'
        loader={deleting}
        confirmText='Delete'
        confirmTone='danger'
        onConfirm={confirmDelete}
        confirmDisabled={deleting}
      >
        <Typography>
          {`Delete all SLOs on ${deleteTarget?.workloadName} in ${deleteTarget?.workloadNamespace}? Their historical reports will be removed and open CPU forecast alerts closed.`}
        </Typography>
      </Modal>
    </>
  );
};

export const SLOReport = ({
  drilldownQuery = {
    availabilityConfig: {} as any,
    latencyConfig: {} as any,
    workloadName: '',
    workloadNamespace: '',
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
    workloadName?: string;
    workloadNamespace?: string;
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
              // Every report row for this config/workload shares one event
              // (see @lib/eventFingerprint) — one fingerprint, not one per row.
              availabilityFindingIds = data.some((g: any) => g.bad_events_count > 0)
                ? [sloFingerprint(accountId, 'availability', drilldownQuery?.workloadName ?? '', drilldownQuery?.workloadNamespace ?? '')]
                : [];
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
            account_id: accountId,
            start_date: new Date(selectedDateRange.startDate).toISOString(),
            end_date: new Date(selectedDateRange.endDate).toISOString(),
          })
          .then((res) => {
            const data = res?.data?.data?.slo_report || [];
            if (data.length > 0) {
              // Every report row for this config/workload shares one event
              // (see @lib/eventFingerprint) — one fingerprint, not one per row.
              latencyFindingIds = data.some((g: any) => g.bad_events_count > 0)
                ? [sloFingerprint(accountId, 'latency', drilldownQuery?.workloadName ?? '', drilldownQuery?.workloadNamespace ?? '')]
                : [];
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
          <Chart.Line
            chartTitle='Availiabity Report'
            dataset={availabilityData.data}
            labels={availabilityData.label}
            data={[]}
            loading={loadingAvailability}
          />
          <Chart.Line chartTitle='Latency Report' dataset={latencyData.data} labels={latencyData.label} data={[]} loading={loadingLatency} />
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

// The forecast and breach alerts of a workload's CPU SLOs; each row opens the alert.
const SLOCpuAlerts = ({ drilldownQuery = {} as any, accountId = '' }) => {
  // Fixed once per mount: the events table resets its paging whenever its query
  // changes, and a window ending at "now" would change on every re-render.
  const [range] = useState(() => ({ startTime: getLast30Days(new Date()).getTime(), endTime: new Date().getTime() }));
  if (!drilldownQuery.cpuConfigs?.length) {
    return <Typography>No CPU SLO on this workload</Typography>;
  }
  return (
    <KubernetesEventsTable
      accountId={accountId}
      enableTrendChart={false}
      enableFilters={false}
      heading={''}
      defaultQuery={{
        namespace: drilldownQuery.workloadNamespace,
        workloadName: drilldownQuery.workloadName,
        startTime: range.startTime,
        endTime: range.endTime,
        aggregation_key: CPU_FORECAST_EVENT_KEYS,
        finding_type: '',
      }}
      showTimeFilter={false}
    />
  );
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
    // Must match what the availability SLI actually counts as a bad event
    // (5xx). The old list mixed in 4xx codes, so this tab showed traces that
    // never consumed any error budget.
    statusCode = ['500', '501', '502', '503', '504'];
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
