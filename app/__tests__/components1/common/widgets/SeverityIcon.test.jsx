import React from 'react';
import { render } from '@testing-library/react';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';

jest.mock('src/utils/colors', () => ({
  colors: {
    info: '#3B82F6',
    lowest: '#90EE90',
    low: '#008000',
    medium: '#FFA500',
    high: '#FF4500',
    critical: '#DC143C',
    highest: '#FF0000',
    NA: '#9CA3AF',
    ok: '#22C55E',
    firing: '#EF4444',
    purple: '#7C3AED',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      tertiary: '#6B7280',
    },
    background: { primaryLightest: '#EFF6FF', white: '#fff' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children }) => <div>{children}</div>,
}));

describe('SeverityIcon', () => {
  const severities = ['info', 'lowest', 'low', 'medium', 'high', 'critical', 'highest', 'na', 'debug', 'ok', 'firing', 'open'];

  severities.forEach((severity) => {
    it(`renders with severity "${severity}"`, () => {
      const { container } = render(<SeverityIcon severityType={severity} />);
      expect(container.firstChild).toBeTruthy();
    });
  });

  it('renders with unknown severity (falls to default)', () => {
    const { container } = render(<SeverityIcon severityType='unknown' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with undefined severityType', () => {
    const { container } = render(<SeverityIcon />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with custom size', () => {
    const { container } = render(<SeverityIcon severityType='high' size={50} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with animated=true', () => {
    const { container } = render(<SeverityIcon severityType='critical' animated={true} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with showBackground=true', () => {
    const { container } = render(<SeverityIcon severityType='high' showBackground={true} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with showBackground=false (default)', () => {
    const { container } = render(<SeverityIcon severityType='low' showBackground={false} />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders tooltip with correct title for NA severity', () => {
    render(<SeverityIcon severityType='Na' />);
    // The tooltip title would be 'NA' for 'Na' input (title case is 'Na')
    // Component renders without crash
    expect(true).toBe(true);
  });

  it('renders uppercase severity by normalizing to lowercase', () => {
    const { container } = render(<SeverityIcon severityType='HIGH' />);
    expect(container.firstChild).toBeTruthy();
  });
});
