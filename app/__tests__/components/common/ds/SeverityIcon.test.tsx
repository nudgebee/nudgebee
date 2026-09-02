jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import React from 'react';
import { render, screen } from '@testing-library/react';
import { SeverityIcon } from '@ui/SeverityIcon';

describe('SeverityIcon', () => {
  it('renders critical icon with letter C', () => {
    render(<SeverityIcon level='critical' />);
    expect(screen.getByText('C')).toBeInTheDocument();
  });

  it('renders high icon with letter H', () => {
    render(<SeverityIcon level='high' />);
    expect(screen.getByText('H')).toBeInTheDocument();
  });

  it('renders medium icon with letter M', () => {
    render(<SeverityIcon level='medium' />);
    expect(screen.getByText('M')).toBeInTheDocument();
  });

  it('renders low icon with letter L', () => {
    render(<SeverityIcon level='low' />);
    expect(screen.getByText('L')).toBeInTheDocument();
  });

  it('renders info icon (SVG-based) without crashing', () => {
    render(<SeverityIcon level='info' />);
    expect(screen.getByRole('img', { name: 'Info' })).toBeInTheDocument();
  });

  it('renders bar variant without crashing', () => {
    const { container } = render(<SeverityIcon level='critical' variant='bar' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders square variant without crashing', () => {
    const { container } = render(<SeverityIcon level='high' variant='square' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders with count badge', () => {
    render(<SeverityIcon level='critical' count={5} />);
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('renders with label', () => {
    render(<SeverityIcon level='high' label='High severity' />);
    expect(screen.getByText('High severity')).toBeInTheDocument();
  });

  it('renders all sizes without crashing', () => {
    const sizes = [12, 14, 16, 20] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<SeverityIcon level='medium' size={size} />);
      unmount();
    });
  });
});
