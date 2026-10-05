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
