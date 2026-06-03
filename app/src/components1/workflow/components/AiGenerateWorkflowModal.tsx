import { useState, useEffect, useRef, useCallback } from 'react';
import { Box, CircularProgress, LinearProgress, Typography } from '@mui/material';
import { Modal } from '@components1/common/modal';
import { Button } from '@components1/ds/Button';
import { FormField } from '@components1/common/NewReusabeFormComponents';
import MarkDowns from '@components1/common/MarkDowns';
import ClarificationQuestion from './ClarificationQuestion';

type ModalStage = 'input' | 'generating' | 'plan_review' | 'text_followup' | 'error';

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
    workflowJson?: string;
    planText?: string;
    planOptions?: string[];
    followupType?: string;
    followupData?: any;
    conversationId?: string;
    messageId?: string;
    messageUpdatedAt?: string;
    agentId?: string;
    errorMessage?: string;
  } | null>;
  onApproveOrRespond?: (query: string, conversationId: string, sessionId: string, messageId?: string, agentId?: string) => Promise<void>;
  onCancel?: (conversationId: string) => Promise<void>;
  onWorkflowCompleted?: (workflowJson: string, conversationId: string, sessionId: string) => void;
  loading: boolean;
}

const POLL_INTERVAL_MS = 4000;

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
  loading,
}) => {
  const [query, setQuery] = useState('');
  const [stage, setStage] = useState<ModalStage>('input');
  const [progress, setProgress] = useState<GenerationProgress | null>(null);
  const [planData, setPlanData] = useState<PlanData | null>(null);
  const [feedbackText, setFeedbackText] = useState('');
  const [showFeedbackInput, setShowFeedbackInput] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');
  const [stageMessageIndex, setStageMessageIndex] = useState(0);

  const pollIntervalRef = useRef<NodeJS.Timeout | null>(null);
  const elapsedIntervalRef = useRef<NodeJS.Timeout | null>(null);
  const lastPlanMessageIdRef = useRef<string>('');

  const clearIntervals = useCallback(() => {
    if (pollIntervalRef.current) {
      clearInterval(pollIntervalRef.current);
      pollIntervalRef.current = null;
    }
    if (elapsedIntervalRef.current) {
      clearInterval(elapsedIntervalRef.current);
      elapsedIntervalRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => clearIntervals();
  }, [clearIntervals]);

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
      const startedAt = Date.now();
      setProgress({ sessionId, conversationId, startedAt, elapsedSeconds: 0 });

      // Update elapsed timer every second
      elapsedIntervalRef.current = setInterval(() => {
        setProgress((prev) => (prev ? { ...prev, elapsedSeconds: Math.floor((Date.now() - startedAt) / 1000) } : prev));
      }, 1000);

      // Poll for status
      pollIntervalRef.current = setInterval(async () => {
        try {
          const result = await onPollConversation?.(sessionId);
          if (!result) {
            return;
          }

          if (result.status === 'COMPLETED' && result.workflowJson) {
            clearIntervals();
            onWorkflowCompleted?.(result.workflowJson, result.conversationId || conversationId, sessionId);
          } else if (result.status === 'WAITING' && result.planText) {
            // Skip stale WAITING results after approval/feedback —
            // same messageId+updated_at means the backend hasn't processed our response yet.
            // We include updated_at because the backend reuses the same followup message
            // for config approval (different content, same ID, but updated_at changes).
            const staleKey = (result.messageId || '') + ':' + (result.messageUpdatedAt || '');
            if (lastPlanMessageIdRef.current && lastPlanMessageIdRef.current === staleKey) {
              return;
            }
            clearIntervals();
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
    },
    [onPollConversation, onWorkflowCompleted, clearIntervals]
  );

  const handleSubmit = async () => {
    if (!query.trim()) {
      return;
    }

    setStage('generating');
    setStageMessageIndex(0);
    setErrorMessage('');

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
    setStageMessageIndex(0);
    lastPlanMessageIdRef.current = '';
    onClose();
  };

  const handleRetry = () => {
    setStage('input');
    setErrorMessage('');
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
          <Box sx={{ mt: 2, mb: 2 }}>
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
            <CircularProgress size={48} />
            <Typography variant='body1' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: 'text.primary' }}>
              {STAGE_MESSAGES[stageMessageIndex]}
            </Typography>
            <LinearProgress variant='indeterminate' sx={{ width: '60%', borderRadius: 1 }} />
            {progress && (
              <Typography variant='body2' sx={{ color: 'text.secondary' }}>
                Elapsed: {formatElapsed(progress.elapsedSeconds)}
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
                borderRadius: 1,
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
            <Typography variant='body1' sx={{ color: 'error.main', fontWeight: 'var(--ds-font-weight-medium)', textAlign: 'center' }}>
              {errorMessage}
            </Typography>
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
            <Button tone='secondary' size='md' onClick={handleClose} disabled={loading}>
              Cancel
            </Button>
            <Button tone='primary' size='md' onClick={handleSubmit} disabled={loading || !query.trim()} loading={loading}>
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
      case 'error':
        return 'Generation Failed';
      default:
        return 'Generate Automation with AI';
    }
  };

  return (
    <Modal open={open} handleClose={handleClose} width='md' title={getTitle()} loader={stage === 'generating'} actionButtons={renderActionButtons()}>
      {renderContent()}
    </Modal>
  );
};

export default AiGenerateWorkflowModal;
