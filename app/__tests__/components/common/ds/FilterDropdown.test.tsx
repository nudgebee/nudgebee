import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import FilterDropdown from '@ui/FilterDropdown';

const options = ['Apple', 'Banana', 'Cherry', 'Date'];

// FilterDropdown uses `onSelect` (not onChange), and search only appears when options.length > 8
const manyOptions = ['Apple', 'Banana', 'Cherry', 'Date', 'Elderberry', 'Fig', 'Grape', 'Honeydew', 'Kiwi'];

describe('FilterDropdown', () => {
  it('renders the trigger button with label', () => {
    render(<FilterDropdown options={options} value={null} onSelect={jest.fn()} label='Fruit' />);
    expect(screen.getByText('Fruit')).toBeInTheDocument();
  });

  it('opens dropdown on trigger click', () => {
    render(<FilterDropdown options={options} value={null} onSelect={jest.fn()} label='Fruit' />);
    fireEvent.click(screen.getByText('Fruit'));
    expect(screen.getByText('Apple')).toBeInTheDocument();
  });

  it('calls onSelect when option selected', () => {
    const onSelect = jest.fn();
    render(<FilterDropdown options={options} value={null} onSelect={onSelect} label='Fruit' />);
    fireEvent.click(screen.getByText('Fruit'));
    fireEvent.click(screen.getByText('Banana'));
    expect(onSelect).toHaveBeenCalled();
  });

  it('renders selected value in trigger', () => {
    render(<FilterDropdown options={options} value='Apple' onSelect={jest.fn()} label='Fruit' />);
    expect(screen.getByText('Apple')).toBeInTheDocument();
  });

  it('leads the trigger with the selected option icon only when asked', () => {
    const iconOptions = [{ label: 'Apple', value: 'a', icon: <span data-testid='opt-icon'>i</span> }];

    const { unmount } = render(<FilterDropdown options={iconOptions} value={iconOptions[0]} onSelect={jest.fn()} label='Fruit' />);
    expect(screen.queryByTestId('opt-icon')).not.toBeInTheDocument();
    unmount();

    render(<FilterDropdown options={iconOptions} value={iconOptions[0]} onSelect={jest.fn()} label='Fruit' showSelectedIcon />);
    expect(screen.getByTestId('opt-icon')).toBeInTheDocument();
  });

  it('keeps the caret instead of a clear control when not clearable', () => {
    const { container } = render(<FilterDropdown options={options} value='Apple' onSelect={jest.fn()} label='Fruit' clearable={false} />);
    // The clear affordance is the only <line>-based svg in the trigger.
    expect(container.querySelectorAll('svg line')).toHaveLength(0);
  });

  it('renders multiple selected values', () => {
    render(<FilterDropdown options={options} multiple value={['Apple', 'Cherry']} onSelect={jest.fn()} label='Fruits' />);
    expect(screen.getByText(/Apple|2/)).toBeInTheDocument();
  });

  it('renders with searchable input when options > 8', () => {
    render(<FilterDropdown options={manyOptions} value={null} onSelect={jest.fn()} label='F' />);
    fireEvent.click(screen.getByText('F'));
    expect(screen.getByPlaceholderText(/search/i)).toBeInTheDocument();
  });

  it('renders with searchable input when freeSolo', () => {
    render(<FilterDropdown options={options} value={null} onSelect={jest.fn()} label='F' freeSolo />);
    fireEvent.click(screen.getByText('F'));
    expect(screen.getByPlaceholderText(/search|type/i)).toBeInTheDocument();
  });

  it('renders object options with label/value shape', () => {
    const objOptions = [
      { label: 'Production', value: 'prod' },
      { label: 'Staging', value: 'stg' },
    ];
    render(<FilterDropdown options={objOptions} value={null} onSelect={jest.fn()} label='Env' />);
    fireEvent.click(screen.getByText('Env'));
    expect(screen.getByText('Production')).toBeInTheDocument();
  });

  it('matches search against opt.searchText, not just the visible label', () => {
    // Short labels, but the full key lives in searchText (KG node-row shape).
    const nodeOptions = Array.from({ length: 9 }, (_, i) => ({
      label: `node-${i}`,
      value: `id-${i}`,
      searchText: `k8s:k8s-dev::Workload:${i === 0 ? 'argocd' : 'nudgebee'}:node-${i}`,
    }));
    render(<FilterDropdown options={nodeOptions} value={null} onSelect={jest.fn()} label='Node' />);
    fireEvent.click(screen.getByText('Node'));
    // Typing a namespace (present only in searchText) filters to the matching row.
    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: 'argocd' } });
    expect(screen.getByText('node-0')).toBeInTheDocument();
    expect(screen.queryByText('node-1')).not.toBeInTheDocument();
  });
});
