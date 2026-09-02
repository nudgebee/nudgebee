jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { ToggleGroup } from '@ui/ToggleGroup';

const options = [
  { value: 'a', label: 'Option A', ariaLabel: 'Option A' },
  { value: 'b', label: 'Option B', ariaLabel: 'Option B' },
  { value: 'c', label: 'Option C', ariaLabel: 'Option C', disabled: true },
];

describe('ToggleGroup (single)', () => {
  it('renders all options', () => {
    render(<ToggleGroup options={options} selection='single' value='a' onChange={jest.fn()} />);
    expect(screen.getByText('Option A')).toBeInTheDocument();
    expect(screen.getByText('Option B')).toBeInTheDocument();
    expect(screen.getByText('Option C')).toBeInTheDocument();
  });

  it('renders group role', () => {
    render(<ToggleGroup options={options} selection='single' value='a' onChange={jest.fn()} />);
    expect(screen.getByRole('group')).toBeInTheDocument();
  });

  it('marks active option with aria-checked=true', () => {
    render(<ToggleGroup options={options} selection='single' value='b' onChange={jest.fn()} />);
    expect(screen.getByRole('radio', { name: 'Option B' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: 'Option A' })).toHaveAttribute('aria-checked', 'false');
  });

  it('calls onChange when option is clicked', () => {
    const onChange = jest.fn();
    render(<ToggleGroup options={options} selection='single' value='a' onChange={onChange} />);
    fireEvent.click(screen.getByRole('radio', { name: 'Option B' }));
    expect(onChange).toHaveBeenCalledWith('b');
  });

  it('does not call onChange for disabled option', () => {
    const onChange = jest.fn();
    render(<ToggleGroup options={options} selection='single' value='a' onChange={onChange} />);
    fireEvent.click(screen.getByRole('radio', { name: 'Option C' }));
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe('ToggleGroup (multiple)', () => {
  it('marks multiple selected options with aria-checked=true', () => {
    render(<ToggleGroup options={options} selection='multiple' value={['a', 'b']} onChange={jest.fn()} />);
    expect(screen.getByRole('checkbox', { name: 'Option A' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('checkbox', { name: 'Option B' })).toHaveAttribute('aria-checked', 'true');
  });

  it('calls onChange with toggled values array when new item clicked', () => {
    const onChange = jest.fn();
    render(<ToggleGroup options={options} selection='multiple' value={['a']} onChange={onChange} />);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Option B' }));
    expect(onChange).toHaveBeenCalledWith(['a', 'b']);
  });

  it('removes value when already selected item is clicked', () => {
    const onChange = jest.fn();
    render(<ToggleGroup options={options} selection='multiple' value={['a', 'b']} onChange={onChange} />);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Option A' }));
    expect(onChange).toHaveBeenCalledWith(['b']);
  });
});
