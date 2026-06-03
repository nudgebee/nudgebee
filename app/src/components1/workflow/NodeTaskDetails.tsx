import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@components1/ds/Button';
import CloseIcon from '@mui/icons-material/Close';
import apiWorkflow from '@api1/workflow';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { colors } from 'src/utils/colors';
import Datetime from '@components1/common/format/Datetime';
import JsonTreeView from '@components1/common/JsonTreeView';
import type { WorkflowExecutionTaskResponse } from '@api1/workflow/types';
import Loader from '@components1/common/Loader';

interface NodeTaskDetailsProps {
  executionId: string;
  workflowId: string;
  accountId: string;
  nodeId: string;
  nodeLabel: string;
  nodeInternalName: string;
  onClose: () => void;
}

// Helper function to filter tasks based on node type
const filterTasksForNode = (tasks: WorkflowExecutionTaskResponse[], nodeInternalName: string, nodeId: string): WorkflowExecutionTaskResponse[] => {
  if (!tasks || tasks.length === 0) {
    return [];
  }

  // Filter tasks that match the node by type or ID
  const filtered = tasks.filter((task) => {
    // Check if task type matches the node internal name (e.g., 'llm.summary')
    const typeMatches = task.type === nodeInternalName;

    // Check if task ID matches the node ID
    const idMatches = task.id === nodeId;

    return typeMatches || idMatches;
  });

  return filtered;
};

const NodeTaskDetails: React.FC<NodeTaskDetailsProps> = ({ executionId, workflowId, accountId, nodeId, nodeLabel, nodeInternalName, onClose }) => {
  const [tasks, setTasks] = useState<WorkflowExecutionTaskResponse[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedTask, setSelectedTask] = useState<WorkflowExecutionTaskResponse | null>(null);

  useEffect(() => {
    if (executionId && workflowId && accountId) {
      fetchExecutionDetails();
    }
  }, [executionId, workflowId, accountId, nodeId]);

  const fetchExecutionDetails = async () => {
    try {
      setLoading(true);

      const response: any = await apiWorkflow.getWorkflowExecution(accountId, workflowId, executionId);

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        console.error('Failed to fetch workflow execution details:', errorMessage);
        setTasks([]);
        return;
      }

      const executionData = response.data?.workflow_get_execution;
      const allTasks = executionData?.tasks || [];

      // Filter tasks to match the clicked node
      const filteredTasks = filterTasksForNode(allTasks, nodeInternalName, nodeId);

      setTasks(filteredTasks);

      // Auto-select first task if available
      if (filteredTasks.length > 0) {
        setSelectedTask(filteredTasks[0]);
      }
    } catch (error) {
      console.error('Failed to fetch workflow execution details:', error);
      setTasks([]);
    } finally {
      setLoading(false);
    }
  };

  const formatDateTime = (dateString: string) => {
    if (!dateString) {
      return 'N/A';
    }
    const date = new Date(dateString.endsWith('Z') ? dateString : dateString + 'Z');
    return date.toLocaleString();
  };

  const renderTaskDetails = (task: WorkflowExecutionTaskResponse) => (
    <Box
      sx={{
        padding: 'var(--ds-space-4)',
        backgroundColor: colors.background.primaryLightest,
        borderRadius: 'var(--ds-radius-md)',
        marginTop: 'var(--ds-space-1)',
      }}
    >
      <Box sx={{ marginBottom: 'var(--ds-space-3)', fontSize: 'var(--ds-text-body)' }}>
        <Box sx={{ marginBottom: 'var(--ds-space-1)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.tertiary }}>
            Started:
          </Typography>{' '}
          <Datetime value={task.start_time} />
        </Box>

        {task.end_time && (
          <Box sx={{ marginBottom: 'var(--ds-space-1)' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.tertiary }}>
              Ended:
            </Typography>{' '}
            <Datetime value={task.end_time} />
          </Box>
        )}

        <Box sx={{ marginBottom: 'var(--ds-space-1)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.tertiary }}>
            Attempt:
          </Typography>{' '}
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.text.secondary }}>{task.attempt || 1}</Typography>
        </Box>
      </Box>

      {task.error && (
        <Box sx={{ marginBottom: 'var(--ds-space-3)' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: colors.text.tertiary,
              marginBottom: 'var(--ds-space-1)',
            }}
          >
            Error:
          </Typography>
          <Box
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-red-600)',
              wordWrap: 'break-word',
              whiteSpace: 'pre-wrap',
              backgroundColor: 'var(--ds-red-100)',
              padding: 'var(--ds-space-2)',
              borderRadius: 'var(--ds-radius-sm)',
              border: '1px solid var(--ds-red-200)',
            }}
          >
            {task.error}
          </Box>
        </Box>
      )}

      {task.output && (
        <Box sx={{ marginBottom: 'var(--ds-space-3)' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: colors.text.tertiary,
              marginBottom: 'var(--ds-space-1)',
            }}
          >
            Output:
          </Typography>
          <JsonTreeView
            data={task.output}
            defaultExpanded={2}
            maxHeight='200px'
            fontSize='11px'
            templatePrefix={task.id ? `Tasks['${task.id}'].output` : undefined}
          />
        </Box>
      )}

      {task.children && (
        <Box>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: colors.text.tertiary,
              marginBottom: 'var(--ds-space-1)',
            }}
          >
            {task.type === 'sub-workflow' ? 'Sub-Automation Tasks:' : 'Children:'}
          </Typography>

          {task.type === 'sub-workflow' && Array.isArray(task.children) ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              {task.children.map((childTask: any, index: number) => (
                <Box
                  key={childTask.id || index}
                  sx={{
                    backgroundColor: 'var(--ds-background-200)',
                    padding: 'var(--ds-space-2)',
                    borderRadius: 'var(--ds-radius-sm)',
                    border: '1px solid var(--ds-gray-200)',
                    fontSize: 'var(--ds-text-caption)',
                  }}
                >
                  <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--ds-space-1)' }}>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: colors.text.secondary }}>
                      {childTask.id}
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary, fontFamily: 'monospace' }}>
                      {childTask.type}
                    </Typography>
                  </Box>

                  {childTask.params && (
                    <Box sx={{ marginTop: 'var(--ds-space-1)' }}>
                      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>Params:</Typography>
                      <JsonTreeView data={childTask.params} defaultExpanded={1} maxHeight='80px' fontSize='10px' />
                    </Box>
                  )}

                  {childTask.depends_on && childTask.depends_on.length > 0 && (
                    <Box sx={{ marginTop: 'var(--ds-space-1)' }}>
                      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>Depends on:</Typography>
                      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary, fontFamily: 'monospace' }}>
                        {childTask.depends_on.join(', ')}
                      </Box>
                    </Box>
                  )}
                </Box>
              ))}
            </Box>
          ) : (
            <JsonTreeView data={task.children} defaultExpanded={2} maxHeight='300px' fontSize='11px' />
          )}
        </Box>
      )}
    </Box>
  );

  return (
    <Box
      sx={{
        position: 'absolute',
        top: '80px',
        right: '16px',
        width: '380px',
        height: 'calc(100vh - 100px)',
        backgroundColor: 'white',
        zIndex: 20,
        border: '3px solid rgb(170, 144, 235)',
        borderRadius: 'var(--ds-radius-xl)',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {/* Header */}
      <Box
        sx={{
          padding: 'var(--ds-space-4) var(--ds-space-4)',
          borderBottom: '1px solid var(--ds-brand-150)',
          borderTop: '1px solid var(--ds-brand-150)',
          borderRadius: 'var(--ds-radius-xl) var(--ds-radius-xl) 0 0',
          backgroundColor: colors.background.primaryLightest,
          display: 'flex',
          flexDirection: 'row',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>Node Execution Details</Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: colors.text.secondary,
              margin: 0,
              fontFamily: 'poppins',
              letterSpacing: '-0.010em',
            }}
          >
            {nodeLabel}
          </Typography>
        </Box>
        <Button
          composition='icon-only'
          tone='ghost'
          size='sm'
          aria-label='Close'
          icon={<CloseIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-600)' }} />}
          onClick={onClose}
        />
      </Box>

      {/* Content */}
      <Box sx={{ flex: 1, overflowY: 'auto', padding: 'var(--ds-space-4)' }}>
        {loading ? (
          <Loader style={{ height: '100%', width: '100%' }} />
        ) : tasks.length === 0 ? (
          <Box sx={{ padding: 'var(--ds-space-4)', textAlign: 'center', color: 'var(--ds-gray-600)' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', marginBottom: 'var(--ds-space-2)', fontWeight: 'var(--ds-font-weight-medium)' }}>
              No tasks found for this node
            </Typography>
            <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>
              Node:{' '}
              <Typography component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {nodeLabel}
              </Typography>
            </Typography>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', marginTop: 'var(--ds-space-2)', color: 'var(--ds-gray-400)' }}>
              This node may not have executed any tasks in this execution.
            </Typography>
          </Box>
        ) : (
          <>
            <Box sx={{ marginBottom: 'var(--ds-space-4)', display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: colors.text.secondary }}>
                Tasks for this node
              </Typography>
              <Typography sx={{ margin: 0, fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-600)' }}>({tasks.length})</Typography>
            </Box>

            {tasks.map((task) => (
              <Box key={task.id} sx={{ marginBottom: 'var(--ds-space-3)' }}>
                <Box
                  onClick={() => setSelectedTask(selectedTask?.id === task.id ? null : task)}
                  sx={{
                    padding: 'var(--ds-space-3)',
                    border: selectedTask?.id === task.id ? '2px solid #3b82f6' : '1px solid #e5e7eb',
                    borderRadius: 'var(--ds-radius-md)',
                    cursor: 'pointer',
                    backgroundColor: selectedTask?.id === task.id ? '#eff6ff' : 'white',
                    transition: 'all 0.2s ease',
                    '&:hover': {
                      backgroundColor: selectedTask?.id === task.id ? '#eff6ff' : '#f9fafb',
                      borderColor: selectedTask?.id === task.id ? '#3b82f6' : '#d1d5db',
                    },
                  }}
                >
                  <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <Box sx={{ flex: 1 }}>
                      <Typography
                        sx={{
                          fontSize: 'var(--ds-text-body)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          color: 'var(--ds-brand-700)',
                          marginBottom: 'var(--ds-space-1)',
                        }}
                      >
                        {nodeLabel}
                      </Typography>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                        <CustomLabels text={task.status.toUpperCase()} />
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
                          {formatDateTime(task.start_time)}
                        </Typography>
                      </Box>
                    </Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)' }}>
                      {selectedTask?.id === task.id ? '▼' : '▶'}
                    </Typography>
                  </Box>
                </Box>

                {selectedTask?.id === task.id && renderTaskDetails(task)}
              </Box>
            ))}
          </>
        )}
      </Box>
    </Box>
  );
};

export default NodeTaskDetails;
