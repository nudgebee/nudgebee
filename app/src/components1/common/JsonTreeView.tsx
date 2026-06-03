import React, { useState, useCallback, useMemo } from 'react';
import { Box, IconButton, Tooltip } from '@mui/material';
import { ContentCopy } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

interface JsonTreeViewProps {
  readonly data: unknown;
  readonly defaultExpanded?: number; // depth to auto-expand (default: 2)
  readonly maxHeight?: string;
  readonly fontSize?: string;
  readonly bare?: boolean; // render without outer border/padding/background (for embedding)
  readonly showCopy?: boolean; // override copy-button visibility (default: !bare)
  readonly templatePrefix?: string; // e.g. "Tasks['core_print'].output" — enables key tooltips with template expressions
}

const FONT_FAMILY = "'JetBrains Mono', 'Fira Code', monospace";

// Syntax colors (light theme)
const SYN = {
  key: 'var(--ds-purple-600)',
  string: 'var(--ds-green-600)',
  number: 'var(--ds-amber-600)',
  boolean: 'var(--ds-blue-600)',
  null: 'var(--ds-gray-500)',
  brace: 'var(--ds-gray-600)',
  preview: 'var(--ds-gray-500)',
  guide: 'var(--ds-gray-200)',
  toggle: 'var(--ds-gray-500)',
  toggleHover: 'var(--ds-blue-600)',
} as const;

function copyText(text: string) {
  navigator.clipboard.writeText(text).catch((err) => {
    console.error('Failed to copy text: ', err);
  });
}

// ─── Key with template tooltip ───
function KeyLabel({ keyName, path, templatePrefix }: Readonly<{ keyName: string | number; path: string; templatePrefix?: string }>) {
  const [copied, setCopied] = useState(false);

  const displayKey = typeof keyName === 'number' ? keyName : `"${keyName}"`;

  if (!templatePrefix || typeof keyName === 'number') {
    return (
      <>
        <span style={{ color: SYN.key, fontWeight: 'var(--ds-font-weight-medium)' }}>{displayKey}</span>
        <span style={{ color: SYN.brace }}>: </span>
      </>
    );
  }

  const templateExpr = `{{ ${templatePrefix}.${path} }}`;

  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation();
    copyText(templateExpr);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <>
      <Tooltip
        title={
          <Box sx={{ p: 1, maxWidth: 400 }}>
            <Box sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-600)', mb: 0.5 }}>
              Reference this field
            </Box>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-brand-400)', mb: 1, lineHeight: 1.4 }}>
              Use this template expression to access the value of <strong style={{ color: 'var(--ds-purple-600)' }}>{String(keyName)}</strong> in
              subsequent workflow steps.
            </Box>
            <Box
              onClick={handleCopy}
              sx={{
                fontFamily: FONT_FAMILY,
                fontSize: '11.5px',
                bgcolor: 'var(--ds-background-300)',
                color: 'var(--ds-brand-500)',
                px: 1.5,
                py: 1,
                borderRadius: 'var(--ds-radius-md)',
                border: '1px solid var(--ds-brand-150)',
                cursor: 'pointer',
                wordBreak: 'break-all',
                lineHeight: 1.5,
                '&:hover': { bgcolor: 'var(--ds-brand-150)', borderColor: 'var(--ds-brand-200)' },
              }}
            >
              {templateExpr}
            </Box>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: copied ? '#16a34a' : '#94a3b8', mt: 0.75, fontWeight: copied ? 600 : 400 }}>
              {copied ? 'Copied to clipboard!' : 'Click the expression to copy'}
            </Box>
          </Box>
        }
        placement='top'
        arrow
        enterDelay={300}
        leaveDelay={100}
        slotProps={{
          tooltip: {
            sx: {
              bgcolor: 'var(--ds-background-100)',
              color: 'var(--ds-brand-600)',
              boxShadow: '0 4px 20px rgba(0,0,0,0.12), 0 1px 4px rgba(0,0,0,0.08)',
              border: '1px solid var(--ds-brand-150)',
              borderRadius: 'var(--ds-radius-lg)',
              '& .MuiTooltip-arrow': { color: 'var(--ds-background-100)', '&::before': { border: '1px solid var(--ds-brand-150)' } },
              maxWidth: 420,
            },
          },
        }}
      >
        <span style={{ color: SYN.key, fontWeight: 'var(--ds-font-weight-medium)', cursor: 'pointer', borderBottom: '1px dashed #5c21a544' }}>
          {displayKey}
        </span>
      </Tooltip>
      <span style={{ color: SYN.brace }}>: </span>
    </>
  );
}

// ─── Leaf value renderer ───
function JsonValue({ value }: Readonly<{ value: string | number | boolean | null }>) {
  if (value === null) return <span style={{ color: SYN.null, fontStyle: 'italic' }}>null</span>;
  if (typeof value === 'boolean') return <span style={{ color: SYN.boolean }}>{String(value)}</span>;
  if (typeof value === 'number') return <span style={{ color: SYN.number }}>{value}</span>;
  return <span style={{ color: SYN.string }}>&quot;{String(value)}&quot;</span>;
}

// ─── Toggle button ───
function Toggle({ expanded, onClick }: Readonly<{ expanded: boolean; onClick: () => void }>) {
  return (
    <button
      type='button'
      onClick={onClick}
      aria-label={expanded ? 'Collapse' : 'Expand'}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: 16,
        height: 16,
        cursor: 'pointer',
        borderRadius: 3,
        fontSize: 10,
        marginRight: 4,
        verticalAlign: 'middle',
        color: SYN.toggle,
        border: '1px solid var(--ds-gray-300)',
        background: 'var(--ds-background-100)',
        userSelect: 'none',
        flexShrink: 0,
        padding: 0,
        lineHeight: 1,
      }}
      onMouseEnter={(e) => {
        (e.target as HTMLButtonElement).style.color = SYN.toggleHover;
        (e.target as HTMLButtonElement).style.borderColor = '#90caf9';
        (e.target as HTMLButtonElement).style.background = '#e3f2fd';
      }}
      onMouseLeave={(e) => {
        (e.target as HTMLButtonElement).style.color = SYN.toggle;
        (e.target as HTMLButtonElement).style.borderColor = '#ddd';
        (e.target as HTMLButtonElement).style.background = '#fff';
      }}
    >
      {expanded ? '\u25BC' : '\u25B6'}
    </button>
  );
}

// ─── Collapsed preview ───
function collapsedPreview(data: Record<string, unknown> | unknown[]): string {
  const isArr = Array.isArray(data);
  const count = isArr ? data.length : Object.keys(data).length;
  if (isArr) return `[ ${count} item${count !== 1 ? 's' : ''} ]`;
  const keys = Object.keys(data).slice(0, 3).join(', ');
  const suffix = Object.keys(data).length > 3 ? ', ...' : '';
  return `{ ${keys}${suffix} }`;
}

// ─── Build the dot-path for a key ───
function buildPath(parentPath: string, key: string | number): string {
  if (typeof key === 'number') return parentPath ? `${parentPath}[${key}]` : `[${key}]`;
  return parentPath ? `${parentPath}.${key}` : key;
}

// ─── Try parsing a string as JSON, returns parsed object or null ───
function tryParseJsonString(value: unknown): object | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  if ((!trimmed.startsWith('{') || !trimmed.endsWith('}')) && (!trimmed.startsWith('[') || !trimmed.endsWith(']'))) {
    return null;
  }
  try {
    const parsed = JSON.parse(trimmed);
    return typeof parsed === 'object' && parsed !== null ? parsed : null;
  } catch {
    return null;
  }
}

// ─── Render primitive leaf node ───
function PrimitiveNode({ keyLabel, value, comma }: Readonly<{ keyLabel: React.ReactNode; value: string | number | boolean | null; comma: string }>) {
  return (
    <div style={{ paddingLeft: 4, lineHeight: 1.7 }}>
      {keyLabel}
      <JsonValue value={value} />
      {comma}
    </div>
  );
}

interface JsonNodeProps {
  readonly keyName?: string | number;
  readonly value: unknown;
  readonly depth: number;
  readonly defaultExpanded: number;
  readonly isLast: boolean;
  readonly parentPath: string;
  readonly templatePrefix?: string;
}

// ─── Recursive node ───
function JsonNode({ keyName, value, depth, defaultExpanded, isLast, parentPath, templatePrefix }: JsonNodeProps) {
  const [expanded, setExpanded] = useState(depth < defaultExpanded);
  const toggle = useCallback(() => setExpanded((e) => !e), []);
  const comma = isLast ? '' : ',';

  const currentPath = keyName !== undefined ? buildPath(parentPath, keyName) : parentPath;
  const keyLabel = keyName !== undefined ? <KeyLabel keyName={keyName} path={currentPath} templatePrefix={templatePrefix} /> : null;

  // Auto-parse stringified JSON strings into expandable trees
  const parsedString = tryParseJsonString(value);
  if (parsedString) {
    return (
      <JsonNode
        keyName={keyName}
        value={parsedString}
        depth={depth}
        defaultExpanded={defaultExpanded}
        isLast={isLast}
        parentPath={parentPath}
        templatePrefix={templatePrefix}
      />
    );
  }

  // Primitive values
  if (value === null || typeof value !== 'object') {
    return <PrimitiveNode keyLabel={keyLabel} value={value as string | number | boolean | null} comma={comma} />;
  }

  // Object / Array
  const isArr = Array.isArray(value);
  const entries: [string | number, unknown][] = isArr ? value.map((v, i) => [i, v]) : Object.entries(value as Record<string, unknown>);
  const open = isArr ? '[' : '{';
  const close = isArr ? ']' : '}';

  if (entries.length === 0) {
    return (
      <div style={{ paddingLeft: 4, lineHeight: 1.7 }}>
        {keyLabel}
        <span style={{ color: SYN.brace }}>
          {open}
          {close}
        </span>
        {comma}
      </div>
    );
  }

  return (
    <div style={{ paddingLeft: 4, lineHeight: 1.7 }}>
      <div>
        <Toggle expanded={expanded} onClick={toggle} />
        {keyLabel}
        <span style={{ color: SYN.brace }}>{open}</span>
        {!expanded && (
          <>
            <button
              type='button'
              onClick={toggle}
              aria-label='Expand'
              style={{
                color: SYN.preview,
                fontStyle: 'italic',
                fontSize: '0.9em',
                cursor: 'pointer',
                background: 'none',
                border: 'none',
                padding: 0,
                font: 'inherit',
              }}
            >
              {' '}
              {collapsedPreview(value as Record<string, unknown> | unknown[])}{' '}
            </button>
            <span style={{ color: SYN.brace }}>{close}</span>
            {comma}
          </>
        )}
      </div>
      {expanded && (
        <>
          <div style={{ paddingLeft: 20, borderLeft: `1px solid ${SYN.guide}`, marginLeft: 7 }}>
            {entries.map(([k, v], i) => (
              <JsonNode
                key={String(k)}
                keyName={k}
                value={v}
                depth={depth + 1}
                defaultExpanded={defaultExpanded}
                isLast={i === entries.length - 1}
                parentPath={currentPath}
                templatePrefix={templatePrefix}
              />
            ))}
          </div>
          <div style={{ paddingLeft: 4 }}>
            <span style={{ color: SYN.brace }}>{close}</span>
            {comma}
          </div>
        </>
      )}
    </div>
  );
}

// ─── Main component ───
export default function JsonTreeView({
  data,
  defaultExpanded = 2,
  maxHeight = '400px',
  fontSize = '12px',
  bare = false,
  showCopy,
  templatePrefix,
}: JsonTreeViewProps) {
  const copyVisible = showCopy ?? !bare;
  const [copied, setCopied] = useState(false);

  const jsonString = useMemo(() => {
    if (typeof data === 'string') return data;
    try {
      return JSON.stringify(data, null, 2);
    } catch {
      return String(data);
    }
  }, [data]);

  const handleCopy = useCallback(() => {
    copyText(jsonString);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }, [jsonString]);

  // If data is a plain string (not JSON object), just render it
  const parsedData = useMemo(() => {
    if (typeof data === 'string') {
      try {
        return JSON.parse(data);
      } catch {
        return null; // not valid JSON, render as-is
      }
    }
    return data;
  }, [data]);

  if (parsedData === null || parsedData === undefined || typeof parsedData !== 'object') {
    return (
      <Box
        sx={{
          fontFamily: FONT_FAMILY,
          fontSize,
          color: colors.text.secondary,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          lineHeight: 1.6,
          maxHeight,
          overflowY: 'auto',
          position: 'relative',
        }}
      >
        {typeof data === 'string' ? data : JSON.stringify(data, null, 2)}
      </Box>
    );
  }

  return (
    <Box
      sx={{
        position: 'relative',
        fontFamily: FONT_FAMILY,
        fontSize,
        lineHeight: 1.6,
        maxHeight,
        overflowY: 'auto',
        ...(bare
          ? {}
          : {
              background: colors.background.tertiaryLightestestest,
              border: `1px solid ${colors.border.secondaryLight}`,
              borderRadius: 'var(--ds-radius-lg)',
              padding: 'var(--ds-space-3)',
            }),
      }}
    >
      {copyVisible && (
        <Tooltip title={copied ? 'Copied!' : 'Copy JSON'} placement='top'>
          <IconButton
            className='json-copy-btn'
            size='small'
            onClick={handleCopy}
            data-testid='json-copy-btn'
            sx={{
              position: 'absolute',
              top: bare ? -2 : 6,
              right: bare ? -2 : 6,
              color: 'var(--ds-gray-600)',
              background: 'var(--ds-background-100)',
              border: '1px solid var(--ds-brand-150)',
              '&:hover': { background: 'var(--ds-background-300)' },
              zIndex: 1,
              padding: 'var(--ds-space-1)',
            }}
          >
            <ContentCopy sx={{ fontSize: 'var(--ds-text-body-lg)' }} />
          </IconButton>
        </Tooltip>
      )}
      <JsonNode value={parsedData} depth={0} defaultExpanded={defaultExpanded} isLast parentPath='' templatePrefix={templatePrefix} />
    </Box>
  );
}
