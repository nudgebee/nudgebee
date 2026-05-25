import React, { useEffect, useState, useMemo } from 'react';
import { Box } from '@mui/material';
import { useForm } from 'react-hook-form';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import apiUserManagement from '@api1/user';
import { textValidation, emailValidation } from '@lib/validation';
import { Modal } from '@common-new/modal';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { colors } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';

const ROLE_DESCRIPTIONS = {
  tenant_admin: 'Full access to manage users, integrations, and settings.',
  tenant_admin_readonly: 'View everything but cannot make changes.',
  admin: 'Full access to manage users, integrations, and settings.',
  readonly: 'View everything but cannot make changes.',
};

const STATUS_OPTIONS = [
  { value: 'active', label: 'Active', dotColor: colors.success, helper: 'User can sign in and access the tenant.' },
  { value: 'inactive', label: 'Inactive', dotColor: colors.text.tertiaryLight, helper: 'User cannot sign in but can be reactivated anytime.' },
  { value: 'suspended', label: 'Suspended', dotColor: colors.error, helper: 'Sign-in blocked. Active sessions revoked immediately.' },
];

function RoleTile({ name, description, selected, onClick, dataTestId }) {
  return (
    <Box
      component='button'
      type='button'
      role='radio'
      aria-checked={selected}
      onClick={onClick}
      data-testid={dataTestId}
      sx={{
        position: 'relative',
        textAlign: 'left',
        cursor: 'pointer',
        background: selected ? colors.background.primaryLightest : colors.background.white,
        border: `1.5px solid ${selected ? colors.primary : colors.border.secondaryLight}`,
        borderRadius: '10px',
        padding: '12px',
        transition: 'all 0.15s',
        '&:hover': { borderColor: selected ? colors.primary : colors.border.primaryLight },
      }}
    >
      <Box
        sx={{
          position: 'absolute',
          top: 8,
          right: 8,
          width: 16,
          height: 16,
          borderRadius: '999px',
          border: `1.5px solid ${selected ? colors.primary : colors.border.secondary}`,
          background: selected ? colors.primary : colors.background.white,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          '&::after': selected ? { content: '""', width: '6px', height: '6px', borderRadius: '999px', background: colors.background.white } : {},
        }}
      />
      <Box sx={{ font: "600 13px/1.2 'Roboto'", color: colors.text.title, mb: '3px', pr: '20px' }}>{name}</Box>
      <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: colors.text.tertiary }}>{description}</Box>
    </Box>
  );
}

RoleTile.propTypes = {
  name: PropTypes.string,
  description: PropTypes.string,
  selected: PropTypes.bool,
  onClick: PropTypes.func,
  dataTestId: PropTypes.string,
};

function StatusSegmented({ value, onChange }) {
  return (
    <Box
      role='radiogroup'
      aria-label='User status'
      sx={{ display: 'inline-flex', padding: '3px', background: colors.background.suggestionCardHover, borderRadius: '8px', gap: '2px' }}
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
              gap: '7px',
              padding: '7px 14px',
              borderRadius: '6px',
              background: selected ? colors.background.white : 'transparent',
              color: selected ? colors.text.title : colors.text.tertiary,
              boxShadow: selected ? colors.shadow.softBlack : 'none',
              border: 'none',
              cursor: 'pointer',
              fontFamily: 'Roboto',
              fontWeight: selected ? 600 : 500,
              fontSize: '12.5px',
              transition: 'all 0.15s',
            }}
          >
            <Box component='span' sx={{ width: 7, height: 7, borderRadius: '999px', background: opt.dotColor, flexShrink: 0 }} />
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
        const list = res || [];
        setRolesList(list);
        if (isAddMode && list.length > 0) {
          setUserRole((prev) => prev || list[0].value);
        }
      });
      apiUserManagement.listUserGroups().then((res) => {
        if (res?.data?.admin_get_user_groups_v2?.rows?.length > 0) {
          setGroupList([...res.data.admin_get_user_groups_v2.rows]);
        }
        if (isEditMode && userData?.user_groups?.length > 0) {
          // Match the user's groups against the full group list and store just
          // the IDs — Select expects value as string[].
          const rows = res?.data?.admin_get_user_groups_v2?.rows ?? [];
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
      const updateResult = response?.data?.user_update_profile;
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

  const roleTiles = (rolesList || []).map((r) => ({
    id: r.value,
    name: r.display_name || r.value,
    description: ROLE_DESCRIPTIONS[r.value] || '',
  }));

  const fieldLabel = (text, required) => (
    <Box component='label' sx={{ display: 'block', font: "500 12px/1.2 'Roboto'", color: colors.text.secondary, mb: '6px' }}>
      {text}
      {required && (
        <Box component='span' sx={{ color: colors.error, ml: '2px' }}>
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
      sx={{ '& .MuiDialog-paper': { maxWidth: '560px', maxHeight: '90vh' } }}
      contentStyles={{ padding: '20px 24px', overflowX: 'hidden' }}
      actionButtons={
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: '8px',
            padding: '12px 24px',
            background: colors.background.tertiaryLightestest,
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
        sx={{ display: 'flex', flexDirection: 'column', gap: '16px' }}
      >
        {/* First + Last name */}
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', '& > *': { minWidth: 0 } }}>
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

        {/* Tenant role tiles */}
        {roleTiles.length > 0 && (
          <Box data-testid='user-modal-tenant-role'>
            {fieldLabel('Tenant role', true)}
            <Box
              role='radiogroup'
              aria-label='Tenant role'
              // Stable id retained for e2e — old impl used FilterDropdownButton with id
              // `auto-complete-user-modal-tenant-role` (see usersLocators.ts). The redesign
              // moved to per-role tiles; e2e tests should target `user-modal-role-<roleId>`.
              id='user-modal-tenant-role'
              sx={{
                display: 'grid',
                gridTemplateColumns: roleTiles.length === 1 ? '1fr' : '1fr 1fr',
                gap: '8px',
                '& > *': { minWidth: 0 },
              }}
            >
              {roleTiles.map((r) => (
                <RoleTile
                  key={r.id}
                  name={r.name}
                  description={r.description}
                  selected={userRole === r.id}
                  onClick={() => setUserRole(r.id)}
                  dataTestId={`user-modal-role-${r.id}`}
                />
              ))}
            </Box>
          </Box>
        )}

        {/* Status (edit only) */}
        {isEditMode && (
          <Box data-testid='user-modal-status'>
            {fieldLabel('Status', true)}
            <StatusSegmented value={userStatus} onChange={setUserStatus} />
            <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: colors.text.tertiaryLight, mt: '6px' }}>
              {STATUS_OPTIONS.find((s) => s.value === userStatus)?.helper || ''}
            </Box>
            {validationError.status && (
              <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: colors.error, mt: '5px' }}>Status selection is mandatory</Box>
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
