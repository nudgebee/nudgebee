import type { Node, Edge } from 'reactflow';
import { reconcileTriggerEdges } from '../triggerEdges';
import { convertWorkflowToReactFlow } from '../workflowLayoutEngine';
import { extractTasksFromWorkflowNodes } from '../workflowTaskExtraction';

const trigger = (id: string): Node => ({ id, type: 'trigger', position: { x: 0, y: 0 }, data: { trigger: { type: 'manual', params: {} } } });
const task = (id: string, taskConfig: Record<string, unknown> = {}): Node => ({
  id,
  type: 'action',
  position: { x: 0, y: 0 },
  data: { taskConfig: { id, type: 'core.print', config: {}, ...taskConfig } },
});
const edge = (id: string, source: string, target: string, extra: Partial<Edge> = {}): Edge => ({ id, source, target, ...extra });
const pairs = (edges: Edge[]): string[] => edges.map((e) => `${e.source}->${e.target}`).sort();

// The definition from issue #39835: `second` depends on `first`.
const issueDefinition = () => ({
  version: 'v1',
  tasks: [
    { id: 'first', type: 'core.print', params: { message: 'first' } },
    { id: 'second', type: 'core.print', depends_on: ['first'], params: { message: 'second ran' } },
  ],
  triggers: [{ type: 'manual', params: {} }],
});

describe('reconcileTriggerEdges', () => {
  it('leaves a freshly loaded canvas untouched (same array back)', () => {
    const { nodes, edges } = convertWorkflowToReactFlow(issueDefinition());
    expect(reconcileTriggerEdges(nodes, edges)).toBe(edges);
  });

  it('attaches a step to the trigger when its only incoming edge is deleted', () => {
    const { nodes, edges } = convertWorkflowToReactFlow(issueDefinition());
    const triggerId = nodes.find((n) => n.type === 'trigger')!.id;
    const afterDelete = edges.filter((e) => !(e.source === 'first' && e.target === 'second'));

    // The saved definition already treats `second` as a root task...
    const saved = extractTasksFromWorkflowNodes(nodes, afterDelete).find((t) => t.id === 'second');
    expect(saved?.depends_on).toBeUndefined();

    // ...so the canvas has to draw it attached to the trigger.
    const result = reconcileTriggerEdges(nodes, afterDelete);
    expect(pairs(result)).toEqual([`${triggerId}->first`, `${triggerId}->second`]);
    // What a reload of the saved definition would draw.
    const reloaded = convertWorkflowToReactFlow({ ...issueDefinition(), tasks: extractTasksFromWorkflowNodes(nodes, afterDelete) });
    expect(pairs(result)).toEqual(pairs(reloaded.edges));
  });

  it('attaches the steps left behind when the step above them is removed', () => {
    const nodes = [trigger('t'), task('a'), task('b')];
    const result = reconcileTriggerEdges(nodes, []);
    expect(pairs(result)).toEqual(['t->a', 't->b']);
  });

  it('attaches a root step to every trigger', () => {
    const nodes = [trigger('t1'), trigger('t2'), task('a')];
    const result = reconcileTriggerEdges(nodes, [edge('edge_t1_a_1', 't1', 'a')]);
    expect(pairs(result)).toEqual(['t1->a', 't2->a']);
  });

  it('drops the trigger edge once the step gets a predecessor', () => {
    const nodes = [trigger('t'), task('a'), task('b')];
    const attached = reconcileTriggerEdges(nodes, [edge('trigger-edge-t-a', 't', 'a')]);
    expect(pairs(attached)).toEqual(['t->a', 't->b']);

    const connected = reconcileTriggerEdges(nodes, [...attached, edge('edge_a_b_1', 'a', 'b')]);
    expect(pairs(connected)).toEqual(['a->b', 't->a']);
  });

  it('treats a switch-case edge as a predecessor', () => {
    const sw: Node = { id: 'sw', type: 'switch', position: { x: 0, y: 0 }, data: { taskConfig: { id: 'sw', type: 'core.switch', config: {} } } };
    const nodes = [trigger('t'), sw, task('a')];
    const edges = [edge('trigger-edge-t-sw', 't', 'sw'), edge('switch-edge-sw-x-a', 'sw', 'a', { sourceHandle: 'switch-case-x' })];
    expect(reconcileTriggerEdges(nodes, edges)).toBe(edges);
  });

  it('keeps a trigger edge the user drew into a step that starts with the trigger', () => {
    const nodes = [trigger('t'), task('a')];
    const edges = [edge('edge_t_a_1', 't', 'a')];
    expect(reconcileTriggerEdges(nodes, edges)).toBe(edges);
  });

  it('drops a trigger edge into a step that waits for another step, however it was drawn', () => {
    // From the trigger's "+" button, or restored when a disabled step is enabled again.
    const nodes = [trigger('t'), task('a'), task('b')];
    const edges = [edge('edge_t_a_1', 't', 'a'), edge('reactflow-edge-t-b-1', 't', 'b'), edge('edge_a_b_2', 'a', 'b')];
    expect(pairs(reconcileTriggerEdges(nodes, edges))).toEqual(['a->b', 't->a']);
  });

  it('attaches a loaded step whose depends_on names a missing step, as saving it would make it a root', () => {
    const definition = {
      version: 'v1',
      tasks: [{ id: 'orphan', type: 'core.print', depends_on: ['ghost'], params: {} }],
      triggers: [{ type: 'manual', params: {} }],
    };
    const { nodes, edges } = convertWorkflowToReactFlow(definition);
    const triggerId = nodes.find((n) => n.type === 'trigger')!.id;
    expect(extractTasksFromWorkflowNodes(nodes, edges)[0].depends_on).toBeUndefined();
    expect(pairs(reconcileTriggerEdges(nodes, edges))).toEqual([`${triggerId}->orphan`]);
  });

  it('keeps disabled steps detached', () => {
    const nodes = [trigger('t'), task('a'), task('off', { disabled: true })];
    const result = reconcileTriggerEdges(nodes, [edge('trigger-edge-t-a', 't', 'a'), edge('trigger-edge-t-off', 't', 'off')]);
    expect(pairs(result)).toEqual(['t->a']);
  });

  it('does nothing when the automation has no trigger', () => {
    const edges: Edge[] = [];
    expect(reconcileTriggerEdges([task('a')], edges)).toBe(edges);
  });

  it('is stable: a second pass returns the same array', () => {
    const nodes = [trigger('t'), task('a'), task('b')];
    const once = reconcileTriggerEdges(nodes, []);
    expect(reconcileTriggerEdges(nodes, once)).toBe(once);
  });

  it('does not reuse an edge id that a renamed step still holds', () => {
    // `a` was renamed to `renamed`; its trigger edge kept the old id. A new step then takes the id `a`.
    const nodes = [trigger('t'), task('renamed'), task('a')];
    const result = reconcileTriggerEdges(nodes, [edge('trigger-edge-t-a', 't', 'renamed')]);
    expect(pairs(result)).toEqual(['t->a', 't->renamed']);
    expect(new Set(result.map((e) => e.id)).size).toBe(result.length);
  });
});
