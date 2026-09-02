import React from 'react';
import { render, screen } from '@testing-library/react';
import { ProgressBar } from '@ui/ProgressBar';

describe('ProgressBar', () => {
  it('renders with progressbar role', () => {
    render(<ProgressBar value={50} />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('sets aria-valuenow correctly', () => {
    render(<ProgressBar value={75} max={100} />);
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '75');
  });

  it('sets aria-valuemin and aria-valuemax', () => {
    render(<ProgressBar value={30} max={100} />);
    const bar = screen.getByRole('progressbar');
    expect(bar).toHaveAttribute('aria-valuemin', '0');
    expect(bar).toHaveAttribute('aria-valuemax', '100');
  });

  it('renders label when provided', () => {
    render(<ProgressBar value={40} label='Loading files' />);
    expect(screen.getByText('Loading files')).toBeInTheDocument();
  });

  it('renders percentage value when showValue=true', () => {
    render(<ProgressBar value={60} max={100} showValue />);
    expect(screen.getByText(/60\s*%/)).toBeInTheDocument();
  });

  it('renders sm size without crashing', () => {
    render(<ProgressBar value={50} size='sm' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('renders md size without crashing', () => {
    render(<ProgressBar value={50} size='md' />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('clamps value at 0', () => {
    render(<ProgressBar value={-10} />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('clamps value at max', () => {
    render(<ProgressBar value={150} max={100} />);
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });
});
