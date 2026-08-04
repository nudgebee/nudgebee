import React from 'react';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';

jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const mockListModelPricing = jest.fn();
const mockUpsert = jest.fn();
const mockDelete = jest.fn();
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listModelPricing: (...a) => mockListModelPricing(...a),
    upsertModelPricing: (...a) => mockUpsert(...a),
    deleteModelPricing: (...a) => mockDelete(...a),
  },
}));

const mockIsTenantAdmin = jest.fn(() => true);
jest.mock('@lib/auth', () => ({ isTenantAdmin: () => mockIsTenantAdmin() }));

import ModelPricingTab from '@components/llm/ModelPricingTab';

// The tab reads exactly one table. A model absent from it has no price — that
// IS "not priced" — so there is nothing else to cross-reference and no account
// to scope by.
const pricing = (rows) => ({ data: { prices: rows }, errors: [] });

const BUILT_IN_GPT4O = {
  model_name: 'gpt-4o',
  provider_name: 'openai',
  cost_per_million_input_tokens: 2.5,
  cost_per_million_output_tokens: 10,
  is_built_in: true,
};
// Gemini Pro bills roughly double above a threshold — the shape an override
// must not silently flatten.
const BUILT_IN_GEMINI_TIERED = {
  model_name: 'gemini-2.5-pro',
  provider_name: 'googleai',
  cost_per_million_input_tokens: 1.25,
  cost_per_million_output_tokens: 10,
  context_threshold_tokens: 200000,
  cost_per_million_input_tokens_long_ctx: 2.5,
  cost_per_million_output_tokens_long_ctx: 15,
  is_built_in: true,
};
const TENANT_GPT4O = {
  model_name: 'gpt-4o',
  provider_name: 'openai',
  cost_per_million_input_tokens: 1.8,
  cost_per_million_output_tokens: 7.2,
  is_built_in: false,
  pricing_updated_at: '2026-08-03T10:00:00Z',
};

const rowFor = (model) => screen.getAllByRole('row').find((r) => within(r).queryByText(model));

beforeEach(() => {
  jest.clearAllMocks();
  mockIsTenantAdmin.mockReturnValue(true);
});

// ─── which rate wins ───────────────────────────────────────────────────────

it('shows the tenant rate in place of the built-in, not both', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O, TENANT_GPT4O]));

  render(<ModelPricingTab />);

  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  const row = rowFor('gpt-4o');
  // The tenant's rate — the same precedence the server bills by.
  expect(within(row).getByText('$1.80')).toBeInTheDocument();
  expect(within(row).queryByText('$2.50')).not.toBeInTheDocument();
  expect(within(row).getByText('Custom rate')).toBeInTheDocument();
  expect(screen.getAllByRole('row').filter((r) => within(r).queryByText('gpt-4o'))).toHaveLength(1);
});

it('takes no account — the call carries nothing', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));

  render(<ModelPricingTab />);

  await waitFor(() => expect(mockListModelPricing).toHaveBeenCalled());
  expect(mockListModelPricing.mock.calls[0]).toHaveLength(0);
});

// ─── long-context tiering ──────────────────────────────────────────────────

it('surfaces a tiered rate rather than hiding it behind one number', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GEMINI_TIERED]));

  render(<ModelPricingTab />);

  await waitFor(() => expect(rowFor('gemini-2.5-pro')).toBeTruthy());
  expect(within(rowFor('gemini-2.5-pro')).getByText(/200k/)).toBeInTheDocument();
});

it('overriding a tiered model prefills its tier, so it is not dropped by accident', async () => {
  // The costly failure: a flat override on a tiered model bills long prompts at
  // the short rate, under-reporting spend by about half.
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GEMINI_TIERED]));
  mockUpsert.mockResolvedValue({ data: { saved: 1 }, errors: [] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gemini-2.5-pro')).toBeTruthy());
  fireEvent.click(within(rowFor('gemini-2.5-pro')).getByText('Override'));

  expect(screen.getByLabelText('Threshold (prompt tokens)')).toHaveValue(200000);
  expect(screen.getByLabelText('Input rate above threshold')).toHaveValue(2.5);

  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '1.0' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).toHaveBeenCalled());
  const [prices] = mockUpsert.mock.calls[0];
  expect(prices[0]).toMatchObject({
    cost_per_million_input_tokens: 1,
    context_threshold_tokens: 200000,
    cost_per_million_input_tokens_long_ctx: 2.5,
    cost_per_million_output_tokens_long_ctx: 15,
  });
});

it('refuses a half-filled tier — it would read as configured but bill flat', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Override'));

  fireEvent.change(screen.getByLabelText('Threshold (prompt tokens)'), { target: { value: '200000' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).not.toHaveBeenCalled());
});

it('warns before clearing a built-in tier', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GEMINI_TIERED]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gemini-2.5-pro')).toBeTruthy());
  fireEvent.click(within(rowFor('gemini-2.5-pro')).getByText('Override'));

  fireEvent.change(screen.getByLabelText('Threshold (prompt tokens)'), { target: { value: '' } });
  expect(await screen.findByText(/under-report spend/)).toBeInTheDocument();
});

// ─── prompt caching ────────────────────────────────────────────────────────

it('shows the cached rate in the table, so a rate you set is visible without opening the editor', async () => {
  mockListModelPricing.mockResolvedValue(pricing([{ ...BUILT_IN_GPT4O, cost_per_million_cached_input_tokens: 1.25 }]));

  render(<ModelPricingTab />);

  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  expect(within(rowFor('gpt-4o')).getByText('$1.25')).toBeInTheDocument();
});

it('sends the cached rates so cache hits are not billed at full input rate', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));
  mockUpsert.mockResolvedValue({ data: { saved: 1 }, errors: [] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Override'));

  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '1.8' } });
  fireEvent.change(screen.getByLabelText('Cached input rate'), { target: { value: '0.9' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).toHaveBeenCalled());
  expect(mockUpsert.mock.calls[0][0][0]).toMatchObject({
    cost_per_million_input_tokens: 1.8,
    cost_per_million_cached_input_tokens: 0.9,
  });
});

it('omits a blank cached rate rather than sending zero, which would bill cache hits as free', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));
  mockUpsert.mockResolvedValue({ data: { saved: 1 }, errors: [] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Override'));

  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '1.8' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).toHaveBeenCalled());
  expect(mockUpsert.mock.calls[0][0][0]).not.toHaveProperty('cost_per_million_cached_input_tokens');
});

it('prefills an existing cached rate so overriding does not silently drop it', async () => {
  const cached = { ...BUILT_IN_GPT4O, cost_per_million_cached_input_tokens: 1.25 };
  mockListModelPricing.mockResolvedValue(pricing([cached]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Override'));

  expect(screen.getByLabelText('Cached input rate')).toHaveValue(1.25);
});

it('refuses a negative cache rate before it reaches the server', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Override'));

  fireEvent.change(screen.getByLabelText('Cache creation rate'), { target: { value: '-1' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).not.toHaveBeenCalled());
});

// ─── adding a price for something not on file ──────────────────────────────

it('adds a price for a model the table has never seen', async () => {
  // The custom-endpoint case: nothing prices it, so it must be addable by name
  // rather than discovered from somewhere else.
  mockListModelPricing.mockResolvedValue(pricing([]));
  mockUpsert.mockResolvedValue({ data: { saved: 1 }, errors: [] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(screen.getByTestId('add-model-price')).toBeTruthy());
  fireEvent.click(screen.getByTestId('add-model-price'));

  // Provider is a Select, not free text: a typo like `open-ai` would store a
  // price that never matches a usage row, so the value has to be picked.
  fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'custom' } });
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'Qwen/Qwen3.6-35B-A3B-FP8' } });
  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '0.4' } });
  fireEvent.change(screen.getByLabelText('Output rate'), { target: { value: '0.6' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).toHaveBeenCalled());
  expect(mockUpsert.mock.calls[0][0][0]).toMatchObject({
    provider_name: 'custom',
    model_name: 'Qwen/Qwen3.6-35B-A3B-FP8',
    cost_per_million_input_tokens: 0.4,
  });
});

it('will not add a price with no provider or model to attach it to', async () => {
  mockListModelPricing.mockResolvedValue(pricing([]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(screen.getByTestId('add-model-price')).toBeTruthy());
  fireEvent.click(screen.getByTestId('add-model-price'));

  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '1' } });
  fireEvent.change(screen.getByLabelText('Output rate'), { target: { value: '1' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).not.toHaveBeenCalled());
});

it('refuses a negative rate before it reaches the server', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Override'));

  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '-5' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).not.toHaveBeenCalled());
});

// ─── permissions and empty states ──────────────────────────────────────────

it('a non-admin sees the rates but no way to change them', async () => {
  mockIsTenantAdmin.mockReturnValue(false);
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));

  render(<ModelPricingTab />);

  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  expect(within(rowFor('gpt-4o')).getByText('$2.50')).toBeInTheDocument();
  expect(screen.queryByText('Override')).not.toBeInTheDocument();
  expect(screen.queryByTestId('add-model-price')).not.toBeInTheDocument();
  expect(await screen.findByText(/tenant admin/)).toBeInTheDocument();
});

// A blank table with no explanation is the failure mode that sent us hunting:
// it looks identical whether the fetch failed, nothing is on file, or a filter
// excluded everything.
it('a failed fetch says so instead of rendering an empty table', async () => {
  mockListModelPricing.mockResolvedValue({ data: {}, errors: [{ message: 'llm-server unreachable' }] });

  render(<ModelPricingTab />);

  expect(await screen.findByText('Could not load pricing.')).toBeInTheDocument();
});

it('an empty table explains what to do', async () => {
  mockListModelPricing.mockResolvedValue(pricing([]));

  render(<ModelPricingTab />);

  expect(await screen.findByText('No pricing on file')).toBeInTheDocument();
});

it('filtering everything out is distinguishable from having nothing', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GPT4O]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());

  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'nothing-matches-this' } });

  expect(await screen.findByText('No models match these filters')).toBeInTheDocument();
});

// ─── removing a tenant override ────────────────────────────────────────────

it('offers Remove on a tenant rate but not on a built-in', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GEMINI_TIERED, { ...TENANT_GPT4O, has_built_in: true }]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());

  // A built-in has no override to drop; the server would refuse it anyway.
  expect(within(rowFor('gemini-2.5-pro')).queryByText('Remove')).not.toBeInTheDocument();
  expect(within(rowFor('gpt-4o')).getByText('Remove')).toBeInTheDocument();
});

it('confirms before removing, and says the model reverts to the built-in rate', async () => {
  mockListModelPricing.mockResolvedValue(pricing([{ ...TENANT_GPT4O, has_built_in: true }]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Remove'));

  expect(await screen.findByText(/will bill at the built-in rate again/)).toBeInTheDocument();
  // Nothing is sent until the confirm is clicked.
  expect(mockDelete).not.toHaveBeenCalled();
});

// The costly case: with nothing underneath, removing does not revert a price —
// it stops pricing the model, and its spend silently reports as zero.
it('warns that removing leaves the model unpriced when no built-in exists', async () => {
  mockListModelPricing.mockResolvedValue(
    pricing([
      {
        model_name: 'Qwen/Qwen3.6-35B',
        provider_name: 'custom',
        cost_per_million_input_tokens: 0.4,
        cost_per_million_output_tokens: 0.6,
        is_built_in: false,
        has_built_in: false,
      },
    ])
  );

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('Qwen/Qwen3.6-35B')).toBeTruthy());
  fireEvent.click(within(rowFor('Qwen/Qwen3.6-35B')).getByText('Remove'));

  expect(await screen.findByText('No built-in rate to fall back on')).toBeInTheDocument();
  expect(screen.getByText(/report as \$0/)).toBeInTheDocument();
});

it('sends provider and model on confirm, then reloads', async () => {
  mockListModelPricing.mockResolvedValue(pricing([{ ...TENANT_GPT4O, has_built_in: true }]));
  mockDelete.mockResolvedValue({ data: { removed: 1 }, errors: [] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Remove'));
  fireEvent.click(screen.getByTestId('confirm-remove-price'));

  await waitFor(() => expect(mockDelete).toHaveBeenCalledWith('openai', 'gpt-4o'));
  await waitFor(() => expect(mockListModelPricing).toHaveBeenCalledTimes(2));
});

it('keeps the dialog open when the server refuses', async () => {
  mockListModelPricing.mockResolvedValue(pricing([{ ...TENANT_GPT4O, has_built_in: true }]));
  mockDelete.mockResolvedValue({ data: {}, errors: [{ message: 'model pricing can only be changed by a tenant admin' }] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(rowFor('gpt-4o')).toBeTruthy());
  fireEvent.click(within(rowFor('gpt-4o')).getByText('Remove'));
  fireEvent.click(screen.getByTestId('confirm-remove-price'));

  await waitFor(() => expect(mockDelete).toHaveBeenCalled());
  expect(screen.getByTestId('confirm-remove-price')).toBeInTheDocument();
});

// The typo class: `open-ai` or a stray capital would store a row that never
// matches a usage row, so the model reads as priced and still reports $0.
it('rejects a provider that is not a real one, rather than storing an unmatchable row', async () => {
  mockListModelPricing.mockResolvedValue(pricing([]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(screen.getByTestId('add-model-price')).toBeTruthy());
  fireEvent.click(screen.getByTestId('add-model-price'));

  fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'open-ai' } });
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'gpt-4o' } });
  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '1' } });
  fireEvent.change(screen.getByLabelText('Output rate'), { target: { value: '1' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).not.toHaveBeenCalled());
});

// Case and whitespace are recoverable, so recover them instead of rejecting.
it('normalises provider case and whitespace so a near-miss still matches', async () => {
  mockListModelPricing.mockResolvedValue(pricing([]));
  mockUpsert.mockResolvedValue({ data: { saved: 1 }, errors: [] });

  render(<ModelPricingTab />);
  await waitFor(() => expect(screen.getByTestId('add-model-price')).toBeTruthy());
  fireEvent.click(screen.getByTestId('add-model-price'));

  fireEvent.change(screen.getByLabelText('Provider'), { target: { value: '  OpenAI ' } });
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'gpt-4o' } });
  fireEvent.change(screen.getByLabelText('Input rate'), { target: { value: '1' } });
  fireEvent.change(screen.getByLabelText('Output rate'), { target: { value: '2' } });
  fireEvent.click(screen.getByText('Save'));

  await waitFor(() => expect(mockUpsert).toHaveBeenCalled());
  expect(mockUpsert.mock.calls[0][0][0].provider_name).toBe('openai');
});

// The warning lookup must normalise exactly as the save does. Typing `GoogleAI`
// used to find no built-in, so the tier-drop warning stayed hidden while the
// save itself matched and dropped the tier.
it('warns about dropping a tier even when the provider is typed with odd casing', async () => {
  mockListModelPricing.mockResolvedValue(pricing([BUILT_IN_GEMINI_TIERED]));

  render(<ModelPricingTab />);
  await waitFor(() => expect(screen.getByTestId('add-model-price')).toBeTruthy());
  fireEvent.click(screen.getByTestId('add-model-price'));

  fireEvent.change(screen.getByLabelText('Provider'), { target: { value: ' GoogleAI ' } });
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'gemini-2.5-pro' } });

  expect(await screen.findByText(/under-report spend/)).toBeInTheDocument();
});
