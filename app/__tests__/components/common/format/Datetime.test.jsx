import React from 'react';
import { render, screen } from '@testing-library/react';
import Datetime from '@shared/format/Datetime';

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='tooltip' data-title={title}>
      {children}
    </div>
  ),
}));

const ONE_SEC = 1_000;
const ONE_MIN = 60_000;
const ONE_HOUR = 60 * ONE_MIN;
const ONE_DAY = 24 * ONE_HOUR;

// Fixed reference point — all relative tests pass this as baseDate so results
// are deterministic regardless of when the suite runs.
const BASE = new Date('2024-06-15T12:00:00Z');
const past = (ms) => new Date(BASE.getTime() - ms);
const future = (ms) => new Date(BASE.getTime() + ms);

// ─── empty / falsy value ──────────────────────────────────────────────────────

describe('Datetime', () => {
  describe('empty / falsy value', () => {
    it('renders "-" when value is null', () => {
      render(<Datetime value={null} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is undefined', () => {
      render(<Datetime value={undefined} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders custom emptyValue when value is falsy', () => {
      render(<Datetime value={null} emptyValue='N/A' />);
      expect(screen.getByText('N/A')).toBeInTheDocument();
    });
  });

  // ─── value parsing ──────────────────────────────────────────────────────────

  describe('value parsing', () => {
    it('accepts a Date object', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('30s')).toBeInTheDocument();
    });

    it('accepts an ISO string with explicit timezone', () => {
      render(<Datetime value={past(30 * ONE_SEC).toISOString()} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('30s')).toBeInTheDocument();
    });

    it('appends Z to a string without timezone marker', () => {
      // '2024-06-15T11:59:30' has no tz → parsed as UTC (Z appended internally)
      render(<Datetime value='2024-06-15T11:59:30' baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('30s')).toBeInTheDocument();
    });

    it('accepts a unix millisecond timestamp', () => {
      render(<Datetime value={past(30 * ONE_SEC).getTime()} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('30s')).toBeInTheDocument();
    });

    it('accepts a nanosecond timestamp (value > 1e15)', () => {
      // 1.5e18 ns ÷ 1e6 = 1.5e12 ms → July 2017
      const nsValue = 1.5e18;
      const parsedMs = Math.floor(nsValue / 1e6);
      const nanosBaseDate = new Date(parsedMs + 5 * ONE_DAY); // 5 days later → absolute display
      render(<Datetime value={nsValue} baseDate={nanosBaseDate} showTooltip={false} />);
      expect(screen.getByText(/Jul/)).toBeInTheDocument();
    });
  });

  // ─── relative time buckets (past) ──────────────────────────────────────────

  describe('relative time — past', () => {
    it('shows "now" for delta < 1 second, without "ago"', () => {
      render(<Datetime value={past(500)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('now')).toBeInTheDocument();
      expect(screen.queryByText('ago')).not.toBeInTheDocument();
    });

    it('shows "Xs ago" for delta in seconds', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('30s')).toBeInTheDocument();
      expect(screen.getByText('ago')).toBeInTheDocument();
    });

    it('shows "Xm ago" for delta in minutes', () => {
      render(<Datetime value={past(5 * ONE_MIN)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('5m')).toBeInTheDocument();
      expect(screen.getByText('ago')).toBeInTheDocument();
    });

    it('shows "Xh ago" when hours have no remainder minutes', () => {
      render(<Datetime value={past(3 * ONE_HOUR)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('3h')).toBeInTheDocument();
      expect(screen.getByText('ago')).toBeInTheDocument();
    });

    it('shows "Xh Ym ago" when hours have a minute remainder', () => {
      render(<Datetime value={past(3 * ONE_HOUR + 30 * ONE_MIN)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('3h 30m')).toBeInTheDocument();
      expect(screen.getByText('ago')).toBeInTheDocument();
    });

    it('shows "Xd ago" for 1-2 day delta', () => {
      render(<Datetime value={past(2 * ONE_DAY)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('2d')).toBeInTheDocument();
      expect(screen.getByText('ago')).toBeInTheDocument();
    });

    it('shows absolute "dd-mmm" date for delta >= 3 days, without "ago"', () => {
      // 3 days before June 15 → still June, format dd-Jun
      render(<Datetime value={past(3 * ONE_DAY)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText(/\d{2}-Jun/)).toBeInTheDocument();
      expect(screen.queryByText('ago')).not.toBeInTheDocument();
    });
  });

  // ─── relative time buckets (future) ────────────────────────────────────────

  describe('relative time — future', () => {
    it('shows "now" for delta < 1 second, without "in"', () => {
      render(<Datetime value={future(500)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('now')).toBeInTheDocument();
      expect(screen.queryByText('in')).not.toBeInTheDocument();
    });

    it('shows "in Xs" for future seconds', () => {
      render(<Datetime value={future(45 * ONE_SEC)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('in')).toBeInTheDocument();
      expect(screen.getByText('45s')).toBeInTheDocument();
      expect(screen.queryByText('ago')).not.toBeInTheDocument();
    });

    it('shows "in Xm" for future minutes', () => {
      render(<Datetime value={future(10 * ONE_MIN)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('in')).toBeInTheDocument();
      expect(screen.getByText('10m')).toBeInTheDocument();
    });

    it('shows "in Xh" for future hours', () => {
      render(<Datetime value={future(2 * ONE_HOUR)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('in')).toBeInTheDocument();
      expect(screen.getByText('2h')).toBeInTheDocument();
    });

    it('shows "in Xd" for future days (< 3)', () => {
      render(<Datetime value={future(ONE_DAY)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText('in')).toBeInTheDocument();
      expect(screen.getByText('1d')).toBeInTheDocument();
    });

    it('shows absolute "dd-mmm" date for future >= 3 days, without "in"', () => {
      // 3 days after June 15 → still June, format dd-Jun
      render(<Datetime value={future(3 * ONE_DAY)} baseDate={BASE} showTooltip={false} />);
      expect(screen.getByText(/\d{2}-Jun/)).toBeInTheDocument();
      expect(screen.queryByText('in')).not.toBeInTheDocument();
    });
  });

  // ─── tooltip ────────────────────────────────────────────────────────────────

  describe('tooltip', () => {
    it('wraps content in a Tooltip by default', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} />);
      expect(screen.getByTestId('tooltip')).toBeInTheDocument();
    });

    it('does not render Tooltip when showTooltip=false', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} showTooltip={false} />);
      expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
    });

    it('tooltip title follows "HH:MM TZ, DD-MMM" format', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} />);
      const title = screen.getByTestId('tooltip').getAttribute('data-title');
      expect(title).toMatch(/^\d{2}:\d{2} .+, \d{2}-[A-Z][a-z]{2}$/);
    });
  });

  // ─── prefix and suffix ───────────────────────────────────────────────────────

  describe('prefix and suffix', () => {
    it('renders prefix when provided', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} prefix='Since:' showTooltip={false} />);
      expect(screen.getByText('Since:')).toBeInTheDocument();
    });

    it('renders suffix when provided', () => {
      render(<Datetime value={past(30 * ONE_SEC)} baseDate={BASE} suffix='elapsed' showTooltip={false} />);
      expect(screen.getByText('elapsed')).toBeInTheDocument();
    });
  });
});
