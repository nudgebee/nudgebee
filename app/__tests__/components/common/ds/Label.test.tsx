jest.mock('@shared/icons/SafeIcon', () => {
  const Mock = ({ src }: { src: string }) => <span data-testid={`safe-icon-${src}`} />;
  Mock.displayName = 'SafeIcon';
  return { __esModule: true, default: Mock };
});

import React from 'react';
import { render, screen } from '@testing-library/react';
import { Label } from '@ui/Label';

describe('Label', () => {
  it('renders text content', () => {
    render(<Label text='Production' />);
    expect(screen.getByText('Production')).toBeInTheDocument();
  });

  it('renders children content', () => {
    render(<Label>Running</Label>);
    expect(screen.getByText('Running')).toBeInTheDocument();
  });

  it('renders all tones without crashing', () => {
    const tones = ['neutral', 'info', 'success', 'warning', 'critical'] as const;
    tones.forEach((tone) => {
      const { unmount } = render(<Label text='Test' tone={tone} />);
      unmount();
    });
  });

  it('renders sm size', () => {
    render(<Label text='Small' size='sm' />);
    expect(screen.getByText('Small')).toBeInTheDocument();
  });

  it('renders md size', () => {
    render(<Label text='Medium' size='md' />);
    expect(screen.getByText('Medium')).toBeInTheDocument();
  });

  it('renders with dot indicator', () => {
    const { container } = render(<Label text='Active' dot />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders with icon ReactNode', () => {
    render(<Label text='With Icon' icon={<span data-testid='label-icon'>★</span>} />);
    expect(screen.getByTestId('label-icon')).toBeInTheDocument();
  });

  it('renders tooltip when displayTooltip=true', () => {
    render(<Label text='A very long label' displayTooltip />);
    expect(screen.getByText('A very long label')).toBeInTheDocument();
  });
});
