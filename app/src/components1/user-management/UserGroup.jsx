import React, { useEffect, useState } from 'react';
import apiUserManagement from '@api1/user';
import CustomTable from '@common-new/tables/CustomTable2';
import { Box, Typography, List, ListItem, ListItemText } from '@mui/material';
import { writeIcon } from '@assets';
import GroupModal from './modal/GroupModal';
import { hasWriteAccess } from '@lib/auth';
import UserGroupUsers from './UserGroupUsers';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import SafeIcon from '@components1/common/SafeIcon';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import PropTypes from 'prop-types';
import { toast as snackbar } from '@components1/ds/Toast';

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
        for (let item of response.data?.admin_get_user_groups_v2?.rows ?? []) {
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
              component: <Text value={item.group_roles.map((r) => r.role).join(', ')} sx={{ maxWidth: '200px', overflowWrap: 'normal' }} />,
            },
            { component: <Datetime value={item?.created_at} baseDate={new Date()} /> },
            {
              component: (
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
                  {hasWriteAccess() ? (
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
        setTotalCount(response.data?.admin_get_user_groups_grouping_v2?.rows?.[0]?.count ?? 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const userGroupStyle = {
    listItem: {
      p: '2px 0 0 8px',
    },
    listItemText: {
      m: '0',
    },
  };
  useEffect(() => {
    if (!Object.keys(accounts).length || !groupFdqn.length) {
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
          entity_name: accounts[id] || null,
          entity_namespace: value,
        };
      });
      const renderPermissionList = (permissions, title, formatter) => {
        if (!permissions.length) {
          return null;
        }
        return (
          <Box sx={{ mb: '4px' }}>
            <Typography sx={{ fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-body-lg)', color: 'var(--nb-text-secondary)' }}>
              {title}
            </Typography>
            <List sx={{ p: '4px 0px ' }}>{permissions.map(formatter)}</List>
          </Box>
        );
      };
      const namespaceList = renderPermissionList(namespaceAccountMap, 'Namespace Permission', (h) => (
        <ListItem key={h.entity_id} sx={userGroupStyle.listItem}>
          <ListItemText
            sx={userGroupStyle.listItemText}
            primary={
              <Box>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--nb-text-secondary)' }}>
                  Account: {h.entity_name}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  Namespace: {h.entity_namespace}
                </Typography>
              </Box>
            }
            secondary={
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--nb-text-tertiary)' }}>Role: {snakeToTitleCase(h?.role)}</Typography>
            }
          />
        </ListItem>
      ));
      const accountList = renderPermissionList(accountPermission, 'Account Permission', (h) => (
        <ListItem key={h.entity_id} sx={{ ...userGroupStyle.listItem, display: 'flex', flexDirection: 'column', alignItems: 'flex-start' }}>
          <ListItemText
            sx={{ ...userGroupStyle.listItemText, width: '100%' }}
            primary={
              <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--nb-text-secondary)' }}>
                Account: {accounts[h.entity_id]}
              </Typography>
            }
            secondary={
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--nb-text-tertiary)' }}>
                Role: {snakeToTitleCase(h?.role)}{' '}
              </Typography>
            }
          />
        </ListItem>
      ));
      const tenantList = renderPermissionList(tenantPermission, 'Tenant Permission', (h) => (
        <ListItem key={h.entity_id} sx={userGroupStyle.listItem}>
          <ListItemText
            sx={userGroupStyle.listItemText}
            secondary={
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--nb-text-tertiary)' }}>
                Role: {snakeToTitleCase(h?.role)}{' '}
              </Typography>
            }
          />
        </ListItem>
      ));
      ug[4] = {
        component: (
          <>
            {namespaceList}
            {accountList}
            {tenantList}
          </>
        ),
      };
      return ug;
    });

    setUserGroupList(updatedUserGroupList);
  }, [accounts, groupFdqn]);

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
              hasWriteAccess() ? (
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
