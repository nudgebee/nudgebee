/**
 * DiffCard — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/agentic/diff-card.html
 *
 * Agent-proposed change with approve / reject actions. The trust-critical
 * surface — purple-tinted header marks the proposal as agent-originated, the
 * body shows the change (text, YAML, or action summary), the footer carries
 * the decision.
 *
 * Composes `ds/Chip` (Auto-pilot provenance) + `ds/Button` (Approve/Reject) +
 * `ds/DiffViewer` (text/yaml diff) + `ds/CostCallout` + `ds/ConfidenceIndicator`
 * (meta row).
 *
 * Variants per spec:
 *   composition   = 'text-diff' | 'yaml-diff' | 'action-summary' | 'command'
 *   state         = 'pending' | 'approved' | 'rejected' | 'applied' | 'failed'
 *   tone          = severity-axis (low/medium/high/critical, inherited from change risk)
 *   requireReason = 'never' | 'on-reject' | 'always'
 *
 * Don't (per spec):
 *   - Don't render a DiffCard without a `why` line. The whole trust contract
 *     is "agent explains, human decides".
 *   - Don't show approve as `danger` tone unless the action is destructive
 *     (delete, restart, scale-to-zero). Apply-config is `primary`.
 *   - Don't auto-approve — even on `high` confidence, the user has to click.
 *   - Don't reuse DiffCard for non-agent changes (PR diff, config history).
 *     Those are `DiffViewer`.
 */
import * as React from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from './Button';
import { Chip } from './Chip';
import DiffViewer from './DiffViewer';
import { CostCallout, type CostTone } from './CostCallout';
import { ConfidenceIndicator, type ConfidenceLevel } from './ConfidenceIndicator';

export type DiffCardComposition = 'text-diff' | 'yaml-diff' | 'action-summary' | 'command';
export type DiffCardState = 'pending' | 'approved' | 'rejected' | 'applied' | 'failed';
export type DiffCardRequireReason = 'never' | 'on-reject' | 'always';

export interface DiffCardCostMeta {
  /** Numeric value. Sign is conveyed via `impact` (savings → cost-axis green; waste → red). */
  value: number;
  /** Cost axis bucket. `savings`/`waste` resolve to high/medium/low based on |value|. */
  impact: 'savings' | 'waste' | 'neutral';
  /** Period suffix (e.g. "/ mo"). */
  period?: React.ReactNode;
  currency?: string;
}

export interface DiffCardMeta {
  confidence?: ConfidenceLevel;
  cost?: DiffCardCostMeta;
  /** Risk level — drives optional severity-axis chip in the meta row. */
  risk?: 'low' | 'medium' | 'high' | 'critical';
}

export interface DiffCardProps {
  composition: DiffCardComposition;
  /** Header title (e.g. "Memory limit increase for nginx-7c4b5d"). */
  title: React.ReactNode;
  /** Provenance label rendered in the agent chip (default "Auto-pilot"). */
  source?: string;
  /** Required per spec — the "agent explains, human decides" rationale line. */
  why: React.ReactNode;
  /** For composition='text-diff' or 'yaml-diff': pass to DiffViewer as `gitDiff`. */
  diff?: string;
  /** For composition='yaml-diff' / 'text-diff': use a different file name in DiffViewer. */
  diffFileName?: string;
  /** For composition='action-summary': free-form summary content. */
  summary?: React.ReactNode;
  /** For composition='command': the shell command. Renders as a dark-bg pre block. */
  command?: string;
  /** Meta row (confidence + cost + risk). Optional but encouraged. */
  meta?: DiffCardMeta;
  state?: DiffCardState;
  /** Optional contextual time note shown in the state badge ("pending · 02:14"). */
  stateNote?: React.ReactNode;
  requireReason?: DiffCardRequireReason;
  /** Approve handler — typically primary; destructive (`command` of restart/delete/scale-to-zero) → danger. */
  onApprove?: () => void;
  /** Reject handler — receives optional reason text per `requireReason`. */
  onReject?: (reason?: string) => void;
  /** Override approve button label (default depends on composition). */
  approveLabel?: string;
  /** Force approve tone — defaults to 'primary' for config diffs, 'danger' for destructive commands. */
  approveTone?: 'primary' | 'danger';
  className?: string;
  id?: string;
}

const STATE_TEXT: Record<DiffCardState, string> = {
  pending: 'pending',
  approved: '✓ approved',
  rejected: '✕ rejected',
  applied: '✓ applied',
  failed: '✕ failed',
};

const STATE_TONE: Record<DiffCardState, { color: string; bg: string }> = {
  pending: { color: 'var(--ds-amber-700)', bg: 'var(--ds-amber-100)' },
  approved: { color: 'var(--ds-green-700)', bg: 'var(--ds-green-100)' },
  rejected: { color: 'var(--ds-gray-700)', bg: 'var(--ds-gray-100)' },
  applied: { color: 'var(--ds-green-700)', bg: 'var(--ds-green-100)' },
  failed: { color: 'var(--ds-red-700)', bg: 'var(--ds-red-100)' },
};

function deriveCostTone(impact: 'savings' | 'waste' | 'neutral', value: number): CostTone {
  if (impact === 'neutral') return 'neutral';
  if (impact === 'waste') return 'waste';
  // savings: bucket by |value|
  const abs = Math.abs(value);
  if (abs >= 2000) return 'high-savings';
  if (abs >= 500) return 'medium-savings';
  return 'low-savings';
}

const RISK_TONE: Record<NonNullable<DiffCardMeta['risk']>, { color: string; label: string }> = {
  low: { color: 'var(--ds-gray-600)', label: 'low risk' },
  medium: { color: 'var(--ds-amber-600)', label: 'medium risk' },
  high: { color: 'var(--ds-red-600)', label: 'high risk' },
  critical: { color: 'var(--ds-red-700)', label: 'critical risk' },
};

function defaultApproveLabel(composition: DiffCardComposition): string {
  if (composition === 'command') return 'Approve & run';
  if (composition === 'action-summary') return 'Approve';
  return 'Approve & apply';
}

export function DiffCard({
  composition,
  title,
  source = 'Auto-pilot',
  why,
  diff,
  diffFileName,
  summary,
  command,
  meta,
  state = 'pending',
  stateNote,
  requireReason = 'never',
  onApprove,
  onReject,
  approveLabel,
  approveTone,
  className,
  id,
}: DiffCardProps) {
  const [reason, setReason] = React.useState('');
  const [showReasonInput, setShowReasonInput] = React.useState(false);

  const stateMeta = STATE_TONE[state];
  const isCollapsed = state !== 'pending'; // approved/rejected/applied/failed cards collapse to header-only per spec
  const resolvedApproveTone = approveTone ?? (composition === 'command' ? 'danger' : 'primary');

  const handleRejectClick = () => {
    if (requireReason === 'always' || (requireReason === 'on-reject' && !showReasonInput)) {
      setShowReasonInput(true);
      return;
    }
    onReject?.(reason || undefined);
  };

  const handleConfirmReject = () => {
    onReject?.(reason || undefined);
    setShowReasonInput(false);
  };

  return (
    <Box
      id={id}
      className={className}
      sx={{
        border: '1px solid var(--ds-purple-200, var(--ds-blue-200))',
        borderRadius: 'var(--ds-radius-md)',
        backgroundColor: 'var(--ds-background-100)',
        overflow: 'hidden',
        opacity: isCollapsed ? 0.85 : 1,
      }}
    >
      {/* Header — purple-tint marks agent-originated proposal */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
          padding: 'var(--ds-space-3)',
          backgroundColor: 'var(--ds-purple-100, var(--ds-blue-100))',
          borderBottom: isCollapsed ? 'none' : '1px solid var(--ds-purple-200, var(--ds-blue-200))',
        }}
      >
        <Chip size='xs' tone='agent'>
          {source}
        </Chip>
        <Box
          component='span'
          sx={{
            flex: 1,
            color: 'var(--ds-purple-700, var(--ds-blue-700))',
            fontSize: 'var(--ds-text-small)',
            minWidth: 0,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {title}
        </Box>
        <Box
          component='span'
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '4px',
            paddingLeft: '8px',
            paddingRight: '8px',
            paddingTop: '2px',
            paddingBottom: '2px',
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: stateMeta.color,
            backgroundColor: stateMeta.bg,
            borderRadius: 'var(--ds-radius-sm)',
            whiteSpace: 'nowrap',
            flexShrink: 0,
          }}
        >
          {STATE_TEXT[state]}
          {stateNote !== undefined && <span> · {stateNote}</span>}
        </Box>
      </Box>

      {!isCollapsed && (
        <>
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            {/* Meta row — confidence + cost + risk */}
            {meta && (
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--ds-space-3)',
                  marginBottom: 'var(--ds-space-3)',
                  fontSize: 'var(--ds-text-small)',
                  color: 'var(--ds-gray-600)',
                  flexWrap: 'wrap',
                }}
              >
                {meta.confidence && <ConfidenceIndicator level={meta.confidence} label={`${meta.confidence} confidence`} size='sm' />}
                {meta.cost && (
                  <>
                    <Box component='span' aria-hidden='true' sx={{ color: 'var(--ds-gray-300)' }}>
                      ·
                    </Box>
                    <CostCallout
                      value={meta.cost.value}
                      currency={meta.cost.currency}
                      tone={deriveCostTone(meta.cost.impact, meta.cost.value)}
                      period={meta.cost.period}
                      arrow={meta.cost.impact === 'savings' ? 'down' : meta.cost.impact === 'waste' ? 'up' : 'flat'}
                      size='sm'
                    />
                  </>
                )}
                {meta.risk && (
                  <>
                    <Box component='span' aria-hidden='true' sx={{ color: 'var(--ds-gray-300)' }}>
                      ·
                    </Box>
                    <Box component='span' sx={{ color: RISK_TONE[meta.risk].color, fontSize: 'var(--ds-text-small)' }}>
                      {RISK_TONE[meta.risk].label}
                    </Box>
                  </>
                )}
              </Box>
            )}

            {/* Body — composition-driven */}
            {(composition === 'text-diff' || composition === 'yaml-diff') && diff && (
              <DiffViewer gitDiff={diff} fileName={diffFileName ?? (composition === 'yaml-diff' ? 'config.yaml' : 'change.txt')} showHeader={false} />
            )}
            {composition === 'action-summary' && summary !== undefined && (
              <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{summary}</Box>
            )}
            {composition === 'command' && command !== undefined && (
              <Box
                component='pre'
                sx={{
                  backgroundColor: 'var(--ds-gray-700)',
                  color: 'var(--ds-background-100)',
                  padding: 'var(--ds-space-3)',
                  borderRadius: 'var(--ds-radius-sm)',
                  margin: 0,
                  fontFamily: 'var(--ds-font-mono)',
                  fontSize: 'var(--ds-text-small)',
                  overflowX: 'auto',
                }}
              >
                {command}
              </Box>
            )}

            {/* Why — required per spec */}
            <Box sx={{ marginTop: 'var(--ds-space-3)', fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
              <Typography component='strong' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', display: 'inline' }}>
                Why:
              </Typography>{' '}
              {why}
            </Box>
          </Box>

          {/* Reason input (when requireReason triggers it) */}
          {showReasonInput && (
            <Box
              sx={{
                paddingLeft: 'var(--ds-space-4)',
                paddingRight: 'var(--ds-space-4)',
                paddingBottom: 'var(--ds-space-3)',
                display: 'flex',
                gap: 'var(--ds-space-2)',
                alignItems: 'center',
              }}
            >
              <Box
                component='input'
                type='text'
                value={reason}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setReason(e.target.value)}
                placeholder='Reason for rejecting…'
                sx={{
                  flex: 1,
                  padding: 'var(--ds-space-2) var(--ds-space-3)',
                  fontSize: 'var(--ds-text-small)',
                  border: '1px solid var(--ds-gray-300)',
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: 'var(--ds-background-100)',
                  color: 'var(--ds-gray-700)',
                  '&:focus': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '0px', borderColor: 'transparent' },
                }}
              />
              <Button tone='secondary' size='sm' onClick={() => setShowReasonInput(false)}>
                Cancel
              </Button>
              <Button tone='primary' size='sm' onClick={handleConfirmReject}>
                Confirm reject
              </Button>
            </Box>
          )}

          {/* Actions — only on pending state, hidden when reason input is shown */}
          {!showReasonInput && (
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'flex-end',
                gap: 'var(--ds-space-2)',
                padding: 'var(--ds-space-3)',
                borderTop: '1px solid var(--ds-gray-200)',
                backgroundColor: 'var(--ds-gray-100)',
              }}
            >
              <Button tone='ghost' size='sm' onClick={handleRejectClick}>
                Reject
              </Button>
              <Button tone={resolvedApproveTone} size='sm' onClick={onApprove}>
                {approveLabel ?? defaultApproveLabel(composition)}
              </Button>
            </Box>
          )}
        </>
      )}
    </Box>
  );
}

export default DiffCard;
