import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import DynamicForm from '@shared/forms/DynamicForm';

jest.mock('@utils/colors');

jest.mock('@ui/Input', () => ({
  Input: ({ value, onChange, placeholder, type, disabled, label, onKeyDown, error: _error }) => (
    <input
      aria-label={label || placeholder}
      placeholder={placeholder}
      value={value ?? ''}
      type={type || 'text'}
      disabled={disabled}
      onChange={(e) => onChange && onChange(e.target.value)}
      onKeyDown={onKeyDown}
    />
  ),
}));

jest.mock('@ui/Select', () => ({
  Select: ({ value, onChange, options, disabled, label }) => (
    <select aria-label={label || 'select'} value={value || ''} disabled={disabled} onChange={(e) => onChange && onChange(e.target.value)}>
      <option value=''>Select</option>
      {(options || []).map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
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

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, disabled, icon, 'aria-label': ariaLabel }) => (
    <button onClick={onClick} disabled={disabled} aria-label={ariaLabel}>
      {icon}
      {children}
    </button>
  ),
}));

jest.mock('@ui/Chip', () => ({
  Chip: ({ children, onDismiss }) => <span onClick={onDismiss}>{children}</span>,
}));

jest.mock('@ui/Divider', () => ({
  Divider: () => <hr />,
}));

jest.mock('@components/common/Heading', () => ({
  __esModule: true,
  default: ({ value, children }) => (
    <div>
      {value}
      {children}
    </div>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => React.createElement('img', { alt }),
}));

jest.mock('@components/k8s/common/TextArea', () => ({
  Textarea: ({ value, onChange, placeholder, minRows: _minRows, maxRows: _maxRows }) => (
    <textarea value={value || ''} onChange={onChange} placeholder={placeholder} aria-label={placeholder} />
  ),
}));

jest.mock('@components/events/SigNozQueryAutocomplete', () => ({
  __esModule: true,
  default: () => <div data-testid='signoz-autocomplete' />,
}));

jest.mock('@assets', () => ({
  DeleteIconRed: '/delete.svg',
  PlusIcon: '/plus.svg',
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s) => s.replace(/_/g, ' '),
}));

// ── Helper ───────────────────────────────────────────────────────────────────
const renderDynamicForm = (props = {}) =>
  render(<DynamicForm actionKey='test_action' onChange={jest.fn()} errors={{}} initialValues={{}} actionDetails={{ params: {} }} {...props} />);

// ── Tests ────────────────────────────────────────────────────────────────────
describe('DynamicForm', () => {
  test('renders without crashing with empty fields', () => {
    const { container } = renderDynamicForm();
    expect(container).toBeTruthy();
  });

  test('renders Trigger Conditions textarea', () => {
    renderDynamicForm();
    expect(screen.getByPlaceholderText('Define conditions as Python Template')).toBeInTheDocument();
  });

  test('renders text input (string type) field with correct label', () => {
    renderDynamicForm({
      actionDetails: {
        params: {
          my_field: {
            type: 'string',
            display_name: 'My Field',
            required: true,
          },
        },
      },
    });

    expect(screen.getByText('My Field')).toBeInTheDocument();
  });

  test('renders dropdown field with possible_values options', () => {
    renderDynamicForm({
      actionDetails: {
        params: {
          region: {
            type: 'string',
            display_name: 'Region',
            possible_values: [
              { label: 'US East', value: 'us-east-1' },
              { label: 'US West', value: 'us-west-2' },
            ],
          },
        },
      },
    });

    expect(screen.getByText('Region')).toBeInTheDocument();
    const select = screen.getByRole('combobox');
    expect(select).toBeInTheDocument();
  });

  test('renders bool/checkbox field', () => {
    renderDynamicForm({
      actionDetails: {
        params: {
          enable_feature: {
            type: 'bool',
            display_name: 'Enable Feature',
          },
        },
      },
    });

    expect(screen.getByText('Enable Feature')).toBeInTheDocument();
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  test('calls onChange when string field value changes', async () => {
    const onChange = jest.fn();
    renderDynamicForm({
      actionKey: 'my_action',
      onChange,
      actionDetails: {
        params: {
          url: {
            type: 'string',
            display_name: 'URL',
          },
        },
      },
    });

    const urlInput = screen.getByPlaceholderText('url');
    if (urlInput) {
      fireEvent.change(urlInput, { target: { value: 'https://example.com' } });
      await waitFor(() => {
        expect(onChange).toHaveBeenCalled();
      });
    } else {
      expect(onChange).toBeDefined();
    }
  });

  test('renders Action Parameters section when params provided', () => {
    renderDynamicForm({
      actionDetails: {
        params: {
          timeout: {
            type: 'int',
            display_name: 'Timeout',
          },
        },
      },
    });

    expect(screen.getByText('Action Parameters')).toBeInTheDocument();
  });

  test('renders with initialValues pre-filled in trigger condition', () => {
    renderDynamicForm({
      initialValues: { if: 'some_condition == true' },
      actionDetails: { params: {} },
    });

    const textarea = screen.getByPlaceholderText('Define conditions as Python Template');
    expect(textarea.value).toBe('some_condition == true');
  });

  test('renders number input for int type field', () => {
    renderDynamicForm({
      actionDetails: {
        params: {
          replicas: {
            type: 'int',
            display_name: 'Replicas',
          },
        },
      },
    });

    expect(screen.getByText('Replicas')).toBeInTheDocument();
    const numberInput = screen.getByRole('spinbutton');
    expect(numberInput).toBeInTheDocument();
  });
});
