import { buildTaskTree, flattenWithOrphans } from '@components/llm/common/TasksDrawerContent';

// A drawer row (agent or tool call). `id` doubles as `tool_id` (taskKeyOf reads tool_id first),
// so a child's `parentId` points at a parent row's `id`.
const call = (id, parentId, createdAt) => ({ id, tool_id: id, parentId: parentId ?? null, created_at: createdAt });

// agent1 → [tool1, sub1 → [tool2]] — a tool call and a sub-agent under one agent, and a nested
// tool call under the sub-agent, exercising depth 0/1/2.
const nestedRun = () => [call('agent1', null, '1'), call('tool1', 'agent1', '2'), call('sub1', 'agent1', '3'), call('tool2', 'sub1', '4')];

describe('buildTaskTree', () => {
  it('nests tool calls and sub-agents under their parent by parentId', () => {
    const { roots, childrenOf } = buildTaskTree(nestedRun());
    expect(roots.map((n) => n.key)).toEqual(['task:agent1']);
    expect(
      childrenOf
        .get('task:agent1')
        .map((n) => n.key)
        .sort()
    ).toEqual(['task:sub1', 'task:tool1']);
    expect(childrenOf.get('task:sub1').map((n) => n.key)).toEqual(['task:tool2']);
  });

  it('treats a row with no parentId — or one that does not resolve — as a root', () => {
    const { roots } = buildTaskTree([call('a', null, '1'), call('b', 'missing', '2')]);
    expect(roots.map((n) => n.key).sort()).toEqual(['task:a', 'task:b']);
  });

  it('does not let a self-referential parentId make a row its own child', () => {
    const { roots, childrenOf } = buildTaskTree([call('a', 'a', '1')]);
    expect(roots.map((n) => n.key)).toEqual(['task:a']);
    expect(childrenOf.get('task:a')).toBeUndefined();
  });
});

describe('flattenWithOrphans', () => {
  it('emits pre-order rows with depth growing by nesting level (created_at within a level)', () => {
    const tasks = nestedRun();
    const rows = flattenWithOrphans(tasks, buildTaskTree(tasks));
    expect(rows.map((r) => [r.node.key, r.depth])).toEqual([
      ['task:agent1', 0],
      ['task:tool1', 1],
      ['task:sub1', 1],
      ['task:tool2', 2],
    ]);
  });

  it('re-homes rows orphaned by a broken (cyclic) parent chain instead of dropping them', () => {
    const tasks = [call('a', 'b', '1'), call('b', 'a', '2')];
    const rows = flattenWithOrphans(tasks, buildTaskTree(tasks));
    expect(rows.map((r) => r.node.key).sort()).toEqual(['task:a', 'task:b']);
    rows.forEach((r) => expect(r.depth).toBe(0));
  });
});
