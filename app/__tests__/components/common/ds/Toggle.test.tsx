jest.mock('@shared/icons/SafeIcon', () => {
  const Mock = ({ src }: { src: string }) => <span data-testid={`safe-icon-${src}`} />;
  Mock.displayName = 'SafeIcon';
  return { __esModule: true, default: Mock };
});

import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Toggle } from '@ui/Toggle';

const options = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
];

describe('Toggle', () => {
  it('renders all option labels', () => {
    render(<Toggle options={options} activeValue='day' onChange={jest.fn()} />);
    expect(screen.getByText('Day')).toBeInTheDocument();
    expect(screen.getByText('Week')).toBeInTheDocument();
    expect(screen.getByText('Month')).toBeInTheDocument();
  });

  it('marks the active option with aria-selected=true', () => {
    render(<Toggle options={options} activeValue='week' onChange={jest.fn()} />);
    expect(screen.getByRole('tab', { name: 'Week' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'Day' })).toHaveAttribute('aria-selected', 'false');
  });

  it('calls onChange with the clicked option value', () => {
    const onChange = jest.fn();
    render(<Toggle options={options} activeValue='day' onChange={onChange} />);
    fireEvent.click(screen.getByText('Month'));
    expect(onChange).toHaveBeenCalledWith('month');
  });

  it('does not call onChange for disabled option', () => {
    const onChange = jest.fn();
    const withDisabled = [...options, { value: 'year', label: 'Year', disabled: true }];
    render(<Toggle options={withDisabled} activeValue='day' onChange={onChange} />);
    fireEvent.click(screen.getByText('Year'));
    expect(onChange).not.toHaveBeenCalled();
  });

  it('renders with src-based icon option via SafeIcon', () => {
    const withIcon = [{ value: 'grid', label: 'Grid', icon: 'GridViewIcon' }];
    render(<Toggle options={withIcon} activeValue='grid' onChange={jest.fn()} />);
    expect(screen.getByTestId('safe-icon-GridViewIcon')).toBeInTheDocument();
  });

  it('renders with React element icon', () => {
    const withIcon = [{ value: 'star', label: 'Star', icon: <span data-testid='react-icon'>★</span> }];
    render(<Toggle options={withIcon} activeValue='star' onChange={jest.fn()} />);
    expect(screen.getByTestId('react-icon')).toBeInTheDocument();
  });

  it('renders large size without crashing', () => {
    render(<Toggle options={options} activeValue='day' onChange={jest.fn()} size='large' />);
    expect(screen.getByText('Day')).toBeInTheDocument();
  });

  it('renders sm size without crashing', () => {
    render(<Toggle options={options} activeValue='day' onChange={jest.fn()} size='sm' />);
    expect(screen.getByText('Day')).toBeInTheDocument();
  });

  it('renders noShadow variant without crashing', () => {
    render(<Toggle options={options} activeValue='day' onChange={jest.fn()} noShadow />);
    expect(screen.getByText('Day')).toBeInTheDocument();
  });

  it('renders with role=tablist container', () => {
    render(<Toggle options={options} activeValue='day' onChange={jest.fn()} />);
    expect(screen.getByRole('tablist')).toBeInTheDocument();
  });
});
