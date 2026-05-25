import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import TenantAccountCommonSettings from '@components1/common/TenantAccountCommonSettings';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
      disabled: '#9CA3AF',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
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

jest.mock('@components1/common/CustomTextField', () => ({
  __esModule: true,
  default: ({ label, value, placeholder, onChange }) => (
    <div>
      <label>{label}</label>
      <input data-testid={`field-${label}`} value={value} placeholder={placeholder} onChange={onChange} />
    </div>
  ),
}));

jest.mock('@components1/common/TextWithBorder', () => ({
  __esModule: true,
  default: ({ value }) => <div data-testid='text-with-border'>{value}</div>,
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
