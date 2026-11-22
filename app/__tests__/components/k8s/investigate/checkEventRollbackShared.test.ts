jest.mock('@lib/HttpService', () => ({ __esModule: true, queryGraphQL: jest.fn() }));

import { queryGraphQL } from '@lib/HttpService';
import apiRecommendations, { ROLLBACK_CHECK_SHARED_MS } from '@api1/recommendation';

const answer = (allowed: boolean, reason = '') => ({ data: { data: { events_check_rollback: { allowed, reason } } } });
const ask = queryGraphQL as jest.Mock;

// The Remediation row, each history row and the dialog opened right after ask about the same
// change, and the server reads every later change on the workload to answer.
describe('checkEventRollback', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers();
  });
  afterEach(() => jest.useRealTimers());

  it('asks once when the same change is asked about together', async () => {
    ask.mockResolvedValue(answer(false, 'event resolution: changed again'));
    const [row, dialog] = await Promise.all([
      apiRecommendations.checkEventRollback('acc', 'event-shared', 'c1'),
      apiRecommendations.checkEventRollback('acc', 'event-shared', 'c1'),
    ]);
    expect(row).toEqual({ allowed: false, reason: 'event resolution: changed again' });
    expect(dialog).toEqual(row);
    expect(ask).toHaveBeenCalledTimes(1);

    // A different change is a different question.
    await apiRecommendations.checkEventRollback('acc', 'event-shared', 'c2');
    expect(ask).toHaveBeenCalledTimes(2);
  });

  // The whole point of the check is a change that landed after the page loaded.
  it('asks again once the answer is older than the sharing window', async () => {
    ask.mockResolvedValue(answer(true));
    await apiRecommendations.checkEventRollback('acc', 'event-aging', 'c1');
    jest.advanceTimersByTime(ROLLBACK_CHECK_SHARED_MS - 1);
    await apiRecommendations.checkEventRollback('acc', 'event-aging', 'c1');
    expect(ask).toHaveBeenCalledTimes(1);

    jest.advanceTimersByTime(2);
    ask.mockResolvedValue(answer(false, 'event resolution: changed again'));
    expect(await apiRecommendations.checkEventRollback('acc', 'event-aging', 'c1')).toEqual({
      allowed: false,
      reason: 'event resolution: changed again',
    });
    expect(ask).toHaveBeenCalledTimes(2);
  });

  it('does not share a question that went unanswered', async () => {
    ask.mockRejectedValueOnce(new Error('network'));
    expect(await apiRecommendations.checkEventRollback('acc', 'event-unanswered', 'c1')).toBeNull();
    ask.mockResolvedValue({ data: { errors: [{ message: 'invalid action name - events_check_rollback' }] } });
    expect(await apiRecommendations.checkEventRollback('acc', 'event-unanswered', 'c1')).toBeNull();
    ask.mockResolvedValue(answer(true));
    expect(await apiRecommendations.checkEventRollback('acc', 'event-unanswered', 'c1')).toEqual({ allowed: true, reason: '' });
    expect(ask).toHaveBeenCalledTimes(3);
  });

  // After a rollback the change it rolled back is no longer rollable; the answer from before must
  // not be shown for the rest of the sharing window.
  it('forgets the event’s answers once something is applied to it', async () => {
    ask.mockResolvedValue(answer(true));
    await apiRecommendations.checkEventRollback('acc', 'event-applied', 'c1');
    ask.mockResolvedValue({ data: { data: { events_update_resolution: { data: [] } } } });
    await apiRecommendations.applyRecommendation(
      'acc',
      'event-applied',
      { revert: true, card_id: 'LastDeploymentCard_0' },
      'kubernetes',
      {},
      'event'
    );
    ask.mockResolvedValue(answer(false, 'event resolution: changed again'));
    expect(await apiRecommendations.checkEventRollback('acc', 'event-applied', 'c1')).toEqual({
      allowed: false,
      reason: 'event resolution: changed again',
    });
  });

  // The row names the event's own change by its id, the dialog by leaving the id out.
  it('treats the event’s own id and no id as the same change', async () => {
    ask.mockResolvedValue(answer(true));
    await apiRecommendations.checkEventRollback('acc', 'event-own', 'event-own');
    await apiRecommendations.checkEventRollback('acc', 'event-own', '');
    await apiRecommendations.checkEventRollback('acc', 'event-own');
    expect(ask).toHaveBeenCalledTimes(1);
    expect(ask.mock.calls[0][2]).toEqual({ account_id: 'acc', event_id: 'event-own', change_event_id: '' });
  });
});
