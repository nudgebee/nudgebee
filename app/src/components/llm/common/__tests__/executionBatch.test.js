import { executionBatchLabel, executionBatchTooltip, parseExecutionBatchMetadata } from '../executionBatch';
import { buildTaskTree, flattenWithOrphans, withExecutionBatchNodes } from '../TasksDrawerContent';

describe('execution batch metadata', () => {
  it('parses persisted JSON metadata and uses dispatch wording', () => {
    const batch = parseExecutionBatchMetadata(
      JSON.stringify({
        execution_batch_id: 'batch-1',
        execution_mode: 'parallel_dispatch',
        execution_batch_size: 3,
        execution_parallelism_limit: 2,
        planner_iteration: 4,
      })
    );

    expect(executionBatchLabel(batch)).toBe('Iteration 4 · 3 direct tools · In parallel');
    expect(executionBatchTooltip(batch)).toContain('up to 2');
    expect(executionBatchTooltip(batch)).toContain('one at a time');
    expect(executionBatchTooltip(batch)).toContain('Nested rows');
  });

  it('returns null for legacy or malformed metadata', () => {
    expect(parseExecutionBatchMetadata(null)).toBeNull();
    expect(parseExecutionBatchMetadata('{')).toBeNull();
    expect(parseExecutionBatchMetadata('{}')).toBeNull();
  });

  it('does not infer sequential dispatch when mode is absent', () => {
    const batch = parseExecutionBatchMetadata('{"execution_batch_id":"batch-1","execution_batch_size":2}');
    expect(executionBatchLabel(batch)).toBe('2 direct tools · Grouped');
  });

  it('represents a single-tool planner iteration without inventing a dispatch mode', () => {
    const batch = parseExecutionBatchMetadata('{"planner_iteration":3}');
    expect(executionBatchLabel(batch, 1)).toBe('Iteration 3 · 1 direct tool');
  });

  it('normalizes non-string sequential fallback reasons', () => {
    const batch = parseExecutionBatchMetadata({
      execution_batch_id: 'batch-1',
      execution_mode: 'sequential_dispatch',
      sequential_fallback_reason: 429,
    });

    expect(executionBatchTooltip(batch)).toContain('429');
  });
});

describe('withExecutionBatchNodes', () => {
  it('groups matching sibling tool calls under one drawer-only batch node', () => {
    const metadata = JSON.stringify({
      execution_batch_id: 'batch-1',
      execution_mode: 'parallel_dispatch',
      execution_batch_size: 2,
    });
    const tasks = [
      { id: 'agent-1', nodeKind: 'agent', parentId: null },
      { id: 'tool-1', nodeKind: 'tool', parentId: 'agent-1', metadata, created_at: '2026-08-23T01:00:00Z' },
      { id: 'tool-2', nodeKind: 'tool', parentId: 'agent-1', metadata, created_at: '2026-08-23T01:00:01Z' },
    ];

    const normalized = withExecutionBatchNodes(tasks);
    const batchNode = normalized.find((task) => task.nodeKind === 'execution_batch');
    expect(batchNode).toMatchObject({ parentId: 'agent-1', observedBatchSize: 2 });
    expect(normalized.find((task) => task.id === 'tool-1').parentId).toBe(batchNode.id);
    expect(normalized.find((task) => task.id === 'tool-2').parentId).toBe(batchNode.id);
  });

  it('leaves legacy tool calls ungrouped', () => {
    const tasks = [{ id: 'tool-1', nodeKind: 'tool', parentId: 'agent-1', metadata: null }];
    expect(withExecutionBatchNodes(tasks)).toEqual(tasks);
  });

  it('groups a single tool when planner iteration metadata is present', () => {
    const tasks = [
      { id: 'agent-1', nodeKind: 'agent', parentId: null },
      { id: 'tool-1', nodeKind: 'tool', parentId: 'agent-1', metadata: '{"planner_iteration":3}' },
    ];

    const normalized = withExecutionBatchNodes(tasks);
    const iterationNode = normalized.find((task) => task.nodeKind === 'execution_batch');
    expect(iterationNode.executionBatch.plannerIteration).toBe(3);
    expect(normalized.find((task) => task.id === 'tool-1').parentId).toBe(iterationNode.id);
  });

  it('preserves recursive child-agent parentage beneath grouped tool calls', () => {
    const metadata = JSON.stringify({
      execution_batch_id: 'batch-1',
      execution_mode: 'parallel_dispatch',
      execution_batch_size: 2,
    });
    const tasks = [
      { id: 'agent-1', nodeKind: 'agent', parentId: null },
      { id: 'tool-1', nodeKind: 'tool', parentId: 'agent-1', metadata },
      { id: 'child-agent', nodeKind: 'agent', parentId: 'tool-1' },
      { id: 'child-tool', nodeKind: 'tool', parentId: 'child-agent', metadata: '{"planner_iteration":1}' },
      { id: 'grandchild-agent', nodeKind: 'agent', parentId: 'child-tool' },
      { id: 'tool-2', nodeKind: 'tool', parentId: 'agent-1', metadata },
    ];

    const rows = flattenWithOrphans(tasks, buildTaskTree(tasks));
    expect(rows.map(({ node, depth }) => [node.task.id, node.task.nodeKind, depth])).toEqual([
      ['agent-1', 'agent', 0],
      ['execution-batch:agent-1:batch-1', 'execution_batch', 1],
      ['tool-1', 'tool', 2],
      ['child-agent', 'agent', 3],
      ['execution-batch:child-agent:iteration-1', 'execution_batch', 4],
      ['child-tool', 'tool', 5],
      ['grandchild-agent', 'agent', 6],
      ['tool-2', 'tool', 2],
    ]);
  });

  it('groups spawned agent rows carrying planner action metadata', () => {
    const metadata = JSON.stringify({
      execution_batch_id: 'batch-1',
      execution_mode: 'parallel_dispatch',
      execution_batch_size: 2,
      planner_iteration: 1,
    });
    const tasks = [
      { id: 'agent-1', nodeKind: 'agent', parentId: null },
      { id: 'child-1', nodeKind: 'agent', parentId: 'agent-1', metadata, isPlannerAction: true },
      { id: 'child-2', nodeKind: 'agent', parentId: 'agent-1', metadata, isPlannerAction: true },
    ];

    const normalized = withExecutionBatchNodes(tasks);
    const batchNode = normalized.find((task) => task.nodeKind === 'execution_batch');
    expect(batchNode).toMatchObject({ parentId: 'agent-1', observedBatchSize: 2 });
    expect(normalized.find((task) => task.id === 'child-1').parentId).toBe(batchNode.id);
    expect(normalized.find((task) => task.id === 'child-2').parentId).toBe(batchNode.id);
  });

  it('uses a null timestamp when grouped tasks have no creation time', () => {
    const tasks = [{ id: 'tool-1', nodeKind: 'tool', parentId: null, metadata: '{"planner_iteration":1}' }];

    const normalized = withExecutionBatchNodes(tasks);
    const batchNode = normalized.find((task) => task.nodeKind === 'execution_batch');
    expect(batchNode.created_at).toBeNull();
  });

  it('selects the earliest valid batch timestamp across timezone offsets', () => {
    const metadata = '{"execution_batch_id":"batch-1"}';
    const tasks = [
      { id: 'tool-1', nodeKind: 'tool', parentId: null, metadata, created_at: '2026-08-23T01:00:00+02:00' },
      { id: 'tool-2', nodeKind: 'tool', parentId: null, metadata, created_at: '2026-08-22T23:30:00Z' },
      { id: 'tool-3', nodeKind: 'tool', parentId: null, metadata, created_at: 'not-a-date' },
    ];

    const normalized = withExecutionBatchNodes(tasks);
    const batchNode = normalized.find((task) => task.nodeKind === 'execution_batch');
    expect(batchNode.created_at).toBe('2026-08-23T01:00:00+02:00');
  });

  it('does not duplicate synthetic batch nodes when grouped tasks are orphaned', () => {
    const metadata = '{"execution_batch_id":"batch-1","planner_iteration":1}';
    const tasks = [
      { id: 'tool-1', nodeKind: 'tool', parentId: 'missing-agent', metadata },
      { id: 'tool-2', nodeKind: 'tool', parentId: 'missing-agent', metadata },
    ];

    const rows = flattenWithOrphans(tasks, buildTaskTree(tasks));
    expect(rows.map(({ node }) => node.task.id)).toEqual(['tool-1', 'tool-2']);
  });
});
