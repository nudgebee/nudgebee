import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomTextField from '@shared/forms/CustomTextField';

jest.mock('@utils/colors');

jest.mock('next/router', () => ({ useRouter: jest.fn(() => ({ push: jest.fn(), pathname: '/', asPath: '/' })) }));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

describe('CustomTextField', () => {
  it('renders label text', () => {
    render(<CustomTextField label='Email Address' />);
    expect(screen.getByText('Email Address')).toBeInTheDocument();
  });

  it('renders required asterisk when required=true', () => {
    render(<CustomTextField label='Username' required={true} />);
    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders instructionText', () => {
    render(<CustomTextField label='Name' instructionText='Enter your full name' />);
    expect(screen.getByText('Enter your full name')).toBeInTheDocument();
  });

  it('renders placeholder', () => {
    render(<CustomTextField placeholder='Type here...' />);
    expect(screen.getByPlaceholderText('Type here...')).toBeInTheDocument();
  });

  it('calls onChange when typing', () => {
    const onChange = jest.fn();
    render(<CustomTextField value='' onChange={onChange} />);
    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: 'hello' } });
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it('renders helperText when error=true', () => {
    render(<CustomTextField error={true} helperText='This field is required' />);
    expect(screen.getByText('This field is required')).toBeInTheDocument();
  });

  it('disabled input when disabled=true', () => {
    render(<CustomTextField disabled={true} />);
    const input = screen.getByRole('textbox');
    expect(input).toBeDisabled();
  });

  it('does not render label when label not provided', () => {
    render(<CustomTextField placeholder='No label here' />);
    // No Typography label element should appear with label text
    const input = screen.getByPlaceholderText('No label here');
    expect(input).toBeInTheDocument();
    // Confirm there's no stray label typography
    expect(screen.queryByText(/label/i)).not.toBeInTheDocument();
  });
});
