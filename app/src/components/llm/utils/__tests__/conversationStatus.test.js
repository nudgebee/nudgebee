import { canStopConversation, isConversationActivelyExecuting } from '../conversationStatus';

describe('conversationStatus', () => {
  it.each(['IN_PROGRESS', 'WAITING_FOR_CLIENT_TOOL'])('treats %s as active execution', (status) => {
    expect(isConversationActivelyExecuting(status)).toBe(true);
  });

  it.each(['WAITING', 'COMPLETED', 'FAILED', 'KILLED', 'TERMINATED', ''])('does not treat %s as active execution', (status) => {
    expect(isConversationActivelyExecuting(status)).toBe(false);
  });

  it('allows stopping a persisted conversation waiting on an adapter tool without rendered messages', () => {
    expect(
      canStopConversation({
        allowStop: true,
        conversationId: 'conversation-1',
        conversationStatus: 'WAITING_FOR_CLIENT_TOOL',
        currentlyProcessingQuestion: null,
      })
    ).toBe(true);
  });

  it('does not expose Stop for generic WAITING workflow or followup states', () => {
    expect(
      canStopConversation({
        allowStop: true,
        conversationId: 'conversation-1',
        conversationStatus: 'WAITING',
        currentlyProcessingQuestion: null,
      })
    ).toBe(false);
  });
});
