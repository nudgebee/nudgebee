import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import FieldRenderer from '@components1/common/FieldRenderer';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    tertiary: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
      disabled: '#9CA3AF',
      secondaryDark: '#1F2937',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      tertiaryLightestestest: '#F8FAFC',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }: any) => <img alt={alt} data-testid='safe-icon' {...props} />,
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

  it('renders "N/A" for null field values', () => {
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
    const naElements = screen.getAllByText('N/A');
    expect(naElements.length).toBeGreaterThan(0);
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
