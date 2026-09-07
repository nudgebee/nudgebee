import { render, screen, waitFor } from '@testing-library/react';
import EffectiveLabelMappingPanel from '@components/integrations/modal/EffectiveLabelMappingPanel';
import observability from '@api1/observability';

// The panel is the one component that renders "which setting decided this". It now
// serves logs AND traces off the same server resolver, so what needs pinning is that
// the signal reaches the server, and that a second instance on the same form does not
// collide with the log instance's testids — which app-e2e-tests binds to.

jest.mock('@api1/observability', () => ({ __esModule: true, default: { getLabelMapping: jest.fn() } }));

const response = (overrides = {}) => ({
  account_id: 'acc-1',
  provider: 'datadog',
  provider_source: 'user',
  provider_type: 'traces',
  integration_saved: true,
  draft_applied: false,
  tier_order: ['provider_default', 'tenant', 'account', 'provider_config', 'integration'],
  fields: [
    {
      canonical: 'service_name',
      effective: 'service.name',
      winning_tier: 'integration',
      contributions: { integration: 'service.name', account: 'svc' },
    },
    { canonical: 'duration_ns', effective: '', winning_tier: '', contributions: {} },
  ],
  effective: { service_name: 'service.name' },
  ...overrides,
});

beforeEach(() => {
  jest.clearAllMocks();
  observability.getLabelMapping.mockResolvedValue(response());
});

const baseProps = { accountId: 'acc-1', provider: 'datadog', providerSource: 'user', draftMappings: {}, cardIdx: 0 };

describe('EffectiveLabelMappingPanel', () => {
  it('defaults to the log resolver, so the existing log card is unchanged', async () => {
    render(<EffectiveLabelMappingPanel {...baseProps} />);
    await waitFor(() => expect(observability.getLabelMapping).toHaveBeenCalled());
    expect(observability.getLabelMapping.mock.calls[0][0]).toMatchObject({ provider_type: 'logs' });
  });

  it('asks the trace resolver when providerType is traces', async () => {
    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' />);
    await waitFor(() => expect(observability.getLabelMapping).toHaveBeenCalled());
    expect(observability.getLabelMapping.mock.calls[0][0]).toMatchObject({
      provider_type: 'traces',
      account_id: 'acc-1',
      provider: 'datadog',
      provider_source: 'user',
      // Always sent: the form is the authority on the integration tier while it is open,
      // and "the operator deleted every row" has to be expressible as an empty map.
      draft_set: true,
    });
  });

  // Renaming or deleting a testid is a breaking change to app-e2e-tests, so the log
  // instance must keep the exact ids it shipped with while the trace instance gets
  // its own.
  it('keeps the log testids and namespaces the trace ones', async () => {
    const { unmount } = render(<EffectiveLabelMappingPanel {...baseProps} />);
    // The rows arrive with the response, so wait on a row rather than the container.
    await screen.findByTestId('effective-label-row-service_name');
    expect(screen.getByTestId('effective-label-mapping-0')).toBeInTheDocument();
    unmount();

    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' testIdPrefix='trace-label-mapping-effective' />);
    await screen.findByTestId('trace-label-mapping-effective-row-service_name');
    expect(screen.getByTestId('trace-label-mapping-effective-mapping-0')).toBeInTheDocument();
    expect(screen.queryByTestId('effective-label-mapping-0')).not.toBeInTheDocument();
  });

  it('names the winning tier, and shows the value it beat', async () => {
    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' signalNoun='trace' />);
    await screen.findByText('This integration');
    // The losing account value stays visible (struck through) — the panel exists to
    // explain the outcome, not only to state it.
    expect(screen.getByText('svc')).toBeInTheDocument();
    expect(screen.getByText(/the same mapping trace queries use/)).toBeInTheDocument();
  });

  // An unmapped canonical field is the interesting case, not an omission: the canonical
  // name reaches the backend verbatim and usually matches nothing, with no error.
  it('spells out that an unmapped field is sent verbatim', async () => {
    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' />);
    await screen.findByText(/not mapped — sent as/);
  });
});

// ---------------------------------------------------------------------------
// Human-readable concepts
//
// The trace panel used to open on rows like `@k8s.pod.name` — Datadog's internal
// OTel-semconv aliases — sorted above every canonical field because '@' sorts below
// letters. The server now advertises only the canonical vocabulary; these pin the
// display half: friendly names, in the order the Settings mapper uses, and the log
// panel untouched.
// ---------------------------------------------------------------------------

describe('EffectiveLabelMappingPanel concepts', () => {
  const conceptLabels = { service_name: 'Service name', duration_ns: 'Duration (ns)' };
  const conceptOrder = ['service_name', 'duration_ns'];

  it('shows the human name with the canonical name beneath it', async () => {
    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' conceptLabels={conceptLabels} conceptOrder={conceptOrder} />);

    expect(await screen.findByText('Service name')).toBeInTheDocument();
    // The canonical name must survive: it is what you type into the mapping box and
    // what the query carries, so the friendly label leads it rather than replacing it.
    expect(screen.getByText('service_name')).toBeInTheDocument();
    expect(screen.getByText('Duration (ns)')).toBeInTheDocument();
  });

  it('orders rows by conceptOrder, not the server alphabetical order', async () => {
    observability.getLabelMapping.mockResolvedValue(
      response({
        fields: [
          { canonical: 'duration_ns', effective: '@duration', winning_tier: 'provider_default', contributions: {} },
          { canonical: 'service_name', effective: 'service.name', winning_tier: 'integration', contributions: {} },
        ],
      })
    );
    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' conceptLabels={conceptLabels} conceptOrder={conceptOrder} />);

    await screen.findByText('Service name');
    const rendered = screen.getAllByText(/^(Service name|Duration \(ns\))$/).map((el) => el.textContent);
    expect(rendered).toEqual(['Service name', 'Duration (ns)']);
  });

  it('keeps a field the order does not name, rather than dropping it', async () => {
    observability.getLabelMapping.mockResolvedValue(
      response({
        fields: [{ canonical: 'span_name', effective: 'operation_name', winning_tier: 'provider_default', contributions: {} }],
      })
    );
    render(<EffectiveLabelMappingPanel {...baseProps} providerType='traces' conceptLabels={conceptLabels} conceptOrder={conceptOrder} />);

    expect(await screen.findByTestId('effective-label-row-span_name')).toBeInTheDocument();
    expect(screen.getByText('span_name')).toBeInTheDocument();
  });

  it('renders bare canonical names when no concept props are given — the log path', async () => {
    render(<EffectiveLabelMappingPanel {...baseProps} />);

    expect(await screen.findByText('service_name')).toBeInTheDocument();
    expect(screen.queryByText('Service name')).not.toBeInTheDocument();
  });
});
