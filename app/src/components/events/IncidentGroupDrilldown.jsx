import { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { Link } from '@ui/Link';
import { queryGraphQL } from '@lib/HttpService';
import KubernetesEventsTable from '@components/events/KubernetesEvents';

const RESOLVE_ALERT_GROUP = `
query resolve_alert_group($id: String!) {
  events: events_list(where: {id: {_eq: $id}}) {
    rows {
      id
      title
      starts_at
      incident_leader_id
      incident_member_count
    }
  }
}`;

// Triage Inbox drill-down (#34655): resolves the alert group the row's latest
// event belongs to — whether it is the group's leader or a child — and lists
// the grouped alerts under the leading event.
function IncidentGroupDrilldown({ eventId, accountId }) {
  const [loading, setLoading] = useState(true);
  const [leader, setLeader] = useState(null);

  useEffect(() => {
    let cancelled = false;
    setLeader(null);
    if (!eventId) {
      setLoading(false);
      return undefined;
    }
    setLoading(true);
    const resolve = async () => {
      const res = await queryGraphQL(RESOLVE_ALERT_GROUP, 'resolve_alert_group', { id: eventId });
      const row = res?.data?.data?.events?.rows?.[0];
      if (!row) return null;
      if (row.incident_member_count > 0) {
        return row; // this event leads its group
      }
      if (row.incident_leader_id) {
        const lres = await queryGraphQL(RESOLVE_ALERT_GROUP, 'resolve_alert_group', { id: row.incident_leader_id });
        return lres?.data?.data?.events?.rows?.[0] || null;
      }
      return null;
    };
    resolve()
      .then((l) => {
        if (!cancelled) setLeader(l);
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [eventId]);

  if (loading) {
    return <Typography sx={{ p: 'var(--ds-space-3)', fontSize: 'var(--ds-text-body)', color: ds.gray[500] }}>Loading alert group…</Typography>;
  }
  if (!leader) {
    return (
      <Box sx={{ p: 'var(--ds-space-3)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[600] }}>No related alerts were linked to this alert.</Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[500], mt: '2px' }}>
          Alerts group when the same subject, a direct caller/callee, or (soon) the same failure across several subjects raises alerts within a
          15-minute window. Frequently-firing alerts are treated as background noise and never grouped.
        </Typography>
      </Box>
    );
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', p: 'var(--ds-space-2) var(--ds-space-3) 0' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[600], whiteSpace: 'nowrap' }}>
          Leading event ({leader.incident_member_count} grouped):
        </Typography>
        <Link
          style={{ textDecoration: 'none', display: 'inline-flex', margin: 0, minWidth: 0 }}
          href={`/investigate?id=${leader.id}&accountId=${accountId}`}
          openInNew={true}
        >
          <Typography
            sx={{ fontSize: 'var(--ds-text-body)', color: ds.blue[600], overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
          >
            {leader.title}
          </Typography>
        </Link>
      </Box>
      <KubernetesEventsTable
        accountId={accountId}
        incidentLeaderId={leader.id}
        defaultQuery={{
          // Anchor the window to the group, not "now": members live within the
          // absorption cap of the leader, which may be days in the past.
          startTime: leader.starts_at ? new Date(leader.starts_at).getTime() - 60 * 60 * 1000 : undefined,
          endTime: leader.starts_at ? new Date(leader.starts_at).getTime() + 2 * 60 * 60 * 1000 : undefined,
        }}
        enableFilters={false}
        showTimeFilter={false}
        hideScopeFilters
        tableColumns={[
          { name: 'Severity', width: '10%' },
          { name: 'Message', width: '42%' },
          { name: 'Alert Status', width: '10%' },
          { name: 'Error Type', width: '16%' },
          { name: 'Source', width: '12%' },
          '',
        ]}
      />
    </Box>
  );
}

IncidentGroupDrilldown.propTypes = {
  eventId: PropTypes.string,
  accountId: PropTypes.string,
};

export default IncidentGroupDrilldown;
