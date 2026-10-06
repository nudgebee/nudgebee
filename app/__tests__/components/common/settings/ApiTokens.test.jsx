import React from 'react';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom';
import ApiTokens from '@shared/settings/ApiTokens';

jest.mock('@utils/colors');

jest.mock('@ui/Modal', () => ({
  __esModule: true,
  // The standard footer mirrors ds/Modal: `loader` disables both buttons, `confirmDisabled` the confirm.
  Modal: ({ open, onClose, onConfirm, confirmText, confirmDisabled, loader, title, children, actionButtons }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
        {actionButtons}
        {!actionButtons && confirmText && (
          <>
            <button onClick={onClose} disabled={loader} data-testid='btn-Cancel'>
              Cancel
            </button>
            <button onClick={onConfirm} disabled={confirmDisabled || loader} data-testid={`btn-${confirmText}`}>
              {confirmText}
            </button>
          </>
        )}
      </div>
    ) : null,
}));

jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick, disabled, loading }) => (
    <button onClick={onClick} disabled={disabled || loading} data-testid={`btn-${children}`}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Input', () => ({
  __esModule: true,
  Input: ({ label, value, onChange, placeholder }) => (
    <input
      aria-label={label}
      value={value || ''}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
      data-testid={`field-${label}`}
    />
  ),
}));

jest.mock('@ui/Select', () => ({
  __esModule: true,
  Select: ({ label, value, onChange, options, multiple, instructionText }) => (
    <>
      <select
        aria-label={label}
        multiple={multiple}
        value={value ?? (multiple ? [] : '')}
        onChange={(e) => onChange(multiple ? [...e.target.selectedOptions].map((o) => o.value) : e.target.value)}
        data-testid={`select-${label}`}
      >
        {options.map((o) => {
          const opt = typeof o === 'string' ? { value: o, label: o } : o;
          return (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          );
        })}
      </select>
      {instructionText && <span data-testid={`help-${label}`}>{instructionText}</span>}
    </>
  ),
}));

jest.mock('@ui/Banner', () => ({
  __esModule: true,
  Banner: ({ tone, title, message }) => (
    <div data-testid={`banner-${tone}`}>
      {title}
      {message}
    </div>
  ),
}));

jest.mock('@ui/EmptyState', () => ({
  __esModule: true,
  EmptyState: ({ title }) => <div data-testid='empty-state'>{title}</div>,
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData }) => (
    <table>
      <tbody>
        {tableData.map((row, i) => (
          <tr key={i}>
            {row.map((cell, j) => (
              <td key={j}>{cell.component || cell.text}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => React.createElement('img', { alt, 'data-testid': 'safe-icon', ...props }),
}));

jest.mock('@assets', () => ({
  DeleteIconRed: 'delete-icon-red.svg',
}));

jest.mock('@lib/externalUrls', () => ({
  getAppBaseUrl: () => 'http://localhost:3000',
}));

const mockListUserTokens = jest.fn().mockResolvedValue({ data: [] });
const mockCreateUserToken = jest.fn().mockResolvedValue({ data: { token: 'new-token-value-xyz' } });
const mockDeleteUserToken = jest.fn().mockResolvedValue({ data: {} });
const mockUpdateUserToken = jest.fn().mockResolvedValue({ data: { status: 'updated' } });

jest.mock('@api1/user/', () => ({
  __esModule: true,
  default: {
    listUserTokens: (...args) => mockListUserTokens(...args),
    createUserToken: (...args) => mockCreateUserToken(...args),
    deleteUserToken: (...args) => mockDeleteUserToken(...args),
    updateUserToken: (...args) => mockUpdateUserToken(...args),
  },
}));

jest.mock('@ui/CodeBlock', () => ({
  __esModule: true,
  CodeBlock: ({ code }) => <code>{code}</code>,
}));

const mockListGatewayModels = jest.fn();
jest.mock('@api1/gateway-config', () => ({
  GATEWAY_MATCH_PROVIDER_OPTIONS: [
    { value: 'anthropic', label: 'anthropic' },
    { value: 'openai', label: 'openai' },
    { value: 'gemini', label: 'gemini' },
    { value: 'vllm', label: 'custom / vertex-openai' },
    { value: 'vertex', label: 'vertex' },
    { value: 'bedrock', label: 'bedrock' },
  ],
  listGatewayModels: (...args) => mockListGatewayModels(...args),
}));

const mockGetTokenScopeCatalog = jest.fn();
jest.mock('@api1/roles', () => ({
  getTokenScopeCatalog: (...args) => mockGetTokenScopeCatalog(...args),
}));

const SCOPE_CATALOG = [
  { module: 'tickets', scope: 'account', classes: ['Read', 'Write'], actions: {} },
  { module: 'workflows', scope: 'account', classes: ['Read', 'Write', 'Execute'], actions: {} },
  { module: 'users', scope: 'tenant', classes: ['Read', 'Write'], actions: {} },
];

jest.mock('src/utils/common', () => ({
  parseHttpResponseBodyMessage: jest.fn().mockReturnValue('Error occurred'),
}));

describe('ApiTokens', () => {
  const defaultProps = {
    open: true,
    title: 'API Tokens',
    onClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    mockListUserTokens.mockResolvedValue({ data: [] });
    mockCreateUserToken.mockResolvedValue({ data: { token: 'new-token-value-xyz' } });
    mockDeleteUserToken.mockResolvedValue({ data: {} });
    mockGetTokenScopeCatalog.mockResolvedValue(SCOPE_CATALOG);
    mockUpdateUserToken.mockResolvedValue({ data: { status: 'updated' } });
    mockListGatewayModels.mockResolvedValue([
      { id: 'openai/gpt-4o-mini', provider: 'openai' },
      { id: 'anthropic/claude-haiku-4-5', provider: 'anthropic' },
    ]);
  });

  it('renders modal when open is true', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });
    expect(screen.getByRole('heading', { name: 'API Tokens' })).toBeInTheDocument();
    // The modal header names the screen; the body does not repeat it.
    expect(screen.getAllByText('API Tokens')).toHaveLength(1);
  });

  it('does not render modal when open is false', () => {
    render(<ApiTokens {...defaultProps} open={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('shows empty state when no tokens', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument();
    });
    expect(screen.getByText('No API tokens yet')).toBeInTheDocument();
  });

  it('shows Create Token button initially', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /create token/i })).toBeInTheDocument();
    });
  });

  it('lists every key in one table, labelled by its type', async () => {
    const base = { status: 'active', created_at: '2026-09-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [], scopes: null };
    mockListUserTokens.mockResolvedValue({
      data: [
        { ...base, id: '1', name: 'nbctl', kind: 'platform' },
        { ...base, id: '2', name: 'claude-code', kind: 'gateway', limits: [{ metric: 'cost', period: 'month', value: 20, model: '' }] },
        { ...base, id: '3', name: 'shared', kind: 'all' },
      ],
    });
    render(<ApiTokens {...defaultProps} />);
    const row = async (name) => (await screen.findByText(name)).closest('tr');
    expect(within(await row('nbctl')).getByText('Platform')).toBeInTheDocument();
    expect(within(await row('claude-code')).getByText('AI Gateway')).toBeInTheDocument();
    expect(within(await row('shared')).getByText('All access')).toBeInTheDocument();
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    // Scopes are platform-only and limits meter gateway calls only, so each row shows its own.
    expect(within(await row('nbctl')).getByText('Full access')).toBeInTheDocument();
    expect(within(await row('claude-code')).getByText('20 cost/month')).toBeInTheDocument();
    expect(within(await row('claude-code')).queryByText('Full access')).not.toBeInTheDocument();
  });

  it('the create form starts on the platform type, never all-access', async () => {
    render(<ApiTokens {...defaultProps} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    expect(screen.getByRole('radio', { name: /^platform$/i })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: /all access/i })).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('radio', { name: /restricted/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /add limit/i })).not.toBeInTheDocument();
  });

  it('a cancelled all-access draft does not carry over to the next create', async () => {
    render(<ApiTokens {...defaultProps} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.click(screen.getByRole('radio', { name: /all access/i }));
    await userEvent.click(screen.getByRole('button', { name: /^cancel$/i }));
    await userEvent.click(screen.getByRole('button', { name: /create token/i }));
    expect(screen.getByRole('radio', { name: /^platform$/i })).toHaveAttribute('aria-checked', 'true');
  });

  it('a failed create keeps the draft, type included, for a retry', async () => {
    mockCreateUserToken.mockResolvedValue({ errors: [{ message: 'boom' }] });
    render(<ApiTokens {...defaultProps} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.click(screen.getByRole('radio', { name: /all access/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'shared');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    await waitFor(() => expect(mockCreateUserToken).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('radio', { name: /all access/i })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByLabelText(/name/i)).toHaveValue('shared');
  });

  it('keeps Create disabled while the request is in flight', async () => {
    let finish;
    mockCreateUserToken.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    render(<ApiTokens {...defaultProps} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'ci');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
    finish({ data: { token: 'sk-nb-xyz' } });
    await waitFor(() => expect(screen.getByText('sk-nb-xyz')).toBeInTheDocument());
  });

  it('defaultKind picks the type the create form starts on', async () => {
    render(<ApiTokens {...defaultProps} defaultKind='gateway' />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    expect(screen.getByRole('radio', { name: /ai gateway/i })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('button', { name: /add limit/i })).toBeInTheDocument();
    // A gateway key's access means models and endpoints, never the platform scope grid.
    expect(screen.queryByTestId('token-scope-picker')).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /all models/i })).toHaveAttribute('aria-checked', 'true');
  });

  it('an all-access key create sends kind=all and carries limits', async () => {
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-xyz', name: 'shared', kind: 'all' } });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    // 'all' opens both doors, so it is only ever minted by picking it.
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.click(screen.getByRole('radio', { name: /all access/i }));
    expect(screen.getByText(/opens both the app api and the ai gateway/i)).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/name/i), 'shared');
    await userEvent.click(screen.getByRole('button', { name: /add limit/i }));
    await userEvent.type(screen.getByLabelText(/value/i), '20');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    await waitFor(() => {
      expect(mockCreateUserToken).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'shared',
          kind: 'all',
          limits: [expect.objectContaining({ metric: 'cost', period: 'month', value: 20 })],
        })
      );
    });
  });

  it('a platform key create sends no limits', async () => {
    mockListUserTokens.mockResolvedValue({ data: [] });
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-xyz', name: 'nbctl', kind: 'platform' } });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'nbctl');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(expect.objectContaining({ name: 'nbctl', kind: 'platform', limits: [] }));
  });

  it('a full-access platform key sends no scopes', async () => {
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-xyz', name: 'ci', kind: 'platform' } });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'ci');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(expect.objectContaining({ kind: 'platform', scopes: null }));
    expect(mockGetTokenScopeCatalog).not.toHaveBeenCalled();
  });

  it('a restricted platform key sends the ticked scopes; unticking Read clears Write', async () => {
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-xyz', name: 'ci', kind: 'platform' } });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'ci');
    await userEvent.click(screen.getByRole('radio', { name: /restricted/i }));
    expect(await screen.findByText(/started by this key use your full access/i)).toBeInTheDocument();
    // Write stays disabled until Read is ticked, as in the role editor.
    expect(await screen.findByRole('checkbox', { name: /tickets — write/i })).toBeDisabled();
    await userEvent.click(screen.getByRole('checkbox', { name: /tickets — read/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /tickets — write/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /automations — read/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /automations — write/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /automations — read/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /automations — execute/i }));
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'platform', scopes: ['tickets:Read', 'tickets:Write', 'workflows:Execute'] })
    );
  });

  it('keeps Create disabled for a restricted key with no scope ticked', async () => {
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'ci');
    await userEvent.click(screen.getByRole('radio', { name: /restricted/i }));
    await screen.findByRole('checkbox', { name: /tickets — read/i });
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
  });

  it('shows each platform key as full access or its scope count', async () => {
    const base = { status: 'active', created_at: '2026-09-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [], kind: 'platform' };
    mockListUserTokens.mockResolvedValue({
      data: [
        { ...base, id: '1', name: 'open', scopes: null },
        { ...base, id: '2', name: 'narrow', scopes: ['tickets:Read', 'tickets:Write'] },
      ],
    });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    expect(await screen.findByText('Full access')).toBeInTheDocument();
    expect(screen.getByText('2 scopes')).toBeInTheDocument();
  });

  it('a custom expiry in hours sends an expires_at roughly that far ahead', async () => {
    mockListUserTokens.mockResolvedValue({ data: [] });
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-xyz', name: 'nbctl', kind: 'platform' } });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'nbctl');
    await userEvent.selectOptions(screen.getByLabelText(/^expires$/i), 'custom');
    await userEvent.selectOptions(screen.getByLabelText(/^unit$/i), 'hours');
    await userEvent.type(screen.getByLabelText(/^duration$/i), '1');
    const before = Date.now();
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    const after = Date.now();
    expect(mockCreateUserToken).toHaveBeenCalledTimes(1);
    const [payload] = mockCreateUserToken.mock.calls[0];
    const expiresAtMs = new Date(payload.expiresAt).getTime();
    const oneHourMs = 60 * 60 * 1000;
    // Range, not an exact string — this test must not flake on clock drift between
    // `before`/`after` and the component's own `Date.now()` call.
    expect(expiresAtMs).toBeGreaterThanOrEqual(before + oneHourMs - 5000);
    expect(expiresAtMs).toBeLessThanOrEqual(after + oneHourMs + 5000);
  });

  it('leaves Create disabled when the custom expiry is empty or zero', async () => {
    mockListUserTokens.mockResolvedValue({ data: [] });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'nbctl');
    await userEvent.selectOptions(screen.getByLabelText(/^expires$/i), 'custom');
    // Empty custom value.
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
    // Zero is just as invalid as empty.
    await userEvent.type(screen.getByLabelText(/^duration$/i), '0');
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
  });

  it('a gateway key create filters out zero-value limit rows', async () => {
    mockListUserTokens.mockResolvedValue({ data: [] });
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-gw-xyz', name: 'claude-code', kind: 'gateway' } });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.click(screen.getByRole('radio', { name: /ai gateway/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'claude-code');
    await userEvent.click(screen.getByRole('button', { name: /add limit/i }));
    // Leave the value at '' (invalid, filtered out) and add a second, valid limit.
    await userEvent.click(screen.getByRole('button', { name: /add limit/i }));
    const valueInputs = screen.getAllByLabelText(/value/i);
    await userEvent.type(valueInputs[1], '20');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'claude-code',
        kind: 'gateway',
        limits: [expect.objectContaining({ value: 20 })],
      })
    );
  });

  it('switching to platform drops the limits a gateway draft had', async () => {
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-xyz', name: 'ci', kind: 'platform' } });
    render(<ApiTokens {...defaultProps} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'ci');
    await userEvent.click(screen.getByRole('radio', { name: /ai gateway/i }));
    await userEvent.click(screen.getByRole('button', { name: /add limit/i }));
    await userEvent.type(screen.getByLabelText(/value/i), '20');
    await userEvent.click(screen.getByRole('radio', { name: /^platform$/i }));
    expect(screen.queryByRole('button', { name: /add limit/i })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(expect.objectContaining({ kind: 'platform', limits: [], scopes: null }));
  });

  it('switching to AI Gateway drops the scopes a platform draft had', async () => {
    mockCreateUserToken.mockResolvedValue({ data: { token: 'sk-nb-gw-xyz', name: 'ci', kind: 'gateway' } });
    render(<ApiTokens {...defaultProps} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.type(screen.getByLabelText(/name/i), 'ci');
    await userEvent.click(screen.getByRole('radio', { name: /restricted/i }));
    await userEvent.click(await screen.findByRole('checkbox', { name: /tickets — read/i }));
    await userEvent.click(screen.getByRole('radio', { name: /ai gateway/i }));
    // The platform Restricted choice does not carry over: the gateway key starts on All models.
    expect(screen.queryByTestId('token-scope-picker')).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /all models/i })).toHaveAttribute('aria-checked', 'true');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(expect.objectContaining({ kind: 'gateway', scopes: null }));
  });

  const openGatewayCreate = async (name) => {
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.click(screen.getByRole('radio', { name: /ai gateway/i }));
    await userEvent.type(screen.getByLabelText(/name/i), name);
  };

  it('an unrestricted gateway key sends no policy and never loads the model catalog', async () => {
    await openGatewayCreate('laptop');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(expect.objectContaining({ kind: 'gateway', gatewayPolicy: null }));
    expect(mockListGatewayModels).not.toHaveBeenCalled();
  });

  it('a restricted gateway key sends the picked endpoints and models, leaving providers open', async () => {
    await openGatewayCreate('ci');
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /^chat$/i }));
    await waitFor(() => expect(screen.getByRole('option', { name: 'openai/gpt-4o-mini' })).toBeInTheDocument());
    await userEvent.selectOptions(screen.getByLabelText(/^models$/i), 'openai/gpt-4o-mini');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'gateway',
        gatewayPolicy: { providers: null, models: ['openai/gpt-4o-mini'], endpoints: ['chat'] },
      })
    );
  });

  it('keeps Create disabled for a restricted gateway key with nothing picked', async () => {
    await openGatewayCreate('ci');
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
    await userEvent.click(screen.getByRole('checkbox', { name: /^embeddings$/i }));
    expect(screen.getByRole('button', { name: /^create$/i })).toBeEnabled();
  });

  it('an all-access key with a gateway restriction must also restrict its app API access', async () => {
    await openGatewayCreate('shared');
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /^chat$/i }));
    await userEvent.click(screen.getByRole('radio', { name: /all access/i }));
    // Unscoped, the key could mint an unrestricted one, so Create waits for scopes.
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
    // Gateway toggle renders first, app API toggle second.
    await userEvent.click(screen.getAllByRole('radio', { name: /^restricted$/i })[1]);
    await userEvent.click(await screen.findByRole('checkbox', { name: /tickets — read/i }));
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'all',
        scopes: ['tickets:Read'],
        gatewayPolicy: { providers: null, models: null, endpoints: ['chat'] },
      })
    );
  });

  it('shows each gateway key as all models or its restriction, and edits access in place', async () => {
    const base = { status: 'active', created_at: '2026-09-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [], kind: 'gateway' };
    mockListUserTokens.mockResolvedValue({
      data: [
        { ...base, id: 'k1', name: 'laptop', gateway_policy: null },
        { ...base, id: 'k2', name: 'ci', gateway_policy: { providers: null, models: ['openai/gpt-4o-mini'], endpoints: ['chat'] } },
      ],
    });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    expect(await screen.findByText('All models')).toBeInTheDocument();
    expect(screen.getByText('openai/gpt-4o-mini · chat')).toBeInTheDocument();

    await userEvent.click(screen.getAllByRole('button', { name: /edit access/i })[1]);
    expect(await screen.findByRole('heading', { name: 'Access for ci' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /^chat$/i })).toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: /all models/i }));
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(mockUpdateUserToken).toHaveBeenCalledWith({ id: 'k2', gatewayPolicy: {} }));
  });

  it('endpoints start on All, the last endpoint cannot be unselected, and only All widens the key again', async () => {
    await openGatewayCreate('ci');
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    const endpoint = (name) => screen.getByRole('checkbox', { name });
    expect(endpoint(/^all$/i)).toBeChecked();

    await userEvent.click(endpoint(/^chat$/i));
    expect(endpoint(/^chat$/i)).toBeChecked();
    expect(endpoint(/^all$/i)).not.toBeChecked();

    // Unselecting the only endpoint must not quietly open the key to every endpoint.
    await userEvent.click(endpoint(/^chat$/i));
    expect(endpoint(/^chat$/i)).toBeChecked();
    expect(endpoint(/^all$/i)).not.toBeChecked();

    await userEvent.click(endpoint(/^all$/i));
    expect(endpoint(/^all$/i)).toBeChecked();
    expect(endpoint(/^chat$/i)).not.toBeChecked();
    // With every endpoint open and nothing else picked there is nothing to restrict.
    expect(screen.getByRole('button', { name: /^create$/i })).toBeDisabled();
  });

  it('a restricted key with All endpoints sends no endpoint list', async () => {
    await openGatewayCreate('ci');
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /^chat$/i }));
    await userEvent.click(screen.getByRole('checkbox', { name: /^all$/i }));
    await waitFor(() => expect(screen.getByRole('option', { name: 'openai/gpt-4o-mini' })).toBeInTheDocument());
    await userEvent.selectOptions(screen.getByLabelText(/^models$/i), 'openai/gpt-4o-mini');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));
    expect(mockCreateUserToken).toHaveBeenCalledWith(
      expect.objectContaining({ gatewayPolicy: { providers: null, models: ['openai/gpt-4o-mini'], endpoints: null } })
    );
  });

  it('says so when the model catalog fails to load, and still offers every provider lane', async () => {
    mockListGatewayModels.mockRejectedValue(new Error('gateway unavailable'));
    await openGatewayCreate('ci');
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    expect(await screen.findByText(/couldn't load your model catalog/i)).toBeInTheDocument();
    const providers = within(screen.getByLabelText(/^providers$/i))
      .getAllByRole('option')
      .map((o) => o.value);
    expect(providers).toEqual(expect.arrayContaining(['anthropic', 'openai', 'gemini', 'vllm', 'vertex', 'bedrock']));
  });

  it('does not report a catalog failure for a request aborted by a remount', async () => {
    // StrictMode mounts effects twice in dev: the first request is aborted, the second succeeds.
    mockListGatewayModels.mockImplementation(
      (signal) =>
        new Promise((resolve, reject) => {
          signal.addEventListener('abort', () => reject(new Error('aborted')));
          setTimeout(() => resolve([{ id: 'openai/gpt-4o-mini', provider: 'openai' }]), 0);
        })
    );
    render(
      <React.StrictMode>
        <ApiTokens open title='API Tokens' onClose={() => {}} />
      </React.StrictMode>
    );
    await userEvent.click(await screen.findByRole('button', { name: /create token/i }));
    await userEvent.click(screen.getByRole('radio', { name: /ai gateway/i }));
    await userEvent.click(screen.getByRole('radio', { name: /^restricted$/i }));
    expect(await screen.findByRole('option', { name: 'openai/gpt-4o-mini' })).toBeInTheDocument();
    expect(screen.queryByText(/couldn't load your model catalog/i)).not.toBeInTheDocument();
  });

  it('disables Save and Cancel in the edit access dialog while the change is saving', async () => {
    const base = { status: 'active', created_at: '2026-09-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [], kind: 'gateway' };
    mockListUserTokens.mockResolvedValue({
      data: [{ ...base, id: 'k2', name: 'ci', gateway_policy: { providers: null, models: null, endpoints: ['chat'] } }],
    });
    let finishSave;
    mockUpdateUserToken.mockReturnValue(
      new Promise((resolve) => {
        finishSave = resolve;
      })
    );
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /edit access/i }));
    await userEvent.click(screen.getByRole('radio', { name: /all models/i }));
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));

    const dialog = screen.getByRole('heading', { name: 'Access for ci' }).closest('[data-testid="modal"]');
    expect(within(dialog).getByRole('button', { name: /^save$/i })).toBeDisabled();
    expect(within(dialog).getByRole('button', { name: /^cancel$/i })).toBeDisabled();
    expect(mockUpdateUserToken).toHaveBeenCalledTimes(1);

    finishSave({ data: { status: 'updated' } });
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Access for ci' })).not.toBeInTheDocument());
  });

  it('offers Edit access on AI Gateway keys and scoped all-access keys only', async () => {
    const base = { status: 'active', created_at: '2026-09-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [] };
    mockListUserTokens.mockResolvedValue({
      data: [
        { ...base, id: 'p1', name: 'nbctl', kind: 'platform' },
        { ...base, id: 'a1', name: 'shared', kind: 'all' },
        { ...base, id: 'a2', name: 'scoped', kind: 'all', scopes: ['tickets:Read'], gateway_policy: null },
        { ...base, id: 'g1', name: 'laptop', kind: 'gateway', gateway_policy: null },
      ],
    });
    render(<ApiTokens open title='API Tokens' onClose={() => {}} />);
    await screen.findByText('laptop');
    expect(screen.getAllByRole('button', { name: /edit access/i })).toHaveLength(2);
    expect(within(screen.getByText('scoped').closest('tr')).getByText('1 scope')).toBeInTheDocument();
  });

  it('revokes a key by id', async () => {
    mockListUserTokens.mockResolvedValue({
      data: [
        { id: 'tok-1', name: 'My Token', kind: 'platform', created_at: '2024-01-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [] },
      ],
    });
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByText('My Token')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole('button', { name: /revoke/i }));
    fireEvent.click(screen.getByTestId('btn-Delete'));
    await waitFor(() => {
      expect(mockDeleteUserToken).toHaveBeenCalledWith({ id: 'tok-1' });
    });
  });

  it('calls onClose when Close button is clicked', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-Close')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-Close'));
    expect(defaultProps.onClose).toHaveBeenCalled();
  });

  it('shows How to use instructions modal when button clicked', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-How to use')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-How to use'));
    await waitFor(() => {
      expect(screen.getByText('How to use API Tokens')).toBeInTheDocument();
    });
  });
});
