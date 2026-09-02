import React from 'react';
import { render, screen } from '@testing-library/react';
import { StatusIndicator } from '@ui/StatusIndicator';

describe('StatusIndicator', () => {
  it('renders label', () => {
    render(<StatusIndicator tone='healthy' label='All systems operational' />);
    expect(screen.getByText('All systems operational')).toBeInTheDocument();
  });

  it('renders all tones without crashing', () => {
    const tones = ['healthy', 'degraded', 'failed', 'pending', 'unknown'] as const;
    tones.forEach((tone) => {
      const { unmount } = render(<StatusIndicator tone={tone} label='Status' />);
      unmount();
    });
  });

  it('renders subtext when provided', () => {
    render(<StatusIndicator tone='degraded' label='Degraded' subtext='2 errors detected' />);
    expect(screen.getByText('2 errors detected')).toBeInTheDocument();
  });

  it('renders sm size', () => {
    render(<StatusIndicator tone='healthy' label='OK' size='sm' />);
    expect(screen.getByText('OK')).toBeInTheDocument();
  });

  it('renders md size', () => {
    render(<StatusIndicator tone='failed' label='Failed' size='md' />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });

  it('renders custom icon when provided', () => {
    render(<StatusIndicator tone='pending' label='Pending' icon={<span data-testid='custom-icon'>⟳</span>} />);
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  it('renders without label (accessible via container)', () => {
    const { container } = render(<StatusIndicator tone='healthy' />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
