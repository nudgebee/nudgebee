import React from 'react';
import { render, screen } from '@testing-library/react';
import NewIssueChip from '@components1/common/widgets/NewIssueChip';

describe('NewIssueChip', () => {
  it('renders NEW chip label', () => {
    render(<NewIssueChip firstSeenAt='2024-01-01T00:00:00Z' />);
    expect(screen.getByText('NEW')).toBeInTheDocument();
  });

  it('renders with firstSeenAt provided', () => {
    render(<NewIssueChip firstSeenAt='2024-03-15T10:30:00Z' />);
    expect(screen.getByText('NEW')).toBeInTheDocument();
  });

  it('renders with null firstSeenAt', () => {
    render(<NewIssueChip firstSeenAt={null} />);
    expect(screen.getByText('NEW')).toBeInTheDocument();
  });

  it('renders with undefined firstSeenAt', () => {
    render(<NewIssueChip />);
    expect(screen.getByText('NEW')).toBeInTheDocument();
  });
});
