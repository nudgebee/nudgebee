import React, { useState, useEffect, useRef } from 'react';
import { Box, Typography, IconButton, TextareaAutosize } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import CustomButton from '@components1/common/NewCustomButton';
import { Modal } from '@components1/common/modal';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { v4 as uuidv4 } from 'uuid';

const styles = {
  label: {
    mb: 1,
    fontSize: '14px',
    fontWeight: 500,
    color: colors.text.secondary,
  },
  instructionText: {
    fontSize: '12px',
    color: colors.text.secondary,
    mb: 1,
    lineHeight: 1.4,
  },
  inputField: {
    fontSize: '14px',
    '& .MuiOutlinedInput-root': {
      borderRadius: '8px',
      backgroundColor: 'white',
      '& fieldset': {
        borderColor: colors.border.vertical,
      },
      '&:hover fieldset': {
        borderColor: colors.border.vertical,
      },
    },
    '& .MuiInputBase-input': {
      padding: '8px 12px',
    },
  },
  textareaContainer: {
    position: 'relative',
    border: `1px solid ${colors.border.vertical}`,
    borderRadius: '8px',
    backgroundColor: 'white',
  },
  copyButton: {
    position: 'absolute',
    top: '8px',
    right: '8px',
    backgroundColor: '#F5F5F5',
    '&:hover': {
      backgroundColor: '#EEEEEE',
    },
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

      const investigateResponse = response?.data?.data?.ai_trigger_investigation ?? {};

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

  const copyToClipboard = (text) => {
    navigator.clipboard.writeText(text);
    snackbar.success('Copied to clipboard');
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
      <Box sx={{ p: 3 }}>
        {/* Current Prompt */}
        <Box sx={{ mb: 3 }}>
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
                padding: '12px',
                border: 'none',
                outline: 'none',
                fontSize: '14px',
                fontFamily: 'inherit',
                resize: 'vertical',
                backgroundColor: 'transparent',
              }}
            />
            <IconButton sx={styles.copyButton} size='small' onClick={() => copyToClipboard(editableCurrentPrompt)}>
              <ContentCopyIcon fontSize='small' />
            </IconButton>
          </Box>
        </Box>

        {/* Additional Instructions */}
        <Box sx={{ mb: 3 }}>
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
              padding: '12px',
              border: `1px solid ${colors.border.vertical}`,
              borderRadius: '8px',
              fontSize: '14px',
              fontFamily: 'inherit',
              resize: 'vertical',
              outline: 'none',
            }}
          />
        </Box>

        {/* Rewrite Button */}
        <Box sx={{ mb: 3, textAlign: 'center' }}>
          <CustomButton text='Rewrite Prompt' size='Medium' onClick={handleRewritePrompt} loading={isRewriting} />
        </Box>

        {/* Suggested Prompt - Only show after rewriting */}
        {hasRewritten && (
          <Box sx={{ mb: 3 }}>
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
                  padding: '12px',
                  border: 'none',
                  outline: 'none',
                  fontSize: '14px',
                  fontFamily: 'inherit',
                  resize: 'vertical',
                  backgroundColor: 'transparent',
                }}
              />
              <IconButton sx={styles.copyButton} size='small' onClick={() => copyToClipboard(suggestedPrompt)}>
                <ContentCopyIcon fontSize='small' />
              </IconButton>
            </Box>
          </Box>
        )}

        {/* Action Buttons */}
        <Box sx={{ display: 'flex', gap: 2, justifyContent: 'flex-end' }}>
          <CustomButton text='Cancel' variant='secondary' size='Medium' onClick={handleClose} disabled={isRewriting} />
          {hasRewritten && <CustomButton text='Update Prompt Function' size='Medium' onClick={handleUpdatePrompt} />}
        </Box>
      </Box>
    </Modal>
  );
};

export default PromptRewriteModal;
