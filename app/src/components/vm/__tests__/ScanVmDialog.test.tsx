import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ScanVmDialog from '@components/vm/ScanVmDialog';
import apiVm from '@api1/vm';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  default: { listDiscoveryTargets: jest.fn(), scanVm: jest.fn() },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const NOT_CONFIGURED = /No forager discovery datasource is configured for this account/;
const vm = { id: 'resource-1', name: 'i-0abc', resourse_id: 'i-0abc' } as any;

const renderDialog = () => render(<ScanVmDialog open accountId='account-1' vm={vm} onClose={jest.fn()} />);

describe('ScanVmDialog', () => {
  beforeEach(() => jest.clearAllMocks());

  // #39859: a 403 on the lookup was reported as "nothing configured", which sent
  // the user to configure a datasource that already existed.
  it('reports a failed lookup with its reason, not as a missing datasource', async () => {
    const log = jest.spyOn(console, 'error').mockImplementation(() => {});
    try {
      (apiVm.listDiscoveryTargets as jest.Mock).mockRejectedValue(new Error("Role 'account_admin' is not permitted to invoke 'integrations_list'"));
      renderDialog();

      expect(await screen.findByText(/Could not load the discovery datasources for this account/)).toHaveTextContent('is not permitted to invoke');
      expect(screen.queryByText(NOT_CONFIGURED)).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Start scan' })).toBeDisabled();
    } finally {
      log.mockRestore();
    }
  });

  it('says no datasource is configured only when the lookup succeeds and is empty', async () => {
    (apiVm.listDiscoveryTargets as jest.Mock).mockResolvedValue([]);
    renderDialog();

    expect(await screen.findByText(NOT_CONFIGURED)).toBeInTheDocument();
    expect(screen.queryByText(/Could not load the discovery datasources/)).not.toBeInTheDocument();
  });

  it('preselects the only datasource and starts the scan through it', async () => {
    (apiVm.listDiscoveryTargets as jest.Mock).mockResolvedValue([
      { id: 'ds-1', name: 'aws-forager-discovery', status: 'enabled', host: '', username: '' },
    ]);
    (apiVm.scanVm as jest.Mock).mockResolvedValue([]);
    renderDialog();

    const start = screen.getByRole('button', { name: 'Start scan' });
    await waitFor(() => expect(start).toBeEnabled());
    expect(screen.queryByText(NOT_CONFIGURED)).not.toBeInTheDocument();

    await userEvent.setup().click(start);
    expect(apiVm.scanVm).toHaveBeenCalledWith({ accountId: 'account-1', datasourceId: 'ds-1', cloudResourceId: 'resource-1' });
  });
});
