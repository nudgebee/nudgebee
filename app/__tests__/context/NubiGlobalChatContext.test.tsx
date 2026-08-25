import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { NubiGlobalChatProvider, useNubiGlobalChat } from '@context/NubiGlobalChatContext';

const mockPathname = { current: '/optimise' };
const mockPageAccount = { current: 'account-123' as string | undefined };
jest.mock('next/router', () => ({
  useRouter: () => ({ pathname: mockPathname.current, query: { accountId: mockPageAccount.current } }),
}));

jest.mock('@context/DataContext', () => ({
  useData: () => ({ selectedCluster: { value: 'account-123' } }),
}));

jest.mock('@lib/auth', () => ({
  getUserSession: () => ({ user: { email: 'user@example.com' } }),
}));

// These tests cover the open/close/shortcut contract, not the WIP poll. Stub
// the history call with a promise that never resolves so no post-test state
// update fires (which would otherwise log an act() warning).
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { llmConversationHistory: jest.fn(() => new Promise(() => {})) },
}));

const Probe: React.FC = () => {
  const { isOpen, open, close, toggle, accountId, chatContext, openWithContext, clearChatContext, pageAccountId, setChatAccount } =
    useNubiGlobalChat();
  return (
    <div>
      <span data-testid='state'>{isOpen ? 'open' : 'closed'}</span>
      <span data-testid='account'>{accountId}</span>
      <span data-testid='page-account'>{pageAccountId}</span>
      <button onClick={() => setChatAccount('picked-account-7')}>pick</button>
      <span data-testid='session'>{chatContext?.sessionId ?? ''}</span>
      <span data-testid='query'>{chatContext?.query ?? ''}</span>
      <button onClick={open}>open</button>
      <button onClick={close}>close</button>
      <button onClick={toggle}>toggle</button>
      <button onClick={() => openWithContext({ accountId: 'row-account-9', sessionId: 'recom_42', query: 'Analyze this' })}>ask</button>
      <button onClick={clearChatContext}>clear</button>
    </div>
  );
};

const renderProvider = () =>
  render(
    <NubiGlobalChatProvider>
      <Probe />
    </NubiGlobalChatProvider>
  );

describe('NubiGlobalChatProvider', () => {
  beforeEach(() => {
    mockPathname.current = '/optimise';
    mockPageAccount.current = 'account-123';
  });

  describe('chat account selection', () => {
    const rerenderProvider = (rerender: (ui: React.ReactElement) => void) =>
      rerender(
        <NubiGlobalChatProvider>
          <Probe />
        </NubiGlobalChatProvider>
      );

    it('points the chat at the picked account without moving the page account', () => {
      renderProvider();
      fireEvent.click(screen.getByText('pick'));
      expect(screen.getByTestId('account')).toHaveTextContent('picked-account-7');
      expect(screen.getByTestId('page-account')).toHaveTextContent('account-123');
    });

    it('drops the conversation when the account is picked, keeping only the account', () => {
      renderProvider();
      fireEvent.click(screen.getByText('ask'));
      expect(screen.getByTestId('session')).toHaveTextContent('recom_42');
      fireEvent.click(screen.getByText('pick'));
      expect(screen.getByTestId('session')).toBeEmptyDOMElement();
      expect(screen.getByTestId('query')).toBeEmptyDOMElement();
    });

    it('moves the chat back when the page account changes', () => {
      const { rerender } = renderProvider();
      fireEvent.click(screen.getByText('pick'));
      expect(screen.getByTestId('account')).toHaveTextContent('picked-account-7');

      mockPageAccount.current = 'account-999';
      rerenderProvider(rerender);

      expect(screen.getByTestId('account')).toHaveTextContent('account-999');
    });

    it('keeps the picked account while the page account is unchanged', () => {
      const { rerender } = renderProvider();
      fireEvent.click(screen.getByText('pick'));
      rerenderProvider(rerender);
      expect(screen.getByTestId('account')).toHaveTextContent('picked-account-7');
    });

    it('does not treat the page account first resolving as a change', () => {
      mockPageAccount.current = undefined;
      const { rerender } = renderProvider();
      fireEvent.click(screen.getByText('pick'));
      expect(screen.getByTestId('account')).toHaveTextContent('picked-account-7');

      // selectedCluster resolves a beat later — a load, not a user switching accounts.
      mockPageAccount.current = 'account-123';
      rerenderProvider(rerender);

      expect(screen.getByTestId('account')).toHaveTextContent('picked-account-7');
    });
  });

  it('starts closed', () => {
    renderProvider();
    expect(screen.getByTestId('state')).toHaveTextContent('closed');
  });

  it('open() and close() control the drawer state', () => {
    renderProvider();
    fireEvent.click(screen.getByText('open'));
    expect(screen.getByTestId('state')).toHaveTextContent('open');
    fireEvent.click(screen.getByText('close'));
    expect(screen.getByTestId('state')).toHaveTextContent('closed');
  });

  it('toggle() flips the drawer state', () => {
    renderProvider();
    fireEvent.click(screen.getByText('toggle'));
    expect(screen.getByTestId('state')).toHaveTextContent('open');
    fireEvent.click(screen.getByText('toggle'));
    expect(screen.getByTestId('state')).toHaveTextContent('closed');
  });

  it('Cmd/Ctrl+J toggles the drawer from anywhere', () => {
    renderProvider();
    act(() => {
      fireEvent.keyDown(window, { key: 'j', metaKey: true });
    });
    expect(screen.getByTestId('state')).toHaveTextContent('open');
    act(() => {
      fireEvent.keyDown(window, { key: 'j', metaKey: true });
    });
    expect(screen.getByTestId('state')).toHaveTextContent('closed');
  });

  it('ignores other shortcuts', () => {
    renderProvider();
    act(() => {
      fireEvent.keyDown(window, { key: 'k', metaKey: true });
    });
    expect(screen.getByTestId('state')).toHaveTextContent('closed');
  });

  it('openWithContext opens the drawer preloaded with the caller context', () => {
    renderProvider();
    fireEvent.click(screen.getByText('ask'));
    expect(screen.getByTestId('state')).toHaveTextContent('open');
    expect(screen.getByTestId('session')).toHaveTextContent('recom_42');
    expect(screen.getByTestId('query')).toHaveTextContent('Analyze this');
  });

  it("the caller's account overrides the page account", () => {
    renderProvider();
    expect(screen.getByTestId('account')).toHaveTextContent('account-123');
    fireEvent.click(screen.getByText('ask'));
    expect(screen.getByTestId('account')).toHaveTextContent('row-account-9');
  });

  it('clearChatContext drops the conversation but keeps the account', () => {
    renderProvider();
    fireEvent.click(screen.getByText('ask'));
    fireEvent.click(screen.getByText('clear'));
    expect(screen.getByTestId('session')).toBeEmptyDOMElement();
    expect(screen.getByTestId('query')).toBeEmptyDOMElement();
    expect(screen.getByTestId('account')).toHaveTextContent('row-account-9');
  });

  it('drops a stale caller context when navigating with the drawer closed', () => {
    const { rerender } = renderProvider();
    fireEvent.click(screen.getByText('ask'));
    fireEvent.click(screen.getByText('close'));

    mockPathname.current = '/kubernetes/details';
    rerender(
      <NubiGlobalChatProvider>
        <Probe />
      </NubiGlobalChatProvider>
    );

    expect(screen.getByTestId('session')).toBeEmptyDOMElement();
    expect(screen.getByTestId('account')).toHaveTextContent('account-123');
  });

  it('keeps the conversation when navigating with the drawer open', () => {
    const { rerender } = renderProvider();
    fireEvent.click(screen.getByText('ask'));

    mockPathname.current = '/kubernetes/details';
    rerender(
      <NubiGlobalChatProvider>
        <Probe />
      </NubiGlobalChatProvider>
    );

    expect(screen.getByTestId('state')).toHaveTextContent('open');
    expect(screen.getByTestId('session')).toHaveTextContent('recom_42');
    expect(screen.getByTestId('account')).toHaveTextContent('row-account-9');
  });

  it('ignores Cmd/Ctrl+J on the dedicated Nubi page', () => {
    mockPathname.current = '/ask-nudgebee';
    renderProvider();
    act(() => {
      fireEvent.keyDown(window, { key: 'j', metaKey: true });
    });
    expect(screen.getByTestId('state')).toHaveTextContent('closed');
  });
});
