import React, { useEffect, useState, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import { useForm } from 'react-hook-form';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import apiUserManagement from '@api1/user';
import { listCustomRoles, updateRoleUserAssignments } from '@api1/roles';
import { isCustomRolesEnabled, isTenantAdmin } from '@lib/auth';
import { textValidation, emailValidation } from '@lib/validation';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { ds } from 'src/utils/colors';
import { toast as snackbar } from '@ui/Toast';
import { Card } from '@ui/Card';
import { ToggleGroup } from '@ui/ToggleGroup';
import IntegrationProfiles from '../IntegrationProfiles';

function CardHeader({ title, description }) {
  return (
    <Box>
      <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>
      {description && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[0] }}>{description}</Typography>}
    </Box>
  );
}

const STATUS_OPTIONS = [
  { value: 'active', label: 'Active', dotColor: ds.green[600], helper: 'User can sign in and access the tenant.' },
  { value: 'inactive', label: 'Inactive', dotColor: ds.gray[400], helper: 'User cannot sign in but can be reactivated anytime.' },
  { value: 'suspended', label: 'Suspended', dotColor: ds.red[600], helper: 'Sign-in blocked. Active sessions revoked immediately.' },
];

const STATUS_TOGGLE_OPTIONS = STATUS_OPTIONS.map((opt) => ({
  value: opt.value,
  label: opt.label,
  icon: (
    <Box
      component='span'
      sx={{ width: ds.space.mul(0, 3), height: ds.space.mul(0, 3), borderRadius: 'var(--ds-radius-pill)', background: opt.dotColor, flexShrink: 0 }}
    />
  ),
}));

function UserModal({ open, handleClose, handleSnackBarData, mode, userData = null }) {
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
  // Dynamic-RBAC custom roles: full role objects (with user_ids) for the diff,
  // and the ids currently picked in the multi-select.
  const [customRolesList, setCustomRolesList] = useState([]);
  const [selectedCustomRoles, setSelectedCustomRoles] = useState([]);
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
    setSelectedCustomRoles([]);
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
      // Custom roles are tenant_admin-only; a non-admin caller gets an empty
      // list and the picker stays hidden. Skipped outright while the tenant's
      // CUSTOM_ROLES feature is off — the service refuses the call then, and the
      // modal must look exactly as it did before dynamic RBAC.
      if (isCustomRolesEnabled()) {
        listCustomRoles()
          .then((roles) => {
            setCustomRolesList(roles ?? []);
            if (isEditMode && userData?.id) {
              setSelectedCustomRoles((roles ?? []).filter((r) => (r.user_ids ?? []).includes(userData.id)).map((r) => r.id));
            }
          })
          .catch(() => setCustomRolesList([]));
      }
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

  // Apply custom-role (dynamic RBAC) assignments for this user. The backend
  // assignment API is role-side replace-all (customroles_update_user_assignments
  // replaces a role's entire user list), so from a per-user modal we
  // read-modify-write only the roles whose membership for THIS user changed,
  // starting from the freshly-loaded snapshot. Returns false (and toasts) on
  // failure so the caller can skip the success path.
  async function applyCustomRoleAssignments(userId) {
    if (!userId) return true;
    try {
      const desired = new Set(selectedCustomRoles);
      const initial = new Set(customRolesList.filter((r) => (r.user_ids ?? []).includes(userId)).map((r) => r.id));
      const changed = customRolesList.filter((r) => desired.has(r.id) !== initial.has(r.id));
      const promises = changed.map((r) => {
        const current = r.user_ids ?? [];
        const nextIds = desired.has(r.id) ? Array.from(new Set([...current, userId])) : current.filter((id) => id !== userId);
        return updateRoleUserAssignments(r.id, nextIds);
      });
      if (promises.length > 0) await Promise.all(promises);
      return true;
    } catch {
      handleSnackBarData({ message: 'Failed to update custom roles', severity: 'error' });
      return false;
    }
  }

  // Assigning roles — built-in or custom — is privilege administration, and both
  // write paths (users_create / users_update_profile's role sync, and
  // customroles_update_user_assignments) are tenant-admin-only on purpose. A
  // users:Write grant admits everything else in this modal, so show the picker
  // read-only for those callers rather than letting them submit a guaranteed 403.
  const canAssignRoles = isTenantAdmin();

  const submitForm = async (data) => {
    setLoading(true);
    if (!validateForm()) {
      setLoading(false);
      return;
    }
    // The group/role mutations reject on an upstream error (they no longer
    // swallow it). Without this guard a rejection here — after the user has
    // already been created — left the modal spinning with no message. Surface
    // the reason and stop the spinner; the success paths still return early.
    try {
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
          const rolesOk = await applyCustomRoleAssignments(res?.data?.users_create?.id);
          if (!rolesOk) {
            setLoading(false);
            return;
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
            if (!(await applyCustomRoleAssignments(userData?.id))) {
              setLoading(false);
              return;
            }
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
    } catch (error) {
      console.error('Error submitting user form:', error);
      handleSnackBarData({ message: error?.message || 'An error occurred', severity: 'error' });
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
      handleClose={() => (loading ? undefined : handleModalClose())}
      title={isAddMode ? 'Add User' : 'Edit User'}
      width='sm'
      loader={loading}
      sx={{ '& .MuiDialog-paper': { maxWidth: ds.space.mul(0, 280), maxHeight: '90vh' } }}
      contentStyles={{ padding: 'var(--ds-space-4) var(--ds-space-5)', overflowX: 'hidden' }}
      actionButtons={
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--ds-space-2)',
          }}
        >
          <Button id='user-modal-cancel-button' tone='secondary' size='md' onClick={handleModalClose} disabled={loading}>
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
        {/* User Info */}
        <Card variant='outlined' elevation='flat' header={<CardHeader title='User Info' />}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
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
          </Box>
        </Card>

        {/* Access */}
        {(rolesList.length > 0 || isEditMode || customRolesList.length > 0) && (
          <Card variant='outlined' elevation='flat' header={<CardHeader title='Access' />}>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
              {(rolesList.length > 0 || customRolesList.length > 0) && (
                <Box data-testid='user-modal-role'>
                  <Select
                    multiple
                    id='user-modal-tenant-role'
                    label='Role'
                    placeholder='Select role(s)'
                    value={[...(userRole ? [userRole] : []), ...selectedCustomRoles]}
                    onChange={(next) => {
                      // One merged picker over built-in roles + custom roles. Built-in
                      // roles still write to user_roles (single — that's what carries
                      // data scope); custom roles write to custom_role_assignments
                      // (additive permissions). Enforce a single built-in by keeping the
                      // most recently selected one.
                      const builtinSet = new Set(rolesList.map((r) => r.value));
                      const builtins = next.filter((v) => builtinSet.has(v));
                      const customs = next.filter((v) => !builtinSet.has(v));
                      setUserRole(builtins.length ? builtins[builtins.length - 1] : '');
                      setSelectedCustomRoles(customs);
                    }}
                    options={[
                      ...rolesList.map((r) => ({ value: r.value, label: r.display_name || r.value })),
                      ...customRolesList.map((r) => ({ value: r.id, label: r.name })),
                    ]}
                    maxChips={4}
                    disabled={!canAssignRoles}
                    help={
                      canAssignRoles
                        ? 'Built-in roles grant access to accounts; custom roles add extra action permissions. Pick one built-in role plus any custom roles.'
                        : 'Only a tenant admin can change a user’s roles. You can still edit the profile and status.'
                    }
                    minWidth='100%'
                  />
                </Box>
              )}
              {isEditMode && (
                <Box data-testid='user-modal-status'>
                  {fieldLabel('Status', true)}
                  <ToggleGroup
                    selection='single'
                    options={STATUS_TOGGLE_OPTIONS}
                    value={userStatus}
                    onChange={setUserStatus}
                    size='md'
                    ariaLabel='User status'
                  />
                  <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.gray[400], mt: 'var(--ds-space-1)' }}>
                    {STATUS_OPTIONS.find((s) => s.value === userStatus)?.helper || ''}
                  </Box>
                  {validationError.status && (
                    <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.red[600], mt: 'var(--ds-space-1)' }}>Status selection is mandatory</Box>
                  )}
                </Box>
              )}
            </Box>
          </Card>
        )}

        {/* Groups */}
        <Card variant='outlined' elevation='flat' header={<CardHeader title='Groups' />}>
          <Box data-testid='user-modal-group'>
            <Select
              multiple
              id='user-modal-group'
              label={null}
              placeholder='Select groups'
              value={userGroups || []}
              onChange={handleGroupChange}
              options={(groupList || []).map((v) => ({ value: v.id, label: v.name }))}
              maxChips={4}
              help={isAddMode ? 'Groups control which clusters and dashboards this user can access.' : undefined}
            />
          </Box>
        </Card>

        {/* Integration profiles (edit only — requires a persisted user id) */}
        {isEditMode && userData?.id && (
          <Card variant='outlined' elevation='flat' header={<CardHeader title='Integration Profiles' />}>
            <IntegrationProfiles userId={userData.id} onNotify={handleSnackBarData} hideHeading />
          </Card>
        )}
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

export default UserModal;
