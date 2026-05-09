import React, { useEffect, useState, useCallback } from 'react';
import { Box, Typography, TextField, Radio, RadioGroup, FormControlLabel, Tabs, Tab, CircularProgress } from '@mui/material';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import CustomDropdown from '@components1/common/CustomDropdown';
import CustomSwitch from '@components1/common/CustomSwitch';
import BlockWithHeading from '@components1/runbooks/BlockWithHeading';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import dayjs from 'dayjs';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import apiTriage, { type TriageRule, type CreateTriageRuleInput, type UpdateTriageRuleInput, type RulePreviewResponse } from '@api1/triage';

interface TriageRuleModalProps {
  open: boolean;
  handleClose: () => void;
  accountId?: string;
  rule?: TriageRule | null;
  isCreate: boolean;
  onSuccess?: () => void;
}

const RULE_TYPES = [
  { value: 'suppression', label: 'Suppression', description: 'Suppress or drop matching events' },
  { value: 'scoring', label: 'Scoring', description: 'Adjust the priority score of matching events' },
  { value: 'classification', label: 'Classification', description: 'Automatically classify matching events' },
];

const SUPPRESSION_ACTIONS = [
  { value: 'suppress', label: 'Suppress', description: 'Mark as suppressed but keep visible' },
  { value: 'drop', label: 'Drop', description: 'Completely hide from view' },
];

const CLASSIFICATION_ACTIONS = [
  { value: 'auto_classify_fp', label: 'False Positive', description: 'Auto-classify as false positive' },
  { value: 'auto_classify_duplicate', label: 'Duplicate', description: 'Auto-classify as duplicate' },
];

// Convert "key:value,key2:value2" to JSON string {"key":"value","key2":"value2"}
const labelsToJson = (labels: string): string => {
  if (!labels.trim()) return '';
  const obj: Record<string, string> = {};
  labels.split(',').forEach((pair) => {
    const idx = pair.indexOf(':');
    if (idx > 0) {
      const key = pair.substring(0, idx).trim();
      const value = pair.substring(idx + 1).trim();
      if (key) obj[key] = value;
    }
  });
  return Object.keys(obj).length > 0 ? JSON.stringify(obj) : '';
};

// Convert JSON string {"key":"value"} to "key:value,key2:value2"
const jsonToLabels = (json: string): string => {
  if (!json) return '';
  try {
    const obj = JSON.parse(json);
    if (typeof obj === 'object' && obj !== null) {
      return Object.entries(obj)
        .map(([k, v]) => `${k}:${v}`)
        .join(',');
    }
  } catch {
    // If it's already in key:value format (not JSON), return as-is
    return json;
  }
  return json;
};

const TriageRuleModal: React.FC<TriageRuleModalProps> = ({ open, handleClose, accountId, rule, isCreate, onSuccess }) => {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [priority, setPriority] = useState(100);
  const [ruleType, setRuleType] = useState<string>('suppression');
  const [matchFingerprint, setMatchFingerprint] = useState('');
  const [matchAlertname, setMatchAlertname] = useState('');
  const [matchNamespace, setMatchNamespace] = useState('');
  const [matchService, setMatchService] = useState('');
  const [matchSource, setMatchSource] = useState('');
  const [matchPriority, setMatchPriority] = useState('');
  const [matchLabels, setMatchLabels] = useState('');
  const [matchFindingType, setMatchFindingType] = useState('');
  const [action, setAction] = useState('suppress');
  const [scoreAdjustment, setScoreAdjustment] = useState(0);
  const [effectiveUntil, setEffectiveUntil] = useState<Date | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Apply to existing state
  const [applyToExisting, setApplyToExisting] = useState(false);
  const [preview, setPreview] = useState<RulePreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  useEffect(() => {
    if (open) {
      if (rule && !isCreate) {
        setName(rule.name || '');
        setDescription(rule.description || '');
        setPriority(rule.priority || 100);
        setRuleType(rule.rule_type || 'suppression');
        setMatchFingerprint(rule.match_fingerprint || '');
        setMatchAlertname(rule.match_alertname || '');
        setMatchNamespace(rule.match_namespace || '');
        setMatchService(rule.match_service || '');
        setMatchSource(rule.match_source || '');
        setMatchPriority(rule.match_priority || '');
        setMatchLabels(jsonToLabels(rule.match_labels || ''));
        setMatchFindingType(rule.match_finding_type || '');
        setAction(rule.action || 'suppress');
        if (rule.action_value) {
          try {
            const actionValue = JSON.parse(rule.action_value);
            setScoreAdjustment(actionValue.adjustment || 0);
          } catch {
            setScoreAdjustment(0);
          }
        }
        setEffectiveUntil(rule.effective_until ? new Date(rule.effective_until) : null);
      } else {
        setName('');
        setDescription('');
        setPriority(100);
        setRuleType('suppression');
        setMatchFingerprint('');
        setMatchAlertname('');
        setMatchNamespace('');
        setMatchService('');
        setMatchSource('');
        setMatchPriority('');
        setMatchLabels('');
        setMatchFindingType('');
        setAction('suppress');
        setScoreAdjustment(0);
        setEffectiveUntil(null);
      }
      setApplyToExisting(rule?.apply_to_existing ?? false);
      setPreview(null);
    }
  }, [open, rule, isCreate]);

  // Fetch preview when applyToExisting is enabled and match criteria change
  const fetchPreview = useCallback(async () => {
    const hasMatchCriteria =
      matchFingerprint || matchAlertname || matchNamespace || matchService || matchSource || matchPriority || matchLabels || matchFindingType;
    if (!accountId || !applyToExisting || !hasMatchCriteria || !ruleType || !action) {
      setPreview(null);
      return;
    }

    setPreviewLoading(true);
    try {
      const result = await apiTriage.previewTriageRule({
        cloud_account_id: accountId,
        rule_type: ruleType,
        action,
        match_fingerprint: matchFingerprint || undefined,
        match_alertname: matchAlertname || undefined,
        match_namespace: matchNamespace || undefined,
        match_service: matchService || undefined,
        match_source: matchSource || undefined,
        match_priority: matchPriority || undefined,
        match_labels: labelsToJson(matchLabels) || undefined,
        match_finding_type: matchFindingType || undefined,
      });
      setPreview(result);
    } catch (error) {
      console.error('Failed to fetch preview:', error);
      setPreview(null);
    } finally {
      setPreviewLoading(false);
    }
  }, [
    accountId,
    ruleType,
    action,
    matchFingerprint,
    matchAlertname,
    matchNamespace,
    matchService,
    matchSource,
    matchPriority,
    matchLabels,
    matchFindingType,
    applyToExisting,
  ]);

  useEffect(() => {
    if (open && applyToExisting) {
      const debounceTimer = setTimeout(fetchPreview, 500);
      return () => clearTimeout(debounceTimer);
    }
  }, [open, applyToExisting, fetchPreview]);

  useEffect(() => {
    if (ruleType === 'suppression') {
      setAction('suppress');
    } else if (ruleType === 'scoring') {
      setAction('adjust_score');
    } else if (ruleType === 'classification') {
      setAction('auto_classify_fp');
    }
  }, [ruleType]);

  const handleSubmit = async () => {
    if (!accountId) {
      snackbar.error('Account ID is required to save rules');
      return;
    }
    if (!ruleType) {
      snackbar.error('Please select a rule type');
      return;
    }
    const hasMatchCriteria =
      matchFingerprint || matchAlertname || matchNamespace || matchService || matchSource || matchPriority || matchLabels || matchFindingType;
    if (!hasMatchCriteria) {
      snackbar.error('Please specify at least one match criterion');
      return;
    }

    setSubmitting(true);
    try {
      let actionValue: string | undefined;
      if (ruleType === 'scoring') {
        actionValue = JSON.stringify({ adjustment: scoreAdjustment });
      }

      const data: CreateTriageRuleInput = {
        cloud_account_id: accountId,
        rule_type: ruleType as CreateTriageRuleInput['rule_type'],
        action,
        match_fingerprint: matchFingerprint || undefined,
        match_alertname: matchAlertname || undefined,
        match_namespace: matchNamespace || undefined,
        match_service: matchService || undefined,
        match_source: matchSource || undefined,
        match_priority: matchPriority || undefined,
        match_labels: labelsToJson(matchLabels) || undefined,
        match_finding_type: matchFindingType || undefined,
        action_value: actionValue,
        priority,
        effective_until: effectiveUntil?.toISOString(),
        name: name || undefined,
        description: description || undefined,
        apply_to_existing: applyToExisting,
      };

      let result;
      if (isCreate) {
        result = await apiTriage.createTriageRule(data);
      } else {
        // Update existing rule
        const updateData: UpdateTriageRuleInput = {
          ...data,
          rule_id: rule!.id,
        };
        result = await apiTriage.updateTriageRule(updateData);
      }

      if (result?.success) {
        let successMsg = isCreate ? 'Rule created successfully' : 'Rule updated successfully';
        if (result.bulk_operation?.events_to_update) {
          successMsg += `. Applied to ${result.bulk_operation.events_to_update} existing events.`;
        }
        snackbar.success(successMsg);
        handleClose();
        onSuccess?.();
      } else {
        snackbar.error(result?.error || 'Failed to save rule');
      }
    } catch (error) {
      console.error('Failed to save rule:', error);
      snackbar.error('Failed to save rule');
    } finally {
      setSubmitting(false);
    }
  };

  const renderActionConfig = () => {
    if (ruleType === 'suppression') {
      return (
        <RadioGroup value={action} onChange={(e) => setAction(e.target.value)}>
          {SUPPRESSION_ACTIONS.map((opt) => (
            <FormControlLabel
              key={opt.value}
              value={opt.value}
              control={<Radio size='small' />}
              label={
                <Box>
                  <Typography variant='body2' sx={{ fontWeight: 500 }}>
                    {opt.label}
                  </Typography>
                  <Typography variant='caption' sx={{ color: colors.text.tertiary }}>
                    {opt.description}
                  </Typography>
                </Box>
              }
              sx={{ alignItems: 'flex-start', mb: 1 }}
            />
          ))}
        </RadioGroup>
      );
    } else if (ruleType === 'scoring') {
      return (
        <Box>
          <Typography variant='body2' sx={{ mb: 2 }}>
            Adjust the event priority score:
          </Typography>
          <TextField
            type='number'
            label='Score Adjustment'
            value={scoreAdjustment}
            onChange={(e) => setScoreAdjustment(parseInt(e.target.value) || 0)}
            size='small'
            fullWidth
            helperText='Positive = increase priority, Negative = decrease'
          />
        </Box>
      );
    } else if (ruleType === 'classification') {
      return (
        <RadioGroup value={action} onChange={(e) => setAction(e.target.value)}>
          {CLASSIFICATION_ACTIONS.map((opt) => (
            <FormControlLabel
              key={opt.value}
              value={opt.value}
              control={<Radio size='small' />}
              label={
                <Box>
                  <Typography variant='body2' sx={{ fontWeight: 500 }}>
                    {opt.label}
                  </Typography>
                  <Typography variant='caption' sx={{ color: colors.text.tertiary }}>
                    {opt.description}
                  </Typography>
                </Box>
              }
              sx={{ alignItems: 'flex-start', mb: 1 }}
            />
          ))}
        </RadioGroup>
      );
    }
    return null;
  };

  const renderPreviewContent = () => {
    if (previewLoading) {
      return (
        <Box display='flex' alignItems='center' gap={2}>
          <CircularProgress size={20} />
          <Typography variant='body2' sx={{ color: colors.text.tertiary }}>
            Checking matching events...
          </Typography>
        </Box>
      );
    }

    if (preview) {
      return (
        <Box>
          <Typography variant='body2' sx={{ fontWeight: 600, mb: 1 }}>
            {preview.matching_events_count} event{preview.matching_events_count !== 1 ? 's' : ''} will be affected
          </Typography>
          {preview.matching_events_count > 0 && (
            <>
              <Typography variant='caption' sx={{ color: colors.text.tertiary, display: 'block', mb: 1 }}>
                {preview.new_status === 'NO_CHANGE' ? (
                  'Priority score will be adjusted (no status change)'
                ) : (
                  <>
                    Status will change to: <strong>{preview.new_status}</strong>
                  </>
                )}
              </Typography>
              {preview.sample_events.length > 0 && (
                <Box mt={1}>
                  <Typography variant='caption' sx={{ color: colors.text.tertiary, fontWeight: 500 }}>
                    Sample matching events:
                  </Typography>
                  {preview.sample_events.slice(0, 3).map((event) => (
                    <Typography key={event.id} variant='caption' sx={{ display: 'block', color: colors.text.secondary, ml: 1 }}>
                      • {event.title}
                      {event.namespace && ` (${event.namespace})`}
                    </Typography>
                  ))}
                  {preview.sample_events.length > 3 && (
                    <Typography variant='caption' sx={{ color: colors.text.tertiary, ml: 1 }}>
                      ... and {preview.matching_events_count - 3} more
                    </Typography>
                  )}
                </Box>
              )}
            </>
          )}
        </Box>
      );
    }

    return (
      <Typography variant='body2' sx={{ color: colors.text.tertiary }}>
        Define match criteria above to see how many events will be affected
      </Typography>
    );
  };

  return (
    <Modal
      width='md'
      open={open}
      handleClose={handleClose}
      title={isCreate ? 'Create Triage Rule' : 'Edit Triage Rule'}
      contentStyles={{ padding: '0px' }}
      loader={submitting}
    >
      <Box p={3} sx={{ maxHeight: '70vh', overflowY: 'auto' }}>
        <BlockWithHeading number={1} heading='Basic Information' isExpandable={false}>
          <Box display='flex' flexDirection='column' gap={2}>
            <TextField
              label='Rule Name'
              value={name}
              onChange={(e) => setName(e.target.value)}
              size='small'
              fullWidth
              placeholder='e.g., Suppress maintenance alerts'
            />
            <TextField
              label='Description'
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              multiline
              rows={2}
              size='small'
              fullWidth
            />
            <TextField
              type='number'
              label='Priority'
              value={priority}
              onChange={(e) => setPriority(parseInt(e.target.value) || 100)}
              size='small'
              fullWidth
              helperText='Lower numbers are evaluated first'
            />
          </Box>
        </BlockWithHeading>

        <BlockWithHeading number={2} heading='Rule Type' isExpandable={false}>
          <Tabs
            value={ruleType}
            onChange={(_, v) => setRuleType(v)}
            sx={{ mb: 2, '& .MuiTab-root': { textTransform: 'none', minWidth: 'auto', px: 2 } }}
          >
            {RULE_TYPES.map((t) => (
              <Tab key={t.value} value={t.value} label={t.label} />
            ))}
          </Tabs>
          <Typography variant='body2' sx={{ color: colors.text.tertiary }}>
            {RULE_TYPES.find((t) => t.value === ruleType)?.description}
          </Typography>
        </BlockWithHeading>

        <BlockWithHeading number={3} heading='Match Criteria' isExpandable={true} defaultStateOfExpand={true}>
          <Typography variant='body2' sx={{ color: colors.text.tertiary, mb: 2 }}>
            Define which events this rule applies to. At least one criterion is required.
          </Typography>
          <Box display='flex' flexDirection='column' gap={2}>
            <TextField
              label='Fingerprint (exact match)'
              value={matchFingerprint}
              onChange={(e) => setMatchFingerprint(e.target.value)}
              size='small'
              fullWidth
            />
            <TextField
              label='Alert Name (regex)'
              value={matchAlertname}
              onChange={(e) => setMatchAlertname(e.target.value)}
              size='small'
              fullWidth
              placeholder='e.g., KubePodCrashLooping'
            />
            <TextField
              label='Namespace (regex)'
              value={matchNamespace}
              onChange={(e) => setMatchNamespace(e.target.value)}
              size='small'
              fullWidth
              placeholder='e.g., kube-system|monitoring'
            />
            <TextField label='Service (regex)' value={matchService} onChange={(e) => setMatchService(e.target.value)} size='small' fullWidth />
            <CustomDropdown
              label='Source'
              value={matchSource}
              options={[
                { label: 'Any', value: '' },
                { label: 'Kubernetes', value: 'kubernetes' },
                { label: 'Prometheus', value: 'prometheus' },
                { label: 'PagerDuty', value: 'pagerduty' },
                { label: 'Datadog', value: 'datadog' },
              ]}
              onChange={(e: any) => setMatchSource(e.target.value)}
            />
            <CustomDropdown
              label='Priority'
              value={matchPriority}
              options={[
                { label: 'Any', value: '' },
                { label: 'Critical', value: 'critical' },
                { label: 'High', value: 'high' },
                { label: 'Medium', value: 'medium' },
                { label: 'Low', value: 'low' },
              ]}
              onChange={(e: any) => setMatchPriority(e.target.value)}
            />
            <TextField
              label='Labels (key:value, comma-separated)'
              value={matchLabels}
              onChange={(e) => setMatchLabels(e.target.value)}
              size='small'
              fullWidth
              placeholder='env:production,team:platform'
            />
            <TextField label='Finding Type' value={matchFindingType} onChange={(e) => setMatchFindingType(e.target.value)} size='small' fullWidth />
          </Box>
        </BlockWithHeading>

        <BlockWithHeading number={4} heading='Action' isExpandable={false}>
          {renderActionConfig()}
        </BlockWithHeading>

        <BlockWithHeading number={5} heading='Apply to Existing Events' isExpandable={false}>
          <Box display='flex' alignItems='center' gap={2} mb={2}>
            <CustomSwitch checked={applyToExisting} onChange={() => setApplyToExisting(!applyToExisting)} />
            <Box>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                Apply this rule to existing events
              </Typography>
              <Typography variant='caption' sx={{ color: colors.text.tertiary }}>
                Update matching events that are currently open
              </Typography>
            </Box>
          </Box>

          {applyToExisting && (
            <Box
              p={2}
              sx={{
                bgcolor: colors.background.primaryLightest,
                borderRadius: '8px',
                border: `1px solid ${colors.border.primary}`,
              }}
            >
              {renderPreviewContent()}
            </Box>
          )}
        </BlockWithHeading>

        <BlockWithHeading number={6} heading='Expiration (Optional)' isExpandable={true} defaultStateOfExpand={false}>
          <Typography variant='body2' sx={{ color: colors.text.tertiary, mb: 2 }}>
            Set an expiration date for temporary rules.
          </Typography>
          <LocalizationProvider dateAdapter={AdapterDayjs}>
            <DateTimePicker
              label='Expires At'
              value={effectiveUntil}
              onChange={(newValue: import('dayjs').Dayjs | null) => setEffectiveUntil(newValue ? newValue.toDate() : null)}
              minDate={dayjs()}
              renderInput={(params: any) => <TextField {...params} size='small' fullWidth />}
            />
          </LocalizationProvider>
        </BlockWithHeading>
      </Box>

      <Box
        display='flex'
        alignItems='center'
        justifyContent='flex-end'
        gap='12px'
        p='16px 24px'
        sx={{ borderTop: `0.5px solid ${colors.border.vertical}`, button: { minWidth: '140px' } }}
      >
        <CustomButton type='button' id='cancel' text='Cancel' size='Medium' variant='secondary' onClick={handleClose} disabled={submitting} />
        <CustomButton
          type='button'
          id='submit'
          text={isCreate ? 'Create Rule' : 'Save Changes'}
          size='Medium'
          onClick={handleSubmit}
          disabled={submitting}
        />
      </Box>
    </Modal>
  );
};

export default TriageRuleModal;
