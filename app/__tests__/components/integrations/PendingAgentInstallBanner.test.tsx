import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import PendingAgentInstallBanner from '@components/integrations/PendingAgentInstallBanner';
import { addPendingAgentInstall } from '@components/integrations/pendingAgentInstall';
import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import { hasWriteAccess } from '@lib/auth';
import { toast } from '@ui/Toast';

jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 'tenant-1' }), hasWriteAccess: jest.fn(() => true) }));
jest.mock('next-auth/react', () => ({ useSession: () => ({ data: { tenant: { id: 'tenant-1' } } }) }));
jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { listK8sAccAgentHealth: jest.fn(() => Promise.resolve({ data: {} })) } }));
jest.mock('@api1/account', () => ({ __esModule: true, default: { generateAgentToken: jest.fn() } }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn(), info: jest.fn() } }));

const generateAgentToken = apiAccount.generateAgentToken as jest.Mock;
const writeText = jest.fn(() => Promise.resolve());

describe('PendingAgentInstallBanner — Renew and copy command', () => {
  beforeEach(() => {
    localStorage.clear();
    jest.clearAllMocks();
    (apiKubernetes1.listK8sAccAgentHealth as jest.Mock).mockResolvedValue({ data: {} });
    (hasWriteAccess as jest.Mock).mockReturnValue(true);
    Object.assign(navigator, { clipboard: { writeText } });
  });

  it('renews the token and copies the install command for a pending cluster', async () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    generateAgentToken.mockResolvedValue({ data: { data: { agents_create_token: { access_key: 'key1', access_secret: 'secret1' } } } });

    render(<PendingAgentInstallBanner />);
    fireEvent.click(await screen.findByRole('button', { name: 'Renew and copy command' }));

    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(generateAgentToken).toHaveBeenCalledWith('acc-1');
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('bash installation.sh -a "key1:secret1"'));
    expect(toast.success).toHaveBeenCalled();
  });

  it('reports a failure and copies nothing when no token comes back', async () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    generateAgentToken.mockResolvedValue(null);

    render(<PendingAgentInstallBanner />);
    fireEvent.click(await screen.findByRole('button', { name: 'Renew and copy command' }));

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(writeText).not.toHaveBeenCalled();
  });

  it('shows the command instead of losing it when the clipboard refuses', async () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    generateAgentToken.mockResolvedValue({ data: { data: { agents_create_token: { access_key: 'key1', access_secret: 'secret1' } } } });
    writeText.mockRejectedValueOnce(new Error('NotAllowedError'));

    render(<PendingAgentInstallBanner />);
    fireEvent.click(await screen.findByRole('button', { name: 'Renew and copy command' }));

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(toast.error).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ description: expect.stringContaining('key1:secret1') }));
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('does not mint a token when the agent has connected since the last poll', async () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    // First call is the banner's own poll on mount (still pending); the second is
    // the re-check made when Renew is clicked, by which time the agent has connected.
    (apiKubernetes1.listK8sAccAgentHealth as jest.Mock)
      .mockResolvedValueOnce({ data: {} })
      .mockResolvedValueOnce({ data: { data: { agent: [{ cloud_account_id: 'acc-1', status: 'CONNECTED' }] } } });

    render(<PendingAgentInstallBanner />);
    fireEvent.click(await screen.findByRole('button', { name: 'Renew and copy command' }));

    await waitFor(() => expect(toast.info).toHaveBeenCalled());
    expect(generateAgentToken).not.toHaveBeenCalled();
    expect(writeText).not.toHaveBeenCalled();
  });

  it('names the cluster on each button when there are two', async () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    addPendingAgentInstall('acc-2', 'Cluster B');

    render(<PendingAgentInstallBanner />);

    expect(await screen.findByRole('button', { name: 'Renew and copy command for "Cluster A"' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Renew and copy command for "Cluster B"' })).toBeInTheDocument();
  });

  it('shows no button without write access, for VM accounts, or past two clusters', async () => {
    (hasWriteAccess as jest.Mock).mockReturnValue(false);
    addPendingAgentInstall('acc-1', 'Cluster A');
    const { unmount } = render(<PendingAgentInstallBanner />);
    await screen.findByTestId('pending-agent-install-banner');
    expect(screen.queryByRole('button', { name: /Renew and copy command/ })).not.toBeInTheDocument();
    unmount();

    (hasWriteAccess as jest.Mock).mockReturnValue(true);
    localStorage.clear();
    addPendingAgentInstall('vm-1', 'Fleet', 'vm');
    const vm = render(<PendingAgentInstallBanner />);
    await screen.findByTestId('pending-agent-install-banner');
    expect(screen.queryByRole('button', { name: /Renew and copy command/ })).not.toBeInTheDocument();
    vm.unmount();

    localStorage.clear();
    ['a', 'b', 'c'].forEach((id) => addPendingAgentInstall(id, `Cluster ${id}`));
    render(<PendingAgentInstallBanner />);
    await screen.findByTestId('pending-agent-install-banner');
    expect(screen.queryByRole('button', { name: /Renew and copy command/ })).not.toBeInTheDocument();
  });
});
