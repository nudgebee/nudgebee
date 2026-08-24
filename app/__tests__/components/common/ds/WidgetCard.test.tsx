import React from 'react';
import { render, screen } from '@testing-library/react';
import WidgetCard from '@ui/WidgetCard';

describe('WidgetCard', () => {
  it('renders children', () => {
    render(<WidgetCard>Card content</WidgetCard>);
    expect(screen.getByText('Card content')).toBeInTheDocument();
  });

  it('renders without children', () => {
    const { container } = render(<WidgetCard />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('applies custom sx prop', () => {
    const { container } = render(<WidgetCard sx={{ padding: '10px' }}>test</WidgetCard>);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('forwards additional Box props', () => {
    render(<WidgetCard data-testid='widget-card'>content</WidgetCard>);
    expect(screen.getByTestId('widget-card')).toBeInTheDocument();
  });

  it('renders multiple children', () => {
    render(
      <WidgetCard>
        <span>child 1</span>
        <span>child 2</span>
      </WidgetCard>
    );
    expect(screen.getByText('child 1')).toBeInTheDocument();
    expect(screen.getByText('child 2')).toBeInTheDocument();
  });
});
