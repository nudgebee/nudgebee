import { getUpstreamSuggestedValues } from '@components/workflow/utils/getUpstreamSuggestedValues';
import type { Node } from 'reactflow';

describe('getUpstreamSuggestedValues', () => {
  const approvalNode: Node = {
    id: 'node-1',
    type: 'action',
    position: { x: 0, y: 0 },
    data: {
      label: 'My Approval',
      taskConfig: {
        id: 'approval-1',
        type: 'core.approval',
        config: {
          approval_options: ['Yes', 'No', 'Needs Info'],
        },
      },
    },
  };

  const defaultApprovalNode: Node = {
    id: 'node-2',
    type: 'action',
    position: { x: 0, y: 0 },
    data: {
      label: 'Default Approval',
      taskConfig: {
        id: 'approval-default',
        type: 'core.approval',
        config: {},
      },
    },
  };

  const printNode: Node = {
    id: 'node-3',
    type: 'action',
    position: { x: 0, y: 0 },
    data: {
      label: 'Print Task',
      taskConfig: {
        id: 'print-1',
        type: 'core.print',
        config: { message: 'hello' },
      },
    },
  };

  const nodes = [approvalNode, defaultApprovalNode, printNode];

  it('returns null for empty or non-string expressions', () => {
    expect(getUpstreamSuggestedValues('', nodes)).toBeNull();
    expect(getUpstreamSuggestedValues('   ', nodes)).toBeNull();
  });

  it('returns null when no task reference is found', () => {
    expect(getUpstreamSuggestedValues('{{ Inputs.env == "prod" }}', nodes)).toBeNull();
  });

  it('extracts approval options for single-quoted task references', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['approval-1'].output.status }}", nodes);
    expect(res).toEqual({
      isComplexExpression: false,
      taskId: 'approval-1',
      taskName: 'My Approval',
      taskType: 'core.approval',
      outputField: 'status',
      values: ['Yes', 'No', 'Needs Info'],
    });
  });

  it('extracts approval options for double-quoted task references', () => {
    const res = getUpstreamSuggestedValues('{{ Tasks["approval-1"].output.status }}', nodes);
    expect(res).toEqual({
      isComplexExpression: false,
      taskId: 'approval-1',
      taskName: 'My Approval',
      taskType: 'core.approval',
      outputField: 'status',
      values: ['Yes', 'No', 'Needs Info'],
    });
  });

  it('falls back to default approve/reject when approval_options is not configured', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['approval-default'].output.status }}", nodes);
    expect(res?.values).toEqual(['approve', 'reject']);
  });

  it('flags expression as complex when multiple distinct task references exist', () => {
    const expr = "{{ Tasks['approval-1'].output.status }} - {{ Tasks['approval-default'].output.status }}";
    const res = getUpstreamSuggestedValues(expr, nodes);
    expect(res).toEqual({ isComplexExpression: true });
  });

  it('returns null when referencing a task without known finite outputs', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['print-1'].output.message }}", nodes);
    expect(res).toBeNull();
  });

  it('transforms suggestions to lower case when | lower filter is used', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['approval-1'].output.status | lower }}", nodes);
    expect(res?.values).toEqual(['yes', 'no', 'needs info']);
  });

  it('transforms suggestions to upper case when | upper filter is used', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['approval-1'].output.status | upper }}", nodes);
    expect(res?.values).toEqual(['YES', 'NO', 'NEEDS INFO']);
  });

  it('flags expression as complex when task ref has literal prefix text', () => {
    const res = getUpstreamSuggestedValues("prefix-{{ Tasks['approval-1'].output.status }}", nodes);
    expect(res).toEqual({ isComplexExpression: true });
  });

  it('flags expression as complex when task ref has literal suffix text', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['approval-1'].output.status }}-suffix", nodes);
    expect(res).toEqual({ isComplexExpression: true });
  });

  it('flags expression as complex when single task ref is mixed with other namespaces', () => {
    const res = getUpstreamSuggestedValues("{{ Tasks['approval-1'].output.status }}-{{ Inputs.env }}", nodes);
    expect(res).toEqual({ isComplexExpression: true });
  });
});
