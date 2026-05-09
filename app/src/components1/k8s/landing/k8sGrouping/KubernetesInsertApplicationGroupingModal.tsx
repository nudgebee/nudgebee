import React, { useState, useEffect } from 'react';
import { Box, OutlinedInput, Typography, Checkbox, FormHelperText, CircularProgress } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { CrossIcon } from '@assets';
import { Modal } from '@components1/common/modal';
import CustomIconButton from '@components1/CustomIconButton';
import apiHome from '@api1/home';
import k8sApi from '@api1/kubernetes';
import apiAppGrouping from '@api1/application-groupings';
import { textValidation } from '@lib/validation';
import CustomButton from '@components1/common/NewCustomButton';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';

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
        height: '56px',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: '10px',
        flexShrink: 0,
        paddingX: '10px',
      }}
    >
      <Box>
        <Typography
          sx={{
            display: 'flex',
            alignItems: 'center',
            color: '#374151',
            fontSize: '16px',
            fontWeight: '500',
            span: {
              backgroundColor: '#E1EFFE',
              height: '28px',
              width: '27px',
              borderRadius: '4px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              ml: '4px',
            },
          }}
        >
          Total Application Selected - <span>{selectedWorkloadCount}</span>
        </Typography>
      </Box>

      <Box sx={{ display: 'flex', gap: '6px', alignItems: 'center', button: { minWidth: '140px' } }}>
        {rightButtons.map((button: any) => (
          <React.Fragment key={button.label}>
            <CustomButton
              size='Medium'
              text={button.label}
              onClick={() => {
                button.onClick();
              }}
              variant={button.label == 'Cancel' ? 'secondary' : 'primary'}
              disabled={button.isDisabled}
            />
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
  const [selectedNamespaceOptions, setSelectedNamespaceOptions] = useState<any[]>([]);
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
          } else if (res?.data?.data?.application_group_update) {
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
          } else if (res?.data?.data?.application_group_create) {
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
          label: getAccountNameById(groupData.application_group_mappings[0].account_id),
          value: groupData.application_group_mappings[0].account_id,
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
      color: '#3B82F6',
      activeColor: '#3B82F6',
      onClick: clearAllAndClose,
      isDisabled: isSubmitting,
    },

    {
      label: isUpdateGroup ? 'Update' : 'Create',
      backgroundColor: '#3B82F6',
      color: 'white',
      activeColor: '#3B82F6',
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
          maxWidth: '1010px',
          '& .MuiDialogContent-root': {
            padding: '16px 24px',
          },
        },
      }}
    >
      <Box sx={{ pb: '30px' }}>
        <Box display='flex' flexDirection={'column'} gap={'20px'}>
          <Box sx={{ borderRadius: '4px', borderTop: '1px solid #DBEAFE)', background: '#EFF6FF', padding: '8px 16px' }}>
            <Typography sx={{ color: '#374151', fontSize: '14px', fontWeight: 600 }}>Details</Typography>
          </Box>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 2fr', columnGap: '16px', padding: '0px 14px' }}>
            <Box>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <label htmlFor='grouping-name' style={{ color: '#737373', fontSize: '12px', fontWeight: 500 }}>
                  Grouping Name
                </label>
                <OutlinedInput
                  id='grouping-name'
                  placeholder='Enter Name*'
                  size='small'
                  value={groupName}
                  onChange={(e) => {
                    setGroupName(e?.target?.value);
                  }}
                  onKeyUp={(e: any) =>
                    textValidation(e.target.value, validationError, setValidationError, 'groupName', ['required', 'firstLetterAlpha'])
                  }
                  required
                />
                {validationError.groupName && <FormHelperText error>{validationError.groupName}</FormHelperText>}
              </Box>
            </Box>
            <Box>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <label htmlFor='short-description' style={{ color: '#737373', fontSize: '12px', fontWeight: 500 }}>
                  Short Description
                </label>
                <OutlinedInput
                  id='short-description'
                  placeholder='Description'
                  size='small'
                  value={groupDesc}
                  onChange={(e) => {
                    setGroupDesc(e?.target.value);
                  }}
                />
              </Box>
            </Box>
          </Box>

          <Box sx={{ borderRadius: '4px', borderTop: '1px solid #DBEAFE)', background: '#EFF6FF', padding: '8px 16px' }}>
            <Typography sx={{ color: '#374151', fontSize: '14px', fontWeight: 600 }}>Application Selection</Typography>
          </Box>
          <Box
            display={'flex'}
            gap={'12px'}
            sx={{
              padding: '8px 16px',
              '& .MuiTextField-root': {
                marginTop: '8px',
              },
            }}
          >
            <FilterDropdownButton
              label='Cluster'
              value={selectedCluster.value ? selectedCluster : null}
              options={clusters}
              onSelect={(event: any) => {
                const cluster = clusters.find((cluster: any) => cluster.value === event.target.value) || { label: '', value: '' };
                handleChangeCluster(cluster);
              }}
              isOptionsLoading={isClustersLoading}
            />
            <FilterDropdownButton
              multiple
              label='Namespaces'
              options={namespaceOptions}
              value={selectedNamespaceOptions}
              onSelect={(event: any) => {
                setSelectedNamespaceOptions(event.target.value);
                setSelectedNamespaces(event.target.value.map((e: any) => e.value) || []);
              }}
              limitTag={1}
              isOptionsLoading={isNamespacesLoading}
            />
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: '16px' }}>
              <Box>
                <Box
                  sx={{
                    borderRadius: '4px',
                    borderTop: '1px solid #DBEAFE)',
                    background: '#F3F3F3',
                    padding: '8px 16px',
                    display: 'flex',
                    alignItems: 'center',
                    height: '36px',
                    '& .MuiCheckbox-root': {
                      padding: '0px 10px 0px 0px !important',
                    },
                  }}
                >
                  <Checkbox checked={selectAllChecked} onChange={() => handleSelectAllCheckbox()} size='small' />
                  <Typography sx={{ color: '#374151', fontSize: '12px', fontWeight: 600 }}>
                    Listed Applications - {isRelevantWorkloads ? 0 : relevantWorkloads?.length}
                  </Typography>
                </Box>
                <Box height={'8px'} />
                <Box
                  height={'240px'}
                  sx={{
                    overflowY: 'scroll',
                    '&::-webkit-scrollbar': { width: '4px' },
                    '&::-webkit-scrollbar-thumb': { backgroundColor: '#EBEBEB' },
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
                          padding: '6px 16px',
                          '&:hover': {
                            bgcolor: '#F8F8F8',
                            cursor: 'pointer',
                          },
                          '& .MuiCheckbox-root': {
                            padding: '5px 10px 0px 0px !important',
                            borderRadius: '8px',
                          },
                        }}
                        onClick={() => handleCheckboxChange(workload)}
                      >
                        <Checkbox checked={checkWorkloadSelected(workload)} onChange={() => handleCheckboxChange(workload)} size='small' />
                        <Box>
                          <Typography sx={{ color: '#374151', fontSize: '13px', fontWeight: 400 }}>{workload.label}</Typography>
                          <Typography sx={{ color: '#9F9F9F', fontSize: '11px', fontWeight: 400 }}>
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
                    borderRadius: '4px',
                    borderTop: '1px solid #DBEAFE)',
                    background: '#EFF6FF',
                    padding: '8px 16px',
                    height: '36px',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                  }}
                >
                  <Typography sx={{ color: '#374151', fontSize: '12px', fontWeight: 600 }}>
                    Applications selected - {selectedWorkloads.length}
                  </Typography>
                  <CustomIconButton
                    onClick={() => setSelectedWorkloads([])}
                    sx={{
                      '&:hover': {
                        cursor: 'pointer',
                        '& p': {
                          color: '#FF1744',
                        },
                      },
                    }}
                  >
                    <Typography
                      sx={{
                        color: '#374151',
                        fontSize: '12px',
                        fontWeight: 600,
                      }}
                    >
                      Clear all
                    </Typography>
                  </CustomIconButton>
                </Box>
                <Box height={'8px'} />
                <Box
                  height={'240px'}
                  sx={{
                    overflowY: 'scroll',
                    '&::-webkit-scrollbar': { width: '4px' },
                    '&::-webkit-scrollbar-thumb': { backgroundColor: '#EBEBEB' },
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
                        padding: '6px 16px',
                        '&:hover': {
                          bgcolor: '#F8F8F8',
                          cursor: 'pointer',
                          'img,svg': {
                            filter:
                              'brightness(0) saturate(100%) invert(72%) sepia(39%) saturate(7387%) hue-rotate(323deg) brightness(108%) contrast(103%)',
                          },
                        },
                        '& .MuiCheckbox-root': {
                          padding: '5px 10px 0px 0px !important',
                          borderRadius: '8px',
                        },
                      }}
                    >
                      <Box>
                        <Typography sx={{ color: '#374151', fontSize: '13px', fontWeight: 400 }}>{item?.label}</Typography>
                        <Typography sx={{ color: '#9F9F9F', fontSize: '11px', fontWeight: 400 }}>
                          ns: {item?.namespace} | cl: {item?.account_name}
                        </Typography>
                      </Box>
                      <Box>
                        <CustomIconButton id='remove-selected-workload-button' variant={'no-border-white'} onClick={() => handleDelete(item)}>
                          <SafeIcon src={CrossIcon} alt='cross icon' />
                        </CustomIconButton>
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
