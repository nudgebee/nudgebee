import { render, screen, fireEvent } from '@testing-library/react';
import CollapsableCard from '@shared/widgets/CollapsableCard';

jest.mock('next/router', () => ({
  useRouter: jest.fn().mockReturnValue({ query: { accountId: 'acc1' } }),
}));

jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn().mockReturnValue(true) }));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='text'>{value}</span>,
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick }) => (
    <button onClick={onClick} data-testid='card-btn'>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Label', () => ({
  __esModule: true,
  default: ({ text }) => <span data-testid='label'>{text}</span>,
  Label: ({ text }) => <span data-testid='label'>{text}</span>,
}));

jest.mock('@ui/CustomBorderCard', () => ({
  __esModule: true,
  default: ({ children }) => <div data-testid='border-card'>{children}</div>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({
  WrenchIconOutline: 'wrench.svg',
  BetaIcon: 'beta.svg',
}));

jest.mock('@assets/kubernetes/sparkle.svg', () => 'sparkle.svg');

jest.mock('@utils/colors');

const baseProps = {
  idx: 0,
  text: 'Test Card',
  highlightsData: [],
  contentComponents: [],
  onCardClick: jest.fn(),
  expandedCardIndex: null,
  collapsedObj: { 0: false },
};

beforeEach(() => {
  baseProps.onCardClick.mockClear();
});

describe('CollapsableCard', () => {
  describe('rendering', () => {
    it('renders card text', () => {
      render(<CollapsableCard {...baseProps} />);
      expect(screen.getByText('Test Card')).toBeInTheDocument();
    });

    it('renders "Nothing major to show" when no highlights', () => {
      render(<CollapsableCard {...baseProps} />);
      expect(screen.getByText('Nothing major to show. Check the card for more.')).toBeInTheDocument();
    });

    it('renders highlight messages', () => {
      const props = {
        ...baseProps,
        highlightsData: [{ message: 'High CPU usage' }],
      };
      render(<CollapsableCard {...props} />);
      expect(screen.getByText('High CPU usage')).toBeInTheDocument();
    });
  });

  describe('collapse / expand', () => {
    it('calls onCardClick(idx) when collapse button is clicked', () => {
      render(<CollapsableCard {...baseProps} />);
      fireEvent.click(screen.getAllByRole('button')[0]);
      expect(baseProps.onCardClick).toHaveBeenCalledWith(0);
    });

    it('renders content components when expanded', () => {
      const ContentComp = () => <div data-testid='content-comp'>Content</div>;
      const props = {
        ...baseProps,
        expandedCardIndex: 0,
        collapsedObj: { 0: true },
        contentComponents: [ContentComp],
      };
      render(<CollapsableCard {...props} />);
      expect(screen.getByTestId('content-comp')).toBeInTheDocument();
    });

    it('does not render content components when not expanded', () => {
      const ContentComp = () => <div data-testid='content-comp'>Content</div>;
      const props = {
        ...baseProps,
        expandedCardIndex: 1,
        collapsedObj: { 0: false },
        contentComponents: [ContentComp],
      };
      render(<CollapsableCard {...props} />);
      expect(screen.queryByTestId('content-comp')).not.toBeInTheDocument();
    });
  });

  describe('resolve button', () => {
    it('renders Fix it button when resolveButton=true and user has write access', () => {
      render(<CollapsableCard {...baseProps} resolveButton={true} resolveButtonClick={jest.fn()} />);
      expect(screen.getByText('Fix it')).toBeInTheDocument();
    });

    it('calls resolveButtonClick when Fix it is clicked', () => {
      const resolveButtonClick = jest.fn();
      render(<CollapsableCard {...baseProps} resolveButton={true} resolveButtonClick={resolveButtonClick} id='card-1' />);
      fireEvent.click(screen.getByText('Fix it'));
      expect(resolveButtonClick).toHaveBeenCalledWith('card-1');
    });
  });

  describe('show more', () => {
    it('renders "Show more" link when highlights exceed 1', () => {
      const props = {
        ...baseProps,
        highlightsData: [{ message: 'Issue 1' }, { message: 'Issue 2' }],
      };
      render(<CollapsableCard {...props} />);
      expect(screen.getByText('Show more (1)')).toBeInTheDocument();
    });

    it('shows all items when "Show more" is clicked', () => {
      const props = {
        ...baseProps,
        highlightsData: [{ message: 'Issue 1' }, { message: 'Issue 2' }],
      };
      render(<CollapsableCard {...props} />);
      fireEvent.click(screen.getByText('Show more (1)'));
      expect(screen.getByText('Issue 2')).toBeInTheDocument();
    });
  });
});
