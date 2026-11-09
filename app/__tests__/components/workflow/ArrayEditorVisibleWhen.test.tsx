import React from 'react';
import { render, screen } from '@testing-library/react';
import { ArrayEditor } from '@components/workflow/components/WorkflowFieldComponents';

// Store Event evidence items: fields show only for the picked type.
const itemSchema = {
  type: { type: 'string', required: true, options: ['markdown', 'json', 'table'], order: 2 },
  content: { type: 'string', sub_type: 'textarea', description: 'Markdown body', order: 9, visible_when: { field: 'type', value: ['markdown'] } },
  headers: { type: 'array', description: 'Column names', order: 7, visible_when: { field: 'type', value: ['table'] } },
};

describe('ArrayEditor visible_when', () => {
  it('shows only the fields of the picked type', () => {
    render(<ArrayEditor value={[{ type: 'table' }]} onChange={jest.fn()} itemSchema={itemSchema} />);
    expect(screen.getByText('Column names')).toBeInTheDocument();
    expect(screen.queryByText('Markdown body')).not.toBeInTheDocument();
  });
});
