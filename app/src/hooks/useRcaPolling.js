import { useState, useRef, useEffect, useCallback } from 'react';
import apiKubernetes from '@api1/kubernetes';

const INITIAL_INTERVAL = 5000; // 5 seconds
const MAX_INTERVAL = 30000; // 30 seconds
const MAX_POLL_DURATION = 15 * 60 * 1000; // 15 minutes

/**
 * Custom hook for managing RCA (Root Cause Analysis) polling.
 * Uses recursive setTimeout to avoid overlapping requests.
 * Backs off from 5s to 30s over time. Stops after 15 minutes.
 *
 * @param {string} eventId - The event ID to poll RCA status for
 * @param {string} accountId - The account ID
 * @param {function} onStatusChange - Callback invoked with (response, status) when status changes to COMPLETED or FAILED
 * @returns {Object} Object containing polling state and handlers
 */
export const useRcaPolling = (eventId, accountId, onStatusChange) => {
  const [isPolling, setIsPolling] = useState(false);
  const timeoutRef = useRef(null);
  const isPollingRef = useRef(false);
  const startTimeRef = useRef(null);
  const onStatusChangeRef = useRef(onStatusChange);

  // Keep the callback ref up to date without re-creating poll function
  useEffect(() => {
    onStatusChangeRef.current = onStatusChange;
  }, [onStatusChange]);

  const stopPolling = useCallback(() => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
    startTimeRef.current = null;
    isPollingRef.current = false;
    setIsPolling(false);
  }, []);

  const poll = useCallback(async () => {
    if (!isPollingRef.current) return;
    try {
      const response = await apiKubernetes.generateRCA(eventId, accountId, false);
      if (!response?.status) {
        scheduleNext();
        return;
      }
      const status = response.status.toUpperCase();
      if (status === 'COMPLETED' || status === 'FAILED') {
        stopPolling();
        if (onStatusChangeRef.current) {
          onStatusChangeRef.current(response, status);
        }
        return;
      }
      scheduleNext();
    } catch (error) {
      console.error('Error polling RCA status:', error);
      scheduleNext();
    }

    function scheduleNext() {
      if (!isPollingRef.current) return;
      const elapsed = Date.now() - (startTimeRef.current || Date.now());
      if (elapsed >= MAX_POLL_DURATION) {
        stopPolling();
        if (onStatusChangeRef.current) {
          onStatusChangeRef.current({ status: 'FAILED' }, 'FAILED');
        }
        return;
      }
      // Backoff: start at 5s, increase by 1s every minute, cap at 30s
      const interval = Math.min(INITIAL_INTERVAL + Math.floor(elapsed / 60000) * 1000, MAX_INTERVAL);
      timeoutRef.current = setTimeout(poll, interval);
    }
  }, [eventId, accountId, stopPolling]);

  const startPolling = useCallback(() => {
    stopPolling();
    isPollingRef.current = true;
    startTimeRef.current = Date.now();
    setIsPolling(true);
    timeoutRef.current = setTimeout(poll, INITIAL_INTERVAL);
  }, [stopPolling, poll]);

  // Pause polling when browser tab is hidden, resume when visible
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.hidden) {
        if (timeoutRef.current) {
          clearTimeout(timeoutRef.current);
          timeoutRef.current = null;
        }
      } else if (isPollingRef.current) {
        poll();
      }
    };
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      stopPolling();
    };
  }, [poll, stopPolling]);

  return {
    isPolling,
    startPolling,
    stopPolling,
  };
};
