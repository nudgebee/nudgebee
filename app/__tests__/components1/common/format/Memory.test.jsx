import React from 'react';
import { render, screen } from '@testing-library/react';
import Memory from '@components1/common/format/Memory';

// Mock @lib/formatter
jest.mock('@lib/formatter', () => ({
  formatMemory: jest.fn((value, sourceUnit, targetUnit) => {
    return `${value}-${sourceUnit}-${targetUnit}`;
  }),
}));

// Mock colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#666',
      secondaryDark: '#333',
    },
  },
}));

import { formatMemory } from '@lib/formatter';

describe('Memory component', () => {
  beforeEach(() => {
    formatMemory.mockClear();
  });

  it('renders "-" when value is undefined', () => {
    render(<Memory />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders "-" when value is null', () => {
    render(<Memory value={null} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders formatted memory value with default units', () => {
    formatMemory.mockReturnValue('1.5');
    render(<Memory value={1610612736} />);
    expect(screen.getByText('1.5')).toBeInTheDocument();
    expect(formatMemory).toHaveBeenCalledWith(1610612736, 'bytes', 'gb', false);
  });

  it('renders suffix (GB) when suffix=true (default)', () => {
    formatMemory.mockReturnValue('2');
    render(<Memory value={2147483648} />);
    expect(screen.getByText('GB')).toBeInTheDocument();
  });

  it('does not render suffix when suffix=false', () => {
    formatMemory.mockReturnValue('2');
    render(<Memory value={2147483648} suffix={false} />);
    expect(screen.queryByText('GB')).not.toBeInTheDocument();
  });

  it('renders with custom sourceUnit and targetUnit', () => {
    formatMemory.mockReturnValue('512');
    render(<Memory value={524288} sourceUnit='kb' targetUnit='mb' />);
    expect(formatMemory).toHaveBeenCalledWith(524288, 'kb', 'mb', false);
    expect(screen.getByText('MB')).toBeInTheDocument();
  });

  it('renders custom sx styling', () => {
    formatMemory.mockReturnValue('1');
    render(<Memory value={1073741824} sx={{ fontSize: '16px' }} />);
    expect(screen.getByText('1')).toBeInTheDocument();
  });

  it('renders suffixSx styling', () => {
    formatMemory.mockReturnValue('1');
    render(<Memory value={1073741824} suffixSx={{ color: 'blue' }} />);
    expect(screen.getByText('GB')).toBeInTheDocument();
  });

  it('renders with targetUnit uppercased in suffix', () => {
    formatMemory.mockReturnValue('100');
    render(<Memory value={104857600} sourceUnit='bytes' targetUnit='mb' />);
    expect(screen.getByText('MB')).toBeInTheDocument();
  });
});
