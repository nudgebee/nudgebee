import React, { useEffect, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import apiAudits from '@api1/audits';
import Datetime from '@components1/common/format/Datetime';
import Text from '@components1/common/format/Text';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { getSpecificTime } from '@lib/datetime';
import CopyButton from '@components1/common/CopyButton';
import capitalize from 'lodash/capitalize';
import { Box } from '@mui/material';
import {
  convertToReadableFormat,
  formatUserRoleName,
  formatSLOAuditMessage,
  formatActionNameForAuditMessage,
  snakeToTitleCase,
} from 'src/utils/common';
import apiUser from '@api1/user';
import CustomLink from '@components1/common/CustomLink';
import CodeMirrorDiffViewer from '@components1/common/DiffViewer';
import { colors } from 'src/utils/colors';
import { CategoryListing, EventListing } from './common';
import { snackbar } from '@components1/common/snackbarService';

const headers = [
  'User',
  'Summary',
  'Category/Type',
  { name: 'Action', width: '10%' },
  { name: 'Status', width: '10%' },
  { name: 'Created At', width: '8%' },
  'Target',
];

const DiffTab = (option, query, _row) => {
  let currentState = query.data.event_state;
  let prevState = query.data.event_prev_state;

  let currentStateString = '';
  let prevStateString = '';

  if (currentState) {
    try {
      currentStateString = JSON.stringify(JSON.parse(currentState), null, 2);
    } catch (e) {
      console.error(e);
    }
  }

  if (prevState) {
    try {
      prevStateString = JSON.stringify(JSON.parse(prevState), null, 2);
    } catch (e) {
      console.error(e);
    }
  }

  const handleCopy = (text) => {
    navigator.clipboard
      .writeText(text)
      .then(() => {
        snackbar.success('Copied to clipboard');
      })
      .catch((_err) => {
        snackbar.error('Failed to copy to clipboard');
      });
  };
  return (
    <div>
      <div style={{ display: 'flex', flexDirection: 'row', marginBottom: '10px', gap: '470px' }}>
        <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', justifyContent: 'center' }}>
          <span style={{ margin: 10, fontWeight: 500 }}>Previous State</span>
          <CopyButton onClick={() => handleCopy(prevStateString)} style={{ marginLeft: '5px' }} />
        </div>
        <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', justifyContent: 'center', paddingLeft: '20px' }}>
          <span style={{ margin: 10, fontWeight: 500 }}>Current State</span>
          <CopyButton onClick={() => handleCopy(currentStateString)} />
        </div>
      </div>
      <CodeMirrorDiffViewer originalCode={prevStateString} newCode={currentStateString} />
    </div>
  );
};

export const AuditsTable = () => {
  const auditsTableId = 'audits-table';

  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [totalRows, setTotalRows] = useState(0);
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedCategory, setSelectedCategory] = useState('');
  const [selectedCategoryType, setSelectedCategoryType] = useState('');
  const [selectedAction, setSelectedAction] = useState('');
  const [userFilter, setUserFilter] = useState([]);
  const [usersMap, setUsersMap] = useState({});
  const [selectedUser, setSelectedUser] = useState('');
  const [accountFilter, setAccountFilter] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState('');
  const [loading, setLoading] = useState(false);
  const [dateRange, setDateRange] = useState({
    startDate: getSpecificTime(1440),
    endDate: new Date().getTime(),
  });

  useEffect(() => {
    apiAudits
      .lisUsersAndAccounts()
      .then((res) => {
        let accounts =
          res?.data?.cloud_accounts?.rows?.map((item) => {
            return { value: item.id, label: item.account_name };
          }) || [];
        setAccountFilter(accounts);

        let usersMapData = {};
        let users =
          res?.data?.users?.rows?.map((item) => {
            usersMapData[item.id] = item.username;
            return { value: item.username, label: item.username };
          }) || [];
        usersMapData['00000000-0000-0000-0000-000000000000'] = 'SYSTEM';
        users.push({ value: 'SYSTEM', label: 'SYSTEM' });
        setUsersMap(usersMapData);
        setUserFilter(users);
      })
      .catch((error) => {
        console.error('Error loading users and accounts:', error);
      });
  }, []);

  function getTargetComponent(item, targetName) {
    if (item.event_category == 'AUTO_PILOT') {
      let displayName = item.event_target;
      try {
        const data = JSON.parse(item.event_state) || {};
        displayName = data.name || item.event_target;
      } catch (e) {
        console.error(e);
      }
      return (
        <CustomLink href={`/auto-pilot/task/${item.event_target}?accountId=${item.account_id}`}>
          <Text sx={{ color: 'inherit' }} value={displayName} showAutoEllipsis />
        </CustomLink>
      );
    } else if (item.event_category == 'AUTO_RUNBOOK') {
      return (
        <CustomLink href={`/auto-pilot/auto-playbook/task/${item.event_target}?accountId=${item.account_id}`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </CustomLink>
      );
    } else if (item.event_category == 'ACCOUNTS') {
      return (
        <CustomLink href={`/kubernetes/details/${item.event_target}`}>
          <Text sx={{ color: 'inherit' }} value={targetName} showAutoEllipsis />
        </CustomLink>
      );
    } else if (item.event_category == 'GROUPS') {
      return (
        <CustomLink href={`/user-management?groupId=${item.event_target}#groups`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </CustomLink>
      );
    } else if (item.event_category == 'TICKET') {
      return (
        <CustomLink href={`/tickets`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </CustomLink>
      );
    } else if (item.event_category == 'NOTIFICATIONS') {
      let platform = targetName;
      try {
        const data = JSON.parse(item.event_state) || {};
        platform = data.platform || targetName;
      } catch (e) {
        console.error(e);
      }
      return <Text placement='top' marginBottom='0px' value={platform} showAutoEllipsis />;
    } else if (item.event_category == 'NOTIFICATIONS CHAT ACTIONS') {
      // Target is a cloud account ID, show the account name
      return <Text placement='top' marginBottom='0px' value={targetName} showAutoEllipsis />;
    }

    return <Text placement='top' marginBottom='0px' value={targetName} showAutoEllipsis />;
  }

  function getSummaryMessageForCreate(item) {
    let data = {};
    try {
      data = JSON.parse(item.event_state) || {};
    } catch (e) {
      console.error(e);
    }
    if (item.event_type == 'TENANT_USER_CREATE') {
      let userName = usersMap[data.user] || data.user;
      return <Text value={`User ${userName} Assigned To Tenant`} showAutoEllipsis />;
    } else if (item.event_type == 'RECOMMENDATION_APPLY') {
      return <Text value={`Recommendation Resolution Applied`} showAutoEllipsis />;
    } else if (item.event_type == 'K8SRELAY_TASK_CREATE') {
      return <Text value={`New K8s Task ${formatActionNameForAuditMessage(data?.body?.action_name)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'TICKET_CONFIGURATION_CREATE') {
      return <Text value={`New Ticket Configuration With Name ${data.name || data.account_name} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'NOTIFICATION_SLACK_CONFIGURATION_CREATE') {
      return <Text value={`New Slack Configuration Created`} showAutoEllipsis />;
    } else if (item.event_type == 'NOTIFICATION_MS_TEAMS_CONFIGURATION_CREATE') {
      return <Text value={`New Ms Teams Configuration Created`} showAutoEllipsis />;
    } else if (item.event_type == 'NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_CREATE') {
      return <Text value={`Google Chat Configuration Created`} showAutoEllipsis />;
    } else if (item.event_type == 'ROLE_USER_CREATE') {
      return <Text value={`New Role With Name ${formatUserRoleName(data.role)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'ROLE_ACCOUNT_CREATE') {
      return <Text value={`New Account Role Created`} showAutoEllipsis />;
    } else if (item.event_type == 'ROLE_GROUP_CREATE') {
      return <Text value={`New ${capitalize(item.event_category)} With Name ${formatUserRoleName(data.role)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'SLO_CREATE') {
      return <Text value={`New ${capitalize(item.event_category)} With Name ${formatSLOAuditMessage(data)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'AUTORUNBOOK_ACTION_CREATE') {
      return <Text value={`New ${capitalize(item.event_category)} Action With Name ${data.action_name} Created`} showAutoEllipsis />;
    }
    const name = data.name || data.account_name || data.job_name || data.title || data.action_name || '';
    const value = `New ${capitalize(item.event_category)} ${name ? `With Name ${name} ` : ''} Created`;
    return <Text value={value} showAutoEllipsis />;
  }

  function getSummaryMessageForUpdate(item, accountName) {
    let data = {};
    try {
      data = JSON.parse(item.event_state) || {};
    } catch (e) {
      console.error(e);
    }

    if (item.event_type == 'TICKET_CONFIGURATION_UPDATE') {
      return <Text value={`Ticket Configuration With Name ${data.name || data.account_name} Updated`} showAutoEllipsis />;
    } else if (item.event_type == 'NOTIFICATION_SLACK_CONFIGURATION_CREATE') {
      return <Text value={`Slack Configuration Updated`} showAutoEllipsis />;
    } else if (item.event_type == 'NOTIFICATION_MS_TEAMS_CONFIGURATION_CREATE') {
      return <Text value={`Ms Teams Configuration Updated`} showAutoEllipsis />;
    } else if (item.event_type == 'NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_CREATE') {
      return <Text value={`Google Chat Configuration Updated`} showAutoEllipsis />;
    } else if (item.event_type == 'ROLE_USER_UPDATE') {
      return <Text value={`New ${capitalize(item.event_category)} With Name ${formatUserRoleName(data?.role)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'ROLE_ACCOUNT_CREATE') {
      return <Text value={`New Account Role Created`} showAutoEllipsis />;
    } else if (item.event_type == 'ROLE_GROUP_CREATE') {
      return <Text value={`New ${capitalize(item.event_category)} With Name ${formatUserRoleName(data?.role)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'SLO_UPDATE') {
      return <Text value={`New ${capitalize(item.event_category)} With Name ${formatSLOAuditMessage(data)} Created`} showAutoEllipsis />;
    } else if (item.event_type == 'UPDATE_AGENT_TOKEN') {
      return <Text value={`Agent Token of Account ${accountName} is Updated`} showAutoEllipsis />;
    } else if (item.event_type == 'CUSTOM_AGENT_UPDATE') {
      return <Text value={`New ${capitalize(item.event_category)} Updated`} showAutoEllipsis />;
    } else if (item.event_type == 'MESSAGING_PLATFORM_UPDATE') {
      return <Text value={`Messaging Platform For ${formatActionNameForAuditMessage(data?.platform)} Updated`} showAutoEllipsis />;
    }
    return (
      <Text
        value={`${capitalize(item.event_category)} With Name ${data.name || data.account_name || data.job_name || data.action_name} Updated`}
        showAutoEllipsis
      />
    );
  }

  function getSummaryMessageForDelete(item) {
    let data = {};
    let prevStateData = {};
    try {
      prevStateData = JSON.parse(item.event_prev_state) || {};
      data = JSON.parse(item.event_attr) || {};
    } catch (e) {
      console.error(e);
    }
    const name =
      data.name ||
      data.account_name ||
      data.job_name ||
      data.title ||
      data.action_name ||
      data.agent_name ||
      prevStateData.action_name ||
      prevStateData.name ||
      '';
    if (item.event_type == 'AUTORUNBOOK_ACTION_DELETE') {
      return <Text value={` ${capitalize(item.event_category)} Action With Name ${name} Deleted`} />;
    }
    if (item.event_type == 'MESSAGING_PLATFORM_DELETE') {
      return <Text value={`Messaging Platform For ${formatActionNameForAuditMessage(prevStateData?.platform)} Deleted`} />;
    }
    const value = `${capitalize(item.event_category)} ${name ? `With Name ${name} ` : ''} Deleted`;
    return <Text value={value} showAutoEllipsis />;
  }

  function getSummaryMessage(item, accountName) {
    if (item.event_action == 'CREATE') {
      return getSummaryMessageForCreate(item);
    } else if (item.event_action == 'UPDATE') {
      return getSummaryMessageForUpdate(item, accountName);
    } else if (item.event_action == 'DELETE') {
      return getSummaryMessageForDelete(item);
    }
    return <Text value={''} showAutoEllipsis />;
  }

  useEffect(
    () => {
      let query = {
        status: selectedStatus,
        category: selectedCategory,
        action: selectedAction,
        username: selectedUser,
        accountId: selectedAccount,
        eventStart: dateRange.startDate ? new Date(dateRange.startDate) : undefined,
        eventEnd: dateRange.endDate ? new Date(dateRange.endDate) : undefined,
        eventType: selectedCategoryType,
      };
      setData([]);
      setTotalRows(0);
      if (userFilter.length > 0) {
        setLoading(true);
        apiAudits
          .listAudits(limit, offset, query)
          .then((res) => {
            setLoading(false);
            let data = res.data?.audits?.map((item) => {
              let userName = usersMap[item.user_id] || item.user_id;
              let accountName = item.account_id;
              let targetName = item.event_target;
              for (let account of accountFilter) {
                if (account.value === item.account_id) {
                  accountName = account.label;
                }
                // Check if target matches an account
                if (account.value === item.event_target) {
                  targetName = account.label;
                }
              }

              return [
                {
                  component: (
                    <>
                      <Text value={userName} showAutoEllipsis />
                      {item.account_id && (
                        <Box
                          display={'flex'}
                          alignItems={'center'}
                          gap={'2px'}
                          color={colors.text.secondaryDark}
                          sx={{
                            fontSize: '12px',
                            '& a': {
                              textDecoration: 'none',
                              '&:hover': {
                                color: colors.text.primary,
                                textDecoration: 'underline',
                              },
                            },
                          }}
                        >
                          {'cl: '}
                          <CustomLink href={'/kubernetes/details/' + item.account_id}>
                            <Text showAutoEllipsis value={accountName} secondaryText />
                          </CustomLink>
                        </Box>
                      )}
                    </>
                  ),
                  drilldownQuery: {
                    userName: userName,
                    userId: item.user_id,
                    accountName: accountName,
                    accountId: item.account_id,
                    data: item,
                  },
                },
                {
                  component: getSummaryMessage(item, accountName),
                },
                {
                  component: (
                    <Text showAutoEllipsis value={capitalize(item.event_category.replaceAll('_', ' ')) + '/' + snakeToTitleCase(item.event_type)} />
                  ),
                },
                { component: <Text showAutoEllipsis value={capitalize(item.event_action)} /> },
                {
                  component: (
                    <CustomLabels
                      margin='auto'
                      text={capitalize(item.event_status)}
                      variant={item.event_status.toLowerCase() === 'success' ? 'green' : 'red'}
                    />
                  ),
                },
                { component: <Datetime value={item.event_time} baseDate={new Date()} /> },
                { component: getTargetComponent(item, targetName) },
              ];
            });
            setData(data);
            setTotalRows(res.data?.count);
          })
          .catch((_error) => {
            setLoading(false);
            snackbar.error('Failed to fetch audits');
          });
      }
    },
    [
      offset,
      limit,
      selectedStatus,
      selectedCategory,
      selectedCategoryType,
      selectedAction,
      selectedUser,
      selectedAccount,
      dateRange.startDate,
      dateRange.endDate,
      userFilter,
    ],
    []
  );

  function handleDateRangeChange(passedSelectedDateTime) {
    setDateRange((prevDateRange) => ({
      ...prevDateRange,
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    }));
  }

  return (
    <BoxLayout2
      id='audit'
      sx={{
        padding: '16px 14px 20px 14px',
        alignSelf: 'stretch',
        backgroundColor: 'white',
      }}
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: dateRange.startDate,
          endTime: dateRange.endDate,
        },
      }}
      sharingOptions={{
        sharing: {
          enabled: true,
          onClick: null,
        },
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: auditsTableId,
            };
          },
        },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: ['SUCCESS', 'FAILURE'].map((v) => {
            return { value: v, label: capitalize(v) };
          }),
          onSelect: (e) => {
            setSelectedStatus(e?.target?.value);
            setOffset(0);
          },
          minWidth: '150px',
          label: 'Status',
          value: selectedStatus,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: CategoryListing.sort((a, b) => {
            return a.localeCompare(b);
          }).map((v) => {
            return { value: v, label: capitalize(v.replaceAll('_', ' ')) };
          }),
          onSelect: (e) => {
            setSelectedCategory(e?.target?.value);
            setOffset(0);
          },
          minWidth: '150px',
          label: 'Category',
          value: selectedCategory,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: EventListing.sort((a, b) => {
            return a.localeCompare(b);
          }).map((v) => {
            return { value: v, label: convertToReadableFormat(v.replaceAll('_', ' ')) };
          }),
          onSelect: (e) => {
            setSelectedCategoryType(e?.target?.value);
            setOffset(0);
          },
          minWidth: '150px',
          label: 'Event Type',
          value: selectedCategoryType,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: ['CREATE', 'UPDATE', 'DELETE', 'READ']
            .sort((a, b) => {
              return a.localeCompare(b);
            })
            .map((v) => {
              return { value: v, label: capitalize(v) };
            }),
          onSelect: (e) => {
            setSelectedAction(e?.target?.value);
            setOffset(0);
          },
          minWidth: '150px',
          label: 'Action',
          value: selectedAction,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: userFilter,
          onSelect: (e) => {
            setSelectedUser(e?.target?.value);
            setOffset(0);
          },
          minWidth: '150px',
          label: 'User',
          value: selectedUser,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: accountFilter,
          onSelect: (e) => {
            setSelectedAccount(e?.target?.value);
            setOffset(0);
          },
          minWidth: '150px',
          label: 'Cluster',
          value: selectedAccount,
        },
      ]}
    >
      <CustomTable2
        id={auditsTableId}
        headers={headers}
        tableData={data}
        rowsPerPage={limit}
        onPageChange={(e, limit1) => {
          setOffset((e - 1) * limit1);
          setLimit(limit1);
        }}
        totalRows={totalRows}
        showExpandable={true}
        expandable={{
          tabs: [
            {
              text: 'Diff State',
              value: 0,
              key: 'audit-diff-state',
              componentFn: DiffTab,
            },
          ],
        }}
        loading={loading}
        textAlign='left'
        tableHeadingCenter={['Status']}
        pageNumber={offset / limit + 1}
      />
    </BoxLayout2>
  );
};

AuditsTable.propTypes = {};
