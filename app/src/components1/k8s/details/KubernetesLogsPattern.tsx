import React, { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import ticketsApi from '@api1/tickets';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import { type WorkloadObject, extractNamespaceAndApplication, extractWorkloadName } from 'src/utils/common';
import Text from '@common-new/format/Text';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { TicketsIcon } from '@assets';
import { DEFAULT_TITLE, getNubiIconUrl } from '@hooks/useTenantBranding';
import { getAllowedNamespaces } from '@lib/auth';
import { Box } from '@mui/material';
import { Switch } from '@components1/ds/Switch';
import EmptyData from '@components1/common/EmptyData';
import noDataImg from '@assets/Icon-no-data-available.svg';
import { useData } from '@context/DataContext';
import { action } from 'src/utils/actionStyles';
import KubernetesTracesListing from './KubernetesTracesListing';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import { Link } from '@components1/ds/Link';
import { toast as snackbar } from '@components1/ds/Toast';
import Datetime from '@common-new/format/Datetime';
import { Button as DsButton } from '@components1/ds/Button';
import CopyButton from '@common-new/CopyButton';
import SafeIcon from '@components1/common/SafeIcon';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import apiKubernetes1 from '@api1/kubernetes1';
import { md5 } from '@lib/encode';

// Parse namespace and workload from container_id, handling both 3-segment (/k8s/ns/workload)
// and 4-segment (/k8s/ns/workload/container) formats, with fallback to item-level fields.
function parseLogGroupItem(item: any) {
  const containerId = item?.container_id ?? '';
  const segments = containerId ? containerId.split('/').filter((s: string) => s !== '') : [];
  const isStandard = segments.length === 4;
  const namespace = isStandard
    ? extractNamespaceAndApplication(containerId, 'namespace') ?? segments[1]
    : segments.length >= 2
    ? segments[1]
    : item?.namespace ?? '';
  const workload = isStandard
    ? extractNamespaceAndApplication(containerId, 'application') ?? segments[2]
    : segments.length >= 3
    ? segments[2]
    : item?.workload ?? item?.container ?? '';
  return { namespace, workload };
}

interface TracesComponentProps {
  drilldownQuery: any;
  row: any;
  accountId: string;
}

const TracesComponent: React.FC<TracesComponentProps> = ({ drilldownQuery, accountId }) => {
  const timestamps = drilldownQuery?.timestamps ?? [];
  let minTime = timestamps.length > 0 ? Math.min(...timestamps) * 1000 : Date.now() - 10 * 60 * 1000;
  const maxTime = timestamps.length > 0 ? Math.max(...timestamps) * 1000 : Date.now() + 10 * 60 * 1000;
  if (minTime === maxTime) {
    minTime = maxTime - 5 * 60 * 1000;
  }

  return (
    <KubernetesTracesListing
      showNamespaceFilter={false}
      showWorkloadFilter={false}
      destinationNamespace={''}
      destinationWorkload={''}
      namespace={parseLogGroupItem(drilldownQuery).namespace}
      workloadName={parseLogGroupItem(drilldownQuery).workload}
      accountId={accountId}
      passedSelectedTimestamp={{
        startTimestamp: minTime,
        endTimestamp: maxTime,
      }}
      destinationName={''}
      showTimeFilter={false}
      httpStatus={''}
    />
  );
};

interface KubernetesLogsPatternProps {
  accountId: string;
  workloadName?: string;
  workloadNamespace?: string;
}

const KubernetesLogsPattern: React.FC<KubernetesLogsPatternProps> = ({ accountId, workloadName = '', workloadNamespace = '' }) => {
  const router = useRouter();
  const k8sGroupingLogs = 'k8sGroupingLogs';

  const [groupingLogLoading, setGroupingLogLoading] = useState(false);
  const [groupingLogErrorMsg, setGroupingLogErrorMsg] = useState('');
  const [groupingLogData, setGroupingLogData] = useState([]);
  const [selectedDateRange, setSelectedDateRange] = useState<any>({
    startDate: new Date().getTime() - 3600 * 12 * 1000,
    endDate: new Date().getTime(),
  });
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>(
    ((router.query?.namespace ?? router.query?.workloadNamespace) as string) ?? workloadNamespace ?? ''
  );
  const [allWorkload, setAllWorkload] = useState<WorkloadObject[]>([]);
  const [workloadFilter, setWorkloadFilter] = useState<string[]>([]);
  const [selectedWorkload, setSelectedWorkload] = useState<string>((router.query?.workloadName as string) ?? workloadName ?? '');
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [count, setCount] = useState(0);
  const [allowInActivePod, setAllowInActivePod] = useState(false);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiSessionId, setNubiSessionId] = useState('');
  const { providerCapabilities } = useData();
  const logsCaps = providerCapabilities.find((e: any) => e.provider_type === 'logs')?.capabilities;
  const supportsFeature = logsCaps?.supports_log_groups ?? null;

  useEffect(() => {
    handleSubmit();
  }, [accountId, selectedNamespace, selectedDateRange.startDate, selectedDateRange.endDate, selectedWorkload]);

  useEffect(() => {
    if (!accountId || workloadName) {
      return;
    }
    k8sApi.getK8sNamespaceNames(accountId).then((res) => {
      const namespaces = res.data.namespaces as string[];
      setNamespaceFilter(namespaces);
    });
  }, [accountId]);

  useEffect(() => {
    if (!accountId || workloadNamespace) {
      return;
    }
    const query = {
      accountId: accountId,
      allow_in_active_pod: allowInActivePod,
    };
    k8sApi.getAllK8sWorkload(query).then((res) => {
      const data = res?.data as any[];
      const workloadNames = data.map((e: any) => e.name) as string[];
      setWorkloadFilter([...new Set(workloadNames)]);
      setAllWorkload(data);
      setAllWorkload(res?.data);
    });
  }, [accountId, allowInActivePod]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const onNamespaceFilterChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    if (e?.target?.value) {
      setSelectedNamespace(e.target.value);
      applyFiltersOnRouter(router, { namespace: e.target.value, workloadName: '' });
      const filterWorkloads = allWorkload.filter((f) => f.namespace == e.target.value).map((d) => d.name);
      setWorkloadFilter(filterWorkloads);
    } else {
      setWorkloadFilter(allWorkload.map((e: WorkloadObject) => e.name));
      setSelectedNamespace('');
      applyFiltersOnRouter(router, { namespace: '', workloadName: '' });
    }
    setSelectedWorkload('');
  };

  const onWorkloadFilterChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    applyFiltersOnRouter(router, { workloadName: e.target.value || '' });
    setSelectedWorkload(e.target.value || '');
  };

  const onToggleChangeAllowInActivePod = (e: boolean) => {
    setAllowInActivePod(e);
  };

  const onMenuClick = (menuItems: any, data: any) => {
    const c = data?.values?.reduce((accumulator: number, currentValue: string) => accumulator + parseInt(currentValue), 0);
    setCount(c);
    if (menuItems.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const handleGenerateLogAnalysis = (item: any) => {
    const containerIds = item?.container_id?.split('/');
    const namespace = containerIds?.[2] ?? item?.namespace ?? '';
    const pod = containerIds?.[3] ?? item?.workload ?? item?.container ?? '';
    const workloadName = extractWorkloadName(pod);

    setNubiQuery(
      `@loganalysis analyse the following log and provide the root cause and possible actions to resolve the issue.\n namespace: ${namespace}, pod - ${pod}, workload - ${workloadName}  \n\n Log - ${item.sample}`
    );
    setNubiSessionId(md5([item?.pattern_hash ?? item?.sample ?? '']));
    setNubiSidebarVisible(true);
  };

  const handleSubmit = () => {
    setGroupingLogLoading(true);
    setGroupingLogErrorMsg('');
    apiKubernetes1
      .logGroup({
        account_id: accountId,
        end_time: selectedDateRange.endDate,
        start_time: selectedDateRange.startDate,
        request: {
          ...(selectedNamespace && { selectedNamespace }),
          ...(selectedWorkload && { selectedWorkload }),
        },
      })
      .then((res) => {
        const evidence = res?.data?.data?.log_group?.groups || [];
        if (evidence.length > 0) {
          const uniqueReferenceIds = new Set<string>();
          evidence?.forEach((item: any) => {
            uniqueReferenceIds.add(item.pattern_hash);
          });
          const references = Array.from(uniqueReferenceIds);
          return ticketsApi.listTicketsSummary({ reference_id: references }).then((res: any) => {
            const ticketReferenceMap = new Map<string, any>();
            res?.data?.tickets?.forEach((element: any) => {
              ticketReferenceMap.set(element.reference_id, element);
            });
            let data = evidence;
            const allowedNamespace = getAllowedNamespaces(accountId);
            if (allowedNamespace != null && allowedNamespace.length > 0) {
              data = data.filter((item: any) => {
                const { namespace } = parseLogGroupItem(item);
                return allowedNamespace.includes(namespace);
              });
            }
            let filteredData = data?.filter((g: any) =>
              Array.isArray(g.values) ? g.values.reduce((sum: number, v: number) => sum + (v || 0), 0) > 0 : parseFloat(g.values) > 0
            );
            let isFiltered = false;
            if (filteredData?.length > 500) {
              filteredData = filteredData.slice(0, 500);
              isFiltered = true;
            }
            const groupData = filteredData.map((item: any) => {
              const logReferenceId = item.pattern_hash;
              const MENU_ITEMS: any = [
                {
                  icon: TicketsIcon,
                  label: 'Create Ticket',
                  id: 0,
                  disabled: ticketReferenceMap.has(logReferenceId),
                },
              ];
              const { namespace: namespaceName, workload: app } = parseLogGroupItem(item);
              const logQuery = `{"namespaceName": "${namespaceName}", "workloadName": "${app}"}`;
              return [
                {
                  component: (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
                      <Box sx={{ flexShrink: 0 }}>
                        <CopyButton text={item?.sample ?? ''} size='sm' />
                      </Box>
                      <Box sx={{ flex: 1, minWidth: 0 }}>
                        <Text value={item?.sample ?? ''} showAutoEllipsis />
                      </Box>
                    </Box>
                  ),
                  drilldownQuery: { ...item, logQuery, data: { timestamp: Math.max(...(item?.timestamps ?? [])) * 1000 } },
                },
                {
                  component: (
                    <Box>
                      <Text showAutoEllipsis value={app || ''} />
                      <Text secondaryText value={`ns: ${namespaceName}`} />
                    </Box>
                  ),
                },
                {
                  component: item?.timestamps ? <Datetime value={Math.max(...(item?.timestamps ?? [])) * 1000} /> : '-',
                },
                {
                  component: (
                    <Text
                      value={
                        Array.isArray(item?.values) && item?.values.length > 0
                          ? item.values.reduce((accumulator: number, currentValue: number) => accumulator + (currentValue || 0), 0).toFixed()
                          : '-'
                      }
                    />
                  ),
                },
                {
                  component: ticketReferenceMap.has(logReferenceId) ? (
                    <Link target='_blank' href={`${ticketReferenceMap.get(logReferenceId)?.url}`}>
                      {ticketReferenceMap.get(logReferenceId)?.ticket_id}
                    </Link>
                  ) : (
                    '-'
                  ),
                },
                {
                  component: (
                    <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}>
                      <DsButton
                        tone='secondary'
                        size='xs'
                        composition='icon-only'
                        aria-label={`Ask ${DEFAULT_TITLE}`}
                        tooltip={`Ask ${DEFAULT_TITLE}`}
                        icon={<SafeIcon src={getNubiIconUrl()} width={20} height={20} alt={`Ask ${DEFAULT_TITLE}`} />}
                        onClick={(e) => {
                          e.stopPropagation();
                          handleGenerateLogAnalysis(item);
                        }}
                      />
                      <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
                    </Box>
                  ),
                },
              ];
            });
            setGroupingLogData(groupData);
            if (isFiltered) {
              snackbar.info('Showing first 500 records. Use filters to view specific data.');
            }
          });
        } else {
          setGroupingLogData([]);
        }
      })
      .catch((_error) => {
        setGroupingLogData([]);
        setGroupingLogErrorMsg(`Failed to fetch the Log Group`);
      })
      .finally(() => {
        setGroupingLogLoading(false);
      });
  };

  const getTicketDescription = (data: any) => {
    let description = '';
    const { namespace: ticketNs, workload: ticketApp } = parseLogGroupItem(data);
    description += '**Namespace**: ' + ticketNs + '\n';
    description += '**Application**: ' + ticketApp + '\n';
    description += '**Count**: ' + count + '\n';
    description += '**Sample**: ' + data?.sample + '\n';
    return description;
  };

  const getTicketId = (data: any) => {
    let id = '';
    id += data?.pattern_hash;
    return id;
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const handleTicketSuccess = () => {
    handleSubmit();
  };

  const handleTicketFailure = (res: string) => {
    snackbar.error(`Failed! ${res}.`);
  };

  if (supportsFeature === false) {
    return (
      <Box
        sx={{
          border: '1px solid var(--ds-gray-300)',
          borderRadius: 'var(--ds-radius-lg)',
          bgcolor: 'var(--ds-background-100)',
        }}
      >
        <EmptyData
          id='log-grouping-unsupported'
          img={noDataImg}
          heading='Log Grouping not supported'
          subHeading='Your current log provider does not support log grouping.'
          height='400px'
          sx={{ flexDirection: 'column', gap: 'var(--ds-space-4)', textAlign: 'center' }}
        />
      </Box>
    );
  }

  return (
    <div>
      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={accountId}
        query={nubiQuery}
        context={{ type: 'cluster', data: { conversationId: nubiSessionId } }}
        apiMode='investigate'
        source='log_pattern_analysis'
        position='right'
        mode='overlay'
        width='500px'
      />
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Log Group',
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        ticketUrl={{}}
        reference={{
          id: getTicketId(ticketData),
          type: 'kubernetes',
        }}
      />
      <ListingLayout id='logs-pattern'>
        <ListingLayout.Toolbar
          actions={
            <>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                  shortcutClickTime: 0,
                }}
                onChange={({ selection }) => handleDateRangeChange(selection)}
              />
              <DownloadButton onClick={() => ({ tableId: k8sGroupingLogs })} />
            </>
          }
        >
          {!workloadNamespace && (
            <FilterDropdown
              label='Namespace'
              options={namespaceFilter.map((o) => ({ value: o, label: o }))}
              value={selectedNamespace}
              onSelect={(e: React.ChangeEvent<HTMLInputElement>) => onNamespaceFilterChange(e as any)}
            />
          )}
          {!workloadName && (
            <FilterDropdown
              label='Application'
              options={workloadFilter.map((o) => ({ value: o, label: o }))}
              value={selectedWorkload}
              onSelect={(e: React.ChangeEvent<HTMLInputElement>) => onWorkloadFilterChange(e as any)}
            />
          )}
          <Switch
            id='in-active-workloads'
            checked={allowInActivePod}
            onChange={(_e: React.ChangeEvent<HTMLInputElement>, checked: boolean) => onToggleChangeAllowInActivePod(checked)}
            label='Include In-Active Workloads'
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <KubernetesTable2
            id={k8sGroupingLogs}
            totalRows={groupingLogData.length}
            data={groupingLogData}
            headers={[
              {
                name: 'Sample',
                width: '50%',
              },
              {
                name: 'Application',
                width: '20%',
              },
              {
                name: 'Last Time',
                width: '10%',
              },
              {
                name: 'Count',
                width: '5%',
              },
              {
                name: 'Ticket',
                width: '10%',
              },
              {
                name: '',
                width: '10%',
              },
            ]}
            rowsPerPage={groupingLogData.length}
            showExpandable={true}
            expandable={{
              tabs: [
                { text: 'Sample Details', value: 0, key: 'log-group-detail' },
                {
                  text: '+/- Logs',
                  value: 1,
                  key: 'log-plus-minus',
                },
                {
                  componentFn: (_opt: any, drilldownQuery: any, row: any) => (
                    <TracesComponent drilldownQuery={drilldownQuery} row={row} accountId={accountId} />
                  ),
                  text: 'Traces',
                  value: 2,
                  key: 'traces',
                },
              ],
            }}
            loading={groupingLogLoading}
            errorMessage={groupingLogErrorMsg}
            onPageChange={undefined}
            onSortChange={undefined}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default KubernetesLogsPattern;
