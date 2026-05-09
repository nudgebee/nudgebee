import React, { useEffect, useState, useMemo } from 'react';
import { Box, Modal } from '@mui/material';
import Typography from '@mui/material/Typography';
import TextField from '@mui/material/TextField';
import { useForm } from 'react-hook-form';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import apiUserManagement from '@api1/user';
import { textValidation, emailValidation } from '@lib/validation';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';

function UserModal({ open, handleClose, handleSnackBarData, mode, userData }) {
  const { register, reset, handleSubmit } = useForm();
  const router = useRouter();
  const currentFragment = useMemo(() => {
    const hash = router.asPath.split('#')[1];
    return hash || 'users';
  }, [router.asPath]);

  const [validationError, setValidationError] = useState({});
  const [emailValidationError, setEmailValidationError] = useState('');
  const [loading, setLoading] = useState(false);
  const [emailValue, setEmailValue] = useState('');
  const [lastNameValue, setLastNameValue] = useState('');
  const [firstNameValue, setFirstNameValue] = useState('');
  const [userList, setUserList] = useState([]);
  const [rolesList, setRolesList] = useState([]);
  const [userRole, setUserRole] = useState('');
  const [groupList, setGroupList] = useState([]);
  const [userGroups, setUserGroups] = useState([]);
  const [userStatus, setUserStatus] = useState('active');

  const statusList = [{ value: 'inactive' }, { value: 'suspended' }, { value: 'active' }];

  const isAddMode = mode === 'add';
  const isEditMode = mode === 'edit';

  const resetForm = () => {
    setFirstNameValue('');
    setLastNameValue('');
    setEmailValue('');
    setUserRole('');
    setUserGroups([]);
    setUserStatus('active');
    setValidationError({});
    setEmailValidationError('');
  };

  // Fetch roles and groups when modal opens
  useEffect(() => {
    if (open) {
      apiUserManagement.getAllRoles().then((res) => {
        setRolesList(res);
      });
      apiUserManagement.listUserGroups().then((res) => {
        if (res?.data?.admin_get_user_groups_v2?.rows?.length > 0) {
          setGroupList([...res.data.admin_get_user_groups_v2.rows]);
        }
        // For edit mode, set selected groups
        if (isEditMode && userData?.user_groups?.length > 0) {
          let selectedUserGroups = [];
          for (let i = 0; i < userData?.user_groups?.length; i++) {
            for (let j = 0; j < res?.data?.admin_get_user_groups_v2?.rows?.length; j++) {
              if (userData?.user_groups[i]?.name === res?.data?.admin_get_user_groups_v2?.rows[j]?.name) {
                selectedUserGroups.push({
                  value: res?.data?.admin_get_user_groups_v2?.rows[j]?.id,
                  label: res?.data?.admin_get_user_groups_v2?.rows[j]?.name,
                });
              }
            }
          }
          setUserGroups(selectedUserGroups);
        }
      });
    }
  }, [open, isEditMode, userData]);

  // Fetch user list for duplicate check (add mode only)
  useEffect(() => {
    if (open && isAddMode) {
      setLoading(true);
      const data = {
        query: {},
        options: {
          select: ['username', 'id'],
          page: 1,
          paginate: 100,
        },
        isCountOnly: false,
      };
      apiUserManagement.listUsers(data).then((res) => {
        setUserList(res.data);
        setLoading(false);
      });
    }
  }, [open, isAddMode]);

  // Initialize form for edit mode
  useEffect(() => {
    if (open && isEditMode && userData) {
      setEmailValue(userData?.username || '');
      const role = userData?.user_roles?.[0]?.role;
      const status = userData?.status;
      setUserStatus(status || 'active');
      setUserRole(role || '');
      const nameParts = userData?.display_name?.split(' ') || [];
      if (nameParts.length > 0) {
        setFirstNameValue(nameParts[0] || '');
        // Take everything after the first word as last name (handles multi-word last names)
        setLastNameValue(nameParts.slice(1).join(' ') || '');
      }
    } else if (open && isAddMode) {
      resetForm();
    }
  }, [open, isEditMode, isAddMode, userData]);

  const handleRoleChange = (event) => {
    setUserRole(event.target.value?.value ?? event.target.value);
  };

  const handleStatusChange = (event) => {
    setUserStatus(event.target.value);
  };

  const handleGroupChange = (event) => {
    setUserGroups(event.target.value);
  };

  const handleKeyDown = (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      if (isFormValid()) {
        document.getElementById('user-modal-submit-button').click();
      }
    }
  };

  const isFormValid = () => {
    const baseValid = !!(firstNameValue && lastNameValue && !validationError.firstname && !validationError.lastname);
    if (isAddMode) {
      return !!(baseValid && emailValue && !emailValidationError);
    }
    return !!(baseValid && userStatus);
  };

  const validateForm = () => {
    textValidation(firstNameValue.trim(), validationError, setValidationError, 'firstname', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);
    textValidation(lastNameValue.trim(), validationError, setValidationError, 'lastname', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);

    if (isAddMode) {
      emailValidation(emailValue.toString(), setEmailValidationError, ['required', 'validate']);
      return !!(firstNameValue && lastNameValue && emailValue && !emailValidationError && !validationError.firstname && !validationError.lastname);
    }

    textValidation(userStatus ?? '', validationError, setValidationError, 'status', ['required']);
    return !!(firstNameValue && lastNameValue && userStatus && !validationError.firstname && !validationError.lastname);
  };

  // Edit mode: handle group changes (diff-based)
  async function handleGroupChanges() {
    try {
      const addedGroups = getAddedGroups();
      const removedGroups = getRemovedGroups();

      const promises = [];
      for (const groupId of removedGroups) {
        promises.push(
          apiUserManagement.manageGroupUsers({
            group_id: groupId,
            add_usernames: [],
            remove_usernames: [userData?.username],
          })
        );
      }
      for (const groupId of addedGroups) {
        promises.push(
          apiUserManagement.manageGroupUsers({
            group_id: groupId,
            add_usernames: [userData?.username],
            remove_usernames: [],
          })
        );
      }

      if (promises.length > 0) {
        await Promise.all(promises);
      }

      return true;
    } catch {
      handleSnackBarData({ message: 'Failed to edit user', severity: 'error' });
      return false;
    }
  }

  function getAddedGroups() {
    const currentIds = userGroups?.map((g) => g?.value ?? g) || [];
    const initialGroupIds = new Set(userData?.user_groups?.map((u) => u.id) ?? []);
    return currentIds.filter((id) => !initialGroupIds.has(id));
  }

  function getRemovedGroups() {
    const currentIds = userGroups?.map((g) => g?.value ?? g) || [];
    return userData?.user_groups?.map((u) => u.id)?.filter((id) => !currentIds.includes(id)) ?? [];
  }

  const submitForm = async (data) => {
    setLoading(true);

    if (!validateForm()) {
      setLoading(false);
      return;
    }

    if (isAddMode) {
      // Check for duplicate email
      for (const element of userList) {
        if (element.username === emailValue.toString()) {
          snackbar.error('This email is already in use');
          setLoading(false);
          reset({ username: '' });
          return;
        }
      }

      const addData = {
        ...data,
        firstname: firstNameValue,
        lastname: lastNameValue,
        email: emailValue,
        role: userRole,
      };

      const res = await apiUserManagement.addUser(addData);
      if (res?.data?.users_insert_one?.status === 'Ok') {
        if (userGroups.length > 0) {
          const newUsername = emailValue;
          const groupPromises = userGroups.map((group) =>
            apiUserManagement.manageGroupUsers({
              group_id: group?.value ?? group,
              add_usernames: [newUsername],
              remove_usernames: [],
            })
          );
          await Promise.all(groupPromises);
        }

        handleSnackBarData({
          message: 'User Added Successfully',
          icon: '',
          severity: 'success',
        });
        handleClose(true);
        resetForm();
        setLoading(false);
        return;
      }
      handleSnackBarData({ message: res.message, severity: 'error' });
      setLoading(false);
    } else {
      // Edit mode
      const formData = {
        username: userData?.username,
        display_name: `${firstNameValue} ${lastNameValue}`,
        status: userStatus,
        role: userRole ?? '',
      };

      const response = await apiUserManagement.updateUser(formData);
      const updateResult = response?.data?.data?.data?.user_update_profile;

      if (updateResult?.status === 'success') {
        if (await handleGroupChanges()) {
          handleSnackBarData({ message: 'User updated', severity: 'success' });
          setUserGroups([]);
          setTimeout(() => {
            handleClose(true);
            router.push(`/user-management#${currentFragment}`);
          }, 2000);
        }
      } else {
        handleSnackBarData({ message: 'Failed to edit user', severity: 'error' });
        setTimeout(() => {
          handleClose();
          router.push(`/user-management#${currentFragment}`);
        }, 2000);
      }
      setLoading(false);
    }
  };

  const handleModalClose = () => {
    if (isEditMode) {
      router.push(`/user-management#${currentFragment}`);
      setUserGroups([]);
    } else {
      resetForm();
    }
    handleClose();
  };

  return (
    <Modal
      id={isAddMode ? 'add-user-modal' : 'edit-user-modal'}
      data-testid={isAddMode ? 'add-user-modal' : 'edit-user-modal'}
      open={open}
      onClose={handleModalClose}
      sx={{ width: '480px', margin: 'auto', top: isAddMode ? '12vh' : '10vh' }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', background: colors.background.white, borderRadius: '8px' }}>
        <Box
          sx={{
            background: colors.background.primaryLightest,
            padding: '16px 0px 16px 32px',
            borderRadius: '8px 8px 0px 0px',
            borderBottom: `1px solid ${colors.border.primary}`,
            boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.10), 0px 2px 4px -2px rgba(0, 0, 0, 0.10)',
          }}
        >
          <Typography sx={{ color: colors.text.secondary, fontFamily: 'Roboto', fontSize: isAddMode ? '18px' : '16px', fontWeight: 600 }}>
            {isAddMode ? 'Add User' : 'Edit User'}
          </Typography>
        </Box>
        <Box
          onSubmit={handleSubmit(submitForm)}
          component='form'
          onKeyDown={handleKeyDown}
          sx={{
            '& .MuiTextField-root': { p: '0px 0px', marginLeft: '32px', marginBottom: '12px' },
            '& .MuiFormControl-root': { marginTop: '16px' },
            '& .MuiSelect-select': {
              padding: '8.5px 16px',
              display: isAddMode ? 'flex' : undefined,
              alignItems: isAddMode ? 'center' : undefined,
              justifyContent: isAddMode ? 'center' : undefined,
            },
            '& .MuiInputBase-input.MuiOutlinedInput-input': { height: isAddMode ? '31px' : undefined },
            '& .MuiInputLabel-root': { left: isAddMode ? '5px' : undefined, top: isAddMode ? '5px' : undefined },
            width: '100%',
            height: '100%',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <Box>
            <TextField
              sx={{ maxWidth: '417px', marginTop: isAddMode ? '16px' : undefined }}
              size='small'
              value={firstNameValue || ''}
              margin='normal'
              fullWidth
              id='user-modal-firstname'
              data-testid='user-modal-firstname'
              label='First Name'
              required
              type='text'
              name='firstname'
              {...register('firstname', {
                onChange: (e) => setFirstNameValue(e.target.value.trimStart()),
              })}
              onBlur={(e) => setFirstNameValue(e.target.value.trim())}
              onKeyUp={(e) =>
                textValidation(e.target.value.trim(), validationError, setValidationError, 'firstname', [
                  'required',
                  'firstLetterAlpha',
                  'alphaNumWithSpace',
                ])
              }
              helperText={validationError.firstname}
              error={!!validationError.firstname}
            />

            <TextField
              sx={{ maxWidth: '417px' }}
              size='small'
              margin='normal'
              value={lastNameValue || ''}
              fullWidth
              id='user-modal-lastname'
              data-testid='user-modal-lastname'
              label='Last Name'
              required
              type='text'
              name='lastname'
              {...register('lastname', {
                onChange: (e) => setLastNameValue(e.target.value.trimStart()),
              })}
              onBlur={(e) => setLastNameValue(e.target.value.trim())}
              onKeyUp={(e) =>
                textValidation(e.target.value.trim(), validationError, setValidationError, 'lastname', [
                  'required',
                  'firstLetterAlpha',
                  'alphaNumWithSpace',
                ])
              }
              helperText={validationError.lastname}
              error={!!validationError.lastname}
            />

            <TextField
              sx={{ maxWidth: '417px' }}
              size='small'
              value={emailValue || ''}
              margin='normal'
              fullWidth
              id='user-modal-email'
              data-testid='user-modal-email'
              label='Email'
              required={isAddMode}
              type='text'
              name='email'
              disabled={isEditMode}
              {...register('email', {
                onChange: (e) => isAddMode && setEmailValue(e.target.value),
              })}
              onKeyDown={isAddMode ? handleKeyDown : undefined}
              onKeyUp={isAddMode ? (e) => emailValidation(e.target.value, setEmailValidationError, ['required', 'validate']) : undefined}
              helperText={isAddMode ? emailValidationError : undefined}
              error={isAddMode ? !!emailValidationError : undefined}
            />

            <Box data-testid='user-modal-tenant-role' sx={{ display: 'flex', alignItems: 'center', ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
              <FilterDropdownButton
                id='user-modal-tenant-role'
                label='Tenant Role'
                value={userRole || null}
                options={rolesList?.map((v) => ({ value: v.value, label: v.display_name || v.value }))}
                onSelect={handleRoleChange}
              />
            </Box>

            <Box data-testid='user-modal-group' sx={{ display: 'flex', alignItems: 'center', ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
              <FilterDropdownButton
                id='user-modal-group'
                label='Groups'
                value={userGroups || []}
                multiple
                options={groupList?.map((v) => ({ value: v.id, label: v.name }))}
                onSelect={handleGroupChange}
              />
            </Box>

            {isEditMode && (
              <>
                <Box data-testid='user-modal-status' sx={{ display: 'flex', alignItems: 'center', ml: '32px', mr: '32px', mt: '16px', mb: '12px' }}>
                  <FilterDropdownButton
                    id='user-modal-status'
                    label='Status'
                    value={userStatus || null}
                    options={statusList.map((v) => ({ value: v.value, label: v.value }))}
                    onSelect={handleStatusChange}
                  />
                </Box>
                {validationError.status && (
                  <Typography ml='32px' sx={{ color: colors.text.red, fontSize: '12px' }}>
                    Status selection is mandatory
                  </Typography>
                )}
              </>
            )}
          </Box>

          <Box
            sx={{
              borderTop: isAddMode ? `0.5px solid ${colors.border.vertical}` : undefined,
              marginTop: isAddMode ? '20px' : undefined,
            }}
          >
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'flex-end',
                marginX: '32px',
                marginY: '20px',
                gap: '12px',
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <CustomButton
                id='user-modal-cancel-button'
                data-testid='user-modal-cancel-button'
                variant='secondary'
                size='Medium'
                onClick={handleModalClose}
                text='Cancel'
              />
              <CustomButton
                type='submit'
                id='user-modal-submit-button'
                data-testid='user-modal-submit-button'
                size='Medium'
                text={isAddMode ? 'Add User' : 'Update'}
                disabled={!isFormValid()}
                loading={loading}
              />
            </Box>
          </Box>
        </Box>
      </Box>
    </Modal>
  );
}

UserModal.propTypes = {
  open: PropTypes.bool.isRequired,
  handleClose: PropTypes.func.isRequired,
  handleSnackBarData: PropTypes.func.isRequired,
  mode: PropTypes.oneOf(['add', 'edit']).isRequired,
  userData: PropTypes.object,
};

UserModal.defaultProps = {
  userData: null,
};

export default UserModal;
