import React from 'react';
import { render, screen } from '@testing-library/react';
import FailedStepsMark, { failedStepsText, firstFailedStepSummary } from '../FailedStepsMark';

describe('FailedStepsMark', () => {
  it('renders "N step(s) failed" for a run with failed steps', () => {
    const { rerender } = render(<FailedStepsMark info={{ failed_step_count: 1, first_failed_step: 'send-slack-dm' }} />);
    expect(screen.getByTestId('failed-steps-mark')).toHaveTextContent('1 step failed');

    rerender(<FailedStepsMark info={{ failed_step_count: 3 }} />);
    expect(screen.getByTestId('failed-steps-mark')).toHaveTextContent('3 steps failed');
  });

  it('renders nothing for a clean run', () => {
    const { container } = render(<FailedStepsMark info={{ failed_step_count: 0 }} />);
    expect(container).toBeEmptyDOMElement();
    render(<FailedStepsMark info={null} />);
    expect(screen.queryByTestId('failed-steps-mark')).toBeNull();
  });

  it('summarises the first failed step', () => {
    expect(failedStepsText(2)).toBe('2 steps failed');
    expect(firstFailedStepSummary({ failed_step_count: 1, first_failed_step: 'send-slack-dm', first_failed_step_error: 'User not found' })).toBe(
      'send-slack-dm: User not found'
    );
    expect(firstFailedStepSummary({ first_failed_step: 'x' })).toBe('');
  });
});
