import crypto from 'crypto';
import React from 'react';
import { render, screen } from '@testing-library/react';

const KEY = 'test-signing-key';
const TOKEN = 'abcd:acct-1:temporal-1:run-1:ask';
const APPROVER = 'dana@example.com';

process.env.APPROVAL_SIGNING_KEY = KEY;
process.env.WORKFLOW_SERVER_URL = 'http://workflow-server:8000';
// eslint-disable-next-line @typescript-eslint/no-var-requires
const page = require('@pages/automation/approvals/[token]');

function sign(status: string): string {
  return `v0=${crypto.createHmac('sha256', KEY).update(`${TOKEN}|${status}|${APPROVER}`).digest('hex')}`;
}

function context(method: string, status = 'approve', sig = sign(status)) {
  return {
    req: { method },
    res: { setHeader: jest.fn() },
    params: { token: TOKEN },
    query: { status, approver: APPROVER, sig },
  } as never;
}

const fetchMock = jest.fn();

beforeEach(() => {
  fetchMock.mockReset();
  global.fetch = fetchMock as never;
});

describe('an approval link', () => {
  it.each(['GET', 'HEAD'])('only asks for confirmation when opened (%s)', async (method) => {
    const result = await page.getServerSideProps(context(method));
    expect(result).toEqual({ props: { outcome: 'confirm', status: 'approve' } });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('records the decision when the confirmation is posted', async () => {
    fetchMock.mockResolvedValue({ status: 200 });
    const result = await page.getServerSideProps(context('POST'));
    expect(result).toEqual({ props: { outcome: 'recorded', status: 'approve' } });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(`http://workflow-server:8000/approvals/${encodeURIComponent(TOKEN)}`);
    expect(JSON.parse(init.body)).toEqual({
      status: 'approve',
      approver: APPROVER,
      sig: sign('approve'),
      result: { approver: `email:${APPROVER}` },
    });
  });

  it('gives up on a workflow server that does not answer', async () => {
    fetchMock.mockRejectedValue(new DOMException('The operation was aborted due to timeout', 'TimeoutError'));
    const result = await page.getServerSideProps(context('POST'));
    expect(result.props.outcome).toBe('error');
    expect(fetchMock.mock.calls[0][1].signal).toBeInstanceOf(AbortSignal);
  });

  it.each(['GET', 'POST'])('with a bad signature records nothing (%s)', async (method) => {
    const result = await page.getServerSideProps(context(method, 'approve', sign('reject')));
    expect(result.props.outcome).toBe('invalid_link');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('shows the choice the way the email labels it', () => {
    render(<page.default outcome='confirm' status='needs-info' />);
    expect(screen.getByTestId('workflow-approval-heading')).toHaveTextContent('Confirm your decision');
    expect(screen.getByTestId('workflow-approval-body')).toHaveTextContent(
      'You chose Needs info in the approval email. Nothing is recorded until you confirm.'
    );
    const confirm = screen.getByTestId('workflow-approval-confirm-btn');
    expect(confirm).toHaveTextContent('Confirm Needs info');
    expect(confirm).toHaveAttribute('type', 'submit');
    expect(confirm.closest('form')).toHaveAttribute('method', 'post');
  });

  it('shows no confirm button once a decision is recorded', () => {
    render(<page.default outcome='recorded' status='approve' />);
    expect(screen.queryByTestId('workflow-approval-confirm-btn')).not.toBeInTheDocument();
  });
});
