import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', tertiary: '#6B7280' },
    background: { white: '#fff' },
    border: { secondary: '#D1D5DB', vertical: '#E5E7EB' },
  },
}));

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ alt, width, height }: { alt: string; width: number; height: number }) => <img alt={alt} width={width} height={height} />,
}));

jest.mock('@assets', () => ({
  CollapseLeftIcon: 'collapse-left.svg',
  NubiIcon: 'nubi-icon.svg',
}));

jest.mock('@components1/llm/KubernetesLLMResponseGeneratorV2', () => ({
  __esModule: true,
  default: ({ accountId }: { accountId: string }) => <div data-testid='llm-response-generator' data-account-id={accountId} />,
}));

jest.mock('uuid', () => ({
  v4: jest.fn(() => 'test-uuid-1234'),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ assistantName: 'NuBi' }),
}));

describe('NubiChatSidebar', () => {
  const defaultProps = {
    isVisible: true,
    accountId: 'account-123',
    onClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders header with assistant name when visible', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    expect(screen.getByText('NuBi Assistant')).toBeInTheDocument();
  });

  it('renders NuBi icon image', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    expect(screen.getByAltText('NuBi')).toBeInTheDocument();
  });

  it('renders close button when onClose is provided', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    fireEvent.click(screen.getByRole('button'));
    expect(defaultProps.onClose).toHaveBeenCalledTimes(1);
  });

  it('renders KubernetesLLMResponseGenerator when accountId is provided', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    expect(screen.getByTestId('llm-response-generator')).toBeInTheDocument();
  });

  it('passes accountId to KubernetesLLMResponseGenerator', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-account-id', 'account-123');
  });

  it('shows "select cluster" message when accountId is empty', () => {
    render(<NubiChatSidebar {...defaultProps} accountId='' />);
    expect(screen.getByText('Please select a cluster to start chatting with NuBi')).toBeInTheDocument();
  });

  it('does not render KubernetesLLMResponseGenerator when accountId is empty', () => {
    render(<NubiChatSidebar {...defaultProps} accountId='' />);
    expect(screen.queryByTestId('llm-response-generator')).not.toBeInTheDocument();
  });

  it('does not render header when showHeader is false', () => {
    render(<NubiChatSidebar {...defaultProps} showHeader={false} />);
    expect(screen.queryByText('NuBi Assistant')).not.toBeInTheDocument();
  });

  it('shows context type label when context is provided', () => {
    render(<NubiChatSidebar {...defaultProps} context={{ type: 'cluster' }} />);
    expect(screen.getByText('cluster Context')).toBeInTheDocument();
  });

  it('does not show context label when context is not provided', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    expect(screen.queryByText(/Context/)).not.toBeInTheDocument();
  });

  it('does not render close button when onClose is not provided', () => {
    const { container } = render(<NubiChatSidebar isVisible={true} accountId='account-123' />);
    expect(container.querySelector('button')).not.toBeInTheDocument();
  });

  it('calls onClose when Ctrl+K is pressed while visible', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(defaultProps.onClose).toHaveBeenCalledTimes(1);
  });

  it('does not call onClose for other key presses', () => {
    render(<NubiChatSidebar {...defaultProps} />);
    fireEvent.keyDown(window, { key: 'a', ctrlKey: true });
    expect(defaultProps.onClose).not.toHaveBeenCalled();
  });

  it('does not attach keyboard listener when enableKeyboardShortcut is false', () => {
    render(<NubiChatSidebar {...defaultProps} enableKeyboardShortcut={false} />);
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(defaultProps.onClose).not.toHaveBeenCalled();
  });
});
