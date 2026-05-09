import React, { useEffect, useState } from 'react';
import { Typography, Box, IconButton } from '@mui/material';
import { writeIcon } from '@assets';
import apiUserManagement from '@api1/user';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { hasWriteAccess } from '@lib/auth';
import UserModal from './modal/UserModal';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { action } from 'src/utils/actionStyles';
import { snackbar } from '@components1/common/snackbarService';
import { useSession } from 'next-auth/react';

const UserGroupUsers = ({ groupId, onUserUpdate }) => {
  const [loading, setLoading] = useState(false);
  const [allUserTableData, setAllUserTableData] = useState();
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [perPage, setPerPage] = useState(apiUserManagement.getUserPreferencesTablePageSize());
  const [editUserModalVisible, setEditUserModalVisible] = useState(false);
  const [activeUserData, setActiveUserData] = useState();
  const allUsersTableHeaders = ['Name ', 'Status', 'Role', 'Email', ''];

  const { data: currentUser } = useSession({
    required: true,
  });
  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setPerPage(limit);
  };

  const handleEditUserModal = (event, userData) => {
    setActiveUserData(userData?.user);
    setEditUserModalVisible(true);
  };

  useEffect(() => {
    listUserGroupUsers();
  }, [groupId, currentPage, perPage]);

  const listUserGroupUsers = () => {
    setLoading(true);
    const data = {
      offset: currentPage * perPage,
      limit: perPage,
      id: groupId,
    };
    setAllUserTableData([]);
    setTotalCount(0);
    apiUserManagement
      .listUserGroupUsers(data)
      .then((res) => {
        let result = res?.data?.usergroup_users;
        const totalParticipants = res?.data?.usergroup_users_aggregate?.aggregate.count;
        let tableComponentsList = [];
        for (let user of result || []) {
          tableComponentsList.push([
            {
              component: (
                <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: 500, color: '#374151' }}>{user.user.display_name}</Typography>
              ),
            },
            {
              component: <CustomLabels text={user.user.status} />,
            },
            {
              component: (
                <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: 500, color: '#374151' }}>
                  {user.user.user_roles?.[0]?.roleByRole?.display_name || user.user?.user_roles?.[0]?.role}
                </Typography>
              ),
            },
            {
              component: (
                <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: 500, color: '#374151' }}>{user.user.username}</Typography>
              ),
            },
            {
              component: (
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
                  {hasWriteAccess() && currentUser?.user?.email !== user?.user?.username ? (
                    <IconButton
                      onClick={(e) => {
                        handleEditUserModal(e, user);
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
        setAllUserTableData(tableComponentsList);
        setTotalCount(totalParticipants);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  const handleEditUserModalClose = () => {
    setEditUserModalVisible(false);
  };

  return (
    <>
      <UserModal
        open={editUserModalVisible}
        handleClose={handleEditUserModalClose}
        userData={activeUserData}
        handleSnackBarData={(data) => {
          if (data.severity === 'success') {
            listUserGroupUsers();
            if (onUserUpdate) {
              onUserUpdate();
            }
          }
          if (['success', 'error'].includes(data.severity)) {
            snackbar[data.severity](data.message);
          }
        }}
        mode='edit'
      />
      <CustomTable2
        checkForTabsWithData={function () {
          return;
        }}
        tableData={allUserTableData}
        headers={allUsersTableHeaders}
        rowsPerPage={perPage}
        totalRows={totalCount}
        onPageChange={onPageChange}
        loading={loading}
        pageNumber={currentPage + 1}
      />
    </>
  );
};

export default UserGroupUsers;
