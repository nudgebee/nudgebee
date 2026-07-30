import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { NubiGlobalChatProvider, useNubiGlobalChat } from '@context/NubiGlobalChatContext';

jest.mock('next/router', () => ({
  useRouter: () => ({ query: { accountId: 'account-123' } }),
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
  const { isOpen, open, close, toggle } = useNubiGlobalChat();
  return (
    <div>
      <span data-testid='state'>{isOpen ? 'open' : 'closed'}</span>
      <button onClick={open}>open</button>
      <button onClick={close}>close</button>
      <button onClick={toggle}>toggle</button>
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
});
