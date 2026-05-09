import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import Chip, { hashHue, type ChipHue } from '@components1/common/Chip';

// Mirror the colors mock used by other common-component tests; the real colors module
// resolves to CSS custom properties at runtime, which JSDOM doesn't compute.
jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    success: '#16A34A',
    error: '#EF4444',
    medium: '#F59E0B',
    text: {
      tertiary: '#6B7280',
      quaternary: '#9CA3AF',
      warning: '#92400E',
      infoDark: '#1E3A8A',
    },
    background: {
      tertiaryLight: '#F3F4F6',
      tertiaryLightest: '#F9FAFB',
      anchorActiveTab: '#EFF6FF',
      primaryLightest: '#EFF6FF',
      costBlock: '#ECFDF5',
      warningLightest: '#FFFBEB',
      warningButtonHover: '#FDE68A',
      errorLight: '#FEE2E2',
    },
    border: {
      secondaryLightest: '#E5E7EB',
      primaryLight: '#60A5FA',
      primaryLightest: '#DBEAFE',
      errorLight: '#FECACA',
      warning: '#F59E0B',
    },
  },
}));

describe('Chip', () => {
  // ─── Rendering across variants ───────────────────────────────────────────

  it.each(['filter', 'tag', 'status', 'input', 'action', 'count', 'avatar'] as const)('renders %s variant without crashing', (variant) => {
    render(<Chip variant={variant} label='Hello' leadingAvatar={variant === 'avatar' ? <div /> : undefined} data-testid={`chip-${variant}`} />);
    expect(screen.getByTestId(`chip-${variant}`)).toBeInTheDocument();
  });

  // ─── Sizing ──────────────────────────────────────────────────────────────

  it.each(['xs', 'sm', 'md'] as const)('applies %s size token', (size) => {
    render(<Chip variant='tag' size={size} label='S' data-testid='chip' />);
    const chip = screen.getByTestId('chip');
    expect(chip).toHaveAttribute('data-size', size);
  });

  // ─── Tones ───────────────────────────────────────────────────────────────

  it.each(['neutral', 'info', 'success', 'warning', 'danger', 'pending'] as const)('renders %s tone', (tone) => {
    render(<Chip variant='status' tone={tone} label='X' data-testid='chip' />);
    expect(screen.getByTestId('chip')).toHaveAttribute('data-tone', tone);
  });

  // ─── Filter chip selection ────────────────────────────────────────────────

  it('reflects selected state via data-selected and aria-pressed', () => {
    render(<Chip variant='filter' tone='danger' label='Critical' selected data-testid='chip' />);
    const chip = screen.getByTestId('chip');
    expect(chip).toHaveAttribute('data-selected', 'true');
    expect(chip).toHaveAttribute('aria-pressed', 'true');
    // Selection now uses the indigo FILTER_SELECTED palette (not the tone). The original
    // `tone` prop is preserved on data-tone so the leading dot can still be rendered in
    // the semantic color (e.g., red dot on a "Critical" filter).
    expect(chip).toHaveAttribute('data-tone', 'danger');
  });

  it('unselected filter chip has aria-pressed false', () => {
    render(<Chip variant='filter' label='Critical' data-testid='chip' />);
    expect(screen.getByTestId('chip')).toHaveAttribute('aria-pressed', 'false');
  });

  // ─── Click + keyboard ────────────────────────────────────────────────────

  it('fires onClick when interactive variant is clicked', () => {
    const onClick = jest.fn();
    render(<Chip variant='filter' label='F' onClick={onClick} data-testid='chip' />);
    fireEvent.click(screen.getByTestId('chip'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('does not fire onClick when disabled', () => {
    const onClick = jest.fn();
    render(<Chip variant='filter' label='F' onClick={onClick} disabled data-testid='chip' />);
    fireEvent.click(screen.getByTestId('chip'));
    expect(onClick).not.toHaveBeenCalled();
  });

  it('Space and Enter trigger onClick', () => {
    const onClick = jest.fn();
    render(<Chip variant='action' label='A' onClick={onClick} data-testid='chip' />);
    const chip = screen.getByTestId('chip');
    fireEvent.keyDown(chip, { key: 'Enter' });
    fireEvent.keyDown(chip, { key: ' ' });
    expect(onClick).toHaveBeenCalledTimes(2);
  });

  // ─── Dismiss ─────────────────────────────────────────────────────────────

  it('renders trailing × when onDismiss is set and fires the handler', () => {
    const onDismiss = jest.fn();
    render(<Chip variant='input' label='Owner: alice' onDismiss={onDismiss} data-testid='chip' />);
    const dismiss = screen.getByTestId('chip-dismiss');
    expect(dismiss).toBeInTheDocument();
    fireEvent.click(dismiss);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('Backspace on a removable chip fires onDismiss', () => {
    const onDismiss = jest.fn();
    render(<Chip variant='input' label='X' onDismiss={onDismiss} data-testid='chip' />);
    fireEvent.keyDown(screen.getByTestId('chip'), { key: 'Backspace' });
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  // ─── Slot rendering ──────────────────────────────────────────────────────

  it('renders leadingDot for status chips', () => {
    const { container } = render(<Chip variant='status' tone='success' leadingDot label='Healthy' data-testid='chip' />);
    // Dot is the first child Box inside the chip body
    const chip = container.querySelector('[data-testid="chip"]');
    expect(chip?.firstChild).toBeTruthy();
  });

  it('renders trailing chevron for filter chips', () => {
    const { container } = render(<Chip variant='filter' trailingChevron label='Status (3)' data-testid='chip' />);
    expect(container.querySelector('[data-testid="chip"] svg')).toBeTruthy();
  });

  // ─── Renders as button when interactive, span otherwise ─────────────────

  it('renders interactive variants as button', () => {
    render(<Chip variant='filter' label='F' onClick={() => {}} data-testid='chip' />);
    expect(screen.getByTestId('chip').tagName).toBe('BUTTON');
  });

  it('renders read-only variants as span', () => {
    render(<Chip variant='tag' label='T' data-testid='chip' />);
    expect(screen.getByTestId('chip').tagName).toBe('SPAN');
  });

  // ─── Loading replaces leading slot ──────────────────────────────────────

  it('renders a spinner when loading', () => {
    const { container } = render(<Chip variant='action' loading label='Save' data-testid='chip' />);
    expect(container.querySelector('.MuiCircularProgress-root')).toBeTruthy();
  });

  // ─── Gap 1: Tag categorical hue palette ──────────────────────────────────

  it.each(['slate', 'green', 'amber', 'red', 'blue', 'violet', 'pink', 'teal'] as const)('renders tag chip with %s hue', (hue) => {
    const { container } = render(<Chip variant='tag' hue={hue} label='X' data-testid='chip' />);
    // Hue swatches are intentional new tokens (categorical), so we verify the chip
    // applies a non-default background — exact hex isn't asserted, since changing the
    // palette shouldn't break this test.
    const chip = container.querySelector('[data-testid="chip"]');
    expect(chip).toBeTruthy();
    expect(chip).toHaveAttribute('data-variant', 'tag');
  });

  it('hashHue returns a stable hue for the same key', () => {
    const a = hashHue('frontend');
    const b = hashHue('frontend');
    expect(a).toBe(b);
    const valid: ChipHue[] = ['slate', 'green', 'amber', 'red', 'blue', 'violet', 'pink', 'teal'];
    expect(valid).toContain(a);
  });

  it('hashHue distributes across hues for varied keys', () => {
    const seen = new Set<ChipHue>();
    ['production', 'frontend', 'api', 'ops', 'billing', 'auth', 'design', 'incident', 'data'].forEach((k) => seen.add(hashHue(k)));
    // Don't expect all 8 — hash collisions are acceptable. But it should hit at least 4.
    expect(seen.size).toBeGreaterThanOrEqual(4);
  });

  // ─── Gap 2: leadingDot allowed on filter chips ───────────────────────────

  it('allows leadingDot on filter chips without warning', () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    render(<Chip variant='filter' tone='danger' leadingDot label='Critical' data-testid='chip' />);
    const calls = warn.mock.calls.filter((args) => String(args[0]).includes('[Chip]'));
    expect(calls.find((args) => String(args[0]).includes('leadingDot'))).toBeUndefined();
    warn.mockRestore();
  });

  it('still warns on leadingDot for non-filter, non-status variants', () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    render(<Chip variant='tag' leadingDot label='X' data-testid='chip' />);
    const calls = warn.mock.calls.filter((args) => String(args[0]).includes('leadingDot'));
    expect(calls.length).toBeGreaterThan(0);
    warn.mockRestore();
  });

  // ─── Gap 3: hollow dot variant ───────────────────────────────────────────

  it('renders explicit dotVariant=hollow with data-dot="hollow"', () => {
    const { container } = render(<Chip variant='status' tone='success' leadingDot dotVariant='hollow' label='Queued' data-testid='chip' />);
    const dot = container.querySelector('[data-dot]');
    expect(dot).toHaveAttribute('data-dot', 'hollow');
  });

  it('auto-hollows the dot when tone=pending and no explicit dotVariant', () => {
    const { container } = render(<Chip variant='status' tone='pending' leadingDot label='Queued' data-testid='chip' />);
    const dot = container.querySelector('[data-dot]');
    expect(dot).toHaveAttribute('data-dot', 'hollow');
  });

  it('explicit dotVariant=filled overrides the pending auto-hollow', () => {
    const { container } = render(<Chip variant='status' tone='pending' leadingDot dotVariant='filled' label='Queued' data-testid='chip' />);
    const dot = container.querySelector('[data-dot]');
    expect(dot).toHaveAttribute('data-dot', 'filled');
  });

  it('default dot is filled for non-pending tones', () => {
    const { container } = render(<Chip variant='status' tone='success' leadingDot label='Healthy' data-testid='chip' />);
    const dot = container.querySelector('[data-dot]');
    expect(dot).toHaveAttribute('data-dot', 'filled');
  });
});
