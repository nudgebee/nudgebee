import React, { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { writeIcon } from '@assets';
import apiUserManagement from '@api1/user';
import { useSession } from 'next-auth/react';
import { hasWriteAccess } from '@lib/auth';
import UserModal from './modal/UserModal';
import { Label } from '@components1/ds/Label';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { getUsersByTenant } from '@lib/UserService';
import UserGroup from './UserGroup';
import { toast as snackbar } from '@components1/ds/Toast';
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
    setAllUserTableData([]);
    setTotalCount(0);
    getUsersByTenant(data)
      .then((res) => {
        let result = res?.users_list_by_tenant?.rows;
        const totalParticipants = res?.users_aggregate_by_tenant?.rows?.[0]?.count ?? 0;
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
              component: <Label margin='auto' text={user.status} />,
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
  }, [currentPage, selectedStatus, sortObject, perPage, selectedName]);

  const handleEditUserModalClose = (updated) => {
    setEditUserModalVisible(false);
    if (updated) {
      fetchUsers();
    }
  };

  const selectedStatusOption = statusOptions.find((o) => o.value === selectedStatus) ?? null;

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
            hasWriteAccess() ? (
              <DsButton id='new-user' tone='primary' size='md' onClick={() => setAddUserModalVisible(true)}>
                Add New User
              </DsButton>
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default AllUsers;
