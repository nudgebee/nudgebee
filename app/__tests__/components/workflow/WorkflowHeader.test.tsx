import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import WorkflowHeader from '@components/workflow/WorkflowHeader';
import homeApi from '@api1/home';

jest.mock('next/router', () => ({ useRouter: () => ({ query: {}, push: jest.fn() }) }));
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: jest.fn() } }));

const getCloudAccounts = homeApi.getCloudAccounts as jest.Mock;

const accounts = [
  { id: 'acct-a', account_name: 'k8s-prod', cloud_provider: 'K8s' },
  { id: 'acct-b', account_name: 'aws-prod', cloud_provider: 'AWS' },
];

const stripProps = { showStateStrip: true, liveVersionNumber: 3, liveVersionId: 'v-live', onPublish: jest.fn(), onHistory: jest.fn() };

describe('WorkflowHeader account chip', () => {
  let consoleError: jest.SpyInstance;

  beforeEach(() => {
    getCloudAccounts.mockReset();
    consoleError = jest.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    consoleError.mockRestore();
  });

  it("shows the name of the automation's own account", async () => {
    getCloudAccounts.mockResolvedValue(accounts);
    render(<WorkflowHeader {...stripProps} accountId='acct-b' />);
    expect(await screen.findByTestId('workflow-account-indicator')).toHaveTextContent('aws-prod');
  });

  it('shows no account, and keeps the rest of the strip, when the account list fails to load', async () => {
    getCloudAccounts.mockRejectedValue(new Error('network'));
    render(<WorkflowHeader {...stripProps} accountId='acct-a' />);
    await waitFor(() => expect(consoleError).toHaveBeenCalled());
    expect(screen.queryByTestId('workflow-account-indicator')).not.toBeInTheDocument();
    expect(screen.getByTestId('workflow-live-indicator')).toBeInTheDocument();
  });

  it('shows no account when the list does not include it', async () => {
    getCloudAccounts.mockResolvedValue(accounts);
    render(<WorkflowHeader {...stripProps} accountId='acct-gone' />);
    await waitFor(() => expect(getCloudAccounts).toHaveBeenCalled());
    expect(screen.queryByTestId('workflow-account-indicator')).not.toBeInTheDocument();
  });

  it('does not load accounts while the strip is hidden', () => {
    getCloudAccounts.mockResolvedValue(accounts);
    render(<WorkflowHeader showStateStrip={false} accountId='acct-a' />);
    expect(getCloudAccounts).not.toHaveBeenCalled();
  });
});
