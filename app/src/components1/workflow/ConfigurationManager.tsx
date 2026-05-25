import React, { useState, useEffect } from 'react';
import { Box, Tab, Tabs, Typography } from '@mui/material';
import { Add as AddIcon, Save as SaveIcon } from '@mui/icons-material';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { Text } from '@components1/common';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { snackbar } from '@components1/common/snackbarService';
import { Modal } from '@components1/common/modal';
import { FormCard, FormField } from '@components1/common/NewReusabeFormComponents';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import apiWorkflow from '@api1/workflow';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';
import { DeleteIconRed, EditNewIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

type Scope = 'tenant' | 'account';

interface ConfigurationManagerProps {
  accountId: string;
  open: boolean;
  onClose: () => void;
}

interface Config {
  id: string;
  key: string;
  value: string;
  type: string;
  labels?: any;
  metadata?: any;
  tenant_id?: string;
  account_id?: string | null;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

const ConfigurationManager: React.FC<ConfigurationManagerProps> = ({ accountId, open, onClose }) => {
  const [configs, setConfigs] = useState<Config[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [editFormOpen, setEditFormOpen] = useState<boolean>(false);
  const [deleteModalOpen, setDeleteModalOpen] = useState<boolean>(false);
  const [selectedConfig, setSelectedConfig] = useState<Config | null>(null);
  const [configToDelete, setConfigToDelete] = useState<Config | null>(null);
  // Which scope the list view is showing.
  const [viewScope, setViewScope] = useState<Scope>('account');
  // Which scope the form is writing to (defaults to current view scope).
  const [formScope, setFormScope] = useState<Scope>('account');
  const [formData, setFormData] = useState({
    key: '',
    value: '',
    type: 'config',
    labels: '',
    metadata: '',
  });

  // Returns the accountId argument the API expects for a given scope.
  const accountArgFor = (scope: Scope) => (scope === 'tenant' ? '' : accountId);

  const loadConfigs = async () => {
    if (!accountId) {
      return;
    }

    setLoading(true);
    try {
      const response: any = await apiWorkflow.listConfigs(accountArgFor(viewScope));
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }

      // `data.config_list` absence means the proxy/backend failed (e.g.
      // {"error":"...","message":"fetch failed"}); surface the error rather
      // than silently rendering an empty list.
      if (response?.data?.config_list) {
        setConfigs(response.data.config_list);
      } else if (response?.error || response?.message) {
        snackbar.error(response?.message || 'Failed to load configurations');
        setConfigs([]);
      } else {
        setConfigs([]);
      }
    } catch (error) {
      console.error('Error loading configs:', error);
      snackbar.error('Failed to load configurations');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open && accountId) {
      loadConfigs();
    }
    // viewScope intentionally included so toggling reloads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, accountId, viewScope]);

  const handleSaveConfig = async () => {
    if (!formData.key || !formData.value) {
      snackbar.error('Key and value are required');
      return;
    }

    // Check for duplicate key when creating a new config in the same scope.
    if (!selectedConfig) {
      const existingConfig = configs.find((c) => {
        const cIsTenant = !c.account_id;
        const formIsTenant = formScope === 'tenant';
        return c.key === formData.key && cIsTenant === formIsTenant;
      });
      if (existingConfig) {
        snackbar.error(`A ${formScope}-level configuration with key "${formData.key}" already exists`);
        return;
      }
    }

    setLoading(true);
    try {
      let parsedMetadata = {};
      if (formData.metadata) {
        try {
          parsedMetadata = JSON.parse(formData.metadata);
        } catch {
          snackbar.error('Invalid JSON format in metadata field. Please check your JSON syntax.');
          setLoading(false);
          return;
        }
      }

      const config = {
        id: selectedConfig?.id,
        key: formData.key,
        value: formData.value,
        type: formData.type,
        labels: formData.labels
          ? formData.labels.split(',').reduce((acc, label) => {
              const trimmed = label.trim();
              if (trimmed) {
                acc[trimmed] = trimmed;
              }
              return acc;
            }, {} as Record<string, string>)
          : {},
        metadata: parsedMetadata,
      };

      const response: any = await apiWorkflow.saveConfig(accountArgFor(formScope), config);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }
      // Verify the action actually returned the expected payload — when the
      // upstream /api/graphql proxy returns {"error":"...","message":"fetch failed"},
      // both `data` and `errors` end up undefined and parseHttpResponseBodyMessage
      // returns ''. Without this check the UI would show success on failure.
      if (!response?.data?.config_save?.id) {
        snackbar.error(response?.message || 'Failed to save configuration');
        return;
      }

      snackbar.success(selectedConfig ? 'Configuration updated successfully' : 'Configuration created successfully');
      handleCloseForm();
      loadConfigs();
    } catch (error) {
      console.error('Error saving config:', error);
      snackbar.error('Failed to save configuration');
    } finally {
      setLoading(false);
    }
  };

  const handleEditConfig = (config: Config) => {
    setSelectedConfig(config);
    setFormScope(config.account_id ? 'account' : 'tenant');
    setFormData({
      key: config.key,
      value: config.value,
      type: config.type,
      labels:
        config.labels && typeof config.labels === 'object' && !Array.isArray(config.labels)
          ? Object.keys(config.labels).join(', ')
          : Array.isArray(config.labels)
          ? config.labels.join(', ')
          : '',
      metadata: config.metadata ? JSON.stringify(config.metadata, null, 2) : '',
    });
    setEditFormOpen(true);
  };

  const handleNewConfig = () => {
    setSelectedConfig(null);
    setFormScope(viewScope);
    setFormData({
      key: '',
      value: '',
      type: 'config',
      labels: '',
      metadata: '',
    });
    setEditFormOpen(true);
  };

  const handleCloseForm = () => {
    setEditFormOpen(false);
    setSelectedConfig(null);
    setFormData({
      key: '',
      value: '',
      type: 'config',
      labels: '',
      metadata: '',
    });
  };

  const handleCloseListModal = () => {
    setEditFormOpen(false);
    onClose();
  };

  const validateJsonString = (jsonString: string): boolean => {
    if (!jsonString.trim()) {
      return true;
    }
    try {
      JSON.parse(jsonString);
      return true;
    } catch {
      return false;
    }
  };

  const handleDeleteConfig = (config: Config) => {
    setConfigToDelete(config);
    setDeleteModalOpen(true);
  };

  const handleCloseDeleteModal = () => {
    setDeleteModalOpen(false);
    setConfigToDelete(null);
  };

  const handleConfirmDelete = async () => {
    if (!configToDelete) {
      return;
    }

    setLoading(true);
    try {
      // Delete must target the row's actual scope, not the current view scope —
      // a tenant row shown under the merged "Account" view must still be
      // deleted at tenant scope.
      const deleteAccountArg = configToDelete.account_id ? accountId : '';
      const response: any = await apiWorkflow.deleteConfig(deleteAccountArg, configToDelete.key);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }
      // See save handler — guard against the {error, message} proxy-failure
      // shape that bypasses parseHttpResponseBodyMessage.
      if (!response?.data?.config_delete?.message) {
        snackbar.error(response?.message || 'Failed to delete configuration');
        return;
      }

      snackbar.success('Configuration deleted successfully');
      handleCloseDeleteModal();
      loadConfigs();
    } catch (error) {
      console.error('Error deleting config:', error);
      snackbar.error('Failed to delete configuration');
    } finally {
      setLoading(false);
    }
  };

  const getMenuItems = (): { label: string; id: number; icon: any }[] => {
    return [
      {
        label: 'Edit',
        id: 1,
        icon: EditNewIcon,
      },
      {
        label: 'Delete',
        id: 2,
        icon: DeleteIconRed,
      },
    ];
  };

  const onMenuClick = (menuItem: any, config: Config) => {
    if (menuItem.id === 1) {
      handleEditConfig(config);
    } else if (menuItem.id === 2) {
      handleDeleteConfig(config);
    }
  };

  const tableHeaders = [
    { name: 'Key', width: '18%' },
    { name: 'Scope', width: '8%' },
    { name: 'Value', width: '22%' },
    { name: 'Type', width: '8%' },
    { name: 'Labels', width: '13%' },
    { name: 'Created At', width: '13%' },
    { name: 'Updated At', width: '13%' },
    { name: 'Actions', width: '5%' },
  ];

  const tableData = configs.map((config) => [
    { component: <Text value={config.key} /> },
    { component: <CustomLabels text={config.account_id ? 'Account' : 'Tenant'} /> },
    { component: <Text value={config.value.length > 50 ? config.value.substring(0, 50) + '...' : config.value} /> },
    { component: <CustomLabels text={config.type} /> },
    {
      component: (() => {
        const labels = config.labels;
        if (!labels) {
          return <Text value='-' />;
        }

        const labelArray = typeof labels === 'object' && !Array.isArray(labels) ? Object.keys(labels) : Array.isArray(labels) ? labels : [];

        return labelArray.length > 0 ? (
          <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>
            {labelArray.slice(0, 2).map((label: string, index: number) => (
              <CustomLabels text={label} key={index} />
            ))}
            {labelArray.length > 2 && <Text value={`+${labelArray.length - 2} more`} />}
          </Box>
        ) : (
          <Text value='-' />
        );
      })(),
    },
    { component: <Datetime baseDate={new Date()} value={config.created_at} /> },
    { component: <Datetime baseDate={new Date()} value={config.updated_at} /> },
    {
      component: (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mr: '10px' }}>
          <ThreeDotsMenu menuItems={getMenuItems()} data={config} onMenuClick={onMenuClick} />
        </Box>
      ),
    },
  ]);

  return (
    <>
      {/* Configuration List Modal */}
      <Modal open={open && !editFormOpen} handleClose={handleCloseListModal} width='lg' title='Automation Configurations'>
        <Box sx={{ p: 3 }}>
          <Box sx={{ width: '100%', display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', mb: 2, gap: 2 }}>
            <Box sx={{ flex: 1, minWidth: 0 }}>
              <Tabs
                value={viewScope}
                onChange={(_, val: Scope) => setViewScope(val)}
                data-testid='config-scope-toggle'
                sx={{
                  minHeight: 36,
                  '& .MuiTab-root': {
                    textTransform: 'none',
                    fontSize: 13,
                    fontWeight: 500,
                    minHeight: 36,
                    px: 2,
                  },
                }}
              >
                <Tab value='account' label='This Account' data-testid='config-scope-account' />
                <Tab value='tenant' label='Tenant (shared)' data-testid='config-scope-tenant' />
              </Tabs>
              <Typography variant='caption' sx={{ mt: 0.75, color: 'text.secondary', display: 'block' }}>
                {viewScope === 'account'
                  ? 'Effective view: tenant-shared configs plus this account’s overrides.'
                  : 'Tenant-shared configs visible to every account in this tenant.'}
              </Typography>
            </Box>
            <CustomButton startIcon={<AddIcon />} variant='primary' onClick={handleNewConfig} disabled={loading} text='Add Config' />
          </Box>
          <CustomTable2 tableData={tableData} headers={tableHeaders} loading={loading} rowsPerPage={10} totalRows={configs.length} />
        </Box>
      </Modal>

      {/* Add/Edit Configuration Modal */}
      <Modal open={editFormOpen} handleClose={handleCloseForm} width='md' title={selectedConfig ? 'Edit Configuration' : 'Add New Configuration'}>
        <Box sx={{ p: 2 }}>
          <FormCard
            title='Configuration Details'
            description={selectedConfig ? 'Update the configuration parameters below.' : 'Enter the configuration parameters below.'}
            icon={null}
            number={1}
            columns={1}
          >
            <FormField
              label='Scope'
              description='Tenant configs are shared across all accounts; account configs override the tenant value for this account only.'
              value={formScope}
              onChange={(e: any) => setFormScope(e.target.value as Scope)}
              placeholder='Select scope'
              required={true}
              disabled={loading || !!selectedConfig}
              fieldType='autocomplete'
              options={
                [
                  { label: 'Account (this account only)', value: 'account' },
                  { label: 'Tenant (shared across accounts)', value: 'tenant' },
                ] as any
              }
              customRender={null}
              maxRows={1}
              minRows={1}
              maxLength={0}
              limitTags={0}
              minWidth='50%'
            />

            <FormField
              label='Key'
              description='Unique identifier for this configuration'
              value={formData.key}
              onChange={(e: any) => setFormData({ ...formData, key: e.target.value })}
              placeholder='Enter configuration key'
              required={true}
              disabled={loading}
              fieldType='textfield'
              error={!formData.key ? 'Key is required' : ''}
              onSelect={() => {}}
              customRender={null}
              maxRows={1}
              minRows={1}
              maxLength={100}
              limitTags={0}
              minWidth=''
            />

            <FormField
              label='Value'
              description='The configuration value (supports multi-line text)'
              value={formData.value}
              onChange={(e: any) => setFormData({ ...formData, value: e.target.value })}
              placeholder='Enter configuration value'
              required={true}
              disabled={loading}
              fieldType='textarea'
              rows={3}
              maxRows={6}
              minRows={2}
              error={!formData.value ? 'Value is required' : ''}
              onSelect={() => {}}
              customRender={null}
              maxLength={5000}
              limitTags={0}
              minWidth=''
            />

            <FormField
              label='Type'
              description='Configuration type: config for regular values, secret for sensitive data'
              value={formData.type}
              onChange={(e: any) => setFormData({ ...formData, type: e.target.value })}
              placeholder='Select configuration type'
              disabled={loading}
              fieldType='autocomplete'
              options={
                [
                  { label: 'Config', value: 'config' },
                  { label: 'Secret', value: 'secret' },
                ] as any
              }
              customRender={null}
              maxRows={1}
              minRows={1}
              maxLength={0}
              limitTags={0}
              minWidth='50%'
            />

            <FormField
              label='Labels'
              description='Comma-separated labels for categorizing this configuration'
              value={formData.labels}
              onChange={(e: any) => setFormData({ ...formData, labels: e.target.value })}
              placeholder='label1, label2, label3'
              disabled={loading}
              fieldType='textfield'
              onSelect={() => {}}
              customRender={null}
              maxRows={1}
              minRows={1}
              maxLength={200}
              limitTags={0}
              minWidth=''
            />

            <FormField
              label='Metadata'
              description='Additional metadata in JSON format'
              value={formData.metadata}
              onChange={(e: any) => setFormData({ ...formData, metadata: e.target.value })}
              placeholder='{"key": "value", "description": "Configuration metadata"}'
              disabled={loading}
              fieldType='textarea'
              rows={3}
              maxRows={6}
              minRows={2}
              onSelect={() => {}}
              customRender={null}
              maxLength={2000}
              limitTags={0}
              minWidth=''
              error={formData.metadata && !validateJsonString(formData.metadata) ? 'Invalid JSON format' : ''}
            />
          </FormCard>

          <Box sx={{ display: 'flex', gap: 1, mt: 2, justifyContent: 'flex-end' }}>
            <CustomButton onClick={handleCloseForm} disabled={loading} text='Cancel' variant='secondary' />
            <CustomButton variant='primary' startIcon={<SaveIcon />} onClick={handleSaveConfig} disabled={loading} text='Save Configuration' />
          </Box>
        </Box>
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal open={deleteModalOpen} handleClose={handleCloseDeleteModal} width='sm' title='Delete Configuration'>
        <Box sx={{ p: 3 }}>
          <FormCard
            title='Confirm Deletion'
            description={
              configToDelete && !configToDelete.account_id
                ? 'This is a tenant-level configuration shared across all accounts. Deleting it will affect every account in the tenant.'
                : 'This action cannot be undone. Are you sure you want to delete this configuration?'
            }
            icon={null}
            number=''
            columns={1}
          >
            <Box sx={{ mb: 2 }}>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                <Box sx={{ display: 'flex', gap: 1 }}>
                  <Text value='Key:' />
                  <Text value={configToDelete?.key || ''} />
                </Box>
                <Box sx={{ display: 'flex', gap: 1 }}>
                  <Text value='Scope:' />
                  <CustomLabels text={configToDelete?.account_id ? 'Account' : 'Tenant'} />
                </Box>
                <Box sx={{ display: 'flex', gap: 1 }}>
                  <Text value='Type:' />
                  <CustomLabels text={configToDelete?.type || ''} />
                </Box>
                <Box sx={{ display: 'flex', gap: 1 }}>
                  <Text value='Value:' />
                  <Text
                    value={
                      configToDelete?.value
                        ? configToDelete.value.length > 50
                          ? configToDelete.value.substring(0, 50) + '...'
                          : configToDelete.value
                        : ''
                    }
                  />
                </Box>
              </Box>
            </Box>
          </FormCard>

          <Box sx={{ display: 'flex', gap: 1, mt: 2, justifyContent: 'flex-end' }}>
            <CustomButton onClick={handleCloseDeleteModal} disabled={loading} text='Cancel' variant='secondary' />
            <CustomButton
              variant='primary'
              startIcon={<SafeIcon src={DeleteIconRed} alt={'delete'} id={'delete-config'} />}
              onClick={handleConfirmDelete}
              disabled={loading}
              text='Delete Configuration'
            />
          </Box>
        </Box>
      </Modal>
    </>
  );
};

export default ConfigurationManager;
