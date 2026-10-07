import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import apiAudits from '@api1/audits';
import apiUser from '@api1/user';
import Datetime, { formatClockCompact } from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import { Label } from '@ui/Label';
import { ds } from '@utils/colors';
import { formatAuditActor, formatCurrency, formatCount } from 'src/utils/common';

const BUDGET_TYPE_LABELS = {
  monthly_cost: 'Monthly Cost',
  daily_cost: 'Daily Cost',
  monthly_count: 'Monthly Count',
  daily_count: 'Daily Count',
  bypass: 'Disable All Budget Checks',
};

const ACTION_TONE = {
  CREATE: 'success',
  ENABLE: 'success',
  DELETE: 'critical',
  DISABLE: 'critical',
  INCREASE: 'warning',
  DECREASE: 'warning',
};

const COUNT_TYPES = new Set(['monthly_count', 'daily_count']);

// Widths sum to 100%; labels never wrap so a header stays on one line even when
// its column is narrow (the cell content truncates instead).
const COLUMNS = [
  { name: 'User', width: '24%' },
  { name: 'Timestamp', width: '13%' },
  { name: 'Module', width: '15%' },
  { name: 'Budget Type', width: '14%' },
  { name: 'Previous Value', width: '12%' },
  { name: 'Updated Value', width: '12%' },
  { name: 'Action', width: '10%' },
];

const headers = COLUMNS.map(({ name, width }) => ({
  name,
  width,
  component: (
    <Box component='span' sx={{ whiteSpace: 'nowrap' }}>
      {name}
    </Box>
  ),
}));

function parseJson(raw) {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (_e) {
    return null;
  }
}

// formatBudgetValue renders one side (previous or updated) of a history row.
// Prev is empty on Create, updated is `{deleted: true}` on Delete (the audit
// pipeline's required-field placeholder) — both render as "—".
function formatBudgetValue(rawJsonString, budgetType) {
  const parsed = parseJson(rawJsonString);
  if (!parsed || parsed.deleted) return '—';
  if (budgetType === 'bypass') return parsed.enabled ? 'Enabled' : 'Disabled';
  if (parsed.limit == null) return '—';
  const formatted = COUNT_TYPES.has(budgetType) ? formatCount(parsed.limit) : formatCurrency(parsed.limit);
  return parsed.enabled === false ? `${formatted} (disabled)` : formatted;
}

// BudgetHistoryTable lists budget config change history (create/update/
// delete/enable-disable), scoped to the tenant or one account. Reuses the
// existing audits_v2-backed apiAudits.listAudits, filtered to category
// 'BUDGET' — no dedicated backend endpoint exists or is needed for this.
// Stable default: moduleLabels is a fetch-effect dependency, so an inline `{}` would refetch every render.
const NO_MODULE_LABELS = {};

const BudgetHistoryTable = ({ accountId, isTenantWide, moduleLabels = NO_MODULE_LABELS }) => {
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [totalRows, setTotalRows] = useState(0);
  const [loading, setLoading] = useState(true);
  const [usersMap, setUsersMap] = useState({});
  const [usersLoaded, setUsersLoaded] = useState(false);

  useEffect(() => {
    apiAudits
      .lisUsersAndAccounts()
      .then((res) => {
        const map = {};
        (res?.data?.users?.rows || []).forEach((u) => {
          map[u.id] = u.username;
        });
        map['00000000-0000-0000-0000-000000000000'] = 'SYSTEM';
        setUsersMap(map);
        setUsersLoaded(true);
      })
      .catch(() => setUsersLoaded(true));
  }, []);

  useEffect(() => {
    if (!usersLoaded) return;
    setLoading(true);
    apiAudits
      .listAudits(limit, offset, { category: 'BUDGET', accountId: isTenantWide ? undefined : accountId })
      .then((res) => {
        setLoading(false);
        const rows = (res.data?.audits || []).map((item) => {
          const attr = parseJson(item.event_attr) || {};
          const budgetType = attr.budget_type || '';
          const moduleName = attr.module || '';
          const action = (item.event_type || '').replace('BUDGET_CONFIG_', '');
          const userName = usersMap[item.user_id] || item.user_id || formatAuditActor(item.event_actor);
          return [
            { component: <Text value={userName} showAutoEllipsis /> },
            {
              component: (
                <Box>
                  <Datetime value={item.event_time} baseDate={new Date()} />
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                    {formatClockCompact(item.event_time)}
                  </Typography>
                </Box>
              ),
            },
            { component: <Text value={moduleLabels[moduleName] || moduleName} showAutoEllipsis /> },
            { component: <Text value={BUDGET_TYPE_LABELS[budgetType] || budgetType} showAutoEllipsis /> },
            { component: <Text value={formatBudgetValue(item.event_prev_state, budgetType)} showAutoEllipsis /> },
            { component: <Text value={formatBudgetValue(item.event_state, budgetType)} showAutoEllipsis /> },
            { component: <Label margin='auto' text={action} tone={ACTION_TONE[action] || 'neutral'} /> },
          ];
        });
        setData(rows);
        setTotalRows(res.data?.count || 0);
      })
      .catch(() => setLoading(false));
  }, [offset, limit, accountId, isTenantWide, usersLoaded, usersMap, moduleLabels]);

  return (
    <Box sx={{ mt: ds.space[6] }}>
      <Typography
        sx={{
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: 'var(--ds-gray-500)',
          textTransform: 'uppercase',
          letterSpacing: 0.4,
          mb: ds.space[4],
        }}
      >
        History
      </Typography>
      <CustomTable
        id='budget-history-table'
        headers={headers}
        tableData={data}
        rowsPerPage={limit}
        onPageChange={(page, newLimit) => {
          setOffset((page - 1) * newLimit);
          setLimit(newLimit);
        }}
        totalRows={totalRows}
        loading={loading}
        textAlign='left'
        pageNumber={offset / limit + 1}
      />
    </Box>
  );
};

BudgetHistoryTable.propTypes = {
  accountId: PropTypes.string,
  isTenantWide: PropTypes.bool,
  moduleLabels: PropTypes.objectOf(PropTypes.string),
};

export default BudgetHistoryTable;
