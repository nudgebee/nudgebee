/**
 * Stepper — DS V2 of legacy VerticalStepNavigation + CustomStepper + NewVerticalStepper.
 * Spec:        app/design-system/primitives/navigation/stepper.html
 * Variants:    orientation = 'vertical' | 'horizontal'
 *              step.state = 'upcoming' | 'current' | 'done' | 'failed' | 'skipped'
 *              composition = 'label' | 'label+sub' | 'label+sub+meta' (auto from step shape)
 *              interactivity = 'static' | 'clickable-done' | 'all-clickable'
 *
 * Migration:   `import VerticalStepNavigation from '@common/VerticalStepNavigation'`
 *              `import CustomStepper from '@common/CustomStepper'`
 *              `import NewVerticalStepper from '@common/NewVerticalStepper'`
 *           →  `import { Stepper } from '@components1/ds/Stepper'`
 *
 * Don't (per spec):
 *   - Don't use Stepper to indicate stages of a long-running task — use ProgressLinear.
 *   - Don't allow clicking forward into upcoming steps when prior steps are required.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import RemoveIcon from '@mui/icons-material/Remove';

export type StepperOrientation = 'vertical' | 'horizontal';
export type StepperInteractivity = 'static' | 'clickable-done' | 'all-clickable';
export type StepState = 'upcoming' | 'current' | 'done' | 'failed' | 'skipped';

export interface StepperStep {
  id: string;
  label: React.ReactNode;
  sub?: React.ReactNode;
  meta?: React.ReactNode;
  /** Overrides the default state derived from `current`. Useful for failed/skipped. */
  state?: StepState;
}

export interface StepperProps {
  steps: StepperStep[];
  /** Index of the active step (0-based). Steps before are 'done', after are 'upcoming'. */
  current: number;
  orientation?: StepperOrientation;
  interactivity?: StepperInteractivity;
  onStepClick?: (id: string, index: number) => void;
}

function deriveState(index: number, current: number, override?: StepState): StepState {
  if (override) return override;
  if (index < current) return 'done';
  if (index === current) return 'current';
  return 'upcoming';
}

const STATE_DOT_BG: Record<StepState, string> = {
  upcoming: 'var(--ds-background-100)',
  current: 'var(--ds-blue-500)',
  done: 'var(--ds-blue-500)',
  failed: 'var(--ds-red-500)',
  skipped: 'var(--ds-gray-300)',
};

const STATE_DOT_BORDER: Record<StepState, string> = {
  upcoming: 'var(--ds-gray-300)',
  current: 'var(--ds-blue-500)',
  done: 'var(--ds-blue-500)',
  failed: 'var(--ds-red-500)',
  skipped: 'var(--ds-gray-300)',
};

const STATE_DOT_FG: Record<StepState, string> = {
  upcoming: 'var(--ds-gray-500)',
  current: 'var(--ds-background-100)',
  done: 'var(--ds-background-100)',
  failed: 'var(--ds-background-100)',
  skipped: 'var(--ds-gray-500)',
};

const STATE_LABEL_COLOR: Record<StepState, string> = {
  upcoming: 'var(--ds-gray-500)',
  current: 'var(--ds-gray-700)',
  done: 'var(--ds-gray-700)',
  failed: 'var(--ds-red-600)',
  skipped: 'var(--ds-gray-500)',
};

const STATE_LINE_COLOR: Record<StepState, string> = {
  upcoming: 'var(--ds-gray-200)',
  current: 'var(--ds-gray-200)',
  done: 'var(--ds-blue-500)',
  failed: 'var(--ds-red-500)',
  skipped: 'var(--ds-gray-200)',
};

function isClickable(state: StepState, interactivity: StepperInteractivity): boolean {
  if (interactivity === 'static') return false;
  if (interactivity === 'all-clickable') return true;
  // 'clickable-done': only done and current are clickable
  return state === 'done' || state === 'current';
}

function StepDot({ state, index }: { state: StepState; index: number }) {
  return (
    <Box
      aria-hidden='true'
      sx={{
        width: 24,
        height: 24,
        borderRadius: 'var(--ds-radius-pill)',
        border: `1.5px solid ${STATE_DOT_BORDER[state]}`,
        backgroundColor: STATE_DOT_BG[state],
        color: STATE_DOT_FG[state],
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 'var(--ds-text-caption)',
        fontWeight: 'var(--ds-font-weight-semibold)',
        flexShrink: 0,
        transition: 'all var(--ds-motion-micro) var(--ds-motion-ease)',
      }}
    >
      {state === 'done' && <CheckIcon sx={{ fontSize: 14 }} />}
      {state === 'failed' && <CloseIcon sx={{ fontSize: 14 }} />}
      {state === 'skipped' && <RemoveIcon sx={{ fontSize: 14 }} />}
      {(state === 'upcoming' || state === 'current') && index + 1}
    </Box>
  );
}

function StepContent({ step, state }: { step: StepperStep; state: StepState }) {
  return (
    <Box>
      <Box
        sx={{
          fontSize: 'var(--ds-text-body)',
          fontWeight: state === 'current' ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
          color: STATE_LABEL_COLOR[state],
          lineHeight: 1.3,
        }}
      >
        {step.label}
      </Box>
      {step.sub !== undefined && (
        <Box
          sx={{
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-gray-600)',
            mt: 0.25,
            lineHeight: 1.4,
          }}
        >
          {step.sub}
        </Box>
      )}
      {step.meta !== undefined && <Box sx={{ mt: 0.5, fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{step.meta}</Box>}
    </Box>
  );
}

function VerticalStepper({
  steps,
  current,
  interactivity,
  onStepClick,
}: Required<Pick<StepperProps, 'steps' | 'current' | 'interactivity'>> & Pick<StepperProps, 'onStepClick'>) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column' }}>
      {steps.map((step, i) => {
        const state = deriveState(i, current, step.state);
        const clickable = isClickable(state, interactivity) && !!onStepClick;
        const isLast = i === steps.length - 1;
        return (
          <Box
            key={step.id}
            onClick={clickable ? () => onStepClick!(step.id, i) : undefined}
            sx={{
              display: 'flex',
              gap: 'var(--ds-space-3)',
              cursor: clickable ? 'pointer' : 'default',
              p: clickable ? 'var(--ds-space-1) var(--ds-space-2)' : 'var(--ds-space-1) 0',
              borderRadius: clickable ? 'var(--ds-radius-sm)' : 0,
              '&:hover': clickable ? { backgroundColor: 'var(--ds-gray-100)' } : undefined,
            }}
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', minHeight: 60 }}>
              <StepDot state={state} index={i} />
              {!isLast && (
                <Box
                  sx={{
                    flex: 1,
                    width: '2px',
                    backgroundColor: STATE_LINE_COLOR[state],
                    mt: 0.5,
                    minHeight: 24,
                  }}
                />
              )}
            </Box>
            <Box sx={{ pb: isLast ? 0 : 'var(--ds-space-3)', flex: 1 }}>
              <StepContent step={step} state={state} />
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}

function HorizontalStepper({
  steps,
  current,
  interactivity,
  onStepClick,
}: Required<Pick<StepperProps, 'steps' | 'current' | 'interactivity'>> & Pick<StepperProps, 'onStepClick'>) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'flex-start', width: '100%' }}>
      {steps.map((step, i) => {
        const state = deriveState(i, current, step.state);
        const clickable = isClickable(state, interactivity) && !!onStepClick;
        const isLast = i === steps.length - 1;
        return (
          <React.Fragment key={step.id}>
            <Box
              onClick={clickable ? () => onStepClick!(step.id, i) : undefined}
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                cursor: clickable ? 'pointer' : 'default',
                px: 'var(--ds-space-2)',
                py: 'var(--ds-space-1)',
                borderRadius: clickable ? 'var(--ds-radius-sm)' : 0,
                '&:hover': clickable ? { backgroundColor: 'var(--ds-gray-100)' } : undefined,
                minWidth: 80,
              }}
            >
              <StepDot state={state} index={i} />
              <Box sx={{ mt: 'var(--ds-space-1)', textAlign: 'center' }}>
                <StepContent step={step} state={state} />
              </Box>
            </Box>
            {!isLast && (
              <Box
                aria-hidden='true'
                sx={{
                  flex: 1,
                  height: '2px',
                  backgroundColor: STATE_LINE_COLOR[state],
                  alignSelf: 'flex-start',
                  mt: '11px',
                  minWidth: 16,
                }}
              />
            )}
          </React.Fragment>
        );
      })}
    </Box>
  );
}

export function Stepper({ steps, current, orientation = 'vertical', interactivity = 'static', onStepClick }: StepperProps) {
  if (orientation === 'horizontal') {
    return <HorizontalStepper steps={steps} current={current} interactivity={interactivity} onStepClick={onStepClick} />;
  }
  return <VerticalStepper steps={steps} current={current} interactivity={interactivity} onStepClick={onStepClick} />;
}

export default Stepper;
