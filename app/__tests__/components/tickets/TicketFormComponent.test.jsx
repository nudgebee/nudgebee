import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import TicketFormComponent from '@components/tickets/TicketFormComponent';

jest.mock('@api1/tickets', () => ({ __esModule: true, default: { getTicketFieldValues: jest.fn() } }));
jest.mock('@shared/widgets/CustomDateTimePicker', () => () => null);
jest.mock('@ui/Input', () => ({ Input: () => null }));
// Stand-in for the DS Select: one click emits a fixed selection so the assertions
// cover handleChange's encoding rather than the primitive's option plumbing.
jest.mock('@ui/Select', () => ({
  Select: ({ id, multiple, onChange }) => (
    <button data-testid={`select-${id}`} onClick={() => onChange(multiple ? ['10023', '10024'] : '10023')}>
      {id}
    </button>
  ),
}));

const renderForm = (fields) => {
  const onChanges = jest.fn();
  render(<TicketFormComponent fields={fields} initialValues={{}} onChanges={onChanges} configurationId='cfg-1' />);
  return onChanges;
};

describe('TicketFormComponent value encoding', () => {
  it('emits the raw option id for a Jira custom select, leaving the {id} wrap to ticket-server', () => {
    const onChanges = renderForm({
      customfield_100: { key: 'customfield_100', name: 'Team', type: 'select' },
    });
    fireEvent.click(screen.getByTestId('select-customfield_100'));
    expect(onChanges).toHaveBeenCalledWith({ customfield_100: '10023' });
  });

  it('emits the raw option id for a system select too', () => {
    const onChanges = renderForm({
      priority: { key: 'priority', name: 'Priority', type: 'select' },
    });
    fireEvent.click(screen.getByTestId('select-priority'));
    expect(onChanges).toHaveBeenCalledWith({ priority: '10023' });
  });

  it('keeps the tool-agnostic {id} wrap on multiselect values', () => {
    const onChanges = renderForm({
      customfield_200: { key: 'customfield_200', name: 'Options', type: 'multiselect' },
    });
    fireEvent.click(screen.getByTestId('select-customfield_200'));
    expect(onChanges).toHaveBeenCalledWith({ customfield_200: [{ id: '10023' }, { id: '10024' }] });
  });

  it('passes free-string array values through unchanged', () => {
    const onChanges = renderForm({
      labels: { key: 'labels', name: 'Labels', type: 'array' },
    });
    fireEvent.click(screen.getByTestId('select-labels'));
    expect(onChanges).toHaveBeenCalledWith({ labels: ['10023', '10024'] });
  });
});
