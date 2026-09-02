import { render, screen, fireEvent, act } from '@testing-library/react';
import AnchorComponent from '@components/common/navigation/AnchorComponent';

global.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

jest.mock('next/router', () => ({
  useRouter: jest.fn().mockReturnValue({
    asPath: '/dashboard',
    push: jest.fn(),
    query: {},
    route: '/dashboard',
  }),
}));

jest.mock('next/link', () => {
  const React = require('react');
  const Link = React.forwardRef(function Link({ children, href, passHref: _passHref, ...rest }, ref) {
    return (
      <a href={href} ref={ref} {...rest}>
        {children}
      </a>
    );
  });
  Link.displayName = 'Link';
  return { __esModule: true, default: Link };
});

jest.mock('@components/common/navigation/Tabs', () => ({
  __esModule: true,
  default: ({ options, value, showBorderBottom, showCustomRounded, p, showGroupedTabs, tooltip }) => (
    <div
      data-testid='custom-tabs'
      data-show-border-bottom={String(!!showBorderBottom)}
      data-show-custom-rounded={String(!!showCustomRounded)}
      data-p={p || ''}
      data-show-grouped-tabs={String(!!showGroupedTabs)}
      data-tooltip={tooltip || ''}
    >
      {options?.tabOptions?.map((opt) => (
        <span key={opt.value} data-testid={`subtab-${opt.value}`} aria-selected={value === opt.value ? 'true' : 'false'}>
          {opt.text}
        </span>
      ))}
    </div>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick }) => (
    <button onClick={onClick} data-testid='ds-btn'>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Chip', () => ({
  Chip: ({ children }) => <span data-testid='chip'>{children}</span>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${(alt || '').replace(/\s+/g, '-').toLowerCase()}`}>{alt}</span>,
}));

jest.mock('@assets', () => ({ BetaIcon: 'beta.svg', MenuArrowDownIcon: 'arrow.svg' }));

jest.mock('@utils/colors');

const baseOptions = [
  { value: 0, name: 'Overview', fragment: 'overview' },
  { value: 1, name: 'Details', fragment: 'details' },
];

const optionsWithSubTabs = [
  {
    value: 0,
    name: 'Overview',
    fragment: 'overview',
    tabOptions: [
      { value: 0, text: 'Summary', fragment: 'summary' },
      { value: 1, text: 'Events', fragment: 'events' },
    ],
  },
];

describe('AnchorComponent', () => {
  describe('rendering', () => {
    it('renders without crashing with empty filterOptions', () => {
      const { container } = render(<AnchorComponent filterOptions={[]} onChangeFilter={jest.fn()} />);
      expect(container.firstChild).toBeInTheDocument();
    });

    it('renders without crashing with filterOptions', () => {
      const { container } = render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} />);
      expect(container.firstChild).toBeInTheDocument();
    });

    it('renders tab names for top-level filter options', () => {
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} />);
      expect(screen.getAllByText('Overview').length).toBeGreaterThan(0);
      expect(screen.getAllByText('Details').length).toBeGreaterThan(0);
    });

    it('renders CustomTabs when active tab has tabOptions', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('custom-tabs')).toBeInTheDocument();
    });
  });

  describe('hidden tabs', () => {
    it('does not render a tab with hidden=true', () => {
      const options = [
        { value: 0, name: 'Visible', fragment: 'visible' },
        { value: 1, name: 'Hidden Tab', fragment: 'hidden-tab', hidden: true },
      ];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      expect(screen.queryByText('Hidden Tab')).not.toBeInTheDocument();
    });

    it('renders non-hidden tabs while excluding hidden ones', () => {
      const options = [
        { value: 0, name: 'Tab A', fragment: 'a' },
        { value: 1, name: 'Tab B', fragment: 'b', hidden: true },
        { value: 2, name: 'Tab C', fragment: 'c' },
      ];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      expect(screen.getAllByText('Tab A').length).toBeGreaterThan(0);
      expect(screen.queryByText('Tab B')).not.toBeInTheDocument();
      expect(screen.getAllByText('Tab C').length).toBeGreaterThan(0);
    });
  });

  describe('count chips', () => {
    it('renders a Chip when tab has a count', () => {
      const options = [{ value: 0, name: 'Alerts', fragment: 'alerts', count: 5 }];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('chip')).toHaveTextContent('5');
    });

    it('shows 99+ when count exceeds 99', () => {
      const options = [{ value: 0, name: 'Alerts', fragment: 'alerts', count: 100 }];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('chip')).toHaveTextContent('99+');
    });

    it('shows exact count when count is exactly 99', () => {
      const options = [{ value: 0, name: 'Alerts', fragment: 'alerts', count: 99 }];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('chip')).toHaveTextContent('99');
    });

    it('does not render a Chip when tab has no count', () => {
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} />);
      expect(screen.queryByTestId('chip')).not.toBeInTheDocument();
    });

    it('renders a chip for each tab that has a count', () => {
      const options = [
        { value: 0, name: 'Alerts', fragment: 'alerts', count: 3 },
        { value: 1, name: 'Events', fragment: 'events', count: 7 },
      ];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      const chips = screen.getAllByTestId('chip');
      expect(chips).toHaveLength(2);
      expect(chips[0]).toHaveTextContent('3');
      expect(chips[1]).toHaveTextContent('7');
    });
  });

  describe('betaIcon', () => {
    it('renders beta icon for a tab with betaIcon=true', () => {
      const options = [{ value: 0, name: 'New Feature', fragment: 'new', betaIcon: true }];
      render(<AnchorComponent filterOptions={options} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('icon-beta-icon')).toBeInTheDocument();
    });

    it('does not render beta icon when betaIcon is not set', () => {
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} />);
      expect(screen.queryByTestId('icon-beta-icon')).not.toBeInTheDocument();
    });
  });

  describe('tabOptions arrow icon', () => {
    it('renders down arrow for a tab with tabOptions', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('icon-down-arrow')).toBeInTheDocument();
    });

    it('does not render down arrow for tabs without tabOptions', () => {
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} />);
      expect(screen.queryByTestId('icon-down-arrow')).not.toBeInTheDocument();
    });
  });

  describe('button', () => {
    it('renders button with correct title when buttonTitle is provided', () => {
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} buttonTitle='Add New' />);
      expect(screen.getByTestId('ds-btn')).toHaveTextContent('Add New');
    });

    it('does not render button when buttonTitle is empty', () => {
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} buttonTitle='' />);
      expect(screen.queryByTestId('ds-btn')).not.toBeInTheDocument();
    });

    it('calls handleButtonAction when button is clicked', () => {
      const handleButtonAction = jest.fn();
      render(
        <AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} buttonTitle='Add New' handleButtonAction={handleButtonAction} />
      );
      fireEvent.click(screen.getByTestId('ds-btn'));
      expect(handleButtonAction).toHaveBeenCalledTimes(1);
    });

    it('renders buttonComponent content', () => {
      render(
        <AnchorComponent
          filterOptions={baseOptions}
          onChangeFilter={jest.fn()}
          buttonComponent={<button data-testid='custom-btn'>Custom Action</button>}
        />
      );
      expect(screen.getByTestId('custom-btn')).toHaveTextContent('Custom Action');
    });

    it('does not show DsButton when only buttonComponent is provided', () => {
      render(
        <AnchorComponent filterOptions={baseOptions} onChangeFilter={jest.fn()} buttonComponent={<button data-testid='custom-btn'>Custom</button>} />
      );
      expect(screen.queryByTestId('ds-btn')).not.toBeInTheDocument();
    });
  });

  describe('onChangeFilter', () => {
    it('calls onChangeFilter on initial render with tab 0 and subtab 0', () => {
      const onChangeFilter = jest.fn();
      render(<AnchorComponent filterOptions={baseOptions} onChangeFilter={onChangeFilter} />);
      expect(onChangeFilter).toHaveBeenCalledWith(0, 0, { tab: 0, subtab: 0 });
    });

    it('does not throw when onChangeFilter is not provided', () => {
      expect(() => render(<AnchorComponent filterOptions={baseOptions} />)).not.toThrow();
    });
  });

  describe('CustomTabs props', () => {
    it('passes showBorderBottom=true to CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} showBorderBottom={true} />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-show-border-bottom', 'true');
    });

    it('passes showCustomRounded=true to CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} showCustomRounded={true} />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-show-custom-rounded', 'true');
    });

    it('passes tooltip string to CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} tooltip='Helpful info' />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-tooltip', 'Helpful info');
    });

    it('passes tabPadding as p to CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} tabPadding='8px 16px' />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-p', '8px 16px');
    });

    it('defaults showBorderBottom to false in CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-show-border-bottom', 'false');
    });

    it('defaults showCustomRounded to false in CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-show-custom-rounded', 'false');
    });

    it('defaults tooltip to empty string in CustomTabs', () => {
      render(<AnchorComponent filterOptions={optionsWithSubTabs} onChangeFilter={jest.fn()} />);
      expect(screen.getByTestId('custom-tabs')).toHaveAttribute('data-tooltip', '');
    });
  });

  describe('options secondary navigation', () => {
    it('renders scroll-to buttons when the active tab has options', () => {
      const optionsWithNav = [
        {
          value: 0,
          name: 'Metrics',
          fragment: 'metrics',
          options: [
            { id: 'section-cpu', name: 'CPU' },
            { id: 'section-mem', name: 'Memory' },
          ],
        },
      ];
      render(<AnchorComponent filterOptions={optionsWithNav} onChangeFilter={jest.fn()} />);
      expect(screen.getByText('CPU')).toBeInTheDocument();
      expect(screen.getByText('Memory')).toBeInTheDocument();
    });
  });

  describe('hover popover', () => {
    it('shows dropdown items on mouse over a tab with tabOptions', () => {
      const optionsWithDropdown = [
        {
          value: 0,
          name: 'Menu',
          fragment: 'menu',
          tabOptions: [
            { id: 'sub0', value: 0, text: 'Submenu A', fragment: 'a' },
            { id: 'sub1', value: 1, text: 'Submenu B', fragment: 'b' },
          ],
        },
      ];
      render(<AnchorComponent filterOptions={optionsWithDropdown} onChangeFilter={jest.fn()} />);
      const tabLink = document.querySelector('a[href*="menu"]');
      act(() => {
        fireEvent.mouseOver(tabLink);
      });
      expect(screen.getAllByText('Submenu A').length).toBeGreaterThan(0);
      expect(screen.getAllByText('Submenu B').length).toBeGreaterThan(0);
    });
  });
});
