import React, { useMemo, useState } from 'react';
import { Box, Typography, Popover } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PersonOutlineOutlinedIcon from '@mui/icons-material/PersonOutlineOutlined';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { DropdownMenu, type DropdownMenuAlign } from '@ui/DropdownMenu';
import { Button as DsButton } from '@ui/Button';
import { Chip, type ChipTone } from '@ui/Chip';
import { snackbar } from '@shared/snackbarService';
import { ds } from '@utils/colors';
import apiTriage from '@api1/triage';

type Priority = 'P0' | 'P1' | 'P2' | 'P3';

const PRIORITY_LABELS: Record<Priority, string> = { P0: 'Critical', P1: 'High', P2: 'Medium', P3: 'Low' };

// Priority → DS chip tone. Only P0 is solid so critical issues stand out;
// P1–P3 use the tone's light tint with colored text (design handoff, Sep 2026).
const PRIORITY_TONES: Record<Priority, ChipTone> = { P0: 'critical', P1: 'critical', P2: 'warning', P3: 'info' };

// Label text color follows the chip's tone family.
const PRIORITY_LABEL_COLORS: Record<Priority, string> = {
  P0: 'var(--ds-red-700)',
  P1: 'var(--ds-red-600)',
  P2: 'var(--ds-amber-700)',
  P3: 'var(--ds-blue-600)',
};

// Mirrors backend priorityToMinScore (api-server/services/triage/scoring.go): pinning a
// priority snaps computed_score to the band floor. Keep the two in sync.
const PRIORITY_MIN_SCORE: Record<string, number> = { P0: 80, P1: 60, P2: 40, P3: 20 };

// Mirrors the backend's human-override overlay (api-server/services/triage/classification.go,
// rules.go): scoring_path/authority/corrected_priority are merged on top of the prior factors so
// the breakdown popover's isHuman check flips immediately, without waiting for a refetch.
const applyHumanOverrideFactors = (currentFactors: unknown, priority: string, scope: 'this_event' | 'this_fingerprint'): Record<string, unknown> => {
  const parsed =
    typeof currentFactors === 'string'
      ? (() => {
          try {
            return JSON.parse(currentFactors || '{}');
          } catch {
            return {};
          }
        })()
      : currentFactors || {};

  // JSON.parse succeeds on stringified primitives too (e.g. "123"); guard against spreading
  // anything but a plain object, which would otherwise corrupt the result (e.g. a string spreads
  // into per-character keys).
  const parsedObj = typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : {};

  return {
    ...parsedObj,
    scoring_path: 'human_override',
    authority: scope === 'this_fingerprint' ? 'human:class' : 'human:event',
    corrected_priority: priority,
  };
};

const getTierName = (tier: number): string => {
  switch (tier) {
    case 0:
      return 'Customer Facing';
    case 1:
      return 'Core Infra';
    case 2:
      return 'Business Service';
    case 3:
      return 'Monitoring';
    default:
      return `Tier ${tier}`;
  }
};

// "control_plane" -> "Control Plane", "escalating" -> "Escalating".
const titleCase = (s: unknown): string =>
  s
    ? String(s)
        .replace(/_/g, ' ')
        .replace(/\b\w/g, (c) => c.toUpperCase())
    : '';

const envLabel = (env: unknown): string => {
  if (env === 'prod') {
    return 'Production';
  }
  if (env === 'non_prod' || env === 'non-prod') {
    return 'Non-Production';
  }
  return 'Default';
};

// Shared styles for the breakdown label/value rows.
const rowSx = { display: 'flex', justifyContent: 'space-between', alignItems: 'center' } as const;
const labelSx = { fontSize: ds.text.small, color: ds.gray[600] } as const;
const valueSx = { fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] } as const;

export interface TriageScoreProps {
  /** Computed score (0–100). Absent → the score line shows a dash. */
  score?: number | null;
  /** Effective computed priority (P0..P3) shown on the chip. */
  priority?: string | null;
  /** Score factors (object or JSON string) backing the breakdown popover. */
  scoreFactors?: unknown;
  /** Score confidence (0–1) shown in the breakdown popover. */
  confidence?: number | null;
  /** Representative event id the correction is recorded against. The backend derives the
   * signal class from this event's fingerprint, so no fingerprint prop is needed. */
  eventId?: string | null;
  /** Cloud account id the event belongs to. */
  accountId?: string | null;
  /** Whether the user can write to this account. Read-only users see a static chip. */
  canWrite?: boolean;
  /**
   * Called optimistically on selection with the new priority, its band-floor score (matching
   * what the backend will persist), and the overlaid score factors; and again with the previous
   * values if the background API call fails (so the caller can revert).
   */
  onChanged?: (newPriority: string | null, newScore?: number | null, newFactors?: Record<string, unknown> | unknown) => void;
  /** Pin scope. Defaults to the whole alert class ('this_fingerprint'). */
  scope?: 'this_event' | 'this_fingerprint';
  align?: DropdownMenuAlign;
  /** 'stacked' = two lines for table cells (design 2a); 'inline' = one line for panels (3a). */
  layout?: 'stacked' | 'inline';
}

/**
 * Unified triage-score cell (design handoff "Triage score component", Sep 2026): a priority
 * chip + label + chevron forming one override trigger, with the score as neutral mono text.
 * Replaces the former ScoreDisplay (colored score + bar) / PriorityPinControl (pill) pair.
 *
 * Deviation from the handoff, agreed with the team: the per-row info icon and its per-event
 * breakdown popover (LLM reasoning, factors, confidence, human-override notice) are kept —
 * the handoff's header-level tooltip only explains scoring generically. "Reset to auto" is
 * deferred until the backend can clear an override and expose the auto priority.
 */
const TriageScore: React.FC<TriageScoreProps> = ({
  score,
  priority,
  scoreFactors,
  confidence,
  eventId,
  accountId,
  canWrite = false,
  onChanged,
  scope = 'this_fingerprint',
  align = 'start',
  layout = 'stacked',
}) => {
  const [anchorEl, setAnchorEl] = useState<Element | null>(null);
  const popoverOpen = Boolean(anchorEl);
  const handleMouseEnter = (event: React.MouseEvent) => setAnchorEl(event.currentTarget);
  const handleMouseLeave = () => setAnchorEl(null);

  const factors = useMemo(() => {
    if (typeof scoreFactors === 'string') {
      try {
        return JSON.parse(scoreFactors || '{}');
      } catch {
        return {};
      }
    }
    return (scoreFactors as Record<string, any>) || {};
  }, [scoreFactors]);

  const isHuman = factors.scoring_path === 'human_override' || (typeof factors.authority === 'string' && factors.authority.startsWith('human:'));
  const isLLM = !isHuman && factors.scoring_path === 'llm_verdict';
  const correctionScope = factors.authority === 'human:class' ? 'this alert class' : 'this alert';
  const actualConfidence =
    factors.verdict_confidence !== undefined ? factors.verdict_confidence : factors.confidence !== undefined ? factors.confidence : confidence;

  const knownPriority = priority && Object.prototype.hasOwnProperty.call(PRIORITY_LABELS, priority) ? (priority as Priority) : null;
  const hasScore = score !== null && score !== undefined;
  const canOverride = canWrite && Boolean(eventId) && Boolean(accountId);

  const pin = async (newPriority: string): Promise<void> => {
    if (newPriority === priority || !eventId) return;

    const previousPriority = priority ?? null;
    const previousScore = score ?? null;
    const previousFactors = scoreFactors ?? null;
    // optimistic update — also overlay the human-override markers so the breakdown popover
    // reflects the correction immediately instead of showing stale model reasoning.
    onChanged?.(newPriority, PRIORITY_MIN_SCORE[newPriority] ?? null, applyHumanOverrideFactors(scoreFactors, newPriority, scope));

    try {
      const res = await apiTriage.classifyEvent({
        event_id: eventId,
        classification: 'true_positive',
        reason_code: 'correct_severity',
        corrected_priority: newPriority,
        apply_scope: scope,
        apply_to_existing: scope === 'this_fingerprint',
        confirmed: true,
        // A priority override is not a triage-state assertion: keep nb_status as-is
        // instead of letting the classify pipeline force it to ACTION_REQUIRED.
        preserve_status: true,
      });
      if (!res?.success) {
        snackbar.error('Failed to set priority');
        onChanged?.(previousPriority, previousScore, previousFactors); // revert
      }
    } catch {
      snackbar.error('Failed to set priority');
      onChanged?.(previousPriority, previousScore, previousFactors); // revert
    }
  };

  const priorityChip = knownPriority ? (
    <Chip variant='tag' size='xs' shape='rect' tone={PRIORITY_TONES[knownPriority]} solid={knownPriority === 'P0'}>
      {knownPriority}
    </Chip>
  ) : null;

  const priorityLabel = knownPriority ? (
    <Typography component='span' sx={{ fontSize: ds.text.small, color: PRIORITY_LABEL_COLORS[knownPriority], lineHeight: 1 }}>
      {PRIORITY_LABELS[knownPriority]}
    </Typography>
  ) : null;

  // Parts chip + label + chevron form ONE button opening the override menu; the score is
  // not clickable. Read-only users get the same chip/label without the menu affordance.
  const trigger = canOverride ? (
    <DropdownMenu
      align={align}
      minWidth={184}
      trigger={
        <DsButton
          tone='ghost'
          size='xs'
          // Tighten the button chrome to the handoff's trigger spec (padding 0 4px 0 2px,
          // -2px margin) so the chip's left edge stays flush with the Score line below /
          // the divider row beside it. Inline style wins over the size tokens' padX.
          style={{ paddingLeft: ds.space[0], paddingRight: ds.space[1], marginLeft: ds.space.mul(0, -1) }}
          aria-label={`Priority ${knownPriority ? `${knownPriority} ${PRIORITY_LABELS[knownPriority]}` : 'not set'}${
            hasScore ? `, score ${score}` : ''
          }. Change priority`}
          tooltip='Override priority for this alert class'
          onClick={(e) => e.stopPropagation()}
        >
          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
            {priorityChip}
            {priorityLabel || (
              <Typography component='span' sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: 1 }}>
                Set
              </Typography>
            )}
            <KeyboardArrowDownIcon sx={{ fontSize: ds.text.body, color: ds.gray[500] }} />
          </Box>
        </DsButton>
      }
      items={[
        {
          type: 'section' as const,
          label: hasScore ? `Override priority · score ${score}` : 'Override priority',
        },
        ...(Object.keys(PRIORITY_LABELS) as Priority[]).map((p) => ({
          id: p,
          label: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              <Chip variant='tag' size='xs' shape='rect' tone={PRIORITY_TONES[p]} solid={p === 'P0'}>
                {p}
              </Chip>
              {PRIORITY_LABELS[p]}
            </Box>
          ),
          active: p === priority,
          onSelect: () => pin(p),
        })),
      ]}
    />
  ) : (
    (priorityChip || priorityLabel) && (
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
        {priorityChip}
        {priorityLabel}
      </Box>
    )
  );

  const scoreLine = (
    <Box data-testid='score-display' sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
      <Typography component='span' sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
        Score
      </Typography>
      <Typography
        component='span'
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        sx={{
          fontFamily: 'var(--ds-font-mono)',
          fontSize: ds.text.small,
          fontWeight: ds.weight.medium,
          color: hasScore ? ds.gray[700] : ds.gray[400],
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {hasScore ? score : '-'}
      </Typography>
      <Box onMouseEnter={handleMouseEnter} onMouseLeave={handleMouseLeave} sx={{ display: 'inline-flex', alignItems: 'center' }}>
        {isHuman ? (
          <PersonOutlineOutlinedIcon
            data-testid='score-corrected-icon'
            titleAccess='Manually corrected'
            sx={{ fontSize: ds.text.small, color: ds.blue?.[500] || ds.gray[600] }}
          />
        ) : (
          <InfoOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.gray[400] }} />
        )}
      </Box>
    </Box>
  );

  // Nothing to show at all (no score, no priority, read-only): the old cell's dash.
  if (!hasScore && !knownPriority && !canOverride) {
    return <Typography sx={{ color: ds.gray[400], fontSize: ds.text.small }}>-</Typography>;
  }

  return (
    <>
      {layout === 'inline' ? (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
          {trigger}
          <Box sx={{ width: '1px', height: ds.space.mul(0, 7), backgroundColor: ds.gray[200] }} />
          {scoreLine}
        </Box>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], alignItems: 'flex-start' }}>
          {trigger}
          {scoreLine}
        </Box>
      )}

      <Popover
        open={popoverOpen}
        anchorEl={anchorEl}
        onClose={handleMouseLeave}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
        transformOrigin={{ vertical: 'top', horizontal: 'center' }}
        sx={{
          pointerEvents: 'none',
          '& .MuiPopover-paper': {
            borderRadius: ds.radius.md,
            boxShadow: `0 4px 20px ${ds.gray.alpha[300]}`,
            minWidth: ds.space.mul(0, 140),
            maxWidth: ds.space.mul(0, 180),
          },
        }}
        disableRestoreFocus
      >
        <Box sx={{ p: ds.space[4], minWidth: ds.space.mul(0, 120) }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[4] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Priority Score</Typography>
          </Box>

          {isHuman ? (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: ds.space[1],
                mb: ds.space[2],
                px: ds.space[2],
                py: ds.space[1],
                borderRadius: ds.radius.sm,
                backgroundColor: ds.blue?.[100] || ds.gray[100],
              }}
            >
              <PersonOutlineOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.blue?.[500] || ds.gray[600] }} />
              <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.blue?.[600] || ds.gray[700] }}>
                Corrected by a human — overrides the model for {correctionScope}
              </Typography>
            </Box>
          ) : null}

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            {isHuman ? (
              <>
                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Set priority</Typography>
                  <Typography sx={valueSx}>{factors.corrected_priority || priority}</Typography>
                </Box>

                {factors.category || factors.intrinsic || factors.reasoning ? (
                  <Box
                    sx={{
                      mt: ds.space[1],
                      pt: ds.space[1],
                      borderTop: `1px solid ${ds.gray[200]}`,
                      display: 'flex',
                      flexDirection: 'column',
                      gap: ds.space[1],
                    }}
                  >
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontStyle: 'italic' }}>Model had suggested</Typography>
                    {factors.category ? (
                      <Box sx={rowSx}>
                        <Typography sx={labelSx}>Category</Typography>
                        <Typography sx={valueSx}>{factors.category}</Typography>
                      </Box>
                    ) : null}
                    {factors.intrinsic ? (
                      <Box sx={rowSx}>
                        <Typography sx={labelSx}>Severity</Typography>
                        <Typography sx={valueSx}>{titleCase(factors.intrinsic)}</Typography>
                      </Box>
                    ) : null}
                    {factors.reasoning ? (
                      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: 1.4 }}>{factors.reasoning}</Typography>
                    ) : null}
                  </Box>
                ) : null}
              </>
            ) : isLLM ? (
              <>
                {factors.reasoning ? (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], mb: ds.space[1] }}>
                    <Typography sx={labelSx}>Why</Typography>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.4 }}>{factors.reasoning}</Typography>
                  </Box>
                ) : null}

                {factors.category ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Category</Typography>
                    <Typography sx={valueSx}>{factors.category}</Typography>
                  </Box>
                ) : null}

                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Severity</Typography>
                  <Typography sx={valueSx}>{titleCase(factors.intrinsic)}</Typography>
                </Box>

                {factors.blast ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Blast radius</Typography>
                    <Typography sx={valueSx}>{titleCase(factors.blast)}</Typography>
                  </Box>
                ) : null}

                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Environment</Typography>
                  <Typography sx={valueSx}>{envLabel(factors.env_category)}</Typography>
                </Box>

                {factors.recurrence_semantics && factors.recurrence_semantics !== 'neutral' ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Recurrence</Typography>
                    <Typography sx={valueSx}>{titleCase(factors.recurrence_semantics)}</Typography>
                  </Box>
                ) : null}

                {factors.band ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Priority band</Typography>
                    <Typography sx={valueSx}>{factors.band}</Typography>
                  </Box>
                ) : null}

                {factors.correlation_type ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Correlation</Typography>
                    <Typography sx={valueSx}>{titleCase(factors.correlation_type)}</Typography>
                  </Box>
                ) : null}
              </>
            ) : (
              <>
                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Severity</Typography>
                  <Typography sx={valueSx}>{factors.base_severity >= 50 ? 'High' : factors.base_severity >= 25 ? 'Medium' : 'Low'}</Typography>
                </Box>

                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Service Tier</Typography>
                  <Typography sx={valueSx}>{getTierName(factors.service_tier)}</Typography>
                </Box>

                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Environment</Typography>
                  <Typography
                    sx={{
                      fontSize: ds.text.small,
                      fontWeight: ds.weight.medium,
                      color: factors.env_multiplier < 1 ? ds.gray[400] : ds.gray[700],
                    }}
                  >
                    {factors.env_multiplier === 1 ? 'Production' : factors.env_multiplier === 0.3 ? 'Non-Production' : 'Default'}
                  </Typography>
                </Box>

                {factors.duplicate_penalty > 0 && (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Duplicate</Typography>
                    <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.red[500] }}>Yes (score reduced)</Typography>
                  </Box>
                )}
              </>
            )}

            {(() => {
              const confidenceValue = parseFloat(actualConfidence) || 0;
              const confidencePercent = Math.round(confidenceValue * 100);
              return (
                <Box
                  sx={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    mt: ds.space[1],
                    pt: ds.space[1],
                    borderTop: `1px solid ${ds.gray[200]}`,
                  }}
                >
                  <Typography sx={labelSx}>Confidence</Typography>
                  <Typography sx={valueSx}>{confidencePercent}%</Typography>
                </Box>
              );
            })()}
          </Box>
        </Box>
      </Popover>
    </>
  );
};

export default TriageScore;
