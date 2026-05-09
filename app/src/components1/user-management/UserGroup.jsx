import React, { useEffect, useState } from 'react';
import apiUserManagement from '@api1/user';
import CustomTable from '@components1/common/tables/CustomTable2';
import { Box, IconButton, Typography, List, ListItem, ListItemText } from '@mui/material';
import { writeIcon } from '@assets';
import GroupModal from './modal/GroupModal';
import { hasWriteAccess } from '@lib/auth';
import UserGroupUsers from './UserGroupUsers';
import Datetime from '@components1/common/format/Datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { action } from 'src/utils/actionStyles';
import { Text } from '@components1/common';
import CustomButton from '@components1/common/NewCustomButton';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import PropTypes from 'prop-types';
import { snackbar } from '@components1/common/snackbarService';

function UserGroup({ groupNames = [], onUserUpdate }) {
  const [groupModalVisible, setGroupModalVisible] = React.useState(false);
  const [userGroupList, setUserGroupList] = React.useState([]);
  const [loading, setLoading] = useState(false);
  const [activeGroupData, setActiveGroupData] = React.useState(null);
  const [searchName, setSearchName] = useState('');
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
  }, [currentPage, perPage]);

  const fetchUserGroups = (nameOverride) => {
    if (groupNames == null) {
      return;
    }
    const nameSearch = nameOverride !== undefined ? nameOverride : searchName;
    const data = {
      offset: currentPage * perPage,
      limit: perPage,
      nameSearch: groupNames.length ? groupNames : nameSearch,
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
                    <IconButton
                      onClick={(e) => {
                        handleEditGroupModal(e, item);
                      }}
                      sx={{ ...action.primary }}
                    >
                      <Box
                        component='img'
                        sx={{
                          marginX: 'auto',
                          height: '16px',
                          width: '16px',
                        }}
                        alt='more'
                        src={writeIcon.default.src}
                      />
                    </IconButton>
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

  const onEnterPress = () => {
    setCurrentPage(0);
    fetchUserGroups();
  };

  const onNameFilterChange = (e) => {
    setSearchName(e?.target?.value);
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
            <Typography sx={{ fontWeight: 500, fontSize: '14px', color: '#374151' }}>{title}</Typography>
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
                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>Account: {h.entity_name}</Typography>
                <Typography sx={{ fontSize: '12px', fontWeight: 500 }}>Namespace: {h.entity_namespace}</Typography>
              </Box>
            }
            secondary={<Typography sx={{ fontSize: '14px', color: '#737373' }}>Role: {snakeToTitleCase(h?.role)}</Typography>}
          />
        </ListItem>
      ));
      const accountList = renderPermissionList(accountPermission, 'Account Permission', (h) => (
        <ListItem key={h.entity_id} sx={{ ...userGroupStyle.listItem, display: 'flex', flexDirection: 'column', alignItems: 'flex-start' }}>
          <ListItemText
            sx={{ ...userGroupStyle.listItemText, width: '100%' }}
            primary={<Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>Account: {accounts[h.entity_id]}</Typography>}
            secondary={<Typography sx={{ fontSize: '14px', color: '#737373' }}>Role: {snakeToTitleCase(h?.role)} </Typography>}
          />
        </ListItem>
      ));
      const tenantList = renderPermissionList(tenantPermission, 'Tenant Permission', (h) => (
        <ListItem key={h.entity_id} sx={userGroupStyle.listItem}>
          <ListItemText
            sx={userGroupStyle.listItemText}
            secondary={<Typography sx={{ fontSize: '14px', color: '#737373' }}>Role: {snakeToTitleCase(h?.role)} </Typography>}
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
  return (
    <>
      <BoxLayout2
        dateRange={{ enabled: false }}
        sharingOptions={{
          sharing: {
            enabled: false,
          },
          download: {
            enabled: false,
          },
        }}
        filterOptions={[]}
        searchOption={
          groupNames?.length
            ? { enabled: false }
            : {
                enabled: true,
                placeholder: 'Enter Name',
                value: searchName,
                onChange: onNameFilterChange,
                onClear: () => {
                  setSearchName('');
                  setCurrentPage(0);
                  fetchUserGroups('');
                },
                onEnter: onEnterPress,
                width: '250px',
              }
        }
        extraOptions={[
          <>
            {hasWriteAccess() && !groupNames?.length && <CustomButton id='new-user-group' text={'Add User Group'} onClick={handleAddGroupModal} />}
          </>,
        ]}
      >
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
      </BoxLayout2>
    </>
  );
}
export default UserGroup;

UserGroup.propTypes = {
  groupNames: PropTypes.array,
  onUserUpdate: PropTypes.func,
};
