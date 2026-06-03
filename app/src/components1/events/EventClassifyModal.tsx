import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography, Radio, RadioGroup, FormControlLabel, CircularProgress } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { Modal } from '@components1/ds/Modal';
import { Button as DsButton } from '@components1/ds/Button';
import CustomDateTimePicker from '@common-new/widgets/CustomDateTimePicker';
import BlockWithHeading from '@components1/runbooks/BlockWithHeading';
import { toast as snackbar } from '@components1/ds/Toast';
import { ds } from '@utils/colors';
import dayjs from 'dayjs';
import apiTriage, {
  CLASSIFICATION_OPTIONS,
  APPLY_SCOPE_OPTIONS,
  REASON_CODES,
  type ClassifyEventInput,
  type ClassifyPreviewResponse,
  type DuplicateSuggestion,
} from '@api1/triage';

interface EventClassifyModalProps {
  open: boolean;
  handleClose: () => void;
  event: {
    id: string;
    title: string;
    fingerprint?: string;
    accountId: string;
  };
  onSuccess?: () => void;
  defaultClassification?: string;
}

const EventClassifyModal: React.FC<EventClassifyModalProps> = ({ open, handleClose, event, onSuccess, defaultClassification }) => {
  // Form state
  const [classification, setClassification] = useState<string>('');
  const [reasonCode, setReasonCode] = useState<string>('');
  const [reasonText, setReasonText] = useState<string>('');
  const [applyScope, setApplyScope] = useState<string>('this_event');
  const [applyUntilDate, setApplyUntilDate] = useState<Date | null>(null);
  const [linkedEventId, setLinkedEventId] = useState<string>('');

  // Preview state
  const [preview, setPreview] = useState<ClassifyPreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);

  // Submit state
  const [submitting, setSubmitting] = useState<boolean>(false);

  // Duplicate suggestions state
  const [duplicates, setDuplicates] = useState<DuplicateSuggestion[]>([]);
  const [duplicatesLoading, setDuplicatesLoading] = useState<boolean>(false);

  // Reset form when modal opens/closes
  useEffect(() => {
    if (open) {
      setClassification(defaultClassification || '');
      setReasonCode('');
      setReasonText('');
      setApplyScope('this_event');
      setApplyUntilDate(null);
      setLinkedEventId('');
      setPreview(null);
      setDuplicates([]);
    }
  }, [open, defaultClassification]);

  // Fetch preview when classification or scope changes
  const fetchPreview = useCallback(async () => {
    if (!classification || !applyScope || !event.id) {
      return;
    }

    setPreviewLoading(true);
    try {
      const applyUntilHours = applyUntilDate ? Math.ceil((applyUntilDate.getTime() - Date.now()) / (1000 * 60 * 60)) : undefined;

      const result = await apiTriage.classifyPreview({
        event_id: event.id,
        classification,
        apply_scope: applyScope,
        apply_until_hours: applyScope === 'time_limited' ? applyUntilHours : undefined,
      });
      setPreview(result);
    } catch (error) {
      console.error('Failed to fetch preview:', error);
    } finally {
      setPreviewLoading(false);
    }
  }, [classification, applyScope, applyUntilDate, event.id]);

  useEffect(() => {
    if (open && classification && applyScope) {
      const debounceTimer = setTimeout(fetchPreview, 500);
      return () => clearTimeout(debounceTimer);
    }
  }, [open, classification, applyScope, applyUntilDate, fetchPreview]);

  // Fetch duplicate suggestions when classification is duplicate
  useEffect(() => {
    if (classification === 'duplicate' && event.id) {
      setDuplicatesLoading(true);
      apiTriage
        .getDuplicates(event.id)
        .then((result) => {
          setDuplicates(result?.suggestions || []);
        })
        .catch(console.error)
        .finally(() => setDuplicatesLoading(false));
    }
  }, [classification, event.id]);

  // Get reason code options based on classification
  const getReasonCodeOptions = () => {
    if (!classification) {
      return [];
    }
    const codes = REASON_CODES[classification as keyof typeof REASON_CODES] || [];
    return codes.map((code) => ({
      label: code.label,
      value: code.value,
    }));
  };

  const handleClassificationChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setClassification(e.target.value);
    setReasonCode(''); // Reset reason code when classification changes
    setLinkedEventId('');
  };

  const handleSubmit = async () => {
    if (!classification || !reasonCode || !applyScope) {
      snackbar.error('Please fill in all required fields');
      return;
    }

    if (classification === 'duplicate' && !linkedEventId) {
      snackbar.error('Please select the original event for duplicate classification');
      return;
    }

    setSubmitting(true);
    try {
      const applyUntilHours = applyUntilDate ? Math.ceil((applyUntilDate.getTime() - Date.now()) / (1000 * 60 * 60)) : undefined;

      const data: ClassifyEventInput = {
        event_id: event.id,
        classification: classification as ClassifyEventInput['classification'],
        reason_code: reasonCode,
        reason_text: reasonText || undefined,
        apply_scope: applyScope as ClassifyEventInput['apply_scope'],
        apply_until_hours: applyScope === 'time_limited' ? applyUntilHours : undefined,
        linked_event_id: classification === 'duplicate' ? linkedEventId : undefined,
        apply_to_existing: applyScope === 'this_fingerprint',
        confirmed: true,
      };

      const result = await apiTriage.classifyEvent(data);

      if (result?.success) {
        snackbar.success('Event classified successfully');
        handleClose();
        onSuccess?.();
      } else {
        snackbar.error('Failed to classify event');
      }
    } catch (error) {
      console.error('Failed to classify event:', error);
      snackbar.error('Failed to classify event');
    } finally {
      setSubmitting(false);
    }
  };

  const isFormValid = classification && reasonCode && applyScope;

  const renderDuplicateSelection = () => {
    if (duplicatesLoading) {
      return (
        <Box display='flex' justifyContent='center' p={ds.space[4]}>
          <CircularProgress size={24} />
        </Box>
      );
    }

    if (duplicates.length > 0) {
      return (
        <RadioGroup value={linkedEventId} onChange={(e) => setLinkedEventId(e.target.value)}>
          {duplicates.map((dup) => (
            <FormControlLabel
              key={dup.event_id}
              value={dup.event_id}
              control={<Radio size='small' />}
              label={
                <Box>
                  <Typography variant='body2'>{dup.title}</Typography>
                  <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                    {dup.is_first ? 'First occurrence' : `Occurrence #${dup.occurrence_number}`}
                    {' - '}
                    {new Date(dup.starts_at).toLocaleString()}
                  </Typography>
                </Box>
              }
              sx={{ mb: ds.space[2] }}
            />
          ))}
        </RadioGroup>
      );
    }

    return (
      <Typography variant='body2' sx={{ color: ds.gray[600] }}>
        No duplicate suggestions found. Enter the original event ID manually:
      </Typography>
    );
  };

  return (
    <Modal
      width='md'
      open={open}
      handleClose={handleClose}
      title={`Classify Event`}
      contentStyles={{ padding: '0px', overflowY: 'auto' }}
      maxHeight='85vh'
      loader={submitting}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
          <DsButton id='cancel' tone='secondary' size='md' onClick={handleClose} disabled={submitting}>
            Cancel
          </DsButton>
          <DsButton id='submit' tone='primary' size='md' onClick={handleSubmit} disabled={!isFormValid || submitting}>
            Classify
          </DsButton>
        </Box>
      }
    >
      <Box p={ds.space[5]} display='flex' flexDirection='column' gap={ds.space[5]}>
        {/* Event Info */}
        <Box p={ds.space[4]} sx={{ bgcolor: ds.blue[100], borderRadius: 'var(--ds-radius-lg)' }}>
          <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[2] }}>
            Event
          </Typography>
          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
            {event.title}
          </Typography>
        </Box>

        {/* Classification Type */}
        <BlockWithHeading number={1} heading='Classification Type' isExpandable={false}>
          <RadioGroup value={classification} onChange={handleClassificationChange}>
            {CLASSIFICATION_OPTIONS.map((option) => (
              <Box key={option.value} mb={ds.space[2]}>
                <FormControlLabel
                  value={option.value}
                  control={<Radio size='small' />}
                  label={
                    <Box>
                      <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                        {option.label}
                      </Typography>
                      <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                        {option.description}
                      </Typography>
                    </Box>
                  }
                  sx={{ alignItems: 'flex-start', mb: ds.space[2] }}
                />
              </Box>
            ))}
          </RadioGroup>
        </BlockWithHeading>

        {/* Reason Code */}
        {classification && (
          <BlockWithHeading number={2} heading='Reason' isExpandable={false}>
            <Box display='flex' flexDirection='column' gap={ds.space[4]}>
              <Box sx={{ width: ds.space.mul(0, 140) }}>
                <Select
                  id='reason-code'
                  label='Reason Code'
                  required
                  value={reasonCode}
                  options={getReasonCodeOptions()}
                  onChange={(next) => setReasonCode(next)}
                  placeholder='Select reason code'
                />
              </Box>
              <Input label='Additional Notes (Optional)' value={reasonText} onChange={setReasonText} type='textarea' rows={2} size='sm' />
            </Box>
          </BlockWithHeading>
        )}

        {/* Duplicate Selection */}
        {classification === 'duplicate' && (
          <BlockWithHeading number={3} heading='Select Original Event' isExpandable={false}>
            {renderDuplicateSelection()}
            {duplicates.length === 0 && (
              <Box sx={{ mt: ds.space[2], width: ds.space.mul(0, 140) }}>
                <Input label='Original Event ID' value={linkedEventId} onChange={setLinkedEventId} size='sm' />
              </Box>
            )}
          </BlockWithHeading>
        )}

        {/* Apply Scope - Only for classifications that support rules */}
        {classification && (classification === 'false_positive' || classification === 'duplicate') && (
          <BlockWithHeading number={classification === 'duplicate' ? 4 : 3} heading='Apply To' isExpandable={false}>
            <RadioGroup value={applyScope} onChange={(e) => setApplyScope(e.target.value)}>
              {APPLY_SCOPE_OPTIONS.map((option) => (
                <Box key={option.value} mb={ds.space[2]}>
                  <FormControlLabel
                    value={option.value}
                    control={<Radio size='small' />}
                    label={
                      <Box>
                        <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                          {option.label}
                        </Typography>
                        <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                          {option.description}
                        </Typography>
                      </Box>
                    }
                    sx={{ alignItems: 'flex-start', mb: ds.space[2] }}
                  />
                </Box>
              ))}
            </RadioGroup>

            {/* Time-limited date picker */}
            {applyScope === 'time_limited' && (
              <Box mt={ds.space[4]}>
                <CustomDateTimePicker
                  id='expires-at'
                  label='Expires At'
                  value={applyUntilDate ? dayjs(applyUntilDate) : null}
                  onChange={(newValue: import('dayjs').Dayjs | null) => setApplyUntilDate(newValue ? newValue.toDate() : null)}
                  minDate={dayjs()}
                  width={ds.space.mul(0, 140)}
                  maxDateTime={undefined}
                  componentsProps={undefined}
                />
              </Box>
            )}
          </BlockWithHeading>
        )}

        {/* Preview Section */}
        {preview && (
          <Box
            p={ds.space[4]}
            sx={{
              bgcolor: ds.blue[100],
              borderRadius: 'var(--ds-radius-lg)',
              border: `1px solid ${ds.blue[500]}`,
            }}
          >
            <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[4] }}>
              Impact Preview
            </Typography>

            {previewLoading ? (
              <Box display='flex' justifyContent='center' p={ds.space[4]}>
                <CircularProgress size={20} />
              </Box>
            ) : (
              <Box display='flex' flexDirection='column' gap={ds.space[2]}>
                <Box display='flex' justifyContent='space-between'>
                  <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                    This event:
                  </Typography>
                  <Typography variant='caption' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                    {preview.current_event.new_status}
                  </Typography>
                </Box>

                {preview.existing_events.count > 0 && preview.existing_events.will_be_updated && (
                  <Box display='flex' justifyContent='space-between'>
                    <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                      Existing similar events:
                    </Typography>
                    <Typography variant='caption' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                      {preview.existing_events.count} events will be updated
                    </Typography>
                  </Box>
                )}

                {preview.future_events.rule_applies && (
                  <Box display='flex' justifyContent='space-between'>
                    <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                      Future events:
                    </Typography>
                    <Typography variant='caption' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                      {preview.future_events.scope_description}
                    </Typography>
                  </Box>
                )}

                {preview.rule_to_create && (
                  <Box mt={ds.space[2]} pt={ds.space[2]} sx={{ borderTop: `1px solid ${ds.blue[500]}` }}>
                    <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                      A {preview.rule_to_create.rule_type} rule will be created
                      {preview.rule_to_create.expires_at ? ` (expires ${new Date(preview.rule_to_create.expires_at).toLocaleDateString()})` : ''}
                    </Typography>
                  </Box>
                )}
              </Box>
            )}
          </Box>
        )}
      </Box>
    </Modal>
  );
};

export default EventClassifyModal;
