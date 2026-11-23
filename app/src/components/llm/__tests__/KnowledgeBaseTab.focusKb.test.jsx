import { render, waitFor } from '@testing-library/react';
import KnowledgeBaseTab from '@components/llm/KnowledgeBaseTab';
import apiKnowledgeBase from '@api1/knowledge-base';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';
import { toast } from '@ui/Toast';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  KB_AGENT_WILDCARD: '*',
  default: {
    getKnowledgeBases: jest.fn(),
    listSkillSources: jest.fn(),
    getKBLoadHistory: jest.fn(),
  },
}));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listAgents: jest.fn(), listTools: jest.fn() } }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { getK8sWorkloadNames: jest.fn(), getK8sNamespaceNames: jest.fn() } }));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn(), isTenantAdmin: jest.fn() }));
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn(), success: jest.fn(), info: jest.fn() } }));
jest.mock('@components/llm/ScopeChip', () => ({ __esModule: true, default: () => null, HybridScopeChips: () => null }));
jest.mock('@components/llm/KnowledgePolicySettings', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/KnowledgeBaseFormModal', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/KnowledgeBaseSharingModal', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/SkillSourcesSection', () => ({
  __esModule: true,
  default: () => null,
  SkillDetailsModal: () => null,
  shortCommit: (sha) => sha,
}));
const mockDocumentsModal = jest.fn();
jest.mock('@components/llm/KBDocumentsModal', () => ({
  __esModule: true,
  default: (props) => {
    mockDocumentsModal(props);
    return null;
  },
}));

const runbook = {
  id: 'kb-1',
  name: 'Order DB Slow Query Runbook',
  kb_type: 'manual',
  status: 'active',
  enabled: true,
  account_id: '',
  visibility: 'organisation',
};
const ok = (data) => ({ data, errors: [] });
const lastDocuments = () => mockDocumentsModal.mock.calls.at(-1)?.[0];

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockReturnValue(false);
  isTenantAdmin.mockReturnValue(false);
  apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok([runbook]));
  apiKnowledgeBase.listSkillSources.mockResolvedValue(ok([]));
  apiAskNudgebee.listAgents.mockResolvedValue({ data: { data: { ai_list_agents: { data: [] } } } });
  apiAskNudgebee.listTools.mockResolvedValue({ data: { data: { ai_list_tools: { data: [] } } } });
});

// A fix step's runbook link names one knowledge base; the tab opens its documents once the list has loaded.
describe('KnowledgeBaseTab opened on one knowledge base', () => {
  it("opens the linked knowledge base's documents, read through the account in view", async () => {
    render(<KnowledgeBaseTab accountId='acct-1' focusKbId='kb-1' />);
    await waitFor(() => expect(lastDocuments()?.open).toBe(true));
    expect(lastDocuments()).toEqual(expect.objectContaining({ kbId: 'kb-1', kbName: 'Order DB Slow Query Runbook', accountId: 'acct-1' }));
  });

  it('says so when the linked knowledge base is not in the list', async () => {
    render(<KnowledgeBaseTab accountId='acct-1' focusKbId='kb-gone' />);
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('This knowledge base is not available here'));
    expect(lastDocuments()?.open).toBe(false);
  });

  it('adds nothing to a failed list', async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue({ data: null, errors: [{ message: 'down' }] });
    render(<KnowledgeBaseTab accountId='acct-1' focusKbId='kb-1' />);
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to fetch knowledge bases'));
    expect(toast.error).not.toHaveBeenCalledWith('This knowledge base is not available here');
  });

  it('opens nothing without a link', async () => {
    render(<KnowledgeBaseTab accountId='acct-1' />);
    await waitFor(() => expect(apiKnowledgeBase.getKnowledgeBases).toHaveBeenCalled());
    expect(lastDocuments()?.open).toBe(false);
    expect(toast.error).not.toHaveBeenCalled();
  });
});
