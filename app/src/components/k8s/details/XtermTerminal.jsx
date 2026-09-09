import React, { useEffect, useRef, useState } from 'react';
import '@xterm/xterm/css/xterm.css';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Select } from '@ui/Select';
import { getRelayServerEndpoint } from '@lib/HttpService';
import { ds } from '@utils/colors';

/**
 * Pull a human-readable reason out of a parsed error body.
 *
 * The two upstreams disagree on shape: the relay proxy answers
 * `{ error, description }` while relay-server answers
 * `{ errors: [{ code, message }] }` (`utils.BuildError`). Walk either one down to
 * the first string rather than stringifying an object into `[object Object]`.
 */
const pickErrorMessage = (value, depth = 0) => {
  if (typeof value === 'string') return value;
  if (depth >= 5 || value == null) return '';
  if (Array.isArray(value)) return pickErrorMessage(value[0], depth + 1);
  if (typeof value === 'object') {
    return pickErrorMessage(value.message ?? value.description ?? value.error ?? value.errors, depth + 1);
  }
  return '';
};

/**
 * Build an error message for a failed terminal request.
 *
 * `res.statusText` is empty over HTTP/2, so failures used to surface as a bare
 * `HTTP 400:` with no reason — that is what a relay-proxy rejection looked like in
 * #36589. The response body carries the actual cause, so prefer it.
 */
const describeHttpError = async (res) => {
  let detail = '';
  try {
    const body = await res.text();
    if (body) {
      try {
        detail = pickErrorMessage(JSON.parse(body)) || body;
      } catch {
        detail = body;
      }
    }
  } catch {
    // Body unreadable (already consumed, or the connection dropped) — fall back below.
  }
  // Cap it: an upstream HTML error page would otherwise flood the terminal.
  const reason = (detail || res.statusText || '').trim().slice(0, 200);
  return `HTTP ${res.status}: ${reason}`;
};

/**
 * HTTP-based TerminalComponent with improved polling logic and dimension handling
 * Features:
 * - Prevents concurrent read requests
 * - Adaptive polling intervals
 * - Exponential backoff on errors
 * - Request timeout handling
 * - Fixed dimension calculation issues
 */
const TerminalComponent = ({ accountId, httpEndpoint = getRelayServerEndpoint() + '/ws', data: { name, namespace } }) => {
  const terminalRef = useRef(null);
  const xtermRef = useRef(null);
  const fitAddonRef = useRef(null);
  const sessionIdRef = useRef(null);
  const pollTimerRef = useRef(null);
  const isConnectedRef = useRef(false);
  const containerRef = useRef(null);

  // State to track if container is ready
  const [isContainerReady, setIsContainerReady] = useState(false);

  // Pod-container selection. Three separate pieces of state on purpose:
  //   requestedContainer — the user's explicit pick. This IS a dependency of the
  //     init effect, so changing it tears the session down and reopens against
  //     the chosen container.
  //   podContainers / activeContainer — what the agent reported back on start.
  //     These are deliberately NOT effect dependencies: they are set *from* the
  //     start response, so making them dependencies would restart the session
  //     that just produced them, forever.
  const [requestedContainer, setRequestedContainer] = useState('');
  const [podContainers, setPodContainers] = useState([]);
  const [activeContainer, setActiveContainer] = useState('');

  // Drop the selection when this component is pointed at a different pod —
  // 'nginx' chosen on one pod is meaningless on the next, and would be rejected
  // by the agent as a container that pod does not have.
  //
  // Done during render rather than in an effect on purpose: requestedContainer
  // is a dependency of the init effect, so an effect-based reset would let that
  // effect open one session against the previous pod's container before the
  // reset landed. Adjusting during render means the init effect only ever sees
  // the cleared value.
  //
  // Both current call sites unmount the terminal between pods, so this guards
  // the component's contract rather than a live bug — but the contract has to
  // hold now that the selection feeds the init effect.
  const podKey = `${namespace}/${name}`;
  // Bumped whenever a session is superseded — by a container switch, a pod
  // change, or unmount. `startSession` captures the value it was issued with and
  // discards its own response if it no longer matches, because the start fetch
  // is not abortable from the effect cleanup: a late response would otherwise
  // overwrite sessionIdRef with a session nothing will ever close, leaving a
  // live shell process running inside the pod.
  const sessionGenRef = useRef(0);

  const [selectionPodKey, setSelectionPodKey] = useState(podKey);
  if (selectionPodKey !== podKey) {
    setSelectionPodKey(podKey);
    setRequestedContainer('');
    setPodContainers([]);
    setActiveContainer('');
  }

  // New refs for improved polling logic
  const isPollingRef = useRef(false);
  const pollIntervalRef = useRef(1000); // Start with 1 second
  const consecutiveEmptyReadsRef = useRef(0);
  const abortControllerRef = useRef(null);
  const lastActivityRef = useRef(Date.now());
  // Set when a keystroke lands while a read is already in flight, so that read
  // re-arms quickly on completion instead of falling back to the idle cadence.
  const pendingInputPollRef = useRef(false);

  // Constants for polling behavior
  const MIN_POLL_INTERVAL = 500; // Minimum 500ms
  const MAX_POLL_INTERVAL = 5000; // Maximum 5 seconds
  const ACTIVE_POLL_INTERVAL = 100; // While output is still flowing
  const INPUT_POLL_DELAY = 30; // After a keystroke, just long enough to coalesce a burst
  const EMPTY_READ_THRESHOLD = 5; // After 5 empty reads, slow down
  const REQUEST_TIMEOUT = 100000; // 10 second timeout for requests

  // Safe fit function with dimension checks
  const safeFit = () => {
    if (!fitAddonRef.current || !terminalRef.current || !xtermRef.current) {
      return false;
    }

    try {
      // Check if container has dimensions
      const container = terminalRef.current;
      const rect = container.getBoundingClientRect();

      if (rect.width === 0 || rect.height === 0) {
        console.warn('Container has no dimensions, skipping fit');
        return false;
      }

      // Additional check for container visibility
      if (container.offsetWidth === 0 || container.offsetHeight === 0) {
        console.warn('Container is not visible, skipping fit');
        return false;
      }

      fitAddonRef.current.fit();
      return true;
    } catch (error) {
      console.warn('Fit operation failed:', error);
      return false;
    }
  };

  // Retry fit with exponential backoff
  const retryFit = (attempt = 1, maxAttempts = 5) => {
    if (attempt > maxAttempts) {
      console.warn('Max fit attempts reached, giving up');
      return;
    }

    const success = safeFit();
    if (!success) {
      const delay = Math.min(100 * Math.pow(2, attempt - 1), 1000); // Exponential backoff, max 1s
      setTimeout(() => retryFit(attempt + 1, maxAttempts), delay);
    }
  };

  // Send raw input to backend with timeout
  const sendInput = async (data) => {
    const sid = sessionIdRef.current;
    if (!sid || !isConnectedRef.current) {
      return;
    }

    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);

      await fetch(httpEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'exec', session_id: sid, command: data, account_id: accountId }),
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      // The keystroke itself is delivered immediately, but the terminal has no local
      // echo — the character only becomes visible when a `read` brings the pod's echo
      // back. Resetting the interval alone left the already-armed timer running, so
      // each character waited out up to MAX_POLL_INTERVAL before appearing. Pull the
      // next read forward instead.
      // ACTIVE_POLL_INTERVAL, not MIN_POLL_INTERVAL: the echo needs a round trip, so
      // the 30ms read below usually comes back empty, and an empty read reschedules at
      // whatever this holds. Parking it at 500ms would put the echo half a second out
      // and undo the point of the early poll. At 100ms the EMPTY_READ_THRESHOLD window
      // gives ~500ms of tight polling after each keystroke before the idle backoff.
      consecutiveEmptyReadsRef.current = 0;
      pollIntervalRef.current = ACTIVE_POLL_INTERVAL;
      lastActivityRef.current = Date.now();

      if (isPollingRef.current) {
        // readOutput refuses to overlap requests, so an immediate re-arm here would
        // be swallowed. Let the in-flight read pick this up when it finishes.
        pendingInputPollRef.current = true;
      } else {
        scheduleNextPoll(INPUT_POLL_DELAY);
      }
    } catch (err) {
      if (err.name !== 'AbortError') {
        console.error('Send input error:', err);
      }
    }
  };

  // Adaptive polling interval calculation
  const calculateNextPollInterval = (hasData, isError = false) => {
    if (isError) {
      // Exponential backoff on errors, max 30 seconds
      pollIntervalRef.current = Math.min(pollIntervalRef.current * 2, 30000);
      return pollIntervalRef.current;
    }

    if (hasData) {
      // Output is still flowing — poll tightly so a command's output streams in
      // rather than arriving in half-second steps. This self-throttles: the first
      // empty read starts winding the interval back up.
      consecutiveEmptyReadsRef.current = 0;
      pollIntervalRef.current = ACTIVE_POLL_INTERVAL;
      lastActivityRef.current = Date.now();
    } else {
      // No data received
      consecutiveEmptyReadsRef.current++;

      if (consecutiveEmptyReadsRef.current >= EMPTY_READ_THRESHOLD) {
        // Gradually increase interval when no activity
        const timeSinceActivity = Date.now() - lastActivityRef.current;
        const inactivityMultiplier = Math.min(Math.floor(timeSinceActivity / 10000), 4); // Max 4x

        pollIntervalRef.current = Math.min(MIN_POLL_INTERVAL * (1 + inactivityMultiplier), MAX_POLL_INTERVAL);
      }
    }

    return pollIntervalRef.current;
  };

  // Improved read output with request serialization
  const readOutput = async () => {
    // Prevent concurrent requests
    if (isPollingRef.current) {
      console.debug('Skipping poll - previous request still in progress');
      return;
    }

    const sid = sessionIdRef.current;
    if (!sid || !isConnectedRef.current) {
      return;
    }

    isPollingRef.current = true;

    try {
      // Cancel any existing request
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
      }

      abortControllerRef.current = new AbortController();
      const timeoutId = setTimeout(() => abortControllerRef.current.abort(), REQUEST_TIMEOUT);

      const res = await fetch(httpEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'read', session_id: sid, account_id: accountId }),
        signal: abortControllerRef.current.signal,
      });

      clearTimeout(timeoutId);

      if (!res.ok) {
        throw new Error(await describeHttpError(res));
      }

      const json = await res.json();
      const { data, exit } = json;

      if (exit) {
        stopPolling();
        xtermRef.current?.write('\r\n[Session closed]\r\n');
        return;
      }

      const hasData = data && data.length > 0;

      // Write data to terminal if available
      if (hasData) {
        xtermRef.current?.write(data);
      }

      // A keystroke that landed during this read takes priority over the idle
      // cadence; its echo is what the user is waiting to see.
      const nextInterval = pendingInputPollRef.current ? INPUT_POLL_DELAY : calculateNextPollInterval(hasData);
      pendingInputPollRef.current = false;

      // Schedule next poll
      scheduleNextPoll(nextInterval);
    } catch (error) {
      if (error.name === 'AbortError') {
        console.debug('Request aborted');
      } else {
        console.error('Read output error:', error);

        // Handle error with exponential backoff
        const nextInterval = calculateNextPollInterval(false, true);
        scheduleNextPoll(nextInterval);
      }
    } finally {
      isPollingRef.current = false;
      abortControllerRef.current = null;
    }
  };

  // Schedule next polling cycle
  const scheduleNextPoll = (interval) => {
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
    }

    if (isConnectedRef.current) {
      pollTimerRef.current = setTimeout(readOutput, interval);
    }
  };

  // Stop polling cleanly
  const stopPolling = () => {
    isConnectedRef.current = false;

    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }

    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }

    isPollingRef.current = false;
    pendingInputPollRef.current = false;
  };

  // Start session with improved error handling
  const startSession = async (gen) => {
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);

      const res = await fetch(httpEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'start',
          name: name,
          namespace,
          account_id: accountId,
          // Empty on first open: let the agent apply its kubectl-style default
          // (default-container annotation, else first container in spec order).
          container: requestedContainer || undefined,
        }),
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      if (!res.ok) {
        throw new Error(await describeHttpError(res));
      }

      const { session_id, container, containers } = await res.json();

      if (gen !== sessionGenRef.current) {
        // Superseded while this request was in flight. Close what we just opened
        // instead of leaking it, and leave the current session untouched.
        if (session_id) {
          fetch(httpEndpoint, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action: 'close', session_id, account_id: accountId }),
          }).catch((err) => console.error('Close superseded session error:', err));
        }
        return;
      }

      sessionIdRef.current = session_id;
      isConnectedRef.current = true;

      // Agents older than the container-selection change omit both fields; the
      // picker then stays hidden and the shell behaves exactly as before.
      if (Array.isArray(containers)) {
        setPodContainers(containers);
      }
      if (container) {
        setActiveContainer(container);
      }

      // Reset polling state
      pollIntervalRef.current = MIN_POLL_INTERVAL;
      consecutiveEmptyReadsRef.current = 0;
      lastActivityRef.current = Date.now();

      xtermRef.current?.writeln(container ? `Terminal session started in ${container}...` : 'Terminal session started...');

      // Start first poll
      scheduleNextPoll(100); // Start quickly
    } catch (error) {
      if (error.name === 'AbortError') {
        xtermRef.current?.writeln('\r\nConnection timeout\r\n');
      } else {
        xtermRef.current?.writeln(`\r\nError starting session: ${error.message}\r\n`);
      }
    }
  };

  // Effect to handle container readiness
  useEffect(() => {
    if (containerRef.current) {
      // Use ResizeObserver to detect when container becomes visible/sized
      const resizeObserver = new ResizeObserver((entries) => {
        for (const entry of entries) {
          if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
            setIsContainerReady(true);
          }
        }
      });

      resizeObserver.observe(containerRef.current);

      // Fallback timeout in case ResizeObserver doesn't fire
      const fallbackTimeout = setTimeout(() => {
        setIsContainerReady(true);
      }, 100);

      return () => {
        resizeObserver.disconnect();
        clearTimeout(fallbackTimeout);
      };
    }
  }, []);

  // Initialize terminal
  useEffect(() => {
    if (typeof window === 'undefined' || !isContainerReady) {
      return;
    }

    const { Terminal } = require('@xterm/xterm');
    const { FitAddon } = require('@xterm/addon-fit');

    const xterm = new Terminal({
      cursorBlink: true,
      convertEol: true,
      rows: 30,
      cols: 100,
      scrollback: 10000,
      fontSize: 14,
      fontFamily: 'Monaco, Menlo, "Ubuntu Mono", Consolas, "source-code-pro", monospace',
      lineHeight: 1.4,
      // NOTE: xterm.js renders to <canvas> (rendererType: 'canvas'), which cannot
      // resolve CSS custom properties. These MUST stay literal hex values, not ds tokens.
      theme: {
        background: '#0d1117',
        foreground: '#f0f6fc',
        cursor: '#58a6ff',
        cursorAccent: '#0d1117',
        selection: '#264f78',
        black: '#484f58',
        red: '#ff7b72',
        green: '#7ee787',
        yellow: '#ffa657',
        blue: '#79c0ff',
        magenta: '#bc8cff',
        cyan: '#39c5cf',
        white: '#b1bac4',
        brightBlack: '#6e7681',
        brightRed: '#ffa198',
        brightGreen: '#7ee787',
        brightYellow: '#ffdf5d',
        brightBlue: '#a5b4fc',
        brightMagenta: '#bc8cff',
        brightCyan: '#56d4dd',
        brightWhite: '#f0f6fc',
      },
      disableStdin: false,
      bellStyle: 'none',
      cursorStyle: 'block',
      allowTransparency: true,
      macOptionIsMeta: true,
      rightClickSelectsWord: true,
      rendererType: 'canvas',
    });

    const fitAddon = new FitAddon();
    xterm.loadAddon(fitAddon);

    if (terminalRef.current) {
      xterm.open(terminalRef.current);
    }

    // Store references
    xtermRef.current = xterm;
    fitAddonRef.current = fitAddon;

    // Wait for terminal to be fully rendered before fitting
    const initTimeout = setTimeout(() => {
      retryFit();
    }, 50);

    // Handle terminal input
    xterm.onData((data) => {
      if (!isConnectedRef.current) {
        return;
      }
      sendInput(data);
    });

    // Handle window resize with debouncing
    let resizeTimeout;
    const handleResize = () => {
      clearTimeout(resizeTimeout);
      resizeTimeout = setTimeout(() => {
        retryFit();
      }, 100);
    };

    window.addEventListener('resize', handleResize);

    // Start the session
    startSession(++sessionGenRef.current);

    // Cleanup function
    return () => {
      // Supersede any start still in flight so its response closes itself rather
      // than resurrecting a session past teardown.
      sessionGenRef.current++;

      clearTimeout(initTimeout);
      clearTimeout(resizeTimeout);
      window.removeEventListener('resize', handleResize);

      // Stop polling cleanly
      stopPolling();

      // Close session
      if (sessionIdRef.current) {
        fetch(httpEndpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: 'close', session_id: sessionIdRef.current, account_id: accountId }),
        }).catch((err) => console.error('Close session error:', err));
      }

      if (xterm) {
        xterm.dispose();
      }
    };
    // requestedContainer is a dependency on purpose: switching container tears the
    // xterm instance and the shell session down and reopens both, which is the
    // correct mental model (a different container is a different shell, with its
    // own scrollback) and avoids having to reconcile session state across a swap.
  }, [httpEndpoint, name, namespace, accountId, isContainerReady, requestedContainer]);

  // Only worth showing when there is an actual choice to make.
  const showContainerPicker = podContainers.length > 1;
  const selectedContainer = requestedContainer || activeContainer;

  return (
    <Box sx={{ width: '100%' }}>
      {showContainerPicker ? (
        <Box sx={{ ml: 'var(--ds-space-6)', mt: 'var(--ds-space-6)', maxWidth: 320 }}>
          <Select
            label='Container'
            value={selectedContainer}
            options={podContainers}
            onChange={(next) => {
              // Re-selecting the current container would restart the session for
              // no reason.
              if (next && next !== selectedContainer) {
                setRequestedContainer(next);
              }
            }}
            required
            size='sm'
          />
        </Box>
      ) : null}
      <div
        ref={containerRef}
        className='terminal-container'
        style={{
          width: '100%',
          height: '100%',
          fontFamily: 'Monaco, Menlo, "Ubuntu Mono", "Consolas", "source-code-pro", monospace',
          fontSize: 'var(--ds-text-body-lg)',
          lineHeight: '1.4',
          background: 'var(--ds-gray-700)',
          border: '1px solid var(--ds-brand-600)',
          borderRadius: 'var(--ds-radius-lg)',
          padding: 'var(--ds-space-2)',
          overflow: 'hidden',
          position: 'relative',
          marginTop: 'var(--ds-space-6)',
          marginLeft: 'var(--ds-space-6)',
          minHeight: ds.space.mul(0, 200), // Ensure minimum height
          minWidth: ds.space.mul(0, 150), // Ensure minimum width
        }}
      >
        <div
          ref={terminalRef}
          style={{
            width: '100%',
            height: '100%',
            backgroundColor: 'transparent',
          }}
        />
      </div>
    </Box>
  );
};

TerminalComponent.propTypes = {
  accountId: PropTypes.string.isRequired,
  httpEndpoint: PropTypes.string,
  data: PropTypes.shape({
    name: PropTypes.string.isRequired,
    namespace: PropTypes.string.isRequired,
  }).isRequired,
};

export default TerminalComponent;
