import { decisionsRecord } from '@api1/memory';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn().mockResolvedValue({ data: {} }),
}));

const sentPayload = () => JSON.stringify((queryGraphQL as jest.Mock).mock.calls[0]);

beforeEach(() => (queryGraphQL as jest.Mock).mockClear());

// The RCA thumbs up/down used to send the user's question as `subject`, which
// is how decisions ended up reading "hi: user thumbs-up on RCA". It now sends
// the message id instead and the server derives the subject from that answer.
describe('decisionsRecord — vote', () => {
  it('sends message_id and account_id, and no question as subject', async () => {
    await decisionsRecord({
      decisionType: 'root_cause_agreed',
      rationale: 'user thumbs-up on RCA',
      messageId: 'msg-123',
      accountId: 'acct-456',
      conversationId: 'conv-789',
    });

    const payload = sentPayload();
    expect(payload).toContain('"message_id":"msg-123"');
    expect(payload).toContain('"account_id":"acct-456"');
    expect(payload).toContain('"conversation_id":"conv-789"');
    // subject must go out empty — the server fills it in from the answer.
    expect(payload).toContain('"subject":""');
  });
});

// AutoPilot approve/decline still names its own subject and sends no message,
// which is what keeps it on the plain "record" path server-side.
describe('decisionsRecord — caller-supplied subject', () => {
  it('sends the subject and leaves message_id empty', async () => {
    await decisionsRecord({
      decisionType: 'action_approved',
      subject: 'rightsize: payments-api',
      rationale: 'approved without review comment',
    });

    const payload = sentPayload();
    expect(payload).toContain('"subject":"rightsize: payments-api"');
    expect(payload).toContain('"message_id":""');
  });
});
