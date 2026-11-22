import { useEffect, useState } from 'react';
import apiRecommendations from '@api1/recommendation';
import { resolutionRefusalText } from '@components/k8s/investigate/changeRollback';

const CHECKING = { status: 'checking', reason: '' };

// How long the dialog waits for an answer before it stops holding the rollback back. The check is
// a courtesy ahead of the click; the apply runs it again regardless.
export const ROLLBACK_CHECK_WAIT_MS = 8000;

/**
 * What the server says about rolling one recorded change back, asked when `enabled` turns true and
 * again whenever the change differs. The page only holds the values a change recorded; whether the
 * workload was changed again since is known to the server alone.
 *
 * status: 'checking' | 'allowed' | 'blocked' | 'unknown'. 'unknown' — the question could not be
 * answered, or was not answered in time — is not 'blocked': the apply runs the same check on the
 * click regardless.
 *
 * @param {{accountId?: string, eventId?: string, changeEventId?: string, enabled?: boolean}} target
 *   changeEventId '' means the change attached to the event itself
 */
export const useRollbackCheck = ({ accountId, eventId, changeEventId = '', enabled = true }) => {
  // An answer is kept with the change it is about, so the answer for one change can never be read
  // as the answer for another — not even for the render between picking a change and asking.
  const target = enabled ? `${accountId}|${eventId}|${changeEventId}` : '';
  const [answer, setAnswer] = useState({ target: '', ...CHECKING });

  useEffect(() => {
    if (!target) return undefined;
    let cancelled = false;
    const settle = (next) => {
      if (!cancelled) setAnswer({ target, ...next });
    };
    const waited = setTimeout(() => settle({ status: 'unknown', reason: '' }), ROLLBACK_CHECK_WAIT_MS);
    apiRecommendations.checkEventRollback(accountId, eventId, changeEventId).then((check) => {
      clearTimeout(waited);
      if (!check) settle({ status: 'unknown', reason: '' });
      else if (check.allowed) settle({ status: 'allowed', reason: '' });
      else settle({ status: 'blocked', reason: resolutionRefusalText(check.reason) });
    });
    return () => {
      cancelled = true;
      clearTimeout(waited);
      setAnswer({ target: '', ...CHECKING });
    };
  }, [target, accountId, eventId, changeEventId]);

  return target && answer.target === target ? answer : CHECKING;
};
