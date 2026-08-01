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

// The shape ai_list_models returns: unique credentials, each carrying the models
// reachable through it. All collapsing happened server-side.
const CREDENTIALS = [
  {
    id: 'googleai|eba3a87f||',
    name: 'System · Default',
    provider: 'googleai',
    configSource: 'env:global',
    sources: ['env:global', 'env:tier:reasoning', 'env:tier:summary'],
    models: [{ model: 'gemini-3-flash-preview' }, { model: 'gemini-2.5-pro' }, { model: 'gemini-3.1-pro-preview' }],
  },
  {
    id: 'googleai|0cc04dd9||',
    name: 'piyush-llm',
    provider: 'googleai',
    configSource: 'db:182f5d24',
    sources: ['db:182f5d24', 'db:182f5d24:tier:summary'],
    models: [{ model: 'gemini-3-flash-preview' }, { model: 'gemini-2.5-flash' }],
  },
];

describe('ModelPickerPopover', () => {
  let onModelSelect: jest.Mock;
  let onTierModelsSelect: jest.Mock;

  beforeEach(() => {
    onModelSelect = jest.fn();
    onTierModelsSelect = jest.fn();
  });

  function openPicker() {
    fireEvent.click(screen.getByTestId('model-picker-trigger'));
  }
  /** Left pane — models, deduplicated across credentials. Clicking navigates,
   *  and also stages the model when only one credential can serve it. */
  const modelPane = () => within(screen.getByTestId('model-pane'));
  /** Right pane — credentials serving the active model. Clicking selects. */
  const credentialPane = () => within(screen.getByTestId('credential-pane'));

  const focusModel = (name: string) => fireEvent.click(modelPane().getByText(name));
  const chooseCredential = (name: string) => fireEvent.click(credentialPane().getByText(name));

  // ─── mutual exclusivity of blanket vs tier mode ──────────────────────────

  it('All-calls mode: choosing a model + Apply fires onModelSelect AND onTierModelsSelect(null)', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    focusModel('gemini-3.1-pro-preview');
    chooseCredential('System · Default');
    fireEvent.click(screen.getByText('Apply'));

    expect(onTierModelsSelect).toHaveBeenCalledWith(null);
    // The credential's configSource rides along as the pin.
    expect(onModelSelect).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: 'googleai',
        model: 'gemini-3.1-pro-preview',
        configSource: 'env:global',
        configName: 'System · Default',
      })
    );
  });

  it('By-task mode: picking per-tier + Apply fires onTierModelsSelect with picks AND onModelSelect(null)', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    fireEvent.click(screen.getByText('By task'));

    // Default active tier is Reasoning.
    focusModel('gemini-3.1-pro-preview');
    chooseCredential('System · Default');

    // Switch active tier to Retrieval. "Retrieval" also appears in the summary
    // row below, so scope by the tier-toggle group.
    const tierToggleGroup = screen.getByRole('group', { name: 'Active task' });
    fireEvent.click(within(tierToggleGroup).getByText('Retrieval'));

    focusModel('gemini-2.5-flash');
    chooseCredential('piyush-llm');

    fireEvent.click(screen.getByText('Apply'));

    expect(onModelSelect).toHaveBeenCalledWith(null);
    expect(onTierModelsSelect).toHaveBeenCalledTimes(1);
    const picks = onTierModelsSelect.mock.calls[0][0];
    expect(picks.reasoning).toMatchObject({ model: 'gemini-3.1-pro-preview', configSource: 'env:global' });
    expect(picks.retrieval).toMatchObject({ model: 'gemini-2.5-flash', configSource: 'db:182f5d24' });
  });

  it('Clear all fires both callbacks with null', () => {
    render(
      <ModelPickerPopover
        credentials={CREDENTIALS}
        selectedModel={{ provider: 'googleai', model: 'gemini-3.1-pro-preview', configSource: 'env:global' }}
        onModelSelect={onModelSelect}
        onTierModelsSelect={onTierModelsSelect}
      />
    );
    openPicker();

    fireEvent.click(screen.getByText('Clear all'));

    expect(onModelSelect).toHaveBeenCalledWith(null);
    expect(onTierModelsSelect).toHaveBeenCalledWith(null);
  });

  // ─── two-pane structure: credentials left, their models right ────────────

  it('Models are deduplicated on the left; the right pane lists what can serve one', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    // Four unique models across the two credentials, not five rows.
    expect(modelPane().getAllByRole('option')).toHaveLength(4);

    // gemini-3-flash-preview is first and reachable through both credentials.
    expect(modelPane().getByText(/2 configs/)).toBeInTheDocument();
    expect(credentialPane().getAllByRole('option')).toHaveLength(2);
  });

  it('The same model under two credentials resolves to whichever is active', () => {
    // gemini-3-flash-preview is reachable through both. The active credential
    // is what makes the pick unambiguous.
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    focusModel('gemini-3-flash-preview');
    chooseCredential('piyush-llm');
    fireEvent.click(screen.getByText('Apply'));

    expect(onModelSelect).toHaveBeenCalledWith(expect.objectContaining({ model: 'gemini-3-flash-preview', configSource: 'db:182f5d24' }));
  });

  it('The model holding the current pick is marked, and opening lands on it', () => {
    render(
      <ModelPickerPopover
        credentials={CREDENTIALS}
        selectedModel={{ provider: 'googleai', model: 'gemini-2.5-flash', configSource: 'db:182f5d24' }}
        onModelSelect={onModelSelect}
        onTierModelsSelect={onTierModelsSelect}
      />
    );
    openPicker();

    const row = modelPane()
      .getAllByRole('option')
      .find((r) => r.textContent?.includes('gemini-2.5-flash'));
    expect(row).toHaveAttribute('aria-selected', 'true');
    // …and piyush-llm, the only credential serving it, is the selected one.
    expect(credentialPane().getByRole('option')).toHaveAttribute('aria-selected', 'true');
  });

  it('A pin naming a folded slot still resolves to its credential', () => {
    // Conversations pinned before slots were collapsed carry e.g.
    // 'db:182f5d24:tier:summary', which is no longer any credential's
    // configSource — only an entry in sources[].
    render(
      <ModelPickerPopover
        credentials={CREDENTIALS}
        selectedModel={{ provider: 'googleai', model: 'gemini-2.5-flash', configSource: 'db:182f5d24:tier:summary' }}
        onModelSelect={onModelSelect}
        onTierModelsSelect={onTierModelsSelect}
      />
    );
    openPicker();

    expect(credentialPane().getByRole('option')).toHaveAttribute('aria-selected', 'true');
  });

  it('Provider sits with the model; credential rows carry only the name', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    expect(modelPane().getAllByText(/googleai/).length).toBeGreaterThan(0);
    expect(credentialPane().queryAllByText('googleai')).toHaveLength(0);
  });

  it('A legacy selection with no pin still highlights its model', () => {
    // Predates pinning entirely: provider/model but no configSource.
    render(
      <ModelPickerPopover
        credentials={CREDENTIALS}
        selectedModel={{ provider: 'googleai', model: 'gemini-3-flash-preview' }}
        onModelSelect={onModelSelect}
        onTierModelsSelect={onTierModelsSelect}
      />
    );
    openPicker();

    const row = modelPane()
      .getAllByRole('option')
      .find((o) => o.textContent?.includes('gemini-3-flash-preview'));
    expect(row).toHaveAttribute('aria-selected', 'true');
  });

  // ─── search ──────────────────────────────────────────────────────────────

  it('Search narrows to the credentials that can serve a model', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    const search = screen.getByPlaceholderText('Search models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'gemini-2.5-flash' } });

    // Only that model is left, and only piyush-llm can serve it.
    expect(modelPane().getAllByRole('option')).toHaveLength(1);
    expect(credentialPane().getAllByRole('option')).toHaveLength(1);
    expect(credentialPane().getByText('piyush-llm')).toBeInTheDocument();
  });

  it('Search also matches the credential name, keeping all its models', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    const search = screen.getByPlaceholderText('Search models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'piyush' } });

    // Matching the credential keeps all of its models.
    expect(modelPane().getAllByRole('option')).toHaveLength(2);
    expect(credentialPane().getAllByRole('option')).toHaveLength(1);
  });

  it('No matches shows an empty state instead of blank panes', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    const search = screen.getByPlaceholderText('Search models…') as HTMLInputElement;
    fireEvent.change(search, { target: { value: 'no-such-model' } });

    expect(screen.getByText('No models match')).toBeInTheDocument();
    expect(screen.queryByTestId('model-pane')).not.toBeInTheDocument();
  });

  // ─── one click is the whole choice when there's nothing to choose ─────────

  it('A model with one config is staged by the left click alone', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    // gemini-2.5-flash is reachable only through piyush-llm. Deliberately no
    // credential-pane click here: that second step is what users were missing,
    // and Apply used to commit null and wipe the selection.
    focusModel('gemini-2.5-flash');
    fireEvent.click(screen.getByText('Apply'));

    expect(onModelSelect).toHaveBeenCalledWith(
      expect.objectContaining({ provider: 'googleai', model: 'gemini-2.5-flash', configSource: 'db:182f5d24', configName: 'piyush-llm' })
    );
  });

  it('A model with several configs prompts for one and blocks Apply until picked', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    // gemini-3-flash-preview is served by both credentials, so the click can't
    // resolve to one and the question has to be put to the user.
    focusModel('gemini-3-flash-preview');
    expect(screen.getByTestId('credential-choice-prompt')).toBeInTheDocument();
    expect(screen.getByText('Apply').closest('button')).toBeDisabled();

    chooseCredential('piyush-llm');
    expect(screen.queryByTestId('credential-choice-prompt')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('Apply'));
    expect(onModelSelect).toHaveBeenCalledWith(expect.objectContaining({ model: 'gemini-3-flash-preview', configSource: 'db:182f5d24' }));
  });

  it('Apply stays disabled while nothing is staged, so it cannot silently clear', () => {
    render(<ModelPickerPopover credentials={CREDENTIALS} onModelSelect={onModelSelect} onTierModelsSelect={onTierModelsSelect} />);
    openPicker();

    expect(screen.getByText('Apply').closest('button')).toBeDisabled();
    expect(onModelSelect).not.toHaveBeenCalled();
  });
});
