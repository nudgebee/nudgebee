import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import DecisionsTab from '@components/llm/memory2/DecisionsTab';

// Verifies the #32552 item-5 fix: DecisionsTab.saveEdit must keep the edit
// modal open (and preserve the draft) when the save fails, instead of closing
// it optimistically and discarding the user's typing.

const decisionsCorrect = jest.fn();
jest.mock('@api1/memory', () => ({
  decisionsList: jest.fn(() =>
    Promise.resolve({
      data: {
        entries: [{ id: 'd1', subject: 'Use blue-green deploys', rationale: 'safer rollbacks', agent_module: 'k8s_ops', decided_at: '2026-01-01' }],
      },
      errors: null,
    })
  ),
  decisionsCorrect: (...args) => decisionsCorrect(...args),
  decisionsPromoteToGlobal: jest.fn(() => Promise.resolve({ errors: null })),
}));

const toastError = jest.fn();
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: (...a) => toastError(...a) } }));
jest.mock('@lib/auth', () => ({ isTenantAdmin: () => false }));

// Lightweight DS passthroughs so we can drive the flow without portals/tooltips.
jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, children }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
}));
jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, disabled, ...p }) => (
    <button aria-label={p['aria-label']} disabled={disabled} onClick={onClick}>
      {children}
    </button>
  ),
}));
jest.mock('@ui/Input', () => ({
  Input: ({ value, onChange, label }) => <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />,
}));

describe('DecisionsTab saveEdit (#32552 item 5)', () => {
  beforeEach(() => {
    decisionsCorrect.mockReset();
    toastError.mockReset();
  });

  it('keeps the modal open and preserves the draft when the save fails', async () => {
    decisionsCorrect.mockResolvedValue({ errors: [{ message: 'boom' }] });
    render(<DecisionsTab scope='mine' accountId='acc-1' />);

    // Row loads, open the edit modal.
    fireEvent.click(await screen.findByLabelText('Edit'));
    expect(screen.getByTestId('modal')).toBeInTheDocument();

    // Click Save → the mocked decisionsCorrect rejects with errors.
    fireEvent.click(screen.getByText('Save'));

    await waitFor(() => expect(decisionsCorrect).toHaveBeenCalledTimes(1));
    // Modal must STILL be open (draft preserved) and an error toast shown.
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('Edit decision')).toBeInTheDocument();
    expect(toastError).toHaveBeenCalled();
  });

  it('closes the modal after a successful save', async () => {
    decisionsCorrect.mockResolvedValue({ errors: null });
    render(<DecisionsTab scope='mine' accountId='acc-1' />);

    fireEvent.click(await screen.findByLabelText('Edit'));
    fireEvent.click(screen.getByText('Save'));

    await waitFor(() => expect(screen.queryByTestId('modal')).not.toBeInTheDocument());
  });
});
