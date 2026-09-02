import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import NubiBrainNav from '@components/common/layout/NubiBrainNav';

// Originally added by #32552 item-6: assert NubiBrainNav.fetchAgents
// skips listAgents on empty accountId. PR #32978 (RPC action collapse)
// inverted that contract — empty account_id is now routed to
// ListAgentsForTenant (system catalog + every custom agent the caller
// can read across the tenant). The empty-accountId case is now expected
// to fire, with the empty value flowing through to the backend.

const listAgents = jest.fn(() => Promise.resolve({ data: { data: { ai_list_agents: { data: [] } } } }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listAgents: (...a) => listAgents(...a) } }));
jest.mock('next/router', () => ({ useRouter: () => ({ query: {} }) }));
jest.mock('@components/llm/SettingsModal', () => () => null);
jest.mock('@components/llm/BCortexModal', () => () => null);
jest.mock('@components/common/icons/SafeIcon', () => () => null);

describe('NubiBrainNav fetchAgents (post-#32978 tenant-wide collapse)', () => {
  beforeEach(() => listAgents.mockClear());

  it('calls listAgents with empty accountId for the tenant-wide fetch', async () => {
    render(<NubiBrainNav accountId='' />);
    fireEvent.click(screen.getByTestId('nav-nubi-settings-btn'));
    await waitFor(() => expect(listAgents).toHaveBeenCalledTimes(1));
    expect(listAgents).toHaveBeenCalledWith({ accountId: '' });
  });

  it('calls listAgents with the accountId once it resolves', async () => {
    render(<NubiBrainNav accountId='acc-1' />);
    fireEvent.click(screen.getByTestId('nav-nubi-settings-btn'));
    await waitFor(() => expect(listAgents).toHaveBeenCalledTimes(1));
    expect(listAgents).toHaveBeenCalledWith({ accountId: 'acc-1' });
  });
});
