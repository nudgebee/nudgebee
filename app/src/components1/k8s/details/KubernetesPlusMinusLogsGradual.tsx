import React, { useEffect, useState, useRef } from 'react';
import { Box, IconButton, CircularProgress, Tooltip } from '@mui/material';
import AddIcon from '@mui/icons-material/Add';
import RemoveIcon from '@mui/icons-material/Remove';
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import MyLocationIcon from '@mui/icons-material/MyLocation';
import { Text } from '@components1/common';
import { LogDate } from '@components1/k8s/common/LogDate';
import observability from '@api1/observability';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import CustomIconButton from '@components1/CustomIconButton';
import SafeIcon from '@components1/common/SafeIcon';
import Loader from '@components1/common/Loader';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { action } from 'src/utils/actionStyles';

interface LogEntry {
  timestamp: string;
  message: string;
  severity?: string;
  labels?: Record<string, unknown>;
  [key: string]: any;
}

interface KubernetesPlusMinusLogsGradualProps {
  accountId: string;
  query: {
    sample?: string;
    data: {
      timestamp: string;
      message: string;
      [key: string]: any;
    };
    logQuery?: string;
  };
  onMenuClick?: (action: string, data: { stream: LogEntry; data: string }) => void;
  getMenuItem?: () => any[];
  onGenerateLogAnalysis?: (stream: LogEntry, message: string) => void;
}

const TIME_INCREMENT_SECONDS = 10; // 10 seconds per load
const INITIAL_WINDOW_SECONDS = 10; // ±10 seconds initially
const SCROLL_INCREMENT = 10; // Number of logs to scroll per click
const LOG_FETCH_LIMIT = 100; // Maximum number of logs to fetch per request

// Normalize a timestamp to milliseconds.
// Handles epoch-seconds (<1e12), epoch-ms (1e12–1e15), epoch-µs (>1e15), and ISO strings.
const normalizeTimestampToMs = (ts: string | number): number => {
  const num = typeof ts === 'number' ? ts : Number(ts);
  if (!isNaN(num)) {
    if (num > 1e15) return Math.round(num / 1000); // microseconds → ms
    if (num < 1e12 && num > 0) return num * 1000; // seconds → ms
    return num; // already ms
  }
  return new Date(ts as string).getTime();
};

const KubernetesPlusMinusLogsGradual: React.FC<KubernetesPlusMinusLogsGradualProps> = ({
  accountId,
  query,
  onMenuClick,
  getMenuItem,
  onGenerateLogAnalysis,
}) => {
  const { assistantName } = useTenantBranding();
  const eventTimestamp = query?.data?.timestamp ? normalizeTimestampToMs(query.data.timestamp) : Date.now();
  const logContainerRef = useRef<HTMLDivElement>(null);
  const eventMarkerRef = useRef<HTMLDivElement>(null);

  const logLabels = { namespace: 'namespace', pod: 'pod', app: 'app' };

  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [startTime, setStartTime] = useState(eventTimestamp - INITIAL_WINDOW_SECONDS * 1000);
  const [endTime, setEndTime] = useState(Math.min(eventTimestamp + INITIAL_WINDOW_SECONDS * 1000, Date.now()));
  const [loadingBefore, setLoadingBefore] = useState(false);
  const [loadingAfter, setLoadingAfter] = useState(false);
  const [initialLoading, setInitialLoading] = useState(true);
  const [hasMoreBefore, setHasMoreBefore] = useState(true);
  const [hasMoreAfter, setHasMoreAfter] = useState(true);
  const [visibleStartIndex, setVisibleStartIndex] = useState(0);

  // Format time duration for display
  const getTimeDuration = () => {
    const beforeSeconds = Math.round((eventTimestamp - startTime) / 1000);
    const afterSeconds = Math.round((endTime - eventTimestamp) / 1000);
    return `−${beforeSeconds}s / +${afterSeconds}s`;
  };

  // Fetch logs for a given time range
  const fetchLogs = async (start: number, end: number) => {
    try {
      const rawLogQuery = query.logQuery || '';
      const andFilters: any[] = [];

      if (rawLogQuery) {
        try {
          const parsed = JSON.parse(rawLogQuery);
          if (parsed.namespaceName) {
            andFilters.push({ _binary: { [logLabels.namespace]: { _eq: parsed.namespaceName } } });
          }
          if (parsed.podName) {
            andFilters.push({ _binary: { [logLabels.pod]: { _eq: parsed.podName } } });
          }
          if (parsed.workloadName) {
            andFilters.push({ _binary: { [logLabels.app]: { _eq: parsed.workloadName } } });
          }
        } catch (e) {
          console.error('Error parsing logQuery for query_request:', e);
        }
      }

      if (andFilters.length === 0) {
        snackbar.warning('No label filters available to fetch surrounding logs');
        return [];
      }

      const requestBody = {
        account_id: accountId,
        start_time: start,
        end_time: end,
        query: '',
        limit: LOG_FETCH_LIMIT,
        offset: 0,
        query_request: {
          where: { _and: andFilters },
        },
        request: { query_type: 'dsl', checkMapper: true },
      };

      const response = await observability.fetchLogs(requestBody);
      const error = response?.error || response?.data?.errors;

      if (error) {
        snackbar.error(`Error fetching logs: ${parseHttpResponseBodyMessage(response.data)}`);
        return [];
      }

      const fetchedLogs = response?.data?.data?.logs_query || [];
      return fetchedLogs;
    } catch (error) {
      console.error('Error fetching logs:', error);
      snackbar.error('Failed to fetch logs');
      return [];
    }
  };

  const originalLog = { ...query.data, message: !query.data?.message && query?.sample ? query.sample : query.data?.message };

  const isOriginalLog = (log: LogEntry) => {
    const originalLogKey = `${originalLog.timestamp}-${originalLog.message}`;
    const logKey = `${log.timestamp}-${log.message}`;
    return logKey === originalLogKey;
  };

  // Initial load - reset state when query changes
  useEffect(() => {
    if (!query?.data?.timestamp) return;

    const loadInitialLogs = async () => {
      // Reset state when query changes
      setLogs([]);
      setInitialLoading(true);
      setHasMoreBefore(true);
      setHasMoreAfter(true);
      setVisibleStartIndex(0);

      const newEventTimestamp = query?.data?.timestamp ? normalizeTimestampToMs(query.data.timestamp) : Date.now();
      const newStartTime = newEventTimestamp - INITIAL_WINDOW_SECONDS * 1000;
      const newEndTime = Math.min(newEventTimestamp + INITIAL_WINDOW_SECONDS * 1000, Date.now());

      setStartTime(newStartTime);
      setEndTime(newEndTime);

      const fetchedLogs = await fetchLogs(newStartTime, newEndTime);

      // Ensure the original log entry is included

      const hasOriginalLog = fetchedLogs.some((log: LogEntry) => isOriginalLog(log));

      let allLogs = [...fetchedLogs];
      if (!hasOriginalLog && originalLog) {
        // Add the original log entry if it's not in the fetched results
        allLogs.push(originalLog);
      }

      // Sort logs by timestamp
      allLogs.sort((a: LogEntry, b: LogEntry) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
      const sortedLogs = allLogs;

      setLogs(sortedLogs);
      setInitialLoading(false);

      // Scroll to event marker after initial load
      setTimeout(() => {
        eventMarkerRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
      }, 100);
    };

    loadInitialLogs();
  }, [query?.data?.timestamp, query?.data?.message, query?.sample, query?.logQuery]);

  // Safety check for query data - after all hooks
  if (!query?.data?.timestamp) {
    return (
      <Box display='flex' justifyContent='center' alignItems='center' minHeight='200px'>
        <Text value='No timestamp data available for this log entry' sx={{ fontSize: '14px', color: colors.text.greyDark }} />
      </Box>
    );
  }

  // Load earlier logs (before current start time)
  const loadEarlierLogs = async () => {
    if (loadingBefore || !hasMoreBefore) {
      return;
    }

    setLoadingBefore(true);
    const newStartTime = startTime - TIME_INCREMENT_SECONDS * 1000;
    const fetchedLogs = await fetchLogs(newStartTime, startTime);

    if (fetchedLogs.length === 0) {
      setHasMoreBefore(false);
      snackbar.info('No more earlier logs available');
    } else {
      // Sort and merge with existing logs
      const sortedNewLogs = fetchedLogs.sort((a: LogEntry, b: LogEntry) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());

      // Remove duplicates based on timestamp and message
      const existingKeys = new Set(logs.map((log: LogEntry) => `${log.timestamp}-${log.message}`));
      const uniqueNewLogs = sortedNewLogs.filter((log: LogEntry) => !existingKeys.has(`${log.timestamp}-${log.message}`));

      setLogs((prev) => [...uniqueNewLogs, ...prev]);
      setStartTime(newStartTime);
    }

    setLoadingBefore(false);
  };

  // Load later logs (after current end time)
  const loadLaterLogs = async () => {
    if (loadingAfter || !hasMoreAfter) {
      return;
    }

    setLoadingAfter(true);
    const newEndTime = Math.min(endTime + TIME_INCREMENT_SECONDS * 1000, Date.now());
    const fetchedLogs = await fetchLogs(endTime, newEndTime);

    if (fetchedLogs.length === 0 || newEndTime >= Date.now()) {
      setHasMoreAfter(false);
      if (fetchedLogs.length === 0) {
        snackbar.info('No more later logs available');
      }
    }

    if (fetchedLogs.length > 0) {
      // Sort and merge with existing logs
      const sortedNewLogs = fetchedLogs.sort((a: LogEntry, b: LogEntry) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());

      // Remove duplicates
      const existingKeys = new Set(logs.map((log: LogEntry) => `${log.timestamp}-${log.message}`));
      const uniqueNewLogs = sortedNewLogs.filter((log: LogEntry) => !existingKeys.has(`${log.timestamp}-${log.message}`));

      setLogs((prev) => [...prev, ...uniqueNewLogs]);
      setEndTime(newEndTime);
    }

    setLoadingAfter(false);
  };

  // Scroll navigation
  const scrollUp = () => {
    if (visibleStartIndex > 0) {
      const newIndex = Math.max(0, visibleStartIndex - SCROLL_INCREMENT);
      setVisibleStartIndex(newIndex);
      scrollToLog(newIndex);
    }
  };

  const scrollDown = () => {
    if (visibleStartIndex < logs.length - SCROLL_INCREMENT) {
      const newIndex = Math.min(logs.length - 1, visibleStartIndex + SCROLL_INCREMENT);
      setVisibleStartIndex(newIndex);
      scrollToLog(newIndex);
    }
  };

  const scrollToLog = (index: number) => {
    const logElements = logContainerRef.current?.querySelectorAll('.log-entry');
    const element = logElements?.[index];
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  };

  const scrollToOriginalLog = () => {
    eventMarkerRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  };

  const handleGenerateLogAnalysis = (stream: any, message: string) => {
    if (onGenerateLogAnalysis) {
      onGenerateLogAnalysis(stream, message);
    }
  };

  if (initialLoading) {
    return <Loader style={{ height: '200px', width: '100%' }} />;
  }

  return (
    <Box sx={{ width: '100%', position: 'relative' }}>
      {/* Header with controls */}
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          padding: '8px 12px',
          backgroundColor: colors.background.white,
          borderBottom: `1px solid ${colors.border.secondary}`,
          position: 'sticky',
          top: 0,
          zIndex: 10,
        }}
      >
        {/* Time range info */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
          <Text value={`Time range: ${getTimeDuration()}`} sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }} />
          <Text value={`Showing ${logs.length} logs`} sx={{ fontSize: '12px', color: colors.text.greyDark }} />
        </Box>

        {/* Time expansion controls */}
        <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
          <Tooltip title={`Load ${TIME_INCREMENT_SECONDS}s earlier logs`}>
            <span>
              <IconButton
                size='small'
                onClick={loadEarlierLogs}
                disabled={loadingBefore || !hasMoreBefore}
                sx={{
                  backgroundColor: colors.background.white,
                  border: `1px solid ${colors.border.secondary}`,
                  '&:hover': { backgroundColor: colors.background.primaryLightest },
                }}
              >
                {loadingBefore ? <CircularProgress size={16} /> : <RemoveIcon fontSize='small' />}
              </IconButton>
            </span>
          </Tooltip>

          <Text value='Time' sx={{ fontSize: '12px', color: colors.text.greyDark, mx: 1 }} />

          <Tooltip title={`Load ${TIME_INCREMENT_SECONDS}s later logs`}>
            <span>
              <IconButton
                size='small'
                onClick={loadLaterLogs}
                disabled={loadingAfter || !hasMoreAfter}
                sx={{
                  backgroundColor: colors.background.white,
                  border: `1px solid ${colors.border.secondary}`,
                  '&:hover': { backgroundColor: colors.background.primaryLightest },
                }}
              >
                {loadingAfter ? <CircularProgress size={16} /> : <AddIcon fontSize='small' />}
              </IconButton>
            </span>
          </Tooltip>
        </Box>

        {/* Scroll navigation controls */}
        <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
          <Tooltip title={`Scroll up ${SCROLL_INCREMENT} logs`}>
            <span>
              <IconButton
                size='small'
                onClick={scrollUp}
                disabled={visibleStartIndex === 0}
                sx={{
                  backgroundColor: colors.background.white,
                  border: `1px solid ${colors.border.secondary}`,
                  '&:hover': { backgroundColor: colors.background.primaryLightest },
                }}
              >
                <KeyboardArrowUpIcon fontSize='small' />
              </IconButton>
            </span>
          </Tooltip>

          <Text value='Scroll' sx={{ fontSize: '12px', color: colors.text.greyDark, mx: 1 }} />

          <Tooltip title={`Scroll down ${SCROLL_INCREMENT} logs`}>
            <span>
              <IconButton
                size='small'
                onClick={scrollDown}
                disabled={visibleStartIndex >= logs.length - SCROLL_INCREMENT}
                sx={{
                  backgroundColor: colors.background.white,
                  border: `1px solid ${colors.border.secondary}`,
                  '&:hover': { backgroundColor: colors.background.primaryLightest },
                }}
              >
                <KeyboardArrowDownIcon fontSize='small' />
              </IconButton>
            </span>
          </Tooltip>

          <Tooltip title='Go to original log'>
            <span>
              <IconButton
                size='small'
                onClick={scrollToOriginalLog}
                sx={{
                  backgroundColor: colors.background.white,
                  border: `1px solid ${colors.border.secondary}`,
                  '&:hover': { backgroundColor: colors.background.primaryLightest },
                  ml: 1,
                }}
              >
                <MyLocationIcon fontSize='small' />
              </IconButton>
            </span>
          </Tooltip>
        </Box>
      </Box>

      {/* Log list */}
      <Box
        ref={logContainerRef}
        sx={{
          maxHeight: '600px',
          overflowY: 'auto',
          padding: '8px',
          backgroundColor: colors.background.white,
        }}
      >
        {logs.length === 0 ? (
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'center',
              alignItems: 'center',
              minHeight: '200px',
              color: colors.text.greyDark,
            }}
          >
            <Text value='No logs found in this time range' sx={{ fontSize: '14px' }} />
          </Box>
        ) : (
          logs.map((log, index) => {
            const isEventLog = isOriginalLog(log);

            return (
              <Box key={`${log.timestamp}-${index}`} className='log-entry'>
                {/* Log entry */}
                <Box
                  ref={isEventLog ? eventMarkerRef : null}
                  sx={{
                    display: 'flex',
                    gap: 2,
                    padding: '8px 12px',
                    borderBottom: `1px solid ${colors.border.secondary}`,
                    borderLeft: isEventLog ? `3px solid ${colors.text.secondary}` : 'none',
                    '&:hover': {
                      backgroundColor: colors.background.primaryLightest,
                    },
                    backgroundColor: isEventLog ? 'rgba(59, 130, 246, 0.05)' : 'transparent',
                  }}
                >
                  {/* Timestamp */}
                  <Box sx={{ minWidth: '180px', flexShrink: 0 }}>
                    <LogDate timestamp={new Date(log.timestamp).getTime()} log={log.severity || log.message || ''} />
                  </Box>

                  {/* Message */}
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Text
                      value={log.message}
                      sx={{
                        fontSize: '13px',
                        lineHeight: '1.6',
                        overflowWrap: 'anywhere',
                        wordBreak: 'break-all',
                        fontFamily: 'monospace',
                      }}
                    />
                  </Box>

                  {/* Actions */}
                  <Box sx={{ display: 'flex', gap: 1, flexShrink: 0 }}>
                    {onGenerateLogAnalysis && (
                      <CustomIconButton
                        onClick={(event) => {
                          event.stopPropagation();
                          handleGenerateLogAnalysis(log, log.message);
                        }}
                        variant='secondary'
                        size='xsmall'
                        sx={{ height: '28px', width: '28px' }}
                      >
                        <SafeIcon alt={`Ask ${assistantName}`} src={getNubiIconUrl()} width={24} height={24} />
                      </CustomIconButton>
                    )}
                    {getMenuItem && onMenuClick && (
                      <ThreeDotsMenu
                        sx={{ ...action.primary }}
                        menuItems={getMenuItem()}
                        data={{ stream: log, data: log.message }}
                        onMenuClick={onMenuClick}
                      />
                    )}
                  </Box>
                </Box>
              </Box>
            );
          })
        )}
      </Box>

      {/* Bottom load more indicator */}
      {(loadingBefore || loadingAfter) && (
        <Box
          sx={{
            position: 'sticky',
            bottom: 0,
            backgroundColor: colors.background.secondary,
            padding: '8px',
            textAlign: 'center',
            borderTop: `1px solid ${colors.border.secondary}`,
          }}
        >
          <CircularProgress size={20} sx={{ mr: 1 }} />
          <Text
            value={loadingBefore ? 'Loading earlier logs...' : 'Loading later logs...'}
            sx={{ fontSize: '12px', color: colors.text.greyDark, display: 'inline' }}
          />
        </Box>
      )}
    </Box>
  );
};

export default KubernetesPlusMinusLogsGradual;
