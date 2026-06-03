import React, { useState } from 'react';
import { Box, Typography, Collapse } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import {
  checklistIcon,
  checkIconBold,
  MenuArrowDownIcon,
  checkFilledIcon,
  AskNudgebeeSkipIcon,
  RunningIcon,
  timelapseBlackSVG,
  AskNudgebeeErrorIcon,
  timelapse,
} from '@assets';
import { Divider } from '@components1/ds/Divider';
import SafeIcon from '@components1/common/SafeIcon';

interface Task {
  id: string;
  title: string;
  description: string;
  status: string;
  is_required?: boolean;
}

interface Step {
  id: string;
  title: string;
  description: string;
  tasks: Task[];
  sequence: number;
}

interface VerticalStepNavigationProps {
  steps: Step[];
  activeStep: number;
  activeTask?: string;
  onStepChange: (step: number, id: string) => void;
  onTaskChange?: (stepId: string, taskId: string) => void;
  stepErrors?: boolean[];
  showTasks?: boolean;
}

const VerticalStepNavigation: React.FC<VerticalStepNavigationProps> = ({
  steps,
  activeStep,
  activeTask,
  onStepChange,
  onTaskChange,
  showTasks = false,
}) => {
  const [expandedSteps, setExpandedSteps] = useState<Record<string, boolean>>(() => {
    // Initialize with the active step expanded if showTasks is true
    if (showTasks && steps.length > 0) {
      const activeStepData = steps[activeStep - 1];
      return activeStepData ? { [activeStepData.id]: true } : {};
    }
    return {};
  });

  // Helper function to check if all required tasks in a step are completed or skipped
  const areAllTasksCompleted = (step: Step) => {
    if (!step?.tasks || step.tasks.length === 0) {
      return false;
    }
    // Filter for required tasks only (is_required: true or undefined defaults to required)
    const requiredTasks = step.tasks.filter((task: Task) => task.is_required !== false);

    // If there are no required tasks, the step can be considered complete
    if (requiredTasks.length === 0) {
      return true;
    }

    return requiredTasks.every((task: Task) => task.status?.toLowerCase() === 'completed' || task.status?.toLowerCase() === 'skipped');
  };

  // Helper function to count pending tasks across all steps
  const getPendingTasksCount = () => {
    let pendingCount = 0;
    steps.forEach((step) => {
      if (step.tasks) {
        step.tasks.forEach((task) => {
          if (task.status?.toLowerCase() === 'pending') {
            pendingCount++;
          }
        });
      }
    });
    return pendingCount;
  };

  // Helper function to map task status to an icon asset
  const getTaskStatusIcon = (status: string) => {
    const normalizedStatus = status?.toLowerCase();
    switch (normalizedStatus) {
      case 'completed':
        return { src: checkFilledIcon, alt: 'completed' };
      case 'skipped':
        return { src: AskNudgebeeSkipIcon, alt: 'skipped' };
      case 'pending':
        return { src: timelapse, alt: 'pending' };
      case 'active':
        return { src: RunningIcon, alt: 'active' };
      case 'failed':
        return { src: AskNudgebeeErrorIcon, alt: 'failed' };
      default:
        return { src: timelapseBlackSVG, alt: 'pending' };
    }
  };

  const toggleStepExpansion = (stepId: string) => {
    setExpandedSteps((prev) => ({
      ...prev,
      [stepId]: !prev[stepId],
    }));
  };

  const handleTaskClick = (stepId: string, taskId: string) => {
    if (onTaskChange) {
      onTaskChange(stepId, taskId);
    }
  };

  return (
    <Box
      sx={{
        width: '100%',
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        backgroundColor: 'var(--ds-background-100)',
        marginRight: 'var(--ds-space-2)',
        borderRadius: 'var(--ds-radius-lg)',
        border: '1px solid var(--ds-gray-300)',
        position: 'sticky',
        top: 0,
      }}
    >
      {/* Header */}
      <Box
        sx={{
          p: 'var(--ds-space-4) var(--ds-space-4)',
          borderBottom: '1px solid var(--ds-gray-300)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <SafeIcon src={checklistIcon} alt='checklist' width={18} height={18} />
          <Typography
            variant='h6'
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-700)',
              letterSpacing: '-0.02em',
              fontFamily: 'var(--ds-font-display)',
            }}
          >
            Upgrade Steps
          </Typography>
        </Box>

        {/* Pending Tasks Count */}
        {getPendingTasksCount() > 0 && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <SafeIcon src={timelapse} alt='pending' width={14} height={14} style={{ opacity: 0.8 }} />
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-600)',
              }}
            >
              {getPendingTasksCount()} pending
            </Typography>
          </Box>
        )}
      </Box>

      {/* Steps List */}
      <Box
        sx={{
          flex: 1,
          p: 'var(--ds-space-3)',
          overflowY: 'auto',
          '&::-webkit-scrollbar': {
            width: '3px',
          },
          '&::-webkit-scrollbar-track': {
            background: 'transparent',
          },
          '&::-webkit-scrollbar-thumb': {
            background: 'var(--ds-gray-300)',
            borderRadius: 'var(--ds-radius-sm)',
            '&:hover': {
              background: 'var(--ds-gray-400)',
            },
          },
          '&::-webkit-scrollbar-thumb:active': {
            background: 'var(--ds-gray-500)',
          },
        }}
      >
        {steps.map((step, index) => {
          const stepNumber = index + 1;
          const isActive = activeStep === stepNumber;
          const isCompleted = areAllTasksCompleted(step);
          const isExpanded = expandedSteps[step.id];

          return (
            <Box key={step.id || index}>
              {/* Step Button */}
              <Box
                component='button'
                type='button'
                onClick={() => {
                  if (showTasks && step.tasks?.length > 0) {
                    toggleStepExpansion(step.id);
                  }
                  onStepChange(stepNumber, step.id || String(stepNumber));
                }}
                sx={{
                  width: '100%',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'flex-start',
                  gap: 'var(--ds-space-3)',
                  py: 'var(--ds-space-2)',
                  px: 'var(--ds-space-2)',
                  borderRadius: 'var(--ds-radius-md)',
                  border: 'none',
                  minHeight: '48px',
                  backgroundColor: 'transparent',
                  cursor: 'pointer',
                  transition: 'background-color var(--ds-motion-micro) var(--ds-motion-ease)',
                  '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
                }}
              >
                {/* Step Number Circle */}
                <Box
                  sx={{
                    width: 22,
                    height: 22,
                    borderRadius: '50%',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    backgroundColor: isCompleted ? 'var(--ds-green-100)' : 'var(--ds-gray-700)',
                    color: 'var(--ds-background-100)',
                    flexShrink: 0,
                    border: isCompleted ? '1px solid var(--ds-green-400)' : '1px solid transparent',
                  }}
                >
                  {isCompleted ? <SafeIcon src={checkIconBold} alt='check' width={16} height={16} /> : stepNumber}
                </Box>

                {/* Step Title */}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: isActive ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
                    textAlign: 'left',
                    lineHeight: 1.2,
                    flex: 1,
                  }}
                >
                  {step?.title || ''}
                </Typography>

                {/* Expand/Collapse Icon for steps with tasks */}
                {showTasks && step.tasks?.length > 0 && (
                  <Box sx={{ ml: 1, display: 'flex', alignItems: 'center' }}>
                    {isExpanded ? (
                      <SafeIcon src={MenuArrowDownIcon} alt='collapse' width={18} height={18} style={{ transform: 'rotate(180deg)', opacity: 0.5 }} />
                    ) : (
                      <SafeIcon src={MenuArrowDownIcon} alt='expand' width={18} height={18} style={{ opacity: 0.5 }} />
                    )}
                  </Box>
                )}
              </Box>

              {/* Tasks List (Collapsible) */}
              {showTasks && step.tasks?.length > 0 && (
                <Collapse in={isExpanded} timeout='auto' unmountOnExit>
                  <Box sx={{ ml: 2, mt: 'var(--ds-space-1)', mb: 'var(--ds-space-2)', borderLeft: '1px solid var(--ds-gray-200)' }}>
                    {step.tasks.map((task) => {
                      const isActiveTask = activeTask === task.id;
                      const taskStatusIcon = getTaskStatusIcon(task.status);

                      return (
                        <Box
                          component='button'
                          type='button'
                          key={task.id}
                          onClick={() => handleTaskClick(step.id, task.id)}
                          sx={{
                            width: 'calc(100% - 8px)',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'flex-start',
                            gap: 'var(--ds-space-2)',
                            px: 'var(--ds-space-2)',
                            py: 'var(--ds-space-1)',
                            ml: 'var(--ds-space-2)',
                            borderRadius: 'var(--ds-radius-sm)',
                            border: isActiveTask ? '1px solid var(--ds-blue-300)' : '2px solid transparent',
                            borderLeft: isActiveTask ? '4px solid var(--ds-blue-300)' : '4px solid transparent',
                            minHeight: '36px',
                            backgroundColor: isActiveTask ? 'var(--ds-blue-100)' : 'transparent',
                            cursor: 'pointer',
                            transition: 'background-color var(--ds-motion-micro) var(--ds-motion-ease)',
                            '&:hover': { backgroundColor: isActiveTask ? 'var(--ds-blue-100)' : 'var(--ds-gray-100)' },
                          }}
                        >
                          {/* Task Status Icon */}
                          <Tooltip title={`${task.status}`} placement='bottom' tooltipClassName='custom-tooltip'>
                            <SafeIcon src={taskStatusIcon.src} alt={taskStatusIcon.alt} width={14} height={14} />
                          </Tooltip>

                          {/* Task Title */}
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-caption)',
                              fontWeight: isActiveTask ? 500 : 400,
                              color: isActiveTask ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
                              textAlign: 'left',
                              lineHeight: 1.3,
                              flex: 1,
                              wordWrap: 'break-word',
                              whiteSpace: 'normal',
                            }}
                          >
                            {task.title}
                            {task.is_required !== false && (
                              <Typography
                                component='span'
                                sx={{
                                  color: 'var(--ds-red-500)',
                                  fontSize: 'var(--ds-text-caption)',
                                  fontWeight: 'var(--ds-font-weight-semibold)',
                                  ml: 0.5,
                                }}
                              >
                                *
                              </Typography>
                            )}
                          </Typography>
                        </Box>
                      );
                    })}
                  </Box>
                </Collapse>
              )}

              <Divider color='var(--ds-gray-100)' sx={{ my: 'var(--ds-space-1)' }} />
            </Box>
          );
        })}
      </Box>
    </Box>
  );
};

export default VerticalStepNavigation;
