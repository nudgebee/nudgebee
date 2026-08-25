import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Input } from '@ui/Input';

describe('Input', () => {
  it('renders with a label', () => {
    render(<Input value='' onChange={jest.fn()} label='Email' />);
    expect(screen.getByText('Email')).toBeInTheDocument();
  });

  it('displays the current value', () => {
    render(<Input value='test@example.com' onChange={jest.fn()} label='Email' />);
    expect(screen.getByDisplayValue('test@example.com')).toBeInTheDocument();
  });

  it('calls onChange with new value on input', () => {
    const onChange = jest.fn();
    render(<Input value='' onChange={onChange} label='Name' />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Alice' } });
    expect(onChange).toHaveBeenCalledWith('Alice');
  });

  it('renders error message when error prop is set', () => {
    render(<Input value='' onChange={jest.fn()} error='Field is required' />);
    expect(screen.getByText('Field is required')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('renders help text when no error', () => {
    render(<Input value='' onChange={jest.fn()} help='Enter your name' />);
    expect(screen.getByText('Enter your name')).toBeInTheDocument();
  });

  it('renders required indicator when required=true', () => {
    render(<Input value='' onChange={jest.fn()} label='Name' required />);
    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('is disabled when disabled=true', () => {
    render(<Input value='' onChange={jest.fn()} label='Name' disabled />);
    expect(screen.getByRole('textbox')).toBeDisabled();
  });

  it('renders prefix and suffix', () => {
    render(<Input value='' onChange={jest.fn()} prefix='https://' suffix='.com' />);
    expect(screen.getByText('https://')).toBeInTheDocument();
    expect(screen.getByText('.com')).toBeInTheDocument();
  });

  it('renders all sizes without crashing', () => {
    const sizes = ['sm', 'md', 'lg'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<Input value='' onChange={jest.fn()} size={size} />);
      unmount();
    });
  });

  it('renders textarea type', () => {
    render(<Input value='' onChange={jest.fn()} type='textarea' label='Notes' />);
    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('renders instruction text', () => {
    render(<Input value='' onChange={jest.fn()} instructionText='Use a strong password' />);
    expect(screen.getByText('Use a strong password')).toBeInTheDocument();
  });

  it('renders with placeholder', () => {
    render(<Input value='' onChange={jest.fn()} placeholder='Enter text...' />);
    expect(screen.getByPlaceholderText('Enter text...')).toBeInTheDocument();
  });
});
