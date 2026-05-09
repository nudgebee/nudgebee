import { Box, Chip, List, ListItem, Stack, Tab, Tabs, Typography } from '@mui/material';
import React, { useEffect, useState, type JSX } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { hasWriteAccess } from '@lib/auth';
import { action } from 'src/utils/actionStyles';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { DeleteIconRed as NewDelete } from '@assets';
import ECSSummary from './Summary'; // Corrected import
import Datetime from '@components1/common/format/Datetime';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { snackbar } from '@components1/common/snackbarService';
import TagsCell from '@components1/cloudaccount/TagsCell';
import { buildStateApiParams, getStateDropdownOptions } from '@components1/cloudaccount/stateFilter';
import { colors } from '@utils/colors';
import { getActionsForService, buildMenuItems } from '@components1/cloudaccount/resourceActions';
import { useCloudResourceAction } from '@hooks/useCloudResourceAction';
import ConfirmActionDialog from '@components1/cloudaccount/ConfirmActionDialog';
import ResourceActionHistory from '@components1/cloudaccount/ResourceActionHistory';

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
    clusterName?: any;
    taskDefinitionArn?: any;
    serviceName?: any;
    cpu?: string;
    memory?: string;
    resourceId?: any; // This is likely the primary identifier for the resource in the backend
    desiredCount?: number;
    runningCount?: number;
    pendingCount?: number;
    event?: any;
    // Adding fields from the 'item' structure used in mapping
    resourse_id?: string; // Ensure this matches the field name from API
    region?: string;
    meta?: any; // To access nested meta properties like clusterArn, taskDefinitionArn
  };
  text?: any;
  data?: any;
}

const TASK_HEADER = ['Task ID', 'Status', 'Launch Type', 'CPU / Mem', 'Tags', 'Started At', ''];

export const ECSTasks = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  serviceName: string; // Should be 'ecs'
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [ecsInstances, setEcsInstances] = useState([]);
  const [ecsInstancesCount, setEcsInstancesCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(10);
  const [searchFilter, setSearchFilter] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const stateOptions = getStateDropdownOptions(props?.serviceName);

  const ecsInstancesTableId = 'ecsInstancesTable';

  const changePage = (page: number, limit: number) => {
    if (limit && limit !== rowsPerPage) {
      setRowsPerPage(limit);
      setPage(0);
    } else {
      setPage(page - 1);
    }
  };

  const onSearchFilterChange = (e: any) => {
    setSearchFilter(e.target.value);
  };

  const onEnterPress = () => {
    if (page !== 0) {
      setPage(0);
    } else {
      listEcsTasks();
    }
  };

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, 'AmazonECS', 'task').then(setAvailableTagKeys);
    }
  }, [props?.accountId]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, 'AmazonECS', 'task').then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  const listEcsTasks = () => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: 'AmazonECS',
          type: 'task',
          nameFilter: searchFilter,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          ...buildStateApiParams(props?.serviceName, selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const cloudResourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        const cloudResourceData = (res.data?.data?.cloud_resourses || []).map((item: any) => {
          const data: ICustomTable2Row[] = [];
          let MENU_ITEMS;
          if (hasWriteAccess(props?.accountId)) {
            MENU_ITEMS = [
              {
                icon: NewDelete,
                label: 'Delete',
                id: 0,
                disabled: true, // Keep disabled as per original S3 example, actual actions TBD
              },
            ];
          }

          // Mapping ECS properties to table cells
          // Column 1: Task ID
          data.push({
            component: (
              <CustomText
                text1={item.name || item.resourse_id} // Task ID
                subtext1={item.tags?.['aws:ecs:serviceName']?.[0]}
                subtext2={item.tags?.['aws:ecs:clusterName']?.[0]}
              />
            ),
            drilldownQuery: item, // Pass the whole item for drilldown
          });
          data.push({
            // Status
            // Column 3: Status
            component: (
              <Box>
                <CustomLabels text={item.meta?.LastStatus || item.status} />
                {item.meta?.StoppedReason && item.meta?.LastStatus === 'STOPPED' && (
                  <Typography sx={{ fontSize: '10px', color: 'text.secondary', maxWidth: 150 }} noWrap title={item.meta.StoppedReason}>
                    Reason: {item.meta.StoppedReason}
                  </Typography>
                )}
              </Box>
            ),
          });
          data.push({
            // Column 4: Launch Type
            component: <CustomText text1={item.meta?.LaunchType} subtext1={item.meta?.CapacityProviderName} />,
          });
          data.push({
            // Column 5: CPU / Mem
            component: (
              <Stack direction='row' spacing={0.5}>
                <Chip
                  label={`CPU: ${item.meta?.Cpu || 'N/A'}`}
                  size='small'
                  sx={{ fontSize: 11, bgcolor: colors.background.blueChip, color: colors.text.blueChip, fontWeight: 500, border: 'none' }}
                  variant='outlined'
                />
                <Chip
                  label={item.meta?.Memory ? `Mem: ${item.meta.Memory} MiB` : 'Mem: N/A'}
                  size='small'
                  sx={{ fontSize: 11, bgcolor: colors.background.greenChip, color: colors.text.greenChip, fontWeight: 500, border: 'none' }}
                  variant='outlined'
                />
              </Stack>
            ),
          });
          data.push({ component: <TagsCell tags={item.tags} /> });
          data.push({
            // Column 6: Started At
            component: <Datetime value={item.meta?.StartedAt || item.meta?.CreatedAt} />,
          });
          data.push({
            // Actions menu
            component: (
              <Box display={'flex'} justifyContent={'flex-end'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={undefined} />
              </Box>
            ),
          });

          return data;
        });
        setEcsInstances(cloudResourceData);
        setEcsInstancesCount(cloudResourceCount);
      })
      .catch((error) => {
        setLoading(false);
        snackbar.error(`Error fetching ECS instances: ${error.message}`);
        console.error('Error fetching ECS instances:', error);
      });
  };

  useEffect(() => {
    listEcsTasks();
  }, [props?.accountId, page, rowsPerPage, selectedTagKey, selectedTagValue, selectedState]);

  return (
    <BoxLayout2
      heading={props.heading ?? 'ECS Tasks'}
      id='ecs-instances-list'
      filterOptions={[
        {
          type: 'search',
          enabled: true,
          onSelect: onSearchFilterChange,
          minWidth: '200px',
          label: 'Search by Name/ARN Snippet',
          onEnter: onEnterPress,
          value: searchFilter,
        },
        {
          type: 'dropdown',
          label: 'State',
          value: selectedState,
          options: stateOptions,
          onSelect: (e: any) => {
            setSelectedState(e.target.value || 'all');
          },
        },
        {
          type: 'dropdown',
          label: 'Tag Key',
          value: selectedTagKey,
          options: availableTagKeys,
          onSelect: (e: any) => {
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
          },
        },
        {
          type: 'dropdown',
          enabled: !!selectedTagKey,
          label: 'Tag Value',
          value: selectedTagValue,
          options: availableTagValues,
          onSelect: (e: any) => {
            setSelectedTagValue(e.target.value || null);
          },
        },
      ]}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: ecsInstancesTableId,
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
    >
      <CloudAccountTable
        id={ecsInstancesTableId}
        headers={TASK_HEADER}
        data={ecsInstances}
        rowsPerPage={rowsPerPage}
        onPageChange={changePage}
        totalRows={ecsInstancesCount}
        loading={loading}
        showExpandable={true}
        pageNumber={page + 1}
        expandable={{
          tabs: [
            {
              text: 'Monitoring',
              value: 1, // Ensure unique value
              key: 'ecs-monitoring',
              componentFn: function (opt: any, drilldownQuery: any) {
                return (
                  <ECSSummary accountId={props?.accountId} resourceId={drilldownQuery.meta.TaskArn} serviceName={'AmazonECS'} resourceType='task' />
                );
              },
            },
            { text: 'Task Info', value: 2, key: 'ecs-task-info', componentFn: (opt: any, dq: any) => <ECSTaskInfo taskDetails={dq} /> },
            {
              text: 'Containers',
              value: 3,
              key: 'ecs-task-containers',
              componentFn: (opt: any, dq: any) => <ECSTaskContainers taskDetails={dq} />,
            },
            { text: 'Network', value: 4, key: 'ecs-task-network', componentFn: (opt: any, dq: any) => <ECSTaskNetworkInfo taskDetails={dq} /> },
            { text: 'Tags', value: 5, key: 'ecs-task-tags', componentFn: (opt: any, dq: any) => <ECSTaskTagsDisplay taskDetails={dq} /> },
          ],
        }}
        stickyColumnIndex={props.stickyColumnIndex}
        tableHeadingCenter={props.tableHeadingCenter}
      />
    </BoxLayout2>
  );
};

const SERVICE_HEADER = ['Service Name', 'Capacity Provider', 'Replicas', 'Status', 'Tags', 'CreatedAt', ''];
export const ECSServices = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  serviceName: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [ecsInstances, setEcsInstances] = useState([]);
  const [ecsInstancesCount, setEcsInstancesCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [searchFilter, setSearchFilter] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const stateOptions = getStateDropdownOptions(props?.serviceName);

  const rowsPerPage = 10;
  const ecsInstancesTableId = 'ecsInstancesTable';

  const changePage = (page: number) => {
    setPage(page - 1);
  };

  const onSearchFilterChange = (e: any) => {
    setSearchFilter(e.target.value);
  };

  const onEnterPress = () => {
    if (page !== 0) {
      setPage(0);
    } else {
      listEcsService();
    }
  };

  const listEcsService = () => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: 'AmazonECS',
          type: 'service',
          nameFilter: searchFilter,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          ...buildStateApiParams(props?.serviceName, selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const cloudResourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        const cloudResourceData = (res.data?.data?.cloud_resourses || []).map((item: any) => {
          const data: ICustomTable2Row[] = [];
          const serviceState = item?.status || item?.meta?.Status || '-';
          const menuItems = writeAccess ? buildMenuItems(ecsServiceActions, serviceState, writeAccess) : undefined;

          data.push({
            component: <CustomText text1={item.name} subtext1={item.meta?.ClusterName} subtext2={item.region} />,
            drilldownQuery: item,
          });
          data.push({
            component: <CustomText text1={item.meta?.LaunchType} />,
          });
          data.push({
            component: (
              <Typography>
                {item.meta?.Deployments?.[0]?.RunningCount}/{item.meta?.Deployments?.[0]?.DesiredCount}
              </Typography>
            ),
          });
          data.push({
            component: <CustomLabels text={item.status} />,
          });
          data.push({ component: <TagsCell tags={item.tags} /> });
          data.push({
            component: <Datetime value={item.created_at} />,
          });
          data.push({
            component: (
              <Box display={'flex'} justifyContent={'flex-end'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={menuItems} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });

          return data;
        });
        setEcsInstances(cloudResourceData);
        setEcsInstancesCount(cloudResourceCount);
      })
      .catch((error) => {
        setLoading(false);
        snackbar.error(`Error fetching ECS instances: ${error.message}`);
        console.error('Error fetching ECS instances:', error);
      });
  };

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, 'AmazonECS', 'service').then(setAvailableTagKeys);
    }
  }, [props?.accountId]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, 'AmazonECS', 'service').then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  useEffect(() => {
    listEcsService();
  }, [props?.accountId, page, selectedTagKey, selectedTagValue, selectedState]);

  const ecsServiceActions = getActionsForService('AmazonECS', 'service');
  const writeAccess = hasWriteAccess(props?.accountId);

  const actionHook = useCloudResourceAction({
    accountId: props.accountId,
    serviceName: 'AmazonECS',
    onRefresh: () => listEcsService(),
  });

  const onMenuClick = (menuItem: { id: string }, data: any) => {
    const selectedAction = ecsServiceActions.find((a) => a.id === menuItem.id);
    if (selectedAction) {
      actionHook.initiateAction(selectedAction, data);
    }
  };

  return (
    <BoxLayout2
      heading={props.heading ?? 'ECS Tasks'}
      id='ecs-instances-list'
      filterOptions={[
        {
          type: 'search',
          enabled: true,
          onSelect: onSearchFilterChange,
          minWidth: '200px',
          label: 'Search by Name/ARN Snippet',
          onEnter: onEnterPress,
          value: searchFilter,
        },
        {
          type: 'dropdown',
          label: 'State',
          value: selectedState,
          options: stateOptions,
          onSelect: (e: any) => {
            setSelectedState(e.target.value || 'all');
          },
        },
        {
          type: 'dropdown',
          label: 'Tag Key',
          value: selectedTagKey,
          options: availableTagKeys,
          onSelect: (e: any) => {
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
          },
        },
        {
          type: 'dropdown',
          enabled: !!selectedTagKey,
          label: 'Tag Value',
          value: selectedTagValue,
          options: availableTagValues,
          onSelect: (e: any) => {
            setSelectedTagValue(e.target.value || null);
          },
        },
      ]}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: ecsInstancesTableId,
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
    >
      <CloudAccountTable
        id={ecsInstancesTableId}
        headers={SERVICE_HEADER}
        data={ecsInstances}
        rowsPerPage={rowsPerPage}
        onPageChange={changePage}
        totalRows={ecsInstancesCount}
        loading={loading}
        showExpandable={true}
        pageNumber={page + 1}
        expandable={{
          tabs: [
            {
              text: 'Monitoring',
              value: 1,
              key: 'ecs-monitoring',
              componentFn: function (opt: any, drilldownQuery: any) {
                return (
                  <ECSSummary
                    accountId={props?.accountId}
                    resourceId={drilldownQuery.meta.ServiceArn}
                    serviceName={'AmazonECS'}
                    resourceType='service'
                  />
                );
              },
            },
            {
              text: 'Network Configuration',
              value: 2, // Unique value
              key: 'ecs-network-configuration',
              componentFn: function (opt: any, drilldownQuery: any) {
                return <ECSNetworkDetails ecsDetails={drilldownQuery} />;
              },
            },
            {
              text: 'Deployment Configuration',
              value: 3, // Unique value
              key: 'ecs-deployment-configuration',
              componentFn: function (opt: any, drilldownQuery: any) {
                return <ECSDeploymentConfiguration ecsDetails={drilldownQuery} />;
              },
            },
            {
              text: 'Service Events', // Clarified name
              value: 4, // Unique value
              key: 'ecs-events',
              componentFn: function (opt: any, drilldownQuery: any) {
                return <ECSServiceEvent ecsDetails={drilldownQuery} />;
              },
            },
            {
              text: 'Deployments',
              value: 5,
              key: 'ecs-deployments',
              componentFn: function (opt: any, drilldownQuery: any) {
                return <ECSDeploymentsDetails ecsDetails={drilldownQuery} />;
              },
            },
            {
              text: 'Action History',
              value: 6,
              key: 'ecs-action-history',
              componentFn: function (_opt: any, drilldownQuery: any) {
                return <ResourceActionHistory accountId={props?.accountId} resourceId={drilldownQuery.resourse_id} />;
              },
            },
          ],
        }}
        stickyColumnIndex={props.stickyColumnIndex}
        tableHeadingCenter={props.tableHeadingCenter}
      />
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
    </BoxLayout2>
  );
};

const SERVICE_EVENT_TABLE_ID = 'ECS_SERVICE_EVENT_TABLE_ID';
const SERVICE_EVENT_HEADERS = ['Event Message', 'Timestamp'];

interface ECSEvent {
  Id: string;
  Message: string;
  CreatedAt: string;
}

interface ECSServiceEventProps {
  ecsDetails: {
    meta?: any;
  };
}
const ECSServiceEvent = ({ ecsDetails }: ECSServiceEventProps) => {
  const events = ecsDetails?.meta?.Events || [];

  if (events.length === 0) {
    return <Typography sx={{ p: 2 }}>No events available for this service.</Typography>;
  }

  const tableData: ICustomTable2Row[][] = events.map((event: ECSEvent) => {
    const row: ICustomTable2Row[] = [];
    row.push({
      component: <Typography variant='body2'>{event.Message}</Typography>,
    });
    row.push({
      component: <Datetime value={event.CreatedAt} />,
      data: event.CreatedAt, // For potential sorting if enabled
    });
    return row;
  });

  return (
    <CustomTable2
      id={SERVICE_EVENT_TABLE_ID}
      headers={SERVICE_EVENT_HEADERS}
      tableData={tableData}
      totalRows={tableData.length}
      rowsPerPage={Math.max(10, tableData.length)}
      onPageChange={() => false}
      loading={false}
    />
  );
};

interface AwsvpcConfiguration {
  Subnets: string[];
  AssignPublicIp: string; // "ENABLED" or "DISABLED"
  SecurityGroups: string[];
}

interface NetworkConfiguration {
  AwsvpcConfiguration?: AwsvpcConfiguration;
}

interface ECSNetworkDetailsProps {
  ecsDetails: {
    meta?: {
      NetworkConfiguration?: NetworkConfiguration;
    };
  };
}

const ECSNetworkDetails = ({ ecsDetails }: ECSNetworkDetailsProps) => {
  const networkConfig = ecsDetails?.meta?.NetworkConfiguration?.AwsvpcConfiguration;

  if (!networkConfig) {
    return <Typography sx={{ p: 2 }}>No network configuration details available for this service.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={2}>
        <Box>
          <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
            VPC Subnets
          </Typography>
          {networkConfig.Subnets && networkConfig.Subnets.length > 0 ? (
            <List dense>
              {networkConfig.Subnets.map((subnet) => (
                <ListItem key={subnet} sx={{ py: 0.5 }}>
                  <Chip label={subnet} size='small' />
                </ListItem>
              ))}
            </List>
          ) : (
            <Typography variant='body2'>No subnets configured.</Typography>
          )}
        </Box>
        <Box>
          <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
            Security Groups
          </Typography>
          {networkConfig.SecurityGroups && networkConfig.SecurityGroups.length > 0 ? (
            <List dense>
              {networkConfig.SecurityGroups.map((sg) => (
                <ListItem key={sg} sx={{ py: 0.5 }}>
                  <Chip label={sg} size='small' />
                </ListItem>
              ))}
            </List>
          ) : (
            <Typography variant='body2'>No security groups configured.</Typography>
          )}
        </Box>
        <Box>
          <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
            Public IP
          </Typography>
          <Typography variant='body2'>{networkConfig.AssignPublicIp === 'ENABLED' ? 'Enabled' : 'Disabled'}</Typography>
        </Box>
      </Stack>
    </Box>
  );
};

interface DeploymentAlarms {
  Enable: boolean;
  Rollback: boolean;
  AlarmNames: string[];
}

interface DeploymentCircuitBreaker {
  Enable: boolean;
  Rollback: boolean;
}

interface DeploymentConfiguration {
  MaximumPercent?: number;
  MinimumHealthyPercent?: number;
  DeploymentCircuitBreaker?: DeploymentCircuitBreaker;
  Alarms?: DeploymentAlarms;
}

interface ECSDeploymentConfigurationProps {
  ecsDetails: {
    meta?: {
      DeploymentConfiguration?: DeploymentConfiguration;
    };
  };
}

const ECSDeploymentConfiguration = ({ ecsDetails }: ECSDeploymentConfigurationProps) => {
  const deployConfig = ecsDetails?.meta?.DeploymentConfiguration;

  if (!deployConfig) {
    return <Typography sx={{ p: 2 }}>No deployment configuration details available for this service.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={3}>
        <Box>
          <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
            Deployment Strategy
          </Typography>
          <Typography variant='body2'>Maximum Healthy Percent: {deployConfig.MaximumPercent ?? 'N/A'}%</Typography>
          <Typography variant='body2'>Minimum Healthy Percent: {deployConfig.MinimumHealthyPercent ?? 'N/A'}%</Typography>
        </Box>

        {deployConfig.DeploymentCircuitBreaker && (
          <Box>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Deployment Circuit Breaker
            </Typography>
            <Typography variant='body2'>Enabled: {deployConfig.DeploymentCircuitBreaker.Enable ? 'Yes' : 'No'}</Typography>
            <Typography variant='body2'>Rollback on Failure: {deployConfig.DeploymentCircuitBreaker.Rollback ? 'Yes' : 'No'}</Typography>
          </Box>
        )}

        {deployConfig.Alarms && (
          <Box>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Alarms Configuration
            </Typography>
            <Typography variant='body2'>Enabled: {deployConfig.Alarms.Enable ? 'Yes' : 'No'}</Typography>
            <Typography variant='body2'>Rollback on Alarm: {deployConfig.Alarms.Rollback ? 'Yes' : 'No'}</Typography>
            {deployConfig.Alarms.AlarmNames && deployConfig.Alarms.AlarmNames.length > 0 ? (
              <>
                <Typography variant='body2' sx={{ mt: 1, fontWeight: 500 }}>
                  Alarm Names:
                </Typography>
                <List dense sx={{ pl: 2 }}>
                  {deployConfig.Alarms.AlarmNames.map((alarmName) => (
                    <ListItem key={alarmName} sx={{ py: 0.2, px: 0 }}>
                      <Chip label={alarmName} size='small' />
                    </ListItem>
                  ))}
                </List>
              </>
            ) : (
              <Typography variant='body2' sx={{ mt: 1, fontStyle: 'italic' }}>
                No alarms configured.
              </Typography>
            )}
          </Box>
        )}
      </Stack>
    </Box>
  );
};

interface ECSDeploymentItem {
  Id: string;
  Status: string; // e.g., PRIMARY, ACTIVE, INACTIVE
  TaskDefinition: string;
  DesiredCount: number;
  RunningCount: number;
  PendingCount: number;
  RolloutState: string; // e.g., COMPLETED, IN_PROGRESS, FAILED
  RolloutStateReason?: string;
  CreatedAt: string;
  UpdatedAt: string;
  CapacityProviderStrategy?: Array<{ CapacityProvider: string; Weight?: number; Base?: number }>;
  // Add other fields from the deployment object if needed
}

interface ECSDeploymentsDetailsProps {
  ecsDetails: {
    meta?: {
      Deployments?: ECSDeploymentItem[];
    };
  };
}

const ECSDeploymentsDetails = ({ ecsDetails }: ECSDeploymentsDetailsProps) => {
  const deployments = ecsDetails?.meta?.Deployments;

  if (!deployments || deployments.length === 0) {
    return <Typography sx={{ p: 2 }}>No deployment details available for this service.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={3}>
        {deployments.map((deployment, index) => (
          <Box key={deployment.Id || index} sx={{ border: '1px solid #e0e0e0', borderRadius: '4px', p: 2 }}>
            <Typography variant='h6' gutterBottom sx={{ fontWeight: 600 }}>
              Deployment: {deployment.Status} ({deployment.Id.split('/').pop()})
            </Typography>
            <Stack spacing={1.5}>
              <Box>
                <Typography variant='body2' sx={{ fontWeight: 500 }}>
                  Task Definition:
                </Typography>
                <Typography variant='body2'>{deployment.TaskDefinition.split('/').pop()}</Typography>
              </Box>
              <Box>
                <Typography variant='body2' sx={{ fontWeight: 500 }}>
                  Counts:
                </Typography>
                <Typography variant='body2'>
                  Desired: {deployment.DesiredCount}, Running: {deployment.RunningCount}, Pending: {deployment.PendingCount}
                </Typography>
              </Box>
              <Box>
                <Typography variant='body2' sx={{ fontWeight: 500 }}>
                  Rollout Status:
                </Typography>
                <Typography variant='body2'>State: {deployment.RolloutState}</Typography>
                {deployment.RolloutStateReason && (
                  <Typography variant='body2' sx={{ color: 'text.secondary', fontSize: '0.8rem' }}>
                    Reason: {deployment.RolloutStateReason}
                  </Typography>
                )}
              </Box>
              {deployment.CapacityProviderStrategy && deployment.CapacityProviderStrategy.length > 0 && (
                <Box>
                  <Typography variant='body2' sx={{ fontWeight: 500 }}>
                    Capacity Providers:
                  </Typography>
                  <List dense disablePadding sx={{ pl: 1 }}>
                    {deployment.CapacityProviderStrategy.map((cp, cpIndex) => (
                      <ListItem key={cpIndex} sx={{ py: 0.2, px: 0 }}>
                        <Chip size='small' label={`${cp.CapacityProvider} (Weight: ${cp.Weight ?? 'N/A'}, Base: ${cp.Base ?? 'N/A'})`} />
                      </ListItem>
                    ))}
                  </List>
                </Box>
              )}
              <Box>
                <Typography variant='body2' sx={{ fontWeight: 500 }}>
                  Timestamps:
                </Typography>
                <Typography variant='body2' component='div'>
                  Created: <Datetime value={deployment.CreatedAt} />
                </Typography>
                <Typography variant='body2' component='div'>
                  Updated: <Datetime value={deployment.UpdatedAt} />
                </Typography>
              </Box>
            </Stack>
          </Box>
        ))}
      </Stack>
    </Box>
  );
};

// --- ECS Clusters Component ---

const CLUSTER_HEADER = ['Cluster Name', 'Status', 'Services', 'Tasks', 'Container Instances', 'Tags', 'CreatedAt', ''];

const clusterInfoComponentFn = (_opt: any, dq: any) => <ECSClusterInfo clusterDetails={dq} />;

const createClusterMonitoringComponentFn = (accountId: string | undefined) => (_opt: any, drilldownQuery: any) =>
  <ECSSummary accountId={accountId} resourceId={drilldownQuery.meta.ClusterArn} serviceName={'AmazonECS'} resourceType='cluster' />;

const clusterCapacityComponentFn = (_opt: any, dq: any) => <ECSClusterCapacityProviders clusterDetails={dq} />;

const clusterSettingsComponentFn = (_opt: any, dq: any) => <ECSClusterSettings clusterDetails={dq} />;

const clusterTagsComponentFn = (_opt: any, dq: any) => <ECSClusterTagsDisplay clusterDetails={dq} />;

export const ECSClusters = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [ecsClusters, setEcsClusters] = useState([]);
  const [ecsClustersCount, setEcsClustersCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [searchFilter, setSearchFilter] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const stateOptions = getStateDropdownOptions('AmazonECS');

  const rowsPerPage = 10;
  const ecsClustersTableId = 'ecsClustersTable';

  const changePage = (page: number) => {
    setPage(page - 1);
  };

  const onSearchFilterChange = (e: any) => {
    setSearchFilter(e.target.value);
  };

  const onEnterPress = () => {
    if (page !== 0) {
      setPage(0);
    } else {
      listEcsClusters();
    }
  };

  const listEcsClusters = () => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: 'AmazonECS',
          type: 'cluster',
          nameFilter: searchFilter,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          ...buildStateApiParams('AmazonECS', selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const cloudResourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        const cloudResourceData = (res.data?.data?.cloud_resourses || []).map((item: any) => {
          const data: ICustomTable2Row[] = [];
          let MENU_ITEMS;
          if (hasWriteAccess(props?.accountId)) {
            MENU_ITEMS = [
              {
                icon: NewDelete,
                label: 'Delete',
                id: 0,
                disabled: true,
              },
            ];
          }
          data.push({
            component: <CustomText text1={item.name || item.resourse_id} subtext1={item.region} />,
            drilldownQuery: item,
          });
          data.push({
            component: <CustomLabels text={item.meta?.Status || item.status} />,
          });
          data.push({
            component: <Typography fontSize={13}>{item.meta?.ActiveServicesCount ?? 0}</Typography>,
          });
          data.push({
            component: (
              <CustomText
                text1={`${item.meta?.RunningTasksCount ?? 0} running`}
                subtext1={item.meta?.PendingTasksCount ? `${item.meta.PendingTasksCount} pending` : undefined}
              />
            ),
          });
          data.push({
            component: <Typography fontSize={13}>{item.meta?.RegisteredContainerInstancesCount ?? 0}</Typography>,
          });
          data.push({ component: <TagsCell tags={item.tags} /> });
          data.push({
            component: <Datetime value={item.created_at} />,
          });
          data.push({
            component: (
              <Box display={'flex'} justifyContent={'flex-end'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={undefined} />
              </Box>
            ),
          });

          return data;
        });
        setEcsClusters(cloudResourceData);
        setEcsClustersCount(cloudResourceCount);
      })
      .catch((error) => {
        setLoading(false);
        snackbar.error(`Error fetching ECS clusters: ${error.message}`);
        console.error('Error fetching ECS clusters:', error);
      });
  };

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, 'AmazonECS', 'cluster').then(setAvailableTagKeys);
    }
  }, [props?.accountId]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, 'AmazonECS', 'cluster').then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  useEffect(() => {
    listEcsClusters();
  }, [props?.accountId, page, selectedTagKey, selectedTagValue, selectedState]);

  return (
    <BoxLayout2
      heading={props.heading ?? 'ECS Clusters'}
      id='ecs-clusters-list'
      filterOptions={[
        {
          type: 'search',
          enabled: true,
          onSelect: onSearchFilterChange,
          minWidth: '200px',
          label: 'Search by Name/ARN Snippet',
          onEnter: onEnterPress,
          value: searchFilter,
        },
        {
          type: 'dropdown',
          label: 'State',
          value: selectedState,
          options: stateOptions,
          onSelect: (e: any) => {
            setSelectedState(e.target.value || 'all');
          },
        },
        {
          type: 'dropdown',
          label: 'Tag Key',
          value: selectedTagKey,
          options: availableTagKeys,
          onSelect: (e: any) => {
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
          },
        },
        {
          type: 'dropdown',
          enabled: !!selectedTagKey,
          label: 'Tag Value',
          value: selectedTagValue,
          options: availableTagValues,
          onSelect: (e: any) => {
            setSelectedTagValue(e.target.value || null);
          },
        },
      ]}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: ecsClustersTableId,
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
    >
      <CloudAccountTable
        id={ecsClustersTableId}
        headers={CLUSTER_HEADER}
        data={ecsClusters}
        rowsPerPage={rowsPerPage}
        onPageChange={changePage}
        totalRows={ecsClustersCount}
        loading={loading}
        showExpandable={true}
        pageNumber={page + 1}
        expandable={{
          tabs: [
            {
              text: 'Cluster Info',
              value: 1,
              key: 'ecs-cluster-info',
              componentFn: clusterInfoComponentFn,
            },
            {
              text: 'Monitoring',
              value: 2,
              key: 'ecs-cluster-monitoring',
              componentFn: createClusterMonitoringComponentFn(props?.accountId),
            },
            {
              text: 'Capacity Providers',
              value: 3,
              key: 'ecs-cluster-capacity',
              componentFn: clusterCapacityComponentFn,
            },
            {
              text: 'Settings',
              value: 4,
              key: 'ecs-cluster-settings',
              componentFn: clusterSettingsComponentFn,
            },
            {
              text: 'Tags',
              value: 5,
              key: 'ecs-cluster-tags',
              componentFn: clusterTagsComponentFn,
            },
          ],
        }}
        stickyColumnIndex={props.stickyColumnIndex}
        tableHeadingCenter={props.tableHeadingCenter}
      />
    </BoxLayout2>
  );
};

// --- ECS Cluster Detail Components ---

const ECSClusterInfo = ({ clusterDetails }: { clusterDetails: any }) => {
  const meta = clusterDetails?.meta;
  if (!meta) {
    return <Typography sx={{ p: 2 }}>No cluster information available.</Typography>;
  }

  const infoSections = [
    { label: 'Cluster Name', value: meta.ClusterName },
    { label: 'Cluster ARN', value: meta.ClusterArn },
    { label: 'Status', value: meta.Status },
    { label: 'Active Services', value: meta.ActiveServicesCount?.toString() },
    { label: 'Running Tasks', value: meta.RunningTasksCount?.toString() },
    { label: 'Pending Tasks', value: meta.PendingTasksCount?.toString() },
    { label: 'Registered Container Instances', value: meta.RegisteredContainerInstancesCount?.toString() },
    { label: 'Attachments Status', value: meta.AttachmentsStatus || 'N/A' },
  ];

  return (
    <Box sx={{ p: 2 }}>
      <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
        Cluster Overview
      </Typography>
      {infoSections.map((info) => (
        <Typography key={info.label} variant='body2' component='div'>
          <span style={{ fontWeight: 500 }}>{info.label}:</span> {info.value ?? 'N/A'}
        </Typography>
      ))}
    </Box>
  );
};

const ECSClusterCapacityProviders = ({ clusterDetails }: { clusterDetails: any }) => {
  const capacityProviders = clusterDetails?.meta?.CapacityProviders;
  const defaultStrategy = clusterDetails?.meta?.DefaultCapacityProviderStrategy;

  if ((!capacityProviders || capacityProviders.length === 0) && (!defaultStrategy || defaultStrategy.length === 0)) {
    return <Typography sx={{ p: 2 }}>No capacity providers configured for this cluster.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={2}>
        {capacityProviders && capacityProviders.length > 0 && (
          <Box>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Capacity Providers
            </Typography>
            <List dense>
              {capacityProviders.map((cp: string) => (
                <ListItem key={cp} sx={{ py: 0.5 }}>
                  <Chip label={cp} size='small' />
                </ListItem>
              ))}
            </List>
          </Box>
        )}
        {defaultStrategy && defaultStrategy.length > 0 && (
          <Box>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Default Capacity Provider Strategy
            </Typography>
            <List dense>
              {defaultStrategy.map((strategy: any) => (
                <ListItem key={strategy.CapacityProvider} sx={{ py: 0.5 }}>
                  <Chip size='small' label={`${strategy.CapacityProvider} (Weight: ${strategy.Weight ?? 'N/A'}, Base: ${strategy.Base ?? 'N/A'})`} />
                </ListItem>
              ))}
            </List>
          </Box>
        )}
      </Stack>
    </Box>
  );
};

const ECSClusterSettings = ({ clusterDetails }: { clusterDetails: any }) => {
  const settings = clusterDetails?.meta?.Settings;

  if (!settings || settings.length === 0) {
    return <Typography sx={{ p: 2 }}>No settings available for this cluster.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
        Cluster Settings
      </Typography>
      {settings.map((setting: { Name: string; Value: string }) => (
        <Typography key={setting.Name} variant='body2' component='div'>
          <span style={{ fontWeight: 500 }}>{setting.Name}:</span> {setting.Value}
        </Typography>
      ))}
    </Box>
  );
};

const ECSClusterTagsDisplay = ({ clusterDetails }: { clusterDetails: any }) => {
  const tags = clusterDetails?.meta?.Tags;
  if (!tags || tags.length === 0) {
    return <Typography sx={{ p: 2 }}>No tags available for this cluster.</Typography>;
  }
  return (
    <Box sx={{ p: 2 }}>
      <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
        Cluster Tags
      </Typography>
      <List dense>
        {tags.map((tag: { Key: string; Value: string }) => (
          <ListItem key={tag.Key} sx={{ py: 0.5, display: 'flex', justifyContent: 'start' }}>
            <Chip label={`${tag.Key}: ${tag.Value}`} size='small' />
          </ListItem>
        ))}
      </List>
    </Box>
  );
};

// --- New Components for ECS Task Details ---

interface ECSTaskDetailsProps {
  taskDetails: {
    name?: string;
    status?: string;
    region?: string;
    meta?: {
      TaskArn?: string;
      ClusterArn?: string;
      ServiceArn?: string;
      TaskDefinitionArn?: string;
      LaunchType?: string;
      CapacityProviderName?: string;
      Cpu?: string;
      Memory?: string;
      CreatedAt?: string;
      StartedAt?: string;
      PullStartedAt?: string;
      PullStoppedAt?: string;
      StoppingAt?: string;
      StoppedAt?: string;
      StoppedReason?: string;
      StopCode?: string;
      Version?: number;
      PlatformFamily?: string;
      PlatformVersion?: string;
      AvailabilityZone?: string;
      Group?: string;
      LastStatus?: string;
      DesiredStatus?: string;
      EphemeralStorage?: { SizeInGiB?: number };
      FargateEphemeralStorage?: { SizeInGiB?: number };
      EnableExecuteCommand?: boolean;
    };
  };
}

const ECSTaskInfo = ({ taskDetails }: ECSTaskDetailsProps) => {
  const meta = taskDetails?.meta;
  if (!meta) {
    return <Typography sx={{ p: 2 }}>No task information available.</Typography>;
  }

  const infoSections = [
    { label: 'Task ARN', value: meta.TaskArn?.split('/').pop() },
    { label: 'Task ARN (Full)', value: meta.TaskArn },
    { label: 'Task Definition', value: meta.TaskDefinitionArn?.split('/').pop() },
    { label: 'Cluster', value: meta.ClusterArn?.split('/').pop() },
    { label: 'Service', value: meta.ServiceArn?.split('/').pop() },
    { label: 'Status (Reported)', value: taskDetails.status },
    { label: 'Last Status (ECS)', value: meta.LastStatus },
    { label: 'Desired Status', value: meta.DesiredStatus },
    { label: 'Launch Type', value: meta.LaunchType },
    { label: 'Capacity Provider', value: meta.CapacityProviderName || 'N/A' },
    { label: 'CPU (Task)', value: meta.Cpu ? `${meta.Cpu} units` : 'N/A' },
    { label: 'Memory (Task)', value: meta.Memory ? `${meta.Memory} MiB` : 'N/A' },
    { label: 'Ephemeral Storage', value: `${meta.EphemeralStorage?.SizeInGiB || meta.FargateEphemeralStorage?.SizeInGiB || 'N/A'} GiB` },
    { label: 'Version', value: meta.Version?.toString() },
    { label: 'Platform', value: `${meta.PlatformFamily || 'N/A'} (${meta.PlatformVersion || 'N/A'})` },
    { label: 'Availability Zone', value: meta.AvailabilityZone },
    { label: 'Group', value: meta.Group },
    { label: 'Execute Command Enabled', value: meta.EnableExecuteCommand ? 'Yes' : 'No' },
  ];

  const timeSections = [
    { label: 'Created At', value: meta.CreatedAt ? <Datetime value={meta.CreatedAt} /> : 'N/A' },
    { label: 'Pull Started At', value: meta.PullStartedAt ? <Datetime value={meta.PullStartedAt} /> : 'N/A' },
    { label: 'Pull Stopped At', value: meta.PullStoppedAt ? <Datetime value={meta.PullStoppedAt} /> : 'N/A' },
    { label: 'Started At', value: meta.StartedAt ? <Datetime value={meta.StartedAt} /> : 'N/A' },
    { label: 'Stopping At', value: meta.StoppingAt ? <Datetime value={meta.StoppingAt} /> : 'N/A' },
    { label: 'Stopped At', value: meta.StoppedAt ? <Datetime value={meta.StoppedAt} /> : 'N/A' },
  ];

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={3}>
        <Box>
          <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
            Task Overview
          </Typography>
          {infoSections.map((info) =>
            info.value ? (
              <Typography key={info.label} variant='body2' component='div'>
                <span style={{ fontWeight: 500 }}>{info.label}:</span> {info.value}
              </Typography>
            ) : null
          )}
        </Box>
        <Box>
          <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
            Timestamps
          </Typography>
          {timeSections.map((time) => (
            <Typography key={time.label} variant='body2' component='div'>
              <span style={{ fontWeight: 500 }}>{time.label}:</span> {time.value}
            </Typography>
          ))}
        </Box>
        {meta.StoppedReason && (
          <Box>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Stop Information
            </Typography>
            <Typography variant='body2'>
              <span style={{ fontWeight: 500 }}>Stop Code:</span> {meta.StopCode || 'N/A'}
            </Typography>
            <Typography variant='body2'>
              <span style={{ fontWeight: 500 }}>Stop Reason:</span> {meta.StoppedReason}
            </Typography>
          </Box>
        )}
      </Stack>
    </Box>
  );
};

interface ECSTaskContainerItem {
  Name: string;
  Image: string;
  ImageDigest?: string;
  Cpu?: string;
  Memory?: string;
  MemoryReservation?: string;
  LastStatus?: string;
  HealthStatus?: string;
  ExitCode?: number | null;
  Reason?: string | null;
  NetworkInterfaces?: Array<{ PrivateIpv4Address?: string }>;
}
interface ECSTaskContainersProps {
  taskDetails: { meta?: { Containers?: ECSTaskContainerItem[] } };
}

const ECSTaskContainers = ({ taskDetails }: ECSTaskContainersProps) => {
  const containers = taskDetails?.meta?.Containers;
  if (!containers || containers.length === 0) {
    return <Typography sx={{ p: 2 }}>No container details available for this task.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={3}>
        {containers.map((container, index) => (
          <Box key={container.Name || index} sx={{ border: '1px solid #e0e0e0', borderRadius: '4px', p: 2 }}>
            <Typography variant='h6' gutterBottom sx={{ fontWeight: 600 }}>
              Container: {container.Name}
            </Typography>
            <Stack spacing={1}>
              <Typography variant='body2'>
                <span style={{ fontWeight: 500 }}>Image:</span> {container.Image}
              </Typography>
              {container.ImageDigest && (
                <Typography variant='body2' sx={{ fontSize: '0.8rem', color: 'text.secondary' }}>
                  <span style={{ fontWeight: 500 }}>Digest:</span> {container.ImageDigest}
                </Typography>
              )}
              <Typography variant='body2'>
                <span style={{ fontWeight: 500 }}>CPU:</span> {container.Cpu || 'N/A'} units
              </Typography>
              <Typography variant='body2'>
                <span style={{ fontWeight: 500 }}>Memory:</span> {container.Memory || 'N/A'} MiB
              </Typography>
              <Typography variant='body2'>
                <span style={{ fontWeight: 500 }}>Memory Reservation:</span> {container.MemoryReservation || 'N/A'} MiB
              </Typography>
              <Typography variant='body2'>
                <span style={{ fontWeight: 500 }}>Last Status:</span> {container.LastStatus}
              </Typography>
              <Typography variant='body2'>
                <span style={{ fontWeight: 500 }}>Health Status:</span> {container.HealthStatus}
              </Typography>
              {container.ExitCode !== null && (
                <Typography variant='body2'>
                  <span style={{ fontWeight: 500 }}>Exit Code:</span> {container.ExitCode}
                </Typography>
              )}
              {container.Reason && (
                <Typography variant='body2'>
                  <span style={{ fontWeight: 500 }}>Reason:</span> {container.Reason}
                </Typography>
              )}
              {container.NetworkInterfaces && container.NetworkInterfaces.length > 0 && (
                <Box mt={1}>
                  <Typography variant='body2' sx={{ fontWeight: 500 }}>
                    Network Interfaces:
                  </Typography>
                  <List dense disablePadding sx={{ pl: 1 }}>
                    {container.NetworkInterfaces.map((ni, niIndex) => (
                      <ListItem key={niIndex} sx={{ py: 0.2, px: 0 }}>
                        <Chip size='small' label={`Private IP: ${ni.PrivateIpv4Address || 'N/A'}`} />
                      </ListItem>
                    ))}
                  </List>
                </Box>
              )}
            </Stack>
          </Box>
        ))}
      </Stack>
    </Box>
  );
};

interface ECSTaskAttachmentDetail {
  Name: string;
  Value: string;
}
interface ECSTaskAttachment {
  Id: string;
  Type: string;
  Status: string;
  Details: ECSTaskAttachmentDetail[];
}
interface ECSTaskNetworkInfoProps {
  taskDetails: { meta?: { Attachments?: ECSTaskAttachment[]; Connectivity?: string; ConnectivityAt?: string } };
}
const ECSTaskNetworkInfo = ({ taskDetails }: ECSTaskNetworkInfoProps) => {
  const attachments = taskDetails?.meta?.Attachments?.filter((att) => att.Type === 'ElasticNetworkInterface');
  const connectivity = taskDetails?.meta?.Connectivity;
  const connectivityAt = taskDetails?.meta?.ConnectivityAt;

  if ((!attachments || attachments.length === 0) && !connectivity) {
    return <Typography sx={{ p: 2 }}>No network attachment details available.</Typography>;
  }

  return (
    <Box sx={{ p: 2 }}>
      <Stack spacing={2}>
        {connectivity && (
          <Box>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Connectivity
            </Typography>
            <Typography variant='body2'>
              <span style={{ fontWeight: 500 }}>Status:</span> {connectivity}
            </Typography>
            {connectivityAt && (
              <Typography variant='body2' component='div'>
                <span style={{ fontWeight: 500 }}>At:</span> <Datetime value={connectivityAt} />
              </Typography>
            )}
          </Box>
        )}
        {attachments?.map((att, index) => (
          <Box key={att.Id || index} sx={{ border: '1px solid #e0e0e0', borderRadius: '4px', p: 2, mt: connectivity ? 2 : 0 }}>
            <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
              Network Interface: {att.Id.split('-').pop()} ({att.Status})
            </Typography>
            {att.Details.map((detail) => (
              <Typography key={detail.Name} variant='body2'>
                <span style={{ fontWeight: 500 }}>{detail.Name.replace(/([A-Z])/g, ' $1').trim()}:</span> {detail.Value}
              </Typography>
            ))}
          </Box>
        ))}
      </Stack>
    </Box>
  );
};

interface ECSTaskTag {
  Key: string;
  Value: string;
}
interface ECSTaskTagsDisplayProps {
  taskDetails: { meta?: { Tags?: ECSTaskTag[] } };
}
const ECSTaskTagsDisplay = ({ taskDetails }: ECSTaskTagsDisplayProps) => {
  const tags = taskDetails?.meta?.Tags;
  if (!tags || tags.length === 0) {
    return <Typography sx={{ p: 2 }}>No tags available for this task.</Typography>;
  }
  return (
    <Box sx={{ p: 2 }}>
      <Typography variant='subtitle1' gutterBottom sx={{ fontWeight: 600 }}>
        Task Tags
      </Typography>
      <List dense>
        {tags.map((tag) => (
          <ListItem key={tag.Key} sx={{ py: 0.5, display: 'flex', justifyContent: 'start' }}>
            <Chip label={`${tag.Key}: ${tag.Value}`} size='small' />
          </ListItem>
        ))}
      </List>
    </Box>
  );
};

// --- End of New Components for ECS Task Details ---

interface TabPanelProps {
  children?: React.ReactNode;
  index: number;
  value: number;
}

function TabPanel(props: TabPanelProps) {
  const { children, value, index, ...other } = props;

  return (
    <div role='tabpanel' hidden={value !== index} id={`ecs-tabpanel-${index}`} aria-labelledby={`ecs-tab-${index}`} {...other}>
      {value === index && <Box sx={{ pt: 3 }}>{children}</Box>}
    </div>
  );
}

const ECSInstances = (props: { accountId: string | undefined; heading: string | undefined }) => {
  const [activeTab, setActiveTab] = useState(0);

  const handleTabChange = (event: React.SyntheticEvent, newValue: number) => {
    setActiveTab(newValue);
  };

  return (
    <BoxLayout2
      heading={props.heading || 'ECS Resources'}
      id='ecs-cluster-list'
      filterOptions={[]}
      sharingOptions={{
        download: {
          enabled: false,
          onClick: () => ({
            tableId: '',
          }),
        },
        sharing: { enabled: false, onClick: null },
      }}
    >
      <Box sx={{ borderBottom: 1, borderColor: 'divider' }}>
        <Tabs value={activeTab} onChange={handleTabChange} aria-label='ECS resource types'>
          <Tab label='Clusters' id='ecs-tab-0' aria-controls='ecs-tabpanel-0' sx={{ fontSize: 13 }} />
          <Tab label='Services' id='ecs-tab-1' aria-controls='ecs-tabpanel-1' sx={{ fontSize: 13 }} />
          <Tab label='Tasks' id='ecs-tab-2' aria-controls='ecs-tabpanel-2' sx={{ fontSize: 13 }} />
        </Tabs>
      </Box>
      <TabPanel value={activeTab} index={0}>
        <ECSClusters accountId={props.accountId} heading='' />
      </TabPanel>
      <TabPanel value={activeTab} index={1}>
        <ECSServices accountId={props.accountId} serviceName='AmazonECS' heading='' />
      </TabPanel>
      <TabPanel value={activeTab} index={2}>
        <ECSTasks accountId={props.accountId} serviceName='AmazonECS' heading='' />
      </TabPanel>
    </BoxLayout2>
  );
};

export default ECSInstances;
