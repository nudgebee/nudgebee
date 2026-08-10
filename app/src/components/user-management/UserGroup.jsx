import React, { useCallback, useEffect, useMemo, useState } from 'react';
import apiUserManagement from '@api1/user';
import CustomTable from '@shared/tables/CustomTable2';
import { Box, Typography, List, ListItem, ListItemText } from '@mui/material';
import { writeIcon } from '@assets';
import GroupModal from './modal/GroupModal';
import { canManage, canReadCustomRoles } from '@lib/auth';
import { listCustomRoles } from '@api1/roles';
import UserGroupUsers from './UserGroupUsers';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import { ListingLayout } from '@ui/ListingLayout';
import CustomSearch from '@shared/CustomSearch';
import { Button as DsButton } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import PropTypes from 'prop-types';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';

function UserGroup({ groupNames = [], onUserUpdate }) {
  const [groupModalVisible, setGroupModalVisible] = React.useState(false);
  const [userGroupList, setUserGroupList] = React.useState([]);
  const [loading, setLoading] = useState(false);
  const [activeGroupData, setActiveGroupData] = React.useState(null);
  const [searchName, setSearchName] = useState('');
  const [groupNameInput, setGroupNameInput] = useState('');
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [perPage, setPerPage] = useState(apiUserManagement.getUserPreferencesTablePageSize());
  const [accounts, setAccounts] = useState({});
  const [groupFdqn, setGroupFdqn] = useState([]);
  // Dynamic-RBAC custom roles held by each group. These live in
  // custom_role_assignments, NOT in the group_roles blob usergroups_list
  // returns — so a group whose only role is a custom one rendered an empty
  // Roles cell until this was joined in client-side.
  const [customRoles, setCustomRoles] = useState([]);

  const handleEditGroupModal = (event, groupData) => {
    event.stopPropagation();
    setActiveGroupData(groupData);
    setGroupModalVisible(true);
  };

  const handleAddGroupModal = () => {
    setActiveGroupData(null);
    setGroupModalVisible(true);
  };

  const handleGroupModalClose = (shouldUpdate) => {
    setGroupModalVisible(false);
    setActiveGroupData(null);
    if (shouldUpdate) {
      fetchUserGroups();
      fetchCustomRoles();
    }
  };

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setPerPage(limit);
  };

  useEffect(() => {
    apiUserManagement.listAccounts().then((res) => {
      if (res.length > 0) {
        const result = res.reduce((acc, item) => {
          acc[item.id] = item.account_name;
          return acc;
        }, {});
        setAccounts(result || {});
      }
    });
  }, []);

  // Gated on canReadCustomRoles: customroles_list 403s for a plain
  // usergroups:Read holder and 400s tenant-wide when CUSTOM_ROLES is off, and
  // this column is an enrichment — it must not turn either into an error for a
  // page that otherwise works. Failure leaves the built-in roles rendering.
  // Re-run after any group write, not just on mount: the custom-role column is
  // derived from each role's `group_ids`, which the group modal changes via the
  // role-side replace-all assignment API. See the same fix in AllUsers.
  const fetchCustomRoles = useCallback(() => {
    if (!canReadCustomRoles()) {
      return;
    }
    listCustomRoles()
      .then((roles) => setCustomRoles(roles ?? []))
      .catch((err) => {
        console.error('Failed to load custom roles for the groups list:', err);
      });
  }, []);

  useEffect(() => {
    fetchCustomRoles();
  }, [fetchCustomRoles]);

  useEffect(() => {
    fetchUserGroups();
  }, [currentPage, perPage, searchName]);

  const fetchUserGroups = () => {
    if (groupNames == null) {
      return;
    }
    const data = {
      offset: currentPage * perPage,
      limit: perPage,
      nameSearch: groupNames.length ? groupNames : searchName,
    };
    setLoading(true);
    setUserGroupList([]);
    setTotalCount(0);
    apiUserManagement
      .listUserGroups(data)
      .then((response) => {
        let userGroupRows = [];
        let groupFdqn = [];
        for (let item of response.data?.usergroups_list?.rows ?? []) {
          item.group_roles = safeJSONParse(item?.group_roles) || [];
          groupFdqn.push(item.id + '|' + item.name);
          userGroupRows.push([
            {
              component: <Text value={item.name} />,
              drilldownQuery: {
                group_name: item?.name,
                group_id: item?.id,
                group_roles: item.group_roles,
              },
            },
            {
              component: <Text value={item.member_count} />,
            },
            {
              component: <Text value={item.description} />,
            },
            {
              component: <Text value={item?.owner_display_name} />,
            },
            {
              component: (
                <Text value={item.group_roles.map((r) => r.role).join(', ')} sx={{ maxWidth: ds.space.mul(0, 100), overflowWrap: 'normal' }} />
              ),
            },
            { component: <Datetime value={item?.created_at} baseDate={new Date()} /> },
            {
              component: (
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
                  {canManage('usergroups', 'Write') ? (
                    <DsButton
                      tone='ghost'
                      composition='icon-only'
                      size='sm'
                      icon={<SafeIcon src={writeIcon} alt='edit' width={16} height={16} />}
                      aria-label='Edit group'
                      onClick={(e) => {
                        handleEditGroupModal(e, item);
                      }}
                    />
                  ) : (
                    <></>
                  )}
                </Box>
              ),
            },
          ]);
        }
        setGroupFdqn(groupFdqn);
        setUserGroupList(userGroupRows);
        setTotalCount(response.data?.usergroups_aggregate?.rows?.[0]?.count ?? 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const userGroupStyle = {
    listItem: {
      p: 'var(--ds-space-1) 0 0 var(--ds-space-2)',
    },
    listItemText: {
      m: '0',
    },
  };

  // group id → the custom roles bound to it. A role reaches a group either
  // tenant-globally (`group_ids`) or bound to specific accounts
  // (`group_account_assignments`); both are shown, the latter named with its
  // account so the two aren't confused.
  const customRolesByGroup = useMemo(() => {
    const map = new Map();
    const add = (groupId, entry) => {
      if (!groupId) return;
      if (!map.has(groupId)) map.set(groupId, []);
      map.get(groupId).push(entry);
    };
    for (const role of customRoles) {
      for (const groupId of role.group_ids ?? []) {
        add(groupId, { roleId: role.id, name: role.name, accountId: null });
      }
      for (const binding of role.group_account_assignments ?? []) {
        add(binding.principal_id, { roleId: role.id, name: role.name, accountId: binding.entity_id });
      }
    }
    return map;
  }, [customRoles]);

  useEffect(() => {
    // Only the group rows are required. `accounts` is a name lookup with an id
    // fallback below, so waiting on it would hide every role from a tenant with
    // no cloud account — including the custom ones, which need no account.
    if (!groupFdqn.length) {
      return;
    }
    const updatedUserGroupList = userGroupList.map((ug) => {
      if (!ug[0].drilldownQuery?.group_roles) {
        return ug;
      }
      const groupRoles = ug[0].drilldownQuery.group_roles;
      const namespacePermission = groupRoles.filter((np) => np.entity_type === 'k8s_namespace');
      const accountPermission = groupRoles.filter((np) => np.entity_type === 'account');
      const tenantPermission = groupRoles.filter((np) => np.entity_type === 'tenant');
      const namespaceAccountMap = namespacePermission.map((item) => {
        const [id, value] = item.entity_id.split(':');
        return {
          ...item,
          entity_name: accounts[id] || id,
          entity_namespace: value,
        };
      });
      const renderPermissionList = (permissions, title, formatter) => {
        if (!permissions.length) {
          return null;
        }
        return (
          <Box sx={{ mb: 'var(--ds-space-1)' }}>
            <Typography sx={{ fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-body-lg)', color: ds.gray[700] }}>
              {title}
            </Typography>
            <List sx={{ p: 'var(--ds-space-1) 0px ' }}>{permissions.map(formatter)}</List>
          </Box>
        );
      };
      const namespaceList = renderPermissionList(namespaceAccountMap, 'Namespace Permission', (h) => (
        <ListItem key={h.entity_id} sx={userGroupStyle.listItem}>
          <ListItemText
            sx={userGroupStyle.listItemText}
            primary={
              <Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  Account: {h.entity_name}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  Namespace: {h.entity_namespace}
                </Typography>
              </Box>
            }
            secondary={<Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[600] }}>Role: {snakeToTitleCase(h?.role)}</Typography>}
          />
        </ListItem>
      ));
      const accountList = renderPermissionList(accountPermission, 'Account Permission', (h) => (
        <ListItem key={h.entity_id} sx={{ ...userGroupStyle.listItem, display: 'flex', flexDirection: 'column', alignItems: 'flex-start' }}>
          <ListItemText
            sx={{ ...userGroupStyle.listItemText, width: '100%' }}
            primary={
              <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                Account: {accounts[h.entity_id] || h.entity_id}
              </Typography>
            }
            secondary={<Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[600] }}>Role: {snakeToTitleCase(h?.role)} </Typography>}
          />
        </ListItem>
      ));
      const tenantList = renderPermissionList(tenantPermission, 'Tenant Permission', (h) => (
        <ListItem key={h.entity_id} sx={userGroupStyle.listItem}>
          <ListItemText
            sx={userGroupStyle.listItemText}
            secondary={<Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[600] }}>Role: {snakeToTitleCase(h?.role)} </Typography>}
          />
        </ListItem>
      ));
      const customList = renderPermissionList(customRolesByGroup.get(ug[0].drilldownQuery.group_id) ?? [], 'Custom Roles', (h) => (
        <ListItem key={`${h.roleId}:${h.accountId ?? 'tenant'}`} sx={userGroupStyle.listItem}>
          <ListItemText
            sx={userGroupStyle.listItemText}
            primary={
              h.accountId ? (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  Account: {accounts[h.accountId] || h.accountId}
                </Typography>
              ) : null
            }
            secondary={<Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[600] }}>Role: {h.name}</Typography>}
          />
        </ListItem>
      ));
      ug[4] = {
        component: (
          <>
            {namespaceList}
            {accountList}
            {tenantList}
            {customList}
          </>
        ),
      };
      return ug;
    });

    setUserGroupList(updatedUserGroupList);
  }, [accounts, groupFdqn, customRolesByGroup]);

  const userGroupTableHeaders = [
    { name: 'Group Name', width: '15%' },
    { name: 'Total Members', width: '10%' },
    { name: 'Description', width: '15%' },
    { name: 'Owner', width: '20%' },
    { name: 'Roles', width: '30%' },
    { name: 'Created At', width: '8%' },
    { name: '', width: '2%' },
  ];
  const isDrilldown = !!groupNames?.length;

  return (
    <>
      <GroupModal
        open={groupModalVisible}
        handleClose={handleGroupModalClose}
        groupData={activeGroupData}
        handleSnackBarData={(data) => {
          if (data.severity === 'success') {
            snackbar.success(data.message);
          } else {
            snackbar.error(data.message);
          }
        }}
      />
      <ListingLayout id='box-user-groups'>
        {!isDrilldown && (
          <ListingLayout.Toolbar
            actions={
              canManage('usergroups', 'Write') ? (
                <DsButton id='new-user-group' tone='primary' size='md' onClick={handleAddGroupModal}>
                  Add User Group
                </DsButton>
              ) : undefined
            }
          >
            <CustomSearch
              id='user-groups-search'
              value={groupNameInput}
              onChange={(next) => {
                setGroupNameInput((prev) => {
                  if (prev.trim() !== '' && next.trim() === '') {
                    setSearchName('');
                    setCurrentPage(0);
                  }
                  return next;
                });
              }}
              onEnterPress={() => {
                setSearchName(groupNameInput);
                setCurrentPage(0);
              }}
              onClear={() => {
                setGroupNameInput('');
                setSearchName('');
                setCurrentPage(0);
              }}
              label='Enter Name'
            />
          </ListingLayout.Toolbar>
        )}
        <ListingLayout.Body>
          <CustomTable
            checkForTabsWithData={function () {
              return;
            }}
            headers={userGroupTableHeaders}
            tableData={userGroupList}
            rowsPerPage={perPage}
            totalRows={totalCount}
            onPageChange={onPageChange}
            stickyColumnIndex='7'
            expandable={{
              tabs: [
                {
                  text: 'Users',
                  value: 0,
                  key: 'users',
                  componentFn: (option, query, _row) => {
                    return (
                      <UserGroupUsers
                        groupId={query?.group_id}
                        onUserUpdate={() => {
                          fetchUserGroups();
                          if (onUserUpdate) {
                            onUserUpdate();
                          }
                        }}
                      />
                    );
                  },
                },
              ],
            }}
            loading={loading}
            pageNumber={currentPage + 1}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
}
export default UserGroup;

UserGroup.propTypes = {
  groupNames: PropTypes.array,
  onUserUpdate: PropTypes.func,
};
