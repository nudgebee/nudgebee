import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import FieldRenderer from '@shared/forms/FieldRenderer';

jest.mock('@utils/colors');

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }: any) => React.createElement('img', { alt, 'data-testid': 'safe-icon', ...props }),
}));

jest.mock('@shared/viewers/JsonTreeView', () => ({
  __esModule: true,
  default: ({ data }: any) => <pre data-testid='json-tree'>{typeof data === 'string' ? data : JSON.stringify(data)}</pre>,
}));

const mockSchema = {
  name: { type: 'string', required: true },
  count: { type: 'number' },
  config: { type: 'object' },
};

const mockData = {
  name: 'test-value',
  count: 42,
  config: { key: 'val' },
};

const mockTaskDefinitions = [
  {
    name: 'task-type-a',
    input_schema: mockSchema,
    output_schema: mockSchema,
  },
];

describe('FieldRenderer', () => {
  it('renders "No schema available" when taskType is not provided', () => {
    render(
      <FieldRenderer
        data={mockData}
        schema={mockSchema}
        taskType={undefined}
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText('No schema available for formatting')).toBeInTheDocument();
  });

  it('renders "No schema available" when data is null', () => {
    render(
      <FieldRenderer
        data={null}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText('No schema available for formatting')).toBeInTheDocument();
  });

  it('renders field values when schema and data are valid', () => {
    render(
      <FieldRenderer
        data={mockData}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText('test-value')).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('renders field label capitalized and underscores replaced with spaces', () => {
    const schemaWithUnderscore = { my_field: { type: 'string' } };
    render(
      <FieldRenderer
        data={{ my_field: 'hello' }}
        schema={schemaWithUnderscore}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText('My field')).toBeInTheDocument();
  });

  it('renders "(required)" tag for required fields', () => {
    render(
      <FieldRenderer
        data={mockData}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText('(required)')).toBeInTheDocument();
  });

  it('calls copyToClipboard when copy icon is clicked', () => {
    const copyToClipboard = jest.fn();
    render(
      <FieldRenderer
        data={mockData}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={copyToClipboard}
      />
    );
    const copyButtons = screen.getAllByRole('button');
    fireEvent.click(copyButtons[0]);
    expect(copyToClipboard).toHaveBeenCalledTimes(1);
  });

  it('renders formatted JSON for object field values', () => {
    render(
      <FieldRenderer
        data={mockData}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText(/key/)).toBeInTheDocument();
  });

  it('renders null field values via JsonTreeView', () => {
    render(
      <FieldRenderer
        data={{ name: null, count: 0, config: null }}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    // null values go through JsonTreeView (typeof null === 'object'), which serialises them as "null"
    const nullElements = screen.getAllByText('null');
    expect(nullElements.length).toBeGreaterThan(0);
  });

  it('renders schema info text showing taskType and fieldType', () => {
    render(
      <FieldRenderer
        data={mockData}
        schema={mockSchema}
        taskType='task-type-a'
        fieldType='input'
        taskDefinitions={mockTaskDefinitions}
        copyToClipboard={jest.fn()}
      />
    );
    expect(screen.getByText(/Formatted according to task-type-a input schema/)).toBeInTheDocument();
  });
});
