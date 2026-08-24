import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { SourceCitation } from '@ui/SourceCitation';

describe('SourceCitation', () => {
  it('renders label when provided', () => {
    render(<SourceCitation source='prometheus' label='CPU metric' />);
    expect(screen.getByText('CPU metric')).toBeInTheDocument();
  });

  it('renders all known sources without crashing', () => {
    const sources = ['prometheus', 'loki', 'k8s', 'aws', 'gcp', 'github', 'grafana', 'runbook'] as const;
    sources.forEach((source) => {
      const { unmount } = render(<SourceCitation source={source} label='Source' />);
      unmount();
    });
  });

  it('renders unknown string source', () => {
    render(<SourceCitation source='custom-source' label='Custom' />);
    expect(screen.getByText('Custom')).toBeInTheDocument();
  });

  it('renders timestamp relative text when provided', () => {
    render(<SourceCitation source='prometheus' timestamp='2024-01-15' />);
    expect(screen.getByText(/ago|d ago|h ago/)).toBeInTheDocument();
  });

  it('renders number badge when provided', () => {
    render(<SourceCitation source='prometheus' number={3} />);
    expect(screen.getByText(/3/)).toBeInTheDocument();
  });

  it('renders xs size without crashing', () => {
    render(<SourceCitation source='k8s' label='Source' size='xs' />);
    expect(screen.getByText('Source')).toBeInTheDocument();
  });

  it('renders sm size without crashing', () => {
    render(<SourceCitation source='k8s' label='Source' size='sm' />);
    expect(screen.getByText('Source')).toBeInTheDocument();
  });

  it('renders as a link when href is provided', () => {
    render(<SourceCitation source='grafana' label='Dashboard' href='/grafana/d/123' />);
    expect(screen.getByRole('link', { name: /Dashboard/ })).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<SourceCitation source='runbook' label='Runbook' onClick={onClick} />);
    fireEvent.click(screen.getByText('Runbook'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
