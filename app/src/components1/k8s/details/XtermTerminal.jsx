import React, { useEffect, useRef, useState } from 'react';
import '@xterm/xterm/css/xterm.css';
import PropTypes from 'prop-types';
import { getRelayServerEndpoint } from '@lib/HttpService';

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

  // New refs for improved polling logic
  const isPollingRef = useRef(false);
  const pollIntervalRef = useRef(1000); // Start with 1 second
  const consecutiveEmptyReadsRef = useRef(0);
  const abortControllerRef = useRef(null);
  const lastActivityRef = useRef(Date.now());

  // Constants for polling behavior
  const MIN_POLL_INTERVAL = 500; // Minimum 500ms
  const MAX_POLL_INTERVAL = 5000; // Maximum 5 seconds
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

      // Reset polling interval on successful input (user is active)
      pollIntervalRef.current = MIN_POLL_INTERVAL;
      lastActivityRef.current = Date.now();
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
      // Data received - use minimum interval for responsiveness
      consecutiveEmptyReadsRef.current = 0;
      pollIntervalRef.current = MIN_POLL_INTERVAL;
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
        throw new Error(`HTTP ${res.status}: ${res.statusText}`);
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

      // Calculate next polling interval
      const nextInterval = calculateNextPollInterval(hasData);

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
  };

  // Start session with improved error handling
  const startSession = async () => {
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);

      const res = await fetch(httpEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'start', name: name, namespace, account_id: accountId }),
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`);
      }

      const { session_id } = await res.json();
      sessionIdRef.current = session_id;
      isConnectedRef.current = true;

      // Reset polling state
      pollIntervalRef.current = MIN_POLL_INTERVAL;
      consecutiveEmptyReadsRef.current = 0;
      lastActivityRef.current = Date.now();

      xtermRef.current?.writeln('Terminal session started...');

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
    startSession();

    // Cleanup function
    return () => {
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
  }, [httpEndpoint, name, namespace, accountId, isContainerReady]);

  return (
    <div
      ref={containerRef}
      className='terminal-container'
      style={{
        width: '100%',
        height: '100%',
        fontFamily: 'Monaco, Menlo, "Ubuntu Mono", "Consolas", "source-code-pro", monospace',
        fontSize: '14px',
        lineHeight: '1.4',
        background: '#300A24',
        border: '1px solid #30363d',
        borderRadius: '8px',
        padding: '8px',
        overflow: 'hidden',
        position: 'relative',
        marginTop: '30px',
        marginLeft: '30px',
        minHeight: '400px', // Ensure minimum height
        minWidth: '300px', // Ensure minimum width
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
