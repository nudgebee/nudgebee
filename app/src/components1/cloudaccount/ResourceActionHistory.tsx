import React, { useEffect, useState } from 'react';
import { Box, Tooltip, Typography, Chip } from '@mui/material';
import apiCloudAccount from '@api1/cloud-account';
import apiUser from '@api1/user';
import Loader from '@common/Loader';
import Datetime from '@components1/common/format/Datetime';

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

const ResourceActionHistory: React.FC<ResourceActionHistoryProps> = ({ accountId, resourceId }) => {
  const [audits, setAudits] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [count, setCount] = useState(0);
  const [userMap, setUserMap] = useState<Record<string, UserSummary>>({});

  useEffect(() => {
    if (!accountId || !resourceId) return;
    setLoading(true);
    apiCloudAccount
      .listResourceActionHistory(accountId, resourceId, 20, 0)
      .then((result) => {
        setAudits(result.audits);
        setCount(result.count);
      })
      .finally(() => setLoading(false));
  }, [accountId, resourceId]);

  // Fetch users once and build an id->user map so the User column shows
  // a friendly name/email instead of the raw UUID. apiUser.listUsers caches
  // for an hour, so this is cheap on revisits.
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

  const formatUser = (userId: string | null | undefined): React.ReactNode => {
    if (!userId) {
      return '-';
    }
    const user = userMap[userId];
    if (!user) {
      // Unknown user — show short id rather than the full UUID.
      const shortId = userId.length > 8 ? `${userId.slice(0, 8)}…` : userId;
      return (
        <Tooltip title={userId} placement='top'>
          <span>{shortId}</span>
        </Tooltip>
      );
    }
    const display = user.display_name || user.username || userId;
    const subtitle = user.display_name && user.username ? user.username : userId;
    return (
      <Tooltip title={subtitle} placement='top'>
        <span>{display}</span>
      </Tooltip>
    );
  };

  if (loading) {
    return (
      <Box display='flex' justifyContent='center' py={4}>
        <Loader />
      </Box>
    );
  }

  if (audits.length === 0) {
    return (
      <Box py={3} textAlign='center'>
        <Typography color='#737373' fontSize='13px'>
          No action history found for this resource.
        </Typography>
      </Box>
    );
  }

  const parseAttr = (attr: any) => {
    if (typeof attr === 'string') {
      try {
        return JSON.parse(attr);
      } catch {
        return {};
      }
    }
    return attr || {};
  };

  return (
    <Box>
      <Typography fontSize='12px' color='#737373' mb={1}>
        {count} action{count !== 1 ? 's' : ''} recorded
      </Typography>
      <Box component='table' sx={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px' }}>
        <Box component='thead'>
          <Box
            component='tr'
            sx={{
              borderBottom: '1px solid #e5e7eb',
              '& th': { py: 1, px: 1.5, textAlign: 'left', fontWeight: 600, fontSize: '12px', color: '#737373' },
            }}
          >
            <Box component='th'>Time</Box>
            <Box component='th'>Command</Box>
            <Box component='th'>Status</Box>
            <Box component='th'>State</Box>
            <Box component='th'>User</Box>
            <Box component='th'>Message</Box>
          </Box>
        </Box>
        <Box component='tbody'>
          {audits.map((audit, index) => {
            const attr = parseAttr(audit.event_attr);
            return (
              <Box key={index} component='tr' sx={{ borderBottom: '1px solid #f3f4f6', '& td': { py: 1, px: 1.5, fontSize: '13px' } }}>
                <Box component='td'>
                  <Datetime value={audit.event_time} />
                </Box>
                <Box component='td'>{attr.command || '-'}</Box>
                <Box component='td'>
                  <Chip
                    label={audit.event_status}
                    size='small'
                    sx={{
                      fontSize: '11px',
                      height: '20px',
                      backgroundColor: audit.event_status === 'SUCCESS' ? '#dcfce7' : '#fee2e2',
                      color: audit.event_status === 'SUCCESS' ? '#166534' : '#991b1b',
                    }}
                  />
                </Box>
                <Box component='td'>{audit.event_state || '-'}</Box>
                <Box component='td' sx={{ maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {formatUser(audit.user_id)}
                </Box>
                <Box component='td' sx={{ maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {attr.error_message || '-'}
                </Box>
              </Box>
            );
          })}
        </Box>
      </Box>
    </Box>
  );
};

export default ResourceActionHistory;
