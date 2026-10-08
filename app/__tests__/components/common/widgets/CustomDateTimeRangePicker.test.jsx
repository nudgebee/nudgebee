import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { rangeProblem } from '@shared/widgets/dateRangeShortcuts';

jest.mock('@mui/x-date-pickers/LocalizationProvider', () => ({
  LocalizationProvider: ({ children }) => <>{children}</>,
}));

jest.mock('@mui/x-date-pickers/AdapterDayjs', () => ({
  AdapterDayjs: class {},
}));

// Label-based testids let us distinguish From vs To pickers unambiguously.
// The field hands onChange a dayjs, as the real one does — an unparsable entry is an invalid dayjs, not a string.
jest.mock('@mui/x-date-pickers/DateTimePicker', () => ({
  DateTimePicker: ({ value, onChange, label }) => {
    const mockDayjs = jest.requireActual('dayjs');
    return (
      <input
        data-testid={label ? `picker-${label.toLowerCase()}` : 'picker'}
        aria-label={label || 'date-picker'}
        value={value ? String(value) : ''}
        onChange={(e) => onChange?.(mockDayjs(e.target.value))}
      />
    );
  },
}));

// Spread data-testid so the component's data-testid props on DsButton reach the DOM.
jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, ...rest }) => (
    <button id={id} onClick={onClick} disabled={disabled} data-testid={rest['data-testid'] || id || 'btn'}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children }) => <>{children}</>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({
  calendarViewWeek: { default: { src: 'cal.svg' } },
  MenuArrowDownIcon: 'arrow.svg',
}));

jest.mock('@data/constants', () => ({
  TIME_PICK_SHORTCUTS: ['Last 5 Minutes', 'Last 1 Hour', 'Last 24 Hours'],
}));

jest.mock('@utils/colors');

const FIXED_NOW = 1700000000000;
const now = FIXED_NOW;
const baseProps = {
  passedSelectedDateTime: { startTime: now - 3600000, endTime: now },
  onChange: jest.fn(),
};

// Helper: click the trigger (first button, always the Box component='button' trigger)
const clickTrigger = () => fireEvent.click(screen.getAllByRole('button')[0]);

const HOUR = 3600000;
/** What a field is given for an instant: text the mocked field parses back to the same ms. */
const at = (ms) => new Date(ms).toISOString();
// The trigger while the popover is open — `hidden` because an open popover hides the page behind it from the a11y tree.
const triggerBehindPopover = () => screen.getAllByRole('button', { hidden: true })[0];
const problem = () => screen.queryByTestId('date-range-problem')?.textContent ?? null;
const closeWithoutApplying = () => fireEvent.keyDown(screen.getByRole('presentation'), { key: 'Escape' });

beforeEach(() => {
  baseProps.onChange.mockClear();
});

describe('CustomDateTimeRangePicker', () => {
  // ─── Trigger display text ────────────────────────────────────────────────────

  describe('trigger display text', () => {
    it('shows formatted date range containing separator when no shortcut is active', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      expect(screen.getAllByRole('button')[0]).toHaveTextContent(/ - /);
    });

    it('shows selected shortcut name in trigger after shortcut click', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-1-hour'));
      // Popover closes; only the trigger remains
      expect(screen.getByRole('button')).toHaveTextContent('Last 1 Hour');
    });

    it('auto-shows shortcut name in trigger when passedSelectedDateTime has matching shortcutClickTime', () => {
      render(
        <CustomDateTimeRangePicker
          passedSelectedDateTime={{
            startTime: now - 5 * 60 * 1000,
            endTime: now,
            shortcutClickTime: 5 * 60 * 1000,
          }}
          onChange={jest.fn()}
        />
      );
      // The useEffect detects the shortcutClickTime and sets selectedShortCut = 'Last 5 Minutes'
      expect(screen.getByRole('button')).toHaveTextContent('Last 5 Minutes');
    });

    it('hides date range text when showOnlyCalenderIcon=true', () => {
      const start = new Date('2024-06-01').getTime();
      const end = new Date('2024-06-15').getTime();
      render(
        <CustomDateTimeRangePicker passedSelectedDateTime={{ startTime: start, endTime: end }} onChange={jest.fn()} showOnlyCalenderIcon={true} />
      );
      // Without showOnlyCalenderIcon the trigger contains " - " separator; it should not here
      expect(screen.getAllByRole('button')[0]).not.toHaveTextContent(/ - /);
    });
  });

  // ─── Rendering ──────────────────────────────────────────────────────────────

  describe('rendering', () => {
    it('renders trigger button', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      expect(screen.getAllByRole('button').length).toBeGreaterThan(0);
    });

    it('does not show popover by default', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      expect(screen.queryByText('Absolute Date Range')).not.toBeInTheDocument();
    });
  });

  // ─── Popover open / close ────────────────────────────────────────────────────

  describe('popover', () => {
    it('opens popover when trigger button is clicked', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByText('Absolute Date Range')).toBeInTheDocument();
    });

    it('shows all configured shortcut options', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByText('Last 5 Minutes')).toBeInTheDocument();
      expect(screen.getByText('Last 1 Hour')).toBeInTheDocument();
      expect(screen.getByText('Last 24 Hours')).toBeInTheDocument();
    });

    it('closes popover after a shortcut is clicked', async () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByText('Absolute Date Range')).toBeInTheDocument();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-5-minutes'));
      await waitFor(() => {
        expect(screen.queryByText('Absolute Date Range')).not.toBeInTheDocument();
      });
    });

    it('renders From and To DateTimePicker inputs inside the popover', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByTestId('picker-from')).toBeInTheDocument();
      expect(screen.getByTestId('picker-to')).toBeInTheDocument();
    });

    it('renders Apply Time Range button inside the popover', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByTestId('date-range-apply')).toBeInTheDocument();
    });

    it('closes popover after Apply Time Range is clicked', async () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByText('Absolute Date Range')).toBeInTheDocument();
      fireEvent.click(screen.getByTestId('date-range-apply'));
      await waitFor(() => {
        expect(screen.queryByText('Absolute Date Range')).not.toBeInTheDocument();
      });
    });
  });

  // ─── showAbsoluteRange ───────────────────────────────────────────────────────

  describe('showAbsoluteRange prop', () => {
    it('shows Absolute Date Range section by default', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByText('Absolute Date Range')).toBeInTheDocument();
    });

    it('hides Absolute Date Range section when showAbsoluteRange=false', () => {
      render(<CustomDateTimeRangePicker {...baseProps} showAbsoluteRange={false} />);
      clickTrigger();
      expect(screen.queryByText('Absolute Date Range')).not.toBeInTheDocument();
    });

    it('still shows shortcut rail when showAbsoluteRange=false', () => {
      render(<CustomDateTimeRangePicker {...baseProps} showAbsoluteRange={false} />);
      clickTrigger();
      expect(screen.getByText('Last 5 Minutes')).toBeInTheDocument();
    });

    it('hides From/To pickers when showAbsoluteRange=false', () => {
      render(<CustomDateTimeRangePicker {...baseProps} showAbsoluteRange={false} />);
      clickTrigger();
      expect(screen.queryByTestId('picker-from')).not.toBeInTheDocument();
      expect(screen.queryByTestId('picker-to')).not.toBeInTheDocument();
    });
  });

  // ─── Apply button ────────────────────────────────────────────────────────────

  describe('Apply button', () => {
    it('calls onChange when Apply is clicked', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-apply'));
      expect(baseProps.onChange).toHaveBeenCalledTimes(1);
    });

    it('onChange receives selection with startTime and endTime', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-apply'));
      expect(baseProps.onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          selection: expect.objectContaining({
            startTime: expect.anything(),
            endTime: expect.anything(),
          }),
        })
      );
    });

    it('onChange shortcutClickTime is 0 when applied without a shortcut', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-apply'));
      expect(baseProps.onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          selection: expect.objectContaining({ shortcutClickTime: 0 }),
        })
      );
    });

    it('onChange selection reflects the initial passedSelectedDateTime values', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-apply'));
      const { selection } = baseProps.onChange.mock.calls[0][0];
      expect(selection.startTime).toBe(baseProps.passedSelectedDateTime.startTime);
      expect(selection.endTime).toBe(baseProps.passedSelectedDateTime.endTime);
    });
  });

  // ─── From / To picker changes ────────────────────────────────────────────────

  describe('From/To picker changes', () => {
    it('updates startTime when From picker changes and Apply is clicked', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: at(now - 2 * HOUR) } });
      fireEvent.click(screen.getByTestId('date-range-apply'));
      expect(baseProps.onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          selection: expect.objectContaining({ startTime: now - 2 * HOUR }),
        })
      );
    });

    it('updates endTime when To picker changes and Apply is clicked', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-to'), { target: { value: at(now - HOUR / 2) } });
      fireEvent.click(screen.getByTestId('date-range-apply'));
      expect(baseProps.onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          selection: expect.objectContaining({ endTime: now - HOUR / 2 }),
        })
      );
    });

    it('resets shortcutClickTime to 0 when a picker is changed manually then applied', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      // First select a shortcut (sets shortcutClickTime > 0 internally)
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-5-minutes'));
      baseProps.onChange.mockClear();

      // Reopen, change From picker (deselects shortcut)
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: '2024-01-01' } });
      fireEvent.click(screen.getByTestId('date-range-apply'));

      expect(baseProps.onChange).toHaveBeenCalledWith(
        expect.objectContaining({
          selection: expect.objectContaining({ shortcutClickTime: 0 }),
        })
      );
    });
  });

  // ─── Shortcut onClick behavior ───────────────────────────────────────────────

  describe('shortcut onClick behavior', () => {
    it('calls onChange when a shortcut is clicked', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-5-minutes'));
      expect(baseProps.onChange).toHaveBeenCalledWith(expect.objectContaining({ selection: expect.any(Object) }));
    });

    it('onChange shortcutClickTime matches the shortcut window for Last 5 Minutes', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-5-minutes'));
      const { selection } = baseProps.onChange.mock.calls[0][0];
      expect(selection.shortcutClickTime).toBe(5 * 60 * 1000);
    });

    it('onChange shortcutClickTime matches the shortcut window for Last 1 Hour', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-1-hour'));
      const { selection } = baseProps.onChange.mock.calls[0][0];
      expect(selection.shortcutClickTime).toBe(1 * 60 * 60 * 1000);
    });

    it('onChange startTime is exactly now minus shortcut window', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-5-minutes'));
      const { startTime, endTime } = baseProps.onChange.mock.calls[0][0].selection;
      const fiveMin = 5 * 60 * 1000;
      expect(endTime - startTime).toBe(fiveMin);
    });

    it('shortcut for Last 24 Hours has correct data-testid', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      expect(screen.getByTestId('date-range-shortcut-last-24-hours')).toBeInTheDocument();
    });
  });

  // ─── Custom shortCuts prop ───────────────────────────────────────────────────

  describe('custom shortCuts prop', () => {
    it('renders only the provided custom shortcuts', () => {
      render(<CustomDateTimeRangePicker {...baseProps} shortCuts={['Last 15 Minutes', 'Last 3 Hours']} />);
      clickTrigger();
      expect(screen.getByText('Last 15 Minutes')).toBeInTheDocument();
      expect(screen.getByText('Last 3 Hours')).toBeInTheDocument();
    });

    it('does not render default shortcuts when custom shortCuts are provided', () => {
      render(<CustomDateTimeRangePicker {...baseProps} shortCuts={['Last 15 Minutes']} />);
      clickTrigger();
      expect(screen.queryByText('Last 5 Minutes')).not.toBeInTheDocument();
      expect(screen.queryByText('Last 1 Hour')).not.toBeInTheDocument();
    });

    it('clicking a custom shortcut calls onChange with its shortcutClickTime', () => {
      render(<CustomDateTimeRangePicker {...baseProps} shortCuts={['Last 30 Minutes']} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-30-minutes'));
      const { selection } = baseProps.onChange.mock.calls[0][0];
      expect(selection.shortcutClickTime).toBe(30 * 60 * 1000);
    });
  });

  // ─── resetDateTime ───────────────────────────────────────────────────────────

  describe('resetDateTime prop', () => {
    it('resets shortcut selection when resetDateTime changes to a truthy value', () => {
      const { rerender } = render(<CustomDateTimeRangePicker {...baseProps} resetDateTime={0} />);

      // Select a shortcut so the trigger shows its name
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-1-hour'));
      expect(screen.getByRole('button')).toHaveTextContent('Last 1 Hour');

      // Trigger reset by changing resetDateTime to a truthy value
      rerender(<CustomDateTimeRangePicker {...baseProps} resetDateTime={99999} />);

      // Trigger should no longer show the shortcut name — back to date range text
      expect(screen.getByRole('button')).not.toHaveTextContent('Last 1 Hour');
      expect(screen.getByRole('button')).toHaveTextContent(/ - /);
    });

    it('does not reset when resetDateTime stays falsy (0)', () => {
      const { rerender } = render(<CustomDateTimeRangePicker {...baseProps} resetDateTime={0} />);
      clickTrigger();
      fireEvent.click(screen.getByTestId('date-range-shortcut-last-1-hour'));
      expect(screen.getByRole('button')).toHaveTextContent('Last 1 Hour');

      // Re-render with the same resetDateTime=0 — should not reset
      rerender(<CustomDateTimeRangePicker {...baseProps} resetDateTime={0} />);
      expect(screen.getByRole('button')).toHaveTextContent('Last 1 Hour');
    });
  });

  // ─── Range validation ────────────────────────────────────────────────────────
  //
  // Each field is bounded against the calendar but not against the other field, so a From after To could be
  // applied: the page then asked for a backwards range and drew empty panels with no error.

  describe('rangeProblem', () => {
    it('accepts a start before its end', () => {
      expect(rangeProblem(now - HOUR, now)).toBe('');
    });

    it('rejects an end before, or at, the start', () => {
      expect(rangeProblem(now, now - HOUR)).toBe('From must be earlier than To.');
      expect(rangeProblem(now, now)).toBe('From must be earlier than To.');
    });

    it('rejects a date that did not parse, and a missing one', () => {
      expect(rangeProblem(NaN, now)).toBe('Enter a valid date and time in both fields.');
      expect(rangeProblem(now, undefined)).toBe('Enter a valid date and time in both fields.');
      expect(rangeProblem(null, now)).toBe('Enter a valid date and time in both fields.');
    });

    it('reads whatever a page seeds the picker with, as the fields do', () => {
      // Pages hand over what they hold — not always a number of ms.
      expect(rangeProblem(new Date(now - HOUR), new Date(now))).toBe('');
      expect(rangeProblem(new Date(now - HOUR).toISOString(), new Date(now).toISOString())).toBe('');
      expect(rangeProblem(new Date(now), new Date(now - HOUR))).toBe('From must be earlier than To.');
    });

    it('leaves Apply available on first open for a page that seeds the picker with Date objects', () => {
      render(<CustomDateTimeRangePicker {...baseProps} passedSelectedDateTime={{ startTime: new Date(now - HOUR), endTime: new Date(now) }} />);
      clickTrigger();

      expect(screen.getByTestId('date-range-apply')).toBeEnabled();
      expect(problem()).toBeNull();
    });
  });

  describe('applying a range that cannot be used', () => {
    it('refuses a From that is after To, and says why', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: at(now + HOUR) } });

      expect(screen.getByTestId('date-range-apply')).toBeDisabled();
      expect(problem()).toBe('From must be earlier than To.');
      fireEvent.click(screen.getByTestId('date-range-apply'));
      expect(baseProps.onChange).not.toHaveBeenCalled();
    });

    it('allows the range again once it is put right', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: at(now + HOUR) } });
      expect(screen.getByTestId('date-range-apply')).toBeDisabled();

      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: at(now - 2 * HOUR) } });

      expect(screen.getByTestId('date-range-apply')).toBeEnabled();
      expect(problem()).toBeNull();
    });

    it('refuses a half-typed date', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: 'not a date' } });

      expect(screen.getByTestId('date-range-apply')).toBeDisabled();
      expect(problem()).toBe('Enter a valid date and time in both fields.');
    });
  });

  // ─── Unapplied edits ─────────────────────────────────────────────────────────
  //
  // The trigger's label was the popover's draft: editing a field and closing without Apply left the abandoned
  // dates — or "Invalid Date - Invalid Date" — on the toolbar, over data still drawn for the old range.

  describe('the range on the trigger', () => {
    const appliedLabel = () => {
      const day = (ms) => new Date(ms).toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
      return `${day(now - HOUR)} - ${day(now)}`;
    };

    it('stays on the applied range while a field is being edited', () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: 'not a date' } });

      expect(triggerBehindPopover()).toHaveTextContent(appliedLabel());
    });

    it('goes back to the applied range when the picker is closed without Apply', async () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: 'not a date' } });
      closeWithoutApplying();
      await waitFor(() => expect(screen.queryByTestId('date-range-apply')).not.toBeInTheDocument());

      expect(screen.getAllByRole('button')[0]).toHaveTextContent(appliedLabel());
      // And the abandoned edit is gone from the fields, not waiting behind the label.
      clickTrigger();
      expect(screen.getByTestId('picker-from')).toHaveValue(String(new Date(now - HOUR)));
      expect(screen.getByTestId('date-range-apply')).toBeEnabled();
    });

    it('keeps a shortcut’s name when an edit under it is abandoned', async () => {
      render(<CustomDateTimeRangePicker {...baseProps} passedSelectedDateTime={{ startTime: now - HOUR, endTime: now, shortcutClickTime: HOUR }} />);
      expect(screen.getAllByRole('button')[0]).toHaveTextContent('Last 1 Hour');

      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: at(now - 3 * HOUR) } });
      closeWithoutApplying();
      await waitFor(() => expect(screen.queryByTestId('date-range-apply')).not.toBeInTheDocument());

      expect(screen.getAllByRole('button')[0]).toHaveTextContent('Last 1 Hour');
    });

    it('shows the new range once it is applied', async () => {
      render(<CustomDateTimeRangePicker {...baseProps} />);
      clickTrigger();
      fireEvent.change(screen.getByTestId('picker-from'), { target: { value: at(now - 26 * HOUR) } });
      fireEvent.click(screen.getByTestId('date-range-apply'));
      await waitFor(() => expect(screen.queryByTestId('date-range-apply')).not.toBeInTheDocument());

      const day = (ms) => new Date(ms).toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
      expect(screen.getAllByRole('button')[0]).toHaveTextContent(`${day(now - 26 * HOUR)} - ${day(now)}`);
    });
  });
});
