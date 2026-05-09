import { Grid, Typography, TextField, Stack, InputAdornment, IconButton } from '@mui/material';
import { ContentCopy } from '@mui/icons-material';
import React, { useEffect, useState, useCallback, useMemo } from 'react';
import apiAccount from '@api1/account';
import apiIntegrations from '@api1/integrations';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid, safeJSONParse, toKebabCase } from 'src/utils/common';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import Text from '@components1/common/format/Text';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { hasWriteAccess } from '@lib/auth';
import { BoxLayout2, ThreeDotsMenu } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import { inputSx } from '@data/themes/inputField';
import { action } from 'src/utils/actionStyles';
import NDialog from '@components1/common/modal/NDialog';
import { useUpdateAllClusterOption } from '@components1/common/UpdateDataContext';
import apiKubernetes1 from '@api1/kubernetes1';
import apiUser from '@api1/user';
import MarkDowns from '@components1/common/MarkDowns';
import CfUpdateModal from './CfUpdateModal';
import EnableGcpWebhookModal from './EnableGcpWebhookModal';

const EVENTGRID_INSTRUCTIONS = `### Enable Real-Time Resource Events
  ### Step 1. Copy the values below
  ### Step 2. Click "Deploy via Azure Portal"
   - It will open the ARM template deployment page in Azure Portal.
   - Paste the copied **Account Token** into the **NudgebeeExternalId** field.
   - Paste the copied **Webhook URL** into the **NudgebeeWebhookUrl** field.
   - **Already have an Event Grid System Topic?** If your subscription already has one with **Source** = your subscription ID (topic type \`Microsoft.Resources.Subscriptions\`), enter its name in the **existingSystemTopicName** field. Find it via **Azure Portal → Event Grid → System Topics** (look for the one whose Source matches \`/subscriptions/<your-subscription-id>\`), or run: \`az eventgrid system-topic list --subscription <your-subscription-id> --query "[?contains(source, '/subscriptions/')].{Name:name, Source:source, ResourceGroup:resourceGroup}" -o table\`. If the topic is in a different resource group, also fill in **existingSystemTopicResourceGroup**.
   - Click **Review + create** then **Create**.
  ### Step 3. Done!
   - Once deployed, resource changes (VM start/stop, SQL operations, etc.) will be synced in real-time.`;

const EVENTBRIDGE_INSTRUCTIONS = `### Enable Real-Time Resource Events
  ### Step 1. Copy the Account Token below
  ### Step 2. Click "Deploy via AWS Console"
   - It will open the CloudFormation quick-create page in AWS Console.
   - The **NudgebeeExternalId** parameter will be pre-filled.
   - Review the stack details and click **Create stack**.
  ### Step 3. Done!
   - Once deployed, resource changes (EC2 state changes, RDS events, ECS/Lambda updates, CloudWatch alarms) will be synced in real-time.
   - The stack automatically covers all enabled AWS regions.`;

const TABLE_HEADERS = [
  { name: 'Name', width: '22%' },
  { name: 'Created At', width: '12%' },
  { name: 'Created By', width: '12%' },
  { name: 'Account Number', width: '20%' },
  { name: 'Status', width: '10%' },
  { name: 'Real-Time Events', width: '14%' },
  { name: '', width: '5%' },
];

const CloudAccountTile = ({ cloudProvider, title, AddAccountModalComponent, addAccountButtonText, AddOrgModalComponent, addOrgButtonText }) => {
  const [loading, setLoading] = useState(false);
  const [rawAccounts, setRawAccounts] = useState([]);
  const [openAddModal, setOpenAddModal] = useState(false);
  const [openOrgModal, setOpenOrgModal] = useState(false);
  const [updateAccountStatus, setUpdateAccountStatus] = useState({});
  const [isStatusUpdating, setIsStatusUpdating] = useState(false);
  const [renameModalOpen, setRenameModalOpen] = useState(false);
  const [selectedAccount, setSelectedAccount] = useState(null);
  const [newAccountName, setNewAccountName] = useState('');
  const [renameLoading, setRenameLoading] = useState(false);
  const [renameError, setRenameError] = useState('');
  const updateAllClusters = useUpdateAllClusterOption();
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [totalCount, setTotalCount] = useState(0);
  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('active');
  const [refreshKey, setRefreshKey] = useState(0);
  const [eventGridModalOpen, setEventGridModalOpen] = useState(false);
  const [eventGridLoading, setEventGridLoading] = useState(false);
  const [eventGridData, setEventGridData] = useState(null);
  const [eventBridgeModalOpen, setEventBridgeModalOpen] = useState(false);
  const [eventBridgeLoading, setEventBridgeLoading] = useState(false);
  const [eventBridgeData, setEventBridgeData] = useState(null);
  const [billingModalOpen, setBillingModalOpen] = useState(false);
  const [billingProjectId, setBillingProjectId] = useState('');
  const [billingDatasetName, setBillingDatasetName] = useState('');
  const [billingTableName, setBillingTableName] = useState('');
  const [billingLoading, setBillingLoading] = useState(false);
  const [cfUpdateModalOpen, setCfUpdateModalOpen] = useState(false);
  const [cfUpdateAccountId, setCfUpdateAccountId] = useState(null);
  const [webhookModalOpen, setWebhookModalOpen] = useState(false);
  const [webhookModalAccount, setWebhookModalAccount] = useState(null);
  const [webhookAccountIds, setWebhookAccountIds] = useState(new Set());
  const [existedIntegrations, setExistedIntegrations] = useState({});
  const [enabledWebhookIntegrations, setEnabledWebhookIntegrations] = useState([]);

  const fetchWebhookStatus = useCallback(() => {
    if (cloudProvider !== 'GCP' && cloudProvider !== 'Azure') {
      return;
    }
    const integrationTypes = cloudProvider === 'GCP' ? 'gcp_monitoring_webhook' : cloudProvider === 'Azure' ? 'azure_monitor_webhook' : null;
    if (!integrationTypes) {
      return;
    }
    apiIntegrations
      .listIntegrations({ type: integrationTypes, limit: 1000, offset: 0 })
      .then((res) => {
        const integrations = res?.data?.data?.admin_get_integrations_v2?.rows || [];
        const accountIds = new Set();
        const integrationMap = {};
        const enabled = [];
        for (const integration of integrations) {
          if (integration.status === 'enabled') {
            const rawAccounts = integration.integrations_cloud_accounts;
            const cloudAccounts = Array.isArray(rawAccounts) ? rawAccounts : safeJSONParse(rawAccounts) || [];
            const mappedIds = cloudAccounts.map((ca) => ca.cloud_account_id).filter(Boolean);
            enabled.push({ id: integration.id, name: integration.name, accountIds: mappedIds });
            for (const ca of cloudAccounts) {
              if (ca.cloud_account_id && !integrationMap[String(ca.cloud_account_id)]) {
                accountIds.add(ca.cloud_account_id);
                integrationMap[String(ca.cloud_account_id)] = {
                  id: integration.id,
                  integration_config_name: integration.name,
                  accountIds: mappedIds,
                };
              }
            }
          }
        }
        setWebhookAccountIds(accountIds);
        setExistedIntegrations(integrationMap);
        setEnabledWebhookIntegrations(enabled);
      })
      .catch((err) => {
        console.error('Failed to fetch webhook status:', err);
      });
  }, [cloudProvider]);

  useEffect(() => {
    fetchWebhookStatus();
  }, [fetchWebhookStatus]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const statusOptions = [
    { label: 'Active', value: 'active' },
    { label: 'Disabled', value: 'disabled' },
  ];

  const handleNameFilterChange = (e) => {
    setNameInput(e.target.value);
  };

  const handleStatusFilterChange = (e) => {
    setSelectedStatusFilter(e.target.value);
    setCurrentPage(0);
  };

  const onEnterPress = () => {
    setSelectedNameFilter(nameInput);
    setCurrentPage(0);
  };

  // Orphaned-name recovery: GCP webhook integrations may exist with one of
  // two naming patterns — `GCP Monitoring - <account_id>` (current, immutable)
  // or `GCP Monitoring - <account_name>` (legacy, drifts on rename). If the
  // integrations_cloud_accounts mapping is dropped (account recreated, partial
  // onboarding, manual cleanup) but the integration row survives, the menu
  // would show "Enable" and a re-enable attempt would hit a duplicate-name
  // conflict. Match either pattern here so the menu shows "Manage", the
  // indicator shows active, and the modal opens straight to the success view.
  const orphanedNameIntegrations = useMemo(() => {
    if (cloudProvider !== 'GCP') {
      return {};
    }
    const result = {};
    for (const acc of rawAccounts) {
      if (!acc?.id) {
        continue;
      }
      if (existedIntegrations[String(acc.id)]) {
        continue;
      }
      const idKeyedName = `GCP Monitoring - ${acc.id}`;
      const legacyName = acc.account_name ? `GCP Monitoring - ${acc.account_name}` : null;
      const match = enabledWebhookIntegrations.find((i) => i.name === idKeyedName || (legacyName && i.name === legacyName));
      if (match) {
        result[String(acc.id)] = {
          id: match.id,
          integration_config_name: match.name,
          accountIds: Array.from(new Set([...match.accountIds, acc.id])),
        };
      }
    }
    return result;
  }, [rawAccounts, cloudProvider, enabledWebhookIntegrations, existedIntegrations]);

  const allExistedIntegrations = useMemo(
    () => ({ ...existedIntegrations, ...orphanedNameIntegrations }),
    [existedIntegrations, orphanedNameIntegrations]
  );

  const allWebhookAccountIds = useMemo(() => {
    const merged = new Set(webhookAccountIds);
    for (const accId of Object.keys(orphanedNameIntegrations)) {
      merged.add(accId);
    }
    return merged;
  }, [webhookAccountIds, orphanedNameIntegrations]);

  const realtimeEventAccountIds = useMemo(() => {
    const ids = new Set(allWebhookAccountIds);
    if (cloudProvider === 'AWS') {
      for (const account of rawAccounts) {
        const agents = typeof account.agents === 'string' ? safeJSONParse(account.agents) : account.agents;
        if (Array.isArray(agents)) {
          for (const agent of agents) {
            const cs = typeof agent.connection_status === 'string' ? safeJSONParse(agent.connection_status) : agent.connection_status;
            // connected_at is stamped when the user clicks "Connect EventBridge";
            // last_event_at is stamped on every received event. Either one
            // means the integration is in place — without connected_at the
            // indicator stays inactive for hours until the first event arrives.
            if (cs?.eventbridge?.last_event_at || cs?.eventbridge?.connected_at) {
              ids.add(account.id);
            }
          }
        }
      }
    }
    return ids;
  }, [allWebhookAccountIds, rawAccounts, cloudProvider]);

  const getMenuItems = (item) => {
    if (!hasWriteAccess()) {
      return [];
    }
    const items = [
      {
        label: 'Rename Account Name',
        id: 0,
      },
      {
        label: item.status == 'disabled' ? 'Enable' : 'Disable',
        id: 1,
      },
    ];
    if (cloudProvider === 'Azure') {
      items.push({
        label: realtimeEventAccountIds.has(item.id) ? 'Manage Event Grid' : 'Connect Event Grid',
        id: 2,
      });
    }
    if (cloudProvider === 'AWS') {
      items.push({
        label: realtimeEventAccountIds.has(item.id) ? 'Manage EventBridge' : 'Connect EventBridge',
        id: 3,
      });
    }
    if (cloudProvider === 'GCP') {
      items.push({
        label: 'Edit Billing Config',
        id: 4,
      });
      items.push({
        label: realtimeEventAccountIds.has(item.id) ? 'Manage Real-Time Alerts' : 'Enable Real-Time Alerts',
        id: 5,
      });
    }
    if (cloudProvider === 'AWS') {
      items.push({
        label: 'Update Permissions',
        id: 6,
      });
    }
    return items;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setSelectedAccount(data);
      setNewAccountName(data.account_name);
      setRenameModalOpen(true);
      setRenameError('');
    } else if (menuItem.id === 1) {
      setUpdateAccountStatus({ name: data.account_name, id: data.id, status: data.status == 'disabled' ? 'active' : 'disabled' });
    } else if (menuItem.id === 2) {
      handleOpenEventGridModal(data);
    } else if (menuItem.id === 3) {
      handleOpenEventBridgeModal(data);
    } else if (menuItem.id === 4) {
      setSelectedAccount(data);
      let accountData = data.data || {};
      if (typeof accountData === 'string') {
        try {
          accountData = JSON.parse(accountData);
        } catch {
          accountData = {};
        }
      }
      const billing = accountData.billing_data || {};
      setBillingProjectId(billing.billing_project_id || '');
      setBillingDatasetName(billing.dataset_name || '');
      setBillingTableName(billing.table_name || '');
      setBillingModalOpen(true);
    } else if (menuItem.id === 5) {
      setWebhookModalAccount(data);
      setWebhookModalOpen(true);
    } else if (menuItem.id === 6) {
      handleUpdatePermissions(data);
    }
  };

  const tableData = useMemo(
    () =>
      rawAccounts.map((item) => [
        { component: <Text value={item.account_name} /> },
        { component: <Datetime value={item.created_at} /> },
        { component: <Text value={item?.created_by_name || '-'} /> },
        { component: <Text value={item.account_number || '-'} /> },
        { component: <CustomLabels text={item.status || '-'} /> },
        { component: realtimeEventAccountIds.has(item.id) ? <CustomLabels text='active' /> : <Text value='-' /> },
        { component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} /> },
      ]),
    [rawAccounts, realtimeEventAccountIds, getMenuItems, onMenuClick]
  );

  const handleUpdatePermissions = (account) => {
    setCfUpdateAccountId(account.id);
    setCfUpdateModalOpen(true);
  };

  const handleOpenEventGridModal = (account) => {
    setEventGridModalOpen(true);
    setEventGridLoading(true);
    apiKubernetes1
      .getAzureARMTemplateURL(account.id)
      .then((res) => {
        const data = res?.data?.data?.azure_eventgrid_onboard;
        if (data?.url) {
          setEventGridData(data);
        } else {
          snackbar.error('Event Grid configuration is not available.');
          setEventGridModalOpen(false);
        }
      })
      .catch((error) => {
        console.error('Failed to fetch ARM template URL:', error);
        snackbar.error('Failed to fetch Event Grid setup details.');
        setEventGridModalOpen(false);
      })
      .finally(() => {
        setEventGridLoading(false);
      });
  };

  const closeEventGridModal = () => {
    setEventGridModalOpen(false);
    setEventGridData(null);
    fetchWebhookStatus();
  };

  const handleOpenEventBridgeModal = (account) => {
    setEventBridgeModalOpen(true);
    setEventBridgeLoading(true);
    apiKubernetes1
      .getAwsEventBridgeOnboardURL(account.id)
      .then((res) => {
        const data = res?.data?.data?.aws_eventbridge_onboard;
        if (data?.url) {
          setEventBridgeData(data);
        } else {
          snackbar.error('EventBridge configuration is not available.');
          setEventBridgeModalOpen(false);
        }
      })
      .catch((error) => {
        console.error('Failed to fetch EventBridge onboard URL:', error);
        snackbar.error('Failed to fetch EventBridge setup details.');
        setEventBridgeModalOpen(false);
      })
      .finally(() => {
        setEventBridgeLoading(false);
      });
  };

  const closeEventBridgeModal = () => {
    setEventBridgeModalOpen(false);
    setEventBridgeData(null);
  };

  const copyToClipboard = (text) => {
    navigator.clipboard.writeText(text);
    snackbar.success('Copied to clipboard');
  };

  const listCloudAccounts = () => {
    setLoading(true);
    apiKubernetes1
      .listAcc({
        cloudProvider: cloudProvider,
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
        nameSearch: selectedNameFilter || undefined,
        statusSearch: selectedStatusFilter || undefined,
      })
      .then((res) => {
        const accounts = res?.data?.data?.get_cloud_accounts_v2?.rows || [];
        setTotalCount(res?.data?.data?.get_cloud_accounts_grouping_v2?.rows?.[0]?.count || 0);
        setRawAccounts(accounts);
      })
      .catch((error) => {
        snackbar.error(`Failed to fetch ${cloudProvider} accounts.`);
        console.error(`Failed to fetch ${cloudProvider} accounts:`, error);
        setRawAccounts([]);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listCloudAccounts();
  }, [selectedNameFilter, selectedStatusFilter, recordsPerPage, currentPage, refreshKey]);

  const closeRenameModal = () => {
    setRenameModalOpen(false);
    setSelectedAccount(null);
    setNewAccountName('');
    setRenameError('');
    setRenameLoading(false);
  };

  const handleRenameAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setRenameError(
        'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore'
      );
    } else {
      setRenameError('');
    }
    setNewAccountName(value);
  };

  const handleRenameSubmit = () => {
    if (renameError || !selectedAccount || !newAccountName) {
      return;
    }

    setRenameLoading(true);
    apiAccount
      .updateAccount({ id: selectedAccount.id }, { account_name: newAccountName })
      .then((res) => {
        if (res?.data?.errors?.length > 0) {
          snackbar.error('Failed to Rename Account');
        } else {
          snackbar.success('Account Renamed successfully');
          closeRenameModal();
          listCloudAccounts();
        }
      })
      .catch((error) => {
        console.error('Failed to rename account:', error);
        snackbar.error('Failed to Rename Account');
      })
      .finally(() => {
        setRenameLoading(false);
      });
  };

  const handleUpdateAccountStatus = () => {
    if (!updateAccountStatus.id) {
      return;
    }

    setIsStatusUpdating(true);
    apiAccount
      .updateAccount({ id: updateAccountStatus.id }, { status: updateAccountStatus.status })
      .then((res) => {
        if (res?.data?.errors?.length > 0) {
          snackbar.error('Failed to Update Account Status');
        } else {
          snackbar.success('Account Status Updated successfully');
          listCloudAccounts();
          updateAllClusters(true);
        }
      })
      .catch((error) => {
        console.error('Failed to update account status:', error);
        snackbar.error('Failed to Update Account Status');
      })
      .finally(() => {
        setIsStatusUpdating(false);
        setUpdateAccountStatus({});
      });
  };

  const closeBillingModal = () => {
    setBillingModalOpen(false);
    setSelectedAccount(null);
    setBillingProjectId('');
    setBillingDatasetName('');
    setBillingTableName('');
    setBillingLoading(false);
  };

  const handleBillingSubmit = () => {
    if (!selectedAccount || !billingDatasetName || !billingTableName) {
      return;
    }

    setBillingLoading(true);
    let existingData = selectedAccount.data || {};
    if (typeof existingData === 'string') {
      try {
        existingData = JSON.parse(existingData);
      } catch {
        existingData = {};
      }
    }
    const updatedData = {
      ...existingData,
      billing_data: {
        billing_project_id: billingProjectId || '',
        dataset_name: billingDatasetName,
        table_name: billingTableName,
      },
    };
    apiAccount
      .updateAccount({ id: selectedAccount.id }, { data: updatedData })
      .then((res) => {
        if (res?.data?.errors?.length > 0) {
          snackbar.error('Failed to update billing config.');
        } else {
          snackbar.success('Billing config updated successfully.');
          closeBillingModal();
          listCloudAccounts();
        }
      })
      .catch((error) => {
        console.error('Failed to update billing config:', error);
        snackbar.error('Failed to update billing config.');
      })
      .finally(() => {
        setBillingLoading(false);
      });
  };

  const handleCloseAddModal = (wasSuccessful = false) => {
    setOpenAddModal(false);
    // Only refresh the list if an account was successfully added
    if (wasSuccessful) {
      setSelectedNameFilter('');
      setSelectedStatusFilter('active');
      setCurrentPage(0);
      setRefreshKey((prev) => prev + 1);
      updateAllClusters(true);
    }
  };

  const handleCloseOrgModal = (wasSuccessful = false) => {
    setOpenOrgModal(false);
    if (wasSuccessful) {
      listCloudAccounts();
    }
  };

  return (
    <>
      <AddAccountModalComponent open={openAddModal} onClose={handleCloseAddModal} />
      {AddOrgModalComponent && <AddOrgModalComponent open={openOrgModal} onClose={handleCloseOrgModal} />}
      <Modal
        width='sm'
        open={renameModalOpen}
        handleClose={renameLoading ? () => {} : closeRenameModal}
        title={`Rename ${cloudProvider} Account`}
        loader={renameLoading}
      >
        <Grid container spacing={2} p={2}>
          <Grid item xs={12}>
            <TextField
              sx={inputSx}
              value={newAccountName}
              size='small'
              margin='normal'
              fullWidth
              id='rename-account-name'
              label='Display Name'
              required
              onChange={(e) => handleRenameAccountNameChange(e.target.value)}
              error={!!renameError}
              helperText={renameError}
            />
          </Grid>
        </Grid>
        <Grid container spacing={2} mt={1} mb={2} justifyContent='flex-end' sx={{ button: { minWidth: '140px' }, paddingRight: '16px' }}>
          <Grid item>
            <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={closeRenameModal} disabled={renameLoading} />
          </Grid>
          <Grid item>
            <CustomButton
              size='Medium'
              text='Save'
              disabled={!!renameError || renameLoading || !newAccountName || newAccountName === selectedAccount?.account_name}
              onClick={handleRenameSubmit}
            />
          </Grid>
        </Grid>
      </Modal>
      <NDialog
        buttonText='Confirm'
        handleClose={() => setUpdateAccountStatus({})}
        dialogTitle={
          <Typography component='h2' variant='h6' fontWeight={600}>
            {updateAccountStatus.status == 'active' ? 'Enable' : 'Disable'} {cloudProvider} Account
          </Typography>
        }
        dialogContent={`Are you sure you want to ${updateAccountStatus.status == 'active' ? 'enable' : 'disable'} "${
          updateAccountStatus.name
        }" the configured ${cloudProvider} Account?`}
        handleSubmit={handleUpdateAccountStatus}
        loading={isStatusUpdating}
        open={updateAccountStatus && Object.keys(updateAccountStatus).length > 0}
      />
      <Modal
        width='md'
        open={eventGridModalOpen}
        handleClose={eventGridLoading ? () => {} : closeEventGridModal}
        title='Connect Azure Event Grid'
        loader={eventGridLoading}
      >
        {eventGridData && (
          <>
            <MarkDowns data={EVENTGRID_INSTRUCTIONS} sx={{ width: 'auto' }} />

            <Grid container mt={2} mb={2} spacing={2}>
              <Grid item xs={12}>
                <TextField
                  sx={inputSx}
                  value={eventGridData.external_id || ''}
                  size='small'
                  fullWidth
                  id='eventgrid-external-id-token'
                  label='Account Token (NudgebeeExternalId)'
                  InputProps={{
                    readOnly: true,
                    endAdornment: (
                      <InputAdornment position='end'>
                        <IconButton aria-label='copy token' onClick={() => copyToClipboard(eventGridData.external_id)} edge='end'>
                          <ContentCopy fontSize='small' />
                        </IconButton>
                      </InputAdornment>
                    ),
                  }}
                  helperText='Copy this token and paste it into the NudgebeeExternalId field.'
                />
              </Grid>
              {eventGridData.webhook_url && (
                <Grid item xs={12}>
                  <TextField
                    sx={inputSx}
                    value={eventGridData.webhook_url}
                    size='small'
                    fullWidth
                    id='eventgrid-webhook-url'
                    label='Webhook URL (NudgebeeWebhookUrl)'
                    InputProps={{
                      readOnly: true,
                      endAdornment: (
                        <InputAdornment position='end'>
                          <IconButton aria-label='copy webhook url' onClick={() => copyToClipboard(eventGridData.webhook_url)} edge='end'>
                            <ContentCopy fontSize='small' />
                          </IconButton>
                        </InputAdornment>
                      ),
                    }}
                    helperText='Copy this value and paste it into the NudgebeeWebhookUrl field.'
                  />
                </Grid>
              )}
            </Grid>

            <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
              <Grid item>
                <CustomButton id='close-eventgrid-btn' size='Medium' text='Close' variant='secondary' onClick={closeEventGridModal} />
              </Grid>
              <Grid item>
                <CustomButton
                  id='deploy-arm-template-btn'
                  size='Medium'
                  text='Deploy via Azure Portal'
                  onClick={() => window.open(eventGridData.url, '_blank', 'noopener,noreferrer')}
                />
              </Grid>
            </Grid>
          </>
        )}
      </Modal>
      <Modal
        width='sm'
        open={billingModalOpen}
        handleClose={billingLoading ? () => {} : closeBillingModal}
        title='Edit Billing Config'
        loader={billingLoading}
      >
        <Grid container spacing={2} p={2}>
          <Grid item xs={12}>
            <TextField
              sx={inputSx}
              value={billingProjectId}
              size='small'
              margin='normal'
              fullWidth
              id='billing-project-id'
              label='Billing Project ID'
              onChange={(e) => setBillingProjectId(e.target.value)}
              helperText='The GCP project containing the BigQuery billing export. Leave empty if same as service account project.'
            />
          </Grid>
          <Grid item xs={12}>
            <TextField
              sx={inputSx}
              value={billingDatasetName}
              size='small'
              margin='normal'
              fullWidth
              id='billing-dataset-name'
              label='BigQuery Dataset Name'
              required
              onChange={(e) => setBillingDatasetName(e.target.value)}
              placeholder='e.g., billing_export'
            />
          </Grid>
          <Grid item xs={12}>
            <TextField
              sx={inputSx}
              value={billingTableName}
              size='small'
              margin='normal'
              fullWidth
              id='billing-table-name'
              label='BigQuery Table Name'
              required
              onChange={(e) => setBillingTableName(e.target.value)}
              placeholder='e.g., gcp_billing_export_v1_XXXXX'
            />
          </Grid>
        </Grid>
        <Grid container spacing={2} mt={1} mb={2} justifyContent='flex-end' sx={{ button: { minWidth: '140px' }, paddingRight: '16px' }}>
          <Grid item>
            <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={closeBillingModal} disabled={billingLoading} />
          </Grid>
          <Grid item>
            <CustomButton
              size='Medium'
              text='Save'
              disabled={billingLoading || !billingDatasetName || !billingTableName}
              onClick={handleBillingSubmit}
            />
          </Grid>
        </Grid>
      </Modal>
      <Modal
        width='md'
        open={eventBridgeModalOpen}
        handleClose={eventBridgeLoading ? () => {} : closeEventBridgeModal}
        title='Connect AWS EventBridge'
        loader={eventBridgeLoading}
      >
        {eventBridgeData && (
          <>
            <MarkDowns data={EVENTBRIDGE_INSTRUCTIONS} sx={{ width: 'auto' }} />

            <Grid container mt={2} mb={2} spacing={2}>
              <Grid item xs={12}>
                <TextField
                  sx={inputSx}
                  value={eventBridgeData.external_id || ''}
                  size='small'
                  fullWidth
                  id='eventbridge-external-id-token'
                  label='Account Token (NudgebeeExternalId)'
                  InputProps={{
                    readOnly: true,
                    endAdornment: (
                      <InputAdornment position='end'>
                        <IconButton aria-label='copy token' onClick={() => copyToClipboard(eventBridgeData.external_id)} edge='end'>
                          <ContentCopy fontSize='small' />
                        </IconButton>
                      </InputAdornment>
                    ),
                  }}
                  helperText='This token is pre-filled in the CloudFormation template.'
                />
              </Grid>
            </Grid>

            <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
              <Grid item>
                <CustomButton id='close-eventbridge-btn' size='Medium' text='Close' variant='secondary' onClick={closeEventBridgeModal} />
              </Grid>
              <Grid item>
                <CustomButton
                  id='deploy-cfn-template-btn'
                  size='Medium'
                  text='Deploy via AWS Console'
                  onClick={() => window.open(eventBridgeData.url, '_blank', 'noopener,noreferrer')}
                />
              </Grid>
            </Grid>
          </>
        )}
      </Modal>
      <CfUpdateModal
        open={cfUpdateModalOpen}
        onClose={() => {
          setCfUpdateModalOpen(false);
          setCfUpdateAccountId(null);
        }}
        accountId={cfUpdateAccountId}
      />
      {webhookModalOpen && webhookModalAccount && (
        <EnableGcpWebhookModal
          key={webhookModalAccount.id}
          open
          onClose={() => {
            setWebhookModalOpen(false);
            setWebhookModalAccount(null);
            fetchWebhookStatus();
          }}
          account={webhookModalAccount}
          isAlreadyEnabled={allWebhookAccountIds.has(webhookModalAccount.id)}
          existedIntegration={allExistedIntegrations?.[String(webhookModalAccount.id)]}
        />
      )}
      <Grid container padding='5px' mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={colors.text.secondary} fontSize='16px' fontWeight={600}>
                {title}
              </Typography>
              <CloudProviderIcon cloud_provider={cloudProvider} />
            </Stack>
            {hasWriteAccess() && (
              <Stack direction='row' spacing={1}>
                {AddOrgModalComponent && addOrgButtonText && (
                  <CustomButton
                    id={`${toKebabCase(addOrgButtonText)}-btn`}
                    onClick={() => setOpenOrgModal(true)}
                    aria-label={addOrgButtonText}
                    text={addOrgButtonText}
                    variant='secondary'
                  />
                )}
                <CustomButton
                  id={`${toKebabCase(addAccountButtonText)}-btn`}
                  onClick={() => setOpenAddModal(true)}
                  aria-label={addAccountButtonText}
                  text={addAccountButtonText}
                />
              </Stack>
            )}
          </Stack>
        </Grid>
      </Grid>

      <BoxLayout2
        id={`${cloudProvider?.toLowerCase()}-integrations`}
        loading={loading}
        sharingOptions={false}
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: statusOptions,
            onSelect: handleStatusFilterChange,
            minWidth: '150px',
            label: 'By Status',
            value: selectedStatusFilter,
          },
          {
            type: 'search',
            enabled: true,
            onSelect: handleNameFilterChange,
            minWidth: '150px',
            label: 'Enter Name',
            onEnter: onEnterPress,
            value: nameInput,
          },
        ]}
      >
        <CustomTable2
          loading={loading}
          tableData={tableData}
          headers={TABLE_HEADERS}
          totalRows={totalCount || tableData.length}
          rowsPerPage={recordsPerPage}
          pageNumber={currentPage + 1}
          onPageChange={onPageChange}
        />
      </BoxLayout2>
    </>
  );
};

export default CloudAccountTile;
