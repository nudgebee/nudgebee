import React, { useEffect, useState, useCallback } from 'react';
import k8sApi from '@api1/kubernetes';
import { ListingLayout } from '@components1/ds/ListingLayout';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { Typography, Box } from '@mui/material';
import { Input } from '@components1/ds/Input';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { yaml } from '@codemirror/lang-yaml';
import Datetime from '@common-new/format/Datetime';
import { action } from 'src/utils/actionStyles';
import { hasWriteAccess } from '@lib/auth';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { Modal } from '@common-new/modal';
import yaml1 from 'js-yaml';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import {
  awsInstanceCategory,
  awsInstanceFamily,
  awsInstanceZone,
  awsInstances,
  azureInstanceFamily,
  azureInstanceZone,
  compareVersions,
  safeJSONParse,
} from 'src/utils/common';
import { useData } from '@context/DataContext';
import PropTypes from 'prop-types';
import { convertToGB } from '@lib/formatter';
import { DeleteIconRed as deleteIcon, PlusIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { Button as DsButton } from '@components1/ds/Button';
import { snackbar } from '@components1/common/snackbarService';
import { ds } from 'src/utils/colors';

const KubernetesAutoScalerNodePool = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [isEditing, setIsEditing] = useState(false);
  const [isCreating, setIsCreating] = useState(false);
  const [selectedNodePoolData, setSelectedNodePoolData] = useState({});
  const [condition, setCondition] = useState('auto-config');
  const [yamlOutput, setYamlOutput] = useState('');
  const [name, setName] = useState('');
  const [nodeClassRef, setNodeClassRef] = useState('');
  const [capacityType, setCapacityType] = useState([]);
  const [instanceType, setInstanceType] = useState([]);
  const [instanceCategory, setInstanceCategory] = useState([]);
  const [instanceFamily, setInstanceFamily] = useState([]);
  const [instanceCPU, setInstanceCPU] = useState([]);
  const [instanceZone, setInstanceZone] = useState([]);
  const [validationMessage, setValidationMessage] = useState('');
  const [cpuLimit, setCPULimit] = useState();
  const [memoryLimit, setMemoryLimit] = useState();
  const [formSubmitting, setFormSubmitting] = useState(false);
  const [provider, setProvider] = useState('');
  const [errors, setErrors] = useState({
    name: '',
    nodeClassRef: '',
  });
  const [consolidationPolicy, setConsolidationPolicy] = useState('');
  const [nodeBudget, setNodeBudget] = useState('');
  const [weight, setWeight] = useState(0);
  const [consolidateAfter, setConsolidateAfter] = useState('');
  const [nodeClasses, setNodeClasses] = useState([]);
  const [expireAfter, setExpireAfter] = useState('');
  const [instanceArc, setInstanceArc] = useState([]);
  const [newLabel, setNewLabel] = useState({
    key: '',
    value: '',
  });
  const [newTaints, setNewTaints] = useState({});
  const [newStartupTaints, setNewStartupTaints] = useState({});
  const [consolidationPolicies, setConsolidationPolicies] = useState(['WhenUnderutilized', 'WhenEmpty']);
  const [isLoadingNodeClass, setIsLoadingNodeClass] = useState(false);
  const { selectedCluster } = useData();

  useEffect(() => {
    setProvider(selectedCluster?.k8s_provider ?? '');
  }, [selectedCluster]);

  const listNodeClass = useCallback(() => {
    const isKarpenterEnable =
      ((selectedCluster?.agent?.connection_status?.autoScalerEnabled && selectedCluster?.agent?.connection_status?.autoScalerType === 'karpenter') ||
        selectedCluster?.agent?.connection_status?.karpenterEnabled) ??
      false;

    if (isKarpenterEnable || isCreating || isEditing) {
      setIsLoadingNodeClass(true);
      let karpenterVersion = 'v1beta1';
      if (compareVersions(selectedCluster?.agent?.connection_status.autoScalerVersion, '1')) {
        karpenterVersion = 'v1';
      } else {
        setConsolidationPolicies(['WhenEmptyOrUnderutilized', 'WhenEmpty']);
      }
      k8sApi
        .relayForwardRequest(getRelayServerPayloadForNodeClass(karpenterVersion))
        .then((res) => handleNodeClassResponse(res))
        .finally(() => {
          setIsLoadingNodeClass(false);
        });
    }
  }, [isEditing, isCreating]);

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
      data = safeJSONParse(data);
    }

    if (data) {
      const tableData = transformNodeClassData(data);
      setNodeClasses(tableData ?? []);
    }
  };

  const transformNodeClassData = (data) => {
    const items = data.map((nc) => nc.metadata.name);
    return items;
  };

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
      listNodeClass();
      setSelectedNodePoolData(data);
      setYamlOutput(yaml1.dump(data));
    }
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }
    listNodePools();
  }, [accountId]);

  const listNodePools = () => {
    const isKarpenterEnable =
      ((selectedCluster?.agent?.connection_status?.autoScalerEnabled && selectedCluster?.agent?.connection_status?.autoScalerType === 'karpenter') ||
        selectedCluster?.agent?.connection_status?.karpenterEnabled) ??
      false;

    setData([]);
    setTotalCount(0);
    if (isKarpenterEnable) {
      setLoading(true);

      let karpenterVersion = 'v1beta1';
      if (compareVersions(selectedCluster?.agent?.connection_status.autoScalerVersion, '1')) {
        karpenterVersion = 'v1';
      }

      k8sApi
        .relayForwardRequest(getRelayServerPayload(karpenterVersion))
        .then((res) => handleResponse(res))
        .finally(() => {
          setLoading(false);
        });
    }
  };

  const getRelayServerPayload = (karpenterVersion) => ({
    no_sinks: true,
    cache: false,
    body: {
      account_id: accountId,
      action_name: 'get_resource',
      action_params: {
        group: 'karpenter.sh',
        version: karpenterVersion,
        resource_type: 'nodepools',
        all_namespaces: true,
      },
    },
  });

  const handleResponse = (res) => {
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
      const tableData = transformData(data);
      setData(tableData ?? []);
      setTotalCount(tableData?.length);
    }
  };

  const extractData = (res) => res?.data?.findings?.[0]?.evidence?.[0]?.data;
  const parseData = (data) => {
    const parsed = safeJSONParse(data);
    if (parsed && Array.isArray(parsed) && parsed.length > 0 && Object.prototype.hasOwnProperty.call(parsed[0], 'data')) {
      return parsed[0].data;
    }
    return data;
  };

  const transformData = (data) => {
    const items = data;
    return items?.map((item) => {
      const deepCopyItem = JSON.parse(JSON.stringify(item));
      delete deepCopyItem.metadata.managedFields;
      return createTableRow(deepCopyItem);
    });
  };

  const createTableRow = (item) => [
    {
      text: item.kind,
      drilldownQuery: item,
    },
    {
      text: item.metadata.name,
    },
    {
      component: <Datetime value={item.metadata.creationTimestamp} />,
    },
    {
      text: item?.status?.resources?.cpu ?? '-',
    },
    {
      text: item?.status?.resources?.memory ? convertToGB(item.status.resources.memory) : '-',
    },
    {
      text: item?.status?.resources?.pods ?? '-',
    },
    {
      component: hasWriteAccess(Array.isArray(accountId) ? accountId[0] : accountId) ? (
        <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems()} data={item} onMenuClick={onMenuClick} />
      ) : (
        <></>
      ),
    },
  ];

  const handleClose = () => {
    setYamlOutput('');
    setValidationMessage('');
    setName('');
    setInstanceCPU([]);
    setCPULimit();
    setMemoryLimit();
    setInstanceCategory([]);
    setInstanceFamily([]);
    setInstanceType([]);
    setInstanceZone([]);
    setErrors({});
    setCapacityType([]);
    setConsolidationPolicy('');
    setNodeBudget('');
    setWeight(0);
    setNodeClassRef('');
    setNodeClasses([]);
    setConsolidateAfter('');
    setExpireAfter('');
    setSelectedNodePoolData({});
    setFormSubmitting(false);
    setIsEditing(false);
    setIsCreating(false);
  };

  const handleUpdates = (key, value) => {
    const updateFunctions = {
      name: updateName,
      nodeClassRef: updateRequirement('nodeClassRef', setNodeClassRef),
      'instance-types': updateRequirement('node.kubernetes.io/instance-type', setInstanceType),
      'capacity-types': updateRequirement('karpenter.sh/capacity-type', setCapacityType),
      'instance-category': updateRequirement('karpenter.k8s.aws/instance-category', setInstanceCategory),
      'instance-family': updateRequirement(
        provider == 'EKS' ? 'karpenter.k8s.aws/instance-family' : 'karpenter.azure.com/sku-family',
        setInstanceFamily
      ),
      'instance-cpu': updateRequirement(provider == 'EKS' ? 'karpenter.k8s.aws/instance-cpu' : 'karpenter.azure.com/sku-cpu', setInstanceCPU),
      'instance-zone': updateRequirement('topology.kubernetes.io/zone', setInstanceZone),
      'instance-arch': updateRequirement('kubernetes.io/arch', setInstanceArc),
      'cpu-limit': updateLimit('cpu', setCPULimit),
      'memory-limit': updateLimit('memory', setMemoryLimit),
      'consolidation-policy': updateDisruption('consolidation-policy', setConsolidationPolicy),
      'node-budget': updateNodeBudget('node-budget', setNodeBudget),
      weight: updateNodePoolData('weight', setWeight),
      'consolidate-after': updateNodePoolData('consolidate-after', setConsolidateAfter),
      'expire-after': updateNodePoolData('expire-after', setExpireAfter),
    };

    const updateFunction = updateFunctions[key];
    if (updateFunction) {
      updateFunction(value);
    }
  };

  const updateNodePoolData = (type, setterFunc) => (value) => {
    if (!value) {
      return;
    }

    const updateStrategies = {
      weight: (val) => {
        const parsedVal = parseInt(val);
        if (isNaN(parsedVal)) {
          return null;
        }
        return {
          setter: () => setterFunc(parsedVal),
          updater: (prevPool) => {
            const updatedSpec = { ...prevPool.spec };
            if (parsedVal === 0) {
              delete updatedSpec.weight;
            } else {
              updatedSpec.weight = parsedVal;
            }
            return { ...prevPool, spec: updatedSpec };
          },
        };
      },
      'consolidate-after': (val) => ({
        setter: () => setterFunc(parseInt(val)),
        updater: (prevPool) => ({
          ...prevPool,
          spec: {
            ...prevPool.spec,
            disruption: {
              ...prevPool.spec?.disruption,
              consolidateAfter: val === 'Never' ? 'Never' : `${parseInt(val)}m`,
            },
          },
        }),
      }),
      'expire-after': (val) => ({
        setter: () => setterFunc(val),
        updater: (prevPool) => ({
          ...prevPool,
          spec: {
            ...prevPool.spec,
            template: {
              ...prevPool.spec?.template,
              spec: {
                ...prevPool.spec?.template?.spec,
                expireAfter: val === 'Never' ? 'Never' : `${val}h`,
              },
            },
          },
        }),
      }),
    };

    const strategy = updateStrategies[type]?.(value);
    if (strategy) {
      strategy.setter();
      setSelectedNodePoolData(strategy.updater);
    }
  };

  const updateNodeBudget = (_type, setterFunc) => (value) => {
    if (value) {
      setterFunc(value);
      setSelectedNodePoolData((prevNodePool) => {
        const updatedNodePool = { ...prevNodePool };
        if (!updatedNodePool.spec) {
          updatedNodePool.spec = {};
        }
        if (!updatedNodePool.spec.disruption) {
          updatedNodePool.spec.disruption = {};
        }
        updatedNodePool.spec.disruption.budgets = [
          {
            nodes: value + '%',
          },
        ];
        return updatedNodePool;
      });
    }
  };

  const updateDisruption = (type, setterFunc) => (value) => {
    if (value) {
      if (type == 'consolidation-policy') {
        setterFunc(value);
        setSelectedNodePoolData((prevNodePool) => {
          const updatedNodePool = { ...prevNodePool };
          if (!updatedNodePool.spec) {
            updatedNodePool.spec = {};
          }
          if (!updatedNodePool.spec.disruption) {
            updatedNodePool.spec.disruption = {};
          }
          updatedNodePool.spec.disruption.consolidationPolicy = value;
          return updatedNodePool;
        });
      }
    }
  };

  const updateLimit = (limitType, setLimitState) => (value) => {
    setLimitState(value);
    setSelectedNodePoolData((prevNodePool) => {
      const updatedNodePool = { ...prevNodePool };
      updateNodePoolLimits(updatedNodePool, limitType, value);
      return updatedNodePool;
    });
  };

  const updateNodePoolLimits = (nodePool, limitType, value) => {
    if (!nodePool.spec) {
      nodePool.spec = {};
    }

    if (isEmptyValue(value)) {
      removeLimit(nodePool, limitType);
    } else {
      addLimit(nodePool, limitType, value);
    }
  };

  const isEmptyValue = (value) => {
    return value === null || value === undefined || value === '';
  };

  const removeLimit = (nodePool, limitType) => {
    if (nodePool.spec.limits) {
      delete nodePool.spec.limits[limitType];
      if (Object.keys(nodePool.spec.limits).length === 0) {
        delete nodePool.spec.limits;
      }
    }
  };

  const addLimit = (nodePool, limitType, value) => {
    if (!nodePool.spec.limits) {
      nodePool.spec.limits = {};
    }
    nodePool.spec.limits[limitType] = formatLimitValue(limitType, value);
  };

  const formatLimitValue = (limitType, value) => {
    if (limitType === 'memory') {
      return `${parseInt(value)}Gi`;
    }
    return `${parseInt(value)}`;
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
    setSelectedNodePoolData((prevNodePool) => ({
      ...prevNodePool,
      metadata: { ...prevNodePool.metadata, name: value },
    }));
  };

  const cleanUpEmptyObjects = (updatedNodePool) => {
    if (Object.keys(updatedNodePool.spec.template.spec).length === 0) {
      delete updatedNodePool.spec.template.spec;

      if (Object.keys(updatedNodePool.spec.template).length === 0) {
        delete updatedNodePool.spec.template;

        if (Object.keys(updatedNodePool.spec).length === 0) {
          delete updatedNodePool.spec;
        }
      }
    }
  };
  const updateRequirement = (requirementKey, setState) => (value) => {
    setState(value);
    setSelectedNodePoolData((prevNodePool) => {
      const updatedNodePool = { ...prevNodePool };
      if (!updatedNodePool.spec) {
        updatedNodePool.spec = {};
      }
      if (!updatedNodePool.spec.template) {
        updatedNodePool.spec.template = {};
      }
      if (!updatedNodePool.spec.template.spec) {
        updatedNodePool.spec.template.spec = {};
      }
      if (requirementKey == 'nodeClassRef') {
        if (!updatedNodePool.spec.template.spec.nodeClassRef) {
          updatedNodePool.spec.template.spec.nodeClassRef = {};
        }
        if (value) {
          updatedNodePool.spec.template.spec.nodeClassRef.name = value;
          updatedNodePool.spec.template.spec.nodeClassRef.kind = selectedCluster?.k8s_provider == 'EKS' ? 'EC2NodeClass' : 'AKSNodeClass';
          updatedNodePool.spec.template.spec.nodeClassRef.group =
            selectedCluster?.k8s_provider == 'EKS' ? 'karpenter.k8s.aws' : 'karpenter.azure.com';
        } else {
          delete updatedNodePool.spec.template.spec.nodeClassRef;
          cleanUpEmptyObjects(updatedNodePool);
        }
      } else {
        if (!updatedNodePool.spec.template.spec.requirements) {
          updatedNodePool.spec.template.spec.requirements = [];
        }
        const requirements = updatedNodePool.spec.template.spec.requirements;
        const requirementIndex = requirements.findIndex((req) => req.key === requirementKey);

        if (requirementIndex !== -1) {
          if (value.length === 0) {
            requirements.splice(requirementIndex, 1);
          } else {
            requirements[requirementIndex].values = value;
          }
        } else if (value.length > 0) {
          handleCreateRequirement(requirementKey, 'In', value);
        }
      }

      return updatedNodePool;
    });
  };

  const handleSubmit = () => {
    if (!name || name.trim() == '') {
      setErrors((prevErrors) => ({
        ...prevErrors,
        ['name']: `Name is required`,
      }));
      return;
    }
    setFormSubmitting(true);
    if ('spec' in selectedNodePoolData) {
      if (Object.keys(selectedNodePoolData.spec).length == 0) {
        delete selectedNodePoolData.spec;
      }
    }
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: isEditing ? 'replace_workload' : 'create_workload',
        action_params: {
          name: selectedNodePoolData.metadata.name,
          namespace: '',
          kind: 'NodePool',
          ['nodepool']: selectedNodePoolData,
        },
        origin: 'Nudgebee UI',
      },
    };
    k8sApi
      .relayForwardRequest(data)
      .then((res) => {
        if (res?.data?.success) {
          snackbar.success(`${selectedNodePoolData.metadata?.name} is ${isEditing ? 'updated' : 'created'} successfully`);
          handleClose();
          listNodePools();
        } else {
          snackbar.error(`Failed to ${isEditing ? 'update' : 'create'} ${selectedNodePoolData.metadata?.name}`);
        }
      })
      .catch(() => {
        snackbar.error(`Failed to ${isEditing ? 'update' : 'create'} ${selectedNodePoolData.metadata?.name}`);
      })
      .finally(() => {
        setFormSubmitting(false);
      });
    return;
  };

  useEffect(() => {
    if (isEditing && selectedNodePoolData && Object.keys(selectedNodePoolData).length > 0) {
      const requirements = selectedNodePoolData.spec.template.spec.requirements ?? [];
      const disruption = selectedNodePoolData.spec.disruption;
      const instanceTypeRequirement = requirements.find((req) => req.key === 'node.kubernetes.io/instance-type')?.values ?? [];
      const capacityTypeRequirement = requirements.find((req) => req.key === 'karpenter.sh/capacity-type')?.values ?? [];
      const categoryTypeRequirement = requirements.find((req) => req.key === 'karpenter.k8s.aws/instance-category')?.values ?? [];
      const familyTypeRequirement =
        requirements.find((req) => req.key === (provider == 'EKS' ? 'karpenter.k8s.aws/instance-family' : 'karpenter.azure.com/sku-family'))
          ?.values ?? [];
      const cpuTypeRequirement =
        requirements.find((req) => req.key === (provider == 'EKS' ? 'karpenter.k8s.aws/instance-cpu' : 'karpenter.azure.com/sku-cpu'))?.values ?? [];
      const zoneRequirement = requirements.find((req) => req.key === 'topology.kubernetes.io/zone')?.values ?? [];
      const cpuLimit = selectedNodePoolData.spec.limits?.cpu ?? '';
      const memoryLimit = selectedNodePoolData.spec.limits?.memory ?? '';
      const nodeClassRefName = selectedNodePoolData.spec?.template?.spec?.nodeClassRef?.name || '';

      setName(selectedNodePoolData.metadata.name ?? '');
      setInstanceType(instanceTypeRequirement);
      setCapacityType(capacityTypeRequirement);
      setInstanceFamily(familyTypeRequirement);
      setInstanceCategory(categoryTypeRequirement);
      setInstanceCPU(cpuTypeRequirement);
      setInstanceZone(zoneRequirement);
      setCPULimit(cpuLimit);
      setNodeClassRef(nodeClassRefName);
      if (memoryLimit) {
        setMemoryLimit(parseInt(memoryLimit.match(/\d+/)[0], 10));
      }
      if (disruption.consolidationPolicy) {
        setConsolidationPolicy(disruption.consolidationPolicy);
      }
      if (disruption.consolidateAfter) {
        setConsolidateAfter(disruption.consolidateAfter);
      }
      if (selectedNodePoolData.spec.template.spec.expireAfter) {
        setConsolidateAfter(selectedNodePoolData.spec.template.spec.expireAfter);
      }
      if (selectedNodePoolData.spec.weight) {
        setWeight(selectedNodePoolData.spec.weight);
      }
      if (disruption.budgets && disruption.budgets.length > 0) {
        setNodeBudget(disruption.budgets[0].nodes.replace('%', ''));
      }
    }
  }, [isEditing]);

  const handleTabClick = (type) => {
    if (type == 'yaml') {
      setCondition('yaml');
      setValidationMessage('YAML is valid');
      setYamlOutput(yaml1.dump(selectedNodePoolData));
    }
  };

  const handleCreateRequirement = (key, operator, values) => {
    if (key && operator && values) {
      setSelectedNodePoolData((prevNodePool) => {
        const updatedNodePool = { ...prevNodePool };
        if (!updatedNodePool.spec) {
          updatedNodePool.spec = {};
        }
        if (!updatedNodePool.spec.template) {
          updatedNodePool.spec.template = {};
        }
        if (!updatedNodePool.spec.template.spec) {
          updatedNodePool.spec.template.spec = {};
        }
        if (!updatedNodePool.spec.template.spec.requirements) {
          updatedNodePool.spec.template.spec.requirements = [];
        }
        updatedNodePool.spec.template.spec.requirements.push({
          key: key,
          operator: operator,
          values: values,
        });
        return updatedNodePool;
      });
    }
  };

  const handleLabelChange = (field, value) => {
    setNewLabel((prevNewRequirement) => ({
      ...prevNewRequirement,
      [field]: value,
    }));
  };

  const handleLabelCreate = () => {
    if (newLabel.key && newLabel.value) {
      setSelectedNodePoolData((prevNodePool) => {
        const updatedNodePool = { ...prevNodePool };
        if (!updatedNodePool.spec.template.metadata) {
          updatedNodePool.spec.template.metadata = {};
        }
        if (!updatedNodePool.spec.template.metadata.labels) {
          updatedNodePool.spec.template.metadata.labels = {};
        }
        updatedNodePool.spec.template.metadata.labels = {
          ...updatedNodePool.spec.template.metadata.labels,
          [newLabel.key]: newLabel.value,
        };
        return updatedNodePool;
      });
      setNewLabel({ key: '', value: '' });
    }
  };

  const handleDelete = (key) => {
    setSelectedNodePoolData((prevNodePool) => {
      const updatedNodePool = { ...prevNodePool };
      const labels = { ...updatedNodePool.spec.template.metadata.labels };
      delete labels[key];
      updatedNodePool.spec.template.metadata.labels = labels;
      return updatedNodePool;
    });
  };

  const handleDeleteTaint = (indexToDelete) => {
    setSelectedNodePoolData((prevNodePool) => {
      const updatedNodePool = { ...prevNodePool };
      updatedNodePool.spec.template.spec.taints.splice(indexToDelete, 1);
      return updatedNodePool;
    });
  };

  const handleNewTaintsChange = (field, value) => {
    setNewTaints((prevNewTaints) => ({
      ...prevNewTaints,
      [field]: value,
    }));
  };

  const handleNewTaint = () => {
    if (newTaints.key && newTaints.effect) {
      setSelectedNodePoolData((prevNodePool) => {
        const updatedNodePool = { ...prevNodePool };
        if (!updatedNodePool.spec.template) {
          updatedNodePool.spec.template = {};
        }
        if (!updatedNodePool.spec.template.spec) {
          updatedNodePool.spec.template.spec = {};
        }
        if (!updatedNodePool.spec.template.spec?.taints) {
          updatedNodePool.spec.template.spec.taints = [];
        }
        updatedNodePool.spec.template.spec.taints.push(newTaints);
        return updatedNodePool;
      });
      setNewTaints({});
    }
  };

  const handleDeleteStartupTaint = (indexToDelete) => {
    setSelectedNodePoolData((prevNodePool) => {
      const updatedNodePool = { ...prevNodePool };
      updatedNodePool.spec.template.spec.startupTaints.splice(indexToDelete, 1);
      return updatedNodePool;
    });
  };

  const handleNewStartupTaintsChange = (field, value) => {
    setNewStartupTaints((prevNewStartupTaints) => ({
      ...prevNewStartupTaints,
      [field]: value,
    }));
  };

  const handleNewStartupTaint = () => {
    if (newStartupTaints.key && newStartupTaints.effect) {
      setSelectedNodePoolData((prevNodePool) => {
        const updatedNodePool = { ...prevNodePool };
        if (!updatedNodePool.spec.template) {
          updatedNodePool.spec.template = {};
        }
        if (!updatedNodePool.spec.template.spec) {
          updatedNodePool.spec.template.spec = {};
        }
        if (!updatedNodePool.spec.template.spec?.startupTaints) {
          updatedNodePool.spec.template.spec.startupTaints = [];
        }
        updatedNodePool.spec.template.spec.startupTaints.push(newStartupTaints);
        return updatedNodePool;
      });
      setNewStartupTaints({ key: '', effect: '' });
    }
  };

  return (
    <>
      <Modal width='md' open={isEditing || isCreating} handleClose={() => handleClose()} title={'Node Pool Configuration'} loader={formSubmitting}>
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
          <DsButton fullWidth tone={condition === 'auto-config' ? 'primary' : 'secondary'} onClick={() => setCondition('auto-config')}>
            Auto Config
          </DsButton>
          <DsButton fullWidth tone={condition === 'yaml' ? 'primary' : 'secondary'} onClick={() => handleTabClick('yaml')}>
            Manual Config (Yaml)
          </DsButton>
        </Box>
        {condition == 'yaml' && (
          <>
            <Box
              sx={{
                p: 'var(--ds-space-5) 0px',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-4)',
              }}
            >
              <CodeMirror
                value={yamlOutput}
                height='300px'
                extensions={[yaml()]}
                onChange={(value) => {
                  setYamlOutput(value);
                  try {
                    setSelectedNodePoolData(yaml1.load(value));
                    setValidationMessage('YAML is valid');
                  } catch (error) {
                    setValidationMessage('Invalid YAML: ' + error.message);
                  }
                }}
                editable={true}
                style={{
                  border: `2px solid ${validationMessage.startsWith('YAML is valid') ? 'green' : 'red'}`,
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
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-4)' }}>
                <Box sx={{ width: ds.space.mul(0, 118) }}>
                  <Input
                    value={name}
                    size='sm'
                    id='name'
                    label='Name'
                    placeholder='Enter Name'
                    onChange={(value) => handleUpdates('name', value)}
                    error={errors.name || undefined}
                  />
                </Box>
                <FilterDropdownButton
                  id='node-class'
                  label='Node Class'
                  value={nodeClassRef}
                  options={nodeClasses}
                  onSelect={(event) => {
                    handleUpdates('nodeClassRef', event.target.value || '');
                  }}
                  isOptionsLoading={isLoadingNodeClass}
                  sx={{ width: ds.space.mul(0, 118), height: ds.space.mul(0, 20) }}
                />
              </Box>
              <FilterDropdownButton
                id='instance-family'
                label='Instance Family'
                multiple
                limitTag={3}
                value={instanceFamily}
                options={provider == 'EKS' ? awsInstanceFamily : azureInstanceFamily}
                onSelect={(event) => {
                  handleUpdates('instance-family', event.target.value);
                }}
                sx={{ minWidth: ds.space.mul(0, 100) }}
              />
              <FilterDropdownButton
                id='instance-cpu'
                label='Instance CPU'
                multiple
                limitTag={3}
                value={instanceCPU}
                options={['4', '8', '16', '32']}
                onSelect={(event) => {
                  handleUpdates('instance-cpu', event.target.value);
                }}
                sx={{ minWidth: ds.space.mul(0, 100) }}
              />
              <FilterDropdownButton
                id='instance-zone'
                label='Instance Zone'
                multiple
                limitTag={3}
                value={instanceZone}
                options={provider == 'EKS' ? awsInstanceZone : azureInstanceZone}
                onSelect={(event) => {
                  handleUpdates('instance-zone', event.target.value);
                }}
                sx={{ minWidth: ds.space.mul(0, 100) }}
              />
              <FilterDropdownButton
                id='capacity-type'
                label='Capacity Type'
                multiple
                limitTag={3}
                value={capacityType}
                options={['spot', 'on-demand']}
                onSelect={(event) => {
                  handleUpdates('capacity-types', event.target.value);
                }}
                sx={{ minWidth: ds.space.mul(0, 100) }}
              />
              <Typography className='notes'># Labels are arbitrary key-values that are applied to all nodes</Typography>
              {selectedNodePoolData?.spec?.template?.metadata?.labels &&
              Object.keys(selectedNodePoolData?.spec?.template?.metadata?.labels)?.length > 0 ? (
                Object.entries(selectedNodePoolData.spec.template.metadata.labels).map(([key, value], _index) => (
                  <Box key={`${key}-box`} display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                    <Box sx={{ flex: 1 }}>
                      <Input label='Key' value={key} disabled size='sm' onChange={() => {}} />
                    </Box>
                    <Box sx={{ flex: 1 }}>
                      <Input label='Value' value={String(value)} disabled size='sm' onChange={() => {}} />
                    </Box>
                    <Box sx={{ display: 'inline-flex', ml: ds.space[2] }}>
                      <DsButton
                        tone='secondary'
                        size='sm'
                        composition='icon-only'
                        aria-label='Delete'
                        onClick={() => {
                          handleDelete(key);
                        }}
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

                <Box sx={{ display: 'inline-flex', ml: ds.space[4] }}>
                  <DsButton
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    aria-label='Add'
                    onClick={handleLabelCreate}
                    disabled={!newLabel.key || !newLabel.value}
                    icon={<SafeIcon src={PlusIcon} alt='add field' />}
                  />
                </Box>
              </Box>
              {provider == 'EKS' ? (
                <>
                  <FilterDropdownButton
                    id='instance-architecture'
                    label='Instance Architecture'
                    multiple
                    limitTag={3}
                    value={instanceArc}
                    options={['arm64', 'amd64']}
                    onSelect={(e) => {
                      handleUpdates('instance-arch', e.target.value);
                    }}
                    sx={{ minWidth: ds.space.mul(0, 100) }}
                  />
                  <FilterDropdownButton
                    id='instance-type'
                    label='Instance Type'
                    multiple
                    limitTag={3}
                    value={instanceType}
                    options={awsInstances}
                    onSelect={(e) => {
                      handleUpdates('instance-types', e.target.value);
                    }}
                    sx={{ minWidth: ds.space.mul(0, 100) }}
                  />
                  <FilterDropdownButton
                    id='instance-category'
                    label='Instance Category'
                    multiple
                    limitTag={3}
                    value={instanceCategory}
                    options={awsInstanceCategory}
                    onSelect={(e) => {
                      const values = e.target.value.map((v) => (typeof v === 'object' ? v.value : v));
                      handleUpdates('instance-category', values);
                    }}
                    sx={{ minWidth: ds.space.mul(0, 100) }}
                  />
                  <Typography className='notes'>
                    # Describes which types of Nodes Karpenter should consider for consolidation
                    <br />
                    # If using &apos;WhenEmptyOrUnderutilized&apos;, Karpenter will consider all nodes for consolidation and attempt to remove or
                    replace Nodes when it discovers that the Node is empty or underutilized and could be changed to reduce cost
                    <br /># If using &apos;WhenEmpty&apos;, Karpenter will only consider nodes for consolidation that contain no workload pods
                  </Typography>

                  <FilterDropdownButton
                    id='consolidation-policy'
                    label='Consolidation Policy'
                    value={consolidationPolicy}
                    options={consolidationPolicies}
                    onSelect={(e) => {
                      handleUpdates('consolidation-policy', e.target.value);
                    }}
                    sx={{ minWidth: ds.space.mul(0, 100) }}
                  />
                  <Typography className='notes'>
                    # The amount of time a Node can live on the cluster before being removed
                    <br />
                    # Avoiding long-running Nodes helps to reduce security vulnerabilities as well as to reduce the chance of issues that can plague
                    Nodes with long uptimes such as file fragmentation or memory leaks from system processes
                    <br />
                    # You can choose to disable expiration entirely by setting the string value &apos;Never&apos; here
                    <br /># Note: changing this value in the nodepool will drift the nodeclaims.
                  </Typography>
                  <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                    <Input
                      value={expireAfter}
                      size='sm'
                      id='expire-after'
                      label='Expire After'
                      onChange={(value) => handleUpdates('expire-after', value)}
                      suffix='h'
                    />
                  </Box>
                  <Typography className='notes'>
                    # The amount of time Karpenter should wait to consolidate a node after a pod has been added or removed from the node.
                    <br /># You can choose to disable consolidation entirely by setting the string value &apos;Never&apos; here
                  </Typography>
                  <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                    <Input
                      value={consolidateAfter}
                      size='sm'
                      id='consolidate-after'
                      label='Consolidate After'
                      suffix='m'
                      onChange={(value) => handleUpdates('consolidate-after', value)}
                    />
                  </Box>
                  <Typography className='notes'>
                    # Budgets control the speed Karpenter can scale down nodes.
                    <br /># Karpenter will respect the minimum of the currently active budgets, and will round up when considering percentages.
                  </Typography>
                  <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                    <Input
                      value={nodeBudget}
                      size='sm'
                      id='node-budget'
                      label='Node Budget'
                      onChange={(value) => handleUpdates('node-budget', value)}
                      suffix='%'
                    />
                  </Box>
                </>
              ) : (
                <></>
              )}
              <Typography className='notes'>
                # Provisioned nodes will have these taints <br />
                # Taints may prevent pods from scheduling if they are not tolerated by the pod. <br /># Taints to add to provisioned nodes. Pods that
                don’t tolerate those taints could be prevented from scheduling.
              </Typography>
              {selectedNodePoolData?.spec?.template?.spec?.taints && selectedNodePoolData?.spec?.template?.spec?.taints?.length > 0 ? (
                selectedNodePoolData.spec.template.spec?.taints.map((taint, index) => (
                  <Box key={`${taint.key}-box`} display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                    <Input label='Key' value={taint.key ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Effect' value={taint.effect ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Operator' value={taint.operator ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Value' value={taint.value ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Toleration Seconds' value={String(taint.tolerationSeconds ?? '')} disabled size='sm' onChange={() => {}} />
                    <DsButton
                      tone='secondary'
                      size='sm'
                      composition='icon-only'
                      aria-label='Delete'
                      onClick={() => {
                        handleDeleteTaint(index);
                      }}
                      icon={<SafeIcon src={deleteIcon} alt='delete' />}
                    />
                  </Box>
                ))
              ) : (
                <Typography>No Taints available.</Typography>
              )}
              <Box display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                <Input label='Key' value={newTaints.key ?? ''} onChange={(value) => handleNewTaintsChange('key', value)} size='sm' />
                <FilterDropdownButton
                  id='taint-effect'
                  label='Effect'
                  value={newTaints.effect ?? ''}
                  options={['NoSchedule', 'NoExecute', 'PreferNoSchedule']}
                  onSelect={(e) => handleNewTaintsChange('effect', e.target.value)}
                  sx={{ minWidth: ds.space.mul(0, 100), height: ds.space.mul(0, 20) }}
                />
                <FilterDropdownButton
                  id='taint-operator'
                  label='Operator'
                  value={newTaints.operator ?? ''}
                  options={['Equal', 'Exists']}
                  onSelect={(e) => handleNewTaintsChange('operator', e.target.value)}
                  sx={{ minWidth: ds.space.mul(0, 100), height: ds.space.mul(0, 20) }}
                />
                <Input label='Value' value={newTaints.value ?? ''} onChange={(value) => handleNewTaintsChange('value', value)} size='sm' />
                <Input
                  label='Toleration Seconds'
                  value={String(newTaints.tolerationSeconds ?? '')}
                  onChange={(value) => handleNewTaintsChange('tolerationSeconds', value)}
                  size='sm'
                />
                <Box sx={{ display: 'inline-flex', ml: ds.space[4] }}>
                  <DsButton
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    aria-label='Add'
                    onClick={handleNewTaint}
                    disabled={!newTaints.key || !newTaints.effect}
                    icon={<SafeIcon src={PlusIcon} alt='add field' />}
                  />
                </Box>
              </Box>
              <Typography className='notes'>
                # Provisioned nodes will have these taints, but pods do not need to tolerate these taints to be provisioned by this NodePool.
                <br />
                # These taints are expected to be temporary and some other entity (e.g. a DaemonSet) is responsible for removing the taint after it
                has finished initializing the node.
                <br /># Taints that are added to nodes to indicate that a certain condition must be met, such as starting an agent or setting up
                networking, before the node is can be initialized. These taints must be cleared before pods can be deployed to a node.
              </Typography>
              {selectedNodePoolData?.spec?.template?.spec?.startupTaints && selectedNodePoolData?.spec?.template?.spec?.startupTaints?.length > 0 ? (
                selectedNodePoolData.spec.template.spec?.startupTaints.map((startupTaint, index) => (
                  <Box key={`${startupTaint.key}-box`} display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                    <Input label='Key' value={startupTaint.key ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Effect' value={startupTaint.effect ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Operator' value={startupTaint.operator ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Value' value={startupTaint.value ?? ''} disabled size='sm' onChange={() => {}} />
                    <Input label='Toleration Seconds' value={String(startupTaint.tolerationSeconds ?? '')} disabled size='sm' onChange={() => {}} />
                    <DsButton
                      tone='secondary'
                      size='sm'
                      composition='icon-only'
                      aria-label='Delete'
                      onClick={() => {
                        handleDeleteStartupTaint(index);
                      }}
                      icon={<SafeIcon src={deleteIcon} alt='delete' />}
                    />
                  </Box>
                ))
              ) : (
                <Typography>No Startup Taints available.</Typography>
              )}
              <Box display='flex' alignItems='center' mb={ds.space[4]} gap={ds.space[2]}>
                <Input label='Key' value={newStartupTaints.key ?? ''} onChange={(value) => handleNewStartupTaintsChange('key', value)} size='sm' />
                <FilterDropdownButton
                  id='startup-taint-effect'
                  label='Effect'
                  value={newStartupTaints.effect ?? ''}
                  options={['NoSchedule', 'NoExecute', 'PreferNoSchedule']}
                  onSelect={(e) => handleNewStartupTaintsChange('effect', e.target.value)}
                  sx={{ minWidth: ds.space.mul(0, 100), height: ds.space.mul(0, 20) }}
                />
                <FilterDropdownButton
                  id='startup-taint-operator'
                  label='Operator'
                  value={newStartupTaints.operator ?? ''}
                  options={['Equal', 'Exists']}
                  onSelect={(e) => handleNewStartupTaintsChange('operator', e.target.value)}
                  sx={{ minWidth: ds.space.mul(0, 100), height: ds.space.mul(0, 20) }}
                />
                <Input
                  label='Value'
                  value={newStartupTaints.value ?? ''}
                  onChange={(value) => handleNewStartupTaintsChange('value', value)}
                  size='sm'
                />
                <Input
                  label='Toleration Seconds'
                  value={String(newStartupTaints.tolerationSeconds ?? '')}
                  onChange={(value) => handleNewStartupTaintsChange('tolerationSeconds', value)}
                  size='sm'
                />
                <Box sx={{ display: 'inline-flex', ml: ds.space[4] }}>
                  <DsButton
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    aria-label='Add'
                    onClick={handleNewStartupTaint}
                    disabled={!newStartupTaints.key || !newStartupTaints.effect}
                    icon={<SafeIcon src={PlusIcon} alt='add field' />}
                  />
                </Box>
              </Box>
              <Typography className='notes'>
                # Resource limits constrain the total size of the pool.
                <br /># Limits prevent Karpenter from creating new instances once the limit is exceeded.
              </Typography>
              <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                <Input value={cpuLimit} size='sm' id='cpu-limit' label='CPU Limit' onChange={(value) => handleUpdates('cpu-limit', value)} />
              </Box>
              <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                <Input
                  value={memoryLimit}
                  size='sm'
                  id='memory-limit'
                  label='Memory Limit'
                  onChange={(value) => handleUpdates('memory-limit', value)}
                  suffix='Gi'
                />
              </Box>
              <Typography className='notes'>
                # Priority given to the NodePool when the scheduler considers which NodePool to select. Higher weights indicate higher priority when
                comparing NodePools.
                <br /># Specifying no weight is equivalent to specifying a weight of 0.
              </Typography>
              <Box sx={{ maxWidth: ds.space.mul(0, 118) }}>
                <Input value={weight} size='sm' id='weight' type='number' label='Weight' onChange={(value) => handleUpdates('weight', value)} />
              </Box>
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
          <DsButton tone='secondary' onClick={() => handleClose()} disabled={formSubmitting}>
            Cancel
          </DsButton>
          <DsButton onClick={() => handleSubmit()} disabled={(validationMessage !== 'YAML is valid' && condition == 'yaml') || formSubmitting}>
            {`${isEditing ? 'Update' : 'Create'} Node Pool`}
          </DsButton>
        </Box>
      </Modal>
      <ListingLayout id='auto-scaler-box'>
        <ListingLayout.Toolbar
          actions={
            hasWriteAccess() ? (
              <Box key={'1'} sx={{ mr: '1vw' }}>
                <DsButton
                  size='md'
                  onClick={() => {
                    setIsCreating(true);
                    listNodeClass();
                    setSelectedNodePoolData({
                      apiVersion: 'karpenter.sh/v1beta1',
                      kind: 'NodePool',
                      metadata: {
                        creationTimestamp: new Date().toISOString(),
                        generation: 3,
                      },
                    });
                  }}
                >
                  Create New Node Pool
                </DsButton>
              </Box>
            ) : null
          }
        />
        <ListingLayout.Body>
          <KubernetesTable2
            id={'auto-scaler'}
            headers={['Kind', 'Name', 'Time', 'CPU', 'Memory', 'Pods', '']}
            data={data}
            expandable={{
              tabs: [
                {
                  text: 'Details',
                  componentFn: function (accountId, drilldownQuery) {
                    return autoScalerDetailJSONFn(accountId, drilldownQuery);
                  },
                },
              ],
            }}
            rowsPerPage={totalCount}
            stickyColumnIndex={'7'}
            totalRows={totalCount}
            showExpandable={true}
            loading={loading}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

const autoScalerDetailJSONFn = (accountId, drilldownQuery) => {
  if (drilldownQuery && Object.keys(drilldownQuery).length > 0) {
    return (
      <CodeMirror
        value={yaml1.dump(drilldownQuery)}
        height='300px'
        extensions={[yaml(), EditorView.lineWrapping]}
        editable={false}
        style={{
          border: '1px solid silver',
        }}
      />
    );
  }
  return <Typography>No Data Available</Typography>;
};

KubernetesAutoScalerNodePool.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesAutoScalerNodePool;
