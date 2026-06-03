import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import { FormCard, FormField, FormBuilder } from '@components1/common/NewReusabeFormComponents';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    white: '#fff',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      disabled: '#9CA3AF',
      secondaryDark: '#374151',
      tertiarymedium: '#9CA3AF',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      vertical: '#E5E7EB',
      error: '#EF4444',
      primaryLightest: '#BFDBFE',
    },
  },
}));

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ alt }) => <img alt={alt} />,
}));

jest.mock('@components1/k8s/common/TextArea', () => ({
  Textarea: ({ value, onChange, placeholder, id }) => (
    <textarea id={id} value={value || ''} onChange={onChange} placeholder={placeholder} data-testid='textarea-field' />
  ),
}));

jest.mock('@components1/common/CustomDropdown', () => ({
  __esModule: true,
  default: ({ value, onChange, options, id }) => (
    <select id={id} value={value || ''} onChange={onChange} data-testid='dropdown-field'>
      {options?.map((opt, i) => (
        <option key={i} value={opt.value || opt}>
          {opt.label || opt}
        </option>
      ))}
    </select>
  ),
}));

describe('FormCard', () => {
  it('renders children inside the card', () => {
    render(
      <FormCard title='Test Card'>
        <div data-testid='child-content'>Child Content</div>
      </FormCard>
    );
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders title when provided', () => {
    render(
      <FormCard title='My Card Title'>
        <div>child</div>
      </FormCard>
    );
    expect(screen.getByText('My Card Title')).toBeInTheDocument();
  });

  it('renders description when provided', () => {
    render(
      <FormCard title='Card' description='This is a description'>
        <div>child</div>
      </FormCard>
    );
    expect(screen.getByText('This is a description')).toBeInTheDocument();
  });

  it('renders expand/collapse toggle when expand prop is true', () => {
    render(
      <FormCard title='Expandable Card' expand>
        <div data-testid='expandable-child'>Content</div>
      </FormCard>
    );
    // By default collapsed (expand=true means initially collapsed)
    expect(screen.queryByTestId('expandable-child')).not.toBeInTheDocument();
  });

  it('shows children when expand card is toggled open', () => {
    render(
      <FormCard title='Expandable' expand>
        <div data-testid='toggle-child'>Toggled Content</div>
      </FormCard>
    );
    // Click expand button
    const expandBtn = screen.getByRole('button');
    fireEvent.click(expandBtn);
    expect(screen.getByTestId('toggle-child')).toBeInTheDocument();
  });
});

describe('FormField', () => {
  it('renders a text input by default', () => {
    render(<FormField label='My Field' value='test' onChange={jest.fn()} />);
    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('renders label text', () => {
    render(<FormField label='Test Label' value='' onChange={jest.fn()} />);
    expect(screen.getByText('Test Label')).toBeInTheDocument();
  });

  it('renders required star when required is true', () => {
    render(<FormField label='Required Field' required value='' onChange={jest.fn()} />);
    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders error message when error prop is provided', () => {
    render(<FormField label='Error Field' error='This field is required' value='' onChange={jest.fn()} />);
    expect(screen.getByText('This field is required')).toBeInTheDocument();
  });

  it('renders textarea when fieldType is textarea', () => {
    render(<FormField label='Notes' fieldType='textarea' value='' onChange={jest.fn()} />);
    expect(screen.getByTestId('textarea-field')).toBeInTheDocument();
  });

  it('renders dropdown when fieldType is dropdown', () => {
    render(<FormField label='Dropdown' fieldType='dropdown' value='' onChange={jest.fn()} options={[{ value: 'a', label: 'Option A' }]} />);
    expect(screen.getByTestId('dropdown-field')).toBeInTheDocument();
  });

  it('renders checkbox when fieldType is checkbox', () => {
    render(<FormField label='Enable Feature' fieldType='checkbox' value={false} onChange={jest.fn()} />);
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  it('renders custom render content when fieldType is custom', () => {
    render(
      <FormField
        label='Custom'
        fieldType='custom'
        customRender={<div data-testid='custom-content'>Custom Content</div>}
        value=''
        onChange={jest.fn()}
      />
    );
    expect(screen.getByTestId('custom-content')).toBeInTheDocument();
  });
});

describe('FormBuilder', () => {
  it('renders all sections from sections config', () => {
    const sections = [
      {
        title: 'Section 1',
        fields: [{ label: 'Field A', value: '', onChange: jest.fn() }],
      },
      {
        title: 'Section 2',
        fields: [{ label: 'Field B', value: '', onChange: jest.fn() }],
      },
    ];
    render(<FormBuilder sections={sections} />);
    expect(screen.getByText('Section 1')).toBeInTheDocument();
    expect(screen.getByText('Section 2')).toBeInTheDocument();
    expect(screen.getByText('Field A')).toBeInTheDocument();
    expect(screen.getByText('Field B')).toBeInTheDocument();
  });

  it('renders fields with their values', () => {
    const sections = [
      {
        title: 'My Section',
        fields: [{ label: 'Name', value: 'John Doe', onChange: jest.fn() }],
      },
    ];
    render(<FormBuilder sections={sections} />);
    expect(screen.getByDisplayValue('John Doe')).toBeInTheDocument();
  });
});
