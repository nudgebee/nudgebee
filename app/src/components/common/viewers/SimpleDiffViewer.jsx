import React, { useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Button, Typography } from '@mui/material';
import { ChevronRight, ExpandMore, Code } from '@mui/icons-material';

/**
 * Preprocess escaped characters in diff string
 */
const preprocessDiff = (diff) => {
  if (typeof diff === 'string' && diff.includes('\\n')) {
    return diff.replace(/\\n/g, '\n').replace(/\\"/g, '"');
  }
  return diff;
};

/**
 * Extract file name from diff header
 */
const extractFileName = (lines, defaultName) => {
  for (const line of lines) {
    if (line.startsWith('diff --git')) {
      const match = line.match(/diff --git a\/(.*?) b\//);
      if (match?.[1]) {
        return match[1];
      }
    }
  }
  return defaultName;
};

/**
 * Check if line is metadata that should be skipped
 */
const isMetadataLine = (line) => {
  return line.startsWith('diff --git') || line.startsWith('index ') || line.startsWith('---') || line.startsWith('+++');
};

/**
 * Parse hunk header line
 */
const parseHunkHeader = (line) => {
  const match = line.match(/@@ -(\d+),?\d* \+(\d+),?\d* @@(.*)/);
  if (match) {
    return {
      type: 'hunk',
      oldLine: parseInt(match[1]),
      newLine: parseInt(match[2]),
      context: match[3] || '',
      content: line,
    };
  }
  return null;
};

/**
 * Parse content line (addition, deletion, or context)
 */
const parseContentLine = (line) => {
  if (line.startsWith('+')) {
    return { type: 'add', content: line.substring(1), marker: '+', isAddition: true };
  }
  if (line.startsWith('-')) {
    return { type: 'delete', content: line.substring(1), marker: '-', isDeletion: true };
  }
  if (line.startsWith(' ')) {
    return { type: 'context', content: line.substring(1), marker: ' ' };
  }
  if (line.trim()) {
    return { type: 'context', content: line, marker: ' ' };
  }
  return null;
};

/**
 * Process a single line and update counters
 */
const processSingleLine = (line, lineId, stats) => {
  if (isMetadataLine(line)) {
    return null;
  }

  if (line.startsWith('@@')) {
    const hunk = parseHunkHeader(line);
    return hunk ? { ...hunk, id: `hunk-${lineId}` } : null;
  }

  const parsedLine = parseContentLine(line);
  if (parsedLine) {
    if (parsedLine.isAddition) {
      stats.additions++;
    }
    if (parsedLine.isDeletion) {
      stats.deletions++;
    }
    return { ...parsedLine, id: `line-${lineId}` };
  }

  return null;
};

/**
 * Process all diff lines and count additions/deletions
 */
const processLines = (lines) => {
  const diffLines = [];
  const stats = { additions: 0, deletions: 0 };
  let lineId = 0;

  for (const line of lines) {
    const processedLine = processSingleLine(line, lineId, stats);
    if (processedLine) {
      diffLines.push(processedLine);
      lineId++;
    }
  }

  return { diffLines, additions: stats.additions, deletions: stats.deletions };
};

/**
 * SimpleDiffViewer - A simple, reliable diff viewer that parses unified diff format
 *
 * Note: This component intentionally does not implement syntax highlighting to keep
 * it lightweight and dependency-free. It focuses on clear visual distinction between
 * additions/deletions using color coding.
 *
 * @param {Object} props
 * @param {string} props.gitDiff - Git diff string in unified diff format
 * @param {string} props.fileName - Default file name (overridden if extracted from diff)
 * @param {boolean} props.defaultExpanded - Whether to show diff expanded by default
 * @param {string} props.title - Header title text
 * @param {boolean} props.showHeader - Whether to show the collapsible header
 */
const SimpleDiffViewer = ({ gitDiff, fileName = 'code', defaultExpanded = true, title = 'Code Changes', showHeader = true }) => {
  const [expanded, setExpanded] = useState(defaultExpanded);

  const parsedDiff = useMemo(() => {
    if (!gitDiff) {
      return null;
    }

    const processedDiff = preprocessDiff(gitDiff);
    const lines = processedDiff.split('\n');
    const extractedFileName = extractFileName(lines, fileName);
    const { diffLines, additions, deletions } = processLines(lines);

    return {
      fileName: extractedFileName,
      additions,
      deletions,
      lines: diffLines,
    };
  }, [gitDiff, fileName]);

  if (!parsedDiff) {
    return (
      <Box sx={{ padding: 2, border: '1px solid var(--ds-gray-300)', borderRadius: 1 }}>
        <Typography color='error'>No diff data provided</Typography>
      </Box>
    );
  }

  const getLineStyle = (type) => {
    switch (type) {
      case 'add':
        return {
          backgroundColor: 'var(--ds-green-200)',
          borderLeft: '3px solid var(--ds-green-500)',
        };
      case 'delete':
        return {
          backgroundColor: 'var(--ds-red-200)',
          borderLeft: '3px solid var(--ds-red-500)',
        };
      case 'hunk':
        return {
          backgroundColor: 'var(--ds-gray-100)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: 'var(--ds-gray-600)',
        };
      default:
        return {
          backgroundColor: 'var(--ds-background-100)',
        };
    }
  };

  return (
    <Box
      sx={{
        marginY: 2,
        border: '1px solid var(--ds-gray-300)',
        borderRadius: 1,
        overflow: 'hidden',
      }}
    >
      {showHeader && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            padding: 'var(--ds-space-2) var(--ds-space-4)',
            backgroundColor: 'var(--ds-background-300)',
            borderBottom: expanded ? '1px solid #e0e0e0' : 'none',
            cursor: 'pointer',
          }}
          onClick={() => setExpanded(!expanded)}
        >
          <Button
            variant='ghost'
            size='small'
            sx={{
              minWidth: 'auto',
              padding: 'var(--ds-space-1)',
              marginRight: 1,
            }}
            onClick={(e) => {
              e.stopPropagation();
              setExpanded(!expanded);
            }}
          >
            {expanded ? <ExpandMore sx={{ fontSize: 16 }} /> : <ChevronRight sx={{ fontSize: 16 }} />}
          </Button>

          <Code sx={{ fontSize: 14, marginRight: 'var(--ds-space-2)' }} />

          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontFamily: 'monospace',
              flex: 1,
              color: 'var(--ds-gray-600)',
            }}
          >
            {parsedDiff.fileName || title}
          </Typography>

          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              marginLeft: 2,
              color: 'var(--ds-green-500)',
              fontFamily: 'monospace',
            }}
          >
            +{parsedDiff.additions}
          </Typography>

          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              marginLeft: 1,
              color: 'var(--ds-red-500)',
              fontFamily: 'monospace',
            }}
          >
            -{parsedDiff.deletions}
          </Typography>
        </Box>
      )}

      {expanded && (
        <Box
          sx={{
            fontFamily: 'monospace',
            fontSize: 'var(--ds-text-small)',
            overflow: 'auto',
          }}
        >
          {parsedDiff.lines.map((line) => (
            <Box
              key={line.id}
              sx={{
                display: 'flex',
                padding: 'var(--ds-space-1) 0',
                ...getLineStyle(line.type),
              }}
            >
              {line.type !== 'hunk' && (
                <Box
                  component='span'
                  sx={{
                    minWidth: '40px',
                    paddingX: 1,
                    color: 'var(--ds-gray-600)',
                    textAlign: 'right',
                    userSelect: 'none',
                  }}
                >
                  {line.marker}
                </Box>
              )}
              <Box
                component='span'
                sx={{
                  flex: 1,
                  paddingX: 2,
                  whiteSpace: 'pre',
                  overflow: 'auto',
                }}
              >
                {line.content}
              </Box>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  );
};

SimpleDiffViewer.propTypes = {
  gitDiff: PropTypes.string.isRequired,
  fileName: PropTypes.string,
  defaultExpanded: PropTypes.bool,
  title: PropTypes.string,
  showHeader: PropTypes.bool,
};

export default SimpleDiffViewer;
