import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Checkbox } from '@ui/Checkbox';

describe('Checkbox', () => {
  it('renders with a label', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} label='Accept terms' />);
    expect(screen.getByText('Accept terms')).toBeInTheDocument();
  });

  it('renders unchecked by default when checked=false', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} label='Option' />);
    const input = screen.getByRole('checkbox');
    expect(input).not.toBeChecked();
  });

  it('renders checked when checked=true', () => {
    render(<Checkbox checked={true} onChange={jest.fn()} label='Option' />);
    expect(screen.getByRole('checkbox')).toBeChecked();
  });

  it('calls onChange with new checked state', () => {
    const onChange = jest.fn();
    render(<Checkbox checked={false} onChange={onChange} label='Option' />);
    fireEvent.click(screen.getByRole('checkbox'));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('renders as disabled when disabled=true', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} label='Option' disabled />);
    expect(screen.getByRole('checkbox')).toBeDisabled();
  });

  // Regression: `disabled` used to win the fill outright, so a checked+disabled
  // box painted a white tick onto a near-white background and read as UNCHECKED.
  // Read-only surfaces (Tenant Settings for a tenants:Read holder, the built-in
  // role view of the Roles matrix) exist to display state, so this is the case
  // that must not regress.
  it('still shows the ON state when disabled', () => {
    const { container } = render(<Checkbox checked disabled label='On and disabled' onChange={() => {}} />);
    expect(screen.getByRole('checkbox')).toBeChecked();
    const visual = container.querySelector('[aria-hidden="true"]');
    expect(visual).toBeTruthy();
    // Muted, but NOT the empty-box fill.
    expect(visual).toHaveStyle({ backgroundColor: 'var(--ds-gray-500)' });
  });

  it('keeps the empty fill when disabled and unchecked', () => {
    const { container } = render(<Checkbox checked={false} disabled label='Off and disabled' onChange={() => {}} />);
    const visual = container.querySelector('[aria-hidden="true"]');
    expect(visual).toHaveStyle({ backgroundColor: 'var(--ds-background-200)' });
  });

  it('renders with description', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} label='Label' description='Helper text' />);
    expect(screen.getByText('Helper text')).toBeInTheDocument();
  });

  it('renders checkbox-only composition (no label)', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} aria-label='Select row' />);
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  it('renders sm size', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} label='Small' size='sm' />);
    expect(screen.getByText('Small')).toBeInTheDocument();
  });

  it('renders with indeterminate state', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} label='Indeterminate' indeterminate />);
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  it('applies custom id', () => {
    render(<Checkbox checked={false} onChange={jest.fn()} id='my-checkbox' label='Test' />);
    expect(document.getElementById('my-checkbox')).toBeInTheDocument();
  });
});
