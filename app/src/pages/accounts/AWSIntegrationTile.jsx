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
import MarkDowns from '@components1/common/MarkDowns';
import { inputSx } from '@data/themes/inputField';
import { action } from 'src/utils/actionStyles';
import NDialog from '@components1/common/modal/NDialog';
import { useUpdateAllClusterOption } from '@components1/common/UpdateDataContext';

const AWS_ARN_PREFIX = 'arn:aws:iam::';
const SETUP_INSTRUCTIONS = `### Step 1. Give Account Name
  ### Step 2. Click on Connect via AWS Console
     - It will get redirected to Cloud Formation link.
     - All the values are pre-filled. **DO NOT** change any value in the field.
     - Create the stack.
  ### Step 3. Wait until Stack is created successfully
  ### Step 4. After completing the Stack
     - Copy the role name and paste it in the role name box of Nudgebee add AWS account.
     - Hit Save button.`;

const AWSIntegrationTile = () => {
  const [accountNameValue, setAccountNameValue] = useState('');
  const [roleName, setRoleName] = useState('');
  const [validationError, setValidationError] = useState({});
  const [loading, setLoading] = useState(false);
  const [tableData, setTableData] = useState([]);
  const [accountUrl, setAccountUrl] = useState('');
  const [openModal, setOpenModal] = useState(false);
  const [bucketName, setBucketName] = useState('');
  const [_externalId, _setExternalId] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isFetchingCloudFormationUrl, setIsFetchingCloudFormationUrl] = useState(false);
  const TABLE_HEADERS = ['Name', 'Created At', 'Created By', 'Account Number', 'Access', 'Status', ''];
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

  const listAWSAccount = () => {
    setLoading(true);
    apiKubernetes1
      .listAWSAcc()
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
              component: <Text value={item.account_number || '-'} />,
            },
            {
              component: <CustomLabels text={item.account_access === 'readonly' ? 'Read-Only' : 'Standard'} />,
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
        snackbar.error('Failed to fetch AWS accounts.');
        console.error('Failed to fetch AWS accounts:', error);
        setTableData([]);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listAWSAccount();
  }, []);

  const closeModal = () => {
    setAccountNameValue('');
    setRoleName('');
    setAccountUrl('');
    setOpenModal(false);
    setIsSubmitting(false);
    setIsFetchingCloudFormationUrl(false);
    listAWSAccount();
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
          snackbar.error('Failed to Rename Account');
        } else {
          snackbar.success('Account Renamed successfully');
          closeRenameModal();
          listAWSAccount();
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
          listAWSAccount();
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

  const handleNavToAwsConsole = () => {
    setIsFetchingCloudFormationUrl(true);
    apiKubernetes1
      .getAWSCloudFormationURL({
        account_name: accountNameValue,
        account_type: 'cloud',
        cloud_provider: 'AWS',
      })
      .then((res) => {
        const cloudFormation = res?.data?.data?.aws_cloud_formation || {};
        if (cloudFormation && Object.keys(cloudFormation).length == 2) {
          setAccountUrl(cloudFormation.url);
          setBucketName(cloudFormation.bucket_name);
          window.open(cloudFormation.url, '_blank');
        } else {
          snackbar.error('Failed to get Cloud Formation URL');
        }
      })
      .catch(() => {
        snackbar.error('Failed to get Cloud Formation URL');
      })
      .finally(() => {
        setIsFetchingCloudFormationUrl(false);
      });
  };

  const handleRoleNameChange = (e) => {
    const value = e.target.value;
    setRoleName(value);
    if (value.length < 8 || value.startsWith(' ')) {
      setValidationError({
        roleName: 'Text must have a minimum length of 8 characters and should not start with an empty space',
      });
    } else {
      setValidationError((prevState) => {
        const newState = { ...prevState };
        delete newState.roleName;
        return newState;
      });
    }
  };

  const handleAWSAccountNameChange = (value) => {
    if (!isK8sAccountNameValid(value)) {
      setValidationError({
        awsAccountName:
          'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore',
      });
    } else {
      setValidationError((prevState) => {
        const newState = { ...prevState };
        delete newState.awsAccountName;
        return newState;
      });
    }
    setAccountNameValue(value);
  };

  const handleSubmit = (body) => {
    setIsSubmitting(true);
    apiAccount
      .createAccount(body)
      .then((res) => {
        if (res?.data?.status == 'ERROR') {
          snackbar.error(`Failed to Add AWS Account- ${res?.data?.message}`);
          return;
        }
        closeModal();
      })
      .finally(() => {
        setIsSubmitting(false);
      });
  };

  return (
    <>
      <Modal width='md' open={openModal} handleClose={closeModal} title={'Add AWS Account'} loader={isSubmitting}>
        <MarkDowns data={SETUP_INSTRUCTIONS} sx={{ width: 'auto' }} />
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
            onChange={(e) => handleAWSAccountNameChange(e.target.value)}
            error={!!validationError.awsAccountName}
            helperText={validationError.awsAccountName}
          />
          {accountUrl ? (
            <TextField
              value={roleName}
              size='small'
              margin='normal'
              fullWidth
              id='role-name'
              label='Role Name'
              required
              onChange={handleRoleNameChange}
              error={!!validationError.roleName}
              helperText={validationError.roleName}
              InputProps={{
                readOnly: false,
                startAdornment: <span style={{ color: 'gray' }}>{AWS_ARN_PREFIX}</span>,
              }}
            />
          ) : null}
          <CustomButton
            loading={isFetchingCloudFormationUrl}
            size='Medium'
            disabled={!!accountUrl || !!validationError.awsAccountName || !accountNameValue} // Also disable if name is invalid
            text='Connect via AWS Console'
            onClick={handleNavToAwsConsole}
          />
        </Grid>

        <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
          <Grid item>
            <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={() => closeModal()} />
          </Grid>
          <Grid item>
            <CustomButton
              size='Medium'
              id={'create-jira-acc'}
              text='Save'
              disabled={!accountNameValue || !roleName || isSubmitting || !!validationError.roleName} // Also disable if role name is invalid
              onClick={() => {
                handleSubmit({
                  account_name: accountNameValue,
                  assume_role: AWS_ARN_PREFIX + roleName,
                  data: {
                    cost_report_s3_bucket: bucketName,
                  },
                  cloud_provider: 'AWS',
                  account_type: 'cloud',
                });
              }}
            />
          </Grid>
        </Grid>
      </Modal>

      <Modal width='sm' open={renameModalOpen} handleClose={closeRenameModal} title={'Rename AWS Account'} loader={renameLoading}>
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
            {updateAccountStatus.status == 'active' ? 'Enable' : 'Disable'} AWS Account
          </Typography>
        }
        dialogContent={`Are you sure you want to ${updateAccountStatus.status == 'active' ? 'enable' : 'disable'} "${
          updateAccountStatus.name
        }" the configured AWS Account?`}
        handleSubmit={handleUpdateAccountStatus}
        open={updateAccountStatus && Object.keys(updateAccountStatus).length > 0}
        loading={isStatusUpdating}
      />
      <Grid container padding='5px' mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={colors.text.secondary} fontSize='16px' fontWeight={600}>
                {'Amazon Web Services'}
              </Typography>
              <CloudProviderIcon cloud_provider={'AWS'} />
            </Stack>
            {hasWriteAccess() && <CustomButton onClick={() => setOpenModal(true)} aria-label='Add AWS Account' text='Add AWS Account' />}
          </Stack>
        </Grid>
      </Grid>
      <BoxLayout2 id={'aws-integrations'} loading={loading} sharingOptions={false}>
        <CustomTable2 loading={loading} tableData={tableData} headers={TABLE_HEADERS} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </BoxLayout2>
    </>
  );
};

export default AWSIntegrationTile;
