import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { CodeEditor } from '@ui/CodeEditor';
import CopyButton from '@shared/buttons/CopyButton';
import { ds } from 'src/utils/colors';
import apiWorkflow from '@api1/workflow';
import type { CheckTriggerMatchResponse } from '@api1/workflow/types';
import {
  buildTriggerMock,
  flattenPayloadFields,
  EMITTED_LIFECYCLE_PHASES,
  DEFAULT_LIFECYCLE_PHASE,
  type EventMockOverrides,
  type OptimizationMockOverrides,
} from '../utils/triggerPayloadMock';

interface TriggerSimulatorPanelProps {
  /** 'event' or 'optimization' — other trigger types render nothing. */
  triggerType?: string;
  /** Lifecycle phase this trigger fires at (params.on). Determines which fields the payload carries. */
  lifecyclePhase?: string;
  /** Filter values from the trigger config, folded into the mock so it resembles what the user is matching on. */
  eventOverrides?: EventMockOverrides;
  optimizationOverrides?: OptimizationMockOverrides;
  /** Runs the automation against the edited payload. Omitted when no run surface is available. */
  onSimulateRun?: (inputs: Record<string, any>) => void;
  /** Account the match check runs under. Absent = check skipped, payload only. */
  accountId?: string;
  /**
   * Trigger params exactly as they would be saved. Checked against the payload with
   * the same evaluator the live registry uses, so the verdict matches what fires.
   */
  triggerParams?: Record<string, any>;
}

// Long enough to not re-check on every keystroke, short enough to feel attached
// to the edit that caused it.
const MATCH_CHECK_DEBOUNCE_MS = 500;

const capitalize = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

const panelBoxSx = {
  mt: 2,
  p: 2,
  backgroundColor: ds.blue[100],
  borderRadius: ds.radius.sm,
  border: `1px solid ${ds.gray[200]}`,
};

/**
 * Shows the payload a trigger delivers, lets the user edit it, says whether it would
 * fire, and dry-runs against it. The payload is a browser-built mock (no tenant data);
 * the verdict is not — the backend uses the live registry's evaluator.
 */
const TriggerSimulatorPanel: React.FC<TriggerSimulatorPanelProps> = ({
  triggerType,
  lifecyclePhase = DEFAULT_LIFECYCLE_PHASE,
  eventOverrides,
  optimizationOverrides,
  onSimulateRun,
  accountId,
  triggerParams,
}) => {
  // Serialize the override objects so the mock is rebuilt when their values
  // change, not on every parent render (the mock stamps fresh timestamps).
  const eventOverrideKey = JSON.stringify(eventOverrides || {});
  const optimizationOverrideKey = JSON.stringify(optimizationOverrides || {});

  const mockPayload = useMemo(
    () => buildTriggerMock(triggerType, lifecyclePhase, JSON.parse(eventOverrideKey), JSON.parse(optimizationOverrideKey)),
    [triggerType, lifecyclePhase, eventOverrideKey, optimizationOverrideKey]
  );

  const [payloadJson, setPayloadJson] = useState('');
  // Once the user edits the payload we stop re-seeding it from the config, so a
  // dropdown change elsewhere in the panel can't discard what they typed.
  const [isEdited, setIsEdited] = useState(false);

  useEffect(() => {
    if (isEdited || !mockPayload) return;
    setPayloadJson(JSON.stringify(mockPayload, null, 2));
  }, [mockPayload, isEdited]);

  // The edited text is the only source of truth: the parsed value and the error
  // are both derived from it in one pass, so the payload is parsed once per
  // keystroke rather than once to validate and again to use.
  const { parsedPayload, jsonError } = useMemo((): { parsedPayload: Record<string, any> | null; jsonError: string } => {
    // Before the effect seeds it the field is legitimately empty — don't flash an
    // error at a user who hasn't typed yet.
    if (!payloadJson.trim()) return { parsedPayload: null, jsonError: isEdited ? 'Payload is required' : '' };
    try {
      const parsed = JSON.parse(payloadJson);
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        return { parsedPayload: null, jsonError: 'Payload must be a JSON object' };
      }
      return { parsedPayload: parsed as Record<string, any>, jsonError: '' };
    } catch {
      return { parsedPayload: null, jsonError: 'Invalid JSON' };
    }
  }, [payloadJson, isEdited]);

  const fields = useMemo(() => flattenPayloadFields(parsedPayload || mockPayload || {}), [parsedPayload, mockPayload]);

  const phaseInfo = EMITTED_LIFECYCLE_PHASES.find((p) => p.value === lifecyclePhase);

  const [matchResult, setMatchResult] = useState<CheckTriggerMatchResponse | null>(null);
  const [isChecking, setIsChecking] = useState(false);

  // Serialized so the check re-runs when a filter value changes, not on every
  // parent render.
  const triggerParamsKey = JSON.stringify(triggerParams || {});
  const canCheckMatch = !!accountId && !!triggerType && !!parsedPayload;

  useEffect(() => {
    if (!canCheckMatch) {
      setMatchResult(null);
      return;
    }

    let cancelled = false;
    setIsChecking(true);
    const timer = setTimeout(async () => {
      try {
        const response: any = await apiWorkflow.checkTriggerMatch({
          account_id: accountId as string,
          trigger_type: triggerType as string,
          params: JSON.parse(triggerParamsKey),
          payload: parsedPayload as Record<string, any>,
        });
        if (cancelled) return;
        setMatchResult(response?.data?.workflow_check_trigger_match || null);
      } catch {
        // A failed check must not block the payload editor — the panel simply
        // falls back to showing no verdict.
        if (!cancelled) setMatchResult(null);
      } finally {
        if (!cancelled) setIsChecking(false);
      }
    }, MATCH_CHECK_DEBOUNCE_MS);

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // parsedPayload is derived from payloadJson, which is what actually changes.
  }, [canCheckMatch, accountId, triggerType, triggerParamsKey, payloadJson]);

  if (!mockPayload) return null;

  const handlePayloadChange = (value: string) => {
    setPayloadJson(value);
    setIsEdited(true);
  };

  const handleReset = () => {
    setPayloadJson(JSON.stringify(mockPayload, null, 2));
    setIsEdited(false);
  };

  const handleSimulate = () => {
    if (!parsedPayload || jsonError || !onSimulateRun) return;
    onSimulateRun({ event: parsedPayload });
  };

  // The verdict answers the question the trigger config can't: would this event
  // actually reach the automation? A filter that evaluates to false and a filter
  // that can't be evaluated at all are different answers, so they read differently.
  const verdictStyle = (() => {
    if (isChecking || !matchResult) {
      return {
        message: 'Checking whether this payload matches the trigger…',
        text: ds.gray[400],
        border: ds.gray[200],
        background: ds.background[100],
      };
    }
    if (matchResult.error) {
      return {
        message: `This trigger's filter could not be evaluated: ${matchResult.error}`,
        text: ds.amber[700],
        border: ds.amber[300],
        background: ds.amber[100],
      };
    }
    if (matchResult.matched) {
      return {
        message: `This payload fires the automation.${matchResult.reason ? ` ${capitalize(matchResult.reason)}.` : ''}`,
        text: ds.green[700],
        border: ds.green[300],
        background: ds.green[100],
      };
    }
    return {
      message: `This payload does not fire the automation — ${matchResult.reason || 'it does not match the trigger'}.`,
      text: ds.red[700],
      border: ds.red[300],
      background: ds.red[100],
    };
  })();

  return (
    <Box data-testid='trigger-simulator-panel' sx={panelBoxSx}>
      <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700], mb: 1 }}>
        Trigger simulator
      </Typography>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], lineHeight: 1.5, mb: 1 }}>
        A sample of the data this trigger delivers, in the shape your tasks receive it. Edit it to whatever you want to test — the panel checks it
        against this trigger's filter as you type, then runs the automation against it. The sample itself is example data; nothing here is read from
        your account.
      </Typography>

      {triggerType === 'event' && phaseInfo && (
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], lineHeight: 1.5, mb: 1 }}>
          Fires when: <strong style={{ color: ds.gray[700] }}>{phaseInfo.label}</strong> — {phaseInfo.note}
        </Typography>
      )}

      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[400], lineHeight: 1.5, mb: 1 }}>
        Available to tasks as <strong>Inputs.event.*</strong> and to filter expressions as <strong>event.*</strong>.
      </Typography>

      <CodeEditor
        data-testid='trigger-simulator-payload-input'
        ariaLabel='Trigger payload'
        language='json'
        value={payloadJson}
        onChange={handlePayloadChange}
        height='280px'
        error={jsonError}
      />

      {canCheckMatch && (isChecking || matchResult) && (
        <Box
          data-testid='trigger-simulator-match-verdict'
          sx={{
            mt: 1,
            p: 1,
            borderRadius: 'var(--ds-radius-sm)',
            border: `1px solid ${verdictStyle.border}`,
            backgroundColor: verdictStyle.background,
          }}
        >
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: verdictStyle.text, lineHeight: 1.5 }}>{verdictStyle.message}</Typography>
          {!!matchResult?.filter && !isChecking && (
            <Typography sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-caption)', color: ds.gray[400], mt: 0.5, wordBreak: 'break-all' }}>
              {matchResult.filter}
            </Typography>
          )}
        </Box>
      )}

      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center', mt: 1, mb: 1 }}>
        {onSimulateRun && (
          <Button data-testid='trigger-simulator-run-btn' tone='primary' size='sm' disabled={!!jsonError || !parsedPayload} onClick={handleSimulate}>
            Simulate run
          </Button>
        )}
        <Button data-testid='trigger-simulator-reset-btn' tone='secondary' size='sm' disabled={!isEdited} onClick={handleReset}>
          Reset to sample
        </Button>
        <CopyButton text={payloadJson} size='sm' />
      </Box>

      {onSimulateRun && (
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[400], lineHeight: 1.5, mb: 1 }}>
          Simulate run executes the automation as a dry run — tasks report what they would do instead of doing it. It runs the tasks directly, so it
          works even when the payload does not match the trigger.
        </Typography>
      )}

      <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700], mt: 2, mb: 0.5 }}>
        Fields ({fields.length})
      </Typography>
      <Box
        data-testid='trigger-simulator-field-list'
        sx={{
          maxHeight: '220px',
          overflowY: 'auto',
          border: `1px solid ${ds.brand[200]}`,
          borderRadius: 'var(--ds-radius-sm)',
          backgroundColor: ds.background[100],
        }}
      >
        {fields.map((field) => (
          <Box
            key={field.path}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-2)',
              padding: 'var(--ds-space-1) var(--ds-space-2)',
              borderBottom: `1px solid ${ds.gray[200]}`,
              '&:last-of-type': { borderBottom: 'none' },
            }}
          >
            <CopyButton text={`{{ Inputs.event.${field.path} }}`} size='xs' />
            <Typography sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-caption)', color: ds.gray[700], flexShrink: 0 }}>
              event.{field.path}
            </Typography>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[400], flexShrink: 0 }}>{field.type}</Typography>
            <Typography
              title={field.example}
              sx={{
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-caption)',
                color: ds.gray[400],
                flex: 1,
                minWidth: 0,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {field.example}
            </Typography>
          </Box>
        ))}
      </Box>
    </Box>
  );
};

export default TriggerSimulatorPanel;
