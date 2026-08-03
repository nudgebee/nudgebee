import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NubiGlobalChat from '@components/llm/NubiGlobalChat';
import { useNubiGlobalChat } from '@context/NubiGlobalChatContext';

jest.mock('@utils/colors');

const mockPathname = { current: '/optimise' };
jest.mock('next/router', () => ({ useRouter: () => ({ pathname: mockPathname.current, query: {} }) }));

jest.mock('@context/NubiGlobalChatContext', () => ({
  __esModule: true,
  useNubiGlobalChat: jest.fn(),
  NUBI_GLOBAL_CHAT_SHORTCUT_LABEL: '⌘J',
  isNubiFullPageRoute: (pathname: string) => pathname.startsWith('/ask-nudgebee'),
  isNubiLauncherHiddenRoute: (pathname: string) => ['/investigate', '/automation/[workflowId]'].some((route: string) => pathname.startsWith(route)),
}));

jest.mock('@context/DataContext', () => ({
  useData: () => ({
    allCluster: [
      { value: 'account-123', label: 'k8s-dev', cloud_provider: 'K8S' },
      { value: 'account-456', label: 'gcp-test', cloud_provider: 'GCP' },
    ],
  }),
}));
jest.mock('@shared/layout/UpdateDataContext', () => ({ useUpdateAllClusterOption: () => jest.fn() }));

// The account picker is the DS FilterDropdown (chip trigger + grouped panel). Stub it
// as a plain select so a choice can be made without driving its popover.
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ value, options, onSelect, grouped }: any) => (
    <select
      data-testid='nubi-global-chat-account-select'
      data-grouped={grouped ? 'true' : undefined}
      value={value?.value ?? ''}
      onChange={(e) => onSelect({}, (options || []).find((o: any) => o.value === e.target.value) || null)}
    >
      <option value='' />
      {(options || []).map((o: any) => (
        <option key={o.value} value={o.value} data-group={o.group}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ assistantName: 'NuBi', nubiIconUrl: '/nubi-icon.svg' }),
  useBrandingConfig: () => ({ isWhiteLabel: false, loading: false }),
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
  default: ({ open, children, aboveModal }: { open: boolean; children: React.ReactNode; aboveModal?: boolean }) =>
    open ? (
      <div data-testid='custom-drawer' data-above-modal={aboveModal ? 'true' : undefined}>
        {children}
      </div>
    ) : null,
}));

// Generator stub echoes the parent-driven signal props so we can assert the
// header controls bump them.
jest.mock('@components/llm/KubernetesLLMResponseGeneratorV2', () => ({
  __esModule: true,
  default: ({
    accountId,
    newChatSignal,
    historySignal,
    sessionId,
    query,
    source,
  }: {
    accountId: string;
    newChatSignal: number;
    historySignal: number;
    sessionId: string;
    query: string;
    source: string;
  }) => (
    <div
      data-testid='llm-response-generator'
      data-account-id={accountId}
      data-new-chat-signal={newChatSignal}
      data-history-signal={historySignal}
      data-session-id={sessionId}
      data-query={query}
      data-source={source}
    />
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
    chatContext: null,
    openWithContext: jest.fn(),
    clearChatContext: jest.fn(),
    pageAccountId: 'account-123',
    setChatAccount: jest.fn(),
    ...overrides,
  });
};

describe('NubiGlobalChat', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockPathname.current = '/optimise';
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
    // Starts undefined so the generator doesn't fire a new chat on mount.
    expect(screen.getByTestId('llm-response-generator')).not.toHaveAttribute('data-new-chat-signal');
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

  it('renders nothing on the dedicated Nubi page', () => {
    mockPathname.current = '/ask-nudgebee';
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('nubi-global-chat-launcher')).not.toBeInTheDocument();
  });

  it('renders no drawer on the dedicated Nubi page even if the chat was left open', () => {
    mockPathname.current = '/ask-nudgebee';
    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('custom-drawer')).not.toBeInTheDocument();
  });

  it('hides only the launcher on pages that own their opener', () => {
    mockPathname.current = '/investigate';
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('nubi-global-chat-launcher')).not.toBeInTheDocument();
  });

  it('hides the launcher on the workflow builder, which has its own docked chat', () => {
    mockPathname.current = '/automation/[workflowId]';
    render(<NubiGlobalChat />);
    expect(screen.queryByTestId('nubi-global-chat-launcher')).not.toBeInTheDocument();
  });

  it('keeps the launcher on the automation listing page, which has no chat of its own', () => {
    mockPathname.current = '/automation';
    render(<NubiGlobalChat />);
    expect(screen.getByTestId('nubi-global-chat-launcher')).toBeInTheDocument();
  });

  it('still opens the drawer on pages that own their opener', () => {
    mockPathname.current = '/investigate';
    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    expect(screen.getByTestId('custom-drawer')).toBeInTheDocument();
    expect(screen.getByTestId('llm-response-generator')).toBeInTheDocument();
  });

  it('forwards the caller context to the chat', () => {
    setContext({
      isOpen: true,
      accountId: 'row-account-9',
      chatContext: { accountId: 'row-account-9', sessionId: 'recom_42', query: 'Analyze this', categorySource: 'Optimize' },
    });
    render(<NubiGlobalChat />);
    const generator = screen.getByTestId('llm-response-generator');
    expect(generator).toHaveAttribute('data-account-id', 'row-account-9');
    expect(generator).toHaveAttribute('data-session-id', 'recom_42');
    expect(generator).toHaveAttribute('data-query', 'Analyze this');
  });

  it("routes the caller's source to the chat, defaulting when unset", () => {
    setContext({ isOpen: true, chatContext: { sessionId: 'log_abc', source: 'log_analysis' } });
    const { unmount } = render(<NubiGlobalChat />);
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-source', 'log_analysis');
    unmount();

    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-source', 'ask_nudgbee_chat');
  });

  it('lifts the drawer above the modal layer when the opener lives in a Dialog', () => {
    setContext({ isOpen: true, chatContext: { sessionId: 'log_abc', aboveModal: true } });
    render(<NubiGlobalChat />);
    expect(screen.getByTestId('custom-drawer')).toHaveAttribute('data-above-modal', 'true');
  });

  it('offers every reachable account, grouped by cloud provider, on the current one', () => {
    setContext({ isOpen: true });
    render(<NubiGlobalChat />);
    const select = screen.getByTestId('nubi-global-chat-account-select');
    expect(select).toHaveAttribute('data-grouped', 'true');
    expect(select).toHaveValue('account-123');
    expect(screen.getByText('k8s-dev')).toHaveAttribute('data-group', 'K8S');
    expect(screen.getByText('gcp-test')).toHaveAttribute('data-group', 'GCP');
  });

  it('hands the chat back to the page account when the filter is cleared', () => {
    const setChatAccount = jest.fn();
    setContext({ isOpen: true, setChatAccount });
    render(<NubiGlobalChat />);
    fireEvent.change(screen.getByTestId('nubi-global-chat-account-select'), { target: { value: '' } });
    expect(setChatAccount).toHaveBeenCalledWith('');
  });

  it('points the chat at the picked account', () => {
    const setChatAccount = jest.fn();
    setContext({ isOpen: true, setChatAccount });
    render(<NubiGlobalChat />);
    fireEvent.change(screen.getByTestId('nubi-global-chat-account-select'), { target: { value: 'account-456' } });
    expect(setChatAccount).toHaveBeenCalledWith('account-456');
  });

  it('starts a fresh chat when the account changes underneath an open conversation', () => {
    setContext({ isOpen: true, accountId: 'account-123' });
    const { rerender } = render(<NubiGlobalChat />);
    expect(screen.getByTestId('llm-response-generator')).not.toHaveAttribute('data-new-chat-signal');

    setContext({ isOpen: true, accountId: 'account-456' });
    rerender(<NubiGlobalChat />);

    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-new-chat-signal', '1');
    expect(screen.getByTestId('llm-response-generator')).toHaveAttribute('data-account-id', 'account-456');
  });

  it('does not reset when a row trigger brings its own account and conversation', () => {
    setContext({ isOpen: true, accountId: 'account-123' });
    const { rerender } = render(<NubiGlobalChat />);

    // "Ask Nubi" on a row belonging to a different account than the page.
    setContext({
      isOpen: true,
      accountId: 'account-456',
      chatContext: { accountId: 'account-456', sessionId: 'recom_42', query: 'Analyze this' },
    });
    rerender(<NubiGlobalChat />);

    const generator = screen.getByTestId('llm-response-generator');
    expect(generator).not.toHaveAttribute('data-new-chat-signal');
    expect(generator).toHaveAttribute('data-session-id', 'recom_42');
  });

  it('does not start a fresh chat when the account first resolves', () => {
    setContext({ isOpen: true, accountId: '' });
    const { rerender } = render(<NubiGlobalChat />);

    setContext({ isOpen: true, accountId: 'account-123' });
    rerender(<NubiGlobalChat />);

    expect(screen.getByTestId('llm-response-generator')).not.toHaveAttribute('data-new-chat-signal');
  });

  it('clears the preloaded conversation when a new chat is started', () => {
    const clearChatContext = jest.fn();
    setContext({ isOpen: true, chatContext: { sessionId: 'recom_42', query: 'Analyze this' }, clearChatContext });
    render(<NubiGlobalChat />);
    fireEvent.click(screen.getByTestId('nubi-global-chat-new-chat'));
    expect(clearChatContext).toHaveBeenCalledTimes(1);
  });
});
