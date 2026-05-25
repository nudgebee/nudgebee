import React, { useState, useEffect } from 'react';
import { Box, Typography, IconButton } from '@mui/material';
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
    <Box sx={{ padding: '16px', backgroundColor: colors.background.primaryLightest, borderRadius: '6px', marginTop: '6px' }}>
      <Box sx={{ marginBottom: '12px', fontSize: '13px' }}>
        <Box sx={{ marginBottom: '6px' }}>
          <Typography sx={{ fontSize: '12px', fontWeight: '400', color: colors.text.tertiary }}>Started:</Typography>{' '}
          <Datetime value={task.start_time} />
        </Box>

        {task.end_time && (
          <Box sx={{ marginBottom: '6px' }}>
            <Typography sx={{ fontSize: '12px', fontWeight: '400', color: colors.text.tertiary }}>Ended:</Typography>{' '}
            <Datetime value={task.end_time} />
          </Box>
        )}

        <Box sx={{ marginBottom: '6px' }}>
          <Typography sx={{ fontSize: '12px', fontWeight: '400', color: colors.text.tertiary }}>Attempt:</Typography>{' '}
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{task.attempt || 1}</Typography>
        </Box>
      </Box>

      {task.error && (
        <Box sx={{ marginBottom: '12px' }}>
          <Typography sx={{ fontSize: '12px', fontWeight: '400', color: colors.text.tertiary, marginBottom: '4px' }}>Error:</Typography>
          <Box
            sx={{
              fontSize: '11px',
              color: '#dc2626',
              wordWrap: 'break-word',
              whiteSpace: 'pre-wrap',
              backgroundColor: '#fef2f2',
              padding: '8px',
              borderRadius: '4px',
              border: '1px solid #fecaca',
            }}
          >
            {task.error}
          </Box>
        </Box>
      )}

      {task.output && (
        <Box sx={{ marginBottom: '12px' }}>
          <Typography sx={{ fontSize: '12px', fontWeight: '400', color: colors.text.tertiary, marginBottom: '4px' }}>Output:</Typography>
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
          <Typography sx={{ fontSize: '12px', fontWeight: '400', color: colors.text.tertiary, marginBottom: '4px' }}>
            {task.type === 'sub-workflow' ? 'Sub-Automation Tasks:' : 'Children:'}
          </Typography>

          {task.type === 'sub-workflow' && Array.isArray(task.children) ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
              {task.children.map((childTask: any, index: number) => (
                <Box
                  key={childTask.id || index}
                  sx={{
                    backgroundColor: '#f8f9fa',
                    padding: '8px',
                    borderRadius: '4px',
                    border: '1px solid #e9ecef',
                    fontSize: '11px',
                  }}
                >
                  <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '4px' }}>
                    <Typography sx={{ fontSize: '11px', fontWeight: '500', color: colors.text.secondary }}>{childTask.id}</Typography>
                    <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, fontFamily: 'monospace' }}>{childTask.type}</Typography>
                  </Box>

                  {childTask.params && (
                    <Box sx={{ marginTop: '4px' }}>
                      <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Params:</Typography>
                      <JsonTreeView data={childTask.params} defaultExpanded={1} maxHeight='80px' fontSize='10px' />
                    </Box>
                  )}

                  {childTask.depends_on && childTask.depends_on.length > 0 && (
                    <Box sx={{ marginTop: '4px' }}>
                      <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Depends on:</Typography>
                      <Box sx={{ fontSize: '10px', color: colors.text.secondary, fontFamily: 'monospace' }}>{childTask.depends_on.join(', ')}</Box>
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
        borderRadius: '12px',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {/* Header */}
      <Box
        sx={{
          padding: '16px 20px',
          borderBottom: '1px solid #e5e7eb',
          borderTop: '1px solid #e5e7eb',
          borderRadius: '12px 12px 0 0',
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
          <Typography sx={{ fontSize: '11px', color: '#6b7280' }}>Node Execution Details</Typography>
          <Typography
            sx={{ fontSize: '16px', fontWeight: '600', color: colors.text.secondary, margin: 0, fontFamily: 'poppins', letterSpacing: '-0.010em' }}
          >
            {nodeLabel}
          </Typography>
        </Box>
        <IconButton
          onClick={onClose}
          sx={{
            color: '#6b7280',
            padding: '4px',
          }}
        >
          <CloseIcon sx={{ fontSize: '18px' }} />
        </IconButton>
      </Box>

      {/* Content */}
      <Box sx={{ flex: 1, overflowY: 'auto', padding: '16px' }}>
        {loading ? (
          <Loader style={{ height: '100%', width: '100%' }} />
        ) : tasks.length === 0 ? (
          <Box sx={{ padding: '20px', textAlign: 'center', color: '#6b7280' }}>
            <Typography sx={{ fontSize: '14px', marginBottom: '8px', fontWeight: '500' }}>No tasks found for this node</Typography>
            <Typography sx={{ fontSize: '12px' }}>
              Node:{' '}
              <Typography component='span' sx={{ fontWeight: '600' }}>
                {nodeLabel}
              </Typography>
            </Typography>
            <Typography sx={{ fontSize: '11px', marginTop: '8px', color: '#9ca3af' }}>
              This node may not have executed any tasks in this execution.
            </Typography>
          </Box>
        ) : (
          <>
            <Box sx={{ marginBottom: '16px', display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '4px' }}>
              <Typography sx={{ fontSize: '14px', fontWeight: '500', color: colors.text.secondary }}>Tasks for this node</Typography>
              <Typography sx={{ margin: 0, fontSize: '14px', color: '#6b7280' }}>({tasks.length})</Typography>
            </Box>

            {tasks.map((task) => (
              <Box key={task.id} sx={{ marginBottom: '12px' }}>
                <Box
                  onClick={() => setSelectedTask(selectedTask?.id === task.id ? null : task)}
                  sx={{
                    padding: '12px',
                    border: selectedTask?.id === task.id ? '2px solid #3b82f6' : '1px solid #e5e7eb',
                    borderRadius: '6px',
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
                      <Typography sx={{ fontSize: '13px', fontWeight: '500', color: '#1f2937', marginBottom: '6px' }}>{nodeLabel}</Typography>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                        <CustomLabels text={task.status.toUpperCase()} />
                        <Typography sx={{ fontSize: '11px', color: '#6b7280' }}>{formatDateTime(task.start_time)}</Typography>
                      </Box>
                    </Box>
                    <Typography sx={{ fontSize: '12px', color: '#9ca3af' }}>{selectedTask?.id === task.id ? '▼' : '▶'}</Typography>
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
