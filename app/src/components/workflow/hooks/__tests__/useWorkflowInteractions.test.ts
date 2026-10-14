import { act, renderHook } from '@testing-library/react';
import { useWorkflowInteractions } from '../useWorkflowInteractions';

jest.mock('@components/workflow/nodes', () => ({ nodeTypes: {} }));
jest.mock('@components/workflow/components/DeletableEdge', () => () => null);
jest.mock('@components/workflow/components/ConditionalEdge', () => () => null);

// #40526: after Publish the trigger panel re-seeded from a pre-save selectedNode, showing (and
// re-saving) the previous config.
describe('useWorkflowInteractions.updateTriggerConfig', () => {
  const trigger = (filter: string) => ({
    id: 't1',
    type: 'trigger',
    position: { x: 0, y: 0 },
    data: { trigger: { type: 'event', params: { filter } } },
  });

  it('updates the selected trigger node along with the canvas nodes', () => {
    const { result } = renderHook(() => useWorkflowInteractions({} as any, {}));
    act(() => result.current.setSelectedNode(trigger('old') as any));

    let nodes: any[] = [trigger('old')];
    const setNodes = (update: any) => {
      nodes = update(nodes);
    };
    act(() => result.current.updateTriggerConfig('t1', { type: 'event', params: { filter: 'new' } }, setNodes as any));

    expect(nodes[0].data.trigger.params).toEqual({ filter: 'new' });
    expect(result.current.selectedNode?.data.trigger.params).toEqual({ filter: 'new' });
  });

  it('leaves a different selected node alone', () => {
    const { result } = renderHook(() => useWorkflowInteractions({} as any, {}));
    const other = { ...trigger('keep'), id: 'other' };
    act(() => result.current.setSelectedNode(other as any));
    act(() => result.current.updateTriggerConfig('t1', { type: 'event', params: { filter: 'new' } }, (() => {}) as any));
    expect(result.current.selectedNode).toBe(other);
  });
});
