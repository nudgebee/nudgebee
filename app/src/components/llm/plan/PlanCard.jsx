import React, { useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { CollapsableCard } from '@ui/CollapsableCard';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { AskNudgebeeSkipIcon, AskNudgebeeSuccessIcon, RunningIcon } from '@assets';
import { ds } from '@utils/colors';
import { groupStepsByPhase, isClosedStatus, OUTSIDE_PLAN_STEP, summarizePlan } from './planState';

// Live plan of one Nubi turn (#41443): phases, steps with status, and the tool
// calls each step served. Open while the turn runs; collapsed once it answered.

// Hypothesis results read as a status pill; task steps as an icon.
const RESULT_LABELS = {
  supported: { tone: 'success', text: 'Supported' },
  refuted: { tone: 'neutral', text: 'Refuted' },
  inconclusive: { tone: 'warning', text: 'Inconclusive' },
  blocked: { tone: 'warning', text: 'Blocked' },
};

const STATUS_ICONS = {
  done: { icon: AskNudgebeeSuccessIcon, label: 'Done' },
  skipped: { icon: AskNudgebeeSkipIcon, label: 'Skipped' },
  in_progress: { icon: RunningIcon, label: 'In progress' },
  testing: { icon: RunningIcon, label: 'Testing' },
};

// Not started yet (pending task, open hypothesis): a quiet hollow circle, so
// only the step actually being worked on looks active.
const NotStartedIcon = () => (
  <Box
    component='span'
    aria-label='Not started'
    data-testid='plan-step-not-started'
    sx={{ width: 14, height: 14, flexShrink: 0, borderRadius: ds.radius.pill, border: `1.5px solid ${ds.gray[400]}`, mx: '1px' }}
  />
);

const StepRow = ({ step, calls, count, nested, onOpen, plain }) => {
  const [showCalls, setShowCalls] = useState(false);
  const result = RESULT_LABELS[step.status];
  const icon = STATUS_ICONS[step.status];
  return (
    <Box data-testid={`plan-step-${step.id}`} sx={{ pl: nested ? ds.space[6] : 0, py: ds.space[1] }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
        {plain ? null : result ? (
          <Label tone={result.tone}>{result.text}</Label>
        ) : icon ? (
          <SafeIcon src={icon.icon} alt={icon.label} width={16} height={16} />
        ) : (
          <NotStartedIcon />
        )}
        <Box component='span' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium }}>
          {step.title}
        </Box>
        {step.kind === 'hypothesis' && step.prior && !isClosedStatus(step.status) && (
          <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}>
            Likely: {step.prior}
          </Box>
        )}
        {count > 0 && (
          <Button
            tone='link'
            size='xs'
            data-testid={`plan-step-calls-${step.id}`}
            onClick={() => (onOpen ? onOpen(step.id) : setShowCalls((v) => !v))}
          >
            {count === 1 ? '1 call' : `${count} calls`}
          </Button>
        )}
      </Box>
      {step.finding && <Box sx={{ pl: ds.space[6], fontSize: ds.text.caption, color: ds.gray[700] }}>{step.finding}</Box>}
      {showCalls && (
        <Box component='ul' sx={{ m: 0, pl: ds.space[7], fontSize: ds.text.caption, color: ds.gray[700] }}>
          {calls.map((c) => (
            <li key={c.id}>
              <Box component='span' sx={{ fontFamily: ds.font.mono }}>
                {c.tool}
              </Box>
              {c.thought ? ` — ${c.thought}` : ''}
            </li>
          ))}
        </Box>
      )}
    </Box>
  );
};

StepRow.propTypes = {
  step: PropTypes.object.isRequired,
  calls: PropTypes.array.isRequired,
  count: PropTypes.number.isRequired,
  nested: PropTypes.bool,
  onOpen: PropTypes.func,
  plain: PropTypes.bool,
};

const PlanCard = ({ plan, live, onOpenStep }) => {
  if (!plan?.steps?.length) return null;
  const { total, closed, activeTitle } = summarizePlan(plan.steps);
  const phases = groupStepsByPhase(plan.steps);
  const callsFor = (id) => plan.toolCallsByStep?.[id] || [];
  // Sub-agent work started from a step counts toward it, so the numbers add up to the Tasks chip.
  const countFor = (id) => plan.callCountByStep?.[id] ?? callsFor(id).length;
  const outsideCount = countFor(OUTSIDE_PLAN_STEP);
  let meta = `${closed} of ${total} closed`;
  if (live && activeTitle) meta = `Now: ${activeTitle}`;
  else if (live && closed === total) meta = 'All steps closed · still working';

  return (
    <Box data-testid='nubi-plan-card' sx={{ my: ds.space[3] }}>
      <CollapsableCard
        // Live turns open; answered turns collapse but remember a user's toggle per notebook row.
        key={live ? 'live' : 'done'}
        defaultOpen={live}
        elevation='flat'
        composition='header+meta+body'
        header={<Box sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold }}>Plan</Box>}
        meta={<Box sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}>{meta}</Box>}
      >
        {phases.map((phase) => (
          <Box key={phase.name || 'steps'} sx={{ mb: ds.space[2] }}>
            {phase.name && (
              <Box
                sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[600], textTransform: 'uppercase', mb: ds.space[1] }}
              >
                {phase.name}
              </Box>
            )}
            {phase.steps.map((step) => (
              <React.Fragment key={step.id}>
                <StepRow step={step} calls={callsFor(step.id)} count={countFor(step.id)} onOpen={onOpenStep} />
                {step.children.map((child) => (
                  <StepRow key={child.id} step={child} calls={callsFor(child.id)} count={countFor(child.id)} onOpen={onOpenStep} nested />
                ))}
              </React.Fragment>
            ))}
          </Box>
        ))}
        {outsideCount > 0 && (
          <Box sx={{ mt: ds.space[1] }}>
            <StepRow
              step={{ id: OUTSIDE_PLAN_STEP, title: 'Outside the plan', status: 'done' }}
              calls={callsFor(OUTSIDE_PLAN_STEP)}
              count={outsideCount}
              onOpen={onOpenStep}
              plain
            />
          </Box>
        )}
      </CollapsableCard>
    </Box>
  );
};

PlanCard.propTypes = {
  plan: PropTypes.shape({
    steps: PropTypes.array,
    toolCallsByStep: PropTypes.object,
    callCountByStep: PropTypes.object,
  }),
  live: PropTypes.bool,
  onOpenStep: PropTypes.func,
};

export default PlanCard;
