import React from 'react';
import { render, screen } from '@testing-library/react';
import NumberComponent from '@components1/common/format/Number';

// Mock @lib/formatter
jest.mock('@lib/formatter', () => ({
  formatNumber: jest.fn((value, defaultVal = '-') => {
    const num = typeof value === 'string' ? parseFloat(value) : value;
    if (!isFinite(num) || value === null || value === '' || value === 0) return defaultVal;
    return num.toLocaleString('en-US');
  }),
}));

import { formatNumber } from '@lib/formatter';

describe('NumberComponent', () => {
  beforeEach(() => {
    formatNumber.mockClear();
  });

  it('renders formatted number using formatNumber', () => {
    formatNumber.mockReturnValue('1,234');
    render(<NumberComponent value={1234} />);
    expect(screen.getByText('1,234')).toBeInTheDocument();
    expect(formatNumber).toHaveBeenCalledWith(1234, '-', 0, 2);
  });

  it('renders defaultVal when value yields default', () => {
    formatNumber.mockReturnValue('-');
    render(<NumberComponent value={0} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders custom defaultVal', () => {
    formatNumber.mockReturnValue('N/A');
    render(<NumberComponent value={null} defaultVal='N/A' />);
    expect(formatNumber).toHaveBeenCalledWith(null, 'N/A', 0, 2);
  });

  it('renders with custom minimumFractionDigits and maximumFractionDigits', () => {
    formatNumber.mockReturnValue('1,234.50');
    render(<NumberComponent value={1234.5} minimumFractionDigits={2} maximumFractionDigits={2} />);
    expect(formatNumber).toHaveBeenCalledWith(1234.5, '-', 2, 2);
    expect(screen.getByText('1,234.50')).toBeInTheDocument();
  });

  it('renders suffix when provided', () => {
    formatNumber.mockReturnValue('100');
    render(<NumberComponent value={100} suffix='ms' />);
    expect(screen.getByText('ms')).toBeInTheDocument();
  });

  it('does not render suffix when suffix is empty string', () => {
    formatNumber.mockReturnValue('100');
    render(<NumberComponent value={100} />);
    expect(screen.queryByText('ms')).not.toBeInTheDocument();
  });

  it('renders with custom sx', () => {
    formatNumber.mockReturnValue('500');
    render(<NumberComponent value={500} sx={{ fontSize: '16px', color: 'blue' }} />);
    expect(screen.getByText('500')).toBeInTheDocument();
  });

  it('renders with custom suffixSx', () => {
    formatNumber.mockReturnValue('500');
    render(<NumberComponent value={500} suffix='%' suffixSx={{ fontSize: '10px' }} />);
    expect(screen.getByText('%')).toBeInTheDocument();
  });

  it('uses Tooltip wrapping (value appears as tooltip title)', () => {
    formatNumber.mockReturnValue('42');
    render(<NumberComponent value={42} />);
    // The Tooltip is rendered — value shows as formatted text
    expect(screen.getByText('42')).toBeInTheDocument();
  });
});
