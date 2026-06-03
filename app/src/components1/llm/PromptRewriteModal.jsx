import React, { useState, useEffect, useRef } from 'react';
import { Box, Typography, TextareaAutosize } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Modal } from '@components1/ds/Modal';
import CopyButton from '@common-new/CopyButton';
import { toast as snackbar } from '@components1/ds/Toast';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { v4 as uuidv4 } from 'uuid';

const styles = {
  label: {
    mb: 'var(--ds-space-2)',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-medium)',
    color: 'var(--ds-gray-700)',
  },
  instructionText: {
    fontSize: 'var(--ds-text-small)',
    color: 'var(--ds-gray-700)',
    mb: 'var(--ds-space-2)',
    lineHeight: 1.4,
  },
  textareaContainer: {
    position: 'relative',
    border: '1px solid var(--ds-gray-200)',
    borderRadius: 'var(--ds-radius-md)',
    backgroundColor: 'var(--ds-background-100)',
  },
  copyButton: {
    position: 'absolute',
    top: 'var(--ds-space-2)',
    right: 'var(--ds-space-2)',
  },
};

const PromptRewriteModal = ({ open, onClose, currentPrompt, onPromptUpdate, accountId }) => {
  const [additionalInstructions, setAdditionalInstructions] = useState('');
  const [suggestedPrompt, setSuggestedPrompt] = useState('');
  const [editableCurrentPrompt, setEditableCurrentPrompt] = useState(currentPrompt);
  const [isRewriting, setIsRewriting] = useState(false);
  const [hasRewritten, setHasRewritten] = useState(false);
  const [_sessionId, setSessionId] = useState('');
  const [rewriteStatus, setRewriteStatus] = useState('');
  const intervalRef = useRef(null);

  // Sync editableCurrentPrompt with currentPrompt prop changes
  useEffect(() => {
    setEditableCurrentPrompt(currentPrompt);
  }, [currentPrompt]);

  const handleRewritePrompt = async () => {
    if (rewriteStatus === 'IN_PROGRESS' || !editableCurrentPrompt.trim()) {
      return;
    }

    const rewriteSessionId = uuidv4();
    setSessionId(rewriteSessionId);
    setIsRewriting(true);
    setRewriteStatus('IN_PROGRESS');
    setSuggestedPrompt('');
    setHasRewritten(false);

    // Create the rewrite query
    const rewriteQuery = `@PromptRefiner Please rewrite and improve the following prompt for better AI comprehension and effectiveness:
Current Prompt:
${editableCurrentPrompt}

${
  additionalInstructions
    ? `Additional Instructions:
${additionalInstructions}`
    : ''
}
Return only the improved prompt without any additional explanation.`;

    try {
      const response = await apiAskNudgebee.aiGenerateInvestigate({
        account_id: accountId,
        query: rewriteQuery,
        session_id: rewriteSessionId,
      });

      const investigateResponse = response?.data?.data?.ai_execute_investigation ?? {};

      if (!investigateResponse?.data?.query) {
        setIsRewriting(false);
        setRewriteStatus('FAILED');
        snackbar.error('Failed to start prompt rewriting. Please try again.');
        return;
      }

      // Start polling for results
      startPolling(rewriteSessionId);
    } catch {
      setIsRewriting(false);
      setRewriteStatus('FAILED');
      snackbar.error('Failed to rewrite prompt');
    }
  };

  const startPolling = (rewriteSessionId) => {
    // Clear any existing interval
    if (intervalRef.current) {
      clearInterval(intervalRef.current);
    }

    intervalRef.current = setInterval(async () => {
      await pollRewriteStatus(rewriteSessionId);
    }, 3000); // Poll every 3 seconds
  };

  const pollRewriteStatus = async (rewriteSessionId) => {
    try {
      const response = await apiAskNudgebee.getLlmConversation({
        accountId: accountId,
        sessionId: rewriteSessionId,
      });

      const errors = response?.data?.errors ?? [];
      if (errors.length > 0) {
        return;
      }

      const conversations = response?.data?.data?.llm_conversations ?? [];

      if (conversations.length > 0) {
        const conversation = conversations[0];

        // Check if conversation is completed
        if (conversation.status === 'COMPLETED') {
          const messages = conversation.llm_conversation_messages || [];

          // Look for the completed message with response
          const completedMessage = messages.find((msg) => msg.status === 'COMPLETED' && msg.response && msg.response.trim());

          if (completedMessage) {
            // Found completed response
            setSuggestedPrompt(completedMessage.response);
            setHasRewritten(true);
            setIsRewriting(false);
            setRewriteStatus('COMPLETED');
            snackbar.success('Prompt rewritten successfully');

            // Clear polling
            if (intervalRef.current) {
              clearInterval(intervalRef.current);
              intervalRef.current = null;
            }
          } else {
            setIsRewriting(false);
            setRewriteStatus('FAILED');
            snackbar.error('No valid response received');

            // Clear polling
            if (intervalRef.current) {
              clearInterval(intervalRef.current);
              intervalRef.current = null;
            }
          }
        } else if (conversation.status === 'FAILED') {
          setIsRewriting(false);
          setRewriteStatus('FAILED');
          snackbar.error('Failed to rewrite prompt');

          // Clear polling
          if (intervalRef.current) {
            clearInterval(intervalRef.current);
            intervalRef.current = null;
          }
        }
      }
    } catch {
      // Error polling rewrite status
    }
  };

  const handleUpdatePrompt = () => {
    if (suggestedPrompt.trim()) {
      onPromptUpdate(suggestedPrompt);
      onClose();
      snackbar.success('Prompt updated successfully');
    }
  };

  const handleClose = () => {
    // Clear polling interval
    if (intervalRef.current) {
      clearInterval(intervalRef.current);
      intervalRef.current = null;
    }

    // Reset state when closing
    setAdditionalInstructions('');
    setSuggestedPrompt('');
    setEditableCurrentPrompt(currentPrompt);
    setHasRewritten(false);
    setIsRewriting(false);
    setSessionId('');
    setRewriteStatus('');
    onClose();
  };

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, []);

  return (
    <Modal width='xl' title='Rewrite Prompt Function' open={open} handleClose={handleClose} onClose={handleClose} loader={isRewriting}>
      <Box sx={{ p: 'var(--ds-space-6)' }}>
        {/* Current Prompt */}
        <Box sx={{ mb: 'var(--ds-space-6)' }}>
          <Typography sx={styles.label}>Prompt - Current Version</Typography>
          <Typography sx={styles.instructionText}>
            Show current prompt in editable text version. Provide ability for user to override it here as well. Show 20 lines, scrollable text area.
          </Typography>
          <Box sx={styles.textareaContainer}>
            <TextareaAutosize
              value={editableCurrentPrompt}
              onChange={(e) => setEditableCurrentPrompt(e.target.value)}
              minRows={20}
              style={{
                width: '100%',
                padding: 'var(--ds-space-3)',
                border: 'none',
                outline: 'none',
                fontSize: 'var(--ds-text-body-lg)',
                fontFamily: 'inherit',
                resize: 'vertical',
                backgroundColor: 'transparent',
              }}
            />
            <CopyButton text={editableCurrentPrompt} size='sm' />
          </Box>
        </Box>

        {/* Additional Instructions */}
        <Box sx={{ mb: 'var(--ds-space-6)' }}>
          <Typography sx={styles.label}>Enter Any Additional Instructions for Rewriting</Typography>
          <Typography sx={styles.instructionText}>
            Note: You can provide additional instructions, guidance or tips that NudgeBee should consider while rewriting this prompt
          </Typography>
          <TextareaAutosize
            value={additionalInstructions}
            onChange={(e) => setAdditionalInstructions(e.target.value)}
            minRows={5}
            maxRows={10}
            placeholder='Enter additional instructions for rewriting the prompt...'
            style={{
              width: '100%',
              padding: 'var(--ds-space-3)',
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-md)',
              fontSize: 'var(--ds-text-body-lg)',
              fontFamily: 'inherit',
              resize: 'vertical',
              outline: 'none',
            }}
          />
        </Box>

        {/* Rewrite Button */}
        <Box sx={{ mb: 'var(--ds-space-6)', textAlign: 'center' }}>
          <Button tone='primary' size='md' onClick={handleRewritePrompt} loading={isRewriting}>
            Rewrite Prompt
          </Button>
        </Box>

        {/* Suggested Prompt - Only show after rewriting */}
        {hasRewritten && (
          <Box sx={{ mb: 'var(--ds-space-6)' }}>
            <Typography sx={styles.label}>Prompt - Suggested Version</Typography>
            <Typography sx={styles.instructionText}>
              Show the full prompt regardless of the lines. Show it in a text area, so that the user can make edits here itself, and then copy the
              prompt for further refining.
            </Typography>
            <Box sx={styles.textareaContainer}>
              <TextareaAutosize
                value={suggestedPrompt}
                onChange={(e) => setSuggestedPrompt(e.target.value)}
                minRows={20}
                style={{
                  width: '100%',
                  padding: 'var(--ds-space-3)',
                  border: 'none',
                  outline: 'none',
                  fontSize: 'var(--ds-text-body-lg)',
                  fontFamily: 'inherit',
                  resize: 'vertical',
                  backgroundColor: 'transparent',
                }}
              />
              <CopyButton text={suggestedPrompt} size='sm' toastMessage='Copied to clipboard' />
            </Box>
          </Box>
        )}

        {/* Action Buttons */}
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'flex-end' }}>
          <Button tone='secondary' size='md' onClick={handleClose} disabled={isRewriting}>
            Cancel
          </Button>
          {hasRewritten && (
            <Button tone='primary' size='md' onClick={handleUpdatePrompt}>
              Update Prompt Function
            </Button>
          )}
        </Box>
      </Box>
    </Modal>
  );
};

export default PromptRewriteModal;
