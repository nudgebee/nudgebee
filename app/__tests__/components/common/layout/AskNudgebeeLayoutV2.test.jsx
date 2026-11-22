import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import AskNudgebeeLayout from '@components/common/layout/AskNudgebeeLayoutV2';

jest.mock('next/router', () => ({ useRouter: () => ({ query: {}, isReady: true, pathname: '/ask-nudgebee', replace: jest.fn() }) }));
jest.mock('next/dynamic', () => () => () => null);
jest.mock('@hooks/useBCortexEnabled', () => ({ isOSSDeploymentMode: () => false }));
jest.mock('@lib/auth', () => ({ hasAdminSurfaceAccess: () => true }));

describe("AskNudgebeeLayoutV2 (Nubi's sub-nav)", () => {
  it('renders the page content beside the sub-nav', () => {
    render(
      <AskNudgebeeLayout>
        <div>chat body</div>
      </AskNudgebeeLayout>
    );
    expect(screen.getByTestId('nubi-subnav')).toBeInTheDocument();
    expect(screen.getByText('chat body')).toBeInTheDocument();
  });

  it('wires New chat and Chats to their handlers', () => {
    const handleNewChat = jest.fn();
    const handleToggle = jest.fn();
    render(
      <AskNudgebeeLayout handleNewChat={handleNewChat} handleToggle={handleToggle}>
        <div />
      </AskNudgebeeLayout>
    );
    fireEvent.click(screen.getByRole('button', { name: 'New chat' }));
    fireEvent.click(screen.getByText('Chats'));
    expect(handleNewChat).toHaveBeenCalledTimes(1);
    expect(handleToggle).toHaveBeenCalledTimes(1);
  });

  it('carries AI & Tools but no back button, b-Cortex or account menu', () => {
    render(
      <AskNudgebeeLayout>
        <div />
      </AskNudgebeeLayout>
    );
    expect(screen.getByTestId('nav-nubi-ai-tools-btn')).toBeInTheDocument();
    expect(screen.queryByText('App')).not.toBeInTheDocument();
    expect(screen.queryByTestId('nav-bcortex-btn')).not.toBeInTheDocument();
    expect(screen.queryByTestId('sidebar-tenant-name')).not.toBeInTheDocument();
  });
});
