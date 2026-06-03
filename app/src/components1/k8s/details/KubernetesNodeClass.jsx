import React, { useEffect, useState, useCallback } from 'react';
import k8sApi from '@api1/kubernetes';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Typography, Box } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import { Input } from '@components1/ds/Input';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { yaml } from '@codemirror/lang-yaml';
import Datetime from '@common-new/format/Datetime';
import { action } from 'src/utils/actionStyles';
import { hasWriteAccess } from '@lib/auth';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { Modal } from '@common-new/modal';
import { Button } from '@components1/ds/Button';
import yaml1 from 'js-yaml';
import Text from '@common-new/format/Text';
import { useData } from '@context/DataContext';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import PropTypes from 'prop-types';
import { DeleteIconRed as deleteIcon, PlusIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { toast as snackbar } from '@components1/ds/Toast';
import { compareVersions } from 'src/utils/common';
import CustomTable from '@common-new/tables/CustomTable2';
import { ds } from '@utils/colors';

const KubernetesNodeClass = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [isEditing, setIsEditing] = useState(false);
  const [isCreating, setIsCreating] = useState(false);
  const [selectedNodeClassData, setSelectedNodeClassData] = useState({});
  const [condition, setCondition] = useState('auto-config');
  const [yamlOutput, setYamlOutput] = useState('');
  const [name, setName] = useState('');
  const [validationMessage, setValidationMessage] = useState('');
  const [clusterName, setClusterName] = useState('');
  const [formSubmitting, setFormSubmitting] = useState(false);
  const [errors, setErrors] = useState({
    name: '',
    clusterName: '',
    amiFamily: '',
  });
  const [amiFamily, setAMIFamily] = useState('');
  const [isEKSCluster, setIsEKSCluster] = useState(false);
  const [newLabel, setNewLabel] = useState({
    key: '',
    value: '',
  });
  const [newBlockDeviceMappings, setNewBlockDeviceMappings] = useState({});

  const { selectedCluster } = useData();

  useEffect(() => {
    setIsEKSCluster(selectedCluster?.k8s_provider === 'EKS');
  }, [selectedCluster]);

  const getMenuItems = () => {
    if (!hasWriteAccess(accountId)) {
      return [];
    }
    return [
      {
        label: 'Edit',
        id: 0,
      },
    ];
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setIsEditing(true);
      setSelectedNodeClassData(data);
      setYamlOutput(yaml1.dump(data));
    }
  };

  useEffect(() => {
    if (!accountId || !selectedCluster?.value || !selectedCluster?.k8s_provider) {
      return;
    }
    setData([]);
    setTotalCount(0);
    listNodeClass();
  }, [selectedCluster?.value, selectedCluster?.k8s_provider]);

  const listNodeClass = useCallback(() => {
    const isKarpenterEnable =
      ((selectedCluster?.agent?.connection_status?.autoScalerEnabled && selectedCluster?.agent?.connection_status?.autoScalerType === 'karpenter') ||
        selectedCluster?.agent?.connection_status?.karpenterEnabled) ??
      false;
    let karpenterVersion = 'v1beta1';
    if (
      compareVersions(selectedCluster?.agent?.connection_status.autoScalerVersion ?? selectedCluster?.agent?.connection_status.karpenterVersion, '1')
    ) {
      karpenterVersion = 'v1';
    }
    if (isKarpenterEnable) {
      setLoading(true);
      k8sApi
        .relayForwardRequest(getRelayServerPayloadForNodeClass(karpenterVersion))
        .then((res) => handleNodeClassResponse(res))
        .finally(() => {
          setLoading(false);
        });
    }
  }, [selectedCluster?.value]);

  const getRelayServerPayloadForNodeClass = (karpenterVersion) => ({
    no_sinks: true,
    cache: false,
    body: {
      account_id: selectedCluster?.value || accountId,
      action_name: 'get_resource',
      action_params: {
        group: selectedCluster?.k8s_provider == 'EKS' ? 'karpenter.k8s.aws' : 'karpenter.azure.com',
        version: selectedCluster?.k8s_provider == 'EKS' ? karpenterVersion : 'v1alpha2',
        resource_type: selectedCluster?.k8s_provider == 'EKS' ? 'ec2nodeclasses' : 'aksnodeclasses',
        all_namespaces: true,
      },
    },
  });

  const handleNodeClassResponse = (res) => {
    let data = extractData(res);

    if (data) {
      try {
        data = parseData(data);
      } catch (e) {
        console.error('Error parsing data', e);
      }
    }

    if (typeof data === 'string') {
      data = JSON.parse(data);
    }

    if (data) {
      const tableData = transformNodeClassData(data);
      setData(tableData ?? []);
      setTotalCount(tableData?.length);
    }
  };

  const extractData = (res) => res?.data?.findings?.[0]?.evidence?.[0]?.data;

  const parseData = (data) => JSON.parse(data)[0].data;

  const transformNodeClassData = (data) => {
    const items = data;
    return items?.map((item) => {
      const deepCopyItem = JSON.parse(JSON.stringify(item));
      delete deepCopyItem.metadata.managedFields;
      return createNodeClassTableRow(deepCopyItem);
    });
  };

  const createNodeClassTableRow = (item) => [
    {
      component: <Text value={item.kind} />,
      drilldownQuery: item,
    },
    {
      component: <Text value={item.metadata.name} />,
    },
    {
      component: <Datetime value={item.metadata.creationTimestamp} />,
    },
    {
      component: hasWriteAccess(accountId) ? (
        <Box display={'flex'} justifyContent={'flex-end'}>
          <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems()} data={item} onMenuClick={onMenuClick} />
        </Box>
      ) : (
        <></>
      ),
    },
  ];

  const handleClose = () => {
    setSelectedNodeClassData({});
    setValidationMessage('');
    setName('');
    setClusterName('');
    setErrors({});
    setAMIFamily('');
    setIsEditing(false);
    setIsCreating(false);
    setFormSubmitting(false);
    setNewBlockDeviceMappings({});
  };

  const handleUpdates = (key, value) => {
    const updateFunctions = {
      name: updateName,
      'cluster-name': updateNodeClassData(setClusterName, 'cluster-name'),
      'ami-family': updateNodeClassData(setAMIFamily, 'ami-family'),
    };

    const updateFunction = updateFunctions[key];
    if (updateFunction) {
      updateFunction(value);
    }
  };

  const updateName = (value) => {
    setName(value);
    if (value.trim() === '') {
      setErrors((prevErrors) => ({
        ...prevErrors,
        ['name']: `Name is required`,
      }));
    } else {
      setErrors((prevErrors) => ({
        ...prevErrors,
        ['name']: '',
      }));
    }
    setSelectedNodeClassData((prevNodeClass) => ({
      ...prevNodeClass,
      metadata: { ...prevNodeClass.metadata, name: value },
    }));
  };

  const updateNodeClassData = (setState, key) => (value) => {
    setState(value);
    setSelectedNodeClassData((prevNodeClassData) => {
      const updateNodeClassData = { ...prevNodeClassData };
      if (!updateNodeClassData.spec) {
        updateNodeClassData.spec = {};
      }
      if (key == 'ami-family') {
        if (isEKSCluster) {
          updateNodeClassData.spec.amiFamily = value;
        } else {
          updateNodeClassData.spec.imageFamily = value;
        }
      } else if (key == 'cluster-name' && isEKSCluster) {
        updateNodeClassData.spec.role = `KarpenterNodeRole-${value}`;
        updateNodeClassData.spec.subnetSelectorTerms = [
          {
            tags: {
              'karpenter.sh/discovery': `${value}`,
            },
          },
        ];
        updateNodeClassData.spec.securityGroupSelectorTerms = [
          {
            tags: {
              'karpenter.sh/discovery': `${value}`,
            },
          },
        ];
      }
      return updateNodeClassData;
    });
  };

  const handleSubmit = () => {
    if (!validateInputs()) {
      return;
    }
    setFormSubmitting(true);
    const processedData = {
      ...selectedNodeClassData,
      spec: {
        ...selectedNodeClassData.spec,
        blockDeviceMappings: selectedNodeClassData.spec.blockDeviceMappings.map((mapping) => {
          const updatedEbs = { ...mapping.ebs };
          if (mapping.ebs.volumeSize) {
            updatedEbs.volumeSize = `${mapping.ebs.volumeSize}Gi`;
          } else {
            delete updatedEbs.volumeSize;
          }
          return {
            ...mapping,
            ebs: updatedEbs,
          };
        }),
      },
    };
    const className = isEKSCluster ? 'ec2nodeclass' : 'aksnodeclass';
    const data = createRequestData(className, processedData);
    k8sApi
      .relayForwardRequest(data)
      .then((res) => handleResponse(res))
      .catch(() => handleError())
      .finally(() => setFormSubmitting(false));
  };

  const validateInputs = () => {
    if (!name || name.trim() === '') {
      setErrors((prevErrors) => ({
        ...prevErrors,
        name: 'Name is required',
      }));
      return false;
    }
    if ((!clusterName || clusterName.trim() === '') && isEKSCluster && isCreating) {
      setErrors((prevErrors) => ({
        ...prevErrors,
        clusterName: 'Cluster Name is required',
      }));
      return false;
    }
    if (!amiFamily) {
      setErrors((prevErrors) => ({
        ...prevErrors,
        amiFamily: 'AMI Family is required',
      }));
      return false;
    }
    return true;
  };

  const createRequestData = (className, processedData) => ({
    no_sinks: true,
    body: {
      account_id: accountId,
      action_name: isEditing ? 'replace_workload' : 'create_workload',
      action_params: {
        name: processedData.metadata.name,
        namespace: '',
        kind: isEKSCluster ? 'EC2NodeClass' : 'AKSNodeClass',
        [className]: processedData,
      },
      origin: 'Nudgebee UI',
    },
  });

  const handleResponse = (res) => {
    if (res?.data?.success) {
      snackbar.success(`${selectedNodeClassData.metadata?.name} is ${isEditing ? 'updated' : 'created'} successfully`);
      handleClose();
      listNodeClass();
    } else {
      handleError();
    }
  };

  const handleError = () => {
    snackbar.error(`Failed to ${isEditing ? 'update' : 'create'} ${selectedNodeClassData.metadata?.name} node class`);
  };

  useEffect(() => {
    if (isEditing && selectedNodeClassData && Object.keys(selectedNodeClassData).length > 0) {
      setName(selectedNodeClassData.metadata.name ?? '');
      const amiFamily = isEKSCluster ? selectedNodeClassData.spec.amiFamily : selectedNodeClassData.spec.imageFamily;
      setAMIFamily(amiFamily ?? '');
    }
  }, [isEditing]);

  const handleTabClick = (type) => {
    if (type == 'yaml') {
      setCondition('yaml');
      setValidationMessage('YAML is valid');
      setYamlOutput(yaml1.dump(selectedNodeClassData));
    }
  };

  const handleLabelChange = (field, value) => {
    setNewLabel((prevNewLabel) => ({
      ...prevNewLabel,
      [field]: value,
    }));
  };

  const handleLabelCreate = () => {
    if (newLabel.key && newLabel.value) {
      setSelectedNodeClassData((prevNodeClass) => {
        const updatedNodeClass = { ...prevNodeClass };
        if (!updatedNodeClass.spec.tags) {
          updatedNodeClass.spec.tags = {};
        }
        updatedNodeClass.spec.tags = {
          ...updatedNodeClass.spec.tags,
          [newLabel.key]: newLabel.value,
        };
        return updatedNodeClass;
      });
      setNewLabel({ key: '', value: '' });
    }
  };

  const handleDelete = (key) => {
    setSelectedNodeClassData((prevNodeClass) => {
      const updatedNodeClass = { ...prevNodeClass };
      const labels = { ...updatedNodeClass.spec.tags };
      delete labels[key];
      updatedNodeClass.spec.tags = labels;
      return updatedNodeClass;
    });
  };

  const handleDeleteBlockDeviceMapping = (indexToDelete) => {
    setSelectedNodeClassData((prevNodeClass) => {
      const updatedNodeClass = { ...prevNodeClass };
      updatedNodeClass.spec.blockDeviceMappings.splice(indexToDelete, 1);
      return updatedNodeClass;
    });
  };

  const handleNewBlockDeviceMappingsChange = (field, value) => {
    setNewBlockDeviceMappings((prevNewBlockDeviceMapping) => {
      if (field === 'encrypted' && !value) {
        return {
          ...prevNewBlockDeviceMapping,
          ebs: {
            ...prevNewBlockDeviceMapping.ebs,
            encrypted: value,
            kmsKeyID: '',
          },
        };
      }
      if (['throughput', 'iops', 'volumeSize'].includes(field)) {
        value = parseInt(value, 10);
      }
      if (field === 'deviceName') {
        return {
          ...prevNewBlockDeviceMapping,
          deviceName: value,
        };
      }
      return {
        ...prevNewBlockDeviceMapping,
        ebs: {
          ...prevNewBlockDeviceMapping.ebs,
          [field]: value,
        },
      };
    });
  };

  const handleNewBlockDeviceMapping = () => {
    setSelectedNodeClassData((prevNodeClass) => {
      const updatedNodeClass = { ...prevNodeClass };
      if (!updatedNodeClass.spec) {
        updatedNodeClass.spec = {};
      }
      if (!updatedNodeClass.spec.blockDeviceMappings) {
        updatedNodeClass.spec.blockDeviceMappings = [];
      }
      updatedNodeClass.spec.blockDeviceMappings.push(newBlockDeviceMappings);
      return updatedNodeClass;
    });
    setNewBlockDeviceMappings({});
  };

  return (
    <>
      <Modal width='md' open={isEditing || isCreating} handleClose={() => handleClose()} title={'Node Class Configuration'} loader={formSubmitting}>
        <Box
          sx={{
            p: 'var(--ds-space-4) var(--ds-space-5)',
            borderBottom: '1px solid var(--ds-blue-400)',
            boxShadow: '0px 2px 12px 2px #00000014',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-3)',
            marginBottom: 'var(--ds-space-4)',
          }}
        >
          <Button tone={condition === 'auto-config' ? 'primary' : 'secondary'} fullWidth onClick={() => setCondition('auto-config')}>
            Auto Config
          </Button>
          <Button tone={condition === 'yaml' ? 'primary' : 'secondary'} fullWidth onClick={() => handleTabClick('yaml')}>
            Manual Config (Yaml)
          </Button>
        </Box>
        {condition == 'yaml' && (
          <>
            <Box sx={{ p: 'var(--ds-space-5) 0px', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
              <CodeMirror
                value={yamlOutput}
                height='300px'
                extensions={[yaml()]}
                onChange={(value) => {
                  setYamlOutput(value);
                  try {
                    setSelectedNodeClassData(yaml1.load(value));
                    setValidationMessage('YAML is valid');
                  } catch (error) {
                    setValidationMessage('Invalid YAML: ' + error.message);
                  }
                }}
                editable={true}
                style={{
                  border: `2px solid ${validationMessage.startsWith('YAML is valid') ? ds.green[500] : ds.red[500]}`,
                  borderRadius: 'var(--ds-radius-lg)',
                }}
              />
            </Box>
            <Typography>{validationMessage}</Typography>
          </>
        )}
        <Box sx={{ p: 'var(--ds-space-5) var(--ds-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          {condition == 'auto-config' && (
            <>
              <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                <Input
                  value={name}
                  size='sm'
                  id='name'
                  label='Name'
                  placeholder='Enter Name'
                  onChange={(value) => {
                    handleUpdates('name', value);
                    setErrors((prevErrors) => ({
                      ...prevErrors,
                      name: '',
                    }));
                  }}
                  error={errors.name || undefined}
                />
              </Box>
              {isCreating && isEKSCluster ? (
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  <Box sx={{ width: ds.space.mul(0, 118) }}>
                    <Input
                      value={clusterName}
                      size='sm'
                      id='clusterName'
                      label='Cluster Name'
                      placeholder='Enter Cluster Name'
                      onChange={(value) => {
                        handleUpdates('cluster-name', value);
                        setErrors((prevErrors) => ({
                          ...prevErrors,
                          clusterName: '',
                        }));
                      }}
                      error={errors.clusterName || undefined}
                    />
                  </Box>
                  <Typography sx={{ marginLeft: ds.space[2], color: ds.gray[500] }}>Name of Kubernetes Cluster of this account</Typography>
                </Box>
              ) : null}
              <FilterDropdownButton
                id='ami-family'
                label='AMI Family'
                value={amiFamily}
                options={
                  isEKSCluster
                    ? [
                        { label: 'Amazon Linux 2', value: 'AL2' },
                        { label: 'Bottlerocket', value: 'Bottlerocket' },
                        { label: 'Ubuntu', value: 'Ubuntu' },
                        { label: 'Windows2019', value: 'Windows2019' },
                        { label: 'Windows2022', value: 'Windows2022' },
                      ]
                    : [
                        { label: 'Ubuntu2204', value: 'Ubuntu2204' },
                        { label: 'AzureLinux', value: 'AzureLinux' },
                      ]
                }
                onSelect={(e) => {
                  handleUpdates('ami-family', e.target.value);
                  setErrors((prevErrors) => ({
                    ...prevErrors,
                    amiFamily: '',
                  }));
                }}
                sx={{ width: ds.space.mul(0, 118), height: ds.space.mul(0, 20) }}
              />
              {errors.amiFamily ? <Typography sx={{ color: ds.red[500] }}>{errors.amiFamily}</Typography> : null}
              {isEKSCluster && (
                <>
                  <Typography className='notes'>
                    # Adds tags to all resources it creates, including EC2 Instances, EBS volumes, and Launch Templates.
                  </Typography>

                  {selectedNodeClassData?.spec?.tags && Object.keys(selectedNodeClassData.spec.tags)?.length > 0 ? (
                    Object.entries(selectedNodeClassData.spec.tags).map(([key, value], _index) => (
                      <Box key={`${key}-box`} display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                        <Box sx={{ flex: 1 }}>
                          <Input label='Key' value={key} disabled size='sm' onChange={() => {}} />
                        </Box>
                        <Box sx={{ flex: 1 }}>
                          <Input label='Value' value={value} disabled size='sm' onChange={() => {}} />
                        </Box>
                        <Box sx={{ display: 'inline-flex', ml: ds.space[2] }}>
                          <Button
                            tone='secondary'
                            size='sm'
                            composition='icon-only'
                            aria-label='Delete'
                            onClick={() => handleDelete(key)}
                            icon={<SafeIcon src={deleteIcon} alt='delete' />}
                          />
                        </Box>
                      </Box>
                    ))
                  ) : (
                    <Typography>No Labels available.</Typography>
                  )}

                  <Box display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                    <Box sx={{ flex: 1 }}>
                      <Input label='Key' value={newLabel.key} size='sm' onChange={(value) => handleLabelChange('key', value)} />
                    </Box>
                    <Box sx={{ flex: 1 }}>
                      <Input label='Value' value={newLabel.value} size='sm' onChange={(value) => handleLabelChange('value', value)} />
                    </Box>
                    <Box sx={{ display: 'inline-flex', ml: 1 }}>
                      <Button
                        tone='secondary'
                        size='sm'
                        composition='icon-only'
                        aria-label='Add'
                        disabled={!newLabel.key || !newLabel.value}
                        onClick={handleLabelCreate}
                        icon={<SafeIcon src={PlusIcon} alt='add field' />}
                      />
                    </Box>
                  </Box>
                  {selectedNodeClassData?.spec?.blockDeviceMappings && selectedNodeClassData?.spec?.blockDeviceMappings?.length > 0 ? (
                    selectedNodeClassData.spec?.blockDeviceMappings.map((blockDeviceMapping, index) => (
                      <Box
                        key={`${blockDeviceMapping.deviceName}-box`}
                        display='grid'
                        alignItems='center'
                        gridTemplateColumns={'repeat(5,auto)'}
                        gap={ds.space[2]}
                      >
                        <Input label='Device Name' value={blockDeviceMapping.deviceName} disabled size='sm' onChange={() => {}} />
                        <Input label='Volume Size' value={String(blockDeviceMapping.ebs?.volumeSize ?? '')} disabled size='sm' onChange={() => {}} />
                        <Input label='Volume Type' value={blockDeviceMapping.ebs?.volumeType ?? ''} disabled size='sm' onChange={() => {}} />
                        <Input label='iops' value={String(blockDeviceMapping.ebs?.iops ?? '')} disabled size='sm' onChange={() => {}} />
                        <Input label='Encrypted' value={String(blockDeviceMapping.ebs?.encrypted ?? '')} disabled size='sm' onChange={() => {}} />
                        <Input label='kmsKeyID' value={blockDeviceMapping.ebs?.kmsKeyID ?? ''} disabled size='sm' onChange={() => {}} />
                        <Input
                          label='deleteOnTermination'
                          value={String(blockDeviceMapping.ebs?.deleteOnTermination ?? '')}
                          disabled
                          size='sm'
                          onChange={() => {}}
                        />
                        <Input label='snapshotID' value={blockDeviceMapping.ebs?.snapshotID ?? ''} disabled size='sm' onChange={() => {}} />
                        <Input label='throughput' value={String(blockDeviceMapping.ebs?.throughput ?? '')} disabled size='sm' onChange={() => {}} />
                        <Button
                          tone='secondary'
                          size='sm'
                          composition='icon-only'
                          aria-label='Delete'
                          onClick={() => {
                            handleDeleteBlockDeviceMapping(index);
                          }}
                          icon={<SafeIcon src={deleteIcon} alt='delete' />}
                        />
                      </Box>
                    ))
                  ) : (
                    <Typography>No Block Device Mappings available.</Typography>
                  )}
                  <Box display='grid' alignItems='center' gridTemplateColumns={'repeat(5,auto)'} gap={ds.space[2]}>
                    <Input
                      label='Device Name'
                      value={newBlockDeviceMappings.deviceName ?? ''}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('deviceName', value)}
                      size='sm'
                    />
                    <Input
                      label='Volume Size'
                      value={String(newBlockDeviceMappings.ebs?.volumeSize ?? '')}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('volumeSize', value)}
                      type='number'
                      size='sm'
                      suffix='Gi'
                    />
                    <Input
                      label='Volume Type'
                      value={newBlockDeviceMappings.ebs?.volumeType ?? ''}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('volumeType', value)}
                      size='sm'
                    />
                    <Input
                      label='iops'
                      value={String(newBlockDeviceMappings.ebs?.iops ?? '')}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('iops', value)}
                      size='sm'
                      type='number'
                    />
                    <Checkbox
                      checked={!!newBlockDeviceMappings.ebs?.encrypted}
                      onChange={(next) => handleNewBlockDeviceMappingsChange('encrypted', next)}
                      label='Encrypted'
                    />
                    <Input
                      label='KMS Key ID'
                      value={newBlockDeviceMappings.ebs?.kmsKeyID ?? ''}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('kmsKeyID', value)}
                      size='sm'
                      disabled={!newBlockDeviceMappings.ebs?.encrypted}
                    />
                    <Checkbox
                      checked={!!newBlockDeviceMappings.ebs?.deleteOnTermination}
                      onChange={(next) => handleNewBlockDeviceMappingsChange('deleteOnTermination', next)}
                      label='Delete On Termination'
                    />
                    <Input
                      label='Snapshot ID'
                      value={newBlockDeviceMappings.ebs?.snapshotID ?? ''}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('snapshotID', value)}
                      size='sm'
                    />
                    <Input
                      label='Throughput'
                      value={String(newBlockDeviceMappings.ebs?.throughput ?? '')}
                      onChange={(value) => handleNewBlockDeviceMappingsChange('throughput', value)}
                      size='sm'
                      type='number'
                    />
                    <Button
                      tone='secondary'
                      size='sm'
                      composition='icon-only'
                      aria-label='Add'
                      onClick={handleNewBlockDeviceMapping}
                      icon={<SafeIcon src={PlusIcon} alt='add field' />}
                    />
                  </Box>
                </>
              )}
            </>
          )}
        </Box>
        <Box
          display='flex'
          alignItems='center'
          justifyContent='flex-end'
          gap={ds.space[3]}
          p={`${ds.space[4]} ${ds.space[5]}`}
          sx={{ borderTop: '0.5px solid var(--ds-gray-200)', '& button': { minWidth: ds.space.mul(0, 70) } }}
        >
          <Button tone='secondary' size='md' onClick={() => handleClose()} disabled={formSubmitting}>
            Cancel
          </Button>
          <Button size='md' onClick={() => handleSubmit()} disabled={formSubmitting}>
            {isEditing ? 'Update' : 'Create'} Node Class
          </Button>
        </Box>
      </Modal>
      <ListingLayout id='auto-scaler-box'>
        <ListingLayout.Toolbar
          actions={
            hasWriteAccess() ? (
              <Button
                size='md'
                onClick={() => {
                  setIsCreating(true);
                  setSelectedNodeClassData({
                    apiVersion: isEKSCluster ? 'karpenter.k8s.aws/v1beta1' : 'karpenter.azure.com/v1alpha2',
                    kind: isEKSCluster ? 'EC2NodeClass' : 'AKSNodeClass',
                    spec: {},
                  });
                }}
              >
                Create New Node Class
              </Button>
            ) : null
          }
        />
        <ListingLayout.Body>
          <CustomTable
            id={'auto-scaler-node-class'}
            headers={['Kind', 'Name', { name: 'Time', width: '40%' }, '']}
            tableData={data}
            expandable={{
              tabs: [
                {
                  text: 'Details',
                  componentFn: function (_accountId, drilldownQuery, _row) {
                    return AutoScalerDetailJSONFn(drilldownQuery);
                  },
                },
              ],
            }}
            rowsPerPage={totalCount}
            totalRows={totalCount}
            showExpandable={true}
            loading={loading}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

const AutoScalerDetailJSONFn = (drilldownQuery) => {
  if (drilldownQuery && Object.keys(drilldownQuery).length > 0) {
    return (
      <CodeMirror
        value={JSON.stringify(drilldownQuery, null, 2)}
        height='300px'
        extensions={[json(), EditorView.lineWrapping]}
        editable={false}
        style={{
          border: `1px solid ${ds.gray[400]}`,
        }}
      />
    );
  }
  return <Typography>No Data Available</Typography>;
};

KubernetesNodeClass.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesNodeClass;
