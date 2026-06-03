import { Box, Tooltip, Typography } from '@mui/material';
import React, { useEffect, useState, type JSX } from 'react';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import apiCloudAccount from '@api1/cloud-account';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import Currency from '@common-new/format/Currency';
import { Label } from '@components1/ds/Label';
import { OptimizeSummary } from './Summary';
import CopyableText from '@components1/common/CopyableText';
import Datetime from '@common-new/format/Datetime';
import CloudAccountEvents from '@components1/cloudaccount/CloudAccountEvents';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { DataBlock, CustomText } from '@components1/cloudaccount/common';
import { usePagination } from '@hooks/usePagination';
import { formatValueWithUnit, safeJSONParse } from 'src/utils/common';
import TagsCell from '@components1/cloudaccount/TagsCell';
import { buildStateApiParams, getInstanceState, getStateColor, getStateDropdownOptions } from '@components1/cloudaccount/stateFilter';
import { hasWriteAccess } from '@lib/auth';
import { getActionsForService, buildMenuItems } from '@components1/cloudaccount/resourceActions';
import { useCloudResourceAction } from '@hooks/useCloudResourceAction';
import ConfirmActionDialog from '@components1/cloudaccount/ConfirmActionDialog';
import RunSsmCommandDialog from '@components1/cloudaccount/RunSsmCommandDialog';
import ResourceActionHistory from '@components1/cloudaccount/ResourceActionHistory';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import DownloadButton from '@common-new/DownloadButton';
import { ds } from '@utils/colors';

const getFormattedUnitValue = (val: any, unit: string) => {
  if (typeof val !== 'number') {
    if (typeof val !== 'string') {
      return '-';
    }
    val = val.trim();
    if (val === '') {
      return '-';
    }
    val = Number(val);
  }
  if (!Number.isFinite(val)) {
    return '-';
  }
  if (unit === 'Percent') {
    unit = '%';
  }
  if (unit === 'Bytes') {
    const result = formatValueWithUnit(val, 'Memory');
    return `${Number(result.value.toFixed(2))}${result.unit}`;
  }
  if (unit === 'MB') {
    const result = formatValueWithUnit(val * 1024 * 1024, 'Memory');
    return `${Number(result.value.toFixed(2))}${result.unit}`;
  }
  if (unit.length > 3) {
    return `${val} ${unit}`;
  }
  return `${val}${unit}`;
};

/**
 * Safely parse tags that may be a JSON string or already an object.
 * Returns a key-value tag array suitable for DetailTagsEc2Table, or null if empty/invalid.
 */
function parseTags(tags: any): { Key: string; Value: string }[] | null {
  const parsed =
    typeof tags === 'string'
      ? (() => {
          try {
            return JSON.parse(tags);
          } catch {
            return null;
          }
        })()
      : tags;
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || Object.keys(parsed).length === 0) return null;
  return Object.entries(parsed).map(([key, values]: [string, any]) => ({
    Key: key,
    Value: Array.isArray(values) ? values.join(', ') : String(values ?? ''),
  }));
}

const DetailTagsEc2Table = ({ tagData }: any) => {
  const tagsTableId = 'ec2DetailTagsTable';
  if (tagData && tagData.length > 0) {
    const convertedJson2 = tagData.map((item: any) => {
      const data: ICustomTable2Row[] = [];
      data.push({
        component: <CustomText text1={item.Key} />,
      });
      data.push({
        component: <CustomText text1={item.Value} />,
      });
      return data;
    });
    return (
      <ListingLayout id={`${tagsTableId}-card`}>
        <ListingLayout.Toolbar title='Tags' actions={<DownloadButton id={`${tagsTableId}-download`} onClick={() => ({ tableId: tagsTableId })} />} />
        <ListingLayout.Body>
          <CustomTable2 id={tagsTableId} headers={['Field', 'Value']} tableData={convertedJson2} rowsPerPage={convertedJson2.length} />
        </ListingLayout.Body>
      </ListingLayout>
    );
  }
  return <div>Nothing</div>;
};
const EC2_HEADER = [
  { name: 'Name', width: '25%' },
  { name: 'Metric', width: '10%' },
  { name: 'State', width: '10%' },
  { name: 'Actions', width: '10%' },
  { name: 'Reason', width: '20%' },
  { name: 'Data', width: '25%' },
];

const formatStateReasonData = (stateReasonData: string): JSX.Element => {
  try {
    if (!stateReasonData) return <CustomText text1={'-'} />;
    const data = safeJSONParse(stateReasonData);
    const unit = data?.unit || '';
    const threshold = getFormattedUnitValue(data?.threshold, unit);
    const recentValue = getFormattedUnitValue(data?.recentDatapoints?.[0]?.toFixed(2), unit);
    const period = getFormattedUnitValue(data?.period, 's');
    const queryDate = data?.queryDate ? new Date(data.queryDate).toLocaleDateString() : '-';

    return (
      <Box>
        <CustomText text1={recentValue} subtext1={`Threshold: ${threshold}`} />
        <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption, marginTop: ds.space[1] }}>Period: {period}</Typography>
        <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption, marginTop: ds.space[1] }}>Query-Date: {queryDate}</Typography>
      </Box>
    );
  } catch (error) {
    console.error('Error parsing StateReasonData:', error);
    return <CustomText text1={'Invalid data'} />;
  }
};

const AlarmEC2Table = ({ alarmDetails }: any) => {
  const alarmTableId = 'ec2AlarmDetailsTable';
  const convertedJson2 = alarmDetails.map((item: any) => {
    const data: ICustomTable2Row[] = [];
    data.push({
      component: <CustomText text1={item.alarmName || item.AlarmName} subtext1={item.comparisonOperator || item.ComparisonOperator} />,
    });
    data.push({
      component: <CustomText text1={item.metricName || item.MetricName} subtext1={item.statistic || item.Statistic} />,
    });
    data.push({
      component: <CustomText text1={item.stateValue || item.StateValue} />,
    });
    data.push({
      component: <CustomText text1={item.actionsEnabled ?? item.ActionsEnabled ? 'Enabled' : 'Disabled'} />,
    });
    data.push({
      component: <CustomText text1={item.stateReason || item.StateReason} />,
    });
    data.push({
      component: formatStateReasonData(item.stateReasonData || item.StateReasonData),
    });
    return data;
  });
  return (
    <ListingLayout id={`${alarmTableId}-card`}>
      <ListingLayout.Toolbar
        title='Alarm Details'
        actions={<DownloadButton id={`${alarmTableId}-download`} onClick={() => ({ tableId: alarmTableId })} />}
      />
      <ListingLayout.Body>
        <CustomTable2 id={alarmTableId} headers={EC2_HEADER} tableData={convertedJson2} rowsPerPage={convertedJson2.length} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const DetailBlockDeviceMappingEc2Table = ({ blockDeviceMappingData }: any) => {
  const blockDeviceTableId = 'ec2BlockDeviceMappingTable';
  if (blockDeviceMappingData && blockDeviceMappingData.length > 0) {
    const convertedJson2 = blockDeviceMappingData.map((item: any) => {
      const data = [];
      data.push({
        component: <CustomText text1={item['Ebs'].VolumeId} />,
      });
      data.push({
        component: <CustomText text1={item.DeviceName} />,
      });
      data.push({
        component: <Datetime value={item['Ebs'].AttachTime} />,
      });
      data.push({
        component: <CustomText text1={item['Ebs'].DeleteOnTermination ? 'Yes' : 'No'} />,
      });
      data.push({
        component: <CustomText text1={item['Ebs'].Status} />,
      });
      return data;
    });
    return (
      <ListingLayout id={`${blockDeviceTableId}-card`}>
        <ListingLayout.Toolbar
          title='Block Device Mapping'
          actions={<DownloadButton id={`${blockDeviceTableId}-download`} onClick={() => ({ tableId: blockDeviceTableId })} />}
        />
        <ListingLayout.Body>
          <CustomTable2
            id={blockDeviceTableId}
            headers={['Volume Id', 'Device Name', 'Attachment Time', 'Delete on Termination', 'Attachment Status']}
            tableData={convertedJson2}
            rowsPerPage={convertedJson2.length}
          />
        </ListingLayout.Body>
      </ListingLayout>
    );
  }
  return <div>Nothing</div>;
};

const INSTANCE_HEADER = [
  { name: 'Instance ID', width: '16%' },
  { name: 'Instance Name', width: '16%' },
  { name: 'CPU usage', width: '10%' },
  { name: 'Memory usage', width: '10%' },
  { name: 'State', width: '8%' },
  { name: 'Cost', width: '8%' },
  { name: 'Tags', width: '18%' },
  { name: 'Launch Time', width: '10%' },
  { name: '', width: '4%' },
];

export interface ICustomTable2Row {
  component?: JSX.Element;
  drilldownQuery?: {
    podName?: any;
    workloadName?: any;
    namespaceName?: any;
    cpuRecc?: string;
    cpuReq?: string;
    memoryReq?: string;
    memoryRecc?: string;
    resourceId?: any;
    memLimit?: string | undefined;
    cpuLimit?: any;
    recommendation?: any;
    recommenedationDetails?: any;
    event?: any;
  };
  text?: any;
  data?: any;
}

// Strip GCP resource URL down to its trailing segment (e.g. ".../zones/us-central1-c" -> "us-central1-c").
const gcpTail = (s?: string): string => (s ? s.split('/').pop() || s : '');

const GCPComputeDisksTable = ({ disks }: { disks: any[] }) => {
  const tableId = 'gcpComputeDisksTable';
  const headers = ['Device Name', 'Size (GB)', 'Type', 'Mode', 'Boot', 'Auto Delete'];
  const rows = disks.map((disk: any) => {
    const row: ICustomTable2Row[] = [];
    row.push({ component: <CustomText text1={disk.device_name || '-'} /> });
    row.push({ component: <CustomText text1={disk.disk_size_gb !== undefined ? String(disk.disk_size_gb) : '-'} /> });
    row.push({ component: <CustomText text1={disk.type || '-'} /> });
    row.push({ component: <CustomText text1={disk.mode || '-'} /> });
    row.push({ component: <CustomText text1={disk.boot ? 'Yes' : 'No'} /> });
    row.push({ component: <CustomText text1={disk.auto_delete ? 'Yes' : 'No'} /> });
    return row;
  });
  return (
    <ListingLayout id={`${tableId}-card`}>
      <ListingLayout.Toolbar title='Disks' actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />} />
      <ListingLayout.Body>
        <CustomTable2 id={tableId} headers={headers} tableData={rows} rowsPerPage={rows.length} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const GCPComputeAlertPoliciesTable = ({ alertPolicies }: { alertPolicies: any[] }) => {
  const tableId = 'gcpComputeAlertPoliciesTable';
  const headers = [
    { name: 'Policy Name', width: '22%' },
    { name: 'Enabled', width: '8%' },
    { name: 'Condition', width: '28%' },
    { name: 'Threshold', width: '15%' },
    { name: 'Documentation', width: '27%' },
  ];
  const rows = alertPolicies.flatMap((policy: any) =>
    (policy.conditions || []).map((condition: any) => {
      const threshold = condition.conditionThreshold;
      const row: ICustomTable2Row[] = [];
      row.push({ component: <CustomText text1={policy.displayName || '-'} /> });
      row.push({ component: <CustomText text1={policy.enabled ? 'Yes' : 'No'} /> });
      row.push({ component: <CustomText text1={condition.displayName || '-'} /> });
      row.push({
        component: (
          <CustomText
            text1={threshold?.thresholdValue !== undefined ? String(threshold.thresholdValue) : '-'}
            subtext1={threshold?.comparison ? threshold.comparison.replace('COMPARISON_', '') : undefined}
          />
        ),
      });
      row.push({ component: <CustomText text1={policy.documentation?.subject || '-'} /> });
      return row;
    })
  );
  if (rows.length === 0) return null;
  return (
    <ListingLayout id={`${tableId}-card`}>
      <ListingLayout.Toolbar title='Alert Policies' actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />} />
      <ListingLayout.Body>
        <CustomTable2 id={tableId} headers={headers} tableData={rows} rowsPerPage={rows.length} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const GCP_SECTION_SX = {
  display: 'grid',
  gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
  columnGap: ds.space[4],
  rowGap: ds.space[5],
  mb: ds.space[5],
  backgroundColor: ds.background[100],
  padding: ds.space[5],
  borderRadius: ds.radius.md,
} as const;

const GCPSectionHeader = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ gridColumn: '1 / -1', fontWeight: ds.weight.semibold, fontSize: ds.text.bodyLg, color: ds.gray[700] }}>{children}</Typography>
);

// Prefer resourse_id (string from DB) over meta.id: GCP int64 IDs go through Go encoding/json
// -> float64, which loses precision above 2^53 (~9e15). Most GCP instance IDs exceed that.
const resolveGCPInstanceId = (drilldownQuery: any, meta: any): string | undefined => {
  if (drilldownQuery?.resourse_id !== undefined) return String(drilldownQuery.resourse_id);
  if (meta?.id !== undefined) return String(meta.id);
  return undefined;
};

const GCPComputeDetails = ({ drilldownQuery }: { drilldownQuery: any }) => {
  const meta = drilldownQuery?.meta || {};
  const instanceId = resolveGCPInstanceId(drilldownQuery, meta);
  const machineType = gcpTail(meta.machine_type);
  const zone = gcpTail(meta.zone);
  const networkInterfaces: any[] = Array.isArray(meta.network_interfaces) ? meta.network_interfaces : [];
  const primaryNic = networkInterfaces[0] || {};
  const externalIp = primaryNic?.access_configs?.[0]?.nat_i_p;
  const shielded = meta.shielded_instance_config || {};
  const scheduling = meta.scheduling || {};
  const serviceAccounts: any[] = Array.isArray(meta.service_accounts) ? meta.service_accounts : [];
  const disks: any[] = Array.isArray(meta.disks) ? meta.disks : [];
  const alertPolicies: any[] = Array.isArray(meta.AlertPolicies) ? meta.AlertPolicies : [];
  const memoryMiBRaw = meta?.InstanceTypeDetails?.MemoryInfo?.SizeInMiB;
  const memoryMiB = Number(memoryMiBRaw);
  const memoryDisplay = memoryMiBRaw !== undefined && Number.isFinite(memoryMiB) ? `${(memoryMiB / 1024).toFixed(2)} GiB` : null;
  const vcpus = meta?.InstanceTypeDetails?.VCpuInfo?.DefaultVCpus;
  const labelTags = parseTags(meta.labels);

  return (
    <>
      {/* Core Instance Info */}
      <Box sx={GCP_SECTION_SX}>
        {instanceId && <DataBlock title={'Instance Id'} data={instanceId} />}
        {meta.name && <DataBlock title={'Instance Name'} data={meta.name} />}
        {meta.status && <DataBlock title={'Status'} data={meta.status} isCopyable={false} />}
        {zone && <DataBlock title={'Zone'} data={zone} isCopyable={false} />}
        {machineType && <DataBlock title={'Machine Type'} data={machineType} isCopyable={false} />}
        {meta.cpu_platform && <DataBlock title={'CPU Platform'} data={meta.cpu_platform} isCopyable={false} />}
        {vcpus !== undefined && <DataBlock title={'vCPUs'} data={String(vcpus)} isCopyable={false} />}
        {memoryDisplay && <DataBlock title={'Memory'} data={memoryDisplay} isCopyable={false} />}
        {meta.creation_timestamp && (
          <DataBlock title={'Created'}>
            <Datetime value={meta.creation_timestamp} />
          </DataBlock>
        )}
        {meta.last_start_timestamp && (
          <DataBlock title={'Last Started'}>
            <Datetime value={meta.last_start_timestamp} />
          </DataBlock>
        )}
        {meta.deletion_protection !== undefined && (
          <DataBlock title={'Deletion Protection'} data={meta.deletion_protection ? 'Enabled' : 'Disabled'} isCopyable={false} />
        )}
        {meta.description && <DataBlock title={'Description'} data={meta.description} />}
      </Box>

      {/* Networking */}
      {networkInterfaces.length > 0 && (
        <Box sx={GCP_SECTION_SX}>
          <GCPSectionHeader>Networking</GCPSectionHeader>
          {primaryNic.name && <DataBlock title={'Interface'} data={primaryNic.name} isCopyable={false} />}
          {gcpTail(primaryNic.network) && <DataBlock title={'Network'} data={gcpTail(primaryNic.network)} />}
          {gcpTail(primaryNic.subnetwork) && <DataBlock title={'Subnetwork'} data={gcpTail(primaryNic.subnetwork)} />}
          {primaryNic.network_i_p && <DataBlock title={'Internal IP'} data={primaryNic.network_i_p} />}
          {externalIp && <DataBlock title={'External IP'} data={externalIp} />}
          {primaryNic.stack_type && <DataBlock title={'Stack Type'} data={primaryNic.stack_type} isCopyable={false} />}
        </Box>
      )}

      {/* Scheduling */}
      {(scheduling.provisioning_model ||
        scheduling.on_host_maintenance ||
        scheduling.preemptible !== undefined ||
        scheduling.automatic_restart !== undefined) && (
        <Box sx={GCP_SECTION_SX}>
          <GCPSectionHeader>Scheduling</GCPSectionHeader>
          {scheduling.provisioning_model && <DataBlock title={'Provisioning Model'} data={scheduling.provisioning_model} isCopyable={false} />}
          {scheduling.on_host_maintenance && <DataBlock title={'On Host Maintenance'} data={scheduling.on_host_maintenance} isCopyable={false} />}
          {scheduling.preemptible !== undefined && (
            <DataBlock title={'Preemptible'} data={scheduling.preemptible ? 'Yes' : 'No'} isCopyable={false} />
          )}
          {scheduling.automatic_restart !== undefined && (
            <DataBlock title={'Automatic Restart'} data={scheduling.automatic_restart ? 'Yes' : 'No'} isCopyable={false} />
          )}
        </Box>
      )}

      {/* Shielded VM */}
      {(shielded.enable_vtpm !== undefined || shielded.enable_secure_boot !== undefined || shielded.enable_integrity_monitoring !== undefined) && (
        <Box sx={GCP_SECTION_SX}>
          <GCPSectionHeader>Shielded VM</GCPSectionHeader>
          {shielded.enable_vtpm !== undefined && <DataBlock title={'vTPM'} data={shielded.enable_vtpm ? 'Enabled' : 'Disabled'} isCopyable={false} />}
          {shielded.enable_secure_boot !== undefined && (
            <DataBlock title={'Secure Boot'} data={shielded.enable_secure_boot ? 'Enabled' : 'Disabled'} isCopyable={false} />
          )}
          {shielded.enable_integrity_monitoring !== undefined && (
            <DataBlock title={'Integrity Monitoring'} data={shielded.enable_integrity_monitoring ? 'Enabled' : 'Disabled'} isCopyable={false} />
          )}
        </Box>
      )}

      {/* Service Accounts */}
      {serviceAccounts.length > 0 && (
        <Box sx={GCP_SECTION_SX}>
          <GCPSectionHeader>Service Accounts</GCPSectionHeader>
          {serviceAccounts.map((sa: any, idx: number) => (
            <DataBlock key={`${sa?.email ?? 'sa'}-${idx}`} title={'Email'} data={sa?.email || '-'} />
          ))}
        </Box>
      )}

      {/* Labels (tags) */}
      {labelTags && <DetailTagsEc2Table tagData={labelTags} />}

      {/* Disks */}
      {disks.length > 0 && <GCPComputeDisksTable disks={disks} />}

      {/* Alert Policies */}
      {alertPolicies.length > 0 && <GCPComputeAlertPoliciesTable alertPolicies={alertPolicies} />}
    </>
  );
};

const InstancesView = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  serviceName: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [ec2Instances, setEC2Instances] = useState([]);
  const [ec2InstancesCount, setEC2InstancesCount] = useState(0);
  const [loading, setLoading] = useState(false);
  // Two-state pattern (per ManualInvestigated.jsx): `selectedInstanceIdName`
  // tracks what the user is typing; `appliedInstanceIdName` is what the API
  // actually filters by. Fetch fires only on Enter or Clear.
  const [selectedInstanceIdName, setSelectedInstanceIdName] = useState('');
  const [appliedInstanceIdName, setAppliedInstanceIdName] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const [selectedRegion, setSelectedRegion] = useState<string | null>(null);
  const [availableRegions, setAvailableRegions] = useState<{ label: string; value: string }[]>([]);
  const stateOptions = getStateDropdownOptions(props?.serviceName);

  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const ec2OptimizeInstancesTable = 'ec2OptimizeInstancesTable';

  const ec2TypeMap: Record<string, string[]> = {
    AmazonEC2: ['compute-instance'],
    'Compute Engine': ['compute.googleapis.com/Instance'],
  };
  const ec2Type = ec2TypeMap[props?.serviceName] || ['virtualmachines'];

  const onSearchEnter = () => {
    setAppliedInstanceIdName(selectedInstanceIdName);
    setPage(0);
  };

  const onSearchClear = () => {
    setSelectedInstanceIdName('');
    setAppliedInstanceIdName('');
    setPage(0);
  };

  const ec2Actions = getActionsForService(props.serviceName);
  const writeAccess = hasWriteAccess(props?.accountId);

  const actionHook = useCloudResourceAction({
    accountId: props.accountId ?? '',
    serviceName: props.serviceName,
    onRefresh: () => listEC2Instances(),
  });

  const onMenuClick = (menuItem: { id: string }, data: any) => {
    const selectedAction = ec2Actions.find((a) => a.id === menuItem.id);
    if (selectedAction) {
      actionHook.initiateAction(selectedAction, data);
    }
  };

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, props?.serviceName, ec2Type).then(setAvailableTagKeys);
      apiCloudAccount.getDistinctRegions(props.accountId, props?.serviceName).then(setAvailableRegions);
    }
  }, [props?.accountId, props?.serviceName]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, props?.serviceName, ec2Type).then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  useEffect(() => {
    listEC2Instances();
  }, [
    props?.accountId,
    props?.serviceName,
    page,
    rowsPerPage,
    selectedTagKey,
    selectedTagValue,
    selectedState,
    selectedRegion,
    appliedInstanceIdName,
  ]);

  const listEC2Instances = async () => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    try {
      const res: any = await apiCloudAccount.getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: props?.serviceName,
          type: ec2Type,
          metric: ['average_cpuutilization', 'average_memoryutilization'],
          nameFilter: appliedInstanceIdName,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          region: selectedRegion || undefined,
          ...buildStateApiParams(props?.serviceName, selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      );

      const ec2ResourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
      const resources = res.data?.data?.cloud_resourses || [];

      // Fetch live CPU and memory metrics directly from cloud provider APIs
      let metricsMap: Record<string, Record<string, { value: number; timestamp: string }>> = {};
      if (resources.length > 0) {
        metricsMap = await apiCloudAccount.getLiveMetricsForResources({
          account_id: props.accountId,
          service_name: props.serviceName,
          resources: resources.map((item: any) => ({ resourse_id: item.resourse_id, region: item.region, meta: item.meta })),
        });
      }

      const ec2ResourceData = resources.map((item: any) => {
        const data: ICustomTable2Row[] = [];
        const nativeState = getInstanceState(props?.serviceName, item?.meta);
        const state = nativeState || item?.status || '-';
        const color = getStateColor(state);
        const resourceMetrics = metricsMap[item.resourse_id] || {};
        const cpuValue = resourceMetrics['cpu']?.value;
        const memValue = resourceMetrics['memory']?.value;

        data.push({
          component: <CustomText text1={item.resourse_id} subtext1={item.region} subtext2={item.meta?.InstanceType} />,
          drilldownQuery: item,
        });
        data.push({
          component: <CustomText text1={item.name || '-'} />,
        });
        data.push({
          component: (
            <CustomText
              text1={getFormattedUnitValue(cpuValue != null ? Number(cpuValue).toFixed(2) : undefined, '%')}
              subtext1={`avbl: ${getFormattedUnitValue(item.meta?.InstanceTypeDetails?.VCpuInfo?.DefaultVCpus, 'vCPU')}`}
            />
          ),
        });
        data.push({
          component:
            memValue != null ? (
              <CustomText
                text1={getFormattedUnitValue(Number(memValue).toFixed(2), '%')}
                subtext1={`avbl: ${getFormattedUnitValue(item.meta?.InstanceTypeDetails?.MemoryInfo?.SizeInMiB, 'MB')}`}
              />
            ) : (
              <Tooltip title='Requires monitoring agent (CloudWatch Agent / Ops Agent)' arrow placement='top'>
                <Box>
                  <CustomText text1='N/A' subtext1={`avbl: ${getFormattedUnitValue(item.meta?.InstanceTypeDetails?.MemoryInfo?.SizeInMiB, 'MB')}`} />
                </Box>
              </Tooltip>
            ),
        });
        data.push({
          component: <Label variant={color} text={state} />,
        });
        data.push({ component: <Currency value={item?.spends_aggregate?.aggregate?.sum?.amount} precison={1} /> });
        data.push({ component: <TagsCell tags={item.tags} /> });
        data.push({
          component: (
            <Datetime
              value={
                item?.meta?.LaunchTime || // AWS EC2
                item?.meta?.creation_timestamp || // GCP Compute Engine
                item?.meta?.properties?.timeCreated // Azure VM
              }
            />
          ),
        });
        const menuItems = buildMenuItems(ec2Actions, state, writeAccess);
        data.push({
          component:
            menuItems && menuItems.length > 0 ? (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={ds.space[1]}>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={menuItems.map((m: any) => ({
                    id: `ec2-action-${item.resourse_id}-${m.id}`,
                    label: m.label,
                    disabled: m.disabled,
                    onSelect: () => onMenuClick({ id: m.id }, item),
                  }))}
                  trigger={
                    <DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon fontSize='small' />} />
                  }
                />
              </Box>
            ) : undefined,
        });

        return data;
      });
      setEC2Instances(ec2ResourceData);
      setEC2InstancesCount(ec2ResourceCount);
    } catch (error) {
      console.error(error);
    } finally {
      setLoading(false);
    }
  };

  return (
    <ListingLayout id='right-sizing'>
      <ListingLayout.Toolbar
        title={props.heading || undefined}
        actions={<DownloadButton id={`${ec2OptimizeInstancesTable}-download`} onClick={() => ({ tableId: ec2OptimizeInstancesTable })} />}
      >
        <CustomSearch
          id='ec2-instances-search'
          label='Search By Instance Id/Name'
          value={selectedInstanceIdName}
          onChange={(next) => {
            // Auto-clear applied filter (and reset page) when user backspaces
            // the input to empty.
            if (selectedInstanceIdName !== '' && next === '') {
              setAppliedInstanceIdName('');
              setPage(0);
            }
            setSelectedInstanceIdName(next);
          }}
          onEnterPress={onSearchEnter}
          onClear={onSearchClear}
        />
        <FilterDropdown
          id='ec2-filter-state'
          label='State'
          value={selectedState}
          options={stateOptions}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedState(e.target.value || 'all');
          }}
        />
        <FilterDropdown
          id='ec2-filter-region'
          label='Region'
          value={selectedRegion}
          options={availableRegions}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedRegion(e.target.value || null);
          }}
        />
        <FilterDropdown
          id='ec2-filter-tag-key'
          label='Tag Key'
          value={selectedTagKey}
          options={availableTagKeys}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
          }}
        />
        <FilterDropdown
          id='ec2-filter-tag-value'
          label='Tag Value'
          value={selectedTagValue}
          options={availableTagValues}
          disabled={!selectedTagKey}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedTagValue(e.target.value || null);
          }}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CloudAccountTable
          id={ec2OptimizeInstancesTable}
          headers={INSTANCE_HEADER}
          data={ec2Instances}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={ec2InstancesCount}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'ec2-details',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  const isAzure = drilldownQuery.service_name?.toLowerCase().includes('microsoft.compute');
                  const isGcp =
                    drilldownQuery.service_name?.toLowerCase().includes('compute engine') || drilldownQuery.meta?.kind === 'compute#instance';

                  // GCP Compute Engine Details
                  if (isGcp) {
                    return <GCPComputeDetails drilldownQuery={drilldownQuery} />;
                  }

                  // Azure VM Details
                  if (isAzure) {
                    const azureProps = drilldownQuery.meta?.properties || {};
                    return (
                      <>
                        <Box
                          sx={{
                            display: 'grid',
                            gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
                            columnGap: ds.space[4],
                            rowGap: ds.space[5],
                            mb: ds.space[5],
                            backgroundColor: ds.background[100],
                            padding: ds.space[5],
                            borderRadius: ds.radius.md,
                          }}
                        >
                          {drilldownQuery.meta?.id && <DataBlock title={'Resource Id'} data={drilldownQuery.meta.id} />}
                          {azureProps.vmId && <DataBlock title={'VM Id'} data={azureProps.vmId} />}
                          {drilldownQuery.meta?.name && <DataBlock title={'VM Name'} data={drilldownQuery.meta.name} />}
                          {azureProps.hardwareProfile?.vmSize && <DataBlock title={'VM Size'} data={azureProps.hardwareProfile.vmSize} />}
                          {drilldownQuery.meta?.location && <DataBlock title={'Location'} data={drilldownQuery.meta.location} />}
                          {drilldownQuery.meta?.zones && drilldownQuery.meta.zones.length > 0 && (
                            <DataBlock title={'Availability Zone'} data={drilldownQuery.meta.zones.join(', ')} />
                          )}
                          {azureProps.osProfile?.computerName && <DataBlock title={'Computer Name'} data={azureProps.osProfile.computerName} />}
                          {azureProps.osProfile?.adminUsername && <DataBlock title={'Admin Username'} data={azureProps.osProfile.adminUsername} />}
                          {azureProps.storageProfile?.osDisk?.osType && (
                            <DataBlock title={'OS Type'} data={azureProps.storageProfile.osDisk.osType} />
                          )}
                          {azureProps.storageProfile?.osDisk?.name && (
                            <DataBlock title={'OS Disk Name'} data={azureProps.storageProfile.osDisk.name} />
                          )}
                          {azureProps.storageProfile?.osDisk?.diskSizeGB && (
                            <DataBlock title={'OS Disk Size'} data={`${azureProps.storageProfile.osDisk.diskSizeGB} GB`} isCopyable={false} />
                          )}
                          {azureProps.storageProfile?.osDisk?.managedDisk?.storageAccountType && (
                            <DataBlock
                              title={'Storage Type'}
                              data={azureProps.storageProfile.osDisk.managedDisk.storageAccountType}
                              isCopyable={false}
                            />
                          )}
                          {azureProps.provisioningState && (
                            <DataBlock title={'Provisioning State'} data={azureProps.provisioningState} isCopyable={false} />
                          )}
                          {azureProps.timeCreated && <DataBlock title={'Time Created'} data={new Date(azureProps.timeCreated).toLocaleString()} />}
                          {azureProps.networkProfile?.networkInterfaces && azureProps.networkProfile.networkInterfaces.length > 0 && (
                            <DataBlock title={'Network Interfaces'}>
                              {azureProps.networkProfile.networkInterfaces.map((ni: any, idx: number) => (
                                <Typography key={`${ni.id}-${idx}`} fontSize={ds.text.body}>
                                  <CopyableText copyableText={ni.id} iconColor={undefined} onCopy={undefined}>
                                    {ni.id.split('/').pop()}
                                  </CopyableText>
                                </Typography>
                              ))}
                            </DataBlock>
                          )}
                          {azureProps.securityProfile?.securityType && (
                            <DataBlock title={'Security Type'} data={azureProps.securityProfile.securityType} isCopyable={false} />
                          )}
                        </Box>
                        {parseTags(drilldownQuery.tags) && <DetailTagsEc2Table tagData={parseTags(drilldownQuery.tags)} />}
                        {azureProps.storageProfile?.dataDisks && azureProps.storageProfile.dataDisks.length > 0 && (
                          <ListingLayout id='azureDataDisksTable-card'>
                            <ListingLayout.Toolbar title='Data Disks' />
                            <ListingLayout.Body>
                              <CustomTable2
                                id='azureDataDisksTable'
                                headers={['Name', 'Size (GB)', 'Caching', 'Storage Type']}
                                tableData={azureProps.storageProfile.dataDisks.map((disk: any) => [
                                  { component: <CustomText text1={disk.name} /> },
                                  { component: <CustomText text1={disk.diskSizeGB?.toString()} /> },
                                  { component: <CustomText text1={disk.caching} /> },
                                  { component: <CustomText text1={disk.managedDisk?.storageAccountType} /> },
                                ])}
                                rowsPerPage={azureProps.storageProfile.dataDisks.length}
                              />
                            </ListingLayout.Body>
                          </ListingLayout>
                        )}
                      </>
                    );
                  }

                  // AWS EC2 Details
                  return (
                    <>
                      <Box
                        sx={{
                          display: 'grid',
                          gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
                          columnGap: ds.space[4],
                          rowGap: ds.space[5],
                          mb: ds.space[5],
                          backgroundColor: ds.background[100],
                          padding: ds.space[5],
                          borderRadius: ds.radius.md,
                        }}
                      >
                        {drilldownQuery.meta.InstanceId && <DataBlock title={'Instance Id'} data={drilldownQuery.meta.InstanceId} />}
                        {drilldownQuery.meta.PublicIpAddress && <DataBlock title={'Public IPv4'} data={drilldownQuery.meta.PublicIpAddress} />}
                        {drilldownQuery.meta.PublicDnsName && <DataBlock title={'Public IPv4 DNS'} data={drilldownQuery.meta.PublicDnsName} />}
                        {drilldownQuery.meta.PrivateDnsName && <DataBlock title={'Private IPv4 DNS'} data={drilldownQuery.meta.PrivateDnsName} />}
                        {drilldownQuery.meta.VpcId && <DataBlock title={'VPC'} data={drilldownQuery.meta.VpcId} />}
                        {drilldownQuery.meta.RootDeviceType && <DataBlock title={'Root device name'} data={drilldownQuery.meta.RootDeviceName} />}
                        {drilldownQuery.meta.EbsOptimized !== undefined && (
                          <DataBlock title={'Ebs optimization'} isCopyable={false} data={drilldownQuery.meta.EbsOptimized ? 'enabled' : 'disabled'} />
                        )}
                        {drilldownQuery.meta.RootDeviceType && (
                          <DataBlock title={'Root device type'} isCopyable={false} data={drilldownQuery.meta.RootDeviceType} />
                        )}
                        {drilldownQuery.meta.SecurityGroups && drilldownQuery.meta.SecurityGroups.length > 0 && (
                          <DataBlock title={'Security Groups'}>
                            {drilldownQuery.meta.SecurityGroups.map((sg: any, idx: number) => (
                              <Typography key={`${sg.GroupId ?? sg.GroupName}-${idx}`} fontSize={ds.text.body}>
                                <CopyableText copyableText={sg.GroupName} iconColor={undefined} onCopy={undefined}>
                                  {sg.GroupName}
                                </CopyableText>
                              </Typography>
                            ))}
                          </DataBlock>
                        )}
                        {drilldownQuery.meta?.SubnetId && <DataBlock title={'Subnet Id'} data={drilldownQuery.meta.SubnetId} />}
                        {drilldownQuery.meta?.NetworkInterfaces && drilldownQuery.meta?.NetworkInterfaces?.length > 0 ? (
                          <DataBlock title={'Private IPv4 addresses'}>
                            {drilldownQuery.meta.NetworkInterfaces.map((ni: any, idx: number) => (
                              <Typography key={`${ni.NetworkInterfaceId ?? ni.PrivateIpAddress}-${idx}`} fontSize={ds.text.body}>
                                <CopyableText copyableText={ni.PrivateIpAddress} iconColor={undefined} onCopy={undefined}>
                                  {ni.PrivateIpAddress}
                                </CopyableText>
                              </Typography>
                            ))}
                          </DataBlock>
                        ) : null}
                        {drilldownQuery.meta?.PlatformDetails && <DataBlock title={'Platform'} data={drilldownQuery.meta.PlatformDetails} />}
                        {drilldownQuery.meta?.Monitoring?.State && (
                          <DataBlock title={'Monitoring'} data={drilldownQuery.meta.Monitoring.State} isCopyable={false} />
                        )}
                      </Box>
                      {drilldownQuery.meta?.Tags && drilldownQuery.meta?.Tags.length > 0 && <DetailTagsEc2Table tagData={drilldownQuery.meta.Tags} />}
                      {!drilldownQuery.meta?.Tags?.length && parseTags(drilldownQuery.tags) && (
                        <DetailTagsEc2Table tagData={parseTags(drilldownQuery.tags)} />
                      )}
                      {drilldownQuery.meta?.BlockDeviceMappings && drilldownQuery.meta?.BlockDeviceMappings.length > 0 ? (
                        <DetailBlockDeviceMappingEc2Table blockDeviceMappingData={drilldownQuery.meta.BlockDeviceMappings} />
                      ) : null}
                      {drilldownQuery.meta.AlarmDetails && drilldownQuery.meta.AlarmDetails.length > 0 && (
                        <AlarmEC2Table alarmDetails={drilldownQuery.meta.AlarmDetails} />
                      )}
                    </>
                  );
                },
              },
              {
                text: 'Monitoring',
                value: 1,
                key: 'ec2-monitoring',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  return <OptimizeSummary accountId={props?.accountId} resourceId={drilldownQuery.resourse_id} serviceName={props.serviceName} />;
                },
              },
              {
                text: 'Events',
                value: 2,
                key: 'ec2-events',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  return <CloudAccountEvents accountId={props?.accountId} serviceName={props.serviceName} subjectName={drilldownQuery.resourse_id} />;
                },
              },
              {
                text: 'Action History',
                value: 3,
                key: 'ec2-action-history',
                componentFn: function (_opt: any, drilldownQuery: any, _row: any) {
                  return <ResourceActionHistory accountId={props?.accountId ?? ''} resourceId={drilldownQuery.resourse_id} />;
                },
              },
            ],
          }}
          tableHeadingCenter={props.tableHeadingCenter}
          stickyColumnIndex={props.stickyColumnIndex}
        />
      </ListingLayout.Body>
      {actionHook.selectedAction?.customDialog === 'ssm_run_command' ? (
        <RunSsmCommandDialog
          open={actionHook.isConfirmOpen}
          resource={actionHook.selectedResource}
          loading={actionHook.isLoading}
          onConfirm={(args) => actionHook.executeAction(args)}
          onCancel={actionHook.closeConfirm}
        />
      ) : (
        <ConfirmActionDialog
          open={actionHook.isConfirmOpen}
          action={actionHook.selectedAction}
          resource={actionHook.selectedResource}
          loading={actionHook.isLoading}
          confirmInput={actionHook.confirmInput}
          isStrictConfirmValid={actionHook.isStrictConfirmValid}
          actionArgs={actionHook.actionArgs}
          onConfirmInputChange={actionHook.setConfirmInput}
          onActionArgsChange={actionHook.setActionArgs}
          onConfirm={() => actionHook.executeAction()}
          onCancel={actionHook.closeConfirm}
        />
      )}
    </ListingLayout>
  );
};
export default InstancesView;
