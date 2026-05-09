import { Grid, Typography, TextField, Stack, InputAdornment, IconButton } from '@mui/material';
import { Visibility, VisibilityOff } from '@mui/icons-material';
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
import NDialog from '@components1/common/modal/NDialog';
import { useUpdateAllClusterOption } from '@components1/common/UpdateDataContext';
import { action } from 'src/utils/actionStyles';

const AzureIntegrationTile = () => {
  const [accountNameValue, setAccountNameValue] = useState('');
  const [tenantId, setTenantId] = useState('');
  const [clientId, setClientId] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [subscriptionId, setSubscriptionId] = useState('');
  const [showSecret, setShowSecret] = useState(false);

  const [validationError, setValidationError] = useState({});
  const [loading, setLoading] = useState(false);
  const [tableData, setTableData] = useState([]);
  const [openModal, setOpenModal] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const TABLE_HEADERS = ['Name', 'Created At', 'Created By', 'Account Number', 'Status', ''];
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

  const listAzureAccount = () => {
    setLoading(true);
    apiKubernetes1
      .listAcc({
        cloudProvider: 'Azure',
      })
      .then((res) => {
        const accounts = res?.data?.data?.get_cloud_accounts_v2?.rows || [];
        const data = accounts.map((item) => {
          return [
            {
              component: <Text value={item.account_name} />,
            },
            {
              component: <Datetime value={item.created_at} />,
            },
            {
              component: <Text value={item?.created_by_name || '-'} />,
            },
            {
              component: <Text value={item.account_number || '-'} />,
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
        snackbar.error('Failed to fetch Azure accounts.');
        console.error('Failed to fetch Azure accounts:', error);
        setTableData([]);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listAzureAccount();
  }, []);

  const clearForm = () => {
    setAccountNameValue('');
    setTenantId('');
    setClientId('');
    setClientSecret('');
    setSubscriptionId('');
    setShowSecret(false);
    setValidationError({});
  };

  const closeModal = () => {
    clearForm();
    setOpenModal(false);
    setIsSubmitting(false);
    listAzureAccount();
  };

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
          snackbar.error('Failed to Rename Azure Account');
        } else {
          snackbar.success('Azure Account Renamed successfully');
          closeRenameModal();
          listAzureAccount();
        }
      })
      .catch((error) => {
        console.error('Failed to rename account:', error);
        snackbar.error('Failed to Rename Azure Account');
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
          snackbar.error('Failed to Update Azure Account Status');
        } else {
          snackbar.success('Azure Account Status Updated successfully');
          listAzureAccount();
          updateAllClusters();
        }
      })
      .catch((error) => {
        console.error('Failed to update account status:', error);
        snackbar.error('Failed to Update Azure Account Status');
      })
      .finally(() => {
        setIsStatusUpdating(false);
        setUpdateAccountStatus({});
      });
  };

  const validateField = (name, value) => {
    if (name === 'subscriptionId' && !value) {
      setValidationError((prevState) => {
        const newState = { ...prevState };
        delete newState[name];
        return newState;
      });
      return;
    }

    let errorMsg = '';
    if (name !== 'subscriptionId' && !value) {
      errorMsg = 'This field is required';
    } else if (name === 'accountName' && !isK8sAccountNameValid(value)) {
      errorMsg =
        'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore';
    }

    setValidationError((prevState) => {
      const newState = { ...prevState };
      if (errorMsg) {
        newState[name] = errorMsg;
      } else {
        delete newState[name];
      }
      return newState;
    });
  };

  const handleChange = (name, value) => {
    // ... (rest of the function is unchanged)
    if (name === 'accountName') {
      setAccountNameValue(value);
    } else if (name === 'tenantId') {
      setTenantId(value);
    } else if (name === 'clientId') {
      setClientId(value);
    } else if (name === 'clientSecret') {
      setClientSecret(value);
    } else if (name === 'subscriptionId') {
      setSubscriptionId(value);
    }
    validateField(name, value);
  };

  const handleSubmit = () => {
    validateField('accountName', accountNameValue);
    validateField('tenantId', tenantId);
    validateField('clientId', clientId);
    validateField('clientSecret', clientSecret);
    const currentErrors = { ...validationError };
    if (!accountNameValue) {
      currentErrors.accountName = 'This field is required';
    }
    if (!tenantId) {
      currentErrors.tenantId = 'This field is required';
    }
    if (!clientId) {
      currentErrors.clientId = 'This field is required';
    }
    if (!clientSecret) {
      currentErrors.clientSecret = 'This field is required';
    }
    if (!isK8sAccountNameValid(accountNameValue)) {
      currentErrors.accountName =
        'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore';
    }

    setValidationError(currentErrors);

    if (Object.keys(currentErrors).length > 0) {
      snackbar.error('Please fill out all required fields correctly.');
      return;
    }

    setIsSubmitting(true);

    const body = {
      account_name: accountNameValue,
      cloud_provider: 'Azure',
      account_type: 'cloud',
      account_number: tenantId,
      access_key: clientId,
      access_secret: clientSecret,
      assume_role: subscriptionId || null,
    };

    apiAccount
      .createAccount(body)
      .then((res) => {
        if (res?.data?.status == 'ERROR') {
          snackbar.error(`Failed to Add Azure Account- ${res?.data?.message}`);
          return;
        }
        snackbar.success('Azure account added successfully.');
        closeModal();
      })
      .catch((error) => {
        snackbar.error('Failed to add Azure account.');
        console.error('Failed to add Azure account:', error);
      })
      .finally(() => {
        setIsSubmitting(false);
      });
  };

  const handleClickShowSecret = () => setShowSecret((show) => !show);
  const handleMouseDownPassword = (event) => {
    event.preventDefault();
  };

  const isSaveDisabled = isSubmitting;

  return (
    <>
      <Modal width='md' open={openModal} handleClose={closeModal} title={'Add Azure Account'} loader={isSubmitting}>
        <Grid container>
          <TextField
            sx={inputSx}
            value={accountNameValue}
            size='small'
            margin='normal'
            fullWidth
            id='account-name'
            label='Display Name'
            required
            onChange={(e) => handleChange('accountName', e.target.value)}
            onBlur={(e) => validateField('accountName', e.target.value)} // Validate on blur
            error={!!validationError.accountName}
            helperText={validationError.accountName}
          />
          <TextField
            sx={inputSx}
            value={tenantId}
            size='small'
            margin='normal'
            fullWidth
            id='tenant-id'
            label='Directory (tenant) ID'
            required
            onChange={(e) => handleChange('tenantId', e.target.value)}
            onBlur={(e) => validateField('tenantId', e.target.value)}
            error={!!validationError.tenantId}
            helperText={validationError.tenantId}
          />
          <TextField
            sx={inputSx}
            value={clientId}
            size='small'
            margin='normal'
            fullWidth
            id='client-id'
            label='Application (client) ID'
            required
            onChange={(e) => handleChange('clientId', e.target.value)}
            onBlur={(e) => validateField('clientId', e.target.value)}
            error={!!validationError.clientId}
            helperText={validationError.clientId}
          />
          <TextField
            sx={inputSx}
            value={clientSecret}
            size='small'
            margin='normal'
            fullWidth
            id='client-secret'
            label='Client Secret'
            required
            type={showSecret ? 'text' : 'password'}
            onChange={(e) => handleChange('clientSecret', e.target.value)}
            onBlur={(e) => validateField('clientSecret', e.target.value)}
            error={!!validationError.clientSecret}
            helperText={validationError.clientSecret}
            InputProps={{
              endAdornment: (
                <InputAdornment position='end'>
                  <IconButton
                    aria-label='toggle password visibility'
                    onClick={handleClickShowSecret}
                    onMouseDown={handleMouseDownPassword}
                    edge='end'
                  >
                    {showSecret ? <VisibilityOff /> : <Visibility />}
                  </IconButton>
                </InputAdornment>
              ),
            }}
          />
          <TextField
            sx={inputSx}
            value={subscriptionId}
            size='small'
            margin='normal'
            fullWidth
            id='subscription-id'
            label='Subscription ID (Optional)'
            onChange={(e) => handleChange('subscriptionId', e.target.value)}
            onBlur={(e) => validateField('subscriptionId', e.target.value)}
            error={!!validationError.subscriptionId}
            helperText={validationError.subscriptionId || "If empty, we'll try to find all accessible subscriptions."}
          />
        </Grid>

        <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
          <Grid item>
            <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={() => closeModal()} />
          </Grid>
          <Grid item>
            <CustomButton size='Medium' id={'create-azure-acc'} text='Save' disabled={isSaveDisabled} onClick={handleSubmit} />
          </Grid>
        </Grid>
      </Modal>

      <Modal width='sm' open={renameModalOpen} handleClose={closeRenameModal} title={'Rename Azure Account'} loader={renameLoading}>
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
            {updateAccountStatus.status == 'active' ? 'Enable' : 'Disable'} Azure Account
          </Typography>
        }
        dialogContent={`Are you sure you want to ${updateAccountStatus.status == 'active' ? 'enable' : 'disable'} "${
          updateAccountStatus.name
        }" the configured Azure Account?`}
        handleSubmit={handleUpdateAccountStatus}
        open={updateAccountStatus && Object.keys(updateAccountStatus).length > 0}
        loading={isStatusUpdating}
      />

      <Grid container padding='5px' mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={colors.text.secondary} fontSize='16px' fontWeight={600}>
                {'Azure'}
              </Typography>
              <CloudProviderIcon cloud_provider={'Azure'} />
            </Stack>
            {hasWriteAccess() && <CustomButton onClick={() => setOpenModal(true)} aria-label='Add Azure Account' text='Add Azure Account' />}
          </Stack>
        </Grid>
      </Grid>
      <BoxLayout2 id={'azure-integrations'} loading={loading} sharingOptions={false}>
        <CustomTable2 loading={loading} tableData={tableData} headers={TABLE_HEADERS} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </BoxLayout2>
    </>
  );
};

export default AzureIntegrationTile;
