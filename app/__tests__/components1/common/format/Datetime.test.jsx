import React from 'react';
import { render, screen } from '@testing-library/react';
import Datetime from '@components1/common/format/Datetime';

// Mock @lib/datetime
jest.mock('@lib/datetime', () => ({
  getDateDiff: jest.fn(),
  convertToLocalTime: jest.fn(() => 'Jan 1, 2024, 12:00:00 PM'),
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

import { getDateDiff, convertToLocalTime } from '@lib/datetime';

const BASE_DATE = new Date('2024-01-01T12:00:00Z');

// Helper to build a dateDiff object
const makeDiff = ({ days = 0, hours = 0, minutes = 0, seconds = 0 } = {}) => ({
  days,
  hours,
  minutes,
  seconds,
});

describe('Datetime component', () => {
  beforeEach(() => {
    getDateDiff.mockClear();
    convertToLocalTime.mockClear();
    convertToLocalTime.mockReturnValue('Jan 1, 2024, 12:00:00 PM');
  });

  // ---- No value ----
  it('renders emptyValue when value is null', () => {
    render(<Datetime value={null} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders emptyValue when value is undefined', () => {
    render(<Datetime />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders custom emptyValue when no value', () => {
    render(<Datetime value={null} emptyValue='N/A' />);
    expect(screen.getByText('N/A')).toBeInTheDocument();
  });

  it('renders emptyValue with sxSuffixSecondary=false', () => {
    render(<Datetime value={null} sxSuffixSecondary={false} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  // ---- value as Date object ----
  it('renders past date value as Date object — shows days', () => {
    const pastDate = new Date('2023-12-29T12:00:00Z');
    getDateDiff.mockReturnValue(makeDiff({ days: 3 }));
    render(<Datetime value={pastDate} baseDate={BASE_DATE} />);
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('d')).toBeInTheDocument();
    expect(screen.getByText('ago')).toBeInTheDocument();
  });

  // ---- value as string with timezone ----
  it('renders past date from string with timezone', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 2 }));
    render(<Datetime value='2024-01-01T10:00:00Z' baseDate={BASE_DATE} />);
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
    expect(screen.getByText('ago')).toBeInTheDocument();
  });

  // ---- value as string without timezone (Z appended) ----
  it('renders past date from string without timezone (Z is appended)', () => {
    getDateDiff.mockReturnValue(makeDiff({ minutes: 30 }));
    render(<Datetime value='2024-01-01T11:30:00' baseDate={BASE_DATE} />);
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('m')).toBeInTheDocument();
  });

  // ---- value as number (milliseconds) ----
  it('renders past date from number (ms)', () => {
    const pastMs = new Date('2023-12-31T12:00:00Z').getTime(); // ~1e12 range
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={pastMs} baseDate={BASE_DATE} />);
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('d')).toBeInTheDocument();
  });

  // ---- value as number (nanoseconds > 1e15) ----
  it('renders past date from nanoseconds (> 1e15)', () => {
    const nanos = 1704067200000 * 1e6; // some large ns value
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={nanos} baseDate={BASE_DATE} />);
    expect(getDateDiff).toHaveBeenCalled();
  });

  // ---- value as other type (fallback to new Date(value)) ----
  it('handles other value type via new Date(value)', () => {
    getDateDiff.mockReturnValue(makeDiff({ seconds: 5 }));
    // Pass an object that will result in new Date(value)
    render(<Datetime value={true} baseDate={BASE_DATE} />);
    // Should not throw; getDateDiff is called
    expect(getDateDiff).toHaveBeenCalled();
  });

  // ---- baseDate = null (fallback to new Date()) ----
  it('uses new Date() when baseDate is null', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2023-12-31T12:00:00Z')} baseDate={null} />);
    expect(getDateDiff).toHaveBeenCalled();
  });

  // ---- Just now (no units) ----
  it('renders "Just now" when all diff units are zero (past)', () => {
    getDateDiff.mockReturnValue(makeDiff({}));
    const justNow = new Date('2024-01-01T11:59:59Z');
    render(<Datetime value={justNow} baseDate={BASE_DATE} />);
    expect(screen.getByText('Just now')).toBeInTheDocument();
  });

  // ---- Future date ----
  it('renders "in" prefix for future date', () => {
    const futureDate = new Date('2024-01-02T12:00:00Z');
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={futureDate} baseDate={BASE_DATE} />);
    expect(screen.getByText('in')).toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('d')).toBeInTheDocument();
  });

  // ---- Hours display ----
  it('renders hours with spacing separator', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 5 }));
    const pastDate = new Date('2024-01-01T07:00:00Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} />);
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
  });

  // ---- Minutes display ----
  it('renders minutes', () => {
    getDateDiff.mockReturnValue(makeDiff({ minutes: 45 }));
    const pastDate = new Date('2024-01-01T11:15:00Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} />);
    expect(screen.getByText('45')).toBeInTheDocument();
    expect(screen.getByText('m')).toBeInTheDocument();
  });

  // ---- Seconds display ----
  it('renders seconds', () => {
    getDateDiff.mockReturnValue(makeDiff({ seconds: 30 }));
    const pastDate = new Date('2024-01-01T11:59:30Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} />);
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('s')).toBeInTheDocument();
  });

  // ---- maxLevel=2 — shows 2 units ----
  it('renders two levels with maxLevel=2', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1, hours: 3 }));
    const pastDate = new Date('2023-12-31T09:00:00Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} maxLevel={2} />);
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('d')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
  });

  // ---- suffix with past date (push) ----
  it('renders suffix appended for past date', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 1 }));
    const pastDate = new Date('2024-01-01T11:00:00Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} suffix='ago' />);
    const agos = screen.getAllByText('ago');
    // There will be the regular 'ago' plus the suffix 'ago'
    expect(agos.length).toBeGreaterThan(0);
  });

  // ---- suffix with future date (unshift) ----
  it('renders suffix prepended for future date', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    const futureDate = new Date('2024-01-02T12:00:00Z');
    render(<Datetime value={futureDate} baseDate={BASE_DATE} suffix='from now' />);
    expect(screen.getByText('from now')).toBeInTheDocument();
    expect(screen.getByText('in')).toBeInTheDocument();
  });

  // ---- prefix ----
  it('renders prefix', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 2 }));
    const pastDate = new Date('2023-12-30T12:00:00Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} prefix='since' />);
    expect(screen.getByText('since')).toBeInTheDocument();
  });

  // ---- showTooltip=false ----
  it('renders without tooltip when showTooltip=false', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 2 }));
    const pastDate = new Date('2024-01-01T10:00:00Z');
    render(<Datetime value={pastDate} baseDate={BASE_DATE} showTooltip={false} />);
    expect(convertToLocalTime).not.toHaveBeenCalled();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
    expect(screen.getByText('ago')).toBeInTheDocument();
  });

  // ---- showTooltip=false with future ----
  it('renders future without tooltip when showTooltip=false', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    const futureDate = new Date('2024-01-02T12:00:00Z');
    render(<Datetime value={futureDate} baseDate={BASE_DATE} showTooltip={false} />);
    expect(convertToLocalTime).not.toHaveBeenCalled();
    expect(screen.getByText('in')).toBeInTheDocument();
  });

  // ---- showTooltip=false, just now ----
  it('renders "Just now" without tooltip when showTooltip=false', () => {
    getDateDiff.mockReturnValue(makeDiff({}));
    const justNow = new Date('2024-01-01T11:59:59Z');
    render(<Datetime value={justNow} baseDate={BASE_DATE} showTooltip={false} />);
    expect(screen.getByText('Just now')).toBeInTheDocument();
  });

  // ---- sxSecondary=true ----
  it('renders with sxSecondary=true', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2023-12-31T12:00:00Z')} baseDate={BASE_DATE} sxSecondary={true} />);
    expect(screen.getByText('1')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false ----
  it('renders with sxSuffixSecondary=false', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2023-12-31T12:00:00Z')} baseDate={BASE_DATE} sxSuffixSecondary={false} />);
    expect(screen.getByText('d')).toBeInTheDocument();
  });

  // ---- sxPrefixSecondary=false ----
  it('renders with sxPrefixSecondary=false and prefix', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2023-12-31T12:00:00Z')} baseDate={BASE_DATE} sxPrefixSecondary={false} prefix='Age:' />);
    expect(screen.getByText('Age:')).toBeInTheDocument();
  });

  // ---- days + hours with maxLevel=2 and space separator ----
  it('renders hours with space separator when days are already shown and maxLevel=2', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 2, hours: 4, minutes: 10 }));
    render(<Datetime value={new Date('2023-12-29T08:00:00Z')} baseDate={BASE_DATE} maxLevel={2} />);
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('d')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
    // minutes should NOT be shown (maxLevel=2)
    expect(screen.queryByText('m')).not.toBeInTheDocument();
  });

  // ---- minutes with space separator when hours already shown ----
  it('renders minutes with space separator when hours are already shown', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 1, minutes: 30 }));
    render(<Datetime value={new Date('2024-01-01T10:30:00Z')} baseDate={BASE_DATE} maxLevel={2} />);
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('m')).toBeInTheDocument();
  });

  // ---- seconds with space separator when minutes already shown ----
  it('renders seconds with space separator when minutes are shown', () => {
    getDateDiff.mockReturnValue(makeDiff({ minutes: 5, seconds: 20 }));
    render(<Datetime value={new Date('2024-01-01T11:54:40Z')} baseDate={BASE_DATE} maxLevel={2} />);
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('m')).toBeInTheDocument();
    expect(screen.getByText('20')).toBeInTheDocument();
    expect(screen.getByText('s')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with hours (covers false branch of ternary in spacer/hour unit) ----
  it('renders hours with sxSuffixSecondary=false after days are shown (spacer and hour unit false branch)', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1, hours: 3 }));
    render(<Datetime value={new Date('2023-12-31T09:00:00Z')} baseDate={BASE_DATE} maxLevel={2} sxSuffixSecondary={false} />);
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with minutes (covers false branch in minutes spacer/unit) ----
  it('renders minutes with sxSuffixSecondary=false after hours shown', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 1, minutes: 30 }));
    render(<Datetime value={new Date('2024-01-01T10:30:00Z')} baseDate={BASE_DATE} maxLevel={2} sxSuffixSecondary={false} />);
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('m')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with seconds (covers false branch in seconds spacer/unit) ----
  it('renders seconds with sxSuffixSecondary=false after minutes shown', () => {
    getDateDiff.mockReturnValue(makeDiff({ minutes: 2, seconds: 30 }));
    render(<Datetime value={new Date('2024-01-01T11:57:30Z')} baseDate={BASE_DATE} maxLevel={2} sxSuffixSecondary={false} />);
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('s')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with suffix (past) ----
  it('renders past date suffix with sxSuffixSecondary=false', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 2 }));
    render(<Datetime value={new Date('2024-01-01T10:00:00Z')} baseDate={BASE_DATE} suffix='old' sxSuffixSecondary={false} />);
    expect(screen.getByText('old')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with suffix (future) ----
  it('renders future date suffix with sxSuffixSecondary=false', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2024-01-02T12:00:00Z')} baseDate={BASE_DATE} suffix='soon' sxSuffixSecondary={false} />);
    expect(screen.getByText('soon')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with future date + tooltip (covers 'in' Typography false branch) ----
  it('renders future date "in" with sxSuffixSecondary=false and tooltip', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2024-01-02T12:00:00Z')} baseDate={BASE_DATE} sxSuffixSecondary={false} showTooltip={true} />);
    expect(screen.getByText('in')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with future date + no tooltip ----
  it('renders future date "in" with sxSuffixSecondary=false and no tooltip', () => {
    getDateDiff.mockReturnValue(makeDiff({ days: 1 }));
    render(<Datetime value={new Date('2024-01-02T12:00:00Z')} baseDate={BASE_DATE} sxSuffixSecondary={false} showTooltip={false} />);
    expect(screen.getByText('in')).toBeInTheDocument();
  });

  // ---- sxSuffixSecondary=false with past date + no tooltip (covers 'ago'/'Just now' false branch) ----
  it('renders past date "ago" with sxSuffixSecondary=false and no tooltip', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 1 }));
    render(<Datetime value={new Date('2024-01-01T11:00:00Z')} baseDate={BASE_DATE} sxSuffixSecondary={false} showTooltip={false} />);
    expect(screen.getByText('ago')).toBeInTheDocument();
  });

  // ---- sxSecondary=true with hours (covers sxSecondary true branch at line 152-153) ----
  it('renders hours with sxSecondary=true', () => {
    getDateDiff.mockReturnValue(makeDiff({ hours: 3 }));
    render(<Datetime value={new Date('2024-01-01T09:00:00Z')} baseDate={BASE_DATE} sxSecondary={true} />);
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('h')).toBeInTheDocument();
  });

  // ---- sxSecondary=true with minutes (covers sxSecondary true branch at line 203-204) ----
  it('renders minutes with sxSecondary=true', () => {
    getDateDiff.mockReturnValue(makeDiff({ minutes: 20 }));
    render(<Datetime value={new Date('2024-01-01T11:40:00Z')} baseDate={BASE_DATE} sxSecondary={true} />);
    expect(screen.getByText('20')).toBeInTheDocument();
    expect(screen.getByText('m')).toBeInTheDocument();
  });

  // ---- sxSecondary=true with seconds (covers sxSecondary true branch at line 255-256) ----
  it('renders seconds with sxSecondary=true', () => {
    getDateDiff.mockReturnValue(makeDiff({ seconds: 45 }));
    render(<Datetime value={new Date('2024-01-01T11:59:15Z')} baseDate={BASE_DATE} sxSecondary={true} />);
    expect(screen.getByText('45')).toBeInTheDocument();
    expect(screen.getByText('s')).toBeInTheDocument();
  });
});
