import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { EmptyState } from '@ui/EmptyState';

describe('EmptyState', () => {
  it('renders title', () => {
    render(<EmptyState title='No incidents found' />);
    expect(screen.getByText('No incidents found')).toBeInTheDocument();
  });

  it('renders description when provided', () => {
    render(<EmptyState title='Empty' description='Try adjusting your filters.' />);
    expect(screen.getByText('Try adjusting your filters.')).toBeInTheDocument();
  });

  it('renders action button when action is provided', () => {
    const onClick = jest.fn();
    render(<EmptyState title='Empty' action={{ label: 'Create new', onClick }} />);
    fireEvent.click(screen.getByText('Create new'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders all sizes', () => {
    const sizes = ['inline', 'section', 'page'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<EmptyState title='Empty' size={size} />);
      unmount();
    });
  });

  it('renders all illustrations', () => {
    const illustrations = ['none', 'first-time', 'no-results', 'no-permissions', 'clear-skies'] as const;
    illustrations.forEach((ill) => {
      const { unmount } = render(<EmptyState title='Empty' illustration={ill} />);
      unmount();
    });
  });

  it('renders success tone', () => {
    render(<EmptyState title='All clear!' tone='success' />);
    expect(screen.getByText('All clear!')).toBeInTheDocument();
  });

  it('renders custom icon', () => {
    render(<EmptyState title='Custom' icon={<span data-testid='custom-icon'>✓</span>} />);
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  it('has role=status for screen readers', () => {
    render(<EmptyState title='Empty' />);
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('renders as a full-width surface panel', () => {
    render(<EmptyState title='Empty' surface />);
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('renders the action icon when provided', () => {
    const onClick = jest.fn();
    render(<EmptyState title='Empty' action={{ label: 'Open', onClick, icon: <span data-testid='action-icon'>→</span> }} />);
    expect(screen.getByTestId('action-icon')).toBeInTheDocument();
  });

  it('renders children below action', () => {
    render(
      <EmptyState title='Empty'>
        <div>Extra content</div>
      </EmptyState>
    );
    expect(screen.getByText('Extra content')).toBeInTheDocument();
  });
});
