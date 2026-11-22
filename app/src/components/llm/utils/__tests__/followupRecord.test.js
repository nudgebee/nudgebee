import { describeFollowup, isFollowupTask, summarizeFollowups } from '../followupRecord';

// Shapes mirror real dev rows (llm_conversation_messages, message_type = 'followup')
// as mapped by useLLMInvestigationControl.
const followup = ({ config, response, status, asked = '2026-10-01T08:05:00Z', updated = '2026-10-01T08:05:40Z' }) => ({
  type: 'followup-question',
  tool: 'followup-question',
  text: config.question,
  asked_at: asked,
  last_updated_at: updated,
  response: { type: 'followup-response', text: response, status, message_config: JSON.stringify(config) },
});

const approval = (response, status = 'COMPLETED') =>
  followup({
    config: {
      followupType: 'tool_confirmation',
      toolName: 'kubectl_execute',
      confirmationKey: 'kubectl_execute',
      followupOptions: ['yes', 'no'],
      question:
        'Tool(kubectl_execute) is trying to update cluster resources. Do you want to continue?\nCommand - {"command":"kubectl delete pod web-1 -n prod"}',
    },
    response,
    status,
  });

describe('describeFollowup', () => {
  it('reports an approved write with its bare command and answer time', () => {
    const r = describeFollowup(approval('yes'));
    expect(r).toMatchObject({
      isApproval: true,
      toolName: 'kubectl_execute',
      command: 'kubectl delete pod web-1 -n prod',
      question: 'Tool(kubectl_execute) is trying to update cluster resources. Do you want to continue?',
      outcome: 'approved',
      detail: '',
      askedAt: '2026-10-01T08:05:00Z',
      endedAt: '2026-10-01T08:05:40Z',
    });
  });

  it.each(['ok', 'YES', ' true '])('treats %p as approval, like the server', (answer) => {
    expect(describeFollowup(approval(answer)).outcome).toBe('approved');
  });

  it('reports a plain "no" as denied without repeating it', () => {
    expect(describeFollowup(approval('no'))).toMatchObject({ outcome: 'denied', detail: '' });
  });

  it('keeps the instruction that came with a rejection', () => {
    const r = describeFollowup(approval('no - do not restart anything, just summarize'));
    expect(r).toMatchObject({ outcome: 'denied', detail: 'no - do not restart anything, just summarize' });
  });

  it('reports the integration picked for a tool_config question', () => {
    const r = describeFollowup(
      followup({
        config: {
          followupType: 'tool_config',
          toolName: 'postgres_query_execute',
          question: 'Kubernetes cluster k8s-prod has several integrations for postgres_query_execute. Which one should be used?',
        },
        response: 'prod',
        status: 'COMPLETED',
      })
    );
    expect(r).toMatchObject({ isApproval: false, outcome: 'answered', detail: 'prod', command: '' });
    expect(r.question).toContain('Which one should be used?');
  });

  it('joins a multi_select answer', () => {
    const r = describeFollowup(
      followup({ config: { followupType: 'multi_select', question: 'Which namespaces?' }, response: '["a","b"]', status: 'COMPLETED' })
    );
    expect(r.detail).toBe('a, b');
  });

  it('reports a dismissed followup with its reason', () => {
    const r = describeFollowup(approval('{"resolution":"dismissed","reason":"smoke cleanup"}', 'TERMINATED'));
    expect(r).toMatchObject({ outcome: 'dismissed', detail: 'smoke cleanup', endedAt: '2026-10-01T08:05:40Z' });
  });

  it('reports a followup ended with the conversation', () => {
    const r = describeFollowup(approval('Conversation terminated by user', 'TERMINATED'));
    expect(r).toMatchObject({ outcome: 'dismissed', detail: 'Conversation terminated by user' });
  });

  it('reports a followup still WAITING in a finished turn as unanswered, with no answer time', () => {
    const r = describeFollowup(approval('', 'WAITING'));
    expect(r).toMatchObject({ outcome: 'unanswered', detail: '', endedAt: null, askedAt: '2026-10-01T08:05:00Z' });
  });

  it('keeps an approval question that has no command line', () => {
    const r = describeFollowup(
      followup({
        config: { followupType: 'tool_confirmation', toolName: 'github_execute', question: 'Merge pull request #12 into main?' },
        response: 'yes',
        status: 'COMPLETED',
      })
    );
    expect(r).toMatchObject({ command: '', question: 'Merge pull request #12 into main?', outcome: 'approved' });
  });

  it('does not throw on a non-string answer or question', () => {
    const task = approval('yes');
    task.response.text = { a: 1 };
    task.response.message_config = { followupType: 'text', question: { a: 1 } };
    expect(describeFollowup(task)).toMatchObject({ outcome: 'answered', question: task.text, detail: '' });
  });

  it('accepts an already-parsed message_config', () => {
    const task = approval('yes');
    task.response.message_config = JSON.parse(task.response.message_config);
    expect(describeFollowup(task).outcome).toBe('approved');
  });
});

describe('isFollowupTask', () => {
  it('matches followup tasks only', () => {
    expect(isFollowupTask({ type: 'followup-question' })).toBe(true);
    expect(isFollowupTask({ tool: 'kubectl_execute', type: 'tool_call' })).toBe(false);
  });
});

describe('summarizeFollowups', () => {
  const pick = followup({ config: { followupType: 'tool_config', question: 'Which integration?' }, response: 'prod', status: 'COMPLETED' });

  it('counts approvals and clarifications separately', () => {
    expect(summarizeFollowups([approval('yes'), approval('ok'), pick])).toEqual({
      approvals: { count: 2, needsAttention: false },
      clarifications: { count: 1, needsAttention: false },
    });
  });

  it.each([
    ['denied', approval('no')],
    ['dismissed', approval('{"resolution":"dismissed"}', 'TERMINATED')],
    ['unanswered', approval('', 'WAITING')],
  ])('flags only the approvals when one was %s', (_, task) => {
    const summary = summarizeFollowups([pick, task]);
    expect(summary.approvals.needsAttention).toBe(true);
    expect(summary.clarifications.needsAttention).toBe(false);
  });

  it('is empty for no followups', () => {
    expect(summarizeFollowups([])).toEqual({ approvals: { count: 0, needsAttention: false }, clarifications: { count: 0, needsAttention: false } });
  });
});

describe('auto-approved tool calls', () => {
  const { collectAutoApprovals, describeFollowup, summarizeFollowups } = require('../followupRecord');
  const agents = [
    {
      llm_conversation_tool_calls: [
        {
          id: 't1',
          tool_name: 'kubectl_execute',
          parameters: '{"command":"kubectl rollout restart deployment approval-probe -n e2e"}',
          created_at: '2026-10-13T14:01:00Z',
          metadata: JSON.stringify({ nb_approved_by_rule: { rule_name: 'kubectl_rollout_restart', rule_revision: 2 } }),
        },
        { id: 't2', tool_name: 'kubectl_execute', parameters: '{"command":"kubectl get pods"}', metadata: '{}' },
      ],
    },
  ];

  it('collects only calls a rule approved', () => {
    const entries = collectAutoApprovals(agents);
    expect(entries).toHaveLength(1);
    expect(entries[0].autoApproval).toEqual({ ruleName: 'kubectl_rollout_restart', ruleRevision: 2 });
  });

  it('describes them as approvals that need no attention', () => {
    const [entry] = collectAutoApprovals(agents);
    const record = describeFollowup(entry);
    expect(record).toMatchObject({
      isApproval: true,
      outcome: 'auto_approved',
      command: 'kubectl rollout restart deployment approval-probe -n e2e',
      detail: 'Rule: kubectl_rollout_restart (revision 2)',
    });
    expect(summarizeFollowups([entry]).approvals).toEqual({ count: 1, needsAttention: false });
  });
});
