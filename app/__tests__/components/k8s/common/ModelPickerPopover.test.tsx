import React from 'react';
import { render, screen, fireEvent, within } from '@testing-library/react';

// Mock @assets so importing TextAreaV2 doesn't pull image binaries through jest.
jest.mock('@assets', () => ({
  ArrowRightWhiteIcon: 'arrow-right-mock',
  CustomAgentBlueIcon: 'agent-blue-mock',
}));

jest.mock('@components/llm/common/AgentIcon', () => ({
  getIcon: jest.fn(() => 'agent-icon-mock'),
}));

// Source migrated to @shared/icons/SafeIcon.
jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: { alt: string }) => <span data-testid='safe-icon'>{alt}</span>,
}));

jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

import { ModelPickerPopover } from '@components/k8s/common/TextAreaV2';

// ai_list_models `models[]` — one row per configured slot, each keeping the name
// of the config it belongs to. This is the ungrouped list the picker works from.
//
// piyush-llm and hsundar-gemini deliberately share an API key: `credentials[]`
// folds them into a single entry under one name, which is exactly the collapse
// the picker must not inherit.
const MODELS = [
  { model: 'gemini-3-flash-preview', provider: 'googleai', configSource: 'env:global', configName: 'System · Default' },
  { model: 'gemini-3.1-pro-preview', provider: 'googleai', configSource: 'env:tier:reasoning', configName: 'System · Reasoning tier' },
  { model: 'gemini-2.5-pro', provider: 'googleai', configSource: 'env:tier:summary', configName: 'System · Summary tier' },

  { model: 'gemini-3-flash-preview', provider: 'googleai', configSource: 'db:182f5d24', configName: 'piyush-llm' },
  { model: 'gemini-3-flash-preview', provider: 'googleai', configSource: 'db:182f5d24:tier:retrieval', configName: 'piyush-llm · Retrieval tier' },
  { model: 'gemini-2.5-flash', provider: 'googleai', configSource: 'db:182f5d24:tier:summary', configName: 'piyush-llm · Summary tier' },

  { model: 'gemini-3.1-pro-preview', provider: 'googleai', configSource: 'db:9a1b2c3d', configName: 'hsundar-gemini' },
  // Same slot as its primary — a fallback row differs only by model + the flag.
  {
    model: 'gemini-2.5-flash-lite',
    provider: 'googleai',
    configSource: 'db:9a1b2c3d:tier:summary',
    configName: 'hsundar-gemini · Summary tier (fallback)',
    isFallback: true,
  },
];

// Only still needed so the trigger can name the config behind a stored pin.
const CREDENTIALS = [
  {
    id: 'googleai|eba3a87f||',
    name: 'System · Default',
    provider: 'googleai',
    configSource: 'env:global',
    sources: ['env:global', 'env:tier:reasoning', 'env:tier:summary'],
    models: [{ model: 'gemini-3-flash-preview' }, { model: 'gemini-3.1-pro-preview' }, { model: 'gemini-2.5-pro' }],
  },
  {
    id: 'googleai|0cc04dd9||',
    name: 'piyush-llm',
    provider: 'googleai',
    configSource: 'db:182f5d24',
    sources: ['db:182f5d24', 'db:182f5d24:tier:retrieval', 'db:182f5d24:tier:summary', 'db:9a1b2c3d', 'db:9a1b2c3d:tier:summary'],
    models: [{ model: 'gemini-3-flash-preview' }, { model: 'gemini-2.5-flash' }],
  },
];

describe('ModelPickerPopover', () => {
  let onModelSelect: jest.Mock;
  let onTierModelsSelect: jest.Mock;
  let onConfigSelect: jest.Mock;

  beforeEach(() => {
    onModelSelect = jest.fn();
    onTierModelsSelect = jest.fn();
    onConfigSelect = jest.fn();
  });

  const renderPicker = (props: Record<string, unknown> = {}) =>
    render(
      <ModelPickerPopover
        credentials={CREDENTIALS}
        models={MODELS}
        onModelSelect={onModelSelect}
        onTierModelsSelect={onTierModelsSelect}
        onConfigSelect={onConfigSelect}
        {...props}
      />
    );

  function openPicker() {
    fireEvent.click(screen.getByTestId('model-picker-trigger'));
  }
  /** Left pane — one row per config. Clicking navigates; it never selects. */
  const configPane = () => within(screen.getByTestId('config-pane'));
  /** Right pane — the active config's models. Clicking stages the selection. */
  const modelPane = () => within(screen.getByTestId('model-pane'));

  const openConfig = (name: string) => fireEvent.click(configPane().getByText(name));
  const chooseModel = (name: string) => fireEvent.click(modelPane().getByText(name));
  /** Tier names also appear in the summary panel, so scope to the toggle. */
  const switchTier = (label: string) => fireEvent.click(within(screen.getByRole('group', { name: 'Active task' })).getByText(label));

  // ─── config axis: one row per config, no credential folding ──────────────

  it('Lists every config, including two the credential fold collapses into one', () => {
    renderPicker();
    openPicker();

    // Three configs, even though piyush-llm and hsundar-gemini share a key and
    // arrive as a single credential.
    expect(configPane().getAllByRole('option')).toHaveLength(3);
    expect(configPane().getByText('System')).toBeInTheDocument();
    expect(configPane().getByText('piyush-llm')).toBeInTheDocument();
    expect(configPane().getByText('hsundar-gemini')).toBeInTheDocument();
  });

  it("Tier and agent slots fold into their parent config's row", () => {
    renderPicker();
    openPicker();

    // piyush-llm owns a base slot plus two tier slots, and is still one row.
    expect(configPane().queryByText(/Retrieval tier/)).not.toBeInTheDocument();
    expect(configPane().queryByText(/Summary tier/)).not.toBeInTheDocument();
  });

  it('A model listed at both the base and a tier slot is one row, not two', () => {
    renderPicker();
    openPicker();

    openConfig('piyush-llm');
    // gemini-3-flash-preview sits on db:182f5d24 AND db:182f5d24:tier:retrieval.
    expect(modelPane().getAllByRole('option')).toHaveLength(2);
    expect(modelPane().getAllByText('gemini-3-flash-preview')).toHaveLength(1);
  });

  // ─── selecting a model ───────────────────────────────────────────────────

  it('Choosing a model + Apply fires onModelSelect AND onTierModelsSelect(null)', () => {
    renderPicker();
    openPicker();

    openConfig('piyush-llm');
    chooseModel('gemini-3-flash-preview');
    fireEvent.click(screen.getByText('Apply'));

    // Per-task picks are always cleared: a model applies to every call, so a
    // leftover tier pick would silently override it.
    expect(onTierModelsSelect).toHaveBeenCalledWith(null);
    expect(onModelSelect).toHaveBeenCalledWith({
      provider: 'googleai',
      model: 'gemini-3-flash-preview',
      configSource: 'db:182f5d24',
      configName: 'piyush-llm',
    });
  });

  it('The same model under two configs pins the config it was picked from', () => {
    renderPicker();
    openPicker();

    // gemini-3-flash-preview exists under System and piyush-llm; the left pane
    // is what disambiguates them.
    openConfig('System');
    chooseModel('gemini-3-flash-preview');
    fireEvent.click(screen.getByText('Apply'));

    expect(onModelSelect).toHaveBeenCalledWith(expect.objectContaining({ configSource: 'env:global', configName: 'System' }));
  });

  it('A model only reachable under a tier pins that tier slot', () => {
    renderPicker();
    openPicker();

    openConfig('piyush-llm');
    chooseModel('gemini-2.5-flash');
    fireEvent.click(screen.getByText('Apply'));

    expect(onModelSelect).toHaveBeenCalledWith(expect.objectContaining({ model: 'gemini-2.5-flash', configSource: 'db:182f5d24:tier:summary' }));
  });

  it('Fallback models are listed as ordinary choices, with no marking', () => {
    renderPicker();
    openPicker();

    openConfig('hsundar-gemini');
    expect(modelPane().queryByText(/fallback/i)).not.toBeInTheDocument();

    chooseModel('gemini-2.5-flash-lite');
    fireEvent.click(screen.getByText('Apply'));

    expect(onModelSelect).toHaveBeenCalledWith(expect.objectContaining({ model: 'gemini-2.5-flash-lite', configSource: 'db:9a1b2c3d:tier:summary' }));
  });

  // ─── by-task mode ────────────────────────────────────────────────────────

  it('By task: each tier is picked from its own config and applied together', () => {
    renderPicker();
    openPicker();

    fireEvent.click(screen.getByText('By task'));

    // Reasoning is the tier the picker opens on.
    openConfig('hsundar-gemini');
    chooseModel('gemini-3.1-pro-preview');

    switchTier('Summary');
    openConfig('piyush-llm');
    chooseModel('gemini-2.5-flash');

    fireEvent.click(screen.getByText('Apply'));

    // The two modes are mutually exclusive, so the blanket model is cleared.
    expect(onModelSelect).toHaveBeenCalledWith(null);
    const picks = onTierModelsSelect.mock.calls[0][0];
    expect(picks.reasoning).toMatchObject({ model: 'gemini-3.1-pro-preview', configSource: 'db:9a1b2c3d', configName: 'hsundar-gemini' });
    expect(picks.summary).toMatchObject({ model: 'gemini-2.5-flash', configSource: 'db:182f5d24:tier:summary', configName: 'piyush-llm' });
    expect(picks.retrieval).toBeUndefined();
  });

  it('By task: two configs sharing an API key stay separately pickable', () => {
    renderPicker();
    openPicker();
    fireEvent.click(screen.getByText('By task'));

    // Both serve gemini-3-flash-preview / gemini-3.1-pro-preview through one
    // folded credential; the config axis is what keeps them distinguishable.
    openConfig('piyush-llm');
    chooseModel('gemini-3-flash-preview');
    switchTier('Retrieval');
    openConfig('hsundar-gemini');
    chooseModel('gemini-3.1-pro-preview');

    fireEvent.click(screen.getByText('Apply'));

    const picks = onTierModelsSelect.mock.calls[0][0];
    expect(picks.reasoning.configSource).toBe('db:182f5d24');
    expect(picks.retrieval.configSource).toBe('db:9a1b2c3d');
  });

  it('By task: a tier can be cleared back to the default', () => {
    renderPicker();
    openPicker();
    fireEvent.click(screen.getByText('By task'));

    openConfig('piyush-llm');
    chooseModel('gemini-2.5-flash');
    fireEvent.click(screen.getByLabelText('Clear Reasoning'));

    fireEvent.click(screen.getByText('Apply'));

    // Every tier cleared applies as null, not an empty map.
    expect(onTierModelsSelect).toHaveBeenCalledWith(null);
  });

  it('Reopening lands in the mode the conversation is already in', () => {
    renderPicker({
      selectedTierModels: {
        reasoning: { provider: 'googleai', model: 'gemini-3.1-pro-preview', configSource: 'db:9a1b2c3d', configName: 'hsundar-gemini' },
      },
    });
    openPicker();

    // …on the By-task tier pane, opened at the config that pick came from.
    expect(screen.getByRole('group', { name: 'Active task' })).toBeInTheDocument();
    const config = configPane()
      .getAllByRole('option')
      .find((o) => o.textContent?.includes('hsundar-gemini'));
    expect(config).toHaveAttribute('aria-selected', 'true');
  });

  // ─── config mode ─────────────────────────────────────────────────────────

  it('Config: selecting a config applies the whole config, not a model', () => {
    renderPicker();
    openPicker();
    fireEvent.click(screen.getByText('Config'));

    openConfig('piyush-llm');
    fireEvent.click(screen.getByText('Apply'));

    // ':all' is what tells the server to let the config's own tiers choose.
    expect(onConfigSelect).toHaveBeenCalledWith({ configSource: 'db:182f5d24:all', configName: 'piyush-llm' });
    // All three modes are mutually exclusive.
    expect(onModelSelect).toHaveBeenCalledWith(null);
    expect(onTierModelsSelect).toHaveBeenCalledWith(null);
  });

  it('Config: the right pane shows what the config will actually do per task', () => {
    renderPicker();
    openPicker();
    fireEvent.click(screen.getByText('Config'));

    openConfig('piyush-llm');
    const summary = within(screen.getByTestId('config-summary-pane'));
    expect(summary.getByText('Models this config uses per task')).toBeInTheDocument();
    // piyush-llm defines retrieval + summary tiers.
    expect(summary.getByText('Retrieval')).toBeInTheDocument();
    expect(summary.getByText('Summary')).toBeInTheDocument();
    // …and the model every untagged call falls back to.
    expect(summary.getByText('Everything else')).toBeInTheDocument();
  });

  it('Config: an agent slot is not mistaken for the base model', () => {
    // db:<uuid>:agent:<name> contains no ':tier:', so a "not a tier" test would
    // report the agent's model as what every untagged call falls back to.
    renderPicker({
      models: [
        { model: 'base-model', provider: 'googleai', configSource: 'db:mix', configName: 'mixed' },
        { model: 'agent-only-model', provider: 'googleai', configSource: 'db:mix:agent:memory_compose', configName: 'mixed · Agent: memory_compose' },
      ],
    });
    openPicker();
    fireEvent.click(screen.getByText('Config'));

    const summary = within(screen.getByTestId('config-summary-pane'));
    expect(summary.getByText('base-model')).toBeInTheDocument();
    expect(summary.queryByText('agent-only-model')).not.toBeInTheDocument();
  });

  it('Config: a config with no per-task models says so', () => {
    renderPicker({
      models: [{ model: 'gemini-3-flash-preview', provider: 'googleai', configSource: 'db:flat', configName: 'flat-config' }],
    });
    openPicker();
    fireEvent.click(screen.getByText('Config'));

    const summary = within(screen.getByTestId('config-summary-pane'));
    expect(summary.getByText('This config sets no per-task models')).toBeInTheDocument();
    expect(summary.getByText('All calls')).toBeInTheDocument();
  });

  it('Config: the System config selects as env:all', () => {
    renderPicker();
    openPicker();
    fireEvent.click(screen.getByText('Config'));

    openConfig('System');
    fireEvent.click(screen.getByText('Apply'));

    expect(onConfigSelect).toHaveBeenCalledWith({ configSource: 'env:all', configName: 'System' });
  });

  it('Config: Apply is blocked until a config is picked', () => {
    renderPicker();
    openPicker();
    fireEvent.click(screen.getByText('Config'));

    // Opening does not stage the config the pane happens to land on — Apply
    // would then commit a config the user never clicked.
    expect(screen.getByText('Apply').closest('button')).toBeDisabled();
  });

  it('Config: reopening lands back in Config mode on the pinned config', () => {
    renderPicker({ selectedConfig: { configSource: 'db:9a1b2c3d:all', configName: 'hsundar-gemini' } });
    openPicker();

    expect(screen.getByTestId('config-summary-pane')).toBeInTheDocument();
    const row = configPane()
      .getAllByRole('option')
      .find((o) => o.textContent?.includes('hsundar-gemini'));
    expect(row).toHaveAttribute('aria-selected', 'true');
  });

  it('Config: a restored pin is labelled with the config name, not a generic word', () => {
    // A conversation stores only the source id, so the trigger has to resolve
    // the name from the loaded configs — otherwise every restored whole-config
    // conversation reads the same.
    renderPicker({ selectedConfig: { configSource: 'db:182f5d24:all' } });

    expect(screen.getByTestId('model-picker-trigger')).toHaveTextContent('piyush-llm');
  });

  it('Clear all fires every callback with null', () => {
    renderPicker({ selectedModel: { provider: 'googleai', model: 'gemini-2.5-pro', configSource: 'env:tier:summary' } });
    openPicker();

    fireEvent.click(screen.getByText('Clear all'));

    expect(onModelSelect).toHaveBeenCalledWith(null);
    expect(onTierModelsSelect).toHaveBeenCalledWith(null);
    expect(onConfigSelect).toHaveBeenCalledWith(null);
  });

  it('Apply stays disabled while nothing is staged, so it cannot silently clear', () => {
    renderPicker();
    openPicker();

    expect(screen.getByText('Apply').closest('button')).toBeDisabled();
    expect(onModelSelect).not.toHaveBeenCalled();
  });

  // ─── restoring an existing selection ─────────────────────────────────────

  it('Opening lands on the config of the current pick and marks both panes', () => {
    renderPicker({ selectedModel: { provider: 'googleai', model: 'gemini-2.5-flash', configSource: 'db:182f5d24:tier:summary' } });
    openPicker();

    // A tier-slot pin still resolves to its parent config.
    const config = configPane()
      .getAllByRole('option')
      .find((o) => o.textContent?.includes('piyush-llm'));
    expect(config).toHaveAttribute('aria-selected', 'true');

    const row = modelPane()
      .getAllByRole('option')
      .find((o) => o.textContent?.includes('gemini-2.5-flash'));
    expect(row).toHaveAttribute('aria-selected', 'true');
  });

  it('A legacy selection with no pin still highlights its model', () => {
    // Predates pinning entirely: provider/model but no configSource, so it can
    // only be matched by model — against whichever config opens first.
    renderPicker({ selectedModel: { provider: 'googleai', model: 'gemini-3-flash-preview' } });
    openPicker();

    const row = modelPane()
      .getAllByRole('option')
      .find((o) => o.textContent?.includes('gemini-3-flash-preview'));
    expect(row).toHaveAttribute('aria-selected', 'true');
  });

  // ─── search ──────────────────────────────────────────────────────────────

  it('Search narrows to the configs that can serve a model', () => {
    renderPicker();
    openPicker();

    const search = screen.getByPlaceholderText('Search configs and models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'gemini-2.5-flash' } });

    // gemini-2.5-flash is piyush-llm's, gemini-2.5-flash-lite is hsundar-gemini's.
    expect(configPane().getAllByRole('option')).toHaveLength(2);
    expect(configPane().queryByText('System')).not.toBeInTheDocument();
  });

  it('Search also matches the config name, keeping all of its models', () => {
    renderPicker();
    openPicker();

    const search = screen.getByPlaceholderText('Search configs and models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'piyush' } });

    expect(configPane().getAllByRole('option')).toHaveLength(1);
    expect(modelPane().getAllByRole('option')).toHaveLength(2);
  });

  it('Search matches any provider in a config, not just its first slot', () => {
    // A mixed config (Gemini for reasoning, HF for summary) must surface under
    // either provider — the config row itself no longer names one.
    renderPicker({
      models: [
        { model: 'gemini-3.1-pro-preview', provider: 'googleai', configSource: 'db:mixed', configName: 'gemini-qwen-summary' },
        {
          model: 'Qwen/Qwen3.6-35B-A3B-FP8',
          provider: 'huggingface',
          configSource: 'db:mixed:tier:summary',
          configName: 'gemini-qwen-summary · Summary tier',
        },
      ],
    });
    openPicker();

    const search = screen.getByPlaceholderText('Search configs and models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'huggingface' } });

    expect(configPane().getByText('gemini-qwen-summary')).toBeInTheDocument();
  });

  it('A row with no config_name falls back to its source id, not a crash', () => {
    // config_name is omitempty on the wire, so it can arrive undefined even
    // though the type says otherwise. A generic placeholder would be worse than
    // the id: every unnamed config would collapse into one identical row.
    renderPicker({
      models: [
        { model: 'a', provider: 'googleai', configSource: 'db:unnamed' },
        { model: 'b', provider: 'googleai', configSource: 'db:named', configName: 'has-a-name' },
      ],
    });
    openPicker();

    expect(configPane().getByText('db:unnamed')).toBeInTheDocument();
    expect(configPane().getByText('has-a-name')).toBeInTheDocument();
  });

  it('A config is named by the first slot that has a name, whatever the order', () => {
    // Naming from whichever slot arrives first would label the whole config
    // with a raw source id just because that one slot lacked a name.
    renderPicker({
      models: [
        { model: 'a', provider: 'googleai', configSource: 'db:late' },
        { model: 'b', provider: 'googleai', configSource: 'db:late:tier:summary', configName: 'named-later · Summary tier' },
      ],
    });
    openPicker();

    expect(configPane().getByText('named-later')).toBeInTheDocument();
    expect(configPane().queryByText('db:late')).not.toBeInTheDocument();
  });

  it('No matches shows an empty state instead of blank panes', () => {
    renderPicker();
    openPicker();

    const search = screen.getByPlaceholderText('Search configs and models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'no-such-model' } });

    expect(screen.getByText('No models match')).toBeInTheDocument();
    expect(screen.queryByTestId('config-pane')).not.toBeInTheDocument();
    expect(screen.queryByTestId('model-pane')).not.toBeInTheDocument();
  });
});
