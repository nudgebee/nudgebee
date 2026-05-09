import React, { useEffect, useRef, useState } from 'react';
import { Box, CircularProgress, FormControl, FormControlLabel, IconButton, Modal, Radio, RadioGroup } from '@mui/material';
import Typography from '@mui/material/Typography';
import TextField from '@mui/material/TextField';
import { useForm } from 'react-hook-form';
import apiUserManagement from '@api1/user';
import { textValidation } from '@lib/validation';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import { useSession } from 'next-auth/react';
import SafeIcon from '@components1/common/SafeIcon';
import { inputCustomSx } from '@data/themes/inputField';
import Title from '@components1/common/Title';
import PropTypes from 'prop-types';
import CustomButton from '@components1/common/NewCustomButton';
import { hasWriteAccess } from '@lib/auth';
import { colors } from 'src/utils/colors';
import { DeleteIconRed as DeleteIcon, modalerror } from '@assets';
import CustomLabels from '@components1/common/widgets/CustomLabels';

function GroupModal({ open, handleClose, groupData, handleSnackBarData }) {
  const { register, handleSubmit, reset } = useForm();
  const { data: currentUser } = useSession({
    required: true,
  });

  // Determine mode based on groupData presence
  const isEdit = groupData && Object.keys(groupData).length > 0;

  // Ref to prevent re-fetching group users after initial load
  const groupUsersLoaded = useRef(false);

  // Common state
  const [validationError, setValidationError] = useState({});
  const [users, setUsers] = useState([]);
  const [userOptions, setUserOptions] = useState([]);
  const [selectedUsers, setSelectedUsers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [groupNameValue, setGroupNameValue] = useState('');
  const [groupDescValue, setGroupDescValue] = useState('');

  // Edit mode specific state
  const [userStatusFilter, setUserStatusFilter] = useState('active');
  const [userAdded, setUserAdded] = useState(new Set());
  const [userRemoved, setUserRemoved] = useState(new Set());
  const [rbacType, setRbacType] = useState('tenant');
  const [groupRole, setGroupRole] = useState('');
  const [accountOptions, setAccountOptions] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [showSelectedAccounts, setShowSelectedAccounts] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState('');
  const [selectedAccountRole, setSelectedAccountRole] = useState('');
  const [accountNamespaceOptions, setAccountNamespaceOptions] = useState([]);
  const [accountNamespaceAdded, setAccountNamespaceAdded] = useState([]);
  const [accountNamespaceRemoved, setAccountNamespaceRemoved] = useState([]);
  const [showSelectedAccountNamespaces, setShowSelectedAccountNamespaces] = useState([]);
  const [selectedAccountNamespace, setSelectedAccountNamespace] = useState('');
  const [selectedAccountNamespaceRole, setSelectedAccountNamespaceRole] = useState('');

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
    }
  };

  const groupNameExists = async (name) => {
    let groupNameList = (await apiUserManagement.checkGroupNameExists(name))?.data;
    return !!groupNameList.length;
  };

  // Synchronous validation helper that returns error message or empty string
  const validateGroupName = (value) => {
    if (!value || value.toString().trim().length === 0) {
      return 'This field required';
    }
    const firstChar = value[0];
    const isAlpha = /^[A-Za-z]+$/i.test(firstChar);
    const isDigit = /^\d+$/i.test(firstChar);
    if (!isAlpha && !isDigit) {
      return 'Should start with an alphabet or a digit';
    }
    if (value.length < 5) {
      return 'Name should have atleast 5 characters';
    }
    if (!/^[a-z\d\-_\s]+$/i.test(value)) {
      return 'This field should be alpha-numeric';
    }
    return '';
  };

  function cleanState() {
    setUserStatusFilter('active');
    setValidationError({});
    setUsers([]);
    setUserOptions([]);
    setSelectedUsers([]);
    setGroupNameValue('');
    setGroupDescValue('');

    setUserAdded(new Set());
    setUserRemoved(new Set());
    setRbacType('tenant');
    setGroupRole('');
    setAccountOptions([]);
    setAccounts([]);
    setShowSelectedAccounts([]);
    setSelectedAccount('');
    setSelectedAccountRole('');
    setAccountNamespaceOptions([]);
    setAccountNamespaceAdded([]);
    setAccountNamespaceRemoved([]);
    setShowSelectedAccountNamespaces([]);
    setSelectedAccountNamespace('');
    setSelectedAccountNamespaceRole('');
    setLoading(false);
    setIsSubmitting(false);
    groupUsersLoaded.current = false;
    reset();
  }

  function adjustCloseAction(shouldUpdate = false) {
    cleanState();
    handleClose(shouldUpdate);
  }

  // Submit form handler for both add and edit modes
  const submitForm = async (data) => {
    const nameToValidate = isEdit ? groupNameValue : data.groupname;

    // Validate group name synchronously
    const groupNameError = validateGroupName(nameToValidate);
    if (groupNameError) {
      setValidationError({ groupname: groupNameError });
      return;
    }

    // Check if group name already exists (skip if edit mode and name unchanged)
    if (!isEdit || (isEdit && groupNameValue !== groupData.name)) {
      if (await groupNameExists(nameToValidate)) {
        setValidationError({ groupname: 'Group name already in use' });
        return;
      }
    }

    setValidationError({});

    if (isEdit) {
      // Edit mode logic
      setIsSubmitting(true);
      try {
        let formData = {
          id: groupData.id,
          name: groupNameValue,
          description: groupDescValue,
          role: groupRole || '',
        };

        // Update group name/description/tenantRole first
        if (
          formData.name != groupData.name ||
          formData.description != groupData.description ||
          groupData.group_roles?.filter((gr) => gr.entity_type == 'tenant' && gr.role == groupRole).length == 0
        ) {
          let resp = await apiUserManagement.updateUserGroup(formData);
          if (resp?.status !== 'success') {
            handleSnackBarData({ message: 'Failed to update group', severity: 'error' });
            setIsSubmitting(false);
            return;
          }
        }

        // Collect all update operations as promises
        const updatePromises = [];

        // Handle user additions and removals via manageGroupUsers
        if (userAdded?.size > 0 || userRemoved?.size > 0) {
          updatePromises.push(
            apiUserManagement.manageGroupUsers({
              group_id: groupData.id,
              add_usernames: [...userAdded],
              remove_usernames: [...userRemoved],
            })
          );
        }

        // Handle account roles
        const userGroupAccountObj = showSelectedAccounts.map((a) => ({
          account_id: a[0].drilldownQuery.id,
          role: a[1].text,
        }));
        if (userGroupAccountObj.length > 0) {
          updatePromises.push(apiUserManagement.upsertGroupAccountRoles({ group_id: groupData.id, account_roles: userGroupAccountObj }));
        }

        // Handle account namespace roles
        const userGroupAccountNamespaceObj = accountNamespaceAdded
          .filter((a) => {
            for (let aR of accountNamespaceRemoved) {
              if (aR.accountId == a.accountId && aR.namespace == a.namespace && aR.role == a.role) {
                return false;
              }
            }
            return true;
          })
          .map((a) => ({ account_id: a.accountId, role: a.role, namespace: a.namespace }));
        if (userGroupAccountNamespaceObj.length > 0) {
          updatePromises.push(
            apiUserManagement.upsertGroupAccountNamespaceRoles({
              group_id: groupData.id,
              k8saccount_namespace_roles: userGroupAccountNamespaceObj,
            })
          );
        }

        // Execute all updates and handle errors centrally
        if (updatePromises.length > 0) {
          await Promise.all(updatePromises);
        }

        handleSnackBarData({ message: 'Group updated successfully', severity: 'success' });
        adjustCloseAction(true);
      } catch (error) {
        console.error('Error updating group:', error);
        handleSnackBarData({ message: 'Failed to update group. Please try again.', severity: 'error' });
        setIsSubmitting(false);
      }
    } else {
      // Add mode logic
      setIsSubmitting(true);
      if (selectedUsers && selectedUsers.length > 0) {
        apiUserManagement
          .addUserGroup(data.groupname, data.description)
          .then((result) => {
            const group = result?.data?.data?.id;
            const usernames = selectedUsers.map((user) => user[1].drilldownQuery.username);
            if (usernames && usernames.length > 0) {
              apiUserManagement
                .manageGroupUsers({ group_id: group, add_usernames: usernames, remove_usernames: [] })
                .then(() => {
                  handleSnackBarData({
                    message: 'Group added successfully',
                    icon: '',
                    severity: 'success',
                  });
                  adjustCloseAction(true);
                })
                .catch(() => {
                  handleSnackBarData({
                    message: 'An error occurred',
                    severity: 'error',
                    icon: modalerror.default.src,
                  });
                  adjustCloseAction(false);
                });
            }
          })
          .catch(() => {
            handleSnackBarData({
              message: 'An error occurred',
              severity: 'error',
              icon: modalerror.default.src,
            });
            adjustCloseAction(false);
          });
      } else {
        apiUserManagement
          .addUserGroup(data.groupname, data.description)
          .then(() => {
            handleSnackBarData({
              message: 'Group added successfully',
              severity: 'success',
              icon: '',
            });
            adjustCloseAction(true);
          })
          .catch(() => {
            handleSnackBarData({
              message: 'An error occurred',
              severity: 'error',
              icon: modalerror.default.src,
            });
            adjustCloseAction(false);
          });
      }
    }
  };

  // Fetch users on open
  useEffect(() => {
    if (open) {
      apiUserManagement.listUsers({ status: 'active' }).then((res) => {
        setUsers(res?.data);
        const userOptions = res?.data
          ?.filter((m) => m.username != '')
          .map((u) => ({
            label: u.username,
            value: u.username,
          }));
        setUserOptions(userOptions);
      });

      // Edit mode: fetch accounts when rbac type is account or k8s_namespace
      if (isEdit && (rbacType == 'account' || rbacType == 'k8s_namespace')) {
        apiUserManagement.listAccounts().then((res) => {
          setAccounts(res);
          const allSelectedAccountIds = showSelectedAccounts.map((item) => item[0].drilldownQuery.id);
          const accountOptions = res
            ?.filter((m) => !allSelectedAccountIds.includes(m.id))
            .map((u) => ({
              label: u.account_name,
              value: u.id,
              cloud_provider: u.cloud_provider,
            }));
          setAccountOptions(accountOptions);
        });
      }

      // Edit mode: fetch k8s namespaces
      if (isEdit && rbacType == 'k8s_namespace') {
        apiUserManagement.listK8sNamespaces().then((res) => {
          setAccountNamespaceOptions(res?.k8s_namespaces?.rows ?? []);
        });
      }
    }
  }, [open, rbacType, isEdit]);

  // Edit mode: populate existing roles & data
  useEffect(() => {
    if (isEdit) {
      setGroupNameValue(groupData?.name);
      setGroupDescValue(groupData?.description);
      if (open && groupData?.group_roles?.length > 0) {
        if (accounts.length > 0) {
          for (let gr of groupData?.group_roles ?? []) {
            if (gr.entity_type == 'account') {
              handleAccountSelection(gr.entity_id, gr.role);
            } else if (gr.entity_type == 'k8s_namespace') {
              let entitySplits = gr.entity_id.split(':');
              handleAccountNamespaceSelection(entitySplits[0], entitySplits[1], gr.role);
            }
          }
        }
        const tenant = groupData?.group_roles.filter((gf) => gf.entity_type == 'tenant') || [];
        if (tenant && tenant.length > 0) {
          setGroupRole(tenant[0].role);
        }
      }
    }
  }, [open, groupData, accounts, isEdit]);

  // Edit mode: get existing users in the group (only once per modal open)
  useEffect(() => {
    if (isEdit && open && groupData.id && currentUser && !groupUsersLoaded.current) {
      groupUsersLoaded.current = true;
      const data = {
        offset: 0,
        limit: 100,
        id: groupData.id,
        isCountOnly: false,
      };

      setLoading(true);
      apiUserManagement.listUserGroupUsers(data).then((res) => {
        let result = res?.data?.usergroup_users;
        let alreadySelectedUsers = result.map((user) => {
          return [
            { text: user.user?.display_name },
            {
              text: user.user?.username,
              drilldownQuery: { username: user.user?.username },
              status: user.user.status,
            },
            { component: <CustomLabels margin='auto' text={user.user.status} /> },
            {
              component: (
                <IconButton
                  sx={{
                    border: `1px solid ${colors.border.vertical}`,
                    width: '36px',
                    height: '36px',
                    borderRadius: '4px',
                    color: colors.editIcon,
                  }}
                  onClick={() => handleUserDelete(user.user?.username)}
                  disabled={!hasWriteAccess() && currentUser.user.email == user.user.username}
                >
                  <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
                </IconButton>
              ),
            },
          ];
        });
        setLoading(false);
        setSelectedUsers(alreadySelectedUsers);
      });
    }
  }, [groupData, open, isEdit, currentUser]);

  // Add mode: handle user delete
  function handleDeleteAdd(username) {
    setSelectedUsers((prevSelectedUsers) => prevSelectedUsers.filter((user) => user[1].drilldownQuery.username !== username));
  }

  // Edit mode: handle user delete
  function handleUserDelete(username) {
    setSelectedUsers((prevSelectedUsers) => prevSelectedUsers.filter((user) => user[1].drilldownQuery.username !== username));
    if (userAdded.has(username)) {
      setUserAdded((prev) => {
        const next = new Set(prev);
        next.delete(username);
        return next;
      });
    } else {
      setUserRemoved((prev) => new Set([...prev, username]));
    }
  }

  // Add mode: handle user selection
  function handleUserSelectionAdd(value) {
    if (!value) {
      return;
    }
    const filterUser = users.find((u) => u.username === value);
    if (filterUser) {
      const newUser = [
        { text: filterUser.display_name },
        { text: filterUser.username, drilldownQuery: { username: filterUser.username }, status: filterUser.status },
        { component: <CustomLabels margin='auto' text={filterUser.status} /> },
        {
          component: (
            <IconButton
              sx={{
                border: `1px solid ${colors.border.vertical}`,
                width: '36px',
                height: '36px',
                borderRadius: '4px',
                color: colors.error,
              }}
              onClick={() => {
                handleDeleteAdd(filterUser.username);
              }}
            >
              <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
            </IconButton>
          ),
        },
      ];
      setSelectedUsers((prev) => {
        if (prev.some((user) => user[1].drilldownQuery.username === value)) return prev;
        return [...prev, newUser];
      });
    }
  }

  // Edit mode: handle user selection
  function handleUserSelectionEdit(value) {
    const filterUser = users.filter((u) => u.username === value)[0];
    if (filterUser) {
      const newUser = [
        { text: filterUser.display_name },
        { text: filterUser.username, drilldownQuery: { username: filterUser.username }, status: filterUser.status },
        { component: <CustomLabels margin='auto' text={filterUser.status} /> },
        {
          component: (
            <IconButton
              sx={{
                border: `1px solid ${colors.border.vertical}`,
                width: '36px',
                height: '36px',
                borderRadius: '4px',
                color: colors.error,
              }}
            >
              <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' onClick={() => handleUserDelete(filterUser.username)} />
            </IconButton>
          ),
        },
      ];
      setSelectedUsers((prev) => {
        if (prev.some((user) => user[1].drilldownQuery.username === value)) return prev;
        return [...prev, newUser];
      });
      setUserAdded((prev) => new Set([...prev, value]));
      setUserRemoved((prev) => {
        const next = new Set(prev);
        next.delete(value);
        return next;
      });
    }
  }

  // Combined user selection handler
  function handleUserSelection(value) {
    if (isEdit) {
      handleUserSelectionEdit(value);
    } else {
      handleUserSelectionAdd(value);
    }
  }

  // Edit mode: account selection handlers
  function handleAccountSelection(account, accountRole) {
    if (!account || !accountRole) {
      return;
    }

    const alreadyExists = showSelectedAccounts.filter((a) => a[0].drilldownQuery.id == account);
    if (alreadyExists.length > 0) {
      return;
    }

    const filterAccount = accounts.filter((u) => u.id === account)[0];
    if (filterAccount) {
      const newAccount = [
        {
          text: filterAccount.account_name,
          drilldownQuery: {
            id: filterAccount.id,
          },
        },
        { text: accountRole },
        {
          component: (
            <IconButton
              sx={{
                border: `1px solid ${colors.border.vertical}`,
                width: '36px',
                height: '36px',
                borderRadius: '4px',
                color: colors.error,
              }}
              onClick={() => handleAccountDelete(filterAccount.id)}
            >
              <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
            </IconButton>
          ),
        },
      ];
      setShowSelectedAccounts((prevAccounts) => [...prevAccounts, newAccount]);
      setSelectedAccount('');
      setSelectedAccountRole('');
    }
  }

  function handleAccountNamespaceSelection(accountId, namespace, namespaceRole) {
    if (!accountId || !namespaceRole || !namespace) {
      return;
    }

    const existingRecord = accountNamespaceAdded.filter((a) => a.accountId === accountId && a.namespace === namespace && a.role === namespaceRole);
    const filterAccount = accounts.filter((u) => u.id === accountId)[0];
    if (existingRecord.length == 0 && filterAccount) {
      const newAccountNamespace = [
        {
          text: filterAccount.account_name,
          drilldownQuery: {
            id: filterAccount.id,
            namespace: namespace,
            role: namespaceRole,
          },
        },
        { text: namespace },
        { text: namespaceRole },
        {
          component: (
            <IconButton
              sx={{
                border: `1px solid ${colors.border.vertical}`,
                width: '36px',
                height: '36px',
                borderRadius: '4px',
                color: colors.error,
              }}
              onClick={() => handleAccountNamespaceDelete(accountId, namespace, namespaceRole)}
            >
              <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
            </IconButton>
          ),
        },
      ];
      setAccountNamespaceAdded((prevAccounts) => [...prevAccounts, { accountId: accountId, role: namespaceRole, namespace: namespace }]);
      setShowSelectedAccountNamespaces((prevAccounts) => [...prevAccounts, newAccountNamespace]);
      let indexToRemove = -1;
      for (let i = 0; i < accountNamespaceRemoved.length; i++) {
        if (
          accountNamespaceRemoved[i].accountId == accountId &&
          accountNamespaceRemoved[i].namespace == namespace &&
          accountNamespaceRemoved[i].role == namespaceRole
        ) {
          indexToRemove = i;
          break;
        }
      }
      if (indexToRemove !== -1) {
        accountNamespaceRemoved.splice(indexToRemove, 1);
      }
      setSelectedAccount('');
      setSelectedAccountNamespace('');
      setSelectedAccountNamespaceRole('');
    }
  }

  function handleAccountDelete(id) {
    setShowSelectedAccounts((prevSelectedAccounts) => prevSelectedAccounts.filter((account) => account[0].drilldownQuery.id !== id));
  }

  function handleAccountNamespaceDelete(accountId, namespace, namespaceRole) {
    setShowSelectedAccountNamespaces((prevSelectedAccounts) =>
      prevSelectedAccounts.filter(
        (account) =>
          !(
            account[0].drilldownQuery.id == accountId &&
            account[0].drilldownQuery.namespace == namespace &&
            account[0].drilldownQuery.role == namespaceRole
          )
      )
    );
    let indexToRemove = -1;
    for (let i = 0; i < accountNamespaceAdded.length; i++) {
      if (
        accountNamespaceAdded[i].accountId == accountId &&
        accountNamespaceAdded[i].namespace == namespace &&
        accountNamespaceAdded[i].role == namespaceRole
      ) {
        indexToRemove = i;
        break;
      }
    }
    if (indexToRemove !== -1) {
      setAccountNamespaceAdded(accountNamespaceAdded.splice(indexToRemove, 1));
    } else {
      setAccountNamespaceRemoved([...accountNamespaceRemoved, { accountId: accountId, role: namespaceRole, namespace: namespace }]);
    }
  }

  const getStatusFilterSx = (status) => ({
    px: '12px',
    py: '4px',
    fontSize: '12px',
    fontFamily: 'inherit',
    fontWeight: userStatusFilter === status ? 600 : 400,
    borderRadius: '20px',
    border: userStatusFilter === status ? `1.5px solid ${colors.background.primary}` : `1px solid ${colors.border.secondary}`,
    backgroundColor: userStatusFilter === status ? colors.background.primaryLightest : 'transparent',
    color: userStatusFilter === status ? colors.background.primary : colors.text.secondary,
    cursor: 'pointer',
    textTransform: 'capitalize',
    transition: 'all 0.15s ease',
    outline: 'none',
  });

  // Derive active-only value for FilterDropdownButton (table shows all including inactive)
  const activeUsernames = new Set(userOptions?.map((u) => u.value) ?? []);
  const autocompleteValue = selectedUsers
    .filter((user) => activeUsernames.has(user[1].drilldownQuery.username))
    .map((user) => ({
      label: user[1].text,
      value: user[1].drilldownQuery.username,
    }));

  return (
    <Modal
      open={open}
      onClose={() => adjustCloseAction(false)}
      sx={{ overflow: 'auto', width: '580px', margin: 'auto', top: isEdit ? undefined : '10vh', maxHeight: '600px' }}
      disableEscapeKeyDown
      disableBackdropClick
      slotProps={{
        backdrop: {
          style: { pointerEvents: 'none' },
        },
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', background: colors.background.white, borderRadius: '8px', outline: 'none' }}>
        <Box
          sx={{
            background: colors.background.primaryLightest,
            padding: '16px 0px 16px 32px',
            borderRadius: '8px 8px 0px 0px',
            borderBottom: `1px solid ${isEdit ? colors.border.tertiaryBorder : colors.border.tertiary}`,
            boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.10), 0px 2px 4px -2px rgba(0, 0, 0, 0.10)',
          }}
        >
          <Typography sx={{ color: colors.text.secondary, fontFamily: 'Roboto', fontSize: '16px', fontWeight: 600 }}>
            {isEdit ? 'Edit Group' : 'Add User Group'}
          </Typography>
        </Box>
        <Box
          onSubmit={handleSubmit(submitForm)}
          onKeyDown={handleKeyDown}
          component='form'
          sx={
            isEdit
              ? {
                  '& .MuiTextField-root': { p: '0px 0px', marginLeft: '32px', marginBottom: '12px' },
                  '& .MuiFormControl-root': { marginTop: '16px' },
                  '& .MuiSelect-select': { padding: '8.5px 16px' },
                  width: '100%',
                  height: '100%',
                  display: 'flex',
                  flexDirection: 'column',
                }
              : {
                  '& .MuiTextField-root': { p: '0px 0px', marginBottom: '12px' },
                  '& .MuiFormControl-root': { marginTop: '16px' },
                  '& .MuiInputLabel-root': { fontSize: '17px' },
                  height: '100%',
                  margin: 'auto',
                  width: '510px',
                }
          }
        >
          <Box>
            {isEdit ? (
              <>
                <TextField
                  sx={{ ...inputCustomSx, maxWidth: '500px' }}
                  value={groupNameValue || ''}
                  size='small'
                  margin='normal'
                  fullWidth
                  id='groupname'
                  label='Group Name'
                  type='text'
                  {...register('groupname', {
                    onChange: (e) => setGroupNameValue(e.target.value),
                  })}
                  onKeyDown={handleKeyDown}
                  onKeyUp={(e) =>
                    textValidation(e.target.value, validationError, setValidationError, 'groupname', [
                      'required',
                      'alphaNumWithSpace',
                      'firstLetterAlphaNum',
                      'minlength5',
                    ])
                  }
                  helperText={validationError.groupname}
                  error={validationError.groupname}
                />
                <TextField
                  sx={{ ...inputCustomSx, maxWidth: '500px' }}
                  value={groupDescValue || ''}
                  rows={4}
                  size='small'
                  margin='normal'
                  fullWidth
                  id='description'
                  label='Description  (Optional)'
                  type='text'
                  {...register('description', {
                    onChange: (e) => setGroupDescValue(e.target.value),
                  })}
                  onKeyDown={handleKeyDown}
                />
              </>
            ) : (
              <>
                <TextField
                  size='small'
                  margin='normal'
                  fullWidth
                  id='groupname'
                  label='Group Name'
                  required
                  type='text'
                  {...register('groupname')}
                  onKeyDown={handleKeyDown}
                  onKeyUp={(e) =>
                    textValidation(e.target.value, validationError, setValidationError, 'groupname', [
                      'required',
                      'firstLetterAlphaNum',
                      'minlength5',
                      'alphaNumWithSpace',
                    ])
                  }
                  helperText={validationError.groupname}
                  error={validationError.groupname}
                />
                <TextField
                  size='small'
                  margin='normal'
                  multiline
                  rows={4}
                  fullWidth
                  id='description'
                  label='Description  (Optional)'
                  type='text'
                  {...register('description')}
                  onKeyDown={handleKeyDown}
                />
              </>
            )}
          </Box>

          {/* RBAC Section - Edit mode only */}
          {isEdit && (
            <>
              <br />
              <Box ml={'32px'} mr={'32px'}>
                <Title title={'Assign Roles'} />
                <FormControl>
                  <RadioGroup
                    value={rbacType}
                    onChange={(e) => setRbacType(e.target.value)}
                    onKeyDown={handleKeyDown}
                    row
                    aria-labelledby='demo-radio-buttons-group-label'
                    defaultValue='tenant'
                    name='radio-buttons-group'
                  >
                    <FormControlLabel value='tenant' control={<Radio />} label='Tenant' />
                    <FormControlLabel value='account' control={<Radio />} label='Account' />
                    <FormControlLabel value='k8s_namespace' control={<Radio />} label='K8s Namespace' />
                  </RadioGroup>
                </FormControl>
              </Box>
              {rbacType === 'tenant' && (
                <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                  <FilterDropdownButton
                    label='Tenant Role'
                    value={groupRole || null}
                    options={[
                      { label: 'Admin', value: 'tenant_admin' },
                      { label: 'ReadOnly Admin', value: 'tenant_admin_readonly' },
                    ]}
                    onSelect={(e) => setGroupRole(e.target?.value ?? null)}
                  />
                </Box>
              )}

              {rbacType === 'account' && (
                <Box>
                  <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                    <FilterDropdownButton
                      label='Accounts'
                      value={selectedAccount || null}
                      options={accountOptions}
                      onSelect={(e) => setSelectedAccount(e.target.value)}
                    />
                  </Box>
                  <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                    <FilterDropdownButton
                      label='Roles'
                      value={selectedAccountRole || null}
                      options={[
                        { label: 'Admin', value: 'account_admin' },
                        { label: 'ReadOnly Admin', value: 'account_admin_readonly' },
                      ]}
                      onSelect={(e) => setSelectedAccountRole(e.target.value)}
                    />
                  </Box>
                  <CustomButton
                    type='button'
                    text='Add'
                    sx={{
                      marginTop: '16px',
                      marginLeft: '32px',
                    }}
                    onClick={() => {
                      handleAccountSelection(selectedAccount, selectedAccountRole);
                    }}
                  />
                  {showSelectedAccounts.length > 0 ? (
                    <Box sx={{ margin: '0px 32px' }}>
                      <CustomTable2
                        tableData={showSelectedAccounts}
                        headers={[{ name: 'Account Name', width: '40%' }, 'Role', '']}
                        id='selected-accounts'
                        showExpandable={false}
                        rowsPerPage={showSelectedAccounts.length}
                        totalRows={showSelectedAccounts.length}
                        loading={loading}
                        showEmptyStateText={true}
                      />
                    </Box>
                  ) : null}
                </Box>
              )}

              {rbacType === 'k8s_namespace' && (
                <Box>
                  <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                    <FilterDropdownButton
                      label='K8s Accounts'
                      value={selectedAccount || null}
                      options={accountOptions?.filter((a) => a.cloud_provider == 'K8s')}
                      onSelect={(e) => setSelectedAccount(e.target.value)}
                    />
                  </Box>
                  <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                    <FilterDropdownButton
                      label='Namespace'
                      value={selectedAccountNamespace || null}
                      options={accountNamespaceOptions?.filter((a) => a.account_id == selectedAccount).map((a) => ({ label: a.name, value: a.name }))}
                      onSelect={(e) => setSelectedAccountNamespace(e.target.value)}
                    />
                  </Box>
                  <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                    <FilterDropdownButton
                      label='Roles'
                      value={selectedAccountNamespaceRole || null}
                      options={[
                        { label: 'Admin', value: 'k8s_namespace_admin' },
                        { label: 'ReadOnly Admin', value: 'k8s_namespace_admin_readonly' },
                      ]}
                      onSelect={(e) => setSelectedAccountNamespaceRole(e.target.value)}
                    />
                  </Box>
                  <CustomButton
                    type='button'
                    text='Add'
                    sx={{
                      marginTop: '16px',
                      marginLeft: '32px',
                    }}
                    onClick={() => {
                      handleAccountNamespaceSelection(selectedAccount, selectedAccountNamespace, selectedAccountNamespaceRole);
                    }}
                  />
                  {showSelectedAccountNamespaces.length > 0 ? (
                    <Box sx={{ margin: '0px 32px' }}>
                      <CustomTable2
                        tableData={showSelectedAccountNamespaces}
                        headers={[{ name: 'Account Name', width: '40%' }, 'Namespace', 'Role', '']}
                        id='selected-account-namespaces'
                        showExpandable={false}
                        rowsPerPage={showSelectedAccountNamespaces.length}
                        totalRows={showSelectedAccountNamespaces.length}
                        loading={loading}
                        showEmptyStateText={true}
                      />
                    </Box>
                  ) : null}
                </Box>
              )}
              <br />
              <Box ml={'32px'} mr={'32px'}>
                <Title title={'Assign Users'} />
              </Box>
            </>
          )}

          {/* User selection section */}
          <Box>
            <Box sx={{ ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
              <FilterDropdownButton
                id='all-users-for-group'
                label={isEdit ? 'Add User' : 'Select User'}
                value={autocompleteValue}
                options={userOptions}
                multiple
                limitTag={2}
                onSelect={(event) => {
                  const newValues = event.target.value;
                  if (!Array.isArray(newValues)) return;
                  const newUsernames = newValues.map((v) => (typeof v === 'object' ? v.value : v));
                  // Diff only against active users — inactive users in the table are unaffected
                  const oldActiveUsernames = autocompleteValue.map((u) => u.value);
                  newUsernames.filter((u) => !oldActiveUsernames.includes(u)).forEach((u) => handleUserSelection(u));
                  oldActiveUsernames
                    .filter((u) => !newUsernames.includes(u))
                    .forEach((u) => {
                      if (isEdit) {
                        handleUserDelete(u);
                      } else {
                        handleDeleteAdd(u);
                      }
                    });
                }}
              />
            </Box>
            {isEdit ? (
              <Box sx={{ margin: '0px 32px' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mb: '10px', mt: '8px' }}>
                  {['active', 'inactive'].map((status) => (
                    <Box key={status} component='button' type='button' onClick={() => setUserStatusFilter(status)} sx={getStatusFilterSx(status)}>
                      {status}
                    </Box>
                  ))}
                </Box>
                <CustomTable2
                  tableData={selectedUsers.filter((u) => u[1].status === userStatusFilter)}
                  headers={[{ name: 'Display Name', width: '40%' }, 'Username', 'Status', '']}
                  id='selected-users'
                  showExpandable={false}
                  rowsPerPage={selectedUsers.length}
                  totalRows={selectedUsers.length}
                  loading={loading}
                  showEmptyStateText={true}
                />
              </Box>
            ) : (
              selectedUsers.length > 0 && (
                <CustomTable2
                  tableData={selectedUsers}
                  headers={[{ name: 'Display Name', width: '40%' }, 'Username', 'Status', '']}
                  id='selected-users'
                  showExpandable={false}
                  rowsPerPage={selectedUsers.length}
                  totalRows={selectedUsers.length}
                  showEmptyStateText={true}
                />
              )
            )}
          </Box>

          {/* Action buttons */}
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'flex-end',
              marginRight: isEdit ? '32px' : undefined,
              marginY: '20px',
              gap: '12px',
              button: {
                minWidth: '140px',
              },
            }}
          >
            <CustomButton
              id='cancel'
              text={'Cancel'}
              variant='secondary'
              size='Medium'
              onClick={() => {
                adjustCloseAction(false);
              }}
            />
            <CustomButton
              type='submit'
              text={isEdit ? 'Update' : 'Create Group'}
              id='submit'
              size='Medium'
              disabled={isSubmitting}
              endIcon={isSubmitting ? <CircularProgress color='secondary' size={20} thickness={4} /> : null}
            />
          </Box>
        </Box>
      </Box>
    </Modal>
  );
}

GroupModal.propTypes = {
  open: PropTypes.bool,
  handleClose: PropTypes.func,
  groupData: PropTypes.object,
  handleSnackBarData: PropTypes.func,
};

export default GroupModal;
