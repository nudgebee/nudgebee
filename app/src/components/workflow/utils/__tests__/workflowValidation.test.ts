import type { Node } from 'reactflow';
import { OPTIMIZATION_NEEDS_FILTER_MSG, optimizationParamsHaveFilter, validateWorkflowForSave } from '../workflowValidation';

const workflow = { id: null, name: 'wf', definition: {}, tags: {} };
const withTask = () => [{ id: 'task1' }];
const optimizationNode = (params: Record<string, unknown> | undefined): Node =>
  ({ id: 't1', type: 'trigger', position: { x: 0, y: 0 }, data: { trigger: { type: 'optimization', params } } } as Node);

describe('optimizationParamsHaveFilter', () => {
  it.each([
    ['no params', undefined],
    ['empty params', {}],
    ['only empty lists', { categories: [], rule_names: [], clusters: [] }],
    ['empty string items', { categories: [''] }],
    ['blank filter expression', { filter: '{{  }}' }],
  ])('is false for %s', (_, params) => {
    expect(optimizationParamsHaveFilter(params)).toBe(false);
  });

  it.each([
    ['one category', { categories: ['RightSizing'] }],
    ['one rule name', { rule_names: ['rightsize_pod'] }],
    ['one cluster', { clusters: ['acc-1'] }],
    ['a filter expression', { filter: "{{ event.category == 'RightSizing' }}" }],
  ])('is true for %s', (_, params) => {
    expect(optimizationParamsHaveFilter(params)).toBe(true);
  });
});

describe('validateWorkflowForSave optimization trigger', () => {
  it('blocks an optimization trigger with no filter, naming the fields', () => {
    expect(validateWorkflowForSave(workflow, [optimizationNode({})], withTask)).toEqual([OPTIMIZATION_NEEDS_FILTER_MSG]);
    expect(OPTIMIZATION_NEEDS_FILTER_MSG).toContain('Cluster, Category or Rule Name');
  });

  it('accepts an optimization trigger with one category', () => {
    expect(validateWorkflowForSave(workflow, [optimizationNode({ categories: ['RightSizing'] })], withTask)).toEqual([]);
  });
});
