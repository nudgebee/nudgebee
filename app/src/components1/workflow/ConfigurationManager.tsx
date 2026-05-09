import React, { useState, useEffect } from 'react';
import { Box } from '@mui/material';
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
  account_id: string;
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
  const [formData, setFormData] = useState({
    key: '',
    value: '',
    type: 'config',
    labels: '',
    metadata: '',
  });

  const loadConfigs = async () => {
    if (!accountId) {
      return;
    }

    setLoading(true);
    try {
      const response: any = await apiWorkflow.listConfigs(accountId);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }

      if (response?.data?.config_list) {
        setConfigs(response.data.config_list);
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
  }, [open, accountId]);

  const handleSaveConfig = async () => {
    if (!formData.key || !formData.value) {
      snackbar.error('Key and value are required');
      return;
    }

    // Check for duplicate key when creating a new config
    if (!selectedConfig) {
      const existingConfig = configs.find((config) => config.key === formData.key);
      if (existingConfig) {
        snackbar.error(`A configuration with key "${formData.key}" already exists`);
        return;
      }
    }

    setLoading(true);
    try {
      // Validate and parse metadata JSON
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

      const response: any = await apiWorkflow.saveConfig(accountId, config);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
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
    } // Empty string is valid
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
      const response: any = await apiWorkflow.deleteConfig(accountId, configToDelete.key);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
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
    { name: 'Key', width: '20%' },
    { name: 'Value', width: '25%' },
    { name: 'Type', width: '10%' },
    { name: 'Labels', width: '15%' },
    { name: 'Created At', width: '15%' },
    { name: 'Updated At', width: '15%' },
    { name: 'Actions', width: '10%' },
  ];

  const tableData = configs.map((config) => [
    { component: <Text value={config.key} /> },
    { component: <Text value={config.value.length > 50 ? config.value.substring(0, 50) + '...' : config.value} /> },
    { component: <CustomLabels text={config.type} /> },
    {
      component: (() => {
        const labels = config.labels;
        if (!labels) {
          return <Text value='-' />;
        }

        // Handle both object format (expected) and array format (legacy)
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
          <Box sx={{ width: '100%', display: 'flex', justifyContent: 'flex-end', mb: 2 }}>
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
            description='This action cannot be undone. Are you sure you want to delete this configuration?'
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
