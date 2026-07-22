import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Chip } from '@ui/Chip';
import apiCloudAccount from '@api1/cloud-account';
import apiUser from '@api1/user';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';

interface ResourceActionHistoryProps {
  accountId: string | undefined;
  resourceId: string;
}

interface AuditEvent {
  user_id: string;
  event_time: string;
  event_status: string;
  event_state: string;
  event_target: string;
  event_attr: any;
}

interface UserSummary {
  id: string;
  display_name?: string | null;
  username?: string | null;
}

const HEADERS = [
  { name: 'Time', width: '18%' },
  { name: 'Command', width: '18%', truncate: 'ellipsis' },
  { name: 'Status', width: '10%' },
  { name: 'State', width: '12%' },
  { name: 'User', width: '14%', truncate: 'ellipsis' },
  { name: 'Message', width: '28%', truncate: 'ellipsis' },
];

const parseAttr = (attr: any): any => {
  if (typeof attr === 'string') {
    try {
      return JSON.parse(attr);
    } catch {
      return {};
    }
  }
  return attr || {};
};

const ResourceActionHistory: React.FC<ResourceActionHistoryProps> = ({ accountId, resourceId }) => {
  const [audits, setAudits] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [count, setCount] = useState(0);
  const [userMap, setUserMap] = useState<Record<string, UserSummary>>({});
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(10);

  const resourceKey = `${accountId ?? ''}|${resourceId}`;
  const [prevResourceKey, setPrevResourceKey] = useState(resourceKey);
  if (prevResourceKey !== resourceKey) {
    setPrevResourceKey(resourceKey);
    setOffset(0);
  }

  useEffect(() => {
    if (!accountId || !resourceId) return;
    setLoading(true);
    apiCloudAccount
      .listResourceActionHistory(accountId, resourceId, limit, offset)
      .then((result) => {
        setAudits(result.audits);
        setCount(result.count);
      })
      .finally(() => setLoading(false));
  }, [accountId, resourceId, limit, offset]);

  useEffect(() => {
    apiUser
      .listUsers({ limit: 1000 })
      .then((response: any) => {
        const rows: UserSummary[] = response?.data || [];
        const map: Record<string, UserSummary> = {};
        for (const u of rows) {
          if (u?.id) {
            map[u.id] = u;
          }
        }
        setUserMap(map);
      })
      .catch(() => {
        // Lookup failure is non-fatal — table falls back to short UUID.
      });
  }, []);

  const formatUser = useCallback(
    (userId: string | null | undefined): { text: string; tooltip: string } => {
      if (!userId) {
        return { text: '-', tooltip: '' };
      }
      const user = userMap[userId];
      if (!user) {
        const shortId = userId.length > 8 ? `${userId.slice(0, 8)}…` : userId;
        return { text: shortId, tooltip: userId };
      }
      const display = user.display_name || user.username || userId;
      const subtitle = user.display_name && user.username ? user.username : userId;
      return { text: display, tooltip: subtitle };
    },
    [userMap]
  );

  const tableData = useMemo(() => {
    return audits.map((audit) => {
      const attr = parseAttr(audit.event_attr);
      const command = attr.command || '-';
      const message = attr.error_message || '-';
      const user = formatUser(audit.user_id);
      const status = audit.event_status;
      const state = audit.event_state || '-';

      return [
        { component: <Datetime value={audit.event_time} />, text: audit.event_time, data: audit.event_time },
        { text: command, data: command, tooltipText: command },
        {
          component: (
            <Chip variant='status' size='xs' tone={status === 'SUCCESS' ? 'success' : 'critical'}>
              {status}
            </Chip>
          ),
          text: status,
          data: status,
          align: 'center',
        },
        { text: state, data: state },
        { text: user.text, data: user.text, tooltipText: user.tooltip || user.text },
        { text: message, data: message, tooltipText: message },
      ];
    });
  }, [audits, formatUser]);

  return (
    <CustomTable
      id={`resource-action-history-${resourceId}`}
      headers={HEADERS}
      tableData={tableData}
      loading={loading}
      rowsPerPage={limit}
      totalRows={count}
      pageNumber={Math.floor(offset / limit) + 1}
      onPageChange={(page: number, newLimit: number) => {
        setOffset((page - 1) * newLimit);
        setLimit(newLimit);
      }}
      showUpdatedTable
      showEmptyStateText
      emptyStateText='No action history found for this resource.'
      tableHeadingCenter={['Status']}
    />
  );
};

export default ResourceActionHistory;
