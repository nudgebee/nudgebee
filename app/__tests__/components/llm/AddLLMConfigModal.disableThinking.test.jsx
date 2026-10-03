import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import AddLLMConfigModal from '@components/llm/AddLLMConfigModal';

jest.mock('@api1/user', () => ({ __esModule: true, default: { getAccounts: jest.fn().mockResolvedValue([]) } }));
jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    addIntegration: jest.fn(),
    updateIntegration: jest.fn(),
    getIntegrationTypes: jest.fn().mockResolvedValue([]),
  },
}));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listAgents: jest.fn().mockResolvedValue([]) } }));

const NAME = /turn off model reasoning/i;

const editData = (provider, extra = {}) => ({
  id: 'cfg-1',
  name: 'test-config',
  integration_config_values: {
    llm_provider: provider,
    llm_model_name: 'Qwen/Qwen3.6-35B-A3B-FP8',
    ...extra,
  },
});

const openAdvanced = async () => {
  const toggle = await screen.findByRole('button', { name: /advanced options/i });
  await userEvent.click(toggle);
};

describe('AddLLMConfigModal — turn off model reasoning', () => {
  // The switch only reaches the model through the OpenAI-compatible client, so
  // it must not appear for providers whose keys llm-server would never read.
  it('offers the switch for custom endpoints', async () => {
    render(<AddLLMConfigModal open onClose={() => {}} editData={editData('custom')} accountId='acct-1' />);
    await openAdvanced();
    await waitFor(() => expect(screen.getByRole('checkbox', { name: NAME })).toBeInTheDocument());
  });

  it.each(['googleai', 'anthropic', 'openai', 'bedrock', 'sagemaker'])('hides the switch for %s', async (provider) => {
    render(<AddLLMConfigModal open onClose={() => {}} editData={editData(provider)} accountId='acct-1' />);
    await openAdvanced();
    expect(screen.queryByRole('checkbox', { name: NAME })).not.toBeInTheDocument();
  });

  // A saved flag has to be visible without hunting: Advanced auto-expands.
  it('reflects a saved value and expands Advanced to show it', async () => {
    render(<AddLLMConfigModal open onClose={() => {}} accountId='acct-1' editData={editData('custom', { llm_disable_thinking: 'true' })} />);
    await waitFor(() => expect(screen.getByRole('checkbox', { name: NAME })).toBeChecked());
  });

  it('leaves the switch off when the key is absent', async () => {
    render(<AddLLMConfigModal open onClose={() => {}} editData={editData('custom')} accountId='acct-1' />);
    await openAdvanced();
    await waitFor(() => expect(screen.getByRole('checkbox', { name: NAME })).not.toBeChecked());
  });
});
