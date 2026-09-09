import React from 'react';
import { Box, Typography } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import Tooltip from '@ui/Tooltip';
import { Chip } from '@ui/Chip';
import MessageItem from '../MessageItem';
import { executionBatchLabel, executionBatchTone, executionBatchTooltip, parseExecutionBatchMetadata } from './executionBatch';

const taskKeyOf = (task) => String(task.tool_id ?? task.id ?? task.originalIndex ?? '');
const byCreated = (a, b) => {
  const ca = a.task?.created_at || '';
  const cb = b.task?.created_at || '';
  return ca < cb ? -1 : ca > cb ? 1 : 0;
};

const withExecutionBatchNodes = (tasks) => {
  const groups = new Map();
  const taskIds = new Set(tasks.map(taskKeyOf).filter(Boolean));
  tasks.forEach((task) => {
    if (task.nodeKind !== 'tool' && !task.isPlannerAction) {
      return;
    }
    const batch = parseExecutionBatchMetadata(task.metadata);
    if (!batch) {
      return;
    }
    const parentId = task.parentId != null ? String(task.parentId) : '';
    if (parentId && !taskIds.has(parentId)) {
      return;
    }
    const key = `${parentId}:${batch.id}`;
    const group = groups.get(key) || { batch, parentId, tasks: [] };
    group.tasks.push(task);
    groups.set(key, group);
  });

  const batchParentByTask = new Map();
  const batchNodes = [];
  groups.forEach((group) => {
    const batchNodeId = `execution-batch:${group.parentId}:${group.batch.id}`;
    group.tasks.forEach((task) => batchParentByTask.set(taskKeyOf(task), batchNodeId));
    const timestamps = group.tasks
      .map((task) => task.created_at)
      .filter(Boolean)
      .map((value) => ({ value, time: Date.parse(value) }))
      .filter(({ time }) => Number.isFinite(time))
      .sort((a, b) => a.time - b.time);
    batchNodes.push({
      id: batchNodeId,
      tool_id: batchNodeId,
      parentId: group.parentId || null,
      nodeKind: 'execution_batch',
      type: 'execution_batch',
      executionBatch: group.batch,
      observedBatchSize: group.tasks.length,
      created_at: timestamps[0]?.value || null,
    });
  });

  return [
    ...tasks.map((task) => {
      const batchParent = batchParentByTask.get(taskKeyOf(task));
      return batchParent ? { ...task, parentId: batchParent } : task;
    }),
    ...batchNodes,
  ];
};

const buildTaskTree = (tasks) => {
  const normalizedTasks = withExecutionBatchNodes(tasks);
  const byId = new Map();
  normalizedTasks.forEach((t) => {
    const id = taskKeyOf(t);
    if (id) {
      byId.set(id, t);
    }
  });

  const childrenOf = new Map(); // node key -> child nodes[]
  const roots = [];

  const addChild = (parentKey, node) => {
    if (!childrenOf.has(parentKey)) {
      childrenOf.set(parentKey, []);
    }
    childrenOf.get(parentKey).push(node);
  };

  normalizedTasks.forEach((task) => {
    const node = { key: 'task:' + taskKeyOf(task), task };
    const parentId = task.parentId != null ? String(task.parentId) : null;
    // Nest under the parent row when it resolves; guard against a self-parent so a bad link
    // can't make a row its own child.
    if (parentId && parentId !== taskKeyOf(task) && byId.has(parentId)) {
      addChild('task:' + parentId, node);
    } else {
      roots.push(node);
    }
  });

  return { roots, childrenOf, tasks: normalizedTasks };
};

const flattenTree = ({ roots, childrenOf }) => {
  const out = [];
  const visited = new Set();
  const visit = (node, depth) => {
    if (visited.has(node.key)) {
      return;
    }
    visited.add(node.key);
    out.push({ node, depth });
    (childrenOf.get(node.key) || [])
      .slice()
      .sort(byCreated)
      .forEach((child) => visit(child, depth + 1));
  };
  roots
    .slice()
    .sort(byCreated)
    .forEach((root) => visit(root, 0));
  return { out, visited };
};

// Re-home orphans as flat root rows: only genuinely-unreachable rows (cyclic/broken parent link)
// qualify. Kept separate from `flattenTree` so that stays a pure tree walk.
const flattenWithOrphans = (tasks, tree) => {
  const { out, visited } = flattenTree(tree);
  tasks.forEach((task) => {
    const key = 'task:' + taskKeyOf(task);
    if (!visited.has(key)) {
      visited.add(key);
      out.push({ node: { key, task }, depth: 0 });
    }
  });
  return out;
};

const LEVEL_COLOR = ['var(--ds-gray-700)', '#6B7280', '#9AA0A8', '#BCBFC4'];
const VIEWBOX_W = 21;
const VIEWBOX_H = 24;
const MAX_LEVEL = 4;
const ORIGIN_X = 1.5;
const ARROW_RUN = 9;
const ARROW_HEAD = 2.4;
const STEP_X = ARROW_RUN / 2;
const STEP_Y = 6;
const ARROW_HEAD_SCALE = [1, 0.85, 0.7];
const BRANCH_WIDTH = 1.2;
const TRUNK_WIDTH = 2.4;
const INDICATOR_COL_W = 36; // fixed gutter width (px) — keeps every row's title aligned
const NESTED_ROW_INDENT = 12;
const INDICATOR_H = 24;
const INDICATOR_W = (INDICATOR_H * VIEWBOX_W) / VIEWBOX_H;

const DepthIndicator = ({ depth }) => {
  const level = Math.min(Math.max(depth + 1, 1), MAX_LEVEL); // drawer depth 0 → level 1 (main); clamp at 4
  const color = LEVEL_COLOR[level - 1];
  const arrowCount = level - 1;
  const headSize = ARROW_HEAD * (ARROW_HEAD_SCALE[arrowCount - 1] ?? 1);
  const top = (VIEWBOX_H - (arrowCount * STEP_Y + headSize)) / 2;

  return (
    <svg width={INDICATOR_W} height={INDICATOR_H} viewBox={`0 0 ${VIEWBOX_W} ${VIEWBOX_H}`} fill='none' aria-hidden style={{ display: 'block' }}>
      {level === 1 ? (
        <line x1={ORIGIN_X} y1={3} x2={ORIGIN_X} y2={VIEWBOX_H - 3} strokeLinecap='round' style={{ stroke: color, strokeWidth: TRUNK_WIDTH }} />
      ) : (
        Array.from({ length: arrowCount }, (_, i) => {
          const x = ORIGIN_X + i * STEP_X;
          const y = top + (i + 1) * STEP_Y;
          const toX = x + ARROW_RUN;
          const elbow = `M${x} ${top + i * STEP_Y}V${y}H${toX}`;
          const head = `M${toX - headSize} ${y - headSize}L${toX} ${y}L${toX - headSize} ${y + headSize}`;
          return (
            <path
              key={i}
              d={`${elbow}${head}`}
              strokeLinecap='round'
              strokeLinejoin='round'
              fill='none'
              style={{ stroke: color, strokeWidth: BRANCH_WIDTH }}
            />
          );
        })
      )}
    </svg>
  );
};

DepthIndicator.propTypes = {
  depth: PropTypes.number,
};

// Tooltip on the depth glyph naming its sub-level. Root (depth 0) is the main task; everything below
// is a sub-task numbered by how deep it nests.
const subLevelLabel = (depth) => (depth <= 0 ? 'Task' : `Sub-task · level ${depth}`);

const ExecutionBatchRow = ({ task, collapsed }) => {
  const batch = task.executionBatch;
  return (
    <Box sx={{ px: ds.space[3], py: ds.space[2], display: 'flex', alignItems: 'center', gap: ds.space[2] }} data-testid='execution-batch-row'>
      <Tooltip title={executionBatchTooltip(batch)} placement='top'>
        <Box component='span' sx={{ display: 'inline-flex' }}>
          <Chip variant='tag' size='xs' tone={executionBatchTone(batch)} dot>
            {executionBatchLabel(batch, task.observedBatchSize)}
          </Chip>
        </Box>
      </Tooltip>
      <Box sx={{ ml: 'auto', display: 'inline-flex', color: 'var(--ds-gray-500)' }}>
        {collapsed ? <KeyboardArrowRightIcon fontSize='small' /> : <KeyboardArrowDownIcon fontSize='small' />}
      </Box>
    </Box>
  );
};

ExecutionBatchRow.propTypes = {
  task: PropTypes.object.isRequired,
  collapsed: PropTypes.bool,
};

const TaskRow = ({ task, depth, collapsed, onToggleCollapse, accountId, conversationId, isLast, isActive, onOpenToolDetails, itemProps }) => {
  const isBatch = task.nodeKind === 'execution_batch';
  const isHeader = depth === 0 && task.nodeKind === 'agent';
  const isActionable = (task.nodeKind === 'agent' || task.nodeKind === 'tool') && !isHeader;
  const collapsible = collapsed !== undefined;
  const openDetails = isActionable ? () => onOpenToolDetails(task) : undefined;
  const onClick = collapsible ? onToggleCollapse : openDetails;
  const clickable = collapsible || isActionable;
  return (
    <Box
      onClick={onClick}
      role={isBatch ? 'button' : undefined}
      tabIndex={isBatch ? 0 : undefined}
      aria-expanded={isBatch && collapsible ? !collapsed : undefined}
      onKeyDown={
        isBatch
          ? (event) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                onClick?.();
              }
            }
          : undefined
      }
      sx={{
        display: 'flex',
        cursor: clickable ? 'pointer' : 'default',
        ...(clickable ? { '& [id^="task-card-"] *': { cursor: 'pointer' } } : {}),
      }}
    >
      <Box sx={{ flexShrink: 0, width: INDICATOR_COL_W, pl: ds.space[2], display: 'flex', alignItems: 'center', justifyContent: 'flex-start' }}>
        <Tooltip title={subLevelLabel(depth)} placement='top'>
          <Box component='span' aria-hidden sx={{ display: 'inline-flex', lineHeight: 0 }}>
            <DepthIndicator depth={depth} />
          </Box>
        </Tooltip>
      </Box>
      {/* Only the task box carries the hover/active highlight — the depth gutter stays outside it. */}
      <Box
        sx={{
          flex: 1,
          minWidth: 0,
          ml: depth > 1 ? `${(depth - 1) * NESTED_ROW_INDENT}px` : 0,
          borderRadius: ds.radius.lg,
          transition: 'background-color 0.15s ease, box-shadow 0.15s ease',
          '& [id^="task-card-"] > div': {
            backgroundColor: 'transparent !important',
          },
          // Drop the card's own bottom divider on the active box so the inset ring isn't doubled by a
          // stray grey line near its base (which read as top-heavy).
          '& [id^="task-card-"]': { borderBottom: isActive ? 'none' : undefined },
          // The (repeated) Details button reveal is actionable-only; the hover highlight applies to any
          // clickable row (Details rows and collapsible headers).
          ...(isActionable
            ? {
                '& #tool-details-btn': { opacity: 0, transition: 'opacity 0.15s ease' },
                '&:hover #tool-details-btn, &:focus-within #tool-details-btn': { opacity: 1 },
              }
            : {}),
          ...(clickable ? { '&:hover': { backgroundColor: 'var(--ds-background-100)' } } : {}),
          backgroundColor: isActive ? 'var(--ds-background-100)' : 'transparent',
          boxShadow: isActive ? 'inset 0 0 0 1px var(--ds-blue-200)' : 'none',
        }}
      >
        {isBatch ? (
          <ExecutionBatchRow task={task} collapsed={collapsed} />
        ) : (
          <MessageItem
            message={task}
            index={task.originalIndex ?? task.id ?? 0}
            isLastInGroup={isLast}
            isLastTaskOfLastGroup={false}
            isCollapsed={false}
            collapsedObj={{}}
            onToggle={openDetails}
            showFullText={false}
            onShowFullText={() => {}}
            accountId={accountId}
            conversationId={conversationId}
            sessionId={itemProps?.sessionId}
            generateQuestionText={itemProps?.generateQuestionText}
            handleShare={itemProps?.handleShare}
            agentTokenData={itemProps?.getAgentTokenDataForMessage?.(task)}
            messageTokenData={itemProps?.messageTokenData?.[task.id]}
            handleTokenUsageHover={itemProps?.handleTokenUsageHover}
            isFetchingTokenData={itemProps?.isFetchingTokenData}
            selectedModel={itemProps?.selectedModel}
            conversationStatus={itemProps?.conversationStatus}
            onOpenToolDetails={openDetails}
            indentDepth={depth}
            collapsed={collapsed}
            hideTimeline
          />
        )}
      </Box>
    </Box>
  );
};

TaskRow.propTypes = {
  task: PropTypes.object.isRequired,
  depth: PropTypes.number,
  collapsed: PropTypes.bool,
  onToggleCollapse: PropTypes.func,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
  isLast: PropTypes.bool,
  isActive: PropTypes.bool,
  onOpenToolDetails: PropTypes.func.isRequired,
  itemProps: PropTypes.object,
};

const matchesActiveKey = (task, activeTaskKey) => {
  if (activeTaskKey == null) {
    return false;
  }
  const candidates = [task.id, task.tool_id, task.originalIndex];
  return candidates.some((c) => c != null && String(c) === String(activeTaskKey));
};

const hasChildren = (key, childrenOf) => (childrenOf.get(key) || []).length > 0;

const EXPANDABLE_MIN_DEPTH = 1;

const TasksDrawerContent = ({ tasks, accountId, conversationId, activeTaskKey, onOpenToolDetails, itemProps }) => {
  const tree = React.useMemo(() => buildTaskTree(tasks ?? []), [tasks]);
  const rows = React.useMemo(() => flattenWithOrphans(tasks ?? [], tree), [tasks, tree]);

  const isExpandable = React.useCallback((row) => row.depth >= EXPANDABLE_MIN_DEPTH && hasChildren(row.node.key, tree.childrenOf), [tree]);

  const [expandedKeys, setExpandedKeys] = React.useState(() => new Set());
  const [collapsedBatchKeys, setCollapsedBatchKeys] = React.useState(() => new Set());
  const isExpanded = React.useCallback(
    (row) => (row.node.task.nodeKind === 'execution_batch' ? !collapsedBatchKeys.has(row.node.key) : expandedKeys.has(row.node.key)),
    [collapsedBatchKeys, expandedKeys]
  );
  const toggleExpand = React.useCallback((row) => {
    const setter = row.node.task.nodeKind === 'execution_batch' ? setCollapsedBatchKeys : setExpandedKeys;
    setter((prev) => {
      const next = new Set(prev);
      if (next.has(row.node.key)) {
        next.delete(row.node.key);
      } else {
        next.add(row.node.key);
      }
      return next;
    });
  }, []);

  const visibleRows = React.useMemo(() => {
    const out = [];
    let closedAtDepth = null;
    rows.forEach((row) => {
      if (closedAtDepth !== null && row.depth > closedAtDepth) {
        return;
      }
      closedAtDepth = null;
      out.push(row);
      if (isExpandable(row) && !isExpanded(row)) {
        closedAtDepth = row.depth;
      }
    });
    return out;
  }, [rows, isExpandable, isExpanded]);

  if (!tasks || tasks.length === 0) {
    return (
      <Typography
        sx={{
          fontSize: 'var(--ds-text-body)',
          color: 'var(--ds-gray-500)',
          fontFamily: ds.font.sans,
          textAlign: 'center',
          mt: ds.space[5],
        }}
      >
        No tool calls for this response.
      </Typography>
    );
  }
  return (
    <Box>
      {visibleRows.map((row, idx) => {
        const { node, depth } = row;
        const expandable = isExpandable(row);
        return (
          <TaskRow
            key={node.key}
            task={node.task}
            depth={depth}
            collapsed={expandable ? !isExpanded(row) : undefined}
            onToggleCollapse={expandable ? () => toggleExpand(row) : undefined}
            accountId={accountId}
            conversationId={conversationId}
            isLast={idx === visibleRows.length - 1}
            isActive={matchesActiveKey(node.task, activeTaskKey)}
            onOpenToolDetails={onOpenToolDetails}
            itemProps={itemProps}
          />
        );
      })}
    </Box>
  );
};

TasksDrawerContent.propTypes = {
  tasks: PropTypes.array.isRequired,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
  activeTaskKey: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  onOpenToolDetails: PropTypes.func.isRequired,
  itemProps: PropTypes.object,
};

export default TasksDrawerContent;

// Exported for unit tests only. Not part of the public component API.
export { buildTaskTree, flattenWithOrphans, withExecutionBatchNodes };
