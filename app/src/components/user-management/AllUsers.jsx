import React, { useCallback, useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { writeIcon } from '@assets';
import { TourLauncher } from '@components/common/tour';
import apiUserManagement from '@api1/user';
import { useSession } from 'next-auth/react';
import { canManage, isTenantWideRole, hasPermission, canReadCustomRoles } from '@lib/auth';
import { listCustomRoles } from '@api1/roles';
import UserModal from './modal/UserModal';
import { Label } from '@ui/Label';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import CustomSearch from '@shared/CustomSearch';
import { Button as DsButton } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import CustomTable2 from '@shared/tables/CustomTable2';
import { getUsersByTenant } from '@lib/UserService';
import UserGroup from './UserGroup';
import IntegrationProfiles from './IntegrationProfiles';
import { toast as snackbar } from '@ui/Toast';
import { safeJSONParse } from 'src/utils/common';
import { ds } from 'src/utils/colors';

// The Role cell: the built-in role on the first line, any directly-assigned
// custom roles under it. Custom roles were previously only visible by opening
// the Edit User modal, so the list read as "this user has one role" when they
// could hold several.
function UserRoleCell({ user, customRoleNames }) {
  const builtIn = user?.user_roles?.[0]?.role_display_name || user?.user_roles?.[0]?.role || '-';
  if (!customRoleNames.length) {
    return <Text value={builtIn} />;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column' }}>
      <Text value={builtIn} />
      <Text value={`Custom: ${customRoleNames.join(', ')}`} sx={{ color: ds.gray[600] }} />
    </Box>
  );
}

UserRoleCell.propTypes = {
  user: PropTypes.object.isRequired,
  customRoleNames: PropTypes.arrayOf(PropTypes.string).isRequired,
};

const AllUsers = () => {
  const [loading, setLoading] = useState(false);
  // Raw rows, not built cells: the Role column also renders dynamic-RBAC custom
  // roles, which load on their own clock. Deriving the table from both keeps a
  // late custom-roles response from needing a second users fetch to show up.
  const [users, setUsers] = useState([]);
  const [customRoles, setCustomRoles] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [editUserModalVisible, setEditUserModalVisible] = useState(false);
  const [activeUserData, setActiveUserData] = useState({});
  const [addUserModalVisible, setAddUserModalVisible] = React.useState(false);
  const [statusOptions, setStatusOptions] = useState([]);
  const [selectedStatus, setSelectedStatus] = useState('active');
  const [sortObject, setSortObject] = useState({
    name: 'Name',
    order: 'asc',
  });
  const [selectedName, setSelectedName] = useState('');
  const [userNameInput, setUserNameInput] = useState('');
  const [perPage, setPerPage] = useState(apiUserManagement.getUserPreferencesTablePageSize());

  const { data: currentUser } = useSession({
    required: true,
  });
  const allUsersTableHeaders = [
    { name: 'Name', sortEnabled: true },
    'Status',
    {
      name: 'Role',
      info: 'Built-in role plus any custom roles assigned to the user. Your effective role is determined by your assigned roles but may change if you belong to a group with a role for a specific namespace or account.',
    },
    { name: 'Email', sortEnabled: true },
    'Group',
    'Last Accessed',
    '',
  ];

  const handleAddUserModalClose = (updated) => {
    setAddUserModalVisible(false);
    if (updated) {
      fetchUsers();
      fetchCustomRoles();
    }
  };

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setPerPage(limit);
  };

  const handleEditUserModal = (event, userData) => {
    setActiveUserData(userData);
    setEditUserModalVisible(true);
  };

  const sortEventChange = (e) => {
    setSortObject(e);
  };

  const handleStatusChange = (e) => {
    setCurrentPage(0);
    setSelectedStatus(e.target.value);
  };

  const capitalizeFirstLetter = (text) => {
    return text.charAt(0).toUpperCase() + text.slice(1, text.length);
  };
  useEffect(() => {
    apiUserManagement.getAllStatuses().then((res) => {
      let responseStatusList = res?.data?.user_status_type || [];
      let statusArray = [];
      for (let item of responseStatusList) {
        statusArray.push({ label: capitalizeFirstLetter(item.value), value: item.value });
      }
      setStatusOptions(statusArray);
    });
  }, []);

  const showGroupNames = (usergroups) => {
    if (usergroups && usergroups.length > 0) {
      return usergroups.map((group) => group.name).join(', ');
    }
    return '-';
  };

  const fetchUsers = () => {
    let sortColValue = '';
    if (sortObject.name == 'Email') {
      sortColValue = 'username';
    } else if (sortObject.name == 'Name') {
      sortColValue = 'display_name';
    }
    const data = {
      offset: currentPage * perPage,
      limit: perPage,
      sortOrder: sortObject.order,
      sortCol: sortColValue,
      nameSearch: selectedName,
      statusSearch: selectedStatus,
    };
    setLoading(true);
    setUsers([]);
    setTotalCount(0);
    getUsersByTenant(data)
      .then((res) => {
        const result = res?.users_list_by_tenant?.rows ?? [];
        for (let user of result) {
          user.user_groups = safeJSONParse(user.user_groups) || [];
          user.user_roles = safeJSONParse(user.user_roles) || [];
        }
        setUsers(result);
        setTotalCount(res?.users_aggregate_by_tenant?.rows?.[0]?.count ?? 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    fetchUsers();
  }, [currentPage, selectedStatus, sortObject, perPage, selectedName]);

  // Gated on canReadCustomRoles: customroles_list 403s for a plain users:Read
  // holder and 400s tenant-wide when CUSTOM_ROLES is off. The Role column is an
  // enrichment, so a failure here must leave the built-in role rendering rather
  // than break the page.
  // Re-run after any user write, not just on mount: the Role column is derived
  // from each role's `user_ids`, which the Edit User modal changes via the
  // role-side replace-all assignment API. Refetching only the users left a
  // custom role the admin had just unassigned rendering in the column until a
  // full page reload.
  const fetchCustomRoles = useCallback(() => {
    if (!canReadCustomRoles()) {
      return;
    }
    listCustomRoles()
      .then((roles) => setCustomRoles(roles ?? []))
      .catch((err) => {
        console.error('Failed to load custom roles for the users list:', err);
      });
  }, []);

  useEffect(() => {
    fetchCustomRoles();
  }, [fetchCustomRoles]);

  // user id → names of the custom roles assigned directly to them. Group-derived
  // custom roles are deliberately NOT folded in: they belong to the group and
  // are shown on the Groups tab, and merging them here would read as a direct
  // assignment that the Edit User modal doesn't show.
  const customRoleNamesByUser = useMemo(() => {
    const map = new Map();
    for (const role of customRoles) {
      for (const userId of role.user_ids ?? []) {
        if (!map.has(userId)) map.set(userId, []);
        map.get(userId).push(role.name);
      }
    }
    return map;
  }, [customRoles]);

  const allUserTableData = useMemo(
    () =>
      users.map((user) => [
        {
          component: <Text value={user.display_name} showAutoEllipsis />,
          drilldownQuery: { groupNames: user.user_groups.map((group) => group.name), userId: user.id },
        },
        {
          component: <Label margin='auto' text={user.status} />,
        },
        {
          component: <UserRoleCell user={user} customRoleNames={customRoleNamesByUser.get(user.id) ?? []} />,
        },
        {
          component: <Text value={user.username} />,
        },
        {
          component: <Text value={showGroupNames(user?.user_groups) || '-'} />,
        },
        {
          component: <Datetime value={user?.last_accessed_at} baseDate={new Date()} maxLevel={1} />,
        },
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
              {/* The email must be present before it is compared: this cell is
                  now built during render (it used to be built inside the fetch
                  callback, by which time the session had resolved), so an
                  unresolved `currentUser` would make `undefined !== username`
                  true and flash an Edit button on the user's own row. */}
              {canManage('users', 'Write') && currentUser?.user?.email && currentUser.user.email !== user.username ? (
                <DsButton
                  tone='ghost'
                  composition='icon-only'
                  size='sm'
                  icon={<SafeIcon src={writeIcon} alt='edit' width={16} height={16} />}
                  aria-label='Edit user'
                  id='edit-user-button'
                  onClick={(e) => {
                    e.stopPropagation();
                    handleEditUserModal(e, user);
                  }}
                />
              ) : (
                <></>
              )}
            </Box>
          ),
        },
      ]),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [users, customRoleNamesByUser, currentUser]
  );

  const handleEditUserModalClose = (updated) => {
    setEditUserModalVisible(false);
    if (updated) {
      fetchUsers();
      fetchCustomRoles();
    }
  };

  const selectedStatusOption = statusOptions.find((o) => o.value === selectedStatus) ?? null;

  // The user-drilldown "Groups" tab renders group data, so it's gated by the
  // same usergroups:Read permission as the top-level Groups tab (tenant-wide
  // admins always qualify). Without it the tab is hidden rather than showing an
  // empty "No Data Available" for a section the user can't read.
  const canReadGroups = isTenantWideRole() || hasPermission('usergroups', 'Read');

  return (
    <>
      <UserModal
        open={addUserModalVisible}
        handleClose={handleAddUserModalClose}
        handleSnackBarData={(data) => {
          if (data.severity === 'success') {
            snackbar.success(data.message);
          } else {
            snackbar.error(data.message);
          }
        }}
        mode='add'
      />
      <UserModal
        open={editUserModalVisible}
        handleClose={handleEditUserModalClose}
        userData={activeUserData}
        handleSnackBarData={(data) => {
          if (data.severity === 'success') {
            snackbar.success(data.message);
          } else {
            snackbar.error(data.message);
          }
        }}
        mode='edit'
      />
      <ListingLayout id='box-all-users'>
        <ListingLayout.Toolbar
          actions={
            canManage('users', 'Write') ? (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <TourLauncher tourId='create-user' label='How to add a user' />
                <DsButton id='new-user' tone='primary' size='md' onClick={() => setAddUserModalVisible(true)}>
                  Add New User
                </DsButton>
              </Box>
            ) : undefined
          }
        >
          <FilterDropdown
            id='all-users-status-filter'
            label='Status'
            options={statusOptions}
            value={selectedStatusOption}
            onSelect={(_e, item) => handleStatusChange({ target: { value: item?.value || '' } })}
          />
          <CustomSearch
            id='all-users-name-search'
            value={userNameInput}
            onChange={(next) => {
              setUserNameInput((prev) => {
                if (prev.trim() !== '' && next.trim() === '') {
                  setSelectedName('');
                  setCurrentPage(0);
                }
                return next;
              });
            }}
            onEnterPress={() => {
              setSelectedName(userNameInput);
              setCurrentPage(0);
            }}
            onClear={() => {
              setUserNameInput('');
              setSelectedName('');
              setCurrentPage(0);
            }}
            label='Enter Name'
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable2
            tableData={allUserTableData}
            headers={allUsersTableHeaders}
            rowsPerPage={perPage}
            totalRows={totalCount}
            onPageChange={onPageChange}
            loading={loading}
            onSortChange={(e) => {
              sortEventChange(e);
            }}
            sort={sortObject}
            tableHeadingCenter={['Status']}
            stickyColumnIndex='7'
            id='all-users'
            pageNumber={currentPage + 1}
            expandable={{
              // `value` must equal the tab's array index (TabPanel matches by
              // index) — so Integration profiles shifts to 0 when the
              // usergroups-gated Groups tab is hidden.
              tabs: [
                ...(canReadGroups
                  ? [
                      {
                        text: 'Groups',
                        value: 0,
                        key: 'groups',
                        componentFn: (option, query) => {
                          return <UserGroup groupNames={query.groupNames.length ? query.groupNames : null} onUserUpdate={fetchUsers} />;
                        },
                      },
                    ]
                  : []),
                {
                  text: 'Integration profiles',
                  value: canReadGroups ? 1 : 0,
                  key: 'integration-profiles',
                  componentFn: (option, query) => (
                    <ListingLayout id='box-user-integration-profiles'>
                      <ListingLayout.Body>
                        <Box sx={{ py: 2 }}>
                          <IntegrationProfiles userId={query.userId} readOnly hideHeading />
                        </Box>
                      </ListingLayout.Body>
                    </ListingLayout>
                  ),
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default AllUsers;
