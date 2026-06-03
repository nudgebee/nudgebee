import React, { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import MessageItem from './MessageItem';
import CustomTable from '@common-new/tables/CustomTable2';
import { convertToReadableFormat } from 'src/utils/common';
import Text from '@common-new/format/Text';
import CustomDrawer, { SecondaryDrawer } from '@components1/common/CustomDrawer';
import TasksDrawerContent from './common/TasksDrawerContent';
import MemoriesDrawerContent from './common/MemoriesDrawerContent';
import ToolDetails from './common/ToolDetails';
import useMessageAdditionalData from '@hooks/useMessageAdditionalData';
import { ds } from '@utils/colors';

const taskKey = (task) => task?.id ?? task?.tool_id ?? task?.originalIndex ?? null;

const buildTable = (rows) => {
  if (!rows?.length) {
    return { headers: [], tableData: [] };
  }
  const headers = Object.keys(rows[0]);
  for (let i = 1; i < rows.length; i++) {
    Object.keys(rows[i]).forEach((k) => {
      if (!headers.includes(k)) {
        headers.push(k);
      }
    });
  }
  const tableData = rows.map((row) =>
    headers.map((h) => {
      let value = row[h];
      if (typeof value === 'object' || Array.isArray(value)) {
        value = JSON.stringify(value);
      }
      return { component: <Text value={value} showAutoEllipsis sx={{ minWidth: ds.space.mul(0, 25) }} /> };
    })
  );
  return {
    headers: headers.map((f) => convertToReadableFormat(f.replaceAll('_', ' '))),
    tableData,
  };
};

const MessageStream = ({ messages, isProcessing, collapsedObj, setCollapsedObj, showFullText, setShowFullText, itemProps }) => {
  // Primary right drawer — Tasks list, Contexts table, Memories, or (for inline rows) Tool Details.
  // `kind` drives which content renderer runs so the panel re-renders when `secondary` changes
  // (we can't store JSX directly because the active-task highlight needs to update live).
  const [drawer, setDrawer] = useState({ open: false, kind: null, title: '', data: null });
  // Secondary panel — opens flush against the left edge of the primary drawer to show
  // ToolDetails for the task selected inside the Tasks list (replaces the old in-row accordion).
  const [secondary, setSecondary] = useState({ open: false, task: null });
  const [primaryWidth, setPrimaryWidth] = useState(0);

  const closeSecondary = useCallback(() => setSecondary({ open: false, task: null }), []);
  const closeDrawer = useCallback(() => {
    setDrawer((d) => ({ ...d, open: false }));
    setSecondary({ open: false, task: null });
  }, []);

  const openSecondaryToolDetails = useCallback((task) => {
    setSecondary({ open: true, task });
  }, []);

  const groupedMessages = useMemo(() => {
    const groups = [];
    let currentGroup = null;
    messages.forEach((m, index) => {
      const type = m.tool ?? m.type;
      if (type === 'question') {
        if (currentGroup) {
          groups.push(currentGroup);
        }
        currentGroup = { question: { ...m, originalIndex: index }, children: [] };
      } else if (currentGroup) {
        currentGroup.children.push({ ...m, originalIndex: index });
      }
    });
    if (currentGroup) {
      groups.push(currentGroup);
    }
    return groups;
  }, [messages]);

  const additionalData = useMessageAdditionalData(groupedMessages, itemProps.accountId, itemProps.conversationId);

  const handleCardClick = useCallback(
    (index) => {
      setCollapsedObj((prev) => ({ ...prev, [index]: !prev[index] }));
    },
    [setCollapsedObj]
  );

  // Per-task "Tool Details" drawer — used by inline task rows during active runs (no Tasks
  // drawer is open in that case, so we open the primary drawer with the ToolDetails view).
  const handleOpenToolDetails = useCallback((toolCallMessage) => {
    setDrawer({ open: true, kind: 'tool-details', title: 'Tool Details', data: { task: toolCallMessage } });
    setSecondary({ open: false, task: null });
  }, []);

  const openTasksDrawer = useCallback(({ tasks, expandedTaskKey }) => {
    setDrawer({ open: true, kind: 'tasks', title: `Tasks · ${tasks.length}`, data: { tasks } });
    if (expandedTaskKey != null) {
      const target = tasks.find((t) => {
        const candidates = [t.id, t.tool_id, t.originalIndex];
        return candidates.some((c) => c != null && String(c) === String(expandedTaskKey));
      });
      if (target) {
        setSecondary({ open: true, task: target });
        return;
      }
    }
    setSecondary({ open: false, task: null });
  }, []);

  const openContextsDrawer = useCallback((references) => {
    setDrawer({ open: true, kind: 'contexts', title: `Additional Contexts · ${references.length}`, data: { references } });
    setSecondary({ open: false, task: null });
  }, []);

  const openMemoriesDrawer = useCallback((memories) => {
    setDrawer({ open: true, kind: 'memories', title: `New Memories · ${memories.length}`, data: { memories } });
    setSecondary({ open: false, task: null });
  }, []);

  // Auto-expand newly-arrived followup-question cards in the active group, and scroll the
  // viewport to the latest one so the user notices it. Tracks the count we've already seen
  // per group so we only react to *new* arrivals (polling can return the same set repeatedly).
  const seenFollowupCountRef = useRef({});
  useEffect(() => {
    if (messages.length === 0) {
      seenFollowupCountRef.current = {};
    }
  }, [messages.length]);

  useEffect(() => {
    groupedMessages.forEach((group, groupIndex) => {
      const followups = group.children.filter((c) => (c.tool ?? c.type) === 'followup-question');
      if (followups.length === 0) {
        return;
      }
      const prevCount = seenFollowupCountRef.current[groupIndex] || 0;
      if (followups.length <= prevCount) {
        // Count went down (polling flicker) or stayed the same — sync ref and exit.
        seenFollowupCountRef.current[groupIndex] = followups.length;
        return;
      }
      const newFollowups = followups.slice(prevCount);
      seenFollowupCountRef.current[groupIndex] = followups.length;

      // Auto-expand each newly-arrived followup card.
      setCollapsedObj((prev) => {
        const updates = {};
        newFollowups.forEach((f) => {
          updates[f.originalIndex] = true;
        });
        return { ...prev, ...updates };
      });

      // Scroll to the latest one after the next paint — but only if the bottom-anchored
      // FollowupSheet isn't already taking over for this followup. Otherwise scrolling to
      // a read-only inline card on top would bury the interactive sheet at the bottom.
      const lastFollowup = newFollowups[newFollowups.length - 1];
      if (!lastFollowup) {
        return;
      }
      const lastFollowupKey = `${lastFollowup.response?.message_id || ''}:${lastFollowup.response?.agent_id || ''}`;
      if (itemProps.followupReadOnlyKey && itemProps.followupReadOnlyKey === lastFollowupKey) {
        return;
      }
      requestAnimationFrame(() => {
        const el = document.getElementById(`task-card-${lastFollowup.originalIndex}`);
        if (el) {
          el.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
      });
    });
  }, [groupedMessages, setCollapsedObj, itemProps.followupReadOnlyKey]);

  // Navigate to a task. If it's still rendered inline (active group, no response yet),
  // expand the card and scroll to it. Otherwise (completed group) open the right drawer
  // with that task pre-expanded.
  const handleNavigateToTask = useCallback(
    (groupIndex, taskOriginalIndex) => {
      const group = groupedMessages[groupIndex];
      if (!group) {
        return;
      }
      const hasResponse = group.children.some((c) => (c.tool ?? c.type) === 'response');
      const tasks = group.children.filter((c) => (c.tool ?? c.type) !== 'question' && (c.tool ?? c.type) !== 'response');
      if (tasks.length === 0) {
        return;
      }

      if (!hasResponse) {
        // Tasks are inline — expand and scroll.
        setCollapsedObj((prev) => ({ ...prev, [taskOriginalIndex]: true }));
        setTimeout(() => {
          const el = document.getElementById(`task-card-${taskOriginalIndex}`);
          if (el) {
            el.scrollIntoView({ behavior: 'smooth', block: 'start' });
          }
        }, 200);
        return;
      }

      // Completed group — open the drawer.
      const target = tasks.find((t) => t.originalIndex === taskOriginalIndex);
      const expandedTaskKey = target?.id || target?.tool_id;
      openTasksDrawer({ tasks, expandedTaskKey });
    },
    [groupedMessages, openTasksDrawer, setCollapsedObj]
  );

  return (
    <Box>
      {groupedMessages.map((group, groupIndex) => {
        const response = group.children.find((c) => (c.tool ?? c.type) === 'response');
        const tasks = group.children.filter((c) => (c.tool ?? c.type) !== 'question' && (c.tool ?? c.type) !== 'response');
        const extra = response ? additionalData[response.id] : null;
        const references = extra?.references || [];
        const memories = extra?.memories || [];

        const responseTokenData = response ? itemProps.messageTokenData?.[response.id] || itemProps.messageTokenData?.[response.messageId] : null;

        const responseMeta = response
          ? {
              taskCount: tasks.length,
              contextCount: references.length,
              memoryCount: memories.length,
              onOpenTasks: tasks.length > 0 ? () => openTasksDrawer({ tasks }) : undefined,
              onOpenContexts: references.length > 0 ? () => openContextsDrawer(references) : undefined,
              onOpenMemories: memories.length > 0 ? () => openMemoriesDrawer(memories) : undefined,
              messageTokenData: responseTokenData,
              onTokenUsageHover: itemProps.handleTokenUsageHover,
              isFetchingTokenData: itemProps.isFetchingTokenData,
            }
          : null;

        const isLastGroup = groupIndex === groupedMessages.length - 1;
        // Inline-render tasks only for groups that haven't produced a response yet.
        // Past turns drop their tasks from the inline view — they remain accessible via the
        // response meta-rail's "Tasks" chip → drawer.
        const showInlineTasks = !response && tasks.length > 0;

        return (
          <React.Fragment key={group.question.originalIndex}>
            <MessageItem
              message={group.question}
              index={group.question.originalIndex}
              isCollapsed={false}
              collapsedObj={collapsedObj}
              onToggle={() => {}}
              showFullText={showFullText}
              onShowFullText={() => setShowFullText(!showFullText)}
              {...itemProps}
            />
            {showInlineTasks &&
              tasks.map((task, taskIdx) => {
                const isLastTaskInGroup = taskIdx === tasks.length - 1;
                return (
                  <MessageItem
                    key={task.originalIndex}
                    message={task}
                    index={task.originalIndex}
                    isLastInGroup={isLastTaskInGroup}
                    isLastTaskOfLastGroup={isLastGroup && isLastTaskInGroup}
                    isCollapsed={!!collapsedObj[task.originalIndex]}
                    collapsedObj={collapsedObj}
                    onToggle={() => handleCardClick(task.originalIndex)}
                    showFullText={showFullText}
                    onShowFullText={() => setShowFullText(!showFullText)}
                    isLoadingInvestigation={isProcessing}
                    {...itemProps}
                    siblingTasks={tasks}
                    agentTokenData={itemProps.getAgentTokenDataForMessage?.(task)}
                    messageTokenData={itemProps.messageTokenData?.[task.id] || itemProps.messageTokenData?.[task.messageId]}
                    onOpenToolDetails={handleOpenToolDetails}
                    onNavigateToTask={handleNavigateToTask}
                    groupIndex={groupIndex}
                  />
                );
              })}
            {response && (
              <MessageItem
                key={response.originalIndex}
                message={response}
                index={response.originalIndex}
                isLastInGroup={true}
                isLastTaskOfLastGroup={isLastGroup}
                isCollapsed={!!collapsedObj[response.originalIndex]}
                collapsedObj={collapsedObj}
                onToggle={() => handleCardClick(response.originalIndex)}
                showFullText={showFullText}
                onShowFullText={() => setShowFullText(!showFullText)}
                isLoadingInvestigation={isProcessing}
                {...itemProps}
                siblingTasks={tasks}
                agentTokenData={itemProps.getAgentTokenDataForMessage(response)}
                messageTokenData={itemProps.messageTokenData?.[response.id] || itemProps.messageTokenData?.[response.messageId]}
                onNavigateToTask={handleNavigateToTask}
                groupIndex={groupIndex}
                responseMeta={responseMeta}
              />
            )}
          </React.Fragment>
        );
      })}

      <CustomDrawer
        open={drawer.open}
        onClose={closeDrawer}
        title={drawer.title}
        width='38%'
        onWidthChange={setPrimaryWidth}
        resizable={false}
        variant={drawer.kind === 'tasks' ? 'modern' : 'default'}
      >
        <Box sx={{ color: 'var(--ds-gray-700)' }}>
          {renderDrawerContent({
            drawer,
            secondary,
            itemProps,
            onOpenToolDetails: openSecondaryToolDetails,
          })}
        </Box>
      </CustomDrawer>

      <SecondaryDrawer
        open={secondary.open && drawer.open && drawer.kind === 'tasks'}
        onClose={closeSecondary}
        title='Tool Details'
        rightOffset={primaryWidth}
        defaultWidth='45%'
        variant='modern'
      >
        {secondary.task && <ToolDetails toolCall={secondary.task} accountId={itemProps.accountId} conversationId={itemProps.conversationId} />}
      </SecondaryDrawer>
    </Box>
  );
};

const renderDrawerContent = ({ drawer, secondary, itemProps, onOpenToolDetails }) => {
  if (!drawer.kind || !drawer.data) {
    return null;
  }
  switch (drawer.kind) {
    case 'tasks':
      return (
        <TasksDrawerContent
          tasks={drawer.data.tasks}
          accountId={itemProps.accountId}
          conversationId={itemProps.conversationId}
          activeTaskKey={taskKey(secondary.task)}
          onOpenToolDetails={onOpenToolDetails}
          itemProps={itemProps}
        />
      );
    case 'contexts': {
      const { headers, tableData } = buildTable(
        drawer.data.references.map(({ content, metadata, type, created_at }) => ({ content, type, created_at, ...metadata }))
      );
      return (
        <Box sx={{ overflowX: 'auto' }}>
          <CustomTable
            tableData={tableData}
            headers={headers}
            totalRows={tableData.length}
            rowsPerPage={10}
            renderVertical={tableData?.length <= 1}
          />
        </Box>
      );
    }
    case 'memories':
      return <MemoriesDrawerContent memories={drawer.data.memories} />;
    case 'tool-details':
      return <ToolDetails toolCall={drawer.data.task} accountId={itemProps.accountId} conversationId={itemProps.conversationId} />;
    default:
      return null;
  }
};

MessageStream.propTypes = {
  messages: PropTypes.array.isRequired,
  isProcessing: PropTypes.bool,
  collapsedObj: PropTypes.object,
  setCollapsedObj: PropTypes.func.isRequired,
  showFullText: PropTypes.bool,
  setShowFullText: PropTypes.func.isRequired,
  itemProps: PropTypes.object.isRequired,
};

export default MessageStream;
