import React, { useMemo } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { ContentCopy, Schedule, Input as InputIcon } from '@mui/icons-material';
import { ds } from 'src/utils/colors';
import { workflowUserIcon, workflowCalendarIcon, workflowWebhookIcon, InvestigateHomeIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { Label } from '@ui/Label';
import Datetime from '@shared/format/Datetime';
import JsonTreeView from '@shared/viewers/JsonTreeView';
import type { Node } from 'reactflow';

interface TriggerDetailsPanelProps {
  triggerNode: Node;
  executionData: any;
  selectedExecution: {
    status: string;
    start_time?: string;
    close_time?: string;
    triggered_by?: string;
  } | null;
  getDuration: (startTime: string, endTime?: string) => string;
  copyToClipboard: (text: string, label: string) => void;
  // Page account id — fallback for the Investigate link when the event payload omits cloud_account_id.
  accountId?: string;
}

// Trigger type display config — shared amber palette with TriggerNode (badge + icon-container).
// Alpha tints (gradient/border) are applied at use-sites via color-mix() since DS tokens are fully opaque.
const TRIGGER_COLOR = 'var(--ds-amber-500)';
const TRIGGER_BG = 'var(--ds-amber-100)';

const TRIGGER_META: Record<string, { label: string; icon: any; iconIsImage?: boolean }> = {
  manual: { label: 'Manual', icon: workflowUserIcon, iconIsImage: true },
  schedule: { label: 'Schedule', icon: workflowCalendarIcon, iconIsImage: true },
  webhook: { label: 'Webhook', icon: workflowWebhookIcon, iconIsImage: true },
  event: { label: 'Event', icon: null },
};

// Structured event filter fields (matches TriggerConfigSidebar.tsx)
const EVENT_STRUCTURED_FIELDS = [
  { key: 'event_type', eventField: 'event_type', label: 'Event Type' },
  { key: 'cluster', eventField: 'cluster', label: 'Cluster' },
  { key: 'namespace', eventField: 'subject_namespace', label: 'Namespace' },
  { key: 'source', eventField: 'source', label: 'Source' },
  { key: 'priority', eventField: 'priority', label: 'Priority' },
] as const;

// Legacy params.event_type may be a string or array; collapse to a comma list for display.
const formatLegacyEventType = (raw: unknown): string => {
  if (Array.isArray(raw)) {
    const items = raw.filter((v: unknown): v is string => typeof v === 'string' && v !== '');
    return items.join(', ');
  }
  if (typeof raw === 'string') {
    return raw;
  }
  return '';
};

// Mono style for code-like values in detail rows
const MONO_STYLE = {
  fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
  backgroundColor: ds.background[200],
  border: `1px solid ${ds.gray[200]}`,
  borderRadius: 'var(--ds-radius-md)',
  padding: 'var(--ds-space-2) var(--ds-space-3)',
};

// Reusable detail row for label + value pairs
const DetailRow = ({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) => {
  if (!value) {
    return null;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Typography
        sx={{
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: ds.gray[600],
          textTransform: 'uppercase',
          letterSpacing: '0.04em',
        }}
      >
        {label}
      </Typography>
      {typeof value === 'string' ? (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: ds.gray[700],
            wordBreak: 'break-word',
            ...(mono ? MONO_STYLE : {}),
          }}
        >
          {value}
        </Typography>
      ) : (
        value
      )}
    </Box>
  );
};

// Schedule trigger config
const ScheduleConfig = ({ params }: { params: any }) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
    <DetailRow label='Cron Expression' value={params.cron} mono />
    <DetailRow label='Overlap Policy' value={params.overlap_policy || 'Skip'} />
    <DetailRow label='Catchup Window' value={params.catchup_window || '60s'} />
  </Box>
);

// Webhook trigger config
const WebhookConfig = ({ params }: { params: any }) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
    <DetailRow label='Integration Name' value={params.integration_name} mono />
    <DetailRow label='Filter Expression' value={params.filter} mono />
  </Box>
);

// Event trigger config — parses structured filters from expression
const EventConfig = ({ params }: { params: any }) => {
  const filterStr = params.filter || '';
  const parsed: Record<string, string> = {};
  for (const f of EVENT_STRUCTURED_FIELDS) {
    const regex = new RegExp(`event\\.${f.eventField}\\s*==\\s*"([^"]*)"`, 'i');
    const match = regex.exec(filterStr);
    if (match) {
      parsed[f.key] = match[1];
    }
  }
  // Legacy: workflows saved before event_type became a structured filter still carry params.event_type.
  // Surface it under the same key so the UI looks consistent.
  if (!parsed.event_type) {
    const legacy = formatLegacyEventType(params.event_type);
    if (legacy) {
      parsed.event_type = legacy;
    }
  }
  const hasStructured = Object.keys(parsed).length > 0;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      {hasStructured ? (
        <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 'var(--ds-space-3)' }}>
          {EVENT_STRUCTURED_FIELDS.map(
            (f) =>
              parsed[f.key] && (
                <Box key={f.key} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: ds.gray[600],
                      textTransform: 'uppercase',
                      letterSpacing: '0.04em',
                    }}
                  >
                    {f.label}
                  </Typography>
                  <Box
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      color: ds.gray[700],
                      backgroundColor: ds.background[200],
                      border: `1px solid ${ds.gray[200]}`,
                      borderRadius: 'var(--ds-radius-md)',
                      padding: 'var(--ds-space-1) var(--ds-space-2)',
                    }}
                  >
                    {parsed[f.key]}
                  </Box>
                </Box>
              )
          )}
        </Box>
      ) : (
        <DetailRow label='Filter Expression' value={filterStr} mono />
      )}
    </Box>
  );
};

const EMPTY_STATE_MESSAGES: Record<string, string> = {
  manual: 'This manual execution did not include any input parameters.',
  webhook: 'No webhook payload was captured for this execution.',
  event: 'No event data was captured for this execution.',
  schedule: 'Schedule trigger has no additional configuration.',
};

const INPUT_SECTION_LABELS: Record<string, string> = {
  manual: 'Input Parameters',
  webhook: 'Webhook Payload',
  event: 'Event Data',
};

const TriggerDetailsPanel: React.FC<TriggerDetailsPanelProps> = ({
  triggerNode,
  executionData,
  selectedExecution,
  getDuration,
  copyToClipboard,
  accountId,
}) => {
  const router = useRouter();
  const triggerData = triggerNode.data;
  const triggerType: string = triggerData?.trigger?.type || triggerData?.triggerType || 'manual';
  const triggerParams = triggerData?.trigger?.params || triggerData?.triggerParams || {};
  const triggerLabel = triggerData?.label || 'Trigger';
  const executionInputs = executionData?.inputs;
  const triggeredBy = executionData?.triggered_by || selectedExecution?.triggered_by;

  // Investigate link — mirrors the backend event-id resolution (executor.go extractEventIDFromInputs):
  // the triggering event is nested under inputs.event, keyed by `id` (fallback `event_id`).
  const eventObj = executionInputs && typeof executionInputs === 'object' ? (executionInputs as any).event : undefined;
  const eventId: string | undefined = eventObj?.id || eventObj?.event_id;
  const investigateAccountId: string | undefined = eventObj?.cloud_account_id || accountId;
  const investigateHref =
    triggerType === 'event' && eventId && investigateAccountId
      ? `/investigate?id=${encodeURIComponent(eventId)}&accountId=${encodeURIComponent(investigateAccountId)}`
      : undefined;

  const meta = TRIGGER_META[triggerType] || TRIGGER_META.manual;
  const hasConfig = triggerType !== 'manual' && Object.keys(triggerParams).length > 0;
  const hasInputs = executionInputs && (typeof executionInputs === 'string' ? executionInputs.length > 0 : Object.keys(executionInputs).length > 0);

  // Fix #5: Memoize JSON.stringify for large payloads
  const inputsString = useMemo(() => {
    if (!executionInputs) {
      return '';
    }
    return typeof executionInputs === 'string' ? executionInputs : JSON.stringify(executionInputs, null, 2);
  }, [executionInputs]);

  const renderTriggerConfig = () => {
    switch (triggerType) {
      case 'schedule':
        return <ScheduleConfig params={triggerParams} />;
      case 'webhook':
        return <WebhookConfig params={triggerParams} />;
      case 'event':
        return <EventConfig params={triggerParams} />;
      default:
        return null;
    }
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {/* Header */}
      <Box sx={{ padding: 'var(--ds-space-4) var(--ds-space-4)', borderBottom: '1px solid var(--ds-brand-150)', flexShrink: 0 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
          <Box
            sx={{
              width: '36px',
              height: '36px',
              borderRadius: 'var(--ds-radius-lg)',
              background: `linear-gradient(135deg, color-mix(in srgb, ${TRIGGER_COLOR} 9%, transparent) 0%, color-mix(in srgb, ${TRIGGER_COLOR} 19%, transparent) 100%)`,
              border: `1.5px solid color-mix(in srgb, ${TRIGGER_COLOR} 25%, transparent)`,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            {meta.iconIsImage && meta.icon ? (
              <SafeIcon src={meta.icon?.default || meta.icon} alt={triggerType} width={18} height={18} />
            ) : (
              <span style={{ fontSize: 'var(--ds-text-title)' }}>&#9889;</span>
            )}
          </Box>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography
              sx={{
                fontWeight: 'var(--ds-font-weight-semibold)',
                fontSize: 'var(--ds-text-body-lg)',
                color: ds.gray[700],
                fontFamily: 'Poppins, sans-serif',
                letterSpacing: '-0.01em',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {triggerLabel}
            </Typography>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', mt: 'var(--ds-space-1)' }}>
              <Box
                data-testid='trigger-type-badge'
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: TRIGGER_COLOR,
                  backgroundColor: TRIGGER_BG,
                  border: `1px solid color-mix(in srgb, ${TRIGGER_COLOR} 19%, transparent)`,
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.05em',
                }}
              >
                {meta.label} Trigger
              </Box>
            </Box>
          </Box>
          {investigateHref && (
            <Button
              id='trigger-investigate-btn'
              composition='icon+text'
              tone='secondary'
              size='xs'
              href={investigateHref}
              // Client-side nav to avoid a full reload; keep href so modifier-clicks
              // (cmd/ctrl/middle) still open the investigation in a new tab.
              onClick={(e) => {
                if (e.metaKey || e.ctrlKey || e.shiftKey || e.button === 1) {
                  return;
                }
                e.preventDefault();
                router.push(investigateHref);
              }}
              icon={<SafeIcon src={InvestigateHomeIcon} width={13} height={13} />}
            >
              Investigation
            </Button>
          )}
          {selectedExecution && (
            <Box sx={{ flexShrink: 0 }} data-testid='trigger-panel-status-badge'>
              <Label text={selectedExecution.status.toUpperCase()} />
            </Box>
          )}
        </Box>
      </Box>

      {/* Scrollable content */}
      <Box className='custom-scrollbar' sx={{ flex: 1, overflowY: 'auto', padding: 'var(--ds-space-4)' }}>
        {/* Execution summary strip */}
        {selectedExecution && (
          <Box
            sx={{
              display: 'flex',
              gap: 'var(--ds-space-4)',
              flexWrap: 'wrap',
              padding: 'var(--ds-space-3) var(--ds-space-4)',
              backgroundColor: ds.background[200],
              borderRadius: 'var(--ds-radius-lg)',
              border: `1px solid ${ds.gray[200]}`,
              mb: 'var(--ds-space-4)',
            }}
          >
            {triggeredBy && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: ds.gray[600],
                    textTransform: 'uppercase',
                    letterSpacing: '0.04em',
                  }}
                >
                  Triggered by
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  {triggeredBy}
                </Typography>
              </Box>
            )}
            {selectedExecution.start_time && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: ds.gray[600],
                    textTransform: 'uppercase',
                    letterSpacing: '0.04em',
                  }}
                >
                  Started
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  <Datetime value={selectedExecution.start_time} />
                </Typography>
              </Box>
            )}
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: ds.gray[600],
                  textTransform: 'uppercase',
                  letterSpacing: '0.04em',
                }}
              >
                Duration
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                {getDuration(selectedExecution.start_time as string, selectedExecution.close_time)}
              </Typography>
            </Box>
          </Box>
        )}

        {/* Trigger configuration section */}
        {hasConfig && (
          <Box sx={{ mb: 'var(--ds-space-4)' }} data-testid='trigger-config-section'>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', mb: 'var(--ds-space-3)' }}>
              <Schedule sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[400] }} />
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: ds.gray[700],
                  fontFamily: 'Poppins, sans-serif',
                }}
              >
                Configuration
              </Typography>
            </Box>
            <Box
              sx={{
                padding: 'var(--ds-space-4)',
                backgroundColor: 'white',
                borderRadius: 'var(--ds-radius-lg)',
                border: `1px solid ${ds.gray[200]}`,
              }}
            >
              {renderTriggerConfig()}
            </Box>
          </Box>
        )}

        {/* Execution inputs section */}
        {hasInputs && (
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 'var(--ds-space-3)' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                <InputIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[400] }} />
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: ds.gray[700],
                    fontFamily: 'Poppins, sans-serif',
                  }}
                >
                  {INPUT_SECTION_LABELS[triggerType] || 'Execution Inputs'}
                </Typography>
              </Box>
              <Button
                id='trigger-inputs-copy-btn'
                composition='icon-only'
                tone='ghost'
                size='xs'
                aria-label='Copy inputs'
                icon={<ContentCopy sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                onClick={() => copyToClipboard(inputsString, 'Inputs')}
              />
            </Box>
            <Box data-testid='trigger-inputs-display'>
              <JsonTreeView data={executionInputs} defaultExpanded={2} maxHeight='400px' fontSize='var(--ds-text-small)' />
            </Box>
          </Box>
        )}

        {/* Empty state */}
        {!hasConfig && !hasInputs && (
          <Box
            data-testid='trigger-empty-state'
            sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', py: 6, gap: 'var(--ds-space-2)' }}
          >
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.gray[400] }}>
              {EMPTY_STATE_MESSAGES[triggerType] || 'No trigger details available for this execution.'}
            </Typography>
          </Box>
        )}
      </Box>
    </Box>
  );
};

export default TriggerDetailsPanel;
