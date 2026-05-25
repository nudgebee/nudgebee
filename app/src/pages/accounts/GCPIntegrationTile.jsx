import { Grid, Typography, TextField, Stack } from '@mui/material';
import React, { useEffect, useState } from 'react';
import apiAccount from '@api1/account';
import { Modal } from '@components1/common/modal';
import { isK8sAccountNameValid } from 'src/utils/common';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import Text from '@components1/common/format/Text';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import apiKubernetes1 from '@api1/kubernetes1';
import { hasWriteAccess } from '@lib/auth';
import { BoxLayout2, ThreeDotsMenu } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import { inputSx } from '@data/themes/inputField';
import { action } from 'src/utils/actionStyles';
import NDialog from '@components1/common/modal/NDialog';
import { useUpdateAllClusterOption } from '@components1/common/UpdateDataContext';
import AddGcpAccountModal from './AddGcpAccountModal';

const GCPIntegrationTile = () => {
  const [loading, setLoading] = useState(false);
  const [tableData, setTableData] = useState([]);
  const [openModal, setOpenModal] = useState(false);
  const TABLE_HEADERS = ['Name', 'Created At', 'Created By', 'Project ID', 'Status', ''];
  const [updateAccountStatus, setUpdateAccountStatus] = useState({});
  const [isStatusUpdating, setIsStatusUpdating] = useState(false);
  const [renameModalOpen, setRenameModalOpen] = useState(false);
  const [selectedAccount, setSelectedAccount] = useState(null);
  const [newAccountName, setNewAccountName] = useState('');
  const [renameLoading, setRenameLoading] = useState(false);
  const [renameError, setRenameError] = useState('');
  const updateAllClusters = useUpdateAllClusterOption();

  const getMenuItems = (item) => {
    if (!hasWriteAccess()) {
      return [];
    }
    return [
      {
        label: 'Rename Account Name',
        id: 0,
      },
      {
        label: item.status == 'disabled' ? 'Enable' : 'Disable',
        id: 1,
      },
    ];
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setSelectedAccount(data);
      setNewAccountName(data.account_name);
      setRenameModalOpen(true);
      setRenameError('');
    } else if (menuItem.id === 1) {
      setUpdateAccountStatus({ name: data.account_name, id: data.id, status: data.status == 'disabled' ? 'active' : 'disabled' });
    }
  };

  const listGCPAccount = () => {
    setLoading(true);
    apiKubernetes1
      .listGCPAcc()
      .then((res) => {
        const accounts = res?.data?.data?.cloud_accounts || [];
        const data = accounts.map((item) => {
          return [
            {
              component: <Text value={item.account_name} />,
            },
            {
              component: <Datetime value={item.created_at} />,
            },
            {
              component: <Text value={item.user?.display_name || '-'} />,
            },
            {
              component: <Text value={item.account_number} />,
            },
            {
              component: <CustomLabels text={item.status || '-'} />,
            },
            {
              component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />,
            },
          ];
        });
        setTableData(data);
      })
      .catch((error) => {
        snackbar.error('Failed to fetch GCP accounts.');
        console.error('Failed to fetch GCP accounts:', error);
        setTableData([]);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listGCPAccount();
  }, []);

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
          listGCPAccount();
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
          listGCPAccount();
          updateAllClusters();
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

  return (
    <>
      <AddGcpAccountModal
        open={openModal}
        onClose={(wasSuccessful) => {
          setOpenModal(false);
          if (wasSuccessful) listGCPAccount();
        }}
      />

      <Modal width='sm' open={renameModalOpen} handleClose={closeRenameModal} title={'Rename GCP Account'} loader={renameLoading}>
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
            <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={closeRenameModal} />
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
            {updateAccountStatus.status == 'active' ? 'Enable' : 'Disable'} GCP Account
          </Typography>
        }
        dialogContent={`Are you sure you want to ${updateAccountStatus.status == 'active' ? 'enable' : 'disable'} "${
          updateAccountStatus.name
        }" the configured GCP Account?`}
        handleSubmit={handleUpdateAccountStatus}
        open={updateAccountStatus && Object.keys(updateAccountStatus).length > 0}
        loading={isStatusUpdating}
      />
      <Grid container padding='5px' mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={colors.text.secondary} fontSize='16px' fontWeight={600}>
                {'Google Cloud Platform'}
              </Typography>
              <CloudProviderIcon cloud_provider={'GCP'} />
            </Stack>
            {hasWriteAccess() && <CustomButton onClick={() => setOpenModal(true)} aria-label='Add GCP Account' text='Add GCP Account' />}
          </Stack>
        </Grid>
      </Grid>
      <BoxLayout2 id={'gcp-integrations'} loading={loading} sharingOptions={false}>
        <CustomTable2 loading={loading} tableData={tableData} headers={TABLE_HEADERS} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </BoxLayout2>
    </>
  );
};

export default GCPIntegrationTile;
