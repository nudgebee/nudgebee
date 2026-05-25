import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import SecondaryLink from '@components1/common/SecondaryLink';

describe('SecondaryLink', () => {
  it('renders children text', () => {
    render(<SecondaryLink>View details</SecondaryLink>);
    expect(screen.getByText('View details')).toBeInTheDocument();
  });

  it('renders children as React nodes', () => {
    render(
      <SecondaryLink>
        <span data-testid='child-node'>Node</span>
      </SecondaryLink>
    );
    expect(screen.getByTestId('child-node')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<SecondaryLink onClick={onClick}>Click me</SecondaryLink>);
    fireEvent.click(screen.getByText('Click me'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders without onClick without crashing', () => {
    render(<SecondaryLink>No handler</SecondaryLink>);
    const el = screen.getByText('No handler');
    expect(el).toBeInTheDocument();
    fireEvent.click(el);
  });

  it('renders without style prop without crashing', () => {
    render(<SecondaryLink>No style</SecondaryLink>);
    expect(screen.getByText('No style')).toBeInTheDocument();
  });

  it('renders multiple children', () => {
    render(
      <SecondaryLink>
        <span>Count:</span>
        <span className='count'>5</span>
      </SecondaryLink>
    );
    expect(screen.getByText('Count:')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
  });
});
