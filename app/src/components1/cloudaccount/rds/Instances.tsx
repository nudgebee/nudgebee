import { Box, Typography } from '@mui/material';
import React, { useEffect, useState, type JSX } from 'react';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import apiCloudAccount from '@api1/cloud-account';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import CustomLabels from '@common-new/widgets/CustomLabels';
import CloudAccountEvents from '@components1/cloudaccount/CloudAccountEvents';
import CopyableText from '@components1/common/CopyableText';
import OptimizeSummary from '@components1/cloudaccount/ec2/Summary';
import Datetime from '@common-new/format/Datetime';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { DataBlock } from '@components1/cloudaccount/common';
import { usePagination } from '@hooks/usePagination';
import PerformanceInsights from '../PerformanceInsights';
import CloudLogs from './CloudLogs';
import TagsCell from '@components1/cloudaccount/TagsCell';
import { buildStateApiParams, getInstanceState, getStateColor, getStateDropdownOptions } from '@components1/cloudaccount/stateFilter';
import { hasWriteAccess } from '@lib/auth';
import { getActionsForService, buildMenuItems } from '@components1/cloudaccount/resourceActions';
import { useCloudResourceAction } from '@hooks/useCloudResourceAction';
import ConfirmActionDialog from '@components1/cloudaccount/ConfirmActionDialog';
import ResourceActionHistory from '@components1/cloudaccount/ResourceActionHistory';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import DownloadButton from '@common-new/DownloadButton';

const INSTANCE_HEADER = ['Instance Name', 'State', 'Engine', 'Launch Time', 'Storage', 'Tags', 'Storage Type', ''];
const RDS_HEADER = [
  { name: 'Name', width: '25%' },
  { name: 'Metric', width: '10%' },
  { name: 'State', width: '10%' },
  { name: 'Actions', width: '10%' },
  { name: 'Reason', width: '20%' },
  { name: 'Data', width: '25%' },
];
// Resolves the correct database identifier based on cloud provider:
// - AWS: uses DBInstanceIdentifier from RDS metadata
// - Azure: uses full resource ID path (required by Azure Monitor API)
// - GCP/fallback: uses short instance name
const getDbIdentifier = (drilldownQuery: any): string => {
  if (drilldownQuery.meta?.DBInstanceIdentifier) return drilldownQuery.meta.DBInstanceIdentifier;
  if (drilldownQuery.resourse_id?.startsWith('/subscriptions/')) return drilldownQuery.resourse_id;
  return drilldownQuery.name || drilldownQuery.resourse_id;
};

// Multi-cloud field mapping helpers (extracted to module level to reduce nesting)
const getInstanceClass = (item: any): string => {
  if (item.meta?.DBInstanceClass) return item.meta.DBInstanceClass;
  if (item.meta?.settings?.tier) return item.meta.settings.tier;
  if (item.meta?.sku?.name) return item.meta.sku.name;
  return '-';
};

const getAvailabilityZone = (item: any): string => {
  if (item.region) return item.region;
  if (item.meta?.region) return item.meta.region;
  if (item.meta?.AvailabilityZone) return item.meta.AvailabilityZone;
  return '-';
};

const getEngine = (item: any): string => {
  if (item.meta?.Engine) return item.meta.Engine;
  if (item.meta?.databaseVersion) return item.meta.databaseVersion.split('_')[0];
  if (item.meta?.kind) return item.meta.kind;
  if (item.meta?.properties?.version) return 'SQL Server';
  return '-';
};

const getEngineVersion = (item: any): string => {
  if (item.meta?.EngineVersion) return item.meta.EngineVersion;
  if (item.meta?.databaseVersion) {
    const parts = item.meta.databaseVersion.split('_');
    return parts.length > 1 ? parts.slice(1).join('_') : item.meta.databaseVersion;
  }
  if (item.meta?.properties?.version) return item.meta.properties.version;
  if (item.meta?.version) return item.meta.version;
  if (item.meta?.sku?.tier) return item.meta.sku.tier;
  if (item.meta?.properties?.sku?.tier) return item.meta.properties.sku.tier;
  return '-';
};

const getCreateTime = (item: any): string => {
  if (item.meta?.InstanceCreateTime) return item.meta.InstanceCreateTime;
  if (item.meta?.createTime) return item.meta.createTime;
  if (item.meta?.properties?.creationDate) return item.meta.properties.creationDate;
  if (item.meta?.creationDate) return item.meta.creationDate;
  return item.created_at;
};

const getStorage = (item: any): string => {
  if (item.meta?.AllocatedStorage) return item.meta.AllocatedStorage + ' GiB';
  if (item.meta?.settings?.dataDiskSizeGb) return item.meta.settings.dataDiskSizeGb + ' GiB';
  if (item.meta?.properties?.maxSizeBytes) return (item.meta.properties.maxSizeBytes / 1024 ** 3).toFixed(2) + ' GiB';
  if (item.meta?.maxSizeBytes) return (item.meta.maxSizeBytes / 1024 ** 3).toFixed(2) + ' GiB';
  if (item.meta?.properties?.currentServiceObjectiveName) return item.meta.properties.currentServiceObjectiveName;
  if (item.meta?.currentServiceObjectiveName) return item.meta.currentServiceObjectiveName;
  if (item.meta?.properties?.storageAccountType) return item.meta.properties.storageAccountType;
  return '-';
};

const getStorageType = (item: any): string => {
  if (item.meta?.StorageType) return item.meta.StorageType;
  if (item.meta?.settings?.dataDiskType) {
    const diskType = item.meta.settings.dataDiskType;
    return diskType.includes('/') ? diskType.split('/').pop() : diskType;
  }
  if (item.meta?.properties?.storageAccountType) return item.meta.properties.storageAccountType;
  if (item.meta?.sku?.tier) return item.meta.sku.tier;
  if (item.meta?.properties?.sku?.tier) return item.meta.properties.sku.tier;
  if (item.meta?.properties?.requestedServiceObjectiveName) return item.meta.properties.requestedServiceObjectiveName;
  return '-';
};

const AzureRdsDetails = ({ drilldownQuery }: { drilldownQuery: any }) => {
  const azureProps = drilldownQuery.meta?.properties || {};
  return (
    <>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
          columnGap: '15px',
          rowGap: '20px',
          mb: '25px',
          backgroundColor: '#fff',
          padding: '20px',
          borderRadius: '8px',
        }}
      >
        {drilldownQuery.meta?.id && <DataBlock title={'Resource Id'} data={drilldownQuery.meta.id} />}
        {drilldownQuery.meta?.name && <DataBlock title={'Database Name'} data={drilldownQuery.meta.name} />}
        {azureProps.status && <DataBlock title={'Status'} data={azureProps.status} isCopyable={false} />}
        {drilldownQuery.meta?.location && <DataBlock title={'Location'} data={drilldownQuery.meta.location} />}
        {drilldownQuery.meta?.sku?.name && <DataBlock title={'SKU Name'} data={drilldownQuery.meta.sku.name} />}
        {drilldownQuery.meta?.sku?.tier && <DataBlock title={'SKU Tier'} data={drilldownQuery.meta.sku.tier} isCopyable={false} />}
        {drilldownQuery.meta?.sku?.capacity && <DataBlock title={'Capacity'} data={drilldownQuery.meta.sku.capacity.toString()} isCopyable={false} />}
        {drilldownQuery.meta?.kind && <DataBlock title={'Kind'} data={drilldownQuery.meta.kind} isCopyable={false} />}
        {azureProps.collation && <DataBlock title={'Collation'} data={azureProps.collation} />}
        {azureProps.maxSizeBytes && (
          <DataBlock title={'Max Size'} data={`${(azureProps.maxSizeBytes / 1024 ** 3).toFixed(2)} GiB`} isCopyable={false} />
        )}
        {azureProps.currentServiceObjectiveName && <DataBlock title={'Service Objective'} data={azureProps.currentServiceObjectiveName} />}
        {azureProps.readScale && <DataBlock title={'Read Scale'} data={azureProps.readScale} isCopyable={false} />}
        {azureProps.zoneRedundant !== undefined && (
          <DataBlock title={'Zone Redundant'} data={azureProps.zoneRedundant ? 'Yes' : 'No'} isCopyable={false} />
        )}
        {azureProps.defaultSecondaryLocation && <DataBlock title={'Secondary Location'} data={azureProps.defaultSecondaryLocation} />}
        {azureProps.catalogCollation && <DataBlock title={'Catalog Collation'} data={azureProps.catalogCollation} />}
        {azureProps.currentBackupStorageRedundancy && (
          <DataBlock title={'Backup Storage Redundancy'} data={azureProps.currentBackupStorageRedundancy} isCopyable={false} />
        )}
        {azureProps.isLedgerOn !== undefined && <DataBlock title={'Ledger'} data={azureProps.isLedgerOn ? 'On' : 'Off'} isCopyable={false} />}
        {azureProps.isInfraEncryptionEnabled !== undefined && (
          <DataBlock title={'Infrastructure Encryption'} data={azureProps.isInfraEncryptionEnabled ? 'Enabled' : 'Disabled'} isCopyable={false} />
        )}
        {azureProps.minCapacity && <DataBlock title={'Min Capacity'} data={azureProps.minCapacity.toString()} isCopyable={false} />}
        {azureProps.creationDate && (
          <DataBlock title={'Created'}>
            <Datetime value={azureProps.creationDate} />
          </DataBlock>
        )}
        {azureProps.resumedDate && (
          <DataBlock title={'Resumed'}>
            <Datetime value={azureProps.resumedDate} />
          </DataBlock>
        )}
        {azureProps.maintenanceConfigurationId && (
          <DataBlock title={'Maintenance Configuration'} data={azureProps.maintenanceConfigurationId.split('/').pop() || ''} />
        )}
        {drilldownQuery.meta?.managedBy && <DataBlock title={'Managed By'} data={drilldownQuery.meta.managedBy.split('/').pop() || ''} />}
      </Box>
      {drilldownQuery.tags &&
        typeof drilldownQuery.tags === 'object' &&
        !Array.isArray(drilldownQuery.tags) &&
        Object.keys(drilldownQuery.tags).length > 0 && (
          <DetailsTagsRdsTable
            tagData={Object.entries(drilldownQuery.tags).map(([key, values]: [string, any]) => ({
              Key: key,
              Value: Array.isArray(values) ? values.join(', ') : values,
            }))}
          />
        )}
    </>
  );
};

const AmazonRdsDetails = ({ drilldownQuery }: { drilldownQuery: any }) => {
  return (
    <>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
          columnGap: '15px',
          rowGap: '20px',
          mb: '25px',
          backgroundColor: '#fff',
          padding: '20px',
          borderRadius: '8px',
        }}
      >
        {drilldownQuery?.meta?.PubliclyAccessible !== undefined && (
          <DataBlock title={'Publicly accessible'} data={drilldownQuery.meta.PubliclyAccessible ? 'Yes' : 'No'} isCopyable={false} />
        )}
        {drilldownQuery?.meta?.MultiAZ !== undefined && (
          <DataBlock title={'Multi-AZ'} data={drilldownQuery.meta.MultiAZ ? 'Enabled' : 'Disabled'} isCopyable={false} />
        )}
        {drilldownQuery?.meta?.Endpoint?.Address && <DataBlock title={'Endpoint'} data={drilldownQuery.meta.Endpoint.Address} />}
        {drilldownQuery?.meta?.Endpoint?.Port && <DataBlock title={'Port'} data={drilldownQuery.meta.Endpoint.Port.toString()} isCopyable={false} />}
        {drilldownQuery.meta?.MasterUsername && <DataBlock title={'Master Username'} data={drilldownQuery.meta.MasterUsername} />}
        {drilldownQuery.meta?.VpcSecurityGroups && drilldownQuery.meta?.VpcSecurityGroups?.length > 0 && (
          <DataBlock title={'VPC security groups'}>
            {drilldownQuery.meta?.VpcSecurityGroups?.map((e: any) => (
              <Typography key={e?.VpcSecurityGroupId} fontSize={'13px'}>
                {e?.VpcSecurityGroupId + ' ' + e.Status}
              </Typography>
            ))}
          </DataBlock>
        )}
        {drilldownQuery.meta?.DBSubnetGroup?.VpcId && <DataBlock title={'VPC'} data={drilldownQuery.meta.DBSubnetGroup.VpcId} />}
        {drilldownQuery.meta?.DBSubnetGroup?.DBSubnetGroupName && (
          <DataBlock title={'Subnet group'} data={drilldownQuery.meta.DBSubnetGroup.DBSubnetGroupName} />
        )}
        {drilldownQuery.meta?.DBSubnetGroup?.Subnets && drilldownQuery.meta?.DBSubnetGroup?.Subnets?.length > 0 && (
          <DataBlock title={'Subnets'}>
            {drilldownQuery.meta.DBSubnetGroup.Subnets.map((sg: any) => (
              <Typography key={sg.SubnetIdentifier} fontSize={'13px'}>
                <CopyableText copyableText={sg.SubnetIdentifier} iconColor={undefined} onCopy={undefined}>
                  {sg.SubnetIdentifier + ' -> ' + sg.SubnetAvailabilityZone.Name}
                </CopyableText>
              </Typography>
            ))}
          </DataBlock>
        )}
        {drilldownQuery.meta?.CertificateDetails && (
          <DataBlock title={'Certificate authority'} data={drilldownQuery.meta.CertificateDetails.CAIdentifier} />
        )}
        {drilldownQuery.meta?.CertificateDetails && (
          <DataBlock title={'Certificate authority date'}>
            <Datetime value={drilldownQuery.meta.CertificateDetails.ValidTill} />
          </DataBlock>
        )}
        {drilldownQuery.meta?.KmsKeyId && <DataBlock title={'AWS KMS key'} data={drilldownQuery.meta.KmsKeyId} />}
        {drilldownQuery.meta?.AllocatedStorage && (
          <DataBlock title={'Allocated Storage'} isCopyable={false} data={drilldownQuery.meta.AllocatedStorage + ' GiB'} />
        )}
        {drilldownQuery.meta?.OptionGroupMemberships && drilldownQuery.meta?.OptionGroupMemberships?.length > 0 && (
          <DataBlock title={'Option groups'}>
            {drilldownQuery.meta.OptionGroupMemberships.map((f: any) => (
              <Typography key={f.OptionGroupName} fontSize={'13px'}>
                {f.OptionGroupName}
              </Typography>
            ))}
          </DataBlock>
        )}
        {drilldownQuery.meta?.DBParameterGroups && drilldownQuery.meta?.DBParameterGroups?.length > 0 && (
          <DataBlock title={'DB instance parameter group'}>
            {drilldownQuery.meta.DBParameterGroups.map((f: any) => (
              <Typography key={f.DBParameterGroupName} fontSize={'13px'}>
                {f.DBParameterGroupName}
              </Typography>
            ))}
          </DataBlock>
        )}
        {drilldownQuery.meta?.DeletionProtection !== undefined && (
          <DataBlock title={'Deletion protection'} isCopyable={false} data={drilldownQuery.meta.DeletionProtection ? 'Enabled' : 'Disabled'} />
        )}
        {drilldownQuery.meta?.Iops && <DataBlock title={'Provisioned IOPS'} isCopyable={false} data={drilldownQuery.meta.Iops + ' IOPS'} />}
        {drilldownQuery.meta?.StorageThroughput && (
          <DataBlock title={'Storage throughput'} isCopyable={false} data={drilldownQuery.meta.StorageThroughput + ' MiBps'} />
        )}
        {drilldownQuery.meta?.MaxAllocatedStorage && (
          <DataBlock title={'Maximum storage threshold'} isCopyable={false} data={drilldownQuery.meta.MaxAllocatedStorage + ' GiB'} />
        )}
        {drilldownQuery.meta?.StorageEncrypted !== undefined && (
          <DataBlock title={'Storage encryption'} isCopyable={false} data={drilldownQuery.meta?.StorageEncrypted ? 'Enabled' : 'Disabled'} />
        )}
        {drilldownQuery.meta?.PerformanceInsightsEnabled !== undefined && (
          <DataBlock
            title={'Performance Insights enabled'}
            isCopyable={false}
            data={drilldownQuery.meta?.PerformanceInsightsEnabled ? 'Turned On' : 'Turned Off'}
          />
        )}
        {drilldownQuery.meta?.PerformanceInsightsRetentionPeriod && (
          <DataBlock title={'Retention period'} isCopyable={false} data={drilldownQuery.meta.PerformanceInsightsRetentionPeriod + ' days'} />
        )}
      </Box>
      {drilldownQuery.meta?.TagList && drilldownQuery.meta?.TagList.length > 0 && <DetailsTagsRdsTable tagData={drilldownQuery.meta.TagList} />}
      {!drilldownQuery.meta?.TagList?.length &&
        drilldownQuery.tags &&
        typeof drilldownQuery.tags === 'object' &&
        !Array.isArray(drilldownQuery.tags) &&
        Object.keys(drilldownQuery.tags).length > 0 && (
          <DetailsTagsRdsTable
            tagData={Object.entries(drilldownQuery.tags).map(([key, values]: [string, any]) => ({
              Key: key,
              Value: Array.isArray(values) ? values.join(', ') : String(values),
            }))}
          />
        )}
      {drilldownQuery.meta?.AlarmDetails && drilldownQuery.meta?.AlarmDetails?.length > 0 && (
        <AlarmRdsTable alarmDetails={drilldownQuery.meta.AlarmDetails} />
      )}
    </>
  );
};

const GCPAlertPoliciesTable = ({ alertPolicies }: { alertPolicies: any[] }) => {
  const tableId = 'gcpRdsAlertPoliciesTable';
  const headers = [
    { name: 'Policy Name', width: '20%' },
    { name: 'Severity', width: '10%' },
    { name: 'Enabled', width: '8%' },
    { name: 'Condition', width: '25%' },
    { name: 'Threshold', width: '15%' },
    { name: 'Documentation', width: '22%' },
  ];
  const rows = alertPolicies.flatMap((policy: any) =>
    (policy.conditions || []).map((condition: any) => {
      const threshold = condition.conditionThreshold;
      const row: ICustomTable2Row[] = [];
      row.push({ component: <CustomText text1={policy.displayName} /> });
      row.push({ component: <CustomText text1={policy.severity || '-'} /> });
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
      row.push({
        component: <CustomText text1={policy.documentation?.subject || '-'} subtext1={policy.documentation?.content?.slice(0, 80) || undefined} />,
      });
      return row;
    })
  );
  return (
    <ListingLayout id={`${tableId}-card`}>
      <ListingLayout.Toolbar title='Alert Policies' actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />} />
      <ListingLayout.Body>
        <CustomTable2 id={tableId} headers={headers} tableData={rows} rowsPerPage={rows.length} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const GCPDatabaseFlagsTable = ({ flags }: { flags: any[] }) => {
  const tableId = 'gcpRdsDatabaseFlagsTable';
  const rows = flags.map((flag: any) => {
    const row: ICustomTable2Row[] = [];
    row.push({ component: <CustomText text1={flag.name} /> });
    row.push({ component: <CustomText text1={flag.value} /> });
    return row;
  });
  return (
    <ListingLayout id={`${tableId}-card`}>
      <ListingLayout.Toolbar title='Database Flags' actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />} />
      <ListingLayout.Body>
        <CustomTable2 id={tableId} headers={['Flag Name', 'Value']} tableData={rows} rowsPerPage={rows.length} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

const GCPRdsDetails = ({ drilldownQuery }: { drilldownQuery: any }) => {
  const meta = drilldownQuery.meta || {};
  const settings = meta.settings || {};
  const backupConfig = settings.backupConfiguration || {};
  const ipConfig = settings.ipConfiguration || {};
  const insightsConfig = settings.insightsConfig || {};
  const passwordPolicy = settings.passwordValidationPolicy || {};
  const maintenanceWindow = settings.maintenanceWindow || {};
  const backupRetention = backupConfig.backupRetentionSettings || {};

  return (
    <>
      {/* Core Instance Info */}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
          columnGap: '15px',
          rowGap: '20px',
          mb: '25px',
          backgroundColor: '#fff',
          padding: '20px',
          borderRadius: '8px',
        }}
      >
        {meta.connectionName && <DataBlock title={'Connection Name'} data={meta.connectionName} />}
        {meta.project && <DataBlock title={'Project'} data={meta.project} />}
        {meta.region && <DataBlock title={'Region'} data={meta.region} />}
        {meta.gceZone && <DataBlock title={'Zone'} data={meta.gceZone} isCopyable={false} />}
        {meta.databaseVersion && <DataBlock title={'Database Version'} data={meta.databaseVersion} isCopyable={false} />}
        {meta.databaseInstalledVersion && <DataBlock title={'Installed Version'} data={meta.databaseInstalledVersion} isCopyable={false} />}
        {meta.maintenanceVersion && <DataBlock title={'Maintenance Version'} data={meta.maintenanceVersion} isCopyable={false} />}
        {meta.instanceType && <DataBlock title={'Instance Type'} data={meta.instanceType} isCopyable={false} />}
        {meta.backendType && <DataBlock title={'Backend Type'} data={meta.backendType} isCopyable={false} />}
        {settings.tier && <DataBlock title={'Machine Type'} data={settings.tier} />}
        {settings.edition && <DataBlock title={'Edition'} data={settings.edition} isCopyable={false} />}
        {settings.pricingPlan && <DataBlock title={'Pricing Plan'} data={settings.pricingPlan} isCopyable={false} />}
        {settings.activationPolicy && <DataBlock title={'Activation Policy'} data={settings.activationPolicy} isCopyable={false} />}
        {settings.availabilityType && <DataBlock title={'Availability Type'} data={settings.availabilityType} isCopyable={false} />}
        {settings.replicationType && <DataBlock title={'Replication Type'} data={settings.replicationType} isCopyable={false} />}
        {settings.dataDiskSizeGb && <DataBlock title={'Disk Size'} data={`${settings.dataDiskSizeGb} GiB`} isCopyable={false} />}
        {settings.dataDiskType && <DataBlock title={'Disk Type'} data={settings.dataDiskType} isCopyable={false} />}
        {settings.storageAutoResize !== undefined && (
          <DataBlock title={'Storage Auto Resize'} data={settings.storageAutoResize ? 'Enabled' : 'Disabled'} isCopyable={false} />
        )}
        {settings.deletionProtectionEnabled !== undefined && (
          <DataBlock title={'Deletion Protection'} data={settings.deletionProtectionEnabled ? 'Enabled' : 'Disabled'} isCopyable={false} />
        )}
        {settings.retainBackupsOnDelete !== undefined && (
          <DataBlock title={'Retain Backups on Delete'} data={settings.retainBackupsOnDelete ? 'Yes' : 'No'} isCopyable={false} />
        )}
        {settings.connectorEnforcement && <DataBlock title={'Connector Enforcement'} data={settings.connectorEnforcement} isCopyable={false} />}
        {maintenanceWindow.updateTrack && <DataBlock title={'Maintenance Update Track'} data={maintenanceWindow.updateTrack} isCopyable={false} />}
        {meta.availableMaintenanceVersions?.length > 0 && (
          <DataBlock title={'Available Maintenance Version'} data={meta.availableMaintenanceVersions[0]} isCopyable={false} />
        )}
        {meta.replicaNames?.length > 0 && (
          <DataBlock title={'Read Replicas'}>
            {meta.replicaNames.map((r: string) => (
              <Typography key={r} fontSize={'13px'}>
                {r}
              </Typography>
            ))}
          </DataBlock>
        )}
        {meta.serviceAccountEmailAddress && <DataBlock title={'Service Account'} data={meta.serviceAccountEmailAddress} />}
        {meta.satisfiesPzi !== undefined && <DataBlock title={'Satisfies PZI'} data={meta.satisfiesPzi ? 'Yes' : 'No'} isCopyable={false} />}
        {meta.createTime && (
          <DataBlock title={'Created'}>
            <Datetime value={meta.createTime} />
          </DataBlock>
        )}
        {meta.ipAddresses?.length > 0 && (
          <DataBlock title={'IP Addresses'}>
            {meta.ipAddresses.map((ip: any) => (
              <Typography key={ip.ipAddress} fontSize={'13px'}>
                <CopyableText copyableText={ip.ipAddress} iconColor={undefined} onCopy={undefined}>
                  {ip.ipAddress} ({ip.type})
                </CopyableText>
              </Typography>
            ))}
          </DataBlock>
        )}
      </Box>

      {/* Backup Configuration */}
      {(backupConfig.enabled !== undefined || backupConfig.location || backupConfig.startTime) && (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
            columnGap: '15px',
            rowGap: '20px',
            mb: '25px',
            backgroundColor: '#fff',
            padding: '20px',
            borderRadius: '8px',
          }}
        >
          <Typography sx={{ gridColumn: '1 / -1', fontWeight: 600, fontSize: 14, color: '#374151' }}>Backup Configuration</Typography>
          {backupConfig.enabled !== undefined && (
            <DataBlock title={'Automated Backups'} data={backupConfig.enabled ? 'Enabled' : 'Disabled'} isCopyable={false} />
          )}
          {backupConfig.location && <DataBlock title={'Backup Location'} data={backupConfig.location} isCopyable={false} />}
          {backupConfig.startTime && <DataBlock title={'Backup Start Time'} data={backupConfig.startTime} isCopyable={false} />}
          {backupConfig.backupTier && <DataBlock title={'Backup Tier'} data={backupConfig.backupTier} isCopyable={false} />}
          {backupConfig.pointInTimeRecoveryEnabled !== undefined && (
            <DataBlock title={'Point-in-Time Recovery'} data={backupConfig.pointInTimeRecoveryEnabled ? 'Enabled' : 'Disabled'} isCopyable={false} />
          )}
          {backupConfig.transactionLogRetentionDays && (
            <DataBlock title={'Transaction Log Retention'} data={`${backupConfig.transactionLogRetentionDays} days`} isCopyable={false} />
          )}
          {backupRetention.retainedBackups && (
            <DataBlock
              title={'Retained Backups'}
              data={`${backupRetention.retainedBackups} (${backupRetention.retentionUnit || 'COUNT'})`}
              isCopyable={false}
            />
          )}
        </Box>
      )}

      {/* Network & Security */}
      {(ipConfig.sslMode || ipConfig.serverCaMode || ipConfig.ipv4Enabled !== undefined || ipConfig.privateNetwork) && (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
            columnGap: '15px',
            rowGap: '20px',
            mb: '25px',
            backgroundColor: '#fff',
            padding: '20px',
            borderRadius: '8px',
          }}
        >
          <Typography sx={{ gridColumn: '1 / -1', fontWeight: 600, fontSize: 14, color: '#374151' }}>Network & Security</Typography>
          {ipConfig.ipv4Enabled !== undefined && (
            <DataBlock title={'Public IP (IPv4)'} data={ipConfig.ipv4Enabled ? 'Enabled' : 'Disabled'} isCopyable={false} />
          )}
          {ipConfig.sslMode && <DataBlock title={'SSL Mode'} data={ipConfig.sslMode} isCopyable={false} />}
          {ipConfig.serverCaMode && <DataBlock title={'Server CA Mode'} data={ipConfig.serverCaMode} isCopyable={false} />}
          {ipConfig.privateNetwork && (
            <DataBlock title={'Private Network'} data={ipConfig.privateNetwork.split('/').pop() || ipConfig.privateNetwork} />
          )}
        </Box>
      )}

      {/* Query Insights */}
      {insightsConfig.queryInsightsEnabled !== undefined && (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
            columnGap: '15px',
            rowGap: '20px',
            mb: '25px',
            backgroundColor: '#fff',
            padding: '20px',
            borderRadius: '8px',
          }}
        >
          <Typography sx={{ gridColumn: '1 / -1', fontWeight: 600, fontSize: 14, color: '#374151' }}>Query Insights</Typography>
          <DataBlock title={'Query Insights'} data={insightsConfig.queryInsightsEnabled ? 'Enabled' : 'Disabled'} isCopyable={false} />
          {insightsConfig.queryStringLength && (
            <DataBlock title={'Query String Length'} data={`${insightsConfig.queryStringLength} chars`} isCopyable={false} />
          )}
          {insightsConfig.queryPlansPerMinute && (
            <DataBlock title={'Query Plans / Min'} data={String(insightsConfig.queryPlansPerMinute)} isCopyable={false} />
          )}
          {insightsConfig.recordClientAddress !== undefined && (
            <DataBlock title={'Record Client Address'} data={insightsConfig.recordClientAddress ? 'Yes' : 'No'} isCopyable={false} />
          )}
          {insightsConfig.recordApplicationTags !== undefined && (
            <DataBlock title={'Record Application Tags'} data={insightsConfig.recordApplicationTags ? 'Yes' : 'No'} isCopyable={false} />
          )}
        </Box>
      )}

      {/* Password Policy */}
      {passwordPolicy.enablePasswordPolicy !== undefined && (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
            columnGap: '15px',
            rowGap: '20px',
            mb: '25px',
            backgroundColor: '#fff',
            padding: '20px',
            borderRadius: '8px',
          }}
        >
          <Typography sx={{ gridColumn: '1 / -1', fontWeight: 600, fontSize: 14, color: '#374151' }}>Password Policy</Typography>
          <DataBlock title={'Password Policy'} data={passwordPolicy.enablePasswordPolicy ? 'Enabled' : 'Disabled'} isCopyable={false} />
          {passwordPolicy.minLength && <DataBlock title={'Min Length'} data={String(passwordPolicy.minLength)} isCopyable={false} />}
          {passwordPolicy.complexity && <DataBlock title={'Complexity'} data={passwordPolicy.complexity} isCopyable={false} />}
          {passwordPolicy.disallowUsernameSubstring !== undefined && (
            <DataBlock title={'Disallow Username Substring'} data={passwordPolicy.disallowUsernameSubstring ? 'Yes' : 'No'} isCopyable={false} />
          )}
        </Box>
      )}

      {/* Database Flags */}
      {settings.databaseFlags?.length > 0 && <GCPDatabaseFlagsTable flags={settings.databaseFlags} />}

      {/* Alert Policies */}
      {meta.AlertPolicies?.length > 0 && <GCPAlertPoliciesTable alertPolicies={meta.AlertPolicies} />}
    </>
  );
};

const PerformanceInsightsTab = ({ accountId, drilldownQuery }: { accountId: string; drilldownQuery: any }) => {
  const dbIdentifier = getDbIdentifier(drilldownQuery);
  const region = drilldownQuery.region || drilldownQuery.meta?.AvailabilityZone?.slice(0, -1) || 'us-east-1';
  return <PerformanceInsights accountId={accountId} databaseIdentifier={dbIdentifier} region={region} />;
};

const CloudLogsTab = ({ accountId, drilldownQuery, serviceName }: { accountId: string; drilldownQuery: any; serviceName: string }) => {
  const dbIdentifier = getDbIdentifier(drilldownQuery);
  const region = drilldownQuery.region || drilldownQuery.meta?.AvailabilityZone?.slice(0, -1) || 'us-east-1';
  return <CloudLogs accountId={accountId} resourceId={dbIdentifier} region={region} serviceName={drilldownQuery.service_name || serviceName} />;
};

const DetailsTagsRdsTable = ({ tagData }: any) => {
  const tagsTableId = 'rdsDetailTagsTable';
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
};

const formatStateReasonData = (stateReasonData: string): JSX.Element => {
  try {
    if (!stateReasonData) return <CustomText text1={'-'} />;
    const data = JSON.parse(stateReasonData);
    const threshold = data.threshold || '-';
    const recentValue = data.recentDatapoints?.[0]?.toFixed(2) || '-';
    const unit = data.unit || '';
    const period = data.period || '-';
    const queryDate = data.queryDate ? new Date(data.queryDate).toLocaleDateString() : '-';

    return (
      <Box>
        <CustomText text1={`${recentValue}${unit}`} subtext1={`Threshold: ${threshold}${unit}`} />
        <Typography sx={{ color: '#9F9F9F', fontSize: 11, marginTop: '2px' }}>
          Period: {period}s | Query Date: {queryDate}
        </Typography>
      </Box>
    );
  } catch (error) {
    console.error(error);
    return <CustomText text1={'Invalid data'} />;
  }
};

const AlarmRdsTable = ({ alarmDetails }: any) => {
  const alarmTableId = 'rdsAlarmDetailsTable';
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
        <CustomTable2 id={alarmTableId} headers={RDS_HEADER} tableData={convertedJson2} rowsPerPage={convertedJson2.length} />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export const CustomText = (data: { text1: string | null; text2?: string | null; subtext1?: string | null; subtext2?: string | null }) => {
  return (
    <>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'row',
        }}
      >
        {data.text1 && <Typography sx={{ color: '#374151', fontWeight: 400, fontSize: 13, marginRight: '2px' }}>{data.text1}</Typography>}
        {data.text2 && <Typography sx={{ color: '#9F9F9F', fontSize: 13 }}>{data.text2}</Typography>}
      </Box>
      {(data.subtext1 || data.subtext2) && (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'row',
          }}
        >
          <Typography sx={{ color: '#9F9F9F', fontSize: 13, marginRight: '4px' }}>{data.subtext1}</Typography>
          {data.subtext2 && (
            <>
              <span
                style={{
                  width: '1px',
                  height: '13px',
                  marginTop: '3px',
                  backgroundColor: '#737373',
                }}
              />
              <Typography sx={{ color: '#9F9F9F', fontSize: 13, marginLeft: '4px' }}>{data.subtext2}</Typography>
            </>
          )}
        </Box>
      )}
    </>
  );
};
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
    event?: any;
  };
  text?: any;
  data?: any;
}

const createPerformanceInsightsComponentFn = (accountId: string) => (_opt: any, drilldownQuery: any, _row: any) =>
  <PerformanceInsightsTab accountId={accountId} drilldownQuery={drilldownQuery} />;

const createCloudLogsComponentFn = (accountId: string, serviceName: string) => (_opt: any, drilldownQuery: any, _row: any) =>
  <CloudLogsTab accountId={accountId} drilldownQuery={drilldownQuery} serviceName={serviceName} />;

const InstancesView = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  serviceName: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [rdsInstances, setRdsInstances] = useState([]);
  const [rdsInstancesCount, setRdsInstancesCount] = useState(0);
  // Typing state + applied state per ManualInvestigated.jsx — fetch fires only
  // on Enter or Clear, not on every keystroke.
  const [selectedInstanceName, setSelectedInstanceName] = useState('');
  const [appliedInstanceName, setAppliedInstanceName] = useState('');
  const [loading, setLoading] = useState(false);
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const [selectedRegion, setSelectedRegion] = useState<string | null>(null);
  const [availableRegions, setAvailableRegions] = useState<{ label: string; value: string }[]>([]);
  const stateOptions = getStateDropdownOptions(props?.serviceName);

  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const rdsOptimizeInstancesTable = 'rdsOptimizeInstancesTable';

  const onSearchEnter = () => {
    setAppliedInstanceName(selectedInstanceName);
    setPage(0);
  };

  const onSearchClear = () => {
    setSelectedInstanceName('');
    setAppliedInstanceName('');
    setPage(0);
  };

  const rdsActions = getActionsForService(props.serviceName);
  const writeAccess = hasWriteAccess(props?.accountId);

  const actionHook = useCloudResourceAction({
    accountId: props.accountId,
    serviceName: props.serviceName,
    onRefresh: () => listRDSInstances(),
  });

  const onMenuClick = (menuItem: { id: string }, data: any) => {
    const selectedAction = rdsActions.find((a) => a.id === menuItem.id);
    if (selectedAction) {
      actionHook.initiateAction(selectedAction, data);
    }
  };

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, props?.serviceName).then(setAvailableTagKeys);
      apiCloudAccount.getDistinctRegions(props.accountId, props?.serviceName).then(setAvailableRegions);
    }
  }, [props?.accountId, props?.serviceName]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, props?.serviceName).then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  useEffect(() => {
    listRDSInstances();
  }, [props?.accountId, props?.serviceName, page, rowsPerPage, selectedTagKey, selectedTagValue, selectedState, selectedRegion, appliedInstanceName]);

  const listRDSInstances = () => {
    if (!props?.accountId) {
      return;
    }

    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: props?.serviceName,
          nameFilter: appliedInstanceName,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          region: selectedRegion || undefined,
          ...buildStateApiParams(props?.serviceName, selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const rdsResourceData = res.data?.data?.cloud_resourses?.map((item: any) => {
          const data: ICustomTable2Row[] = [];

          data.push({
            component: <CustomText text1={item.name || item.resourse_id} subtext1={getAvailabilityZone(item)} subtext2={getInstanceClass(item)} />,
            drilldownQuery: item,
          });
          const instanceState = getInstanceState(props?.serviceName, item?.meta) || item?.status;
          data.push({
            component: <CustomLabels text={instanceState} variant={getStateColor(instanceState)} />,
          });
          data.push({
            component: <CustomText text1={getEngine(item)} subtext1={getEngineVersion(item)} />,
          });
          data.push({
            component: <Datetime value={getCreateTime(item)} />,
          });
          data.push({
            component: <CustomText text1={getStorage(item)} />,
          });
          data.push({ component: <TagsCell tags={item.tags} /> });
          data.push({
            component: <CustomText text1={getStorageType(item)} />,
          });
          // Use the native provider state (DBInstanceStatus / Cloud SQL state /
          // Azure SQL properties.status) so action predicates match the values
          // the user actually sees in the State column. item.status is the DB
          // column ("Active"/"Inactive") which doesn't carry start/stop info.
          const menuItems = buildMenuItems(rdsActions, instanceState || '-', writeAccess);
          data.push({
            component:
              menuItems && menuItems.length > 0 ? (
                <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'4px'}>
                  <DsDropdownMenu
                    align='end'
                    size='sm'
                    items={menuItems.map((m: any) => ({
                      id: `rds-action-${item.resourse_id}-${m.id}`,
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
        setRdsInstances(rdsResourceData);

        const rdsResourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        setRdsInstancesCount(rdsResourceCount);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  return (
    <ListingLayout id='right-sizing'>
      <ListingLayout.Toolbar
        title={props.heading || undefined}
        actions={<DownloadButton id={`${rdsOptimizeInstancesTable}-download`} onClick={() => ({ tableId: rdsOptimizeInstancesTable })} />}
      >
        <CustomSearch
          id='rds-instances-search'
          label='Search By Instance Name'
          value={selectedInstanceName}
          onChange={(next) => {
            if (selectedInstanceName !== '' && next === '') {
              setAppliedInstanceName('');
              setPage(0);
            }
            setSelectedInstanceName(next);
          }}
          onEnterPress={onSearchEnter}
          onClear={onSearchClear}
        />
        <FilterDropdown
          id='rds-filter-state'
          label='State'
          value={selectedState}
          options={stateOptions}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedState(e.target.value || 'all');
          }}
        />
        <FilterDropdown
          id='rds-filter-region'
          label='Region'
          value={selectedRegion}
          options={availableRegions}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedRegion(e.target.value || null);
          }}
        />
        <FilterDropdown
          id='rds-filter-tag-key'
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
          id='rds-filter-tag-value'
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
          id={rdsOptimizeInstancesTable}
          headers={INSTANCE_HEADER}
          data={rdsInstances}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={rdsInstancesCount}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'rds-details',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  const isAzure = drilldownQuery.service_name?.toLowerCase().includes('microsoft.sql');
                  const isAmazon = drilldownQuery.service_name?.toLowerCase().includes('amazon');
                  const isGcp = drilldownQuery.service_name?.toLowerCase().includes('cloud sql');

                  if (isAzure) {
                    return <AzureRdsDetails drilldownQuery={drilldownQuery} />;
                  } else if (isAmazon) {
                    return <AmazonRdsDetails drilldownQuery={drilldownQuery} />;
                  } else if (isGcp) {
                    return <GCPRdsDetails drilldownQuery={drilldownQuery} />;
                  } else {
                    return <>No data available!</>;
                  }
                },
              },
              {
                text: 'Monitoring',
                value: 1,
                key: 'rds-monitoring',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  return <OptimizeSummary accountId={props?.accountId} resourceId={drilldownQuery.resourse_id} serviceName={props.serviceName} />;
                },
              },
              {
                text: 'Events',
                value: 2,
                key: 'rds-events',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  return <CloudAccountEvents accountId={props?.accountId} serviceName={props.serviceName} subjectName={drilldownQuery.resourse_id} />;
                },
              },
              {
                text: 'Performance Insights',
                value: 3,
                key: 'rds-performance-insights',
                componentFn: createPerformanceInsightsComponentFn(props?.accountId || ''),
              },
              {
                text: 'Logs',
                value: 4,
                key: 'rds-logs',
                componentFn: createCloudLogsComponentFn(props?.accountId || '', props.serviceName),
              },
              {
                text: 'Action History',
                value: 5,
                key: 'rds-action-history',
                componentFn: function (_opt: any, drilldownQuery: any, _row: any) {
                  return <ResourceActionHistory accountId={props?.accountId} resourceId={drilldownQuery.resourse_id} />;
                },
              },
            ],
          }}
          tableHeadingCenter={props.tableHeadingCenter}
          stickyColumnIndex={props.stickyColumnIndex}
        />
      </ListingLayout.Body>
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
    </ListingLayout>
  );
};
export default InstancesView;
