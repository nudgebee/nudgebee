import React from 'react';
import { Box, Typography } from '@mui/material';
import ExpandMoreRoundedIcon from '@mui/icons-material/ExpandMoreRounded';
import ChevronRightRoundedIcon from '@mui/icons-material/ChevronRightRounded';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import MessageItem from '../MessageItem';

// 12px per nesting level, clamped at 4 — kept in sync with MessageItem's own indent so a
// group header lines up with the task rows at the same depth.
const indentPx = (depth) => (depth > 0 ? ds.space.mul(3, Math.min(depth, 4)) : 0);

// originalIndex fallback keeps keyless rows (e.g. the acknowledgment row) unique so they
// can't collide in the tree map or the flatten visited-set and drop each other.
const taskKeyOf = (task) => String(task.tool_id ?? task.id ?? task.originalIndex ?? '');
const createdAtOf = (node) => (node.kind === 'group' ? node.orchestrator?.created_at : node.task?.created_at) || '';
const byCreated = (a, b) => (createdAtOf(a) < createdAtOf(b) ? -1 : createdAtOf(a) > createdAtOf(b) ? 1 : 0);

// Build a display tree from the flat task list — drawer-only. A task nests under its parent
// task when that parent is also rendered (parentAgentId resolves); otherwise, if its (hidden)
// parent is an orchestrator, it nests under a synthesized collapsible group. Everything else
// is a root. Orchestrators themselves are never rendered inline, so this reconstruction lives
// entirely here and the main message stream is unaffected.
const buildTaskTree = (tasks) => {
  const byId = new Map();
  tasks.forEach((t) => {
    const id = taskKeyOf(t);
    if (id) {
      byId.set(id, t);
    }
  });

  const groups = new Map(); // groupKey -> group node
  const childrenOf = new Map(); // node key -> child nodes[]
  const roots = [];

  const addChild = (parentKey, node) => {
    if (!childrenOf.has(parentKey)) {
      childrenOf.set(parentKey, []);
    }
    childrenOf.get(parentKey).push(node);
  };

  tasks.forEach((task) => {
    const node = { kind: 'task', key: 'task:' + taskKeyOf(task), task };
    const parentAgentId = task.parentAgentId != null ? String(task.parentAgentId) : null;

    if (parentAgentId && byId.has(parentAgentId)) {
      addChild('task:' + parentAgentId, node);
    } else if (task.orchestratorParent?.id) {
      const orch = task.orchestratorParent;
      const groupKey = 'orch:' + String(orch.id);
      if (!groups.has(groupKey)) {
        const groupNode = { kind: 'group', key: groupKey, orchestrator: orch };
        groups.set(groupKey, groupNode);
        roots.push(groupNode);
      }
      addChild(groupKey, node);
    } else {
      roots.push(node);
    }
  });

  return { roots, childrenOf };
};

// Flatten to an ordered render list (pre-order, created_at within each level). Children of a
// collapsed group are skipped. A reachability guard appends any task node that a *broken* parent
// chain (e.g. a cycle) would otherwise orphan — sub-agent rows must never be silently dropped.
const flattenTree = (tasks, collapsedKeys) => {
  const { roots, childrenOf } = buildTaskTree(tasks);

  // Structural reachability, computed IGNORING collapse — so a node hidden only because its
  // group is collapsed is not mistaken for an orphan and re-dumped at root.
  const reachable = new Set();
  const mark = (node) => {
    if (reachable.has(node.key)) {
      return;
    }
    reachable.add(node.key);
    (childrenOf.get(node.key) || []).forEach(mark);
  };
  roots.forEach(mark);

  const out = [];
  const visited = new Set();
  const visit = (node, depth) => {
    if (visited.has(node.key)) {
      return;
    }
    visited.add(node.key);
    out.push({ node, depth });
    if (node.kind === 'group' && collapsedKeys.has(node.key)) {
      return;
    }
    (childrenOf.get(node.key) || [])
      .slice()
      .sort(byCreated)
      .forEach((child) => visit(child, depth + 1));
  };

  roots
    .slice()
    .sort(byCreated)
    .forEach((root) => visit(root, 0));

  // Safety net: only genuinely-unreachable tasks (cyclic/broken parent link) get re-homed at
  // root. Nodes hidden by collapse are structurally reachable, so they are left hidden.
  tasks.forEach((task) => {
    const key = 'task:' + taskKeyOf(task);
    if (!reachable.has(key) && !visited.has(key)) {
      visited.add(key);
      out.push({ node: { kind: 'task', key, task }, depth: 0 });
    }
  });

  return out;
};

const prettyOrchestratorName = (name) => {
  if (!name) {
    return 'Orchestrator';
  }
  const spaced = name.replace(/_/g, ' ');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
};

const GroupHeaderRow = ({ orchestrator, depth, childCount, collapsed, onToggle }) => (
  <Box
    role='button'
    tabIndex={0}
    onClick={onToggle}
    onKeyDown={(e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        onToggle();
      }
    }}
    sx={{
      display: 'flex',
      alignItems: 'center',
      gap: ds.space[1],
      ml: indentPx(depth),
      mb: ds.space[1],
      px: ds.space[2],
      py: ds.space[1],
      cursor: 'pointer',
      borderRadius: ds.radius.md,
      userSelect: 'none',
      '&:hover': { backgroundColor: 'var(--ds-background-100)' },
    }}
  >
    {collapsed ? (
      <ChevronRightRoundedIcon sx={{ fontSize: 18, color: 'var(--ds-gray-500)' }} />
    ) : (
      <ExpandMoreRoundedIcon sx={{ fontSize: 18, color: 'var(--ds-gray-500)' }} />
    )}
    <Typography
      sx={{
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-medium)',
        color: 'var(--ds-gray-700)',
        fontFamily: ds.font.sans,
      }}
    >
      {prettyOrchestratorName(orchestrator?.name)}
    </Typography>
    <Typography
      sx={{
        fontSize: 'var(--ds-text-caption)',
        color: 'var(--ds-gray-500)',
        fontFamily: ds.font.sans,
      }}
    >
      · {childCount} {childCount === 1 ? 'step' : 'steps'}
    </Typography>
  </Box>
);

GroupHeaderRow.propTypes = {
  orchestrator: PropTypes.object,
  depth: PropTypes.number,
  childCount: PropTypes.number,
  collapsed: PropTypes.bool,
  onToggle: PropTypes.func.isRequired,
};

const TaskRow = ({ task, depth, accountId, conversationId, isLast, isActive, onOpenToolDetails, itemProps }) => (
  <Box
    sx={{
      position: 'relative',
      borderRadius: ds.radius.lg,
      transition: 'background-color 0.15s ease, box-shadow 0.15s ease',
      '& [id^="task-card-"] > div': {
        backgroundColor: 'transparent !important',
      },
      backgroundColor: isActive ? 'var(--ds-background-100)' : 'transparent',
      boxShadow: isActive ? `inset 4px 0 0 0 1, 0 0 0 1px ${'var(--ds-blue-200)'}` : 'none',
      mb: ds.space[1],
    }}
  >
    <MessageItem
      message={task}
      index={task.originalIndex ?? task.id ?? 0}
      isLastInGroup={isLast}
      isLastTaskOfLastGroup={false}
      isCollapsed={false}
      collapsedObj={{}}
      onToggle={() => onOpenToolDetails(task)}
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
      onOpenToolDetails={() => onOpenToolDetails(task)}
      indentDepth={depth}
    />
  </Box>
);

TaskRow.propTypes = {
  task: PropTypes.object.isRequired,
  depth: PropTypes.number,
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

const TasksDrawerContent = ({ tasks, accountId, conversationId, activeTaskKey, onOpenToolDetails, itemProps }) => {
  const [collapsedKeys, setCollapsedKeys] = React.useState(() => new Set());

  const toggleGroup = React.useCallback((key) => {
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

  const rows = React.useMemo(() => flattenTree(tasks ?? [], collapsedKeys), [tasks, collapsedKeys]);
  const childCounts = React.useMemo(() => {
    const { childrenOf } = buildTaskTree(tasks ?? []);
    const counts = {};
    childrenOf.forEach((children, key) => {
      counts[key] = children.length;
    });
    return counts;
  }, [tasks]);

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
      {rows.map(({ node, depth }, idx) => {
        if (node.kind === 'group') {
          return (
            <GroupHeaderRow
              key={node.key}
              orchestrator={node.orchestrator}
              depth={depth}
              childCount={childCounts[node.key] ?? 0}
              collapsed={collapsedKeys.has(node.key)}
              onToggle={() => toggleGroup(node.key)}
            />
          );
        }
        return (
          <TaskRow
            key={node.key}
            task={node.task}
            depth={depth}
            accountId={accountId}
            conversationId={conversationId}
            isLast={idx === rows.length - 1}
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
