import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import apiKubernetes from '@api1/kubernetes';
import { parseDateValue } from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import SafeIcon from '@shared/icons/SafeIcon';
import Tooltip from '@ui/Tooltip';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { ds } from '@utils/colors';
import { infoIcon } from '@assets';
import AlertStatusBadge from '@shared/widgets/AlertStatusBadge';
import { describeEventState, describeOccurrence } from './eventState';

// Same grid as the sidebar's other rows, so the key lines up with its value.
// minmax(0, 1fr): a plain 1fr track grows to its content and pushed the whole
// panel into horizontal scroll.
const rowSx = { display: 'grid', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} minmax(0, 1fr)`, columnGap: ds.space[2] };
const valueSx = { display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0, minHeight: ds.space.mul(0, 9) };
const detailSx = { color: ds.gray[700], whiteSpace: 'nowrap' };

// Anything created within this long after the event is the event itself: created_at carries
// microseconds, the filter only milliseconds.
const SELF_CREATED_AT_SLACK_MS = 1000;
// How far back to look for earlier firings of the same fingerprint. The row says
// "since <date>", so a bounded window states what it counted rather than implying
// all of history.
const HISTORY_WINDOW_MS = 30 * 24 * 60 * 60 * 1000;
// starts_at is grouped on; the query engine truncates a grouped datetime to the day
// unless told otherwise, which made every same-day firing tie (wrong first/latest
// firing) and read "Last one" as midnight.
const STARTS_AT_TO_THE_SECOND = [{ name: 'starts_at', expr: 'date_unit', args: ['second'] }];

// The badge takes over for FIRING/CLOSED — the two statuses the Close/Open
// toggle actually knows how to act on — so "State" doubles as the Alert
// Status control instead of sitting next to a second, redundant row showing
// the same value. RESOLVED (and anything else) falls back to the old
// read-only Label: AlertStatusBadge has no dropdown transitions for it, and
// showing an arrow that does nothing would be worse than no arrow.
const StateLine = ({
  state,
  testId,
  eventId,
  rawStatus,
  canToggle,
  closedByName,
  closedReasonCode,
  closedReasonText,
  closedEvidenceUrl,
  onToggle,
}) => (
  <Box sx={valueSx} data-testid={testId}>
    {rawStatus === 'FIRING' || rawStatus === 'CLOSED' ? (
      <AlertStatusBadge
        eventId={eventId}
        status={rawStatus}
        canToggle={canToggle}
        closedByName={closedByName}
        closedReasonCode={closedReasonCode}
        closedReasonText={closedReasonText}
        closedEvidenceUrl={closedEvidenceUrl}
        onToggle={onToggle}
      />
    ) : (
      <Label tone={state.tone} text={state.label} margin='0' />
    )}
    {state.detail && <Text value={state.detail} secondaryText sx={detailSx} />}
    {state.hint && (
      <Tooltip title={state.hint} arrow placement='bottom'>
        {/* SafeIcon can't hold a ref, and without one the tooltip never opens. */}
        <Box component='span' sx={{ display: 'inline-flex' }} data-testid='event-lifecycle-state-hint'>
          <SafeIcon src={infoIcon} alt='info' style={{ width: '13px', height: '13px', cursor: 'pointer', filter: 'opacity(0.5)' }} />
        </Box>
      </Tooltip>
    )}
  </Box>
);

StateLine.propTypes = {
  state: PropTypes.object.isRequired,
  testId: PropTypes.string,
  eventId: PropTypes.string,
  rawStatus: PropTypes.string,
  canToggle: PropTypes.bool,
  closedByName: PropTypes.string,
  closedReasonCode: PropTypes.string,
  closedReasonText: PropTypes.string,
  closedEvidenceUrl: PropTypes.string,
  onToggle: PropTypes.func,
};

/**
 * Where this event sits in the run of firings of the same problem: same
 * fingerprint over the window before it (including it) and everything after.
 * null until known, and null when this is the only firing.
 */
export const useEventOccurrence = (row) => {
  const [history, setHistory] = useState(null);
  const accountId = row?.cloud_account_id;
  const isDemo = accountId === 'demo';
  const createdMs = row?.created_at ? parseDateValue(row.created_at).getTime() : NaN;

  useEffect(() => {
    setHistory(null);
    // Demo groupings are canned mock data; counting them would invent recurrences.
    if (isDemo || !accountId || !row?.fingerprint || !Number.isFinite(createdMs)) return undefined;
    let cancelled = false;
    const columns = ['id', 'status', 'starts_at'];
    const groupings = (startDate, endDate, order) =>
      apiKubernetes.getK8sEventGroupings(
        1,
        0,
        { account_id: accountId, fingerprint: row.fingerprint, start_date: startDate, end_date: endDate },
        columns,
        columns,
        { name: 'starts_at', order },
        STARTS_AT_TO_THE_SECOND
      );
    Promise.all([
      groupings(new Date(createdMs - HISTORY_WINDOW_MS), new Date(createdMs + SELF_CREATED_AT_SLACK_MS), 'asc'),
      // +1ms: the filter is inclusive at both ends, so without it an event created
      // exactly on the boundary would be counted in both windows.
      groupings(new Date(createdMs + SELF_CREATED_AT_SLACK_MS + 1), new Date(), 'desc'),
    ])
      .then(([earlier, later]) => {
        if (cancelled) return;
        const count = (res) => Number(res?.data?.event_groupings_aggregate?.aggregate?.count) || 0;
        setHistory({
          before: count(earlier),
          after: count(later),
          first: earlier?.data?.event_groupings?.[0],
          latest: later?.data?.event_groupings?.[0],
        });
      })
      // A failed lookup must not put a claim on screen, but it must not vanish either.
      .catch((err) => console.error('Failed to look up occurrences for fingerprint', row?.fingerprint, err));
    return () => {
      cancelled = true;
    };
  }, [accountId, isDemo, row?.fingerprint, createdMs]);

  return history ? describeOccurrence(history) : null;
};

/** "State" row: is this still going on, or has it ended. */
export const EventStateRows = ({ row, canToggleStatus, closedByName, closedReasonCode, closedReasonText, closedEvidenceUrl, onToggleStatus }) => {
  const state = describeEventState({ status: row?.status, startsAt: row?.starts_at, endsAt: row?.ends_at });
  if (!state) return null;
  return (
    <Box sx={rowSx}>
      <Text value={'State'} secondaryText />
      <StateLine
        state={state}
        testId='event-lifecycle-state'
        eventId={row?.id}
        rawStatus={row?.status}
        canToggle={canToggleStatus}
        closedByName={closedByName}
        closedReasonCode={closedReasonCode}
        closedReasonText={closedReasonText}
        closedEvidenceUrl={closedEvidenceUrl}
        onToggle={onToggleStatus}
      />
    </Box>
  );
};

const eventLink = (id, accountId, text) => (
  <Link
    style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
    href={`/investigate?id=${id}&accountId=${accountId}`}
    openInNew={true}
  >
    <Text value={text} secondaryText sx={{ color: ds.blue[600] }} />
  </Link>
);

/** "Firings" and "Last one" rows for the How often section. Plain text, no chip. */
export const EventOccurrenceRows = ({ occurrence, accountId }) => {
  if (!occurrence) return null;
  return (
    <>
      <Box sx={rowSx}>
        <Text value={'Firings'} secondaryText />
        <Box sx={{ ...valueSx, flexWrap: 'wrap', rowGap: ds.space[1] }} data-testid='event-occurrence'>
          <Text value={occurrence.text} secondaryText sx={{ color: ds.gray[700], fontWeight: ds.weight.semibold }} />
          {occurrence.firstId && eventLink(occurrence.firstId, accountId, 'first')}
        </Box>
      </Box>
      {occurrence.lastSeen && (
        <Box sx={rowSx}>
          <Text value={'Last one'} secondaryText />
          <Box sx={{ ...valueSx, flexWrap: 'wrap', rowGap: ds.space[1] }} data-testid='event-occurrence-latest'>
            <Text value={occurrence.lastSeen} secondaryText sx={{ color: ds.gray[700] }} />
            {occurrence.latestId && eventLink(occurrence.latestId, accountId, 'open')}
          </Box>
        </Box>
      )}
    </>
  );
};

EventOccurrenceRows.propTypes = {
  occurrence: PropTypes.object,
  accountId: PropTypes.string,
};

EventStateRows.propTypes = {
  row: PropTypes.object,
  canToggleStatus: PropTypes.bool,
  closedByName: PropTypes.string,
  closedReasonCode: PropTypes.string,
  closedReasonText: PropTypes.string,
  closedEvidenceUrl: PropTypes.string,
  onToggleStatus: PropTypes.func,
};
