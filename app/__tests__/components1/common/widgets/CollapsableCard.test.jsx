import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CollapsableCard from '@components1/common/widgets/CollapsableCard';

// Mock heavy dependencies
jest.mock('@lib/auth', () => ({
  hasWriteAccess: jest.fn(() => true),
}));

jest.mock(
  '@assets',
  () => ({
    WrenchIconOutline: 'wrench-icon',
    BetaIcon: 'beta-icon',
  }),
  { virtual: true }
);

jest.mock('@assets/kubernetes/sparkle.svg', () => 'sparkle-icon', { virtual: true });

jest.mock('@components1/common/widgets/HighLights', () => {
  const HighLights = ({ text, component }) => <div data-testid='highlights'>{component || text}</div>;
  HighLights.displayName = 'HighLights';
  return HighLights;
});

jest.mock('@components1/common/NewCustomButton', () => {
  const CustomButton = ({ text, onClick, variant: _variant, size: _size }) => (
    <button data-testid={`custom-btn-${text?.replace(/\s+/g, '-').toLowerCase()}`} onClick={onClick}>
      {text}
    </button>
  );
  CustomButton.displayName = 'CustomButton';
  return CustomButton;
});

jest.mock('@components1/common/SafeIcon', () => {
  const SafeIcon = ({ alt, src: _src }) => <img data-testid='safe-icon' alt={alt} />;
  SafeIcon.displayName = 'SafeIcon';
  return SafeIcon;
});

jest.mock('@components1/common/widgets/CustomLabels', () => {
  const CustomLabels = ({ text }) => <span data-testid='custom-labels'>{text}</span>;
  CustomLabels.displayName = 'CustomLabels';
  return CustomLabels;
});

jest.mock('@components1/common/CustomBorderCard', () => {
  const CustomBorderCard = ({ children, borderLeftColor: _borderLeftColor }) => (
    <div data-testid='custom-border-card' data-border-color={_borderLeftColor}>
      {children}
    </div>
  );
  CustomBorderCard.displayName = 'CustomBorderCard';
  return CustomBorderCard;
});

import { hasWriteAccess } from '@lib/auth';

const defaultProps = {
  id: 'card-1',
  icon: 'test-icon',
  text: 'Test Card',
  highlightsData: [],
  contentComponents: [],
  idx: 0,
  onCardClick: jest.fn(),
  expandedCardIndex: -1,
  collapsedObj: { 0: false },
  resolveButton: false,
  resolveButtonClick: null,
  ResolveComponent: null,
  isBeta: false,
  disabled: false,
};

describe('CollapsableCard', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    hasWriteAccess.mockReturnValue(true);
  });

  it('renders card text', () => {
    render(<CollapsableCard {...defaultProps} />);
    expect(screen.getByText('Test Card')).toBeInTheDocument();
  });

  it('renders "Nothing major to show" when no highlightsData', () => {
    render(<CollapsableCard {...defaultProps} />);
    expect(screen.getByText('Nothing major to show. Check the card for more.')).toBeInTheDocument();
  });

  it('calls onCardClick when card is clicked', () => {
    const onCardClick = jest.fn();
    render(<CollapsableCard {...defaultProps} onCardClick={onCardClick} />);
    // Click the grid container
    fireEvent.click(screen.getByText('Test Card').closest('div'));
    expect(onCardClick).toHaveBeenCalledWith(0);
  });

  it('calls onCardClick when icon button is clicked', () => {
    const onCardClick = jest.fn();
    render(<CollapsableCard {...defaultProps} onCardClick={onCardClick} />);
    // Find the IconButton by its role
    const buttons = screen.getAllByRole('button');
    fireEvent.click(buttons[0]);
    expect(onCardClick).toHaveBeenCalled();
  });

  it('renders with highlightsData items', () => {
    render(<CollapsableCard {...defaultProps} highlightsData={[{ message: 'Highlight 1', severity: 'Critical' }, { message: 'Highlight 2' }]} />);
    expect(screen.getByText('Highlight 1')).toBeInTheDocument();
  });

  it('renders "Show more" when highlightsData has more than MAX_VISIBLE', () => {
    render(<CollapsableCard {...defaultProps} highlightsData={[{ message: 'H1' }, { message: 'H2' }, { message: 'H3' }]} />);
    expect(screen.getByText('Show more (2)')).toBeInTheDocument();
  });

  it('toggles show all when "Show more" is clicked', () => {
    render(<CollapsableCard {...defaultProps} highlightsData={[{ message: 'H1' }, { message: 'H2' }, { message: 'H3' }]} />);
    const showMoreBtn = screen.getByText('Show more (2)');
    fireEvent.click(showMoreBtn);
    expect(screen.getByText('Show less')).toBeInTheDocument();
  });

  it('collapses back when "Show less" is clicked', () => {
    render(<CollapsableCard {...defaultProps} highlightsData={[{ message: 'H1' }, { message: 'H2' }]} />);
    const showMoreBtn = screen.getByText('Show more (1)');
    fireEvent.click(showMoreBtn);
    expect(screen.getByText('Show less')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Show less'));
    expect(screen.getByText('Show more (1)')).toBeInTheDocument();
  });

  it('renders isBeta icon when isBeta=true', () => {
    render(<CollapsableCard {...defaultProps} isBeta={true} />);
    const icons = screen.getAllByTestId('safe-icon');
    expect(icons.length).toBeGreaterThan(1); // At least the card icon + beta icon
  });

  it('renders with disabled=true', () => {
    render(<CollapsableCard {...defaultProps} disabled={true} />);
    expect(screen.getByText('Test Card')).toBeInTheDocument();
  });

  it('renders resolveButton with eventResolution', () => {
    render(<CollapsableCard {...defaultProps} resolveButton={true} eventResolution={{ status: 'InProgress', data: null }} />);
    expect(screen.getByTestId('custom-labels')).toBeInTheDocument();
    expect(screen.getByText('In Progress')).toBeInTheDocument();
  });

  it('renders resolveButton with eventResolution non-InProgress status', () => {
    render(<CollapsableCard {...defaultProps} resolveButton={true} eventResolution={{ status: 'Resolved', data: null }} />);
    expect(screen.getByText('Resolved')).toBeInTheDocument();
  });

  it('renders Fix it button when resolveButton=true, no eventResolution, has write access', () => {
    render(<CollapsableCard {...defaultProps} resolveButton={true} eventResolution={null} />);
    expect(screen.getByTestId('custom-btn-fix-it')).toBeInTheDocument();
  });

  it('does not render Fix it button when no write access', () => {
    hasWriteAccess.mockReturnValue(false);
    render(<CollapsableCard {...defaultProps} resolveButton={true} eventResolution={null} />);
    expect(screen.queryByTestId('custom-btn-fix-it')).not.toBeInTheDocument();
  });

  it('calls resolveButtonClick when Fix it is clicked', () => {
    const resolveButtonClick = jest.fn();
    render(<CollapsableCard {...defaultProps} resolveButton={true} eventResolution={null} resolveButtonClick={resolveButtonClick} />);
    fireEvent.click(screen.getByTestId('custom-btn-fix-it'));
    expect(resolveButtonClick).toHaveBeenCalledWith('card-1');
  });

  it('opens ResolveComponent when Fix it is clicked without resolveButtonClick', () => {
    const ResolveComp = jest.fn(({ open }) => (
      <div data-testid='resolve-comp' data-open={String(open)}>
        Resolve
      </div>
    ));
    render(
      <CollapsableCard {...defaultProps} resolveButton={true} eventResolution={null} resolveButtonClick={null} ResolveComponent={ResolveComp} />
    );
    fireEvent.click(screen.getByTestId('custom-btn-fix-it'));
    expect(screen.getByTestId('resolve-comp')).toHaveAttribute('data-open', 'true');
  });

  it('renders expanded content when isExpanded=true', () => {
    const ContentComp = () => <div data-testid='content-comp'>Content</div>;
    render(<CollapsableCard {...defaultProps} expandedCardIndex={0} collapsedObj={{ 0: true }} contentComponents={[ContentComp]} />);
    expect(screen.getByTestId('content-comp')).toBeInTheDocument();
  });

  it('renders eventResolution border card when expanded', () => {
    const ContentComp = () => <div>Content</div>;
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={{ status: 'InProgress', data: null }}
      />
    );
    expect(screen.getByTestId('custom-border-card')).toBeInTheDocument();
  });

  it('renders Failed border card with red color', () => {
    const ContentComp = () => <div>Content</div>;
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={{ status: 'Failed', status_message: 'Something went wrong', data: null }}
      />
    );
    expect(screen.getByTestId('custom-border-card')).toHaveAttribute('data-border-color', '#EF4444');
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });

  it('renders Retry button for Failed status with write access', () => {
    const ContentComp = () => <div>Content</div>;
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        resolveButton={true}
        eventResolution={{ status: 'Failed', data: null }}
      />
    );
    expect(screen.getByTestId('custom-btn-retry')).toBeInTheDocument();
  });

  it('calls resolveButtonClick on Retry when provided', () => {
    const ContentComp = () => <div>Content</div>;
    const resolveButtonClick = jest.fn();
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        resolveButton={true}
        resolveButtonClick={resolveButtonClick}
        eventResolution={{ status: 'Failed', data: null }}
      />
    );
    fireEvent.click(screen.getByTestId('custom-btn-retry'));
    expect(resolveButtonClick).toHaveBeenCalledWith('card-1');
  });

  it('opens ResolveComponent on Retry when resolveButtonClick is null', () => {
    const ContentComp = () => <div>Content</div>;
    const ResolveComp = jest.fn(({ open }) => (
      <div data-testid='resolve-comp' data-open={String(open)}>
        Resolve
      </div>
    ));
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        resolveButton={true}
        ResolveComponent={ResolveComp}
        eventResolution={{ status: 'Failed', data: null }}
      />
    );
    fireEvent.click(screen.getByTestId('custom-btn-retry'));
    expect(screen.getByTestId('resolve-comp')).toHaveAttribute('data-open', 'true');
  });

  // getResolutionDescription tests
  it('getResolutionDescription: returns empty for no data', () => {
    const ContentComp = () => <div>Content</div>;
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={{ status: 'InProgress', data: null }}
      />
    );
    // No crash
    expect(screen.getByTestId('custom-border-card')).toBeInTheDocument();
  });

  it('getResolutionDescription: parses JSON string data', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: JSON.stringify({ data: { size: '2Gi' } }),
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Resize to 2Gi')).toBeInTheDocument();
  });

  it('getResolutionDescription: increase_replicas', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: { increase_replicas: 5 } },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Increase replicas to 5')).toBeInTheDocument();
  });

  it('getResolutionDescription: restart pod', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: { restart: true } },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Restart pod')).toBeInTheDocument();
  });

  it('getResolutionDescription: revert deployment', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: { revert: true } },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Revert deployment')).toBeInTheDocument();
  });

  it('getResolutionDescription: imageNameWithTag', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: { imageNameWithTag: 'myimage:v2' } },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Change image to myimage:v2')).toBeInTheDocument();
  });

  it('getResolutionDescription: cordon node', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: { cordon: true } },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Cordon node')).toBeInTheDocument();
  });

  it('getResolutionDescription: drain node', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: { drain: true } },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Drain node')).toBeInTheDocument();
  });

  it('getResolutionDescription: container_name with all fields', () => {
    const ContentComp = () => <div>Content</div>;
    const containerData = {
      memory: { oldLimit: '256Mi', limit: '512Mi', oldRequest: '128Mi', request: '256Mi' },
      cpu: { oldLimit: '100m', limit: '200m', oldRequest: '50m', request: '100m' },
    };
    const resolution = {
      status: 'InProgress',
      data: {
        data: {
          container_name: 'my-container',
          'my-container': containerData,
          cloud_resourse: {
            meta: { controllerKind: 'Deployment', controller: 'my-deploy', namespace: 'default' },
          },
        },
      },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Memory Limit: 256Mi → 512Mi')).toBeInTheDocument();
    expect(screen.getByText('Memory Request: 128Mi → 256Mi')).toBeInTheDocument();
    expect(screen.getByText('CPU Limit: 100m → 200m')).toBeInTheDocument();
    expect(screen.getByText('CPU Request: 50m → 100m')).toBeInTheDocument();
  });

  it('getResolutionDescription: container_name with no cloud_resourse meta', () => {
    const ContentComp = () => <div>Content</div>;
    const containerData = {
      memory: { oldLimit: '256Mi', limit: '512Mi' },
      cpu: null,
    };
    const resolution = {
      status: 'InProgress',
      data: {
        data: {
          container_name: 'my-container',
          'my-container': containerData,
        },
      },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Memory Limit: 256Mi → 512Mi')).toBeInTheDocument();
  });

  it('getResolutionDescription: empty data object falls through to empty string', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { data: {} },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    // No crash, just empty description
    expect(screen.getByTestId('custom-border-card')).toBeInTheDocument();
  });

  it('getResolutionDescription: no data.data field returns empty', () => {
    const ContentComp = () => <div>Content</div>;
    const resolution = {
      status: 'InProgress',
      data: { someOtherKey: 'value' },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByTestId('custom-border-card')).toBeInTheDocument();
  });

  it('renders multiple content components when expanded', () => {
    const Comp1 = () => <div data-testid='comp1'>Component 1</div>;
    const Comp2 = () => <div data-testid='comp2'>Component 2</div>;
    render(<CollapsableCard {...defaultProps} expandedCardIndex={0} collapsedObj={{ 0: true }} contentComponents={[Comp1, Comp2]} />);
    expect(screen.getByTestId('comp1')).toBeInTheDocument();
    expect(screen.getByTestId('comp2')).toBeInTheDocument();
  });

  it('renders with maxWidth prop', () => {
    const { container } = render(<CollapsableCard {...defaultProps} maxWidth='1200px' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('renders with string highlights (not object)', () => {
    render(<CollapsableCard {...defaultProps} highlightsData={['Simple highlight string']} />);
    expect(screen.getByText('Simple highlight string')).toBeInTheDocument();
  });

  it('renders with highlight that has a component field', () => {
    const HighlightComp = <span data-testid='highlight-comp'>Custom Component</span>;
    render(<CollapsableCard {...defaultProps} highlightsData={[{ message: 'H1', component: HighlightComp }]} />);
    // HighLights is mocked - the component content will render
    expect(screen.getByTestId('highlights')).toBeInTheDocument();
  });

  it('does not open ResolveComponent initially', () => {
    const ResolveComp = jest.fn(({ open }) => (
      <div data-testid='resolve-comp' data-open={String(open)}>
        Resolve
      </div>
    ));
    render(<CollapsableCard {...defaultProps} ResolveComponent={ResolveComp} />);
    expect(screen.getByTestId('resolve-comp')).toHaveAttribute('data-open', 'false');
  });

  it('closeResolveComponent sets openResolveComponent to false', () => {
    const ResolveComp = ({ open, onCloseComponent }) => (
      <div>
        <span data-testid='resolve-status'>{String(open)}</span>
        <button data-testid='close-btn' onClick={onCloseComponent}>
          Close
        </button>
      </div>
    );
    render(<CollapsableCard {...defaultProps} resolveButton={true} eventResolution={null} ResolveComponent={ResolveComp} />);
    // Open it
    fireEvent.click(screen.getByTestId('custom-btn-fix-it'));
    expect(screen.getByTestId('resolve-status')).toHaveTextContent('true');
    // Close it
    fireEvent.click(screen.getByTestId('close-btn'));
    expect(screen.getByTestId('resolve-status')).toHaveTextContent('false');
  });

  it('getResolutionDescription: container with only memory limit change', () => {
    const ContentComp = () => <div>Content</div>;
    const containerData = {
      memory: { oldLimit: '256Mi', limit: '512Mi' },
    };
    const resolution = {
      status: 'InProgress',
      data: {
        data: {
          container_name: 'app',
          app: containerData,
          cloud_resourse: {
            meta: { controllerKind: 'Deployment', controller: 'app-deploy', namespace: 'prod' },
          },
        },
      },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByText('Memory Limit: 256Mi → 512Mi')).toBeInTheDocument();
  });

  it('getResolutionDescription: cpu with oldRequest null (skips cpu request)', () => {
    const ContentComp = () => <div>Content</div>;
    const containerData = {
      cpu: { oldLimit: '100m', limit: '200m', oldRequest: null, request: '100m' },
    };
    const resolution = {
      status: 'InProgress',
      data: {
        data: {
          container_name: 'app',
          app: containerData,
        },
      },
    };
    render(
      <CollapsableCard
        {...defaultProps}
        expandedCardIndex={0}
        collapsedObj={{ 0: true }}
        contentComponents={[ContentComp]}
        eventResolution={resolution}
      />
    );
    expect(screen.getByTestId('custom-border-card')).toBeInTheDocument();
  });
});
