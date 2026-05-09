import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomPill from '@components1/common/CustomPill';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      tertiary: '#6B7280',
      primaryLight: '#60A5FA',
      secondaryDark: '#1F2937',
    },
    background: { primaryLightest: '#EFF6FF', white: '#fff' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    primary: '#3B82F6',
  },
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children }) => <>{children}</>,
}));

describe('CustomPill', () => {
  it('renders the value text', () => {
    render(<CustomPill value={5} />);
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('renders "99+" when value is greater than 99', () => {
    render(<CustomPill value={100} />);
    expect(screen.getByText('99+')).toBeInTheDocument();
  });

  it('renders "99+" when value is 200', () => {
    render(<CustomPill value={200} />);
    expect(screen.getByText('99+')).toBeInTheDocument();
  });

  it('renders "99" as text when value is exactly 99 (not 99+)', () => {
    render(<CustomPill value={99} />);
    expect(screen.getByText('99')).toBeInTheDocument();
    expect(screen.queryByText('99+')).not.toBeInTheDocument();
  });

  it('renders exact value when value is less than 99', () => {
    render(<CustomPill value={42} />);
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('renders value=0', () => {
    render(<CustomPill value={0} />);
    expect(screen.getByText('0')).toBeInTheDocument();
  });

  it('renders value=1', () => {
    render(<CustomPill value={1} />);
    expect(screen.getByText('1')).toBeInTheDocument();
  });

  it('renders string values', () => {
    render(<CustomPill value='active' />);
    expect(screen.getByText('active')).toBeInTheDocument();
  });
});
