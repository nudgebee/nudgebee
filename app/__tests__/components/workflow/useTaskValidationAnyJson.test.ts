import { validateTaskData } from '@components/workflow/hooks/useTaskValidation';

// data.filter's `list` is declared `any` on the backend, which in practice means
// "a JSON array, or a template that resolves to one". Before this case existed
// the field was skipped entirely by the type switch and malformed JSON only
// surfaced as a Go parser error after Run (issue #34764).
const TASK_DEFINITIONS = [
  {
    name: 'data.filter',
    input_schema: {
      list: { type: 'any', required: true },
      condition: { type: 'string', required: true },
    },
  },
];

const validateList = (list: any) => validateTaskData('data.filter', { list, condition: 'status = "active"' }, TASK_DEFINITIONS);

describe('validateTaskData "any" fields', () => {
  it('rejects the single-quoted JSON from the bug report', () => {
    const res = validateList(`[{"name":'23'},{"name":'24'}]`);
    expect(res.isValid).toBe(false);
    expect(res.errors['list']).toContain('must be valid JSON');
  });

  it('surfaces the parser message so the user can find the character', () => {
    const res = validateList('{not json');
    expect(res.errors['list']).toMatch(/must be valid JSON — .+/);
  });

  it('accepts a well-formed JSON array', () => {
    const res = validateList('[{"name": "checkout", "cpu": 91}]');
    expect(res.isValid).toBe(true);
    expect(res.errors['list']).toBeUndefined();
  });

  it('accepts an already-parsed array', () => {
    const res = validateList([{ name: 'checkout' }]);
    expect(res.isValid).toBe(true);
    expect(res.errors['list']).toBeUndefined();
  });

  it('accepts a template reference, which resolves at execution time', () => {
    const res = validateList("{{ Tasks['previous_task'].output.result }}");
    expect(res.isValid).toBe(true);
    expect(res.errors['list']).toBeUndefined();
  });

  it('still reports the field as required when empty', () => {
    const res = validateList('');
    expect(res.isValid).toBe(false);
    expect(res.errors['list']).toContain('required');
  });
});

// The same rule covers the other two `any` input fields in the task registry,
// both of which also require JSON when handed a string.
describe('validateTaskData "any" fields on other tasks', () => {
  const OTHER_TASKS = [
    { name: 'core.foreach', input_schema: { items: { type: 'any', required: true } } },
    { name: 'llm.a2a', input_schema: { params: { type: 'any', required: false } } },
  ];

  it('flags malformed JSON in core.foreach items', () => {
    const res = validateTaskData('core.foreach', { items: "['a', 'b']" }, OTHER_TASKS);
    expect(res.isValid).toBe(false);
    expect(res.errors['items']).toContain('must be valid JSON');
  });

  it('leaves an optional, empty a2a params alone', () => {
    const res = validateTaskData('llm.a2a', { params: '' }, OTHER_TASKS);
    expect(res.isValid).toBe(true);
    expect(res.errors['params']).toBeUndefined();
  });
});
