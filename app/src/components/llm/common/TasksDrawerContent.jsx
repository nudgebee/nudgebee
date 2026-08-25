import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import Tooltip from '@ui/Tooltip';
import MessageItem from '../MessageItem';

const taskKeyOf = (task) => String(task.tool_id ?? task.id ?? task.originalIndex ?? '');
const byCreated = (a, b) => {
  const ca = a.task?.created_at || '';
  const cb = b.task?.created_at || '';
  return ca < cb ? -1 : ca > cb ? 1 : 0;
};

const buildTaskTree = (tasks) => {
  const byId = new Map();
  tasks.forEach((t) => {
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

  tasks.forEach((task) => {
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

  return { roots, childrenOf };
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
const CASCADE = {
  1: { trunkX: 8, trunkY: [3, 21], branchW: 1.5, branches: [] },
  2: { trunkX: 8, trunkY: [3, 21], branchW: 1.5, branches: [{ fromX: 8, y: 12, toX: 18.5 }] },
  3: {
    trunkX: 6,
    trunkY: [3, 21],
    branchW: 1.4,
    branches: [
      { fromX: 6, y: 9, toX: 19 },
      { fromX: 11, y: 16, toX: 19, dropFromY: 9 },
    ],
  },
  4: {
    trunkX: 4.5,
    trunkY: [2.5, 21.5],
    branchW: 1.3,
    branches: [
      { fromX: 4.5, y: 6, toX: 20 },
      { fromX: 9, y: 12.5, toX: 20, dropFromY: 6 },
      { fromX: 13.5, y: 19, toX: 20, dropFromY: 12.5 },
    ],
  },
};
const ARROW_HEAD = 2.2; // chevron half-size on each branch tip
const LEVEL1_TRUNK_WIDTH = 2.2; // level-1 trunks (main task + acknowledgment) are a little thicker than the 1.7 sub-level default
const INDICATOR_COL_W = 32; // fixed gutter width (px) — keeps every row's title aligned
const INDICATOR_SIZE = 16; // rendered icon size (px), from a 24×24 viewBox

const DepthIndicator = ({ depth }) => {
  const level = Math.min(Math.max(depth + 1, 1), 4); // drawer depth 0 → level 1 (main); clamp at 4
  const { trunkX, trunkY, branchW, branches } = CASCADE[level];
  const color = LEVEL_COLOR[level - 1];

  return (
    <svg width={INDICATOR_SIZE} height={INDICATOR_SIZE} viewBox='0 0 24 24' fill='none' aria-hidden style={{ display: 'block' }}>
      <line
        x1={trunkX}
        y1={trunkY[0]}
        x2={trunkX}
        y2={trunkY[1]}
        strokeLinecap='round'
        style={{ stroke: color, strokeWidth: level === 1 ? LEVEL1_TRUNK_WIDTH : 1.7 }}
      />
      {branches.map((b, i) => {
        const drop = 'dropFromY' in b ? `M${b.fromX} ${b.dropFromY}V${b.y}` : '';
        const line = `M${b.fromX} ${b.y}H${b.toX}`;
        const arrow = `M${b.toX - ARROW_HEAD} ${b.y - ARROW_HEAD}L${b.toX} ${b.y}L${b.toX - ARROW_HEAD} ${b.y + ARROW_HEAD}`;
        return (
          <path
            key={i}
            d={`${drop}${line}${arrow}`}
            strokeLinecap='round'
            strokeLinejoin='round'
            fill='none'
            style={{ stroke: color, strokeWidth: branchW }}
          />
        );
      })}
    </svg>
  );
};

DepthIndicator.propTypes = {
  depth: PropTypes.number,
};

// Tooltip on the depth glyph naming its sub-level. Root (depth 0) is the main task; everything below
// is a sub-task numbered by how deep it nests.
const subLevelLabel = (depth) => (depth <= 0 ? 'Task' : `Sub-task · level ${depth}`);

const TaskRow = ({
  task,
  depth,
  stepCount,
  collapsed,
  onToggleCollapse,
  accountId,
  conversationId,
  isLast,
  isActive,
  onOpenToolDetails,
  itemProps,
}) => {
  const isHeader = depth === 0 && task.nodeKind === 'agent';
  const isActionable = (task.nodeKind === 'agent' || task.nodeKind === 'tool') && !isHeader;
  const collapsible = collapsed !== undefined; // set only for headers with >1 items
  const openDetails = isActionable ? () => onOpenToolDetails(task) : undefined;
  const onClick = collapsible ? onToggleCollapse : openDetails;
  const clickable = collapsible || isActionable;
  return (
    <Box
      onClick={onClick}
      sx={{
        display: 'flex',
        cursor: clickable ? 'pointer' : 'default',
        ...(clickable ? { '& [id^="task-card-"] *': { cursor: 'pointer' } } : {}),
      }}
    >
      <Box sx={{ flexShrink: 0, width: INDICATOR_COL_W, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
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
          stepCount={isHeader ? stepCount : undefined}
          collapsed={collapsed}
          hideTimeline
        />
      </Box>
    </Box>
  );
};

TaskRow.propTypes = {
  task: PropTypes.object.isRequired,
  depth: PropTypes.number,
  stepCount: PropTypes.number,
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

// Total descendant rows under a node — drives the root orchestrator's "· N steps" roll-up.
const countDescendants = (key, childrenOf) => (childrenOf.get(key) || []).reduce((n, child) => n + 1 + countDescendants(child.key, childrenOf), 0);

const TasksDrawerContent = ({ tasks, accountId, conversationId, activeTaskKey, onOpenToolDetails, itemProps }) => {
  const tree = React.useMemo(() => buildTaskTree(tasks ?? []), [tasks]);
  const rows = React.useMemo(() => flattenWithOrphans(tasks ?? [], tree), [tasks, tree]);

  // "· N steps" roll-up per root orchestrator (depth-0 agent) so the root reads as a container header.
  const stepCountByKey = React.useMemo(() => {
    const map = new Map();
    tree.roots.forEach((root) => {
      if (root.task?.nodeKind === 'agent') {
        map.set(root.key, countDescendants(root.key, tree.childrenOf));
      }
    });
    return map;
  }, [tree]);

  // Expand/collapse for the main task (a depth-0 header with >1 items). Collapsed headers hide their
  // descendants; expanded by default. Keyed by node key.
  const [collapsedKeys, setCollapsedKeys] = React.useState(() => new Set());
  const toggleCollapse = React.useCallback((key) => {
    setCollapsedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }, []);

  // Hide the descendants of a collapsed depth-0 header. Pre-order groups a root's descendants right
  // after it, so we drop depth>0 rows until the next depth-0 row.
  const visibleRows = React.useMemo(() => {
    const out = [];
    let hidden = false;
    rows.forEach((row) => {
      if (row.depth === 0) {
        out.push(row);
        hidden = collapsedKeys.has(row.node.key);
      } else if (!hidden) {
        out.push(row);
      }
    });
    return out;
  }, [rows, collapsedKeys]);

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
      {visibleRows.map(({ node, depth }, idx) => {
        const isHeaderNode = depth === 0 && node.task.nodeKind === 'agent';
        const steps = isHeaderNode ? stepCountByKey.get(node.key) : undefined;
        const collapsible = isHeaderNode && steps > 1;
        return (
          <TaskRow
            key={node.key}
            task={node.task}
            depth={depth}
            stepCount={steps}
            collapsed={collapsible ? collapsedKeys.has(node.key) : undefined}
            onToggleCollapse={collapsible ? () => toggleCollapse(node.key) : undefined}
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
export { buildTaskTree, flattenWithOrphans };
