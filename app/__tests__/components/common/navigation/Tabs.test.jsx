import { render, screen, fireEvent } from '@testing-library/react';
import Tabs from '@shared/navigation/Tabs';

global.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

jest.mock('next/router', () => ({
  useRouter: jest.fn().mockReturnValue({
    asPath: '/dashboard#home',
    query: { accountId: '123' },
    push: jest.fn(),
  }),
}));

jest.mock('next/link', () => {
  const { forwardRef } = require('react');
  return {
    __esModule: true,
    default: forwardRef(({ children, href }, ref) => (
      <a ref={ref} href={href}>
        {children}
      </a>
    )),
  };
});

jest.mock('@ui/Chip', () => ({
  Chip: ({ children }) => <span data-testid='chip'>{children}</span>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({ BetaIcon: 'beta.svg' }));

jest.mock('@utils/colors');

const baseOptions = {
  tabOptions: [
    { value: 'tab1', text: 'Tab One' },
    { value: 'tab2', text: 'Tab Two' },
  ],
};

describe('Tabs', () => {
  describe('rendering', () => {
    it('renders tab labels', () => {
      render(<Tabs value='tab1' onChange={jest.fn()} options={baseOptions} />);
      expect(screen.getByText('Tab One')).toBeInTheDocument();
      expect(screen.getByText('Tab Two')).toBeInTheDocument();
    });

    it('renders empty when tabOptions is empty', () => {
      const { container } = render(<Tabs value='' onChange={jest.fn()} options={{ tabOptions: [] }} />);
      expect(container.querySelector('[role="tablist"]')).not.toBeInTheDocument();
    });
  });

  describe('hidden tabs', () => {
    it('does not render hidden tabs', () => {
      const options = {
        tabOptions: [
          { value: 'tab1', text: 'Visible' },
          { value: 'tab2', text: 'Hidden', hidden: true },
        ],
      };
      render(<Tabs value='tab1' onChange={jest.fn()} options={options} />);
      expect(screen.getByText('Visible')).toBeInTheDocument();
      expect(screen.queryByText('Hidden')).not.toBeInTheDocument();
    });
  });

  describe('onChange', () => {
    it('calls onChange when a tab is clicked', () => {
      const onChange = jest.fn();
      render(<Tabs value='tab1' onChange={onChange} options={baseOptions} behavior='filter' />);
      fireEvent.click(screen.getByText('Tab Two'));
      expect(onChange).toHaveBeenCalledWith('tab2');
    });
  });

  describe('count chip', () => {
    it('renders count chip when opt.count is provided', () => {
      const options = {
        tabOptions: [{ value: 't1', text: 'Tab', count: 5 }],
      };
      render(<Tabs value='t1' onChange={jest.fn()} options={options} behavior='filter' />);
      expect(screen.getByTestId('chip')).toHaveTextContent('5');
    });

    it('shows "99+" when count exceeds 99', () => {
      const options = {
        tabOptions: [{ value: 't1', text: 'Tab', count: 150 }],
      };
      render(<Tabs value='t1' onChange={jest.fn()} options={options} behavior='filter' />);
      expect(screen.getByTestId('chip')).toHaveTextContent('99+');
    });
  });

  describe('behavior=router', () => {
    it('renders tab as anchor (via Link) when behavior=router', () => {
      render(<Tabs value='tab1' onChange={jest.fn()} options={baseOptions} behavior='router' />);
      const links = screen.getAllByRole('link');
      expect(links.length).toBeGreaterThan(0);
    });
  });

  describe('behavior=filter', () => {
    it('renders tab as button when behavior=filter', () => {
      render(<Tabs value='tab1' onChange={jest.fn()} options={baseOptions} behavior='filter' />);
      expect(screen.getAllByRole('tab').length).toBeGreaterThan(0);
    });
  });
});
