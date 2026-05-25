import React, { useState } from 'react';
import { Box, Button, Typography, Collapse } from '@mui/material';
import { colors } from 'src/utils/colors';
import CustomTooltip from './CustomTooltip';
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
import CustomDivider from './CustomDivider';
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
        backgroundColor: colors.background.white,
        marginRight: '8px',
        borderRadius: '8px',
        border: `1px solid ${colors.border.secondaryLightest}`,
        position: 'sticky',
        top: 0,
      }}
    >
      {/* Header */}
      <Box
        sx={{
          p: '16px 20px',
          borderBottom: `1px solid ${colors.border.secondaryLightest}`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <SafeIcon src={checklistIcon} alt='checklist' width={18} height={18} />
          <Typography
            variant='h6'
            sx={{
              fontSize: '13px',
              fontWeight: 500,
              color: colors.text.secondary,
              letterSpacing: '-0.02em',
              fontFamily: `"Poppins", sans-serif`,
            }}
          >
            Upgrade Steps
          </Typography>
        </Box>

        {/* Pending Tasks Count */}
        {getPendingTasksCount() > 0 && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <SafeIcon src={timelapse} alt='pending' width={14} height={14} style={{ opacity: 0.8 }} />
            <Typography
              sx={{
                fontSize: '11px',
                fontWeight: 400,
                color: colors.text.tertiary,
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
          p: '12px',
          overflowY: 'auto',
          '&::-webkit-scrollbar': {
            width: '3px',
          },
          '&::-webkit-scrollbar-track': {
            background: 'transparent',
          },
          '&::-webkit-scrollbar-thumb': {
            background: 'rgb(212, 212, 212)',
            borderRadius: '2px',
            '&:hover': {
              background: '#D1D5DB',
            },
          },
          '&::-webkit-scrollbar-thumb:active': {
            background: '#9CA3AF',
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
              <Button
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
                  gap: 1.5,
                  py: '8px',
                  borderRadius: 2,
                  textTransform: 'none',
                  minHeight: '48px',
                  backgroundColor: 'transparent',
                  transition: 'all 0.2s ease-in-out',
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
                    fontSize: '12px',
                    fontWeight: 400,
                    backgroundColor: isCompleted ? '#F0FDF4' : colors.background.secondary,
                    color: colors.text.white,
                    flexShrink: 0,
                    border: isCompleted ? `1px solid #22C55E` : '1px solid transparent',
                  }}
                >
                  {isCompleted ? <SafeIcon src={checkIconBold} alt='check' width={16} height={16} /> : stepNumber}
                </Box>

                {/* Step Title */}
                <Typography
                  sx={{
                    fontSize: '13px',
                    fontWeight: 500,
                    color: isActive ? colors.text.primary : colors.text.secondary,
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
              </Button>

              {/* Tasks List (Collapsible) */}
              {showTasks && step.tasks?.length > 0 && (
                <Collapse in={isExpanded} timeout='auto' unmountOnExit>
                  <Box sx={{ ml: 2, mb: '6px', borderLeft: '1px solid #E5E7EB' }}>
                    {step.tasks.map((task) => {
                      const isActiveTask = activeTask === task.id;
                      const taskStatusIcon = getTaskStatusIcon(task.status);

                      return (
                        <Button
                          key={task.id}
                          onClick={() => handleTaskClick(step.id, task.id)}
                          sx={{
                            width: 'calc(100% - 8px)',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'flex-start',
                            gap: 1,
                            px: '8px',
                            py: '6px',
                            ml: '8px',
                            borderRadius: 1.5,
                            textTransform: 'none',
                            minHeight: '36px',
                            border: isActiveTask ? `1px solid ${colors.border.primaryLightest}` : '2px solid transparent',
                            borderLeft: isActiveTask ? `4px solid ${colors.border.primaryLightest}` : '4px solid transparent',
                          }}
                        >
                          {/* Task Status Icon */}
                          <CustomTooltip title={`${task.status}`} placement='bottom' tooltipClassName='custom-tooltip'>
                            <SafeIcon src={taskStatusIcon.src} alt={taskStatusIcon.alt} width={14} height={14} />
                          </CustomTooltip>

                          {/* Task Title */}
                          <Typography
                            sx={{
                              fontSize: '11px',
                              fontWeight: isActiveTask ? 500 : 400,
                              color: isActiveTask ? colors.text.primary : colors.text.secondary,
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
                                  color: '#EF4444',
                                  fontSize: '11px',
                                  fontWeight: 600,
                                  ml: 0.5,
                                }}
                              >
                                *
                              </Typography>
                            )}
                          </Typography>
                        </Button>
                      );
                    })}
                  </Box>
                </Collapse>
              )}

              <CustomDivider margin='6px 0px' borderColor={colors.border.tertiaryLightest} />
            </Box>
          );
        })}
      </Box>
    </Box>
  );
};

export default VerticalStepNavigation;
