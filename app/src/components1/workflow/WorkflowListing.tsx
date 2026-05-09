import apiWorkflow from '@api1/workflow';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiUser from '@api1/user';
import type { WorkflowCreateRequest } from '@api1/workflow/types';
import { useEffect, useState, useCallback, useRef, createContext, useContext } from 'react';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { Box, DialogContent, DialogContentText, Link, Tooltip } from '@mui/material';
import { Text } from '@components1/common';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { useRouter } from 'next/router';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { Modal } from '@components1/common/modal';
import { snackbar } from '@components1/common/snackbarService';
import { hasWriteAccess, hasFeatureAccess } from '@lib/auth';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { action } from 'src/utils/actionStyles';
import TriggerWorkflowModal from './components/TriggerWorkflowModal';
import AiGenerateWorkflowModal from './components/AiGenerateWorkflowModal';
import ConfigurationManager from './ConfigurationManager';
import CreateWorkflowOptionsModal from './components/CreateWorkflowOptionsModal';
import WorkflowTemplatesModal from './components/WorkflowTemplatesModal';
import {
  manualTriggerIcon,
  SettingsIcon,
  workflowCalendarIcon,
  workflowWebhookIcon,
  EventIconPurple,
  addIconWhite,
  EditIcon,
  CopyIconBlue,
  DeleteIconRed,
} from '@assets';
import CustomButton from '@components1/common/NewCustomButton';
import CustomIconButton from '@components1/CustomIconButton';
import { applyFiltersOnRouter } from '@lib/router';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { Refresh, StopCircleOutlined } from '@mui/icons-material';

// Icons for menu items
const pauseIcon = require('@assets/m_block.svg');
const playIcon = require('@assets/play-circle.svg');

// Statuses that indicate Temporal has finished with an execution. Shared
// between the post-trigger polling loop and the post-cancel reconcile loop
// so both agree on what counts as "done."
const TERMINAL_EXECUTION_STATUSES = ['CANCELED', 'CANCELLED', 'COMPLETED', 'COMPLETE', 'COMPLETE_WITH_ERROR', 'FAILED', 'TERMINATED', 'TIMED_OUT'];

// Snapshot of the most recent polled state for a triggered execution.
// `closeTime` is only set once Temporal reports a terminal status;
// `startTime` is seeded locally at trigger and replaced with the server
// value once the first poll returns.
interface ExecutionSnapshot {
  status: string;
  startTime?: string;
  closeTime?: string;
}

// Map of workflowId -> latest polled execution snapshot. Lives in context so
// row cells (which are baked into static `data` at fetch time) can re-render
// on status updates via context subscription, without rebuilding the table
// or refetching the listing.
const LiveExecutionStatusContext = createContext<Record<string, ExecutionSnapshot>>({});

const formatExecutionStatus = (status: string): string =>
  status
    .toLowerCase()
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase());

interface WorkflowActionsCellProps {
  workflow: any;
  accountId: string | undefined;
  onStop: (workflow: any) => void;
  onEdit: (workflowId: string) => void;
  getMenuItems: (workflow: any) => { label: string; id: number; icon: any; disabled?: boolean }[];
  onMenuClick: (menuItem: any, workflow: any) => void;
}

const WorkflowActionsCell: React.FC<WorkflowActionsCellProps> = ({ workflow, accountId, onStop, onEdit, getMenuItems, onMenuClick }) => {
  const liveStatuses = useContext(LiveExecutionStatusContext);
  if (!accountId || !hasWriteAccess(accountId)) return <></>;
  const liveStatus = liveStatuses[workflow.id]?.status;
  // Prefer live status from active polling so the Cancel button stays in
  // sync mid-execution; fall back to the listing's last_execution_status.
  const effectiveStatus = (liveStatus || workflow.last_execution_status || '').toUpperCase();
  const isRunning = ['RUNNING', 'IN_PROGRESS'].includes(effectiveStatus);
  const tooltipStatus = liveStatus || effectiveStatus;
  const cancelTooltip = tooltipStatus ? `Execution status: ${formatExecutionStatus(tooltipStatus)}` : 'Cancel running execution';
  return (
    <Box sx={{ display: 'flex', justifyContent: 'flex-end', mr: '10px', gap: '6px' }}>
      {isRunning && (
        <Tooltip title={cancelTooltip} arrow placement='top'>
          <span>
            <CustomButton
              id={`workflow-stop-btn-${workflow.id}`}
              text='Cancel'
              variant='secondary'
              startIcon={<StopCircleOutlined sx={{ fontSize: 14, color: colors.error }} />}
              size='xSmall'
              sx={{ color: colors.error, borderColor: colors.error }}
              onClick={() => onStop(workflow)}
            />
          </span>
        </Tooltip>
      )}
      <CustomButton
        id={`workflow-edit-btn-${workflow.id}`}
        text='Edit'
        variant='secondary'
        startIcon={<SafeIcon style={{ height: '14px', width: '14px' }} src={EditIcon} alt={'edit'} />}
        size='xSmall'
        onClick={() => onEdit(workflow.id)}
      />
      <ThreeDotsMenu
        id={`workflow-menu-${workflow.id}`}
        sx={{ ...action.primary }}
        menuItems={getMenuItems(workflow)}
        data={workflow}
        onMenuClick={onMenuClick}
      />
    </Box>
  );
};

interface LastExecutionCellProps {
  workflow: any;
}

// Renders the "Last Execution" column. Reads LiveExecutionStatusContext so
// the row updates in place when a triggered execution transitions states —
// no listing refetch required.
const LastExecutionCell: React.FC<LastExecutionCellProps> = ({ workflow }) => {
  const liveStatuses = useContext(LiveExecutionStatusContext);
  const override = liveStatuses[workflow.id];
  const status = override?.status || workflow.last_execution_status;
  // Prefer terminal close time when present, then the just-triggered run's
  // start time (seeded locally on trigger, refined by polling), then the
  // server's last_execution_time from the listing.
  const time = override?.closeTime || override?.startTime || workflow.last_execution_time;
  if (!status) {
    return <Text value='No Executions yet' sx={{ fontSize: '12px', color: colors.text.tertiarymedium, fontStyle: 'italic' }} />;
  }
  return (
    <Box sx={{ display: 'flex', gap: 1, flexDirection: 'row', alignItems: 'center' }}>
      <CustomLabels text={status.toLowerCase()} textTransform='capitalize' />
      <Text value='|' secondaryText sx={{ fontSize: '12px', fontWeight: 400, opacity: 0.7 }} />
      <Datetime
        baseDate={new Date()}
        value={time}
        sxSuffix={{ fontSize: '11px', color: colors.text.tertiary }}
        sx={{ fontSize: '12px', color: colors.text.secondary }}
      />
    </Box>
  );
};

const WorkflowListing: React.FC<WorkflowListingProps> = ({ accountId }) => {
  const [data, setData] = useState<any[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [deleteModalOpen, setDeleteModalOpen] = useState<boolean>(false);
  const [pauseModalOpen, setPauseModalOpen] = useState<boolean>(false);
  const [resumeModalOpen, setResumeModalOpen] = useState<boolean>(false);
  const [stopExecutionModalOpen, setStopExecutionModalOpen] = useState<boolean>(false);
  const [stopExecutionLoading, setStopExecutionLoading] = useState<boolean>(false);
  const [triggerModalOpen, setTriggerModalOpen] = useState<boolean>(false);
  const [configModalOpen, setConfigModalOpen] = useState<boolean>(false);
  const [aiGenerateModalOpen, setAiGenerateModalOpen] = useState<boolean>(false);
  const [aiGenerateLoading, setAiGenerateLoading] = useState<boolean>(false);
  const [createWorkflowOptionsOpen, setCreateWorkflowOptionsOpen] = useState<boolean>(false);
  const [templateModalOpen, setTemplateModalOpen] = useState<boolean>(false);
  const [aiFeatureEnabled, setAiFeatureEnabled] = useState<boolean>(false);
  const [templateFeatureEnabled, setTemplateFeatureEnabled] = useState<boolean>(false);
  const [selectedWorkflow, setSelectedWorkflow] = useState<any>({ id: '', name: '' });
  const [triggerLoading, setTriggerLoading] = useState<boolean>(false);
  const router = useRouter();
  const [selectedStatus, setSelectedStatus] = useState<string>((router?.query?.status as string) || 'All');
  const [selectedLastExecutionStatus, setSelectedLastExecutionStatus] = useState<string>((router?.query?.last_execution_status as string) || 'All');
  const [selectedTriggerType, setSelectedTriggerType] = useState<string>((router?.query?.type as string) || '');
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [rowsPerPage, setRowsPerPage] = useState<number>(10);
  const [totalRows, setTotalRows] = useState<number>(0);

  const triggerTypeOptions = [
    { label: 'Manual', value: 'manual' },
    { label: 'Schedule', value: 'schedule' },
    { label: 'Webhook', value: 'webhook' },
    { label: 'Optimization', value: 'optimization' },
    { label: 'Event', value: 'event' },
  ];

  // Store offset tokens for each page (hybrid approach: use when available, calculate when not)
  // Key: page number, Value: offset as string (e.g., { 1: '', 2: '10', 3: '20' })
  const [pageOffsetTokens, setPageOffsetTokens] = useState<{ [page: number]: string }>({ 1: '' });

  // Refs mirroring pagination state so async callbacks (e.g. cancel polling)
  // read the latest values instead of closure-captured stale ones.
  const currentPageRef = useRef(1);
  const rowsPerPageRef = useRef(10);
  const pageOffsetTokensRef = useRef<{ [page: number]: string }>({ 1: '' });

  // Interval handle for post-cancel status polling; cleared on unmount.
  const cancelPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Per-workflow polling started after a manual trigger. Polls
  // getWorkflowExecution until Temporal returns a terminal status, then
  // refreshes the listing so the row's last_execution_status (and the
  // Cancel button visibility) updates without the user having to click
  // refresh. Keyed by workflow id so concurrent triggers don't trample
  // each other; all handles are cleared on unmount.
  const triggerPollsRef = useRef<Map<string, ReturnType<typeof setInterval>>>(new Map());

  // Latest polled execution snapshot per workflow id. Surfaced via
  // LiveExecutionStatusContext so the Cancel button's tooltip and the
  // Last Execution column reflect real-time status without refetching the
  // listing.
  const [liveExecutionStatuses, setLiveExecutionStatuses] = useState<Record<string, ExecutionSnapshot>>({});

  // Coalesces post-terminal listing refreshes. With many concurrent
  // triggers, multiple polls can hit a terminal status within a few seconds
  // of each other; without this we'd fire one full listWorkflows per
  // terminal event. The first terminal schedules a refresh ~1.5s out;
  // additional terminals during the window are absorbed into the same
  // pending refresh.
  const pendingListingRefreshRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    currentPageRef.current = currentPage;
  }, [currentPage]);
  useEffect(() => {
    rowsPerPageRef.current = rowsPerPage;
  }, [rowsPerPage]);
  useEffect(() => {
    pageOffsetTokensRef.current = pageOffsetTokens;
  }, [pageOffsetTokens]);

  useEffect(() => {
    return () => {
      if (cancelPollRef.current) {
        clearInterval(cancelPollRef.current);
        cancelPollRef.current = null;
      }
      triggerPollsRef.current.forEach((handle) => clearInterval(handle));
      triggerPollsRef.current.clear();
      if (pendingListingRefreshRef.current) {
        clearTimeout(pendingListingRefreshRef.current);
        pendingListingRefreshRef.current = null;
      }
    };
  }, []);

  const [searchName, setSearchName] = useState<string>((router?.query?.name as string) || '');
  const [selectedTags, setSelectedTags] = useState<string>((router?.query?.tags as string) || '');
  const [selectedCreatedBy, setSelectedCreatedBy] = useState<string>((router?.query?.created_by as string) || 'All');
  const [createdByOptions, setCreatedByOptions] = useState<string[]>(['All']);

  // Committed search values — only update on Enter or Clear, not on every keystroke.
  const [committedSearchName, setCommittedSearchName] = useState<string>((router?.query?.name as string) || '');
  const [committedSelectedTags, setCommittedSelectedTags] = useState<string>((router?.query?.tags as string) || '');

  const getTriggerIcon = (triggerType: string) => {
    const type = triggerType?.toLowerCase();
    switch (type) {
      case 'manual':
        return manualTriggerIcon;
      case 'schedule':
        return workflowCalendarIcon;
      case 'webhook':
        return workflowWebhookIcon;
      case 'event':
        return EventIconPurple;
      default:
        return manualTriggerIcon;
    }
  };

  const getMenuItems = (workflow: any): { label: string; id: number; icon: any; disabled?: boolean }[] => {
    const MENU_ITEMS: { label: string; id: number; icon: any; disabled?: boolean }[] = [];
    const isRunning = ['RUNNING', 'IN_PROGRESS'].includes(workflow?.last_execution_status?.toUpperCase());

    if (accountId && hasWriteAccess(accountId)) {
      // Add trigger option for all workflows
      MENU_ITEMS.push({
        label: 'Manual run',
        id: 3,
        icon: manualTriggerIcon,
        disabled: isRunning,
      });

      MENU_ITEMS.push({
        label: 'Duplicate',
        id: 4,
        icon: CopyIconBlue,
      });

      // Check if workflow has schedule trigger
      const pauseResumeApplicable = workflow?.definition?.triggers?.some((trigger: any) => ['schedule', 'event', 'webhook'].includes(trigger.type));

      if (pauseResumeApplicable) {
        // Show pause button only if workflow is not paused
        if (workflow?.status !== 'PAUSED') {
          MENU_ITEMS.push({
            label: 'Pause',
            id: 1,
            icon: pauseIcon,
          });
        } else {
          MENU_ITEMS.push({
            label: 'Resume',
            id: 2,
            icon: playIcon,
          });
        }
      }

      MENU_ITEMS.push({
        label: 'Delete',
        id: 0,
        icon: DeleteIconRed,
      });
    }

    return MENU_ITEMS;
  };

  const onMenuClick = (menuItem: any, workflow: any) => {
    if (menuItem.id === 0) {
      setSelectedWorkflow(workflow);
      setDeleteModalOpen(true);
    } else if (menuItem.id === 1) {
      setSelectedWorkflow(workflow);
      setPauseModalOpen(true);
    } else if (menuItem.id === 2) {
      setSelectedWorkflow(workflow);
      setResumeModalOpen(true);
    } else if (menuItem.id === 3) {
      if (workflow?.status === 'DRAFT') {
        snackbar.warning('Automation is in Draft status and cannot be executed. Set the status to Active to run it, or use Dry Run to test.');
        return;
      }
      setSelectedWorkflow(workflow);
      setTriggerModalOpen(true);
    } else if (menuItem.id === 4) {
      handleDuplicateWorkflow(workflow);
    }
  };

  const handleDeleteWorkflow = async () => {
    setLoading(true);
    try {
      const response = await apiWorkflow.deleteWorkflow(accountId!, selectedWorkflow.id);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
      } else {
        snackbar.success(`Automation "${selectedWorkflow.name}" deleted successfully`);
        // Refresh current page
        const offsetToken = pageOffsetTokens[currentPage] ?? ((currentPage - 1) * rowsPerPage).toString();
        listWorkflows(currentPage, rowsPerPage, offsetToken);
      }
    } catch (_error) {
      console.error(_error);
      snackbar.error(`Failed to delete automation "${selectedWorkflow.name}"`);
    } finally {
      setDeleteModalOpen(false);
      setSelectedWorkflow({ id: '', name: '' });
      setLoading(false);
    }
  };

  const handleCloseDeleteModal = () => {
    setDeleteModalOpen(false);
    setSelectedWorkflow({ id: '', name: '' });
  };

  const handlePauseWorkflow = async () => {
    setLoading(true);
    try {
      const response = await apiWorkflow.pauseWorkflow(accountId!, selectedWorkflow.id);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
      } else {
        snackbar.success(`Automation "${selectedWorkflow.name}" paused successfully`);
        // Refresh current page
        const offsetToken = pageOffsetTokens[currentPage] ?? ((currentPage - 1) * rowsPerPage).toString();
        listWorkflows(currentPage, rowsPerPage, offsetToken);
      }
    } catch (_error) {
      console.error(_error);

      snackbar.error(`Failed to pause automation "${selectedWorkflow.name}"`);
    } finally {
      setPauseModalOpen(false);
      setSelectedWorkflow({ id: '', name: '' });
      setLoading(false);
    }
  };

  const handleClosePauseModal = () => {
    setPauseModalOpen(false);
    setSelectedWorkflow({ id: '', name: '' });
  };

  const handleResumeWorkflow = async () => {
    setLoading(true);
    try {
      const response = await apiWorkflow.resumeWorkflow(accountId!, selectedWorkflow.id);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
      } else {
        snackbar.success(`Automation "${selectedWorkflow.name}" resumed successfully`);
        // Refresh current page
        const offsetToken = pageOffsetTokens[currentPage] ?? ((currentPage - 1) * rowsPerPage).toString();
        listWorkflows(currentPage, rowsPerPage, offsetToken);
      }
    } catch (_error) {
      console.error(_error);

      snackbar.error(`Failed to resume automation "${selectedWorkflow.name}"`);
    } finally {
      setResumeModalOpen(false);
      setSelectedWorkflow({ id: '', name: '' });
      setLoading(false);
    }
  };

  const handleCloseResumeModal = () => {
    setResumeModalOpen(false);
    setSelectedWorkflow({ id: '', name: '' });
  };

  const handleOpenStopExecutionModal = useCallback((workflow: any) => {
    setSelectedWorkflow(workflow);
    setStopExecutionModalOpen(true);
  }, []);

  const handleEditWorkflow = useCallback(
    (workflowId: string) => {
      router.push(`/workflow/${workflowId}?accountId=${accountId}`);
    },
    [router, accountId]
  );

  const handleCloseStopExecutionModal = () => {
    setStopExecutionModalOpen(false);
    setSelectedWorkflow({ id: '', name: '' });
  };

  const handleStopExecution = async () => {
    if (!accountId || !selectedWorkflow?.id) return;
    setStopExecutionLoading(true);
    try {
      // Fetch recent executions to find the running one
      const execResponse: any = await apiWorkflow.ListWorkflowExecutions(accountId, selectedWorkflow.id, 5);
      const execErrorMessage = parseHttpResponseBodyMessage(execResponse);
      if (execErrorMessage) {
        snackbar.error(`Failed to fetch executions: ${execErrorMessage}`);
        return;
      }

      const executions: any[] = execResponse?.data?.workflow_list_executions?.executions || [];
      const runningExecution = executions.find((e: any) => ['RUNNING', 'IN_PROGRESS'].includes(e.status?.toUpperCase()));

      if (!runningExecution) {
        snackbar.warning('No running execution found — it may have already completed');
        const offsetToken = pageOffsetTokens[currentPage] ?? ((currentPage - 1) * rowsPerPage).toString();
        listWorkflows(currentPage, rowsPerPage, offsetToken);
        return;
      }

      const cancelResponse: any = await apiWorkflow.cancelExecution({
        account_id: accountId,
        id: selectedWorkflow.id,
        execution_id: runningExecution.id,
      });

      const cancelErrorMessage = parseHttpResponseBodyMessage(cancelResponse);
      if (cancelErrorMessage) {
        snackbar.error(`Failed to stop execution: ${cancelErrorMessage}`);
        return;
      }

      const cancelMsg = cancelResponse?.data?.workflow_cancel_execution?.message;
      if (cancelMsg?.toLowerCase().includes('workflow execution canceled successfully')) {
        snackbar.success(cancelMsg);
      } else {
        snackbar.error(cancelMsg || 'Failed to stop execution');
      }

      // Poll the execution until Temporal propagates a terminal status,
      // then refresh using the latest pagination state from refs.
      // Uses refs so changes to page/filters during polling pick up fresh values.
      if (cancelPollRef.current) {
        clearInterval(cancelPollRef.current);
        cancelPollRef.current = null;
      }
      const pollWorkflowId = selectedWorkflow.id;
      const pollExecutionId = runningExecution.id;
      let attempts = 0;
      const maxAttempts = 10; // ~5s cap at 500ms intervals
      const refreshWithLatest = () => {
        const page = currentPageRef.current;
        const size = rowsPerPageRef.current;
        const token = pageOffsetTokensRef.current[page] ?? ((page - 1) * size).toString();
        listWorkflows(page, size, token);
      };
      const poll = async () => {
        attempts++;
        try {
          const execResp: any = await apiWorkflow.getWorkflowExecution(accountId, pollWorkflowId, pollExecutionId);
          const status = execResp?.data?.workflow_get_execution?.status?.toUpperCase() || '';
          if (TERMINAL_EXECUTION_STATUSES.includes(status) || attempts >= maxAttempts) {
            if (cancelPollRef.current) {
              clearInterval(cancelPollRef.current);
              cancelPollRef.current = null;
            }
            refreshWithLatest();
          }
        } catch {
          if (attempts >= maxAttempts && cancelPollRef.current) {
            clearInterval(cancelPollRef.current);
            cancelPollRef.current = null;
            refreshWithLatest();
          }
        }
      };
      poll();
      cancelPollRef.current = setInterval(poll, 500);
    } catch (_error) {
      console.error(_error);
      snackbar.error('Failed to stop execution');
    } finally {
      setStopExecutionLoading(false);
      setStopExecutionModalOpen(false);
      setSelectedWorkflow({ id: '', name: '' });
    }
  };

  const handleDuplicateWorkflow = async (workflow: any) => {
    if (!accountId) {
      snackbar.error('Account ID is required');
      return;
    }

    try {
      // Fetch full workflow definition (listing query does not include tasks)
      const fullWorkflowResponse: any = await apiWorkflow.getWorkflowById(accountId, workflow.id);
      const fullWorkflowErrorMessage = parseHttpResponseBodyMessage(fullWorkflowResponse);
      if (fullWorkflowErrorMessage) {
        snackbar.error(fullWorkflowErrorMessage);
        return;
      }

      const fullWorkflow = fullWorkflowResponse.data?.workflow_get;
      if (!fullWorkflow?.definition) {
        snackbar.error('Failed to fetch automation definition for duplication');
        return;
      }

      // Build create request with cloned definition
      const clonedDefinition = JSON.parse(JSON.stringify(fullWorkflow.definition));

      // Strip fields not accepted by WorkflowDefinitionTaskRequest (e.g. 'outputs')
      const cleanTasks = (tasks: any[]) => {
        if (!Array.isArray(tasks)) return;
        for (const task of tasks) {
          delete task.outputs;
          // Recursively clean nested tasks (e.g. core.foreach)
          if (Array.isArray(task.params?.tasks)) {
            cleanTasks(task.params.tasks);
          }
        }
      };
      if (clonedDefinition.tasks) {
        cleanTasks(clonedDefinition.tasks);
      }

      const createRequest: WorkflowCreateRequest = {
        account_id: accountId,
        workflow: {
          name: `Copy of ${fullWorkflow.name}`,
          definition: clonedDefinition,
          tags: fullWorkflow.tags ? JSON.parse(JSON.stringify(fullWorkflow.tags)) : {},
          status: 'DRAFT',
        },
      };

      const response: any = await apiWorkflow.createWorkflow(createRequest);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }

      const newWorkflowId = response.data?.workflow_create?.id;
      if (newWorkflowId) {
        snackbar.success(`Automation duplicated as "Copy of ${fullWorkflow.name}"`);
        // Refresh current page
        const offsetToken = pageOffsetTokens[currentPage] ?? ((currentPage - 1) * rowsPerPage).toString();
        listWorkflows(currentPage, rowsPerPage, offsetToken);
      } else {
        snackbar.error('Failed to get new automation ID after duplication');
      }
    } catch (error) {
      console.error('Error duplicating workflow:', error);
      snackbar.error(`Failed to duplicate automation "${workflow.name}"`);
    }
  };

  // Get default inputs for trigger based on workflow configuration
  const getDefaultTriggerInputs = (workflow: any) => {
    // Look for manual trigger configuration first
    const manualTrigger = workflow?.definition?.triggers?.find((trigger: any) => trigger.type === 'manual');
    if (manualTrigger?.params?.inputs) {
      return manualTrigger.params.inputs;
    }

    // Return empty object as default
    return {};
  };

  // Get input schema from workflow definition
  const getWorkflowInputSchema = (workflow: any) => {
    const inputs = workflow?.definition?.inputs;
    if (!inputs || !Array.isArray(inputs)) {
      return [];
    }

    return inputs.map((input: any) => ({
      id: input.id,
      type: input.type,
      description: input.description || `Input parameter: ${input.id}`,
      default: input.default,
    }));
  };

  // Get the primary trigger type for display
  const getPrimaryTriggerType = (workflow: any) => {
    const triggers = workflow?.definition?.triggers;
    if (!triggers || triggers.length === 0) {
      return 'manual';
    }

    // Prefer manual trigger if available, otherwise use first trigger
    const manualTrigger = triggers.find((trigger: any) => trigger.type === 'manual');
    if (manualTrigger) {
      return 'manual';
    }

    return triggers[0].type;
  };

  // Poll the just-triggered execution until Temporal reports a terminal
  // status (or we hit the safety cap), then refresh the listing so the row
  // reflects the new last_execution_status. The polling interval and
  // terminal-status set match the post-cancel polling above so behavior is
  // consistent.
  const startTriggerPolling = (workflowId: string, executionId: string) => {
    if (!accountId) return;
    const existing = triggerPollsRef.current.get(workflowId);
    if (existing) {
      clearInterval(existing);
      triggerPollsRef.current.delete(workflowId);
    }

    let attempts = 0;
    // ~10 minutes at 3s intervals — long enough for typical workflows but
    // bounded so a stuck execution doesn't poll forever in the user's tab.
    const maxAttempts = 200;

    const scheduleRefresh = () => {
      if (pendingListingRefreshRef.current) return;
      pendingListingRefreshRef.current = setTimeout(() => {
        pendingListingRefreshRef.current = null;
        const page = currentPageRef.current;
        const size = rowsPerPageRef.current;
        const token = pageOffsetTokensRef.current[page] ?? ((page - 1) * size).toString();
        listWorkflows(page, size, token);
      }, 1500);
    };

    const stopPolling = () => {
      const handle = triggerPollsRef.current.get(workflowId);
      if (handle) {
        clearInterval(handle);
        triggerPollsRef.current.delete(workflowId);
      }
    };

    const removeOverride = () => {
      setLiveExecutionStatuses((prev) => {
        if (!(workflowId in prev)) return prev;
        const next = { ...prev };
        delete next[workflowId];
        return next;
      });
    };

    const poll = async () => {
      attempts++;
      try {
        const resp: any = await apiWorkflow.getWorkflowExecution(accountId, workflowId, executionId);
        const exec = resp?.data?.workflow_get_execution;
        const status = (exec?.status || '').toUpperCase();
        const closeTime = exec?.close_time || undefined;
        const startTime = exec?.start_time || undefined;
        if (status) {
          setLiveExecutionStatuses((prev) => {
            const current = prev[workflowId];
            if (
              current &&
              current.status === status &&
              current.closeTime === closeTime &&
              // Don't downgrade a server-confirmed start_time back to the
              // locally-seeded one if a later poll happens to omit it.
              (!startTime || current.startTime === startTime)
            ) {
              return prev;
            }
            return {
              ...prev,
              [workflowId]: {
                status,
                startTime: startTime || current?.startTime,
                closeTime,
              },
            };
          });
        }
        if (TERMINAL_EXECUTION_STATUSES.includes(status)) {
          // Override stays in context so the row reflects the terminal
          // state without refetching the entire listing. It naturally
          // reconciles next time listWorkflows runs (filter / page change
          // / manual refresh).
          stopPolling();
          return;
        }
        if (attempts >= maxAttempts) {
          // Gave up without seeing a terminal status — drop the (stale)
          // override and fall back to a server-side reconcile so the row
          // doesn't display a forever-RUNNING label.
          stopPolling();
          removeOverride();
          scheduleRefresh();
        }
      } catch {
        if (attempts >= maxAttempts) {
          stopPolling();
          removeOverride();
          scheduleRefresh();
        }
      }
    };

    // Seed RUNNING + a local start time immediately so the row reflects the
    // new execution before the first poll round-trips (~3s). Server's
    // canonical start_time replaces this on the first successful poll.
    setLiveExecutionStatuses((prev) => ({ ...prev, [workflowId]: { status: 'RUNNING', startTime: new Date().toISOString() } }));
    const handle = setInterval(poll, 3000);
    triggerPollsRef.current.set(workflowId, handle);
  };

  const handleTriggerWorkflow = async (inputs: any) => {
    if (!selectedWorkflow.id || !accountId) {
      snackbar.error('Invalid automation or account ID');
      return;
    }

    setTriggerLoading(true);
    try {
      const response: any = await apiWorkflow.triggerWorkflow({
        account_id: accountId,
        id: selectedWorkflow.id,
        inputs: inputs,
      });

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        return;
      }

      const triggerData = response?.data?.workflow_trigger;
      if (triggerData?.execution_id) {
        snackbar.success(`Automation "${selectedWorkflow.name}" triggered successfully!`);
        // Begin polling the new execution. The poller seeds an immediate
        // RUNNING + startTime override so the row updates in place — no
        // need to refetch the listing here. The override is reconciled
        // with server data on the next user-driven listing fetch.
        startTriggerPolling(selectedWorkflow.id, triggerData.execution_id);
      } else {
        snackbar.error('Failed to get execution ID from automation trigger');
      }
    } catch (error) {
      console.error('Error triggering workflow:', error);
      snackbar.error(`Failed to trigger automation "${selectedWorkflow.name}"`);
    } finally {
      setTriggerLoading(false);
    }
  };

  const handleCloseTriggerModal = () => {
    setTriggerModalOpen(false);
    setSelectedWorkflow({ id: '', name: '' });
    setTriggerLoading(false);
  };

  const handleAiGenerateWorkflow = async (query: string) => {
    if (!accountId || !query.trim()) {
      snackbar.error('Invalid input');
      return;
    }

    setAiGenerateLoading(true);
    try {
      const response: any = await apiWorkflow.aiGenerateWorkflow(accountId, query);

      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        snackbar.error(errorMessage);
        setAiGenerateLoading(false);
        return;
      }

      const aiData = response?.data?.ai_generate_workflow?.data;

      if (aiData?.response && aiData.response.length > 0) {
        // Parse the workflow JSON from response
        const workflowJson = aiData.response[0];

        // Store in sessionStorage instead of URL (avoids URL length limits)
        sessionStorage.setItem('aiGeneratedWorkflow', workflowJson);

        // Store conversation context for iterative refinement
        if (aiData.conversation_id) {
          sessionStorage.setItem('aiConversationId', aiData.conversation_id);
        }
        if (aiData.session_id) {
          sessionStorage.setItem('aiSessionId', aiData.session_id);
        }
        // Store the initial query for chat context
        sessionStorage.setItem('aiInitialQuery', query);

        // Navigate with clean URL
        router.push(`/workflow/new?accountId=${accountId}&loadFromAI=true`);

        snackbar.success('Automation generated successfully!');
        setAiGenerateModalOpen(false);
      } else {
        snackbar.error('No automation data returned from AI');
      }
    } catch (error) {
      console.error('Error generating workflow:', error);
      snackbar.error('Failed to generate automation');
    } finally {
      setAiGenerateLoading(false);
    }
  };

  const handleGenerateWorkflowAsync = async (query: string): Promise<{ sessionId: string; conversationId: string } | null> => {
    if (!accountId || !query.trim()) {
      return null;
    }
    try {
      const response: any = await apiWorkflow.aiGenerateWorkflow(accountId, query, undefined, undefined, undefined, true);
      const errorMessage = parseHttpResponseBodyMessage(response);
      if (errorMessage) {
        return null;
      }
      const aiData = response?.data?.ai_generate_workflow?.data;
      if (aiData?.session_id) {
        sessionStorage.setItem('aiInitialQuery', query);
        return { sessionId: aiData.session_id, conversationId: aiData.conversation_id || '' };
      }
      return null;
    } catch (error) {
      console.error('Error starting async workflow generation:', error);
      return null;
    }
  };

  const parseConversationPollResult = (conversation: any) => {
    const status = conversation.status;
    const messages = conversation.llm_conversation_messages || [];
    const reversedMessages = [...messages].reverse();
    const lastGenMsg = reversedMessages.find((m: any) => m.message_type === 'generation');
    const lastFollowupMsg = reversedMessages.find((m: any) => m.message_type === 'followup');

    if (status === 'COMPLETED') {
      // After plan approval, the workflow JSON is stored on the followup message's response.
      // For direct generation (no plan approval), it's on the generation message.
      let workflowJson = '';
      const followupResponse = (lastFollowupMsg?.response || '').trimStart();
      if (followupResponse.startsWith('{')) {
        workflowJson = lastFollowupMsg?.response || '';
      }
      if (!workflowJson) {
        const genResponse = (lastGenMsg?.response || '').trimStart();
        if (genResponse.startsWith('{')) {
          workflowJson = lastGenMsg?.response || '';
        }
      }
      if (!workflowJson) {
        workflowJson = lastGenMsg?.response || '';
      }
      return { status: 'COMPLETED', workflowJson, conversationId: conversation.id };
    }

    if (status === 'FAILED') {
      return { status: 'FAILED', errorMessage: lastGenMsg?.response || 'Automation generation failed. Please try again.' };
    }

    if (status !== 'WAITING') {
      return null;
    }

    // WAITING: could be plan approval or config approval. The followup question
    // text lives in three places depending on the backend version:
    //   1) message_config.question — canonical location (always populated)
    //   2) followup message.message — also populated by the backend
    //   3) followup message.response — legacy; backend stopped writing this in
    //      llm-server #29309 to avoid duplicate rendering in ask-nudgebee chat.
    // Read in that order so we recover the plan text regardless of backend
    // version.
    const agents = lastGenMsg?.llm_conversation_agents || [];
    const lastAgent = agents[agents.length - 1];
    let planOptions: string[] | undefined;
    let followupType: string | undefined;
    let configQuestion = '';
    let agentId = lastAgent?.id;

    let followupData: any;

    const rawConfig = lastFollowupMsg?.message_config;
    if (rawConfig) {
      try {
        const config = typeof rawConfig === 'string' ? JSON.parse(rawConfig) : rawConfig;
        planOptions = config.followupOptions;
        followupType = config.followupType;
        followupData = config.followupData;
        configQuestion = typeof config.question === 'string' ? config.question : '';
        if (config.agentId) {
          agentId = config.agentId;
        }
      } catch {
        // ignore parse errors
      }
    }

    const followupResponse = lastFollowupMsg?.response || '';
    const followupMessage = lastFollowupMsg?.message || '';
    const genResponse = lastGenMsg?.response || '';
    let planText: string;
    if (configQuestion) {
      planText = configQuestion;
    } else if (followupMessage) {
      planText = followupMessage;
    } else if (followupResponse && !followupResponse.trimStart().startsWith('{')) {
      planText = followupResponse;
    } else {
      planText = genResponse;
    }
    planText = planText.replace(/^Here's my plan for building your workflow:\s*/i, '');
    planText = planText.replace(/\s*Would you like to approve this plan or request changes\?\s*$/i, '');

    return {
      status: 'WAITING',
      planText,
      planOptions,
      followupType,
      followupData,
      conversationId: conversation.id,
      messageId: lastFollowupMsg?.id,
      messageUpdatedAt: lastFollowupMsg?.updated_at,
      agentId,
    };
  };

  const handlePollWorkflowConversation = async (sessionId: string) => {
    if (!accountId) {
      return null;
    }
    try {
      const response: any = await apiAskNudgebee.getLlmConversation({ sessionId, accountId });
      const conversation = response?.data?.data?.llm_conversations?.[0];
      if (!conversation) {
        return null;
      }
      return parseConversationPollResult(conversation);
    } catch (error) {
      console.error('Error polling workflow conversation:', error);
      return null;
    }
  };

  const handleApproveOrRespondWorkflow = async (query: string, conversationId: string, sessionId: string, messageId?: string, agentId?: string) => {
    if (!accountId) {
      return;
    }
    await apiWorkflow.aiGenerateWorkflow(accountId, query, conversationId, sessionId, undefined, true, messageId, agentId);
  };

  const handleWorkflowCompleted = (workflowJson: string, _conversationId: string, sessionId: string) => {
    sessionStorage.setItem('aiGeneratedWorkflow', workflowJson);
    sessionStorage.setItem('aiSessionId', sessionId);
    router.push(`/workflow/new?accountId=${accountId}&loadFromAI=true`);
    snackbar.success('Automation generated successfully!');
    setAiGenerateModalOpen(false);
  };

  const handleCloseAiGenerateModal = () => {
    if (!aiGenerateLoading) {
      setAiGenerateModalOpen(false);
    }
  };

  const handleRefresh = () => {
    setCurrentPage(1);
    setPageOffsetTokens({ 1: '' });
    listWorkflows(1, rowsPerPage, '');
  };

  const listWorkflows = useCallback(
    (page: number, pageSize: number, offsetToken: string) => {
      if (!accountId) {
        return;
      }

      setLoading(true);
      const getStatusFilter = (status: string) => {
        if (!status || status === 'All') {
          return undefined;
        }
        if (status === 'Active') {
          return 'ACTIVE';
        }
        if (status === 'Inactive') {
          return 'INACTIVE';
        }
        if (status === 'Paused') {
          return 'PAUSED';
        }
        return status.toUpperCase();
      };

      const getLastExecutionStatusFilter = (status: string) => {
        if (!status || status === 'All') {
          return undefined;
        }
        // Map user-friendly names to backend values
        const statusMap: { [key: string]: string } = {
          Running: 'RUNNING',
          Completed: 'COMPLETED',
          Failed: 'FAILED',
          Canceled: 'CANCELED',
          Terminated: 'TERMINATED',
          'Timed Out': 'TIMED_OUT',
          'Continued As New': 'CONTINUED_AS_NEW',
          Unspecified: 'UNSPECIFIED',
        };
        return statusMap[status] || status.toUpperCase();
      };

      const getTriggerTypeFilter = (type: string) => {
        // `type` is a CSV of lowercase trigger types (e.g. "manual,schedule").
        // Empty string → no filter; backend OR's the values together.
        const trimmed = (type || '').trim();
        return trimmed === '' ? undefined : trimmed.toLowerCase();
      };

      const statusFilter = getStatusFilter(selectedStatus);
      const lastExecutionStatusFilter = getLastExecutionStatusFilter(selectedLastExecutionStatus);
      const triggerTypeFilter = getTriggerTypeFilter(selectedTriggerType);
      const createdByFilter = !selectedCreatedBy || selectedCreatedBy === 'All' ? undefined : selectedCreatedBy;

      apiWorkflow
        .listWorkflows(
          accountId,
          statusFilter,
          lastExecutionStatusFilter,
          triggerTypeFilter,
          pageSize,
          offsetToken,
          committedSearchName,
          committedSelectedTags,
          createdByFilter
        )
        .then((res: any) => {
          if (res?.data?.workflow_list) {
            const workflowList = res.data.workflow_list;
            const workflows = workflowList.workflows || [];

            const tableData = workflows.map((workflow: any) => [
              {
                component: (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
                    <Link
                      id={`workflow-name-link-${workflow.id}`}
                      href={`/workflow/${workflow.id}?accountId=${accountId}#executions`}
                      sx={{
                        textDecoration: 'none',
                        fontSize: '13px',
                        color: colors.text.primary,
                        '&:hover': {
                          textDecoration: 'underline',
                          cursor: 'pointer',
                        },
                      }}
                    >
                      {workflow.name || '-'}
                    </Link>
                    <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 0.5, fontSize: '12px', color: colors.text.secondary }}>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, whiteSpace: 'nowrap' }}>
                        <Text value='Created:' sx={{ fontSize: '11px', color: colors.text.secondaryDark }} />
                        <Datetime
                          baseDate={new Date()}
                          value={workflow.created_at}
                          sxSuffix={{ fontSize: '11px', color: colors.text.tertiary }}
                          sx={{ fontSize: '11px', color: colors.text.secondary }}
                        />
                        {workflow.created_by_user?.display_name && (
                          <Tooltip title={workflow.created_by_user.display_name} arrow placement='top'>
                            <span>
                              <Text
                                value={`· ${workflow.created_by_user.display_name.split(' ')[0]}`}
                                sx={{ fontSize: '11px', color: colors.text.tertiary, cursor: 'default' }}
                              />
                            </span>
                          </Tooltip>
                        )}
                      </Box>
                      <Text value='|' secondaryText sx={{ fontSize: '10px', fontWeight: 500 }} />
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, whiteSpace: 'nowrap' }}>
                        <Text value='Updated:' sx={{ fontSize: '11px', color: colors.text.secondaryDark }} />
                        <Datetime
                          baseDate={new Date()}
                          value={workflow.updated_at}
                          sxSuffix={{ fontSize: '11px', color: colors.text.tertiary }}
                          sx={{ fontSize: '11px', color: colors.text.secondary }}
                        />
                        {workflow.updated_by_user?.display_name && (
                          <Tooltip title={workflow.updated_by_user.display_name} arrow placement='top'>
                            <span>
                              <Text
                                value={`· ${workflow.updated_by_user.display_name.split(' ')[0]}`}
                                sx={{ fontSize: '11px', color: colors.text.tertiary, cursor: 'default' }}
                              />
                            </span>
                          </Tooltip>
                        )}
                      </Box>
                    </Box>
                  </Box>
                ),
              },

              {
                component: <LastExecutionCell workflow={workflow} />,
              },
              {
                component:
                  workflow.definition?.triggers && workflow.definition.triggers.length > 0 ? (
                    <Box sx={{ display: 'flex', gap: 0.5 }}>
                      {workflow.definition.triggers.map((trigger: any, index: number) => (
                        <Box key={index} sx={{ display: 'flex', gap: 0.5 }}>
                          <SafeIcon src={getTriggerIcon(trigger.type)} alt={trigger.type} style={{ height: '18px', width: '18px' }} />
                          <Text
                            value={trigger.type.charAt(0).toUpperCase() + trigger.type.slice(1).toLowerCase()}
                            sx={{ fontSize: '12px', color: colors.text.secondary, fontWeight: 400, marginRight: '8px' }}
                          />
                        </Box>
                      ))}
                    </Box>
                  ) : (
                    <Text value='-' />
                  ),
              },
              {
                component: workflow?.tags ? (
                  <TagsDisplay tags={workflow?.tags} maxVisible={2} />
                ) : (
                  <Text value='Unlabeled' sx={{ fontSize: '12px', color: colors.text.tertiarymedium, fontStyle: 'italic' }} />
                ),
              },
              {
                component: (
                  <Box sx={{ display: 'flex', justifyContent: 'center' }}>
                    <CustomLabels text={workflow.status?.toLowerCase() || 'Unknown'} textTransform='capitalize' />
                  </Box>
                ),
              },
              {
                component: (
                  <WorkflowActionsCell
                    workflow={workflow}
                    accountId={accountId}
                    onStop={handleOpenStopExecutionModal}
                    onEdit={handleEditWorkflow}
                    getMenuItems={getMenuItems}
                    onMenuClick={onMenuClick}
                  />
                ),
              },
            ]);
            setData(tableData);

            // Set pagination data
            setTotalRows(workflowList.total_count || 0);

            // Store the offset token for the next page (if it exists)
            const nextPageOffsetToken = workflowList.next_page_token;
            if (nextPageOffsetToken) {
              setPageOffsetTokens((prev) => ({
                ...prev,
                [page + 1]: nextPageOffsetToken,
              }));
            }
          } else {
            setData([]);
            setTotalRows(0);
          }

          setLoading(false);
        })
        .catch((error) => {
          console.error('Error fetching workflows:', error);
          setData([]);
          setTotalRows(0);
          setLoading(false);
        });
      // eslint-disable-next-line react-hooks/exhaustive-deps
    },
    [accountId, selectedStatus, selectedLastExecutionStatus, selectedTriggerType, committedSearchName, committedSelectedTags, selectedCreatedBy]
  );

  // Sync state from router query params (e.g. direct URL navigation or bookmark).
  // Committed values are updated here so navigating to a URL with ?name=foo
  // immediately triggers the search without requiring an Enter press.
  useEffect(() => {
    const { status, last_execution_status, type, name, tags, created_by } = router.query;

    setSelectedStatus((status as string) || 'All');
    setSelectedLastExecutionStatus((last_execution_status as string) || 'All');
    setSelectedTriggerType((type as string) || '');
    setSearchName((name as string) || '');
    setCommittedSearchName((name as string) || '');
    setSelectedTags((tags as string) || '');
    setCommittedSelectedTags((tags as string) || '');
    setSelectedCreatedBy((created_by as string) || 'All');
  }, [router.query]);

  // Fetch active users for the "Created By" filter
  useEffect(() => {
    const fetchActiveUsers = async () => {
      try {
        const response = await apiUser.listUsers({ status: 'active' });
        const users = response?.data || [];
        const userNames = users
          .map((user: any) => user.display_name)
          .filter(Boolean)
          .sort();
        setCreatedByOptions(['All', ...userNames]);
      } catch (error) {
        console.error('Error fetching active users:', error);
      }
    };
    fetchActiveUsers();
  }, []);

  // Trigger search when filters or debounced search values change
  useEffect(() => {
    // Clear all tokens and reset to page 1
    setCurrentPage(1);
    setPageOffsetTokens({ 1: '' });
    listWorkflows(1, rowsPerPage, '');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, selectedStatus, selectedLastExecutionStatus, selectedTriggerType, committedSearchName, committedSelectedTags, selectedCreatedBy]);

  // Check AI workflow feature flag
  useEffect(() => {
    const checkAiFeatureAccess = async () => {
      try {
        const hasAccess = await hasFeatureAccess('WORKFLOWS');
        setAiFeatureEnabled(hasAccess);
      } catch (error) {
        console.error('Error checking AI workflow feature access:', error);
        setAiFeatureEnabled(false);
      }
    };
    checkAiFeatureAccess();
  }, []);

  // Check workflow templates feature flag
  useEffect(() => {
    const checkTemplateFeatureAccess = async () => {
      try {
        const hasAccess = await hasFeatureAccess('WORKFLOW_TEMPLATES');
        setTemplateFeatureEnabled(hasAccess);
      } catch (error) {
        console.error('Error checking workflow templates feature access:', error);
        setTemplateFeatureEnabled(false);
      }
    };
    checkTemplateFeatureAccess();
  }, []);

  const tableHeaders = [
    { name: 'Name', width: '30%' },
    { name: 'Last Execution', width: '15%' },
    { name: 'Trigger Type', width: '10%' },
    { name: 'Tags', width: '18%' },
    { name: 'Status', width: '10%' },
    { name: '', width: '5%' },
  ];

  const handleCreateWorkflow = () => {
    setCreateWorkflowOptionsOpen(true);
  };

  const handleCreateFromScratch = () => {
    setCreateWorkflowOptionsOpen(false);
    let path = '/workflow/new';
    if (accountId) {
      path = path + '?accountId=' + accountId;
    }
    router.push(path);
  };

  const handleUseTemplate = () => {
    setCreateWorkflowOptionsOpen(false);
    setTemplateModalOpen(true);
  };

  const handleCloseTemplateModal = () => {
    setTemplateModalOpen(false);
  };

  const handleAskAIFromOptions = () => {
    setCreateWorkflowOptionsOpen(false);
    setAiGenerateModalOpen(true);
  };

  const handleCloseCreateWorkflowOptions = () => {
    setCreateWorkflowOptionsOpen(false);
  };

  const handleConfigModalClose = () => {
    setConfigModalOpen(false);
  };

  const onNameFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (committedSearchName !== '' && e?.target?.value === '') {
      setCommittedSearchName('');
      applyFiltersOnRouter(router, { name: '' });
    }
    setSearchName(e?.target?.value);
  };

  const onNameSearchEnter = () => {
    setCommittedSearchName(searchName);
    applyFiltersOnRouter(router, { name: searchName });
  };

  const onNameSearchClear = () => {
    setSearchName('');
    setCommittedSearchName('');
    applyFiltersOnRouter(router, { name: '' });
  };

  const onTagsFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (committedSelectedTags !== '' && e?.target?.value === '') {
      setCommittedSelectedTags('');
      applyFiltersOnRouter(router, { tags: '' });
    }
    setSelectedTags(e?.target?.value);
  };

  const onTagsSearchEnter = () => {
    setCommittedSelectedTags(selectedTags);
    applyFiltersOnRouter(router, { tags: selectedTags });
  };

  const onTagsSearchClear = () => {
    setSelectedTags('');
    setCommittedSelectedTags('');
    applyFiltersOnRouter(router, { tags: '' });
  };

  const handlePageChange = (page: number, limit: number) => {
    const prevLimit = rowsPerPage;
    setRowsPerPage(limit);

    // If page size changed, clear all tokens and reset to page 1
    if (limit !== prevLimit) {
      setCurrentPage(1);
      setPageOffsetTokens({ 1: '' });
      listWorkflows(1, limit, '');
      return;
    }

    // Update current page
    setCurrentPage(page);

    // Hybrid approach: Use stored token if available, calculate offset if not
    const offsetToken = pageOffsetTokens[page] ?? ((page - 1) * limit).toString();

    // Call API with the offset token
    listWorkflows(page, limit, offsetToken);
  };

  return (
    <LiveExecutionStatusContext.Provider value={liveExecutionStatuses}>
      <Modal
        open={deleteModalOpen}
        handleClose={handleCloseDeleteModal}
        width='md'
        title={`Delete Automation "${selectedWorkflow.name}"`}
        loader={loading}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 2, px: 2, py: 2 }}>
            <CustomButton id='workflow-delete-cancel-btn' variant='secondary' text='Cancel' onClick={handleCloseDeleteModal} size='Medium' />
            <CustomButton id='workflow-delete-confirm-btn' text='Delete' onClick={handleDeleteWorkflow} disabled={loading} size='Medium' />
          </Box>
        }
      >
        <DialogContent sx={{ padding: '24px' }}>
          <DialogContentText>Are you sure you want to delete this automation? This action cannot be undone.</DialogContentText>
        </DialogContent>
      </Modal>

      <Modal
        open={pauseModalOpen}
        handleClose={handleClosePauseModal}
        width='md'
        title={`Pause Automation "${selectedWorkflow.name}"`}
        loader={loading}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 2, px: 2, py: 2 }}>
            <CustomButton id='workflow-pause-cancel-btn' variant='secondary' text='Cancel' onClick={handleClosePauseModal} size='Medium' />
            <CustomButton id='workflow-pause-confirm-btn' text='Pause' onClick={handlePauseWorkflow} disabled={loading} size='Medium' />
          </Box>
        }
      >
        <DialogContent sx={{ padding: '24px' }}>
          <DialogContentText>Are you sure you want to pause this scheduled automation? It will stop executing until resumed.</DialogContentText>
        </DialogContent>
      </Modal>

      <Modal
        open={resumeModalOpen}
        handleClose={handleCloseResumeModal}
        width='md'
        title={`Resume Automation "${selectedWorkflow.name}"`}
        loader={loading}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 2, px: 2, py: 2 }}>
            <CustomButton id='workflow-resume-cancel-btn' variant='secondary' text='Cancel' onClick={handleCloseResumeModal} size='Medium' />
            <CustomButton id='workflow-resume-confirm-btn' text='Resume' onClick={handleResumeWorkflow} disabled={loading} size='Medium' />
          </Box>
        }
      >
        <DialogContent sx={{ padding: '24px' }}>
          <DialogContentText>
            Are you sure you want to resume this scheduled automation? It will start executing according to its schedule.
          </DialogContentText>
        </DialogContent>
      </Modal>

      <Modal
        open={stopExecutionModalOpen}
        handleClose={handleCloseStopExecutionModal}
        width='md'
        title={`Cancel Running Execution — "${selectedWorkflow.name}"`}
        loader={stopExecutionLoading}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 2, px: 2, py: 2 }}>
            <CustomButton
              id='workflow-stop-execution-cancel-btn'
              variant='secondary'
              text='Keep Running'
              onClick={handleCloseStopExecutionModal}
              size='Medium'
            />
            <CustomButton
              id='workflow-stop-execution-confirm-btn'
              text='Cancel Execution'
              onClick={handleStopExecution}
              disabled={stopExecutionLoading}
              size='Medium'
              sx={{ backgroundColor: colors.error, '&:hover': { backgroundColor: colors.errorHover } }}
            />
          </Box>
        }
      >
        <DialogContent sx={{ padding: '24px' }}>
          <DialogContentText>Are you sure you want to cancel the currently running execution? This action cannot be undone.</DialogContentText>
        </DialogContent>
      </Modal>

      <TriggerWorkflowModal
        open={triggerModalOpen}
        onClose={handleCloseTriggerModal}
        workflowName={selectedWorkflow.name}
        triggerType={getPrimaryTriggerType(selectedWorkflow)}
        defaultInputs={getDefaultTriggerInputs(selectedWorkflow)}
        inputSchema={getWorkflowInputSchema(selectedWorkflow)}
        onTrigger={handleTriggerWorkflow}
        loading={triggerLoading}
      />

      <AiGenerateWorkflowModal
        open={aiGenerateModalOpen}
        onClose={handleCloseAiGenerateModal}
        onGenerate={handleAiGenerateWorkflow}
        onGenerateAsync={handleGenerateWorkflowAsync}
        onPollConversation={handlePollWorkflowConversation}
        onApproveOrRespond={handleApproveOrRespondWorkflow}
        onWorkflowCompleted={handleWorkflowCompleted}
        loading={aiGenerateLoading}
      />

      <CreateWorkflowOptionsModal
        open={createWorkflowOptionsOpen}
        onClose={handleCloseCreateWorkflowOptions}
        onCreateFromScratch={handleCreateFromScratch}
        onUseTemplate={handleUseTemplate}
        onAskAI={handleAskAIFromOptions}
        aiFeatureEnabled={aiFeatureEnabled}
        templateFeatureEnabled={templateFeatureEnabled}
      />

      <WorkflowTemplatesModal open={templateModalOpen} onClose={handleCloseTemplateModal} accountId={accountId!} />

      <ConfigurationManager accountId={accountId!} open={configModalOpen} onClose={handleConfigModalClose} />

      <BoxLayout2
        leftExtraOptions={[]}
        extraOptions={[
          <CustomIconButton
            key='refresh'
            id='workflow-listing-refresh-btn'
            variant='iconButton'
            onClick={handleRefresh}
            isDisabled={loading}
            tooltip={'Refresh'}
            sx={{
              width: '32px',
              height: '32px',
              padding: '0px',
            }}
          >
            <Refresh sx={{ fontSize: '20px' }} />
          </CustomIconButton>,
          <CustomButton
            key='manage-configs'
            id='workflow-listing-configs-btn'
            text='Configs'
            onClick={() => setConfigModalOpen(true)}
            variant='secondary'
            size='Small'
            startIcon={<SafeIcon style={{ height: '14px', width: '14px' }} src={SettingsIcon} alt={'manage configs'} />}
          />,
          ...(accountId && hasWriteAccess(accountId)
            ? [
                <CustomButton
                  key='create-workflow'
                  id='workflow-listing-create-btn'
                  text='Create Automation'
                  onClick={handleCreateWorkflow}
                  variant='primary'
                  size='Small'
                  sx={{ width: '160px' }}
                  startIcon={<SafeIcon style={{ height: '16px', width: '16px' }} src={addIconWhite} alt={'create automation'} />}
                />,
              ]
            : []),
        ]}
        filterOptions={[
          {
            type: 'search',
            enabled: true,
            onSelect: onNameFilterChange,
            onEnter: onNameSearchEnter,
            onClear: onNameSearchClear,
            value: searchName,
            minWidth: '100px',
            label: 'Search by Automation Name',
          },
          {
            type: 'search',
            enabled: true,
            onSelect: onTagsFilterChange,
            onEnter: onTagsSearchEnter,
            onClear: onTagsSearchClear,
            value: selectedTags,
            minWidth: '100px',
            width: '100px',
            label: 'Search by Tags',
          },
          {
            type: 'dropdown',
            enabled: true,
            options: createdByOptions,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedCreatedBy(e?.target?.value);
              applyFiltersOnRouter(router, { created_by: e?.target?.value });
            },
            width: '160px',
            label: 'Created By',
            value: selectedCreatedBy,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: ['All', 'Active', 'Inactive', 'Paused'],
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedStatus(e?.target?.value);
              applyFiltersOnRouter(router, { status: e?.target?.value });
            },
            width: '140px',
            label: 'Status',
            value: selectedStatus,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: ['All', 'Running', 'Completed', 'Failed', 'Canceled', 'Terminated', 'Timed Out', 'Continued As New', 'Unspecified'],
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedLastExecutionStatus(e?.target?.value);
              applyFiltersOnRouter(router, { last_execution_status: e?.target?.value });
            },
            width: '140px',
            label: 'Last Exec. Status',
            value: selectedLastExecutionStatus,
          },
          {
            type: 'multi-dropdown',
            enabled: true,
            options: triggerTypeOptions,
            onSelect: (e: { target: { value: { label: string; value: string }[] | null } }) => {
              const next = Array.isArray(e?.target?.value) ? e.target.value : [];
              const csv = next.map((option: { value: string }) => option.value).join(',');
              setSelectedTriggerType(csv);
              applyFiltersOnRouter(router, { type: csv });
            },
            width: '160px',
            label: 'Trigger Type',
            value: selectedTriggerType
              ? selectedTriggerType
                  .split(',')
                  .map((v) => v.trim().toLowerCase())
                  .filter(Boolean)
                  .map((v) => triggerTypeOptions.find((opt) => opt.value === v) ?? { label: v, value: v })
              : [],
          },
        ]}
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: false,
            onClick: () => ({ tableId: 'workflows-table' }),
          },
        }}
      >
        <CustomTable2
          id='workflows-table'
          tableData={data}
          headers={tableHeaders}
          loading={loading}
          rowsPerPage={rowsPerPage}
          totalRows={totalRows}
          pageNumber={currentPage}
          onPageChange={handlePageChange}
          tableHeadingCenter={['Status']}
        />
      </BoxLayout2>
    </LiveExecutionStatusContext.Provider>
  );
};

export default WorkflowListing;

interface WorkflowListingProps {
  accountId?: string;
}

interface TagsDisplayProps {
  tags: string[] | Record<string, any> | string;
  maxVisible?: number;
}

const TagsDisplay: React.FC<TagsDisplayProps> = ({ tags, maxVisible = 3 }) => {
  const [showMore, setShowMore] = useState(false);
  const renderTags = () => {
    if (!tags) {
      return null;
    }

    let tagsArray: string[] = [];

    if (Array.isArray(tags)) {
      tagsArray = tags;
    } else if (typeof tags === 'object') {
      tagsArray = Object.entries(tags).map(([key, value]) => `${key}: ${value}`);
    } else {
      tagsArray = [String(tags)];
    }

    const visibleTags = showMore ? tagsArray : tagsArray.slice(0, maxVisible);
    const hasMoreTags = tagsArray.length > maxVisible;
    return (
      <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap', alignItems: 'center' }}>
        {visibleTags.map((tag: string, index: number) => (
          <CustomLabels text={tag} key={index} textTransform='none' />
        ))}
        {hasMoreTags && (
          <CustomButton
            id='workflow-tags-toggle-btn'
            sx={{
              fontWeight: '400 !important',
              fontSize: '11px !important',
              background: 'transparent !important',
              minWidth: '50px !important',
              height: '24px !important',
              p: '0 6px !important',
            }}
            text={showMore ? 'Show Less' : `+${tagsArray.length - maxVisible} more`}
            variant={'secondary'}
            size={'xSmall'}
            onClick={() => setShowMore(!showMore)}
          />
        )}
      </Box>
    );
  };

  return tags ? renderTags() : <Text value='-' />;
};
