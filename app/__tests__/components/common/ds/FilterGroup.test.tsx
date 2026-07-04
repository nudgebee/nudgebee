import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { FilterGroup } from '@ui/FilterGroup';

const filters = [
  { id: 'f1', label: 'severity = high' },
  { id: 'f2', label: 'namespace = default' },
];

const availableFilters = [
  { id: 'severity', label: 'Severity' },
  { id: 'namespace', label: 'Namespace' },
];

describe('FilterGroup', () => {
  it('renders all active filter chips', () => {
    render(<FilterGroup filters={filters} onRemove={jest.fn()} />);
    expect(screen.getByText('severity = high')).toBeInTheDocument();
    expect(screen.getByText('namespace = default')).toBeInTheDocument();
  });

  it('calls onRemove when dismiss button clicked', () => {
    const onRemove = jest.fn();
    render(<FilterGroup filters={filters} onRemove={onRemove} />);
    fireEvent.click(screen.getByRole('button', { name: /remove filter severity = high/i }));
    expect(onRemove).toHaveBeenCalledWith(filters[0]);
  });

  it('shows Filters button when onAdd and availableFilters are provided', () => {
    render(<FilterGroup filters={filters} onRemove={jest.fn()} onAdd={jest.fn()} availableFilters={availableFilters} />);
    expect(screen.getByText('Filters')).toBeInTheDocument();
  });

  it('opens filter menu when Filters button is clicked', () => {
    render(<FilterGroup filters={filters} onRemove={jest.fn()} onAdd={jest.fn()} availableFilters={availableFilters} />);
    fireEvent.click(screen.getByText('Filters'));
    expect(screen.getByText('Severity')).toBeInTheDocument();
    expect(screen.getByText('Namespace')).toBeInTheDocument();
  });

  it('calls onAdd when filter menu item is selected', () => {
    const onAdd = jest.fn();
    render(<FilterGroup filters={[]} onRemove={jest.fn()} onAdd={onAdd} availableFilters={availableFilters} />);
    fireEvent.click(screen.getByText('Filters'));
    fireEvent.click(screen.getByText('Severity'));
    expect(onAdd).toHaveBeenCalledWith(availableFilters[0]);
  });

  it('shows Clear all when onClear and filters exist', () => {
    render(<FilterGroup filters={filters} onRemove={jest.fn()} onClear={jest.fn()} />);
    expect(screen.getByText('Clear all')).toBeInTheDocument();
  });

  it('does not show Clear all when no filters are active', () => {
    render(<FilterGroup filters={[]} onRemove={jest.fn()} onClear={jest.fn()} />);
    expect(screen.queryByText('Clear all')).not.toBeInTheDocument();
  });

  it('calls onClear when Clear all is clicked', () => {
    const onClear = jest.fn();
    render(<FilterGroup filters={filters} onRemove={jest.fn()} onClear={onClear} />);
    fireEvent.click(screen.getByText('Clear all'));
    expect(onClear).toHaveBeenCalledTimes(1);
  });
});
