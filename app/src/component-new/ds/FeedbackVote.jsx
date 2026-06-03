/**
 * FeedbackVote — DS V2. Full re-implementation of legacy ThumpsUpAndDown.
 * Spec: app/design-system/primitives/agentic/feedback-vote.html
 *
 * Prop API preserved from V1, plus:
 *   iconOnly — render the thumbs as icon-only buttons (no "Yes"/"No" labels).
 *              aria-labels ("Yes"/"No") are kept for accessibility. Use in
 *              compact inline contexts (e.g. per-message feedback rows).
 */
import React, { useState, useEffect } from 'react';
import { Box } from '@mui/material';
import { BiLike, BiDislike } from 'react-icons/bi';
import PropTypes from 'prop-types';
import { Button } from './Button';
import { Checkbox } from './Checkbox';
import { Input } from './Input';
import Modal from './Modal';
import Tooltip from './Tooltip';
import { toast } from './Toast';

const FEEDBACK_OPTIONS = [
  'Agent/Plan Incorrect',
  'Input parameters incorrect',
  '100% incorrect response',
  'Partially correct response, but missing some details',
  'Not able to get response',
  'Poorly formatted response',
];

const FeedbackVote = ({ onFeedbackSubmit, sentFeedback = {}, iconOnly = false }) => {
  const [openDialog, setOpenDialog] = useState(false);
  const [feedback, setFeedback] = useState('');
  const [feedbackStatus, setFeedbackStatus] = useState({ submitted: false, isPositive: null });
  const [selectedOptions, setSelectedOptions] = useState([]);

  useEffect(() => {
    setFeedbackStatus({
      submitted: sentFeedback.submitted ?? false,
      isPositive: sentFeedback.isPositive ?? null,
    });
  }, [sentFeedback]);

  const handleThumbsUp = async () => {
    try {
      await onFeedbackSubmit({ type: 'thumbs_up', message: '' });
      setFeedbackStatus({ submitted: true, isPositive: true });
      toast.success('Feedback submitted successfully!');
    } catch {
      toast.error('Error submitting feedback. Please try again.');
    }
  };

  const handleThumbsDown = () => setOpenDialog(true);

  const handleCloseDialog = () => {
    setOpenDialog(false);
    setFeedback('');
    setSelectedOptions([]);
  };

  const handleToggleOption = (option) => {
    setSelectedOptions((prev) => (prev.includes(option) ? prev.filter((o) => o !== option) : [...prev, option]));
  };

  const handleSubmitFeedback = async () => {
    try {
      await onFeedbackSubmit({
        type: 'thumbs_down',
        message: (selectedOptions.join(', ') + ' ' + feedback).trim(),
      });
      setFeedbackStatus({ submitted: true, isPositive: false });
      handleCloseDialog();
      toast.success('Feedback submitted successfully!');
    } catch {
      toast.error('Error submitting feedback. Please try again.');
    }
  };

  const isYesActive = feedbackStatus.submitted && feedbackStatus.isPositive;
  const isNoActive = feedbackStatus.submitted && !feedbackStatus.isPositive;

  const hasNegativeFeedback = isNoActive && sentFeedback.message && sentFeedback.message.trim() !== '';

  const negativeTooltipContent = hasNegativeFeedback ? (
    <Box>
      <Box sx={{ fontWeight: 'var(--ds-font-weight-medium)', mb: 'var(--ds-space-1)' }}>The Feedback:</Box>
      {sentFeedback.message.split(',').map((msg, idx) => (
        <Box key={idx}>{msg.trim()}</Box>
      ))}
    </Box>
  ) : null;

  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      {/* Thumbs up */}
      <Box sx={isYesActive ? { '& button': { color: 'var(--ds-green-500)', borderColor: 'var(--ds-green-500)' } } : {}}>
        <Button
          tone='secondary'
          size='xs'
          composition={iconOnly ? 'icon-only' : undefined}
          aria-label={iconOnly ? 'Yes' : undefined}
          icon={<BiLike />}
          onClick={handleThumbsUp}
        >
          {iconOnly ? undefined : 'Yes'}
        </Button>
      </Box>

      {/* Thumbs down — wrapped in Tooltip when submitted feedback exists */}
      <Box sx={isNoActive ? { '& button': { color: 'var(--ds-red-500)', borderColor: 'var(--ds-red-500)' } } : {}}>
        {hasNegativeFeedback ? (
          <Tooltip title={negativeTooltipContent} placement='top'>
            <span>
              <Button
                tone='secondary'
                size='xs'
                composition={iconOnly ? 'icon-only' : undefined}
                aria-label={iconOnly ? 'No' : undefined}
                icon={<BiDislike />}
                onClick={handleThumbsDown}
              >
                {iconOnly ? undefined : 'No'}
              </Button>
            </span>
          </Tooltip>
        ) : (
          <Button
            tone='secondary'
            size='xs'
            composition={iconOnly ? 'icon-only' : undefined}
            aria-label={iconOnly ? 'No' : undefined}
            icon={<BiDislike />}
            onClick={handleThumbsDown}
          >
            {iconOnly ? undefined : 'No'}
          </Button>
        )}
      </Box>

      <Modal
        open={openDialog}
        handleClose={handleCloseDialog}
        title='What went wrong?'
        contentStyles={{ padding: 'var(--ds-space-4) var(--ds-space-5)' }}
        actionButtons={
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', justifyContent: 'flex-end', padding: 'var(--ds-space-1) var(--ds-space-2)' }}>
            <Button tone='secondary' size='md' onClick={handleCloseDialog}>
              Cancel
            </Button>
            <Button tone='primary' size='md' disabled={selectedOptions.length === 0 && feedback.trim() === ''} onClick={handleSubmitFeedback}>
              Submit
            </Button>
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
            {FEEDBACK_OPTIONS.map((option) => (
              <Checkbox
                key={option}
                checked={selectedOptions.includes(option)}
                onChange={() => handleToggleOption(option)}
                label={option}
                size='md'
              />
            ))}
          </Box>
          <Input
            type='textarea'
            label='Your Feedback'
            placeholder='What was unsatisfying about this response?'
            value={feedback}
            onChange={setFeedback}
            rows={4}
          />
        </Box>
      </Modal>
    </Box>
  );
};

FeedbackVote.propTypes = {
  onFeedbackSubmit: PropTypes.func.isRequired,
  sentFeedback: PropTypes.shape({
    submitted: PropTypes.bool,
    isPositive: PropTypes.bool,
    message: PropTypes.string,
  }),
  /** Render thumbs as icon-only buttons (no "Yes"/"No" labels). aria-labels are kept. */
  iconOnly: PropTypes.bool,
};

export default FeedbackVote;
