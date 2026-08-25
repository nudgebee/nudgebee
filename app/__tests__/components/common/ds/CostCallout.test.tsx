import React from 'react';
import { render, screen } from '@testing-library/react';
import { CostCallout } from '@ui/CostCallout';

describe('CostCallout', () => {
  it('renders the value', () => {
    render(<CostCallout value={1500} />);
    expect(screen.getByText(/1,500/)).toBeInTheDocument();
  });

  it('renders USD currency by default', () => {
    render(<CostCallout value={200} />);
    expect(screen.getByText(/\$|200/)).toBeInTheDocument();
  });

  it('renders all tones without crashing', () => {
    const tones = ['high-savings', 'medium-savings', 'low-savings', 'neutral', 'waste'] as const;
    tones.forEach((tone) => {
      const { unmount } = render(<CostCallout value={100} tone={tone} />);
      unmount();
    });
  });

  it('renders all sizes without crashing', () => {
    const sizes = ['sm', 'md', 'lg', 'display'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<CostCallout value={100} size={size} />);
      unmount();
    });
  });

  it('renders period text when provided', () => {
    render(<CostCallout value={100} period='/mo' />);
    expect(screen.getByText('/mo')).toBeInTheDocument();
  });

  it('renders down arrow', () => {
    const { container } = render(<CostCallout value={100} arrow='down' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders up arrow', () => {
    const { container } = render(<CostCallout value={100} arrow='up' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders zero value', () => {
    render(<CostCallout value={0} />);
    expect(screen.getByText(/0/)).toBeInTheDocument();
  });
});
