import React from 'react';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({ __esModule: true, default: { applyRecommendation: jest.fn(), checkEventRollback: jest.fn() } }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { resolveEventRecord: jest.fn() } }));
jest.mock('@lib/auth', () => ({ __esModule: true, hasWriteAccess: () => true }));

import apiKubernetes from '@api1/kubernetes';
import apiRecommendations from '@api1/recommendation';
import LastDeploymentCard from '@components/k8s/investigate/cards/LastDeploymentCard';

const IMAGE = 'spec.template.spec.containers[0].image';
const image = (from, to) => ({ path: IMAGE, old: from, new: to });

// An alert whose history holds three image changes: v1 → v2, then v2 → v3, then v3 → v2 again. The
// first one's image is live once more, so rolling it back is safe — and the page, deciding from this
// history alone, used to mark it "Replaced" and drop it, with no way to get it back.
const renderHistory = async () => {
  const evidence = {
    type: 'json',
    additional_info: { action_name: 'deployment_history' },
    data: JSON.stringify({
      namespace: 'shop',
      rollout_name: 'checkout',
      service_name: 'checkout',
      time_range_hours: 168,
      deployments: [
        { event_id: 'put-back', timestamp: '2026-09-17T12:00:00Z', updated_values: [image('v3', 'v2')] },
        { event_id: 'moved-away', timestamp: '2026-09-17T10:00:00Z', updated_values: [image('v2', 'v3')] },
        { event_id: 'first', timestamp: '2026-09-16T10:00:00Z', updated_values: [image('v1', 'v2')] },
      ],
    }),
  };
  const event = { id: 'alert-1', cloud_account_id: 'acc', starts_at: '2026-09-18T00:14:11Z', evidences: [evidence] };
  const card = new LastDeploymentCard(evidence, event, 0);
  await card.canRenderContent();
  const Content = card.getContentComponents()[0];
  render(<Content />);
};

describe('deployment history rows', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    // The server's view: the change that was moved away from is still stood on by the put-back;
    // the first change and the put-back itself can be rolled back.
    apiRecommendations.checkEventRollback.mockImplementation((_account, _event, changeEventId) =>
      Promise.resolve(
        changeEventId === 'moved-away'
          ? { allowed: false, reason: `event resolution: "${IMAGE}" was changed again by a later change (2026-09-17T12:00:00Z)` }
          : { allowed: true, reason: '' }
      )
    );
    apiKubernetes.resolveEventRecord.mockResolvedValue({
      data: {
        events: [
          {
            id: 'first',
            starts_at: '2026-09-16T10:00:00Z',
            evidences: [
              { type: 'diff', data: { resource_name: 'deployment/shop/checkout.yaml', old: 'a', new: 'b', updated_values: [image('v1', 'v2')] } },
            ],
          },
        ],
      },
    });
  });

  it('asks the server about each change and shows its answer on the row', async () => {
    await renderHistory();

    const label = await screen.findByText('Changed again later');
    expect(label).toHaveAttribute('title', expect.stringContaining('was changed again by a later change (2026-09-17T12:00:00Z)'));
    expect(label).not.toHaveAttribute('title', expect.stringContaining('event resolution:'));
    // The other two keep their button; the blocked row has none.
    expect(screen.getAllByRole('button', { name: 'Roll back' })).toHaveLength(2);
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledWith('acc', 'alert-1', 'first');
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledWith('acc', 'alert-1', 'moved-away');
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledWith('acc', 'alert-1', 'put-back');
  });

  // The ticket's last step, on a card with several changes: once the later change is undone, the
  // earlier one is offered again and its rollback can be run.
  it('offers a change again once the change after it has been undone', async () => {
    await renderHistory();
    await screen.findByText('Changed again later');

    const offers = screen.getAllByRole('button', { name: 'Roll back' });
    fireEvent.click(offers[offers.length - 1]);

    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('Now')).toBeInTheDocument();
    expect(within(dialog).queryByTestId('rollback-blocked-reason')).not.toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Roll back' })).toBeEnabled());
    expect(apiKubernetes.resolveEventRecord).toHaveBeenCalledWith('first', 'acc');
  });
});
