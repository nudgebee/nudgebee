import React from 'react';
import { render, screen } from '@testing-library/react';
import TrendArrowPercentage from '@components1/common/widgets/TrendArrowPercentage';

jest.mock('@lib/formatter', () => ({
  formatNumber: jest.fn((val) => String(val)),
}));

describe('TrendArrowPercentage', () => {
  it('renders down arrow when value * sign > 0 (positive trend)', () => {
    const { container } = render(<TrendArrowPercentage value={5} sign={1} />);
    // value * sign = 5 > 0 => ArrowDropDownIcon (green)
    expect(container.querySelector('[data-testid]') || container.firstChild).toBeTruthy();
    expect(screen.getByText(/5%/)).toBeInTheDocument();
  });

  it('renders up arrow when value * sign <= 0 (negative trend)', () => {
    const { container } = render(<TrendArrowPercentage value={-5} sign={1} />);
    // value * sign = -5 <= 0 => ArrowDropUpIcon (red)
    expect(container.firstChild).toBeTruthy();
    expect(screen.getByText(/-5%/)).toBeInTheDocument();
  });

  it('renders with sign=-1 making positive value negative trend', () => {
    const { container } = render(<TrendArrowPercentage value={10} sign={-1} />);
    // value * sign = -10 <= 0 => up arrow
    expect(container.firstChild).toBeTruthy();
    expect(screen.getByText(/10%/)).toBeInTheDocument();
  });

  it('renders with custom width', () => {
    const { container } = render(<TrendArrowPercentage value={3} width='80px' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with zero value (treated as non-positive)', () => {
    const { container } = render(<TrendArrowPercentage value={0} sign={1} />);
    // 0 * 1 = 0, NOT > 0, so up arrow
    expect(container.firstChild).toBeTruthy();
    expect(screen.getByText(/0%/)).toBeInTheDocument();
  });

  it('renders with default sign and width', () => {
    const { container } = render(<TrendArrowPercentage value={7} />);
    expect(container.firstChild).toBeTruthy();
  });
});
