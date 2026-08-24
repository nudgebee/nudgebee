import React from 'react';
import { render, screen } from '@testing-library/react';
import { Comparison, ComparisonGroup } from '@ui/Comparison';

describe('Comparison', () => {
  it('renders before and after values', () => {
    render(<Comparison before={{ value: 100, unit: '$' }} after={{ value: 80, unit: '$' }} />);
    expect(screen.getByText(/100/)).toBeInTheDocument();
    expect(screen.getByText(/80/)).toBeInTheDocument();
  });

  it('renders lower-is-better polarity (improvement when value drops)', () => {
    render(<Comparison before={{ value: 100 }} after={{ value: 80 }} polarity='lower-is-better' />);
    expect(screen.getByText(/80/)).toBeInTheDocument();
  });

  it('renders higher-is-better polarity (improvement when value rises)', () => {
    render(<Comparison before={{ value: 80 }} after={{ value: 100 }} polarity='higher-is-better' />);
    expect(screen.getByText(/100/)).toBeInTheDocument();
  });

  it('renders neutral polarity without crashing', () => {
    render(<Comparison before={{ value: 50 }} after={{ value: 50 }} polarity='neutral' />);
    expect(screen.getAllByText(/50/).length).toBeGreaterThanOrEqual(1);
  });

  it('renders inline layout', () => {
    render(<Comparison before={{ value: 10 }} after={{ value: 5 }} layout='inline' />);
    expect(screen.getByText(/10/)).toBeInTheDocument();
  });

  it('renders stacked layout', () => {
    render(<Comparison before={{ value: 10 }} after={{ value: 5 }} layout='stacked' />);
    expect(screen.getByText(/10/)).toBeInTheDocument();
  });
});

describe('ComparisonGroup', () => {
  it('renders multiple Comparison children', () => {
    render(
      <ComparisonGroup>
        <Comparison before={{ value: 100 }} after={{ value: 80 }} />
        <Comparison before={{ value: 200 }} after={{ value: 150 }} />
      </ComparisonGroup>
    );
    expect(screen.getByText(/100/)).toBeInTheDocument();
    expect(screen.getByText(/200/)).toBeInTheDocument();
  });
});
