import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import TenantAccountCommonSettings from '@shared/settings/TenantAccountCommonSettings';

jest.mock('@utils/colors');

jest.mock('@ui/Input', () => ({
  __esModule: true,
  Input: ({ label, value, placeholder, onChange }) => (
    <div>
      <label>{label}</label>
      <input data-testid={`field-${label}`} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </div>
  ),
}));

describe('TenantAccountCommonSettings', () => {
  const defaultLogSettings = {
    logPodLabel: 'pod',
    logNamespaceLabel: 'namespace',
    logAppLabel: 'app',
    logDefaultQuery: '',
  };

  it('renders the Log Label Mapper heading', () => {
    render(<TenantAccountCommonSettings logSettings={defaultLogSettings} setLogSettings={jest.fn()} />);
    expect(screen.getByText('Log Label Mapper')).toBeInTheDocument();
  });

  it('renders all four field labels', () => {
    render(<TenantAccountCommonSettings logSettings={defaultLogSettings} setLogSettings={jest.fn()} />);
    expect(screen.getByText('Pod')).toBeInTheDocument();
    expect(screen.getByText('Namespace')).toBeInTheDocument();
    expect(screen.getByText('App')).toBeInTheDocument();
    expect(screen.getByText('Default query')).toBeInTheDocument();
  });

  it('renders field inputs with correct values from logSettings', () => {
    render(<TenantAccountCommonSettings logSettings={defaultLogSettings} setLogSettings={jest.fn()} />);
    expect(screen.getByTestId('field-Pod')).toHaveValue('pod');
    expect(screen.getByTestId('field-Namespace')).toHaveValue('namespace');
    expect(screen.getByTestId('field-App')).toHaveValue('app');
  });

  it('calls setLogSettings when a field changes', () => {
    const setLogSettings = jest.fn();
    render(<TenantAccountCommonSettings logSettings={defaultLogSettings} setLogSettings={setLogSettings} />);
    fireEvent.change(screen.getByTestId('field-Pod'), { target: { value: 'new-pod' } });
    expect(setLogSettings).toHaveBeenCalledTimes(1);
  });

  it('renders with empty logSettings without crashing', () => {
    render(<TenantAccountCommonSettings logSettings={{}} setLogSettings={jest.fn()} />);
    expect(screen.getByText('Log Label Mapper')).toBeInTheDocument();
  });

  it('renders inputs with placeholder text', () => {
    render(<TenantAccountCommonSettings logSettings={{}} setLogSettings={jest.fn()} />);
    expect(screen.getByPlaceholderText('Log Pod label')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Log Namespace label')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Log App label')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Default Query')).toBeInTheDocument();
  });

  it('uses empty string as fallback when logSettings field is undefined', () => {
    render(<TenantAccountCommonSettings logSettings={{ logPodLabel: undefined }} setLogSettings={jest.fn()} />);
    expect(screen.getByTestId('field-Pod')).toHaveValue('');
  });
});
