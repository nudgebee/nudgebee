import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import apiKubernetes from '@api1/kubernetes';
import Text from '@shared/format/Text';
import { ds } from '@utils/colors';
import { describeRecurrence, RECURRENCE_BASELINE_WINDOW_MS } from './recurrence';

const TONE_COLOR = {
  success: ds.green[600],
  warning: ds.yellow[600],
  neutral: ds.gray[600],
};

// Sums the per-grouping counts the groupings API returns; shape is
// { data: { event_groupings: [{ event_count }] } }.
const sumEventCount = (res) => (res?.data?.event_groupings || []).reduce((total, group) => total + (Number(group?.event_count) || 0), 0);

/**
 * One line under a completed action saying whether the problem came back.
 *
 * Deliberately says "no recurrence since", never "fixed by this": something else may have resolved
 * it, or load may simply have dropped. Renders nothing at all when it cannot answer honestly.
 */
const RecurrenceSince = ({ accountId, fingerprint, since }) => {
  const [verdict, setVerdict] = useState(null);

  // Depend on the timestamp as a number, never on the prop itself: `since` accepts a Date, and a
  // fresh Date instance on every parent render would re-run this effect forever.
  const sinceMs = since ? new Date(since).getTime() : NaN;

  useEffect(() => {
    if (!accountId || !fingerprint || !Number.isFinite(sinceMs)) return undefined;
    const actedAt = new Date(sinceMs);

    let cancelled = false;
    const baselineStart = new Date(actedAt.getTime() - RECURRENCE_BASELINE_WINDOW_MS);

    Promise.all([
      // Occurrences after the action ran.
      apiKubernetes.getK8sEventGroupings(1000, 0, { account_id: accountId, fingerprint, start_date: actedAt, end_date: new Date() }),
      // Cadence BEFORE it ran — the baseline the silence is judged against.
      apiKubernetes.getK8sEventGroupings(1000, 0, { account_id: accountId, fingerprint, start_date: baselineStart, end_date: actedAt }),
    ])
      .then(([sinceRes, baselineRes]) => {
        if (cancelled) return;
        setVerdict(
          describeRecurrence({
            recurrencesSince: sumEventCount(sinceRes),
            baselineCount: sumEventCount(baselineRes),
            baselineWindowMs: RECURRENCE_BASELINE_WINDOW_MS,
            actedAtMs: actedAt.getTime(),
          })
        );
      })
      // A failed lookup must not put a claim on screen; stay silent instead.
      .catch(() => {
        if (!cancelled) setVerdict(null);
      });

    return () => {
      cancelled = true;
    };
  }, [accountId, fingerprint, sinceMs]);

  if (!verdict) return null;
  return <Text value={verdict.text} sx={{ fontSize: ds.text.caption, color: TONE_COLOR[verdict.tone] || ds.gray[600] }} />;
};

RecurrenceSince.propTypes = {
  accountId: PropTypes.string,
  fingerprint: PropTypes.string,
  since: PropTypes.oneOfType([PropTypes.string, PropTypes.instanceOf(Date)]),
};

export default RecurrenceSince;
