import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KnowledgePolicySettings from '../KnowledgePolicySettings';
import { getKnowledgePolicy, saveKnowledgePolicy } from '@api1/knowledge-base/policy';
jest.mock('@api1/knowledge-base/policy', () => ({ getKnowledgePolicy: jest.fn(), saveKnowledgePolicy: jest.fn() }));
it('uses the real picker to select model-directed mode and exposes its limitation', async () => {
  (getKnowledgePolicy as jest.Mock).mockResolvedValue('auto');
  (saveKnowledgePolicy as jest.Mock).mockResolvedValue(undefined);
  render(<KnowledgePolicySettings accountId='a' canEdit />);
  await waitFor(() => expect(screen.getByTestId('knowledge-retrieval-trigger')).toHaveTextContent('Auto'));
  fireEvent.click(screen.getByTestId('knowledge-retrieval-trigger'));
  fireEvent.click(screen.getByText('Model-directed only'));
  expect(screen.getByText(/Some custom agents require automatic discovery/)).toBeInTheDocument();
  fireEvent.click(screen.getByTestId('save-knowledge-policy'));
  await screen.findByText('Retrieval mode saved. It applies to new requests.');
  expect(saveKnowledgePolicy).toHaveBeenCalledWith('a', 'llm_only');
});
