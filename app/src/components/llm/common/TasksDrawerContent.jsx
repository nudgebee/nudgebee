import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import MessageItem from '../MessageItem';

const TaskRow = ({ task, accountId, conversationId, isLast, isActive, onOpenToolDetails, itemProps }) => (
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
    />
  </Box>
);

TaskRow.propTypes = {
  task: PropTypes.object.isRequired,
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
      {tasks.map((task, idx) => (
        <TaskRow
          key={task.id || task.tool_id || idx}
          task={task}
          accountId={accountId}
          conversationId={conversationId}
          isLast={idx === tasks.length - 1}
          isActive={matchesActiveKey(task, activeTaskKey)}
          onOpenToolDetails={onOpenToolDetails}
          itemProps={itemProps}
        />
      ))}
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
