import React from 'react';
import { render, screen } from '@testing-library/react';
import NubiChatSidebar from '@shared/layout/NubiChatSidebar';

jest.mock('@utils/colors');

jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ assistantName: 'NuBi', nubiIconUrl: '/nubi-icon.svg' }),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: { alt: string }) => React.createElement('div', { role: 'img', 'aria-label': alt }),
}));

// Surface the props under test as data attributes instead of booting the real chat.
jest.mock('@components/llm/KubernetesLLMResponseGeneratorV2', () => ({
  __esModule: true,
  default: ({ persistLastSession, sessionId, workflowId }: any) => (
    <div data-testid='nubi-chat' data-persist-last-session={String(persistLastSession)} data-session-id={sessionId} data-workflow-id={workflowId} />
  ),
}));

describe('NubiChatSidebar', () => {
  // Restoring the shared pointer here loaded an unrelated workflow's chat.
  it('opts out of the shared last-session storage', () => {
    render(
      <NubiChatSidebar isVisible mode='fixed' accountId='account-123' context={{ type: 'workflow', data: { id: 'workflow-1' } }} showHeader={false} />
    );

    expect(screen.getByTestId('nubi-chat')).toHaveAttribute('data-persist-last-session', 'false');
  });

  it('forwards the workflow session id from the route/workflow row', () => {
    render(
      <NubiChatSidebar
        isVisible
        mode='fixed'
        accountId='account-123'
        context={{ type: 'workflow', data: { id: 'workflow-1' } }}
        urlSessionId='5290947c-f56e-4c15-bdbc-9e494fa36657'
        showHeader={false}
      />
    );

    const chat = screen.getByTestId('nubi-chat');
    expect(chat).toHaveAttribute('data-session-id', '5290947c-f56e-4c15-bdbc-9e494fa36657');
    expect(chat).toHaveAttribute('data-workflow-id', 'workflow-1');
  });
});
