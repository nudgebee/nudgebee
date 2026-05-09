import React, { useEffect, useState } from 'react';
import { Box, IconButton } from '@mui/material';
import { writeIcon } from '@assets';
import apiUserManagement from '@api1/user';
import { useSession } from 'next-auth/react';
import { hasWriteAccess } from '@lib/auth';
import UserModal from './modal/UserModal';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import Datetime from '@components1/common/format/Datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { action } from 'src/utils/actionStyles';
import { getUsersByTenant } from '@lib/UserService';
import { Text } from '@components1/common';
import { colors } from 'src/utils/colors';
import UserGroup from './UserGroup';
import { snackbar } from '@components1/common/snackbarService';
import { safeJSONParse } from 'src/utils/common';

const AllUsers = () => {
  const [loading, setLoading] = useState(false);
  const [allUserTableData, setAllUserTableData] = useState();
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
  const [perPage, setPerPage] = useState(apiUserManagement.getUserPreferencesTablePageSize());

  const { data: currentUser } = useSession({
    required: true,
  });
  const allUsersTableHeaders = [
    { name: 'Name', sortEnabled: true },
    'Status',
    {
      name: 'Role',
      info: 'Your effective role is determined by your assigned roles but may change if you belong to a group with a role for a specific namespace or account.',
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
    }
  };

  const onNameFilterChange = (e) => {
    setCurrentPage(0);
    setSelectedName(e?.target?.value);
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

  const fetchUsers = (nameOverride) => {
    let sortColValue = '';
    if (sortObject.name == 'Email') {
      sortColValue = 'username';
    } else if (sortObject.name == 'Name') {
      sortColValue = 'display_name';
    }
    const nameSearch = nameOverride !== undefined ? nameOverride : selectedName;
    const data = {
      offset: currentPage * perPage,
      limit: perPage,
      sortOrder: sortObject.order,
      sortCol: sortColValue,
      nameSearch: nameSearch,
      statusSearch: selectedStatus,
    };
    setLoading(true);
    setAllUserTableData([]);
    setTotalCount(0);
    getUsersByTenant(data)
      .then((res) => {
        let result = res?.admin_get_users_by_tenant_v2?.rows;
        const totalParticipants = res?.admin_get_users_grouping_by_tenant_v2?.rows?.[0]?.count ?? 0;
        let tableComponentsList = [];
        for (let user of result || []) {
          user.user_groups = safeJSONParse(user.user_groups) || [];
          user.user_roles = safeJSONParse(user.user_roles) || [];
          tableComponentsList.push([
            {
              component: <Text value={user.display_name} showAutoEllipsis />,
              drilldownQuery: { groupNames: user.user_groups.map((group) => group.name) },
            },
            {
              component: <CustomLabels margin='auto' text={user.status} />,
            },
            {
              component: <Text value={user?.user_roles[0]?.role_display_name || user?.user_roles[0]?.role} />,
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
                  {hasWriteAccess() && currentUser.user.email != user.username ? (
                    <IconButton
                      onClick={(e) => {
                        e.stopPropagation();
                        handleEditUserModal(e, user);
                      }}
                      sx={{ ...action.primary }}
                      id='edit-user-button'
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
        setAllUserTableData(tableComponentsList);
        setTotalCount(totalParticipants);
      })
      .finally(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    fetchUsers();
  }, [currentPage, selectedStatus, sortObject, perPage]);

  const handleEditUserModalClose = (updated) => {
    setEditUserModalVisible(false);
    if (updated) {
      fetchUsers();
    }
  };

  const onEnterPress = () => {
    setCurrentPage(0);
    fetchUsers();
  };

  return (
    <BoxLayout2
      sx={{
        padding: '16px 14px 20px 14px',
        alignSelf: 'stretch',
        backgroundColor: colors.background.white,
        borderRadius: '12px',
        boxShadow: '0px 4px 4px 0px #00000026',
        '@media (max-width: 1350px)': {
          padding: '16px 8px 20px 8px',
        },
      }}
      id='box-all-users'
      modalButton={{
        enabled: hasWriteAccess(),
        text: 'Add New User',
        onClick: () => setAddUserModalVisible(true),
        id: 'new-user',
      }}
      sharingOptions={{
        download: { enabled: false },
        sharing: { enabled: false },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: statusOptions,
          onSelect: (e) => handleStatusChange(e),
          minWidth: '150px',
          label: 'By Status',
          value: selectedStatus,
        },
      ]}
      searchOption={{
        enabled: true,
        placeholder: 'Enter Name',
        value: selectedName,
        onChange: onNameFilterChange,
        onClear: () => {
          setSelectedName('');
          setCurrentPage(0);
          fetchUsers('');
        },
        onEnter: onEnterPress,
        width: '250px',
      }}
    >
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
          tabs: [
            {
              text: 'Groups',
              value: 0,
              key: 'groups',
              componentFn: (option, query) => {
                return <UserGroup groupNames={query.groupNames.length ? query.groupNames : null} onUserUpdate={fetchUsers} />;
              },
            },
          ],
        }}
      />
    </BoxLayout2>
  );
};

export default AllUsers;
