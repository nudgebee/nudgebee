import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import { FormCard, FormField, FormBuilder } from '@shared/forms/FormComponents';

jest.mock('@utils/colors');

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ alt }) => React.createElement('img', { alt }),
}));

jest.mock('@components/k8s/common/TextArea', () => ({
  Textarea: ({ value, onChange, placeholder, id }) => (
    <textarea id={id} value={value || ''} onChange={onChange} placeholder={placeholder} data-testid='textarea-field' />
  ),
}));

jest.mock('@ui/Select', () => ({
  Select: ({ value, onChange, options, id, disabled, label: _label }) => (
    <select id={id} value={value || ''} disabled={disabled} onChange={(e) => onChange && onChange(e.target.value)} data-testid='dropdown-field'>
      {(options || []).map((opt, i) => (
        <option key={i} value={opt.value || opt}>
          {opt.label || opt}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@ui/Input', () => ({
  Input: ({ value, onChange, placeholder, type, disabled, id, error }) => (
    <>
      <input
        id={id}
        type={type || 'text'}
        value={value ?? ''}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e) => onChange && onChange(e.target.value)}
      />
      {error && <span role='alert'>{error}</span>}
    </>
  ),
}));

jest.mock('@ui/Checkbox', () => ({
  Checkbox: ({ checked, onChange, disabled, label }) => (
    <label>
      <input type='checkbox' checked={!!checked} disabled={disabled} onChange={(e) => onChange && onChange(e.target.checked)} />
      {label}
    </label>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => React.createElement('img', { alt }),
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

  it('renders dropdown when fieldType is select', () => {
    render(<FormField label='Dropdown' fieldType='select' value='' onChange={jest.fn()} options={[{ value: 'a', label: 'Option A' }]} />);
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
