import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import TenantAccountCommonSettings from '@shared/settings/TenantAccountCommonSettings';
import { EMPTY_TRACE_LABEL_SETTINGS, TRACE_LABEL_ADVANCED_FIELDS, TRACE_LABEL_FIELDS } from '@shared/settings/labelMapperFields';

jest.mock('@utils/colors');

// Keeps the `field-<label>` testid the log assertions below already use, and forwards
// the props the trace mapper relies on: `id` (so the idPrefix wiring is observable) and
// `disabled` (so the read-only path is testable). @ui/Button is deliberately left
// unmocked so the disclosure toggle's real aria/testid forwarding is exercised.
jest.mock('@ui/Input', () => ({
  __esModule: true,
  Input: ({ label, value, placeholder, onChange, id, disabled }) => (
    <div>
      <label>{label}</label>
      <input
        data-testid={`field-${label}`}
        id={id}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  ),
}));

describe('TenantAccountCommonSettings', () => {
  const defaultLogSettings = {
    logPodLabel: 'pod',
    logNamespaceLabel: 'namespace',
    logAppLabel: 'app',
  };

  it('renders the Log Label Mapper heading', () => {
    render(<TenantAccountCommonSettings settings={defaultLogSettings} setSettings={jest.fn()} />);
    expect(screen.getByText('Log Label Mapper')).toBeInTheDocument();
  });

  it('renders all three field labels', () => {
    render(<TenantAccountCommonSettings settings={defaultLogSettings} setSettings={jest.fn()} />);
    expect(screen.getByText('Pod')).toBeInTheDocument();
    expect(screen.getByText('Namespace')).toBeInTheDocument();
    expect(screen.getByText('App')).toBeInTheDocument();
  });

  // The mapper maps label names only. "Default query" wrote a key no reader ever consumed
  // (#37402) while the working setting lived on the log integration, so an operator who
  // filled it in believed a filter was in force when it was not. Pinned negatively so it
  // cannot reappear unnoticed.
  it('renders no Default query input', () => {
    render(<TenantAccountCommonSettings settings={defaultLogSettings} setSettings={jest.fn()} />);
    expect(screen.queryByText('Default query')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('Default Query')).not.toBeInTheDocument();
  });

  it('renders field inputs with correct values from settings', () => {
    render(<TenantAccountCommonSettings settings={defaultLogSettings} setSettings={jest.fn()} />);
    expect(screen.getByTestId('field-Pod')).toHaveValue('pod');
    expect(screen.getByTestId('field-Namespace')).toHaveValue('namespace');
    expect(screen.getByTestId('field-App')).toHaveValue('app');
  });

  it('calls setSettings when a field changes', () => {
    const setSettings = jest.fn();
    render(<TenantAccountCommonSettings settings={defaultLogSettings} setSettings={setSettings} />);
    fireEvent.change(screen.getByTestId('field-Pod'), { target: { value: 'new-pod' } });
    expect(setSettings).toHaveBeenCalledTimes(1);
  });

  it('renders with empty settings without crashing', () => {
    render(<TenantAccountCommonSettings settings={{}} setSettings={jest.fn()} />);
    expect(screen.getByText('Log Label Mapper')).toBeInTheDocument();
  });

  it('renders inputs with placeholder text', () => {
    render(<TenantAccountCommonSettings settings={{}} setSettings={jest.fn()} />);
    expect(screen.getByPlaceholderText('Log Pod label')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Log Namespace label')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Log App label')).toBeInTheDocument();
  });

  it('uses empty string as fallback when a settings field is undefined', () => {
    render(<TenantAccountCommonSettings settings={{ logPodLabel: undefined }} setSettings={jest.fn()} />);
    expect(screen.getByTestId('field-Pod')).toHaveValue('');
  });

  it('renders no disclosure when no advancedFields are supplied', () => {
    render(<TenantAccountCommonSettings settings={defaultLogSettings} setSettings={jest.fn()} />);
    expect(screen.queryByTestId('log-label-toggle-advanced')).not.toBeInTheDocument();
  });

  describe('trace mapping', () => {
    const renderTraceMapper = (props = {}) =>
      render(
        <TenantAccountCommonSettings
          title='Trace Label Mapper'
          idPrefix='trace-label'
          fields={TRACE_LABEL_FIELDS}
          advancedFields={TRACE_LABEL_ADVANCED_FIELDS}
          advancedLabel='advanced trace fields'
          settings={{ ...EMPTY_TRACE_LABEL_SETTINGS }}
          setSettings={jest.fn()}
          {...props}
        />
      );

    it('renders the trace title and the five common fields', () => {
      renderTraceMapper();
      expect(screen.getByText('Trace Label Mapper')).toBeInTheDocument();
      TRACE_LABEL_FIELDS.forEach(({ label }) => expect(screen.getByTestId(`field-${label}`)).toBeInTheDocument());
    });

    it('prefixes each input id so both mappers stay independently addressable', () => {
      renderTraceMapper();
      expect(screen.getByTestId('field-Service name')).toHaveAttribute('id', 'trace-label-service_name');
    });

    it('renders no heading when title is null (the account tile supplies its own)', () => {
      renderTraceMapper({ title: null });
      expect(screen.queryByText('Trace Label Mapper')).not.toBeInTheDocument();
      expect(screen.getByTestId('field-Service name')).toBeInTheDocument();
    });

    it('hides the advanced fields behind a collapsed disclosure by default', () => {
      renderTraceMapper();
      TRACE_LABEL_ADVANCED_FIELDS.forEach(({ label }) => expect(screen.queryByTestId(`field-${label}`)).not.toBeInTheDocument());
      const toggle = screen.getByTestId('trace-label-toggle-advanced');
      expect(toggle).toHaveAttribute('aria-expanded', 'false');
      expect(toggle).toHaveTextContent('Show advanced trace fields');
    });

    it('reveals the advanced fields when the disclosure is clicked', () => {
      renderTraceMapper();
      fireEvent.click(screen.getByTestId('trace-label-toggle-advanced'));
      TRACE_LABEL_ADVANCED_FIELDS.forEach(({ label }) => expect(screen.getByTestId(`field-${label}`)).toBeInTheDocument());
      const toggle = screen.getByTestId('trace-label-toggle-advanced');
      expect(toggle).toHaveAttribute('aria-expanded', 'true');
      expect(toggle).toHaveTextContent('Hide advanced trace fields');
    });

    // A stored override in a collapsed field would be invisible: the operator sees an
    // empty-looking mapper while the backend applies a value they cannot see.
    it('auto-expands on mount when an advanced field already has a value', () => {
      renderTraceMapper({ settings: { ...EMPTY_TRACE_LABEL_SETTINGS, trace_id: 'traceID' } });
      expect(screen.getByTestId('field-Trace ID')).toHaveValue('traceID');
    });

    // The case both screens actually hit — they hydrate asynchronously, so the component
    // mounts with empty settings and is re-rendered once the attributes resolve.
    it('auto-expands after an async hydrate', () => {
      const { rerender } = renderTraceMapper();
      expect(screen.queryByTestId('field-Trace ID')).not.toBeInTheDocument();
      rerender(
        <TenantAccountCommonSettings
          title='Trace Label Mapper'
          idPrefix='trace-label'
          fields={TRACE_LABEL_FIELDS}
          advancedFields={TRACE_LABEL_ADVANCED_FIELDS}
          advancedLabel='advanced trace fields'
          settings={{ ...EMPTY_TRACE_LABEL_SETTINGS, resource: 'k8s.resource' }}
          setSettings={jest.fn()}
        />
      );
      expect(screen.getByTestId('field-Resource')).toHaveValue('k8s.resource');
    });

    it('never auto-collapses when the last advanced value is cleared mid-edit', () => {
      const { rerender } = renderTraceMapper({ settings: { ...EMPTY_TRACE_LABEL_SETTINGS, trace_id: 'traceID' } });
      expect(screen.getByTestId('field-Trace ID')).toBeInTheDocument();
      rerender(
        <TenantAccountCommonSettings
          title='Trace Label Mapper'
          idPrefix='trace-label'
          fields={TRACE_LABEL_FIELDS}
          advancedFields={TRACE_LABEL_ADVANCED_FIELDS}
          advancedLabel='advanced trace fields'
          settings={{ ...EMPTY_TRACE_LABEL_SETTINGS }}
          setSettings={jest.fn()}
        />
      );
      expect(screen.getByTestId('field-Trace ID')).toBeInTheDocument();
    });

    // The setter must be a functional updater, or the account tile's single traceSettings
    // object would be clobbered field by field.
    it('updates one field without dropping the rest', () => {
      const setSettings = jest.fn();
      renderTraceMapper({ setSettings });
      fireEvent.change(screen.getByTestId('field-Service name'), { target: { value: 'k8s.service' } });
      expect(setSettings).toHaveBeenCalledTimes(1);
      const updater = setSettings.mock.calls[0][0];
      expect(updater({ workload_name: 'wl' })).toEqual({ workload_name: 'wl', service_name: 'k8s.service' });
    });

    it('disables every input for a read-only viewer but leaves the disclosure usable', () => {
      renderTraceMapper({ settings: { ...EMPTY_TRACE_LABEL_SETTINGS, trace_id: 'traceID' }, disabled: true });
      TRACE_LABEL_FIELDS.forEach(({ label }) => expect(screen.getByTestId(`field-${label}`)).toBeDisabled());
      TRACE_LABEL_ADVANCED_FIELDS.forEach(({ label }) => expect(screen.getByTestId(`field-${label}`)).toBeDisabled());
      expect(screen.getByTestId('trace-label-toggle-advanced')).toBeEnabled();
    });
  });
});
