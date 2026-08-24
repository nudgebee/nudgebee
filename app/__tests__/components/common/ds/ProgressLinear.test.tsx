import React from 'react';
import { render, screen } from '@testing-library/react';
import { ProgressLinear } from '@ui/ProgressLinear';

describe('ProgressLinear', () => {
  it('renders with progressbar role', () => {
    render(<ProgressLinear mode='indeterminate' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('has aria-busy=true', () => {
    render(<ProgressLinear mode='indeterminate' />);
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-busy', 'true');
  });

  it('renders determinate mode with value', () => {
    render(<ProgressLinear mode='determinate' value={50} total={100} />);
    const bar = screen.getByRole('progressbar');
    expect(bar).toHaveAttribute('aria-valuenow', '50');
  });

  it('renders info tone without crashing', () => {
    render(<ProgressLinear mode='indeterminate' tone='info' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('renders neutral tone without crashing', () => {
    render(<ProgressLinear mode='indeterminate' tone='neutral' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('renders page-top surface', () => {
    render(<ProgressLinear mode='indeterminate' surface='page-top' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('renders section surface', () => {
    render(<ProgressLinear mode='indeterminate' surface='section' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('renders inline surface', () => {
    render(<ProgressLinear mode='indeterminate' surface='inline' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });
});
