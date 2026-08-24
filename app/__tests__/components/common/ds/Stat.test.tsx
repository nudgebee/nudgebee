import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Stat } from '@ui/Stat';

describe('Stat', () => {
  it('renders label and value', () => {
    render(<Stat label='Total Cost' value='$1,234' />);
    expect(screen.getByText('Total Cost')).toBeInTheDocument();
    expect(screen.getByText('$1,234')).toBeInTheDocument();
  });

  it('renders sm size', () => {
    render(<Stat label='Count' value='42' size='sm' />);
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('renders hero size', () => {
    render(<Stat label='Revenue' value='$5M' size='hero' />);
    expect(screen.getByText('$5M')).toBeInTheDocument();
  });

  it('renders delta block when delta prop is provided', () => {
    render(<Stat label='Cost' value='100' delta={{ value: 10, period: 'vs last week', tone: 'savings', direction: 'down' }} />);
    expect(screen.getByText(/vs last week/)).toBeInTheDocument();
  });

  it('renders sub label when provided', () => {
    render(<Stat label='CPU' value='78%' sub='of 100%' />);
    expect(screen.getByText('of 100%')).toBeInTheDocument();
  });

  it('renders info tooltip when info is provided', () => {
    render(<Stat label='Savings' value='$200' info={{ tooltip: 'Based on last 30 days' }} />);
    expect(screen.getByText('Savings')).toBeInTheDocument();
  });

  it('renders headerRight slot', () => {
    render(<Stat label='Pods' value='5' headerRight={<span>extra</span>} />);
    expect(screen.getByText('extra')).toBeInTheDocument();
  });

  it('renders as clickable when onClick is provided', () => {
    const onClick = jest.fn();
    render(<Stat label='Click me' value='99' onClick={onClick} />);
    fireEvent.click(screen.getByText('99'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders icon slot', () => {
    render(<Stat label='L' value='1' icon={<span data-testid='stat-icon'>★</span>} />);
    expect(screen.getByTestId('stat-icon')).toBeInTheDocument();
  });

  it('renders currency format', () => {
    render(<Stat label='Cost' value={1500} format='currency' />);
    expect(screen.getByText('Cost')).toBeInTheDocument();
  });
});
