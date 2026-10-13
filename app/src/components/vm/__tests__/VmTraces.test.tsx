import { render, screen } from '@testing-library/react';
import VmTraces from '@components/vm/VmTraces';
import apiAccount from '@api1/account';

jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: jest.fn() } }));
jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn(), query: {} }) }));
jest.mock('@components/k8s/details/KubernetesTracesListing', () => ({
  __esModule: true,
  default: (props: any) => <div data-testid='listing'>{props.accountId}</div>,
}));

const providerResponse = (provider: string) => ({ data: { data: { observability_get_default_provider: { provider } } } });

describe('VmTraces', () => {
  beforeEach(() => jest.clearAllMocks());

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
});
