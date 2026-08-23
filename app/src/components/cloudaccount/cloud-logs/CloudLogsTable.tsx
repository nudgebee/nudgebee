import React, { useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import { Chip as DsChip } from '@ui/Chip';
import { Button as DsButton } from '@ui/Button';
import { ds } from '@utils/colors';

// Presentational cloud-logs table shared by every GCP/AWS/Azure log surface:
// the Cloud Logs tab (CloudLogsViewer), the investigation evidence card
// (CloudLog), and the trace-correlated logs panel (CloudTraceLogs). Callers own
// their own fetching, toolbar, and empty/error states — this component only maps
// a normalized log list to the CustomTable (severity bar + timestamp + message,
// or dynamic attribute columns for message-less records, with expandable labels).
export interface LogEntry {
  timestamp: string;
  message: string;
  severity: string;
  labels: Record<string, any>;
}

const SEVERITY_COLORS: Record<string, string> = {
  error: ds.red[500],
  critical: ds.red[700],
  fatal: ds.red[700],
  warning: ds.amber[500],
  warn: ds.amber[500],
  info: ds.blue[500],
  debug: ds.gray[400],
  notice: ds.green[500],
};

const MAX_DYNAMIC_COLUMNS = 5;
const LONG_VALUE_THRESHOLD = 80;

function getSeverityColor(severity: string): string {
  if (!severity) {
    return ds.gray[400];
  }
  return SEVERITY_COLORS[severity.toLowerCase()] || ds.gray[400];
}

function isUsefulValue(value: any): boolean {
  return value !== undefined && value !== null && value !== '' && value !== '<nil>';
}

const CopyableValue = ({ value }: { value: string }) => {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    // navigator.clipboard is undefined in non-secure (HTTP) contexts; guard so a copy click can't crash.
    if (!navigator.clipboard?.writeText) {
      return;
    }
    navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {});
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], minWidth: 0 }}>
      <Typography
        sx={{
          fontSize: ds.text.small,
          fontFamily: 'monospace',
          color: ds.gray[600],
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          maxWidth: ds.space.mul(0, 160),
        }}
        title={value}
      >
        {value}
      </Typography>
      <DsButton
        tone='ghost'
        size='xs'
        composition='icon-only'
        icon={<ContentCopyIcon fontSize='small' sx={{ color: copied ? ds.green[600] : undefined }} />}
        aria-label='Copy value'
        tooltip={copied ? 'Copied!' : 'Copy'}
        onClick={handleCopy}
      />
    </Box>
  );
};

const LogExpandedRow = ({ row }: { row: any[] }) => {
  const labels: Record<string, any> = row?.[row.length - 1]?._labels || {};
  const entries = Object.entries(labels).filter(([, v]) => isUsefulValue(v));

  if (entries.length === 0) {
    return (
      <Box p={ds.space[3]}>
        <Typography variant='body2' sx={{ color: ds.gray[500] }}>
          No additional details
        </Typography>
      </Box>
    );
  }

  return (
    <Box
      sx={{
        p: ds.space[3],
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
        gap: ds.space[2],
      }}
    >
      {entries.map(([key, value]) => {
        const strValue = String(value);
        const isLong = strValue.length > LONG_VALUE_THRESHOLD || key === '@ptr';

        return (
          <Box
            key={key}
            sx={{
              display: 'flex',
              gap: ds.space[2],
              alignItems: 'baseline',
              py: ds.space[1],
              borderBottom: `1px solid ${ds.gray[200]}`,
            }}
          >
            <Box sx={{ flexShrink: 0 }}>
              <DsChip variant='tag' tone='neutral' size='xs'>
                {key}
              </DsChip>
            </Box>
            {isLong ? (
              <CopyableValue value={strValue} />
            ) : (
              <Typography
                sx={{
                  fontSize: ds.text.small,
                  fontFamily: 'monospace',
                  wordBreak: 'break-all',
                  color: ds.gray[600],
                }}
              >
                {strValue}
              </Typography>
            )}
          </Box>
        );
      })}
    </Box>
  );
};

interface CloudLogsTableProps {
  logs: LogEntry[];
  // Unique per surface so the download button / multiple cards on one page don't collide.
  id?: string;
  loading?: boolean;
}

// Untyped JS callers (CloudLog.js, CloudTraceLogs.jsx) can pass a non-string message
// for a structured log; stringify it so it never reaches React as an object (which crashes).
const toMessageString = (message: any): string => (typeof message === 'string' ? message : message == null ? '' : JSON.stringify(message, null, 2));

const CloudLogsTable: React.FC<CloudLogsTableProps> = ({ logs = [], id = 'cloudLogsTable', loading = false }) => {
  const hasMessages = useMemo(() => logs.some((log) => !!log.message), [logs]);

  const dynamicLabelKeys = useMemo(() => {
    if (hasMessages || logs.length === 0) {
      return [];
    }
    const keyCounts: Record<string, number> = {};
    for (const log of logs) {
      for (const [key, value] of Object.entries(log.labels || {})) {
        if (isUsefulValue(value)) {
          keyCounts[key] = (keyCounts[key] || 0) + 1;
        }
      }
    }
    return Object.entries(keyCounts)
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([key]) => key)
      .slice(0, MAX_DYNAMIC_COLUMNS);
  }, [logs, hasMessages]);

  const hasLabels = useMemo(() => logs.some((log) => Object.keys(log.labels || {}).length > 0), [logs]);
  const useDynamicColumns = !hasMessages && dynamicLabelKeys.length > 0;

  const tableHeaders = useMemo(() => {
    const headers: { name: string; width: string }[] = [{ name: 'Timestamp', width: '160px' }];
    if (useDynamicColumns) {
      for (const key of dynamicLabelKeys) {
        headers.push({ name: key, width: 'auto' });
      }
    } else {
      headers.push({ name: 'Message', width: '90%' });
    }
    return headers;
  }, [useDynamicColumns, dynamicLabelKeys]);

  const logTableData = useMemo(() => {
    return logs.map((log) => {
      const severity = log.severity || '';
      const timestampCell = {
        // `whiteSpace: 'nowrap'` prevents `table-layout: auto` from shrinking
        // this column to min-content ("7h") on wide tables, which would wrap
        // "7h 12m ago" across multiple lines.
        text: (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], whiteSpace: 'nowrap' }}>
            <Box
              sx={{
                width: 3,
                height: ds.space[5],
                borderRadius: ds.radius.sm,
                bgcolor: getSeverityColor(severity),
                flexShrink: 0,
              }}
            />
            <Datetime value={log.timestamp} />
          </Box>
        ),
      };

      if (useDynamicColumns) {
        const labelCells = dynamicLabelKeys.map((key) => {
          const value = log.labels?.[key];
          return {
            text: (
              <Typography
                sx={{
                  fontSize: ds.text.small,
                  fontFamily: 'monospace',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  maxWidth: ds.space.mul(0, 150),
                }}
                title={isUsefulValue(value) ? String(value) : ''}
              >
                {isUsefulValue(value) ? String(value) : '-'}
              </Typography>
            ),
            data: isUsefulValue(value) ? String(value) : '',
          };
        });
        // Attach labels to the last cell so LogExpandedRow can read them.
        const lastLabel = labelCells[labelCells.length - 1];
        return [timestampCell, ...labelCells.slice(0, -1), { ...lastLabel, _labels: log.labels || {} }];
      }

      const messageCell = {
        text: (
          <Typography
            component='pre'
            sx={{
              fontSize: ds.text.small,
              fontFamily: 'monospace',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              m: 0,
              maxHeight: ds.space.mul(0, 100),
              overflow: 'auto',
            }}
          >
            {toMessageString(log.message)}
          </Typography>
        ),
        _labels: log.labels || {},
      };

      return [timestampCell, messageCell];
    });
  }, [logs, useDynamicColumns, dynamicLabelKeys]);

  const hasRows = logTableData.length > 0;

  return (
    <CustomTable
      id={id}
      headers={tableHeaders}
      tableData={logTableData}
      rowsPerPage={hasRows ? logTableData.length : 5}
      loading={loading}
      showExpandable={hasLabels}
      expandable={{ component: LogExpandedRow }}
    />
  );
};

export default CloudLogsTable;
