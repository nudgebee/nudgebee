import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import KnowledgePolicySettings from '../KnowledgePolicySettings';
import { getKnowledgePolicy, saveKnowledgePolicy } from '@api1/knowledge-base/policy';
jest.mock('@api1/knowledge-base/policy', () => ({ getKnowledgePolicy: jest.fn(), saveKnowledgePolicy: jest.fn() }));
const read = getKnowledgePolicy as jest.Mock;
const save = saveKnowledgePolicy as jest.Mock;
beforeEach(() => {
  jest.clearAllMocks();
  read.mockResolvedValue('auto');
  save.mockResolvedValue(undefined);
});
async function openMenu() {
  await waitFor(() => expect(screen.getByTestId('knowledge-retrieval-trigger')).not.toHaveTextContent('Loading'));
  fireEvent.click(screen.getByTestId('knowledge-retrieval-trigger'));
}
it('keeps the closed control compact and saves only after confirmation', async () => {
  render(<KnowledgePolicySettings accountId='a' canEdit />);
  expect(screen.queryByText(/Recommended for most/)).not.toBeInTheDocument();
  await openMenu();
  fireEvent.click(screen.getByRole('menuitem', { name: /^Disabled/ }));
  expect(save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByTestId('save-knowledge-policy'));
  await screen.findByText('Retrieval mode saved. It applies to new requests.');
  expect(save).toHaveBeenCalledWith('a', 'disabled');
  expect(screen.getByTestId('knowledge-retrieval-trigger')).toHaveTextContent('Disabled');
});
it('cancels an unsaved selection and resets it when reopened', async () => {
  render(<KnowledgePolicySettings accountId='a' canEdit />);
  await openMenu();
  fireEvent.click(screen.getByRole('menuitem', { name: /^Always discover/ }));
  fireEvent.click(screen.getByTestId('cancel-knowledge-policy'));
  await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
  await openMenu();
  expect(screen.getByTestId('save-knowledge-policy')).toBeDisabled();
  expect(save).not.toHaveBeenCalled();
});
it('keeps read-only accounts read-only', async () => {
  render(<KnowledgePolicySettings accountId='a' canEdit={false} />);
  await openMenu();
  expect(screen.getByRole('menuitem', { name: /^Always discover/ })).toHaveAttribute('aria-disabled', 'true');
  expect(screen.queryByTestId('save-knowledge-policy')).not.toBeInTheDocument();
});
it('reports failed reads without an editable default and supports retry', async () => {
  read.mockRejectedValueOnce(new Error('Read failed'));
  render(<KnowledgePolicySettings accountId='a' canEdit />);
  await openMenu();
  expect(screen.getByText('Read failed')).toBeInTheDocument();
  expect(screen.queryByRole('menuitem')).not.toBeInTheDocument();
  fireEvent.click(screen.getByText('Retry'));
  await screen.findByRole('menuitem', { name: /^Auto/ });
});
it('keeps a failed save editable without claiming success', async () => {
  save.mockRejectedValueOnce(new Error('Save failed'));
  render(<KnowledgePolicySettings accountId='a' canEdit />);
  await openMenu();
  fireEvent.click(screen.getByRole('menuitem', { name: /^Always discover/ }));
  fireEvent.click(screen.getByTestId('save-knowledge-policy'));
  await screen.findByText('Save failed');
  expect(screen.queryByText(/Retrieval mode saved/)).not.toBeInTheDocument();
  expect(screen.getByTestId('save-knowledge-policy')).toBeEnabled();
});
it('ignores stale reads after switching accounts', async () => {
  let resolveA!: (value: string) => void;
  read.mockImplementation((id) =>
    id === 'a'
      ? new Promise<string>((resolve) => {
          resolveA = resolve;
        })
      : Promise.resolve('disabled')
  );
  const { rerender } = render(<KnowledgePolicySettings accountId='a' canEdit />);
  rerender(<KnowledgePolicySettings accountId='b' canEdit />);
  await waitFor(() => expect(screen.getByTestId('knowledge-retrieval-trigger')).toHaveTextContent('Disabled'));
  await act(async () => resolveA('always'));
  expect(screen.getByTestId('knowledge-retrieval-trigger')).toHaveTextContent('Disabled');
});
it('preserves the pending selection when reopened during a save', async () => {
  let finish!: () => void;
  save.mockImplementationOnce(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      })
  );
  render(<KnowledgePolicySettings accountId='a' canEdit />);
  await openMenu();
  fireEvent.click(screen.getByRole('menuitem', { name: /^Disabled/ }));
  fireEvent.click(screen.getByTestId('save-knowledge-policy'));
  fireEvent.click(screen.getByTestId('cancel-knowledge-policy'));
  await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
  await openMenu();
  await act(async () => finish());
  expect(screen.getByTestId('knowledge-retrieval-trigger')).toHaveTextContent('Disabled');
  expect(screen.getByTestId('save-knowledge-policy')).toBeDisabled();
});
