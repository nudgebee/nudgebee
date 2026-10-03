import React, { useState } from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { JsonEditor } from '@components/workflow/components/WorkflowFieldComponents';
import { getExamplePlaceholder, parseJsonExample } from '@components/workflow/utils/fieldTypeUtils';
import SubTaskParamForm from '@components/workflow/components/subtasks/SubTaskParamForm';

// Harness mirroring how ActionDetailsSidebar drives the editor: the value lives
// in parent state, and something other than the editor (a click-to-fill example
// chip) can write to it.
const Harness: React.FC<{ initial?: any; fill: any }> = ({ initial, fill }) => {
  const [value, setValue] = useState<any>(initial);
  return (
    <div>
      <button type='button' onClick={() => setValue(parseJsonExample(fill))}>
        fill
      </button>
      <JsonEditor value={value} onChange={setValue} />
    </div>
  );
};

const editorText = (): string =>
  Array.from(document.querySelectorAll('.cm-line'))
    .map((line) => line.textContent ?? '')
    .join('\n');

describe('JsonEditor external value sync', () => {
  it('adopts a value written by something other than the editor', () => {
    render(<Harness fill='["alpha","beta"]' />);
    expect(editorText()).not.toContain('alpha');

    fireEvent.click(screen.getByText('fill'));
    expect(editorText()).toContain('alpha');
    expect(editorText()).toContain('beta');
  });

  it('leaves a template reference as a string rather than quoting it', () => {
    render(<Harness fill="{{ Tasks['previous_task'].output.result }}" />);
    fireEvent.click(screen.getByText('fill'));
    expect(editorText()).toContain("{{ Tasks['previous_task'].output.result }}");
  });

  it('starts empty rather than pre-filling an object into an array field', () => {
    render(<Harness fill='[]' />);
    expect(editorText().trim()).toBe('');
  });
});

describe('parseJsonExample', () => {
  it('parses JSON strings so the field holds the same shape as hand-typed input', () => {
    expect(parseJsonExample('[{"name": "checkout"}]')).toEqual([{ name: 'checkout' }]);
  });

  it('leaves non-JSON strings alone', () => {
    expect(parseJsonExample('status = "active"')).toBe('status = "active"');
    expect(parseJsonExample("{{ Tasks['x'].output.result }}")).toBe("{{ Tasks['x'].output.result }}");
  });

  it('passes non-strings straight through', () => {
    expect(parseJsonExample([1, 2])).toEqual([1, 2]);
    expect(parseJsonExample(undefined)).toBeUndefined();
  });
});

describe('getExamplePlaceholder', () => {
  it('prefers an explicit FIELD_PLACEHOLDERS entry', () => {
    expect(getExamplePlaceholder('script', { type: 'string' })).toContain('#!/bin/bash');
  });

  it('falls back to the first string example so the empty box is self-documenting', () => {
    const schema = { type: 'any', examples: [{ label: 'two items', value: '["alpha", "beta"]' }] };
    expect(getExamplePlaceholder('list', schema)).toBe('["alpha", "beta"]');
  });

  it('returns empty when the field declares no examples', () => {
    expect(getExamplePlaceholder('list', { type: 'any' })).toBe('');
  });

  it('ignores a non-string example value rather than stringifying it', () => {
    const schema = { type: 'any', examples: [{ label: 'parsed', value: [1, 2] }] };
    expect(getExamplePlaceholder('list', schema)).toBe('');
  });
});

// SubTaskParamForm routes both `json` and `nested_schema` to the same JsonEditor,
// so an example clicked on either must reach form state parsed, not as a string.
describe('SubTaskParamForm example values for JSON-shaped fields', () => {
  const definitionFor = (extra: Record<string, any>) => ({
    name: 'core.print',
    input_schema: {
      payload: {
        type: 'object',
        required: true,
        title: 'Payload',
        examples: [{ label: 'a payload', value: '{"a": 1}' }],
        ...extra,
      },
    },
  });

  const renderForm = (extra: Record<string, any>, onChange: jest.Mock) =>
    render(
      <SubTaskParamForm
        taskDefinition={definitionFor(extra)}
        values={{}}
        errors={{}}
        onChange={onChange}
        previousTasks={[]}
        workflowInputs={[]}
        workflowConfigs={[]}
      />
    );

  it('parses the example for a plain json field', () => {
    const onChange = jest.fn();
    renderForm({}, onChange);
    fireEvent.click(screen.getByTestId('field-example-payload-0'));
    expect(onChange).toHaveBeenCalledWith('payload', { a: 1 });
  });

  // A declared sub-schema resolves to `nested_schema`, which renders the same
  // JsonEditor — it was the branch the first cut of this missed.
  it('parses the example for a nested_schema field', () => {
    const onChange = jest.fn();
    renderForm({ schema: { properties: { a: { type: 'number' } } } }, onChange);
    fireEvent.click(screen.getByTestId('field-example-payload-0'));
    expect(onChange).toHaveBeenCalledWith('payload', { a: 1 });
  });
});
