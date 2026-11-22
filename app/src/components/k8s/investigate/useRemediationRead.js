import { useCallback, useEffect, useRef, useState } from 'react';
import apiKubernetes from '@api1/kubernetes';
import { loopOf } from './remediationLoop';

// useRemediationRead owns the page's reads of ai_remediation_get: one read at a time, the next one scheduled only
// after the current one settles, so a slow read is never thrown away by the next tick.
//
// - `readKey` names what is being read (the event and the analysis the plan belongs to). A new key reads at once;
//   a read that lands for an older key is dropped and the new key is read.
// - `reload()` asks for a read now. During a read in flight it only marks "read again when done" (coalesced).
// - While polling is on (setPolling), the next read starts `intervalMs` after the last one settled.
// - A read that fails, or comes back empty, keeps what was shown. A read whose loop says the waiting or denied
//   approvals could not be read (loop.pending_approvals_unread / denied_approvals_unread) keeps that list as shown.
//
// `sessionIds` are the alert's conversations: a follow-up in one of them (nb:conversation-followup) reads at once
// and calls `onFollowup`.
//
// Returns {key, eventId, res, readCount, reload, setPolling, setPlan}: `key` is the readKey of the read on screen,
// `readCount` counts reads that landed, `setPlan` lets the old panel hand back a plan it generated for that key.
export default function useRemediationRead({
  accountId,
  eventId,
  readKey,
  sessionIds = [],
  onFollowup,
  intervalMs = 10000,
  read = (a, e) => apiKubernetes.getRemediation(a, e),
}) {
  const [state, setState] = useState({ key: '', eventId: '', res: null, readCount: 0 });
  const [polling, setPolling] = useState(false);
  const inFlight = useRef(false);
  const again = useRef(false);
  const timer = useRef(null);
  const current = useRef({ accountId, eventId, readKey });
  current.current = { accountId, eventId, readKey };
  const pollingRef = useRef(polling);
  pollingRef.current = polling;
  const readRef = useRef(read);
  readRef.current = read;
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearTimeout(timer.current);
    };
  }, []);

  // run and schedule close over refs only, so they are made once and never go stale.
  const intervalRef = useRef(intervalMs);
  intervalRef.current = intervalMs;
  const api = useRef(null);
  if (!api.current) {
    // The next poll, after the last read settled. A hidden tab waits for the next turn instead of reading.
    const schedule = function scheduleNext() {
      clearTimeout(timer.current);
      timer.current = null;
      if (!pollingRef.current || !mounted.current) return;
      timer.current = setTimeout(() => {
        timer.current = null;
        if (typeof document !== 'undefined' && document.hidden) scheduleNext();
        // eslint-disable-next-line no-use-before-define
        else run();
      }, intervalRef.current);
    };
    const run = async function readNow() {
      const asked = { ...current.current, eventId: String(current.current.eventId || '') };
      if (!asked.readKey || !asked.accountId || !asked.eventId) return;
      if (inFlight.current) {
        again.current = true;
        return;
      }
      clearTimeout(timer.current);
      timer.current = null;
      inFlight.current = true;
      let res;
      try {
        res = await readRef.current(asked.accountId, asked.eventId);
      } catch (err) {
        console.error('Failed to read the remediation', err);
        res = undefined;
      }
      inFlight.current = false;
      if (!mounted.current) return;
      const now = current.current;
      if (now.readKey !== asked.readKey || String(now.eventId || '') !== asked.eventId) {
        // What is asked for changed while this read was out: read the new one.
        again.current = false;
        readNow();
        return;
      }
      if (res && typeof res === 'object') {
        setState((prev) => {
          const loop = res.loop;
          const sameEvent = prev.eventId === asked.eventId;
          const prevLoop = prev.res?.loop || {};
          // A list the server could not read keeps the one shown before (approvals waiting, approvals denied).
          const keepPending = loop && loop.pending_approvals_unread && sameEvent;
          const keepDenied = loop && loop.denied_approvals_unread && sameEvent;
          const next =
            keepPending || keepDenied
              ? {
                  ...res,
                  loop: {
                    ...loop,
                    ...(keepPending ? { pending_approvals: prevLoop.pending_approvals } : null),
                    ...(keepDenied ? { denied_approvals: prevLoop.denied_approvals } : null),
                  },
                }
              : res;
          return { key: asked.readKey, eventId: asked.eventId, res: next, readCount: prev.readCount + 1 };
        });
      } else {
        // A failed read keeps what this event showed; it still marks the key as read, so nothing waits on it forever.
        setState((prev) => ({ ...prev, key: asked.readKey, eventId: asked.eventId, res: prev.eventId === asked.eventId ? prev.res : null }));
      }
      if (again.current) {
        again.current = false;
        readNow();
        return;
      }
      schedule();
    };
    api.current = { run, schedule };
  }
  const { run, schedule } = api.current;

  // A new key (or event) reads at once.
  useEffect(() => {
    if (readKey) run();
  }, [readKey, eventId, accountId, run]);

  // Polling turned on while idle: schedule the next read; turned off: drop the timer.
  useEffect(() => {
    if (!polling) {
      clearTimeout(timer.current);
      timer.current = null;
      return;
    }
    if (!inFlight.current && !timer.current) schedule();
  }, [polling, schedule]);

  // The chat says a follow-up appeared or was answered (nb:conversation-followup): when it is in one of this alert's
  // conversations, read now rather than at the next poll.
  const sessionKey = sessionIds.filter(Boolean).join('|');
  const onFollowupRef = useRef(onFollowup);
  onFollowupRef.current = onFollowup;
  useEffect(() => {
    if (!sessionKey) return undefined;
    const ids = sessionKey.split('|');
    const handler = (e) => {
      if (!ids.includes(e?.detail?.sessionId)) return;
      run();
      onFollowupRef.current?.();
    };
    window.addEventListener('nb:conversation-followup', handler);
    return () => window.removeEventListener('nb:conversation-followup', handler);
  }, [sessionKey, run]);

  const setPlan = useCallback((plan, forKey) => {
    setState((prev) => (prev.key === forKey ? { ...prev, res: plan } : prev));
  }, []);

  return { ...state, reload: run, setPolling, setPlan };
}

// useStickyLoop decides which panel the tab shows. "Loop on" is sticky per event: once any read for this event
// returned a loop object, the loop panel stays, fed the last loop state, through key changes, reads in flight,
// failed or empty reads, and a read that comes back without a loop. The old panel (which generates a plan on its
// own) is allowed only when a read of the current plan key affirmatively came back without a loop and no read of
// this event ever had one.
export function useStickyLoop({ eventId, res, planKey, readKey }) {
  const id = String(eventId || '');
  const live = loopOf(res) ? res : null;
  const [memory, setMemory] = useState({ eventId: '', res: null });
  useEffect(() => {
    if (live && id) setMemory({ eventId: id, res: live });
  }, [live, id]);
  const remembered = memory.eventId === id ? memory.res : null;
  const loopOn = Boolean(live || remembered);
  return {
    loopRes: live || remembered,
    loopOn,
    legacy: !loopOn && Boolean(planKey) && readKey === planKey && Boolean(res) && typeof res === 'object',
  };
}

// remediationReadKeyFor is the key the page reads the remediation under. With the loop flag on, the event is read
// before its analysis completes too (a probe), so the loop's state shows; with it off (or not read yet) only once per
// analysis, as before the loop.
export function remediationReadKeyFor({ accountId, eventId, planKey, loopFlag }) {
  if (!accountId || !eventId) return '';
  return planKey || (loopFlag === true ? `probe-${eventId}` : '');
}

// remediationPanelsFor decides the tab's panel from the tenant's loop flag (null while it is read) and the read:
// the loop panel only with the flag on and a read that had a loop; the old panel always with the flag off, whatever
// the read returned, and with it on only when a read affirmatively came back without a loop. Neither while the flag
// is read, so the old panel never flashes on a loop tenant.
export function remediationPanelsFor({ loopFlag, sticky }) {
  const loopOn = loopFlag === true && Boolean(sticky?.loopOn);
  return { loopOn, legacy: loopFlag === false || (loopFlag === true && !loopOn && Boolean(sticky?.legacy)) };
}
