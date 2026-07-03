import React, { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import ListingLayout from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { Label } from '@ui/Label';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiUsers from '@api1/user';
import CustomTable from '@shared/tables/CustomTable';
import { Link } from '@ui/Link';
import { DEFAULT_TITLE } from '@hooks/useTenantBranding';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import Text from '@shared/format/Text';
import dayjs from 'dayjs';
import ScopeChip from '@components/llm/ScopeChip';

const DEFAULT_DATE_RANGE_MS = 7 * 24 * 60 * 60 * 1000;

const HEADERS_USER_FEEDBACK = [
  { name: 'Module', width: '15%' },
  { name: 'Useful', width: '5%' },
  { name: 'Question', width: '30%' },
  { name: 'AdditionalDetails', width: '40%' },
  { name: 'User', width: '10%' },
];

// Tenant-wide variant adds an Account column so cross-account feedback
// rows are distinguishable when the Settings modal is opened from the
// global sidebar (no accountId in scope).
const HEADERS_USER_FEEDBACK_TENANT = [
  { name: 'Module', width: '15%' },
  { name: 'Useful', width: '5%' },
  { name: 'Question', width: '25%' },
  { name: 'AdditionalDetails', width: '35%' },
  { name: 'User', width: '10%' },
  { name: 'Account', width: '10%' },
];

const MODULE_NAMES = {
  investigate: 'Troubleshoot',
  loki: 'Loki Query',
  prometheus: 'Prometheus Query',
  es: 'ElasticSearch Query',
  'new-investigation': `Ask ${DEFAULT_TITLE}`,
};

const MODULES = ['investigate', 'loki', 'prometheus', 'es', 'new-investigation'].map((item) => ({
  label: MODULE_NAMES[item] ?? item,
  value: item,
}));

const UserFeedbackTab = ({ accountId, stickyTable = false }) => {
  // Tenant-wide read: when the Settings modal is opened from the global
  // sidebar there's no current account. Skip the cloud_account_id filter
  // and let api-server's query service apply its standard row-level
  // account ACL (tenant/super admins see all in tenant; account-admins
  // see only their assigned accounts). Render an Account column so
  // cross-account rows are distinguishable.
  const isTenantWide = !accountId;
  const [feedbackRows, setFeedbackRows] = useState([]);
  const [userIdNameMap, setUserIdNameMap] = useState({});
  const [selectedModule, setSelectedModule] = useState('');
  const [selectedUseful, setSelectedUseful] = useState('');
  const [resetPageKey, setResetPageKey] = useState('');
  const usefulness = [
    { label: 'Yes', value: 'true' },
    { label: 'No', value: 'false' },
  ];
  const [loading, setLoading] = useState(false);
  const [dateRange, setDateRange] = useState(() => ({
    startTime: Date.now() - DEFAULT_DATE_RANGE_MS,
    endTime: Date.now(),
  }));

  useEffect(() => {
    // The user-id → username map is tenant-scoped (apiUsers.listUsers
    // reads from the session's tenant), so it's safe to fetch in both
    // modes.
    apiUsers
      .listUsers({ status: 'active' })
      .then((res) => {
        const map = {};
        res?.data?.forEach((item) => {
          map[item.id] = item.username;
        });
        setUserIdNameMap(map);
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    // Drop cloud_account_id from the query in tenant-wide mode — the
    // listAiFeedback wrapper only includes it when truthy, and the
    // api-server query service then applies its standard row-level
    // account ACL automatically.
    const query = { start_time: dateRange.startTime, end_time: dateRange.endTime };
    if (!isTenantWide) query.cloud_account_id = accountId;
    if (selectedModule) query.module = selectedModule;
    if (selectedUseful) query.useful = selectedUseful === 'true';
    setLoading(true);
    apiAskNudgebee
      .listAiFeedback(query)
      .then((res) => {
        setFeedbackRows(res?.data?.data?.llm_conversation_feedback?.rows || []);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [selectedModule, selectedUseful, accountId, dateRange, isTenantWide]);

  const tableData = useMemo(
    () =>
      feedbackRows.map((item) => {
        let moduleHref = '';
        if (item.module === 'investigate') {
          moduleHref = `/investigate?id=${item.conversation_id}&accountId=${item.cloud_account_id}&autoInvestigate=true`;
        } else if (item.module === 'new-investigation') {
          moduleHref = `/ask-nudgebee?accountId=${item.cloud_account_id}&session_id=${item.conversation_id}`;
        }
        return [
          {
            text: moduleHref ? (
              <Link href={moduleHref} openInNew>
                {MODULE_NAMES[item.module] ?? item.module}
              </Link>
            ) : (
              item.module
            ),
          },
          {
            component: <Label text={item.useful === true ? 'Yes' : 'No'} variant={item.useful === true ? 'green' : 'red'} />,
          },
          { component: <Text value={item.question} showAutoEllipsis lineClamp={3} /> },
          {
            component: (
              <Box>
                {item.additional_notes && <Text value={item.additional_notes} showAutoEllipsis lineClamp={2} />}
                {item.llm_response ? (
                  <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5, mt: item.additional_notes ? 0.5 : 0 }}>
                    <Box component='span' sx={{ fontWeight: 'bold', flexShrink: 0 }}>
                      Answer:
                    </Box>
                    <Text value={item.llm_response} showAutoEllipsis lineClamp={3} />
                  </Box>
                ) : null}
                {!item.additional_notes && !item.llm_response && '-'}
              </Box>
            ),
          },
          { text: userIdNameMap[item.user_id] ?? item.user_id },
          // ScopeChip resolves the raw UUID to the cached account-name
          // so the column shows "k8s-dev-oss (aws)" instead of a UUID.
          ...(isTenantWide ? [{ component: item.cloud_account_id ? <ScopeChip accountId={item.cloud_account_id} /> : <Text value='—' /> }] : []),
        ];
      }),
    [feedbackRows, userIdNameMap, isTenantWide]
  );

  return (
    <ListingLayout id='user-feedback-tab'>
      <ListingLayout.Toolbar
        title={
          <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 1 }}>
            User Feedback
            <ScopeChip accountId={accountId} />
          </Box>
        }
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={dateRange}
            onChange={({ selection }) => {
              setDateRange({ startTime: selection.startTime, endTime: selection.endTime });
              setResetPageKey(`dateTime-[${selection.startTime}-${selection.endTime}]`);
            }}
            minDate={dayjs().subtract(6, 'month')}
          />
        }
      >
        <FilterDropdown
          label='Module'
          options={MODULES}
          value={selectedModule}
          onSelect={(e) => {
            setSelectedModule(e?.target?.value);
            setResetPageKey(`module-${e?.target?.value}`);
          }}
          size='sm'
        />
        <FilterDropdown
          label='Useful'
          options={usefulness}
          value={selectedUseful}
          onSelect={(e) => {
            setSelectedUseful(e?.target?.value);
            setResetPageKey(`useful-${e?.target?.value}`);
          }}
          size='sm'
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <CustomTable
          headers={isTenantWide ? HEADERS_USER_FEEDBACK_TENANT : HEADERS_USER_FEEDBACK}
          tableData={tableData}
          loading={loading}
          resetPage={resetPageKey}
          stickyHeader={stickyTable}
          sx={stickyTable ? { maxHeight: 'calc(90vh - 300px)', overflowY: 'auto' } : undefined}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

UserFeedbackTab.propTypes = {
  // Optional: empty / unset means tenant-wide read (Settings opened from
  // the global sidebar).
  accountId: PropTypes.string,
  stickyTable: PropTypes.bool,
};

export default UserFeedbackTab;
