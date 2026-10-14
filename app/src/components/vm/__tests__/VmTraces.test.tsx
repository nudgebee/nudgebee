import { render, screen, waitFor } from '@testing-library/react';
import VmTraces from '@components/vm/VmTraces';
import apiAccount from '@api1/account';
import apiVm from '@api1/vm';

jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: jest.fn() } }));
jest.mock('@api1/vm', () => ({ __esModule: true, default: { getServiceMap: jest.fn() } }));
jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn(), query: {} }) }));
jest.mock('@components/k8s/details/KubernetesTracesListing', () => ({
  __esModule: true,
  default: (props: any) => (
    <div data-testid='listing' data-services={(props.serviceOptions || []).join(',')}>
      {props.accountId}
    </div>
  ),
}));

const providerResponse = (provider: string) => ({ data: { data: { observability_get_default_provider: { provider } } } });

describe('VmTraces', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (apiVm.getServiceMap as jest.Mock).mockResolvedValue([]);
  });

  it('lists traces for the account when a trace store is connected', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(providerResponse('jaeger'));
    render(<VmTraces accountId='account-1' />);

    expect(await screen.findByTestId('listing')).toHaveTextContent('account-1');
    expect(apiAccount.getDefaultProvider).toHaveBeenCalledWith({ account_id: 'account-1', provider_type: 'traces' });
  });

  it('says no trace store is connected when the account has none', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(providerResponse(''));
    render(<VmTraces accountId='account-1' />);

    expect(await screen.findByText('No trace store connected')).toBeInTheDocument();
    expect(screen.queryByTestId('listing')).not.toBeInTheDocument();
  });

  // Same lesson as the scan dialog (#39859): a failed lookup is not "nothing configured".
  it('reports a failed lookup with its reason, not as a missing trace store', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue({ data: { errors: [{ message: 'upstream timed out' }] } });
    render(<VmTraces accountId='account-1' />);

    expect(await screen.findByText(/upstream timed out/)).toBeInTheDocument();
    expect(screen.queryByText('No trace store connected')).not.toBeInTheDocument();
  });

  it("offers the VM's systemd units and containers in the Service filter, not other map nodes", async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(providerResponse('jaeger'));
    (apiVm.getServiceMap as jest.Mock).mockResolvedValue([
      { Id: { kind: 'SystemdService', name: 'sshd.service', namespace: 'host-a' } },
      { Id: { kind: 'DockerContainer', name: 'orders-cache', namespace: 'host-a' } },
      { Id: { kind: 'SystemdService', name: 'sshd.service', namespace: 'host-b' } },
      { Id: { kind: 'ExternalService', name: 'api.stripe.com', namespace: 'external' } },
      { Id: { kind: 'UnknownInternalHost', name: '172.31.9.9', namespace: 'unknown-internal' } },
    ]);
    render(<VmTraces accountId='account-1' />);

    const listing = await screen.findByTestId('listing');
    await waitFor(() => expect(listing).toHaveAttribute('data-services', 'orders-cache,sshd.service'));
  });
});
