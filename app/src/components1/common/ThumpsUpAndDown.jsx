import React, { useState, useEffect } from 'react';
import { Box, FormGroup } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import { Input } from '@components1/ds/Input';
import { Modal } from './modal';
import { snackbar } from './snackbarService';
import PropTypes from 'prop-types';
import { BiDislike, BiLike } from 'react-icons/bi';
import CustomButton from './NewCustomButton';

const FeedbackComponent = ({ onFeedbackSubmit, sentFeedback = {} }) => {
  const [openDialog, setOpenDialog] = useState(false);
  const [feedback, setFeedback] = useState('');
  const [feedbackStatus, setFeedbackStatus] = useState({
    submitted: false,
    isPositive: null,
  });
  const [selectedOptions, setSelectedOptions] = useState([]);

  const options = [
    'Agent/Plan Incorrect',
    'Input parameters incorrect',
    '100% incorrect response',
    'Partially correct response, but missing some details',
    'Not able to get response',
    'Poorly formatted response',
  ];

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
      snackbar.success('Feedback submitted successfully!');
    } catch {
      snackbar.error('Error submitting feedback. Please try again.');
    }
  };

  const handleThumbsDown = () => {
    setOpenDialog(true);
  };

  const handleCloseDialog = () => {
    setOpenDialog(false);
    setFeedback('');
    setSelectedOptions([]);
  };

  const handleSelect = (option) => {
    setSelectedOptions((prev) => (prev.includes(option) ? prev.filter((item) => item !== option) : [...prev, option]));
  };

  const handleSubmitFeedback = async () => {
    try {
      await onFeedbackSubmit({ type: 'thumbs_down', message: (selectedOptions.join(', ') + ' ' + feedback).trim() });
      setFeedbackStatus({ submitted: true, isPositive: false });
      handleCloseDialog();
      snackbar.success('Feedback submitted successfully!');
    } catch {
      snackbar.error('Error submitting feedback. Please try again.');
    }
  };

  const additionalComponent = () => {
    return (
      <>
        <FormGroup sx={{ mb: 2 }}>
          {options.map((option) => (
            <Box key={option} sx={{ mb: 0.5 }}>
              <Checkbox size='md' checked={selectedOptions.includes(option)} onChange={() => handleSelect(option)} label={option} />
            </Box>
          ))}
        </FormGroup>
        <Box sx={{ mt: 1 }}>
          <Input
            id='feedback'
            label='Your Feedback'
            type='textarea'
            rows={4}
            size='sm'
            value={feedback}
            placeholder='What was unsatisfying about this response?'
            onChange={setFeedback}
          />
        </Box>
      </>
    );
  };

  return (
    <div>
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
        <CustomButton
          variant='secondary'
          size='xSmall'
          text='Yes'
          startIcon={<BiLike />}
          onClick={handleThumbsUp}
          sx={{
            minWidth: '60px',
            fontSize: 'var(--ds-text-small)',
            height: '24px',
            fontWeight: 'var(--ds-font-weight-regular)',
            color: feedbackStatus.submitted && feedbackStatus.isPositive ? '#22C55E' : 'inherit',
            border: feedbackStatus.submitted && feedbackStatus.isPositive ? '1px solid #22C55E' : '0.5px solid #D0D5DD',
            '& svg': {
              height: '14px',
              width: '14px',
              color: 'var(--ds-green-500)',
              filter:
                feedbackStatus.submitted && feedbackStatus.isPositive
                  ? 'none'
                  : 'brightness(0) saturate(100%) invert(39%) sepia(100%) saturate(13%) hue-rotate(139deg) brightness(94%) contrast(86%)',
            },
          }}
        />
        <CustomButton
          variant='secondary'
          size='xSmall'
          text='No'
          startIcon={<BiDislike />}
          onClick={handleThumbsDown}
          showTooltip={feedbackStatus.submitted && !feedbackStatus.isPositive && !!sentFeedback.message && sentFeedback.message.trim() !== ''}
          toolTipTitle={
            sentFeedback.message && sentFeedback.message.trim() !== '' ? (
              <span>
                The Feedback:
                <br />
                {sentFeedback.message.split(',').map((msg, idx) => (
                  <span key={idx}>
                    {msg.trim()}
                    <br />
                  </span>
                ))}
              </span>
            ) : undefined
          }
          sx={{
            minWidth: '60px',
            fontSize: 'var(--ds-text-small)',
            height: '24px',
            fontWeight: 'var(--ds-font-weight-regular)',
            color: feedbackStatus.submitted && !feedbackStatus.isPositive ? '#EF4444' : 'inherit',
            border: feedbackStatus.submitted && !feedbackStatus.isPositive ? '1px solid #EF4444' : '0.5px solid #D0D5DD',
            '& svg': {
              height: '14px',
              width: '14px',
              color: 'var(--ds-red-500)',
              filter:
                feedbackStatus.submitted && !feedbackStatus.isPositive
                  ? 'none'
                  : 'brightness(0) saturate(100%) invert(39%) sepia(100%) saturate(13%) hue-rotate(139deg) brightness(94%) contrast(86%)',
            },
          }}
        />
      </Box>

      <Modal
        open={openDialog}
        handleClose={handleCloseDialog}
        title='What went wrong?'
        actionButtons={
          <Box sx={{ display: 'flex', gap: 2, justifyContent: 'flex-end', padding: 'var(--ds-space-1) var(--ds-space-2)' }}>
            <CustomButton variant='secondary' text='Cancel' onClick={handleCloseDialog} size='Medium' />
            <CustomButton
              text='Submit'
              onClick={handleSubmitFeedback}
              size='Medium'
              disabled={selectedOptions.length === 0 && feedback.trim() === ''}
            />
          </Box>
        }
      >
        {additionalComponent()}
      </Modal>
    </div>
  );
};

FeedbackComponent.propTypes = {
  onFeedbackSubmit: PropTypes.func.isRequired,
  sentFeedback: PropTypes.shape({
    submitted: PropTypes.bool,
    isPositive: PropTypes.bool,
    message: PropTypes.string,
  }),
};

export default FeedbackComponent;
