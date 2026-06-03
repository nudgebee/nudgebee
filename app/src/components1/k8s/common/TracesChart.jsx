import React, { useState } from 'react';
import { Box, Typography, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, IconButton } from '@mui/material';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Modal } from '@components1/ds/Modal';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { formatDurationInTrace, parseJSONSafely, safeJSONParse } from 'src/utils/common';
import { Card } from '@components1/ds/Card';
import { Skeleton } from '@components1/ds/Skeleton';
import StarIcon from '@mui/icons-material/Star';
import Text from '@common-new/format/Text';
import { ds } from 'src/utils/colors';

function convertSpansToTree(spans = []) {
  if (!Array.isArray(spans) || spans.length === 0) {
    return null;
  }

  const parsedSpans = spans
    .map((span) => {
      if (!span?.timestamp || !span?.duration_ns || !span?.span_name) {
        return null;
      }
      const start = new Date(span.timestamp);
      const parsedSpanAttributes = (() => {
        try {
          return JSON.parse(span.span_attributes);
        } catch (e) {
          console.warn('Failed to parse span attributes', span.span_id, span.span_attributes, e);
          return {};
        }
      })();
      return {
        id: span.span_id || Math.random().toString(36),
        traceId: span.trace_id,
        name: span.span_name,
        start,
        end: new Date(start.getTime() + span.duration_ns / 1e6),
        duration: span.duration_ns,
        children: [],
        entireObject: span,
        status_code: span.status_code,
        method: parsedSpanAttributes?.['http.method'] || '',
      };
    })
    .filter(Boolean); // Remove nulls

  if (parsedSpans.length === 0) {
    return null;
  }

  parsedSpans.sort((a, b) => a.start - b.start);

  const root = parsedSpans[0];
  const stack = [root];

  for (let i = 1; i < parsedSpans.length; i++) {
    const current = parsedSpans[i];
    while (stack.length > 0) {
      const parent = stack[stack.length - 1];
      if (current.start >= parent.start && current.end <= parent.end) {
        parent.children.push(current);
        stack.push(current);
        break;
      } else {
        stack.pop();
      }
    }
    if (stack.length === 0) {
      root.children.push(current);
      stack.push(current);
    }
  }

  return root;
}

const formatTime = (date) => {
  return `${date.getHours().toString().padStart(2, '0')}:${date.getMinutes().toString().padStart(2, '0')}:${date
    .getSeconds()
    .toString()
    .padStart(2, '0')}.${date.getMilliseconds().toString().padStart(3, '0')}`;
};

const TraceNode = ({ trace, level = 0, timeScale, startTimeMs, ganttWidth, randomColors, onToggleExpand, expandedNodes }) => {
  const [selectedTrace, setSelectedTrace] = useState(null);
  const [showModal, setShowModal] = useState(false);

  if (!trace || !trace.start || !trace.end) {
    return null;
  }
  const children = trace.children || [];
  const hasChildren = children.length > 0;
  const isExpanded = expandedNodes[trace.id] !== false;

  const toggleExpand = () => {
    onToggleExpand(trace.id, !isExpanded);
  };

  const calculatePosition = (date) => {
    return ((date.getTime() - startTimeMs) / timeScale) * ganttWidth;
  };

  const barStart = calculatePosition(trace.start);
  const barWidth = calculatePosition(trace.end) - barStart;

  const getBarColor = (level) => {
    const colors = randomColors;
    return colors[level % colors.length];
  };

  const handleBarClick = (trace) => {
    const entireTrace = trace?.entireObject || {};
    if (Object.keys(entireTrace).length > 0) {
      const jsonParsed = Object.entries(entireTrace).reduce((acc, [key, value]) => {
        acc[key] = parseJSONSafely(value);
        return acc;
      }, {});
      setSelectedTrace(jsonParsed);
      setShowModal(true);
    }
  };

  const additionalComponent = () => {
    return (
      <CodeMirror
        value={JSON.stringify(selectedTrace, null, 2)}
        height='400px'
        extensions={[json(), EditorView.lineWrapping]}
        editable={false}
        style={{
          border: '1px solid var(--ds-gray-300)',
          borderRadius: 'var(--ds-radius-sm)',
          padding: 'var(--ds-space-2)',
          backgroundColor: 'var(--ds-background-300)',
          boxShadow: '0 4px 8px rgba(0, 0, 0, 0.1)',
        }}
      />
    );
  };

  const handleClose = () => {
    setShowModal(false);
    setSelectedTrace({});
  };

  const getServiceName = (data) => {
    if (data.service_name) {
      return data.service_name;
    }
    if (data.span_attributes && typeof data.span_attributes === 'string') {
      const spanAttributes = safeJSONParse(data.span_attributes) || {};
      if (spanAttributes?.['service.name']) {
        return spanAttributes['service.name'];
      }
    }
    return '';
  };

  return (
    <>
      <Modal open={showModal} handleClose={handleClose} title='Trace Details' width='md'>
        <Box sx={{ my: 'var(--ds-space-2)' }}>{additionalComponent()}</Box>
      </Modal>
      <TableRow hover sx={{ '&:hover': { backgroundColor: 'var(--ds-gray-100)' } }}>
        <TableCell
          sx={{
            pl: level * 2 + 2,
            width: ds.space.mul(0, 125),
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', pl: level * 2 + 2 }}>
            {hasChildren ? (
              <IconButton size='small' onClick={toggleExpand} sx={{ p: 0 }}>
                {isExpanded ? <KeyboardArrowDownIcon /> : <KeyboardArrowRightIcon />}
              </IconButton>
            ) : (
              <Box sx={{ width: 28, height: 28, mr: ds.space[2] }} />
            )}
            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              <Text
                value={getServiceName({ service_name: trace.entireObject.service_name, span_attributes: trace.entireObject.span_attributes })}
                showAutoEllipsis
              />
              <Text value={trace.name} secondaryText />
              {trace.method && <Text value={trace.method} secondaryText />}
            </Box>
          </Box>
        </TableCell>

        <TableCell
          sx={{ p: 0, position: 'relative', height: ds.space.mul(0, 18), width: `${ganttWidth}px`, zIndex: 5 }}
          onClick={() => handleBarClick(trace)}
        >
          {trace.status_code === 'STATUS_CODE_ERROR' && (
            <StarIcon
              fontSize='inherit'
              sx={{
                color: 'var(--ds-red-500)',
                fontSize: 'var(--ds-text-body-lg)',
                position: 'absolute',
                left: `${barStart - 12}px`,
                top: '30%',
                transform: 'translateY(-50%)',
                zIndex: 6,
              }}
            />
          )}
          <Box
            sx={{
              position: 'absolute',
              left: `${barStart}px`,
              width: `${Math.max(barWidth, 30)}px`,
              height: ds.space[5],
              top: ds.space.mul(0, 3),
              backgroundColor: getBarColor(level),
              borderRadius: 'var(--ds-radius-sm)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              zIndex: 5,
              marginLeft: 'var(--ds-space-1)',
            }}
          >
            <Typography variant='caption' sx={{ fontSize: 'var(--ds-text-caption)' }}>
              {formatDurationInTrace(trace.duration)}
            </Typography>
          </Box>
        </TableCell>
      </TableRow>

      {isExpanded &&
        hasChildren &&
        children.map((child) => (
          <TraceNode
            key={child.id}
            trace={child}
            level={level + 1}
            timeScale={timeScale}
            startTimeMs={startTimeMs}
            ganttWidth={ganttWidth}
            randomColors={randomColors}
            onToggleExpand={onToggleExpand}
            expandedNodes={expandedNodes}
          />
        ))}
    </>
  );
};

const TimelineHeader = ({ startTime, endTime, width, ganttHeight }) => {
  const duration = endTime.getTime() - startTime.getTime();
  const tickCount = 4;
  const ticks = [];

  for (let i = 0; i <= tickCount; i++) {
    const time = new Date(startTime.getTime() + (duration * i) / tickCount);
    const position = (i / tickCount) * width;
    ticks.push({ time, position });
  }

  return (
    <Box
      sx={{
        position: 'relative',
        height: ds.space.mul(0, 15),
        display: 'flex',
        width: `100%`,
        gap: 'var(--ds-space-7)',
        zIndex: 6,
        '@media screen(max-width: 1024px)': {
          gap: 'var(--ds-space-7)',
        },
        '.timeline-border': {
          position: 'relative',
          '&::before': {
            content: '""',
            position: 'absolute',
            left: 0,
            top: 0,
            width: '2px',
            backgroundColor: 'var(--ds-gray-200)',
            zIndex: 0,
            height: `calc(100% + ${ganttHeight}px)`,
          },
        },
      }}
    >
      {ticks.map((tick, i) => (
        <Box
          key={i}
          sx={{
            position: 'absolute',
            top: 0,
          }}
          className='timeline-border'
        >
          <Box sx={{ height: ds.space[1] }} />
          <Typography
            variant='caption'
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-600)',
              pl: 'var(--ds-space-1)',
              display: 'block',
              whiteSpace: 'nowrap',
            }}
          >
            {formatTime(tick.time)}
          </Typography>
        </Box>
      ))}
    </Box>
  );
};

const findTimeExtents = (trace) => {
  if (!trace || !trace.start || !trace.end) {
    return [new Date(), new Date()];
  }

  let earliestStart = trace.start;
  let latestEnd = trace.end;

  const checkChildren = (node) => {
    if (node.start < earliestStart) {
      earliestStart = node.start;
    }
    if (node.end > latestEnd) {
      latestEnd = node.end;
    }

    (node.children || []).forEach(checkChildren);
  };

  (trace.children || []).forEach(checkChildren);

  return [earliestStart, latestEnd];
};

const TraceGanttChart = ({ data = [], loading = false, randomColors = [] }) => {
  const traceData = convertSpansToTree(data);
  const [startTime, endTime] = findTimeExtents(traceData);
  const duration = endTime.getTime() - startTime.getTime();
  const [expandedNodes, setExpandedNodes] = useState({});

  const countVisibleRows = (node) => {
    if (!node) {
      return 0;
    }
    let count = 1;
    if (node.children && node.children.length > 0 && expandedNodes[node.id] !== false) {
      for (const child of node.children) {
        count += countVisibleRows(child);
      }
    }
    return count;
  };

  const handleToggleExpand = (nodeId, isExpanded) => {
    setExpandedNodes((prev) => ({
      ...prev,
      [nodeId]: isExpanded,
    }));
  };

  const rowHeight = 50;
  const headerHeight = 30;
  const visibleRows = traceData ? countVisibleRows(traceData) : 0;
  const contentHeight = visibleRows * rowHeight + headerHeight;
  const minHeight = 100;
  const maxHeight = 1500;
  const ganttHeight = Math.max(minHeight, Math.min(contentHeight, maxHeight));

  const timeScale = duration * 1.1;
  const ganttWidth = 700;

  if (loading) {
    return <Skeleton shape='rect' height={300} width='100%' />;
  }

  return (
    <Card variant='outlined' size='md' header={<Typography sx={{ fontWeight: 'bold' }}>Trace Chart</Typography>} sx={{ overflow: 'hidden' }}>
      <Box sx={{ width: '100%', overflowX: 'auto', padding: 0, borderRadius: '0 0 var(--ds-radius-xl) var(--ds-radius-xl)' }}>
        <TableContainer
          sx={{
            overflowX: 'auto',
            overflowY: 'hidden',
            maxHeight: `${ganttHeight}px`,
            '&::-webkit-scrollbar': {
              width: `${ds.space[1]} !important`,
              height: `${ds.space[1]} !important`,
            },
          }}
        >
          <Table
            stickyHeader
            aria-label='trace timeline'
            size='small'
            sx={{
              '& tbody tr td': {
                p: '0px 0px 0px var(--ds-space-1) !important',
              },
            }}
          >
            <TableHead
              sx={{
                '& th': {
                  p: '0px',
                  zIndex: 6,
                },
              }}
            >
              <TableRow>
                <TableCell sx={{ width: ds.space.mul(0, 122) }} />
                <TableCell sx={{ p: 0 }}>
                  <TimelineHeader startTime={startTime} endTime={endTime} width={ganttWidth} ganttHeight={ganttHeight} />
                </TableCell>
              </TableRow>
            </TableHead>
            <TableBody sx={{ backgroundColor: 'var(--ds-background-100)' }}>
              {traceData && (
                <TraceNode
                  trace={traceData}
                  timeScale={timeScale}
                  startTimeMs={startTime.getTime()}
                  ganttWidth={ganttWidth}
                  randomColors={randomColors}
                  onToggleExpand={handleToggleExpand}
                  expandedNodes={expandedNodes}
                />
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Box>
    </Card>
  );
};

export default TraceGanttChart;
