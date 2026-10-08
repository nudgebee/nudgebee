import React from 'react';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import '@testing-library/jest-dom';
import SetupCenter from '@components/onboarding/SetupCenter';

jest.mock('src/utils/colors');

const mockPush = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ push: mockPush }) }));

// The card reads who may act from the gate, which reads it from the session —
// not from `hasWriteAccess()`, whose backing global only `withAuth` populates.
const mockCanManage = jest.fn(() => true);
// Defaults true: most tests below exercise the admin/has-an-account paths and
// don't care about this signal. The three read-only tests that do set it
// explicitly, matching whether their own `mockGetAllAccount` payload is empty.
const mockHasConnectedAccount = jest.fn(() => true);
const mockGateRefresh = jest.fn();
jest.mock('@context/SetupGateContext', () => ({
  useSetupGate: () => ({
    loading: false,
    locked: false,
    canManageSetup: mockCanManage(),
    hasConnectedAccount: mockHasConnectedAccount(),
    refresh: mockGateRefresh,
  }),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({ title: 'Nudgebee' }),
  fillBrandTokens: (t: string) => (typeof t === 'string' ? t.replace(/\{brand\}/g, 'Nudgebee').replace(/\{Assistant\}/g, 'Nubi') : t),
  getBrandingAsset: () => '',
  getIsWhiteLabel: () => false,
}));

// Carries `cloud_provider` through as a data attribute (rather than
// discarding it) so a test can tell *which* provider's icon a given chip
// rendered, not just that some icon did.
jest.mock('@shared/icons/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }: { cloud_provider?: string }) => <span data-testid='cloud-icon' data-provider={cloud_provider} />,
}));

jest.mock('@components/integrations/modal/IntegrationDynamicFormModal', () => ({
  __esModule: true,
  default: ({
    integrationName,
    editData,
    title,
  }: {
    integrationName: string;
    editData?: { name?: string; integration_config_values?: unknown } | null;
    title?: string;
  }) => (
    <div
      data-testid='integration-modal'
      data-edit-name={editData?.name || ''}
      data-edit-config={editData ? JSON.stringify(editData.integration_config_values) : ''}
    >
      {integrationName}:{title}
    </div>
  ),
}));

jest.mock('@components/llm/AddLLMConfigModal', () => ({
  __esModule: true,
  default: ({ editData }: { editData?: { name?: string } | null }) => <div data-testid='llm-modal' data-edit-name={editData?.name || ''} />,
}));

jest.mock('@components/llm/KnowledgeBaseFormModal', () => ({
  __esModule: true,
  default: ({ editKnowledgeBase }: { editKnowledgeBase?: { name?: string } | null }) => (
    <div data-testid='kb-form-modal' data-edit-name={editKnowledgeBase?.name || ''} />
  ),
}));

// The real `@ui/Modal` wraps MUI's Dialog — portal, backdrop, focus trap and a
// real timed Fade transition. Every other modal in this suite avoids that
// weight with a plain stub (see IntegrationDynamicFormModal etc. above); this
// is the first place in this file to reach for the raw primitive directly, so
// it gets the same treatment rather than being the one exception.
jest.mock('@ui/Modal', () => ({
  __esModule: true,
  Modal: ({ open, title, children }: { open: boolean; title?: React.ReactNode; children?: React.ReactNode }) =>
    open ? (
      <div data-testid='ds-modal-stub'>
        <div data-testid='ds-modal-stub-title'>{title}</div>
        {children}
      </div>
    ) : null,
}));

jest.mock('@components/integrations/modal/K8sAccountModal', () => ({
  __esModule: true,
  default: ({ handleClose, handleOnAccountCreate }: { handleClose: (saved?: boolean) => void; handleOnAccountCreate?: () => void }) => (
    <div data-testid='account-modal-k8s'>
      <button data-testid='k8s-modal-account-created' onClick={handleOnAccountCreate}>
        simulate step 1 success
      </button>
      <button data-testid='k8s-modal-close' onClick={() => handleClose(true)}>
        close (account created)
      </button>
      <button data-testid='k8s-modal-cancel' onClick={() => handleClose(false)}>
        cancel (nothing created)
      </button>
    </div>
  ),
}));
jest.mock('@pages/accounts/AddAwsAccountModal', () => ({ __esModule: true, default: () => <div data-testid='account-modal-aws' /> }));
jest.mock('@pages/accounts/AddGcpAccountModal', () => ({ __esModule: true, default: () => <div data-testid='account-modal-gcp' /> }));
jest.mock('@pages/accounts/AddAzureAccountModal', () => ({ __esModule: true, default: () => <div data-testid='account-modal-azure' /> }));
jest.mock('@pages/accounts/AddSelfHostedAccountModal', () => ({
  __esModule: true,
  default: () => <div data-testid='account-modal-selfhosted' />,
}));

const mockGetAllAccount = jest.fn();
const mockListProviderCapabilities = jest.fn();
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getAllAccount: () => mockGetAllAccount(),
    listProviderCapabilities: (a: { account_id: string }) => mockListProviderCapabilities(a),
  },
}));

const mockListK8sAccAgentHealth = jest.fn();
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    listK8sAccAgentHealth: (ids: string[]) => mockListK8sAccAgentHealth(ids),
  },
}));

const mockGetKnowledgeBases = jest.fn();
const mockGetKnowledgeBase = jest.fn();
const mockGetKBAgents = jest.fn();
const mockCreateKnowledgeBase = jest.fn();
jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: {
    getKnowledgeBases: (accountId: string) => mockGetKnowledgeBases(accountId),
    getKnowledgeBase: (accountId: string, id: string) => mockGetKnowledgeBase(accountId, id),
    getKBAgents: (accountId: string, id: string) => mockGetKBAgents(accountId, id),
    createKnowledgeBase: (accountId: string, data: unknown) => mockCreateKnowledgeBase(accountId, data),
    updateKnowledgeBase: jest.fn().mockResolvedValue({ data: {} }),
    mapKnowledgeBaseToAgent: jest.fn().mockResolvedValue({}),
    unmapKnowledgeBaseFromAgent: jest.fn().mockResolvedValue({}),
  },
}));

// Only reached once a knowledge base create/edit form actually opens (the
// hook's own agents/tools/context-tag fetches) — defaulted to empty lists so
// tests that never open the form don't need to know these exist.
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listAgents: () => Promise.resolve({ data: { data: { ai_list_agents: { data: [] } } } }),
    listTools: () => Promise.resolve({ data: { data: { ai_list_tools: { data: [] } } } }),
  },
}));
jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sWorkloadNames: () => Promise.resolve({ data: { workloadNames: [] } }),
    getK8sNamespaceNames: () => Promise.resolve({ data: { namespaces: [] } }),
  },
}));

const mockListIntegrations = jest.fn();
jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listIntegrations: (a: unknown) => mockListIntegrations(a),
  },
}));

const payload = (accounts: unknown[] = [], integrations: unknown[] = [], messaging: unknown[] = []) => ({
  data: { all_accounts: accounts, integrations, messaging_platforms: messaging },
});
const capabilities = (entries: Array<{ provider: string; provider_type: string }>) => ({
  data: { data: { observability_list_provider_capabilities: entries } },
});
// Defaults every requested id to CONNECTED, so existing tests that never asked
// about agent health keep seeing the account item resolve to `done`.
const agentHealth = (ids: string[], status = 'CONNECTED') => ({
  data: { data: { agent: ids.map((id) => ({ cloud_account_id: id, status })) } },
});
const k8s = { id: 'acc-1', cloud_provider: 'K8s', account_name: 'prod', status: 'active' };

// `integration_config_values`/`integrations_cloud_accounts` arrive from the
// real backend as JSON strings — `parseIntegrationItem` (the real, unmocked
// helper under test here too) is what turns them into arrays.
const aiIntegrationRow = (type: string, name: string, configValues: Array<{ name: string; value: unknown }> = []) => ({
  id: `${type}-1`,
  type,
  name,
  integration_config_values: JSON.stringify(configValues),
  integrations_cloud_accounts: '[]',
});
const aiIntegrationsResponse = (rows: unknown[]) => ({ data: { data: { integrations_list: { rows } } } });

beforeEach(() => {
  jest.clearAllMocks();
  mockCanManage.mockReturnValue(true);
  mockHasConnectedAccount.mockReturnValue(true);
  mockListProviderCapabilities.mockResolvedValue(capabilities([]));
  mockListK8sAccAgentHealth.mockImplementation((ids: string[]) => Promise.resolve(agentHealth(ids)));
  mockGetKnowledgeBases.mockResolvedValue({ data: [] });
  mockGetKnowledgeBase.mockResolvedValue({ data: { id: 'kb-1', name: 'Runbooks' } });
  mockGetKBAgents.mockResolvedValue({ data: [] });
  mockCreateKnowledgeBase.mockResolvedValue({ data: { id: 'kb-1' } });
  mockListIntegrations.mockResolvedValue(aiIntegrationsResponse([]));
});

describe('SetupCenter', () => {
  it('renders all six panels with their outcome names once an account exists', async () => {
    // Pending, not active: the account row exists (not fresh, unlocked) but the panel isn't 'done' yet.
    mockGetAllAccount.mockResolvedValue(payload([{ ...k8s, status: 'pending' }]));
    render(<SetupCenter />);

    expect(await screen.findByText('Finish setting up Nudgebee')).toBeInTheDocument();
    ['Accounts and clusters', 'Telemetry', 'AI', 'Make your AI agents smarter', 'Notifications and ticketing', 'Alerts and deployments'].forEach(
      (title) => expect(screen.getByText(title)).toBeInTheDocument()
    );
  });

  it('locks every other panel until an account exists — no accordion, exit bar stays disabled', async () => {
    mockGetAllAccount.mockResolvedValue(payload());
    render(<SetupCenter />);

    expect(await screen.findByText('Set up Nudgebee')).toBeInTheDocument();
    expect(screen.getByText('Start here — pick a provider')).toBeInTheDocument();
    expect(screen.getByText('Unlocks once an account exists')).toBeInTheDocument();

    // The other five render as flat, inert, locked rows — not the normal accordion.
    ['telemetry', 'ai', 'smarter', 'delivery', 'triggers'].forEach((id) => expect(screen.getByTestId(`setup-locked-${id}`)).toBeInTheDocument());
    // Nothing to click into: no accordion card, no item content.
    expect(screen.queryByTestId('setup-panel-telemetry')).not.toBeInTheDocument();
    // The exit bar itself still shows on a fresh tenant — it says an account
    // is needed rather than vanishing — but the button stays disabled since
    // there's nowhere real to send anyone yet.
    expect(screen.getByTestId('setup-go-to-dashboard')).toBeDisabled();

    // Clicking a locked row's title does nothing — it isn't a button.
    fireEvent.click(screen.getByText('Telemetry'));
    expect(screen.queryByTestId('setup-item-metrics')).not.toBeInTheDocument();
  });

  it('shows a non-admin only the ask-an-admin message on a tenant with no accounts — no checklist at all', async () => {
    mockCanManage.mockReturnValue(false);
    mockHasConnectedAccount.mockReturnValue(false);
    mockGetAllAccount.mockResolvedValue(payload());
    render(<SetupCenter />);

    expect(await screen.findByTestId('setup-ask-admin')).toBeInTheDocument();
    expect(screen.queryByText('Set up Nudgebee')).not.toBeInTheDocument();
    expect(screen.queryByText('Start here — pick a provider')).not.toBeInTheDocument();
    expect(screen.queryByText(/You need write access to connect accounts/)).not.toBeInTheDocument();
  });

  it('offers the way out as soon as one account exists, with everything else empty', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    const exit = await screen.findByTestId('setup-go-to-dashboard');
    expect(exit).not.toBeDisabled();
    expect(screen.getByText(/please add more integrations/)).toBeInTheDocument();
  });

  // The row exists (unlocked, no longer fresh) but the agent hasn't checked in
  // yet — the exit button and the "connected" badge must not get ahead of that.
  it('keeps the exit disabled and the badge silent while the only account is still partial', async () => {
    mockGetAllAccount.mockResolvedValue(payload([{ ...k8s, status: 'pending' }]));
    render(<SetupCenter />);

    const exit = await screen.findByTestId('setup-go-to-dashboard');
    expect(exit).toBeDisabled();
    expect(screen.getByText(/You need at least one connected account/)).toBeInTheDocument();
    expect(screen.queryByText(/\d+ connected/)).not.toBeInTheDocument();
  });

  it('enables the exit and shows the connected badge once the account is actually done', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    const exit = await screen.findByTestId('setup-go-to-dashboard');
    expect(exit).not.toBeDisabled();
    expect(screen.getByText('1 connected')).toBeInTheDocument();
  });

  // The gate used to read the accounts panel's own worst-wins state, so a
  // second account still installing held the exit disabled even though the
  // tenant already had a fully working first one — a real dashboard to send
  // them to. It now reads the per-account rows directly: any one done is
  // enough for the exit, independent of how many others are still pending.
  it('enables the exit once any account is done, even while a second is still installing', async () => {
    const aws = { id: 'aws-1', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' };
    mockGetAllAccount.mockResolvedValue(payload([k8s, aws]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([k8s.id], 'PENDING'));
    render(<SetupCenter />);

    const exit = await screen.findByTestId('setup-go-to-dashboard');
    expect(exit).not.toBeDisabled();
    expect(screen.getByText(/please add more integrations/)).toBeInTheDocument();
  });

  it('keeps the exit disabled while every account is still installing', async () => {
    mockGetAllAccount.mockResolvedValue(
      payload([
        { ...k8s, id: 'k8s-1' },
        { ...k8s, id: 'k8s-2', account_name: 'staging' },
      ])
    );
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth(['k8s-1', 'k8s-2'], 'PENDING'));
    render(<SetupCenter />);

    const exit = await screen.findByTestId('setup-go-to-dashboard');
    expect(exit).toBeDisabled();
  });

  // Known issues #16: two adjacent numbers in two units ("N essential panels
  // left" above, "X of Y" on the bar) invite a reader to assume they are the
  // same axis. One clarifying line breaks that assumption without touching
  // the bar's own width-constrained label.
  it('explains that the progress bar counts items, not the essential-panel count above it', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    await screen.findByTestId('setup-go-to-dashboard');
    expect(screen.getByText(/essential panel.* left/)).toBeInTheDocument();
    expect(screen.getByText(/Counts required items across every panel/)).toBeInTheDocument();
  });

  it('does not show that line once every essential panel is done — there is nothing left to disambiguate', async () => {
    // The four essential panels a live tenant can actually finish: accounts
    // (k8s), telemetry (all three signals — anyOf, but give it all three so
    // no single-provider mapping assumption is baked into the fixture), ai
    // (llm) and smarter (skills, via a knowledge base on the one account).
    // Preferences and Rules are `hidden` (and still `planned` underneath
    // that), so they are not a further thing this fixture needs to satisfy.
    mockGetAllAccount.mockResolvedValue(
      payload(
        [k8s],
        [
          { type: 'prometheus', status: 'enabled', name: 'prometheus' },
          { type: 'loki', status: 'enabled', name: 'loki' },
          { type: 'jaeger', status: 'enabled', name: 'jaeger' },
          { type: 'llm', status: 'enabled', name: 'llm' },
        ]
      )
    );
    mockGetKnowledgeBases.mockResolvedValue({ data: [{ id: 'kb-1', name: 'Runbooks' }] });
    render(<SetupCenter />);

    await screen.findByTestId('setup-go-to-dashboard');
    expect(screen.getByText(/Essentials are done/)).toBeInTheDocument();
    expect(screen.queryByText(/essential panel.* left/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Counts required items across every panel/)).not.toBeInTheDocument();
  });

  it('shows an agent-detected signal as connected, with the auto-detected chip', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockListProviderCapabilities.mockResolvedValue(capabilities([{ provider: 'prometheus', provider_type: 'metrics' }]));
    render(<SetupCenter />);

    // Panels collapse to an accordion; only Accounts starts open.
    fireEvent.click(await screen.findByText('Telemetry'));
    expect(await screen.findByText('Auto-detected · nothing to configure')).toBeInTheDocument();
    expect(mockListProviderCapabilities).toHaveBeenCalledWith({ account_id: 'acc-1' });
  });

  it('still renders the panels when telemetry detection fails', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockListProviderCapabilities.mockRejectedValue(new Error('capabilities down'));
    render(<SetupCenter />);

    expect(await screen.findByText('Telemetry')).toBeInTheDocument();
    expect(screen.queryByText('Auto-detected · nothing to configure')).not.toBeInTheDocument();
  });

  it('shows Skills connected once the account has a knowledge base', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockGetKnowledgeBases.mockResolvedValue({ data: [{ id: 'kb-1', name: 'Runbooks' }] });
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    expect(await screen.findByTestId('setup-item-skills')).toBeInTheDocument();
    // Tenant-wide (''), not per-account — the same call readiness's own
    // hasKnowledgeBase signal now uses, so the two can no longer disagree
    // about whether this tenant has a knowledge base at all.
    expect(mockGetKnowledgeBases).toHaveBeenCalledWith('');
    // The real knowledge base's own name — not the generic "Knowledge base"
    // category label every connected Skills chip used to repeat back.
    expect(within(screen.getByTestId('setup-item-skills')).getByText('Runbooks')).toBeInTheDocument();
  });

  it('hides Preferences and Rules from the smarter panel, without swallowing Skills’ own name row', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    const skillsItem = await screen.findByTestId('setup-item-skills');
    expect(screen.queryByTestId('setup-item-prefs')).not.toBeInTheDocument();
    expect(screen.queryByTestId('setup-item-rules')).not.toBeInTheDocument();
    expect(screen.queryByText('Preferences')).not.toBeInTheDocument();
    expect(screen.queryByText('Rules')).not.toBeInTheDocument();
    // Skills becoming the panel's only visible item must not put it into
    // `compact` mode the way the sole Accounts item is — this panel's own
    // title ("Make your AI agents smarter") never repeats "Skills" the way
    // "Accounts and clusters" repeats "Cluster or cloud account", so the
    // item's own name line has to keep rendering.
    expect(within(skillsItem).getByText('Skills')).toBeInTheDocument();
  });

  it('opens the Knowledge Base edit form, scoped to the entry’s own account, when its chip is clicked', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockGetKnowledgeBases.mockResolvedValue({ data: [{ id: 'kb-1', name: 'Runbooks', account_id: 'acc-1', account_name: 'prod' }] });
    mockGetKnowledgeBase.mockResolvedValue({ data: { id: 'kb-1', name: 'Runbooks' } });
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    fireEvent.click(await within(screen.getByTestId('setup-item-skills')).findByText('Runbooks — prod'));

    // The chip already knows which account its own row belongs to, so the
    // edit form opens directly, scoped to it — no route, no account picker.
    expect(mockPush).not.toHaveBeenCalled();
    expect(await screen.findByTestId('kb-form-modal')).toHaveAttribute('data-edit-name', 'Runbooks');
    expect(mockGetKnowledgeBase).toHaveBeenCalledWith('acc-1', 'kb-1');
    expect(mockGetKBAgents).toHaveBeenCalledWith('acc-1', 'kb-1');
    expect(screen.queryByTestId('setup-kb-account-picker')).not.toBeInTheDocument();
  });

  // Known issue #22: unlike every other item, opening this one is not
  // instant — the edit fetch (name/content/agent mappings) takes a beat.
  // Before the fix, nothing on screen said the click did anything.
  it('shows the clicked chip busy while its edit fetch is in flight, then opens the form', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockGetKnowledgeBases.mockResolvedValue({ data: [{ id: 'kb-1', name: 'Runbooks', account_id: 'acc-1', account_name: 'prod' }] });
    let resolveFetch: (value: unknown) => void = () => {};
    mockGetKnowledgeBase.mockReturnValue(
      new Promise((resolve) => {
        resolveFetch = resolve;
      })
    );
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    const skillsItem = screen.getByTestId('setup-item-skills');
    fireEvent.click(await within(skillsItem).findByText('Runbooks — prod'));

    // Busy: a spinner where the chip's own icon would be, and — per Chip's
    // own `loading` behavior — no longer a clickable button while it stays
    // true, so a second click can't fire a second fetch.
    await waitFor(() => expect(within(skillsItem).getByRole('progressbar')).toBeInTheDocument());
    expect(within(skillsItem).queryByRole('button', { name: /Runbooks/ })).not.toBeInTheDocument();

    resolveFetch({ data: { id: 'kb-1', name: 'Runbooks' } });

    expect(await screen.findByTestId('kb-form-modal')).toHaveAttribute('data-edit-name', 'Runbooks');
    expect(within(skillsItem).queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('names each account when a tenant has knowledge bases on more than one', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s, { id: 'acc-2', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' }]));
    mockGetKnowledgeBases.mockResolvedValue({
      data: [
        { id: 'kb-1', name: 'Runbooks', account_name: 'prod' },
        { id: 'kb-2', name: 'Ownership map', account_name: 'prod-aws' },
      ],
    });
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    const skillsItem = within(await screen.findByTestId('setup-item-skills'));
    expect(await skillsItem.findByText('Runbooks — prod')).toBeInTheDocument();
    expect(skillsItem.getByText('Ownership map — prod-aws')).toBeInTheDocument();
  });

  it('leaves Skills not connected while no account has a knowledge base', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockGetKnowledgeBases.mockResolvedValue({ data: [] });
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    expect(await screen.findByTestId('setup-item-skills')).toBeInTheDocument();
    expect(within(screen.getByTestId('setup-item-skills')).queryByText('Knowledge base')).not.toBeInTheDocument();
  });

  it('still renders the panels when the knowledge-base lookup fails', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockGetKnowledgeBases.mockRejectedValue(new Error('kb lookup down'));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    expect(await screen.findByTestId('setup-item-skills')).toBeInTheDocument();
    expect(within(screen.getByTestId('setup-item-skills')).queryByText('Knowledge base')).not.toBeInTheDocument();
  });

  // Skills has no integration form of its own: its Add button opens the
  // Knowledge Base create form directly, rather than opening
  // IntegrationDynamicFormModal like every other item's Add button does.
  it("opens the Knowledge Base create form directly when Skills' Add is clicked with one account", async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    fireEvent.click(await screen.findByTestId('setup-add-skills'));

    // One eligible account — skips the picker and opens create mode directly.
    expect(mockPush).not.toHaveBeenCalled();
    expect(screen.queryByTestId('integration-modal')).not.toBeInTheDocument();
    expect(screen.queryByTestId('setup-kb-account-picker')).not.toBeInTheDocument();
    expect(await screen.findByTestId('kb-form-modal')).toHaveAttribute('data-edit-name', '');
  });

  it("asks which account when Skills' Add is clicked with more than one, then opens create mode for the one picked", async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s, { id: 'acc-2', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Make your AI agents smarter'));
    fireEvent.click(await screen.findByTestId('setup-add-skills'));

    expect(await screen.findByTestId('ds-modal-stub')).toBeInTheDocument();
    expect(screen.queryByTestId('kb-form-modal')).not.toBeInTheDocument();

    fireEvent.click(within(screen.getByTestId('setup-kb-account-picker')).getByRole('button'));
    fireEvent.click(screen.getByRole('option', { name: 'prod-aws', hidden: true }));

    expect(screen.queryByTestId('ds-modal-stub')).not.toBeInTheDocument();
    expect(await screen.findByTestId('kb-form-modal')).toBeInTheDocument();
  });

  it('surfaces a failing integration as Needs attention', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'jira', status: 'error', name: 'jira' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Notifications and ticketing'));
    expect(await screen.findAllByText('Needs attention')).not.toHaveLength(0);
  });

  it('keeps every panel collapsed but Accounts by default, an accordion not a wall', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    await screen.findByTestId('setup-item-account');
    expect(screen.queryByTestId('setup-item-metrics')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('Telemetry'));
    expect(await screen.findByTestId('setup-item-metrics')).toBeInTheDocument();
    // Single-open accordion: expanding Telemetry closes Accounts.
    expect(screen.queryByTestId('setup-item-account')).not.toBeInTheDocument();
  });

  it('folds the single Accounts item into its own panel header, no duplicate title', async () => {
    // Not a fresh tenant — the account panel only renders its normal PanelBody
    // (where the duplicate-title risk lives) once it's out of the locked view.
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    await screen.findByText('Accounts and clusters');
    expect(screen.queryByText('Cluster or cloud account')).not.toBeInTheDocument();
  });

  it('hides every action from a read-only user and says why', async () => {
    // Has an account already — the ask-admin-only branch is for the *other*
    // read-only case (no account at all yet), not this one.
    mockCanManage.mockReturnValue(false);
    mockHasConnectedAccount.mockReturnValue(true);
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    expect(await screen.findByText(/You need write access to connect accounts/)).toBeInTheDocument();
    expect(screen.queryByTestId('setup-go-to-dashboard')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('Telemetry'));
    expect(await screen.findByTestId('setup-item-metrics')).toBeInTheDocument();
    expect(screen.queryByTestId('setup-add-metrics')).not.toBeInTheDocument();
  });

  // Was: "retires itself once every scorable panel is done". That test passed for
  // the wrong reason -- waitFor(...toBeNull()) resolves on the very first frame,
  // while the card is still its loading skeleton, so it never observed the state
  // it claimed to. The behaviour it described is also gone: the card lives on its
  // own route now and always renders, which is what stops a gated user reaching a
  // screen with no Finish button on it.
  it('still renders, with Finish enabled, when every panel is already done', async () => {
    mockGetAllAccount.mockResolvedValue(
      payload(
        [k8s],
        ['prometheus', 'loki', 'llm', 'slack', 'jira', 'prometheus_alertmanager_webhook', 'github'].map((type) => ({
          type,
          status: 'enabled',
          name: type,
        }))
      )
    );
    render(<SetupCenter />);

    // Wait for the real card, not the skeleton, before asserting anything.
    const card = await screen.findByTestId('home-setup-center');
    expect(card).toBeInTheDocument();
    expect(await screen.findByTestId('setup-go-to-dashboard')).not.toBeDisabled();
  });

  it('says so, rather than rendering nothing, when the lookup fails', async () => {
    mockGetAllAccount.mockResolvedValue(undefined);
    render(<SetupCenter />);

    expect(await screen.findByTestId('home-setup-center-unavailable')).toBeInTheDocument();
    expect(screen.getByText(/could not load your setup state/i)).toBeInTheDocument();
  });

  it('opens a cloud/K8s account tile as its own modal in place, never a route', async () => {
    mockGetAllAccount.mockResolvedValue(payload());
    render(<SetupCenter />);

    fireEvent.click(await screen.findByTestId('setup-provider-aws'));
    expect(await screen.findByTestId('account-modal-aws')).toBeInTheDocument();
    // Nothing in the flow may route away — not to account-form, not to the catalog.
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('opens the K8s account tile as the K8s account modal', async () => {
    mockGetAllAccount.mockResolvedValue(payload());
    render(<SetupCenter />);

    fireEvent.click(await screen.findByTestId('setup-provider-k8s'));
    expect(await screen.findByTestId('account-modal-k8s')).toBeInTheDocument();
    expect(mockPush).not.toHaveBeenCalled();
  });

  // The tile's own "Connected" state used to mean "a row for this provider
  // exists", not "this provider is actually done" — `connectedProviders` is
  // an existence check with no state in it at all. A K8s account still
  // waiting on its agent made the tile claim the provider was finished
  // while the row right above it correctly showed the account as `partial`.
  //
  // Asserted via the tile's own `aria-label`, not its visual badge: the
  // badge is presentation (a text pill, then a status glyph — visual choices
  // that can change again), the accessible name is the actual contract this
  // behaviour has to keep, in either form.
  it('keeps the K8s tile off “connected” while the only account is still waiting on its agent', async () => {
    mockGetAllAccount.mockResolvedValue(payload([{ ...k8s, status: 'active' }]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([k8s.id], 'PENDING'));
    render(<SetupCenter />);

    const tile = await screen.findByTestId('setup-provider-k8s');
    expect(tile).toHaveAttribute('aria-label', 'Connect Kubernetes');
  });

  it('reads the K8s tile as connected once its account is actually done', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    const tile = await screen.findByTestId('setup-provider-k8s');
    expect(tile).toHaveAttribute('aria-label', 'Kubernetes — connected. Add another');
  });

  // The full "Connected" text pill used to crowd the provider's own name
  // down to a one-letter ellipsis at this tile's width (e.g. "Azure App
  // Insights" → "A…"). A compact status glyph replaces it — same "done"
  // signal, a fraction of the space.
  it('shows a compact status glyph on a connected tile, not a text badge that would crowd out its name', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    const tile = await screen.findByTestId('setup-provider-k8s');
    expect(within(tile).queryByText('Connected')).not.toBeInTheDocument();
    expect(within(tile).getByText('Kubernetes')).toBeInTheDocument();
    expect(tile.querySelector('svg path')).not.toBeNull();
  });

  it('keeps the AWS tile off “connected” until an AWS account exists, even with K8s already done', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    const tile = await screen.findByTestId('setup-provider-aws');
    expect(tile).toHaveAttribute('aria-label', 'Connect AWS');
  });

  it('keeps the K8s wizard open when step 1 creates the account — only its own close may dismiss it', async () => {
    mockGetAllAccount.mockResolvedValue(payload());
    render(<SetupCenter />);

    fireEvent.click(await screen.findByTestId('setup-provider-k8s'));
    await screen.findByTestId('account-modal-k8s');
    expect(mockGetAllAccount).toHaveBeenCalledTimes(1);

    // Step 1 succeeding must refresh readiness in the background, not close
    // the wizard — step 2 (install command, wait-for-agent) still has to show.
    fireEvent.click(screen.getByTestId('k8s-modal-account-created'));
    await waitFor(() => expect(mockGetAllAccount).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('account-modal-k8s')).toBeInTheDocument();

    // The wizard's own close is what actually dismisses it.
    fireEvent.click(screen.getByTestId('k8s-modal-close'));
    await waitFor(() => expect(screen.queryByTestId('account-modal-k8s')).not.toBeInTheDocument());
  });

  it('does not refresh when the K8s wizard is cancelled with nothing created', async () => {
    mockGetAllAccount.mockResolvedValue(payload());
    render(<SetupCenter />);

    fireEvent.click(await screen.findByTestId('setup-provider-k8s'));
    await screen.findByTestId('account-modal-k8s');
    expect(mockGetAllAccount).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('k8s-modal-cancel'));
    await waitFor(() => expect(screen.queryByTestId('account-modal-k8s')).not.toBeInTheDocument());
    expect(mockGetAllAccount).toHaveBeenCalledTimes(1);
  });

  it('looks up K8s agent health for an active account, keyed by account id', async () => {
    // The row is already `active` — only the agent-health lookup can still hold it back.
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([k8s.id], 'PENDING'));
    render(<SetupCenter />);

    await screen.findByTestId('setup-item-account');
    expect(mockListK8sAccAgentHealth).toHaveBeenCalledWith([k8s.id]);
  });

  it('shows an unconnected item’s picker open by default, with no Add click needed', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    expect(await screen.findByTestId('setup-provider-prometheus')).toBeInTheDocument();
    // Logs has never been the "first required item" special-case — every unconnected item opens by default.
    expect(screen.getByTestId('setup-picker-logs')).toBeInTheDocument();
  });

  it('keeps a picker open by default with exactly one thing connected — still deciding, not done browsing', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'prometheus', status: 'enabled', name: 'prometheus' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    expect(await screen.findByTestId('setup-picker-metrics')).toBeInTheDocument();
  });

  it('collapses the picker by default once more than one provider is already connected, reachable again via Add more', async () => {
    mockGetAllAccount.mockResolvedValue(
      payload(
        [k8s],
        [
          { type: 'prometheus', status: 'enabled', name: 'prometheus' },
          { type: 'datadog', status: 'enabled', name: 'datadog' },
        ]
      )
    );
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    const metricsItem = await screen.findByTestId('setup-item-metrics');
    // The connected-provider chips are the point of the collapse — they're
    // there instead of the picker, not missing.
    expect(within(metricsItem).getByText('Prometheus')).toBeInTheDocument();
    expect(screen.queryByTestId('setup-picker-metrics')).not.toBeInTheDocument();

    fireEvent.click(within(metricsItem).getByTestId('setup-add-metrics'));
    expect(await screen.findByTestId('setup-picker-metrics')).toBeInTheDocument();
  });

  it('caps tiles at 6 for a provider list of exactly 7, moving the rest to the dropdown', async () => {
    // Ticketing has exactly 7 providers today and none declare cloudCapable, so this
    // isolates the flat tile cap from the cloud-provider filtering below. Under the
    // old >8 threshold a list this size rendered all 7 tiles with no dropdown at all.
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Notifications and ticketing'));
    const ticketPicker = await screen.findByTestId('setup-picker-ticket');

    expect(within(ticketPicker).getAllByTestId(/^setup-provider-/)).toHaveLength(6);
    expect(within(ticketPicker).getByTestId('setup-provider-github')).toBeInTheDocument();
    expect(within(ticketPicker).queryByTestId('setup-provider-gitlab')).not.toBeInTheDocument();
    expect(document.getElementById('setup-more-ticket')).toBeInTheDocument();
  });

  it('deprioritizes K8s-only telemetry providers to the dropdown when the tenant has no K8s or self-hosted account', async () => {
    mockGetAllAccount.mockResolvedValue(payload([{ id: 'acc-aws', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    const metricsPicker = await screen.findByTestId('setup-picker-metrics');

    // Cloud-capable providers always make the front row.
    expect(within(metricsPicker).getByTestId('setup-provider-datadog')).toBeInTheDocument();
    // The one K8s-only provider that fits the remaining tile slot still gets shown —
    // deprioritizing never means leaving tiles empty when there's something to fill them.
    expect(within(metricsPicker).getByTestId('setup-provider-prometheus')).toBeInTheDocument();
    // The rest of the K8s-only providers lose the tile race — deprioritized, never
    // hidden: still reachable from the dropdown below, just not a front-row tile.
    expect(within(metricsPicker).queryByTestId('setup-provider-newrelic')).not.toBeInTheDocument();
    expect(within(metricsPicker).queryByTestId('setup-provider-cubeapm')).not.toBeInTheDocument();
    expect(within(metricsPicker).queryByTestId('setup-provider-splunk_enterprise')).not.toBeInTheDocument();
    expect(document.getElementById('setup-more-metrics')).toBeInTheDocument();
  });

  it('opens every other provider as an integration modal in place, never a route', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    // The picker is open by default — no Add click needed to see the tiles.
    fireEvent.click(await screen.findByText('Telemetry'));
    fireEvent.click(await screen.findByTestId('setup-provider-prometheus'));

    // The real backend type (lowercase), not the model's uppercase display route.
    expect(await screen.findByTestId('integration-modal')).toHaveTextContent('prometheus');
    // Nothing in the flow may route away — not to account-form, not to the catalog.
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('opens the LLM provider item as the dedicated LLM modal', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('AI'));
    fireEvent.click(await screen.findByTestId('setup-add-llm'));
    expect(await screen.findByTestId('llm-modal')).toBeInTheDocument();
    expect(mockPush).not.toHaveBeenCalled();
  });

  // A connected LLM/gateway/MCP item used to show the same generic provider
  // label as its own chip that its own name already carries ("LLM provider"
  // under "LLM provider") — no way to tell what was actually configured.
  describe('AI panel connected-item detail', () => {
    it('names the configured LLM provider and model on its chip, not the generic label', async () => {
      mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'llm', status: 'enabled', name: 'llm' }]));
      mockListIntegrations.mockResolvedValue(
        aiIntegrationsResponse([aiIntegrationRow('llm', 'Prod OpenAI', [{ name: 'llm_model_name', value: 'gpt-5' }])])
      );
      render(<SetupCenter />);

      fireEvent.click(await screen.findByText('AI'));
      expect(await within(screen.getByTestId('setup-item-llm')).findByText('Prod OpenAI · gpt-5')).toBeInTheDocument();
    });

    it('reopens the LLM modal pre-filled when the connected chip is clicked', async () => {
      mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'llm', status: 'enabled', name: 'llm' }]));
      mockListIntegrations.mockResolvedValue(aiIntegrationsResponse([aiIntegrationRow('llm', 'Prod OpenAI')]));
      render(<SetupCenter />);

      fireEvent.click(await screen.findByText('AI'));
      fireEvent.click(await screen.findByText('Prod OpenAI'));
      expect(await screen.findByTestId('llm-modal')).toHaveAttribute('data-edit-name', 'Prod OpenAI');
    });

    it('names the connected MCP server on its chip, clickable to reopen it pre-filled', async () => {
      mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'mcp', status: 'enabled', name: 'mcp' }]));
      mockListIntegrations.mockResolvedValue(
        aiIntegrationsResponse([aiIntegrationRow('mcp', 'Internal tools server', [{ name: 'url', value: 'https://mcp.internal.example.com' }])])
      );
      render(<SetupCenter />);

      fireEvent.click(await screen.findByText('AI'));
      fireEvent.click(await within(screen.getByTestId('setup-item-mcp')).findByText('Internal tools server'));
      const modal = await screen.findByTestId('integration-modal');
      expect(modal).toHaveAttribute('data-edit-name', 'Internal tools server');
      // The provider's own label ("MCP server", singular) — not the item's
      // plural display name ("MCP servers") — same label `openIntegration`
      // (the "Add" path) already used for this same modal's title.
      expect(modal).toHaveTextContent('Edit MCP server');
      // IntegrationDynamicFormModal reads `integration_config_values` as an
      // object map (`editData?.integration_config_values?.[key]`), not the
      // array shape the backend actually returns it in. Passing the raw
      // array through leaves every field blank on open — this is the one
      // assertion that would have caught it.
      const config = JSON.parse(modal.getAttribute('data-edit-config') || '{}');
      expect(config).toMatchObject({ url: 'https://mcp.internal.example.com', integration_config_name: 'Internal tools server' });
      expect(Array.isArray(config)).toBe(false);
    });

    it('falls back to the generic provider label when the enrichment fetch has nothing for this item', async () => {
      mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'llm', status: 'enabled', name: 'llm' }]));
      // Default `beforeEach` mock already resolves to no rows — the point is
      // that this is a real, not-yet-caught-up case, not a fetch failure.
      render(<SetupCenter />);

      fireEvent.click(await screen.findByText('AI'));
      // Once for the item's own heading, once for the chip falling back to
      // the same plain provider label `connectedBy` always carried.
      expect(screen.getAllByText('LLM provider')).toHaveLength(2);
    });
  });

  it('opens the disclosure with the impact of skipping an item', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    fireEvent.click(await screen.findByTestId('setup-why-logs'));
    expect(screen.getByText('Skip this and these stay empty')).toBeInTheDocument();
    expect(screen.getByText(/No source to query/)).toBeInTheDocument();
  });

  it('shows the required-by module chips behind the disclosure', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    fireEvent.click(await screen.findByTestId('setup-why-logs'));
    expect(screen.getByText('Required by')).toBeInTheDocument();
    expect(screen.getByText('Troubleshooting')).toBeInTheDocument();
  });
});

// A label's text can appear twice in an item — once on its own connected
// chip, once on the (open-by-default) picker tile offering the same
// provider — so every lookup here filters down to the element that carries
// the chip's own `data-tone`, never a bare `getByText`.
const chipFor = (container: HTMLElement, text: string): HTMLElement => {
  const match = within(container)
    .getAllByText(text)
    .map((el) => el.closest('[data-tone]'))
    .find((el): el is HTMLElement => Boolean(el));
  if (!match) throw new Error(`no connected chip found for "${text}"`);
  return match;
};

describe('per-account chips on the Accounts item', () => {
  it('renders one chip per real account, each carrying its own provider icon and name', async () => {
    const aws = { id: 'aws-1', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' };
    mockGetAllAccount.mockResolvedValue(payload([k8s, aws]));
    render(<SetupCenter />);

    const accountItem = await screen.findByTestId('setup-item-account');
    const k8sChip = chipFor(accountItem, 'prod');
    const awsChip = chipFor(accountItem, 'prod-aws');

    expect(within(k8sChip).getByTestId('cloud-icon')).toHaveAttribute('data-provider', 'K8s');
    expect(within(awsChip).getByTestId('cloud-icon')).toHaveAttribute('data-provider', 'AWS');
  });

  it('renders account chips as static tags, never a click target', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    const accountItem = await screen.findByTestId('setup-item-account');
    const chip = chipFor(accountItem, 'prod');
    // A clickable chip (see the AI panel's connected-integration chips) is a
    // `<button>` (MUI ButtonBase); the static tag path renders a plain `<span>`.
    expect(chip.tagName.toLowerCase()).toBe('span');
    expect(within(accountItem).queryByRole('button', { name: 'prod' })).not.toBeInTheDocument();
  });

  it('gives a K8s account still waiting on its agent its own amber chip, independent of a healthy sibling', async () => {
    const aws = { id: 'aws-1', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' };
    mockGetAllAccount.mockResolvedValue(payload([k8s, aws]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([k8s.id], 'PENDING'));
    render(<SetupCenter />);

    const accountItem = await screen.findByTestId('setup-item-account');
    const pendingChip = chipFor(accountItem, 'prod');
    const doneChip = chipFor(accountItem, 'prod-aws');
    expect(pendingChip).toHaveAttribute('data-tone', 'warning');
    expect(doneChip).toHaveAttribute('data-tone', 'success');

    // The status glyph, not just the background tint, tells the two apart —
    // a checkmark path only on the done chip's own trailing icon.
    expect(doneChip.querySelector('svg path')).not.toBeNull();
    expect(pendingChip.querySelector('svg path')).toBeNull();
    expect(pendingChip.querySelector('svg')).not.toBeNull();
  });
});

describe('per-provider chips on Metrics/Logs/Traces', () => {
  it('gives each connected provider chip its own icon, keyed by that provider’s route', async () => {
    mockGetAllAccount.mockResolvedValue(
      payload(
        [k8s],
        [
          { type: 'prometheus', status: 'enabled', name: 'prometheus' },
          { type: 'loki', status: 'enabled', name: 'loki' },
          { type: 'jaeger', status: 'enabled', name: 'jaeger' },
        ]
      )
    );
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    const metricsItem = await screen.findByTestId('setup-item-metrics');
    const logsItem = await screen.findByTestId('setup-item-logs');
    const tracesItem = await screen.findByTestId('setup-item-traces');

    expect(within(chipFor(metricsItem, 'Prometheus')).getByTestId('cloud-icon')).toHaveAttribute('data-provider', 'PROMETHEUS');
    expect(within(chipFor(logsItem, 'Loki')).getByTestId('cloud-icon')).toHaveAttribute('data-provider', 'LOKI');
    expect(within(chipFor(tracesItem, 'Jaeger')).getByTestId('cloud-icon')).toHaveAttribute('data-provider', 'JAEGER');
  });

  it('still renders the provider chip, without an icon, for the one label a registry lookup can miss', async () => {
    // The auto-detected-but-something-else-needs-attention path in
    // `providerMatchEvaluator` appends the raw detected-signal name to
    // `connectedBy` alongside the matched labels — a string that was never a
    // registered provider `label`, so the icon lookup has nothing to key off.
    // The chip must still render (text-only), not crash or vanish.
    mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'newrelic', status: 'error', name: 'newrelic' }]));
    mockListProviderCapabilities.mockResolvedValue(capabilities([{ provider: 'newrelic-agent', provider_type: 'metrics' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    const metricsItem = await screen.findByTestId('setup-item-metrics');
    // The matched, registered label still gets its icon...
    expect(within(chipFor(metricsItem, 'New Relic')).getByTestId('cloud-icon')).toHaveAttribute('data-provider', 'NEWRELIC');
    // ...the raw detected-signal name renders too, but with no icon to show.
    expect(within(chipFor(metricsItem, 'newrelic-agent')).queryByTestId('cloud-icon')).not.toBeInTheDocument();
  });
});

describe('mixed-state note — known issue #34', () => {
  it('explains the accounts item once any account works while a sibling is still pending', async () => {
    const aws = { id: 'aws-1', cloud_provider: 'AWS', account_name: 'prod-aws', status: 'active' };
    mockGetAllAccount.mockResolvedValue(payload([k8s, aws]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([k8s.id], 'PENDING'));
    render(<SetupCenter />);

    const accountItem = await screen.findByTestId('setup-item-account');
    expect(within(accountItem).getByTestId('setup-item-account-mixed-state-note')).toHaveTextContent('Marked connected once any one account works');
  });

  it('does not show the accounts note once every account is actually connected', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([k8s.id], 'CONNECTED'));
    render(<SetupCenter />);

    const accountItem = await screen.findByTestId('setup-item-account');
    expect(within(accountItem).queryByTestId('setup-item-account-mixed-state-note')).not.toBeInTheDocument();
  });

  it('explains a provider-matched item flagged by one broken connection while a sibling is healthy', async () => {
    mockGetAllAccount.mockResolvedValue(
      payload(
        [k8s],
        [
          { type: 'loki', status: 'enabled', name: 'loki' },
          { type: 'es', status: 'error', name: 'es-1' },
        ]
      )
    );
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    const logsItem = await screen.findByTestId('setup-item-logs');
    expect(within(logsItem).getByTestId('setup-item-logs-mixed-state-note')).toHaveTextContent(
      'Flags if any connection needs attention, even while another works fine.'
    );
  });

  it('does not show the mixed note when every matched provider agrees', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'loki', status: 'enabled', name: 'loki' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    const logsItem = await screen.findByTestId('setup-item-logs');
    expect(within(logsItem).queryByTestId('setup-item-logs-mixed-state-note')).not.toBeInTheDocument();
  });
});

describe('module chips', () => {
  it('shows all four, all on by default', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    for (const id of ['ts', 'opt', 'k8s', 'auto']) {
      expect(await screen.findByTestId(`setup-module-${id}`)).toHaveAttribute('aria-pressed', 'true');
    }
    expect(screen.getByText('Changes what is required. Nothing is hidden.')).toBeInTheDocument();
  });

  it('changes what is required without hiding anything', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    // Everything selected: eight required items, so one account is 1 of 8.
    expect(await screen.findByText('1 of 8')).toBeInTheDocument();

    // Optimize alone needs only an account and metrics.
    ['ts', 'k8s', 'auto'].forEach((id) => fireEvent.click(screen.getByTestId(`setup-module-${id}`)));
    expect(await screen.findByText('1 of 2')).toBeInTheDocument();

    // Logs is `na` for Optimize. Open telemetry — panels below accounts render
    // collapsed — and it is still there, tagged rather than removed. Nothing is
    // hidden: a customer who picked Optimize can still connect it. The tag
    // names the module driving it, not a bare "Not needed" — Optimize is the
    // only chip left selected, so it's the only one the tag can mean.
    fireEvent.click(screen.getByTestId('setup-panel-toggle-telemetry'));
    const logsItem = await screen.findByTestId('setup-item-logs');
    expect(within(logsItem).getByText('Not needed for Optimize')).toBeInTheDocument();
  });

  // MCP is `na` for both Optimize and Kubernetes (and only those two) — safe
  // to name both by name, unlike the `req` caption elsewhere on this card,
  // because `need` is the *strongest* level across the selection: every
  // currently-selected module has to individually agree it's `na` for the
  // item to read `na` at all, so there's no risk of naming the wrong one.
  // The two are named in the filter row's own left-to-right order regardless
  // of which one was clicked off first.
  it('names every currently-selected module an item is not needed for, in a stable order', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);
    await screen.findByText('1 of 8');

    // Click Automation off before Troubleshoot, to prove the label doesn't
    // just echo click order — Optimize still has to come before Kubernetes.
    fireEvent.click(screen.getByTestId('setup-module-auto'));
    fireEvent.click(screen.getByTestId('setup-module-ts'));

    fireEvent.click(await screen.findByText('AI'));
    const mcpItem = await screen.findByTestId('setup-item-mcp');
    expect(within(mcpItem).getByText('Not needed for Optimize, Kubernetes')).toBeInTheDocument();
  });

  it('treats turning every chip off as turning them all on', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);
    expect(await screen.findByText('1 of 8')).toBeInTheDocument();

    ['ts', 'opt', 'k8s', 'auto'].forEach((id) => fireEvent.click(screen.getByTestId(`setup-module-${id}`)));

    // No dead end: the count is what it was with everything selected.
    expect(await screen.findByText('1 of 8')).toBeInTheDocument();
  });

  it('hides the chips from a read-only viewer, who cannot act on them', async () => {
    // Has an account already — see the same note on 'hides every action...' above.
    mockCanManage.mockReturnValue(false);
    mockHasConnectedAccount.mockReturnValue(true);
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    await screen.findByText(/You need write access/);
    expect(screen.queryByTestId('setup-module-ts')).not.toBeInTheDocument();
  });
});

describe('telemetry / traces gap copy (build spec §9)', () => {
  // Telemetry is anyOf — metrics alone is enough to read done — but traces is
  // `req` for Troubleshoot and Automation. Every module is selected by
  // default, so this reproduces without touching the chips.
  it('names the gap when metrics alone satisfies Telemetry but traces is still required', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s], [{ type: 'prometheus', status: 'enabled', name: 'prometheus' }]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    expect(await screen.findByTestId('setup-telemetry-traces-gap')).toBeInTheDocument();
  });

  it('says nothing once traces is connected too', async () => {
    mockGetAllAccount.mockResolvedValue(
      payload(
        [k8s],
        [
          { type: 'prometheus', status: 'enabled', name: 'prometheus' },
          { type: 'jaeger', status: 'enabled', name: 'jaeger' },
        ]
      )
    );
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    await screen.findByTestId('setup-item-traces');
    expect(screen.queryByTestId('setup-telemetry-traces-gap')).not.toBeInTheDocument();
  });

  it('says nothing while Telemetry itself is not done', async () => {
    mockGetAllAccount.mockResolvedValue(payload([k8s]));
    render(<SetupCenter />);

    fireEvent.click(await screen.findByText('Telemetry'));
    await screen.findByTestId('setup-item-metrics');
    expect(screen.queryByTestId('setup-telemetry-traces-gap')).not.toBeInTheDocument();
  });
});
