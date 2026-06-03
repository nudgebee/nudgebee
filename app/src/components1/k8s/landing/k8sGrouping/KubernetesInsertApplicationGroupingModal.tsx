import React, { useState, useEffect } from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import { Input } from '@components1/ds/Input';
import SafeIcon from '@components1/common/SafeIcon';
import { CrossIcon } from '@assets';
import { Modal } from '@components1/ds/Modal';
import { Button as DsButton } from '@components1/ds/Button';
import { Select } from '@components1/ds/Select';
import apiHome from '@api1/home';
import k8sApi from '@api1/kubernetes';
import apiAppGrouping from '@api1/application-groupings';
import { textValidation } from '@lib/validation';
import { ds } from '@utils/colors';

interface KubernetesInsertApplicationGroupingModalProps {
  open: boolean;
  handleClose: () => void;
  isUpdateGroup: boolean;
  groupId: string;
  handleSnackBarData: (data: any) => void;
}

interface WorkloadDetails {
  accountId: string;
  account_name: string;
  label: string;
  namespace: string;
  value: string;
  kind: string;
  id: string;
}
interface ActionButtonProps {
  buttons: any;
  selectedWorkloadCount: number;
}

interface ClusterDetails {
  label: string;
  value: string;
}

interface ValidationErrorProps {
  groupName: string;
}

const ActionButtons: React.FC<ActionButtonProps> = ({ buttons, selectedWorkloadCount }) => {
  const cancelIndex = buttons.findIndex((button: any) => button.label === '');
  const rightButtons = buttons.slice(cancelIndex + 1);

  return (
    <Box
      sx={{
        display: 'flex',
        height: ds.space.mul(0, 28),
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: 'var(--ds-space-2)',
        flexShrink: 0,
        paddingX: ds.space.mul(0, 5),
      }}
    >
      <Box>
        <Typography
          sx={{
            display: 'flex',
            alignItems: 'center',
            color: 'var(--ds-brand-500)',
            fontSize: 'var(--ds-text-title)',
            fontWeight: 'var(--ds-font-weight-medium)',
            span: {
              backgroundColor: 'var(--ds-blue-200)',
              height: ds.space.mul(0, 14),
              width: ds.space.mul(0, 13),
              borderRadius: 'var(--ds-radius-sm)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              ml: 'var(--ds-space-1)',
            },
          }}
        >
          Total Application Selected - <span>{selectedWorkloadCount}</span>
        </Typography>
      </Box>

      <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', alignItems: 'center', button: { minWidth: ds.space.mul(0, 70) } }}>
        {rightButtons.map((button: any) => (
          <React.Fragment key={button.label}>
            <DsButton
              tone={button.label == 'Cancel' ? 'secondary' : 'primary'}
              onClick={() => {
                button.onClick();
              }}
              disabled={button.isDisabled}
            >
              {button.label}
            </DsButton>
          </React.Fragment>
        ))}
      </Box>
    </Box>
  );
};

const KubernetesInsertApplicationGroupingModal: React.FC<KubernetesInsertApplicationGroupingModalProps> = ({
  open,
  handleClose,
  isUpdateGroup = false,
  groupId = '',
  handleSnackBarData,
}) => {
  const [selectedCluster, setSelectedCluster] = useState<ClusterDetails>({ label: '', value: '' });
  const [namespaceOptions, setNamespaceOptions] = useState<any[]>([]);
  const [selectedNamespaces, setSelectedNamespaces] = useState<any>([]);
  const [clusters, setClusters] = useState<ClusterDetails[]>([]);
  const [relevantWorkloads, setRelevantWorkloads] = useState<WorkloadDetails[]>([]);
  const [selectedWorkloads, setSelectedWorkloads] = useState<WorkloadDetails[]>([]);
  const [allAppGroupNames, setAllAppGroupNames] = useState<string[]>([]);
  const [groupName, setGroupName] = useState<string>('');
  const [groupDesc, setGroupDesc] = useState<string>('');
  const [validationError, setValidationError] = useState<ValidationErrorProps>({ groupName: '' });
  const [selectAllChecked, setSelectAllChecked] = useState<boolean>(false);
  const [isClustersLoading, setIsClustersLoading] = useState<boolean>(false);
  const [isNamespacesLoading, setIsNamespacesLoading] = useState<boolean>(false);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);
  const [isRelevantWorkloads, setIsRelevantWorkloads] = useState<boolean>(false);

  const handleCheckboxChange = (item: WorkloadDetails) => {
    const foundWorkload = selectedWorkloads.find((i) => i.id === item.id);
    if (!foundWorkload) {
      setSelectedWorkloads([...selectedWorkloads, item]);
    } else {
      handleDelete(item);
    }
  };

  const handleSelectAllCheckbox = () => {
    if (selectAllChecked) {
      const filterDeselectedWorkloads = selectedWorkloads.filter((item) => !relevantWorkloads.map((i) => i.id).includes(item?.id));
      setSelectedWorkloads(filterDeselectedWorkloads);
      setSelectAllChecked(false);
    } else {
      //below is the array declaration of workloads that are not present in selected workloads
      const newlySelectedWorkloads: WorkloadDetails[] = relevantWorkloads.filter((item) => !selectedWorkloads.map((i) => i.id).includes(item?.id));
      setSelectedWorkloads(selectedWorkloads.concat(newlySelectedWorkloads));
      setSelectAllChecked(true);
    }
  };

  const handleDelete = (item: WorkloadDetails) => {
    const selWorkload = selectedWorkloads.filter((workload) => workload.id != item.id);
    setSelectedWorkloads(selWorkload);
  };

  const findDuplicateNames = (name: string, id: string) => {
    return allAppGroupNames.some((item: any) => item.name === name && (!isUpdateGroup || item.id !== id));
  };
  const handleSubmit = () => {
    textValidation(groupName, validationError, setValidationError, 'groupName', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);

    if (findDuplicateNames(groupName, groupId)) {
      setValidationError({ groupName: 'Group name already in use' });
      return;
    }

    if (!groupName || validationError.groupName) {
      return;
    }
    const transformWorkloadData = selectedWorkloads.map((item) => ({
      workload_name: item.label,
      workload_kind: item.kind,
      namespace_name: item.namespace,
      account_id: item.accountId,
      cloud_resource_id: item.id,
    }));

    setIsSubmitting(true);
    let data;
    if (isUpdateGroup && groupId) {
      data = {
        id: groupId,
        name: groupName,
        description: groupDesc,
      };
      apiAppGrouping
        .UpdateAppGrouping(data, transformWorkloadData)
        .then((res: any) => {
          if (res?.data?.errors) {
            handleSnackBarData({ message: `Failed to update grouping '${groupName}' !`, severity: 'error' });
            handleClose();
          } else if (res?.data?.data?.applications_update_group) {
            handleSnackBarData({ message: `Application grouping '${groupName}' updated !`, severity: 'success' });
            handleClose();
          }
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    } else {
      data = {
        name: groupName,
        description: groupDesc,
      };
      apiAppGrouping
        .InsertAppGrouping(data, transformWorkloadData)
        .then((res: any) => {
          if (res?.data?.errors) {
            handleSnackBarData({ message: 'Failed to create grouping !', severity: 'error' });
            handleClose();
          } else if (res?.data?.data?.applications_create_group) {
            handleSnackBarData({ message: `Application grouping '${groupName}' created !`, severity: 'success' });
            handleClose();
          }
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    }
  };

  // Load all app names
  useEffect(() => {
    if (open) {
      apiAppGrouping.listAllApplicationGroupNames().then((res: any) => {
        setAllAppGroupNames(res);
      });
    }
  }, [open]);

  const fetchExistingGroupInfo = () => {
    if (isUpdateGroup && groupId && clusters) {
      apiAppGrouping.getAppGroupByPK(groupId).then((res) => {
        if (res.errors) {
          handleClose();
        }
        const groupData = res?.data?.data?.application_group_by_pk;
        setGroupName(groupData?.name);
        setGroupDesc(groupData?.description);
        const existingWorkloads: WorkloadDetails[] = groupData.application_group_mappings.map((item: any) => ({
          accountId: item.account_id,
          account_name: getAccountNameById(item.account_id),
          label: item.workload_name,
          value: item.workload_name,
          namespace: item.namespace_name,
          kind: item.workload_kind,
          id: item.cloud_resource_id,
        }));

        const distinctNamespaces: string[] = [];
        existingWorkloads.forEach((item: any) => {
          if (!distinctNamespaces.includes(item.namespace)) {
            distinctNamespaces.push(item.namespace);
          }
        });
        setSelectedNamespaces(distinctNamespaces);
        setSelectedWorkloads(existingWorkloads);
        setSelectedCluster({
          label: getAccountNameById(groupData.application_group_mappings?.[0]?.account_id),
          value: groupData.application_group_mappings?.[0]?.account_id,
        });
      });
    }
  };

  useEffect(() => {
    const extraWorkload: WorkloadDetails | undefined = relevantWorkloads.find((item) => !selectedWorkloads.map((i) => i.id).includes(item?.id));
    if (extraWorkload || selectedWorkloads.length == 0) {
      setSelectAllChecked(false);
    } else {
      setSelectAllChecked(true);
    }
  }, [selectedWorkloads, relevantWorkloads]);

  useEffect(() => {
    fetchExistingGroupInfo();
  }, [isUpdateGroup, clusters]);

  const clearAllAndClose = () => {
    setSelectedCluster({ label: '', value: '' });
    setGroupName('');
    setGroupDesc('');
    setSelectedWorkloads([]);
    handleClose();
  };

  const buttons = [
    {
      label: 'Cancel',
      backgroundColor: 'transparent',
      color: 'var(--ds-blue-500)',
      activeColor: 'var(--ds-blue-500)',
      onClick: clearAllAndClose,
      isDisabled: isSubmitting,
    },

    {
      label: isUpdateGroup ? 'Update' : 'Create',
      backgroundColor: 'var(--ds-blue-500)',
      color: 'white',
      activeColor: 'var(--ds-blue-500)',
      onClick: handleSubmit,
      isDisabled: isSubmitting,
    },
  ];

  const getClustersData = async () => {
    try {
      setIsClustersLoading(true);
      const response = await apiHome.getCloudAccounts('K8s');
      if (response && response.length > 0) {
        const clusters = response.map((item: any) => ({
          label: item.account_name,
          value: item.id,
        }));
        setClusters(clusters);
      } else {
        setClusters([]);
      }
    } catch (error) {
      console.error(error);
    } finally {
      setIsClustersLoading(false);
    }
  };

  useEffect(() => {
    if (open) {
      getClustersData();
    }
  }, [open]);

  const getAccountNameById = (value: string) => {
    const row: any = clusters.find((item) => item.value == value);
    if (row) {
      return row.label;
    }
    return '';
  };

  const getWorkloads = async () => {
    if (selectedCluster.value) {
      setIsRelevantWorkloads(true);
      k8sApi
        .getK8sWorkload(0, 0, {
          accountId: selectedCluster.value,
          namespaceList: selectedNamespaces,
        })
        .then((res) => {
          const response = res?.data?.k8s_workloads?.map((item: any) => ({
            value: item.name,
            label: item.name,
            accountId: item.cloud_account_id,
            account_name: getAccountNameById(item?.cloud_account_id),
            namespace: item.namespace,
            kind: item.kind,
            id: item.cloud_resource_id,
          }));
          setRelevantWorkloads(response || []);
        })
        .finally(() => {
          setIsRelevantWorkloads(false);
        });
    }
  };

  const filterSelectedWorkloadsByNamespace = (workloads: WorkloadDetails[]) => {
    setSelectedWorkloads(workloads.filter((item) => selectedNamespaces.includes(item.namespace)));
  };
  useEffect(() => {
    getWorkloads();
    filterSelectedWorkloadsByNamespace(selectedWorkloads);
  }, [selectedNamespaces, selectedCluster, open]);

  useEffect(() => {
    if (selectedCluster.value) {
      setIsNamespacesLoading(true);
      k8sApi
        .getK8sNamespaceNames(selectedCluster.value)
        .then((res) => {
          const namespaces = res.data.namespaces.map((item) => ({
            label: item,
            value: item,
          }));
          setNamespaceOptions(namespaces);
        })
        .catch((error) => {
          console.error('Error loading namespaces:', error);
          setNamespaceOptions([]);
        })
        .finally(() => {
          setIsNamespacesLoading(false);
        });
    } else {
      setNamespaceOptions([]);
      setIsNamespacesLoading(false);
    }
  }, [selectedCluster]);

  const handleChangeCluster = (value: ClusterDetails) => {
    if (value.value != selectedCluster.value) {
      setSelectedWorkloads([]);
      setNamespaceOptions([]);
      setSelectedCluster(value);
    }
  };

  const checkWorkloadSelected = (workload: WorkloadDetails) => {
    if (
      selectedWorkloads.find((item) => item.label == workload.label && item.namespace == workload.namespace && item.accountId == workload.accountId)
    ) {
      return true;
    }
    return false;
  };

  return (
    <Modal
      width='lg'
      open={open}
      handleClose={clearAllAndClose}
      title={isUpdateGroup ? 'Update Grouping' : 'Create Grouping'}
      loader={isSubmitting}
      actionButtons={<ActionButtons buttons={buttons} selectedWorkloadCount={selectedWorkloads.length} />}
      sx={{
        '& .MuiPaper-root': {
          maxWidth: ds.space.mul(0, 505),
          '& .MuiDialogContent-root': {
            padding: 'var(--ds-space-4) var(--ds-space-5)',
          },
        },
      }}
    >
      <Box sx={{ pb: 'var(--ds-space-6)' }}>
        <Box display='flex' flexDirection={'column'} gap={ds.space.mul(0, 10)}>
          <Box
            sx={{
              borderRadius: 'var(--ds-radius-sm)',
              borderTop: '1px solid var(--ds-blue-200))',
              background: 'var(--ds-blue-100)',
              padding: 'var(--ds-space-2) var(--ds-space-4)',
            }}
          >
            <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
              Details
            </Typography>
          </Box>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 2fr', columnGap: 'var(--ds-space-4)', padding: '0px var(--ds-space-3)' }}>
            <Input
              id='grouping-name'
              label='Grouping Name'
              placeholder='Enter Name'
              size='sm'
              value={groupName}
              onChange={(next) => {
                setGroupName(next);
                textValidation(next, validationError, setValidationError, 'groupName', ['required', 'firstLetterAlpha']);
              }}
              error={validationError.groupName || undefined}
              required
            />
            <Input
              id='short-description'
              label='Short Description'
              placeholder='Description'
              size='sm'
              value={groupDesc}
              onChange={(next) => setGroupDesc(next)}
            />
          </Box>

          <Box
            sx={{
              borderRadius: 'var(--ds-radius-sm)',
              borderTop: '1px solid var(--ds-blue-200))',
              background: 'var(--ds-blue-100)',
              padding: 'var(--ds-space-2) var(--ds-space-4)',
            }}
          >
            <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
              Application Selection
            </Typography>
          </Box>
          <Box
            display={'flex'}
            gap={ds.space[3]}
            sx={{
              padding: 'var(--ds-space-2) var(--ds-space-4)',
              '& .MuiTextField-root': {
                marginTop: 'var(--ds-space-2)',
              },
            }}
          >
            <Select
              label='Cluster'
              value={selectedCluster.value || ''}
              options={clusters}
              onChange={(next) => {
                const cluster = clusters.find((cluster: any) => cluster.value === next) || { label: '', value: '' };
                handleChangeCluster(cluster);
              }}
              loading={isClustersLoading}
            />
            <Select
              multiple
              label='Namespaces'
              options={namespaceOptions}
              value={selectedNamespaces}
              onChange={(next) => {
                setSelectedNamespaces(next);
              }}
              maxChips={1}
              loading={isNamespacesLoading}
            />
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: 'var(--ds-space-4)' }}>
              <Box>
                <Box
                  sx={{
                    borderRadius: 'var(--ds-radius-sm)',
                    borderTop: '1px solid var(--ds-blue-200))',
                    background: 'var(--ds-gray-100)',
                    padding: 'var(--ds-space-2) var(--ds-space-4)',
                    display: 'flex',
                    alignItems: 'center',
                    height: ds.space.mul(0, 18),
                    '& .MuiCheckbox-root': {
                      padding: '0px var(--ds-space-2) 0px 0px !important',
                    },
                  }}
                >
                  <Checkbox size='sm' checked={selectAllChecked} onChange={() => handleSelectAllCheckbox()} aria-label='Select all applications' />
                  <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                    Listed Applications - {isRelevantWorkloads ? 0 : relevantWorkloads?.length}
                  </Typography>
                </Box>
                <Box height={ds.space[2]} />
                <Box
                  height={ds.space.mul(0, 120)}
                  sx={{
                    overflowY: 'scroll',
                    '&::-webkit-scrollbar': { width: ds.space[1] },
                    '&::-webkit-scrollbar-thumb': { backgroundColor: 'var(--ds-gray-200)' },
                    '&::-webkit-scrollbar-track': { backgroundColor: 'transparent' },
                  }}
                >
                  {isRelevantWorkloads && (
                    <Box display='flex' justifyContent='center' alignItems='center' height='100%'>
                      <CircularProgress color='inherit' size={20} />
                    </Box>
                  )}
                  {!isRelevantWorkloads &&
                    relevantWorkloads?.map((workload: any) => (
                      <Box
                        key={''}
                        display={'flex'}
                        alignItems={'flex-start'}
                        sx={{
                          padding: 'var(--ds-space-1) var(--ds-space-4)',
                          '&:hover': {
                            bgcolor: 'var(--ds-background-200)',
                            cursor: 'pointer',
                          },
                          '& .MuiCheckbox-root': {
                            padding: 'var(--ds-space-1) var(--ds-space-2) 0px 0px !important',
                            borderRadius: 'var(--ds-radius-lg)',
                          },
                        }}
                        onClick={() => handleCheckboxChange(workload)}
                      >
                        <Checkbox
                          size='sm'
                          checked={checkWorkloadSelected(workload)}
                          onChange={() => handleCheckboxChange(workload)}
                          aria-label={workload.label}
                        />
                        <Box>
                          <Typography
                            sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)' }}
                          >
                            {workload.label}
                          </Typography>
                          <Typography
                            sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}
                          >
                            ns: {workload.namespace} | cl: {workload.account_name}
                          </Typography>
                        </Box>
                      </Box>
                    ))}
                </Box>
              </Box>
              <Box>
                <Box
                  sx={{
                    borderRadius: 'var(--ds-radius-sm)',
                    borderTop: '1px solid var(--ds-blue-200))',
                    background: 'var(--ds-blue-100)',
                    padding: 'var(--ds-space-2) var(--ds-space-4)',
                    height: ds.space.mul(0, 18),
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                  }}
                >
                  <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                    Applications selected - {selectedWorkloads.length}
                  </Typography>
                  <DsButton tone='link' size='sm' onClick={() => setSelectedWorkloads([])}>
                    Clear all
                  </DsButton>
                </Box>
                <Box height={ds.space[2]} />
                <Box
                  height={ds.space.mul(0, 120)}
                  sx={{
                    overflowY: 'scroll',
                    '&::-webkit-scrollbar': { width: ds.space[1] },
                    '&::-webkit-scrollbar-thumb': { backgroundColor: 'var(--ds-gray-200)' },
                    '&::-webkit-scrollbar-track': { backgroundColor: 'transparent' },
                  }}
                >
                  {selectedWorkloads.map((item) => (
                    <Box
                      key={item?.label}
                      display={'flex'}
                      alignItems={'center'}
                      justifyContent={'space-between'}
                      sx={{
                        padding: 'var(--ds-space-1) var(--ds-space-4)',
                        '&:hover': {
                          bgcolor: 'var(--ds-background-200)',
                          cursor: 'pointer',
                          'img,svg': {
                            filter:
                              'brightness(0) saturate(100%) invert(72%) sepia(39%) saturate(7387%) hue-rotate(323deg) brightness(108%) contrast(103%)',
                          },
                        },
                        '& .MuiCheckbox-root': {
                          padding: 'var(--ds-space-1) var(--ds-space-2) 0px 0px !important',
                          borderRadius: 'var(--ds-radius-lg)',
                        },
                      }}
                    >
                      <Box>
                        <Typography
                          sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)' }}
                        >
                          {item?.label}
                        </Typography>
                        <Typography
                          sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}
                        >
                          ns: {item?.namespace} | cl: {item?.account_name}
                        </Typography>
                      </Box>
                      <Box>
                        <DsButton
                          id='remove-selected-workload-button'
                          tone='ghost'
                          composition='icon-only'
                          aria-label='Remove workload'
                          onClick={() => handleDelete(item)}
                          icon={<SafeIcon src={CrossIcon} alt='cross icon' />}
                        />
                      </Box>
                    </Box>
                  ))}
                </Box>
              </Box>
            </Box>
          </Box>
        </Box>
      </Box>
    </Modal>
  );
};

export default KubernetesInsertApplicationGroupingModal;
