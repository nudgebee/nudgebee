import React, { useEffect, useState, useMemo } from 'react';
import { Box } from '@mui/material';
import { useForm } from 'react-hook-form';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import apiUserManagement from '@api1/user';
import { textValidation, emailValidation } from '@lib/validation';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { colors, ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';

const ROLE_DESCRIPTIONS = {
  tenant_admin: 'Full access to manage users, integrations, and settings.',
  tenant_admin_readonly: 'View everything but cannot make changes.',
  admin: 'Full access to manage users, integrations, and settings.',
  readonly: 'View everything but cannot make changes.',
};

const STATUS_OPTIONS = [
  { value: 'active', label: 'Active', dotColor: ds.green[600], helper: 'User can sign in and access the tenant.' },
  { value: 'inactive', label: 'Inactive', dotColor: ds.gray[400], helper: 'User cannot sign in but can be reactivated anytime.' },
  { value: 'suspended', label: 'Suspended', dotColor: ds.red[600], helper: 'Sign-in blocked. Active sessions revoked immediately.' },
];

function StatusSegmented({ value, onChange }) {
  return (
    <Box
      role='radiogroup'
      aria-label='User status'
      sx={{
        display: 'inline-flex',
        padding: 'var(--ds-space-1)',
        background: ds.background[300],
        borderRadius: 'var(--ds-radius-lg)',
        gap: 'var(--ds-space-1)',
      }}
    >
      {STATUS_OPTIONS.map((opt) => {
        const selected = value === opt.value;
        return (
          <Box
            key={opt.value}
            component='button'
            type='button'
            role='radio'
            aria-checked={selected}
            onClick={() => onChange(opt.value)}
            data-testid={`user-modal-status-${opt.value}`}
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--ds-space-2)',
              padding: 'var(--ds-space-2) var(--ds-space-3)',
              borderRadius: 'var(--ds-radius-md)',
              background: selected ? ds.background[100] : 'transparent',
              color: selected ? ds.gray[700] : ds.gray[600],
              boxShadow: selected ? colors.shadow.softBlack : 'none',
              border: 'none',
              cursor: 'pointer',
              fontFamily: 'Roboto',
              fontWeight: selected ? 600 : 500,
              fontSize: ds.text.small,
              transition: 'all 0.15s',
            }}
          >
            <Box
              component='span'
              sx={{
                width: ds.space.mul(0, 3),
                height: ds.space.mul(0, 3),
                borderRadius: 'var(--ds-radius-pill)',
                background: opt.dotColor,
                flexShrink: 0,
              }}
            />
            {opt.label}
          </Box>
        );
      })}
    </Box>
  );
}

StatusSegmented.propTypes = {
  value: PropTypes.string,
  onChange: PropTypes.func,
};

function UserModal({ open, handleClose, handleSnackBarData, mode, userData }) {
  const { reset, handleSubmit } = useForm();
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

  useEffect(() => {
    if (open) {
      apiUserManagement.getAllRoles().then((res) => {
        setRolesList(res || []);
      });
      apiUserManagement.listUserGroups().then((res) => {
        if (res?.data?.usergroups_list?.rows?.length > 0) {
          setGroupList([...res.data.usergroups_list.rows]);
        }
        if (isEditMode && userData?.user_groups?.length > 0) {
          // Match the user's groups against the full group list and store just
          // the IDs — Select expects value as string[].
          const rows = res?.data?.usergroups_list?.rows ?? [];
          const selectedIds = userData.user_groups.map((ug) => rows.find((r) => r?.name === ug?.name)?.id).filter((id) => Boolean(id));
          setUserGroups(selectedIds);
        }
      });
    }
  }, [open, isEditMode, isAddMode, userData]);

  useEffect(() => {
    if (open && isAddMode) {
      setLoading(true);
      const data = {
        query: {},
        options: { select: ['username', 'id'], page: 1, paginate: 100 },
        isCountOnly: false,
      };
      apiUserManagement.listUsers(data).then((res) => {
        setUserList(res.data);
        setLoading(false);
      });
    }
  }, [open, isAddMode]);

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
        setLastNameValue(nameParts.slice(1).join(' ') || '');
      }
    } else if (open && isAddMode) {
      resetForm();
    }
  }, [open, isEditMode, isAddMode, userData]);

  const handleGroupChange = (next) => {
    setUserGroups(next);
  };

  const handleKeyDown = (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      if (isFormValid()) {
        document.getElementById('user-modal-submit-button')?.click();
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
    // Accumulate all field errors into one local object, then commit a single state update at
    // the end. This avoids the "each setValidationError(computed) overwrites the previous one
    // from the same stale snapshot" bug where earlier-validated field errors would silently
    // disappear in the rendered UI.
    let errors = { ...validationError };
    const collectText = (value, field, options) => {
      textValidation(
        value,
        errors,
        (next) => {
          errors = typeof next === 'function' ? next(errors) : next;
        },
        field,
        options
      );
      return errors[field];
    };

    const firstNameError = collectText(firstNameValue.trim(), 'firstname', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);
    const lastNameError = collectText(lastNameValue.trim(), 'lastname', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);

    let emailError;
    let statusError;
    if (isAddMode) {
      emailValidation(
        emailValue.toString(),
        (msg) => {
          emailError = msg;
          setEmailValidationError(msg);
        },
        ['required', 'validate']
      );
    } else {
      statusError = collectText(userStatus ?? '', 'status', ['required']);
    }

    setValidationError(errors);

    if (isAddMode) {
      return !!(firstNameValue && lastNameValue && emailValue && !emailError && !firstNameError && !lastNameError);
    }
    return !!(firstNameValue && lastNameValue && userStatus && !firstNameError && !lastNameError && !statusError);
  };

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
      if (res?.data?.users_create?.status === 'Ok') {
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
        handleSnackBarData({ message: 'User Added Successfully', icon: '', severity: 'success' });
        handleClose(true);
        resetForm();
        setLoading(false);
        return;
      }
      handleSnackBarData({ message: res.message, severity: 'error' });
      setLoading(false);
    } else {
      const formData = {
        username: userData?.username,
        display_name: `${firstNameValue} ${lastNameValue}`,
        status: userStatus,
        role: userRole ?? '',
      };
      const response = await apiUserManagement.updateUser(formData);
      const updateResult = response?.data?.users_update_profile;
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

  const fieldLabel = (text, required) => (
    <Box component='label' sx={{ display: 'block', font: "500 12px/1.2 'Roboto'", color: ds.gray[700], mb: 'var(--ds-space-1)' }}>
      {text}
      {required && (
        <Box component='span' sx={{ color: ds.red[600], ml: 'var(--ds-space-1)' }}>
          *
        </Box>
      )}
    </Box>
  );

  return (
    <Modal
      open={open}
      handleClose={handleModalClose}
      title={isAddMode ? 'Add User' : 'Edit User'}
      width='sm'
      sx={{ '& .MuiDialog-paper': { maxWidth: ds.space.mul(0, 280), maxHeight: '90vh' } }}
      contentStyles={{ padding: 'var(--ds-space-4) var(--ds-space-5)', overflowX: 'hidden' }}
      actionButtons={
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--ds-space-2)',
            padding: 'var(--ds-space-3) var(--ds-space-5)',
            background: ds.background[200],
          }}
        >
          <Button id='user-modal-cancel-button' tone='secondary' size='md' onClick={handleModalClose}>
            Cancel
          </Button>
          <Button
            id='user-modal-submit-button'
            type='submit'
            size='md'
            disabled={!isFormValid()}
            loading={loading}
            onClick={handleSubmit(submitForm)}
          >
            {isAddMode ? 'Add user' : 'Save changes'}
          </Button>
        </Box>
      }
    >
      <Box
        component='form'
        // Stable id required by e2e tests (app-e2e-tests/.../usersLocators.ts uses #edit-user-modal).
        id={isAddMode ? 'add-user-modal' : 'edit-user-modal'}
        data-testid={isAddMode ? 'add-user-modal' : 'edit-user-modal'}
        onSubmit={(e) => e.preventDefault()}
        onKeyDown={handleKeyDown}
        sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}
      >
        {/* First + Last name */}
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--ds-space-3)', '& > *': { minWidth: 0 } }}>
          <Box data-testid='user-modal-firstname'>
            <Input
              id='user-modal-firstname'
              name='firstname'
              label='First name'
              required
              placeholder='Alex'
              value={firstNameValue || ''}
              onChange={(next) => {
                const v = next.trimStart();
                setFirstNameValue(v);
                textValidation(v.trim(), validationError, setValidationError, 'firstname', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);
              }}
              onBlur={(e) => setFirstNameValue(e.currentTarget.value.trim())}
              error={validationError.firstname}
            />
          </Box>
          <Box data-testid='user-modal-lastname'>
            <Input
              id='user-modal-lastname'
              name='lastname'
              label='Last name'
              required
              placeholder='Morgan'
              value={lastNameValue || ''}
              onChange={(next) => {
                const v = next.trimStart();
                setLastNameValue(v);
                textValidation(v.trim(), validationError, setValidationError, 'lastname', ['required', 'firstLetterAlpha', 'alphaNumWithSpace']);
              }}
              onBlur={(e) => setLastNameValue(e.currentTarget.value.trim())}
              error={validationError.lastname}
            />
          </Box>
        </Box>

        {/* Email */}
        <Box data-testid='user-modal-email'>
          <Input
            id='user-modal-email'
            name='email'
            label='Work email'
            required={isAddMode}
            type='email'
            placeholder='name@yourcompany.com'
            value={emailValue || ''}
            disabled={isEditMode}
            onChange={(next) => {
              if (!isAddMode) return;
              setEmailValue(next);
              emailValidation(next, setEmailValidationError, ['required', 'validate']);
            }}
            error={isAddMode ? emailValidationError : undefined}
          />
        </Box>

        {/* Tenant role */}
        {rolesList.length > 0 && (
          <Box data-testid='user-modal-tenant-role'>
            <Select
              id='user-modal-tenant-role'
              label='Tenant role'
              value={userRole || ''}
              options={rolesList.map((r) => ({ value: r.value, label: r.display_name || r.value }))}
              onChange={(next) => setUserRole(next)}
              placeholder='Select tenant role'
              help={userRole ? ROLE_DESCRIPTIONS[userRole] : 'Leave empty if no tenant-level role is needed.'}
              minWidth='100%'
            />
          </Box>
        )}

        {/* Status (edit only) */}
        {isEditMode && (
          <Box data-testid='user-modal-status'>
            {fieldLabel('Status', true)}
            <StatusSegmented value={userStatus} onChange={setUserStatus} />
            <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.gray[400], mt: 'var(--ds-space-1)' }}>
              {STATUS_OPTIONS.find((s) => s.value === userStatus)?.helper || ''}
            </Box>
            {validationError.status && (
              <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.red[600], mt: 'var(--ds-space-1)' }}>Status selection is mandatory</Box>
            )}
          </Box>
        )}

        {/* Groups */}
        <Box data-testid='user-modal-group'>
          <Select
            multiple
            id='user-modal-group'
            label='Groups'
            placeholder='Select groups'
            value={userGroups || []}
            onChange={handleGroupChange}
            options={(groupList || []).map((v) => ({ value: v.id, label: v.name }))}
            maxChips={4}
            help={isAddMode ? 'Groups control which clusters and dashboards this user can access.' : undefined}
          />
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
