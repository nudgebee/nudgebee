import React from 'react';
import { WarningAmberRounded } from '@mui/icons-material';
import { Label, type LabelSize } from '@ui/Label';
import Tooltip from '@ui/Tooltip';

/** Server-recorded mark for a COMPLETED run whose continue-on-failure steps failed. */
export interface FailedStepsInfo {
  failed_step_count?: number;
  first_failed_step?: string;
  first_failed_step_error?: string;
}

export const failedStepsText = (count: number): string => `${count} step${count === 1 ? '' : 's'} failed`;

/** "step: error" for the first failed step, or '' when the run carries none. */
export const firstFailedStepSummary = (info?: FailedStepsInfo | null): string => {
  if (!info?.failed_step_count) return '';
  return [info.first_failed_step, info.first_failed_step_error].filter(Boolean).join(': ');
};

interface FailedStepsMarkProps {
  info?: FailedStepsInfo | null;
  size?: LabelSize;
  testId?: string;
}

/**
 * Amber "N steps failed" shown beside a run's status. The run status stays
 * COMPLETED; this only tells the user a step failed under "continue on failure".
 */
const FailedStepsMark: React.FC<FailedStepsMarkProps> = ({ info, size = 'sm', testId = 'failed-steps-mark' }) => {
  const count = info?.failed_step_count || 0;
  if (count <= 0) return null;
  const first = firstFailedStepSummary(info);
  const mark = (
    <span data-testid={testId} style={{ display: 'inline-flex' }}>
      <Label tone='warning' size={size} icon={<WarningAmberRounded />} textTransform='none'>
        {failedStepsText(count)}
      </Label>
    </span>
  );
  if (!first) return mark;
  return (
    <Tooltip title='Run completed with failed steps' variant='explainer' desc={`First failed: ${first}`}>
      {mark}
    </Tooltip>
  );
};

export default FailedStepsMark;
