import { useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Collapse, Typography } from '@mui/material';
import { KeyboardArrowDown, KeyboardArrowRight } from '@mui/icons-material';
import Datetime from '@shared/format/Datetime';

// prettyValue pretty-prints a JSON-ish string (object/array payloads logged as a
// string) so the expanded "Sample" preserves structure; otherwise returns as-is.
const prettyValue = (value) => {
  if (typeof value !== 'string') {
    // Defensive: a non-string payload (object/array) must not reach React as-is.
    return value && typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value);
  }
  const trimmed = value.trim();
  if ((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2);
    } catch {
      return value;
    }
  }
  return value;
};

const isPresent = (value) => value !== null && value !== undefined;

const hasText = (message) => isPresent(message) && message !== '' && message !== 'null';

const parseJSONorNull = (value) => {
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  if (!((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']')))) {
    return null;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    return null;
  }
};

// fieldEntries returns the structured attributes as a stable, non-empty key/value list.
const fieldEntries = (attributes) => {
  if (!attributes || typeof attributes !== 'object') {
    return [];
  }
  return Object.entries(attributes)
    .filter(([, value]) => value !== null && value !== undefined && value !== '')
    .sort(([a], [b]) => a.localeCompare(b));
};

// summaryText normalizes every log type to one clean line so the collapsed list
// reads consistently (instead of mixing raw JSON blobs and key=value strings):
//   - structured JSON log  -> its inner message/msg/body/error field
//   - plain text log       -> the text as-is
//   - HTTP request log     -> "METHOD url -> status" (standard OTel http fields)
//   - anything else        -> generic key=value of all attributes (no hand-picking)
// The full raw payload is always available in the expanded view, so nothing is lost.
const MESSAGE_KEYS = ['message', 'msg', 'body', 'error', 'event', 'log'];

const summaryText = (entry) => {
  if (hasText(entry.message)) {
    const parsed = parseJSONorNull(entry.message);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const key = MESSAGE_KEYS.find((k) => isPresent(parsed[k]) && parsed[k] !== '');
      if (key) {
        return typeof parsed[key] === 'string' ? parsed[key] : JSON.stringify(parsed[key]);
      }
    }
    // Always return a string — guards against a non-string message reaching React.
    return typeof entry.message === 'string' ? entry.message : JSON.stringify(entry.message);
  }

  const attributes = entry.attributes || {};
  const method = attributes['http.request.method'];
  const url = attributes['url.full'] || attributes['url.path'] || attributes['url.template'];
  const status = attributes['http.response.status_code'];
  if (method || url || isPresent(status)) {
    return [method, url, isPresent(status) ? `-> ${status}` : ''].filter(Boolean).join(' ');
  }

  return fieldEntries(attributes)
    .map(([key, value]) => `${key}=${typeof value === 'object' ? JSON.stringify(value) : value}`)
    .join('   ');
};

const monospaceSx = {
  fontFamily: 'var(--ds-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, "Courier New", monospace)',
  fontSize: 'var(--ds-text-body-sm)',
};

const LogEntry = ({ entry }) => {
  const [open, setOpen] = useState(false);
  const fields = fieldEntries(entry.attributes);
  const messageBody = hasText(entry.message) ? prettyValue(entry.message) : '';

  return (
    <Box sx={{ borderBottom: '1px solid var(--ds-gray-200)' }}>
      <Box
        onClick={() => setOpen((o) => !o)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
          cursor: 'pointer',
          py: 'var(--ds-space-2)',
          '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
        }}
      >
        {open ? (
          <KeyboardArrowDown sx={{ fontSize: 18, color: 'var(--ds-gray-500)' }} />
        ) : (
          <KeyboardArrowRight sx={{ fontSize: 18, color: 'var(--ds-gray-500)' }} />
        )}
        <Box sx={{ minWidth: 96, flexShrink: 0, color: 'var(--ds-gray-500)' }}>
          <Datetime value={entry.timestamp} />
        </Box>
        <Typography
          sx={{
            ...monospaceSx,
            color: 'var(--ds-gray-700)',
            flex: 1,
            minWidth: 0,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {summaryText(entry)}
        </Typography>
      </Box>

      <Collapse in={open} unmountOnExit>
        <Box sx={{ pb: 'var(--ds-space-3)', pl: 'var(--ds-space-6)', pr: 'var(--ds-space-2)' }}>
          {messageBody && (
            <Box
              component='pre'
              sx={{
                ...monospaceSx,
                m: 0,
                mb: fields.length ? 'var(--ds-space-3)' : 0,
                p: 'var(--ds-space-3)',
                backgroundColor: 'var(--ds-gray-100)',
                border: '1px solid var(--ds-gray-200)',
                borderRadius: 'var(--ds-radius-sm)',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                color: 'var(--ds-gray-800)',
              }}
            >
              {messageBody}
            </Box>
          )}

          {fields.length > 0 && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              {fields.map(([key, value]) => (
                <Box key={key} sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'baseline' }}>
                  <Typography
                    component='span'
                    sx={{ ...monospaceSx, color: 'var(--ds-gray-500)', whiteSpace: 'nowrap', minWidth: 220, flexShrink: 0 }}
                  >
                    {key}
                  </Typography>
                  <Typography component='span' sx={{ ...monospaceSx, color: 'var(--ds-gray-800)', wordBreak: 'break-all' }}>
                    {typeof value === 'object' ? JSON.stringify(value) : String(value)}
                  </Typography>
                </Box>
              ))}
            </Box>
          )}
        </Box>
      </Collapse>
    </Box>
  );
};

LogEntry.propTypes = {
  entry: PropTypes.object.isRequired,
};

const CloudLogViewer = ({ logs = [] }) => {
  if (!Array.isArray(logs) || logs.length === 0) {
    return null;
  }
  return (
    <Box>
      {logs.map((entry, index) => (
        <LogEntry key={index} entry={entry} />
      ))}
    </Box>
  );
};

CloudLogViewer.propTypes = {
  logs: PropTypes.array,
};

export default CloudLogViewer;
