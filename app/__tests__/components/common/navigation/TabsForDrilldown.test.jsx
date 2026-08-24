import { render, screen, fireEvent } from '@testing-library/react';
import TabsForDrilldown from '@shared/navigation/TabsForDrilldown';

global.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

jest.mock('@shared/navigation/Tabs', () => ({
  __esModule: true,
  default: ({ value, onChange, options, behavior, ariaLabel }) => (
    <div data-testid='custom-tabs' data-behavior={behavior} data-aria-label={ariaLabel}>
      {options?.tabOptions?.map((opt) => (
        <button key={opt.value} data-testid={`tab-${opt.value}`} onClick={() => onChange(opt.value)} aria-selected={value === opt.value}>
          {opt.text}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick }) => (
    <button data-testid='right-btn' onClick={onClick}>
      {children}
    </button>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({ AlphaIcon: 'alpha.svg' }));

jest.mock('@utils/colors');

const baseOptions = [
  { value: 'overview', text: 'Overview' },
  { value: 'details', text: 'Details' },
];

describe('TabsForDrilldown', () => {
  describe('rendering', () => {
    it('renders CustomTabs with behavior=filter', () => {
      render(<TabsForDrilldown value='overview' onChange={jest.fn()} options={baseOptions} />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-behavior', 'filter');
    });

    it('renders tab options', () => {
      render(<TabsForDrilldown value='overview' onChange={jest.fn()} options={baseOptions} />);
      expect(screen.getByText('Overview')).toBeInTheDocument();
      expect(screen.getByText('Details')).toBeInTheDocument();
    });

    it('renders with empty options when none provided', () => {
      render(<TabsForDrilldown value='' onChange={jest.fn()} />);
      expect(screen.getByTestId('custom-tabs')).toBeInTheDocument();
    });
  });

  describe('onChange adapter', () => {
    it('calls onChange with (null, newValue) when tab is clicked', () => {
      const onChange = jest.fn();
      render(<TabsForDrilldown value='overview' onChange={onChange} options={baseOptions} />);
      fireEvent.click(screen.getByTestId('tab-details'));
      expect(onChange).toHaveBeenCalledWith(null, 'details');
    });
  });

  describe('rightButton', () => {
    it('renders right button when visible=true', () => {
      const rightButton = { visible: true, text: 'Add', onClick: jest.fn() };
      render(<TabsForDrilldown value='overview' onChange={jest.fn()} options={baseOptions} rightButton={rightButton} />);
      expect(screen.getByTestId('right-btn')).toBeInTheDocument();
    });

    it('does not render right button when visible=false', () => {
      const rightButton = { visible: false, text: 'Add', onClick: jest.fn() };
      render(<TabsForDrilldown value='overview' onChange={jest.fn()} options={baseOptions} rightButton={rightButton} />);
      expect(screen.queryByTestId('right-btn')).not.toBeInTheDocument();
    });

    it('calls rightButton.onClick when clicked', () => {
      const onClick = jest.fn();
      render(<TabsForDrilldown value='overview' onChange={jest.fn()} options={baseOptions} rightButton={{ visible: true, text: 'Add', onClick }} />);
      fireEvent.click(screen.getByTestId('right-btn'));
      expect(onClick).toHaveBeenCalledTimes(1);
    });
  });
});
