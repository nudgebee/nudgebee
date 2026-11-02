import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import DashboardVariablesModal from '../DashboardVariablesModal';

const setup = (variables: any[] = []) => {
  const onApply = jest.fn();
  render(<DashboardVariablesModal open variables={variables} onClose={jest.fn()} onApply={onApply} />);
  return onApply;
};

describe('the dashboard variables form', () => {
  it('refuses a variable with nothing filled in, and says why', () => {
    const onApply = setup();
    fireEvent.click(screen.getByTestId('dashboard-variable-add-btn'));
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).not.toHaveBeenCalled();
    expect(screen.getAllByRole('alert').length).toBeGreaterThan(0);
  });

  it('hands back an existing variable unchanged', () => {
    const variable = { name: 'scale_set', type: 'query', query: 'q', label_key: 'name' };
    const onApply = setup([variable]);
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).toHaveBeenCalledWith([variable]);
  });

  it('keeps only a fixed list’s values, trimmed', () => {
    const onApply = setup([{ name: 'env', type: 'custom', values: [' dev', '', 'prod '], query: 'stale' }]);
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).toHaveBeenCalledWith([{ name: 'env', type: 'custom', values: ['dev', 'prod'] }]);
  });

  it('removes a variable', () => {
    const onApply = setup([{ name: 'a', type: 'query', query: 'q', label_key: 'l' }]);
    fireEvent.click(screen.getByText('Remove'));
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).toHaveBeenCalledWith([]);
  });

  it('says when no panel uses a variable', () => {
    render(
      <DashboardVariablesModal
        open
        variables={[{ name: 'ns', type: 'custom', values: ['a'] }]}
        panels={[{ title: 'CPU', datasource: 'metrics', targets: [{ ref_id: 'A', expr: 'up' }] } as any]}
        onClose={jest.fn()}
        onApply={jest.fn()}
      />
    );
    expect(screen.getByTestId('dashboard-variable-usage-ns')).toHaveTextContent('No panel uses $ns yet');
  });

  it('lists the panels that take a variable and warns about the ones that only half work', () => {
    render(
      <DashboardVariablesModal
        open
        variables={[{ name: 'ns', type: 'custom', values: ['a'] }]}
        panels={
          [
            { title: 'Good', datasource: 'metrics', targets: [{ ref_id: 'A', expr: 'up{n=~"$ns"}' }] },
            { title: 'Exact', datasource: 'metrics', targets: [{ ref_id: 'A', expr: 'up{n="$ns"}' }] },
            { title: 'Other', datasource: 'metrics', targets: [{ ref_id: 'A', expr: 'up' }] },
          ] as any
        }
        onClose={jest.fn()}
        onApply={jest.fn()}
      />
    );
    const usage = screen.getByTestId('dashboard-variable-usage-ns');
    expect(usage).toHaveTextContent('Used by 2 of 3 panels.');
    expect(usage).toHaveTextContent('Exact: Uses =');
    expect(usage).toHaveTextContent('1 panel doesn’t use it');
  });

  it('keeps an All value', () => {
    const onApply = setup([{ name: 'ns', type: 'custom', values: ['a'], all_value: ' .+ ' }]);
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).toHaveBeenCalledWith([expect.objectContaining({ all_value: '.+' })]);
  });

  it('keeps the table and column of a variable read from the query engine', () => {
    const onApply = setup([{ name: 'status', type: 'nudgebee', table: 'events_v2', column: 'status' }]);
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).toHaveBeenCalledWith([{ name: 'status', type: 'nudgebee', table: 'events_v2', column: 'status' }]);
  });

  it('refuses one with no column', () => {
    const onApply = setup([{ name: 'status', type: 'nudgebee', table: 'events_v2' }]);
    fireEvent.click(screen.getByTestId('dashboard-variables-apply-btn'));
    expect(onApply).not.toHaveBeenCalled();
  });

  it('explains the fields', () => {
    setup([{ name: 'status', type: 'nudgebee', table: 'events_v2', column: 'status' }]);
    expect(screen.getByText(/Use it in panel queries as \$name/)).toBeInTheDocument();
    expect(screen.getByText(/Follows the dashboard’s time range/)).toBeInTheDocument();
  });
});
