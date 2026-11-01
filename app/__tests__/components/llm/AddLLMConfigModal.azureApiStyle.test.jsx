import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
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

const editData = (values) => ({ id: 'cfg-1', name: 'azure-config', integration_config_values: values });

const apiVersionFields = () => screen.queryAllByText(/^API Version/);

describe('AddLLMConfigModal — Azure API style', () => {
  it('hides the API version for the v1 style', async () => {
    render(
      <AddLLMConfigModal
        open
        onClose={() => {}}
        accountId='acct-1'
        editData={editData({ llm_provider: 'azure', llm_model_name: 'gpt-5-mini', llm_provider_api_style: 'v1' })}
      />
    );
    await waitFor(() => expect(screen.getAllByText(/^API Style/).length).toBeGreaterThan(0));
    expect(apiVersionFields()).toHaveLength(0);
    expect(screen.getByText(/enter the deployment name/)).toBeInTheDocument();
  });

  it('shows the API version for the classic style', async () => {
    render(
      <AddLLMConfigModal
        open
        onClose={() => {}}
        accountId='acct-1'
        editData={editData({
          llm_provider: 'azure',
          llm_model_name: 'gpt-5-mini',
          llm_provider_api_style: 'classic',
          llm_provider_api_version: '2024-10-21',
        })}
      />
    );
    await waitFor(() => expect(apiVersionFields().length).toBeGreaterThan(0));
    expect(screen.getByDisplayValue('2024-10-21')).toBeInTheDocument();
  });

  it('lets a tier on Azure pick its own style', async () => {
    render(
      <AddLLMConfigModal
        open
        onClose={() => {}}
        accountId='acct-1'
        editData={editData({
          llm_provider: 'googleai',
          llm_model_name: 'gemini-3.7-flash',
          llm_tier_provider_reasoning: 'azure',
          llm_tier_model_reasoning: 'gpt-4o',
          llm_tier_api_style_reasoning: 'classic',
          llm_tier_api_version_reasoning: '2024-10-21',
        })}
      />
    );
    await waitFor(() => expect(screen.getByDisplayValue('2024-10-21')).toBeInTheDocument());
    expect(screen.getAllByText(/^API Style/).length).toBeGreaterThan(0);
    expect(screen.getByText(/enter the deployment name/)).toBeInTheDocument();
  });
});
