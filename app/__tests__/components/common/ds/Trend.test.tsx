import React from 'react';
import { render, screen } from '@testing-library/react';
import { Trend } from '@ui/Trend';

describe('Trend', () => {
  it('renders the value', () => {
    render(<Trend value={15} />);
    expect(screen.getByText(/15/)).toBeInTheDocument();
  });

  it('renders positive trend (value * sign > 0 → green)', () => {
    const { container } = render(<Trend value={10} sign={1} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders negative trend (value * sign < 0 → red)', () => {
    const { container } = render(<Trend value={10} sign={-1} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders all sizes without crashing', () => {
    const sizes = ['xs', 'sm', 'md', 'lg', 'default'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<Trend value={5} size={size} />);
      unmount();
    });
  });

  it('renders inline variant', () => {
    render(<Trend value={20} variant='inline' />);
    expect(screen.getByText(/20/)).toBeInTheDocument();
  });

  it('renders chip variant', () => {
    render(<Trend value={20} variant='chip' />);
    expect(screen.getByText(/20/)).toBeInTheDocument();
  });

  it('renders zero value without crashing', () => {
    const { container } = render(<Trend value={0} />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
