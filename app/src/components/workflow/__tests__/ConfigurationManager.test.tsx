import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import ConfigurationManager from '../ConfigurationManager';

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: { listConfigs: jest.fn(), saveConfig: jest.fn(), deleteConfig: jest.fn() },
}));

jest.mock('@lib/auth', () => ({
  hasPermission: () => false,
  hasWriteAccess: () => true,
  isTenantAdmin: () => true,
}));

import apiWorkflow from '@api1/workflow';

const mockList = (apiWorkflow as any).listConfigs as jest.Mock;
const mockSave = (apiWorkflow as any).saveConfig as jest.Mock;

const ACCOUNTS = [
  { value: 'acct-1', label: 'Account One' },
  { value: 'acct-2', label: 'Account Two' },
];

const openFormAndFill = async () => {
  fireEvent.click(await screen.findByRole('button', { name: /Add Config/ }));
  fireEvent.change(await screen.findByPlaceholderText('Enter configuration key'), { target: { value: 'k' } });
  fireEvent.change(screen.getByPlaceholderText('Enter configuration value'), { target: { value: 'v' } });
};

describe('ConfigurationManager account-scoped save', () => {
  beforeEach(() => {
    mockList.mockReset().mockResolvedValue({ data: { config_list: [] } });
    mockSave.mockReset().mockResolvedValue({ data: { config_save: { id: 'new-id' } } });
  });

  it('blocks saving an account-scoped config when no account is selected', async () => {
    render(<ConfigurationManager accountOptions={ACCOUNTS} open onClose={jest.fn()} />);
    await openFormAndFill();

    expect(screen.getByText('Account is required for an account-scoped configuration')).toBeInTheDocument();
    const save = screen.getByRole('button', { name: 'Save Configuration' });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(mockSave).not.toHaveBeenCalled();
  });

  it('saves with the account id when one is selected', async () => {
    render(<ConfigurationManager accountOptions={[ACCOUNTS[0]]} open onClose={jest.fn()} />);
    await openFormAndFill();

    const save = screen.getByRole('button', { name: 'Save Configuration' });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await waitFor(() => expect(mockSave).toHaveBeenCalledWith('acct-1', expect.objectContaining({ key: 'k', value: 'v' })));
  });
});

// #41486: Configs opened from the Automations list ignored the account already
// selected on the page and told the user to "try refreshing".
describe('ConfigurationManager starting account', () => {
  beforeEach(() => {
    mockList.mockReset().mockResolvedValue({ data: { config_list: [] } });
  });

  it("opens on the page's account and lists its configs", async () => {
    render(<ConfigurationManager accountOptions={ACCOUNTS} defaultAccountId='acct-2' open onClose={jest.fn()} />);
    await waitFor(() => expect(mockList).toHaveBeenCalledWith('acct-2'));
    expect(screen.getByText('Account Two')).toBeInTheDocument();
    expect(await screen.findByText('No configs in this account yet')).toBeInTheDocument();
    expect(screen.queryByText(/try refreshing/)).not.toBeInTheDocument();
  });

  it("picks the page's account once the account list arrives after mount", async () => {
    const { rerender } = render(<ConfigurationManager accountOptions={[]} defaultAccountId='acct-2' open={false} onClose={jest.fn()} />);
    rerender(<ConfigurationManager accountOptions={ACCOUNTS} defaultAccountId='acct-2' open onClose={jest.fn()} />);
    await waitFor(() => expect(mockList).toHaveBeenCalledWith('acct-2'));
  });

  it("reopens on the page's account when it changed since the last open", async () => {
    const onClose = jest.fn();
    const { rerender } = render(<ConfigurationManager accountOptions={ACCOUNTS} defaultAccountId='acct-1' open onClose={onClose} />);
    await waitFor(() => expect(mockList).toHaveBeenCalledWith('acct-1'));
    rerender(<ConfigurationManager accountOptions={ACCOUNTS} defaultAccountId='acct-2' open={false} onClose={onClose} />);
    rerender(<ConfigurationManager accountOptions={ACCOUNTS} defaultAccountId='acct-2' open onClose={onClose} />);
    await waitFor(() => expect(mockList).toHaveBeenLastCalledWith('acct-2'));
  });

  it("ignores a page account the user can't write configs in", async () => {
    render(<ConfigurationManager accountOptions={ACCOUNTS} defaultAccountId='acct-other' open onClose={jest.fn()} />);
    expect(await screen.findByText('Select an account to see its configs')).toBeInTheDocument();
    expect(mockList).not.toHaveBeenCalled();
  });

  it('asks to pick an account instead of suggesting a refresh', async () => {
    render(<ConfigurationManager accountOptions={ACCOUNTS} open onClose={jest.fn()} />);
    expect(await screen.findByText('Select an account to see its configs')).toBeInTheDocument();
    expect(screen.queryByText(/try refreshing/)).not.toBeInTheDocument();
  });

  it('leaves the Tenant (shared) view unchanged', async () => {
    render(<ConfigurationManager accountOptions={ACCOUNTS} open onClose={jest.fn()} />);
    fireEvent.click(await screen.findByRole('radio', { name: 'Tenant (shared)' }));
    await waitFor(() => expect(mockList).toHaveBeenCalledWith(''));
    expect(screen.queryByText('Select an account to see its configs')).not.toBeInTheDocument();
  });
});
