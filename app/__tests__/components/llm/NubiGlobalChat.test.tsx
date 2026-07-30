import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NubiGlobalChat from '@components/llm/NubiGlobalChat';
import { useNubiGlobalChat } from '@context/NubiGlobalChatContext';

jest.mock('@utils/colors');

jest.mock('@context/NubiGlobalChatContext', () => ({
  __esModule: true,
  useNubiGlobalChat: jest.fn(),
  NUBI_GLOBAL_CHAT_SHORTCUT_LABEL: '⌘J',
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ assistantName: 'NuBi', nubiIconUrl: '/nubi-icon.svg' }),
}));

// SafeIcon pulls in next/image; stub it to avoid Image optimization in tests
jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: { alt: string }) => React.createElement('div', { 'data-testid': 'safe-icon', role: 'img', 'aria-label': alt }),
}));

// CustomDrawer renders its children only while open — mirror that so drawer
// content is present/absent based on `open`.
jest.mock('@shared/CustomDrawer', () => ({
  __esModule: true,
  default: ({ open, children }: { open: boolean; children: React.ReactNode }) => (open ? <div data-testid='custom-drawer'>{children}</div> : null),
}));

// Generator stub echoes the parent-driven signal props so we can assert the
// header controls bump them.
jest.mock('@components/llm/KubernetesLLMResponseGeneratorV2', () => ({
  __esModule: true,
  default: ({ accountId, newChatSignal, historySignal }: { accountId: string; newChatSignal: number; historySignal: number }) => (
    <div data-testid='llm-response-generator' data-account-id={accountId} data-new-chat-signal={newChatSignal} data-history-signal={historySignal} />
  ),
}));

const mockUseNubiGlobalChat = useNubiGlobalChat as jest.MockedFunction<typeof useNubiGlobalChat>;

const setContext = (overrides: Partial<ReturnType<typeof useNubiGlobalChat>> = {}) => {
  mockUseNubiGlobalChat.mockReturnValue({
    isOpen: false,
    open: jest.fn(),
    close: jest.fn(),
    toggle: jest.fn(),
    wipCount: 0,
    accountId: 'account-123',
    ...overrides,
  });
};

describe('NubiGlobalChat', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setContext();
  });

  it('renders the sticky launcher when the drawer is closed', () => {
    render(<NubiGlobalChat />);
    expect(screen.getByTestId('nubi-global-chat-launcher')).toBeInTheDocument();
  });

  it('opens the drawer when the launcher is clicked', () => {
    const open = jest.fn();
    setContext({ open });
    render(<NubiGlobalChat />);
    fireEvent.click(screen.getByTestId('nubi-global-chat-launcher'));
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('shows a WIP count badge when chats are generating', () => {
    setContext({ wipCount: 3 });
    render(<NubiGlobalChat />);
    const badge = screen.getByTestId('nubi-global-chat-wip-badge');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('3');
  });

  it('does not show the WIP badge when nothing is generating', () => {
    setContext({ wipCount: 0 });
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('nubi-global-chat-wip-badge')).not.toBeInTheDocument();
  });

  it('hides the launcher and shows the drawer + chat when open', () => {
    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('nubi-global-chat-launcher')).not.toBeInTheDocument();
    expect(screen.getByTestId('custom-drawer')).toBeInTheDocument();
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-account-id', 'account-123');
  });

  it('bumps the new-chat signal when the New chat control is clicked', () => {
    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-new-chat-signal', '0');
    fireEvent.click(screen.getByTestId('nubi-global-chat-new-chat'));
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-new-chat-signal', '1');
  });

  it('bumps the history signal when the History control is clicked', () => {
    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    fireEvent.click(screen.getByTestId('nubi-global-chat-history'));
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-history-signal', '1');
  });

  it('calls close when the drawer close control is clicked', () => {
    const close = jest.fn();
    setContext({ isOpen: true, close });
    render(<NubiGlobalChat />);
    fireEvent.click(screen.getByTestId('nubi-global-chat-close'));
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('prompts to select a cluster when no account is available', () => {
    setContext({ isOpen: true, accountId: '' });
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('llm-response-generator')).not.toBeInTheDocument();
    expect(screen.getByText('Please select a cluster to start chatting with NuBi')).toBeInTheDocument();
  });
});
