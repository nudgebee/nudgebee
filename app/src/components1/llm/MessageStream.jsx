import React, { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { colors } from 'src/utils/colors';
import MessageItem from './MessageItem';
import CustomTable from '@components1/common/tables/CustomTable2';
import { convertToReadableFormat } from 'src/utils/common';
import { Text } from '@components1/common';
import CustomDrawer from '@components1/common/CustomDrawer';
import TasksDrawerContent from './common/TasksDrawerContent';
import MemoriesDrawerContent from './common/MemoriesDrawerContent';
import ToolDetails from './common/ToolDetails';
import useMessageAdditionalData from '@hooks/useMessageAdditionalData';

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
      return { component: <Text value={value} showAutoEllipsis sx={{ minWidth: '50px' }} /> };
    })
  );
  return {
    headers: headers.map((f) => convertToReadableFormat(f.replaceAll('_', ' '))),
    tableData,
  };
};

const MessageStream = ({ messages, isProcessing, collapsedObj, setCollapsedObj, showFullText, setShowFullText, itemProps }) => {
  const [drawer, setDrawer] = useState({ open: false, title: '', content: null });
  const closeDrawer = useCallback(() => setDrawer((d) => ({ ...d, open: false })), []);

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

  // Per-task "Tool Details" drawer — opens the right drawer with the full ToolDetails view
  // for one tool call. Used by the inline task rows during active runs and by anything else
  // wired through `onOpenToolDetails`.
  const handleOpenToolDetails = useCallback(
    (toolCallMessage) => {
      setDrawer({
        open: true,
        title: 'Tool Details',
        content: <ToolDetails toolCall={toolCallMessage} accountId={itemProps.accountId} conversationId={itemProps.conversationId} />,
      });
    },
    [itemProps.accountId, itemProps.conversationId]
  );

  const openTasksDrawer = useCallback(
    ({ tasks, expandedTaskKey }) => {
      setDrawer({
        open: true,
        title: `Tasks · ${tasks.length}`,
        content: (
          <TasksDrawerContent
            tasks={tasks}
            accountId={itemProps.accountId}
            conversationId={itemProps.conversationId}
            expandedTaskKey={expandedTaskKey}
            itemProps={itemProps}
          />
        ),
      });
    },
    [itemProps]
  );

  const openContextsDrawer = useCallback((references) => {
    const { headers, tableData } = buildTable(
      references.map(({ content, metadata, type, created_at }) => ({ content, type, created_at, ...metadata }))
    );
    setDrawer({
      open: true,
      title: `Additional Contexts · ${references.length}`,
      content: (
        <Box sx={{ overflowX: 'auto' }}>
          <CustomTable
            tableData={tableData}
            headers={headers}
            totalRows={tableData.length}
            rowsPerPage={10}
            renderVertical={tableData?.length <= 1}
          />
        </Box>
      ),
    });
  }, []);

  const openMemoriesDrawer = useCallback((memories) => {
    setDrawer({
      open: true,
      title: `New Memories · ${memories.length}`,
      content: <MemoriesDrawerContent memories={memories} />,
    });
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

      // Scroll to the latest one after the next paint.
      const lastFollowup = newFollowups[newFollowups.length - 1];
      if (lastFollowup) {
        requestAnimationFrame(() => {
          const el = document.getElementById(`task-card-${lastFollowup.originalIndex}`);
          if (el) {
            el.scrollIntoView({ behavior: 'smooth', block: 'start' });
          }
        });
      }
    });
  }, [groupedMessages, setCollapsedObj]);

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

      <CustomDrawer open={drawer.open} onClose={closeDrawer} title={drawer.title} width='40%'>
        <Box sx={{ color: colors.text.secondary }}>{drawer.content}</Box>
      </CustomDrawer>
    </Box>
  );
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
