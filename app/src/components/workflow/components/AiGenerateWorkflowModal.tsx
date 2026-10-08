import { useState, useEffect, useRef, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { ProgressLinear } from '@ui/ProgressLinear';
import { Button } from '@ui/Button';
import { FormField } from '@shared/forms/FormComponents';
import MarkDowns from '@shared/viewers/MarkDowns';
import { ds } from '@utils/colors';
import ClarificationQuestion from './ClarificationQuestion';
import { getAiBuildActions, SavedAutomationSummary } from '@components/workflow/automationMenu';

type ModalStage = 'input' | 'generating' | 'plan_review' | 'text_followup' | 'success' | 'error';

/**
 * Identity of the question a WAITING poll result is showing, used to tell a genuinely new
 * question from the one already on screen.
 *
 * Keyed on CONTENT rather than the followup row's `updated_at`. The backend records the user's
 * answer onto the same followup row, bumping `updated_at` without changing the question — so an
 * updated_at-keyed identity reads "answer recorded" as "new question", re-renders the question
 * already displayed and stops polling, leaving the user staring at their unanswered click
 * (#35393). Content keying still distinguishes config approval, which reuses one followup row
 * with different content.
 */
export function followupIdentity(result: { messageId?: string; planText?: string; followupData?: unknown }): string {
  return (result.messageId || '') + ':' + (result.planText || '') + ':' + JSON.stringify(result.followupData ?? null);
}

interface PlanData {
  planText: string;
  options: string[];
  followupType: string;
  followupData?: any;
  conversationId: string;
  sessionId: string;
  messageId: string;
  agentId: string;
}

interface GenerationProgress {
  conversationId: string;
  sessionId: string;
  startedAt: number;
  elapsedSeconds: number;
}

interface AiGenerateWorkflowModalProps {
  open: boolean;
  onClose: () => void;
  onGenerate: (query: string) => Promise<void>;
  onGenerateAsync?: (query: string) => Promise<{ sessionId: string; conversationId: string } | null>;
  onPollConversation?: (sessionId: string) => Promise<{
    status: string;
    // Whether this session saved an automation; unset when the caller could not check.
    saved?: boolean;
    // The automation this session saved, as the list returns it; unset when it is not known.
    savedWorkflow?: SavedAutomationSummary;
    // What the builder has done so far in this conversation: tool calls made, and tasks added.
    progress?: { steps: number; tasksAdded: number };
    workflowJson?: string;
    summaryText?: string;
    planText?: string;
    planOptions?: string[];
    followupType?: string;
    followupData?: any;
    conversationId?: string;
    messageId?: string;
    agentId?: string;
    errorMessage?: string;
  } | null>;
  onApproveOrRespond?: (query: string, conversationId: string, sessionId: string, messageId?: string, agentId?: string) => Promise<void>;
  onCancel?: (conversationId: string) => Promise<void>;
  onWorkflowCompleted?: (workflowJson: string, conversationId: string, sessionId: string) => void;
  // Offered on the success screen for the automation the build saved. The caller owns what each
  // does; leaving one out hides its button.
  onRunNow?: (savedWorkflow: SavedAutomationSummary) => void;
  onActivate?: (savedWorkflow: SavedAutomationSummary) => void;
  loading: boolean;
}

const POLL_INTERVAL_MS = 4000;

// How long a run may show no observable change before the modal says so. The longest quiet
// stretch of a healthy run is the model writing out its final answer, which has taken about two
// minutes on large automations.
const STALL_AFTER_MS = 3 * 60 * 1000;

const STAGE_MESSAGES = [
  'Understanding your request...',
  'Planning automation structure...',
  'Building automation definition...',
  'Validating and refining...',
];

const AiGenerateWorkflowModal: React.FC<AiGenerateWorkflowModalProps> = ({
  open,
  onClose,
  onGenerate,
  onGenerateAsync,
  onPollConversation,
  onApproveOrRespond,
  onCancel,
  onWorkflowCompleted,
  onRunNow,
  onActivate,
  loading,
}) => {
  const [query, setQuery] = useState('');
  const [stage, setStage] = useState<ModalStage>('input');
  const [progress, setProgress] = useState<GenerationProgress | null>(null);
  const [planData, setPlanData] = useState<PlanData | null>(null);
  const [feedbackText, setFeedbackText] = useState('');
  const [showFeedbackInput, setShowFeedbackInput] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');
  // The AI's own reply (markdown), shown under the error message when a run ends without an automation.
  const [errorDetail, setErrorDetail] = useState('');
  const [stageMessageIndex, setStageMessageIndex] = useState(0);
  const [summaryText, setSummaryText] = useState('');
  // Tasks the builder has added so far, read from the conversation on every poll.
  const [tasksAdded, setTasksAdded] = useState(0);
  // True once the conversation has shown no change for STALL_AFTER_MS.
  const [stalled, setStalled] = useState(false);
  const [completedData, setCompletedData] = useState<{
    workflowJson: string;
    conversationId: string;
    sessionId: string;
    savedWorkflow?: SavedAutomationSummary;
  } | null>(null);

  const pollIntervalRef = useRef<NodeJS.Timeout | null>(null);
  const elapsedIntervalRef = useRef<NodeJS.Timeout | null>(null);
  const lastPlanMessageIdRef = useRef<string>('');
  const processingWaitingRef = useRef(false);
  // The last distinct state the poll returned and when it was first seen, for the stall check.
  const lastActivityRef = useRef({ key: '', at: 0 });

  const clearPolling = useCallback(() => {
    if (pollIntervalRef.current) {
      clearInterval(pollIntervalRef.current);
      pollIntervalRef.current = null;
    }
  }, []);

  const clearElapsed = useCallback(() => {
    if (elapsedIntervalRef.current) {
      clearInterval(elapsedIntervalRef.current);
      elapsedIntervalRef.current = null;
    }
  }, []);

  const clearIntervals = useCallback(() => {
    clearPolling();
    clearElapsed();
  }, [clearPolling, clearElapsed]);

  useEffect(() => {
    return () => clearIntervals();
  }, [clearIntervals]);

  // The modal stays mounted (visibility is driven by `open`), so reset transient state whenever it
  // closes — otherwise a parent-initiated close (e.g. after "Open in Editor") would re-open on the
  // stale success/error screen. The `generating` stage keeps `open` true, so this never interrupts it.
  useEffect(() => {
    if (open) {
      return;
    }
    clearIntervals();
    setStage('input');
    setQuery('');
    setProgress(null);
    setPlanData(null);
    setFeedbackText('');
    setShowFeedbackInput(false);
    setErrorMessage('');
    setErrorDetail('');
    setStageMessageIndex(0);
    setSummaryText('');
    setCompletedData(null);
    lastPlanMessageIdRef.current = '';
    processingWaitingRef.current = false;
  }, [open, clearIntervals]);

  // Rotate stage messages every 15 seconds
  useEffect(() => {
    if (stage !== 'generating') {
      return;
    }
    const interval = setInterval(() => {
      setStageMessageIndex((prev) => Math.min(prev + 1, STAGE_MESSAGES.length - 1));
    }, 15000);
    return () => clearInterval(interval);
  }, [stage]);

  const startPolling = useCallback(
    (sessionId: string, conversationId: string) => {
      // Answering a follow-up calls this again while the previous elapsed interval is still
      // running: displaying a question clears the POLL interval only, never the elapsed one. The
      // assignment below would then overwrite the ref and orphan that interval, which keeps
      // ticking with its own startedAt captured in its closure. Two intervals writing
      // elapsedSeconds from different baselines makes the counter flip between values every
      // second, and one more is orphaned per follow-up answered.
      clearIntervals();

      const startedAt = Date.now();
      setProgress({ sessionId, conversationId, startedAt, elapsedSeconds: 0 });
      lastActivityRef.current = { key: '', at: startedAt };
      setStalled(false);

      // Update elapsed timer every second
      elapsedIntervalRef.current = setInterval(() => {
        setProgress((prev) => (prev ? { ...prev, elapsedSeconds: Math.floor((Date.now() - startedAt) / 1000) } : prev));
      }, 1000);

      // Poll for status
      const pollInterval = setInterval(async () => {
        try {
          const result = await onPollConversation?.(sessionId);
          // Cancelled, closed or restarted while the request was in flight: this answer belongs
          // to a run the modal has already left.
          if (pollIntervalRef.current !== pollInterval) {
            return;
          }

          // A run is moving while its status, its step count or the question it is asking keeps
          // changing. When none of them has changed for STALL_AFTER_MS — or the conversation could
          // not be read at all for that long — say so instead of showing a progress bar over a run
          // that may be dead; polling goes on, and the notice clears if the run moves again.
          const activityKey = result ? `${result.status}:${result.progress?.steps ?? 0}:${followupIdentity(result)}` : lastActivityRef.current.key;
          if (activityKey !== lastActivityRef.current.key) {
            lastActivityRef.current = { key: activityKey, at: Date.now() };
            setStalled(false);
          } else if (Date.now() - lastActivityRef.current.at >= STALL_AFTER_MS) {
            setStalled(true);
          }
          if (!result) {
            return;
          }
          setTasksAdded(result.progress?.tasksAdded ?? 0);

          if (result.status === 'COMPLETED') {
            clearIntervals();
            if (result.saved ?? Boolean(result.workflowJson)) {
              // Automation built. Show the build summary + an Open-in-Editor button rather than
              // navigating away immediately, so the user can see what was created.
              setSummaryText(result.summaryText || '');
              setCompletedData({
                workflowJson: result.workflowJson || '',
                conversationId: result.conversationId || conversationId,
                sessionId,
                savedWorkflow: result.savedWorkflow,
              });
              setStage('success');
            } else if (result.summaryText) {
              // Nothing was saved, but the AI did reply — a failed save with its reason, or a
              // plain answer to a prompt that was not a build request. That reply is the whole
              // message: the title says no automation was saved, and nothing here guesses why.
              setErrorMessage('');
              setErrorDetail(result.summaryText);
              setStage('error');
            } else {
              // Non-workflow completion with no reply at all — show helpful message
              setErrorMessage(
                "This prompt didn't result in an automation. Try describing a specific task, " + 'e.g., "Send a Slack alert when CPU exceeds 80%".'
              );
              setStage('error');
            }
          } else if (result.status === 'WAITING' && result.planText) {
            // Guard: prevent concurrent poll callbacks from both processing the same result
            if (processingWaitingRef.current) {
              return;
            }
            // Skip a WAITING result that is still the question already on screen — the backend has
            // not produced the next one yet. Displaying a question calls clearPolling(), so
            // mistaking "answer recorded" for "new question" here strands the modal permanently.
            // See followupIdentity above for why this is content-keyed.
            const staleKey = followupIdentity(result);
            if (lastPlanMessageIdRef.current && lastPlanMessageIdRef.current === staleKey) {
              return;
            }
            processingWaitingRef.current = true;
            clearPolling();
            lastPlanMessageIdRef.current = staleKey;
            const followupType = result.followupType || 'single_select';
            setPlanData({
              planText: result.planText,
              options: result.planOptions ?? ['Approve and Build', 'Request Changes'],
              followupType,
              followupData: result.followupData,
              conversationId: result.conversationId ?? conversationId,
              sessionId,
              messageId: result.messageId ?? '',
              agentId: result.agentId ?? '',
            });
            if (followupType === 'text') {
              setStage('text_followup');
            } else {
              setStage('plan_review');
            }
            processingWaitingRef.current = false;
          } else if (result.status === 'FAILED') {
            clearIntervals();
            setErrorMessage(result.errorMessage || 'Automation generation failed. Please try again.');
            setStage('error');
          }
          // else status is IN_PROGRESS, continue polling
        } catch (err) {
          console.error('Error polling conversation:', err);
        }
      }, POLL_INTERVAL_MS);
      pollIntervalRef.current = pollInterval;
    },
    [onPollConversation, clearIntervals, clearPolling]
  );

  const handleSubmit = async () => {
    if (!query.trim()) {
      return;
    }

    setStage('generating');
    setStageMessageIndex(0);
    setErrorMessage('');
    setTasksAdded(0);
    setStalled(false);

    try {
      const result = await onGenerateAsync?.(query);
      if (result) {
        startPolling(result.sessionId, result.conversationId);
      } else {
        // Fallback: async submission failed, try sync
        await onGenerate(query);
      }
    } catch (err) {
      console.error('Error starting automation generation:', err);
      setErrorMessage('Failed to start automation generation. Please try again.');
      setStage('error');
    }
  };

  const handleApprove = async () => {
    if (!planData) {
      return;
    }

    setStage('generating');
    setStageMessageIndex(2); // Skip to "Building" stage since plan is approved

    try {
      await onApproveOrRespond?.('Approve and Build', planData.conversationId, planData.sessionId, planData.messageId, planData.agentId);
      startPolling(planData.sessionId, planData.conversationId);
    } catch (err) {
      console.error('Error approving plan:', err);
      setErrorMessage('Failed to approve plan. Please try again.');
      setStage('error');
    }
  };

  const handleRequestChanges = () => {
    setShowFeedbackInput(true);
  };

  const handleSubmitFeedback = async () => {
    if (!planData || !feedbackText.trim()) {
      return;
    }

    setStage('generating');
    setStageMessageIndex(1); // Back to "Planning" since we're re-planning

    try {
      await onApproveOrRespond?.(feedbackText, planData.conversationId, planData.sessionId, planData.messageId, planData.agentId);
      startPolling(planData.sessionId, planData.conversationId);
    } catch (err) {
      console.error('Error submitting feedback:', err);
      setErrorMessage('Failed to submit feedback. Please try again.');
      setStage('error');
    }
    setFeedbackText('');
    setShowFeedbackInput(false);
  };

  const handleCancelGeneration = async () => {
    clearIntervals();
    processingWaitingRef.current = false;
    if (progress?.conversationId) {
      try {
        await onCancel?.(progress.conversationId);
      } catch (err) {
        console.error('Error canceling generation:', err);
      }
    }
    setStage('input');
    setProgress(null);
    setPlanData(null);
  };

  const handleClose = () => {
    if (stage === 'generating') {
      return; // Don't close while generating
    }
    clearIntervals();
    setQuery('');
    setStage('input');
    setProgress(null);
    setPlanData(null);
    setFeedbackText('');
    setShowFeedbackInput(false);
    setErrorMessage('');
    setErrorDetail('');
    setStageMessageIndex(0);
    setSummaryText('');
    setCompletedData(null);
    lastPlanMessageIdRef.current = '';
    processingWaitingRef.current = false;
    onClose();
  };

  const handleRetry = () => {
    setStage('input');
    setErrorMessage('');
    setErrorDetail('');
    setProgress(null);
  };

  const formatElapsed = (seconds: number) => {
    const mins = Math.floor(seconds / 60);
    const secs = seconds % 60;
    return mins > 0 ? `${mins}m ${secs}s` : `${secs}s`;
  };

  const renderContent = () => {
    switch (stage) {
      case 'input':
        return (
          <Box id='wf-ai-prompt' sx={{ mt: 2, mb: 2 }}>
            <FormField
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              label='Prompt'
              fieldType='textarea'
              placeholder='e.g., Create an automation that sends a notification when CPU usage exceeds 80%'
              multiline
              minRows={25}
              maxRows={40}
              disabled={loading}
            />
          </Box>
        );

      case 'generating':
        return (
          <Box
            sx={{
              mt: 4,
              mb: 4,
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: 3,
              minHeight: '200px',
              justifyContent: 'center',
            }}
          >
            <Typography variant='body1' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: 'text.primary' }}>
              {tasksAdded > 0 ? `Building automation: ${tasksAdded} ${tasksAdded === 1 ? 'task' : 'tasks'} added` : STAGE_MESSAGES[stageMessageIndex]}
            </Typography>
            <Box sx={{ width: '60%' }}>
              <ProgressLinear mode='indeterminate' tone='info' surface='section' aria-label='Generating automation' />
            </Box>
            {progress && (
              <Typography variant='body2' sx={{ color: 'text.secondary' }}>
                Elapsed: {formatElapsed(progress.elapsedSeconds)}
              </Typography>
            )}
            {stalled && (
              <Typography id='wf-ai-stalled-notice' variant='body2' sx={{ color: 'warning.main', textAlign: 'center' }}>
                No progress for {STALL_AFTER_MS / 60000} minutes. This run may be stuck: keep waiting, or cancel and try again.
              </Typography>
            )}
          </Box>
        );

      case 'plan_review':
        // Clarification question — render interactive option card
        if (planData?.followupData?.type === 'clarification') {
          const clarifyOptions = (planData.followupData.options || []).map((opt: any) => ({
            label: typeof opt === 'string' ? opt : opt.label || '',
            description: typeof opt === 'string' ? undefined : opt.description,
          }));
          return (
            <ClarificationQuestion
              question={planData?.planText || ''}
              options={clarifyOptions}
              allowCustom={planData.followupData.allow_custom !== false}
              allowSkip={planData.followupData.allow_skip !== false}
              onSelect={async (answer) => {
                if (!planData) {
                  return;
                }
                setStage('generating');
                setStageMessageIndex(1);
                try {
                  await onApproveOrRespond?.(answer, planData.conversationId, planData.sessionId, planData.messageId, planData.agentId);
                  startPolling(planData.sessionId, planData.conversationId);
                } catch (err) {
                  console.error('Error responding to clarification:', err);
                  setErrorMessage('Failed to submit response. Please try again.');
                  setStage('error');
                }
              }}
              disabled={stage !== 'plan_review'}
            />
          );
        }

        // Standard plan review
        return (
          <Box sx={{ mt: 2, mb: 2 }}>
            <Typography variant='subtitle2' sx={{ mb: 1, fontWeight: 'var(--ds-font-weight-semibold)' }}>
              AI has created a plan for your automation:
            </Typography>
            <Box
              sx={{
                p: 2,
                borderRadius: 'var(--ds-radius-sm)',
                border: '1px solid',
                borderColor: 'divider',
                bgcolor: 'background.default',
                fontSize: 'var(--ds-text-body)',
                lineHeight: 1.6,
              }}
            >
              <MarkDowns data={planData?.planText} sx={{}} allowExecutable={false} onLinkClick={null} />
            </Box>
            {showFeedbackInput && (
              <Box sx={{ mt: 2 }}>
                <FormField
                  value={feedbackText}
                  onChange={(e) => setFeedbackText(e.target.value)}
                  label='What changes would you like?'
                  fieldType='textarea'
                  placeholder='Describe the changes you want to make to this plan...'
                  multiline
                  minRows={3}
                  maxRows={8}
                />
              </Box>
            )}
          </Box>
        );

      case 'text_followup':
        return (
          <Box sx={{ mt: 2, mb: 2 }}>
            <Typography variant='subtitle2' sx={{ mb: 1, fontWeight: 'var(--ds-font-weight-semibold)' }}>
              {planData?.planText || 'Please provide additional details:'}
            </Typography>
            <Box sx={{ mt: 2 }}>
              <FormField
                value={feedbackText}
                onChange={(e) => setFeedbackText(e.target.value)}
                label='Your response'
                fieldType='textarea'
                placeholder='Type your response...'
                multiline
                minRows={3}
                maxRows={8}
              />
            </Box>
          </Box>
        );

      case 'success': {
        // Drop a trailing "[Open in Editor](...)" link from the summary — we render our own button.
        const successMarkdown = (summaryText || '').replace(/\n*\[Open in Editor\]\([^)]*\)\s*$/i, '').trim();
        return (
          <Box sx={{ mt: 2, mb: 2 }}>
            <Box
              sx={{
                p: 2,
                borderRadius: 'var(--ds-radius-sm)',
                border: '1px solid',
                borderColor: 'divider',
                bgcolor: 'background.default',
                fontSize: 'var(--ds-text-body)',
                lineHeight: 1.6,
              }}
            >
              {successMarkdown ? (
                <MarkDowns data={successMarkdown} sx={{}} allowExecutable={false} onLinkClick={null} />
              ) : (
                <Typography variant='body1' sx={{ color: 'text.primary' }}>
                  Your automation has been built and saved.
                </Typography>
              )}
            </Box>
          </Box>
        );
      }

      case 'error':
        return (
          <Box
            sx={{
              mt: 4,
              mb: 4,
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: 2,
              minHeight: '200px',
              justifyContent: 'center',
            }}
          >
            {errorMessage && (
              <Typography variant='body1' sx={{ color: 'error.main', fontWeight: 'var(--ds-font-weight-medium)', textAlign: 'center' }}>
                {errorMessage}
              </Typography>
            )}
            {errorDetail && (
              <Box
                id='wf-ai-error-detail'
                sx={{
                  alignSelf: 'stretch',
                  p: ds.space[4],
                  borderRadius: 'var(--ds-radius-sm)',
                  border: '1px solid',
                  borderColor: 'divider',
                  bgcolor: 'background.default',
                  fontSize: 'var(--ds-text-body)',
                  lineHeight: 1.6,
                }}
              >
                <MarkDowns data={errorDetail} sx={{}} allowExecutable={false} onLinkClick={null} />
              </Box>
            )}
          </Box>
        );

      default:
        return null;
    }
  };

  const renderActionButtons = () => {
    switch (stage) {
      case 'input':
        return (
          <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
            <Button id='wf-ai-cancel-btn' tone='secondary' size='md' onClick={handleClose} disabled={loading}>
              Cancel
            </Button>
            <Button id='wf-ai-generate-btn' tone='primary' size='md' onClick={handleSubmit} disabled={loading || !query.trim()} loading={loading}>
              {loading ? 'Generating...' : 'Generate Automation'}
            </Button>
          </Box>
        );

      case 'generating':
        return (
          <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
            <Button tone='secondary' size='md' onClick={handleCancelGeneration}>
              Cancel Generation
            </Button>
          </Box>
        );

      case 'plan_review':
        // Clarification questions handle their own interactions — only show cancel
        if (planData?.followupData?.type === 'clarification') {
          return (
            <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
              <Button tone='secondary' size='md' onClick={handleClose}>
                Cancel
              </Button>
            </Box>
          );
        }
        if (showFeedbackInput) {
          return (
            <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
              <Button tone='secondary' size='md' onClick={() => setShowFeedbackInput(false)}>
                Back
              </Button>
              <Button tone='primary' size='md' onClick={handleSubmitFeedback} disabled={!feedbackText.trim()}>
                Submit Changes
              </Button>
            </Box>
          );
        }
        // Check if this is a non-plan followup (e.g., config approval) by seeing
        // if the options differ from the default plan approval options
        if (
          planData?.options &&
          planData.options.length > 0 &&
          !(planData.options.includes('Approve and Build') && planData.options.includes('Request Changes'))
        ) {
          return (
            <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
              <Button tone='secondary' size='md' onClick={handleClose}>
                Cancel
              </Button>
              {planData.options.map((option, index) => (
                <Button
                  key={option}
                  tone={index === 0 ? 'primary' : 'secondary'}
                  size='md'
                  onClick={async () => {
                    if (!planData) return;
                    setStage('generating');
                    setStageMessageIndex(3);
                    try {
                      await onApproveOrRespond?.(option, planData.conversationId, planData.sessionId, planData.messageId, planData.agentId);
                      startPolling(planData.sessionId, planData.conversationId);
                    } catch (err) {
                      console.error('Error responding to followup:', err);
                      setErrorMessage('Failed to submit response. Please try again.');
                      setStage('error');
                    }
                  }}
                >
                  {option}
                </Button>
              ))}
            </Box>
          );
        }
        return (
          <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
            <Button tone='secondary' size='md' onClick={handleClose}>
              Cancel
            </Button>
            <Button tone='secondary' size='md' onClick={handleRequestChanges}>
              Request Changes
            </Button>
            <Button tone='primary' size='md' onClick={handleApprove}>
              Approve and Build
            </Button>
          </Box>
        );

      case 'text_followup':
        return (
          <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
            <Button tone='secondary' size='md' onClick={handleClose}>
              Cancel
            </Button>
            <Button tone='primary' size='md' onClick={handleSubmitFeedback} disabled={!feedbackText.trim()}>
              Submit
            </Button>
          </Box>
        );

      case 'success': {
        // A new automation is saved paused and "built and saved" was where this window stopped,
        // so most were never run and almost none with a schedule was switched on. Offer both
        // here. One primary button: the next step for this automation, else Open in Editor.
        const savedWorkflow = completedData?.savedWorkflow;
        const actions = getAiBuildActions(savedWorkflow);
        const showRunNow = Boolean(savedWorkflow && actions.runNow && onRunNow);
        const showActivate = Boolean(savedWorkflow && actions.activate && onActivate);
        return (
          <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
            <Button tone='secondary' size='md' onClick={handleClose}>
              Close
            </Button>
            <Button
              id='wf-ai-open-editor-btn'
              tone={showRunNow || showActivate ? 'secondary' : 'primary'}
              size='md'
              onClick={() => {
                if (!completedData) {
                  return;
                }
                onWorkflowCompleted?.(completedData.workflowJson, completedData.conversationId, completedData.sessionId);
              }}
            >
              Open in Editor
            </Button>
            {savedWorkflow && showActivate && (
              <Button id='wf-ai-activate-btn' tone={showRunNow ? 'secondary' : 'primary'} size='md' onClick={() => onActivate?.(savedWorkflow)}>
                Activate
              </Button>
            )}
            {savedWorkflow && showRunNow && (
              <Button id='wf-ai-run-now-btn' tone='primary' size='md' onClick={() => onRunNow?.(savedWorkflow)}>
                Run now
              </Button>
            )}
          </Box>
        );
      }

      case 'error':
        return (
          <Box sx={{ p: 2, pt: 0, display: 'flex', gap: 2, justifyContent: 'flex-end', '& button': { minWidth: '140px' } }}>
            <Button tone='secondary' size='md' onClick={handleClose}>
              Close
            </Button>
            <Button tone='primary' size='md' onClick={handleRetry}>
              Try Again
            </Button>
          </Box>
        );

      default:
        return null;
    }
  };

  const getTitle = () => {
    switch (stage) {
      case 'input':
        return 'Generate Automation with AI';
      case 'generating':
        return 'Generating Automation...';
      case 'plan_review':
        if (
          planData?.options &&
          planData.options.length > 0 &&
          !(planData.options.includes('Approve and Build') && planData.options.includes('Request Changes'))
        ) {
          return 'Action Required';
        }
        return 'Review Automation Plan';
      case 'text_followup':
        return 'Additional Information Needed';
      case 'success':
        return 'Automation Built';
      case 'error':
        // With the AI's reply on screen the run did finish; what is missing is a saved automation.
        return errorDetail ? 'Automation not saved' : 'Generation Failed';
      default:
        return 'Generate Automation with AI';
    }
  };

  return (
    <Modal open={open} handleClose={handleClose} width='md' title={getTitle()} actionButtons={renderActionButtons()}>
      {renderContent()}
    </Modal>
  );
};

export default AiGenerateWorkflowModal;
