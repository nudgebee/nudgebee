import React, { useEffect, useMemo, useState } from 'react';
import { Box, Chip, Typography } from '@mui/material';
import apiCloudAccount from '@api1/cloud-account';
import apiUser from '@api1/user';
import Loader from '@shared/Loader';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import { ds } from '@utils/colors';
import WidgetCard from '@ui/WidgetCard';
import CustomTable2 from '@shared/tables/CustomTable2';
import { usePagination } from '@hooks/usePagination';
import CommandExecutionDetail, { CommandEntry } from './CommandExecutionDetail';

interface CommandExecutionHistoryProps {
  accountId: string;
  recommendationId: string;
  resolutionId?: string;
}

interface AuditRow {
  user_id: string;
  account_id: string;
  event_time: string;
  event_status: string;
  event_state: string;
  event_target: string;
  event_attr: string;
}

interface UserSummary {
  id: string;
  display_name?: string | null;
  username?: string | null;
}

const TABLE_HEADERS = [
  { name: 'Time', width: '18%' },
  { name: 'Command', width: '42%' },
  { name: 'Status', width: '15%' },
  { name: 'User', width: '25%' },
];

interface ParsedAttr {
  commands?: CommandEntry[];
  [key: string]: unknown;
}

function parseAttr(raw: string | null | undefined): ParsedAttr {
  if (!raw) return {};
  try {
    return JSON.parse(raw) || {};
  } catch {
    return {};
  }
}

const CommandDetail = (_option: any, drilldownQuery: any) => {
  const attr: ParsedAttr = drilldownQuery?.attr || {};
  const commands = Array.isArray(attr.commands) ? attr.commands : [];

  return (
    <WidgetCard sx={{ mt: 0 }}>
      <CommandExecutionDetail commands={commands} status={drilldownQuery?.status} />
    </WidgetCard>
  );
};

function buildUserMap(users: UserSummary[]): Record<string, UserSummary> {
  const map: Record<string, UserSummary> = {};
  for (const u of users) {
    if (u?.id) map[u.id] = u;
  }
  return map;
}

function buildRows(audits: AuditRow[], userMap: Record<string, UserSummary>): any[][] {
  return audits.map((row) => {
    const attr = parseAttr(row.event_attr);
    const commands: CommandEntry[] = Array.isArray(attr.commands) ? attr.commands : [];
    const firstCommand = commands[0]?.command || '-';
    const commandPreview = commands.length > 1 ? `${commands.length} commands` : firstCommand;
    const isSuccess = row.event_status?.toUpperCase() === 'SUCCESS';
    const user = userMap[row.user_id];
    const displayUser = user?.display_name || user?.username || row.user_id || '-';

    return [
      {
        component: <Datetime value={row.event_time} />,
        drilldownQuery: { attr, status: row.event_status },
      },
      {
        component: (
          <Box
            sx={{
              fontFamily: 'var(--ds-font-mono)',
              fontSize: ds.text.small,
              wordBreak: 'break-all',
              display: '-webkit-box',
              WebkitLineClamp: 2,
              WebkitBoxOrient: 'vertical',
              overflow: 'hidden',
            }}
          >
            {commandPreview}
          </Box>
        ),
      },
      {
        component: (
          <Chip
            label={isSuccess ? 'Success' : 'Failed'}
            size='small'
            sx={{
              fontSize: ds.text.caption,
              height: ds.space.mul(0, 10),
              backgroundColor: isSuccess ? ds.green[100] : ds.red[100],
              color: isSuccess ? ds.green[700] : ds.red[700],
            }}
          />
        ),
      },
      {
        component: <Text value={displayUser} showAutoEllipsis />,
      },
    ];
  });
}

const CommandExecutionHistory: React.FC<CommandExecutionHistoryProps> = ({ accountId, recommendationId, resolutionId }) => {
  const [audits, setAudits] = useState<AuditRow[]>([]);
  const [userMap, setUserMap] = useState<Record<string, UserSummary>>({});
  const [totalRows, setTotalRows] = useState(0);
  const [loading, setLoading] = useState(false);
  const { page, rowsPerPage, changePage } = usePagination(5);

  // Fetch users once on mount — not re-fetched on pagination changes.
  useEffect(() => {
    apiUser
      .listUsers({ limit: 1000 })
      .then((r: any) => setUserMap(buildUserMap(r?.data || [])))
      .catch(() => {});
  }, []);

  // Fetch history whenever page/filters change.
  useEffect(() => {
    if (!accountId || !recommendationId) {
      setAudits([]);
      setTotalRows(0);
      return;
    }

    let active = true;
    setLoading(true);

    apiCloudAccount
      .listCommandExecutionHistory(accountId, recommendationId, resolutionId, rowsPerPage, page * rowsPerPage)
      .then((result: { audits: AuditRow[]; count: number }) => {
        if (!active) return;
        setAudits(result.audits);
        setTotalRows(result.count);
      })
      .catch(() => {
        if (!active) return;
        setAudits([]);
        setTotalRows(0);
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [accountId, recommendationId, resolutionId, page, rowsPerPage]);

  // Re-derive rows whenever audits or userMap change.
  const tableData = useMemo(() => buildRows(audits, userMap), [audits, userMap]);

  if (loading) {
    return (
      <Box display='flex' justifyContent='center' alignItems='center' height={ds.space.mul(2, 25)}>
        <Loader />
      </Box>
    );
  }

  if (audits.length === 0) {
    return (
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          py: ds.space[3],
          borderRadius: ds.radius.lg,
          border: `1px solid ${ds.gray[200]}`,
          backgroundColor: ds.background[100],
        }}
      >
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[600] }}>No command executions recorded for this recommendation.</Typography>
      </Box>
    );
  }

  return (
    <CustomTable2
      id={`command-execution-history-${recommendationId}`}
      headers={TABLE_HEADERS}
      tableData={tableData}
      rowsPerPage={rowsPerPage}
      onPageChange={changePage}
      totalRows={totalRows}
      showExpandable={true}
      expandable={{
        tabs: [{ text: 'Details', componentFn: CommandDetail }],
      }}
      loading={loading}
      pageNumber={page + 1}
    />
  );
};

export default CommandExecutionHistory;
