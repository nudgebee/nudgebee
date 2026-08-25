import React from 'react';
import { render, screen } from '@testing-library/react';
import ScheduleBuilder from '@components/workflow/components/ScheduleBuilder';

describe('ScheduleBuilder', () => {
  it('adopts a valid default when the trigger has no cron yet', () => {
    const onChange = jest.fn();
    render(<ScheduleBuilder value='' onChange={onChange} />);
    expect(onChange).toHaveBeenCalledWith('0 9 * * *');
  });

  it('opens an expression the pickers can represent in Builder mode', () => {
    render(<ScheduleBuilder value='30 17 * * 1,3' onChange={jest.fn()} />);
    expect(screen.getByText('Frequency')).toBeInTheDocument();
    expect(screen.getByText('Days of week')).toBeInTheDocument();
    expect(screen.queryByLabelText('Cron Expression')).not.toBeInTheDocument();
  });

  it('opens an expression the pickers cannot represent in Advanced mode, unchanged', () => {
    const onChange = jest.fn();
    render(<ScheduleBuilder value='@every 1h' onChange={onChange} />);
    expect(screen.getByDisplayValue('@every 1h')).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('shows a preview with the description and next runs for a valid expression', () => {
    render(<ScheduleBuilder value='0 9 * * 1-5' onChange={jest.fn()} />);
    const preview = screen.getByTestId('schedule-builder-preview');
    expect(preview).toHaveTextContent('0 9 * * 1-5');
    expect(preview).toHaveTextContent('Monday through Friday');
    expect(preview).toHaveTextContent('Next runs:');
  });

  it('surfaces a parse error and no preview for an invalid expression', () => {
    render(<ScheduleBuilder value='99 * * * *' onChange={jest.fn()} />);
    expect(screen.queryByTestId('schedule-builder-preview')).not.toBeInTheDocument();
    expect(screen.getByText(/expected range 0-59/i)).toBeInTheDocument();
  });
});
