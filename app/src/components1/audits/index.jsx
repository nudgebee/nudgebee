import React, { useEffect, useState } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable2 from '@common-new/tables/CustomTable2';
import apiAudits from '@api1/audits';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import { Label } from '@components1/ds/Label';
import { getSpecificTime } from '@lib/datetime';
import CopyButton from '@common-new/CopyButton';
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
import { Link } from '@components1/ds/Link';
import CodeMirrorDiffViewer from '@components1/common/DiffViewer';
import { CategoryListing, EventListing } from './common';
import { toast as snackbar } from '@components1/ds/Toast';

const headers = [
  'User',
  'Summary',
  'Category/Type',
  { name: 'Action', width: '10%' },
  { name: 'Status', width: '10%' },
  { name: 'Created At', width: '8%' },
  'Target',
];

// formatStateForDiff renders a stored state value for the diff viewer.
// Most event types store JSON; some (e.g. RESOURCE_ACTION) store a plain
// string like "STOPPED" — render those as-is rather than producing an empty
// pane via a failed JSON.parse.
const formatStateForDiff = (state) => {
  if (!state) {
    return '';
  }
  const trimmed = state.trim();
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      return JSON.stringify(JSON.parse(state), null, 2);
    } catch (e) {
      console.error(e);
      return state;
    }
  }
  return state;
};

const DiffTab = (option, query, _row) => {
  const currentStateString = formatStateForDiff(query.data.event_state);
  const prevStateString = formatStateForDiff(query.data.event_prev_state);

  return (
    <CodeMirrorDiffViewer
      originalCode={prevStateString}
      newCode={currentStateString}
      leftLabel={
        <>
          <span style={{ margin: 10, fontWeight: 'var(--ds-font-weight-medium)' }}>Previous State</span>
          <CopyButton text={prevStateString} />
        </>
      }
      rightLabel={
        <>
          <span style={{ margin: 10, fontWeight: 'var(--ds-font-weight-medium)' }}>Current State</span>
          <CopyButton text={currentStateString} />
        </>
      }
    />
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
        <Link href={`/auto-pilot/task/${item.event_target}?accountId=${item.account_id}`}>
          <Text sx={{ color: 'inherit' }} value={displayName} showAutoEllipsis />
        </Link>
      );
    } else if (item.event_category == 'AUTOMATION') {
      return (
        <Link href={`/workflow/${item.event_target}?accountId=${item.account_id}`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </Link>
      );
    } else if (item.event_category == 'AUTO_RUNBOOK') {
      // Legacy category — historical rows from the auto-pilot Python service.
      // New automation audits use the 'AUTOMATION' branch above.
      return (
        <Link href={`/auto-pilot/auto-playbook/task/${item.event_target}?accountId=${item.account_id}`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </Link>
      );
    } else if (item.event_category == 'ACCOUNTS') {
      // RESOURCE_ACTION targets are cloud resource ids (e.g. EC2 instance ids)
      // which the kubernetes details page can't resolve — render as plain text.
      if (item.event_type == 'RESOURCE_ACTION') {
        return <Text placement='top' marginBottom='0px' value={targetName} showAutoEllipsis />;
      }
      return (
        <Link href={`/kubernetes/details/${item.event_target}`}>
          <Text sx={{ color: 'inherit' }} value={targetName} showAutoEllipsis />
        </Link>
      );
    } else if (item.event_category == 'GROUPS') {
      return (
        <Link href={`/user-management?groupId=${item.event_target}#groups`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </Link>
      );
    } else if (item.event_category == 'TICKET') {
      return (
        <Link href={`/tickets`}>
          <Text sx={{ color: 'inherit' }} value={item.event_target} showAutoEllipsis />
        </Link>
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
    } else if (item.event_type == 'INTEGRATION_CREATE') {
      // INTEGRATION_CREATE stores the config name in event_state — usually a
      // JSON-encoded string, but fall back to the raw value if it isn't JSON.
      let integrationName = '';
      try {
        const parsed = JSON.parse(item.event_state);
        integrationName = typeof parsed === 'string' ? parsed : parsed?.name || '';
      } catch (e) {
        console.error(e);
        integrationName = item.event_state || '';
      }
      return <Text value={`New Integration ${integrationName ? `With Name ${integrationName} ` : ''}Created`} showAutoEllipsis />;
    }
    const name = data.name || data.account_name || data.job_name || data.title || data.action_name || '';
    const value = `New ${capitalize(item.event_category)} ${name ? `With Name ${name} ` : ''} Created`;
    return <Text value={value} showAutoEllipsis />;
  }

  function getSummaryMessageForUpdate(item, accountName) {
    let data = {};
    // event_state is JSON for most event types, but some (e.g. RESOURCE_ACTION)
    // store a plain string like "STOPPED". Skip parsing in that case so the
    // per-event-type branches below can read event_attr instead.
    const stateRaw = (item.event_state || '').trim();
    if (stateRaw.startsWith('{') || stateRaw.startsWith('[')) {
      try {
        data = JSON.parse(stateRaw) || {};
      } catch (e) {
        console.error(e);
      }
    }

    if (item.event_type == 'RESOURCE_ACTION') {
      let attr = {};
      try {
        attr = JSON.parse(item.event_attr) || {};
      } catch (e) {
        console.error(e);
      }
      const verb = snakeToTitleCase(attr.command || item.event_action || 'update');
      const kind = attr.resource_type || 'resource';
      const id = attr.resource_id || item.event_target;
      const result = item.event_state || '';
      return <Text value={`${verb} ${kind} ${id}${result ? ` → ${result}` : ''}`} showAutoEllipsis />;
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
    } else if (item.event_type == 'INTEGRATION_UPDATE') {
      return <Text value={`Integration ${data.name ? `With Name ${data.name} ` : ''}Updated`} showAutoEllipsis />;
    }
    const updateName = data.name || data.account_name || data.job_name || data.action_name || '';
    return <Text value={`${snakeToTitleCase(item.event_category)} ${updateName ? `With Name ${updateName} ` : ''}Updated`} showAutoEllipsis />;
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
    if (item.event_type == 'INTEGRATION_DELETE') {
      // INTEGRATION_DELETE stores the config name in event_prev_state — usually a
      // JSON-encoded string, but fall back to the raw value if it isn't JSON.
      let integrationName = '';
      try {
        const parsed = JSON.parse(item.event_prev_state);
        integrationName = typeof parsed === 'string' ? parsed : parsed?.name || name || '';
      } catch (e) {
        console.error(e);
        integrationName = item.event_prev_state || name || '';
      }
      return <Text value={`Integration ${integrationName ? `With Name ${integrationName} ` : ''}Deleted`} showAutoEllipsis />;
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
                          color={'var(--ds-gray-500)'}
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            '& a': {
                              textDecoration: 'none',
                              '&:hover': {
                                color: 'var(--ds-blue-500)',
                                textDecoration: 'underline',
                              },
                            },
                          }}
                        >
                          {'cl: '}
                          <Link href={'/kubernetes/details/' + item.account_id}>
                            <Text showAutoEllipsis value={accountName} secondaryText />
                          </Link>
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
                    <Text
                      showAutoEllipsis
                      value={capitalize((item.event_category || '').replaceAll('_', ' ')) + '/' + snakeToTitleCase(item.event_type || '')}
                    />
                  ),
                },
                { component: <Text showAutoEllipsis value={capitalize(item.event_action)} /> },
                {
                  component: (
                    <Label
                      margin='auto'
                      text={capitalize(item.event_status)}
                      variant={(item.event_status || '').toLowerCase() === 'success' ? 'green' : 'red'}
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
    const dt = passedSelectedDateTime?.selection || passedSelectedDateTime;
    setDateRange({
      startDate: dt.startTime,
      endDate: dt.endTime,
    });
  }

  const statusOptions = ['SUCCESS', 'FAILURE'].map((v) => ({ value: v, label: capitalize(v) }));
  const categoryOptions = [...CategoryListing]
    .sort((a, b) => a.localeCompare(b))
    .map((v) => ({ value: v, label: capitalize(v.replaceAll('_', ' ')) }));
  const eventTypeOptions = [...EventListing]
    .sort((a, b) => a.localeCompare(b))
    .map((v) => ({ value: v, label: convertToReadableFormat(v.replaceAll('_', ' ')) }));
  const actionOptions = ['CREATE', 'UPDATE', 'DELETE', 'READ'].sort((a, b) => a.localeCompare(b)).map((v) => ({ value: v, label: capitalize(v) }));

  const findOption = (options, value) => (value ? options.find((o) => o.value === value) ?? null : null);

  return (
    <ListingLayout id='audit'>
      <ListingLayout.Toolbar
        actions={
          <>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{ startTime: dateRange.startDate, endTime: dateRange.endDate }}
              onChange={handleDateRangeChange}
              sx={{ height: '32px' }}
            />
            <DownloadButton onClick={() => ({ tableId: auditsTableId })} />
          </>
        }
      >
        <FilterDropdown
          id='audit-filter-status'
          label='Status'
          options={statusOptions}
          value={findOption(statusOptions, selectedStatus)}
          onSelect={(_e, item) => {
            setSelectedStatus(item?.value || '');
            setOffset(0);
          }}
        />
        <FilterDropdown
          id='audit-filter-category'
          label='Category'
          options={categoryOptions}
          value={findOption(categoryOptions, selectedCategory)}
          onSelect={(_e, item) => {
            setSelectedCategory(item?.value || '');
            setOffset(0);
          }}
        />
        <FilterDropdown
          id='audit-filter-event-type'
          label='Event Type'
          options={eventTypeOptions}
          value={findOption(eventTypeOptions, selectedCategoryType)}
          onSelect={(_e, item) => {
            setSelectedCategoryType(item?.value || '');
            setOffset(0);
          }}
        />
        <FilterDropdown
          id='audit-filter-action'
          label='Action'
          options={actionOptions}
          value={findOption(actionOptions, selectedAction)}
          onSelect={(_e, item) => {
            setSelectedAction(item?.value || '');
            setOffset(0);
          }}
        />
        <FilterDropdown
          id='audit-filter-user'
          label='User'
          options={userFilter}
          value={findOption(userFilter, selectedUser)}
          onSelect={(_e, item) => {
            setSelectedUser(item?.value || '');
            setOffset(0);
          }}
        />
        <FilterDropdown
          id='audit-filter-cluster'
          label='Cluster'
          options={accountFilter}
          value={findOption(accountFilter, selectedAccount)}
          onSelect={(_e, item) => {
            setSelectedAccount(item?.value || '');
            setOffset(0);
          }}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
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
      </ListingLayout.Body>
    </ListingLayout>
  );
};

AuditsTable.propTypes = {};
