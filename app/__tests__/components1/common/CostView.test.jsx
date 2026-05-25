import React from 'react';
import { render, screen } from '@testing-library/react';
import CostView from '@components1/common/CostView';

jest.mock('@components1/common/widgets/TrendArrowPercentage', () => ({
  __esModule: true,
  default: ({ sign, value }) => (
    <span data-testid='trend-arrow'>
      {sign > 0 ? 'up' : 'down'} {value}
    </span>
  ),
}));

jest.mock('@components1/common/format/Currency', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='currency'>{value}</span>,
}));

describe('CostView', () => {
  test('renders "No cost data provided." when data is null', () => {
    render(<CostView data={null} />);
    expect(screen.getByText('No cost data provided.')).toBeInTheDocument();
  });

  test('renders "No cost data provided." when data is empty array', () => {
    render(<CostView data={[]} />);
    expect(screen.getByText('No cost data provided.')).toBeInTheDocument();
  });

  test('renders cost entries for each item in data', () => {
    const data = [
      { name: 'Current Month', cost: 100 },
      { name: 'Last Month', cost: 120 },
      { name: 'Forecast Month', cost: 90 },
    ];
    render(<CostView data={data} />);
    const currencies = screen.getAllByTestId('currency');
    expect(currencies.length).toBe(3);
  });

  test('renders entry name', () => {
    const data = [
      { name: 'Current Month', cost: 100 },
      { name: 'Last Month', cost: 120 },
    ];
    render(<CostView data={data} />);
    expect(screen.getByText('Current Month')).toBeInTheDocument();
    expect(screen.getByText('Last Month')).toBeInTheDocument();
  });

  test('renders Currency component for each entry', () => {
    const data = [
      { name: 'Current Month', cost: 150 },
      { name: 'Last Month', cost: 200 },
    ];
    render(<CostView data={data} />);
    const currencies = screen.getAllByTestId('currency');
    expect(currencies[0].textContent).toBe('150');
    expect(currencies[1].textContent).toBe('200');
  });

  test('renders TrendArrowPercentage for "Forecast Month" entry', () => {
    const data = [
      { name: 'Current Month', cost: 100 },
      { name: 'Last Month', cost: 120 },
      { name: 'Forecast Month', cost: 90 },
    ];
    render(<CostView data={data} />);
    expect(screen.getByTestId('trend-arrow')).toBeInTheDocument();
  });
});
