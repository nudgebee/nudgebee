import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Select } from '@ui/Select';

const options = [
  { label: 'Apple', value: 'apple' },
  { label: 'Banana', value: 'banana' },
  { label: 'Cherry', value: 'cherry' },
];

// The DS Select renders a <button aria-haspopup="listbox"> trigger (not role="combobox")
// disablePortal defaults to true so the listbox renders in-tree

describe('Select (single)', () => {
  it('renders with label', () => {
    render(<Select options={options} value={null} onChange={jest.fn()} label='Fruit' />);
    expect(screen.getByText('Fruit')).toBeInTheDocument();
  });

  it('renders placeholder when no value selected', () => {
    render(<Select options={options} value={null} onChange={jest.fn()} placeholder='Select fruit' />);
    expect(screen.getByText('Select fruit')).toBeInTheDocument();
  });

  it('renders selected value', () => {
    render(<Select options={options} value='apple' onChange={jest.fn()} />);
    expect(screen.getByText('Apple')).toBeInTheDocument();
  });

  it('renders error message', () => {
    render(<Select options={options} value={null} onChange={jest.fn()} error='Required field' />);
    expect(screen.getByText('Required field')).toBeInTheDocument();
  });

  it('opens dropdown on click', () => {
    render(<Select options={options} value={null} onChange={jest.fn()} />);
    fireEvent.click(screen.getByRole('button'));
    // Options render in-tree (disablePortal=true) — check with hidden:true for MUI wrappers
    expect(screen.getByRole('option', { name: 'Apple', hidden: true })).toBeInTheDocument();
  });

  it('calls onChange when option selected', () => {
    const onChange = jest.fn();
    render(<Select options={options} value={null} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByRole('option', { name: 'Banana', hidden: true }));
    expect(onChange).toHaveBeenCalled();
  });

  it('renders disabled state', () => {
    render(<Select options={options} value={null} onChange={jest.fn()} disabled />);
    expect(screen.getByRole('button')).toBeDisabled();
  });

  it('renders clearable button when value is set', () => {
    render(<Select options={options} value='apple' onChange={jest.fn()} clearable />);
    expect(screen.getByRole('button', { name: /clear/i })).toBeInTheDocument();
  });
});

describe('Select (multiple)', () => {
  it('renders multiple selected values', () => {
    render(<Select options={options} multiple value={['apple', 'banana']} onChange={jest.fn()} />);
    expect(screen.getByText('Apple')).toBeInTheDocument();
    expect(screen.getByText('Banana')).toBeInTheDocument();
  });

  it('opens dropdown on click for multiple select', () => {
    render(<Select options={options} multiple value={[]} onChange={jest.fn()} />);
    // The trigger in multiple mode may not have "Select…" text — find the listbox trigger
    const trigger = screen.getAllByRole('button')[0];
    fireEvent.click(trigger);
    expect(screen.getByRole('option', { name: 'Apple', hidden: true })).toBeInTheDocument();
  });
});
