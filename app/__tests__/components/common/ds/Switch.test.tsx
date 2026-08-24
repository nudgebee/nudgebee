import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Switch } from '@ui/Switch';

describe('Switch', () => {
  it('renders with a label', () => {
    render(<Switch checked={false} onChange={jest.fn()} label='Enable notifications' />);
    expect(screen.getByText('Enable notifications')).toBeInTheDocument();
  });

  it('renders unchecked state', () => {
    render(<Switch checked={false} onChange={jest.fn()} label='Toggle' />);
    expect(screen.getByRole('checkbox')).not.toBeChecked();
  });

  it('renders checked state', () => {
    render(<Switch checked={true} onChange={jest.fn()} label='Toggle' />);
    expect(screen.getByRole('checkbox')).toBeChecked();
  });

  it('calls onChange when toggled (passes event and checked=true)', () => {
    const onChange = jest.fn();
    render(<Switch checked={false} onChange={onChange} label='Toggle' />);
    fireEvent.click(screen.getByRole('checkbox'));
    expect(onChange).toHaveBeenCalledWith(expect.anything(), true);
  });

  it('renders as disabled when disabled=true', () => {
    render(<Switch checked={false} onChange={jest.fn()} label='Toggle' disabled />);
    expect(screen.getByRole('checkbox')).toBeDisabled();
  });

  it('renders with description', () => {
    render(<Switch checked={false} onChange={jest.fn()} label='Label' description='Manage alerts' />);
    expect(screen.getByText('Manage alerts')).toBeInTheDocument();
  });

  it('renders sm size', () => {
    render(<Switch checked={false} onChange={jest.fn()} label='Small' size='sm' />);
    expect(screen.getByText('Small')).toBeInTheDocument();
  });

  it('renders without label using aria-label', () => {
    render(<Switch checked={false} onChange={jest.fn()} aria-label='Toggle feature' />);
    expect(screen.getByRole('checkbox', { name: 'Toggle feature' })).toBeInTheDocument();
  });

  it('renders loading state without crashing', () => {
    render(<Switch checked={false} onChange={jest.fn()} label='Loading' loading />);
    expect(screen.getByText('Loading')).toBeInTheDocument();
  });

  it('applies custom id', () => {
    render(<Switch checked={false} onChange={jest.fn()} id='my-switch' label='Switch' />);
    expect(document.getElementById('my-switch')).toBeInTheDocument();
  });
});
