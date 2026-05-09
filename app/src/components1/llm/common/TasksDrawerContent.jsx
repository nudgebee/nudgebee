import React, { useState } from 'react';
import { Box, Typography, Collapse } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import MessageItem from '../MessageItem';
import ToolDetails from './ToolDetails';

const TaskRow = ({ task, accountId, conversationId, isLast, defaultExpanded, itemProps }) => {
  const [expanded, setExpanded] = useState(Boolean(defaultExpanded));

  return (
    <Box>
      <MessageItem
        message={task}
        index={task.originalIndex ?? task.id ?? 0}
        isLastInGroup={isLast}
        isLastTaskOfLastGroup={false}
        isCollapsed={false}
        collapsedObj={{}}
        onToggle={() => setExpanded((p) => !p)}
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
        onOpenToolDetails={() => setExpanded((p) => !p)}
      />
      <Collapse in={expanded} unmountOnExit>
        <Box
          sx={{
            ml: '40px',
            mb: '12px',
            mt: '4px',
            p: '12px',
            borderRadius: '8px',
            border: `1px solid ${colors.border.secondaryLightest}`,
            backgroundColor: colors.background.tertiaryLightest,
          }}
        >
          <ToolDetails toolCall={task} accountId={accountId} conversationId={conversationId} />
        </Box>
      </Collapse>
    </Box>
  );
};

TaskRow.propTypes = {
  task: PropTypes.object.isRequired,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
  isLast: PropTypes.bool,
  defaultExpanded: PropTypes.bool,
  itemProps: PropTypes.object,
};

const matchesExpandedKey = (task, expandedTaskKey) => {
  if (expandedTaskKey == null) {
    return false;
  }
  const candidates = [task.id, task.tool_id, task.originalIndex];
  return candidates.some((c) => c != null && String(c) === String(expandedTaskKey));
};

const TasksDrawerContent = ({ tasks, accountId, conversationId, expandedTaskKey, itemProps }) => {
  if (!tasks || tasks.length === 0) {
    return (
      <Typography
        sx={{
          fontSize: '13px',
          color: colors.text.tertiary,
          fontFamily: 'Roboto',
          textAlign: 'center',
          mt: '24px',
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
          defaultExpanded={matchesExpandedKey(task, expandedTaskKey)}
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
  expandedTaskKey: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  itemProps: PropTypes.object,
};

export default TasksDrawerContent;
