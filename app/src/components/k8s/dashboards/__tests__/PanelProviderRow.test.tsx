import { cleanup, render, screen } from '@testing-library/react';
import PanelProviderRow from '../PanelProviderRow';
import type { AccountOption } from '@api1/dashboards';
import type { AccountProvider } from '../panelProviders';

// The icon set resolves real SVG assets and is irrelevant to what these tests
// assert, which is the text the author actually reads.
jest.mock('@shared/icons/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }: { cloud_provider: string }) => <span data-testid='provider-icon'>{cloud_provider}</span>,
}));

const account = (label: string): AccountOption => ({ label, value: label, cloud_provider: 'K8S' });

const entry = (label: string, provider: string, available: string[] = [provider]): AccountProvider => ({
  account: account(label),
  provider,
  available,
  disabled: false,
});

const disabledEntry = (label: string): AccountProvider => ({
  account: { label, value: label, cloud_provider: 'K8S', status: 'disabled' },
  provider: '',
  available: [],
  disabled: true,
});

afterEach(cleanup);

describe('PanelProviderRow', () => {
  it('renders nothing once resolved with no accounts', () => {
    const { container } = render(<PanelProviderRow providerType='metrics' loading={false} entries={[]} total={0} declared='' />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows a skeleton only on the FIRST resolve, not on every account change', () => {
    // Re-resolving with entries already on screen must not blank the row back to
    // a skeleton — that flashes on every keystroke in the account multi-select.
    const { container: first } = render(<PanelProviderRow providerType='metrics' loading entries={[]} total={1} declared='' />);
    expect(first.querySelector('[aria-label="Resolving provider"]')).not.toBeNull();
    cleanup();
    const { container: later } = render(
      <PanelProviderRow providerType='metrics' loading entries={[entry('prod', 'prometheus')]} total={1} declared='' />
    );
    expect(later.querySelector('[aria-label="Resolving provider"]')).toBeNull();
    expect(screen.getByText('Prometheus')).toBeInTheDocument();
  });

  it('names the single provider and stays quiet', () => {
    render(<PanelProviderRow providerType='metrics' loading={false} entries={[entry('prod', 'prometheus')]} total={1} declared='' />);
    expect(screen.getByText('Prometheus')).toBeInTheDocument();
    expect(screen.queryByText(/different metrics providers/)).not.toBeInTheDocument();
    expect(screen.queryByText(/other/)).not.toBeInTheDocument();
  });

  it('counts the other providers configured on a single account', () => {
    render(
      <PanelProviderRow
        providerType='metrics'
        loading={false}
        entries={[entry('prod', 'prometheus', ['prometheus', 'datadog', 'chronosphere'])]}
        total={1}
        declared=''
      />
    );
    expect(screen.getByText('2 others configured')).toBeInTheDocument();
  });

  it('reports agreement across several accounts as one badge', () => {
    render(
      <PanelProviderRow
        providerType='logs'
        loading={false}
        entries={[entry('prod', 'loki'), entry('staging', 'loki'), entry('dev', 'loki')]}
        total={3}
        declared=''
      />
    );
    expect(screen.getByText('Loki')).toBeInTheDocument();
    expect(screen.getByText('all 3 accounts')).toBeInTheDocument();
  });

  it('warns when the accounts disagree, since one query cannot serve both', () => {
    render(
      <PanelProviderRow
        providerType='metrics'
        loading={false}
        entries={[entry('prod', 'prometheus'), entry('billing', 'datadog')]}
        total={2}
        declared=''
      />
    );
    expect(screen.getByText('Prometheus')).toBeInTheDocument();
    expect(screen.getByText('Datadog')).toBeInTheDocument();
    expect(screen.getByText(/different metrics providers/)).toBeInTheDocument();
  });

  it('escalates to the missing-provider banner instead of stacking two banners', () => {
    // Banner spec: one per surface, more severe wins.
    render(<PanelProviderRow providerType='traces' loading={false} entries={[entry('prod', 'jaeger'), entry('orphan', '')]} total={2} declared='' />);
    expect(screen.getByText(/No traces provider is configured for orphan/)).toBeInTheDocument();
    expect(screen.queryByText(/different traces providers/)).not.toBeInTheDocument();
  });

  it('says so when the fan-out was capped, rather than reporting a subset as the whole', () => {
    // A type-scoped panel ("every K8S account") can exceed MAX_PROVIDER_ACCOUNTS.
    render(
      <PanelProviderRow
        providerType='metrics'
        loading={false}
        entries={[entry('a', 'prometheus'), entry('b', 'prometheus')]}
        total={40}
        declared=''
      />
    );
    expect(screen.getByText('(first 2 of 40 accounts checked)')).toBeInTheDocument();
    expect(screen.queryByText('all 2 accounts')).not.toBeInTheDocument();
  });

  describe('when the panel names its provider', () => {
    it('stays silent when every account can answer it — the Select already said which', () => {
      const { container } = render(
        <PanelProviderRow
          providerType='metrics'
          loading={false}
          entries={[entry('prod', 'prometheus'), entry('staging', 'prometheus')]}
          total={2}
          declared='prometheus'
        />
      );
      expect(container).toBeEmptyDOMElement();
    });

    it('accepts an account that merely HAS the provider, not only one defaulting to it', () => {
      // Naming a provider overrides the account default on the request, so an
      // account with Datadog as default and Prometheus configured answers fine.
      const { container } = render(
        <PanelProviderRow
          providerType='metrics'
          loading={false}
          entries={[entry('prod', 'datadog', ['datadog', 'prometheus'])]}
          total={1}
          declared='prometheus'
        />
      );
      expect(container).toBeEmptyDOMElement();
    });

    it('names the accounts that cannot answer it, and what to do about them', () => {
      render(
        <PanelProviderRow
          providerType='metrics'
          loading={false}
          entries={[entry('prod', 'prometheus'), entry('legacy', 'ES')]}
          total={2}
          declared='prometheus'
        />
      );
      expect(screen.getByText(/legacy does not have Prometheus configured for metrics/)).toBeInTheDocument();
      expect(screen.getByText(/add a second panel/)).toBeInTheDocument();
    });

    it('escalates when NO account can answer it — the panel cannot render at all', () => {
      render(<PanelProviderRow providerType='logs' loading={false} entries={[entry('a', 'ES'), entry('b', 'ES')]} total={2} declared='loki' />);
      const banner = screen.getByText(/a, b do not have Loki configured for logs/);
      expect(banner).toBeInTheDocument();
    });
  });

  describe('when an account is disabled', () => {
    it('names it instead of reporting a missing provider', () => {
      render(
        <PanelProviderRow
          providerType='metrics'
          loading={false}
          entries={[entry('live', 'prometheus'), disabledEntry('off')]}
          total={2}
          declared=''
        />
      );
      expect(screen.getByText(/off is disabled and will return nothing/)).toBeInTheDocument();
      expect(screen.queryByText(/No metrics provider is configured/)).not.toBeInTheDocument();
      // The live account is still described.
      expect(screen.getByText('Prometheus')).toBeInTheDocument();
    });

    it('escalates when every account is disabled', () => {
      render(<PanelProviderRow providerType='logs' loading={false} entries={[disabledEntry('a'), disabledEntry('b')]} total={2} declared='' />);
      expect(screen.getByText(/This panel has no live account to query/)).toBeInTheDocument();
    });

    it('outranks the provider-mismatch warning, which is not the actionable problem', () => {
      render(
        <PanelProviderRow
          providerType='metrics'
          loading={false}
          entries={[entry('live', 'ES'), disabledEntry('off')]}
          total={2}
          declared='prometheus'
        />
      );
      expect(screen.getByText(/off is disabled/)).toBeInTheDocument();
      expect(screen.queryByText(/does not have Prometheus configured/)).not.toBeInTheDocument();
    });
  });

  it('labels CloudWatch by its product name and gives it the AWS glyph', () => {
    render(<PanelProviderRow providerType='metrics' loading={false} entries={[entry('billing', 'aws_cloudwatch')]} total={1} declared='' />);
    expect(screen.getByText('CloudWatch')).toBeInTheDocument();
    expect(screen.getByTestId('provider-icon')).toHaveTextContent('aws');
  });
});
